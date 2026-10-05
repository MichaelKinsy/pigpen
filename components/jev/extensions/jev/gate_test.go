package jev_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of the original's gate behavior (src/index.ts tool_call, src/gate.ts).
// The original has no tests of its own; each case is derived from a branch of
// the source and named after it. Upstream file:line are in port/PORT.md.

func bash(cmd string) map[string]any { return map[string]any{"command": cmd} }

func TestGate_ShadowFlaggedNotifiesAndNeverBlocks(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(flaggedGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	block, _ := h.toolCall("bash", bash("rm -rf src && git push --force origin main"))
	if block {
		t.Fatal("shadow mode blocked a call")
	}
	want := "warning: jev shadow: bash - destructive 0.99, exfiltration 0.79, beyond_scope 0.98, impact 3.00/3 at confidence 0.91"
	if !h.anyNotification(want) {
		t.Errorf("notifications = %q, want %q", h.notifications(), want)
	}
	if got := h.lastStatus(); got != "jev: destructive 0.99, exfiltration 0.79, beyond_scope 0.98, impact 3.00/3 at confidence 0.91" {
		t.Errorf("status = %q", got)
	}
}

func TestGate_ClearVerdictSetsClearStatus(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	if block, _ := h.toolCall("bash", bash("git status --short")); block {
		t.Fatal("blocked")
	}
	if got := h.lastStatus(); got != "jev: clear (shadow)" {
		t.Errorf("status = %q", got)
	}
	if n := len(h.notifications()); n != 0 {
		t.Errorf("clear verdict notified: %q", h.notifications())
	}
}

func TestGate_SessionStartStatus(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"output": map[string]any{"enabled": false}}))
	h := start(t, e, newHostState(), HostOptions{})
	if got := h.lastStatus(); got != "jev: shadow (out off)" {
		t.Errorf("status = %q, want %q", got, "jev: shadow (out off)")
	}
}

func TestGate_EnforceAsksAndDeclineBlocks(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(gateBody(0.99, 0.04, 0.4, 0.5, 0.9)))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"gate": map[string]any{"mode": "enforce"}}))
	hs := newHostState()
	h := start(t, e, hs, HostOptions{})
	block, reason := h.toolCall("bash", bash("rm -rf src"))
	if !block || reason != "pi-jev: destructive 0.99 (declined)" {
		t.Fatalf("block=%v reason=%q", block, reason)
	}
	c := hs.confirmCalls()
	if len(c) != 1 || c[0]["title"] != "Jev flagged this tool call" || c[0]["message"] != "bash\ndestructive 0.99\n\nRun it anyway?" {
		t.Errorf("confirm = %+v", c)
	}
}

func TestGate_EnforceAcceptedRuns(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(gateBody(0.99, 0.04, 0.4, 0.5, 0.9)))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"gate": map[string]any{"mode": "enforce"}}))
	hs := newHostState()
	hs.confirm = true
	h := start(t, e, hs, HostOptions{})
	if block, _ := h.toolCall("bash", bash("rm -rf src")); block {
		t.Fatal("blocked after the user allowed it")
	}
}

func TestGate_EnforceHeadlessDegradesToWarning(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(gateBody(0.99, 0.04, 0.4, 0.5, 0.9)))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"gate": map[string]any{"mode": "enforce"}}))
	no := false
	hs := newHostState()
	h := start(t, e, hs, HostOptions{Mode: "print", HasUI: &no})
	if block, _ := h.toolCall("bash", bash("rm -rf src")); block {
		t.Fatal("headless enforce blocked without blockWithoutUI")
	}
	if !h.anyNotification("warning: jev: bash - destructive 0.99 (headless: not blocking; set gate.blockWithoutUI to block)") {
		t.Errorf("notifications = %q", h.notifications())
	}
	if len(hs.confirmCalls()) != 0 {
		t.Error("asked for confirmation with no UI")
	}
}

