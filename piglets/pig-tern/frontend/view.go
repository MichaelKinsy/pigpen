package frontend

import (
	"bytes"
	"image"
	_ "image/gif" // DecodeConfig reads an image's natural size.
	_ "image/jpeg"
	_ "image/png"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
)

// An extension surface whose lines Pi's tui components drew carries their
// structure, its view (D102): the transcript's lines and a tool's result,
// the dock's widgets, header and footer, and an extension's overlay. Tern
// draws each component as its own element: a container or a vstack as a
// col, an hstack as a row whose children keep their stack entry options as
// flex props, a box as a card (tinted when it fills a background, bare
// otherwise), text as styled spans, markdown as md, a spacer, a dynamic
// border as a rule, a select or settings list as a list, an image and a
// lines range that depicts one as an image blob in the image's cells, a
// loader as a spinner, a lines range that shows a position as its text
// around a progress bar, a lines range that is a list as a list, and any
// other lines as ansi. A node the member cannot draw (an unknown kind,
// markdown that carries escape sequences, an image without bytes) draws the
// rows it covers, cut from the surface's lines, as ansi. A view whose rows
// are not the surface's lines draws the lines.
//
// The view's own theme overrides apply to its element only: its root, a
// col with the role "pig.view.<id>", sets Tern's color variables for the
// overridden tokens in the member's view stylesheet (viewSheet), corrected
// to read on the backgrounds they land on in light and dark (contrast.go).
// Spans and tones name Pi's tokens, which the surface palette colors.
//
// A click on an element of an overlay's view or of a dock node's view is
// PiG's click (Env.Click) at that element's cell, named by the path of its
// view node (D102): on an item of a select or settings list, that item; on
// a row of a lines list, that row; on any other element, its first cell.
// PiG hands the click to the extension's component as the terminal's click
// at that cell in fullscreen mode, and does nothing otherwise. The views of
// the transcript and of a tool's result take no click: PiG has no
// component to hand one to.

// viewRole is the role of a view's root without theme overrides.
const viewRole = "pig.view"

// viewSheetName names the stylesheet of the views' theme overrides and
// background fills.
const viewSheetName = "pig-view"

// viewSheetBase colors a box or text that fills Pi's customMessageBg with
// the custom-message tint Tern takes from the palette.
const viewSheetBase = `.sf-card[data-role='pig.bg.customMessageBg']{background:var(--sf-tint-custom,var(--sf-card-bg))}`

// childPath is the view path of child i of the view node at path.
func childPath(path []int, i int) []int {
	return append(slices.Clip(path), i)
}

// viewer maps one view to Tern elements.
type viewer struct {
	s     *session
	lines []string
	// tokens maps a color, as #rrggbb, to the Pi token it draws; theme
	// is the theme overrides in effect, the view's and its ancestors'.
	tokens map[string]string
	theme  map[string]string
	// click sends PiG's click on the view's node, or is nil when the view
	// takes none.
	click func(fe.Click)
}

// region is the cells a node covers in the surface's lines.
type region struct{ row, col, rows, width int }

// validView reports whether v describes lines: its root covers them all.
func validView(v *fe.View, lines []string) bool {
	return v != nil && v.Root.Rows == len(lines)
}

// sameView reports whether two views are the same frame of their
// producer: the same Seq laid out at the same width.
func sameView(a, b *fe.View) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Seq == b.Seq && a.Root.Width == b.Root.Width
}

// hasView reports a dock or overlay node that draws a view.
func hasView(node fe.Node) bool {
	switch n := node.(type) {
	case fe.Lines:
		return validView(n.View, n.Lines)
	case fe.Overlay:
		return validView(n.View, n.Lines)
	}
	return false
}

// viewElement maps v, the structure of lines, to the element with key key
// whose TSP id is id. node is PiG's id of the node whose view v is, which a
// click on an element names, or "" for a view that takes no click.
func (s *session) viewElement(key, id string, v *fe.View, lines []string, node string) tern.Node {
	// The rules of the view's last mapping go; this one sets its own.
	s.dropViewRules(id)
	s.viewsShown = true
	b := &viewer{s: s, lines: lines, tokens: s.viewTokens(nil, v.Theme), theme: v.Theme}
	if send := s.env.Click; node != "" && send != nil {
		b.click = func(click fe.Click) {
			click.Node = node
			send(click)
		}
	}
	var root tern.Node
	if title, body, at, ok := framed(&v.Root); ok {
		// The card is the frame: its title, in strong text, heads the
		// body, and the borders go. The body is the frame's hstack's
		// middle child.
		root = tern.Node{Kind: tern.KindCol, Children: []tern.Element{
			tern.Node{Kind: tern.KindText, Props: hiddenIf(map[string]any{"spans": []any{map[string]any{"t": title, "s": "strong"}}}, title == "")},
			b.node(body, id+".0.1", at, []int{1, 1}),
		}}
	} else {
		root = b.node(&v.Root, id+".0", region{0, 0, v.Root.Rows, v.Root.Width}, []int{})
	}
	props := map[string]any{"role": s.themeRole(id, v.Theme, v.Theme, &v.Root)}
	if key != "" {
		props["key"] = key
	}
	return tern.Node{Kind: tern.KindCol, Props: props, Children: []tern.Element{root}}
}

