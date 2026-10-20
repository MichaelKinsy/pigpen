package websearch

import (
	"strconv"
	"strings"
)

// Port of defuddle 0.19.3 dist/extractors/gemini.js: gemini.google.com conversations.

type dfdGemini struct {
	ctx        dfdExtCtx
	containers []*ldNode
	footnotes  []dfdConvFootnote
	count      int
	haveCount  bool
}

func init() {
	dfdRegisterExtractor("gemini", func(c dfdExtCtx) dfdExtractor {
		return &dfdGemini{ctx: c, containers: ldQuerySelectorAll(c.doc, "div.conversation-container")}
	})
}

func (e *dfdGemini) name() string { return "gemini" }

func (e *dfdGemini) canExtract() bool { return len(e.containers) > 0 }

func (e *dfdGemini) extract() dfdExtracted { return dfdConversationExtract(e.ctx, e) }

func (e *dfdGemini) getFootnotes() []dfdConvFootnote { return e.footnotes }

func (e *dfdGemini) extractMessages() []dfdMessage {
	e.count = 0
	e.haveCount = true
	var messages []dfdMessage
	e.extractSources()
	for _, container := range e.containers {
		if uq := ldQuerySelector(container, "user-query"); uq != nil {
			if qt := ldQuerySelector(uq, ".query-text"); qt != nil {
				messages = append(messages, dfdMessage{author: "You", content: jsTrim(extSerialize(qt)), metadata: [][2]string{{"role", "user"}}})
			}
		}
		if mr := ldQuerySelector(container, "model-response"); mr != nil {
			regular := ldQuerySelector(mr, ".model-response-text .markdown")
			extended := ldQuerySelector(mr, "#extended-response-markdown-content")
			el := extended
			if el == nil {
				el = regular
			}
			if el != nil {
				tmp := ldNewElement("div")
				tmp.appendChild(ldParseFragment(extSerialize(el)))
				for _, t := range ldQuerySelectorAll(tmp, ".table-content") {
					t.classListRemove("table-content")
				}
				messages = append(messages, dfdMessage{author: "Gemini", content: jsTrim(extSerialize(tmp)), metadata: [][2]string{{"role", "assistant"}}})
			}
		}
	}
	e.count = len(messages)
	return messages
}

// extractSources is the footnotes of the browse-item elements. The original tests `link instanceof
// HTMLAnchorElement`, and Node has no such global: with linkedom the expression throws a ReferenceError for every
// browse-item, which the pipeline reports as its processing error. The rest of the loop is unreachable.
func (e *dfdGemini) extractSources() {
	if len(ldQuerySelectorAll(e.ctx.doc, "browse-item")) > 0 {
		panic(&jsThrow{name: "ReferenceError", msg: "HTMLAnchorElement is not defined"})
	}
}

func (e *dfdGemini) getMetadata() dfdConvMeta {
	title := e.getTitle()
	count := e.count
	if !e.haveCount {
		count = len(e.extractMessages())
	}
	return dfdConvMeta{title: title, site: "Gemini", description: "Gemini conversation with " + strconv.Itoa(count) + " messages"}
}

func (e *dfdGemini) getTitle() string {
	pageTitle := jsTrim(e.ctx.doc.title())
	if pageTitle != "" && pageTitle != "Gemini" && !strings.Contains(pageTitle, "Gemini") {
		return pageTitle
	}
	if t := extText(ldQuerySelector(e.ctx.doc, ".title-text")); t != "" {
		return t
	}
	if len(e.containers) > 0 {
		if q := ldQuerySelector(e.containers[0], ".query-text"); q != nil {
			return extTruncate50(q.textContent())
		}
	}
	return "Gemini Conversation"
}
