package websearch

import (
	"strings"
)

// Port of defuddle 0.19.3 dist/extractors/x-article.js: a long-form X article rendered in the page.

type dfdXArticle struct {
	ctx              dfdExtCtx
	articleContainer *ldNode
}

var (
	reXaURLAuthor = jsRE(`\/([a-zA-Z0-9_][a-zA-Z0-9_]{0,14})\/(article|status)\/\d+`, "")
	reXaOgTitle   = jsRE(`^(?:\(\d+\)\s+)?(.+?)\s+on\s+X\s*:`, "")
	reXaLang      = jsRE(`language-(\w+)`, "")
	reXaNameParam = jsRE(`&name=\w+`, "")
)

const (
	xaReadView  = `[data-testid="twitterArticleReadView"]`
	xaContainer = `[data-testid="twitterArticleRichTextView"]`
	xaImages    = `[data-testid="tweetPhoto"] img`
)

func init() {
	dfdRegisterExtractor("xarticle", func(c dfdExtCtx) dfdExtractor {
		return &dfdXArticle{ctx: c, articleContainer: ldQuerySelector(c.doc, xaContainer)}
	})
}

func (e *dfdXArticle) name() string { return "xarticle" }

func (e *dfdXArticle) canExtract() bool { return e.articleContainer != nil }

func (e *dfdXArticle) extract() dfdExtracted {
	title := e.extractTitle()
	author := e.extractAuthor()
	contentHTML := e.extractContent()
	description := e.createDescription()
	return dfdExtracted{
		content:     contentHTML,
		contentHTML: contentHTML,
		variables:   map[string]string{"title": title, "author": author, "site": "X (Twitter)", "description": description},
	}
}

func (e *dfdXArticle) extractTitle() string {
	if t := extText(ldQuerySelector(e.ctx.doc, `[data-testid="twitter-article-title"]`)); t != "" {
		return t
	}
	return "Untitled X Article"
}

func (e *dfdXArticle) extractAuthor() string {
	container := ldQuerySelector(e.ctx.doc, `[itemprop="author"]`)
	if container == nil {
		return e.getAuthorFromURL()
	}
	name := extAttr(ldQuerySelector(container, `meta[itemprop="name"]`), "content")
	handle := extAttr(ldQuerySelector(container, `meta[itemprop="additionalName"]`), "content")
	if name != "" && handle != "" {
		return name + " (@" + handle + ")"
	}
	if name != "" {
		return name
	}
	if handle != "" {
		return handle
	}
	return e.getAuthorFromURL()
}

func (e *dfdXArticle) getAuthorFromURL() string {
	// The username before /article/ or /status/, excluding system paths like /i/.
	if m := reXaURLAuthor.FindStringSubmatch(e.ctx.url); m != nil {
		return "@" + m[1]
	}
	ogTitle := extAttr(ldQuerySelector(e.ctx.doc, `meta[property="og:title"]`), "content")
	// "(4) Heinrich on X: ..." or "Heinrich on X: ...".
	if m := reXaOgTitle.FindStringSubmatch(ogTitle); m != nil {
		return jsTrim(m[1])
	}
	return "Unknown"
}

func (e *dfdXArticle) extractContent() string {
	if e.articleContainer == nil {
		return ""
	}
	clone := e.articleContainer.cloneNode(true)
	e.cleanContent(clone)
	return `<article class="x-article">` + e.extractHeaderImage() + extSerialize(clone) + `</article>`
}

func (e *dfdXArticle) extractHeaderImage() string {
	readView := ldQuerySelector(e.ctx.doc, xaReadView)
	if readView == nil {
		return ""
	}
	photo := ldQuerySelector(readView, xaImages)
	if photo == nil || e.articleContainer.contains(photo) {
		return ""
	}
	src := photo.attr("src")
	if src == "" {
		return ""
	}
	alt := xaAlt(photo)
	return `<img src="` + dfdEscapeHTML(xaUpgradeImageSrc(src)) + `" alt="` + dfdEscapeHTML(alt) + `">`
}

// xaAlt is `img.getAttribute('alt')?.replace(/\s+/g, ' ').trim() || 'Image'`.
func xaAlt(img *ldNode) string {
	if alt := jsTrim(jsCollapseWS(img.attr("alt"))); alt != "" {
		return alt
	}
	return "Image"
}

func (e *dfdXArticle) cleanContent(container *ldNode) {
	// Complex elements first, then the other transformations.
	xaConvertEmbeddedTweets(container)
	xaConvertCodeBlocks(container)
	xaConvertHeaders(container)
	xaUnwrapLinkedImages(container)
	xaUpgradeImageQuality(container)
	// Bold spans before paragraphs, so the formatting survives.
	xaConvertBoldSpans(container)
	xaConvertDraftParagraphs(container)
	extRemoveAttr(container, "[data-offset-key]", "data-offset-key")
	// repairSurrogatePairs joins a high surrogate at the end of a text node to the low surrogate that starts the
	// next one. Text of this port's document never holds a lone surrogate (the parser turns a surrogate reference
	// into U+FFFD and a Go string cannot hold one), so there is never a pair to repair.
}

func extRemoveAttr(root *ldNode, selector, name string) {
	for _, el := range ldQuerySelectorAll(root, selector) {
		el.removeAttribute(name)
	}
}

