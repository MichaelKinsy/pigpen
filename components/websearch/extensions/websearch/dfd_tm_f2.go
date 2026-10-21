package websearch

import (
	"strings"
)

// Temml 0.13.4 (MIT): src/functions/delimiter.js, enclose.js, environment.js, envTag.js, font.js, genfrac.js, hbox.js,
// horizBracket.js, html.js, includegraphics.js, kern.js, label.js, lap.js, math.js, mathchoice.js and utils.js.

// ---- utils.js ----------------------------------------------------------------------------------------------------

func tmGetBaseElem(group any) any {
	g, ok := group.(tmObj)
	if !ok {
		return group
	}
	switch tmStr(g, "type") {
	case "ordgroup", "color":
		if body := tmList(g["body"]); len(body) == 1 {
			return tmGetBaseElem(body[0])
		}
		return g
	case "font":
		return tmGetBaseElem(g["body"])
	}
	return g
}

func tmIsCharacterBox(group any) bool {
	t := tmType(tmGetBaseElem(group))
	return t == "mathord" || t == "textord" || t == "atom"
}

// tmSmallsHas is smalls.indexOf(v) > -1 for a value that may be undefined.
func tmSmallsHas(v any) bool {
	s, ok := v.(string)
	if !ok {
		s = "undefined"
	}
	return strings.Contains(tmSmalls, s)
}

// ---- cancelto.js builder -----------------------------------------------------------------------------------------

var tmCancelRe = mustRE(`[f∫∑]`)

func tmFirstBodyText(group tmObj) any {
	body, ok := group["body"].(tmObj)
	if !ok {
		panic(&tmJSError{"Cannot read properties of undefined (reading 'body')"})
	}
	list := tmList(body["body"])
	if len(list) == 0 {
		panic(&tmJSError{"Cannot read properties of undefined (reading 'text')"})
	}
	return list[0].(tmObj)["text"]
}

func tmCanceltoBuilder(group tmObj, style *tmStyle) mmNode {
	fromNode := newMathNode("mrow", []mmNode{tmBuildGroup(group["body"], style)}, []string{"ff-narrow"}, nil)
	phantom := mn("mphantom", tmBuildGroup(group["body"], style))
	arrow := newMathNode("mrow", []mmNode{phantom}, []string{"tml-cancelto"}, nil)
	arrow.style.set("color", style.color())
	isChar := tmTruthy(group["isCharacterBox"])
	if isChar && tmSmallsHas(tmFirstBodyText(group)) {
		arrow.style.set("left", "0.1em")
		arrow.style.set("width", "90%")
	}
	node := newMathNode("mrow", []mmNode{fromNode, arrow}, []string{"menclose"}, nil)
	test := func() bool {
		t, _ := tmFirstBodyText(group).(string)
		return tmCancelRe.MatchString(t)
	}
	if !isChar || test() {
		phantom.style.set("paddingRight", "0.2em")
	} else {
		phantom.style.set("padding", "0.5ex 0.1em 0 0")
		strut := mn("mspace")
		strut.setAttribute("height", "0.85em")
		fromNode.children = append(fromNode.children, strut)
	}
	var dummyNode mmNode
	if isChar {
		d := mn("mspace")
		d.setAttribute("height", "1em")
		dummyNode = d
	} else {
		inner := tmBuildGroup(group["body"], style)
		zeroWidth := mn("mpadded", inner)
		zeroWidth.setAttribute("width", "0.1px")
		dummyNode = mn("mphantom", zeroWidth)
	}
	toNode := tmBuildGroup(group["to"], style)
	mmStyle(toNode).set("color", style.color())
	zeroWidthTo := mn("mpadded", toNode)
	if !isChar || test() {
		w := mn("mspace")
		w.setAttribute("width", "0.2em")
		zeroWidthTo.children = append([]mmNode{w}, zeroWidthTo.children...)
	}
	zeroWidthTo.setAttribute("width", "0.1px")
	mover := mn("mover", dummyNode, zeroWidthTo)
	nudgeLeft := newMathNode("mrow", nil, []string{"ff-nudge-left"}, nil)
	return newDocFragment([]mmNode{tmMakeRow([]mmNode{node, mover}, false), nudgeLeft})
}

// ---- delimiter.js ------------------------------------------------------------------------------------------------

type tmDelimSize struct {
	mclass string
	size   int
}

var tmDelimiterSizes = map[string]tmDelimSize{
	"\\bigl": {"mopen", 1}, "\\Bigl": {"mopen", 2}, "\\biggl": {"mopen", 3}, "\\Biggl": {"mopen", 4},
	"\\bigr": {"mclose", 1}, "\\Bigr": {"mclose", 2}, "\\biggr": {"mclose", 3}, "\\Biggr": {"mclose", 4},
	"\\bigm": {"mrel", 1}, "\\Bigm": {"mrel", 2}, "\\biggm": {"mrel", 3}, "\\Biggm": {"mrel", 4},
	"\\big": {"mord", 1}, "\\Big": {"mord", 2}, "\\bigg": {"mord", 3}, "\\Bigg": {"mord", 4},
}

// tmLeftToRight is leftToRight in the order of its keys (the function names of the "delimiter" type).
var tmLeftToRightKeys = []string{"(", "\\lparen", "[", "\\lbrack", "\\{", "\\lbrace", "⦇", "\\llparenthesis", "\\lfloor", "\u230a", "\\lceil", "\u2308",
	"\\langle", "\u27e8", "\\lAngle", "\u27ea", "\\llangle", "⦉", "\\lvert", "\\lVert", "\\lgroup", "\u27ee", "\\lmoustache", "\u23b0", "\\llbracket",
	"\u27e6", "\\lBrace", "\u2983"}