func TestGate_EnforceHeadlessBlocksWithBlockWithoutUI(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(gateBody(0.99, 0.04, 0.4, 0.5, 0.9)))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"gate": map[string]any{"mode": "enforce", "blockWithoutUI": true}}))
	no := false
	h := start(t, e, newHostState(), HostOptions{Mode: "print", HasUI: &no})
	block, reason := h.toolCall("bash", bash("rm -rf src"))
	if !block || reason != "pi-jev: destructive 0.99" {
		t.Fatalf("block=%v reason=%q", block, reason)
	}
}

func TestGate_OnlyJudgesConfiguredTools(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("read", map[string]any{"path": "a.txt"})
	if srv.count() != 0 {
		t.Fatalf("judged read: %d requests", srv.count())
	}
	for _, tool := range []string{"bash", "write", "edit"} {
		h.toolCall(tool, map[string]any{"x": tool})
	}
	if srv.count() != 3 {
		t.Fatalf("requests = %d, want 3 (bash, write, edit)", srv.count())
	}
}

func TestGate_ConfiguredToolsReplaceDefaults(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"gate": map[string]any{"tools": []any{"bash"}}}))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("write", map[string]any{"path": "a", "content": "b"})
	if srv.count() != 0 {
		t.Fatal("judged write with gate.tools = [bash]")
	}
}

func TestGate_RequestShape(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	hs := newHostState()
	hs.user = "please list the files"
	h := start(t, e, hs, HostOptions{})
	h.toolCall("bash", bash("ls"))
	reqs := srv.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d", len(reqs))
	}
	r := reqs[0]
	if r.Auth != "Bearer "+testKey {
		t.Errorf("Authorization = %q", r.Auth)
	}
	if r.Path != "/v1/systemone" {
		t.Errorf("path = %q: the endpoint is the API root and the client adds /v1/systemone", r.Path)
	}
	if r.Body["model"] != "jev-test" {
		t.Errorf("model = %v", r.Body["model"])
	}
	st := stateOf(t, r)
	if st["cwd"] != e.cwd || st["tool"] != "bash" || st["user_request"] != "please list the files" {
		t.Errorf("state = %v", st)
	}
	args, _ := st["arguments"].(map[string]any)
	if args["command"] != "ls" {
		t.Errorf("arguments = %v", st["arguments"])
	}
	if p, _ := st["platform"].(string); p == "" {
		t.Error("platform missing")
	}
	qs, _ := r.Body["questions"].(map[string]any)
	for _, id := range []string{"destructive", "exfiltration", "beyond_scope", "impact"} {
		if _, ok := qs[id]; !ok {
			t.Errorf("question %q missing", id)
		}
	}
	d := qs["destructive"].(map[string]any)
	crit := d["criteria"].(map[string]any)
	if d["type"] != "noul" || d["instructions"] != "Is this action destructive?" ||
		crit["true"] != "Deletes or overwrites data, or rewrites shared history" || crit["false"] != "Nothing is deleted or overwritten" {
		t.Errorf("destructive question = %v", d)
	}
	imp := qs["impact"].(map[string]any)
	lv, _ := imp["criteria"].([]any)
	if imp["type"] != "score" || len(lv) != 4 || lv[3] != "Severe, data loss or a forced overwrite of shared history" {
		t.Errorf("impact question = %v", imp)
	}
	// Question order is part of the wire shape: destructive, exfiltration, beyond_scope, impact.
	order := questionOrder(r.Raw)
	if strings.Join(order, ",") != "destructive,exfiltration,beyond_scope,impact" {
		t.Errorf("question order = %v", order)
	}
}

