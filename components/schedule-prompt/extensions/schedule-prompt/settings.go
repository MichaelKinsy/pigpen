package schedule_prompt

import (
	"os"
	"path/filepath"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// Persistence for the settings (upstream: settings.ts).
//   - Global:  <agent dir>/schedule-prompts-settings.json, the user's defaults, never written here
//   - Project: <cwd>/.pi/schedule-prompts-settings.json, written by the UI, overrides the global file on load
//
// <agent dir> is PiG's agent directory, not the fixed ~/.pi/agent of the original's getAgentDir() (PORT.md D2).

const settingsFile = "schedule-prompts-settings.json"

// settings: nil pointers are unset members.
type settings struct {
	widgetVisible   *bool
	defaultJobScope string // "", "session" or "workdir"
}

func sanitizeSettings(raw any) settings {
	var out settings
	o, ok := raw.(*jsobj.Object)
	if !ok {
		return out
	}
	if b, ok := o.Get("widgetVisible").(bool); ok {
		out.widgetVisible = &b
	}
	if s, ok := o.Get("defaultJobScope").(string); ok && (s == "session" || s == "workdir") {
		out.defaultJobScope = s
	}
	return out
}

func readSettings(path string) settings {
	data, err := os.ReadFile(path)
	if err != nil {
		return settings{}
	}
	v, err := jsobj.Parse(string(data))
	if err != nil {
		logf("[pi-schedule-prompt] Ignoring malformed settings at %s: %v", path, err)
		return settings{}
	}
	return sanitizeSettings(v)
}

// agentSettingsDir is where the global file lives; a replaceable seam for tests.
var agentSettingsDir = defaultAgentDir

func loadSettings(cwd string) settings {
	global := readSettings(filepath.Join(agentSettingsDir(), settingsFile))
	project := readSettings(filepath.Join(cwd, ".pi", settingsFile))
	out := global
	if project.widgetVisible != nil {
		out.widgetVisible = project.widgetVisible
	}
	if project.defaultJobScope != "" {
		out.defaultJobScope = project.defaultJobScope
	}
	return out
}

// saveSettings applies a partial update to the project file: it reads the project file (not the merged state), spreads the change over
// it and writes it back, so the project file only holds deliberate overrides. false is an IO failure.
func saveSettings(cwd string, widgetVisible *bool, defaultJobScope string) bool {
	path := filepath.Join(cwd, ".pi", settingsFile)
	obj := jsobj.New()
	if data, err := os.ReadFile(path); err == nil {
		if v, err := jsobj.Parse(string(data)); err == nil {
			// `{...read(path)}` of the sanitized file: only the two known members survive
			cur := sanitizeSettings(v)
			if cur.widgetVisible != nil {
				obj.Set("widgetVisible", *cur.widgetVisible)
			}
			if cur.defaultJobScope != "" {
				obj.Set("defaultJobScope", cur.defaultJobScope)
			}
		} else {
			logf("[pi-schedule-prompt] Ignoring malformed settings at %s: %v", path, err)
		}
	}
	if widgetVisible != nil {
		obj.Set("widgetVisible", *widgetVisible)
	}
	if defaultJobScope != "" {
		obj.Set("defaultJobScope", defaultJobScope)
	}
	text, err := jsobj.Stringify(obj)
	if err != nil {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return false
	}
	return os.WriteFile(path, []byte(text), 0o666) == nil
}

// defaultAgentDir is PiG's writable agent directory: PIG_CODING_AGENT_DIR, else <PIG_HOME or ~/.pig>/agent; with PIG_USE_PI_DIRS=1 Pi's
// (PI_CODING_AGENT_DIR, else ~/.pi/agent). PiG's own rule (divergence D2), as in the tasks port.
func defaultAgentDir() string {
	if dir, err := sdk.AgentDir(); err == nil && dir != "" {
		return dir
	}
	if os.Getenv("PIG_USE_PI_DIRS") == "1" {
		if v := os.Getenv("PI_CODING_AGENT_DIR"); v != "" {
			return expandTilde(v)
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".pi", "agent")
	}
	if v := os.Getenv("PIG_CODING_AGENT_DIR"); v != "" {
		return expandTilde(v)
	}
	if v := os.Getenv("PIG_HOME"); v != "" {
		return filepath.Join(expandTilde(v), "agent")
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(expandTilde(v), "pig", "agent")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pig", "agent")
}

func expandTilde(p string) string {
	if p == "~" || (len(p) > 1 && p[:2] == "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[1:])
	}
	return p
}
