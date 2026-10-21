package websearch

import "strings"

// Temml 0.13.4 (MIT): buildMathML.js, linebreaking.js (the "none" wrap mode, the only one reachable: temml.js never
// sets Settings.wrap) and variant.js.

func tmSymbolTable(mode string) map[string]*tmSymbol {
	if mode == "text" {
		return tmSymbolsText()
	}
	return tmSymbolsMath()
}

// tmMakeText is makeText(text, mode, style); style is nil where the original passes none.
func tmMakeText(text, mode string, style *tmStyle) *textNode {
	if sym, ok := tmSymbolTable(mode)[text]; ok && sym.replace != "" && tmCodePointUnit(text) != 0xd835 {
		if !(tmLigatures()[text] && style != nil && ((style.fontFamily() != "" && tmSlice46(style.fontFamily()) == "tt") || (style.font() != "" && tmSlice46(style.font()) == "tt"))) {
			text = sym.replace
		}
	}
	return &textNode{text}
}

func tmSlice46(s string) string {
	if len(s) <= 4 {
		return ""
	}
	return s[4:min(len(s), 6)]
}

func tmCopyChar(newRow *mathNode, child mmNode) {
	c := mmChildren(child)
	text := c[0].(*textNode).text
	if len(newRow.children) == 0 || mmType(newRow.children[len(newRow.children)-1]) != "mtext" {
		newRow.children = append(newRow.children, mn("mtext", &textNode{text}))
	} else {
		last := newRow.children[len(newRow.children)-1].(*mathNode)
		last.children[0].(*textNode).text += text
	}
}

func tmConsolidateText(node mmNode) mmNode {
	mrow, ok := node.(*mathNode)
	if !ok || (mrow.typ != "mrow" && mrow.typ != "mstyle") {
		return node
	}
	if len(mrow.children) == 0 {
		return mrow
	}
	newRow := mn("mrow")
	for _, child := range mrow.children {
		cm, isMath := child.(*mathNode)
		if isMath && cm.typ == "mtext" && cm.attrs.size() == 0 {
			tmCopyChar(newRow, child)
		} else if isMath && cm.typ == "mrow" {
			canConsolidate := true
			for _, gc := range cm.children {
				if mmType(gc) != "mtext" || cm.attrs.size() != 0 {
					canConsolidate = false
					break
				}
			}
			if canConsolidate {
				for _, gc := range cm.children {
					tmCopyChar(newRow, gc)
				}
			} else {
				newRow.children = append(newRow.children, child)
			}
		} else {
			newRow.children = append(newRow.children, child)
		}
	}
	for _, c := range newRow.children {
		if m, ok := c.(*mathNode); ok && m.typ == "mtext" {
			t := m.children[0].(*textNode)
			if strings.HasPrefix(t.text, " ") {
				t.text = "\u00a0" + t.text[1:]
			}
			if strings.HasSuffix(t.text, " ") {
				t.text = t.text[:len(t.text)-1] + "\u00a0"
			}
			for _, k := range mrow.attrs.keys {
				m.attrs.set(k, mrow.attrs.vals[k])
			}
		}
	}
	if len(newRow.children) == 1 && mmType(newRow.children[0]) == "mtext" {
		return newRow.children[0]
	}
	return newRow
}

func tmMakeRow(body []mmNode, semisimple bool) mmNode {
	if len(body) == 1 {
		if _, isFrag := body[0].(*docFragment); !isFrag {
			return body[0]
		}
	}
	if !semisimple {
		if len(body) > 0 {
			if m, ok := body[0].(*mathNode); ok && m.typ == "mo" && !tmTruthy(m.attrs.get("fence")) {
				m.attrs.set("lspace", "0em")
				m.attrs.set("rspace", "0em")
			}
		}
		if end := len(body) - 1; end >= 0 {
			if m, ok := body[end].(*mathNode); ok && m.typ == "mo" && !tmTruthy(m.attrs.get("fence")) {
				m.attrs.set("lspace", "0em")
				m.attrs.set("rspace", "0em")
			}
		}
	}
	return newMathNode("mrow", body, nil, nil)
}

