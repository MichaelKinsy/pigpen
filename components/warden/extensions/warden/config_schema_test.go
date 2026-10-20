package warden

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Expected values are the defaultConfig() literals in port/oracle/src/config.ts (line numbers cited).
func TestDefaultConfigSchema(t *testing.T) {
	c := DefaultConfig()
	cases := []struct {
		name      string
		got, want any
	}{
		{"action.feedbackLog (523)", c.Action.FeedbackLog, true},
		{"action.pathRules (527)", len(c.Action.PathRules), 0},
		{"slop.threshold (534)", c.Slop.Threshold, 0.7},
		{"slop.prose.audience (534)", c.Slop.Prose.Audience, "technical"},
		{"slop.prose.minChars (534)", c.Slop.Prose.MinChars, 200},
		{"security.maskOutput (535)", c.Security.MaskOutput, true},
		{"rules.maxChars (536)", c.Rules.MaxChars, 8000},
		{"rules.fallback (536)", c.Rules.Fallback, true},
		{"context.tailMinChars (537)", c.Context.TailMinChars, 12000},
		{"context.recallTool (537)", c.Context.RecallTool, RecallTool("auto")},
		{"context.largeOutput.threshold (537)", c.Context.LargeOutput.Threshold, 0.85},
		{"context.filter.enabled (537)", c.Context.Filter.Enabled, false},
		{"context.filter.maxKeptChars (537)", c.Context.Filter.MaxKeptChars, 6000},
		{"runaway.thinkingRepeats (538)", c.Runaway.ThinkingRepeats, 10},
		{"notify.cooldownMs (539)", c.Notify.CooldownMs, 10000},
		{"judge.cooldownMs (540)", c.Judge.CooldownMs, 60000},
		{"subagent.cooldownMs (541)", c.Subagent.CooldownMs, 120000},
		{"widget.placement (281)", c.Widget.Placement, "aboveEditor"},
		{"widget.panelWidth (281)", c.Widget.PanelWidth, "40%"},
		{"steers.minSteers (546)", c.Steers.MinSteers, 30},
		{"learning.retentionDays (547)", c.Learning.RetentionDays, 365},
		{"conscience.enabled (549)", c.Conscience.Enabled, false},
		{"conscience.maxLoadedBytes (557)", c.Conscience.MaxLoadedBytes, 65536},
		{"conscience.recommendThreshold (559)", c.Conscience.RecommendThreshold, 0.80},
		{"prefs.inject (562)", c.Prefs.Inject, true},
		{"waste.every (563)", c.Waste.Every, 20},
		{"compaction.maxSummaryTokens (565)", c.Compaction.MaxSummaryTokens, 20000},
		{"compaction.skipProviders (565)", c.Compaction.SkipProviders, []string{"claude-bridge"}},
	}
	for _, tc := range cases {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Errorf("%s: got %#v, want %#v", tc.name, tc.got, tc.want)
		}
	}
}

func TestIsRecallTool(t *testing.T) {
	for _, v := range []string{"auto", "rg", "none", "findstr"} {
		if !IsRecallTool(v) {
			t.Errorf("%s should be a recall tool", v)
		}
	}
	if IsRecallTool("ack") || IsRecallTool("") {
		t.Error("unknown value accepted")
	}
}

func TestDefaultConfigRoundTrips(t *testing.T) {
	want := DefaultConfig()
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Config
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip differs:\n got %+v\nwant %+v", got, want)
	}
}
