package warden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Layer-1 tests through the fake host: the events PiG sends, the calls the extension makes back, with a fake
// TypeSafe server behind the judge. Nothing here reaches a real TypeSafe or reads a real credential.

type sent struct {
	Type, Content string
	Display       bool
	Options       map[string]any
}

type harness struct {
	t      *testing.T
	host   *Host
	ts     *fakeTypeSafe
	home   string
	mu     sync.Mutex
	branch []map[string]any
	// confirm and pick answer the dialogs.
	confirm bool
	pick    string
	model   map[string]any
	// registry answers the model calls the session model receives (own-model backend).
	registry *fakeRegistry
	// subscribed is set once the extension has asked for the session log.
	subscribed bool
	fail       map[string]string
}

type harnessOpts struct {
	cfg      *Config // written to the config file before the session starts; nil: no file
	noUI     bool
	env      map[string]string
	newJudge func(Config, *Budget) (Judge, error)
	git      GitRunner
	// fail makes host calls of these methods answer with this error message.
	fail map[string]string
}

func startHarness(t *testing.T, o harnessOpts) *harness {
	t.Helper()
	h := &harness{t: t, ts: newFakeTypeSafe(t), home: t.TempDir(), confirm: true, model: map[string]any{"id": "mod", "provider": "prov", "name": "Mod"}, fail: o.fail}
	t.Setenv("PIG_HOME", h.home)
	if o.cfg != nil {
		if err := SaveConfig(ConfigPath(h.home), *o.cfg); err != nil {
			t.Fatal(err)
		}
	}
	getenv := func(k string) string {
		if v, ok := o.env[k]; ok {
			return v
		}
		switch k {
		case "TYPESAFE_API_KEY":
			return "test-key-not-real"
		case "TYPESAFE_BASE_URL":
			return h.ts.URL
		}
		return ""
	}
	opts := Options{Getenv: getenv, Git: o.git}
	if o.newJudge != nil {
		opts.NewJudge = func(_ sdk.Context, cfg Config, b *Budget, _ func(string) string) (Judge, error) {
			return o.newJudge(cfg, b)
		}
	}
	hasUI := !o.noUI
	h.host = StartHost(t, New(opts), HostOptions{HasUI: &hasUI, Cwd: t.TempDir(), OnCall: h.onCall})
	return h
}

func (h *harness) onCall(method string, args map[string]any) (map[string]any, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if msg, ok := h.fail[method]; ok {
		return nil, msg
	}
	switch method {
	case "watchSessionLog":
		h.subscribed = true
		// The host pages the log by cursor: entries after the cursor, the total, and nothing more to say once the
		// mirror has caught up (a live host would hold that last poll until the session grows).
		cursor := 0
		if c, ok := args["cursor"].(float64); ok {
			cursor = min(int(c), len(h.branch))
		}
		return map[string]any{"entries": h.branch[cursor:], "entryCount": len(h.branch), "hasMore": false, "leafId": h.leaf()}, ""
	case "ui.confirm":
		return map[string]any{"confirmed": h.confirm}, ""
	case "ui.select":
		return map[string]any{"selected": h.pick, "ok": h.pick != ""}, ""
	case "getModelInfo":
		return h.model, ""
	case "getModel":
		return h.model, ""
	case "getModelAuth":
		return map[string]any{"ok": true, "apiKey": "session-key-not-real"}, ""
	case "modelStream":
		// The host streams the model's events as notifications, then answers the call.
		id, _ := args["streamId"].(string)
		req, _ := args["request"].(map[string]any)
		model, _ := args["model"].(map[string]any)
		h.mu.Unlock()
		msg := h.registry.Complete(model, req, nil)
		h.host.Notify("model_stream_event", map[string]any{"streamId": id, "started": true})
		h.host.Notify("model_stream_event", map[string]any{"streamId": id, "event": map[string]any{"type": "done", "reason": "stop", "message": msg}})
		h.mu.Lock()
		return map[string]any{}, ""
	}
	return nil, ""
}

