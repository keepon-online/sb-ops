// Package logging builds the application's structured logger (gookit/slog).
//
// The wiring is a fixed setup for this single-VPS service instead of a
// runtime-configurable surface beyond the logging options: every log
// statement is dispatched to two sinks — a colorized text console on stdout
// for immediate viewing and a size-rotated file under <data_dir>/logs for
// postmortem (colorless text by default, JSON on demand). The process runs
// without systemd/docker orchestration, so it owns its own retention.
//
// gookit/slog has no zerolog-style chained API, so this package exposes a small
// fluent facade (Info().Str().Msg()); an Entry accumulates fields and dispatches
// exactly one gookit record per Msg call. Call sites therefore depend only on
// this package.
package logging

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gookit/color"
	"github.com/gookit/slog"
	"github.com/gookit/slog/handler"
)

// Fixed sink parameters: rotate at 20 MiB, keep 5 backups (~120 MiB worst
// case) next to the SQLite database.
const (
	logFileName   = "sb-control.log"
	logMaxSizeMB  = 20
	logMaxBackups = 5
)

// Options tunes the dual-sink wiring. Level is one of "debug", "info",
// "warn", "error" (empty = info) and gates both sinks; FileFormat is the
// file sink's on-disk format — "text" (colorless, human-readable, the
// default) or "json". RotateMaxMB/RotateBackups drive the file sink's size
// rotation; non-positive values fall back to the package defaults. The
// console sink always stays colored text.
type Options struct {
	Level         string
	FileFormat    string
	RotateMaxMB   int
	RotateBackups int
}

// ParseLevel maps a config string onto the gookit level used to gate both
// sinks. Unknown values fall back to info.
func ParseLevel(value string) slog.Level {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "debug":
		return slog.DebugLevel
	case "warn", "warning":
		return slog.WarnLevel
	case "error":
		return slog.ErrorLevel
	default:
		return slog.InfoLevel
	}
}

// Logger is the application logger, a thin fluent facade over gookit/slog.
// Injecting *Logger is what makes the service/http layers substitutable in
// tests.
type Logger struct {
	core *slog.Logger
}

// Closer is the resource that backs the logger, if any. Callers should Close it
// on shutdown to flush buffered file writes. A nil Closer means the logger
// writes to os.Stdout only and owns no resource.
type Closer interface {
	Close() error
}

// Entry accumulates the structured fields of a single log statement. It is
// dispatched by Msg and must not be reused afterwards.
type Entry struct {
	logger *Logger
	level  slog.Level
	fields slog.M
}

func (e *Entry) Str(key, value string) *Entry {
	e.init()
	e.fields[key] = value
	return e
}

// Err attaches the error's message under the conventional "error" key. A nil
// error is ignored so guards like `logger.Error().Err(err)` stay in place.
func (e *Entry) Err(err error) *Entry {
	if err == nil {
		return e
	}
	e.init()
	e.fields["error"] = err.Error()
	return e
}

func (e *Entry) Int(key string, value int) *Entry {
	e.init()
	e.fields[key] = value
	return e
}

func (e *Entry) Int64(key string, value int64) *Entry {
	e.init()
	e.fields[key] = value
	return e
}

func (e *Entry) Msg(msg string) {
	if len(e.fields) == 0 {
		e.logger.core.Log(e.level, msg)
		return
	}
	e.logger.core.WithData(e.fields).Log(e.level, msg)
}

func (e *Entry) init() {
	if e.fields == nil {
		e.fields = slog.M{}
	}
}

func (l *Logger) Info() *Entry  { return &Entry{logger: l, level: slog.InfoLevel} }
func (l *Logger) Warn() *Entry  { return &Entry{logger: l, level: slog.WarnLevel} }
func (l *Logger) Error() *Entry { return &Entry{logger: l, level: slog.ErrorLevel} }

var (
	defaultOnce   sync.Once
	defaultLogger *Logger
)

