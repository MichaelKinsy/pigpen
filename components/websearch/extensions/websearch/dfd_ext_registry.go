package websearch

import (
	"regexp"
	"strings"
)

// Port of defuddle 0.19.3 dist/extractor-registry.js and dist/extractors/_base.js.
//
// Only the synchronous path is ported: Defuddle runs here with useAsync false, so findAsyncExtractor,
// findPreferredAsyncExtractor, canExtractAsync, prefersAsync and extractAsync are never called, and nothing on
// the synchronous path reads them (BaseExtractor.fetch is only used by the asynchronous methods of the
// extractors). The extractor options are { includeReplies: 'extractors', language: undefined, fetch: undefined }
// here, so every `includeReplies !== false` test of the extractors is true and is not carried.

// dfdExtractor is what the registry returns: BaseExtractor's synchronous surface. name is
// constructor.name.replace('Extractor', ”).toLowerCase(), the extractorType of the result.
type dfdExtractor interface {
	canExtract() bool
	extract() dfdExtracted
	name() string
}

// dfdExtracted is ExtractorResult: contentHtml, contentSelector and the variables (title, description, author,
// published, site, language, image and the custom ones). A missing variable is the empty string, which the
// original's `variables?.x || metadata.x` treats the same. content and extractedContent are not read by Defuddle.
type dfdExtracted struct {
	content         string
	contentHTML     string
	contentSelector string
	variables       map[string]string
}

// dfdExtCtx is the constructor arguments of an extractor: (document, url, schemaOrgData, options), plus the
// Defuddle instance that asked (the conversation extractors run a nested Defuddle whose error the caller reports).
type dfdExtCtx struct {
	d      *dfdDefuddle
	doc    *ldNode
	url    string
	schema []any
}

type dfdExtCtor func(c dfdExtCtx) dfdExtractor

// dfdExtractorCtors maps an extractor's name() to its constructor; each extractor file registers itself in an init
// function. The order of the registry is the table below, not the registration order.
var dfdExtractorCtors = map[string]dfdExtCtor{}

func dfdRegisterExtractor(name string, ctor dfdExtCtor) { dfdExtractorCtors[name] = ctor }

// dfdExtMapping is { patterns, extractor }: a string pattern matches when the hostname includes it, a regular
// expression when it matches the whole URL.
type dfdExtMapping struct {
	name    string
	domains []string
	res     []*regexp.Regexp
}

func dfdMap(name string, domains []string, res ...string) dfdExtMapping {
	m := dfdExtMapping{name: name, domains: domains}
	for _, r := range res {
		m.res = append(m.res, jsRE(r, ""))
	}
	return m
}

// dfdExtractorMappings is ExtractorRegistry.initialize, in the original's order.
var dfdExtractorMappings = []dfdExtMapping{
	dfdMap("xarticle", []string{"x.com", "twitter.com"}),
	dfdMap("twitter", []string{"twitter.com"}, `\/x\.com\/.*`),
	dfdMap("xoembed", []string{"x.com", "twitter.com"}),
	dfdMap("gmail", []string{"mail.google.com"}),
	dfdMap("reddit", []string{"reddit.com", "old.reddit.com", "new.reddit.com"}, `^https:\/\/[^\/]+\.reddit\.com`),
	dfdMap("youtube", []string{"youtube.com", "youtu.be"}, `youtube\.com\/watch\?v=.*`, `youtu\.be\/.*`),
	dfdMap("bilibili", []string{"bilibili.com"}, `www\.bilibili\.com\/video\/BV[0-9A-Za-z]+`),
	dfdMap("hackernews", []string{"news.ycombinator.com"}),
	dfdMap("chatgpt", nil, `^https?:\/\/chatgpt\.com\/(c|share)\/.*`),
	dfdMap("claude", []string{"claude.ai"}, `^https?:\/\/claude\.ai\/(chat|share)\/.*`),
	dfdMap("grok", nil, `^https?:\/\/grok\.com\/(chat|share)(\/.*)?$`),
	dfdMap("gemini", nil, `^https?:\/\/gemini\.google\.com\/app\/.*`),
	dfdMap("github", []string{"github.com"}, `^https?:\/\/github\.com\/.*`),
	dfdMap("linkedin", []string{"linkedin.com"}),
	dfdMap("threads", []string{"threads.net", "www.threads.com", "threads.com"}),
	dfdMap("bluesky", []string{"bsky.app"}),
	dfdMap("medium", []string{"medium.com"}, `\.medium\.com`),
	dfdMap("c2wiki", []string{"wiki.c2.com"}),
	dfdMap("substack", []string{"substack.com"}, `^https?:\/\/substack\.com\/@[^/]+\/note\/.+`, `^https?:\/\/substack\.com\/home\/post\/p-\d+`),
	dfdMap("nytimes", []string{"nytimes.com"}),
	dfdMap("wikipedia", []string{"wikipedia.org"}),
	dfdMap("mastodon", nil, `\/@[^/]+\/\d+`),
	dfdMap("discourse", nil, `\/t\/[^/]+\/\d+`),
	dfdMap("leetcode", []string{"leetcode.com"}),
	dfdMap("lwn", []string{"lwn.net"}),
	dfdMap("bbcodedata", nil, `.*`),
}