func (h *harness) leaf() string {
	if len(h.branch) == 0 {
		return ""
	}
	return h.branch[len(h.branch)-1]["id"].(string)
}

func (h *harness) say(role, text string) {
	h.append(map[string]any{"role": role, "content": text})
}

// append adds a message to the session and, once the extension is reading the log, pushes it the way the host
// does (a state_update with the appended entries).
func (h *harness) append(message map[string]any) {
	h.mu.Lock()
	entry := map[string]any{"id": "e" + itoaInt(len(h.branch)), "type": "message", "message": message}
	if n := len(h.branch); n > 0 {
		entry["parentId"] = h.branch[n-1]["id"]
	}
	h.branch = append(h.branch, entry)
	n, live := len(h.branch), h.subscribed
	h.mu.Unlock()
	if live {
		h.host.Notify("state_update", map[string]any{"state": map[string]any{"session": map[string]any{"leafId": entry["id"], "entriesAppended": []any{entry}, "entryCount": n}}})
		time.Sleep(20 * time.Millisecond) // the extension applies notifications on its own goroutine
	}
}

// assistantCalls adds an assistant message with words and tool calls (the SDK reads `arguments` as a string).
func (h *harness) assistantCalls(text string, calls ...map[string]string) {
	blocks := []any{}
	if text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	for _, c := range calls {
		blocks = append(blocks, map[string]any{"type": "toolCall", "id": c["id"], "name": c["name"], "arguments": c["args"]})
	}
	h.append(map[string]any{"role": "assistant", "content": blocks})
}

func (h *harness) start() { h.host.Fire("session_start", map[string]any{"reason": "startup"}) }

func (h *harness) toolCall(tool, id string, input map[string]any) map[string]any {
	h.t.Helper()
	raw := h.host.Fire("tool_call", map[string]any{"toolName": tool, "toolCallId": id, "input": input})
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		h.t.Fatalf("tool_call result %s: %v", raw, err)
	}
	return out
}

func (h *harness) toolResult(tool string, input map[string]any, text string, isError bool) {
	h.host.Fire("tool_result", map[string]any{"toolName": tool, "toolCallId": "r", "input": input, "content": []any{map[string]any{"type": "text", "text": text}}, "isError": isError})
}

func (h *harness) messages() []sent {
	var out []sent
	for _, c := range h.host.CallsTo("sendMessage") {
		m, _ := c.Args["message"].(map[string]any)
		opts, _ := c.Args["options"].(map[string]any)
		s := sent{Options: opts}
		s.Type, _ = m["customType"].(string)
		s.Content, _ = m["content"].(string)
		s.Display, _ = m["display"].(bool)
		out = append(out, s)
	}
	return out
}

func (h *harness) steers() []sent {
	var out []sent
	for _, m := range h.messages() {
		if m.Type == steerType {
			out = append(out, m)
		}
	}
	return out
}

func (h *harness) notices() []string {
	var out []string
	for _, c := range h.host.CallsTo("ui.notify") {
		s, _ := c.Args["message"].(string)
		out = append(out, s)
	}
	return out
}

func (h *harness) status() string {
	calls := h.host.CallsTo("ui.setStatus")
	if len(calls) == 0 {
		return ""
	}
	s, _ := calls[len(calls)-1].Args["text"].(string)
	return s
}

func on(backend string) *Config {
	c := DefaultConfig()
	c.Enabled, c.Backend, c.Consent = true, backend, backend
	return &c
}

