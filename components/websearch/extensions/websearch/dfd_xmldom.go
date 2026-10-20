package websearch

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// A port of the part of @xmldom/xmldom 0.9.12 (MIT) that mathml-to-latex 1.8.0 uses:
// new DOMParser({onError}).parseFromString(xml, 'text/xml') (dom-parser.js, sax.js, grammar.js, and the
// document operations the DOM handler performs). The tree it builds is the minimum its caller reads: element
// names, attributes in order, and the child nodes with their kinds.

const (
	xmlNS      = "http://www.w3.org/XML/1998/namespace"
	xmlnsNS    = "http://www.w3.org/2000/xmlns/"
	xElement   = 1
	xText      = 3
	xCDATA     = 4
	xPI        = 7
	xComment   = 8
	xDocument  = 9
	xDocType   = 10
)

type xNode struct {
	kind     int
	name     string // tagName of an element
	data     string // data of character nodes
	attrs    []*xAttr
	children []*xNode
	parent   *xNode
}

type xAttr struct {
	qName, value string
	prefix       string
	hasPrefix    bool
	local        string
	uri          string
}

func (n *xNode) nodeName() string {
	switch n.kind {
	case xText:
		return "#text"
	case xCDATA:
		return "#cdata-section"
	case xComment:
		return "#comment"
	case xDocument:
		return "#document"
	}
	return n.name
}

func (n *xNode) textContent() string {
	var b strings.Builder
	var walk func(*xNode)
	walk = func(m *xNode) {
		for _, c := range m.children {
			switch c.kind {
			case xElement:
				walk(c)
			case xText, xCDATA:
				b.WriteString(c.data)
			}
		}
	}
	walk(n)
	return b.String()
}

// xParseError is a ParseError thrown by the parser (a fatal error); xDOMException a DOMException of the DOM
// operations; xJSError any other exception of the parse loop (a TypeError or Error of the library code).
type xParseError struct{ msg string }
type xDOMException struct{ msg string }
type xJSError struct{ msg string }

type xHandler struct {
	doc            *xNode
	currentElement *xNode
	cdata          bool
	onError        func(level, msg string)
}

func (h *xHandler) report(level, msg string) {
	if h.onError != nil {
		h.onError(level, msg)
	}
}
func (h *xHandler) warning(msg string) { h.report("warning", msg) }
func (h *xHandler) error(msg string)   { h.report("error", msg) }
func (h *xHandler) fatalError(msg string) {
	h.report("fatalError", msg)
	panic(&xParseError{msg})
}

const (
	xNameStart = `[:_a-zA-Z\x{C0}-\x{D6}\x{D8}-\x{F6}\x{F8}-\x{2FF}\x{370}-\x{1FFF}\x{200C}-\x{200D}\x{2070}-\x{218F}\x{2C00}-\x{2FEF}\x{3001}-\x{D7FF}\x{F900}-\x{FDCF}\x{FDF0}-\x{FFFD}\x{10000}-\x{10FFFF}]`
	xNameChar  = `[:_a-zA-Z\x{C0}-\x{D6}\x{D8}-\x{F6}\x{F8}-\x{2FF}\x{370}-\x{1FFF}\x{200C}-\x{200D}\x{2070}-\x{218F}\x{2C00}-\x{2FEF}\x{3001}-\x{D7FF}\x{F900}-\x{FDCF}\x{FDF0}-\x{FFFD}\x{10000}-\x{10FFFF}\-.0-9\x{B7}\x{300}-\x{36F}\x{203F}-\x{2040}]`
	xNCStart   = `[_a-zA-Z\x{C0}-\x{D6}\x{D8}-\x{F6}\x{F8}-\x{2FF}\x{370}-\x{1FFF}\x{200C}-\x{200D}\x{2070}-\x{218F}\x{2C00}-\x{2FEF}\x{3001}-\x{D7FF}\x{F900}-\x{FDCF}\x{FDF0}-\x{FFFD}\x{10000}-\x{10FFFF}]`
	xNCChar    = `[_a-zA-Z\x{C0}-\x{D6}\x{D8}-\x{F6}\x{F8}-\x{2FF}\x{370}-\x{1FFF}\x{200C}-\x{200D}\x{2070}-\x{218F}\x{2C00}-\x{2FEF}\x{3001}-\x{D7FF}\x{F900}-\x{FDCF}\x{FDF0}-\x{FFFD}\x{10000}-\x{10FFFF}\-.0-9\x{B7}\x{300}-\x{36F}\x{203F}-\x{2040}]`
	xChar      = `[\x{9}\x{A}\x{D}\x{20}-\x{D7FF}\x{E000}-\x{FFFD}\x{10000}-\x{10FFFF}]`
	xCharNoDsh = `[\x{9}\x{A}\x{D}\x{20}-\x{2C}\x{2E}-\x{D7FF}\x{E000}-\x{FFFD}\x{10000}-\x{10FFFF}]`
	xName      = xNameStart + xNameChar + `*`
	xNCName    = xNCStart + xNCChar + `*`
	xQName     = xNCName + `(?::` + xNCName + `)?`
)

