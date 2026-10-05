package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// enrichParallel is how many enrichments run at once. Each is a yt-dlp run of a few seconds, so a list is filled a few
// tracks at a time, in the order a person is likely to look at it, never on the drawing path.
const enrichParallel = 3

// enrichWindow is how many rows of one list, from the cursor on, are asked about: the liked songs are hundreds of rows and
// each answer is a request, so only what is on screen and just below it is filled in, and moving the cursor asks for more.
const enrichWindow = 30

// enrichState is what has been learned about tracks, by video ID, for as long as the model lives. It is shared by the copies
// of the model (the model is a value) and touched only from Update.
type enrichState struct {
	info     map[string]music.Track // answers, keyed by video ID
	asked    map[string]bool        // every ID asked about, answered or not: nothing is asked twice on its own
	failed   map[string]bool        // IDs whose answer was an error; a new search (an explicit action) may ask again
	inflight int
	failures int
}

func newEnrichState() *enrichState {
	return &enrichState{info: map[string]music.Track{}, asked: map[string]bool{}, failed: map[string]bool{}}
}

// fill completes a track from what is known: a cached answer, and the length mpv reports for the track that is playing.
func (m Model) fill(t music.Track) music.Track {
	if e, ok := m.enr.info[t.ID]; ok {
		if len(t.Artists) == 0 {
			t.Artists = e.Artists
		}
		if t.Album == "" {
			t.Album = e.Album
		}
		if t.Duration == 0 {
			t.Duration = e.Duration
		}
	}
	if cur := m.state.Track; cur != nil && cur.ID == t.ID && t.Duration == 0 && m.state.Duration > 0 {
		t.Duration = m.state.Duration
	}
	return t
}

// filled is fill for a list; it never changes the list it is given.
func (m Model) filled(ts []music.Track) []music.Track {
	out := make([]music.Track, len(ts))
	for i, t := range ts {
		out[i] = m.fill(t)
	}
	return out
}

// needs reports whether a track still lacks what a screen shows for it. The track that is playing also wants its album.
func (m Model) needs(t music.Track, current bool) bool {
	if t.ID == "" || m.enr.asked[t.ID] {
		return false
	}
	t = m.fill(t)
	return t.Duration == 0 || len(t.Artists) == 0 || (current && t.Album == "")
}

// wanted lists the tracks to ask about, most useful first: the playing track, then what the current screen is on and
// around, then the rest of what the other screens hold.
func (m Model) wanted() []music.Track {
	var out []music.Track
	add := func(ts ...music.Track) { out = append(out, ts[:min(len(ts), enrichWindow)]...) }
	rotate := func(ts []music.Track, from int) []music.Track {
		if from < 0 || from >= len(ts) {
			return ts
		}
		return append(append([]music.Track(nil), ts[from:]...), ts[:from]...)
	}
	queue := rotate(m.state.Queue, max(m.state.Index, 0))
	results := rotate(m.results, m.resCur)
	var library, opened []music.Track
	if m.lib.open != nil {
		library = rotate(m.lib.tracks, m.lib.tcur)
	}
	if m.ent != nil {
		opened = rotate(m.ent.tracks, m.ent.cur) // an opened album or artist (entities.go)
	}
	switch m.tab {
	case tabPlayer:
		add(rotate(m.state.Queue, m.queueCur)...)
		add(results...)
		add(opened...)
		add(library...)
	case tabLibrary:
		add(library...)
		add(queue...)
		add(results...)
		add(opened...)
	default:
		add(opened...)
		add(results...)
		add(queue...)
		add(library...)
	}
	return out
}

// pump starts enrichments, up to enrichParallel at a time. It is called whenever the lists or the cursor change and when an
// answer arrives, which is what lets the next track start.
func (m Model) pump() (tea.Model, tea.Cmd) {
	e, ok := m.deps.Source.(music.Enricher)
	if !ok {
		return m, nil
	}
	var cmds []tea.Cmd
	start := func(t music.Track) {
		m.enr.asked[t.ID] = true
		m.enr.inflight++
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
			defer cancel()
			got, err := e.Enrich(ctx, t)
			return enrichedMsg{id: t.ID, track: got, err: err}
		})
	}
	if cur := m.state.Track; cur != nil && m.enr.inflight < enrichParallel && m.needs(*cur, true) {
		start(*cur)
	}
	for _, t := range m.wanted() {
		if m.enr.inflight >= enrichParallel {
			break
		}
		if m.needs(t, false) {
			start(t)
		}
	}
	return m, tea.Batch(cmds...)
}

// retryFailed lets the tracks whose enrichment failed be asked again. A new search calls it: a network blip while a list
// was being filled in fails every track then asked about, and they would otherwise stay blank until the extension restarts.
func (e *enrichState) retryFailed() {
	for id := range e.failed {
		delete(e.asked, id)
	}
	e.failed = map[string]bool{}
	e.failures = 0
}
