package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// navSource is a Source with artist, album, radio and related pages. watches maps a video ID to what YouTube Music says of it.
type navSource struct {
	*stubSource
	mu      sync.Mutex
	watches map[string]music.Watch
	pages   map[string]music.Page
	watched []string
	opened  []music.Entity
	ctxs    map[string]context.Context
}

func (n *navSource) Watch(ctx context.Context, id string) (music.Watch, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.watched = append(n.watched, id)
	n.ctxs["watch "+id] = ctx
	w, ok := n.watches[id]
	if !ok {
		return music.Watch{}, errors.New("YouTube Music did not answer")
	}
	return w, nil
}

func (n *navSource) OpenPage(ctx context.Context, e music.Entity) (music.Page, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.opened = append(n.opened, e)
	n.ctxs["open "+e.ID] = ctx
	p, ok := n.pages[e.ID]
	if !ok {
		return music.Page{}, errors.New("no such page")
	}
	p.Kind, p.ID = e.Kind, e.ID
	return p, nil
}

func (n *navSource) calls() (watched []string, opened []music.Entity) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.watched...), append([]music.Entity(nil), n.opened...)
}

const (
	dpID  = "UCdaftpunkxxxxxxxxxxxxx"
	pharr = "UCpharrellxxxxxxxxxxxxx"
	ramID = "MPREb_ramxxxxxxxx"
	relID = "MPTRt_relatedxxxx"
)

