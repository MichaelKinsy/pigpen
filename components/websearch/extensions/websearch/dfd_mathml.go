package websearch

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Port of mathml-to-latex 1.8.0 (MIT): MathMLToLaTeX.convert(mathml) for dist/index.cjs. The parser is the
// xmldom port of dfd_xmldom.go; the lookup tables are generated (dfd_mathml_tables.go, port/gen-defuddle-mathml-tables.mjs).

type mlEl struct {
	name     string
	value    string
	attrs    map[string]string
	children []*mlEl
}

func (e *mlEl) attr(name string) (string, bool) {
	v, ok := e.attrs[name]
	return v, ok
}

// attrTruthy is the value of attributes[name] as a condition: a missing attribute and "" are false.
func (e *mlEl) attrOr(name string) string { return e.attrs[name] }

func mlThrow(msg string) { panic(&xJSError{msg}) }

// dfdMathMLToLatex is MathMLToLaTeX.convert(mathml) inside the try/catch of its callers: ok is false when it threw.
func dfdMathMLToLatex(mathml string) (out string, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			switch r.(type) {
			case *xJSError, *xParseError, *xDOMException:
				out, ok = "", false
			default:
				panic(r)
			}
		}
	}()
	roots := mlParse(mathml)
	var b strings.Builder
	for _, r := range roots {
		b.WriteString(mlConvertTree(r))
	}
	return jsTrim(b.String()), true
}

// ---- parsing (XmlToMathMLAdapter, ErrorHandler, ElementsToMathMLAdapter) ----------------------------------------

var (
	reMLLineBreaks   = regexp.MustCompile(`[\r\n]`)
	reMLMissingAttr  = regexp.MustCompile(`attribute "([^"]+)" missed value`)
	reMLGenericValue = regexp.MustCompile(`(<[^\n\r\x{2028}\x{2029}]* )(\w+=)((?:[^"'\n\r\x{2028}\x{2029}][^\n\r\x{2028}\x{2029}]*>|>))`)
)

func mlParse(xml string) []*mlEl {
	xml = reMLLineBreaks.ReplaceAllLiteralString(xml, "")
	xml = strings.ReplaceAll(xml, "mml:", "")
	var errors []string
	cur := xml
	onError := func(_ string, message string) {
		if !mlIsMissingAttributeValueError(message) {
			return
		}
		errors = append(errors, message)
		cur = mlFixMissingAttribute(message, cur)
	}
	doc := xParse(cur, onError)
	if len(errors) > 0 {
		errors = nil
		doc = xParse(cur, onError)
	}
	if doc == nil {
		return nil
	}
	var els []*xNode
	var visit func(*xNode)
	visit = func(n *xNode) {
		for _, c := range n.children {
			if c.kind == xElement {
				if c.name == "math" {
					els = append(els, c)
				}
				visit(c)
			}
		}
	}
	visit(doc)
	roots := make([]*mlEl, 0, len(els))
	for _, el := range els {
		roots = append(roots, mlBuild(el))
	}
	return roots
}

func mlIsMissingAttributeValueError(m string) bool {
	return (strings.Contains(m, "attribute") && strings.Contains(m, "missed")) ||
		strings.Contains(m, "attribute value missed") ||
		strings.Contains(m, `AttValue: ' or " expected`)
}

func mlFixMissingAttribute(message, xml string) string {
	if m := reMLMissingAttr.FindStringSubmatch(message); m != nil {
		return mlStripAttribute(xml, m[1])
	}
	for reMLGenericValue.MatchString(xml) {
		xml = reMLGenericValue.ReplaceAllString(xml, "${1}${3}")
	}
	return xml
}

