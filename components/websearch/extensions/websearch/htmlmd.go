package websearch

import (
	"regexp"

	readability "github.com/go-shiori/go-readability"
	"golang.org/x/net/html"
)

// The HTML branch of extract.ts extractViaHttp: Mozilla Readability picks the article and turndown
// converts it to Markdown. Readability is github.com/go-shiori/go-readability, a line-by-line Go
// port of Readability.js; turndown is the hand port in turndown.go. The fixtures of
// testdata/html, recorded from the original (port/record-html-extract.mjs), pin the output byte
// for byte; what still differs is listed in port/PORT.md.

var wsRE = regexp.MustCompile(`\s+`)

// documentTitle is document.title: the first <title> element's text, trimmed.
func documentTitle(doc *html.Node) string {
	if t := firstElement(doc, func(n *html.Node) bool { return n.Data == "title" }); t != nil {
		return jsTrim(wsRE.ReplaceAllString(textContent(t), " "))
	}
	return ""
}

// readableArticle is `new Readability(document).parse()` followed by turndown on `article.content`:
// the article's title and Markdown, or ok=false where Readability returns null.
func readableArticle(doc *html.Node) (title, markdown string, ok bool) {
	// The original's linkedom document has no base URI, so Readability leaves every URI as written:
	// no page URL is given here either.
	parser := readability.NewParser()
	article, err := parser.ParseDocument(doc, nil)
	if err != nil || article.Content == "" {
		return "", "", false
	}
	return article.Title, turndown(article.Content), true
}
