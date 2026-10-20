package pi_subagents

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The child-side watchdog (watchdog/register-child.ts): the same runtime as the main session's, in the child, reporting status
// events to the run that started it. The original hands the child's host a status callback; a pig child is a separate process, so
// the events travel as JSON lines in a file the role names (WatchdogStatusFile) and the parent tails it (tailChildWatchdogStatus).

// childResolvedConfig is the config a child's runtime resolves to: the watchdog on, its model and bounds from the child config.
func childResolvedConfig(c childWatchdogConfig) *watchdogConfig {
	config := defaultWatchdogConfig()
	config.Enabled = true
	config.AgentEndTimeoutMs = c.AgentEndTimeoutMs
	config.MaxWarnings = c.MaxWarnings
	config.Main = wdEndpoint{Enabled: true, Model: c.Model, Thinking: c.Thinking}
	config.StalemateRepeats = c.StalemateRepeats
	config.Cadence = wdCadence{EveryNTools: c.Cadence.EveryNTools}
	config.Children.WatchdogTailTimeoutMs = c.WatchdogTailTimeoutMs
	config.Lsp = c.Lsp
	return config
}

// childWatchdogBinding is what the child watchdog sends through: warning messages and entries of the child's session.
type childWatchdogBinding struct {
	SendMessage   func(msg watchdogWarningMessage, opts *wdSendOptions)
	AppendEntry   func(customType string, details wdWarning)
	ThinkingLevel func() string
}

type childWatchdogOptions struct {
	// Review, RepoChangeSignature and LspDiagnostics replace the real ones (tests); nil keeps them.
	Review              watchdogReviewFunc
	RepoChangeSignature func(cwd string) *watchdogRepoChangeSignature
	LspDiagnostics      watchdogLspFunc
	ReviewDescription   string
}

type childWatchdog struct {
	Runtime *watchdogRuntime

	config       childWatchdogConfig
	writeStatus  func(childWatchdogStatusEvent)
	mu           sync.Mutex
	seq          int
	agentSettled bool
}

func (c *childWatchdog) emitStatus(phase, reason string, warning *childWatchdogWarningSummary) {
	c.mu.Lock()
	c.seq++
	e := childWatchdogStatusEvent{Type: childWatchdogStatusEventType, RunID: c.config.RunID, Agent: c.config.Agent, Seq: c.seq, Phase: phase, Ts: wdNowMillis(), Reason: reason, Warning: warning}
	if c.config.ChildIndex != nil {
		i := *c.config.ChildIndex
		e.ChildIndex, e.StepIndex = &i, &i
	}
	c.mu.Unlock()
	c.writeStatus(e)
}

func wdWarningSummary(d wdWarning) *childWatchdogWarningSummary {
	return &childWatchdogWarningSummary{Severity: d.Severity, Importance: d.Importance, Category: d.Category, Summary: d.Summary, Evidence: d.Evidence,
		RecommendedAction: d.RecommendedAction, DisplayedAt: d.DisplayedAt, Addressed: false, Stalemate: d.State == "stalemate"}
}

func (c *childWatchdog) childDetails(d wdWarning) wdWarning {
	if d.Source != "lsp" {
		d.Source = "child"
	}
	if c.config.Agent != "" {
		d.Agent = c.config.Agent
	}
	if c.config.RunID != "" {
		d.RunID = c.config.RunID
	}
	return d
}

// newChildWatchdog builds the child's runtime. cfg must be enabled; writeStatus is the status sink.
func newChildWatchdog(cfg childWatchdogConfig, writeStatus func(childWatchdogStatusEvent), binding childWatchdogBinding, review watchdogReviewFunc, opts childWatchdogOptions) (*childWatchdog, error) {
	if writeStatus == nil {
		return nil, errors.New("Child watchdog status sink is missing; the host must pass ChildRuntimeConfig.watchdogStatus.")
	}
	c := &childWatchdog{config: cfg, writeStatus: writeStatus}
	resolved := childResolvedConfig(cfg)
	if opts.Review != nil {
		review = opts.Review
	}
	description := "child model review"
	if opts.ReviewDescription != "" {
		description = opts.ReviewDescription
	}
	c.Runtime = newWatchdogRuntime(watchdogRuntimeOptions{
		ResolveConfig: func(string, *jsObject) watchdogSettingsResult {
			return watchdogSettingsResult{OK: true, Config: resolved, Sources: []wdSettingsSource{{Scope: "session", Exists: true}}}
		},
		Review: review, ReviewDescription: description, ReviewChangesOnly: true,
		LspDiagnostics: opts.LspDiagnostics, RepoChangeSignature: opts.RepoChangeSignature,
		DisplayWarning: func(details wdWarning, o *wdSendOptions) {
			d := c.childDetails(details)
			c.emitStatus("reviewing", "", wdWarningSummary(d))
			c.mu.Lock()
			settled := c.agentSettled
			c.mu.Unlock()
			if settled {
				binding.AppendEntry(subagentWatchdogWarningType, d)
			} else {
				binding.SendMessage(createWatchdogWarningMessage(d, wdMessageOptions{Details: d}), o)
			}
		},
		DisplayUserWarning: func(details wdWarning) {
			d := c.childDetails(details)
			c.emitStatus("reviewing", "", wdWarningSummary(d))
			binding.AppendEntry(subagentWatchdogWarningType, d)
		},
	})
	return c, nil
}

