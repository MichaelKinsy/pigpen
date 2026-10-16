package pig_music

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/art"
	"github.com/MichaelKinsy/pigpen/pig-music/ui"
)

// pig additive (D103): the screens go to PiG as a kit view, the extension component kit's description of a surface, so a
// frontend (Tern) can draw the screen with elements of its own: the cover as a picture, the play bar as a bar, text as text.
// The host renders the view into the terminal's rows, and every node here is chosen so that those rows are the screen's own:
// a run of rows is a `lines` node (drawn as it is), a row of text a `truncated-text` node only when PiG measures it as this
// extension does (textSafe), the Player screen's columns an hstack only when every row of every column passes the same
// test (the hstack puts a reset between the columns; each column is painted on its own, so the cells stay the same), and a
// row of tabs an hstack of text nodes, one per tab and one per gap between them, each carrying the styles in force where it
// starts, so a frontend's click on a tab names the tab's first cell.

// drawing is what the screen's host keeps with each drawing: the parts of its rows and the palette's theme.
type drawing struct {
	parts []ui.Part
	theme map[string]string
}

// drawScreen is the player host's Draw: the model's rows with their parts and its palette as theme overrides.
func drawScreen(m tea.Model) (string, any) {
	switch m := m.(type) {
	case ui.Model:
		text, parts := m.Draw()
		return text, drawing{parts: parts, theme: paletteTheme(m.Palette())}
	case ui.QuickModel:
		text, parts := m.Draw()
		return text, drawing{parts: parts}
	}
	return m.View().Content, nil
}

// paletteTheme is the palette the screen is painted with as Pi's theme tokens, nil when the screen is not painted.
func paletteTheme(p art.Palette, painted bool) map[string]string {
	if !painted {
		return nil
	}
	return map[string]string{"accent": rgbHex(p.Accent), "muted": rgbHex(p.Dim), "dim": rgbHex(p.Dim), "text": rgbHex(p.Text), "error": rgbHex(p.Error)}
}

func rgbHex(c art.RGB) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// screenView is the kit view of a screen's lines (exactly width cells each) with what its host kept with them.
func screenView(lines []string, shape any, width int) kit.View {
	d, _ := shape.(drawing)
	return kit.View{Root: screenNode(lines, d.parts, width), Theme: d.theme}
}

// screenNode is the node of lines cut into parts; lines the parts do not describe are one lines node.
func screenNode(lines []string, parts []ui.Part, width int) kit.Node {
	if partRows(parts) != len(lines) {
		return kit.NewLines(lines)
	}
	root := kit.NewContainer()
	row := 0
	for _, p := range parts {
		addPart(root, p, lines[row:row+p.Rows], width)
		row += p.Rows
	}
	return root
}

func partRows(parts []ui.Part) int {
	n := 0
	for _, p := range parts {
		n += p.Rows
	}
	return n
}

// addPart adds the nodes of rows, which show p, to c.
func addPart(c *kit.Container, p ui.Part, rows []string, width int) {
	switch p.Kind {
	case ui.PartText:
		addText(c, rows, width)
	case ui.PartLyric:
		sung := kit.NewContainer()
		addText(sung, rows, width)
		c.AddChild(sung)
	case ui.PartBar:
		bar := kit.NewLines(rows)
		if p.Duration > 0 {
			bar.Progress = &kit.Progress{Value: p.Position.Seconds(), Max: p.Duration.Seconds()}
		}
		c.AddChild(bar)
	case ui.PartCover:
		cover := kit.NewLines(rows)
		if p.Cover != nil {
			cover.Image = &kit.LinesImage{Data: p.Cover.Data, MimeType: p.Cover.MimeType}
		}
		c.AddChild(cover)
	case ui.PartStage:
		c.AddChild(stageNode(p, rows, width))
	case ui.PartTabs:
		addTabs(c, p, rows, width)
	case ui.PartList:
		list := kit.NewLines(rows)
		if p.List != nil && len(p.List.Items) == len(rows) {
			items := make([]kit.ListItem, len(rows))
			for i, it := range p.List.Items {
				items[i] = kit.ListItem{Label: it.Label, Detail: it.Detail, Columns: it.Columns}
			}
			list.List = &kit.List{Items: items, Selected: p.List.Selected}
		}
		c.AddChild(list)
	default: // PartRows
		c.AddChild(kit.NewLines(rows))
	}
}

// addText adds rows of text: a truncated-text node for each row PiG measures as this extension does, the others as lines.
func addText(c *kit.Container, rows []string, width int) {
	var other []string
	flush := func() {
		if len(other) > 0 {
			c.AddChild(kit.NewLines(other))
			other = nil
		}
	}
	for _, r := range rows {
		if textSafe(r, width) {
			flush()
			c.AddChild(kit.NewTruncatedText(r, 0, 0))
			continue
		}
		other = append(other, r)
	}
	flush()
}

// stageNode is the Player screen's columns side by side, or its rows as they are when a column holds a row PiG might
// measure otherwise than this extension (the hstack would lay it out by PiG's measure).
func stageNode(p ui.Part, rows []string, width int) kit.Node {
	total := 0
	for _, col := range p.Columns {
		total += col.Width
		if len(col.Lines) != len(rows) || partRows(col.Parts) != len(rows) {
			return kit.NewLines(rows)
		}
		for _, l := range col.Lines {
			if !textSafe(l, col.Width) {
				return kit.NewLines(rows)
			}
		}
	}
	if total != width {
		return kit.NewLines(rows)
	}
	stack := kit.NewHStack()
	none := 0
	for _, col := range p.Columns {
		basis := col.Width
		stack.AddChild(kit.Entry(screenNode(col.Lines, col.Parts, col.Width), kit.StackEntryOptions{Basis: &basis, Grow: &none, Shrink: &none}))
	}
	return stack
}

