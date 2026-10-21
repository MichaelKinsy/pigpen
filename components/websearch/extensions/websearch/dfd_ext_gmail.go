package websearch

import (
	"fmt"
	"math"
	"strings"
)

// Port of defuddle 0.19.3 dist/extractors/gmail.js: a Gmail thread, every message a top-level comment.

type dfdGmail struct{ ctx dfdExtCtx }

type gmailMessage struct{ author, email, date, content string }

var reGmailSubject = jsRE(` - [^-]+ - Gmail$`, "")

var gmailRemoveSelectors = strings.Join([]string{
	".gmail_quote",
	".gmail_attr",
	".gmail_extra",
	`blockquote[type="cite"]`,
	".moz-cite-prefix",
	`blockquote[style*="border-left"]`,
	".yahoo_quoted",
	".a6S",
	".adL",
	".h5",
	".adm",
	".ajR",
	".ajT",
	".h4",
	".yj6qo",
}, ", ")

const gmailMediaSelector = "img, picture, video, audio, iframe, svg, canvas, object, embed"

// gmailInlineTags is the shared INLINE_ELEMENTS and the legacy and replaced-element tags sender HTML still uses.
var gmailInlineTags = func() map[string]bool {
	m := map[string]bool{}
	for k := range dfdInlineElements {
		m[k] = true
	}
	for _, k := range []string{"bdi", "bdo", "big", "img", "kbd", "label", "s", "samp", "strike", "tt", "var", "wbr"} {
		m[k] = true
	}
	return m
}()

func init() {
	dfdRegisterExtractor("gmail", func(c dfdExtCtx) dfdExtractor { return &dfdGmail{ctx: c} })
}

func (e *dfdGmail) name() string { return "gmail" }

func (e *dfdGmail) canExtract() bool { return ldQuerySelector(e.ctx.doc, ".adn.ads") != nil }

func (e *dfdGmail) extract() dfdExtracted {
	messages := e.extractMessages()
	data := make([]dfdComment, len(messages))
	for i, m := range messages {
		data[i] = dfdComment{author: m.author, date: m.date, content: m.content}
	}
	commentsHTML := dfdBuildCommentTree(data)
	contentHTML := `<article data-defuddle><div class="gmail comments">` + commentsHTML + `</div></article>`
	subject := e.getSubject()
	var first *gmailMessage
	if len(messages) > 0 {
		first = &messages[0]
	}
	author, published := "", ""
	if first != nil {
		author = first.author
		published = e.toISODate(first.date)
	}
	description := fmt.Sprintf("Gmail thread with %d messages", len(messages))
	if author != "" {
		description = fmt.Sprintf("Gmail thread from %s with %d messages", author, len(messages))
	}
	return dfdExtracted{
		content:     contentHTML,
		contentHTML: contentHTML,
		variables: map[string]string{
			"title":       subject,
			"author":      author,
			"site":        "Gmail",
			"published":   published,
			"description": description,
		},
	}
}

func (e *dfdGmail) extractMessages() []gmailMessage {
	var messages []gmailMessage
	for _, row := range ldQuerySelectorAll(e.ctx.doc, ".adn.ads") {
		sender := ldQuerySelector(row, ".gD")
		author := jsTrim(extAttr(sender, "name"))
		if author == "" {
			author = extText(sender)
		}
		if author == "" {
			author = "Unknown"
		}
		email := jsTrim(extAttr(sender, "email"))
		content := e.getMessageBody(row)
		if content == "" {
			continue
		}
		messages = append(messages, gmailMessage{author: author, email: email, date: e.getDate(row), content: content})
	}
	return messages
}

func (e *dfdGmail) getDate(row *ldNode) string {
	dateEl := ldQuerySelector(row, ".g3")
	if t := jsTrim(extAttr(dateEl, "title")); t != "" {
		return t
	}
	return extText(dateEl)
}

// toISODate is the local calendar date of a parsed time (not the UTC one), ” when the text is not a date.
func (e *dfdGmail) toISODate(date string) string {
	if date == "" {
		return ""
	}
	ms := jsDateFromString(date)
	if math.IsNaN(ms) {
		return ""
	}
	y, m, d := jsLocalDate(ms)
	return fmt.Sprintf("%d-%02d-%02d", y, m, d)
}

func (e *dfdGmail) getMessageBody(row *ldNode) string {
	body := ldQuerySelector(row, ".a3s")
	if body == nil {
		return ""
	}
	clone := body.cloneNode(true)
	extRemoveAll(clone, gmailRemoveSelectors)
	gmailStripPlainTextQuotes(clone)
	gmailTrimTrailingBlanks(clone)
	return jsTrim(extSerialize(clone))
}

func gmailIsBlank(node *ldNode) bool {
	if node.kind != ldElem {
		return jsTrim(node.textContent()) == ""
	}
	if jsTrim(node.textContent()) != "" {
		return false
	}
	return !(ldMatches(node, gmailMediaSelector) || ldQuerySelector(node, gmailMediaSelector) != nil)
}

func gmailTrimTrailingBlanks(el *ldNode) {
	for el.last != nil && gmailIsBlank(el.last) {
		el.last.remove()
	}
	if last := el.last; last != nil && last.kind == ldElem {
		gmailTrimTrailingBlanks(last)
	}
}

var reGmailQuote = jsRE(`^\s*>`, "")

func gmailStripPlainTextQuotes(root *ldNode) {
	var lines [][]*ldNode
	var line []*ldNode
	endLine := func() {
		lines = append(lines, line)
		line = nil
	}
	splits := map[*ldNode]bool{}
	for _, br := range ldQuerySelectorAll(root, "br") {
		for p := br.parent; p != nil && p != root && !splits[p]; p = p.parent {
			splits[p] = true
		}
	}
	var walk func(parent *ldNode)
	walk = func(parent *ldNode) {
		for _, node := range parent.childNodes() {
			if node.kind != ldElem {
				line = append(line, node)
				continue
			}
			tag := strings.ToLower(node.tagName())
			if tag == "br" {
				line = append(line, node)
				endLine()
			} else if gmailInlineTags[tag] {
				if splits[node] {
					walk(node)
				} else {
					line = append(line, node)
				}
			} else {
				endLine()
				walk(node)
				endLine()
			}
		}
	}
	walk(root)
	endLine()
	for _, nodes := range lines {
		var text strings.Builder
		for _, n := range nodes {
			text.WriteString(n.textContent())
		}
		if !reGmailQuote.MatchString(text.String()) {
			continue
		}
		for _, n := range nodes {
			if n.parent != nil {
				n.remove()
			}
		}
	}
}

func (e *dfdGmail) getSubject() string {
	if s := extText(ldQuerySelector(e.ctx.doc, "h2.hP")); s != "" {
		return s
	}
	pageTitle := jsTrim(e.ctx.doc.title())
	t := jsTrim(reGmailSubject.ReplaceAllLiteralString(pageTitle, ""))
	if t == "" {
		return "Gmail thread"
	}
	return t
}
