package websearch

import (
	"strconv"
	"strings"
	"unicode"
)

// Port of defuddle 0.19.3 dist/extractors/_conversation.js: the base of the chat extractors (ChatGPT, Claude,
// Gemini, Grok). A conversation is rendered to HTML, put in a document of its own and run through a nested
// Defuddle, whose output is the extractor's contentHtml.

// dfdMessage is ConversationMessage. metadata keeps the order of the original's object entries.
type dfdMessage struct {
	author    string
	content   string
	timestamp string
	metadata  [][2]string
}

// dfdConvFootnote is Footnote; text is raw HTML.
type dfdConvFootnote struct {
	url  string
	text string
}

// dfdConvMeta is ConversationMetadata (title, site, description; the others are not read).
type dfdConvMeta struct {
	title       string
	site        string
	description string
}

// dfdConversation is what a subclass of ConversationExtractor supplies.
type dfdConversation interface {
	extractMessages() []dfdMessage
	getMetadata() dfdConvMeta
	getFootnotes() []dfdConvFootnote
}

// dfdConversationExtract is ConversationExtractor.extract.
func dfdConversationExtract(c dfdExtCtx, impl dfdConversation) dfdExtracted {
	messages := impl.extractMessages()
	meta := impl.getMetadata()
	footnotes := impl.getFootnotes()
	raw := dfdConversationHTML(messages, footnotes)

	// The temporary document: DOMParser().parseFromString('<!doctype html><html><body></body></html>'), the
	// only way linkedom's document offers (it has no implementation.createHTMLDocument).
	tempDoc := ldParseDocument("<!doctype html><html><body></body></html>")
	container := ldNewElement("article")
	container.appendChild(ldParseFragment(raw))
	tempDoc.body().appendChild(container)

	nested := &dfdDefuddle{doc: tempDoc, url: "about:blank"}
	defuddled := nested.parse()
	// The original's console hook records the error of every Defuddle run, the nested one included.
	if nested.processingError != nil {
		c.d.processingError = nested.processingError
	}
	title := meta.title
	if title == "" {
		title = "Conversation"
	}
	description := meta.description
	if description == "" {
		description = meta.site + " conversation with " + strconv.Itoa(len(messages)) + " messages"
	}
	return dfdExtracted{
		content:     defuddled.content,
		contentHTML: defuddled.content,
		variables: map[string]string{
			"title":       title,
			"site":        meta.site,
			"description": description,
			"wordCount":   strconv.Itoa(defuddled.wordCount),
		},
	}
}

var (
	reHasParagraphs = jsRE(`<p[^>]*>[\s\S]*?<\/p>`, "i")
	reDataKey       = jsRE(`^[a-z][a-z0-9-]*$`, "i")
)

// dfdConversationHTML is ConversationExtractor.createContentHtml.
func dfdConversationHTML(messages []dfdMessage, footnotes []dfdConvFootnote) string {
	parts := make([]string, len(messages))
	for index, m := range messages {
		timestampHTML := ""
		if m.timestamp != "" {
			timestampHTML = `<div class="message-timestamp">` + dfdEscapeHTML(m.timestamp) + `</div>`
		}
		contentHTML := m.content
		if !reHasParagraphs.MatchString(m.content) {
			contentHTML = "<p>" + m.content + "</p>"
		}
		var attrs []string
		for _, kv := range m.metadata {
			if reDataKey.MatchString(kv[0]) {
				attrs = append(attrs, "data-"+kv[0]+`="`+dfdEscapeHTML(kv[1])+`"`)
			}
		}
		sep := ""
		if index < len(messages)-1 {
			sep = "\n<hr>"
		}
		parts[index] = "\n\t\t\t<div class=\"message message-" + dfdEscapeHTML(jsToLower(m.author)) + "\" " + strings.Join(attrs, " ") + ">\n" +
			"\t\t\t\t<div class=\"message-header\">\n" +
			"\t\t\t\t\t<p class=\"message-author\"><strong>" + dfdEscapeHTML(m.author) + "</strong></p>\n" +
			"\t\t\t\t\t" + timestampHTML + "\n" +
			"\t\t\t\t</div>\n" +
			"\t\t\t\t<div class=\"message-content\">\n" +
			"\t\t\t\t\t" + contentHTML + "\n" +
			"\t\t\t\t</div>\n" +
			"\t\t\t</div>" + sep
	}
	messagesHTML := jsTrim(strings.Join(parts, "\n"))
	footnotesHTML := ""
	if len(footnotes) > 0 {
		var items strings.Builder
		for i, f := range footnotes {
			n := strconv.Itoa(i + 1)
			items.WriteString("\n\t\t\t\t\t\t<li class=\"footnote\" id=\"fn:" + n + "\">\n" +
				"\t\t\t\t\t\t\t<p>\n" +
				"\t\t\t\t\t\t\t\t<a href=\"" + dfdEscapeHTML(f.url) + "\" target=\"_blank\">" + f.text + "</a>&nbsp;<a href=\"#fnref:" + n + "\" class=\"footnote-backref\">↩</a>\n" +
				"\t\t\t\t\t\t\t</p>\n" +
				"\t\t\t\t\t\t</li>\n\t\t\t\t\t")
		}
		footnotesHTML = "\n\t\t\t<div id=\"footnotes\">\n\t\t\t\t<ol>\n\t\t\t\t\t" + items.String() + "\n\t\t\t\t</ol>\n\t\t\t</div>"
	}
	return jsTrim(messagesHTML + "\n" + footnotesHTML)
}

// jsToLower is String.prototype.toLowerCase: Unicode lowercasing with the two context rules Go's ToLower lacks, the
// dotted capital I (U+0130 gives i and a combining dot) and the final sigma.
func jsToLower(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToLower(s)
	}
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		switch {
		case r == 0x130:
			b.WriteString("i\u0307")
		case r == 'Σ':
			if jsFinalSigma(rs, i) {
				b.WriteRune('ς')
			} else {
				b.WriteRune('σ')
			}
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// jsFinalSigma is the Final_Sigma condition: a cased letter before (skipping case-ignorable characters) and none
// after.
func jsFinalSigma(rs []rune, i int) bool {
	before := false
	for j := i - 1; j >= 0; j-- {
		if jsCaseIgnorable(rs[j]) {
			continue
		}
		before = jsCased(rs[j])
		break
	}
	if !before {
		return false
	}
	for j := i + 1; j < len(rs); j++ {
		if jsCaseIgnorable(rs[j]) {
			continue
		}
		return !jsCased(rs[j])
	}
	return true
}

func jsCased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r)
}

func jsCaseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', '^', '`', 0xa8, 0xad, 0xaf, 0xb4, 0xb7, 0xb8, 0x2019:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}
