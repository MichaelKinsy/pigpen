package ui

import (
	"context"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Review of M10b (rich listings).

// ctxSource records whether each call's context was already cancelled when it ran.
type ctxSource struct {
	*stubSource
	mu   sync.Mutex
	errs map[string]error
}

func (s *ctxSource) note(what string, ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs[what] = ctx.Err()
}

func (s *ctxSource) Search(ctx context.Context, q string, _ int) ([]music.Track, error) {
	s.note("songs "+q, ctx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return tracks(2), nil
}

func (s *ctxSource) SearchEntities(ctx context.Context, q string, _ int) ([]music.Entity, error) {
	s.note("entities "+q, ctx)
	return []music.Entity{{Kind: "album", ID: "MPREb_aaaaaaaaaaa", Title: "A"}}, ctx.Err()
}

func (s *ctxSource) OpenEntity(ctx context.Context, e music.Entity) (string, []music.Track, error) {
	s.note("open "+e.ID, ctx)
	if ctx.Err() != nil {
		return "", nil, ctx.Err()
	}
	return e.Title, tracks(2), nil
}

func runAll(c tea.Cmd) []tea.Msg {
	if c == nil {
		return nil
	}
	out := c()
	if b, ok := out.(tea.BatchMsg); ok {
		var msgs []tea.Msg
		for _, cc := range b {
			msgs = append(msgs, runAll(cc)...)
		}
		return msgs
	}
	return []tea.Msg{out}
}

// A search that a newer one replaced is cancelled (its requests stop rather than run on for up to 30 s), and leaving an album
// that is still loading cancels its request too.
func TestReviewM10bANewSearchCancelsTheOneItReplaces(t *testing.T) {
	src := &ctxSource{stubSource: &stubSource{}, errs: map[string]error{}}
	var m tea.Model = New(Deps{Source: src, Player: &stubPlayer{}})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	press := func(names ...string) tea.Cmd {
		var cmd tea.Cmd
		for _, n := range names {
			m, cmd = m.Update(tea.KeyPressMsg(keyOf(n)))
		}
		return cmd
	}
	first := press("/", "a", "enter")
	second := press("/", "b", "enter") // the prompt keeps the last query: this one is "ab"
	runAll(first)
	for _, msg := range runAll(second) {
		m, _ = m.Update(msg)
	}
	src.mu.Lock()
	if src.errs["songs a"] != context.Canceled || src.errs["entities a"] != context.Canceled {
		t.Errorf("the replaced search ran on: %v", src.errs)
	}
	if src.errs["songs ab"] != nil || src.errs["entities ab"] != nil {
		t.Errorf("the current search was cancelled: %v", src.errs)
	}
	src.mu.Unlock()

	open := press("2", "enter")
	press("backspace")
	runAll(open)
	src.mu.Lock()
	if src.errs["open MPREb_aaaaaaaaaaa"] != context.Canceled {
		t.Errorf("the album that was left still loads: %v", src.errs)
	}
	src.mu.Unlock()

	// The same query searched again: the cancelled answer of the first, arriving last, neither empties the list nor says
	// "context canceled".
	again1 := press("/", "enter")
	again2 := press("/", "enter")
	for _, msg := range append(runAll(again2), runAll(again1)...) {
		m, _ = m.Update(msg)
	}
	if got := m.(Model); len(got.results) != 2 || len(got.ents) != 1 || got.status != "" {
		t.Errorf("a cancelled answer of the same query won: %d results, %d entities, status %q", len(got.results), len(got.ents), got.status)
	}
	// The same album opened again after leaving it: the first, cancelled, answer does not close the page.
	press("2")
	open1 := press("enter")
	press("backspace")
	open2 := press("enter")
	for _, msg := range append(runAll(open2), runAll(open1)...) {
		m, _ = m.Update(msg)
	}
	if got := m.(Model); got.ent == nil || len(got.ent.tracks) != 2 || got.status != "" {
		t.Errorf("a cancelled open of the same album won: %+v, status %q", got.ent, got.status)
	}
}
