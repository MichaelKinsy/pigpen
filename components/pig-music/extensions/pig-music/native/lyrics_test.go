package native

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func lyricsServer(t *testing.T, next string) (*Source, *[]string) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, r.URL.Path+" "+string(b))
		if r.Header.Get("Cookie")+r.Header.Get("Authorization") != "" {
			t.Error("a credential was sent")
		}
		if strings.HasSuffix(r.URL.Path, "/next") {
			_, _ = w.Write(fixture(t, next))
			return
		}
		_, _ = w.Write(fixture(t, "browse-lyrics.json"))
	}))
	t.Cleanup(srv.Close)
	return &Source{NextURL: srv.URL + "/next", BrowseURL: srv.URL + "/browse"}, &bodies
}

func TestYouTubeLyricsAreFoundThroughTheLyricsTabOfTheWatchNextAnswer(t *testing.T) {
	s, bodies := lyricsServer(t, "next-lyrics-tab.json")
	text, err := s.YouTubeLyrics(context.Background(), "fOT0BUpITw8")
	if err != nil || !strings.HasPrefix(text, "Invented first line\nInvented second line") {
		t.Fatalf("%q %v", text, err)
	}
	if len(*bodies) != 2 || !strings.Contains((*bodies)[1], `"browseId":"MPLYt_X8pk5s2tHrH"`) {
		t.Errorf("%v", *bodies)
	}
}

func TestATrackWithoutALyricsTabHasNoLyrics(t *testing.T) {
	s, bodies := lyricsServer(t, "next-no-lyrics-tab.json")
	if _, err := s.YouTubeLyrics(context.Background(), "fOT0BUpITw8"); !errors.Is(err, music.ErrNoLyrics) {
		t.Fatalf("%v", err)
	}
	if len(*bodies) != 1 {
		t.Errorf("%d requests", len(*bodies))
	}
}

func TestABadVideoIDIsNotSent(t *testing.T) {
	s, bodies := lyricsServer(t, "next-lyrics-tab.json")
	if _, err := s.YouTubeLyrics(context.Background(), "../x"); err == nil || len(*bodies) != 0 {
		t.Errorf("%v %d", err, len(*bodies))
	}
}