func blocked(r map[string]any) (string, bool) {
	if r == nil {
		return "", false
	}
	b, _ := r["block"].(bool)
	reason, _ := r["reason"].(string)
	return reason, b
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// --- opt-in --------------------------------------------------------------------------------------------------

func TestWardenIsOffUntilYouEnableIt(t *testing.T) {
	h := startHarness(t, harnessOpts{})
	h.start()
	if r := h.toolCall("bash", "c1", map[string]any{"command": "git push --force origin main"}); r != nil {
		t.Fatalf("a call was touched while warden is off: %v", r)
	}
	if h.ts.count() != 0 || len(h.steers()) != 0 {
		t.Fatal("nothing may be sent or said while off")
	}
	if got := h.status(); got != "warden: off · /warden enable" {
		t.Errorf("status %q", got)
	}
	if _, err := os.Stat(ConfigPath(h.home)); err == nil {
		t.Error("warden wrote a config file without being asked")
	}
}

// --- the action guard ----------------------------------------------------------------------------------------

func TestAnIrreversibleCallIsHeldAndTheAgentIsToldWhy(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe)})
	h.ts.set(map[string]float64{"irreversible": 0.95})
	h.say("user", "push my branch")
	h.start()
	reason, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "git push --force origin main"}))
	if !held {
		t.Fatal("the force push was not held")
	}
	for _, want := range []string{"pi-warden held this bash call before it ran", "git force push", "irreversible 0.95", "Do not retry it unchanged", "retry the same call and pi-warden will let it through"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the agent's reason lacks %q:\n%s", want, reason)
		}
	}
	if got := h.notices(); len(got) == 0 || !strings.Contains(got[len(got)-1], "held bash") {
		t.Errorf("the user was not told: %v", got)
	}
	if !strings.HasPrefix(h.status(), "warden: typesafe · steer · 1 checked, 1 held") {
		t.Errorf("status %q", h.status())
	}
	if h.ts.count() != 1 {
		t.Errorf("judged %d times", h.ts.count())
	}
}

func TestAnApprovingReplyReleasesTheHeldCall(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe)})
	h.ts.set(map[string]float64{"irreversible": 0.95})
	h.say("user", "push my branch")
	h.start()
	if _, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "git push --force origin main"})); !held {
		t.Fatal("not held")
	}
	h.say("assistant", "That push is a force push; do you want it?")
	h.say("user", "yes, force push it, I own that branch")
	h.ts.set(map[string]float64{"irreversible": 0.95, "approved": 0.97})
	if r, held := blocked(h.toolCall("bash", "c2", map[string]any{"command": "git push --force origin main"})); held {
		t.Fatalf("the approved retry was held again: %s", r)
	}
	reqs := h.ts.reqs()
	qs := reqs[len(reqs)-1].Body["questions"].(map[string]any)
	if _, ok := qs["approved"]; !ok {
		t.Error("the retry did not ask whether the reply approves")
	}
}

func TestOfflineWardenStillHoldsWithoutAnyBackend(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	h := startHarness(t, harnessOpts{cfg: &cfg})
	h.start()
	reason, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "rm -rf /"}))
	if !held || !strings.Contains(reason, "recursive rm") {
		t.Fatalf("offline patterns did not hold: %q", reason)
	}
	if h.ts.count() != 0 {
		t.Error("offline warden must send nothing")
	}
	if r := h.toolCall("bash", "c2", map[string]any{"command": "npm test"}); r != nil {
		t.Errorf("an ordinary call was touched: %v", r)
	}
}

func TestReadOnlyAndUnguardedCallsSpendNoRequest(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe)})
	h.start()
	h.toolCall("bash", "c1", map[string]any{"command": "ls -la && git status"})
	h.toolCall("read", "c2", map[string]any{"path": "README.md"})
	if h.ts.count() != 0 {
		t.Errorf("%d requests for calls that only read", h.ts.count())
	}
}

func TestConfirmModeAsksYouAndDeclineBlocksIt(t *testing.T) {
	c := on(BackendTypeSafe)
	c.Mode = "confirm"
	h := startHarness(t, harnessOpts{cfg: c})
	h.ts.set(map[string]float64{"irreversible": 0.95})
	h.confirm = false
	h.say("user", "clean up")
	h.start()
	reason, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "rm -rf ~/projects"}))
	if !held || !strings.Contains(reason, "the user declined this bash call") {
		t.Fatalf("declined call: %q", reason)
	}
	dialog := h.host.CallsTo("ui.confirm")
	if len(dialog) != 1 || !strings.Contains(dialog[0].Args["message"].(string), "Judgments are model output, not authorization") {
		t.Fatalf("dialog %v", dialog)
	}
	h.mu.Lock()
	h.confirm = true
	h.mu.Unlock()
	if _, held := blocked(h.toolCall("bash", "c2", map[string]any{"command": "rm -rf ~/projects"})); held {
		t.Error("an approved dialog still blocked")
	}
}

