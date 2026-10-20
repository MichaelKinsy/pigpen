package websearch

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Temml 0.13.4 (MIT): the output trees (src/mathMLTree.js, domTree.js, tree.js), Style.js, units.js, utils.js, replace.js
// and stretchy.js.

// tmOrd is a JavaScript object used as an ordered dictionary: the keys of a for...in loop, in insertion order.
type tmOrd struct {
	keys []string
	vals map[string]any
}

func tmNewOrd() *tmOrd { return &tmOrd{vals: map[string]any{}} }

func (o *tmOrd) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *tmOrd) get(k string) any { return o.vals[k] }

func (o *tmOrd) has(k string) bool { _, ok := o.vals[k]; return ok }

func (o *tmOrd) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, kk := range o.keys {
		if kk == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *tmOrd) size() int { return len(o.keys) }

// jsStr is String(v) for the values the tree holds.
func jsStr(v any) string {
	switch x := v.(type) {
	case nil:
		return "undefined"
	case string:
		return x
	case float64:
		return jsNumberString(x)
	case int:
		return strconv.Itoa(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case []float64:
		parts := make([]string, len(x))
		for i, f := range x {
			parts[i] = jsNumberString(f)
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// tmTruthy is the JavaScript truthiness of a value.
func tmTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	case int:
		return x != 0
	}
	return true
}

func tmEscape(s string) string {
	if !strings.ContainsAny(s, `&><"'`) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '>':
			b.WriteString("&gt;")
		case '<':
			b.WriteString("&lt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#x27;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// tmHyphenate is hyphenate(str): "fontFamily" becomes "font-family".
func tmHyphenate(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('-')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// tmToFixed is Number.prototype.toFixed(digits) for a finite number.
func tmToFixed(x float64, digits int) string {
	if math.IsNaN(x) {
		return "NaN"
	}
	if math.IsInf(x, 0) || math.Abs(x) >= 1e21 {
		return jsNumberString(x)
	}
	neg := x < 0
	if neg {
		x = -x
	}
	r := new(big.Rat).SetFloat64(x)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	r.Mul(r, new(big.Rat).SetInt(scale))
	// n = floor(r + 1/2)
	r.Add(r, big.NewRat(1, 2))
	n := new(big.Int).Div(r.Num(), r.Denom())
	s := n.String()
	if digits > 0 {
		for len(s) <= digits {
			s = "0" + s
		}
		s = s[:len(s)-digits] + "." + s[len(s)-digits:]
	}
	if neg && (n.Sign() != 0) {
		s = "-" + s
	} else if neg {
		// -0.00 prints as "-0.00" in JavaScript (the sign of a negative number is kept)
		s = "-" + s
	}
	return s
}

// tmRound is utils.round: +n.toFixed(4).
func tmRound(n float64) float64 {
	f, _ := strconv.ParseFloat(tmToFixed(n, 4), 64)
	return f
}

// ---- output tree ----------------------------------------------------------------------------------------------

type mmNode interface {
	toMarkup() string
	toText() string
	childrenPtr() *[]mmNode
	classesPtr() *[]string
	styleOf() *tmOrd
	attrsOf() *tmOrd
}

type mathNode struct {
	typ      string
	attrs    *tmOrd
	children []mmNode
	classes  []string
	style    *tmOrd
	label    string
}

func newMathNode(typ string, children []mmNode, classes []string, style *tmOrd) *mathNode {
	if style == nil {
		style = tmNewOrd()
	}
	return &mathNode{typ: typ, attrs: tmNewOrd(), children: children, classes: classes, style: style}
}

func mn(typ string, children ...mmNode) *mathNode { return newMathNode(typ, children, nil, nil) }

func (n *mathNode) setAttribute(name string, value any) { n.attrs.set(name, value) }

func (n *mathNode) getAttribute(name string) any { return n.attrs.get(name) }

func (n *mathNode) childrenPtr() *[]mmNode { return &n.children }
func (n *mathNode) classesPtr() *[]string  { return &n.classes }
func (n *mathNode) styleOf() *tmOrd        { return n.style }
func (n *mathNode) attrsOf() *tmOrd        { return n.attrs }

func tmCreateClass(classes []string) string {
	var out []string
	for _, c := range classes {
		if c != "" {
			out = append(out, c)
		}
	}
	return strings.Join(out, " ")
}

func tmStyleMarkup(style *tmOrd) string {
	var b strings.Builder
	for _, k := range style.keys {
		b.WriteString(tmHyphenate(k) + ":" + jsStr(style.vals[k]) + ";")
	}
	return b.String()
}

func (n *mathNode) toMarkup() string {
	var b strings.Builder
	b.WriteString("<" + n.typ)
	for _, k := range n.attrs.keys {
		b.WriteString(" " + k + `="` + tmEscape(jsStr(n.attrs.vals[k])) + `"`)
	}
	if len(n.classes) > 0 {
		b.WriteString(` class="` + tmEscape(tmCreateClass(n.classes)) + `"`)
	}
	if styles := tmStyleMarkup(n.style); styles != "" {
		b.WriteString(` style="` + styles + `"`)
	}
	b.WriteString(">")
	for _, c := range n.children {
		b.WriteString(c.toMarkup())
	}
	b.WriteString("</" + n.typ + ">")
	return b.String()
}

func (n *mathNode) toText() string {
	var b strings.Builder
	for _, c := range n.children {
		b.WriteString(c.toText())
	}
	return b.String()
}

// textNode is TextNode of mathMLTree.js and domTree.js.
type textNode struct{ text string }

func (t *textNode) toMarkup() string       { return tmEscape(t.text) }
func (t *textNode) toText() string         { return t.text }
func (t *textNode) childrenPtr() *[]mmNode { return nil }
func (t *textNode) classesPtr() *[]string  { return nil }
func (t *textNode) styleOf() *tmOrd        { return nil }
func (t *textNode) attrsOf() *tmOrd        { return nil }

// docFragment is DocumentFragment of tree.js.
type docFragment struct {
	children []mmNode
	classes  []string
	style    *tmOrd
}

func newDocFragment(children []mmNode) *docFragment {
	return &docFragment{children: children, style: tmNewOrd()}
}

func (f *docFragment) childrenPtr() *[]mmNode { return &f.children }
func (f *docFragment) classesPtr() *[]string  { return &f.classes }
func (f *docFragment) styleOf() *tmOrd        { return f.style }
func (f *docFragment) attrsOf() *tmOrd        { return nil }
func (f *docFragment) toMarkup() string {
	var b strings.Builder
	for _, c := range f.children {
		b.WriteString(c.toMarkup())
	}
	return b.String()
}
func (f *docFragment) toText() string { return f.toMarkupText() }
func (f *docFragment) toMarkupText() string {
	var b strings.Builder
	for _, c := range f.children {
		b.WriteString(c.toText())
	}
	return b.String()
}

// spanNode is Span of domTree.js.
type spanNode struct {
	classes  []string
	attrs    *tmOrd
	style    *tmOrd
	children []mmNode
}

func newSpan(classes []string, children []mmNode) *spanNode {
	return &spanNode{classes: classes, attrs: tmNewOrd(), style: tmNewOrd(), children: children}
}

func (s *spanNode) childrenPtr() *[]mmNode { return &s.children }
func (s *spanNode) classesPtr() *[]string  { return &s.classes }
func (s *spanNode) styleOf() *tmOrd        { return s.style }
func (s *spanNode) attrsOf() *tmOrd        { return s.attrs }
func (s *spanNode) toText() string         { panic(&tmJSError{"child.toText is not a function"}) }
func (s *spanNode) toMarkup() string {
	var b strings.Builder
	b.WriteString("<span")
	if len(s.classes) > 0 {
		b.WriteString(` class="` + tmEscape(tmCreateClass(s.classes)) + `"`)
	}
	if styles := tmStyleMarkup(s.style); styles != "" {
		b.WriteString(` style="` + styles + `"`)
	}
	for _, k := range s.attrs.keys {
		b.WriteString(" " + k + `="` + tmEscape(jsStr(s.attrs.vals[k])) + `"`)
	}
	b.WriteString(">")
	for _, c := range s.children {
		b.WriteString(c.toMarkup())
	}
	b.WriteString("</span>")
	return b.String()
}

// anchorNode is AnchorNode of domTree.js.
type anchorNode struct {
	href     string
	classes  []string
	children []mmNode
}

func (a *anchorNode) childrenPtr() *[]mmNode { return &a.children }
func (a *anchorNode) classesPtr() *[]string  { return &a.classes }
func (a *anchorNode) styleOf() *tmOrd        { return nil }
func (a *anchorNode) attrsOf() *tmOrd        { return nil }
func (a *anchorNode) toText() string         { panic(&tmJSError{"child.toText is not a function"}) }
func (a *anchorNode) toMarkup() string {
	var b strings.Builder
	b.WriteString("<a href='" + tmEscape(a.href) + "'")
	if len(a.classes) > 0 {
		b.WriteString(` class="` + tmEscape(tmCreateClass(a.classes)) + `"`)
	}
	b.WriteString(">")
	for _, c := range a.children {
		b.WriteString(c.toMarkup())
	}
	b.WriteString("</a>")
	return b.String()
}

// mmType is node.type: the type of a MathNode, "" for the other kinds.
func mmType(n mmNode) string {
	if m, ok := n.(*mathNode); ok {
		return m.typ
	}
	return ""
}

func mmChildren(n mmNode) []mmNode {
	p := n.childrenPtr()
	if p == nil {
		return nil
	}
	return *p
}

// mmStyle is node.style, throwing the TypeError of a property write on undefined when there is none.
func mmStyle(n mmNode) *tmOrd {
	s := n.styleOf()
	if s == nil {
		panic(&tmJSError{"Cannot set properties of undefined"})
	}
	return s
}

func mmPushClass(n mmNode, class string) {
	p := n.classesPtr()
	if p == nil {
		panic(&tmJSError{"Cannot read properties of undefined (reading 'push')"})
	}
	*p = append(*p, class)
}

func wrapWithMstyle(expression []mmNode) *mathNode {
	if len(expression) == 1 {
		if m, ok := expression[0].(*mathNode); ok && m.typ == "mrow" {
			m.typ = "mstyle"
			return m
		}
	}
	return mn("mstyle", expression...)
}

// ---- Style -------------------------------------------------------------------------------------------------------

// tmStyle is a Style instance. It keeps its fields in a tmOrd because the original shares the object with the
// nodes that take it as their `style` (array.js builds padding cells with it).
type tmStyle struct{ o *tmOrd }

var tmInfinity = math.Inf(1)

func tmNewStyle(level float64, color any, font, fontFamily string, fontSize float64, fontWeight, fontShape string, maxSize []float64) *tmStyle {
	o := tmNewOrd()
	o.set("level", level)
	o.set("color", color)
	o.set("font", font)
	o.set("fontFamily", fontFamily)
	if fontSize == 0 {
		fontSize = 1.0
	}
	o.set("fontSize", fontSize)
	o.set("fontWeight", fontWeight)
	o.set("fontShape", fontShape)
	o.set("maxSize", maxSize)
	return &tmStyle{o}
}

func (s *tmStyle) level() int         { return int(s.o.get("level").(float64)) }
func (s *tmStyle) color() any         { return s.o.get("color") }
func (s *tmStyle) font() string       { v, _ := s.o.get("font").(string); return v }
func (s *tmStyle) fontFamily() string { v, _ := s.o.get("fontFamily").(string); return v }
func (s *tmStyle) fontSize() float64  { v, _ := s.o.get("fontSize").(float64); return v }
func (s *tmStyle) fontWeight() string { v, _ := s.o.get("fontWeight").(string); return v }
func (s *tmStyle) fontShape() string  { v, _ := s.o.get("fontShape").(string); return v }
func (s *tmStyle) maxSize() []float64 { v, _ := s.o.get("maxSize").([]float64); return v }

type tmStyleExt struct {
	level      *float64
	color      *any
	font       *string
	fontFamily *string
	fontSize   *float64
	fontWeight *string
	fontShape  *string
}

func (s *tmStyle) extend(e tmStyleExt) *tmStyle {
	level := s.o.get("level").(float64)
	color := s.color()
	font, family, size, weight, shape := s.font(), s.fontFamily(), s.fontSize(), s.fontWeight(), s.fontShape()
	if e.level != nil {
		level = *e.level
	}
	if e.color != nil {
		color = *e.color
	}
	if e.font != nil {
		font = *e.font
	}
	if e.fontFamily != nil {
		family = *e.fontFamily
	}
	if e.fontSize != nil {
		size = *e.fontSize
	}
	if e.fontWeight != nil {
		weight = *e.fontWeight
	}
	if e.fontShape != nil {
		shape = *e.fontShape
	}
	return tmNewStyle(level, color, font, family, size, weight, shape, s.maxSize())
}

func (s *tmStyle) withLevel(n int) *tmStyle {
	f := float64(n)
	return s.extend(tmStyleExt{level: &f})
}

func (s *tmStyle) incrementLevel() *tmStyle {
	return s.withLevel(min(s.level()+1, 3))
}

func (s *tmStyle) inSubOrSup() *tmStyle {
	return s.withLevel([]int{2, 2, 3, 3}[s.level()])
}

func (s *tmStyle) withColor(color any) *tmStyle  { return s.extend(tmStyleExt{color: &color}) }
func (s *tmStyle) withFont(font string) *tmStyle { return s.extend(tmStyleExt{font: &font}) }
func (s *tmStyle) withFontSize(n float64) *tmStyle {
	return s.extend(tmStyleExt{fontSize: &n})
}

func (s *tmStyle) withTextFontFamily(family string) *tmStyle {
	empty := ""
	return s.extend(tmStyleExt{fontFamily: &family, font: &empty})
}

func (s *tmStyle) withTextFontWeight(weight string) *tmStyle {
	empty := ""
	return s.extend(tmStyleExt{fontWeight: &weight, font: &empty})
}

func (s *tmStyle) withTextFontShape(shape string) *tmStyle {
	empty := ""
	return s.extend(tmStyleExt{fontShape: &shape, font: &empty})
}

const (
	tmStyleDisplay = 0
	tmStyleText    = 1
	tmStyleScript  = 2
	tmStyleScriptS = 3
)

// ---- units -------------------------------------------------------------------------------------------------------

var tmPtPerUnit = map[string]float64{
	"pt": 800.0 / 803.0,
	"pc": (12 * 800.0) / 803.0,
	"dd": ((1238.0 / 1157.0) * 800) / 803,
	"cc": ((14856.0 / 1157.0) * 800) / 803,
	"nd": ((685.0 / 642.0) * 800) / 803,
	"nc": ((1370.0 / 107.0) * 800) / 803,
	"sp": ((1.0 / 65536.0) * 800) / 803,
	"mm": 25.4 / 72,
	"cm": 2.54 / 72,
	"in": 1.0 / 72,
	"px": 96.0 / 72,
}

var tmValidUnits = map[string]bool{"em": true, "ex": true, "mu": true, "pt": true, "mm": true, "cm": true, "in": true, "px": true, "bp": true, "pc": true, "dd": true, "cc": true, "nd": true, "nc": true, "sp": true}

func tmValidUnit(unit string) bool { return tmValidUnits[unit] }

func tmEmScale(styleLevel int) float64 {
	scriptLevel := max(styleLevel-1, 0)
	return []float64{1, 0.7, 0.5}[scriptLevel]
}

// tmSize is { number, unit }.
type tmSize struct {
	number float64
	unit   string
}

func (s tmSize) String() string { return jsNumberString(s.number) + s.unit }

func tmCalculateSize(v tmSize, style *tmStyle) tmSize {
	number := v.number
	maxSize := style.maxSize()
	if maxSize[0] < 0 && number > 0 {
		return tmSize{0, "em"}
	}
	switch v.unit {
	case "mm", "cm", "in", "px":
		numInCssPts := number * tmPtPerUnit[v.unit]
		if numInCssPts > maxSize[1] {
			return tmSize{maxSize[1], "pt"}
		}
		return tmSize{number, v.unit}
	case "em", "ex":
		if v.unit == "ex" {
			number *= 0.431
		}
		number = math.Min(number/tmEmScale(style.level()), maxSize[0])
		return tmSize{tmRound(number), "em"}
	case "bp":
		if number > maxSize[1] {
			number = maxSize[1]
		}
		return tmSize{number, "pt"}
	case "pt", "pc", "dd", "cc", "nd", "nc", "sp":
		number = math.Min(number*tmPtPerUnit[v.unit], maxSize[1])
		return tmSize{tmRound(number), "pt"}
	case "mu":
		number = math.Min(number/18, maxSize[0])
		return tmSize{tmRound(number), "em"}
	}
	tmThrowf("Invalid unit: '" + v.unit + "'")
	return tmSize{}
}

// tmSizeOf reads a { number, unit } object from a parse node field.
func tmSizeOf(v any) tmSize {
	switch x := v.(type) {
	case tmSize:
		return x
	case *tmSize:
		return *x
	case tmObj:
		n, _ := x["number"].(float64)
		u, _ := x["unit"].(string)
		return tmSize{n, u}
	}
	panic(&tmJSError{"Cannot read properties of undefined (reading 'number')"})
}

// ---- replace.js and stretchy.js ----------------------------------------------------------------------------------

var tmScriptOffs = map[string]int{"B": 0x20EA, "E": 0x20EB, "F": 0x20EB, "H": 0x20C3, "I": 0x20C7, "L": 0x20C6, "M": 0x20E6, "R": 0x20C9, "e": 0x20CA, "g": 0x20A3, "o": 0x20C5}
var tmFrakOffs = map[string]int{"C": 0x20EA, "H": 0x20C4, "I": 0x20C8, "R": 0x20CA, "Z": 0x20CE}
var tmBbbOffs = map[string]int{"C": 0x20BF, "H": 0x20C5, "N": 0x20C7, "P": 0x20C9, "Q": 0x20C9, "R": 0x20CB, "Z": 0x20CA}
var tmBoldOffs = map[string]int{"\u03f5": 0x1D2E7, "\u03d1": 0x1D30C, "\u03f0": 0x1D2EE, "\u03c6": 0x1D319, "\u03f1": 0x1D2EF, "\u03d6": 0x1D30B}
var tmBoldItalicOffs = map[string]int{"\u03f5": 0x1D35B, "\u03d1": 0x1D380, "\u03f0": 0x1D362, "\u03c6": 0x1D38D, "\u03f1": 0x1D363, "\u03d6": 0x1D37F}
var tmBoldSfOffs = map[string]int{"\u03f5": 0x1D395, "\u03d1": 0x1D3BA, "\u03f0": 0x1D39C, "\u03c6": 0x1D3C7, "\u03f1": 0x1D39D, "\u03d6": 0x1D3B9}
var tmBisfOffs = map[string]int{"\u03f5": 0x1D3CF, "\u03d1": 0x1D3F4, "\u03f0": 0x1D3D6, "\u03c6": 0x1D401, "\u03f1": 0x1D3D7, "\u03d6": 0x1D3F3}

func tmOr(m map[string]int, ch string, def int) int {
	if v, ok := m[ch]; ok && v != 0 {
		return v
	}
	return def
}

var tmVariantOffsets = map[string]map[string]func(ch string) int{
	"upperCaseLatin": {
		"normal":                 func(string) int { return 0 },
		"bold":                   func(string) int { return 0x1D3BF },
		"italic":                 func(string) int { return 0x1D3F3 },
		"bold-italic":            func(string) int { return 0x1D427 },
		"script":                 func(ch string) int { return tmOr(tmScriptOffs, ch, 0x1D45B) },
		"script-bold":            func(string) int { return 0x1D48F },
		"fraktur":                func(ch string) int { return tmOr(tmFrakOffs, ch, 0x1D4C3) },
		"fraktur-bold":           func(string) int { return 0x1D52B },
		"double-struck":          func(ch string) int { return tmOr(tmBbbOffs, ch, 0x1D4F7) },
		"sans-serif":             func(string) int { return 0x1D55F },
		"sans-serif-bold":        func(string) int { return 0x1D593 },
		"sans-serif-italic":      func(string) int { return 0x1D5C7 },
		"sans-serif-bold-italic": func(string) int { return 0x1D63C },
		"monospace":              func(string) int { return 0x1D62F },
	},
	"lowerCaseLatin": {
		"normal": func(string) int { return 0 },
		"bold":   func(string) int { return 0x1D3B9 },
		"italic": func(ch string) int {
			if ch == "h" {
				return 0x20A6
			}
			return 0x1D3ED
		},
		"bold-italic":            func(string) int { return 0x1D421 },
		"script":                 func(ch string) int { return tmOr(tmScriptOffs, ch, 0x1D455) },
		"script-bold":            func(string) int { return 0x1D489 },
		"fraktur":                func(string) int { return 0x1D4BD },
		"fraktur-bold":           func(string) int { return 0x1D525 },
		"double-struck":          func(string) int { return 0x1D4F1 },
		"sans-serif":             func(string) int { return 0x1D559 },
		"sans-serif-bold":        func(string) int { return 0x1D58D },
		"sans-serif-italic":      func(string) int { return 0x1D5C1 },
		"sans-serif-bold-italic": func(string) int { return 0x1D5F5 },
		"monospace":              func(string) int { return 0x1D629 },
	},
	"upperCaseGreek": {
		"normal": func(string) int { return 0 }, "bold": func(string) int { return 0x1D317 }, "italic": func(string) int { return 0x1D351 },
		"bold-italic": func(string) int { return 0x1D317 }, "script": func(string) int { return 0 }, "script-bold": func(string) int { return 0 },
		"fraktur": func(string) int { return 0 }, "fraktur-bold": func(string) int { return 0 }, "double-struck": func(string) int { return 0 },
		"sans-serif": func(string) int { return 0x1D3C5 }, "sans-serif-bold": func(string) int { return 0x1D3C5 }, "sans-serif-italic": func(string) int { return 0 },
		"sans-serif-bold-italic": func(string) int { return 0x1D3FF }, "monospace": func(string) int { return 0 },
	},
	"lowerCaseGreek": {
		"normal": func(string) int { return 0 }, "bold": func(string) int { return 0x1D311 }, "italic": func(string) int { return 0x1D34B },
		"bold-italic": func(ch string) int {
			if ch == "\u03d5" {
				return 0x1D37E
			}
			return 0x1D385
		},
		"script": func(string) int { return 0 }, "script-bold": func(string) int { return 0 }, "fraktur": func(string) int { return 0 },
		"fraktur-bold": func(string) int { return 0 }, "double-struck": func(string) int { return 0 },
		"sans-serif": func(string) int { return 0x1D3BF }, "sans-serif-bold": func(string) int { return 0x1D3BF }, "sans-serif-italic": func(string) int { return 0 },
		"sans-serif-bold-italic": func(string) int { return 0x1D3F9 }, "monospace": func(string) int { return 0 },
	},
	"varGreek": {
		"normal": func(string) int { return 0 }, "bold": func(ch string) int { return tmOr(tmBoldOffs, ch, -51) }, "italic": func(string) int { return 0 },
		"bold-italic": func(ch string) int { return tmOr(tmBoldItalicOffs, ch, 0x3A) }, "script": func(string) int { return 0 },
		"script-bold": func(string) int { return 0 }, "fraktur": func(string) int { return 0 }, "fraktur-bold": func(string) int { return 0 },
		"double-struck": func(string) int { return 0 }, "sans-serif": func(ch string) int { return tmOr(tmBoldSfOffs, ch, 0x74) },
		"sans-serif-bold": func(ch string) int { return tmOr(tmBoldSfOffs, ch, 0x74) }, "sans-serif-italic": func(string) int { return 0 },
		"sans-serif-bold-italic": func(ch string) int { return tmOr(tmBisfOffs, ch, 0xAE) }, "monospace": func(string) int { return 0 },
	},
	"numeral": {
		"normal": func(string) int { return 0 }, "bold": func(string) int { return 0x1D79E }, "italic": func(string) int { return 0 },
		"bold-italic": func(string) int { return 0 }, "script": func(string) int { return 0 }, "script-bold": func(string) int { return 0 },
		"fraktur": func(string) int { return 0 }, "fraktur-bold": func(string) int { return 0 }, "double-struck": func(string) int { return 0x1D7A8 },
		"sans-serif": func(string) int { return 0x1D7B2 }, "sans-serif-bold": func(string) int { return 0x1D7BC },
		"sans-serif-italic": func(string) int { return 0 }, "sans-serif-bold-italic": func(string) int { return 0 }, "monospace": func(string) int { return 0x1D7C6 },
	},
}

// tmCodePointAt is str.codePointAt(0): -1 for the empty string.
func tmCodePointAt(s string) int {
	for _, r := range s {
		return int(r)
	}
	return -1
}

func tmVariantChar(ch, variant string) string {
	cp := tmCodePointAt(ch)
	var block string
	_, inBold := tmBoldOffs[ch]
	switch {
	case 0x40 < cp && cp < 0x5b:
		block = "upperCaseLatin"
	case 0x60 < cp && cp < 0x7b:
		block = "lowerCaseLatin"
	case 0x390 < cp && cp < 0x3AA:
		block = "upperCaseGreek"
	case (0x3B0 < cp && cp < 0x3CA) || ch == "\u03d5":
		block = "lowerCaseGreek"
	case (0x1D6E1 < cp && cp < 0x1D6FC) || inBold:
		block = "varGreek"
	case 0x2F < cp && cp < 0x3A:
		block = "numeral"
	default:
		block = "other"
	}
	if block == "other" {
		return ch
	}
	f := tmVariantOffsets[block][variant]
	if f == nil {
		panic(&tmJSError{"offset[block][variant] is not a function"})
	}
	return string(rune(cp + f(ch)))
}

var tmSmallCaps = map[string]string{
	"a": "ᴀ", "b": "ʙ", "c": "ᴄ", "d": "ᴅ", "e": "ᴇ", "f": "ꜰ", "g": "ɢ", "h": "ʜ", "i": "ɪ", "j": "ᴊ", "k": "ᴋ", "l": "ʟ", "m": "ᴍ",
	"n": "ɴ", "o": "ᴏ", "p": "ᴘ", "q": "ǫ", "r": "ʀ", "s": "s", "t": "ᴛ", "u": "ᴜ", "v": "ᴠ", "w": "ᴡ", "x": "x", "y": "ʏ", "z": "ᴢ",
}

var tmStretchyCodePoint = map[string]string{
	"widehat": "\u02c6", "widecheck": "ˇ", "widetilde": "~", "wideparen": "⏜", "utilde": "~", "overleftarrow": "\u2190",
	"underleftarrow": "\u2190", "xleftarrow": "\u2190", "overrightarrow": "\u2192", "underrightarrow": "\u2192", "xrightarrow": "\u2192",
	"underbrace": "\u23df", "overbrace": "\u23de", "overbracket": "\u23b4", "underbracket": "\u23b5", "overgroup": "\u23e0", "overparen": "⏜",
	"undergroup": "\u23e1", "underparen": "\u23dd", "overleftrightarrow": "\u2194", "underleftrightarrow": "\u2194", "xleftrightarrow": "\u2194",
	"Overrightarrow": "\u21d2", "xRightarrow": "\u21d2", "overleftharpoon": "\u21bc", "xleftharpoonup": "\u21bc", "overrightharpoon": "\u21c0",
	"xrightharpoonup": "\u21c0", "xLeftarrow": "\u21d0", "xLeftrightarrow": "\u21d4", "xhookleftarrow": "\u21a9", "xhookrightarrow": "\u21aa",
	"xmapsto": "\u21a6", "xrightharpoondown": "\u21c1", "xleftharpoondown": "\u21bd", "xtwoheadleftarrow": "\u219e", "xtwoheadrightarrow": "\u21a0",
	"xlongequal": "=", "xrightleftarrows": "\u21c4", "xtofrom": "\u21c4", "xleftrightharpoons": "\u21cb", "xrightleftharpoons": "\u21cc",
	"yields": "\u2192", "yieldsLeft": "\u2190", "mesomerism": "\u2194", "longrightharpoonup": "\u21c0", "longleftharpoondown": "\u21bd",
	"eqrightharpoonup": "\u21c0", "eqleftharpoondown": "\u21bd", "\\cdrightarrow": "\u2192", "\\cdleftarrow": "\u2190", "\\cdlongequal": "=",
	"yieldsLeftRight": "\u21c4", "chemequilibrium": "\u21cc",
}

// tmStretchyNode is stretchy.mathMLnode(label): the label without its first character selects the code point.
func tmStretchyNode(label string) *mathNode {
	// label.slice(1) on a UTF-16 string: the first code unit is a backslash for every label
	rest := label
	if len(rest) > 0 {
		rest = rest[1:]
	}
	cp, ok := tmStretchyCodePoint[rest]
	if !ok {
		// new TextNode(undefined) prints "undefined"
		cp = "undefined"
	}
	node := mn("mo", &textNode{cp})
	node.setAttribute("stretchy", "true")
	return node
}

func tmEstimatedWidth(node tmObj) float64 {
	width := 0.0
	body := node["body"]
	switch b := body.(type) {
	case []any:
		for _, item := range b {
			if io, ok := item.(tmObj); ok {
				width += tmEstimatedWidth(io)
			} else {
				width += 1.0
			}
		}
		return width
	case tmObj:
		return tmEstimatedWidth(b)
	}
	typ, _ := node["type"].(string)
	switch typ {
	case "supsub":
		width += tmEstimatedWidth(node["base"].(tmObj))
		if sub, ok := node["sub"].(tmObj); ok {
			width += 0.7 * tmEstimatedWidth(sub)
		}
		if sup, ok := node["sup"].(tmObj); ok {
			width += 0.7 * tmEstimatedWidth(sup)
		}
	case "mathord", "textord":
		text, _ := node["text"].(string)
		for _, u := range tmUTF16Units(text) {
			cp := int(u)
			// codePointAt(0) of a one-unit string: a lone surrogate is itself
			switch {
			case (0x60 < cp && cp < 0x7B) || (0x03B0 < cp && cp < 0x3CA):
				width += 0.56
			case 0x2F < cp && cp < 0x3A:
				width += 0.50
			default:
				width += 0.92
			}
		}
	default:
		width += 1.0
	}
	return width
}

func tmAccentNode(group tmObj) *mathNode {
	label, _ := group["label"].(string)
	mo := tmStretchyNode(label)
	if strings.Contains(label, "tilde") {
		width := tmEstimatedWidth(group["base"].(tmObj))
		switch {
		case 1 < width && width < 1.6:
			mo.classes = append(mo.classes, "tml-tilde-2")
		case 1.6 <= width && width < 2.5:
			mo.classes = append(mo.classes, "tml-tilde-3")
		case 2.5 <= width:
			mo.classes = append(mo.classes, "tml-tilde-4")
		}
	} else if label == "\\widehat" || label == "\\widecheck" {
		width := tmEstimatedWidth(group["base"].(tmObj))
		switch {
		case 0 < width && width <= 1:
			mo.classes = append(mo.classes, "tml-hat-1")
		case 1 < width && width < 1.6:
			mo.classes = append(mo.classes, "tml-hat-2")
		case 1.6 <= width && width < 2.5:
			mo.classes = append(mo.classes, "tml-hat-3")
		case 2.5 <= width:
			mo.classes = append(mo.classes, "tml-hat-4")
		}
	}
	return mo
}
