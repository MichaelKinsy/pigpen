package tintinweb_subagents

import (
	"path/filepath"
	"strings"
	"testing"
)

func (r *caRig) loadWarn() (*agentRegistry, string) {
	reg, w := loadCustomAgents(r.tmp)
	return reg, strings.Join(w, "\n")
}

func TestLoadCustomAgentsNames(t *testing.T) {
	const f = "custom-agents"
	tw(t, f, "handles enabled: false frontmatter", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("disabled", "---\nenabled: false\n---\n")
		eq(t, r.agent("disabled").Enabled, false)
	})
	tw(t, f, "takes the agent type from frontmatter name, not the filename", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("blubb", "---\nname: code-review\ndescription: Reviews code\n---\nAgent prompt.")
		reg := r.load()
		eq(t, reg.m["code-review"].Name, "code-review")
		eq(t, reg.m["blubb"] == nil, true)
	})
	tw(t, f, "records the file it was read from, not the one its type would name", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("blubb", "---\nname: code-review\ndescription: Reviews code\n---\nAgent prompt.")
		eq(t, r.agent("code-review").SourcePath, filepath.Join(r.tmp, ".pi", "agents", "blubb.md"))
	})
	tw(t, f, "falls back to the filename for an empty or blank declared name", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("myagent", "---\nname: \"\"\ndescription: My Agent\n---\n\nPrompt.")
		r.writeAgent("other", "---\nname: \"   \"\ndescription: Other\n---\n\nPrompt.")
		reg := r.load()
		eq(t, reg.m["myagent"].Name, "myagent")
		eq(t, reg.m["other"].Name, "other")
		eq(t, reg.m[""] == nil, true)
	})
	tw(t, f, "trims a declared name so it matches what the user meant to type", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("blubb", "---\nname: \" code-review \"\ndescription: Reviews code\n---\n\nPrompt.")
		eq(t, r.agent("code-review").Name, "code-review")
	})
	tw(t, f, "falls back to the filename when no name is declared", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("myagent", "---\ndescription: My Agent\n---\nAgent prompt.")
		eq(t, r.agent("myagent").Name, "myagent")
	})
	tw(t, f, "keeps display_name as a label only, independent of the type", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("blubb", "---\nname: code-review\ndescription: My Agent\ndisplay_name: MyAgent\n---\nAgent prompt.")
		a := r.agent("code-review")
		eq(t, a.Name, "code-review")
		eq(t, a.DisplayName, "MyAgent")
	})
	tw(t, f, "leaves displayName unset so the badge falls back to the type", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("whatever", "---\nname: code-reviewer\ndescription: Reviews code\ncolor: \"#8B5CF6\"\n---\nAgent prompt.")
		a := r.agent("code-reviewer")
		eq(t, a.Name, "code-reviewer")
		eq(t, a.DisplayName, "")
		eq(t, a.Color, "#8B5CF6")
	})
	tw(t, f, "accepts a name Claude Code accepts, however unlike a type it looks", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("reviewer", "---\nname: Code Reviewer\ndescription: Reviews code\n---\nAgent prompt.")
		eq(t, r.agent("Code Reviewer").Name, "Code Reviewer")
	})
	tw(t, f, "refuses a name containing the plugin-scope separator", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("scoped", "---\nname: my-plugin:reviewer\ndescription: Reviews code\n---\nAgent prompt.")
		reg := r.load()
		eq(t, reg.m["my-plugin:reviewer"] == nil, true)
		eq(t, reg.m["scoped"] == nil, true)
	})
	tw(t, f, "does not claim the rejected file was overriding its filename's agent", func(t *testing.T) {
		r := newCARig(t)
		r.writeWorkspace("scoped", "---\ndescription: An unrelated agent\n---\n\nBody.")
		r.writeAgent("scoped", "---\nname: my-plugin:reviewer\ndescription: Reviews code\n---\n\nBody.")
		reg, message := r.loadWarn()
		eq(t, reg.m["scoped"].Description, "An unrelated agent")
		eq(t, strings.Contains(message, "reserved for plugin-scoped identifiers"), true)
		eq(t, strings.Contains(message, "now loads from"), false)
	})
	tw(t, f, "lets a later file win a declared-name clash, as a filename clash always did", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("a-first", "---\nname: shared\ndescription: first\n---\nFirst.")
		r.writeAgent("b-second", "---\nname: shared\ndescription: second\n---\nSecond.")
		reg := r.load()
		eq(t, reg.m["shared"].Description, "second")
		n := 0
		for _, k := range reg.keys {
			if k == "shared" {
				n++
			}
		}
		eq(t, n, 1)
	})
}
