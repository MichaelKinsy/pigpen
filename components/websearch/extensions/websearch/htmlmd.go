package websearch

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// A small readability pass and an HTML-to-Markdown converter, standing in for the original's
// @mozilla/readability + turndown (and defuddle, which is not ported). The pipeline is the same:
// find the article, drop chrome, convert to Markdown with turndown's default conventions (atx
// headings, fenced code, `*   ` bullets, `_` emphasis). It is not byte-identical to Mozilla
// Readability's scoring; that is a named gap (docs/PORT.md).

var (
	unlikelyRE    = regexp.MustCompile(`(?i)-ad-|ai2html|banner|breadcrumbs|combx|comment|community|cover-wrap|disqus|extra|footer|gdpr|header|legends|menu|related|remark|replies|rss|shoutbox|sidebar|skyscraper|social|sponsor|supplemental|ad-break|agegate|pagination|pager|popup|yom-remote`)
	okMaybeRE     = regexp.MustCompile(`(?i)and|article|body|column|content|main|mathjax|shadow`)
	hiddenStyleRE = regexp.MustCompile(`(?i)display:\s*none|visibility:\s*hidden`)
	droppedTags   = map[string]bool{"script": true, "style": true, "noscript": true, "template": true, "iframe": true, "object": true, "embed": true, "link": true, "meta": true, "svg": true, "canvas": true, "nav": true, "aside": true, "footer": true, "form": true, "button": true, "select": true, "input": true, "textarea": true, "dialog": true, "head": true, "title": true}
	blockTags     = map[string]bool{"address": true, "article": true, "aside": true, "audio": true, "blockquote": true, "body": true, "canvas": true, "center": true, "dd": true, "dir": true, "div": true, "dl": true, "dt": true, "fieldset": true, "figcaption": true, "figure": true, "footer": true, "form": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "header": true, "hgroup": true, "hr": true, "html": true, "li": true, "main": true, "menu": true, "nav": true, "noscript": true, "ol": true, "output": true, "p": true, "pre": true, "section": true, "table": true, "tbody": true, "td": true, "tfoot": true, "th": true, "thead": true, "tr": true, "ul": true}
	titleSepRE    = regexp.MustCompile(`[|\-\\/>»] `)
	wsRE          = regexp.MustCompile(`\s+`)
)

// documentTitle is document.title: the first <title> element's text, trimmed.
func documentTitle(doc *html.Node) string {
	if t := firstElement(doc, func(n *html.Node) bool { return n.Data == "title" }); t != nil {
		return jsTrim(wsRE.ReplaceAllString(textContent(t), " "))
	}
	return ""
}

func wordCount(s string) int { return len(strings.Fields(s)) }

// articleTitle is the readability title heuristic: strip a trailing site name, fall back to the
// only <h1> when the result is implausibly short or long.
func articleTitle(doc *html.Node) string {
	orig := documentTitle(doc)
	cur := orig
	if titleSepRE.MatchString(cur) {
		idx := titleSepRE.FindAllStringIndex(cur, -1)
		last := idx[len(idx)-1]
		cur = jsTrim(cur[:last[0]])
		if wordCount(cur) < 3 {
			first := titleSepRE.FindStringIndex(orig)
			cur = jsTrim(orig[first[1]:])
		}
	}
	if l := jsLen(cur); l > 150 || l < 15 {
		var h1s []*html.Node
		collect(doc, func(n *html.Node) bool { return n.Data == "h1" }, &h1s)
		if len(h1s) == 1 {
			if h := jsTrim(wsRE.ReplaceAllString(textContent(h1s[0]), " ")); h != "" {
				cur = h
			}
		}
	}
	if cur == "" {
		cur = orig
	}
	return cur
}

func collect(n *html.Node, match func(*html.Node) bool, out *[]*html.Node) {
	if n.Type == html.ElementNode && match(n) {
		*out = append(*out, n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collect(c, match, out)
	}
}

func isElement(n *html.Node, names ...string) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	for _, x := range names {
		if n.Data == x {
			return true
		}
	}
	return false
}

func nodeText(n *html.Node) string { return jsTrim(wsRE.ReplaceAllString(textContent(n), " ")) }

func linkDensity(n *html.Node) float64 {
	total := jsLen(nodeText(n))
	if total == 0 {
		return 0
	}
	var links []*html.Node
	collect(n, func(x *html.Node) bool { return x.Data == "a" }, &links)
	linkLen := 0
	for _, a := range links {
		linkLen += jsLen(nodeText(a))
	}
	return float64(linkLen) / float64(total)
}

