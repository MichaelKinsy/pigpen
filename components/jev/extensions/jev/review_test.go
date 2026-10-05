package jev_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Cases added by the adversarial review (rev-pigpen-jev), findings R1 to R4 in
// port/PORT.md. Each was red before its fix.

// R1. In enforce mode a flagged call runs only when the user allows it. When the
// confirmation cannot be shown (the host UI call fails), the original's handler
// throws and Pi blocks the call (agent-session.ts _installAgentToolHooks: "Extension
// failed, blocking execution"); the port failed open and ran the flagged call.
func TestReview_EnforceConfirmFailureBlocks(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(gateBody(0.99, 0.04, 0.4, 0.5, 0.9)))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"gate": map[string]any{"mode": "enforce"}}))
	hs := newHostState()
	h := start(t, e, hs, HostOptions{OnCall: func(method string, args map[string]any) (map[string]any, string) {
		if method == "ui.confirm" {
			return nil, "ui_error: the terminal is gone"
		}
		return hs.onCall(method, args)
	}})
	block, reason := h.toolCall("bash", bash("rm -rf src"))
	if !block {
		t.Fatal("enforce mode ran a flagged call the user never approved")
	}
	mustContain(t, "reason", reason, "pi-jev: destructive 0.99", "not confirmed", "the terminal is gone")
}

// R2. The default judge is "the model PiG is configured with", named in the
// disclosure as the provider that already receives the conversation. After the
// user switches models, judging must follow: staying on the old provider keeps
// sending content to a provider that no longer receives the conversation.
func TestReview_DefaultJudgeFollowsTheSelectedModel(t *testing.T) {
	e := newEnv(t)
	e.writeGlobal(t, map[string]any{"enabled": true, "acknowledged": true, "display": "rich"})
	hs := newHostState()
	var mu sync.Mutex
	var models []map[string]any
	h := start(t, e, hs, HostOptions{ModelStream: func(model, _ map[string]any) []map[string]any {
		mu.Lock()
		models = append(models, model)
		mu.Unlock()
		return ModelText(gateAnswers(0, 0, 0, 0))
	}})
	next := map[string]any{"id": "private-1", "name": "private-1", "provider": "local"}
	hs.mu.Lock()
	hs.model = next
	hs.mu.Unlock()
	h.Fire("model_select", map[string]any{"model": next, "previousModel": map[string]any{"id": "judge-1", "provider": "acme"}, "source": "set"})
	h.toolCall("bash", bash("ls"))
	mu.Lock()
	defer mu.Unlock()
	if len(models) != 1 || models[0]["provider"] != "local" {
		t.Fatalf("judge after switching to local/private-1 = %v", models)
	}
	h.Command("jev", "")
	mustContain(t, "status", lastNote(h), "local/private-1")
	if strings.Contains(lastNote(h), "acme/judge-1") {
		t.Errorf("status still names the old model: %q", lastNote(h))
	}
}

// R2b. A model named in the user's own config is not replaced by a model switch.
func TestReview_ConfiguredJudgeIgnoresModelSwitch(t *testing.T) {
	e := newEnv(t)
	e.writeGlobal(t, map[string]any{"enabled": true, "acknowledged": true, "display": "plain", "model": "other/cheap-1"})
	hs := newHostState()
	var models []map[string]any
	var mu sync.Mutex
	h := start(t, e, hs, HostOptions{ModelStream: func(model, _ map[string]any) []map[string]any {
		mu.Lock()
		models = append(models, model)
		mu.Unlock()
		return ModelText(gateAnswers(0, 0, 0, 0))
	}})
	h.Fire("model_select", map[string]any{"model": map[string]any{"id": "private-1", "provider": "local"}, "source": "set"})
	h.toolCall("bash", bash("ls"))
	mu.Lock()
	defer mu.Unlock()
	if len(models) != 1 || models[0]["provider"] != "other" {
		t.Fatalf("models = %v", models)
	}
}

