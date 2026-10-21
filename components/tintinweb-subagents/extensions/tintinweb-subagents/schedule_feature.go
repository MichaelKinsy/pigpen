package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The switch of the scheduled-runs feature and the parts of the Agent tool text that belong to it. Scheduled runs are
// on by default, as in the original, and `schedulingEnabled: false` in subagents.json (global, then project) turns
// them off: the `schedule` parameter and its guideline leave the tool text (read once, at registration) and a call
// that passes `schedule` anyway is answered with the "disabled" message. upstream: src/index.ts schedulingEnabled,
// scheduleParamShape, scheduleGuideline; src/settings.ts.
//
// TEMPORARY, until the settings slice (branch parity/tintinweb-subagents-settings) lands: that slice assembles every
// part of the Agent text in description.go and reads the setting through settingsView(). On merge, drop
// scheduleFeatureOn and scheduleSettingEnabled in favor of `featureScheduling && settingsView().SchedulingEnabled`,
// and delete withScheduleParam and withScheduleGuideline together with their two call sites in registerTools.
const scheduleFeatureOn = true

// scheduleSettingEnabled is the subagents.json value of schedulingEnabled (a boolean; the project file wins over the
// global one; true when neither has one).
func scheduleSettingEnabled(cwd string) bool {
	enabled := true
	for _, path := range []string{filepath.Join(agentDir(), "subagents.json"), filepath.Join(cwd, ".pi", "subagents.json")} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw map[string]any
		if json.Unmarshal(data, &raw) != nil {
			continue
		}
		if b, ok := raw["schedulingEnabled"].(bool); ok {
			enabled = b
		}
	}
	return enabled
}

// The text of the original's schedule parameter and guideline. upstream: src/index.ts scheduleParamShape, scheduleGuideline.
const scheduleParamDescription = `Opt-in only — fire later instead of now. Omit to run immediately (the default, almost always correct). ` +
	`Formats: 6-field cron ("0 0 9 * * 1" = 9am Mon), interval ("5m"/"1h"), one-shot ("+10m" or ISO). ` +
	`Forces run_in_background; incompatible with inherit_context and resume. Returns job ID.`

const scheduleGuideline = "\n- Use `schedule` only when the user explicitly asked for scheduled / recurring / delayed execution " +
	`(e.g. "every Monday", "in an hour"). Don't auto-schedule from vague intent like "monitor X" — run once now or ask.`

// withScheduleParam adds the `schedule` parameter (last, optional) when scheduling is on.
func (a *app) withScheduleParam(schema sdk.Schema) sdk.Schema {
	if !a.schedulingOn() {
		return schema
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return schema
	}
	props["schedule"] = map[string]any{"type": "string", "description": scheduleParamDescription}
	return schema
}

// withScheduleGuideline adds the guideline to the Agent description's usage notes, after the last of them.
func (a *app) withScheduleGuideline(description string) string {
	const anchor = "\n\n## Writing the prompt"
	if !a.schedulingOn() || strings.Contains(description, scheduleGuideline) {
		return description
	}
	return strings.Replace(description, anchor, scheduleGuideline+anchor, 1)
}
