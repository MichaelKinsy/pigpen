package posthorse

// Coverage-audit regression tests of lane A (port/review/coverage.md C-03, C-07, C-09, C-18).

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// covToolList answers getAllTools with one active tool whose parameters are the given JSON.
func covToolList(parameters string) fixOpt {
	return withHostAnswer("getAllTools", func(map[string]any) (any, string) {
		var parsed any
		if err := json.Unmarshal([]byte(parameters), &parsed); err != nil {
			return nil, err.Error()
		}
		return rec{"tools": []any{rec{"name": "probe", "description": "probe tool", "parameters": parsed, "sourceInfo": rec{"path": "x", "source": "x", "scope": "user", "origin": "top-level"}}}}, ""
	})
}

// C-07: the port-only token cache is keyed on the definition; a parameters-only change must change the budget.
func TestCoverageToolTokenCacheSeesParameterChanges(t *testing.T) {
	f := newFixture(t, withUsage(8_192, 100), withModelWindow(8_192), withEnabled(false), withHostAnswer("getActiveTools", func(map[string]any) (any, string) { return rec{"tools": []any{"probe"}}, "" }))
	f.Set(covToolList(`{"type":"object","properties":{}}`))
	before := handoffLimit(t, f)
	f.Set(covToolList(`{"type":"object","properties":{"` + strings.Repeat("p", 400) + `":{"type":"string","description":"` + strings.Repeat("d", 400) + `"}}}`))
	after := handoffLimit(t, f)
	if after >= before {
		t.Fatalf("a larger schema must lower the handoff limit: %d -> %d", before, after)
	}
	f.Set(covToolList(`{"type":"object","properties":{}}`))
	if again := handoffLimit(t, f); again != before {
		t.Fatalf("restoring the schema restores the limit: %d, want %d", again, before)
	}
}

// C-09: upstream posthorse.test.ts:2130 asserts the request-local guidance message is not displayed, and
// that agent_settled drops the run's forced-prompt option so idle evaluations use the section again.
func TestCoverageForcedPromptGuidanceIsAHiddenMessageUntilTheRunSettles(t *testing.T) {
	f := newFixture(t)
	f.BeforeAgentStart("", rec{"sections": rec{}, "forceSystemPrompt": "Forced"})
	res := f.contextLike("context_with_system", []rec{{"role": "system", "content": "Forced", "timestamp": float64(0)}})
	list, _ := res["messages"].([]any)
	if len(list) != 2 {
		t.Fatalf("forced prompt: messages = %v", res)
	}
	custom := list[1].(map[string]any)
	if custom["role"] != "custom" || custom["customType"] != "posthorse-guidance" || custom["display"] != false {
		t.Errorf("the guidance message is a hidden custom message: %v", custom)
	}
	f.Fire("agent_settled", rec{})
	after := f.contextLike("context_with_system", []rec{{"role": "system", "content": "", "sections": rec{}, "timestamp": float64(0)}})
	messages, _ := after["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("after agent_settled the guidance is a section again: %v", after)
	}
	if section, _ := messages[0].(map[string]any)["sections"].(map[string]any)["posthorse"].(string); !strings.HasPrefix(section, "<posthorse>\n") {
		t.Errorf("section = %q", section)
	}
}

// C-18 (official-host.ts:409): the fresh window after a reset sizes itself from the declared window until usage
// is known, and a rejected handoff commits no boundary.
func TestCoverageRoutedResetGuidanceAndRejectedHandoff(t *testing.T) {
	f := newFixture(t)
	fresh := f.With(withUsageUnknown(100_000), withModelWindow(100_000), withEnabled(true))
	coreMatch(t, fresh.GuidanceFor(), `Configured context capacity: 100,000 tokens`)

	small := f.With(withUsage(8_192, 100), withModelWindow(100_000), withEnabled(false))
	coreFails(t, small, "new_context", rec{"handoff": strings.Repeat("x", 20_000)}, `Handoff is too large for the active model`)
	boundary := small.TurnEnd(rec{"toolName": "new_context", "toolCallId": "id"})
	if entries, _ := boundary["entries"].([]any); len(entries) != 0 {
		t.Errorf("the rejected handoff must not commit a boundary: %v", boundary)
	}
}

