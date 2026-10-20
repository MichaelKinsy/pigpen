package websearch

import (
	"strings"
)

// Port of defuddle 0.19.3 dist/extractors/nytimes.js: the article is rendered from window.__preloadedData into an
// element injected into the document (by the constructor, as in the original), and the ordinary pipeline runs on it.

const nytInjectedAttr = "data-defuddle-nyt"

type dfdNytimes struct {
	ctx             dfdExtCtx
	preloaded       any
	contentSelector string
}

var reNytPreload = jsRE(`window\.__preloadedData\s*=\s*({[\s\S]+})\s*;?\s*$`, "")

func init() {
	dfdRegisterExtractor("nytimes", func(c dfdExtCtx) dfdExtractor {
		e := &dfdNytimes{ctx: c}
		e.preloaded = e.extractPreloadData()
		if e.preloaded != nil {
			body := extGet(e.preloaded, "sprinkledBody")
			if !jsTruthy(body) {
				body = extGet(e.preloaded, "body")
			}
			if extHasLen(extGet(body, "content")) {
				// Inject rendered content into the document for the pipeline.
				if ldQuerySelector(c.doc, "["+nytInjectedAttr+"]") == nil {
					wrapper := ldNewElement("div")
					wrapper.setAttribute(nytInjectedAttr, "")
					wrapper.appendChild(ldParseFragment(e.renderBlocks(extGet(body, "content"))))
					docBody := c.doc.body()
					if docBody == nil {
						ldThrowType("Cannot read properties of null (reading 'appendChild')")
					}
					docBody.appendChild(wrapper)
				}
				e.contentSelector = "[" + nytInjectedAttr + "]"
			}
		}
		return e
	})
}

func (e *dfdNytimes) name() string { return "nytimes" }

func (e *dfdNytimes) canExtract() bool { return e.contentSelector != "" }

func (e *dfdNytimes) extract() dfdExtracted {
	article := e.preloaded
	orEmpty := func(v any) string {
		if jsTruthy(v) {
			return rscString(v)
		}
		return ""
	}
	var authors []string
	creators := extGet(extIdx(extGet(article, "bylines"), 0), "creators")
	if !jsTruthy(creators) {
		creators = []any{}
	}
	for _, c := range extArray(creators, "creators") {
		if name := extGetNN(c, "displayName"); jsTruthy(name) {
			authors = append(authors, rscString(name))
		}
	}
	return dfdExtracted{
		contentSelector: e.contentSelector,
		variables: map[string]string{
			"title":       orEmpty(extGet(extGet(article, "headline"), "default")),
			"author":      strings.Join(authors, ", "),
			"published":   orEmpty(extGet(article, "firstPublished")),
			"description": orEmpty(extGet(article, "summary")),
		},
	}
}

func (e *dfdNytimes) extractPreloadData() any {
	for _, script := range ldQuerySelectorAll(e.ctx.doc, "script:not([src])") {
		text := script.textContent()
		if !strings.Contains(text, "window.__preloadedData") {
			continue
		}
		m := reNytPreload.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		// NYT JSON contains JS `undefined` values: replace with null.
		data, err := jsonParse(nytUndefinedToNull(m[1]))
		if err != nil {
			return nil
		}
		article := extGet(extGet(extGet(data, "initialData"), "data"), "article")
		if !jsTruthy(article) {
			return nil
		}
		return article
	}
	return nil
}

// nytUndefinedToNull is .replace(/(?<=:)undefined(?=[,}\]])/g, 'null').
func nytUndefinedToNull(s string) string {
	var b strings.Builder
	last := 0
	for i := 0; ; {
		j := strings.Index(s[i:], "undefined")
		if j < 0 {
			break
		}
		j += i
		end := j + len("undefined")
		if j > 0 && s[j-1] == ':' && end < len(s) && (s[end] == ',' || s[end] == '}' || s[end] == ']') {
			b.WriteString(s[last:j])
			b.WriteString("null")
			last = end
		}
		i = end
	}
	b.WriteString(s[last:])
	return b.String()
}

