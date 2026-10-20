package websearch

import (
	"strconv"
	"strings"
	"unicode/utf16"
)

// Port of Temml 0.13.4 (MIT): the lexer, the macro expander ("gullet") and the namespaces of macros
// (src/Lexer.js, MacroExpander.js, Namespace.js, Token.js, SourceLocation.js, ParseError.js).
//
// Parse nodes are the dynamic objects of the original (tmObj), so that every handler and builder can stay as close
// to the JavaScript as possible. See port/defuddle-math-notes.md.

// tmObj is a parse node or any other JavaScript object of the original.
type tmObj = map[string]any

// tmParseError is the ParseError of the original (message already prefixed and decorated with the position).
type tmParseError struct {
	msg      string
	position int
	hasPos   bool
}

func (e *tmParseError) Error() string { return "ParseError: " + e.msg }

// tmJSError is any other exception of the original (TypeError, Error): it is not rendered as an error span.
type tmJSError struct{ msg string }

func (e *tmJSError) Error() string { return e.msg }

type tmLoc struct {
	lexer      *tmLexer
	start, end int
}

type tmToken struct {
	text         string
	loc          *tmLoc
	noexpand     bool
	treatAsRelax bool
}

func (t *tmToken) rangeTo(end *tmToken, text string) *tmToken {
	return &tmToken{text: text, loc: tmRange(t, end)}
}

// tmLocOf is the loc property of a token or a parse node.
func tmLocOf(x any) *tmLoc {
	switch v := x.(type) {
	case *tmToken:
		if v == nil {
			return nil
		}
		return v.loc
	case tmObj:
		if l, ok := v["loc"].(*tmLoc); ok {
			return l
		}
	case *tmLoc:
		return v
	}
	return nil
}

// tmRange is SourceLocation.range(first, second); second is nil when it is not given.
func tmRange(first, second any) *tmLoc {
	if second == nil || isNilToken(second) {
		return tmLocOf(first)
	}
	fl, sl := tmLocOf(first), tmLocOf(second)
	if first == nil || fl == nil || sl == nil || fl.lexer != sl.lexer {
		return nil
	}
	return &tmLoc{lexer: fl.lexer, start: fl.start, end: sl.end}
}

func isNilToken(x any) bool {
	t, ok := x.(*tmToken)
	return ok && t == nil
}

// tmThrow is `throw new ParseError(message, token)`.
func tmThrow(message string, token any) {
	panic(tmNewParseError(message, token))
}

func tmThrowf(message string) { tmThrow(message, nil) }

func tmNewParseError(message string, token any) *tmParseError {
	e := " " + message
	loc := tmLocOf(token)
	pe := &tmParseError{}
	if loc != nil && loc.start <= loc.end {
		input := loc.lexer.input
		start, end := loc.start, loc.end
		pe.position, pe.hasPos = start, true
		if start == len(input) {
			e += " at end of input: "
		} else {
			e += " at position " + strconv.Itoa(start+1) + ": \n"
		}
		var underlined strings.Builder
		for _, u := range input[start:min(end, len(input))] {
			underlined.WriteString(tmU16(u))
			underlined.WriteString("\u0332")
		}
		// surrogate pairs are underlined per code unit in the original; a pair is kept whole here
		var left, right string
		if start > 15 {
			left = "…" + tmU16s(input[start-15:start])
		} else {
			left = tmU16s(input[:start])
		}
		if end+15 < len(input) {
			right = tmU16s(input[end:end+15]) + "…"
		} else {
			right = tmU16s(input[min(end, len(input)):])
		}
		e += left + underlined.String() + right
	}
	pe.msg = e
	return pe
}

func tmU16(u uint16) string { return string(utf16.Decode([]uint16{u})) }

func tmU16s(u []uint16) string { return string(utf16.Decode(u)) }

// ---- Lexer ---------------------------------------------------------------------------------------------------

type tmLexer struct {
	input    []uint16
	settings *tmSettings
	pos      int // tokenRegex.lastIndex
	catcodes map[string]int
}

func tmNewLexer(input string, settings *tmSettings) *tmLexer {
	return &tmLexer{input: utf16.Encode([]rune(input)), settings: settings, catcodes: map[string]int{"%": 14, "~": 13}}
}

func (l *tmLexer) setCatcode(ch string, code int) { l.catcodes[ch] = code }

