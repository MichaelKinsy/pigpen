package websearch

import (
	"strconv"
	"strings"
	"unicode/utf16"
)

// Temml 0.13.4 (MIT): Settings.js, Parser.js, parseTree.js, parseNode.js, defineFunction.js and the entry points of
// temml.js that Defuddle uses (renderToString with displayMode and throwOnError: false).

type tmSettings struct {
	displayMode        bool
	annotate           bool
	leqno              bool
	throwOnError       bool
	errorColor         string
	macros             map[string]any
	wrap               string
	xml                bool
	colorIsTextColor   bool
	strict             bool
	maxExpand          float64
	wrapDelimiterPairs bool
	maxSize            []float64
}

func tmNewSettings(displayMode, throwOnError bool) *tmSettings {
	return &tmSettings{
		displayMode: displayMode, throwOnError: throwOnError, errorColor: "#b22222", macros: map[string]any{}, wrap: "none",
		maxExpand: 1000, wrapDelimiterPairs: true, maxSize: []float64{tmInfinity, tmInfinity},
	}
}

// isTrusted: trust is false, so every command that needs trust is rejected.
func (s *tmSettings) isTrusted(context tmObj) bool { return false }

// tmCtx is the context object handed to function and environment handlers.
type tmCtx struct {
	funcName         string
	parser           *tmParser
	token            *tmToken
	breakOnTokenText string
	envName          string
	mode             string
	delimiters       []any
}

type tmHandler func(ctx *tmCtx, args []any, opt []any) any

// tmFuncData is the data defineFunction and defineEnvironment record for a name.
type tmFuncData struct {
	typ               string
	numArgs           int
	argTypes          []string
	allowedInArgument bool
	allowedInText     bool
	allowedInMath     bool
	numOptionalArgs   int
	infix             bool
	primitive         bool
	handler           tmHandler
}

var (
	tmFunctions    = map[string]*tmFuncData{}
	tmEnvironments = map[string]*tmFuncData{}
	tmBuilders     = map[string]tmBuilder{}
)

type tmBuilder func(group tmObj, style *tmStyle) mmNode

type tmProps struct {
	numArgs           int
	argTypes          []string
	allowedInArgument bool
	allowedInText     bool
	allowedInMath     *bool
	numOptionalArgs   int
	infix             bool
	primitive         bool
}

var tmFalse = false

func tmDefineFunction(typ string, names []string, p tmProps, handler tmHandler, builder tmBuilder) {
	allowedInMath := true
	if p.allowedInMath != nil {
		allowedInMath = *p.allowedInMath
	}
	data := &tmFuncData{typ: typ, numArgs: p.numArgs, argTypes: p.argTypes, allowedInArgument: p.allowedInArgument, allowedInText: p.allowedInText,
		allowedInMath: allowedInMath, numOptionalArgs: p.numOptionalArgs, infix: p.infix, primitive: p.primitive, handler: handler}
	for _, n := range names {
		tmFunctions[n] = data
	}
	if typ != "" && builder != nil {
		tmBuilders[typ] = builder
	}
}

func tmDefineBuilder(typ string, builder tmBuilder) { tmBuilders[typ] = builder }

func tmDefineEnvironment(typ string, names []string, numArgs int, handler tmHandler, builder tmBuilder) {
	data := &tmFuncData{typ: typ, numArgs: numArgs, handler: handler}
	for _, n := range names {
		tmEnvironments[n] = data
	}
	if builder != nil {
		tmBuilders[typ] = builder
	}
}

// ---- parse node helpers ------------------------------------------------------------------------------------------

func tmType(x any) string {
	if n, ok := x.(tmObj); ok {
		t, _ := n["type"].(string)
		return t
	}
	return ""
}

func tmStr(n tmObj, k string) string {
	s, _ := n[k].(string)
	return s
}

// tmList is node[k] as an array (nil when it is not one).
func tmList(x any) []any {
	l, _ := x.([]any)
	return l
}