var tmLeftToRight = map[string]string{
	"(": ")", "\\lparen": "\\rparen", "[": "]", "\\lbrack": "\\rbrack", "\\{": "\\}", "\\lbrace": "\\rbrace", "⦇": "⦈", "\\llparenthesis": "\\rrparenthesis",
	"\\lfloor": "\\rfloor", "\u230a": "\u230b", "\\lceil": "\\rceil", "\u2308": "\u2309", "\\langle": "\\rangle", "\u27e8": "\u27e9", "\\lAngle": "\\rAngle",
	"\u27ea": "\u27eb", "\\llangle": "\\rrangle", "⦉": "⦊", "\\lvert": "\\rvert", "\\lVert": "\\rVert", "\\lgroup": "\\rgroup", "\u27ee": "\u27ef",
	"\\lmoustache": "\\rmoustache", "\u23b0": "\u23b1", "\\llbracket": "\\rrbracket", "\u27e6": "\u27e7", "\\lBrace": "\\rBrace", "\u2983": "\u2984",
}

var tmDelimiters = func() map[string]bool {
	m := map[string]bool{}
	for _, d := range []string{"(", "\\lparen", ")", "\\rparen", "[", "\\lbrack", "]", "\\rbrack", "\\{", "\\lbrace", "\\}", "\\rbrace", "⦇", "\\llparenthesis",
		"⦈", "\\rrparenthesis", "\\lfloor", "\\rfloor", "\u230a", "\u230b", "\\lceil", "\\rceil", "\u2308", "\u2309", "<", ">", "\\langle", "\u27e8", "\\rangle",
		"\u27e9", "\\lAngle", "\u27ea", "\\rAngle", "\u27eb", "\\llangle", "⦉", "\\rrangle", "⦊", "\\lt", "\\gt", "\\lvert", "\\rvert", "\\lVert", "\\rVert",
		"\\lgroup", "\\rgroup", "\u27ee", "\u27ef", "\\lmoustache", "\\rmoustache", "\u23b0", "\u23b1", "\\llbracket", "\\rrbracket", "\u27e6", "\u27e7",
		"\\lBrace", "\\rBrace", "\u2983", "\u2984", "/", "\\backslash", "|", "\\vert", "\\|", "\\Vert", "\u2016", "\\uparrow", "\\Uparrow", "\\downarrow",
		"\\Downarrow", "\\updownarrow", "\\Updownarrow", "."} {
		m[d] = true
	}
	return m
}()

func tmIsDelimiter(str string) bool {
	if len(str) == 0 {
		return false
	}
	if tmDelimiters[str] {
		return true
	}
	if _, ok := tmDelimiterSizes[str]; ok {
		return true
	}
	return str == "}" || str == "\\left" || str == "\\middle" || str == "\\right"
}

var tmSizeToMaxHeight = []string{"0", "1.2", "1.8", "2.4", "3"}

func tmCheckDelimiter(delim any, funcName string) tmObj {
	d := delim.(tmObj)
	if tmStr(d, "type") == "ordgroup" {
		if body := tmList(d["body"]); len(body) == 1 {
			d = body[0].(tmObj)
		}
	}
	sym := tmCheckSymbolNodeType(d)
	if sym != nil && tmDelimiters[tmStr(sym, "text")] {
		if t := tmStr(sym, "text"); t == "<" || t == "\\lt" {
			sym["text"] = "⟨"
		}
		if t := tmStr(sym, "text"); t == ">" || t == "\\gt" {
			sym["text"] = "⟩"
		}
		return sym
	} else if sym != nil {
		tmThrow("Invalid delimiter '"+tmStr(sym, "text")+"' after '"+funcName+"'", d)
	}
	tmThrow("Invalid delimiter type '"+tmStr(d, "type")+"'", d)
	return nil
}

var tmNeedExplicitStretch = map[string]bool{"/": true, "\\": true, "\\backslash": true, "\u2216": true, "\\vert": true, "|": true}

func tmMakeFenceMo(delim, mode, form string, isStretchy bool) *mathNode {
	text := delim
	if delim == "." {
		text = ""
	}
	node := mn("mo", tmMakeText(text, mode, nil))
	node.setAttribute("fence", "true")
	node.setAttribute("form", form)
	if isStretchy {
		node.setAttribute("stretchy", "true")
	} else {
		node.setAttribute("stretchy", "false")
	}
	return node
}

func tmAssertParsed(group tmObj) {
	if !tmTruthy(group["body"]) {
		panic(&tmJSError{"Bug: The delim ParseNode wasn't fully parsed."})
	}
}

func tmFenceBuilder(isStretchy bool) tmBuilder {
	return func(group tmObj, style *tmStyle) mmNode {
		tmAssertParsed(group)
		inner := tmBuildExpression(tmList(group["body"]), style, false)
		leftNode := tmMakeFenceMo(tmStr(group, "left"), tmStr(group, "mode"), "prefix", isStretchy)
		inner = append([]mmNode{leftNode}, inner...)
		rightNode := tmMakeFenceMo(tmStr(group, "right"), tmStr(group, "mode"), "postfix", isStretchy)
		if body := tmList(group["body"]); len(body) > 0 {
			last := body[len(body)-1].(tmObj)
			if tmStr(last, "type") == "color" && !tmTruthy(last["isTextColor"]) {
				rightNode.setAttribute("mathcolor", last["color"])
			}
		}
		inner = append(inner, rightNode)
		return tmMakeRow(inner, false)
	}
}

// ---- enclose.js --------------------------------------------------------------------------------------------------

