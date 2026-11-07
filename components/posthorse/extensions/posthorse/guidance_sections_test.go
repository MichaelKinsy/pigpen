package posthorse

import (
	"slices"
	"testing"
)

// guidance.ts:35-38 keeps the order of Pi's system-prompt sections (core/system-prompt.ts:
// preamble, tools, rules, docs, cwd, then the extension's own sections); the Go map the host event
// decodes into has none, and before_agent_start options carry no sections to learn it from.
func TestWithPosthorseSectionKeepsPisBuiltInSectionOrder(t *testing.T) {
	existing := rec{"cwd": "<cwd>", "docs": "<docs>", "preamble": "p", "rules": "<rules>", "tools": "<tools>", "zeta": "z", "alpha": "a"}
	got := withPosthorseSection(existing, nil, "<posthorse>").Keys()
	want := []string{"preamble", "tools", "rules", "docs", "cwd", "alpha", "zeta", "posthorse"}
	if !slices.Equal(got, want) {
		t.Fatalf("section order %v want %v", got, want)
	}
	again := withPosthorseSection(rec{"preamble": "p", "posthorse": "old", "alpha": "a", "cwd": "c"}, nil, "new")
	if want := []string{"preamble", "cwd", "alpha", "posthorse"}; !slices.Equal(again.Keys(), want) {
		t.Fatalf("section order with an earlier posthorse %v want %v", again.Keys(), want)
	}
	if v, _ := again.Get("posthorse"); v != "new" {
		t.Fatalf("posthorse text %v", v)
	}
}