func tmAssertNodeType(node any, typ string) tmObj {
	n, ok := node.(tmObj)
	if !ok || n == nil || tmStr(n, "type") != typ {
		got := "undefined"
		if ok && n != nil {
			got = "node of type " + tmStr(n, "type")
		}
		panic(&tmJSError{"Expected node of type " + typ + ", but got " + got})
	}
	return n
}

var tmNonAtoms = map[string]bool{"accent-token": true, "mathord": true, "op-token": true, "spacing": true, "textord": true}

func tmCheckSymbolNodeType(node any) tmObj {
	n, ok := node.(tmObj)
	if !ok || n == nil {
		return nil
	}
	t := tmStr(n, "type")
	if t == "atom" || t == "delimiter" || tmNonAtoms[t] {
		return n
	}
	return nil
}

func tmAssertSymbolNodeType(node any) tmObj {
	if n := tmCheckSymbolNodeType(node); n != nil {
		return n
	}
	panic(&tmJSError{"Expected node of symbol group type"})
}

func tmNormalizeArgument(arg any) any {
	n := arg.(tmObj)
	if tmStr(n, "type") == "ordgroup" {
		if body := tmList(n["body"]); len(body) == 1 {
			return body[0]
		}
	}
	return n
}

func tmOrdArgument(arg any) []any {
	n := arg.(tmObj)
	if tmStr(n, "type") == "ordgroup" {
		return tmList(n["body"])
	}
	return []any{n}
}

func tmUTF16Units(s string) []uint16 { return utf16.Encode([]rune(s)) }

// ---- Parser ------------------------------------------------------------------------------------------------------

type tmParser struct {
	mode           string
	gullet         *tmGullet
	settings       *tmSettings
	leftrightDepth int
	prevAtomType   string
	nextToken      *tmToken
}

func tmNewParser(input string, settings *tmSettings) *tmParser {
	p := &tmParser{mode: "math", settings: settings}
	p.gullet = tmNewGullet(input, settings, p.mode)
	return p
}

func (p *tmParser) expect(text string, consume bool) {
	if p.fetch().text != text {
		tmThrow("Expected '"+text+"', got '"+p.fetch().text+"'", p.fetch())
	}
	if consume {
		p.consume()
	}
}

func (p *tmParser) consume() { p.nextToken = nil }

func (p *tmParser) fetch() *tmToken {
	if p.nextToken == nil {
		p.nextToken = p.gullet.expandNextToken()
	}
	return p.nextToken
}

func (p *tmParser) switchMode(m string) {
	p.mode = m
	p.gullet.switchMode(m)
}

func (p *tmParser) parse() []any {
	p.gullet.beginGroup()
	if p.settings.colorIsTextColor {
		p.gullet.macros.set("\\color", "\\textcolor", false)
	}
	parse := p.parseExpression(false, "", false)
	p.expect("EOF", true)
	tag := p.gullet.macros.get("\\df@tag")
	p.gullet.endGroup()
	if tmTruthy(tag) {
		p.gullet.macros.current["\\df@tag"] = tag
	}
	return parse
}

var tmEndOfExpression = []string{"}", "\\endgroup", "\\end", "\\right", "\\endtoggle", "&"}

func (p *tmParser) subparse(tokens []*tmToken) []any {
	oldToken := p.nextToken
	p.consume()
	p.gullet.pushToken(&tmToken{text: "}"})
	p.gullet.pushTokens(tokens)
	parse := p.parseExpression(false, "", false)
	p.expect("}", true)
	p.nextToken = oldToken
	return parse
}

func (p *tmParser) parseExpression(breakOnInfix bool, breakOnTokenText string, breakOnMiddle bool) []any {
	body := []any{}
	p.prevAtomType = ""
	for {
		if p.mode == "math" {
			p.consumeSpaces()
		}
		lex := p.fetch()
		ended := false
		for _, e := range tmEndOfExpression {
			if e == lex.text {
				ended = true
				break
			}
		}
		if ended {
			break
		}
		if breakOnTokenText != "" && lex.text == breakOnTokenText {
			break
		}
		if breakOnMiddle && lex.text == "\\middle" {
			break
		}
		if breakOnInfix {
			if f, ok := tmFunctions[lex.text]; ok && f.infix {
				break
			}
		}
		atom := p.parseAtom(breakOnTokenText)
		if atom == nil {
			break
		} else if tmStr(atom, "type") == "internal" {
			continue
		}
		body = append(body, atom)
		if tmStr(atom, "type") == "atom" {
			p.prevAtomType = tmStr(atom, "family")
		} else {
			p.prevAtomType = tmStr(atom, "type")
		}
	}
	if p.mode == "text" {
		body = p.formLigatures(body)
	}
	return p.handleInfixNodes(body)
}

