package singbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	deploymentcompiler "github.com/keepon-online/sb-ops/internal/deployment/compiler"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
	"github.com/sagernet/sing-box/option"
)

func TestCheckRejectsInvalidConfiguration(t *testing.T) {
	manager := New()
	if err := manager.Check(context.Background(), []byte(`{"inbounds":[`)); err == nil {
		t.Fatal("Check() error = nil, want parse error")
	}
}

func TestApplyStartsCompiledShadowsocks2022Configuration(t *testing.T) {
	password := base64.StdEncoding.EncodeToString(make([]byte, 16))
	compiled, err := deploymentcompiler.Compile(deploymentcompiler.Input{
		PublicAddress: "203.0.113.10",
		SingBoxConfig: servicesingbox.Config{
			LogLevel: "info", LogTimestamp: true, DefaultOutboundTag: "direct",
			ClashAPIEnabled: true, ClashAPIListen: "127.0.0.1:19090",
			V2RayAPIEnabled: true, V2RayAPIListen: "127.0.0.1:19000",
		},
		Protocols: []serviceprotocol.Protocol{{
			ID: "shadowsocks", Name: "Shadowsocks 2022", Type: serviceprotocol.TypeShadowsocks,
			Enabled: true, Listen: "::", ListenPort: 18980,
			Settings: map[string]any{"method": "2022-blake3-aes-128-gcm", "password": password},
		}},
	})
	if err != nil {
		t.Fatalf("compile Shadowsocks configuration: %v", err)
	}

	manager := New()
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.Check(t.Context(), compiled.ServerConfig); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if err := manager.Apply(t.Context(), compiled.ServerConfig); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if status := manager.Status(); status.State != "running" {
		t.Fatalf("Status() = %#v, want running", status)
	}
}

func TestApplyRestoresPreviousRuntimeWhenCandidateStartFails(t *testing.T) {
	previous := &fakeRuntime{}
	candidate := &fakeRuntime{startErr: errors.New("candidate unavailable")}
	restored := &fakeRuntime{}
	instances := []runtimeInstance{previous, candidate, restored}
	manager := New()
	manager.newInstance = func(context.Context, option.Options) (runtimeInstance, error) {
		instance := instances[0]
		instances = instances[1:]
		return instance, nil
	}
	first := []byte(`{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`)
	second := []byte(`{"outbounds":[{"type":"block","tag":"block"}],"route":{"final":"block"}}`)
	if err := manager.Apply(t.Context(), first); err != nil {
		t.Fatalf("apply first configuration: %v", err)
	}
	if err := manager.Apply(t.Context(), second); err == nil || !strings.Contains(err.Error(), "candidate unavailable") {
		t.Fatalf("apply replacement error = %v", err)
	}
	if status := manager.Status(); status.State != "running" || !strings.Contains(status.Reason, "candidate unavailable") {
		t.Fatalf("Status() = %#v", status)
	}
	if !bytes.Equal(manager.current, first) || !previous.closed || !candidate.closed || restored.closed {
		t.Fatalf("runtime rollback state is invalid")
	}
}

func TestApplyContinuesWhenPreviousCloseFails(t *testing.T) {
	previous := &fakeRuntime{closeErr: errors.New("close glitch")}
	candidate := &fakeRuntime{}
	instances := []runtimeInstance{previous, candidate}
	manager := New()
	manager.newInstance = func(context.Context, option.Options) (runtimeInstance, error) {
		instance := instances[0]
		instances = instances[1:]
		return instance, nil
	}
	first := []byte(`{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`)
	second := []byte(`{"outbounds":[{"type":"block","tag":"block"}],"route":{"final":"block"}}`)
	if err := manager.Apply(t.Context(), first); err != nil {
		t.Fatalf("apply first configuration: %v", err)
	}
	if err := manager.Apply(t.Context(), second); err != nil {
		t.Fatalf("apply replacement error = %v, want nil", err)
	}
	if status := manager.Status(); status.State != "running" {
		t.Fatalf("Status() = %#v, want running", status)
	}
	if !bytes.Equal(manager.current, second) || !previous.closed || candidate.closed {
		t.Fatalf("runtime state is invalid")
	}
}

type fakeRuntime struct {
	startErr error
	closeErr error
	closed   bool
}

