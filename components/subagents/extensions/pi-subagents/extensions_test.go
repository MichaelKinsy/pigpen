package pi_subagents

import (
	"path/filepath"
	"testing"
)

// The original's `extensions` / `subagentOnlyExtensions` frontmatter and `subagents.defaultExtensions` /
// `defaultSubagentOnlyExtensions` settings, applied to the port's child command line.

func TestChildExtensionArgs(t *testing.T) {
	base := []string{"--print"}
	eq(t, buildChildArgs(&AgentConfig{Name: "a", SubagentOnlyExtensions: []string{"/x.ts"}}, ""), append(base, "--extension", "/x.ts"), "child-only extensions keep discovery")
	eq(t, buildChildArgs(&AgentConfig{Name: "a", Extensions: []string{}}, ""), append(base, "--no-extensions"), "an empty list loads nothing")
	eq(t, buildChildArgs(&AgentConfig{Name: "a", Extensions: []string{"/p.ts", "npm:q"}, SubagentOnlyExtensions: []string{"/p.ts", "/c.ts"}}, ""),
		append(base, "--no-extensions", "--extension", "/p.ts", "--extension", "npm:q", "--extension", "/c.ts"), "a list, then child-only, each once")
	eq(t, noExtensionsWarning(&AgentConfig{Name: "a"}), "", "no warning when unset")
	eq(t, noExtensionsWarning(&AgentConfig{Name: "a", Extensions: []string{"/p.ts"}}), "", "no warning for a list")
	eq(t, noExtensionsWarning(&AgentConfig{Name: "a", Extensions: []string{}}),
		`extensions: [] override for agent 'a' disables ALL ambient extensions for this child (not just "adds nothing"), including any model-provider extension needed to resolve a provider-qualified model. List the extensions this child actually needs instead of an empty array.`,
		"the original's warning")
}

func TestExtensionFrontmatter(t *testing.T) {
	withTempHome(t)
	cwd := tmp(t)
	dir := filepath.Join(cwd, ".pi", "agents")
	writeFile(t, filepath.Join(dir, "listed.md"), "---\nname: listed\ndescription: L\nextensions: ./ext/p.ts, ../q.ts, npm:r, /abs/s.ts\nsubagentOnlyExtensions:\n  - ./child.ts\n---\n")
	writeFile(t, filepath.Join(dir, "empty.md"), "---\nname: empty\ndescription: E\nextensions:\n---\n")
	writeFile(t, filepath.Join(dir, "plain.md"), "---\nname: plain\ndescription: P\n---\n")
	d := DiscoverAgentsAll(cwd)
	listed := findAgent(d.Project, "listed")
	eq(t, listed.Extensions, []string{filepath.Join(dir, "ext", "p.ts"), filepath.Join(cwd, ".pi", "q.ts"), "npm:r", "/abs/s.ts"}, "relative to the agent file, others as written")
	eq(t, listed.SubagentOnlyExtensions, []string{filepath.Join(dir, "child.ts")}, "a block list")
	eq(t, findAgent(d.Project, "empty").Extensions, []string{}, "empty is a list")
	eq(t, findAgent(d.Project, "plain").Extensions == nil, true, "absent is unset")
	eq(t, findAgent(d.Project, "plain").SubagentOnlyExtensions == nil, true, "absent is unset")
}

