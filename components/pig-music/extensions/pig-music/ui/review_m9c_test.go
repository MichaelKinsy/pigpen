package ui

import (
	tea "charm.land/bubbletea/v2"

	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Test gaps found by mutation in rev-pig-music-m9c (both pass on the M9c code; each failed with its mutation).

// A page whose first row is not the row after what is shown (an answer from before the collection was reopened, or one
// that raced another) is dropped, not appended in the wrong place.
func TestAPageThatDoesNotFollowWhatIsShownIsNotMixedIn(t *testing.T) {
	r, _ := pagingRig(t, 566)
	before := len(r.m.(Model).lib.tracks)
	stale := []music.Track{{ID: "staleid0001", Title: "Stale row", Artists: []string{"A"}, Duration: 60e9}}
	r.send(libTracksMsg{id: "LM", from: before - 20, tracks: stale, more: true, paged: true})
	if got := len(r.m.(Model).lib.tracks); got != before {
		t.Fatalf("%d rows after a stale page, want %d", got, before)
	}
	if strings.Contains(r.text(), "Stale row") {
		t.Errorf("the stale page is shown:\n%s", r.text())
	}
}

// Moving the cursor in the Player's queue asks about the rows now near it, as in the results and the library.
func TestMovingInTheQueueAsksAboutTheRowsNowNearTheCursor(t *testing.T) {
	r := newRig(t, 100, 30)
	src := &enrichingSource{stubSource: r.source}
	r.m = New(Deps{Source: src, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	m := r.m.(Model)
	m.tab = tabPlayer
	r.m = m
	queue := bareTracks(100)
	r.state(music.State{Connected: true, Queue: queue, Index: 0})
	if len(src.enriched) == 0 || len(src.enriched) > enrichWindow+1 {
		t.Fatalf("%d rows asked about with the cursor at the top", len(src.enriched))
	}
	for i := 0; i < 45; i++ {
		r.key("j")
	}
	got := strings.Join(src.enriched, ",")
	if !strings.Contains(got, fmt.Sprintf("id%02d", 60)) || strings.Contains(got, "id99") {
		t.Errorf("rows asked about after moving down 45: %s", got)
	}
}
