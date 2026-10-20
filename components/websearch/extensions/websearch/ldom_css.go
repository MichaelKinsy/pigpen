package websearch

import (
	"regexp"
	"strconv"
	"strings"
)

// css-what 6.2.2 (parse.js) and css-select 5.2.2 (compile.js, general.js, attributes.js, pseudo-selectors/*) with the
// adapter linkedom gives it (shared/matches.js), in an HTML document. Pseudo-classes and combinators Defuddle never
// uses (:host, column combinators) fail as they do there, "not supported", or are not recognized. The thrown
// errors carry the library's messages: Defuddle's own twin observes "Unknown pseudo-class :a".

const (
	stDescendant     = "descendant"
	stChild          = "child"
	stParent         = "parent"
	stSibling        = "sibling"
	stAdjacent       = "adjacent"
	stColumn         = "column-combinator"
	stTag            = "tag"
	stUniversal      = "universal"
	stAttribute      = "attribute"
	stPseudo         = "pseudo"
	stPseudoElement  = "pseudo-element"
	stFlexDescendant = "_flexibleDescendant"
)

const (
	icNull   = -1
	icFalse  = 0
	icTrue   = 1
	icQuirks = 2
)

type cssTok struct {
	typ        string
	name       string
	action     string
	value      string
	ignoreCase int
	namespace  *string
	hasData    bool
	data       string
	subs       [][]cssTok // data of :is, :not, :has, ...
	isSubs     bool
}

func (t cssTok) isTraversal() bool {
	switch t.typ {
	case stDescendant, stChild, stParent, stSibling, stAdjacent, stColumn, stFlexDescendant:
		return true
	}
	return false
}

// sortProcedure's isTraversal: any token that is not universal, tag, attribute or pseudo.
func (t cssTok) isTraversalSort() bool {
	switch t.typ {
	case stUniversal, stTag, stAttribute, stPseudo:
		return false
	}
	return true
}

var (
	cssReName   = regexp.MustCompile(`^[^\\#]?(?:\\(?:[\da-f]{1,6}\s?|.)|[\w\-\x{00b0}-\x{FFFF}])+`)
	cssReEscape = regexp.MustCompile(`(?i)\\([\da-f]{1,6}\s?|(\s)|.)`)
)

var cssActionTypes = map[byte]string{'~': "element", '^': "start", '$': "end", '*': "any", '!': "not", '|': "hyphen"}

var cssUnpackPseudos = map[string]bool{"has": true, "not": true, "matches": true, "is": true, "where": true, "host": true, "host-context": true}

func cssIsQuote(c byte) bool { return c == '\'' || c == '"' }
func cssIsWS(c byte) bool    { return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r' }

// cssUnescape is unescapeCSS.
func cssUnescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	return cssReEscape.ReplaceAllStringFunc(s, func(m string) string {
		sub := cssReEscape.FindStringSubmatch(m)
		escaped := sub[1]
		whitespace := sub[2]
		high, err := strconv.ParseInt(strings.TrimSpace(escaped), 16, 64)
		if err != nil || whitespace != "" {
			return escaped
		}
		cp := high
		if cp < 0x10000 {
			return string(utf16Unit(rune(cp)))
		}
		return string(rune(cp))
	})
}

// utf16Unit is String.fromCharCode for a BMP unit; a lone surrogate cannot be a Go string, so it becomes U+FFFD.
func utf16Unit(r rune) rune {
	if r >= 0xd800 && r <= 0xdfff {
		return 0xfffd
	}
	return r
}

// cssParse is css-what's parse: the comma-separated selectors, each a list of tokens.
func cssParse(selector string) [][]cssTok {
	var subs [][]cssTok
	end := cssParseSelector(&subs, selector, 0)
	if end < len(selector) {
		ldThrow("Unmatched selector: " + selector[end:])
	}
	return subs
}