// framed finds the frame PiG draws around a titled overlay's view: a
// container of the top border, an hstack of a "│" column, the body and a
// "│" column, padding rows and the bottom border. It returns the title in
// the top border, the body and the cells it covers.
func framed(root *fe.ViewNode) (title string, body *fe.ViewNode, at region, ok bool) {
	c := root.Children
	if root.Kind != fe.ViewKindContainer || len(c) < 3 || c[0].Kind != fe.ViewKindLines || c[0].Rows != 1 ||
		c[1].Kind != fe.ViewKindHStack || len(c[1].Children) != 3 || c[len(c)-1].Kind != fe.ViewKindLines {
		return "", nil, region{}, false
	}
	side := func(n *fe.ViewNode) bool {
		return n.Kind == fe.ViewKindLines && !slices.ContainsFunc(n.Lines, func(line string) bool { return strings.TrimSpace(stripANSI(line)) != "│" })
	}
	left, right := &c[1].Children[0], &c[1].Children[2]
	if !side(left) || !side(right) || len(c[0].Lines) != 1 {
		return "", nil, region{}, false
	}
	title = strings.Trim(stripANSI(c[0].Lines[0]), "─┌┐╭╮ ")
	body = &c[1].Children[1]
	return title, body, region{1, left.Width + c[1].Gap, body.Rows, body.Width}, true
}

// themeRole is the role of the element whose TSP id is id and that draws
// n with own overrides, under overrides in effect: its own when own holds
// an override, whose rule the view sheet then holds.
func (s *session) themeRole(id string, own, overrides map[string]string, n *fe.ViewNode) string {
	if len(own) == 0 {
		return viewRole
	}
	r := viewRule{own: own, overrides: overrides, fills: viewFills(n)}
	r.css = themeRule(id, own, overrides, s.paletteColors(), r.fills)
	if r.css == "" {
		return viewRole
	}
	s.setViewRule(id, r)
	return viewRole + "." + id
}

// viewRule is the theme rule of a view's element (themeRule) and what it
// takes besides the palette, so that a new palette recolors it.
type viewRule struct {
	own, overrides map[string]string
	fills          []string
	css            string
}

// paletteColors are the colors of PiG's palette, or nil before its theme.
func (s *session) paletteColors() map[string]string {
	if s.theme == nil {
		return nil
	}
	return s.theme.Colors
}

// rethemeViewRules recolors the views' rules in a new palette.
func (s *session) rethemeViewRules() {
	for id, r := range s.viewRules {
		r.css = themeRule(id, r.own, r.overrides, s.paletteColors(), r.fills)
		s.viewRules[id] = r
		s.viewSheetDirty = true
	}
}

// node maps n, which covers r and is at path in the view, under the TSP id
// id. A subtree with overrides of its own, such as one widget of the dock's
// view, is a col with its own role around n's element; the element takes
// n's clicks.
func (b *viewer) node(n *fe.ViewNode, id string, r region, path []int) tern.Node {
	if len(n.Theme) == 0 {
		return b.clickable(n, b.kind(n, id, r, path), path)
	}
	sub := *b
	sub.tokens = b.s.viewTokens(b.tokens, n.Theme)
	sub.theme = maps.Clone(b.theme)
	if sub.theme == nil {
		sub.theme = map[string]string{}
	}
	maps.Copy(sub.theme, n.Theme)
	return tern.Node{Kind: tern.KindCol, Props: map[string]any{"role": b.s.themeRole(id, n.Theme, sub.theme, n)}, Children: []tern.Element{
		sub.clickable(n, sub.kind(n, id+".0", r, path), path),
	}}
}

// clickable makes el, the element of n at path, take a click as PiG's click
// on n's first cell. A container's and a stack's children take their own
// clicks, and a list's items theirs. Tern sends a click to the one element
// it names, so a click inside a box's child is the child's only.
func (b *viewer) clickable(n *fe.ViewNode, el tern.Node, path []int) tern.Node {
	switch n.Kind {
	case fe.ViewKindContainer, fe.ViewKindVStack, fe.ViewKindHStack, fe.ViewKindSelectList, fe.ViewKindSettingsList:
		return el
	}
	if b.click == nil || el.Kind == tern.KindList {
		return el
	}
	click := b.click
	el.Handlers.OnClick = func(*tern.Action) { click(fe.Click{ViewPath: path}) }
	return el
}

