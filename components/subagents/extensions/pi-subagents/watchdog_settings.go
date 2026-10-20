package pi_subagents


// Watchdog settings (watchdog/settings.ts): `subagents.watchdog.*` of the user's settings.json, the project's and a session
// override, each validated field by field, merged, and resolved against the defaults. The patches are ordered objects
// (jsjson.go) merged like the original's deepMerge; the result is decoded into watchdogConfig.

type wdMeta struct {
	scope string
	path  string
}

func (m wdMeta) sourceName() string { panic("not implemented") }

func wdInvalid(m wdMeta, field, expected string) error { panic("not implemented") }

func wdUnknown(m wdMeta, field string) error { panic("not implemented") }

var (
	wdWatchdogFields = []string{"enabled", "clarification", "agentEndTimeoutMs", "severityThreshold", "maxWarnings", "guidance", "stalemateRepeats", "scope", "cadence", "main", "children", "lsp", "rules"}
	wdRulesFields    = []string{"action", "roleModels"}
	wdRoleRuleFields = []string{"allow", "deny", "note"}
	wdGuidanceFields = []string{"watchdogMd"}
	wdScopeFields    = []string{"enabled"}
	wdCadenceFields  = []string{"everyNTools"}
	wdEndpointFields = []string{"enabled", "model", "thinking"}
	wdChildrenFields = []string{"enabled", "model", "thinking", "watchdogTailTimeoutMs", "cadence", "overrides"}
	wdOverrideFields = []string{"enabled", "model", "thinking", "cadence"}
	wdLspFields      = []string{"enabled", "timeoutMs", "maxFiles", "maxDiagnostics"}
)

func wdAssertKnown(input *jsObject, allowed []string, prefix string, m wdMeta) error {
	panic("not implemented")
}

func wdParseObject(v any, field string, m wdMeta) (*jsObject, error) { panic("not implemented") }

func wdParseBoolean(v any, field string, m wdMeta) (bool, error) { panic("not implemented") }

func wdParseNonEmptyString(v any, field string, m wdMeta) (string, error) { panic("not implemented") }

func wdParseThinking(v any, field string, m wdMeta) (any, error) { panic("not implemented") }

func wdIsInteger(v any) (float64, bool) { panic("not implemented") }

func wdParseInteger(v any, field string, m wdMeta, expected string, check func(float64) bool) (float64, error) {
	panic("not implemented")
}

func wdParseEnum(v any, field string, m wdMeta, values []string) (string, error) {
	panic("not implemented")
}

func wdHasKey(o *jsObject, k string) bool { panic("not implemented") }

func wdParseGuidance(v any, field string, m wdMeta) (*jsObject, error) { panic("not implemented") }

func wdParseScope(v any, field string, m wdMeta) (*jsObject, error) { panic("not implemented") }

func wdParseCadence(v any, field string, m wdMeta) (*jsObject, error) { panic("not implemented") }

func wdParseEndpoint(v any, field string, m wdMeta) (*jsObject, error) { panic("not implemented") }

// wdParseCommon parses the enabled, model and thinking fields shared by endpoints, children and overrides.
func wdParseCommon(in, patch *jsObject, field string, m wdMeta) error { panic("not implemented") }

func wdParseChildOverride(v any, field string, m wdMeta) (*jsObject, error) { panic("not implemented") }

func wdParseChildren(v any, field string, m wdMeta) (*jsObject, error) { panic("not implemented") }

func wdParseLsp(v any, field string, m wdMeta) (*jsObject, error) { panic("not implemented") }

func wdParseStringList(v any, field string, m wdMeta) ([]any, error) { panic("not implemented") }

func wdParseRoleRule(v any, field string, m wdMeta) (*jsObject, error) { panic("not implemented") }

func wdParseRules(v any, field string, m wdMeta) (*jsObject, error) { panic("not implemented") }

func wdParsePatch(v any, field string, m wdMeta) (*jsObject, error) { panic("not implemented") }

func wdParseSettingsObject(settings *jsObject, m wdMeta) (*jsObject, error) { panic("not implemented") }

func getWatchdogUserSettingsPath() string { panic("not implemented") }

func wdIsDir(dir string) bool { panic("not implemented") }

func wdProjectSettingsPath(cwd string) string { panic("not implemented") }

func getWatchdogProjectSettingsPath(cwd string) string { panic("not implemented") }

// wdDeepMerge is deepMerge: plain objects merge key by key, everything else is replaced.
func wdDeepMerge(base, patch *jsObject) *jsObject { panic("not implemented") }

func wdDefaultObject() *jsObject { panic("not implemented") }

// defaultWatchdogConfig is DEFAULT_WATCHDOG_CONFIG (a fresh copy: nothing in it is shared).
func defaultWatchdogConfig() *watchdogConfig { panic("not implemented") }

func wdInt(o *jsObject, k string) int { panic("not implemented") }

func wdBool(o *jsObject, k string) bool { panic("not implemented") }

func wdOptInt(o *jsObject, k string) *int { panic("not implemented") }

func wdThinkingOf(o *jsObject, k string) wdThinking { panic("not implemented") }

func wdCadenceOf(o *jsObject, k string) *wdCadence { panic("not implemented") }

func wdStringsOf(o *jsObject, k string) []string { panic("not implemented") }

func wdDecodeConfig(o *jsObject) *watchdogConfig { panic("not implemented") }

func wdResolvePatch(patch *jsObject) *watchdogConfig { panic("not implemented") }

func wdParseSourceFile(path, scope string) (*jsObject, error) { panic("not implemented") }

func wdParseSessionOverride(value *jsObject) (*jsObject, error) { panic("not implemented") }

func resolveWatchdogConfigStrict(cwd string, session *jsObject) (*watchdogConfig, error) {
	panic("not implemented")
}

// resolveWatchdogConfig resolves without throwing: an invalid source disables the watchdog and is reported.
func resolveWatchdogConfig(cwd string, session *jsObject) watchdogSettingsResult {
	panic("not implemented")
}

// ── writing ──

type wdTarget struct {
	Kind  string // "main", "children" or "child"
	Agent string
}

// wdPatch is a value of a settings write that is left alone (zero), set or cleared (null).
type wdPatch[T any] struct {
	mode  int // 0 untouched, 1 set, 2 clear
	Value T
}

func wdSetTo[T any](v T) wdPatch[T] { panic("not implemented") }
func wdClear[T any]() wdPatch[T]    { panic("not implemented") }

type wdModelWrite struct {
	Scope    string // "user" or "project"
	Cwd      string
	Target   wdTarget
	Model    wdPatch[string]
	Thinking wdPatch[wdThinking]
}

func wdEnsureObjectField(parent *jsObject, key, field string, m wdMeta) (*jsObject, error) {
	panic("not implemented")
}

func wdEnsureWatchdogSettings(settings *jsObject, m wdMeta) (*jsObject, error) {
	panic("not implemented")
}

func wdTargetSettings(watchdog *jsObject, target wdTarget, m wdMeta) (*jsObject, error) {
	panic("not implemented")
}

func wdDeleteKey(o *jsObject, k string) { panic("not implemented") }

func writeUserWatchdogEnabled(enabled bool) (string, error) { panic("not implemented") }

func writeWatchdogModelSettings(in wdModelWrite) (string, error) { panic("not implemented") }
