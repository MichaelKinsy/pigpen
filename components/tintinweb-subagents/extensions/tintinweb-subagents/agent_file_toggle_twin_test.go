package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of the original's test/agent-file-toggle.test.ts.
const fToggle = "agent-file-toggle"

const (
	handAuthoredDisabled     = "---\ndescription: Scout the repo\nenabled: false\n---\n\nYou are a scout.\n"
	extensionWrittenDisabled = "---\nenabled: false\ndescription: Scout the repo\n---\n\nYou are a scout.\n"
	enabledAgentFile         = "---\ndescription: Scout the repo\n---\n\nYou are a scout.\n"
)

// loaderSeesDisabled is what the loader concludes about a file, via the same parser it really uses.
func loaderSeesDisabled(t *testing.T, content string) bool {
	t.Helper()
	fm, _, err := parseFrontmatter(content)
	if err != nil {
		t.Fatalf("parse %q: %v", content, err)
	}
	b, ok := fm["enabled"].(bool)
	return ok && !b
}

func TestEnableInContent(t *testing.T) {
	tw(t, fToggle, "strips enabled: false when it is the first frontmatter line", func(t *testing.T) {
		content, changed := enableInContent(extensionWrittenDisabled)
		eq(t, changed, true)
		eq(t, loaderSeesDisabled(t, content), false)
	})
	tw(t, fToggle, "strips enabled: false when another key precedes it", func(t *testing.T) {
		// The shape a user gets by following README.md: description first.
		content, changed := enableInContent(handAuthoredDisabled)
		eq(t, changed, true)
		eq(t, loaderSeesDisabled(t, content), false)
	})
	tw(t, fToggle, "strips enabled: false when it is the last frontmatter line", func(t *testing.T) {
		content, changed := enableInContent("---\ndescription: Scout\ndisplay_name: Scout\nenabled: false\n---\n\nBody.\n")
		eq(t, changed, true)
		eq(t, loaderSeesDisabled(t, content), false)
	})
	tw(t, fToggle, "reports changed: false when there is nothing to strip", func(t *testing.T) {
		content, changed := enableInContent(enabledAgentFile)
		eq(t, changed, false)
		eq(t, content, enabledAgentFile)
	})
	tw(t, fToggle, "leaves the body and other frontmatter keys untouched", func(t *testing.T) {
		content, _ := enableInContent("---\ndescription: Scout\nenabled: false\n# a comment\nmodel: haiku\n---\n\nLine 1.\n\nLine 2.\n")
		hasStr(t, content, "# a comment")
		hasStr(t, content, "model: haiku")
		hasStr(t, content, "Line 1.\n\nLine 2.\n")
		eq(t, loaderSeesDisabled(t, content), false)
	})
	tw(t, fToggle, "handles CRLF line endings", func(t *testing.T) {
		content, changed := enableInContent("---\r\ndescription: Scout\r\nenabled: false\r\n---\r\n\r\nBody.\r\n")
		eq(t, changed, true)
		eq(t, loaderSeesDisabled(t, content), false)
	})
}

func TestDisableInContent(t *testing.T) {
	tw(t, fToggle, "inserts enabled: false into a normal frontmatter block", func(t *testing.T) {
		content, outcome := disableInContent(enabledAgentFile)
		eq(t, outcome, outcomeDisabled)
		eq(t, loaderSeesDisabled(t, content), true)
	})
	tw(t, fToggle, "is idempotent when the key is already first", func(t *testing.T) {
		_, outcome := disableInContent(extensionWrittenDisabled)
		eq(t, outcome, outcomeAlreadyDisabled)
	})
	tw(t, fToggle, "is idempotent when the key is already present mid-block", func(t *testing.T) {
		_, outcome := disableInContent(handAuthoredDisabled)
		eq(t, outcome, outcomeAlreadyDisabled)
	})
	// The previous guard was `content.includes("\nenabled: false\n")`, which a trailing space defeats: it then inserted
	// a SECOND `enabled: false`, and duplicate map keys make the whole file unparseable.
	tw(t, fToggle, "never writes a file the loader cannot parse", func(t *testing.T) {
		for _, src := range []string{
			"---\ndescription: x\nenabled: false  \n---\nbody\n",
			"---\ndescription: x\nenabled: false\t\n---\nbody\n",
			handAuthoredDisabled,
			extensionWrittenDisabled,
			enabledAgentFile,
		} {
			content, _ := disableInContent(src)
			if _, _, err := parseFrontmatter(content); err != nil {
				t.Errorf("%q: %v", src, err)
			}
			eq(t, loaderSeesDisabled(t, content), true)
		}
	})
	tw(t, fToggle, "reports no-frontmatter rather than claiming success on a fence-less file", func(t *testing.T) {
		src := "Just a body, no frontmatter at all.\n"
		content, outcome := disableInContent(src)
		eq(t, outcome, outcomeNoFrontmatter)
		eq(t, content, src)
	})
	tw(t, fToggle, "disables a CRLF file instead of misreporting it as frontmatter-less", func(t *testing.T) {
		content, outcome := disableInContent("---\r\ndescription: Scout\r\n---\r\n\r\nBody.\r\n")
		eq(t, outcome, outcomeDisabled)
		eq(t, loaderSeesDisabled(t, content), true)
	})
	tw(t, fToggle, "toggles a BOM-prefixed file, and leaves the BOM where it found it", func(t *testing.T) {
		src := "\ufeff---\ndescription: 侦察\n---\n\n本文。\n"
		content, outcome := disableInContent(src)
		eq(t, outcome, outcomeDisabled)
		eq(t, isDisabledContent(content), true)
		eq(t, strings.HasPrefix(content, "\ufeff"), true)
		back, _ := enableInContent(content)
		eq(t, back, src)
	})
}

