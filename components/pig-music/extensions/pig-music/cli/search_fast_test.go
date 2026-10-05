//go:build !windows

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

type fakeSearcher struct {
	tracks []music.Track
	err    error
	calls  int
}

func (f *fakeSearcher) Search(context.Context, string, int) ([]music.Track, error) {
	f.calls++
	return f.tracks, f.err
}
func (f *fakeSearcher) Library(context.Context) ([]music.Collection, error)   { panic("library") }
func (f *fakeSearcher) Tracks(context.Context, string) ([]music.Track, error) { panic("tracks") }
func (f *fakeSearcher) PlayURL(music.Track) string                            { panic("playurl") }

// M9c: `pigmusic search` measured about 2 s before it searched at all, because the doctor ran mpv --version, yt-dlp --version,
// yt-dlp -v and node --version first. A search the direct YouTube Music listing answers needs none of them.
func TestASearchTheDirectListingAnswersRunsNoProgramAtAll(t *testing.T) {
	r := newRig(t)
	log := filepath.Join(t.TempDir(), "runs.log")
	env := fakeBin(t, r, map[string]string{"mpv": `echo x >> ` + log + `; echo "mpv 0.37.0 Copyright"`, "yt-dlp": libraryYtdlp(log)})
	s := &fakeSearcher{tracks: []music.Track{{ID: "lYBUbBu4W08", Title: "Never Gonna Give You Up", Artists: []string{"Rick Astley"}, Duration: 214 * time.Second}}}
	env.Searcher = s
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"search", "never gonna"}, IO{Out: &out, Err: &errOut}, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "lYBUbBu4W08") || !strings.Contains(out.String(), "3:34") || !strings.Contains(out.String(), "Rick Astley") {
		t.Errorf("output %q", out.String())
	}
	if b, _ := os.ReadFile(log); len(b) != 0 {
		t.Errorf("a program ran for a search the direct listing answered:\n%s", b)
	}
	// the result list is saved, so `play 1` works after it
	if tracks, err := (&command{paths: mustPaths(t, env)}).lastResults(); err != nil || len(tracks) != 1 {
		t.Errorf("saved results %v, %v", tracks, err)
	}
}

func TestASearchTheDirectListingCannotAnswerGoesThroughTheDoctorAndYtdlp(t *testing.T) {
	r := newRig(t)
	log := filepath.Join(t.TempDir(), "runs.log")
	env := fakeBin(t, r, map[string]string{"mpv": `echo "mpv 0.37.0 Copyright"`, "yt-dlp": libraryYtdlp(log)})
	s := &fakeSearcher{err: errors.New("YouTube Music search answered HTTP 403")}
	env.Searcher = s
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"search", "x"}, IO{Out: &out, Err: &errOut}, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "A song") || s.calls != 1 {
		t.Errorf("output %q, direct listing asked %d times", out.String(), s.calls)
	}
	if b, _ := os.ReadFile(log); !strings.Contains(string(b), "search?q=") {
		t.Errorf("yt-dlp was not asked:\n%s", b)
	}
}

func mustPaths(t *testing.T, env Env) music.Paths {
	t.Helper()
	p, err := music.DefaultPaths(env.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAutoModeSearchDoesNotWaitForTheDoctorEither(t *testing.T) {
	r := newRig(t)
	log := filepath.Join(t.TempDir(), "runs.log")
	env := fakeBin(t, r, map[string]string{"mpv": `echo x >> ` + log + `; echo "mpv 0.37.0 Copyright"`, "yt-dlp": libraryYtdlp(log)})
	inner := env.Getenv
	env.Getenv = func(k string) string {
		if k == "PIG_MUSIC_ENGINE" {
			return "auto"
		}
		return inner(k)
	}
	env.Searcher = &fakeSearcher{tracks: []music.Track{{ID: "lYBUbBu4W08", Title: "T", Artists: []string{"A"}, Duration: time.Minute}}}
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"search", "x"}, IO{Out: &out, Err: &errOut}, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if b, _ := os.ReadFile(log); len(b) != 0 {
		t.Errorf("a program ran in auto mode for a search the direct listing answered:\n%s", b)
	}
}

// rev-pig-music-m9c F3: a search the direct listing could not answer goes on to the doctor and yt-dlp; when that cannot run
// (no mpv here), the message named mpv alone, not why the direct listing (which needs no program) failed.
func TestASearchThatCannotGoOnToYtdlpAlsoSaysWhyTheDirectListingFailed(t *testing.T) {
	r := newRig(t)
	log := filepath.Join(t.TempDir(), "runs.log")
	env := fakeBin(t, r, map[string]string{"yt-dlp": libraryYtdlp(log)}) // no mpv
	env.Searcher = &fakeSearcher{err: errors.New("YouTube Music search answered HTTP 400")}
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"search", "x"}, IO{Out: &out, Err: &errOut}, env); code == 0 {
		t.Fatalf("exit 0: %s", out.String())
	}
	if !strings.Contains(errOut.String(), "HTTP 400") || !strings.Contains(errOut.String(), "mpv") {
		t.Errorf("stderr %q", errOut.String())
	}
}
