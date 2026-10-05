package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

type libState int

const (
	libIdle libState = iota
	libLoading
	libReady
	libConsent  // waiting for the user's yes or no to reading the browser's cookies
	libDeclined // the user said no; r asks again
	libProblem  // no browser, or an error: shown as it is
)

// library is the Library screen: the account's collections, and one opened collection's tracks.
type library struct {
	state   libState
	cols    []music.Collection
	cur     int
	problem string
	consent *music.NeedsConsentError

	open    *music.Collection // the collection being looked into, nil on the list
	tracks  []music.Track
	tcur    int
	loading bool // the opened collection's tracks are on their way
	more    bool // the source has more pages of the opened collection
	paging  bool // a page is on its way

	refreshing bool // the collections shown are the cache of an earlier session; the account's answer is on its way
	tstale     bool // the same for the opened collection's tracks
}

const (
	// libPage is how many tracks of a collection are fetched at a time (a Source that pages), and libPageAhead how close to
	// the end of what is loaded the cursor gets before the next page is fetched.
	libPage      = 50
	libPageAhead = 10
)

type libraryDoneMsg struct {
	cols []music.Collection
	err  error
}

type libTracksMsg struct {
	id     string
	tracks []music.Track
	err    error
	from   int  // the index of tracks[0] in the collection
	more   bool // a pager says more follow
	paged  bool // the answer came from a TrackPager
	cached bool // the answer is the cache of an earlier session (a LibraryCache), not the account's
}

// libCachedMsg is the cache's answer to opening the Library: what the last session left on disk.
type libCachedMsg struct{ cols []music.Collection }

type grantedMsg struct{ err error }

func (m Model) libraryCmd() tea.Cmd {
	src := m.deps.Source
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), libTimeout)
		defer cancel()
		cols, err := src.Library(ctx)
		return libraryDoneMsg{cols, err}
	}
}

// loadLibrary starts listing the library; the first visit and r do it. When the Source keeps a cache of the last session and
// useCache is set, the cache is asked at the same time: its rows show at once and the account's answer replaces them.
func (m Model) loadLibrary(useCache bool) (Model, tea.Cmd) {
	m.lib = library{state: libLoading}
	if c, ok := m.deps.Source.(music.LibraryCache); ok && useCache {
		cached := func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
			defer cancel()
			cols, _ := c.CachedLibrary(ctx)
			return libCachedMsg{cols}
		}
		return m, tea.Batch(cached, m.libraryCmd())
	}
	return m, m.libraryCmd()
}

// libraryCached shows the rows of the cache while the account has not answered; it never replaces what the account gave.
func (m Model) libraryCached(msg libCachedMsg) Model {
	if m.lib.state == libLoading && len(msg.cols) > 0 {
		m.lib = library{state: libReady, cols: msg.cols, refreshing: true}
	}
	return m
}

func (m Model) libraryDone(msg libraryDoneMsg) Model {
	var need *music.NeedsConsentError
	switch {
	case errors.As(msg.err, &need):
		m.lib = library{state: libConsent, consent: need}
	case msg.err != nil && m.lib.state == libReady && m.lib.refreshing:
		m.lib.refreshing = false // the cached rows stay, and the error is named
		m.status = "could not update the library: " + msg.err.Error()
	case msg.err != nil:
		m.lib = library{state: libProblem, problem: msg.err.Error()}
	case m.lib.state == libReady && m.lib.refreshing:
		// diff-update: the account's rows replace the cached ones, the cursor stays on the same collection and an opened
		// collection stays open
		kept := m.lib
		kept.refreshing = false
		kept.cols = msg.cols
		kept.cur = 0
		if kept.cur < len(m.lib.cols) {
			for i, c := range msg.cols {
				if c.ID == m.lib.cols[m.lib.cur].ID {
					kept.cur = i
				}
			}
		}
		m.lib = kept
	default:
		m.lib = library{state: libReady, cols: msg.cols}
	}
	return m
}

func (m Model) libTracksDone(msg libTracksMsg) Model {
	if m.lib.open == nil || m.lib.open.ID != msg.id {
		return m // an answer for a collection that was left
	}
	if msg.cached { // the cache of an earlier session: only while nothing else has answered
		if m.lib.loading && msg.err == nil && len(msg.tracks) > 0 {
			m.lib.tracks, m.lib.tcur, m.lib.loading, m.lib.tstale = msg.tracks, 0, false, true
			m.lib.more, m.lib.paging = false, false // the next pages follow the account's answer, not the cache
		}
		return m
	}
	var need *music.NeedsConsentError
	switch {
	case errors.As(msg.err, &need):
		m.lib = library{state: libConsent, consent: need}
	case msg.err != nil && m.lib.tstale:
		m.lib.tstale = false // the cached rows stay and the error is named
		m.status = "could not update this collection: " + msg.err.Error()
	case msg.err != nil && msg.paged && msg.from > 0:
		m.lib.paging = false // the rows already shown stay; moving the cursor tries the page again
		m.status = msg.err.Error()
	case msg.err != nil:
		m.status = msg.err.Error()
		m.lib.open, m.lib.loading = nil, false
	case msg.paged && msg.from > 0:
		m.lib.paging = false
		if msg.from == len(m.lib.tracks) { // the page that follows what is shown; any other is stale
			m.lib.tracks = append(append([]music.Track(nil), m.lib.tracks...), msg.tracks...)
			m.lib.more = msg.more
		}
	default:
		cur := 0
		if m.lib.tstale && m.lib.tcur < len(m.lib.tracks) { // the cursor stays on the same track
			for i, t := range msg.tracks {
				if t.ID == m.lib.tracks[m.lib.tcur].ID {
					cur = i
				}
			}
		}
		m.lib.tracks, m.lib.tcur, m.lib.loading, m.lib.tstale = msg.tracks, cur, false, false
		m.lib.more, m.lib.paging = msg.paged && msg.more, false
	}
	return m
}

