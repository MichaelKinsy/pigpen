package ui

import (
	"strings"
	"testing"
)

// Review of M10b (rich listings).

// Pressing / while an album is open must show what is typed (the prompt keeps the last query); esc goes back to the album.
func TestReviewM10bTypingASearchOverAnOpenedAlbumShowsThePrompt(t *testing.T) {
	r, _ := entRig(t)
	r.key("2", "enter")
	r.key("/")
	r.typed("abc")
	if text := r.text(); !strings.Contains(text, "/ daft punkabc") {
		t.Errorf("the prompt is hidden by the album:\n%s", text)
	}
	r.key("esc")
	if text := r.text(); !strings.Contains(text, "Give Life Back to Music") {
		t.Errorf("esc did not go back to the album:\n%s", text)
	}
}
