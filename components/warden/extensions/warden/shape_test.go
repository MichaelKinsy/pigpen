package warden

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Twins of the completeConfig half of tests/shape.test.ts (the task spine half is spine_test.go). A "stale" config is
// built the way a partly updated module graph would hand one over: the default config with sections or keys deleted,
// decoded into Config, so what was deleted is the zero value.

func staleConfig(t *testing.T, edit func(m map[string]any)) Config {
	t.Helper()
	data, err := json.Marshal(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	data, _ = json.Marshal(m)
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// twin: tests/shape.test.ts:7
func TestCompleteConfigPassesACompleteConfigThrough(t *testing.T) {
	cfg := DefaultConfig()
	got, missing := CompleteConfig(cfg)
	eq(t, "missing", len(missing), 0)
	if !reflect.DeepEqual(got, cfg) {
		t.Errorf("a complete config changed:\n got %+v\nwant %+v", got, cfg)
	}
	eq(t, "schema", ConfigSchema, 11) // upstream: CONFIG_SCHEMA == EXPECTED_SCHEMA == 11
}

// twin: tests/shape.test.ts:15
func TestCompleteConfigDisablesTheLiveCrashShape(t *testing.T) {
	legacy := staleConfig(t, func(m map[string]any) {
		delete(m, "security")
		delete(m, "context")
		m["slop"] = map[string]any{"enabled": true, "threshold": 0.7}
	})
	got, missing := CompleteConfig(legacy)
	eq(t, "missing", missing, []string{"security", "context", "slop.prose"})
	eq(t, "prose off", got.Slop.Prose.Enabled, false)
	eq(t, "security off", got.Security.Enabled, false)
	eq(t, "context off", got.Context.Enabled, false)
	eq(t, "present sections keep working", got.Action.Enabled, true)
	// The exact expression that threw in the live sessions.
	_ = got.Slop.Enabled && got.Slop.Prose.Enabled && 300 >= got.Slop.Prose.MinChars
	zero := 1
	msg := ShapeWarning(missing, nil)
	for _, want := range []string{"security, context, slop.prose", "schema pre-3", "extension expects 11", "restart Pi"} {
		if !strings.Contains(msg, want) {
			t.Errorf("warning %q lacks %q", msg, want)
		}
	}
	if !strings.Contains(ShapeWarning(missing, &zero), "schema 1,") {
		t.Error("a loaded schema is named")
	}
	_, none := CompleteConfig(Config{})
	if len(none) < 9 {
		t.Errorf("an empty config names its missing sections: %v", none)
	}
	empty, _ := CompleteConfig(Config{})
	eq(t, "empty is off (D1: the port's default is off, upstream's is on)", empty.Enabled, false)
	eq(t, "empty mode", empty.Mode, "steer")
}

// twin: tests/shape.test.ts:34
func TestCompleteConfigWithoutRunawayAndNotify(t *testing.T) {
	older := staleConfig(t, func(m map[string]any) {
		delete(m, "runaway")
		delete(m, "notify")
		delete(m["widget"].(map[string]any), "runaway")
	})
	got, missing := CompleteConfig(older)
	eq(t, "missing", missing, []string{"runaway", "notify"})
	eq(t, "runaway", got.Runaway.Enabled, false)
	eq(t, "notify", got.Notify.Enabled, false)
	eq(t, "widget line", got.Widget.Runaway, DefaultConfig().Widget.Runaway)
}

// twin: tests/shape.test.ts:48
func TestCompleteConfigRulesSectionWithoutTheSoftTier(t *testing.T) {
	older := staleConfig(t, func(m map[string]any) { delete(m["rules"].(map[string]any), "softThreshold") })
	got, missing := CompleteConfig(older)
	eq(t, "missing", len(missing), 0)
	eq(t, "soft tier off", got.Rules.SoftThreshold, 0.0)
	set := DefaultConfig()
	set.Rules.SoftThreshold = 0.5
	kept, _ := CompleteConfig(set)
	eq(t, "kept", kept.Rules.SoftThreshold, 0.5)
}

// twin: tests/shape.test.ts:60
func TestCompleteConfigWithoutTheRulesSection(t *testing.T) {
	older := staleConfig(t, func(m map[string]any) {
		delete(m, "rules")
		delete(m["widget"].(map[string]any), "rules")
	})
	got, missing := CompleteConfig(older)
	eq(t, "missing", missing, []string{"rules"})
	eq(t, "rules off", got.Rules.Enabled, false)
	eq(t, "sensitivePaths", got.Rules.SensitivePaths, map[string]string{})
	eq(t, "widget line", got.Widget.Rules, DefaultConfig().Widget.Rules)
}

// twin: tests/shape.test.ts:73
func TestCompleteConfigWithoutTheSubagentSection(t *testing.T) {
	older := staleConfig(t, func(m map[string]any) {
		delete(m, "subagent")
		delete(m["widget"].(map[string]any), "subagent")
	})
	got, missing := CompleteConfig(older)
	eq(t, "missing", missing, []string{"subagent"})
	eq(t, "triage off", got.Subagent.Enabled, false)
	eq(t, "widget line", got.Widget.Subagent, DefaultConfig().Widget.Subagent)
	// Upstream's second half, guardCurrentSections on a stale shape module, has no Go counterpart: there is one package.
}

// twin: tests/shape.test.ts:112
func TestCompleteConfigKeepsActionRuleListsNonNil(t *testing.T) {
	older := staleConfig(t, func(m map[string]any) {
		a := m["action"].(map[string]any)
		delete(a, "commandRules")
		delete(a, "commandDenyRules")
		delete(a, "exemptRules")
	})
	got, _ := CompleteConfig(older)
	if got.Action.CommandRules == nil || got.Action.CommandDenyRules == nil || got.Action.ExemptRules == nil {
		t.Error("the rule lists must stay present")
	}
	if len(got.Action.CommandRules)+len(got.Action.CommandDenyRules)+len(got.Action.ExemptRules) != 0 {
		t.Error("and empty")
	}
}
