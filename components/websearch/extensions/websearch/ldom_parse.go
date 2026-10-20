package websearch

import (
	"strconv"
	"strings"
)

// The HTML parser linkedom uses: htmlparser2 9.1.0 (Tokenizer.js, Parser.js) with linkedom's handlers
// (shared/parse-from-string.js) and the options linkedom passes: lowerCaseAttributeNames false, decodeEntities true,
// xmlMode false. It is a lenient tokenizer with a few implied-end rules, not the HTML5 tree builder: no implied
// html, head, body or tbody, attribute names keep their case, `<p>` and list items close by the rules of
// openImpliesClose, and the text around every character reference is a separate text node. The whole document is
// written as one chunk, so the streaming bookkeeping of the original (offsets, buffers) is not here.

const (
	tkText = iota + 1
	tkBeforeTagName
	tkInTagName
	tkInSelfClosingTag
	tkBeforeClosingTagName
	tkInClosingTagName
	tkAfterClosingTagName
	tkBeforeAttributeName
	tkInAttributeName
	tkAfterAttributeName
	tkBeforeAttributeValue
	tkInAttributeValueDq
	tkInAttributeValueSq
	tkInAttributeValueNq
	tkBeforeDeclaration
	tkInDeclaration
	tkInProcessingInstruction
	tkBeforeComment
	tkCDATASequence
	tkInSpecialComment
	tkInCommentLike
	tkBeforeSpecialS
	tkBeforeSpecialT
	tkSpecialStartSequence
	tkInSpecialTag
)

const (
	quoteNoValue = iota
	quoteUnquoted
	quoteSingle
	quoteDouble
)

var (
	seqCdata      = []byte("CDATA[")
	seqCdataEnd   = []byte("]]>")
	seqCommentEnd = []byte("-->")
	seqScriptEnd  = []byte("</script")
	seqStyleEnd   = []byte("</style")
	seqTitleEnd   = []byte("</title")
	seqTextareaEnd = []byte("</textarea")
)

func tkIsWS(c byte) bool {
	return c == ' ' || c == '\n' || c == '\t' || c == '\f' || c == '\r'
}

func tkIsEndOfTagSection(c byte) bool { return c == '/' || c == '>' || tkIsWS(c) }

func tkIsASCIIAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

type ldParser struct {
	// tokenizer
	buf          string
	state        int
	sectionStart int
	index        int
	entityStart  int
	baseState    int
	isSpecial    bool
	seq          []byte
	seqIsTitle   bool
	seqIndex     int

	// parser (htmlparser2 Parser)
	stack         []string // the open tags, the innermost last
	foreign       []bool   // foreignContext, the innermost last
	tagname       string
	attribname    string
	attribvalue   strings.Builder
	attribs       []ldAttr // an object keyed by name, in insertion order
	haveAttribs   bool
	document      *ldNode

	// linkedom handlers
	node          *ldNode
	ownerSVG      *ldNode
}

var (
	ldFormTags    = map[string]bool{"input": true, "option": true, "optgroup": true, "select": true, "button": true, "datalist": true, "textarea": true}
	ldPTag        = map[string]bool{"p": true}
	ldTableSect   = map[string]bool{"thead": true, "tbody": true}
	ldDdtTags     = map[string]bool{"dd": true, "dt": true}
	ldRtpTags     = map[string]bool{"rt": true, "rp": true}
	ldOpenImplies = map[string]map[string]bool{
		"tr": {"tr": true, "th": true, "td": true}, "th": {"th": true}, "td": {"thead": true, "th": true, "td": true},
		"body": {"head": true, "link": true, "script": true}, "li": {"li": true}, "p": ldPTag,
		"h1": ldPTag, "h2": ldPTag, "h3": ldPTag, "h4": ldPTag, "h5": ldPTag, "h6": ldPTag,
		"select": ldFormTags, "input": ldFormTags, "output": ldFormTags, "button": ldFormTags, "datalist": ldFormTags, "textarea": ldFormTags,
		"option": {"option": true}, "optgroup": {"optgroup": true, "option": true}, "dd": ldDdtTags, "dt": ldDdtTags,
		"address": ldPTag, "article": ldPTag, "aside": ldPTag, "blockquote": ldPTag, "details": ldPTag, "div": ldPTag, "dl": ldPTag,
		"fieldset": ldPTag, "figcaption": ldPTag, "figure": ldPTag, "footer": ldPTag, "form": ldPTag, "header": ldPTag, "hr": ldPTag,
		"main": ldPTag, "nav": ldPTag, "ol": ldPTag, "pre": ldPTag, "section": ldPTag, "table": ldPTag, "ul": ldPTag,
		"rt": ldRtpTags, "rp": ldRtpTags, "tbody": ldTableSect, "tfoot": ldTableSect,
	}
	ldVoidParse = map[string]bool{
		"area": true, "base": true, "basefont": true, "br": true, "col": true, "command": true, "embed": true, "frame": true,
		"hr": true, "img": true, "input": true, "isindex": true, "keygen": true, "link": true, "meta": true, "param": true,
		"source": true, "track": true, "wbr": true,
	}
	ldForeignContext   = map[string]bool{"math": true, "svg": true}
	ldHTMLIntegration  = map[string]bool{"mi": true, "mo": true, "mn": true, "ms": true, "mtext": true, "annotation-xml": true, "foreignobject": true, "desc": true, "title": true}
)

