package frontend

import (
	"maps"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
	"github.com/stencil-hq/tern-sdk/go/tern/ui"
	"golang.org/x/text/width"
)

// PiG plays its fullscreen mode's animations, such as the easter eggs of the
// header's logo and /pigsayhi, as a fullscreen overlay. Tern draws it on a
// screen surface opened over the inline one: it covers the pane, takes the
// pointer and the view events (resize, visible) while it shows, and leaves
// the inline surface exactly as it was when it closes, so nothing is sent
// again. Keys still reach PiG on the pty, which hands them to the overlay.
// The screen surface's dock mirrors the inline one, so the editor and the
// status strip stay as Tern draws them, and the overlay plays in main above
// them, as one ansi node. The SDK's render sends the latest view's
// difference whenever Tern acknowledges the last one.

// screenID is the screen surface's id and role.
const screenID = "pig.screen"

// A screen surface's main region holds fewer rows than the pane's terminal
// has. In Tern 0.4.5 the screen covers the grid less 10px, which is no more
// than the grid keeps under its last row; main's first line sits
// screenTopPx down; and the dock under it is screenDockPx tall with a
// one-line editor and the status strip (its 24px top padding, a 28px
// editor, a 4px gap and a 24px strip), each further line of the editor's
// text a row more and a working indicator a strip more. Programs cannot
// style the regions themselves, so the overlay is that much shorter, in
// whole rows.
const (
	screenTopPx   = 6
	screenDockPx  = 80
	screenStripPx = 24
)

// screen is the fullscreen overlay shown, and the surface it shows on.
type screen struct {
	// id is the overlay's PiG id, and node the overlay as last applied.
	id   string
	node fe.Overlay
	// sf is the open screen surface, or nil, and dirty reports a node or a
	// dock no render took yet.
	sf    *tern.Surface
	dirty bool
}

// Screen reports the screen a fullscreen overlay covers: the pane, which is
// PiG's terminal, less the rows the screen surface's padding and its dock
// take, since the session shows the dock itself. Tern draws main centered
// on the Reader measure. Main's first node shows a row down, where PiG's
// first line, the blank spacer above the header that Tern hides, would be,
// so main's lines line up from the screen's first row.
func (s *session) Screen() (fe.Screen, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tsp == nil || s.sf == nil || s.closed || s.fellBack || s.suspended {
		return fe.Screen{}, false
	}
	short := float64(screenTopPx + screenDockPx)
	for _, e := range s.dock {
		if _, ok := e.node.(fe.Working); ok {
			short += screenStripPx
		}
	}
	if s.hasEditor {
		short += float64(strings.Count(s.editor.Text, "\n")) * s.cellHeightPx
	}
	return fe.Screen{
		// One column fewer than the pane's, so a cell width Tern rounds
		// in its reports never wraps a full line.
		Columns:      -1,
		Rows:         -int(math.Ceil(short / s.cellHeightPx)),
		MainColumn:   readerMainColumn(s.cols),
		ShowsDock:    true,
		Hidden:       s.hidden,
		ReduceMotion: s.reduceMotion,
	}, true
}

// applyScreen keeps a fullscreen overlay to show, or forgets the one shown
// when PiG removes it, closing its surface.
func (s *session) applyScreen(op fe.Op) {
	if op.Kind == fe.Remove {
		if s.screen != nil && s.screen.id == op.ID {
			if s.screen.sf != nil {
				_ = s.sendErr(s.screen.sf.Remove())
			}
			s.screen = nil
		}
		return
	}
	overlay := op.Node.(fe.Overlay)
	if s.screen == nil || s.screen.id != op.ID {
		if s.screen != nil && s.screen.sf != nil {
			_ = s.sendErr(s.screen.sf.Remove())
		}
		s.screen = &screen{id: op.ID}
	}
	s.screen.node = overlay
	s.screen.dirty = true
}

// isScreenOp reports an op on the fullscreen overlay: one that adds or
// changes a fullscreen overlay, or removes the one shown.
func (s *session) isScreenOp(op fe.Op) bool {
	if op.Kind == fe.Remove {
		return s.screen != nil && s.screen.id == op.ID
	}
	overlay, ok := op.Node.(fe.Overlay)
	return ok && overlay.Fullscreen
}

// flushScreen opens the screen surface for the overlay to show and renders
// its lines and the dock, unless the pane is hidden: PiG pauses its
// animation then, and the latest view goes out when it shows again.
func (s *session) flushScreen() error {
	sc := s.screen
	if sc == nil || !sc.dirty || s.hidden {
		return nil
	}
	if sc.sf == nil {
		sf, err := s.tsp.Open(tern.SurfaceOptions{ID: screenID, Mode: tern.Screen, Title: "pig", Role: screenID, Discard: true})
		if err != nil {
			return err
		}
		sc.sf = sf
	}
	sc.dirty = false
	return sc.sf.Render(s.screenView(sc))
}