// kind maps n, at path in the view, by its kind.
func (b *viewer) kind(n *fe.ViewNode, id string, r region, path []int) tern.Node {
	switch n.Kind {
	case fe.ViewKindContainer, fe.ViewKindVStack:
		props := map[string]any{}
		if n.Kind == fe.ViewKindVStack {
			addGap(props, n.Gap)
			if align := stackAlign(n.Align); align != "" {
				props["align"] = align
			}
		}
		children := make([]tern.Element, len(n.Children))
		row := r.row
		for i := range n.Children {
			child := &n.Children[i]
			children[i] = b.stackChild(child, id+"."+strconv.Itoa(i), region{row, r.col, child.Rows, r.width}, n, false, childPath(path, i))
			row += child.Rows + n.Gap*boolInt(n.Kind == fe.ViewKindVStack)
		}
		return tern.Node{Kind: tern.KindCol, Props: hiddenIf(props, n.Rows == 0), Children: children}
	case fe.ViewKindHStack:
		props := map[string]any{"align": "stretch"}
		addGap(props, n.Gap)
		if align := stackAlign(n.Align); align != "" {
			props["align"] = align
		}
		children := make([]tern.Element, len(n.Children))
		col := r.col
		for i := range n.Children {
			child := &n.Children[i]
			children[i] = b.stackChild(child, id+"."+strconv.Itoa(i), region{r.row, col, r.rows, child.Width}, n, true, childPath(path, i))
			col += child.Width + n.Gap
		}
		return tern.Node{Kind: tern.KindRow, Props: hiddenIf(props, n.Rows == 0), Children: children}
	case fe.ViewKindBox:
		inner := region{r.row + n.PaddingY, r.col + n.PaddingX, max(r.rows-2*n.PaddingY, 0), max(r.width-2*n.PaddingX, 0)}
		children := make([]tern.Element, len(n.Children))
		row := inner.row
		for i := range n.Children {
			child := &n.Children[i]
			children[i] = b.node(child, id+"."+strconv.Itoa(i), region{row, inner.col, child.Rows, inner.width}, childPath(path, i))
			row += child.Rows
		}
		if n.Bg == "" {
			// A box that fills nothing is its padding: a bare card, with no
			// ring, fill or fold.
			return tern.Node{Kind: tern.KindCard, Props: hiddenIf(map[string]any{"variant": "bare"}, n.Rows == 0), Children: children}
		}
		return tern.Node{Kind: tern.KindCard, Props: hiddenIf(bgProps(n.Bg), n.Rows == 0), Children: children}
	case fe.ViewKindText, fe.ViewKindTruncatedText:
		text := n.Text
		props := map[string]any{}
		if n.Kind == fe.ViewKindTruncatedText {
			text, _, _ = strings.Cut(text, "\n")
			props["wrap"], props["truncate"] = "none", "end"
		}
		b.setRich(props, "text", "spans", text)
		hidden := n.Rows == 0 || blank(text)
		if n.Kind == fe.ViewKindText && n.Bg != "" {
			return tern.Node{Kind: tern.KindCard, Props: hiddenIf(bgProps(n.Bg), hidden), Children: []tern.Element{
				tern.Node{Kind: tern.KindText, Props: props},
			}}
		}
		return tern.Node{Kind: tern.KindText, Props: hiddenIf(props, hidden)}
	case fe.ViewKindMarkdown:
		if strings.ContainsRune(n.Text, 0x1b) {
			break
		}
		return tern.Node{Kind: tern.KindMd, Props: hiddenIf(map[string]any{"text": n.Text}, n.Rows == 0 || blank(n.Text))}
	case fe.ViewKindSpacer:
		return tern.Node{Kind: tern.KindSpacer, Props: hiddenIf(map[string]any{"size": gapSize(n.Rows)}, n.Rows == 0)}
	case fe.ViewKindDynamicBorder:
		props := map[string]any{}
		if tone := tokenTone(n.Color); tone != "" {
			props["tone"] = tone
		}
		return tern.Node{Kind: tern.KindRule, Props: props}
	case fe.ViewKindSelectList, fe.ViewKindSettingsList:
		if n.Submenu != nil {
			// PiG reaches no node of an open submenu: a view path names
			// the settings list, whose rows it then refuses. The submenu
			// takes no click.
			sub := *b
			sub.click = nil
			return tern.Node{Kind: tern.KindCol, Children: []tern.Element{sub.node(n.Submenu, id+".0", r, nil)}}
		}
		return b.list(n, id, path)
	case fe.ViewKindImage:
		if node, ok := b.image(n.Image, n); ok {
			return node
		}
	case fe.ViewKindLoader:
		message := b.rich(n.Text, tokenStyle(n.MessageColor))
		if n.Frames == nil {
			// A loader without an indicator shows its message only.
			props := map[string]any{}
			if text, ok := message.(string); ok {
				props["text"] = text
			} else {
				props["spans"] = message
			}
			return tern.Node{Kind: tern.KindText, Props: props}
		}
		props := map[string]any{}
		if message != "" {
			props["label"] = message
		}
		if tone := tokenTone(n.Color); tone != "" {
			props["tone"] = tone
		}
		return tern.Node{Kind: tern.KindSpinner, Props: props}
	case fe.ViewKindLines:
		if n.Image != nil {
			if node, ok := b.image(n.Image, n); ok {
				return node
			}
		}
		if p := n.Progress; p != nil && p.Max > 0 {
			return progressNode(n.Lines, min(max(p.Value/p.Max, 0), 1))
		}
		if l := n.List; l != nil && len(l.Items) == len(n.Lines) {
			return b.linesList(l, id, path)
		}
		return ansiNode(n.Lines)
	}
	return ansiNode(b.cut(r))
}

// linesList draws rows that are a list, one item per row, as a list: each
// item's label, its detail and every column but the last after it, the
// last column (a table row's length or count) right-aligned, and the
// selection. Rows whose items are not one per row draw as they are. A
// click on an item, which Tern sends as the list's select, is PiG's click
// on its row, and the activate Tern sends after a double click's second
// select is that click again as a double click's second (Count 2).
func (b *viewer) linesList(l *fe.ViewList, id string, path []int) tern.Node {
	items := make([]tern.Element, len(l.Items))
	for i, item := range l.Items {
		props := map[string]any{}
		b.setRich(props, "label", "label", item.Label)
		detail, value := item.Detail, ""
		if n := len(item.Columns); n > 0 {
			detail = joinNonEmpty(" · ", append([]string{item.Detail}, item.Columns[:n-1]...)...)
			value = item.Columns[n-1]
		}
		if detail != "" {
			b.setRich(props, "detail", "detail", detail)
		}
		if value != "" {
			b.setRich(props, "value", "value", value)
		}
		items[i] = tern.Node{Kind: tern.KindItem, Props: props}
	}
	props := map[string]any{}
	if l.Selected >= 0 && l.Selected < len(items) {
		props["selected"] = id + "." + strconv.Itoa(l.Selected)
	}
	node := tern.Node{Kind: tern.KindList, Props: hiddenIf(props, len(items) == 0), Children: items}
	if b.click == nil {
		return node
	}
	// An item's id names the list's id, then its index.
	rows := make(map[string]int, len(items))
	for i := range items {
		rows[id+"."+strconv.Itoa(i)] = i
	}
	click := b.click
	node.Handlers.OnSelect = func(e *tern.Select) {
		if row, ok := rows[e.Item]; ok {
			click(fe.Click{ViewPath: path, Row: row})
		}
	}
	node.Handlers.OnActivate = func(e *tern.Activate) {
		if row, ok := rows[e.Item]; ok {
			click(fe.Click{ViewPath: path, Row: row, Count: 2})
		}
	}
	return node
}

