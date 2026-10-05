package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Review of M10b (rich listings).

// A wide character cut at a column's edge must not pull the next columns one cell to the left: the badge, the artist and the
// length stay under their headings whatever script the title is in.
func TestReviewM10bAWideTitleCutAtItsEdgeKeepsTheBadgeAligned(t *testing.T) {
	cjk := strings.Repeat("東京は夜の七時", 12)
	for _, w := range []int{40, 41, 50, 51, 80, 81, 90, 91, 120, 121, 160, 161} {
		for _, title := range []string{cjk, "x" + cjk} { // both parities of where the cut falls
			ts := []music.Track{
				{ID: "aaaaaaaaaaa", Title: title, Artists: []string{"ピチカート・ファイヴ"}, Album: "オーヴァードーズ", Duration: 200 * time.Second, Explicit: true},
				{ID: "bbbbbbbbbbb", Title: "Lose Yourself", Artists: []string{"Eminem"}, Album: "8 Mile", Duration: 321 * time.Second, Explicit: true},
			}
			m := sized(w, 20)
			rows := m.trackTable(ts, -1, -1, 10)
			at := func(row string) int {
				s := ansi.Strip(row)
				i := strings.Index(s, " E ")
				if i < 0 {
					t.Fatalf("width %d: no badge in %q", w, s)
				}
				return ansi.StringWidth(s[:i])
			}
			if a, b := at(rows[1]), at(rows[2]); a != b {
				t.Errorf("width %d: the badge is at cell %d on the wide row and %d on the narrow one:\n%s\n%s", w, a, b, ansi.Strip(rows[1]), ansi.Strip(rows[2]))
			}
			if got := ansi.StringWidth(rows[1]); got != w {
				t.Errorf("width %d: the wide row is %d cells", w, got)
			}
		}
	}
}