func TestParseAgentFrontmatter(t *testing.T) {
	tw(t, fToggle, "reads a BOM-prefixed file's fields instead of dropping them", func(t *testing.T) {
		// An unnormalised BOM made the fence miss, so the frontmatter came back empty and the whole file became the
		// body; `tools` going missing is the sharp edge (the agent registers with the default toolset).
		fm, body, err := parseFrontmatter("\ufeff---\ndescription: 侦察\ntools: none\n---\n\n本文。\n")
		eq(t, err, nil)
		eq(t, fm, map[string]any{"description": "侦察", "tools": "none"})
		eq(t, body, "本文。")
	})
	tskip(t, fToggle, "leaves a file without a BOM exactly as the parser reads it", "the original compares its BOM-normalising wrapper with Pi's parser; Go has the one parser, which strips the BOM itself, so there is no second reading to compare")
}

func TestIsDisabledContent(t *testing.T) {
	tw(t, fToggle, "sees the key at the first frontmatter line", func(t *testing.T) { eq(t, isDisabledContent(extensionWrittenDisabled), true) })
	tw(t, fToggle, "sees the key mid-block", func(t *testing.T) { eq(t, isDisabledContent(handAuthoredDisabled), true) })
	tw(t, fToggle, "sees the key in a CRLF file", func(t *testing.T) {
		eq(t, isDisabledContent("---\r\ndescription: Scout\r\nenabled: false\r\n---\r\n\r\nBody.\r\n"), true)
	})
	tw(t, fToggle, "is false for an enabled file", func(t *testing.T) { eq(t, isDisabledContent(enabledAgentFile), false) })
}

// Detection is a read, so it asks the loader's parser instead of mirroring it: a mirror has to be right about YAML's
// other spellings of `false` and about pi's fence scan, which closes the block at any line starting `---`.
func TestAgreesWithTheLoader(t *testing.T) {
	for _, c := range []struct {
		label, content string
		want           bool
	}{
		{"lowercase bare false", "---\ndescription: x\nenabled: false\n---\nbody\n", true},
		{"False", "---\ndescription: x\nenabled: False\n---\nbody\n", true},
		{"FALSE", "---\ndescription: x\nenabled: FALSE\n---\nbody\n", true},
		{"trailing comment", "---\ndescription: x\nenabled: false # off for now\n---\nbody\n", true},
		{"quoted key", "---\ndescription: x\n\"enabled\": false\n---\nbody\n", true},
		{"trailing whitespace", "---\ndescription: x\nenabled: false  \n---\nbody\n", true},
		{"'----' closes the block early", "---\ndescription: x\n----\nenabled: false\n---\nbody\n", false},
		{"'--- x' closes the block early", "---\ndescription: x\n--- x\nenabled: false\n---\nbody\n", false},
		{"quoted string, not a boolean", "---\ndescription: x\nenabled: \"false\"\n---\nbody\n", false},
		{"YAML 1.1 'no' is a string here", "---\ndescription: x\nenabled: no\n---\nbody\n", false},
		{"key only in the body", "---\ndescription: x\n---\nenabled: false\n", false},
	} {
		t.Run("agrees with the loader: "+c.label, func(t *testing.T) {
			eq(t, loaderSeesDisabled(t, c.content), c.want) // premise: what the loader concludes
			eq(t, isDisabledContent(c.content), c.want)     // we must conclude the same
		})
	}
}

