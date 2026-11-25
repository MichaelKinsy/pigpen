package ask

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Minimal HTML tokenizer + tree builder ported from packages/utils/src/dom/parser.ts.
// Uses only the Go standard library (no golang.org/x/net/html).

type htmlNodeKind int

const (
	nodeDocument htmlNodeKind = iota
	nodeElement
	nodeText
	nodeComment
)

type htmlAttr struct {
	name  string
	value string
}

type htmlNode struct {
	kind       htmlNodeKind
	tag        string // lower-case local name for nodeElement
	svgNS      bool   // true for <svg> and its descendants
	text       string // decoded text for nodeText
	attrs      []htmlAttr
	rawHTML    string // raw outer HTML for <svg>
	parent     *htmlNode
	children   []*htmlNode
	hasControl bool // precomputed: this subtree contains input/select/textarea
}

func (n *htmlNode) getAttr(name string) (string, bool) {
	for _, a := range n.attrs {
		if a.name == name {
			return a.value, true
		}
	}
	return "", false
}

func (n *htmlNode) hasAttr(name string) bool {
	_, ok := n.getAttr(name)
	return ok
}

func (n *htmlNode) id() string {
	v, _ := n.getAttr("id")
	return v
}

func (n *htmlNode) textContent() string {
	var b strings.Builder
	var walk func(*htmlNode)
	walk = func(cur *htmlNode) {
		switch cur.kind {
		case nodeText:
			b.WriteString(cur.text)
		case nodeElement, nodeDocument:
			for _, c := range cur.children {
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}

func (n *htmlNode) appendChild(child *htmlNode) {
	child.parent = n
	n.children = append(n.children, child)
}

func (n *htmlNode) closest(tag string) *htmlNode {
	for cur := n; cur != nil; cur = cur.parent {
		if cur.kind == nodeElement && cur.tag == tag {
			return cur
		}
	}
	return nil
}

func (n *htmlNode) contains(target *htmlNode) bool {
	for cur := target; cur != nil; cur = cur.parent {
		if cur == n {
			return true
		}
	}
	return false
}

func (n *htmlNode) querySelector(tag string) *htmlNode {
	var found *htmlNode
	var walk func(*htmlNode)
	walk = func(cur *htmlNode) {
		if found != nil {
			return
		}
		for _, c := range cur.children {
			if c.kind == nodeElement && c.tag == tag {
				found = c
				return
			}
			walk(c)
			if found != nil {
				return
			}
		}
	}
	walk(n)
	return found
}

func (n *htmlNode) querySelectorAll(tags ...string) []*htmlNode {
	tagSet := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		tagSet[t] = struct{}{}
	}
	var out []*htmlNode
	var walk func(*htmlNode)
	walk = func(cur *htmlNode) {
		for _, c := range cur.children {
			if c.kind == nodeElement {
				if _, ok := tagSet[c.tag]; ok {
					out = append(out, c)
				}
			}
			walk(c)
		}
	}
	walk(n)
	return out
}

func (n *htmlNode) childElements() []*htmlNode {
	var out []*htmlNode
	for _, c := range n.children {
		if c.kind == nodeElement {
			out = append(out, c)
		}
	}
	return out
}

// computeHasControl marks every node whose subtree contains a form control
// in O(N) total time so list/table walkers never re-scan deeply nested trees.
func computeHasControl(n *htmlNode) bool {
	has := n.kind == nodeElement && (n.tag == "input" || n.tag == "select" || n.tag == "textarea")
	for _, c := range n.children {
		if computeHasControl(c) {
			has = true
		}
	}
	n.hasControl = has
	return has
}

// upstream: packages/utils/src/dom/parser.ts:12-27
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// upstream: packages/utils/src/dom/parser.ts:4
var rawTextElements = map[string]bool{
	"script": true, "style": true,
}

// upstream: packages/utils/src/dom/parser.ts:28-39
var closeOnOpen = map[string][]string{
	"li":     {"li"},
	"dt":     {"dt", "dd"},
	"dd":     {"dt", "dd"},
	"tr":     {"tr"},
	"th":     {"th", "td"},
	"td":     {"th", "td"},
	"option": {"option"},
	"thead":  {"thead", "tbody", "tfoot"},
	"tbody":  {"thead", "tbody", "tfoot"},
	"tfoot":  {"thead", "tbody", "tfoot"},
}

// upstream: packages/utils/src/dom/parser.ts:40-66
var pClosers = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true, "div": true,
	"dl": true, "fieldset": true, "footer": true, "form": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"header": true, "hr": true, "menu": true, "nav": true, "ol": true,
	"p": true, "pre": true, "section": true, "table": true, "ul": true,
}

// upstream: packages/utils/src/dom/parser.ts:68-181
var namedEntities = map[string]string{
	"AElig": "Æ", "Aacute": "Á", "Acirc": "Â", "Agrave": "À", "Aring": "Å", "Atilde": "Ã", "Auml": "Ä",
	"Ccedil": "Ç", "ETH": "Ð", "Eacute": "É", "Ecirc": "Ê", "Egrave": "È", "Euml": "Ë",
	"Iacute": "Í", "Icirc": "Î", "Igrave": "Ì", "Iuml": "Ï", "Ntilde": "Ñ",
	"Oacute": "Ó", "Ocirc": "Ô", "Ograve": "Ò", "Oslash": "Ø", "Otilde": "Õ", "Ouml": "Ö", "THORN": "Þ",
	"Uacute": "Ú", "Ucirc": "Û", "Ugrave": "Ù", "Uuml": "Ü", "Yacute": "Ý",
	"aacute": "á", "acirc": "â", "aelig": "æ", "agrave": "à", "aring": "å", "atilde": "ã", "auml": "ä",
	"amp": "&", "apos": "'", "bull": "•", "brvbar": "¦", "ccedil": "ç", "cedil": "¸", "cent": "¢",
	"copy": "©", "deg": "°", "curren": "¤", "divide": "÷", "emsp": "\u2003", "ensp": "\u2002", "euro": "€",
	"eacute": "é", "ecirc": "ê", "egrave": "è", "eth": "ð", "euml": "ë",
	"frac12": "½", "frac14": "¼", "frac34": "¾", "gt": ">",
	"iacute": "í", "icirc": "î", "iexcl": "¡", "igrave": "ì", "iquest": "¿", "iuml": "ï",
	"hellip": "…", "laquo": "«", "ldquo": "“", "lsquo": "‘", "lt": "<",
	"mdash": "—", "macr": "¯", "micro": "µ", "middot": "·", "nbsp": "\u00a0", "ndash": "–", "ntilde": "ñ",
	"oacute": "ó", "ocirc": "ô", "ograve": "ò", "ordf": "ª", "ordm": "º", "oslash": "ø", "otilde": "õ", "ouml": "ö",
	"para": "¶", "pound": "£", "plusmn": "±", "quot": `"`, "raquo": "»", "rdquo": "”", "reg": "®", "rsquo": "’",
	"sect": "§", "shy": "\u00ad", "szlig": "ß", "thorn": "þ", "sup1": "¹", "sup2": "²", "sup3": "³",
	"thinsp": "\u2009", "times": "×", "trade": "™",
	"uacute": "ú", "ucirc": "û", "ugrave": "ù", "uml": "¨", "uuml": "ü", "yacute": "ý", "yuml": "ÿ", "yen": "¥",
}

// decodeEntities ports packages/utils/src/dom/parser.ts:183-203:
// decodes only semicolon-terminated named entities from the fixed table and numeric character references.
func decodeEntities(value string) string {
	if !strings.ContainsRune(value, '&') {
		return value
	}
	var b strings.Builder
	b.Grow(len(value))
	i := 0
	for i < len(value) {
		amp := strings.IndexByte(value[i:], '&')
		if amp < 0 {
			b.WriteString(value[i:])
			break
		}
		b.WriteString(value[i : i+amp])
		i += amp
		semi := strings.IndexByte(value[i+1:], ';')
		if semi < 0 {
			b.WriteString(value[i:])
			break
		}
		entity := value[i+1 : i+1+semi]
		if decoded, ok := decodeOneEntity(entity); ok {
			b.WriteString(decoded)
			i += 1 + semi + 1
			continue
		}
		b.WriteByte('&')
		i++
	}
	return b.String()
}

func decodeOneEntity(entity string) (string, bool) {
	if len(entity) < 2 {
		return "", false
	}
	if entity[0] == '#' {
		var base int
		var digits string
		if len(entity) >= 3 && (entity[1] == 'x' || entity[1] == 'X') {
			base = 16
			digits = entity[2:]
			for k := 0; k < len(digits); k++ {
				c := digits[k]
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
					return "", false
				}
			}
		} else {
			base = 10
			digits = entity[1:]
			for k := 0; k < len(digits); k++ {
				if digits[k] < '0' || digits[k] > '9' {
					return "", false
				}
			}
		}
		if digits == "" {
			return "", false
		}
		cp, err := strconv.ParseInt(digits, base, 64)
		if err != nil || cp <= 0 || cp > 0x10ffff || (cp >= 0xd800 && cp <= 0xdfff) {
			return "\ufffd", true
		}
		return string(rune(cp)), true
	}
	// Named entity: /^[a-z][a-z0-9]+$/i
	c0 := entity[0]
	if !((c0 >= 'a' && c0 <= 'z') || (c0 >= 'A' && c0 <= 'Z')) {
		return "", false
	}
	for k := 1; k < len(entity); k++ {
		c := entity[k]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return "", false
		}
	}
	if s, ok := namedEntities[entity]; ok {
		return s, true
	}
	if s, ok := namedEntities[strings.ToLower(entity)]; ok {
		return s, true
	}
	return "", false
}