// stackChild maps a stack's child and sets its stack entry options as flex
// props along the stack's axis: grow and shrink, a basis in cells as a
// fraction of the stack, and the minimum and maximum size in cells (an
// hstack) or lines (a vstack). A container's children have none.
func (b *viewer) stackChild(child *fe.ViewNode, id string, r region, stack *fe.ViewNode, horizontal bool, path []int) tern.Node {
	node := b.node(child, id, r, path)
	entry := child.Stack
	if entry == nil || stack.Kind == fe.ViewKindContainer {
		return node
	}
	props := maps.Clone(node.Props)
	if props == nil {
		props = map[string]any{}
	}
	if entry.Grow > 0 {
		props["grow"] = entry.Grow
	}
	if entry.Shrink != 1 {
		props["shrink"] = entry.Shrink
	}
	unit, size := "lines", stack.Rows
	if horizontal {
		unit, size = "ch", stack.Width
	}
	if entry.Basis >= 0 && size > 0 {
		props["basis"] = float64(entry.Basis) / float64(size)
	}
	if entry.MinSize > 0 {
		props["min"] = extent(horizontal, strconv.Itoa(entry.MinSize)+unit)
	}
	if entry.MaxSize >= 0 {
		props["max"] = extent(horizontal, strconv.Itoa(entry.MaxSize)+unit)
	}
	node.Props = props
	return node
}

func extent(horizontal bool, value string) map[string]any {
	if horizontal {
		return map[string]any{"w": value}
	}
	return map[string]any{"h": value}
}

// list maps a select list or a settings list to a list of its shown items,
// the selected one selected, the filter marked, at most MaxVisible rows
// tall, and the empty line Pi shows. A click on an item, which Tern sends
// as the list's select, is PiG's click on that item: a select list's by its
// value, a settings list's by its id. Tern sends a select per click and an
// activate after a double click's second: the activate is that click again
// as a double click's second (Count 2), which PiG counted already when it
// came within Pi's double-click interval.
func (b *viewer) list(n *fe.ViewNode, id string, path []int) tern.Node {
	settings := n.Kind == fe.ViewKindSettingsList
	keys := itemKeys(n.Items, settings)
	items := make([]tern.Element, len(n.Items))
	for i, item := range n.Items {
		props := map[string]any{"key": keys[i]}
		b.setRich(props, "label", "label", item.Label)
		if item.Description != "" {
			b.setRich(props, "detail", "detail", item.Description)
		}
		if settings {
			b.setRich(props, "value", "value", item.CurrentValue)
		}
		items[i] = tern.Node{Kind: tern.KindItem, Props: props}
	}
	props := map[string]any{"max": map[string]any{"lines": max(n.MaxVisible, 1)}}
	if n.Selected >= 0 && n.Selected < len(keys) {
		props["selected"] = id + "." + keys[n.Selected]
	}
	if n.Query != "" {
		props["filter"] = n.Query
	}
	switch {
	case !settings:
		props["empty"] = "No matching commands"
	case n.Query != "":
		props["empty"] = "No matching settings"
	default:
		props["empty"] = "No settings available"
	}
	node := tern.Node{Kind: tern.KindList, Props: props, Children: items}
	if b.click == nil {
		return node
	}
	// An item's id names the list's id, then its key.
	value := make(map[string]string, len(keys))
	for i, item := range n.Items {
		if settings {
			value[id+"."+keys[i]] = item.ID
		} else {
			value[id+"."+keys[i]] = item.Value
		}
	}
	click := b.click
	node.Handlers.OnSelect = func(e *tern.Select) {
		if item, ok := value[e.Item]; ok {
			click(fe.Click{ViewPath: path, Item: item})
		}
	}
	node.Handlers.OnActivate = func(e *tern.Activate) {
		if item, ok := value[e.Item]; ok {
			click(fe.Click{ViewPath: path, Item: item, Count: 2})
		}
	}
	return node
}

// itemKeys are the keys of a list's items: their values (a settings list's
// ids), which stay as a filter changes which items show, or their indexes
// when two are the same or one is empty.
func itemKeys(items []fe.ViewItem, settings bool) []string {
	keys := make([]string, len(items))
	seen := make(map[string]bool, len(items))
	for i, item := range items {
		key := item.Value
		if settings {
			key = item.ID
		}
		if key == "" || seen[key] {
			for j := range keys {
				keys[j] = strconv.Itoa(j)
			}
			return keys
		}
		seen[key] = true
		keys[i] = key
	}
	return keys
}