// The defect, stated as an invariant: the two sides must never disagree about whether a file is disabled. Whatever the
// loader reads as disabled, `/agents → Enable` has to be able to re-enable.
func TestReadAndWritePathsAgree(t *testing.T) {
	shapes := []struct{ label, content string }{
		{"key first", extensionWrittenDisabled},
		{"key after description", handAuthoredDisabled},
		{"key last", "---\ndescription: Scout\ndisplay_name: S\nenabled: false\n---\n\nBody.\n"},
		{"CRLF", "---\r\ndescription: Scout\r\nenabled: false\r\n---\r\n\r\nBody.\r\n"},
	}
	tw(t, fToggle, "a file the loader reads as disabled can be enabled — ${label}", func(t *testing.T) {
		for _, s := range shapes {
			eq(t, loaderSeesDisabled(t, s.content), true) // premise: the loader agrees it's disabled
			_, changed := enableInContent(s.content)
			if !changed {
				t.Errorf("%s: not enabled", s.label)
			}
		}
	})
	tw(t, fToggle, "disable → enable round-trips to the original hand-authored file", func(t *testing.T) {
		disabled, outcome := disableInContent(enabledAgentFile)
		eq(t, outcome, outcomeDisabled)
		back, _ := enableInContent(disabled)
		eq(t, back, enabledAgentFile)
	})
}

func TestIsEmptyStub(t *testing.T) {
	tw(t, fToggle, "recognises the stub behind a BOM", func(t *testing.T) { eq(t, isEmptyStub("\ufeff---\n---"), true) })
	tw(t, fToggle, "recognizes the stub /agents writes to disable a built-in default", func(t *testing.T) {
		eq(t, isEmptyStub("---\n---\n"), true)
		stripped, _ := enableInContent("---\nenabled: false\n---\n")
		eq(t, isEmptyStub(stripped), true)
	})
	tw(t, fToggle, "is false for a file with real frontmatter", func(t *testing.T) { eq(t, isEmptyStub(enabledAgentFile), false) })
}

func TestFindAgentFile(t *testing.T) {
	setup := func(t *testing.T) (tmp, agents string) {
		tmp, agents = t.TempDir(), t.TempDir()
		t.Setenv("PIG_CODING_AGENT_DIR", agents)
		t.Setenv("PIG_USE_PI_DIRS", "")
		return tmp, agents
	}
	write := func(t *testing.T, dir, name string) {
		t.Helper()
		eq(t, os.MkdirAll(dir, 0o755), nil)
		eq(t, os.WriteFile(filepath.Join(dir, name+".md"), []byte(enabledAgentFile), 0o644), nil)
	}
	tw(t, fToggle, "prefers .pi/agents over .agents/agents and the personal dir", func(t *testing.T) {
		tmp, agents := setup(t)
		write(t, filepath.Join(tmp, ".pi", "agents"), "scout")
		write(t, filepath.Join(tmp, ".agents", "agents"), "scout")
		write(t, filepath.Join(agents, "agents"), "scout")
		eq(t, findAgentFile("scout", tmp), &agentFile{filepath.Join(tmp, ".pi", "agents", "scout.md"), locationProject})
	})
	tw(t, fToggle, "falls back to the workspace .agents/agents dir", func(t *testing.T) {
		tmp, agents := setup(t)
		write(t, filepath.Join(tmp, ".agents", "agents"), "scout")
		write(t, filepath.Join(agents, "agents"), "scout")
		eq(t, findAgentFile("scout", tmp), &agentFile{filepath.Join(tmp, ".agents", "agents", "scout.md"), locationWorkspace})
	})
	tw(t, fToggle, "falls back to the personal agent dir", func(t *testing.T) {
		tmp, agents := setup(t)
		write(t, filepath.Join(agents, "agents"), "scout")
		eq(t, findAgentFile("scout", tmp), &agentFile{filepath.Join(agents, "agents", "scout.md"), locationPersonal})
	})
	tw(t, fToggle, "returns undefined when the agent has no file anywhere", func(t *testing.T) {
		tmp, _ := setup(t)
		eq(t, findAgentFile("nope", tmp), (*agentFile)(nil))
	})
	// An agent's type comes from its frontmatter `name:`, so `<type>.md` is a guess; a wrong guess is not a harmless
	// miss: Disable would write a NEW stub that loses to the real file on load.
	tw(t, fToggle, "uses the file the loader read, whatever it is called", func(t *testing.T) {
		tmp, _ := setup(t)
		write(t, filepath.Join(tmp, ".pi", "agents"), "reviewer")
		source := filepath.Join(tmp, ".pi", "agents", "reviewer.md")
		eq(t, locateAgentFile("code-reviewer", source, tmp), &agentFile{source, locationProject})
	})
	tw(t, fToggle, "classifies a workspace and a personal source path", func(t *testing.T) {
		tmp, agents := setup(t)
		write(t, filepath.Join(tmp, ".agents", "agents"), "reviewer")
		write(t, filepath.Join(agents, "agents"), "auditor")
		eq(t, locateAgentFile("code-reviewer", filepath.Join(tmp, ".agents", "agents", "reviewer.md"), tmp).Location, locationWorkspace)
		eq(t, locateAgentFile("code-auditor", filepath.Join(agents, "agents", "auditor.md"), tmp).Location, locationPersonal)
	})
	tw(t, fToggle, "falls back to the <type>.md probe for a built-in with no source file", func(t *testing.T) {
		tmp, _ := setup(t)
		write(t, filepath.Join(tmp, ".pi", "agents"), "scout")
		eq(t, locateAgentFile("scout", "", tmp), &agentFile{filepath.Join(tmp, ".pi", "agents", "scout.md"), locationProject})
	})
	tw(t, fToggle, "falls back when the recorded path has since been deleted", func(t *testing.T) {
		tmp, _ := setup(t)
		write(t, filepath.Join(tmp, ".pi", "agents"), "scout")
		eq(t, locateAgentFile("scout", filepath.Join(tmp, ".pi", "agents", "gone.md"), tmp), &agentFile{filepath.Join(tmp, ".pi", "agents", "scout.md"), locationProject})
	})
	tw(t, fToggle, "finds nothing when neither the source path nor the probe resolves", func(t *testing.T) {
		tmp, _ := setup(t)
		eq(t, locateAgentFile("nope", filepath.Join(tmp, ".pi", "agents", "gone.md"), tmp), (*agentFile)(nil))
	})
}

