package websearch

// Port of defuddle 0.19.3 dist/extractors/leetcode.js: the problem statement is the content, found by the
// ordinary pipeline through contentSelector.

type dfdLeetCode struct{ ctx dfdExtCtx }

var reLeetCodeSuffix = jsRE(`\s*[-–—]\s*LeetCode\s*$`, "")

func init() {
	dfdRegisterExtractor("leetcode", func(c dfdExtCtx) dfdExtractor { return &dfdLeetCode{ctx: c} })
}

func (e *dfdLeetCode) name() string { return "leetcode" }

func (e *dfdLeetCode) canExtract() bool {
	return ldQuerySelector(e.ctx.doc, `[data-track-load="description_content"]`) != nil
}

func (e *dfdLeetCode) extract() dfdExtracted {
	ogTitle := extAttr(ldQuerySelector(e.ctx.doc, `meta[property="og:title"]`), "content")
	title := reLeetCodeSuffix.ReplaceAllLiteralString(ogTitle, "")
	if title == "" {
		title = ogTitle
	}
	return dfdExtracted{
		contentSelector: `[data-track-load="description_content"]`,
		variables:       map[string]string{"title": title, "site": "LeetCode"},
	}
}