func (p *tmParser) handleInfixNodes(body []any) []any {
	overIndex := -1
	var funcName string
	for i, b := range body {
		n := b.(tmObj)
		if tmStr(n, "type") == "infix" {
			if overIndex != -1 {
				tmThrow("only one infix operator per group", n["token"])
			}
			overIndex = i
			funcName = tmStr(n, "replaceWith")
		}
	}
	if overIndex != -1 && funcName != "" {
		numerBody := append([]any{}, body[:overIndex]...)
		denomBody := append([]any{}, body[overIndex+1:]...)
		var numerNode, denomNode tmObj
		if len(numerBody) == 1 && tmType(numerBody[0]) == "ordgroup" {
			numerNode = numerBody[0].(tmObj)
		} else {
			numerNode = tmObj{"type": "ordgroup", "mode": p.mode, "body": numerBody}
		}
		if len(denomBody) == 1 && tmType(denomBody[0]) == "ordgroup" {
			denomNode = denomBody[0].(tmObj)
		} else {
			denomNode = tmObj{"type": "ordgroup", "mode": p.mode, "body": denomBody}
		}
		var node any
		if funcName == "\\\\abovefrac" {
			node = p.callFunction(funcName, []any{numerNode, body[overIndex], denomNode}, nil, nil, "")
		} else {
			node = p.callFunction(funcName, []any{numerNode, denomNode}, nil, nil, "")
		}
		return []any{node}
	}
	return body
}

func (p *tmParser) handleSupSubscript(name string) tmObj {
	symbolToken := p.fetch()
	symbol := symbolToken.text
	p.consume()
	p.consumeSpaces()
	var group tmObj
	for {
		group = p.parseGroup(name, "")
		if group == nil {
			panic(&tmJSError{"Cannot read properties of null (reading 'type')"})
		}
		if tmStr(group, "type") != "internal" {
			break
		}
	}
	_ = symbol
	return group
}

func (p *tmParser) formatUnsupportedCmd(text string) tmObj {
	var textords []any
	for _, r := range text {
		textords = append(textords, tmObj{"type": "textord", "mode": "text", "text": string(r)})
	}
	textNode := tmObj{"type": "text", "mode": p.mode, "body": textords}
	return tmObj{"type": "color", "mode": p.mode, "color": p.settings.errorColor, "body": []any{textNode}}
}

var tmUnicodeSubRe = mustRE(`^[₊₋₌₍₎₀₁₂₃₄₅₆₇₈₉ₐₑₕᵢⱼₖₗₘₙₒₚᵣₛₜᵤᵥₓᵦᵧᵨᵩᵪ]`)

// nilNode keeps a missing node as an untyped nil in an object, like `undefined` in the original.
func nilNode(n tmObj) any {
	if n == nil {
		return nil
	}
	return n
}

