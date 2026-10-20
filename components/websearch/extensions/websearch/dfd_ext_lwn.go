package websearch

import "strings"

// Port of defuddle 0.19.3 dist/extractors/lwn.js: an LWN.net article and its comment boxes.

type dfdLwn struct{ ctx dfdExtCtx }

var (
	reLwnAuthor = jsRE(`by\s+(\w+)`, "i")
	reLwnPosted = jsRE(`Posted\s+(\w+\s+\d+,\s+\d{4})`, "")
)

func init() {
	dfdRegisterExtractor("lwn", func(c dfdExtCtx) dfdExtractor { return &dfdLwn{ctx: c} })
}

func (e *dfdLwn) name() string { return "lwn" }

func (e *dfdLwn) canExtract() bool {
	return ldQuerySelector(e.ctx.doc, ".PageHeadline") != nil && ldQuerySelector(e.ctx.doc, ".ArticleText") != nil
}

func (e *dfdLwn) extract() dfdExtracted {
	doc := e.ctx.doc
	main := ldQuerySelector(doc, ".ArticleText main")
	articleContent, comments := "", ""
	if main != nil {
		articleContent = e.getArticleContent(main)
		comments = e.extractComments(main)
	}
	contentHTML := dfdBuildContentHTML("lwn", articleContent, comments)
	byline := extText(ldQuerySelector(doc, ".Byline"))
	author := ""
	if m := reLwnAuthor.FindStringSubmatch(byline); m != nil {
		author = m[1]
	}
	return dfdExtracted{
		content:     contentHTML,
		contentHTML: contentHTML,
		variables: map[string]string{
			"title":       extText(ldQuerySelector(doc, ".PageHeadline h1")),
			"author":      author,
			"site":        "LWN.net",
			"published":   lwnParseDate(byline),
			"description": extAttr(ldQuerySelector(doc, `meta[property="og:description"]`), "content"),
		},
	}
}

func lwnParseDate(text string) string {
	m := reLwnPosted.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	ms := jsDateFromString(m[1])
	if jsIsNaN(ms) {
		return ""
	}
	return jsISODate(ms)
}

func (e *dfdLwn) getArticleContent(main *ldNode) string {
	clone := main.cloneNode(true)
	extRemoveAll(clone, `details.CommentBox, form, a[name^="Comm"]`)
	// Remove trailing <hr> and <br clear="all"> separating article from comments.
	last := clone.lastElementChild()
	for last != nil && (last.is("hr") || (last.is("br") && last.attr("clear") != "")) {
		prev := last.previousElementSibling()
		last.remove()
		last = prev
	}
	return extSerialize(clone)
}

func (e *dfdLwn) extractComments(main *ldNode) string {
	var data []dfdComment
	for _, box := range ldQuerySelectorAll(main, "details.CommentBox") {
		depth := lwnCommentDepth(box, main)
		if c, ok := e.extractCommentData(box, depth); ok {
			data = append(data, c)
		}
	}
	if len(data) > 0 {
		return dfdBuildCommentTree(data)
	}
	return ""
}

func lwnCommentDepth(el, root *ldNode) float64 {
	depth := 0.0
	for parent := el.parentElement(); parent != nil && parent != root; parent = parent.parentElement() {
		if parent.is("details") && parent.hasClass("CommentBox") {
			depth++
		}
	}
	return depth
}

func (e *dfdLwn) extractCommentData(box *ldNode, depth float64) (dfdComment, bool) {
	poster := ldQuerySelector(box, ":scope > summary .CommentPoster")
	if poster == nil {
		return dfdComment{}, false
	}
	author := extText(ldQuerySelector(poster, "b"))
	articlePath := extAttr(ldQuerySelector(poster, `a[href^="/Articles/"]`), "href")
	url := ""
	if articlePath != "" {
		url = "https://lwn.net" + articlePath
	}
	date := lwnParseDate(poster.textContent())
	title := extText(ldQuerySelector(box, ":scope > summary h3.CommentTitle"))
	parentTitle := ""
	if pe := box.parentElement(); pe != nil {
		if parentBox := ldClosest(pe, "details.CommentBox"); parentBox != nil {
			parentTitle = extText(ldQuerySelector(parentBox, ":scope > summary h3.CommentTitle"))
		}
	}
	uniqueTitle := ""
	if title != "" && title != parentTitle {
		uniqueTitle = title
	}
	return dfdComment{author: author, date: date, content: e.getCommentContent(box, uniqueTitle), depth: depth, url: url}, true
}

func (e *dfdLwn) getCommentContent(box *ldNode, title string) string {
	content := ""
	if title != "" {
		content += "<p><strong>" + dfdEscapeHTML(title) + "</strong></p>"
	}
	if formatted := ldQuerySelector(box, ":scope > .FormattedComment"); formatted != nil {
		content += extSerialize(formatted)
		return content
	}
	// Collect direct content nodes, skipping structural elements.
	temp := ldNewElement("div")
	for _, child := range box.childNodes() {
		if child.kind == ldElem {
			if child.is("summary") || child.is("details") || child.hasClass("CommentReplyButton") {
				continue
			}
			if child.is("form") {
				continue
			}
			if child.is("a") && strings.HasPrefix(child.attr("name"), "CommAnchor") {
				continue
			}
			if child.is("p") && extText(child) == "" {
				continue
			}
		}
		temp.appendChild(child.cloneNode(true))
	}
	if text := jsTrim(extSerialize(temp)); text != "" {
		content += text
	}
	return content
}
