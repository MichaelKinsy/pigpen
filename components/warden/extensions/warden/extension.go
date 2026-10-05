// Package warden is a Go port of DevMortimer's pi-warden (MIT, pinned a12b2703): guardrails that steer
// instead of interrupt. It reads each tool call before it runs and holds the irreversible ones, tells the
// agent when a call is off-task or off-plan, notices stuck loops and repeated calls, and asks the agent to
// verify when it claims a change is done and nothing checked it.
//
// It is opt-in (`/warden enable`), discloses what leaves the machine before anything does, and judges with
// either TypeSafe's Jev or the session's own model; with neither it still runs its offline pattern checks.
package warden

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	steerType  = "pi-warden-steer" // the original's customType, so a session that holds its steers reads the same on either
	statusType = "pigpen-warden-status"
	statusKey  = "pigpen-warden"
	widgetKey  = "pigpen-warden"
)

// Options configure the extension; the zero value is the real thing. Tests inject the judge and git.
type Options struct {
	Getenv func(string) string
	Git    GitRunner
	// NewJudge builds the judge for the configured backend; nil uses the real backends.
	NewJudge func(ctx sdk.Context, cfg Config, budget *Budget, getenv func(string) string) (Judge, error)
	Now      func() time.Time
}

type stats struct {
	Inspected, Judged, Held, Warned, Approved, Errors    int
	Steers, SteersSkipped, Stuck, DoneChecks, Unverified int
}

type traceEntry struct {
	At   time.Time
	Line string
}

type warden struct {
	opts Options

	mu     sync.Mutex
	loaded bool
	cfg    Config
	// fileCfg is the configuration file's content, without the environment's overrides: what a command saves.
	fileCfg  Config
	cfgPath  string
	loadErr  string
	guard    *ActionGuard
	window   *AttemptWindow
	evidence *RunEvidence
	budget   *Budget
	repeats  *SteerRepeatWindow

	steersThisRun int
	doneNudged    bool
	continuation  bool
	lastPrompt    string
	noted         map[string]bool
	stats         stats
	trace         []traceEntry
}

// Extension returns the guard.
func Extension() *sdk.Extension { return New(Options{}) }

// New returns the guard with injectable parts.
func New(o Options) *sdk.Extension {
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	w := &warden{opts: o, guard: NewActionGuard(), evidence: EmptyEvidence(), budget: &Budget{}, repeats: NewSteerRepeatWindow(), noted: map[string]bool{}}
	e := sdk.New("warden")
	e.Command("warden", "Guardrails that steer: /warden [status|enable|disable|mode|backend|test|trace]", w.command)
	e.OnEvent(sdk.EventSessionStart, w.onSessionStart)
	e.OnEvent(sdk.EventBeforeAgentStart, w.onBeforeAgentStart)
	e.OnEvent(sdk.EventAgentStart, w.onAgentStart)
	e.OnEvent(sdk.EventTurnEnd, func(sdk.Context, map[string]any) (any, error) { w.guard.TurnEnd(); return nil, nil })
	e.OnEvent(sdk.EventToolCall, w.onToolCall)
	e.OnEvent(sdk.EventToolResult, w.onToolResult)
	e.OnEvent(sdk.EventAgentEnd, w.onAgentEnd)
	e.OnEvent(sdk.EventSessionShutdown, func(sdk.Context, map[string]any) (any, error) { w.guard.Wait(); return nil, nil })
	return e
}

// ---------------------------------------------------------------------------
// Configuration.

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// load reads the file and applies the environment: PIGPEN_WARDEN_ENABLED turns warden on for a headless run,
// PIGPEN_WARDEN_BACKEND names its backend (setting it is the consent), PIGPEN_WARDEN_MODE picks the mode, PIGPEN_WARDEN_STEER_VISIBLE=0|1 hides or shows steers in the transcript (default shown).
func (w *warden) load(ctx sdk.Context) {
	path := ConfigPath(ctx.ConfigHome())
	cfg, err := LoadConfig(path)
	w.cfgPath, w.loadErr = path, ""
	if err != nil {
		w.loadErr = "warden: could not read " + path + ": " + err.Error() + ". Defaults are in use."
	}
	w.fileCfg = cfg
	if truthy(w.opts.Getenv("PIGPEN_WARDEN_ENABLED")) {
		cfg.Enabled = true
		if b := strings.ToLower(strings.TrimSpace(w.opts.Getenv("PIGPEN_WARDEN_BACKEND"))); b == BackendTypeSafe || b == BackendOwnModel {
			cfg.Backend, cfg.Consent = b, b
		}
	}
	if m := strings.ToLower(strings.TrimSpace(w.opts.Getenv("PIGPEN_WARDEN_MODE"))); m == "steer" || m == "confirm" || m == "advise" {
		cfg.Mode = m
	}
	if v := strings.ToLower(strings.TrimSpace(w.opts.Getenv("PIGPEN_WARDEN_STEER_VISIBLE"))); v != "" {
		cfg.SteerVisible = truthy(v)
	}
	w.cfg, w.loaded = cfg, true
	w.budget.Max = cfg.MaxRequests
}