func cssParseSelector(subselects *[][]cssTok, selector string, idx int) int {
	var tokens []cssTok
	at := func(i int) int {
		if i >= 0 && i < len(selector) {
			return int(selector[i])
		}
		return -1
	}
	getName := func(offset int) string {
		rest := ""
		if idx+offset <= len(selector) {
			rest = selector[idx+offset:]
		}
		m := cssReName.FindString(rest)
		if m == "" {
			ldThrow("Expected name, found " + selector[idx:])
		}
		idx += offset + len(m)
		return cssUnescape(m)
	}
	stripWhitespace := func(offset int) {
		idx += offset
		for idx < len(selector) && cssIsWS(selector[idx]) {
			idx++
		}
	}
	isEscaped := func(pos int) bool {
		slashes := 0
		for pos--; pos >= 0 && selector[pos] == '\\'; pos-- {
			slashes++
		}
		return slashes&1 == 1
	}
	readValueWithParenthesis := func() string {
		idx++
		start := idx
		counter := 1
		for ; counter > 0 && idx < len(selector); idx++ {
			if selector[idx] == '(' && !isEscaped(idx) {
				counter++
			} else if selector[idx] == ')' && !isEscaped(idx) {
				counter--
			}
		}
		if counter != 0 {
			ldThrow("Parenthesis not matched")
		}
		return cssUnescape(selector[start : idx-1])
	}
	ensureNotTraversal := func() {
		if len(tokens) > 0 && tokens[len(tokens)-1].isTraversal() {
			ldThrow("Did not expect successive traversals.")
		}
	}
	addTraversal := func(typ string) {
		if len(tokens) > 0 && tokens[len(tokens)-1].typ == stDescendant {
			tokens[len(tokens)-1].typ = typ
			return
		}
		ensureNotTraversal()
		tokens = append(tokens, cssTok{typ: typ})
	}
	addSpecialAttribute := func(name, action string) {
		tokens = append(tokens, cssTok{typ: stAttribute, name: name, action: action, value: getName(1), ignoreCase: icQuirks})
	}
	finalize := func() {
		if len(tokens) > 0 && tokens[len(tokens)-1].typ == stDescendant {
			tokens = tokens[:len(tokens)-1]
		}
		if len(tokens) == 0 {
			ldThrow("Empty sub-selector")
		}
		*subselects = append(*subselects, tokens)
	}

	stripWhitespace(0)
	if len(selector) == idx {
		return idx
	}
loop:
	for idx < len(selector) {
		first := selector[idx]
		switch first {
		case ' ', '\t', '\n', '\f', '\r':
			if len(tokens) == 0 || tokens[0].typ != stDescendant {
				ensureNotTraversal()
				tokens = append(tokens, cssTok{typ: stDescendant})
			}
			stripWhitespace(1)
		case '>':
			addTraversal(stChild)
			stripWhitespace(1)
		case '<':
			addTraversal(stParent)
			stripWhitespace(1)
		case '~':
			addTraversal(stSibling)
			stripWhitespace(1)
		case '+':
			addTraversal(stAdjacent)
			stripWhitespace(1)
		case '.':
			addSpecialAttribute("class", "element")
		case '#':
			addSpecialAttribute("id", "equals")
		case '[':
			stripWhitespace(1)
			var name string
			var namespace *string
			if at(idx) == '|' {
				name = getName(1)
			} else if strings.HasPrefix(selector[idx:], "*|") {
				ns := "*"
				namespace = &ns
				name = getName(2)
			} else {
				name = getName(0)
				if at(idx) == '|' && at(idx+1) != '=' {
					ns := name
					namespace = &ns
					name = getName(1)
				}
			}
			stripWhitespace(0)
			action := "exists"
			if a, ok := cssActionTypes[byte(at(idx))]; ok && at(idx) >= 0 {
				action = a
				if at(idx+1) != '=' {
					ldThrow("Expected `=`")
				}
				stripWhitespace(2)
			} else if at(idx) == '=' {
				action = "equals"
				stripWhitespace(1)
			}
			value := ""
			ignoreCase := icNull
			if action != "exists" {
				if at(idx) >= 0 && cssIsQuote(selector[idx]) {
					quote := selector[idx]
					sectionEnd := idx + 1
					for sectionEnd < len(selector) && (selector[sectionEnd] != quote || isEscaped(sectionEnd)) {
						sectionEnd++
					}
					if at(sectionEnd) != int(quote) {
						ldThrow("Attribute value didn't end")
					}
					value = cssUnescape(selector[idx+1 : sectionEnd])
					idx = sectionEnd + 1
				} else {
					valueStart := idx
					for idx < len(selector) && ((!cssIsWS(selector[idx]) && selector[idx] != ']') || isEscaped(idx)) {
						idx++
					}
					value = cssUnescape(selector[valueStart:idx])
				}
				stripWhitespace(0)
				// A force-ignore flag, i or s.
				if c := at(idx); c >= 0 {
					switch c | 0x20 {
					case 's':
						ignoreCase = icFalse
						stripWhitespace(1)
					case 'i':
						ignoreCase = icTrue
						stripWhitespace(1)
					}
				}
			}
			if at(idx) != ']' {
				ldThrow("Attribute selector didn't terminate")
			}
			idx++
			tokens = append(tokens, cssTok{typ: stAttribute, name: name, action: action, value: value, namespace: namespace, ignoreCase: ignoreCase})
		case ':':
			if at(idx+1) == ':' {
				name := strings.ToLower(getName(2))
				t := cssTok{typ: stPseudoElement, name: name}
				if at(idx) == '(' {
					t.hasData = true
					t.data = readValueWithParenthesis()
				}
				tokens = append(tokens, t)
				continue
			}
			name := strings.ToLower(getName(1))
			t := cssTok{typ: stPseudo, name: name}
			if at(idx) == '(' {
				if cssUnpackPseudos[name] {
					if at(idx+1) >= 0 && cssIsQuote(selector[idx+1]) {
						ldThrow("Pseudo-selector " + name + " cannot be quoted")
					}
					t.isSubs = true
					idx = cssParseSelector(&t.subs, selector, idx+1)
					if at(idx) != ')' {
						ldThrow("Missing closing parenthesis in :" + name + " (" + selector + ")")
					}
					idx++
				} else {
					t.hasData = true
					data := readValueWithParenthesis()
					if name == "contains" || name == "icontains" {
						if len(data) > 0 && data[0] == data[len(data)-1] && cssIsQuote(data[0]) {
							data = data[1 : len(data)-1]
						}
					}
					t.data = cssUnescape(data)
				}
			}
			tokens = append(tokens, t)
		case ',':
			finalize()
			tokens = nil
			stripWhitespace(1)
		default:
			if strings.HasPrefix(selector[idx:], "/*") {
				endIdx := strings.Index(selector[idx+2:], "*/")
				if endIdx < 0 {
					ldThrow("Comment was not terminated")
				}
				idx = idx + 2 + endIdx + 2
				if len(tokens) == 0 {
					stripWhitespace(0)
				}
				continue
			}
			var namespace *string
			var name string
			switch {
			case first == '*':
				idx++
				name = "*"
			case first == '|':
				name = ""
				if at(idx+1) == '|' {
					addTraversal(stColumn)
					stripWhitespace(2)
					continue
				}
			case cssReName.MatchString(selector[idx:]):
				name = getName(0)
			default:
				break loop
			}
			if at(idx) == '|' && at(idx+1) != '|' {
				ns := name
				namespace = &ns
				if at(idx+1) == '*' {
					name = "*"
					idx += 2
				} else {
					name = getName(1)
				}
			}
			if name == "*" {
				tokens = append(tokens, cssTok{typ: stUniversal, namespace: namespace})
			} else {
				tokens = append(tokens, cssTok{typ: stTag, name: name, namespace: namespace})
			}
		}
	}
	finalize()
	return idx
}

