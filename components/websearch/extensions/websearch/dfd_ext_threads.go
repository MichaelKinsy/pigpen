package websearch

import (
	"strings"
)

// Port of defuddle 0.19.3 dist/extractors/threads.js: a Threads post page, either rendered (pagelets) or
// server-rendered (a region, with the replies in hydration JSON).

type dfdThreads struct {
	ctx             dfdExtCtx
	pagelets        []*ldNode
	regionContainer *ldNode
}

type thPost struct {
	username, date, permalink, content string
	element                            *ldNode
}

var (
	reThDate      = jsRE(`^\d{2}\/\d{2}\/\d{2}$`, "")
	reThPostPath  = jsRE(`^@?\w+\/post\/\w+$`, "")
	reThNumber    = jsRE(`\s*\d+\s*\/\s*\d+\s*$`, "")
	reThPermalink = jsRE(`\/@[\w.]+\/post\/`, "")
	reThFraction  = jsRE(`^\d+\/\d+$`, "")
	reThUser      = jsRE(`\/@([^/]+)`, "")
	reThUserPost  = jsRE(`\/@([^/]+)\/post\/`, "")
	reThComment   = jsRE(`<!--.*?-->`, "")
)

func init() {
	dfdRegisterExtractor("threads", func(c dfdExtCtx) dfdExtractor {
		e := &dfdThreads{ctx: c}
		for _, p := range ldQuerySelectorAll(c.doc, `[data-pagelet^="threads_post_page_"]`) {
			if ldQuerySelector(p, `a[href^="/@"], time[datetime]`) != nil {
				e.pagelets = append(e.pagelets, p)
			}
		}
		// Server-rendered HTML has no pagelets but a region with /@username links.
		if len(e.pagelets) == 0 {
			if region := ldQuerySelector(c.doc, `div[role="region"]`); region != nil && ldQuerySelector(region, `a[href^="/@"]`) != nil {
				e.regionContainer = region
			}
		}
		return e
	})
}

func (e *dfdThreads) name() string { return "threads" }

func (e *dfdThreads) canExtract() bool { return len(e.pagelets) > 0 || e.regionContainer != nil }

func (e *dfdThreads) extract() dfdExtracted {
	if len(e.pagelets) == 0 && e.regionContainer != nil {
		return e.extractFromRegion(e.regionContainer)
	}
	mainAuthor := e.getUsername(e.pagelets[0])
	// Pagelets are the thread (posts by the main author) and replies; each is parsed once.
	var threadPosts []thPost
	var replyPosts [][]thPost
	threadEnded := false
	for _, pagelet := range e.pagelets {
		posts := e.getPostsFromPagelet(pagelet)
		if len(posts) == 0 {
			continue
		}
		if !threadEnded && posts[0].username == mainAuthor && len(posts) == 1 {
			threadPosts = append(threadPosts, posts[0])
		} else {
			threadEnded = true
			replyPosts = append(replyPosts, posts)
		}
	}
	contents := make([]string, len(threadPosts))
	for i, p := range threadPosts {
		contents[i] = p.content
	}
	postContent := strings.Join(contents, "\n<hr>\n")
	comments := e.extractComments(replyPosts)
	contentHTML := dfdBuildContentHTML("threads", postContent, comments)
	author := "@" + mainAuthor
	var first *ldNode
	published := ""
	if len(threadPosts) > 0 {
		first = threadPosts[0].element
		published = threadPosts[0].date
	}
	vars := map[string]string{
		"title":       "Post by " + author + " on Threads",
		"author":      author,
		"site":        "Threads",
		"description": e.createDescription(first),
	}
	if published != "" {
		vars["published"] = published
	}
	return dfdExtracted{content: contentHTML, contentHTML: contentHTML, variables: vars}
}

// extractFromRegion reads server-rendered HTML where the post is inside a div[role="region"] without pagelets.
func (e *dfdThreads) extractFromRegion(region *ldNode) dfdExtracted {
	mainAuthor := e.getUsername(region)
	if mainAuthor == "" {
		return dfdExtracted{}
	}
	author := "@" + mainAuthor
	postContent := e.extractPostContent(region)
	comments := e.extractCommentsFromJSON(mainAuthor)
	contentHTML := dfdBuildContentHTML("threads", postContent, comments)
	description := e.createDescription(region)
	date := e.getDate(region)
	vars := map[string]string{
		"title":       "Post by " + author + " on Threads",
		"author":      author,
		"site":        "Threads",
		"description": description,
	}
	if date != "" {
		vars["published"] = date
	}
	return dfdExtracted{content: contentHTML, contentHTML: contentHTML, variables: vars}
}

