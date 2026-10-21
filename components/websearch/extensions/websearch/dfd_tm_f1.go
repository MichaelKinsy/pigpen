package websearch

import (
	"math"
	"strconv"
	"strings"
)

// Temml 0.13.4 (MIT): src/functions/accent.js, accentunder.js, arrow.js, bordermatrix.js, cancelto.js, char.js,
// color.js, cr.js, def.js.

func tmPadding(width any) *mathNode {
	node := mn("mspace")
	node.setAttribute("width", jsStr(width)+"em")
	return node
}

const (
	tmSmallNudge  = "DHKLUcegorsuvxyzΠΥΨαδηιμνοτυχϵ"
	tmMediumNudge = "BCEGIMNOPQRSTXZlpqtwΓΘΞΣΦΩβεζθξρςφψϑϕϱ"
	tmLargeNudge  = "AFJdfΔΛ"
	tmSmalls      = "acegıȷmnopqrsuvwxyzαγεηικμνοπρςστυχωϕ𝐚𝐜𝐞𝐠𝐦𝐧𝐨𝐩𝐪𝐫𝐬𝐮𝐯𝐰𝐱𝐲𝐳"
)

var tmNonStretchyAccents = map[string]bool{
	"\\acute": true, "\\check": true, "\\grave": true, "\\ddot": true, "\\dddot": true, "\\ddddot": true, "\\tilde": true, "\\bar": true,
	"\\breve": true, "\\hat": true, "\\vec": true, "\\dot": true, "\\mathring": true,
}

var tmNeedsWebkitVerticalShift = map[string]bool{
	"\\acute": true, "\\bar": true, "\\breve": true, "\\check": true, "\\dot": true, "\\ddot": true, "\\grave": true, "\\hat": true,
	"\\mathring": true, "\\`": true, "\\'": true, "\\^": true, "\\=": true, "\\u": true, "\\.": true, "\\\"": true, "\\r": true, "\\H": true, "\\v": true,
}

var tmCombiningChar = map[string]string{
	"\\`": "\u0300", "\\'": "\u0301", "\\^": "\u0302", "\\~": "\u0303", "\\=": "\u0304", "\\u": "\u0306", "\\.": "\u0307", "\\\"": "\u0308",
	"\\r": "\u030A", "\\H": "\u030B", "\\v": "\u030C", "\\c": "\u0327",
}

func tmAccentBuilder(group tmObj, style *tmStyle) mmNode {
	label := tmStr(group, "label")
	mode := tmStr(group, "mode")
	var accentNode *mathNode
	if tmTruthy(group["isStretchy"]) {
		accentNode = tmAccentNode(group)
	} else {
		accentNode = mn("mo", tmMakeText(label, mode, nil))
	}
	if !tmTruthy(group["isStretchy"]) {
		accentNode.setAttribute("stretchy", "false")
	}
	if label != "\\vec" {
		accentNode.style.set("mathDepth", "0")
	}
	tag := "mover"
	if label == "\\c" {
		tag = "munder"
	}
	needsWbkVertShift := tmNeedsWebkitVerticalShift[label]
	base, _ := group["base"].(tmObj)
	baseText := ""
	if base != nil {
		baseText, _ = base["text"].(string)
	}
	if tag == "mover" && mode == "math" && !tmTruthy(group["isStretchy"]) && baseText != "" && jsLen(baseText) == 1 {
		text := baseText
		isVec := label == "\\vec"
		if isVec {
			accentNode.classes = append(accentNode.classes, "tml-vec")
		}
		wbkPostfix := ""
		if isVec {
			wbkPostfix = "-vec"
		} else if needsWbkVertShift {
			wbkPostfix = "-acc"
		}
		switch {
		case strings.Contains(tmSmallNudge, text):
			accentNode.classes = append(accentNode.classes, "chr-sml", "wbk-sml"+wbkPostfix)
		case strings.Contains(tmMediumNudge, text):
			accentNode.classes = append(accentNode.classes, "chr-med", "wbk-med"+wbkPostfix)
		case strings.Contains(tmLargeNudge, text):
			accentNode.classes = append(accentNode.classes, "chr-lrg", "wbk-lrg"+wbkPostfix)
		case isVec:
			accentNode.classes = append(accentNode.classes, "wbk-vec")
		case needsWbkVertShift:
			accentNode.classes = append(accentNode.classes, "wbk-acc")
		}
	} else if needsWbkVertShift {
		accentNode.classes = append(accentNode.classes, "wbk-acc")
	}
	return mn(tag, tmBuildGroup(group["base"], style), accentNode)
}