func navRig(t *testing.T) (*rig, *navSource) {
	t.Helper()
	song := func(id, title string) music.Track {
		return music.Track{ID: id, Title: title, Artists: []string{"Daft Punk"}, Album: "Random Access Memories", Duration: 200 * time.Second}
	}
	src := &navSource{
		stubSource: &stubSource{tracks: tracks(3)},
		ctxs:       map[string]context.Context{},
		watches: map[string]music.Watch{
			"id00": {Artists: []music.Entity{{Kind: "artist", ID: dpID, Title: "Daft Punk"}}, Album: music.Entity{Kind: "album", ID: ramID, Title: "Random Access Memories"},
				Radio: []music.Track{song("id00", "Song 1"), song("rad0001", "Radio One"), song("rad0002", "Radio Two")}, RelatedID: relID},
			"id01": {Artists: []music.Entity{{Kind: "artist", ID: dpID, Title: "Daft Punk"}, {Kind: "artist", ID: pharr, Title: "Pharrell Williams"}}},
			"id02": {Artists: []music.Entity{{Kind: "artist", ID: dpID, Title: "Daft Punk"}}}, // a video: no album, nothing related
		},
		pages: map[string]music.Page{
			dpID: {Title: "Daft Punk", Subtitle: "80.8M monthly audience", Sections: []music.Section{
				{Title: "Top songs", Tracks: []music.Track{song("top0001", "Get Lucky"), song("top0002", "Instant Crush")}},
				{Title: "Albums", Entities: []music.Entity{{Kind: "album", ID: ramID, Title: "Random Access Memories", Subtitle: "Album • 2013"}}},
				{Title: "Fans might also like", Entities: []music.Entity{{Kind: "artist", ID: pharr, Title: "Pharrell Williams"}}},
			}},
			ramID: {Title: "Random Access Memories", Subtitle: "Album • 2013", Sections: []music.Section{{Title: "Tracks", Tracks: []music.Track{song("alb0001", "Give Life Back to Music"), song("alb0002", "The Game of Love"), song("alb0003", "Giorgio by Moroder")}}}},
			pharr: {Title: "Pharrell Williams", Sections: []music.Section{{Title: "Top songs", Tracks: []music.Track{song("ph00001", "Happy")}}}},
			relID: {Sections: []music.Section{{Title: "You might also like", Tracks: []music.Track{song("rel0001", "Related One")}}}},
		},
	}
	r := newRig(t, 100, 30)
	r.source = src.stubSource
	r.m = New(Deps{Source: src, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	r.key("/")
	r.typed("daft")
	r.key("enter")
	return r, src
}

func TestAGoesToTheArtistPageOfTheSelectedSong(t *testing.T) {
	r, src := navRig(t)
	r.key("A")
	text := r.text()
	for _, want := range []string{"Search › Daft Punk", "80.8M monthly audience", "Top songs", "Get Lucky", "Albums", "Random Access Memories", "Fans might also like", "Pharrell Williams"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	if w, o := src.calls(); len(w) != 1 || w[0] != "id00" || len(o) != 1 || o[0].ID != dpID {
		t.Errorf("watched %v opened %v", w, o)
	}
}

func TestEscGoesBackOnePageAtATimeAndAtTheRootQHides(t *testing.T) {
	r, _ := navRig(t)
	r.key("A", "j", "j", "enter") // the artist, down to the album, open it
	if text := r.text(); !strings.Contains(text, "Search › Daft Punk › Random Access Memories") || !strings.Contains(text, "Give Life Back to Music") {
		t.Fatalf("not the album:\n%s", text)
	}
	r.key("esc")
	if text := r.text(); !strings.Contains(text, "Search › Daft Punk") || strings.Contains(text, "Give Life Back") || r.quit {
		t.Fatalf("not back on the artist:\n%s", text)
	}
	r.key("esc")
	if got := r.model(); len(got.pages) != 0 || r.quit || !strings.Contains(r.text(), "Song 1") {
		t.Fatalf("not back on the results: %d pages, quit %v\n%s", len(got.pages), r.quit, r.text())
	}
	r.key("esc")
	if !r.quit {
		t.Error("esc on the root does not hide the player")
	}
}

func TestBackspaceGoesBackToo(t *testing.T) {
	r, _ := navRig(t)
	r.key("A", "backspace")
	if len(r.model().pages) != 0 {
		t.Error("still on the page")
	}
}

func TestQHidesFromAPage(t *testing.T) {
	r, _ := navRig(t)
	r.key("A", "q")
	if !r.quit {
		t.Error("q did not hide")
	}
}

func TestTheCursorStaysOnTheSongAfterComingBack(t *testing.T) {
	r, _ := navRig(t)
	r.key("j", "j", "A", "esc", "A")
	if got := r.model(); got.resCur != 2 {
		t.Errorf("cursor %d", got.resCur)
	}
}

func TestEnterOnASongOfAPagePlaysThatSection(t *testing.T) {
	r, _ := navRig(t)
	r.key("A", "j", "enter")
	if got := strings.Join(r.player.log(), ","); !strings.Contains(got, "replace 2 tracks at 1 first=top0001") {
		t.Errorf("calls %s", got)
	}
	if got := r.model(); got.tab != tabPlayer || len(got.pages) != 0 {
		t.Errorf("tab %s pages %d", got.tab, len(got.pages))
	}
}

func TestPPlaysAllAndSPlaysItShuffledAndANeitherChangesTheShuffleMode(t *testing.T) {
	r, _ := navRig(t)
	r.key("A", "j", "j", "enter", "P")
	if got := strings.Join(r.player.log(), ","); !strings.Contains(got, "replace 3 tracks at 0 first=alb0001") {
		t.Errorf("P: %s", got)
	}
	r, _ = navRig(t)
	r.key("A", "j", "j", "enter", "S")
	seen := map[string]bool{}
	for _, tr := range r.player.lastReplace {
		seen[tr.ID] = true
	}
	if len(r.player.lastReplace) != 3 || !seen["alb0001"] || !seen["alb0002"] || !seen["alb0003"] {
		t.Errorf("S: %v", r.player.lastReplace)
	}
	if got := strings.Join(r.player.log(), ","); strings.Contains(got, "shuffle") {
		t.Errorf("S changed the shuffle mode: %s", got)
	}
}

func TestAOnAPageAddsTheSongToTheQueue(t *testing.T) {
	r, _ := navRig(t)
	r.key("A", "a")
	if got := strings.Join(r.player.log(), ","); !strings.Contains(got, "enqueue") {
		t.Errorf("calls %s", got)
	}
}

func TestSeveralArtistsOfASongAreAChoiceWithoutAnotherRequest(t *testing.T) {
	r, src := navRig(t)
	r.key("j", "A")
	text := r.text()
	if !strings.Contains(text, "Artists of Song 2") || !strings.Contains(text, "Daft Punk") || !strings.Contains(text, "Pharrell Williams") {
		t.Fatalf("no choice:\n%s", text)
	}
	if _, o := src.calls(); len(o) != 0 {
		t.Errorf("opened %v", o)
	}
	r.key("j", "enter")
	if text := r.text(); !strings.Contains(text, "Search › Artists of Song 2 › Pharrell Williams") || !strings.Contains(text, "Happy") {
		t.Errorf("not Pharrell:\n%s", text)
	}
}

func TestBGoesToTheAlbumAndSaysSoWhenThereIsNone(t *testing.T) {
	r, _ := navRig(t)
	r.key("B")
	if text := r.text(); !strings.Contains(text, "Search › Random Access Memories") || !strings.Contains(text, "The Game of Love") {
		t.Fatalf("not the album:\n%s", text)
	}
	r.key("esc", "j", "j", "B")
	got := r.model()
	if len(got.pages) != 0 || !strings.Contains(got.status, "not on an album") {
		t.Errorf("%d pages, status %q", len(got.pages), got.status)
	}
}

func TestRShowsTheRadioWithTheSongFirstAndTPlaysItFromTheStart(t *testing.T) {
	r, _ := navRig(t)
	r.key("R")
	if text := r.text(); !strings.Contains(text, "Search › Radio: Song 1") || !strings.Contains(text, "Radio One") || !strings.Contains(text, "Radio Two") {
		t.Fatalf("no radio:\n%s", text)
	}
	r.key("P")
	if got := strings.Join(r.player.log(), ","); !strings.Contains(got, "replace 3 tracks at 0 first=id00") {
		t.Errorf("calls %s", got)
	}
}

func TestTShowsTheRelatedPageAndSaysSoWhenThereIsNone(t *testing.T) {
	r, src := navRig(t)
	r.key("T")
	if text := r.text(); !strings.Contains(text, "Search › Related: Song 1") || !strings.Contains(text, "Related One") {
		t.Fatalf("no related page:\n%s", text)
	}
	if _, o := src.calls(); len(o) != 1 || o[0].Kind != "related" || o[0].ID != relID {
		t.Errorf("opened %v", o)
	}
	r.key("esc", "j", "j", "T")
	if got := r.model(); len(got.pages) != 0 || !strings.Contains(got.status, "nothing related") {
		t.Errorf("%d pages, status %q", len(got.pages), got.status)
	}
}

func TestAskingAgainAboutTheSameSongIsNotAnotherRequest(t *testing.T) {
	r, src := navRig(t)
	r.key("A", "esc", "B", "esc", "R")
	if w, _ := src.calls(); len(w) != 1 {
		t.Errorf("watched %v", w)
	}
}

func TestAFailedRequestSaysWhyAndLeavesNoPage(t *testing.T) {
	r, src := navRig(t)
	delete(src.watches, "id00")
	r.key("A")
	if got := r.model(); len(got.pages) != 0 || !strings.Contains(got.status, "did not answer") {
		t.Errorf("%d pages, status %q", len(got.pages), got.status)
	}
	delete(src.pages, dpID)
	r.key("j", "j", "A") // id02 has an artist, whose page is missing
	if got := r.model(); len(got.pages) != 0 || !strings.Contains(got.status, "no such page") {
		t.Errorf("%d pages, status %q", len(got.pages), got.status)
	}
}

func TestLeavingAPageWhileItLoadsStopsItsRequestAndALateAnswerIsIgnored(t *testing.T) {
	r, src := navRig(t)
	var m tea.Model = r.m
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyPressMsg(keyOf("A")))
	m, _ = m.Update(tea.KeyPressMsg(keyOf("esc")))
	late := cmd()
	src.mu.Lock()
	err := src.ctxs["watch id00"].Err()
	src.mu.Unlock()
	if err == nil {
		t.Error("the request of the page that was left was not stopped")
	}
	m, _ = m.Update(late)
	if got := m.(Model); len(got.pages) != 0 {
		t.Errorf("a late answer opened a page: %d", len(got.pages))
	}
}

func TestTheTabKeyLeavesThePages(t *testing.T) {
	r, _ := navRig(t)
	r.key("A", "tab")
	if got := r.model(); len(got.pages) != 0 || got.tab == tabSearch {
		t.Errorf("%d pages on %s", len(got.pages), got.tab)
	}
}

func TestANewSearchLeavesThePages(t *testing.T) {
	r, _ := navRig(t)
	r.key("A", "/")
	r.key("enter")
	if got := r.model(); len(got.pages) != 0 {
		t.Errorf("%d pages", len(got.pages))
	}
}

func TestOnThePlayerScreenTheKeysActOnThePlayingSongWhileTheLyricsShowAndOnTheCursorWithTheQueue(t *testing.T) {
	r, src := navRig(t)
	ts := tracks(3)
	r.state(playing(ts, 2)) // id02 is playing
	r.key("tab", "tab")
	if r.model().tab != tabPlayer {
		t.Fatalf("on %s", r.model().tab)
	}
	r.key("k", "A") // the queue is on screen: its cursor, moved up from the playing row
	if w, _ := src.calls(); len(w) == 0 || w[len(w)-1] != "id01" {
		t.Errorf("the queue's cursor was not used: %v", w)
	}
	if got := r.model(); got.pagesTab != tabPlayer || len(got.pages) == 0 {
		t.Errorf("not opened over the player: %+v", got.pagesTab)
	}
}

func TestGoMsgOpensThePageOfThePlayingSong(t *testing.T) {
	r, src := navRig(t)
	r.state(playing(tracks(3), 0))
	r.send(GoMsg{Kind: "radio"})
	if got := r.model(); got.tab != tabPlayer || len(got.pages) != 1 || !strings.Contains(r.text(), "Radio: Song 1") {
		t.Errorf("tab %s pages %d\n%s", got.tab, len(got.pages), r.text())
	}
	if w, _ := src.calls(); len(w) != 1 || w[0] != "id00" {
		t.Errorf("watched %v", w)
	}
}

func TestGoMsgWithNothingPlayingSaysSo(t *testing.T) {
	r, _ := navRig(t)
	r.send(GoMsg{Kind: "artist"})
	if got := r.model(); len(got.pages) != 0 || !strings.Contains(got.status, "nothing is playing") {
		t.Errorf("%d pages, status %q", len(got.pages), got.status)
	}
}

func TestASourceWithoutPagesSaysSo(t *testing.T) {
	r := newRig(t, 100, 30)
	r.key("/")
	r.typed("x")
	r.key("enter", "A")
	if got := r.model(); len(got.pages) != 0 || !strings.Contains(got.status, "no artist or album pages") {
		t.Errorf("%d pages, status %q", len(got.pages), got.status)
	}
}

func TestEveryPageFitsEverySize(t *testing.T) {
	for _, size := range [][2]int{{20, 8}, {40, 12}, {60, 15}, {80, 24}, {120, 30}, {200, 50}} {
		r, _ := navRig(t)
		r.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		r.key("A", "j", "j", "enter")
		for _, key := range []string{"", "esc", "R", "esc", "T", "esc", "esc"} {
			if key != "" {
				r.key(key)
			}
			lines := r.screen()
			if len(lines) != size[1] {
				t.Errorf("%dx%d after %q: %d lines", size[0], size[1], key, len(lines))
			}
			for _, l := range lines {
				if w := ansi.StringWidth(l); w > size[0] {
					t.Errorf("%dx%d after %q: a line is %d wide: %q", size[0], size[1], key, w, l)
				}
			}
		}
	}
}

func TestOnThePlayerScreenWithTheLyricsShowingTheKeysActOnThePlayingSong(t *testing.T) {
	r, src := navRig(t)
	m := r.model()
	m.deps.Lyrics = &stubLyrics{}
	r.m = m
	r.state(playing(tracks(3), 2))
	r.key("tab", "tab", "k", "A") // k would move a queue cursor: there is none on screen
	if w, _ := src.calls(); len(w) == 0 || w[len(w)-1] != "id02" {
		t.Errorf("the playing song was not used: %v", w)
	}
}
