package pi_goal_x_test

// goalRig is the Go twin of the original tests' createHarness (tests/goal-*.test.ts): the whole extension behind the REAL SDK, through
// the Skill's fake host (fakehost_test.go). A slice's twins start a rig, drive it, and read back what the host saw.
//
//	rig := newGoalRig(t, rigOptions{Branch: []any{...}, Seed: map[string]string{...}})
//	rig.Fire("session_start", map[string]any{"reason": "startup"})   // pi.on handlers (all of them; Fire reaches the last one registered)
//	rig.Command("goal-direct", "Ship it")                              // slash commands; returns the error text
//	out, failure := rig.Tool("get_goal", nil)                          // registered tools
//	rig.Notices() / rig.Entries() / rig.Sent() / rig.Statuses() / rig.Widgets() / rig.ActiveTools()
//	rig.Probe.Goal() / FocusedGoalID() / Scheduler*() / FakeClock(...)  // core internals (export_test.go)
//
// What the host answers (all of it overridable per option): the session id, the branch (an entry the extension appends is visible to
// the next read, as in a real session), idle and pending-message state, select/confirm/input answers (scripts are consulted in order;
// an exhausted script cancels), the active tool list (setActiveTools replaces it), context usage and the model. Widget lines the
// extension pushes are recorded as Widgets() (pushes and clears, in order); a host in RPC mode drops pushes, as Pi's does. Options
// the rig does not model go to OnCall. The rig starts no session: fire session_start, or set StartSession.
//
// Everything is read under the rig's lock; the host answers from other goroutines.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

// rigOptions configures newGoalRig. The zero value is a TUI session with UI, an idle agent, no goals and maxAutonomousRuns 0.
type rigOptions struct {
	Cwd       string
	Branch    []any // sessionEntries: the session branch getBranch serves
	HasUI     *bool
	Mode      string // "tui" (default), "rpc", "json", "print"
	SessionID string // default "rig-session"
	// Settings is .pi/pi-goal-x-settings.json; "" writes {"maxAutonomousRuns": 0}, "-" writes none.
	Settings string
	Seed     map[string]string // files under cwd (rel path -> content)
	Env      map[string]string // t.Setenv

	// Idle answers isIdle (nil: idle); Pending hasPendingMessages (nil: none).
	Idle    func() bool
	Pending func() bool
	// Select, Confirm and Input script the dialogs; nil answers cancel / false.
	Select  func(title string, options []string) (string, bool)
	Confirm func(title, message string) bool
	Input   func(title, placeholder string) (string, bool)
	// Custom answers ui.custom (the SDK renders the component locally; this is the host's verdict): nil answers "closed, no result".
	Custom func(args map[string]any) map[string]any
	// OnSendMessage sees every pi.sendMessage (message and options); its result is not used.
	OnSendMessage func(message, options map[string]any)
	ActiveTools   []string // default read, write, bash
	ContextUsage  map[string]any
	Model         map[string]any // getModelInfo answer; nil: no model
	OnCall        func(method string, args map[string]any) (any, string)

	Deps         goal.RigDeps
	StartSession bool
}

type rigEntry struct {
	CustomType string
	Data       map[string]any
}

type rigSent struct{ Message, Options map[string]any }

type rigStatus struct {
	Key  string
	Text string // "" when cleared
}

// rigWidget is one widget call: Clear for ui.setWidget(key, undefined), otherwise the lines pushed.
type rigWidget struct {
	Key   string
	Clear bool
	Lines []string
}

type goalRig struct {
	t     *testing.T
	Host  *Host
	Probe *goal.Probe
	Cwd   string
	opts  rigOptions

	mu          sync.Mutex
	branch      []any
	activeTools []string
	widgets     []rigWidget
	sendHook    func(message, options map[string]any)
	sessionID   string
	model       map[string]any
}

