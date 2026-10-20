package websearch

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// Part 2 of the Markdown stage: the table, list item and figure rules and createMarkdownContent.

func (svc *dfdTurndown) tableRule(content string, node *html.Node) string {
	if tdHasClass(node, "ltx_equation") || tdHasClass(node, "ltx_eqn_table") || tdHasClass(node, "numblk") {
		return tdHandleNestedEquations(node)
	}
	hasNested := len(tdQSA(node, "table")) > 0
	var directCells []*html.Node
	for _, c := range tdQSA(node, "td, th") {
		if tdIsDirectTableChild(c, node) {
			directCells = append(directCells, c)
		}
	}
	if hasNested || len(directCells) <= 1 {
		var directRows []*html.Node
		for _, r := range tdQSA(node, "tr") {
			if tdIsDirectTableChild(r, node) {
				directRows = append(directRows, r)
			}
		}
		counts := make([]int, len(directRows))
		unique := map[int]bool{}
		for i, tr := range directRows {
			for _, cell := range directCells {
				if cell.Parent == tr {
					counts[i]++
				}
			}
			unique[counts[i]] = true
		}
		isSingleColumn := len(directRows) > 0 && len(unique) == 1 && counts[0] <= 1
		if isSingleColumn || hasNested {
			var b strings.Builder
			for _, cell := range directCells {
				b.WriteString(tdInnerHTML(cell))
			}
			return "\n\n" + svc.turndown(b.String()) + "\n\n"
		}
	}
	cells := tdQSA(node, "td, th")
	for _, cell := range cells {
		if _, ok := tdAttr(cell, "colspan"); ok {
			return "\n\n" + tdCleanupTableHTML(node) + "\n\n"
		}
		if _, ok := tdAttr(cell, "rowspan"); ok {
			return "\n\n" + tdCleanupTableHTML(node) + "\n\n"
		}
	}
	// table.rows is getElementsByTagName("tr") and row.cells is querySelectorAll("td,th") in domino.
	rowElements := tdQSA(node, "tr")
	if len(rowElements) == 0 {
		for _, r := range tdQSA(node, "tr") {
			if tdIsDirectTableChild(r, node) {
				rowElements = append(rowElements, r)
			}
		}
	}
	var rows [][]string
	for _, row := range rowElements {
		cellElements := tdQSA(row, "td, th")
		var cellTexts []string
		for _, cell := range cellElements {
			text := svc.turndown(tdInnerHTML(cell))
			text = jsTrim(strings.ReplaceAll(text, "\n", " "))
			text = strings.ReplaceAll(text, "|", `\|`)
			cellTexts = append(cellTexts, text)
		}
		rows = append(rows, cellTexts)
	}
	if len(rows) == 0 {
		return content
	}
	columnCount := 0
	for _, r := range rows {
		if len(r) > columnCount {
			columnCount = len(r)
		}
	}
	if columnCount == 0 {
		return content
	}
	formatRow := func(cells []string) string {
		padded := append([]string(nil), cells...)
		for len(padded) < columnCount {
			padded = append(padded, "")
		}
		return "| " + strings.Join(padded, " | ") + " |"
	}
	sep := make([]string, columnCount)
	for i := range sep {
		sep[i] = "---"
	}
	lines := []string{formatRow(rows[0]), "| " + strings.Join(sep, " | ") + " |"}
	for _, r := range rows[1:] {
		lines = append(lines, formatRow(r))
	}
	return "\n\n" + strings.Join(lines, "\n") + "\n\n"
}

var tdTableAllowedAttrs = map[string]bool{"src": true, "href": true, "style": true, "align": true, "width": true, "height": true, "rowspan": true, "colspan": true, "bgcolor": true, "scope": true, "valign": true, "headers": true}

