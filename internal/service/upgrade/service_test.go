package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub 模拟 Releases API：latest 端点返回 release JSON，资产端点
// 按 ID 回放预置内容；status 非零时 latest 直接失败。
type fakeGitHub struct {
	mu      sync.Mutex
	release githubRelease
	bodies  map[int64][]byte
	status  int
}

func (f *fakeGitHub) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			if f.status != 0 {
				w.WriteHeader(f.status)
				_, _ = w.Write([]byte(`{"message":"内部错误"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(f.release)
		case strings.Contains(r.URL.Path, "/releases/assets/"):
			id := 0
			if _, err := fmt.Sscanf(filepath.Base(r.URL.Path), "%d", &id); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			body, ok := f.bodies[int64(id)]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

type testRig struct {
	service  *Service
	fake     *fakeGitHub
	exe      string
	restarts chan struct{}
}

func newTestRig(t *testing.T, current string, fake *fakeGitHub) *testRig {
	t.Helper()
	server := httptest.NewServer(fake.handler())
	t.Cleanup(server.Close)

	exe := filepath.Join(t.TempDir(), "sb-ops")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("write fake exe: %v", err)
	}
	restarts := make(chan struct{}, 1)
	service := NewService(func() (string, string) { return "owner/repo", "token" }, current, func() {
		restarts <- struct{}{}
	}, nil)
	service.github.apiBase = server.URL
	service.executable = func() (string, error) { return exe, nil }
	return &testRig{service: service, fake: fake, exe: exe, restarts: restarts}
}

// setBinary 构造一个带单资产二进制的 release；withDigest 为 false 时
// 摘要改由 SHA256SUMS 资产提供（digest 字段留空）。
func (f *fakeGitHub) setBinary(t *testing.T, tag string, content []byte, withDigest bool) {
	t.Helper()
	assetName := assetPrefix + runtime.GOARCH
	digest := sha256.Sum256(content)
	release := githubRelease{
		TagName:     tag,
		Body:        "release notes for " + tag,
		PublishedAt: "2026-08-20T15:36:19Z",
		Assets:      []releaseAsset{{ID: 1, Name: assetName}},
	}
	if withDigest {
		release.Assets[0].Digest = "sha256:" + hex.EncodeToString(digest[:])
	} else {
		release.Assets = append(release.Assets, releaseAsset{ID: 2, Name: "SHA256SUMS"})
		f.bodies[2] = []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(digest[:]), assetName))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.release = release
	f.bodies[1] = content
}

func waitPhase(t *testing.T, s *Service, phase Phase) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.Status().Phase == phase {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("phase %s 未到达，当前 %s", phase, s.Status().Phase)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestCheckParsesReleaseAndCompares(t *testing.T) {
	rig := newTestRig(t, "v0.2.0", &fakeGitHub{bodies: map[int64][]byte{}})
	rig.fake.setBinary(t, "v0.3.0", []byte("new"), true)

	release, err := rig.service.Check(context.Background())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if release.Version != "v0.3.0" || !release.Upgradable {
		t.Fatalf("release = %+v", release)
	}
	if release.Notes == "" || release.PublishedAt == 0 {
		t.Fatalf("release notes/published missing: %+v", release)
	}

	status := rig.service.Status()
	if status.Phase != PhaseIdle || status.Latest == nil || !status.Latest.Upgradable {
		t.Fatalf("status after check = %+v", status)
	}
	if !status.Configured || status.Repo != "owner/repo" || status.CurrentVersion != "v0.2.0" {
		t.Fatalf("status source info = %+v", status)
	}
}

func TestCheckOlderTagNotUpgradable(t *testing.T) {
	rig := newTestRig(t, "v0.2.0", &fakeGitHub{bodies: map[int64][]byte{}})
	rig.fake.setBinary(t, "v0.1.0", []byte("old"), true)

	release, err := rig.service.Check(context.Background())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if release.Upgradable {
		t.Fatalf("older tag should not be upgradable: %+v", release)
	}
	if err := rig.service.Start(context.Background()); err != ErrAlreadyLatest {
		t.Fatalf("start should reject latest, got %v", err)
	}
}

func TestCheckDevBuildNotUpgradable(t *testing.T) {
	rig := newTestRig(t, "dev", &fakeGitHub{bodies: map[int64][]byte{}})
	rig.fake.setBinary(t, "v0.3.0", []byte("new"), true)

	release, err := rig.service.Check(context.Background())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if release.Upgradable {
		t.Fatalf("dev build should never prompt upgrade: %+v", release)
	}
	if err := rig.service.Start(context.Background()); err != ErrAlreadyLatest {
		t.Fatalf("start should reject dev build, got %v", err)
	}
}

func TestCheckRequiresConfiguration(t *testing.T) {
	service := NewService(func() (string, string) { return "", "" }, "v0.2.0", nil, nil)
	if _, err := service.Check(context.Background()); err != ErrNotConfigured {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
	if err := service.Start(context.Background()); err != ErrNotConfigured {
		t.Fatalf("start expected ErrNotConfigured, got %v", err)
	}
	if status := service.Status(); status.Configured {
		t.Fatal("empty repo must report unconfigured")
	}
}

func TestCheckPropagatesGitHubError(t *testing.T) {
	rig := newTestRig(t, "v0.2.0", &fakeGitHub{bodies: map[int64][]byte{}, status: 500})
	_, err := rig.service.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "查询最新版本失败") {
		t.Fatalf("expected wrapped check error, got %v", err)
	}
	if status := rig.service.Status(); status.Phase != PhaseIdle {
		t.Fatalf("failed check must return to idle, got %s", status.Phase)
	}
}

func TestStartReplacesBinaryBackupAndRestart(t *testing.T) {
	rig := newTestRig(t, "v0.2.0", &fakeGitHub{bodies: map[int64][]byte{}})
	rig.fake.setBinary(t, "v0.3.0", []byte("new-binary"), true)

	if err := rig.service.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitPhase(t, rig.service, PhaseRestarting)

	select {
	case <-rig.restarts:
	default:
		t.Fatal("restart hook not invoked")
	}
	if got := readFile(t, rig.exe); got != "new-binary" {
		t.Fatalf("exe content = %q", got)
	}
	if got := readFile(t, rig.exe+".bak"); got != "old-binary" {
		t.Fatalf("backup content = %q", got)
	}
	if info, err := os.Stat(rig.exe); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("replaced exe must stay executable: %v %v", info, err)
	}
	if _, err := os.Stat(rig.exe + ".download"); !os.IsNotExist(err) {
		t.Fatal("download temp should be consumed by rename")
	}
}

func TestStartFallsBackToSha256Sums(t *testing.T) {
	rig := newTestRig(t, "v0.2.0", &fakeGitHub{bodies: map[int64][]byte{}})
	rig.fake.setBinary(t, "v0.3.0", []byte("sums-verified"), false)

	if err := rig.service.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitPhase(t, rig.service, PhaseRestarting)
	if got := readFile(t, rig.exe); got != "sums-verified" {
		t.Fatalf("exe content = %q", got)
	}
}

func TestStartRejectsTamperedBinary(t *testing.T) {
	rig := newTestRig(t, "v0.2.0", &fakeGitHub{bodies: map[int64][]byte{}})
	rig.fake.setBinary(t, "v0.3.0", []byte("new-binary"), true)
	// 篡改资产内容使摘要不再匹配。
	rig.fake.mu.Lock()
	rig.fake.bodies[1] = []byte("tampered")
	rig.fake.mu.Unlock()

	if err := rig.service.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitPhase(t, rig.service, PhaseFailed)

	status := rig.service.Status()
	if status.Error == "" || !strings.Contains(status.Error, "sha256 校验失败") {
		t.Fatalf("failure should surface digest mismatch, got %q", status.Error)
	}
	if got := readFile(t, rig.exe); got != "old-binary" {
		t.Fatalf("exe must stay untouched, got %q", got)
	}
	if _, err := os.Stat(rig.exe + ".download"); !os.IsNotExist(err) {
		t.Fatal("failed upgrade must clean the download temp")
	}
	select {
	case <-rig.restarts:
		t.Fatal("restart hook must not fire on verification failure")
	default:
	}
	// 失败后允许重试（资产仍被篡改，等待其再次失败以保证后台收尾）。
	if err := rig.service.Start(context.Background()); err != nil {
		t.Fatalf("restart after failure: %v", err)
	}
	waitPhase(t, rig.service, PhaseFailed)
}

func TestStartGuardBusy(t *testing.T) {
	rig := newTestRig(t, "v0.2.0", &fakeGitHub{bodies: map[int64][]byte{}})
	rig.fake.setBinary(t, "v0.3.0", []byte("new"), true)

	if err := rig.service.Start(context.Background()); err != nil {
		t.Fatalf("first start: %v", err)
	}
	waitPhase(t, rig.service, PhaseRestarting)
	if err := rig.service.Start(context.Background()); err != ErrBusy {
		t.Fatalf("second start expected ErrBusy, got %v", err)
	}
	if _, err := rig.service.Check(context.Background()); err != ErrBusy {
		t.Fatalf("check during upgrade expected ErrBusy, got %v", err)
	}
}

func TestStartMissingAssetFails(t *testing.T) {
	rig := newTestRig(t, "v0.2.0", &fakeGitHub{bodies: map[int64][]byte{}})
	rig.fake.mu.Lock()
	rig.fake.release = githubRelease{
		TagName: "v0.3.0",
		Assets:  []releaseAsset{{ID: 9, Name: "not-a-binary"}},
	}
	rig.fake.mu.Unlock()

	if err := rig.service.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitPhase(t, rig.service, PhaseFailed)
	if status := rig.service.Status(); !strings.Contains(status.Error, "缺少 sb-ops-linux-") {
		t.Fatalf("failure should mention missing asset, got %q", status.Error)
	}
}

func TestStartWithoutCacheChecksFirst(t *testing.T) {
	rig := newTestRig(t, "v0.2.0", &fakeGitHub{bodies: map[int64][]byte{}})
	rig.fake.setBinary(t, "v0.3.0", []byte("new"), true)

	// 未先 Check 直接 Start：服务应自行检查并继续升级。
	if err := rig.service.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitPhase(t, rig.service, PhaseRestarting)
	if got := readFile(t, rig.exe); got != "new" {
		t.Fatalf("exe content = %q", got)
	}
}

func TestDigestFromSums(t *testing.T) {
	data := []byte("aaaa  sb-ops-linux-amd64\nbbbb  other-asset\n")
	if got := digestFromSums(data, "sb-ops-linux-amd64"); got != "aaaa" {
		t.Fatalf("digest = %q", got)
	}
	if got := digestFromSums(data, "missing"); got != "" {
		t.Fatalf("missing digest should be empty, got %q", got)
	}
}
