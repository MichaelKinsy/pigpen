package tintinweb_subagents

import (
	"errors"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The scheduler in the extension: the session-bound scheduler, its events, and the `schedule` branch of the Agent
// tool. upstream: src/index.ts (startScheduler, session_start, session_before_switch, session_shutdown, the
// "Schedule: register a job, don't spawn now" branch of Agent execute).

var errNoSession = errors.New("No active session")

// startupWait is how long a fire waits for its agent to start. upstream: the awaitStartup of schedule.ts executeJob.
const startupWait = 30 * time.Second

// scheduleState is the scheduler and whether scheduling is on (the setting, and the runtime toggle of /agents).
type scheduleState struct {
	mu      sync.Mutex
	sched   *subagentScheduler
	enabled bool
}

func (a *app) initSchedule() {
	a.schedule.sched = newSubagentScheduler(nil)
	a.schedule.enabled = featureScheduling
}

// schedulingOn is `isSchedulingEnabled()`.
func (a *app) schedulingOn() bool {
	a.schedule.mu.Lock()
	defer a.schedule.mu.Unlock()
	return a.schedule.enabled
}

// setSchedulingEnabled is `setSchedulingEnabled(b)`: the switch of the feature. The tool text is decided when the
// extension loads; the `schedule` check and the scheduler follow the setting as it is when a session starts (like the
// other settings of this port, which are read at a session start in the workspace the host reports).
func (a *app) setSchedulingEnabled(on bool) {
	a.schedule.mu.Lock()
	a.schedule.enabled = on && featureScheduling
	a.schedule.mu.Unlock()
}

// scheduler is the session's scheduler, for the `/agents` Scheduled jobs menu: isActive, list, getNextRun, removeJob.
func (a *app) scheduler() *subagentScheduler { return a.schedule.sched }

func (a *app) registerSchedule(e *sdk.Extension) {
	e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		a.setSchedulingEnabled(settingsView().schedulingEnabled()) // read for this workspace by the handler that reloads the settings
		if a.schedulingOn() && !a.scheduler().isActive() {
			a.startScheduler(ctx)
		}
		return nil, nil
	})
	// A session switch: schedules reset on /new and are restored on /resume (the store is per session).
	e.OnEvent(sdk.EventSessionBeforeSwitch, func(sdk.Context, map[string]any) (any, error) {
		a.scheduler().stop()
		return nil, nil
	})
	e.OnSessionShutdown(func(sdk.Context, map[string]any) (any, error) {
		a.scheduler().stop()
		return nil, nil
	})
}

// startScheduler binds the scheduler to the store of the session. upstream: index.ts startScheduler.
func (a *app) startScheduler(ctx sdk.Context) {
	sessionID, err := ctx.GetSessionID()
	if err != nil || sessionID == "" {
		return // no session id yet: try again on the next event
	}
	store := newScheduleStore(resolveStorePath(ctx.Cwd(), sessionID))
	a.scheduler().start(scheduleManagerOf{a}, store, a.emitScheduled)
	_ = ctx.Events().Emit("subagents:scheduler_ready", map[string]any{"sessionId": sessionID, "jobCount": len(store.list())})
}

// emitScheduled publishes on `subagents:scheduled`, the channel cross-extension consumers listen to.
func (a *app) emitScheduled(ev scheduleChange) {
	if ctx, ok := a.context(); ok {
		_ = ctx.Events().Emit("subagents:scheduled", ev)
	}
}

// scheduleManagerOf is the scheduler's view of the manager: a fire starts a background agent that bypasses the
// concurrency limit. upstream: manager.spawn(pi, ctx, type, prompt, { bypassQueue: true, ... }).
type scheduleManagerOf struct{ a *app }

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func (m scheduleManagerOf) spawn(typ, prompt string, o spawnJobOptions) (string, error) {
	ctx, ok := m.a.context()
	if !ok {
		return "", errNoSession
	}
	// The one door of a spawn that does not come from the Agent tool: the model is resolved and scope-checked, the
	// worktree option is gated by the project switch. o.Invocation is the snapshot the conversation viewer shows; the
	// viewer is not part of this slice (G5).
	options := map[string]any{"description": o.Description, "isBackground": o.IsBackground, "bypassQueue": o.BypassQueue}
	if o.Model != nil {
		options["model"] = *o.Model
	}
	if o.Thinking != nil {
		options["thinkingLevel"] = *o.Thinking
	}
	if o.MaxTurns != nil {
		options["maxTurns"] = float64(*o.MaxTurns)
	}
	if o.Isolated != nil {
		options["isolated"] = *o.Isolated
	}
	if o.Isolation != "" {
		options["isolation"] = o.Isolation
	}
	r, err := m.a.spawnTopLevel(ctx, typ, prompt, options)
	if err != nil {
		return "", err
	}
	return r.ID, nil
}