var (
	xQNameExact   = regexp.MustCompile(`^` + xQName + `$`)
	xEntityReg    = regexp.MustCompile(`&#?\w+;?`)
	xReference    = regexp.MustCompile(`(?:&` + xName + `;|&#[0-9]+;|&#x[0-9a-fA-F]+;)`)
	xEndTagStrict = regexp.MustCompile(`^(` + xQName + `)[\x{20}\x{9}\x{D}\x{A}]*$`)
	xEndTagLead   = regexp.MustCompile(`^(` + xQName + `)`)
	xCommentRe    = regexp.MustCompile(`^<!--(?:` + xCharNoDsh + `|-` + xCharNoDsh + `)*-->`)
	xCDSectRe     = regexp.MustCompile(`^<!\[CDATA\[` + xChar + `*?\]\]>`)
	xSOptRe       = regexp.MustCompile(`[\x{20}\x{9}\x{D}\x{A}]*`)
	xNameAt       = regexp.MustCompile(`^` + xName)
	xPEReference  = regexp.MustCompile(`^%` + xName + `;`)
	xXMLDecl      = regexp.MustCompile(`^<\?xml[\x{20}\x{9}\x{D}\x{A}]+version[\x{20}\x{9}\x{D}\x{A}]*=[\x{20}\x{9}\x{D}\x{A}]*(?:'1[.]\d+'|"1[.]\d+")(?:[\x{20}\x{9}\x{D}\x{A}]+encoding[\x{20}\x{9}\x{D}\x{A}]*=[\x{20}\x{9}\x{D}\x{A}]*(?:"[A-Za-z][-A-Za-z0-9._]*"|'[A-Za-z][-A-Za-z0-9._]*'))?(?:[\x{20}\x{9}\x{D}\x{A}]+standalone[\x{20}\x{9}\x{D}\x{A}]*=[\x{20}\x{9}\x{D}\x{A}]*(?:'(?:yes|no)'|"(?:yes|no)"))?[\x{20}\x{9}\x{D}\x{A}]*\?>`)
)

const xS = `[\x{20}\x{9}\x{D}\x{A}]+`
const xSO = `[\x{20}\x{9}\x{D}\x{A}]*`

// The markup declarations of a DOCTYPE internal subset (grammar.js).
var (
	xSystemLiteral = `(?:"[^"]*"|'[^']*')`
	xPubidChar     = `[\x{20}\x{D}\x{A}a-zA-Z0-9\-'()+,./:=?;!*#@$_%]`
	xPubidLiteral  = `(?:"` + xPubidChar + `*"|'` + strings.Replace(xPubidChar, `'`, ``, 1) + `*')`
	xExternalID    = `(?:SYSTEM` + xS + xSystemLiteral + `|PUBLIC` + xS + xPubidLiteral + xS + xSystemLiteral + `)`
	xRefRe         = `(?:&` + xName + `;|&#[0-9]+;|&#x[0-9a-fA-F]+;)`
	xPERef         = `%` + xName + `;`
	xEntityValue   = `(?:"(?:[^%&"]|` + xPERef + `|` + xRefRe + `)*"|'(?:[^%&']|` + xPERef + `|` + xRefRe + `)*')`
	xAttValue      = `(?:"(?:[^<&"]|` + xRefRe + `)*"|'(?:[^<&']|` + xRefRe + `)*')`
	xNmtoken       = xNameChar + `+`
	xMixed         = `(?:\(` + xSO + `#PCDATA(?:` + xSO + `\|` + xSO + xQName + `)*` + xSO + `\)\*|\(` + xSO + `#PCDATA` + xSO + `\))`
	xContentSpec   = `(?:EMPTY|ANY|` + xMixed + `|\([^>]+\)[?*+]?)`
	xElementDecl   = regexp.MustCompile(`^<!ELEMENT` + xS + `(?:` + xQName + `|` + xPERef + `)` + xS + `(?:` + xContentSpec + `|` + xPERef + `)` + xSO + `>`)
	xNotationType  = `NOTATION` + xS + `\(` + xSO + xName + `(?:` + xSO + `\|` + xSO + xName + `)*` + xSO + `\)`
	xEnumeration   = `\(` + xSO + xNmtoken + `(?:` + xSO + `\|` + xSO + xNmtoken + `)*` + xSO + `\)`
	xAttType       = `(?:CDATA|ID|IDREF|IDREFS|ENTITY|ENTITIES|NMTOKEN|NMTOKENS|` + xNotationType + `|` + xEnumeration + `)`
	xDefaultDecl   = `(?:#REQUIRED|#IMPLIED|(?:#FIXED` + xS + `)?` + xAttValue + `)`
	xAttDef        = `(?:` + xS + xName + xS + xAttType + xS + xDefaultDecl + `)`
	xAttlistDecl   = regexp.MustCompile(`^<!ATTLIST` + xS + xName + xAttDef + `*` + xSO + `>`)
	xGEDecl        = `<!ENTITY` + xS + xName + xS + `(?:` + xEntityValue + `|` + xExternalID + `(?:` + xS + `NDATA` + xS + xName + `)?)` + xSO + `>`
	xPEDecl        = `<!ENTITY` + xS + `%` + xS + xName + xS + `(?:` + xEntityValue + `|` + xExternalID + `)` + xSO + `>`
	xEntityDecl    = regexp.MustCompile(`^(?:` + xGEDecl + `|` + xPEDecl + `)`)
	xNotationDecl  = regexp.MustCompile(`^<!NOTATION` + xS + xName + xS + `(?:` + xExternalID + `|PUBLIC` + xS + xPubidLiteral + `)` + xSO + `>`)
	xExternalIDRe  = regexp.MustCompile(`^(?:SYSTEM` + xS + `(?P<SystemLiteralOnly>` + xSystemLiteral + `)|PUBLIC` + xS + `(?P<PubidLiteral>` + xPubidLiteral + `)` + xS + `(?P<SystemLiteral>` + xSystemLiteral + `))`)
)

