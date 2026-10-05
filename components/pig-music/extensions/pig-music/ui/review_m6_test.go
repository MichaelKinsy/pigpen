package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// rev-pig-music-m6: the owner's finding on the Mac (search results, queue rows and library tracks without artist, album or
// length), checked against M6b's fix (ui/enrich.go): every row, three at a time, cached by video ID, never while drawing.

func TestEveryResultOfASearchGetsItsArtistAndLengthWithoutMovingTheCursor(t *testing.T) {
	r, src := enrichRig(t, nil)
	r.key("/")
	r.typed("x")
	r.key("enter")
	txt := r.text()
	for _, id := range []string{"id00", "id01", "id02", "id03", "id04"} {
		if !strings.Contains(txt, "Artist of "+id) {
			t.Errorf("row %s has no artist:\n%s", id, txt)
		}
	}
	seen := map[string]int{}
	for _, id := range src.enriched {
		if seen[id]++; seen[id] > 1 {
			t.Errorf("%s was asked for %d times", id, seen[id])
		}
	}
}

// Each answer costs a yt-dlp run of a few seconds: a whole list is asked for a few at a time, and each ID once.
func TestEnrichingAWholeListRunsAFewAtATimeAndAsksEachIDOnce(t *testing.T) {
	src := &countingEnricher{stubSource: &stubSource{}}
	var m tea.Model = New(Deps{Source: src, Player: &stubPlayer{}})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m, cmd := m.Update(searchDoneMsg{query: "", tracks: bareTracks(20)})
	pending := run(cmd)
	for i := 0; i < 10; i++ {
		var c tea.Cmd
		m, c = m.Update(tea.KeyPressMsg(keyOf("down")))
		pending = append(pending, run(c)...)
	}
	if len(pending) > 3 {
		t.Fatalf("%d enrichments on their way at once (%v); want at most 3", len(pending), src.started)
	}
	if src.started[0] != "id00" {
		t.Errorf("the first track asked for is %s; the selected row comes first", src.started[0])
	}
	for len(pending) > 0 {
		msg := pending[0]
		pending = pending[1:]
		var c tea.Cmd
		m, c = m.Update(msg)
		pending = append(pending, run(c)...)
	}
	seen := map[string]bool{}
	for _, id := range src.started {
		if seen[id] {
			t.Errorf("%s asked for twice", id)
		}
		seen[id] = true
	}
	if len(seen) != 20 {
		t.Errorf("%d of 20 results asked for: %v", len(seen), src.started)
	}
}

// mpv knows only URLs: a track queued before its row was filled in comes back from the player with its ID and title only.
func TestQueueRowsShowTheArtistAndLengthKnownForTheirVideoID(t *testing.T) {
	r, src := enrichRig(t, nil)
	q := bareTracks(4)
	r.state(playing(q, 1))
	r.key("tab", "tab") // the Player screen
	txt := r.text()
	for _, id := range []string{"id00", "id01", "id02", "id03"} {
		if !strings.Contains(txt, "Artist of "+id) {
			t.Errorf("queue row %s has no artist:\n%s", id, txt)
		}
	}
	n := len(src.enriched)
	r.state(playing(q, 2)) // the same queue again: nothing is asked twice
	if len(src.enriched) != n {
		t.Errorf("asked again: %v", src.enriched)
	}
}

// A network blip while a list is being filled in fails every track then asked about, quickly, one after the other. M6b
// remembered each failure for the life of the extension, so those rows stayed blank until a /reload. A new search (an
// explicit action) asks again for the rows that failed; it does not loop on its own.
func TestRowsWhoseEnrichmentFailedAreAskedAgainAfterANewSearchNotBefore(t *testing.T) {
	fail := map[string]error{}
	for _, id := range []string{"id00", "id01", "id02", "id03", "id04"} {
		fail[id] = errors.New("yt-dlp: unable to download webpage: network is unreachable")
	}
	r, src := enrichRig(t, fail)
	r.key("/")
	r.typed("x")
	r.key("enter")
	if len(src.enriched) != 5 {
		t.Fatalf("asked %v", src.enriched)
	}
	r.key("down", "up") // moving around asks nothing again
	if len(src.enriched) != 5 {
		t.Fatalf("failed rows asked again without a new search: %v", src.enriched)
	}
	for k := range fail { // the network is back
		delete(fail, k)
	}
	r.key("/")
	r.typed("y")
	r.key("enter")
	txt := r.text()
	for _, id := range []string{"id00", "id04"} {
		if !strings.Contains(txt, "Artist of "+id) {
			t.Errorf("row %s still blank after a new search:\n%s", id, txt)
		}
	}
}
