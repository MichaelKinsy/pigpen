package pi_goal_x

import (
	"os"
	"sort"
	"strings"
)

// What this port does not do yet is refused, never ignored. The message names the working alternative: the original, which
// runs unchanged in PiG. Package name and version come from package.json and provenance.json.
const (
	packageName      = "@pi-in-go/pigpen-goal"
	originalSpec     = "npm:pi-goal-x@0.32.3"
	refusalDirective = "Install the original extension, " + originalSpec + ", which runs unchanged in PiG."
)

func refusal(feature string) string {
	return packageName + " (Go port) does not support " + feature + " yet. " + refusalDirective
}

// unportedCommands are the commands of the original that this port does not register as working commands; each answers with the
// refusal (the original's descriptions, so that the palette still lists them).
var unportedCommands = []struct{ name, desc, feature string }{
	{"goal", "Draft a regular goal with clarification, task planning, and confirmation.", "guided goal drafting (/goal)"},
	{"sisyphus", "Draft a Sisyphus goal with clarification, task planning, and confirmation.", "guided Sisyphus goal drafting (/sisyphus)"},
	{"goal-cancel", "Cancel the in-progress guided draft without creating or modifying a goal.", "guided goal drafting (/goal-cancel)"},
	{"goal-status", "Show the unified goal dashboard (read-only). Append \"verbose\" for full detail or \"health\" for storage/runtime checks.", "the goal dashboard (/goal-status)"},
	{"goal-refresh", "Re-read goal storage caches (pool, ledger, settings) from disk and report what changed. Picks up external edits to .pi files — no file watchers needed.", "/goal-refresh"},
	{"goal-recovery", "Read-only storage/recovery report (malformed goal files, malformed ledger lines, stale locks, orphaned snapshot data). Append \"repair\" to remove stale locks and refresh the pool snapshot after confirmation (with backup).", "/goal-recovery"},
	{"goal-focus", "Choose which open goal this session focuses on.", "/goal-focus"},
	{"goal-settings", "Open pi-goal settings (auditor provider/model/thinking level).", "the goal settings menu (/goal-settings)"},
	{"goal-tweak", "Revise the current goal or its token budget with confirmation.", "goal revision (/goal-tweak)"},
	{"goal-resume", "Continue now and renew the configured autonomous-run allowance.", "automatic goal continuation (/goal-resume)"},
}

const continuationFeature = "automatic goal continuation (maxAutonomousRuns other than 0)"

// unportedSettings lists the settings of the original that this port does not implement and that are present in the project
// file, the global file or the environment. The four settings the port reads are not among them.
func unportedSettings(cwd string) []string {
	known := map[string]bool{"maxAutonomousRuns": true, "disableContracts": true, "autoSelectSingleGoal": true, "strictExecutionContract": true}
	found := map[string]bool{}
	for _, file := range []string{projectSettingsPath(cwd), globalSettingsPath()} {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		v, err := parseJSON(data)
		if err != nil {
			continue
		}
		o, ok := v.(*jsObject)
		if !ok {
			continue
		}
		for k := range o.vals {
			if !known[k] {
				found[k] = true
			}
		}
	}
	for _, name := range []string{"PI_GOAL_ROOT", "PI_GOAL_DISABLE_TASKS", "PI_GOAL_OBJECTIVE_MAX_CHARS", "PI_GOAL_NETWORK_RECOVERY_MAX_ATTEMPTS", "PI_GOAL_NETWORK_RECOVERY_MAX_DELAY_MS", "PI_GOAL_PROMPT_PROFILE", "PI_GOAL_AUTO_CONFIRM"} {
		if getenv(name) != "" {
			found[name] = true
		}
	}
	out := make([]string, 0, len(found))
	for k := range found {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func unportedSettingsFeature(keys []string) string {
	if len(keys) == 1 {
		return "the setting " + keys[0]
	}
	return "the settings " + strings.Join(keys, ", ")
}
