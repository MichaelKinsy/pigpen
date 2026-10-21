package pi_background_tasks

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The package's setting accessors. upstream: extensions/settings.ts.

// settingNumber is a number setting: a JSON number, or a string Number() reads as a finite number.
func settingNumber(key string, fallback float64, cwd string) float64 {
	switch v := settings.config(configID, cwd)[key].(type) {
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return v
		}
	case string:
		if f, ok := jsParseNumber(v); ok && !math.IsInf(f, 0) {
			return f
		}
	}
	return fallback
}

// settingBoolean is a boolean setting; only a JSON boolean counts.
func settingBoolean(key string, fallback bool, cwd string) bool {
	if v, ok := settings.config(configID, cwd)[key].(bool); ok {
		return v
	}
	return fallback
}

// settingString is a string setting, trimmed; blank is unset.
func settingString(key, fallback, cwd string) string {
	if v, ok := settings.config(configID, cwd)[key].(string); ok {
		if t := jsTrim(v); t != "" {
			return t
		}
	}
	return fallback
}

// settingEnum is a string setting restricted to allowed.
func settingEnum(key string, allowed []string, fallback, cwd string) string {
	if v, ok := settings.config(configID, cwd)[key].(string); ok && slices.Contains(allowed, v) {
		return v
	}
	return fallback
}

// taskDir is where task logs live: PI_BG_TASK_DIR (trimmed), else the `taskDir` setting (home-expanded, made
// absolute), else `<tmp>/kendex-pi-bg`. The environment variable overrides the setting.
func taskDir() string {
	if env := jsTrim(os.Getenv("PI_BG_TASK_DIR")); env != "" {
		return env
	}
	if configured := settingString("taskDir", "", ""); configured != "" {
		abs, err := filepath.Abs(expandHome(configured))
		if err != nil {
			return expandHome(configured)
		}
		return abs
	}
	return filepath.Join(os.TempDir(), "kendex-pi-bg")
}

var unsafeLabel = regexp.MustCompile(`[^A-Za-z0-9-]+`)

// safeLabel is the id as a file name part: runs of other characters become "-", leading and trailing "-" go, at most 48
// UTF-16 units, "task" when nothing is left.
func safeLabel(input string) string {
	label := unsafeLabel.ReplaceAllString(input, "-")
	label = strings.Trim(label, "-")
	label = jsHead(label, 48)
	if label == "" {
		return "task"
	}
	return label
}

// logFilePath is the log file for task id in the lane directory laneDir.
func logFilePath(laneDir, id string, now int64) string {
	return filepath.Join(laneDir, safeLabel(id)+"-"+strconv.FormatInt(now, 10)+".log")
}

// taskLanesRoot is the folder inside the task directory that holds this package's lane directories. The task directory
// is a user setting that may hold other tools' folders; the retention prune reads only this one.
func taskLanesRoot() string { return filepath.Join(taskDir(), "lanes") }

var unsafeLane = regexp.MustCompile(`[^\w.-]+`)

// taskLaneDir is the directory one session's task logs live in, under taskLanesRoot.
func taskLaneDir(sessionID string) string {
	return filepath.Join(taskLanesRoot(), unsafeLane.ReplaceAllString(sessionID, "_"))
}

// taskEnv is the environment a task runs in: the process's, with `<agent dir>/bin` first on PATH when it exists.
func taskEnv() []string {
	env := os.Environ()
	binDir := filepath.Join(agentDirectory(), "bin")
	info, err := os.Stat(binDir)
	if err != nil || !info.IsDir() {
		return env
	}
	pathKey := "PATH"
	current := ""
	index := -1
	for i, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if strings.EqualFold(k, "PATH") {
			pathKey, current, index = k, v, i
			break
		}
	}
	var parts []string
	for _, p := range strings.Split(current, string(os.PathListSeparator)) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if slices.Contains(parts, binDir) {
		return env
	}
	entry := pathKey + "=" + strings.Join(append([]string{binDir}, parts...), string(os.PathListSeparator))
	if index >= 0 {
		env[index] = entry
	} else {
		env = append(env, entry)
	}
	return env
}
