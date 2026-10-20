package pi_subagents

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// Watchdog settings (watchdog/settings.ts): `subagents.watchdog.*` of the user's settings.json, the project's and a session
// override, each validated field by field, merged, and resolved against the defaults. The patches are ordered objects
// (jsjson.go) merged like the original's deepMerge; the result is decoded into watchdogConfig.

type wdMeta struct {
	scope string
	path  string
}

func (m wdMeta) sourceName() string {
	if m.path != "" {
		return "'" + m.path + "'"
	}
	return "session override"
}

func wdInvalid(m wdMeta, field, expected string) error {
	return errors.New("Watchdog settings in " + m.sourceName() + " have invalid '" + field + "'; expected " + expected + ".")
}

func wdUnknown(m wdMeta, field string) error {
	return errors.New("Watchdog settings in " + m.sourceName() + " have unknown field '" + field + "'.")
}

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
	for _, k := range input.order() {
		if !wdContains(allowed, k) {
			return wdUnknown(m, prefix+"."+k)
		}
	}
	return nil
}

func wdParseObject(v any, field string, m wdMeta) (*jsObject, error) {
	if o, ok := v.(*jsObject); ok {
		return o, nil
	}
	return nil, wdInvalid(m, field, "an object")
}

func wdParseBoolean(v any, field string, m wdMeta) (bool, error) {
	if b, ok := v.(bool); ok {
		return b, nil
	}
	return false, wdInvalid(m, field, "a boolean")
}

func wdParseNonEmptyString(v any, field string, m wdMeta) (string, error) {
	if s, ok := v.(string); ok && jsTrim(s) != "" {
		return jsTrim(s), nil
	}
	return "", wdInvalid(m, field, "a non-empty string")
}

func wdParseThinking(v any, field string, m wdMeta) (any, error) {
	if b, ok := v.(bool); ok && !b {
		return false, nil
	}
	if s, ok := v.(string); ok && wdContains(thinkingLevelList, s) {
		return s, nil
	}
	var quoted []string
	for _, l := range thinkingLevelList {
		quoted = append(quoted, "'"+l+"'")
	}
	return nil, wdInvalid(m, field, strings.Join(quoted, " or ")+" or false")
}

func wdIsInteger(v any) (float64, bool) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
		return 0, false
	}
	return f, true
}

func wdParseInteger(v any, field string, m wdMeta, expected string, check func(float64) bool) (float64, error) {
	if f, ok := wdIsInteger(v); ok && check(f) {
		return f, nil
	}
	return 0, wdInvalid(m, field, expected)
}

func wdParseEnum(v any, field string, m wdMeta, values []string) (string, error) {
	if s, ok := v.(string); ok && wdContains(values, s) {
		return s, nil
	}
	quoted := make([]string, len(values))
	for i, x := range values {
		quoted[i] = "'" + x + "'"
	}
	return "", wdInvalid(m, field, strings.Join(quoted, " or "))
}

func wdHasKey(o *jsObject, k string) bool { _, ok := o.vals[k]; return ok }

func wdParseGuidance(v any, field string, m wdMeta) (*jsObject, error) {
	in, err := wdParseObject(v, field, m)
	if err != nil {
		return nil, err
	}
	if err := wdAssertKnown(in, wdGuidanceFields, field, m); err != nil {
		return nil, err
	}
	patch := newObject()
	if wdHasKey(in, "watchdogMd") {
		b, err := wdParseBoolean(in.vals["watchdogMd"], field+".watchdogMd", m)
		if err != nil {
			return nil, err
		}
		patch.set("watchdogMd", b)
	}
	return patch, nil
}

func wdParseScope(v any, field string, m wdMeta) (*jsObject, error) {
	in, err := wdParseObject(v, field, m)
	if err != nil {
		return nil, err
	}
	if err := wdAssertKnown(in, wdScopeFields, field, m); err != nil {
		return nil, err
	}
	patch := newObject()
	if wdHasKey(in, "enabled") {
		b, err := wdParseBoolean(in.vals["enabled"], field+".enabled", m)
		if err != nil {
			return nil, err
		}
		patch.set("enabled", b)
	}
	return patch, nil
}