func TestAdviseModeWarnsButNeverHolds(t *testing.T) {
	c := on(BackendTypeSafe)
	c.Mode = "advise"
	h := startHarness(t, harnessOpts{cfg: c})
	h.ts.set(map[string]float64{"irreversible": 0.95})
	h.say("user", "x")
	h.start()
	if _, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "git push --force origin main"})); held {
		t.Fatal("advise mode held a call")
	}
	if n := h.notices(); len(n) == 0 || !strings.Contains(n[len(n)-1], "advise mode, not held") {
		t.Errorf("notices %v", n)
	}
}

func TestADenyRuleBlocksWithoutAJudge(t *testing.T) {
	c := on(BackendTypeSafe)
	c.Action.CommandDenyRules = []CommandRule{{ID: "no-prod", Pattern: `kubectl .*--context prod`, Severity: "deny", Message: "prod is off limits"}}
	h := startHarness(t, harnessOpts{cfg: c})
	h.start()
	reason, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "kubectl get pods --context prod"}))
	if !held || !strings.Contains(reason, "prod is off limits") || h.ts.count() != 0 {
		t.Fatalf("deny: %q, requests %d", reason, h.ts.count())
	}
}

func TestAWarningPlanGapReachesTheAgentOnlyWhenConfigured(t *testing.T) {
	for _, mode := range []string{"all", "none"} {
		c := on(BackendTypeSafe)
		c.Action.IntentTraceOnly = mode
		h := startHarness(t, harnessOpts{cfg: c})
		h.ts.set(map[string]float64{"intent_mismatch": 0.95, "mutates": 0.9})
		h.say("user", "clean the build")
		h.assistantCalls("Let me first list what is in build/.", map[string]string{"id": "c1", "name": "bash", "args": `{"command":"rm -rf build"}`})
		h.start()
		h.toolCall("bash", "c1", map[string]any{"command": "rm -rf build"})
		steers := h.steers()
		if mode == "all" && len(steers) != 0 {
			t.Errorf("trace-only mismatch was sent to the agent: %v", steers)
		}
		if mode == "none" {
			if len(steers) != 1 || !strings.Contains(steers[0].Content, "does something different from what you said you were about to do") || steers[0].Options["deliverAs"] != "steer" || !steers[0].Display {
				t.Errorf("steers %+v", steers)
			}
		}
		// The plan travelled with the request, in the agent's own words.
		body := h.ts.reqs()[0].Body["state"].(map[string]any)
		if body["plan"] != "Let me first list what is in build/." {
			t.Errorf("plan %v", body["plan"])
		}
	}
}

func TestSiblingCallsOfOneMessageAreJudgedTogether(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe)})
	h.say("user", "run the checks")
	h.assistantCalls("", map[string]string{"id": "a", "name": "bash", "args": `{"command":"npm test"}`}, map[string]string{"id": "b", "name": "bash", "args": `{"command":"npm run lint"}`})
	h.start()
	h.toolCall("bash", "a", map[string]any{"command": "npm test"})
	waitUntil(t, func() bool { return h.ts.count() >= 2 })
	h.toolCall("bash", "b", map[string]any{"command": "npm run lint"})
	if n := h.ts.count(); n != 2 {
		t.Errorf("each sibling is judged exactly once: %d requests", n)
	}
}

func TestAFailingJudgeFallsBackToTheOfflinePatternsAndSaysSoOnce(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe)})
	h.ts.mu.Lock()
	h.ts.status = 500
	h.ts.mu.Unlock()
	h.say("user", "x")
	h.start()
	reason, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "git push --force origin main"}))
	if !held || !strings.Contains(reason, "built-in patterns decide") {
		t.Fatalf("the pattern floor did not hold: %q", reason)
	}
	h.toolCall("bash", "c2", map[string]any{"command": "npm test"})
	h.toolCall("bash", "c3", map[string]any{"command": "npm run build"})
	errNotices := 0
	for _, n := range h.notices() {
		if strings.Contains(n, "TypeSafe returned HTTP 500") {
			errNotices++
		}
		if strings.Contains(n, tsErrorMarker) {
			t.Errorf("upstream detail leaked into a notice: %s", n)
		}
	}
	if errNotices != 1 {
		t.Errorf("the judge failure was announced %d times", errNotices)
	}
}

