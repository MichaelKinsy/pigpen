package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// QuickValues are the settings the quick screen shows, as stored in settings.json.
type QuickValues struct {
	Engine        string // auto, mpv or native
	CookieBrowser string // empty: the system's default browser
	NowPlaying    string // auto, footer or widget
	CoverArt      bool
	// How the player looks and moves; they apply at once.
	Palette, Pulse, Calm, LowPower bool
}

// QuickPort is the quick screen's way to settings.json. Set takes the settings key and its value as text ("true" or
// "false" for the switches, "" to remove a key), saves it, applies what can be applied at once and returns a short note
// about the rest ("takes effect after /reload").
type QuickPort interface {
	Get() (QuickValues, error)
	Set(key, value string) (note string, err error)
}

// QuickDeps are what the quick settings screen works with.
type QuickDeps struct {
	Player music.Player
	Port   QuickPort
}

// The values each row cycles through.
var (
	quickEngines   = []string{"auto", "mpv", "native"}
	quickBrowsers  = []string{"", "chrome", "firefox", "safari", "edge", "brave", "chromium", "opera", "vivaldi"}
	quickNowPlaced = []string{"auto", "footer", "widget"}
	quickRepeats   = []music.RepeatMode{music.RepeatOff, music.RepeatAll, music.RepeatOne}
)

const (
	rowVolume = iota
	rowShuffle
	rowRepeat
	rowEngine
	rowBrowser
	rowNowPlaying
	rowCoverArt
	rowPalette
	rowPulse
	rowCalm
	rowLowPower
	rowCount
)

var quickLabels = [rowCount]string{"Volume", "Shuffle", "Repeat", "Engine", "Library browser", "Now playing line", "Cover art", "Palette", "Pulse", "Calm", "Low power"}

// QuickModel is /music settings: a small screen with the basic settings, for a status item that cannot take a key. The
// player's own settings (volume, shuffle, repeat) act on the music at once; the others are written to settings.json.
type QuickModel struct {
	deps QuickDeps
	vals QuickValues
	row  int
	note string
	w, h int
}

// NewQuick returns the quick settings screen.
func NewQuick(d QuickDeps) QuickModel {
	m := QuickModel{deps: d, vals: QuickValues{Engine: "auto", NowPlaying: "auto", CoverArt: true}}
	if d.Port != nil {
		if v, err := d.Port.Get(); err == nil {
			m.vals = v
		} else {
			m.note = err.Error()
		}
	}
	return m
}

// Init has nothing to start.
func (m QuickModel) Init() tea.Cmd { return nil }

// Update handles one message.
func (m QuickModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.KeyPressMsg:
		return m.key(msg.String())
	}
	return m, nil
}

func (m QuickModel) key(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc", "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.row = max(m.row-1, 0)
	case "down", "j":
		m.row = min(m.row+1, rowCount-1)
	case "right", "l", "+", "=", "space", " ", "enter":
		m.note = ""
		return m.change(+1)
	case "left", "h", "-":
		m.note = ""
		return m.change(-1)
	}
	return m, nil
}

// change moves the selected row's value one step, forward or back.
func (m QuickModel) change(dir int) (tea.Model, tea.Cmd) {
	switch m.row {
	case rowVolume, rowShuffle, rowRepeat:
		return m.player(dir)
	case rowEngine:
		return m.save("engine", cycle(quickEngines, m.vals.Engine, dir))
	case rowBrowser:
		return m.save("cookieBrowser", cycle(quickBrowsers, m.vals.CookieBrowser, dir))
	case rowNowPlaying:
		return m.save("nowPlaying", cycle(quickNowPlaced, orDefault(m.vals.NowPlaying, "auto"), dir))
	}
	toggle := func(key string, cur bool) (tea.Model, tea.Cmd) { return m.save(key, fmt.Sprint(!cur)) }
	switch m.row {
	case rowPalette:
		return toggle("palette", m.vals.Palette)
	case rowPulse:
		return toggle("pulse", m.vals.Pulse)
	case rowCalm:
		return toggle("calm", m.vals.Calm)
	case rowLowPower:
		return toggle("lowPower", m.vals.LowPower)
	}
	return toggle("coverArt", m.vals.CoverArt)
}

