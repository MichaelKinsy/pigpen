package websearch

import (
	"strings"
)

// Port of defuddle 0.19.3 dist/extractors/mastodon.js: a Mastodon status page, its self-reply thread and replies.

type dfdMastodon struct {
	ctx           dfdExtCtx
	mainPost      *ldNode
	replyStatuses []*ldNode
}

var reMastoAt = jsRE(`^@`, "")

func init() {
	dfdRegisterExtractor("mastodon", func(c dfdExtCtx) dfdExtractor {
		e := &dfdMastodon{ctx: c}
		e.mainPost = ldQuerySelector(c.doc, ".detailed-status__wrapper")
		for _, w := range ldQuerySelectorAll(c.doc, ".status__wrapper") {
			if ldQuerySelector(w, ".status[data-id]") != nil {
				e.replyStatuses = append(e.replyStatuses, w)
			}
		}
		return e
	})
}

func (e *dfdMastodon) name() string { return "mastodon" }

func (e *dfdMastodon) canExtract() bool {
	if e.mainPost == nil {
		return false
	}
	if e.ctx.doc.getElementByID("mastodon") != nil {
		return true
	}
	if initialState := ldQuerySelector(e.ctx.doc, "script#initial-state"); initialState != nil {
		text := initialState.textContent()
		if strings.Contains(text, "mastodon/mastodon") || strings.Contains(text, `"mastodon"`) {
			return true
		}
	}
	for _, link := range ldQuerySelectorAll(e.ctx.doc, `link[rel="stylesheet"]`) {
		if strings.Contains(link.attr("href"), "mastodon") {
			return true
		}
	}
	return false
}

func mastoHandleOf(full string) string { return strings.SplitN(full, "@", 2)[0] }

func (e *dfdMastodon) extract() dfdExtracted {
	mainFullHandle := e.getFullHandle(e.mainPost)
	mainHandle := mastoHandleOf(mainFullHandle)
	displayName := e.getDisplayName(e.mainPost)

	// Consecutive self-replies at the start form the thread.
	var threadItems, replyItems []*ldNode
	threadEnded := false
	for _, status := range e.replyStatuses {
		if handle := mastoHandleOf(e.getFullHandle(status)); !threadEnded && handle == mainHandle {
			threadItems = append(threadItems, status)
		} else {
			threadEnded = true
			replyItems = append(replyItems, status)
		}
	}

	var allParts []string
	if mainContent := e.extractPostContent(e.mainPost); mainContent != "" {
		allParts = append(allParts, mainContent)
	}
	for _, item := range threadItems {
		if part := e.extractPostContent(item); part != "" {
			allParts = append(allParts, part)
		}
	}
	postContent := strings.Join(allParts, "\n<hr>\n")
	comments := e.extractComments(replyItems)
	contentHTML := dfdBuildContentHTML("mastodon", postContent, comments)
	author := displayName
	if author == "" {
		author = "@" + mainFullHandle
	}
	description := e.getDescription()
	published := e.getPublishedDate()
	siteName := extAttr(ldQuerySelector(e.ctx.doc, `meta[property="og:site_name"]`), "content")
	site := siteName
	if site == "" {
		site = "Mastodon"
	}
	vars := map[string]string{
		"title":       "Post by " + author + " on " + site,
		"author":      author,
		"site":        site,
		"description": description,
	}
	if published != "" {
		vars["published"] = published
	}
	return dfdExtracted{content: contentHTML, contentHTML: contentHTML, variables: vars}
}

func (e *dfdMastodon) getFullHandle(container *ldNode) string {
	text := extText(ldQuerySelector(container, ".display-name__account"))
	return reMastoAt.ReplaceAllLiteralString(text, "")
}

func (e *dfdMastodon) getDisplayName(container *ldNode) string {
	name := ldQuerySelector(container, ".display-name__html")
	if name == nil {
		return ""
	}
	clone := name.cloneNode(true)
	e.replaceEmojiImages(clone)
	return jsTrim(clone.textContent())
}

// mastoISODate is `new Date(s).toISOString().split('T')[0]` inside a try that falls through to ”.
func mastoISODate(s string) string {
	ms := jsDateFromString(s)
	if jsIsNaN(ms) {
		return ""
	}
	return jsISODate(ms)
}

func (e *dfdMastodon) getReplyDate(wrapper *ldNode) string {
	t := ldQuerySelector(wrapper, "time[datetime]")
	if t == nil {
		return ""
	}
	return mastoISODate(t.attr("datetime"))
}

func (e *dfdMastodon) getReplyPermalink(wrapper *ldNode) string {
	link := ldQuerySelector(wrapper, "a.status__relative-time[href]")
	if link == nil {
		return ""
	}
	href := link.attr("href")
	if href == "" {
		return ""
	}
	base, ok := newURL(e.ctx.url, "")
	if !ok {
		return href
	}
	if strings.HasPrefix(href, "http") {
		return href
	}
	return base.origin() + href
}