func tmIsLineTerminator(u uint16) bool { return u == '\n' || u == '\r' || u == 0x2028 || u == 0x2029 }

func tmIsLetterOrAt(u uint16) bool {
	return (u >= 'a' && u <= 'z') || (u >= 'A' && u <= 'Z') || u == '@'
}

func tmIsCombining(u uint16) bool { return u >= 0x300 && u <= 0x36f }

// matchAt matches tokenRegex at p: the token text, the end of the match and ok. whitespace reports the groups 1 and 2.
func (l *tmLexer) matchAt(p int) (text string, end int, ok bool) {
	in := l.input
	n := len(in)
	u := in[p]
	// (1) [ \r\n\t]+
	if u == ' ' || u == '\r' || u == '\n' || u == '\t' {
		e := p
		for e < n && (in[e] == ' ' || in[e] == '\r' || in[e] == '\n' || in[e] == '\t') {
			e++
		}
		return " ", e, true
	}
	// (2) \\(\n|[ \r\t]+\n?)[ \r\t]*
	if u == '\\' && p+1 < n {
		e := p + 1
		matched := false
		if in[e] == '\n' {
			e++
			matched = true
		} else if in[e] == ' ' || in[e] == '\r' || in[e] == '\t' {
			for e < n && (in[e] == ' ' || in[e] == '\r' || in[e] == '\t') {
				e++
			}
			if e < n && in[e] == '\n' {
				e++
			}
			matched = true
		}
		if matched {
			for e < n && (in[e] == ' ' || in[e] == '\r' || in[e] == '\t') {
				e++
			}
			return "\\ ", e, true
		}
	}
	// (3) single code point plus combining marks
	if (u >= 0x21 && u <= 0x5b) || (u >= 0x5d && u <= 0x2027) || (u >= 0x202a && u <= 0xd7ff) || (u >= 0xf900) {
		e := p + 1
		for e < n && tmIsCombining(in[e]) {
			e++
		}
		return tmU16s(in[p:e]), e, true
	}
	// surrogate pair plus combining marks
	if u >= 0xd800 && u <= 0xdbff && p+1 < n && in[p+1] >= 0xdc00 && in[p+1] <= 0xdfff {
		e := p + 2
		for e < n && tmIsCombining(in[e]) {
			e++
		}
		return tmU16s(in[p:e]), e, true
	}
	if u == '\\' {
		// \verb*
		if p+6 < n && tmUnitsEqual(in[p+1:p+6], "verb*") {
			if e, ok := tmVerbEnd(in, p+6, false); ok {
				return tmU16s(in[p:e]), e, true
			}
		}
		// \verb unstarred
		if p+5 < n && tmUnitsEqual(in[p+1:p+5], "verb") && in[p+5] != '*' && !(in[p+5] >= 'a' && in[p+5] <= 'z') && !(in[p+5] >= 'A' && in[p+5] <= 'Z') {
			if e, ok := tmVerbEnd(in, p+5, true); ok {
				return tmU16s(in[p:e]), e, true
			}
		}
		// \macroName
		e := p + 1
		for e < n && tmIsLetterOrAt(in[e]) {
			e++
		}
		if e > p+1 {
			word := tmU16s(in[p:e])
			for e < n && (in[e] == ' ' || in[e] == '\r' || in[e] == '\n' || in[e] == '\t') {
				e++
			}
			return word, e, true
		}
		// \\ followed by any code unit that is not a surrogate
		if p+1 < n && !(in[p+1] >= 0xd800 && in[p+1] <= 0xdfff) {
			return tmU16s(in[p : p+2]), p + 2, true
		}
	}
	return "", 0, false
}

func tmUnitsEqual(u []uint16, s string) bool {
	if len(u) != len(s) {
		return false
	}
	for i := range u {
		if u[i] != uint16(s[i]) {
			return false
		}
	}
	return true
}

// tmVerbEnd matches ([^]).*?\1 with the delimiter at p: the end of the match.
func tmVerbEnd(in []uint16, p int, _ bool) (int, bool) {
	if p >= len(in) {
		return 0, false
	}
	c := in[p]
	for j := p + 1; ; j++ {
		if j >= len(in) {
			return 0, false
		}
		if in[j] == c {
			return j + 1, true
		}
		if tmIsLineTerminator(in[j]) {
			return 0, false
		}
	}
}

