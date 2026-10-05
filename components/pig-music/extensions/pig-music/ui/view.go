package ui

import (
	"fmt"
	"math"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/art"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

const (
	minWidth  = 24
	minHeight = 6
)

var (
	bold    = lipgloss.NewStyle().Bold(true)
	dim     = lipgloss.NewStyle().Faint(true)
	reverse = lipgloss.NewStyle().Reverse(true)
	accent  = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(5)).Bold(true)
	errored = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(1))
)

// View draws the screen: exactly the terminal's height in lines, none wider than its width.
func (m Model) View() tea.View {
	return tea.NewView(strings.Join(m.lines(), "\n"))
}

func (m Model) lines() []string {
	w, h := m.w, m.h
	if w < 1 || h < 1 {
		return []string{""}
	}
	if w < minWidth || h < minHeight {
		out := blank(w, h)
		out[0] = pad("terminal too small", w)
		return out
	}
	out := make([]string, 0, h)
	out = append(out, m.header(), m.tabBar(), dim.Render(strings.Repeat("─", w)))
	body := m.body(h - 6)
	for len(body) < h-6 {
		body = append(body, "")
	}
	for _, l := range body[:h-6] {
		out = append(out, pad(l, w))
	}
	out = append(out, m.playBar(), m.statusLine(), m.hints())
	switch {
	case m.vibes():
		p := m.pal.Pulsed(m.beat) // once for the whole screen
		for i, l := range out {
			out[i] = m.paint(p, i, len(out), l)
		}
	case m.deps.NoColor:
		for i, l := range out {
			out[i] = uncolour(l)
		}
	}
	return out
}

// uncolour makes the accent and error styles bold instead of coloured, for a user who asked for no colour.
func uncolour(l string) string {
	for _, open := range []string{accentOpen, erroredOpen} {
		if open != "" {
			l = strings.ReplaceAll(l, open, "\x1b[1m")
		}
	}
	return l
}

// The escapes the shared styles open with, found by rendering, so the palette can swap them for its own colours.
var accentOpen, dimOpen, erroredOpen = openSeq(accent), openSeq(dim), openSeq(errored)

func openSeq(s lipgloss.Style) string {
	r := s.Render("x")
	if i := strings.Index(r, "x"); i > 0 {
		return r[:i]
	}
	return ""
}

// paint puts one line on its row of the palette's gradient: the palette's text colour and background open the line and come
// back after every reset inside it, and the faint, accent and error styles are swapped for palette colours whose contrast was
// checked (the faint attribute would lower it by an unknown amount). It is a no-op in monochrome and with the palette off.
// The colours are drawn in the overlay's own cells only: nothing here touches the terminal's background.
func (m Model) paint(p art.Palette, row, rows int, l string) string {
	mode := m.deps.ArtMode
	if !m.vibes() {
		return l
	}
	base := "\x1b[" + p.Text.TextSGR(mode) + ";" + p.At(row, rows).BackSGR(mode) + "m"
	swap := func(s, open, with string) string {
		if open == "" {
			return s
		}
		return strings.ReplaceAll(s, open, with)
	}
	l = swap(l, dimOpen, "\x1b["+p.Dim.TextSGR(mode)+"m")
	l = swap(l, accentOpen, "\x1b[1;"+p.Accent.TextSGR(mode)+"m")
	l = swap(l, erroredOpen, "\x1b["+p.Error.TextSGR(mode)+"m")
	l = strings.ReplaceAll(l, "\x1b[m", "\x1b[m"+base)
	l = strings.ReplaceAll(l, "\x1b[0m", "\x1b[0m"+base)
	return base + l + "\x1b[m"
}

func blank(w, h int) []string {
	out := make([]string, h)
	for i := range out {
		out[i] = pad("", w)
	}
	return out
}

// pad clips s to w cells and pads it with spaces to exactly w. A wide character that would straddle the edge is dropped
// and its place filled, so the columns after it stay where they are.
func pad(s string, w int) string {
	if w < 1 {
		return ""
	}
	cw := ansi.StringWidth(s)
	if cw > w {
		s = ansi.Truncate(s, w, "")
		cw = ansi.StringWidth(s)
	}
	if cw < w {
		s += strings.Repeat(" ", w-cw)
	}
	return s
}

