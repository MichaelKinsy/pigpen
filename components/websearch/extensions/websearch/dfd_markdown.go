package websearch

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// Port of defuddle 0.19.3 dist/markdown.js: the rules Defuddle adds to turndown and createMarkdownContent.

var (
	reWidthDesc    = regexp.MustCompile(`^(\d+)w,?$`)
	reDensityDesc  = regexp.MustCompile(`^\d+(?:\.\d+)?x,?$`)
	reTitleNL      = jsRE(`(\n+\s*)+`, "")
	reTrailingNLs  = regexp.MustCompile(`\n+$`)
	reInputTag     = regexp.MustCompile(`<input[^>]*>`)
	reLangClass    = regexp.MustCompile(`language-(\w+)`)
	reYouTube      = regexp.MustCompile(`(?:youtube\.com|youtube-nocookie\.com|youtu\.be)`)
	reTwitter      = regexp.MustCompile(`(?:twitter\.com|x\.com)`)
	reYouTubeID    = regexp.MustCompile(`(?:https?://)?(?:www\.)?(?:youtube\.com|youtube-nocookie\.com|youtu\.be)/(?:embed/|watch\?v=)?([a-zA-Z0-9_-]+)`)
	reTweetDirect  = regexp.MustCompile(`(?:https?://)?(?:www\.)?(?:twitter\.com|x\.com)/([^/]+)/status/([0-9]+)`)
	reTweetEmbed   = regexp.MustCompile(`(?:https?://)?(?:platform\.)?twitter\.com/embed/Tweet\.html\?.*?id=([0-9]+)`)
	reMathInCap    = jsRE(`<math.*?>(.*?)</math>`, "")
	reTagPrefixH1  = jsRE(`^# .+\n+`, "")
	reEmptyLink    = regexp.MustCompile(`\[\]\([^)]+\)`)
	reNL3Run       = regexp.MustCompile(`\n{3,}`)
	reBlankQuote   = regexp.MustCompile(`(?m)^([ \t]*>[ \t>]*)(?:\n[ \t]*>[ \t>]*)+$`)
	reWbr          = jsRE(`<wbr\s*/?>`, "i")
	reOLTag        = regexp.MustCompile(`^<span class="ltx_tag ltx_tag_item">\d+\.</span>\s*`)
	reTrailingBack = jsRE(`\s*\x{21a9}\x{fe0e}$`, "")
	reCiteNoteID   = jsRE(`cite_note-(.+)`, "")
	reLatexBegin2  = regexp.MustCompile(`\\begin\{[^}]+\}`)
	reBareMatrix   = jsRE(`^\\begin\{matrix\}([\s\S]*?)\\end\{matrix\}$`, "")
	reLatexSource  = jsRE(`\\[a-zA-Z]+|[_^{}]|[$&]|\\\\|\\begin\{`, "")
	reHyphenWord   = jsRE(`[a-zA-Z]{3,}-[a-zA-Z]{2,}`, "")
	reNbspEntity   = regexp.MustCompile(`&nbsp;`)
	reAmpNbsp      = regexp.MustCompile(`&amp;nbsp;`)
)

func dfdFormatMarkdownLinkDestination(href string) string {
	hasSpace := false
	for _, r := range href {
		if jsSpace(r) {
			hasSpace = true
			break
		}
	}
	if !hasSpace {
		return strings.NewReplacer("(", `\(`, ")", `\)`).Replace(href)
	}
	return "<" + strings.ReplaceAll(href, ">", `\>`) + ">"
}

func dfdFormatMarkdownLinkTitle(title string) string {
	if title == "" {
		return ""
	}
	t := tdCleanAttributeLoose(title)
	return ` "` + strings.ReplaceAll(t, `"`, `\"`) + `"`
}

// tdCleanAttributeLoose is title.replace(/(\n+\s*)+/g, '\n').
func tdCleanAttributeLoose(s string) string { return tdCleanAttribute(s) }