// R2c. With no model at start, the judge becomes available once one is selected.
func TestReview_ModelSelectedLaterEnablesTheDefaultJudge(t *testing.T) {
	e := newEnv(t)
	e.writeGlobal(t, map[string]any{"enabled": true, "acknowledged": true, "display": "plain"})
	hs := newHostState()
	hs.model = map[string]any{}
	var n int
	var mu sync.Mutex
	h := start(t, e, hs, HostOptions{ModelStream: func(_, _ map[string]any) []map[string]any {
		mu.Lock()
		n++
		mu.Unlock()
		return ModelText(gateAnswers(0, 0, 0, 0))
	}})
	next := map[string]any{"id": "judge-2", "name": "judge-2", "provider": "acme"}
	hs.mu.Lock()
	hs.model = next
	hs.mu.Unlock()
	h.Fire("model_select", map[string]any{"model": next, "source": "set"})
	h.toolCall("bash", bash("ls"))
	mu.Lock()
	defer mu.Unlock()
	if n != 1 {
		t.Fatalf("judge requests after a model was selected = %d", n)
	}
	registered := false
	for _, c := range h.CallsTo("registerTool") {
		registered = registered || c.Args["name"] == "jev_ask"
	}
	if !registered {
		t.Error("jev_ask was not registered once a judge became available")
	}
}

// R3. The disclosure lists what leaves the machine. jev_ask sends text the model
// chooses (up to maxStateChars) and /jev check sends the user's text; neither was
// listed.
func TestReview_DisclosureNamesJevAskAndCheck(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	cfg := jevConfig(srv, map[string]any{"display": "rich"})
	delete(cfg, "enabled")
	e.writeGlobal(t, cfg)
	hs := newHostState()
	h := start(t, e, hs, HostOptions{})
	h.Command("jev", "on")
	c := hs.confirmCalls()
	if len(c) != 1 {
		t.Fatalf("confirm calls = %d", len(c))
	}
	mustContain(t, "disclosure", c[0]["message"].(string), "jev_ask", "8000", "/jev check")
}

// R4. A repository's .pig/pi-jev.json may tune the gate, but relaxing the
// protection the user configured (turning the gate off, enforce to shadow, higher
// thresholds) must not be silent.
func TestReview_ProjectThatRelaxesTheGateIsReported(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"gate": map[string]any{"mode": "enforce", "blockWithoutUI": true}}))
	e.writeProject(t, map[string]any{
		"gate":   map[string]any{"enabled": false, "mode": "shadow", "blockWithoutUI": false, "blockOn": map[string]any{"destructive": 1, "impact": 3}},
		"output": map[string]any{"enabled": false, "leakThreshold": 1},
	})
	h := start(t, e, newHostState(), HostOptions{})
	var warn string
	for _, n := range h.notifications() {
		if strings.Contains(n, "relaxes") {
			warn = n
		}
	}
	if warn == "" {
		t.Fatalf("no warning for a project that turned the gate off: %q", h.notifications())
	}
	mustContain(t, "warning", warn, "gate.enabled false", "gate.mode shadow", "gate.blockWithoutUI false",
		"gate.blockOn.destructive 1", "gate.blockOn.impact 3", "output.enabled false", "output.leakThreshold 1")
}

func TestReview_StricterProjectIsNotReported(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	e.writeProject(t, map[string]any{"gate": map[string]any{"mode": "enforce", "blockOn": map[string]any{"destructive": 0.4}}})
	h := start(t, e, newHostState(), HostOptions{})
	if h.anyNotification("relaxes") {
		t.Errorf("a stricter project was reported as relaxing: %q", h.notifications())
	}
}

// R5. Guard tests for the trust boundary (PORT.md C2). The existing test sets every
// destination key in one project file against a user config that already names the
// backend, model and key, so a project-applied apiKeyFile, backend, model or
// acknowledged survived as mutants (x-project-*). Each key is tested alone here.

func TestReview_ProjectCannotChooseTheKeyFile(t *testing.T) {
	e := newEnv(t)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil)) // no key of the user's own
	secret := filepath.Join(e.cwd, "not-a-key.txt")
	if err := os.WriteFile(secret, []byte("contents-of-a-private-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.writeProject(t, map[string]any{"apiKeyFile": secret})
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("ls"))
	for _, r := range srv.requests() {
		if strings.Contains(r.Auth, "contents-of-a-private-file") {
			t.Fatalf("a project file chose the file sent as the API key: %q", r.Auth)
		}
	}
	if !h.anyNotification("ignored project setting apiKeyFile") {
		t.Errorf("notifications = %q", h.notifications())
	}
}