func cycle(list []string, cur string, dir int) string {
	at := 0
	for i, v := range list {
		if v == cur {
			at = i
		}
	}
	return list[((at+dir)%len(list)+len(list))%len(list)]
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// save writes one setting through the port and, only when that worked, takes the new value.
func (m QuickModel) save(key, value string) (tea.Model, tea.Cmd) {
	if m.deps.Port == nil {
		m.note = "settings are not available here"
		return m, nil
	}
	note, err := m.deps.Port.Set(key, value)
	if err != nil {
		m.note = err.Error()
		return m, nil
	}
	switch key {
	case "engine":
		m.vals.Engine = value
	case "cookieBrowser":
		m.vals.CookieBrowser = value
	case "nowPlaying":
		m.vals.NowPlaying = value
	case "coverArt":
		m.vals.CoverArt = value == "true"
	case "palette":
		m.vals.Palette = value == "true"
	case "pulse":
		m.vals.Pulse = value == "true"
	case "calm":
		m.vals.Calm = value == "true"
	case "lowPower":
		m.vals.LowPower = value == "true"
	}
	m.note = note
	return m, nil
}

// player acts on the music: volume, shuffle or repeat. With nothing playing there is nothing to act on.
func (m QuickModel) player(dir int) (tea.Model, tea.Cmd) {
	p := m.deps.Player
	st := music.State{Index: -1}
	if p != nil {
		st = p.State()
	}
	if p == nil || (!st.Connected && st.Track == nil) {
		m.note = "not playing: nothing to change"
		return m, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var err error
	switch m.row {
	case rowVolume:
		err = p.SetVolume(ctx, min(max(st.Volume+5*dir, 0), 100))
	case rowShuffle:
		if mo, ok := p.(music.Modes); ok {
			err = mo.SetShuffle(ctx, !st.Shuffle)
		} else {
			m.note = "this player has no shuffle"
		}
	case rowRepeat:
		if mo, ok := p.(music.Modes); ok {
			cur := 0
			for i, r := range quickRepeats {
				if r == st.Repeat {
					cur = i
				}
			}
			err = mo.SetRepeat(ctx, quickRepeats[((cur+dir)%len(quickRepeats)+len(quickRepeats))%len(quickRepeats)])
		} else {
			m.note = "this player has no repeat"
		}
	}
	if err != nil {
		m.note = err.Error()
		return m, nil
	}
	// mpv reports a new value a moment later; ask for it now so the screen shows what the player has, not what it had.
	_, _ = p.Sync(ctx)
	return m, nil
}

// View draws the screen at the size it was given, one string per row of the terminal.
func (m QuickModel) View() tea.View { return tea.NewView(strings.Join(m.lines(), "\n")) }

func (m QuickModel) lines() []string {
	if m.w < 1 || m.h < 1 {
		return []string{""}
	}
	st := music.State{Index: -1}
	if m.deps.Player != nil {
		st = m.deps.Player.State()
	}
	live := st.Connected || st.Track != nil
	out := []string{" " + bold.Render("Pigpen Music") + dim.Render("  quick settings"), ""}
	switch {
	case st.Track != nil:
		state := "Playing"
		if st.Paused {
			state = "Paused"
		}
		line := clean(st.Track.Title)
		if len(st.Track.Artists) > 0 {
			line += " - " + clean(strings.Join(st.Track.Artists, ", "))
		}
		out = append(out, " "+bold.Render(state)+"  "+line+dim.Render("  "+clock(st.Position)+"/"+clock(st.Duration)), "")
	default:
		out = append(out, dim.Render(" Nothing playing"), "")
	}
	for i := 0; i < rowCount; i++ {
		mark := "  "
		if i == m.row {
			mark = "> "
		}
		label := fmt.Sprintf("%-18s", quickLabels[i])
		val := m.value(i, st, live)
		line := " " + mark + label + val
		if i == m.row {
			line = reverse.Render(pad(line, m.w))
		}
		out = append(out, line)
	}
	out = append(out, "")
	if m.note != "" {
		out = append(out, " "+clean(m.note))
	}
	out = append(out, dim.Render(" up/down choose   left/right change   esc close"))
	res := make([]string, m.h)
	for i := range res {
		if i < len(out) {
			res[i] = pad(out[i], m.w)
		} else {
			res[i] = pad("", m.w)
		}
	}
	return res
}

func (m QuickModel) value(row int, st music.State, live bool) string {
	onoff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	switch row {
	case rowVolume:
		if !live {
			return "(not playing)"
		}
		return fmt.Sprintf("< %d >", st.Volume)
	case rowShuffle:
		if !live {
			return "(not playing)"
		}
		return onoff(st.Shuffle)
	case rowRepeat:
		if !live {
			return "(not playing)"
		}
		return orDefault(string(st.Repeat), "off")
	case rowEngine:
		return orDefault(m.vals.Engine, "auto")
	case rowBrowser:
		return orDefault(m.vals.CookieBrowser, "system default")
	case rowNowPlaying:
		return orDefault(m.vals.NowPlaying, "auto")
	case rowPalette:
		return onoff(m.vals.Palette)
	case rowPulse:
		return onoff(m.vals.Pulse)
	case rowCalm:
		return onoff(m.vals.Calm)
	case rowLowPower:
		return onoff(m.vals.LowPower)
	}
	return onoff(m.vals.CoverArt)
}
