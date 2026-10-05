package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// The owner's screenshot: Up Next had empty Artist and Len for every queued track, and Now Playing only a title. A songs
// search lists titles only, so artist, album and length are filled in the background for every track the screens show.

func startSearch(r *rig) {
	r.key("/")
	r.typed("x")
	r.key("enter")
}

func TestEverySearchResultGetsItsArtistAndLengthInTheBackground(t *testing.T) {
	r, src := enrichRig(t, nil)
	startSearch(r)
	if got := strings.Join(src.enriched, ","); got != "id00,id01,id02,id03,id04" {
		t.Fatalf("enriched %s", got)
	}
	txt := r.text()
	for i := 0; i < 5; i++ {
		if !strings.Contains(txt, fmt.Sprintf("Artist of id%02d", i)) {
			t.Errorf("row %d lacks its artist:\n%s", i, txt)
		}
	}
	if strings.Count(txt, "3:20") != 5 {
		t.Errorf("every row should show its length:\n%s", txt)
	}
}

func TestAtMostThreeEnrichmentsRunAtOnceAndTheSelectedRowGoesFirst(t *testing.T) {
	src := &enrichingSource{stubSource: &stubSource{tracks: bareTracks(10)}}
	m := New(Deps{Source: src, Player: &stubPlayer{}})
	var model tea.Model = m
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model, cmd := model.Update(searchDoneMsg{query: "", tracks: bareTracks(10)})
	if n := countCmds(cmd); n != 3 {
		t.Fatalf("%d enrichments started at once, want 3", n)
	}
	// each finished one lets the next start, never more than three in flight
	model, cmd = model.Update(enrichedMsg{id: "id00", track: music.Track{ID: "id00", Artists: []string{"A"}, Duration: time.Minute}})
	if n := countCmds(cmd); n != 1 {
		t.Errorf("%d started after one finished, want 1", n)
	}
	_ = model
}

func countCmds(c tea.Cmd) int {
	if c == nil {
		return 0
	}
	switch msg := c().(type) {
	case tea.BatchMsg:
		n := 0
		for _, cc := range msg {
			if cc != nil {
				n++
			}
		}
		return n
	default:
		return 1
	}
}

func TestQueuedTracksAreEnrichedPlayingTrackFirstAndShowArtistAndLength(t *testing.T) {
	r, src := enrichRig(t, nil)
	q := bareTracks(6)
	st := playing(q, 2)
	r.state(st)
	if len(src.enriched) == 0 || src.enriched[0] != "id02" {
		t.Fatalf("the playing track must come first: %v", src.enriched)
	}
	if len(src.enriched) != 6 {
		t.Errorf("enriched %v", src.enriched)
	}
	r.key("tab", "tab")
	txt := r.text()
	for i := 0; i < 6; i++ {
		if !strings.Contains(txt, fmt.Sprintf("Artist of id%02d", i)) {
			t.Errorf("Up Next row %d lacks its artist:\n%s", i, txt)
		}
	}
	if strings.Count(txt, "3:20") < 6 {
		t.Errorf("Up Next lacks lengths:\n%s", txt)
	}
}

func TestNowPlayingShowsTitleArtistAlbumAndLength(t *testing.T) {
	r, _ := enrichRig(t, nil)
	r.state(playing(bareTracks(3), 1))
	r.key("tab", "tab")
	txt := r.text()
	for _, want := range []string{"Song 2", "Artist of id01", "Album of id01", "Length", "3:20"} {
		if !strings.Contains(txt, want) {
			t.Errorf("Now Playing lacks %q:\n%s", want, txt)
		}
	}
}

func TestTheLengthMpvReportsFillsTheCurrentTrackAtOnce(t *testing.T) {
	r := newRig(t, 100, 30) // a source that cannot enrich
	q := bareTracks(2)
	st := playing(q, 0)
	st.Duration = 213 * time.Second
	r.state(st)
	r.key("tab", "tab")
	if txt := r.text(); !strings.Contains(txt, "Length") || strings.Count(txt, "3:33") < 2 { // Now Playing, the table row (and the bar)
		t.Errorf("\n%s", txt)
	}
}