func newGoalRig(t *testing.T, opts rigOptions) *goalRig {
	t.Helper()
	r := &goalRig{t: t, opts: opts, branch: append([]any{}, opts.Branch...), activeTools: opts.ActiveTools, sendHook: opts.OnSendMessage, model: opts.Model}
	if r.activeTools == nil {
		r.activeTools = []string{"read", "write", "bash"}
	}
	if opts.SessionID == "" {
		opts.SessionID = "rig-session"
	}
	r.sessionID = opts.SessionID
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	for k, v := range opts.Env {
		t.Setenv(k, v)
	}
	cwd := opts.Cwd
	if cwd == "" {
		cwd = t.TempDir()
	}
	r.Cwd = cwd
	write := func(rel, text string) {
		p := filepath.Join(cwd, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	switch opts.Settings {
	case "-":
	case "":
		write(".pi/pi-goal-x-settings.json", `{"maxAutonomousRuns": 0}`)
	default:
		write(".pi/pi-goal-x-settings.json", opts.Settings)
	}
	for rel, text := range opts.Seed {
		write(rel, text)
	}
	ext, probe := goal.NewGoalExtensionForTest(opts.Deps)
	r.Probe = probe
	mode := opts.Mode
	if mode == "" {
		mode = "tui"
	}
	r.Host = StartHost(t, ext, HostOptions{Mode: mode, HasUI: opts.HasUI, Cwd: cwd, OnCallValue: r.answer})
	if probe != nil {
		probe.WidgetTap(func(key string, lines []string) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.widgets = append(r.widgets, rigWidget{Key: key, Lines: append([]string{}, lines...)})
		})
	}
	if opts.StartSession {
		r.Fire("session_start", map[string]any{"reason": "startup"})
	}
	return r
}

func (r *goalRig) answer(method string, args map[string]any) (any, string) {
	o := r.opts
	switch method {
	case "getSessionID":
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.sessionID == "-" { // a host without a session id
			return map[string]any{}, ""
		}
		return map[string]any{"sessionId": r.sessionID}, ""
	case "getSessionFile":
		return map[string]any{"sessionFile": ""}, ""
	case "isIdle":
		idle := true
		if o.Idle != nil {
			idle = o.Idle()
		}
		return map[string]any{"idle": idle}, ""
	case "hasPendingMessages":
		pending := false
		if o.Pending != nil {
			pending = o.Pending()
		}
		return map[string]any{"pending": pending}, ""
	case "sessionRead":
		if args["method"] == "getBranch" {
			r.mu.Lock()
			defer r.mu.Unlock()
			return append([]any{}, r.branch...), ""
		}
	case "appendEntry":
		r.mu.Lock()
		defer r.mu.Unlock()
		r.branch = append(r.branch, map[string]any{"type": "custom", "customType": args["customType"], "data": args["data"]})
		return map[string]any{}, ""
	case "getActiveTools":
		r.mu.Lock()
		defer r.mu.Unlock()
		return map[string]any{"tools": append([]string{}, r.activeTools...)}, ""
	case "setActiveTools":
		r.mu.Lock()
		defer r.mu.Unlock()
		r.activeTools = nil
		for _, n := range args["tools"].([]any) {
			r.activeTools = append(r.activeTools, n.(string))
		}
		return map[string]any{}, ""
	case "getContextUsage":
		if o.ContextUsage == nil {
			return nil, "no usage"
		}
		return o.ContextUsage, ""
	case "getModelInfo":
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.model == nil {
			return map[string]any{}, ""
		}
		return r.model, ""
	case "sendMessage":
		r.mu.Lock()
		hook := r.sendHook
		r.mu.Unlock()
		if hook != nil {
			msg, _ := args["message"].(map[string]any)
			opt, _ := args["options"].(map[string]any)
			hook(msg, opt)
		}
		return map[string]any{}, ""
	case "ui.select":
		if o.Select == nil {
			return map[string]any{"selected": "", "ok": false}, ""
		}
		opts, _ := args["options"].([]any)
		list := make([]string, len(opts))
		for i, v := range opts {
			list[i], _ = v.(string)
		}
		v, ok := o.Select(str(args["title"]), list)
		return map[string]any{"selected": v, "ok": ok}, ""
	case "ui.confirm":
		ok := false
		if o.Confirm != nil {
			ok = o.Confirm(str(args["title"]), str(args["message"]))
		}
		return map[string]any{"confirmed": ok}, ""
	case "ui.input":
		if o.Input == nil {
			return map[string]any{"text": "", "ok": false}, ""
		}
		v, ok := o.Input(str(args["title"]), str(args["placeholder"]))
		return map[string]any{"text": v, "ok": ok}, ""
	case "ui.custom":
		if o.Custom == nil {
			return map[string]any{"ok": false}, ""
		}
		return o.Custom(args), ""
	case "ui.setWidget":
		r.mu.Lock()
		if args["content"] == nil {
			r.widgets = append(r.widgets, rigWidget{Key: str(args["key"]), Clear: true})
		}
		r.mu.Unlock()
	}
	if o.OnCall != nil {
		return o.OnCall(method, args)
	}
	return map[string]any{}, ""
}

func str(v any) string { s, _ := v.(string); return s }

// Fire delivers an event to the last handler registered for it; FireAll to every one.
func (r *goalRig) Fire(event string, payload map[string]any) json.RawMessage {
	r.t.Helper()
	return r.Host.Fire(event, payload)
}
func (r *goalRig) FireAll(event string, payload map[string]any) {
	r.t.Helper()
	r.Host.FireAll(event, payload)
}

// Command runs a slash command; it returns the failure text ("" on success).
func (r *goalRig) Command(name, args string) string {
	r.t.Helper()
	return r.Host.Command(name, args)
}

// Tool runs a registered tool with params; it returns the raw result and the failure text.
func (r *goalRig) Tool(name string, params map[string]any) (json.RawMessage, string) {
	r.t.Helper()
	return r.Host.Tool(name, params)
}

// Notices are the ui.notify calls as "level: message".
func (r *goalRig) Notices() []string {
	var out []string
	for _, c := range r.Host.CallsTo("ui.notify") {
		out = append(out, str(c.Args["level"])+": "+str(c.Args["message"]))
	}
	return out
}

// LastNotice is the last notice, "" when there is none.
func (r *goalRig) LastNotice() string {
	n := r.Notices()
	if len(n) == 0 {
		return ""
	}
	return n[len(n)-1]
}

// Entries are the pi.appendEntry calls, in order.
func (r *goalRig) Entries() []rigEntry {
	var out []rigEntry
	for _, c := range r.Host.CallsTo("appendEntry") {
		d, _ := c.Args["data"].(map[string]any)
		out = append(out, rigEntry{CustomType: str(c.Args["customType"]), Data: d})
	}
	return out
}

// Sent are the pi.sendMessage calls, in order.
func (r *goalRig) Sent() []rigSent {
	var out []rigSent
	for _, c := range r.Host.CallsTo("sendMessage") {
		m, _ := c.Args["message"].(map[string]any)
		o, _ := c.Args["options"].(map[string]any)
		out = append(out, rigSent{Message: m, Options: o})
	}
	return out
}

// SentUser are the pi.sendUserMessage calls' content, in order.
func (r *goalRig) SentUser() []string {
	var out []string
	for _, c := range r.Host.CallsTo("sendUserMessage") {
		out = append(out, str(c.Args["content"]))
	}
	return out
}

// Statuses are the ui.setStatus calls, in order.
func (r *goalRig) Statuses() []rigStatus {
	var out []rigStatus
	for _, c := range r.Host.CallsTo("ui.setStatus") {
		out = append(out, rigStatus{Key: str(c.Args["key"]), Text: str(c.Args["text"])})
	}
	return out
}

// LastStatus is the last status of the goal key (found=false when none was set).
func (r *goalRig) LastStatus(key string) (text string, found bool) {
	for _, s := range r.Statuses() {
		if s.Key == key {
			text, found = s.Text, true
		}
	}
	return text, found
}

// Widgets are the widget pushes and clears, in the order the extension made them.
func (r *goalRig) Widgets() []rigWidget {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]rigWidget{}, r.widgets...)
}