// mlStripAttribute is xml.replace(new RegExp(`(${a}=(?!("|')))|(${a}(?!("|')))`, 'gm'), ''): the attribute name is
// used as a pattern, where only "." (any character but a line terminator) is special in a name.
func mlStripAttribute(xml, attr string) string {
	rs := []rune(xml)
	ar := []rune(attr)
	matchAt := func(i int) int {
		if i+len(ar) > len(rs) {
			return -1
		}
		for k, c := range ar {
			r := rs[i+k]
			if c == '.' {
				if r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 {
					return -1
				}
			} else if c != r {
				return -1
			}
		}
		return len(ar)
	}
	isQuote := func(i int) bool { return i < len(rs) && (rs[i] == '"' || rs[i] == '\'') }
	var out []rune
	for i := 0; i < len(rs); {
		l := matchAt(i)
		if l < 0 {
			out = append(out, rs[i])
			i++
			continue
		}
		n := 0
		if i+l < len(rs) && rs[i+l] == '=' && !isQuote(i+l+1) {
			n = l + 1
		} else if !isQuote(i + l) {
			n = l
		}
		if n == 0 {
			out = append(out, rs[i])
			i++
			continue
		}
		i += n
	}
	return string(out)
}

func mlBuild(el *xNode) *mlEl {
	node := mlConvertElement(el)
	if mlHasElementChild(el) {
		for _, c := range el.children {
			if c.kind == xElement {
				node.children = append(node.children, mlBuild(c))
			}
		}
	}
	return node
}

func mlHasElementChild(el *xNode) bool {
	if len(el.children) == 0 {
		return false
	}
	for _, c := range el.children {
		if c.kind != xText {
			return true
		}
	}
	return false
}

func mlConvertElement(el *xNode) *mlEl {
	e := &mlEl{name: el.name, attrs: map[string]string{}}
	for _, a := range el.attrs {
		v := a.value
		if v == a.qName {
			v = ""
		}
		if _, ok := e.attrs[a.qName]; !ok {
			e.attrs[a.qName] = v
		}
	}
	if !mlHasElementChild(el) {
		e.value = el.textContent()
	}
	return e
}

// ---- tree normalizers --------------------------------------------------------------------------------------

func mlConvertTree(root *mlEl) string {
	mlNormalizeFences(root)
	mlAnnotateTables(root)
	memo := map[*mlEl]string{}
	type frame struct {
		node    *mlEl
		visited bool
	}
	stack := []frame{{root, false}}
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if !f.visited {
			f.visited = true
			node := f.node
			for i := len(node.children) - 1; i >= 0; i-- {
				stack = append(stack, frame{node.children[i], false})
			}
			continue
		}
		node := f.node
		stack = stack[:len(stack)-1]
		memo[node] = mlConvertNode(node, memo)
	}
	return memo[root]
}

var mlOpeners = map[string]bool{"(": true, "[": true, "{": true}
var mlClosers = map[string]bool{")": true, "]": true, "}": true}
var mlBars = map[string]bool{"|": true, "||": true, "‖": true, "∥": true}
var mlScriptElements = map[string]bool{"msub": true, "msup": true, "msubsup": true}

type mlFenceFrame struct {
	opener  *mlEl
	content []*mlEl
}

func mlNormalizeFences(root *mlEl) {
	stack := []*mlEl{root}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if len(node.children) == 0 {
			continue
		}
		if paired, changed := mlPairFences(node.children); changed {
			node.children = paired
		}
		stack = append(stack, node.children...)
	}
}

func mlIsMo(e *mlEl, set map[string]bool) bool {
	return e.name == "mo" && set[jsTrim(e.value)]
}

func mlClosesBar(child *mlEl, top *mlFenceFrame) bool {
	if top == nil || !mlIsMo(child, mlBars) || !mlIsMo(top.opener, mlBars) {
		return false
	}
	return jsTrim(top.opener.value) == jsTrim(child.value)
}

func mlOpensFrame(child *mlEl, top *mlFenceFrame) bool {
	if mlIsMo(child, mlOpeners) {
		return true
	}
	return mlIsMo(child, mlBars) && !mlClosesBar(child, top)
}

func mlClosesFrame(child *mlEl, top *mlFenceFrame) bool {
	if top == nil {
		return false
	}
	if mlIsMo(child, mlClosers) && mlIsMo(top.opener, mlOpeners) {
		return true
	}
	return mlClosesBar(child, top)
}

func mlMakeFence(opener, closer *mlEl, content []*mlEl) *mlEl {
	var child *mlEl
	if len(content) == 1 {
		child = content[0]
	} else {
		child = &mlEl{name: "mrow", attrs: map[string]string{}, children: content}
	}
	return &mlEl{name: "mfenced", attrs: map[string]string{"open": jsTrim(opener.value), "close": jsTrim(closer.value)}, children: []*mlEl{child}}
}

