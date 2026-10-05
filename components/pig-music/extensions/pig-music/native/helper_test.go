package native

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// The test binary doubles as `pigmusic native <playlist|probe> <id>` (see main_test.go).
func fakeHelper(mode string, args []string) int {
	switch mode {
	case "helper-ok":
		switch args[0] {
		case "playlist":
			WriteHelperReply(os.Stdout, []PlaylistEntry{
				{VideoID: "dQw4w9WgXcQ", Title: "args: " + strings.Join(args, " "), Author: "A", Duration: 3 * time.Minute},
				{VideoID: "bad", Title: "not a video id"},
			}, nil, nil)
		case "probe":
			WriteHelperReply(os.Stdout, nil, &Check{Name: "stream probe", OK: true, Detail: "resolved in 5 ms for " + args[1]}, nil)
		}
		return 0
	case "helper-error":
		WriteHelperReply(os.Stdout, nil, nil, fmt.Errorf(`Get "https://rr1.googlevideo.com/videoplayback?sig=SECRET": dial tcp 203.0.113.9:443: refused`))
		return 1
	case "helper-garbage":
		fmt.Println("this is not json")
		return 0
	case "helper-flood":
		chunk := strings.Repeat("x", 1<<20)
		for i := 0; i < 10; i++ {
			fmt.Print(chunk)
		}
		return 0
	case "helper-hang":
		time.Sleep(time.Hour)
		return 0
	}
	if code := reviewHelper(mode, args); code >= 0 {
		return code
	}
	return 4
}

func helperFor(t *testing.T, mode string) Helper {
	t.Helper()
	t.Setenv(testModeEnv, mode)
	return Helper{Binary: os.Args[0], Timeout: 5 * time.Second}
}

func TestHelperListsAPlaylistThroughThePigmusicProgram(t *testing.T) {
	h := helperFor(t, "helper-ok")
	entries, err := h.Playlist(context.Background(), "PLabcdefghijklmnop")
	if err != nil || len(entries) != 2 || entries[0].VideoID != "dQw4w9WgXcQ" || entries[0].Duration != 3*time.Minute {
		t.Fatalf("%+v %v", entries, err)
	}
	if !strings.Contains(entries[0].Title, "args: playlist PLabcdefghijklmnop") {
		t.Errorf("the helper was run as %q", entries[0].Title)
	}
}

func TestASourceListsPlaylistTracksThroughTheHelper(t *testing.T) {
	src := &Source{Lister: helperFor(t, "helper-ok")}
	tracks, err := src.Tracks(context.Background(), "PLabcdefghijklmnop")
	if err != nil || len(tracks) != 1 || tracks[0].ID != "dQw4w9WgXcQ" || tracks[0].Artists[0] != "A" {
		t.Fatalf("%+v %v", tracks, err)
	}
	var _ music.Source = src
}

func TestHelperErrorsAreShownWithoutURLsOrAddresses(t *testing.T) {
	h := helperFor(t, "helper-error")
	_, err := h.Playlist(context.Background(), "PLabcdefghijklmnop")
	if err == nil {
		t.Fatal("no error")
	}
	for _, leak := range []string{"googlevideo", "SECRET", "203.0.113.9", "https://"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("the error leaks %q: %v", leak, err)
		}
	}
}

func TestHelperGarbageAndFloodsAreRefusedNotTrusted(t *testing.T) {
	// (review of M9b: a flood is now stopped at the cap and said to be one)
	for mode, want := range map[string]string{"helper-garbage": "cannot read", "helper-flood": "MiB"} {
		h := helperFor(t, mode)
		_, err := h.Playlist(context.Background(), "PLabcdefghijklmnop")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", mode, err)
		}
	}
}

func TestAHelperThatHangsIsStoppedOnTime(t *testing.T) {
	h := helperFor(t, "helper-hang")
	h.Timeout = 400 * time.Millisecond
	start := time.Now()
	_, err := h.Playlist(context.Background(), "PLabcdefghijklmnop")
	if err == nil || !strings.Contains(err.Error(), "in time") || time.Since(start) > 4*time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
}

func TestAMissingHelperProgramSaysHowToGetIt(t *testing.T) {
	h := Helper{Configured: "/nonexistent/pigmusic", Getenv: func(string) string { return "" }}
	_, err := h.Playlist(context.Background(), "PLabcdefghijklmnop")
	if err == nil || !strings.Contains(err.Error(), "pigmusic") {
		t.Fatalf("%v", err)
	}
	c := h.Probe(context.Background(), "dQw4w9WgXcQ")
	if c.OK || c.Name != "stream probe" || !strings.Contains(c.Detail, "pigmusic") {
		t.Fatalf("%+v", c)
	}
}

func TestHelperProbeReturnsTheHelpersCheck(t *testing.T) {
	c := helperFor(t, "helper-ok").Probe(context.Background(), "dQw4w9WgXcQ")
	if !c.OK || !strings.Contains(c.Detail, "dQw4w9WgXcQ") {
		t.Fatalf("%+v", c)
	}
}