func xIsSpaceRune(r rune) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\n' }

func xLeadingSpaces(rs []rune, i int) int {
	n := 0
	for i+n < len(rs) && xIsSpaceRune(rs[i+n]) {
		n++
	}
	return n
}

// xParseIntJS is parseInt(s) for the strings entityReplacer passes: decimal digits or 0x followed by hex digits.
func xParseIntJS(s string) float64 {
	if strings.HasPrefix(s, "0x") {
		f := 0.0
		for _, c := range s[2:] {
			d := strings.IndexRune("0123456789abcdef", c|0x20)
			if d < 0 || c > 'f' {
				break
			}
			f = f*16 + float64(d)
		}
		if len(s) == 2 {
			return math.NaN()
		}
		return f
	}
	f := 0.0
	any := false
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		f = f*10 + float64(c-'0')
		any = true
	}
	if !any {
		return math.NaN()
	}
	return f
}

func xToInt32(f float64) int32 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	f = math.Trunc(f)
	f = math.Mod(f, 4294967296)
	if f < 0 {
		f += 4294967296
	}
	return int32(uint32(f))
}

// xFromCharCodes is String.fromCharCode(units...) as UTF-8; a lone surrogate (not representable in a Go string)
// becomes U+FFFD, the one place this port differs from the original (see port/defuddle-math-notes.md).
func xFromCharCodes(units ...uint16) string {
	return string(utf16.Decode(units))
}

func xFixedFromCharCode(code float64) string {
	if code > 0xffff {
		code -= 0x10000
		c := xToInt32(code)
		s1 := uint16(0xd800 + (c >> 10))
		s2 := uint16(0xdc00 + (c & 0x3ff))
		return xFromCharCodes(s1, s2)
	}
	if math.IsNaN(code) {
		return xFromCharCodes(0)
	}
	return xFromCharCodes(uint16(xToInt32(code)))
}

func xValidateAndExtract(ns, qName string) (string, string, string) {
	if !xQNameExact.MatchString(qName) {
		panic(&xDOMException{"invalid character in qualified name \"" + qName + "\""})
	}
	prefix, hasPrefix, local := "", false, qName
	if i := strings.IndexByte(qName, ':'); i >= 0 {
		parts := strings.Split(qName, ":")
		prefix, hasPrefix, local = parts[0], true, parts[1]
	}
	if hasPrefix && ns == "" {
		panic(&xDOMException{"prefix is non-null and namespace is null"})
	}
	if hasPrefix && prefix == "xml" && ns != xmlNS {
		panic(&xDOMException{"prefix is \"xml\" and namespace is not the XML namespace"})
	}
	if ((hasPrefix && prefix == "xmlns") || qName == "xmlns") && ns != xmlnsNS {
		panic(&xDOMException{"either qualifiedName or prefix is \"xmlns\" and namespace is not the XMLNS namespace"})
	}
	if ns == xmlnsNS && !(hasPrefix && prefix == "xmlns") && qName != "xmlns" {
		panic(&xDOMException{"namespace is the XMLNS namespace and neither qualifiedName nor prefix is \"xmlns\""})
	}
	return ns, prefix, local
}

type xNSScope struct {
	parent *xNSScope
	m      map[string]string
}

func (s *xNSScope) lookup(prefix string) string {
	for ; s != nil; s = s.parent {
		if v, ok := s.m[prefix]; ok {
			return v
		}
	}
	return ""
}

type xFrame struct {
	currentNSMap *xNSScope
	localNSMap   map[string]bool
	hasLocal     bool
	closed       bool
}

