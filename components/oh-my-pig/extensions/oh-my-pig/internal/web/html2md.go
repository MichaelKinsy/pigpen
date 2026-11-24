package web

// Standard-library HTML-to-Markdown converter for Fetch.
// Covers headings, paragraphs, blockquotes, links, images (dropping inline data: URIs),
// inline emphasis/code/strikethrough, ordered/unordered nested lists, fenced code blocks,
// and GFM pipe tables.
// upstream: crates/pi-natives/src/html.rs, packages/coding-agent/src/tools/fetch.ts:564-574

import (
	"fmt"
	"html"
	"regexp"
	"slices"
	"strings"
)

// droppedTags are elements whose entire subtree is removed (scripts, styles, nav, forms, embeds, SVG).
var droppedTags = map[string]bool{
	"script":   true,
	"style":    true,
	"noscript": true,
	"template": true,
	"head":     true,
	"nav":      true,
	"form":     true,
	"iframe":   true,
	"svg":      true,
	"canvas":   true,
	"video":    true,
	"audio":    true,
	"object":   true,
	"embed":    true,
	"button":   true,
	"input":    true,
	"select":   true,
	"textarea": true,
}

var voidTags = map[string]bool{
	"area":   true,
	"base":   true,
	"br":     true,
	"col":    true,
	"embed":  true,
	"hr":     true,
	"img":    true,
	"input":  true,
	"link":   true,
	"meta":   true,
	"param":  true,
	"source": true,
	"track":  true,
	"wbr":    true,
}

type htmlNodeKind int

const (
	nodeText htmlNodeKind = iota
	nodeElement
)

type htmlNode struct {
	kind     htmlNodeKind
	tag      string
	text     string
	attrs    map[string]string
	children []*htmlNode
}

// HTMLToMarkdown converts an HTML document to clean Markdown using the standard library only.
// Mirrors omp's htmlToMarkdown (html-to-markdown-rs 3.12.4 Tier2 with extract_metadata: true):
//   - Prepends YAML frontmatter from `<head>` (`base`, `canonical`, `meta-<name>`,
//     `meta-<property>`, `title`, sorted alphabetically; omitted when `<head>` yields no keys).
//   - Preserves link `href` and image `src` values verbatim without resolving against a base URL.
//
// The second parameter is kept for signature compatibility and is unused.
// upstream: crates/pi-natives/src/html.rs, html-to-markdown-rs 3.12.4 converter/main_helpers.rs,
// converter/inline/link.rs, converter/handlers/image.rs
func HTMLToMarkdown(rawHTML, _ string) string {
	root := parseHTMLTree(rawHTML)
	meta := extractHeadMetadata(root)
	conv := &mdConverter{}
	conv.renderChildren(root, renderState{})
	body := finalizeOutput(stripDataURIImages(conv.buf.String()))
	if len(meta) == 0 {
		return body
	}
	fm := formatMetadataFrontmatter(meta)
	if body == "" {
		return strings.TrimRight(fm, "\n")
	}
	return fm + "\n" + body
}

