package kagentpolicy_test

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	kp "github.com/MichaelKinsy/pigpen/kagent-policy"
)

// A policy path the fake ReadFile answers; the file never touches the disk in unit tests.
const policyPath = "/policy/kagent.json"

// testEnv is the environment of a test: KAGENT_PIG_POLICY plus the variables the policy's headers name.
func testEnv(extra map[string]string) func(string) (string, bool) {
	m := map[string]string{kp.EnvPolicy: policyPath}
	for k, v := range extra {
		m[k] = v
	}
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

var headerEnv = map[string]string{"KAGENT_PIG_HDR_1": "Bearer placeholder"}

// build constructs the extension over a policy text, as PiG does when it loads the Binary.
func build(policy string, env func(string) (string, bool)) (*sdk.Extension, error) {
	if env == nil {
		env = testEnv(headerEnv)
	}
	return kp.New(kp.Options{
		LookupEnv: env,
		ReadFile: func(path string, _ int64) ([]byte, error) {
			if path != policyPath {
				return nil, os.ErrNotExist
			}
			return []byte(policy), nil
		},
	})
}

func mustBuild(t *testing.T, policy string) *sdk.Extension {
	t.Helper()
	ext, err := build(policy, nil)
	if err != nil {
		t.Fatalf("policy %s: %v", policy, err)
	}
	return ext
}

// registration is what the extension declares to the host when it loads.
type registration struct {
	Tools []struct {
		Name string `json:"name"`
	} `json:"tools"`
	Handlers []struct {
		Event string `json:"event"`
	} `json:"handlers"`
	McpServers []struct {
		Name   string         `json:"name"`
		Config map[string]any `json:"config"`
	} `json:"mcp_servers"`
}

func (r registration) hasHandler(event string) bool {
	for _, h := range r.Handlers {
		if h.Event == event {
			return true
		}
	}
	return false
}

func (r registration) hasTool(name string) bool {
	for _, tl := range r.Tools {
		if tl.Name == name {
			return true
		}
	}
	return false
}

// register runs the extension against a pipe and returns its register frame, which is how the host learns of the
// tools, handlers and MCP servers a factory declared.
func register(t *testing.T, ext *sdk.Extension) registration {
	t.Helper()
	host, extSide := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ext.RunWithConn(extSide) }()
	defer func() {
		_ = host.Close()
		<-done
	}()
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(host, hdr[:]); err != nil {
			t.Fatalf("read the register frame: %v", err)
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(host, data); err != nil {
			t.Fatalf("read the register frame: %v", err)
		}
		var f struct {
			Type     string       `json:"type"`
			Register registration `json:"register"`
		}
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatalf("decode frame %s: %v", data, err)
		}
		if f.Type == "register" {
			return f.Register
		}
	}
}

// confirmAnswer makes the fake host answer every ui.confirm with the given `confirmed` value and records the calls.
func confirmAnswer(value any) func(string, map[string]any) (map[string]any, string) {
	return func(method string, _ map[string]any) (map[string]any, string) {
		if method == "ui.confirm" {
			return map[string]any{"confirmed": value}, ""
		}
		return nil, ""
	}
}

const probePolicy = `{"mcp_servers":[{"name":"kagent-tools","url":"https://tools.kagent.svc.cluster.local/mcp",` +
	`"headers":{"Authorization":"${KAGENT_PIG_HDR_1}"},"timeout_seconds":60,"tools":["kagent_probe"],"require_approval":true}],"ask_user":true}`