// ---- compile ---------------------------------------------------------------------------------------------------

type cssFnKind uint8

const (
	cssNormal cssFnKind = iota
	cssTrue
	cssFalse
)

// cssFn is a compiled selector: boolbase's trueFunc and falseFunc are told apart by kind, as the library does by identity.
type cssFn struct {
	f                      func(*ldNode) bool
	kind                   cssFnKind
	shouldTestNextSiblings bool
}

var (
	cssTrueFn  = cssFn{kind: cssTrue, f: func(*ldNode) bool { return true }}
	cssFalseFn = cssFn{kind: cssFalse, f: func(*ldNode) bool { return false }}
)

func cssNew(f func(*ldNode) bool) cssFn { return cssFn{f: f} }

// cssCtx is a compile context: the elements :scope stands for (a placeholder, nil, until :has fills it in).
type cssCtx struct {
	nodes   []*ldNode
	isArray bool
}

func cssIsTag(n *ldNode) bool { return n != nil && n.kind == ldElem }

func cssElementParent(n *ldNode) *ldNode {
	if p := n.parent; p != nil && p.kind == ldElem {
		return p
	}
	return nil
}

// cssSiblings is adapter.getSiblings: the parent's children, or the node alone.
func cssSiblings(n *ldNode) []*ldNode {
	if n.parent != nil {
		return n.parent.childNodes()
	}
	return []*ldNode{n}
}

// cssText is adapter.getText.
func cssText(n *ldNode) string {
	switch n.kind {
	case ldText:
		return n.data
	case ldElem:
		var b strings.Builder
		for c := n.first; c != nil; c = c.next {
			b.WriteString(cssText(c))
		}
		return b.String()
	}
	return ""
}

// cssAttrValue is adapter.getAttributeValue.
func cssAttrValue(n *ldNode, name string) (string, bool) {
	if name == "class" {
		return n.attr("class"), true
	}
	return n.attrValue(name)
}

// cssExistsOne is adapter.existsOne: the test on an element or any element below one in the list.
func cssExistsOne(test func(*ldNode) bool, nodes []*ldNode) bool {
	for _, n := range nodes {
		if cssIsTag(n) && (test(n) || cssExistsOne(test, n.childNodes())) {
			return true
		}
	}
	return false
}

func cssCompile(selector string, scope *ldNode) cssFn {
	token := cssClone(cssParseCached(selector))
	next := cssCompileToken(token, scope, nil)
	return cssEnsureIsTag(next)
}

func cssEnsureIsTag(next cssFn) cssFn {
	if next.kind == cssFalse {
		return cssFalseFn
	}
	return cssNew(func(n *ldNode) bool { return cssIsTag(n) && next.f(n) })
}

var cssParseCache = map[string][][]cssTok{}

