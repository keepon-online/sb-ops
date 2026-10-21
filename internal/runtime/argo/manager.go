// Package argo supervises cloudflared tunnel processes for the panel's Argo
// tunnels. Each enabled tunnel maps to one cloudflared process forwarding a
// local vmess WebSocket inbound through a Cloudflare edge domain; the
// supervisor restarts crashed processes with a minimum interval and reports
// runtime state (assigned domain / status / last error) back to the store.
package argo

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	serviceargo "github.com/keepon-online/sb-ops/internal/service/argo"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
)

// LogFunc routes supervisor messages into the panel logging facade,
// mirroring the kernelLogSink pattern.
type LogFunc func(level, message string)

// tryCloudflarePattern matches the quick-tunnel URL cloudflared prints on
// stderr when running without an account.
var tryCloudflarePattern = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

const (
	reconcileInterval = 15 * time.Second
	restartMinWait    = 10 * time.Second
	downloadTimeout   = 5 * time.Minute
)

// Manager owns all cloudflared processes for the lifetime of the panel.
type Manager struct {
	store      *serviceargo.Store
	protocols  *serviceprotocol.Store
	binaryDir  string
	log        LogFunc
	onChange   func(context.Context)
	now        func() time.Time
	httpClient *http.Client

	mu       sync.Mutex
	running  map[string]*tunnelProcess
	lastStop map[string]time.Time
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

type tunnelProcess struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	// assigned is the domain most recently parsed from process output.
	assigned string
}

// NewManager creates a supervisor. onChange fires (from a separate goroutine)
// whenever a tunnel's assigned domain changes so subscriptions can be
// regenerated.
func NewManager(store *serviceargo.Store, protocols *serviceprotocol.Store, binaryDir string, log LogFunc, onChange func(context.Context)) *Manager {
	return &Manager{
		store: store, protocols: protocols, binaryDir: binaryDir, log: log, onChange: onChange,
		now:        func() time.Time { return time.Now().UTC() },
		httpClient: &http.Client{Timeout: downloadTimeout},
		running:    map[string]*tunnelProcess{}, lastStop: map[string]time.Time{},
	}
}

// Start launches the reconcile loop. It returns immediately; failures are
// recorded per-tunnel in the store and logged.
func (m *Manager) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		cancel()
		return
	}
	m.cancel = cancel
	m.mu.Unlock()
	m.wg.Go(func() {
		ticker := time.NewTicker(reconcileInterval)
		defer ticker.Stop()
		m.safeReconcile(ctx)
		for {
			select {
			case <-ctx.Done():
				m.stopAll()
				return
			case <-ticker.C:
				m.safeReconcile(ctx)
			}
		}
	})
}

// safeReconcile 带 panic 自愈地跑一轮对账：reconcile 触达 cloudflared 下载
// 与进程管理边界，本包与面板/内核同进程，一处 panic 不应带崩全体服务；
// 恢复后等下一个 tick 自然重试（reconcileInterval 即退避），与 ops_app
// goLoop 的循环级自愈同语义（ticker 循环内就地 recover 即可）。
func (m *Manager) safeReconcile(ctx context.Context) {
	defer func() {
		if recovered := recover(); recovered != nil {
			m.log("error", fmt.Sprintf("Argo 对账循环 panic（已恢复，下一轮重试）: %v\n%s", recovered, debug.Stack()))
		}
	}()
	m.reconcile(ctx)
}

// Close stops the reconcile loop and all tunnel processes.
func (m *Manager) Close() {
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
}

// RestartTunnel stops one tunnel immediately; the reconcile loop brings it
// back within reconcileInterval.
func (m *Manager) RestartTunnel(id string) {
	m.mu.Lock()
	proc := m.running[id]
	delete(m.running, id)
	m.lastStop[id] = m.now()
	m.mu.Unlock()
	if proc != nil {
		proc.cancel()
	}
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	processes := m.running
	m.running = map[string]*tunnelProcess{}
	m.mu.Unlock()
	for _, proc := range processes {
		proc.cancel()
	}
	for id := range processes {
		_ = m.store.UpdateRuntime(context.Background(), id, serviceargo.RuntimeUpdate{Status: serviceargo.StatusStopped}, m.now())
	}
}