func tmIsNumberPunctuation(group mmNode) bool {
	m, ok := group.(*mathNode)
	if !ok {
		return false
	}
	if m.typ == "mi" && len(m.children) == 1 {
		t, ok := m.children[0].(*textNode)
		return ok && t.text == "."
	} else if m.typ == "mtext" && len(m.children) == 1 {
		t, ok := m.children[0].(*textNode)
		return ok && t.text == "\u2008"
	} else if m.typ == "mo" && len(m.children) == 1 && m.getAttribute("separator") == "true" &&
		m.getAttribute("lspace") == "0em" && m.getAttribute("rspace") == "0em" {
		t, ok := m.children[0].(*textNode)
		return ok && t.text == ","
	}
	return false
}

func tmIsComma(expression []any, i int) bool {
	node := expression[i].(tmObj)
	following := expression[i+1].(tmObj)
	if !(tmStr(node, "type") == "atom" && tmStr(node, "text") == ",") {
		return false
	}
	nl, fl := tmLocOf(node), tmLocOf(following)
	return nl != nil && fl != nil && nl.end == fl.start
}

func tmIsRel(item any) bool {
	n := item.(tmObj)
	return (tmStr(n, "type") == "atom" && tmStr(n, "family") == "rel") || (tmStr(n, "type") == "mclass" && tmStr(n, "mclass") == "mrel")
}

// tmSetAttr is group.setAttribute(name, value) for a node of any kind.
func tmSetAttr(n mmNode, name string, value any) {
	m, ok := n.(*mathNode)
	if !ok {
		panic(&tmJSError{"group.setAttribute is not a function"})
	}
	m.setAttribute(name, value)
}

func tmBuildExpression(expression []any, style *tmStyle, semisimple bool) []mmNode {
	if !semisimple && len(expression) == 1 {
		group := tmBuildGroup(expression[0], style)
		if m, ok := group.(*mathNode); ok && m.typ == "mo" {
			m.setAttribute("lspace", "0em")
			m.setAttribute("rspace", "0em")
		}
		return []mmNode{group}
	}
	groups := []mmNode{}
	groupArray := make([]mmNode, len(expression))
	var lastGroup mmNode
	for i := range expression {
		groupArray[i] = tmBuildGroup(expression[i], style)
	}
	for i := range groupArray {
		group := groupArray[i]
		if i < len(expression)-1 && tmIsRel(expression[i]) && tmIsRel(expression[i+1]) {
			tmSetAttr(group, "rspace", "0em")
		}
		if i > 0 && tmIsRel(expression[i]) && tmIsRel(expression[i-1]) {
			tmSetAttr(group, "lspace", "0em")
		}
		gType := mmType(group)
		lType := ""
		if lastGroup != nil {
			lType = mmType(lastGroup)
		}
		if gType == "mn" && lastGroup != nil && lType == "mn" {
			l := lastGroup.(*mathNode)
			l.children = append(l.children, mmChildren(group)...)
			continue
		} else if tmIsNumberPunctuation(group) && lastGroup != nil && lType == "mn" {
			l := lastGroup.(*mathNode)
			l.children = append(l.children, mmChildren(group)...)
			continue
		} else if lastGroup != nil && lType == "mn" && i < len(groupArray)-1 && mmType(groupArray[i+1]) == "mn" && tmIsComma(expression, i) {
			l := lastGroup.(*mathNode)
			l.children = append(l.children, mmChildren(group)...)
			continue
		} else if gType == "mn" && lastGroup != nil && tmIsNumberPunctuation(lastGroup) {
			g := group.(*mathNode)
			g.children = append(append([]mmNode{}, mmChildren(lastGroup)...), g.children...)
			groups = groups[:len(groups)-1]
		} else if (gType == "msup" || gType == "msub") && len(mmChildren(group)) >= 1 && lastGroup != nil && (lType == "mn" || tmIsNumberPunctuation(lastGroup)) {
			if base, ok := mmChildren(group)[0].(*mathNode); ok && base.typ == "mn" {
				base.children = append(append([]mmNode{}, mmChildren(lastGroup)...), base.children...)
				groups = groups[:len(groups)-1]
			}
		}
		groups = append(groups, group)
		lastGroup = group
	}
	return groups
}

