package singbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

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
