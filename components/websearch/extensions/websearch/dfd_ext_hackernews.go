package websearch

import (
	"strings"
)

// Port of defuddle 0.19.3 dist/extractors/hackernews.js: story, comment and listing pages of Hacker News.

type dfdHackerNews struct {
	ctx           dfdExtCtx
	mainPost      *ldNode
	isListingPage bool
	isCommentPage bool
	mainComment   *ldNode
}

var (
	reHNCommentsText = jsRE(`\d+\s*comment`, "")
	reHNPostID       = jsRE(`id=(\d+)`, "")
	reHNTitleSuffix  = jsRE(`\s*\|\s*Hacker News$`, "")
)

func init() {
	dfdRegisterExtractor("hackernews", func(c dfdExtCtx) dfdExtractor {
		e := &dfdHackerNews{ctx: c}
		e.mainPost = ldQuerySelector(c.doc, ".fatitem")
		e.isListingPage = e.detectListingPage()
		e.isCommentPage = e.mainPost != nil && ldQuerySelector(e.mainPost, ".onstory") != nil && ldQuerySelector(e.mainPost, ".titleline") == nil
		if e.isCommentPage {
			e.mainComment = ldQuerySelector(e.mainPost, "tr.athing")
		}
		return e
	})
}

func (e *dfdHackerNews) name() string { return "hackernews" }

func (e *dfdHackerNews) detectListingPage() bool {
	if e.mainPost != nil {
		return false
	}
	return len(ldQuerySelectorAll(e.ctx.doc, "tr.athing")) > 1
}

func (e *dfdHackerNews) canExtract() bool { return e.mainPost != nil || e.isListingPage }

func hnDate(ageEl *ldNode) string {
	ts := extAttr(ageEl, "title")
	if i := strings.IndexByte(ts, 'T'); i >= 0 {
		ts = ts[:i]
	}
	return ts
}

func (e *dfdHackerNews) extract() dfdExtracted {
	if e.isListingPage {
		return e.extractListing()
	}
	postContent := e.getPostContent()
	comments := e.extractComments()
	contentHTML := dfdBuildContentHTML("hackernews", postContent, comments)
	return dfdExtracted{
		content:     contentHTML,
		contentHTML: contentHTML,
		variables: map[string]string{
			"title":       e.getPostTitle(),
			"author":      e.getPostAuthor(),
			"site":        "Hacker News",
			"description": e.createDescription(),
			"published":   e.getPostDate(),
		},
	}
}

type hnMoreLink struct{ url, text string }

func (e *dfdHackerNews) getMoreLink() *hnMoreLink {
	more := ldQuerySelector(e.ctx.doc, ".morelink")
	if more == nil {
		return nil
	}
	text := extText(more)
	if text == "" {
		text = "More"
	}
	return &hnMoreLink{url: more.attr("href"), text: text}
}

type hnStory struct {
	id, title, url, site, score, author, date, comments, commentsURL string
}

func (e *dfdHackerNews) extractListing() dfdExtracted {
	stories := e.extractStories()
	more := e.getMoreLink()
	contentHTML := e.buildListingHTML(stories, more)
	title := jsTrim(reHNTitleSuffix.ReplaceAllLiteralString(e.ctx.doc.title(), ""))
	if title == "" {
		title = "Hacker News"
	}
	return dfdExtracted{
		content:     contentHTML,
		contentHTML: contentHTML,
		variables:   map[string]string{"title": title, "site": "Hacker News"},
	}
}

func (e *dfdHackerNews) extractStories() []hnStory {
	var stories []hnStory
	for _, row := range ldQuerySelectorAll(e.ctx.doc, "tr.athing") {
		id := row.attr("id")
		titleEl := ldQuerySelector(row, ".titleline a")
		if titleEl == nil {
			continue
		}
		subRow := row.nextElementSibling()
		var score, author, date, commentsText string
		if subRow != nil {
			score = extText(ldQuerySelector(subRow, ".score"))
			author = extText(ldQuerySelector(subRow, ".hnuser"))
			date = hnDate(ldQuerySelector(subRow, ".age"))
			links := ldQuerySelectorAll(subRow, "td.subtext a")
			if len(links) > 0 {
				commentsText = jsTrim(strings.ReplaceAll(links[len(links)-1].textContent(), "\u00a0", " "))
			}
		}
		comments := ""
		if reHNCommentsText.MatchString(commentsText) {
			comments = commentsText
		}
		commentsURL := ""
		if id != "" {
			commentsURL = "https://news.ycombinator.com/item?id=" + id
		}
		stories = append(stories, hnStory{
			id: id, title: extText(titleEl), url: titleEl.attr("href"),
			site: extText(ldQuerySelector(row, ".sitestr")), score: score, author: author, date: date,
			comments: comments, commentsURL: commentsURL,
		})
	}
	return stories
}

