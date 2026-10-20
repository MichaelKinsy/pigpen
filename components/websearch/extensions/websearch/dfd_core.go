package websearch

import (
	"math"
	"strconv"
	"strings"
)

// Port of defuddle 0.19.3 dist/defuddle.js (class Defuddle), as extract.ts calls it: a linkedom document, the
// page URL, and the options { markdown: true, useAsync: false }.
//
// Not reachable from that call, so not ported: the asynchronous extractors (useAsync is false), the debug and
// profile options, the media-query evaluation (linkedom's document has no styleSheets and the window no
// CSSMediaRule), getComputedStyle (the window is a Proxy over Node's globalThis, which has none), and the shadow
// DOM flattening (linkedom's shadowRoot is only set by attachShadow, which the parser never calls).

type dfdResult struct {
	content       string
	wordCount     int
	meta          dfdMetadata
	extractorType string
}

type dfdOptions struct {
	removeExactSelectors   bool
	removePartialSelectors bool
	removeHiddenElements   bool
	removeLowScoring       bool
	removeSmallImages      bool
	removeContentPatterns  bool
	standardize            bool
	contentSelector        string
}

func dfdDefaultOptions() dfdOptions {
	return dfdOptions{true, true, true, true, true, true, true, ""}
}

type dfdDefuddle struct {
	doc      *ldNode
	url      string // options.url
	location string // document.location.href

	schemaOrg       []any
	schemaExtracted bool
	metaTags        []dfdMetaTag
	haveMeta        bool
	metadata        dfdMetadata
	haveMetadata    bool
	smallImages     map[string]bool
	haveSmall       bool

	inExtractorPipelineRun bool
	processingError        *jsThrow
}

func (d *dfdDefuddle) getSchemaOrgData() []any {
	if !d.schemaExtracted {
		d.schemaOrg = d.extractSchemaOrgData(d.doc)
		d.schemaExtracted = true
	}
	return d.schemaOrg
}

// parse is Defuddle.parse: the first attempt and the retries when it finds little content.
func (d *dfdDefuddle) parse() dfdResult {
	if body := d.doc.body(); body != nil {
		d.normalizeAttributes(body)
		d.resolveNoscriptImages(body)
	}
	result := d.parseInternal(dfdDefaultOptions(), nil)
	if result.wordCount < 200 {
		o := dfdDefaultOptions()
		o.removePartialSelectors = false
		retry := d.parseInternal(o, nil)
		if retry.wordCount > result.wordCount*2 {
			result = retry
		}
	}
	if result.wordCount < 50 {
		o := dfdDefaultOptions()
		o.removeHiddenElements = false
		hiddenRetry := d.parseInternal(o, nil)
		if hiddenRetry.wordCount > result.wordCount*2 {
			result = hiddenRetry
		}
		if hiddenSelector, ok := d.findLargestHiddenContentSelector(); ok {
			o := dfdDefaultOptions()
			o.removeHiddenElements = false
			o.removePartialSelectors = false
			o.contentSelector = hiddenSelector
			r := d.parseInternal(o, nil)
			if r.wordCount > result.wordCount || (float64(r.wordCount) > math.Max(20, float64(result.wordCount)*0.7) && jsLen(r.content) < jsLen(result.content)) {
				result = r
			}
		}
	}
	if result.wordCount < 50 {
		o := dfdDefaultOptions()
		o.removeLowScoring = false
		o.removePartialSelectors = false
		o.removeContentPatterns = false
		indexRetry := d.parseInternal(o, nil)
		if indexRetry.wordCount > result.wordCount {
			result = indexRetry
		}
	}
	schemaText := d.getSchemaText(result.meta.schemaOrgData, 0)
	if schemaText != "" && float64(d.countHTMLWords(schemaText)) > float64(result.wordCount)*1.5 {
		live := d.doc
		safe := live.cloneNode(true)
		d.stripUnsafeElements(safe.body())
		d.doc = safe
		func() {
			defer func() { d.doc = live }()
			if best := d.findElementBySchemaText(d.doc.body(), schemaText); best != nil {
				selector := d.getElementSelector(best)
				o := dfdDefaultOptions()
				o.contentSelector = selector
				result = d.parseInternal(o, nil)
			} else {
				safe := d.sanitizeExtractorHTML(schemaText)
				result.content = safe
				result.wordCount = d.countHTMLWords(safe)
			}
		}()
	}
	return result
}

