package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// The Search screen's home is what it shows before anything is searched: Recents (what was searched and played here, kept in
// the data directory), Suggested (the account's YouTube Music home, when the user allowed the account for the library) and
// Popular (a chart, no account). It loads when it is first seen, from the last session's copy at once and then from YouTube
// Music, all off the update loop; one cursor runs through every row of it.

// feedState is how far one of the feeds got.
type feedState int

const (
	feedIdle    feedState = iota
	feedLoading           // asked; rows from the cache may show meanwhile
	feedReady
	feedFailed  // the last answer was an error (err); rows from before stay
	feedConsent // the suggestions need the user's OK for the account, given on the Library tab
	feedNone    // there is no such feed (no account in use, a source without it)
)

// How many rows of each the home shows.
const (
	homeRecents       = 6
	homeSuggestedSecs = 2
	homeSuggestedRows = 5
	homePopular       = 20
)

type homeState struct {
	asked bool // loaded this session (it loads on first sight, and again with r)
	gen   int  // counts loads, so the answers of an earlier one are told apart
	cur   int  // the cursor, over all the rows of every section

	recents []music.Recent

	popular   music.Section
	pop       feedState
	popErr    string
	popCached bool // the rows are the last session's

	suggested []music.Section
	sug       feedState
	sugErr    string
	sugCached bool
}

type recentsMsg struct {
	list []music.Recent
	err  error
}

type homeCachedMsg struct {
	gen       int
	popular   music.Section
	suggested []music.Section
}

type popularMsg struct {
	gen int
	sec music.Section
	err error
}

type suggestedMsg struct {
	gen  int
	secs []music.Section
	err  error
}

// homeActive: this Search screen has a home (a history, or a source with feeds); else it shows the bare hint it always had.
func (m Model) homeActive() bool {
	_, feed := m.deps.Source.(music.HomeFeed)
	return m.deps.Recents != nil || feed
}

// homeShown: the Search screen shows its home now.
func (m Model) homeShown() bool {
	return m.tab == tabSearch && m.bodyShown() == bodySearch && m.searched == "" && m.homeActive()
}

// loadHome asks for the history and both feeds; withCache also asks for the last session's copy of the feeds, which shows
// until the answers come.
func (m Model) loadHome(withCache bool) (Model, tea.Cmd) {
	m.home.asked = true
	m.home.gen++
	gen := m.home.gen
	var cmds []tea.Cmd
	if r := m.deps.Recents; r != nil && withCache {
		cmds = append(cmds, func() tea.Msg {
			list, err := r.Load()
			return recentsMsg{list, err}
		})
	}
	feed, ok := m.deps.Source.(music.HomeFeed)
	if !ok {
		return m, tea.Batch(cmds...)
	}
	m.home.pop, m.home.popErr = feedLoading, ""
	if m.home.sug != feedNone && m.home.sug != feedConsent || withCache {
		m.home.sug, m.home.sugErr = feedLoading, ""
	}
	if withCache {
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
			defer cancel()
			pop, sug, _ := feed.CachedHome(ctx)
			return homeCachedMsg{gen, pop, sug}
		})
	}
	cmds = append(cmds, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		sec, err := feed.Popular(ctx)
		return popularMsg{gen, sec, err}
	}, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), libTimeout) // the first read of the account may export cookies
		defer cancel()
		secs, err := feed.Suggested(ctx)
		return suggestedMsg{gen, secs, err}
	})
	return m, tea.Batch(cmds...)
}

