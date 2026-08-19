package singbox

import (
	"regexp"
	"strings"

	boxlog "github.com/sagernet/sing-box/log"
)

// LogSink receives each kernel log line with the sing-box prefix stripped.
// level is one of "debug", "info", "warn", "error" — the panel logger decides
// what to drop (its floor is info, so debug lines vanish there).
type LogSink func(level, message string)

// kernelLogWriter adapts the panel logger to sing-box's log.PlatformWriter.
// Registering it on box.Options makes the kernel hand every line to the panel
// instead of only writing its own hardcoded "+0800 2006-01-02 15:04:05"
// format to stderr, so kernel lines get the panel timestamp, colors, and the
// rotating JSON file sink.
type kernelLogWriter struct {
	sink LogSink
}

var kernelLogANSI = regexp.MustCompile("\x1b\\[[0-9;]*m")

// platformFormatter always colorizes the level word (its DisableColors flag is
// never set), so a line reaches us as "ESC[36mINFO ESC[0m[ 12] message".
var kernelLogPrefix = regexp.MustCompile(`^[A-Z]+\[\s*\d+\]\s`)

func (w *kernelLogWriter) WriteMessage(level boxlog.Level, message string) {
	if w.sink == nil {
		return
	}
	message = kernelLogPrefix.ReplaceAllLiteralString(kernelLogANSI.ReplaceAllLiteralString(message, ""), "")
	if message = strings.TrimSpace(message); message == "" {
		return
	}
	w.sink(kernelLogLevel(level), message)
}

func kernelLogLevel(level boxlog.Level) string {
	switch level {
	case boxlog.LevelTrace, boxlog.LevelDebug:
		return "debug"
	case boxlog.LevelInfo:
		return "info"
	case boxlog.LevelWarn:
		return "warn"
	default:
		return "error"
	}
}
