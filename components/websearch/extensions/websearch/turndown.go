package websearch

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// A hand port of turndown 7.2.4 (lib/turndown.cjs.js, the build Node loads with @mixmark-io/domino),
// configured as extract.ts getTurndown configures it: headingStyle "atx" and codeBlockStyle
// "fenced", every other option at its default (hr "* * *", bulletListMarker "*", fence "```",
// emDelimiter "_", strongDelimiter "**", linkStyle "inlined", br "  ", preformattedCode false, no
// plugin, no keep and no remove rule). The rule set, the escaping, the whitespace collapsing and
// the joining are the library's own, in its order; the DOM is x/net/html where the library has
// domino, so JavaScript's string semantics (UTF-16, the whitespace class of `\s`, `trim`) are
// reproduced where they differ from Go's (jsstr.go, isJSSpace).
//
// The proof is port/record-html-extract.mjs: the original's output for the fixtures of
// testdata/html, which htmlmd_fidelity_test.go compares byte for byte.

const (
	tdHR     = "* * *"
	tdBullet = "*"
	tdFence  = "```"
	tdEm     = "_"
	tdStrong = "**"
	tdBR     = "  "
	tdRootID = "turndown-root"
)

var (
	tdBlockElements = tdSet("ADDRESS", "ARTICLE", "ASIDE", "AUDIO", "BLOCKQUOTE", "BODY", "CANVAS", "CENTER", "DD", "DIR", "DIV", "DL", "DT", "FIELDSET", "FIGCAPTION", "FIGURE", "FOOTER", "FORM", "FRAMESET", "H1", "H2", "H3", "H4", "H5", "H6", "HEADER", "HGROUP", "HR", "HTML", "ISINDEX", "LI", "MAIN", "MENU", "NAV", "NOFRAMES", "NOSCRIPT", "OL", "OUTPUT", "P", "PRE", "SECTION", "TABLE", "TBODY", "TD", "TFOOT", "TH", "THEAD", "TR", "UL")
	tdVoidElements  = tdSet("AREA", "BASE", "BR", "COL", "COMMAND", "EMBED", "HR", "IMG", "INPUT", "KEYGEN", "LINK", "META", "PARAM", "SOURCE", "TRACK", "WBR")
	tdMeaningful    = tdSet("A", "TABLE", "THEAD", "TBODY", "TFOOT", "TH", "TD", "IFRAME", "SCRIPT", "AUDIO", "VIDEO")
)

func tdSet(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// tdName is node.nodeName: the upper-cased tag of an HTML element, the qualified name of a
// foreign one, "#text" and so on.
func tdName(n *html.Node) string {
	switch n.Type {
	case html.ElementNode:
		if n.Namespace == "" || n.Namespace == "html" {
			return strings.ToUpper(n.Data)
		}
		return n.Data
	case html.TextNode:
		return "#text"
	case html.CommentNode:
		return "#comment"
	case html.DocumentNode:
		return "#document"
	}
	return "#other"
}

func tdAttr(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == name {
			return a.Val, true
		}
	}
	return "", false
}

// tdIs reports whether n is an element whose upper-cased name is in set.
func tdIs(n *html.Node, set map[string]bool) bool {
	return n.Type == html.ElementNode && set[tdName(n)]
}

// tdHas is has(node, tagNames): a descendant element with one of the names.
func tdHas(n *html.Node, set map[string]bool) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if set[tdName(c)] || tdHas(c, set) {
			return true
		}
	}
	return false
}

// jsSpace is a character of JavaScript's `\s` (and of String.prototype.trim).
func jsSpace(r rune) bool { return isJSSpace(r) }

func tdAsciiSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// tdTextContent is node.textContent: the data of every text node below n, in order.
func tdTextContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	tdAppendText(&b, n)
	return b.String()
}

func tdAppendText(b *strings.Builder, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			b.WriteString(c.Data)
		case html.ElementNode:
			tdAppendText(b, c)
		}
	}
}

// tdTextBlank is /^\s*$/.test(node.textContent) without building the string.
func tdTextBlank(n *html.Node) bool {
	if n.Type == html.TextNode {
		return strings.TrimFunc(n.Data, jsSpace) == ""
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			if strings.TrimFunc(c.Data, jsSpace) != "" {
				return false
			}
		case html.ElementNode:
			if !tdTextBlank(c) {
				return false
			}
		}
	}
	return true
}

