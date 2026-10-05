package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func TestSTogglesShuffleFromTheState(t *testing.T) {
	r := newRig(t, 100, 30)
	st := playing(tracks(4), 0)
	r.state(st)
	r.key("s")
	st.Shuffle = true
	r.state(st)
	r.key("s")
	if log := r.player.log(); len(log) != 2 || log[0] != "shuffle true" || log[1] != "shuffle false" {
		t.Errorf("%v", log)
	}
}

func TestLCyclesRepeatOffAllOneOff(t *testing.T) {
	r := newRig(t, 100, 30)
	st := playing(tracks(4), 0)
	for _, mode := range []music.RepeatMode{music.RepeatOff, music.RepeatAll, music.RepeatOne} {
		st.Repeat = mode
		r.state(st)
		r.key("l")
	}
	want := []string{`repeat "all"`, `repeat "one"`, `repeat ""`}
	if log := r.player.log(); strings.Join(log, "|") != strings.Join(want, "|") {
		t.Errorf("%v, want %v", log, want)
	}
}

func TestThePlayerScreenAndTheHelpShowTheModes(t *testing.T) {
	r := newRig(t, 100, 30)
	st := playing(tracks(3), 0)
	st.Shuffle, st.Repeat = true, music.RepeatOne
	r.state(st)
	r.key("tab", "tab")
	txt := r.text()
	for _, want := range []string{"Shuffle: on", "Repeat: one"} {
		if !strings.Contains(txt, want) {
			t.Errorf("lacks %q:\n%s", want, txt)
		}
	}
	st.Shuffle, st.Repeat = false, music.RepeatOff
	r.state(st)
	if txt := r.text(); !strings.Contains(txt, "Shuffle: off") || !strings.Contains(txt, "Repeat: off") {
		t.Errorf("\n%s", txt)
	}
	r.key("?")
	if txt := r.text(); !strings.Contains(txt, "shuffle") || !strings.Contains(txt, "repeat") {
		t.Errorf("help lacks the mode keys:\n%s", txt)
	}
}

// noModes is a Player that has no shuffle or repeat (an embedded interface hides the extra methods).
type noModes struct{ music.Player }

func TestAPlayerWithoutModesSaysSoAndShowsNothingAboutThem(t *testing.T) {
	r := newRig(t, 100, 30)
	r.m = New(Deps{Source: r.source, Player: noModes{r.player}})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	r.state(playing(tracks(3), 0))
	r.key("s")
	if !strings.Contains(r.text(), "not available") {
		t.Errorf("no word that it is unavailable:\n%s", r.text())
	}
	r.key("tab", "tab")
	if strings.Contains(r.text(), "Shuffle:") || strings.Contains(r.text(), "Repeat:") {
		t.Errorf("modes shown for a player that has none:\n%s", r.text())
	}
	if len(r.player.log()) != 0 {
		t.Errorf("%v", r.player.log())
	}
}

func TestAModeErrorIsShown(t *testing.T) {
	r := newRig(t, 100, 30)
	r.player.err = errors.New("turning shuffle off needs mpv 0.37 or later")
	r.state(playing(tracks(3), 0))
	r.key("s")
	if !strings.Contains(r.text(), "needs mpv 0.37") {
		t.Errorf("\n%s", r.text())
	}
}
