package termgame

import "testing"

// Twins of PiG d86eb93 piglets/standard/internal/termgame/termgame_test.go.

func TestParseKeyDecodesLegacyApplicationAndKitty(t *testing.T) {
	cases := []struct {
		in   string
		key  Key
		text rune
	}{
		{"\x1b[A", KeyUp, 0}, {"\x1bOA", KeyUp, 0}, {"\x1b[1;1:1A", KeyUp, 0}, {"\x1b[1;1:2A", KeyUp, 0},
		{"\x1b[B", KeyDown, 0}, {"\x1b[C", KeyRight, 0}, {"\x1b[1;2D", KeyLeft, 0},
		{"\x1b[1;1:3A", KeyNone, 0}, {"\x1b[113;1:3u", KeyNone, 0},
		{"\x1b", KeyEscape, 0}, {"\x1b[27u", KeyEscape, 0}, {"\x1b[27;1:1u", KeyEscape, 0},
		{"\r", KeyEnter, 0}, {"\x1b[13u", KeyEnter, 0},
		{" ", KeyText, ' '}, {"q", KeyText, 'q'}, {"\x1b[113u", KeyText, 'q'}, {"\x1b[32u", KeyText, ' '},
		{"\x1b[113;5u", KeyNone, 0}, {"\x03", KeyNone, 0}, {"ab", KeyNone, 0},
		// Pig Snake additions: WASD arrive as text, and empty input is nothing.
		{"w", KeyText, 'w'}, {"\x1b[97u", KeyText, 'a'}, {"", KeyNone, 0}, {"\n", KeyEnter, 0},
	}
	for _, tc := range cases {
		key, text := ParseKey(tc.in)
		if key != tc.key || text != tc.text {
			t.Errorf("ParseKey(%q) = %v %q, want %v %q", tc.in, key, text, tc.key, tc.text)
		}
	}
}

func TestViewportSubtractsTheOverlayBox(t *testing.T) {
	for _, tc := range []struct{ w, h, wantW, wantH int }{
		{80, 24, 78, 22}, {1, 1, 1, 1}, {120, 0, 118, FallbackHeight - 2},
	} {
		if w, h := Viewport(tc.w, tc.h); w != tc.wantW || h != tc.wantH {
			t.Errorf("Viewport(%d, %d) = %d, %d", tc.w, tc.h, w, h)
		}
	}
	options := Overlay("Game")
	if !options.Overlay || options.WidthFraction != 1 || options.HeightFraction != 1 || options.Title != "Game" {
		t.Fatalf("overlay options = %+v", options)
	}
}

// Gap, recorded as a named skipped twin: Pig Snake takes no mouse input, so it
// has no ParseMouse/ViewCell. Upstream uses them for Angry Pigs' slingshot drag.
func TestParseMouseDecodesSGRReports(t *testing.T) {
	t.Skip("gap: Pig Snake is keyboard only; upstream ParseMouse/ViewCell serve Angry Pigs' drag aiming")
}
