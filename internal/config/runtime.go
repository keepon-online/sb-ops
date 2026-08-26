package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type RuntimeConfig struct {
	Server        AppServerConfig        `mapstructure:"server"`
	Logging       LoggingConfig          `mapstructure:"logging"`
	SingBox       SingBoxApplyConfig     `mapstructure:"singbox"`
	Traffic       TrafficCollectorConfig `mapstructure:"traffic"`
	Deployment    DeploymentConfig       `mapstructure:"deployment"`
	Notifications NotificationsConfig    `mapstructure:"notifications"`
	Updates       UpdatesConfig          `mapstructure:"updates"`
	Acme          AcmeConfig             `mapstructure:"acme"`
	Panel         PanelConfig            `mapstructure:"panel"`
	Development   DevelopmentConfig      `mapstructure:"development"`
}

// LoggingConfig tunes the dual-sink logger. Level gates both sinks;
// FileFormat picks the file sink's on-disk format — colorless readable
// "text" (default) or "json". RotateMaxMB/RotateBackups drive the file
// sink's size rotation; non-positive values fall back to the defaults.
// All keys take effect after a restart.
type LoggingConfig struct {
	Level         string `mapstructure:"level"`
	FileFormat    string `mapstructure:"file_format"`
	RotateMaxMB   int    `mapstructure:"rotate_max_mb"`
	RotateBackups int    `mapstructure:"rotate_backups"`
}

// SingBoxApplyConfig drives the embedded sing-box runtime. The control plane
// writes generated certificate material to CertificateDir and owns the core
// lifecycle in-process.
type SingBoxApplyConfig struct {
	CertificateDir string `mapstructure:"certificate_dir"`
}

// TrafficCollectorConfig tunes the local embedded sing-box traffic collector.
// The V2Ray stats gRPC source is derived from the panel-managed configuration;
// only the collection cadence and retention are configured here.
type TrafficCollectorConfig struct {
	Interval      time.Duration `mapstructure:"interval"`
	RetentionDays int           `mapstructure:"retention_days"`
}

// DeploymentConfig holds deployment parameters that subscription compilation
// needs: the public address clients dial and optional TLS certificate paths.
// These are startup configuration (config.yaml), not panel-managed settings.
type DeploymentConfig struct {
	PublicAddress string `mapstructure:"public_address"`
	TLSCertPath   string `mapstructure:"tls_cert_path"`
	TLSKeyPath    string `mapstructure:"tls_key_path"`
}

// NotificationsConfig holds outbound alert delivery endpoints. All fields are
// optional: an empty webhook URL and empty Telegram credentials disable that
// channel.
type NotificationsConfig struct {
	WebhookURL       string `mapstructure:"webhook_url"`
	WebhookSecret    string `mapstructure:"webhook_secret"`
	TelegramBotToken string `mapstructure:"telegram_bot_token"`
	TelegramChatID   string `mapstructure:"telegram_chat_id"`
}

// UpdatesConfig points the panel at the GitHub repository that hosts its own
// release artifacts. All fields are optional: an empty repo disables in-panel
// upgrades. The token is a PAT with read access to the (possibly private)
// repository and is read live on every upgrade operation.
type UpdatesConfig struct {
	Repo  string `mapstructure:"repo"`
	Token string `mapstructure:"token"`
}

// AcmeConfig holds the Let's Encrypt (ACME) settings used to issue and renew
// trusted certificates for a domain. DNS-01 via Cloudflare is the only
// challenge method: it requires no open port and never conflicts with the proxy
// inbounds. All fields are optional; ACME issuance is unavailable until they
// are configured.
type AcmeConfig struct {
	Email       string `mapstructure:"email"`
	DNSProvider string `mapstructure:"dns_provider"`
	APIToken    string `mapstructure:"api_token"`
}

