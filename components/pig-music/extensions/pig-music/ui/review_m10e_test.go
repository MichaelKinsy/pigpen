package ui

import (
	"fmt"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func init() {
	// the rig runs commands inline: a real timer would hold every test that toasts for two seconds
	toastTick = func(time.Duration) tea.Cmd { return func() tea.Msg { return nil } }
}

// A confirmation goes away by itself, also when nothing else redraws the screen (paused: no position reports, no animation).
func TestReviewAToastGoesAwayWhilePaused(t *testing.T) {
	var asked []time.Duration
	old := toastTick
	toastTick = func(d time.Duration) tea.Cmd { asked = append(asked, d); return func() tea.Msg { return nil } }
	t.Cleanup(func() { toastTick = old })
	ck := &fakeClock{t: time.Unix(1000, 0)}
	r := newRig(t, 100, 30)
	r.m = New(Deps{Source: r.source, Player: r.player, Now: ck.now})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	st := playing(tracks(3), 0)
	st.Paused = true
	r.state(st)
	r.key("+")
	if len(asked) != 1 || asked[0] != toastFor {
		t.Fatalf("no timer for the toast: %v", asked)
	}
	r.key("+") // a second toast within the first one's time: still one timer
	if len(asked) != 1 {
		t.Fatalf("timers %v", asked)
	}
	ck.t = ck.t.Add(toastFor)
	r.send(toastDoneMsg{})
	if strings.Contains(r.text(), "Volume") {
		t.Errorf("the toast stayed:\n%s", r.text())
	}
}

// The key line fits its width by leaving out the less needed keys, never "? help" and "q hide" (at 80 columns they fell off the
// end of the Player screen's line, and a page's line never named q).
func TestReviewTheKeyLineAlwaysKeepsHelpAndHide(t *testing.T) {
	for _, w := range []int{60, 80, 100, 200} {
		r := stageRig(t, w, 24)
		lines := strings.Split(r.text(), "\n")
		last := lines[len(lines)-1]
		for _, want := range []string{"? help", "q hide", "space play"} {
			if !strings.Contains(last, want) {
				t.Errorf("%d wide, Player: no %q in %q", w, want, last)
			}
		}
	}
	r, _ := navRig(t)
	r.key("A")
	lines := strings.Split(r.text(), "\n")
	if last := lines[len(lines)-1]; !strings.Contains(last, "backspace back") || !strings.Contains(last, "q hide") {
		t.Errorf("page: %q", last)
	}
}

// The "up next" card does not move the stage: the cover (or the disc) keeps its size when the card comes in the last seconds
// (at 80x24 the disc used to vanish, at 100x30 shrink by a third, for ten seconds of every track).
func TestReviewTheUpNextCardKeepsTheCoversSize(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {100, 30}, {120, 40}} {
		r := newRig(t, size[0], size[1])
		r.send(ShowPlayerMsg{})
		st := playing(tracks(3), 0)
		st.Duration, st.Position = 200*time.Second, 150*time.Second
		r.state(st)
		before := r.model().coverRows()
		st.Position = 193 * time.Second
		r.state(st)
		if !strings.Contains(r.text(), "Up next in") {
			t.Fatalf("%v: no card:\n%s", size, r.text())
		}
		if after := r.model().coverRows(); after != before || before == 0 {
			t.Errorf("%v: cover %d rows, %d with the card", size, before, after)
		}
	}
}

// The line being sung sits at a fixed height of the lyrics panel (a third of the way down), from the first line to the last, the
// way Apple Music and YouTube Music hold it: not at the top of the panel at the start, and the sheet scrolls under it to the end.
func TestReviewTheCurrentLyricLineIsHeldAtAThirdOfThePanel(t *testing.T) {
	var lines []music.LyricLine
	for i := 0; i < 30; i++ {
		lines = append(lines, music.LyricLine{At: sec(float64(4 + 4*i)), End: sec(float64(8 + 4*i)), Text: fmt.Sprintf("lyric line number %02d", i)})
	}
	lp := &stubLyrics{byID: map[string]music.Lyrics{"id00": {Synced: true, Lines: lines}}}
	r, _ := lyricRig(t, lp, func(d *Deps) { d.Calm = true }) // calm: the view jumps, no easing to wait for
	r.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	r.send(ShowPlayerMsg{})
	rowOf := func(want string) int {
		for i, l := range strings.Split(r.text(), "\n") {
			if strings.Contains(l, want) {
				return i
			}
		}
		return -1
	}
	var rows []int
	for _, c := range []struct {
		pos  float64
		line string
	}{{5, "number 00"}, {50, "number 11"}, {121, "number 29"}} {
		st := at(tracks(3), 0, sec(c.pos))
		st.Duration = sec(130)
		r.state(st)
		rows = append(rows, rowOf(c.line))
	}
	if rows[0] < 8 || rows[0] != rows[1] || rows[1] != rows[2] {
		t.Errorf("the current line moved on the screen: rows %v\n%s", rows, r.text())
	}
}