// questionOrder lists the keys of "questions" in wire order.
func questionOrder(raw string) []string {
	var doc struct {
		Questions json.RawMessage `json:"questions"`
	}
	_ = json.Unmarshal([]byte(raw), &doc)
	dec := json.NewDecoder(strings.NewReader(string(doc.Questions)))
	var keys []string
	dec.Token()
	depth := 0
	for dec.More() || depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch v := tok.(type) {
		case json.Delim:
			if v == '{' || v == '[' {
				depth++
			} else {
				depth--
			}
		case string:
			if depth == 0 {
				keys = append(keys, v)
				var skip json.RawMessage
				_ = dec.Decode(&skip)
			}
		}
	}
	return keys
}

func TestGate_ArgumentsKeepInsertionOrder_GAP(t *testing.T) {
	t.Skip("the Go SDK decodes event data into map[string]any, so the original's argument key order is not available; keys are sent sorted. The judgment does not depend on key order; the equivalence harness compares parsed JSON")
}

func TestGate_LongArgumentsAreElided(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	body := strings.Repeat("a", 900)
	h.toolCall("write", map[string]any{"path": "f.txt", "content": body})
	args := stateOf(t, srv.first(t))["arguments"].(map[string]any)
	if got, want := args["content"], strings.Repeat("a", 400)+"…[500 chars elided]"; got != want {
		t.Errorf("content = %q", got)
	}
	if args["path"] != "f.txt" {
		t.Errorf("path = %v", args["path"])
	}
}

func TestGate_ArgumentCharsCountUTF16Units(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"gate": map[string]any{"argumentChars": 5}}))
	h := start(t, e, newHostState(), HostOptions{})
	// "😀" is two UTF-16 units. "ab😀cd😀" is 8 units: keep 5 = "ab😀c", 3 elided.
	h.toolCall("bash", bash("ab😀cd😀"))
	args := stateOf(t, srv.first(t))["arguments"].(map[string]any)
	if got, want := args["command"], "ab😀c…[3 chars elided]"; got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestGate_CutInsideSurrogatePairStaysValidUTF8(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"gate": map[string]any{"argumentChars": 3}}))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("ab😀cd")) // cut after 3 units splits the pair
	raw := srv.first(t).Raw
	if !strings.Contains(raw, "…[3 chars elided]") {
		t.Errorf("marker missing: %s", raw)
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Errorf("request is not valid JSON: %v", err)
	}
}

// C7: the original stops eliding below depth 4, so a deep string left whole.
func TestCorrection_DeepStringsAreElidedToo(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	secret := strings.Repeat("S", 3000)
	deep := map[string]any{"a": map[string]any{"b": map[string]any{"c": map[string]any{"d": map[string]any{"e": map[string]any{"f": secret}}}}}}
	h.toolCall("bash", deep)
	if strings.Contains(srv.first(t).Raw, strings.Repeat("S", 401)) {
		t.Error("a deeply nested string left the machine whole")
	}
}

// C3: maxStateChars is parsed and documented by the original but never applied.
func TestCorrection_MaxStateCharsCapsTheState(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"maxStateChars": 300}))
	hs := newHostState()
	hs.user = strings.Repeat("u", 1200)
	h := start(t, e, hs, HostOptions{})
	in := map[string]any{}
	for i := 0; i < 20; i++ {
		in[string(rune('a'+i))] = strings.Repeat("x", 100)
	}
	h.toolCall("bash", in)
	st := srv.first(t).Body["state"]
	b, _ := json.Marshal(st)
	if n := len([]rune(string(b))); n > 300 {
		t.Errorf("state is %d chars, cap is 300: %s", n, b)
	}
	if s, ok := st.(map[string]any); !ok || s["tool"] != "bash" || s["cwd"] != e.cwd {
		t.Errorf("the cap must keep identifying fields: %s", b)
	}
}

func TestGate_UserRequestIsTruncatedTo1200Units(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	hs := newHostState()
	hs.user = strings.Repeat("u", 1300)
	h := start(t, e, hs, HostOptions{})
	h.toolCall("bash", bash("ls"))
	if got, want := stateOf(t, srv.first(t))["user_request"], strings.Repeat("u", 1200)+"…[truncated]"; got != want {
		t.Errorf("user_request = %v", got)
	}
}