func tdFirstElement(n *html.Node) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			return c
		}
	}
	return nil
}

func tdLastElement(n *html.Node) *html.Node {
	for c := n.LastChild; c != nil; c = c.PrevSibling {
		if c.Type == html.ElementNode {
			return c
		}
	}
	return nil
}

// tdIsBlank is isBlank: no text, nothing void and nothing that matters when empty.
func tdIsBlank(n *html.Node) bool {
	return !tdIs(n, tdVoidElements) && !tdIs(n, tdMeaningful) && tdTextBlank(n) && !tdHas(n, tdVoidElements) && !tdHas(n, tdMeaningful)
}

// tdEdge is edgeWhitespace: the leading and trailing whitespace of a string, split into the
// ASCII part next to the text and the rest.
type tdEdges struct{ leading, leadingASCII, leadingNonASCII, trailing, trailingASCII, trailingNonASCII string }

func tdEdgeWhitespace(s string) tdEdges {
	end := len(s)
	start := 0
	for start < end {
		r, size := utf8.DecodeRuneInString(s[start:])
		if !jsSpace(r) {
			break
		}
		start += size
	}
	if start == end {
		// whole string for whitespace-only strings
		asciiEnd := 0
		for asciiEnd < len(s) && tdAsciiSpace(s[asciiEnd]) {
			asciiEnd++
		}
		return tdEdges{leading: s, leadingASCII: s[:asciiEnd], leadingNonASCII: s[asciiEnd:]}
	}
	asciiEnd := 0
	for asciiEnd < start && tdAsciiSpace(s[asciiEnd]) {
		asciiEnd++
	}
	tail := end
	for tail > start {
		r, size := utf8.DecodeLastRuneInString(s[:tail])
		if !jsSpace(r) {
			break
		}
		tail -= size
	}
	asciiTail := end
	for asciiTail > tail && tdAsciiSpace(s[asciiTail-1]) {
		asciiTail--
	}
	return tdEdges{
		leading: s[:start], leadingASCII: s[:asciiEnd], leadingNonASCII: s[asciiEnd:start],
		trailing: s[tail:], trailingASCII: s[asciiTail:], trailingNonASCII: s[tail:asciiTail],
	}
}

// tdFlanked is isFlankedByWhitespace: is the nearest sibling on that side a space?
func tdFlanked(left bool, n *html.Node) bool {
	sibling := n.NextSibling
	if left {
		sibling = n.PrevSibling
	}
	if sibling == nil {
		return false
	}
	test := func(s string) bool {
		if left {
			return strings.HasSuffix(s, " ")
		}
		return strings.HasPrefix(s, " ")
	}
	switch {
	case sibling.Type == html.TextNode:
		return test(sibling.Data)
	case sibling.Type == html.ElementNode && !tdIs(sibling, tdBlockElements):
		return test(tdTextContent(sibling))
	}
	return false
}

// tdFlankingWhitespace is flankingWhitespace for an element that is not a block.
func tdFlankingWhitespace(n *html.Node) (leading, trailing string) {
	if tdIs(n, tdBlockElements) {
		return "", ""
	}
	e := tdEdgeWhitespace(tdTextContent(n))
	leading, trailing = e.leading, e.trailing
	// abandon leading ASCII WS if left-flanked by ASCII WS
	if e.leadingASCII != "" && tdFlanked(true, n) {
		leading = e.leadingNonASCII
	}
	// abandon trailing ASCII WS if right-flanked by ASCII WS
	if e.trailingASCII != "" && tdFlanked(false, n) {
		trailing = e.trailingNonASCII
	}
	return leading, trailing
}

// ---- collapse-whitespace ------------------------------------------------------------------

func tdIsPre(n *html.Node) bool { return n.Type == html.ElementNode && tdName(n) == "PRE" }

