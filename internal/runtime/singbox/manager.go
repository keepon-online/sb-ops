package singbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
)

const Mode = "embedded"

type Status struct {
	State     string
	StartedAt *time.Time
	Version   string
	Reason    string
}

type runtimeInstance interface {
	Start() error
	Close() error
}

type runtimeConstructor func(context.Context, option.Options) (runtimeInstance, error)

type Manager struct {
	mu          sync.Mutex
	instance    runtimeInstance
	current     []byte
	started     *time.Time
	failure     string
	newInstance runtimeConstructor
}

func New(logSinks ...LogSink) *Manager {
	sink := LogSink(nil)
	if len(logSinks) > 0 {
		sink = logSinks[0]
	}
	return &Manager{newInstance: func(ctx context.Context, options option.Options) (runtimeInstance, error) {
		return newRuntimeInstance(ctx, options, sink)
	}}
}

func (m *Manager) Check(ctx context.Context, content []byte) error {
	_, err := parse(ctx, content)
	return err
}

func (m *Manager) Apply(ctx context.Context, content []byte) error {
	options, err := parse(ctx, content)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	previous := append([]byte(nil), m.current...)
	// A failed close only means the discarded instance shut down uncleanly;
	// proceed with the new configuration rather than leaving the kernel
	// stopped, and fall back to the previous one if the new one cannot start.
	closeErr := m.closeLocked()
	if err := m.startLocked(options, content); err != nil {
		m.failure = err.Error()
		if len(previous) > 0 {
			if rollbackErr := m.startContentLocked(previous); rollbackErr != nil {
				m.failure = fmt.Sprintf("%v; restore previous core: %v", err, rollbackErr)
				return errors.New(m.failure)
			}
		}
		if closeErr != nil {
			m.failure = fmt.Sprintf("%v; close previous core: %v", err, closeErr)
			return errors.New(m.failure)
		}
		return err
	}
	m.failure = ""
	return nil
}

func (m *Manager) Restart(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.current) == 0 {
		return errors.New("嵌入式 sing-box 尚未应用配置")
	}
	content := append([]byte(nil), m.current...)
	if err := m.closeLocked(); err != nil {
		m.failure = err.Error()
		return err
	}
	if err := m.startContentLocked(content); err != nil {
		m.failure = err.Error()
		return err
	}
	m.failure = ""
	return nil
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := Status{Version: version(), Reason: m.failure}
	if m.instance == nil {
		status.State = "stopped"
		return status
	}
	status.State = "running"
	if m.started != nil {
		value := *m.started
		status.StartedAt = &value
	}
	return status
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeLocked()
}

func (m *Manager) closeLocked() error {
	if m.instance == nil {
		return nil
	}
	err := m.instance.Close()
	m.instance = nil
	m.started = nil
	if isClosedNetworkError(err) {
		return nil
	}
	return err
}

func isClosedNetworkError(err error) bool {
	if err == nil || errors.Is(err, os.ErrClosed) || errors.Is(err, net.ErrClosed) {
		return true
	}
	return strings.Contains(err.Error(), "use of closed network connection")
}

func (m *Manager) startContentLocked(content []byte) error {
	options, err := parse(context.Background(), content)
	if err != nil {
		return err
	}
	return m.startLocked(options, content)
}

// startLocked always builds the instance on a detached context: the kernel
// must outlive the trigger. Creating it with the caller's context (an HTTP
// request from a save handler) cancels every inbound connection the moment
// the handler returns, leaving listeners that accept but abort every
// handshake instantly.
func (m *Manager) startLocked(options option.Options, content []byte) error {
	instance, err := m.newInstance(context.Background(), options)
	if err != nil {
		return fmt.Errorf("创建嵌入式 sing-box: %w", err)
	}
	if err := instance.Start(); err != nil {
		_ = instance.Close()
		// bbolt waits ~10s for the cache-file flock before giving up; the
		// usual holder is another live panel instance (e.g. a restart raced
		// with a slow exit), so name it instead of a bare "timeout".
		if strings.Contains(err.Error(), "initialize cache-file: timeout") {
			return fmt.Errorf("启动嵌入式 sing-box: %w（缓存文件被其他进程占用，可能已有面板实例在运行）", err)
		}
		return fmt.Errorf("启动嵌入式 sing-box: %w", err)
	}
	now := time.Now().UTC()
	m.instance = instance
	m.current = append([]byte(nil), content...)
	m.started = &now
	return nil
}

func newRuntimeInstance(ctx context.Context, options option.Options, sink LogSink) (runtimeInstance, error) {
	boxOptions := box.Options{Context: include.Context(ctx), Options: options}
	// With a sink, kernel lines are forwarded to the panel logger in panel
	// format; sing-box's own writer (hardcoded "+0800 2006-01-02 15:04:05"
	// timestamps on stderr) is pointed at the null device so lines are not
	// logged twice. Setting PlatformLogWriter also force-enables sing-box's
	// cache-file and clash-server services in box.New — both are already
	// compiled into the server config unconditionally.
	if sink != nil && options.Log != nil && !options.Log.Disabled {
		if options.Log.Output == "" {
			options.Log.Output = os.DevNull
		}
		boxOptions.PlatformLogWriter = &kernelLogWriter{sink: sink}
	}
	return box.New(boxOptions)
}

func parse(ctx context.Context, content []byte) (option.Options, error) {
	if len(content) == 0 {
		return option.Options{}, errors.New("嵌入式 sing-box 配置为空")
	}
	var options option.Options
	if err := options.UnmarshalJSONContext(include.Context(ctx), content); err != nil {
		return option.Options{}, fmt.Errorf("解析嵌入式 sing-box 配置: %w", err)
	}
	return options, nil
}

func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "embedded"
	}
	for _, dependency := range info.Deps {
		if dependency.Path == "github.com/sagernet/sing-box" {
			return strings.TrimPrefix(dependency.Version, "v")
		}
	}
	return "embedded"
}