type AppServerConfig struct {
	Listen  string `mapstructure:"listen"`
	DataDir string `mapstructure:"data_dir"`
	DBPath  string `mapstructure:"db_path"`
	// HTTP hardening (M1 §3.3). Zero/empty means use the safe built-in default.
	// WriteTimeout is intentionally NOT exposed: setting it would terminate SSE
	// streams (/events) and long polls. Only read/idle sides are tunable.
	// Durations decode from strings ("10s") via viper's mapstructure hook.
	ReadHeaderTimeout time.Duration `mapstructure:"read_header_timeout"`
	ReadTimeout       time.Duration `mapstructure:"read_timeout"`
	IdleTimeout       time.Duration `mapstructure:"idle_timeout"`
	// MaxHeaderBytes bounds request header size; 0 keeps net/http's default.
	MaxHeaderBytes int `mapstructure:"max_header_bytes"`
	// SQLite tuning (M1 §3.1). Zero/empty values fall back to the defaults in
	// Ops SQLite store. mmap_size is in bytes.
	DBMMapSize    int64  `mapstructure:"db_mmap_size"`
	DBSynchronous string `mapstructure:"db_synchronous"`
}

type PanelConfig struct {
	SessionTTL time.Duration `mapstructure:"session_ttl"`
}

// DevelopmentConfig holds development-only toggles. FrontendDevProxy is empty
// by default (embedded dist is served); set it to a Vite dev server URL to
// proxy frontend requests during local development.
type DevelopmentConfig struct {
	FrontendDevProxy    string `mapstructure:"frontend_dev_proxy"`
	AllowPrivateAddress bool   `mapstructure:"allow_private_address"`
}

func DefaultRuntimeConfig() RuntimeConfig {
	cfg := RuntimeConfig{}
	cfg.Server.Listen = "127.0.0.1:8030"
	cfg.Server.DataDir = "./data"
	cfg.Server.DBPath = filepath.Join(cfg.Server.DataDir, "singbox-web.db")
	cfg.Server.ReadHeaderTimeout = 10 * time.Second
	cfg.Server.ReadTimeout = 30 * time.Second
	cfg.Server.IdleTimeout = 120 * time.Second
	cfg.Server.MaxHeaderBytes = 1 << 20
	cfg.Server.DBMMapSize = 268435456 // 256 MB
	cfg.Server.DBSynchronous = "NORMAL"
	cfg.Logging.Level = "info"
	cfg.Logging.FileFormat = "text"
	cfg.Logging.RotateMaxMB = 20
	cfg.Logging.RotateBackups = 5
	cfg.SingBox.CertificateDir = ""
	cfg.Traffic.Interval = time.Minute
	cfg.Traffic.RetentionDays = 90
	cfg.Panel.SessionTTL = 24 * time.Hour
	return cfg
}