func (m Model) header() string {
	word := "Idle"
	title := ""
	if t := m.state.Track; t != nil {
		word = "Playing"
		if m.state.Paused {
			word = "Paused"
		}
		title = "  " + describe(*t)
	}
	if !m.state.Connected {
		// Before the first state, or after mpv went away: /music attaches again.
		word, title = "Stopped", "  mpv is not running; press q and run /music to start it again"
	}
	right := fmt.Sprintf("vol %d%% ", m.state.Volume)
	left := " " + accent.Render(word) + title
	room := m.w - ansi.StringWidth(right)
	return pad(left, room) + dim.Render(right)
}

func (m Model) tabBar() string {
	var b strings.Builder
	b.WriteString(" ")
	for _, t := range tabOrder {
		name := strings.ToUpper(t[:1]) + t[1:]
		if t == m.tab {
			b.WriteString(reverse.Render(" " + name + " "))
		} else {
			b.WriteString(dim.Render(" " + name + " "))
		}
		b.WriteString(" ")
	}
	return pad(b.String(), m.w)
}

func describe(t music.Track) string {
	if len(t.Artists) == 0 {
		return clean(t.Title)
	}
	return clean(t.Title + " - " + strings.Join(t.Artists, ", "))
}

// clean makes text from outside (titles, artists and albums from yt-dlp and mpv,
// error messages, typed and pasted input) safe to draw: the host paints lines as
// they are, so a control character would reach the terminal (an escape sequence
// from the network, or a newline that splits the layout). Whitespace controls
// become a space and every other control character U+FFFD. The bidirectional embeddings, overrides and isolates
// (U+202A-U+202E, U+2066-U+2069) are dropped: unbalanced, or cut off by a column's edge, they would turn the rest of the row
// around in a terminal that does bidi. The marks (U+200E, U+200F) stay: they change only the text next to them.
func clean(s string) string {
	ok := true
	for _, r := range s {
		if unicode.IsControl(r) || bidiControl(r) {
			ok = false
			break
		}
	}
	if ok {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f':
			return ' '
		case unicode.IsControl(r):
			return '\uFFFD'
		case bidiControl(r):
			return -1
		}
		return r
	}, s)
}

func bidiControl(r rune) bool {
	return (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069')
}

func (m Model) body(rows int) []string {
	if m.settings.open {
		return m.settingsLines()
	}
	if m.help {
		return m.helpLines()
	}
	switch m.tab {
	case tabLibrary:
		return m.libraryLines(rows)
	case tabPlayer:
		return m.playerLines(rows)
	}
	return m.searchLines(rows)
}

// ── search ──────────────────────────────────────────────────────────────────

func (m Model) searchLines(rows int) []string {
	var prompt string
	switch {
	case m.typing:
		prompt = " / " + clean(m.query) + reverse.Render(" ")
	case m.loading:
		prompt = fmt.Sprintf(" Searching for %q...", clean(m.searched))
	case m.searched == "":
		return []string{
			dim.Render(" Press / to search YouTube Music."), "",
			dim.Render(" Then type a song or artist, press enter to search,"),
			dim.Render(" and press enter on a result to play it."),
		}
	case len(m.results) == 0:
		prompt = fmt.Sprintf(" No results for %q", clean(m.searched))
	default:
		prompt = fmt.Sprintf(" Results for %q", clean(m.searched))
	}
	if m.ent != nil && !m.typing { // a new search typed over an opened album shows its prompt; esc goes back to the album
		return m.entityPageLines(rows)
	}
	out := []string{prompt}
	if tabs := m.entityTabs(); tabs != "" {
		out = append(out, tabs)
		rows--
	}
	if m.entKind == "albums" || m.entKind == "artists" {
		return append(out, m.entityListLines(rows-1)...)
	}
	if len(m.results) == 0 {
		return out
	}
	return append(out, m.trackTable(m.filled(m.results), m.resCur, -1, rows-1)...)
}

// ── library ─────────────────────────────────────────────────────────────────

// ── player ──────────────────────────────────────────────────────────────────

func (m Model) playerLines(rows int) []string {
	q := m.state.Queue
	if m.w < 60 {
		return append([]string{" " + bold.Render("Up Next")}, m.trackTable(m.filled(q), m.queueCur, m.state.Index, rows-1)...)
	}
	leftW := min(max(m.w/3, 22), 38)
	left := m.nowPlaying(leftW, rows)
	right := append([]string{" " + bold.Render("Up Next")}, m.trackTable(m.filled(q), m.queueCur, m.state.Index, rows-1)...)
	rightW := m.w - leftW - 1
	out := make([]string, 0, rows)
	for i := 0; i < rows; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, pad(l, leftW)+dim.Render("│")+pad(r, rightW))
	}
	return out
}