func tdCleanupTableHTML(element *html.Node) string {
	clone := tdCloneDeep(element)
	var clean func(n *html.Node)
	clean = func(n *html.Node) {
		var keep []html.Attribute
		for _, a := range n.Attr {
			name := a.Key
			if a.Namespace != "" {
				name = a.Namespace + ":" + a.Key
			}
			if tdTableAllowedAttrs[name] {
				keep = append(keep, a)
			}
		}
		n.Attr = keep
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode {
				clean(c)
			}
		}
	}
	clean(clone)
	out := tdOuterHTML(clone)
	out = strings.ReplaceAll(out, "&amp;", "&")
	out = strings.ReplaceAll(out, "&lt;", "<")
	out = strings.ReplaceAll(out, "&gt;", ">")
	return out
}

func tdHandleNestedEquations(element *html.Node) string {
	mathElements := tdQSA(element, "math")
	if len(mathElements) == 0 {
		return ""
	}
	var parts []string
	for _, m := range mathElements {
		latex := ""
		if ann := tdQS(m, `annotation[encoding="application/x-tex"]`); ann != nil {
			latex = jsTrim(tdTextContent(ann))
		}
		if latex == "" {
			latex = jsTrim(tdAttrStr(m, "alttext"))
		}
		if latex != "" {
			if tdClosest(m, ".ltx_eqn_inline, .mwe-math-element-inline") != nil {
				parts = append(parts, "$"+latex+"$")
			} else {
				parts = append(parts, "\n$$\n"+latex+"\n$$")
			}
		} else {
			parts = append(parts, "")
		}
	}
	return strings.Join(parts, "\n\n")
}

func (svc *dfdTurndown) listItemRule(content string, node *html.Node) string {
	isTask := tdHasClass(node, "task-list-item")
	checkbox := tdQS(node, `input[type="checkbox"]`)
	taskMarker := ""
	if isTask && checkbox != nil {
		content = reInputTag.ReplaceAllString(content, "")
		taskMarker = "[ ] "
		if v, _ := tdAttr(checkbox, "checked"); v != "" {
			taskMarker = "[x] "
		}
	}
	content = reTrailingNLs.ReplaceAllString(content, "")
	var kept []string
	for _, line := range strings.Split(content, "\n") {
		if line != "" {
			kept = append(kept, line)
		}
	}
	content = strings.Join(kept, "\n\t")
	prefix := "- "
	parent := node.Parent
	level := 0
	for cp := node.Parent; cp != nil && cp.Type == html.ElementNode; cp = cp.Parent {
		if tdName(cp) == "UL" || tdName(cp) == "OL" {
			level++
		} else if tdName(cp) != "LI" {
			break
		}
	}
	indent := level - 1
	if indent < 0 {
		indent = 0
	}
	prefix = strings.Repeat("\t", indent) + prefix
	if parent != nil && parent.Type == html.ElementNode && tdName(parent) == "OL" {
		start := tdAttrStr(parent, "start")
		index := 1
		for i, c := range tdElemChildren(parent) {
			if c == node {
				index = i + 1
				break
			}
		}
		num := ""
		if start != "" {
			num = jsNumberString(jsToNumber(start) + float64(index) - 1)
		} else {
			num = strconv.Itoa(index)
		}
		prefix = strings.Repeat("\t", level-1) + num + ". "
	}
	suffix := ""
	if node.NextSibling != nil && !strings.HasSuffix(content, "\n") {
		suffix = "\n"
	}
	return prefix + taskMarker + jsTrim(content) + suffix
}

