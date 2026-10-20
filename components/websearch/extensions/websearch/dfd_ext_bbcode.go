package websearch

import (
	"regexp"
	"strings"
)

// Port of defuddle 0.19.3 dist/utils/bbcode.js: Steam and forum BBCode to HTML. The original's regular expressions
// are case-insensitive; the tag names are written here as [Bb]-style classes instead of (?i), because Go's
// case-insensitive matching also folds the long s and the Kelvin sign onto s and k, which JavaScript's does not.

func bbTag(name string) string {
	var b strings.Builder
	for _, c := range name {
		if c >= 'a' && c <= 'z' {
			b.WriteString("[" + string(c) + string(c-32) + "]")
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func bbPair(tag string) string {
	return `\[` + bbTag(tag) + `\]([\s\S]*?)\[\/` + bbTag(tag) + `\]`
}

var (
	reBBH1         = jsRE(bbPair("h1"), "")
	reBBH2         = jsRE(bbPair("h2"), "")
	reBBH3         = jsRE(bbPair("h3"), "")
	reBBB          = jsRE(bbPair("b"), "")
	reBBI          = jsRE(bbPair("i"), "")
	reBBU          = jsRE(bbPair("u"), "")
	reBBS          = jsRE(bbPair("s"), "")
	reBBURL        = jsRE(`\[`+bbTag("url")+`=["']?([^"'\]]+)["']?\]([\s\S]*?)\[\/`+bbTag("url")+`\]`, "")
	reBBImg        = jsRE(bbPair("img"), "")
	reBBYouTube    = jsRE(`\[`+bbTag("previewyoutube")+`=["']?([^;'"]+)[^"'\]]*["']?\]\[\/`+bbTag("previewyoutube")+`\]`, "")
	reBBList       = jsRE(bbPair("list"), "")
	reBBOList      = jsRE(bbPair("olist"), "")
	reBBQuote      = jsRE(`\[`+bbTag("quote")+`(?:=[^\]]+)?\]([\s\S]*?)\[\/`+bbTag("quote")+`\]`, "")
	reBBCode       = jsRE(bbPair("code"), "")
	reBBSpoiler    = jsRE(bbPair("spoiler"), "")
	reBBP          = jsRE(bbPair("p"), "")
	reBBUnknownTag = jsRE(`\[[^\]]+\]`, "")
)

func bbWrap(re *regexp.Regexp, s, open, close string) string {
	return jsReplaceAll(re, s, func(m []string) string { return open + m[1] + close })
}

// bbListItems is inner.replace(/\[\*\]([\s\S]*?)(?=\[\*\]|\[\/list\]|$)/gi, '<li>$1</li>'): the lookahead is
// done by hand, closer being "[/list]" or "[/olist]".
func bbListItems(inner, closer string) string {
	lb := []byte(inner)
	for i, c := range lb {
		if c >= 'A' && c <= 'Z' {
			lb[i] = c + 32
		}
	}
	lower := string(lb) // ASCII lowering keeps the offsets of inner
	var b strings.Builder
	i := 0
	for {
		j := strings.Index(lower[i:], "[*]")
		if j < 0 {
			b.WriteString(inner[i:])
			return b.String()
		}
		b.WriteString(inner[i : i+j])
		start := i + j + 3
		end := len(inner)
		for k := start; k <= len(inner); k++ {
			if strings.HasPrefix(lower[k:], "[*]") || strings.HasPrefix(lower[k:], closer) {
				end = k
				break
			}
		}
		b.WriteString("<li>" + inner[start:end] + "</li>")
		i = end
	}
}

// bbcodeToHTML is bbcodeToHtml.
func bbcodeToHTML(bbcode string) string {
	h := bbcode
	h = bbWrap(reBBH1, h, "<h1>", "</h1>")
	h = bbWrap(reBBH2, h, "<h2>", "</h2>")
	h = bbWrap(reBBH3, h, "<h3>", "</h3>")
	h = bbWrap(reBBB, h, "<strong>", "</strong>")
	h = bbWrap(reBBI, h, "<em>", "</em>")
	h = bbWrap(reBBU, h, "<u>", "</u>")
	h = bbWrap(reBBS, h, "<s>", "</s>")
	h = jsReplaceAll(reBBURL, h, func(m []string) string {
		if dfdIsDangerousURL(m[1], true) {
			return m[2]
		}
		return `<a href="` + m[1] + `">` + m[2] + `</a>`
	})
	h = jsReplaceAll(reBBImg, h, func(m []string) string { return `<img src="` + m[1] + `">` })
	h = jsReplaceAll(reBBYouTube, h, func(m []string) string { return `<img src="https://www.youtube.com/watch?v=` + m[1] + `">` })
	h = jsReplaceAll(reBBList, h, func(m []string) string { return "<ul>" + bbListItems(m[1], "[/list]") + "</ul>" })
	h = jsReplaceAll(reBBOList, h, func(m []string) string { return "<ol>" + bbListItems(m[1], "[/olist]") + "</ol>" })
	h = bbWrap(reBBQuote, h, "<blockquote>", "</blockquote>")
	h = bbWrap(reBBCode, h, "<pre><code>", "</code></pre>")
	h = jsReplaceAll(reBBSpoiler, h, func(m []string) string { return "<details><summary>Spoiler</summary>" + m[1] + "</details>" })
	h = jsReplaceAll(reBBP, h, func(m []string) string { return "<p>" + strings.ReplaceAll(m[1], "\n", "<br>") + "</p>" })
	h = strings.ReplaceAll(h, "\n", "<br>")
	return reBBUnknownTag.ReplaceAllLiteralString(h, "")
}
