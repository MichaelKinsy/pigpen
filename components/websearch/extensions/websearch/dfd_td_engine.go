package websearch

import (
	"strings"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

// Defuddle's Markdown stage: dist/markdown.js on top of turndown 7.2.4 with the library's own rule set and the
// options createMarkdownContent sets (atx headings, "---", "-" bullets, fenced code, "*" emphasis, preformattedCode).
// The engine is the library's (rules in priority order, keep and remove lists, whitespace collapsing, flanking
// whitespace, join); the DOM is x/net/html where the library has domino, as in turndown.go, and domino's
// serialization (outerHTML, innerHTML) is reproduced where the rules use it.

type dfdTdRule struct {
	filter      func(n *html.Node) bool
	replacement func(content string, n *html.Node) string
}

type dfdTurndown struct {
	rules  []dfdTdRule
	keep   []dfdTdRule
	remove []dfdTdRule
}

func tdFilterNames(names ...string) func(*html.Node) bool {
	return func(n *html.Node) bool {
		name := strings.ToLower(tdName(n))
		for _, x := range names {
			if x == name {
				return true
			}
		}
		return false
	}
}

// ---- DOM helpers -----------------------------------------------------------------------------------------------

var tdSelCache = map[string]cascadia.Selector{}

func tdSel(s string) cascadia.Selector {
	if c, ok := tdSelCache[s]; ok {
		return c
	}
	c := cascadia.MustCompile(s)
	tdSelCache[s] = c
	return c
}

// tdQSA is querySelectorAll on a domino element: the descendants that match, never the element itself.
func tdQSA(root *html.Node, sel string) []*html.Node {
	s := tdSel(sel)
	var out []*html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode {
				if s.Match(c) {
					out = append(out, c)
				}
				walk(c)
			}
		}
	}
	walk(root)
	return out
}

func tdQS(root *html.Node, sel string) *html.Node {
	if r := tdQSA(root, sel); len(r) > 0 {
		return r[0]
	}
	return nil
}

func tdClosest(n *html.Node, sel string) *html.Node {
	s := tdSel(sel)
	for c := n; c != nil && c.Type == html.ElementNode; c = c.Parent {
		if s.Match(c) {
			return c
		}
	}
	return nil
}

func tdHasClass(n *html.Node, class string) bool {
	v, _ := tdAttr(n, "class")
	for _, c := range strings.FieldsFunc(v, jsSpace) {
		if c == class {
			return true
		}
	}
	return false
}

func tdElemChildren(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			out = append(out, c)
		}
	}
	return out
}

func tdCloneDeep(n *html.Node) *html.Node {
	c := &html.Node{Type: n.Type, DataAtom: n.DataAtom, Data: n.Data, Namespace: n.Namespace}
	c.Attr = append([]html.Attribute(nil), n.Attr...)
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.AppendChild(tdCloneDeep(ch))
	}
	return c
}

// tdRemoveNode is node.remove().
func tdRemoveNode(n *html.Node) {
	if n.Parent != nil {
		n.Parent.RemoveChild(n)
	}
}

var (
	tdRawContent   = map[string]bool{"STYLE": true, "SCRIPT": true, "XMP": true, "IFRAME": true, "NOEMBED": true, "NOFRAMES": true, "PLAINTEXT": true}
	tdEmptyElement = map[string]bool{"area": true, "base": true, "basefont": true, "bgsound": true, "br": true, "col": true, "embed": true, "frame": true, "hr": true, "img": true, "input": true, "keygen": true, "link": true, "meta": true, "param": true, "source": true, "track": true, "wbr": true}
)

func tdEscapeText(s string) string {
	if !strings.ContainsAny(s, "&<>\u00a0") {
		return s
	}
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\u00a0", "&nbsp;").Replace(s)
}

func tdEscapeAttr(s string) string {
	if !strings.ContainsAny(s, "&<>\"\u00a0") {
		return s
	}
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\u00a0", "&nbsp;").Replace(s)
}

// tdOuterHTML is domino's outerHTML (NodeUtils.serializeOne).
func tdOuterHTML(n *html.Node) string {
	var b strings.Builder
	tdSerializeOne(&b, n, nil)
	return b.String()
}