func (m Model) homeMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	h := &m.home
	switch msg := msg.(type) {
	case recentsMsg:
		if msg.err != nil {
			m.status = "recents: " + msg.err.Error()
		}
		if msg.err == nil || msg.list != nil {
			h.recents = msg.list
		}
	case homeCachedMsg:
		if msg.gen != h.gen {
			break
		}
		if h.pop == feedLoading && len(msg.popular.Tracks) > 0 {
			h.popular, h.popCached = msg.popular, true
		}
		if h.sug == feedLoading && len(msg.suggested) > 0 {
			h.suggested, h.sugCached = msg.suggested, true
		}
	case popularMsg:
		if msg.gen != h.gen {
			break
		}
		if msg.err != nil {
			h.pop, h.popErr = feedFailed, msg.err.Error()
			break
		}
		h.pop, h.popular, h.popCached = feedReady, msg.sec, false
	case suggestedMsg:
		if msg.gen != h.gen {
			break
		}
		var need *music.NeedsConsentError
		var none *music.NoBrowserError
		switch {
		case errors.As(msg.err, &need):
			h.sug, h.suggested = feedConsent, nil
		case errors.Is(msg.err, music.ErrNoAccount) || errors.As(msg.err, &none):
			h.sug, h.suggested = feedNone, nil
		case msg.err != nil:
			h.sug, h.sugErr = feedFailed, msg.err.Error()
		default:
			h.sug, h.suggested, h.sugCached = feedReady, msg.secs, false
		}
	}
	h.cur = clamp(h.cur, len(m.homeItems()))
	return m, nil
}

// remember adds r to the history, off the update loop; nil without a history.
func (m Model) remember(r music.Recent) tea.Cmd {
	rec := m.deps.Recents
	if rec == nil {
		return nil
	}
	if r.Track != nil {
		t := m.fill(*r.Track)
		r.Track = &t
	}
	return func() tea.Msg {
		list, err := rec.Add(r)
		return recentsMsg{list, err}
	}
}

// homeItem is one row of the home that can be chosen: a search to run again, a track to play (from its place in queue), or an
// album, artist or playlist to open.
type homeItem struct {
	query string
	track *music.Track
	ent   *music.Entity
	queue []music.Track
	at    int
	num   int // the place in the chart; 0 for none
}

// homeSection is one section of the home: its heading, and its rows of items, or a line of text instead (loading, a hint, an
// error).
type homeSection struct {
	recents bool // the Recents, whose heading has the clear key
	title   string
	note    string
	items   []homeItem
	text    string
}

func (m Model) homeSections() []homeSection {
	var out []homeSection
	h := m.home
	if len(h.recents) > 0 {
		sec := homeSection{recents: true, title: "Recents"}
		var tracks []music.Track
		for _, r := range h.recents {
			if r.Track != nil {
				tracks = append(tracks, *r.Track)
			}
		}
		at := 0
		for _, r := range h.recents[:min(len(h.recents), homeRecents)] {
			if r.Track != nil {
				sec.items = append(sec.items, homeItem{track: &tracks[at], queue: tracks, at: at})
				at++
				continue
			}
			sec.items = append(sec.items, homeItem{query: r.Query})
		}
		out = append(out, sec)
	}
	state := func(s feedState, cached bool, err, what string) (note, text string) {
		switch {
		case s == feedLoading && cached:
			return "updating...", ""
		case s == feedLoading:
			return "", "Loading..."
		case s == feedFailed && cached:
			return "not updated: " + err, ""
		case s == feedFailed:
			return "", "Could not load " + what + ": " + err + "  (r tries again)"
		}
		return "", ""
	}
	switch {
	case h.sug == feedConsent:
		out = append(out, homeSection{title: "Suggested", text: "Allow your YouTube Music account on the Library tab to see suggestions."})
	case len(h.suggested) > 0:
		note, _ := state(h.sug, h.sugCached, h.sugErr, "the suggestions")
		for i, s := range h.suggested[:min(len(h.suggested), homeSuggestedSecs)] {
			sec := homeSection{title: "Suggested", note: clean(s.Title)}
			if i == 0 && note != "" {
				sec.note += "  " + note
			}
			n := 0
			for j := range s.Tracks {
				if n == homeSuggestedRows {
					break
				}
				sec.items = append(sec.items, homeItem{track: &s.Tracks[j], queue: s.Tracks, at: j})
				n++
			}
			for j := range s.Entities {
				if n == homeSuggestedRows {
					break
				}
				sec.items = append(sec.items, homeItem{ent: &s.Entities[j]})
				n++
			}
			out = append(out, sec)
		}
	case h.sug == feedLoading || h.sug == feedFailed:
		_, text := state(h.sug, false, h.sugErr, "the suggestions")
		out = append(out, homeSection{title: "Suggested", text: text})
	}
	switch {
	case len(h.popular.Tracks) > 0:
		note, _ := state(h.pop, h.popCached, h.popErr, "the chart")
		sec := homeSection{title: "Popular", note: clean(h.popular.Title)}
		if note != "" {
			sec.note += "  " + note
		}
		ts := h.popular.Tracks
		for i := range ts[:min(len(ts), homePopular)] {
			sec.items = append(sec.items, homeItem{track: &ts[i], queue: ts, at: i, num: i + 1})
		}
		out = append(out, sec)
	case h.pop == feedLoading || h.pop == feedFailed:
		_, text := state(h.pop, false, h.popErr, "the chart")
		out = append(out, homeSection{title: "Popular", text: text})
	}
	return out
}