// isJSSpace matches JavaScript /\s/ (Unicode whitespace plus BOM U+FEFF).
func isJSSpace(r rune) bool {
	return unicode.IsSpace(r) || r == '\ufeff'
}

func findTagEnd(input string, start int) int {
	var quote byte
	for i := start; i < len(input); i++ {
		ch := input[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
		} else if ch == '"' || ch == '\'' {
			quote = ch
		} else if ch == '>' {
			return i
		}
	}
	return len(input) - 1
}

// parseStartTag ports packages/utils/src/dom/parser.ts:219-272.
func parseStartTag(source string) (string, []htmlAttr, bool, bool) {
	i := 0
	for i < len(source) {
		r, sz := utf8.DecodeRuneInString(source[i:])
		if !isJSSpace(r) {
			break
		}
		i += sz
	}
	nameStart := i
	for i < len(source) {
		r, sz := utf8.DecodeRuneInString(source[i:])
		if isJSSpace(r) || r == '/' || r == '>' {
			break
		}
		i += sz
	}
	if i == nameStart {
		return "", nil, false, false
	}
	name := strings.ToLower(source[nameStart:i])
	var attrs []htmlAttr
	selfClosing := false
	for i < len(source) {
		for i < len(source) {
			r, sz := utf8.DecodeRuneInString(source[i:])
			if !isJSSpace(r) {
				break
			}
			i += sz
		}
		if i >= len(source) {
			break
		}
		if source[i] == '/' {
			selfClosing = true
			break
		}
		attrStart := i
		for i < len(source) {
			r, sz := utf8.DecodeRuneInString(source[i:])
			if isJSSpace(r) || r == '=' || r == '/' || r == '>' {
				break
			}
			i += sz
		}
		if i == attrStart {
			break
		}
		attrName := strings.ToLower(source[attrStart:i])
		for i < len(source) {
			r, sz := utf8.DecodeRuneInString(source[i:])
			if !isJSSpace(r) {
				break
			}
			i += sz
		}
		val := ""
		if i < len(source) && source[i] == '=' {
			i++
			for i < len(source) {
				r, sz := utf8.DecodeRuneInString(source[i:])
				if !isJSSpace(r) {
					break
				}
				i += sz
			}
			if i < len(source) && (source[i] == '"' || source[i] == '\'') {
				q := source[i]
				i++
				end := strings.IndexByte(source[i:], q)
				if end < 0 {
					val = source[i:]
					i = len(source)
				} else {
					val = source[i : i+end]
					i += end + 1
				}
			} else {
				valStart := i
				for i < len(source) {
					r, sz := utf8.DecodeRuneInString(source[i:])
					if isJSSpace(r) || r == '>' {
						break
					}
					i += sz
				}
				val = source[valStart:i]
			}
		}
		dup := false
		for _, existing := range attrs {
			if existing.name == attrName {
				dup = true
				break
			}
		}
		if !dup {
			attrs = append(attrs, htmlAttr{name: attrName, value: decodeEntities(val)})
		}
	}
	return name, attrs, selfClosing, true
}

