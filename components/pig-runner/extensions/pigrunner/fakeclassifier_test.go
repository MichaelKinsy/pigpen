package pigrunner

import "strings"

// The fake classifier stands in for Jev in tests: a deterministic rule that answers the
// question the demo script asks ("given the next obstacle, which action avoids it?") from
// game_state alone, as the game's instructions say: jump over a pie and duck under a flying
// pie when decide_now is true, otherwise none. It reads the facts a person would, never the
// game.

// fakeJev returns the action and its probability.
func fakeJev(state map[string]any) (string, float64) {
	if state["game_over"] == true {
		return "restart", 0.99
	}
	if state["running"] != true || state["decide_now"] != true {
		return "none", 0.9
	}
	kind, _ := state["next_obstacle"].(map[string]any)["kind"].(string)
	switch {
	case strings.HasPrefix(kind, "pie_"):
		return "jump", 0.97
	case strings.HasPrefix(kind, "flying_pie_"):
		return "duck", 0.95
	}
	return "none", 0.9
}
