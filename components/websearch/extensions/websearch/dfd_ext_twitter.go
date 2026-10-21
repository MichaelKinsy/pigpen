package websearch

import (
	"strings"
)

// Port of defuddle 0.19.3 dist/extractors/twitter.js: a rendered X or Twitter conversation (main post, the author's
// own thread, replies).

type dfdTwitter struct {
	ctx         dfdExtCtx
	mainTweet   *ldNode
	threadTw    []*ldNode
	replyTw     []*ldNode
	replyDepths []float64
}

var (
	reTwHandle   = jsRE(`^@\w{1,15}$`, "")
	reTwHrefName = jsRE(`^(?:https?:\/\/[^/]+)?\/(\w{1,15})(?:\/|$)`, "")
	reTwAnyAt    = jsRE(`@(\w{1,15})`, "")
	reTwName     = jsRE(`&name=\w+$`, "")
)

var twReservedPaths = map[string]bool{
	"i": true, "home": true, "explore": true, "search": true, "notifications": true, "messages": true,
	"settings": true, "compose": true, "hashtag": true, "intent": true,
}

const twTweet = `article[data-testid="tweet"]`

func init() {
	dfdRegisterExtractor("twitter", func(c dfdExtCtx) dfdExtractor {
		e := &dfdTwitter{ctx: c}
		e.classifyCells(e.conversationCells())
		// A markup change must never yield nothing: a bare post beats the generic extractor.
		if e.mainTweet == nil {
			e.mainTweet = ldQuerySelector(c.doc, twTweet)
		}
		return e
	})
}

func (e *dfdTwitter) name() string { return "twitter" }

func (e *dfdTwitter) canExtract() bool { return e.mainTweet != nil }

// conversationCells are the cells in document order, stopping at X's "Discover more" and similar trailing sections.
// Anything before the first cell is a header, not a boundary.
func (e *dfdTwitter) conversationCells() []*ldNode {
	var cells []*ldNode
	for _, el := range ldQuerySelectorAll(e.ctx.doc, `[data-testid="cellInnerDiv"], section, h2`) {
		if el.attr("data-testid") == "cellInnerDiv" {
			cells = append(cells, el)
		} else if len(cells) > 0 && ldClosest(el, twTweet) == nil {
			// A heading inside a tweet belongs to that tweet, not to a boundary.
			break
		}
	}
	return cells
}

// classifyCells: the main tweet (first article), the thread (consecutive self-replies by the main author before any
// reply by someone else) and the replies (everything after).
func (e *dfdTwitter) classifyCells(cells []*ldNode) {
	mainHandle := ""
	threadEnded := false
	lastWasTweet := false
	currentDepth := 0.0
	for _, cell := range cells {
		article := ldQuerySelector(cell, twTweet)
		if article == nil {
			lastWasTweet = false
			continue
		}
		if e.mainTweet == nil {
			e.mainTweet = article
			mainHandle = e.getHandle(article)
			lastWasTweet = true
			continue
		}
		handle := e.getHandle(article)
		if !threadEnded && handle != "" && handle == mainHandle {
			e.threadTw = append(e.threadTw, article)
			lastWasTweet = true
			continue
		}
		// The first tweet by a different person ends the thread.
		threadEnded = true
		if lastWasTweet {
			currentDepth++
		} else {
			currentDepth = 0
		}
		e.replyTw = append(e.replyTw, article)
		e.replyDepths = append(e.replyDepths, currentDepth)
		lastWasTweet = true
	}
}

func (e *dfdTwitter) extract() dfdExtracted {
	parts := []string{e.extractTweetContent(e.mainTweet)}
	for _, tweet := range e.threadTw {
		parts = append(parts, e.extractTweetContent(tweet))
	}
	postContent := strings.Join(parts, "\n<hr>\n")
	comments := e.extractComments()
	contentHTML := dfdBuildContentHTML("twitter", postContent, comments)
	tweetAuthor := e.getTweetAuthor()
	description := e.createDescription(e.mainTweet)
	return dfdExtracted{
		content:     contentHTML,
		contentHTML: contentHTML,
		variables: map[string]string{
			"title":       "Post by " + tweetAuthor + " on X",
			"author":      tweetAuthor,
			"site":        "X (Twitter)",
			"description": description,
		},
	}
}