func (p *tmParser) parseAtom(breakOnTokenText string) tmObj {
	base := p.parseGroup("atom", breakOnTokenText)
	if base != nil && tmStr(base, "type") == "internal" {
		return base
	}
	if p.mode == "text" {
		return base
	}
	var superscript, subscript tmObj
	for {
		p.consumeSpaces()
		lex := p.fetch()
		if lex.text == "\\limits" || lex.text == "\\nolimits" {
			if base != nil && tmStr(base, "type") == "op" {
				base["limits"] = lex.text == "\\limits"
				base["alwaysHandleSupSub"] = true
			} else if base != nil && tmStr(base, "type") == "operatorname" {
				if tmTruthy(base["alwaysHandleSupSub"]) {
					base["limits"] = lex.text == "\\limits"
				}
			} else {
				tmThrow("Limit controls must follow a math operator", lex)
			}
			p.consume()
		} else if lex.text == "^" {
			if superscript != nil {
				tmThrow("Double superscript", lex)
			}
			superscript = p.handleSupSubscript("superscript")
		} else if lex.text == "_" {
			if subscript != nil {
				tmThrow("Double subscript", lex)
			}
			subscript = p.handleSupSubscript("subscript")
		} else if lex.text == "'" {
			if superscript != nil {
				tmThrow("Double superscript", lex)
			}
			prime := tmObj{"type": "textord", "mode": p.mode, "text": "\\prime"}
			primes := []any{prime}
			p.consume()
			for p.fetch().text == "'" {
				primes = append(primes, prime)
				p.consume()
			}
			if p.fetch().text == "^" {
				primes = append(primes, p.handleSupSubscript("superscript"))
			}
			superscript = tmObj{"type": "ordgroup", "mode": p.mode, "body": primes}
		} else if _, ok := tmSubsAndSups()[lex.text]; ok {
			isSub := tmUnicodeSubRe.MatchString(lex.text)
			subsupTokens := []*tmToken{{text: tmSubsAndSups()[lex.text]}}
			p.consume()
			for {
				token := p.fetch().text
				if _, ok := tmSubsAndSups()[token]; !ok {
					break
				}
				if tmUnicodeSubRe.MatchString(token) != isSub {
					break
				}
				subsupTokens = append([]*tmToken{{text: tmSubsAndSups()[token]}}, subsupTokens...)
				p.consume()
			}
			body := p.subparse(subsupTokens)
			if isSub {
				subscript = tmObj{"type": "ordgroup", "mode": "math", "body": body}
			} else {
				superscript = tmObj{"type": "ordgroup", "mode": "math", "body": body}
			}
		} else {
			break
		}
	}
	if superscript != nil || subscript != nil {
		if base != nil && tmStr(base, "type") == "multiscript" && !tmTruthy(base["postscripts"]) {
			base["postscripts"] = tmObj{"sup": nilNode(superscript), "sub": nilNode(subscript)}
			return base
		}
		var isFollowedByDelimiter any
		if !(base == nil || (tmStr(base, "type") != "op" && tmStr(base, "type") != "operatorname")) {
			if p.nextToken == nil {
				panic(&tmJSError{"Cannot read properties of null (reading 'text')"})
			}
			isFollowedByDelimiter = tmIsDelimiter(p.nextToken.text)
		}
		return tmObj{"type": "supsub", "mode": p.mode, "base": nilNode(base), "sup": nilNode(superscript), "sub": nilNode(subscript), "isFollowedByDelimiter": isFollowedByDelimiter}
	}
	return base
}

func (p *tmParser) parseFunction(breakOnTokenText, name string) tmObj {
	token := p.fetch()
	fn := token.text
	funcData, ok := tmFunctions[fn]
	if !ok {
		return nil
	}
	p.consume()
	if name != "" && name != "atom" && !funcData.allowedInArgument {
		tmThrow("Got function '"+fn+"' with no arguments as "+name, token)
	} else if p.mode == "text" && !funcData.allowedInText {
		tmThrow("Can't use function '"+fn+"' in text mode", token)
	} else if p.mode == "math" && !funcData.allowedInMath {
		tmThrow("Can't use function '"+fn+"' in math mode", token)
	}
	prevAtomType := p.prevAtomType
	args, optArgs := p.parseArguments(fn, funcData)
	p.prevAtomType = prevAtomType
	r := p.callFunction(fn, args, optArgs, token, breakOnTokenText)
	n, _ := r.(tmObj)
	return n
}

func (p *tmParser) callFunction(name string, args, optArgs []any, token *tmToken, breakOnTokenText string) any {
	ctx := &tmCtx{funcName: name, parser: p, token: token, breakOnTokenText: breakOnTokenText}
	if f, ok := tmFunctions[name]; ok && f.handler != nil {
		return f.handler(ctx, args, optArgs)
	}
	tmThrowf("No function handler for " + name)
	return nil
}