func (c *childWatchdog) onSessionStart(ctx wdRunCtx) {
	c.Runtime.bindSession(ctx)
	c.emitStatus("idle", "", nil)
}

func (c *childWatchdog) onBeforeAgentStart(event any, ctx wdRunCtx) {
	c.mu.Lock()
	c.agentSettled = false
	c.mu.Unlock()
	c.Runtime.handleBeforeAgentStart(event, ctx)
}

func (c *childWatchdog) onTurnEnd(event any, ctx wdRunCtx, structuredTerminal bool) {
	c.Runtime.handleTurnEnd(event, ctx, structuredTerminal)
}

func (c *childWatchdog) onToolResult(ctx wdRunCtx) { c.Runtime.handleToolResult(ctx) }

func (c *childWatchdog) onAgentEnd(event any, ctx wdRunCtx) {
	c.emitStatus("reviewing", "", nil)
	c.Runtime.handleAgentEnd(event, ctx)
	snapshot := c.Runtime.getSnapshot(ctx.Cwd)
	switch snapshot.Status {
	case "failed":
		c.emitStatus("failed", snapshot.LastError, nil)
	case "stale":
		c.emitStatus("stale", "review stale", nil)
	default:
		c.emitStatus("idle", "", nil)
	}
}

func (c *childWatchdog) onAgentSettled() {
	c.mu.Lock()
	c.agentSettled = true
	c.mu.Unlock()
}

func (c *childWatchdog) onSessionShutdown() {
	c.Runtime.dispose()
	c.emitStatus("idle", "", nil)
}

// ── the child process ──

func (e childWatchdogStatusEvent) toObject() *jsObject {
	o := newObject()
	o.set("type", e.Type)
	if e.RunID != "" {
		o.set("runId", e.RunID)
	}
	if e.Agent != "" {
		o.set("agent", e.Agent)
	}
	if e.ChildIndex != nil {
		o.set("childIndex", float64(*e.ChildIndex))
	}
	if e.StepIndex != nil {
		o.set("stepIndex", float64(*e.StepIndex))
	}
	o.set("seq", float64(e.Seq))
	o.set("phase", e.Phase)
	o.set("ts", e.Ts)
	if e.Reason != "" {
		o.set("reason", e.Reason)
	}
	if w := e.Warning; w != nil {
		wo := wdMsg("severity", w.Severity, "importance", w.Importance, "category", w.Category, "summary", w.Summary, "evidence", w.Evidence, "recommendedAction", w.RecommendedAction)
		if w.DisplayedAt != "" {
			wo.set("displayedAt", w.DisplayedAt)
		}
		wo.set("addressed", w.Addressed)
		wo.set("stalemate", w.Stalemate)
		o.set("warning", wo)
	}
	return o
}

// childWatchdogStatusFileWriter appends each status event as one JSON line.
func childWatchdogStatusFileWriter(path string) func(childWatchdogStatusEvent) {
	var mu sync.Mutex
	return func(e childWatchdogStatusEvent) {
		mu.Lock()
		defer mu.Unlock()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = f.WriteString(marshalJSON(e.toObject(), "") + "\n")
	}
}

// tailChildWatchdogStatus delivers every status event a child appended to path, once, as the original's sink delivers them; it
// polls the file until stop is called (after one last read).
func tailChildWatchdogStatus(path string, onEvent func(event childWatchdogStatusEvent)) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		var offset int64
		var partial []byte
		poll := func() {
			f, err := os.Open(path)
			if err != nil {
				return
			}
			defer f.Close()
			if _, err := f.Seek(offset, 0); err != nil {
				return
			}
			r := bufio.NewReader(f)
			for {
				chunk, err := r.ReadBytes('\n')
				offset += int64(len(chunk))
				partial = append(partial, chunk...)
				if err != nil {
					return
				}
				line := partial
				partial = nil
				if v, perr := parseJSON(line); perr == nil {
					if e, ok := decodeChildWatchdogStatusEvent(v); ok {
						onEvent(e)
					}
				}
			}
		}
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				poll()
			case <-done:
				poll()
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
		<-finished
	}
}

