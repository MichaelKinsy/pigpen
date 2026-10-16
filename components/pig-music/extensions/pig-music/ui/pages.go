package ui

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Pages are YouTube Music's own: an artist's (top songs, albums, singles, videos, related artists), an album's, a playlist's,
// a track's radio and its Related page. They open over the screen they were asked from, one on another, and Esc (or
// backspace) goes back one. The keys that open them are the same on every screen, for the track under the cursor (or the one
// that is playing, on the Player screen): A artist, B album, R radio, T related.

// pageView is one page of the stack.
type pageView struct {
	e       music.Entity // what was asked for; Kind says which page
	title   string
	sub     string
	secs    []music.Section
	rows    []pageRow
	cur     int // index into rows, on a selectable one
	loading bool
	gen     int
}

// pageRow is a line of a page: a section's heading, or one of its tracks, or one of its entities.
type pageRow struct {
	sec, i int
	kind   rowKind
}

type rowKind int

const (
	rowHead rowKind = iota
	rowTrack
	rowEntity
)

// pageOpenedMsg is the answer for a page that was asked for by its entity.
type pageOpenedMsg struct {
	gen  int
	page music.Page
	err  error
}

// watchMsg is the answer of a "go to" key: what YouTube Music says about the track.
type watchMsg struct {
	gen   int
	kind  string // "artist" | "album" | "radio" | "related"
	track music.Track
	w     music.Watch
	err   error
}

// GoMsg is the quick command /music artist, album, radio or related: the same as the key, for the track that is playing
// (Track is the one the command saw playing, for when the model has not heard of it yet).
type GoMsg struct {
	Kind  string
	Track *music.Track
}

var goKinds = map[string]string{"A": "artist", "B": "album", "R": "radio", "T": "related"}

// pageStackMax bounds how deep the stack goes (each page holds a list of rows).
const pageStackMax = 12

func (p *pageView) build() {
	p.rows = p.rows[:0]
	for s, sec := range p.secs {
		if sec.Title != "" && len(p.secs) > 1 {
			p.rows = append(p.rows, pageRow{sec: s, kind: rowHead})
		}
		for i := range sec.Tracks {
			p.rows = append(p.rows, pageRow{sec: s, i: i, kind: rowTrack})
		}
		for i := range sec.Entities {
			p.rows = append(p.rows, pageRow{sec: s, i: i, kind: rowEntity})
		}
	}
	p.cur = 0
	for p.cur < len(p.rows) && p.rows[p.cur].kind == rowHead {
		p.cur++
	}
}

func (p *pageView) row() (pageRow, bool) {
	if p.cur < 0 || p.cur >= len(p.rows) {
		return pageRow{}, false
	}
	return p.rows[p.cur], true
}

func (p *pageView) track() (music.Track, bool) {
	if r, ok := p.row(); ok && r.kind == rowTrack {
		return p.secs[r.sec].Tracks[r.i], true
	}
	return music.Track{}, false
}

// move puts the cursor on the next selectable row in a direction (headings are skipped), staying put at the ends.
func (p *pageView) move(dir int) {
	for i := p.cur + dir; i >= 0 && i < len(p.rows); i += dir {
		if p.rows[i].kind != rowHead {
			p.cur = i
			return
		}
	}
}

// listFromCursor is the tracks of the cursor's section, from its row on (what Enter plays), and where it starts.
func (p *pageView) section() (music.Section, int) {
	if r, ok := p.row(); ok && len(p.secs[r.sec].Tracks) > 0 {
		return p.secs[r.sec], r.i
	}
	for _, s := range p.secs {
		if len(s.Tracks) > 0 {
			return s, 0
		}
	}
	return music.Section{}, 0
}

func (m Model) topPage() *pageView {
	if len(m.pages) == 0 || m.tab != m.pagesTab || m.typing {
		return nil
	}
	return m.pages[len(m.pages)-1]
}

// clearPages leaves the stack (the user moved to another screen, or searched).
func (m Model) clearPages() Model {
	m.pages = nil
	return m.stopPage()
}

func (m Model) stopPage() Model {
	m.stopCell.stop()
	return m
}