func (l *tmLexer) lex() *tmToken {
	for {
		pos := l.pos
		if pos == len(l.input) {
			return &tmToken{text: "EOF", loc: &tmLoc{l, pos, pos}}
		}
		text, end, ok := l.matchAt(pos)
		if !ok {
			// the regex finds nothing at pos: "Unexpected character"
			tmThrow("Unexpected character: '"+tmU16(l.input[pos])+"'", &tmToken{text: tmU16(l.input[pos]), loc: &tmLoc{l, pos, pos + 1}})
		}
		l.pos = end
		if l.catcodes[text] == 14 && hasCatcode(l.catcodes, text) {
			nl := -1
			for i := end; i < len(l.input); i++ {
				if l.input[i] == '\n' {
					nl = i
					break
				}
			}
			if nl == -1 {
				l.pos = len(l.input)
				if l.settings.strict {
					tmThrowf("% comment has no terminating newline; LaTeX would fail because of commenting the end of math mode")
				}
			} else {
				l.pos = nl + 1
			}
			continue
		}
		return &tmToken{text: text, loc: &tmLoc{l, pos, end}}
	}
}

func hasCatcode(m map[string]int, k string) bool { _, ok := m[k]; return ok }

// ---- Namespace -----------------------------------------------------------------------------------------------

type tmNamespace struct {
	current   map[string]any
	builtins  map[string]any
	undefStack []map[string]any
}

func tmNewNamespace(builtins, global map[string]any) *tmNamespace {
	return &tmNamespace{current: global, builtins: builtins}
}

func (n *tmNamespace) beginGroup() { n.undefStack = append(n.undefStack, map[string]any{}) }

func (n *tmNamespace) endGroup() {
	if len(n.undefStack) == 0 {
		tmThrowf("Unbalanced namespace destruction: attempt to pop global namespace; please report this as a bug")
	}
	undefs := n.undefStack[len(n.undefStack)-1]
	n.undefStack = n.undefStack[:len(n.undefStack)-1]
	for k, v := range undefs {
		if v == nil {
			delete(n.current, k)
		} else {
			n.current[k] = v
		}
	}
}

func (n *tmNamespace) has(name string) bool {
	if _, ok := n.current[name]; ok {
		return true
	}
	_, ok := n.builtins[name]
	return ok
}

// get is the current value of a name, nil if there is none.
func (n *tmNamespace) get(name string) any {
	if v, ok := n.current[name]; ok {
		return v
	}
	return n.builtins[name]
}

func (n *tmNamespace) set(name string, value any, global bool) {
	if global {
		for _, m := range n.undefStack {
			delete(m, name)
		}
		if len(n.undefStack) > 0 {
			n.undefStack[len(n.undefStack)-1][name] = value
		}
	} else if len(n.undefStack) > 0 {
		top := n.undefStack[len(n.undefStack)-1]
		if _, ok := top[name]; !ok {
			top[name] = n.current[name]
		}
	}
	n.current[name] = value
}

// ---- Macro expander --------------------------------------------------------------------------------------------

// tmMacroDef is an expansion: the tokens in reverse order, the number of arguments, delimiters and unexpandable.
type tmMacroDef struct {
	tokens       []*tmToken
	numArgs      int
	delimiters   [][]string
	unexpandable bool
}

// tmMacroFunc is a macro defined by a function: it returns a string or a *tmMacroDef.
type tmMacroFunc func(g *tmGullet) any

type tmGullet struct {
	settings       *tmSettings
	expansionCount int
	lexer          *tmLexer
	macros         *tmNamespace
	mode           string
	stack          []*tmToken
}

var tmImplicitCommands = map[string]bool{"^": true, "_": true, "\\limits": true, "\\nolimits": true}

func tmNewGullet(input string, settings *tmSettings, mode string) *tmGullet {
	g := &tmGullet{settings: settings, mode: mode}
	g.feed(input)
	g.macros = tmNewNamespace(tmBuiltinMacros(), settings.macros)
	return g
}

func (g *tmGullet) feed(input string)       { g.lexer = tmNewLexer(input, g.settings) }
func (g *tmGullet) switchMode(m string)     { g.mode = m }
func (g *tmGullet) beginGroup()             { g.macros.beginGroup() }
func (g *tmGullet) endGroup()               { g.macros.endGroup() }
func (g *tmGullet) pushToken(t *tmToken)    { g.stack = append(g.stack, t) }
func (g *tmGullet) pushTokens(ts []*tmToken) { g.stack = append(g.stack, ts...) }

