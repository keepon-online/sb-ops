package warphealth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRestartServiceSuccessUpdatesState(t *testing.T) {
	upstream := newTLSUpstream(t, "ip=2a09:bac5::1\nwarp=on\nloc=US\n", "200", "")
	withFixtureTargets(t, upstream)
	socksAddr := newSocksServer(t, pipeTarget)
	calls := 0
	restart := func(context.Context) error { calls++; return nil }
	state := NewState()
	result, err := RestartService(context.Background(), restart, socksAddr, state)
	if err != nil {
		t.Fatalf("RestartService: %v", err)
	}
	if result.Verdict != VerdictOK || result.ExitFamily != "ipv6" {
		t.Fatalf("result=%+v", result)
	}
	if calls != 1 {
		t.Fatalf("restart 调用次数=%d", calls)
	}
	if snapshot := state.Snapshot(); snapshot.Verdict != VerdictOK || !snapshot.WarpOn {
		t.Fatalf("state=%+v", snapshot)
	}
}

func TestRestartServiceRestartFailureKeepsState(t *testing.T) {
	restart := func(context.Context) error { return errors.New("unit not found") }
	state := NewState()
	if _, err := RestartService(context.Background(), restart, "127.0.0.1:1", state); err == nil || !strings.Contains(err.Error(), "重启 warp-svc 失败") {
		t.Fatalf("err=%v", err)
	}
	if snapshot := state.Snapshot(); snapshot.Verdict != "" {
		t.Fatalf("失败不应改动 state: %+v", snapshot)
	}
}

func TestRestartServiceExhaustedWindowReturnsError(t *testing.T) {
	// 重启后链路仍黑洞：宽限窗口耗尽返回错误，state 保持原判定。
	restartProbeAttempts = 2
	restartProbeInterval = 10 * time.Millisecond
	probeTimeout = 300 * time.Millisecond
	defer func() {
		restartProbeAttempts, restartProbeInterval, probeTimeout = 8, 2*time.Second, 15*time.Second
	}()

	socksAddr := newSocksServer(t, hangTarget)
	restart := func(context.Context) error { return nil }
	state := NewState()
	state.Record(Result{Verdict: VerdictOK, WarpOn: true})
	if _, err := RestartService(context.Background(), restart, socksAddr, state); err == nil || !strings.Contains(err.Error(), "复测未通过") {
		t.Fatalf("err=%v", err)
	}
	if snapshot := state.Snapshot(); snapshot.Verdict != VerdictOK {
		t.Fatalf("歧义失败不应改动 state: %+v", snapshot)
	}
}
