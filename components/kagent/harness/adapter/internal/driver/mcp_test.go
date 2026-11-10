//go:build unix

package driver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/config"
	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/journal"
)

func TestSanitizeToolNameFollowsPiGsRule(t *testing.T) {
	for raw, want := range map[string]string{
		"kagent_probe": "kagent_probe", "k8s-get": "k8s_get", "a.b/c d": "a_b_c_d", "Ünï": "_n_", "": "",
		"😀":                    "__", // one underscore per UTF-16 code unit, as PiG does
		"mcp__kagent-tools__x": "mcp__kagent_tools__x",
	} {
		if got := SanitizeToolName(raw); got != want {
			t.Errorf("SanitizeToolName(%q) = %q, want %q", raw, got, want)
		}
	}
}

// The vectors were computed from PiG's CreateMcpToolName (coding/mcpext/tools.go):
// sha256(server NUL tool), first 8 hex digits, appended after truncating to 55 characters.
func TestPigToolNamesIncludeTheHashedForm(t *testing.T) {
	for _, tc := range []struct {
		server, tool string
		want         []string
	}{
		{"kb", "a-b", []string{"mcp__kb__a_b", "mcp__kb__a_b_461fde27"}},
		{"kagent-tools", strings.Repeat("x", 60), []string{"mcp__kagent_tools__" + strings.Repeat("x", 36) + "_aa9a12c7"}},
	} {
		got := PigToolNames(tc.server, tc.tool)
		if len(got) != len(tc.want) {
			t.Fatalf("PigToolNames(%s, %s) = %v", tc.server, tc.tool, got)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("PigToolNames(%s, %s)[%d] = %q, want %q", tc.server, tc.tool, i, got[i], tc.want[i])
			}
		}
	}
}

func TestRawToolNameUsesTheAllowlistWhenSanitisingWasLossy(t *testing.T) {
	servers := []config.PolicyServer{{Name: "kagent-tools", Tools: []string{"k8s-get", "plain", "a.b", "a-b"}}, {Name: "open"}}
	for _, tc := range []struct{ server, pigName, want string }{
		{"kagent-tools", "mcp__kagent_tools__k8s_get", "k8s-get"},
		{"kagent-tools", "mcp__kagent_tools__plain", "plain"},
		{"kagent-tools", "mcp__kagent_tools__a_b", "mcp__kagent_tools__a_b"[len("mcp__kagent_tools__"):]}, // a.b and a-b collide: undecidable
		{"kagent-tools", "mcp__kagent_tools__unknown", "unknown"},
		{"open", "mcp__open__anything", "anything"},
		{"nope", "mcp__x__y", "mcp__x__y"},
	} {
		if got := RawToolName(servers, tc.server, tc.pigName); got != tc.want {
			t.Errorf("RawToolName(%s, %s) = %q, want %q", tc.server, tc.pigName, got, tc.want)
		}
	}
	// the hash-suffixed name PiG gives a colliding tool still resolves through the allowlist
	hashed := []config.PolicyServer{{Name: "kb", Tools: []string{"a-b"}}}
	if got := RawToolName(hashed, "kb", "mcp__kb__a_b_461fde27"); got != "a-b" {
		t.Errorf("hashed name resolved to %q", got)
	}
}

func TestSplitMCPNamePrefersTheLongestServerAndFoldsSeparators(t *testing.T) {
	servers := []config.PolicyServer{{Name: "kb"}, {Name: "kb-extra"}, {Name: "kagent-tools"}}
	for name, want := range map[string][3]string{
		"mcp__kb__probe":             {"kb", "probe", "true"},
		"mcp__kb_extra__probe":       {"kb-extra", "probe", "true"},
		"mcp__kagent_tools__k8s_get": {"kagent-tools", "k8s_get", "true"},
		"mcp__kb__a__b":              {"kb", "a__b", "true"},
		"mcp__other__probe":          {"", "", "false"},
		"bash":                       {"", "", "false"},
		"mcp__kagent-tools__k8s_get": {"", "", "false"}, // PiG never emits an unsanitised server part
	} {
		server, tool, ok := SplitMCPName(servers, name)
		if server != want[0] || tool != want[1] || (want[2] == "true") != ok {
			t.Errorf("SplitMCPName(%q) = %q %q %v", name, server, tool, ok)
		}
	}
}

