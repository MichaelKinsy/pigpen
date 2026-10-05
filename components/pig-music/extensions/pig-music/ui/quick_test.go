package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// The quick settings overlay (/music settings): a status item cannot be interactive, so this small screen gives the basic
// settings without opening the player.

type stubQuickPort struct {
	vals  QuickValues
	sets  [][2]string
	err   error
	notes map[string]string
}

func (p *stubQuickPort) Get() (QuickValues, error) { return p.vals, nil }
func (p *stubQuickPort) Set(key, value string) (string, error) {
	if p.err != nil {
		return "", p.err
	}
	p.sets = append(p.sets, [2]string{key, value})
	switch key {
	case "engine":
		p.vals.Engine = value
	case "cookieBrowser":
		p.vals.CookieBrowser = value
	case "nowPlaying":
		p.vals.NowPlaying = value
	case "coverArt":
		p.vals.CoverArt = value == "true"
	case "palette":
		p.vals.Palette = value == "true"
	case "pulse":
		p.vals.Pulse = value == "true"
	case "calm":
		p.vals.Calm = value == "true"
	case "lowPower":
		p.vals.LowPower = value == "true"
	}
	return p.notes[key], nil
}

type quickRig struct {
	t      *testing.T
	m      tea.Model
	player *stubPlayer
	port   *stubQuickPort
	quit   bool
}

func newQuickRig(t *testing.T, st music.State) *quickRig {
	t.Helper()
	r := &quickRig{t: t, player: &stubPlayer{state: st}, port: &stubQuickPort{
		vals:  QuickValues{Engine: "auto", NowPlaying: "auto", CoverArt: true, Palette: true, Pulse: true},
		notes: map[string]string{"engine": "takes effect after /reload"},
	}}
	r.m = NewQuick(QuickDeps{Player: r.player, Port: r.port})
	r.send(tea.WindowSizeMsg{Width: 70, Height: 20})
	return r
}

func (r *quickRig) send(msg tea.Msg) {
	r.t.Helper()
	m, cmd := r.m.Update(msg)
	r.m = m
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			r.quit = true
		}
	}
}

func (r *quickRig) key(names ...string) {
	for _, n := range names {
		r.send(tea.KeyPressMsg(keyOf(n)))
	}
}

func (r *quickRig) text() string { return ansi.Strip(r.m.View().Content) }

func quickPlaying() music.State {
	st := playing(tracks(3), 0)
	st.Volume, st.Shuffle, st.Repeat = 40, false, music.RepeatOff
	return st
}

func TestQuickSettingsShowsWhatIsPlayingAndEveryBasicSetting(t *testing.T) {
	r := newQuickRig(t, quickPlaying())
	txt := r.text()
	for _, want := range []string{"Song 1", "Artist", "Playing", "Volume", "40", "Shuffle", "off", "Repeat", "Engine", "auto", "Library browser", "system default", "Now playing line", "Cover art", "Palette", "Pulse", "Calm", "Low power", "on"} {
		if !strings.Contains(txt, want) {
			t.Errorf("lacks %q:\n%s", want, txt)
		}
	}
}

func TestVolumeMovesByFiveAndStaysBetweenZeroAndAHundred(t *testing.T) {
	r := newQuickRig(t, quickPlaying())
	r.key("right")
	r.key("left", "left")
	// the stub keeps its state fixed at 40, so each press is 40 plus or minus 5
	if got := strings.Join(r.player.log(), ","); got != "volume 45,volume 35,volume 35" {
		t.Errorf("%s", got)
	}
	st := quickPlaying()
	st.Volume = 98
	r2 := newQuickRig(t, st)
	r2.key("right")
	if got := r2.player.log(); len(got) != 1 || got[0] != "volume 100" {
		t.Errorf("%v", got)
	}
	st.Volume = 2
	r3 := newQuickRig(t, st)
	r3.key("left")
	if got := r3.player.log(); len(got) != 1 || got[0] != "volume 0" {
		t.Errorf("%v", got)
	}
}

func TestShuffleAndRepeatAreChangedFromTheirRows(t *testing.T) {
	r := newQuickRig(t, quickPlaying())
	r.key("down", "space") // Shuffle on
	r.key("down", "right") // Repeat: off -> all
	if got := strings.Join(r.player.log(), ","); got != `shuffle true,repeat "all"` {
		t.Errorf("%s", got)
	}
	st := quickPlaying()
	st.Repeat = music.RepeatAll
	r2 := newQuickRig(t, st)
	r2.key("down", "down", "right")
	r2.key("left")
	if got := strings.Join(r2.player.log(), ","); got != `repeat "one",repeat ""` {
		t.Errorf("all -> one with right, then back to off with left:\n%s", got)
	}
}

func TestEngineBrowserLineAndCoverArtAreSavedAtOnceWithTheirNote(t *testing.T) {
	r := newQuickRig(t, quickPlaying())
	r.key("down", "down", "down") // Engine
	r.key("right")                // auto -> mpv
	if txt := r.text(); !strings.Contains(txt, "mpv") || !strings.Contains(txt, "takes effect after /reload") {
		t.Errorf("%s", txt)
	}
	r.key("right", "right") // native, then back to auto
	r.key("down", "right")  // Library browser: system default -> chrome
	r.key("down", "right")  // Now playing line: auto -> footer
	r.key("down", "space")  // Cover art on -> off
	want := "engine=mpv engine=native engine=auto cookieBrowser=chrome nowPlaying=footer coverArt=false"
	var got []string
	for _, s := range r.port.sets {
		got = append(got, s[0]+"="+s[1])
	}
	if strings.Join(got, " ") != want {
		t.Errorf("saved %v\nwant %s", got, want)
	}
}

