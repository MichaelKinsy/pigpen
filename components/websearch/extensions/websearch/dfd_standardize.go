package websearch

import (
	"sort"
	"strconv"
	"strings"
)

// Port of defuddle 0.19.3 dist/standardize.js.

type dfdRule struct {
	selector  string
	fastCheck string
	transform func(el *ldNode) *ldNode
}

func dfdStandardizeRules() []dfdRule {
	rules := []dfdRule{}
	rules = append(rules, dfdMathRules()...)
	rules = append(rules, dfdRule{selector: strings.Join([]string{"pre", `div[class*="prismjs"]`, ".syntaxhighlighter", ".highlight", ".highlight-source", ".wp-block-syntaxhighlighter-code", ".wp-block-code", `div[class*="language-"]`, ".code-block[data-lang]", "code.hl.block"}, ", "), transform: dfdCodeTransform})
	rules = append(rules, dfdRule{selector: "h1, h2, h3, h4, h5, h6", transform: dfdHeadingTransform})
	rules = append(rules,
		dfdRule{selector: "picture", transform: dfdPictureTransform},
		dfdRule{selector: "uni-image-full-width", transform: dfdUniImageTransform},
		dfdRule{selector: `img[data-src], img[data-srcset], img[loading="lazy"], img.lazy, img.lazyload, img[src^="data:image/svg+xml"]`, transform: dfdLazyImageTransform},
		dfdRule{selector: "span:has(img)", transform: dfdSpanImageTransform},
		dfdRule{selector: `figure, p:has([class*="caption"])`, transform: dfdFigureTransform},
		dfdRule{selector: `div[data-testid^="paragraph"], div[role="paragraph"]`, transform: func(el *ldNode) *ldNode {
			p := ldNewElement("p")
			dfdTransferContent(el, p)
			for _, a := range append([]ldAttr(nil), el.attrs...) {
				if dfdAllowedAttributes[a.name] {
					p.setAttribute(a.name, a.value)
				}
			}
			return p
		}},
		dfdRule{selector: `div[role="list"]`, transform: dfdRoleListTransform},
		dfdRule{selector: `div[role="listitem"]`, transform: func(el *ldNode) *ldNode {
			content := ldQuerySelector(el, ".content")
			if content == nil {
				return el
			}
			for _, div := range ldQuerySelectorAll(content, `div[role="paragraph"]`) {
				p := ldNewElement("p")
				dfdTransferContent(div, p)
				div.replaceWith(p)
			}
			return content
		}},
	)
	return rules
}

var reOrderedLabel = jsRE(`^\d+\)`, "")

func dfdRoleListTransform(el *ldNode) *ldNode {
	label := ""
	if first := ldQuerySelector(el, `div[role="listitem"] .label`); first != nil {
		label = jsTrim(first.textContent())
	}
	tag := "ul"
	if reOrderedLabel.MatchString(label) {
		tag = "ol"
	}
	list := ldNewElement(tag)
	for _, item := range ldQuerySelectorAll(el, `div[role="listitem"]`) {
		li := ldNewElement("li")
		if content := ldQuerySelector(item, ".content"); content != nil {
			for _, div := range ldQuerySelectorAll(content, `div[role="paragraph"]`) {
				p := ldNewElement("p")
				dfdTransferContent(div, p)
				div.replaceWith(p)
			}
			for _, nested := range ldQuerySelectorAll(content, `div[role="list"]`) {
				nestedLabel := ""
				if f := ldQuerySelector(nested, `div[role="listitem"] .label`); f != nil {
					nestedLabel = jsTrim(f.textContent())
				}
				nt := "ul"
				if reOrderedLabel.MatchString(nestedLabel) {
					nt = "ol"
				}
				newNested := ldNewElement(nt)
				for _, nestedItem := range ldQuerySelectorAll(nested, `div[role="listitem"]`) {
					nestedLi := ldNewElement("li")
					if nestedContent := ldQuerySelector(nestedItem, ".content"); nestedContent != nil {
						for _, div := range ldQuerySelectorAll(nestedContent, `div[role="paragraph"]`) {
							p := ldNewElement("p")
							dfdTransferContent(div, p)
							div.replaceWith(p)
						}
						dfdTransferContent(nestedContent, nestedLi)
					}
					newNested.appendChild(nestedLi)
				}
				nested.replaceWith(newNested)
			}
			dfdTransferContent(content, li)
		}
		list.appendChild(li)
	}
	return list
}

// dfdStandardizeContent is standardizeContent with the options Defuddle's call uses (debug off).
func dfdStandardizeContent(element *ldNode, title string, doc *ldNode) {
	dfdStandardizeDropCaps(element)
	dfdStandardizeSpaces(element)
	dfdRemoveHTMLComments(element)
	dfdStandardizeHeadings(element, title)
	dfdWrapPreformattedCode(element)
	dfdStandardizeElements(element, doc)
	dfdResolveSvgColors(element)
	dfdReplaceCustomElements(element)
	dfdConvertDataAsSpans(element)
	dfdConvertBlockSpans(element)
	dfdUnwrapLayoutTables(element)
	dfdFlattenWrapperElements(element)
	dfdRemovePermalinkAnchors(element)
	dfdStripUnwantedAttributes(element, false)
	dfdUnwrapBareSpans(element)
	for _, a := range ldQuerySelectorAll(element, "code a") {
		dfdUnwrapElement(a)
	}
	for _, a := range ldQuerySelectorAll(element, `a[href^="javascript:"]`) {
		dfdUnwrapElement(a)
	}
	for _, link := range ldQuerySelectorAll(element, "a") {
		href, ok := link.getAttribute("href")
		if !ok || href == "" || strings.HasPrefix(href, "#") {
			continue
		}
		var heading *ldNode
		for _, c := range link.children() {
			if len(c.name) == 2 && c.name[0] == 'h' && c.name[1] >= '1' && c.name[1] <= '6' {
				heading = c
				break
			}
		}
		if heading == nil {
			continue
		}
		inner := ldNewElement("a")
		inner.setAttribute("href", href)
		for heading.first != nil {
			inner.appendChild(heading.first)
		}
		heading.appendChild(inner)
		dfdUnwrapElement(link)
	}
	for _, link := range ldQuerySelectorAll(element, `a[href^="#"]`) {
		if ldQuerySelector(link, "h1, h2, h3, h4, h5, h6") != nil {
			dfdUnwrapElement(link)
		}
	}
	for _, el := range ldQuerySelectorAll(element, "object, embed, applet") {
		el.remove()
	}
	dfdRemoveEmptyElements(element)
	dfdRemoveTrailingHeadings(element)
	dfdRemoveOrphanedDividers(element)
	dfdFlattenWrapperElements(element)
	dfdRemoveOrphanedDividers(element)
	dfdStripExtraBrElements(element)
	dfdRemoveEmptyLines(element)
}

