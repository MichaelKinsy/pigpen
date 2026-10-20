package pi_goal_x

import (
	"encoding/json"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The extension entry (goal.ts): a thin installer. All state lives in the core (core.go); the commands, tools and events are
// registered from their own files (goal_commands.go, goal_tools.go, goal_events.go). No goal-file writes, ledger appends or
// prompt construction happen here.

// goalPi is `pi` for the registration seams: the SDK extension and the core its handlers run against.
type goalPi struct {
	e *sdk.Extension
	c *core
}

// command registers a slash command whose handler runs under the core's lock with the call's context bound as the core's host.
func (p *goalPi) command(name, desc string, fn func(c *core, args string) error) {
	p.e.RegisterCommand(name, sdk.CommandOptions{Description: desc, Handler: func(ctx sdk.Context, args string) error {
		var err error
		p.c.withCtx(ctx, func() { err = fn(p.c, args) })
		return err
	}})
}

// withCtx runs fn as one unit of work: under the core's lock with ctx bound as the core's host, followed by the UI update the
// original queues as a microtask.
func (c *core) withCtx(ctx sdk.Context, fn func()) {
	c.locked(func() {
		h := sdkHost{ctx: ctx, c: c}
		c.h = h
		c.st = storage{cwd: h.Cwd()}
		fn()
	})
}

// Extension is pi-goal-x.
func Extension() *sdk.Extension { return newGoalExtension(goalDeps{}) }

// newGoalExtension is goalExtension(pi, dependencies).
func newGoalExtension(deps goalDeps) *sdk.Extension {
	e, _ := buildGoalExtension(deps)
	return e
}

// buildGoalExtension also returns the core (the original's `pi._goalCore` test hook); the core is nil for a delegated session.
func buildGoalExtension(deps goalDeps) (*sdk.Extension, *core) {
	e := sdk.New("pi-goal-x")
	if isDelegatedGoalSession(nil) {
		// Inherit conversation without inheriting ownership of the parent's goal.
		e.OnEvent("context", func(_ sdk.Context, data map[string]any) (any, error) { return goalDelegatedContext(data) })
		return e, nil
	}
	e.MessageRenderer(goalEventEntry, goalMessageRenderer(func() goalMessageRenderFunc { return goalEventRenderer }))
	e.MessageRenderer(goalAuditEntry, goalMessageRenderer(func() goalMessageRenderFunc { return goalAuditEventRenderer }))
	c := newGoalCore(nil, deps)
	c.pi = &goalPi{e: e, c: c}
	registerGoalCommands(c)
	registerGoalTools(c)
	registerGoalEvents(c)
	return e, c
}

// goalDelegatedContext is the delegated session's only handler: the conversation without the parent's goal messages.
func goalDelegatedContext(data map[string]any) (any, error) {
	messages, _ := data["messages"].([]any)
	if filtered := filterGoalSessionContext(messages, true); filtered != nil {
		return map[string]any{"messages": filtered}, nil
	}
	return nil, nil
}

// goalMessageRenderFunc draws one goal message (decoded JSON) at width; expanded is the host's expand state.
type goalMessageRenderFunc func(message map[string]any, expanded bool, width int, theme goalTheme) []string

// goalEventRenderer and goalAuditEventRenderer are renderGoalEvent and renderGoalAuditEvent of goal-format.ts.
var (
	goalEventRenderer      goalMessageRenderFunc = renderGoalEventMessage
	goalAuditEventRenderer goalMessageRenderFunc = renderGoalAuditEventMessage
)

// jsonValue is the value as the *jsObject tree the ported modules read (decoded JSON in, ordered objects out).
func jsonValue(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	out, err := parseJSON(b)
	if err != nil {
		return nil
	}
	return out
}

func renderGoalEventMessage(message map[string]any, expanded bool, width int, theme goalTheme) []string {
	return renderGoalEvent(jsonValue(message["details"]), expanded, theme).Render(width)
}

func renderGoalAuditEventMessage(message map[string]any, _ bool, width int, theme goalTheme) []string {
	phase := ""
	if d := asRecord(jsonValue(message["details"])); d != nil {
		phase, _ = d.str("phase")
	}
	return renderGoalAuditEvent(message["content"], phase, theme).Render(width)
}

func goalMessageRenderer(pick func() goalMessageRenderFunc) sdk.RendererFunc {
	return func(ctx sdk.Context, message map[string]any, options sdk.MessageRenderOptions, width int) ([]string, error) {
		return pick()(message, options.Expanded, width, sdkTheme{ctx.UITheme()}), nil
	}
}
