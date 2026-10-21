package pi_goal_x

// ported from goal-settings.ts: layered project/global/environment settings, sparse layers, validation diagnostics,
// provenance, the zero-op cache, and scoped mutation under a filesystem path lock with an atomic write.
//
// Sparse layers (the content of a settings FILE) are ordered *jsObject values, as in the original, because a mutation
// writes the parsed layer back and the key order of that file is observable. The resolved runtime value is the goalSettings
// struct. An env argument is a map; a nil map is the process environment (the original's default `process.env`), an empty
// non-nil map is an empty environment.

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// ── keybindings ─────────────────────────────────────────────────────────────

type goalDashboardKeybindings struct{ toggleExpand, scrollUp, scrollDown string }

type goalKeybindings struct{ dashboard goalDashboardKeybindings }

var defaultGoalKeybindings = goalKeybindings{dashboard: goalDashboardKeybindings{
	toggleExpand: "ctrl+shift+t",
	scrollUp:     "ctrl+shift+up",
	scrollDown:   "ctrl+shift+down",
}}

// defaultNetworkRecoveryMaxDelayMs is the ceiling of a single recovery delay (the escalation ladder plateaus here).
const defaultNetworkRecoveryMaxDelayMs = 80000

var goalKeybindingLabels = map[string]string{
	"ctrl": "Ctrl", "control": "Ctrl", "shift": "Shift", "alt": "Alt",
	"up": "↑", "down": "↓", "left": "←", "right": "→",
	"pageup": "PageUp", "pagedown": "PageDown", "home": "Home", "end": "End", "enter": "Enter", "escape": "Esc",
}

func formatGoalKeybinding(key string) string {
	parts := strings.Split(key, "+")
	for i, p := range parts {
		if label, ok := goalKeybindingLabels[strings.ToLower(p)]; ok {
			parts[i] = label
		} else {
			parts[i] = strings.ToUpper(p)
		}
	}
	return strings.Join(parts, "+")
}

// ── resolved settings ───────────────────────────────────────────────────────

type resolvedGoalOracleSettings struct {
	enabled                     bool
	provider, model             string
	thinkingLevel               string
	projectResources            bool
	maxFailedAttemptsPerBlocker int
}

type resolvedGoalNetworkRecoverySettings struct{ maxAttempts, maxDelayMs float64 }

// goalSettings is the resolved runtime value (GoalSettings in the original). A string field that is "" and a
// maxAutonomousRuns/goalsRoot that is nil are the original's `undefined`.
type goalSettings struct {
	disableTasks            bool
	disableContracts        bool
	subtaskDepth            float64
	provider                string
	model                   string
	thinkingLevel           string
	disabled                bool
	autoSelectSingleGoal    bool
	auditorProjectResources bool
	stallTimeoutMinutes     float64
	maxAutonomousRuns       *float64
	showAutonomousRuns      bool
	strictExecutionContract bool
	objectiveMaxChars       float64
	keybindings             goalKeybindings
	hideUnfocusedBanner     bool
	hideUnfocusedPrompt     bool
	goalsRoot               *string
	oracle                  resolvedGoalOracleSettings
	networkRecovery         resolvedGoalNetworkRecoverySettings
}

const (
	piGoalSettingsFileEnv       = "PI_GOAL_SETTINGS_FILE"
	piGoalGlobalSettingsFileEnv = "PI_GOAL_GLOBAL_SETTINGS_FILE"
)

var thinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh"}

// ── environment and paths ───────────────────────────────────────────────────

// getenv is the process environment as the unported callers (refusal.go) read it.
var getenv = os.Getenv

func settingsEnvGet(env map[string]string, key string) (string, bool) {
	if env == nil {
		return os.LookupEnv(key)
	}
	v, ok := env[key]
	return v, ok
}

// settingsEnvAny is env[key] as a JavaScript value: nil (undefined) or the string.
func settingsEnvAny(env map[string]string, key string) any {
	if v, ok := settingsEnvGet(env, key); ok {
		return v
	}
	return nil
}

