package jev_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Opt-in, credential destination trust and disclosure. Every case here is a
// deliberate correction or requirement, not upstream parity: the original judged
// as soon as a key existed and let a project file choose the endpoint.

func TestOptIn_NothingIsJudgedByDefault(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey) // a key alone is not consent
	srv := newFakeJev(t, always(flaggedGate()))
	e.writeGlobal(t, map[string]any{"backend": "typesafe", "endpoint": srv.srv.URL, "model": "m"})
	hs := newHostState()
	h := start(t, e, hs, HostOptions{})
	h.toolCall("bash", bash("rm -rf /"))
	h.toolResult("bash", bash("x"), text("secret"), false)
	if srv.count() != 0 || len(hs.completeCalls()) != 0 {
		t.Fatalf("judged without opt-in: %d http, %d model requests", srv.count(), len(hs.completeCalls()))
	}
	if len(h.notifications()) != 0 {
		t.Errorf("off by default must be silent: %q", h.notifications())
	}
	for _, s := range h.statuses() {
		if s != "" {
			t.Errorf("status set while off: %q", s)
		}
	}
}

func TestOptIn_NoConfigAtAllIsInert(t *testing.T) {
	e := newEnv(t)
	hs := newHostState()
	h := start(t, e, hs, HostOptions{})
	h.toolCall("bash", bash("x"))
	h.toolResult("bash", bash("x"), text("y"), false)
	if len(hs.completeCalls()) != 0 || len(h.CallsTo("ui.notify")) != 0 {
		t.Errorf("calls = %+v", h.Calls())
	}
}

func TestOptIn_ProjectFileCannotEnable(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(flaggedGate()))
	e.writeGlobal(t, map[string]any{"backend": "typesafe", "endpoint": srv.srv.URL, "model": "m"})
	e.writeProject(t, map[string]any{"enabled": true, "acknowledged": true})
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("x"))
	if srv.count() != 0 {
		t.Fatal("a repository file opted the user in to sending content off the machine")
	}
	if !h.anyNotification("ignored project setting") || !h.anyNotification("enabled") {
		t.Errorf("no warning for the ignored key: %q", h.notifications())
	}
}

func TestOptIn_GlobalConfigEnables(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("x"))
	if srv.count() != 1 {
		t.Fatalf("requests = %d", srv.count())
	}
}

func TestDisclosure_ShownAtStartUntilAcknowledged(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"acknowledged": false, "display": "rich"}))
	h := start(t, e, newHostState(), HostOptions{})
	var note string
	for _, n := range h.notifications() {
		if strings.Contains(n, "leave") {
			note = n
		}
	}
	if note == "" {
		t.Fatalf("no disclosure: %q", h.notifications())
	}
	mustContain(t, "disclosure", note,
		"working directory", "tool name", "last message", "tool arguments", "bash output",
		srv.srv.URL, "fail", "acknowledged")
}

func TestDisclosure_AcknowledgedIsSilent(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	for _, n := range h.notifications() {
		if strings.Contains(n, "leave") {
			t.Errorf("disclosure shown although acknowledged: %q", n)
		}
	}
}

func TestDisclosure_ModelBackendNamesTheSessionModel(t *testing.T) {
	e := newEnv(t)
	e.writeGlobal(t, map[string]any{"enabled": true, "display": "rich"})
	h := start(t, e, newHostState(), HostOptions{})
	if !h.anyNotification("acme/judge-1") {
		t.Errorf("disclosure does not name the model that receives the content: %q", h.notifications())
	}
}

func TestDisclosure_ModelBackendDoesNotMentionTypeSafe(t *testing.T) {
	e := newEnv(t)
	e.writeGlobal(t, map[string]any{"enabled": true, "display": "rich"})
	h := start(t, e, newHostState(), HostOptions{})
	for _, n := range h.notifications() {
		if strings.Contains(strings.ToLower(n), "typesafe") {
			t.Errorf("model backend disclosure names another provider: %q", n)
		}
	}
}

// C2: the review probe. A project file named its own endpoint and the
// environment key travelled to it.
func TestCorrection_ProjectFileCannotRedirectTheEndpoint(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	trusted := newFakeJev(t, always(clearGate()))
	attacker := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(trusted, nil))
	e.writeProject(t, map[string]any{"endpoint": attacker.srv.URL, "apiKey": "stolen", "apiKeyFile": "/etc/hostname", "model": "x", "backend": "typesafe"})
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("ls"))
	if attacker.count() != 0 {
		t.Fatalf("the project's endpoint received %d requests (key %q)", attacker.count(), attacker.first(t).Auth)
	}
	if trusted.count() != 1 {
		t.Fatalf("trusted endpoint requests = %d", trusted.count())
	}
	if !h.anyNotification("ignored project setting") {
		t.Errorf("no warning: %q", h.notifications())
	}
}

func TestCorrection_NoDefaultEndpointForTheHTTPBackend(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	e.writeGlobal(t, map[string]any{"enabled": true, "acknowledged": true, "backend": "typesafe", "display": "plain"})
	h := start(t, e, newHostState(), HostOptions{})
	block, _ := h.toolCall("bash", bash("ls"))
	if block {
		t.Fatal("blocked")
	}
	if !h.anyNotification("endpoint") {
		t.Errorf("no explanation for the missing endpoint: %q", h.notifications())
	}
}

func TestCorrection_PlainHTTPToARemoteHostIsRefused(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	e.writeGlobal(t, map[string]any{"enabled": true, "acknowledged": true, "backend": "typesafe", "display": "plain",
		"endpoint": "http://jev.example.invalid/v1/systemone", "model": "m"})
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("ls"))
	if !h.anyNotification("https") {
		t.Errorf("an http:// endpoint on a remote host must be refused: %q", h.notifications())
	}
}

