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

func TestParseSearchRecordedResponse(t *testing.T) {
	data, err := os.ReadFile("testdata/search-music.json")
	if err != nil {
		t.Fatal(err)
	}
	tracks, err := parseSearch(data, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 6 {
		t.Fatalf("%d tracks, want 6", len(tracks))
	}
	first := tracks[0]
	if first.ID != "MJoSyNdffGo" || first.Title != "Chamber Of Reflection" ||
		len(first.Artists) != 1 || first.Artists[0] != "Mac DeMarco" || first.Album != "Salad Days" ||
		first.Duration != 3*time.Minute+52*time.Second || !strings.HasPrefix(first.ArtURL, "https://") {
		t.Fatalf("first track: %+v", first)
	}
	// Several artists, and an album equal to the title.
	var multi *music.Track
	for i := range tracks {
		if len(tracks[i].Artists) == 2 {
			multi = &tracks[i]
		}
	}
	if multi == nil || multi.Artists[0] != "IRSNa" || multi.Artists[1] != "RACH" {
		t.Fatalf("no two-artist track parsed: %+v", tracks)
	}
	if got, _ := parseSearch(data, 2); len(got) != 2 {
		t.Fatalf("limit 2 gave %d", len(got))
	}
}

func TestParseSearchSurvivesOddItems(t *testing.T) {
	const doc = `{"contents":{"tabbedSearchResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{"contents":[
	 {"musicShelfRenderer":{"contents":[
	  {"musicResponsiveListItemRenderer":{"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"No ID"}]}}}]}},
	  {"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"bad id"},"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Bad ID"}]}}}]}},
	  {"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"AAAAAAAAAAA"},"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Plain"}]}}},{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Song"},{"text":" • "},{"text":"Some Band"},{"text":" • "},{"text":"Some Album"},{"text":" • "},{"text":"1:02:03"}]}}}]}},
	  {"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"AAAAAAAAAAA"},"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Duplicate"}]}}}]}},
	  {"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"BBBBBBBBBBB"}}}
	 ]}}]}}}}]}}}`
	tracks, err := parseSearch([]byte(doc), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 { // no ID, a bad ID, a duplicate and one with no title are all dropped
		t.Fatalf("%+v", tracks)
	}
	p := tracks[0]
	if p.Title != "Plain" || len(p.Artists) != 1 || p.Artists[0] != "Some Band" || p.Album != "Some Album" || p.Duration != time.Hour+2*time.Minute+3*time.Second {
		t.Fatalf("plain: %+v", p)
	}
}

func TestParseSearchShapes(t *testing.T) {
	if tr, err := parseSearch([]byte(`{"contents":{"tabbedSearchResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{"contents":[{"itemSectionRenderer":{}}]}}}}]}}}`), 5); err != nil || len(tr) != 0 {
		t.Fatalf("no songs shelf is an empty result: %v %v", tr, err)
	}
	if _, err := parseSearch([]byte(`{"error":{"code":400,"message":"boom"}}`), 5); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("an API error must be shown: %v", err)
	}
	if _, err := parseSearch([]byte(`{"something":"else"}`), 5); err == nil {
		t.Fatal("an unrecognised response must be an error, not silence")
	}
	if _, err := parseSearch([]byte(`<html>`), 5); err == nil {
		t.Fatal("non-JSON must be an error")
	}
}

func TestSourceSearchRequest(t *testing.T) {
	data, _ := os.ReadFile("testdata/search-music.json")
	var got map[string]any
	var hdr http.Header
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr, path = r.Header, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	s := &Source{HTTP: srv.Client(), SearchURL: srv.URL + "/youtubei/v1/search"}
	tracks, err := s.Search(context.Background(), "night drive", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 3 {
		t.Fatalf("%d tracks", len(tracks))
	}
	if got["query"] != "night drive" || got["params"] == "" {
		t.Fatalf("request body %v", got)
	}
	client := got["context"].(map[string]any)["client"].(map[string]any)
	if client["clientName"] != "WEB_REMIX" {
		t.Fatalf("client %v", client)
	}
	if hdr.Get("User-Agent") == "" || hdr.Get("Cookie") != "" || hdr.Get("Authorization") != "" {
		t.Fatalf("headers %v: need a User-Agent and no credentials", hdr)
	}
	if !strings.HasSuffix(path, "/search") {
		t.Fatalf("path %s", path)
	}
	if _, err := s.Search(context.Background(), "  ", 3); err == nil {
		t.Error("an empty query must be refused")
	}
}

func TestSourceSearchErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	s := &Source{HTTP: srv.Client(), SearchURL: srv.URL}
	_, err := s.Search(context.Background(), "x", 3)
	if err == nil || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("err = %v", err)
	}
	srv.Close()
	_, err = s.Search(context.Background(), "x", 3)
	if err == nil || strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "http://") {
		t.Fatalf("a refused connection must not leak the address: %v", err)
	}
	// The call is bounded even when the origin never answers.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.ReadAll(r.Body); <-r.Context().Done() }))
	defer slow.Close()
	s2 := &Source{HTTP: slow.Client(), SearchURL: slow.URL, Timeout: 100 * time.Millisecond}
	start := time.Now()
	if _, err := s2.Search(context.Background(), "x", 3); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("no timeout: %v after %v", err, time.Since(start))
	}
}

func TestSourceLibraryAndCollections(t *testing.T) {
	s := &Source{}
	if _, err := s.Library(context.Background()); err == nil || !strings.Contains(err.Error(), "mpv") {
		t.Fatalf("Library: %v (must say the native engine has no sign-in and point at the mpv engine)", err)
	}
	for _, id := range []string{"LM", "MPREb_abc", "not a playlist"} {
		if _, err := s.Tracks(context.Background(), id); err == nil {
			t.Errorf("Tracks(%q) must fail", id)
		}
	}
	if got := s.PlayURL(music.Track{ID: "dQw4w9WgXcQ"}); got != "https://music.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("PlayURL %q", got)
	}
}

type fakeLister struct {
	got string
	pl  []PlaylistEntry
	err error
}

func (f *fakeLister) Playlist(ctx context.Context, id string) ([]PlaylistEntry, error) {
	f.got = id
	return f.pl, f.err
}

func TestSourceTracksFromAPlaylist(t *testing.T) {
	l := &fakeLister{pl: []PlaylistEntry{
		{VideoID: "AAAAAAAAAAA", Title: "One", Author: "Band", Duration: 90 * time.Second},
		{VideoID: "bad", Title: "Broken"},
		{VideoID: "BBBBBBBBBBB", Title: "", Author: "Band"},
		{VideoID: "CCCCCCCCCCC", Title: "Three"},
		{VideoID: "AAAAAAAAAAA", Title: "One again"},
	}}
	s := &Source{Lister: l}
	tracks, err := s.Tracks(context.Background(), "PLabcdefghijklmnop")
	if err != nil {
		t.Fatal(err)
	}
	if l.got != "PLabcdefghijklmnop" {
		t.Fatalf("listed %q", l.got)
	}
	if len(tracks) != 2 || tracks[0].Title != "One" || tracks[0].Artists[0] != "Band" || tracks[0].Duration != 90*time.Second || tracks[1].ID != "CCCCCCCCCCC" {
		t.Fatalf("%+v", tracks)
	}
}