func NormalizeRuntimeConfig(cfg RuntimeConfig) RuntimeConfig {
	defaults := DefaultRuntimeConfig()

	cfg.Server.Listen = strings.TrimSpace(cfg.Server.Listen)
	cfg.Server.DataDir = strings.TrimSpace(cfg.Server.DataDir)
	cfg.Server.DBPath = strings.TrimSpace(cfg.Server.DBPath)
	cfg.SingBox.CertificateDir = strings.TrimSpace(cfg.SingBox.CertificateDir)
	if cfg.SingBox.CertificateDir == "" {
		dataDir := cfg.Server.DataDir
		if dataDir == "" {
			dataDir = defaults.Server.DataDir
		}
		cfg.SingBox.CertificateDir = filepath.Join(dataDir, "runtime", "certificates")
	}
	// A non-positive interval would make time.NewTicker panic; HTTP timeouts
	// keep net/http's "0 = no timeout" semantics and are not coerced.
	if cfg.Traffic.Interval <= 0 {
		cfg.Traffic.Interval = defaults.Traffic.Interval
	}
	cfg.Deployment.PublicAddress = strings.TrimSpace(cfg.Deployment.PublicAddress)
	cfg.Deployment.TLSCertPath = strings.TrimSpace(cfg.Deployment.TLSCertPath)
	cfg.Deployment.TLSKeyPath = strings.TrimSpace(cfg.Deployment.TLSKeyPath)
	cfg.Notifications.WebhookURL = strings.TrimSpace(cfg.Notifications.WebhookURL)
	cfg.Notifications.WebhookSecret = strings.TrimSpace(cfg.Notifications.WebhookSecret)
	cfg.Notifications.TelegramBotToken = strings.TrimSpace(cfg.Notifications.TelegramBotToken)
	cfg.Notifications.TelegramChatID = strings.TrimSpace(cfg.Notifications.TelegramChatID)
	cfg.Updates.Repo = strings.TrimSpace(cfg.Updates.Repo)
	cfg.Updates.Token = strings.TrimSpace(cfg.Updates.Token)
	cfg.Acme.Email = strings.TrimSpace(cfg.Acme.Email)
	cfg.Acme.DNSProvider = strings.ToLower(strings.TrimSpace(cfg.Acme.DNSProvider))
	cfg.Acme.APIToken = strings.TrimSpace(cfg.Acme.APIToken)
	if cfg.Acme.DNSProvider == "" {
		cfg.Acme.DNSProvider = "cloudflare"
	}
	if cfg.Server.DBPath == "" {
		dataDir := cfg.Server.DataDir
		if dataDir == "" {
			dataDir = defaults.Server.DataDir
		}
		cfg.Server.DBPath = filepath.Join(dataDir, "singbox-web.db")
	}

	cfg.Logging.Level = strings.ToLower(strings.TrimSpace(cfg.Logging.Level))
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = defaults.Logging.Level
	}
	cfg.Logging.FileFormat = strings.ToLower(strings.TrimSpace(cfg.Logging.FileFormat))
	if cfg.Logging.FileFormat == "" {
		cfg.Logging.FileFormat = defaults.Logging.FileFormat
	}
	if cfg.Logging.RotateMaxMB <= 0 {
		cfg.Logging.RotateMaxMB = defaults.Logging.RotateMaxMB
	}
	if cfg.Logging.RotateBackups <= 0 {
		cfg.Logging.RotateBackups = defaults.Logging.RotateBackups
	}
	if cfg.Panel.SessionTTL <= 0 {
		cfg.Panel.SessionTTL = defaults.Panel.SessionTTL
	}

	cfg.Development.FrontendDevProxy = strings.TrimSpace(cfg.Development.FrontendDevProxy)

	return cfg
}

func ValidateRuntimeConfig(cfg RuntimeConfig) error {
	if strings.TrimSpace(cfg.Server.Listen) == "" {
		return fmt.Errorf("必须配置 server.listen")
	}
	if strings.TrimSpace(cfg.SingBox.CertificateDir) == "" {
		return fmt.Errorf("必须配置 singbox.certificate_dir")
	}
	if strings.TrimSpace(cfg.Server.DBPath) == "" {
		return fmt.Errorf("必须配置 server.db_path")
	}
	if strings.TrimSpace(cfg.Server.DataDir) == "" {
		return fmt.Errorf("必须配置 server.data_dir")
	}
	switch strings.ToUpper(strings.TrimSpace(cfg.Server.DBSynchronous)) {
	case "", "NORMAL", "FULL":
	default:
		return fmt.Errorf("server.db_synchronous 必须是 NORMAL 或 FULL")
	}
	if cfg.Development.FrontendDevProxy != "" {
		if _, err := url.ParseRequestURI(cfg.Development.FrontendDevProxy); err != nil {
			return fmt.Errorf("development.frontend_dev_proxy 无效: %w", err)
		}
	}
	if cfg.Updates.Repo != "" && !validGitHubRepo(cfg.Updates.Repo) {
		return fmt.Errorf("updates.repo 必须是 owner/repo 形式的 GitHub 仓库标识")
	}
	return nil
}

// validGitHubRepo accepts "owner/repo" with neither part empty and no extra
// slashes, scheme prefixes or trailing whitespace (already normalized).
func validGitHubRepo(repo string) bool {
	owner, name, found := strings.Cut(repo, "/")
	if !found {
		return false
	}
	return owner != "" && name != "" && !strings.Contains(name, "/")
}
