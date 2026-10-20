package websearch

import "strconv"

// Port of defuddle 0.19.3 dist/extractors/claude.js: claude.ai conversations.

type dfdClaude struct {
	ctx      dfdExtCtx
	articles []*ldNode
}

var (
	reClaudeSuffix = jsRE(` - Claude$`, "")
	reClaudeEmptyP = jsRE(`<p[^>]*>\s*<\/p>`, "")
)

func init() {
	dfdRegisterExtractor("claude", func(c dfdExtCtx) dfdExtractor {
		return &dfdClaude{ctx: c, articles: ldQuerySelectorAll(c.doc,
			`div[data-testid="user-message"], div[data-testid="assistant-message"], div.font-claude-response`)}
	})
}

func (e *dfdClaude) name() string { return "claude" }

func (e *dfdClaude) canExtract() bool { return len(e.articles) > 0 }

func (e *dfdClaude) extract() dfdExtracted { return dfdConversationExtract(e.ctx, e) }

func (e *dfdClaude) getFootnotes() []dfdConvFootnote { return nil }

func (e *dfdClaude) extractMessages() []dfdMessage {
	var messages []dfdMessage
	for _, article := range e.articles {
		var role, content string
		if article.hasAttribute("data-testid") {
			if article.attr("data-testid") == "user-message" {
				role = "you"
				content = extSerialize(article)
			} else {
				continue
			}
		} else if article.hasClass("font-claude-response") {
			role = "assistant"
			body := ldQuerySelector(article, ".standard-markdown")
			if body == nil {
				body = article
			}
			content = extSerialize(body)
		} else {
			continue
		}
		if content == "" {
			continue
		}
		content = reClaudeEmptyP.ReplaceAllLiteralString(jsReplaceStr(jsRE("\u200B", ""), content, ""), "")
		author := "Claude"
		if role == "you" {
			author = "You"
		}
		messages = append(messages, dfdMessage{author: author, content: jsTrim(content), metadata: [][2]string{{"role", role}}})
	}
	return messages
}

func (e *dfdClaude) getMetadata() dfdConvMeta {
	title := e.getTitle()
	messages := e.extractMessages()
	return dfdConvMeta{title: title, site: "Claude", description: "Claude conversation with " + strconv.Itoa(len(messages)) + " messages"}
}

func (e *dfdClaude) getTitle() string {
	pageTitle := jsTrim(e.ctx.doc.title())
	if pageTitle != "" && pageTitle != "Claude" {
		return reClaudeSuffix.ReplaceAllLiteralString(pageTitle, "")
	}
	if h := extText(ldQuerySelector(e.ctx.doc, "header .font-tiempos")); h != "" {
		return h
	}
	if len(e.articles) > 0 {
		if first := ldQuerySelector(e.articles[0], `[data-testid="user-message"]`); first != nil {
			return extTruncate50(first.textContent())
		}
	}
	return "Claude Conversation"
}
