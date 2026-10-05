package angrypigs

import "strings"

// The fake classifier stands in for Jev in tests: a deterministic rule that answers the
// question the demo script asks ("which adjustment moves the aimed shot toward the nearest
// bird?") from game_state's words alone, as the game's instructions say: fire when the shot
// is on target, power_up when it is short (aim_up at full power), power_down when it is long.
// It reads the facts a person would, never the game.

// fakeJev returns the action and its probability.
func fakeJev(state map[string]any) (string, float64) {
	switch {
	case state["flying"] == true || state["game_over"] == true:
		return "none", 0.99
	case state["level_done"] == true:
		return "next_level", 0.99
	}
	versus, _ := state["landing_vs_target"].(string)
	power := 0.0
	if pig, ok := state["pig"].(map[string]any); ok {
		switch p := pig["power"].(type) {
		case int:
			power = float64(p)
		case float64:
			power = p
		}
	}
	switch {
	case versus == "on target":
		return "fire", 0.96
	case strings.HasPrefix(versus, "short") && power < 100:
		return "power_up", 0.93
	case strings.HasPrefix(versus, "short"):
		return "aim_up", 0.85
	case strings.HasPrefix(versus, "long"):
		return "power_down", 0.9
	}
	return "none", 0.6
}
