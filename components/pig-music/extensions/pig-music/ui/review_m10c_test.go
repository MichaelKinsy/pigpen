package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// While the player is hidden the lyrics are not followed (each position report would wrap the whole sheet for nobody); shown
// again, the view is at the current line at once.
func TestReviewHiddenLyricsAreNotFollowedAndCatchUpWhenShown(t *testing.T) {
	var lines []music.LyricLine
	for i := 0; i < 100; i++ {
		lines = append(lines, music.LyricLine{At: sec(float64(i * 2)), End: sec(float64(i*2 + 2)), Text: "line"})
	}
	lp := &stubLyrics{byID: map[string]music.Lyrics{"id00": {Synced: true, Lines: lines}}}
	r, _ := lyricRig(t, lp, nil)
	r.send(ShowPlayerMsg{})
	r.state(at(tracks(3), 0, sec(2)))
	r.send(ClosedMsg{})
	before := r.model().lyr.top
	r.state(at(tracks(3), 0, sec(120)))
	if got := r.model().lyr.top; got != before {
		t.Fatalf("followed while hidden: %d -> %d", before, got)
	}
	r.send(OpenedMsg{})
	if got := r.model().lyr.top; got < 40 {
		t.Fatalf("shown again at row %d, want the current line's (~55)", got)
	}
}

// A sheet is wrapped once for a width, not on every frame and every position report (2000 lines took 6.7 ms a wrap, twice a
// frame at up to 15 frames a second).
func TestReviewTheSheetIsWrappedOncePerWidth(t *testing.T) {
	lp := &stubLyrics{byID: map[string]music.Lyrics{"id00": synced()}}
	r, _ := lyricRig(t, lp, nil)
	r.send(ShowPlayerMsg{})
	r.state(at(tracks(3), 0, sec(6)))
	n := wraps.Load()
	for i := 0; i < 10; i++ {
		r.state(at(tracks(3), 0, sec(6+float64(i)/10)))
		r.send(TickMsg{})
		_ = r.screen()
	}
	if got := wraps.Load() - n; got != 0 {
		t.Errorf("%d wraps for one sheet at one width", got)
	}
	r.send(tea.WindowSizeMsg{Width: 90, Height: 30})
	_ = r.screen()
	if got := wraps.Load() - n; got != 1 {
		t.Errorf("%d wraps after a resize, want 1", got)
	}
}
