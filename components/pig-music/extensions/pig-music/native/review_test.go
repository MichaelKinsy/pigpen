package native

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Rules carried over from the M4/M4a review: network text has no control
// characters, WaxTap's debug dumps (which hold signed URLs) cannot be switched on
// by the environment, and a stalled transfer is given up, not waited on.

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func TestCleanStripsControlCharacters(t *testing.T) {
	got := Clean("Song\x1b[2J\x07 Title\twith\ntabs\u0085\u202e")
	if hasControl(got) || strings.Contains(got, "\x1b") {
		t.Fatalf("Clean left controls: %q", got)
	}
	if !strings.Contains(got, "Song") || !strings.Contains(got, "Title with tabs") {
		t.Fatalf("Clean damaged the text: %q", got)
	}
	if Clean("Ünïcödé 音楽") != "Ünïcödé 音楽" {
		t.Fatal("Clean must keep ordinary text")
	}
}

func TestSearchResultsAreCleaned(t *testing.T) {
	const doc = `{"contents":{"tabbedSearchResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{"contents":[
	 {"musicShelfRenderer":{"contents":[
	  {"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"AAAAAAAAAAA"},"flexColumns":[
	   {"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Evil\u001b]0;pwn\u0007 Title"}]}}},
	   {"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Ban\u001b[31md"},{"text":" • "},{"text":"Alb\u0000um"},{"text":" • "},{"text":"1:00"}]}}}]}}
	 ]}}]}}}}]}}}`
	tr, err := parseSearch([]byte(doc), 5)
	if err != nil || len(tr) != 1 {
		t.Fatalf("%v %v", tr, err)
	}
	for _, s := range append([]string{tr[0].Title, tr[0].Album}, tr[0].Artists...) {
		if hasControl(s) {
			t.Errorf("control character reached a track field: %q", s)
		}
	}
}

func TestPlaylistEntriesAreCleaned(t *testing.T) {
	s := &Source{Lister: &fakeLister{pl: []PlaylistEntry{{VideoID: "AAAAAAAAAAA", Title: "A\x1b[2Jb", Author: "C\x07d"}}}}
	tr, err := s.Tracks(context.Background(), "PLabcdefghijklmnop")
	if err != nil || len(tr) != 1 || hasControl(tr[0].Title) || hasControl(tr[0].Artists[0]) {
		t.Fatalf("%+v %v", tr, err)
	}
}

func TestErrorsAndLogsHaveNoControlCharacters(t *testing.T) {
	if got := Redact("bad \x1b[31mred\x1b[0m text\x07"); hasControl(got) {
		t.Fatalf("Redact left controls: %q", got)
	}
	if msg := describeFailure(music.Track{Title: "T\x1b[2J"}, os.ErrClosed); hasControl(msg) {
		t.Fatalf("LastError has controls: %q", msg)
	}
}

func TestDumpEnvironmentIsNeutralised(t *testing.T) {
	t.Setenv("WAXTAP_DUMP_DIR", t.TempDir())
	t.Setenv("WAXTAP_SABR_DUMP_DIR", t.TempDir())
	SanitizeEnvironment()
	if os.Getenv("WAXTAP_DUMP_DIR") != "" || os.Getenv("WAXTAP_SABR_DUMP_DIR") != "" {
		t.Fatal("WaxTap's dump variables are still set: they would write signed URLs to disk")
	}
	env := CleanEnv([]string{"A=1", "WAXTAP_DUMP_DIR=/x", "WAXTAP_SABR_DUMP_DIR=/y", "B=2"})
	if len(env) != 2 || env[0] != "A=1" || env[1] != "B=2" {
		t.Fatalf("CleanEnv = %v", env)
	}
}

func TestStalledReadIsGivenUp(t *testing.T) {
	o, d := newOpener(map[string]int64{"t1": 1 << 20, "t2": 100}), &fakeOut{}
	o.readGate["t1"] = make(chan struct{}) // t1's network read never returns
	e, err := NewEngine(EngineConfig{Opener: o, Output: d, Tick: 5 * time.Millisecond, StallTimeout: 150 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	_ = e.Replace(bg, tracks(2), 0)
	eventually(t, "t1 to be playing", func() bool { return d.isRunning() && e.State().Duration > 0 })
	go func() { // the device pulls and gets stuck in the read
		buf := make([]byte, 800)
		_, _ = d.src.Read(buf)
	}()
	eventually(t, "the stall to be given up and track 2 to play", func() bool { return e.State().Index == 1 })
	if msg := e.LastError(); !strings.Contains(msg, "stalled") {
		t.Fatalf("LastError = %q", msg)
	}
}