// prune removes scripts, navigation, hidden and unlikely-content elements.
func prune(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode && shouldDrop(c) {
			n.RemoveChild(c)
		} else if c.Type == html.CommentNode {
			n.RemoveChild(c)
		} else {
			prune(c)
		}
		c = next
	}
}

func shouldDrop(n *html.Node) bool {
	switch n.Data {
	case "body", "html", "article", "main", "a", "pre", "code", "table", "td", "tr":
		return false
	}
	if droppedTags[n.Data] {
		return true
	}
	if hasAttr(n, "hidden") || attrValue(n, "aria-hidden") == "true" || hiddenStyleRE.MatchString(attrValue(n, "style")) {
		return true
	}
	if role := attrValue(n, "role"); role == "main" || role == "article" {
		return false
	}
	match := attrValue(n, "class") + " " + attrValue(n, "id")
	return unlikelyRE.MatchString(match) && !okMaybeRE.MatchString(match)
}

// pickContent finds the node that holds the article.
func pickContent(body *html.Node) *html.Node {
	var best *html.Node
	bestLen := 0
	var semantic []*html.Node
	collect(body, func(n *html.Node) bool {
		return n.Data == "article" || n.Data == "main" || attrValue(n, "role") == "main"
	}, &semantic)
	for _, n := range semantic {
		if l := jsLen(nodeText(n)); l > bestLen {
			best, bestLen = n, l
		}
	}
	if bestLen >= 200 {
		return best
	}
	scores := map[*html.Node]float64{}
	var order []*html.Node
	bump := func(n *html.Node, v float64) {
		if n == nil || n.Type != html.ElementNode {
			return
		}
		if _, ok := scores[n]; !ok {
			order = append(order, n)
		}
		scores[n] += v
	}
	var paras []*html.Node
	collect(body, func(n *html.Node) bool { return n.Data == "p" || n.Data == "pre" || n.Data == "blockquote" }, &paras)
	for _, p := range paras {
		text := nodeText(p)
		if jsLen(text) < 25 {
			continue
		}
		v := 1 + float64(strings.Count(text, ",")) + minF(float64(jsLen(text))/100, 3)
		bump(p.Parent, v)
		if p.Parent != nil {
			bump(p.Parent.Parent, v/2)
		}
	}
	var top *html.Node
	topScore := 0.0
	for _, n := range order {
		if s := scores[n] * (1 - linkDensity(n)); s > topScore {
			top, topScore = n, s
		}
	}
	if top != nil && top.Data != "html" {
		if best != nil && jsLen(nodeText(best)) > jsLen(nodeText(top)) {
			return best
		}
		return top
	}
	if best != nil {
		return best
	}
	return body
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// cleanConditionally drops link farms (menus, related lists) inside the chosen node.
func cleanConditionally(root *html.Node) {
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			if isElement(c, "ul", "ol", "div", "section", "table") {
				text := nodeText(c)
				var ps []*html.Node
				collect(c, func(x *html.Node) bool { return x.Data == "p" }, &ps)
				if jsLen(text) > 0 && len(ps) < 2 && linkDensity(c) > 0.5 && jsLen(text) < 500 {
					n.RemoveChild(c)
					c = next
					continue
				}
			}
			visit(c)
			c = next
		}
	}
	visit(root)
}

// readableArticle extracts the article of a parsed document; nil when it has no text at all.
func readableArticle(doc *html.Node) (title, markdown string, ok bool) {
	body := firstElement(doc, func(n *html.Node) bool { return n.Data == "body" })
	if body == nil {
		return "", "", false
	}
	title = articleTitle(doc)
	prune(body)
	content := pickContent(body)
	cleanConditionally(content)
	if nodeText(content) == "" {
		return "", "", false
	}
	return title, htmlToMarkdown(content), true
}

// ---- turndown-style Markdown -------------------------------------------------------------

var (
	newlinesRE     = regexp.MustCompile(`\n{3,}`)
	lineStartRE    = regexp.MustCompile(`(?m)^`)
	codeLanguageRE = regexp.MustCompile(`language-(\S+)`)
)

