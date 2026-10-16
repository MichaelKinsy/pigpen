package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// MouseMsg is a mouse event on the screen at cell (X, Y), counted from its top-left: a left click when Clicks > 0 (2 for the
// second click of a double click on the same cell), a turn of the wheel by Wheel lines otherwise (down when positive).
type MouseMsg struct {
	X, Y   int
	Clicks int
	Wheel  int
}

// mouse does what the keys do where the event lands, found in the layout the screen is drawn from (tabBarSpans,
// entityTabsSpans, playBarSpan and the drawing's parts): a tab or a kind of search result shows it, a click on a row of a
// list chooses it (plays a track, opens an album, artist, playlist or page, jumps to a queue entry; toggles a setting), the
// wheel moves a list's cursor, the play bar seeks to the point clicked, and a row of tabs that names keys (the Player's Lyrics
// and Up Next, the Recents' clear) presses the key of the tab clicked. Anywhere else it does nothing.
func (m Model) mouse(e MouseMsg) (tea.Model, tea.Cmd) {
	if e.X < 0 || e.Y < 0 || e.X >= m.w || e.Y >= m.h || (e.Clicks < 1 && e.Wheel == 0) {
		return m, nil
	}
	if m.full() && e.Clicks > 0 {
		switch {
		case e.Y == tabRow:
			_, spans := m.tabBarSpans()
			if t, ok := spanAt(spans, e.X); ok {
				return m.keys(m.tabKey(t)...)
			}
			return m, nil
		case e.Y == bodyTop+1 && m.bodyShown() == bodySearch && m.h-bodyTop-bottomRows >= 2:
			if _, spans := m.entityTabsSpans(); spans != nil {
				if k, ok := spanAt(spans, e.X); ok {
					return m.keys(k)
				}
				return m, nil
			}
		}
	}
	_, parts := m.drawn(true)
	p, x, y, ok := partAt(parts, e.X, e.Y)
	switch {
	case !ok:
	case p.Kind == PartBar && e.Clicks > 0:
		return m.seekAt(x)
	case p.Kind == PartList && p.List != nil && y < len(p.List.Items):
		return m.keys(m.listKeys(p.List, y, e)...)
	case p.Kind == PartTabs && p.keys != nil && e.Clicks > 0:
		for i, t := range p.Tabs {
			if x >= t.From && x < t.To && p.keys[i] != "" {
				return m.keys(p.keys[i])
			}
		}
	}
	return m, nil
}

// tabKey is the key that goes from the screen shown to tab: none when it is shown.
func (m Model) tabKey(t string) []string {
	at, to := 0, 0
	for i, o := range tabOrder {
		if o == m.tab {
			at = i
		}
		if o == t {
			to = i
		}
	}
	switch (to - at + len(tabOrder)) % len(tabOrder) {
	case 0:
		return nil
	case 1:
		return []string{"tab"}
	}
	return []string{"shift+tab"} // three tabs: the other one is a step back
}

// listKeys are the keys that do to a list what e does on its row i: the wheel moves the cursor a row (as Pi's lists do,
// whatever the lines); a click moves it to the row and chooses it (enter), as Pi's SelectList and YouTube Music do, or
// toggles a setting (space, every click). The further clicks of a double or triple click do nothing: the first one chose
// the row already, and the screen under the pointer may be another one now.
func (m Model) listKeys(l *List, i int, e MouseMsg) []string {
	settings := m.bodyShown() == bodySettings
	switch {
	case e.Clicks == 0 && e.Wheel > 0:
		return []string{"down"}
	case e.Clicks == 0:
		return []string{"up"}
	case e.Clicks > 1 && !settings:
		return nil
	case l.Selected < 0 && !l.shared: // the cursor is out of sight: no row to count from
		return nil
	case i < len(l.heads) && l.heads[i]: // a heading: the cursor never rests there
		return nil
	}
	var keys []string
	d := i - l.Selected
	if l.shared {
		d = l.first + i - l.cur
	}
	for j, head := range l.heads { // the keys pass over the headings between the cursor and the row
		switch {
		case head && l.Selected < j && j < i:
			d--
		case head && i < j && j < l.Selected:
			d++
		}
	}
	for d != 0 {
		if d > 0 {
			keys, d = append(keys, "down"), d-1
		} else {
			keys, d = append(keys, "up"), d+1
		}
	}
	if settings {
		return append(keys, "space")
	}
	return append(keys, "enter")
}