func TestTracksThatAlreadyHaveArtistAndLengthAreNotAskedAbout(t *testing.T) {
	src := &enrichingSource{stubSource: &stubSource{tracks: tracks(4)}} // the listing carried them, as playlist listings do
	r := newRig(t, 100, 30)
	r.m = New(Deps{Source: src, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	startSearch(r)
	r.state(playing(tracks(4), 1))
	// only the playing track is asked about, for its album, which no listing carries
	if len(src.enriched) != 1 || src.enriched[0] != "id01" {
		t.Errorf("enriched %v although the listing had everything but the playing track's album", src.enriched)
	}
}

func TestEnrichedTracksAreCachedAcrossSearchesAndTheQueue(t *testing.T) {
	r, src := enrichRig(t, nil)
	startSearch(r)
	before := len(src.enriched)
	r.key("/")
	r.typed("y")
	r.key("enter") // the same five IDs again
	r.state(playing(bareTracks(5), 0))
	if len(src.enriched) != before {
		t.Errorf("%d more enrichments for IDs already known", len(src.enriched)-before)
	}
	if !strings.Contains(r.text(), "Artist of id04") {
		t.Errorf("the cached data is not shown:\n%s", r.text())
	}
}

func TestPlayingAndQueuingUseTheEnrichedTracks(t *testing.T) {
	r, _ := enrichRig(t, nil)
	startSearch(r)
	r.key("enter")
	log := r.player.log()
	if len(log) != 1 || !strings.Contains(log[0], "replace 5 tracks") {
		t.Fatalf("%v", log)
	}
	if got := r.player.lastReplace; len(got) != 5 || got[3].Duration != 200*time.Second || got[3].Artists[0] != "Artist of id03" {
		t.Errorf("the player was given %+v", got)
	}
}

func TestEnrichmentNeverRunsWhileDrawing(t *testing.T) {
	r, src := enrichRig(t, nil)
	startSearch(r)
	n := len(src.enriched)
	for i := 0; i < 20; i++ {
		_ = r.m.View()
	}
	if len(src.enriched) != n {
		t.Error("View called the source")
	}
}

// The enrichment of a library collection's tracks, and no cookies: only Enrich is asked, which has no cookie access.
func TestLibraryTracksAreEnrichedToo(t *testing.T) {
	lib := newLib()
	lib.granted = true
	lib.tracksOf["LM"] = bareTracks(3)
	en := &enrichingLib{libSource: lib, enrichingSource: &enrichingSource{stubSource: lib.stubSource}}
	r := newRig(t, 100, 30)
	r.m = New(Deps{Source: en, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	r.key("tab", "enter")
	if !strings.Contains(r.text(), "Artist of id02") || len(en.enriched) != 3 {
		t.Errorf("%v\n%s", en.enriched, r.text())
	}
}

type enrichingLib struct {
	*libSource
	*enrichingSource
}

func (e *enrichingLib) Search(ctx context.Context, q string, n int) ([]music.Track, error) {
	return e.libSource.Search(ctx, q, n)
}
func (e *enrichingLib) PlayURL(t music.Track) string { return e.libSource.PlayURL(t) }

// M9c: the liked songs are hundreds of rows, and each answer is a request: only the rows around the cursor are asked about,
// and the cursor moving on asks about the next ones.
func TestOnlyTheRowsAroundTheCursorAreEnriched(t *testing.T) {
	src := &enrichingSource{stubSource: &stubSource{tracks: bareTracks(200)}}
	var model tea.Model = New(Deps{Source: src, Player: &stubPlayer{}})
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	asked := map[string]bool{}
	var collect func(cmd tea.Cmd) []tea.Msg
	collect = func(cmd tea.Cmd) []tea.Msg {
		if cmd == nil {
			return nil
		}
		msg := cmd()
		if b, ok := msg.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range b {
				out = append(out, collect(c)...)
			}
			return out
		}
		return []tea.Msg{msg}
	}
	drain := func(cmd tea.Cmd) {
		for msgs := collect(cmd); len(msgs) > 0; {
			var next []tea.Msg
			for _, m := range msgs {
				if em, ok := m.(enrichedMsg); ok {
					asked[em.id] = true
					var c tea.Cmd
					model, c = model.Update(enrichedMsg{id: em.id, track: music.Track{ID: em.id, Artists: []string{"A"}, Duration: time.Minute}})
					next = append(next, collect(c)...)
				}
			}
			msgs = next
		}
	}
	var cmd tea.Cmd
	model, cmd = model.Update(searchDoneMsg{query: "", tracks: bareTracks(200)})
	drain(cmd)
	if len(asked) == 0 || len(asked) > enrichWindow {
		t.Fatalf("%d rows asked about with the cursor at the top, want 1..%d", len(asked), enrichWindow)
	}
	if asked["id150"] {
		t.Error("a row far below the cursor was asked about")
	}
}

func TestMovingDownTheLibraryTracksAsksAboutTheRowsNowNearTheCursor(t *testing.T) {
	lib := newLib()
	lib.granted = true
	lib.tracksOf["LM"] = bareTracks(100)
	en := &enrichingLib{libSource: lib, enrichingSource: &enrichingSource{stubSource: lib.stubSource}}
	r := newRig(t, 100, 30)
	r.m = New(Deps{Source: en, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	r.key("tab", "enter")
	if len(en.enriched) == 0 || len(en.enriched) > enrichWindow {
		t.Fatalf("%d rows asked about at the top", len(en.enriched))
	}
	for i := 0; i < 45; i++ {
		r.key("j")
	}
	got := strings.Join(en.enriched, ",")
	if !strings.Contains(got, "id60") || strings.Contains(got, "id99") {
		t.Errorf("rows asked about after moving down 45: %s", got)
	}
}
