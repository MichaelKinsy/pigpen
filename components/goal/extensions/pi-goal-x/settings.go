package pi_goal_x

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// The slice of goal-settings.ts the ported commands read: maxAutonomousRuns, disableContracts, autoSelectSingleGoal and
// strictExecutionContract (the scheduler restore reads it), from the project file, the global file and (for disableContracts)
// the environment. A key that is not valid is ignored, as in the original, which keeps the rest of the file. The original
// caches each file for the session; this port reads it when asked.

type goalSettings struct {
	maxAutonomousRuns       *float64
	disableContracts        bool
	autoSelectSingle        bool
	strictExecutionContract bool
}

var getenv = os.Getenv

func nonEmpty(s string) string { return jsTrim(s) }

func agentDir(home string) string {
	if o := nonEmpty(getenv("PI_CODING_AGENT_DIR")); o != "" {
		if filepath.IsAbs(o) {
			return filepath.Clean(o)
		}
		return filepath.Join(home, o)
	}
	return filepath.Join(home, ".pi", "agent")
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

func globalSettingsPath() string {
	home := homeDir()
	if o := nonEmpty(getenv("PI_GOAL_GLOBAL_SETTINGS_FILE")); o != "" {
		if filepath.IsAbs(o) {
			return filepath.Clean(o)
		}
		return filepath.Join(home, o)
	}
	return filepath.Join(agentDir(home), "pi-goal-x-settings.json")
}

func projectSettingsPath(cwd string) string {
	if o := nonEmpty(getenv("PI_GOAL_SETTINGS_FILE")); o != "" {
		if filepath.IsAbs(o) {
			return o
		}
		return filepath.Join(cwd, o)
	}
	return filepath.Join(cwd, ".pi", "pi-goal-x-settings.json")
}

type settingsLayer struct {
	maxRuns          *float64
	disableContracts *bool
	autoSelect       *bool
	strict           *bool
}

func asBool(v any) *bool {
	switch x := v.(type) {
	case bool:
		return &x
	case string:
		if x == "true" || x == "false" {
			b := x == "true"
			return &b
		}
	}
	return nil
}

var digitsOnly = regexp.MustCompile(`^[0-9]+$`)

func readLayer(file string) settingsLayer {
	var l settingsLayer
	data, err := os.ReadFile(file)
	if err != nil {
		return l
	}
	v, err := parseJSON(data)
	if err != nil {
		return l
	}
	o, ok := v.(*jsObject)
	if !ok {
		return l
	}
	if x, ok := o.vals["maxAutonomousRuns"]; ok {
		n := -1.0
		switch t := x.(type) {
		case float64:
			n = t
		case string:
			if s := jsTrim(t); digitsOnly.MatchString(s) {
				n, _ = strconv.ParseFloat(s, 64)
			}
		}
		if isSafeInteger(n) && n >= 0 {
			l.maxRuns = &n
		}
	}
	l.disableContracts = asBool(o.vals["disableContracts"])
	l.autoSelect = asBool(o.vals["autoSelectSingleGoal"])
	l.strict = asBool(o.vals["strictExecutionContract"])
	return l
}

func loadGoalSettings(cwd string) goalSettings {
	project, global := readLayer(projectSettingsPath(cwd)), readLayer(globalSettingsPath())
	var s goalSettings
	switch {
	case project.maxRuns != nil:
		s.maxAutonomousRuns = project.maxRuns
	case global.maxRuns != nil:
		s.maxAutonomousRuns = global.maxRuns
	}
	switch envv := asBool(getenv("PI_GOAL_DISABLE_CONTRACTS")); {
	case envv != nil:
		s.disableContracts = *envv
	case project.disableContracts != nil:
		s.disableContracts = *project.disableContracts
	case global.disableContracts != nil:
		s.disableContracts = *global.disableContracts
	}
	switch {
	case project.autoSelect != nil:
		s.autoSelectSingle = *project.autoSelect
	case global.autoSelect != nil:
		s.autoSelectSingle = *global.autoSelect
	}
	switch {
	case project.strict != nil:
		s.strictExecutionContract = *project.strict
	case global.strict != nil:
		s.strictExecutionContract = *global.strict
	}
	return s
}
