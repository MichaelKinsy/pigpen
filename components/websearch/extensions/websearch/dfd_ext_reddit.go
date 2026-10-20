package websearch

// Port of defuddle 0.19.3 dist/extractors/reddit.js, synchronous surface only: the rendered shreddit page and the
// old.reddit page. The extractor's other half (canExtractAsync, prefersAsync, extractAsync, the request cache and the
// Atom feed reader behind it) is reached only through parseAsync, which does not run with useAsync false, and
// extract() reads none of it: it only calls extractOldReddit, buildResult and the DOM readers below.

type dfdReddit struct {
	ctx          dfdExtCtx
	shredditPost *ldNode
	isOldReddit  bool
}

var reRedditSubreddit = jsRE(`\/r\/([^/]+)`, "")

func init() {
	dfdRegisterExtractor("reddit", func(c dfdExtCtx) dfdExtractor {
		return &dfdReddit{
			ctx:          c,
			shredditPost: ldQuerySelector(c.doc, "shreddit-post"),
			isOldReddit:  ldQuerySelector(c.doc, ".thing.link") != nil,
		}
	})
}

func (e *dfdReddit) name() string { return "reddit" }

func (e *dfdReddit) canExtract() bool { return e.shredditPost != nil || e.isOldReddit }

func (e *dfdReddit) extract() dfdExtracted {
	if e.isOldReddit {
		return e.extractOldReddit(e.ctx.doc)
	}
	postTitle := extText(ldQuerySelector(e.ctx.doc, "h1"))
	subreddit := e.getSubreddit()
	postAuthor := extAttr(e.shredditPost, "author")
	postContent := e.getPostContent()
	description := e.createDescription(postContent)
	// Comments already in the DOM (a browser renders them with JavaScript; server-rendered HTML has none).
	comments := e.processComments(ldQuerySelectorAll(e.ctx.doc, "shreddit-comment"))
	return e.buildResult(postContent, comments, postTitle, postAuthor, subreddit, description)
}

// buildResult is the one result shape of every source: only where the title, author and body come from differs.
func (e *dfdReddit) buildResult(postBody, comments, title, author, subreddit, description string) dfdExtracted {
	contentHTML := dfdBuildContentHTML("reddit", postBody, comments)
	return dfdExtracted{
		content:     contentHTML,
		contentHTML: contentHTML,
		variables:   map[string]string{"title": title, "author": author, "site": "r/" + subreddit, "description": description},
	}
}

func (e *dfdReddit) extractOldReddit(root *ldNode) dfdExtracted {
	thingLink := ldQuerySelector(root, ".thing.link")
	postTitle := extText(extQuery(thingLink, "a.title"))
	postAuthor := extAttr(thingLink, "data-author")
	subreddit := extAttr(thingLink, "data-subreddit")
	postBody := ""
	if el := extQuery(thingLink, ".usertext-body .md"); el != nil {
		postBody = extSerialize(el)
	}
	var data []dfdComment
	if area := ldQuerySelector(root, ".commentarea .sitetable"); area != nil {
		data = e.collectOldRedditComments(area, 0)
	}
	comments := dfdBuildCommentTree(data)
	return e.buildResult(postBody, comments, postTitle, postAuthor, subreddit, e.createDescription(postBody))
}

func (e *dfdReddit) getPostContent() string {
	textBody := ""
	if el := extQuery(e.shredditPost, `[slot="text-body"]`); el != nil {
		textBody = extSerialize(el)
	}
	mediaBody := ""
	if el := extQuery(e.shredditPost, "#post-image"); el != nil {
		mediaBody = ldOuterHTML(el)
	}
	return textBody + mediaBody
}

func (e *dfdReddit) getSubreddit() string {
	if m := reRedditSubreddit.FindStringSubmatch(e.ctx.url); m != nil {
		return m[1]
	}
	return ""
}

func (e *dfdReddit) createDescription(postContent string) string {
	if postContent == "" {
		return ""
	}
	temp := ldNewElement("div")
	temp.appendChild(ldParseFragment(postContent))
	return jsCollapseWS(jsSlice(jsTrim(temp.textContent()), 0, 140))
}

func (e *dfdReddit) collectOldRedditComments(container *ldNode, depth float64) []dfdComment {
	var result []dfdComment
	for _, comment := range ldQuerySelectorAll(container, ":scope > .thing.comment") {
		author := comment.attr("data-author")
		permalink := comment.attr("data-permalink")
		score := extText(ldQuerySelector(comment, ".entry .tagline .score.unvoted"))
		datetime := extAttr(ldQuerySelector(comment, ".entry .tagline time[datetime]"), "datetime")
		date := ""
		if datetime != "" {
			date = jsISODate(jsDateFromString(datetime))
		}
		body := ""
		if el := ldQuerySelector(comment, ".entry .usertext-body .md"); el != nil {
			body = extSerialize(el)
		}
		c := dfdComment{author: author, date: date, content: body, depth: depth, score: score}
		if permalink != "" {
			c.url = "https://reddit.com" + permalink
		}
		result = append(result, c)
		if child := ldQuerySelector(comment, ".child > .sitetable"); child != nil {
			result = append(result, e.collectOldRedditComments(child, depth+1)...)
		}
	}
	return result
}

func (e *dfdReddit) processComments(comments []*ldNode) string {
	var data []dfdComment
	for _, comment := range comments {
		depth := jsParseInt(orDefault(comment.attr("depth"), "0"))
		author := comment.attr("author")
		score := orDefault(comment.attr("score"), "0")
		permalink := comment.attr("permalink")
		content := ""
		if el := ldQuerySelector(comment, `[slot="comment"]`); el != nil {
			content = extSerialize(el)
		}
		timestamp := orDefault(comment.attr("created"), extAttr(ldQuerySelector(comment, "time"), "datetime"))
		date := ""
		if timestamp != "" {
			date = jsISODate(jsDateFromString(timestamp))
		}
		c := dfdComment{author: author, date: date, content: content, depth: depth, score: score + " points"}
		if permalink != "" {
			c.url = "https://reddit.com" + permalink
		}
		data = append(data, c)
	}
	return dfdBuildCommentTree(data)
}

// orDefault is `value || fallback` for strings.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