// LastWidgetLines are the lines of the last push of key joined by newline ("" when the last call cleared it or none was made).
func (r *goalRig) LastWidgetLines(key string) string {
	for _, w := range reverse(r.Widgets()) {
		if w.Key == key {
			return strings.Join(w.Lines, "\n")
		}
	}
	return ""
}

func reverse[T any](s []T) []T {
	out := make([]T, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}

// ActiveTools is the active tool list as the host holds it.
func (r *goalRig) ActiveTools() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.activeTools...)
}

// Branch is the session branch as the host holds it (the seeded entries plus what the extension appended).
func (r *goalRig) Branch() []any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]any{}, r.branch...)
}

// SetSendHook replaces OnSendMessage after the rig started.
func (r *goalRig) SetSendHook(fn func(message, options map[string]any)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sendHook = fn
}

// SetSession changes the session id the host answers ("-": none), as a second ctx of another session would.
func (r *goalRig) SetSession(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionID = id
}

// SetModel changes the model the host answers (nil: none).
func (r *goalRig) SetModel(provider, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.model = map[string]any{"provider": provider, "id": id, "name": id}
}

// RunSignal tells the extension about the run in progress: active, and aborted (the user's Esc), as the host's run_signal frame.
func (r *goalRig) RunSignal(active, aborted bool) {
	r.t.Helper()
	r.Host.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "run_signal", "args": map[string]any{"run": 1, "active": active, "aborted": aborted}}})
}

