package pig_music_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	pig_music "github.com/MichaelKinsy/pigpen/pig-music"
	"github.com/MichaelKinsy/pigpen/pig-music/internal/mpvfake"
	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// pagedSource is a source with pages: the radio of every song is two more songs.
type pagedSource struct{ fixedSource }

func (pagedSource) Watch(_ context.Context, id string) (music.Watch, error) {
	return music.Watch{Radio: []music.Track{{ID: id, Title: "Song 1", Artists: []string{"Some Artist"}}, {ID: "radio000001", Title: "Radio One", Artists: []string{"Some Artist"}}}}, nil
}
func (pagedSource) OpenPage(context.Context, music.Entity) (music.Page, error) {
	return music.Page{}, nil
}

func TestMusicRadioOpensThePlayerOnTheRadioOfTheSongThatIsPlaying(t *testing.T) {
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
	src := pagedSource{fixedSource{songs(5)}}
	p := mpv.New(mpv.Config{Paths: paths, PlayURL: src.PlayURL})
	t.Cleanup(func() { _ = p.Close() })
	h := startOverlayHost(t, pig_music.ExtensionWith(src, p, env, nil, nil), "tui", 100, 30)

	// nothing is playing: a notification, no screen
	done := h.command("music", "radio")
	if failure := <-done; failure != "" {
		t.Fatal(failure)
	}
	if got := fmt.Sprint(h.notifications()); !strings.Contains(got, "nothing is playing") {
		t.Errorf("notifications %q", got)
	}

	done = h.command("music")
	h.waitOpen()
	h.waitSnapshot("the search screen", hasText("Press / to search"))
	for _, in := range []string{"/", "n", "i", "g", "h", "t", "\r"} {
		h.input(in)
	}
	h.waitSnapshot("results", hasText("Song 5"))
	h.input("\r")
	h.waitSnapshot("playing", hasText("Playing"))
	h.input("q")
	<-done

	done = h.command("music", "radio")
	h.waitOpenN(2)
	text := screenText(h.waitSnapshot("the radio page", hasText("Radio One")))
	if !strings.Contains(text, "Player › Radio: Song 1") {
		t.Errorf("no breadcrumbs:\n%s", text)
	}
	h.input("\x1b") // back: the Player screen
	h.waitSnapshot("the player again", hasText("Up Next"))
	h.input("q")
	<-done
}