func dfdWrapPreformattedCode(element *ldNode) {
	for _, code := range ldQuerySelectorAll(element, "code") {
		if ldClosest(code, "pre") != nil {
			continue
		}
		if !jsRE(`white-space\s*:\s*pre`, "").MatchString(code.attr("style")) {
			continue
		}
		pre := ldNewElement("pre")
		if code.parent != nil {
			code.parent.insertBefore(pre, code)
		}
		pre.appendChild(code)
	}
}

func dfdStandardizeSpaces(element *ldNode) {
	var process func(n *ldNode)
	process = func(n *ldNode) {
		if n.kind == ldElem {
			if n.name == "pre" || n.name == "code" || dfdIsSVG(n) {
				return
			}
		}
		if n.kind == ldText {
			if t := strings.ReplaceAll(n.data, "\u00a0", " "); t != n.data {
				n.data = t
			}
		}
		if n.first != nil {
			for _, c := range n.childNodes() {
				process(c)
			}
		}
	}
	process(element)
}

func dfdRemoveTrailingHeadings(element *ldNode) {
	var hasContentAfter func(el *ldNode) bool
	hasContentAfter = func(el *ldNode) bool {
		var next strings.Builder
		for s := el.next; s != nil; s = s.next {
			switch s.kind {
			case ldText:
				next.WriteString(s.data)
			case ldElem:
				next.WriteString(s.textContent())
			}
		}
		if jsTrim(next.String()) != "" {
			return true
		}
		if parent := el.parentElement(); parent != nil && parent != element {
			return hasContentAfter(parent)
		}
		return false
	}
	headings := ldQuerySelectorAll(element, "h1, h2, h3, h4, h5, h6")
	for i := len(headings) - 1; i >= 0; i-- {
		if !hasContentAfter(headings[i]) {
			headings[i].remove()
		} else {
			break
		}
	}
}

func dfdRemoveOrphanedDividers(element *ldNode) {
	for {
		node := element.first
		for node != nil && node.kind == ldText && jsTrim(node.data) == "" {
			node = node.next
		}
		if node != nil && node.kind == ldElem && node.name == "hr" {
			node.remove()
		} else {
			break
		}
	}
	for {
		node := element.last
		for node != nil && node.kind == ldText && jsTrim(node.data) == "" {
			node = node.prev
		}
		if node != nil && node.kind == ldElem && node.name == "hr" {
			node.remove()
		} else {
			break
		}
	}
	for _, hr := range ldQuerySelectorAll(element, "hr") {
		if hr.parent == nil {
			continue
		}
		node := hr.next
		for node != nil {
			if node.kind == ldText && jsTrim(node.data) == "" {
				node = node.next
				continue
			}
			if node.kind == ldElem && node.name == "hr" {
				next := node.next
				node.remove()
				node = next
				continue
			}
			break
		}
	}
}

func dfdStandardizeHeadings(element *ldNode, title string) {
	for _, h1 := range element.getElementsByTagName("h1") {
		h2 := ldNewElement("h2")
		dfdTransferContent(h1, h2)
		for _, a := range append([]ldAttr(nil), h1.attrs...) {
			if dfdAllowedAttributes[a.name] {
				h2.setAttribute(a.name, a.value)
			}
		}
		if h1.parent != nil {
			h1.parent.replaceChild(h2, h1)
		}
	}
	h2s := element.getElementsByTagName("h2")
	if len(h2s) > 0 {
		first := h2s[0]
		permalinkText := ""
		for _, a := range ldQuerySelectorAll(first, "a") {
			if dfdIsPermalinkAnchor(a) {
				permalinkText += a.textContent()
			}
		}
		firstText := dfdNormalizeText(strings.Replace(first.textContent(), permalinkText, "", 1))
		if n := dfdNormalizeText(title); n != "" && n == firstText {
			first.remove()
		}
	}
}

func dfdRemoveHTMLComments(element *ldNode) {
	var comments []*ldNode
	ldWalkAll(element, func(n *ldNode) {
		if n.kind == ldComment {
			comments = append(comments, n)
		}
	})
	for _, c := range comments {
		c.remove()
	}
}

// ---- attributes ------------------------------------------------------------------------------------------------

var reCalloutClass = jsRE(`^callout(?:-|$)`, "")

func dfdStripUnwantedAttributes(element *ldNode, extractorOutput bool) {
	process := func(el *ldNode) {
		if dfdIsSVG(el) {
			if el.hasAttribute("class") {
				el.removeAttribute("class")
			}
			return
		}
		tag := el.name
		for _, a := range append([]ldAttr(nil), el.attrs...) {
			attrName := strings.ToLower(a.name)
			attrValue := a.value
			if (attrName == "id" && (strings.HasPrefix(attrValue, "fnref:") || strings.HasPrefix(attrValue, "fn:") || attrValue == "footnotes")) ||
				(attrName == "class" && ((tag == "code" && strings.HasPrefix(attrValue, "language-")) || attrValue == "footnote-backref" || reCalloutClass.MatchString(attrValue))) ||
				(extractorOutput && attrName == "data-defuddle") {
				continue
			}
			if extractorOutput && attrName == "class" {
				kept := dfdFilterExtractorClasses(attrValue)
				if kept != "" {
					if kept != attrValue {
						el.setAttribute("class", kept)
					}
					continue
				}
			}
			if !dfdAllowedAttributes[attrName] {
				el.removeAttribute(a.name)
			}
		}
	}
	process(element)
	for _, el := range ldQuerySelectorAll(element, "*") {
		process(el)
	}
}

