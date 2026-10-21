package websearch

import (
	"strings"
)

// Port of defuddle 0.19.3 dist/extractors/bluesky.js: a Bluesky post thread (the author's own consecutive posts are
// the thread, everything after is a reply).

type dfdBluesky struct {
	ctx       dfdExtCtx
	postItems []*ldNode
}

var (
	reBskyTestID  = jsRE(`^postThreadItem-by-(.+)$`, "")
	reBskyAvatar  = jsRE(`^(.+)'s avatar$`, "")
	reBskyBidi    = jsRE(`[\u200E\u200F\u200B]`, "")
	reBskyNewline = jsRE(`\n+`, "")
)

func init() {
	dfdRegisterExtractor("bluesky", func(c dfdExtCtx) dfdExtractor {
		e := &dfdBluesky{ctx: c}
		if screen := ldQuerySelector(c.doc, `[data-testid="postThreadScreen"]`); screen != nil {
			e.postItems = ldQuerySelectorAll(screen, `[data-testid^="postThreadItem-by-"]`)
		}
		return e
	})
}

func (e *dfdBluesky) name() string { return "bluesky" }

func (e *dfdBluesky) canExtract() bool { return len(e.postItems) > 0 }

func (e *dfdBluesky) extract() dfdExtracted {
	mainHandle := e.getHandle(e.postItems[0])
	var threadItems, replyItems []*ldNode
	threadEnded := false
	for _, item := range e.postItems {
		if handle := e.getHandle(item); !threadEnded && handle == mainHandle {
			threadItems = append(threadItems, item)
		} else {
			threadEnded = true
			replyItems = append(replyItems, item)
		}
	}
	postParts := make([]string, len(threadItems))
	for i, item := range threadItems {
		postParts[i] = e.extractPostContent(item)
	}
	postContent := strings.Join(postParts, "\n<hr>\n")
	comments := e.extractComments(replyItems)
	contentHTML := dfdBuildContentHTML("bluesky", postContent, comments)
	author := "@" + mainHandle
	displayName := e.getDisplayName(e.postItems[0])
	description := e.createDescription(e.postItems[0])
	published := e.getPublishedDate()
	shown := displayName
	if shown == "" {
		shown = author
	}
	vars := map[string]string{
		"title":       "Post by " + shown + " on Bluesky",
		"author":      shown,
		"site":        "Bluesky",
		"description": description,
	}
	if published != "" {
		vars["published"] = published
	}
	return dfdExtracted{content: contentHTML, contentHTML: contentHTML, variables: vars}
}

func (e *dfdBluesky) extractComments(replyItems []*ldNode) string {
	if len(replyItems) == 0 {
		return ""
	}
	currentDepth := 0.0
	data := make([]dfdComment, len(replyItems))
	for i, item := range replyItems {
		handle := e.getHandle(item)
		displayName := e.getDisplayName(item)
		content := e.extractPostContent(item)
		date := e.getReplyDate(item)
		permalink := e.getPermalink(item)
		// A thread connector line in the top spacer area marks a reply to the post above (one level deeper);
		// without one the post is a new top-level reply.
		if e.hasTopConnector(item) {
			currentDepth++
		} else {
			currentDepth = 0
		}
		author := "@" + handle
		if displayName != "" {
			author = displayName + " @" + handle
		}
		data[i] = dfdComment{author: author, date: date, content: content, depth: currentDepth, url: permalink}
	}
	return dfdBuildCommentTree(data)
}

// hasTopConnector: the first child of a reply is the connector area; a 2px-wide div with a background-color in it
// is a thread line to the post above.
func (e *dfdBluesky) hasTopConnector(item *ldNode) bool {
	kids := item.children()
	if len(kids) == 0 {
		return false
	}
	for _, div := range ldQuerySelectorAll(kids[0], "div") {
		style := div.attr("style")
		if strings.Contains(style, "width: 2px") && strings.Contains(style, "background-color") {
			return true
		}
	}
	return false
}

func (e *dfdBluesky) getHandle(item *ldNode) string {
	if m := reBskyTestID.FindStringSubmatch(item.attr("data-testid")); m != nil {
		return m[1]
	}
	return ""
}

func (e *dfdBluesky) getDisplayName(item *ldNode) string {
	// A root post: the avatar link has aria-label="{name}'s avatar".
	if avatarLink := ldQuerySelector(item, `a[aria-label*="avatar"]`); avatarLink != nil {
		if m := reBskyAvatar.FindStringSubmatch(avatarLink.attr("aria-label")); m != nil {
			return m[1]
		}
	}
	// A reply: the first link to a profile with text.
	for _, link := range ldQuerySelectorAll(item, `a[href^="/profile/"]`) {
		text := extText(link)
		if text != "" && !strings.HasPrefix(text, "@") && !strings.Contains(text, "avatar") && !strings.Contains(text, "·") {
			return text
		}
	}
	return ""
}

func (e *dfdBluesky) getPublishedDate() string {
	// twitter:value1 is the ISO date of the root post.
	if meta := ldQuerySelector(e.ctx.doc, `meta[name="twitter:value1"]`); meta != nil {
		return mastoISODate(meta.attr("content"))
	}
	return ""
}

func (e *dfdBluesky) getReplyDate(item *ldNode) string {
	timeLink := ldQuerySelector(item, `a[href*="/post/"]`)
	if timeLink == nil {
		return ""
	}
	// aria-label is "March 27, 2026 at 8:35 AM"; Date parses it without " at ".
	label := timeLink.attr("aria-label")
	if label == "" {
		return ""
	}
	return mastoISODate(strings.Replace(label, " at ", " ", 1))
}