// nextPage fetches the next page of the opened collection when the cursor is near the end of what is loaded.
func (m Model) nextPage() (Model, tea.Cmd) {
	l := m.lib
	pager, ok := m.deps.Source.(music.TrackPager)
	if !ok || l.open == nil || !l.more || l.paging || l.loading || l.tstale || l.tcur < len(l.tracks)-libPageAhead {
		return m, nil
	}
	m.lib.paging = true
	id, from := l.open.ID, len(l.tracks)
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), libTimeout)
		defer cancel()
		tracks, more, err := pager.TracksPage(ctx, id, from, libPage)
		return libTracksMsg{id: id, tracks: tracks, err: err, from: from, more: more, paged: true}
	}
}

// libraryKey handles a key on the Library screen that the global keys did not take.
func (m Model) libraryKey(k string) (tea.Model, tea.Cmd) {
	l := m.lib
	switch {
	case k == "r" && l.state != libLoading:
		if r, ok := m.deps.Source.(music.Refresher); ok {
			r.Refresh() // r means ask the account again, not show what the session remembers
		}
		return m.loadLibrary(false)
	case l.state == libConsent && k == "y":
		src, ok := m.deps.Source.(music.CookieConsenter)
		if !ok {
			m.lib = library{state: libProblem, problem: "this source cannot record your consent"}
			return m, nil
		}
		browser := l.consent.Browser // the yes is for the browser the prompt named, and only for it
		m.lib.state = libLoading
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
			defer cancel()
			return grantedMsg{src.GrantCookieAccess(ctx, browser)}
		}
	case l.state == libConsent && k == "n":
		m.lib = library{state: libDeclined}
	case l.state == libReady && l.open == nil:
		switch k {
		case "down", "j":
			m.lib.cur = clamp(l.cur+1, len(l.cols))
		case "up", "k":
			m.lib.cur = clamp(l.cur-1, len(l.cols))
		case "enter":
			if len(l.cols) == 0 {
				return m, nil
			}
			col := l.cols[l.cur]
			m.lib.open, m.lib.tracks, m.lib.loading, m.lib.more, m.lib.paging = &col, nil, true, false, false
			src := m.deps.Source
			live := func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), libTimeout)
				defer cancel()
				if pager, ok := src.(music.TrackPager); ok { // the first page shows at once; the rest follows the cursor
					tracks, more, err := pager.TracksPage(ctx, col.ID, 0, libPage)
					return libTracksMsg{id: col.ID, tracks: tracks, err: err, more: more, paged: true}
				}
				tracks, err := src.Tracks(ctx, col.ID)
				return libTracksMsg{id: col.ID, tracks: tracks, err: err}
			}
			if c, ok := src.(music.LibraryCache); ok {
				cached := func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
					defer cancel()
					tracks, _, found := c.CachedTracks(ctx, col.ID, libPage)
					if !found {
						return libTracksMsg{id: col.ID, cached: true}
					}
					return libTracksMsg{id: col.ID, tracks: tracks, cached: true}
				}
				return m, tea.Batch(cached, live)
			}
			return m, live
		}
	case l.state == libReady && l.open != nil:
		n := len(l.tracks)
		p := m.deps.Player
		switch k {
		case "down", "j":
			m.lib.tcur = clamp(l.tcur+1, n)
			return m.moved()
		case "up", "k":
			m.lib.tcur = clamp(l.tcur-1, n)
			return m.moved()
		case "backspace":
			m.lib.open, m.lib.tracks = nil, nil
		case "enter":
			if n == 0 {
				return m, nil
			}
			tracks, at := m.filled(l.tracks), l.tcur
			m.tab = tabPlayer
			gen := m.queueGen.Add(1)
			if pager, ok := m.deps.Source.(music.TrackPager); ok && l.more {
				return m, replaceAndFollow(p, pager, l.open.ID, tracks, at, gen, m.queueGen)
			}
			return m, op(func(ctx context.Context) error { return p.Replace(ctx, tracks, at) })
		case "a":
			if n == 0 {
				return m, nil
			}
			t := m.fill(l.tracks[l.tcur])
			return m, op(func(ctx context.Context) error { return p.Enqueue(ctx, t) })
		}
	}
	return m, nil
}

