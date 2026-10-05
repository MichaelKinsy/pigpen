package ui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// entitiesPerKind is how many albums and how many artists a search asks for.
const entitiesPerKind = 5

// entityPage is an opened album or artist: its tracks, to play from the one under the cursor.
type entityPage struct {
	e       music.Entity
	title   string
	tracks  []music.Track
	cur     int
	loading bool
	gen     int // Model.entGen when it was opened
}

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
	albums, artists := len(m.entitiesOf("album")), len(m.entitiesOf("artist"))
	if albums+artists == 0 {
		return ""
	}
	part := func(key, name string, n int, kind string) string {
		label := fmt.Sprintf("%s %s %d", key, name, n)
		if m.entKind == kind || (m.entKind == "" && kind == "songs") {
			return accent.Render(label)
		}
		return dim.Render(label)
	}
	return " " + part("1", "Songs", len(m.results), "songs") + "   " + part("2", "Albums", albums, "albums") + "   " + part("3", "Artists", artists, "artists")
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

func (m Model) entityPageLines(rows int) []string {
	p := m.ent
	out := []string{" " + bold.Render(clean(p.title)) + dim.Render("  backspace: back")}
	if p.loading {
		return append(out, "", dim.Render(" Loading..."))
	}
	if len(p.tracks) == 0 {
		return append(out, "", dim.Render(" Nothing to show."))
	}
	return append(out, m.trackTable(m.filled(p.tracks), p.cur, -1, rows-1)...)
}

// entityKey handles the keys of albums and artists on the search screen: the number keys switch lists, the arrows move, enter
// opens, and in an opened one enter plays and backspace goes back.
func (m Model) entityKey(k string) (tea.Model, tea.Cmd, bool) {
	if m.ent != nil {
		return m.openedEntityKey(k)
	}
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
		e := list[m.entCur]
		m.entGen++
		gen := m.entGen
		m.ent = &entityPage{e: e, title: e.Title, loading: true, gen: gen}
		src, ok := m.deps.Source.(music.EntitySearcher)
		if !ok {
			m.ent = nil
			return m, nil, true
		}
		m = m.stopEntity()
		parent, stop := context.WithCancel(context.Background())
		m.entStop = stop
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(parent, libTimeout)
			defer cancel()
			title, tracks, err := src.OpenEntity(ctx, e)
			return entityOpenedMsg{id: e.ID, title: title, tracks: tracks, err: err, gen: gen}
		}, true
	case "a":
		return m, nil, true // nothing to add: an album or an artist is opened first
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m Model) openedEntityKey(k string) (tea.Model, tea.Cmd, bool) {
	p := m.ent
	n := len(p.tracks)
	switch k {
	case "down", "j", "up", "k":
		cp := *p
		if k == "down" || k == "j" {
			cp.cur = clamp(p.cur+1, n)
		} else {
			cp.cur = clamp(p.cur-1, n)
		}
		m.ent = &cp
		next, cmd := m.pump() // the rows from the cursor on are filled first
		return next, cmd, true
	case "backspace":
		m.ent = nil
		m = m.stopEntity() // an album left while it loads is not asked about any longer
	case "enter":
		if n == 0 || p.loading {
			return m, nil, true
		}
		pl, res, at := m.deps.Player, m.filled(p.tracks), p.cur
		m.tab = tabPlayer
		m.queueGen.Add(1)
		return m, op(func(ctx context.Context) error { return pl.Replace(ctx, res, at) }), true
	case "a":
		if n == 0 || p.loading {
			return m, nil, true
		}
		pl, t := m.deps.Player, m.fill(p.tracks[p.cur])
		return m, op(func(ctx context.Context) error { return pl.Enqueue(ctx, t) }), true
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m Model) entityOpened(msg entityOpenedMsg) Model {
	if m.ent == nil || m.ent.e.ID != msg.id || m.ent.gen != msg.gen {
		return m // an answer for something that was left
	}
	if msg.err != nil {
		m.status = strings.TrimSpace(msg.err.Error())
		m.ent = nil
		return m
	}
	cp := *m.ent
	cp.loading, cp.tracks = false, msg.tracks
	if msg.title != "" {
		cp.title = msg.title
	}
	m.ent = &cp
	return m
}

// stopEntity cancels the request of the album or artist being opened, if any.
func (m Model) stopEntity() Model {
	if m.entStop != nil {
		m.entStop()
		m.entStop = nil
	}
	return m
}