type thJSONPost struct{ username, text string }

// extractCommentsFromJSON reads the replies from the React hydration JSON scripts, which can spread them over
// several scripts.
func (e *dfdThreads) extractCommentsFromJSON(mainAuthor string) string {
	var allPosts []thJSONPost
	seen := map[string]bool{}
	for _, script := range ldQuerySelectorAll(e.ctx.doc, `script[type="application/json"]`) {
		raw := script.textContent()
		if strings.Count(raw, `"text_fragments"`) < 2 {
			continue
		}
		if !strings.Contains(raw, `"username"`) {
			continue
		}
		var posts []thJSONPost
		// An unparseable script, or one whose data throws while it is read, is skipped.
		if th := ldTry(func() {
			data, err := jsonParse(raw)
			if err != nil {
				ldThrow("JSON")
			}
			posts = e.findPostsInJSON(data, 0, nil)
		}); th != nil {
			continue
		}
		for _, post := range posts {
			// Deduplicate by text content.
			key := post.username + ":" + jsSlice(post.text, 0, 80)
			if seen[key] {
				continue
			}
			seen[key] = true
			allPosts = append(allPosts, post)
		}
	}
	if len(allPosts) < 2 {
		return ""
	}
	// The first entry by the main author is the post itself.
	var data []dfdComment
	isFirstByMainAuthor := true
	for _, post := range allPosts {
		if isFirstByMainAuthor && post.username == mainAuthor {
			isFirstByMainAuthor = false
			continue
		}
		data = append(data, dfdComment{author: "@" + post.username, content: "<p>" + dfdEscapeHTML(post.text) + "</p>"})
	}
	if len(data) > 0 {
		return dfdBuildCommentTree(data)
	}
	return ""
}

func (e *dfdThreads) findPostsInJSON(obj any, depth int, results []thJSONPost) []thJSONPost {
	if _, isObj := obj.(*jsObj); depth > 35 || obj == nil || !(isObj || isArr(obj)) {
		return results
	}
	if username, ok := extGet(extGet(obj, "user"), "username").(string); ok && username != "" {
		if text := e.extractTextFromJSON(obj, 0); text != "" {
			results = append(results, thJSONPost{username: username, text: text})
		}
	}
	for _, key := range extKeys(obj) {
		if key == "quoted_post" {
			continue
		}
		results = e.findPostsInJSON(extGet(obj, key), depth+1, results)
	}
	return results
}

func isArr(v any) bool { _, ok := v.([]any); return ok }

func (e *dfdThreads) extractTextFromJSON(obj any, depth int) string {
	if _, isObj := obj.(*jsObj); depth > 10 || obj == nil || !(isObj || isArr(obj)) {
		return ""
	}
	if fragments := extGet(extGet(obj, "text_fragments"), "fragments"); jsTruthy(fragments) {
		list, ok := fragments.([]any)
		if !ok {
			ldThrowType("obj.text_fragments.fragments.map is not a function")
		}
		var b strings.Builder
		for _, f := range list {
			if v := extGetNN(f, "plaintext"); jsTruthy(v) {
				b.WriteString(rscString(v))
			} else if v := extGet(extGet(f, "mention_fragment"), "username"); jsTruthy(v) {
				b.WriteString("@" + rscString(v))
			} else if v := extGet(f, "linkified_web_url"); jsTruthy(v) {
				b.WriteString(rscString(v))
			}
		}
		return b.String()
	}
	for _, key := range extKeys(obj) {
		if key == "quoted_post" {
			continue
		}
		if result := e.extractTextFromJSON(extGet(obj, key), depth+1); result != "" {
			return result
		}
	}
	return ""
}

func (e *dfdThreads) getPostsFromPagelet(pagelet *ldNode) []thPost {
	var posts []thPost
	for _, container := range ldQuerySelectorAll(pagelet, "[data-pressable-container]") {
		// Quoted posts (a pressable inside another pressable) are skipped.
		if pe := container.parentElement(); pe != nil && ldClosest(pe, "[data-pressable-container]") != nil {
			continue
		}
		username := e.getUsername(container)
		if username == "" {
			continue
		}
		posts = append(posts, thPost{
			username:  username,
			date:      e.getDate(container),
			permalink: e.getPermalink(container),
			content:   e.extractPostContent(container),
			element:   container,
		})
	}
	return posts
}

