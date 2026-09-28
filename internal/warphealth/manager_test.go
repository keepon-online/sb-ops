package warphealth

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRunner 按命令名返回预设输出并记录全部调用（夹具按生产实测输出写，
// 勿照解析器改）。
type fakeRunner struct {
	mu      sync.Mutex
	outputs map[string]string
	errors  map[string]error
	calls   [][]string
	gate    chan struct{} // 非空时在该命令上阻塞（测并发 busy）
	gateOn  string
}

func tTempDir() string {
	dir, err := os.MkdirTemp("", "warp-manager-test-*")
	if err != nil {
		panic(err)
	}
	return dir
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{outputs: map[string]string{}, errors: map[string]error{}}
}

func (f *fakeRunner) run(_ context.Context, _ time.Duration, name string, args ...string) (string, error) {
	f.mu.Lock()
	if f.gate != nil && f.gateOn == name {
		f.mu.Unlock()
		<-f.gate
		f.mu.Lock()
	}
	f.calls = append(f.calls, append([]string{name}, args...))
	f.mu.Unlock()
	key := commandKey(name, args...)
	if err, ok := f.errors[key]; ok {
		return f.outputs[key], err
	}
	return f.outputs[key], nil
}

func commandKey(name string, args ...string) string {
	return strings.Join(append([]string{name}, args...), " ")
}

func (f *fakeRunner) calledPrefix(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if strings.HasPrefix(strings.Join(call, " "), prefix) {
			return true
		}
	}
	return false
}

func (f *fakeRunner) called(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if strings.Join(call, " ") == key {
			return true
		}
	}
	return false
}

func (f *fakeRunner) setOutput(key, output string)   { f.outputs[key] = output }
func (f *fakeRunner) setError(key string, err error) { f.errors[key] = err }

