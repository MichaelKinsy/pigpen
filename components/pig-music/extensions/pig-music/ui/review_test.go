package ui

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// sgr is the only escape sequence the view itself writes (lipgloss styles).
var sgr = regexp.MustCompile("\x1b\\[[0-9;:]*m")

// Titles, artists, albums and error text come from the network (yt-dlp, mpv's
// media-title, ICY metadata of a stream) and from pasted input. The host paints
// the lines it is given as they are, so a control character in them would reach
// the terminal: an OSC 52 clipboard write, a window title, a screen clear, or a
// newline that breaks the layout. Nothing but the view's own styles may remain.
func TestUntrustedTextCannotReachTheTerminalAsControls(t *testing.T) {
	evil := "Evil\x1b]52;c;cHduZWQ=\x07Song\x1b[2J\x1b]0;title\x1b\\\nsecond\rline\x9b31m\x7f\tend"
	ts := []music.Track{{ID: "a", Title: evil, Artists: []string{"Art\x1b[5mist\n2"}, Album: "Al\x1bbum\n"}, {ID: "b", Title: "plain"}}
	for _, tab := range []string{"search", "player", "library"} {
		r := newRig(t, 100, 30)
		r.source.tracks = ts
		r.source.err = errors.New("yt-dlp: ERROR: \x1b]0;x\x07bad\nthing")
		r.key("/")
		r.typed("pasted\nquery\x1b[2J")
		r.key("enter")
		cur := ts[0]
		r.state(music.State{Connected: true, Track: &cur, Queue: ts, Index: 0, Duration: time.Minute})
		switch tab {
		case "player":
			r.key("tab", "tab")
		case "library":
			r.key("tab")
		}
		lines := r.screen()
		if len(lines) != 30 {
			t.Errorf("%s: %d lines, want 30 (a newline from the data split one)", tab, len(lines))
		}
		for i, l := range lines {
			rest := sgr.ReplaceAllString(l, "")
			for _, c := range rest {
				if c < 0x20 || (c >= 0x7f && c < 0xa0) {
					t.Errorf("%s: line %d holds control %U: %q", tab, i, c, l)
					break
				}
			}
			if w := ansi.StringWidth(l); w > 100 {
				t.Errorf("%s: line %d is %d cells", tab, i, w)
			}
		}
	}
}

// After mpv went away (stopped from the command line, crashed, killed) the screen
// must say so, not go on showing the last track as playing.
func TestAStoppedMpvIsShownAsStopped(t *testing.T) {
	r := newRig(t, 100, 30)
	s := playing(tracks(3), 1)
	r.state(s)
	s.Connected = false
	r.state(s)
	head := ansi.Strip(r.screen()[0])
	if strings.Contains(head, "Playing") || !strings.Contains(head, "mpv is not running") {
		t.Fatalf("header after mpv went away: %q", head)
	}
}

// A slower earlier search must not replace the results of a later one.
func TestAnEarlierSearchFinishingLateIsIgnored(t *testing.T) {
	r := newRig(t, 100, 30)
	r.key("/")
	r.typed("second")
	r.key("enter")
	r.send(searchDoneMsg{query: "first", tracks: []music.Track{{ID: "x", Title: "From the first search"}}})
	if txt := r.text(); strings.Contains(txt, "From the first search") || !strings.Contains(txt, "Song 1") {
		t.Fatalf("screen:\n%s", txt)
	}
}

// mpv can report a position past the duration (a stream's estimate, the last
// frame); the bar must stay inside its line and the view must not fail.
func TestAPositionPastTheDurationKeepsTheBarInItsLine(t *testing.T) {
	r := newRig(t, 80, 20)
	s := playing(tracks(1), 0)
	s.Position, s.Duration = 130*time.Second, 100*time.Second
	r.state(s)
	lines := r.screen()
	bar := lines[len(lines)-3]
	if ansi.StringWidth(bar) != 80 || !strings.Contains(ansi.Strip(bar), "2:10") {
		t.Fatalf("play bar %q", ansi.Strip(bar))
	}
}

// A long list scrolls so that the selected row stays visible.
func TestTheSelectionStaysVisibleInALongList(t *testing.T) {
	r := newRig(t, 100, 14)
	r.state(playing(tracks(60), 0))
	r.key("tab", "tab")
	for i := 0; i < 40; i++ {
		r.key("down")
	}
	if txt := r.text(); !strings.Contains(txt, "Song 41") {
		t.Fatalf("the selected entry (41) is not on screen:\n%s", txt)
	}
}