func (e *dfdTwitter) extractComments() string {
	if len(e.replyTw) == 0 {
		return ""
	}
	data := make([]dfdComment, len(e.replyTw))
	for i, tweet := range e.replyTw {
		info := e.extractUserInfo(tweet)
		content := e.extractTweetContent(tweet)
		author := info.handle
		if info.fullName != "" {
			author = info.fullName + " " + info.handle
		}
		data[i] = dfdComment{author: author, date: info.date, content: content, depth: e.replyDepths[i], url: info.permalink}
	}
	return dfdBuildCommentTree(data)
}

// getHandle is the @handle of a tweet: link text starting with "@", then the profile path of a link href, then any
// "@name" in the name block's text (quoted tweets render the handle as plain text).
func (e *dfdTwitter) getHandle(tweet *ldNode) string {
	nameElement := ldQuerySelector(tweet, `[data-testid="User-Name"]`)
	if nameElement == nil {
		return ""
	}
	links := ldQuerySelectorAll(nameElement, "a")
	for _, link := range links {
		if text := extText(link); reTwHandle.MatchString(text) {
			return text
		}
	}
	for _, link := range links {
		if m := reTwHrefName.FindStringSubmatch(link.attr("href")); m != nil && !twReservedPaths[jsToLower(m[1])] {
			return "@" + m[1]
		}
	}
	if m := reTwAnyAt.FindStringSubmatch(nameElement.textContent()); m != nil {
		return "@" + m[1]
	}
	return ""
}

func (e *dfdTwitter) formatTweetText(text string) string {
	if text == "" {
		return ""
	}
	temp := ldNewElement("div")
	temp.appendChild(ldParseFragment(text))
	// Links become their text (the @handles).
	for _, link := range ldQuerySelectorAll(temp, "a") {
		link.replaceWith(ldNewText(extText(link)))
	}
	for _, el := range ldQuerySelectorAll(temp, "span, div") {
		el.replaceWith(el.childNodes()...)
	}
	var paras []string
	for _, line := range strings.Split(extSerialize(temp), "\n") {
		if line = jsTrim(line); line != "" {
			paras = append(paras, "<p>"+line+"</p>")
		}
	}
	return strings.Join(paras, "\n")
}

func (e *dfdTwitter) replaceEmojiImages(container *ldNode) {
	for _, img := range ldQuerySelectorAll(container, `img[src*="/emoji/"]`) {
		if alt := img.attr("alt"); alt != "" {
			img.replaceWith(ldNewText(alt))
		}
	}
}

func (e *dfdTwitter) findQuotedTweet(tweet *ldNode) *ldNode {
	const sel = `[aria-labelledby*="id__"]`
	outer := ldQuerySelector(tweet, sel)
	if outer == nil {
		return nil
	}
	name := ldQuerySelector(outer, `[data-testid="User-Name"]`)
	if name == nil {
		return nil
	}
	return ldClosest(name, sel)
}

func (e *dfdTwitter) extractTweetContent(tweet *ldNode) string {
	if tweet == nil {
		return ""
	}
	clone := tweet.cloneNode(true)
	e.replaceEmojiImages(clone)
	tweetText := ""
	if el := ldQuerySelector(clone, `[data-testid="tweetText"]`); el != nil {
		tweetText = extSerialize(el)
	}
	formatted := e.formatTweetText(tweetText)
	quoted := e.findQuotedTweet(tweet)
	images := e.extractImages(tweet, quoted)
	quotedHTML := ""
	if quoted != nil {
		quotedHTML = e.extractQuotedTweet(quoted)
	}
	cardLink := e.extractCard(tweet)
	html := ""
	if formatted != "" {
		html += formatted
	}
	if len(images) > 0 {
		html += "\n" + strings.Join(images, "\n")
	}
	if cardLink != "" {
		html += "\n" + cardLink
	}
	if quotedHTML != "" {
		html += "\n" + quotedHTML
	}
	return html
}

