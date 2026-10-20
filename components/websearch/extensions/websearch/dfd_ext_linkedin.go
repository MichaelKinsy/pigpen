package websearch

import (
	"strings"
)

// Port of defuddle 0.19.3 dist/extractors/linkedin.js: a LinkedIn feed post and its comments.

type dfdLinkedIn struct {
	ctx         dfdExtCtx
	postArticle *ldNode
}

var (
	reLiComment   = jsRE(`<!--.*?-->`, "")
	reLiParaSplit = jsRE(`(?:<br\s*\/?>\s*){2,}|\n{2,}`, "")
	reLiBr        = jsRE(`<br\s*\/?>`, "")
	reLiAge       = jsRE(`^(\d+\w+)`, "")
)

const (
	liCommentary = ".update-components-text.update-components-update-v2__commentary"
	liWrapper    = ".feed-shared-update-v2__update-content-wrapper"
)

func init() {
	dfdRegisterExtractor("linkedin", func(c dfdExtCtx) dfdExtractor {
		return &dfdLinkedIn{ctx: c, postArticle: ldQuerySelector(c.doc, `[role="article"].feed-shared-update-v2`)}
	})
}

func (e *dfdLinkedIn) name() string { return "linkedin" }

func (e *dfdLinkedIn) canExtract() bool { return e.postArticle != nil }

func (e *dfdLinkedIn) extract() dfdExtracted {
	postContent := e.getPostContent()
	comments := e.extractComments()
	contentHTML := dfdBuildContentHTML("linkedin", postContent, comments)
	author := e.getAuthorName()
	description := e.createDescription()
	return dfdExtracted{
		content:     contentHTML,
		contentHTML: contentHTML,
		variables: map[string]string{
			"title":       "Post by " + author + " on LinkedIn",
			"author":      author,
			"site":        "LinkedIn",
			"description": description,
		},
	}
}

func (e *dfdLinkedIn) getPostContent() string {
	if e.postArticle == nil {
		return ""
	}
	// Only the top-level commentary, not a quoted or reposted post nested inside.
	quotedWrapper := ldQuerySelector(e.postArticle, liWrapper)
	textEl := ldQuerySelector(e.postArticle, liCommentary)
	text := ""
	if textEl != nil && (quotedWrapper == nil || !quotedWrapper.contains(textEl)) {
		text = e.cleanTextContent(textEl)
	}
	images := e.extractImages()
	video := e.extractVideo()
	quotedPost := e.extractQuotedPost(quotedWrapper)
	html := ""
	if text != "" {
		html += text
	}
	if images != "" {
		html += "\n" + images
	}
	if video != "" {
		html += "\n" + video
	}
	if quotedPost != "" {
		html += "\n" + quotedPost
	}
	return html
}

// getVisibleText is the text of an element without its screen-reader duplicates and the alsoRemove selectors.
func (e *dfdLinkedIn) getVisibleText(el *ldNode, alsoRemove string) string {
	clone := el.cloneNode(true)
	selector := ".visually-hidden"
	if alsoRemove != "" {
		selector = ".visually-hidden, " + alsoRemove
	}
	extRemoveAll(clone, selector)
	return jsTrim(clone.textContent())
}

func (e *dfdLinkedIn) cleanTextContent(el *ldNode) string {
	clone := el.cloneNode(true)
	extRemoveAll(clone, ".visually-hidden, .feed-shared-inline-show-more-text__see-more-less-toggle")
	// Links keep their href and text; LinkedIn's internal attributes go.
	for _, link := range ldQuerySelectorAll(clone, "a") {
		href := link.attr("href")
		text := jsTrim(link.textContent())
		if href != "" && text != "" {
			clean := ldNewElement("a")
			clean.setAttribute("href", href)
			clean.setTextContent(text)
			link.replaceWith(clean)
		} else {
			link.replaceWith(ldNewText(link.textContent()))
		}
	}
	// Spans and divs are unwrapped.
	for _, w := range ldQuerySelectorAll(clone, "span, div") {
		w.replaceWith(w.childNodes()...)
	}
	html := jsTrim(extSerialize(clone))
	html = reLiComment.ReplaceAllLiteralString(html, "")
	var paras []string
	for _, p := range reLiParaSplit.Split(html, -1) {
		p = reLiBr.ReplaceAllLiteralString(p, " ")
		p = jsTrim(jsCollapseWS(p))
		if p != "" {
			paras = append(paras, "<p>"+p+"</p>")
		}
	}
	return strings.Join(paras, "\n")
}

