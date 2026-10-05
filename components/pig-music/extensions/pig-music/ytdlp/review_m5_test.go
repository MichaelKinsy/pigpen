package ytdlp

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Empty answers are "nothing", like nulls: they do not blank what the track had (a mutant that took "" survived).
func TestEnrichKeepsExistingFieldsWhenYtdlpAnswersEmpty(t *testing.T) {
	have := music.Track{ID: "a", Title: "T", Artists: []string{"Known"}, Duration: 99 * time.Second, Album: "Alb"}
	got, err := enrichWith(`{"id":"a","artists":[],"artist":"","channel":" ","duration":0,"album":""}`, nil, nil).Enrich(context.Background(), have)
	if err != nil || got.Duration != 99*time.Second || got.Album != "Alb" || len(got.Artists) != 1 || got.Artists[0] != "Known" {
		t.Errorf("%+v %v", got, err)
	}
}

// Listing a collection pages through it: about 0.65 s per 100 entries measured with yt-dlp 2026.08.19, so liked songs past
// a few thousand do not fit in the 30 s a search gets. Library calls get longer; search and enrichment keep 30 s.
func TestLibraryCallsGetLongerThanASearch(t *testing.T) {
	deadlines := map[string]time.Duration{}
	s := &Source{Cookies: granted("firefox"), Runner: func(ctx context.Context, _ string, args []string) ([]byte, []byte, error) {
		d, _ := ctx.Deadline()
		kind := "search"
		for _, a := range args {
			if a == "--cookies-from-browser" {
				kind = "library"
			}
		}
		deadlines[kind] = time.Until(d)
		return []byte(`{"entries":[]}`), nil, nil
	}}
	_, _ = s.Library(context.Background())
	_, _ = s.Search(context.Background(), "x", 5)
	if deadlines["library"] < 90*time.Second {
		t.Errorf("a library listing gets %v", deadlines["library"])
	}
	if deadlines["search"] > 31*time.Second {
		t.Errorf("a search gets %v", deadlines["search"])
	}
}
