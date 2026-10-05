package pi_goal_x

import (
	"encoding/json"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The SDK wiring: sdkHost adapts a Context to the core's host interface; one core lives for the extension process.

type sdkHost struct{ ctx sdk.Context }

func (h sdkHost) Cwd() string { return h.ctx.Cwd() }
func (h sdkHost) SessionID() string {
	id, _ := h.ctx.GetSessionID()
	return id
}
func (h sdkHost) HasUI() bool           { return h.ctx.HasUI() }
func (h sdkHost) Rich() bool            { return h.ctx.HasUI() && h.ctx.Mode() != "rpc" }
func (h sdkHost) Notify(m, l string)    { h.ctx.Notify(m, l) }
func (h sdkHost) SetStatus(k, t string) { h.ctx.SetStatus(k, t) }
func (h sdkHost) ClearWidget(k string)  { _ = h.ctx.SetWidget(k, nil) }
func (h sdkHost) Confirm(t, m string) bool {
	ok, _ := h.ctx.Confirm(t, m)
	return ok
}
func (h sdkHost) Select(t string, o []string) (string, bool) {
	v, ok, err := h.ctx.Select(t, o)
	return v, ok && err == nil
}
func (h sdkHost) Branch() []any {
	raw, err := h.ctx.SessionManager().GetBranch(nil)
	if err != nil {
		return nil
	}
	out := make([]any, 0, len(raw))
	for _, e := range raw {
		b, err := json.Marshal(e)
		if err != nil {
			continue
		}
		if v, err := parseJSON(b); err == nil {
			out = append(out, v)
		}
	}
	return out
}
func (h sdkHost) AppendEntry(t string, d *jsObject) { _ = h.ctx.AppendEntry(t, d) }
func (h sdkHost) IsIdle() bool {
	idle, err := h.ctx.IsIdle()
	return err != nil || idle
}
func (h sdkHost) Abort() { h.ctx.Abort() }

type app struct {
	mu sync.Mutex
	c  *core
}

// with runs fn against the core bound to this call's context and then does the UI update the original queues.
func (a *app) with(ctx sdk.Context, fn func(c *core)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := sdkHost{ctx: ctx}
	if a.c == nil {
		a.c = newCore(h)
	}
	a.c.h = h
	a.c.st = storage{cwd: h.Cwd()}
	fn(a.c)
	a.c.flush()
}

func (a *app) command(fn func(c *core, args string) error) sdk.CommandFunc {
	return func(ctx sdk.Context, args string) error {
		var err error
		a.with(ctx, func(c *core) { err = fn(c, args) })
		return err
	}
}

// Extension is pi-goal-x, the part that stores goals and runs the commands that need no scheduler.
func Extension() *sdk.Extension {
	e := sdk.New("pi-goal-x")
	a := &app{}
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		invalidateGoalPoolCache()
		a.with(ctx, func(c *core) { c.sessionStart() })
		return nil, nil
	})
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		a.with(ctx, func(c *core) { c.shutdown() })
		return nil, nil
	})
	for _, d := range commandDefs {
		e.RegisterCommand(d.name, sdk.CommandOptions{Description: d.desc, Handler: a.command(d.fn)})
	}
	return e
}

// commandDefs are the commands this port registers; the tests drive the same table.
var commandDefs = []struct {
	name, desc string
	fn         func(c *core, args string) error
}{
	{"goal-direct", "Create and start a regular goal immediately, without drafting.", func(c *core, args string) error { return c.direct(args, false) }},
	{"sisyphus-direct", "Create and start a Sisyphus goal immediately, without drafting.", func(c *core, args string) error { return c.direct(args, true) }},
	{"goal-list", "List all open goals and the current focus.", func(c *core, _ string) error { c.list(); return nil }},
	{"goal-pause", "Pause the currently running goal. Esc also pauses while running.", func(c *core, _ string) error { c.pause(); return nil }},
	{"goal-clear", "Archive the current goal after confirmation (user-owned abandonment).", func(c *core, _ string) error { c.clear(); return nil }},
	{"goal-unfocus", "Stop focusing the current goal (session only; goal stays open).", func(c *core, _ string) error { c.unfocus(); return nil }},
}
