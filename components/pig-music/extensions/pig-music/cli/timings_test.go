//go:build !windows

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

type enrichingSearcher struct {
	fakeSearcher
	enriched int
}

func (e *enrichingSearcher) Enrich(_ context.Context, t music.Track) (music.Track, error) {
	e.enriched++
	return t, nil
}

// M9c: `pigmusic doctor --timings` says where the time goes, step by step, so a slow machine can be told from a slow step.
func TestDoctorTimingsListsEveryStepWithItsDuration(t *testing.T) {
	r := newRig(t)
	log := filepath.Join(t.TempDir(), "runs.log")
	env := fakeBin(t, r, map[string]string{"mpv": `echo "mpv 0.37.0 Copyright"`, "yt-dlp": libraryYtdlp(log)})
	s := &enrichingSearcher{fakeSearcher: fakeSearcher{tracks: []music.Track{{ID: "lYBUbBu4W08", Title: "T", Artists: []string{"A"}, Duration: time.Minute}}}}
	env.Searcher = s
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"doctor", "--timings"}, IO{Out: &out, Err: &errOut}, env)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out.String(), errOut.String())
	}
	text := out.String()
	for _, want := range []string{"timings", "all of it", "mpv --version", "yt-dlp --version", "search (direct", "details (direct", "stream, what mpv waits for", "stream, prefetched", "library (account)", " ms"} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
	if s.calls != 1 || s.enriched != 1 {
		t.Errorf("direct search asked %d times, details %d", s.calls, s.enriched)
	}
	if b, _ := os.ReadFile(log); !strings.Contains(string(b), "search?q=") {
		t.Errorf("the yt-dlp search was not timed:\n%s", b)
	}
}

func TestDoctorTakesOnlyItsOwnFlags(t *testing.T) {
	var errOut bytes.Buffer
	env := newRig(t).env()
	if code := Run(context.Background(), []string{"doctor", "--nope"}, IO{Out: &bytes.Buffer{}, Err: &errOut}, env); code != 2 || !strings.Contains(errOut.String(), "--timings") {
		t.Errorf("%d %q", code, errOut.String())
	}
}
