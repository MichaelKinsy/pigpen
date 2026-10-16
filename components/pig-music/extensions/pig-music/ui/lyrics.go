package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// lyricEvery is the frame time of the sweep across the current line (about 10 frames a second: a few characters a frame).
const lyricEvery = 100 * time.Millisecond

// nudge is how far one press of "," or "." moves a track's lyrics.
const nudge = 100 * time.Millisecond

type lyricStatus int

const (
	lyricIdle lyricStatus = iota
	lyricLoading
	lyricReady
	lyricNone
	lyricFailed
)

// lyricState is the lyrics of the track that is playing.
type lyricState struct {
	id     string
	status lyricStatus
	data   music.Lyrics
	err    string
	top    int        // the first row shown; it eases toward where the current line wants it
	wrap   *lyricWrap // data wrapped for the panel; a new one with every sheet
	snap   bool       // the next follow puts the view in place at once (the lyrics just arrived)
	offset time.Duration
}

type lyricsMsg struct {
	id  string
	l   music.Lyrics
	err error
}

// lyricsVisible: the lyrics take the place of the queue on the Player screen. They need a provider; L turns them off.
func (m Model) lyricsVisible() bool {
	return m.deps.Lyrics != nil && m.lyricsOff == false && m.tab == tabPlayer && !m.settings.open && !m.help
}

func (m Model) now() time.Time {
	if m.deps.Now != nil {
		return m.deps.Now()
	}
	return time.Now()
}

// lyricPos is where the lyrics are in the track: the player's real position, carried forward by the clock only between two of
// its reports while it plays, minus the user's nudge for this track. It is never the clock alone.
func (m Model) lyricPos() time.Duration {
	pos := m.state.Position
	if !m.state.Paused && m.state.Connected && !m.posAt.IsZero() {
		if d := m.now().Sub(m.posAt); d > 0 && d < 2*time.Second {
			pos += d
		}
	}
	return pos - m.lyr.offset
}

// fetchLyrics starts the lookup for the playing track when the lyrics are on screen and are not already known.
func (m Model) fetchLyrics() (Model, tea.Cmd) {
	cur := m.state.Track
	if !m.lyricsVisible() || m.hidden || cur == nil || m.lyr.id == cur.ID {
		return m, nil
	}
	t := m.fill(*cur)
	prov := m.deps.Lyrics
	m.lyr = lyricState{id: cur.ID, status: lyricLoading, offset: prov.Offset(cur.ID)}
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), libTimeout)
		defer cancel()
		l, err := prov.Lyrics(ctx, t)
		return lyricsMsg{id: t.ID, l: l, err: err}
	}
}

func (m Model) lyricsArrived(msg lyricsMsg) Model {
	if m.lyr.id != msg.id {
		return m // the track changed meanwhile
	}
	switch {
	case msg.err == nil && (len(msg.l.Lines) > 0 || msg.l.Instrumental):
		m.lyr.status, m.lyr.data, m.lyr.wrap = lyricReady, msg.l, &lyricWrap{}
	case msg.err == nil || errors.Is(msg.err, music.ErrNoLyrics):
		m.lyr.status = lyricNone
	default:
		m.lyr.status, m.lyr.err = lyricFailed, msg.err.Error()
	}
	m.lyr.snap = true // they arrive in place, no scroll from the top
	return m.followLyrics()
}

// lyricsKey handles L (show or hide), "," and "." (nudge) on the Player screen.
func (m Model) lyricsKey(k string) (Model, tea.Cmd, bool) {
	if m.deps.Lyrics == nil {
		return m, nil, false
	}
	switch k {
	case "L":
		m.lyricsOff = !m.lyricsOff
		if m.lyricsOff {
			return m, nil, true
		}
		if m.lyr.status == lyricFailed {
			m.lyr = lyricState{} // showing them again tries again
		}
		m, cmd := m.fetchLyrics()
		return m.followLyrics(), cmd, true
	case ",", ".":
		if m.lyr.id == "" || !m.lyricsVisible() {
			return m, nil, false
		}
		d := nudge
		if k == "," {
			d = -nudge
		}
		m.lyr.offset += d
		m.deps.Lyrics.SetOffset(m.lyr.id, m.lyr.offset)
		return m.followLyrics(), nil, true
	}
	return m, nil, false
}