// ---- arrow.js ----------------------------------------------------------------------------------------------------

// tmPaddedNode is paddedNode(group, lspace, rspace, mustSmash) with the string widths arrow.js passes; rspaceZero is
// the number 0 the callers pass for an empty label.
func tmPaddedNode(group mmNode, lspace string, rspace string, rspaceZero bool, mustSmash bool) mmNode {
	if group == nil && rspaceZero {
		return tmPadding(lspace)
	}
	var row []mmNode
	if group != nil {
		row = []mmNode{group}
	}
	row = append([]mmNode{tmPadding(lspace)}, row...)
	rs := 0.0
	if !rspaceZero {
		rs, _ = strconv.ParseFloat(rspace, 64)
	}
	if rs > 0 {
		row = append(row, tmPadding(rspace))
	}
	if mustSmash {
		mpadded := newMathNode("mpadded", row, nil, nil)
		mpadded.setAttribute("height", "0.1px")
		return mpadded
	}
	return newMathNode("mrow", row, nil, nil)
}

func tmLabelSize(size float64, scriptLevel int) float64 { return size / tmEmScale(scriptLevel) }

func tmMunderoverNode(fName string, body, below any, style *tmStyle) *mathNode {
	arrowNode := tmStretchyNode(fName)
	isEq := len(fName) >= 3 && fName[1:3] == "eq"
	var minWidth string
	switch {
	case len(fName) > 1 && fName[1] == 'x':
		minWidth = "1.75"
	case len(fName) >= 4 && fName[2:4] == "cd":
		minWidth = "3.0"
	case isEq:
		minWidth = "1.0"
	default:
		minWidth = "2.0"
	}
	arrowNode.setAttribute("lspace", "0")
	if isEq {
		arrowNode.setAttribute("rspace", "0.5em")
	} else {
		arrowNode.setAttribute("rspace", "0")
	}
	lvl := 3
	if style.level() < 2 {
		lvl = 2
	}
	labelStyle := style.withLevel(lvl)
	mw, _ := strconv.ParseFloat(minWidth, 64)
	minArrowWidth := tmLabelSize(mw, labelStyle.level())
	dummyWidth := tmLabelSize(mw, 3)
	emptyLabel := tmPaddedNode(nil, tmToFixed(minArrowWidth, 4), "", true, false)
	dummyNode := tmPaddedNode(nil, tmToFixed(dummyWidth, 4), "", true, false)
	sp := 0.3
	if isEq {
		sp = 0
	}
	space := tmToFixed(tmLabelSize(sp, labelStyle.level()), 4)
	var upperNode, lowerNode mmNode
	hasBody := func(n any) bool {
		node, ok := n.(tmObj)
		if !ok || node == nil {
			return false
		}
		bb := node["body"]
		if !tmTruthy(bb) {
			return false
		}
		switch x := bb.(type) {
		case tmObj:
			return tmTruthy(x["body"])
		case []any:
			return len(x) > 0
		}
		return false
	}
	gotUpper := hasBody(body)
	if gotUpper {
		label := tmBuildGroup(body, labelStyle)
		mustSmash := fName == "\\\\cdrightarrow" || fName == "\\\\cdleftarrow"
		label = tmPaddedNode(label, space, space, false, mustSmash)
		upperNode = mn("mover", label, dummyNode)
	}
	gotLower := hasBody(below)
	if gotLower {
		label := tmBuildGroup(below, labelStyle)
		label = tmPaddedNode(label, space, space, false, false)
		lowerNode = mn("munder", label, dummyNode)
	}
	var node *mathNode
	switch {
	case !gotUpper && !gotLower:
		node = mn("mover", arrowNode, emptyLabel)
	case gotUpper && gotLower:
		node = mn("munderover", arrowNode, lowerNode, upperNode)
	case gotUpper:
		node = mn("mover", arrowNode, upperNode)
	default:
		node = mn("munder", arrowNode, lowerNode)
	}
	if minWidth == "3.0" {
		node.style.set("height", "1em")
	}
	node.setAttribute("accent", "false")
	return node
}

