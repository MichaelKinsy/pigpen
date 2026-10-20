package pi_subagents

import (
	"sync/atomic"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The mission slice registers its own tool surface (see refuse.go): the `mission.*` actions, the `mission` and `missionId`
// launch parameters and the goal-mission notice after each turn.

// missionActionParams are the parameters besides `action` each mission action reads (cwd places the project root).
var missionActionParams = map[string][]string{
	"mission.create":           {"mission", "missionStatus", "cwd"},
	"mission.list":             {"missionScope", "cwd"},
	"mission.show":             {"missionId", "cwd"},
	"mission.update":           {"missionId", "missionUpdate", "cwd"},
	"mission.resolve-decision": {"missionId", "id", "summary", "cwd"},
	"mission.attach-run":       {"missionId", "runId", "id", "runMode", "dir", "agent", "runStatus", "cwd"},
	"mission.close":            {"missionId", "missionStatus", "summary", "cwd"},
}

// goalRetainedChildren lists the retained children of a session for the goal notices; the background-run slice sets it.
var goalRetainedChildren = func(sessionID string) []GoalRetainedChild { return nil }

var goalTurn atomic.Int64

// currentSessionIdentity is resolveCurrentSessionId: the session file, else the session id.
func currentSessionIdentity(ctx sdk.Context) string {
	if f, err := ctx.GetSessionFile(); err == nil && f != nil && *f != "" {
		return *f
	}
	if id, err := ctx.GetSessionID(); err == nil {
		return id
	}
	return ""
}

func init() {
	for _, name := range MissionActions {
		name := name
		registerAction(name, missionActionParams[name], func(ctx sdk.Context, params map[string]any) (any, error) {
			dyn, _ := toDyn(params).(*jsObject)
			cwd := ctx.Cwd()
			if requested, ok := params["cwd"].(string); ok && requested != "" {
				cwd = nodeResolve(cwd, requested)
			}
			res, err := handleMissionAction(name, dyn, MissionActionContext{Cwd: cwd, CurrentSessionID: currentSessionIdentity(ctx), Config: loadMissionStoreConfig()})
			if err != nil {
				return nil, sdk.NewToolError(err.Error())
			}
			return explicitOK{Content: res.Text, Details: res.Details}, nil
		})
	}
	registerRunParams("mission", "missionId")
	extensionHooks = append(extensionHooks, func(e *sdk.Extension) {
		e.OnEvent(sdk.EventAgentEnd, func(ctx sdk.Context, _ map[string]any) (any, error) {
			owner := currentSessionIdentity(ctx)
			if owner == "" {
				return nil, nil
			}
			turn := goalTurn.Add(1)
			loc := resolveMissionStoreLocation(ctx.Cwd(), loadMissionStoreConfig(), "")
			notices, err := collectGoalContinuationNotices(loc, owner, goalRetainedChildren(owner), int(turn), 0)
			if err != nil {
				return nil, nil // the original logs "Failed to evaluate goal missions" and goes on
			}
			for _, n := range notices {
				_ = ctx.SendCustomMessage(sdk.CustomMessage{
					CustomType: "subagent_control_notice", Content: n.Message, Display: true,
					Details: map[string]any{"source": "goal", "event": n.Event, "noticeText": n.Message},
				}, sdk.SendMessageOptions{TriggerTurn: sdk.Bool(false)})
			}
			return nil, nil
		})
	})
}