func (e *dfdThreads) extractComments(replyPosts [][]thPost) string {
	var data []dfdComment
	for _, posts := range replyPosts {
		// A single post is a top-level reply (depth 0); several are a reply chain (depth 0, 1, 2, ...).
		for i, p := range posts {
			depth := float64(i)
			if len(posts) == 1 {
				depth = 0
			}
			data = append(data, dfdComment{author: "@" + p.username, date: p.date, content: p.content, depth: depth, url: p.permalink})
		}
	}
	if len(data) > 0 {
		return dfdBuildCommentTree(data)
	}
	return ""
}

func (e *dfdThreads) getUsername(container *ldNode) string {
	for _, link := range ldQuerySelectorAll(container, `a[href^="/@"][role="link"]`) {
		if text := extText(link); text != "" && !strings.Contains(text, "profile picture") {
			return text
		}
	}
	// The fallback is the username of the first profile link's href.
	if first := ldQuerySelector(container, `a[href^="/@"]`); first != nil {
		if m := reThUser.FindStringSubmatch(first.attr("href")); m != nil {
			return m[1]
		}
	}
	return ""
}

func (e *dfdThreads) getDate(container *ldNode) string {
	t := ldQuerySelector(container, "time[datetime]")
	if t == nil {
		return ""
	}
	return mastoISODate(t.attr("datetime"))
}

func (e *dfdThreads) getPermalink(container *ldNode) string {
	timeLink := ldQuerySelector(container, `a[href*="/post/"]`)
	if timeLink == nil {
		return ""
	}
	href := timeLink.attr("href")
	if strings.HasPrefix(href, "http") {
		return href
	}
	return "https://www.threads.com" + href
}

func thSkipText(text string) bool {
	return text == "" || text == "Author" || text == "·" || text == "Top" || text == "View activity"
}

func (e *dfdThreads) extractPostContent(container *ldNode) string {
	var parts []string
	for _, span := range ldQuerySelectorAll(container, `span[dir="auto"]`) {
		if ldClosest(span, `a[href^="/@"], a[href*="/post/"], a[href*="l.threads.com"], time`) != nil {
			continue
		}
		if ldClosest(span, `[role="button"]`) != nil {
			continue
		}
		text := extText(span)
		if thSkipText(text) {
			continue
		}
		if reThDate.MatchString(text) || reThPostPath.MatchString(text) {
			continue
		}
		if e.stripThreadNumber(text) == "" {
			continue
		}
		if cleaned := e.cleanText(span); cleaned != "" {
			parts = append(parts, "<p>"+cleaned+"</p>")
		}
	}
	if images := e.extractImages(container); images != "" {
		parts = append(parts, images)
	}
	if card := e.extractLinkCard(container); card != "" {
		parts = append(parts, card)
	}
	if quoted := e.extractQuotedPost(container); quoted != "" {
		parts = append(parts, quoted)
	}
	return strings.Join(parts, "\n")
}

func (e *dfdThreads) cleanText(span *ldNode) string {
	clone := span.cloneNode(true)
	e.removeThreadNumbers(clone)
	for _, link := range ldQuerySelectorAll(clone, "a") {
		href := link.attr("href")
		text := extText(link)
		// Post permalink links go entirely.
		if reThPermalink.MatchString(href) {
			link.remove()
			continue
		}
		clean := ldNewElement("a")
		if strings.Contains(href, "l.threads.com") {
			clean.setAttribute("href", e.unwrapRedirectURL(href))
		} else if strings.HasPrefix(href, "/@") {
			username := strings.TrimPrefix(href, "/@")
			clean.setAttribute("href", "https://www.threads.com/@"+username)
			clean.setTextContent("@" + username)
			link.replaceWith(clean)
			continue
		} else if strings.HasPrefix(href, "http") {
			clean.setAttribute("href", href)
		} else {
			clean.setAttribute("href", "https://www.threads.com"+href)
		}
		clean.setTextContent(text)
		link.replaceWith(clean)
	}
	for _, el := range ldQuerySelectorAll(clone, "span, div") {
		el.replaceWith(el.childNodes()...)
	}
	html := ldInnerHTML(clone)
	if html == "" {
		html = clone.textContent()
	}
	html = reThComment.ReplaceAllLiteralString(jsTrim(html), "")
	return jsTrim(jsCollapseWS(html))
}

func (e *dfdThreads) stripThreadNumber(text string) string {
	return jsTrim(reThNumber.ReplaceAllLiteralString(text, ""))
}

// removeThreadNumbers drops the divs whose whole text is a fraction like "1/2" made of separate spans.
func (e *dfdThreads) removeThreadNumbers(container *ldNode) {
	for _, div := range ldQuerySelectorAll(container, "div") {
		if reThFraction.MatchString(extText(div)) && len(ldQuerySelectorAll(div, "span")) >= 2 {
			div.remove()
		}
	}
}