func mlScriptBaseCloser(child *mlEl, top *mlFenceFrame) *mlEl {
	if top == nil || !mlIsMo(top.opener, mlBars) {
		return nil
	}
	if !mlScriptElements[child.name] || len(child.children) == 0 {
		return nil
	}
	base := child.children[0]
	bar := base
	if base.name == "mrow" && len(base.children) == 1 {
		bar = base.children[0]
	}
	if bar.name != "mo" || jsTrim(bar.value) != jsTrim(top.opener.value) {
		return nil
	}
	return bar
}

func mlPairFences(children []*mlEl) ([]*mlEl, bool) {
	var root []*mlEl
	var openFrames []*mlFenceFrame
	// current is the list being appended to: the root, or the content of the innermost open frame.
	push := func(n *mlEl) {
		if len(openFrames) > 0 {
			f := openFrames[len(openFrames)-1]
			f.content = append(f.content, n)
		} else {
			root = append(root, n)
		}
	}
	paired := false
	for _, child := range children {
		var top *mlFenceFrame
		if len(openFrames) > 0 {
			top = openFrames[len(openFrames)-1]
		}
		if mlOpensFrame(child, top) {
			openFrames = append(openFrames, &mlFenceFrame{opener: child})
			continue
		}
		if mlClosesFrame(child, top) {
			openFrames = openFrames[:len(openFrames)-1]
			push(mlMakeFence(top.opener, child, top.content))
			paired = true
			continue
		}
		if sc := mlScriptBaseCloser(child, top); sc != nil {
			openFrames = openFrames[:len(openFrames)-1]
			fence := mlMakeFence(top.opener, sc, top.content)
			cp := *child
			cp.children = append([]*mlEl{fence}, child.children[1:]...)
			push(&cp)
			paired = true
			continue
		}
		push(child)
	}
	if !paired {
		return children, false
	}
	for _, f := range openFrames {
		root = append(root, f.opener)
		root = append(root, f.content...)
	}
	return root, true
}

func mlLinearSystemMatches(e *mlEl) bool {
	if e.name != "mrow" {
		return false
	}
	n := len(e.children)
	if n != 2 && n != 3 {
		return false
	}
	c0, c1 := e.children[0], e.children[1]
	if !(c0.name == "mo" && jsTrim(c0.value) == "{") || c1.name != "mtable" {
		return false
	}
	if n == 2 {
		return true
	}
	c2 := e.children[2]
	return c2.name == "mo" && jsTrim(c2.value) == ""
}

func mlAnnotateTables(root *mlEl) {
	type frame struct {
		node                *mlEl
		insideMtable, wrapped bool
	}
	stack := []frame{{root, false, false}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.node.name == "mtable" {
			if f.insideMtable {
				f.node.attrs["innerTable"] = "innerTable"
			} else if !f.wrapped {
				f.node.attrs["bareTable"] = "bareTable"
			}
		}
		if f.node.name == "mtr" && !f.insideMtable {
			f.node.attrs["bareRow"] = "bareRow"
		}
		childInside := f.insideMtable || f.node.name == "mtable"
		childWrapped := f.wrapped || f.node.name == "mfenced"
		var linear *mlEl
		if mlLinearSystemMatches(f.node) {
			linear = f.node.children[1]
		}
		for _, c := range f.node.children {
			stack = append(stack, frame{c, childInside, childWrapped || c == linear})
		}
	}
}

// ---- converters ------------------------------------------------------------------------------------------

// The names of Object.prototype members: looked up in the converter registry they find a non-converter and the
// conversion throws.
var mlPrototypeNames = map[string]bool{
	"constructor": true, "__defineGetter__": true, "__defineSetter__": true, "hasOwnProperty": true, "__lookupGetter__": true,
	"__lookupSetter__": true, "isPrototypeOf": true, "propertyIsEnumerable": true, "toString": true, "valueOf": true,
	"__proto__": true, "toLocaleString": true,
}

func mlChild(memo map[*mlEl]string, e *mlEl) string {
	if e == nil {
		return ""
	}
	return memo[e]
}

