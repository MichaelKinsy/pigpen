package websearch

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Port of defuddle 0.19.3 dist/elements/math.base.js and math.full.js.
//
// The MathML to LaTeX conversion is the port of mathml-to-latex (dfd_mathml.go). The LaTeX to MathML rendering
// of temml, which createCleanMathEl uses for an element that has LaTeX but no MathML, is not ported: see
// dfdRenderLatexToMathML and port/PORT.md.

const dfdMathFastCheck = `math, mjx-container, .MathJax, .katex, img.latex, [data-math], [data-latex], script[type^="math/"]`

const dfdMathSelectors = `img.latex[src*="latex.php"],span.MathJax,mjx-container,script[type="math/tex"],script[type="math/tex; mode=display"],.MathJax_Preview + script[type="math/tex"],.MathJax_Display,.MathJax_SVG,.MathJax_MathML,.mwe-math-element,.mwe-math-fallback-image-inline,.mwe-math-fallback-image-display,.mwe-math-mathml-inline,.mwe-math-mathml-display,.katex,.katex-display,.katex-mathml,.katex-html,[data-katex],script[type="math/katex"],math,[data-math],[data-latex],[data-tex],script[type^="math/"],annotation[encoding="application/x-tex"]`

type dfdMathData struct {
	mathml  string
	latex   string
	isBlock bool
}

var (
	dfdMjxTokenTags      = map[string]bool{"mi": true, "mo": true, "mn": true, "mtext": true, "ms": true, "mspace": true, "mglyph": true}
	dfdMjxStructuralTags = map[string]bool{"mrow": true, "mstyle": true, "mpadded": true, "mphantom": true, "menclose": true, "merror": true, "mtable": true, "mtr": true, "mtd": true, "mlabeledtr": true}
	dfdMjxDropTags       = map[string]bool{"mjx-nstrut": true, "mjx-dstrut": true, "mjx-strut": true, "mjx-line": true, "mjx-spacer": true, "mjx-break": true, "mjx-mark": true}
	reInvisibleMath      = regexp.MustCompile("[\u2061\u2062\u2063\u2064]")
)

func dfdNormalizeMathGlyphs(s string) string {
	return reInvisibleMath.ReplaceAllString(norm.NFKC.String(s), "")
}

func dfdDirectChild(node *ldNode, tag string) *ldNode {
	for _, c := range node.children() {
		if c.name == tag {
			return c
		}
	}
	return nil
}

func dfdFlattenMjxChildren(node *ldNode, skip *ldNode) []*ldNode {
	if node == nil {
		return nil
	}
	var out []*ldNode
	for _, child := range node.children() {
		if child == skip {
			continue
		}
		out = append(out, dfdConvertMjxNode(child)...)
	}
	return out
}

func dfdWrapParts(parts []*ldNode) *ldNode {
	if len(parts) == 1 {
		return parts[0]
	}
	mrow := ldNewElement("mrow")
	for _, p := range parts {
		mrow.appendChild(p)
	}
	return mrow
}

func dfdBuildEl(name string, children []*ldNode) *ldNode {
	el := ldNewElement(name)
	for _, c := range children {
		el.appendChild(c)
	}
	return el
}

