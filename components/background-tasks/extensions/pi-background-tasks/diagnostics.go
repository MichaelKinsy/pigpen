package pi_background_tasks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The diagnostics log: opt in with PI_BG_TASK_DEBUG, PI_BG_TASK_DIAGNOSTICS or PI_BG_TASK_DIAGNOSTIC_LOG. It never writes to
// the terminal. upstream: extensions/diagnostics.ts.

func envEnabled(value string) bool {
	switch strings.ToLower(jsTrim(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func diagnosticPath() string {
	if configured := jsTrim(os.Getenv("PI_BG_TASK_DIAGNOSTIC_LOG")); configured != "" {
		abs, err := filepath.Abs(configured)
		if err != nil {
			return configured
		}
		return abs
	}
	return filepath.Join(os.TempDir(), "kendex-pi-bg", "diagnostics.log")
}

func backgroundDiagnosticsEnabled() bool {
	return envEnabled(os.Getenv("PI_BG_TASK_DEBUG")) || envEnabled(os.Getenv("PI_BG_TASK_DIAGNOSTICS")) || jsTrim(os.Getenv("PI_BG_TASK_DIAGNOSTIC_LOG")) != ""
}

// logBackgroundDiagnostic appends one line; a failure to write is ignored.
func logBackgroundDiagnostic(message string, details any) {
	if !backgroundDiagnosticsEnabled() {
		return
	}
	path := diagnosticPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	suffix := ""
	if details != nil {
		if b, err := json.Marshal(details); err == nil {
			suffix = " " + string(b)
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o666)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString("[" + time.Now().UTC().Format("2006-01-02T15:04:05.000Z") + "] " + message + suffix + "\n")
}

func itoa(n int) string { return strconv.Itoa(n) }