func TestAJudgedCallIsFastEnoughToSitInFrontOfATool(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe)})
	h.say("user", "x")
	h.start()
	h.toolCall("bash", "warm", map[string]any{"command": "npm test"})
	start := time.Now()
	h.toolCall("bash", "c1", map[string]any{"command": "npm run build"})
	if d := time.Since(start); d > 250*time.Millisecond {
		t.Errorf("a judged call took %v", d)
	}
}

// --- stuck ---------------------------------------------------------------------------------------------------

func TestTheSecondIdenticalFailureIsNudgedAndTheThirdIsStuck(t *testing.T) {
	c := on(BackendNone)
	c.Enabled = true
	h := startHarness(t, harnessOpts{cfg: c})
	h.start()
	in := map[string]any{"command": "npm run build"}
	for i := 0; i < 3; i++ {
		h.toolResult("bash", in, "Error: module not found\nCommand exited with code 1", true)
	}
	steers := h.steers()
	if len(steers) != 2 {
		t.Fatalf("steers %d: %+v", len(steers), steers)
	}
	if !strings.Contains(steers[0].Content, "you already ran `npm run build`; it failed the same way") {
		t.Errorf("quick repeat: %s", steers[0].Content)
	}
	if !strings.Contains(steers[1].Content, "the same call failed 3 times with the same output") || !strings.Contains(steers[1].Content, "Stop retrying") {
		t.Errorf("stuck: %s", steers[1].Content)
	}
	if h.ts.count() != 0 {
		t.Error("exact repeats are decided in code, not by a request")
	}
}

func TestALoopThatChangesItsErrorGoesToTheJudge(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe)})
	h.ts.set(map[string]float64{"same_strategy": 0.95, "progress": 0.1})
	h.start()
	for i, msg := range []string{"cannot find a", "cannot find b", "cannot find c"} {
		h.toolResult("bash", map[string]any{"command": "make " + itoaInt(i)}, msg, true)
	}
	if h.ts.count() != 1 {
		t.Fatalf("requests %d", h.ts.count())
	}
	steers := h.steers()
	if len(steers) != 1 || !strings.Contains(steers[0].Content, "3 failures with the same strategy (0.95)") {
		t.Fatalf("steers %+v", steers)
	}
}

func TestSteersPastTheBudgetAreRecordedNotSent(t *testing.T) {
	c := on(BackendNone)
	c.Enabled = true
	c.SteerBudget = 1
	h := startHarness(t, harnessOpts{cfg: c})
	h.start()
	// Two different files each read twice: two quick-repeat notices, one steer of budget.
	for _, p := range []string{"a.ts", "b.ts"} {
		h.toolResult("read", map[string]any{"path": p}, "content of "+p, false)
		h.toolResult("read", map[string]any{"path": p}, "content of "+p, false)
	}
	if n := len(h.steers()); n != 1 {
		t.Errorf("the budget of one steer allowed %d", n)
	}
	h.host.Command("warden", "status")
	msgs := h.messages()
	if last := msgs[len(msgs)-1].Content; !strings.Contains(last, "1 steers sent (1 skipped") {
		t.Errorf("status does not count the skipped steer: %s", last)
	}
	// The next prompt starts a new run: its budget is fresh.
	h.host.Fire("before_agent_start", map[string]any{"prompt": "next", "systemPromptOptions": map[string]any{}})
	h.toolResult("read", map[string]any{"path": "c.ts"}, "c", false)
	h.toolResult("read", map[string]any{"path": "c.ts"}, "c", false)
	if n := len(h.steers()); n != 2 {
		t.Errorf("a new prompt did not restore the budget: %d", n)
	}
}