var tmArrowComponent = map[string][2]string{
	"\\equilibriumRight": {"\\longrightharpoonup", "\\eqleftharpoondown"},
	"\\equilibriumLeft":  {"\\eqrightharpoonup", "\\longleftharpoondown"},
}

// ---- color.js ----------------------------------------------------------------------------------------------------

var (
	tmHtmlRe       = mustRE(`(?i)^(#[a-f0-9]{3}|#?[a-f0-9]{6})$`)
	tmHtmlOrNameRe = mustRE(`(?i)^(#[a-f0-9]{3}|#?[a-f0-9]{6}|[a-z]+)$`)
	tmRGBRe        = mustRE(`^ *\d{1,3} *(?:, *\d{1,3} *){2}$`)
	tmRgbRe        = mustRE(`^ *[10](?:\.\d*)? *(?:, *[10](?:\.\d*)? *){2}$`)
	tmXColorHtmlRe = mustRE(`(?i)^[a-f0-9]{6}$`)
)

func tmToHex(num float64) string {
	s := strconv.FormatInt(int64(num), 16)
	if len(s) == 1 {
		s = "0" + s
	}
	return s
}

func tmColorFromSpec(model, spec string) string {
	color := ""
	switch model {
	case "HTML":
		if !tmHtmlRe.MatchString(spec) {
			tmThrowf("Invalid HTML input.")
		}
		color = spec
	case "RGB":
		if !tmRGBRe.MatchString(spec) {
			tmThrowf("Invalid RGB input.")
		}
		for _, e := range strings.Split(spec, ",") {
			n, _ := strconv.ParseFloat(jsTrim(e), 64)
			color += tmToHex(n)
		}
	default:
		if !tmRgbRe.MatchString(spec) {
			tmThrowf("Invalid rbg input.")
		}
		for _, e := range strings.Split(spec, ",") {
			num, _ := strconv.ParseFloat(jsTrim(e), 64)
			if num > 1 {
				tmThrowf("Color rgb input must be < 1.")
			}
			n, _ := strconv.ParseFloat(tmToFixed(num*255, 0), 64)
			color += tmToHex(n)
		}
	}
	if !strings.HasPrefix(color, "#") {
		color = "#" + color
	}
	return color
}

func tmValidateColor(color string, macros *tmNamespace, token *tmToken) string {
	macroName := "\\\\color@" + color
	if !tmHtmlOrNameRe.MatchString(color) {
		tmThrow("Invalid color: '"+color+"'", token)
	}
	if tmXColorHtmlRe.MatchString(color) {
		return "#" + color
	} else if strings.HasPrefix(color, "#") {
		return color
	} else if macros.has(macroName) {
		def := macros.get(macroName).(*tmMacroDef)
		return def.tokens[0].text
	} else if c, ok := tmXColors()[color]; ok && c != "" {
		return c
	}
	return color
}

func tmColorBuilder(group tmObj, style *tmStyle) mmNode {
	color := group["color"]
	expr := tmBuildExpression(tmList(group["body"]), style.withColor(color), false)
	if len(expr) == 0 {
		expr = append(expr, mn("mrow"))
	}
	for _, e := range expr {
		mmStyle(e).set("color", color)
	}
	return newDocFragment(expr)
}

