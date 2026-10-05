package ui

import (
	"errors"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type fakeSettings struct {
	mu   sync.Mutex
	vals map[string]bool
	sets []string
	err  error
	info []string
}

func (f *fakeSettings) Get() (bool, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.vals["stopOnExit"], f.vals["footerStatus"], nil
}

func (f *fakeSettings) Set(key string, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.vals[key] = on
	f.sets = append(f.sets, key+"="+map[bool]string{true: "on", false: "off"}[on])
	return nil
}

func (f *fakeSettings) Info() []string { return f.info }

func settingsRig(t *testing.T, f *fakeSettings) *rig {
	t.Helper()
	r := newRig(t, 100, 30)
	r.m = New(Deps{Source: r.source, Player: r.player, Settings: f})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	return r
}

func newFake() *fakeSettings {
	return &fakeSettings{vals: map[string]bool{"stopOnExit": true, "footerStatus": true}, info: []string{"mpv 0.37.0 (/usr/bin/mpv)", "browser for the library: chrome", "bad\x1b]0;x\x07 line"}}
}

func TestOOpensTheSettingsScreenWithTheTogglesAndTheInfo(t *testing.T) {
	r := settingsRig(t, newFake())
	r.key("o")
	txt := r.text()
	for _, want := range []string{"Settings", "[x] Stop the music when PiG quits", "[x] Show the track in PiG's footer", "mpv 0.37.0", "browser for the library: chrome", "esc"} {
		if !strings.Contains(txt, want) {
			t.Errorf("lacks %q:\n%s", want, txt)
		}
	}
	if strings.ContainsAny(r.m.View().Content, "\x07") || strings.Contains(r.m.View().Content, "\x1b]") {
		t.Error("control characters from the info lines reached the screen")
	}
}

func TestSpaceTogglesTheSelectedSettingAndSavesAtOnce(t *testing.T) {
	f := newFake()
	r := settingsRig(t, f)
	r.key("o", "space")
	r.key("down", "enter")
	if strings.Join(f.sets, ",") != "stopOnExit=off,footerStatus=off" {
		t.Fatalf("saved %v", f.sets)
	}
	txt := r.text()
	if !strings.Contains(txt, "[ ] Stop the music") || !strings.Contains(txt, "[ ] Show the track") {
		t.Errorf("\n%s", txt)
	}
	r.key("space") // footerStatus back on
	if f.vals["footerStatus"] != true || !strings.Contains(r.text(), "[x] Show the track") {
		t.Errorf("%v\n%s", f.vals, r.text())
	}
}

func TestEscAndOCloseItAndQHidesThePlayerAndOtherKeysDoNothingInside(t *testing.T) {
	f := newFake()
	r := settingsRig(t, f)
	r.key("o", "n", "p", "s", "tab", "/")
	if len(r.player.log()) != 0 || r.m.(Model).Typing() || r.m.(Model).Tab() != "search" {
		t.Errorf("keys leaked out of the settings screen: %v tab %s", r.player.log(), r.m.(Model).Tab())
	}
	r.key("esc")
	if r.quit || strings.Contains(r.text(), "Settings") {
		t.Errorf("esc did not just close the settings:\n%s", r.text())
	}
	r.key("o", "o")
	if strings.Contains(r.text(), "Settings") {
		t.Error("o did not close it")
	}
	r.key("o", "q")
	if !r.quit {
		t.Error("q did not hide the player")
	}
}

func TestASaveFailureIsShownAndTheSwitchIsNotShownChanged(t *testing.T) {
	f := newFake()
	f.err = errors.New("settings.json: invalid character; fix or remove it, nothing was changed")
	r := settingsRig(t, f)
	r.key("o", "space")
	txt := r.text()
	if !strings.Contains(txt, "nothing was changed") || !strings.Contains(txt, "[x] Stop the music") {
		t.Errorf("\n%s", txt)
	}
}

func TestWithoutASettingsFileTheScreenSaysSo(t *testing.T) {
	r := newRig(t, 100, 30)
	r.key("o")
	if !strings.Contains(r.text(), "not available") || strings.Contains(r.text(), "Settings\n") && r.quit {
		t.Errorf("\n%s", r.text())
	}
}

func TestTheSettingsScreenFillsTheTerminalAtEverySize(t *testing.T) {
	for _, sz := range [][2]int{{120, 40}, {80, 24}, {60, 16}, {40, 12}, {30, 8}, {24, 6}, {20, 5}, {1, 1}} {
		r := newRig(t, sz[0], sz[1])
		r.m = New(Deps{Source: r.source, Player: r.player, Settings: newFake()})
		r.send(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		r.key("o")
		lines := r.screen()
		if len(lines) != sz[1] {
			t.Errorf("%v: %d lines", sz, len(lines))
		}
		for i, l := range lines {
			if ansi.StringWidth(l) > sz[0] {
				t.Errorf("%v: line %d is %d cells", sz, i, ansi.StringWidth(l))
			}
		}
	}
}
