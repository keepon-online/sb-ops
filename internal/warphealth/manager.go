package warphealth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Manager 纳管官方 WARP 客户端（cloudflare-warp 软件包 + warp-svc 服务）：
// 状态探测、一键安装（阶段机后台执行，照升级服务先例）与幂等配置序列，
// 把部署文档的手工步骤面板化。执行面是 Go 代码里的**固定命令枚举**，不接受
// 外部输入拼接；面板按 root 部署直接执行（见 docs/deployment.md），非 root
// 部署下这些命令会失败——错误消息如实呈现。节奏常量照仓库约定硬编码。
const (
	// WarpProxyPort 是 proxy 模式的本地 SOCKS5 端口（部署文档既有约定，
	// warp-masque 出站指向它）。
	WarpProxyPort = 40000

	warpPackageName = "cloudflare-warp"
	warpSvcUnit     = "warp-svc"
	warpCliBinary   = "/usr/bin/warp-cli"
	keyringPath     = "/usr/share/keyrings/cloudflare-warp-archive-keyring.gpg"
	aptSourcePath   = "/etc/apt/sources.list.d/cloudflare-client.list"
	pubkeyURL       = "https://pkg.cloudflareclient.com/pubkey.gpg"
	aptRepoLineFmt  = "deb [signed-by=%s] https://pkg.cloudflareclient.com/ %s main\n"

	statusStepTimeout    = 8 * time.Second
	configureStepTimeout = 30 * time.Second
	aptUpdateTimeout     = 3 * time.Minute
	aptInstallTimeout    = 4 * time.Minute
	installPhaseTimeout  = 10 * time.Minute
)

// 安装阶段机的阶段值（照升级服务的阶段快照模式：后台 goroutine 推进，
// 前端轮询 Status 观察）。
const (
	PhaseIdle        = "idle"
	PhaseAddingRepo  = "adding-repo"
	PhaseInstalling  = "installing"
	PhaseConfiguring = "configuring"
	PhaseDone        = "done"
	PhaseFailed      = "failed"
)

// ErrInstallBusy 标记安装阶段机正在进行（端点据此回 409）。
var ErrInstallBusy = errors.New("WARP 客户端安装正在进行中")

// ErrWarpClientNotInstalled 标记客户端未安装（reconfigure 据此回 404）。
var ErrWarpClientNotInstalled = errors.New("WARP 客户端未安装")

// CommandRunner 执行一条系统命令并返回合并输出（装配层注入真实实现、
// 测试注入录制桩）。timeout 由调用方按步骤性质给定。
type CommandRunner func(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error)

// PublicKeyDownloader 下载 Cloudflare 仓库公钥（armored 文本）到指定路径。
type PublicKeyDownloader func(ctx context.Context, url, destination string) error

// ClientStatus 是 WARP 客户端的完整状态视图：系统侧（软件包/服务/注册/
// 模式）+ 面板侧联动（warp-masque 出站存在性、链路判定快照，由装配层
// 适配器填充）+ 安装阶段机。
type ClientStatus struct {
	Installed      bool   `json:"installed"`
	Version        string `json:"version,omitempty"`
	ServiceActive  bool   `json:"serviceActive"`
	ServiceEnabled bool   `json:"serviceEnabled"`
	Registered     bool   `json:"registered"`
	Connected      bool   `json:"connected"`
	ModeProxy      bool   `json:"modeProxy"`
	ProxyPort      int    `json:"proxyPort,omitempty"`

	OutboundConfigured bool       `json:"outboundConfigured"`
	LinkVerdict        string     `json:"linkVerdict,omitempty"`
	LinkCheckedAt      *time.Time `json:"linkCheckedAt,omitempty"`

	Phase      string `json:"phase"`
	PhaseError string `json:"phaseError,omitempty"`
}

type Manager struct {
	mu         sync.Mutex
	phase      string
	phaseError string
	runner     CommandRunner
	download   PublicKeyDownloader
	logError   func(error)
	// 落盘目标（默认取常量；包内测试可覆写到临时目录，避免真实写系统路径）。
	keyringDest   string
	aptSourceDest string
}

func NewManager(runner CommandRunner, download PublicKeyDownloader, logError func(error)) *Manager {
	return &Manager{phase: PhaseIdle, runner: runner, download: download, logError: logError, keyringDest: keyringPath, aptSourceDest: aptSourcePath}
}

// OSCommandRunner 是 CommandRunner 的真实实现。
func OSCommandRunner(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(output), err
}

