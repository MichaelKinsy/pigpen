package pi_subagents

import (
	"errors"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// trackMission runs a single-agent launch under its mission, as the executor does around a launch: prepare the binding
// (a task launch gets a mission unless missions are disabled or `mission: false`), run, and record the result. An explicit
// `mission` or `missionId` that fails is the tool's error; the automatic one only adds `missionWarning` to the details.
func trackMission(ctx sdk.Context, params map[string]any, cwd, agent, task string, run func() (string, error)) (any, error) {
	launch := newObject()
	launch.set("task", task)
	for _, k := range []string{"missionId", "mission"} {
		if v, ok := params[k]; ok {
			launch.set(k, toDyn(v))
		}
	}
	_, explicit := params["missionId"]
	if _, has := params["mission"]; has {
		explicit = true
	}
	binding, err := prepareMissionLaunch(launch, cwd, loadMissionStoreConfig(), currentSessionIdentity(ctx))
	warning := ""
	if err != nil {
		if explicit {
			return nil, sdk.NewToolError(err.Error())
		}
		warning, binding = "Mission tracking unavailable: "+err.Error(), nil
	}
	runID := missionUUID()
	out, runErr := run()
	details := map[string]any{"mode": "single", "agent": agent, "runId": runID}
	text := out
	result := MissionToolResult{Mode: "single", RunID: runID}
	child := MissionChildResult{Agent: agent, Task: task}
	if runErr != nil {
		text = runErr.Error()
		result.IsError = true
		child.ExitCode = 1
		child.Interrupted = errors.Is(runErr, errCancelled)
	}
	result.Content = []MissionContent{{Type: "text", Text: text}}
	result.Results = []MissionChildResult{child}
	if binding != nil {
		attached, aerr := attachMissionToLaunchResult(*binding, result)
		if aerr != nil {
			warning = "Mission tracking unavailable after launch: " + aerr.Error()
			if explicit {
				text += "\n" + warning
				result.IsError = true
			}
		} else {
			text = attached.Content[0].Text
			details["missionId"], details["missionPath"], details["mission"] = attached.MissionID, attached.MissionPath, attached.Mission
		}
	}
	if warning != "" {
		details["missionWarning"] = warning
	}
	if result.IsError {
		return nil, sdk.NewToolError(text)
	}
	return explicitOK{Content: text, Details: details}, nil
}
