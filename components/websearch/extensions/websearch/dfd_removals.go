package websearch

import (
	"math"
	"strings"
	"sync"
)

// Port of defuddle 0.19.3 dist/removals/selectors.js, hidden.js, small-images.js and metadata-block.js.

// ---- the partial selectors -------------------------------------------------------------------------------------

// The partial selectors are one case-insensitive alternation in the original. Three of its alternatives use
// lookbehind or lookahead, which RE2 lacks: they are matched by hand (dfdPartialLookaround). Of the others 543 are
// plain ASCII text, and testing a class or an id against an alternation of that size is the slowest step of the
// pipeline in Go's regexp: those are looked up by their first two characters, and only the few that are expressions
// (and any text that is not ASCII, whose case folding is Unicode's) go through the expression.
type dfdPartialSet struct {
	all, allAnchored   *lazyRE // every alternative without lookaround, as the original compiles them
	rest, restAnchored *lazyRE // the alternatives that are expressions
	plain              map[string]bool
	pair               [128][128]bool
	byPair             map[uint16][]string
}

const dfdRegexpMeta = `\^$.*+?()[]{}|`

var dfdPartials = sync.OnceValue(func() *dfdPartialSet {
	set := &dfdPartialSet{plain: map[string]bool{}, byPair: map[uint16][]string{}}
	var alts, rest []string
	for _, p := range dfdPartialSelectors {
		if strings.Contains(p, "(?<") || strings.Contains(p, "(?!") {
			continue
		}
		alts = append(alts, p)
		if len(p) < 2 || !cssIsASCII(p) || strings.ContainsAny(p, dfdRegexpMeta) {
			rest = append(rest, p)
			continue
		}
		lp := strings.ToLower(p)
		set.plain[lp] = true
		set.pair[lp[0]][lp[1]] = true
		key := uint16(lp[0])<<8 | uint16(lp[1])
		set.byPair[key] = append(set.byPair[key], lp)
	}
	joined := strings.Join(alts, "|")
	set.all, set.allAnchored = mustRE("(?i)"+joined), mustRE("(?i)^(?:"+joined+")$")
	restJoined := strings.Join(rest, "|")
	set.rest, set.restAnchored = mustRE("(?i)"+restJoined), mustRE("(?i)^(?:"+restJoined+")$")
	return set
})

// containsPlain reports whether the lower-case ASCII text holds one of the plain alternatives.
func (set *dfdPartialSet) containsPlain(ls string) bool {
	for i := 0; i+1 < len(ls); i++ {
		if set.pair[ls[i]][ls[i+1]] {
			for _, p := range set.byPair[uint16(ls[i])<<8|uint16(ls[i+1])] {
				if strings.HasPrefix(ls[i:], p) {
					return true
				}
			}
		}
	}
	return false
}

