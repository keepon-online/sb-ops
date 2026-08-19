package singbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestKernelLogsForwardedToSink starts a real embedded core and asserts its
// log lines reach the sink in panel form: sing-box's elapsed prefix stripped,
// no ANSI escapes, level mapped.
func TestKernelLogsForwardedToSink(t *testing.T) {
	var lines []string
	done := make(chan struct{})
	var once bool
	manager := New(func(level, message string) {
		if strings.Contains(message, "sing-box started") && !once {
			once = true
			lines = append(lines, level+"|"+message)
			close(done)
		}
	})
	config := []byte(`{
		"log": {"level": "info", "timestamp": true},
		"inbounds": [{"type": "mixed", "tag": "in", "listen": "127.0.0.1", "listen_port": 39171}],
		"outbounds": [{"type": "direct", "tag": "direct"}]
	}`)
	if err := manager.Apply(context.Background(), config); err != nil {
		t.Fatalf("apply: %v", err)
	}
	defer func() { _ = manager.Close() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("sink did not receive the kernel start line")
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "info|sing-box started") {
		t.Fatalf("lines = %v, want one info line starting with sing-box started", lines)
	}
}
