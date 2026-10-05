package ui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// countingEnricher counts Enrich calls that have started and not yet been answered back to the model.
type countingEnricher struct {
	*stubSource
	mu      sync.Mutex
	started []string
}

func (s *countingEnricher) Enrich(_ context.Context, t music.Track) (music.Track, error) {
	s.mu.Lock()
	s.started = append(s.started, t.ID)
	s.mu.Unlock()
	t.Artists = []string{"A"}
	return t, nil
}

// run runs a command the way Bubble Tea does (each in its own goroutine, here one after the other) and returns its
// messages without giving them back to the model, so that answers are still "on their way".
func run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	out := cmd()
	if b, ok := out.(tea.BatchMsg); ok {
		var all []tea.Msg
		for _, c := range b {
			all = append(all, run(c)...)
		}
		return all
	}
	if out == nil {
		return nil
	}
	return []tea.Msg{out}
}

// Each enrichment is a yt-dlp process of a few seconds. Holding j through a result list must not start one per row passed:
// at most enrichParallel (3) are on their way, and when one is back the row the cursor is on now is asked for next.
// (The owner asked for every result, queue item and library track to be filled in "a few at a time", which replaced the
// reviewer's limit of one.)
func TestScrollingThroughResultsKeepsAtMostThreeEnrichmentsOnTheirWay(t *testing.T) {
	src := &countingEnricher{stubSource: &stubSource{}}
	var m tea.Model = New(Deps{Source: src, Player: &stubPlayer{}})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	var pending []tea.Msg
	var cmd tea.Cmd
	m, cmd = m.Update(searchDoneMsg{query: "", tracks: bareTracks(20)})
	pending = append(pending, run(cmd)...)
	for i := 0; i < 10; i++ {
		m, cmd = m.Update(tea.KeyPressMsg(keyOf("down")))
		pending = append(pending, run(cmd)...)
	}
	if len(src.started) != enrichParallel {
		t.Fatalf("%d enrichments started while scrolling (%v); want %d on their way at a time", len(src.started), src.started, enrichParallel)
	}
	// the answer for row 1 comes back: the row under the cursor (11) is asked for next, and only it
	for _, msg := range pending {
		m, cmd = m.Update(msg)
		for _, next := range run(cmd) {
			m, _ = m.Update(next)
		}
	}
	if len(src.started) < enrichParallel+1 || src.started[enrichParallel] != "id10" {
		t.Errorf("enriched %v; want the row the cursor rests on, id10, right after the first three", src.started)
	}
	_ = strings.Join
}

// The consent prompt names one browser; the yes is for that browser, and the UI says so to the Source.
type namedConsenter struct {
	*libSource
	grantedFor []string
	answer     error
}

func (s *namedConsenter) GrantCookieAccess(_ context.Context, browser string) error {
	s.grantedFor = append(s.grantedFor, browser)
	if s.answer != nil {
		return s.answer
	}
	s.granted = true
	return nil
}

func TestTheYesIsGivenForTheBrowserThePromptNamed(t *testing.T) {
	lib := newLib()
	lib.need = consentNeeded()
	src := &namedConsenter{libSource: lib}
	r := newRig(t, 100, 30)
	r.m = New(Deps{Source: src, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	r.key("tab")
	r.key("y")
	if len(src.grantedFor) != 1 || src.grantedFor[0] != "chrome" {
		t.Fatalf("granted for %v; the prompt named chrome", src.grantedFor)
	}
	if !strings.Contains(r.text(), "Road trip") {
		t.Errorf("after y:\n%s", r.text())
	}
}

func TestWhenTheBrowserChangedUnderThePromptTheNewOneIsAskedAbout(t *testing.T) {
	lib := newLib()
	lib.need = consentNeeded()
	firefox := &music.NeedsConsentError{Browser: "firefox", Description: "firefox (the default browser, firefox.desktop)", Notes: "Firefox: no prompt."}
	src := &namedConsenter{libSource: lib, answer: firefox}
	r := newRig(t, 100, 30)
	r.m = New(Deps{Source: src, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	r.key("tab")
	r.key("y")
	txt := strings.Join(strings.Fields(r.text()), " ")
	if !strings.Contains(txt, "firefox (the default browser, firefox.desktop)") || !strings.Contains(txt, "[y]") {
		t.Errorf("the new browser is not asked about:\n%s", r.text())
	}
}

// A collection's tracks that arrive after the user left it for another must not appear under the other (a mutant that
// accepted them survived).
func TestTracksOfACollectionThatWasLeftDoNotAppearInAnother(t *testing.T) {
	src := newLib()
	src.granted = true
	r := libRig(t, src, 100, 30)
	r.key("tab", "down", "down", "enter") // Focus: no tracks
	r.send(libTracksMsg{id: "PLroadtrip0000001", tracks: []music.Track{{ID: "ccccccccccc", Title: "Highway song"}}})
	if txt := r.text(); strings.Contains(txt, "Highway song") || !strings.Contains(txt, "Focus") {
		t.Errorf("Road trip's late answer shown in Focus:\n%s", txt)
	}
}

// deadlineSource records how long the UI lets a library call run.
type deadlineSource struct {
	*libSource
	lib, tracks time.Duration
}

func (s *deadlineSource) Library(ctx context.Context) ([]music.Collection, error) {
	d, _ := ctx.Deadline()
	s.lib = time.Until(d)
	return s.libSource.Library(ctx)
}

func (s *deadlineSource) Tracks(ctx context.Context, id string) ([]music.Track, error) {
	d, _ := ctx.Deadline()
	s.tracks = time.Until(d)
	return s.libSource.Tracks(ctx, id)
}

func TestTheUILetsALibraryListingRunLongerThanASearch(t *testing.T) {
	lib := newLib()
	lib.granted = true
	src := &deadlineSource{libSource: lib}
	r := newRig(t, 100, 30)
	r.m = New(Deps{Source: src, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	r.key("tab", "enter")
	if src.lib < 90*time.Second || src.tracks < 90*time.Second {
		t.Errorf("library %v, tracks %v: a long liked-songs list does not fit", src.lib, src.tracks)
	}
}
