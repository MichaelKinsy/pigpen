package ui

import (
	"testing"
)

// Review of M10b (rich listings).

// A title can carry bidirectional controls (an unbalanced right-to-left override turns the rest of the row around in a
// terminal that does bidi): they are dropped like any other control that is not text.
func TestReviewM10bBidiControlsInATitleAreDropped(t *testing.T) {
	for _, s := range []string{"abc\u202edef", "abc\u2067def", "abc\u202adef", "abc\u2069def"} {
		if got := clean(s); got != "abcdef" {
			t.Errorf("clean(%q) = %q", s, got)
		}
	}
	if got := clean("שלום \u200fworld"); got != "שלום \u200fworld" {
		t.Errorf("a plain right-to-left text or mark was changed: %q", got)
	}
}
