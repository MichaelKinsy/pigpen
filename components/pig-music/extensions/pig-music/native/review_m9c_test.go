package native

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// rev-pig-music-m9c F2: the direct search and details made a new HTTP transport for every request (Source.HTTP nil), so
// each one paid a new TCP and TLS handshake (measured on the test machine: 169 ms a detail, 129 ms with one client) and left an
// idle connection behind in a transport nobody used again (up to 30 of them while a list is filled in). The Searcher the
// extension and pigmusic use keeps one client.
func TestTheSearcherReusesItsConnection(t *testing.T) {
	data, err := os.ReadFile("testdata/next-song.json")
	if err != nil {
		t.Fatal(err)
	}
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) }))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()
	src, ok := NewSearcher(nil).(*Source)
	if !ok {
		t.Fatal("NewSearcher is not the direct Source")
	}
	src.NextURL = srv.URL + "/youtubei/v1/next"
	for i := 0; i < 3; i++ {
		if _, err := src.Enrich(context.Background(), music.Track{ID: "lYBUbBu4W08"}); err != nil {
			t.Fatal(err)
		}
	}
	if n := conns.Load(); n != 1 {
		t.Errorf("three requests opened %d connections", n)
	}
}

func TestPIGMUSICSEARCHytdlpTurnsTheDirectSearchOff(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "PIG_MUSIC_SEARCH" {
				return v
			}
			return ""
		}
	}
	if NewSearcher(env(" YTDLP ")) != nil {
		t.Error("PIG_MUSIC_SEARCH=ytdlp left the direct search on")
	}
	if NewSearcher(env("")) == nil || NewSearcher(nil) == nil {
		t.Error("the direct search is off by default")
	}
}
