package pig_music_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func plainText(s snapshot) string { return ansi.Strip(screenText(s)) }

func hasPlain(want string) func(snapshot) bool {
	return func(s snapshot) bool { return strings.Contains(plainText(s), want) }
}

// TestTheSearchHomeKeepsItsRecentsInTheDataDirectoryAndTakesTheMouse: through the SDK, a search and a track played are on the
// Search screen's home (a private file in pig-music's data directory), a double click on a recent search runs it again, and C
// clears the file.
func TestTheSearchHomeKeepsItsRecentsInTheDataDirectoryAndTakesTheMouse(t *testing.T) {
	h, srv, env, _ := playerEnv(t)
	paths, err := music.DefaultPaths(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	file := music.RecentsPath(paths.Data)
	done := h.command("music")
	h.waitOpen()
	h.waitSnapshot("the search screen", hasText("Press / to search"))
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("a history before anything was searched: %v", err)
	}
	for _, in := range []string{"/", "x", "\r"} {
		h.input(in)
	}
	results := h.waitSnapshot("results", hasText("Song 5"))
	x, y := cellIn(results, "Song 3")
	gesture(h, x, y, 1)
	gesture(h, x, y, 2)
	waitFor(t, "mpv playing the third result", func() bool {
		urls, pos := srv.Playlist()
		return len(urls) == 5 && pos == 2
	})
	player := h.waitSnapshot("the player screen", hasText("Up Next"))
	tx, ty := cellIn(player, " Search ")
	gesture(h, tx, ty, 1)
	h.waitSnapshot("the results again", hasText(`Results for "x"`))
	h.input("\x7f") // backspace: from the results to the home
	home := h.waitSnapshot("the home", hasPlain("Recents  C clear"))
	text := plainText(home)
	if i, j := strings.Index(text, "Song 3"), strings.Index(text, "/    x"); i < 0 || j < 0 || i > j {
		t.Fatalf("the home lists the track played, then the search:\n%s", text)
	}
	data, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(data), "song0000002") || !strings.Contains(string(data), `"query":"x"`) {
		t.Fatalf("the history file: %s %v", data, err)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 || filepath.Dir(file) != paths.Data {
		t.Errorf("the history file is %v in %s", info.Mode(), filepath.Dir(file))
	}
	sx, sy := cellIn(home, "/    x")
	gesture(h, sx+5, sy, 1)
	gesture(h, sx+5, sy, 2)
	h.waitSnapshot("the search run again", hasText(`Results for "x"`))
	h.input("\x7f")
	h.waitSnapshot("the home again", hasPlain("Recents  C clear"))
	h.input("C")
	h.waitSnapshot("the home without its recents", func(s snapshot) bool {
		return !strings.Contains(plainText(s), "Recents  C clear") && strings.Contains(plainText(s), "Press / to search")
	})
	waitFor(t, "the history file gone", func() bool {
		_, err := os.Stat(file)
		return os.IsNotExist(err)
	})
	h.input("q")
	if failure := <-done; failure != "" {
		t.Fatal(failure)
	}
}