// reconcile starts enabled tunnels that are not running and stops processes
// whose tunnel disappeared or was disabled.
func (m *Manager) reconcile(ctx context.Context) {
	tunnels, err := m.store.ListRuntime(ctx)
	if err != nil {
		m.log("error", fmt.Sprintf("读取 Argo 隧道失败: %v", err))
		return
	}
	protocols, err := m.protocols.List(ctx)
	if err != nil {
		m.log("error", fmt.Sprintf("读取协议列表失败（Argo）: %v", err))
		return
	}
	ports := map[string]int{}
	for _, protocol := range protocols {
		if protocol.Enabled {
			ports[protocol.ID] = protocol.ListenPort
		}
	}
	desired := map[string]serviceargo.Tunnel{}
	for _, tunnel := range tunnels {
		if tunnel.Enabled && ports[tunnel.ProtocolID] > 0 {
			desired[tunnel.ID] = tunnel
		}
	}

	m.mu.Lock()
	var toStop []string
	for id := range m.running {
		if _, ok := desired[id]; !ok {
			toStop = append(toStop, id)
		}
	}
	running := make(map[string]*tunnelProcess, len(m.running))
	maps.Copy(running, m.running)
	m.mu.Unlock()
	for _, id := range toStop {
		m.RestartTunnel(id) // stop path: removing from running stops it for good until re-enabled
		_ = m.store.UpdateRuntime(ctx, id, serviceargo.RuntimeUpdate{Status: serviceargo.StatusStopped}, m.now())
	}

	for id, tunnel := range desired {
		if running[id] != nil {
			continue
		}
		if time.Since(m.lastStop[id]) < restartMinWait {
			continue
		}
		m.startTunnel(ctx, tunnel, ports[tunnel.ProtocolID])
	}
}

func (m *Manager) startTunnel(ctx context.Context, tunnel serviceargo.Tunnel, port int) {
	binary, err := m.ensureBinary()
	if err != nil {
		m.recordError(ctx, tunnel.ID, fmt.Sprintf("cloudflared 不可用: %v", err))
		return
	}
	var args []string
	if tunnel.Mode == serviceargo.ModeToken {
		args = []string{"tunnel", "run", "--token", tunnel.Token}
	} else {
		args = []string{"tunnel", "--url", fmt.Sprintf("http://127.0.0.1:%d", port), "--no-autoupdate", "--edge-ip-version", "4"}
	}
	procCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(procCtx, binary, args...)
	cmd.Cancel = func() error { return cmd.Process.Kill() }
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		m.recordError(ctx, tunnel.ID, fmt.Sprintf("启动 cloudflared 失败: %v", err))
		return
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		cancel()
		m.recordError(ctx, tunnel.ID, fmt.Sprintf("启动 cloudflared 失败: %v", err))
		return
	}
	proc := &tunnelProcess{cmd: cmd, cancel: cancel}
	m.mu.Lock()
	m.running[tunnel.ID] = proc
	m.mu.Unlock()
	started := m.now()
	_ = m.store.UpdateRuntime(ctx, tunnel.ID, serviceargo.RuntimeUpdate{Status: serviceargo.StatusStarting, StartedAt: &started}, m.now())
	m.log("info", fmt.Sprintf("Argo 隧道已启动: tunnel=%s mode=%s pid=%d", tunnel.Name, tunnel.Mode, cmd.Process.Pid))

	m.wg.Add(2)
	go m.watchOutput(tunnel.ID, tunnel, proc, stdout)
	go func() {
		defer m.wg.Done()
		waitErr := cmd.Wait()
		stdout.Close()
		m.mu.Lock()
		if m.running[tunnel.ID] == proc {
			delete(m.running, tunnel.ID)
		}
		m.lastStop[tunnel.ID] = m.now()
		m.mu.Unlock()
		if procCtx.Err() != nil {
			return // deliberate stop (shutdown / disable / restart)
		}
		m.recordError(context.Background(), tunnel.ID, fmt.Sprintf("cloudflared 异常退出: %v", waitErr))
	}()
}

