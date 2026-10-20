package pi_permission_system

import (
	"strings"
	"testing"
)

// skillCall is one checkPermission call the mock saw.
type skillCall struct {
	surface string
	input   any
	agent   *string
}

// mockSkillChecker is makeManager() of the upstream test: a checker answering a default state with overrides by skill name, which
// records its calls (the original's vi.fn).
type mockSkillChecker struct {
	defaultState string
	overrides    map[string]string
	calls        []skillCall
}

func makeManager(defaultState string, overrides map[string]string) *mockSkillChecker {
	return &mockSkillChecker{defaultState: defaultState, overrides: overrides}
}

func (m *mockSkillChecker) CheckPermission(surface string, input any, agentName *string) PermissionCheckResult {
	m.calls = append(m.calls, skillCall{surface, input, agentName})
	name, _ := ToRecord(input).str("name")
	state := m.defaultState
	if override, ok := m.overrides[name]; ok {
		state = override
	}
	return PermissionCheckResult{ToolName: "skill", State: state, Source: "tool", Origin: "builtin"}
}

// policySkillChecker adapts a Policy to the raw SkillPermissionChecker contract, as the original's asChecker adapts a real
// PermissionManager (a tool intent on the skill surface).
type policySkillChecker struct{ p *Policy }

func (c policySkillChecker) CheckPermission(surface string, input any, _ *string) PermissionCheckResult {
	name, _ := ToRecord(input).str("name")
	return PermissionCheckResult{ToolName: "skill", State: Evaluate(surface, name, c.p.rules, PosixPathFlavor, "").Action, Source: "tool", Origin: "builtin"}
}

func skillBlock(name, location string) string {
	if location == "" {
		location = "/skills/" + name + "/SKILL.md"
	}
	return strings.Join([]string{"  <skill>", "    <name>" + name + "</name>", "    <description>Description of " + name + "</description>",
		"    <location>" + location + "</location>", "  </skill>"}, "\n")
}

func availableSkillsSection(names ...string) string {
	lines := []string{"<available_skills>"}
	for _, n := range names {
		lines = append(lines, skillBlock(n, ""))
	}
	return strings.Join(append(lines, "</available_skills>"), "\n")
}