func TestMCPCallerResolvesHeaderReferencesAndFailsWithoutExecuting(t *testing.T) {
	f := newFakeMCP(t)
	server := config.PolicyServer{Name: "localhost", URL: f.url(), Headers: map[string]string{"X-A": "${KAGENT_PIG_HDR_1}"}, Tools: []string{"kagent_probe"}}
	caller := mustCaller(t, map[string]string{"KAGENT_PIG_HDR_1": "Bearer abc"}, "")
	result, err := caller.Call(t.Context(), server, "mcp__localhost__kagent_probe", map[string]any{"note": "n"})
	if err != nil || outcomeOf(result).Content[0].Text != "probe:n" {
		t.Fatalf("Call = %+v, %v", result, err)
	}
	if f.header[0].Get("X-A") != "Bearer abc" {
		t.Fatalf("headers = %v", f.header[0])
	}
	for name, server := range map[string]config.PolicyServer{
		"unset variable":   {Name: "s", URL: f.url(), Headers: map[string]string{"X": "${KAGENT_PIG_HDR_9}"}},
		"literal header":   {Name: "s", URL: f.url(), Headers: map[string]string{"X": "literal"}},
		"unreachable host": {Name: "s", URL: "http://127.0.0.1:1/mcp"},
	} {
		_, err := caller.Call(t.Context(), server, "mcp__s__kagent_probe", nil)
		if err == nil {
			t.Fatalf("%s: Call succeeded", name)
		}
		var notExecuted *notExecutedError
		if !asNotExecuted(err, &notExecuted) {
			t.Errorf("%s: err = %T, want a not-executed error", name, err)
		}
	}
	if f.callCount() != 1 {
		t.Fatalf("tools/call count = %d", f.callCount())
	}
}

func asNotExecuted(err error, target **notExecutedError) bool {
	e, ok := err.(*notExecutedError)
	*target = e
	return ok
}

func TestMCPCallerListsToolsToRecoverTheRawName(t *testing.T) {
	f := newFakeMCP(t)
	// no allowlist: the sanitised name equals the raw one, found by listing
	server := config.PolicyServer{Name: "localhost", URL: f.url()}
	result, err := mustCaller(t, nil, "").Call(t.Context(), server, "mcp__localhost__kagent_probe", map[string]any{"note": "listed"})
	if err != nil || outcomeOf(result).Content[0].Text != "probe:listed" {
		t.Fatalf("Call = %+v, %v", result, err)
	}
	// a name the server does not have is a not-executed error
	if _, err := mustCaller(t, nil, "").Call(t.Context(), server, "mcp__localhost__missing_tool", nil); err == nil {
		t.Fatal("Call succeeded for a missing tool")
	} else if _, ok := err.(*notExecutedError); !ok {
		t.Fatalf("err = %T", err)
	}
	if f.callCount() != 1 {
		t.Fatalf("tools/call count = %d", f.callCount())
	}
}

