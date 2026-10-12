package ui_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/internal/screens"
	"github.com/MichaelKinsy/pigpen/pig-music/ui"
)

// TestDrawIsViewWithParts: Draw draws exactly View's text, and its parts cover the screen's rows: a stage's columns are its
// rows cut at their widths, the cover's rows are the picture with its file, the bar knows the position.
func TestDrawIsViewWithParts(t *testing.T) {
	dump := map[string][]string{}
	for _, s := range screens.All() {
		view := s.Model.View().Content
		text, parts := s.Model.Draw()
		if text != view {
			t.Fatalf("%s: Draw's text is not View's", s.Name)
		}
		rows := strings.Split(text, "\n")
		dump[s.Name] = rows
		if got := partRows(parts); got != s.H || len(rows) != s.H {
			t.Fatalf("%s: parts cover %d rows, the screen has %d (%d drawn)", s.Name, got, s.H, len(rows))
		}
		kinds := map[ui.PartKind]int{}
		row := 0
		for _, p := range parts {
			kinds[p.Kind]++
			switch p.Kind {
			case ui.PartBar:
				idle := strings.HasPrefix(s.Name, "search-idle")
				if idle != (p.Duration == 0) {
					t.Errorf("%s: the bar's duration is %v", s.Name, p.Duration)
				}
				if want := clock(p.Position); !strings.Contains(ansi.Strip(rows[row]), want) {
					t.Errorf("%s: the bar's row %q lacks %s", s.Name, ansi.Strip(rows[row]), want)
				}
			case ui.PartStage:
				checkStage(t, s, p, rows[row:row+p.Rows], kinds)
			case ui.PartTabs:
				plain := ansi.Strip(rows[row])
				var names []string
				for _, tab := range p.Tabs {
					names = append(names, ansi.Cut(plain, tab.From, tab.To))
				}
				// the row's margin is its first tab's: a frontend draws them as one node, and a click on it names the margin
				want := map[int]string{1: "  Search | Library | Player ", 4: " 1 Songs 24|2 Albums 1|3 Artists 1"}[row]
				switch {
				case strings.HasPrefix(s.Name, "search-home") && row == 5: // the Recents' heading, with its clear key
					want = "C clear"
				case lyricsSwitch(s) && row > 3: // the panel under the track, below 80 cells
					want = " Lyrics | Up Next L "
				}
				if got := strings.Join(names, "|"); got != want || row == 4 && !strings.HasPrefix(s.Name, "search-kinds") {
					t.Errorf("%s: row %d's tabs hold %q", s.Name, row, names)
				}
			}
			row += p.Rows
		}
		if strings.HasPrefix(s.Name, "player-cover") && s.W >= 50 && kinds[ui.PartCover] != 1 {
			t.Errorf("%s: %d covers", s.Name, kinds[ui.PartCover])
		}
		if strings.Contains(s.Name, "lyrics") && kinds[ui.PartLyric] != 1 {
			t.Errorf("%s: %d sung lines", s.Name, kinds[ui.PartLyric])
		}
		if strings.HasPrefix(s.Name, "search-results") && kinds[ui.PartList] != 1 {
			t.Errorf("%s: %d lists", s.Name, kinds[ui.PartList])
		}
		if s.W >= 50 && strings.HasPrefix(s.Name, "player") && kinds[ui.PartStage] != 1 {
			t.Errorf("%s: %d stages", s.Name, kinds[ui.PartStage])
		}
		tabRows := 0
		if s.H >= 6 { // the full layout has its tab bar, the strip none
			tabRows = 1
		}
		if strings.HasPrefix(s.Name, "search-kinds") || strings.HasPrefix(s.Name, "search-home") || lyricsSwitch(s) {
			tabRows++
		}
		if kinds[ui.PartTabs] != tabRows {
			t.Errorf("%s: %d rows of tabs, want %d", s.Name, kinds[ui.PartTabs], tabRows)
		}
	}
	if out := os.Getenv("PIG_MUSIC_SCREENS_OUT"); out != "" {
		b, err := json.MarshalIndent(dump, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// lyricsSwitch reports whether s has the row of tabs that switches the panel between the lyrics and the queue: a Player screen
// with lyrics, below 132 cells.
func lyricsSwitch(s screens.Screen) bool {
	return strings.HasPrefix(s.Name, "player") && strings.Contains(s.Name, "lyrics") && s.W < 132 && s.H >= 6
}

func clock(d time.Duration) string {
	s := int(d / time.Second)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func partRows(parts []ui.Part) int {
	n := 0
	for _, p := range parts {
		n += p.Rows
	}
	return n
}

func checkStage(t *testing.T, s screens.Screen, p ui.Part, rows []string, kinds map[ui.PartKind]int) {
	t.Helper()
	width := 0
	for _, c := range p.Columns {
		width += c.Width
		if len(c.Lines) != p.Rows || partRows(c.Parts) != p.Rows {
			t.Fatalf("%s: a column of %d rows has %d lines and parts of %d", s.Name, p.Rows, len(c.Lines), partRows(c.Parts))
		}
		for _, l := range c.Lines {
			if ansi.StringWidth(l) != c.Width {
				t.Fatalf("%s: a column %d wide has a row of %d cells: %q", s.Name, c.Width, ansi.StringWidth(l), l)
			}
		}
		at := 0
		for _, cp := range c.Parts {
			kinds[cp.Kind]++
			if cp.Kind == ui.PartCover {
				// a scene with art shows the cover (half blocks, shades in monochrome) with its file; one without, the disc
				withArt := !strings.Contains(s.Name, "plain")
				if withArt != (cp.Cover != nil) || withArt && (!bytes.Equal(cp.Cover.Data, screens.CoverPNG) || cp.Cover.MimeType != "image/png") {
					t.Errorf("%s: the cover part's file is %v", s.Name, cp.Cover != nil)
				}
				for _, l := range c.Lines[at : at+cp.Rows] {
					if withArt && !strings.ContainsAny(l, "▀░▒▓█") || !withArt && !strings.ContainsFunc(l, func(r rune) bool { return r >= 0x2800 && r <= 0x28ff }) {
						t.Errorf("%s: a cover row without the picture: %q", s.Name, ansi.Strip(l))
					}
				}
			}
			if cp.Kind == ui.PartTabs {
				var names []string
				for _, tab := range cp.Tabs {
					names = append(names, ansi.Cut(ansi.Strip(c.Lines[at]), tab.From, tab.To))
				}
				if got := strings.Join(names, "|"); got != " Lyrics | Up Next L " || !lyricsSwitch(s) {
					t.Errorf("%s: the stage's tabs hold %q", s.Name, names)
				}
			}
			at += cp.Rows
		}
	}
	if width != s.W {
		t.Fatalf("%s: the columns are %d cells, the screen %d", s.Name, width, s.W)
	}
	for i, r := range rows {
		var b strings.Builder
		for _, c := range p.Columns {
			b.WriteString(ansi.Strip(c.Lines[i]))
		}
		if b.String() != ansi.Strip(r) {
			t.Fatalf("%s: row %d of the stage is not its columns:\n%q\n%q", s.Name, i, ansi.Strip(r), b.String())
		}
	}
}