// lyricsAnimating: the sweep moves only while synced lyrics are on screen and the track plays, without Calm or low power.
func (m Model) lyricsAnimating() bool {
	return m.lyricsVisible() && !m.hidden && m.lyr.status == lyricReady && m.lyr.data.Synced && !m.deps.Calm &&
		m.state.Connected && !m.state.Paused && m.state.Track != nil
}

// ── geometry ────────────────────────────────────────────────────────────────

// maxLyricW is the widest a lyric line is set: on a wide terminal the lines stay a readable length, centred in their column.
const maxLyricW = 76

// lyricGeom is the width and the number of rows of the lyrics panel (below its heading).
func (m Model) lyricGeom() (w, rows int) {
	_, w, _ = m.stageWidths()
	return max(min(w-2, maxLyricW), 1), m.panelRows()
}

type lyricRow struct {
	line  int    // which line of the lyrics
	text  string // this row's part of the line
	start int    // the rune index of text within the line
}

// wraps counts the sheets wrapped (tests).
var wraps atomic.Int64

// lyricWrap is the sheet of the lyrics on screen wrapped at one width. It is shared by the model's copies and touched only on
// the host's loop (update and drawing), like artDrawn.
type lyricWrap struct {
	w    int
	rows []lyricRow
}

// lyricRows is the lyrics' rows at width w, wrapped once per sheet and width.
func (m Model) lyricRows(w int) []lyricRow {
	c := m.lyr.wrap
	if c == nil {
		return wrapLyrics(m.lyr.data, w)
	}
	if c.rows == nil || c.w != w {
		c.w, c.rows = w, wrapLyrics(m.lyr.data, w)
	}
	return c.rows
}

// wrapLyrics breaks every line of the lyrics into rows of at most w cells.
func wrapLyrics(l music.Lyrics, w int) []lyricRow {
	wraps.Add(1)
	var rows []lyricRow
	for i, ln := range l.Lines {
		text := strings.TrimSpace(clean(ln.Text))
		if text == "" {
			rows = append(rows, lyricRow{line: i})
			continue
		}
		runes := []rune(text)
		start := 0
		for start < len(runes) {
			end, width, lastSpace := start, 0, -1
			for end < len(runes) {
				rw := ansi.StringWidth(string(runes[end]))
				if width+rw > w {
					break
				}
				width += rw
				if runes[end] == ' ' {
					lastSpace = end
				}
				end++
			}
			if end < len(runes) && lastSpace > start {
				end = lastSpace + 1 // break after the space
			}
			if end == start {
				end = start + 1
			}
			rows = append(rows, lyricRow{line: i, text: strings.TrimRight(string(runes[start:end]), " "), start: start})
			start = end
		}
	}
	return rows
}

// currentLine is the index of the line being sung at pos: the last one that has started, -1 before the first.
func currentLine(l music.Lyrics, pos time.Duration) int {
	cur := -1
	for i, ln := range l.Lines {
		if ln.At <= pos {
			cur = i
		} else {
			break
		}
	}
	return cur
}

