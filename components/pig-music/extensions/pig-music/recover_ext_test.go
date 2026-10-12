package pig_music_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	pig_music "github.com/MichaelKinsy/pigpen/pig-music"
	"github.com/MichaelKinsy/pigpen/pig-music/internal/mpvfake"
	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// The player the extension builds loads a track whose stream is cut again in its place, once; cut again, it moves past it and
// says why: while the player is hidden, as a PiG notification (and on the player's status line when it is open,
// ui.NoticeMsg).
func TestATrackThePlayerMovesPastIsReportedAsANotificationWhileHidden(t *testing.T) {
	dir, err := os.MkdirTemp("", "pm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	env := map[string]string{"XDG_RUNTIME_DIR": dir, "HOME": dir}
	paths, err := music.DefaultPaths(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	srv, err := mpvfake.Listen(paths.Socket, mpvfake.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	src := fixedSource{songs(1)}
	// Another client (a pigmusic command) started the music, with repeat one; the extension finds it playing.
	ctl := mpv.New(mpv.Config{Paths: paths, PlayURL: src.PlayURL})
	if err := ctl.Attach(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ctl.Close() })
	if err := ctl.SetRepeat(t.Context(), music.RepeatOne); err != nil {
		t.Fatal(err)
	}
	if err := ctl.Replace(t.Context(), src.tracks, 0); err != nil {
		t.Fatal(err)
	}
	srv.Advance(3.3)

	h := startOverlayHost(t, pig_music.ExtensionWith(src, nil, env, nil, nil), "tui", 100, 30)
	h.emit("session_start", map[string]any{"reason": "startup"})
	waitFor(t, "the playing track in the footer", statusHas(h, "Song 1"))
	was := srv.IDs()
	srv.CutCurrent(3.3)
	waitFor(t, "the track loaded again", func() bool { return !slices.Equal(srv.IDs(), was) })
	srv.CutCurrent(4)
	waitFor(t, "a notification", func() bool { return len(h.notifications()) > 0 })
	got := fmt.Sprint(h.notifications())
	if !strings.Contains(got, `"Song 1" could not be played after a retry`) || !strings.Contains(got, "pig-music") {
		t.Errorf("notified %s", got)
	}
}