func tmColorHandler(isTextColor bool) tmHandler {
	return func(ctx *tmCtx, args []any, opt []any) any {
		parser := ctx.parser
		model := ""
		if opt[0] != nil {
			model = tmStr(tmAssertNodeType(opt[0], "raw"), "string")
		}
		color := ""
		if model != "" {
			spec := tmStr(tmAssertNodeType(args[0], "raw"), "string")
			color = tmColorFromSpec(model, spec)
		} else {
			color = tmValidateColor(tmStr(tmAssertNodeType(args[0], "raw"), "string"), parser.gullet.macros, ctx.token)
		}
		if isTextColor {
			return tmObj{"type": "color", "mode": parser.mode, "color": color, "isTextColor": true, "body": tmOrdArgument(args[1])}
		}
		body := parser.parseExpression(true, ctx.breakOnTokenText, true)
		return tmObj{"type": "color", "mode": parser.mode, "color": color, "isTextColor": false, "body": body}
	}
}

// ---- def.js ------------------------------------------------------------------------------------------------------

var tmGlobalMap = map[string]string{
	"\\global": "\\global", "\\long": "\\\\globallong", "\\\\globallong": "\\\\globallong", "\\def": "\\gdef", "\\gdef": "\\gdef",
	"\\edef": "\\xdef", "\\xdef": "\\xdef", "\\let": "\\\\globallet", "\\futurelet": "\\\\globalfuture",
}

var (
	tmControlSeqRe = mustRE(`^(?:[\\{}$&#^_]|EOF)$`)
	tmDigit19Re    = mustRE(`^[1-9]$`)
	tmDigit09Re    = mustRE(`^[0-9]$`)
	tmLatinNameRe  = mustRE(`^[A-Za-z]+$`)
)

func tmCheckControlSequence(tok *tmToken) string {
	if tmControlSeqRe.MatchString(tok.text) {
		tmThrow("Expected a control sequence", tok)
	}
	return tok.text
}

func tmGetRHS(parser *tmParser) *tmToken {
	tok := parser.gullet.popToken()
	if tok.text == "=" {
		tok = parser.gullet.popToken()
		if tok.text == " " {
			tok = parser.gullet.popToken()
		}
	}
	return tok
}

func tmLetCommand(parser *tmParser, name string, tok *tmToken, global bool) {
	macro := parser.gullet.macros.get(tok.text)
	if macro == nil {
		tok.noexpand = true
		macro = &tmMacroDef{tokens: []*tmToken{tok}, numArgs: 0, unexpandable: !parser.gullet.isExpandable(tok.text)}
	}
	parser.gullet.macros.set(name, macro, global)
}

func tmInternal(parser *tmParser) tmObj { return tmObj{"type": "internal", "mode": parser.mode} }