func TestSkillPromptSanitizerTwins(t *testing.T) {
	const f = "exposure/skill-prompt-sanitizer"
	const cwd = "/projects/my-app"
	// findSkillPathMatch only uses the normalizer's platform (it compares two already-absolute paths via isWithinDirectory), so this
	// CWD-baked instance serves every call regardless of the entries' cwd.
	normalizer := NewPathNormalizer(PosixPathFlavor, cwd)
	visible := func(prompt string, m SkillPermissionChecker, agent *string) []SkillPromptEntry {
		return VisibleSkillPromptEntries(prompt, m, agent, normalizer)
	}
	names := func(entries []SkillPromptEntry) []string {
		out := []string{}
		for _, e := range entries {
			out = append(out, e.Name)
		}
		return out
	}
	S := func(s ...string) []string { return s }

	// visibleSkillPromptEntries
	tw(t, f, "returns nothing and checks nothing when no skills section is present", func(t *testing.T) {
		manager := makeManager("deny", nil)
		entries := visible("You are a helpful assistant.", manager, nil)
		eq(t, entries, []SkillPromptEntry{}, "entries")
		eq(t, len(manager.calls), 0, "calls")
	})
	tw(t, f, "keeps all skills visible when all are allowed", func(t *testing.T) {
		eq(t, names(visible(availableSkillsSection("librarian", "ask-user"), makeManager("allow", nil), nil)), S("librarian", "ask-user"), "names")
	})
	tw(t, f, "keeps a denied skill out of the visible entries", func(t *testing.T) {
		eq(t, names(visible(availableSkillsSection("alpha", "beta"), makeManager("allow", map[string]string{"beta": "deny"}), nil)), S("alpha"), "names")
	})
	tw(t, f, "keeps an ask-state skill visible", func(t *testing.T) {
		entries := visible(availableSkillsSection("alpha", "beta"), makeManager("allow", map[string]string{"beta": "ask"}), nil)
		eq(t, [][2]string{{entries[0].Name, entries[0].State}, {entries[1].Name, entries[1].State}}, [][2]string{{"alpha", "allow"}, {"beta", "ask"}}, "states")
		eq(t, len(entries), 2, "len")
	})
	tw(t, f, "classifies every catalogue in the prompt", func(t *testing.T) {
		input := availableSkillsSection("alpha") + "\n" + availableSkillsSection("beta")
		eq(t, names(visible(input, makeManager("allow", map[string]string{"beta": "deny"}), nil)), S("alpha"), "names")
	})
	tw(t, f, "delegates permission check to permissionManager for each skill", func(t *testing.T) {
		manager := makeManager("allow", nil)
		visible(availableSkillsSection("alpha", "beta"), manager, nil)
		eq(t, manager.calls, []skillCall{{"skill", jo(t, `{"name":"alpha"}`), nil}, {"skill", jo(t, `{"name":"beta"}`), nil}}, "calls")
	})
	tw(t, f, "passes agentName to permissionManager", func(t *testing.T) {
		manager := makeManager("allow", nil)
		visible(availableSkillsSection("librarian"), manager, sp("my-agent"))
		eq(t, manager.calls, []skillCall{{"skill", jo(t, `{"name":"librarian"}`), sp("my-agent")}}, "calls")
	})
	tw(t, f, "passes agentName to permissionManager", func(t *testing.T) {
		// withoutDeniedSkills passes it the same way.
		manager := makeManager("allow", nil)
		WithoutDeniedSkills([]string{"librarian"}, func(s string) string { return s }, manager, sp("my-agent"))
		eq(t, manager.calls, []skillCall{{"skill", jo(t, `{"name":"librarian"}`), sp("my-agent")}}, "calls")
	})
	tw(t, f, "caches permission result: checkPermission called once per unique skill name", func(t *testing.T) {
		// Same skill appears in two separate sections.
		input := strings.Join([]string{availableSkillsSection("librarian"), availableSkillsSection("librarian")}, "\n")
		manager := makeManager("allow", nil)
		visible(input, manager, nil)
		eq(t, len(manager.calls), 1, "calls")
	})
	tw(t, f, "resolves entry normalizedLocation relative to cwd", func(t *testing.T) {
		entries := visible(availableSkillsSection("librarian"), makeManager("allow", nil), nil)
		eq(t, entries[0].NormalizedLocation, "/skills/librarian/SKILL.md", "location")
		eq(t, entries[0].NormalizedBaseDir, "/skills/librarian", "base dir")
	})

	// withoutDeniedSkills
	type named struct{ Name string }
	nameOf := func(n named) string { return n.Name }
	tw(t, f, "drops a denied skill and keeps the rest in order", func(t *testing.T) {
		manager := makeManager("allow", map[string]string{"beta": "deny"})
		got := WithoutDeniedSkills([]named{{"alpha"}, {"beta"}, {"gamma"}}, nameOf, manager, nil)
		eq(t, got, []named{{"alpha"}, {"gamma"}}, "skills")
	})
	tw(t, f, "keeps an ask-state skill", func(t *testing.T) {
		manager := makeManager("allow", map[string]string{"beta": "ask"})
		eq(t, WithoutDeniedSkills([]named{{"alpha"}, {"beta"}}, nameOf, manager, nil), []named{{"alpha"}, {"beta"}}, "skills")
	})

	// findSkillPathMatch
	entries := []SkillPromptEntry{
		{Name: "librarian", Description: "desc", Location: "/skills/librarian/SKILL.md", State: "allow", NormalizedLocation: "/skills/librarian/SKILL.md", NormalizedBaseDir: "/skills/librarian"},
		{Name: "ask-user", Description: "desc", Location: "/skills/ask-user/SKILL.md", State: "allow", NormalizedLocation: "/skills/ask-user/SKILL.md", NormalizedBaseDir: "/skills/ask-user"},
	}
	matchName := func(e *SkillPromptEntry) string {
		if e == nil {
			return "<null>"
		}
		return e.Name
	}
	tw(t, f, "returns null for empty normalized path", func(t *testing.T) {
		eq(t, FindSkillPathMatch("", entries, normalizer) == nil, true, "nil")
	})
	tw(t, f, "returns null for empty entries array", func(t *testing.T) {
		eq(t, FindSkillPathMatch("/skills/librarian/SKILL.md", nil, normalizer) == nil, true, "nil")
	})
	tw(t, f, "matches exact location path", func(t *testing.T) {
		eq(t, matchName(FindSkillPathMatch("/skills/librarian/SKILL.md", entries, normalizer)), "librarian", "name")
	})
	tw(t, f, "matches path within skill base directory", func(t *testing.T) {
		eq(t, matchName(FindSkillPathMatch("/skills/librarian/extra/helper.md", entries, normalizer)), "librarian", "name")
	})
	tw(t, f, "returns null for path not within any skill directory", func(t *testing.T) {
		eq(t, FindSkillPathMatch("/other/path/file.md", entries, normalizer) == nil, true, "nil")
	})
	tw(t, f, "returns null for sibling path that shares a prefix", func(t *testing.T) {
		// "/skills/librarian-extra" should not match "/skills/librarian"
		eq(t, FindSkillPathMatch("/skills/librarian-extra/SKILL.md", entries, normalizer) == nil, true, "nil")
	})
	tw(t, f, "prefers longer matching base directory (most specific skill wins)", func(t *testing.T) {
		nested := []SkillPromptEntry{
			{Name: "parent", Description: "desc", Location: "/skills/parent/SKILL.md", State: "allow", NormalizedLocation: "/skills/parent/SKILL.md", NormalizedBaseDir: "/skills/parent"},
			{Name: "child", Description: "desc", Location: "/skills/parent/child/SKILL.md", State: "allow", NormalizedLocation: "/skills/parent/child/SKILL.md", NormalizedBaseDir: "/skills/parent/child"},
		}
		eq(t, matchName(FindSkillPathMatch("/skills/parent/child/helper.md", nested, normalizer)), "child", "name")
	})

	// Moved from permission-system.test.ts catch-all (#342)
	tw(t, f, "parseAllSkillPromptSections finds every available_skills block", func(t *testing.T) {
		prompt := strings.Join([]string{"Some preamble", "<available_skills>", "  <skill>", "    <name>skill-one</name>",
			"    <description>First skill</description>", "    <location>/path/to/one</location>", "  </skill>", "</available_skills>",
			"Some content between", "<available_skills>", "  <skill>", "    <name>skill-two</name>", "    <description>Second skill</description>",
			"    <location>/path/to/two</location>", "  </skill>", "</available_skills>", "Footer"}, "\n")
		sections := ParseAllSkillPromptSections(prompt)
		eq(t, len(sections), 2, "sections")
		eq(t, sections[0].Entries[0].Name, "skill-one", "first")
		eq(t, sections[1].Entries[0].Name, "skill-two", "second")
	})
	tw(t, f, "REGRESSION: visibleSkillPromptEntries excludes a denied skill from every available_skills block", func(t *testing.T) {
		prompt := strings.Join([]string{"System prompt start", "<available_skills>", "  <skill>", "    <name>visible-skill</name>",
			"    <description>Allowed skill</description>", "    <location>/skills/visible/index.ts</location>", "  </skill>", "  <skill>",
			"    <name>denied-skill</name>", "    <description>Denied in first block</description>", "    <location>/skills/blocked/one.ts</location>",
			"  </skill>", "</available_skills>", "Agent identity section", "<available_skills>", "  <skill>", "    <name>denied-skill</name>",
			"    <description>Denied in second block</description>", "    <location>/skills/blocked/two.ts</location>", "  </skill>",
			"</available_skills>", "System prompt end"}, "\n")
		checker := policySkillChecker{policyOf(t, `{"permission":{"*":"ask","skill":{"denied-skill":"deny"}}}`)}
		got := VisibleSkillPromptEntries(prompt, checker, nil, NewPathNormalizer(PosixPathFlavor, "/cwd"))
		eq(t, names(got), S("visible-skill"), "names")
	})
	tw(t, f, "REGRESSION: visibleSkillPromptEntries keeps only visible skills available for path matching", func(t *testing.T) {
		prompt := strings.Join([]string{"System prompt start", "<available_skills>", "  <skill>", "    <name>blocked-skill</name>",
			"    <description>Blocked skill</description>", "    <location>@./skills/blocked/entry.ts</location>", "  </skill>",
			"</available_skills>", "Middle section", "<available_skills>", "  <skill>", "    <name>visible-skill</name>",
			"    <description>Visible skill</description>", "    <location>@./skills/visible/entry.ts</location>", "  </skill>",
			"</available_skills>", "System prompt end"}, "\n")
		checker := policySkillChecker{policyOf(t, `{"permission":{"*":"ask","skill":{"blocked-skill":"deny"}}}`)}
		got := VisibleSkillPromptEntries(prompt, checker, nil, NewPathNormalizer(PosixPathFlavor, "/cwd"))
		matchedVisible := FindSkillPathMatch("/cwd/skills/visible/file.ts", got, normalizer)
		matchedBlocked := FindSkillPathMatch("/cwd/skills/blocked/file.ts", got, normalizer)
		eq(t, matchName(matchedVisible), "visible-skill", "visible")
		eq(t, matchedBlocked == nil, true, "blocked")
	})
}