func tdNext(prev, cur *html.Node) *html.Node {
	if (prev != nil && prev.Parent == cur) || tdIsPre(cur) {
		if cur.NextSibling != nil {
			return cur.NextSibling
		}
		return cur.Parent
	}
	if cur.FirstChild != nil {
		return cur.FirstChild
	}
	if cur.NextSibling != nil {
		return cur.NextSibling
	}
	return cur.Parent
}

// tdRemove is remove(node): detach n and return the node that follows in the walk.
func tdRemove(n *html.Node) *html.Node {
	next := n.NextSibling
	if next == nil {
		next = n.Parent
	}
	n.Parent.RemoveChild(n)
	return next
}

// tdCollapseWhitespace is collapseWhitespace (adapted from collapse-whitespace by Luc Thevenard).
func tdCollapseWhitespace(element *html.Node) {
	if element.FirstChild == nil || tdIsPre(element) {
		return
	}
	var prevText *html.Node
	keepLeadingWs := false
	var prev *html.Node
	node := tdNext(prev, element)
	for node != element {
		switch node.Type {
		case html.TextNode:
			text := tdCollapseRun(node.Data)
			if (prevText == nil || strings.HasSuffix(prevText.Data, " ")) && !keepLeadingWs && strings.HasPrefix(text, " ") {
				text = text[1:]
			}
			// text might be empty at this point.
			if text == "" {
				node = tdRemove(node)
				continue
			}
			node.Data = text
			prevText = node
		case html.ElementNode:
			switch {
			case tdIs(node, tdBlockElements) || tdName(node) == "BR":
				if prevText != nil {
					prevText.Data = strings.TrimSuffix(prevText.Data, " ")
				}
				prevText = nil
				keepLeadingWs = false
			case tdIs(node, tdVoidElements) || tdIsPre(node):
				// Avoid trimming space around non-block, non-BR void elements and inline PRE.
				prevText = nil
				keepLeadingWs = true
			case prevText != nil:
				// Drop protection if set previously.
				keepLeadingWs = false
			}
		default:
			node = tdRemove(node)
			continue
		}
		nextNode := tdNext(prev, node)
		prev = node
		node = nextNode
	}
	if prevText != nil {
		prevText.Data = strings.TrimSuffix(prevText.Data, " ")
		if prevText.Data == "" {
			tdRemove(prevText)
		}
	}
}

// tdCollapseRun is text.replace(/[ \r\n\t]+/g, ' ').
func tdCollapseRun(s string) string {
	clean := true
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' || s[i] == '\n' || s[i] == '\t' || (s[i] == ' ' && i+1 < len(s) && tdAsciiSpace(s[i+1])) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	inRun := false
	for i := 0; i < len(s); i++ {
		if tdAsciiSpace(s[i]) {
			if !inRun {
				b.WriteByte(' ')
				inRun = true
			}
			continue
		}
		inRun = false
		b.WriteByte(s[i])
	}
	return b.String()
}

// ---- escaping ------------------------------------------------------------------------------