// closeOptionalElements ports packages/utils/src/dom/parser.ts:274-283.
func closeOptionalElements(stack *[]*htmlNode, incoming string) {
	if len(*stack) == 0 {
		return
	}
	cur := (*stack)[len(*stack)-1]
	if cur.tag == "p" && pClosers[incoming] {
		*stack = (*stack)[:len(*stack)-1]
		return
	}
	if closable, ok := closeOnOpen[incoming]; ok {
		for _, c := range closable {
			if cur.tag == c {
				*stack = (*stack)[:len(*stack)-1]
				return
			}
		}
	}
}

func isDoctypeAt(input string, index int) bool {
	if index+9 > len(input) {
		return false
	}
	if !strings.EqualFold(input[index:index+9], "<!doctype") {
		return false
	}
	if index+9 == len(input) {
		return true
	}
	c := input[index+9]
	return !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_')
}

// parseHTMLDocument ports packages/utils/src/dom/parser.ts:286-380.
func parseHTMLDocument(input string) *htmlNode {
	doc := &htmlNode{kind: nodeDocument}
	var stack []*htmlNode
	parent := doc

	// Record start positions of open <svg> nodes so outerHTML is sliced in O(1)
	// when </svg> closes (or at EOF if unclosed), avoiding quadratic re-scanning.
	svgStartPos := make(map[*htmlNode]int)

	index := 0
	n := len(input)
	for index < n {
		if strings.HasPrefix(input[index:], "<!--") {
			end := strings.Index(input[index+4:], "-->")
			contentEnd := n
			if end >= 0 {
				contentEnd = index + 4 + end
			}
			parent.appendChild(&htmlNode{kind: nodeComment, text: input[index+4 : contentEnd]})
			if end < 0 {
				index = n
			} else {
				index = contentEnd + 3
			}
			continue
		}
		if input[index] != '<' {
			next := strings.IndexByte(input[index:], '<')
			end := n
			if next >= 0 {
				end = index + next
			}
			parent.appendChild(&htmlNode{kind: nodeText, text: decodeEntities(input[index:end])})
			index = end
			continue
		}
		if index+1 < n && input[index+1] == '!' && isDoctypeAt(input, index) {
			end := strings.IndexByte(input[index+2:], '>')
			if end < 0 {
				index = n
			} else {
				index = index + 2 + end + 1
			}
			continue
		}
		if strings.HasPrefix(input[index:], "<![CDATA[") {
			end := strings.Index(input[index+9:], "]]>")
			contentEnd := n
			if end >= 0 {
				contentEnd = index + 9 + end
			}
			parent.appendChild(&htmlNode{kind: nodeText, text: input[index+9 : contentEnd]})
			if end < 0 {
				index = n
			} else {
				index = contentEnd + 3
			}
			continue
		}
		if index+1 < n && input[index+1] == '/' {
			end := findTagEnd(input, index+2)
			rawClose := strings.TrimSpace(input[index+2 : end])
			if sp := strings.IndexFunc(rawClose, isJSSpace); sp >= 0 {
				rawClose = rawClose[:sp]
			}
			closingName := strings.ToLower(rawClose)
			matchIdx := -1
			for k := len(stack) - 1; k >= 0; k-- {
				if stack[k].tag == closingName {
					matchIdx = k
					break
				}
			}
			closeEndPos := end + 1
			if closeEndPos > n {
				closeEndPos = n
			}
			if matchIdx >= 0 {
				// Finalize rawHTML for any <svg> elements being popped.
				for k := len(stack) - 1; k >= matchIdx; k-- {
					if st, ok := svgStartPos[stack[k]]; ok {
						if k == matchIdx {
							stack[k].rawHTML = input[st:closeEndPos]
						} else {
							stack[k].rawHTML = input[st:index] + "</svg>"
						}
						delete(svgStartPos, stack[k])
					}
				}
				stack = stack[:matchIdx]
			}
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			} else {
				parent = doc
			}
			index = closeEndPos
			continue
		}
		if index+1 < n && (input[index+1] == '!' || input[index+1] == '?') {
			end := strings.IndexByte(input[index+2:], '>')
			if end < 0 {
				index = n
			} else {
				index = index + 2 + end + 1
			}
			continue
		}
		tagStart := index
		end := findTagEnd(input, index+1)
		inner := ""
		if index+1 <= end {
			if end < n && input[end] == '>' {
				inner = input[index+1 : end]
			} else {
				inner = input[index+1:]
			}
		}
		tagName, attrs, selfClosing, ok := parseStartTag(inner)
		if !ok {
			parent.appendChild(&htmlNode{kind: nodeText, text: "<"})
			index++
			continue
		}
		closeOptionalElements(&stack, tagName)
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		} else {
			parent = doc
		}
		svgNS := (parent != doc && parent.svgNS) || tagName == "svg"
		el := &htmlNode{kind: nodeElement, tag: tagName, svgNS: svgNS, attrs: attrs}
		parent.appendChild(el)
		index = end + 1
		if index > n {
			index = n
		}
		if rawTextElements[tagName] && !selfClosing {
			closeStart, closeAfter := findRawTextClose(input, index, tagName)
			if closeStart >= 0 {
				el.appendChild(&htmlNode{kind: nodeText, text: input[index:closeStart]})
				index = closeAfter
			} else {
				el.appendChild(&htmlNode{kind: nodeText, text: input[index:]})
				index = n
			}
			continue
		}
		isVoid := voidElements[tagName] || (svgNS && selfClosing)
		if tagName == "svg" {
			if isVoid {
				el.rawHTML = input[tagStart:index]
			} else {
				svgStartPos[el] = tagStart
			}
		}
		if !isVoid {
			stack = append(stack, el)
			parent = el
		}
	}
	// Close any unclosed <svg> elements at EOF in O(1) each.
	for el, st := range svgStartPos {
		el.rawHTML = input[st:]
	}
	computeHasControl(doc)
	return doc
}

func findRawTextClose(input string, from int, tag string) (int, int) {
	needle := "</" + tag
	nLen := len(needle)
	for i := from; i+nLen <= len(input); i++ {
		if input[i] == '<' && input[i+1] == '/' && strings.EqualFold(input[i:i+nLen], needle) {
			j := i + nLen
			for j < len(input) {
				r, sz := utf8.DecodeRuneInString(input[j:])
				if !isJSSpace(r) {
					break
				}
				j += sz
			}
			if j < len(input) && input[j] == '>' {
				return i, j + 1
			}
		}
	}
	return -1, -1
}