func tmEncloseBuilder(group tmObj, style *tmStyle) mmNode {
	label := tmStr(group, "label")
	tag := "menclose"
	if label == "\\boxed" || label == "\\fcolorbox" || label == "\\colorbox" {
		tag = "mrow"
	}
	node := mn(tag, tmBuildGroup(group["body"], style))
	push := func(classes ...string) *mathNode { return newMathNode("mrow", nil, classes, nil) }
	switch label {
	case "\\overline":
		node.setAttribute("notation", "top")
		node.classes = append(node.classes, "tml-overline")
	case "\\underline":
		node.setAttribute("notation", "bottom")
		node.classes = append(node.classes, "tml-underline")
	case "\\cancel":
		node.setAttribute("notation", "updiagonalstrike")
		node.children = append(node.children, push("tml-cancel", "upstrike"))
	case "\\bcancel":
		node.setAttribute("notation", "downdiagonalstrike")
		node.children = append(node.children, push("tml-cancel", "downstrike"))
	case "\\sout":
		node.setAttribute("notation", "horizontalstrike")
		node.children = append(node.children, push("tml-cancel", "sout"))
	case "\\xcancel":
		node.setAttribute("notation", "updiagonalstrike downdiagonalstrike")
		node.children = append(node.children, push("tml-cancel", "tml-xcancel"))
	case "\\longdiv":
		node.setAttribute("notation", "longdiv")
		node.classes = append(node.classes, "longdiv-top")
		node.children = append(node.children, push("longdiv-arc"))
	case "\\phase":
		node.setAttribute("notation", "phasorangle")
		node.classes = append(node.classes, "phasor-bottom")
		node.children = append(node.children, push("phasor-angle"))
	case "\\textcircled":
		node.setAttribute("notation", "circle")
		node.classes = append(node.classes, "circle-pad")
		node.children = append(node.children, push("textcircle"))
	case "\\angl":
		node.setAttribute("notation", "actuarial")
		node.classes = append(node.classes, "actuarial")
	case "\\boxed":
		node.style.set("padding", "3pt")
		node.style.set("border", "1px solid")
		node.setAttribute("scriptlevel", "0")
		node.setAttribute("displaystyle", "true")
	case "\\fbox":
		node.setAttribute("notation", "box")
		node.classes = append(node.classes, "tml-fbox")
	case "\\fcolorbox", "\\colorbox":
		node.style.set("padding", "0.3em")
		if label == "\\fcolorbox" {
			node.style.set("border", "0.0667em solid "+jsStr(group["borderColor"]))
		}
	}
	if tmTruthy(group["backgroundColor"]) {
		node.setAttribute("mathbackground", group["backgroundColor"])
	}
	return node
}

func tmEncloseColorHandler(numColors int) tmHandler {
	return func(ctx *tmCtx, args, opt []any) any {
		parser := ctx.parser
		model := ""
		if opt[0] != nil {
			model = tmStr(tmAssertNodeType(opt[0], "raw"), "string")
		}
		if numColors == 1 {
			color := ""
			if model != "" {
				color = tmColorFromSpec(model, tmStr(tmAssertNodeType(args[0], "raw"), "string"))
			} else {
				color = tmValidateColor(tmStr(tmAssertNodeType(args[0], "raw"), "string"), parser.gullet.macros, nil)
			}
			return tmObj{"type": "enclose", "mode": parser.mode, "label": ctx.funcName, "backgroundColor": color, "body": args[1]}
		}
		var borderColor, backgroundColor string
		if model != "" {
			borderSpec := tmStr(tmAssertNodeType(args[0], "raw"), "string")
			backgroundSpec := tmStr(tmAssertNodeType(args[0], "raw"), "string")
			borderColor = tmColorFromSpec(model, borderSpec)
			backgroundColor = tmColorFromSpec(model, backgroundSpec)
		} else {
			borderColor = tmValidateColor(tmStr(tmAssertNodeType(args[0], "raw"), "string"), parser.gullet.macros, nil)
			backgroundColor = tmValidateColor(tmStr(tmAssertNodeType(args[1], "raw"), "string"), parser.gullet.macros, nil)
		}
		return tmObj{"type": "enclose", "mode": parser.mode, "label": ctx.funcName, "backgroundColor": backgroundColor, "borderColor": borderColor, "body": args[2]}
	}
}

func tmEncloseHandler(ctx *tmCtx, args, opt []any) any {
	return tmObj{"type": "enclose", "mode": ctx.parser.mode, "label": ctx.funcName, "body": args[0]}
}

// ---- font.js -----------------------------------------------------------------------------------------------------

func tmIsLongVariableName(group tmObj, font string) bool {
	if font != "mathrm" && font != "mathit" {
		return false
	}
	body, ok := group["body"].(tmObj)
	if !ok || body == nil || tmStr(body, "type") != "ordgroup" || len(tmList(body["body"])) == 1 {
		return false
	}
	list := tmList(body["body"])
	if tmType(list[0]) != "mathord" {
		return false
	}
	for i := 1; i < len(list); i++ {
		t := tmType(list[i])
		if !(t == "mathord" || (t == "textord" && !tmIsNaNString(tmStr(list[i].(tmObj), "text")))) {
			return false
		}
	}
	return true
}

// tmIsNaNString is isNaN(string): Number(string) is NaN.
func tmIsNaNString(s string) bool {
	t := jsTrim(s)
	if t == "" {
		return false
	}
	_, ok := tmParseJSNumber(t)
	return !ok
}

func tmFontBuilder(group tmObj, style *tmStyle) mmNode {
	font := tmStr(group, "font")
	newStyle := style.withFont(font)
	mathGroup := tmBuildGroup(group["body"], newStyle)
	if len(mmChildren(mathGroup)) == 0 {
		return mathGroup
	}
	mg := mathGroup.(*mathNode)
	if font == "boldsymbol" && (mg.typ == "mo" || mg.typ == "mpadded" || mg.typ == "mrow") {
		mg.style.set("fontWeight", "bold")
		return mg
	}
	if tmIsLongVariableName(group, font) {
		var mi *mathNode
		if c00 := mg.children[0].(*mathNode).children[0]; c00.childrenPtr() != nil {
			mi = c00.(*mathNode)
		} else {
			mi = mg.children[0].(*mathNode)
		}
		mi.attrs.del("mathvariant")
		for i := 1; i < len(mg.children); i++ {
			c := mg.children[i].(*mathNode)
			var add string
			if first := c.children[0]; first.childrenPtr() != nil {
				add = (*first.childrenPtr())[0].(*textNode).text
			} else {
				add = first.(*textNode).text
			}
			t := mi.children[0].(*textNode)
			t.text += add
		}
		if font == "mathit" {
			t := mi.children[0].(*textNode)
			var b strings.Builder
			for _, u := range tmUTF16Units(t.text) {
				b.WriteString(tmVariantChar(tmU16(u), "italic"))
			}
			t.text = b.String()
			return mi
		}
		mpadded := mn("mpadded", mi)
		mpadded.setAttribute("lspace", "0")
		return mpadded
	}
	canConsolidate := mmType(mg.children[0]) == "mo"
	for i := 1; i < len(mg.children); i++ {
		c := mg.children[i]
		if mmType(c) == "mo" && font == "boldsymbol" {
			mmStyle(c).set("fontWeight", "bold")
		}
		if mmType(c) != "mi" {
			canConsolidate = false
		}
		localVariant := ""
		if a := c.attrsOf(); a != nil {
			if v, ok := a.get("mathvariant").(string); ok {
				localVariant = v
			}
		}
		if localVariant != "normal" {
			canConsolidate = false
		}
	}
	if !canConsolidate {
		return mg
	}
	mi := mg.children[0].(*mathNode)
	for i := 1; i < len(mg.children); i++ {
		mi.children = append(mi.children, mg.children[i].(*mathNode).children[0])
	}
	if mi.attrs.get("mathvariant") == "normal" {
		bogus := mn("mtext")
		return mn("mrow", bogus, mi)
	}
	return mi
}

