package pi_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The management actions (create/update/delete/eject, disable/enable/reset)
// with the original's texts and file formats.

func mgmtHost(t *testing.T) (cwd, home string) {
	t.Helper()
	home = withTempHome(t)
	cwd = tmp(t)
	return
}

func TestCreateWritesTheOriginalsFile(t *testing.T) {
	cwd, _ := mgmtHost(t)
	r := createAction(cwd, map[string]any{"config": map[string]any{
		"name": "My Helper", "description": "Helps.", "scope": "project",
		"model": "p/m", "tools": "read, grep, mcp:docs", "aliases": "helper-alias", "thinking": "high",
	}})
	if r.isError {
		t.Fatalf("create: %s", r.text)
	}
	path := filepath.Join(cwd, ".pi", "agents", "my-helper.md")
	if !strings.HasPrefix(r.text, "Created agent 'my-helper' at "+path) {
		t.Errorf("headline: %s", r.text)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"---",
		"name: my-helper",
		"description: Helps.",
		"aliases: helper-alias",
		"tools: read, grep, mcp:docs",
		"model: p/m",
		"thinking: high",
		"systemPromptMode: replace",
		"inheritProjectContext: false",
		"inheritSkills: false",
		"---",
		"",
		"",
		"",
	}, "\n")
	eq(t, string(content), want, "the created file")
}

func TestCreateValidatesTheOriginalsWay(t *testing.T) {
	cwd, _ := mgmtHost(t)
	cases := []struct {
		cfg  map[string]any
		want string
	}{
		{map[string]any{}, "config.name is required and must be a non-empty string."},
		{map[string]any{"name": "x"}, "config.description is required and must be a non-empty string."},
		{map[string]any{"name": "@@@", "description": "d"}, "config.name is invalid after sanitization. Use letters, numbers, spaces, or hyphens."},
		{map[string]any{"name": "x", "description": "d", "scope": "team"}, "config.scope must be 'user' or 'project'."},
		{map[string]any{"name": "x", "description": "d", "steps": []any{map[string]any{"agent": "a"}}}, "Durable chain definitions were removed; use workflowScript or /prompt-workflow for repeatable workflows."},
		{map[string]any{"name": "x", "description": "d", "systemPromptMode": "sometimes"}, "config.systemPromptMode must be 'append' or 'replace' when provided."},
		{map[string]any{"name": "x", "description": "d", "timeoutMs": 0}, "config.timeoutMs must be a positive integer or false when provided."},
		{map[string]any{"name": "x", "description": "d", "fallbackModels": []any{"m"}}, "config.fallbackModels was removed; configure one model instead."},
		{map[string]any{"name": "x", "description": "d", "toolBudget": map[string]any{"soft": 5, "hard": 4}}, "config.toolBudget.soft must be <= config.toolBudget.hard."},
		{map[string]any{"name": "x", "description": "d", "runner": map[string]any{"type": "external-cli"}}, runnerConfigError()},
	}
	for i, c := range cases {
		r := createAction(cwd, map[string]any{"config": c.cfg})
		if !r.isError || r.text != c.want {
			t.Errorf("case %d: got %q, want %q", i, r.text, c.want)
		}
	}
	// A config string that is not JSON keeps the original's message.
	r := createAction(cwd, map[string]any{"config": "{nope"})
	if !r.isError || !strings.HasPrefix(r.text, "config must be valid JSON: ") {
		t.Errorf("json string: %q", r.text)
	}
	// An existing name is refused, and a builtin's name shadows with a note.
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "taken.md"), agentMD("taken", "T", "T."))
	r = createAction(cwd, map[string]any{"config": map[string]any{"name": "taken", "description": "d", "scope": "project"}})
	eq(t, r.text, "Name 'taken' already exists in project scope. Use update instead.", "existing name")
	r = createAction(cwd, map[string]any{"config": map[string]any{"name": "scout", "description": "d", "scope": "project"}})
	if !strings.Contains(r.text, "Note: this shadows the builtin agent 'scout'.") {
		t.Errorf("shadow warning: %s", r.text)
	}
}

