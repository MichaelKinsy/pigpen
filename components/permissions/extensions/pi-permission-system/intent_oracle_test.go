package pi_permission_system

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// testdata/intent-oracle.json is what the original's real PermissionResolver does with intents whose shape its types forbid
// (port/record-intent-probe.sh). The port's AccessIntent is a sealed interface: a gate can only build a tool, access-path or
// bash-command intent, so the original's answers for an unknown kind (it evaluates it as a tool intent) have no Go input. What the
// port can be handed is nil or a type of its own package that the resolver does not know; both panic, where the original answers an
// unknown kind and throws a TypeError for null/undefined. The gate pipeline's recover turns a panic into a block (the fail-closed
// difference recorded in PORT.md); this test pins the panic and checks the representable records against the original's answers.

type stubIntent struct{}

func (stubIntent) accessIntent() {}

func TestIntentOracleDifferential(t *testing.T) {
	var oracle []struct {
		Name   string         `json:"name"`
		Throws string         `json:"throws"`
		Result map[string]any `json:"result"`
	}
	data, err := os.ReadFile("testdata/intent-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	cfg := filepath.Join(base, "config.json")
	if err := os.MkdirAll(filepath.Join(base, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(`{"permission":{"*":"ask","read":"allow","bash":"deny"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewPermissionManager(PermissionManagerOptions{
		PolicyLoaderOptions: PolicyLoaderOptions{GlobalConfigPath: cfg, AgentsDir: filepath.Join(base, "agents"),
			ProjectGlobalConfigPath: filepath.Join(base, "none.json"), ProjectAgentsDir: filepath.Join(base, "none"), McpServerNames: []string{}},
		IsYoloEnabled: func() bool { return false },
	})
	r := NewPermissionResolver(m, &SessionRules{})
	panics := func(f func()) (panicked bool) {
		defer func() { panicked = recover() != nil }()
		f()
		return false
	}

	byName := map[string]int{}
	for i, o := range oracle {
		byName[o.Name] = i
	}
	represented := map[string]AccessIntent{
		"tool read": ToolAccessIntent{Surface: "read", Input: newObject()},
		"tool bash": ToolAccessIntent{Surface: "bash", Input: parseObject(t, `{"command":"ls"}`)},
	}
	for name, intent := range represented {
		want := oracle[byName[name]]
		if want.Throws != "" {
			t.Fatalf("%s: the original threw %s", name, want.Throws)
		}
		got := r.Resolve(intent)
		for _, k := range []string{"toolName", "state", "source"} {
			var g any
			switch k {
			case "toolName":
				g = got.ToolName
			case "state":
				g = got.State
			case "source":
				g = got.Source
			}
			if !reflect.DeepEqual(g, want.Result[k]) {
				t.Errorf("%s: %s port %v original %v", name, k, g, want.Result[k])
			}
		}
	}

	// the original answers an unknown kind as a tool intent: no Go input exists for it, and the port's nearest inputs fail closed
	for _, name := range []string{"unknown kind, surface read", "unknown kind, surface bash", "no kind, surface read", "no kind, surface bash", "kind null"} {
		if oracle[byName[name]].Throws != "" {
			t.Errorf("%s: the original no longer answers it (threw %s); the PORT.md note is stale", name, oracle[byName[name]].Throws)
		}
	}
	if !panics(func() { r.Resolve(stubIntent{}) }) {
		t.Error("an intent type the resolver does not know must panic (the gate turns it into a block), not be answered")
	}
	// the original throws a TypeError for null and undefined; so does the port (a panic), never an answer
	for _, name := range []string{"null", "undefined"} {
		if oracle[byName[name]].Throws != "TypeError" {
			t.Errorf("%s: the original did not throw a TypeError: %+v", name, oracle[byName[name]])
		}
	}
	if !panics(func() { r.Resolve(nil) }) {
		t.Error("a nil intent must panic")
	}
}

func parseObject(t *testing.T, text string) *jsObject {
	v, err := parseJSON([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsObject)
}
