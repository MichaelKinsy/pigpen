package ytdlp

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// rev-pig-music-m6: seen from yt-dlp 2026.08.19 on a music.youtube.com search result: "artists": ["Vindaloo Singh",
// "Vindaloo Singh"]. The row showed the name twice.
func TestEnrichShowsAnArtistYouTubeListsTwiceOnce(t *testing.T) {
	s := enrichWith(`{"id":"kbfzU3xOCx4","artists":["Vindaloo Singh","Vindaloo Singh"],"artist":"Vindaloo Singh, Vindaloo Singh","duration":180}`, nil, nil)
	got, err := s.Enrich(context.Background(), music.Track{ID: "kbfzU3xOCx4"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Artists, "+") != "Vindaloo Singh" {
		t.Errorf("artists %v", got.Artists)
	}
}

// Enrichment (search results and queue rows) never carries cookies, even when the Source can read them for the library.
func TestEnrichNeverCarriesCookies(t *testing.T) {
	var args []string
	s := enrichWith(`{"id":"aaaaaaaaaaa","duration":1}`, nil, &args)
	s.Cookies = granted("chrome")
	if _, err := s.Enrich(context.Background(), music.Track{ID: "aaaaaaaaaaa"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "cookies") {
		t.Errorf("enrichment carries cookies: %v", args)
	}
}