func cssParseCached(selector string) [][]cssTok {
	if t, ok := cssParseCache[selector]; ok {
		return t
	}
	t := cssParse(selector)
	if len(cssParseCache) < 4096 {
		cssParseCache[selector] = t
	}
	return t
}

func cssClone(t [][]cssTok) [][]cssTok {
	out := make([][]cssTok, len(t))
	for i, s := range t {
		out[i] = append([]cssTok(nil), s...)
	}
	return out
}

func cssIncludesScope(t cssTok) bool {
	if t.typ == stPseudo && t.name == "scope" {
		return true
	}
	if t.isSubs {
		for _, sub := range t.subs {
			for _, x := range sub {
				if cssIncludesScope(x) {
					return true
				}
			}
		}
	}
	return false
}

var (
	cssDescendantTok = cssTok{typ: stDescendant}
	cssFlexTok       = cssTok{typ: stFlexDescendant}
	cssScopeTok      = cssTok{typ: stPseudo, name: "scope"}
)

// cssAbsolutize is css-select's absolutize: a relative selector is anchored to the scope.
func cssAbsolutize(token [][]cssTok, ctx *cssCtx) {
	hasContext := false
	if ctx != nil {
		hasContext = true
		for _, e := range ctx.nodes {
			if !(e == nil || (e.parent != nil && cssIsTag(e.parent))) {
				hasContext = false
				break
			}
		}
	}
	for i, t := range token {
		if len(t) > 0 && t[0].isTraversalSort() && t[0].typ != stDescendant {
			// Don't continue in the else branch.
		} else if hasContext && !cssAnyScope(t) {
			t = append([]cssTok{cssDescendantTok}, t...)
		} else {
			continue
		}
		token[i] = append([]cssTok{cssScopeTok}, t...)
	}
}

func cssAnyScope(t []cssTok) bool {
	for _, x := range t {
		if cssIncludesScope(x) {
			return true
		}
	}
	return false
}

// cssCompileToken is compileToken. scope is options.context, an element when the selector mentions :scope.
func cssCompileToken(token [][]cssTok, scope *ldNode, param *cssCtx) cssFn {
	ctx := param
	if scope != nil {
		ctx = &cssCtx{nodes: []*ldNode{scope}}
	}
	isArray := ctx != nil && ctx.isArray
	if ctx != nil {
		// finalContext: whatever the context was, the nested compiles see an array.
		ctx = &cssCtx{nodes: ctx.nodes, isArray: true}
	}
	cssAbsolutize(token, ctx)
	shouldTestNextSiblings := false
	query := cssFalseFn
	for _, rules := range token {
		if len(rules) >= 2 {
			first, second := rules[0], rules[1]
			if first.typ != stPseudo || first.name != "scope" {
				// ignore
			} else if isArray && second.typ == stDescendant {
				rules[1] = cssFlexTok
			} else if second.typ == stAdjacent || second.typ == stSibling {
				shouldTestNextSiblings = true
			}
		}
		query = cssReduce(query, cssCompileRules(rules, ctx))
	}
	query.shouldTestNextSiblings = shouldTestNextSiblings
	return query
}

func cssCompileRules(rules []cssTok, ctx *cssCtx) cssFn {
	prev := cssTrueFn
	for _, r := range rules {
		if prev.kind == cssFalse {
			return cssFalseFn
		}
		prev = cssCompileGeneral(prev, r, ctx)
	}
	return prev
}

func cssReduce(a, b cssFn) cssFn {
	if b.kind == cssFalse || a.kind == cssTrue {
		return a
	}
	if a.kind == cssFalse || b.kind == cssTrue {
		return b
	}
	return cssNew(func(n *ldNode) bool { return a.f(n) || b.f(n) })
}

