package websearch

import (
	"strings"
)

// Port of defuddle 0.19.3 dist/extractors/substack.js: a Substack post (its body rendered in the page, or found
// in window._preloads and injected into the document by the constructor) or a Note.

const substackInjectedAttr = "data-defuddle-substack-post"

type dfdSubstack struct {
	ctx                 dfdExtCtx
	noteText, noteImage *ldNode
	postData            any
	postContentSelector string
}

var (
	reSubstackHandle = jsRE(`\s*\(@[^)]+\)\s*$`, "")
	reSubstackCase   = jsRE(`([a-z])([A-Z])`, "")
	reSubstackDate   = jsRE(`\b(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+(\d{1,2}),?\s+(\d{4})\b`, "")
	reSubstackSrcset = jsRE(`(.+?)\s+(\d+(?:\.\d+)?)w`, "")
	reSubstackComma  = jsRE(`^,\s*`, "")
	reSubstackW      = jsRE(`,w_\d+`, "")
	reSubstackC      = jsRE(`,c_\w+`, "")
)

var substackMonths = map[string]string{
	"Jan": "01", "Feb": "02", "Mar": "03", "Apr": "04", "May": "05", "Jun": "06",
	"Jul": "07", "Aug": "08", "Sep": "09", "Oct": "10", "Nov": "11", "Dec": "12",
}

func init() {
	dfdRegisterExtractor("substack", func(c dfdExtCtx) dfdExtractor {
		e := &dfdSubstack{ctx: c}
		doc := c.doc
		// A rendered post body (a browser, after hydration): the preloads are read for metadata only.
		if ldQuerySelector(doc, "div.body.markup") != nil {
			e.postData = e.extractPreloadData()
			e.postContentSelector = "div.body.markup"
			return e
		}
		// Otherwise window._preloads (a server-rendered page): body_html is injected for the pipeline.
		e.postData = e.extractPreloadData()
		if bodyHTML := extGet(e.postData, "body_html"); jsTruthy(bodyHTML) {
			if ldQuerySelector(doc, "["+substackInjectedAttr+"]") == nil {
				wrapper := ldNewElement("div")
				wrapper.setAttribute(substackInjectedAttr, "")
				wrapper.appendChild(ldParseFragment(rscString(bodyHTML)))
				body := doc.body()
				if body == nil {
					ldThrowType("Cannot read properties of null (reading 'appendChild')")
				}
				body.appendChild(wrapper)
			}
			e.postContentSelector = "[" + substackInjectedAttr + "]"
			return e
		}
		// Otherwise a Note (the ProseMirror editor div). On a permalink page the main note is inside a
		// feedPermalinkUnit; the other editors belong to feed recommendations.
		scope := ldQuerySelector(doc, `[class*="feedPermalinkUnit"]`)
		if scope == nil {
			scope = doc
		}
		e.noteText = ldQuerySelector(scope, "div.ProseMirror.FeedProseMirror")
		if e.noteText != nil {
			if body := ldClosest(e.noteText, `[class*="feedCommentBody"]:not([class*="feedCommentBodyInner"])`); body != nil {
				// imageGrid is a sibling of feedCommentBody or of its parent wrapper.
				var candidates []*ldNode
				candidates = append(candidates, body.nextElementSibling())
				if pe := body.parentElement(); pe != nil {
					candidates = append(candidates, pe.nextElementSibling())
				} else {
					candidates = append(candidates, nil)
				}
				for _, el := range candidates {
					if el != nil && strings.Contains(el.attr("class"), "imageGrid") {
						e.noteImage = el
						break
					}
				}
			}
		}
		return e
	})
}

func (e *dfdSubstack) name() string { return "substack" }

func (e *dfdSubstack) canExtract() bool { return e.postContentSelector != "" || e.noteText != nil }

func (e *dfdSubstack) extract() dfdExtracted {
	if e.postContentSelector != "" {
		return e.extractPost()
	}
	return e.extractNote()
}

// ogContent is the content attribute of the og meta tag of that property, or "".
func (e *dfdSubstack) ogContent(property string) string {
	return extAttr(ldQuerySelector(e.ctx.doc, `meta[property="`+property+`"]`), "content")
}