// --- done ----------------------------------------------------------------------------------------------------

func TestAnUnverifiedDoneClaimIsSentBackToVerify(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe)})
	h.ts.set(map[string]float64{"claims_done": 0.95, "claims_verified": 0.1, "verification_applies": 0.9})
	h.say("user", "fix the parser")
	h.start()
	h.host.Fire("agent_start", nil)
	h.toolResult("edit", map[string]any{"path": "src/parser.go"}, "ok", false)
	h.host.Fire("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": "Fixed the parser, all done."}}}}})
	steers := h.steers()
	if len(steers) != 1 {
		t.Fatalf("steers %+v", steers)
	}
	if !strings.Contains(steers[0].Content, "reports completion (0.95) after 1 file change with no test, build, or lint run since the last change") || steers[0].Options["deliverAs"] != "followUp" || steers[0].Options["triggerTurn"] != true {
		t.Errorf("done nudge %+v", steers[0])
	}
	// One nudge per prompt: a second unverified claim is judged but not nudged again.
	h.host.Fire("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": "Done again."}}})
	if n := len(h.steers()); n != 1 {
		t.Errorf("nudged %d times", n)
	}
}

func TestAPassingCheckAfterTheChangeAsksNothing(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe)})
	h.start()
	h.host.Fire("agent_start", nil)
	h.toolResult("edit", map[string]any{"path": "src/parser.go"}, "ok", false)
	h.toolResult("bash", map[string]any{"command": "go test ./..."}, "ok  \tpkg\t0.2s", false)
	h.host.Fire("agent_end", map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Done."}}}}})
	if h.ts.count() != 0 {
		t.Errorf("the done-check asked although a check passed after the change")
	}
}

// --- commands and consent ------------------------------------------------------------------------------------

func TestEnableShowsTheDataFlowBeforeAnythingIsSentAndDecliningChangesNothing(t *testing.T) {
	h := startHarness(t, harnessOpts{})
	h.confirm = false
	h.host.Command("warden", "enable typesafe")
	dialog := h.host.CallsTo("ui.confirm")
	if len(dialog) != 1 {
		t.Fatalf("dialogs %d", len(dialog))
	}
	msg := dialog[0].Args["message"].(string)
	for _, want := range []string{"Offline checks run on this machine and send nothing", "your latest request", "Never sent:", "TYPESAFE_API_KEY", h.ts.URL, "/warden disable"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the disclosure lacks %q", want)
		}
	}
	if h.ts.count() != 0 {
		t.Error("something was sent before consent")
	}
	if _, err := os.Stat(ConfigPath(h.home)); err == nil {
		t.Error("declining wrote the config")
	}
}

func TestEnableRecordsConsentAndTurnsItOn(t *testing.T) {
	h := startHarness(t, harnessOpts{})
	h.ts.set(map[string]float64{"irreversible": 0.95})
	h.host.Command("warden", "enable typesafe")
	cfg, err := LoadConfig(ConfigPath(h.home))
	if err != nil || !cfg.Enabled || cfg.Backend != BackendTypeSafe || cfg.Consent != BackendTypeSafe {
		t.Fatalf("config %+v %v", cfg, err)
	}
	if info, _ := os.Stat(ConfigPath(h.home)); info.Mode().Perm() != 0o600 {
		t.Errorf("config mode %v", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(ConfigPath(h.home))
	if strings.Contains(string(raw), "test-key-not-real") {
		t.Error("the key was written to the config")
	}
	h.say("user", "push")
	if _, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "git push --force origin main"})); !held {
		t.Error("enabling did not take effect in the running session")
	}
}

