package websearch

// Port of defuddle 0.19.3 dist/extractors/medium.js: the article element is cleaned in the live document and the
// ordinary pipeline runs on it through contentSelector.

type dfdMedium struct {
	ctx     dfdExtCtx
	article *ldNode
}

var (
	reMediumDate    = jsRE(`^\w{3}\s+\d{1,2},\s+\d{4}`, "")
	reMediumAgo     = jsRE(`^·\s*\d+\s*\w+\s*ago$`, "")
	reMediumMinRead = jsRE(`^·?\s*\d+\s*min\s*read$`, "")
	reMediumNoise   = jsRE(`^[\d\W]+$`, "")
)

var mediumUIText = map[string]bool{
	"Member-only story": true, "Listen": true, "Share": true, "Top highlight": true, "·": true,
	"Press enter or click to view image in full size": true,
}

func init() {
	dfdRegisterExtractor("medium", func(c dfdExtCtx) dfdExtractor {
		e := &dfdMedium{ctx: c}
		e.article = ldQuerySelector(c.doc, "article.meteredContent")
		if e.article == nil {
			e.article = ldQuerySelector(c.doc, "article")
		}
		return e
	})
}

func (e *dfdMedium) name() string { return "medium" }

func (e *dfdMedium) canExtract() bool {
	if e.article == nil {
		return false
	}
	if e.article.hasClass("meteredContent") {
		return true
	}
	siteName := extAttr(ldQuerySelector(e.ctx.doc, `meta[property="og:site_name"]`), "content")
	appName := extAttr(ldQuerySelector(e.ctx.doc, `meta[property="al:android:app_name"]`), "content")
	return siteName == "Medium" || appName == "Medium"
}

func (e *dfdMedium) extract() dfdExtracted {
	title := e.getTitle()
	subtitle := extText(ldQuerySelector(e.ctx.doc, ".pw-subtitle-paragraph"))
	author := extText(ldQuerySelector(e.ctx.doc, `[data-testid="authorName"]`))
	publication := e.getPublication()
	// Clean before getDescription so it doesn't pick up UI text.
	e.cleanArticle()
	description := subtitle
	if description == "" {
		description = e.getDescription()
	}
	site := publication
	if site == "" {
		site = "Medium"
	}
	return dfdExtracted{
		contentSelector: "article",
		variables:       map[string]string{"title": title, "author": author, "site": site, "description": description},
	}
}

func (e *dfdMedium) cleanArticle() {
	if e.article == nil {
		return
	}
	for _, btn := range ldQuerySelectorAll(e.article, `figure [role="button"]`) {
		btn.replaceWith(btn.childNodes()...)
	}
	for _, el := range ldQuerySelectorAll(e.article, `[role="tooltip"]`) {
		el.removeAttribute("role")
	}
	for _, link := range ldQuerySelectorAll(e.article, `a[href*="medium.com/plans"]`) {
		wrapper := ldClosest(link, "div")
		if wrapper != nil && wrapper != e.article {
			wrapper.remove()
		} else {
			link.remove()
		}
	}
	extRemoveAll(e.article, `[data-testid="post-preview"]`)
	extRemoveAll(e.article, `[data-testid*="Clap"], [data-testid*="Bookmark"], [data-testid*="Share"], [data-testid*="Response"]`)
	extRemoveAll(e.article, `[data-testid="authorPhoto"], [data-testid="authorName"], [data-testid="storyReadTime"]`)
	for _, el := range ldQuerySelectorAll(e.article, "p, span, div") {
		text := extText(el)
		if text == "" {
			continue
		}
		if mediumUIText[text] {
			el.remove()
			continue
		}
		if reMediumDate.MatchString(text) && jsLen(text) < 30 {
			el.remove()
			continue
		}
		if reMediumAgo.MatchString(text) {
			el.remove()
			continue
		}
		if reMediumMinRead.MatchString(text) {
			el.remove()
		}
	}
}

func (e *dfdMedium) getTitle() string {
	if st := ldQuerySelector(e.ctx.doc, `[data-testid="storyTitle"]`); st != nil {
		return extText(st)
	}
	return extText(extQuery(e.article, "h1"))
}

func (e *dfdMedium) getPublication() string {
	siteName := extAttr(ldQuerySelector(e.ctx.doc, `meta[property="og:site_name"]`), "content")
	if siteName != "" && siteName != "Medium" {
		return siteName
	}
	for _, schema := range e.ctx.schema {
		pub, ok := schema.(*jsObj)
		if !ok {
			continue
		}
		p, _ := pub.get("publisher")
		po, ok := p.(*jsObj)
		if !ok {
			continue
		}
		if name, _ := po.get("name"); jsTruthy(name) {
			return jsToString(name)
		}
	}
	return ""
}

func (e *dfdMedium) getDescription() string {
	if e.article == nil {
		return ""
	}
	for _, p := range ldQuerySelectorAll(e.article, "p") {
		text := extText(p)
		if jsLen(text) < 3 || reMediumNoise.MatchString(text) {
			continue
		}
		return jsCollapseWS(jsSlice(text, 0, 140))
	}
	return ""
}