// homeItems is every row of the home the cursor goes through, in order.
func (m Model) homeItems() []homeItem {
	var out []homeItem
	for _, s := range m.homeSections() {
		out = append(out, s.items...)
	}
	return out
}

func (m Model) homeKey(k string) (tea.Model, tea.Cmd) {
	items := m.homeItems()
	switch k {
	case "down", "j":
		m.home.cur = clamp(m.home.cur+1, len(items))
	case "up", "k":
		m.home.cur = clamp(m.home.cur-1, len(items))
	case "enter":
		if m.home.cur >= len(items) {
			return m, nil
		}
		it := items[m.home.cur]
		switch {
		case it.ent != nil:
			return m.openEntity(*it.ent)
		case it.track != nil:
			p, queue, at := m.deps.Player, m.filled(it.queue), it.at
			m.tab = tabPlayer
			m.queueGen.Add(1)
			return m, tea.Batch(op(func(ctx context.Context) error { return p.Replace(ctx, queue, at) }), m.remember(music.Recent{Track: &queue[at]}))
		default:
			m.query = it.query
			return m.search(it.query)
		}
	case "a":
		if m.home.cur < len(items) && items[m.home.cur].track != nil {
			p, t := m.deps.Player, m.fill(*items[m.home.cur].track)
			return m, op(func(ctx context.Context) error { return p.Enqueue(ctx, t) })
		}
	case "C":
		rec := m.deps.Recents
		if rec == nil || len(m.home.recents) == 0 {
			return m, nil
		}
		m.home.recents = nil
		m.home.cur = clamp(m.home.cur, len(m.homeItems()))
		m = m.toasted("Recents cleared")
		return m, func() tea.Msg { return recentsMsg{nil, rec.Clear()} }
	case "r":
		return m.loadHome(false)
	}
	return m, nil
}

// homeTrack is the track under the home's cursor, for the "go to" keys.
func (m Model) homeTrack() (music.Track, bool) {
	items := m.homeItems()
	if m.home.cur < len(items) && items[m.home.cur].track != nil {
		return *items[m.home.cur].track, true
	}
	return music.Track{}, false
}

// homeLine is one row of the home as it is drawn, with what it is: text, a heading with the clear key, or item i of the
// cursor's rows.
type homeLine struct {
	text  string
	spans []span // a heading whose clear key a click presses
	item  int    // -1 for none
	sec   int
}