// pushPage opens a page over the screen; the request that fills it (cmd) is the one request in flight.
func (m Model) pushPage(p *pageView) Model {
	if len(m.pages) == 0 {
		m.pagesTab = m.tab
	}
	m.pageGen++
	p.gen = m.pageGen
	if len(m.pages) >= pageStackMax { // the oldest goes: Esc does not need a trail of twelve
		m.pages = append([]*pageView(nil), m.pages[1:]...)
	}
	m.pages = append(append([]*pageView(nil), m.pages...), p)
	return m
}

// popPage goes back one page; the request of the one left stops.
func (m Model) popPage() Model {
	if len(m.pages) == 0 {
		return m
	}
	m = m.stopPage()
	m.pages = append([]*pageView(nil), m.pages[:len(m.pages)-1]...)
	return m
}

// replaceTop swaps the top page for another (a "go to" page that found its entity).
func (m Model) replaceTop(p *pageView) Model {
	m.pages = append([]*pageView(nil), m.pages...)
	m.pageGen++
	p.gen = m.pageGen
	m.pages[len(m.pages)-1] = p
	return m
}

func (m Model) navigator() (music.Navigator, bool) {
	n, ok := m.deps.Source.(music.Navigator)
	return n, ok
}

// openEntity opens the page of an album, artist or playlist.
func (m Model) openEntity(e music.Entity) (Model, tea.Cmd) {
	nav, ok := m.navigator()
	if !ok {
		m.status = "this source has no album or artist pages"
		return m, nil
	}
	m = m.stopPage()
	m = m.pushPage(&pageView{e: e, title: e.Title, sub: e.Subtitle, loading: true})
	return m, m.pageRequest(nav, e, m.pages[len(m.pages)-1].gen)
}

func (m Model) pageRequest(nav music.Navigator, e music.Entity, gen int) tea.Cmd {
	parent, stop := context.WithCancel(context.Background())
	m.stopCell.set(stop)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, libTimeout)
		defer cancel()
		p, err := nav.OpenPage(ctx, e)
		return pageOpenedMsg{gen: gen, page: p, err: err}
	}
}

// stopCell holds the cancel function of the request in flight for the page on top. The model is copied around by value, so the
// cell is shared between its copies.
type stopCell struct {
	mu sync.Mutex
	f  context.CancelFunc
}

func (c *stopCell) set(f context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.f != nil {
		c.f()
	}
	c.f = f
}

func (c *stopCell) stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.f != nil {
		c.f()
		c.f = nil
	}
}

// goTo is a "go to" key or quick command for a track: ask what YouTube Music says about it, then open the page.
func (m Model) goTo(kind string, t music.Track) (Model, tea.Cmd) {
	nav, ok := m.navigator()
	if !ok {
		m.status = "this source has no artist or album pages"
		return m, nil
	}
	if t.ID == "" {
		m.status = "nothing selected to go from"
		return m, nil
	}
	m = m.stopPage()
	label := map[string]string{"artist": "Artist of ", "album": "Album of ", "radio": "Radio: ", "related": "Related: "}[kind]
	m = m.pushPage(&pageView{e: music.Entity{Kind: kind, Title: label + t.Title}, title: label + clean(t.Title), loading: true})
	gen := m.pages[len(m.pages)-1].gen
	if w, ok := m.watches[t.ID]; ok { // asked before: no request
		return m.watched(watchMsg{gen: gen, kind: kind, track: t, w: w})
	}
	parent, stop := context.WithCancel(context.Background())
	m.stopCell.set(stop)
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, libTimeout)
		defer cancel()
		w, err := nav.Watch(ctx, t.ID)
		return watchMsg{gen: gen, kind: kind, track: t, w: w, err: err}
	}
}

func (m Model) topIs(gen int) bool {
	return len(m.pages) > 0 && m.pages[len(m.pages)-1].gen == gen
}

// fail leaves a page that could not be opened and says why.
func (m Model) fail(why string) Model {
	m = m.popPage()
	m.status = why
	return m
}