var tmFontAliases = map[string]string{"\\Bbb": "\\mathbb", "\\bold": "\\mathbf", "\\frak": "\\mathfrak", "\\bm": "\\boldsymbol"}

// ---- genfrac.js --------------------------------------------------------------------------------------------------

var tmStylArray = []string{"display", "text", "script", "scriptscript"}
var tmScriptLevel = map[string]float64{"auto": -1, "display": 0, "text": 0, "script": 1, "scriptscript": 2}

func tmAdjustStyle(functionSize string, style *tmStyle) *tmStyle {
	switch functionSize {
	case "display":
		newSize := tmStyleDisplay
		if style.level() >= tmStyleScript {
			newSize = tmStyleText
		}
		return style.withLevel(newSize)
	case "text":
		if style.level() == tmStyleDisplay {
			return style.withLevel(tmStyleText)
		}
	case "auto":
		return style.incrementLevel()
	case "script":
		return style.withLevel(tmStyleScript)
	case "scriptscript":
		return style.withLevel(tmStyleScriptS)
	}
	return style
}

func tmGenfracBuilder(group tmObj, style *tmStyle) mmNode {
	scriptLevel, _ := group["scriptLevel"].(string)
	style = tmAdjustStyle(scriptLevel, style)
	numer := tmBuildGroup(group["numer"], style)
	denom := tmBuildGroup(group["denom"], style)
	if style.level() == 3 {
		mmStyle(numer).set("mathDepth", "2")
		tmSetAttr(numer, "scriptlevel", "2")
		mmStyle(denom).set("mathDepth", "2")
		tmSetAttr(denom, "scriptlevel", "2")
	}
	var node mmNode = mn("mfrac", numer, denom)
	if !tmTruthy(group["hasBarLine"]) {
		node.(*mathNode).setAttribute("linethickness", "0px")
	} else if tmTruthy(group["barSize"]) {
		ruleWidth := tmCalculateSize(tmSizeOf(group["barSize"]), style)
		node.(*mathNode).setAttribute("linethickness", ruleWidth.String())
	}
	if group["leftDelim"] != nil || group["rightDelim"] != nil {
		var withDelims []mmNode
		if ld, ok := group["leftDelim"].(string); ok {
			leftOp := mn("mo", &textNode{strings.Replace(ld, "\\", "", 1)})
			leftOp.setAttribute("fence", "true")
			withDelims = append(withDelims, leftOp)
		}
		withDelims = append(withDelims, node)
		if rd, ok := group["rightDelim"].(string); ok {
			rightOp := mn("mo", &textNode{strings.Replace(rd, "\\", "", 1)})
			rightOp.setAttribute("fence", "true")
			withDelims = append(withDelims, rightOp)
		}
		node = tmMakeRow(withDelims, false)
	}
	if scriptLevel != "auto" {
		node = mn("mstyle", node)
		node.(*mathNode).setAttribute("displaystyle", jsStr(scriptLevel == "display"))
		if lvl, ok := tmScriptLevel[scriptLevel]; ok {
			node.(*mathNode).setAttribute("scriptlevel", lvl)
		} else {
			node.(*mathNode).setAttribute("scriptlevel", nil)
		}
	}
	return node
}

func tmDelimFromValue(s string) any {
	if len(s) > 0 && s != "." {
		return s
	}
	return nil
}

func tmJSNumberOfNode(n tmObj) float64 {
	f, _ := tmParseJSNumber(jsTrim(tmStr(n, "text")))
	return f
}

func tmStylArrayAt(f float64) any {
	i := int(f)
	if float64(i) != f || i < 0 || i >= len(tmStylArray) {
		return nil
	}
	return tmStylArray[i]
}

