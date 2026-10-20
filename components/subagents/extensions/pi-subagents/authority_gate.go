package pi_subagents

import (
	"encoding/json"
	"fmt"
	"os"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The authority gate of the management actions (the policy block of subagent-executor.ts execute: policyAction): child-safe fanout
// mode is a hard capability boundary, the authority policy an operator preference. The boundary is checked first, so the gate
// never prompts for an action that will be rejected and never masks the more specific reason.

// mutatingManagementActions are the actions a child-safe fanout child is not given (MUTATING_MANAGEMENT_ACTIONS).
var mutatingManagementActions = setOf("create", "update", "delete", "eject", "disable", "enable", "reset", "grant-spawn-budget", "watchdog.configure",
	"mission.create", "mission.update", "mission.resolve-decision", "mission.attach-run", "mission.close", "inspector.open", "inspector.close",
	"project.open", "project.close", "worktree.discard", "worktree.cleanup", "lane.recordMerge", "lane.recordSupersession", "refine", "refine.rollback",
	"dismiss", "schedule.create", "schedule.pause", "schedule.resume", "schedule.run", "schedule.run-due", "schedule.delete")

// authorityPolicyActions maps a management action to the authority action its policy names.
var authorityPolicyActions = map[string]string{"stop": "stopRun", "steer": "steerRun", "schedule.create": "scheduleCreate", "inspector.open": "inspectorOpen", "project.open": "projectOpen"}

// authorityGate carries what the gate decides on.
type authorityGate struct {
	// AllowMutating is false in child-safe fanout mode.
	AllowMutating bool
	Policy        authorityPolicy
	HasUI         bool
	// Confirm asks the operator (ctx.ui.confirm); only called when the policy says confirm and there is a UI.
	Confirm func(title, message string) (bool, error)
}

// authorityRefusal is the gate's refusal: the text, and whether it is an error (a declined confirmation is not).
type authorityRefusal struct {
	Text    string
	IsError bool
}

// check is nil when the action may run.
func (g authorityGate) check(action string) *authorityRefusal {
	policyAction, gated := authorityPolicyActions[action]
	if !gated {
		return nil
	}
	if !g.AllowMutating && mutatingManagementActions[action] {
		return &authorityRefusal{fmt.Sprintf("Action '%s' is not available from child-safe subagent fanout mode.", action), true}
	}
	switch resolveAuthorityDecision(policyAction, g.Policy) {
	case "forbid":
		return &authorityRefusal{fmt.Sprintf("Authority policy forbids action '%s'.", action), true}
	case "confirm":
		if !g.HasUI {
			return &authorityRefusal{fmt.Sprintf("Authority policy requires user confirmation for action '%s', but this session has no interactive UI.", action), true}
		}
		ok, err := false, error(nil)
		if g.Confirm != nil {
			ok, err = g.Confirm(fmt.Sprintf("Authorize subagent %s?", action), fmt.Sprintf("Authority policy requires confirmation before '%s'.", action))
		}
		if err != nil || !ok {
			return &authorityRefusal{fmt.Sprintf("Action '%s' canceled; authority was not granted.", action), false}
		}
	}
	return nil
}

// configuredAuthorityPolicy is `authorityPolicy` of the extension's config.json (nil when absent or invalid: an invalid policy is
// refused when the config is validated, and the defaults stay in force until then).
func configuredAuthorityPolicy() authorityPolicy {
	data, err := os.ReadFile(asyncConfigPath())
	if err != nil {
		return nil
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil || raw["authorityPolicy"] == nil {
		return nil
	}
	v, err := parseJSON(raw["authorityPolicy"])
	if err != nil {
		return nil
	}
	policy, err := validateAuthorityPolicy(v, "authorityPolicy")
	if err != nil {
		return nil
	}
	return policy
}

// gateFor is the gate of the running session: the config's policy, the session's UI.
func gateFor(ctx sdk.Context) authorityGate {
	return authorityGate{AllowMutating: true, Policy: configuredAuthorityPolicy(), HasUI: ctx.HasUI(), Confirm: ctx.Confirm}
}

// gatedAction runs an action behind the gate.
func gatedAction(action string, run func(ctx sdk.Context, p map[string]any) (any, error)) func(ctx sdk.Context, p map[string]any) (any, error) {
	return func(ctx sdk.Context, p map[string]any) (any, error) {
		if r := gateFor(ctx).check(action); r != nil {
			if r.IsError {
				return nil, mgmtError(r.Text)
			}
			return explicitOK{Content: r.Text, Details: map[string]any{"mode": "management", "results": []any{}}}, nil
		}
		return run(ctx, p)
	}
}

// stopGated records that the stop action is already behind the gate (Extension() runs the hooks once per extension built).
var stopGated bool

func init() {
	// stop is an async lane action; steering is gated the same way, so the gate wraps it once every action is registered.
	extensionHooks = append(extensionHooks, func(*sdk.Extension) {
		if a := actions["stop"]; a != nil && !stopGated {
			a.run = gatedAction("stop", a.run)
			stopGated = true
		}
	})
}
