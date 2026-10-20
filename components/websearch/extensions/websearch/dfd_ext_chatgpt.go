package websearch

import (
	"strconv"
	"strings"
)

// Port of defuddle 0.19.3 dist/extractors/chatgpt.js: chatgpt.com conversations.

type dfdChatGPT struct {
	ctx           dfdExtCtx
	turns         []*ldNode
	cached        []dfdMessage
	haveCache     bool
	footnotes     []dfdConvFootnote
	footnoteCount int
}

const chatgptTurnSelector = `[data-testid^="conversation-turn-"]`

var (
	reChatGPTAuthorColon = jsRE(`:\s*$`, "")
	reChatGPTEmptyP      = jsRE(`<p[^>]*>\s*<\/p>`, "")
	reHostWWW            = jsRE(`^www\.`, "")
	reCommaEnc           = jsRE(`%2C`, "")
)

func init() {
	dfdRegisterExtractor("chatgpt", func(c dfdExtCtx) dfdExtractor {
		return &dfdChatGPT{ctx: c, turns: ldQuerySelectorAll(c.doc, chatgptTurnSelector)}
	})
}

func (e *dfdChatGPT) name() string { return "chatgpt" }

func (e *dfdChatGPT) canExtract() bool { return len(e.turns) > 0 }

func (e *dfdChatGPT) extract() dfdExtracted { return dfdConversationExtract(e.ctx, e) }

func (e *dfdChatGPT) getFootnotes() []dfdConvFootnote { return e.footnotes }

// asciiFoldPrefix reports whether s[i:] starts with the lower-case ASCII literal p, ignoring ASCII case (the i
// flag of a JavaScript expression without the u flag folds nothing else).
func asciiFoldPrefix(s string, i int, p string) bool {
	if i+len(p) > len(s) {
		return false
	}
	for k := 0; k < len(p); k++ {
		c := s[i+k]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		if c != p[k] {
			return false
		}
	}
	return true
}

// skipJSSpace returns the index after the JavaScript whitespace that starts at i.
func skipJSSpace(s string, i int) int {
	for i < len(s) {
		r, size := decodeRune(s[i:])
		if !jsSpace(r) {
			break
		}
		i += size
	}
	return i
}

// foldIndex is the index of the first occurrence of the lower-case ASCII literal p in s[from:end], ignoring ASCII
// case, or -1.
func foldIndex(s string, from, end int, p string) int {
	for i := from; i+len(p) <= end; i++ {
		if asciiFoldPrefix(s, i, p) {
			return i
		}
	}
	return -1
}

// matchCitation is the regular expression
//
//	(&ZeroWidthSpace;)?(<span[^>]*?>\s*(?:<span[^>]*?>\s*)*<a(?=[^>]*?href="([^"]+)")(?=[^>]*?target="_blank")
//	(?=[^>]*?rel="noopener")[^>]*?>[\s\S]*?<\/a>\s*(?:<\/span>\s*)+)
//
// (flags gi) tried at one position of s: the end of the match and the href captured by the first lookahead, or
// ok false. RE2 has no lookahead, so the backtracking is spelled out: every quantified part has one way to match
// except the lazy [\s\S]*? before </a>, which moves on to the next </a> when no </span> follows.
func matchCitation(s string, i int) (end int, href string, ok bool) {
	if asciiFoldPrefix(s, i, "&zerowidthspace;") {
		i += len("&ZeroWidthSpace;")
	}
	if !asciiFoldPrefix(s, i, "<span") {
		return 0, "", false
	}
	// <span[^>]*?>: up to the first '>'
	gt := strings.IndexByte(s[i:], '>')
	if gt < 0 {
		return 0, "", false
	}
	i += gt + 1
	i = skipJSSpace(s, i)
	for asciiFoldPrefix(s, i, "<span") {
		gt := strings.IndexByte(s[i:], '>')
		if gt < 0 {
			return 0, "", false
		}
		i += gt + 1
		i = skipJSSpace(s, i)
	}
	if !asciiFoldPrefix(s, i, "<a") {
		return 0, "", false
	}
	i += 2
	// The tag head: [^>]* up to the first '>'.
	headEnd := strings.IndexByte(s[i:], '>')
	if headEnd < 0 {
		return 0, "", false
	}
	headEnd += i
	href = ""
	found := false
	for from := i; ; {
		p := foldIndex(s, from, headEnd, `href="`)
		if p < 0 {
			break
		}
		v := p + len(`href="`)
		q := strings.IndexByte(s[v:], '"')
		if q > 0 {
			href = s[v : v+q]
			found = true
			break
		}
		from = p + 1
	}
	if !found || foldIndex(s, i, headEnd, `target="_blank"`) < 0 || foldIndex(s, i, headEnd, `rel="noopener"`) < 0 {
		return 0, "", false
	}
	pos := headEnd + 1
	for {
		c := foldIndex(s, pos, len(s), "</a>")
		if c < 0 {
			return 0, "", false
		}
		j := skipJSSpace(s, c+4)
		if asciiFoldPrefix(s, j, "</span>") {
			for asciiFoldPrefix(s, j, "</span>") {
				j = skipJSSpace(s, j+len("</span>"))
			}
			return j, href, true
		}
		pos = c + 1
	}
}