// DownloadPublicKey 下载仓库公钥到 destination（0644，非空校验）。
func DownloadPublicKey(ctx context.Context, url, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("构造公钥下载请求失败: %w", err)
	}
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("下载 WARP 仓库公钥失败: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("下载 WARP 仓库公钥返回 HTTP %d", response.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	if err != nil {
		return fmt.Errorf("读取 WARP 仓库公钥失败: %w", err)
	}
	if len(content) == 0 {
		return errors.New("WARP 仓库公钥内容为空")
	}
	if err := os.WriteFile(destination, content, 0o644); err != nil {
		return fmt.Errorf("写入 WARP 仓库公钥失败: %w", err)
	}
	return nil
}

// Status 探测系统侧状态。单条命令失败视为该项未知（false），不让整体
// 失败——面板在非 root 或客户端半装状态下也要能渲染。
func (m *Manager) Status(ctx context.Context) ClientStatus {
	m.mu.Lock()
	status := ClientStatus{Phase: m.phase, PhaseError: m.phaseError}
	m.mu.Unlock()
	if out, err := m.runner(ctx, statusStepTimeout, "dpkg-query", "-W", "-f=${Status} | ${Version}", warpPackageName); err == nil {
		status.Installed, status.Version = parseDPkgStatus(out)
	}
	if !status.Installed {
		return status
	}
	if out, err := m.runner(ctx, statusStepTimeout, "systemctl", "is-active", warpSvcUnit); err == nil {
		status.ServiceActive = strings.TrimSpace(out) == "active"
	}
	if out, err := m.runner(ctx, statusStepTimeout, "systemctl", "is-enabled", warpSvcUnit); err == nil {
		status.ServiceEnabled = strings.TrimSpace(out) == "enabled"
	}
	if _, err := m.runner(ctx, statusStepTimeout, warpCliBinary, "--accept-tos", "registration", "show"); err == nil {
		status.Registered = true
	}
	if out, err := m.runner(ctx, statusStepTimeout, warpCliBinary, "--accept-tos", "status"); err == nil {
		status.Connected = parseConnected(out)
	}
	if out, err := m.runner(ctx, statusStepTimeout, warpCliBinary, "--accept-tos", "settings"); err == nil {
		status.ModeProxy, status.ProxyPort = parseProxyMode(out)
	}
	return status
}

// Install 启动后台安装（添加软件源 → 安装软件包 → 配置并连接），立即返回；
// 进度与失败经 Status 的阶段字段观察。安装进行中重复调用回 ErrInstallBusy。
func (m *Manager) Install(ctx context.Context) error {
	m.mu.Lock()
	if m.phase == PhaseAddingRepo || m.phase == PhaseInstalling || m.phase == PhaseConfiguring {
		m.mu.Unlock()
		return ErrInstallBusy
	}
	m.phase, m.phaseError = PhaseAddingRepo, ""
	m.mu.Unlock()
	go m.runInstall()
	return nil
}

// Reconfigure 幂等地执行配置序列（服务自启 + 注册 + proxy 模式 + 端口 +
// 连接），已注册则跳过注册步骤；同步返回。
func (m *Manager) Reconfigure(ctx context.Context) error {
	m.mu.Lock()
	busy := m.phase == PhaseAddingRepo || m.phase == PhaseInstalling || m.phase == PhaseConfiguring
	m.mu.Unlock()
	if busy {
		return ErrInstallBusy
	}
	if out, err := m.runner(ctx, statusStepTimeout, "dpkg-query", "-W", "-f=${Status} | ${Version}", warpPackageName); err != nil {
		return ErrWarpClientNotInstalled
	} else if installed, _ := parseDPkgStatus(out); !installed {
		return ErrWarpClientNotInstalled
	}
	return m.Configure(ctx)
}

// Configure 是安装与重新配置共用的序列：全程幂等，任何一步失败带步骤名
// 返回（错误串中文开头，ST1005）。
func (m *Manager) Configure(ctx context.Context) error {
	if _, err := m.runner(ctx, configureStepTimeout, "systemctl", "enable", "--now", warpSvcUnit); err != nil {
		return fmt.Errorf("启用 warp-svc 服务失败: %w", err)
	}
	if _, err := m.runner(ctx, statusStepTimeout, warpCliBinary, "--accept-tos", "registration", "show"); err != nil {
		if _, err := m.runner(ctx, configureStepTimeout, warpCliBinary, "--accept-tos", "registration", "new"); err != nil {
			return fmt.Errorf("注册 WARP 账户失败: %w", err)
		}
	}
	for _, step := range []struct {
		label string
		args  []string
	}{
		{"切换 proxy 模式", []string{"--accept-tos", "mode", "proxy"}},
		{"设置代理端口", []string{"--accept-tos", "proxy", "port", strconv.Itoa(WarpProxyPort)}},
		{"连接 WARP", []string{"--accept-tos", "connect"}},
	} {
		if _, err := m.runner(ctx, configureStepTimeout, warpCliBinary, step.args...); err != nil {
			return fmt.Errorf("%s失败: %w", step.label, err)
		}
	}
	return nil
}