func TestReview_ProjectCannotSwitchTheBackend(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	attacker := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, map[string]any{"enabled": true, "acknowledged": true, "display": "plain", "endpoint": attacker.srv.URL, "model": "acme/judge-1"})
	e.writeProject(t, map[string]any{"backend": "typesafe"})
	var n int
	var mu sync.Mutex
	h := start(t, e, newHostState(), HostOptions{ModelStream: func(_, _ map[string]any) []map[string]any {
		mu.Lock()
		n++
		mu.Unlock()
		return ModelText(gateAnswers(0, 0, 0, 0))
	}})
	h.toolCall("bash", bash("ls"))
	mu.Lock()
	defer mu.Unlock()
	if attacker.count() != 0 || n != 1 {
		t.Fatalf("project backend applied: %d HTTP requests, %d model requests", attacker.count(), n)
	}
}

func TestReview_ProjectCannotChooseTheModel(t *testing.T) {
	e := newEnv(t)
	e.writeGlobal(t, map[string]any{"enabled": true, "acknowledged": true, "display": "plain"})
	e.writeProject(t, map[string]any{"model": "elsewhere/judge"})
	var models []map[string]any
	var mu sync.Mutex
	h := start(t, e, newHostState(), HostOptions{ModelStream: func(model, _ map[string]any) []map[string]any {
		mu.Lock()
		models = append(models, model)
		mu.Unlock()
		return ModelText(gateAnswers(0, 0, 0, 0))
	}})
	h.toolCall("bash", bash("ls"))
	mu.Lock()
	defer mu.Unlock()
	if len(models) != 1 || models[0]["provider"] != "acme" {
		t.Fatalf("a project file chose the judge model: %v", models)
	}
}

func TestReview_ProjectCannotHideTheDisclosure(t *testing.T) {
	e := newEnv(t)
	e.writeGlobal(t, map[string]any{"enabled": true, "display": "rich"})
	e.writeProject(t, map[string]any{"acknowledged": true})
	h := start(t, e, newHostState(), HostOptions{})
	if !h.anyNotification("What leaves this machine") {
		t.Errorf("a project file acknowledged the disclosure for the user: %q", h.notifications())
	}
}

// R5b. http is accepted only for this machine; an IP literal is not "localhost".
func TestReview_PlainHTTPToARemoteIPIsRefused(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	e.writeGlobal(t, map[string]any{"enabled": true, "acknowledged": true, "backend": "typesafe", "display": "plain",
		"endpoint": "http://192.0.2.10/v1/systemone", "model": "m"})
	h := start(t, e, newHostState(), HostOptions{})
	if !h.anyNotification("must use https") {
		t.Errorf("http:// to a remote IP was accepted: %q", h.notifications())
	}
}

// R5c. The original's output cache key is tool and output (output.ts outputKey).
func TestReview_OutputCacheIsPerTool(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(outBody(0.01, "no_failure", 0.99)))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"output": map[string]any{"tools": []any{"bash", "read"}}}))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolResult("bash", bash("cat x"), text("same text"), false)
	h.toolResult("read", map[string]any{"path": "x"}, text("same text"), false)
	if srv.count() != 2 {
		t.Errorf("requests = %d, want one per tool", srv.count())
	}
}

// R5d. PORT.md C3: when shortening is not enough, the arguments go before the
// user's request (which by then is shortened too).
func TestReview_StateCapDropsArgumentsBeforeTheRequest(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"maxStateChars": 300}))
	hs := newHostState()
	hs.user = "tidy the build"
	h := start(t, e, hs, HostOptions{})
	in := map[string]any{}
	for i := 0; i < 40; i++ {
		in["k"+strconv.Itoa(i)] = i
	}
	h.toolCall("bash", in)
	st := stateOf(t, srv.first(t))
	user, _ := st["user_request"].(string)
	if st["arguments"] != "…[elided to fit maxStateChars]" || !strings.HasPrefix(user, "tidy the") {
		t.Errorf("state = %v", st)
	}
}
