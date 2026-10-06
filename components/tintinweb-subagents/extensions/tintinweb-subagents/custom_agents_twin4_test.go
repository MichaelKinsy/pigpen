package tintinweb_subagents

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCustomAgentsPolicy(t *testing.T) {
	const f = "custom-agents"
	tw(t, f, "parses disallowed_tools as csv list", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("restricted", "---\ndescription: Restricted Agent\ndisallowed_tools: bash, write\n---\nNo bash or write.")
		eq(t, r.agent("restricted").DisallowedTools, []string{"bash", "write"})
	})
	tw(t, f, "disallowed_tools defaults to undefined when omitted", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("unrestricted", "---\ndescription: Unrestricted\n---\nAll tools.")
		eq(t, r.agent("unrestricted").DisallowedTools, []string(nil))
	})
	tw(t, f, "parses memory scope", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("rememberer", "---\ndescription: Agent with memory\nmemory: project\n---\nRemember things.")
		eq(t, r.agent("rememberer").Memory, "project")
	})
	tw(t, f, "parses memory: user scope", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("global-mem", "---\nmemory: user\n---\nUser memory.")
		eq(t, r.agent("global-mem").Memory, "user")
	})
	tw(t, f, "memory defaults to undefined when omitted", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("no-mem", "---\ndescription: No memory\n---\nStateless.")
		eq(t, r.agent("no-mem").Memory, "")
	})
	tw(t, f, "rejects invalid memory scope", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("bad-mem", "---\nmemory: invalid\n---\nBad memory.")
		eq(t, r.agent("bad-mem").Memory, "")
	})
	tw(t, f, "parses isolation: worktree", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("isolated-wt", "---\ndescription: Worktree agent\nisolation: worktree\n---\nIsolated.")
		eq(t, r.agent("isolated-wt").Isolation, "worktree")
	})
	tw(t, f, "isolation defaults to undefined when omitted", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("no-isolation", "---\ndescription: Normal\n---\nNormal.")
		eq(t, r.agent("no-isolation").Isolation, "")
	})
	tw(t, f, "rejects invalid isolation mode", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("bad-isolation", "---\nisolation: docker\n---\nBad isolation.")
		eq(t, r.agent("bad-isolation").Isolation, "")
	})
	tw(t, f, "parses isolation: off", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("no-wt", "---\ndescription: Never worktree\nisolation: off\n---\nNo worktree.")
		eq(t, r.agent("no-wt").Isolation, "off")
	})
	tw(t, f, "accepts %s as a spelling of off", func(t *testing.T) {
		r := newCARig(t)
		for _, p := range [][2]string{{"false", "isolation: false"}, {"none", "isolation: none"}, {"no", "isolation: no"}} {
			r.writeAgent("off-"+p[0], "---\n"+p[1]+"\n---\nOff.")
			eq(t, r.agent("off-"+p[0]).Isolation, "off")
		}
	})
	tw(t, f, "skips a file with malformed frontmatter and still loads the others", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("broken", "---\nname: broken\ndescription: Use this: that\n---\nBroken body.")
		r.writeAgent("good", "---\ndescription: Still loads\n---\nGood body.")
		reg := r.load()
		eq(t, reg.m["broken"] == nil, true)
		eq(t, reg.m["good"].Description, "Still loads")
	})
	tw(t, f, "names the offending file and the reason when skipping it", func(t *testing.T) {
		r := newCARig(t)
		r.writeAgent("broken", "---\nname: broken\ndescription: Use this: that\n---\n\nBroken body.")
		_, message := r.loadWarn()
		eq(t, strings.Contains(message, filepath.Join(r.tmp, ".pi", "agents", "broken.md")), true)
		eq(t, strings.Contains(message, "Nested mappings are not allowed"), true)
	})
}
