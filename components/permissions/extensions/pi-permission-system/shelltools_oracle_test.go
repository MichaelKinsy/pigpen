package pi_permission_system

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The end-to-end differential for the shell-tool aliases: testdata/shelltools-oracle.json holds what the original's real ConfigStore (loader,
// schema, project merge, normalize) and PermissionSession made of each config in testdata/shelltools-cases.json (recorded by
// port/record-shelltools-probe.sh), and the shell invocation its resolveShellInvocation decides from those aliases for every tool call. The
// session-level differential feeds the config store a hand-built config; this one goes through the files, so it covers a config the schema
// rejects: GetShellToolAliases drops an entry without a string commandArgument, and that must change nothing the original would have
// decided. Any difference fails.

type shellToolsCase struct {
	ID      string  `json:"id"`
	Global  string  `json:"global"`
	Project *string `json:"project"`
	Trusted bool    `json:"trusted"`
	Tools   []struct {
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"tools"`
}

type quietConfigLogger struct{}

func (quietConfigLogger) Debug(string, *jsObject)  {}
func (quietConfigLogger) Review(string, *jsObject) {}

type noPolicyPaths struct{}

func (noPolicyPaths) GetResolvedPolicyPaths() ResolvedPolicyPaths { return ResolvedPolicyPaths{} }

func TestShellToolAliasesOracleDifferential(t *testing.T) {
	read := func(name string, into any) {
		data, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, into); err != nil {
			t.Fatal(err)
		}
	}
	var cases []shellToolsCase
	var oracle []struct {
		ID          string `json:"id"`
		Aliases     any    `json:"aliases"`
		Issues      []string
		Invocations []any `json:"invocations"`
	}
	read("shelltools-cases.json", &cases)
	read("shelltools-oracle.json", &oracle)
	if len(cases) != len(oracle) {
		t.Fatalf("%d cases, %d records", len(cases), len(oracle))
	}
	tmp := t.TempDir()
	checks, nonNil := 0, 0
	for i, c := range cases {
		want := oracle[i]
		if want.ID != c.ID || len(want.Invocations) != len(c.Tools) {
			t.Fatalf("case %d: corpus %q (%d tools), record %q (%d results)", i, c.ID, len(c.Tools), want.ID, len(want.Invocations))
		}
		agentDir := filepath.Join(tmp, "agent", c.ID)
		cwd := filepath.Join(tmp, "project", c.ID)
		for _, f := range []struct {
			dir  string
			text *string
		}{{agentDir, &c.Global}, {cwd, c.Project}} {
			if f.text == nil {
				continue
			}
			p := filepath.Join(f.dir, "extensions", "pi-permission-system", "config.json")
			if f.dir == cwd {
				p = filepath.Join(cwd, ".pi", "extensions", "pi-permission-system", "config.json")
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(*f.text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		rec := &sessionRecorder{configs: []sessionConfigCase{{}}}
		store := NewConfigStore(ConfigStoreDeps{AgentDir: agentDir, PolicyPaths: noPolicyPaths{}, Logger: quietConfigLogger{}})
		session := NewPermissionSession(ExtensionPaths{}, rec, rec, &SessionRules{}, store, rec, PosixPathFlavor)
		ctx := SessionContext{ForwarderContext: ForwarderContext{Cwd: cwd, SessionManager: sessionManagerFake{entries: json.RawMessage(`[]`)}}}
		session.RefreshConfig(&ctx, c.Trusted)
		aliases := session.GetShellToolAliases()

		var gotAliases any
		if aliases != nil {
			m := map[string]any{}
			for name, a := range aliases {
				e := map[string]any{"commandArgument": a.CommandArgument}
				if a.WorkdirArgument != "" {
					e["workdirArgument"] = a.WorkdirArgument
				}
				m[name] = e
			}
			gotAliases = m
			nonNil++
		}
		if !reflect.DeepEqual(roundTrip(t, gotAliases), want.Aliases) {
			t.Errorf("%s: aliases\n  port     %v\n  original %v", c.ID, gotAliases, want.Aliases)
		}
		checks++
		for j, tc := range c.Tools {
			input, err := parseJSON(tc.Input)
			if err != nil {
				t.Fatal(err)
			}
			var got any
			if inv := ResolveShellInvocation(tc.Name, input, aliases); inv != nil {
				var workdir any
				if inv.Workdir != nil {
					workdir = *inv.Workdir
				}
				got = map[string]any{"command": inv.Command, "workdir": workdir}
			}
			if !reflect.DeepEqual(roundTrip(t, got), want.Invocations[j]) {
				t.Errorf("%s: tool %q input %s\n  port     %v\n  original %v", c.ID, tc.Name, tc.Input, got, want.Invocations[j])
			}
			checks++
		}
	}
	if nonNil == 0 {
		t.Fatal("no case produced aliases: the corpus is not reaching the session")
	}
	t.Logf("%d cases, %d comparisons, %d with aliases, 0 differences", len(cases), checks, nonNil)
}

// roundTrip maps a value through JSON so it compares with a decoded record (numbers, nil, nested maps).
func roundTrip(t *testing.T, v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