func (e *dfdTwitter) extractQuotedTweet(quoted *ldNode) string {
	clone := quoted.cloneNode(true)
	e.replaceEmojiImages(clone)
	tweetText := ""
	if el := ldQuerySelector(clone, `[data-testid="tweetText"]`); el != nil {
		tweetText = extSerialize(el)
	}
	formatted := e.formatTweetText(tweetText)
	info := e.extractUserInfo(quoted)
	images := e.extractImages(quoted, nil)
	content := ""
	if formatted != "" {
		content += formatted
	}
	if len(images) > 0 {
		content += "\n" + strings.Join(images, "\n")
	}
	author := info.handle
	if info.fullName != "" {
		author = info.fullName + " " + info.handle
	}
	return dfdBuildQuotedPost(dfdQuotedPost{author: author, date: info.date, content: content})
}

type twUserInfo struct{ fullName, handle, date, permalink string }

func (e *dfdTwitter) extractUserInfo(tweet *ldNode) twUserInfo {
	nameElement := ldQuerySelector(tweet, `[data-testid="User-Name"]`)
	if nameElement == nil {
		return twUserInfo{}
	}
	handle := e.getHandle(tweet)
	// The display name is the first link that is neither the handle nor the timestamp. Quoted tweets render no
	// links, so the first child ([0] the display name, [1] "@handle·date") is the fallback.
	fullName := ""
	for _, link := range ldQuerySelectorAll(nameElement, "a") {
		text := ""
		if ldQuerySelector(link, "time") == nil {
			text = extText(link)
		}
		if text != "" && text != handle && !strings.HasPrefix(text, "@") {
			fullName = text
			break
		}
	}
	if fullName == "" {
		first := ""
		if kids := nameElement.children(); len(kids) > 0 {
			first = extText(kids[0])
		}
		if first != "" && !strings.HasPrefix(first, "@") {
			fullName = first
		}
	}
	timestamp := ldQuerySelector(tweet, "time")
	datetime := extAttr(timestamp, "datetime")
	date := ""
	if datetime != "" {
		date = jsISODate(jsDateFromString(datetime))
	}
	permalink := ""
	if timestamp != nil {
		if a := ldClosest(timestamp, "a"); a != nil {
			permalink = extAnchorHref(a)
		}
	}
	return twUserInfo{fullName: fullName, handle: handle, date: date, permalink: permalink}
}

func (e *dfdTwitter) extractImages(tweet, quoted *ldNode) []string {
	var images []string
	for _, selector := range []string{`[data-testid="tweetPhoto"]`, `[data-testid="tweet-image"]`, `img[src*="media"]`} {
		for _, img := range ldQuerySelectorAll(tweet, selector) {
			if quoted != nil && quoted.contains(img) {
				continue
			}
			if img.is("img") && img.attr("alt") != "" {
				src := reTwName.ReplaceAllLiteralString(img.attr("src"), "&name=large")
				alt := jsTrim(jsCollapseWS(img.attr("alt")))
				images = append(images, `<img src="`+dfdEscapeHTML(src)+`" alt="`+dfdEscapeHTML(alt)+`" />`)
			}
		}
	}
	return images
}

func (e *dfdTwitter) extractCard(tweet *ldNode) string {
	card := ldQuerySelector(tweet, `[data-testid="card.wrapper"]`)
	if card == nil {
		return ""
	}
	link := ldQuerySelector(card, "a[href]")
	if link == nil {
		return ""
	}
	href := link.attr("href")
	title := jsTrim(strings.SplitN(link.attr("aria-label"), "\n", 2)[0])
	if title == "" {
		title = href
	}
	return `<p><a href="` + dfdEscapeHTML(href) + `">` + dfdEscapeHTML(title) + `</a></p>`
}

func (e *dfdTwitter) getTweetAuthor() string {
	handle := e.getHandle(e.mainTweet)
	if strings.HasPrefix(handle, "@") {
		return handle
	}
	return "@" + handle
}

func (e *dfdTwitter) createDescription(tweet *ldNode) string {
	if tweet == nil {
		return ""
	}
	text := extRawText(ldQuerySelector(tweet, `[data-testid="tweetText"]`))
	return jsCollapseWS(jsSlice(jsTrim(text), 0, 140))
}
