package websearch

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Temml 0.13.4 (MIT): src/functions/mclass.js, multiscript.js, not.js, op.js, operatorname.js, ordgroup.js, phantom.js,
// pmb.js, raise.js, ref.js, reflect.js, relax.js, rule.js, sfrac.js, sizing.js, smash.js, sqrt.js, styling.js,
// supsub.js, symbolsOp.js, symbolsOrd.js, symbolsSpacing.js, text.js, vcenter.js, verb.js.

// tmParseJSNumber is Number(s) for a trimmed string: ok is false for NaN.
func tmParseJSNumber(s string) (float64, bool) {
	if s == "" {
		return 0, true
	}
	switch s {
	case "Infinity", "+Infinity":
		return math.Inf(1), true
	case "-Infinity":
		return math.Inf(-1), true
	}
	if len(s) > 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X' || s[1] == 'o' || s[1] == 'O' || s[1] == 'b' || s[1] == 'B') {
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[s[1]]
		n, err := strconv.ParseUint(s[2:], base, 64)
		if err != nil {
			return 0, false
		}
		return float64(n), true
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c == '.' || c == '+' || c == '-' || c == 'e' || c == 'E') {
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return f, true
		}
		return 0, false
	}
	return f, true
}

// ---- mclass.js ---------------------------------------------------------------------------------------------------

var tmTextAtomTypes = map[string]bool{"text": true, "textord": true, "mathord": true, "atom": true}
var tmLatinLetterRe = regexp.MustCompile(`[A-Za-z]`)

func tmMclassBuilder(group tmObj, style *tmStyle) mmNode {
	inner := tmBuildExpression(tmList(group["body"]), style, false)
	mclass := tmStr(group, "mclass")
	switch mclass {
	case "minner":
		return newMathNode("mpadded", inner, nil, nil)
	case "mord":
		if tmTruthy(group["isCharacterBox"]) || mmType(inner[0]) == "mathord" {
			node := inner[0].(*mathNode)
			node.typ = "mi"
			if len(node.children) == 1 {
				if t, ok := node.children[0].(*textNode); ok && t.text == "∇" {
					node.setAttribute("mathvariant", "normal")
				}
			}
			return node
		}
		return newMathNode("mi", inner, nil, nil)
	}
	var node *mathNode
	if tmTruthy(group["mustPromote"]) {
		node = inner[0].(*mathNode)
		node.typ = "mo"
		body := tmList(group["body"])
		if tmTruthy(group["isCharacterBox"]) {
			if text, ok := body[0].(tmObj)["text"].(string); ok && text != "" && tmLatinLetterRe.MatchString(text) {
				node.setAttribute("mathvariant", "italic")
			}
		}
	} else {
		node = newMathNode("mrow", inner, nil, nil)
	}
	doSpacing := style.level() < 2
	if node.typ == "mrow" {
		if doSpacing {
			switch mclass {
			case "mbin":
				node.children = append(append([]mmNode{tmPadding(0.2222)}, node.children...), tmPadding(0.2222))
			case "mrel":
				node.children = append(append([]mmNode{tmPadding(0.2778)}, node.children...), tmPadding(0.2778))
			case "mpunct":
				node.children = append(node.children, tmPadding(0.1667))
			case "minner":
				node.children = append(append([]mmNode{tmPadding(0.0556)}, node.children...), tmPadding(0.0556))
			}
		}
	} else {
		switch mclass {
		case "mbin":
			v := "0"
			if doSpacing {
				v = "0.2222em"
			}
			node.attrs.set("lspace", v)
			node.attrs.set("rspace", v)
		case "mrel":
			v := "0"
			if doSpacing {
				v = "0.2778em"
			}
			node.attrs.set("lspace", v)
			node.attrs.set("rspace", v)
		case "mpunct":
			node.attrs.set("lspace", "0em")
			if doSpacing {
				node.attrs.set("rspace", "0.1667em")
			} else {
				node.attrs.set("rspace", "0")
			}
		case "mopen", "mclose":
			node.attrs.set("lspace", "0em")
			node.attrs.set("rspace", "0em")
		case "minner":
			if doSpacing {
				node.attrs.set("lspace", "0.0556em")
				node.attrs.set("width", "+0.1111em")
			}
		}
	}
	if !(mclass == "mopen" || mclass == "mclose") {
		node.attrs.del("stretchy")
		node.attrs.del("form")
	}
	return node
}

func tmBinrelClass(arg tmObj) string {
	atom := arg
	if tmStr(arg, "type") == "ordgroup" {
		if l := tmList(arg["body"]); len(l) == 1 {
			atom = l[0].(tmObj)
		}
	}
	if tmStr(atom, "type") == "atom" {
		body := tmList(arg["body"])
		if arg["body"] == nil {
			panic(&tmJSError{"Cannot read properties of undefined (reading 'length')"})
		}
		family := tmStr(atom, "family")
		if len(body) > 0 {
			if t := tmStr(body[0].(tmObj), "text"); t != "" {
				if sym := tmSymbolsMath[t]; sym != nil {
					family = sym.group
				}
			}
		}
		if family == "bin" || family == "rel" {
			return "m" + family
		}
	}
	return "mord"
}

// ---- multiscript.js ----------------------------------------------------------------------------------------------

func tmMultiscriptBuildGroup(el any, style *tmStyle, noneNode *mathNode) mmNode {
	if !tmTruthy(el) {
		return noneNode
	}
	node := tmBuildGroup(el, style)
	if mmType(node) == "mrow" && len(mmChildren(node)) == 0 {
		return noneNode
	}
	return node
}

// ---- op.js and operatorname.js ----------------------------------------------------------------------------------

var tmOrdTypes = map[string]bool{"textord": true, "mathord": true, "ordgroup": true, "close": true, "leftright": true, "font": true}
var tmOrdAtomTypes = map[string]bool{"textord": true, "mathord": true, "atom": true}

func tmSetOpSpacing(node *mathNode) {
	node.attrs.set("lspace", "0.1667em")
	node.attrs.set("rspace", "0.1667em")
}

func tmOpBuilder(group tmObj, style *tmStyle) mmNode {
	var node *mathNode
	name := tmStr(group, "name")
	if tmTruthy(group["symbol"]) {
		node = mn("mo", tmMakeText(name, tmStr(group, "mode"), nil))
		if name == "\\smallint" {
			node.setAttribute("largeop", "false")
		} else {
			node.setAttribute("movablelimits", "false")
		}
		if tmTruthy(group["fromMathOp"]) {
			tmSetOpSpacing(node)
		}
	} else if tmTruthy(group["body"]) {
		node = newMathNode("mo", tmBuildExpression(tmList(group["body"]), style, false), nil, nil)
		if tmTruthy(group["fromMathOp"]) {
			tmSetOpSpacing(node)
		}
	} else {
		node = mn("mi", &textNode{sliceUnitsFrom(name, 1)})
		if !tmTruthy(group["parentIsSupSub"]) {
			operator := mn("mo", tmMakeText("\u2061", "text", nil))
			row := []mmNode{node, operator}
			if tmTruthy(group["needsLeadingSpace"]) {
				lead := mn("mspace")
				lead.setAttribute("width", "0.1667em")
				row = append([]mmNode{lead}, row...)
			}
			if !tmTruthy(group["isFollowedByDelimiter"]) {
				trail := mn("mspace")
				trail.setAttribute("width", "0.1667em")
				row = append(row, trail)
			}
			node = newMathNode("mrow", row, nil, nil)
		}
	}
	return node
}

