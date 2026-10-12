package ui

import (
	"fmt"
	"math"
	"strings"
	"time"
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

// The full screen's rows (full): the header and the tab bar (tabRow), a rule, the body from bodyTop, then bottomRows rows
// for the play bar, the status line and the keys.
const (
	tabRow     = 1
	bodyTop    = 3
	bottomRows = 3
)

// full reports whether the screen is drawn in full: from its tab bar to its keys.
func (m Model) full() bool { return m.w >= minWidth && m.h >= minHeight }

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

// Draw is View together with the drawing's parts (parts.go): the same text, and what each run of its rows shows.
func (m Model) Draw() (string, []Part) {
	rows, parts := m.drawn(true)
	return strings.Join(rows, "\n"), parts
}

func (m Model) lines() []string {
	rows, _ := m.drawn(false)
	return rows
}

// drawn is the screen's rows, and with withParts their parts as well.
func (m Model) drawn(withParts bool) ([]string, []Part) {
	w, h := m.w, m.h
	if w < 1 || h < 1 {
		return []string{""}, []Part{{Kind: PartRows, Rows: 1}}
	}
	if w >= minWidth && h < minHeight { // a strip of the terminal: what is playing, and its bar
		return m.stripLines(), fitParts([]Part{{Kind: PartText, Rows: 1}, m.barPart(), {Kind: PartText, Rows: 2}}, h)
	}
	if !m.full() {
		out := blank(w, h)
		out[0] = pad("terminal too small", w)
		return out, fitParts([]Part{{Kind: PartText, Rows: 1}}, h)
	}
	rows := h - bodyTop - bottomRows
	out := make([]string, 0, h)
	out = append(out, m.header(), m.tabBar(), dim.Render(strings.Repeat("─", w)))
	body, bodyParts := m.body(rows)
	for len(body) < rows {
		body = append(body, "")
	}
	for _, l := range body[:rows] {
		out = append(out, pad(l, w))
	}
	out = append(out, m.playBar(), m.statusLine(), m.hints())
	var parts []Part
	if withParts {
		_, spans := m.tabBarSpans()
		parts = append([]Part{{Kind: PartText, Rows: tabRow}, tabsPart(spans), {Kind: PartRows, Rows: bodyTop - tabRow - 1}}, fitParts(bodyParts, rows)...)
		parts = append(parts, m.barPart(), Part{Kind: PartText, Rows: bottomRows - 1})
	}
	switch {
	case m.vibes():
		p := m.pal.Pulsed(m.beat) // once for the whole screen
		for i, l := range out {
			out[i] = m.paint(p, i, len(out), l)
		}
		paintColumns(parts, func(row int, l string) string { return m.paint(p, row, len(out), l) })
	case m.deps.NoColor:
		for i, l := range out {
			out[i] = uncolour(l)
		}
		paintColumns(parts, func(_ int, l string) string { return uncolour(l) })
	}
	return out, parts
}

// barPart is the play bar's part.
func (m Model) barPart() Part {
	return Part{Kind: PartBar, Rows: 1, Position: m.state.Position, Duration: m.state.Duration}
}

// paintColumns gives the columns of every stage among parts what the screen's rows were given, each column's part of a row on
// its own: paint takes the screen's row and the text.
func paintColumns(parts []Part, paint func(row int, l string) string) {
	row := 0
	for _, p := range parts {
		if p.Kind == PartStage {
			for _, c := range p.Columns {
				for i, l := range c.Lines {
					c.Lines[i] = paint(row+i, l)
				}
			}
		}
		row += p.Rows
	}
}

// stripLines is the player in fewer rows than the full screen needs: the track on the first row, the play bar on the second,
// the status and keys after that.
func (m Model) stripLines() []string {
	out := blank(m.w, m.h)
	line := " " + dim.Render("Nothing playing  / search")
	if t := m.state.Track; t != nil {
		f := m.fill(*t)
		line = " " + accent.Render(clean(f.Title))
		if len(f.Artists) > 0 {
			line += dim.Render(" - " + clean(strings.Join(f.Artists, ", ")))
		}
		if m.state.Paused {
			line += dim.Render("  paused")
		}
	}
	rows := []string{line, m.playBar(), m.statusLine(), m.hints()}
	if m.h == 1 {
		rows = rows[:1]
	}
	for i := 0; i < len(out) && i < len(rows); i++ {
		out[i] = pad(rows[i], m.w)
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
	row, _ := m.tabBarSpans()
	return row
}

// span is the cells [from, to) of a row that one thing takes: a tab (key is its name) or a kind of search result (key is
// the key that shows it).
type span struct {
	from, to int
	key      string
}

// tabBarSpans is the tab bar with the cells of its tabs.
func (m Model) tabBarSpans() (string, []span) {
	var b strings.Builder
	spans := make([]span, 0, len(tabOrder))
	b.WriteString(" ")
	at := 1
	for _, t := range tabOrder {
		label := " " + strings.ToUpper(t[:1]) + t[1:] + " "
		if t == m.tab {
			b.WriteString(reverse.Render(label))
		} else {
			b.WriteString(dim.Render(label))
		}
		b.WriteString(" ")
		w := ansi.StringWidth(label)
		spans = append(spans, span{at, at + w, t})
		at += w + 1
	}
	spans[0].from = 0 // the margin is the first tab's: a frontend draws them as one node (kitview.go), named by the margin's cell
	return pad(b.String(), m.w), spans
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

// The bodies the screen shows between its rule and its play bar.
const (
	bodySearch = iota
	bodySettings
	bodyHelp
	bodyPage
	bodyLibrary
	bodyPlayer
)

// bodyShown is the body the screen shows.
func (m Model) bodyShown() int {
	switch {
	case m.settings.open:
		return bodySettings
	case m.help:
		return bodyHelp
	case m.topPage() != nil:
		return bodyPage
	case m.tab == tabLibrary:
		return bodyLibrary
	case m.tab == tabPlayer:
		return bodyPlayer
	}
	return bodySearch
}

// body is the rows between the rule and the play bar, with their parts.
func (m Model) body(rows int) ([]string, []Part) {
	var s sheet
	switch m.bodyShown() {
	case bodySettings:
		m.settingsBody(&s)
	case bodyHelp:
		s.add(PartText, m.helpLines()...)
	case bodyPage:
		m.pageBody(&s, rows)
	case bodyLibrary:
		m.libraryBody(&s, rows)
	case bodyPlayer:
		m.playerBody(&s, rows)
	default:
		m.searchBody(&s, rows)
	}
	return s.rows, s.parts
}

// tableBody adds the rows of a track table: its heading, the tracks it shows as a list, and the line about the rest; a table
// with no tracks is text.
func (s *sheet) tableBody(t table) {
	if t.list == nil || len(t.rows) == 0 {
		s.add(PartText, t.rows...)
		return
	}
	s.add(PartText, t.rows[0])
	s.listRows(t.rows[1:], t.list)
}

// listRows adds rows whose first ones show l, the rest as text; rows that l does not fit are text.
func (s *sheet) listRows(rows []string, l *List) {
	n := len(l.Items)
	if n == 0 || n > len(rows) {
		s.add(PartText, rows...)
		return
	}
	s.list(l, rows[:n]...)
	s.add(PartText, rows[n:]...)
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

// searchBody is searchLines with its parts: the prompt and the result kinds are text, the results a list. Before anything is
// searched it is the home (home.go), when there is one to show.
func (m Model) searchBody(s *sheet, rows int) {
	if m.homeShown() {
		intro := dim.Render(" Press / to search YouTube Music.")
		if m.typing {
			intro = " / " + clean(m.query) + reverse.Render(" ")
		}
		if m.homeBody(s, intro, rows) {
			return
		}
	}
	lines := m.searchLines(rows)
	if !m.typing && !m.loading && m.searched == "" {
		s.add(PartText, lines...)
		return
	}
	head := 1
	tabs, spans := m.entityTabsSpans()
	if tabs != "" {
		head = 2
		rows-- // as searchLines counts them
	}
	head = min(head, len(lines))
	s.add(PartText, lines[:min(head, 1)]...)
	if head == 2 {
		s.tabs(lines[1], spans)
	}
	if m.entKind == "albums" || m.entKind == "artists" {
		list := m.shownEntities()
		first, end := window(len(list), m.entCur, rows-1)
		items := make([]ListItem, 0, end-first)
		for _, e := range list[first:end] {
			items = append(items, ListItem{Label: clean(e.Title), Detail: clean(e.Subtitle)})
		}
		s.listRows(lines[head:], listAt(items, first, m.entCur))
		return
	}
	t := m.trackTableOf(m.filled(m.results), m.resCur, -1, rows-1)
	t.rows = lines[head:] // the same rows, as searchLines drew them
	s.tableBody(t)
}

// ── library ─────────────────────────────────────────────────────────────────

// ── player ──────────────────────────────────────────────────────────────────

// The Player screen's layouts, by width:
//   - from wideStage cells, three columns: the now-playing panel, the lyrics, and the queue as a rail (two without lyrics);
//   - from twoColumns, two: the now-playing panel and one panel, the lyrics or the queue, which a row of two tabs over it
//     switches (L, or a click); while it shows the lyrics, the now-playing panel ends with the next tracks;
//   - narrower, the track over the panel: its cover beside its text from coverStack cells, its text alone below that.
const (
	wideStage  = 132
	twoColumns = 80
	coverStack = 50
)

// stageWidths are the widths of the Player screen's columns: the now-playing panel, the middle (the queue or the lyrics) and the
// queue rail (0 when there is none). Below twoColumns there is one column.
func (m Model) stageWidths() (left, mid, rail int) {
	if m.w < twoColumns {
		return 0, m.w, 0
	}
	left = min(max(m.w/3, 22), 38)
	if m.w >= wideStage && m.lyricsVisible() {
		rail = min(max(m.w/4, 32), 44)
		return left, m.w - left - rail - 2, rail
	}
	return left, m.w - left - 1, 0
}

// switchable: the panel switches between the lyrics and the queue, with its row of tabs (below wideStage, with lyrics).
func (m Model) switchable() bool { return m.deps.Lyrics != nil && m.w < wideStage }

// shortUpNext: the now-playing panel ends with the next tracks, as the lyrics fill the panel beside it.
func (m Model) shortUpNext() bool {
	return m.lyricsVisible() && m.w >= twoColumns && m.w < wideStage
}

// stackHead is how many of rows the track takes over the panel below twoColumns: none in a short body.
func (m Model) stackHead(rows int) int {
	switch {
	case m.w >= twoColumns || rows < 8:
		return 0
	case m.w >= coverStack:
		return min(max(rows*2/5, 4), 8)
	}
	return 3
}

// panelRows is the rows of the lyrics or queue panel under its heading.
func (m Model) panelRows() int {
	rows := max(m.h-bodyTop-bottomRows, 0)
	return max(rows-m.stackHead(rows)-1, 0)
}

// panelHeading adds the panel's heading: on a switchable stage the row of tabs Lyrics and Up Next (the one shown bold, the other
// with its key, L, which a click presses), with notes after it; else the panel's own name.
func (m Model) panelHeading(s *sheet, name, notes string) {
	if !m.switchable() {
		s.add(PartText, " "+bold.Render(name)+notes)
		return
	}
	// each label carries a space on either side, as the tab bar's do: a frontend that draws the tabs as nodes keeps the gaps
	var b strings.Builder
	var spans []span
	at := 0
	for i, tab := range []string{"Lyrics", "Up Next"} {
		label, key := " "+tab+" ", ""
		if tab == name {
			b.WriteString(bold.Render(label))
		} else {
			label, key = " "+tab+" L ", "L"
			b.WriteString(dim.Render(label))
		}
		spans = append(spans, span{at, at + len(label), key})
		at += len(label)
		if i == 0 {
			b.WriteString(" ")
			at++
		}
	}
	s.keyTabs(b.String()+notes, spans)
}

// playerBody is the Player screen in the layout of its width (see wideStage).
func (m Model) playerBody(s *sheet, rows int) {
	q := m.filled(m.state.Queue)
	left, mid, rail := m.stageWidths()
	queue := func(w, rows int) sheet {
		var c sheet
		m.panelHeading(&c, "Up Next", "")
		c.tableBody(m.tableIn(w, q, m.queueCur, m.state.Index, rows-1))
		return c
	}
	if m.w < twoColumns {
		head := m.stackHead(rows)
		if head > 0 {
			m.trackBlock(s, head)
		}
		c := queue(m.w, rows-head)
		if m.lyricsVisible() {
			c = m.lyricsPanel(rows - head)
		}
		s.rows, s.parts = append(s.rows, c.rows...), append(s.parts, c.parts...)
		return
	}
	cols := []sheet{m.nowPlaying(left, rows)}
	widths := []int{left}
	if m.lyricsVisible() {
		cols, widths = append(cols, m.lyricsPanel(rows)), append(widths, mid)
		if rail > 0 {
			cols, widths = append(cols, queue(rail, rows)), append(widths, rail)
		}
	} else {
		cols, widths = append(cols, queue(mid, rows)), append(widths, mid)
	}
	s.stage(cols, widths, rows, dim.Render("│"))
}

// stage adds columns side by side, rows high, each cut and padded to its width; divider ("" none) goes between them.
func (s *sheet) stage(cols []sheet, widths []int, rows int, divider string) {
	stage := Part{Kind: PartStage, Rows: rows}
	for c, col := range cols {
		if c > 0 && divider != "" {
			lines := make([]string, rows)
			for i := range lines {
				lines[i] = divider
			}
			stage.Columns = append(stage.Columns, Column{Width: 1, Lines: lines, Parts: []Part{{Kind: PartRows, Rows: rows}}})
		}
		lines := make([]string, rows)
		for i := range lines {
			l := ""
			if i < len(col.rows) {
				l = col.rows[i]
			}
			lines[i] = pad(l, widths[c])
		}
		stage.Columns = append(stage.Columns, Column{Width: widths[c], Lines: lines, Parts: fitParts(col.parts, rows)})
	}
	for i := range rows {
		var b strings.Builder
		for _, c := range stage.Columns {
			b.WriteString(c.Lines[i])
		}
		s.rows = append(s.rows, b.String())
	}
	s.parts = append(s.parts, stage)
}

// trackBlock adds the track over the panel below twoColumns, rows high: from coverStack cells its cover (or the disc) beside its
// text, else the text alone.
func (m Model) trackBlock(s *sheet, rows int) {
	var text sheet
	text.add(PartText, m.stackText(rows)...)
	if m.w < coverStack {
		for len(text.rows) < rows {
			text.add(PartText, "")
		}
		s.rows, s.parts = append(s.rows, text.rows...), append(s.parts, text.parts...)
		return
	}
	cw := 2*rows + 2 // the picture (two cells a row, from coverSize), its margin, and a gap
	var pic sheet
	if lines := m.cover(cw, rows); lines != nil {
		pic.rows = lines
		pic.parts = []Part{{Kind: PartCover, Rows: len(lines), Cover: m.coverFile()}}
	}
	s.stage([]sheet{pic, text}, []int{cw, m.w - cw}, rows, "")
}

// stackText is the track's text over the panel, at most rows lines: the title, the artists, what comes next (in the track's last
// seconds, how soon), the length and the place in the queue, the album, and a gap before the length; what does not fit goes from
// the end of that list.
func (m Model) stackText(rows int) []string {
	t := m.state.Track
	if t == nil {
		return []string{dim.Render(" Nothing playing"), dim.Render(" Search with / and press enter")}[:min(rows, 2)]
	}
	f := m.fill(*t)
	title := bold
	if m.vibes() {
		title = accent
	}
	const (
		lTitle = iota
		lArtist
		lAlbum
		lGap
		lInfo
		lNext
		lines
	)
	var text [lines]string
	var has [lines]bool
	set := func(i int, s string) { text[i], has[i] = s, true }
	set(lTitle, " "+title.Render(clean(f.Title)))
	if len(f.Artists) > 0 {
		set(lArtist, " "+clean(strings.Join(f.Artists, ", ")))
	}
	if f.Album != "" {
		set(lAlbum, " "+dim.Render(clean(f.Album)))
	}
	info := fmt.Sprintf("%d of %d", m.state.Index+1, len(m.state.Queue))
	if f.Duration > 0 {
		info = clock(f.Duration) + " · " + info
	}
	set(lGap, "")
	set(lInfo, " "+dim.Render(info))
	if next, ok := m.nextTrack(); ok {
		label := dim.Render("Next ")
		if m.upNextCard() != nil {
			left := m.state.Duration - m.state.Position
			label = accent.Render(fmt.Sprintf("Next in %ds ", int((left+time.Second-1)/time.Second)))
		}
		n := m.fill(next)
		line := " " + label + clean(n.Title)
		if len(n.Artists) > 0 {
			line += dim.Render(" - " + clean(strings.Join(n.Artists, ", ")))
		}
		set(lNext, line)
	}
	keep := 0
	for _, i := range []int{lTitle, lArtist, lNext, lInfo, lAlbum, lGap} { // the order in which the lines earn a row
		if has[i] && keep < rows {
			keep++
			continue
		}
		has[i] = false
	}
	var out []string
	for i := range text {
		if has[i] {
			out = append(out, text[i])
		}
	}
	return out
}

// nextTrack is the track that follows the current one in the queue (the first again when the whole queue repeats).
func (m Model) nextTrack() (music.Track, bool) {
	next := m.state.Index + 1
	if next >= len(m.state.Queue) {
		if m.state.Repeat != music.RepeatAll || len(m.state.Queue) < 2 {
			return music.Track{}, false
		}
		next = 0
	}
	if m.state.Index < 0 {
		return music.Track{}, false
	}
	return m.state.Queue[next], true
}

// nowPlaying is the left panel: a heading, the cover or the disc when there is room, then the track's text, and the next tracks
// while the lyrics fill the panel beside it.
func (m Model) nowPlaying(w, rows int) sheet {
	var s sheet
	s.add(PartText, " "+bold.Render("Now Playing"), "")
	text := m.nowText()
	var next sheet
	if m.shortUpNext() && m.state.Track != nil {
		next = m.upNextShort(w)
	}
	if pic := m.cover(w, rows-2-len(text)-len(next.rows)-1); pic != nil {
		s.rows = append(s.rows, pic...)
		s.parts = append(s.parts, Part{Kind: PartCover, Rows: len(pic), Cover: m.coverFile()})
		s.add(PartRows, "")
	}
	s.add(PartText, text...)
	s.rows, s.parts = append(s.rows, next.rows...), append(s.parts, next.parts...)
	return s
}

// upNextRowsShort is how many of the next tracks the now-playing panel lists beside the lyrics.
const upNextRowsShort = 3

// upNextShort is the next tracks under the track's text, w cells wide: a heading and a list that shares the queue's cursor (a
// click jumps to the track), always the same number of rows.
func (m Model) upNextShort(w int) sheet {
	var s sheet
	s.add(PartText, "", " "+bold.Render("Up Next"))
	first := m.state.Index + 1
	end := min(first+upNextRowsShort, len(m.state.Queue))
	if first >= end {
		s.add(PartText, dim.Render(" Nothing after this one"))
	} else {
		var rows []string
		var items []ListItem
		for i := first; i < end; i++ {
			t := m.fill(m.state.Queue[i])
			row := " " + clean(t.Title)
			if len(t.Artists) > 0 {
				row += dim.Render(" - " + clean(strings.Join(t.Artists, ", ")))
			}
			if i == m.queueCur {
				row = reverse.Render(pad(row, w))
			}
			rows = append(rows, row)
			items = append(items, ListItem{Label: clean(t.Title), Detail: clean(strings.Join(t.Artists, ", "))})
		}
		s.list(sharedList(items, first, m.queueCur), rows...)
	}
	for len(s.rows) < 2+upNextRowsShort {
		s.add(PartText, "")
	}
	return s
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
	if m.pulsing() && m.haveLevel && !m.lyricsVisible() { // the lyrics take the level bar's place: the colours still move with the beat
		out = append(out, " "+meter(m.rms, 14))
	}
	// the queue's place and modes, or in the last seconds the "up next" card in their place: the same number of rows either way,
	// so the cover above does not change size when the card comes. Beside the lyrics, the next tracks take their place instead
	// (upNextShort).
	if m.shortUpNext() {
		return out
	}
	tail := []string{"", dim.Render(fmt.Sprintf(" %d of %d in the queue", m.state.Index+1, len(m.state.Queue)))}
	if _, ok := m.deps.Player.(music.Modes); ok {
		shuffle, repeat := "off", "off"
		if m.state.Shuffle {
			shuffle = "on"
		}
		if m.state.Repeat != music.RepeatOff {
			repeat = string(m.state.Repeat)
		}
		tail = append(tail, "", " Shuffle: "+shuffle, " Repeat: "+repeat)
	}
	rows := max(len(tail), upNextRows)
	if card := m.upNextCard(); card != nil {
		tail = card
	}
	for len(tail) < rows {
		tail = append(tail, "")
	}
	return append(out, tail...)
}

// upNextRows is the most rows the "up next" card takes.
const upNextRows = 4

// upNextFor is how long before the end of a track the "up next" card appears.
const upNextFor = 10 * time.Second

// upNextCard is the card for the track that follows, in the last seconds of this one: "" lines when there is no such track.
func (m Model) upNextCard() []string {
	t := m.state.Track
	if t == nil || m.state.Duration <= 0 || m.state.Paused || m.state.Repeat == music.RepeatOne {
		return nil
	}
	left := m.state.Duration - m.state.Position
	if left <= 0 || left > upNextFor {
		return nil
	}
	next := m.state.Index + 1
	if next >= len(m.state.Queue) {
		if m.state.Repeat != music.RepeatAll || len(m.state.Queue) < 2 {
			return nil
		}
		next = 0
	}
	n := m.fill(m.state.Queue[next])
	out := []string{"", " " + accent.Render(fmt.Sprintf("Up next in %ds", int((left+time.Second-1)/time.Second))), " " + bold.Render(clean(n.Title))}
	if len(n.Artists) > 0 {
		out = append(out, " "+dim.Render(clean(strings.Join(n.Artists, ", "))))
	}
	return out
}

// ── tables ──────────────────────────────────────────────────────────────────

// trackTable draws a header and rows of tracks, scrolled so that cur is visible.
// playing marks the current track's row with ">"; max is the number of lines available.
func (m Model) trackTable(ts []music.Track, cur, playing, max int) []string {
	return m.trackTableOf(ts, cur, playing, max).rows
}

// trackTableOf is trackTable with the list of the tracks it shows.
func (m Model) trackTableOf(ts []music.Track, cur, playing, max int) table {
	avail := m.w
	if m.tab == tabPlayer {
		_, avail, _ = m.stageWidths()
	}
	return m.tableIn(avail, ts, cur, playing, max)
}

// trackTableIn is trackTable in a column avail cells wide.
func (m Model) trackTableIn(avail int, ts []music.Track, cur, playing, max int) []string {
	return m.tableIn(avail, ts, cur, playing, max).rows
}

// table is a track table's rows, and the list its rows of tracks show (rows[1:] onwards; nil when there are none).
type table struct {
	rows []string
	list *List
}

// tableIn is trackTableIn with the list of the tracks it shows.
func (m Model) tableIn(avail int, ts []music.Track, cur, playing, max int) table {
	if max < 2 || len(ts) == 0 {
		if len(ts) == 0 && max >= 1 {
			return table{rows: []string{dim.Render("  The queue is empty. Search with / and press enter.")}}
		}
		return table{}
	}
	var anyAlbum, anyExplicit bool
	for _, t := range ts {
		anyAlbum = anyAlbum || t.Album != ""
		anyExplicit = anyExplicit || t.Explicit
	}
	plan := columnPlan(avail, anyAlbum, anyExplicit)
	line := func(num, title, badge, artist, album, length string) string {
		return planLine(plan, num, title, badge, artist, album, length)
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
	items := make([]ListItem, 0, end-first)
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
		title, artists, album := clean(t.Title), clean(strings.Join(t.Artists, ", ")), clean(t.Album)
		row := line(num, title, badge, artists, album, l)
		if i == cur {
			row = reverse.Render(pad(row, avail))
		}
		out = append(out, row)
		item := ListItem{Label: title, Detail: artists}
		if plan.album > 0 {
			item.Columns = []string{album}
		}
		item.Columns = append(item.Columns, l)
		items = append(items, item)
	}
	if more > 0 {
		out = append(out, dim.Render(fmt.Sprintf("  + %d more", more)))
	}
	t := table{rows: out}
	if len(items) > 0 {
		t.list = listAt(items, first, cur)
	}
	return t
}

// planLine is one row of a track table in the columns of plan: the number, the title, the explicit badge, the artist, the album
// and the length, each cut to its column.
func planLine(plan columns, num, title, badge, artist, album, length string) string {
	const numW, lenW = 4, 5
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
	row, _, _ := m.playBarSpan()
	return row
}

// playBarSpan is the play bar's row and the cells of its bar: barW cells from start, 0 when the row is too narrow for one.
func (m Model) playBarSpan() (row string, start, barW int) {
	icon := ">"
	if m.state.Paused {
		icon = "|"
	}
	pos, dur := clock(m.state.Position), clock(m.state.Duration)
	barW = m.w - ansi.StringWidth(pos) - ansi.StringWidth(dur) - 7
	if barW < 4 {
		return pad(" "+icon+" "+pos+"/"+dur, m.w), 0, 0
	}
	filled := 0
	if m.state.Duration > 0 {
		filled = int(int64(barW) * int64(m.state.Position) / int64(m.state.Duration))
		filled = minInt(maxInt(filled, 0), barW)
	}
	// a thin rule, heavy where it has played (the weight shows the progress without colour too)
	bar := accent.Render(strings.Repeat("━", filled)) + dim.Render(strings.Repeat("─", barW-filled))
	head := " " + icon + " " + pos + " "
	return pad(head+bar+" "+dur+" ", m.w), ansi.StringWidth(head), barW
}

// toastFor is how long a confirmation stays on the status line.
const toastFor = 2 * time.Second

func (m Model) toasted(s string) Model {
	m.toast, m.toastAt = s, m.now()
	return m
}

// toastDoneMsg is the end of a toast's time: the screen is drawn again without it, also when nothing else would redraw it
// (paused, or calm).
type toastDoneMsg struct{}

// toastTick is the timer of a toast (a variable for the tests' rig, which runs commands inline).
var toastTick = func(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return toastDoneMsg{} })
}

// toastTimer starts the one timer of the toast on screen, for what is left of its time.
func (m Model) toastTimer() (Model, tea.Cmd) {
	if m.toast == "" || m.toastArmed {
		return m, nil
	}
	left := toastFor - m.now().Sub(m.toastAt)
	if left <= 0 {
		m.toast = ""
		return m, nil
	}
	m.toastArmed = true
	return m, toastTick(left)
}

// volumeToast is "Volume ████████░░░░ 65%".
func volumeToast(v int) string {
	n := min(max(v, 0), 100) * 12 / 100
	return fmt.Sprintf("Volume %s%s %d%%", strings.Repeat("█", n), strings.Repeat("░", 12-n), v)
}

func (m Model) statusLine() string {
	if m.status == "" {
		if m.toast != "" && m.now().Sub(m.toastAt) < toastFor {
			return pad(" "+accent.Render(clean(m.toast)), m.w)
		}
		return pad("", m.w)
	}
	return pad(" "+errored.Render(clean(m.status)), m.w)
}

func (m Model) hints() string {
	keys := []string{"space play", "n/p skip", "left/right seek", "+/- vol", "s shuffle", "l repeat"}
	if m.deps.Lyrics != nil && m.tab == tabPlayer {
		keys = append(keys, "L lyrics", ", . nudge")
	}
	keys = append(keys, "/ search")
	if m.tab == tabSearch && m.bodyShown() == bodySearch && m.searched != "" && m.homeActive() {
		keys = append([]string{"backspace home"}, keys...)
	}
	if m.homeShown() && len(m.homeItems()) > 0 {
		home := []string{"enter play/open"}
		if len(m.home.recents) > 0 {
			home = append(home, "C clear recents")
		}
		if _, ok := m.deps.Source.(music.HomeFeed); ok {
			home = append(home, "r reload")
		}
		keys = append(home, keys...)
	}
	head, tail := []string(nil), []string{"? help", "q hide"}
	if m.topPage() != nil {
		head, keys, tail = []string{"esc/backspace back"}, []string{"enter play/open", "P play all", "S shuffle", "a add", "A artist", "B album", "R radio", "T related"}, []string{"q hide"}
	}
	if m.typing {
		head, keys, tail = nil, []string{"type to search", "enter search", "esc cancel"}, nil
	}
	return pad(" "+dim.Render(fitKeys(m.w-1, head, keys, tail)), m.w)
}

// fitKeys joins the key hints that fit in w cells: head and tail always (the way back and out, and help), then as many of the
// others, in order, as there is room for.
func fitKeys(w int, head, keys, tail []string) string {
	const gap = "  "
	used := 0
	for _, k := range append(append([]string(nil), head...), tail...) {
		used += ansi.StringWidth(k) + len(gap)
	}
	mid := []string{}
	for _, k := range keys {
		if used+ansi.StringWidth(k)+len(gap) > w+len(gap) {
			break
		}
		used += ansi.StringWidth(k) + len(gap)
		mid = append(mid, k)
	}
	return strings.Join(append(append(append([]string(nil), head...), mid...), tail...), gap)
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
		{"up/down j/k wheel", "move the selection"},
		{"enter, click", "play or open the selected row, or jump to that queue entry (a click chooses the row it is on)"},
		{"1 / 2 / 3", "search results: songs / albums / artists (enter opens one, esc or backspace goes back)"},
		{"A / B / R / T", "go to the artist, the album, the radio, the related page of the track under the cursor (the playing one while the lyrics fill the Player screen)"},
		{"P / S", "on a page: play all / play shuffled"},
		{"esc / backspace", "on a page: back one page (the breadcrumbs show where you are); on search results, backspace: the home"},
		{"a", "add the selected result to the queue"},
		{"C / r", "the Search screen's home: clear the recents / load it again"},
		{"L", "Player screen: switch the panel between the lyrics and Up Next (wide: lyrics on / off)"},
		{", / .", "lyrics earlier / later by 0.1 s, remembered for the track"},
		{"x, [ / ]", "remove the selected queue entry; reorder: move it up / down"},
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