func dfdIsWordOrDash(c byte) bool {
	return c == '_' || c == '-' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// dfdPartialLookaround matches the three alternatives with lookaround: (?<!main-)access-wall,
// (?:(?<![\w-])logo|logo(?![\w-])) and (?<!h[1-6]-)related.
func dfdPartialLookaround(s string) bool {
	ls := strings.ToLower(s)
	for from := 0; ; {
		i := strings.Index(ls[from:], "access-wall")
		if i < 0 {
			break
		}
		i += from
		if !(i >= 5 && ls[i-5:i] == "main-") {
			return true
		}
		from = i + 1
	}
	for from := 0; ; {
		i := strings.Index(ls[from:], "logo")
		if i < 0 {
			break
		}
		i += from
		if i == 0 || !dfdIsWordOrDash(ls[i-1]) || i+4 >= len(ls) || !dfdIsWordOrDash(ls[i+4]) {
			return true
		}
		from = i + 1
	}
	for from := 0; ; {
		i := strings.Index(ls[from:], "related")
		if i < 0 {
			break
		}
		i += from
		if !(i >= 3 && ls[i-3] == 'h' && ls[i-2] >= '1' && ls[i-2] <= '6' && ls[i-1] == '-') {
			return true
		}
		from = i + 1
	}
	return false
}

func dfdPartialMatch(s string) bool {
	set := dfdPartials()
	if !cssIsASCII(s) {
		return set.all.MatchString(s) || dfdPartialLookaround(s)
	}
	return set.containsPlain(strings.ToLower(s)) || set.rest.MatchString(s) || dfdPartialLookaround(s)
}

// dfdPartialMatchAnchored is the anchored variant: the whole string must be one selector token.
func dfdPartialMatchAnchored(s string) bool {
	set := dfdPartials()
	if cssIsASCII(s) {
		if set.plain[strings.ToLower(s)] || set.restAnchored.MatchString(s) {
			return true
		}
	} else if set.allAnchored.MatchString(s) {
		return true
	}
	switch strings.ToLower(s) {
	case "access-wall", "logo", "related":
		return true
	}
	return false
}

// ---- selectors.js ----------------------------------------------------------------------------------------------

var (
	reHeadingTag  = jsRE(`^H[1-6]$`, "")
	reIDDelimiter = jsRE(`[\s_\-:.]`, "")
)

type dfdOrdered struct {
	order []*ldNode
	set   map[*ldNode]bool
}

func newDfdOrdered() *dfdOrdered { return &dfdOrdered{set: map[*ldNode]bool{}} }

func (o *dfdOrdered) add(n *ldNode) {
	if !o.set[n] {
		o.set[n] = true
		o.order = append(o.order, n)
	}
}

func dfdRemoveBySelector(doc *ldNode, removeExact, removePartial bool, mainContent *ldNode, skipHiddenExactSelectors bool) {
	toRemove := newDfdOrdered()
	if removeExact {
		for _, el := range ldQuerySelectorAll(doc, dfdExactSelectorsJoined) {
			if el.parent == nil {
				continue
			}
			if skipHiddenExactSelectors {
				hiddenAncestor := ldClosest(el, dfdHiddenExactSkipSelector)
				role := strings.ToLower(el.attr("role"))
				if ldMatches(el, dfdHiddenExactSelector) || (hiddenAncestor != nil && role == "dialog") {
					continue
				}
			}
			if ldClosest(el, "pre, code") != nil {
				continue
			}
			if ldMatches(el, dfdHiddenExactSelector) && dfdHasResponsiveShowClass(dfdClassName(el)) {
				continue
			}
			toRemove.add(el)
		}
	}
	if removePartial {
		all := newDfdOrdered()
		for _, el := range ldQuerySelectorAll(doc, dfdTestAttributesSelector) {
			all.add(el)
		}
		if mainContent != nil {
			for _, el := range ldQuerySelectorAll(mainContent, dfdTestAttributesSelector) {
				all.add(el)
			}
		}
		partial := newDfdOrdered()
		for _, el := range all.order {
			if toRemove.set[el] {
				continue
			}
			if ldClosest(el, "[data-defuddle]") != nil {
				continue
			}
			tag := el.tagName()
			if tag == "CODE" || tag == "PRE" || ldQuerySelector(el, "pre") != nil || ldClosest(el, "code, pre") != nil {
				continue
			}
			isHeading := reHeadingTag.MatchString(tag)
			var attrs string
			if isHeading {
				attrs = strings.ToLower(dfdClassName(el))
			} else {
				attrs = strings.ToLower(dfdClassName(el) + " " + el.attr("data-component") + " " + el.attr("data-test") + " " +
					el.attr("data-testid") + " " + el.attr("data-test-id") + " " + el.attr("data-qa") + " " + el.attr("data-cy"))
			}
			id := ""
			if !isHeading {
				id = strings.ToLower(el.id())
			}
			hasAttrs := jsTrim(attrs) != ""
			if !hasAttrs && id == "" {
				continue
			}
			attrsMatch := hasAttrs && dfdPartialMatch(attrs)
			idHasDelimiter := id != "" && reIDDelimiter.MatchString(id)
			idMatch := false
			if id != "" {
				if idHasDelimiter {
					idMatch = dfdPartialMatch(id)
				} else {
					idMatch = dfdPartialMatchAnchored(id)
				}
			}
			if attrsMatch || idMatch {
				partial.add(el)
			}
		}
		for _, el := range partial.order {
			toRemove.add(el)
		}
	}
	for _, el := range toRemove.order {
		if mainContent != nil && el.contains(mainContent) {
			continue
		}
		if el.tagName() == "A" && ldClosest(el, "h1, h2, h3, h4, h5, h6") != nil {
			continue
		}
		if skip := dfdFootnoteProtected(el); skip {
			continue
		}
		if el.tagName() == "BUTTON" && ldQuerySelector(el, "img, picture, video") != nil {
			if parent := el.parentElement(); parent != nil {
				for _, media := range ldQuerySelectorAll(el, "img, picture, video") {
					parent.insertBefore(media, el)
				}
				el.remove()
			}
			continue
		}
		if el.tagName() == "BUTTON" && ldClosest(el, "p, li, td, th, span, h1, h2, h3, h4, h5, h6") != nil {
			el.replaceWith(el.childNodes()...)
			continue
		}
		el.remove()
	}
}

// dfdFootnoteProtected is the try block of the removal loop: footnote lists, their parents and their backlinks stay.
// An exception from a selector is swallowed, as the original's empty catch does.
func dfdFootnoteProtected(el *ldNode) (skip bool) {
	th := ldTry(func() {
		if ldMatches(el, dfdFootnoteListSelectors) || ldQuerySelector(el, dfdFootnoteListSelectors) != nil {
			skip = true
			return
		}
		if parent := el.parentElement(); parent != nil && ldMatches(parent, dfdFootnoteListSelectors) {
			skip = true
			return
		}
		if el.hasClass("footnote-backref") && ldClosest(el, "#footnotes") != nil {
			skip = true
		}
	})
	if th != nil {
		return false
	}
	return skip
}

// ---- hidden.js -------------------------------------------------------------------------------------------------

var reHiddenStyle = jsRE(`(?:^|;\s*)(?:display\s*:\s*none|visibility\s*:\s*hidden|opacity\s*:\s*0)(?:\s*;|\s*$)`, "i")

func dfdRemoveHiddenElements(doc *ldNode) {
	toRemove := newDfdOrdered()
	for _, element := range ldQuerySelectorAll(doc, "*") {
		if ldQuerySelector(element, "math, [data-mathml], .katex-mathml") != nil || element.name == "math" {
			continue
		}
		if style, ok := element.getAttribute("style"); ok && style != "" && reHiddenStyle.MatchString(style) {
			toRemove.add(element)
			continue
		}
		className := element.attr("class")
		if className != "" {
			tokens := jsSplitWS(className)
			if dfdHasResponsiveShowClass(className) {
				continue
			}
			for _, token := range tokens {
				isExact := token == "hidden" || token == "invisible"
				isVariant := !strings.Contains(token, "[") && (strings.HasSuffix(token, ":hidden") || strings.HasSuffix(token, ":invisible"))
				if isExact || isVariant {
					toRemove.add(element)
					break
				}
			}
		}
	}
	for _, el := range toRemove.order {
		el.remove()
	}
}

// ---- small-images.js -------------------------------------------------------------------------------------------

var (
	reStyleWidth   = jsRE(`width\s*:\s*(\d+)`, "")
	reStyleHeight  = jsRE(`height\s*:\s*(\d+)`, "")
	reURLWidth     = jsRE(`(?:width[=:/]|[/,?&]w[_:=])(\d+)`, "")
	reViewBoxSplit = jsRE(`[\s,]+`, "")
	reOneX         = jsRE(`(\S+)\s+1x`, "")
	reLooksLatex   = jsRE(`\\[a-zA-Z]{2,}`, "")
	reB64DataURL   = mustRE(`^data:image/([^;]+);base64,`)
)

func dfdElementIdentifier(el *ldNode) (string, bool) {
	if el.name == "img" {
		if ds, ok := el.getAttribute("data-src"); ok && ds != "" {
			return "src:" + ds, true
		}
		src := el.attr("src")
		srcset := el.attr("srcset")
		dataSrcset, _ := el.getAttribute("data-srcset")
		if src != "" {
			return "src:" + src, true
		}
		if srcset != "" {
			return "srcset:" + srcset, true
		}
		if dataSrcset != "" {
			return "srcset:" + dataSrcset, true
		}
	}
	id := el.id()
	className := dfdClassName(el)
	viewBox := ""
	if el.name == "svg" {
		viewBox = el.attr("viewBox")
	}
	switch {
	case id != "":
		return "id:" + id, true
	case viewBox != "":
		return "viewBox:" + viewBox, true
	case className != "":
		return "class:" + className, true
	}
	return "", false
}

func dfdFindSmallImages(doc *ldNode) map[string]bool {
	const minDimension = 33
	small := map[string]bool{}
	for _, element := range ldQuerySelectorAll(doc, "img, svg") {
		attrWidth := jsParseInt(orZero(element.attr("width")))
		attrHeight := jsParseInt(orZero(element.attr("height")))
		viewBoxWidth, viewBoxHeight := 0.0, 0.0
		if element.name == "svg" {
			if viewBox, ok := element.getAttribute("viewBox"); ok && viewBox != "" {
				if parts := reViewBoxSplit.Split(viewBox, -1); len(parts) == 4 {
					viewBoxWidth = jsOr0(jsParseFloat(parts[2]))
					viewBoxHeight = jsOr0(jsParseFloat(parts[3]))
				}
			}
		}
		style := element.attr("style")
		styleWidth := jsParseInt(orZero(firstSubmatch(reStyleWidth, style)))
		styleHeight := jsParseInt(orZero(firstSubmatch(reStyleHeight, style)))
		var widths, heights []float64
		for _, d := range []float64{attrWidth, styleWidth, 0, viewBoxWidth} {
			if d > 0 {
				widths = append(widths, d)
			}
		}
		for _, d := range []float64{attrHeight, styleHeight, 0, viewBoxHeight} {
			if d > 0 {
				heights = append(heights, d)
			}
		}
		if len(widths) == 0 && len(heights) == 0 && element.name == "img" {
			srcset := element.attr("srcset")
			if m := reOneX.FindStringSubmatch(srcset); m != nil {
				if urlWidth := jsParseInt(orZero(firstSubmatch(reURLWidth, m[1]))); urlWidth > 0 {
					widths = append(widths, urlWidth)
				}
			}
		}
		if len(widths) > 0 || len(heights) > 0 {
			effW, effH := math.Inf(1), math.Inf(1)
			for _, w := range widths {
				effW = math.Min(effW, w)
			}
			for _, h := range heights {
				effH = math.Min(effH, h)
			}
			if effW < minDimension || effH < minDimension {
				if element.name == "img" {
					alt := element.attr("alt")
					if reLooksLatex.MatchString(alt) {
						continue
					}
					if element.hasClass("latex") || element.hasClass("tex") {
						continue
					}
					if element.attr("data-latex") != "" || element.attr("data-math") != "" {
						continue
					}
				}
				if id, ok := dfdElementIdentifier(element); ok {
					small[id] = true
				}
			}
		}
	}
	return small
}

// orZero is `s || '0'`.
func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

// firstSubmatch is `s.match(re)?.[1]`, "" when there is no match.
func firstSubmatch(re *lazyRE, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// dfdIsBase64Placeholder is images.isBase64Placeholder.
func dfdIsBase64Placeholder(src string) bool {
	m := reB64DataURL.FindStringSubmatch(src)
	if m == nil {
		return false
	}
	if m[1] == "svg+xml" {
		return false
	}
	return jsLen(src)-jsLen(m[0]) < 133
}

func dfdRemoveSmallImages(doc *ldNode, small map[string]bool) {
	for _, tag := range []string{"img", "svg"} {
		for _, element := range doc.getElementsByTagName(tag) {
			if tag == "img" {
				src := element.attr("src")
				hasAltSrc := element.attr("srcset") != "" || element.attr("data-src") != "" || element.attr("data-srcset") != "" ||
					element.attr("data-lazy-src") != "" || element.attr("data-original") != ""
				if src == "" && !hasAltSrc {
					element.remove()
					continue
				}
				if !hasAltSrc && ldClosest(element, "picture") == nil && dfdIsBase64Placeholder(src) {
					element.remove()
					continue
				}
			}
			if id, ok := dfdElementIdentifier(element); ok && small[id] {
				element.remove()
			}
		}
	}
}

// ---- metadata-block.js -----------------------------------------------------------------------------------------

const dfdMonthAlt = `(?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:t(?:ember)?)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)`

var reDateBlock = jsRE(`\b(?:`+dfdMonthAlt+`\s+\d{1,2}[\s,]+\d{4}|\d{1,2}(?:st|nd|rd|th)?\s+`+dfdMonthAlt+`\s+\d{4}|\d{4}[-/]\d{1,2}[-/]\d{1,2})\b`, "i")

func dfdRemoveMetadataBlock(mainContent *ldNode) {
	h1 := ldQuerySelector(mainContent, "h1")
	if h1 == nil {
		return
	}
	sibling := h1.nextElementSibling()
	for i := 0; i < 3 && sibling != nil; i++ {
		next := sibling.nextElementSibling()
		text := jsTrim(sibling.textContent())
		if l := jsLen(text); l > 0 && l < 300 {
			hasDate := reDateBlock.MatchString(text)
			if !hasDate {
				for _, el := range ldQuerySelectorAll(sibling, "p, time") {
					if reDateBlock.MatchString(jsTrim(el.textContent())) {
						hasDate = true
						break
					}
				}
			}
			if hasDate {
				sibling.remove()
				break
			}
		}
		sibling = next
	}
}