// sungRunes is how many runes of the line are sung at pos: per word when the line has word timings, else spread evenly over the
// time until the next line (at most six seconds, and finished a little before the next line starts).
func sungRunes(ln music.LyricLine, pos time.Duration) int {
	total := utf8.RuneCountInString(strings.TrimSpace(clean(ln.Text)))
	if total == 0 || pos < ln.At {
		return 0
	}
	if len(ln.Words) > 0 {
		sung := 0
		for i, w := range ln.Words {
			n := utf8.RuneCountInString(strings.TrimRight(w.Text, " "))
			if pos >= w.At {
				end := ln.End
				if i+1 < len(ln.Words) {
					end = ln.Words[i+1].At
				}
				if end > w.At && pos < end {
					return min(sung+int(float64(n)*float64(pos-w.At)/float64(end-w.At)+0.5), total)
				}
				sung += utf8.RuneCountInString(w.Text)
				continue
			}
			break
		}
		return min(sung, total)
	}
	dur := 4 * time.Second
	if ln.End > ln.At {
		dur = ln.End - ln.At
	}
	dur = time.Duration(float64(min(dur, 6*time.Second)) * 0.9)
	if dur <= 0 {
		return total
	}
	frac := float64(pos-ln.At) / float64(dur)
	if frac >= 1 {
		return total
	}
	return int(frac*float64(total) + 0.5)
}

// lyricTarget is the first row the view wants at the top: for a synced sheet, the one that puts the current line a third of the
// way down, from the first line to the last (the top may be above the sheet, the rows there are blank), so the line being sung
// stays at one height and the sheet scrolls under it; for a plain sheet, the part that matches how far into the track it is.
func lyricTarget(rows []lyricRow, l music.Lyrics, pos, dur time.Duration, visible int) int {
	maxTop := max(len(rows)-visible, 0)
	if !l.Synced {
		if dur <= 0 {
			return 0
		}
		return min(max(int(float64(maxTop)*float64(pos)/float64(dur)), 0), maxTop)
	}
	cur := currentLine(l, pos)
	first := 0
	for i, r := range rows {
		if r.line >= cur {
			first = i
			break
		}
	}
	if cur < 0 {
		first = 0
	}
	anchor := lyricAnchor(visible)
	return min(max(first-anchor, -anchor), max(len(rows)-1-anchor, -anchor))
}

// lyricAnchor is the row of the panel the current line is held at.
func lyricAnchor(visible int) int { return visible / 3 }