func (e *dfdChatGPT) replaceCitations(content string) string {
	var b strings.Builder
	last := 0
	for i := 0; i < len(content); {
		if content[i] != '&' && content[i] != '<' {
			i++
			continue
		}
		end, url, ok := matchCitation(content, i)
		if !ok {
			i++
			continue
		}
		b.WriteString(content[last:i])
		b.WriteString(e.footnoteRef(url))
		i = end
		last = end
	}
	b.WriteString(content[last:])
	return b.String()
}

func (e *dfdChatGPT) footnoteRef(url string) string {
	domain, fragment := "", ""
	if u, ok := newURL(url, ""); ok {
		domain = reHostWWW.ReplaceAllLiteralString(u.hostname(), "")
		parts := strings.Split(url, "#:~:text=")
		failed := false
		if len(parts) > 1 {
			dec, ok := jsDecodeURIComponent(parts[1])
			if !ok {
				failed = true
			} else {
				fragment = reCommaEnc.ReplaceAllLiteralString(dec, ",")
				segs := strings.Split(fragment, ",")
				switch {
				case len(segs) > 1 && jsTrim(segs[0]) != "":
					fragment = " — " + jsTrim(segs[0]) + "..."
				case jsTrim(segs[0]) != "":
					fragment = " — " + jsTrim(fragment)
				default:
					fragment = ""
				}
			}
		}
		if failed {
			domain, fragment = url, ""
		}
	} else {
		domain = url
	}
	idx := -1
	for i, f := range e.footnotes {
		if f.url == url {
			idx = i
			break
		}
	}
	var n int
	if idx == -1 {
		e.footnoteCount++
		n = e.footnoteCount
		e.footnotes = append(e.footnotes, dfdConvFootnote{
			url:  url,
			text: `<a href="` + dfdEscapeHTML(url) + `">` + dfdEscapeHTML(domain) + `</a>` + dfdEscapeHTML(fragment),
		})
	} else {
		n = idx + 1
	}
	ns := strconv.Itoa(n)
	return `<sup id="fnref:` + ns + `"><a href="#fn:` + ns + `">` + ns + `</a></sup>`
}

func (e *dfdChatGPT) extractMessages() []dfdMessage {
	if e.haveCache {
		return e.cached
	}
	messages := []dfdMessage{}
	e.footnotes = nil
	e.footnoteCount = 0
	for _, turn := range e.turns {
		authorEl := ldQuerySelector(turn, "h4.sr-only, h5.sr-only, h6.sr-only")
		authorText := ""
		if authorEl != nil {
			authorText = reChatGPTAuthorColon.ReplaceAllLiteralString(jsTrim(authorEl.textContent()), "")
		}
		var messageEls []*ldNode
		for _, el := range ldQuerySelectorAll(turn, "[data-message-author-role]") {
			if ldClosest(el, chatgptTurnSelector) == turn {
				messageEls = append(messageEls, el)
			}
		}
		role := ""
		if len(messageEls) > 0 {
			role = messageEls[0].attr("data-message-author-role")
		}
		var parts []string
		for _, el := range messageEls {
			content := e.getMessageContentElements(el)
			if len(content) > 0 {
				for _, c := range content {
					parts = append(parts, extSerialize(c))
				}
			} else {
				parts = append(parts, extSerialize(el))
			}
		}
		if len(parts) == 0 {
			parts = []string{extSerialize(turn)}
		}
		messageContent := strings.ReplaceAll(strings.Join(parts, "\n"), "\u200B", "")

		tmp := ldNewElement("div")
		tmp.appendChild(ldParseFragment(messageContent))
		extRemoveAll(tmp, "h4.sr-only, h5.sr-only, h6.sr-only")
		messageContent = extSerialize(tmp)

		messageContent = e.replaceCitations(messageContent)

		cleanup := ldNewElement("div")
		cleanup.appendChild(ldParseFragment(messageContent))
		extRemoveAll(cleanup, `span[data-state="closed"]`)
		messageContent = extSerialize(cleanup)
		messageContent = reChatGPTEmptyP.ReplaceAllLiteralString(messageContent, "")

		if role == "" {
			role = "unknown"
		}
		messages = append(messages, dfdMessage{
			author:   authorText,
			content:  jsTrim(messageContent),
			metadata: [][2]string{{"role", role}},
		})
	}
	e.cached = messages
	e.haveCache = true
	return messages
}

func (e *dfdChatGPT) getMessageContentElements(messageEl *ldNode) []*ldNode {
	const sel = ".markdown, .whitespace-pre-wrap"
	var candidates []*ldNode
	if ldMatches(messageEl, sel) {
		candidates = append(candidates, messageEl)
	}
	candidates = append(candidates, ldQuerySelectorAll(messageEl, sel)...)
	var out []*ldNode
	for _, c := range candidates {
		nested := false
		for _, o := range candidates {
			if o != c && o.contains(c) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, c)
		}
	}
	return out
}

func (e *dfdChatGPT) getMetadata() dfdConvMeta {
	title := e.getTitle()
	messages := e.extractMessages()
	return dfdConvMeta{
		title:       title,
		site:        "ChatGPT",
		description: "ChatGPT conversation with " + strconv.Itoa(len(messages)) + " messages",
	}
}

func (e *dfdChatGPT) getTitle() string {
	pageTitle := jsTrim(e.ctx.doc.title())
	if pageTitle != "" && pageTitle != "ChatGPT" {
		return pageTitle
	}
	if len(e.turns) > 0 {
		if first := ldQuerySelector(e.turns[0], ".text-message"); first != nil {
			return extTruncate50(first.textContent())
		}
	}
	return "ChatGPT Conversation"
}