func TestGate_NoUserRequestOmitsTheField(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("ls"))
	if _, ok := stateOf(t, srv.first(t))["user_request"]; ok {
		t.Error("user_request present with no user message")
	}
}

func TestGate_ImpactBelowMinConfidenceDoesNotFlag(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(gateBody(0.1, 0.1, 0.1, 3.0, 0.4)))
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("x"))
	if h.lastStatus() != "jev: clear (shadow)" {
		t.Errorf("status = %q", h.lastStatus())
	}
}

func TestGate_ThresholdIsInclusive(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(gateBody(0.9, 0, 0, 0, 0.9)))
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("x"))
	if h.lastStatus() != "jev: destructive 0.90" {
		t.Errorf("status = %q", h.lastStatus())
	}
}

// Number semantics: JavaScript toFixed rounds an exact tie up (0.125 -> "0.13").
func TestGate_ToFixedRoundsTiesUpLikeJavaScript(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(gateBody(0.125, 0, 0, 0, 0.9)))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"gate": map[string]any{"blockOn": map[string]any{"destructive": 0.1}}}))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("x"))
	if h.lastStatus() != "jev: destructive 0.13" {
		t.Errorf("status = %q, want the JavaScript rendering 0.13", h.lastStatus())
	}
}

func TestGate_DisabledGateSkips(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"gate": map[string]any{"enabled": false}}))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("x"))
	if srv.count() != 0 {
		t.Fatal("judged with gate.enabled=false")
	}
}

// ---- cache and in-flight sharing --------------------------------------------------

func TestGate_IdenticalCallIsJudgedOncePerWindow(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	hs := newHostState()
	hs.user = "list files"
	h := start(t, e, hs, HostOptions{})
	h.toolCall("bash", bash("ls"))
	h.toolCall("bash", bash("ls"))
	if srv.count() != 1 {
		t.Fatalf("requests = %d, want 1", srv.count())
	}
	h.toolCall("bash", bash("ls -la"))
	if srv.count() != 2 {
		t.Fatalf("different input reused a verdict: %d requests", srv.count())
	}
}

func TestGate_CacheKeyIgnoresPropertyOrder(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	h.Fire("tool_call", map[string]any{"toolName": "write", "input": json.RawMessage(`{"path":"a","content":"b"}`)})
	h.Fire("tool_call", map[string]any{"toolName": "write", "input": json.RawMessage(`{"content":"b","path":"a"}`)})
	if srv.count() != 1 {
		t.Fatalf("requests = %d, want 1", srv.count())
	}
}

// C1: probe from the review. The same arguments after the user changed their
// request reused a verdict that had assessed the old intent.
func TestCorrection_CacheIsBoundToTheUserRequest(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, func(i int, _ recordedRequest) jevReply {
		if i == 0 {
			return jevReply{body: clearGate()}
		}
		return jevReply{body: gateBody(0.1, 0.1, 0.99, 0.5, 0.9)}
	})
	e.writeGlobal(t, jevConfig(srv, nil))
	hs := newHostState()
	hs.user = "delete the build directory"
	h := start(t, e, hs, HostOptions{})
	h.toolCall("bash", bash("rm -rf build"))
	h.pushUser(hs, "just explain what the build directory is")
	h.toolCall("bash", bash("rm -rf build"))
	if srv.count() != 2 {
		t.Fatalf("requests = %d, want 2: the second judgment used a verdict from another request", srv.count())
	}
	if !strings.Contains(h.lastStatus(), "beyond_scope") {
		t.Errorf("status = %q, want the second verdict", h.lastStatus())
	}
}

func TestCorrection_CacheIsBoundToTheModelAndEndpoint(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("ls"))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"model": "jev-other"}))
	h.Fire("session_start", map[string]any{"reason": "reload"})
	h.toolCall("bash", bash("ls"))
	if srv.count() != 2 {
		t.Fatalf("requests = %d: a verdict from another model was reused", srv.count())
	}
}