// extractHeadMetadata extracts YAML frontmatter key/value pairs from the first `<head>` element.
// Keys match html-to-markdown-rs 3.12.4 `converter/main_helpers.rs:extract_head_metadata`:
//   - `<title>` -> `title` (trimmed; omitted if empty)
//   - `<meta name="X" content="Y">` -> `meta-<X>: <Y>`
//   - `<meta property="X" content="Y">` -> `meta-<X>: <Y>`
//   - `<link rel="...canonical..." href="Y">` -> `canonical: <Y>`
//   - `<base href="Y">` -> `base: <Y>`
func extractHeadMetadata(root *htmlNode) map[string]string {
	head := findFirstHead(root)
	if head == nil {
		return nil
	}
	meta := map[string]string{}
	for _, ch := range head.children {
		if ch.kind != nodeElement {
			continue
		}
		switch ch.tag {
		case "title":
			title := strings.TrimSpace(collectPlainText(ch))
			if title != "" {
				meta["title"] = title
			}
		case "meta":
			content, hasContent := ch.attrs["content"]
			if !hasContent {
				continue
			}
			if name, ok := ch.attrs["name"]; ok {
				meta["meta-"+name] = content
			}
			if prop, ok := ch.attrs["property"]; ok {
				meta["meta-"+prop] = content
			}
		case "link":
			rel, hasRel := ch.attrs["rel"]
			href, hasHref := ch.attrs["href"]
			if hasRel && hasHref && strings.Contains(rel, "canonical") {
				meta["canonical"] = href
			}
		case "base":
			if href, ok := ch.attrs["href"]; ok {
				meta["base"] = href
			}
		}
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

func findFirstHead(node *htmlNode) *htmlNode {
	if node.kind == nodeElement && node.tag == "head" {
		return node
	}
	for _, ch := range node.children {
		if ch.kind == nodeElement {
			if found := findFirstHead(ch); found != nil {
				return found
			}
		}
	}
	return nil
}

// formatMetadataFrontmatter emits sorted `key: value` lines fenced by `---`.
// upstream: html-to-markdown-rs 3.12.4 converter/main_helpers.rs:format_metadata_frontmatter
func formatMetadataFrontmatter(meta map[string]string) string {
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(meta[k])
		b.WriteByte('\n')
	}
	b.WriteString("---\n")
	return b.String()
}

// parseHTMLTree builds a forgiving DOM tree from raw HTML.
func parseHTMLTree(src string) *htmlNode {
	root := &htmlNode{kind: nodeElement, tag: "document"}
	stack := []*htmlNode{root}

	n := len(src)
	i := 0
	for i < n {
		// Handle raw-text elements (script, style, textarea, title) until matching close tag.
		cur := stack[len(stack)-1]
		if cur.tag == "script" || cur.tag == "style" || cur.tag == "noscript" {
			closeTag := "</" + cur.tag
			idx := indexCaseInsensitive(src[i:], closeTag)
			if idx < 0 {
				break
			}
			i += idx
			end := strings.IndexByte(src[i:], '>')
			if end < 0 {
				break
			}
			i += end + 1
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			continue
		}

		lt := strings.IndexByte(src[i:], '<')
		if lt < 0 {
			appendText(cur, src[i:])
			break
		}
		if lt > 0 {
			appendText(cur, src[i:i+lt])
			i += lt
		}

		// Comment: <!-- ... -->
		if strings.HasPrefix(src[i:], "<!--") {
			end := strings.Index(src[i+4:], "-->")
			if end < 0 {
				break
			}
			i += 4 + end + 3
			continue
		}
		// Doctype or processing instruction: <!...> or <?...>
		if i+1 < n && (src[i+1] == '!' || src[i+1] == '?') {
			end := strings.IndexByte(src[i:], '>')
			if end < 0 {
				break
			}
			i += end + 1
			continue
		}
		// Closing tag: </name>
		if i+1 < n && src[i+1] == '/' {
			end := strings.IndexByte(src[i:], '>')
			if end < 0 {
				break
			}
			tagName := strings.ToLower(strings.TrimSpace(src[i+2 : i+end]))
			i += end + 1
			// Pop up to matching open tag if present on the stack.
			for k := len(stack) - 1; k >= 1; k-- {
				if stack[k].tag == tagName {
					stack = stack[:k]
					break
				}
			}
			continue
		}
		// Opening tag: <name attrs...>
		tagName, attrs, selfClose, nextPos, ok := parseStartTag(src, i)
		if !ok {
			appendText(cur, "<")
			i++
			continue
		}
		i = nextPos

		// Implicitly close sibling list items or table cells/rows when unclosed.
		stack = autoCloseImplicit(stack, tagName)
		cur = stack[len(stack)-1]

		elem := &htmlNode{
			kind:  nodeElement,
			tag:   tagName,
			attrs: attrs,
		}
		cur.children = append(cur.children, elem)
		if !selfClose && !voidTags[tagName] {
			stack = append(stack, elem)
		}
	}
	return root
}

func appendText(parent *htmlNode, raw string) {
	if raw == "" {
		return
	}
	parent.children = append(parent.children, &htmlNode{kind: nodeText, text: raw})
}

func indexCaseInsensitive(s, sub string) int {
	subLower := strings.ToLower(sub)
	n := len(subLower)
	if n == 0 || n > len(s) {
		return -1
	}
	for i := 0; i <= len(s)-n; i++ {
		if strings.EqualFold(s[i:i+n], subLower) {
			return i
		}
	}
	return -1
}

func parseStartTag(src string, start int) (tag string, attrs map[string]string, selfClose bool, next int, ok bool) {
	n := len(src)
	i := start + 1
	if i >= n || !isASCIILetter(src[i]) {
		return "", nil, false, start, false
	}
	nameStart := i
	for i < n && (isASCIILetter(src[i]) || (src[i] >= '0' && src[i] <= '9') || src[i] == '-' || src[i] == ':') {
		i++
	}
	tag = strings.ToLower(src[nameStart:i])

	for i < n {
		for i < n && isHTMLSpace(src[i]) {
			i++
		}
		if i >= n {
			return tag, attrs, false, n, true
		}
		if src[i] == '>' {
			return tag, attrs, false, i + 1, true
		}
		if src[i] == '/' && i+1 < n && src[i+1] == '>' {
			return tag, attrs, true, i + 2, true
		}
		// Attribute name
		kStart := i
		for i < n && !isHTMLSpace(src[i]) && src[i] != '=' && src[i] != '>' && src[i] != '/' {
			i++
		}
		if i == kStart {
			i++
			continue
		}
		key := strings.ToLower(src[kStart:i])
		for i < n && isHTMLSpace(src[i]) {
			i++
		}
		val := ""
		if i < n && src[i] == '=' {
			i++
			for i < n && isHTMLSpace(src[i]) {
				i++
			}
			if i < n && (src[i] == '"' || src[i] == '\'') {
				q := src[i]
				i++
				vStart := i
				for i < n && src[i] != q {
					i++
				}
				val = html.UnescapeString(src[vStart:i])
				if i < n {
					i++
				}
			} else {
				vStart := i
				for i < n && !isHTMLSpace(src[i]) && src[i] != '>' {
					i++
				}
				val = html.UnescapeString(src[vStart:i])
			}
		}
		if attrs == nil {
			attrs = map[string]string{}
		}
		if _, exists := attrs[key]; !exists {
			attrs[key] = val
		}
	}
	return tag, attrs, false, n, true
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isHTMLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

func autoCloseImplicit(stack []*htmlNode, nextTag string) []*htmlNode {
	top := stack[len(stack)-1].tag
	switch nextTag {
	case "li":
		if top == "li" {
			return stack[:len(stack)-1]
		}
	case "p":
		if top == "p" {
			return stack[:len(stack)-1]
		}
	case "td", "th":
		if top == "td" || top == "th" {
			return stack[:len(stack)-1]
		}
	case "tr":
		for len(stack) > 1 && (stack[len(stack)-1].tag == "td" || stack[len(stack)-1].tag == "th" || stack[len(stack)-1].tag == "tr") {
			stack = stack[:len(stack)-1]
		}
	case "dt", "dd":
		if top == "dt" || top == "dd" {
			return stack[:len(stack)-1]
		}
	}
	return stack
}

type renderState struct {
	inPre     bool
	listDepth int
}

type mdConverter struct {
	buf strings.Builder
}

func (c *mdConverter) ensureBlankLine() {
	s := c.buf.String()
	if s == "" {
		return
	}
	if strings.HasSuffix(s, "\n\n") {
		return
	}
	if strings.HasSuffix(s, "\n") {
		c.buf.WriteByte('\n')
		return
	}
	c.buf.WriteString("\n\n")
}

func (c *mdConverter) ensureNewline() {
	s := c.buf.String()
	if s == "" || strings.HasSuffix(s, "\n") {
		return
	}
	c.buf.WriteByte('\n')
}

func (c *mdConverter) renderChildren(node *htmlNode, st renderState) {
	for _, ch := range node.children {
		c.renderNode(ch, st)
	}
}

func (c *mdConverter) renderInlineText(node *htmlNode) string {
	sub := &mdConverter{}
	sub.renderChildren(node, renderState{})
	return strings.TrimSpace(sub.buf.String())
}

func (c *mdConverter) renderNode(node *htmlNode, st renderState) {
	if node.kind == nodeText {
		text := html.UnescapeString(node.text)
		if st.inPre {
			c.buf.WriteString(text)
			return
		}
		collapsed := collapseInlineSpace(text)
		if collapsed == "" {
			return
		}
		// Avoid leading space right after a newline or another space.
		if collapsed == " " {
			cur := c.buf.String()
			if cur == "" || strings.HasSuffix(cur, "\n") || strings.HasSuffix(cur, " ") {
				return
			}
		} else if strings.HasPrefix(collapsed, " ") {
			cur := c.buf.String()
			if cur == "" || strings.HasSuffix(cur, "\n") || strings.HasSuffix(cur, " ") {
				collapsed = collapsed[1:]
			}
		}
		c.buf.WriteString(collapsed)
		return
	}

	if droppedTags[node.tag] {
		return
	}

	switch node.tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(node.tag[1] - '0')
		text := normalizeSearchText(c.renderInlineText(node))
		if text == "" {
			return
		}
		c.ensureBlankLine()
		c.buf.WriteString(strings.Repeat("#", level))
		c.buf.WriteByte(' ')
		c.buf.WriteString(text)
		c.buf.WriteString("\n\n")

	case "p":
		text := c.renderInlineText(node)
		if text == "" {
			return
		}
		c.ensureBlankLine()
		c.buf.WriteString(text)
		c.buf.WriteString("\n\n")

	case "br":
		c.buf.WriteByte('\n')

	case "hr":
		c.ensureBlankLine()
		c.buf.WriteString("---\n\n")

	case "blockquote":
		inner := &mdConverter{}
		inner.renderChildren(node, st)
		body := strings.TrimSpace(inner.buf.String())
		if body == "" {
			return
		}
		c.ensureBlankLine()
		for _, line := range strings.Split(body, "\n") {
			if strings.TrimSpace(line) == "" {
				c.buf.WriteString(">\n")
			} else {
				c.buf.WriteString("> ")
				c.buf.WriteString(line)
				c.buf.WriteByte('\n')
			}
		}
		c.buf.WriteByte('\n')

	case "pre":
		lang, code := extractPreCode(node)
		code = strings.Trim(code, "\n")
		if code == "" {
			return
		}
		c.ensureBlankLine()
		fence := "```"
		if strings.Contains(code, "```") {
			fence = "~~~~"
		}
		c.buf.WriteString(fence)
		c.buf.WriteString(lang)
		c.buf.WriteByte('\n')
		c.buf.WriteString(code)
		c.buf.WriteByte('\n')
		c.buf.WriteString(fence)
		c.buf.WriteString("\n\n")

	case "code":
		if st.inPre {
			c.renderChildren(node, st)
			return
		}
		raw := strings.TrimSpace(collectPlainText(node))
		if raw == "" {
			return
		}
		if strings.Contains(raw, "`") {
			c.buf.WriteString("`` ")
			c.buf.WriteString(raw)
			c.buf.WriteString(" ``")
		} else {
			c.buf.WriteByte('`')
			c.buf.WriteString(raw)
			c.buf.WriteByte('`')
		}

	case "strong", "b":
		text := c.renderInlineText(node)
		if text != "" {
			c.buf.WriteString("**")
			c.buf.WriteString(text)
			c.buf.WriteString("**")
		}

	case "em", "i":
		text := c.renderInlineText(node)
		if text != "" {
			c.buf.WriteByte('*')
			c.buf.WriteString(text)
			c.buf.WriteByte('*')
		}

	case "del", "s", "strike":
		text := c.renderInlineText(node)
		if text != "" {
			c.buf.WriteString("~~")
			c.buf.WriteString(text)
			c.buf.WriteString("~~")
		}

	case "a":
		text := normalizeSearchText(c.renderInlineText(node))
		href := strings.TrimSpace(node.attrs["href"])
		lowerHref := strings.ToLower(href)
		if href == "" || strings.HasPrefix(lowerHref, "javascript:") || href == "#" {
			if text != "" {
				c.buf.WriteString(text)
			}
			return
		}
		if text == "" {
			text = href
		}
		c.buf.WriteByte('[')
		c.buf.WriteString(text)
		c.buf.WriteString("](")
		c.buf.WriteString(href)
		c.buf.WriteByte(')')

	case "img":
		src := strings.TrimSpace(node.attrs["src"])
		alt := normalizeSearchText(node.attrs["alt"])
		if strings.HasPrefix(strings.ToLower(src), "data:") {
			if alt != "" {
				c.buf.WriteString("![")
				c.buf.WriteString(alt)
				c.buf.WriteByte(']')
			}
			return
		}
		if src == "" {
			if alt != "" {
				c.buf.WriteString(alt)
			}
			return
		}
		c.buf.WriteString("![")
		c.buf.WriteString(alt)
		c.buf.WriteString("](")
		c.buf.WriteString(src)
		c.buf.WriteByte(')')

	case "ul", "ol":
		c.renderList(node, node.tag == "ol", st)

	case "table":
		c.renderTable(node)

	case "dl":
		c.ensureBlankLine()
		for _, ch := range node.children {
			if ch.kind != nodeElement {
				continue
			}
			switch ch.tag {
			case "dt":
				if t := normalizeSearchText(c.renderInlineText(ch)); t != "" {
					c.ensureNewline()
					c.buf.WriteString("**")
					c.buf.WriteString(t)
					c.buf.WriteString("**\n")
				}
			case "dd":
				if t := c.renderInlineText(ch); t != "" {
					c.ensureNewline()
					c.buf.WriteString(": ")
					c.buf.WriteString(t)
					c.buf.WriteString("\n\n")
				}
			}
		}

	case "div", "section", "article", "main", "header", "footer", "aside", "figure", "figcaption", "address":
		c.ensureNewline()
		c.renderChildren(node, st)
		c.ensureNewline()

	default:
		c.renderChildren(node, st)
	}
}

func (c *mdConverter) renderList(node *htmlNode, ordered bool, st renderState) {
	if st.listDepth == 0 {
		c.ensureBlankLine()
	} else {
		c.ensureNewline()
	}
	indent := strings.Repeat("  ", st.listDepth)
	idx := 1
	for _, ch := range node.children {
		if ch.kind != nodeElement || ch.tag != "li" {
			continue
		}
		prefix := "- "
		if ordered {
			prefix = fmt.Sprintf("%d. ", idx)
			idx++
		}
		// Render inline/block text separate from nested ul/ol lists.
		var inlineBuf mdConverter
		var nested []*htmlNode
		for _, itemChild := range ch.children {
			if itemChild.kind == nodeElement && (itemChild.tag == "ul" || itemChild.tag == "ol") {
				nested = append(nested, itemChild)
			} else {
				inlineBuf.renderNode(itemChild, renderState{listDepth: st.listDepth + 1})
			}
		}
		itemText := strings.TrimSpace(inlineBuf.buf.String())
		if itemText != "" || len(nested) > 0 {
			c.ensureNewline()
			c.buf.WriteString(indent)
			c.buf.WriteString(prefix)
			if itemText != "" {
				lines := strings.Split(itemText, "\n")
				c.buf.WriteString(lines[0])
				for _, extra := range lines[1:] {
					c.buf.WriteByte('\n')
					c.buf.WriteString(indent)
					c.buf.WriteString("  ")
					c.buf.WriteString(extra)
				}
			}
			c.buf.WriteByte('\n')
			for _, sub := range nested {
				c.renderList(sub, sub.tag == "ol", renderState{listDepth: st.listDepth + 1})
			}
		}
	}
	if st.listDepth == 0 {
		c.ensureBlankLine()
	}
}

func (c *mdConverter) renderTable(table *htmlNode) {
	var rows [][]string
	var collectRows func(n *htmlNode)
	collectRows = func(n *htmlNode) {
		for _, ch := range n.children {
			if ch.kind != nodeElement {
				continue
			}
			switch ch.tag {
			case "tr":
				var cells []string
				for _, cell := range ch.children {
					if cell.kind == nodeElement && (cell.tag == "th" || cell.tag == "td") {
						txt := normalizeSearchText(c.renderInlineText(cell))
						txt = strings.ReplaceAll(txt, "|", `\|`)
						cells = append(cells, txt)
					}
				}
				if len(cells) > 0 {
					rows = append(rows, cells)
				}
			case "thead", "tbody", "tfoot":
				collectRows(ch)
			}
		}
	}
	collectRows(table)
	if len(rows) == 0 {
		return
	}
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	padRow := func(r []string) string {
		padded := make([]string, cols)
		copy(padded, r)
		return "| " + strings.Join(padded, " | ") + " |"
	}

	c.ensureBlankLine()
	c.buf.WriteString(padRow(rows[0]))
	c.buf.WriteByte('\n')
	seps := make([]string, cols)
	for i := range seps {
		seps[i] = "---"
	}
	c.buf.WriteString("| ")
	c.buf.WriteString(strings.Join(seps, " | "))
	c.buf.WriteString(" |\n")
	for _, r := range rows[1:] {
		c.buf.WriteString(padRow(r))
		c.buf.WriteByte('\n')
	}
	c.buf.WriteByte('\n')
}

func extractPreCode(pre *htmlNode) (lang, code string) {
	// If <pre> wraps a single <code> child, grab its language class.
	for _, ch := range pre.children {
		if ch.kind == nodeElement && ch.tag == "code" {
			lang = extractCodeLang(ch.attrs["class"])
			return lang, collectPlainText(ch)
		}
	}
	return extractCodeLang(pre.attrs["class"]), collectPlainText(pre)
}

func extractCodeLang(classAttr string) string {
	for _, tok := range strings.Fields(classAttr) {
		lower := strings.ToLower(tok)
		if strings.HasPrefix(lower, "language-") {
			return strings.TrimPrefix(lower, "language-")
		}
		if strings.HasPrefix(lower, "lang-") {
			return strings.TrimPrefix(lower, "lang-")
		}
	}
	return ""
}

func collectPlainText(node *htmlNode) string {
	var b strings.Builder
	var walk func(n *htmlNode)
	walk = func(n *htmlNode) {
		if n.kind == nodeText {
			b.WriteString(html.UnescapeString(n.text))
			return
		}
		if n.tag == "br" {
			b.WriteByte('\n')
			return
		}
		for _, ch := range n.children {
			walk(ch)
		}
	}
	walk(node)
	return b.String()
}

func collapseInlineSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// dataURIImageRe matches inline data: URI images in Markdown, keeping alt text.
// upstream: packages/coding-agent/src/tools/fetch.ts:564-574
var dataURIImageRe = regexp.MustCompile(`(?i)!\[((?:\\.|[^\\\]])*)\]\(\s*(?:<data:[^>]*>|data:[^)\s]*)(?:\s+(?:"(?:\\.|[^\\"])*"|'(?:\\.|[^\\'])*'|\((?:\\.|[^\\)])*\)))?\s*\)`)

func stripDataURIImages(md string) string {
	return dataURIImageRe.ReplaceAllStringFunc(md, func(match string) string {
		sub := dataURIImageRe.FindStringSubmatch(match)
		if len(sub) > 1 && strings.TrimSpace(sub[1]) != "" {
			return "![" + sub[1] + "]"
		}
		return ""
	})
}