func (e *dfdLinkedIn) extractQuotedPost(wrapper *ldNode) string {
	if wrapper == nil {
		return ""
	}
	authorName := ""
	if actorTitle := ldQuerySelector(wrapper, ".update-components-actor__title"); actorTitle != nil {
		authorName = e.getVisibleText(actorTitle, ".update-components-actor__supplementary-actor-info, .text-view-model__verified-icon")
	}
	// The date comes from the sub-description (for example "1w • 🌐").
	date := ""
	if subDesc := ldQuerySelector(wrapper, ".update-components-actor__sub-description"); subDesc != nil {
		src := ldQuerySelector(subDesc, `[aria-hidden="true"]`)
		if src == nil {
			src = subDesc
		}
		if m := reLiAge.FindStringSubmatch(extText(src)); m != nil {
			date = m[1]
		}
	}
	content := ""
	if textEl := ldQuerySelector(wrapper, liCommentary); textEl != nil {
		content = e.cleanTextContent(textEl)
	}
	postURL := extAttr(ldQuerySelector(wrapper, "a.update-components-mini-update-v2__link-to-details-page"), "href")
	url := ""
	if postURL != "" {
		full := postURL
		if !strings.HasPrefix(postURL, "http") {
			full = "https://www.linkedin.com" + postURL
		}
		url = strings.SplitN(full, "?", 2)[0]
	}
	return dfdBuildQuotedPost(dfdQuotedPost{author: authorName, date: date, content: content, url: url})
}

func (e *dfdLinkedIn) extractImages() string {
	if e.postArticle == nil {
		return ""
	}
	var images []string
	for _, img := range ldQuerySelectorAll(e.postArticle, ".update-components-image img, .feed-shared-image img") {
		src := img.attr("src")
		alt := img.attr("alt")
		if src != "" && !strings.Contains(src, "profile-displayphoto") && !strings.Contains(src, "avm-avatar") {
			images = append(images, `<img src="`+dfdEscapeHTML(src)+`" alt="`+dfdEscapeHTML(alt)+`" />`)
		}
	}
	return strings.Join(images, "\n")
}

func (e *dfdLinkedIn) extractVideo() string {
	if e.postArticle == nil {
		return ""
	}
	video := ldQuerySelector(e.postArticle, ".update-components-linkedin-video video[poster]")
	if video == nil {
		return ""
	}
	return `<img src="` + dfdEscapeHTML(video.attr("poster")) + `" alt="Video thumbnail" />`
}

func (e *dfdLinkedIn) extractComments() string {
	if e.postArticle == nil {
		return ""
	}
	var data []dfdComment
	for _, comment := range ldQuerySelectorAll(e.postArticle, "article.comments-comment-entity:not(.comments-comment-entity--reply)") {
		if d, ok := e.extractCommentData(comment, 0); ok {
			data = append(data, d)
		}
		for _, reply := range ldQuerySelectorAll(comment, ".comments-replies-list article.comments-comment-entity--reply") {
			if d, ok := e.extractCommentData(reply, 1); ok {
				data = append(data, d)
			}
		}
	}
	if len(data) > 0 {
		return dfdBuildCommentTree(data)
	}
	return ""
}

func (e *dfdLinkedIn) extractCommentData(comment *ldNode, depth float64) (dfdComment, bool) {
	author := extText(ldQuerySelector(comment, ".comments-comment-meta__description-title"))
	if author == "" {
		return dfdComment{}, false
	}
	content := ""
	if textEl := ldQuerySelector(comment, ".comments-comment-entity__content .update-components-text"); textEl != nil {
		content = e.cleanTextContent(textEl)
	}
	date := extText(ldQuerySelector(comment, "time.comments-comment-meta__data"))
	profileHref := strings.SplitN(extAttr(ldQuerySelector(comment, "a.comments-comment-meta__description-container"), "href"), "?", 2)[0]
	url := ""
	if profileHref != "" {
		url = profileHref
		if !strings.HasPrefix(profileHref, "http") {
			url = "https://www.linkedin.com" + profileHref
		}
	}
	reactions := extText(ldQuerySelector(comment, ".comments-comment-social-bar__reactions-count--cr span.v-align-middle"))
	score := ""
	if reactions != "" {
		score = reactions + " reactions"
	}
	return dfdComment{author: author, date: date, content: content, depth: depth, score: score, url: url}, true
}

func (e *dfdLinkedIn) getAuthorName() string {
	if e.postArticle == nil {
		return ""
	}
	nameEl := ldQuerySelector(e.postArticle, ".update-components-actor__title")
	if nameEl == nil {
		return ""
	}
	return e.getVisibleText(nameEl, ".text-view-model__verified-icon, .update-components-actor__supplementary-actor-info")
}

func (e *dfdLinkedIn) createDescription() string {
	if e.postArticle == nil {
		return ""
	}
	quotedWrapper := ldQuerySelector(e.postArticle, liWrapper)
	textEl := ldQuerySelector(e.postArticle, liCommentary)
	if textEl == nil || (quotedWrapper != nil && quotedWrapper.contains(textEl)) {
		return ""
	}
	return jsCollapseWS(jsSlice(e.getVisibleText(textEl, ""), 0, 140))
}
