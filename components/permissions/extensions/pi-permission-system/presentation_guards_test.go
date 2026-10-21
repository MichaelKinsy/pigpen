package pi_permission_system

import (
	"math"
	"testing"
	"time"
)

// Guards of the presentation layer that the upstream twins and the differential do not exercise: each test pins one input class
// (budgets, label widths, control characters in a notification, edit previews, registries, registration checks, skill matching) to
// the original's answer, worked out from the original's source.

func TestRenderBudgets(t *testing.T) {
	eq(t, DefaultRenderBudget, RenderBudget{MaxRows: 24, FieldMaxWidth: 400}, "default budget")
	rows, field := 7.0, 9.0
	eq(t, ResolveRenderBudget(PromptBudgetConfig{}), RenderBudget{MaxRows: 24, FieldMaxWidth: 400}, "nothing configured")
	eq(t, ResolveRenderBudget(PromptBudgetConfig{PromptMaxRows: &rows}), RenderBudget{MaxRows: 7, FieldMaxWidth: 400}, "rows only")
	eq(t, ResolveRenderBudget(PromptBudgetConfig{PromptFieldMaxWidth: &field}), RenderBudget{MaxRows: 24, FieldMaxWidth: 9}, "field only")
	eq(t, ResolveRenderBudget(PromptBudgetConfig{PromptMaxRows: &rows, PromptFieldMaxWidth: &field}), RenderBudget{MaxRows: 7, FieldMaxWidth: 9}, "both")
	complete := CompleteViewBudget(80)
	eq(t, math.IsInf(complete.MaxRows, 1), true, "complete rows are unbounded")
	eq(t, math.IsInf(complete.FieldMaxWidth, 1), true, "complete field width is unbounded")
	eq(t, complete.Width, 80, "complete width")
}

func TestDialogLabelColumnCountsUTF16Units(t *testing.T) {
	// The label column is as wide as the longest label in UTF-16 units (padEnd), not in bytes.
	got := ppRender("tool", func(p *PromptPayload) {
		p.Evidence = []PromptEvidence{evid("\u00e9\u00e9\u00e9\u00e9\u00e9", "x", nil)}
	})
	eq(t, got, []string{"tool  : read", "\u00e9\u00e9\u00e9\u00e9\u00e9 : x"}, "lines")
}

func TestPaintOccurrencesEmptyNeedleIsNeverPainted(t *testing.T) {
	// An empty flagged element (a bash_external_directory evidence entry with no text) must not paint or loop.
	done := make(chan string, 1)
	painted := 0
	go func() {
		done <- paintOccurrences("abc", "", func(s string) string { painted++; return "[" + s + "]" })
	}()
	select {
	case got := <-done:
		eq(t, got, "abc", "line")
		eq(t, painted, 0, "paint calls")
	case <-time.After(5 * time.Second):
		t.Fatal("paintOccurrences did not return for an empty needle")
	}
}

func TestNotificationDropsEveryControlCharacter(t *testing.T) {
	// 0x1f (unit separator) is the last C0 control; 0x20 (space) is the first kept character; DEL goes too.
	eq(t, withoutControlCharacters("a\x1fb\x7fc d\x00e\x1b[31m\x07"), "abc de[31m", "stripped")
	eq(t, withoutControlCharacters("\x1f"), "", "unit separator alone")
}

func TestPatternSuggestBashSurroundingWhitespace(t *testing.T) {
	eq(t, SuggestBashPattern("  ls  "), "ls", "padded bare command")
	eq(t, SuggestBashPattern("\n\t git status\n"), "git status*", "padded command")
	eq(t, SuggestBashPattern(" \n\t "), "", "blank")
	eq(t, SuggestBashPattern("  # only a comment\n  "), "", "comment only")
}

func TestSerializeToolInputPreviewDeclinedSerialization(t *testing.T) {
	// safeJsonStringify answers undefined for what it cannot serialize: the preview is empty whatever text accompanies the decline.
	saved := safeJSONStringify
	defer func() { safeJSONStringify = saved }()
	safeJSONStringify = func(any) (string, bool) { return "{\"a\":1}", false }
	eq(t, SerializeToolInputPreview(nil), "", "declined")
	safeJSONStringify = func(any) (string, bool) { return "{\"a\":1}", true }
	eq(t, SerializeToolInputPreview(nil), "{\"a\":1}", "accepted")
}

func TestEditPreviewShapes(t *testing.T) {
	rec := func(json string) *jsObject { return jo(t, json) }
	// An edits array that is present but empty wins over a top-level oldText/newText pair.
	eq(t, FormatEditInputForPrompt(rec(`{"path":"/f","edits":[],"oldText":"a","newText":"b"}`)), "for '/f' with edit input", "empty edits array")
	// An edit needs both texts.
	eq(t, FormatEditInputForPrompt(rec(`{"path":"/f","edits":[{"oldText":"a"}]}`)), "for '/f' with edit input", "old only in an array")
	eq(t, FormatEditInputForPrompt(rec(`{"path":"/f","edits":[{"newText":"a"}]}`)), "for '/f' with edit input", "new only in an array")
	eq(t, FormatEditInputForPrompt(rec(`{"oldText":"a"}`)), "with edit input", "old only at top level")
	eq(t, FormatEditInputForPrompt(rec(`{"newText":"a"}`)), "with edit input", "new only at top level")
	// The first edit is summarized, the rest counted.
	eq(t, FormatEditInputForPrompt(rec(`{"edits":[{"oldText":"a\nb\nc","newText":"x"},{"oldText":"d","newText":"e\nf"}]}`)),
		"(2 replacements: edit #1 replaces 3 lines with 1 line, plus 1 additional edit)", "first of two")
}

