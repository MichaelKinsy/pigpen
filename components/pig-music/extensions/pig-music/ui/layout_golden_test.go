package ui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/internal/screens"
)

// goldenWidths are the widths the layouts are kept at: one in each band of the Player screen (the track's text over the panel,
// its cover beside it, two columns, three) and the half of a wide terminal the owner uses.
var goldenWidths = []int{40, 56, 80, 107, 160}

// TestTheLayoutsPerWidthAreTheGoldenOnes: the Player screen (with lyrics, and without) and the Search screen's home at each
// band's width are the screens in testdata/layouts, character for character (styles aside: the kit parity and the screens'
// dump compare those). PIG_MUSIC_UPDATE_GOLDEN=1 writes them.
func TestTheLayoutsPerWidthAreTheGoldenOnes(t *testing.T) {
	update := os.Getenv("PIG_MUSIC_UPDATE_GOLDEN") == "1"
	for _, name := range []string{"player-cover-lyrics", "player-cover", "player-lyrics-plain", "search-home"} {
		for _, w := range goldenWidths {
			h := 30
			if w < 60 {
				h = 20
			}
			s, ok := screens.At(name, w, h)
			if !ok {
				t.Fatalf("no scene %s", name)
			}
			got := ansi.Strip(s.Model.View().Content) + "\n"
			file := filepath.Join("testdata", "layouts", s.Name+".txt")
			if update {
				if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("%v (PIG_MUSIC_UPDATE_GOLDEN=1 writes it)", err)
			}
			if got != string(want) {
				gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
				for i := range min(len(gl), len(wl)) {
					if gl[i] != wl[i] {
						t.Errorf("%s: row %d differs:\n got %q\nwant %q", s.Name, i, gl[i], wl[i])
						break
					}
				}
				if len(gl) != len(wl) {
					t.Errorf("%s: %d rows, want %d", s.Name, len(gl), len(wl))
				}
			}
		}
	}
}
