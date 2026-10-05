package native

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// M9c: a track's artist, album and length came from a full yt-dlp watch-page extraction (2.8 s measured). YouTube Music's
// own `next` answer for the video carries them in one small request (0.17 s measured).
func TestParseNextReadsTheSongsOwnRow(t *testing.T) {
	data, err := os.ReadFile("testdata/next-song.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseNext(data, "lYBUbBu4W08")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Never Gonna Give You Up" || len(got.Artists) != 1 || got.Artists[0] != "Rick Astley" ||
		got.Album != "Whenever You Need Somebody" || got.Duration != 3*time.Minute+34*time.Second || got.ArtURL == "" {
		t.Fatalf("%+v", got)
	}
}

func TestParseNextTakesTheRowOfTheVideoAskedAbout(t *testing.T) {
	doc := `{"contents":{"singleColumnMusicWatchNextResultsRenderer":{"tabbedRenderer":{"watchNextTabbedResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"musicQueueRenderer":{"content":{"playlistPanelRenderer":{"contents":[
	 {"playlistPanelVideoRenderer":{"videoId":"aaaaaaaaaaa","title":{"runs":[{"text":"First"}]},"longBylineText":{"runs":[{"text":"One"}]},"lengthText":{"runs":[{"text":"1:00"}]}}},
	 {"playlistPanelVideoRenderer":{"videoId":"bbbbbbbbbbb","title":{"runs":[{"text":"Second"}]},"longBylineText":{"runs":[{"text":"Two"},{"text":" • "},{"text":"Album Two"},{"text":" • "},{"text":"2020"}]},"lengthText":{"runs":[{"text":"1:02:03"}]}}}
	]}}}}}}]}}}}}`
	got, err := parseNext([]byte(doc), "bbbbbbbbbbb")
	if err != nil || got.Artists[0] != "Two" || got.Album != "Album Two" || got.Duration != time.Hour+2*time.Minute+3*time.Second {
		t.Fatalf("%+v, %v", got, err)
	}
	if _, err := parseNext([]byte(doc), "ccccccccccc"); err == nil {
		t.Error("a video that is not in the answer was accepted")
	}
}

func TestParseNextRefusesWhatIsNotAnAnswer(t *testing.T) {
	for name, doc := range map[string]string{
		"html": `<html>`, "error": `{"error":{"code":400,"message":"boom"}}`, "other": `{"something":"else"}`,
	} {
		if _, err := parseNext([]byte(doc), "aaaaaaaaaaa"); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestEnrichAsksNextWithoutCredentialsAndKeepsWhatItHad(t *testing.T) {
	data, _ := os.ReadFile("testdata/next-song.json")
	var body map[string]any
	var hdr http.Header
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr, path = r.Header, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	s := &Source{HTTP: srv.Client(), NextURL: srv.URL + "/youtubei/v1/next"}
	in := music.Track{ID: "lYBUbBu4W08", Title: "kept title", ArtURL: "kept-art"}
	got, err := s.Enrich(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "kept title" || got.ArtURL != "kept-art" || got.Artists[0] != "Rick Astley" || got.Album == "" || got.Duration == 0 {
		t.Fatalf("%+v", got)
	}
	if body["videoId"] != "lYBUbBu4W08" || !strings.HasSuffix(path, "/next") || hdr.Get("Cookie") != "" || hdr.Get("Authorization") != "" {
		t.Fatalf("request %v %s %v", body, path, hdr)
	}
	if _, err := s.Enrich(context.Background(), music.Track{ID: "../x"}); err == nil {
		t.Error("a bad video ID was sent")
	}
}

func TestEnrichErrorsLeakNoAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	s := &Source{HTTP: srv.Client(), NextURL: srv.URL}
	if _, err := s.Enrich(context.Background(), music.Track{ID: "lYBUbBu4W08"}); err == nil || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("%v", err)
	}
	srv.Close()
	if _, err := s.Enrich(context.Background(), music.Track{ID: "lYBUbBu4W08"}); err == nil || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("%v", err)
	}
}
