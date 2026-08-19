package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// atomicWriteConfig 通过临时文件 + rename 原子替换目标文件。fsnotify 对
// os.WriteFile 的 truncate+write 会触发多次 WRITE/CLOSE 事件，中途 reload
// 会读到残缺内容（viper 把空文件解析成全默认值并校验通过，污染 holder）。
// rename 只产生单次 RENAME 事件，消除该竞态。
func atomicWriteConfig(t *testing.T, path string, content []byte) {
	t.Helper()
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		t.Fatalf("create temp config: %v", err)
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		t.Fatalf("write temp config: %v", err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("close temp config: %v", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		t.Fatalf("rename temp config: %v", err)
	}
}

func TestWatcherAppliesValidConfigAndKeepsOldOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	initial := []byte("server:\n  listen: \"127.0.0.1:8030\"\npanel:\n  session_ttl: \"24h\"\n")
	atomicWriteConfig(t, path, initial)
	holder, stop, err := StartRuntimeConfigWatcher(path)
	if err != nil {
		t.Fatalf("start watcher: %v", err)
	}
	defer stop()

	updated := []byte("server:\n  listen: \"127.0.0.1:8030\"\npanel:\n  session_ttl: \"12h\"\n")
	atomicWriteConfig(t, path, updated)
	requireEventually(t, func() bool {
		return holder.Current().Panel.SessionTTL == 12*time.Hour
	})

	invalid := []byte("server:\n  listen: \"\"\n")
	atomicWriteConfig(t, path, invalid)
	requireEventually(t, func() bool {
		// invalid reload 应被拒绝；holder 须保持上一个 valid 状态。
		return holder.Current().Panel.SessionTTL == 12*time.Hour
	})
}

func requireEventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("condition not satisfied before timeout")
}

func TestLoggingConfigNormalize(t *testing.T) {
	normalized := NormalizeRuntimeConfig(RuntimeConfig{Logging: LoggingConfig{Level: " WARN ", FileFormat: "JSON"}})
	if normalized.Logging.Level != "warn" || normalized.Logging.FileFormat != "json" {
		t.Fatalf("normalize unexpected: %+v", normalized.Logging)
	}
	empty := NormalizeRuntimeConfig(RuntimeConfig{})
	if empty.Logging.Level != DefaultRuntimeConfig().Logging.Level {
		t.Fatalf("empty level should default to %q, got %q", DefaultRuntimeConfig().Logging.Level, empty.Logging.Level)
	}
	if empty.Logging.FileFormat != DefaultRuntimeConfig().Logging.FileFormat {
		t.Fatalf("empty format should default to %q, got %q", DefaultRuntimeConfig().Logging.FileFormat, empty.Logging.FileFormat)
	}
}

func TestTypedDurationAndLimitsConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	atomicWriteConfig(t, path, []byte(`
server:
  read_timeout: "45s"
  max_header_bytes: 2097152
panel:
  session_ttl: "6h"
traffic:
  interval: "30s"
logging:
  rotate_max_mb: 10
`))
	cfg, _, err := LoadRuntimeConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Server.ReadTimeout != 45*time.Second {
		t.Fatalf("read_timeout = %v", cfg.Server.ReadTimeout)
	}
	if cfg.Server.MaxHeaderBytes != 2097152 {
		t.Fatalf("max_header_bytes = %v", cfg.Server.MaxHeaderBytes)
	}
	if cfg.Panel.SessionTTL != 6*time.Hour {
		t.Fatalf("session_ttl = %v", cfg.Panel.SessionTTL)
	}
	if cfg.Traffic.Interval != 30*time.Second {
		t.Fatalf("traffic.interval = %v", cfg.Traffic.Interval)
	}
	if cfg.Logging.RotateBackups != DefaultRuntimeConfig().Logging.RotateBackups {
		t.Fatalf("unset rotate_backups should default to %d", DefaultRuntimeConfig().Logging.RotateBackups)
	}
	// Absent keys keep the pre-filled defaults (no dotted-path SetDefault copy).
	if cfg.Server.ReadHeaderTimeout != DefaultRuntimeConfig().Server.ReadHeaderTimeout {
		t.Fatalf("absent read_header_timeout = %v", cfg.Server.ReadHeaderTimeout)
	}

	// Invalid durations must fail the load instead of silently falling back.
	atomicWriteConfig(t, path, []byte("server:\n  read_timeout: \"abc\"\n"))
	if _, _, err := LoadRuntimeConfig(path); err == nil {
		t.Fatal("invalid duration should fail config load")
	}

	// Non-positive interval would panic time.NewTicker; normalize to default.
	normalized := NormalizeRuntimeConfig(RuntimeConfig{Traffic: TrafficCollectorConfig{Interval: -1 * time.Second}})
	if normalized.Traffic.Interval != DefaultRuntimeConfig().Traffic.Interval {
		t.Fatalf("interval normalize = %v", normalized.Traffic.Interval)
	}
}