func firstSettingsEnv(env []map[string]string) map[string]string {
	if len(env) > 0 {
		return env[0]
	}
	return nil
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

func asNonEmptyString(v any) (string, bool) {
	if s, ok := v.(string); ok {
		if t := jsTrim(s); t != "" {
			return t, true
		}
	}
	return "", false
}

// settingsPathNormalize is Node's path.normalize (keeps a trailing separator).
func settingsPathNormalize(p string) string {
	c := filepath.Clean(p)
	if strings.HasSuffix(p, "/") && !strings.HasSuffix(c, "/") {
		return c + "/"
	}
	return c
}

// settingsPathResolve is path.resolve(home, rel) for a relative rel.
func settingsPathResolve(home, rel string) string {
	return absClean(filepath.Join(home, rel))
}

// resolveAgentDir honors PI_CODING_AGENT_DIR (absolute or relative to homeDir).
func resolveAgentDir(env map[string]string, home string) string {
	if o, ok := asNonEmptyString(settingsEnvAny(env, "PI_CODING_AGENT_DIR")); ok {
		if filepath.IsAbs(o) {
			return settingsPathNormalize(o)
		}
		return settingsPathResolve(home, o)
	}
	return filepath.Join(home, ".pi", "agent")
}

// goalGlobalSettingsPath is PI_GOAL_GLOBAL_SETTINGS_FILE if set (absolute or relative to homeDir), else
// <agentDir>/pi-goal-x-settings.json.
func goalGlobalSettingsPath(env map[string]string, home string) string {
	if o, ok := asNonEmptyString(settingsEnvAny(env, piGoalGlobalSettingsFileEnv)); ok {
		if filepath.IsAbs(o) {
			return settingsPathNormalize(o)
		}
		return settingsPathResolve(home, o)
	}
	return filepath.Join(resolveAgentDir(env, home), "pi-goal-x-settings.json")
}

// goalSettingsPath is PI_GOAL_SETTINGS_FILE if set (absolute or relative to cwd), else <cwd>/.pi/pi-goal-x-settings.json.
func goalSettingsPath(cwd string, env ...map[string]string) string {
	if o, ok := asNonEmptyString(settingsEnvAny(firstSettingsEnv(env), piGoalSettingsFileEnv)); ok {
		if filepath.IsAbs(o) {
			return o
		}
		return filepath.Join(cwd, o)
	}
	return filepath.Join(cwd, ".pi", "pi-goal-x-settings.json")
}

// globalSettingsPath and projectSettingsPath: the process-environment forms the unported commands read.
func globalSettingsPath() string { return goalGlobalSettingsPath(nil, homeDir()) }

func projectSettingsPath(cwd string) string { return goalSettingsPath(cwd) }

// ── diagnostics ─────────────────────────────────────────────────────────────

type settingsScope string

const (
	settingsScopeGlobal  settingsScope = "global"
	settingsScopeProject settingsScope = "project"
)

type settingsDiagnostic struct {
	scope       settingsScope
	path        string
	settingPath string // "" = absent
	code        string // invalid_json, not_object, unknown_key, invalid_value, invalid_nested_key
	message     string
}

type settingsLayerRead struct {
	scope       settingsScope
	path        string
	status      string // ok, missing, invalid
	layer       *jsObject
	diagnostics []settingsDiagnostic
	fingerprint string
}

// ── zero-op cache ───────────────────────────────────────────────────────────

type resolvedSettingsCacheEntry struct {
	global, project *settingsLayerRead
	environment     []*string
	snapshot        *settingsSnapshot
}

var (
	settingsCacheMu       sync.Mutex
	settingsFileCache     = map[string]*settingsLayerRead{}
	resolvedSettingsCache []resolvedSettingsCacheEntry
)

// invalidateGoalSettingsCache is the session boundary: both layers are read fresh from disk afterwards.
func invalidateGoalSettingsCache() {
	settingsCacheMu.Lock()
	defer settingsCacheMu.Unlock()
	settingsFileCache = map[string]*settingsLayerRead{}
	resolvedSettingsCache = nil
}

func invalidateSettingsCachePath(target string) {
	settingsCacheMu.Lock()
	defer settingsCacheMu.Unlock()
	delete(settingsFileCache, target)
	resolvedSettingsCache = nil
}

// ── leaf parsers ────────────────────────────────────────────────────────────

func asBool(v any) (value bool, ok bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		if x == "true" {
			return true, true
		}
		if x == "false" {
			return false, true
		}
	}
	return false, false
}

// settingsParseInt is parseInt(s, 10): leading whitespace, an optional sign, then the digits.
func settingsParseInt(s string) (float64, bool) {
	s = strings.TrimLeftFunc(s, isJSSpace)
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, _ := strconv.ParseFloat(s[:end], 64)
	if neg {
		n = -n
	}
	return n, true
}

func settingsIsInteger(f float64) bool { return !math.IsInf(f, 0) && f == math.Trunc(f) }

func asPositiveInt(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		if settingsIsInteger(x) && x >= 1 {
			return x, true
		}
	case string:
		if n, ok := settingsParseInt(x); ok && n >= 1 {
			return n, true
		}
	}
	return 0, false
}

// asNonNegativeInt parses settings where 0 means off / no limit.
func asNonNegativeInt(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		if settingsIsInteger(x) && x >= 0 {
			return x, true
		}
	case string:
		if n, ok := settingsParseInt(x); ok && n >= 0 {
			return n, true
		}
	}
	return 0, false
}

func asThinkingLevel(v any) (string, bool) {
	text, ok := asNonEmptyString(v)
	if !ok {
		return "", false
	}
	for _, l := range thinkingLevels {
		if l == text {
			return text, true
		}
	}
	return "", false
}

var (
	settingsBoolKeys = map[string]bool{
		"strictExecutionContract": true, "disableTasks": true, "disableContracts": true, "disabled": true,
		"autoSelectSingleGoal": true, "auditorProjectResources": true, "hideUnfocusedBanner": true,
		"showAutonomousRuns": true, "hideUnfocusedPrompt": true,
	}
	allowedKeybindingKeys          = map[string]bool{"dashboard": true}
	allowedDashboardKeybindingKeys = map[string]bool{"toggleExpand": true, "scrollUp": true, "scrollDown": true}
	allowedOracleKeys              = map[string]bool{
		"enabled": true, "provider": true, "model": true, "thinkingLevel": true, "thinking_level": true,
		"projectResources": true, "maxFailedAttemptsPerBlocker": true,
	}
	allowedNetworkRecoveryKeys = map[string]bool{"maxAttempts": true, "maxDelayMs": true}
)

// plainObject is `value && typeof value === "object" && !Array.isArray(value)`.
func plainObject(v any) (*jsObject, bool) {
	o, ok := v.(*jsObject)
	return o, ok && o != nil
}