// ldParseDocument is DOMParser.parseFromString(markup, "text/html"): a document, with no children for an empty
// string.
func ldParseDocument(markup string) *ldNode {
	doc := ldNewDocument()
	if markup == "..." {
		markup = "<!doctype html><html><head></head><body></body></html>"
	}
	if markup != "" {
		ldParseInto(doc, markup)
	}
	return doc
}

// ldParseInto is parseFromString(document, true, markup): the nodes are appended to doc.
func ldParseInto(doc *ldNode, markup string) {
	p := &ldParser{buf: markup, state: tkText, baseState: tkText, document: doc, node: doc, foreign: []bool{false}}
	p.parse()
	p.finish()
}

// ---- tokenizer -------------------------------------------------------------------------------------------------

func (p *ldParser) parse() {
	for p.index < len(p.buf) {
		c := p.buf[p.index]
		switch p.state {
		case tkText:
			p.stateText(c)
		case tkSpecialStartSequence:
			p.stateSpecialStartSequence(c)
		case tkInSpecialTag:
			p.stateInSpecialTag(c)
		case tkCDATASequence:
			p.stateCDATASequence(c)
		case tkInAttributeValueDq:
			p.handleInAttributeValue(c, '"')
		case tkInAttributeName:
			p.stateInAttributeName(c)
		case tkInCommentLike:
			p.stateInCommentLike(c)
		case tkInSpecialComment:
			p.stateInSpecialComment(c)
		case tkBeforeAttributeName:
			p.stateBeforeAttributeName(c)
		case tkInTagName:
			p.stateInTagName(c)
		case tkInClosingTagName:
			p.stateInClosingTagName(c)
		case tkBeforeTagName:
			p.stateBeforeTagName(c)
		case tkAfterAttributeName:
			p.stateAfterAttributeName(c)
		case tkInAttributeValueSq:
			p.handleInAttributeValue(c, '\'')
		case tkBeforeAttributeValue:
			p.stateBeforeAttributeValue(c)
		case tkBeforeClosingTagName:
			p.stateBeforeClosingTagName(c)
		case tkAfterClosingTagName:
			p.stateAfterClosingTagName(c)
		case tkBeforeSpecialS:
			p.stateBeforeSpecialS(c)
		case tkBeforeSpecialT:
			p.stateBeforeSpecialT(c)
		case tkInAttributeValueNq:
			p.stateInAttributeValueNoQuotes(c)
		case tkInSelfClosingTag:
			p.stateInSelfClosingTag(c)
		case tkInDeclaration:
			p.stateInDeclaration(c)
		case tkBeforeDeclaration:
			p.stateBeforeDeclaration(c)
		case tkBeforeComment:
			p.stateBeforeComment(c)
		case tkInProcessingInstruction:
			p.stateInProcessingInstruction(c)
		}
		p.index++
	}
	p.cleanup()
}

func (p *ldParser) fastForwardTo(c byte) bool {
	for {
		p.index++
		if p.index >= len(p.buf) {
			break
		}
		if p.buf[p.index] == c {
			return true
		}
	}
	p.index = len(p.buf) - 1
	return false
}