func (e *dfdHackerNews) buildListingHTML(stories []hnStory, more *hnMoreLink) string {
	if len(stories) == 0 {
		return ""
	}
	var items []string
	for _, s := range stories {
		h := "<li>"
		h += `<a href="` + dfdEscapeHTML(s.url) + `">` + dfdEscapeHTML(s.title) + `</a>`
		if s.site != "" {
			h += " <small>(" + dfdEscapeHTML(s.site) + ")</small>"
		}
		var meta []string
		if s.score != "" {
			meta = append(meta, dfdEscapeHTML(s.score))
		}
		if s.author != "" {
			meta = append(meta, "by "+dfdEscapeHTML(s.author))
		}
		if s.comments != "" {
			meta = append(meta, `<a href="`+dfdEscapeHTML(s.commentsURL)+`">`+dfdEscapeHTML(s.comments)+`</a>`)
		}
		if len(meta) > 0 {
			h += "<br><small>" + strings.Join(meta, " · ") + "</small>"
		}
		items = append(items, h+"</li>")
	}
	h := "<ol>" + strings.Join(items, "") + "</ol>"
	if more != nil {
		h += `<p><a href="` + dfdEscapeHTML(more.url) + `">` + dfdEscapeHTML(more.text) + `</a></p>`
	}
	return h
}

func (e *dfdHackerNews) getPostContent() string {
	if e.mainPost == nil {
		return ""
	}
	if e.isCommentPage && e.mainComment != nil {
		author := extRawText(ldQuerySelector(e.mainComment, ".hnuser"))
		if author == "" {
			author = "[deleted]"
		}
		commentText := ""
		if ct := ldQuerySelector(e.mainComment, ".commtext"); ct != nil {
			commentText = extSerialize(ct)
		}
		return dfdBuildComment(dfdComment{
			author:  author,
			date:    hnDate(ldQuerySelector(e.mainComment, ".age")),
			content: commentText,
			score:   extText(ldQuerySelector(e.mainComment, ".score")),
		})
	}
	titleRow := ldQuerySelector(e.mainPost, "tr.athing")
	url := extAttr(extQuery(titleRow, ".titleline a"), "href")
	content := ""
	if url != "" {
		content += `<p><a href="` + url + `" target="_blank">` + url + `</a></p>`
	}
	if text := ldQuerySelector(e.mainPost, ".toptext"); text != nil {
		content += `<div class="post-text">` + extSerialize(text) + `</div>`
	}
	return content
}

func (e *dfdHackerNews) extractComments() string {
	return e.processComments(ldQuerySelectorAll(e.ctx.doc, "tr.comtr"))
}

func (e *dfdHackerNews) processComments(comments []*ldNode) string {
	var data []dfdComment
	seen := map[string]bool{}
	for _, c := range comments {
		id := c.attr("id")
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		indent := extAttr(ldQuerySelector(c, ".ind img"), "width")
		if indent == "" {
			indent = "0"
		}
		depth := jsParseInt(indent) / 40
		text := ldQuerySelector(c, ".commtext")
		author := extRawText(ldQuerySelector(c, ".hnuser"))
		if author == "" {
			author = "[deleted]"
		}
		points := extText(ldQuerySelector(c, ".score"))
		if text == nil {
			continue
		}
		data = append(data, dfdComment{
			author:  author,
			date:    hnDate(ldQuerySelector(c, ".age")),
			content: extSerialize(text),
			depth:   depth,
			score:   points,
			url:     "https://news.ycombinator.com/item?id=" + id,
		})
	}
	return dfdBuildCommentTree(data)
}

func (e *dfdHackerNews) getPostTitle() string {
	if e.isCommentPage && e.mainComment != nil {
		author := extRawText(ldQuerySelector(e.mainComment, ".hnuser"))
		if author == "" {
			author = "[deleted]"
		}
		commentText := extRawText(ldQuerySelector(e.mainComment, ".commtext"))
		preview := jsSlice(jsTrim(commentText), 0, 50)
		if jsLen(commentText) > 50 {
			preview += "..."
		}
		return "Comment by " + author + ": " + preview
	}
	return extText(extQuery(e.mainPost, ".titleline"))
}

func (e *dfdHackerNews) getPostAuthor() string { return extText(extQuery(e.mainPost, ".hnuser")) }

func (e *dfdHackerNews) createDescription() string {
	title := e.getPostTitle()
	author := e.getPostAuthor()
	if e.isCommentPage {
		return "Comment by " + author + " on Hacker News"
	}
	return title + " - by " + author + " on Hacker News"
}

func (e *dfdHackerNews) getPostDate() string {
	if e.mainPost == nil {
		return ""
	}
	return hnDate(ldQuerySelector(e.mainPost, ".age"))
}
