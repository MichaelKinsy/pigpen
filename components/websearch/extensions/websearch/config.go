package websearch

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const configFileName = "web-search.json"

var (
	cacheMu     sync.Mutex
	cachedDir   string
	cachedDirOK bool
)

// ResetCaches forgets the memoized config directory (tests, and reload).
func ResetCaches() {
	cacheMu.Lock()
	cachedDir, cachedDirOK = "", false
	cacheMu.Unlock()
	resetProviderState()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

func pigHome() string {
	if h := os.Getenv("PIG_HOME"); h != "" {
		return h
	}
	return filepath.Join(homeDir(), ".pig")
}

// ConfigDir is where web-search.json lives (upstream getWebSearchConfigDir, memoized for the
// process like the original). Order: an explicit agent directory (PIG_CODING_AGENT_DIR, then
// Pi's PI_CODING_AGENT_DIR); otherwise the first directory that already holds web-search.json
// among PiG's own (XDG_CONFIG_HOME/pig when XDG is set, $PIG_HOME/agent) and Pi's (XDG/pi,
// ~/.pi/agent, ~/.pi); otherwise the new-config target: XDG_CONFIG_HOME/pig when XDG is set, else
// $PIG_HOME/agent. Pi's locations stay readable so a Pi user's file keeps working.
func ConfigDir() string {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cachedDirOK {
		return cachedDir
	}
	cachedDir, cachedDirOK = resolveConfigDir(), true
	return cachedDir
}

func resolveConfigDir() string {
	for _, name := range []string{"PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"} {
		if dir := os.Getenv(name); dir != "" {
			return dir
		}
	}
	home := homeDir()
	var candidates []string
	target := filepath.Join(pigHome(), "agent")
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		target = filepath.Join(xdg, "pig")
		candidates = append(candidates, target, filepath.Join(xdg, "pi"))
	}
	candidates = append(candidates, filepath.Join(pigHome(), "agent"), filepath.Join(home, ".pi", "agent"), filepath.Join(home, ".pi"))
	for _, dir := range candidates {
		if fileExists(filepath.Join(dir, configFileName)) {
			return dir
		}
	}
	return target
}

// ConfigPath is the full path of web-search.json.
func ConfigPath() string { return filepath.Join(ConfigDir(), configFileName) }

// ReadConfigRoot parses web-search.json. A missing file gives (nil, nil); invalid JSON or a
// non-object root fail with the original's messages.
func ReadConfigRoot() (map[string]any, error) {
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("Failed to parse %s: %s", path, jsonErrorText(err))
	}
	root, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Invalid config in %s: expected a JSON object", path)
	}
	return root, nil
}

func jsonErrorText(err error) string { return err.Error() }

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// jsType is `typeof value` for a decoded JSON value.
func jsType(v any) string {
	switch v.(type) {
	case nil:
		return "object"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	default:
		return "object"
	}
}

func asObject(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// APIBaseURLOptions mirrors resolveApiBaseUrl's argument.
type APIBaseURLOptions struct {
	ConfigKey       string
	ConfiguredValue any
	DefaultValue    string
	EnvironmentKey  string
	// EnvironmentValue is nil when the variable is unset; an empty string counts as set.
	EnvironmentValue *string
}

// IsLoopbackHostname reports localhost, ::1 and 127.0.0.0/8 literals.
func IsLoopbackHostname(hostname string) bool {
	n := strings.ToLower(hostname)
	n = strings.TrimPrefix(n, "[")
	n = strings.TrimSuffix(n, "]")
	n = strings.TrimSuffix(n, ".")
	return n == "localhost" || n == "::1" || (ipVersion(n) == 4 && strings.HasPrefix(n, "127."))
}

// ResolveAPIBaseURL validates a provider base URL from the environment or config: absolute
// HTTPS (HTTP only for loopback), no credentials, query or fragment; the trailing slash goes.
func ResolveAPIBaseURL(o APIBaseURLOptions) (string, error) {
	var value any
	fromEnv := o.EnvironmentValue != nil
	if fromEnv {
		value = *o.EnvironmentValue
	} else {
		value = o.ConfiguredValue
	}
	if value == nil {
		return o.DefaultValue, nil
	}
	source := o.ConfigKey + " in " + ConfigPath()
	if fromEnv {
		source = o.EnvironmentKey
	}
	s, ok := value.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("%s must be an absolute HTTP(S) URL", source)
	}
	u, err := ParseURL(strings.TrimSpace(s), nil)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		if err != nil || u.Scheme == "" {
			return "", fmt.Errorf("%s must be an absolute HTTP(S) URL", source)
		}
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !IsLoopbackHostname(u.Hostname())) {
		return "", fmt.Errorf("%s must be an absolute HTTPS URL", source)
	}
	if u.User != nil {
		return "", fmt.Errorf("%s must not include credentials", source)
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(s, "#") {
		return "", fmt.Errorf("%s must not include query parameters or fragments", source)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/"), nil
}

// FormatSeconds renders H:MM:SS or M:SS.
func FormatSeconds(s int) string {
	h, m, sec := s/3600, (s%3600)/60, s%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}

// nowMs is the clock; tests replace it (the original tests replace Date.now).
var nowMs = func() int64 { return time.Now().UnixMilli() }

func isFiniteInt(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
		return 0, false
	}
	return int(f), true
}