func init() {
	// delimiter.js
	tmDefineFunction("delimsizing", []string{"\\bigl", "\\Bigl", "\\biggl", "\\Biggl", "\\bigr", "\\Bigr", "\\biggr", "\\Biggr", "\\bigm", "\\Bigm", "\\biggm",
		"\\Biggm", "\\big", "\\Big", "\\bigg", "\\Bigg"},
		tmProps{numArgs: 1, argTypes: []string{"primitive"}},
		func(ctx *tmCtx, args, opt []any) any {
			delim := tmCheckDelimiter(args[0], ctx.funcName)
			ds := tmDelimiterSizes[ctx.funcName]
			delimNode := tmObj{"type": "delimsizing", "mode": ctx.parser.mode, "size": float64(ds.size), "mclass": ds.mclass, "delim": delim["text"]}
			nextToken := ctx.parser.fetch().text
			if nextToken != "^" && nextToken != "_" {
				return delimNode
			}
			return tmObj{"type": "ordgroup", "mode": "math", "body": []any{delimNode, tmObj{"type": "ordgroup", "mode": "math", "body": []any{}}}}
		},
		func(group tmObj, style *tmStyle) mmNode {
			delim := tmStr(group, "delim")
			if delim == "." {
				delim = ""
			}
			node := mn("mo", tmMakeText(delim, tmStr(group, "mode"), nil))
			if m := tmStr(group, "mclass"); m == "mopen" || m == "mclose" {
				node.setAttribute("fence", "true")
			} else {
				node.setAttribute("fence", "false")
			}
			if tmNeedExplicitStretch[delim] || strings.Contains(delim, "arrow") {
				node.setAttribute("stretchy", "true")
			}
			node.setAttribute("symmetric", "true")
			size := int(group["size"].(float64))
			node.setAttribute("minsize", tmSizeToMaxHeight[size]+"em")
			node.setAttribute("maxsize", tmSizeToMaxHeight[size]+"em")
			return node
		})
	tmDefineFunction("leftright-right", []string{"\\right"}, tmProps{numArgs: 1, argTypes: []string{"primitive"}},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "leftright-right", "mode": ctx.parser.mode, "delim": tmCheckDelimiter(args[0], ctx.funcName)["text"]}
		}, nil)
	tmDefineFunction("leftright", []string{"\\left"}, tmProps{numArgs: 1, argTypes: []string{"primitive"}},
		func(ctx *tmCtx, args, opt []any) any {
			delim := tmCheckDelimiter(args[0], ctx.funcName)
			parser := ctx.parser
			parser.leftrightDepth++
			body := parser.parseExpression(false, "\\right", true)
			nextToken := parser.fetch()
			for nextToken.text == "\\middle" {
				parser.consume()
				middle := parser.fetch().text
				if tmSymbolsMath()[middle] == nil {
					tmThrowf("Invalid delimiter '" + middle + "' after '\\middle'")
				}
				tmCheckDelimiter(tmObj{"type": "atom", "mode": "math", "text": middle}, "\\middle")
				body = append(body, tmObj{"type": "middle", "mode": "math", "delim": middle})
				parser.consume()
				body = append(body, parser.parseExpression(false, "\\right", true)...)
				nextToken = parser.fetch()
			}
			parser.leftrightDepth--
			parser.expect("\\right", false)
			right := tmAssertNodeType(nilNode(parser.parseFunction("", "")), "leftright-right")
			return tmObj{"type": "leftright", "mode": parser.mode, "body": body, "left": delim["text"], "right": right["delim"], "isStretchy": true}
		}, tmFenceBuilder(true))
	names := append([]string{}, tmLeftToRightKeys...)
	tmDefineFunction("delimiter", names, tmProps{numArgs: 0, allowedInText: true, allowedInArgument: true},
		func(ctx *tmCtx, args, opt []any) any {
			parser := ctx.parser
			if parser.mode == "text" {
				return tmObj{"type": "textord", "mode": "text", "text": ctx.funcName, "loc": ctx.token.loc}
			} else if !parser.settings.wrapDelimiterPairs {
				return tmObj{"type": "atom", "mode": "math", "family": "open", "loc": ctx.token.loc, "text": ctx.funcName}
			}
			rightDelim := tmLeftToRight[ctx.funcName]
			body := parser.parseExpression(false, rightDelim, false)
			nextToken := parser.fetch().text
			if nextToken != rightDelim {
				tmThrowf("Unmatched delimiter")
			}
			parser.consume()
			return tmObj{"type": "delimiter", "mode": parser.mode, "body": body, "left": ctx.funcName, "right": rightDelim}
		}, tmFenceBuilder(false))
	tmDefineFunction("middle", []string{"\\middle"}, tmProps{numArgs: 1, argTypes: []string{"primitive"}},
		func(ctx *tmCtx, args, opt []any) any {
			delim := tmCheckDelimiter(args[0], ctx.funcName)
			if ctx.parser.leftrightDepth == 0 {
				tmThrow("\\middle without preceding \\left", delim)
			}
			return tmObj{"type": "middle", "mode": ctx.parser.mode, "delim": delim["text"]}
		},
		func(group tmObj, style *tmStyle) mmNode {
			textNode := tmMakeText(tmStr(group, "delim"), tmStr(group, "mode"), nil)
			middleNode := mn("mo", textNode)
			middleNode.setAttribute("stretchy", "true")
			middleNode.setAttribute("form", "infix")
			if textNode.text != "/" {
				middleNode.setAttribute("lspace", "0.05em")
				middleNode.setAttribute("rspace", "0.05em")
			}
			return middleNode
		})

	// enclose.js
	tmDefineFunction("enclose", []string{"\\colorbox"}, tmProps{numArgs: 2, numOptionalArgs: 1, allowedInText: true, argTypes: []string{"raw", "raw", "text"}},
		tmEncloseColorHandler(1), tmEncloseBuilder)
	tmDefineFunction("enclose", []string{"\\fcolorbox"}, tmProps{numArgs: 3, numOptionalArgs: 1, allowedInText: true, argTypes: []string{"raw", "raw", "raw", "text"}},
		tmEncloseColorHandler(2), tmEncloseBuilder)
	tmDefineFunction("enclose", []string{"\\fbox"}, tmProps{numArgs: 1, argTypes: []string{"hbox"}, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "enclose", "mode": ctx.parser.mode, "label": "\\fbox", "body": args[0]}
		}, tmEncloseBuilder)
	tmDefineFunction("enclose", []string{"\\angl", "\\cancel", "\\bcancel", "\\xcancel", "\\overline", "\\boxed", "\\longdiv", "\\phase"}, tmProps{numArgs: 1},
		tmEncloseHandler, tmEncloseBuilder)
	tmDefineFunction("enclose", []string{"\\sout"}, tmProps{numArgs: 1, allowedInText: true}, tmEncloseHandler, tmEncloseBuilder)
	tmDefineFunction("enclose", []string{"\\underline"}, tmProps{numArgs: 1, allowedInText: true}, tmEncloseHandler, tmEncloseBuilder)
	tmDefineFunction("enclose", []string{"\\textcircled"}, tmProps{numArgs: 1, argTypes: []string{"text"}, allowedInArgument: true, allowedInText: true},
		tmEncloseHandler, tmEncloseBuilder)

	// environment.js
	tmDefineFunction("environment", []string{"\\begin", "\\end"}, tmProps{numArgs: 1, argTypes: []string{"text"}},
		func(ctx *tmCtx, args, opt []any) any {
			parser := ctx.parser
			nameGroup := args[0].(tmObj)
			if tmStr(nameGroup, "type") != "ordgroup" {
				tmThrow("Invalid environment name", nameGroup)
			}
			envName := ""
			for _, n := range tmList(nameGroup["body"]) {
				envName += tmStr(tmAssertNodeType(n, "textord"), "text")
			}
			if ctx.funcName == "\\begin" {
				env, ok := tmEnvironments[envName]
				if !ok {
					tmThrow("No such environment: "+envName, nameGroup)
				}
				eargs, eopt := parser.parseArguments("\\begin{"+envName+"}", env)
				context := &tmCtx{mode: parser.mode, envName: envName, parser: parser}
				result := env.handler(context, eargs, eopt)
				parser.expect("\\end", false)
				endNameToken := parser.nextToken
				end := tmAssertNodeType(nilNode(parser.parseFunction("", "")), "environment")
				if tmStr(end, "name") != envName {
					tmThrow("Mismatch: \\begin{"+envName+"} matched by \\end{"+tmStr(end, "name")+"}", endNameToken)
				}
				return result
			}
			return tmObj{"type": "environment", "mode": parser.mode, "name": envName, "nameGroup": nameGroup}
		}, nil)

	// envTag.js
	tmDefineFunction("envTag", []string{"\\env@tag"}, tmProps{numArgs: 1, argTypes: []string{"math"}},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "envTag", "mode": ctx.parser.mode, "body": args[0]}
		}, func(group tmObj, style *tmStyle) mmNode { return mn("mrow") })
	tmDefineFunction("noTag", []string{"\\env@notag"}, tmProps{numArgs: 0},
		func(ctx *tmCtx, args, opt []any) any { return tmObj{"type": "noTag", "mode": ctx.parser.mode} },
		func(group tmObj, style *tmStyle) mmNode { return mn("mrow") })

	// font.js
	tmDefineFunction("font", []string{"\\mathrm", "\\mathit", "\\mathbf", "\\mathnormal", "\\up@greek", "\\boldsymbol", "\\mathbb", "\\mathcal", "\\mathfrak",
		"\\mathscr", "\\mathsf", "\\mathsfit", "\\mathtt", "\\Bbb", "\\bm", "\\bold", "\\frak"}, tmProps{numArgs: 1, allowedInArgument: true},
		func(ctx *tmCtx, args, opt []any) any {
			body := tmNormalizeArgument(args[0])
			fn := ctx.funcName
			if a, ok := tmFontAliases[fn]; ok {
				fn = a
			}
			return tmObj{"type": "font", "mode": ctx.parser.mode, "font": fn[1:], "body": body}
		}, tmFontBuilder)
	tmDefineFunction("font", []string{"\\rm", "\\sf", "\\tt", "\\bf", "\\it", "\\cal"}, tmProps{numArgs: 0, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			parser := ctx.parser
			mode := parser.mode
			body := parser.parseExpression(true, ctx.breakOnTokenText, true)
			return tmObj{"type": "font", "mode": mode, "font": "math" + ctx.funcName[1:], "body": tmObj{"type": "ordgroup", "mode": parser.mode, "body": body}}
		}, tmFontBuilder)

	// genfrac.js
	tmDefineFunction("genfrac", []string{"\\cfrac", "\\dfrac", "\\frac", "\\tfrac", "\\dbinom", "\\binom", "\\tbinom", "\\\\atopfrac", "\\\\bracefrac", "\\\\brackfrac"},
		tmProps{numArgs: 2, allowedInArgument: true},
		func(ctx *tmCtx, args, opt []any) any {
			hasBarLine := false
			var leftDelim, rightDelim any
			scriptLevel := "auto"
			switch ctx.funcName {
			case "\\cfrac", "\\dfrac", "\\frac", "\\tfrac":
				hasBarLine = true
			case "\\\\atopfrac":
			case "\\dbinom", "\\binom", "\\tbinom":
				leftDelim, rightDelim = "(", ")"
			case "\\\\bracefrac":
				leftDelim, rightDelim = "\\{", "\\}"
			case "\\\\brackfrac":
				leftDelim, rightDelim = "[", "]"
			default:
				panic(&tmJSError{"Unrecognized genfrac command"})
			}
			if ctx.funcName == "\\cfrac" || strings.HasPrefix(ctx.funcName, "\\d") {
				scriptLevel = "display"
			} else if strings.HasPrefix(ctx.funcName, "\\t") {
				scriptLevel = "text"
			}
			return tmObj{"type": "genfrac", "mode": ctx.parser.mode, "continued": false, "numer": args[0], "denom": args[1], "hasBarLine": hasBarLine,
				"leftDelim": leftDelim, "rightDelim": rightDelim, "scriptLevel": scriptLevel, "barSize": nil}
		}, tmGenfracBuilder)
	tmDefineFunction("infix", []string{"\\over", "\\choose", "\\atop", "\\brace", "\\brack"}, tmProps{numArgs: 0, infix: true},
		func(ctx *tmCtx, args, opt []any) any {
			var replaceWith string
			switch ctx.funcName {
			case "\\over":
				replaceWith = "\\frac"
			case "\\choose":
				replaceWith = "\\binom"
			case "\\atop":
				replaceWith = "\\\\atopfrac"
			case "\\brace":
				replaceWith = "\\\\bracefrac"
			case "\\brack":
				replaceWith = "\\\\brackfrac"
			default:
				panic(&tmJSError{"Unrecognized infix genfrac command"})
			}
			return tmObj{"type": "infix", "mode": ctx.parser.mode, "replaceWith": replaceWith, "token": ctx.token}
		}, nil)
	tmDefineFunction("genfrac", []string{"\\genfrac"}, tmProps{numArgs: 6, allowedInArgument: true, argTypes: []string{"math", "math", "size", "text", "math", "math"}},
		func(ctx *tmCtx, args, opt []any) any {
			numer, denom := args[4], args[5]
			leftNode := tmNormalizeArgument(args[0]).(tmObj)
			var leftDelim any
			if tmStr(leftNode, "type") == "atom" && tmStr(leftNode, "family") == "open" {
				leftDelim = tmDelimFromValue(tmStr(leftNode, "text"))
			}
			rightNode := tmNormalizeArgument(args[1]).(tmObj)
			var rightDelim any
			if tmStr(rightNode, "type") == "atom" && tmStr(rightNode, "family") == "close" {
				rightDelim = tmDelimFromValue(tmStr(rightNode, "text"))
			}
			barNode := tmAssertNodeType(args[2], "size")
			var hasBarLine bool
			var barSize any
			if tmTruthy(barNode["isBlank"]) {
				hasBarLine = true
			} else {
				barSize = barNode["value"]
				hasBarLine = tmSizeOf(barSize).number > 0
			}
			scriptLevel := any("auto")
			styl := args[3].(tmObj)
			if tmStr(styl, "type") == "ordgroup" {
				if body := tmList(styl["body"]); len(body) > 0 {
					textOrd := tmAssertNodeType(body[0], "textord")
					scriptLevel = tmStylArrayAt(tmJSNumberOfNode(textOrd))
				}
			} else {
				styl = tmAssertNodeType(styl, "textord")
				scriptLevel = tmStylArrayAt(tmJSNumberOfNode(styl))
			}
			return tmObj{"type": "genfrac", "mode": ctx.parser.mode, "numer": numer, "denom": denom, "continued": false, "hasBarLine": hasBarLine,
				"barSize": barSize, "leftDelim": leftDelim, "rightDelim": rightDelim, "scriptLevel": scriptLevel}
		}, tmGenfracBuilder)
	tmDefineFunction("infix", []string{"\\above"}, tmProps{numArgs: 1, argTypes: []string{"size"}, infix: true},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "infix", "mode": ctx.parser.mode, "replaceWith": "\\\\abovefrac", "barSize": tmAssertNodeType(args[0], "size")["value"], "token": ctx.token}
		}, nil)
	tmDefineFunction("genfrac", []string{"\\\\abovefrac"}, tmProps{numArgs: 3, argTypes: []string{"math", "size", "math"}},
		func(ctx *tmCtx, args, opt []any) any {
			barSize := tmAssertNodeType(args[1], "infix")["barSize"]
			if !tmTruthy(barSize) {
				panic(&tmJSError{"Expected non-null, but got " + jsStr(barSize)})
			}
			return tmObj{"type": "genfrac", "mode": ctx.parser.mode, "numer": args[0], "denom": args[2], "continued": false,
				"hasBarLine": tmSizeOf(barSize).number > 0, "barSize": barSize, "leftDelim": nil, "rightDelim": nil, "scriptLevel": "auto"}
		}, tmGenfracBuilder)

	// hbox.js
	tmDefineFunction("hbox", []string{"\\hbox"}, tmProps{numArgs: 1, argTypes: []string{"hbox"}, allowedInArgument: true},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "hbox", "mode": ctx.parser.mode, "body": tmOrdArgument(args[0])}
		},
		func(group tmObj, style *tmStyle) mmNode {
			newStyle := style.withLevel(tmStyleText)
			mrow := tmBuildExpressionRow(tmList(group["body"]), newStyle, false)
			return tmConsolidateText(mrow)
		})

	// horizBracket.js
	tmDefineFunction("horizBracket", []string{"\\overbrace", "\\underbrace", "\\overbracket", "\\underbracket"}, tmProps{numArgs: 1},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "horizBracket", "mode": ctx.parser.mode, "label": ctx.funcName, "isOver": strings.HasPrefix(ctx.funcName, "\\over"), "base": args[0]}
		},
		func(group tmObj, style *tmStyle) mmNode {
			accentNode := tmStretchyNode(tmStr(group, "label"))
			accentNode.style.set("math-depth", 0.0)
			typ := "munder"
			if tmTruthy(group["isOver"]) {
				typ = "mover"
			}
			return mn(typ, tmBuildGroup(group["base"], style), accentNode)
		})

	// html.js (every command here needs trust, which Temml's defaults deny)
	tmDefineFunction("html", []string{"\\class", "\\id", "\\style", "\\data"}, tmProps{numArgs: 2, argTypes: []string{"raw", "original"}, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			value := tmStr(tmAssertNodeType(args[0], "raw"), "string")
			if ctx.parser.settings.strict {
				tmThrow("Function \""+ctx.funcName+"\" is disabled in strict mode", ctx.token)
			}
			if ctx.funcName == "\\data" {
				for _, kv := range strings.Split(value, ",") {
					if len(strings.Split(kv, "=")) != 2 {
						tmThrowf("Error parsing key-value for \\data")
					}
				}
			}
			tmThrow("Function \""+ctx.funcName+"\" is not trusted", ctx.token)
			return nil
		}, nil)

	// includegraphics.js (needs trust as well)
	tmDefineFunction("includegraphics", []string{"\\includegraphics"}, tmProps{numArgs: 1, numOptionalArgs: 1, argTypes: []string{"raw", "url"}},
		tmIncludegraphicsHandler, nil)

	// kern.js
	tmDefineFunction("kern", []string{"\\kern", "\\mkern", "\\hskip", "\\mskip"}, tmProps{numArgs: 1, argTypes: []string{"size"}, primitive: true, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			size := tmAssertNodeType(args[0], "size")
			if ctx.parser.settings.strict {
				mathFunction := ctx.funcName[1] == 'm'
				muUnit := tmSizeOf(size["value"]).unit == "mu"
				if mathFunction {
					if !muUnit {
						tmThrow("LaTeX's "+ctx.funcName+" supports only mu units, not "+tmSizeOf(size["value"]).unit+" units", ctx.token)
					}
					if ctx.parser.mode != "math" {
						tmThrow("LaTeX's "+ctx.funcName+" works only in math mode", ctx.token)
					}
				} else if muUnit {
					tmThrow("LaTeX's "+ctx.funcName+" doesn't support mu units", ctx.token)
				}
			}
			return tmObj{"type": "kern", "mode": ctx.parser.mode, "dimension": size["value"]}
		},
		func(group tmObj, style *tmStyle) mmNode {
			dimension := tmCalculateSize(tmSizeOf(group["dimension"]), style)
			ch := ""
			if dimension.number > 0 && dimension.unit == "em" {
				ch = tmSpaceCharacter(dimension.number)
			}
			if tmStr(group, "mode") == "text" && len(ch) > 0 {
				return mn("mtext", &textNode{ch})
			}
			if dimension.number >= 0 {
				node := mn("mspace")
				node.setAttribute("width", dimension.String())
				return node
			}
			node := mn("mrow")
			node.style.set("marginLeft", dimension.String())
			return node
		})

	// label.js
	tmDefineFunction("label", []string{"\\label"}, tmProps{numArgs: 1, argTypes: []string{"raw"}},
		func(ctx *tmCtx, args, opt []any) any {
			s := tmStr(args[0].(tmObj), "string")
			return tmObj{"type": "label", "mode": ctx.parser.mode, "string": tmInvalidIDRe.ReplaceAllString(s, "")}
		},
		func(group tmObj, style *tmStyle) mmNode {
			node := newMathNode("mrow", nil, []string{"tml-label"}, nil)
			if s := tmStr(group, "string"); len(s) > 0 {
				node.label = s
			}
			return node
		})

	// lap.js
	tmDefineFunction("lap", []string{"\\mathllap", "\\mathrlap", "\\mathclap", "\\clap", "\\llap", "\\rlap"}, tmProps{numArgs: 1, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			fn := ctx.funcName
			if fn == "\\clap" || fn == "\\llap" || fn == "\\rlap" {
				if ctx.parser.settings.strict && ctx.parser.mode != "text" {
					tmThrow("{"+fn+"} can be used only in text mode.\n Try \\math"+fn[1:], ctx.token)
				}
				fn = fn[1:]
			} else {
				fn = fn[5:]
			}
			return tmObj{"type": "lap", "mode": ctx.parser.mode, "alignment": fn, "body": args[0]}
		},
		tmLapBuilder)

	// math.js
	tmDefineFunction("ordgroup", []string{"\\(", "$"}, tmProps{numArgs: 0, allowedInText: true, allowedInMath: &tmFalse},
		func(ctx *tmCtx, args, opt []any) any {
			parser := ctx.parser
			outerMode := parser.mode
			parser.switchMode("math")
			closeTok := "$"
			if ctx.funcName == "\\(" {
				closeTok = "\\)"
			}
			body := parser.parseExpression(false, closeTok, false)
			parser.expect(closeTok, true)
			parser.switchMode(outerMode)
			return tmObj{"type": "ordgroup", "mode": parser.mode, "body": body}
		}, nil)
	tmDefineFunction("text", []string{"\\)", "\\]"}, tmProps{numArgs: 0, allowedInText: true, allowedInMath: &tmFalse},
		func(ctx *tmCtx, args, opt []any) any {
			tmThrow("Mismatched "+ctx.funcName, nil)
			return nil
		}, nil)

	// mathchoice.js
	tmDefineFunction("mathchoice", []string{"\\mathchoice"}, tmProps{numArgs: 4, primitive: true},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "mathchoice", "mode": ctx.parser.mode, "display": tmOrdArgument(args[0]), "text": tmOrdArgument(args[1]),
				"script": tmOrdArgument(args[2]), "scriptscript": tmOrdArgument(args[3])}
		},
		func(group tmObj, style *tmStyle) mmNode {
			var body any
			switch style.level() {
			case tmStyleDisplay:
				body = group["display"]
			case tmStyleText:
				body = group["text"]
			case tmStyleScript:
				body = group["script"]
			case tmStyleScriptS:
				body = group["scriptscript"]
			default:
				body = group["text"]
			}
			return tmBuildExpressionRow(tmList(body), style, false)
		})
}