func (w *warden) config(ctx sdk.Context) Config {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.loaded {
		w.load(ctx)
	}
	return w.cfg
}

// update applies a command's change to the running configuration and to the file, and saves the file. The
// environment's overrides (PIGPEN_WARDEN_*) stay in the running configuration only: they are that run's choice
// and consent, never written to disk.
func (w *warden) update(ctx sdk.Context, change func(*Config)) (Config, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.loaded {
		w.load(ctx)
	}
	file := w.fileCfg
	change(&file)
	if err := SaveConfig(w.cfgPath, file); err != nil {
		return w.cfg, err
	}
	w.fileCfg = file
	change(&w.cfg)
	w.budget.Max = w.cfg.MaxRequests
	return w.cfg, nil
}

// judgeFor is the judge for the configured backend, or nil for offline operation (no backend, no consent, or a
// backend that cannot start: the reason is noted once).
func (w *warden) judgeFor(ctx sdk.Context, cfg Config) Judge {
	if !cfg.Enabled || cfg.Backend == BackendNone {
		return nil
	}
	if cfg.Consent != cfg.Backend {
		w.noteOnce(ctx, "consent", "warden: the "+cfg.Backend+" backend was not agreed to; running offline. Run /warden enable to review what it sends and turn it on.")
		return nil
	}
	build := w.opts.NewJudge
	if build == nil {
		build = defaultJudge
	}
	j, err := build(ctx, cfg, w.budget, w.opts.Getenv)
	if err != nil {
		w.noteOnce(ctx, "judge:"+err.Error(), "warden: "+err.Error()+" Running offline patterns only.")
		return nil
	}
	return j
}

func defaultJudge(ctx sdk.Context, cfg Config, budget *Budget, getenv func(string) string) (Judge, error) {
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	switch cfg.Backend {
	case BackendTypeSafe:
		j, err := NewTypeSafeJudge(timeout, budget, getenv)
		if err != nil {
			return nil, err
		}
		return j, nil
	case BackendOwnModel:
		info, err := ctx.GetModelInfo()
		if err != nil {
			return nil, &IntegrationError{Code: "configuration", Message: "The session model could not be read: " + err.Error()}
		}
		if info == nil {
			return nil, &IntegrationError{Code: "configuration", Message: "No session model is selected."}
		}
		j, err := NewOwnModelJudge(ctx.ModelRegistry(), info.Provider, info.ID, budget)
		if err != nil {
			return nil, err
		}
		return j, nil
	}
	return nil, nil
}

func (w *warden) noteOnce(ctx sdk.Context, key, text string) {
	w.mu.Lock()
	seen := w.noted[key]
	w.noted[key] = true
	w.mu.Unlock()
	if seen {
		return
	}
	w.say(ctx, text, "warning")
}

// say shows the user a notice: a notification in a UI, a visible message otherwise.
func (w *warden) say(ctx sdk.Context, text, level string) {
	if ctx.HasUI() {
		ctx.Notify(text, level)
		return
	}
	no := false
	_ = ctx.SendMessage(statusType, text, true, sdk.SendMessageOptions{TriggerTurn: &no})
}

func (w *warden) timeout(cfg Config) time.Duration {
	return time.Duration(cfg.TimeoutMs) * time.Millisecond
}

func (w *warden) mode(cfg Config, ctx sdk.Context) string {
	if cfg.Mode == "confirm" && !ctx.HasUI() {
		return "steer"
	}
	return cfg.Mode
}

// ---------------------------------------------------------------------------
// Session and run lifecycle.