func dfdConvertMjxNode(node *ldNode) []*ldNode {
	tag := node.name
	if tag == "mjx-c" {
		if text := dfdNormalizeMathGlyphs(node.textContent()); text != "" {
			return []*ldNode{ldNewText(text)}
		}
		return nil
	}
	if dfdMjxDropTags[tag] {
		return nil
	}
	if !strings.HasPrefix(tag, "mjx-") {
		return nil
	}
	name := tag[4:]
	if dfdMjxTokenTags[name] {
		text := dfdNormalizeMathGlyphs(node.textContent())
		if text == "" && name != "mspace" {
			return nil
		}
		el := ldNewElement(name)
		if text != "" {
			el.setTextContent(text)
		}
		return []*ldNode{el}
	}
	if dfdMjxStructuralTags[name] {
		return []*ldNode{dfdBuildEl(name, dfdFlattenMjxChildren(node, nil))}
	}
	switch name {
	case "mfrac":
		num := dfdWrapParts(dfdFlattenMjxChildren(ldQuerySelector(node, "mjx-num"), nil))
		den := dfdWrapParts(dfdFlattenMjxChildren(ldQuerySelector(node, "mjx-den"), nil))
		return []*ldNode{dfdBuildEl("mfrac", []*ldNode{num, den})}
	case "msqrt":
		box := ldQuerySelector(node, "mjx-box")
		if box == nil {
			box = node
		}
		return []*ldNode{dfdBuildEl("msqrt", dfdFlattenMjxChildren(box, nil))}
	case "msub", "msup":
		script := dfdDirectChild(node, "mjx-script")
		base := dfdWrapParts(dfdFlattenMjxChildren(node, script))
		sup := dfdWrapParts(dfdFlattenMjxChildren(script, nil))
		return []*ldNode{dfdBuildEl(name, []*ldNode{base, sup})}
	case "msubsup":
		script := dfdDirectChild(node, "mjx-script")
		base := dfdWrapParts(dfdFlattenMjxChildren(node, script))
		parts := dfdFlattenMjxChildren(script, nil)
		var sub, sup *ldNode
		if len(parts) > 1 {
			sub = parts[len(parts)-1]
			sup = parts[0]
		} else {
			if len(parts) > 0 {
				sub = parts[0]
			} else {
				sub = ldNewElement("mrow")
			}
			sup = ldNewElement("mrow")
		}
		return []*ldNode{dfdBuildEl("msubsup", []*ldNode{base, sub, sup})}
	case "munder", "mover", "munderover":
		base := dfdWrapParts(dfdFlattenMjxChildren(ldQuerySelector(node, "mjx-base"), nil))
		under := ldQuerySelector(node, "mjx-under")
		over := ldQuerySelector(node, "mjx-over")
		switch name {
		case "munder":
			return []*ldNode{dfdBuildEl("munder", []*ldNode{base, dfdWrapParts(dfdFlattenMjxChildren(under, nil))})}
		case "mover":
			return []*ldNode{dfdBuildEl("mover", []*ldNode{base, dfdWrapParts(dfdFlattenMjxChildren(over, nil))})}
		}
		return []*ldNode{dfdBuildEl("munderover", []*ldNode{base, dfdWrapParts(dfdFlattenMjxChildren(under, nil)), dfdWrapParts(dfdFlattenMjxChildren(over, nil))})}
	}
	return dfdFlattenMjxChildren(node, nil)
}

func dfdReconstructMathMLFromMjx(mjxMath *ldNode) *dfdMathData {
	math := ldNewElement("math")
	math.setAttribute("xmlns", "http://www.w3.org/1998/Math/MathML")
	for _, child := range mjxMath.children() {
		for _, n := range dfdConvertMjxNode(child) {
			math.appendChild(n)
		}
	}
	if !math.hasChildNodes() {
		return nil
	}
	isBlock := mjxMath.attr("display") == "true"
	if isBlock {
		math.setAttribute("display", "block")
	}
	return &dfdMathData{mathml: ldOuterHTML(math), isBlock: isBlock}
}

func dfdFlattenTaggedEquationTables(mathEl *ldNode) {
	for _, table := range ldQuerySelectorAll(mathEl, "mtable") {
		var rows []*ldNode
		for _, c := range table.children() {
			if c.name == "mtr" || c.name == "mlabeledtr" {
				rows = append(rows, c)
			}
		}
		if len(rows) != 1 {
			continue
		}
		row := rows[0]
		if row.name != "mlabeledtr" {
			continue
		}
		var cells []*ldNode
		for _, c := range row.children() {
			if c.name == "mtd" {
				cells = append(cells, c)
			}
		}
		if len(cells) < 2 {
			continue
		}
		labelCell, contentCells := cells[0], cells[1:]
		replacement := ldNewElement("mrow")
		for _, cell := range contentCells {
			for cell.first != nil {
				replacement.appendChild(cell.first)
			}
		}
		if labelCell.hasChildNodes() {
			spacer := ldNewElement("mspace")
			spacer.setAttribute("width", "2em")
			replacement.appendChild(spacer)
			for labelCell.first != nil {
				replacement.appendChild(labelCell.first)
			}
		}
		table.replaceWith(replacement)
	}
}

func dfdSerializeNormalizedMathML(mathEl *ldNode) string {
	clone := mathEl.cloneNode(true)
	dfdFlattenTaggedEquationTables(clone)
	return ldOuterHTML(clone)
}