type xElAttrs struct {
	tagName string
	attrs   []*xAttr
	names   map[string]bool
	closed  bool
}

func (e *xElAttrs) setTagName(name string) {
	if !xQNameExact.MatchString(name) {
		panic(&xJSError{"invalid tagName:" + name})
	}
	e.tagName = name
}

func (e *xElAttrs) addValue(qName, value string) {
	if !xQNameExact.MatchString(qName) {
		panic(&xJSError{"invalid attribute:" + qName})
	}
	e.names[qName] = true
	e.attrs = append(e.attrs, &xAttr{qName: qName, value: value})
}

// xParse is DOMParser.parseFromString(source, 'text/xml'): it returns the document, or nil when parsing threw.
func xParse(source string, onError func(level, msg string)) (doc *xNode) {
	h := &xHandler{onError: onError}
	defer func() {
		if r := recover(); r != nil {
			switch r.(type) {
			case *xParseError, *xDOMException, *xJSError:
				doc = nil
			default:
				panic(r)
			}
		}
	}()
	source = xNormalizeLineEndings(source)
	h.doc = &xNode{kind: xDocument}
	root := &xNSScope{m: map[string]string{"": "", "xml": xmlNS}}
	xSaxParse([]rune(source), root, h)
	if !xDocumentElement(h.doc) {
		h.fatalError("missing root element")
	}
	return h.doc
}

var xLineEndA = regexp.MustCompile("\r[\n\u0085]")
var xLineEndB = regexp.MustCompile("[\r\u0085\u2028\u2029]")

func xNormalizeLineEndings(s string) string {
	return xLineEndB.ReplaceAllLiteralString(xLineEndA.ReplaceAllLiteralString(s, "\n"), "\n")
}

func xDocumentElement(doc *xNode) bool {
	for _, c := range doc.children {
		if c.kind == xElement {
			return true
		}
	}
	return false
}

func (h *xHandler) appendNode(n *xNode) {
	parent := h.currentElement
	if parent == nil {
		parent = h.doc
	}
	h.insert(parent, n)
}

// insert is parent.appendChild(n) with the checks of _insertBefore that can fail while parsing.
func (h *xHandler) insert(parent, n *xNode) {
	if parent.kind == xDocument {
		switch n.kind {
		case xElement:
			for _, c := range parent.children {
				if c.kind == xElement {
					panic(&xDOMException{"Only one element can be added and only after doctype"})
				}
			}
		case xDocType:
			for _, c := range parent.children {
				if c.kind == xDocType {
					panic(&xDOMException{"Only one doctype is allowed"})
				}
				if c.kind == xElement {
					panic(&xDOMException{"Doctype can not be appended since element is present"})
				}
			}
		}
	} else if n.kind == xDocType {
		panic(&xDOMException{"Unexpected node type 10 for parent node type 1"})
	} else if parent.kind != xElement {
		panic(&xDOMException{"Unexpected parent node type"})
	}
	n.parent = parent
	parent.children = append(parent.children, n)
}

func (h *xHandler) characters(chars string) {
	if chars == "" {
		return
	}
	kind := xText
	if h.cdata {
		kind = xCDATA
	}
	node := &xNode{kind: kind, data: chars}
	if h.currentElement != nil {
		h.insert(h.currentElement, node)
	} else if strings.TrimFunc(chars, isJSSpace) == "" {
		h.insert(h.doc, node)
	}
}

func (h *xHandler) startElement(ns, qName string, el *xElAttrs) {
	xValidateAndExtract(ns, qName)
	node := &xNode{kind: xElement, name: qName}
	h.appendNode(node)
	h.currentElement = node
	for _, a := range el.attrs {
		xValidateAndExtract(a.uri, a.qName)
		// setAttributeNode replaces an attribute with the same namespace and local name in place.
		replaced := false
		for i, old := range node.attrs {
			if old.uri == a.uri && old.local == a.local {
				node.attrs[i] = a
				replaced = true
				break
			}
		}
		if !replaced {
			node.attrs = append(node.attrs, a)
		}
	}
}

func (h *xHandler) endElement() {
	if h.currentElement == nil {
		panic(&xJSError{"Cannot read properties of undefined (reading 'parentNode')"})
	}
	h.currentElement = h.currentElement.parent
}

