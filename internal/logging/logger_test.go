package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/slog"
	"github.com/gookit/slog/handler"
)

// TestNewBuildsConsoleAndRotatingFileSinks exercises the recommended dual-sink
// wiring: the file sink must land under <dataDir>/logs, receive the log line
// in the configured format, and tolerate a double Close (deferred close +
// graceful shutdown).
func TestNewBuildsConsoleAndRotatingFileSinks(t *testing.T) {
	t.Run("text file is colorless and readable", func(t *testing.T) {
		dataDir := t.TempDir()
		logger, closer, err := New(dataDir, Options{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if closer == nil {
			t.Fatal("dual-sink logger should yield a non-nil closer for the file sink")
		}
		logger.Info().Str("component", "test").Msg("hello")
		if err := closer.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		// Closing twice must not panic/error — the rotate sink is wrapped idempotent.
		if err := closer.Close(); err != nil {
			t.Fatalf("second close: %v", err)
		}
		content, err := os.ReadFile(filepath.Join(dataDir, "logs", logFileName))
		if err != nil {
			t.Fatalf("read log file: %v", err)
		}
		out := strings.TrimSpace(string(content))
		if strings.HasPrefix(out, "{") || !strings.Contains(out, "hello") || !strings.Contains(out, "component") {
			t.Fatalf("text file output unexpected: %q", out)
		}
		if strings.ContainsRune(out, '\x1b') {
			t.Fatalf("text file output must not carry ANSI colors: %q", out)
		}
	})
	t.Run("json file on demand", func(t *testing.T) {
		dataDir := t.TempDir()
		logger, closer, err := New(dataDir, Options{FileFormat: "json"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		logger.Info().Str("component", "test").Msg("hello")
		if err := closer.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		content, err := os.ReadFile(filepath.Join(dataDir, "logs", logFileName))
		if err != nil {
			t.Fatalf("read log file: %v", err)
		}
		if out := strings.TrimSpace(string(content)); !strings.HasPrefix(out, "{") || !strings.Contains(out, `"message":"hello"`) || !strings.Contains(out, `"component":"test"`) {
			t.Fatalf("json file output unexpected: %q", out)
		}
	})
}

// TestLevelGatesBothSinks asserts the configured level filters the file
// sink: at warn, an info line is dropped and a warn line lands.
func TestLevelGatesBothSinks(t *testing.T) {
	dataDir := t.TempDir()
	logger, closer, err := New(dataDir, Options{Level: "warn"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	logger.Info().Msg("dropped")
	logger.Warn().Msg("kept")
	if err := closer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dataDir, "logs", logFileName))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if out := string(content); strings.Contains(out, "dropped") || !strings.Contains(out, "kept") {
		t.Fatalf("level filtering unexpected: %q", out)
	}
}

// TestParseLevel covers config value mapping including fallbacks.
func TestParseLevel(t *testing.T) {
	tests := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.DebugLevel},
		{"INFO", slog.InfoLevel},
		{"", slog.InfoLevel},
		{"warning", slog.WarnLevel},
		{"warn", slog.WarnLevel},
		{"error", slog.ErrorLevel},
		{"nonsense", slog.InfoLevel},
	}
	for _, tt := range tests {
		if got := ParseLevel(tt.in); got != tt.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// TestWriterFormatSelection drives the facade through both formats and asserts
// the on-the-wire difference: json emits a raw JSON object, text emits a
// prettified (non-JSON-object) line. This guards against the format wiring
// silently falling back to the wrong formatter.
func TestWriterFormatSelection(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		logger, buf := newBufferedLogger(slog.NewJSONFormatter())
		logger.Info().Str("k", "v").Msg("ping")
		out := strings.TrimSpace(buf.String())
		if !strings.HasPrefix(out, "{") || !strings.Contains(out, `"message":"ping"`) || !strings.Contains(out, `"k":"v"`) {
			t.Fatalf("json output unexpected: %q", out)
		}
	})
	t.Run("text", func(t *testing.T) {
		logger, buf := newBufferedLogger(slog.NewTextFormatter(textTemplate))
		logger.Info().Str("k", "v").Msg("ping")
		out := strings.TrimSpace(buf.String())
		if !strings.Contains(out, "ping") || strings.HasPrefix(out, "{") {
			t.Fatalf("text output unexpected: %q", out)
		}
	})
}

// TestErrFieldSerializesError exercises the fluent facade's Err chaining, the
// dominant call-site shape for failure logging.
func TestErrFieldSerializesError(t *testing.T) {
	logger, buf := newBufferedLogger(slog.NewJSONFormatter())
	logger.Error().Str("server", "management").Err(errTest).Msg("强制关闭失败")
	out := strings.TrimSpace(buf.String())
	for _, want := range []string{`"message":"强制关闭失败"`, `"server":"management"`, `"error":"boom"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q missing %s", out, want)
		}
	}
}

var errTest = errorString("boom")

type errorString string

func (e errorString) Error() string { return string(e) }

func newBufferedLogger(formatter slog.Formatter) (*Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	h := handler.NewIOWriter(&buf, slog.AllLevels)
	h.SetFormatter(formatter)
	return &Logger{core: newCore(h)}, &buf
}