func (d *dfdDefuddle) getSchemaText(schema any, depth int) string {
	if schema == nil || depth > 10 {
		return ""
	}
	var items []any
	if arr, ok := schema.([]any); ok {
		items = arr
	} else {
		items = []any{schema}
	}
	for _, item := range items {
		if arr, ok := item.([]any); ok {
			if found := d.getSchemaText(arr, depth+1); found != "" {
				return found
			}
			continue
		}
		obj, ok := item.(*jsObj)
		if !ok {
			continue
		}
		if v, ok := obj.get("text"); ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
		if v, ok := obj.get("articleBody"); ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
		if v, ok := obj.get("@graph"); ok {
			if g, ok := v.([]any); ok {
				if found := d.getSchemaText(g, depth+1); found != "" {
					return found
				}
			}
		}
	}
	return ""
}

func (d *dfdDefuddle) serializeFallbackBody() string {
	body := d.doc.body()
	if body == nil {
		return ""
	}
	safe := body.cloneNode(true)
	d.stripUnsafeElements(safe)
	return d.resolveContentURLs(ldInnerHTML(safe))
}

const dfdDangerousElements = `script:not([type^="math/"]), style, noscript, frame, frameset, object, embed, applet, base, animate, set, animatemotion, animatetransform, animatecolor, discard, animateMotion, animateTransform, animateColor`

func (d *dfdDefuddle) stripUnsafeElements(body *ldNode) {
	if body == nil {
		return
	}
	for _, el := range ldQuerySelectorAll(body, dfdDangerousElements) {
		el.remove()
	}
	all := append([]*ldNode{body}, ldQuerySelectorAll(body, "*")...)
	for _, el := range all {
		for _, a := range append([]ldAttr(nil), el.attrs...) {
			name := strings.ToLower(a.name)
			switch {
			case strings.HasPrefix(name, "on"):
				el.removeAttribute(a.name)
			case name == "srcdoc":
				el.removeAttribute(a.name)
			case name == "href" || name == "src" || name == "action" || name == "formaction" || name == "xlink:href":
				allowInline := !(name == "src" && el.tagName() == "IFRAME")
				if dfdIsDangerousURL(a.value, allowInline) {
					el.removeAttribute(a.name)
				}
			}
		}
	}
}

func (d *dfdDefuddle) deduplicateImages(body *ldNode) {
	for _, figure := range ldQuerySelectorAll(body, "figure") {
		var figImgs []*ldNode
		for _, img := range ldQuerySelectorAll(figure, "img") {
			if ldClosest(img, "noscript") == nil && img.parent != nil {
				figImgs = append(figImgs, img)
			}
		}
		if len(figImgs) < 2 {
			continue
		}
		type group struct {
			key  string
			null bool
			imgs []*ldNode
		}
		var groups []*group
		find := func(key string, null bool) *group {
			for _, g := range groups {
				if g.key == key && g.null == null {
					return g
				}
			}
			g := &group{key: key, null: null}
			groups = append(groups, g)
			return g
		}
		for _, img := range figImgs {
			src := img.attr("src")
			if src == "" || strings.HasPrefix(src, "data:") {
				continue
			}
			key := jsTrim(img.attr("alt"))
			g := find(key, key == "")
			g.imgs = append(g.imgs, img)
		}
		for _, g := range groups {
			if len(g.imgs) < 2 {
				continue
			}
			if !g.null {
				same := true
				for _, img := range g.imgs {
					if img.attr("src") != g.imgs[0].attr("src") {
						same = false
						break
					}
				}
				if same {
					continue
				}
			}
			d.keepBestImage(g.imgs)
		}
	}
	imgs := ldQuerySelectorAll(body, "img")
	for i := 0; i+1 < len(imgs); i++ {
		img := imgs[i]
		if img.parentElement() == nil {
			continue
		}
		if ldClosest(img, "noscript") != nil || ldClosest(img, "figure") != nil {
			continue
		}
		alt := jsTrim(img.attr("alt"))
		if alt == "" {
			continue
		}
		src := img.attr("src")
		if src == "" || strings.HasPrefix(src, "data:") {
			continue
		}
		other := imgs[i+1]
		if other.parentElement() == nil {
			continue
		}
		if ldClosest(other, "noscript") != nil || ldClosest(other, "figure") != nil {
			continue
		}
		if jsTrim(other.attr("alt")) != alt {
			continue
		}
		otherSrc := other.attr("src")
		if otherSrc == "" || strings.HasPrefix(otherSrc, "data:") {
			continue
		}
		if otherSrc == src {
			continue
		}
		if !d.noVisibleContentBetween(img, other) {
			continue
		}
		d.keepBestImage([]*ldNode{img, other})
	}
	for _, img := range ldQuerySelectorAll(body, "img") {
		if img.parentElement() == nil {
			continue
		}
		if ldClosest(img, "a, figure, noscript") != nil {
			continue
		}
		src := img.attr("src")
		if src == "" || strings.HasPrefix(src, "data:") {
			continue
		}
		parent := img.parentElement()
		normalized := d.normalizeSrc(src)
		for _, link := range ldQuerySelectorAll(parent, ":scope > a[href]") {
			if ldQuerySelector(link, "img") == nil {
				continue
			}
			if normalized == d.normalizeSrc(link.attr("href")) {
				img.remove()
				break
			}
		}
	}
}

