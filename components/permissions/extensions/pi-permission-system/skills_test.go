package pi_permission_system

import (
	"strings"
	"testing"
)

func TestSkillPromptSanitizer(t *testing.T) {
	const f = "exposure/skill-prompt-sanitizer"
	entries := []skillEntry{
		{name: "librarian", state: "allow", location: "/skills/librarian/SKILL.md", baseDir: "/skills/librarian"},
		{name: "ask-user", state: "allow", location: "/skills/ask-user/SKILL.md", baseDir: "/skills/ask-user"},
	}
	name := func(e *skillEntry) string {
		if e == nil {
			return ""
		}
		return e.name
	}
	tw(t, f, "returns null for empty normalized path", func(t *testing.T) {
		eq(t, findSkillPathMatch("", entries) == nil, true, "nil")
	})
	tw(t, f, "returns null for empty entries array", func(t *testing.T) {
		eq(t, findSkillPathMatch("/skills/librarian/SKILL.md", nil) == nil, true, "nil")
	})
	tw(t, f, "matches exact location path", func(t *testing.T) {
		eq(t, name(findSkillPathMatch("/skills/librarian/SKILL.md", entries)), "librarian", "name")
	})
	tw(t, f, "matches path within skill base directory", func(t *testing.T) {
		eq(t, name(findSkillPathMatch("/skills/librarian/extra/helper.md", entries)), "librarian", "name")
	})
	tw(t, f, "returns null for path not within any skill directory", func(t *testing.T) {
		eq(t, findSkillPathMatch("/other/path/file.md", entries) == nil, true, "nil")
	})
	tw(t, f, "returns null for sibling path that shares a prefix", func(t *testing.T) {
		eq(t, findSkillPathMatch("/skills/librarian-extra/SKILL.md", entries) == nil, true, "nil")
	})
	tw(t, f, "prefers longer matching base directory (most specific skill wins)", func(t *testing.T) {
		nested := []skillEntry{
			{name: "parent", state: "allow", location: "/skills/parent/SKILL.md", baseDir: "/skills/parent"},
			{name: "child", state: "allow", location: "/skills/parent/child/SKILL.md", baseDir: "/skills/parent/child"},
		}
		eq(t, name(findSkillPathMatch("/skills/parent/child/helper.md", nested)), "child", "name")
	})
	tw(t, f, "parseAllSkillPromptSections finds every available_skills block", func(t *testing.T) {
		prompt := strings.Join([]string{"Some preamble", "<available_skills>", "  <skill>", "    <name>skill-one</name>",
			"    <description>First skill</description>", "    <location>/path/to/one</location>", "  </skill>", "</available_skills>",
			"Some content between", "<available_skills>", "  <skill>", "    <name>skill-two</name>", "    <description>Second skill</description>",
			"    <location>/path/to/two</location>", "  </skill>", "</available_skills>", "Footer"}, "\n")
		sections := parseAllSkillPromptSections(prompt)
		if len(sections) != 2 || len(sections[0]) == 0 || len(sections[1]) == 0 {
			t.Fatalf("sections: %#v", sections)
		}
		eq(t, sections[0][0].name, "skill-one", "first")
		eq(t, sections[1][0].name, "skill-two", "second")
	})
}

func skillCatalogue(skills ...[2]string) string {
	var b strings.Builder
	b.WriteString("intro\n<available_skills>\n")
	for _, s := range skills {
		b.WriteString("  <skill>\n    <name>" + s[0] + "</name>\n    <description>d</description>\n    <location>" + s[1] + "</location>\n  </skill>\n")
	}
	b.WriteString("</available_skills>\n")
	return b.String()
}

