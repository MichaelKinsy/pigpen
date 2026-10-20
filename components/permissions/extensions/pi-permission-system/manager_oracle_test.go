package pi_permission_system

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The differential for the permission manager: testdata/manager-oracle.json holds every verdict the original's PermissionManager gave
// over the worlds of port/probe/manager-cases.json (config files, agent files, session rules, yolo, MCP servers; recorded by
// port/record-manager-probe.sh), and this test replays the corpus through the port. Any difference fails: an allow where the original
// asks or denies is a security bug.

type managerWorld struct {
	ID                      string            `json:"id"`
	Global                  *string           `json:"global"`
	Project                 *string           `json:"project"`
	Agents                  map[string]string `json:"agents"`
	ProjectAgents           map[string]string `json:"projectAgents"`
	UnreadableAgents        []string          `json:"unreadableAgents"`
	UnreadableProjectAgents []string          `json:"unreadableProjectAgents"`
	Mcp                     []string          `json:"mcp"`
	Yolo                    bool              `json:"yolo"`
	SessionRules            []struct {
		Surface, Pattern, Action, Layer, Origin string
	} `json:"sessionRules"`
	AgentNames []*string         `json:"agentNames"`
	Tools      []string          `json:"tools"`
	Intents    []json.RawMessage `json:"intents"`
}

type managerOracle struct {
	Home   string `json:"home"`
	Worlds []struct {
		ID      string                    `json:"id"`
		Results []map[string]any          `json:"results"`
		Display map[string]map[string]any `json:"display"`
	} `json:"worlds"`
}

func checkResultJSON(r PermissionCheckResult) map[string]any {
	m := map[string]any{"toolName": r.ToolName, "state": r.State, "source": r.Source, "origin": r.Origin}
	if r.Reason != nil {
		m["reason"] = *r.Reason
	}
	if r.MatchedPattern != nil {
		m["matchedPattern"] = *r.MatchedPattern
	}
	if r.Command != nil {
		m["command"] = *r.Command
	}
	if r.Target != nil {
		m["target"] = *r.Target
	}
	return m
}

func ruleJSON(r Rule) map[string]any {
	m := map[string]any{"surface": r.Surface, "pattern": r.Pattern, "action": r.Action, "origin": r.Origin}
	if r.Layer != "" {
		m["layer"] = r.Layer
	}
	if r.Reason != nil {
		m["reason"] = *r.Reason
	}
	return m
}

func (w managerWorld) intent(t *testing.T, raw json.RawMessage) ResolvedAccessIntent {
	t.Helper()
	var head struct {
		Kind      string          `json:"kind"`
		Surface   string          `json:"surface"`
		AgentName *string         `json:"agentName"`
		Values    []string        `json:"values"`
		Command   string          `json:"command"`
		Spellings []string        `json:"spellings"`
		Input     json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		t.Fatal(err)
	}
	switch head.Kind {
	case "tool":
		input, err := parseJSON(head.Input)
		if err != nil {
			t.Fatal(err)
		}
		return ToolAccessIntent{Surface: head.Surface, Input: input, AgentName: head.AgentName}
	case "path-values":
		return PathValuesAccessIntent{Surface: head.Surface, Values: head.Values, AgentName: head.AgentName}
	case "bash-command":
		return BashCommandAccessIntent{Surface: head.Surface, Command: head.Command, Spellings: head.Spellings, AgentName: head.AgentName}
	}
	t.Fatalf("intent kind %q", head.Kind)
	return nil
}