// keys gives the model the named keys one by one, as the host would; a prompt being typed in is left first (esc), as the
// keys act on the screen and not on the query.
func (m Model) keys(names ...string) (tea.Model, tea.Cmd) {
	if len(names) == 0 {
		return m, nil
	}
	if m.typing {
		names = append([]string{"esc"}, names...)
	}
	var next tea.Model = m
	cmds := make([]tea.Cmd, 0, len(names))
	for _, k := range names {
		var cmd tea.Cmd
		next, cmd = next.Update(tea.KeyPressMsg(namedKey(k)))
		cmds = append(cmds, cmd)
	}
	return next, tea.Batch(cmds...)
}

// namedKey is the key press of a key name mouse uses.
func namedKey(name string) tea.Key {
	switch name {
	case "up":
		return tea.Key{Code: tea.KeyUp}
	case "down":
		return tea.Key{Code: tea.KeyDown}
	case "enter":
		return tea.Key{Code: tea.KeyEnter}
	case "esc":
		return tea.Key{Code: tea.KeyEscape}
	case "space":
		return tea.Key{Code: tea.KeySpace, Text: " "}
	case "tab":
		return tea.Key{Code: tea.KeyTab}
	case "shift+tab":
		return tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}
	}
	r := []rune(name)[0] // the number keys of the kinds of search results
	return tea.Key{Code: r, Text: name}
}

// seekAt seeks to the point of the track that cell x of the play bar's row shows: the middle of the cell's share of the bar.
func (m Model) seekAt(x int) (tea.Model, tea.Cmd) {
	_, start, barW := m.playBarSpan()
	if barW == 0 || x < start || x >= start+barW || m.state.Duration <= 0 {
		return m, nil
	}
	to := seekPoint(x-start, barW, m.state.Duration)
	d, p := to-m.state.Position, m.deps.Player
	m.status = ""
	m = m.toasted("Seek to " + clock(to))
	return m, op(func(ctx context.Context) error { return p.SeekRelative(ctx, d) })
}

// seekPoint is the middle of cell c's share of a bar of w cells that shows a track of length dur.
func seekPoint[D ~int64](c, w int, dur D) D {
	return D(int64(dur) * int64(2*c+1) / int64(2*w))
}

// spanAt is the key of the span that takes cell x.
func spanAt(spans []span, x int) (string, bool) {
	for _, s := range spans {
		if x >= s.from && x < s.to {
			return s.key, true
		}
	}
	return "", false
}

// partAt is the part at cell (x, y) of parts (down the screen, or down a column of a stage), with the cell counted from the
// part's top-left; in a stage it is the part of the column x falls in.
func partAt(parts []Part, x, y int) (Part, int, int, bool) {
	top := 0
	for _, p := range parts {
		if y >= top+p.Rows {
			top += p.Rows
			continue
		}
		y -= top
		if p.Kind != PartStage {
			return p, x, y, true
		}
		left := 0
		for _, c := range p.Columns {
			if x < left+c.Width {
				return partAt(c.Parts, x-left, y)
			}
			left += c.Width
		}
		return Part{}, 0, 0, false
	}
	return Part{}, 0, 0, false
}

// mouse is a click or the wheel on the quick settings: the wheel moves the selection a row, a click selects the row it lands
// on and changes its value as right and enter do (as left does on the "<" of the volume).
func (m QuickModel) mouse(e MouseMsg) (tea.Model, tea.Cmd) {
	if e.X < 0 || e.Y < 0 || e.X >= m.w || e.Y >= m.h {
		return m, nil
	}
	_, parts := m.Draw()
	p, x, y, ok := partAt(parts, e.X, e.Y)
	if !ok || p.Kind != PartList || p.List == nil || y >= len(p.List.Items) {
		return m, nil
	}
	switch {
	case e.Clicks > 0:
	case e.Wheel > 0:
		return m.key("down")
	case e.Wheel < 0:
		return m.key("up")
	default:
		return m, nil
	}
	m.row = y
	dir := +1
	if x == ansi.StringWidth(m.rowHead(y)) && len(p.List.Items[y].Detail) > 0 && p.List.Items[y].Detail[0] == '<' {
		dir = -1
	}
	m.note = ""
	return m.change(dir)
}