func wdParseCadence(v any, field string, m wdMeta) (*jsObject, error) {
	in, err := wdParseObject(v, field, m)
	if err != nil {
		return nil, err
	}
	if err := wdAssertKnown(in, wdCadenceFields, field, m); err != nil {
		return nil, err
	}
	patch := newObject()
	if wdHasKey(in, "everyNTools") {
		if in.vals["everyNTools"] == nil {
			patch.set("everyNTools", nil)
		} else {
			f, err := wdParseInteger(in.vals["everyNTools"], field+".everyNTools", m, "null or an integer >= 5", func(c float64) bool { return c >= 5 })
			if err != nil {
				return nil, err
			}
			patch.set("everyNTools", f)
		}
	}
	return patch, nil
}

func wdParseEndpoint(v any, field string, m wdMeta) (*jsObject, error) {
	in, err := wdParseObject(v, field, m)
	if err != nil {
		return nil, err
	}
	if err := wdAssertKnown(in, wdEndpointFields, field, m); err != nil {
		return nil, err
	}
	patch := newObject()
	if err := wdParseCommon(in, patch, field, m); err != nil {
		return nil, err
	}
	return patch, nil
}

// wdParseCommon parses the enabled, model and thinking fields shared by endpoints, children and overrides.
func wdParseCommon(in, patch *jsObject, field string, m wdMeta) error {
	if wdHasKey(in, "enabled") {
		b, err := wdParseBoolean(in.vals["enabled"], field+".enabled", m)
		if err != nil {
			return err
		}
		patch.set("enabled", b)
	}
	if wdHasKey(in, "model") {
		s, err := wdParseNonEmptyString(in.vals["model"], field+".model", m)
		if err != nil {
			return err
		}
		patch.set("model", s)
	}
	if wdHasKey(in, "thinking") {
		th, err := wdParseThinking(in.vals["thinking"], field+".thinking", m)
		if err != nil {
			return err
		}
		patch.set("thinking", th)
	}
	return nil
}

func wdParseChildOverride(v any, field string, m wdMeta) (*jsObject, error) {
	in, err := wdParseObject(v, field, m)
	if err != nil {
		return nil, err
	}
	if err := wdAssertKnown(in, wdOverrideFields, field, m); err != nil {
		return nil, err
	}
	patch := newObject()
	if err := wdParseCommon(in, patch, field, m); err != nil {
		return nil, err
	}
	if wdHasKey(in, "cadence") {
		c, err := wdParseCadence(in.vals["cadence"], field+".cadence", m)
		if err != nil {
			return nil, err
		}
		patch.set("cadence", c)
	}
	return patch, nil
}

func wdParseChildren(v any, field string, m wdMeta) (*jsObject, error) {
	in, err := wdParseObject(v, field, m)
	if err != nil {
		return nil, err
	}
	if err := wdAssertKnown(in, wdChildrenFields, field, m); err != nil {
		return nil, err
	}
	patch := newObject()
	if err := wdParseCommon(in, patch, field, m); err != nil {
		return nil, err
	}
	if wdHasKey(in, "watchdogTailTimeoutMs") {
		f, err := wdParseInteger(in.vals["watchdogTailTimeoutMs"], field+".watchdogTailTimeoutMs", m, "a positive integer", func(c float64) bool { return c >= 1 })
		if err != nil {
			return nil, err
		}
		patch.set("watchdogTailTimeoutMs", f)
	}
	if wdHasKey(in, "cadence") {
		c, err := wdParseCadence(in.vals["cadence"], field+".cadence", m)
		if err != nil {
			return nil, err
		}
		patch.set("cadence", c)
	}
	if wdHasKey(in, "overrides") {
		overrides, err := wdParseObject(in.vals["overrides"], field+".overrides", m)
		if err != nil {
			return nil, err
		}
		out := newObject()
		for _, agent := range overrides.order() {
			if jsTrim(agent) == "" {
				return nil, wdInvalid(m, field+".overrides", "agent names to be non-empty")
			}
			o, err := wdParseChildOverride(overrides.vals[agent], field+".overrides."+agent, m)
			if err != nil {
				return nil, err
			}
			out.set(agent, o)
		}
		patch.set("overrides", out)
	}
	return patch, nil
}