func (svc *dfdTurndown) figureRule(content string, node *html.Node) string {
	img := tdQS(node, "img")
	figcaption := tdQS(node, "figcaption")
	if img == nil {
		return content
	}
	for _, p := range tdQSA(node, "p") {
		outside := true
		for a := p.Parent; a != nil && a != node; a = a.Parent {
			if tdName(a) == "FIGCAPTION" {
				outside = false
				break
			}
		}
		if outside {
			return content
		}
	}
	alt := tdAttrStr(img, "alt")
	src := dfdGetBestImageSrc(img)
	caption := ""
	if figcaption != nil {
		tagText := ""
		if ts := tdQS(figcaption, ".ltx_tag_figure"); ts != nil {
			tagText = jsTrim(tdTextContent(ts))
		}
		captionContent := tdInnerHTML(figcaption)
		var b strings.Builder
		last := 0
		for _, m := range reMathInCap.FindAllStringSubmatchIndex(captionContent, -1) {
			b.WriteString(captionContent[last:m[0]])
			match := captionContent[m[0]:m[1]]
			latex := ""
			if frag := dfdParseHTMLFragment(match); frag != nil {
				if mathEl := tdQS(frag, "math"); mathEl != nil {
					latex = tdExtractLatex(mathEl)
				}
			}
			prevChar, nextChar := "", ""
			if m[0] > 0 {
				prevChar = tdLastUnit(captionContent[:m[0]])
			}
			if m[1] < len(captionContent) {
				nextChar = tdFirstUnit(captionContent[m[1]:])
			}
			isStart := m[0] == 0 || tdIsSpace(prevChar)
			isEnd := m[1] == len(captionContent) || tdIsSpace(nextChar)
			left, right := "", ""
			if !isStart && !tdIsSpaceOrDollar(prevChar) {
				left = " "
			}
			if !isEnd && !tdIsSpaceOrDollar(nextChar) {
				right = " "
			}
			b.WriteString(left + "$" + latex + "$" + right)
			last = m[1]
		}
		b.WriteString(captionContent[last:])
		captionMarkdown := svc.turndown(b.String())
		caption = jsTrim(tagText + " " + captionMarkdown)
	}
	return "![" + alt + "](" + src + ")\n\n" + caption + "\n\n"
}

func tdIsSpace(s string) bool {
	if s == "" {
		return false
	}
	r, _ := decodeRune(s)
	return jsSpace(r)
}

// dfdParseHTMLFragment is dom.parseHTML for the Markdown stage: the nodes of the markup under a container.
func dfdParseHTMLFragment(markup string) *html.Node {
	doc, err := html.Parse(strings.NewReader(`<x-fragment>` + markup + `</x-fragment>`))
	if err != nil {
		return nil
	}
	var root *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if root != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "x-fragment" {
			root = n
			return
		}
		for c := n.FirstChild; c != nil && root == nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	return root
}

var dfdTurndownService *dfdTurndown

// dfdCreateMarkdownContent is createMarkdownContent: the content HTML as Markdown.
func dfdCreateMarkdownContent(content string) string {
	svc := newDfdTurndown()
	content = reWbr.ReplaceAllString(content, "")
	markdown := svc.turndown(content)
	if loc := reTagPrefixH1.FindStringIndex(markdown); loc != nil {
		markdown = markdown[loc[1]:]
	}
	markdown = dfdRemoveEmptyLinks(markdown)
	markdown = dfdSpaceBangBeforeImage(markdown)
	markdown = reNL3Run.ReplaceAllString(markdown, "\n\n")
	markdown = reBlankQuote.ReplaceAllString(markdown, "$1")
	return jsTrim(markdown)
}

// dfdRemoveEmptyLinks is markdown.replace(/\n*(?<!!)\[]\([^)]+\)\n*/g, '').
func dfdRemoveEmptyLinks(s string) string {
	locs := reEmptyLink.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		start, end := loc[0], loc[1]
		if start > 0 && s[start-1] == '!' {
			continue
		}
		for start > last && s[start-1] == '\n' {
			start--
		}
		if start < last {
			start = last
		}
		for end < len(s) && s[end] == '\n' {
			end++
		}
		b.WriteString(s[last:start])
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

// dfdSpaceBangBeforeImage is markdown.replace(/!(?=!\[|\[!\[)/g, '! ').
func dfdSpaceBangBeforeImage(s string) string {
	if !strings.Contains(s, "!") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		b.WriteByte(s[i])
		if s[i] == '!' && (strings.HasPrefix(s[i+1:], "![") || strings.HasPrefix(s[i+1:], "[![")) {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

var _ = regexp.MustCompile