func dfdGetMathMLFromElement(el *ldNode) *dfdMathData {
	if el.name == "math" {
		return &dfdMathData{mathml: dfdSerializeNormalizedMathML(el), latex: el.attr("alttext"), isBlock: el.attr("display") == "block"}
	}
	if s := el.attr("data-mathml"); s != "" {
		fragment := ldParseFragment(s)
		if m := ldQuerySelector(fragment, "math"); m != nil {
			return &dfdMathData{mathml: dfdSerializeNormalizedMathML(m), latex: m.attr("alttext"), isBlock: m.attr("display") == "block"}
		}
	}
	if c := ldQuerySelector(el, ".MJX_Assistive_MathML, mjx-assistive-mml"); c != nil {
		if m := ldQuerySelector(c, "math"); m != nil {
			isBlock := m.attr("display") == "block" || c.attr("display") == "block"
			return &dfdMathData{mathml: dfdSerializeNormalizedMathML(m), latex: m.attr("alttext"), isBlock: isBlock}
		}
	}
	if k := ldQuerySelector(el, ".katex-mathml math"); k != nil {
		return &dfdMathData{mathml: dfdSerializeNormalizedMathML(k)}
	}
	mjx := el
	if el.name != "mjx-math" {
		mjx = ldQuerySelector(el, "mjx-math")
	}
	if mjx != nil {
		if r := dfdReconstructMathMLFromMjx(mjx); r != nil {
			return r
		}
	}
	return nil
}

var reLatexPHP = regexp.MustCompile(`latex\.php\?latex=([^&]+)`)