// sliceUnitsFrom is s.slice(n) on a UTF-16 string.
func sliceUnitsFrom(s string, n int) string {
	u := tmUTF16Units(s)
	if n >= len(u) {
		return ""
	}
	return tmU16s(u[n:])
}

var tmSingleCharBigOps = map[string]string{
	"\u220F": "\\prod", "\u2210": "\\coprod", "\u2211": "\\sum", "\u22c0": "\\bigwedge", "\u22c1": "\\bigvee", "\u22c2": "\\bigcap", "\u22c3": "\\bigcup",
	"\u2a00": "\\bigodot", "\u2a01": "\\bigoplus", "\u2a02": "\\bigotimes", "\u2a04": "\\biguplus", "\u2a05": "\\bigsqcap", "\u2a06": "\\bigsqcup",
	"\u2a03": "\\bigcupdot", "\u2a07": "\\bigdoublevee", "\u2a08": "\\bigdoublewedge", "\u2a09": "\\bigtimes",
}

var tmSingleCharIntegrals = map[string]string{
	"\u222b": "\\int", "\u222c": "\\iint", "\u222d": "\\iiint", "\u222e": "\\oint", "\u222f": "\\oiint", "\u2230": "\\oiiint", "\u2231": "\\intclockwise",
	"\u2232": "\\varointclockwise", "\u2a0c": "\\iiiint", "\u2a0d": "\\intbar", "\u2a0e": "\\intBar", "\u2a0f": "\\fint", "\u2a12": "\\rppolint",
	"\u2a13": "\\scpolint", "\u2a15": "\\pointint", "\u2a16": "\\sqint", "\u2a17": "\\intlarhk", "\u2a18": "\\intx", "\u2a19": "\\intcap", "\u2a1a": "\\intcup",
}

func tmFuncOpHandler(limits bool) tmHandler {
	return func(ctx *tmCtx, args, opt []any) any {
		parser := ctx.parser
		prevAtomType := parser.prevAtomType
		next := parser.gullet.future().text
		return tmObj{"type": "op", "mode": parser.mode, "limits": limits, "parentIsSupSub": false, "symbol": false, "stack": false,
			"isFollowedByDelimiter": tmIsDelimiter(next), "needsLeadingSpace": len(prevAtomType) > 0 && tmOrdTypes[prevAtomType], "name": ctx.funcName}
	}
}

func tmOperatornameBuilder(group tmObj, style *tmStyle) mmNode {
	expression := tmBuildExpression(tmList(group["body"]), style.withFont("mathrm"), false)
	isAllString := true
	for i := 0; i < len(expression); i++ {
		node, ok := expression[i].(*mathNode)
		if ok {
			if (node.typ == "mrow" || node.typ == "mpadded") && len(node.children) == 1 {
				if c, ok := node.children[0].(*mathNode); ok {
					node = c
				}
			} else if node.typ == "mrow" && len(node.children) == 2 {
				c0, ok0 := node.children[0].(*mathNode)
				c1, ok1 := node.children[1].(*mathNode)
				if ok0 && ok1 && c1.typ == "mspace" && !tmTruthy(c1.attrs.get("width")) && len(c1.children) == 0 {
					node = c0
				}
			}
			switch node.typ {
			case "mi", "mn", "ms", "mtext":
			case "mspace":
				if w := node.attrs.get("width"); tmTruthy(w) {
					width := strings.Replace(jsStr(w), "em", "", 1)
					f, _ := tmParseJSNumber(jsTrim(width))
					ch := tmSpaceCharacter(f)
					if ch == "" {
						isAllString = false
					} else {
						expression[i] = mn("mtext", &textNode{ch})
					}
				}
			case "mo":
				if len(node.children) == 1 {
					if child, ok := node.children[0].(*textNode); ok {
						child.text = strings.Replace(strings.Replace(child.text, "\u2212", "-", 1), "\u2217", "*", 1)
						break
					}
				}
				isAllString = false
			default:
				isAllString = false
			}
		} else {
			isAllString = false
		}
	}
	if isAllString {
		var b strings.Builder
		for _, n := range expression {
			b.WriteString(n.toText())
		}
		expression = []mmNode{&textNode{b.String()}}
	} else if len(expression) == 1 && (mmType(expression[0]) == "mover" || mmType(expression[0]) == "munder") &&
		(mmType(mmChildren(expression[0])[0]) == "mi" || mmType(mmChildren(expression[0])[0]) == "mtext") {
		mmChildren(expression[0])[0].(*mathNode).typ = "mi"
		if tmTruthy(group["parentIsSupSub"]) {
			return newMathNode("mrow", expression, nil, nil)
		}
		operator := mn("mo", tmMakeText("\u2061", "text", nil))
		return newDocFragment([]mmNode{expression[0], operator})
	}
	var wrapper *mathNode
	if isAllString {
		wrapper = newMathNode("mi", expression, nil, nil)
		if jsLen(expression[0].(*textNode).text) == 1 {
			wrapper.setAttribute("mathvariant", "normal")
		}
	} else {
		wrapper = newMathNode("mrow", expression, nil, nil)
	}
	if !tmTruthy(group["parentIsSupSub"]) {
		operator := mn("mo", tmMakeText("\u2061", "text", nil))
		fragment := []mmNode{wrapper, operator}
		if tmTruthy(group["needsLeadingSpace"]) {
			space := mn("mspace")
			space.setAttribute("width", "0.1667em")
			fragment = append([]mmNode{space}, fragment...)
		}
		if !tmTruthy(group["isFollowedByDelimiter"]) {
			trail := mn("mspace")
			trail.setAttribute("width", "0.1667em")
			fragment = append(fragment, trail)
		}
		return newDocFragment(fragment)
	}
	return wrapper
}

// ---- sizing, styling, text ---------------------------------------------------------------------------------------

var tmSizeMap = map[string]float64{
	"\\tiny": 0.5, "\\sixptsize": 0.6, "\\Tiny": 0.6, "\\scriptsize": 0.7, "\\footnotesize": 0.8, "\\small": 0.9, "\\normalsize": 1.0,
	"\\large": 1.2, "\\Large": 1.44, "\\LARGE": 1.728, "\\huge": 2.074, "\\Huge": 2.488,
}

