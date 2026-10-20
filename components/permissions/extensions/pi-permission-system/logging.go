package pi_permission_system

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Port of src/logging/logging.ts: the JSONL writers for the debug log and the permission review log.

// PermissionSystemLogger writes one JSONL line per call. Both return "" on success (or when the stream is switched off) and
// otherwise the warning the caller should surface; neither ever panics.
type PermissionSystemLogger interface {
	Debug(event string, details *jsObject) string
	Review(event string, details *jsObject) string
}

// PermissionSystemLoggerOptions are the writer's collaborators.
type PermissionSystemLoggerOptions struct {
	// GetConfig is read at write time, so a toggle changed mid-session takes effect.
	GetConfig     func() LogConfig
	DebugLogPath  string
	ReviewLogPath string
	// EnsureLogsDirectory returns "" or the message to report when the directory cannot be made.
	EnsureLogsDirectory func() string
	// Now supplies the timestamp; nil is the wall clock.
	Now func() time.Time
}

type permissionSystemLogger struct {
	opts PermissionSystemLoggerOptions
	// hardened is per logger, so a log inherited from an earlier version is tightened once rather than on every line.
	mu       sync.Mutex
	hardened map[string]bool
}

// CreatePermissionSystemLogger builds the writer.
func CreatePermissionSystemLogger(options PermissionSystemLoggerOptions) PermissionSystemLogger {
	return &permissionSystemLogger{opts: options, hardened: map[string]bool{}}
}

func (l *permissionSystemLogger) now() time.Time {
	if l.opts.Now != nil {
		return l.opts.Now()
	}
	return time.Now()
}

// prepareLogLine runs the transform stages in the order they must: command masking first (for both streams, and before any cap,
// so the masker never sees a truncated command), then the width bound (review stream only), then key-name redaction.
func (l *permissionSystemLogger) prepareLogLine(stream, event string, details *jsObject, maxFieldWidth *float64) (string, bool) {
	masked := MaskCommandFields(details)
	bounded := masked
	if maxFieldWidth != nil {
		bounded = CapLogFieldWidths(masked, *maxFieldWidth).(*jsObject)
	}
	line := newObject()
	line.set("timestamp", l.now().UTC().Format("2006-01-02T15:04:05.000Z"))
	line.set("extension", extensionID)
	line.set("stream", stream)
	line.set("event", event)
	// The original spreads the details after the fixed fields, so a detail named like one of them replaces its value in place.
	for _, k := range bounded.order() {
		line.set(k, bounded.vals[k])
	}
	return RedactedJSONStringify(line)
}

// writeLine is the only place a log line is produced.
func (l *permissionSystemLogger) writeLine(stream, path, event string, details *jsObject, maxFieldWidth *float64) (warning string) {
	if msg := l.opts.EnsureLogsDirectory(); msg != "" {
		return msg
	}
	defer func() {
		if r := recover(); r != nil {
			warning = fmt.Sprintf("Failed to write permission-system %s log '%s': %s", stream, path, thrownMessage(r))
		}
	}()
	line, ok := l.prepareLogLine(stream, event, details, maxFieldWidth)
	if !ok || line == "" {
		return fmt.Sprintf("Failed to write permission-system %s log '%s': event could not be serialized.", stream, path)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, OwnerOnlyFileMode)
	if err != nil {
		return fmt.Sprintf("Failed to write permission-system %s log '%s': %s", stream, path, nodeFsMessage(err, "open", path))
	}
	_, werr := f.WriteString(line + "\n")
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Sprintf("Failed to write permission-system %s log '%s': %s", stream, path, nodeFsMessage(werr, "write", path))
	}
	l.mu.Lock()
	first := !l.hardened[path]
	l.hardened[path] = true
	l.mu.Unlock()
	if first {
		RestrictExistingPathToOwner(path, OwnerOnlyFileMode)
	}
	return ""
}

// thrownMessage is `error instanceof Error ? error.message : String(error)` for a recovered panic value.
func thrownMessage(r any) string {
	if e, ok := r.(error); ok {
		return e.Error()
	}
	return fmt.Sprint(r)
}

func (l *permissionSystemLogger) Debug(event string, details *jsObject) string {
	if !l.opts.GetConfig().DebugLog {
		return ""
	}
	if details == nil {
		details = newObject()
	}
	return l.writeLine("debug", l.opts.DebugLogPath, event, details, nil)
}

func (l *permissionSystemLogger) Review(event string, details *jsObject) string {
	config := l.opts.GetConfig()
	if !config.PermissionReviewLog {
		return ""
	}
	if details == nil {
		details = newObject()
	}
	width := ResolveReviewLogFieldWidth(config.ReviewLogFieldMaxWidth)
	return l.writeLine("review", l.opts.ReviewLogPath, event, details, &width)
}