func TestEnableWithoutAnArgumentOffersTheChoiceAndOwnModelSendsNothingToTypeSafe(t *testing.T) {
	reg := newRegistry()
	reg.noul["irreversible"] = 0.95
	var built int
	h := startHarness(t, harnessOpts{newJudge: func(cfg Config, b *Budget) (Judge, error) {
		built++
		return NewOwnModelJudge(reg, "prov", "mod", b)
	}})
	h.pick = "This session's model — nothing goes to a third party (own-model)"
	h.host.Command("warden", "enable")
	if sel := h.host.CallsTo("ui.select"); len(sel) != 1 {
		t.Fatalf("select %d", len(sel))
	}
	msg := h.host.CallsTo("ui.confirm")[0].Args["message"].(string)
	if !strings.Contains(msg, "your session model (prov/mod)") || !strings.Contains(msg, "Nothing goes to TypeSafe") {
		t.Errorf("own-model disclosure:\n%s", msg)
	}
	h.say("user", "push my branch")
	if _, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "git push --force origin main"})); !held {
		t.Fatal("the own-model judge did not hold the force push")
	}
	if h.ts.count() != 0 || len(reg.systemPrompts()) == 0 {
		t.Errorf("TypeSafe requests %d, session-model requests %d", h.ts.count(), len(reg.systemPrompts()))
	}
	if h.status() == "" || !strings.Contains(h.status(), "own model") {
		t.Errorf("status %q", h.status())
	}
}

func TestABackendWithoutConsentRunsOfflineAndSaysWhy(t *testing.T) {
	c := DefaultConfig()
	c.Enabled, c.Backend = true, BackendTypeSafe // no Consent
	h := startHarness(t, harnessOpts{cfg: &c})
	h.say("user", "x")
	h.start()
	h.toolCall("bash", "c1", map[string]any{"command": "npm test"})
	if h.ts.count() != 0 {
		t.Fatal("a backend that was not agreed to was used")
	}
	found := false
	for _, n := range h.notices() {
		if strings.Contains(n, "was not agreed to") {
			found = true
		}
	}
	if !found {
		t.Errorf("no explanation: %v", h.notices())
	}
}

func TestDisableStopsEverythingAndForgetsTheConsent(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe)})
	h.start()
	h.host.Command("warden", "disable")
	cfg, _ := LoadConfig(ConfigPath(h.home))
	if cfg.Enabled || cfg.Consent != "" {
		t.Errorf("config %+v", cfg)
	}
	if r := h.toolCall("bash", "c1", map[string]any{"command": "git push --force origin main"}); r != nil {
		t.Errorf("a disabled warden held a call: %v", r)
	}
	if h.status() != "warden: off · /warden enable" {
		t.Errorf("status %q", h.status())
	}
}

func TestModeCommandChangesHowAHoldIsHandled(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe)})
	h.ts.set(map[string]float64{"irreversible": 0.95})
	h.say("user", "x")
	h.start()
	h.host.Command("warden", "mode advise")
	if _, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "git push --force origin main"})); held {
		t.Error("advise mode held")
	}
	cfg, _ := LoadConfig(ConfigPath(h.home))
	if cfg.Mode != "advise" {
		t.Errorf("mode %q", cfg.Mode)
	}
}

func TestTestCommandShowsWhatWardenWouldDoAndRunsNothing(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	h := startHarness(t, harnessOpts{cfg: &cfg})
	h.start()
	h.host.Command("warden", "test")
	var report string
	for _, m := range h.messages() {
		if m.Type == statusType {
			report = m.Content
		}
	}
	for _, want := range []string{"nothing was run", "git push --force origin main", "HELD", "git force push", "rm -rf ~/projects", "the agent would read: pi-warden held this bash call", "npm test", "allowed"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report lacks %q:\n%s", want, report)
		}
	}
	if h.ts.count() != 0 {
		t.Error("the self-test sent something")
	}
	for _, m := range h.messages() {
		if m.Options["triggerTurn"] != false {
			t.Errorf("a status message must not start a turn: %+v", m)
		}
	}
}

func TestStatusNamesTheBackendAndWhatLeaves(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe), env: map[string]string{}})
	h.host.Command("warden", "status")
	var status string
	for _, m := range h.messages() {
		status = m.Content
	}
	for _, want := range []string{"warden is on: TypeSafe judge at", "steer mode", "Never sent:", "checked 0 calls"} {
		if !strings.Contains(status, want) {
			t.Errorf("status lacks %q:\n%s", want, status)
		}
	}
}

