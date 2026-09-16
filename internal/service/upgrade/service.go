package upgrade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	controlevents "github.com/keepon-online/sb-ops/internal/events"
	"golang.org/x/mod/semver"
)

var (
	ErrNotConfigured = errors.New("升级源未配置（config.yaml 的 updates 段）")
	ErrBusy          = errors.New("已有升级正在进行")
	ErrAlreadyLatest = errors.New("当前已是最新版本")
)

// assetPrefix 是 make release 产物在 Release 资产中的命名前缀，与
// Makefile 的 sb-ops-linux-<arch> 保持一致。
const assetPrefix = "sb-ops-linux-"

// upgradeTimeout 约束脱离请求生命周期的下载+替换流水线（对齐
// api.syncTimeout 的脱离语义，给慢链路下载留足余量）。
const upgradeTimeout = 10 * time.Minute

// Source 返回升级源（GitHub owner/repo 与访问令牌），每次操作现取以
// 跟随 config.yaml 热更新。token 为空表示公开仓库匿名访问。
type Source func() (repo, token string)

// Service 实现面板自升级：检查 GitHub 最新 Release、下载并校验对应架构
// 的二进制、备份当前二进制后原子替换，最后经 restart 钩子触发进程重生。
// 状态仅存内存——进程重启即回到 idle。
type Service struct {
	source         Source
	events         *controlevents.Bus
	github         *githubClient
	now            func() time.Time
	currentVersion string
	restart        func()
	executable     func() (string, error)

	mu        sync.Mutex
	phase     Phase
	latest    *Release
	latestRaw *githubRelease
	failure   string
	updatedAt time.Time
}

func NewService(source Source, currentVersion string, restart func(), events *controlevents.Bus) *Service {
	return &Service{
		source:         source,
		events:         events,
		github:         newGithubClient(),
		now:            func() time.Time { return time.Now().UTC() },
		currentVersion: currentVersion,
		restart:        restart,
		executable:     os.Executable,
		phase:          PhaseIdle,
	}
}

// Status 返回当前快照，永不失败。
func (s *Service) Status() Status {
	repo, _ := s.source()
	s.mu.Lock()
	defer s.mu.Unlock()
	value := Status{
		Configured:     repo != "",
		Repo:           repo,
		CurrentVersion: s.currentVersion,
		Phase:          s.phase,
		Error:          s.failure,
	}
	if !s.updatedAt.IsZero() {
		value.UpdatedAt = s.updatedAt.Unix()
	}
	if s.latest != nil {
		latest := *s.latest
		value.Latest = &latest
	}
	return value
}

// Check 查询最新 Release 并缓存比较结果。升级进行中返回 ErrBusy。
func (s *Service) Check(ctx context.Context) (Release, error) {
	repo, token := s.source()
	if repo == "" {
		return Release{}, ErrNotConfigured
	}
	s.mu.Lock()
	if s.phase.Active() {
		s.mu.Unlock()
		return Release{}, ErrBusy
	}
	s.phase = PhaseChecking
	s.mu.Unlock()

	release, err := s.github.latestRelease(ctx, repo, token)

	s.mu.Lock()
	s.phase = PhaseIdle
	if err == nil {
		latest := newRelease(s.currentVersion, release)
		s.latest = &latest
		s.latestRaw = &release
		s.updatedAt = s.now()
		s.mu.Unlock()
		return latest, nil
	}
	s.mu.Unlock()
	return Release{}, fmt.Errorf("查询最新版本失败: %w", err)
}

// Start 校验守卫后把下载与替换丢到后台执行，立即返回。restart 钩子被
// 调用意味着新二进制已就位、进程即将优雅停机并重生。
func (s *Service) Start(ctx context.Context) error {
	repo, token := s.source()
	if repo == "" {
		return ErrNotConfigured
	}

	s.mu.Lock()
	if s.phase.Active() {
		s.mu.Unlock()
		return ErrBusy
	}
	latest, raw := s.latest, s.latestRaw
	s.mu.Unlock()

	if latest == nil || raw == nil || !latest.Upgradable {
		fresh, err := s.Check(ctx)
		if err != nil {
			return err
		}
		if !fresh.Upgradable {
			return ErrAlreadyLatest
		}
		s.mu.Lock()
		latest, raw = s.latest, s.latestRaw
		s.mu.Unlock()
	}

	s.mu.Lock()
	s.phase = PhaseDownloading
	s.failure = ""
	s.updatedAt = s.now()
	s.mu.Unlock()

	target, assets := *latest, *raw
	go s.run(context.WithoutCancel(ctx), repo, token, target, assets)
	return nil
}

func (s *Service) run(ctx context.Context, repo, token string, target Release, raw githubRelease) {
	ctx, cancel := context.WithTimeout(ctx, upgradeTimeout)
	defer cancel()
	s.publish(ctx, "upgrade.started", target, "")
	if err := s.apply(ctx, repo, token, target, raw); err != nil {
		s.fail(ctx, target, err)
	}
}