func TestUpdateRoundTripPreservesDeclaredKeys(t *testing.T) {
	cwd, _ := mgmtHost(t)
	path := filepath.Join(cwd, ".pi", "agents", "portable.md")
	writeFile(t, path, "---\nname: portable\ndescription: Portable agent\nextensions: ./tools/parent.ts, package-extension\nsubagentOnlyExtensions: ../child.ts\nvendor_key: kept verbatim\n---\nOriginal prompt.\n")
	r := updateAction(cwd, map[string]any{"agent": "portable", "config": map[string]any{"description": "Updated agent"}})
	eq(t, r.isError, false, "update")
	eq(t, r.text, "Updated agent 'portable' at "+path+".", "headline")
	content, _ := os.ReadFile(path)
	text := string(content)
	for _, want := range []string{
		"description: Updated agent",
		"extensions: ./tools/parent.ts, package-extension",
		"subagentOnlyExtensions: ../child.ts",
		"vendor_key: kept verbatim",
		"\n\nOriginal prompt.\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("file missing %q:\n%s", want, text)
		}
	}
	// The updated file loads with the new description and keeps the extras.
	d := DiscoverAgentsAll(cwd)
	a, _ := ResolveAgentName("portable", d.Project)
	if a == nil || a.Description != "Updated agent" {
		t.Fatalf("reload: %+v", a)
	}
	// The declaration on disk stays relative; the loaded config carries the resolved path (#59's semantics).
	if len(a.Extensions) != 2 || a.Extensions[0] != filepath.Join(cwd, ".pi", "agents", "tools", "parent.ts") {
		t.Errorf("extensions: %v", a.Extensions)
	}

	// A rename moves the file and the headline names both agents.
	r = updateAction(cwd, map[string]any{"agent": "portable", "config": map[string]any{"name": "Portable Two"}})
	eq(t, r.isError, false, "rename")
	if !strings.Contains(r.text, "Updated agent 'portable' to 'portable-two' at "+filepath.Join(cwd, ".pi", "agents", "portable-two.md")) {
		t.Errorf("rename headline: %s", r.text)
	}
	if fileExists(path) {
		t.Error("the old file still exists")
	}
}

func TestUpdateValidatesTheOriginalsWay(t *testing.T) {
	cwd, _ := mgmtHost(t)
	cases := []struct {
		params map[string]any
		want   string
	}{
		{map[string]any{"config": map[string]any{"description": "d"}}, "Specify 'agent' for update."},
		{map[string]any{"agent": "x"}, "config required for update."},
		{map[string]any{"agent": "x", "config": map[string]any{"steps": []any{}}}, "Durable chain definitions were removed; use workflowScript or /prompt-workflow for repeatable workflows."},
		{map[string]any{"agent": "scout", "config": map[string]any{"description": "d"}}, "Agent 'scout' is read-only and cannot be modified. Create a same-named agent in user or project scope to override it."},
		{map[string]any{"agent": "nope", "config": map[string]any{"description": "d"}}, "Agent 'nope' not found. Available: scout."},
		{map[string]any{"agent": "x", "config": map[string]any{"name": " "}}, "config.name must be a non-empty string when provided."},
	}
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "x.md"), agentMD("x", "X", "X."))
	for i, c := range cases {
		r := updateAction(cwd, c.params)
		if !r.isError {
			t.Errorf("case %d: no error", i)
			continue
		}
		if i == 4 { // the available list carries the builtins
			if !strings.HasPrefix(r.text, "Agent 'nope' not found. Available:") {
				t.Errorf("case %d: %q", i, r.text)
			}
			continue
		}
		eq(t, r.text, c.want, "case "+string(rune('0'+i)))
	}
	// A name in both scopes updates the one agentScope names; the merged
	// effective set (project over user) is what an unscoped call edits.
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "both.md"), agentMD("both", "P", "P."))
	userAgents := filepath.Join(agentDir(), "agents")
	writeFile(t, filepath.Join(userAgents, "both.md"), agentMD("both", "U", "U."))
	r := updateAction(cwd, map[string]any{"agent": "both", "agentScope": "user", "config": map[string]any{"description": "U2"}})
	eq(t, r.isError, false, "scope picks the user file")
	userContent, _ := os.ReadFile(filepath.Join(userAgents, "both.md"))
	if !strings.Contains(string(userContent), "description: U2") {
		t.Errorf("user file: %s", userContent)
	}
	projectContent, _ := os.ReadFile(filepath.Join(cwd, ".pi", "agents", "both.md"))
	if !strings.Contains(string(projectContent), "description: P") {
		t.Errorf("project file changed: %s", projectContent)
	}
}

func TestDeleteAndEject(t *testing.T) {
	cwd, home := mgmtHost(t)
	path := filepath.Join(cwd, ".pi", "agents", "gone.md")
	writeFile(t, path, agentMD("gone", "G", "G."))
	r := deleteAction(cwd, map[string]any{"agent": "gone"})
	eq(t, r.text, "Deleted agent 'gone' at "+path+".", "delete")
	if fileExists(path) {
		t.Error("the file still exists")
	}
	r = deleteAction(cwd, map[string]any{})
	eq(t, r.text, "Specify 'agent' for delete.", "delete needs an agent")

	// eject copies a builtin into the user scope.
	r = ejectAction(cwd, map[string]any{"agent": "scout"})
	if r.isError {
		t.Fatalf("eject: %s", r.text)
	}
	copied := filepath.Join(userDirForTest(home), "scout.md")
	if !strings.Contains(r.text, "Ejected agent 'scout' from builtin to user scope at "+copied+". Edit it there to customize; it shadows the bundled builtin agent of the same name.") {
		t.Errorf("eject: %s", r.text)
	}
	original, _ := os.ReadFile(filepath.Join("builtin", "scout.md"))
	after, _ := os.ReadFile(copied)
	eq(t, string(after), string(original), "the copy is byte for byte")
	// A second eject explains the existing custom agent.
	r = ejectAction(cwd, map[string]any{"agent": "scout"})
	if !r.isError || !strings.Contains(r.text, "Agent 'scout' is already a custom user agent at "+copied) {
		t.Errorf("second eject: %s", r.text)
	}
	r = ejectAction(cwd, map[string]any{"agent": "helper"})
	if !r.isError || !strings.HasPrefix(r.text, "Agent 'helper' not found or is not a bundled/package agent.") {
		t.Errorf("unknown eject: %s", r.text)
	}
}