func TestOutcomeFromMCPConvertsContent(t *testing.T) {
	out := outcomeOf(&mcp.CallToolResult{IsError: true, Content: []mcp.Content{
		&mcp.TextContent{Text: "t"},
		&mcp.ImageContent{Data: []byte("img"), MIMEType: "image/png"},
		&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "u", Text: "res"}},
		&mcp.ResourceLink{URI: "file:///x"},
		&mcp.AudioContent{Data: []byte("a"), MIMEType: "audio/wav"},
	}})
	want := []journal.Block{{Type: "text", Text: "t"}, {Type: "image", Data: "aW1n", MIMEType: "image/png"}, {Type: "text", Text: "res"},
		{Type: "text", Text: "[resource link: file:///x]"}, {Type: "text", Text: "[*mcp.AudioContent content omitted]"}}
	if !out.IsError || len(out.Content) != len(want) {
		t.Fatalf("out = %+v", out)
	}
	for i := range want {
		if out.Content[i] != want[i] {
			t.Errorf("block %d = %+v, want %+v", i, out.Content[i], want[i])
		}
	}
	structured := outcomeOf(&mcp.CallToolResult{StructuredContent: map[string]any{"a": 1}})
	if structured.Content[0].Text != `{"a":1}` {
		t.Fatalf("structured = %+v", structured)
	}
	if empty := outcomeOf(&mcp.CallToolResult{}); len(empty.Content) != 1 || empty.Content[0].Type != "text" {
		t.Fatalf("empty = %+v", empty)
	}
}

// TestMCPCallerHonoursTheEgressTrustBundleEnvironment runs the caller in a
// subprocess, because Go reads SSL_CERT_FILE once per process.
func TestMCPCallerHonoursTheEgressTrustBundleEnvironment(t *testing.T) {
	if os.Getenv("KAGENT_TLS_HELPER_URL") != "" {
		server := config.PolicyServer{Name: "tls", URL: os.Getenv("KAGENT_TLS_HELPER_URL"), Tools: []string{"kagent_probe"}}
		result, err := mustCaller(t, nil, "").Call(context.Background(), server, "mcp__tls__kagent_probe", map[string]any{"note": "tls"})
		if err != nil {
			t.Fatalf("TLS call failed: %v", err)
		}
		if outcomeOf(result).Content[0].Text != "probe:tls" {
			t.Fatalf("result = %+v", result)
		}
		return
	}
	f := startFakeMCP(t, true)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw})
	caFile := t.TempDir() + "/trust-bundle.pem"
	os.WriteFile(caFile, ca, 0o600)
	run := func(extra ...string) (string, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMCPCallerHonoursTheEgressTrustBundleEnvironment$")
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "KAGENT_TLS_HELPER_URL=" + f.server.URL + "/mcp"}, extra...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run(); err == nil || !strings.Contains(out, "certificate") {
		t.Fatalf("without the bundle the call must fail on the certificate: %v\n%s", err, out)
	}
	if out, err := run("SSL_CERT_FILE=" + caFile); err != nil {
		t.Fatalf("with SSL_CERT_FILE the call must succeed: %v\n%s", err, out)
	}
	if f.callCount() != 1 {
		t.Fatalf("tools/call count = %d", f.callCount())
	}
}

func TestOutcomeFromMCPBoundsWhatIsInjected(t *testing.T) {
	big := strings.Repeat("x", MaxResultTextBytes+1234)
	out, dropped := OutcomeFromMCP(&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: big}, &mcp.TextContent{Text: "second"}}})
	total := 0
	for _, b := range out.Content[:len(out.Content)-1] {
		total += len(b.Text)
	}
	marker := out.Content[len(out.Content)-1].Text
	if total != MaxResultTextBytes || dropped != 1234+len("second") || !strings.HasPrefix(marker, "[truncated: ") || !strings.Contains(marker, "1240 bytes") {
		t.Fatalf("kept %d dropped %d marker %q", total, dropped, marker)
	}
	huge := make([]byte, MaxResultImageBytes)
	img, droppedImg := OutcomeFromMCP(&mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: huge, MIMEType: "image/png"}, &mcp.TextContent{Text: "caption"}}})
	for _, b := range img.Content {
		if b.Type == "image" {
			t.Fatal("an oversized image was injected")
		}
	}
	if droppedImg == 0 || img.Content[0].Text != "caption" {
		t.Fatalf("img = %+v dropped %d", img, droppedImg)
	}
	small, d := OutcomeFromMCP(&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}})
	if d != 0 || len(small.Content) != 1 {
		t.Fatalf("a small result was altered: %+v", small)
	}
	// multi-byte text is cut on a character boundary
	cut, _ := OutcomeFromMCP(&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("é", MaxResultTextBytes)}}})
	if !utf8.ValidString(cut.Content[0].Text) {
		t.Fatal("truncation split a character")
	}
}