// watched is the answer of goTo: the page to open from what YouTube Music says about the track.
func (m Model) watched(msg watchMsg) (Model, tea.Cmd) {
	if !m.topIs(msg.gen) {
		return m, nil // left in the meantime
	}
	if msg.err != nil {
		return m.fail(strings.TrimSpace(clean(msg.err.Error()))), nil
	}
	if len(m.watches) >= 64 {
		m.watches = map[string]music.Watch{}
	}
	m.watches[msg.track.ID] = msg.w
	w, t := msg.w, msg.track
	switch msg.kind {
	case "artist":
		switch len(w.Artists) {
		case 0:
			return m.fail("YouTube Music names no artist page for " + clean(t.Title)), nil
		case 1:
			return m.openInPlace(w.Artists[0])
		}
		p := &pageView{e: music.Entity{Kind: "artist", Title: "Artists of " + t.Title}, title: "Artists of " + clean(t.Title), secs: []music.Section{{Title: "Artists", Entities: w.Artists}}}
		p.build()
		return m.replaceTop(p), nil
	case "album":
		if w.Album.ID == "" {
			return m.fail(clean(t.Title) + " is not on an album (YouTube Music lists it as a video)"), nil
		}
		return m.openInPlace(w.Album)
	case "radio":
		if len(w.Radio) == 0 {
			return m.fail("YouTube Music has no radio for " + clean(t.Title)), nil
		}
		p := &pageView{e: music.Entity{Kind: "radio", ID: t.ID, Title: "Radio: " + t.Title}, title: "Radio: " + clean(t.Title), sub: "YouTube Music's mix for this track", secs: []music.Section{{Title: "Radio", Tracks: w.Radio}}}
		p.build()
		return m.replaceTop(p).pumped()
	case "related":
		if w.RelatedID == "" {
			return m.fail("YouTube Music has nothing related for " + clean(t.Title)), nil
		}
		return m.openInPlace(music.Entity{Kind: "related", ID: w.RelatedID, Title: "Related: " + t.Title})
	}
	return m.fail("unknown page"), nil
}

// openInPlace turns the loading page on top into the page of an entity, and asks for it.
func (m Model) openInPlace(e music.Entity) (Model, tea.Cmd) {
	nav, ok := m.navigator()
	if !ok {
		return m.fail("this source has no album or artist pages"), nil
	}
	m = m.replaceTop(&pageView{e: e, title: clean(e.Title), sub: e.Subtitle, loading: true})
	return m, m.pageRequest(nav, e, m.pages[len(m.pages)-1].gen)
}

// pageOpened is the answer for an entity's page.
func (m Model) pageOpened(msg pageOpenedMsg) (Model, tea.Cmd) {
	if !m.topIs(msg.gen) {
		return m, nil
	}
	if msg.err != nil {
		return m.fail(strings.TrimSpace(clean(msg.err.Error()))), nil
	}
	top := m.pages[len(m.pages)-1]
	p := &pageView{e: top.e, title: top.title, sub: top.sub, secs: msg.page.Sections}
	if msg.page.Title != "" {
		p.title = clean(msg.page.Title)
	}
	if msg.page.Subtitle != "" {
		p.sub = clean(msg.page.Subtitle)
	}
	p.build()
	return m.replaceTop(p).pumped() // an artist's top songs come without a length
}

// goKey is the keys A, B, R, T and the quick commands: from the track under the cursor.
func (m Model) goKey(k string) (tea.Model, tea.Cmd, bool) {
	kind, ok := goKinds[k]
	if !ok {
		return m, nil, false
	}
	t, ok := m.selectedTrack()
	if !ok {
		m.status = "nothing selected to go from"
		return m, nil, true
	}
	next, cmd := m.goTo(kind, t)
	return next, cmd, true
}

// selectedTrack is the track the "go to" keys act on: the one under the cursor of the screen (of the page on top, when there
// is one), the playing one on the Player screen while the lyrics fill the queue's place (on a wide stage the queue rail is
// beside them, and its cursor counts).
func (m Model) selectedTrack() (music.Track, bool) {
	if p := m.topPage(); p != nil {
		return p.track()
	}
	switch m.tab {
	case tabSearch:
		if m.homeShown() {
			return m.homeTrack()
		}
		if (m.entKind == "" || m.entKind == "songs") && m.resCur < len(m.results) {
			return m.results[m.resCur], true
		}
		return music.Track{}, false
	case tabLibrary:
		if m.lib.state == libReady && m.lib.open != nil && m.lib.tcur < len(m.lib.tracks) {
			return m.lib.tracks[m.lib.tcur], true
		}
		return music.Track{}, false
	}
	_, _, rail := m.stageWidths()
	queueShown := !m.lyricsVisible() || rail > 0 // the queue, or its rail beside the lyrics on a wide stage
	if queueShown && m.queueCur >= 0 && m.queueCur < len(m.state.Queue) {
		return m.state.Queue[m.queueCur], true
	}
	if m.state.Track != nil {
		return *m.state.Track, true
	}
	return music.Track{}, false
}