func cssCompileGeneral(next cssFn, sel cssTok, ctx *cssCtx) cssFn {
	switch sel.typ {
	case stPseudoElement:
		ldThrow("Pseudo-elements are not supported by css-select")
	case stColumn:
		ldThrow("Column combinators are not yet supported by css-select")
	case stAttribute:
		if sel.namespace != nil {
			ldThrow("Namespaced attributes are not yet supported by css-select")
		}
		sel.name = strings.ToLower(sel.name)
		return cssAttributeRule(next, sel)
	case stPseudo:
		return cssCompilePseudo(next, sel, ctx)
	case stTag:
		if sel.namespace != nil {
			ldThrow("Namespaced tag names are not yet supported by css-select")
		}
		name := strings.ToLower(sel.name)
		return cssNew(func(e *ldNode) bool { return strings.ToLower(e.name) == name && next.f(e) })
	case stDescendant:
		falseCache := map[*ldNode]bool{}
		return cssNew(func(e *ldNode) bool {
			for cur := cssElementParent(e); cur != nil; cur = cssElementParent(cur) {
				if !falseCache[cur] {
					if cssIsTag(cur) && next.f(cur) {
						return true
					}
					falseCache[cur] = true
				}
			}
			return false
		})
	case stFlexDescendant:
		return cssNew(func(e *ldNode) bool {
			for cur := e; cur != nil; cur = cssElementParent(cur) {
				if next.f(cur) {
					return true
				}
			}
			return false
		})
	case stParent:
		return cssNew(func(e *ldNode) bool {
			for _, c := range e.childNodes() {
				if cssIsTag(c) && next.f(c) {
					return true
				}
			}
			return false
		})
	case stChild:
		return cssNew(func(e *ldNode) bool {
			p := e.parent
			return p != nil && cssIsTag(p) && next.f(p)
		})
	case stSibling:
		return cssNew(func(e *ldNode) bool {
			for _, s := range cssSiblings(e) {
				if s == e {
					break
				}
				if cssIsTag(s) && next.f(s) {
					return true
				}
			}
			return false
		})
	case stAdjacent:
		return cssNew(func(e *ldNode) bool {
			var last *ldNode
			for _, s := range cssSiblings(e) {
				if s == e {
					break
				}
				if cssIsTag(s) {
					last = s
				}
			}
			return last != nil && next.f(last)
		})
	case stUniversal:
		if sel.namespace != nil && *sel.namespace != "*" {
			ldThrow("Namespaced universal selectors are not yet supported by css-select")
		}
		return next
	}
	return next
}

// ---- attributes ------------------------------------------------------------------------------------------------

var cssCaseInsensitiveAttributes = map[string]bool{
	"accept": true, "accept-charset": true, "align": true, "alink": true, "axis": true, "bgcolor": true, "charset": true,
	"checked": true, "clear": true, "codetype": true, "color": true, "compact": true, "declare": true, "defer": true,
	"dir": true, "direction": true, "disabled": true, "enctype": true, "face": true, "frame": true, "hreflang": true,
	"http-equiv": true, "lang": true, "language": true, "link": true, "media": true, "method": true, "multiple": true,
	"nohref": true, "noresize": true, "noshade": true, "nowrap": true, "readonly": true, "rel": true, "rev": true,
	"rules": true, "scope": true, "scrolling": true, "selected": true, "shape": true, "target": true, "text": true,
	"type": true, "valign": true, "valuetype": true, "vlink": true,
}

func cssShouldIgnoreCase(s cssTok) bool {
	switch s.ignoreCase {
	case icTrue:
		return true
	case icFalse, icQuirks:
		return false
	}
	return cssCaseInsensitiveAttributes[s.name]
}

const cssJSWS = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

func cssAttributeRule(next cssFn, d cssTok) cssFn {
	name, value := d.name, d.value
	ic := cssShouldIgnoreCase(d)
	get := func(e *ldNode) (string, bool) { return cssAttrValue(e, name) }
	switch d.action {
	case "equals":
		if ic {
			value = strings.ToLower(value)
			return cssNew(func(e *ldNode) bool {
				a, ok := get(e)
				return ok && strings.ToLower(a) == value && next.f(e)
			})
		}
		return cssNew(func(e *ldNode) bool {
			a, ok := get(e)
			return ok && a == value && next.f(e)
		})
	case "hyphen":
		n := jsLen(value)
		if ic {
			value = strings.ToLower(value)
		}
		return cssNew(func(e *ldNode) bool {
			a, ok := get(e)
			if !ok {
				return false
			}
			al := jsLen(a)
			if !(al == n || (al > n && jsSlice(a, n, n+1) == "-")) {
				return false
			}
			head := jsSlice(a, 0, n)
			if ic {
				head = strings.ToLower(head)
			}
			return head == value && next.f(e)
		})
	case "element":
		if regexp.MustCompile(cssJSWS).MatchString(value) {
			return cssFalseFn
		}
		pat := "(?:^|" + cssJSWS + ")" + regexp.QuoteMeta(value) + "(?:$|" + cssJSWS + ")"
		if ic {
			pat = "(?i)" + pat
		}
		re := regexp.MustCompile(pat)
		return cssNew(func(e *ldNode) bool {
			a, ok := get(e)
			return ok && re.MatchString(a) && next.f(e)
		})
	case "exists":
		return cssNew(func(e *ldNode) bool { return e.hasAttribute(name) && next.f(e) })
	case "start":
		if value == "" {
			return cssFalseFn
		}
		n := jsLen(value)
		if ic {
			value = strings.ToLower(value)
			return cssNew(func(e *ldNode) bool {
				a, ok := get(e)
				return ok && jsLen(a) >= n && strings.ToLower(jsSlice(a, 0, n)) == value && next.f(e)
			})
		}
		return cssNew(func(e *ldNode) bool {
			a, ok := get(e)
			return ok && strings.HasPrefix(a, value) && next.f(e)
		})
	case "end":
		if value == "" {
			return cssFalseFn
		}
		if ic {
			value = strings.ToLower(value)
			n := jsLen(value)
			return cssNew(func(e *ldNode) bool {
				a, ok := get(e)
				if !ok {
					return false
				}
				l := jsLen(a)
				if l < n {
					return strings.ToLower(a) == value && next.f(e)
				}
				return strings.ToLower(jsSlice(a, l-n, l)) == value && next.f(e)
			})
		}
		return cssNew(func(e *ldNode) bool {
			a, ok := get(e)
			return ok && strings.HasSuffix(a, value) && next.f(e)
		})
	case "any":
		if value == "" {
			return cssFalseFn
		}
		if ic {
			re := regexp.MustCompile("(?i)" + regexp.QuoteMeta(value))
			return cssNew(func(e *ldNode) bool {
				a, ok := get(e)
				return ok && re.MatchString(a) && next.f(e)
			})
		}
		return cssNew(func(e *ldNode) bool {
			a, ok := get(e)
			return ok && strings.Contains(a, value) && next.f(e)
		})
	case "not":
		if value == "" {
			return cssNew(func(e *ldNode) bool {
				a, _ := get(e)
				return a != "" && next.f(e)
			})
		}
		if ic {
			value = strings.ToLower(value)
			return cssNew(func(e *ldNode) bool {
				a, ok := get(e)
				return (!ok || strings.ToLower(a) != value) && next.f(e)
			})
		}
		return cssNew(func(e *ldNode) bool {
			a, ok := get(e)
			return (!ok || a != value) && next.f(e)
		})
	}
	return next
}