func dfdUnwrapElement(el *ldNode) {
	for el.first != nil {
		if el.parent != nil {
			el.parent.insertBefore(el.first, el)
		} else {
			break
		}
	}
	el.remove()
}

func dfdUnwrapLayoutTables(element *ldNode) {
	for _, table := range ldQuerySelectorAll(element, "table") {
		if table.parent == nil {
			continue
		}
		if ldQuerySelector(table, "thead, tfoot, th, caption") != nil {
			continue
		}
		var nonEmpty []*ldNode
		for _, td := range ldQuerySelectorAll(table, ":scope > tbody > tr > td, :scope > tr > td") {
			if jsTrim(td.textContent()) != "" {
				nonEmpty = append(nonEmpty, td)
			}
		}
		if len(nonEmpty) != 1 {
			continue
		}
		var children []*ldNode
		for _, c := range nonEmpty[0].children() {
			if jsTrim(c.textContent()) != "" {
				children = append(children, c)
			}
		}
		if len(children) == 1 && dfdBlockLevelElements[children[0].name] {
			table.replaceWith(children[0])
		}
	}
}

func dfdReplaceCustomElements(element *ldNode) {
	var custom []*ldNode
	for _, el := range ldQuerySelectorAll(element, "*") {
		if strings.Contains(el.tagName(), "-") && !dfdInlineElements[el.name] && !dfdIsSVG(el) {
			custom = append(custom, el)
		}
	}
	for i := len(custom) - 1; i >= 0; i-- {
		el := custom[i]
		if el.parent == nil {
			continue
		}
		div := ldNewElement("div")
		for el.first != nil {
			div.appendChild(el.first)
		}
		el.replaceWith(div)
	}
}