func wdParseLsp(v any, field string, m wdMeta) (*jsObject, error) {
	in, err := wdParseObject(v, field, m)
	if err != nil {
		return nil, err
	}
	if err := wdAssertKnown(in, wdLspFields, field, m); err != nil {
		return nil, err
	}
	patch := newObject()
	if wdHasKey(in, "enabled") {
		b, err := wdParseBoolean(in.vals["enabled"], field+".enabled", m)
		if err != nil {
			return nil, err
		}
		patch.set("enabled", b)
	}
	for _, k := range []struct {
		key, expected string
		min           float64
	}{{"timeoutMs", "a positive integer", 1}, {"maxFiles", "a positive integer", 1}, {"maxDiagnostics", "a non-negative integer", 0}} {
		if wdHasKey(in, k.key) {
			min := k.min
			f, err := wdParseInteger(in.vals[k.key], field+"."+k.key, m, k.expected, func(c float64) bool { return c >= min })
			if err != nil {
				return nil, err
			}
			patch.set(k.key, f)
		}
	}
	return patch, nil
}

func wdParseStringList(v any, field string, m wdMeta) ([]any, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, wdInvalid(m, field, "an array of non-empty strings")
	}
	out := make([]any, len(arr))
	for i, e := range arr {
		s, isString := e.(string)
		if !isString || jsTrim(s) == "" {
			return nil, wdInvalid(m, field, "an array of non-empty strings")
		}
		out[i] = jsTrim(s)
	}
	return out, nil
}

func wdParseRoleRule(v any, field string, m wdMeta) (*jsObject, error) {
	in, err := wdParseObject(v, field, m)
	if err != nil {
		return nil, err
	}
	if err := wdAssertKnown(in, wdRoleRuleFields, field, m); err != nil {
		return nil, err
	}
	rule := newObject()
	for _, k := range []string{"allow", "deny"} {
		if wdHasKey(in, k) {
			l, err := wdParseStringList(in.vals[k], field+"."+k, m)
			if err != nil {
				return nil, err
			}
			rule.set(k, l)
		}
	}
	if wdHasKey(in, "note") {
		s, err := wdParseNonEmptyString(in.vals["note"], field+".note", m)
		if err != nil {
			return nil, err
		}
		rule.set("note", s)
	}
	return rule, nil
}

func wdParseRules(v any, field string, m wdMeta) (*jsObject, error) {
	in, err := wdParseObject(v, field, m)
	if err != nil {
		return nil, err
	}
	if err := wdAssertKnown(in, wdRulesFields, field, m); err != nil {
		return nil, err
	}
	rules := newObject()
	rules.set("action", "warn")
	roleModels := newObject()
	rules.set("roleModels", roleModels)
	if wdHasKey(in, "action") {
		a, err := wdParseEnum(in.vals["action"], field+".action", m, []string{"warn", "block"})
		if err != nil {
			return nil, err
		}
		rules.set("action", a)
	}
	if wdHasKey(in, "roleModels") {
		models, err := wdParseObject(in.vals["roleModels"], field+".roleModels", m)
		if err != nil {
			return nil, err
		}
		for _, agent := range models.order() {
			if jsTrim(agent) == "" {
				return nil, wdInvalid(m, field+".roleModels", "agent names to be non-empty")
			}
			rule, err := wdParseRoleRule(models.vals[agent], field+".roleModels."+agent, m)
			if err != nil {
				return nil, err
			}
			roleModels.set(agent, rule)
		}
	}
	return rules, nil
}