// parseSettingsLayer parses raw JSON content into a sparse layer plus diagnostics. Unknown keys and invalid values produce
// diagnostics; they never erase valid known keys in the same file.
func parseSettingsLayer(raw any, scope settingsScope, filePath string) (*jsObject, []settingsDiagnostic) {
	diagnostics := []settingsDiagnostic{}
	diagnostic := func(code, message, settingPath string) {
		diagnostics = append(diagnostics, settingsDiagnostic{scope: scope, path: filePath, settingPath: settingPath, code: code, message: message})
	}
	layer := newObject()
	if raw == nil {
		return layer, diagnostics
	}
	record, isObj := plainObject(raw)
	if !isObj {
		diagnostic("not_object", "settings file must contain a JSON object", "")
		return layer, diagnostics
	}

	for _, key := range record.order() {
		value := record.vals[key]
		if settingsBoolKeys[key] {
			if parsed, ok := asBool(value); ok {
				layer.set(key, parsed)
			} else {
				diagnostic("invalid_value", key+" must be true or false", key)
			}
			continue
		}
		switch key {
		case "subtaskDepth":
			if parsed, ok := asPositiveInt(value); ok {
				layer.set(key, parsed)
			} else {
				diagnostic("invalid_value", key+" must be an integer >= 1", key)
			}
		case "maxAutonomousRuns":
			n := math.NaN()
			switch x := value.(type) {
			case float64:
				n = x
			case string:
				if t := jsTrim(x); t != "" && strings.Trim(t, "0123456789") == "" {
					n, _ = strconv.ParseFloat(t, 64)
				}
			}
			if !isSafeInteger(n) || n < 0 {
				diagnostic("invalid_value", "maxAutonomousRuns must be a nonnegative safe integer (0 disables automatic continuation)", key)
			} else {
				layer.set(key, n)
			}
		case "stallTimeoutMinutes", "objectiveMaxChars":
			if parsed, ok := asNonNegativeInt(value); ok {
				layer.set(key, parsed)
			} else {
				diagnostic("invalid_value", key+" must be an integer >= 0", key)
			}
		case "goalsRoot", "provider", "model":
			if parsed, ok := asNonEmptyString(value); ok {
				layer.set(key, parsed)
			} else {
				diagnostic("invalid_value", key+" must be a non-empty string", key)
			}
		case "thinkingLevel", "thinking_level":
			if parsed, ok := asThinkingLevel(value); ok {
				layer.set("thinkingLevel", parsed)
			} else {
				diagnostic("invalid_value", key+" must be one of: "+strings.Join(thinkingLevels, ", "), key)
			}
		case "keybindings":
			kbRecord, ok := plainObject(value)
			if !ok {
				diagnostic("invalid_nested_key", "keybindings must be an object", key)
				break
			}
			sparse := newObject()
			for _, kbKey := range kbRecord.order() {
				kbValue := kbRecord.vals[kbKey]
				if !allowedKeybindingKeys[kbKey] {
					diagnostic("unknown_key", "unknown keybindings key: "+kbKey, "keybindings."+kbKey)
					continue
				}
				dashRecord, ok := plainObject(kbValue)
				if !ok {
					diagnostic("invalid_nested_key", "keybindings.dashboard must be an object", "keybindings.dashboard")
					continue
				}
				dash := newObject()
				for _, dashKey := range dashRecord.order() {
					if !allowedDashboardKeybindingKeys[dashKey] {
						diagnostic("unknown_key", "unknown dashboard keybinding: "+dashKey, "keybindings.dashboard."+dashKey)
						continue
					}
					bound, ok := asNonEmptyString(dashRecord.vals[dashKey])
					if !ok {
						diagnostic("invalid_value", dashKey+" must be a non-empty key id", "keybindings.dashboard."+dashKey)
						continue
					}
					dash.set(dashKey, bound)
				}
				sparse.set("dashboard", dash)
			}
			layer.set("keybindings", sparse)
		case "oracle":
			oracleRecord, ok := plainObject(value)
			if !ok {
				diagnostic("invalid_nested_key", "oracle must be an object", key)
				break
			}
			oracle := newObject()
			for _, oKey := range oracleRecord.order() {
				oValue := oracleRecord.vals[oKey]
				if !allowedOracleKeys[oKey] {
					diagnostic("unknown_key", "unknown oracle key: "+oKey, "oracle."+oKey)
					continue
				}
				switch oKey {
				case "enabled", "projectResources":
					if parsed, ok := asBool(oValue); ok {
						oracle.set(oKey, parsed)
					} else {
						diagnostic("invalid_value", "oracle."+oKey+" must be true or false", "oracle."+oKey)
					}
				case "provider", "model":
					if parsed, ok := asNonEmptyString(oValue); ok {
						oracle.set(oKey, parsed)
					} else {
						diagnostic("invalid_value", "oracle."+oKey+" must be a non-empty string", "oracle."+oKey)
					}
				case "thinkingLevel", "thinking_level":
					if parsed, ok := asThinkingLevel(oValue); ok {
						oracle.set("thinkingLevel", parsed)
					} else {
						diagnostic("invalid_value", "oracle."+oKey+" must be one of: "+strings.Join(thinkingLevels, ", "), "oracle."+oKey)
					}
				case "maxFailedAttemptsPerBlocker":
					if parsed, ok := asPositiveInt(oValue); !ok || parsed > 3 {
						diagnostic("invalid_value", "oracle.maxFailedAttemptsPerBlocker must be an integer between 1 and 3", "oracle."+oKey)
					} else {
						oracle.set("maxFailedAttemptsPerBlocker", parsed)
					}
				}
			}
			layer.set("oracle", oracle)
		case "networkRecovery":
			nrRecord, ok := plainObject(value)
			if !ok {
				diagnostic("invalid_nested_key", "networkRecovery must be an object", key)
				break
			}
			nr := newObject()
			for _, nKey := range nrRecord.order() {
				nValue := nrRecord.vals[nKey]
				if !allowedNetworkRecoveryKeys[nKey] {
					diagnostic("unknown_key", "unknown networkRecovery key: "+nKey, "networkRecovery."+nKey)
					continue
				}
				parsed, ok := asNonNegativeInt(nValue)
				if nKey == "maxAttempts" {
					if !ok {
						diagnostic("invalid_value", "networkRecovery.maxAttempts must be an integer >= 0 (0 = unbounded)", "networkRecovery."+nKey)
					} else {
						nr.set("maxAttempts", parsed)
					}
				} else if !ok || parsed < 1000 {
					diagnostic("invalid_value", "networkRecovery.maxDelayMs must be an integer >= 1000", "networkRecovery."+nKey)
				} else {
					nr.set("maxDelayMs", parsed)
				}
			}
			if len(nr.keys) > 0 {
				layer.set("networkRecovery", nr)
			}
		default:
			diagnostic("unknown_key", "unknown settings key: "+key, key)
		}
	}
	return layer, diagnostics
}

