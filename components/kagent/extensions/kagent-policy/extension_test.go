package kagentpolicy_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	kp "github.com/MichaelKinsy/pigpen/kagent-policy"
)

func TestInertWithoutPolicyVariable(t *testing.T) {
	ext, err := kp.New(kp.Options{LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	reg := register(t, ext)
	if len(reg.Tools) != 0 || len(reg.Handlers) != 0 || len(reg.McpServers) != 0 {
		t.Fatalf("an inert extension declared %+v", reg)
	}
}

func TestExtensionReadsTheFileNamedByTheVariable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"ask_user":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ext, err := kp.New(kp.Options{LookupEnv: func(k string) (string, bool) { return path, k == kp.EnvPolicy }})
	if err != nil {
		t.Fatal(err)
	}
	if reg := register(t, ext); !reg.hasTool("ask_user") {
		t.Fatalf("declared %+v", reg)
	}
}

func TestFailsClosed(t *testing.T) {
	missing := func(string) (string, bool) { return "/nonexistent/kagent-policy.json", true }
	cases := []struct {
		name   string
		policy string
		env    func(string) (string, bool)
		want   string
	}{
		{"invalid json", `{"mcp_servers":`, nil, "policy:"},
		{"unknown key", `{"gate":false}`, nil, "unknown field"},
		{"literal header", `{"mcp_servers":[{"name":"a","url":"https://a.example","headers":{"X":"!id"}}]}`, nil, "single ${VAR}"},
		{"unset variable", `{"mcp_servers":[{"name":"a","url":"https://a.example","headers":{"X":"${NOPE}"}}]}`, nil, "NOPE"},
		{"unreadable file", ``, missing, "read the policy file"},
		{"empty variable", ``, func(k string) (string, bool) { return "", true }, "set but empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := tc.env
			if env == nil {
				env = testEnv(headerEnv)
			}
			ext, err := build(tc.policy, env)
			if err == nil || ext != nil {
				t.Fatalf("New accepted the policy: ext=%v err=%v", ext, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
			// The entry point PiG calls refuses to construct: PiG then exits instead of running ungated.
			var panicked any
			func() {
				defer func() { panicked = recover() }()
				opts := kp.Options{LookupEnv: env}
				if tc.name != "unreadable file" { // that case reads the real, missing file
					opts.ReadFile = func(string, int64) ([]byte, error) { return []byte(tc.policy), nil }
				}
				kp.ExtensionWith(opts)
			}()
			text, _ := panicked.(string)
			if !strings.Contains(text, "refusing to start ungated") || !strings.Contains(text, tc.want) {
				t.Fatalf("ExtensionWith recovered %v", panicked)
			}
		})
	}
}

func TestOversizedPolicyFileFailsClosed(t *testing.T) {
	_, err := kp.New(kp.Options{
		LookupEnv: testEnv(nil),
		ReadFile:  func(string, int64) ([]byte, error) { return make([]byte, 1<<20+1), nil },
	})
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v", err)
	}
}

func mcpConfig(t *testing.T, policy string) map[string]any {
	t.Helper()
	reg := register(t, mustBuild(t, policy))
	if len(reg.McpServers) != 1 {
		t.Fatalf("servers = %+v", reg.McpServers)
	}
	return reg.McpServers[0].Config
}

func TestAllowlistIsHiddenWithDirectTools(t *testing.T) {
	reg := register(t, mustBuild(t, probePolicy))
	if len(reg.McpServers) != 1 || reg.McpServers[0].Name != "kagent-tools" {
		t.Fatalf("servers = %+v", reg.McpServers)
	}
	want := map[string]any{
		"type": "http", "url": "https://tools.kagent.svc.cluster.local/mcp", "exposure": "hidden",
		"toolExposure": map[string]any{"kagent_probe": "direct"},
		"headers":      map[string]any{"Authorization": "${KAGENT_PIG_HDR_1}"},
		"timeout":      float64(60),
	}
	if got := reg.McpServers[0].Config; !reflect.DeepEqual(got, want) {
		t.Fatalf("config = %v\nwant     %v", got, want)
	}
}

func TestEmptyAllowlistExposesEverythingDirect(t *testing.T) {
	cfg := mcpConfig(t, `{"mcp_servers":[{"name":"a","url":"https://a.example/mcp"}]}`)
	want := map[string]any{"type": "http", "url": "https://a.example/mcp", "exposure": "direct"}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("config = %v, want %v", cfg, want)
	}
}