func (g *tmGullet) future() *tmToken {
	if len(g.stack) == 0 {
		g.pushToken(g.lexer.lex())
	}
	return g.stack[len(g.stack)-1]
}

func (g *tmGullet) popToken() *tmToken {
	g.future()
	t := g.stack[len(g.stack)-1]
	g.stack = g.stack[:len(g.stack)-1]
	return t
}

// scanArgument returns nil where the original returns null.
func (g *tmGullet) scanArgument(optional bool) *tmToken {
	var start, end *tmToken
	var tokens []*tmToken
	if optional {
		g.consumeSpaces()
		if g.future().text != "[" {
			return nil
		}
		start = g.popToken()
		tokens, _, end = g.consumeArg([]string{"]"})
	} else {
		tokens, start, end = g.consumeArg(nil)
	}
	g.pushToken(&tmToken{text: "EOF", loc: end.loc})
	g.pushTokens(tokens)
	return &tmToken{text: "", loc: tmRange(start, end)}
}

func (g *tmGullet) consumeSpaces() {
	for {
		if g.future().text == " " {
			g.stack = g.stack[:len(g.stack)-1]
		} else {
			break
		}
	}
}

func (g *tmGullet) consumeArg(delims []string) (tokens []*tmToken, start, end *tmToken) {
	isDelimited := len(delims) > 0
	if !isDelimited {
		g.consumeSpaces()
	}
	start = g.future()
	var tok *tmToken
	depth, match := 0, 0
	for {
		tok = g.popToken()
		tokens = append(tokens, tok)
		if tok.text == "{" {
			depth++
		} else if tok.text == "}" {
			depth--
			if depth == -1 {
				tmThrow("Extra }", tok)
			}
		} else if tok.text == "EOF" {
			expected := "}"
			if isDelimited {
				expected = delims[match]
			}
			tmThrow("Unexpected end of input in a macro argument, expected '"+expected+"'", tok)
		}
		if isDelimited {
			if (depth == 0 || (depth == 1 && delims[match] == "{")) && tok.text == delims[match] {
				match++
				if match == len(delims) {
					tokens = tokens[:len(tokens)-match]
					break
				}
			} else {
				match = 0
			}
		}
		if !(depth != 0 || isDelimited) {
			break
		}
	}
	if start.text == "{" && tokens[len(tokens)-1].text == "}" {
		tokens = tokens[:len(tokens)-1]
		tokens = tokens[1:]
	}
	// reverse for the stack order
	rev := make([]*tmToken, len(tokens))
	for i, t := range tokens {
		rev[len(tokens)-1-i] = t
	}
	return rev, start, tok
}

func (g *tmGullet) consumeArgs(numArgs int, delimiters [][]string) [][]*tmToken {
	if delimiters != nil {
		if len(delimiters) != numArgs+1 {
			tmThrowf("The length of delimiters doesn't match the number of args!")
		}
		for _, d := range delimiters[0] {
			tok := g.popToken()
			if d != tok.text {
				tmThrow("Use of the macro doesn't match its definition", tok)
			}
		}
	}
	args := make([][]*tmToken, 0, numArgs)
	for i := 0; i < numArgs; i++ {
		var d []string
		if delimiters != nil {
			d = delimiters[i+1]
		}
		toks, _, _ := g.consumeArg(d)
		args = append(args, toks)
	}
	return args
}

// expandOnce returns the number of tokens pushed, or -1 for `false`.
func (g *tmGullet) expandOnce(expandableOnly bool) int {
	top := g.popToken()
	name := top.text
	var exp *tmMacroDef
	if !top.noexpand {
		exp = g.getExpansion(name)
	}
	if exp == nil || (expandableOnly && exp.unexpandable) {
		if expandableOnly && exp == nil && name[0] == '\\' && !g.isDefined(name) {
			tmThrowf("Undefined control sequence: " + name)
		}
		g.pushToken(top)
		return -1
	}
	g.expansionCount++
	if float64(g.expansionCount) > g.settings.maxExpand {
		tmThrowf("Too many expansions: infinite loop or need to increase maxExpand setting")
	}
	tokens := exp.tokens
	args := g.consumeArgs(exp.numArgs, exp.delimiters)
	if exp.numArgs != 0 {
		tokens = append([]*tmToken(nil), tokens...)
		for i := len(tokens) - 1; i >= 0; i-- {
			tok := tokens[i]
			if tok.text == "#" {
				if i == 0 {
					tmThrow("Incomplete placeholder at end of macro body", tok)
				}
				i--
				tok = tokens[i]
				if tok.text == "#" {
					tokens = append(tokens[:i+1], tokens[i+2:]...)
				} else if len(tok.text) == 1 && tok.text[0] >= '1' && tok.text[0] <= '9' {
					arg := args[int(tok.text[0]-'0')-1]
					rest := append([]*tmToken(nil), tokens[i+2:]...)
					tokens = append(append(tokens[:i], arg...), rest...)
				} else {
					tmThrow("Not a valid argument number", tok)
				}
			}
		}
	}
	g.pushTokens(tokens)
	return len(tokens)
}