func (p *ldParser) stateText(c byte) {
	if c == '<' {
		if p.index > p.sectionStart {
			p.ontext(p.sectionStart, p.index)
		}
		p.state = tkBeforeTagName
		p.sectionStart = p.index
	} else if c == '&' {
		p.startEntity()
	}
}

func (p *ldParser) stateSpecialStartSequence(c byte) {
	isEnd := p.seqIndex == len(p.seq)
	var isMatch bool
	if isEnd {
		isMatch = tkIsEndOfTagSection(c)
	} else {
		isMatch = (c | 0x20) == p.seq[p.seqIndex]
	}
	if !isMatch {
		p.isSpecial = false
	} else if !isEnd {
		p.seqIndex++
		return
	}
	p.seqIndex = 0
	p.state = tkInTagName
	p.stateInTagName(c)
}

func (p *ldParser) stateInSpecialTag(c byte) {
	if p.seqIndex == len(p.seq) {
		if c == '>' || tkIsWS(c) {
			endOfText := p.index - len(p.seq)
			if p.sectionStart < endOfText {
				actual := p.index
				p.index = endOfText
				p.ontext(p.sectionStart, endOfText)
				p.index = actual
			}
			p.isSpecial = false
			p.sectionStart = endOfText + 2 // skip the `</`
			p.stateInClosingTagName(c)
			return
		}
		p.seqIndex = 0
	}
	if (c | 0x20) == p.seq[p.seqIndex] {
		p.seqIndex++
	} else if p.seqIndex == 0 {
		if p.seqIsTitle {
			// Entities are decoded in <title> tags.
			if c == '&' {
				p.startEntity()
			}
		} else if p.fastForwardTo('<') {
			p.seqIndex = 1
		}
	} else if c == '<' {
		p.seqIndex = 1
	} else {
		p.seqIndex = 0
	}
}

func (p *ldParser) stateCDATASequence(c byte) {
	if c == seqCdata[p.seqIndex] {
		p.seqIndex++
		if p.seqIndex == len(seqCdata) {
			p.state = tkInCommentLike
			p.seq = seqCdataEnd
			p.seqIndex = 0
			p.sectionStart = p.index + 1
		}
	} else {
		p.seqIndex = 0
		p.state = tkInDeclaration
		p.stateInDeclaration(c)
	}
}

func (p *ldParser) stateInCommentLike(c byte) {
	if c == p.seq[p.seqIndex] {
		p.seqIndex++
		if p.seqIndex == len(p.seq) {
			if len(p.seq) == len(seqCdataEnd) && p.seq[0] == seqCdataEnd[0] {
				p.oncdata(p.sectionStart, p.index, 2)
			} else {
				p.oncomment(p.sectionStart, p.index, 2)
			}
			p.seqIndex = 0
			p.sectionStart = p.index + 1
			p.state = tkText
		}
	} else if p.seqIndex == 0 {
		// Fast-forward to the first character of the sequence.
		if p.fastForwardTo(p.seq[0]) {
			p.seqIndex = 1
		}
	} else if c != p.seq[p.seqIndex-1] {
		// Allow long sequences, eg. --->, ]]]>
		p.seqIndex = 0
	}
}

func (p *ldParser) startSpecial(seq []byte, offset int) {
	p.isSpecial = true
	p.seq = seq
	p.seqIsTitle = &seq[0] == &seqTitleEnd[0]
	p.seqIndex = offset
	p.state = tkSpecialStartSequence
}

func (p *ldParser) stateBeforeTagName(c byte) {
	switch {
	case c == '!':
		p.state = tkBeforeDeclaration
		p.sectionStart = p.index + 1
	case c == '?':
		p.state = tkInProcessingInstruction
		p.sectionStart = p.index + 1
	case tkIsASCIIAlpha(c):
		lower := c | 0x20
		p.sectionStart = p.index
		switch lower {
		case seqScriptEnd[2]:
			p.state = tkBeforeSpecialS
		case seqTitleEnd[2]:
			p.state = tkBeforeSpecialT
		default:
			p.state = tkInTagName
		}
	case c == '/':
		p.state = tkBeforeClosingTagName
	default:
		p.state = tkText
		p.stateText(c)
	}
}

func (p *ldParser) stateInTagName(c byte) {
	if tkIsEndOfTagSection(c) {
		p.onopentagname(p.sectionStart, p.index)
		p.sectionStart = -1
		p.state = tkBeforeAttributeName
		p.stateBeforeAttributeName(c)
	}
}