func (p *tmParser) parseArguments(fn string, funcData *tmFuncData) (args, optArgs []any) {
	totalArgs := funcData.numArgs + funcData.numOptionalArgs
	if totalArgs == 0 {
		return []any{}, []any{}
	}
	args = []any{}
	optArgs = []any{}
	for i := 0; i < totalArgs; i++ {
		argType := ""
		if funcData.argTypes != nil && i < len(funcData.argTypes) {
			argType = funcData.argTypes[i]
		}
		isOptional := i < funcData.numOptionalArgs
		if (funcData.primitive && argType == "") || (funcData.typ == "sqrt" && i == 1 && optArgs[0] == nil) {
			argType = "primitive"
		}
		arg := p.parseGroupOfType("argument to '"+fn+"'", argType, isOptional)
		if isOptional {
			optArgs = append(optArgs, arg)
		} else if arg != nil {
			args = append(args, arg)
		} else {
			tmThrowf("Null argument, please report this as a bug")
		}
	}
	return args, optArgs
}

// parseGroupOfType returns an untyped nil for a missing optional argument.
func (p *tmParser) parseGroupOfType(name, typ string, optional bool) any {
	switch typ {
	case "size":
		return nilNode(p.parseSizeGroup(optional))
	case "url":
		return nilNode(p.parseUrlGroup(optional))
	case "math", "text":
		return nilNode(p.parseArgumentGroup(optional, typ))
	case "hbox":
		group := p.parseArgumentGroup(optional, "text")
		if group == nil {
			return nil
		}
		return tmObj{"type": "styling", "mode": group["mode"], "body": []any{group}, "scriptLevel": "text"}
	case "raw":
		token := p.parseStringGroup("raw", optional)
		if token == nil {
			return nil
		}
		return tmObj{"type": "raw", "mode": "text", "string": token.text}
	case "primitive":
		if optional {
			tmThrowf("A primitive argument cannot be optional")
		}
		group := p.parseGroup(name, "")
		if group == nil {
			tmThrow("Expected group as "+name, p.fetch())
		}
		return group
	case "original", "":
		return nilNode(p.parseArgumentGroup(optional, ""))
	}
	tmThrow("Unknown group type as "+name, p.fetch())
	return nil
}

func (p *tmParser) consumeSpaces() {
	for {
		ch := p.fetch().text
		if ch == " " || ch == "\u00a0" || ch == "\ufe0e" {
			p.consume()
		} else {
			break
		}
	}
}

func (p *tmParser) parseStringGroup(modeName string, optional bool) *tmToken {
	argToken := p.gullet.scanArgument(optional)
	if argToken == nil {
		return nil
	}
	var b strings.Builder
	for {
		next := p.fetch()
		if next.text == "EOF" {
			break
		}
		b.WriteString(next.text)
		p.consume()
	}
	p.consume()
	argToken.text = b.String()
	return argToken
}

var (
	tmSizeGroupRe = mustRE(`^[-+]? *(?:$|\d+|\d+\.\d*|\.\d*) *[a-z]{0,2} *$`)
	tmSizeRe      = mustRE(`([-+]?) *(\d+(?:\.\d*)?|\.\d+) *([a-z]{2})`)
	tmTextRe      = mustRE(`^ *\\text`)
)

func (p *tmParser) parseRegexGroup(re *lazyRE, modeName string) *tmToken {
	firstToken := p.fetch()
	lastToken := firstToken
	str := ""
	for {
		next := p.fetch()
		if next.text == "EOF" || !re.MatchString(str+next.text) {
			break
		}
		lastToken = next
		str += lastToken.text
		p.consume()
	}
	if str == "" {
		tmThrow("Invalid "+modeName+": '"+firstToken.text+"'", firstToken)
	}
	return firstToken.rangeTo(lastToken, str)
}