// nowPlaying is the left panel: a heading, the cover or the disc when there is room, then the track's text.
func (m Model) nowPlaying(w, rows int) []string {
	out := []string{" " + bold.Render("Now Playing"), ""}
	text := m.nowText()
	if pic := m.cover(w, rows-len(out)-len(text)-1); pic != nil {
		out = append(append(out, pic...), "")
	}
	return append(out, text...)
}

func (m Model) nowText() []string {
	var out []string
	t := m.state.Track
	if t == nil {
		return append(out, dim.Render(" Nothing playing"), dim.Render(" Search with / and press enter"))
	}
	f := m.fill(*t)
	title := bold
	if m.vibes() {
		title = accent // the title takes the accent of the palette
	}
	out = append(out, " "+title.Render(clean(f.Title)))
	if len(f.Artists) > 0 {
		out = append(out, " "+clean(strings.Join(f.Artists, ", ")))
	}
	if f.Album != "" {
		out = append(out, " "+dim.Render(clean(f.Album)))
	}
	if f.Duration > 0 {
		out = append(out, " "+dim.Render("Length "+clock(f.Duration)))
	}
	if m.pulsing() && m.haveLevel {
		out = append(out, " "+meter(m.rms, 14))
	}
	out = append(out, "", dim.Render(fmt.Sprintf(" %d of %d in the queue", m.state.Index+1, len(m.state.Queue))))
	if _, ok := m.deps.Player.(music.Modes); ok {
		shuffle, repeat := "off", "off"
		if m.state.Shuffle {
			shuffle = "on"
		}
		if m.state.Repeat != music.RepeatOff {
			repeat = string(m.state.Repeat)
		}
		out = append(out, "", " Shuffle: "+shuffle, " Repeat: "+repeat)
	}
	return out
}

// ── tables ──────────────────────────────────────────────────────────────────