// screenView is the overlay's lines as one ansi node of main that takes a
// click anywhere when the overlay does, over the mirrored dock. One node,
// not a node per line: Tern virtualizes main's blocks and lays out those
// below by an estimate it takes from their text, escapes included, so lines
// with many colors were left out of view and never drawn. A frame then
// sends the screen's whole text, which Tern draws over the last.
func (s *session) screenView(sc *screen) tern.View {
	lines := tern.Node{Kind: tern.KindAnsi, Props: map[string]any{"key": "screen", "text": compactSGR(linesText(sc.node.Lines))}}
	var click func()
	if area := sc.node.Click; area != nil {
		id, pigClick := sc.id, fe.Click{Node: sc.id, Row: area.Row, Column: area.Column}
		click = func() {
			s.mu.Lock()
			shown := s.screen != nil && s.screen.id == id && !s.closed
			s.mu.Unlock()
			if shown && s.env.Click != nil {
				s.env.Click(pigClick)
			}
		}
		lines.Handlers.OnClick = func(*tern.Action) { click() }
	}
	return tern.View{Main: tern.Nodes{lines}, Dock: s.screenDock(click)}
}

// screenDock mirrors the inline surface's dock: its nodes, built by the same
// code with their current props, so the screen surface shows the editor and
// the status strip as the inline one does. The mirrored editor shows the
// draft but takes neither the keys, which go to the overlay, nor a prompt:
// it is not focused and not sendable. A click on a mirrored node is a click
// on the overlay, which covers the dock in the terminal: the mirrored
// views' elements take none of their own.
func (s *session) screenDock(click func()) tern.Nodes {
	dock := tern.Nodes{}
	for _, e := range s.dock {
		if isLayerNode(e.node) {
			continue
		}
		el := s.element(e, false)
		if editor, ok := e.node.(fe.Editor); ok {
			cursor := editor.Cursor
			el = ui.Editor{Common: ui.Common{Key: e.id}, Field: ui.Field{Text: editor.Text, Cursor: &cursor, Placeholder: editor.Placeholder}}
		}
		node := buildNode(el)
		if click != nil {
			node.Handlers = tern.Handlers{OnClick: func(*tern.Action) { click() }, OnFocus: func(*tern.Focus) { click() }}
		}
		dock = append(dock, node)
	}
	return dock
}

// suspendScreen removes the screen surface while PiG lends the terminal out;
// the overlay shows again on the next flush after Resume.
func (s *session) suspendScreen() {
	if s.screen != nil && s.screen.sf != nil {
		_ = s.sendErr(s.screen.sf.Remove())
		s.screen.sf = nil
		s.screen.dirty = true
	}
}

// clickID is the TSP id of the cells of a transcript node that take a
// click.
func clickID(id string) string { return id + ".hit" }

// transcriptClick turns Tern's click on a transcript node's click cells into
// PiG's click on the first cell of its Click area: Tern does not say which
// cell was clicked.
func (s *session) transcriptClick(action *tern.Action) {
	if action.SF != surfaceID || action.Act != "click" {
		return
	}
	nodeID, ok := strings.CutSuffix(action.ID, ".hit")
	if !ok {
		return
	}
	pigID, ok := strings.CutPrefix(nodeID, "n.")
	if !ok {
		return
	}
	s.mu.Lock()
	var click *fe.Click
	if e := s.sent[pigID]; e != nil && !s.closed {
		if lines, ok := e.node.(fe.Lines); ok && lines.Click != nil {
			click = &fe.Click{Node: pigID, Row: lines.Click.Row, Column: lines.Click.Column}
		}
	}
	s.mu.Unlock()
	if click != nil && s.env.Click != nil {
		s.env.Click(*click)
	}
}