// TestApplyDetachesInstanceFromRequestContext pins the lifecycle contract:
// the kernel instance must not inherit the triggering request's context,
// otherwise every inbound handshake dies the moment the HTTP handler
// returns (listeners keep accepting, all connections abort at 0ms).
func TestApplyDetachesInstanceFromRequestContext(t *testing.T) {
	var instanceContext context.Context
	manager := New()
	manager.newInstance = func(ctx context.Context, options option.Options) (runtimeInstance, error) {
		instanceContext = ctx
		return &fakeRuntime{}, nil
	}
	request, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Apply(request, []byte(`{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	cancel()
	if instanceContext == nil {
		t.Fatal("newInstance was not called")
	}
	if err := instanceContext.Err(); err != nil {
		t.Fatalf("实例 context 被请求取消连带取消: %v", err)
	}
}

func (r *fakeRuntime) Start() error { return r.startErr }
func (r *fakeRuntime) Close() error {
	r.closed = true
	return r.closeErr
}

func TestCloseIsIdempotent(t *testing.T) {
	manager := New()
	if err := manager.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestCloseIgnoresWrappedClosedNetworkError(t *testing.T) {
	manager := New()
	manager.instance = &fakeRuntime{closeErr: fmt.Errorf("close v2ray server: %w", net.ErrClosed)}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// TestApplySeversTrackedClientConnections 钉死热重启的关闭语义：换内核实例时，
// 旧实例上已建立的客户端连接必须被切断。sing-box 的 ConnectionManager 只在
// 出站拨号侧登记，客户端 socket 若不被主动关闭会挂在已关闭的旧实例上，后续
// 每个请求都撞 dns router closed，直到客户端自行重连（线上表现为每次内核
// 同步让长连接客户端断流数分钟）。
func TestApplySeversTrackedClientConnections(t *testing.T) {
	origin, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen origin: %v", err)
	}
	defer origin.Close()
	go func() {
		for {
			conn, acceptErr := origin.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				_, _ = io.Copy(io.Discard, conn)
				_ = conn.Close()
			}()
		}
	}()

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve inbound port: %v", err)
	}
	inboundPort := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	config := fmt.Sprintf(`{
		"log": {"level": "error"},
		"inbounds": [{"type": "mixed", "tag": "in", "listen": "127.0.0.1", "listen_port": %d}],
		"outbounds": [{"type": "direct", "tag": "direct"}],
		"route": {"final": "direct"}
	}`, inboundPort)

	// 面板恒设 PlatformLogWriter（日志 sink），由此强制创建 trafficcontrol
	// 管理器——切断逻辑依赖它，测试环境对齐生产装配。
	manager := New(func(level, message string) {})
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.Apply(context.Background(), []byte(config)); err != nil {
		t.Fatalf("apply first configuration: %v", err)
	}

	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", inboundPort))
	if err != nil {
		t.Fatalf("dial inbound: %v", err)
	}
	defer client.Close()
	originPort := origin.Addr().(*net.TCPAddr).Port
	connect := fmt.Sprintf("CONNECT 127.0.0.1:%d HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n", originPort, originPort)
	if _, err := client.Write([]byte(connect)); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	header := make([]byte, 0, 256)
	chunk := make([]byte, 128)
	for !bytes.Contains(header, []byte("\r\n\r\n")) {
		n, readErr := client.Read(chunk)
		if readErr != nil {
			t.Fatalf("read CONNECT reply: %v (%q)", readErr, header)
		}
		header = append(header, chunk[:n]...)
	}
	if !bytes.Contains(header, []byte("200")) {
		t.Fatalf("CONNECT 未建立: %q", header)
	}

	// 重放同一配置触发 close→start 换实例。
	if err := manager.Apply(context.Background(), []byte(config)); err != nil {
		t.Fatalf("re-apply configuration: %v", err)
	}
	if status := manager.Status(); status.State != "running" {
		t.Fatalf("Status() = %#v, want running", status)
	}

	// 旧实例的客户端连接必须已被关闭：读到 EOF/RST，而不是 3 秒后仍超时在读。
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.Read(chunk); err == nil {
		t.Fatal("换实例后旧客户端连接仍可读：未被切断")
	} else if errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("旧客户端连接未被关闭（读超时）: %v", err)
	}
}
