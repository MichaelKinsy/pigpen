package websearch

// Port of defuddle 0.19.3 dist/extractors/c2-wiki.js, synchronous surface only. The extractor works through
// canExtractAsync and extractAsync (a fetch of the page's JSON from c2.com); its canExtract is the constant false,
// so the synchronous registry never returns it and its extract (an empty result) is never called. Nothing else of
// the file is reachable with useAsync false.

type dfdC2Wiki struct{ ctx dfdExtCtx }

func init() {
	dfdRegisterExtractor("c2wiki", func(c dfdExtCtx) dfdExtractor { return &dfdC2Wiki{ctx: c} })
}

func (e *dfdC2Wiki) name() string { return "c2wiki" }

func (e *dfdC2Wiki) canExtract() bool { return false }

func (e *dfdC2Wiki) extract() dfdExtracted { return dfdExtracted{} }
