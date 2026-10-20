package pi_goal_x

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The events (goal-events.ts). registerGoalEvents is the seam the GoalEvents slice fills; until then the extension handles the two
// session events its commands need: session_start loads the goals and session_shutdown persists the focused one.

func registerGoalEvents(c *core) {
	// Until the events that carry a dispatched checkpoint through a run (before_agent_start, message_start, agent_end,
	// agent_settled) are ported, a goal that would run is reported, not run. The GoalEvents slice deletes this line.
	c.scheduler.dispatch = func(*jsObject) { c.h.Notify(refusal(continuationFeature), "error") }
	c.pi.on("session_start", func(c *core, _ map[string]any) (any, error) {
		invalidateGoalPoolCache()
		return nil, c.sessionStart()
	})
	c.pi.on("session_shutdown", func(c *core, _ map[string]any) (any, error) {
		c.shutdown()
		return nil, nil
	})
}

// on registers an event handler that runs under the core's lock with the event's context bound as the core's host.
func (p *goalPi) on(event string, fn func(c *core, data map[string]any) (any, error)) {
	p.e.OnEvent(event, func(ctx sdk.Context, data map[string]any) (any, error) {
		var out any
		var err error
		p.c.withCtx(ctx, func() { out, err = fn(p.c, data) })
		return out, err
	})
}