func xSaxParse(src []rune, defaultNS *xNSScope, h *xHandler) {
	source := string(src)
	if strings.ContainsRune(source, '\uFFFD') {
		h.warning("Unicode replacement character detected, source encoding issues?")
	}
	sub := func(a, b int) string { return string(src[a:b]) }
	indexOf := func(r rune, from int) int {
		for i := from; i < len(src); i++ {
			if src[i] == r {
				return i
			}
		}
		return -1
	}
	charAt := func(i int) rune {
		if i < 0 || i >= len(src) {
			return -1
		}
		return src[i]
	}

	entityReplacer := func(a string) string {
		complete := a
		if !strings.HasSuffix(a, ";") {
			complete = a + ";"
		}
		if complete != a {
			h.error("EntityRef: expecting ;")
			return a
		}
		loc := xReference.FindStringIndex(complete)
		if loc == nil || loc[1]-loc[0] != len(complete) {
			h.error("entity not matching Reference production: " + a)
			return a
		}
		k := complete[1 : len(complete)-1]
		switch k {
		case "amp":
			return "&"
		case "lt":
			return "<"
		case "gt":
			return ">"
		case "quot":
			return "\""
		case "apos":
			return "'"
		}
		if k[0] == '#' {
			return xFixedFromCharCode(xParseIntJS(strings.Replace(k[1:], "x", "0x", 1)))
		}
		h.error("entity not found:" + a)
		return a
	}
	replaceEntities := func(s string) string {
		return xEntityReg.ReplaceAllStringFunc(s, entityReplacer)
	}

	start := 0
	appendText := func(end int) {
		if end > start {
			xt := replaceEntities(sub(start, min(end, len(src))))
			h.characters(xt)
			start = end
		}
	}

	parseStack := []*xFrame{{currentNSMap: defaultNS}}
	var unclosedTags []string
	end := 0
	for {
		tagStart := 0
		stop := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					switch e := r.(type) {
					case *xParseError:
						panic(e)
					case *xDOMException:
						h.fatalError("Error constructing the DOM: " + "DOMException: " + e.msg)
					case *xJSError:
						h.error("element parse error: " + e.msg)
						end = -1
					default:
						panic(r)
					}
				}
			}()
			tagStart = indexOf('<', start)
			if tagStart < 0 {
				if len(unclosedTags) > 0 {
					h.fatalError("unclosed xml tag(s): " + strings.Join(unclosedTags, ", "))
				}
				rest := sub(start, len(src))
				if !regexp.MustCompile(`^[`+dfdWS+`]*$`).MatchString(rest) {
					text := &xNode{kind: xText, data: rest}
					if xDocumentElement(h.doc) {
						h.error("Extra content at the end of the document")
						stop = true
						return
					}
					h.insert(h.doc, text)
					h.currentElement = text
				}
				stop = true
				return
			}
			if tagStart > start {
				fromSource := sub(start, tagStart)
				if len(unclosedTags) == 0 {
					fromSource = xSOptRe.ReplaceAllLiteralString(fromSource, "")
					if fromSource != "" {
						h.error("Unexpected content outside root element: '" + fromSource + "'")
					}
				}
				appendText(tagStart)
			}
			switch charAt(tagStart + 1) {
			case '/':
				end = indexOf('>', tagStart+2)
				var tagNameRaw string
				if end > 0 {
					tagNameRaw = sub(tagStart+2, end)
				} else {
					tagNameRaw = sub(min(tagStart+2, len(src)), len(src))
				}
				if tagNameRaw == "" {
					h.fatalError("end tag name missing")
				}
				var tagNameMatch []string
				if end > 0 {
					tagNameMatch = xEndTagStrict.FindStringSubmatch(tagNameRaw)
				}
				if tagNameMatch == nil {
					var lead []string
					if end > 0 {
						lead = xEndTagLead.FindStringSubmatch(tagNameRaw)
					}
					if lead != nil && regexp.MustCompile(`(?m)^(`+xQName+`)[\x{20}\x{9}\x{D}\x{A}]*$`).MatchString(tagNameRaw) {
						h.error("end tag name is followed by a line break and trailing content: \"" + tagNameRaw + "\"")
						tagNameMatch = lead
					} else {
						h.fatalError("end tag name contains invalid characters: \"" + tagNameRaw + "\"")
					}
				}
				if h.currentElement == nil && !xDocumentElement(h.doc) {
					stop = true
					return
				}
				var currentTagName string
				if len(unclosedTags) > 0 {
					currentTagName = unclosedTags[len(unclosedTags)-1]
				}
				if currentTagName == "" {
					if h.currentElement == nil {
						panic(&xJSError{"Cannot read properties of undefined (reading 'tagName')"})
					}
					currentTagName = h.currentElement.name
					if currentTagName == "" {
						for _, c := range h.doc.children {
							if c.kind == xElement {
								currentTagName = c.name
								break
							}
						}
					}
				}
				if currentTagName != tagNameMatch[1] {
					h.fatalError("Opening and ending tag mismatch: \"" + currentTagName + "\" != \"" + tagNameRaw + "\"")
				}
				config := parseStack[len(parseStack)-1]
				parseStack = parseStack[:len(parseStack)-1]
				if len(unclosedTags) > 0 {
					unclosedTags = unclosedTags[:len(unclosedTags)-1]
				}
				_ = config
				h.endElement()
				end++
			case '?':
				end = xParseProcessingInstruction(src, tagStart, h)
			case '!':
				end = xParseDoctypeCommentOrCData(src, tagStart, h)
			default:
				el := &xElAttrs{names: map[string]bool{}}
				if len(parseStack) == 0 {
					panic(&xJSError{"Cannot read properties of undefined (reading 'currentNSMap')"})
				}
				currentNSMap := parseStack[len(parseStack)-1].currentNSMap
				end = xParseElementStartPart(src, tagStart, el, h, replaceEntities)
				if !el.closed {
					unclosedTags = append(unclosedTags, el.tagName)
				}
				if frame := xAppendElement(el, h, currentNSMap); frame != nil {
					parseStack = append(parseStack, frame)
				}
				end++
			}
		}()
		if stop {
			return
		}
		if end > start {
			start = end
		} else {
			appendText(max(tagStart, start) + 1)
		}
	}
}