// homeLines is the whole home, top to bottom, before it is fitted to the rows there are.
func (m Model) homeLines(intro string) []homeLine {
	secs := m.homeSections()
	if len(secs) == 0 {
		return nil
	}
	var anyExplicit bool
	for _, s := range secs {
		for _, it := range s.items {
			anyExplicit = anyExplicit || it.track != nil && it.track.Explicit
		}
	}
	plan := columnPlan(m.w, false, anyExplicit)
	out := []homeLine{{text: intro, item: -1}, {item: -1}}
	n := 0
	for si, s := range secs {
		if si > 0 {
			out = append(out, homeLine{item: -1, sec: si})
		}
		head := " " + bold.Render(s.title)
		var spans []span
		if s.note != "" {
			head += dim.Render("  " + s.note)
		}
		if s.recents {
			const clear = "C clear"
			at := ansi.StringWidth(head) + 2
			if at+len(clear) <= m.w {
				head += "  " + dim.Render(clear)
				spans = []span{{at, at + len(clear), "C"}}
			}
		}
		out = append(out, homeLine{text: head, spans: spans, item: -1, sec: si})
		if s.text != "" {
			out = append(out, homeLine{text: dim.Render("  " + s.text), item: -1, sec: si})
		}
		for _, it := range s.items {
			row := m.homeRow(plan, it)
			if n == m.home.cur {
				row = reverse.Render(pad(row, m.w))
			}
			out = append(out, homeLine{text: row, item: n, sec: si})
			n++
		}
	}
	return out
}

// homeRow is one item's row, in the columns of a track table: a mark (the chart place, "/" for a search), the title, who or
// what it is, the length.
func (m Model) homeRow(plan columns, it homeItem) string {
	switch {
	case it.track != nil:
		t := m.fill(*it.track)
		num, l, badge := "", "", ""
		if it.num > 0 {
			num = fmt.Sprintf("%d.", it.num)
		}
		if t.Duration > 0 {
			l = clock(t.Duration)
		}
		if t.Explicit {
			badge = "E"
		}
		return planLine(plan, num, clean(t.Title), badge, clean(strings.Join(t.Artists, ", ")), "", l)
	case it.ent != nil:
		detail := map[string]string{"album": "Album", "artist": "Artist", "playlist": "Playlist"}[it.ent.Kind]
		if it.ent.Subtitle != "" {
			detail += " · " + it.ent.Subtitle
		}
		return planLine(plan, "", clean(it.ent.Title), "", clean(detail), "", "")
	}
	return planLine(plan, "/", clean(it.query), "", "search", "", "")
}

// homeItemOf is the list item of a row of the home.
func (m Model) homeItemOf(it homeItem) ListItem {
	switch {
	case it.track != nil:
		t := m.fill(*it.track)
		l := ""
		if t.Duration > 0 {
			l = clock(t.Duration)
		}
		return ListItem{Label: clean(t.Title), Detail: clean(strings.Join(t.Artists, ", ")), Columns: []string{l}}
	case it.ent != nil:
		return ListItem{Label: clean(it.ent.Title), Detail: clean(it.ent.Subtitle)}
	}
	return ListItem{Label: clean(it.query), Detail: "search"}
}

// homeBody draws the home in rows rows: the first row (intro) stays, the rest scrolls to keep the cursor in sight. Each run of
// a section's items is a list, a heading with the clear key a row of tabs. It reports false when there is no home to draw.
func (m Model) homeBody(s *sheet, intro string, rows int) bool {
	lines := m.homeLines(intro)
	if len(lines) == 0 {
		return false
	}
	first := 1
	if len(lines) > rows && rows > 1 {
		at := 1
		for i, l := range lines {
			if l.item == m.home.cur {
				at = i
			}
		}
		first = min(max(at-(rows-1)/2, 1), len(lines)-(rows-1))
	}
	shown := append([]homeLine{lines[0]}, lines[first:min(len(lines), first+rows-1)]...)
	if rows < 1 {
		shown = nil
	}
	items := m.homeItems()
	for i := 0; i < len(shown); {
		l := shown[i]
		switch {
		case l.item >= 0:
			j := i
			var row []string
			var li []ListItem
			for ; j < len(shown) && shown[j].item >= 0 && shown[j].sec == l.sec; j++ {
				row = append(row, shown[j].text)
				li = append(li, m.homeItemOf(items[shown[j].item]))
			}
			s.list(sharedList(li, l.item, m.home.cur), row...)
			i = j
			continue
		case l.spans != nil:
			s.keyTabs(l.text, l.spans)
		default:
			s.add(PartText, l.text)
		}
		i++
	}
	return true
}