func (p *tmParser) parseSizeGroup(optional bool) tmObj {
	var res *tmToken
	isBlank := false
	p.gullet.consumeSpaces()
	if !optional && p.gullet.future().text != "{" {
		res = p.parseRegexGroup(tmSizeGroupRe, "size")
	} else {
		res = p.parseStringGroup("size", optional)
	}
	if res == nil {
		return nil
	}
	if !optional && len(res.text) == 0 {
		res.text = "0pt"
		isBlank = true
	}
	match := tmSizeRe.FindStringSubmatch(res.text)
	if match == nil {
		tmThrow("Invalid size: '"+res.text+"'", res)
	}
	number, _ := strconv.ParseFloat(match[1]+match[2], 64)
	if !tmValidUnit(match[3]) {
		tmThrow("Invalid unit: '"+match[3]+"'", res)
	}
	return tmObj{"type": "size", "mode": p.mode, "value": tmSize{number, match[3]}, "isBlank": isBlank}
}

func (p *tmParser) parseUrlGroup(optional bool) tmObj {
	p.gullet.lexer.setCatcode("%", 13)
	p.gullet.lexer.setCatcode("~", 12)
	res := p.parseStringGroup("url", optional)
	p.gullet.lexer.setCatcode("%", 14)
	p.gullet.lexer.setCatcode("~", 13)
	if res == nil {
		return nil
	}
	url := strings.ReplaceAll(res.text, "{\u2044}", "/")
	return tmObj{"type": "url", "mode": p.mode, "url": url}
}

func (p *tmParser) parseArgumentGroup(optional bool, mode string) tmObj {
	argToken := p.gullet.scanArgument(optional)
	if argToken == nil {
		return nil
	}
	outerMode := p.mode
	if mode != "" {
		p.switchMode(mode)
	}
	p.gullet.beginGroup()
	expression := p.parseExpression(false, "EOF", false)
	p.expect("EOF", true)
	p.gullet.endGroup()
	result := tmObj{"type": "ordgroup", "mode": p.mode, "loc": argToken.loc, "body": expression}
	if mode != "" {
		p.switchMode(outerMode)
	}
	return result
}

func (p *tmParser) parseGroup(name, breakOnTokenText string) tmObj {
	firstToken := p.fetch()
	text := firstToken.text
	if name == "argument to '\\left'" {
		return p.parseSymbol()
	}
	var result tmObj
	if text == "{" || text == "\\begingroup" || text == "\\toggle" {
		p.consume()
		groupEnd := "\\endtoggle"
		if text == "{" {
			groupEnd = "}"
		} else if text == "\\begingroup" {
			groupEnd = "\\endgroup"
		}
		p.gullet.beginGroup()
		expression := p.parseExpression(false, groupEnd, false)
		lastToken := p.fetch()
		p.expect(groupEnd, true)
		p.gullet.endGroup()
		typ := "ordgroup"
		if lastToken.text == "\\endtoggle" {
			typ = "toggle"
		}
		result = tmObj{"type": typ, "mode": p.mode, "loc": tmRange(firstToken, lastToken), "body": expression}
		if text == "\\begingroup" {
			result["semisimple"] = true
		} else {
			result["semisimple"] = nil
		}
	} else {
		result = p.parseFunction(breakOnTokenText, name)
		if result == nil {
			result = p.parseSymbol()
		}
		if result == nil && len(text) > 0 && text[0] == '\\' && !tmImplicitCommands[text] {
			if p.settings.throwOnError {
				tmThrow("Unsupported function name: "+text, firstToken)
			}
			result = p.formatUnsupportedCmd(text)
			p.consume()
		}
	}
	return result
}

func (p *tmParser) formLigatures(group []any) []any {
	n := len(group) - 1
	textOf := func(i int) string {
		if i >= len(group) {
			panic(&tmJSError{"Cannot read properties of undefined (reading 'text')"})
		}
		s, _ := group[i].(tmObj)["text"].(string)
		return s
	}
	for i := 0; i < n; i++ {
		a := group[i].(tmObj)
		v, _ := a["text"].(string)
		if v == "-" && textOf(i+1) == "-" {
			if i+1 < n && textOf(i+2) == "-" {
				group[i] = tmObj{"type": "textord", "mode": "text", "loc": tmRange(a, group[i+2]), "text": "---"}
				group = append(group[:i+1], group[i+3:]...)
				n -= 2
			} else {
				group[i] = tmObj{"type": "textord", "mode": "text", "loc": tmRange(a, group[i+1]), "text": "--"}
				group = append(group[:i+1], group[i+2:]...)
				n--
			}
		}
		if (v == "'" || v == "`") && textOf(i+1) == v {
			group[i] = tmObj{"type": "textord", "mode": "text", "loc": tmRange(a, group[i+1]), "text": v + v}
			group = append(group[:i+1], group[i+2:]...)
			n--
		}
	}
	return group
}