// --- headless -------------------------------------------------------------------------------------------------

func TestAHeadlessRunConsentsThroughTheEnvironmentAndDisclosesInTheTranscript(t *testing.T) {
	h := startHarness(t, harnessOpts{noUI: true, env: map[string]string{"PIGPEN_WARDEN_ENABLED": "1", "PIGPEN_WARDEN_BACKEND": "typesafe", "TYPESAFE_API_KEY": "test-key-not-real"}})
	h.start()
	var disclosed bool
	for _, m := range h.messages() {
		if m.Type == statusType && strings.Contains(m.Content, "warden is on") && strings.Contains(m.Content, "Never sent:") && m.Display {
			disclosed = true
		}
	}
	if !disclosed {
		t.Fatal("a headless run must show the data flow in the transcript")
	}
	h.ts.set(map[string]float64{"irreversible": 0.95})
	if _, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "git push --force origin main"})); !held {
		t.Error("headless steer-mode hold failed")
	}
	if h.host.CallsTo("ui.confirm") != nil || h.host.CallsTo("ui.notify") != nil {
		t.Error("a headless run used a dialog or a notification")
	}
}

func TestHeadlessEnableOfAJudgedBackendNeedsTheEnvironmentNotACommand(t *testing.T) {
	h := startHarness(t, harnessOpts{noUI: true})
	h.host.Command("warden", "enable typesafe")
	if _, err := os.Stat(filepath.Join(h.home, "pigpen-warden", "config.json")); err == nil {
		t.Error("consent was recorded without a dialog")
	}
}

// The own-model backend end to end: the session's model is reached through PiG's model registry (the SDK's
// ModelRegistry over the host's getModel / getModelAuth / modelStream calls), with no injected judge.
func TestOwnModelBackendThroughPiGsModelRegistry(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendOwnModel)})
	h.registry = newRegistry()
	h.registry.noul["irreversible"] = 0.95
	h.say("user", "push my branch")
	h.start()
	reason, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "git push --force origin main"}))
	if !held || !strings.Contains(reason, "irreversible 0.9") {
		t.Fatalf("the session model did not hold the call: %q", reason)
	}
	if h.ts.count() != 0 {
		t.Error("the own-model backend must not reach TypeSafe")
	}
	if n := len(h.registry.systemPrompts()); n != 1 {
		t.Errorf("session-model requests %d", n)
	}
	if !strings.Contains(h.status(), "own model") {
		t.Errorf("status %q", h.status())
	}
}

// Skipped twin: the real-Pi shape of an assistant tool-call message. Pi 0.87.1 writes a toolCall block's
// `arguments` as a JSON object; the Go SDK reads it as a string, so the plan and the sibling calls are lost from
// GetBranch(). Not worked around here: see port/PORT.md, "Findings about the hosts" 1 (routed to the SDK
// owners, 0.4.0). Remove the skip when the SDK accepts object arguments.
func TestSkippedSDKToolCallArgumentsAsObject(t *testing.T) {
	t.Skip("sdk BranchEntry rejects object `arguments` (Pi 0.87.1 shape): port/PORT.md, Findings about the hosts 1")
	c := on(BackendTypeSafe)
	c.Action.IntentTraceOnly = "none"
	h := startHarness(t, harnessOpts{cfg: c})
	h.ts.set(map[string]float64{"intent_mismatch": 0.95, "mutates": 0.9})
	h.say("user", "clean the build")
	h.append(map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "text", "text": "Let me first list what is in build/."},
		map[string]any{"type": "toolCall", "id": "c1", "name": "bash", "arguments": map[string]any{"command": "rm -rf build"}},
	}})
	h.start()
	h.toolCall("bash", "c1", map[string]any{"command": "rm -rf build"})
	state := h.ts.reqs()[0].Body["state"].(map[string]any)
	if state["plan"] != "Let me first list what is in build/." {
		t.Errorf("the agent's words before the call did not reach the judge: %v", state["plan"])
	}
}