func TestSkillReadGate(t *testing.T) {
	useFakeHome(t)
	prompt := skillCatalogue([2]string{"demo", "/agent/skills/demo/SKILL.md"}, [2]string{"open", "~/skills/open/SKILL.md"},
		[2]string{"gone", "/agent/skills/gone/SKILL.md"}, [2]string{"a&amp;b", "/agent/skills/a&amp;b/SKILL.md"},
		[2]string{"", "/agent/skills/noname/SKILL.md"}) +
		"<available_skills><skill><name>nodesc</name><location>/agent/skills/nodesc/SKILL.md</location></skill></available_skills>"
	p := policyOf(t, `{"permission":{"*":"ask",`+allowPaths+`,"read":"allow","skill":{"*":"ask","open":"allow","gone":"deny"}}}`)
	entries := p.visibleSkillEntries(prompt, "/work")
	var names []string
	for _, e := range entries {
		names = append(names, e.name+"="+e.state)
	}
	eq(t, names, []string{"demo=ask", "open=allow", "a&b=ask"}, "a denied skill, a nameless one and one without a description are not listed")
	if len(entries) != 3 {
		t.Fatalf("entries: %#v", entries)
	}
	eq(t, entries[1].location, fakeHome+"/skills/open/SKILL.md", "home expanded")
	blocked := func(path string) bool {
		return skillRead("read", map[string]any{"path": path}, entries, "/work").Block
	}
	eq(t, blocked("/agent/skills/demo/SKILL.md"), true, "the skill file of an ask skill")
	eq(t, blocked("../agent/skills/demo/ref/x.md"), true, "a file inside it, relative to cwd")
	eq(t, blocked(` "@/agent/skills/demo/SKILL.md" `), true, "trimmed, unquoted, @ dropped")
	eq(t, blocked("/agent/skills/demo-x/SKILL.md"), false, "a sibling")
	eq(t, blocked("~/skills/open/SKILL.md"), false, "an allowed skill")
	eq(t, blocked("/agent/skills/gone/SKILL.md"), false, "a denied skill is not listed, so its files are not gated here")
	eq(t, blocked("/agent/skills/a&b/x"), true, "XML entities decoded")
	eq(t, blocked("a.txt"), false, "another file")
	eq(t, blocked(""), false, "no path")
	eq(t, skillRead("bash", map[string]any{"path": "/agent/skills/demo/SKILL.md"}, entries, "/work").Block, false, "only read")
	eq(t, strings.Contains(skillRead("read", map[string]any{"path": "/agent/skills/demo/SKILL.md"}, entries, "/work").Reason, "skill 'demo'"), true, "names the skill")
	// yoloMode grants the ask
	y := policyOf(t, `{"yoloMode":true,"permission":{"*":"ask",`+allowPaths+`,"read":"allow"}}`)
	eq(t, skillRead("read", map[string]any{"path": "/agent/skills/demo/SKILL.md"}, y.visibleSkillEntries(prompt, "/work"), "/work").Block, false, "yolo")
	// isWithinDir edge cases
	eq(t, isWithinDir("/a/b", "/a/b"), true, "itself")
	eq(t, isWithinDir("/a", "/a/b"), false, "parent")
	eq(t, isWithinDir("/a/b/c", "/"), true, "root")
	eq(t, isWithinDir("/a/..b", "/a"), true, "a name starting with two dots is inside")
	eq(t, comparablePath(`'x'`, "/w"), "/w/x", "one quote each side")
	eq(t, comparablePath(`""`, "/w"), "", "only quotes")
	eq(t, promptText([]any{"a", 1, "b"}), "a\nb", "a prompt list")
}

// upstreamSkillsSection is the upstream test's availableSkillsSection: each skill at /skills/<name>/SKILL.md.
func upstreamSkillsSection(names ...string) string {
	lines := []string{"<available_skills>"}
	for _, n := range names {
		lines = append(lines, "  <skill>", "    <name>"+n+"</name>", "    <description>Description of "+n+"</description>",
			"    <location>/skills/"+n+"/SKILL.md</location>", "  </skill>")
	}
	return strings.Join(append(lines, "</available_skills>"), "\n")
}

