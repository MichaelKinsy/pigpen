package websearch

import (
	"errors"
)

// The Defuddle fallback of extract.ts (extractWithDefuddle: a linkedom document, Defuddle with markdown: true and
// useAsync: false). The port is dfd_*.go and ldom*.go; see port/PORT.md.

type defuddleResult struct{ Title, Content string }

// extractWithDefuddle parses the page, runs the pipeline and returns the Markdown and the title. An exception of the
// original is returned as an error with the same message.
func extractWithDefuddle(text, url string) (res *defuddleResult, err error) {
	document := ldParseDocument(text)
	loc, ok := newURL(url, "")
	if !ok {
		return nil, errors.New("Invalid URL")
	}
	d := &dfdDefuddle{doc: document, url: url, location: loc.href()}
	var result dfdResult
	if th := ldTry(func() { result = d.parse() }); th != nil {
		return nil, errors.New(th.msg)
	}
	if d.processingError != nil {
		return nil, errors.New("Defuddle failed to process document: " + d.processingError.msg)
	}
	return &defuddleResult{Title: result.meta.title, Content: dfdCreateMarkdownContent(result.content)}, nil
}