var dfdDataAsAllowed = map[string]bool{"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "li": true, "blockquote": true}

func dfdStandardizeDropCaps(element *ldNode) {
	count := 0
	for _, span := range ldQuerySelectorAll(element, `span[data-caps="initial"]`) {
		if span.parent == nil {
			continue
		}
		next := span.nextElementSibling()
		if next != nil && next.tagName() == "SMALL" {
			merged := ldNewText(span.textContent() + next.textContent())
			span.parent.insertBefore(merged, span)
			next.remove()
			span.remove()
		} else {
			dfdUnwrapElement(span)
		}
		count++
	}
	if count > 0 {
		element.normalize()
	}
}

func dfdConvertDataAsSpans(element *ldNode) {
	for _, span := range ldQuerySelectorAll(element, "span[data-as]") {
		if span.parent == nil {
			continue
		}
		target := strings.ToLower(span.attr("data-as"))
		if !dfdDataAsAllowed[target] {
			continue
		}
		replacement := ldNewElement(target)
		dfdTransferContent(span, replacement)
		span.replaceWith(replacement)
	}
}

var (
	reTWBlock      = jsRE(`(?:^|\s)block(?:\s|$)`, "")
	reDisplayBlock = jsRE(`display\s*:\s*block`, "i")
)

func dfdConvertBlockSpans(element *ldNode) {
	for _, span := range ldQuerySelectorAll(element, `span[class*="block"], span[style*="block"]`) {
		if span.parent == nil {
			continue
		}
		if !(reTWBlock.MatchString(dfdClassName(span)) || reDisplayBlock.MatchString(span.attr("style"))) {
			continue
		}
		if jsTrim(span.textContent()) == "" {
			continue
		}
		p := ldNewElement("p")
		dfdTransferContent(span, p)
		span.replaceWith(p)
	}
}

func dfdUnwrapBareSpans(element *ldNode) {
	spans := ldQuerySelectorAll(element, "span")
	unwrapped := 0
	for i := len(spans) - 1; i >= 0; i-- {
		span := spans[i]
		if span.parent == nil {
			continue
		}
		if len(span.attrs) > 0 {
			continue
		}
		parent := span.parent
		for span.first != nil {
			parent.insertBefore(span.first, span)
		}
		span.remove()
		unwrapped++
	}
	if unwrapped > 0 {
		element.normalize()
	}
}

// ---- svg colours -----------------------------------------------------------------------------------------------

var (
	reLightDark   = jsRE(`light-dark\(\s*([^,]+?)\s*,\s*[^)]+?\)`, "")
	reCSSVar      = jsRE(`var\(--([^,)]+)(?:,\s*([^)]+))?\)`, "")
	reTWName      = mustRE(`(?:^|-)([a-z]+)-(\d{2,3})$`)
	reCSSVarAll   = jsRE(`var\(--[^,)]+(?:,\s*[^)]+)?\)`, "")
	reTWColor     = mustRE(`^(fill|stroke)-([a-z]+)-(\d{2,3})(?:/(\d+))?$`)
	reTWSpecial   = mustRE(`^(fill|stroke)-(black|white|transparent|current)$`)
	reTWArbitrary = jsRE(`^text-\[(.+)\]$`, "")
	reClosedPath  = jsRE(`Z\s*$`, "i")
)

var dfdSvgColorAttrs = []string{"fill", "stroke", "color", "stop-color", "flood-color", "lighting-color"}

func dfdResolveSvgColors(element *ldNode) {
	svgs := ldQuerySelectorAll(element, "svg")
	if len(svgs) == 0 {
		return
	}
	unwrapLight := func(v string) string {
		return jsReplaceAll(reLightDark, v, func(m []string) string { return jsTrim(m[1]) })
	}
	resolveVar := func(value string) string {
		value = unwrapLight(value)
		if !strings.Contains(value, "var(") {
			return value
		}
		if m := reCSSVar.FindStringSubmatch(value); m != nil {
			if fb := jsTrim(m[2]); fb != "" && !strings.Contains(fb, "var(") {
				return fb
			}
			name := strings.ToLower(m[1])
			if tw := reTWName.FindStringSubmatch(name); tw != nil {
				if hex := dfdTailwindColors[tw[1]][tw[2]]; hex != "" {
					return hex
				}
			}
			if strings.HasSuffix(name, "-black") {
				return "#000"
			}
			if strings.HasSuffix(name, "-white") {
				return "#fff"
			}
			if strings.Contains(name, "background") || strings.Contains(name, "card") || strings.Contains(name, "surface") || strings.Contains(name, "bg") {
				return "Canvas"
			}
			if strings.Contains(name, "border") || strings.Contains(name, "divider") || strings.Contains(name, "separator") {
				return "#ccc"
			}
			if strings.Contains(name, "muted") || strings.Contains(name, "subtle") || strings.Contains(name, "secondary") || strings.Contains(name, "placeholder") {
				return "#888"
			}
		}
		return "currentColor"
	}
	for _, svg := range svgs {
		all := append([]*ldNode{svg}, ldQuerySelectorAll(svg, "*")...)
		for _, el := range all {
			for _, attrName := range dfdSvgColorAttrs {
				val, _ := el.getAttribute(attrName)
				if val == "" || (!strings.Contains(val, "var(") && !strings.Contains(val, "light-dark(")) {
					continue
				}
				el.setAttribute(attrName, resolveVar(val))
			}
			if style := el.attr("style"); style != "" && (strings.Contains(style, "var(") || strings.Contains(style, "light-dark(")) {
				resolved := unwrapLight(style)
				resolved = jsReplaceAll(reCSSVarAll, resolved, func(m []string) string { return resolveVar(m[0]) })
				el.setAttribute("style", resolved)
			}
			dfdResolveTailwindClasses(el)
		}
		dfdApplySvgFallbackStyles(svg)
	}
}

var (
	dfdSvgFilled = map[string]bool{"path": true, "rect": true, "circle": true, "ellipse": true, "polygon": true}
	dfdSvgStroke = map[string]bool{"line": true, "polyline": true}
	dfdSvgText   = map[string]bool{"text": true, "tspan": true}
)

const dfdSvgNonRendered = "defs, clipPath, mask, pattern, marker"

func dfdHasStyleProp(el *ldNode, prop string) bool {
	style := el.attr("style")
	if style == "" {
		return false
	}
	return jsRE(`(?:^|;)\s*`+prop+`\s*:`, "").MatchString(style)
}

func dfdApplySvgFallbackStyles(svg *ldNode) {
	if ldQuerySelector(svg, "style") != nil {
		return
	}
	all := ldQuerySelectorAll(svg, "*")
	hasUnstyled := false
	for _, el := range all {
		if !dfdSvgFilled[el.name] || el.attr("class") == "" {
			continue
		}
		if ldClosest(el, dfdSvgNonRendered) != nil {
			continue
		}
		if el.hasAttribute("fill") || dfdHasStyleProp(el, "fill") {
			continue
		}
		hasUnstyled = true
		break
	}
	if !hasUnstyled {
		return
	}
	for _, el := range all {
		tag := el.name
		isFilled, isStroke, isText := dfdSvgFilled[tag], dfdSvgStroke[tag], dfdSvgText[tag]
		if !isFilled && !isStroke && !isText {
			continue
		}
		if el.attr("class") == "" {
			continue
		}
		if ldClosest(el, dfdSvgNonRendered) != nil {
			continue
		}
		if isText {
			if !el.hasAttribute("fill") && !dfdHasStyleProp(el, "fill") {
				el.setAttribute("fill", "currentColor")
			}
			continue
		}
		hasFill := el.hasAttribute("fill") && el.attr("fill") != "none"
		hasStrokeAttr := el.hasAttribute("stroke") || dfdHasStyleProp(el, "stroke")
		if isFilled && !el.hasAttribute("fill") && !dfdHasStyleProp(el, "fill") {
			el.setAttribute("fill", "none")
		}
		if !hasStrokeAttr {
			if isStroke {
				el.setAttribute("stroke", "currentColor")
				if !el.hasAttribute("stroke-opacity") {
					el.setAttribute("stroke-opacity", "0.2")
				}
			} else if isFilled && !hasFill {
				d := el.attr("d")
				if !reClosedPath.MatchString(jsTrim(d)) {
					el.setAttribute("stroke", "currentColor")
				}
			}
		}
	}
}

func dfdResolveTailwindClasses(el *ldNode) {
	className := el.attr("class")
	if className == "" {
		return
	}
	tokens := jsSplitWS(className)
	var keep, styles []string
	for _, token := range tokens {
		if m := reTWColor.FindStringSubmatch(token); m != nil {
			prop, color, shade, opacity := m[1], m[2], m[3], m[4]
			if hex := dfdTailwindColors[color][shade]; hex != "" {
				if opacity != "" {
					a := jsParseInt(opacity) / 100
					r, _ := strconv.ParseInt(hex[1:3], 16, 64)
					g, _ := strconv.ParseInt(hex[3:5], 16, 64)
					b, _ := strconv.ParseInt(hex[5:7], 16, 64)
					el.setAttribute(prop, "rgba("+strconv.FormatInt(r, 10)+","+strconv.FormatInt(g, 10)+","+strconv.FormatInt(b, 10)+","+jsNumberString(a)+")")
				} else {
					el.setAttribute(prop, hex)
				}
				continue
			}
		}
		if m := reTWSpecial.FindStringSubmatch(token); m != nil {
			el.setAttribute(m[1], dfdTailwindSpecial[m[2]])
			continue
		}
		if m := reTWArbitrary.FindStringSubmatch(token); m != nil && !strings.HasPrefix(m[1], "#") && !strings.HasPrefix(m[1], "rgb") && !strings.HasPrefix(m[1], "hsl") {
			styles = append(styles, "font-size:"+m[1])
			continue
		}
		switch token {
		case "font-semibold":
			styles = append(styles, "font-weight:600")
			continue
		case "font-bold":
			styles = append(styles, "font-weight:700")
			continue
		case "font-medium":
			styles = append(styles, "font-weight:500")
			continue
		case "font-mono":
			styles = append(styles, "font-family:monospace")
			continue
		}
		keep = append(keep, token)
	}
	if len(keep) == len(tokens) {
		return
	}
	if len(keep) > 0 {
		el.setAttribute("class", strings.Join(keep, " "))
	} else {
		el.removeAttribute("class")
	}
	if len(styles) > 0 {
		existing := el.attr("style")
		sep := ""
		if existing != "" && !strings.HasSuffix(existing, ";") {
			sep = ";"
		}
		el.setAttribute("style", existing+sep+strings.Join(styles, ";"))
	}
}

// ---- empty elements, breaks, empty lines -----------------------------------------------------------------------

func dfdRemoveEmptyElements(element *ldNode) {
	isEmpty := func(el *ldNode) bool {
		if dfdAllowedEmptyElements[el.name] {
			return false
		}
		if el.tagName() == "DIV" {
			children := el.children()
			if len(children) > 0 {
				all := true
				for _, c := range children {
					if c.tagName() != "SPAN" {
						all = false
						break
					}
					content := jsTrim(c.textContent())
					if content != "," && content != "" && content != " " {
						all = false
						break
					}
				}
				if all {
					return true
				}
			}
		}
		text := el.textContent()
		if len(jsTrim(text)) > 0 || strings.Contains(text, "\u00a0") {
			return false
		}
		if !el.hasChildNodes() {
			return true
		}
		for _, node := range el.childNodes() {
			if node.kind == ldElem && node.name == "br" {
				continue
			}
			if node.kind != ldText {
				return false
			}
			if len(jsTrim(node.data)) > 0 || strings.Contains(node.data, "\u00a0") {
				return false
			}
		}
		return true
	}
	all := ldQuerySelectorAll(element, "*")
	for i := len(all) - 1; i >= 0; i-- {
		el := all[i]
		if el.parent != nil && isEmpty(el) {
			el.remove()
		}
	}
}

func dfdSkipWhitespace(node *ldNode, previous bool) *ldNode {
	step := func(n *ldNode) *ldNode {
		if previous {
			return n.prev
		}
		return n.next
	}
	s := step(node)
	for s != nil && s.kind == ldText && jsTrim(s.data) == "" {
		s = step(s)
	}
	return s
}

func dfdStripExtraBrElements(element *ldNode) {
	var consecutive []*ldNode
	processBrs := func() {
		if len(consecutive) > 2 {
			for _, b := range consecutive[2:] {
				b.remove()
			}
		}
		consecutive = nil
	}
	for _, current := range element.getElementsByTagName("br") {
		isConsecutive := false
		if len(consecutive) > 0 {
			last := consecutive[len(consecutive)-1]
			if dfdSkipWhitespace(current, true) == last {
				isConsecutive = true
			}
		}
		if isConsecutive {
			consecutive = append(consecutive, current)
		} else {
			processBrs()
			consecutive = []*ldNode{current}
		}
	}
	processBrs()
	for _, br := range element.getElementsByTagName("br") {
		parent := br.parentElement()
		if parent == nil {
			continue
		}
		if ldClosest(br, "pre, code") != nil {
			continue
		}
		parentTag := parent.name
		if dfdBlockLevelElements[parentTag] || parentTag == "body" {
			group := []*ldNode{br}
			scan := dfdSkipWhitespace(br, false)
			for scan != nil && scan.kind == ldElem && scan.name == "br" {
				group = append(group, scan)
				scan = dfdSkipWhitespace(scan, false)
			}
			prev := dfdSkipWhitespace(group[0], true)
			next := dfdSkipWhitespace(group[len(group)-1], false)
			prevIsBlock := prev != nil && prev.kind == ldElem && dfdBlockLevelElements[prev.name]
			nextIsBlock := next != nil && next.kind == ldElem && dfdBlockLevelElements[next.name]
			if (prevIsBlock && nextIsBlock) || (prevIsBlock && next == nil) || prev == nil {
				for _, b := range group {
					b.remove()
				}
				continue
			}
		}
		if dfdBlockLevelElements[parentTag] {
			if dfdSkipWhitespace(br, false) == nil {
				br.remove()
			}
		}
	}
}

func dfdMoveWhitespaceOutside(node *ldNode, leading bool) {
	child := node.last
	if leading {
		child = node.first
	}
	if child == nil || child.kind != ldText {
		return
	}
	text := child.data
	var trimmed string
	if leading {
		trimmed = strings.TrimLeftFunc(text, jsSpace)
	} else {
		trimmed = strings.TrimRightFunc(text, jsSpace)
	}
	if trimmed == text || node.parent == nil {
		return
	}
	child.data = trimmed
	neighbor := node.next
	if leading {
		neighbor = node.prev
	}
	has := false
	if neighbor != nil && neighbor.kind == ldText {
		if leading {
			has = strings.HasSuffix(neighbor.data, " ")
		} else {
			has = strings.HasPrefix(neighbor.data, " ")
		}
	}
	if !has {
		before := node.next
		if leading {
			before = node
		}
		node.parent.insertBefore(ldNewText(" "), before)
	}
}

var (
	reZeroWidthOnly   = jsRE(`^[\x{200C}\x{200B}\x{200D}\x{200E}\x{200F}\x{FEFF}]*$`, "")
	reNewlineRuns     = jsRE(`[\n\r]+`, "")
	reTabRuns         = jsRE(`\t+`, "")
	reSpaceRuns2      = jsRE(` {2,}`, "")
	reOnlySpaces      = jsRE(`^[ ]+$`, "")
	reSpaceBeforePunc = jsRE(`\s+([,.!?:;])`, "")
	reZeroWidth       = jsRE(`[\x{200B}\x{200D}\x{200E}\x{200F}\x{FEFF}]+`, "")
	reNBSPRuns        = jsRE(`(?:\x{a0}){2,}`, "")
	reWSInline        = jsRE(`^[\n\r\t\x{200C}\x{200B}\x{200D}\x{200E}\x{200F}\x{FEFF}]*$`, "")
	reNextPunct       = jsRE(`^[,.!?:;)\]]`, "")
	reCurrentPunct    = jsRE(`[,.!?:;(\[]\s*$`, "")
)

func dfdIsFootnoteRef(n *ldNode) bool {
	return n.kind == ldElem && n.name == "sup" && strings.HasPrefix(n.attr("id"), "fnref:")
}

func dfdRemoveEmptyLines(element *ldNode) {
	var removeEmptyText func(n *ldNode)
	removeEmptyText = func(n *ldNode) {
		if n.kind == ldElem && (n.name == "pre" || n.name == "code") {
			return
		}
		for _, c := range n.childNodes() {
			removeEmptyText(c)
		}
		if n.kind == ldText {
			text := n.data
			if text == "" || reZeroWidthOnly.MatchString(text) {
				n.remove()
				return
			}
			nt := reNewlineRuns.ReplaceAllString(text, " ")
			nt = reTabRuns.ReplaceAllString(nt, " ")
			nt = reSpaceRuns2.ReplaceAllString(nt, " ")
			nt = reOnlySpaces.ReplaceAllString(nt, " ")
			nt = reSpaceBeforePunc.ReplaceAllString(nt, "$1")
			nt = reZeroWidth.ReplaceAllString(nt, "")
			nt = reNBSPRuns.ReplaceAllString(nt, "\u00a0")
			if nt != text {
				n.data = nt
			}
		}
	}
	var cleanup func(n *ldNode)
	cleanup = func(n *ldNode) {
		if n.kind != ldElem {
			return
		}
		tag := n.name
		if tag == "pre" || tag == "code" {
			return
		}
		for _, c := range n.childNodes() {
			if c.kind == ldElem {
				cleanup(c)
			}
		}
		n.normalize()
		for n.first != nil && n.first.kind == ldText && reWSInline.MatchString(n.first.data) {
			n.first.remove()
		}
		for n.last != nil && n.last.kind == ldText && reWSInline.MatchString(n.last.data) {
			n.last.remove()
		}
		if dfdInlineElements[tag] && n.parent != nil {
			dfdMoveWhitespaceOutside(n, true)
			dfdMoveWhitespaceOutside(n, false)
		}
		children := n.childNodes()
		for i := 0; i+1 < len(children); i++ {
			cur, next := children[i], children[i+1]
			if cur.kind == ldElem || next.kind == ldElem {
				nextContent := next.textContent()
				curContent := cur.textContent()
				nextIsRef := dfdIsFootnoteRef(next)
				nextPunct := reNextPunct.MatchString(nextContent)
				curPunct := reCurrentPunct.MatchString(curContent)
				hasSpace := (cur.kind == ldText && strings.HasSuffix(cur.data, " ")) || (next.kind == ldText && strings.HasPrefix(next.data, " "))
				if !nextIsRef && !nextPunct && !curPunct && !hasSpace {
					n.insertBefore(ldNewText(" "), next)
				}
			}
		}
	}
	removeEmptyText(element)
	cleanup(element)
}

// ---- standardizeElements ---------------------------------------------------------------------------------------

var reLatexBegin = jsRE(`\\begin\{`, "")

func dfdStandardizeElements(element, doc *ldNode) {
	dfdWrapRawLatexDelimiters(element, doc)
	for _, img := range ldQuerySelectorAll(element, "img[src]") {
		src := img.attr("src")
		if src == "" {
			continue
		}
		latex := dfdExtractLatexFromImageSrc(src)
		if latex == "" {
			alt := img.attr("alt")
			if reLooksLatex.MatchString(alt) {
				latex = alt
			}
		}
		if latex == "" {
			continue
		}
		isBlock := reLatexBegin.MatchString(latex)
		if !isBlock {
			if p := img.parentElement(); p != nil && p.name == "p" && p.first != nil && p.first.next == nil {
				isBlock = true
			}
		}
		mathEl := dfdCreateCleanMathEl(nil, latex, isBlock)
		img.replaceWith(mathEl)
	}
	for _, rule := range dfdStandardizeRules() {
		if rule.fastCheck != "" && ldQuerySelector(element, rule.fastCheck) == nil {
			continue
		}
		var elements []*ldNode
		if th := ldTry(func() { elements = ldQuerySelectorAll(element, rule.selector) }); th != nil {
			continue
		}
		for _, el := range elements {
			transformed := rule.transform(el)
			el.replaceWith(transformed)
		}
	}
	for _, pre := range ldQuerySelectorAll(element, "code > pre") {
		outer := pre.parentElement()
		if outer == nil || outer.tagName() != "CODE" {
			continue
		}
		outer.replaceWith(pre)
	}
	for _, table := range ldQuerySelectorAll(element, "table.ltx_equation, table.ltx_eqn_table, table.ltx_equationgroup") {
		mathElements := ldQuerySelectorAll(table, "math")
		if len(mathElements) == 0 {
			continue
		}
		fragment := ldNewFragment()
		for _, mathEl := range mathElements {
			latex := mathEl.attr("alttext")
			if latex == "" {
				if ann := ldQuerySelector(mathEl, `annotation[encoding="application/x-tex"]`); ann != nil {
					latex = jsTrim(ann.textContent())
				}
			}
			if latex == "" {
				continue
			}
			isBlock := mathEl.attr("display") == "block" || table.hasClass("ltx_equation") || table.hasClass("ltx_equationgroup")
			clean := ldNewElement("math")
			clean.setAttribute("xmlns", "http://www.w3.org/1998/Math/MathML")
			if isBlock {
				clean.setAttribute("display", "block")
			} else {
				clean.setAttribute("display", "inline")
			}
			clean.setAttribute("data-latex", latex)
			clean.setTextContent(latex)
			fragment.appendChild(clean)
		}
		if fragment.first != nil {
			table.replaceWith(fragment)
		}
	}
	for _, outer := range ldQuerySelectorAll(element, "span.ltx_note_outer") {
		outer.remove()
	}
	for _, link := range ldQuerySelectorAll(element, "a.ltx_ref") {
		if ldQuerySelector(link, "span.ltx_ref_tag, span.ltx_text.ltx_ref_tag") != nil {
			link.replaceWith(ldNewText(link.textContent()))
		}
	}
	for _, table := range ldQuerySelectorAll(element, "table") {
		if table.parent == nil {
			continue
		}
		cells := ldQuerySelectorAll(table, "td, th")
		if len(cells) > 0 {
			allEmpty := true
			for _, c := range cells {
				if jsTrim(c.textContent()) != "" {
					allEmpty = false
					break
				}
			}
			if allEmpty && ldQuerySelector(table, "img, picture, video, audio, iframe, svg, math") == nil {
				table.remove()
			}
		}
	}
	for _, table := range ldQuerySelectorAll(element, "table") {
		if table.parent == nil {
			continue
		}
		var directCells []*ldNode
		for _, cell := range ldQuerySelectorAll(table, "td, th") {
			if dfdIsDirectTableChild(cell, table) {
				directCells = append(directCells, cell)
			}
		}
		hasTH := false
		for _, c := range directCells {
			if c.tagName() == "TH" {
				hasTH = true
			}
		}
		if hasTH {
			continue
		}
		var directRows []*ldNode
		for _, row := range ldQuerySelectorAll(table, "tr") {
			if dfdIsDirectTableChild(row, table) {
				directRows = append(directRows, row)
			}
		}
		if len(directRows) == 0 {
			continue
		}
		single := true
		for _, tr := range directRows {
			n := 0
			for _, cell := range directCells {
				if cell.parent == tr {
					n++
				}
			}
			if n > 1 {
				single = false
				break
			}
		}
		if !single {
			continue
		}
		fragment := ldNewFragment()
		for _, cell := range directCells {
			for cell.first != nil {
				fragment.appendChild(cell.first)
			}
		}
		table.replaceWith(fragment)
	}
	for _, el := range ldQuerySelectorAll(element, "video:not([controls])") {
		el.setAttribute("controls", "")
	}
	for _, el := range ldQuerySelectorAll(element, "lite-youtube") {
		videoID := el.attr("videoid")
		if videoID == "" {
			continue
		}
		iframe := ldNewElement("iframe")
		// width, height and frameBorder are plain properties of linkedom's iframe: they set no attribute.
		iframe.setAttribute("src", "https://www.youtube.com/embed/"+videoID)
		title := el.attr("videotitle")
		if title == "" {
			title = "YouTube video player"
		}
		iframe.setAttribute("title", title)
		iframe.setAttribute("allow", "accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share")
		iframe.setAttribute("allowfullscreen", "")
		el.replaceWith(iframe)
	}
	dfdMergeAdjacentVersoCodeBlocks(element)
}

var (
	reVersoLang  = jsRE(`(?:^|\s)language-([a-z0-9_+-]+)(?:\s|$)`, "i")
	reCRLFEnd    = jsRE(`\r?\n$`, "")
	reTrimNLEdge = jsRE(`^\n+|\n+$`, "")
)

func dfdMergeAdjacentVersoCodeBlocks(root *ldNode) {
	getCodeNode := func(pre *ldNode) *ldNode {
		var code *ldNode
		for _, child := range pre.children() {
			if child.name != "code" {
				return nil
			}
			if code != nil {
				return nil
			}
			code = child
		}
		return code
	}
	getLanguage := func(code *ldNode) string {
		if l := strings.ToLower(code.attr("data-lang")); l != "" {
			return l
		}
		if m := reVersoLang.FindStringSubmatch(code.attr("class")); m != nil {
			return strings.ToLower(m[1])
		}
		return ""
	}
	var parents []*ldNode
	seen := map[*ldNode]bool{}
	for _, c := range ldQuerySelectorAll(root, `pre[data-verso-code="true"]`) {
		if p := c.parentElement(); p != nil && !seen[p] {
			seen[p] = true
			parents = append(parents, p)
		}
	}
	for _, container := range parents {
		children := container.childNodes()
		for i := 0; i < len(children); i++ {
			start := children[i]
			if start.kind != ldElem || start.name != "pre" {
				continue
			}
			if start.attr("data-verso-code") != "true" || !start.hasAttribute("data-verso-code") {
				continue
			}
			startCode := getCodeNode(start)
			if startCode == nil {
				continue
			}
			language := getLanguage(startCode)
			if language != "lean" && language != "lean4" {
				continue
			}
			run := []struct{ pre, code *ldNode }{{start, startCode}}
			var between []*ldNode
			j := i + 1
			for j < len(children) {
				node := children[j]
				if node.kind == ldText && jsTrim(node.data) == "" {
					between = append(between, node)
					j++
					continue
				}
				if node.kind != ldElem || node.name != "pre" {
					break
				}
				if node.attr("data-verso-code") != "true" {
					break
				}
				code := getCodeNode(node)
				if code == nil || getLanguage(code) != language {
					break
				}
				run = append(run, struct{ pre, code *ldNode }{node, code})
				j++
			}
			if len(run) <= 1 {
				continue
			}
			parts := make([]string, len(run))
			for k, r := range run {
				parts[k] = reCRLFEnd.ReplaceAllString(r.code.textContent(), "")
			}
			merged := strings.Join(parts, "\n")
			merged = reNL3.ReplaceAllString(merged, "\n\n")
			merged = reTrimNLEdge.ReplaceAllString(merged, "")
			startCode.setTextContent(merged)
			for _, r := range run[1:] {
				r.pre.remove()
			}
			for _, n := range between {
				n.remove()
			}
			i = j - 1
		}
	}
}

// ---- flattenWrapperElements ------------------------------------------------------------------------------------

var (
	reSemanticClass = jsRE(`(?:article|main|content|footnote|reference|bibliography)`, "")
	reWrapperClass  = jsRE(`(?:wrapper|container|layout|row|col|grid|flex|outer|inner|content-area)`, "i")
)

func dfdFlattenWrapperElements(element *ldNode) {
	hasDirectInlineContent := func(el *ldNode) bool {
		for _, c := range el.childNodes() {
			if c.kind == ldText && jsTrim(c.data) != "" {
				return true
			}
			if c.kind == ldElem && dfdInlineElements[c.name] {
				return true
			}
		}
		return false
	}
	shouldPreserve := func(el *ldNode) bool {
		tag := el.name
		if dfdIsSVG(el) {
			return true
		}
		if dfdPreserveElements[tag] {
			return true
		}
		if el.attr("data-callout") != "" || ldClosest(el, "[data-callout]") != nil {
			return true
		}
		if role := el.attr("role"); role != "" {
			switch role {
			case "article", "main", "navigation", "banner", "contentinfo":
				return true
			}
		}
		if cn := dfdClassName(el); cn != "" && reSemanticClass.MatchString(strings.ToLower(cn)) {
			return true
		}
		for _, child := range el.children() {
			if dfdPreserveElements[child.name] || child.attr("role") == "article" {
				return true
			}
			if cn := dfdClassName(child); cn != "" && reSemanticClass.MatchString(strings.ToLower(cn)) {
				return true
			}
		}
		return false
	}
	isWrapper := func(el *ldNode) bool {
		if hasDirectInlineContent(el) {
			return false
		}
		if jsTrim(el.textContent()) == "" {
			return true
		}
		children := el.children()
		if len(children) == 0 {
			return true
		}
		allBlock := true
		for _, c := range children {
			if !dfdBlockLevelElements[c.name] {
				allBlock = false
				break
			}
		}
		if allBlock {
			return true
		}
		if reWrapperClass.MatchString(strings.ToLower(dfdClassName(el))) {
			return true
		}
		textNodes := 0
		for _, n := range el.childNodes() {
			if n.kind == ldText && jsTrim(n.data) != "" {
				textNodes++
			}
		}
		if textNodes == 0 {
			return true
		}
		onlyBlock := len(children) > 0
		for _, c := range children {
			if dfdInlineElements[c.name] {
				onlyBlock = false
				break
			}
		}
		return onlyBlock
	}
	nesting := func(el *ldNode) int {
		depth := 0
		for p := el.parentElement(); p != nil; p = p.parentElement() {
			if dfdBlockElementsSet[p.name] {
				depth++
			}
		}
		return depth
	}
	moveToFragment := func(el *ldNode) {
		fragment := ldNewFragment()
		for el.first != nil {
			fragment.appendChild(el.first)
		}
		el.replaceWith(fragment)
	}
	processElement := func(el *ldNode) bool {
		if el.parent == nil || shouldPreserve(el) {
			return false
		}
		tag := el.name
		if !dfdAllowedEmptyElements[tag] && len(el.children()) == 0 && jsTrim(el.textContent()) == "" {
			el.remove()
			return true
		}
		if el.parentElement() == element {
			children := el.children()
			onlyBlock := len(children) > 0
			for _, c := range children {
				if dfdInlineElements[c.name] {
					onlyBlock = false
					break
				}
			}
			if onlyBlock {
				moveToFragment(el)
				return true
			}
		}
		if isWrapper(el) {
			moveToFragment(el)
			return true
		}
		childNodes := el.childNodes()
		onlyInlineOrText := len(childNodes) > 0
		for _, c := range childNodes {
			if !(c.kind == ldText || (c.kind == ldElem && dfdInlineElements[c.name])) {
				onlyInlineOrText = false
				break
			}
		}
		if onlyInlineOrText && jsTrim(el.textContent()) != "" {
			p := ldNewElement("p")
			for el.first != nil {
				p.appendChild(el.first)
			}
			el.replaceWith(p)
			return true
		}
		if len(el.children()) == 1 {
			child := el.firstElementChild()
			if dfdBlockElementsSet[child.name] && !shouldPreserve(child) {
				el.replaceWith(child)
				return true
			}
		}
		if nesting(el) > 0 && !hasDirectInlineContent(el) {
			moveToFragment(el)
			return true
		}
		return false
	}
	for {
		keep := false
		var top []*ldNode
		for _, el := range element.children() {
			if dfdBlockElementsSet[el.name] {
				top = append(top, el)
			}
		}
		for _, el := range top {
			if processElement(el) {
				keep = true
			}
		}
		all := ldQuerySelectorAll(element, dfdBlockElementsSelector)
		depths := make(map[*ldNode]int, len(all))
		for _, el := range all {
			depths[el] = nesting(el)
		}
		sort.SliceStable(all, func(i, j int) bool { return depths[all[j]] < depths[all[i]] })
		for _, el := range all {
			if processElement(el) {
				keep = true
			}
		}
		for _, el := range ldQuerySelectorAll(element, dfdBlockElementsSelector) {
			children := el.children()
			onlyP := len(children) > 0
			for _, c := range children {
				if c.name != "p" {
					onlyP = false
					break
				}
			}
			if onlyP || (!shouldPreserve(el) && isWrapper(el)) {
				moveToFragment(el)
				keep = true
			}
		}
		if !keep {
			break
		}
	}
}