// ── view ────────────────────────────────────────────────────────────────────

func (m Model) libraryLines(rows int) []string {
	l := m.lib
	switch l.state {
	case libConsent:
		return m.consentLines()
	case libDeclined:
		return []string{" " + bold.Render("Library"), "", " Reading your browser's cookies is not allowed, so the library is empty.", dim.Render(" Press r to be asked again.")}
	case libProblem:
		return append([]string{" " + bold.Render("Library"), ""}, m.wrapped(clean(l.problem))...)
	case libReady:
		if l.open != nil {
			return m.openedLines(rows)
		}
		return m.collectionLines(rows)
	}
	return []string{" " + bold.Render("Library"), "", dim.Render(" Loading...")}
}

// wrapped wraps s to the width, with a one-cell margin on every line.
func (m Model) wrapped(s string) []string {
	lines := strings.Split(ansi.Wrap(strings.TrimSpace(s), max(m.w-2, 1), ""), "\n")
	for i := range lines {
		lines[i] = " " + lines[i]
	}
	return lines
}

func (m Model) consentLines() []string {
	c := m.lib.consent
	paras := []string{
		"pig-music needs your OK before it reads your YouTube Music account.",
		"To list your library, yt-dlp would read the cookies of " + clean(c.Description) + ".",
		"It opens that browser's whole cookie store for each library request, in memory only; pig-music saves no cookie and never sees one. Only the library listing uses it: search, playback and mpv never use cookies.",
		clean(c.Notes),
	}
	out := []string{" " + bold.Render("Library: your playlists and liked songs"), ""}
	for _, p := range paras {
		if p == "" {
			continue
		}
		for _, line := range m.wrapped(p) {
			out = append(out, line)
		}
		out = append(out, "")
	}
	return append(out, " "+accent.Render("[y]")+" allow reading "+clean(c.Browser)+"    "+accent.Render("[n]")+" not now")
}

func (m Model) collectionLines(rows int) []string {
	l := m.lib
	out := []string{" " + bold.Render("Library") + updating(l.refreshing)}
	if len(l.cols) == 0 {
		return append(out, "", dim.Render(" Nothing in your library."))
	}
	first, end := window(len(l.cols), l.cur, rows-1)
	for i := first; i < end; i++ {
		c := l.cols[i]
		count := ""
		if c.Count > 0 {
			count = fmt.Sprintf("%d", c.Count)
		}
		row := fmt.Sprintf(" %-*s %6s", max(m.w-9, 1), clean(c.Title), count)
		if i == l.cur {
			row = reverse.Render(pad(row, m.w))
		}
		out = append(out, row)
	}
	return out
}

func (m Model) openedLines(rows int) []string {
	l := m.lib
	out := []string{" " + bold.Render(clean(l.open.Title)) + dim.Render("  esc: back") + updating(l.tstale || l.refreshing)}
	if l.loading {
		return append(out, "", dim.Render(" Loading..."))
	}
	return append(out, m.trackTable(m.filled(l.tracks), l.tcur, -1, rows-1)...)
}

// updating is the marker beside a heading whose rows are the cache of an earlier session while the account's answer is on its
// way.
func updating(on bool) string {
	if !on {
		return ""
	}
	return dim.Render("  updating...")
}

// window returns the slice of n rows, visible at a time, that keeps cur in view.
func window(n, cur, visible int) (first, end int) {
	if visible < 1 {
		return 0, 0
	}
	if n <= visible {
		return 0, n
	}
	first = min(max(cur-visible/2, 0), n-visible)
	return first, first + visible
}

// moved is what follows the cursor moving in an opened collection: the rows now near it are filled in, and the next page is
// fetched when it nears the end of what is loaded.
func (m Model) moved() (tea.Model, tea.Cmd) {
	m, page := m.nextPage()
	next, pump := m.pump()
	return next, tea.Batch(page, pump)
}

// libFill is how many tracks follow the loaded ones in one request when a collection is played whole.
const libFill = 200

// replaceAndFollow plays what is loaded at once, then fetches the rest of the collection a page at a time and adds it to the
// queue, so that playing a long collection queues all of it, as it did before the library was paged. A later choice of what
// to play (anything that takes the next number from gen) ends it: the pages of the earlier collection are not added to the
// later queue.
func replaceAndFollow(p music.Player, pager music.TrackPager, id string, loaded []music.Track, at int, mine uint64, gen *atomic.Uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		err := p.Replace(ctx, loaded, at)
		cancel()
		for from := len(loaded); err == nil && gen.Load() == mine; {
			pctx, pcancel := context.WithTimeout(context.Background(), libTimeout)
			var more bool
			var page []music.Track
			page, more, err = pager.TracksPage(pctx, id, from, libFill)
			if err == nil && len(page) > 0 && gen.Load() == mine {
				err = p.Enqueue(pctx, page...)
			}
			pcancel()
			if !more || len(page) == 0 {
				break
			}
			from += len(page)
		}
		return opDoneMsg{err}
	}
}