// pageKey handles the keys of the page on top. handled is false for the keys that mean the same on every screen (space,
// volume, tab, q).
func (m Model) pageKey(k string) (tea.Model, tea.Cmd, bool) {
	p := m.topPage()
	if p == nil {
		return m, nil, false
	}
	switch k {
	case "esc", "backspace":
		return m.popPage(), nil, true
	case "down", "j", "up", "k":
		cp := *p
		dir := 1
		if k == "up" || k == "k" {
			dir = -1
		}
		cp.move(dir)
		m = m.replaceTopKeepGen(&cp)
		next, cmd := m.pump()
		return next, cmd, true
	case "enter":
		r, ok := p.row()
		if !ok || p.loading {
			return m, nil, true
		}
		if r.kind == rowEntity {
			next, cmd := m.openEntity(p.secs[r.sec].Entities[r.i])
			return next, cmd, true
		}
		return m.playPage(p, false)
	case "P":
		return m.playPage(p, false)
	case "S":
		return m.playPage(p, true)
	case "a":
		t, ok := p.track()
		if !ok {
			return m, nil, true
		}
		pl, t := m.deps.Player, m.fill(t)
		return m, op(func(ctx context.Context) error { return pl.Enqueue(ctx, t) }), true
	}
	return m.goKey(k)
}

func (m Model) replaceTopKeepGen(p *pageView) Model {
	m.pages = append([]*pageView(nil), m.pages...)
	m.pages[len(m.pages)-1] = p
	return m
}

// playPage plays the section under the cursor: from the cursor's row on Enter, from its start for P, shuffled for S.
func (m Model) playPage(p *pageView, shuffle bool) (tea.Model, tea.Cmd, bool) {
	sec, at := p.section()
	if len(sec.Tracks) == 0 || p.loading {
		return m, nil, true
	}
	tracks := m.filled(sec.Tracks)
	if shuffle {
		tracks = append([]music.Track(nil), tracks...)
		rand.Shuffle(len(tracks), func(i, j int) { tracks[i], tracks[j] = tracks[j], tracks[i] })
		at = 0
	} else if _, ok := p.row(); !ok {
		at = 0
	}
	pl := m.deps.Player
	m = m.clearPages()
	m.tab = tabPlayer
	m.queueGen.Add(1)
	return m, tea.Batch(op(func(ctx context.Context) error { return pl.Replace(ctx, tracks, at) }), m.remember(music.Recent{Track: &tracks[at]})), true
}

// ── drawing ─────────────────────────────────────────────────────────────────

var tabNames = map[string]string{tabSearch: "Search", tabLibrary: "Library", tabPlayer: "Player"}

// breadcrumbs is "Search > Daft Punk > Random Access Memories", cut at the left to fit.
func (m Model) breadcrumbs() string {
	parts := []string{tabNames[m.pagesTab]}
	for _, p := range m.pages {
		parts = append(parts, clean(p.title))
	}
	s := strings.Join(parts, " › ")
	if r := []rune(s); len(r) > m.w-3 && m.w > 8 {
		s = "…" + string(r[len(r)-(m.w-4):])
	}
	return " " + dim.Render(s)
}

// pageBody is pageLines with its parts: the breadcrumbs and the title are text, the page's rows a list.
func (m Model) pageBody(s *sheet, rows int) {
	lines := m.pageLines(rows)
	p := m.topPage()
	if p.loading || len(p.rows) == 0 {
		s.add(PartText, lines...)
		return
	}
	first, end := window(len(p.rows), p.cur, rows-2) // as pageLines shows them, below its two
	items := make([]ListItem, 0, end-first)
	heads := make([]bool, 0, end-first)
	for _, r := range p.rows[first:end] {
		sec := p.secs[r.sec]
		heads = append(heads, r.kind == rowHead)
		switch r.kind {
		case rowHead:
			items = append(items, ListItem{Label: clean(sec.Title), Detail: fmt.Sprint(len(sec.Tracks) + len(sec.Entities))})
		case rowTrack:
			t := m.fill(sec.Tracks[r.i])
			l := ""
			if t.Duration > 0 {
				l = clock(t.Duration)
			}
			items = append(items, ListItem{Label: clean(t.Title), Detail: clean(strings.Join(t.Artists, ", ")), Columns: []string{clean(t.Album), l}})
		case rowEntity:
			e := sec.Entities[r.i]
			items = append(items, ListItem{Label: clean(e.Title), Detail: clean(e.Subtitle)})
		}
	}
	s.add(PartText, lines[:2]...)
	l := listAt(items, first, p.cur)
	l.heads = heads
	s.listRows(lines[2:], l)
}