func xAppendElement(el *xElAttrs, h *xHandler, currentNSMap *xNSScope) *xFrame {
	tagName := el.tagName
	var localNSMap map[string]bool
	for i := len(el.attrs) - 1; i >= 0; i-- {
		a := el.attrs[i]
		var nsPrefix string
		isNS := false
		if nsp := strings.IndexByte(a.qName, ':'); nsp > 0 {
			a.prefix, a.hasPrefix = a.qName[:nsp], true
			a.local = a.qName[nsp+1:]
			if a.prefix == "xmlns" {
				nsPrefix, isNS = a.local, true
			}
		} else {
			a.local = a.qName
			if a.qName == "xmlns" {
				nsPrefix, isNS = "", true
			}
		}
		if isNS {
			if localNSMap == nil {
				localNSMap = map[string]bool{}
				currentNSMap = &xNSScope{parent: currentNSMap, m: map[string]string{}}
			}
			currentNSMap.m[nsPrefix] = a.value
			localNSMap[nsPrefix] = true
			a.uri = xmlnsNS
		}
	}
	for i := len(el.attrs) - 1; i >= 0; i-- {
		a := el.attrs[i]
		if a.hasPrefix && a.prefix != "" {
			if a.prefix == "xml" {
				a.uri = xmlNS
			}
			if a.prefix != "xmlns" {
				a.uri = currentNSMap.lookup(a.prefix)
			}
		}
	}
	prefix := ""
	if nsp := strings.IndexByte(tagName, ':'); nsp > 0 {
		prefix = tagName[:nsp]
	}
	ns := currentNSMap.lookup(prefix)
	h.startElement(ns, tagName, el)
	if el.closed {
		h.endElement()
		return nil
	}
	return &xFrame{currentNSMap: currentNSMap, localNSMap: localNSMap, hasLocal: localNSMap != nil}
}

const (
	xsTag = iota
	xsAttr
	xsAttrSpace
	xsEq
	xsAttrNoquotValue
	xsAttrEnd
	xsTagSpace
	xsTagClose
)

var xTabNLCR = regexp.MustCompile(`[\t\n\r]`)