func wdParsePatch(v any, field string, m wdMeta) (*jsObject, error) {
	in, err := wdParseObject(v, field, m)
	if err != nil {
		return nil, err
	}
	if err := wdAssertKnown(in, wdWatchdogFields, field, m); err != nil {
		return nil, err
	}
	patch := newObject()
	for _, k := range []string{"enabled", "clarification"} {
		if wdHasKey(in, k) {
			b, err := wdParseBoolean(in.vals[k], field+"."+k, m)
			if err != nil {
				return nil, err
			}
			patch.set(k, b)
		}
	}
	if wdHasKey(in, "agentEndTimeoutMs") {
		f, err := wdParseInteger(in.vals["agentEndTimeoutMs"], field+".agentEndTimeoutMs", m, "a positive integer", func(c float64) bool { return c >= 1 })
		if err != nil {
			return nil, err
		}
		patch.set("agentEndTimeoutMs", f)
	}
	if wdHasKey(in, "severityThreshold") {
		s, err := wdParseEnum(in.vals["severityThreshold"], field+".severityThreshold", m, watchdogWarningSeverities)
		if err != nil {
			return nil, err
		}
		patch.set("severityThreshold", s)
	}
	if wdHasKey(in, "maxWarnings") {
		if in.vals["maxWarnings"] == nil {
			patch.set("maxWarnings", nil)
		} else {
			f, err := wdParseInteger(in.vals["maxWarnings"], field+".maxWarnings", m, "null or a non-negative integer", func(c float64) bool { return c >= 0 })
			if err != nil {
				return nil, err
			}
			patch.set("maxWarnings", f)
		}
	}
	if wdHasKey(in, "guidance") {
		g, err := wdParseGuidance(in.vals["guidance"], field+".guidance", m)
		if err != nil {
			return nil, err
		}
		patch.set("guidance", g)
	}
	if wdHasKey(in, "stalemateRepeats") {
		f, err := wdParseInteger(in.vals["stalemateRepeats"], field+".stalemateRepeats", m, "a positive integer", func(c float64) bool { return c >= 1 })
		if err != nil {
			return nil, err
		}
		patch.set("stalemateRepeats", f)
	}
	for _, s := range []struct {
		key   string
		parse func(any, string, wdMeta) (*jsObject, error)
	}{{"scope", wdParseScope}, {"cadence", wdParseCadence}, {"main", wdParseEndpoint}, {"children", wdParseChildren}, {"lsp", wdParseLsp}, {"rules", wdParseRules}} {
		if wdHasKey(in, s.key) {
			o, err := s.parse(in.vals[s.key], field+"."+s.key, m)
			if err != nil {
				return nil, err
			}
			patch.set(s.key, o)
		}
	}
	return patch, nil
}

func wdParseSettingsObject(settings *jsObject, m wdMeta) (*jsObject, error) {
	if !wdHasKey(settings, "subagents") {
		return newObject(), nil
	}
	subagents, err := wdParseObject(settings.vals["subagents"], "subagents", m)
	if err != nil {
		return nil, err
	}
	if !wdHasKey(subagents, "watchdog") {
		return newObject(), nil
	}
	return wdParsePatch(subagents.vals["watchdog"], "subagents.watchdog", m)
}

func getWatchdogUserSettingsPath() string { return filepath.Join(agentDir(), "settings.json") }

func wdIsDir(dir string) bool {
	st, err := os.Stat(dir)
	return err == nil && st.IsDir()
}

