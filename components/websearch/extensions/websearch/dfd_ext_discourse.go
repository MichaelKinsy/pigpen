package websearch

import "strings"

// Port of defuddle 0.19.3 dist/extractors/discourse.js: Discourse topics (a generator meta tag starting with
// Discourse and .topic-post elements); the first post is the post, the others are comments.

type dfdDiscourse struct {
	ctx         dfdExtCtx
	isDiscourse bool
}

func init() {
	dfdRegisterExtractor("discourse", func(c dfdExtCtx) dfdExtractor {
		gen := extAttr(ldQuerySelector(c.doc, `meta[name="generator"]`), "content")
		return &dfdDiscourse{ctx: c, isDiscourse: strings.HasPrefix(gen, "Discourse")}
	})
}

func (e *dfdDiscourse) name() string { return "discourse" }

func (e *dfdDiscourse) canExtract() bool {
	return e.isDiscourse && ldQuerySelector(e.ctx.doc, ".topic-post") != nil
}

func (e *dfdDiscourse) extract() dfdExtracted {
	doc := e.ctx.doc
	title := e.getTopicTitle()
	siteName := extAttr(ldQuerySelector(doc, `meta[property="og:site_name"]`), "content")
	published := e.getPublishedDate()
	posts := ldQuerySelectorAll(doc, ".topic-post")
	var op *ldNode
	for _, p := range posts {
		if p.hasClass("topic-owner") {
			op = p
			break
		}
	}
	postContent, opAuthor := "", ""
	if op != nil {
		postContent = e.extractPostContent(op)
		opAuthor = e.getAuthor(op)
	}
	var replyPosts []*ldNode
	for _, p := range posts {
		if p != op {
			replyPosts = append(replyPosts, p)
		}
	}
	comments := e.extractComments(replyPosts)
	contentHTML := dfdBuildContentHTML("discourse", postContent, comments)
	author := opAuthor
	if author == "" {
		author = e.getAuthor(posts[0])
	}
	description := ""
	if op != nil {
		description = jsCollapseWS(jsSlice(e.getPostText(op), 0, 140))
	}
	site := siteName
	if site == "" {
		site = "Discourse"
	}
	vars := map[string]string{"title": title, "author": author, "site": site, "description": description}
	if published != "" {
		vars["published"] = published
	}
	return dfdExtracted{content: contentHTML, contentHTML: contentHTML, variables: vars}
}

func (e *dfdDiscourse) getTopicTitle() string {
	doc := e.ctx.doc
	if fancy := ldQuerySelector(doc, ".fancy-title"); fancy != nil {
		return extText(fancy)
	}
	if h1 := ldQuerySelector(doc, "h1[data-topic-id]"); h1 != nil {
		clone := h1.cloneNode(true)
		extRemoveAll(clone, "svg, .topic-statuses")
		return extText(clone)
	}
	return ""
}

func (e *dfdDiscourse) getPublishedDate() string {
	meta := ldQuerySelector(e.ctx.doc, `meta[property="article:published_time"]`)
	if meta == nil {
		return ""
	}
	ms := jsDateFromString(meta.attr("content"))
	if ms != ms {
		return ""
	}
	return jsISODate(ms)
}

func (e *dfdDiscourse) getAuthor(post *ldNode) string {
	link := ldQuerySelector(post, ".names a[data-user-card]")
	if a := extAttr(link, "data-user-card"); a != "" {
		return a
	}
	return extText(link)
}

func (e *dfdDiscourse) getPostDate(post *ldNode) string {
	el := ldQuerySelector(post, ".relative-date[data-time]")
	if el == nil {
		return ""
	}
	raw := el.attr("data-time")
	if raw == "" {
		raw = "0"
	}
	t := jsParseInt(raw)
	if t != t || t == 0 {
		return ""
	}
	ms := jsDateFromNumber(t)
	if ms != ms {
		return ""
	}
	return jsISODate(ms)
}

func (e *dfdDiscourse) getPostPermalink(post *ldNode) string {
	link := ldQuerySelector(post, "a.post-date[href]")
	if link == nil {
		return ""
	}
	href := link.attr("href")
	if href == "" {
		return ""
	}
	if base, ok := newURL(e.ctx.url, ""); ok {
		return base.origin() + href
	}
	return href
}

func (e *dfdDiscourse) getLikeCount(post *ldNode) string {
	count := extText(ldQuerySelector(post, "button.like-count"))
	if count == "" {
		return ""
	}
	return count + " likes"
}

func (e *dfdDiscourse) getPostText(post *ldNode) string {
	cooked := ldQuerySelector(post, ".cooked")
	if cooked == nil {
		return ""
	}
	return extText(cooked)
}

func (e *dfdDiscourse) extractPostContent(post *ldNode) string {
	cooked := ldQuerySelector(post, ".cooked")
	if cooked == nil {
		return ""
	}
	clone := cooked.cloneNode(true)
	extRemoveAll(clone, ".cooked-selection-barrier")
	extRemoveAll(clone, "a.anchor")
	return extSerialize(clone)
}

func (e *dfdDiscourse) extractComments(replyPosts []*ldNode) string {
	if len(replyPosts) == 0 {
		return ""
	}
	data := make([]dfdComment, len(replyPosts))
	for i, post := range replyPosts {
		data[i] = dfdComment{
			author:  e.getAuthor(post),
			content: e.extractPostContent(post),
			date:    e.getPostDate(post),
			url:     e.getPostPermalink(post),
			score:   e.getLikeCount(post),
		}
	}
	return dfdBuildCommentTree(data)
}
