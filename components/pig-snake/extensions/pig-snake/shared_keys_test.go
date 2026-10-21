package pig_snake

import (
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/termgame"
)

// Cases Pig Snake added to its own copy of the termgame package, now the shared one (pig-play, libraries/termgame): WASD arrive
// as text, empty input is nothing, a line feed is enter, and the overlay is the whole terminal.
func TestTheSharedParseKeyDecodesTheKeysPigSnakeSteersWith(t *testing.T) {
	for _, tc := range []struct {
		in   string
		key  termgame.Key
		text rune
	}{
		{"w", termgame.KeyText, 'w'}, {"\x1b[97u", termgame.KeyText, 'a'}, {"", termgame.KeyNone, 0}, {"\n", termgame.KeyEnter, 0},
		{"\x1b[A", termgame.KeyUp, 0}, {"\x1bOB", termgame.KeyDown, 0}, {"\x1b[1;1:3A", termgame.KeyNone, 0},
	} {
		if key, text := termgame.ParseKey(tc.in); key != tc.key || text != tc.text {
			t.Errorf("ParseKey(%q) = %v %q, want %v %q", tc.in, key, text, tc.key, tc.text)
		}
	}
}

func TestTheSharedOverlayIsTheWholeTerminalMinusItsBox(t *testing.T) {
	if w, h := termgame.Viewport(80, 24); w != 78 || h != 22 {
		t.Errorf("Viewport(80, 24) = %d, %d", w, h)
	}
	if o := termgame.Overlay("Pig Snake"); !o.Overlay || o.WidthFraction != 1 || o.HeightFraction != 1 || o.Title != "Pig Snake" {
		t.Errorf("overlay options = %+v", o)
	}
}
