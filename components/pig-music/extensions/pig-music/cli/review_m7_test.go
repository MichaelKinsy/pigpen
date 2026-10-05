//go:build !windows

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rev-pig-music-m7: the /music quick commands run this code inside PiG, so its cost is the slash command's cost. In auto mode
// every command ran the whole doctor (mpv --version, yt-dlp --version and -v, the browser lookup) before it looked for a
// running player, about 2 s on the test machine for a /music pause. A running player is the one to control and needs no doctor.
func TestAutoModeControlsARunningMpvWithoutRunningTheDoctor(t *testing.T) {
	r := newRig(t)
	r.ok("play", "night", "drive") // the fake mpv is running and playing
	log := filepath.Join(t.TempDir(), "runs")
	env := fakeBin(t, r, map[string]string{
		"mpv":    `echo mpv >> ` + log + `; echo "mpv 0.37.0 Copyright"`,
		"yt-dlp": `echo yt-dlp >> ` + log + "\n" + fakeYtdlp,
	})
	vars := env.Getenv
	env.Getenv = func(k string) string {
		if k == "PIG_MUSIC_ENGINE" {
			return "" // auto, the default
		}
		return vars(k)
	}
	for _, args := range [][]string{{"pause"}, {"now"}, {"vol", "40"}, {"queue"}, {"repeat", "all"}} {
		var out, errOut bytes.Buffer
		if code := Run(context.Background(), args, IO{Out: &out, Err: &errOut}, env); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errOut.String())
		}
	}
	if data, _ := os.ReadFile(log); len(data) != 0 {
		t.Errorf("controlling a running mpv ran the doctor's programs:\n%s", data)
	}
}

// The slash command names itself in its hints: "start with `/music play`", not the command line's `pigmusic play`.
func TestHintsNameTheCommandTheUserTyped(t *testing.T) {
	r := newRig(t)
	env := r.env()
	env.Name = "/music"
	for _, args := range [][]string{{"pause"}, {"play", "2"}} {
		var out, errOut bytes.Buffer
		Run(context.Background(), args, IO{Out: &out, Err: &errOut}, env)
		if s := errOut.String(); strings.Contains(s, "pigmusic play") || strings.Contains(s, "pigmusic search") || !strings.Contains(s, "/music ") {
			t.Errorf("%v: %q", args, s)
		}
	}
	// the command line keeps its own name
	_, errOut, _ := r.run("pause")
	if !strings.Contains(errOut, "`pigmusic play`") {
		t.Errorf("pigmusic: %q", errOut)
	}
}