var (
	tmVerbRe            = mustRE(`^\\verb[^a-zA-Z]`)
	tmBinLeftCancellers = map[string]bool{"bin": true, "op": true, "open": true, "punct": true, "rel": true}
	tmAtoms             = map[string]bool{"bin": true, "close": true, "inner": true, "open": true, "punct": true, "rel": true}
)

// tmTrailingMarks is combiningDiacriticalMarksEndRegex.exec(text): the UTF-16 index of the trailing run, or -1.
func tmTrailingMarks(units []uint16) int {
	i := len(units)
	for i > 0 && units[i-1] >= 0x300 && units[i-1] <= 0x36f {
		i--
	}
	if i == len(units) {
		return -1
	}
	return i
}

func (p *tmParser) parseSymbol() tmObj {
	nucleus := p.fetch()
	text := nucleus.text
	if tmVerbRe.MatchString(text) {
		p.consume()
		arg := tmUTF16Units(text)[5:]
		star := len(arg) > 0 && arg[0] == '*'
		if star {
			arg = arg[1:]
		}
		if len(arg) < 2 || arg[0] != arg[len(arg)-1] {
			tmThrowf("\\verb assertion failed --\n                    please report what input caused this bug")
		}
		arg = arg[1 : len(arg)-1]
		return tmObj{"type": "verb", "mode": "text", "body": string(utf16.Decode(arg)), "star": star}
	}
	units := tmUTF16Units(text)
	symbolsOf := tmSymbolsMath()
	if p.mode == "text" {
		symbolsOf = tmSymbolsText()
	}
	if len(units) > 0 && (units[0] < 0xd800 || units[0] > 0xdfff) {
		first := tmU16(units[0])
		if rep, ok := tmUnicodeSymbols()[first]; ok && p.mode == "math" && symbolsOf[first] == nil {
			if p.settings.strict && p.mode == "math" {
				tmThrow("Accented Unicode text character \""+first+"\" used in math mode", nucleus)
			}
			text = rep + tmU16s(units[1:])
			units = tmUTF16Units(text)
		}
	}
	matchIdx := -1
	if p.mode == "math" {
		matchIdx = tmTrailingMarks(units)
	}
	var marks []uint16
	if matchIdx >= 0 {
		marks = units[matchIdx:]
		text = tmU16s(units[:matchIdx])
		if text == "i" {
			text = "\u0131"
		} else if text == "j" {
			text = "\u0237"
		}
	}
	var symbol tmObj
	if sym, ok := symbolsOf[text]; ok {
		group := sym.group
		if group == "bin" && (tmBinLeftCancellers[p.prevAtomType] || p.prevAtomType == "") {
			group = "open"
		}
		loc := tmRange(nucleus, nil)
		var s tmObj
		if tmAtoms[group] {
			family := group
			s = tmObj{"type": "atom", "mode": p.mode, "family": family, "loc": loc, "text": text}
			if (family == "rel" || family == "bin") && p.prevAtomType == "text" {
				if loc != nil && tmTextRe.MatchString(tmU16s(loc.lexer.input[min(loc.end, len(loc.lexer.input)):])) {
					s["needsSpacing"] = true
				}
			}
		} else {
			if ascii, ok := tmAsciiFromScript()[text]; ok && ascii != "" {
				p.consume()
				nextCode := tmCodePointUnit(p.fetch().text)
				font := "mathcal"
				if nextCode == 0xfe01 {
					font = "mathscr"
				}
				if nextCode == 0xfe00 || nextCode == 0xfe01 {
					p.consume()
				}
				return tmObj{"type": "font", "mode": "math", "font": font, "body": tmObj{"type": "mathord", "mode": "math", "loc": loc, "text": ascii}}
			}
			s = tmObj{"type": group, "mode": p.mode, "loc": loc, "text": text}
		}
		symbol = s
	} else if tu := tmUTF16Units(text); (len(tu) > 0 && tu[0] >= 0x80) || tmTrailingMarks(tu) >= 0 {
		if p.settings.strict && p.mode == "math" {
			tmThrow("Unicode text character \""+tmU16(tu[0])+"\" used in math mode", nucleus)
		}
		symbol = tmObj{"type": "textord", "mode": "text", "loc": tmRange(nucleus, nil), "text": text}
	} else {
		return nil
	}
	p.consume()
	for _, m := range marks {
		accent := tmU16(m)
		cmd, ok := tmUnicodeAccents()[accent]
		if !ok {
			tmThrow("Unknown accent ' "+accent+"'", nucleus)
		}
		command := cmd.math
		if p.mode != "math" {
			command = ""
		}
		if command == "" {
			command = cmd.text
		}
		if command == "" {
			tmThrow("Accent "+accent+" unsupported in "+p.mode+" mode", nucleus)
		}
		symbol = tmObj{"type": "accent", "mode": p.mode, "loc": tmRange(nucleus, nil), "label": command, "isStretchy": false, "base": symbol}
	}
	return symbol
}