var tmStyleMap = map[string]int{"display": 0, "text": 1, "script": 2, "scriptscript": 3}
var tmStyleAttributes = map[string][2]string{"display": {"0", "true"}, "text": {"0", "false"}, "script": {"1", "false"}, "scriptscript": {"2", "false"}}

var tmTextFontFamilies = map[string]string{"\\textrm": "textrm", "\\textsf": "textsf", "\\texttt": "texttt", "\\textnormal": "textrm", "\\textsc": "textsc"}
var tmTextFontWeights = map[string]string{"\\textbf": "textbf", "\\textmd": "textmd"}
var tmTextFontShapes = map[string]string{"\\textit": "textit", "\\textup": "textup"}

func tmStyleWithFont(group tmObj, style *tmStyle) *tmStyle {
	font := tmStr(group, "font")
	if font == "" {
		return style
	} else if f := tmTextFontFamilies[font]; f != "" {
		return style.withTextFontFamily(f)
	} else if w := tmTextFontWeights[font]; w != "" {
		return style.withTextFontWeight(w)
	} else if font == "\\emph" {
		if style.fontShape() == "textit" {
			return style.withTextFontShape("textup")
		}
		return style.withTextFontShape("textit")
	}
	return style.withTextFontShape(tmTextFontShapes[font])
}

// ---- symbolsOrd.js -----------------------------------------------------------------------------------------------

var (
	tmNumberRe = regexp.MustCompile(`^\d(?:[\d,.]*\d)?$`)
	tmLatinRe  = regexp.MustCompile(`[A-Ba-z]`)
	tmPrimes   = map[string]bool{"\\prime": true, "\\dprime": true, "\\trprime": true, "\\qprime": true, "\\backprime": true, "\\backdprime": true, "\\backtrprime": true}
)

func tmItalicNumber(text *textNode, variant, tag string) mmNode {
	m := mn(tag, text)
	wrapper := mn("mstyle", m)
	wrapper.style.set("font-style", "italic")
	wrapper.style.set("font-family", "Cambria, 'Times New Roman', serif")
	if variant == "bold-italic" {
		wrapper.style.set("font-weight", "bold")
	}
	return wrapper
}

func tmVariantUnits(s, variant string) string {
	var b strings.Builder
	for _, u := range tmUTF16Units(s) {
		b.WriteString(tmVariantChar(tmU16(u), variant))
	}
	return b.String()
}

func tmMathordBuilder(group tmObj, style *tmStyle) mmNode {
	text := tmMakeText(tmStr(group, "text"), tmStr(group, "mode"), style)
	cp := tmCodePointAt(text.text)
	defaultVariant := "italic"
	if 0x0390 < cp && cp < 0x03aa {
		defaultVariant = "normal"
	}
	variant := tmGetVariant(group, style)
	if variant == "" {
		variant = defaultVariant
	}
	if variant == "script" {
		text.text = tmVariantChar(text.text, variant)
		return newMathNode("mi", []mmNode{text}, []string{style.font()}, nil)
	} else if variant != "italic" {
		text.text = tmVariantChar(text.text, variant)
	}
	node := mn("mi", text)
	var result mmNode = node
	if variant == "normal" {
		node.setAttribute("mathvariant", "normal")
		if jsLen(text.text) == 1 {
			result = mn("mrow", node, mn("mspace"))
		}
	}
	return result
}

func tmTextordBuilder(group tmObj, style *tmStyle) mmNode {
	ch := tmStr(group, "text")
	cp := tmCodePointAt(ch)
	if style.fontFamily() == "textsc" {
		if 96 < cp && cp < 123 {
			ch = tmSmallCaps[ch]
		}
	}
	text := tmMakeText(ch, tmStr(group, "mode"), style)
	variant := tmGetVariant(group, style)
	if variant == "" {
		variant = "normal"
	}
	var node *mathNode
	if tmNumberRe.MatchString(tmStr(group, "text")) {
		tag := "mn"
		if tmStr(group, "mode") == "text" {
			tag = "mtext"
		}
		if variant == "italic" || variant == "bold-italic" {
			return tmItalicNumber(text, variant, tag)
		}
		if variant != "normal" {
			text.text = tmVariantUnits(text.text, variant)
		}
		node = mn(tag, text)
	} else if tmStr(group, "mode") == "text" {
		if variant != "normal" {
			text.text = tmVariantChar(text.text, variant)
		}
		node = mn("mtext", text)
	} else if tmPrimes[tmStr(group, "text")] {
		node = mn("mo", text)
		node.classes = append(node.classes, "tml-prime")
	} else {
		origText := text.text
		if variant != "italic" {
			text.text = tmVariantChar(text.text, variant)
		}
		node = mn("mi", text)
		if text.text == origText && tmLatinRe.MatchString(origText) {
			node.setAttribute("mathvariant", "italic")
		}
	}
	return node
}

// ---- symbolsOp.js (atoms) ----------------------------------------------------------------------------------------

var tmShortAtoms = map[string]bool{"\\shortmid": true, "\\nshortmid": true, "\\shortparallel": true, "\\nshortparallel": true, "\\smallsetminus": true}
var tmArrowAtoms = map[string]bool{"\\Rsh": true, "\\Lsh": true, "\\restriction": true}

func tmIsArrow(str string) bool {
	if jsLen(str) == 1 {
		cp := tmCodePointAt(str)
		return 0x218f < cp && cp < 0x2200
	}
	return strings.Contains(str, "arrow") || strings.Contains(str, "harpoon") || tmArrowAtoms[str]
}

func tmAtomBuilder(group tmObj, style *tmStyle) mmNode {
	text := tmStr(group, "text")
	family := tmStr(group, "family")
	node := mn("mo", tmMakeText(text, tmStr(group, "mode"), nil))
	if family == "punct" {
		node.setAttribute("separator", "true")
	} else if family == "open" || family == "close" {
		if family == "open" {
			node.setAttribute("form", "prefix")
			node.setAttribute("stretchy", "false")
		} else {
			node.setAttribute("form", "postfix")
			node.setAttribute("stretchy", "false")
		}
	} else if text == "\\mid" {
		node.setAttribute("lspace", "0.22em")
		node.setAttribute("rspace", "0.22em")
		node.setAttribute("stretchy", "false")
	} else if family == "rel" && tmIsArrow(text) {
		node.setAttribute("stretchy", "false")
	} else if tmShortAtoms[text] {
		node.setAttribute("mathsize", "70%")
	} else if text == ":" {
		node.attrs.set("lspace", "0.2222em")
		node.attrs.set("rspace", "0.2222em")
	} else if tmTruthy(group["needsSpacing"]) {
		if family == "bin" {
			return mn("mrow", tmPadding(0.222), node, tmPadding(0.222))
		}
		return mn("mrow", tmPadding(0.2778), node, tmPadding(0.2778))
	}
	return node
}