// The adapter's own environment names the egress bundle only, and Go reads SSL_CERT_FILE as the whole root set.
// The caller therefore gets the combined bundle as RootCAs; without it (negative control) the call fails.
func TestMCPCallerTrustsTheCombinedBundle(t *testing.T) {
	f := startFakeMCP(t, true)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw})
	system := t.TempDir() + "/system.pem"
	egress := t.TempDir() + "/egress.pem"
	os.WriteFile(system, ca, 0o600)             // stands for the system roots (here: the one CA that signed the server)
	os.WriteFile(egress, unrelatedCA(t), 0o600) // stands for the egress MITM CA, which did not sign this server
	combined, err := CombineCABundle([]string{system}, egress, t.TempDir()+"/combined.pem")
	if err != nil || combined == "" {
		t.Fatalf("combined = %q, %v", combined, err)
	}
	server := config.PolicyServer{Name: "tls", URL: f.server.URL + "/mcp", Tools: []string{"kagent_probe"}}
	if _, err := mustCaller(t, nil, "").Call(t.Context(), server, "mcp__tls__kagent_probe", map[string]any{"note": "x"}); err == nil {
		t.Fatal("negative control: the default roots must not trust the test server")
	}
	if _, err := mustCaller(t, nil, egress).Call(t.Context(), server, "mcp__tls__kagent_probe", map[string]any{"note": "x"}); err == nil {
		t.Fatal("negative control: the egress-only bundle must not trust a server it did not sign")
	}
	result, err := mustCaller(t, nil, combined).Call(t.Context(), server, "mcp__tls__kagent_probe", map[string]any{"note": "tls"})
	if err != nil || outcomeOf(result).Content[0].Text != "probe:tls" {
		t.Fatalf("with the combined bundle = %+v, %v", result, err)
	}
	if _, err := NewMCPCaller(nil, t.TempDir()+"/missing.pem"); err == nil {
		t.Fatal("a missing bundle was accepted")
	}
	empty := t.TempDir() + "/empty.pem"
	os.WriteFile(empty, []byte("not pem"), 0o600)
	if _, err := NewMCPCaller(nil, empty); err == nil {
		t.Fatal("a bundle without certificates was accepted")
	}
}

// unrelatedCA is a fresh self-signed CA that signed nothing in these tests (httptest servers all share one certificate).
func unrelatedCA(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "egress-mitm.test"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// A binding with no allowlist leaves the tool list to the first use, as kagent's Go ADK does: the caller lists the
// server's tools when the approved call arrives and finds the raw name PiG's sanitising folded.
func TestMCPCallerDiscoversTheRawToolNameOnFirstUseWithoutAnAllowlist(t *testing.T) {
	f := newFakeMCP(t)
	server := config.PolicyServer{Name: "localhost", URL: f.url()}
	caller := mustCaller(t, nil, "")
	result, err := caller.Call(t.Context(), server, "mcp__localhost__kagent_probe", map[string]any{"note": "first-use"})
	if err != nil || outcomeOf(result).Content[0].Text != "probe:first-use" {
		t.Fatalf("Call = %+v, %v", result, err)
	}
	_, err = caller.Call(t.Context(), server, "mcp__localhost__not_a_tool", nil)
	var notExecuted *notExecutedError
	if err == nil || !asNotExecuted(err, &notExecuted) {
		t.Fatalf("a tool the server does not list: err = %v, want a not-executed error", err)
	}
	if f.callCount() != 1 {
		t.Fatalf("tools/call count = %d", f.callCount())
	}
}