// parseGoalSettings is the legacy strict parse: it fails on unknown top-level keys.
func parseGoalSettings(raw any) (*jsObject, error) {
	layer, diagnostics := parseSettingsLayer(raw, settingsScopeProject, "(inline)")
	var unknown []string
	for _, d := range diagnostics {
		if d.code == "unknown_key" && d.settingPath != "" && !strings.Contains(d.settingPath, ".") {
			unknown = append(unknown, d.settingPath)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("Unknown pi-goal-x-settings.json key(s): %s", strings.Join(unknown, ", "))
	}
	return layer, nil
}

// ── layer reads ─────────────────────────────────────────────────────────────

// fingerprintOf is a cheap stable fingerprint: length plus an FNV-1a style hash over UTF-16 units (not security).
func fingerprintOf(text string) string {
	units := utf16.Encode([]rune(text))
	hash := uint32(2166136261)
	for _, u := range units {
		hash ^= uint32(u)
		hash *= 16777619
	}
	return fmt.Sprintf("%d:%s", len(units), strconv.FormatUint(uint64(hash), 16))
}

func statusFor(diagnostics []settingsDiagnostic, hadContent bool) string {
	for _, d := range diagnostics {
		if d.code == "invalid_json" || d.code == "not_object" {
			return "invalid"
		}
	}
	if !hadContent {
		return "missing"
	}
	if len(diagnostics) > 0 {
		return "invalid"
	}
	return "ok"
}

func cacheSettingsRead(target string, read *settingsLayerRead) *settingsLayerRead {
	settingsCacheMu.Lock()
	defer settingsCacheMu.Unlock()
	settingsFileCache[target] = read
	return read
}

func readSettingsLayerFresh(target string, scope settingsScope) *settingsLayerRead {
	data, err := os.ReadFile(target)
	if err != nil {
		return cacheSettingsRead(target, &settingsLayerRead{scope: scope, path: target, status: "missing", layer: newObject(), diagnostics: []settingsDiagnostic{}, fingerprint: "missing"})
	}
	rawText := string([]rune(string(data))) // utf8 decoding replaces each invalid byte, as Node does
	parsed, err := parseJSON([]byte(rawText))
	if err != nil {
		return cacheSettingsRead(target, &settingsLayerRead{
			scope: scope, path: target, status: "invalid", layer: newObject(),
			diagnostics: []settingsDiagnostic{{scope: scope, path: target, code: "invalid_json", message: "settings file is not valid JSON"}},
			fingerprint: fingerprintOf(rawText),
		})
	}
	layer, diagnostics := parseSettingsLayer(parsed, scope, target)
	return cacheSettingsRead(target, &settingsLayerRead{
		scope: scope, path: target, status: statusFor(diagnostics, jsTrim(rawText) != ""),
		layer: layer, diagnostics: diagnostics, fingerprint: fingerprintOf(rawText),
	})
}

// readSettingsLayer is the cache-served layer read (zero-op in steady state).
func readSettingsLayer(target string, scope settingsScope) *settingsLayerRead {
	settingsCacheMu.Lock()
	cached := settingsFileCache[target]
	settingsCacheMu.Unlock()
	if cached != nil {
		return cached
	}
	return readSettingsLayerFresh(target, scope)
}

// loadGoalSettingsFileConfig loads just the project layer's sparse config.
func loadGoalSettingsFileConfig(cwd string, env ...map[string]string) *jsObject {
	return readSettingsLayer(goalSettingsPath(cwd, firstSettingsEnv(env)), settingsScopeProject).layer
}

// ── resolution with provenance ──────────────────────────────────────────────

type resolvedSetting struct {
	value  any // nil = undefined
	source string
	envVar string
}

type settingsSnapshot struct {
	global, project *settingsLayerRead
	value           goalSettings
	provenance      map[string]*resolvedSetting
	diagnostics     []settingsDiagnostic
}

func resolveLeaf(envValue, projectValue, globalValue, defaultValue any, envVar string) *resolvedSetting {
	switch {
	case envValue != nil:
		return &resolvedSetting{value: envValue, source: "environment", envVar: envVar}
	case projectValue != nil:
		return &resolvedSetting{value: projectValue, source: "project"}
	case globalValue != nil:
		return &resolvedSetting{value: globalValue, source: "global"}
	}
	return &resolvedSetting{value: defaultValue, source: "default"}
}

// layerLeaf is layer?.a?.b?.c: nil when any step is missing.
func layerLeaf(layer *jsObject, path ...string) any {
	var cur any = layer
	for _, k := range path {
		o, ok := cur.(*jsObject)
		if !ok || o == nil {
			return nil
		}
		v := o.vals[k]
		if _, u := v.(undef); u {
			return nil
		}
		cur = v
	}
	if o, ok := cur.(*jsObject); ok && o == nil {
		return nil
	}
	return cur
}

var resolutionEnvKeys = []string{"PI_GOAL_ROOT", "PI_GOAL_DISABLE_TASKS", "PI_GOAL_DISABLE_CONTRACTS", "PI_GOAL_OBJECTIVE_MAX_CHARS", "PI_GOAL_NETWORK_RECOVERY_MAX_ATTEMPTS", "PI_GOAL_NETWORK_RECOVERY_MAX_DELAY_MS"}

func settingsEnvKeysOf(env map[string]string) []*string {
	out := make([]*string, len(resolutionEnvKeys))
	for i, k := range resolutionEnvKeys {
		if v, ok := settingsEnvGet(env, k); ok {
			s := v
			out[i] = &s
		}
	}
	return out
}

func sameSettingsEnvKeys(a, b []*string) bool {
	for i := range a {
		if (a[i] == nil) != (b[i] == nil) || (a[i] != nil && *a[i] != *b[i]) {
			return false
		}
	}
	return true
}

func resolvedSettingsSnapshot(cwd string, env map[string]string) *settingsSnapshot {
	global := readSettingsLayer(goalGlobalSettingsPath(env, homeDir()), settingsScopeGlobal)
	project := readSettingsLayer(goalSettingsPath(cwd, env), settingsScopeProject)
	envKeys := settingsEnvKeysOf(env)
	settingsCacheMu.Lock()
	for _, c := range resolvedSettingsCache {
		if c.global == global && c.project == project && sameSettingsEnvKeys(envKeys, c.environment) {
			settingsCacheMu.Unlock()
			return c.snapshot
		}
	}
	settingsCacheMu.Unlock()

	provenance := map[string]*resolvedSetting{}
	track := func(key string, r *resolvedSetting) any {
		provenance[key] = r
		return r.value
	}
	envBool := func(name string) any {
		if b, ok := asBool(settingsEnvAny(env, name)); ok {
			return b
		}
		return nil
	}
	envInt := func(name string) any {
		if n, ok := asNonNegativeInt(settingsEnvAny(env, name)); ok {
			return n
		}
		return nil
	}
	leaf := func(key string, def any, path ...string) any {
		return track(key, resolveLeaf(nil, layerLeaf(project.layer, path...), layerLeaf(global.layer, path...), def, ""))
	}
	envLeaf := func(key string, envValue any, envVar string, def any, path ...string) any {
		return track(key, resolveLeaf(envValue, layerLeaf(project.layer, path...), layerLeaf(global.layer, path...), def, envVar))
	}
	asB := func(v any) bool { b, _ := v.(bool); return b }
	asF := func(v any) float64 { f, _ := v.(float64); return f }
	asS := func(v any) string { s, _ := v.(string); return s }
	simple := func(key string, def any) any { return leaf(key, def, key) }

	var v goalSettings
	v.disableTasks = asB(envLeaf("disableTasks", envBool("PI_GOAL_DISABLE_TASKS"), "PI_GOAL_DISABLE_TASKS", false, "disableTasks"))
	v.disableContracts = asB(envLeaf("disableContracts", envBool("PI_GOAL_DISABLE_CONTRACTS"), "PI_GOAL_DISABLE_CONTRACTS", false, "disableContracts"))
	v.subtaskDepth = asF(simple("subtaskDepth", 1.0))
	v.provider = asS(simple("provider", nil))
	v.model = asS(simple("model", nil))
	v.thinkingLevel = asS(simple("thinkingLevel", nil))
	v.disabled = asB(simple("disabled", false))
	v.autoSelectSingleGoal = asB(simple("autoSelectSingleGoal", false))
	v.auditorProjectResources = asB(simple("auditorProjectResources", false))
	v.hideUnfocusedBanner = asB(simple("hideUnfocusedBanner", false))
	v.showAutonomousRuns = asB(simple("showAutonomousRuns", true))
	v.hideUnfocusedPrompt = asB(simple("hideUnfocusedPrompt", false))
	var rootEnv any
	if s, ok := settingsEnvGet(env, "PI_GOAL_ROOT"); ok {
		rootEnv = s
	}
	if root, ok := envLeaf("goalsRoot", rootEnv, "PI_GOAL_ROOT", nil, "goalsRoot").(string); ok {
		v.goalsRoot = &root
	}
	v.oracle.enabled = asB(leaf("oracle.enabled", false, "oracle", "enabled"))
	v.oracle.provider = asS(leaf("oracle.provider", nil, "oracle", "provider"))
	v.oracle.model = asS(leaf("oracle.model", nil, "oracle", "model"))
	v.oracle.thinkingLevel = asS(leaf("oracle.thinkingLevel", nil, "oracle", "thinkingLevel"))
	v.oracle.projectResources = asB(leaf("oracle.projectResources", false, "oracle", "projectResources"))
	v.oracle.maxFailedAttemptsPerBlocker = int(asF(leaf("oracle.maxFailedAttemptsPerBlocker", 2.0, "oracle", "maxFailedAttemptsPerBlocker")))
	v.strictExecutionContract = asB(simple("strictExecutionContract", false))
	if n, ok := simple("maxAutonomousRuns", nil).(float64); ok {
		v.maxAutonomousRuns = &n
	}
	v.stallTimeoutMinutes = asF(simple("stallTimeoutMinutes", 0.0))
	v.objectiveMaxChars = asF(envLeaf("objectiveMaxChars", envInt("PI_GOAL_OBJECTIVE_MAX_CHARS"), "PI_GOAL_OBJECTIVE_MAX_CHARS", 0.0, "objectiveMaxChars"))
	v.networkRecovery.maxAttempts = asF(envLeaf("networkRecovery.maxAttempts", envInt("PI_GOAL_NETWORK_RECOVERY_MAX_ATTEMPTS"), "PI_GOAL_NETWORK_RECOVERY_MAX_ATTEMPTS", 0.0, "networkRecovery", "maxAttempts"))
	v.networkRecovery.maxDelayMs = asF(envLeaf("networkRecovery.maxDelayMs", envInt("PI_GOAL_NETWORK_RECOVERY_MAX_DELAY_MS"), "PI_GOAL_NETWORK_RECOVERY_MAX_DELAY_MS", float64(defaultNetworkRecoveryMaxDelayMs), "networkRecovery", "maxDelayMs"))
	d := defaultGoalKeybindings.dashboard
	v.keybindings.dashboard.toggleExpand = asS(leaf("keybindings.dashboard.toggleExpand", d.toggleExpand, "keybindings", "dashboard", "toggleExpand"))
	v.keybindings.dashboard.scrollUp = asS(leaf("keybindings.dashboard.scrollUp", d.scrollUp, "keybindings", "dashboard", "scrollUp"))
	v.keybindings.dashboard.scrollDown = asS(leaf("keybindings.dashboard.scrollDown", d.scrollDown, "keybindings", "dashboard", "scrollDown"))

	snapshot := &settingsSnapshot{
		global: global, project: project, value: v, provenance: provenance,
		diagnostics: append(append([]settingsDiagnostic{}, global.diagnostics...), project.diagnostics...),
	}
	settingsCacheMu.Lock()
	if len(resolvedSettingsCache) >= 16 {
		resolvedSettingsCache = resolvedSettingsCache[1:]
	}
	resolvedSettingsCache = append(resolvedSettingsCache, resolvedSettingsCacheEntry{global: global, project: project, environment: envKeys, snapshot: snapshot})
	settingsCacheMu.Unlock()
	return snapshot
}

func copyResolvedSettings(v goalSettings) goalSettings {
	if v.maxAutonomousRuns != nil {
		n := *v.maxAutonomousRuns
		v.maxAutonomousRuns = &n
	}
	if v.goalsRoot != nil {
		s := *v.goalsRoot
		v.goalsRoot = &s
	}
	return v
}

// loadSettingsSnapshot returns caller-owned resolved values and provenance; the layer reads are the cached ones.
func loadSettingsSnapshot(cwd string, env ...map[string]string) *settingsSnapshot {
	s := resolvedSettingsSnapshot(cwd, firstSettingsEnv(env))
	prov := make(map[string]*resolvedSetting, len(s.provenance))
	for k, r := range s.provenance {
		c := *r
		prov[k] = &c
	}
	return &settingsSnapshot{
		global: s.global, project: s.project, value: copyResolvedSettings(s.value), provenance: prov,
		diagnostics: append([]settingsDiagnostic{}, s.diagnostics...),
	}
}

// loadGoalSettings loads settings with layered resolution: environment > project > global > defaults.
func loadGoalSettings(cwd string, env ...map[string]string) goalSettings {
	return copyResolvedSettings(resolvedSettingsSnapshot(cwd, firstSettingsEnv(env)).value)
}

// ── conflict-safe scoped mutation ───────────────────────────────────────────

type settingsMutation struct {
	op    string // "set" or "unset"
	path  []string
	value any // set only; undef{} is undefined
}

type settingsMutateInput struct {
	scope    settingsScope
	cwd      string
	env      map[string]string
	mutation settingsMutation
}

type settingsMutationError struct {
	message     string
	diagnostics []settingsDiagnostic
}

func (e *settingsMutationError) Error() string { return e.message }

type pathLock struct {
	path string
	once sync.Once
}

func (l *pathLock) release() {
	l.once.Do(func() { _ = os.Remove(l.path) }) // already removed by stale recovery elsewhere
}

// pathLockOptions: a zero field takes the original's default (200 attempts, 5 ms, 30 s).
type pathLockOptions struct{ attempts, retryMs, staleTtlMs int }

func settingsPidAlive(pid any) bool {
	f, ok := pid.(float64)
	if !ok || !settingsIsInteger(f) || f <= 0 {
		return false
	}
	return pidAlive(int(f))
}

// acquirePathLock is a generic filesystem path lock (same discipline as the goal lock): atomic create, bounded retries,
// stale-TTL and dead-pid recovery.
func acquirePathLock(lockPath string, opts ...pathLockOptions) (*pathLock, error) {
	var o pathLockOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	attempts, retryMs, ttlMs := 200, 5, 30000
	if o.attempts != 0 {
		attempts = o.attempts
	}
	if o.retryMs != 0 {
		retryMs = o.retryMs
	}
	if o.staleTtlMs != 0 {
		ttlMs = o.staleTtlMs
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o777); err != nil {
		return nil, err
	}
	payload := marshalJSON(func() *jsObject {
		p := newObject()
		p.set("pid", float64(os.Getpid()))
		p.set("startedAt", isoFromMs(time.Now().UnixMilli()))
		return p
	}(), "")
	sleep := func() { time.Sleep(time.Duration(retryMs) * time.Millisecond) }

	for attempt := 0; attempt < attempts; attempt++ {
		f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
		if err == nil {
			_, werr := f.WriteString(payload)
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(lockPath)
				return nil, errors.Join(werr, cerr)
			}
			return &pathLock{path: lockPath}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		fi, serr := os.Stat(lockPath)
		if serr != nil {
			// Lock vanished between EEXIST and stat (holder released): brief backoff, never busy-spin.
			sleep()
			continue
		}
		var pid any
		if data, rerr := os.ReadFile(lockPath); rerr == nil {
			if parsed, perr := parseJSON(data); perr == nil {
				if po, ok := parsed.(*jsObject); ok {
					pid = po.vals["pid"]
				}
			}
		}
		_, pidIsNumber := pid.(float64)
		if float64(clockMs())-mtimeMs(fi) > float64(ttlMs) || (pidIsNumber && !settingsPidAlive(pid)) {
			_ = os.Remove(lockPath) // someone else may have recovered it first; retry
			continue
		}
		sleep()
	}
	return nil, &settingsMutationError{message: "Timed out acquiring the settings lock at " + lockPath + ". Another writer may hold it."}
}

func refuseSymlinkTarget(target string) error {
	if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return &settingsMutationError{message: "refusing symlink settings target: " + target}
	}
	return nil // the target does not exist yet: fine
}