func dfdGetBestImageSrc(n *html.Node) string {
	if srcset, ok := tdAttr(n, "srcset"); ok && srcset != "" {
		bestURL := ""
		bestWidth := 0
		var urlParts []string
		for _, token := range jsSplitWS(jsTrim(srcset)) {
			if m := reWidthDesc.FindStringSubmatch(token); m != nil {
				width, _ := strconv.Atoi(m[1])
				if len(urlParts) > 0 && width > bestWidth {
					url := reLeadingComma.ReplaceAllString(strings.Join(urlParts, " "), "")
					if url != "" {
						bestWidth = width
						bestURL = url
					}
				}
				urlParts = nil
			} else if reDensityDesc.MatchString(token) {
				urlParts = nil
			} else {
				urlParts = append(urlParts, token)
			}
		}
		if bestURL != "" {
			return bestURL
		}
	}
	v, _ := tdAttr(n, "src")
	return v
}

func tdAttrStr(n *html.Node, name string) string {
	v, _ := tdAttr(n, name)
	return v
}

func tdIsDirectTableChild(el, ancestor *html.Node) bool {
	p := el.Parent
	for p != nil && p != ancestor {
		if tdName(p) == "TABLE" {
			return false
		}
		p = p.Parent
	}
	return p == ancestor
}

func tdPrevSiblingElementIsP(n *html.Node) bool {
	return n != nil && n.Type == html.ElementNode && strings.ToLower(tdName(n)) == "p"
}

// ---- latex -----------------------------------------------------------------------------------------------------

var dfdMathMLNodeNames = map[string]bool{}
var dfdComplexMathMLNodeNames = map[string]bool{}

func init() {
	for _, n := range strings.Fields(`annotation maction math menclose merror mfenced mfrac mi mmultiscripts mn mo mover mpadded mphantom mprescripts mroot mrow ms mspace msqrt mstyle msub msubsup msup mtable mtd mtext mtr munder munderover none semantics`) {
		dfdMathMLNodeNames[n] = true
	}
	for _, n := range strings.Fields(`menclose mfrac mmultiscripts mover mroot msqrt msub msubsup msup mtable mtd mtr munder munderover`) {
		dfdComplexMathMLNodeNames[n] = true
	}
}

func tdHasMathMLChildren(el *html.Node) bool {
	for _, c := range tdElemChildren(el) {
		if c.Namespace == "math" || dfdMathMLNodeNames[strings.ToLower(tdName(c))] {
			return true
		}
	}
	return false
}

func tdHasComplexMathMLChildren(n *html.Node) bool {
	if dfdComplexMathMLNodeNames[strings.ToLower(tdName(n))] {
		return true
	}
	for _, c := range tdElemChildren(n) {
		if tdHasComplexMathMLChildren(c) {
			return true
		}
	}
	return false
}

func tdIsTrustworthyLatexAttribute(value string, hasComplex bool) bool {
	if reLatexSource.MatchString(value) {
		return true
	}
	trimmed := jsTrim(value)
	if trimmed == "" {
		return false
	}
	if hasComplex {
		return false
	}
	return !reHyphenWord.MatchString(trimmed)
}

func tdExtractLatex(el *html.Node) string {
	if ann := tdQS(el, `annotation[encoding="application/x-tex"]`); ann != nil {
		if t := jsTrim(tdTextContent(ann)); t != "" {
			return t
		}
	}
	latex, hasLatex := tdAttr(el, "data-latex")
	alttext, hasAlt := tdAttr(el, "alttext")
	hasMathML := tdHasMathMLChildren(el)
	hasComplex := tdHasComplexMathMLChildren(el)
	if hasLatex && latex != "" && (!hasMathML || tdIsTrustworthyLatexAttribute(latex, hasComplex)) {
		return jsTrim(latex)
	} else if hasAlt && alttext != "" && (!hasMathML || tdIsTrustworthyLatexAttribute(alttext, hasComplex)) {
		return jsTrim(alttext)
	}
	if strings.ToLower(tdName(el)) == "math" && hasMathML {
		if c := tdConvertMathMLToLatex(el); c != "" {
			return c
		}
	}
	if hasLatex && latex != "" {
		return jsTrim(latex)
	}
	if hasAlt && alttext != "" {
		return jsTrim(alttext)
	}
	return ""
}

