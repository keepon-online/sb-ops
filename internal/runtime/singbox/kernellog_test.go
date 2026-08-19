package singbox

import (
	"testing"

	boxlog "github.com/sagernet/sing-box/log"
)

func TestKernelLogWriterStripsPrefix(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain elapsed prefix",
			input: "INFO[  12] inbound/vless[vless-reality]: tcp server started at [::]:8443",
			want:  "inbound/vless[vless-reality]: tcp server started at [::]:8443",
		},
		{
			name:  "colored level word",
			input: "\x1b[36mINFO\x1b[0m[   3] clash-api: restful api listening at 127.0.0.1:9090",
			want:  "clash-api: restful api listening at 127.0.0.1:9090",
		},
		{
			name:  "connection id segment survives",
			input: "INFO[ 120] [7 1.2s] outbound/direct[direct]: dial tcp 1.2.3.4:443",
			want:  "[7 1.2s] outbound/direct[direct]: dial tcp 1.2.3.4:443",
		},
		{
			name:  "no prefix passes through",
			input: "sing-box started (0.21s)",
			want:  "sing-box started (0.21s)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotLevel, gotMessage string
			(&kernelLogWriter{sink: func(level, message string) {
				gotLevel, gotMessage = level, message
			}}).WriteMessage(boxlog.LevelInfo, tt.input)
			if gotLevel != "info" {
				t.Fatalf("level = %q, want info", gotLevel)
			}
			if gotMessage != tt.want {
				t.Fatalf("message = %q, want %q", gotMessage, tt.want)
			}
		})
	}
}

func TestKernelLogWriterLevels(t *testing.T) {
	tests := []struct {
		level boxlog.Level
		want  string
	}{
		{boxlog.LevelTrace, "debug"},
		{boxlog.LevelDebug, "debug"},
		{boxlog.LevelInfo, "info"},
		{boxlog.LevelWarn, "warn"},
		{boxlog.LevelError, "error"},
		{boxlog.LevelFatal, "error"},
		{boxlog.LevelPanic, "error"},
	}
	for _, tt := range tests {
		if got := kernelLogLevel(tt.level); got != tt.want {
			t.Errorf("kernelLogLevel(%d) = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestKernelLogWriterNilSink(t *testing.T) {
	(&kernelLogWriter{}).WriteMessage(boxlog.LevelError, "INFO[   1] boom")
}

func TestKernelLogWriterEmptyMessage(t *testing.T) {
	called := false
	(&kernelLogWriter{sink: func(level, message string) { called = true }}).WriteMessage(boxlog.LevelInfo, "INFO[   1]  ")
	if called {
		t.Fatal("sink called for empty message")
	}
}