// ---- supsub.js ---------------------------------------------------------------------------------------------------

const (
	tmSmallPad  = "DHKLUcegorsuvxyzΠΥΨαδηιμνοτυχϵ"
	tmMediumPad = "BCEFGIMNOPQRSTXZlpqtwΓΘΞΣΦΩβεζθξρςφψϑϕϱ"
	tmLargePad  = "AJdfΔΛ"
)

func tmSupsubBuilder(group tmObj, style *tmStyle) mmNode {
	isBracket := false
	var isOver, isSup bool
	appendApplyFunction := false
	appendSpace := false
	needsLeadingSpace := false
	base, _ := group["base"].(tmObj)
	if base != nil && tmStr(base, "type") == "horizBracket" {
		isSup = tmTruthy(group["sup"])
		if isSup == tmTruthy(base["isOver"]) {
			isBracket = true
			isOver = tmTruthy(base["isOver"])
		}
	}
	if base != nil && !tmTruthy(group["stack"]) && (tmStr(base, "type") == "op" || tmStr(base, "type") == "operatorname") {
		base["parentIsSupSub"] = true
		appendApplyFunction = !tmTruthy(base["symbol"])
		appendSpace = appendApplyFunction && !tmTruthy(group["isFollowedByDelimiter"])
		needsLeadingSpace = tmTruthy(base["needsLeadingSpace"])
	}
	var children []mmNode
	if tmTruthy(group["stack"]) && len(tmList(group["base"].(tmObj)["body"])) == 1 {
		children = []mmNode{tmBuildGroup(tmList(base["body"])[0], style)}
	} else {
		children = []mmNode{tmBuildGroup(group["base"], style)}
	}
	childStyle := style.inSubOrSup()
	if tmTruthy(group["sub"]) {
		sub := tmBuildGroup(group["sub"], childStyle)
		if style.level() == 3 {
			tmSetAttr(sub, "scriptlevel", "2")
		}
		children = append(children, sub)
	}
	if tmTruthy(group["sup"]) {
		sup := tmBuildGroup(group["sup"], childStyle)
		if style.level() == 3 {
			tmSetAttr(sup, "scriptlevel", "2")
		}
		if base != nil {
			if text, ok := base["text"].(string); ok && text != "" && jsLen(text) == 1 {
				switch {
				case strings.Contains(tmSmallPad, text):
					mmPushClass(sup, "tml-sml-pad")
				case strings.Contains(tmMediumPad, text):
					mmPushClass(sup, "tml-med-pad")
				case strings.Contains(tmLargePad, text):
					mmPushClass(sup, "tml-lrg-pad")
				}
			}
		}
		children = append(children, sup)
	}
	var nodeType string
	baseType := ""
	if base != nil {
		baseType = tmStr(base, "type")
	}
	display := style.level() == tmStyleDisplay
	switch {
	case isBracket:
		if isOver {
			nodeType = "mover"
		} else {
			nodeType = "munder"
		}
	case !tmTruthy(group["sub"]):
		if base != nil && baseType == "op" && tmTruthy(base["limits"]) && (display || tmTruthy(base["alwaysHandleSupSub"])) {
			nodeType = "mover"
		} else if base != nil && baseType == "operatorname" && tmTruthy(base["alwaysHandleSupSub"]) && (tmTruthy(base["limits"]) || display) {
			nodeType = "mover"
		} else {
			nodeType = "msup"
		}
	case !tmTruthy(group["sup"]):
		if tmTruthy(group["stack"]) {
			nodeType = "munder"
		} else if base != nil && baseType == "op" && tmTruthy(base["limits"]) && (display || tmTruthy(base["alwaysHandleSupSub"])) {
			nodeType = "munder"
		} else if base != nil && baseType == "operatorname" && tmTruthy(base["alwaysHandleSupSub"]) && (tmTruthy(base["limits"]) || display) {
			nodeType = "munder"
		} else {
			nodeType = "msub"
		}
	default:
		if base != nil && ((baseType == "op" && tmTruthy(base["limits"])) || baseType == "multiscript") && (display || tmTruthy(base["alwaysHandleSupSub"])) {
			nodeType = "munderover"
		} else if base != nil && baseType == "operatorname" && tmTruthy(base["alwaysHandleSupSub"]) && (display || tmTruthy(base["limits"])) {
			nodeType = "munderover"
		} else {
			nodeType = "msubsup"
		}
	}
	var node mmNode = newMathNode(nodeType, children, nil, nil)
	if appendApplyFunction {
		operator := mn("mo", tmMakeText("\u2061", "text", nil))
		var frag *docFragment
		if needsLeadingSpace {
			space := mn("mspace")
			space.setAttribute("width", "0.1667em")
			frag = newDocFragment([]mmNode{space, node, operator})
		} else {
			frag = newDocFragment([]mmNode{node, operator})
		}
		if appendSpace {
			space := mn("mspace")
			space.setAttribute("width", "0.1667em")
			frag.children = append(frag.children, space)
		}
		node = frag
	} else if nodeType == "mover" || nodeType == "munder" || nodeType == "munderover" {
		node = mn("mrow", node)
	}
	return node
}

// ---- rule.js, raise.js -------------------------------------------------------------------------------------------

func tmRaiseBuilder(group tmObj, style *tmStyle) mmNode {
	newStyle := style.withLevel(tmStyleText)
	node := mn("mpadded", tmBuildGroup(group["body"], newStyle))
	dy := tmCalculateSize(tmSizeOf(group["dy"]), style)
	node.setAttribute("voffset", dy.String())
	if dy.number > 0 {
		node.style.set("padding", dy.String()+" 0 0 0")
	} else {
		node.style.set("padding", "0 0 "+jsNumberString(math.Abs(dy.number))+dy.unit+" 0")
	}
	return node
}

var tmXHeights = map[string]bool{"a": true, "c": true, "e": true, "ı": true, "m": true, "n": true, "o": true, "r": true, "s": true, "u": true, "v": true, "w": true,
	"x": true, "z": true, "α": true, "ε": true, "ι": true, "κ": true, "ν": true, "ο": true, "π": true, "σ": true, "τ": true, "υ": true, "ω": true,
	"\\alpha": true, "\\epsilon": true, "\\iota": true, "\\kappa": true, "\\nu": true, "\\omega": true, "\\pi": true, "\\tau": true}

var tmNumSubs = []string{"₀", "₁", "₂", "₃", "₄", "₅", "₆", "₇", "₈", "₉"}
var tmNumSups = []string{"⁰", "¹", "²", "³", "⁴", "⁵", "⁶", "⁷", "⁸", "⁹"}
var tmDigitRe = regexp.MustCompile(`^[0-9]$`)