func TestToolAccessExtractorRegistryNilIsNoExtractor(t *testing.T) {
	var r ToolAccessExtractorRegistry
	if _, err := r.Register("tool", nil); err != nil {
		t.Fatal(err)
	}
	eq(t, r.Resolve("tool") == nil, true, "a nil extractor resolves to nothing")
}

func TestCheckRequestedToolRegistrationMissingName(t *testing.T) {
	eq(t, CheckRequestedToolRegistration(nil, []any{"read"}, nil).Status, "missing-tool-name", "null")
	eq(t, CheckRequestedToolRegistration(sp(""), []any{"read"}, nil).Status, "missing-tool-name", "empty")
}

// TestCheckRequestedToolRegistrationExhaustive compares the check with a literal transcription of the original over every alias map,
// registered set and requested name drawn from a small alphabet (aliases may chain, point at themselves or at a missing name).
func TestCheckRequestedToolRegistrationExhaustive(t *testing.T) {
	names := []string{"a", "b", "c", "d"}
	targets := append([]string{""}, names...)
	norm := func(n string, aliases map[string]string) string {
		if c := aliases[n]; c != "" {
			return c
		}
		return n
	}
	reference := func(requested string, registered []string, aliases map[string]string) bool {
		reverse := map[string][]string{}
		for alias, canonical := range aliases {
			if canonical == "" {
				continue
			}
			found := false
			for _, e := range reverse[canonical] {
				found = found || e == alias
			}
			if !found {
				reverse[canonical] = append(reverse[canonical], alias)
			}
		}
		lookup := map[string]bool{}
		for _, value := range registered {
			lookup[value] = true
			normalized := norm(value, aliases)
			lookup[normalized] = true
			if c := aliases[value]; c != "" {
				lookup[c] = true
			}
			for _, alias := range reverse[value] {
				lookup[alias] = true
			}
			for _, alias := range reverse[normalized] {
				lookup[alias] = true
			}
		}
		return lookup[requested] || lookup[norm(requested, aliases)]
	}
	cases := 0
	var walk func(i int, aliases map[string]string)
	walk = func(i int, aliases map[string]string) {
		if i < len(names) {
			for _, target := range targets {
				next := map[string]string{}
				for k, v := range aliases {
					next[k] = v
				}
				if target != "" {
					next[names[i]] = target
				}
				walk(i+1, next)
			}
			return
		}
		for mask := 0; mask < 1<<len(names); mask++ {
			var registered []string
			var tools []any
			for j, n := range names {
				if mask>>j&1 == 1 {
					registered = append(registered, n)
					tools = append(tools, n)
				}
			}
			for _, requested := range names {
				r := requested
				got := CheckRequestedToolRegistration(&r, tools, aliases)
				want := reference(requested, registered, aliases)
				cases++
				if (got.Status == "registered") != want {
					t.Fatalf("aliases %v registered %v requested %q: status %s, original %v", aliases, registered, requested, got.Status, want)
				}
			}
		}
	}
	walk(0, map[string]string{})
	eq(t, cases, 5*5*5*5*16*4, "cases")
}

func TestFindSkillPathMatchEmptyLocations(t *testing.T) {
	normalizer := NewPathNormalizer(PosixPathFlavor, "/work")
	blank := []SkillPromptEntry{{Name: "blank"}}
	eq(t, FindSkillPathMatch("", blank, normalizer) == nil, true, "empty path, blank entry")
	eq(t, FindSkillPathMatch("/a/b.md", blank, normalizer) == nil, true, "blank base directory never contains a path")
	root := []SkillPromptEntry{{Name: "root", NormalizedBaseDir: "/"}}
	eq(t, FindSkillPathMatch("", root, normalizer) == nil, true, "empty path, root base directory")
}

func TestResolvedAliasUsesTheFirstMatchingExternalPath(t *testing.T) {
	p := ppPayload("bash_external_directory", func(p *PromptPayload) {
		p.Evidence = []PromptEvidence{
			evid("external path", "/x", sp("/first")), evid("external path", "/x", sp("/second")),
		}
	})
	eq(t, resolvedAlias(p, "/x"), " (resolves to '/first')", "first entry")
	p.Evidence = []PromptEvidence{evid("external path", "/x", nil), evid("external path", "/x", sp("/second"))}
	eq(t, resolvedAlias(p, "/x"), "", "first entry without a detail ends the search")
}

func TestUTF16LenCountsUnits(t *testing.T) {
	// An ASCII prefix, a BMP character, an astral character (two units) and a lone surrogate in WTF-8 (one unit).
	eq(t, utf16LenWTF("abc\u00e9\U0001f600\xed\xa0\x80z"), 8, "mixed")
	eq(t, utf16LenWTF(""), 0, "empty")
	eq(t, utf16LenWTF("plain ascii"), 11, "ascii")
}
