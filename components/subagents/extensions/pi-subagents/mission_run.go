package pi_subagents

import (
	"errors"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// trackMission runs a single-agent launch under its mission, as the executor does around a launch: prepare the binding
// (a task launch gets a mission unless missions are disabled or `mission: false`), run, and record the result. An explicit
// `mission` or `missionId` that fails is the tool's error; the automatic one only adds `missionWarning` to the details.
func trackMission(ctx sdk.Context, params map[string]any, cwd, agent, task string, run func() (any, error)) (any, error) {
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
	res, runErr := run()
	var details any = map[string]any{"mode": "single", "agent": agent}
	text, isToolResult := "", false
	switch r := res.(type) {
	case explicitOK:
		text, details = r.Content, r.Details
	case sdk.ToolResult:
		text, details = r.Content, r.Details
		if r.IsError && runErr == nil {
			runErr, isToolResult = errors.New(r.Content), true
		}
	}
	if existing, _ := detailsGet(details, "runId").(string); existing != "" {
		runID = existing
	} else {
		detailsSet(details, "runId", runID)
	}
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
			detailsSet(details, "missionId", attached.MissionID)
			detailsSet(details, "missionPath", attached.MissionPath)
			detailsSet(details, "mission", attached.Mission)
		}
	}
	if warning != "" {
		detailsSet(details, "missionWarning", warning)
	}
	if result.IsError {
		if isToolResult {
			return sdk.ToolResult{Content: text, IsError: true, Details: details}, nil
		}
		return nil, sdk.NewToolError(text)
	}
	return explicitOK{Content: text, Details: details}, nil
}

func detailsGet(details any, k string) any {
	switch d := details.(type) {
	case *jsObject:
		return d.vals[k]
	case map[string]any:
		return d[k]
	}
	return nil
}

func detailsSet(details any, k string, v any) {
	switch d := details.(type) {
	case *jsObject:
		d.set(k, v)
	case map[string]any:
		d[k] = v
	}
}