// image maps an image to an image node in the cells Pi draws it in, its
// bytes a blob sent once: an image node's width, at most its MaxWidthCells,
// or the widest of a lines node's rows, and the node's rows. An image
// without bytes, or over Tern's bound, is not drawn natively.
func (b *viewer) image(img *fe.ViewImage, n *fe.ViewNode) (tern.Node, bool) {
	if img == nil || len(img.Data) == 0 || len(img.Data) > tern.MaxBlob {
		return tern.Node{}, false
	}
	blob := b.s.blob(img)
	alt := img.Filename
	if alt == "" {
		alt = "image"
	}
	cols := n.Width
	if n.Kind == fe.ViewKindLines {
		cols = linesCells(n.Lines)
	} else if img.MaxWidthCells > 0 {
		cols = min(cols, img.MaxWidthCells)
	}
	props := map[string]any{"blob": blob, "alt": alt, "max": map[string]any{"w": strconv.Itoa(cols) + "ch", "h": strconv.Itoa(n.Rows) + "lines"}}
	if config, _, err := image.DecodeConfig(bytes.NewReader(img.Data)); err == nil {
		// Tern draws an image at its natural size within its bounds; a
		// small one, such as a cover thumbnail, would stay a dot in the
		// cells Pi fills. Scaled up, its bounds alone size it, in its ratio.
		scale := max(1, imageSizePx/max(config.Width, config.Height, 1))
		props["w"], props["h"] = config.Width*scale, config.Height*scale
	}
	return tern.Node{Kind: tern.KindImage, Props: props}, true
}

// imageSizePx is the least size an image's longer side reports to Tern.
const imageSizePx = 1024

// cut is the surface's lines in r as ansi text lines.
func (b *viewer) cut(r region) []string {
	start := min(max(r.row, 0), len(b.lines))
	end := min(start+max(r.rows, 0), len(b.lines))
	out := make([]string, 0, end-start)
	for _, line := range b.lines[start:end] {
		_, middle, _ := cutColumns(line, r.col, r.col+r.width)
		out = append(out, middle)
	}
	return out
}

// progressNode draws rows that show a position, value of 1, as their text
// around a native bar: the longest run of bar glyphs in the rows (at least
// minBarCells), the play bar's, becomes a progress bar, and the text left
// and right of it stays beside it in its cells, so a play bar keeps its
// times and icon. Rows above and below draw as they are. Rows without such
// a run keep their whole text, the bar after it. The node always has the
// same children, the empty ones hidden, so a moving position is one set.
func progressNode(lines []string, value float64) tern.Node {
	row, from, to := barRun(lines)
	var above, below []string
	left, right := lines, []string(nil)
	if row >= 0 {
		above, below = lines[:row], lines[row+1:]
		l, _, r := cutColumns(lines[row], from, to)
		left, right = []string{l}, []string{r}
	}
	// The text beside the bar keeps its cells, a column more against
	// rounding, as the member fits every line to Tern's blocks.
	side := func(lines []string) tern.Node {
		node := ansiNode(lines)
		w := map[string]any{"w": strconv.Itoa(linesCells(lines)+columnMargin) + "ch"}
		node.Props["min"], node.Props["max"] = w, w
		return node
	}
	return tern.Node{Kind: tern.KindCol, Children: []tern.Element{
		ansiNode(above),
		tern.Node{Kind: tern.KindRow, Props: map[string]any{"align": "center"}, Children: []tern.Element{
			side(left),
			tern.Node{Kind: tern.KindProgress, Props: map[string]any{"value": value, "grow": 1, "min": map[string]any{"w": strconv.Itoa(minBarCells) + "ch"}}},
			side(right),
		}},
		ansiNode(below),
	}}
}

// minBarCells is the shortest run of bar glyphs taken for a play bar.
const minBarCells = 3

// barRun finds the longest run of bar glyphs in lines, the first of the
// longest: its row and its cells [from, to), or row -1 without one.
func barRun(lines []string) (row, from, to int) {
	row = -1
	for i, line := range lines {
		column, start := 0, -1
		end := func() {
			if start >= 0 && column-start >= minBarCells && column-start > to-from {
				row, from, to = i, start, column
			}
			start = -1
		}
		for j := 0; j < len(line); {
			if line[j] == 0x1b {
				j += escapeLen(line[j:])
				continue
			}
			r, n := utf8.DecodeRuneInString(line[j:])
			j += n
			if isBarRune(r) {
				if start < 0 {
					start = column
				}
			} else {
				end()
			}
			column += runeCells(r)
		}
		end()
	}
	return row, from, to
}

// isBarRune reports a glyph progress bars are drawn with: horizontal box
// drawing lines, block elements and squares.
func isBarRune(r rune) bool {
	switch r {
	case '─', '━', '═', '▬', '■', '□', '▰', '▱':
		return true
	}
	return r >= 0x2580 && r <= 0x259f
}

// linesCells is the cells the widest of lines draws, without its padding.
func linesCells(lines []string) int {
	widest := 0
	for _, line := range strings.Split(linesText(lines), "\n") {
		cells := 0
		for i := 0; i < len(line); {
			if line[i] == 0x1b {
				i += escapeLen(line[i:])
				continue
			}
			r, n := utf8.DecodeRuneInString(line[i:])
			cells += runeCells(r)
			i += n
		}
		widest = max(widest, cells)
	}
	return widest
}

// ansiNode draws lines as ansi, as the member draws any lines.
func ansiNode(lines []string) tern.Node {
	text := compactSGR(linesText(lines))
	return tern.Node{Kind: tern.KindAnsi, Props: hiddenIf(map[string]any{"text": text}, blank(text))}
}