func (d *dfdDefuddle) keepBestImage(group []*ldNode) {
	best := group[0]
	for i := 1; i < len(group); i++ {
		winner := d.pickBestImage(best, group[i])
		if winner == best {
			group[i].remove()
		} else {
			best.remove()
		}
		best = winner
	}
}

func (d *dfdDefuddle) noVisibleContentBetween(a, b *ldNode) bool {
	next := func(n *ldNode) *ldNode {
		if n == nil {
			return nil
		}
		if n.first != nil {
			return n.first
		}
		for x := n; x != nil; x = x.parent {
			if x.next != nil {
				return x.next
			}
		}
		return nil
	}
	for node := next(a); node != nil && node != b; node = next(node) {
		if node.kind == ldText && jsTrim(node.data) != "" {
			return false
		}
	}
	return true
}

var reNormSrcScheme = jsRE(`^https?://`, "")

func (d *dfdDefuddle) normalizeSrc(url string) string {
	return strings.SplitN(reNormSrcScheme.ReplaceAllString(url, ""), "?", 2)[0]
}

func (d *dfdDefuddle) removeCoverImage(body *ldNode, metadataImage string) string {
	if metadataImage == "" {
		return ""
	}
	metaNorm := d.normalizeSrc(metadataImage)
	for _, img := range ldQuerySelectorAll(body, "img") {
		src := img.attr("src")
		if src == "" || strings.HasPrefix(src, "data:") {
			continue
		}
		if d.normalizeSrc(src) != metaNorm {
			continue
		}
		bestURL := d.getLargestImageSrc(img)
		if fig := ldClosest(img, "figure"); fig != nil && ldQuerySelector(fig, "figcaption") != nil {
			return bestURL
		}
		img.remove()
		return bestURL
	}
	return ""
}

var reURLWidth2 = jsRE(`(?:width[=:/]|[/,?&]w[_:=])(\d+)`, "")

func (d *dfdDefuddle) pickBestImage(a, b *ldNode) *ldNode {
	tier := func(img *ldNode) int {
		if img.attr("srcset") != "" {
			return 2
		}
		if ldClosest(img, "picture") != nil {
			return 1
		}
		return 0
	}
	ta, tb := tier(a), tier(b)
	if ta != tb {
		if ta > tb {
			return a
		}
		return b
	}
	wa, wb := d.urlWidth(a), d.urlWidth(b)
	if wa != wb {
		if wa > wb {
			return a
		}
		return b
	}
	return a
}

func (d *dfdDefuddle) urlWidth(img *ldNode) float64 {
	if m := reURLWidth2.FindStringSubmatch(img.attr("src")); m != nil {
		return jsParseInt(m[1])
	}
	return 0
}

func (d *dfdDefuddle) normalizeAttributes(body *ldNode) {
	for _, el := range ldQuerySelectorAll(body, "img, source") {
		if v, ok := el.attrValue("srcSet"); ok {
			el.removeAttribute("srcSet")
			el.setAttribute("srcset", v)
		}
	}
}

func (d *dfdDefuddle) resolveNoscriptImages(body *ldNode) {
	for _, noscript := range ldQuerySelectorAll(body, "noscript") {
		noscriptImg := ldQuerySelector(noscript, "img")
		if noscriptImg == nil {
			html := ldInnerHTML(noscript)
			if !strings.Contains(html, "<img") {
				html = noscript.textContent()
			}
			if !strings.Contains(html, "<img") {
				continue
			}
			noscriptImg = ldQuerySelector(ldParseFragment(html), "img")
		}
		if noscriptImg == nil {
			continue
		}
		realSrc := noscriptImg.attr("src")
		if realSrc == "" || strings.HasPrefix(realSrc, "data:") {
			continue
		}
		alt, hasAlt := noscriptImg.getAttribute("alt")
		parent := noscript.parentElement()
		if parent == nil {
			continue
		}
		matched := false
		for _, img := range ldQuerySelectorAll(parent, ":scope > img") {
			src := img.attr("src")
			if !strings.HasPrefix(src, "data:") {
				continue
			}
			if !hasAlt || alt == "" || img.attr("alt") != alt {
				continue
			}
			img.setAttribute("src", realSrc)
			if srcset := noscriptImg.attr("srcset"); srcset != "" {
				img.setAttribute("srcset", srcset)
			}
			matched = true
			break
		}
		if !matched && d.isLazyImageContext(noscript) {
			container := ldClosest(noscript, "figure")
			if container == nil {
				container = parent
			}
			hasReal := false
			for _, img := range ldQuerySelectorAll(container, "img") {
				if ldClosest(img, "noscript") != nil {
					continue
				}
				src := img.attr("src")
				if src != "" && !strings.HasPrefix(src, "data:") {
					hasReal = true
					break
				}
			}
			if !hasReal {
				parent.insertBefore(noscriptImg.cloneNode(true), noscript)
			}
		}
	}
}