func (e *dfdMastodon) getPublishedDate() string {
	if meta := ldQuerySelector(e.ctx.doc, `meta[property="og:published_time"]`); meta != nil {
		if d := mastoISODate(meta.attr("content")); d != "" {
			return d
		}
	}
	if e.mainPost != nil {
		if t := ldQuerySelector(e.mainPost, "time[datetime]"); t != nil {
			if d := mastoISODate(t.attr("datetime")); d != "" {
				return d
			}
		}
	}
	return ""
}

func (e *dfdMastodon) getDescription() string {
	if e.mainPost == nil {
		return ""
	}
	textEl := ldQuerySelector(e.mainPost, ".status__content__text")
	if textEl == nil {
		return ""
	}
	return jsCollapseWS(jsSlice(jsTrim(textEl.textContent()), 0, 140))
}

func (e *dfdMastodon) extractPostContent(container *ldNode) string {
	var parts []string
	if text := e.extractTextContent(ldQuerySelector(container, ".status__content")); text != "" {
		parts = append(parts, text)
	}
	if images := e.extractImages(container); images != "" {
		parts = append(parts, images)
	}
	if card := e.extractLinkCard(container); card != "" {
		parts = append(parts, card)
	}
	return strings.Join(parts, "\n")
}

func (e *dfdMastodon) extractTextContent(contentEl *ldNode) string {
	if contentEl == nil {
		return ""
	}
	textEl := ldQuerySelector(contentEl, ".status__content__text")
	if textEl == nil {
		return ""
	}
	clone := textEl.cloneNode(true)
	e.replaceEmojiImages(clone)
	// Mastodon hides parts of displayed URLs in invisible spans.
	extRemoveAll(clone, "span.invisible")
	// The remaining spans (ellipsis, mention wrappers) are unwrapped.
	for _, el := range ldQuerySelectorAll(clone, "span") {
		el.replaceWith(el.childNodes()...)
	}
	out := ldInnerHTML(clone)
	if out == "" {
		out = clone.textContent()
	}
	return jsTrim(out)
}

// replaceEmojiImages replaces emoji <img> tags with their alt text (Unicode emoji or :shortcode:).
func (e *dfdMastodon) replaceEmojiImages(container *ldNode) {
	for _, img := range ldQuerySelectorAll(container, "img.emojione") {
		if alt := img.attr("alt"); alt != "" {
			img.replaceWith(ldNewText(alt))
		} else {
			img.remove()
		}
	}
}

func (e *dfdMastodon) extractImages(container *ldNode) string {
	gallery := ldQuerySelector(container, ".media-gallery")
	if gallery == nil {
		return ""
	}
	var images []string
	for _, link := range ldQuerySelectorAll(gallery, ".media-gallery__item-thumbnail") {
		href := link.attr("href")
		alt := extAttr(ldQuerySelector(link, "img"), "alt")
		if href != "" {
			images = append(images, `<img src="`+dfdEscapeHTML(href)+`" alt="`+dfdEscapeHTML(alt)+`" />`)
		}
	}
	return strings.Join(images, "\n")
}

func (e *dfdMastodon) extractLinkCard(container *ldNode) string {
	card := ldQuerySelector(container, "a.status-card[href]")
	if card == nil {
		return ""
	}
	href := card.attr("href")
	title := extText(ldQuerySelector(card, ".status-card__title"))
	description := extText(ldQuerySelector(card, ".status-card__description"))
	img := ldQuerySelector(card, ".status-card__image-image")
	if title == "" && href == "" {
		return ""
	}
	html := ""
	if img != nil {
		if src := img.attr("src"); src != "" {
			html += `<a href="` + dfdEscapeHTML(href) + `"><img src="` + dfdEscapeHTML(src) + `" alt="` + dfdEscapeHTML(title) + `" /></a>` + "\n"
		}
	}
	shown := title
	if shown == "" {
		shown = href
	}
	html += `<p><a href="` + dfdEscapeHTML(href) + `">` + dfdEscapeHTML(shown) + `</a></p>`
	if description != "" {
		html += "\n<p>" + dfdEscapeHTML(description) + "</p>"
	}
	return html
}

func (e *dfdMastodon) extractComments(replyItems []*ldNode) string {
	if len(replyItems) == 0 {
		return ""
	}
	currentDepth := 0.0
	data := make([]dfdComment, len(replyItems))
	for index, wrapper := range replyItems {
		handle := e.getFullHandle(wrapper)
		displayName := e.getDisplayName(wrapper)
		content := e.extractPostContent(wrapper)
		date := e.getReplyDate(wrapper)
		permalink := e.getReplyPermalink(wrapper)
		// status--first-in-thread starts a top-level reply (depth 0); any other reply goes one level deeper.
		if ldQuerySelector(wrapper, ".status--first-in-thread") != nil || index == 0 {
			currentDepth = 0
		} else {
			currentDepth++
		}
		author := "@" + handle
		if displayName != "" {
			author = displayName + " @" + handle
		}
		data[index] = dfdComment{author: author, date: date, content: content, depth: currentDepth, url: permalink}
	}
	return dfdBuildCommentTree(data)
}
