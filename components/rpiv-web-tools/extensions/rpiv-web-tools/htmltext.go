package rpiv_web_tools

import "strings"

// HTML to text in linear scans. The original does this with regular expressions
// (providers/fetch-helpers.ts:75-82); Go's regexp is a backtracking-free engine that costs about 2.5 ms on a 20 KB
// page, six times Node's, so each expression is a scan with the same matches: the passes run in the same order and
// each replaces exactly what its expression matched. htmltext_reference_test.go keeps the expressions and compares
// the two on generated pages. Names are matched in ASCII case only, as JavaScript's `i` flag does without `u`
// (Go's (?i) would also fold U+017F and U+212A onto `s` and `k`).

// foldPrefix reports whether s[i:] starts with w, a lower-case ASCII word, ignoring ASCII case.
func foldPrefix(s string, i int, w string) bool {
	if len(s)-i < len(w) {
		return false
	}
	for k := range len(w) {
		c := s[i+k]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != w[k] {
			return false
		}
	}
	return true
}

// indexFold is the first index at or after from where s has w (which starts with '<') in any ASCII case, or -1.
func indexFold(s string, from int, w string) int {
	for i := from; i < len(s); {
		j := strings.IndexByte(s[i:], '<')
		if j < 0 {
			return -1
		}
		i += j
		if foldPrefix(s, i, w) {
			return i
		}
		i++
	}
	return -1
}

// removeBlocks drops every open...closer span, the closer being the first one after the opening word:
// `(?is)<script.*?</script>`. An opening with no closer after it matches nothing, and neither does any later one.
func removeBlocks(s, open, closer string) string {
	var b strings.Builder
	pos := 0
	for i := 0; ; {
		start := indexFold(s, i, open)
		if start < 0 {
			break
		}
		end := indexFold(s, start+len(open), closer)
		if end < 0 {
			break
		}
		if b.Len() == 0 {
			b.Grow(len(s))
		}
		b.WriteString(s[pos:start])
		pos = end + len(closer)
		i = pos
	}
	if pos == 0 {
		return s
	}
	b.WriteString(s[pos:])
	return b.String()
}

// blockNames are the closing tags that end a line: `</(p|div|h[1-6]|li|tr|br|blockquote|pre|section|article|header|footer|nav|details|summary)>`.
var blockNames = [...]string{"p", "div", "h1", "h2", "h3", "h4", "h5", "h6", "li", "tr", "br", "blockquote", "pre", "section", "article", "header", "footer", "nav", "details", "summary"}

func isBlockName(name string) bool {
	for _, n := range blockNames {
		if len(n) == len(name) && foldPrefix(name, 0, n) {
			return true
		}
	}
	return false
}

func isASCIIAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// replaceMatches rewrites every span that match reports (its length at i, 0 for none) with repl, scanning left to right.
func replaceMatches(s, repl string, match func(s string, i int) int) string {
	var b strings.Builder
	pos := 0
	for i := 0; i < len(s); {
		j := strings.IndexByte(s[i:], '<')
		if j < 0 {
			break
		}
		i += j
		n := match(s, i)
		if n == 0 {
			i++
			continue
		}
		if b.Len() == 0 {
			b.Grow(len(s))
		}
		b.WriteString(s[pos:i])
		b.WriteString(repl)
		i += n
		pos = i
	}
	if pos == 0 {
		return s
	}
	b.WriteString(s[pos:])
	return b.String()
}

// blockCloserAt is the length of a block-level closing tag at s[i], 0 when there is none.
func blockCloserAt(s string, i int) int {
	if i+1 >= len(s) || s[i+1] != '/' {
		return 0
	}
	j := i + 2
	for j < len(s) && isASCIIAlnum(s[j]) {
		j++
	}
	if j < len(s) && s[j] == '>' && isBlockName(s[i+2:j]) {
		return j + 1 - i
	}
	return 0
}

// breakAt is the length of `<br\s*/?>` at s[i] (\s as Go's regexp has it), 0 when there is none.
func breakAt(s string, i int) int {
	if !foldPrefix(s, i, "<br") {
		return 0
	}
	j := i + 3
	for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\f' || s[j] == '\r') {
		j++
	}
	if j < len(s) && s[j] == '/' {
		j++
	}
	if j < len(s) && s[j] == '>' {
		return j + 1 - i
	}
	return 0
}

// anyTagAt is the length of `<[^>]+>` at s[i], 0 when there is none: a '<', at least one other character, the first '>'.
func anyTagAt(s string, i int) int {
	j := strings.IndexByte(s[i+1:], '>')
	if j <= 0 {
		return 0
	}
	return j + 2
}

// stripTags replaces every `<[^>]+>` with repl. Without a '>' after a '<' nothing later can match either.
func stripTags(s, repl string) string {
	return replaceMatches(s, repl, anyTagAt)
}

// collapseWhitespace turns each run of spaces and tabs into one space and each run of three or more newlines into two.
func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		j := i + 1
		switch c {
		case ' ', '\t':
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			b.WriteByte(' ')
		case '\n':
			for j < len(s) && s[j] == '\n' {
				j++
			}
			b.WriteString(s[i:min(j, i+2)])
		default:
			for j < len(s) && s[j] != ' ' && s[j] != '\t' && s[j] != '\n' {
				j++
			}
			b.WriteString(s[i:j])
		}
		i = j
	}
	return b.String()
}

// htmlToText strips scripts and styles, turns block closers and <br> into newlines, drops the other tags,
// decodes the common entities and collapses whitespace. upstream: fetch-helpers.ts:75-82.
func htmlToText(html string) string {
	text := removeBlocks(html, "<script", "</script>")
	text = removeBlocks(text, "<style", "</style>")
	text = removeBlocks(text, "<noscript", "</noscript>")
	text = replaceMatches(text, "\n", blockCloserAt)
	text = replaceMatches(text, "\n", breakAt)
	text = stripTags(text, " ")
	text = decodeHTMLEntities(text)
	return jsTrim(collapseWhitespace(text))
}

// extractTitle is the text of the first <title>, "" when there is none or it is empty:
// `(?is)<title[^>]*>(.*?)</title>`, its tags dropped.
func extractTitle(html string) string {
	start := indexFold(html, 0, "<title")
	if start < 0 {
		return ""
	}
	gt := strings.IndexByte(html[start+len("<title"):], '>')
	if gt < 0 {
		return ""
	}
	from := start + len("<title") + gt + 1
	end := indexFold(html, from, "</title>")
	if end < 0 {
		return ""
	}
	return jsTrim(stripTags(html[from:end], ""))
}