func (m scheduleManagerOf) awaitStartup(id string) error {
	r := m.a.mgr.get(id)
	if r == nil {
		return nil
	}
	return m.a.mgr.awaitStartup(r, startupWait)
}

func (m scheduleManagerOf) getRecord(id string) *scheduleRun {
	r := m.a.mgr.get(id)
	if r == nil {
		return nil
	}
	return &scheduleRun{Done: r.done, Status: func() string {
		m.a.mgr.mu.Lock()
		defer m.a.mgr.mu.Unlock()
		return r.Status
	}}
}

// scheduleCall registers a job instead of spawning. upstream: index.ts "Schedule: register a job, don't spawn now".
func (a *app) scheduleCall(p map[string]any, schedule string) (any, error) {
	rawType, _ := p["subagent_type"].(string)
	resume, _ := p["resume"].(string)
	// Dispatch is decided before anything is scheduled: an unknown, disabled or ambiguous type is refused here.
	// `resume` ignores the type, so it does not gate a call that is refused for the resume anyway.
	dispatch := resolveSpawnType(rawType)
	if !dispatch.OK && resume == "" {
		return textResult(dispatch.Message), nil
	}
	subagentType := rawType
	if dispatch.OK {
		subagentType = dispatch.Type
	}
	// What the caller asked for, named once: `FellBackFrom` is "" for a blank request.
	requestedType := subagentType
	if dispatch.OK && dispatch.FellBackFrom != "" {
		requestedType = dispatch.FellBackFrom
	}
	fallbackNote := ""
	if dispatch.OK && resolveEnabledTypeIn(currentRegistry(), strings.TrimSpace(rawType)) == "" {
		used := "the fallback agent config"
		if resolveType(subagentType) != "" {
			used = subagentType
		}
		fallbackNote = `Note: Unknown agent type "` + dispatch.FellBackFrom + `" — using ` + used + ".\n\n"
	}
	cfg := getAgentConfig(subagentType)
	if !a.schedulingOn() {
		return textResult("Scheduling is disabled in this project. Enable via /agents → Settings → Scheduling."), nil
	}
	if resume != "" {
		return textResult("Cannot combine `schedule` with `resume` — schedules create fresh agents."), nil
	}
	if b, _ := p["inherit_context"].(bool); b {
		return textResult("Cannot combine `schedule` with `inherit_context` — there is no parent conversation at fire time."), nil
	}
	if b, ok := p["run_in_background"].(bool); ok && !b {
		return textResult("Cannot combine `schedule` with `run_in_background: false` — scheduled jobs always run in background."), nil
	}
	if !a.scheduler().isActive() {
		return textResult("Scheduler is not active in this session yet. Try again after the session has fully started."), nil
	}

	// The run-shaping parameters are resolved as an immediate run resolves them (an agent file outranks the call), so a
	// scheduled run behaves like the same call made now. upstream: invocation-config.ts resolveAgentInvocationConfig.
	var thinking, model *string
	if v, ok := p["thinking"].(string); ok {
		thinking = &v
	}
	if cfg != nil && cfg.Thinking != "" {
		v := cfg.Thinking
		thinking = &v
	}
	if v, ok := p["model"].(string); ok {
		model = &v
	}
	isolated := false
	if v, ok := p["isolated"].(bool); ok {
		isolated = v
	}
	if cfg != nil && cfg.Isolated != nil {
		isolated = *cfg.Isolated
	}
	var maxTurns *int
	if f, ok := p["max_turns"].(float64); ok {
		n := int(f)
		maxTurns = &n
	}
	if cfg != nil && cfg.MaxTurns != nil {
		n := *cfg.MaxTurns
		maxTurns = &n
	}
	if maxTurns == nil {
		maxTurns = a.runtime().defaultMaxTurns
	}
	// A worktree the agent file or the call asks for is kept on the job, unless the project switch is off.
	isolation := resolveInvocationConfig(cfg, invocationParamsOf(p), invocationOptions{worktreeDisallowed: !settingsView().worktreeIsolation()}).isolation
	description, _ := p["description"].(string)
	prompt, _ := p["prompt"].(string)
	job, err := a.scheduler().addJob(newJobInput{
		Name: description, Description: description, Schedule: schedule,
		// The caller's own name, not the substitute: the scheduler re-resolves at fire time, and the original is what a user edits.
		SubagentType: requestedType, Prompt: prompt, Model: model, Thinking: thinking, MaxTurns: normalizeMaxTurns(maxTurns), Isolated: &isolated, Isolation: isolation,
	})
	if err != nil {
		return textResult(err.Error()), nil
	}
	next := "(unknown)"
	if n, ok := a.scheduler().getNextRun(job.ID); ok {
		next = n
	}
	return textResult(fallbackNote + `Scheduled "` + job.Name + `" (id: ` + job.ID + `, type: ` + job.ScheduleType + `). ` +
		`Next run: ` + next + `. ` + `Manage via /agents → Scheduled jobs.`), nil
}