func scrubBase(v any, base string) any {
	data, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal([]byte(strings.ReplaceAll(string(data), strings.ReplaceAll(base, `\`, `\\`), "<BASE>")), &out)
	return out
}

func normalizeJSON(v any) any {
	data, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(data, &out)
	return out
}

func TestManagerOracleDifferential(t *testing.T) {
	data, err := os.ReadFile("testdata/manager-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle managerOracle
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	casesData, err := os.ReadFile("../../port/probe/manager-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var worlds []managerWorld
	if err := json.Unmarshal(casesData, &worlds); err != nil {
		t.Fatal(err)
	}
	if len(worlds) != len(oracle.Worlds) {
		t.Fatalf("%d worlds in the corpus, %d in the record", len(worlds), len(oracle.Worlds))
	}
	old := homeDir
	homeDir = func() string { return oracle.Home }
	t.Cleanup(func() { homeDir = old })

	checks, differences := 0, 0
	for i, w := range worlds {
		want := oracle.Worlds[i]
		if want.ID != w.ID {
			t.Fatalf("world %d: corpus %q, record %q", i, w.ID, want.ID)
		}
		base := t.TempDir()
		globalConfigPath := filepath.Join(base, "pi-permissions.jsonc")
		agentsDir := filepath.Join(base, "agents")
		projectRoot := filepath.Join(base, "project")
		projectConfigPath := filepath.Join(projectRoot, "pi-permissions.jsonc")
		projectAgentsDir := filepath.Join(projectRoot, "agents")
		for _, d := range []string{agentsDir, projectAgentsDir} {
			if err := os.MkdirAll(d, 0o777); err != nil {
				t.Fatal(err)
			}
		}
		if w.Global != nil {
			polWriteFile(t, globalConfigPath, *w.Global)
		}
		if w.Project != nil {
			polWriteFile(t, projectConfigPath, *w.Project)
		}
		for n, c := range w.Agents {
			polWriteFile(t, filepath.Join(agentsDir, n+".md"), c)
		}
		for n, c := range w.ProjectAgents {
			polWriteFile(t, filepath.Join(projectAgentsDir, n+".md"), c)
		}
		for _, n := range w.UnreadableAgents {
			if err := os.Mkdir(filepath.Join(agentsDir, n+".md"), 0o777); err != nil {
				t.Fatal(err)
			}
		}
		for _, n := range w.UnreadableProjectAgents {
			if err := os.Mkdir(filepath.Join(projectAgentsDir, n+".md"), 0o777); err != nil {
				t.Fatal(err)
			}
		}
		yolo := w.Yolo
		m := NewPermissionManager(PermissionManagerOptions{
			PolicyLoaderOptions: PolicyLoaderOptions{GlobalConfigPath: globalConfigPath, AgentsDir: agentsDir, ProjectGlobalConfigPath: projectConfigPath,
				ProjectAgentsDir: projectAgentsDir, McpServerNames: append([]string{}, w.Mcp...)},
			IsYoloEnabled: func() bool { return yolo },
		})
		var session Ruleset
		for _, r := range w.SessionRules {
			session = append(session, Rule{Surface: r.Surface, Pattern: r.Pattern, Action: r.Action, Layer: r.Layer, Origin: r.Origin})
		}

		for j, raw := range w.Intents {
			checks++
			got := checkResultJSON(m.Check(w.intent(t, raw), session))
			wantResult, ok := want.Results[j]["result"]
			if !ok {
				t.Errorf("%s intent %d %s: the original threw (%v), the port answered %v", w.ID, j, raw, want.Results[j]["throws"], got)
				differences++
				continue
			}
			if !reflect.DeepEqual(normalizeJSON(got), wantResult) {
				differences++
				t.Errorf("%s intent %d %s (session %v):\n  port     %v\n  original %v", w.ID, j, raw, session, got, wantResult)
			}
		}

		for _, a := range w.AgentNames {
			key := "<none>"
			if a != nil {
				key = *a
			}
			perms, denied := map[string]any{}, map[string]any{}
			for _, tool := range w.Tools {
				perms[tool] = m.GetToolPermission(tool, a)
				denied[tool] = m.IsToolFullyDenied(tool, a)
			}
			issues := m.GetConfigIssues(a)
			composed := []any{}
			for _, r := range m.GetComposedConfigRules(a) {
				composed = append(composed, ruleJSON(r))
			}
			got := map[string]any{"toolPermission": perms, "fullyDenied": denied, "issues": scrubBase(issues, base), "composed": composed}
			for k, v := range got {
				checks++
				if !reflect.DeepEqual(normalizeJSON(v), want.Display[key][k]) {
					differences++
					t.Errorf("%s agent %s %s:\n  port     %v\n  original %v", w.ID, key, k, v, want.Display[key][k])
				}
			}
		}
	}
	t.Logf("%d worlds, %d comparisons, %d differences", len(worlds), checks, differences)
}