// clickLinesNode draws lines whose Click area takes a click: the lines above
// and below the area as ansi nodes, and the area's lines as a row of the
// cells left of the area, the area, which takes the click, and the cells
// right of it, each sized in cells so the three line up as the lines do.
// A part with nothing in it hides, so the node keeps its children.
func clickLinesNode(id string, lines []string, area fe.Area) tern.WireNode {
	end := min(len(lines), area.Row+area.Rows)
	start := min(area.Row, end)
	var left, hit, right []string
	for _, line := range lines[start:end] {
		l, h, r := cutColumns(line, area.Column, area.Column+area.Columns)
		left, hit, right = append(left, l), append(hit, h), append(right, r)
	}
	cells := func(n int) map[string]any { return map[string]any{"w": strconv.Itoa(n) + "ch"} }
	part := func(suffix string, lines []string, props map[string]any) tern.WireNode {
		p := map[string]any{"text": compactSGR(linesText(lines)), "hidden": len(lines) == 0}
		maps.Copy(p, props)
		return tern.WireNode{ID: id + suffix, K: tern.KindAnsi, P: p}
	}
	return tern.WireNode{ID: id, K: tern.KindCol, C: []tern.WireNode{
		part(".above", lines[:start], nil),
		{ID: id + ".area", K: tern.KindRow, P: map[string]any{"align": "start"}, C: []tern.WireNode{
			part(".left", left, map[string]any{"min": cells(area.Column), "max": cells(area.Column), "hidden": area.Column == 0 || len(left) == 0}),
			part(".hit", hit, map[string]any{"min": cells(area.Columns), "max": cells(area.Columns), "actions": map[string]any{"click": "click"}}),
			part(".right", right, map[string]any{"grow": 1}),
		}},
		part(".below", lines[end:], nil),
	}}
}

// cutColumns cuts an ANSI line at two cell columns. The middle and right
// parts start with the SGR sequences before them since the last reset, so
// each draws in the colors and styles it had in the whole line. A wide character that spans
// a cut goes to the part it starts in.
func cutColumns(line string, from, to int) (left, middle, right string) {
	var parts [3]strings.Builder
	var sgr strings.Builder
	part, column := 0, 0
	for i := 0; i < len(line); {
		if line[i] == 0x1b {
			n := escapeLen(line[i:])
			seq := line[i : i+n]
			switch {
			case seq == "\x1b[0m" || seq == "\x1b[m":
				// A reset ends the state to carry: carrying every sequence
				// before it made each part of a colorful line, such as the
				// header's pig, as long as the whole line again, and Tern,
				// which estimates a block's height from its text, then
				// left the header undrawn in a narrow pane.
				sgr.Reset()
			case strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m"):
				sgr.WriteString(seq)
			}
			parts[part].WriteString(seq)
			i += n
			continue
		}
		r, n := utf8.DecodeRuneInString(line[i:])
		w := runeCells(r)
		for w > 0 && part < 2 && column >= [2]int{from, to}[part] {
			part++
			parts[part].WriteString(sgr.String())
		}
		parts[part].WriteString(line[i : i+n])
		column += w
		i += n
	}
	return parts[0].String(), parts[1].String(), parts[2].String()
}

// escapeLen is the length of the escape sequence at the start of s: a CSI
// sequence up to its final byte, an OSC, DCS or APC string up to its BEL or
// ST, or ESC and one byte.
func escapeLen(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']', 'P', '_':
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	}
	return 2
}

// runeCells is the cells a rune takes: two for an East Asian wide one, none
// for a combining mark or a format character, else one.
func runeCells(r rune) int {
	switch {
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0
	case width.LookupRune(r).Kind() == width.EastAsianWide || width.LookupRune(r).Kind() == width.EastAsianFullwidth:
		return 2
	}
	return 1
}

// compactSGR drops the SGR sequences of ANSI text that do not change how
// the next character draws: a run of sequences between two characters goes
// out only when the state it leads to, the sequences since the last reset,
// differs from the one the previous character drew in. PiG's pig art sets
// and resets the color around every cell, which multiplies a line's length
// several times over, and Tern estimates the height of a block it has not
// laid out from its text, escapes included: a narrow pane then estimated
// the header's and the screen's blocks so tall that it never drew them.
// A newline keeps the state, as in a terminal.
func compactSGR(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	var current, pending strings.Builder
	reset := false
	for i := 0; i < len(text); {
		c := text[i]
		if c == 0x1b {
			n := escapeLen(text[i:])
			seq := text[i : i+n]
			i += n
			switch {
			case seq == "\x1b[0m" || seq == "\x1b[m":
				pending.Reset()
				reset = true
			case strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m"):
				// A sequence that repeats the last one changes nothing.
				state := pending.String()
				if !reset {
					state = current.String() + state
				}
				if !strings.HasSuffix(state, seq) {
					pending.WriteString(seq)
				}
			default:
				out.WriteString(seq)
			}
			continue
		}
		if c != '\n' && (reset || pending.Len() > 0) {
			next := pending.String()
			if !reset {
				next = current.String() + next
			}
			if next != current.String() {
				if reset {
					out.WriteString("\x1b[0m")
				}
				out.WriteString(pending.String())
				current.Reset()
				current.WriteString(next)
			}
			pending.Reset()
			reset = false
		}
		out.WriteByte(c)
		i++
	}
	if reset {
		out.WriteString("\x1b[0m")
	}
	out.WriteString(pending.String())
	return out.String()
}