func TestGate_SiblingCallsShareOneInFlightRequest(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, func(int, recordedRequest) jevReply {
		return jevReply{body: clearGate(), delay: 300 * time.Millisecond}
	})
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); h.toolCall("bash", bash("ls")) }()
	}
	wg.Wait()
	if srv.count() != 1 {
		t.Fatalf("requests = %d, want 1 shared request", srv.count())
	}
}

func TestGate_ErrorsAreNotCached(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, func(i int, _ recordedRequest) jevReply {
		if i == 0 {
			return jevReply{status: 500, body: "boom"}
		}
		return jevReply{body: clearGate()}
	})
	e.writeGlobal(t, jevConfig(srv, nil))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("bash", bash("ls"))
	h.toolCall("bash", bash("ls"))
	if srv.count() != 2 {
		t.Fatalf("requests = %d, want 2", srv.count())
	}
	if h.lastStatus() != "jev: clear (shadow)" {
		t.Errorf("status = %q", h.lastStatus())
	}
}

func TestGate_UserRequestIsTrimmed(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	hs := newHostState()
	hs.user = "  \n list the files \n"
	h := start(t, e, hs, HostOptions{})
	h.toolCall("bash", bash("ls"))
	if got := stateOf(t, srv.first(t))["user_request"]; got != "list the files" {
		t.Errorf("user_request = %q", got)
	}
}

func TestGate_LatestUserMessageWins(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	hs := newHostState()
	hs.user = "first request"
	h := start(t, e, hs, HostOptions{})
	h.toolCall("bash", bash("ls"))
	h.pushUser(hs, "second request")
	h.toolCall("bash", bash("ls -la"))
	reqs := srv.requests()
	if got := stateOf(t, reqs[1])["user_request"]; got != "second request" {
		t.Errorf("user_request = %q", got)
	}
}

// The cap shrinks the long strings before it drops anything.
func TestCorrection_MaxStateCharsKeepsShortenedArguments(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, map[string]any{"maxStateChars": 500}))
	h := start(t, e, newHostState(), HostOptions{})
	h.toolCall("write", map[string]any{"path": "f.txt", "content": strings.Repeat("c", 900)})
	st := stateOf(t, srv.first(t))
	args, ok := st["arguments"].(map[string]any)
	if !ok || args["path"] != "f.txt" {
		t.Fatalf("arguments dropped although shortening was enough: %v", st["arguments"])
	}
	if got := args["content"].(string); !strings.HasPrefix(got, "cccc") || !strings.Contains(got, "chars elided]") || len(got) > 300 {
		t.Errorf("content = %.60q...", got)
	}
}

func TestCorrection_ProjectCannotRaiseTheStateCapOrOutputLimit(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(clearGate()))
	e.writeGlobal(t, jevConfig(srv, nil))
	e.writeProject(t, map[string]any{"maxStateChars": 999999, "output": map[string]any{"outputChars": 999999}})
	h := start(t, e, newHostState(), HostOptions{})
	in := map[string]any{}
	for i := 0; i < 60; i++ {
		in["field"+strconv.Itoa(i)] = strings.Repeat("x", 300)
	}
	h.toolCall("bash", in)
	if n := utf16Units(srv.first(t).Raw); n > 8000+4000 { // the state is capped at 8000; the questions add a fixed amount
		t.Errorf("request is %d units: the project raised maxStateChars", n)
	}
	h.toolResult("bash", bash("y"), text(strings.Repeat("o", 5000)), false)
	reqs := srv.requests()
	out := stateOf(t, reqs[len(reqs)-1])["output"].(string)
	if !strings.HasSuffix(out, "[3000 chars elided]") {
		t.Errorf("project raised output.outputChars: ...%s", out[len(out)-30:])
	}
}

func utf16Units(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}