func tmBuildExpressionRow(expression []any, style *tmStyle, semisimple bool) mmNode {
	return tmMakeRow(tmBuildExpression(expression, style, semisimple), semisimple)
}

func tmBuildGroup(group any, style *tmStyle) mmNode {
	g, ok := group.(tmObj)
	if !ok || g == nil {
		return mn("mrow")
	}
	typ := tmStr(g, "type")
	if b, ok := tmBuilders[typ]; ok {
		return b(g, style)
	}
	tmThrowf("Got group of unknown type: '" + typ + "'")
	return nil
}

func tmGlue() *mathNode {
	n := mn("mtd")
	n.style.set("padding", "0")
	n.style.set("width", "50%")
	return n
}

var tmLabelContainers = map[string]bool{"mrow": true, "mtd": true, "mtable": true, "mtr": true}

func tmGetLabel(parent mmNode) string {
	for _, node := range mmChildren(parent) {
		typ := mmType(node)
		if typ != "" && tmLabelContainers[typ] {
			m := node.(*mathNode)
			if len(m.classes) > 0 && m.classes[0] == "tml-label" {
				return m.label
			}
			if label := tmGetLabel(node); label != "" {
				return label
			}
		} else if typ == "" {
			if label := tmGetLabel(node); label != "" {
				return label
			}
		}
	}
	return ""
}

func tmTaggedExpression(expression mmNode, tag any, style *tmStyle, leqno bool) mmNode {
	tagNode := tmBuildExpressionRow(tmList(tmList(tag)[0].(tmObj)["body"]), style, false)
	tagNode = tmConsolidateText(tagNode)
	mmPushClass(tagNode, "tml-tag")
	label := tmGetLabel(expression)
	expression = mn("mtd", expression)
	rowArray := []mmNode{tmGlue(), expression, tmGlue()}
	idx := 2
	if leqno {
		idx = 0
	}
	cp := rowArray[idx].childrenPtr()
	*cp = append(*cp, tagNode)
	mtr := newMathNode("mtr", rowArray, []string{"tml-tageqn"}, nil)
	if label != "" {
		mtr.setAttribute("id", label)
	}
	table := mn("mtable", mtr)
	table.style.set("width", "100%")
	table.setAttribute("displaystyle", "true")
	return table
}

// tmSetLineBreaks is setLineBreaks(expression, "none", isDisplayMode).
func tmSetLineBreaks(expression []mmNode, isDisplayMode bool) mmNode {
	var mtrs []mmNode
	var mrows []mmNode
	var block []mmNode
	i := 0
	for i < len(expression) {
		for i < len(expression) {
			f, ok := expression[i].(*docFragment)
			if !ok {
				break
			}
			rest := append([]mmNode{}, expression[i+1:]...)
			expression = append(append(expression[:i:i], f.children...), rest...)
		}
		if i >= len(expression) {
			panic(&tmJSError{"Cannot read properties of undefined (reading 'attributes')"})
		}
		node := expression[i]
		if a := node.attrsOf(); a != nil && tmTruthy(a.get("linebreak")) && a.get("linebreak") == "newline" {
			if len(block) > 0 {
				mrows = append(mrows, mn("mrow", block...))
			}
			mrows = append(mrows, node)
			block = nil
			mtd := newMathNode("mtd", mrows, nil, nil)
			mtd.style.set("textAlign", "left")
			mtrs = append(mtrs, mn("mtr", mtd))
			mrows = nil
			i++
			continue
		}
		block = append(block, node)
		i++
	}
	if len(block) > 0 {
		mrows = append(mrows, mn("mrow", block...))
	}
	if len(mtrs) > 0 {
		mtd := newMathNode("mtd", mrows, nil, nil)
		mtd.style.set("textAlign", "left")
		mtrs = append(mtrs, mn("mtr", mtd))
		mtable := newMathNode("mtable", mtrs, nil, nil)
		if !isDisplayMode {
			mtable.setAttribute("columnalign", "left")
			mtable.setAttribute("rowspacing", "0em")
		}
		return mtable
	}
	return newDocFragment(mrows)
}