func (p *ldParser) stateBeforeClosingTagName(c byte) {
	switch {
	case tkIsWS(c):
	case c == '>':
		p.state = tkText
	default:
		if tkIsASCIIAlpha(c) {
			p.state = tkInClosingTagName
		} else {
			p.state = tkInSpecialComment
		}
		p.sectionStart = p.index
	}
}

func (p *ldParser) stateInClosingTagName(c byte) {
	if c == '>' || tkIsWS(c) {
		p.onclosetag(p.sectionStart, p.index)
		p.sectionStart = -1
		p.state = tkAfterClosingTagName
		p.stateAfterClosingTagName(c)
	}
}

func (p *ldParser) stateAfterClosingTagName(c byte) {
	// Skip everything until ">".
	if c == '>' || p.fastForwardTo('>') {
		p.state = tkText
		p.sectionStart = p.index + 1
	}
}

func (p *ldParser) stateBeforeAttributeName(c byte) {
	switch {
	case c == '>':
		p.onopentagend(p.index)
		if p.isSpecial {
			p.state = tkInSpecialTag
			p.seqIndex = 0
		} else {
			p.state = tkText
		}
		p.sectionStart = p.index + 1
	case c == '/':
		p.state = tkInSelfClosingTag
	case !tkIsWS(c):
		p.state = tkInAttributeName
		p.sectionStart = p.index
	}
}

func (p *ldParser) stateInSelfClosingTag(c byte) {
	if c == '>' {
		p.onselfclosingtag(p.index)
		p.state = tkText
		p.sectionStart = p.index + 1
		p.isSpecial = false // reset special state, in case of self-closing special tags
	} else if !tkIsWS(c) {
		p.state = tkBeforeAttributeName
		p.stateBeforeAttributeName(c)
	}
}

func (p *ldParser) stateInAttributeName(c byte) {
	if c == '=' || tkIsEndOfTagSection(c) {
		p.onattribname(p.sectionStart, p.index)
		p.sectionStart = p.index
		p.state = tkAfterAttributeName
		p.stateAfterAttributeName(c)
	}
}

func (p *ldParser) stateAfterAttributeName(c byte) {
	switch {
	case c == '=':
		p.state = tkBeforeAttributeValue
	case c == '/' || c == '>':
		p.onattribend(quoteNoValue)
		p.sectionStart = -1
		p.state = tkBeforeAttributeName
		p.stateBeforeAttributeName(c)
	case !tkIsWS(c):
		p.onattribend(quoteNoValue)
		p.state = tkInAttributeName
		p.sectionStart = p.index
	}
}

func (p *ldParser) stateBeforeAttributeValue(c byte) {
	switch {
	case c == '"':
		p.state = tkInAttributeValueDq
		p.sectionStart = p.index + 1
	case c == '\'':
		p.state = tkInAttributeValueSq
		p.sectionStart = p.index + 1
	case !tkIsWS(c):
		p.sectionStart = p.index
		p.state = tkInAttributeValueNq
		p.stateInAttributeValueNoQuotes(c)
	}
}

func (p *ldParser) handleInAttributeValue(c, quote byte) {
	if c == quote {
		p.onattribdata(p.sectionStart, p.index)
		p.sectionStart = -1
		if quote == '"' {
			p.onattribend(quoteDouble)
		} else {
			p.onattribend(quoteSingle)
		}
		p.state = tkBeforeAttributeName
	} else if c == '&' {
		p.startEntity()
	}
}

func (p *ldParser) stateInAttributeValueNoQuotes(c byte) {
	if tkIsWS(c) || c == '>' {
		p.onattribdata(p.sectionStart, p.index)
		p.sectionStart = -1
		p.onattribend(quoteUnquoted)
		p.state = tkBeforeAttributeName
		p.stateBeforeAttributeName(c)
	} else if c == '&' {
		p.startEntity()
	}
}

func (p *ldParser) stateBeforeDeclaration(c byte) {
	if c == '[' {
		p.state = tkCDATASequence
		p.seqIndex = 0
	} else if c == '-' {
		p.state = tkBeforeComment
	} else {
		p.state = tkInDeclaration
	}
}