// `/agents → Create agent → Manual` writes an agent file from free-text prompts: no validation, no escaping, and an
// unparseable agent file is skipped by the loader, so a bad scalar made the wizard report success for an agent that
// does not exist.
func TestBuildNewAgentFile(t *testing.T) {
	base := newAgentInput{Tools: "read, grep", SystemPrompt: "Do the thing.", Description: "Scout"}
	parse := func(t *testing.T, content string) map[string]any {
		t.Helper()
		fm, _, err := parseFrontmatter(content)
		if err != nil {
			t.Fatalf("the loader cannot parse %q: %v", content, err)
		}
		return fm
	}
	with := func(mut func(*newAgentInput)) newAgentInput { in := base; mut(&in); return in }
	tw(t, fToggle, "round-trips an ordinary description", func(t *testing.T) {
		eq(t, parse(t, buildNewAgentFile(base))["description"], "Scout")
	})
	tw(t, fToggle, "survives a description containing a colon", func(t *testing.T) {
		// "Scout: find things" is a natural thing to type, and an unquoted YAML scalar treats the colon as a nested mapping.
		content := buildNewAgentFile(with(func(i *newAgentInput) { i.Description = "Scout: find things" }))
		eq(t, parse(t, content)["description"], "Scout: find things")
	})
	tw(t, fToggle, "keeps a description containing a # instead of truncating it", func(t *testing.T) {
		content := buildNewAgentFile(with(func(i *newAgentInput) { i.Description = "audit #security" }))
		eq(t, parse(t, content)["description"], "audit #security")
	})
	tw(t, fToggle, "leaves a `provider/model:thinking` suffix intact", func(t *testing.T) {
		content := buildNewAgentFile(with(func(i *newAgentInput) { i.Model = "anthropic/claude-sonnet-4-6:high" }))
		eq(t, parse(t, content)["model"], "anthropic/claude-sonnet-4-6:high")
	})
	tw(t, fToggle, "survives a custom model containing a colon-space or a #", func(t *testing.T) {
		for _, model := range []string{"anthropic/foo: bar", "anthropic/x #c"} {
			content := buildNewAgentFile(with(func(i *newAgentInput) { i.Model = model }))
			eq(t, parse(t, content)["model"], model)
		}
	})
	tw(t, fToggle, "emits the fields the wizard collects, and omits the ones left on inherit", func(t *testing.T) {
		full := parse(t, buildNewAgentFile(with(func(i *newAgentInput) { i.Model, i.Thinking = "anthropic/x", "high" })))
		eq(t, full["tools"], "read, grep")
		eq(t, full["model"], "anthropic/x")
		eq(t, full["thinking"], "high")
		eq(t, full["prompt_mode"], "replace")
		minimal := parse(t, buildNewAgentFile(base))
		_, hasModel := minimal["model"]
		_, hasThinking := minimal["thinking"]
		eq(t, hasModel || hasThinking, false)
	})
	tw(t, fToggle, "keeps the system prompt as the body", func(t *testing.T) {
		content := buildNewAgentFile(with(func(i *newAgentInput) { i.SystemPrompt = "Line 1.\n\nLine 2." }))
		_, body, err := parseFrontmatter(content)
		eq(t, err, nil)
		eq(t, body, "Line 1.\n\nLine 2.")
	})
}
