package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Review of M10b (rich listings).

func sized(w, h int) Model {
	var m tea.Model = New(Deps{Source: &stubSource{}, Player: &stubPlayer{}})
	m, _ = m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m.(Model)
}

// The table must be exactly as wide as its room: one cell more and the screen's own clipping takes the last cell of the
// length ("12:34" read "12:3"; the cursor row was clipped the same way by its own pad).
func TestReviewM10bEveryTableRowIsExactlyItsWidthAndKeepsTheWholeLength(t *testing.T) {
	ts := []music.Track{
		{ID: "aaaaaaaaaaa", Title: "Giorgio by Moroder", Artists: []string{"Daft Punk"}, Album: "Random Access Memories", Duration: 545 * time.Second},
		{ID: "bbbbbbbbbbb", Title: "Touch (feat. Paul Williams)", Artists: []string{"Daft Punk"}, Album: "Random Access Memories", Duration: 754 * time.Second, Explicit: true},
		{ID: "ccccccccccc", Title: "Contact", Artists: []string{"Daft Punk"}, Duration: 621 * time.Second},
	}
	for _, w := range []int{24, 40, 49, 50, 59, 60, 80, 89, 90, 120, 160, 220} {
		m := sized(w, 20)
		rows := m.trackTable(ts, 1, -1, 10)
		for i, row := range rows {
			if got := ansi.StringWidth(row); got != w {
				t.Errorf("width %d: row %d is %d cells: %q", w, i, got, ansi.Strip(row))
			}
		}
		text := ansi.Strip(strings.Join(rows, "\n"))
		for _, want := range []string{"9:05", "12:34", "10:21"} {
			if !strings.Contains(text, want) {
				t.Errorf("width %d: the length %s is clipped:\n%s", w, want, text)
			}
		}
	}
}

// The breakpoints, pinned at their edges so that moving one is seen.
func TestReviewM10bTheBreakpointsAreAtNinetyAndFifty(t *testing.T) {
	if p := columnPlan(89, true, false); p.album != 0 {
		t.Errorf("89: %+v", p)
	}
	if p := columnPlan(90, true, false); p.album == 0 {
		t.Errorf("90: %+v", p)
	}
	if p := columnPlan(49, true, false); p.artist != 0 {
		t.Errorf("49: %+v", p)
	}
	if p := columnPlan(50, true, false); p.artist == 0 || p.album != 0 {
		t.Errorf("50: %+v", p)
	}
	for avail := 24; avail <= 400; avail++ {
		for _, ex := range []bool{false, true} {
			p := columnPlan(avail, true, ex)
			used := 12 + p.title
			if p.artist > 0 {
				used += 1 + p.artist
			}
			if p.album > 0 {
				used += 1 + p.album
			}
			if p.explicit {
				used += 2
			}
			if used != avail || p.title < 1 {
				t.Fatalf("avail %d explicit %v: %+v uses %d", avail, ex, p, used)
			}
		}
	}
}
