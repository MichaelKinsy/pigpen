package pi_subagents

import (
	"strings"
	"testing"
)

func TestSubagentGuide(t *testing.T) {
	const f = "subagent-guide"
	read := func(t *testing.T, topic string) string {
		t.Helper()
		s, err := readSubagentGuide(topic)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	tw(t, f, "reads the packaged overview by default", func(t *testing.T) {
		polMatches(t, read(t, "overview"), `# pi-subagents`)
	})
	tw(t, f, "lists valid topics for an unknown topic without changing files", func(t *testing.T) {
		g := read(t, "unknown")
		polMatches(t, g, `Unknown subagents guide topic 'unknown'`)
		polMatches(t, g, `No files were changed\.`)
		eq(t, strings.Contains(g, strings.Join(guideTopics, ", ")), true, "topics")
	})
	tw(t, f, "registers the guide action for action recovery", func(t *testing.T) {
		_, ok := actions["guide"]
		eq(t, ok, true, "guide action registered")
	})
	tw(t, f, "serves the council protocol and its references without loaded skills", func(t *testing.T) {
		g := read(t, "council")
		polMatches(t, g, `# Council Mode`)
		polMatches(t, g, `skills/council-mode/references/pass-contracts\.md -->`)
		polMatches(t, g, `skills/pi-subagents/references/execution-controls\.md -->`)
		polMatches(t, g, "Completed external-job runs can use `action: \"resume\"` for provider follow-up when the registered provider exposes `followUp\\(input\\)`")
		polNotMatches(t, g, `External job profiles do not support[^.\n]*steer/resume`)
	})
	tw(t, f, "documents external CLI runner limits in packaged guide topics", func(t *testing.T) {
		polMatches(t, read(t, "tool-reference"), `External CLI agent profiles[\s\S]*native Pi child options[\s\S]*model override[\s\S]*native Pi tools`)
		polMatches(t, read(t, "agents"), `External CLI agents use their own runner contract[\s\S]*native Pi child options`)
	})
	tw(t, f, "documents failed-lane recovery boundaries in packaged guide topics", func(t *testing.T) {
		w, tr := read(t, "workflows"), read(t, "tool-reference")
		for _, re := range []string{
			`subagent workflow[\s\S]*child launch[\s\S]*prompt runtime[\s\S]*extension loading[\s\S]*child tooling setup[\s\S]*lane infrastructure blocker`,
			`exact failure[\s\S]*run/status[\s\S]*(?:repo|repository)/cwd/worktree/branch/ref`, `clean[\s\S]*partial diff`, `same-protocol retry`, `asking the owner`,
			`external/foreground/CLI fallback requires explicit owner approval`, `Pi core[\s\S]*pi -ne[\s\S]*out-of-repo hint[\s\S]*not protocol-approved fallback`,
		} {
			polMatches(t, w, re)
		}
		polMatches(t, tr, `lane infrastructure blocker[\s\S]*external/foreground/CLI fallback requires explicit owner approval[\s\S]*interactive_shell[\s\S]*pi -ne`)
	})
	tw(t, f, "keeps advanced workflow details in the packaged guide", func(t *testing.T) {
		g := read(t, "workflows")
		polMatches(t, g, `### Parallel sequential lanes[\s\S]*runs\.lanes`)
		polMatches(t, g, `### Host command steps[\s\S]*runs\.host`)
		polMatches(t, g, `### Advanced rolling child runs[\s\S]*Promise\.race[\s\S]*Promise\.all`)
	})
}