func (d *dfdDefuddle) isLazyImageContext(noscript *ldNode) bool {
	if ldClosest(noscript, "figure") != nil {
		return true
	}
	if parent := noscript.parentElement(); parent != nil {
		for _, sib := range parent.children() {
			if sib == noscript {
				continue
			}
			if strings.Contains(strings.ToLower(dfdClassName(sib)), "lazy") {
				return true
			}
		}
		pc := strings.ToLower(dfdClassName(parent))
		if strings.Contains(pc, "image") || strings.Contains(pc, "img") || strings.Contains(pc, "picture") || strings.Contains(pc, "photo") || strings.Contains(pc, "media") {
			return true
		}
	}
	return false
}

var reParaSplit = jsRE(`\n\s*\n`, "")

func (d *dfdDefuddle) findElementBySchemaText(root *ldNode, schemaText string) *ldNode {
	first := ""
	if parts := reParaSplit.Split(schemaText, -1); len(parts) > 0 {
		first = jsTrim(parts[0])
	}
	searchPhrase := jsTrim(dfdSubstring0(first, 100))
	if searchPhrase == "" {
		return nil
	}
	schemaWordCount := dfdCountWords(schemaText)
	var best *ldNode
	bestSize := math.Inf(1)
	for _, el := range ldQuerySelectorAll(root, "*") {
		if el == root {
			continue
		}
		elText := el.textContent()
		if !strings.Contains(elText, searchPhrase) {
			continue
		}
		elWords := float64(dfdCountWords(elText))
		if elWords >= float64(schemaWordCount)*0.8 && elWords < bestSize {
			bestSize = elWords
			best = el
		}
	}
	return best
}

func (d *dfdDefuddle) findLargestHiddenContentSelector() (string, bool) {
	body := d.doc.body()
	if body == nil {
		return "", false
	}
	var best *ldNode
	bestWords := 0
	for _, el := range ldQuerySelectorAll(body, dfdHiddenExactSkipSelector) {
		if strings.Contains(el.attr("class"), "math") {
			continue
		}
		if w := dfdCountWords(el.textContent()); w > bestWords {
			best = el
			bestWords = w
		}
	}
	if best == nil || bestWords < 30 {
		return "", false
	}
	return d.getElementSelector(best), true
}

var reSrcsetWidthEntry = jsRE(`(.+?)\s+(\d+(?:\.\d+)?)w`, "")
var reCDNWidth = jsRE(`,w_\d+`, "")
var reCDNCrop = jsRE(`,c_\w+`, "")

func (d *dfdDefuddle) getLargestImageSrc(img *ldNode) string {
	srcset := img.attr("srcset")
	if srcset == "" {
		return img.attr("src")
	}
	bestURL := ""
	bestWidth := 0.0
	lastIndex := 0
	for _, m := range reSrcsetWidthEntry.FindAllStringSubmatchIndex(srcset, -1) {
		url := jsTrim(srcset[m[2]:m[3]])
		if lastIndex > 0 {
			url = reLeadingComma.ReplaceAllString(url, "")
		}
		lastIndex = m[1]
		width := jsParseFloat(srcset[m[4]:m[5]])
		if url != "" && width > bestWidth {
			bestWidth = width
			bestURL = url
		}
	}
	url := bestURL
	if url == "" {
		url = img.attr("src")
	}
	url = reCDNWidth.ReplaceAllString(url, "")
	url = reCDNCrop.ReplaceAllString(url, "")
	return url
}

// ---- parseInternal ---------------------------------------------------------------------------------------------

func (d *dfdDefuddle) emptyResult() dfdResult {
	return dfdResult{}
}

var reRCSwap = jsRE(`\$RC\("(B:\d+)","(S:\d+)"\)`, "")