// C-18 (official-host.ts:484): the limit is an exact value. The prompt is sized so that the admission arithmetic
// leaves exactly 2,250 tokens (limit 4,500), as the oracle's test does: 4,500 is accepted, 4,501 and 5,000 are not.
func TestCoverageExactHandoffLimit(t *testing.T) {
	f := newFixture(t)
	base := []fixOpt{withUsage(32_768, 100), withModelWindow(32_768), withEnabled(false)}
	limitFor := func(promptChars int) int {
		return handoffLimit(t, f.With(append(base, withSystemPrompt(strings.Repeat("s", promptChars)))...))
	}
	// The limit falls as the prompt grows: find the first prompt (in whole tokens) that leaves a limit <= 4,500.
	lo, hi := 20_000, 40_000 // below ~20,000 prompt tokens the limit is capped at 20,000 and no refusal names it
	for lo < hi {
		mid := (lo + hi) / 2
		if limitFor(mid*4) <= 4_500 {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	chars := hi * 4
	if got := limitFor(chars); got != 4_500 {
		t.Fatalf("the admission arithmetic must reach exactly 4,500, got %d at %d prompt characters", got, chars)
	}
	view := f.With(append(base, withSystemPrompt(strings.Repeat("s", chars)))...)
	coreFails(t, view, "new_context", rec{"handoff": strings.Repeat("x", 5_000)}, `too large.*limit 4,500`)
	coreFails(t, view, "new_context", rec{"handoff": strings.Repeat("x", 4_501)}, `too large.*limit 4,500`)
	if _, failure := view.RunErr("new_context", rec{"handoff": strings.Repeat("y", 4_500)}); failure != "" {
		t.Errorf("a handoff at the exact limit is accepted: %s", failure)
	}
}

// C-03 (native-host.ts:2302 reads=6/50): many parallel sibling page reads share one reservation. Whatever the
// order, every page fits 40,000 characters, a refusal names the call's own offset, and a delivered page that is not
// the end says how to continue.
func TestCoverageParallelPageReadsShareTheReservation(t *testing.T) {
	for _, reads := range []int{6, 50} {
		t.Run(strconv.Itoa(reads), func(t *testing.T) {
			dir := coreReal(t)
			f := newFixture(t)
			entry := rec{"type": "message", "id": "long", "message": rec{"role": "user", "content": strings.Repeat("h", 120_000)}}
			base := coreUsage(f, 100_000, 60_000, 16_384, false).With(coreBranch(dir, entry)...).PageContext()
			coreWrite(t, filepath.Join(dir, ".pi", "notes", "long.md"), strings.Repeat("n", 120_000))
			coreTurnStart(base)
			type call struct {
				run    *fixRun
				tool   string
				offset int
			}
			calls := make([]call, reads)
			for i := range calls {
				offset := (i % 5) * 4_000
				tool, params := "history", rec{"op": "read", "id": "long", "offset": float64(offset)}
				if i%2 == 1 {
					tool, params = "notes", rec{"op": "read", "path": "long.md", "offset": float64(offset)}
				}
				calls[i] = call{run: base.StartRun(tool, params), tool: tool, offset: offset}
			}
			refused, delivered := 0, 0
			for _, c := range calls {
				result, message, failed := c.run.WaitFailed()
				if failed {
					refused++
					if message != "" && !strings.Contains(message, fmt.Sprintf("retry with offset %d", c.offset)) {
						t.Errorf("%s refusal must name its own offset %d: %q", c.tool, c.offset, message)
					}
					continue
				}
				delivered++
				text := result.Text()
				if u16len(text) > maxPageChars+400 {
					t.Errorf("%s page of %d characters exceeds the page cap", c.tool, u16len(text))
				}
				if !regexp.MustCompile(`continue with offset|More remains`).MatchString(text) {
					t.Errorf("%s page of a 120,000-character source must say how to continue: %q", c.tool, text[max(0, len(text)-120):])
				}
			}
			if delivered == 0 || reads == 50 && refused == 0 {
				t.Errorf("a shared reservation delivers pages (and at 50 siblings refuses the rest): delivered %d refused %d", delivered, refused)
			}
		})
	}
}
