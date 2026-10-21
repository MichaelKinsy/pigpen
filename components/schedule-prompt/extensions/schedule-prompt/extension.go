// Package schedule_prompt is the Go port of pi-schedule-prompt ("Pi's Heartbeat") by tintinweb: the agent schedules future prompts,
// reminders and recurring jobs for itself. See port/PORT.md for what is ported and what is still refused.
package schedule_prompt

import (
	"context"
	"sync"
	"sync/atomic"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// sdkPi implements piAPI on a retained host context (the original holds the ExtensionAPI and its context for its timers).
type sdkPi struct{ ctx sdk.Context }

func (p sdkPi) SendMessage(msg customMessage, opts *deliverOptions) error {
	var o sdk.SendMessageOptions
	if opts != nil {
		o.DeliverAs = opts.DeliverAs
		if opts.TriggerTurn {
			o.TriggerTurn = sdk.Bool(true)
		}
	}
	return p.ctx.SendCustomMessage(sdk.CustomMessage{CustomType: msg.CustomType, Content: msg.Content, Display: msg.Display, Details: msg.Details}, o)
}

func (p sdkPi) SendUserMessage(text, deliverAs string) error {
	return p.ctx.SendUserMessage(text, deliverAs)
}

func (p sdkPi) Emit(channel string, data any) {
	if err := p.ctx.Events().Emit(channel, data); err != nil {
		logf("Failed to emit %s: %v", channel, err)
	}
}

// sdkSession implements sessionInfo and toolSession on a host context.
type sdkSession struct{ ctx sdk.Context }

func (s sdkSession) SessionID() string {
	id, err := s.ctx.SessionManager().GetSessionID()
	if err != nil {
		logf("Failed to read the session id: %v", err)
	}
	return id
}

func (s sdkSession) Entries() ([]map[string]any, error) { return s.ctx.SessionManager().GetEntries() }

// app is the extension's state (upstream: index.ts, the closure of the default export).
type app struct {
	mu       sync.Mutex
	storage  *fileStorage
	sched    *scheduler
	settings settings
	widget   *cronWidget
	widgetOn atomic.Bool
	runner   subagentRunner
}

func (a *app) defaultScope() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.settings.defaultJobScope == "" {
		return "session"
	}
	return a.settings.defaultJobScope
}

// refusingRunner stands for runSubagentOnce until the per-task model mode is ported (PORT.md M16): the run fails with the program's
// refusal, which the scheduler reports like any failed subagent run.
func refusingRunner(string, string, context.Context, subagentOptions) subagentResult {
	return subagentResult{Error: unsupported(modelFeature).Error()}
}

func (a *app) isWidgetVisible() bool { return a.widgetOn.Load() }

func (a *app) cleanupSession(ctx sdk.Context) {
	if a.sched != nil {
		a.sched.stop()
	}
	if a.widget != nil {
		a.widget.hide(sdkWidgetUI{ctx})
		a.widget.destroy()
	}
}

func (a *app) initializeSession(ctx sdk.Context) {
	// Idempotent: tear down any prior scheduler before making a new one (session_start also fires on reload, resume and fork).
	a.cleanupSession(ctx)
	a.settings = loadSettings(ctx.Cwd())
	a.widgetOn.Store(a.settings.widgetVisible == nil || *a.settings.widgetVisible)
	a.storage = newFileStorage(ctx.Cwd())
	a.sched = newScheduler(a.storage, sdkPi{ctx}, sdkSession{ctx}, a.runner)
	a.widget = newCronWidget(a.storage, a.sched, a.isWidgetVisible, sdkSession{ctx}.SessionID())
	// The widget redraws on every change event, so the rows and the counters stay current without waiting for the 30 s tick.
	w := a.widget
	a.sched.onChange(func(*jsobj.Object) { w.refresh() })
	a.sched.start()
	if a.isWidgetVisible() {
		a.widget.show(sdkWidgetUI{ctx})
	}
}

// autoCleanupDisabledJobs sweeps this session's own (or unbound) disabled jobs, never another session's.
func (a *app) autoCleanupDisabledJobs(ctx sdk.Context) {
	if a.storage == nil {
		return
	}
	my := sdkSession{ctx}.SessionID()
	all, err := a.storage.GetAllJobs()
	if err != nil {
		logf("Auto-cleanup failed: %v", err)
		return
	}
	var disabled []string
	for _, j := range all {
		if !truthy(j.Get("enabled")) && isLoadedFor(j, my) {
			disabled = append(disabled, jobID(j))
		}
	}
	for _, id := range disabled {
		_, _ = a.storage.RemoveJob(id)
	}
}

func (a *app) onSessionStart(ctx sdk.Context, data map[string]any) (any, error) {
	reason, _ := data["reason"].(string)
	a.mu.Lock()
	defer a.mu.Unlock()
	if reason != "startup" {
		a.autoCleanupDisabledJobs(ctx)
	}
	a.initializeSession(ctx)
	return nil, nil
}

func (a *app) onSessionShutdown(ctx sdk.Context, _ map[string]any) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.autoCleanupDisabledJobs(ctx)
	a.cleanupSession(ctx)
	return nil, nil
}

func (a *app) toolDeps() toolDeps {
	return toolDeps{
		storage: func() jobStore {
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.storage == nil {
				return nil
			}
			return a.storage
		},
		scheduler: func() jobScheduler {
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.sched == nil {
				return nil
			}
			return a.sched
		},
		defaultScope: a.defaultScope,
	}
}

func (a *app) executeTool(ctx sdk.Context, params map[string]any) (any, error) {
	deps := a.toolDeps()
	if deps.scheduler() == nil {
		return nil, unsupportedBeforeSession()
	}
	out, err := deps.execute(params, sdkSession{ctx})
	if err != nil {
		return nil, err
	}
	return sdk.ToolResult{Content: out.Text, Details: out.Details}, nil
}

// Extension returns the schedule-prompt extension.
func Extension() *sdk.Extension {
	e := sdk.New("schedule-prompt")
	a := &app{runner: refusingRunner}
	e.MessageRenderer("scheduled_prompt", renderScheduledPrompt)
	e.RegisterTool(sdk.ToolDefinition{
		Name: toolName, Label: toolLabel, Description: toolDesc, Parameters: toolParameters(),
		Execute:      a.executeTool,
		RenderCall:   renderToolCall,
		RenderResult: renderToolResult,
	})
	e.OnEvent(sdk.EventSessionStart, a.onSessionStart)
	e.OnEvent(sdk.EventSessionShutdown, a.onSessionShutdown)
	e.Command("schedule-prompt", commandDescription, a.runCommand)
	return e
}

// unsupportedBeforeSession is the error of a tool call that arrives before any session_start (the host always sends one first).
func unsupportedBeforeSession() error {
	return errNoSession
}