// ---- pseudo-classes --------------------------------------------------------------------------------------------

var cssAliases = map[string]string{
	"any-link": ":is(a, area, link)[href]",
	"link":     ":any-link:not(:visited)",
	"disabled": ":is(\n        :is(button, input, select, textarea, optgroup, option)[disabled],\n        optgroup[disabled] > option,\n        fieldset[disabled]:not(fieldset[disabled] legend:first-of-type *)\n    )",
	"enabled":  ":not(:disabled)",
	"checked":  ":is(:is(input[type=radio], input[type=checkbox])[checked], option:selected)",
	"required": ":is(input, select, textarea)[required]",
	"optional": ":is(input, select, textarea):not([required])",
	"selected": "option:is([selected], select:not([multiple]):not(:has(> option[selected])) > :first-of-type)",
	"checkbox": "[type=checkbox]",
	"file":     "[type=file]",
	"password": "[type=password]",
	"radio":    "[type=radio]",
	"reset":    "[type=reset]",
	"image":    "[type=image]",
	"submit":   "[type=submit]",
	"parent":   ":not(:empty)",
	"header":   ":is(h1, h2, h3, h4, h5, h6)",
	"button":   ":is(button, input[type=button])",
	"input":    ":is(input, textarea, select, button)",
	"text":     "input:is(:not([type!='']), [type=text])",
}

func cssCompilePseudo(next cssFn, sel cssTok, ctx *cssCtx) cssFn {
	name := sel.name
	if sel.isSubs {
		switch name {
		case "is", "matches", "where":
			return cssIs(next, sel.subs, ctx)
		case "not":
			return cssNot(next, sel.subs, ctx)
		case "has":
			return cssHas(next, sel.subs)
		}
		ldThrow("Unknown pseudo-class :" + name + "(" + cssSubsString(sel.subs) + ")")
	}
	if alias, ok := cssAliases[name]; ok {
		if sel.hasData {
			ldThrow("Pseudo " + name + " doesn't have any arguments")
		}
		return cssIs(next, cssParse(alias), ctx)
	}
	switch name {
	case "contains":
		text := sel.data
		return cssNew(func(e *ldNode) bool { return next.f(e) && strings.Contains(cssText(e), text) })
	case "icontains":
		text := strings.ToLower(sel.data)
		return cssNew(func(e *ldNode) bool { return next.f(e) && strings.Contains(strings.ToLower(cssText(e)), text) })
	case "nth-child", "nth-last-child", "nth-of-type", "nth-last-of-type":
		return cssNth(next, name, sel)
	case "root":
		return cssNew(func(e *ldNode) bool {
			p := e.parent
			return (p == nil || !cssIsTag(p)) && next.f(e)
		})
	case "scope":
		if ctx == nil || len(ctx.nodes) == 0 {
			return cssNew(func(e *ldNode) bool {
				p := e.parent
				return (p == nil || !cssIsTag(p)) && next.f(e)
			})
		}
		if len(ctx.nodes) == 1 {
			return cssNew(func(e *ldNode) bool { return ctx.nodes[0] == e && next.f(e) })
		}
		return cssNew(func(e *ldNode) bool {
			for _, c := range ctx.nodes {
				if c == e {
					return next.f(e)
				}
			}
			return false
		})
	case "hover", "visited", "active":
		return cssFalseFn
	case "empty", "first-child", "last-child", "first-of-type", "last-of-type", "only-of-type", "only-child":
		if sel.hasData {
			ldThrow("Pseudo-class :" + name + " doesn't have any arguments")
		}
		return cssNew(func(e *ldNode) bool { return cssStructural(name, e) && next.f(e) })
	}
	ldThrow("Unknown pseudo-class :" + name)
	return next
}

