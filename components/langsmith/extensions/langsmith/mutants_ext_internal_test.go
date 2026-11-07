package langsmith

// Internal tests added by the mutation proof (port/mutations.md).

import "testing"

func TestMutPiVersionSeparatorsAreDots(t *testing.T) {
	for _, v := range []string{"0x81y0", "0 81 0", "0.81x0", "0x81.0"} {
		if piEmitsAgentSettled(v) {
			t.Errorf("%q does not start with <digits>.<digits>.<digits>", v)
		}
	}
}
