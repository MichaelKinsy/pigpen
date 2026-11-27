package prediction

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

// ── Parser & Prompt Rendering Tests ──────────────────────────────────────────

func TestParseComposerPrediction(t *testing.T) {
	// upstream: test/composer-prediction-controller.test.ts:28-42
	cases := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{"collapses whitespace", "  run the\n  tests\t now ", "run the tests now", true},
		{"strips straight double quotes", `"ship it"`, "ship it", true},
		{"strips smart double quotes", "“ship it”", "ship it", true},
		{"strips backticks", "`run tests`", "run tests", true},
		{"keeps internal quotes", `use "bun test" here`, `use "bun test" here`, true},
		{"keeps internal backticks", "run `go test` now", "run `go test` now", true},
		{"strips ANSI escape and control chars", "\x1b[31mship\x00 it\x1b[0m", "ship it", true},
		{"rejects NO_PREDICTION", "NO_PREDICTION", "", false},
		{"rejects NO_PREDICTION with period", "NO_PREDICTION.", "", false},
		{"rejects empty quoted string", ` "" `, "", false},
		{"rejects empty backticks", "``", "", false},
		{"rejects rambling >500 chars", strings.Repeat("word ", 200), "", false},
		{"accepts exactly 500 chars", strings.Repeat("a", 500), strings.Repeat("a", 500), true},
		{"rejects 501 chars", strings.Repeat("a", 501), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseComposerPrediction(tc.in)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("ParseComposerPrediction(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestParseComposerPredictions(t *testing.T) {
	// upstream: test/composer-prediction-controller.test.ts:44-67
	t.Run("one per line without markers quotes or case/punct repeats", func(t *testing.T) {
		reply := "1. run the tests\n- \"ship it\"\nRun the tests!\n\n* show the diff\n2) revert it\nopen a PR"
		got4 := ParseComposerPredictions(reply, 4)
		want4 := []string{"run the tests", "ship it", "show the diff", "revert it"}
		if !reflect.DeepEqual(got4, want4) {
			t.Fatalf("got %#v, want %#v", got4, want4)
		}
		got2 := ParseComposerPredictions(reply, 2)
		want2 := []string{"run the tests", "ship it"}
		if !reflect.DeepEqual(got2, want2) {
			t.Fatalf("got %#v, want %#v", got2, want2)
		}
	})

	t.Run("bullet marker and CRLF", func(t *testing.T) {
		reply := "• first choice\r\n• second choice"
		got := ParseComposerPredictions(reply, 4)
		want := []string{"first choice", "second choice"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("declines on leading skip and drops unusable trailing lines", func(t *testing.T) {
		if got := ParseComposerPredictions("NO_PREDICTION", 4); got != nil {
			t.Fatalf("got %#v, want nil", got)
		}
		if got := ParseComposerPredictions("\nNO_PREDICTION\nship it", 4); got != nil {
			t.Fatalf("got %#v, want nil", got)
		}
		got := ParseComposerPredictions("ship it\nNO_PREDICTION\n"+strings.Repeat("word ", 200)+"\n\"\"", 4)
		if !reflect.DeepEqual(got, []string{"ship it"}) {
			t.Fatalf("got %#v, want [ship it]", got)
		}
		if got := ParseComposerPredictions(" \n", 4); got != nil {
			t.Fatalf("got %#v, want nil", got)
		}
	})

	t.Run("drops leading preamble line ending in colon and preserves emoji/punct dedupe", func(t *testing.T) {
		got := ParseComposerPredictions("Here are some options:\nrun the tests\nship it", 4)
		want := []string{"run the tests", "ship it"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
		// Single line ending in colon is NOT dropped.
		gotSingle := ParseComposerPredictions("fix this:", 4)
		if !reflect.DeepEqual(gotSingle, []string{"fix this:"}) {
			t.Fatalf("got %#v, want [fix this:]", gotSingle)
		}
		// Emoji and punctuation-only lines deduplicate verbatim.
		gotEmoji := ParseComposerPredictions("👍\n?\n👍\nok", 4)
		wantEmoji := []string{"👍", "?", "ok"}
		if !reflect.DeepEqual(gotEmoji, wantEmoji) {
			t.Fatalf("got %#v, want %#v", gotEmoji, wantEmoji)
		}
	})
}

func TestRenderPrompt(t *testing.T) {
	single := RenderPrompt(false)
	wantSingle := strings.TrimSpace(`<composer-prediction>
Predict the user's next message in this conversation. Reply with that message only, exactly as the user would type it.
- Write as the user: their voice, language, tone, and typical length, judged only from messages they typed themselves. Mirror how they phrase requests.
- Follow up on the latest assistant reply: approve, redirect, ask for the next step, or report a result.
- One line. No quotes, preamble, or markdown.
- Keep it short; usually under 20 words.
- The user is the person at the keyboard. Messages attributed to another agent, session, or tool are relayed, not theirs: ` + "`<irc>`" + ` blocks, and pasted notes headed with a sender (e.g. "Note from the build session: …", "From LEAD: …"). Never predict a relay or write in its sender's voice.
- No confident guess: reply exactly NO_PREDICTION
</composer-prediction>`)
	if single != wantSingle {
		t.Fatalf("single prompt mismatch:\n--- GOT ---\n%s\n--- WANT ---\n%s", single, wantSingle)
	}

	multi := RenderPrompt(true)
	wantMulti := strings.TrimSpace(`<composer-prediction>
Predict the user's next message in this conversation. Reply with up to 4 different messages they might send next, one per line, most likely first, each exactly as the user would type it.
- Write as the user: their voice, language, tone, and typical length, judged only from messages they typed themselves. Mirror how they phrase requests.
- Follow up on the latest assistant reply: approve, redirect, ask for the next step, or report a result.
- Make each line a different next step or intent, never a rewording of another line. Include only plausible ones; fewer lines is fine.
- One message per line. No numbering, bullets, quotes, preamble, or markdown.
- Keep each short; usually under 20 words.
- The user is the person at the keyboard. Messages attributed to another agent, session, or tool are relayed, not theirs: ` + "`<irc>`" + ` blocks, and pasted notes headed with a sender (e.g. "Note from the build session: …", "From LEAD: …"). Never predict a relay or write in its sender's voice.
- No confident guess: reply exactly NO_PREDICTION
</composer-prediction>`)
	if multi != wantMulti {
		t.Fatalf("multi prompt mismatch:\n--- GOT ---\n%s\n--- WANT ---\n%s", multi, wantMulti)
	}
}

func TestBuildNativeWidgetView(t *testing.T) {
	if _, ok := BuildNativeWidgetView(nil); ok {
		t.Fatal("expected false for empty choices")
	}
	view, ok := BuildNativeWidgetView([]string{"run the tests", "ship it"})
	if !ok {
		t.Fatal("expected ok for non-empty choices")
	}
	root, ok := view.Root.(*kit.VStack)
	if !ok {
		t.Fatalf("root type = %T, want *kit.VStack", view.Root)
	}
	if root.ID != WidgetKey {
		t.Fatalf("root.ID = %q, want %q", root.ID, WidgetKey)
	}
	if len(root.Children) != 2 {
		t.Fatalf("len(children) = %d, want 2", len(root.Children))
	}
	c0 := root.Children[0].(*kit.Text)
	c1 := root.Children[1].(*kit.Text)
	if c0.ID != "omp.composer.prediction.choice#0" || c0.Text != "run the tests" {
		t.Fatalf("child 0 = %+v", c0)
	}
	if c1.ID != "omp.composer.prediction.choice#1" || c1.Text != "ship it" {
		t.Fatalf("child 1 = %+v", c1)
	}
}

// ── Minimal Fake Host for End-to-End Controller & Lifecycle Tests ────────────

type hostCall struct {
	Method string
	Args   map[string]any
}

type fakeHostOpts struct {
	Mode         string
	HasUI        *bool
	SessionID    string
	Model        string
	Provider     string
	NoModel      bool
	SystemPrompt string
	EditorText   string
	Branch       []map[string]any
	ActiveTools  []string
	AllTools     []map[string]any
}

type fakeHost struct {
	t        *testing.T
	nc       net.Conn
	opts     fakeHostOpts
	runDone  chan error
	writeMu  sync.Mutex
	mu       sync.Mutex
	calls    []hostCall
	handlers map[string][]int
	pending  map[string]chan json.RawMessage
	nextID   int

	editorText string
	branch     []map[string]any
}

type wireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
	} `json:"register,omitempty"`
	Response *struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response,omitempty"`
	Call *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"call,omitempty"`
	WidgetPush json.RawMessage `json:"widget_push,omitempty"`
}

func defaultBranch() []map[string]any {
	return []map[string]any{
		{"id": "e1", "type": "message", "message": map[string]any{"role": "user", "content": "fix the bug"}},
		{"id": "e2", "type": "message", "message": map[string]any{"role": "assistant", "content": "I fixed the bug in main.go."}},
	}
}

func doneAgentEndEvent() map[string]any {
	return map[string]any{
		"type": "agent_end",
		"messages": []any{
			map[string]any{"role": "assistant", "stopReason": "stop", "content": "I fixed the bug."},
		},
	}
}

func startFakeHost(t *testing.T, ext *sdk.Extension, opts fakeHostOpts) *fakeHost {
	t.Helper()
	if opts.Mode == "" {
		opts.Mode = "tui"
	}
	hasUI := opts.Mode == "tui"
	if opts.HasUI != nil {
		hasUI = *opts.HasUI
	}
	if opts.SessionID == "" {
		opts.SessionID = "sess-pred-1"
	}
	if opts.Model == "" && !opts.NoModel {
		opts.Model = "claude-sonnet-4-5"
	}
	if opts.Provider == "" && !opts.NoModel {
		opts.Provider = "anthropic"
	}
	if opts.SystemPrompt == "" {
		opts.SystemPrompt = "You are a helpful coding assistant."
	}
	branch := opts.Branch
	if branch == nil {
		branch = defaultBranch()
	}

	hostSide, extSide := net.Pipe()
	h := &fakeHost{
		t:          t,
		nc:         hostSide,
		opts:       opts,
		runDone:    make(chan error, 1),
		handlers:   map[string][]int{},
		pending:    map[string]chan json.RawMessage{},
		editorText: opts.EditorText,
		branch:     branch,
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()

	first := h.read()
	for first.Type == "call" && first.Call != nil {
		h.answer(first)
		first = h.read()
	}
	if first.Type != "register" || first.Register == nil {
		t.Fatalf("expected register, got %+v", first)
	}
	for _, hnd := range first.Register.Handlers {
		h.handlers[hnd.Event] = append(h.handlers[hnd.Event], hnd.HandlerID)
	}

	stateObj := map[string]any{
		"hasUI":     hasUI,
		"sessionId": opts.SessionID,
	}
	if !opts.NoModel {
		stateObj["model"] = map[string]any{
			"id":       opts.Model,
			"provider": opts.Provider,
		}
	}
	stateRaw, _ := json.Marshal(stateObj)
	h.write(map[string]any{"type": "ready", "ready": map[string]any{
		"cwd":   t.TempDir(),
		"mode":  opts.Mode,
		"width": 100,
		"state": json.RawMessage(stateRaw),
	}})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

func (h *fakeHost) stop() {
	_ = h.nc.Close()
	select {
	case <-h.runDone:
	case <-time.After(2 * time.Second):
	}
}

func (h *fakeHost) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		h.t.Fatalf("marshal: %v", err)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_, _ = h.nc.Write(hdr[:])
	_, _ = h.nc.Write(b)
}

func (h *fakeHost) read() wireFrame {
	f, ok := h.readMaybe()
	if !ok {
		h.t.Fatalf("unexpected EOF from extension")
	}
	return f
}

func (h *fakeHost) readMaybe() (wireFrame, bool) {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		return wireFrame{}, false
	}
	n := binary.BigEndian.Uint32(hdr[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(h.nc, buf); err != nil {
		return wireFrame{}, false
	}
	var f wireFrame
	if err := json.Unmarshal(buf, &f); err != nil {
		return wireFrame{}, false
	}
	return f, true
}

func (h *fakeHost) serve() {
	for {
		f, ok := h.readMaybe()
		if !ok {
			return
		}
		switch f.Type {
		case "call":
			go h.answer(f)
		case "widget_push":
			var args map[string]any
			_ = json.Unmarshal(f.WidgetPush, &args)
			h.mu.Lock()
			h.calls = append(h.calls, hostCall{Method: "widget_push", Args: args})
			h.mu.Unlock()
		case "response":
			h.mu.Lock()
			ch := h.pending[f.ID]
			delete(h.pending, f.ID)
			h.mu.Unlock()
			if ch != nil {
				var res json.RawMessage
				if f.Response != nil {
					res = f.Response.Result
				}
				ch <- res
			}
		}
	}
}

func (h *fakeHost) answer(f wireFrame) {
	var args map[string]any
	if len(f.Call.Args) > 0 {
		_ = json.Unmarshal(f.Call.Args, &args)
	}
	h.mu.Lock()
	h.calls = append(h.calls, hostCall{Method: f.Call.Method, Args: args})
	editorText := h.editorText
	branch := append([]map[string]any(nil), h.branch...)
	h.mu.Unlock()

	var res any = map[string]any{}
	switch f.Call.Method {
	case "getSessionID":
		res = map[string]any{"sessionId": h.opts.SessionID}
	case "getModelInfo":
		if h.opts.NoModel {
			res = map[string]any{"id": "", "provider": ""}
		} else {
			res = map[string]any{"id": h.opts.Model, "provider": h.opts.Provider}
		}
	case "getModel":
		if !h.opts.NoModel {
			res = map[string]any{"id": h.opts.Model, "provider": h.opts.Provider, "api": "anthropic-messages"}
		}
	case "getModelAuth":
		res = map[string]any{"apiKey": "test-key"}
	case "getSystemPrompt":
		res = map[string]any{"prompt": h.opts.SystemPrompt}
	case "getActiveTools":
		active := h.opts.ActiveTools
		if active == nil {
			active = []string{"read", "bash"}
		}
		res = map[string]any{"tools": active}
	case "getAllTools":
		all := h.opts.AllTools
		if all == nil {
			all = []map[string]any{
				{
					"name":        "read",
					"description": "Read a file",
					"parameters":  map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
				},
				{
					"name":        "bash",
					"description": "Run shell command",
					"parameters":  map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}},
				},
				{
					"name":        "unused_tool",
					"description": "Inactive tool",
					"parameters":  map[string]any{"type": "object"},
				},
			}
		}
		res = map[string]any{"tools": all}
	case "sessionRead":
		method, _ := args["method"].(string)
		switch method {
		case "buildSessionContext":
			linked := linkBranchEntries(branch)
			if built, err := sdk.BuildSessionContext(linked, sdk.SessionLeafLast, nil); err == nil {
				res = built
			} else {
				res = map[string]any{"messages": []any{}}
			}
		case "getLeafId":
			if len(branch) > 0 {
				if id, _ := branch[len(branch)-1]["id"].(string); id != "" {
					res = id
				} else {
					res = nil
				}
			} else {
				res = nil
			}
		}
	case "getBranch":
		res = map[string]any{"entries": branch}
	case "watchSessionLog", "getSessionLog":
		rawEntries := linkBranchEntries(branch)
		leafID := ""
		if len(rawEntries) > 0 {
			leafID, _ = rawEntries[len(rawEntries)-1]["id"].(string)
		}
		cursor := 0
		if c, ok := args["cursor"].(float64); ok {
			cursor = int(c)
		}
		var slice []map[string]any
		if cursor < len(rawEntries) {
			slice = rawEntries[cursor:]
		} else {
			slice = []map[string]any{}
		}
		res = map[string]any{
			"entries":    slice,
			"entryCount": len(rawEntries),
			"leafId":     leafID,
			"generation": 1,
		}
	case "ui.getEditorText":
		res = map[string]any{"text": editorText}
	case "ui.setWidget":
		res = map[string]any{}
	}
	raw, _ := json.Marshal(res)
	h.write(map[string]any{
		"type": "call_result",
		"id":   f.ID,
		"call_result": map[string]any{
			"result": json.RawMessage(raw),
		},
	})
}

func linkBranchEntries(branch []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(branch))
	for i, orig := range branch {
		e := make(map[string]any, len(orig)+1)
		for k, v := range orig {
			e[k] = v
		}
		if i > 0 {
			if _, hasParent := e["parentId"]; !hasParent {
				if prevID, _ := branch[i-1]["id"].(string); prevID != "" {
					e["parentId"] = prevID
				}
			}
		}
		out = append(out, e)
	}
	return out
}

func (h *fakeHost) fireEvent(event string, data map[string]any) {
	h.t.Helper()
	ids := h.handlers[event]
	if len(ids) == 0 {
		h.t.Fatalf("no handler for event %q", event)
	}
	raw, _ := json.Marshal(data)
	for _, hid := range ids {
		h.mu.Lock()
		h.nextID++
		id := fmt.Sprintf("req-%d", h.nextID)
		ch := make(chan json.RawMessage, 1)
		h.pending[id] = ch
		h.mu.Unlock()
		h.write(map[string]any{
			"type": "request",
			"id":   id,
			"request": map[string]any{
				"method":     "event",
				"event":      event,
				"handler_id": hid,
				"args":       json.RawMessage(raw),
			},
		})
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			h.t.Fatalf("timeout firing %s", event)
		}
	}
}

func (h *fakeHost) setEditorText(text string) {
	h.mu.Lock()
	h.editorText = text
	h.mu.Unlock()
}

func (h *fakeHost) appendBranch(entry map[string]any) {
	h.mu.Lock()
	prevID := ""
	if n := len(h.branch); n > 0 {
		prevID, _ = h.branch[n-1]["id"].(string)
	}
	e := make(map[string]any, len(entry)+1)
	for k, v := range entry {
		e[k] = v
	}
	if prevID != "" {
		if _, ok := e["parentId"]; !ok {
			e["parentId"] = prevID
		}
	}
	h.branch = append(h.branch, e)
	count := len(h.branch)
	leafID, _ := e["id"].(string)
	h.mu.Unlock()
	// Push state_update notify with session.entriesAppended so the SDK's session mirror updates.
	raw, _ := json.Marshal(map[string]any{
		"state": map[string]any{
			"session": map[string]any{
				"sessionId":       h.opts.SessionID,
				"entriesAppended": []map[string]any{e},
				"entryCount":      count,
				"leafId":          leafID,
			},
		},
	})
	h.write(map[string]any{
		"type": "notify",
		"notify": map[string]any{
			"method": "state_update",
			"args":   json.RawMessage(raw),
		},
	})
}

func (h *fakeHost) widgetCalls() []hostCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []hostCall
	for _, c := range h.calls {
		if c.Method == "ui.setWidget" || c.Method == "widget_push" {
			out = append(out, c)
		}
	}
	return out
}

func (h *fakeHost) waitWidgetCalls(minCount int) []hostCall {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		calls := h.widgetCalls()
		if len(calls) >= minCount {
			return calls
		}
		time.Sleep(5 * time.Millisecond)
	}
	return h.widgetCalls()
}

func newTestSetup(t *testing.T, opts fakeHostOpts, native bool, completeFn CompleteFunc) (*Controller, *fakeHost) {
	t.Helper()
	surface.ResetForTests()
	surface.SetNative(native)
	prevSetting := Setting
	prevSubagent := IsSubagent
	Setting = nil
	IsSubagent = nil
	t.Cleanup(func() {
		Setting = prevSetting
		IsSubagent = prevSubagent
		surface.ResetForTests()
		lifecycle.ResetForTests()
	})

	ctrl := NewController()
	if completeFn != nil {
		ctrl.complete = completeFn
	}
	ext := sdk.New("prediction-test")
	registerWithController(ext, ctrl)
	h := startFakeHost(t, ext, opts)
	return ctrl, h
}

func assistantReplyJSON(text string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"stopReason": "stop",
		"content":    []map[string]any{{"type": "text", "text": text}},
	})
	return raw
}

// ── Controller & Gating Tests ────────────────────────────────────────────────

func TestControllerPublishesSingleAndAlternativePredictions(t *testing.T) {
	var capturedReq map[string]any
	ctrl, h := newTestSetup(t, fakeHostOpts{}, true, func(_ context.Context, _ sdk.Context, _, req, _ map[string]any) (json.RawMessage, error) {
		capturedReq = req
		return assistantReplyJSON("run the tests\nship it"), nil
	})

	// Single choice (default composer.predictionAlternatives == false)
	h.fireEvent(sdk.EventAgentEnd, doneAgentEndEvent())
	ctrl.Wait()

	if got := ctrl.Choices("sess-pred-1"); !reflect.DeepEqual(got, []string{"run the tests ship it"}) {
		t.Fatalf("single choices = %#v, want [run the tests ship it]", got)
	}
	if capturedReq["systemPrompt"] != "You are a helpful coding assistant." {
		t.Fatalf("systemPrompt = %v", capturedReq["systemPrompt"])
	}
	if capturedReq["maxTokens"] != PredictionMaxTokens {
		t.Fatalf("maxTokens = %v, want %d", capturedReq["maxTokens"], PredictionMaxTokens)
	}
	if capturedReq["toolChoice"] != "none" {
		t.Fatalf("toolChoice = %v, want none", capturedReq["toolChoice"])
	}
	wCalls := h.waitWidgetCalls(1)
	if len(wCalls) != 1 || wCalls[0].Method != "widget_push" || wCalls[0].Args["key"] != WidgetKey || wCalls[0].Args["view"] == nil {
		t.Fatalf("expected 1 widget_push view call, got %+v", wCalls)
	}

	// Alternatives enabled
	Setting = func(_ sdk.Context, key string) any {
		if key == SettingAlternatives {
			return true
		}
		return true
	}
	h.fireEvent(sdk.EventAgentEnd, doneAgentEndEvent())
	ctrl.Wait()

	if got := ctrl.Choices("sess-pred-1"); !reflect.DeepEqual(got, []string{"run the tests", "ship it"}) {
		t.Fatalf("alternative choices = %#v, want [run the tests, ship it]", got)
	}
}

func TestControllerGating(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	cases := []struct {
		name       string
		opts       fakeHostOpts
		event      map[string]any
		settingFn  func(sdk.Context, string) any
		subagentFn func(sdk.Context) bool
	}{
		{
			name:  "non-UI print mode",
			opts:  fakeHostOpts{Mode: "print", HasUI: boolPtr(false)},
			event: doneAgentEndEvent(),
		},
		{
			name:  "RPC mode with HasUI=true",
			opts:  fakeHostOpts{Mode: "rpc", HasUI: boolPtr(true)},
			event: doneAgentEndEvent(),
		},
		{
			name:       "subagent session",
			opts:       fakeHostOpts{},
			event:      doneAgentEndEvent(),
			subagentFn: func(sdk.Context) bool { return true },
		},
		{
			name:  "composer.predictions disabled",
			opts:  fakeHostOpts{},
			event: doneAgentEndEvent(),
			settingFn: func(_ sdk.Context, key string) any {
				if key == SettingPredictions {
					return false
				}
				return nil
			},
		},
		{
			name:  "non-empty editor draft",
			opts:  fakeHostOpts{EditorText: "my draft"},
			event: doneAgentEndEvent(),
		},
		{
			name:  "whitespace editor draft",
			opts:  fakeHostOpts{EditorText: "   "},
			event: doneAgentEndEvent(),
		},
		{
			name:  "no active model",
			opts:  fakeHostOpts{NoModel: true},
			event: doneAgentEndEvent(),
		},
		{
			name:  "no assistant message in branch",
			opts:  fakeHostOpts{Branch: []map[string]any{{"id": "e1", "type": "message", "message": map[string]any{"role": "user", "content": "hi"}}}},
			event: doneAgentEndEvent(),
		},
		{
			name: "error stopReason",
			opts: fakeHostOpts{},
			event: map[string]any{
				"messages": []any{
					map[string]any{"role": "assistant", "stopReason": "error"},
				},
			},
		},
		{
			name: "aborted stopReason",
			opts: fakeHostOpts{},
			event: map[string]any{
				"messages": []any{
					map[string]any{"role": "assistant", "stopReason": "aborted"},
				},
			},
		},
		{
			name:  "interrupted flag",
			opts:  fakeHostOpts{},
			event: map[string]any{"interrupted": true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var called atomic.Int32
			ctrl, h := newTestSetup(t, tc.opts, true, func(_ context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
				called.Add(1)
				return assistantReplyJSON("next step"), nil
			})
			Setting = tc.settingFn
			IsSubagent = tc.subagentFn

			h.fireEvent(sdk.EventAgentEnd, tc.event)
			ctrl.Wait()

			if called.Load() != 0 {
				t.Fatalf("expected 0 complete calls, got %d", called.Load())
			}
			if len(h.widgetCalls()) != 0 {
				t.Fatalf("expected 0 widget calls, got %+v", h.widgetCalls())
			}
		})
	}
}

func TestControllerPlainTerminalNeverEmitsWidget(t *testing.T) {
	ctrl, h := newTestSetup(t, fakeHostOpts{}, false, func(_ context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		return assistantReplyJSON("run tests"), nil
	})
	h.fireEvent(sdk.EventAgentEnd, doneAgentEndEvent())
	ctrl.Wait()

	if got := ctrl.Choices("sess-pred-1"); !reflect.DeepEqual(got, []string{"run tests"}) {
		t.Fatalf("choices = %#v, want [run tests]", got)
	}
	if len(h.widgetCalls()) != 0 {
		t.Fatalf("plain terminal must not emit SetWidget, got %+v", h.widgetCalls())
	}
}

// ── Concurrency, Cancellation, Staleness, Deadline Tests ─────────────────────

func TestControllerSupersedesAndDiscardsStaleResponse(t *testing.T) {
	firstStarted := make(chan struct{})
	firstGate := make(chan struct{})
	var callNum atomic.Int32

	ctrl, h := newTestSetup(t, fakeHostOpts{}, true, func(goCtx context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		n := callNum.Add(1)
		if n == 1 {
			close(firstStarted)
			<-firstGate
			// Simulate a slow provider that ignores context and returns late.
			return assistantReplyJSON("stale first"), nil
		}
		return assistantReplyJSON("fresh second"), nil
	})

	h.fireEvent(sdk.EventAgentEnd, doneAgentEndEvent())
	<-firstStarted

	// Second agent_end supersedes the first.
	h.fireEvent(sdk.EventAgentEnd, doneAgentEndEvent())
	close(firstGate)
	ctrl.Wait()

	if got := ctrl.Choices("sess-pred-1"); !reflect.DeepEqual(got, []string{"fresh second"}) {
		t.Fatalf("choices = %#v, want [fresh second]", got)
	}
}

func TestControllerCancelsAndClearsWidgetOnLifecycleEvents(t *testing.T) {
	events := []string{
		sdk.EventInput,
		sdk.EventAgentStart,
		sdk.EventBeforeAgentStart,
		sdk.EventSessionCompact,
		sdk.EventSessionTree,
		sdk.EventSessionShutdown,
	}
	for _, ev := range events {
		t.Run(ev, func(t *testing.T) {
			ctrl, h := newTestSetup(t, fakeHostOpts{}, true, func(_ context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
				return assistantReplyJSON("ship it"), nil
			})
			h.fireEvent(sdk.EventAgentEnd, doneAgentEndEvent())
			ctrl.Wait()

			if got := ctrl.Choices("sess-pred-1"); len(got) != 1 {
				t.Fatalf("expected active prediction before %s, got %#v", ev, got)
			}

			evData := map[string]any{}
			if ev == sdk.EventBeforeAgentStart {
				evData["systemPrompt"] = "You are a helpful coding assistant."
				evData["systemPromptOptions"] = map[string]any{}
			}
			h.fireEvent(ev, evData)

			if got := ctrl.Choices("sess-pred-1"); got != nil {
				t.Fatalf("expected cleared prediction after %s, got %#v", ev, got)
			}
			wCalls := h.waitWidgetCalls(2)
			if len(wCalls) < 2 {
				t.Fatalf("expected publish + clear widget calls for %s, got %+v", ev, wCalls)
			}
			if wCalls[0].Method != "widget_push" || wCalls[0].Args["key"] != WidgetKey || wCalls[0].Args["view"] == nil {
				t.Fatalf("expected initial widget_push with view, got %+v", wCalls[0])
			}
			last := wCalls[len(wCalls)-1]
			if last.Method != "ui.setWidget" || last.Args["key"] != WidgetKey || last.Args["content"] != nil {
				t.Fatalf("expected clear ui.setWidget call after %s, got %+v", ev, last)
			}
		})
	}
}

func TestControllerCancelsInFlightRequestOnInput(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})

	ctrl, h := newTestSetup(t, fakeHostOpts{}, true, func(goCtx context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		close(started)
		<-goCtx.Done()
		close(cancelled)
		return nil, goCtx.Err()
	})

	h.fireEvent(sdk.EventAgentEnd, doneAgentEndEvent())
	<-started
	h.fireEvent(sdk.EventInput, map[string]any{"text": "user typed"})

	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight prediction was not cancelled on EventInput")
	}
	ctrl.Wait()
	if got := ctrl.Choices("sess-pred-1"); got != nil {
		t.Fatalf("expected nil choices after cancel, got %#v", got)
	}
}

func TestControllerDeadlineTimeoutCleansUp(t *testing.T) {
	timedOut := make(chan struct{})
	ctrl, h := newTestSetup(t, fakeHostOpts{}, true, func(goCtx context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		<-goCtx.Done()
		close(timedOut)
		return nil, goCtx.Err()
	})
	ctrl.deadline = 30 * time.Millisecond

	h.fireEvent(sdk.EventAgentEnd, doneAgentEndEvent())
	select {
	case <-timedOut:
	case <-time.After(2 * time.Second):
		t.Fatal("prediction did not time out at deadline")
	}
	ctrl.Wait()
	if got := ctrl.Choices("sess-pred-1"); got != nil {
		t.Fatalf("expected nil choices after timeout, got %#v", got)
	}
	if len(h.widgetCalls()) != 0 {
		t.Fatalf("expected 0 widget calls on timeout, got %+v", h.widgetCalls())
	}
}

func TestControllerDropsReplyIfEditorOrBranchChangedMidFlight(t *testing.T) {
	t.Run("editor draft typed mid-flight", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		ctrl, h := newTestSetup(t, fakeHostOpts{}, true, func(_ context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
			close(started)
			<-release
			return assistantReplyJSON("run tests"), nil
		})
		h.fireEvent(sdk.EventAgentEnd, doneAgentEndEvent())
		<-started
		h.setEditorText("actually do X")
		close(release)
		ctrl.Wait()
		if got := ctrl.Choices("sess-pred-1"); got != nil {
			t.Fatalf("expected nil choices when editor changed mid-flight, got %#v", got)
		}
	})

	t.Run("branch advanced mid-flight", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		ctrl, h := newTestSetup(t, fakeHostOpts{}, true, func(_ context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
			close(started)
			<-release
			return assistantReplyJSON("run tests"), nil
		})
		h.fireEvent(sdk.EventAgentEnd, doneAgentEndEvent())
		<-started
		h.appendBranch(map[string]any{"id": "e3", "type": "message", "message": map[string]any{"role": "user", "content": "something else"}})
		close(release)
		ctrl.Wait()
		if got := ctrl.Choices("sess-pred-1"); got != nil {
			t.Fatalf("expected nil choices when branch advanced mid-flight, got %#v", got)
		}
	})
}

func TestControllerCompactedHistoryRealToolMessagesAndActiveToolCatalog(t *testing.T) {
	// Build a compacted branch where e1/e2 are before firstKeptEntryId (e3),
	// e3/e4 contain real toolCall + toolResult blocks, e5 is the compaction
	// entry, and e6/e7 are post-compaction messages.
	branch := []map[string]any{
		{"id": "e1", "type": "message", "message": map[string]any{"role": "user", "content": "ancient compacted prompt"}},
		{"id": "e2", "type": "message", "message": map[string]any{"role": "assistant", "content": "ancient compacted reply"}},
		{
			"id": "e3", "type": "message",
			"message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "text", "text": "Checking main.go"},
					map[string]any{"type": "toolCall", "id": "call_1", "name": "read", "arguments": map[string]any{"path": "main.go"}},
				},
			},
		},
		{
			"id": "e4", "type": "message",
			"message": map[string]any{
				"role":       "toolResult",
				"toolCallId": "call_1",
				"toolName":   "read",
				"content":    []any{map[string]any{"type": "text", "text": "package main"}},
				"isError":    false,
			},
		},
		{
			"id":               "e5",
			"type":             "compaction",
			"summary":          "Earlier we discussed auth refactoring.",
			"firstKeptEntryId": "e3",
			"tokensBefore":     42000,
		},
		{"id": "e6", "type": "message", "message": map[string]any{"role": "user", "content": "now fix the bug"}},
		{"id": "e7", "type": "message", "message": map[string]any{"role": "assistant", "content": "Fixed in main.go."}},
	}

	var capturedReq map[string]any
	ctrl, h := newTestSetup(t, fakeHostOpts{
		Branch:      branch,
		ActiveTools: []string{"bash", "read"},
	}, true, func(_ context.Context, _ sdk.Context, _, req, _ map[string]any) (json.RawMessage, error) {
		capturedReq = req
		return assistantReplyJSON("run go test"), nil
	})

	h.fireEvent(sdk.EventAgentEnd, doneAgentEndEvent())
	ctrl.Wait()

	if got := ctrl.Choices("sess-pred-1"); !reflect.DeepEqual(got, []string{"run go test"}) {
		t.Fatalf("choices = %#v, want [run go test]", got)
	}

	// 1. Verify active tools are forwarded in active order, excluding inactive tools.
	rawTools, ok := capturedReq["tools"].([]map[string]any)
	if !ok || len(rawTools) != 2 {
		t.Fatalf("tools = %#v, want 2 active tools", capturedReq["tools"])
	}
	if rawTools[0]["name"] != "bash" || rawTools[1]["name"] != "read" {
		t.Fatalf("tool order = [%v, %v], want [bash, read]", rawTools[0]["name"], rawTools[1]["name"])
	}
	if capturedReq["toolChoice"] != "none" {
		t.Fatalf("toolChoice = %v, want none when tools present", capturedReq["toolChoice"])
	}

	// 2. Verify compacted messages were dropped, compaction summary was emitted
	// first, and real toolCall + toolResult blocks were preserved verbatim.
	msgs, ok := capturedReq["messages"].([]map[string]any)
	if !ok {
		t.Fatalf("messages type = %T, want []map[string]any", capturedReq["messages"])
	}
	// Expected sequence:
	// [0] compactionSummary -> user text with <summary>
	// [1] e3 assistant with real toolCall block
	// [2] e4 toolResult with toolCallId="call_1"
	// [3] e6 user "now fix the bug"
	// [4] e7 assistant "Fixed in main.go."
	// [5] <composer-prediction> prompt
	if len(msgs) != 6 {
		t.Fatalf("len(messages) = %d, want 6; msgs=%#v", len(msgs), msgs)
	}
	encoded, _ := json.Marshal(msgs)
	if strings.Contains(string(encoded), "ancient compacted") {
		t.Fatalf("pre-compaction messages leaked into request: %s", string(encoded))
	}
	if msgs[0]["role"] != "user" {
		t.Fatalf("msgs[0].role = %v, want user (compaction summary)", msgs[0]["role"])
	}
	c0Blocks, ok := msgs[0]["content"].([]map[string]any)
	if !ok || len(c0Blocks) != 1 {
		t.Fatalf("msgs[0].content = %#v, want 1 text block", msgs[0]["content"])
	}
	c0Text, _ := c0Blocks[0]["text"].(string)
	if !strings.Contains(c0Text, "Earlier we discussed auth refactoring.") || !strings.Contains(c0Text, "<summary>") {
		t.Fatalf("msgs[0] missing compaction summary wrapper: %q", c0Text)
	}
	if msgs[1]["role"] != "assistant" {
		t.Fatalf("msgs[1].role = %v, want assistant", msgs[1]["role"])
	}
	c1, _ := json.Marshal(msgs[1]["content"])
	if !strings.Contains(string(c1), `"type":"toolCall"`) || !strings.Contains(string(c1), `"call_1"`) {
		t.Fatalf("msgs[1] did not preserve real toolCall block: %s", string(c1))
	}
	if msgs[2]["role"] != "toolResult" || msgs[2]["toolCallId"] != "call_1" {
		t.Fatalf("msgs[2] did not preserve real toolResult message: %#v", msgs[2])
	}
}

func TestControllerOmitsToolChoiceWhenNoActiveTools(t *testing.T) {
	var capturedReq map[string]any
	ctrl, h := newTestSetup(t, fakeHostOpts{
		ActiveTools: []string{},
	}, true, func(_ context.Context, _ sdk.Context, _, req, _ map[string]any) (json.RawMessage, error) {
		capturedReq = req
		return assistantReplyJSON("next"), nil
	})

	h.fireEvent(sdk.EventAgentEnd, doneAgentEndEvent())
	ctrl.Wait()

	if _, hasTools := capturedReq["tools"]; hasTools {
		t.Fatalf("expected no tools key when ActiveTools is empty, got %#v", capturedReq["tools"])
	}
	if _, hasTC := capturedReq["toolChoice"]; hasTC {
		t.Fatalf("expected toolChoice omitted when tools is empty, got %#v", capturedReq["toolChoice"])
	}
}

func TestControllerSerializesPublishAndCancelUnderRace(t *testing.T) {
	publishEntered := make(chan struct{})
	releasePublish := make(chan struct{})

	var opsMu sync.Mutex
	var ops []string

	ctrl, h := newTestSetup(t, fakeHostOpts{}, true, func(_ context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		return assistantReplyJSON("run tests"), nil
	})
	ctrl.publishHook = func(ctx sdk.Context, choices []string) {
		close(publishEntered)
		<-releasePublish
		opsMu.Lock()
		ops = append(ops, "publish")
		opsMu.Unlock()
		publishWidget(ctx, choices)
	}
	ctrl.clearHook = func(ctx sdk.Context) {
		opsMu.Lock()
		ops = append(ops, "clear")
		opsMu.Unlock()
		clearWidget(ctx)
	}

	h.fireEvent(sdk.EventAgentEnd, doneAgentEndEvent())
	// Wait until the prediction goroutine has acquired widgetMu and entered publishHook.
	<-publishEntered

	// Trigger Cancel concurrently (via EventInput) while publish is mid-flight.
	cancelDone := make(chan struct{})
	go func() {
		h.fireEvent(sdk.EventInput, map[string]any{"text": "override"})
		close(cancelDone)
	}()

	// Give Cancel a brief moment to contend on widgetMu, then release publish.
	time.Sleep(15 * time.Millisecond)
	close(releasePublish)

	<-cancelDone
	ctrl.Wait()

	// Because widgetMu serializes publish and clear, clear MUST run AFTER publish,
	// never leaving a stale published widget behind.
	opsMu.Lock()
	gotOps := append([]string(nil), ops...)
	opsMu.Unlock()
	if !reflect.DeepEqual(gotOps, []string{"publish", "clear"}) {
		t.Fatalf("widget ops order = %#v, want [publish, clear]", gotOps)
	}
	if got := ctrl.Choices("sess-pred-1"); got != nil {
		t.Fatalf("choices after concurrent cancel = %#v, want nil", got)
	}
	wCalls := h.waitWidgetCalls(2)
	last := wCalls[len(wCalls)-1]
	if last.Method != "ui.setWidget" || last.Args["key"] != WidgetKey || last.Args["content"] != nil {
		t.Fatalf("final widget call must be clear ui.setWidget(nil), got %+v", last)
	}
}

func TestControllerClearsWidgetUnconditionallyAfterTernFallback(t *testing.T) {
	ctrl, h := newTestSetup(t, fakeHostOpts{}, true, func(_ context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		return assistantReplyJSON("ship it"), nil
	})
	h.fireEvent(sdk.EventAgentEnd, doneAgentEndEvent())
	ctrl.Wait()

	wCalls := h.waitWidgetCalls(1)
	if len(wCalls) != 1 || wCalls[0].Method != "widget_push" {
		t.Fatalf("expected initial widget_push, got %+v", wCalls)
	}

	// Simulate Tern -> plain-terminal fallback before Cancel.
	surface.SetNative(false)
	h.fireEvent(sdk.EventInput, map[string]any{"text": "typed after fallback"})

	wCalls = h.waitWidgetCalls(2)
	if len(wCalls) < 2 {
		t.Fatalf("expected clearWidget to run even after surface.SetNative(false), got %+v", wCalls)
	}
	last := wCalls[len(wCalls)-1]
	if last.Method != "ui.setWidget" || last.Args["key"] != WidgetKey || last.Args["content"] != nil {
		t.Fatalf("expected clear ui.setWidget call after fallback, got %+v", last)
	}
}