// cssSubsString is the array's string conversion in "Unknown pseudo-class :name(data)": the selectors' own tokens
// are not kept as text, so this is only used for the message of an unsupported functional pseudo-class.
func cssSubsString(subs [][]cssTok) string {
	parts := make([]string, len(subs))
	for i, s := range subs {
		parts[i] = "[object Object]"
		if len(s) > 1 {
			parts[i] = strings.Repeat("[object Object],", len(s)-1) + "[object Object]"
		}
	}
	return strings.Join(parts, ",")
}

func cssStructural(name string, e *ldNode) bool {
	switch name {
	case "empty":
		for _, c := range e.childNodes() {
			if cssIsTag(c) || cssText(c) != "" {
				return false
			}
		}
		return true
	case "first-child":
		return e.previousElementSibling() == nil
	case "last-child":
		sib := cssSiblings(e)
		for i := len(sib) - 1; i >= 0; i-- {
			if sib[i] == e {
				return true
			}
			if cssIsTag(sib[i]) {
				break
			}
		}
		return false
	case "first-of-type":
		en := strings.ToLower(e.name)
		for _, s := range cssSiblings(e) {
			if s == e {
				return true
			}
			if cssIsTag(s) && strings.ToLower(s.name) == en {
				break
			}
		}
		return false
	case "last-of-type":
		en := strings.ToLower(e.name)
		sib := cssSiblings(e)
		for i := len(sib) - 1; i >= 0; i-- {
			if sib[i] == e {
				return true
			}
			if cssIsTag(sib[i]) && strings.ToLower(sib[i].name) == en {
				break
			}
		}
		return false
	case "only-of-type":
		en := strings.ToLower(e.name)
		for _, s := range cssSiblings(e) {
			if s != e && cssIsTag(s) && strings.ToLower(s.name) == en {
				return false
			}
		}
		return true
	case "only-child":
		for _, s := range cssSiblings(e) {
			if s != e && cssIsTag(s) {
				return false
			}
		}
		return true
	}
	return false
}

// cssNthParse is nth-check's parse.
func cssNthParse(formula string) (a, b int) {
	formula = strings.ToLower(strings.TrimFunc(formula, jsSpace))
	if formula == "even" {
		return 2, 0
	}
	if formula == "odd" {
		return 2, 1
	}
	idx := 0
	readSign := func() int {
		if idx < len(formula) && formula[idx] == '-' {
			idx++
			return -1
		}
		if idx < len(formula) && formula[idx] == '+' {
			idx++
		}
		return 1
	}
	readNumber := func() (int, bool) {
		start := idx
		v := 0
		for idx < len(formula) && formula[idx] >= '0' && formula[idx] <= '9' {
			v = v*10 + int(formula[idx]-'0')
			idx++
		}
		return v, idx != start
	}
	skipWS := func() {
		for idx < len(formula) && cssIsWS(formula[idx]) {
			idx++
		}
	}
	sign := readSign()
	number, ok := readNumber()
	if idx < len(formula) && formula[idx] == 'n' {
		idx++
		if !ok {
			number = 1
		}
		a = sign * number
		skipWS()
		if idx < len(formula) {
			sign = readSign()
			skipWS()
			number, ok = readNumber()
		} else {
			sign, number, ok = 0, 0, true
		}
	}
	if !ok || idx < len(formula) {
		ldThrow("n-th rule couldn't be parsed ('" + formula + "')")
	}
	return a, sign * number
}

func cssNth(next cssFn, name string, sel cssTok) cssFn {
	a, b := cssNthParse(sel.data)
	b-- // one- to zero-indexed
	var check func(int) bool
	switch {
	case b < 0 && a <= 0:
		return cssFalseFn
	case a == -1:
		check = func(i int) bool { return i <= b }
	case a == 0:
		check = func(i int) bool { return i == b }
	case a == 1:
		if b < 0 {
			// every element: only the parent test of getChildFunc remains
			return cssNew(func(e *ldNode) bool { p := e.parent; return p != nil && cssIsTag(p) && next.f(e) })
		}
		check = func(i int) bool { return i >= b }
	default:
		absA := a
		if absA < 0 {
			absA = -absA
		}
		bMod := ((b % absA) + absA) % absA
		if a > 1 {
			check = func(i int) bool { return i >= b && i%absA == bMod }
		} else {
			check = func(i int) bool { return i <= b && i%absA == bMod }
		}
	}
	return cssNew(func(e *ldNode) bool {
		sib := cssSiblings(e)
		pos := 0
		switch name {
		case "nth-child":
			for _, s := range sib {
				if s == e {
					break
				}
				if cssIsTag(s) {
					pos++
				}
			}
		case "nth-last-child":
			for i := len(sib) - 1; i >= 0; i-- {
				if sib[i] == e {
					break
				}
				if cssIsTag(sib[i]) {
					pos++
				}
			}
		case "nth-of-type":
			for _, s := range sib {
				if s == e {
					break
				}
				if cssIsTag(s) && strings.ToLower(s.name) == strings.ToLower(e.name) {
					pos++
				}
			}
		case "nth-last-of-type":
			for i := len(sib) - 1; i >= 0; i-- {
				if sib[i] == e {
					break
				}
				if cssIsTag(sib[i]) && strings.ToLower(sib[i].name) == strings.ToLower(e.name) {
					pos++
				}
			}
		}
		return check(pos) && next.f(e)
	})
}

