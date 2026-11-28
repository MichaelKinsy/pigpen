package modes

import (
	"fmt"
	"math"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Setting reads an omp extension setting for ctx's session.
// Wired by the integrator to `settings.Any`.
var Setting func(ctx sdk.Context, key string) any

// SetSetting updates an omp extension setting for ctx's session.
// Wired by the integrator to `settings.Set(ctx, key, value, persist)`.
var SetSetting func(ctx sdk.Context, key string, value any, persist bool) error

func readModeSettingBool(ctx sdk.Context, key string, fallback bool) bool {
	if Setting == nil {
		return fallback
	}
	if v, ok := Setting(ctx, key).(bool); ok {
		return v
	}
	return fallback
}

func readModeSettingString(ctx sdk.Context, key string) string {
	if Setting == nil {
		return ""
	}
	if v, ok := Setting(ctx, key).(string); ok {
		return v
	}
	return ""
}

func readModeSettingInt(ctx sdk.Context, key string, fallback int) int {
	if Setting == nil {
		return fallback
	}
	switch v := Setting(ctx, key).(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return int(math.Floor(v))
		}
	}
	return fallback
}

func isSkillfulEnabled(ctx sdk.Context) bool {
	return readModeSettingBool(ctx, "skillful", true)
}

func isPlanEnabledSetting(ctx sdk.Context) bool {
	return readModeSettingBool(ctx, "plan.enabled", true)
}

func isPlanAutosaveEnabled(ctx sdk.Context) bool {
	return readModeSettingBool(ctx, "plan.autosave", false)
}

func isGoalEnabledSetting(ctx sdk.Context) bool {
	return readModeSettingBool(ctx, "goal.enabled", true)
}

func loopConditionTimeoutMs(ctx sdk.Context) int {
	ms := readModeSettingInt(ctx, "loop.conditionTimeoutMs", defaultLoopConditionTimeoutMs)
	if ms < 0 {
		return defaultLoopConditionTimeoutMs
	}
	return ms
}

// resolveMagicKeywordSettings reads `magicKeywords.enabled` and per-keyword toggles
// (`magicKeywords.<id>`) via `modes.Setting`, falling back to `DefaultMagicKeywordSettings()`
// when `Setting == nil`.
func resolveMagicKeywordSettings(ctx sdk.Context) MagicKeywordSettings {
	if Setting == nil {
		return DefaultMagicKeywordSettings()
	}
	s := MagicKeywordSettings{
		Enabled: readModeSettingBool(ctx, "magicKeywords.enabled", true),
	}
	if !s.Enabled {
		return s
	}
	for _, kw := range MagicKeywords {
		if !readModeSettingBool(ctx, "magicKeywords."+kw.ID, true) {
			if s.Disabled == nil {
				s.Disabled = make(map[string]bool)
			}
			s.Disabled[kw.ID] = true
		}
	}
	return s
}

// handleSkillfulCommand implements `/skillful [on|off|status]`.
// upstream: packages/coding-agent/src/slash-commands/builtin-modes.ts:564-617
func (m *manager) handleSkillfulCommand(ctx sdk.Context, args string) error {
	arg := strings.ToLower(strings.TrimSpace(args))
	current := isSkillfulEnabled(ctx)

	if arg == "status" {
		state := "off"
		if current {
			state = "on"
		}
		if ctx.HasUI() {
			notifyInfo(ctx, fmt.Sprintf("Skill listing: %s.", state))
		} else {
			notifyInfo(ctx, fmt.Sprintf("Skill listing: %s (session override; default from the skillful setting).", state))
		}
		return nil
	}

	var next bool
	switch arg {
	case "", "toggle":
		next = !current
	case "on":
		next = true
	case "off":
		next = false
	default:
		notifyInfo(ctx, "Usage: /skillful [on|off|status]")
		return nil
	}

	if SetSetting != nil {
		if err := SetSetting(ctx, "skillful", next, false); err != nil {
			notifyWarn(ctx, fmt.Sprintf("Failed to update skillful setting: %v", err))
			return nil
		}
	}

	if next {
		notifyInfo(ctx, "Skill listing enabled for this session.")
	} else {
		notifyInfo(ctx, "Skill listing disabled for this session.")
	}
	return nil
}
