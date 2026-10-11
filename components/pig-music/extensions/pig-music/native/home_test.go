package native

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// The charts, chart and home fixtures are trimmed real unsigned WEB_REMIX browse answers (testdata/README.md).

func TestTheTopChartIsTheTop100MusicVideosPlaylistOfTheCharts(t *testing.T) {
	e, err := TopChart(fixture(t, "browse-charts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if e.Kind != "playlist" || e.ID != "VLPL4fGSI1pDJn69On1f-8NAvX_CYlx7QyZc" || e.Title != "Top 100 Music Videos United States" {
		t.Errorf("%+v", e)
	}
}

func TestWithoutATop100ChartTheFirstVideoChartIsTaken(t *testing.T) {
	data := strings.ReplaceAll(string(fixture(t, "browse-charts.json")), "Top 100 Music Videos", "Most Played Videos")
	e, err := TopChart([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if e.Title != "Top 100 Live Performances - United States" {
		t.Errorf("%+v", e)
	}
	if _, err := TopChart([]byte(`{"contents":{"singleColumnBrowseResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{"contents":[{"musicCarouselShelfRenderer":{"contents":[]}}]}}}}]}}}`)); err == nil {
		t.Error("a charts page with no chart is not an error")
	}
}

func TestAChartIsItsTracksInOrder(t *testing.T) {
	chart := music.Entity{Kind: "playlist", ID: "VLPL4fGSI1pDJn69On1f-8NAvX_CYlx7QyZc", Title: "Top 100 Music Videos United States"}
	sec, err := ParseChart(fixture(t, "browse-chart-playlist.json"), chart)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sec.Title, "Top 100") || len(sec.Tracks) != 25 {
		t.Fatalf("%q, %d tracks", sec.Title, len(sec.Tracks))
	}
	first := sec.Tracks[0]
	if first.ID != "fcnDmrtj6Sk" || first.Title != "Dai Dai" || strings.Join(first.Artists, "|") != "Shakira & Burna Boy" && strings.Join(first.Artists, "|") != "Shakira|Burna Boy" || first.Duration != 4*time.Minute+time.Second {
		t.Errorf("%+v", first)
	}
	for i, tr := range sec.Tracks {
		if !ValidVideoID(tr.ID) || tr.Title == "" || len(tr.Artists) == 0 {
			t.Errorf("track %d: %+v", i, tr)
		}
	}
}

func TestTheHomeIsItsShelvesInOrder(t *testing.T) {
	secs, err := ParseHome(fixture(t, "browse-home.json"))
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, s := range secs {
		titles = append(titles, s.Title)
	}
	if got := strings.Join(titles, "|"); got != "Trending community playlists|Throwbacks" {
		t.Fatalf("sections: %s", got)
	}
	for _, s := range secs {
		if len(s.Entities) == 0 {
			t.Fatalf("%s: nothing to open", s.Title)
		}
		for _, e := range s.Entities {
			if e.Kind != "playlist" || !strings.HasPrefix(e.ID, "VL") || e.Title == "" {
				t.Errorf("%s: %+v", s.Title, e)
			}
		}
	}
	if _, err := ParseHome([]byte(`{"contents":{}}`)); err == nil {
		t.Error("an answer in an unknown shape is not an error")
	}
}

// TestPopularBrowsesTheChartsThenTheChartWithNoCookie: two unsigned browse requests, charts first.
func TestPopularBrowsesTheChartsThenTheChartWithNoCookie(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			BrowseID string `json:"browseId"`
		}
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		asked = append(asked, req.BrowseID)
		mu.Unlock()
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("a credential on %s", req.BrowseID)
		}
		switch req.BrowseID {
		case "FEmusic_charts":
			_, _ = w.Write(fixture(t, "browse-charts.json"))
		case "VLPL4fGSI1pDJn69On1f-8NAvX_CYlx7QyZc":
			_, _ = w.Write(fixture(t, "browse-chart-playlist.json"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	s := &Source{BrowseURL: srv.URL}
	sec, err := s.Popular(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sec.Title != "Top 100 Music Videos United States" || len(sec.Tracks) != 25 {
		t.Errorf("%q: %d tracks", sec.Title, len(sec.Tracks))
	}
	if got := strings.Join(asked, " "); got != "FEmusic_charts VLPL4fGSI1pDJn69On1f-8NAvX_CYlx7QyZc" {
		t.Errorf("asked %s", got)
	}
}

func TestPopularOfflineIsAnErrorThatSaysSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing listens: as offline as a test gets
	_, err := (&Source{BrowseURL: url, Timeout: 2 * time.Second}).Popular(context.Background())
	if err == nil {
		t.Fatal("no error offline")
	}
}
