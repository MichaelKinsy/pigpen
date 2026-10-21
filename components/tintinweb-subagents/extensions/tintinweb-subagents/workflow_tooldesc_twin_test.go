package tintinweb_subagents

import (
	"regexp"
	"strings"
	"testing"
)

// Twins of test/workflow-tool-description.test.ts. The original derives its expectations from worker-source.ts so that
// changing the runtime without changing the prose fails; here they are derived from workflow_worker.js, its port.

func stringArrayOf(t *testing.T, name string) []string {
	t.Helper()
	m := regexp.MustCompile(`(?s)const ` + name + ` = \[(.*?)\];`).FindStringSubmatch(workflowWorkerSource)
	if m == nil {
		t.Fatalf("could not find %s in workflow_worker.js", name)
	}
	var out []string
	for _, x := range regexp.MustCompile(`"(\w+)"`).FindAllStringSubmatch(m[1], -1) {
		out = append(out, x[1])
	}
	return out
}

type documentedCall struct {
	keys    map[string]bool
	efforts []string
	line    int
}

// documentedAgentOptions is every `{ ... }` options literal written as an agent() second argument.
func documentedAgentOptions(description string) []documentedCall {
	var found []documentedCall
	literal := regexp.MustCompile(`\},\s*\{([^{}]*)\}\)|,\s*\{([^{}]*)\}\)`)
	for _, loc := range regexp.MustCompile(`agent\(`).FindAllStringIndex(description, -1) {
		end := min(loc[0]+400, len(description))
		m := literal.FindStringSubmatch(description[loc[0]:end])
		if m == nil {
			continue
		}
		body := m[1]
		if body == "" {
			body = m[2]
		}
		c := documentedCall{keys: map[string]bool{}, line: strings.Count(description[:loc[0]], "\n") + 1}
		for _, k := range regexp.MustCompile(`(\w+)\s*:`).FindAllStringSubmatch(body, -1) {
			c.keys[k[1]] = true
		}
		for _, e := range regexp.MustCompile(`effort:\s*'(\w+)'`).FindAllStringSubmatch(body, -1) {
			c.efforts = append(c.efforts, e[1])
		}
		found = append(found, c)
	}
	return found
}

func TestWorkflowToolDescription(t *testing.T) {
	const f = "workflow-tool-description"
	description := fullWorkflowToolDescription
	agentOptions := stringArrayOf(t, "AGENT_OPTIONS")
	effortLevels := stringArrayOf(t, "EFFORT_LEVELS")
	// Options the runtime refuses to combine with resume, per its own messages.
	var resumeExclusions []string
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`opts\.resume and opts\.(\w+) are mutually exclusive`).FindAllStringSubmatch(workflowWorkerSource, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			resumeExclusions = append(resumeExclusions, m[1])
		}
	}
	if strings.Contains(workflowWorkerSource, "opts.gate cannot be combined with opts.resume") && !seen["gate"] {
		resumeExclusions = append(resumeExclusions, "gate")
	}
	has := func(list []string, s string) bool {
		for _, e := range list {
			if e == s {
				return true
			}
		}
		return false
	}

	// the agent() contract it documents
	tw(t, f, "names every option the runtime accepts", func(t *testing.T) {
		for _, option := range agentOptions {
			if !strings.Contains(description, option) {
				t.Fatalf("agent() opts.%s is undocumented", option)
			}
		}
	})
	tw(t, f, "documents no option the runtime would reject by name", func(t *testing.T) {
		for _, m := range regexp.MustCompile(`opts\.(\w+)`).FindAllStringSubmatch(description, -1) {
			if !has(agentOptions, m[1]) {
				t.Fatalf("documents opts.%s, which the runtime rejects", m[1])
			}
		}
	})
	tw(t, f, "lists every option resume cannot be combined with", func(t *testing.T) {
		sentence := regexp.MustCompile(`opts\.resume[^\n]*`).FindString(description)
		for _, excluded := range resumeExclusions {
			if !strings.Contains(sentence, excluded) {
				t.Fatalf("resume/%s exclusion missing from the resume line", excluded)
			}
		}
		if len(resumeExclusions) <= 1 {
			t.Fatal("no exclusions found")
		}
	})
	tw(t, f, "offers every reasoning effort the runtime has, and no other", func(t *testing.T) {
		m := regexp.MustCompile(`reasoning effort for this agent call \(([^)]*)\)`).FindStringSubmatch(description)
		var quoted []string
		if m != nil {
			for _, q := range regexp.MustCompile(`'(\w+)'`).FindAllStringSubmatch(m[1], -1) {
				quoted = append(quoted, q[1])
			}
		}
		eq(t, quoted, effortLevels)
	})

	// the examples it ships
	calls := documentedAgentOptions(description)
	tw(t, f, "writes at least one options literal to check", func(t *testing.T) {
		if len(calls) <= 4 {
			t.Fatalf("only %d", len(calls))
		}
	})
	tw(t, f, "uses only options the runtime accepts", func(t *testing.T) {
		for _, c := range calls {
			for k := range c.keys {
				if !has(agentOptions, k) {
					t.Fatalf("unknown agent() option %q at description line %d", k, c.line)
				}
			}
		}
	})
	tw(t, f, "never combines resume with an option that throws", func(t *testing.T) {
		for _, c := range calls {
			if !c.keys["resume"] {
				continue
			}
			for k := range c.keys {
				if has(resumeExclusions, k) {
					t.Fatalf("illegal resume combination %q at description line %d", k, c.line)
				}
			}
		}
	})
	tw(t, f, "never names an effort level the runtime would reject", func(t *testing.T) {
		for _, c := range calls {
			for _, e := range c.efforts {
				if !has(effortLevels, e) {
					t.Fatalf("bad effort %q at description line %d", e, c.line)
				}
			}
		}
	})

	// the limits it quotes
	tw(t, f, "quotes the real agent and item caps", func(t *testing.T) {
		if !strings.Contains(description, "capped at 1000") || !strings.Contains(description, "at most 4096 items") {
			t.Fatal("caps")
		}
		eq(t, workflowAgentCap, 1000)
		eq(t, workflowItemCap, 4096)
	})
	tw(t, f, "describes the concurrency formula the runtime actually applies", func(t *testing.T) {
		if !strings.Contains(description, "min(16, available CPUs - 2)") {
			t.Fatal("formula")
		}
		eq(t, workflowConcurrency(8), 6)
		eq(t, workflowConcurrency(64), 16)
	})

	// rendering
	tw(t, f, "keeps the placeholder the live agent roster is substituted into", func(t *testing.T) {
		if !strings.Contains(description, "{{typeList}}") {
			t.Fatal("placeholder")
		}
		rendered := renderWorkflowToolDescription(newRegistry())
		if strings.Contains(rendered, "{{typeList}}") {
			t.Fatal("placeholder left in the rendered text")
		}
	})
	tw(t, f, "leaves no unescaped template interpolation from the source literal", func(t *testing.T) {
		// A bare `${...}` in the .ts literal would interpolate at module load and reach the model as a value.
		if strings.Contains(description, "[object Object]") || !strings.Contains(description, "${f.title}") {
			t.Fatal("interpolation")
		}
	})
}