func TestCorrection_ProjectCanOnlyNarrowWhatLeaves(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	e.writeProject(t, map[string]any{
		"maxStateChars": 999999,
		"gate":          map[string]any{"argumentChars": 100000, "tools": []any{"bash", "write", "edit", "read"}},
		"output":        map[string]any{"outputChars": 100000, "tools": []any{"bash", "read"}},
	})
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("read", map[string]any{"path": "id_rsa"})
	if srv.count() != 0 {
		t.Fatal("project file widened gate.tools")
	}
	h.toolResult("read", map[string]any{"path": "x"}, text("s"), false)
	if srv.count() != 0 {
		t.Fatal("project file widened output.tools")
	}
	h.toolCall("write", map[string]any{"content": strings.Repeat("k", 5000)})
	args := stateOf(t, srv.first(t))["arguments"].(map[string]any)
	if got := args["content"].(string); !strings.HasSuffix(got, "[4600 chars elided]") {
		t.Errorf("project raised argumentChars: %.40s...", got)
	}
}

func TestProject_CanNarrowAndTune(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(gateBody(0.5, 0.1, 0.1, 0.5, 0.9)))
	e.writeGlobal(t, jevConfig(srv, nil))
	e.writeProject(t, map[string]any{"gate": map[string]any{"tools": []any{"bash"}, "argumentChars": 10, "blockOn": map[string]any{"destructive": 0.4}}})
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("write", map[string]any{"content": "x"})
	if srv.count() != 0 {
		t.Fatal("gate.tools narrowing ignored")
	}
	h.toolCall("bash", bash("0123456789abcdef"))
	if !strings.HasSuffix(stateOf(t, srv.first(t))["arguments"].(map[string]any)["command"].(string), "[6 chars elided]") {
		t.Error("argumentChars narrowing ignored")
	}
	if h.lastStatus() != "jev: destructive 0.50" {
		t.Errorf("project blockOn ignored: %q", h.lastStatus())
	}
}

func TestKey_EnvBeatsConfigBeatsFile(t *testing.T) {
	e := newEnv(t)
	keyfile := filepath.Join(e.agent, "key.txt")
	if err := os.WriteFile(keyfile, []byte("file-key-000000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newFakeJev(t, always(clearGate()))
	try := func(env string, inline string, file string) string {
		t.Setenv("TYPESAFE_API_KEY", env)
		extra := map[string]any{}
		if inline != "" {
			extra["apiKey"] = inline
		}
		if file != "" {
			extra["apiKeyFile"] = file
		}
		e.writeGlobal(t, jevConfig(srv, extra))
		h := start(t, e, newHostState(), HostOptions{})
		before := srv.count()
		h.toolCall("bash", bash(env+inline+file))
		reqs := srv.requests()
		if len(reqs) == before {
			return "<no request>"
		}
		return reqs[len(reqs)-1].Auth
	}
	if got := try("env-key-000000", "inline-key-0000", keyfile); got != "Bearer env-key-000000" {
		t.Errorf("env: %q", got)
	}
	if got := try("", "inline-key-0000", keyfile); got != "Bearer inline-key-0000" {
		t.Errorf("inline: %q", got)
	}
	if got := try("", "", keyfile); got != "Bearer file-key-000000" {
		t.Errorf("file: %q", got)
	}
}

func TestKey_HomeIsExpandedInKeyFile(t *testing.T) {
	e := newEnv(t)
	home := os.Getenv("HOME")
	if err := os.WriteFile(filepath.Join(home, "k.txt"), []byte("home-key-00000"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"apiKeyFile": "~/k.txt"}))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("x"))
	if srv.count() != 1 || srv.first(t).Auth != "Bearer home-key-00000" {
		t.Errorf("requests = %+v", srv.requests())
	}
}

func TestKey_MissingKeyWarnsOnceAndStaysInactive(t *testing.T) {
	e := newEnv(t)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	h.Fire("session_start", map[string]any{"reason": "reload"})
	n := 0
	for _, note := range h.notifications() {
		if strings.Contains(note, "no key. Set TYPESAFE_API_KEY or apiKeyFile in pi-jev.json; the gate is inactive until then.") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("missing-key warnings = %d, want 1: %q", n, h.notifications())
	}
	h.toolCall("bash", bash("x"))
	if srv.count() != 0 {
		t.Error("judged without a key")
	}
}

func TestConfig_UnreadableAndInvalidFilesWarn(t *testing.T) {
	e := newEnv(t)
	if err := os.WriteFile(filepath.Join(e.agent, "pi-jev.json"), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := start(t, e, newHostState(), HostOptions{})
	if !h.anyNotification("invalid JSON") {
		t.Errorf("notifications = %q", h.notifications())
	}
}

func TestConfig_WarningsRedactTheKey(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"apiKeyFile": "/nonexistent/" + testKey}))
	h := start(t, e, newHostState(), HostOptions{})
	for _, n := range h.notifications() {
		if strings.Contains(n, testKey) {
			t.Errorf("key in warning: %q", n)
		}
	}
}

func TestConfig_InvalidValuesFallBackToDefaults(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(gateBody(0.95, 0, 0, 0, 0.9)))
	e.writeGlobal(t, jevConfig(srv, map[string]any{
		"gate": map[string]any{"mode": "loud", "argumentChars": -4, "cacheSeconds": 1.5, "blockOn": map[string]any{"destructive": 7, "impact": -1}},
	}))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("x"))
	// mode stays shadow, destructive threshold stays 0.9.
	if got := h.lastStatus(); got != "jev: destructive 0.95" {
		t.Errorf("status = %q", got)
	}
	if !h.anyNotification("jev shadow: bash - destructive 0.95") {
		t.Errorf("notifications = %q", h.notifications())
	}
}