func atomicWriteJson(target string, value any, defaultMode os.FileMode) error {
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o777); err != nil {
		return err
	}
	if err := refuseSymlinkTarget(target); err != nil {
		return err
	}
	mode := defaultMode
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm()
	}
	temp := filepath.Join(parent, fmt.Sprintf(".%s.%d.%d.tmp", filepath.Base(target), os.Getpid(), clockMs()))
	f, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		_ = os.Remove(temp)
		return err
	}
	_, werr := f.WriteString(marshalJSON(value, "  ") + "\n")
	if werr == nil {
		werr = f.Sync()
	}
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(temp, target)
	}
	if werr != nil {
		_ = os.Remove(temp)
		return werr
	}
	if d, err := os.Open(parent); err == nil { // directory fsync is platform-dependent; best effort
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func settingsDelete(o *jsObject, key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func settingsDeepClone(v any) any {
	switch x := v.(type) {
	case *jsObject:
		c := newObject()
		for _, k := range x.keys {
			c.set(k, settingsDeepClone(x.vals[k]))
		}
		return c
	case []any:
		c := make([]any, len(x))
		for i, e := range x {
			c[i] = settingsDeepClone(e)
		}
		return c
	}
	return v
}

func applyPathMutation(layer *jsObject, m settingsMutation) error {
	if len(m.path) == 0 {
		return &settingsMutationError{message: "empty settings path"}
	}
	container := layer
	for _, key := range m.path[:len(m.path)-1] {
		next, ok := container.vals[key].(*jsObject)
		if !ok || next == nil {
			if m.op == "unset" {
				return nil // nothing to unset
			}
			next = newObject()
			container.set(key, next)
		}
		container = next
	}
	last := m.path[len(m.path)-1]
	// thinkingLevel/thinking_level are accepted aliases on read; unset clears hand-edited variants too.
	if last == "thinkingLevel" || last == "thinking_level" {
		if m.op == "set" {
			container.set("thinkingLevel", m.value)
		} else {
			settingsDelete(container, "thinkingLevel")
			settingsDelete(container, "thinking_level")
		}
		return nil
	}
	if m.op == "set" {
		container.set(last, m.value)
	} else {
		settingsDelete(container, last)
	}
	return nil
}

// canonicalizeAliases rewrites the persisted spelling of aliased keys before writing.
func canonicalizeAliases(layer *jsObject) {
	if v, ok := layer.vals["thinkingLevel"]; ok {
		if _, u := v.(undef); !u {
			layer.set("thinking_level", v)
			settingsDelete(layer, "thinkingLevel")
		}
	}
}

func pruneEmptyObjects(o *jsObject) {
	for _, key := range append([]string{}, o.keys...) {
		if child, ok := o.vals[key].(*jsObject); ok && child != nil {
			pruneEmptyObjects(child)
			if len(child.keys) == 0 {
				settingsDelete(o, key)
			}
		}
	}
}

// mutateSettingsLayer applies a scoped mutation under the target's path lock and returns a fresh snapshot. It refuses to
// overwrite an invalid layer, writes atomically and invalidates only the affected cache entry.
func mutateSettingsLayer(input settingsMutateInput) (*settingsSnapshot, error) {
	var target string
	if input.scope == settingsScopeGlobal {
		target = goalGlobalSettingsPath(input.env, homeDir())
	} else {
		target = goalSettingsPath(input.cwd, input.env)
	}
	lock, err := acquirePathLock(target + ".lock")
	if err != nil {
		return nil, err
	}
	defer lock.release()

	current := readSettingsLayerFresh(target, input.scope)
	if current.status == "invalid" {
		return nil, &settingsMutationError{message: fmt.Sprintf("Refusing to overwrite invalid %s settings at %s", input.scope, target), diagnostics: current.diagnostics}
	}
	next := settingsDeepClone(current.layer).(*jsObject)
	if err := applyPathMutation(next, input.mutation); err != nil {
		return nil, err
	}
	pruneEmptyObjects(next)
	canonicalizeAliases(next)

	reparsedRaw, err := parseJSON([]byte(marshalJSON(next, "")))
	if err != nil {
		return nil, err
	}
	_, diagnostics := parseSettingsLayer(reparsedRaw, input.scope, target)
	for _, d := range diagnostics {
		if d.code == "invalid_value" || d.code == "invalid_nested_key" {
			return nil, &settingsMutationError{message: fmt.Sprintf("Mutation produced invalid %s settings", input.scope), diagnostics: diagnostics}
		}
	}
	if err := atomicWriteJson(target, next, 0o600); err != nil {
		return nil, err
	}
	invalidateSettingsCachePath(target)
	return loadSettingsSnapshot(input.cwd, input.env), nil
}

// ── legacy whole-file save (routed through the locked path) ─────────────────

func settingsTruthy(v any) bool {
	switch x := v.(type) {
	case nil, undef:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	}
	return true
}

// saveGoalSettingsFileConfig saves settings to the PROJECT file under the lock, after a fresh re-read, and returns the
// normalized runtime shape (thinkingLevel, not the persisted thinking_level).
func saveGoalSettingsFileConfig(cwd string, settings *jsObject) (*jsObject, error) {
	target := goalSettingsPath(cwd)
	lock, err := acquirePathLock(target + ".lock")
	if err != nil {
		return nil, err
	}
	defer lock.release()
	current := readSettingsLayerFresh(target, settingsScopeProject)
	if current.status == "invalid" {
		return nil, &settingsMutationError{message: "Refusing to overwrite invalid project settings at " + target, diagnostics: current.diagnostics}
	}
	clean := buildPersistedLayer(settings)
	if err := atomicWriteJson(target, clean, 0o600); err != nil {
		return nil, err
	}
	invalidateSettingsCachePath(target)
	returned := clean.clone()
	if s, ok := returned.vals["thinking_level"].(string); ok {
		returned.set("thinkingLevel", s)
		settingsDelete(returned, "thinking_level")
	}
	return returned, nil
}

// buildPersistedLayer is the canonical persisted form of a resolved or sparse settings object.
func buildPersistedLayer(settings *jsObject) *jsObject {
	persisted := newObject()
	defined := func(k string) bool { return settings.has(k) }
	copyDefined := func(keys ...string) {
		for _, k := range keys {
			if defined(k) {
				persisted.set(k, settings.vals[k])
			}
		}
	}
	for _, k := range []string{"provider", "model"} {
		if settingsTruthy(settings.vals[k]) {
			persisted.set(k, settings.vals[k])
		}
	}
	if settingsTruthy(settings.vals["thinkingLevel"]) {
		persisted.set("thinking_level", settings.vals["thinkingLevel"])
	}
	copyDefined("disabled", "disableTasks", "disableContracts", "subtaskDepth", "autoSelectSingleGoal",
		"auditorProjectResources", "hideUnfocusedBanner", "showAutonomousRuns", "goalsRoot", "hideUnfocusedPrompt")
	if nr := settings.obj("networkRecovery"); nr != nil {
		o := newObject()
		for _, k := range []string{"maxAttempts", "maxDelayMs"} {
			if nr.has(k) {
				o.set(k, nr.vals[k])
			}
		}
		if len(o.keys) > 0 {
			persisted.set("networkRecovery", o)
		}
	}
	if so := settings.obj("oracle"); so != nil {
		o := newObject()
		for _, k := range []string{"enabled", "provider", "model"} {
			if so.has(k) {
				o.set(k, so.vals[k])
			}
		}
		if so.has("thinkingLevel") {
			o.set("thinking_level", so.vals["thinkingLevel"])
		}
		for _, k := range []string{"projectResources", "maxFailedAttemptsPerBlocker"} {
			if so.has(k) {
				o.set(k, so.vals[k])
			}
		}
		if len(o.keys) > 0 {
			persisted.set("oracle", o)
		}
	}
	copyDefined("strictExecutionContract", "maxAutonomousRuns", "stallTimeoutMinutes", "objectiveMaxChars")
	if kb := settings.obj("keybindings"); kb != nil {
		if dash := kb.obj("dashboard"); dash != nil {
			wrapper := newObject()
			wrapper.set("dashboard", dash.clone())
			persisted.set("keybindings", wrapper)
		}
	}
	return persisted
}

// ── reporting ───────────────────────────────────────────────────────────────

// envOverrideFor names the env var that overrides a settings key's effective value ("" = none). key is a settings key or
// "settingsFile".
func envOverrideFor(key string, env ...map[string]string) string {
	e := firstSettingsEnv(env)
	has := func(name string) bool { _, ok := settingsEnvGet(e, name); return ok }
	switch {
	case key == "goalsRoot" && has("PI_GOAL_ROOT"):
		return "PI_GOAL_ROOT"
	case key == "disableTasks" && has("PI_GOAL_DISABLE_TASKS"):
		return "PI_GOAL_DISABLE_TASKS"
	case key == "disableContracts" && has("PI_GOAL_DISABLE_CONTRACTS"):
		return "PI_GOAL_DISABLE_CONTRACTS"
	case key == "objectiveMaxChars" && has("PI_GOAL_OBJECTIVE_MAX_CHARS"):
		return "PI_GOAL_OBJECTIVE_MAX_CHARS"
	case key == "networkRecovery" && has("PI_GOAL_NETWORK_RECOVERY_MAX_ATTEMPTS"):
		return "PI_GOAL_NETWORK_RECOVERY_MAX_ATTEMPTS"
	case key == "networkRecovery" && has("PI_GOAL_NETWORK_RECOVERY_MAX_DELAY_MS"):
		return "PI_GOAL_NETWORK_RECOVERY_MAX_DELAY_MS"
	case key == "settingsFile" && has(piGoalSettingsFileEnv):
		return piGoalSettingsFileEnv
	}
	return ""
}

// effectiveSettingsReport is the effective-settings report with per-leaf provenance (environment > project > global >
// default), surfaced by /goal-status.
func effectiveSettingsReport(cwd string, env ...map[string]string) []string {
	e := firstSettingsEnv(env)
	snapshot := loadSettingsSnapshot(cwd, e)
	v := snapshot.value
	lines := []string{"Settings (provenance):"}
	str := func(b bool) string { return strconv.FormatBool(b) }
	orDefault := func(s string) string {
		if s == "" {
			return "(default)"
		}
		return s
	}
	allowance := "unlimited (default)"
	if v.maxAutonomousRuns != nil {
		allowance = jsNumber(*v.maxAutonomousRuns)
		if *v.maxAutonomousRuns == 0 {
			allowance = "0 (disabled)"
		}
	}
	goalsRoot := ".pi/goals"
	if v.goalsRoot != nil {
		goalsRoot = *v.goalsRoot
	}
	kb := v.keybindings.dashboard
	rows := []struct{ key, label, value string }{
		{"autoSelectSingleGoal", "autoSelectSingleGoal", str(v.autoSelectSingleGoal)},
		{"disableContracts", "disableContracts", str(v.disableContracts)},
		{"disableTasks", "disableTasks", str(v.disableTasks)},
		{"subtaskDepth", "subtaskDepth", jsNumber(v.subtaskDepth)},
		{"disabled", "auditor disabled", str(v.disabled)},
		{"provider", "provider", orDefault(v.provider)},
		{"model", "model", orDefault(v.model)},
		{"thinkingLevel", "thinking_level", orDefault(v.thinkingLevel)},
		{"auditorProjectResources", "auditor project resources", str(v.auditorProjectResources)},
		{"hideUnfocusedBanner", "hide unfocused banner", str(v.hideUnfocusedBanner)},
		{"showAutonomousRuns", "show autonomous runs line", str(v.showAutonomousRuns)},
		{"goalsRoot", "goals root (reload to change)", goalsRoot},
		{"hideUnfocusedPrompt", "hide unfocused prompt", str(v.hideUnfocusedPrompt)},
		{"strictExecutionContract", "explicit execution contracts (opt-in)", str(v.strictExecutionContract)},
		{"maxAutonomousRuns", "autonomous run allowance", allowance},
		{"stallTimeoutMinutes", "stall timeout (minutes)", jsNumber(v.stallTimeoutMinutes)},
		{"objectiveMaxChars", "max objective length (0 = none)", jsNumber(v.objectiveMaxChars)},
		{"networkRecovery", "network recovery attempts (0 = unbounded)", jsNumber(v.networkRecovery.maxAttempts)},
		{"networkRecovery", "network recovery max delay (ms)", jsNumber(v.networkRecovery.maxDelayMs)},
		{"keybindings", "dashboard keybindings", kb.toggleExpand + ", " + kb.scrollUp + ", " + kb.scrollDown},
	}
	for _, row := range rows {
		source := "default"
		if r := snapshot.provenance[row.key]; r != nil {
			source = r.source
			if r.source == "environment" {
				source = "env (" + r.envVar + ")"
			}
		}
		lines = append(lines, fmt.Sprintf("  %s: %s (%s)", row.label, row.value, source))
	}
	lines = append(lines, "  project settings file: "+snapshot.project.path)
	lines = append(lines, "  global settings file: "+snapshot.global.path)
	if o := envOverrideFor("settingsFile", e); o != "" {
		lines = append(lines, "  (settings file overridden by "+o+")")
	}
	return lines
}

func isAuditorEnabledByDefault(settings goalSettings) bool { return !settings.disabled }