func wdProjectSettingsPath(cwd string) string {
	current := cwd
	for {
		if wdIsDir(getProjectConfigDir(current)) || wdIsDir(filepath.Join(current, ".agents")) {
			return filepath.Join(getProjectConfigDir(current), "settings.json")
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

func getWatchdogProjectSettingsPath(cwd string) string {
	return filepath.Join(getProjectConfigDir(cwd), "settings.json")
}

// wdDeepMerge is deepMerge: plain objects merge key by key, everything else is replaced.
func wdDeepMerge(base, patch *jsObject) *jsObject {
	next := base.clone()
	for _, k := range patch.order() {
		v := patch.vals[k]
		cur, curIsObj := next.vals[k].(*jsObject)
		val, valIsObj := v.(*jsObject)
		if curIsObj && valIsObj {
			next.set(k, wdDeepMerge(cur, val))
		} else {
			next.set(k, v)
		}
	}
	return next
}

func wdDefaultObject() *jsObject {
	o := func(kv ...any) *jsObject {
		out := newObject()
		for i := 0; i+1 < len(kv); i += 2 {
			out.set(kv[i].(string), kv[i+1])
		}
		return out
	}
	return o(
		"enabled", false, "clarification", false, "agentEndTimeoutMs", float64(30000), "severityThreshold", "concern", "maxWarnings", nil,
		"guidance", o("watchdogMd", true), "stalemateRepeats", float64(3), "scope", o("enabled", true), "cadence", o("everyNTools", nil),
		"main", o("enabled", false),
		"children", o("enabled", false, "watchdogTailTimeoutMs", float64(120000), "overrides", newObject()),
		"lsp", o("enabled", true, "timeoutMs", float64(3000), "maxFiles", float64(20), "maxDiagnostics", float64(50)),
	)
}

// defaultWatchdogConfig is DEFAULT_WATCHDOG_CONFIG (a fresh copy: nothing in it is shared).
func defaultWatchdogConfig() *watchdogConfig { return wdDecodeConfig(wdDefaultObject()) }

func wdInt(o *jsObject, k string) int {
	f, _ := o.num(k)
	return int(f)
}

func wdBool(o *jsObject, k string) bool {
	b, _ := o.flag(k)
	return b
}

func wdOptInt(o *jsObject, k string) *int {
	if f, ok := o.num(k); ok {
		n := int(f)
		return &n
	}
	return nil
}

func wdThinkingOf(o *jsObject, k string) wdThinking {
	switch v := o.vals[k].(type) {
	case string:
		return wdThinking{Level: v}
	case bool:
		if !v {
			return wdThinking{Off: true}
		}
	}
	return wdThinking{}
}

func wdCadenceOf(o *jsObject, k string) *wdCadence {
	c := o.obj(k)
	if c == nil {
		return nil
	}
	return &wdCadence{EveryNTools: wdOptInt(c, "everyNTools")}
}

func wdStringsOf(o *jsObject, k string) []string {
	arr, ok := o.vals[k].([]any)
	if !ok {
		return nil
	}
	out := make([]string, len(arr))
	for i, e := range arr {
		out[i], _ = e.(string)
	}
	return out
}

func wdDecodeConfig(o *jsObject) *watchdogConfig {
	c := &watchdogConfig{
		Enabled: wdBool(o, "enabled"), Clarification: wdBool(o, "clarification"), AgentEndTimeoutMs: wdInt(o, "agentEndTimeoutMs"),
		MaxWarnings: wdOptInt(o, "maxWarnings"), StalemateRepeats: wdInt(o, "stalemateRepeats"),
	}
	c.SeverityThreshold, _ = o.str("severityThreshold")
	if g := o.obj("guidance"); g != nil {
		c.GuidanceWatchdogMd = wdBool(g, "watchdogMd")
	}
	if s := o.obj("scope"); s != nil {
		c.ScopeEnabled = wdBool(s, "enabled")
	}
	if cad := wdCadenceOf(o, "cadence"); cad != nil {
		c.Cadence = *cad
	}
	if m := o.obj("main"); m != nil {
		c.Main.Enabled = wdBool(m, "enabled")
		c.Main.Model, _ = m.str("model")
		c.Main.Thinking = wdThinkingOf(m, "thinking")
	}
	if ch := o.obj("children"); ch != nil {
		c.Children.Enabled = wdBool(ch, "enabled")
		c.Children.Model, _ = ch.str("model")
		c.Children.Thinking = wdThinkingOf(ch, "thinking")
		c.Children.WatchdogTailTimeoutMs = wdInt(ch, "watchdogTailTimeoutMs")
		c.Children.Cadence = wdCadenceOf(ch, "cadence")
		if ov := ch.obj("overrides"); ov != nil {
			for _, agent := range ov.order() {
				e := ov.obj(agent)
				if e == nil {
					continue
				}
				entry := wdChildOverride{Model: "", Thinking: wdThinkingOf(e, "thinking"), Cadence: wdCadenceOf(e, "cadence")}
				entry.Model, _ = e.str("model")
				if b, ok := e.flag("enabled"); ok {
					entry.Enabled = &b
				}
				c.Children.Overrides = append(c.Children.Overrides, wdOverrideEntry{Agent: agent, Override: entry})
			}
		}
	}
	if l := o.obj("lsp"); l != nil {
		c.Lsp = wdLsp{Enabled: wdBool(l, "enabled"), TimeoutMs: wdInt(l, "timeoutMs"), MaxFiles: wdInt(l, "maxFiles"), MaxDiagnostics: wdInt(l, "maxDiagnostics")}
	}
	if r := o.obj("rules"); r != nil {
		rules := &wdRules{}
		rules.Action, _ = r.str("action")
		if rm := r.obj("roleModels"); rm != nil {
			for _, agent := range rm.order() {
				rule := rm.obj(agent)
				if rule == nil {
					continue
				}
				entry := wdRoleModelRule{Allow: wdStringsOf(rule, "allow"), Deny: wdStringsOf(rule, "deny")}
				entry.Note, _ = rule.str("note")
				rules.RoleModels = append(rules.RoleModels, wdRoleModelEntry{Agent: agent, Rule: entry})
			}
		}
		c.Rules = rules
	}
	return c
}

func wdResolvePatch(patch *jsObject) *watchdogConfig {
	merged := wdDeepMerge(wdDefaultObject(), patch)
	config := wdDecodeConfig(merged)
	enabled, _ := patch.flag("enabled")
	config.Enabled = enabled
	config.Main.Enabled = config.Enabled
	if m := patch.obj("main"); m != nil {
		if b, ok := m.flag("enabled"); ok {
			config.Main.Enabled = b
		}
	}
	return config
}

func wdParseSourceFile(path, scope string) (*jsObject, error) {
	settings, err := readSettingsFileStrict(path)
	if err != nil {
		return nil, err
	}
	return wdParseSettingsObject(settings, wdMeta{scope: scope, path: path})
}

func wdParseSessionOverride(value *jsObject) (*jsObject, error) {
	if wdHasKey(value, "subagents") {
		return wdParseSettingsObject(value, wdMeta{scope: "session"})
	}
	return wdParsePatch(value, "subagents.watchdog", wdMeta{scope: "session"})
}

func resolveWatchdogConfigStrict(cwd string, session *jsObject) (*watchdogConfig, error) {
	patch := newObject()
	user, err := wdParseSourceFile(getWatchdogUserSettingsPath(), "user")
	if err != nil {
		return nil, err
	}
	patch = wdDeepMerge(patch, user)
	if p := wdProjectSettingsPath(cwd); p != "" {
		project, err := wdParseSourceFile(p, "project")
		if err != nil {
			return nil, err
		}
		patch = wdDeepMerge(patch, project)
	}
	if session != nil {
		s, err := wdParseSessionOverride(session)
		if err != nil {
			return nil, err
		}
		patch = wdDeepMerge(patch, s)
	}
	return wdResolvePatch(patch), nil
}

// resolveWatchdogConfig resolves without throwing: an invalid source disables the watchdog and is reported.
func resolveWatchdogConfig(cwd string, session *jsObject) watchdogSettingsResult {
	var sources []wdSettingsSource
	var errs []wdSettingsError
	patch := newObject()
	specs := []struct{ scope, path string }{{"user", getWatchdogUserSettingsPath()}, {"project", wdProjectSettingsPath(cwd)}}
	for _, s := range specs {
		if s.path == "" {
			continue
		}
		_, statErr := os.Stat(s.path)
		sources = append(sources, wdSettingsSource{Scope: s.scope, Path: s.path, Exists: statErr == nil})
		parsed, err := wdParseSourceFile(s.path, s.scope)
		if err != nil {
			errs = append(errs, wdSettingsError{Scope: s.scope, Path: s.path, Message: err.Error()})
			continue
		}
		patch = wdDeepMerge(patch, parsed)
	}
	if session != nil {
		sources = append(sources, wdSettingsSource{Scope: "session", Exists: true})
		parsed, err := wdParseSessionOverride(session)
		if err != nil {
			errs = append(errs, wdSettingsError{Scope: "session", Message: err.Error()})
		} else {
			patch = wdDeepMerge(patch, parsed)
		}
	}
	result := watchdogSettingsResult{OK: len(errs) == 0, Errors: errs, Sources: sources}
	if result.OK {
		result.Config = wdResolvePatch(patch)
	} else {
		result.Config = defaultWatchdogConfig()
	}
	return result
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

func wdSetTo[T any](v T) wdPatch[T] { return wdPatch[T]{mode: 1, Value: v} }
func wdClear[T any]() wdPatch[T]    { return wdPatch[T]{mode: 2} }

type wdModelWrite struct {
	Scope    string // "user" or "project"
	Cwd      string
	Target   wdTarget
	Model    wdPatch[string]
	Thinking wdPatch[wdThinking]
}

func wdEnsureObjectField(parent *jsObject, key, field string, m wdMeta) (*jsObject, error) {
	if !wdHasKey(parent, key) {
		parent.set(key, newObject())
	}
	o, ok := parent.vals[key].(*jsObject)
	if !ok {
		return nil, wdInvalid(m, field, "an object")
	}
	return o, nil
}

func wdEnsureWatchdogSettings(settings *jsObject, m wdMeta) (*jsObject, error) {
	subagents, err := wdEnsureObjectField(settings, "subagents", "subagents", m)
	if err != nil {
		return nil, err
	}
	return wdEnsureObjectField(subagents, "watchdog", "subagents.watchdog", m)
}

func wdTargetSettings(watchdog *jsObject, target wdTarget, m wdMeta) (*jsObject, error) {
	if target.Kind == "main" {
		return wdEnsureObjectField(watchdog, "main", "subagents.watchdog.main", m)
	}
	children, err := wdEnsureObjectField(watchdog, "children", "subagents.watchdog.children", m)
	if err != nil {
		return nil, err
	}
	if target.Kind == "children" {
		return children, nil
	}
	agent := jsTrim(target.Agent)
	if agent == "" {
		return nil, wdInvalid(m, "subagents.watchdog.children.overrides.<agent>", "a non-empty agent name")
	}
	overrides, err := wdEnsureObjectField(children, "overrides", "subagents.watchdog.children.overrides", m)
	if err != nil {
		return nil, err
	}
	return wdEnsureObjectField(overrides, agent, "subagents.watchdog.children.overrides."+agent, m)
}

func wdDeleteKey(o *jsObject, k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, key := range o.keys {
		if key == k {
			o.keys = append(o.keys[:i:i], o.keys[i+1:]...)
			break
		}
	}
}

func writeUserWatchdogEnabled(enabled bool) (string, error) {
	path := getWatchdogUserSettingsPath()
	m := wdMeta{scope: "user", path: path}
	settings, err := readSettingsFileStrict(path)
	if err != nil {
		return "", err
	}
	watchdog, err := wdEnsureWatchdogSettings(settings, m)
	if err != nil {
		return "", err
	}
	watchdog.set("enabled", enabled)
	main, err := wdTargetSettings(watchdog, wdTarget{Kind: "main"}, m)
	if err != nil {
		return "", err
	}
	main.set("enabled", enabled)
	return path, writeSettingsFile(path, settings)
}

func writeWatchdogModelSettings(in wdModelWrite) (string, error) {
	path := getWatchdogUserSettingsPath()
	if in.Scope != "user" {
		cwd := in.Cwd
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		path = getWatchdogProjectSettingsPath(cwd)
	}
	m := wdMeta{scope: in.Scope, path: path}
	settings, err := readSettingsFileStrict(path)
	if err != nil {
		return "", err
	}
	watchdog, err := wdEnsureWatchdogSettings(settings, m)
	if err != nil {
		return "", err
	}
	target, err := wdTargetSettings(watchdog, in.Target, m)
	if err != nil {
		return "", err
	}
	switch in.Model.mode {
	case 2:
		wdDeleteKey(target, "model")
	case 1:
		target.set("model", in.Model.Value)
	}
	switch in.Thinking.mode {
	case 2:
		wdDeleteKey(target, "thinking")
	case 1:
		if in.Thinking.Value.Off {
			target.set("thinking", false)
		} else {
			target.set("thinking", in.Thinking.Value.Level)
		}
	}
	return path, writeSettingsFile(path, settings)
}
