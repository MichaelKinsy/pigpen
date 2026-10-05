package ytdlp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func enrichWith(out string, runErr error, got *[]string) *Source {
	return &Source{JSRuntime: "node", Runner: func(_ context.Context, _ string, args []string) ([]byte, []byte, error) {
		if got != nil {
			*got = args
		}
		return []byte(out), []byte("ERROR: [youtube] x: HTTP Error 403: Forbidden\n"), runErr
	}}
}

func TestEnrichFillsArtistAlbumAndDurationAndKeepsWhatItHad(t *testing.T) {
	var args []string
	s := enrichWith(`{"id": "abc", "title": "A Title", "artists": ["Rick Astley", "Guest"], "artist": "Rick Astley", "channel": "Rick Astley - Topic", "duration": 214, "album": "Whenever You Need Somebody"}`+"\n", nil, &args)
	got, err := s.Enrich(context.Background(), music.Track{ID: "abc", Title: "Shown title"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "abc" || got.Title != "Shown title" || strings.Join(got.Artists, "+") != "Rick Astley+Guest" ||
		got.Album != "Whenever You Need Somebody" || got.Duration != 214*time.Second {
		t.Errorf("%+v", got)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--js-runtimes node", "--skip-download", "--no-playlist", "--print", "https://music.youtube.com/watch?v=abc"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q lack %q", joined, want)
		}
	}
}

func TestEnrichFallsBackFromArtistsToArtistToChannel(t *testing.T) {
	for out, want := range map[string]string{
		`{"id":"a","artist":"Solo","channel":"Chan","duration":60}`:               "Solo",
		`{"id":"a","channel":"Rick Astley - Topic","uploader":"u","duration":60}`: "Rick Astley",
		`{"id":"a","uploader":"The Uploader","duration":60}`:                      "The Uploader",
		`{"id":"a","artists":null,"artist":null,"channel":null,"uploader":null}`:  "",
		`{"id":"a","artists":["A"],"artist":"B","channel":"C"}`:                   "A",
	} {
		got, err := enrichWith(out, nil, nil).Enrich(context.Background(), music.Track{ID: "a"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(got.Artists, "+") != want {
			t.Errorf("%s: artists %v, want %q", out, got.Artists, want)
		}
	}
}

func TestEnrichKeepsExistingFieldsWhenYtdlpHasNone(t *testing.T) {
	have := music.Track{ID: "a", Title: "T", Artists: []string{"Known"}, Duration: 99 * time.Second, Album: "Alb"}
	got, err := enrichWith(`{"id":"a","duration":null,"album":null}`, nil, nil).Enrich(context.Background(), have)
	if err != nil || got.Duration != 99*time.Second || got.Album != "Alb" || got.Artists[0] != "Known" {
		t.Errorf("%+v %v", got, err)
	}
}

func TestEnrichErrorsAreReportedAndKeepTheTrack(t *testing.T) {
	have := music.Track{ID: "a", Title: "T"}
	got, err := enrichWith("", errors.New("exit status 1"), nil).Enrich(context.Background(), have)
	if err == nil || !strings.Contains(err.Error(), "403") || got.ID != "a" || got.Title != "T" {
		t.Errorf("%+v %v", got, err)
	}
	if _, err := enrichWith("not json\n", nil, nil).Enrich(context.Background(), have); err == nil {
		t.Error("garbage was accepted")
	}
	if _, err := enrichWith("", nil, nil).Enrich(context.Background(), have); err == nil {
		t.Error("empty output was accepted")
	}
	if _, err := enrichWith("{}", nil, nil).Enrich(context.Background(), music.Track{}); err == nil {
		t.Error("a track without an ID was enriched")
	}
}