// watchOutput streams cloudflared output into the panel log, captures the
// quick-tunnel domain for temporary tunnels, and marks token tunnels running
// once cloudflared registers its first edge connection.
func (m *Manager) watchOutput(id string, tunnel serviceargo.Tunnel, proc *tunnelProcess, output io.Reader) {
	defer m.wg.Done()
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		m.log("info", fmt.Sprintf("cloudflared tunnel=%s: %s", tunnel.Name, line))
		if tunnel.Mode == serviceargo.ModeTemporary {
			if match := tryCloudflarePattern.FindString(line); match != "" {
				m.assignDomain(id, tunnel, proc, strings.TrimPrefix(match, "https://"))
			}
			continue
		}
		if strings.Contains(line, "Registered tunnel connection") {
			m.assignDomain(id, tunnel, proc, tunnel.FixedDomain)
		}
	}
}

func (m *Manager) assignDomain(id string, tunnel serviceargo.Tunnel, proc *tunnelProcess, domain string) {
	m.mu.Lock()
	changed := proc.assigned != domain
	proc.assigned = domain
	m.mu.Unlock()
	if !changed {
		return
	}
	now := m.now()
	_ = m.store.UpdateRuntime(context.Background(), id, serviceargo.RuntimeUpdate{
		AssignedDomain: domain, Status: serviceargo.StatusRunning, StartedAt: &now,
	}, now)
	m.log("info", fmt.Sprintf("Argo 隧道域名已确认: tunnel=%s domain=%s", tunnel.Name, domain))
	m.fireChange(id)
}

// fireChange notifies the app that a tunnel domain changed so subscriptions
// are regenerated. Errors are logged by the callback owner.
func (m *Manager) fireChange(tunnelID string) {
	if m.onChange == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		m.onChange(ctx)
	}()
}

func (m *Manager) recordError(ctx context.Context, id, message string) {
	m.log("error", fmt.Sprintf("Argo 隧道错误: id=%s error=%s", id, message))
	_ = m.store.UpdateRuntime(ctx, id, serviceargo.RuntimeUpdate{Status: serviceargo.StatusError, LastError: message}, m.now())
}

// binaryPath is the expected cloudflared location inside the data directory.
func (m *Manager) binaryPath() string {
	name := "cloudflared"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(m.binaryDir, name)
}

// ensureBinary returns the cloudflared path, downloading the release binary
// on first use. Behind restricted networks place a binary manually at
// <data>/runtime/bin/cloudflared(.exe).
func (m *Manager) ensureBinary() (string, error) {
	path := m.binaryPath()
	if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Size() > 0 {
		return path, nil
	}
	if err := os.MkdirAll(m.binaryDir, 0o755); err != nil {
		return "", err
	}
	asset := fmt.Sprintf("cloudflared-%s-amd64", runtime.GOOS)
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}
	url := "https://github.com/cloudflare/cloudflared/releases/latest/download/" + asset
	temp := path + ".download"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	response, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载 cloudflared 失败（可手动放置二进制到 %s）: %w", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载 cloudflared 返回 %d（可手动放置二进制到 %s）", response.StatusCode, path)
	}
	file, err := os.OpenFile(temp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(file, response.Body); err != nil {
		file.Close()
		_ = os.Remove(temp)
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temp, path); err != nil {
		return "", err
	}
	m.log("info", "已下载 cloudflared 二进制: path="+path)
	return path, nil
}
