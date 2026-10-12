package pig_music

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// ScreenView is the kit view the player sends for a sized model (ui.Model or ui.QuickModel), as its host draws it.
func ScreenView(m tea.Model, width int) kit.View {
	text, shape := drawScreen(m)
	return screenView(strings.Split(text, "\n"), shape, width)
}

// WidgetView is the now-playing widget's view for text and st.
func WidgetView(text string, st music.State, theme map[string]string) kit.View {
	return widgetView(text, st, theme)
}

// AgreedRunes are the ranges of characters a truncated text or a stage column may hold.
func AgreedRunes() [][2]rune { return agreedRunes }