func cssIs(next cssFn, subs [][]cssTok, ctx *cssCtx) cssFn {
	fn := cssCompileToken(cssClone(subs), nil, ctx)
	switch fn.kind {
	case cssTrue:
		return next
	case cssFalse:
		return cssFalseFn
	}
	return cssNew(func(e *ldNode) bool { return fn.f(e) && next.f(e) })
}

func cssNot(next cssFn, subs [][]cssTok, ctx *cssCtx) cssFn {
	fn := cssCompileToken(cssClone(subs), nil, ctx)
	switch fn.kind {
	case cssFalse:
		return next
	case cssTrue:
		return cssFalseFn
	}
	return cssNew(func(e *ldNode) bool { return !fn.f(e) && next.f(e) })
}

func cssHas(next cssFn, subs [][]cssTok) cssFn {
	var ctx *cssCtx
	for _, s := range subs {
		for _, t := range s {
			if t.isTraversalSort() {
				ctx = &cssCtx{nodes: []*ldNode{nil}, isArray: true}
			}
		}
	}
	compiled := cssCompileToken(cssClone(subs), nil, ctx)
	if compiled.kind == cssFalse {
		return cssFalseFn
	}
	hasElement := cssEnsureIsTag(compiled)
	if ctx != nil && compiled.kind != cssTrue {
		testNext := compiled.shouldTestNextSiblings
		return cssNew(func(e *ldNode) bool {
			if !next.f(e) {
				return false
			}
			ctx.nodes[0] = e
			elems := e.childNodes()
			if testNext {
				for s := e.next; s != nil; s = s.next {
					if cssIsTag(s) {
						elems = append(elems, s)
					}
				}
			}
			return cssExistsOne(hasElement.f, elems)
		})
	}
	return cssNew(func(e *ldNode) bool { return next.f(e) && cssExistsOne(hasElement.f, e.childNodes()) })
}

// ---- the DOM methods -------------------------------------------------------------------------------------------

func cssScopeFor(el *ldNode, selector string) *ldNode {
	if strings.Contains(selector, ":scope") {
		return el
	}
	return nil
}

// ldQuerySelectorAll is ParentNode.querySelectorAll: every element below root, in document order, that matches;
// the content of a template element is skipped. It throws the library's error for a selector it cannot parse.
func ldQuerySelectorAll(root *ldNode, selector string) []*ldNode {
	match := cssCompile(selector, cssScopeFor(root, selector))
	var out []*ldNode
	ldEachElement(root, func(e *ldNode) bool {
		if match.f(e) {
			out = append(out, e)
		}
		return true
	})
	return out
}

// ldQuerySelector is ParentNode.querySelector.
func ldQuerySelector(root *ldNode, selector string) *ldNode {
	match := cssCompile(selector, cssScopeFor(root, selector))
	var found *ldNode
	ldEachElement(root, func(e *ldNode) bool {
		if match.f(e) {
			found = e
			return false
		}
		return true
	})
	return found
}

// ldEachElement visits the elements below root in document order, not descending into a template.
func ldEachElement(root *ldNode, fn func(*ldNode) bool) {
	var visit func(n *ldNode) bool
	visit = func(n *ldNode) bool {
		for c := n.first; c != nil; c = c.next {
			if c.kind == ldElem {
				if !fn(c) {
					return false
				}
				if c.name == "template" {
					continue
				}
			}
			if c.first != nil && !visit(c) {
				return false
			}
		}
		return true
	}
	visit(root)
}

// ldMatches is Element.matches.
func ldMatches(el *ldNode, selector string) bool {
	return cssCompile(selector, cssScopeFor(el, selector)).f(el)
}

// ldClosest is Element.closest.
func ldClosest(el *ldNode, selector string) *ldNode {
	match := cssCompile(selector, cssScopeFor(el, selector))
	for cur := el; cur != nil; cur = cur.parentElement() {
		if match.f(cur) {
			return cur
		}
	}
	return nil
}