func wdStructuredTerminal(cfg *childRoleConfig) func() bool {
	if cfg.Structured == nil || cfg.Structured.OutputPath == "" {
		return func() bool { return false }
	}
	path := cfg.Structured.OutputPath
	return func() bool {
		st, err := os.Stat(path)
		return err == nil && st.Size() > 0
	}
}

// registerChildWatchdogRole is the child role's registration: the watchdog of the config the parent passed, the watchdog_diff
// tool when the child requires it.
func registerChildWatchdogRole(e *sdk.Extension, cfg *childRoleConfig) error {
	if wdContains(cfg.RequiredTools, watchdogDiffToolName) {
		cwd, _ := os.Getwd()
		registerWatchdogDiffTool(e, cwd)
	}
	if len(cfg.Watchdog) == 0 {
		return nil
	}
	config, err := decodeChildWatchdogConfig(string(cfg.Watchdog))
	if err != nil || config == nil {
		return err
	}
	if cfg.WatchdogStatusFile == "" {
		return errors.New("Child watchdog status sink is missing; the host must pass ChildRuntimeConfig.watchdogStatus.")
	}
	var mu sync.Mutex
	var current sdk.Context
	var have bool
	var baseline *watchdogDiffBaseline
	remember := func(ctx sdk.Context) {
		mu.Lock()
		current, have = ctx, true
		mu.Unlock()
	}
	getCtx := func() (sdk.Context, bool) {
		mu.Lock()
		defer mu.Unlock()
		return current, have
	}
	provider := func() wdContext {
		if ctx, ok := getCtx(); ok {
			return newSDKWatchdogContext(ctx)
		}
		return nil
	}
	review := createMainWatchdogReview(provider, wdReviewOptions{
		GetThinkingLevel: func() string {
			if c := provider(); c != nil {
				return c.ThinkingLevel()
			}
			return ""
		},
		DiffBaseline: func() *watchdogDiffBaseline {
			mu.Lock()
			defer mu.Unlock()
			return baseline
		},
	})
	binding := childWatchdogBinding{
		SendMessage: func(msg watchdogWarningMessage, o *wdSendOptions) {
			if ctx, ok := getCtx(); ok {
				so := sdk.SendMessageOptions{}
				if o != nil {
					so.DeliverAs, so.TriggerTurn = o.DeliverAs, o.TriggerTurn
				}
				_ = ctx.SendCustomMessage(sdk.CustomMessage{CustomType: msg.CustomType, Content: msg.Content, Display: msg.Display, Details: msg.Details}, so)
			}
		},
		AppendEntry: func(customType string, details wdWarning) {
			if ctx, ok := getCtx(); ok {
				_ = ctx.AppendEntry(customType, details)
			}
		},
	}
	w, err := newChildWatchdog(*config, childWatchdogStatusFileWriter(cfg.WatchdogStatusFile), binding, review, childWatchdogOptions{})
	if err != nil {
		return err
	}
	terminal := wdStructuredTerminal(cfg)
	e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		remember(ctx)
		mu.Lock()
		baseline = captureWatchdogDiffBaseline(ctx.Cwd())
		mu.Unlock()
		w.onSessionStart(wdRunContext(ctx))
		return nil, nil
	})
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		remember(ctx)
		w.onBeforeAgentStart(toDyn(data), wdRunContext(ctx))
		return nil, nil
	})
	e.OnEvent(sdk.EventTurnEnd, func(ctx sdk.Context, data map[string]any) (any, error) {
		remember(ctx)
		w.onTurnEnd(toDyn(data), wdRunContext(ctx), terminal())
		return nil, nil
	})
	e.OnEvent(sdk.EventToolResult, func(ctx sdk.Context, _ map[string]any) (any, error) {
		remember(ctx)
		w.onToolResult(wdRunContext(ctx))
		return nil, nil
	})
	e.OnEvent(sdk.EventAgentEnd, func(ctx sdk.Context, data map[string]any) (any, error) {
		remember(ctx)
		w.onAgentEnd(toDyn(data), wdRunContext(ctx))
		return nil, nil
	})
	e.OnEvent(sdk.EventAgentSettled, func(_ sdk.Context, _ map[string]any) (any, error) {
		w.onAgentSettled()
		return nil, nil
	})
	e.OnEvent(sdk.EventSessionShutdown, func(_ sdk.Context, _ map[string]any) (any, error) {
		mu.Lock()
		have = false
		mu.Unlock()
		w.onSessionShutdown()
		return nil, nil
	})
	return nil
}

func init() {
	childRoleExtras = append(childRoleExtras, registerChildWatchdogRole)
}