func (e *dfdNytimes) renderBlocks(blocks any) string {
	var parts []string
	for _, block := range extArray(blocks, "blocks") {
		switch jsString2(extGetNN(block, "__typename")) {
		case "ParagraphBlock":
			parts = append(parts, "<p>"+e.renderInlines(extGet(block, "content"))+"</p>")
		case "Heading2Block":
			parts = append(parts, "<h2>"+e.renderInlines(extGet(block, "content"))+"</h2>")
		case "Heading3Block":
			parts = append(parts, "<h3>"+e.renderInlines(extGet(block, "content"))+"</h3>")
		case "Heading4Block":
			parts = append(parts, "<h4>"+e.renderInlines(extGet(block, "content"))+"</h4>")
		case "ImageBlock":
			media := extGet(block, "media")
			if !jsTruthy(media) {
				break
			}
			src := e.getBestImageURL(media)
			if !jsTruthy(src) {
				break
			}
			captionText := extGet(extGet(media, "caption"), "text")
			altV := extGet(media, "altText")
			if !jsTruthy(altV) {
				altV = captionText
			}
			alt := ""
			if jsTruthy(altV) {
				alt = nytEscapeAttr(altV)
			}
			var figParts []string
			for _, v := range []any{captionText, extGet(media, "credit")} {
				if jsTruthy(v) {
					figParts = append(figParts, rscString(v))
				}
			}
			if len(figParts) > 0 {
				parts = append(parts, "<figure><img src=\""+nytEscapeAttr(src)+"\" alt=\""+alt+"\"><figcaption>"+
					nytEscapeHTML(strings.Join(figParts, " "))+"</figcaption></figure>")
			} else {
				parts = append(parts, "<img src=\""+nytEscapeAttr(src)+"\" alt=\""+alt+"\">")
			}
		case "HeaderBasicBlock", "Dropzone":
			// Structural blocks are skipped.
		default:
			// Unknown blocks that have inline content.
			if extHasLen(extGet(block, "content")) {
				parts = append(parts, "<p>"+e.renderInlines(extGet(block, "content"))+"</p>")
			}
		}
	}
	return strings.Join(parts, "\n")
}

// jsString2 is the string a `switch` compares a __typename with; only a string can equal a case label.
func jsString2(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return "\x00not a string"
}

func (e *dfdNytimes) renderInlines(inlines any) string {
	if !jsTruthy(inlines) {
		return ""
	}
	list, ok := inlines.([]any)
	if !ok {
		ldThrowType("inlines.map is not a function")
	}
	var b strings.Builder
	for _, inline := range list {
		textV := extGetNN(inline, "text")
		text := ""
		if jsTruthy(textV) {
			text = nytEscapeHTML(textV)
		}
		formats := extGet(inline, "formats")
		if extHasLen(formats) {
			for _, fmt := range extArray(formats, "formats") {
				switch jsString2(extGetNN(fmt, "__typename")) {
				case "BoldFormat":
					text = "<strong>" + text + "</strong>"
				case "ItalicFormat":
					text = "<em>" + text + "</em>"
				case "LinkFormat":
					if url := extGet(fmt, "url"); jsTruthy(url) {
						text = "<a href=\"" + nytEscapeAttr(url) + "\">" + text + "</a>"
					}
				}
			}
		}
		b.WriteString(text)
	}
	return b.String()
}

// getBestImageURL returns the url (any truthy value) of the preferred rendition, or nil.
func (e *dfdNytimes) getBestImageURL(media any) any {
	crops := extGet(media, "crops")
	if !extHasLen(crops) {
		return nil
	}
	cropList := extArray(crops, "crops")
	// Prefer superJumbo > jumbo > articleLarge.
	for _, name := range []string{"superJumbo", "jumbo", "articleLarge"} {
		for _, crop := range cropList {
			renditions := extGetNN(crop, "renditions")
			if !jsTruthy(renditions) {
				continue
			}
			rl, ok := renditions.([]any)
			if !ok {
				ldThrowType("crop.renditions?.find is not a function")
			}
			for _, r := range rl {
				if extGetNN(r, "name") == any(name) {
					if url := extGet(r, "url"); jsTruthy(url) {
						return url
					}
					break
				}
			}
		}
	}
	// Fall back to any rendition with a URL.
	for _, crop := range cropList {
		if extHasLen(extGet(crop, "renditions")) {
			if url := extGet(extIdx(extGet(crop, "renditions"), 0), "url"); jsTruthy(url) {
				return url
			}
		}
	}
	return nil
}

func nytEscapeHTML(v any) string {
	s := extStrCall(v, "replace")
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}

func nytEscapeAttr(v any) string {
	s := extStrCall(v, "replace")
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}