// addTabs adds a row of tabs to c as an hstack of text nodes of fixed widths, one per tab. Tern drops a node of spaces alone,
// and the gap it held with it, so a gap between two tabs and the rest of the row are no nodes of their own: they end the node
// before them. A row's margin is its first tab's (the screen counts a click there as that tab's), and cells before the first
// tab that are not spaces alone (a heading before its key) are a node of their own. When the tabs do not fit the row (a
// terminal narrower than the tab bar), or a node would not hold exactly its cells as PiG measures them, it adds the row as
// addText does.
func addTabs(c *kit.Container, p ui.Part, rows []string, width int) {
	if stack, ok := tabsStack(p.Tabs, rows, width); ok {
		c.AddChild(stack)
		return
	}
	addText(c, rows, width)
}

func tabsStack(tabs []ui.Tab, rows []string, width int) (kit.Node, bool) {
	if len(rows) != 1 || !textSafe(rows[0], width) {
		return nil, false
	}
	cuts := make([]int, 0, 2*len(tabs)+2)
	cuts = append(cuts, 0)
	for _, t := range tabs {
		cuts = append(cuts, t.From, t.To)
	}
	cuts = append(cuts, width)
	var runs [][2]int // the nodes' cells, [from, to)
	for i := 1; i < len(cuts); i++ {
		from, to := cuts[i-1], cuts[i]
		if to < from || (from == to && i%2 == 0) { // out of order, past the row's end, or an empty tab
			return nil, false
		}
		if from == to {
			continue // no cells before the first tab, between two tabs, or after the last
		}
		cells, ok := cutCells(rows[0], from, to, width)
		if !ok {
			return nil, false
		}
		if i%2 == 1 && len(runs) > 0 && strings.TrimLeft(ansi.Strip(cells), " ") == "" { // a gap, or the rest of the row
			runs[len(runs)-1][1] = to
			continue
		}
		runs = append(runs, [2]int{from, to})
	}
	stack := kit.NewHStack()
	none := 0
	for _, r := range runs {
		cells, ok := cutCells(rows[0], r[0], r[1], width)
		if !ok || !textSafe(cells, r[1]-r[0]) {
			return nil, false
		}
		basis := r[1] - r[0]
		stack.AddChild(kit.Entry(kit.NewTruncatedText(cells, 0, 0), kit.StackEntryOptions{Basis: &basis, Grow: &none, Shrink: &none}))
	}
	return stack, true
}

// cutCells is the cells [from, to) of s, a row of width cells that passes textSafe, drawn on their own: every SGR sequence
// before from (so the cells keep the styles in force there, after the reset the hstack puts before them), the cells and the
// sequences among them, and those after the row's last cell when to is its end. It is false when a wide character straddles
// from or to.
func cutCells(s string, from, to, width int) (string, bool) {
	var b strings.Builder
	col := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			n := sgrLen(s[i:])
			if col < to || to == width {
				b.WriteString(s[i : i+n])
			}
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		w := ansi.StringWidth(string(r))
		switch {
		case col < from && col+w > from, col < to && col+w > to:
			return "", false
		case col >= from && col+w <= to:
			b.WriteString(s[i : i+size])
		}
		col += w
		i += size
	}
	return b.String(), true
}

// textSafe reports whether PiG's host measures s as width cells, as this extension does: its escapes are SGR sequences only,
// and every character is one whose width PiG and this extension agree on (narrow letters and symbols, box drawing, blocks,
// braille, and the wide CJK ranges). The kitparity check runs every one of them through PiG's own measure.
func textSafe(s string, width int) bool {
	if ansi.StringWidth(s) != width {
		return false
	}
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			n := sgrLen(s[i:])
			if n == 0 {
				return false
			}
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if !agreedRune(r) {
			return false
		}
		i += size
	}
	return true
}

// sgrLen is the length of the SGR sequence s starts with, 0 when it starts with another escape.
func sgrLen(s string) int {
	if len(s) < 3 || s[1] != '[' {
		return 0
	}
	for i := 2; i < len(s); i++ {
		switch c := s[i]; {
		case c == 'm':
			return i + 1
		case c >= '0' && c <= '9', c == ';', c == ':':
		default:
			return 0
		}
	}
	return 0
}

// agreedRunes are the ranges of characters whose width PiG's measure and this extension's agree on (kitparity checks each).
var agreedRunes = [][2]rune{
	{0x20, 0x7e},     // ASCII
	{0xa0, 0xac},     // Latin-1, without the soft hyphen
	{0xae, 0x24f},    // Latin-1 and Latin Extended A and B
	{0x2010, 0x2027}, // dashes, quotes, the bullet, the ellipsis
	{0x2500, 0x259f}, // box drawing and block elements
	{0x266a, 0x266a}, // the note
	{0x2800, 0x28ff}, // braille (the disc)
	{0x3041, 0x3096}, // hiragana
	{0x30a1, 0x30fa}, // katakana
	{0x4e00, 0x9fff}, // CJK ideographs
	{0xac00, 0xd7a3}, // Hangul syllables
}

func agreedRune(r rune) bool {
	for _, g := range agreedRunes {
		if r >= g[0] && r <= g[1] {
			return true
		}
	}
	return false
}