func tmSfracPart(group tmObj, what string) string {
	out := ""
	for _, n := range tmList(group["body"]) {
		node := n.(tmObj)
		if tmStr(node, "type") != "textord" || !tmDigitRe.MatchString(tmStr(node, "text")) {
			tmThrow(what+" must be an integer.", node)
		}
		out += tmStr(node, "text")
	}
	return out
}

func init() {
	// mclass.js
	tmDefineFunction("mclass", []string{"\\mathord", "\\mathbin", "\\mathrel", "\\mathopen", "\\mathclose", "\\mathpunct", "\\mathinner"},
		tmProps{numArgs: 1, primitive: true},
		func(ctx *tmCtx, args, opt []any) any {
			parser := ctx.parser
			body := args[0].(tmObj)
			isCharacterBox := tmIsCharacterBox(body)
			mustPromote := true
			mord := tmObj{"type": "mathord", "text": "", "mode": parser.mode}
			var arr []any
			if tmTruthy(body["body"]) {
				arr = tmList(body["body"])
			} else {
				arr = []any{body}
			}
			for _, a := range arr {
				arg := a.(tmObj)
				if tmTextAtomTypes[tmStr(arg, "type")] {
					text, _ := arg["text"].(string)
					if sym := tmSymbolTable(parser.mode)[text]; sym != nil {
						mord["text"] = tmStr(mord, "text") + sym.replace
					} else if text != "" {
						mord["text"] = tmStr(mord, "text") + text
					} else if tmTruthy(arg["body"]) {
						for _, e := range tmList(arg["body"]) {
							mord["text"] = tmStr(mord, "text") + jsStr(e.(tmObj)["text"])
						}
					}
				} else {
					mustPromote = false
					break
				}
			}
			if mustPromote && ctx.funcName == "\\mathord" && jsLen(tmStr(mord, "text")) > 1 {
				return mord
			}
			var bodyArg any = body
			if mustPromote {
				bodyArg = mord
			}
			return tmObj{"type": "mclass", "mode": parser.mode, "mclass": "m" + ctx.funcName[5:], "body": tmOrdArgument(bodyArg), "isCharacterBox": isCharacterBox,
				"mustPromote": mustPromote}
		}, tmMclassBuilder)
	tmDefineFunction("mclass", []string{"\\@binrel"}, tmProps{numArgs: 2},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "mclass", "mode": ctx.parser.mode, "mclass": tmBinrelClass(args[0].(tmObj)), "body": tmOrdArgument(args[1]), "isCharacterBox": tmIsCharacterBox(args[1])}
		}, nil)
	tmDefineFunction("mclass", []string{"\\stackrel", "\\overset", "\\underset"}, tmProps{numArgs: 2},
		func(ctx *tmCtx, args, opt []any) any {
			baseArg := args[1].(tmObj)
			shiftedArg := args[0].(tmObj)
			var mclass string
			if ctx.funcName != "\\stackrel" {
				mclass = tmBinrelClass(baseArg)
			} else {
				mclass = "mrel"
			}
			baseType := "ordgroup"
			if mclass == "mrel" || mclass == "mbin" {
				baseType = "op"
			}
			baseOp := tmObj{"type": baseType, "mode": baseArg["mode"], "limits": true, "alwaysHandleSupSub": true, "parentIsSupSub": false, "symbol": false,
				"suppressBaseShift": ctx.funcName != "\\stackrel", "body": tmOrdArgument(baseArg)}
			var sup, sub any
			if ctx.funcName == "\\underset" {
				sub = shiftedArg
			} else {
				sup = shiftedArg
			}
			return tmObj{"type": "supsub", "mode": shiftedArg["mode"], "stack": true, "base": baseOp, "sup": sup, "sub": sub}
		}, tmMclassBuilder)

	// multiscript.js
	tmDefineFunction("multiscript", []string{"\\sideset", "\\pres@cript"}, tmProps{numArgs: 3},
		func(ctx *tmCtx, args, opt []any) any {
			parser := ctx.parser
			a0, a1, a2 := args[0].(tmObj), args[1].(tmObj), args[2].(tmObj)
			if len(tmList(a2["body"])) == 0 {
				tmThrowf(ctx.funcName + "cannot parse an empty base.")
			}
			base := tmList(a2["body"])[0].(tmObj)
			if parser.settings.strict && ctx.funcName == "\\sideset" && !tmTruthy(base["symbol"]) {
				tmThrowf("The base of \\sideset must be a big operator. Try \\prescript.")
			}
			l0, l1 := tmList(a0["body"]), tmList(a1["body"])
			if (len(l0) > 0 && tmType(l0[0]) != "supsub") || (len(l1) > 0 && tmType(l1[0]) != "supsub") {
				tmThrow("\\sideset can parse only subscripts and superscripts in its first two arguments", ctx.token)
			}
			var prescripts, postscripts tmObj
			if len(l0) > 0 {
				prescripts = l0[0].(tmObj)
			}
			if len(l1) > 0 {
				postscripts = l1[0].(tmObj)
			}
			switch {
			case prescripts == nil && postscripts == nil:
				return base
			case prescripts == nil:
				return tmObj{"type": "styling", "mode": parser.mode, "scriptLevel": "text", "body": []any{tmObj{"type": "supsub", "mode": parser.mode, "base": base,
					"sup": postscripts["sup"], "sub": postscripts["sub"]}}}
			}
			return tmObj{"type": "multiscript", "mode": parser.mode, "isSideset": ctx.funcName == "\\sideset", "prescripts": prescripts, "postscripts": nilNode(postscripts), "base": base}
		},
		func(group tmObj, style *tmStyle) mmNode {
			base := tmBuildGroup(group["base"], style)
			prescriptsNode := mn("mprescripts")
			noneNode := mn("none")
			pre := group["prescripts"].(tmObj)
			preSub := tmMultiscriptBuildGroup(pre["sub"], style, noneNode)
			preSup := tmMultiscriptBuildGroup(pre["sup"], style, noneNode)
			if tmTruthy(group["isSideset"]) {
				tmSetAttr(preSub, "style", "text-align: left;")
				tmSetAttr(preSup, "style", "text-align: left;")
			}
			var children []mmNode
			if post, ok := group["postscripts"].(tmObj); ok && post != nil {
				postSub := tmMultiscriptBuildGroup(post["sub"], style, noneNode)
				postSup := tmMultiscriptBuildGroup(post["sup"], style, noneNode)
				children = []mmNode{base, postSub, postSup, prescriptsNode, preSub, preSup}
			} else {
				children = []mmNode{base, prescriptsNode, preSub, preSup}
			}
			return newMathNode("mmultiscripts", children, nil, nil)
		})

	// not.js
	tmDefineFunction("not", []string{"\\not"}, tmProps{numArgs: 1, primitive: true},
		func(ctx *tmCtx, args, opt []any) any {
			isCharacterBox := tmIsCharacterBox(args[0])
			var body []any
			if isCharacterBox {
				body = tmOrdArgument(args[0])
				b0 := body[0].(tmObj)
				text, hasText := b0["text"].(string)
				if !hasText {
					panic(&tmJSError{"Cannot read properties of undefined (reading 'charAt')"})
				}
				if strings.HasPrefix(text, "\\") {
					text = tmSymbolsMath[text].replace
				}
				u := tmUTF16Units(text)
				first := ""
				rest := ""
				if len(u) > 0 {
					first = tmU16s(u[:1])
					rest = tmU16s(u[1:])
				}
				b0["text"] = first + "\u0338" + rest
			} else {
				notNode := tmObj{"type": "textord", "mode": "math", "text": "\u0338"}
				kernNode := tmObj{"type": "kern", "mode": "math", "dimension": tmSize{-0.6, "em"}}
				body = []any{notNode, kernNode, args[0]}
			}
			return tmObj{"type": "not", "mode": ctx.parser.mode, "body": body, "isCharacterBox": isCharacterBox}
		},
		func(group tmObj, style *tmStyle) mmNode {
			if tmTruthy(group["isCharacterBox"]) {
				inner := tmBuildExpression(tmList(group["body"]), style, true)
				return inner[0]
			}
			return tmBuildExpressionRow(tmList(group["body"]), style, false)
		})

	// op.js
	bigOps := []string{"\\coprod", "\\bigvee", "\\bigwedge", "\\biguplus", "\\bigcupplus", "\\bigcupdot", "\\bigcap", "\\bigcup", "\\bigdoublevee", "\\bigdoublewedge",
		"\\intop", "\\prod", "\\sum", "\\bigotimes", "\\bigoplus", "\\bigodot", "\\bigsqcap", "\\bigsqcup", "\\bigtimes", "\\smallint", "\u220F", "\u2210", "\u2211",
		"\u22c0", "\u22c1", "\u22c2", "\u22c3", "\u2a00", "\u2a01", "\u2a02", "\u2a03", "\u2a04", "\u2a05", "\u2a06", "\u2a07", "\u2a08", "\u2a09"}
	tmDefineFunction("op", bigOps, tmProps{numArgs: 0},
		func(ctx *tmCtx, args, opt []any) any {
			fName := ctx.funcName
			if jsLen(fName) == 1 {
				fName = tmSingleCharBigOps[fName]
			}
			return tmObj{"type": "op", "mode": ctx.parser.mode, "limits": true, "parentIsSupSub": false, "symbol": true, "stack": false, "name": fName}
		}, tmOpBuilder)
	tmDefineFunction("op", []string{"\\mathop"}, tmProps{numArgs: 1, primitive: true},
		func(ctx *tmCtx, args, opt []any) any {
			body := args[0].(tmObj)
			var arr []any
			if tmTruthy(body["body"]) {
				arr = tmList(body["body"])
			} else {
				arr = []any{body}
			}
			isSymbol := len(arr) == 1 && tmOrdAtomTypes[tmType(arr[0])]
			var name, bodyOut any
			if isSymbol {
				name = arr[0].(tmObj)["text"]
			} else {
				bodyOut = tmOrdArgument(body)
			}
			return tmObj{"type": "op", "mode": ctx.parser.mode, "limits": true, "parentIsSupSub": false, "symbol": isSymbol, "fromMathOp": true, "stack": false,
				"name": name, "body": bodyOut}
		}, tmOpBuilder)
	tmDefineFunction("op", []string{"\\arcsin", "\\arccos", "\\arctan", "\\arctg", "\\arcctg", "\\arg", "\\ch", "\\cos", "\\cosec", "\\cosh", "\\cot", "\\cotg", "\\coth",
		"\\csc", "\\ctg", "\\cth", "\\deg", "\\dim", "\\exp", "\\hom", "\\ker", "\\lg", "\\ln", "\\log", "\\sec", "\\sin", "\\sinh", "\\sh", "\\sgn", "\\tan", "\\tanh",
		"\\tg", "\\th"}, tmProps{numArgs: 0}, tmFuncOpHandler(false), tmOpBuilder)
	tmDefineFunction("op", []string{"\\det", "\\gcd", "\\inf", "\\lim", "\\max", "\\min", "\\Pr", "\\sup"}, tmProps{numArgs: 0}, tmFuncOpHandler(true), tmOpBuilder)
	tmDefineFunction("op", []string{"\\int", "\\iint", "\\iiint", "\\iiiint", "\\oint", "\\oiint", "\\oiiint", "\\intclockwise", "\\varointclockwise", "\\intbar", "\\intBar",
		"\\fint", "\\rppolint", "\\scpolint", "\\pointint", "\\sqint", "\\intlarhk", "\\intx", "\\intcap", "\\intcup", "\u222b", "\u222c", "\u222d", "\u222e", "\u222f",
		"\u2230", "\u2231", "\u2232", "\u2a0c", "\u2a0d", "\u2a0e", "\u2a0f", "\u2a12", "\u2a13", "\u2a15", "\u2a16", "\u2a17", "\u2a18", "\u2a19", "\u2a1a"},
		tmProps{numArgs: 0, allowedInArgument: true},
		func(ctx *tmCtx, args, opt []any) any {
			fName := ctx.funcName
			if jsLen(fName) == 1 {
				fName = tmSingleCharIntegrals[fName]
			}
			return tmObj{"type": "op", "mode": ctx.parser.mode, "limits": false, "parentIsSupSub": false, "symbol": true, "stack": false, "name": fName}
		}, tmOpBuilder)

	// operatorname.js
	tmDefineFunction("operatorname", []string{"\\operatorname@", "\\operatornamewithlimits"}, tmProps{numArgs: 1, allowedInArgument: true},
		func(ctx *tmCtx, args, opt []any) any {
			parser := ctx.parser
			prevAtomType := parser.prevAtomType
			next := parser.gullet.future().text
			return tmObj{"type": "operatorname", "mode": parser.mode, "body": tmOrdArgument(args[0]), "alwaysHandleSupSub": ctx.funcName == "\\operatornamewithlimits",
				"limits": false, "parentIsSupSub": false, "isFollowedByDelimiter": tmIsDelimiter(next), "needsLeadingSpace": len(prevAtomType) > 0 && tmOrdTypes[prevAtomType]}
		}, tmOperatornameBuilder)

	// ordgroup.js
	tmDefineBuilder("ordgroup", func(group tmObj, style *tmStyle) mmNode {
		return tmBuildExpressionRow(tmList(group["body"]), style, tmTruthy(group["semisimple"]))
	})

	// phantom.js
	tmDefineFunction("phantom", []string{"\\phantom"}, tmProps{numArgs: 1, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "phantom", "mode": ctx.parser.mode, "body": tmOrdArgument(args[0])}
		},
		func(group tmObj, style *tmStyle) mmNode {
			return newMathNode("mphantom", tmBuildExpression(tmList(group["body"]), style, false), nil, nil)
		})
	tmDefineFunction("hphantom", []string{"\\hphantom"}, tmProps{numArgs: 1, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "hphantom", "mode": ctx.parser.mode, "body": args[0]}
		},
		func(group tmObj, style *tmStyle) mmNode {
			inner := tmBuildExpression(tmOrdArgument(group["body"]), style, false)
			node := mn("mpadded", newMathNode("mphantom", inner, nil, nil))
			node.setAttribute("height", "0px")
			node.setAttribute("depth", "0px")
			return node
		})
	tmDefineFunction("vphantom", []string{"\\vphantom"}, tmProps{numArgs: 1, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "vphantom", "mode": ctx.parser.mode, "body": args[0]}
		},
		func(group tmObj, style *tmStyle) mmNode {
			inner := tmBuildExpression(tmOrdArgument(group["body"]), style, false)
			node := mn("mpadded", newMathNode("mphantom", inner, nil, nil))
			node.setAttribute("width", "0.1px")
			return node
		})

	// pmb.js
	tmDefineFunction("pmb", []string{"\\pmb"}, tmProps{numArgs: 1, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "pmb", "mode": ctx.parser.mode, "body": tmOrdArgument(args[0])}
		},
		func(group tmObj, style *tmStyle) mmNode {
			inner := tmBuildExpression(tmList(group["body"]), style, false)
			node := wrapWithMstyle(inner)
			node.setAttribute("style", "font-weight:bold")
			return node
		})

	// raise.js
	tmDefineFunction("raise", []string{"\\raise", "\\lower"}, tmProps{numArgs: 2, argTypes: []string{"size", "primitive"}, primitive: true},
		func(ctx *tmCtx, args, opt []any) any {
			amount := tmSizeOf(tmAssertNodeType(args[0], "size")["value"])
			if ctx.funcName == "\\lower" {
				amount.number *= -1
			}
			return tmObj{"type": "raise", "mode": ctx.parser.mode, "dy": amount, "body": args[1]}
		}, tmRaiseBuilder)
	tmDefineFunction("raise", []string{"\\raisebox"}, tmProps{numArgs: 2, argTypes: []string{"size", "hbox"}, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			amount := tmAssertNodeType(args[0], "size")["value"]
			return tmObj{"type": "raise", "mode": ctx.parser.mode, "dy": amount, "body": args[1]}
		}, tmRaiseBuilder)

	// ref.js
	tmDefineFunction("ref", []string{"\\ref", "\\eqref"}, tmProps{numArgs: 1, argTypes: []string{"raw"}},
		func(ctx *tmCtx, args, opt []any) any {
			s := tmStr(args[0].(tmObj), "string")
			return tmObj{"type": "ref", "mode": ctx.parser.mode, "funcName": ctx.funcName, "string": tmInvalidIDRe.ReplaceAllString(s, "")}
		},
		func(group tmObj, style *tmStyle) mmNode {
			classes := []string{"tml-ref", "tml-eqref"}
			if tmStr(group, "funcName") == "\\ref" {
				classes = []string{"tml-ref"}
			}
			return &anchorNode{href: "#" + tmStr(group, "string"), classes: classes}
		})

	// reflect.js
	tmDefineFunction("reflect", []string{"\\reflectbox"}, tmProps{numArgs: 1, argTypes: []string{"hbox"}, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "reflect", "mode": ctx.parser.mode, "body": args[0]}
		},
		func(group tmObj, style *tmStyle) mmNode {
			node := tmBuildGroup(group["body"], style)
			mmStyle(node).set("transform", "scaleX(-1)")
			return node
		})

	// relax.js
	tmDefineFunction("internal", []string{"\\relax"}, tmProps{numArgs: 0, allowedInText: true, allowedInArgument: true},
		func(ctx *tmCtx, args, opt []any) any { return tmInternal(ctx.parser) }, nil)

	// rule.js
	tmDefineFunction("rule", []string{"\\rule"}, tmProps{numArgs: 2, numOptionalArgs: 1, allowedInText: true, argTypes: []string{"size", "size", "size"}},
		func(ctx *tmCtx, args, opt []any) any {
			var shift any
			if opt[0] != nil {
				shift = tmAssertNodeType(opt[0], "size")["value"]
			}
			width := tmAssertNodeType(args[0], "size")
			height := tmAssertNodeType(args[1], "size")
			return tmObj{"type": "rule", "mode": ctx.parser.mode, "shift": shift, "width": width["value"], "height": height["value"]}
		},
		func(group tmObj, style *tmStyle) mmNode {
			width := tmCalculateSize(tmSizeOf(group["width"]), style)
			height := tmCalculateSize(tmSizeOf(group["height"]), style)
			shift := tmSize{0, "em"}
			if tmTruthy(group["shift"]) {
				shift = tmCalculateSize(tmSizeOf(group["shift"]), style)
			}
			color := "black"
			if c := style.color(); tmTruthy(c) {
				color = jsStr(c)
			}
			rule := mn("mspace")
			if width.number > 0 && height.number > 0 {
				rule.setAttribute("mathbackground", color)
			}
			rule.setAttribute("width", width.String())
			rule.setAttribute("height", height.String())
			if shift.number == 0 {
				return rule
			}
			wrapper := mn("mpadded", rule)
			if shift.number >= 0 {
				wrapper.setAttribute("height", "+"+shift.String())
			} else {
				wrapper.setAttribute("height", shift.String())
				wrapper.setAttribute("depth", "+"+jsNumberString(-shift.number)+shift.unit)
			}
			wrapper.setAttribute("voffset", shift.String())
			return wrapper
		})

	// sfrac.js
	tmDefineFunction("sfrac", []string{"\\sfrac"}, tmProps{numArgs: 2, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			numerator := tmSfracPart(args[0].(tmObj), "Numerator")
			denominator := tmSfracPart(args[1].(tmObj), "Denominator")
			return tmObj{"type": "sfrac", "mode": ctx.parser.mode, "numerator": numerator, "denominator": denominator}
		},
		func(group tmObj, style *tmStyle) mmNode {
			var num, den strings.Builder
			for _, c := range tmStr(group, "numerator") {
				num.WriteString(tmNumSups[c-'0'])
			}
			for _, c := range tmStr(group, "denominator") {
				den.WriteString(tmNumSubs[c-'0'])
			}
			return newMathNode("mn", []mmNode{&textNode{num.String() + "\u2044" + den.String()}}, []string{"special-fraction"}, nil)
		})

	// sizing.js
	tmDefineFunction("sizing", []string{"\\tiny", "\\sixptsize", "\\Tiny", "\\scriptsize", "\\footnotesize", "\\small", "\\normalsize", "\\large", "\\Large", "\\LARGE",
		"\\huge", "\\Huge"}, tmProps{numArgs: 0, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			body := ctx.parser.parseExpression(false, ctx.breakOnTokenText, true)
			return tmObj{"type": "sizing", "mode": ctx.parser.mode, "funcName": ctx.funcName, "body": body}
		},
		func(group tmObj, style *tmStyle) mmNode {
			size := tmSizeMap[tmStr(group, "funcName")]
			newStyle := style.withFontSize(size)
			inner := tmBuildExpression(tmList(group["body"]), newStyle, false)
			node := wrapWithMstyle(inner)
			factor := tmToFixed(size/style.fontSize(), 4)
			node.setAttribute("mathsize", factor+"em")
			return node
		})

	// smash.js
	tmDefineFunction("smash", []string{"\\smash"}, tmProps{numArgs: 1, numOptionalArgs: 1, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			smashHeight, smashDepth := false, false
			if opt[0] != nil {
				tbArg := tmAssertNodeType(opt[0], "ordgroup")
				for _, n := range tmList(tbArg["body"]) {
					letter, _ := n.(tmObj)["text"].(string)
					if letter == "t" {
						smashHeight = true
					} else if letter == "b" {
						smashDepth = true
					} else {
						smashHeight, smashDepth = false, false
						break
					}
				}
			} else {
				smashHeight, smashDepth = true, true
			}
			return tmObj{"type": "smash", "mode": ctx.parser.mode, "body": args[0], "smashHeight": smashHeight, "smashDepth": smashDepth}
		},
		func(group tmObj, style *tmStyle) mmNode {
			node := mn("mpadded", tmBuildGroup(group["body"], style))
			if tmTruthy(group["smashHeight"]) {
				node.setAttribute("height", "0px")
			}
			if tmTruthy(group["smashDepth"]) {
				node.setAttribute("depth", "0px")
			}
			return node
		})

	// sqrt.js
	tmDefineFunction("sqrt", []string{"\\sqrt"}, tmProps{numArgs: 1, numOptionalArgs: 1},
		func(ctx *tmCtx, args, opt []any) any {
			body := args[0].(tmObj)
			if list := tmList(body["body"]); len(list) == 1 {
				if text, ok := list[0].(tmObj)["text"].(string); ok && text != "" && tmXHeights[text] {
					body["body"] = append(list, tmObj{"type": "rule", "mode": "math", "shift": nil, "width": tmSize{0, "pt"}, "height": tmSize{0.5, "em"}})
				}
			}
			return tmObj{"type": "sqrt", "mode": ctx.parser.mode, "body": body, "index": opt[0]}
		},
		func(group tmObj, style *tmStyle) mmNode {
			if tmTruthy(group["index"]) {
				return mn("mroot", tmBuildGroup(group["body"], style), tmBuildGroup(group["index"], style.incrementLevel()))
			}
			return mn("msqrt", tmBuildGroup(group["body"], style))
		})

	// styling.js
	tmDefineFunction("styling", []string{"\\displaystyle", "\\textstyle", "\\scriptstyle", "\\scriptscriptstyle"}, tmProps{numArgs: 0, allowedInText: true, primitive: true},
		func(ctx *tmCtx, args, opt []any) any {
			body := ctx.parser.parseExpression(true, ctx.breakOnTokenText, true)
			scriptLevel := ctx.funcName[1 : len(ctx.funcName)-5]
			return tmObj{"type": "styling", "mode": ctx.parser.mode, "scriptLevel": scriptLevel, "body": body}
		},
		func(group tmObj, style *tmStyle) mmNode {
			scriptLevel := tmStr(group, "scriptLevel")
			newStyle := style.withLevel(tmStyleMap[scriptLevel])
			inner := tmBuildExpression(tmList(group["body"]), newStyle, false)
			node := wrapWithMstyle(inner)
			attr := tmStyleAttributes[scriptLevel]
			node.setAttribute("scriptlevel", attr[0])
			node.setAttribute("displaystyle", attr[1])
			return node
		})

	// supsub.js and the symbol builders
	tmDefineBuilder("supsub", tmSupsubBuilder)
	tmDefineBuilder("atom", tmAtomBuilder)
	tmDefineBuilder("mathord", tmMathordBuilder)
	tmDefineBuilder("textord", tmTextordBuilder)
	tmDefineBuilder("spacing", func(group tmObj, style *tmStyle) mmNode {
		text := tmStr(group, "text")
		switch text {
		case " ", "\\ ", "~", "\\space", "\\nobreakspace":
			return mn("mtext", &textNode{"\u00a0"})
		case "\\nobreak", "\\allowbreak":
			node := mn("mo")
			if text == "\\nobreak" {
				node.setAttribute("linebreak", "nobreak")
			}
			return node
		}
		tmThrowf("Unknown type of space \"" + text + "\"")
		return nil
	})

	// text.js
	tmDefineFunction("text", []string{"\\text", "\\textrm", "\\textsf", "\\texttt", "\\textnormal", "\\textsc", "\\textbf", "\\textmd", "\\textit", "\\textup", "\\emph"},
		tmProps{numArgs: 1, argTypes: []string{"text"}, allowedInArgument: true, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "text", "mode": ctx.parser.mode, "body": tmOrdArgument(args[0]), "font": ctx.funcName}
		},
		func(group tmObj, style *tmStyle) mmNode {
			newStyle := tmStyleWithFont(group, style)
			mrow := tmBuildExpressionRow(tmList(group["body"]), newStyle, false)
			return tmConsolidateText(mrow)
		})

	// vcenter.js
	tmDefineFunction("vcenter", []string{"\\vcenter"}, tmProps{numArgs: 1, argTypes: []string{"original"}},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "vcenter", "mode": ctx.parser.mode, "body": args[0]}
		},
		func(group tmObj, style *tmStyle) mmNode {
			mtd := mn("mtd", tmBuildGroup(group["body"], style))
			mtd.style.set("padding", "0")
			return mn("mtable", mn("mtr", mtd))
		})

	// verb.js
	tmDefineFunction("verb", []string{"\\verb"}, tmProps{numArgs: 0, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			tmThrowf("\\verb ended by end of line instead of matching delimiter")
			return nil
		},
		func(group tmObj, style *tmStyle) mmNode {
			repl := "\u00a0"
			if tmTruthy(group["star"]) {
				repl = "\u2423"
			}
			node := mn("mtext", &textNode{strings.ReplaceAll(tmStr(group, "body"), " ", repl)})
			node.setAttribute("mathvariant", "monospace")
			return node
		})
}
