package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func TestTheAlbumColumnDropsFirstThenTheArtistAndTheTitleNeverVanishes(t *testing.T) {
	cases := []struct {
		avail                 int
		wantAlbum, wantArtist bool
		minTitle              int
	}{
		{200, true, true, 40},
		{120, true, true, 30},
		{100, true, true, 24},
		{80, false, true, 24},
		{60, false, true, 20},
		{40, false, false, 20},
		{24, false, false, 12},
	}
	for _, c := range cases {
		p := columnPlan(c.avail, true, false)
		if (p.album > 0) != c.wantAlbum || (p.artist > 0) != c.wantArtist || p.title < c.minTitle {
			t.Errorf("avail %d: %+v, want album %v artist %v title >= %d", c.avail, p, c.wantAlbum, c.wantArtist, c.minTitle)
		}
		used := 12 + p.title
		if p.artist > 0 {
			used += 1 + p.artist
		}
		if p.album > 0 {
			used += 1 + p.album
		}
		if used > c.avail {
			t.Errorf("avail %d: the columns need %d", c.avail, used)
		}
	}
}

func TestNoAlbumColumnWhenNoTrackHasAnAlbum(t *testing.T) {
	if p := columnPlan(200, false, false); p.album != 0 {
		t.Errorf("%+v", p)
	}
}

func TestTheExplicitBadgeTakesTwoCellsFromTheTitleOnlyWhenSomeTrackIsExplicit(t *testing.T) {
	a, b := columnPlan(100, true, false), columnPlan(100, true, true)
	if !b.explicit || a.explicit || b.title != a.title-2 {
		t.Errorf("%+v %+v", a, b)
	}
}

func richTracks() []music.Track {
	return []music.Track{
		{ID: "aaaaaaaaaaa", Title: "Lose Yourself", Artists: []string{"Eminem"}, Album: "8 Mile", Duration: 321 * time.Second, Explicit: true},
		{ID: "bbbbbbbbbbb", Title: "Clean Song", Artists: []string{"Someone"}, Album: "Wholesome Album", Duration: 200 * time.Second},
	}
}

func richRig(t *testing.T, w int) *rig {
	r := newRig(t, w, 24)
	r.source.tracks = richTracks()
	r.key("/")
	r.typed("x")
	r.key("enter")
	return r
}

func TestAWideScreenShowsTheAlbumColumnAndTheExplicitBadge(t *testing.T) {
	text := richRig(t, 140).text()
	for _, want := range []string{"Album", "8 Mile", "Wholesome Album", "Lose Yourself", "Eminem", "5:21"} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "Lose Yourself") && !strings.Contains(line, " E ") {
			t.Errorf("no badge on the explicit row: %q", line)
		}
		if strings.Contains(line, "Clean Song") && strings.Contains(line, " E ") {
			t.Errorf("a badge on a clean row: %q", line)
		}
	}
}

func TestANarrowScreenDropsTheAlbumButKeepsTitleArtistAndLength(t *testing.T) {
	text := richRig(t, 60).text()
	if strings.Contains(text, "8 Mile") || strings.Contains(text, "Album") {
		t.Errorf("album still shown:\n%s", text)
	}
	for _, want := range []string{"Lose Yourself", "Eminem", "5:21"} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
}

func TestEveryRowFitsTheWidthAtEverySize(t *testing.T) {
	for _, w := range []int{20, 30, 40, 60, 80, 100, 140, 220} {
		r := richRig(t, w)
		for _, line := range r.screen() {
			if got := ansi.StringWidth(line); got > w {
				t.Errorf("width %d: a line is %d cells: %q", w, got, line)
			}
		}
	}
}