func tdConvertMathMLToLatex(el *html.Node) string {
	mathML := tdOuterHTML(el)
	mathML = reAmpNbsp.ReplaceAllString(mathML, "&#xA0;")
	mathML = reNbspEntity.ReplaceAllString(mathML, "&#xA0;")
	out, ok := dfdMathMLToLatex(mathML)
	if !ok {
		return ""
	}
	return jsTrim(out)
}

func tdHasLatexEnvironment(v string) bool { return reLatexBegin2.MatchString(v) }

func tdFormatBlockLatex(value string) string {
	latex := jsTrim(value)
	if latex == "" {
		return latex
	}
	if m := reBareMatrix.FindStringSubmatch(latex); m != nil && !tdHasLatexEnvironment(m[1]) {
		return "\\begin{aligned}\n" + jsTrim(m[1]) + "\n\\end{aligned}"
	}
	if tdHasLatexEnvironment(latex) {
		return latex
	}
	if strings.Contains(latex, `\\`) || strings.Contains(latex, "&") {
		return "\\begin{aligned}\n" + latex + "\n\\end{aligned}"
	}
	return latex
}

func tdIsOnlyMathInParagraph(el *html.Node) bool {
	parent := el.Parent
	if parent == nil || parent.Type != html.ElementNode || strings.ToLower(tdName(parent)) != "p" {
		return false
	}
	kids := tdElemChildren(parent)
	if len(kids) != 1 || kids[0] != el {
		return false
	}
	for c := parent.FirstChild; c != nil; c = c.NextSibling {
		if c != el && !(c.Type == html.TextNode && jsTrim(c.Data) == "") {
			return false
		}
	}
	return true
}

func tdLastUnit(s string) string {
	if s == "" {
		return ""
	}
	r, size := lastRune(s)
	return s[len(s)-size:] + string(r)[:0]
}

func tdFirstUnit(s string) string {
	if s == "" {
		return ""
	}
	_, size := decodeRune(s)
	return s[:size]
}

func tdIsSpaceOrDollar(s string) bool {
	if s == "" {
		return false
	}
	if s == "$" {
		return true
	}
	r, _ := decodeRune(s)
	return jsSpace(r)
}

// ---- the rules -------------------------------------------------------------------------------------------------