func tmBuildMathML(tree []any, texExpression string, style *tmStyle, settings *tmSettings) mmNode {
	var tag any
	if len(tree) == 1 && tmType(tree[0]) == "tag" {
		t := tree[0].(tmObj)
		tag = t["tag"]
		tree = tmList(t["body"])
	}
	expression := tmBuildExpression(tree, style, false)
	if len(expression) == 1 {
		if a, ok := expression[0].(*anchorNode); ok {
			return a
		}
	}
	var wrapper mmNode
	if len(expression) == 1 && tag == nil {
		if m, ok := expression[0].(*mathNode); ok {
			wrapper = m
		}
	}
	if wrapper == nil {
		wrapper = tmSetLineBreaks(expression, settings.displayMode)
	}
	if tag != nil {
		wrapper = tmTaggedExpression(wrapper, tag, style, settings.leqno)
	}
	if settings.annotate {
		annotation := mn("annotation", &textNode{texExpression})
		annotation.setAttribute("encoding", "application/x-tex")
		wrapper = mn("semantics", wrapper, annotation)
	}
	math := mn("math", wrapper)
	if settings.xml {
		math.setAttribute("xmlns", "http://www.w3.org/1998/Math/MathML")
	}
	if settings.displayMode {
		math.setAttribute("display", "block")
		math.style.set("display", "block math")
		math.classes = []string{"tml-display"}
	}
	return math
}

// ---- variant.js --------------------------------------------------------------------------------------------------

var tmFontMap = map[string]string{
	"mathbf": "bold", "mathrm": "normal", "textit": "italic", "mathit": "italic", "mathnormal": "italic", "mathbb": "double-struck",
	"mathcal": "script", "mathfrak": "fraktur", "mathscr": "script", "mathsf": "sans-serif", "mathtt": "monospace",
}

// tmGetVariant is getVariant(group, style): "" stands for null.
func tmGetVariant(group tmObj, style *tmStyle) string {
	switch {
	case style.fontFamily() == "texttt":
		return "monospace"
	case style.fontFamily() == "textsc":
		return "normal"
	case style.fontFamily() == "textsf":
		switch {
		case style.fontShape() == "textit" && style.fontWeight() == "textbf":
			return "sans-serif-bold-italic"
		case style.fontShape() == "textit":
			return "sans-serif-italic"
		case style.fontWeight() == "textbf":
			return "sans-serif-bold"
		}
		return "sans-serif"
	case style.fontShape() == "textit" && style.fontWeight() == "textbf":
		return "bold-italic"
	case style.fontShape() == "textit":
		return "italic"
	case style.fontWeight() == "textbf":
		return "bold"
	}
	font := style.font()
	if font == "" || font == "mathnormal" {
		return ""
	}
	switch font {
	case "mathit":
		return "italic"
	case "mathrm":
		cp := tmCodePointAt(tmStr(group, "text"))
		if 0x03ab < cp && cp < 0x03cf {
			return "italic"
		}
		return "normal"
	case "greekItalic":
		return "italic"
	case "up@greek":
		return "normal"
	case "boldsymbol", "mathboldsymbol":
		return "bold-italic"
	case "mathbf":
		return "bold"
	case "mathbb":
		return "double-struck"
	case "mathfrak":
		return "fraktur"
	case "mathscr", "mathcal":
		return "script"
	case "mathsf":
		return "sans-serif"
	case "mathsfit":
		return "sans-serif-italic"
	case "mathtt":
		return "monospace"
	}
	return tmFontMap[font]
}