// mdEscape escapes Markdown syntax in text the way turndown does, in one pass: backslash, `*`, backtick, `[`, `]`
// and `_` anywhere; and at the start of a line `-`, `+ `, a run of `=`, one to six `#` and a space, `~~~`, `>`, and
// digits followed by `. `. (This replaced thirteen regexp replacements over the whole text; the equivalence is
// tested against that definition.)
func mdEscape(s string) string {
	if !strings.ContainsAny(s, "\\*`[]_-+=#~>0123456789") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	lineStart := true
	for i := 0; i < len(s); {
		c := s[i]
		if lineStart {
			switch {
			case c == '-' || c == '>':
				b.WriteByte('\\')
			case c == '+' && strings.HasPrefix(s[i:], "+ "):
				b.WriteByte('\\')
			case c == '=':
				b.WriteByte('\\')
				j := i
				for j < len(s) && s[j] == '=' {
					j++
				}
				b.WriteString(s[i:j])
				i, lineStart = j, false
				continue
			case c == '#':
				j := i
				for j < len(s) && s[j] == '#' {
					j++
				}
				if j-i <= 6 && j < len(s) && s[j] == ' ' {
					b.WriteByte('\\')
				}
				b.WriteString(s[i:j])
				i, lineStart = j, false
				continue
			case c == '~' && strings.HasPrefix(s[i:], "~~~"):
				b.WriteByte('\\')
			case c >= '0' && c <= '9':
				j := i
				for j < len(s) && s[j] >= '0' && s[j] <= '9' {
					j++
				}
				b.WriteString(s[i:j])
				if strings.HasPrefix(s[j:], ". ") {
					b.WriteString("\\")
					i, lineStart = j, false
					continue
				}
				i, lineStart = j, false
				continue
			}
		}
		switch c {
		case '\\', '*', '`', '[', ']', '_':
			b.WriteByte('\\')
		}
		b.WriteByte(c)
		lineStart = c == '\n'
		i++
	}
	return b.String()
}

func trimLeadingNL(s string) string { return strings.TrimLeft(s, "\n") }

// mdBuf accumulates sibling outputs with the rules turndown joins blocks by (at most the larger of the newline runs between two outputs, never more than two) without copying what is already there: each join trims the
// tail of the buffer and appends, so building n siblings costs the size of the output, not n times it.
type mdBuf struct{ b []byte }

func (m *mdBuf) empty() bool { return len(m.b) == 0 }

// endsWithSpaceOrNewline is the check mdChildren makes before it drops a leading space.
func (m *mdBuf) endsWithSpaceOrNewline() bool {
	return len(m.b) > 0 && (m.b[len(m.b)-1] == '\n' || m.b[len(m.b)-1] == ' ')
}

func (m *mdBuf) String() string { return string(m.b) }

// join appends b to the accumulated output.
func (m *mdBuf) join(b string) {
	a := m.b
	if strings.HasPrefix(b, "\n") {
		for len(a) > 0 && a[len(a)-1] == ' ' {
			a = a[:len(a)-1]
		}
	}
	end := len(a)
	for end > 0 && a[end-1] == '\n' {
		end--
	}
	nls := len(a) - end
	s2 := trimLeadingNL(b)
	if l := len(b) - len(s2); l > nls {
		nls = l
	}
	nls = min(nls, 2)
	m.b = append(append(a[:end], "\n\n"[:nls]...), s2...)
}

func htmlToMarkdown(root *html.Node) string {
	out := mdChildren(root, false)
	out = strings.TrimLeft(out, "\n")
	out = strings.TrimRight(out, " \t\n")
	return out
}

func mdChildren(n *html.Node, inPre bool) string {
	var out mdBuf
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		var rep string
		switch c.Type {
		case html.TextNode:
			if inPre {
				rep = c.Data
			} else {
				rep = wsRE.ReplaceAllString(c.Data, " ")
				if rep == " " && (out.empty() || out.endsWithSpaceOrNewline()) {
					rep = ""
				} else if strings.HasPrefix(rep, " ") && (out.empty() || out.endsWithSpaceOrNewline()) {
					rep = rep[1:]
				}
				rep = mdEscape(rep)
			}
		case html.ElementNode:
			rep = mdElement(c, inPre)
		}
		if rep == "" {
			continue
		}
		out.join(rep)
	}
	return out.String()
}

func block(content string) string { return "\n\n" + content + "\n\n" }