func (s *Service) apply(ctx context.Context, repo, token string, target Release, raw githubRelease) error {
	exe, err := s.executable()
	if err != nil {
		return fmt.Errorf("定位面板二进制失败: %w", err)
	}
	assetName := assetPrefix + runtime.GOARCH
	asset, ok := raw.asset(assetName)
	if !ok {
		return fmt.Errorf("Release %s 缺少 %s 资产", target.Version, assetName)
	}

	temp := exe + ".download"
	if err := s.download(ctx, repo, token, asset, temp); err != nil {
		return err
	}
	defer func() {
		// 成功路径 temp 已被 rename 消费；失败路径清理残留。
		if _, statErr := os.Stat(temp); statErr == nil {
			_ = os.Remove(temp)
		}
	}()

	s.setPhase(PhaseVerifying)
	digest, err := s.expectedDigest(ctx, repo, token, raw, asset)
	if err != nil {
		return err
	}
	if err := verifyFile(temp, digest); err != nil {
		return err
	}

	s.setPhase(PhaseApplying)
	if err := backupExecutable(exe); err != nil {
		return fmt.Errorf("备份当前二进制失败: %w", err)
	}
	if err := os.Rename(temp, exe); err != nil {
		return fmt.Errorf("替换面板二进制失败: %w", err)
	}

	s.setPhase(PhaseRestarting)
	slog.Info("面板二进制已替换，触发升级重启", "target", target.Version)
	if s.restart != nil {
		s.restart()
	}
	return nil
}

func (s *Service) download(ctx context.Context, repo, token string, asset releaseAsset, temp string) error {
	file, err := os.OpenFile(temp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if err := s.github.downloadAsset(ctx, repo, token, asset.ID, file); err != nil {
		file.Close()
		_ = os.Remove(temp)
		return fmt.Errorf("下载新版本二进制失败: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	return nil
}

// expectedDigest 优先用 GitHub 为资产自动计算的摘要；缺失时回退解析
// SHA256SUMS 资产（make release 的固定产物）。
func (s *Service) expectedDigest(ctx context.Context, repo, token string, raw githubRelease, asset releaseAsset) (string, error) {
	if asset.Digest != "" {
		return strings.TrimPrefix(asset.Digest, "sha256:"), nil
	}
	sums, ok := raw.asset("SHA256SUMS")
	if !ok {
		return "", fmt.Errorf("Release %s 既无资产摘要也无 SHA256SUMS，无法校验", raw.TagName)
	}
	buffer := &bytes.Buffer{}
	if err := s.github.downloadAsset(ctx, repo, token, sums.ID, buffer); err != nil {
		return "", fmt.Errorf("下载 SHA256SUMS 失败: %w", err)
	}
	digest := digestFromSums(buffer.Bytes(), asset.Name)
	if digest == "" {
		return "", fmt.Errorf("SHA256SUMS 中找不到 %s 的摘要", asset.Name)
	}
	return digest, nil
}

// digestFromSums 解析 sha256sum 产物行「<hex>  <name>」。
func digestFromSums(data []byte, assetName string) string {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName {
			return fields[0]
		}
	}
	return ""
}

func verifyFile(path, digest string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, digest) {
		return fmt.Errorf("sha256 校验失败（期望 %s，实际 %s）", digest, actual)
	}
	return nil
}

// backupExecutable 把当前二进制复制为 <exe>.bak。新版本起不来时 SSH
// 手动 cp 回去即可回滚，面板不做自动回滚。
func backupExecutable(exe string) error {
	info, err := os.Stat(exe)
	if err != nil {
		return err
	}
	source, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer source.Close()
	backup, err := os.OpenFile(exe+".bak", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(backup, source); err != nil {
		backup.Close()
		return err
	}
	return backup.Close()
}

func (s *Service) fail(ctx context.Context, target Release, cause error) {
	message := cause.Error()
	if len(message) > 300 {
		message = message[:300] + "…"
	}
	s.mu.Lock()
	s.phase = PhaseFailed
	s.failure = message
	s.updatedAt = s.now()
	s.mu.Unlock()
	slog.Error("面板升级失败", "target", target.Version, "error", cause)
	s.publish(ctx, "upgrade.failed", target, message)
}

func (s *Service) setPhase(phase Phase) {
	s.mu.Lock()
	s.phase = phase
	s.updatedAt = s.now()
	s.mu.Unlock()
}

func (s *Service) publish(ctx context.Context, eventType string, target Release, failure string) {
	if s.events == nil {
		return
	}
	payload := map[string]any{"current": s.currentVersion, "target": target.Version}
	if failure != "" {
		payload["error"] = failure
	}
	_, _ = s.events.Publish(ctx, controlevents.Input{
		Type: eventType, Scope: "upgrade", ResourceID: target.Version,
		Permission: "settings:read", Payload: payload,
	})
}

// newRelease 把 GitHub 响应收敛为对外缓存。currentVersion 非法（如本地
// dev 构建）时恒为不可升级（见 newer）。
func newRelease(current string, release githubRelease) Release {
	published := int64(0)
	if when, err := time.Parse(time.RFC3339, release.PublishedAt); err == nil {
		published = when.Unix()
	}
	return Release{
		Version:     release.TagName,
		Notes:       release.Body,
		PublishedAt: published,
		Upgradable:  newer(current, release.TagName),
	}
}

// newer 判断 tag 是否比 current 新。current 非法 semver（本地开发构建，
// version 固定为 "dev"）恒为不可升级——开发环境不做升级提示，否则每次
// 检查都会因 "dev" != tag 触发 upgrade_available 告警与页面提示。
func newer(current, tag string) bool {
	if !semver.IsValid(current) {
		return false
	}
	if !semver.IsValid(tag) {
		return current != tag
	}
	return semver.Compare(tag, current) > 0
}
