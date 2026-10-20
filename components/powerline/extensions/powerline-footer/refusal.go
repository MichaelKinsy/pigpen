package powerline_footer

import "os"

// What the original does that this port does not do yet. Each such command or setting is refused with one message that names the
// working alternative; nothing is accepted and then ignored. The feature names are the ones in port/slices.json.
// upstream: index.ts (the commands and settings named in each entry).

const (
	packageName  = "@pi-in-go/pigpen-powerline"
	originalSpec = "npm:pi-powerline-footer@0.19.1"
)

// refusal is the one message for a feature that is not ported.
func refusal(feature string) string {
	return packageName + " (Go port) does not support " + feature + " yet. Install the original extension, " + originalSpec +
		", which runs unchanged in PiG."
}

// refusedCommands are the commands the original registers that the port does not implement: command name, description, feature.
var refusedCommands = []struct{ name, description, feature string }{
	{"reply", "Quote a previous user or assistant message into the editor", "quote reply (/reply)"},
	{"queue", "Manage the powerline prompt queue", "the prompt queue (/queue)"},
	{"bash-mode", "Toggle bash mode", "bash mode (/bash-mode)"},
	{"bash-reset", "Restart the bash mode shell session", "bash mode (/bash-reset)"},
	{"powerline-perf", "Show or reset the editor performance counters", "the editor performance profiler (/powerline-perf)"},
	{"cd", "Change the session working directory", "the /cd command"},
}

// unsupportedSettings lists the features the user's settings or environment turn on that the port does not provide, in a fixed
// order. cfg is the parsed `powerline` setting; settings holds the merged top-level keys.
func unsupportedSettings(settings map[string]any, cfg powerlineConfig, env func(string) string) []string {
	var features []string
	add := func(feature string) { features = append(features, feature) }

	rawPowerline := asObject(settings["powerline"])
	if rawPowerline != nil {
		if rawPowerline.vals["welcome"] == true {
			add("the startup welcome (powerline.welcome)")
		}
		if cfg.SendDelayMs > 0 {
			add("delayed send (powerline.sendDelayMs)")
		}
	}
	if settings["bashMode"] != nil {
		add("bash mode (bashMode)")
	}
	if settings["showLastPrompt"] == true {
		add("the last-prompt line (showLastPrompt)")
	}
	if readEditorPerfOptions(env).Enabled {
		add("the editor performance profiler (POWERLINE_DEBUG_PERF)")
	}
	return features
}

// warnUnsupported notifies once per unsupported setting, at session start.
func (p *powerline) warnUnsupported() {
	if !p.h.HasUI() {
		return
	}
	for _, feature := range unsupportedSettings(readSettings(p.h.Cwd()), p.config, os.Getenv) {
		p.h.Notify(refusal(feature), "warning")
	}
}