// tdInnerHTML is domino's innerHTML.
func tdInnerHTML(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		tdSerializeOne(&b, c, n)
	}
	return b.String()
}

var reTagEnd = mustRE(`(?i)</`)

func tdSerializeOne(b *strings.Builder, n *html.Node, parent *html.Node) {
	switch n.Type {
	case html.ElementNode:
		tag := n.Data
		isHTML := n.Namespace == "" || n.Namespace == "html"
		b.WriteString("<" + tag)
		for _, a := range n.Attr {
			name := a.Key
			if a.Namespace != "" {
				name = a.Namespace + ":" + a.Key
			}
			b.WriteString(" " + name + `="` + tdEscapeAttr(a.Val) + `"`)
		}
		b.WriteString(">")
		if !(isHTML && tdEmptyElement[tag]) {
			var inner strings.Builder
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				tdSerializeOne(&inner, c, n)
			}
			ss := inner.String()
			if tdRawContent[strings.ToUpper(tag)] {
				ss = tdEscapeMatchingClosingTag(ss, tag)
			}
			b.WriteString(ss)
			b.WriteString("</" + tag + ">")
		}
	case html.TextNode:
		parentTag := ""
		if parent != nil && parent.Type == html.ElementNode && (parent.Namespace == "" || parent.Namespace == "html") {
			parentTag = strings.ToUpper(parent.Data)
		}
		if tdRawContent[parentTag] {
			b.WriteString(n.Data)
		} else {
			b.WriteString(tdEscapeText(n.Data))
		}
	case html.CommentNode:
		b.WriteString("<!--" + reCommentEnd.ReplaceAllString(n.Data, "$1&gt;") + "-->")
	}
}

func tdEscapeMatchingClosingTag(raw, tag string) string {
	closing := "</" + strings.ToLower(tag)
	lower := strings.ToLower(raw)
	if !strings.Contains(lower, closing) {
		return raw
	}
	var b strings.Builder
	for i := 0; i < len(raw); {
		if strings.HasPrefix(lower[i:], closing) {
			b.WriteString("&lt;")
			i++
			continue
		}
		b.WriteByte(raw[i])
		i++
	}
	return b.String()
}

// ---- whitespace ------------------------------------------------------------------------------------------------

func tdIsPreOrCode(n *html.Node) bool {
	return n.Type == html.ElementNode && (tdName(n) == "PRE" || tdName(n) == "CODE")
}

// dfdCollapseWhitespace is collapseWhitespace with isPre = isPreOrCode (preformattedCode).
func dfdCollapseWhitespace(element *html.Node) {
	if element.FirstChild == nil || tdIsPreOrCode(element) {
		return
	}
	next := func(prev, cur *html.Node) *html.Node {
		if (prev != nil && prev.Parent == cur) || tdIsPreOrCode(cur) {
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
	var prevText *html.Node
	keepLeadingWs := false
	var prev *html.Node
	node := next(prev, element)
	for node != element {
		switch node.Type {
		case html.TextNode:
			text := tdCollapseRun(node.Data)
			if (prevText == nil || strings.HasSuffix(prevText.Data, " ")) && !keepLeadingWs && strings.HasPrefix(text, " ") {
				text = text[1:]
			}
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
			case tdIs(node, tdVoidElements) || tdIsPreOrCode(node):
				prevText = nil
				keepLeadingWs = true
			case prevText != nil:
				keepLeadingWs = false
			}
		default:
			node = tdRemove(node)
			continue
		}
		nn := next(prev, node)
		prev = node
		node = nn
	}
	if prevText != nil {
		prevText.Data = strings.TrimSuffix(prevText.Data, " ")
		if prevText.Data == "" {
			tdRemove(prevText)
		}
	}
}

func (svc *dfdTurndown) flanking(n *html.Node, isBlock, isCode bool) (leading, trailing string) {
	if isBlock || isCode {
		return "", ""
	}
	e := tdEdgeWhitespace(tdTextContent(n))
	leading, trailing = e.leading, e.trailing
	if e.leadingASCII != "" && svc.flankedBy(true, n) {
		leading = e.leadingNonASCII
	}
	if e.trailingASCII != "" && svc.flankedBy(false, n) {
		trailing = e.trailingNonASCII
	}
	return leading, trailing
}

func (svc *dfdTurndown) flankedBy(left bool, n *html.Node) bool {
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
	case sibling.Type == html.ElementNode && tdName(sibling) == "CODE":
		return false
	case sibling.Type == html.ElementNode && !tdIs(sibling, tdBlockElements):
		return test(tdTextContent(sibling))
	}
	return false
}