// followLyrics eases the view toward the current line: a row at a time (more when far behind), at once under Calm or when the
// jump is large (a seek).
func (m Model) followLyrics() Model {
	if m.lyr.status != lyricReady || !m.lyricsVisible() || m.hidden { // hidden: caught up when shown (OpenedMsg)
		return m
	}
	w, rows := m.lyricGeom()
	visible := max(rows-1, 1)
	target := lyricTarget(m.lyricRows(w), m.lyr.data, m.lyricPos(), m.stateDuration(), visible)
	diff := target - m.lyr.top
	switch {
	case diff == 0:
	case m.deps.Calm || m.lyr.snap || diff > visible || -diff > visible:
		m.lyr.top = target
	default:
		step := max(abs(diff)/3, 1)
		if diff < 0 {
			step = -step
		}
		m.lyr.top += step
	}
	m.lyr.snap = false
	return m
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (m Model) stateDuration() time.Duration {
	if m.state.Track != nil {
		return max(m.state.Duration, m.state.Track.Duration)
	}
	return m.state.Duration
}

// ── view ────────────────────────────────────────────────────────────────────

func center(s string, w int) string {
	if pad := w - ansi.StringWidth(s); pad > 1 {
		return strings.Repeat(" ", pad/2) + s
	}
	return s
}

// lyricsPanel is the lyrics in place of the queue: a heading and rows that fill the panel; the rows of the line being sung are
// a part of their own.
func (m Model) lyricsPanel(rows int) sheet {
	// the heading names where the lyrics come from and a nudge when there is one; the keys are on the key line and in help
	var notes []string
	if m.lyr.status == lyricReady && m.lyr.data.Source != "" {
		notes = append(notes, m.lyr.data.Source)
	}
	if m.lyr.offset != 0 {
		notes = append(notes, fmt.Sprintf("offset %+.1fs", m.lyr.offset.Seconds()))
	}
	note := ""
	if len(notes) > 0 {
		note = dim.Render("  " + strings.Join(notes, " · "))
	}
	var s sheet
	m.panelHeading(&s, "Lyrics", note)
	w, body := m.lyricGeom()
	body = min(body, rows-1)
	if body < 1 {
		return s
	}
	_, col, _ := m.stageWidths()
	indent := " " + strings.Repeat(" ", max((col-2-w)/2, 0))
	lines, sung := m.lyricBody(w, body)
	for i, l := range lines {
		kind := PartText
		if sung[i] {
			kind = PartLyric
		}
		s.add(kind, indent+l)
	}
	return s
}

// lyricBody is the lyrics' rows, centred, each at most w cells wide; always exactly rows lines. sung marks the rows of the line
// being sung.
func (m Model) lyricBody(w, rows int) (out []string, sung []bool) {
	sung = make([]bool, rows)
	note := func(lines ...string) ([]string, []bool) {
		out := make([]string, rows)
		start := max((rows-len(lines))/2, 0)
		for i, l := range lines {
			if start+i < rows {
				out[start+i] = center(l, w)
			}
		}
		return out, sung
	}
	t := m.state.Track
	switch {
	case t == nil:
		return note(dim.Render("♪"), dim.Render("Play something to see its lyrics"))
	case m.lyr.id != t.ID || m.lyr.status == lyricIdle || m.lyr.status == lyricLoading:
		return note(dim.Render("♪  ♪  ♪"), dim.Render("Looking for the lyrics..."))
	case m.lyr.status == lyricFailed:
		return note(dim.Render("♪"), "Could not get the lyrics", dim.Render(truncateTo(clean(m.lyr.err), w)), dim.Render("L twice tries again"))
	case m.lyr.status == lyricNone:
		return note(dim.Render("♪   ♪   ♪"), "No lyrics for this one", dim.Render(truncateTo(clean(m.fill(*t).Title), w)))
	case m.lyr.data.Instrumental:
		return note(dim.Render("♪   ♪   ♪"), "Instrumental", dim.Render("no words, just the music"))
	}
	l := m.lyr.data
	all := m.lyricRows(w)
	visible := rows
	pos := m.lyricPos()
	top := m.lyr.top
	if l.Synced {
		top = min(max(top, -lyricAnchor(visible)), max(len(all)-1, 0)) // above the sheet: blank rows
	} else {
		top = min(max(top, 0), max(len(all)-visible, 0))
	}
	cur := -1
	if l.Synced {
		cur = currentLine(l, pos)
	}
	lit := 0
	if cur >= 0 {
		lit = sungRunes(l.Lines[cur], pos)
		if m.deps.Calm {
			lit = utf8.RuneCountInString(strings.TrimSpace(clean(l.Lines[cur].Text))) // lit all at once, no sweep
		}
	}
	out = make([]string, 0, rows)
	for i := top; i < len(all) && len(out) < rows; i++ {
		if i < 0 {
			out = append(out, "")
			continue
		}
		r := all[i]
		sung[len(out)] = l.Synced && cur >= 0 && r.line == cur
		switch {
		case r.text == "":
			s := ""
			if r.line == cur {
				s = dim.Render("♪")
			}
			out = append(out, center(s, w))
		case !l.Synced:
			out = append(out, center(r.text, w))
		case r.line == cur:
			out = append(out, center(m.sweepRow(r, lit), w))
		default:
			out = append(out, center(dim.Render(r.text), w))
		}
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out, sung
}

// sweepRow draws a row of the current line: what is sung so far lit (bold, in the accent), the rest in the plain text colour,
// brighter than the other lines but not bold, so the sweep shows by weight as well as by colour (a grey cover gives an accent
// close to white) and without colour at all.
func (m Model) sweepRow(r lyricRow, sung int) string {
	runes := []rune(r.text)
	n := min(max(sung-r.start, 0), len(runes))
	lit, rest := string(runes[:n]), string(runes[n:])
	if m.deps.NoColor {
		return bold.Render(lit) + rest
	}
	out := ""
	if lit != "" {
		out = accent.Render(lit)
	}
	return out + rest
}

func truncateTo(s string, w int) string { return ansi.Truncate(s, w, "…") }