func waitPhase(t *testing.T, manager *Manager, phase string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if manager.Status(context.Background()).Phase == phase {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("阶段未在期限内到达 %s（当前 %s）", phase, manager.Status(context.Background()).Phase)
}

func installedRunner() *fakeRunner {
	runner := newFakeRunner()
	runner.setOutput(commandKey("dpkg-query", "-W", "-f=${Status} | ${Version}", warpPackageName), "install ok installed | 2026.7.1377.0")
	runner.setOutput(commandKey("systemctl", "is-active", warpSvcUnit), "active\n")
	runner.setOutput(commandKey("systemctl", "is-enabled", warpSvcUnit), "enabled\n")
	runner.setOutput(commandKey(warpCliBinary, "--accept-tos", "registration", "show"), "Account type: Free\n")
	runner.setOutput(commandKey(warpCliBinary, "--accept-tos", "status"), "Status update: Connected\nNetwork: healthy\n")
	runner.setOutput(commandKey(warpCliBinary, "--accept-tos", "settings"), "Merged configuration:\n(user set)\tMode: WarpProxy on port 40000\n")
	return runner
}

func newTestManager(runner *fakeRunner, downloadErr error) *Manager {
	manager := NewManager(runner.run, func(context.Context, string, string) error { return downloadErr }, nil)
	// 落盘目标改到临时目录，测试不碰真实系统路径。
	dir := tTempDir()
	manager.keyringDest = dir + "/keyring.gpg"
	manager.aptSourceDest = dir + "/cloudflare-client.list"
	runner.setOutput(commandKey("gpg", "--yes", "--dearmor", "-o", manager.keyringDest, "<key>"), "")
	return manager
}

func TestManagerStatusParsesLiveOutputs(t *testing.T) {
	manager := newTestManager(installedRunner(), nil)
	status := manager.Status(context.Background())
	if !status.Installed || status.Version != "2026.7.1377.0" {
		t.Fatalf("软件包解析 = %+v", status)
	}
	if !status.ServiceActive || !status.ServiceEnabled || !status.Registered || !status.Connected {
		t.Fatalf("状态解析 = %+v", status)
	}
	if !status.ModeProxy || status.ProxyPort != 40000 {
		t.Fatalf("模式解析 = %+v", status)
	}
	if status.Phase != PhaseIdle {
		t.Fatalf("初始阶段 = %s", status.Phase)
	}
}

func TestManagerStatusNotInstalledSkipsSystemProbes(t *testing.T) {
	runner := newFakeRunner()
	runner.setError(commandKey("dpkg-query", "-W", "-f=${Status} | ${Version}", warpPackageName), fmt.Errorf("dpkg-query: no packages found matching cloudflare-warp"))
	manager := newTestManager(runner, nil)
	status := manager.Status(context.Background())
	if status.Installed || status.Version != "" {
		t.Fatalf("未安装解析 = %+v", status)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("未安装时应短路系统探测，实际调用 %v", runner.calls)
	}
}

func TestManagerInstallRunsFullSequence(t *testing.T) {
	runner := installedRunner()
	manager := newTestManager(runner, nil)
	if err := manager.Install(context.Background()); err != nil {
		t.Fatalf("启动安装: %v", err)
	}
	waitPhase(t, manager, PhaseDone)
	for _, prefix := range []string{
		"gpg --yes --dearmor -o " + manager.keyringDest,
		"apt-get update",
		"apt-get install -y " + warpPackageName,
		"systemctl enable --now " + warpSvcUnit,
		commandKey(warpCliBinary, "--accept-tos", "mode", "proxy"),
		commandKey(warpCliBinary, "--accept-tos", "proxy", "port", "40000"),
		commandKey(warpCliBinary, "--accept-tos", "connect"),
	} {
		if !runner.calledPrefix(prefix) {
			t.Fatalf("安装序列缺少: %s（实际 %v）", prefix, runner.calls)
		}
	}
}

func TestManagerInstallFailureMarksPhaseFailed(t *testing.T) {
	runner := installedRunner()
	runner.setError("apt-get install -y "+warpPackageName, fmt.Errorf("exit status 100"))
	manager := newTestManager(runner, nil)
	if err := manager.Install(context.Background()); err != nil {
		t.Fatalf("启动安装: %v", err)
	}
	waitPhase(t, manager, PhaseFailed)
	status := manager.Status(context.Background())
	if status.PhaseError == "" || !strings.Contains(status.PhaseError, "安装 cloudflare-warp 失败") {
		t.Fatalf("失败原因 = %q", status.PhaseError)
	}
}

func TestManagerInstallBusyGuard(t *testing.T) {
	runner := installedRunner()
	runner.gate = make(chan struct{})
	runner.gateOn = "apt-get"
	manager := newTestManager(runner, nil)
	if err := manager.Install(context.Background()); err != nil {
		t.Fatalf("启动安装: %v", err)
	}
	if err := manager.Install(context.Background()); err == nil {
		t.Fatal("安装进行中重复调用应返回 ErrInstallBusy")
	}
	close(runner.gate)
	waitPhase(t, manager, PhaseDone)
}

func TestManagerReconfigureRegistersWhenMissing(t *testing.T) {
	runner := installedRunner()
	runner.setError(commandKey(warpCliBinary, "--accept-tos", "registration", "show"), fmt.Errorf("Registration Missing"))
	manager := newTestManager(runner, nil)
	if err := manager.Reconfigure(context.Background()); err != nil {
		t.Fatalf("重新配置: %v", err)
	}
	if !runner.called(commandKey(warpCliBinary, "--accept-tos", "registration", "new")) {
		t.Fatal("未注册时应执行 registration new")
	}
}

func TestManagerReconfigureSkipsRegistrationWhenRegistered(t *testing.T) {
	manager := newTestManager(installedRunner(), nil)
	if err := manager.Reconfigure(context.Background()); err != nil {
		t.Fatalf("重新配置: %v", err)
	}
}

func TestManagerReconfigureRequiresInstalled(t *testing.T) {
	runner := newFakeRunner()
	runner.setError(commandKey("dpkg-query", "-W", "-f=${Status} | ${Version}", warpPackageName), fmt.Errorf("no packages found"))
	manager := newTestManager(runner, nil)
	if err := manager.Reconfigure(context.Background()); err != ErrWarpClientNotInstalled {
		t.Fatalf("期望 ErrWarpClientNotInstalled，得到 %v", err)
	}
}

func TestParseHelpers(t *testing.T) {
	if ok, version := parseDPkgStatus("install ok installed | 2026.7.1377.0\n"); !ok || version != "2026.7.1377.0" {
		t.Fatalf("dpkg = %v %q", ok, version)
	}
	if ok, _ := parseDPkgStatus("unknown ok not-installed | "); ok {
		t.Fatal("not-installed 不应视为已安装")
	}
	if !parseConnected("Status update: Connected\nNetwork: healthy\n") {
		t.Fatal("Connected 未识别")
	}
	if parseConnected("Status update: Disconnected") {
		t.Fatal("Disconnected 不应误判为连接")
	}
	if mode, port := parseProxyMode("(user set)\tMode: WarpProxy on port 40000"); !mode || port != 40000 {
		t.Fatalf("mode = %v %d", mode, port)
	}
	if mode, _ := parseProxyMode("(user set)\tMode: Warp"); mode {
		t.Fatal("TUN 模式不应判为 proxy")
	}
}
