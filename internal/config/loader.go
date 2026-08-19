package config

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

type RuntimeConfigHolder struct {
	mu      sync.RWMutex
	current RuntimeConfig
}

func NewRuntimeConfigHolder(cfg RuntimeConfig) *RuntimeConfigHolder {
	return &RuntimeConfigHolder{current: cfg}
}

func (h *RuntimeConfigHolder) Current() RuntimeConfig {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.current
}

func (h *RuntimeConfigHolder) Update(cfg RuntimeConfig) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.current = cfg
}

func LoadRuntimeConfig(path string) (RuntimeConfig, *viper.Viper, error) {
	path = NormalizeConfigPath(path)
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		return RuntimeConfig{}, nil, err
	}

	// Defaults live only in DefaultRuntimeConfig: the struct is pre-filled and
	// Unmarshal overlays just the keys the file actually carries, so there is
	// no second (dotted-path) copy of the default values to keep in sync.
	cfg := DefaultRuntimeConfig()
	if err := v.Unmarshal(&cfg); err != nil {
		return RuntimeConfig{}, nil, err
	}
	cfg = NormalizeRuntimeConfig(cfg)
	if err := ValidateRuntimeConfig(cfg); err != nil {
		return RuntimeConfig{}, nil, err
	}
	return cfg, v, nil
}

func StartRuntimeConfigWatcher(path string) (*RuntimeConfigHolder, func(), error) {
	path = NormalizeConfigPath(path)
	cfg, v, err := LoadRuntimeConfig(path)
	if err != nil {
		return nil, nil, err
	}
	holder := NewRuntimeConfigHolder(cfg)
	var stopped atomic.Bool

	v.OnConfigChange(func(event fsnotify.Event) {
		// NOTE: this callback deliberately uses log/slog (the stdlib default)
		// rather than the internal/logging facade: config is the bootstrap
		// layer, before the app logger exists. The callback fires only on
		// config-file changes, so its output lands on slog's stderr default.
		if stopped.Load() {
			return
		}
		time.Sleep(25 * time.Millisecond)
		next, _, err := LoadRuntimeConfig(path)
		if err != nil {
			slog.Error("reload runtime config", "path", path, "error", err)
			return
		}
		previous := holder.Current()
		holder.Update(next)
		summary := ClassifyRuntimeConfigChanges(previous, next)
		slog.Info(
			"runtime config reloaded",
			"path", event.Name,
			"immediate", summary.Immediate,
			"next_request", summary.NextRequest,
			"restart_required", summary.RestartRequired,
		)
	})
	v.WatchConfig()

	stop := func() {
		stopped.Store(true)
	}
	return holder, stop, nil
}