func init() {
	// accent.js
	tmDefineFunction("accent", []string{"\\acute", "\\grave", "\\ddot", "\\dddot", "\\ddddot", "\\tilde", "\\bar", "\\breve", "\\check", "\\hat", "\\vec", "\\dot",
		"\\mathring", "\\overparen", "\\widecheck", "\\widehat", "\\wideparen", "\\widetilde", "\\overrightarrow", "\\overleftarrow", "\\Overrightarrow",
		"\\overleftrightarrow", "\\overgroup", "\\overleftharpoon", "\\overrightharpoon"},
		tmProps{numArgs: 1},
		func(ctx *tmCtx, args, opt []any) any {
			base := tmNormalizeArgument(args[0])
			isStretchy := !tmNonStretchyAccents[ctx.funcName]
			return tmObj{"type": "accent", "mode": ctx.parser.mode, "label": ctx.funcName, "isStretchy": isStretchy, "base": base}
		}, tmAccentBuilder)
	tmDefineFunction("accent", []string{"\\'", "\\`", "\\^", "\\~", "\\=", "\\c", "\\u", "\\.", "\\\"", "\\r", "\\H", "\\v"},
		tmProps{numArgs: 1, allowedInText: true, argTypes: []string{"primitive"}},
		func(ctx *tmCtx, args, opt []any) any {
			base := tmNormalizeArgument(args[0]).(tmObj)
			mode := ctx.parser.mode
			text, _ := base["text"].(string)
			if mode == "text" && text != "" && jsLen(text) == 1 && tmCombiningChar[ctx.funcName] != "" && strings.Contains(tmSmalls, text) {
				return tmObj{"type": "textord", "mode": "text", "text": text + tmCombiningChar[ctx.funcName]}
			} else if ctx.funcName == "\\c" && mode == "text" && text != "" && jsLen(text) == 1 {
				return tmObj{"type": "textord", "mode": "text", "text": text + "\u0327"}
			}
			return tmObj{"type": "accent", "mode": mode, "label": ctx.funcName, "isStretchy": false, "base": base}
		}, tmAccentBuilder)

	// accentunder.js
	tmDefineFunction("accentUnder", []string{"\\underleftarrow", "\\underrightarrow", "\\underleftrightarrow", "\\undergroup", "\\underparen", "\\utilde"},
		tmProps{numArgs: 1},
		func(ctx *tmCtx, args, opt []any) any {
			return tmObj{"type": "accentUnder", "mode": ctx.parser.mode, "label": ctx.funcName, "base": args[0]}
		},
		func(group tmObj, style *tmStyle) mmNode {
			accentNode := tmAccentNode(group)
			accentNode.style.set("math-depth", 0.0)
			return mn("munder", tmBuildGroup(group["base"], style), accentNode)
		})

	// arrow.js
	tmDefineFunction("xArrow", []string{"\\xleftarrow", "\\xrightarrow", "\\xLeftarrow", "\\xRightarrow", "\\xleftrightarrow", "\\xLeftrightarrow", "\\xhookleftarrow",
		"\\xhookrightarrow", "\\xmapsto", "\\xrightharpoondown", "\\xrightharpoonup", "\\xleftharpoondown", "\\xleftharpoonup", "\\xlongequal",
		"\\xtwoheadrightarrow", "\\xtwoheadleftarrow", "\\xtofrom", "\\xleftrightharpoons", "\\xrightleftharpoons", "\\yields", "\\yieldsLeft",
		"\\mesomerism", "\\longrightharpoonup", "\\longleftharpoondown", "\\yieldsLeftRight", "\\chemequilibrium", "\\\\cdrightarrow", "\\\\cdleftarrow",
		"\\\\cdlongequal"},
		tmProps{numArgs: 1, numOptionalArgs: 1},
		func(ctx *tmCtx, args, opt []any) any {
			var body, below any
			if len(args) > 0 {
				body = args[0]
			}
			if len(opt) > 0 {
				below = opt[0]
			}
			return tmObj{"type": "xArrow", "mode": ctx.parser.mode, "name": ctx.funcName, "body": body, "below": below}
		},
		func(group tmObj, style *tmStyle) mmNode {
			node := tmMunderoverNode(tmStr(group, "name"), group["body"], group["below"], style)
			return mn("mrow", tmPadding(0.2778), node, tmPadding(0.2778))
		})
	tmDefineFunction("stackedArrow", []string{"\\equilibriumRight", "\\equilibriumLeft"},
		tmProps{numArgs: 1, numOptionalArgs: 1},
		func(ctx *tmCtx, args, opt []any) any {
			var lowerArrowBody, upperArrowBelow any
			if tmTruthy(args[0]) {
				lowerArrowBody = tmObj{"type": "hphantom", "mode": ctx.parser.mode, "body": args[0]}
			}
			if tmTruthy(opt[0]) {
				upperArrowBelow = tmObj{"type": "hphantom", "mode": ctx.parser.mode, "body": opt[0]}
			}
			return tmObj{"type": "stackedArrow", "mode": ctx.parser.mode, "name": ctx.funcName, "body": args[0], "upperArrowBelow": upperArrowBelow,
				"lowerArrowBody": lowerArrowBody, "below": opt[0]}
		},
		func(group tmObj, style *tmStyle) mmNode {
			name := tmStr(group, "name")
			comp := tmArrowComponent[name]
			topArrow := tmMunderoverNode(comp[0], group["body"], group["upperArrowBelow"], style)
			botArrow := tmMunderoverNode(comp[1], group["lowerArrowBody"], group["below"], style)
			var wrapper *mathNode
			raiseNode := mn("mpadded", topArrow)
			raiseNode.setAttribute("voffset", "0.3em")
			raiseNode.setAttribute("height", "+0.3em")
			raiseNode.setAttribute("depth", "-0.3em")
			if name == "\\equilibriumLeft" {
				botNode := mn("mpadded", botArrow)
				botNode.setAttribute("width", "0.5em")
				wrapper = mn("mpadded", tmPadding(0.2778), botNode, raiseNode, tmPadding(0.2778))
			} else {
				if name == "\\equilibriumRight" {
					raiseNode.setAttribute("width", "0.5em")
				} else {
					raiseNode.setAttribute("width", "0")
				}
				wrapper = mn("mpadded", tmPadding(0.2778), raiseNode, botArrow, tmPadding(0.2778))
			}
			wrapper.setAttribute("voffset", "-0.18em")
			wrapper.setAttribute("height", "-0.18em")
			wrapper.setAttribute("depth", "+0.18em")
			return wrapper
		})

	// bordermatrix.js
	tmDefineFunction("bordermatrix", []string{"\\bordermatrix", "\\matrix"}, tmProps{numArgs: 0, numOptionalArgs: 1},
		func(ctx *tmCtx, args, opt []any) any {
			parser := ctx.parser
			delimiters := []any{"(", ")"}
			if ctx.funcName == "\\bordermatrix" && opt[0] != nil {
				if o, ok := opt[0].(tmObj); ok && tmTruthy(o["body"]) {
					body := tmList(o["body"])
					if len(body) == 1 && tmType(body[0]) == "delimiter" {
						b := body[0].(tmObj)
						delimiters = []any{b["left"], b["right"]}
					}
				}
			}
			parser.consumeSpaces()
			parser.consume()
			env := tmEnvironments["bordermatrix"]
			context := &tmCtx{mode: parser.mode, envName: ctx.funcName[1:], delimiters: delimiters, parser: parser}
			result := env.handler(context, nil, nil)
			parser.expect("}", true)
			return result
		}, nil)

	// cancelto.js
	tmDefineFunction("cancelto", []string{"\\cancelto"}, tmProps{numArgs: 2},
		func(ctx *tmCtx, args, opt []any) any {
			body := args[1]
			return tmObj{"type": "cancelto", "mode": ctx.parser.mode, "body": body, "to": args[0], "isCharacterBox": tmIsCharacterBox(body)}
		},
		tmCanceltoBuilder)

	// char.js
	tmDefineFunction("textord", []string{"\\@char"}, tmProps{numArgs: 1, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			arg := tmAssertNodeType(args[0], "ordgroup")
			number := ""
			for _, g := range tmList(arg["body"]) {
				number += tmStr(tmAssertNodeType(g, "textord"), "text")
			}
			code := jsParseInt(number)
			if code != code {
				tmThrow("\\@char has non-numeric argument "+number, ctx.token)
			}
			if code < 0 || code > 0x10FFFF || code != math.Trunc(code) {
				panic(&tmJSError{"Invalid code point " + jsStr(code)})
			}
			return tmObj{"type": "textord", "mode": ctx.parser.mode, "text": string(rune(int(code)))}
		}, nil)

	// color.js
	tmDefineFunction("color", []string{"\\textcolor"}, tmProps{numArgs: 2, numOptionalArgs: 1, allowedInText: true, argTypes: []string{"raw", "raw", "original"}},
		tmColorHandler(true), tmColorBuilder)
	tmDefineFunction("color", []string{"\\color"}, tmProps{numArgs: 1, numOptionalArgs: 1, allowedInText: true, argTypes: []string{"raw", "raw"}},
		tmColorHandler(false), tmColorBuilder)
	tmDefineFunction("color", []string{"\\definecolor"}, tmProps{numArgs: 3, allowedInText: true, argTypes: []string{"raw", "raw", "raw"}},
		func(ctx *tmCtx, args, opt []any) any {
			parser := ctx.parser
			name := tmStr(tmAssertNodeType(args[0], "raw"), "string")
			if !tmLatinNameRe.MatchString(name) {
				tmThrow("Color name must be latin letters.", ctx.token)
			}
			model := tmStr(tmAssertNodeType(args[1], "raw"), "string")
			if model != "HTML" && model != "RGB" && model != "rgb" {
				tmThrow("Color model must be HTML, RGB, or rgb.", ctx.token)
			}
			spec := tmStr(tmAssertNodeType(args[2], "raw"), "string")
			color := tmColorFromSpec(model, spec)
			parser.gullet.macros.set("\\\\color@"+name, &tmMacroDef{tokens: []*tmToken{{text: color}}, numArgs: 0}, false)
			return tmInternal(parser)
		}, nil)

	// cr.js
	tmDefineFunction("cr", []string{"\\\\"}, tmProps{numArgs: 0, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			parser := ctx.parser
			var size any
			if parser.gullet.future().text == "[" {
				size = nilNode(parser.parseSizeGroup(true))
			}
			newLine := !parser.settings.displayMode
			var sizeValue any
			if tmTruthy(size) {
				sizeValue = tmAssertNodeType(size, "size")["value"]
			}
			return tmObj{"type": "cr", "mode": parser.mode, "newLine": newLine, "size": sizeValue}
		},
		func(group tmObj, style *tmStyle) mmNode {
			node := mn("mo")
			if tmTruthy(group["newLine"]) {
				node.setAttribute("linebreak", "newline")
				if tmTruthy(group["size"]) {
					size := tmCalculateSize(tmSizeOf(group["size"]), style)
					node.setAttribute("height", size.String())
				}
			}
			return node
		})

	// def.js
	tmDefineFunction("internal", []string{"\\global", "\\long", "\\\\globallong"}, tmProps{numArgs: 0, allowedInText: true},
		func(ctx *tmCtx, args, opt []any) any {
			parser := ctx.parser
			parser.consumeSpaces()
			token := parser.fetch()
			if g, ok := tmGlobalMap[token.text]; ok && g != "" {
				if ctx.funcName == "\\global" || ctx.funcName == "\\\\globallong" {
					token.text = g
				}
				return tmAssertNodeType(nilNode(parser.parseFunction("", "")), "internal")
			}
			tmThrow("Invalid token after macro prefix", token)
			return nil
		}, nil)
	tmDefineFunction("internal", []string{"\\def", "\\gdef", "\\edef", "\\xdef"}, tmProps{numArgs: 0, allowedInText: true, primitive: true},
		func(ctx *tmCtx, args, opt []any) any {
			parser := ctx.parser
			tok := parser.gullet.popToken()
			name := tok.text
			if tmControlSeqRe.MatchString(name) {
				tmThrow("Expected a control sequence", tok)
			}
			numArgs := 0
			var insert *tmToken
			delimiters := [][]string{{}}
			for parser.gullet.future().text != "{" {
				tok = parser.gullet.popToken()
				if tok.text == "#" {
					if parser.gullet.future().text == "{" {
						insert = parser.gullet.future()
						delimiters[numArgs] = append(delimiters[numArgs], "{")
						break
					}
					tok = parser.gullet.popToken()
					if !tmDigit19Re.MatchString(tok.text) {
						tmThrowf("Invalid argument number \"" + tok.text + "\"")
					}
					if n, _ := strconv.Atoi(tok.text); n != numArgs+1 {
						tmThrowf("Argument number \"" + tok.text + "\" out of order")
					}
					numArgs++
					delimiters = append(delimiters, []string{})
				} else if tok.text == "EOF" {
					tmThrowf("Expected a macro definition")
				} else {
					delimiters[numArgs] = append(delimiters[numArgs], tok.text)
				}
			}
			tokens, _, _ := parser.gullet.consumeArg(nil)
			if insert != nil {
				tokens = append([]*tmToken{insert}, tokens...)
			}
			if ctx.funcName == "\\edef" || ctx.funcName == "\\xdef" {
				tokens = parser.gullet.expandTokens(tokens)
				if float64(len(tokens)) > parser.gullet.settings.maxExpand {
					tmThrowf("Too many expansions in an " + ctx.funcName)
				}
				for i, j := 0, len(tokens)-1; i < j; i, j = i+1, j-1 {
					tokens[i], tokens[j] = tokens[j], tokens[i]
				}
			}
			parser.gullet.macros.set(name, &tmMacroDef{tokens: tokens, numArgs: numArgs, delimiters: delimiters}, ctx.funcName == tmGlobalMap[ctx.funcName])
			return tmInternal(parser)
		}, nil)
	tmDefineFunction("internal", []string{"\\let", "\\\\globallet"}, tmProps{numArgs: 0, allowedInText: true, primitive: true},
		func(ctx *tmCtx, args, opt []any) any {
			parser := ctx.parser
			name := tmCheckControlSequence(parser.gullet.popToken())
			parser.gullet.consumeSpaces()
			tok := tmGetRHS(parser)
			tmLetCommand(parser, name, tok, ctx.funcName == "\\\\globallet")
			return tmInternal(parser)
		}, nil)
	tmDefineFunction("internal", []string{"\\futurelet", "\\\\globalfuture"}, tmProps{numArgs: 0, allowedInText: true, primitive: true},
		func(ctx *tmCtx, args, opt []any) any {
			parser := ctx.parser
			name := tmCheckControlSequence(parser.gullet.popToken())
			middle := parser.gullet.popToken()
			tok := parser.gullet.popToken()
			tmLetCommand(parser, name, tok, ctx.funcName == "\\\\globalfuture")
			parser.gullet.pushToken(tok)
			parser.gullet.pushToken(middle)
			return tmInternal(parser)
		}, nil)
	tmDefineFunction("internal", []string{"\\newcommand", "\\renewcommand", "\\providecommand"}, tmProps{numArgs: 0, allowedInText: true, primitive: true},
		func(ctx *tmCtx, args, opt []any) any {
			parser := ctx.parser
			name := ""
			tok := parser.gullet.popToken()
			if tok.text == "{" {
				name = tmCheckControlSequence(parser.gullet.popToken())
				parser.gullet.popToken()
			} else {
				name = tmCheckControlSequence(tok)
			}
			exists := parser.gullet.isDefined(name)
			if exists && ctx.funcName == "\\newcommand" {
				tmThrowf("\\newcommand{" + name + "} attempting to redefine " + name + "; use \\renewcommand")
			}
			if !exists && ctx.funcName == "\\renewcommand" {
				tmThrowf("\\renewcommand{" + name + "} when command " + name + " does not yet exist; use \\newcommand")
			}
			numArgs := 0
			if parser.gullet.future().text == "[" {
				tok := parser.gullet.popToken()
				tok = parser.gullet.popToken()
				if !tmDigit09Re.MatchString(tok.text) {
					tmThrowf("Invalid number of arguments: \"" + tok.text + "\"")
				}
				numArgs, _ = strconv.Atoi(tok.text)
				tok = parser.gullet.popToken()
				if tok.text != "]" {
					tmThrowf("Invalid argument \"" + tok.text + "\"")
				}
			}
			tokens, _, _ := parser.gullet.consumeArg(nil)
			if !(ctx.funcName == "\\providecommand" && parser.gullet.macros.has(name)) {
				parser.gullet.macros.set(name, &tmMacroDef{tokens: tokens, numArgs: numArgs}, false)
			}
			return tmInternal(parser)
		}, nil)
}