func (m *Manager) runInstall() {
	ctx, cancel := context.WithTimeout(context.Background(), installPhaseTimeout)
	defer cancel()
	if err := m.addAptRepo(ctx); err != nil {
		m.failPhase(err)
		return
	}
	m.setPhase(PhaseInstalling)
	if _, err := m.runner(ctx, aptUpdateTimeout, "apt-get", "update"); err != nil {
		m.failPhase(fmt.Errorf("刷新软件源索引失败: %w", err))
		return
	}
	if _, err := m.runner(ctx, aptInstallTimeout, "apt-get", "install", "-y", warpPackageName); err != nil {
		m.failPhase(fmt.Errorf("安装 %s 失败: %w", warpPackageName, err))
		return
	}
	m.setPhase(PhaseConfiguring)
	if err := m.Configure(ctx); err != nil {
		m.failPhase(err)
		return
	}
	m.setPhase(PhaseDone)
}

// addAptRepo 添加 Cloudflare 客户端 apt 源：下载公钥（armored）经 gpg
// dearmor 落 keyring，按 /etc/os-release 的发行版代号写 sources 条目。
func (m *Manager) addAptRepo(ctx context.Context) error {
	temporary, err := os.CreateTemp("", "warp-pubkey-*.asc")
	if err != nil {
		return fmt.Errorf("创建公钥临时文件失败: %w", err)
	}
	temporary.Close()
	defer func() { _ = os.Remove(temporary.Name()) }()
	if err := m.download(ctx, pubkeyURL, temporary.Name()); err != nil {
		return err
	}
	if _, err := m.runner(ctx, configureStepTimeout, "gpg", "--yes", "--dearmor", "-o", m.keyringDest, temporary.Name()); err != nil {
		return fmt.Errorf("转换 WARP 仓库公钥失败（系统需预装 gpg）: %w", err)
	}
	codename, err := osReleaseCodename()
	if err != nil {
		return err
	}
	line := fmt.Sprintf(aptRepoLineFmt, m.keyringDest, codename)
	if err := os.WriteFile(m.aptSourceDest, []byte(line), 0o644); err != nil {
		return fmt.Errorf("写入 apt 软件源配置失败: %w", err)
	}
	return nil
}

func (m *Manager) setPhase(phase string) {
	m.mu.Lock()
	m.phase, m.phaseError = phase, ""
	m.mu.Unlock()
}

func (m *Manager) failPhase(err error) {
	m.mu.Lock()
	m.phase, m.phaseError = PhaseFailed, err.Error()
	m.mu.Unlock()
	if m.logError != nil {
		m.logError(err)
	}
}

// parseDPkgStatus 解析 dpkg-query -W -f='${Status} | ${Version}' 输出
// （实测形态：`install ok installed | 2026.7.1377.0`）。
func parseDPkgStatus(output string) (installed bool, version string) {
	before, after, found := strings.Cut(strings.TrimSpace(output), "|")
	if !found || !strings.Contains(before, "installed") || strings.Contains(before, "not-installed") {
		return false, ""
	}
	return true, strings.TrimSpace(after)
}

// parseConnected 解析 warp-cli status 输出（实测形态：`Status update:
// Connected` / `Network: healthy`）。按行尾匹配，避免 Disconnected 误命中。
func parseConnected(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "Connected") {
			return true
		}
	}
	return false
}

var proxyPortPattern = regexp.MustCompile(`port (\d+)`)

// parseProxyMode 解析 warp-cli settings 的 Mode 行（实测形态：
// `(user set)\tMode: WarpProxy on port 40000`）。
func parseProxyMode(output string) (modeProxy bool, port int) {
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "Mode:") {
			continue
		}
		if !strings.Contains(line, "WarpProxy") {
			return false, 0
		}
		if match := proxyPortPattern.FindStringSubmatch(line); match != nil {
			port, _ = strconv.Atoi(match[1])
		}
		return true, port
	}
	return false, 0
}

// osReleaseCodename 读 /etc/os-release 的 VERSION_CODENAME（如 jammy）。
func osReleaseCodename() (string, error) {
	content, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "", fmt.Errorf("读取系统版本信息失败: %w", err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		value, found := strings.CutPrefix(strings.TrimSpace(line), "VERSION_CODENAME=")
		if !found {
			continue
		}
		value = strings.Trim(value, `"`)
		if value != "" {
			return value, nil
		}
	}
	return "", errors.New("系统版本信息缺少发行版代号（VERSION_CODENAME）")
}