func (d *dfdDefuddle) parseInternal(options dfdOptions, _ any) (result dfdResult) {
	if d.doc.documentElement() == nil {
		domain := ""
		if d.url != "" {
			if u, ok := newURL(d.url, ""); ok {
				domain = u.hostname()
			} else {
				ldThrowType("Invalid URL")
			}
		}
		return dfdResult{meta: dfdMetadata{domain: domain}}
	}
	schema := d.getSchemaOrgData()
	if !d.haveMeta {
		d.metaTags = d.collectMetaTags()
		d.haveMeta = true
	}
	if !d.haveMetadata {
		d.metadata = dfdExtractMetadata(d.doc, schema, d.metaTags, d.location)
		d.haveMetadata = true
	}
	metadata := d.metadata
	defer func() {
		if r := recover(); r != nil {
			t, ok := r.(*jsThrow)
			if !ok {
				panic(r)
			}
			d.processingError = t
			content := d.serializeFallbackBody()
			result = dfdResult{content: content, meta: metadata, wordCount: d.countHTMLWords(content)}
		}
	}()
	url := d.url
	if !d.inExtractorPipelineRun {
		if ext := dfdFindExtractor(d, url, schema); ext != nil && ext.canExtract() {
			extracted := ext.extract()
			if extracted.contentSelector != "" {
				d.inExtractorPipelineRun = true
				defer func() { d.inExtractorPipelineRun = false }()
				// The nested run takes the defaults, not the options of the retry that found the extractor.
				o := dfdDefaultOptions()
				o.contentSelector = extracted.contentSelector
				o.removeLowScoring = false
				o.removeHiddenElements = false
				pipeline := d.parseInternal(o, nil)
				return d.mergeExtractorResult(pipeline, extracted, ext)
			}
			return d.buildExtractorResponse(extracted, metadata, ext)
		}
	}
	if !d.haveSmall {
		d.smallImages = dfdFindSmallImages(d.doc)
		d.haveSmall = true
	}
	clone := d.doc.cloneNode(true)
	if b := clone.body(); b != nil {
		b.normalize()
	}
	d.resolveStreamedContent(clone)

	var mainContent *ldNode
	if options.contentSelector != "" {
		mainContent = ldQuerySelector(clone, options.contentSelector)
	}
	if mainContent == nil {
		mainContent = d.findMainContent(clone)
	}
	if mainContent != nil {
		if anc := ldClosest(mainContent, "[data-defuddle]"); anc != nil {
			mainContent = anc
		}
	}
	if mainContent != nil && mainContent.name == "body" {
		if schemaText := d.getSchemaText(schema, 0); schemaText != "" {
			if sc := d.findElementBySchemaText(clone.body(), schemaText); sc != nil {
				mainContent = sc
			}
		}
	}
	if mainContent == nil {
		content := d.serializeFallbackBody()
		return dfdResult{content: content, meta: metadata, wordCount: d.countHTMLWords(content)}
	}
	if metadata.published != "" || metadata.author != "" {
		dfdRemoveMetadataBlock(mainContent)
	}
	for _, el := range ldQuerySelectorAll(mainContent, "wbr") {
		el.remove()
	}
	if options.standardize {
		d.adoptExternalFootnotes(mainContent, clone)
	}
	if options.standardize {
		dfdStandardizeFootnotes(mainContent)
		dfdStandardizeCallouts(mainContent)
	}
	if options.removeSmallImages {
		dfdRemoveSmallImages(clone, d.smallImages)
	}
	if options.removeHiddenElements {
		dfdRemoveHiddenElements(clone)
	}
	if options.removeContentPatterns && mainContent != nil {
		dfdRemoveEyebrowLabel(mainContent)
	}
	if options.removeExactSelectors || options.removePartialSelectors {
		dfdRemoveBySelector(clone, options.removeExactSelectors, options.removePartialSelectors, mainContent, !options.removeHiddenElements)
	}
	if options.removeLowScoring {
		dfdScoreAndRemove(clone, mainContent)
	}
	if options.removeContentPatterns && mainContent != nil {
		dfdRemoveByContentPattern(mainContent, d.url, metadata.title, metadata.description)
	}
	if options.standardize {
		dfdStandardizeContent(mainContent, metadata.title, d.doc)
	}
	d.resolveRelativeURLs(mainContent)
	d.deduplicateImages(mainContent)
	if best := d.removeCoverImage(mainContent, metadata.image); best != "" {
		metadata.image = best
		d.metadata.image = best
	}
	d.stripUnsafeElements(mainContent)
	content := ldOuterHTML(mainContent)
	return dfdResult{content: content, meta: metadata, wordCount: d.countHTMLWords(content)}
}