func mlJoined(e *mlEl, memo map[*mlEl]string, sep string) string {
	parts := make([]string, len(e.children))
	for i, c := range e.children {
		parts[i] = memo[c]
	}
	return strings.Join(parts, sep)
}

func mlNeed(e *mlEl, n int) {
	if len(e.children) != n {
		mlThrow(e.name + " tag must have exactly " + mlItoa(n) + " children. It's actually " + mlItoa(len(e.children)))
	}
}

func mlItoa(n int) string { return strconv.Itoa(n) }

func mlConvertNode(e *mlEl, memo map[*mlEl]string) string {
	if mlPrototypeNames[e.name] {
		mlThrow("Converter is not a constructor")
	}
	switch e.name {
	case "math":
		return jsCollapseWS(mlJoined(e, memo, " "))
	case "mi":
		return mlMI(e.value, e.attrOr("mathvariant"))
	case "mo":
		return mlOperate(jsTrim(jsCollapseWS(e.value)))
	case "mn":
		v := jsTrim(jsCollapseWS(e.value))
		if c := dfdMLNumberByGlyph[v]; c != "" {
			return c
		}
		return v
	case "msqrt":
		return "\\sqrt{" + mlJoined(e, memo, " ") + "}"
	case "mfenced":
		return mlMFenced(e, memo)
	case "mfrac":
		return mlMFrac(e, memo)
	case "mroot":
		mlNeed(e, 2)
		return "\\sqrt[" + memo[e.children[1]] + "]{" + memo[e.children[0]] + "}"
	case "maction":
		if a := e.attrOr("actiontype"); a == "toggle" || a == "" {
			return mlJoined(e, memo, " \\Longrightarrow ")
		}
		if len(e.children) == 0 {
			return ""
		}
		return memo[e.children[0]]
	case "menclose":
		return mlMEnclose(e, mlJoined(e, memo, " "))
	case "merror":
		return "\\color{red}{" + mlJoined(e, memo, " ") + "}"
	case "mphantom":
		return ""
	case "msup":
		mlNeed(e, 2)
		return mlBase(e.children[0], memo) + "^{" + memo[e.children[1]] + "}"
	case "msub":
		mlNeed(e, 2)
		return mlBase(e.children[0], memo) + "_{" + memo[e.children[1]] + "}"
	case "msubsup":
		mlNeed(e, 3)
		return mlBase(e.children[0], memo) + "_{" + memo[e.children[1]] + "}^{" + memo[e.children[2]] + "}"
	case "mmultiscripts":
		return mlMultiscripts(e, memo)
	case "mtext":
		return mlMText(e)
	case "munderover":
		mlNeed(e, 3)
		return memo[e.children[0]] + "_{" + memo[e.children[1]] + "}^{" + memo[e.children[2]] + "}"
	case "mtable":
		return mlMTable(e, memo)
	case "mtr":
		row := mlJoined(e, memo, " & ")
		if e.attrOr("bareRow") != "" {
			return "\\begin{matrix}" + row + "\\end{matrix}"
		}
		return row
	case "mover", "munder":
		mlNeed(e, 2)
		content, accent := memo[e.children[0]], memo[e.children[1]]
		if accent == "\\hat" || accent == "\\bar" || accent == "\\underbrace" || accent == "\\overbrace" {
			return accent + "{" + content + "}"
		}
		cmd := "\\overset"
		if strings.Contains(e.name, "under") {
			cmd = "\\underset"
		}
		return cmd + "{" + accent + "}{" + content + "}"
	case "mrow":
		if mlLinearSystemMatches(e) {
			return "\\begin{cases} " + mlJoined(e.children[1], memo, " \\\\ ") + " \\end{cases}"
		}
		return mlJoined(e, memo, " ")
	case "mspace":
		if e.attrOr("linebreak") == "newline" {
			return " \\\\ "
		}
		return " "
	case "void":
		return ""
	}
	return mlJoined(e, memo, " ")
}

func mlBase(base *mlEl, memo map[*mlEl]string) string {
	s := memo[base]
	if len(base.children) <= 1 {
		return s
	}
	return mlWrapIfMoreThanOneChar(s)
}