// rigToolResult is a tool's wire result: its text, details and whether it ends the turn.
type rigToolResult struct {
	Text      string
	Details   map[string]any
	Terminate bool
	IsError   bool
}

func decodeToolResult(t *testing.T, raw json.RawMessage) rigToolResult {
	t.Helper()
	var w struct {
		Content   json.RawMessage `json:"content"`
		Details   map[string]any  `json:"details"`
		Terminate bool            `json:"terminate"`
		IsError   bool            `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("tool result %s: %v", raw, err)
	}
	out := rigToolResult{Details: w.Details, Terminate: w.Terminate, IsError: w.IsError}
	var text string
	if json.Unmarshal(w.Content, &text) == nil {
		out.Text = text
		return out
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(w.Content, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" {
				out.Text += b.Text
			}
		}
	}
	return out
}

// ToolResult runs a tool and decodes its result; a failure text fails the test.
func (r *goalRig) ToolResult(name string, params map[string]any) rigToolResult {
	r.t.Helper()
	raw, failure := r.Tool(name, params)
	if failure != "" {
		r.t.Fatalf("tool %s failed: %s", name, failure)
	}
	return decodeToolResult(r.t, raw)
}

// rigToolOutcome is a tool call's wire result or failure.
type rigToolOutcome struct {
	Raw     json.RawMessage
	Failure string
}

// ToolAsync starts a tool call and returns at once: the original's tests start `tool.execute(...)` without awaiting it, act on
// the session meanwhile (a command, a dialog answer) and await the call afterwards.
func (r *goalRig) ToolAsync(name string, params map[string]any) <-chan rigToolOutcome {
	if !r.Host.tools[name] {
		r.t.Fatalf("tool %q is not registered", name)
	}
	argv, _ := json.Marshal(params)
	out := make(chan rigToolOutcome, 1)
	go func() {
		raw, failure := r.Host.roundTrip(map[string]any{"method": "tool_call", "tool": name, "tool_call_id": "call-1", "args": json.RawMessage(argv)})
		out <- rigToolOutcome{raw, failure}
	}()
	return out
}
