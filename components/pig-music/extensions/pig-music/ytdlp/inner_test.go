package ytdlp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// innerFake is YouTube Music's own search, answering in one request with artists and lengths (the native package's Source in
// production). M9c: yt-dlp took about 4 s per search and listed titles only.
type innerFake struct {
	tracks []music.Track
	err    error
	calls  int
	query  string
	limit  int
}

func (f *innerFake) Search(_ context.Context, q string, limit int) ([]music.Track, error) {
	f.calls++
	f.query, f.limit = q, limit
	return f.tracks, f.err
}
func (f *innerFake) Library(context.Context) ([]music.Collection, error)   { panic("library") }
func (f *innerFake) Tracks(context.Context, string) ([]music.Track, error) { panic("tracks") }
func (f *innerFake) PlayURL(music.Track) string                            { panic("playurl") }

func TestSearchUsesTheDirectListingAndRunsNoYtdlp(t *testing.T) {
	inner := &innerFake{tracks: []music.Track{
		{ID: "lYBUbBu4W08", Title: "Never Gonna Give You Up", Artists: []string{"Rick Astley"}, Album: "Whenever You Need Somebody", Duration: 214 * time.Second},
		{ID: "GHKOKABHk1E", Title: "Never Gonna Give You Up", Artists: []string{"Stephanie Mills"}, Duration: 314 * time.Second},
	}}
	var calls []call
	s := &Source{Inner: inner, Runner: script(&calls)}
	got, err := s.Search(context.Background(), "  never gonna  ", 10)
	if err != nil || len(got) != 2 || got[0].Artists[0] != "Rick Astley" || got[0].Duration != 214*time.Second {
		t.Fatalf("%+v, %v", got, err)
	}
	if len(calls) != 0 {
		t.Fatalf("yt-dlp ran %d times for a search the direct listing answered", len(calls))
	}
	if inner.query != "never gonna" || inner.limit != 10 {
		t.Errorf("asked %q, %d", inner.query, inner.limit)
	}
}

func TestSearchFallsBackToYtdlpWhenTheDirectListingFailsOrIsEmpty(t *testing.T) {
	for name, inner := range map[string]*innerFake{
		"error": {err: errors.New("YouTube Music search answered HTTP 403")},
		"empty": {},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			s := &Source{Inner: inner, Runner: script(&calls, ok(fixture(t, "search-music.json")))}
			got, err := s.Search(context.Background(), "night drive", 3)
			if err != nil || len(got) != 3 || len(calls) != 1 {
				t.Fatalf("%d tracks, %d runs, %v", len(got), len(calls), err)
			}
			if inner.calls != 1 {
				t.Errorf("the direct listing was asked %d times", inner.calls)
			}
		})
	}
}

func TestSearchStillValidatesBeforeAskingAnyone(t *testing.T) {
	inner := &innerFake{}
	s := &Source{Inner: inner}
	if _, err := s.Search(context.Background(), "   ", 5); err == nil {
		t.Fatal("an empty query was accepted")
	}
	if inner.calls != 0 {
		t.Error("the direct listing was asked for an empty query")
	}
}

func TestACancelledSearchDoesNotFallBackToYtdlp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls []call
	s := &Source{Inner: &innerFake{err: context.Canceled}, Runner: script(&calls)}
	if _, err := s.Search(ctx, "x", 3); err == nil {
		t.Fatal("no error")
	}
	if len(calls) != 0 {
		t.Errorf("yt-dlp ran %d times after cancellation", len(calls))
	}
}

type innerEnricher struct {
	innerFake
	enriched music.Track
	eerr     error
	asked    int
}

func (f *innerEnricher) Enrich(_ context.Context, t music.Track) (music.Track, error) {
	f.asked++
	if f.eerr != nil {
		return t, f.eerr
	}
	return f.enriched, nil
}

func TestEnrichUsesTheDirectDetailsAndRunsNoYtdlp(t *testing.T) {
	inner := &innerEnricher{enriched: music.Track{ID: "lYBUbBu4W08", Artists: []string{"Rick Astley"}, Album: "Whenever You Need Somebody", Duration: 214 * time.Second}}
	var calls []call
	s := &Source{Inner: inner, Runner: script(&calls)}
	got, err := s.Enrich(context.Background(), music.Track{ID: "lYBUbBu4W08", Title: "t"})
	if err != nil || got.Album != "Whenever You Need Somebody" || got.Duration != 214*time.Second {
		t.Fatalf("%+v, %v", got, err)
	}
	if len(calls) != 0 {
		t.Errorf("yt-dlp ran %d times", len(calls))
	}
}

func TestEnrichFallsBackToYtdlpWhenTheDirectDetailsFailOrAreIncomplete(t *testing.T) {
	line := []byte(`{"id":"lYBUbBu4W08","artists":["Rick Astley"],"duration":214,"album":"Whenever You Need Somebody"}`)
	for name, inner := range map[string]*innerEnricher{
		"error":      {eerr: errors.New("HTTP 500")},
		"incomplete": {enriched: music.Track{ID: "lYBUbBu4W08", Title: "only a title"}},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			s := &Source{Inner: inner, Runner: script(&calls, ok(line))}
			got, err := s.Enrich(context.Background(), music.Track{ID: "lYBUbBu4W08"})
			if err != nil || len(calls) != 1 || got.Album == "" || got.Duration != 214*time.Second {
				t.Fatalf("%+v, %d runs, %v", got, len(calls), err)
			}
		})
	}
}