func (p *ldParser) stateInDeclaration(c byte) {
	if c == '>' || p.fastForwardTo('>') {
		p.ondeclaration(p.sectionStart, p.index)
		p.state = tkText
		p.sectionStart = p.index + 1
	}
}

func (p *ldParser) stateInProcessingInstruction(c byte) {
	if c == '>' || p.fastForwardTo('>') {
		p.onprocessinginstruction(p.sectionStart, p.index)
		p.state = tkText
		p.sectionStart = p.index + 1
	}
}

func (p *ldParser) stateBeforeComment(c byte) {
	if c == '-' {
		p.state = tkInCommentLike
		p.seq = seqCommentEnd
		// Allow short comments (eg. <!-->).
		p.seqIndex = 2
		p.sectionStart = p.index + 1
	} else {
		p.state = tkInDeclaration
	}
}

func (p *ldParser) stateInSpecialComment(c byte) {
	if c == '>' || p.fastForwardTo('>') {
		p.oncomment(p.sectionStart, p.index, 0)
		p.state = tkText
		p.sectionStart = p.index + 1
	}
}

func (p *ldParser) stateBeforeSpecialS(c byte) {
	lower := c | 0x20
	switch lower {
	case seqScriptEnd[3]:
		p.startSpecial(seqScriptEnd, 4)
	case seqStyleEnd[3]:
		p.startSpecial(seqStyleEnd, 4)
	default:
		p.state = tkInTagName
		p.stateInTagName(c) // consume the token again
	}
}

func (p *ldParser) stateBeforeSpecialT(c byte) {
	lower := c | 0x20
	switch lower {
	case seqTitleEnd[3]:
		p.startSpecial(seqTitleEnd, 4)
	case seqTextareaEnd[3]:
		p.startSpecial(seqTextareaEnd, 4)
	default:
		p.state = tkInTagName
		p.stateInTagName(c)
	}
}

// startEntity decodes the character reference at the current '&'. The state machine of the original resumes the
// text or attribute state afterwards; here the reference is decoded in one go.
func (p *ldParser) startEntity() {
	base := p.state
	attr := base != tkText && base != tkInSpecialTag
	cps, consumed := ldDecodeEntity(p.buf, p.index, attr)
	if consumed == 0 {
		return // not a reference: the '&' stays text
	}
	entityStart := p.index
	if attr {
		if p.sectionStart < entityStart {
			p.onattribdata(p.sectionStart, entityStart)
		}
		p.sectionStart = entityStart + consumed
		p.index = p.sectionStart - 1
		for _, cp := range cps {
			p.attribvalue.WriteRune(cp)
		}
		return
	}
	if p.sectionStart < entityStart {
		p.ontext(p.sectionStart, entityStart)
	}
	p.sectionStart = entityStart + consumed
	p.index = p.sectionStart - 1
	for _, cp := range cps {
		p.ontextString(string(cp))
	}
}

func (p *ldParser) cleanup() {
	if p.sectionStart != p.index {
		switch {
		case p.state == tkText || (p.state == tkInSpecialTag && p.seqIndex == 0):
			p.ontext(p.sectionStart, p.index)
			p.sectionStart = p.index
		case p.state == tkInAttributeValueDq || p.state == tkInAttributeValueSq || p.state == tkInAttributeValueNq:
			p.onattribdata(p.sectionStart, p.index)
			p.sectionStart = p.index
		}
	}
}

func (p *ldParser) finish() {
	p.handleTrailingData()
	p.onend()
}

func (p *ldParser) handleTrailingData() {
	end := len(p.buf)
	if p.sectionStart >= end {
		return
	}
	switch p.state {
	case tkInCommentLike:
		if len(p.seq) == len(seqCdataEnd) && p.seq[0] == seqCdataEnd[0] {
			p.oncdata(p.sectionStart, end, 0)
		} else {
			p.oncomment(p.sectionStart, end, 0)
		}
	case tkInTagName, tkBeforeAttributeName, tkBeforeAttributeValue, tkAfterAttributeName, tkInAttributeName,
		tkInAttributeValueSq, tkInAttributeValueDq, tkInAttributeValueNq, tkInClosingTagName:
		// An unfinished tag is ignored.
	default:
		p.ontext(p.sectionStart, end)
	}
}

// ---- entities --------------------------------------------------------------------------------------------------