func xParseElementStartPart(src []rune, start int, el *xElAttrs, h *xHandler, replaceEntities func(string) string) int {
	sub := func(a, b int) string {
		if a > len(src) {
			a = len(src)
		}
		if b > len(src) {
			b = len(src)
		}
		if a > b {
			return ""
		}
		return string(src[a:b])
	}
	addAttribute := func(qName, value string) {
		if el.names[qName] {
			h.fatalError("Attribute " + qName + " redefined")
		}
		if strings.Contains(value, "<") {
			h.fatalError("Unescaped '<' not allowed in attributes values")
		}
		value = replaceEntities(xTabNLCR.ReplaceAllLiteralString(value, " "))
		el.addValue(qName, value)
	}
	var attrName, value string
	start++
	p := start
	s := xsTag
	for {
		c := rune(-1)
		if p < len(src) {
			c = src[p]
		}
		if s == xsTag && c == '<' {
			panic(&xJSError{"unexpected < in tag name: " + sub(start, p)})
		}
		switch c {
		case '=':
			if s == xsAttr {
				attrName = sub(start, p)
				s = xsEq
			} else if s == xsAttrSpace {
				s = xsEq
			} else {
				panic(&xJSError{"attribute equal must after attrName"})
			}
		case '\'', '"':
			if s == xsEq || s == xsAttr {
				if s == xsAttr {
					h.warning("attribute value must after \"=\"")
					attrName = sub(start, p)
				}
				start = p + 1
				p = -1
				for i := start; i < len(src); i++ {
					if src[i] == c {
						p = i
						break
					}
				}
				if p > 0 {
					value = sub(start, p)
					addAttribute(attrName, value)
					s = xsAttrEnd
				} else {
					panic(&xJSError{"attribute value no end '" + string(c) + "' match"})
				}
			} else if s == xsAttrNoquotValue {
				value = sub(start, p)
				addAttribute(attrName, value)
				h.warning("attribute \"" + attrName + "\" missed start quot(" + string(c) + ")!!")
				start = p + 1
				s = xsAttrEnd
			} else {
				panic(&xJSError{"attribute value must after \"=\""})
			}
		case '/':
			switch s {
			case xsTag:
				el.setTagName(sub(start, p))
				s = xsTagClose
				el.closed = true
			case xsAttrEnd, xsTagSpace, xsTagClose:
				s = xsTagClose
				el.closed = true
			case xsAttrNoquotValue, xsAttr:
			case xsAttrSpace:
				el.closed = true
			default:
				panic(&xJSError{"attribute invalid close char('/')"})
			}
		case -1:
			h.error("unexpected end of input")
			if s == xsTag {
				el.setTagName(sub(start, p))
			}
			return p
		case '>':
			switch s {
			case xsTag:
				el.setTagName(sub(start, p))
			case xsAttrEnd, xsTagSpace, xsTagClose:
			case xsAttrNoquotValue, xsAttr, xsAttrSpace:
				if s != xsAttrSpace {
					value = sub(start, p)
					if strings.HasSuffix(value, "/") {
						el.closed = true
						value = value[:len(value)-1]
					}
				}
				if s == xsAttrSpace {
					value = attrName
				}
				if s == xsAttrNoquotValue {
					h.warning("attribute \"" + value + "\" missed quot(\")!")
					addAttribute(attrName, value)
				} else {
					h.warning("attribute \"" + value + "\" missed value!! \"" + value + "\" instead!!")
					addAttribute(value, value)
				}
			case xsEq:
				h.fatalError("AttValue: ' or \" expected")
			}
			return p
		default:
			if c == 0x80 {
				c = ' '
			}
			if c <= ' ' {
				switch s {
				case xsTag:
					el.setTagName(sub(start, p))
					s = xsTagSpace
				case xsAttr:
					attrName = sub(start, p)
					s = xsAttrSpace
				case xsAttrNoquotValue:
					value = sub(start, p)
					h.warning("attribute \"" + value + "\" missed quot(\")!!")
					addAttribute(attrName, value)
					s = xsTagSpace
				case xsAttrEnd:
					s = xsTagSpace
				}
			} else {
				switch s {
				case xsAttrSpace:
					h.warning("attribute \"" + attrName + "\" missed value!! \"" + attrName + "\" instead2!!")
					addAttribute(attrName, attrName)
					start = p
					s = xsAttr
				case xsAttrEnd:
					h.warning("attribute space is required\"" + attrName + "\"!!")
					s = xsAttr
					start = p
				case xsTagSpace:
					s = xsAttr
					start = p
				case xsEq:
					s = xsAttrNoquotValue
					start = p
				case xsTagClose:
					panic(&xJSError{"elements closed character '/' and '>' must be connected to"})
				}
			}
		}
		p++
	}
}

// xGetMatch applies re (anchored with ^) to the source from index i; it returns the match length in runes.
func xGetMatch(src []rune, i int, re *regexp.Regexp) (string, bool) {
	rest := string(src[min(i, len(src)):])
	loc := re.FindStringIndex(rest)
	if loc == nil {
		return "", false
	}
	return rest[:loc[1]], true
}

func xParseProcessingInstruction(src []rune, start int, h *xHandler) int {
	target, data, n, ok := xMatchPI(string(src[start:]))
	_ = data
	if !ok {
		h.fatalError("Invalid processing instruction starting at position " + strconv.Itoa(start))
	}
	if strings.ToLower(target) == "xml" {
		if start > 0 {
			h.fatalError("processing instruction at position " + strconv.Itoa(start) + " is an xml declaration which is only at the start of the document")
		}
		if !xXMLDecl.MatchString(string(src[start:])) {
			h.fatalError("xml declaration is not well-formed")
		}
	}
	node := &xNode{kind: xPI, name: target, data: data}
	h.appendNode(node)
	return start + n
}

// xMatchPI is the match of g.PI against the start of s: the target, the data and the length in runes.
func xMatchPI(s string) (target, data string, n int, ok bool) {
	if !strings.HasPrefix(s, "<?") {
		return
	}
	loc := xNameAt.FindStringIndex(s[2:])
	if loc == nil {
		return
	}
	target = s[2 : 2+loc[1]]
	rest := s[2+loc[1]:]
	rs := []rune(rest)
	w := xLeadingSpaces(rs, 0)
	consumed := len([]rune(s[:2+loc[1]]))
	if w >= 1 {
		// S, then Char*? up to the first ?>
		i := w
		for i <= len(rs) {
			if i+1 < len(rs) && rs[i] == '?' && rs[i+1] == '>' {
				return target, string(rs[w:i]), consumed + i + 2, true
			}
			if i >= len(rs) || !regexp.MustCompile(`^`+xChar+`$`).MatchString(string(rs[i])) {
				break
			}
			i++
		}
		return "", "", 0, false
	}
	if len(rs) >= 2 && rs[0] == '?' && rs[1] == '>' {
		return target, "", consumed + 2, true
	}
	return "", "", 0, false
}