func TestAllowlistKeepsEveryName(t *testing.T) {
	cfg := mcpConfig(t, `{"mcp_servers":[{"name":"a","url":"https://a.example","tools":["zeta","alpha","get-thing"]}]}`)
	want := map[string]any{"zeta": "direct", "alpha": "direct", "get-thing": "direct"}
	if !reflect.DeepEqual(cfg["toolExposure"], want) || cfg["exposure"] != "hidden" {
		t.Fatalf("config = %v", cfg)
	}
}

// Header values reach PiG exactly as the policy wrote them: a ${VAR} reference PiG expands from its environment.
// The extension never reads the variable's value into the registration.
func TestHeadersPassThroughUnchanged(t *testing.T) {
	policy := `{"mcp_servers":[{"name":"a","url":"https://a.example","headers":{"Authorization":"${KAGENT_PIG_HDR_1}","X-Two":"${KAGENT_PIG_HDR_2}"}}]}`
	env := testEnv(map[string]string{"KAGENT_PIG_HDR_1": "Bearer secret-1", "KAGENT_PIG_HDR_2": "!echo EVIL"})
	ext, err := build(policy, env)
	if err != nil {
		t.Fatal(err)
	}
	reg := register(t, ext)
	headers, _ := reg.McpServers[0].Config["headers"].(map[string]any)
	want := map[string]any{"Authorization": "${KAGENT_PIG_HDR_1}", "X-Two": "${KAGENT_PIG_HDR_2}"}
	if !reflect.DeepEqual(headers, want) {
		t.Fatalf("headers = %v, want %v", headers, want)
	}
	raw, _ := json.Marshal(reg)
	if strings.Contains(string(raw), "secret-1") || strings.Contains(string(raw), "EVIL") {
		t.Fatalf("the registration carries a header value: %s", raw)
	}
}

func TestSeveralServers(t *testing.T) {
	reg := register(t, mustBuild(t, `{"mcp_servers":[{"name":"one","url":"https://one.example"},{"name":"two","url":"https://two.example","require_approval":true}]}`))
	if len(reg.McpServers) != 2 || reg.McpServers[0].Name != "one" || reg.McpServers[1].Name != "two" {
		t.Fatalf("servers = %+v", reg.McpServers)
	}
}

func TestHandlerAndToolRegistration(t *testing.T) {
	cases := []struct {
		name, policy          string
		wantGate, wantAskTool bool // wantGate: a tool_call handler is registered
	}{
		{"approval and ask_user", probePolicy, true, true},
		{"approval only", `{"mcp_servers":[{"name":"a","url":"https://a.example","require_approval":true}]}`, true, false},
		{"ask_user only (its dialog is in the hook)", `{"ask_user":true}`, true, true},
		{"neither (the default deny is still registered)", `{"mcp_servers":[{"name":"a","url":"https://a.example"}],"ask_user":false}`, true, false},
		{"empty policy", `{}`, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := register(t, mustBuild(t, tc.policy))
			if got := reg.hasHandler("tool_call"); got != tc.wantGate {
				t.Errorf("tool_call handler registered = %v, want %v", got, tc.wantGate)
			}
			if got := reg.hasTool("ask_user"); got != tc.wantAskTool {
				t.Errorf("ask_user registered = %v, want %v", got, tc.wantAskTool)
			}
		})
	}
}