func mlWrapIfMoreThanOneChar(s string) string {
	if jsLen(s) <= 1 {
		return s
	}
	return "\\left(" + s + "\\right)"
}

var reMLAlnum = regexp.MustCompile(`[a-zA-Z0-9]`)

func mlUTF8Convert(char string) string {
	if reMLAlnum.MatchString(char) {
		return char
	}
	if ac, ok := dfdMLVowelsWithAccents[char]; ok {
		if cmd, ok := dfdMLAccentToCmd[ac.accent]; ok && cmd != "" {
			return "\\" + cmd + "{" + ac.char + "}"
		}
		return char
	}
	if sf, ok := dfdMLSpecialFontChars[char]; ok {
		return "\\" + sf.fontCmd + "{" + sf.letter + "}"
	}
	return char
}

func mlCharacter(v string) string {
	if r, ok := dfdMLSymbolsByChar[v]; ok {
		return r
	}
	if r, ok := dfdMLSymbolsByGlyph[v]; ok {
		return r
	}
	if r, ok := dfdMLNumberByGlyph[v]; ok {
		return r
	}
	return mlUTF8Convert(v)
}

func mlOperate(v string) string {
	if r, ok := dfdMLOperatorsByChar[v]; ok {
		return r
	}
	if r, ok := dfdMLOperatorsByGlyph[v]; ok {
		return r
	}
	if r, ok := dfdMLNumberByGlyph[v]; ok {
		return r
	}
	return mlUTF8Convert(v)
}

func mlMI(value, variant string) string {
	normalized := jsCollapseWS(value)
	if normalized == " " {
		return mlCharacter(normalized)
	}
	trimmed := jsTrim(normalized)
	converted := mlCharacter(trimmed)
	parsed := mlUTF8Convert(converted)
	if parsed != converted {
		return parsed
	}
	switch variant {
	case "bold":
		return "\\mathbf{" + converted + "}"
	case "italic":
		return "\\mathit{" + converted + "}"
	case "bold-italic":
		return "\\mathbf{\\mathit{" + converted + "}}"
	case "double-struck":
		return "\\mathbb{" + converted + "}"
	case "bold-fraktur":
		return "\\mathbf{\\mathfrak{" + converted + "}}"
	case "script":
		return "\\mathcal{" + converted + "}"
	case "bold-script":
		return "\\mathbf{\\mathcal{" + converted + "}}"
	case "fraktur":
		return "\\mathfrak{" + converted + "}"
	case "sans-serif":
		return "\\mathsf{" + converted + "}"
	case "bold-sans-serif":
		return "\\mathbf{\\mathsf{" + converted + "}}"
	case "sans-serif-italic":
		return "\\mathsf{\\mathit{" + converted + "}}"
	case "sans-serif-bold-italic":
		return "\\mathbf{\\mathsf{\\mathit{" + converted + "}}}"
	case "monospace":
		return "\\mathtt{" + converted + "}"
	}
	return converted
}

func mlMText(e *mlEl) string {
	type run struct {
		value string
		alnum bool
	}
	var runs []run
	for _, ch := range e.value {
		s := string(ch)
		alnum := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == ' '
		if alnum && len(runs) > 0 && runs[len(runs)-1].alnum {
			runs[len(runs)-1].value += s
			continue
		}
		runs = append(runs, run{s, alnum})
	}
	var b strings.Builder
	for _, r := range runs {
		if !r.alnum {
			b.WriteString(mlMI(r.value, ""))
			continue
		}
		var cmds []string
		switch e.attrOr("mathvariant") {
		case "bold":
			cmds = []string{"\\textbf"}
		case "italic":
			cmds = []string{"\\textit"}
		case "bold-italic":
			cmds = []string{"\\textit", "\\textbf"}
		case "double-struck":
			cmds = []string{"\\mathbb"}
		case "monospace":
			cmds = []string{"\\mathtt"}
		case "bold-fraktur", "fraktur":
			cmds = []string{"\\mathfrak"}
		default:
			cmds = []string{"\\text"}
		}
		acc := ""
		for i, c := range cmds {
			if i == 0 {
				acc = c + "{" + r.value + "}"
			} else {
				acc = c + "{" + acc + "}"
			}
		}
		b.WriteString(acc)
	}
	return b.String()
}

