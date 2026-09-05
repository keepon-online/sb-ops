package config

import (
	"os"
	"path/filepath"
	"strings"
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
probe:
  target_url: " https://example.com/health "
  interval: "2m"
certificates:
  expiry_warning: "48h"
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
	if cfg.Probe.TargetURL != "https://example.com/health" || cfg.Probe.Interval != 2*time.Minute {
		t.Fatalf("probe config = %+v", cfg.Probe)
	}
	if cfg.Certificates.ExpiryWarning != 48*time.Hour {
		t.Fatalf("certificate expiry warning = %v", cfg.Certificates.ExpiryWarning)
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
	if normalized.Probe.TargetURL != DefaultRuntimeConfig().Probe.TargetURL || normalized.Probe.Interval != DefaultRuntimeConfig().Probe.Interval {
		t.Fatalf("probe defaults = %+v", normalized.Probe)
	}
	if normalized.Certificates.ExpiryWarning != DefaultRuntimeConfig().Certificates.ExpiryWarning {
		t.Fatalf("certificate defaults = %+v", normalized.Certificates)
	}
	for _, target := range []string{"", "file:///tmp/x", "javascript:alert(1)", "https://"} {
		candidate := normalized
		candidate.Probe.TargetURL = target
		candidate = NormalizeRuntimeConfig(candidate)
		if target == "" {
			continue
		}
		if err := ValidateRuntimeConfig(candidate); err == nil {
			t.Fatalf("target URL %q should fail validation", target)
		}
	}
}

func TestProbeAndCertificateChangesAreImmediate(t *testing.T) {
	previous := DefaultRuntimeConfig()
	next := previous
	next.Probe.TargetURL = "https://example.com/health"
	next.Probe.Interval = 2 * time.Minute
	next.Certificates.ExpiryWarning = 14 * 24 * time.Hour
	summary := ClassifyRuntimeConfigChanges(previous, next)
	if len(summary.Immediate) != 2 || summary.Immediate[0] != "probe" || summary.Immediate[1] != "certificates.expiry_warning" {
		t.Fatalf("summary=%+v", summary)
	}
	if len(summary.RestartRequired) != 0 {
		t.Fatalf("probe changes should not require restart: %+v", summary.RestartRequired)
	}
}

func TestUpdatesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	atomicWriteConfig(t, path, []byte(`
updates:
  repo: " keepon-online/sb-ops "
  token: " ghp_test "
`))
	cfg, _, err := LoadRuntimeConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Updates.Repo != "keepon-online/sb-ops" || cfg.Updates.Token != "ghp_test" {
		t.Fatalf("updates normalize unexpected: %+v", cfg.Updates)
	}

	// Absent section decodes to the zero value — upgrades stay disabled.
	atomicWriteConfig(t, path, []byte("server:\n  listen: \"127.0.0.1:8030\"\n"))
	cfg, _, err = LoadRuntimeConfig(path)
	if err != nil {
		t.Fatalf("load without updates: %v", err)
	}
	if (cfg.Updates != UpdatesConfig{}) {
		t.Fatalf("absent updates should be zero value, got %+v", cfg.Updates)
	}

	base := NormalizeRuntimeConfig(DefaultRuntimeConfig())
	for _, repo := range []string{"sb-ops", "owner/repo/extra", "/sb-ops", "owner/"} {
		base.Updates = UpdatesConfig{Repo: repo}
		if err := ValidateRuntimeConfig(base); err == nil {
			t.Fatalf("repo %q should fail validation", repo)
		}
	}
	base.Updates = UpdatesConfig{Repo: "keepon-online/sb-ops", Token: "t"}
	if err := ValidateRuntimeConfig(base); err != nil {
		t.Fatalf("valid repo should pass: %v", err)
	}

	summary := ClassifyRuntimeConfigChanges(RuntimeConfig{}, RuntimeConfig{Updates: UpdatesConfig{Repo: "a/b"}})
	if len(summary.Immediate) != 1 || summary.Immediate[0] != "updates" {
		t.Fatalf("updates change should classify as immediate, got %+v", summary)
	}
}

func TestSolusVMConfigAllOrNothingAndHTTPSOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	write := func(content string) {
		atomicWriteConfig(t, path, []byte(content))
	}

	// 三字段齐全 + HTTPS：通过并归一化空白。
	write(`
server:
  listen: "127.0.0.1:8030"
solusvm:
  endpoint: " https://nerdvm.racknerd.com/api/client/command.php "
  api_key: " key-1 "
  api_hash: " hash-1 "
`)
	cfg, _, err := LoadRuntimeConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.SolusVM.Endpoint != "https://nerdvm.racknerd.com/api/client/command.php" || cfg.SolusVM.APIKey != "key-1" || cfg.SolusVM.APIHash != "hash-1" {
		t.Fatalf("solusvm normalize unexpected: %+v", cfg.SolusVM)
	}

	// 缺一不可：只给 endpoint 应被校验拒绝。
	write(`
server:
  listen: "127.0.0.1:8030"
solusvm:
  endpoint: "https://nerdvm.racknerd.com/api/client/command.php"
`)
	if _, _, err := LoadRuntimeConfig(path); err == nil || !strings.Contains(err.Error(), "solusvm") {
		t.Fatalf("残缺配置应被拒绝，got err=%v", err)
	}

	// 凭据不得明文走 HTTP。
	write(`
server:
  listen: "127.0.0.1:8030"
solusvm:
  endpoint: "http://nerdvm.racknerd.com/api/client/command.php"
  api_key: "key-1"
  api_hash: "hash-1"
`)
	if _, _, err := LoadRuntimeConfig(path); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("HTTP endpoint 应被拒绝，got err=%v", err)
	}
}

func TestSolusVMChangesAreImmediate(t *testing.T) {
	previous := DefaultRuntimeConfig()
	next := previous
	next.SolusVM.Endpoint = "https://nerdvm.racknerd.com/api/client/command.php"
	next.SolusVM.APIKey = "key-1"
	next.SolusVM.APIHash = "hash-1"
	summary := ClassifyRuntimeConfigChanges(previous, next)
	if len(summary.Immediate) != 1 || summary.Immediate[0] != "solusvm" {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestSubscriptionURLBaseValidateAndNormalize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	atomicWriteConfig(t, path, []byte(`
server:
  listen: "127.0.0.1:8030"
deployment:
  subscription_url_base: " https://sub.example.com/ "
`))
	cfg, _, err := LoadRuntimeConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Deployment.SubscriptionURLBase != "https://sub.example.com" {
		t.Fatalf("subscription_url_base 归一化异常: %q", cfg.Deployment.SubscriptionURLBase)
	}

	base := NormalizeRuntimeConfig(DefaultRuntimeConfig())
	for _, value := range []string{"sub.example.com", "sub.example.com:8443", "ftp://sub.example.com", "https://", "://"} {
		base.Deployment.SubscriptionURLBase = value
		if err := ValidateRuntimeConfig(base); err == nil {
			t.Fatalf("subscription_url_base %q 应校验失败", value)
		}
	}
	base.Deployment.SubscriptionURLBase = "https://sub.example.com:8443"
	if err := ValidateRuntimeConfig(base); err != nil {
		t.Fatalf("合法 subscription_url_base 应通过校验: %v", err)
	}
	base.Deployment.SubscriptionURLBase = ""
	if err := ValidateRuntimeConfig(base); err != nil {
		t.Fatalf("留空 subscription_url_base 应通过校验: %v", err)
	}
}