func dfdGetBasicLatexFromElement(el *ldNode) (string, bool) {
	if v := el.attr("data-latex"); v != "" {
		return v, true
	}
	if v := el.attr("data-math"); v != "" {
		return v, true
	}
	if p := el.parentElement(); p != nil && p.hasClass("hurmet-tex") {
		if v := p.attr("data-entry"); v != "" {
			return v, true
		}
	}
	if el.name == "img" && el.hasClass("latex") {
		if alt := el.attr("alt"); alt != "" {
			return alt, true
		}
		if src := el.attr("src"); src != "" {
			if m := reLatexPHP.FindStringSubmatch(src); m != nil {
				if d, ok := jsDecodeURIComponent(m[1]); ok {
					d = strings.ReplaceAll(d, "+", " ")
					d = strings.ReplaceAll(d, "%5C", `\`)
					return d, true
				}
				// decodeURIComponent throws: the original lets it propagate.
				ldThrow("URI malformed")
			}
		}
	}
	if ann := ldQuerySelector(el, `annotation[encoding="application/x-tex"]`); ann != nil && ann.textContent() != "" {
		return jsTrim(ann.textContent()), true
	}
	if ldMatches(el, ".katex") {
		if ann := ldQuerySelector(el, `.katex-mathml annotation[encoding="application/x-tex"]`); ann != nil && ann.textContent() != "" {
			return jsTrim(ann.textContent()), true
		}
	}
	if ldMatches(el, `script[type="math/tex"]`) || ldMatches(el, `script[type="math/tex; mode=display"]`) {
		if t := jsTrim(el.textContent()); t != "" {
			return t, true
		}
		return "", false
	}
	if p := el.parentElement(); p != nil {
		if s := ldQuerySelector(p, `script[type="math/tex"], script[type="math/tex; mode=display"]`); s != nil {
			if t := jsTrim(s.textContent()); t != "" {
				return t, true
			}
			return "", false
		}
	}
	if el.name == "math" && jsTrim(el.textContent()) != "" {
		return jsTrim(el.textContent()), true
	}
	if alt := el.attr("alt"); alt != "" {
		return alt, true
	}
	return "", false
}

func dfdIsBlockDisplay(el *ldNode) bool {
	if el.attr("display") == "block" {
		return true
	}
	classNames := strings.ToLower(dfdClassName(el))
	if strings.Contains(classNames, "display") || strings.Contains(classNames, "block") {
		return true
	}
	if ldClosest(el, `.katex-display, .MathJax_Display, [data-display="block"]`) != nil {
		return true
	}
	if prev := el.previousElementSibling(); prev != nil && prev.name == "p" {
		return true
	}
	if ldMatches(el, ".mwe-math-fallback-image-display") {
		return true
	}
	if ldMatches(el, ".katex") {
		return ldClosest(el, ".katex-display") != nil
	}
	if el.hasAttribute("display") {
		return el.attr("display") == "true"
	}
	if ldMatches(el, `script[type="math/tex; mode=display"]`) {
		return true
	}
	if pc := ldClosest(el, "[display]"); pc != nil {
		return pc.attr("display") == "true"
	}
	return false
}

var dfdLatexParamRes = func() []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, p := range []string{"latex", "chl", "tex", "eq", "math"} {
		out = append(out, jsRE(`[?&]`+p+`=([^&#]+)`, "i"))
	}
	return out
}()

var (
	reQueryString = jsRE(`\?([^#]+)`, "")
	rePct5C       = jsRE(`%5[Cc]`, "")
)

func dfdDecodeLatex(raw string) string {
	d, ok := jsDecodeURIComponent(strings.ReplaceAll(raw, "+", " "))
	if !ok {
		return ""
	}
	if reLooksLatex.MatchString(d) {
		return d
	}
	return ""
}

func dfdExtractLatexFromImageSrc(src string) string {
	for _, re := range dfdLatexParamRes {
		if m := re.FindStringSubmatch(src); m != nil {
			if l := dfdDecodeLatex(m[1]); l != "" {
				return l
			}
		}
	}
	if m := reQueryString.FindStringSubmatch(src); m != nil {
		if l := dfdDecodeLatex(m[1]); l != "" {
			return l
		}
	}
	pathPart := strings.SplitN(src, "?", 2)[0]
	segs := strings.Split(pathPart, "/")
	for i := len(segs) - 1; i >= 0; i-- {
		if rePct5C.MatchString(segs[i]) {
			if l := dfdDecodeLatex(segs[i]); l != "" {
				return l
			}
		}
	}
	return ""
}

var (
	reMathJaxCfg  = jsRE(`MathJax\s*[.=]`, "")
	reKatexWord   = jsRE(`katex`, "i")
	reLatexDelim  = jsRE(`\$\$([\s\S]+?)\$\$|\\\[([\s\S]+?)\\\]|\$([^\s$][^$]*[^\s$]|[^\s$])\$|\\\(([\s\S]+?)\\\)`, "")
	reLatexCmd    = jsRE(`\\[a-zA-Z]`, "")
	reLatexStruct = jsRE(`[_^{}]`, "")
)

func dfdHasMathLibrary(doc *ldNode) bool {
	for _, s := range ldQuerySelectorAll(doc, "script[src]") {
		src := strings.ToLower(s.attr("src"))
		if strings.Contains(src, "mathjax") || strings.Contains(src, "katex") {
			return true
		}
	}
	for _, s := range ldQuerySelectorAll(doc, "script:not([src])") {
		text := s.textContent()
		if reMathJaxCfg.MatchString(text) || reKatexWord.MatchString(text) {
			return true
		}
	}
	return false
}

var dfdRawLatexSkipTags = map[string]bool{"PRE": true, "CODE": true, "SCRIPT": true, "STYLE": true, "MATH": true, "SVG": true, "TEXTAREA": true}

type dfdLatexPart struct {
	text    string
	isText  bool
	latex   string
	isBlock bool
}

// dfdWrapRawLatexDelimiters takes the document Defuddle passes (this.doc, the live document, not the clone the
// element is in) for the library test.
func dfdWrapRawLatexDelimiters(element, doc *ldNode) {
	if !dfdHasMathLibrary(doc) {
		return
	}
	if ldQuerySelector(element, dfdMathFastCheck) != nil {
		return
	}
	var textNodes []*ldNode
	var walk func(n *ldNode)
	walk = func(n *ldNode) {
		if n.kind == ldElem && dfdRawLatexSkipTags[n.tagName()] {
			return
		}
		if n.kind == ldText {
			textNodes = append(textNodes, n)
		} else {
			for c := n.first; c != nil; c = c.next {
				walk(c)
			}
		}
	}
	walk(element)
	for _, textNode := range textNodes {
		text := textNode.data
		if !strings.Contains(text, "$") && !strings.Contains(text, `\(`) && !strings.Contains(text, `\[`) {
			continue
		}
		var parts []dfdLatexPart
		lastIndex := 0
		hasBlock := false
		for _, m := range reLatexDelim.FindAllStringSubmatchIndex(text, -1) {
			group := func(i int) (string, bool) {
				if m[2*i] < 0 {
					return "", false
				}
				return text[m[2*i]:m[2*i+1]], true
			}
			g1, ok1 := group(1)
			g2, ok2 := group(2)
			g3, ok3 := group(3)
			g4, ok4 := group(4)
			var blockContent, inlineContent string
			isBlock := ok1 || ok2
			if ok1 {
				blockContent = g1
			} else if ok2 {
				blockContent = g2
			}
			if ok3 {
				inlineContent = g3
			} else if ok4 {
				inlineContent = g4
			}
			content := inlineContent
			if isBlock {
				content = blockContent
			}
			latex := jsTrim(content)
			isBackslash := ok2 || ok4
			if !isBackslash && !(reLatexCmd.MatchString(latex) || reLatexStruct.MatchString(latex)) {
				continue
			}
			if lastIndex < m[0] {
				parts = append(parts, dfdLatexPart{text: text[lastIndex:m[0]], isText: true})
			}
			if isBlock {
				hasBlock = true
			}
			parts = append(parts, dfdLatexPart{latex: latex, isBlock: isBlock})
			lastIndex = m[1]
		}
		if len(parts) == 0 {
			continue
		}
		if lastIndex < len(text) {
			parts = append(parts, dfdLatexPart{text: text[lastIndex:], isText: true})
		}
		if hasBlock {
			hasSurrounding := false
			for _, p := range parts {
				if p.isText && len(jsTrim(p.text)) > 0 {
					hasSurrounding = true
				}
			}
			parentHasOther := false
			if parent := textNode.parentElement(); parent != nil {
				for _, n := range parent.childNodes() {
					if n != textNode && ((n.kind == ldText && len(jsTrim(n.data)) > 0) || n.kind == ldElem) {
						parentHasOther = true
						break
					}
				}
			}
			if hasSurrounding || parentHasOther {
				for i := range parts {
					if !parts[i].isText {
						parts[i].isBlock = false
					}
				}
			}
		}
		frag := ldNewFragment()
		for _, p := range parts {
			if p.isText {
				frag.appendChild(ldNewText(p.text))
			} else {
				mathEl := ldNewElement("math")
				mathEl.setAttribute("xmlns", "http://www.w3.org/1998/Math/MathML")
				if p.isBlock {
					mathEl.setAttribute("display", "block")
				} else {
					mathEl.setAttribute("display", "inline")
				}
				mathEl.setAttribute("data-latex", p.latex)
				mathEl.setTextContent(p.latex)
				frag.appendChild(mathEl)
			}
		}
		textNode.replaceWith(frag)
	}
}

// ---- math.full.js ----------------------------------------------------------------------------------------------

func dfdGetLatexFromElement(el *ldNode) (string, bool) {
	if l, ok := dfdGetBasicLatexFromElement(el); ok && l != "" {
		return l, true
	}
	if data := dfdGetMathMLFromElement(el); data != nil && data.mathml != "" {
		if l, ok := dfdMathMLToLatex(data.mathml); ok {
			return l, true
		}
	}
	return "", false
}

func dfdCreateCleanMathEl(data *dfdMathData, latex string, isBlock bool) *ldNode {
	clean := ldNewElement("math")
	clean.setAttribute("xmlns", "http://www.w3.org/1998/Math/MathML")
	if isBlock {
		clean.setAttribute("display", "block")
	} else {
		clean.setAttribute("display", "inline")
	}
	clean.setAttribute("data-latex", latex)
	if data != nil && data.mathml != "" {
		fragment := ldParseFragment(data.mathml)
		if content := ldQuerySelector(fragment, "math"); content != nil {
			dfdTransferContent(content, clean)
		}
	} else if latex != "" {
		mathml := dfdRenderLatexToMathML(latex, isBlock)
		fragment := ldParseFragment(mathml)
		if content := ldQuerySelector(fragment, "math"); content != nil {
			for content.first != nil {
				clean.appendChild(content.first)
			}
		} else {
			clean.setTextContent(latex)
		}
	}
	return clean
}

func dfdMathRules() []dfdRule {
	return []dfdRule{{
		selector:  dfdMathSelectors,
		fastCheck: dfdMathFastCheck,
		transform: func(el *ldNode) *ldNode {
			data := dfdGetMathMLFromElement(el)
			latex, _ := dfdGetLatexFromElement(el)
			isBlock := dfdIsBlockDisplay(el)
			clean := dfdCreateCleanMathEl(data, latex, isBlock)
			if el.parentElement() != nil && !ldMatches(el, `script[type^="math/"]`) {
				for _, m := range ldQuerySelectorAll(el.parentElement(), `script[type^="math/"], .MathJax_Preview, script[type="text/javascript"][src*="mathjax"], script[type="text/javascript"][src*="katex"]`) {
					m.remove()
				}
			}
			return clean
		},
	}}
}
