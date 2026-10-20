package pi_subagents

import (
	"os"
	"path/filepath"
	"testing"
)

// Twin of test/unit/writer-budget-guidance.test.ts: the original reads its tool reference, its skill and its review-loop prompt
// from the package root; the port ships the same three files (docs/tool-reference.md, skills/pigpen-subagents/SKILL.md and
// prompts/review-loop.md, copies of the original's) at the root of the component.
func TestWriterBudgetGuidance(t *testing.T) {
	tw(t, "writer-budget-guidance", "keeps hard tool and usage caps off mutation-capable workers", func(t *testing.T) {
		read := func(file string) string {
			t.Helper()
			b, err := os.ReadFile(filepath.Join("..", "..", file))
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
		toolReference := read("docs/tool-reference.md")
		for _, text := range []string{toolReference, read("skills/pigpen-subagents/SKILL.md"), read("prompts/review-loop.md")} {
			polMatches(t, text, "As a conservative orchestration policy, do not (?:pass|set) a hard `toolBudget`")
			polMatches(t, text, `default tool budget blocks read/search tools rather than mutation tools`, "i")
			polMatches(t, text, `checkpoint after the current tool returns`)
			polMatches(t, text, `changed files`)
			polMatches(t, text, `build/test state`)
			polMatches(t, text, `commit or PR state`)
		}
		polMatches(t, toolReference, `elapsed timeout is not a mutation-safe boundary`, "i")
	})
}
