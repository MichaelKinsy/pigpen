package pi_goal_x

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The tools (goal-tools.ts: goal-core-tools.ts, goal-task-tools.ts, goal-completion.ts). registerGoalTools is the seam the GoalTools
// slice fills; until then this port registers no tools (the commands stand on their own).

func registerGoalTools(c *core) {}

// goalToolFunc binds an extension tool handler to the core: the handler runs under the core's lock with the call's context.
func (p *goalPi) toolFunc(fn func(c *core, params map[string]any) (any, error)) sdk.ToolFunc {
	return func(ctx sdk.Context, params map[string]any) (any, error) {
		var out any
		var err error
		p.c.withCtx(ctx, func() { out, err = fn(p.c, params) })
		return out, err
	}
}