// unwrapRedirectURL is the u parameter of a l.threads.com redirect, else the href.
func (e *dfdThreads) unwrapRedirectURL(href string) string {
	u, ok := newURL(href, "")
	if !ok {
		return href
	}
	actual, found := extQueryParam(u.search(), "u")
	if !found || actual == "" {
		return href
	}
	decoded, ok := jsDecodeURIComponent(actual)
	if !ok {
		return href
	}
	return decoded
}

func (e *dfdThreads) extractImages(container *ldNode) string {
	var images []string
	for _, img := range ldQuerySelectorAll(container, "img") {
		alt := img.attr("alt")
		src := img.attr("src")
		if strings.Contains(alt, "profile picture") || src == "" {
			continue
		}
		if ldClosest(img, `a[href*="l.threads.com"]`) != nil {
			continue
		}
		if width := jsParseInt(orDefault(img.attr("width"), "0")); width > 0 && width <= 48 {
			continue
		}
		images = append(images, `<img src="`+dfdEscapeHTML(src)+`" alt="`+dfdEscapeHTML(alt)+`" />`)
	}
	return strings.Join(images, "\n")
}

func (e *dfdThreads) extractLinkCard(container *ldNode) string {
	for _, cardLink := range ldQuerySelectorAll(container, `a[href*="l.threads.com"]`) {
		img := ldQuerySelector(cardLink, "img")
		if img == nil {
			continue
		}
		actual := e.unwrapRedirectURL(cardLink.attr("href"))
		imgSrc := img.attr("src")
		imgAlt := img.attr("alt")
		if imgSrc != "" {
			return `<a href="` + dfdEscapeHTML(actual) + `"><img src="` + dfdEscapeHTML(imgSrc) + `" alt="` + dfdEscapeHTML(imgAlt) + `" /></a>`
		}
	}
	return ""
}

func (e *dfdThreads) extractQuotedPost(container *ldNode) string {
	// A browser DOM nests the quoted post as a [data-pressable-container].
	if nested := ldQuerySelector(container, "[data-pressable-container]"); nested != nil {
		return e.extractQuotedPostFrom(nested)
	}
	// Server HTML: a second /@user/post/ link with text (the main post's permalink only has a date).
	for _, link := range ldQuerySelectorAll(container, `a[href*="/post/"]`) {
		text := extText(link)
		if reThDate.MatchString(text) {
			continue
		}
		href := link.attr("href")
		m := reThUserPost.FindStringSubmatch(href)
		if m == nil {
			continue
		}
		permalink := href
		if !strings.HasPrefix(href, "http") {
			permalink = "https://www.threads.com" + href
		}
		return dfdBuildQuotedPost(dfdQuotedPost{author: "@" + m[1], content: "<p>" + dfdEscapeHTML(text) + "</p>", url: permalink})
	}
	return ""
}

func (e *dfdThreads) extractQuotedPostFrom(quoted *ldNode) string {
	username := e.getUsername(quoted)
	date := e.getDate(quoted)
	// The text is often inside the post permalink link: only username-only @-links are skipped.
	content := ""
	for _, span := range ldQuerySelectorAll(quoted, `span[dir="auto"]`) {
		if ldClosest(span, `[role="button"], time`) != nil {
			continue
		}
		if link := ldClosest(span, `a[href^="/@"]`); link != nil && !strings.Contains(link.attr("href"), "/post/") {
			continue
		}
		text := extText(span)
		if text == "" || text == "·" || text == "Author" {
			continue
		}
		if reThDate.MatchString(text) {
			continue
		}
		if cleaned := e.stripThreadNumber(text); cleaned != "" {
			content += "<p>" + dfdEscapeHTML(cleaned) + "</p>\n"
		}
	}
	author := ""
	if username != "" {
		author = "@" + username
	}
	return dfdBuildQuotedPost(dfdQuotedPost{author: author, date: date, content: jsTrim(content)})
}

func (e *dfdThreads) createDescription(container *ldNode) string {
	if container == nil {
		return ""
	}
	for _, span := range ldQuerySelectorAll(container, `span[dir="auto"]`) {
		if ldClosest(span, `a[href^="/@"], [role="button"], a[href*="/post/"], time`) != nil {
			continue
		}
		text := extText(span)
		if thSkipText(text) || reThDate.MatchString(text) {
			continue
		}
		if cleaned := e.stripThreadNumber(text); cleaned != "" {
			return jsCollapseWS(jsSlice(cleaned, 0, 140))
		}
	}
	return ""
}