func hiddenIf(props map[string]any, hidden bool) map[string]any {
	if hidden {
		props["hidden"] = true
	}
	if len(props) == 0 {
		return nil
	}
	return props
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// addGap sets a stack's gap, cells or rows, as Tern's nearest gap.
func addGap(props map[string]any, gap int) {
	if gap > 0 {
		props["gap"] = gapSize(gap)
	}
}

// gapSize is Tern's space for n rows or cells.
func gapSize(n int) string {
	switch {
	case n <= 1:
		return "xs"
	case n == 2:
		return "sm"
	case n <= 4:
		return "md"
	}
	return "lg"
}

// stackAlign is a stack's cross-axis alignment as Tern's.
func stackAlign(align string) string {
	switch align {
	case "start", "center", "end", "stretch":
		return align
	}
	return ""
}

// bgProps are a card's props for a box or text that fills Pi's background
// token: the tone whose tint is that fill, and a role that names it (the
// view sheet tints customMessageBg; selectedBg selects the card).
func bgProps(token string) map[string]any {
	props := map[string]any{"role": "pig.bg." + token}
	switch token {
	case "toolSuccessBg":
		props["tone"] = "success"
	case "toolErrorBg":
		props["tone"] = "error"
	case "toolPendingBg":
		props["tone"] = "pending"
	case "userMessageBg":
		props["tone"] = "user"
	case "selectedBg":
		props["selected"] = true
	}
	return props
}

// tokenTone is the tone of a Pi foreground token, or "".
func tokenTone(token string) string {
	switch token {
	case "accent", "borderAccent":
		return "accent"
	case "success", "warning", "error", "muted":
		return token
	case "dim", "borderMuted":
		return "muted"
	}
	return ""
}

// tokenStyle is the span style of a Pi foreground token: its name, which
// Tern reads through the surface palette, or "" for none.
func tokenStyle(token string) string {
	if token == "" || strings.HasSuffix(token, "Bg") || !validToken.MatchString(token) {
		return ""
	}
	return token
}

var validToken = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// setRich sets text under textKey when it draws unstyled, or its spans
// under spansKey (which may be the same key: a rich prop takes either).
func (b *viewer) setRich(props map[string]any, textKey, spansKey, text string) {
	switch v := b.rich(text, "").(type) {
	case string:
		if v != "" {
			props[textKey] = v
		}
	default:
		props[spansKey] = v
	}
}

// rich is ANSI text as a rich value: its spans, or its plain text, which
// takes style when it has one.
func (b *viewer) rich(text, style string) any {
	spans, plain := ansiSpans(text, b.tokens)
	switch {
	case spans != nil:
		return spans
	case style != "" && plain != "":
		return []any{map[string]any{"t": plain, "s": style}}
	}
	return plain
}

// viewTokens maps colors to Pi's token names, so text an extension colored
// with a token draws in that token: base's (the theme's when nil), and
// overrides over them. Of two tokens of one color, a tone's name wins,
// then the first in name order.
func (s *session) viewTokens(base, overrides map[string]string) map[string]string {
	if base == nil {
		if s.themeTokens == nil && s.theme != nil {
			s.themeTokens = colorTokens(s.theme.Colors)
		}
		base = s.themeTokens
	}
	if len(overrides) == 0 {
		return base
	}
	tokens := maps.Clone(base)
	if tokens == nil {
		tokens = map[string]string{}
	}
	maps.Copy(tokens, colorTokens(overrides))
	return tokens
}

func colorTokens(colors map[string]string) map[string]string {
	names := slices.Sorted(maps.Keys(colors))
	slices.SortStableFunc(names, func(a, b string) int { return boolInt(tokenTone(b) == b) - boolInt(tokenTone(a) == a) })
	out := make(map[string]string, len(colors))
	for _, name := range names {
		hex := strings.ToLower(colors[name])
		if _, taken := out[hex]; !taken && tokenStyle(name) != "" {
			out[hex] = name
		}
	}
	return out
}

// spanStyle is the style ANSI text draws in.
type spanStyle struct {
	fg                           string
	bold, faint, italic, inverse bool
	href                         string
}

func (st spanStyle) tokens() string {
	var parts []string
	switch {
	case st.fg != "":
		parts = append(parts, st.fg)
	case st.faint:
		parts = append(parts, "dim")
	}
	if st.bold {
		parts = append(parts, "strong")
	}
	if st.italic {
		parts = append(parts, "em")
	}
	if st.inverse {
		parts = append(parts, "mark")
	}
	return strings.Join(parts, " ")
}

// ansiSpans translates ANSI text into Tern spans: a foreground color that
// is a theme token's (tokens) or a basic ANSI color with a meaning takes
// that token, bold is strong, faint dim, italic em, reverse video a mark,
// and an OSC 8 hyperlink the span's link. Other colors and backgrounds draw
// in the text's color. Text that draws in no style is plain, without its
// escape sequences, and spans is nil.
func ansiSpans(text string, tokens map[string]string) (spans []any, plain string) {
	if !strings.ContainsRune(text, 0x1b) {
		return nil, text
	}
	var st spanStyle
	var run strings.Builder
	var runStyle spanStyle
	flush := func() {
		if run.Len() == 0 {
			return
		}
		span := map[string]any{"t": run.String()}
		if s := runStyle.tokens(); s != "" {
			span["s"] = s
		}
		if runStyle.href != "" {
			span["href"] = runStyle.href
		}
		if n := len(spans); n > 0 {
			if last := spans[n-1].(map[string]any); last["s"] == span["s"] && last["href"] == span["href"] {
				last["t"] = last["t"].(string) + run.String()
				run.Reset()
				return
			}
		}
		spans = append(spans, span)
		run.Reset()
	}
	for i := 0; i < len(text); {
		if text[i] == 0x1b {
			n := escapeLen(text[i:])
			seq := text[i : i+n]
			i += n
			switch {
			case strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m"):
				st.applySGR(seq[2:len(seq)-1], tokens)
			case strings.HasPrefix(seq, "\x1b]8;"):
				body := strings.TrimSuffix(strings.TrimSuffix(seq[4:], "\x1b\\"), "\x07")
				_, uri, _ := strings.Cut(body, ";")
				st.href = uri
			}
			continue
		}
		_, n := utf8.DecodeRuneInString(text[i:])
		if run.Len() > 0 && st != runStyle {
			flush()
		}
		runStyle = st
		run.WriteString(text[i : i+n])
		i += n
	}
	flush()
	var b strings.Builder
	for _, span := range spans {
		m := span.(map[string]any)
		if len(m) > 1 {
			return spans, ""
		}
		b.WriteString(m["t"].(string))
	}
	return nil, b.String()
}

// applySGR applies an SGR sequence's parameters. A color takes its
// arguments from the parameters that follow (5;n or 2;r;g;b), or from its
// own in the ":" form.
func (st *spanStyle) applySGR(params string, tokens map[string]string) {
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		sub := strings.Split(fields[i], ":")
		code := sgrCode(sub[0])
		switch {
		case code == 0:
			*st = spanStyle{href: st.href}
		case code == 1:
			st.bold = true
		case code == 2:
			st.faint = true
		case code == 3:
			st.italic = true
		case code == 7:
			st.inverse = true
		case code == 22:
			st.bold, st.faint = false, false
		case code == 23:
			st.italic = false
		case code == 27:
			st.inverse = false
		case code >= 30 && code <= 37:
			st.fg = basicToken(code - 30)
		case code >= 90 && code <= 97:
			st.fg = basicToken(code - 90 + 8)
		case code == 39:
			st.fg = ""
		case code == 38 || code == 48:
			args := sub[1:]
			if len(sub) == 1 {
				// The ";" form: the color's arguments follow.
				rest := fields[i+1:]
				take := 0
				if len(rest) > 0 {
					switch sgrCode(rest[0]) {
					case 5:
						take = 2
					case 2:
						take = 4
					}
				}
				take = min(take, len(rest))
				args = rest[:take]
				i += take
			} else if len(args) == 5 && args[0] == "2" {
				// 38:2:colorspace:r:g:b
				args = append([]string{"2"}, args[2:]...)
			}
			if code == 38 {
				st.fg = colorToken(args, tokens)
			}
		}
	}
}