var (
	reHTMLTags = jsRE(`<[^>]*>`, "")
	reNbspEnt  = jsRE(`&nbsp;`, "i")
	reAmpEnt   = jsRE(`&amp;`, "i")
	reLtEnt    = jsRE(`&lt;`, "i")
	reGtEnt    = jsRE(`&gt;`, "i")
	reQuotEnt  = jsRE(`&quot;`, "i")
	reNumEnt   = jsRE(`&#\d+;`, "")
	reNamedEnt = jsRE(`&\w+;`, "")
)

func (d *dfdDefuddle) countHTMLWords(content string) int {
	text := reHTMLTags.ReplaceAllString(content, " ")
	text = reNbspEnt.ReplaceAllString(text, " ")
	text = reAmpEnt.ReplaceAllString(text, "&")
	text = reLtEnt.ReplaceAllString(text, "<")
	text = reGtEnt.ReplaceAllString(text, ">")
	text = reQuotEnt.ReplaceAllString(text, `"`)
	text = reNumEnt.ReplaceAllString(text, " ")
	text = reNamedEnt.ReplaceAllString(text, " ")
	return dfdCountWords(text)
}

func (d *dfdDefuddle) findMainContent(doc *ldNode) *ldNode {
	type cand struct {
		el    *ldNode
		score float64
		idx   int
	}
	var candidates []cand
	for index, selector := range dfdEntryPointElements {
		for _, el := range ldQuerySelectorAll(doc, selector) {
			score := float64(len(dfdEntryPointElements)-index)*40 + dfdScoreElement(el)
			candidates = append(candidates, cand{el, score, index})
		}
	}
	if len(candidates) == 0 {
		return d.findContentByScoring(doc)
	}
	sortStable(len(candidates), func(i, j int) bool { return candidates[i].score > candidates[j].score }, func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	if len(candidates) == 1 && candidates[0].el.name == "body" {
		if t := d.findTableBasedContent(doc); t != nil {
			return t
		}
	}
	top := candidates[0]
	best := top
	for i := 1; i < len(candidates); i++ {
		child := candidates[i]
		childWords := dfdCountWords(child.el.textContent())
		if child.idx < best.idx && best.el.contains(child.el) && childWords > 50 {
			siblingsAtIndex := 0
			for _, c := range candidates {
				if c.idx == child.idx && top.el.contains(c.el) {
					siblingsAtIndex++
					if siblingsAtIndex > 1 {
						break
					}
				}
			}
			if siblingsAtIndex > 1 {
				continue
			}
			best = child
		}
	}
	if best.el != top.el {
		return best.el
	}
	return top.el
}

func (d *dfdDefuddle) findTableBasedContent(doc *ldNode) *ldNode {
	hasTableLayout := false
	for _, table := range doc.getElementsByTagName("table") {
		width := jsParseInt(orZero(table.attr("width")))
		tableClass := strings.ToLower(dfdClassName(table))
		if width > 400 || table.attr("align") == "center" || strings.Contains(tableClass, "content") || strings.Contains(tableClass, "article") {
			hasTableLayout = true
			break
		}
		for _, row := range table.getElementsByTagName("tr") {
			cells := 0
			hasWidth := false
			for _, c := range row.children() {
				if c.tagName() == "TD" {
					cells++
					if c.attr("width") != "" {
						hasWidth = true
					}
				}
			}
			if cells >= 2 && hasWidth {
				hasTableLayout = true
				break
			}
		}
		if hasTableLayout {
			break
		}
	}
	if !hasTableLayout {
		return nil
	}
	bestCell := dfdFindBestElement(doc.getElementsByTagName("td"), 50)
	if bestCell == nil {
		return nil
	}
	bestCellWords := dfdCountWords(bestCell.textContent())
	root := doc.body()
	if root == nil {
		root = doc.documentElement()
	}
	bodyWords := dfdCountWords(root.textContent())
	if bestCellWords*2 < bodyWords {
		return nil
	}
	return bestCell
}

func (d *dfdDefuddle) findContentByScoring(doc *ldNode) *ldNode {
	var best *ldNode
	bestScore := 0.0
	for _, el := range ldQuerySelectorAll(doc, dfdBlockElementsSelector) {
		if s := dfdScoreElement(el); s > 0 && (best == nil || s > bestScore) {
			best, bestScore = el, s
		}
	}
	return best
}

var reUnsafeClass = jsRE(`[:\[\]()#>~+,]`, "")

func (d *dfdDefuddle) getElementSelector(element *ldNode) string {
	var parts []string
	current := element
	for current != nil && current != d.doc.documentElement() {
		selector := current.name
		if id := current.id(); id != "" {
			selector += "#" + id
		} else if cn := dfdClassName(current); cn != "" {
			var safe []string
			for _, cls := range jsSplitWS(jsTrim(cn)) {
				if !reUnsafeClass.MatchString(cls) {
					safe = append(safe, cls)
				}
			}
			if len(safe) > 0 {
				selector += "." + strings.Join(safe, ".")
			}
		}
		parts = append([]string{selector}, parts...)
		current = current.parentElement()
	}
	return strings.Join(parts, " > ")
}

func (d *dfdDefuddle) adoptExternalFootnotes(mainContent, root *ldNode) {
	body := root.body()
	if body == nil || mainContent == body {
		return
	}
	for _, el := range ldQuerySelectorAll(body, "div, section, aside") {
		className := dfdClassName(el)
		id := el.id()
		if !reFootnoteWord.MatchString(className) && !reFootnoteWord.MatchString(id) {
			continue
		}
		if mainContent.contains(el) || el.contains(mainContent) {
			continue
		}
		heading := ldQuerySelector(el, "h1, h2, h3, h4, h5, h6")
		if heading == nil || !reFootnoteSection.MatchString(jsTrim(heading.textContent())) {
			continue
		}
		mainContent.appendChild(el)
	}
}

func (d *dfdDefuddle) resolveRelativeURLs(element *ldNode) {
	docURL := d.url
	if docURL == "" {
		return
	}
	baseURL := docURL
	if baseEl := ldQuerySelector(d.doc, "base[href]"); baseEl != nil {
		if baseHref := baseEl.attr("href"); baseHref != "" {
			if u, ok := newURL(baseHref, docURL); ok {
				baseURL = u.href()
			}
		}
	}
	resolve := func(url string) string {
		normalized := jsTrim(url)
		normalized = reQuoteLead.ReplaceAllString(normalized, "")
		normalized = reQuoteTrail.ReplaceAllString(normalized, "")
		if strings.HasPrefix(normalized, "#") {
			return normalized
		}
		if u, ok := newURL(normalized, baseURL); ok {
			return u.href()
		}
		if normalized != "" {
			return normalized
		}
		return url
	}
	for _, el := range ldQuerySelectorAll(element, "[href]") {
		if href := el.attr("href"); href != "" {
			el.setAttribute("href", resolve(href))
		}
	}
	for _, el := range ldQuerySelectorAll(element, "[src]") {
		if src := el.attr("src"); src != "" {
			el.setAttribute("src", resolve(src))
		}
	}
	for _, el := range ldQuerySelectorAll(element, "[srcset]") {
		srcset := el.attr("srcset")
		if srcset == "" {
			continue
		}
		var entries []string
		lastIdx := 0
		for _, m := range reSrcsetDesc.FindAllStringSubmatchIndex(srcset, -1) {
			url := jsTrim(srcset[m[2]:m[3]])
			if lastIdx > 0 {
				url = reLeadingComma.ReplaceAllString(url, "")
			}
			lastIdx = m[1]
			entries = append(entries, resolve(url)+" "+srcset[m[4]:m[5]])
		}
		if len(entries) > 0 {
			el.setAttribute("srcset", strings.Join(entries, ", "))
		} else {
			parts := strings.Split(srcset, ",")
			for i, entry := range parts {
				fields := jsSplitWS(jsTrim(entry))
				if fields[0] != "" {
					fields[0] = resolve(fields[0])
				}
				parts[i] = strings.Join(fields, " ")
			}
			el.setAttribute("srcset", strings.Join(parts, ", "))
		}
	}
	for _, el := range ldQuerySelectorAll(element, "[poster]") {
		if poster := el.attr("poster"); poster != "" {
			el.setAttribute("poster", resolve(poster))
		}
	}
}

var (
	reQuoteLead  = jsRE(`^\\?["']+`, "")
	reQuoteTrail = jsRE(`\\?["']+$`, "")
	reSrcsetDesc = jsRE(`(.+?)\s+(\d+(?:\.\d+)?[wx])`, "")
)

func (d *dfdDefuddle) resolveContentURLs(html string) string {
	if d.url == "" {
		return html
	}
	container := ldNewElement("div")
	container.appendChild(ldParseFragment(html))
	d.resolveRelativeURLs(container)
	return ldInnerHTML(container)
}

func (d *dfdDefuddle) resolveStreamedContent(doc *ldNode) {
	type swap struct{ templateID, contentID string }
	var swaps []swap
	for _, script := range ldQuerySelectorAll(doc, "script") {
		text := script.textContent()
		if !strings.Contains(text, "$RC(") {
			continue
		}
		for _, m := range reRCSwap.FindAllStringSubmatch(text, -1) {
			swaps = append(swaps, swap{m[1], m[2]})
		}
	}
	for _, s := range swaps {
		template := doc.getElementByID(s.templateID)
		content := doc.getElementByID(s.contentID)
		if template == nil || content == nil {
			continue
		}
		parent := template.parent
		if parent == nil {
			continue
		}
		next := template.next
		foundMarker := false
		for next != nil {
			following := next.next
			if next.kind == ldComment && next.data == "/$" {
				next.remove()
				foundMarker = true
				break
			}
			next.remove()
			next = following
		}
		if !foundMarker {
			continue
		}
		for content.first != nil {
			parent.insertBefore(content.first, template)
		}
		template.remove()
		content.remove()
	}
}

func (d *dfdDefuddle) collectMetaTags() []dfdMetaTag {
	var tags []dfdMetaTag
	for _, meta := range ldQuerySelectorAll(d.doc, "meta") {
		var name, property *string
		if v, ok := meta.attrValue("name"); ok {
			name = &v
		}
		if v, ok := meta.attrValue("property"); ok {
			property = &v
		}
		if content := meta.attr("content"); content != "" {
			tags = append(tags, dfdMetaTag{name: name, property: property, content: content})
		}
	}
	return tags
}

func (d *dfdDefuddle) extractSchemaOrgData(doc *ldNode) []any {
	var raw []any
	for _, script := range ldQuerySelectorAll(doc, `script[type="application/ld+json"]`) {
		jsonContent := script.textContent()
		jsonContent = dfdCleanJSONLD(jsonContent)
		v, err := jsonParse(jsonContent)
		if err != nil {
			continue
		}
		switch data := v.(type) {
		case *jsObj:
			if g, ok := data.get("@graph"); ok {
				if arr, ok := g.([]any); ok {
					raw = append(raw, arr...)
					continue
				}
			}
			raw = append(raw, data)
		case nil:
			// null['@graph'] throws: the original logs it and goes on.
		default:
			raw = append(raw, data)
		}
	}
	if raw == nil {
		raw = []any{}
	}
	return raw
}

var (
	reCDATAWrap   = jsRE(`^\s*<!\[CDATA\[([\s\S]*?)\]\]>\s*$`, "")
	reCommentEdge = jsRE(`^\s*(\*/|/\*)\s*|\s*(\*/|/\*)\s*$`, "")
)

func dfdCleanJSONLD(s string) string {
	s = dfdStripJSComments(s)
	s = reCDATAWrap.ReplaceAllString(s, "$1")
	s = reCommentEdge.ReplaceAllString(s, "")
	return jsTrim(s)
}

// dfdStripJSComments is s.replace(/\/\*[\s\S]*?\*\/|^\s*\/\/.*$/gm, ”): block comments anywhere, and line
// comments that start a line, where a line ends at \n, \r, \u2028 or \u2029.
func dfdStripJSComments(s string) string {
	var b strings.Builder
	isTerm := func(r rune) bool { return r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029' }
	i := 0
	for i < len(s) {
		if strings.HasPrefix(s[i:], "/*") {
			if end := strings.Index(s[i+2:], "*/"); end >= 0 {
				i = i + 2 + end + 2
				continue
			}
		}
		atLineStart := i == 0
		if !atLineStart {
			r, _ := lastRune(s[:i])
			atLineStart = isTerm(r)
		}
		if atLineStart {
			j := i
			for j < len(s) {
				r, size := decodeRune(s[j:])
				if !jsSpace(r) {
					break
				}
				j += size
			}
			if strings.HasPrefix(s[j:], "//") {
				k := j + 2
				for k < len(s) {
					r, size := decodeRune(s[k:])
					if isTerm(r) {
						break
					}
					k += size
				}
				i = k
				continue
			}
		}
		_, size := decodeRune(s[i:])
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

// sortStable sorts by less with swap, stably (insertion into a stable merge): a small helper for the one sort here.
func sortStable(n int, less func(i, j int) bool, swap func(i, j int)) {
	for i := 1; i < n; i++ {
		for j := i; j > 0 && less(j, j-1); j-- {
			swap(j, j-1)
		}
	}
}

// sanitizeExtractorHTML is Defuddle._sanitizeExtractorHtml.
func (d *dfdDefuddle) sanitizeExtractorHTML(html string) string {
	if html == "" {
		return html
	}
	container := ldNewElement("div")
	container.appendChild(ldParseFragment(html))
	d.stripUnsafeElements(container)
	d.resolveRelativeURLs(container)
	dfdStandardizeExtractorOutput(container)
	return ldInnerHTML(container)
}

var _ = strconv.Itoa
