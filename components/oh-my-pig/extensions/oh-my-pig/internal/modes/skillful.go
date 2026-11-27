package modes

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Setting reads an omp extension setting for ctx's session.
// Wired by the integrator to `settings.Any`.
var Setting func(ctx sdk.Context, key string) any

// SetSetting updates an omp extension setting for ctx's session.
// Wired by the integrator to `settings.Set(ctx, key, value, persist)`.
var SetSetting func(ctx sdk.Context, key string, value any, persist bool) error

func isSkillfulEnabled(ctx sdk.Context) bool {
	if Setting == nil {
		return true
	}
	if v, ok := Setting(ctx, "skillful").(bool); ok {
		return v
	}
	return true
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