// colorToken is the token of an extended foreground color, or "".
func colorToken(args []string, tokens map[string]string) string {
	switch {
	case len(args) == 2 && args[0] == "5":
		if n := sgrCode(args[1]); n >= 0 && n < 16 {
			return basicToken(n)
		}
	case len(args) == 4 && args[0] == "2":
		var hex strings.Builder
		hex.WriteByte('#')
		for _, arg := range args[1:] {
			n := sgrCode(arg)
			if n < 0 || n > 255 {
				return ""
			}
			hex.WriteString(strconv.FormatInt(int64(n)|0x100, 16)[1:])
		}
		return tokens[hex.String()]
	}
	return ""
}

// basicToken is the token a basic ANSI color means: red an error, green a
// success, yellow a warning, blue info, cyan the accent, bright black dim.
// A theme that sets a token to one of these colors draws it in that color.
func basicToken(n int) string {
	switch n % 8 {
	case 1:
		return "error"
	case 2:
		return "success"
	case 3:
		return "warning"
	case 4:
		return "info"
	case 6:
		return "accent"
	case 0:
		if n == 8 {
			return "dim"
		}
	}
	return ""
}

// themeVars are the color variables of Tern that draw each overridable Pi
// token: its tone, its span color and the program palette token.
var themeVars = map[string][]string{
	"accent":       {"--accent", "--accent-ink", "--sf-accent", "--sf-c-accent", "--sf-p-accent"},
	"muted":        {"--sf-muted", "--sf-c-muted", "--sf-p-muted"},
	"dim":          {"--sf-c-dim", "--sf-p-dim"},
	"text":         {"--sf-p-text", "color"},
	"border":       {"--sf-p-border"},
	"borderAccent": {"--sf-p-borderAccent"},
	"borderMuted":  {"--sf-p-borderMuted"},
	"success":      {"--ok", "--sf-ok", "--sf-c-success", "--sf-p-success"},
	"warning":      {"--warn", "--sf-warn", "--sf-c-warning", "--sf-p-warning"},
	"error":        {"--bad", "--sf-bad", "--sf-c-error", "--sf-p-error"},
	// Fills tint at 60%, as Tern tints the palette's.
	"selectedBg":      {"--sf-sel", "--sf-mark-bg"},
	"customMessageBg": {"--sf-tint-custom"},
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// themeFg are the foreground tokens a view can override, in name order.
var themeFg = func() []string {
	var fg []string
	for token := range themeVars {
		if !isFill(token) {
			fg = append(fg, token)
		}
	}
	slices.Sort(fg)
	return fg
}()

// themeRule is the CSS that applies a view's theme overrides to its root,
// whose TSP id is id, or "" without any: own are the root's, overrides
// those in effect there (its ancestors' and its own), palette PiG's
// palette and fills the background tokens the root's boxes and text fill.
// Each text color reads on the background it lands on, in light and dark
// (contrast.go): the root's rule on the surface, a rule for its marks and
// selected items on the selection's tint, and one for each box's or text's
// card on its fill's tint. A tint's rule sets every color the root
// overrides; when the root overrides that fill, it also sets the text the
// root inherits onto it: the view's and the palette's colors as they read
// there, and Tern's defaults where they do not read.
func themeRule(id string, own, overrides, palette map[string]string, fills []string) string {
	var names, fg []string
	for token, color := range own {
		if _, ok := themeVars[token]; ok && hexColor.MatchString(color) {
			names = append(names, token)
			if !isFill(token) {
				fg = append(fg, token)
			}
		}
	}
	if len(names) == 0 {
		return ""
	}
	slices.Sort(names)
	slices.Sort(fg)
	root := `[data-role='` + cssString(viewRole+"."+id) + `']`
	var css strings.Builder
	surface, _ := newThemeScope(overrides, palette, "")
	var decls []string
	for _, token := range names {
		value := ""
		if isFill(token) {
			c, _ := parseRGB(own[token])
			value = "rgb(from " + c.String() + " r g b/60%)"
		} else {
			value = surface.value(token, true)
		}
		decls = appendDecls(decls, token, value)
	}
	css.WriteString(root + "{" + strings.Join(decls, ";") + "}")
	tint := func(selector, fill string, refill bool) {
		scope, ok := newThemeScope(overrides, palette, fill)
		if !ok {
			return
		}
		tokens := fg
		if refill {
			tokens = themeFg
		}
		decls = decls[:0]
		for _, token := range tokens {
			_, mine := parseRGB(own[token])
			decls = appendDecls(decls, token, scope.value(token, mine))
		}
		if len(decls) > 0 {
			css.WriteString(selector + "{" + strings.Join(decls, ";") + "}")
		}
	}
	_, ownSel := parseRGB(own["selectedBg"])
	tint(root+" .sf-t-mark,"+root+" mark.sf-mark,"+root+" .sf-item.sel", "selectedBg", ownSel)
	for _, token := range fills {
		card := cardFill(token)
		_, ownCard := parseRGB(own[card])
		_, overridable := themeVars[card]
		tint(root+" [data-role='"+cssString("pig.bg."+token)+"']", card, ownCard && overridable)
	}
	return css.String()
}

// appendDecls appends the declarations that set token's variables to
// value, unless value is "".
func appendDecls(decls []string, token, value string) []string {
	if value == "" {
		return decls
	}
	for _, name := range themeVars[token] {
		decls = append(decls, name+":"+value)
	}
	return decls
}

// viewFills are the background tokens n's boxes and text fill.
func viewFills(n *fe.ViewNode) []string {
	var fills []string
	var walk func(n *fe.ViewNode)
	walk = func(n *fe.ViewNode) {
		if n.Bg != "" && !slices.Contains(fills, n.Bg) {
			fills = append(fills, n.Bg)
		}
		for i := range n.Children {
			walk(&n.Children[i])
		}
	}
	walk(n)
	return fills
}

// cssString escapes s for a single-quoted CSS string.
func cssString(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\n", `\a `).Replace(s)
}

// setViewRule keeps the theme rule of the element whose TSP id is id.
func (s *session) setViewRule(id string, r viewRule) {
	if old, ok := s.viewRules[id]; ok && old.css == r.css {
		return
	}
	if s.viewRules == nil {
		s.viewRules = map[string]viewRule{}
	}
	s.viewRules[id] = r
	s.viewSheetDirty = true
}

// dropViewRules drops the theme rules of the views under the node whose
// TSP id is id.
func (s *session) dropViewRules(id string) {
	for root := range s.viewRules {
		if root == id || strings.HasPrefix(root, id+".") {
			delete(s.viewRules, root)
			s.viewSheetDirty = true
		}
	}
}

// viewSheet is the views' stylesheet: the fills, then each themed view's
// rule in id order.
func (s *session) viewSheet() string {
	var b strings.Builder
	b.WriteString(viewSheetBase)
	for _, id := range slices.Sorted(maps.Keys(s.viewRules)) {
		b.WriteString(s.viewRules[id].css)
	}
	return b.String()
}

// blob queues an image's bytes for Tern, once, and returns its blob id,
// which is its ref: both are the bytes' SHA-256 in lowercase hex.
func (s *session) blob(img *fe.ViewImage) string {
	id := img.Ref
	if id == "" {
		id = tern.BlobID(img.Data)
	}
	if !s.blobsSent[id] && !slices.ContainsFunc(s.blobQueue, func(q *fe.ViewImage) bool { return q.Ref == id }) {
		queued := *img
		queued.Ref = id
		s.blobQueue = append(s.blobQueue, &queued)
	}
	return id
}

// flushBlobs sends the images queued, before the frame that names them.
// Tern keeps blobs a node shows, and stores them by their hash, so each
// goes out once per session.
func (s *session) flushBlobs() error {
	for len(s.blobQueue) > 0 {
		img := s.blobQueue[0]
		if _, err := s.tsp.Blob(img.Data, img.MimeType); err != nil {
			return err
		}
		if s.blobsSent == nil {
			s.blobsSent = map[string]bool{}
		}
		s.blobsSent[img.Ref] = true
		s.blobQueue[0] = nil
		s.blobQueue = s.blobQueue[1:]
	}
	return nil
}

// flushViewSheet sends the views' stylesheet when it changed.
func (s *session) flushViewSheet() error {
	if !s.viewsShown || !s.viewSheetDirty && s.viewSheetSent {
		return nil
	}
	s.viewSheetDirty = false
	sheet := s.viewSheet()
	if s.viewSheetSent && sheet == s.viewSheetText {
		return nil
	}
	if err := s.sf.Stylesheet(viewSheetName, sheet); err != nil {
		return err
	}
	s.viewSheetSent, s.viewSheetText = true, sheet
	return nil
}

// wireNode is node as a raw TSP node with id id: its children's ids are
// id and their key (or index), as the reconciler derives them.
func wireNode(id string, node tern.Node) tern.WireNode {
	w := tern.WireNode{ID: id, K: node.Kind, P: node.Props}
	if len(node.Children) > 0 {
		w.C = make([]tern.WireNode, len(node.Children))
		for i, child := range node.Children {
			c := child.Node()
			key, ok := c.Props["key"].(string)
			if !ok {
				key = strconv.Itoa(i)
			}
			w.C[i] = wireNode(id+"."+key, c)
		}
	}
	return w
}