func xaConvertEmbeddedTweets(container *ldNode) {
	for _, tweet := range ldQuerySelectorAll(container, `[data-testid="simpleTweet"]`) {
		blockquote := ldNewElement("blockquote")
		blockquote.setAttribute("class", "embedded-tweet")
		var fullName, handle string
		if userName := ldQuerySelector(tweet, `[data-testid="User-Name"]`); userName != nil {
			links := ldQuerySelectorAll(userName, "a")
			if len(links) > 0 {
				fullName = extText(links[0])
			}
			if len(links) > 1 {
				handle = extText(links[1])
			}
		}
		tweetText := extText(ldQuerySelector(tweet, `[data-testid="tweetText"]`))
		if fullName != "" || handle != "" {
			cite := ldNewElement("cite")
			if handle != "" {
				cite.setTextContent(fullName + " " + handle)
			} else {
				cite.setTextContent(fullName)
			}
			blockquote.appendChild(cite)
		}
		if tweetText != "" {
			p := ldNewElement("p")
			p.setTextContent(tweetText)
			blockquote.appendChild(p)
		}
		tweet.replaceWith(blockquote)
	}
}

func xaConvertCodeBlocks(container *ldNode) {
	for _, block := range ldQuerySelectorAll(container, `[data-testid="markdown-code-block"]`) {
		pre := ldQuerySelector(block, "pre")
		code := ldQuerySelector(block, "code")
		if pre == nil || code == nil {
			continue
		}
		// The language comes from the class ("language-bash") or from a label span in the block header.
		language := ""
		if m := reXaLang.FindStringSubmatch(code.className()); m != nil {
			language = m[1]
		} else {
			language = extText(ldQuerySelector(block, "span"))
		}
		newPre := ldNewElement("pre")
		newCode := ldNewElement("code")
		if language != "" {
			newCode.setAttribute("data-lang", language)
			newCode.setAttribute("class", "language-"+language)
		}
		newCode.setTextContent(code.textContent())
		newPre.appendChild(newCode)
		block.replaceWith(newPre)
	}
}

func xaConvertHeaders(container *ldNode) {
	for _, header := range ldQuerySelectorAll(container, "h1, h2, h3, h4, h5, h6") {
		text := extText(header)
		if text == "" {
			continue
		}
		newHeader := ldNewElement(strings.ToLower(header.tagName()))
		newHeader.setTextContent(text)
		header.replaceWith(newHeader)
	}
}

func xaUnwrapLinkedImages(container *ldNode) {
	for _, img := range ldQuerySelectorAll(container, xaImages) {
		anchor := ldClosest(img, "a")
		if anchor == nil || !container.contains(anchor) {
			continue
		}
		clean := ldNewElement("img")
		clean.setAttribute("src", xaUpgradeImageSrc(img.attr("src")))
		clean.setAttribute("alt", xaAlt(img))
		anchor.replaceWith(clean)
	}
}

func xaUpgradeImageQuality(container *ldNode) {
	for _, img := range ldQuerySelectorAll(container, xaImages) {
		if src := img.attr("src"); src != "" {
			img.setAttribute("src", xaUpgradeImageSrc(src))
		}
	}
}

func xaUpgradeImageSrc(src string) string {
	if strings.Contains(src, "&name=") {
		return xaReplaceFirstName(src)
	}
	if strings.Contains(src, "?") {
		return src + "&name=large"
	}
	return src + "?name=large"
}

// xaReplaceFirstName is src.replace(/&name=\w+/, '&name=large').
func xaReplaceFirstName(src string) string {
	loc := reXaNameParam.FindStringIndex(src)
	if loc == nil {
		return src
	}
	return src[:loc[0]] + "&name=large" + src[loc[1]:]
}

func xaConvertDraftParagraphs(container *ldNode) {
	for _, div := range ldQuerySelectorAll(container, ".longform-unstyled, .public-DraftStyleDefault-block") {
		p := ldNewElement("p")
		var process func(node *ldNode)
		process = func(node *ldNode) {
			switch node.kind {
			case ldText:
				p.appendChild(ldNewText(node.textContent()))
			case ldElem:
				switch strings.ToLower(node.tagName()) {
				case "strong":
					strong := ldNewElement("strong")
					strong.setTextContent(node.textContent())
					p.appendChild(strong)
				case "a":
					link := ldNewElement("a")
					link.setAttribute("href", node.attr("href"))
					link.setTextContent(node.textContent())
					p.appendChild(link)
				case "code":
					code := ldNewElement("code")
					code.setTextContent(node.textContent())
					p.appendChild(code)
				default:
					// Other elements (spans, divs) are walked into.
					for _, child := range node.childNodes() {
						process(child)
					}
				}
			}
		}
		for _, child := range div.childNodes() {
			process(child)
		}
		div.replaceWith(p)
	}
}

func xaConvertBoldSpans(container *ldNode) {
	for _, span := range ldQuerySelectorAll(container, `span[style*="font-weight: bold"]`) {
		strong := ldNewElement("strong")
		strong.setTextContent(span.textContent())
		span.replaceWith(strong)
	}
}

func (e *dfdXArticle) createDescription() string {
	text := ""
	if e.articleContainer != nil {
		text = jsTrim(e.articleContainer.textContent())
	}
	out := jsSlice(text, 0, 140)
	if jsLen(text) > 140 {
		out += "..."
	}
	return out
}
