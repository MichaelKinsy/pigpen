package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// SettingsPort is the settings screen's way to the settings file: two switches it can change and some lines of
// read-only information about the machine (programs, runtime, browser). Changes are saved at once.
type SettingsPort interface {
	Get() (stopOnExit, footerStatus bool, err error)
	// Set changes "stopOnExit" or "footerStatus".
	Set(key string, on bool) error
	Info() []string
}

type settingsView struct {
	open         bool
	cur          int
	stopOnExit   bool
	footerStatus bool
	info         []string
	loadErr      string
}

type settingsLoadedMsg struct {
	stop, footer bool
	info         []string
	err          error
}

type settingsSavedMsg struct {
	key string
	on  bool
	err error
}

var settingRows = []struct{ key, text string }{
	{"stopOnExit", "Stop the music when PiG quits (stopOnExit)"},
	{"footerStatus", "Show the track in PiG's footer while hidden (footerStatus)"},
}

func (m Model) openSettings() (tea.Model, tea.Cmd) {
	port := m.deps.Settings
	m.settings = settingsView{open: true}
	if port == nil {
		return m, nil
	}
	return m, func() tea.Msg {
		stop, footer, err := port.Get()
		return settingsLoadedMsg{stop, footer, port.Info(), err}
	}
}

func (m Model) settingsKey(k string) (tea.Model, tea.Cmd) {
	s := m.settings
	switch k {
	case "q":
		return m, tea.Quit
	case "esc", "o":
		m.settings.open = false
	case "down", "j":
		m.settings.cur = clamp(s.cur+1, len(settingRows))
	case "up", "k":
		m.settings.cur = clamp(s.cur-1, len(settingRows))
	case "space", "enter":
		port := m.deps.Settings
		if port == nil || s.loadErr != "" {
			return m, nil
		}
		key := settingRows[s.cur].key
		on := !(key == "stopOnExit" && s.stopOnExit || key == "footerStatus" && s.footerStatus)
		return m, func() tea.Msg { return settingsSavedMsg{key, on, port.Set(key, on)} }
	}
	return m, nil
}

func (m Model) settingsSaved(msg settingsSavedMsg) Model {
	if msg.err != nil {
		m.status = msg.err.Error()
		return m
	}
	switch msg.key {
	case "stopOnExit":
		m.settings.stopOnExit = msg.on
	case "footerStatus":
		m.settings.footerStatus = msg.on
	}
	return m
}

func (m Model) settingsLines() []string {
	s := m.settings
	out := []string{" " + bold.Render("Settings"), ""}
	if m.deps.Settings == nil {
		return append(out, " The settings file is not available here.", dim.Render(" Press esc to go back."))
	}
	if s.loadErr != "" {
		out = append(out, m.wrapped(clean(s.loadErr))...)
		return append(out, "", dim.Render(" Press esc to go back."))
	}
	for i, r := range settingRows {
		on := r.key == "stopOnExit" && s.stopOnExit || r.key == "footerStatus" && s.footerStatus
		box := "[ ]"
		if on {
			box = "[x]"
		}
		row := " " + box + " " + r.text
		if i == s.cur {
			row = reverse.Render(pad(row, m.w))
		}
		out = append(out, row)
	}
	out = append(out, "")
	for _, line := range s.info {
		out = append(out, " "+dim.Render(clean(strings.TrimSpace(line))))
	}
	return append(out, "", dim.Render(" space toggles and saves to settings.json; esc closes."))
}