func (e *dfdSubstack) extractPost() dfdExtracted {
	title := e.firstTruthy(extGet(e.postData, "title"), e.ogContent("og:title"))
	description := e.firstTruthy(extGet(e.postData, "subtitle"), e.ogContent("og:description"))
	author := extGet(extIdx(extGet(e.postData, "publishedBylines"), 0), "name")
	if !jsTruthy(author) {
		author = extText(ldQuerySelector(e.ctx.doc, `a[href*="substack.com/@"]`))
	}
	published := extGet(e.postData, "post_date")
	if !jsTruthy(published) {
		published = e.parseDateFromByline()
	}
	str := func(v any) string {
		if jsTruthy(v) {
			return rscString(v)
		}
		return ""
	}
	return dfdExtracted{
		contentSelector: e.postContentSelector,
		variables: map[string]string{
			"title":       title,
			"author":      str(author),
			"site":        "Substack",
			"description": description,
			"published":   str(published),
		},
	}
}

func (e *dfdSubstack) firstTruthy(v any, fallback string) string {
	if jsTruthy(v) {
		return rscString(v)
	}
	return fallback
}

func (e *dfdSubstack) extractNote() dfdExtracted {
	textHTML := ldOuterHTML(e.noteText)
	imageHTML := e.buildImageHTML()
	content := textHTML
	if imageHTML != "" {
		content = textHTML + "\n" + imageHTML
	}
	title := e.ogContent("og:title")
	description := e.ogContent("og:description")
	author := jsTrim(reSubstackHandle.ReplaceAllLiteralString(title, ""))
	return dfdExtracted{
		content:     content,
		contentHTML: content,
		variables:   map[string]string{"title": title, "author": author, "site": "Substack", "description": description},
	}
}

func (e *dfdSubstack) parseDateFromByline() string {
	byline := ldQuerySelector(e.ctx.doc, `[class*="byline-wrapper"]`)
	if byline == nil {
		return ""
	}
	// textContent runs adjacent words together ("ZhutovFeb"): a space goes in at the case boundaries.
	text := reSubstackCase.ReplaceAllString(jsTrim(byline.textContent()), "${1} ${2}")
	m := reSubstackDate.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	day := m[2]
	if len(day) < 2 {
		day = "0" + day
	}
	return m[3] + "-" + substackMonths[m[1]] + "-" + day + "T00:00:00+00:00"
}

// extractPreloadData is the post of window._preloads' feedData, or nil.
func (e *dfdSubstack) extractPreloadData() any {
	for _, script := range ldQuerySelectorAll(e.ctx.doc, "script") {
		text := script.textContent()
		if !strings.Contains(text, "window._preloads") || !strings.Contains(text, "body_html") {
			continue
		}
		idx := strings.Index(text, `JSON.parse("`)
		if idx == -1 {
			continue
		}
		start := idx + len(`JSON.parse("`)
		i := start
		for i < len(text) {
			if text[i] == '\\' {
				i += 2
			} else if text[i] == '"' {
				break
			} else {
				i++
			}
		}
		if i > len(text) {
			i = len(text)
		}
		jsonString, err := jsonParse(`"` + text[start:i] + `"`)
		if err != nil {
			continue
		}
		s, _ := jsonString.(string)
		data, err := jsonParse(s)
		if err != nil {
			continue
		}
		post := extGet(extGet(extGet(data, "feedData"), "initialPost"), "post")
		if jsTruthy(extGet(post, "body_html")) {
			return post
		}
	}
	return nil
}

func (e *dfdSubstack) buildImageHTML() string {
	if e.noteImage == nil {
		return ""
	}
	if og := e.ogContent("og:image"); og != "" {
		return `<img src="` + dfdEscapeHTML(og) + `" alt="" />`
	}
	img := ldQuerySelector(e.noteImage, "img")
	if img == nil {
		return ""
	}
	if src := e.getLargestSrc(img); src != "" {
		return `<img src="` + dfdEscapeHTML(src) + `" alt="" />`
	}
	return ""
}

func (e *dfdSubstack) getLargestSrc(img *ldNode) string {
	if srcset := img.attr("srcset"); srcset != "" {
		bestURL, bestWidth := "", 0.0
		for n, m := range reSubstackSrcset.FindAllStringSubmatch(srcset, -1) {
			url := jsTrim(m[1])
			if n > 0 {
				url = reSubstackComma.ReplaceAllLiteralString(url, "")
			}
			if width := jsParseFloatNaN(m[2]); url != "" && width > bestWidth {
				bestWidth, bestURL = width, url
			}
		}
		if bestURL != "" {
			return reSubstackC.ReplaceAllLiteralString(reSubstackW.ReplaceAllLiteralString(bestURL, ""), "")
		}
	}
	return img.attr("src")
}
