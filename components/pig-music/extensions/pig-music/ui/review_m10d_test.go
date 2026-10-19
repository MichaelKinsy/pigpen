package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// On the wide stage the queue rail is on screen beside the lyrics, its cursor drawn: A/B/R/T act on that row, like everywhere
// else the keys act on the row under the cursor.
func TestReviewOnTheWideStageTheKeysActOnTheQueueRailsCursor(t *testing.T) {
	r, src := navRig(t)
	m := r.model()
	m.deps.Lyrics = &stubLyrics{}
	r.m = m
	r.send(tea.WindowSizeMsg{Width: 160, Height: 40})
	r.state(playing(tracks(3), 2))
	r.key("tab", "tab")
	if _, _, rail := r.model().stageWidths(); rail == 0 {
		t.Fatal("no queue rail at 160 columns")
	}
	r.key("k", "A")
	if w, _ := src.calls(); len(w) == 0 || w[len(w)-1] != "id01" {
		t.Errorf("the rail's cursor was not used: %v", w)
	}
}