func (w *warden) onSessionStart(ctx sdk.Context, _ map[string]any) (any, error) {
	w.mu.Lock()
	w.load(ctx)
	w.window = NewAttemptWindow(w.cfg.Stuck.Window)
	w.evidence = EmptyEvidence()
	w.stats = stats{}
	w.trace = nil
	w.noted = map[string]bool{}
	w.steersThisRun, w.doneNudged, w.continuation, w.lastPrompt = 0, false, false, ""
	w.repeats.Reset()
	w.budget.Reset()
	loadErr, enabled := w.loadErr, w.cfg.Enabled
	w.mu.Unlock()
	w.guard.Reset()
	if loadErr != "" {
		w.say(ctx, loadErr, "warning")
	}
	if enabled && !ctx.HasUI() {
		w.disclose(ctx)
	}
	w.refresh(ctx)
	return nil, nil
}

// disclose prints the data flow once for a run that has no dialog to consent in.
func (w *warden) disclose(ctx sdk.Context) {
	cfg := w.config(ctx)
	target := w.targetOf(ctx, cfg.Backend)
	w.say(ctx, "warden is on ("+describeBackend(cfg.Backend, target)+", "+cfg.Mode+" mode).\n\n"+Disclosure(cfg.Backend, target), "info")
}

func (w *warden) onBeforeAgentStart(ctx sdk.Context, data map[string]any) (any, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if p, ok := data["prompt"].(string); ok && strings.TrimSpace(p) != "" {
		w.lastPrompt = p
	}
	if w.continuation {
		return nil, nil
	}
	// A new user prompt: the loop window, the steer repeats and the done nudge start over.
	if w.window != nil {
		w.window.Reset()
	}
	w.repeats.Reset()
	w.doneNudged = false
	w.steersThisRun = 0
	return nil, nil
}

func (w *warden) onAgentStart(ctx sdk.Context, _ map[string]any) (any, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.continuation {
		w.evidence = EmptyEvidence()
		w.steersThisRun = 0
	}
	w.continuation = false
	return nil, nil
}

// ---------------------------------------------------------------------------
// The branch: what the session says about a call.

func branchText(e sdk.BranchEntry) string { return strings.TrimSpace(e.Content) }

func toolArgs(raw string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// conversationOf reads the branch: the latest user prompt (approval evidence), the earlier messages (scope
// context), the spine, the tool calls of the assistant message that made this one, and its words (the plan).
func conversationOf(branch []sdk.BranchEntry, callID, fallbackTask string, call ToolCallRef) Conversation {
	var msgs []BranchMessage
	for _, e := range branch {
		if e.Type == "message" {
			msgs = append(msgs, BranchMessage{Type: "message", Role: e.Role, Text: e.Content})
		}
	}
	task := ""
	latestUser := -1
	for i := len(branch) - 1; i >= 0; i-- {
		if branch[i].Type == "message" && branch[i].Role == "user" && branchText(branch[i]) != "" {
			task, latestUser = branchText(branch[i]), i
			break
		}
	}
	if task == "" {
		task = strings.TrimSpace(fallbackTask)
	}
	conv := Conversation{Task: task, Spine: TaskSpineOf(msgs, task)}
	for i := latestUser - 1; i >= 0 && len(conv.Context) < 8; i-- {
		e := branch[i]
		if e.Type != "message" || (e.Role != "user" && e.Role != "assistant") || branchText(e) == "" {
			continue
		}
		conv.Context = append([]TaskMessage{{Role: e.Role, Text: e.Content}}, conv.Context...)
	}
	conv.Siblings = []ToolCallRef{call}
	for i := len(branch) - 1; i >= 0; i-- {
		e := branch[i]
		if e.Type != "message" || e.Role != "assistant" {
			continue
		}
		found := false
		for _, tc := range e.ToolCalls {
			if tc.ID == callID {
				found = true
			}
		}
		if !found {
			continue
		}
		conv.Plan = e.Content
		conv.Siblings = nil
		for _, tc := range e.ToolCalls {
			if tc.ID == callID {
				conv.Siblings = append(conv.Siblings, call)
			} else {
				conv.Siblings = append(conv.Siblings, ToolCallRef{ID: tc.ID, Tool: tc.Name, Input: toolArgs(tc.Args)})
			}
		}
		break
	}
	return conv
}

func newContext() context.Context { return context.Background() }
