package tui

import (
	"testing"
)

// keys.ts:505 KeyEventType is the closed union "press" | "repeat" | "release".
func TestKeyEventTypeValues(t *testing.T) {
	for got, want := range map[KeyEventType]string{KeyEventPress: "press", KeyEventRepeat: "repeat", KeyEventRelease: "release"} {
		if string(got) != want {
			t.Errorf("KeyEventType %q, want %q", got, want)
		}
	}
}