// tmCodePointUnit is str.charCodeAt(0).
func tmCodePointUnit(s string) int {
	u := tmUTF16Units(s)
	if len(u) == 0 {
		return -1
	}
	return int(u[0])
}

// ---- parseTree and the entry points -----------------------------------------------------------------------------

func tmParseTree(toParse string, settings *tmSettings) []any {
	var tree []any
	var parser *tmParser
	func() {
		defer func() {
			if r := recover(); r != nil {
				if pe, ok := r.(*tmParseError); ok && pe.Error() == "ParseError:  Unmatched delimiter" {
					settings.wrapDelimiterPairs = false
					parser = tmNewParser(toParse, settings)
					delete(parser.gullet.macros.current, "\\df@tag")
					tree = parser.parse()
					return
				}
				panic(r)
			}
		}()
		parser = tmNewParser(toParse, settings)
		delete(parser.gullet.macros.current, "\\df@tag")
		tree = parser.parse()
	}()
	if !(len(tree) > 0 && tmType(tree[0]) == "array" && tmTruthy(tree[0].(tmObj)["addEqnNum"])) {
		if tmTruthy(parser.gullet.macros.get("\\df@tag")) {
			if !settings.displayMode {
				tmThrowf("\\tag works only in display mode")
			}
			parser.gullet.feed("\\df@tag")
			tree = []any{tmObj{"type": "tag", "mode": "text", "body": tree, "tag": parser.parse()}}
		}
	}
	return tree
}

// dfdRenderLatexToMathML is temml.renderToString(latex, { displayMode, throwOnError: false }) as math.full.js calls
// it. An exception that is not a ParseError (which the original rethrows and the caller catches) yields "": the
// caller finds no <math> element in either case.
func dfdRenderLatexToMathML(latex string, display bool) (out string) {
	defer func() {
		if r := recover(); r != nil {
			// a JavaScript exception, or a Go run-time error standing for a TypeError of the original
			out = ""
		}
	}()
	settings := tmNewSettings(display, false)
	var node mmNode
	func() {
		defer func() {
			if r := recover(); r != nil {
				if pe, ok := r.(*tmParseError); ok {
					span := newSpan([]string{"temml-error"}, []mmNode{&textNode{latex + "\n\n" + pe.Error()}})
					span.style.set("color", settings.errorColor)
					span.style.set("whiteSpace", "pre-line")
					node = span
					return
				}
				panic(r)
			}
		}()
		tree := tmParseTree(latex, settings)
		level := tmStyleText
		if settings.displayMode {
			level = tmStyleDisplay
		}
		style := tmNewStyle(float64(level), nil, "", "", 1, "", "", settings.maxSize)
		node = tmBuildMathML(tree, latex, style, settings)
	}()
	return node.toMarkup()
}
