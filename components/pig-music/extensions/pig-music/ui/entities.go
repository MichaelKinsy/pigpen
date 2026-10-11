package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// entitiesPerKind is how many albums and how many artists a search asks for.
const entitiesPerKind = 5

func (m Model) entitiesDone(msg entitiesDoneMsg) Model {
	if msg.query != m.searched || msg.gen != m.searchGen || msg.err != nil { // a later search replaced this one; a failure leaves the songs alone
		return m
	}
	m.ents = msg.ents
	if len(m.ents) > 0 && m.entKind == "" {
		m.entKind = "songs"
	}
	return m
}

// entitiesOf are the albums or the artists among the search's entities.
func (m Model) entitiesOf(kind string) []music.Entity {
	var out []music.Entity
	for _, e := range m.ents {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func (m Model) shownEntities() []music.Entity {
	switch m.entKind {
	case "albums":
		return m.entitiesOf("album")
	case "artists":
		return m.entitiesOf("artist")
	}
	return nil
}

// entityTabs is the line "Songs 20  Albums 3  Artists 3" over the results, with the shown list bold; "" when the search found
// no albums or artists.
func (m Model) entityTabs() string {
	row, _ := m.entityTabsSpans()
	return row
}

// entityTabsSpans is entityTabs with the cells of its kinds, each with the key that shows it; "" and none without albums or
// artists.
func (m Model) entityTabsSpans() (string, []span) {
	albums, artists := len(m.entitiesOf("album")), len(m.entitiesOf("artist"))
	if albums+artists == 0 {
		return "", nil
	}
	var b strings.Builder
	spans := make([]span, 0, 3)
	b.WriteString(" ")
	at := 1
	part := func(key, name string, n int, kind, gap string) {
		label := fmt.Sprintf("%s %s %d", key, name, n)
		if m.entKind == kind || (m.entKind == "" && kind == "songs") {
			b.WriteString(accent.Render(label))
		} else {
			b.WriteString(dim.Render(label))
		}
		b.WriteString(gap)
		w := ansi.StringWidth(label)
		spans = append(spans, span{at, at + w, key})
		at += w + len(gap)
	}
	part("1", "Songs", len(m.results), "songs", "   ")
	part("2", "Albums", albums, "albums", "   ")
	part("3", "Artists", artists, "artists", "")
	spans[0].from = 0 // the margin is the first kind's: a frontend draws them as one node (kitview.go), named by the margin's cell
	return b.String(), spans
}

func (m Model) entityListLines(rows int) []string {
	list := m.shownEntities()
	if len(list) == 0 || rows < 1 {
		return nil
	}
	titleW := max(min(m.w/2, 44), 12)
	first, end := window(len(list), m.entCur, rows)
	out := make([]string, 0, end-first)
	for i := first; i < end; i++ {
		e := list[i]
		row := " " + pad(clean(e.Title), titleW) + " " + clean(e.Subtitle)
		if i == m.entCur {
			row = reverse.Render(pad(row, m.w))
		}
		out = append(out, row)
	}
	return out
}

// entityKey handles the keys of albums and artists on the search screen: the number keys switch lists, the arrows move, enter
// opens its page (pages.go).
func (m Model) entityKey(k string) (tea.Model, tea.Cmd, bool) {
	if len(m.ents) == 0 {
		return m, nil, false
	}
	switch k {
	case "1":
		m.entKind, m.entCur = "songs", 0
		return m, nil, true
	case "2", "3":
		kind, want := "albums", "album"
		if k == "3" {
			kind, want = "artists", "artist"
		}
		if len(m.entitiesOf(want)) > 0 {
			m.entKind, m.entCur = kind, 0
		}
		return m, nil, true
	}
	list := m.shownEntities()
	if len(list) == 0 {
		return m, nil, false
	}
	switch k {
	case "down", "j":
		m.entCur = clamp(m.entCur+1, len(list))
	case "up", "k":
		m.entCur = clamp(m.entCur-1, len(list))
	case "enter":
		next, cmd := m.openEntity(list[m.entCur])
		return next, cmd, true
	case "a":
		return m, nil, true // nothing to add: an album or an artist is opened first
	default:
		return m, nil, false
	}
	return m, nil, true
}