func mdElement(n *html.Node, inPre bool) string {
	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level, _ := strconv.Atoi(n.Data[1:])
		text := jsTrim(mdChildren(n, false))
		if text == "" {
			return ""
		}
		return block(strings.Repeat("#", level) + " " + text)
	case "p":
		text := mdChildren(n, false)
		if jsTrim(text) == "" {
			return ""
		}
		return block(text)
	case "br":
		return "  \n"
	case "hr":
		return block("* * *")
	case "blockquote":
		text := jsTrim(mdChildren(n, false))
		if text == "" {
			return ""
		}
		text = strings.TrimSpace(text)
		return block(lineStartRE.ReplaceAllString(text, "> "))
	case "ul", "ol":
		content := mdList(n)
		if content == "" {
			return ""
		}
		if n.Parent != nil && n.Parent.Data == "li" && n.NextSibling == nil {
			return "\n" + content
		}
		return block(content)
	case "li":
		return mdListItem(n, "*   ")
	case "pre":
		return mdPre(n)
	case "code":
		if inPre {
			return mdChildren(n, true)
		}
		return mdInlineCode(n)
	case "strong", "b":
		return mdFlanked(n, "**")
	case "em", "i":
		return mdFlanked(n, "_")
	case "a":
		return mdLink(n)
	case "img":
		src := attrValue(n, "src")
		if src == "" {
			return ""
		}
		alt := strings.ReplaceAll(attrValue(n, "alt"), "\n", " ")
		return "![" + mdEscape(alt) + "](" + src + mdTitle(n) + ")"
	case "tr":
		var cells []string
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if isElement(c, "td", "th") {
				if t := jsTrim(mdChildren(c, false)); t != "" {
					cells = append(cells, strings.ReplaceAll(t, "\n\n", " "))
				}
			}
		}
		if len(cells) == 0 {
			return ""
		}
		return block(strings.Join(cells, " | "))
	}
	if droppedTags[n.Data] {
		return ""
	}
	content := mdChildren(n, inPre)
	if blockTags[n.Data] {
		if jsTrim(content) == "" {
			return ""
		}
		return block(content)
	}
	return content
}

func mdTitle(n *html.Node) string {
	title := attrValue(n, "title")
	if title == "" {
		return ""
	}
	return ` "` + strings.ReplaceAll(title, `"`, `\"`) + `"`
}

func mdLink(n *html.Node) string {
	href := attrValue(n, "href")
	content := mdChildren(n, false)
	if href == "" {
		return content
	}
	if jsTrim(content) == "" {
		return ""
	}
	href = strings.ReplaceAll(strings.ReplaceAll(href, "(", `\(`), ")", `\)`)
	return "[" + content + "](" + href + mdTitle(n) + ")"
}

// mdFlanked wraps trimmed content in delimiters, keeping the surrounding whitespace outside.
func mdFlanked(n *html.Node, delim string) string {
	content := mdChildren(n, false)
	trimmed := jsTrim(content)
	if trimmed == "" {
		return ""
	}
	lead := content[:len(content)-len(strings.TrimLeft(content, " \t\n"))]
	trail := content[len(strings.TrimRight(content, " \t\n")):]
	return lead + delim + trimmed + delim + trail
}

func mdInlineCode(n *html.Node) string {
	code := textContent(n)
	if code == "" {
		return ""
	}
	code = strings.ReplaceAll(code, "\n", " ")
	delim := "`"
	for strings.Contains(code, delim) {
		delim += "`"
	}
	extra := ""
	if strings.HasPrefix(code, "`") || strings.HasSuffix(code, "`") {
		extra = " "
	}
	return delim + extra + code + extra + delim
}

func mdPre(n *html.Node) string {
	code := firstElement(n, func(x *html.Node) bool { return x.Data == "code" })
	var text, lang string
	if code != nil {
		text = textContent(code)
		if m := codeLanguageRE.FindStringSubmatch(attrValue(code, "class")); m != nil {
			lang = m[1]
		}
	} else {
		text = textContent(n)
	}
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return ""
	}
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return block(fence + lang + "\n" + text + "\n" + fence)
}

func mdList(n *html.Node) string {
	start := 1
	if n.Data == "ol" {
		if s, err := strconv.Atoi(attrValue(n, "start")); err == nil {
			start = s
		}
	}
	var out mdBuf
	index := 0
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if !isElement(c, "li") {
			continue
		}
		prefix := "*   "
		if n.Data == "ol" {
			num := strconv.Itoa(start+index) + "."
			prefix = num + strings.Repeat(" ", max(1, 4-len(num)))
		}
		index++
		item := mdListItem(c, prefix)
		if item == "" {
			continue
		}
		out.join(item)
	}
	return strings.TrimRight(out.String(), "\n")
}

func mdListItem(n *html.Node, prefix string) string {
	content := mdChildren(n, false)
	content = strings.TrimLeft(content, "\n")
	content = regexp.MustCompile(`\n+$`).ReplaceAllString(content, "\n")
	content = strings.ReplaceAll(content, "\n", "\n    ")
	if jsTrim(content) == "" {
		return ""
	}
	tail := ""
	if n.NextSibling != nil && !strings.HasSuffix(content, "\n") {
		tail = "\n"
	}
	return prefix + content + tail
}