func mlMEnclose(e *mlEl, joined string) string {
	notation := e.attrOr("notation")
	if notation == "" {
		notation = "longdiv"
	}
	switch notation {
	case "actuarial":
		return "\\overline{\\left." + joined + "\\right|}"
	case "radical":
		return "\\sqrt{" + joined + "}"
	case "box", "roundedbox", "circle":
		return "\\boxed{" + joined + "}"
	case "left":
		return "\\left|" + joined
	case "right":
		return joined + "\\right|"
	case "top":
		return "\\overline{" + joined + "}"
	case "bottom":
		return "\\underline{" + joined + "}"
	case "updiagonalstrike":
		return "\\cancel{" + joined + "}"
	case "downdiagonalstrike":
		return "\\bcancel{" + joined + "}"
	case "updiagonalarrow":
		return "\\cancelto{}{" + joined + "}"
	case "verticalstrike", "horizontalstrike":
		return "\\hcancel{" + joined + "}"
	case "madruwb":
		return "\\underline{" + joined + "\\right|}"
	case "phasorangle":
		return "{\\angle \\underline{" + joined + "}}"
	}
	return "\\overline{\\left.\\right)" + joined + "}"
}

func mlMultiscripts(e *mlEl, memo map[*mlEl]string) string {
	if len(e.children) == 0 {
		mlThrow(e.name + " tag must have at least 1 children. It's actually 0")
	}
	base := memo[e.children[0]]
	if base == "" {
		base = "{}"
	} else if jsLen(base) != 1 {
		base = "{" + base + "}"
	}
	scripts := e.children[1:]
	marker := -1
	for i, c := range scripts {
		if c.name == "mprescripts" {
			marker = i
			break
		}
	}
	post, pre := scripts, []*mlEl(nil)
	if marker != -1 {
		post, pre = scripts[:marker], scripts[marker+1:]
	}
	scriptLatex := func(i int, list []*mlEl) string {
		if i >= len(list) || list[i].name == "none" {
			return ""
		}
		return memo[list[i]]
	}
	pairs := func(list []*mlEl, isPre bool) string {
		out := ""
		attaches := !isPre
		for i := 0; i < len(list); i += 2 {
			sub, sup := scriptLatex(i, list), scriptLatex(i+1, list)
			s := ""
			if sub != "" {
				s += "_{" + sub + "}"
			}
			if sup != "" {
				s += "^{" + sup + "}"
			}
			if s == "" {
				continue
			}
			if !attaches {
				out += "{}"
			}
			out += s
			attaches = false
		}
		return out
	}
	return pairs(pre, true) + base + pairs(post, false)
}

func mlMTable(e *mlEl, memo map[*mlEl]string) string {
	content := mlJoined(e, memo, " \\\\\n")
	wrap := func(env string) string { return "\\begin{" + env + "}" + content + "\\end{" + env + "}" }
	if e.attrOr("innerTable") != "" {
		return wrap("matrix")
	}
	if e.attrOr("bareTable") != "" {
		if mlEquationAlignment(e) {
			return wrap("aligned")
		}
		return wrap("matrix")
	}
	return content
}

func mlEquationAlignment(table *mlEl) bool {
	var rows []*mlEl
	for _, c := range table.children {
		if c.name == "mtr" {
			rows = append(rows, c)
		}
	}
	all := []*mlEl{table}
	all = append(all, rows...)
	for _, r := range rows {
		for _, c := range r.children {
			if c.name == "mtd" {
				all = append(all, c)
			}
		}
	}
	hasRight, hasLeft := false, false
	for _, el := range all {
		v := el.attrOr("columnalign")
		if v == "" {
			continue
		}
		for _, tok := range jsSplitWS(jsTrim(v)) {
			if tok == "right" {
				hasRight = true
			}
			if tok == "left" {
				hasLeft = true
			}
		}
	}
	return hasRight && hasLeft
}

var mlDelimiterTranslations = map[string]string{"{": "\\{", "}": "\\}", "||": "\\|", "‖": "\\|", "∥": "\\|"}