// userDirForTest is the user agents dir the discovery uses with this home.
func userDirForTest(home string) string { return filepath.Join(home, ".pi", "agent", "agents") }

func TestDisableEnableReset(t *testing.T) {
	cwd, home := mgmtHost(t)
	r := disableAction(cwd, map[string]any{"agent": "scout"})
	if r.isError {
		t.Fatalf("disable: %s", r.text)
	}
	settingsPath := filepath.Join(home, ".pi", "agent", "settings.json")
	if !strings.Contains(r.text, "Disabled agent 'scout' via user settings override at "+settingsPath+". It is now hidden from runtime discovery and { action: \"list\" }.") {
		t.Errorf("disable: %s", r.text)
	}
	raw, _ := os.ReadFile(settingsPath)
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	overrides := settings["subagents"].(map[string]any)["agentOverrides"].(map[string]any)
	if overrides["scout"].(map[string]any)["disabled"] != true {
		t.Errorf("settings: %s", raw)
	}
	// The agent is hidden from list and runtime discovery, visible to get.
	if l := listAgents(cwd, map[string]any{}); strings.Contains(l.text, "- scout ") {
		t.Errorf("list still shows scout:\n%s", l.text)
	}
	if DiscoverAgents(cwd, ScopeBoth).Agents == nil {
		t.Fatal("no agents")
	}
	for _, a := range DiscoverAgents(cwd, ScopeBoth).Agents {
		if a.Name == "scout" {
			t.Error("runtime discovery still has scout")
		}
	}
	g := getAgent(cwd, map[string]any{"agent": "scout"})
	if !strings.Contains(g.text, "Disabled: true") {
		t.Errorf("get: %s", g.text)
	}
	// enable removes the override.
	r = enableAction(cwd, map[string]any{"agent": "scout"})
	eq(t, r.text, "Enabled agent 'scout' (removed disabled override at "+settingsPath+").", "enable")
	r = enableAction(cwd, map[string]any{"agent": "scout"})
	eq(t, r.text, "Agent 'scout' is already enabled.", "already enabled")
	for _, a := range DiscoverAgents(cwd, ScopeBoth).Agents {
		if a.Name == "scout" && a.Disabled {
			t.Error("scout is still disabled")
		}
	}

	// reset removes a custom file and clears the override. A same-named custom
	// file shadows the builtin; the settings override applies to the custom
	// agent too (applyCustomAgentOverrides in the original), so disable hides
	// it and reset then clears both.
	r = ejectAction(cwd, map[string]any{"agent": "scout"})
	eq(t, r.isError, false, "eject")
	r = disableAction(cwd, map[string]any{"agent": "scout"})
	if r.isError || !strings.Contains(r.text, "Disabled agent 'scout' via user settings override at "+settingsPath+".") {
		t.Errorf("disable with a custom file: %q", r.text)
	}
	if listAgents(cwd, map[string]any{}); strings.Contains(listAgents(cwd, map[string]any{}).text, "- scout ") {
		t.Error("list still shows the disabled custom scout")
	}
	r = resetAction(cwd, map[string]any{"agent": "scout"})
	if r.isError {
		t.Fatalf("reset: %s", r.text)
	}
	want := []string{
		"Deleted custom user agent file at " + filepath.Join(home, ".pi", "agent", "agents", "scout.md") + ".",
		"Removed user settings override at " + settingsPath + ".",
		"Reset agent 'scout' to its bundled builtin default.",
	}
	eq(t, r.text, strings.Join(want, "\n"), "reset lines")
	// Reset with nothing to do says so.
	r = resetAction(cwd, map[string]any{"agent": "scout"})
	eq(t, r.text, "Agent 'scout' has no user customization to reset. It is at its bundled builtin default.", "noop reset")
	// A custom-only agent has no bundled default.
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "local.md"), agentMD("local", "L", "L."))
	r = resetAction(cwd, map[string]any{"agent": "local"})
	if !r.isError || r.text != "Agent 'local' has no bundled default to reset to. Use { action: \"delete\", agent: \"local\" } to remove the custom project agent." {
		t.Errorf("custom reset: %s", r.text)
	}
	// A project override needs a project root.
	empty := tmp(t)
	r = disableAction(empty, map[string]any{"agent": "scout", "agentScope": "project"})
	eq(t, r.text, projectOverrideMissing, "project override without a root")
}