func newDfdTurndown() *dfdTurndown {
	svc := &dfdTurndown{}
	// Rules in the order the original adds them; each goes in front of the earlier ones.
	add := func(r dfdTdRule) { svc.rules = append([]dfdTdRule{r}, svc.rules...) }

	add(dfdTdRule{filter: tdFilterNames("table"), replacement: svc.tableRule})
	svc.remove = append([]dfdTdRule{{filter: tdFilterNames("style", "script"), replacement: func(string, *html.Node) string { return "" }}}, svc.remove...)
	svc.keep = append([]dfdTdRule{{filter: tdFilterNames("iframe", "video", "audio", "sup", "sub", "svg", "math"), replacement: func(_ string, n *html.Node) string {
		if tdIs(n, tdBlockElements) {
			return "\n\n" + tdOuterHTML(n) + "\n\n"
		}
		return tdOuterHTML(n)
	}}}, svc.keep...)
	add(dfdTdRule{filter: tdFilterNames("button"), replacement: func(c string, _ *html.Node) string { return c }})
	add(dfdTdRule{filter: tdFilterNames("ul", "ol"), replacement: func(content string, n *html.Node) string {
		content = jsTrim(content)
		top := !(n.Parent != nil && (tdName(n.Parent) == "UL" || tdName(n.Parent) == "OL"))
		if top {
			return "\n" + content + "\n"
		}
		return content + "\n"
	}})
	add(dfdTdRule{filter: tdFilterNames("li"), replacement: svc.listItemRule})
	add(dfdTdRule{filter: tdFilterNames("figure"), replacement: svc.figureRule})
	add(dfdTdRule{filter: tdFilterNames("img"), replacement: func(_ string, n *html.Node) string {
		alt := tdAttrStr(n, "alt")
		src := dfdGetBestImageSrc(n)
		title := tdAttrStr(n, "title")
		titlePart := ""
		if title != "" {
			titlePart = ` "` + title + `"`
		}
		if src == "" {
			return ""
		}
		return "![" + alt + "](" + src + titlePart + ")"
	}})
	add(dfdTdRule{filter: func(n *html.Node) bool {
		src, ok := tdAttr(n, "src")
		return ok && src != "" && (reYouTube.MatchString(src) || reTwitter.MatchString(src))
	}, replacement: func(content string, n *html.Node) string {
		src := tdAttrStr(n, "src")
		if src != "" {
			if m := reYouTubeID.FindStringSubmatch(src); m != nil && m[1] != "" {
				return "\n![](https://www.youtube.com/watch?v=" + m[1] + ")\n"
			}
			if m := reTweetDirect.FindStringSubmatch(src); m != nil {
				return "\n![](https://x.com/" + m[1] + "/status/" + m[2] + ")\n"
			}
			if m := reTweetEmbed.FindStringSubmatch(src); m != nil {
				return "\n![](https://x.com/i/status/" + m[1] + ")\n"
			}
		}
		return content
	}})
	add(dfdTdRule{filter: tdFilterNames("mark"), replacement: func(c string, _ *html.Node) string { return "==" + c + "==" }})
	add(dfdTdRule{filter: func(n *html.Node) bool {
		name := tdName(n)
		return name == "DEL" || name == "S" || name == "STRIKE"
	}, replacement: func(c string, _ *html.Node) string { return "~~" + c + "~~" }})
	add(dfdTdRule{filter: tdFilterNames("a"), replacement: func(content string, n *html.Node) string {
		href, _ := tdAttr(n, "href")
		if href == "" {
			return content
		}
		title := dfdFormatMarkdownLinkTitle(tdAttrStr(n, "title"))
		return "[" + content + "](" + dfdFormatMarkdownLinkDestination(href) + title + ")"
	}})
	add(dfdTdRule{filter: func(n *html.Node) bool {
		if tdName(n) != "A" {
			return false
		}
		count := 0
		has := false
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			count++
			switch tdName(c) {
			case "H1", "H2", "H3", "H4", "H5", "H6":
				has = true
			}
		}
		return count > 1 && has
	}, replacement: func(_ string, n *html.Node) string {
		href, _ := tdAttr(n, "href")
		title, _ := tdAttr(n, "title")
		headingContent := ""
		heading := tdQS(n, "h1, h2, h3, h4, h5, h6")
		if heading != nil {
			headingContent = svc.turndown(tdOuterHTML(heading))
			tdRemoveNode(heading)
		}
		remaining := svc.turndown(tdInnerHTML(n))
		md := headingContent + "\n\n" + remaining + "\n\n"
		if href != "" {
			md += "[View original](" + dfdFormatMarkdownLinkDestination(href) + dfdFormatMarkdownLinkTitle(title) + ")"
		}
		return md
	}})
	add(dfdTdRule{filter: func(n *html.Node) bool { return tdName(n) == "OL" && tdHasClass(n, "ltx_enumerate") }, replacement: func(_ string, n *html.Node) string {
		var items []string
		for i, item := range tdElemChildren(n) {
			itemContent := reOLTag.ReplaceAllString(tdInnerHTML(item), "")
			items = append(items, strconv.Itoa(i+1)+". "+svc.turndown(itemContent))
		}
		return "\n\n" + strings.Join(items, "\n\n") + "\n\n"
	}})
	add(dfdTdRule{filter: func(n *html.Node) bool {
		id, ok := tdAttr(n, "id")
		return tdName(n) == "SUP" && ok && strings.HasPrefix(id, "fnref:")
	}, replacement: func(content string, n *html.Node) string {
		id, _ := tdAttr(n, "id")
		return "[^" + strings.Split(strings.Replace(id, "fnref:", "", 1), "-")[0] + "]"
	}})
	add(dfdTdRule{filter: func(n *html.Node) bool {
		return tdName(n) == "OL" && n.Parent != nil && n.Parent.Type == html.ElementNode && tdAttrStr(n.Parent, "id") == "footnotes"
	}, replacement: func(_ string, n *html.Node) string {
		var refs []string
		for _, li := range tdElemChildren(n) {
			id := ""
			hasID := false
			if liID, ok := tdAttr(li, "id"); ok {
				hasID = true
				if strings.HasPrefix(liID, "fn:") {
					id = strings.Replace(liID, "fn:", "", 1)
				} else {
					segs := strings.Split(liID, "/")
					if m := reCiteNoteID.FindStringSubmatch(segs[len(segs)-1]); m != nil {
						id = m[1]
					} else {
						id = liID
					}
				}
			}
			if sup := tdQS(li, "sup"); sup != nil && hasID && jsTrim(tdTextContent(sup)) == id {
				tdRemoveNode(sup)
			}
			referenceContent := svc.turndown(tdInnerHTML(li))
			cleaned := jsTrim(reTrailingBack.ReplaceAllString(referenceContent, ""))
			idLower := "undefined"
			if hasID {
				idLower = strings.ToLower(id)
			}
			refs = append(refs, "[^"+idLower+"]: "+cleaned)
		}
		return "\n\n" + strings.Join(refs, "\n\n") + "\n\n"
	}})
	add(dfdTdRule{filter: func(n *html.Node) bool {
		if h, ok := tdAttr(n, "href"); ok && strings.Contains(h, "#fnref") {
			return true
		}
		return tdHasClass(n, "footnote-backref")
	}, replacement: func(string, *html.Node) string { return "" }})
	add(dfdTdRule{filter: tdFilterNames("pre"), replacement: func(content string, n *html.Node) string {
		code := tdQS(n, "code")
		if code == nil {
			return content
		}
		language := tdAttrStr(code, "data-lang")
		if language == "" {
			language = tdAttrStr(code, "data-language")
		}
		if language == "" {
			if m := reLangClass.FindStringSubmatch(tdAttrStr(code, "class")); m != nil {
				language = m[1]
			}
		}
		if language == "" {
			language = tdAttrStr(n, "data-language")
		}
		clean := strings.ReplaceAll(jsTrim(tdTextContent(code)), "`", "\\`")
		return "\n```" + language + "\n" + clean + "\n```\n"
	}})
	add(dfdTdRule{filter: func(n *html.Node) bool {
		if strings.ToLower(tdName(n)) == "math" {
			return true
		}
		return tdHasClass(n, "mwe-math-element") || tdHasClass(n, "mwe-math-fallback-image-inline") || tdHasClass(n, "mwe-math-fallback-image-display")
	}, replacement: func(_ string, n *html.Node) string {
		latex := jsTrim(tdExtractLatex(n))
		isInTable := tdClosest(n, "table") != nil
		blockParent := false
		if p := n.Parent; p != nil && p.Type == html.ElementNode && tdHasClass(p, "mwe-math-element") && p.PrevSibling != nil && p.PrevSibling.Type == html.ElementNode && strings.ToLower(tdName(p.PrevSibling)) == "p" {
			blockParent = true
		}
		if !isInTable && (tdAttrStr(n, "display") == "block" || tdHasClass(n, "mwe-math-fallback-image-display") || tdIsOnlyMathInParagraph(n) || blockParent) {
			latex = tdFormatBlockLatex(latex)
			return "\n$$\n" + latex + "\n$$\n"
		}
		prevNode, nextNode := n.PrevSibling, n.NextSibling
		prevChar, nextChar := "", ""
		if prevNode != nil && prevNode.Type == html.ElementNode {
			prevChar = tdLastUnit(tdTextContent(prevNode))
		}
		if nextNode != nil && nextNode.Type == html.ElementNode {
			nextChar = tdFirstUnit(tdTextContent(nextNode))
		}
		isStart := prevNode == nil || (prevNode.Type == html.TextNode && jsTrim(prevNode.Data) == "")
		isEnd := nextNode == nil || (nextNode.Type == html.TextNode && jsTrim(nextNode.Data) == "")
		left, right := "", ""
		if !isStart && prevChar != "" && !tdIsSpaceOrDollar(prevChar) {
			left = " "
		}
		if !isEnd && nextChar != "" && !tdIsSpaceOrDollar(nextChar) {
			right = " "
		}
		return left + "$" + latex + "$" + right
	}})
	add(dfdTdRule{filter: func(n *html.Node) bool { return tdHasClass(n, "math") || tdHasClass(n, "katex") }, replacement: func(_ string, n *html.Node) string {
		latex := tdAttrStr(n, "data-latex")
		if latex == "" {
			if m := tdQS(n, `.katex-mathml annotation[encoding="application/x-tex"]`); m != nil {
				latex = tdTextContent(m)
			}
		}
		if latex == "" {
			latex = jsTrim(tdTextContent(n))
		}
		mathElement := tdQS(n, ".katex-mathml math")
		isInline := tdHasClass(n, "math-inline") || (mathElement != nil && tdAttrStr(mathElement, "display") != "block")
		if isInline {
			return "$" + latex + "$"
		}
		return "\n$$\n" + latex + "\n$$\n"
	}})
	add(dfdTdRule{filter: func(n *html.Node) bool {
		return tdAttrStr(n, "data-callout") != "" && tdHasClass(n, "callout")
	}, replacement: func(_ string, n *html.Node) string {
		typ := tdAttrStr(n, "data-callout")
		if typ == "" {
			typ = "note"
		}
		fold := tdAttrStr(n, "data-callout-fold")
		foldIndicator := ""
		if fold == "-" || fold == "+" {
			foldIndicator = fold
		}
		title := ""
		if ti := tdQS(n, ".callout-title-inner"); ti != nil {
			title = jsTrim(tdTextContent(ti))
		}
		if title == "" {
			title = dfdCapitalize(typ)
		}
		if td := tdQS(n, ".callout-title"); td != nil {
			tdRemoveNode(td)
		}
		var calloutContent string
		if contentEl := tdQS(n, ".callout-content"); contentEl != nil {
			calloutContent = svc.turndown(tdInnerHTML(contentEl))
		} else {
			calloutContent = svc.turndown(tdInnerHTML(n))
		}
		lines := strings.Split(jsTrim(calloutContent), "\n")
		for i, l := range lines {
			lines[i] = "> " + l
		}
		return "\n\n> [!" + typ + "]" + foldIndicator + " " + title + "\n" + strings.Join(lines, "\n") + "\n\n"
	}})

	// turndown's commonmark rules, with the options Defuddle sets.
	svc.rules = append(svc.rules,
		dfdTdRule{filter: tdFilterNames("p"), replacement: func(c string, _ *html.Node) string { return "\n\n" + c + "\n\n" }},
		dfdTdRule{filter: tdFilterNames("br"), replacement: func(string, *html.Node) string { return "  \n" }},
		dfdTdRule{filter: tdFilterNames("h1", "h2", "h3", "h4", "h5", "h6"), replacement: func(c string, n *html.Node) string {
			return "\n\n" + strings.Repeat("#", int(tdName(n)[1]-'0')) + " " + c + "\n\n"
		}},
		dfdTdRule{filter: tdFilterNames("blockquote"), replacement: func(c string, _ *html.Node) string {
			return "\n\n" + tdLinePrefix(tdTrimNewlines(c), "> ") + "\n\n"
		}},
		dfdTdRule{filter: tdFilterNames("hr"), replacement: func(string, *html.Node) string { return "\n\n---\n\n" }},
		dfdTdRule{filter: tdFilterNames("em", "i"), replacement: func(c string, _ *html.Node) string {
			if jsTrim(c) == "" {
				return ""
			}
			return "*" + c + "*"
		}},
		dfdTdRule{filter: tdFilterNames("strong", "b"), replacement: func(c string, _ *html.Node) string {
			if jsTrim(c) == "" {
				return ""
			}
			return "**" + c + "**"
		}},
		dfdTdRule{filter: func(n *html.Node) bool {
			hasSiblings := n.PrevSibling != nil || n.NextSibling != nil
			isCodeBlock := n.Parent != nil && tdName(n.Parent) == "PRE" && !hasSiblings
			return tdName(n) == "CODE" && !isCodeBlock
		}, replacement: func(c string, _ *html.Node) string { return tdInlineCode(c) }},
	)
	return svc
}
