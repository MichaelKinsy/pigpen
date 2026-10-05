package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// cachedLib is a library whose last session left its listing on disk.
type cachedLib struct {
	*pagingLib
	cachedCols   []music.Collection
	cachedTracks []music.Track
	cachedMore   bool
}

func (c *cachedLib) CachedLibrary(context.Context) ([]music.Collection, bool) {
	return c.cachedCols, c.cachedCols != nil
}

func (c *cachedLib) CachedTracks(_ context.Context, id string, n int) ([]music.Track, bool, bool) {
	if id != "LM" || c.cachedTracks == nil {
		return nil, false, false
	}
	return c.cachedTracks[:min(n, len(c.cachedTracks))], c.cachedMore, true
}

func swrRig(t *testing.T) (*rig, *cachedLib) {
	t.Helper()
	lib := newLib()
	lib.granted = true
	c := &cachedLib{pagingLib: &pagingLib{libSource: lib, total: 3}}
	c.cachedCols = []music.Collection{{ID: "LM", Kind: "liked", Title: "Liked songs"}, {ID: "PLold", Kind: "playlist", Title: "Old playlist"}}
	r := newRig(t, 100, 30)
	r.m = New(Deps{Source: c, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	return r, c
}

func TestTheCachedLibraryIsShownBeforeTheAccountAnswersAndMarkedAsUpdating(t *testing.T) {
	r, _ := swrRig(t)
	var cmd tea.Cmd
	r.m, cmd = r.m.Update(tea.KeyPressMsg(keyOf("tab")))
	_ = cmd
	// the cache answers first; the account has not yet
	r.m, _ = r.m.Update(libCachedMsg{cols: []music.Collection{{ID: "LM", Kind: "liked", Title: "Liked songs"}, {ID: "PLold", Kind: "playlist", Title: "Old playlist"}}})
	text := r.text()
	if !strings.Contains(text, "Old playlist") || !strings.Contains(text, "updating") {
		t.Fatalf("the cached rows are not shown with a marker:\n%s", text)
	}
	r.key("j") // the rows can be used at once
	if r.lib().cur != 1 {
		t.Errorf("cursor %d", r.lib().cur)
	}
}

func TestTheAccountsAnswerReplacesTheCachedRowsAndKeepsTheCursorOnTheSameCollection(t *testing.T) {
	r, c := swrRig(t)
	c.cols = []music.Collection{{ID: "LM", Kind: "liked", Title: "Liked songs"}, {ID: "PLnew", Kind: "playlist", Title: "New one"}, {ID: "PLold", Kind: "playlist", Title: "Old playlist"}}
	r.m, _ = r.m.Update(tea.KeyPressMsg(keyOf("tab")))
	r.m, _ = r.m.Update(libCachedMsg{cols: c.cachedCols})
	r.key("j") // on Old playlist
	r.send(libraryDoneMsg{cols: c.cols})
	if got := r.lib().cols[r.lib().cur].ID; got != "PLold" {
		t.Errorf("the cursor moved to %s", got)
	}
	text := r.text()
	if !strings.Contains(text, "New one") || strings.Contains(text, "updating") {
		t.Errorf("not refreshed:\n%s", text)
	}
}

func TestACacheThatArrivesAfterTheAccountIsIgnored(t *testing.T) {
	r, c := swrRig(t)
	c.cols = []music.Collection{{ID: "LM", Kind: "liked", Title: "Liked songs"}}
	r.m, _ = r.m.Update(tea.KeyPressMsg(keyOf("tab")))
	r.send(libraryDoneMsg{cols: c.cols})
	r.m, _ = r.m.Update(libCachedMsg{cols: c.cachedCols})
	if len(r.lib().cols) != 1 {
		t.Errorf("stale rows replaced fresh ones: %v", r.lib().cols)
	}
}

func TestWhenTheAccountFailsTheCachedRowsStayAndTheErrorIsNamed(t *testing.T) {
	r, c := swrRig(t)
	r.m, _ = r.m.Update(tea.KeyPressMsg(keyOf("tab")))
	r.m, _ = r.m.Update(libCachedMsg{cols: c.cachedCols})
	r.send(libraryDoneMsg{err: errors.New("no network")})
	text := r.text()
	if !strings.Contains(text, "Old playlist") || !strings.Contains(text, "no network") {
		t.Errorf("the rows or the error are missing:\n%s", text)
	}
	if strings.Contains(text, "updating") {
		t.Errorf("still says updating:\n%s", text)
	}
}

func TestWithdrawnConsentAfterACachedPaintShowsTheConsentScreen(t *testing.T) {
	r, c := swrRig(t)
	r.m, _ = r.m.Update(tea.KeyPressMsg(keyOf("tab")))
	r.m, _ = r.m.Update(libCachedMsg{cols: c.cachedCols})
	r.send(libraryDoneMsg{err: &music.NeedsConsentError{Browser: "chrome", Description: "Chrome"}})
	if r.lib().state != libConsent {
		t.Errorf("state %v", r.lib().state)
	}
}

func TestAnOpenedCollectionShowsCachedTracksThenTheLiveOnesKeepingTheCursor(t *testing.T) {
	r, c := swrRig(t)
	c.cachedTracks = []music.Track{{ID: "oldoldold01", Title: "Cached one"}, {ID: "oldoldold02", Title: "Cached two"}}
	r.m, _ = r.m.Update(tea.KeyPressMsg(keyOf("tab")))
	r.send(libraryDoneMsg{cols: c.cachedCols})
	r.m, _ = r.m.Update(tea.KeyPressMsg(keyOf("enter")))
	r.m, _ = r.m.Update(libTracksMsg{id: "LM", tracks: c.cachedTracks, paged: true, cached: true, more: true})
	if text := r.text(); !strings.Contains(text, "Cached two") || !strings.Contains(text, "updating") {
		t.Fatalf("cached tracks not shown:\n%s", text)
	}
	r.key("j") // on Cached two
	live := []music.Track{{ID: "newnewnew01", Title: "Fresh"}, {ID: "oldoldold02", Title: "Cached two"}}
	r.send(libTracksMsg{id: "LM", tracks: live, paged: true})
	if r.lib().tracks[r.lib().tcur].ID != "oldoldold02" {
		t.Errorf("cursor on %v", r.lib().tracks[r.lib().tcur])
	}
	if strings.Contains(r.text(), "updating") {
		t.Errorf("still updating")
	}
	// a cache answer after the live one changes nothing
	r.m, _ = r.m.Update(libTracksMsg{id: "LM", tracks: c.cachedTracks, paged: true, cached: true})
	if r.lib().tracks[0].ID != "newnewnew01" {
		t.Errorf("cached rows replaced live ones")
	}
}

func TestOpeningALibraryWithACacheAsksTheCacheAndTheAccountTogether(t *testing.T) {
	r, c := swrRig(t)
	_, cmd := r.m.Update(tea.KeyPressMsg(keyOf("tab")))
	if cmd == nil {
		t.Fatal("no command")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("not two commands: %T", cmd())
	}
	var got []string
	for _, b := range batch {
		switch msg := b().(type) {
		case libCachedMsg:
			got = append(got, "cache")
			if len(msg.cols) != len(c.cachedCols) {
				t.Errorf("cols %v", msg.cols)
			}
		case libraryDoneMsg:
			got = append(got, "live")
		}
	}
	if len(got) != 2 {
		t.Errorf("%v", got)
	}
}

func (r *rig) lib() library { return r.m.(Model).lib }