func mlGenericWrap(open, close, s string) string {
	tr := func(d string) string {
		if v, ok := mlDelimiterTranslations[d]; ok {
			return v
		}
		return d
	}
	return "\\left" + tr(open) + s + "\\right" + tr(close)
}

func mlHasDescendant(list []*mlEl, name string) bool {
	stack := append([]*mlEl(nil), list...)
	for len(stack) > 0 {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if e.name == name {
			return true
		}
		stack = append(stack, e.children...)
	}
	return false
}

func mlMFenced(e *mlEl, memo map[*mlEl]string) string {
	open, close := e.attrOr("open"), e.attrOr("close")
	latex := make([]string, len(e.children))
	for i, c := range e.children {
		latex[i] = memo[c]
	}
	if mlHasDescendant(e.children, "mtable") {
		var command string
		switch {
		case open == "(" && close == ")":
			command = "pmatrix"
		case open == "[" && close == "]":
			command = "bmatrix"
		case open == "{" && close == "}":
			command = "Bmatrix"
		case open == "|" && close == "|":
			command = "vmatrix"
		case open == close && (open == "||" || open == "‖" || open == "∥"):
			command = "Vmatrix"
		case open != close:
			command = "matrix"
		default:
			command = "bmatrix"
		}
		m := "\\begin{" + command + "}\n" + strings.Join(latex, "") + "\n\\end{" + command + "}"
		if command == "matrix" {
			return mlGenericWrap(open, close, m)
		}
		return m
	}
	sepAttr, hasSep := e.attr("separators")
	var seps []string
	if sepAttr != "" {
		for _, r := range sepAttr {
			seps = append(seps, string(r))
		}
	}
	def := ""
	if !hasSep {
		def = ","
	}
	if len(seps) == 0 {
		seps = []string{def}
	}
	if open == "" {
		open = "("
	}
	if close == "" {
		close = ")"
	}
	var b strings.Builder
	for i, s := range latex {
		b.WriteString(s)
		if i != len(latex)-1 {
			if i < len(seps) && seps[i] != "" {
				b.WriteString(seps[i])
			} else {
				b.WriteString(seps[len(seps)-1])
			}
		}
	}
	return mlGenericWrap(open, close, b.String())
}

var reMLThickness = jsRE(`^(-?\d*\.?\d+)\s*([a-z%]*)$`, "")

func mlToLower(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.ReplaceAll(s, "\u0130", "\x00İ")), "\x00İ", "i\u0307")
}

func mlRound(x float64) float64 {
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	return r
}

func mlFormatPoints(v float64) string {
	return jsNumberString(mlRound(v*10000)/10000) + "pt"
}

// mlThickness is LineThickness.toLaTeX(): ok is false for null.
func mlThickness(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	value := mlToLower(jsTrim(raw))
	switch value {
	case "medium":
		return "", false
	case "thin":
		return mlFormatPoints(0.4 * 0.5), true
	case "thick":
		return mlFormatPoints(0.4 * 2), true
	}
	m := reMLThickness.FindStringSubmatch(value)
	if m == nil {
		return "", false
	}
	amount := jsParseFloat(m[1])
	unit := m[2]
	if amount == 0 {
		return "0pt", true
	}
	if unit == "" {
		if amount == 1 {
			return "", false
		}
		return mlFormatPoints(amount * 0.4), true
	}
	if unit == "px" {
		return mlFormatPoints(amount * 0.75), true
	}
	if unit == "%" {
		return mlFormatPoints(amount / 100 * 0.4), true
	}
	switch unit {
	case "pt", "pc", "in", "cm", "mm", "em", "ex", "bp", "dd", "cc":
		return jsNumberString(amount) + unit, true
	}
	return "", false
}

func mlMFrac(e *mlEl, memo map[*mlEl]string) string {
	mlNeed(e, 2)
	num, den := memo[e.children[0]], memo[e.children[1]]
	if e.attrOr("bevelled") != "" {
		return mlWrapIfMoreThanOneChar(num) + "/" + mlWrapIfMoreThanOneChar(den)
	}
	if t, ok := mlThickness(e.attrOr("linethickness")); ok {
		return "\\genfrac{}{}{" + t + "}{}{" + num + "}{" + den + "}"
	}
	return "\\frac{" + num + "}{" + den + "}"
}
