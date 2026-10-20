package tintinweb_subagents

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The Agent tool's request as Pi sent it, for every combination of the optional features in each toolDescriptionMode
// (port/description-scenarios, recorded from the original under Pi into port/description-golden), against what
// descriptionFlags builds. Every description and every parameter schema is compared byte for byte, the tool list in
// order. The scenarios are not run against the port by `pigeq check`: it refuses the features it does not offer yet.

var setupBody = regexp.MustCompile(`printf '%s' '(.*)' > \.pi/subagents\.json`)

type goldenScenario struct {
	Name       string            `json:"name"`
	Setup      [][]string        `json:"setup"`
	Files      map[string]string `json:"files"`
	AgentFiles map[string]string `json:"agentFiles"`
}

// goldenTools is the tools of the first model request of a recorded trace.
func goldenTools(t *testing.T, path string) []map[string]any {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var e struct {
			Ch   string         `json:"ch"`
			Data map[string]any `json:"data"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Ch != "llm" {
			continue
		}
		var out []map[string]any
		for _, tl := range e.Data["tools"].([]any) {
			out = append(out, tl.(map[string]any))
		}
		return out
	}
	t.Fatalf("no model request in %s", path)
	return nil
}

func TestAgentToolTextEqualsWhatPiSent(t *testing.T) {
	scenarios, _ := filepath.Glob(filepath.Join("..", "..", "port", "description-scenarios", "*.json"))
	if len(scenarios) == 0 {
		t.Skip("port/description-scenarios is not beside this module (a mutation run tests a copy of it)")
	}
	if len(scenarios) != 28 {
		t.Fatalf("%d description scenarios, want 28", len(scenarios))
	}
	for _, path := range scenarios {
		data, _ := os.ReadFile(path)
		var sc goldenScenario
		if err := json.Unmarshal(data, &sc); err != nil {
			t.Fatal(err)
		}
		t.Run(sc.Name, func(t *testing.T) {
			var settingsJSON map[string]any
			m := setupBody.FindStringSubmatch(sc.Setup[0][2])
			if m == nil || json.Unmarshal([]byte(strings.ReplaceAll(m[1], `'\''`, `'`)), &settingsJSON) != nil {
				t.Fatalf("cannot read the settings of %s", sc.Name)
			}
			def, env := setupDesc(t, settingsJSON, func(e *descEnv) {
				for rel, content := range sc.Files {
					e.write(rel, content)
				}
				for rel, content := range sc.AgentFiles {
					if err := os.WriteFile(filepath.Join(e.agent, rel), []byte(content), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			})
			// The recorded traces name Pi's agent directory <tmp>/agent.
			norm := func(s string) string { return strings.ReplaceAll(s, env.agent, "<tmp>/agent") }
			tools := goldenTools(t, filepath.Join("..", "..", "port", "description-golden", sc.Name+".jsonl"))
			var agent map[string]any
			var names []string
			for _, tl := range tools {
				name := tl["name"].(string)
				if name == "Agent" {
					agent = tl
				}
				if name == "Agent" || name == "get_subagent_result" || name == "steer_subagent" || name == "SubagentWorkflow" {
					names = append(names, name)
				}
			}
			eq(t, norm(def.Description), agent["description"])
			got, _ := json.Marshal(def.Parameters)
			want, _ := json.Marshal(agent["parameters"])
			var gm, wm any
			_ = json.Unmarshal([]byte(norm(string(got))), &gm)
			_ = json.Unmarshal(want, &wm)
			eq(t, gm, wm)
			flags := descriptionFlagsNow(settingsView(), settingsView().toolDescriptionMode())
			eq(t, registeredToolNames(flags), names)
		})
	}
}