// trackTable draws a header and rows of tracks, scrolled so that cur is visible.
// playing marks the current track's row with ">"; max is the number of lines available.
func (m Model) trackTable(ts []music.Track, cur, playing, max int) []string {
	if max < 2 || len(ts) == 0 {
		if len(ts) == 0 && max >= 1 {
			return []string{dim.Render("  The queue is empty. Search with / and press enter.")}
		}
		return nil
	}
	avail := m.w
	if m.tab == tabPlayer && m.w >= 60 {
		avail = m.w - min(maxInt(m.w/3, 22), 38) - 1
	}
	var anyAlbum, anyExplicit bool
	for _, t := range ts {
		anyAlbum = anyAlbum || t.Album != ""
		anyExplicit = anyExplicit || t.Explicit
	}
	plan := columnPlan(avail, anyAlbum, anyExplicit)
	const numW, lenW = 4, 5
	line := func(num, title, badge, artist, album, length string) string {
		s := " " + pad(num, numW) + " " + pad(title, plan.title)
		if plan.explicit {
			s += " " + pad(badge, 1)
		}
		if plan.artist > 0 {
			s += " " + pad(artist, plan.artist)
		}
		if plan.album > 0 {
			s += " " + pad(album, plan.album)
		}
		return s + " " + pad(length, lenW)
	}
	out := []string{dim.Render(line("#", "Title", "", "Artist", "Album", "Len"))}
	visible := max - 1
	first := 0
	if len(ts) > visible {
		first = minInt(maxInt(cur-visible/2, 0), len(ts)-visible)
	}
	end := minInt(first+visible, len(ts))
	more := len(ts) - end
	if more > 0 && end > first {
		end--
		more++
	}
	for i := first; i < end; i++ {
		t := ts[i]
		num := fmt.Sprintf("%d.", i+1)
		if i == playing {
			num = ">" + num
		}
		l := ""
		if t.Duration > 0 {
			l = clock(t.Duration)
		}
		badge := ""
		if t.Explicit {
			badge = "E"
		}
		row := line(num, clean(t.Title), badge, clean(strings.Join(t.Artists, ", ")), clean(t.Album), l)
		if i == cur {
			row = reverse.Render(pad(row, avail))
		}
		out = append(out, row)
	}
	if more > 0 {
		out = append(out, dim.Render(fmt.Sprintf("  + %d more", more)))
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── bottom rows ─────────────────────────────────────────────────────────────

func (m Model) playBar() string {
	icon := ">"
	if m.state.Paused {
		icon = "|"
	}
	pos, dur := clock(m.state.Position), clock(m.state.Duration)
	barW := m.w - ansi.StringWidth(pos) - ansi.StringWidth(dur) - 7
	if barW < 4 {
		return pad(" "+icon+" "+pos+"/"+dur, m.w)
	}
	filled := 0
	if m.state.Duration > 0 {
		filled = int(int64(barW) * int64(m.state.Position) / int64(m.state.Duration))
		filled = minInt(maxInt(filled, 0), barW)
	}
	bar := accent.Render(strings.Repeat("█", filled)) + dim.Render(strings.Repeat("░", barW-filled))
	return pad(" "+icon+" "+pos+" "+bar+" "+dur+" ", m.w)
}

func (m Model) statusLine() string {
	if m.status == "" {
		return pad("", m.w)
	}
	return pad(" "+errored.Render(clean(m.status)), m.w)
}

func (m Model) hints() string {
	hint := "space play  n/p skip  left/right seek  +/- vol  s shuffle  l repeat  / search  ? help  q hide"
	if m.typing {
		hint = "type to search  enter search  esc cancel"
	}
	return pad(" "+dim.Render(hint), m.w)
}

// ── help ────────────────────────────────────────────────────────────────────

func (m Model) helpLines() []string {
	rows := [][2]string{
		{"space", "play / pause"},
		{"n / p", "next / previous track"},
		{"left / right", "seek 5 seconds"},
		{"+ / -", "volume up / down"},
		{"s", "shuffle the queue on / off"},
		{"l", "repeat: off, the queue, one track"},
		{"tab / shift+tab", "next / previous screen"},
		{"/", "search YouTube Music"},
		{"up / down, j / k", "move the selection"},
		{"enter", "play the selected result, or jump to the selected queue entry"},
		{"1 / 2 / 3", "search results: songs / albums / artists (enter opens one, backspace goes back)"},
		{"a", "add the selected result to the queue"},
		{"x", "remove the selected queue entry"},
		{"[ / ]", "reorder: move the selected queue entry up / down"},
		{"o", "settings"},
		{"?", "this help"},
		{"q, esc, ctrl+c", "quit: hide the player; the music keeps playing"},
	}
	out := []string{" " + bold.Render("Keys"), ""}
	for _, r := range rows {
		out = append(out, fmt.Sprintf(" %-18s %s", r[0], r[1]))
	}
	return out
}

// Clean makes text from outside safe to put on a terminal line: see clean.
func Clean(s string) string { return clean(s) }

// meter is a slim level bar of w cells in the accent colour, filled in eighths: "Level" and the bar, 6+w cells wide.
func meter(level float64, w int) string {
	eighths := int(math.Round(math.Min(math.Max(level, 0), 1) * float64(w) * 8))
	full, part := eighths/8, eighths%8
	bar := strings.Repeat("█", full)
	if part > 0 {
		bar += string([]rune("▏▎▍▌▋▊▉")[part-1])
	}
	return dim.Render("Level ") + accent.Render(bar) + strings.Repeat(" ", w-ansi.StringWidth(bar))
}

// columns is how the width of a track table is shared out.
type columns struct {
	title, artist, album int  // widths; 0 means the column is not shown
	explicit             bool // a one-cell badge column after the title
}

// columnPlan shares avail cells between the title, artist and album columns. The album is the first to go when the screen is
// narrow (under 90 cells), then the artist (under 50); the title always keeps the rest and never less than one cell. When some
// track is explicit the badge takes two cells (a gap and the letter) from the title.
func columnPlan(avail int, anyAlbum, anyExplicit bool) columns {
	const overhead = 12 // the margin (1), the number (4), the gaps before the title and before the length (2), the length (5)
	var c columns
	if avail >= 50 {
		c.artist = min(max(avail/4, 12), 24)
	}
	if anyAlbum && avail >= 90 {
		c.album = min(max(avail/6, 14), 30)
	}
	c.explicit = anyExplicit
	used := overhead
	if c.artist > 0 {
		used += 1 + c.artist
	}
	if c.album > 0 {
		used += 1 + c.album
	}
	if c.explicit {
		used += 2
	}
	c.title = max(avail-used, 1)
	return c
}