// The upstream tests answer the skill surface with a mock (a default state and overrides by name); here a policy gives the
// same states through the skill surface's rules.
func TestVisibleSkillPromptEntries(t *testing.T) {
	const f = "exposure/skill-prompt-sanitizer"
	const cwd = "/projects/my-app"
	visible := func(p *Policy, prompt string) (names, states []string) {
		for _, e := range p.visibleSkillEntries(prompt, cwd) {
			names, states = append(names, e.name), append(states, e.name+"="+e.state)
		}
		return
	}
	tw(t, f, "keeps all skills visible when all are allowed", func(t *testing.T) {
		names, _ := visible(policyOf(t, `{"permission":{"skill":"allow"}}`), upstreamSkillsSection("librarian", "ask-user"))
		eq(t, names, []string{"librarian", "ask-user"}, "names")
	})
	tw(t, f, "keeps a denied skill out of the visible entries", func(t *testing.T) {
		names, _ := visible(policyOf(t, `{"permission":{"skill":{"*":"allow","beta":"deny"}}}`), upstreamSkillsSection("alpha", "beta"))
		eq(t, names, []string{"alpha"}, "names")
	})
	tw(t, f, "keeps an ask-state skill visible", func(t *testing.T) {
		_, states := visible(policyOf(t, `{"permission":{"skill":{"*":"allow","beta":"ask"}}}`), upstreamSkillsSection("alpha", "beta"))
		eq(t, states, []string{"alpha=allow", "beta=ask"}, "states")
	})
	tw(t, f, "classifies every catalogue in the prompt", func(t *testing.T) {
		names, _ := visible(policyOf(t, `{"permission":{"skill":{"*":"allow","beta":"deny"}}}`),
			upstreamSkillsSection("alpha")+"\n"+upstreamSkillsSection("beta"))
		eq(t, names, []string{"alpha"}, "names")
	})
	tw(t, f, "resolves entry normalizedLocation relative to cwd", func(t *testing.T) {
		e := policyOf(t, `{"permission":{"skill":"allow"}}`).visibleSkillEntries(upstreamSkillsSection("librarian"), cwd)
		if len(e) != 1 {
			t.Fatalf("entries: %#v", e)
		}
		eq(t, e[0].location, "/skills/librarian/SKILL.md", "location")
		eq(t, e[0].baseDir, "/skills/librarian", "base dir")
	})
	tw(t, f, "REGRESSION: visibleSkillPromptEntries excludes a denied skill from every available_skills block", func(t *testing.T) {
		prompt := strings.Join([]string{"System prompt start", "<available_skills>", "  <skill>", "    <name>visible-skill</name>",
			"    <description>Allowed skill</description>", "    <location>/skills/visible/index.ts</location>", "  </skill>", "  <skill>",
			"    <name>denied-skill</name>", "    <description>Denied in first block</description>", "    <location>/skills/blocked/one.ts</location>",
			"  </skill>", "</available_skills>", "Agent identity section", "<available_skills>", "  <skill>", "    <name>denied-skill</name>",
			"    <description>Denied in second block</description>", "    <location>/skills/blocked/two.ts</location>", "  </skill>",
			"</available_skills>", "System prompt end"}, "\n")
		e := policyOf(t, `{"permission":{"*":"ask","skill":{"denied-skill":"deny"}}}`).visibleSkillEntries(prompt, "/cwd")
		if len(e) != 1 {
			t.Fatalf("entries: %#v", e)
		}
		eq(t, e[0].name, "visible-skill", "name")
	})
	tw(t, f, "REGRESSION: visibleSkillPromptEntries keeps only visible skills available for path matching", func(t *testing.T) {
		prompt := strings.Join([]string{"System prompt start", "<available_skills>", "  <skill>", "    <name>blocked-skill</name>",
			"    <description>Blocked skill</description>", "    <location>@./skills/blocked/entry.ts</location>", "  </skill>",
			"</available_skills>", "Middle section", "<available_skills>", "  <skill>", "    <name>visible-skill</name>",
			"    <description>Visible skill</description>", "    <location>@./skills/visible/entry.ts</location>", "  </skill>",
			"</available_skills>", "System prompt end"}, "\n")
		e := policyOf(t, `{"permission":{"*":"ask","skill":{"blocked-skill":"deny"}}}`).visibleSkillEntries(prompt, "/cwd")
		m := findSkillPathMatch("/cwd/skills/visible/file.ts", e)
		eq(t, m != nil && m.name == "visible-skill", true, "the visible skill")
		eq(t, findSkillPathMatch("/cwd/skills/blocked/file.ts", e) == nil, true, "not the denied one")
	})
}