var tmInvalidIDRe = mustRE(`[^A-Za-z_0-9-]`)

func tmSpaceCharacter(width float64) string {
	switch {
	case width >= 0.05555 && width <= 0.05556:
		return "\u200a"
	case width >= 0.1666 && width <= 0.1667:
		return "\u2009"
	case width >= 0.2222 && width <= 0.2223:
		return "\u2005"
	case width >= 0.2777 && width <= 0.2778:
		return "\u2005\u200a"
	}
	return ""
}

func tmLapBuilder(group tmObj, style *tmStyle) mmNode {
	alignment := tmStr(group, "alignment")
	var strut *mathNode
	if alignment == "llap" {
		phantomInner := tmBuildExpression(tmOrdArgument(group["body"]), style, false)
		phantom := newMathNode("mphantom", phantomInner, nil, nil)
		strut = mn("mpadded", phantom)
		strut.setAttribute("width", "0.1px")
	}
	inner := tmBuildGroup(group["body"], style)
	var node *mathNode
	if alignment == "llap" {
		st := mmStyle(inner)
		st.set("position", "absolute")
		st.set("right", "0")
		st.set("bottom", "0")
		node = mn("mpadded", strut, inner)
	} else {
		node = mn("mpadded", inner)
	}
	if alignment == "rlap" {
		body := group["body"].(tmObj)
		if list := tmList(body["body"]); len(list) > 0 && tmType(list[0]) == "genfrac" {
			node.setAttribute("lspace", "0.16667em")
		}
	} else {
		offset := "-0.5"
		if alignment == "llap" {
			offset = "-1"
		}
		node.setAttribute("lspace", offset+"width")
		if alignment == "llap" {
			node.style.set("position", "relative")
		} else {
			node.style.set("display", "flex")
			node.style.set("justifyContent", "center")
		}
	}
	node.setAttribute("width", "0.1px")
	return node
}

