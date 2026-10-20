package pi_permission_system

import (
	"path/filepath"
	"sync"
)

// Port of src/logging/session-logger.ts.

// ReviewLogger is the narrowest logging seam: consumers that only write review-log entries.
type ReviewLogger interface {
	Review(event string, details *jsObject)
}

// DebugLogger is the narrowest logging seam: consumers that only write debug-log entries.
type DebugLogger interface {
	Debug(event string, details *jsObject)
}

// DebugReviewLogger is the seam for consumers that write both.
type DebugReviewLogger interface {
	ReviewLogger
	DebugLogger
}

// SessionLogger is the unified logging and notification surface for handler deps.
type SessionLogger interface {
	DebugReviewLogger
	Warn(message string)
}

// SessionLoggerDeps are the narrow dependencies of a PermissionSessionLogger.
type SessionLoggerDeps struct {
	// GlobalLogsDir is the root logs directory; the debug and review log file paths derive from it.
	GlobalLogsDir string
	// GetConfig reads the current config for the debug and review toggles, at call time.
	GetConfig func() LogConfig
	// Notify surfaces a warning to the user, at warn and IO-failure time.
	Notify func(message string)
}

// PermissionSessionLogger composes the JSONL log writer, owns the IO-failure warning dedup set, and routes both IO-failure
// warnings and explicit Warn calls through the notify sink.
type PermissionSessionLogger struct {
	writer   PermissionSystemLogger
	mu       sync.Mutex
	reported map[string]bool
	notify   func(message string)
}

// NewPermissionSessionLogger builds the session logger.
func NewPermissionSessionLogger(deps SessionLoggerDeps) *PermissionSessionLogger {
	return &PermissionSessionLogger{
		writer: CreatePermissionSystemLogger(PermissionSystemLoggerOptions{
			GetConfig:           deps.GetConfig,
			DebugLogPath:        filepath.Join(deps.GlobalLogsDir, debugLogFilename),
			ReviewLogPath:       filepath.Join(deps.GlobalLogsDir, reviewLogFilename),
			EnsureLogsDirectory: func() string { return ensurePermissionSystemLogsDirectory(deps.GlobalLogsDir) },
		}),
		reported: map[string]bool{},
		notify:   deps.Notify,
	}
}

func (s *PermissionSessionLogger) Debug(event string, details *jsObject) {
	if w := s.writer.Debug(event, details); w != "" {
		s.reportOnce(w)
	}
}

func (s *PermissionSessionLogger) Review(event string, details *jsObject) {
	if w := s.writer.Review(event, details); w != "" {
		s.reportOnce(w)
	}
}

func (s *PermissionSessionLogger) Warn(message string) { s.notify(message) }

func (s *PermissionSessionLogger) reportOnce(warning string) {
	s.mu.Lock()
	seen := s.reported[warning]
	s.reported[warning] = true
	s.mu.Unlock()
	if !seen {
		s.notify(warning)
	}
}

var _ SessionLogger = (*PermissionSessionLogger)(nil)
