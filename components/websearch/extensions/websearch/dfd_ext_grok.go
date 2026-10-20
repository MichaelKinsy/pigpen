package websearch

import (
	"strconv"
	"strings"
)

// Port of defuddle 0.19.3 dist/extractors/grok.js: grok.com conversations.

type dfdGrok struct {
	ctx           dfdExtCtx
	bubbles       []*ldNode
	footnotes     []dfdConvFootnote
	footnoteCount int
}

const grokContainerSelector = ".relative.group.flex.flex-col.justify-center.w-full"

var (
	reGrokSuffix = jsRE(`\s-\s*Grok$`, "")
	reGrokLink   = jsRE(`<a\s+(?:[^>]*?\s+)?href="([^"]*)"[^>]*>(.*?)<\/a>`, "i")
	reHTTPURL    = jsRE(`^https?:\/\/`, "i")
)

func init() {
	dfdRegisterExtractor("grok", func(c dfdExtCtx) dfdExtractor {
		return &dfdGrok{ctx: c, bubbles: ldQuerySelectorAll(c.doc, grokContainerSelector)}
	})
}

func (e *dfdGrok) name() string { return "grok" }

func (e *dfdGrok) canExtract() bool { return len(e.bubbles) > 0 }

func (e *dfdGrok) extract() dfdExtracted { return dfdConversationExtract(e.ctx, e) }

func (e *dfdGrok) getFootnotes() []dfdConvFootnote { return e.footnotes }

func (e *dfdGrok) extractMessages() []dfdMessage {
	var messages []dfdMessage
	e.footnotes = nil
	e.footnoteCount = 0
	for _, container := range e.bubbles {
		isUser := container.hasClass("items-end")
		isGrok := container.hasClass("items-start")
		if !isUser && !isGrok {
			continue
		}
		bubble := ldQuerySelector(container, ".message-bubble")
		if bubble == nil {
			continue
		}
		var content, role, author string
		if isUser {
			content = bubble.textContent()
			role, author = "user", "You"
		} else {
			role, author = "assistant", "Grok"
			cloned := bubble.cloneNode(true)
			if art := ldQuerySelector(cloned, ".relative.border.border-border-l1.bg-surface-base"); art != nil {
				art.remove()
			}
			content = e.processFootnotes(extSerialize(cloned))
		}
		if jsTrim(content) != "" {
			messages = append(messages, dfdMessage{author: author, content: jsTrim(content), metadata: [][2]string{{"role", role}}})
		}
	}
	return messages
}

func (e *dfdGrok) getMetadata() dfdConvMeta {
	title := e.getTitle()
	n := len(e.bubbles)
	return dfdConvMeta{title: title, site: "Grok", description: "Grok conversation with " + strconv.Itoa(n) + " messages"}
}

func (e *dfdGrok) getTitle() string {
	pageTitle := jsTrim(e.ctx.doc.title())
	if pageTitle != "" && pageTitle != "Grok" && !strings.HasPrefix(pageTitle, "Grok by ") {
		return jsTrim(reGrokSuffix.ReplaceAllLiteralString(pageTitle, ""))
	}
	if c := ldQuerySelector(e.ctx.doc, grokContainerSelector+".items-end"); c != nil {
		if b := ldQuerySelector(c, ".message-bubble"); b != nil {
			return extTruncate50(extText(b))
		}
	}
	return "Grok Conversation"
}

func (e *dfdGrok) processFootnotes(content string) string {
	return jsReplaceAll(reGrokLink, content, func(m []string) string {
		url, linkText := m[1], m[2]
		if url == "" || strings.HasPrefix(url, "#") || !reHTTPURL.MatchString(url) {
			return m[0]
		}
		idx := -1
		for i, f := range e.footnotes {
			if f.url == url {
				idx = i
				break
			}
		}
		var n int
		if idx < 0 {
			e.footnoteCount++
			n = e.footnoteCount
			var text string
			if u, ok := newURL(url, ""); ok {
				text = `<a href="` + dfdEscapeHTML(url) + `" target="_blank" rel="noopener noreferrer">` + dfdEscapeHTML(reHostWWW.ReplaceAllLiteralString(u.hostname(), "")) + `</a>`
			} else {
				text = `<a href="` + dfdEscapeHTML(url) + `" target="_blank" rel="noopener noreferrer">` + dfdEscapeHTML(url) + `</a>`
			}
			e.footnotes = append(e.footnotes, dfdConvFootnote{url: url, text: text})
		} else {
			n = idx + 1
		}
		ns := strconv.Itoa(n)
		return linkText + `<sup id="fnref:` + ns + `" class="footnote-ref"><a href="#fn:` + ns + `" class="footnote-link">` + ns + `</a></sup>`
	})
}
