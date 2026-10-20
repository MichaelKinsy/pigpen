package websearch

import "strings"

// Port of defuddle 0.19.3 dist/extractors/github.js: issue and pull request pages of github.com.

type dfdGitHub struct {
	ctx     dfdExtCtx
	isIssue bool
	isPR    bool
}

var (
	reGHIssue      = jsRE(`\/issues\/\d+`, "")
	reGHPull       = jsRE(`\/pull\/\d+`, "")
	reGHRepoURL    = jsRE(`github\.com\/([^\/]+)\/([^\/]+)`, "")
	reGHRepoTitle  = jsRE(`([^\/\s]+)\/([^\/\s]+)`, "")
	reGHAuthorHref = jsRE(`github\.com\/([^\/\?#]+)`, "")
	reGHSourceLang = jsRE(`highlight-source-(\w+)`, "")
)

func init() {
	dfdRegisterExtractor("github", func(c dfdExtCtx) dfdExtractor {
		return &dfdGitHub{ctx: c, isIssue: reGHIssue.MatchString(c.url), isPR: reGHPull.MatchString(c.url)}
	})
}

func (e *dfdGitHub) name() string { return "github" }

func (e *dfdGitHub) anySelector(selectors ...string) bool {
	for _, s := range selectors {
		if ldQuerySelector(e.ctx.doc, s) != nil {
			return true
		}
	}
	return false
}

func (e *dfdGitHub) canExtract() bool {
	if !e.anySelector(
		`meta[name="expected-hostname"][content="github.com"]`,
		`meta[name="octolytics-url"]`,
		`meta[name="github-keyboard-shortcuts"]`,
		`.js-header-wrapper`,
		`#js-repo-pjax-container`,
	) {
		return false
	}
	if e.isIssue {
		return e.anySelector(`[data-testid="issue-metadata-sticky"]`, `[data-testid="issue-title"]`)
	}
	if e.isPR {
		return e.anySelector(`.pull-discussion-timeline`, `.discussion-timeline`, `.gh-header-title`, `.js-issue-title`)
	}
	return false
}

func (e *dfdGitHub) extract() dfdExtracted {
	owner, repo := e.extractRepoInfo()
	var prBody *ldNode
	if e.isPR {
		prBody = e.getPRBody()
	}
	var postContent, author, published string
	if e.isPR {
		postContent, author, published = e.getPRContent(prBody)
	} else {
		postContent, author, published = e.getIssueContent()
	}
	var comments string
	if e.isPR {
		comments = e.extractPRComments(prBody)
	} else {
		comments = e.extractComments()
	}
	contentHTML := dfdBuildContentHTML("github", postContent, comments)
	return dfdExtracted{
		content:     contentHTML,
		contentHTML: contentHTML,
		variables: map[string]string{
			"title":       e.ctx.doc.title(),
			"author":      author,
			"published":   published,
			"site":        "GitHub - " + owner + "/" + repo,
			"description": e.createDescription(contentHTML),
		},
	}
}

func (e *dfdGitHub) getIssueContent() (content, author, published string) {
	container := ldQuerySelector(e.ctx.doc, `[data-testid="issue-viewer-issue-container"]`)
	if container == nil {
		return "", "", ""
	}
	author = e.extractAuthor(container, []string{
		`a[data-testid="issue-body-header-author"]`,
		`.IssueBodyHeaderAuthor-module__authorLoginLink--_S7aT`,
		`.ActivityHeader-module__AuthorLink--iofTU`,
		`a[href*="/users/"][data-hovercard-url*="/users/"]`,
		`a[aria-label*="profile"]`,
	})
	published = extAttr(ldQuerySelector(container, "relative-time"), "datetime")
	body := ldQuerySelector(container, `[data-testid="issue-body-viewer"] .markdown-body`)
	if body == nil {
		return "", author, published
	}
	return e.cleanBodyContent(body), author, published
}

// ghDate is `timestamp ? new Date(timestamp).toISOString().split('T')[0] : ”`; an invalid timestamp throws a
// RangeError, which nothing here catches.
func ghDate(timestamp string) string {
	if timestamp == "" {
		return ""
	}
	return jsISODate(jsDateFromString(timestamp))
}

