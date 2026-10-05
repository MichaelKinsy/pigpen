package angrypigs

// FakeJev is the deterministic stand-in for the Jev classifier, for the external tests.
// State arrives from JSON there, so targets are decoded as []any.
func FakeJev(state map[string]any) (string, float64) {
	if targets, ok := state["targets"].([]any); ok {
		typed := make([]map[string]any, len(targets))
		for i, t := range targets {
			typed[i] = t.(map[string]any)
		}
		copied := make(map[string]any, len(state))
		for k, v := range state {
			copied[k] = v
		}
		copied["targets"] = typed
		state = copied
	}
	return fakeJev(state)
}