func (e *dfdBluesky) getPermalink(item *ldNode) string {
	link := ldQuerySelector(item, `a[href*="/post/"]`)
	if link == nil {
		return ""
	}
	href := link.attr("href")
	if strings.HasPrefix(href, "http") {
		return href
	}
	return "https://bsky.app" + href
}

func (e *dfdBluesky) extractPostContent(item *ldNode) string {
	var parts []string
	if textDiv := ldQuerySelector(item, `div[data-word-wrap="1"]`); textDiv != nil {
		if text := e.cleanText(textDiv); text != "" {
			parts = append(parts, text)
		}
	}
	if images := e.extractImages(item); images != "" {
		parts = append(parts, images)
	}
	if card := e.extractLinkCard(item); card != "" {
		parts = append(parts, card)
	}
	if quoted := e.extractQuotedPost(item); quoted != "" {
		parts = append(parts, quoted)
	}
	return strings.Join(parts, "\n")
}

func (e *dfdBluesky) cleanText(textDiv *ldNode) string {
	clone := textDiv.cloneNode(true)
	// Mention links: DID-based hrefs become readable URLs.
	for _, link := range ldQuerySelectorAll(clone, `a[href*="/profile/"]`) {
		text := extText(link)
		href := link.attr("href")
		if strings.HasPrefix(text, "@") {
			clean := ldNewElement("a")
			clean.setAttribute("href", "https://bsky.app/profile/"+jsSlice(text, 1, jsLen(text)))
			clean.setTextContent(text)
			link.replaceWith(clean)
		} else if strings.HasPrefix(href, "/profile/") {
			link.setAttribute("href", "https://bsky.app"+href)
		}
	}
	// External links: absolute URLs, without the obfuscated classes.
	for _, link := range ldQuerySelectorAll(clone, `a[href^="http"]`) {
		clean := ldNewElement("a")
		clean.setAttribute("href", link.attr("href"))
		clean.setTextContent(extText(link))
		link.replaceWith(clean)
	}
	for _, el := range ldQuerySelectorAll(clone, "span, div") {
		el.replaceWith(el.childNodes()...)
	}
	html := ldInnerHTML(clone)
	if html == "" {
		html = clone.textContent()
	}
	// The bidi markers Bluesky adds around handles go; whitespace other than newlines collapses.
	html = reBskyBidi.ReplaceAllLiteralString(jsTrim(html), "")
	html = jsTrim(bskyCollapseNonNewlineWS(html))
	if html == "" {
		return ""
	}
	var paras []string
	for _, p := range reBskyNewline.Split(html, -1) {
		if p = jsTrim(p); p != "" {
			paras = append(paras, "<p>"+p+"</p>")
		}
	}
	return strings.Join(paras, "\n")
}

// bskyCollapseNonNewlineWS is .replace(/[^\S\n]+/g, ' ').
func bskyCollapseNonNewlineWS(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if r != '\n' && jsSpace(r) {
			if !in {
				b.WriteByte(' ')
			}
			in = true
			continue
		}
		in = false
		b.WriteRune(r)
	}
	return b.String()
}

func (e *dfdBluesky) extractImages(item *ldNode) string {
	var images []string
	// Content images use the feed_thumbnail and feed_fullsize CDN paths; avatars (avatar_thumbnail) are skipped.
	for _, img := range ldQuerySelectorAll(item, `img[src*="/feed_thumbnail/"], img[src*="/feed_fullsize/"]`) {
		src := img.attr("src")
		if src == "" {
			continue
		}
		full := strings.Replace(src, "/feed_thumbnail/", "/feed_fullsize/", 1)
		images = append(images, `<img src="`+dfdEscapeHTML(full)+`" alt="" />`)
	}
	return strings.Join(images, "\n")
}

func (e *dfdBluesky) extractLinkCard(item *ldNode) string {
	// A link card is an <a> with an aria-label (the title) around a bordered container with image, title, domain.
	for _, link := range ldQuerySelectorAll(item, `a[aria-label][href^="http"]`) {
		if ldQuerySelector(link, `div[style*="border"]`) == nil {
			continue
		}
		href := link.attr("href")
		title := link.attr("aria-label")
		img := ldQuerySelector(link, "img")
		if title != "" {
			html := ""
			if img != nil {
				html += `<a href="` + dfdEscapeHTML(href) + `"><img src="` + dfdEscapeHTML(img.attr("src")) + `" alt="` + dfdEscapeHTML(title) + `" /></a>` + "\n"
			}
			return html + `<p><a href="` + dfdEscapeHTML(href) + `">` + dfdEscapeHTML(title) + `</a></p>`
		}
	}
	return ""
}

func (e *dfdBluesky) extractQuotedPost(item *ldNode) string {
	// A quoted post is an embedded postThreadItem.
	for _, embed := range ldQuerySelectorAll(item, `[data-testid^="postThreadItem-by-"]`) {
		if embed == item {
			continue
		}
		handle := e.getHandle(embed)
		displayName := e.getDisplayName(embed)
		text := ""
		if textDiv := ldQuerySelector(embed, `div[data-word-wrap="1"]`); textDiv != nil {
			text = e.cleanText(textDiv)
		}
		author := "@" + handle
		if displayName != "" {
			author = displayName + " @" + handle
		}
		return dfdBuildQuotedPost(dfdQuotedPost{author: author, content: text})
	}
	return ""
}

func (e *dfdBluesky) createDescription(item *ldNode) string {
	textDiv := ldQuerySelector(item, `div[data-word-wrap="1"]`)
	if textDiv == nil {
		return ""
	}
	text := jsTrim(reBskyBidi.ReplaceAllLiteralString(textDiv.textContent(), ""))
	return jsCollapseWS(jsSlice(text, 0, 140))
}
