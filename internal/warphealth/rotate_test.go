package warphealth

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// withFixtureTargets 把探测目标指向本地夹具并跳过自签证书校验（避免夹具
// 流量漏到公网），测试结束恢复生产语义。
func withFixtureTargets(t *testing.T, upstream *httptest.Server) {
	t.Helper()
	activeTraceURL, activeGoogleURL = upstream.URL+"/cdn-cgi/trace", upstream.URL+"/"
	activeTLSConfig = &tls.Config{InsecureSkipVerify: true}
	t.Cleanup(func() {
		activeTraceURL, activeGoogleURL = TraceURL, GoogleURL
		activeTLSConfig = nil
	})
}

func TestRotateSuccessUpdatesState(t *testing.T) {
	upstream := newTLSUpstream(t, "ip=2a09:bac5::1\nwarp=on\nloc=US\n", "200", "")
	withFixtureTargets(t, upstream)
	socksAddr := newSocksServer(t, pipeTarget)
	var calls []string
	runWarpCli := func(_ context.Context, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	state := NewState()
	result, err := Rotate(context.Background(), runWarpCli, socksAddr, state)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if result.Verdict != VerdictOK || result.ExitFamily != "ipv6" {
		t.Fatalf("result=%+v", result)
	}
	if len(calls) != 2 || calls[0] != "disconnect" || calls[1] != "connect" {
		t.Fatalf("warp-cli 调用序列=%v", calls)
	}
	if snapshot := state.Snapshot(); snapshot.Verdict != VerdictOK || !snapshot.WarpOn {
		t.Fatalf("state=%+v", snapshot)
	}
}

func TestRotateConnectFailureKeepsState(t *testing.T) {
	runWarpCli := func(_ context.Context, args ...string) error {
		if len(args) == 1 && args[0] == "connect" {
			return errors.New("daemon unreachable")
		}
		return nil
	}
	state := NewState()
	if _, err := Rotate(context.Background(), runWarpCli, "127.0.0.1:1", state); err == nil || !strings.Contains(err.Error(), "重连 WARP 失败") {
		t.Fatalf("err=%v", err)
	}
	if snapshot := state.Snapshot(); snapshot.Verdict != "" {
		t.Fatalf("失败不应改动 state: %+v", snapshot)
	}
}

func TestRotateGradesThroughNotReadyWindow(t *testing.T) {
	upstream := newTLSUpstream(t, "ip=203.0.113.7\nwarp=on\nloc=US\n", "200", "")
	withFixtureTargets(t, upstream)
	// 首个 CONNECT 直接掐断（隧道未就绪的歧义失败），其后照常透传。
	var attempts atomic.Int32
	socksAddr := newSocksServer(t, func(target string, client net.Conn) {
		if attempts.Add(1) == 1 {
			_ = client.Close()
			return
		}
		pipeTarget(target, client)
	})
	runWarpCli := func(context.Context, ...string) error { return nil }
	state := NewState()
	result, err := Rotate(context.Background(), runWarpCli, socksAddr, state)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if result.Verdict != VerdictOK {
		t.Fatalf("result=%+v", result)
	}
	if attempts.Load() < 2 {
		t.Fatalf("应经历未就绪宽限，attempts=%d", attempts.Load())
	}
	if snapshot := state.Snapshot(); snapshot.Verdict != VerdictOK {
		t.Fatalf("state=%+v", snapshot)
	}
}

func TestRotateExhaustedWindowRecordsDefinitiveVerdict(t *testing.T) {
	// 出口仍被误判：每次复测都拿到确定性 misjudged 终态 → 第一轮就成功返回
	// misjudged（终态即返回，不空耗窗口），state 同步更新。
	upstream := newTLSUpstream(t, "ip=203.0.113.7\nwarp=on\nloc=US\n", "302", "https://www.google.com.hk/url?hl=zh-CN")
	withFixtureTargets(t, upstream)
	socksAddr := newSocksServer(t, pipeTarget)
	runWarpCli := func(context.Context, ...string) error { return nil }
	state := NewState()
	result, err := Rotate(context.Background(), runWarpCli, socksAddr, state)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if result.Verdict != VerdictMisjudged {
		t.Fatalf("result=%+v", result)
	}
	if snapshot := state.Snapshot(); snapshot.Verdict != VerdictMisjudged || snapshot.Reason == "" {
		t.Fatalf("state=%+v", snapshot)
	}
}

func TestRotateExhaustedAmbiguousReturnsError(t *testing.T) {
	// 全程歧义失败（隧道黑洞）：宽限窗口耗尽返回错误，state 保持原判定。
	// 收紧单次探测上限与轮询节奏，用例不必烧满生产超时。
	rotateProbeAttempts = 2
	rotateProbeInterval = 10 * time.Millisecond
	probeTimeout = 300 * time.Millisecond
	defer func() {
		rotateProbeAttempts, rotateProbeInterval, probeTimeout = 5, 1500*time.Millisecond, 15*time.Second
	}()

	socksAddr := newSocksServer(t, hangTarget)
	runWarpCli := func(context.Context, ...string) error { return nil }
	state := NewState()
	state.Record(Result{Verdict: VerdictOK, WarpOn: true})
	if _, err := Rotate(context.Background(), runWarpCli, socksAddr, state); err == nil || !strings.Contains(err.Error(), "复测未通过") {
		t.Fatalf("err=%v", err)
	}
	if snapshot := state.Snapshot(); snapshot.Verdict != VerdictOK {
		t.Fatalf("歧义失败不应改动 state: %+v", snapshot)
	}
}