func TestTheBrowserListEndsWhereItBeganAndLeftGoesBackwards(t *testing.T) {
	r := newQuickRig(t, quickPlaying())
	r.key("down", "down", "down", "down") // Library browser
	r.key("left")                         // system default -> the last browser in the list
	if len(r.port.sets) != 1 || r.port.sets[0][1] == "" || r.port.sets[0][1] == "chrome" {
		t.Fatalf("%v", r.port.sets)
	}
	r.key("right") // and forward again to the default (an empty value removes the setting)
	if r.port.sets[1] != [2]string{"cookieBrowser", ""} {
		t.Errorf("%v", r.port.sets)
	}
}

func TestPlayerRowsDoNothingWhileNothingIsPlayingAndSaySo(t *testing.T) {
	r := newQuickRig(t, music.State{Index: -1})
	r.key("right", "down", "space", "down", "right")
	if got := r.player.log(); len(got) != 0 {
		t.Errorf("the player was driven with nothing playing: %v", got)
	}
	if !strings.Contains(r.text(), "not playing") {
		t.Errorf("%s", r.text())
	}
	// the other rows still work
	r.key("down", "right")
	if len(r.port.sets) != 1 || r.port.sets[0][0] != "engine" {
		t.Errorf("%v", r.port.sets)
	}
}

func TestAFailureToSaveIsShownAndTheValueDoesNotChange(t *testing.T) {
	r := newQuickRig(t, quickPlaying())
	r.port.err = errors.New("settings.json is not a JSON object")
	r.key("down", "down", "down", "right")
	txt := r.text()
	if !strings.Contains(txt, "settings.json is not a JSON object") {
		t.Errorf("%s", txt)
	}
	if strings.Contains(txt, "Engine") && strings.Contains(strings.SplitN(txt[strings.Index(txt, "Engine"):], "\n", 2)[0], "mpv") {
		t.Errorf("the value changed although saving failed:\n%s", txt)
	}
}

func TestAPlayerErrorIsShown(t *testing.T) {
	r := newQuickRig(t, quickPlaying())
	r.player.err = errors.New("mpv is gone")
	r.key("right")
	if !strings.Contains(r.text(), "mpv is gone") {
		t.Errorf("%s", r.text())
	}
}

func TestEscAndQCloseTheOverlay(t *testing.T) {
	for _, k := range []string{"esc", "q"} {
		r := newQuickRig(t, quickPlaying())
		r.key(k)
		if !r.quit {
			t.Errorf("%s did not close it", k)
		}
	}
}

func TestQuickSettingsFitsEverySizeExactlyAndCleansTitles(t *testing.T) {
	st := quickPlaying()
	bad := *st.Track
	bad.Title = "Evil\x1b]0;x\x07 Title\nline"
	st.Track = &bad
	for _, sz := range [][2]int{{80, 24}, {60, 14}, {40, 12}, {30, 10}, {20, 6}, {10, 3}, {1, 1}} {
		r := newQuickRig(t, st)
		r.send(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		raw := r.m.View().Content
		lines := strings.Split(raw, "\n")
		if len(lines) != sz[1] {
			t.Errorf("%v: %d lines", sz, len(lines))
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w != sz[0] {
				t.Errorf("%v line %d is %d wide", sz, i, w)
				break
			}
		}
		if strings.Contains(raw, "\x07") || strings.Contains(raw, "\x1b]") {
			t.Errorf("%v: a control sequence from the title got through: %q", sz, raw)
		}
	}
}

func TestSelectionMovesWithJKAndStopsAtTheEnds(t *testing.T) {
	r := newQuickRig(t, quickPlaying())
	r.key("k", "k") // above the first row: stays
	r.key("right")
	if got := r.player.log(); len(got) != 1 || !strings.HasPrefix(got[0], "volume") {
		t.Errorf("%v", got)
	}
	for i := 0; i < 20; i++ {
		r.key("j")
	}
	r.key("space") // the last row: low power
	if len(r.port.sets) != 1 || r.port.sets[0] != [2]string{"lowPower", "true"} {
		t.Errorf("%v", r.port.sets)
	}
}

func TestTheLookAndMotionSwitchesAreSavedAtOnceWithNoNote(t *testing.T) {
	r := newQuickRig(t, quickPlaying())
	for i := 0; i < 6; i++ {
		r.key("down") // Cover art
	}
	r.key("down", "space") // Palette: on -> off
	r.key("down", "space") // Pulse: on -> off
	r.key("down", "space") // Calm: off -> on
	r.key("down", "space") // Low power: off -> on
	var got []string
	for _, s := range r.port.sets {
		got = append(got, s[0]+"="+s[1])
	}
	if want := "palette=false pulse=false calm=true lowPower=true"; strings.Join(got, " ") != want {
		t.Errorf("saved %v, want %s", got, want)
	}
	if strings.Contains(r.text(), "takes effect after") {
		t.Errorf("these apply at once, with no note:\n%s", r.text())
	}
	txt := r.text()
	for _, want := range []string{"Palette", "Pulse", "Calm", "Low power"} {
		if !strings.Contains(txt, want) {
			t.Errorf("lacks %q", want)
		}
	}
}