func xParseDoctypeCommentOrCData(src []rune, start int, h *xHandler) int {
	c := rune(-1)
	if start+2 < len(src) {
		c = src[start+2]
	}
	switch c {
	case '-':
		m, ok := xGetMatch(src, start, xCommentRe)
		if ok {
			node := &xNode{kind: xComment, data: m[4 : len(m)-3]}
			h.appendNode(node)
			return start + len([]rune(m))
		}
		h.fatalError("comment is not well-formed at position " + strconv.Itoa(start))
	case '[':
		m, ok := xGetMatch(src, start, xCDSectRe)
		if ok {
			if h.currentElement == nil {
				h.fatalError("CDATA outside of element")
			}
			h.cdata = true
			h.characters(m[9 : len(m)-3])
			h.cdata = false
			return start + len([]rune(m))
		}
		h.fatalError("Invalid CDATA starting at position " + strconv.Itoa(start))
	case 'D':
		return xParseDoctype(src, start, h)
	}
	h.fatalError("Not well-formed XML starting with \"<!\" at position " + strconv.Itoa(start))
	return 0
}

func xParseDoctype(src []rune, start int, h *xHandler) int {
	if xDocumentElement(h.doc) {
		h.fatalError("Doctype not allowed inside or after documentElement at position " + strconv.Itoa(start))
	}
	idx := start
	startsWith := func(t string) bool {
		tr := []rune(t)
		return idx+len(tr) <= len(src) && string(src[idx:idx+len(tr)]) == t
	}
	skipBlanks := func() int {
		n := 0
		for idx < len(src) {
			if !xIsSpaceRune(src[idx]) {
				return n
			}
			n++
			idx++
		}
		return -1
	}
	if !startsWith("<!DOCTYPE") {
		h.fatalError("Expected <!DOCTYPE at position " + strconv.Itoa(idx))
	}
	idx += len("<!DOCTYPE")
	if skipBlanks() < 1 {
		h.fatalError("Expected whitespace after <!DOCTYPE at position " + strconv.Itoa(idx))
	}
	name, ok := xGetMatch(src, idx, xNameAt)
	if !ok || name == "" {
		h.fatalError("doctype name missing or contains unexpected characters at position " + strconv.Itoa(idx))
	}
	idx += len([]rune(name))
	skipBlanks()
	if startsWith("PUBLIC") || startsWith("SYSTEM") {
		m := xExternalIDRe.FindString(string(src[idx:]))
		if m == "" {
			h.fatalError("doctype external id is not well-formed at position " + strconv.Itoa(idx))
		}
		idx += len([]rune(m))
	}
	skipBlanks()
	// internal subset
	if idx < len(src) && src[idx] == '[' {
		idx++
		for {
			if idx >= len(src) {
				h.fatalError("doctype internal subset is not well-formed, missing ]")
			}
			skipBlanks()
			if idx < len(src) && src[idx] == ']' {
				idx++
				break
			}
			var matched string
			c1, c2 := rune(-1), rune(-1)
			if idx+1 < len(src) {
				c1 = src[idx+1]
			}
			if idx+2 < len(src) {
				c2 = src[idx+2]
			}
			c3 := rune(-1)
			if idx+3 < len(src) {
				c3 = src[idx+3]
			}
			switch {
			case src[idx] == '<' && c1 == '!':
				switch c2 {
				case 'E':
					if c3 == 'L' {
						matched, _ = xGetMatch(src, idx, xElementDecl)
					} else if c3 == 'N' {
						matched, _ = xGetMatch(src, idx, xEntityDecl)
					}
				case 'A':
					matched, _ = xGetMatch(src, idx, xAttlistDecl)
				case 'N':
					matched, _ = xGetMatch(src, idx, xNotationDecl)
				case '-':
					matched, _ = xGetMatch(src, idx, xCommentRe)
				}
			case src[idx] == '<' && c1 == '?':
				target, _, n, ok := xMatchPI(string(src[idx:]))
				if !ok {
					h.fatalError("processing instruction is not well-formed at position " + strconv.Itoa(idx))
				}
				if strings.ToLower(target) == "xml" {
					h.fatalError("xml declaration is only allowed at the start of the document, but found at position " + strconv.Itoa(idx))
				}
				matched = string(src[idx : idx+n])
			case src[idx] == '%':
				matched, _ = xGetMatch(src, idx, xPEReference)
			default:
				h.fatalError("Error detected in Markup declaration")
			}
			if matched == "" {
				h.fatalError("Error in internal subset at position " + strconv.Itoa(idx))
			}
			idx += len([]rune(matched))
		}
	}
	skipBlanks()
	if idx >= len(src) || src[idx] != '>' {
		h.fatalError("doctype not terminated with > at position " + strconv.Itoa(idx))
	}
	idx++
	h.appendNode(&xNode{kind: xDocType, name: name})
	return idx
}