// ---- the engine ------------------------------------------------------------------------------------------------

func (svc *dfdTurndown) forNode(n *html.Node, isBlank bool) dfdTdRule {
	if isBlank {
		return dfdTdRule{replacement: func(_ string, n *html.Node) string {
			if tdIs(n, tdBlockElements) {
				return "\n\n"
			}
			return ""
		}}
	}
	for _, list := range [][]dfdTdRule{svc.rules, svc.keep, svc.remove} {
		for _, r := range list {
			if r.filter(n) {
				return r
			}
		}
	}
	return dfdTdRule{replacement: func(content string, n *html.Node) string {
		if tdIs(n, tdBlockElements) {
			return "\n\n" + content + "\n\n"
		}
		return content
	}}
}

var reTagLike = mustRE(`<`)

// dfdEscape is Defuddle's escape: turndown's, then a backslash before a "<" that opens a tag-like sequence.
func dfdEscape(s string) string {
	s = tdEscape(s)
	if !strings.Contains(s, "<") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '<' && dfdTagLikeAt(s, i+1) {
			b.WriteString(`\<`)
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// dfdTagLikeAt is the lookahead (?=\/?[A-Za-z][A-Za-z0-9-]*(?:\s|\/?>)) at s[i:].
func dfdTagLikeAt(s string, i int) bool {
	if i < len(s) && s[i] == '/' {
		i++
	}
	if i >= len(s) || !((s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z')) {
		return false
	}
	i++
	for i < len(s) && (ldIsAlnum(s[i]) || s[i] == '-') {
		i++
	}
	if i >= len(s) {
		return false
	}
	if s[i] == '>' {
		return true
	}
	if s[i] == '/' && i+1 < len(s) && s[i+1] == '>' {
		return true
	}
	r, _ := decodeRune(s[i:])
	return jsSpace(r)
}

func (svc *dfdTurndown) process(parent *html.Node, parentIsCode bool) string {
	var out tdOut
	for n := parent.FirstChild; n != nil; n = n.NextSibling {
		replacement := ""
		switch n.Type {
		case html.TextNode:
			if parentIsCode {
				replacement = n.Data
			} else {
				replacement = dfdEscape(n.Data)
			}
		case html.ElementNode:
			replacement = svc.replacementForNode(n, parentIsCode)
		}
		out.join(replacement)
	}
	return string(out.b)
}

func (svc *dfdTurndown) replacementForNode(n *html.Node, parentIsCode bool) string {
	isBlock := tdIs(n, tdBlockElements)
	isCode := tdName(n) == "CODE" || parentIsCode
	isBlank := tdIsBlank(n)
	rule := svc.forNode(n, isBlank)
	content := svc.process(n, isCode)
	leading, trailing := svc.flanking(n, isBlock, isCode)
	if leading != "" || trailing != "" {
		content = strings.TrimFunc(content, jsSpace)
	}
	return leading + rule.replacement(content, n) + trailing
}

// turndown is TurndownService.turndown for a string.
func (svc *dfdTurndown) turndown(input string) string {
	if input == "" {
		return ""
	}
	doc := dfdParseHTMLDocument(`<x-turndown id="turndown-root">` + input + `</x-turndown>`)
	if doc == nil {
		return ""
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
	if root == nil {
		return ""
	}
	dfdCollapseWhitespace(root)
	output := svc.process(root, false)
	output = strings.TrimLeft(output, "\t\r\n")
	return strings.TrimRightFunc(output, jsSpace)
}

var reCommentEnd = mustRE(`(--!?)>`)