// tdEscape is escapeMarkdown: thirteen replacements, in the library's order. The anchored ones
// apply to the start of the string (the library's regexps have no `m` flag), the others anywhere.
func tdEscape(s string) string {
	if !strings.ContainsAny(s, "\\*-+=#`~[]>_0123456789") {
		return s
	}
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `*`, `\*`)
	if strings.HasPrefix(s, "-") {
		s = `\` + s
	}
	if strings.HasPrefix(s, "+ ") {
		s = `\` + s
	}
	if strings.HasPrefix(s, "=") {
		s = `\` + s
	}
	if n := countPrefix(s, '#'); n >= 1 && n <= 6 && len(s) > n && s[n] == ' ' {
		s = `\` + s
	}
	s = strings.ReplaceAll(s, "`", "\\`")
	if strings.HasPrefix(s, "~~~") {
		s = `\` + s
	}
	s = strings.ReplaceAll(s, `[`, `\[`)
	s = strings.ReplaceAll(s, `]`, `\]`)
	if strings.HasPrefix(s, ">") {
		s = `\` + s
	}
	s = strings.ReplaceAll(s, `_`, `\_`)
	if n := countDigits(s); n > 0 && strings.HasPrefix(s[n:], ". ") {
		s = s[:n] + `\` + s[n:]
	}
	return s
}

func countPrefix(s string, c byte) int {
	n := 0
	for n < len(s) && s[n] == c {
		n++
	}
	return n
}

func countDigits(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

// tdCleanAttribute is cleanAttribute: each newline and the whitespace after it become one newline.
func tdCleanAttribute(s string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	var b strings.Builder
	skipping := false
	for _, r := range s {
		switch {
		case skipping && jsSpace(r):
			// (\n+\s*)+ swallows every whitespace character after a newline, newlines included.
		case r == '\n':
			b.WriteByte('\n')
			skipping = true
		default:
			skipping = false
			b.WriteRune(r)
		}
	}
	return b.String()
}

func tdEscapeLinkDestination(d string) string {
	escaped := d
	if strings.ContainsAny(d, "<>()") {
		var b strings.Builder
		for i := 0; i < len(d); i++ {
			switch d[i] {
			case '<', '>', '(', ')':
				b.WriteByte('\\')
			}
			b.WriteByte(d[i])
		}
		escaped = b.String()
	}
	if strings.Contains(escaped, " ") {
		return "<" + escaped + ">"
	}
	return escaped
}

func tdEscapeLinkTitle(t string) string { return strings.ReplaceAll(t, `"`, `\"`) }

// ---- the converter -------------------------------------------------------------------------

func tdTrimLeadingNewlines(s string) string { return strings.TrimLeft(s, "\n") }

func tdTrimNewlines(s string) string { return strings.Trim(s, "\n") }

// tdOut accumulates sibling outputs with join's rule: at most the larger of the newline runs
// between two outputs, and never more than two. It trims the tail of the buffer and appends, so
// n siblings cost the size of the output, not n times it.
type tdOut struct{ b []byte }

func (o *tdOut) join(replacement string) {
	end := len(o.b)
	for end > 0 && o.b[end-1] == '\n' {
		end--
	}
	trailing := len(o.b) - end
	rest := tdTrimLeadingNewlines(replacement)
	leading := len(replacement) - len(rest)
	nls := trailing
	if leading > nls {
		nls = leading
	}
	if nls > 2 {
		nls = 2
	}
	o.b = append(o.b[:end], "\n\n"[:nls]...)
	o.b = append(o.b, rest...)
}

// tdProcess is process: the Markdown of a node's children.
func tdProcess(parent *html.Node, parentIsCode bool) string {
	var out tdOut
	for n := parent.FirstChild; n != nil; n = n.NextSibling {
		replacement := ""
		switch n.Type {
		case html.TextNode:
			if parentIsCode {
				replacement = n.Data
			} else {
				replacement = tdEscape(n.Data)
			}
		case html.ElementNode:
			replacement = tdReplacementForNode(n, parentIsCode)
		}
		out.join(replacement)
	}
	return string(out.b)
}

// tdReplacementForNode is replacementForNode: the node's rule applied to its converted children,
// inside the whitespace that flanks it.
func tdReplacementForNode(n *html.Node, parentIsCode bool) string {
	isCode := tdName(n) == "CODE" || parentIsCode
	isBlock := tdIs(n, tdBlockElements)
	// The flanking whitespace belongs to the node whatever its rule is, a blank one included.
	leading, trailing := tdFlankingWhitespace(n)
	if tdIsBlank(n) {
		// blankRule: node.isBlock ? '\n\n' : ''
		if isBlock {
			return leading + "\n\n" + trailing
		}
		return leading + trailing
	}
	content := tdProcess(n, isCode)
	if leading != "" || trailing != "" {
		content = strings.TrimFunc(content, jsSpace)
	}
	return leading + tdApplyRule(n, content, isBlock) + trailing
}

// tdApplyRule is rules.forNode(node).replacement(content, node, options): the first rule of the
// library's list whose filter accepts the node, else the default rule.
func tdApplyRule(n *html.Node, content string, isBlock bool) string {
	name := strings.ToLower(tdName(n))
	switch name {
	case "p": // paragraph
		return "\n\n" + content + "\n\n"
	case "br": // lineBreak
		return tdBR + "\n"
	case "h1", "h2", "h3", "h4", "h5", "h6": // heading (headingStyle "atx")
		return "\n\n" + strings.Repeat("#", int(name[1]-'0')) + " " + content + "\n\n"
	case "blockquote":
		return "\n\n" + tdLinePrefix(tdTrimNewlines(content), "> ") + "\n\n"
	case "ul", "ol": // list
		parent := n.Parent
		if parent != nil && tdName(parent) == "LI" && tdLastElement(parent) == n {
			return "\n" + content
		}
		return "\n\n" + content + "\n\n"
	case "li":
		return tdListItem(n, content)
	case "pre": // fencedCodeBlock
		if first := n.FirstChild; first != nil && tdName(first) == "CODE" {
			return tdFencedCode(first)
		}
	case "hr":
		return "\n\n" + tdHR + "\n\n"
	case "a": // inlineLink
		if href, _ := tdAttr(n, "href"); href != "" {
			title := tdEscapeLinkTitle(tdCleanAttribute(tdAttrOr(n, "title")))
			titlePart := ""
			if title != "" {
				titlePart = ` "` + title + `"`
			}
			return "[" + content + "](" + tdEscapeLinkDestination(href) + titlePart + ")"
		}
	case "em", "i":
		if strings.TrimFunc(content, jsSpace) == "" {
			return ""
		}
		return tdEm + content + tdEm
	case "strong", "b":
		if strings.TrimFunc(content, jsSpace) == "" {
			return ""
		}
		return tdStrong + content + tdStrong
	case "img":
		alt := tdEscape(tdCleanAttribute(tdAttrOr(n, "alt")))
		src := tdEscapeLinkDestination(tdAttrOr(n, "src"))
		title := tdCleanAttribute(tdAttrOr(n, "title"))
		titlePart := ""
		if title != "" {
			titlePart = ` "` + tdEscapeLinkTitle(title) + `"`
		}
		if src == "" {
			return ""
		}
		return "![" + alt + "](" + src + titlePart + ")"
	}
	// rules.code filters on the node name case-sensitively through nodeName === 'CODE', which
	// the lower-cased switch above cannot see for a foreign element: check it here.
	if tdName(n) == "CODE" {
		hasSiblings := n.PrevSibling != nil || n.NextSibling != nil
		isCodeBlock := n.Parent != nil && tdName(n.Parent) == "PRE" && !hasSiblings
		if !isCodeBlock {
			return tdInlineCode(content)
		}
	}
	// defaultRule
	if isBlock {
		return "\n\n" + content + "\n\n"
	}
	return content
}

func tdAttrOr(n *html.Node, name string) string {
	v, _ := tdAttr(n, name)
	return v
}

// tdLineBreak reports a JavaScript line terminator, which `^` of a multiline regexp follows.
func tdLineBreak(r rune) bool { return r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029' }

// tdLinePrefix is s.replace(/^/gm, prefix): the prefix at the start and after every line terminator.
func tdLinePrefix(s, prefix string) string {
	var b strings.Builder
	b.Grow(len(s) + len(prefix)*4)
	b.WriteString(prefix)
	for _, r := range s {
		b.WriteRune(r)
		if tdLineBreak(r) {
			b.WriteString(prefix)
		}
	}
	return b.String()
}

// tdListItem is rules.listItem.
func tdListItem(n *html.Node, content string) string {
	prefix := tdBullet + "   "
	parent := n.Parent
	if parent != nil && tdName(parent) == "OL" {
		start, _ := tdAttr(parent, "start")
		index := 0
		for c := parent.FirstChild; c != nil && c != n; c = c.NextSibling {
			if c.Type == html.ElementNode {
				index++
			}
		}
		if start != "" {
			n, ok := jsToNumber(start)
			if !ok {
				n = math.NaN()
			}
			prefix = jsNumberString(n+float64(index)) + ".  "
		} else {
			prefix = strconv.Itoa(index+1) + ".  "
		}
	}
	isParagraph := strings.HasSuffix(content, "\n")
	content = tdTrimNewlines(content)
	if isParagraph {
		content += "\n"
	}
	content = strings.ReplaceAll(content, "\n", "\n"+strings.Repeat(" ", len(prefix)))
	suffix := ""
	if n.NextSibling != nil {
		suffix = "\n"
	}
	return prefix + content + suffix
}

// tdFencedCode is rules.fencedCodeBlock for a PRE whose first child is a CODE.
func tdFencedCode(code *html.Node) string {
	className := tdAttrOr(code, "class")
	language := tdLanguage(className)
	text := tdTextContent(code)
	fenceSize := 3
	// fenceInCodeRegex = /^`{3,}/gm
	atLineStart := true
	for i := 0; i < len(text); {
		if atLineStart && text[i] == '`' {
			j := i
			for j < len(text) && text[j] == '`' {
				j++
			}
			if j-i >= 3 {
				if j-i >= fenceSize {
					fenceSize = j - i + 1
				}
				i = j
				atLineStart = false
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		atLineStart = tdLineBreak(r)
		i += size
	}
	fence := strings.Repeat("`", fenceSize)
	return "\n\n" + fence + language + "\n" + strings.TrimSuffix(text, "\n") + "\n" + fence + "\n\n"
}

// tdLanguage is (className.match(/language-(\S+)/) || [null, ”])[1].
func tdLanguage(class string) string {
	const marker = "language-"
	for from := 0; ; {
		i := strings.Index(class[from:], marker)
		if i < 0 {
			return ""
		}
		start := from + i + len(marker)
		end := start
		for end < len(class) {
			r, size := utf8.DecodeRuneInString(class[end:])
			if jsSpace(r) {
				break
			}
			end += size
		}
		if end > start {
			return class[start:end]
		}
		from += i + 1
	}
}

// tdInlineCode is rules.code's replacement.
func tdInlineCode(content string) string {
	if content == "" {
		return ""
	}
	content = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(content)
	extraSpace := ""
	if strings.HasPrefix(content, "`") || strings.HasSuffix(content, "`") || tdPaddedCode(content) {
		extraSpace = " "
	}
	delimiter := "`"
	for tdHasBacktickRun(content, len(delimiter)) {
		delimiter += "`"
	}
	return delimiter + extraSpace + content + extraSpace + delimiter
}

// tdPaddedCode is /^ .*?[^ ].* $/.test(content), where `.` is any character but a line terminator.
func tdPaddedCode(s string) bool {
	if !strings.HasPrefix(s, " ") || !strings.HasSuffix(s, " ") || len(s) < 3 {
		return false
	}
	inner := []rune(s[1 : len(s)-1])
	terminators := make([]int, 0, 2)
	for i, r := range inner {
		if tdLineBreak(r) {
			terminators = append(terminators, i)
		}
	}
	for i, r := range inner {
		if r == ' ' {
			continue
		}
		// `.*?` before and `.*` after the non-space must not cross a terminator.
		ok := true
		for _, t := range terminators {
			if t != i {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// tdHasBacktickRun is content.match(/`+/gm).indexOf(delimiter) !== -1: a run of exactly n backticks.
func tdHasBacktickRun(s string, n int) bool {
	for i := 0; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == '`' {
			j++
		}
		if j-i == n {
			return true
		}
		i = j
	}
	return false
}

// ---- entry ---------------------------------------------------------------------------------

// tdRootNode is RootNode for a string input: the HTML wrapped in a custom element is parsed as a
// document and the wrapper is found by its id; its whitespace is collapsed.
func tdRootNode(input string) *html.Node {
	doc, err := html.Parse(strings.NewReader(`<x-turndown id="` + tdRootID + `">` + input + `</x-turndown>`))
	if err != nil {
		return nil
	}
	var root *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if root != nil {
			return
		}
		if n.Type == html.ElementNode {
			if id, _ := tdAttr(n, "id"); id == tdRootID {
				root = n
				return
			}
		}
		for c := n.FirstChild; c != nil && root == nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if root != nil {
		tdCollapseWhitespace(root)
	}
	return root
}

// turndown is TurndownService.turndown for a string: the Markdown of an HTML fragment.
func turndown(input string) string {
	if input == "" {
		return ""
	}
	root := tdRootNode(input)
	if root == nil {
		return ""
	}
	output := tdProcess(root, false)
	// postProcess: output.replace(/^[\t\r\n]+/, '').replace(/[\t\r\n\s]+$/, '')
	output = strings.TrimLeft(output, "\t\r\n")
	return strings.TrimRightFunc(output, jsSpace)
}
