//go:build unix

package driver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/config"
	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/journal"
	"github.com/kagent-dev/kagent/go/harness/runtime"
)

// The end-to-end test drives a real Piglet Binary (pig with the kagent policy
// extension fused in) against the hermetic mock model server and its MCP server.
// It is opt-in:
//
//	KAGENT_PIG_E2E_BIN   path to the fused Piglet Binary
//	KAGENT_PIG_E2E_MOCK  base URL of the mock model server, e.g. http://127.0.0.1:28090
//	KAGENT_PIG_E2E_MOCK_MCP  optional MCP URL; default is the model URL with port+1 and path /mcp
func e2eSetup(t *testing.T, requireApproval bool) (*harness, string) {
	t.Helper()
	bin, mock := os.Getenv("KAGENT_PIG_E2E_BIN"), os.Getenv("KAGENT_PIG_E2E_MOCK")
	if bin == "" || mock == "" {
		t.Skip("set KAGENT_PIG_E2E_BIN (fused Piglet Binary) and KAGENT_PIG_E2E_MOCK (mock model base URL) to run the real-pig scenarios")
	}
	mcpURL := os.Getenv("KAGENT_PIG_E2E_MOCK_MCP")
	if mcpURL == "" {
		parsed, err := url.Parse(mock)
		if err != nil {
			t.Fatal(err)
		}
		port, _ := strconv.Atoi(parsed.Port())
		mcpURL = fmt.Sprintf("%s://%s:%d/mcp", parsed.Scheme, parsed.Hostname(), port+1)
	}
	raw := fmt.Sprintf(`{"agent":{"name":"e2e","instruction":"You are a test agent.","model":{"model":"mock-model","openai":{"base_url":%q}},
	"http_tools":[{"params":{"url":%q},"tools":["kagent_probe"],"require_approval":%v}]}}`, strings.TrimRight(mock, "/")+"/v1", mcpURL, requireApproval)
	compiled, err := config.Parse([]byte(raw), func(name string) (string, bool) {
		if name == "OPENAI_API_KEY" {
			return config.CredentialPlaceholder, true
		}
		return "", false
	}, config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	d, err := New(Config{
		Executable: bin, Workspace: filepath.Join(root, "data", "workspace"), SessionDir: filepath.Join(root, "data", "pig", "sessions"),
		PigHome: filepath.Join(root, "run", "pig"), AdapterDir: filepath.Join(root, "data", "adapter"), Compiled: compiled,
		Environment: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root}, Tools: mustCaller(t, HeaderValues(compiled), ""), Grace: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	version, err := d.Validate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("pig version: %s", version)
	return &harness{t: t, d: d, cfg: compiled}, compiled.Policy.MCPServers[0].Name
}

func TestE2EBasketScenario(t *testing.T) {
	h, server := e2eSetup(t, false)
	sink := &recorder{}
	out, err := h.d.Run(t.Context(), runtime.Turn{TaskID: "t", ContextID: "c", Prompt: "run the [basket] scenario"}, sink)
	if err != nil || out.Failure != nil || out.Pending != nil {
		t.Fatalf("Run = %+v, %v\n%q", out, err, sink.events)
	}
	if !strings.HasPrefix(sink.text(), "BASKET-DONE ") {
		t.Fatalf("text = %q", sink.text())
	}
	names := []string{}
	for _, c := range sink.calls {
		names = append(names, c.Name)
	}
	if len(sink.calls) != 2 || names[0] != "bash" || names[1] != "mcp__"+SanitizeToolName(server)+"__kagent_probe" || sink.calls[1].MCPTool != "kagent_probe" {
		t.Fatalf("tool calls = %v", names)
	}
	if len(sink.models) != 3 || sink.models[0].InputTokens == 0 {
		t.Fatalf("model calls = %+v", sink.models)
	}
}

func TestE2EApproveScenario(t *testing.T) {
	h, server := e2eSetup(t, true)
	sink := &recorder{}
	out, err := h.d.Run(t.Context(), runtime.Turn{TaskID: "t", ContextID: "c", Prompt: "run the [approve] scenario"}, sink)
	if err != nil || out.Pending == nil {
		t.Fatalf("Run = %+v, %v\n%q", out, err, sink.events)
	}
	request := h.approvalRequest(out)
	if request.Name != "mcp__"+SanitizeToolName(server)+"__kagent_probe" || request.Args["note"] != "basket" {
		t.Fatalf("request = %+v", request)
	}
	resumed := &recorder{}
	final, err := out.Pending.Resume(t.Context(), approve(request), resumed)
	if err != nil || final.Failure != nil || final.Pending != nil {
		t.Fatalf("Resume = %+v, %v\n%q", final, err, resumed.events)
	}
	// The mock keeps the [approve] plan after the continuation nudge and answers with the sha256 of the tool
	// results it saw, here the single probe result.
	sum := sha256.Sum256([]byte("probe:basket"))
	if want := "APPROVE-DONE " + hex.EncodeToString(sum[:]); resumed.text() != want {
		t.Fatalf("final text = %q, want %q", resumed.text(), want)
	}
	if len(resumed.results) != 1 || resumed.results[0].ID != request.CallID || resumed.results[0].IsError || len(resumed.models) != 1 {
		t.Fatalf("text = %q results = %+v models = %d", resumed.text(), resumed.results, len(resumed.models))
	}
	if content, _ := resumed.results[0].Result.([]journal.Block); len(content) == 0 || content[0].Text != "probe:basket" {
		t.Fatalf("injected result = %#v", resumed.results[0].Result)
	}
}