func (m Model) pageLines(rows int) []string {
	p := m.topPage()
	out := []string{m.breadcrumbs(), " " + bold.Render(clean(p.title))}
	if p.sub != "" {
		out[1] += dim.Render("  " + clean(p.sub))
	}
	rows -= len(out)
	if p.loading {
		return append(out, "", dim.Render(" Loading..."))
	}
	if len(p.rows) == 0 {
		return append(out, "", dim.Render(" Nothing on this page."))
	}
	var anyAlbum, anyExplicit bool
	for _, s := range p.secs {
		for _, t := range s.Tracks {
			anyAlbum = anyAlbum || (t.Album != "" && p.e.Kind != "album")
			anyExplicit = anyExplicit || t.Explicit
		}
	}
	plan := columnPlan(m.w, anyAlbum, anyExplicit)
	playing := ""
	if m.state.Track != nil {
		playing = m.state.Track.ID
	}
	first, end := window(len(p.rows), p.cur, rows)
	for i := first; i < end; i++ {
		r := p.rows[i]
		sec := p.secs[r.sec]
		var line string
		switch r.kind {
		case rowHead:
			line = " " + accent.Render(clean(sec.Title)) + dim.Render(fmt.Sprintf("  %d", len(sec.Tracks)+len(sec.Entities)))
		case rowTrack:
			t := m.fill(sec.Tracks[r.i])
			mark := " "
			if t.ID == playing {
				mark = ">"
			}
			l := ""
			if t.Duration > 0 {
				l = clock(t.Duration)
			}
			badge := ""
			if t.Explicit {
				badge = "E"
			}
			line = mark + pad(clean(t.Title), plan.title)
			if plan.explicit {
				line += " " + pad(badge, 1)
			}
			if plan.artist > 0 {
				line += " " + pad(clean(strings.Join(t.Artists, ", ")), plan.artist)
			}
			if plan.album > 0 {
				line += " " + pad(clean(t.Album), plan.album)
			}
			line += " " + pad(l, 5)
		case rowEntity:
			e := sec.Entities[r.i]
			titleW := max(min(m.w/2, 44), 12)
			line = " " + pad(clean(e.Title), titleW) + " " + dim.Render(clean(e.Subtitle))
		}
		if i == p.cur {
			line = reverse.Render(pad(line, m.w))
		}
		out = append(out, line)
	}
	return out
}

// wantedOnPage is the tracks of the page on top, from the cursor on, for the background lookups of lengths.
func (m Model) wantedOnPage() []music.Track {
	p := m.topPage()
	if p == nil {
		return nil
	}
	var all []music.Track
	at := 0
	for s, sec := range p.secs {
		for i, t := range sec.Tracks {
			if r, ok := p.row(); ok && r.kind == rowTrack && r.sec == s && r.i == i {
				at = len(all)
			}
			all = append(all, t)
		}
	}
	return append(append([]music.Track(nil), all[at:]...), all[:at]...)
}

// goMsg is /music artist, album, radio or related: the playing track's page, opened over the Player screen.
func (m Model) goMsg(msg GoMsg) (tea.Model, tea.Cmd) {
	t := m.state.Track
	if t == nil {
		t = msg.Track
	}
	if t == nil {
		m.status = "nothing is playing to go from"
		return m, nil
	}
	m = m.clearPages()
	m.tab = tabPlayer
	return m.goTo(msg.Kind, *t)
}

// pumped is pump with the model's own type.
func (m Model) pumped() (Model, tea.Cmd) {
	next, cmd := m.pump()
	return next.(Model), cmd
}
