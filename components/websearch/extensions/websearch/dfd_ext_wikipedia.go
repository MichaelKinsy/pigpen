package websearch

// Port of defuddle 0.19.3 dist/extractors/wikipedia.js: the article body is the content, found by the ordinary
// pipeline through contentSelector.

type dfdWikipedia struct{ ctx dfdExtCtx }

var reWikipediaSuffix = jsRE(`\s*[-–—]\s*Wikipedia\s*$`, "")

func init() {
	dfdRegisterExtractor("wikipedia", func(c dfdExtCtx) dfdExtractor { return &dfdWikipedia{ctx: c} })
}

func (e *dfdWikipedia) name() string { return "wikipedia" }

func (e *dfdWikipedia) canExtract() bool {
	return ldQuerySelector(e.ctx.doc, "#mw-content-text") != nil
}

func (e *dfdWikipedia) extract() dfdExtracted {
	ogTitle := extAttr(ldQuerySelector(e.ctx.doc, `meta[property="og:title"]`), "content")
	title := reWikipediaSuffix.ReplaceAllLiteralString(ogTitle, "")
	if title == "" {
		title = ogTitle
	}
	return dfdExtracted{
		contentSelector: "#mw-content-text",
		variables:       map[string]string{"title": title, "author": "Wikipedia", "site": "Wikipedia"},
	}
}