// DefaultLogger returns the package-level fallback used when a caller does not
// inject one: console-only (no file sink, nothing to clean up).
func DefaultLogger() *Logger {
	defaultOnce.Do(func() {
		defaultLogger = &Logger{core: newCore(newConsoleHandler(slog.InfoLevel))}
	})
	return defaultLogger
}

// New builds the recommended dual-sink logger: colorized text on stdout plus
// the configured format written to <dataDir>/logs/sb-control.log with
// size-based rotation (text is colorless so the file stays readable in any
// editor; json keeps machine-parseable postmortem). The returned Closer
// flushes the file sink; Close is idempotent.
func New(dataDir string, options Options) (*Logger, Closer, error) {
	level := ParseLevel(options.Level)
	maxMB, backups := options.RotateMaxMB, options.RotateBackups
	if maxMB <= 0 {
		maxMB = logMaxSizeMB
	}
	if backups <= 0 {
		backups = logMaxBackups
	}
	fileHandler, err := handler.NewSizeRotateFile(
		filepath.Join(dataDir, "logs", logFileName),
		maxMB*1024*1024,
		handler.WithBackupNum(uint(backups)),
		handler.WithFilePerm(0o600), // match the DB's restrictive perm; logs may carry tokens-in-error
	)
	if err != nil {
		return nil, nil, fmt.Errorf("创建日志文件失败: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(options.FileFormat), "json") {
		fileHandler.SetFormatter(slog.NewJSONFormatter())
	} else {
		fileFormatter := slog.NewTextFormatter(textTemplate)
		fileFormatter.WithEnableColor(false)
		fileHandler.SetFormatter(fileFormatter)
	}
	// SyncCloseHandler gates levels through its embedded LevelFormattable,
	// which NewSizeRotateFile builds as *LevelsWithFormatter.
	if levels, ok := fileHandler.LevelFormattable.(*slog.LevelsWithFormatter); ok {
		levels.SetLimitLevels(levelsFrom(level))
	}
	return &Logger{core: newCore(newConsoleHandler(level), fileHandler)}, &onceCloser{c: fileHandler}, nil
}

// newConsoleHandler builds the stdout sink: gookit's text template with color
// when the terminal supports it.
func newConsoleHandler(level slog.Level) *handler.ConsoleHandler {
	console := handler.NewConsole(levelsFrom(level))
	formatter := slog.NewTextFormatter(textTemplate)
	formatter.WithEnableColor(color.SupportColor())
	console.SetFormatter(formatter)
	return console
}

// newCore builds the gookit dispatcher with caller reporting disabled: the
// facade's own Msg frame would otherwise be reported as the caller, and the
// per-record stack walk is not worth it on this single-binary service.
func newCore(handlers ...slog.Handler) *slog.Logger {
	return slog.NewWithConfig(func(l *slog.Logger) {
		l.AddHandlers(handlers...)
		l.ReportCaller = false
	})
}

// onceCloser makes the file sink's Close idempotent: gookit's rotate handler
// errors with "file already closed" on repeat calls, while callers (deferred
// close + graceful shutdown) may legitimately close twice.
type onceCloser struct {
	once sync.Once
	err  error
	c    Closer
}

func (o *onceCloser) Close() error {
	o.once.Do(func() { o.err = o.c.Close() })
	return o.err
}

// levelsFrom expands a minimum severity into the full gookit level list the
// handler will accept (gookit filters by explicit level list, not by
// threshold). gookit numbers levels the other way around — Panic=100 …
// Trace=800, smaller value = more severe — so the minimum keeps every level
// numerically <= it.
func levelsFrom(min slog.Level) []slog.Level {
	levels := make([]slog.Level, 0, len(slog.AllLevels))
	for _, level := range slog.AllLevels {
		if level <= min {
			levels = append(levels, level)
		}
	}
	return levels
}

// textTemplate is gookit's default template minus the caller slot (caller
// reporting is off; the default would render empty brackets).
const textTemplate = "[{{datetime}}] [{{channel}}] [{{level}}] {{message}} {{data}} {{extra}}\n"