// tmIncludegraphicsHandler runs the handler of includegraphics.js up to the trust check that always fails.
func tmIncludegraphicsHandler(ctx *tmCtx, args, opt []any) any {
	sizeData := func(str string) {
		if tmPlainNumberRe.MatchString(str) {
			return
		}
		m := tmSizeRe.FindStringSubmatch(str)
		if m == nil {
			tmThrowf("Invalid size: '" + str + "' in \\includegraphics")
		}
		if !tmValidUnit(m[3]) {
			tmThrowf("Invalid unit: '" + m[3] + "' in \\includegraphics.")
		}
	}
	if opt[0] != nil {
		attributeStr := tmStr(tmAssertNodeType(opt[0], "raw"), "string")
		for _, a := range strings.Split(attributeStr, ",") {
			kv := strings.Split(a, "=")
			if len(kv) == 2 {
				str := jsTrim(kv[1])
				switch jsTrim(kv[0]) {
				case "alt":
				case "width", "height", "totalheight":
					sizeData(str)
				default:
					tmThrowf("Invalid key: '" + kv[0] + "' in \\includegraphics.")
				}
			}
		}
	}
	tmAssertNodeType(args[0], "url")
	tmThrow("Function \"\\includegraphics\" is not trusted", ctx.token)
	return nil
}

var tmPlainNumberRe = mustRE(`^[-+]? *(\d+(\.\d*)?|\.\d+)$`)