func ldIsAlnum(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// ldReplaceCodePoint is entities' replaceCodePoint.
func ldReplaceCodePoint(cp uint64) rune {
	if (cp >= 0xd800 && cp <= 0xdfff) || cp > 0x10ffff {
		return 0xfffd
	}
	switch cp {
	case 0:
		return 0xfffd
	case 128:
		return 8364
	case 130:
		return 8218
	case 131:
		return 402
	case 132:
		return 8222
	case 133:
		return 8230
	case 134:
		return 8224
	case 135:
		return 8225
	case 136:
		return 710
	case 137:
		return 8240
	case 138:
		return 352
	case 139:
		return 8249
	case 140:
		return 338
	case 142:
		return 381
	case 145:
		return 8216
	case 146:
		return 8217
	case 147:
		return 8220
	case 148:
		return 8221
	case 149:
		return 8226
	case 150:
		return 8211
	case 151:
		return 8212
	case 152:
		return 732
	case 153:
		return 8482
	case 154:
		return 353
	case 155:
		return 8250
	case 156:
		return 339
	case 158:
		return 382
	case 159:
		return 376
	}
	return rune(cp)
}

// ldDecodeEntity is entities' EntityDecoder for the HTML table in the legacy (text) or attribute mode: the
// character reference that starts with the '&' at s[i], as code points, and the bytes it takes; none when it is
// not a reference. Numeric references need no semicolon; named ones without one only if they are legacy names,
// and in an attribute only when nothing alphanumeric or '=' follows.
func ldDecodeEntity(s string, i int, attr bool) ([]rune, int) {
	j := i + 1
	if j >= len(s) {
		return nil, 0
	}
	if s[j] == '#' {
		k := j + 1
		hex := k < len(s) && (s[k]|0x20) == 'x'
		if hex {
			k++
		}
		start := k
		var v uint64
		for k < len(s) {
			c := s[k]
			var d uint64
			switch {
			case c >= '0' && c <= '9':
				d = uint64(c - '0')
			case hex && c >= 'a' && c <= 'f':
				d = uint64(c-'a') + 10
			case hex && c >= 'A' && c <= 'F':
				d = uint64(c-'A') + 10
			default:
				d = 99
			}
			if d == 99 {
				break
			}
			if v <= 0x110000 {
				if hex {
					v = v*16 + d
				} else {
					v = v*10 + d
				}
			}
			k++
		}
		if k == start {
			return nil, 0
		}
		if k < len(s) && s[k] == ';' {
			k++
		}
		return []rune{ldReplaceCodePoint(v)}, k - i
	}
	k := j
	for k < len(s) && ldIsAlnum(s[k]) {
		k++
	}
	name := s[j:k]
	if k < len(s) && s[k] == ';' {
		if val, ok := ldEntities[name]; ok {
			return []rune(val), k + 1 - i
		}
	}
	l := len(name)
	for l > 0 && !ldLegacyEntities[name[:l]] {
		l--
	}
	if l == 0 {
		return nil, 0
	}
	if attr && (l < len(name) || (k < len(s) && s[k] == '=')) {
		return nil, 0
	}
	return []rune(ldEntities[name[:l]]), 1 + l
}

// ---- parser callbacks ------------------------------------------------------------------------------------------

func (p *ldParser) ontext(start, end int) { p.ontextString(p.buf[start:end]) }

func (p *ldParser) ontextString(data string) {
	p.append(ldNewText(data))
}

func (p *ldParser) isVoidElement(name string) bool { return ldVoidParse[name] }

func (p *ldParser) onopentagname(start, end int) {
	p.emitOpenTag(strings.ToLower(p.buf[start:end]))
}

func (p *ldParser) top() string {
	if len(p.stack) == 0 {
		return ""
	}
	return p.stack[len(p.stack)-1]
}

func (p *ldParser) emitOpenTag(name string) {
	p.tagname = name
	if implied := ldOpenImplies[name]; implied != nil {
		for len(p.stack) > 0 && implied[p.top()] {
			p.stack = p.stack[:len(p.stack)-1]
			p.closeNode()
		}
	}
	if !p.isVoidElement(name) {
		p.stack = append(p.stack, name)
		if ldForeignContext[name] {
			p.foreign = append(p.foreign, true)
		} else if ldHTMLIntegration[name] {
			p.foreign = append(p.foreign, false)
		}
	}
	p.attribs = p.attribs[:0]
	p.haveAttribs = true
}

func (p *ldParser) endOpenTag() {
	if p.haveAttribs {
		p.openNode(p.tagname, p.attribs)
		p.attribs = nil
		p.haveAttribs = false
	}
	if p.isVoidElement(p.tagname) {
		p.closeNode()
	}
	p.tagname = ""
}

func (p *ldParser) onopentagend(end int) { p.endOpenTag() }

func (p *ldParser) onclosetag(start, end int) {
	name := strings.ToLower(p.buf[start:end])
	if ldForeignContext[name] || ldHTMLIntegration[name] {
		if len(p.foreign) > 0 {
			p.foreign = p.foreign[:len(p.foreign)-1]
		}
	}
	if !p.isVoidElement(name) {
		pos := -1
		for i := len(p.stack) - 1; i >= 0; i-- {
			if p.stack[i] == name {
				pos = i
				break
			}
		}
		if pos != -1 {
			for len(p.stack) > pos {
				p.stack = p.stack[:len(p.stack)-1]
				p.closeNode()
			}
		} else if name == "p" {
			// Implicit open before close.
			p.emitOpenTag("p")
			p.closeCurrentTag(true)
		}
	} else if name == "br" {
		// Not emitOpenTag: the br would be closed by the implied-end rules.
		p.openNode("br", nil)
		p.closeNode()
	}
}

func (p *ldParser) onselfclosingtag(end int) {
	if len(p.foreign) > 0 && p.foreign[len(p.foreign)-1] {
		p.closeCurrentTag(false)
	} else {
		// Ignore the fact that the tag is self-closing.
		p.onopentagend(end)
	}
}

func (p *ldParser) closeCurrentTag(isOpenImplied bool) {
	name := p.tagname
	p.endOpenTag()
	// Self-closing tags will be on the top of the stack.
	if p.top() == name && len(p.stack) > 0 {
		p.closeNode()
		p.stack = p.stack[:len(p.stack)-1]
	}
}

func (p *ldParser) onattribname(start, end int) {
	p.attribname = p.buf[start:end] // lowerCaseAttributeNames is false
}

func (p *ldParser) onattribdata(start, end int) { p.attribvalue.WriteString(p.buf[start:end]) }

func (p *ldParser) onattribend(quote int) {
	if p.haveAttribs {
		dup := false
		for _, a := range p.attribs {
			if a.name == p.attribname {
				dup = true
				break
			}
		}
		if !dup {
			p.attribs = append(p.attribs, ldAttr{p.attribname, p.attribvalue.String()})
		}
	}
	p.attribvalue.Reset()
}

func ldInstructionName(value string) string {
	i := strings.IndexFunc(value, func(r rune) bool { return r == '/' || jsSpace(r) })
	if i >= 0 {
		value = value[:i]
	}
	return strings.ToLower(value)
}

func (p *ldParser) ondeclaration(start, end int) {
	value := p.buf[start:end]
	name := "!" + ldInstructionName(value)
	p.processingInstruction(name, "!"+value)
}

func (p *ldParser) onprocessinginstruction(start, end int) {
	value := p.buf[start:end]
	name := "?" + ldInstructionName(value)
	p.processingInstruction(name, "?"+value)
}

func (p *ldParser) oncomment(start, end, offset int) {
	p.append(ldNewComment(p.buf[start : end-offset]))
}

// oncdata: in HTML mode CDATA is a comment, "[CDATA[...]]".
func (p *ldParser) oncdata(start, end, offset int) {
	p.append(ldNewComment("[CDATA[" + p.buf[start:end-offset] + "]]"))
}

func (p *ldParser) onend() {
	for range p.stack {
		p.closeNode()
	}
}

// ---- linkedom's handlers ---------------------------------------------------------------------------------------

func (p *ldParser) append(n *ldNode) *ldNode {
	p.node.insertBefore(n, nil)
	return n
}

// ldIntKey reports whether key is a canonical array index, which a JavaScript object lists before its other keys.
func ldIntKey(key string) (uint64, bool) {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseUint(key, 10, 64)
	if err != nil || v >= 4294967295 {
		return 0, false
	}
	return v, true
}

// ldObjectKeyOrder is Object.keys order for attribute names: array indices ascending, then the rest as inserted.
func ldObjectKeyOrder(attrs []ldAttr) []ldAttr {
	hasInt := false
	for _, a := range attrs {
		if _, ok := ldIntKey(a.name); ok {
			hasInt = true
			break
		}
	}
	if !hasInt {
		return attrs
	}
	var ints, rest []ldAttr
	for _, a := range attrs {
		if _, ok := ldIntKey(a.name); ok {
			ints = append(ints, a)
		} else {
			rest = append(rest, a)
		}
	}
	for i := 1; i < len(ints); i++ {
		for j := i; j > 0; j-- {
			a, _ := ldIntKey(ints[j-1].name)
			b, _ := ldIntKey(ints[j].name)
			if a <= b {
				break
			}
			ints[j-1], ints[j] = ints[j], ints[j-1]
		}
	}
	return append(ints, rest...)
}

// openNode is linkedom's onopentag.
func (p *ldParser) openNode(name string, attrs []ldAttr) {
	var el *ldNode
	switch {
	case p.ownerSVG != nil:
		el = ldNewSVGElement(name)
		p.append(el)
	case name == "svg":
		el = ldNewSVGElement(name)
		p.ownerSVG = el
		p.append(el)
	default:
		el = ldNewElement(name)
		p.append(el)
	}
	p.node = el
	for _, a := range ldObjectKeyOrder(attrs) {
		v := a.value
		if a.name == "class" {
			v = ldClassValue(v)
		}
		el.attrs = append(el.attrs, ldAttr{a.name, v})
	}
}

// closeNode is linkedom's onclosetag.
func (p *ldParser) closeNode() {
	if p.node == p.ownerSVG {
		p.ownerSVG = nil
	}
	if p.node.parent != nil {
		p.node = p.node.parent
	}
}

// processingInstruction is linkedom's onprocessinginstruction: only a doctype is kept.
func (p *ldParser) processingInstruction(name, data string) {
	if strings.ToLower(name) != "!doctype" {
		return
	}
	p.setDoctype(strings.TrimFunc(data[len(name):], jsSpace))
}

// setDoctype is `document.doctype = value`.
func (p *ldParser) setDoctype(value string) {
	name, publicID, systemID, ok := ldParseDoctype(value)
	if !ok {
		return
	}
	d := &ldNode{kind: ldDoctype, name: "#document-type"}
	str := []string{name}
	hasPublic := len(publicID) > 0
	if hasPublic {
		str = append(str, "PUBLIC", `"`+publicID+`"`)
	}
	if len(systemID) > 0 {
		if !hasPublic {
			str = append(str, "SYSTEM")
		}
		str = append(str, `"`+systemID+`"`)
	}
	d.data = "<!DOCTYPE " + strings.Join(str, " ") + ">"
	p.document.insertBefore(d, p.document.first)
}

// ldParseDoctype is /^([a-z:]+)(\s+system|\s+public(\s+"([^"]+)")?)?(\s+"([^"]+)")?/i.
func ldParseDoctype(v string) (name, publicID, systemID string, ok bool) {
	i := 0
	for i < len(v) && (v[i] == ':' || (v[i]|0x20) >= 'a' && (v[i]|0x20) <= 'z') {
		i++
	}
	if i == 0 {
		return "", "", "", false
	}
	name = v[:i]
	rest := v[i:]
	skipSpace := func(s string) (string, bool) {
		t := strings.TrimLeftFunc(s, jsSpace)
		return t, len(t) < len(s)
	}
	quoted := func(s string) (val, after string, ok bool) {
		t, sp := skipSpace(s)
		if !sp || len(t) < 3 || t[0] != '"' {
			return "", s, false
		}
		end := strings.IndexByte(t[1:], '"')
		if end < 1 {
			return "", s, false
		}
		return t[1 : 1+end], t[end+2:], true
	}
	if t, sp := skipSpace(rest); sp {
		low := strings.ToLower(t)
		switch {
		case strings.HasPrefix(low, "system"):
			rest = t[len("system"):]
		case strings.HasPrefix(low, "public"):
			rest = t[len("public"):]
			if val, after, ok := quoted(rest); ok {
				publicID = val
				rest = after
			}
		}
	}
	if val, _, ok := quoted(rest); ok {
		systemID = val
	}
	return name, publicID, systemID, true
}
