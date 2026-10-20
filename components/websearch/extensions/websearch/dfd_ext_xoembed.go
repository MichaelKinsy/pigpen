package websearch

// Port of defuddle 0.19.3 dist/extractors/x-oembed.js, synchronous surface only. The extractor works through
// canExtractAsync, prefersAsync and extractAsync (FxTwitter and oEmbed fetches); its canExtract is the constant
// false, so the synchronous registry never returns it and its extract (an empty result) is never called. The
// rendering code of the file (renderTweet, renderArticle, facets) is only called from extractAsync.

type dfdXOembed struct{ ctx dfdExtCtx }

func init() {
	dfdRegisterExtractor("xoembed", func(c dfdExtCtx) dfdExtractor { return &dfdXOembed{ctx: c} })
}

func (e *dfdXOembed) name() string { return "xoembed" }

func (e *dfdXOembed) canExtract() bool { return false }

func (e *dfdXOembed) extract() dfdExtracted { return dfdExtracted{} }