func (e *dfdGitHub) extractComments() string {
	processed := map[string]bool{}
	var data []dfdComment
	for _, el := range ldQuerySelectorAll(e.ctx.doc, "[data-wrapper-timeline-id]") {
		container := ldQuerySelector(el, ".react-issue-comment")
		if container == nil {
			continue
		}
		id := el.attr("data-wrapper-timeline-id")
		if id == "" || processed[id] {
			continue
		}
		processed[id] = true
		author := e.extractAuthor(container, []string{
			`.ActivityHeader-module__AuthorLink--iofTU`,
			`a[data-testid="avatar-link"]`,
			`a[href^="/"][data-hovercard-url*="/users/"]`,
		})
		date := ghDate(extAttr(ldQuerySelector(container, "relative-time"), "datetime"))
		body := ldQuerySelector(container, ".markdown-body")
		if body == nil {
			continue
		}
		content := e.cleanBodyContent(body)
		if content == "" {
			continue
		}
		data = append(data, dfdComment{author: author, date: date, content: content})
	}
	return dfdBuildCommentTree(data)
}

func (e *dfdGitHub) getPRBody() *ldNode {
	if b := ldQuerySelector(e.ctx.doc, `[id^="pullrequest-"]`); b != nil {
		return b
	}
	return ldQuerySelector(e.ctx.doc, ".timeline-comment")
}

func (e *dfdGitHub) getPRContent(prBody *ldNode) (content, author, published string) {
	bodyEl := extQuery(prBody, ".comment-body.markdown-body")
	if bodyEl == nil {
		bodyEl = ldQuerySelector(e.ctx.doc, ".comment-body.markdown-body")
	}
	if bodyEl != nil {
		content = e.cleanBodyContent(bodyEl)
	}
	authorEl := extQuery(prBody, ".author")
	if authorEl == nil {
		authorEl = ldQuerySelector(e.ctx.doc, ".gh-header-meta .author")
	}
	author = extText(authorEl)
	published = extAttr(extQuery(prBody, "relative-time"), "datetime")
	return
}

func (e *dfdGitHub) extractPRComments(prBody *ldNode) string {
	var data []dfdComment
	for _, c := range ldQuerySelectorAll(e.ctx.doc, ".timeline-comment, .review-comment") {
		if prBody != nil && (c == prBody || prBody.contains(c)) {
			continue
		}
		author := extText(ldQuerySelector(c, ".author"))
		date := ghDate(extAttr(ldQuerySelector(c, "relative-time"), "datetime"))
		bodyEl := ldQuerySelector(c, ".comment-body.markdown-body")
		if bodyEl == nil {
			continue
		}
		content := e.cleanBodyContent(bodyEl)
		if content == "" {
			continue
		}
		data = append(data, dfdComment{author: author, date: date, content: content})
	}
	return dfdBuildCommentTree(data)
}

func (e *dfdGitHub) extractAuthor(container *ldNode, selectors []string) string {
	for _, sel := range selectors {
		link := ldQuerySelector(container, sel)
		if link == nil {
			continue
		}
		href := link.attr("href")
		if href == "" {
			continue
		}
		if strings.HasPrefix(href, "/") {
			return href[1:]
		} else if strings.Contains(href, "github.com/") {
			if m := reGHAuthorHref.FindStringSubmatch(href); m != nil && m[1] != "" {
				return m[1]
			}
		}
	}
	return "Unknown"
}

func (e *dfdGitHub) cleanBodyContent(bodyElement *ldNode) string {
	clean := bodyElement.cloneNode(true)
	extRemoveAll(clean, `button, [data-testid*="button"], [data-testid*="menu"]`)
	extRemoveAll(clean, ".js-clipboard-copy, .zeroclipboard-container")
	for _, pre := range ldQuerySelectorAll(clean, `div.highlight[class*="highlight-source-"] pre, div.highlight pre`) {
		wrapper := pre.parentElement()
		if wrapper == nil {
			continue
		}
		lang := ""
		if m := reGHSourceLang.FindStringSubmatch(wrapper.className()); m != nil {
			lang = m[1]
		}
		content := wrapper.attr("data-snippet-clipboard-copy-content")
		if content == "" {
			content = pre.textContent()
		}
		code := ldNewElement("code")
		if lang != "" {
			code.setAttribute("class", "language-"+lang)
			code.setAttribute("data-lang", lang)
		}
		code.setTextContent(content)
		newPre := ldNewElement("pre")
		newPre.appendChild(code)
		wrapper.replaceWith(newPre)
	}
	return jsTrim(extSerialize(clean))
}

func (e *dfdGitHub) extractRepoInfo() (owner, repo string) {
	if m := reGHRepoURL.FindStringSubmatch(e.ctx.url); m != nil {
		return m[1], m[2]
	}
	if m := reGHRepoTitle.FindStringSubmatch(e.ctx.doc.title()); m != nil {
		return m[1], m[2]
	}
	return "", ""
}

func (e *dfdGitHub) createDescription(content string) string {
	if content == "" {
		return ""
	}
	tmp := ldNewElement("div")
	tmp.appendChild(ldParseFragment(content))
	return jsCollapseWS(jsSlice(extText(tmp), 0, 140))
}