func (g *tmGullet) expandAfterFuture() *tmToken {
	g.expandOnce(false)
	return g.future()
}

func (g *tmGullet) expandNextToken() *tmToken {
	for {
		if g.expandOnce(false) == -1 {
			t := g.stack[len(g.stack)-1]
			g.stack = g.stack[:len(g.stack)-1]
			if t.treatAsRelax {
				t.text = "\\relax"
			}
			return t
		}
	}
}

func (g *tmGullet) expandMacro(name string) ([]*tmToken, bool) {
	if g.macros.has(name) {
		return g.expandTokens([]*tmToken{{text: name}}), true
	}
	return nil, false
}

func (g *tmGullet) expandTokens(tokens []*tmToken) []*tmToken {
	var out []*tmToken
	old := len(g.stack)
	g.pushTokens(tokens)
	for len(g.stack) > old {
		if g.expandOnce(true) == -1 {
			t := g.stack[len(g.stack)-1]
			g.stack = g.stack[:len(g.stack)-1]
			if t.treatAsRelax {
				t.noexpand = false
				t.treatAsRelax = false
			}
			out = append(out, t)
		}
	}
	return out
}

func (g *tmGullet) expandMacroAsText(name string) (string, bool) {
	toks, ok := g.expandMacro(name)
	if !ok {
		return "", false
	}
	var b strings.Builder
	for _, t := range toks {
		b.WriteString(t.text)
	}
	return b.String(), true
}

// getExpansion is _getExpansion: nil when the name has no expansion.
func (g *tmGullet) getExpansion(name string) *tmMacroDef {
	definition := g.macros.get(name)
	if definition == nil {
		return nil
	}
	if jsLen(name) == 1 {
		if code, ok := g.lexer.catcodes[name]; ok && code != 13 {
			return nil
		}
	}
	var expansion any = definition
	if f, ok := definition.(tmMacroFunc); ok {
		expansion = f(g)
	}
	switch e := expansion.(type) {
	case string:
		numArgs := 0
		if strings.Contains(e, "#") {
			stripped := strings.ReplaceAll(e, "##", "")
			for strings.Contains(stripped, "#"+strconv.Itoa(numArgs+1)) {
				numArgs++
			}
		}
		bodyLexer := tmNewLexer(e, g.settings)
		var tokens []*tmToken
		tok := bodyLexer.lex()
		for tok.text != "EOF" {
			tokens = append(tokens, tok)
			tok = bodyLexer.lex()
		}
		for i, j := 0, len(tokens)-1; i < j; i, j = i+1, j-1 {
			tokens[i], tokens[j] = tokens[j], tokens[i]
		}
		return &tmMacroDef{tokens: tokens, numArgs: numArgs}
	case *tmMacroDef:
		return e
	}
	panic(&tmJSError{"invalid macro expansion"})
}

func (g *tmGullet) isDefined(name string) bool {
	if g.macros.has(name) {
		return true
	}
	if _, ok := tmFunctions[name]; ok {
		return true
	}
	if _, ok := tmSymbolsMath[name]; ok {
		return true
	}
	if _, ok := tmSymbolsText[name]; ok {
		return true
	}
	return tmImplicitCommands[name]
}

func (g *tmGullet) isExpandable(name string) bool {
	macro := g.macros.get(name)
	if macro != nil {
		switch m := macro.(type) {
		case string, tmMacroFunc:
			return true
		case *tmMacroDef:
			return !m.unexpandable
		}
		return true
	}
	f, ok := tmFunctions[name]
	return ok && !f.primitive
}
