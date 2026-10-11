package ui

import (
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/art"
)

// The parts of a drawing say what each run of the screen's rows shows, for a frontend that draws the screen with elements of
// its own (the extension sends them as a kit view). They change nothing in the rows: View draws the same text with or without
// them, and the terminal shows the rows.

// PartKind says what a run of rows shows.
type PartKind int

const (
	// PartRows is rows drawn as they are: a rule, blank rows, a column's divider.
	PartRows PartKind = iota
	// PartText is rows of text.
	PartText
	// PartList is the rows of a list: a table of tracks, the albums or artists of a search, a page, the collections, the
	// settings. List says what is in it.
	PartList
	// PartBar is the play bar: Position of Duration.
	PartBar
	// PartCover is the picture on the Player screen: Cover is the file the cover was decoded from, nil for the disc.
	PartCover
	// PartLyric is the rows of the line of the lyrics being sung.
	PartLyric
	// PartStage is the Player screen's columns side by side.
	PartStage
	// PartTabs is one row of tabs: the tab bar, or the kinds of search result over the results. Tabs says which cells each
	// tab takes.
	PartTabs
)

// Part is a run of Rows rows. The parts of a drawing follow one another down the screen, or down a column of a stage.
type Part struct {
	Kind PartKind
	Rows int
	// Position and Duration are the play bar's (PartBar).
	Position, Duration time.Duration
	// Cover is the picture's file (PartCover); nil for the disc.
	Cover *Cover
	// List is what the rows of a PartList show, one item per row.
	List *List
	// Columns are a PartStage's columns, left to right; their widths add up to the screen's.
	Columns []Column
	// Tabs are a PartTabs row's tabs, left to right.
	Tabs []Tab
	// keys are the keys a click on each of Tabs presses ("" none); nil for the rows of tabs mouse finds by their own layout
	// (the tab bar, the kinds of search result).
	keys []string
}

// Tab is the cells [From, To) of its row that a tab takes.
type Tab struct{ From, To int }

// Cover is the file a cover was decoded from.
type Cover struct {
	Data     []byte
	MimeType string
}

// List is what the rows of a list show: Items[i] is row i, Selected the item under the cursor (-1 when it is scrolled out).
type List struct {
	Items    []ListItem
	Selected int
	// shared: one cursor moves through this list and others (the Search screen's home, the Player's short Up Next): row i is
	// item first+i of them all, and cur is the cursor, also while it is in another list.
	shared     bool
	first, cur int
	// heads are the rows the cursor passes over (a page's section headings): the keys skip them, and a click on one does nothing.
	heads []bool
}

// ListItem is one row of a list: what it says first (a title), second (the artists), and the further cells of a table row.
type ListItem struct {
	Label, Detail string
	Columns       []string
}

// listAt is the list of items shown from the first'th, with cur the cursor's index among all the items.
func listAt(items []ListItem, first, cur int) *List {
	sel := cur - first
	if sel < 0 || sel >= len(items) {
		sel = -1
	}
	return &List{Items: items, Selected: sel}
}

// sharedList is listAt for a list whose cursor it shares with others (List.shared).
func sharedList(items []ListItem, first, cur int) *List {
	l := listAt(items, first, cur)
	l.shared, l.first, l.cur = true, first, cur
	return l
}

// Column is one column of a stage: its width in cells, its rows (each exactly Width cells, styled on its own: the palette is
// painted on each column's part of a row, so a column can be drawn apart from the others) and its parts.
type Column struct {
	Width int
	Lines []string
	Parts []Part
}

// Palette is the palette the screen is painted with, and whether it is painted (the vibes, in colour).
func (m Model) Palette() (art.Palette, bool) { return m.pal, m.vibes() }

// sheet builds rows together with their parts.
type sheet struct {
	rows  []string
	parts []Part
}

// add appends rows as a part of kind; a run of text, of plain rows or of the sung line joins the one before it.
func (s *sheet) add(kind PartKind, rows ...string) {
	if len(rows) == 0 {
		return
	}
	s.rows = append(s.rows, rows...)
	if n := len(s.parts); n > 0 && (kind == PartText || kind == PartRows || kind == PartLyric) && s.parts[n-1].Kind == kind {
		s.parts[n-1].Rows += len(rows)
		return
	}
	s.parts = append(s.parts, Part{Kind: kind, Rows: len(rows)})
}

// list appends rows that show l.
func (s *sheet) list(l *List, rows ...string) {
	if len(rows) == 0 {
		return
	}
	s.rows = append(s.rows, rows...)
	s.parts = append(s.parts, Part{Kind: PartList, Rows: len(rows), List: l})
}

// tabs appends row, whose tabs take the cells of spans.
func (s *sheet) tabs(row string, spans []span) {
	s.rows = append(s.rows, row)
	s.parts = append(s.parts, tabsPart(spans))
}

// tabsPart is the part of a row whose tabs take the cells of spans.
func tabsPart(spans []span) Part {
	tabs := make([]Tab, len(spans))
	for i, sp := range spans {
		tabs[i] = Tab{From: sp.from, To: sp.to}
	}
	return Part{Kind: PartTabs, Rows: 1, Tabs: tabs}
}

// keyTabs appends row, whose spans a click presses as their keys say (mouse).
func (s *sheet) keyTabs(row string, spans []span) {
	p := tabsPart(spans)
	p.keys = make([]string, len(spans))
	for i, sp := range spans {
		p.keys[i] = sp.key
	}
	s.rows = append(s.rows, row)
	s.parts = append(s.parts, p)
}

// fitParts cuts parts to n rows, or fills them up to n with blank rows.
func fitParts(parts []Part, n int) []Part {
	out := make([]Part, 0, len(parts)+1)
	left := n
	for _, p := range parts {
		if left == 0 {
			break
		}
		if p.Rows > left {
			p.Rows = left
			p = cutPart(p)
		}
		left -= p.Rows
		out = append(out, p)
	}
	if left > 0 {
		if k := len(out); k > 0 && out[k-1].Kind == PartRows {
			out[k-1].Rows += left
		} else {
			out = append(out, Part{Kind: PartRows, Rows: left})
		}
	}
	return out
}

// cutPart is p cut to its Rows: a stage's columns lose their rows below, a list its items.
func cutPart(p Part) Part {
	switch p.Kind {
	case PartList:
		p.List = listAt(p.List.Items[:p.Rows], 0, p.List.Selected)
	case PartStage:
		cols := make([]Column, len(p.Columns))
		for i, c := range p.Columns {
			cols[i] = Column{Width: c.Width, Lines: c.Lines[:min(len(c.Lines), p.Rows)], Parts: fitParts(c.Parts, p.Rows)}
		}
		p.Columns = cols
	}
	return p
}
