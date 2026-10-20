package pi_permission_system

import (
	"strings"
	"testing"
)

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
