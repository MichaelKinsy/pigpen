package pig_music

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

type listSource struct {
	music.Source
	tracks    []music.Track
	refreshed int
}

func (l *listSource) Tracks(context.Context, string) ([]music.Track, error) { return l.tracks, nil }

type pagingSource struct {
	listSource
	asked [][2]int
}

func (p *pagingSource) TracksPage(_ context.Context, _ string, from, n int) ([]music.Track, bool, error) {
	p.asked = append(p.asked, [2]int{from, n})
	return p.tracks[:1], true, nil
}
func (p *pagingSource) Refresh() { p.refreshed++ }

func tracksOf(n int) []music.Track {
	out := make([]music.Track, n)
	for i := range out {
		out[i] = music.Track{ID: string(rune('a' + i))}
	}
	return out
}

// The guard stands between the UI and the Source, so it has to let the optional extras through (M9c: paging, refresh).
func TestTheGuardPassesPagesAndRefreshToASourceThatHasThem(t *testing.T) {
	src := &pagingSource{listSource: listSource{tracks: tracksOf(3)}}
	g := (&app{}).guarded(src).(guard)
	got, more, err := g.TracksPage(context.Background(), "LM", 2, 5)
	if err != nil || len(got) != 1 || !more || len(src.asked) != 1 || src.asked[0] != [2]int{2, 5} {
		t.Fatalf("%v %v %v %v", got, more, err, src.asked)
	}
	g.Refresh()
	if src.refreshed != 1 {
		t.Error("Refresh was not passed on")
	}
}

func TestTheGuardPagesASourceThatListsEverythingAtOnce(t *testing.T) {
	src := &listSource{tracks: tracksOf(5)}
	g := (&app{}).guarded(src).(guard)
	got, more, err := g.TracksPage(context.Background(), "LM", 0, 2)
	if err != nil || len(got) != 2 || !more {
		t.Fatalf("%v %v %v", got, more, err)
	}
	got, more, err = g.TracksPage(context.Background(), "LM", 4, 2)
	if err != nil || len(got) != 1 || more {
		t.Fatalf("%v %v %v", got, more, err)
	}
	if got, more, _ = g.TracksPage(context.Background(), "LM", 9, 2); len(got) != 0 || more {
		t.Fatalf("past the end: %v %v", got, more)
	}
	g.Refresh() // a source with nothing to forget
}