func (m *dfdExtMapping) matches(domain, url string) bool {
	for _, p := range m.domains {
		if strings.Contains(domain, p) {
			return true
		}
	}
	for _, re := range m.res {
		if re.MatchString(url) {
			return true
		}
	}
	return false
}

// dfdFindExtractor is ExtractorRegistry.findExtractor: the first mapping whose pattern matches the URL and whose
// extractor accepts the page. An invalid URL, or an exception of a constructor or of canExtract, gives null (the
// original logs it).
//
// Patterns are tried in order, the string patterns of a mapping before its expressions: the outcome is the same
// as the original's `patterns.some`, which only tests whether one of them matches.
func dfdFindExtractor(d *dfdDefuddle, url string, schema []any) (found dfdExtractor) {
	u, ok := newURL(url, "")
	if !ok {
		return nil
	}
	domain := u.hostname()
	if th := ldTry(func() {
		for i := range dfdExtractorMappings {
			m := &dfdExtractorMappings[i]
			if !m.matches(domain, url) {
				continue
			}
			ctor := dfdExtractorCtors[m.name]
			if ctor == nil {
				continue
			}
			inst := ctor(dfdExtCtx{d: d, doc: d.doc, url: url, schema: schema})
			if inst.canExtract() {
				found = inst
				return
			}
		}
	}); th != nil {
		return nil
	}
	return found
}

// mergeExtractorResult is the return of parseInternal for an extractor that names a contentSelector: the result of
// the pipeline run on that selector, with the extractor's variables over its metadata (image stays the page's).
func (d *dfdDefuddle) mergeExtractorResult(pipeline dfdResult, e dfdExtracted, ext dfdExtractor) dfdResult {
	pipeline.meta = dfdApplyVariables(pipeline.meta, e.variables)
	pipeline.extractorType = ext.name()
	return pipeline
}

// buildExtractorResponse is Defuddle.buildExtractorResponse: the extractor's HTML, sanitized, with the
// extractor's variables over the page metadata.
func (d *dfdDefuddle) buildExtractorResponse(e dfdExtracted, m dfdMetadata, ext dfdExtractor) dfdResult {
	content := d.sanitizeExtractorHTML(e.contentHTML)
	return dfdResult{
		content:       content,
		meta:          dfdApplyVariables(m, e.variables),
		wordCount:     d.countHTMLWords(content),
		extractorType: ext.name(),
	}
}

// dfdApplyVariables is `extracted.variables?.x || metadata.x` for title, description, language, published, author
// and site.
func dfdApplyVariables(m dfdMetadata, v map[string]string) dfdMetadata {
	pick := func(key string, fallback string) string {
		if s := v[key]; s != "" {
			return s
		}
		return fallback
	}
	m.title = pick("title", m.title)
	m.description = pick("description", m.description)
	m.language = pick("language", m.language)
	m.published = pick("published", m.published)
	m.author = pick("author", m.author)
	m.site = pick("site", m.site)
	return m
}