func TestExtensionSettings(t *testing.T) {
	home := withTempHome(t)
	cwd := tmp(t)
	userSettings := filepath.Join(home, ".pi", "agent", "settings.json")
	projectSettings := filepath.Join(cwd, ".pi", "settings.json")

	d, err := extensionDefaultsFor(cwd, ScopeBoth)
	eq(t, err, nil, "no settings")
	eq(t, d, extensionDefaults{}, "no settings set nothing")

	writeFile(t, userSettings, `{"subagents": {"defaultExtensions": [" /u.ts "], "defaultSubagentOnlyExtensions": ["/uc.ts"]}}`)
	d, _ = extensionDefaultsFor(cwd, ScopeBoth)
	eq(t, d, extensionDefaults{[]string{"/u.ts"}, []string{"/uc.ts"}}, "the user's, trimmed")
	own := d.apply(AgentConfig{Name: "a", Extensions: []string{}})
	eq(t, own.Extensions, []string{}, "an agent's own list wins, even empty")
	eq(t, own.SubagentOnlyExtensions, []string{"/uc.ts"}, "the unset field takes the default")

	writeFile(t, projectSettings, `{"subagents": {"defaultExtensions": []}}`)
	d, _ = extensionDefaultsFor(cwd, ScopeBoth)
	eq(t, d, extensionDefaults{[]string{}, []string{"/uc.ts"}}, "the project's where it sets one")
	d, _ = extensionDefaultsFor(cwd, ScopeUser)
	eq(t, d.extensions, []string{"/u.ts"}, "scope user reads no project settings")
	d, _ = extensionDefaultsFor(cwd, ScopeProject)
	eq(t, d, extensionDefaults{[]string{}, nil}, "scope project reads no user settings")

	writeFile(t, projectSettings, `{"subagents": {"defaultSubagentOnlyExtensions": ["ok", " "]}}`)
	_, err = extensionDefaultsFor(cwd, ScopeBoth)
	hasMatch(t, err, "Subagent settings in '"+projectSettings+"' have invalid 'defaultSubagentOnlyExtensions'; expected an array of non-empty strings.")
	writeFile(t, projectSettings, `{"subagents": {"defaultExtensions": "/p.ts"}}`)
	_, err = extensionDefaultsFor(cwd, ScopeBoth)
	hasMatch(t, err, "have invalid 'defaultExtensions'")
	writeFile(t, projectSettings, `{`)
	_, err = extensionDefaultsFor(cwd, ScopeBoth)
	hasMatch(t, err, "Failed to parse settings file '"+projectSettings+"'")
}

func TestExtensionSettingsFromTheHost(t *testing.T) {
	home := withTempHome(t)
	cwd := tmp(t)
	pigUser := filepath.Join(home, ".pig", "agent", "settings.json")
	piUser := filepath.Join(home, ".pi", "agent", "settings.json")
	pigProject := filepath.Join(cwd, ".pig", "settings.json")
	piProject := filepath.Join(cwd, ".pi", "settings.json")
	t.Setenv("PIG_EXT_HOST_INFO", `{"agentDir": "`+filepath.Join(home, ".pig", "agent")+`", "configDirName": ".pig"}`)

	writeFile(t, piUser, `{"subagents": {"defaultExtensions": ["/pi-user.ts"], "defaultSubagentOnlyExtensions": ["/pi-user-child.ts"]}}`)
	writeFile(t, pigUser, `{"subagents": {"defaultSubagentOnlyExtensions": ["/pig-user-child.ts"]}}`)
	d, err := extensionDefaultsFor(cwd, ScopeBoth)
	eq(t, err, nil, "error")
	eq(t, d, extensionDefaults{[]string{"/pi-user.ts"}, []string{"/pig-user-child.ts"}}, "the host's user file over Pi's, field by field")

	writeFile(t, piProject, `{"subagents": {"defaultSubagentOnlyExtensions": ["/pi-project-child.ts"]}}`)
	d, _ = extensionDefaultsFor(cwd, ScopeBoth)
	eq(t, d.subagentOnly, []string{"/pi-project-child.ts"}, "any project file over any user file")
	writeFile(t, pigProject, `{"subagents": {"defaultSubagentOnlyExtensions": ["/pig-project-child.ts"]}}`)
	d, _ = extensionDefaultsFor(cwd, ScopeBoth)
	eq(t, d.subagentOnly, []string{"/pig-project-child.ts"}, "the host's project file over Pi's")
	d, _ = extensionDefaultsFor(cwd, ScopeUser)
	eq(t, d.subagentOnly, []string{"/pig-user-child.ts"}, "scope user reads no project file")

	writeFile(t, pigUser, `{"subagents": {"defaultExtensions": {}}}`)
	_, err = extensionDefaultsFor(cwd, ScopeBoth)
	hasMatch(t, err, "Subagent settings in '"+pigUser+"' have invalid 'defaultExtensions'")
	_, err = extensionDefaultsFor(cwd, ScopeProject)
	eq(t, err, nil, "scope project does not read the user file")
}
