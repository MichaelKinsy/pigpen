package websearch

import (
	"strings"
)

// Port of defuddle 0.19.3 dist/utils/comments.js (the standardized comment markup the extractors share), the
// escapeHtml of dist/utils/dom.js, and the extractor-output half of dist/standardize.js.

// dfdEscapeHTML is dom.escapeHtml: & < > and the double quote.
func dfdEscapeHTML(s string) string {
	if !strings.ContainsAny(s, `&<>"`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// dfdSiteTokens is SITE_TOKENS: the wrapper classes the extractors emit next to post and comments.
var dfdSiteTokens = map[string]bool{
	"bluesky": true, "discourse": true, "github": true, "gmail": true, "hackernews": true, "linkedin": true,
	"lwn": true, "mastodon": true, "reddit": true, "threads": true, "twitter": true,
}

var reExtractorClass = jsRE(`^(?:comments?|posts?|quoted-post|x-article)(?:-[\w-]+)?$`, "")

// dfdIsExtractorClass is isExtractorClass: is this class token markup an extractor wrote, not one copied off the
// page.
func dfdIsExtractorClass(token string) bool {
	return dfdSiteTokens[token] || reExtractorClass.MatchString(token)
}

var reWSRun = jsRE(`\s+`, "")

// dfdFilterExtractorClasses is standardize's filterExtractorClasses: the extractor classes of a class value.
func dfdFilterExtractorClasses(v string) string {
	var kept []string
	for _, t := range reWSRun.Split(v, -1) {
		if dfdIsExtractorClass(t) {
			kept = append(kept, t)
		}
	}
	return strings.Join(kept, " ")
}

// dfdStandardizeExtractorOutput is standardizeExtractorOutput (debug off): attribute stripping, empty-element
// removal, and the extra <br> cleanup, in that order.
func dfdStandardizeExtractorOutput(el *ldNode) {
	dfdStripUnwantedAttributes(el, true)
	dfdRemoveEmptyElements(el)
	dfdStripExtraBrElements(el)
}

// dfdComment is CommentData. depth is a number as in the original (Hacker News divides a pixel width by 40, so it
// may be fractional or NaN); a missing depth is 0, as `comment.depth ?? 0` reads it. score and url are empty when
// the original leaves them undefined.
type dfdComment struct {
	author  string
	date    string
	content string
	depth   float64
	score   string
	url     string
}

// dfdBuildContentHTML is buildContentHtml: the post block, and the comments block when there are comments.
func dfdBuildContentHTML(site, postContent, comments string) string {
	var b strings.Builder
	b.WriteString("<article data-defuddle>\n\t\t\t<div class=\"" + site + " post\">\n\t\t\t\t<div class=\"post-content\">\n\t\t\t\t\t")
	b.WriteString(postContent)
	b.WriteString("\n\t\t\t\t</div>\n\t\t\t</div>\n\t\t\t")
	if comments != "" {
		b.WriteString("\n\t\t\t\t<hr>\n\t\t\t\t<div class=\"" + site + " comments\">\n\t\t\t\t\t<h2>Comments</h2>\n\t\t\t\t\t")
		b.WriteString(comments)
		b.WriteString("\n\t\t\t\t</div>\n\t\t\t")
	}
	b.WriteString("\n\t\t</article>")
	return b.String()
}

// dfdBuildCommentTree is buildCommentTree: a flat list with depths as nested blockquotes. Depth comparisons are
// JavaScript's, so a NaN depth never opens or closes a level.
func dfdBuildCommentTree(comments []dfdComment) string {
	var b strings.Builder
	var stack []float64
	for _, c := range comments {
		depth := c.depth
		if depth == 0 {
			for len(stack) > 0 {
				b.WriteString("</blockquote>")
				stack = stack[:len(stack)-1]
			}
			b.WriteString("<blockquote>")
			stack = append(stack, 0)
		} else {
			current := -1.0
			if len(stack) > 0 {
				current = stack[len(stack)-1]
			}
			if depth < current {
				for len(stack) > 0 && stack[len(stack)-1] >= depth {
					b.WriteString("</blockquote>")
					stack = stack[:len(stack)-1]
				}
			}
			newCurrent := -1.0
			if len(stack) > 0 {
				newCurrent = stack[len(stack)-1]
			}
			if depth > newCurrent {
				b.WriteString("<blockquote>")
				stack = append(stack, depth)
			}
		}
		b.WriteString(dfdBuildComment(c))
	}
	for len(stack) > 0 {
		b.WriteString("</blockquote>")
		stack = stack[:len(stack)-1]
	}
	return b.String()
}

// dfdBuildComment is buildComment: author, date (a link when there is a safe URL) and score, then the content.
func dfdBuildComment(c dfdComment) string {
	author := `<span class="comment-author"><strong>` + dfdEscapeHTML(c.author) + `</strong></span>`
	safeURL := ""
	if c.url != "" && !dfdIsDangerousURL(c.url, true) {
		safeURL = c.url
	}
	var dateHTML string
	if safeURL != "" {
		dateHTML = `<a href="` + dfdEscapeHTML(safeURL) + `" class="comment-link">` + dfdEscapeHTML(c.date) + `</a>`
	} else {
		dateHTML = `<span class="comment-date">` + dfdEscapeHTML(c.date) + `</span>`
	}
	scoreHTML := ""
	if c.score != "" {
		scoreHTML = ` · <span class="comment-points">` + dfdEscapeHTML(c.score) + `</span>`
	}
	return "<div class=\"comment\">\n\t<div class=\"comment-metadata\">\n\t\t" + author + " · " + dateHTML + scoreHTML +
		"\n\t</div>\n\t<div class=\"comment-content\">" + c.content + "</div>\n</div>"
}

// dfdQuotedPost is the argument of buildQuotedPost.
type dfdQuotedPost struct {
	author  string
	date    string
	content string
	url     string
}

// dfdBuildQuotedPost is buildQuotedPost: a quoted or reposted post as a blockquote.
func dfdBuildQuotedPost(p dfdQuotedPost) string {
	header := ""
	if p.author != "" {
		header += "<p><strong>" + dfdEscapeHTML(p.author) + "</strong>"
		if p.date != "" {
			header += " · " + dfdEscapeHTML(p.date)
		}
		header += "</p>"
	}
	footer := ""
	if p.url != "" {
		safe := p.url
		if dfdIsDangerousURL(p.url, true) {
			safe = ""
		}
		if safe != "" {
			footer = "\n<p><a href=\"" + dfdEscapeHTML(safe) + "\">" + dfdEscapeHTML(safe) + "</a></p>"
		}
	}
	return `<blockquote class="quoted-post">` + header + p.content + footer + `</blockquote>`
}
