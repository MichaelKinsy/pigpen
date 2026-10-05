package ui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// entSource is a Source that also finds albums and artists.
type entSource struct {
	*stubSource
	mu      sync.Mutex
	ents    []music.Entity
	entErr  error
	page    []music.Track
	pageErr error
	opened  []music.Entity
	asked   []string
}

func (e *entSource) SearchEntities(_ context.Context, q string, limit int) ([]music.Entity, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.asked = append(e.asked, q)
	return e.ents, e.entErr
}

func (e *entSource) OpenEntity(_ context.Context, en music.Entity) (string, []music.Track, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.opened = append(e.opened, en)
	return en.Title + " (page)", e.page, e.pageErr
}

func entRig(t *testing.T) (*rig, *entSource) {
	t.Helper()
	src := &entSource{
		stubSource: &stubSource{tracks: tracks(3)},
		ents: []music.Entity{
			{Kind: "album", ID: "MPREb_aaaaaaaaaaa", Title: "Random Access Memories", Subtitle: "Daft Punk • 2013"},
			{Kind: "album", ID: "MPREb_bbbbbbbbbbb", Title: "Discovery", Subtitle: "Daft Punk • 2001"},
			{Kind: "artist", ID: "UCaaaaaaaaaaaaaaaaaaaaaa", Title: "Daft Punk", Subtitle: "80.8M monthly audience"},
		},
		page: []music.Track{{ID: "aaaaaaaaaaa", Title: "Give Life Back to Music", Artists: []string{"Daft Punk"}, Album: "Random Access Memories", Duration: 274 * time.Second}, {ID: "bbbbbbbbbbb", Title: "The Game of Love", Duration: 321 * time.Second}},
	}
	r := newRig(t, 100, 30)
	r.m = New(Deps{Source: src, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	r.key("/")
	r.typed("daft punk")
	r.key("enter")
	return r, src
}

func TestASearchAlsoAsksForAlbumsAndArtistsAndNamesThemInATabLine(t *testing.T) {
	r, src := entRig(t)
	if len(src.asked) != 1 || src.asked[0] != "daft punk" {
		t.Fatalf("entities asked %v", src.asked)
	}
	text := r.text()
	for _, want := range []string{"Songs 3", "Albums 2", "Artists 1"} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
}

func TestTheNumberKeysSwitchBetweenSongsAlbumsAndArtists(t *testing.T) {
	r, _ := entRig(t)
	r.key("2")
	text := r.text()
	if !strings.Contains(text, "Random Access Memories") || !strings.Contains(text, "Daft Punk • 2013") || strings.Contains(text, "Song 1") {
		t.Errorf("albums:\n%s", text)
	}
	r.key("3")
	if text := r.text(); !strings.Contains(text, "80.8M monthly audience") || strings.Contains(text, "Discovery") {
		t.Errorf("artists:\n%s", text)
	}
	r.key("1")
	if !strings.Contains(r.text(), "Song 1") {
		t.Errorf("songs:\n%s", r.text())
	}
}

func TestEnterOnAnAlbumOpensItsTracksAndEnterThereHandsThemToThePlayer(t *testing.T) {
	r, src := entRig(t)
	r.key("2", "enter")
	if len(src.opened) != 1 || src.opened[0].ID != "MPREb_aaaaaaaaaaa" {
		t.Fatalf("opened %v", src.opened)
	}
	text := r.text()
	if !strings.Contains(text, "Random Access Memories (page)") || !strings.Contains(text, "Give Life Back to Music") || !strings.Contains(text, "backspace") {
		t.Errorf("not the album page:\n%s", text)
	}
	r.key("j", "enter")
	if len(r.player.lastReplace) != 2 || r.player.lastReplace[1].ID != "bbbbbbbbbbb" || r.m.(Model).tab != tabPlayer {
		t.Errorf("replace %v, tab %s", r.player.lastReplace, r.m.(Model).tab)
	}
	if got := strings.Join(r.player.calls, ","); !strings.Contains(got, "replace 2 tracks at 1") {
		t.Errorf("calls %s", got)
	}
}

func TestBackspaceLeavesAnOpenedEntityAndKeepsTheCursorOnIt(t *testing.T) {
	r, _ := entRig(t)
	r.key("2", "j", "enter", "backspace")
	text := r.text()
	if !strings.Contains(text, "Discovery") || strings.Contains(text, "Give Life Back") {
		t.Errorf("not back on the list:\n%s", text)
	}
	if r.m.(Model).entCur != 1 {
		t.Errorf("cursor %d", r.m.(Model).entCur)
	}
}

func TestAnAlbumThatCannotBeOpenedSaysSoAndStaysOnTheList(t *testing.T) {
	r, src := entRig(t)
	src.pageErr = errors.New("album is gone")
	r.key("2", "enter")
	text := r.text()
	if !strings.Contains(text, "album is gone") || !strings.Contains(text, "Discovery") {
		t.Errorf("%s", text)
	}
}

func TestASourceWithoutAlbumsShowsSongsOnlyAndTheNumberKeysDoNothing(t *testing.T) {
	r := newRig(t, 100, 30)
	r.key("/")
	r.typed("x")
	r.key("enter", "2")
	if text := r.text(); strings.Contains(text, "Albums") || !strings.Contains(text, "Song 1") {
		t.Errorf("%s", text)
	}
}

func TestEntitiesOfAnEarlierSearchAreIgnoredAndAFailedEntitySearchLeavesTheSongs(t *testing.T) {
	r, src := entRig(t)
	r.send(entitiesDoneMsg{query: "something else", ents: []music.Entity{{Kind: "album", ID: "MPREb_ccccccccccc", Title: "Stale"}}})
	r.key("2")
	if strings.Contains(r.text(), "Stale") {
		t.Error("a stale answer was shown")
	}
	src.entErr = errors.New("no albums today")
	src.ents = nil
	r.key("/")
	r.typed("again")
	r.key("enter")
	text := r.text()
	if !strings.Contains(text, "Song 1") || strings.Contains(text, "Albums") {
		t.Errorf("%s", text)
	}
}
