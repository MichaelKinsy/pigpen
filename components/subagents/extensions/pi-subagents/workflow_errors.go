package pi_subagents

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/dop251/goja"
)

// Error texts the model sees. The script runs in goja, whose messages and stack frames differ from V8's; the original's
// worker reports V8's. These helpers put goja's output in V8's form for the cases a workflow script plausibly hits.

var workflowGojaFrame = regexp.MustCompile(`^\s*at (.*?)(?:\((\d+)\))?$`)

// workflowV8Stack turns a goja stack ("\tat f (file:2:9(3))") into V8's ("    at f (file:2:9)"). Frame text before the
// position is kept; the pc suffix goja adds is dropped.
func workflowV8Stack(stack string) string {
	lines := strings.Split(strings.TrimRight(stack, "\n"), "\n")
	out := lines[:1]
	for _, l := range lines[1:] {
		// frames of host functions and of the sandbox's own setup are not the script's
		if strings.Contains(l, "(native)") || strings.Contains(l, "<eval>") {
			continue
		}
		if m := workflowGojaFrame.FindStringSubmatch(l); m != nil {
			l = "    at " + m[1]
			l = strings.Replace(l, "(:", "(", 1)
		}
		out = append(out, strings.TrimSuffix(l, " "))
	}
	return strings.Join(out, "\n")
}

var (
	workflowReadPropRe   = regexp.MustCompile(`^Cannot read property '(.*)' of (undefined|null)$`)
	workflowSetPropRe    = regexp.MustCompile(`^Cannot set property '(.*)' of (undefined|null)$`)
	workflowReadOnlyRe   = regexp.MustCompile(`^Cannot assign to read only property '(.*)'$`)
	workflowNotFunctionR = regexp.MustCompile(`^(?:Object|.*) has no member '(.*)'$`)
)

// workflowV8Message rewrites the goja message of an error to V8's wording where the two differ.
func workflowV8Message(name, msg string) string {
	if v, ok := workflowStaticV8[msg]; ok {
		return v
	}
	if name == "RangeError" {
		if m := workflowBigIntRe.FindStringSubmatch(msg); m != nil {
			return "The number " + m[1] + " cannot be converted to a BigInt because it is not an integer"
		}
	}
	if name == "TypeError" {
		if m := workflowReadPropRe.FindStringSubmatch(msg); m != nil {
			return "Cannot read properties of " + m[2] + " (reading '" + m[1] + "')"
		}
		if m := workflowSetPropRe.FindStringSubmatch(msg); m != nil {
			return "Cannot set properties of " + m[2] + " (setting '" + m[1] + "')"
		}
		if m := workflowReadOnlyRe.FindStringSubmatch(msg); m != nil {
			return "Cannot assign to read only property '" + m[1] + "' of object '#<Object>'"
		}
	}
	return msg
}

// workflowErrorHeader is the first line V8 puts in a stack: "<name>: <message>" (just the name without a message).
func workflowErrorHeader(name, msg string) string {
	if msg == "" {
		return name
	}
	if name == "" {
		return msg
	}
	return name + ": " + msg
}

var (
	workflowFirstFrame   = regexp.MustCompile(`\n\s*at workflow-script\.js:(\d+):(\d+)`)
	workflowCalleeChain  = regexp.MustCompile(`^[A-Za-z_$][\w$]*(?:\??\.[A-Za-z_$][\w$]*)*$`)
	workflowNotAFunction = regexp.MustCompile(`^(?:Object has no member '.*'|Value is not an object: .*|Value is not a function: .*)$`)
	workflowStaticV8     = map[string]string{
		"No initial value":     "Reduce of empty array with no initial value",
		"Malformed URI":        "URI malformed",
		"Invalid array length": "Invalid array length",
		"Class constructor cannot be invoked without 'new'": "Class constructor cannot be invoked without 'new'",
	}
	workflowBigIntRe = regexp.MustCompile(`^Cannot convert (.*) to a BigInt$`)
)

// calleeText is the source text V8 would print for the callee of the call at (line, col): goja reports the position of the
// opening parenthesis. Only plain identifier and member chains are rendered; anything else is left to goja's message.
func (r *wfRealm) calleeText(line, col int) (string, bool) {
	if r.wrapped == "" {
		return "", false
	}
	off := workflowOffset(r.wrapped, line, col-1)
	if off >= len(r.wrapped) || r.wrapped[off] != '(' {
		return "", false
	}
	start := off
	for start > 0 {
		c := r.wrapped[start-1]
		if c == '.' || c == '?' || c == '_' || c == '$' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			start--
			continue
		}
		break
	}
	text := r.wrapped[start:off]
	if !workflowCalleeChain.MatchString(text) {
		return "", false
	}
	return strings.ReplaceAll(text, "?.", "."), true
}

// workflowFormatError is formatWorkflowScriptError (scripted-workflow.ts:865): the stack when it holds the message, else the
// message and the stack.
func (r *wfRealm) formatError(v goja.Value) string {
	message := ""
	stack := ""
	if obj, ok := v.(*goja.Object); ok {
		if m, ok := isJSString(obj.Get("message")); ok {
			message = m
		} else {
			message = r.safeString(v)
		}
		if s, ok := isJSString(obj.Get("stack")); ok {
			stack = s
		}
		if name, ok := isJSString(obj.Get("name")); ok && r.isError(obj) {
			nm := workflowV8Message(name, message)
			if name == "TypeError" && workflowNotAFunction.MatchString(message) {
				if m := workflowFirstFrame.FindStringSubmatch(stack); m != nil {
					l, _ := strconv.Atoi(m[1])
					c, _ := strconv.Atoi(m[2])
					if callee, ok := r.calleeText(l, c); ok {
						nm = callee + " is not a function"
					}
				}
			}
			if nm != message {
				stack = strings.Replace(stack, message, nm, 1)
				message = nm
			}
		}
	} else {
		message = r.safeString(v)
	}
	if stack == "" {
		return message
	}
	stack = workflowV8Stack(stack)
	if strings.Contains(stack, message) {
		return stack
	}
	return message + "\n" + stack
}

// safeString is String(v), "[object Object]" style, never throwing.
func (r *wfRealm) safeString(v goja.Value) (s string) {
	defer func() {
		if recover() != nil {
			s = "[object Object]"
		}
	}()
	if v == nil {
		return "undefined"
	}
	if sym, ok := v.(*goja.Symbol); ok {
		return sym.String()
	}
	return v.String()
}

// ---- JSON.parse with V8's error messages ----

// workflowJSONError is the message V8 gives for a JSON.parse that fails, or "" when text is valid JSON. It follows
// V8's JsonParser (src/json/json-parser.cc) for the shapes a script meets: truncated or malformed child output.
func workflowJSONError(text string) string {
	p := &workflowJSONParser{src: utf16.Encode([]rune(text)), text: text}
	return p.parse()
}

type workflowJSONParser struct {
	src  []uint16
	text string
	pos  int
}

func (p *workflowJSONParser) posMsg(msg string, pos int) string {
	line, col := 1, 1
	for i := 0; i < pos && i < len(p.src); i++ {
		switch p.src[i] {
		case '\n':
			line++
			col = 1
		case '\r':
			if i+1 < len(p.src) && p.src[i+1] == '\n' {
				i++
			}
			line++
			col = 1
		default:
			col++
		}
	}
	sep := " in JSON at position "
	if strings.HasSuffix(msg, " after JSON") {
		sep = " at position "
	}
	return msg + sep + strconv.Itoa(pos) + " (line " + strconv.Itoa(line) + " column " + strconv.Itoa(col) + ")"
}

func (p *workflowJSONParser) unexpected(pos int) string {
	if pos >= len(p.src) {
		return "Unexpected end of JSON input"
	}
	switch p.text {
	case "NaN", "Infinity", "undefined", "[object Object]":
		return "\"" + p.text + "\" is not valid JSON"
	}
	tok := string(utf16.Decode(p.src[pos : pos+1]))
	const maxContext, minLen = 10, 21
	if len(p.src) < minLen {
		return "Unexpected token '" + tok + "', \"" + p.text + "\" is not valid JSON"
	}
	start, end := pos-maxContext, pos+maxContext
	prefix, suffix := "...", "..."
	if start <= 0 {
		start, prefix = 0, ""
	}
	if end >= len(p.src) {
		end, suffix = len(p.src), ""
	}
	return "Unexpected token '" + tok + "', " + prefix + "\"" + string(utf16.Decode(p.src[start:end])) + "\"" + suffix + " is not valid JSON"
}

func (p *workflowJSONParser) ws() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *workflowJSONParser) parse() string {
	p.ws()
	if msg := p.value(); msg != "" {
		return msg
	}
	p.ws()
	if p.pos < len(p.src) {
		return p.posMsg("Unexpected non-whitespace character after JSON", p.pos)
	}
	return ""
}

func (p *workflowJSONParser) literal(word string) string {
	for i := 0; i < len(word); i++ {
		if p.pos+i >= len(p.src) {
			return "Unexpected end of JSON input"
		}
		if p.src[p.pos+i] != uint16(word[i]) {
			return p.unexpected(p.pos + i)
		}
	}
	p.pos += len(word)
	return ""
}

func (p *workflowJSONParser) value() string {
	if p.pos >= len(p.src) {
		return "Unexpected end of JSON input"
	}
	switch c := p.src[p.pos]; {
	case c == '{':
		return p.object()
	case c == '[':
		return p.array()
	case c == '"':
		return p.str()
	case c == 't':
		return p.literal("true")
	case c == 'f':
		return p.literal("false")
	case c == 'n':
		return p.literal("null")
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	}
	return p.unexpected(p.pos)
}

func (p *workflowJSONParser) digit() bool {
	return p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9'
}

func (p *workflowJSONParser) number() string {
	if p.src[p.pos] == '-' {
		p.pos++
		if !p.digit() {
			return p.posMsg("No number after minus sign", p.pos)
		}
	}
	if p.src[p.pos] == '0' {
		p.pos++
		if p.digit() {
			return p.posMsg("Unexpected number", p.pos)
		}
	} else {
		for p.digit() {
			p.pos++
		}
	}
	if p.pos < len(p.src) && p.src[p.pos] == '.' {
		p.pos++
		if !p.digit() {
			return p.posMsg("Unterminated fractional number", p.pos)
		}
		for p.digit() {
			p.pos++
		}
	}
	if p.pos < len(p.src) && (p.src[p.pos] == 'e' || p.src[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.src) && (p.src[p.pos] == '+' || p.src[p.pos] == '-') {
			p.pos++
		}
		if !p.digit() {
			return p.posMsg("Exponent part is missing a number", p.pos)
		}
		for p.digit() {
			p.pos++
		}
	}
	return ""
}

func (p *workflowJSONParser) str() string {
	p.pos++
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == '"':
			p.pos++
			return ""
		case c < 0x20:
			return p.posMsg("Bad control character in string literal", p.pos)
		case c == '\\':
			p.pos++
			if p.pos >= len(p.src) {
				return "Unexpected end of JSON input"
			}
			switch p.src[p.pos] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				p.pos++
			case 'u':
				p.pos++
				for i := 0; i < 4; i++ {
					if p.pos >= len(p.src) {
						return p.posMsg("Bad Unicode escape", p.pos)
					}
					h := p.src[p.pos]
					if !((h >= '0' && h <= '9') || (h >= 'a' && h <= 'f') || (h >= 'A' && h <= 'F')) {
						return p.posMsg("Bad Unicode escape", p.pos)
					}
					p.pos++
				}
			default:
				return p.posMsg("Bad escaped character", p.pos)
			}
		default:
			p.pos++
		}
	}
	return p.posMsg("Unterminated string", len(p.src))
}

func (p *workflowJSONParser) at(c uint16) bool { return p.pos < len(p.src) && p.src[p.pos] == c }

func (p *workflowJSONParser) array() string {
	p.pos++
	p.ws()
	if p.at(']') {
		p.pos++
		return ""
	}
	for {
		p.ws()
		if msg := p.value(); msg != "" {
			return msg
		}
		p.ws()
		switch {
		case p.at(','):
			p.pos++
		case p.at(']'):
			p.pos++
			return ""
		default:
			return p.posMsg("Expected ',' or ']' after array element", p.pos)
		}
	}
}

func (p *workflowJSONParser) object() string {
	p.pos++
	p.ws()
	if p.at('}') {
		p.pos++
		return ""
	}
	if !p.at('"') {
		return p.posMsg("Expected property name or '}'", p.pos)
	}
	for {
		if msg := p.str(); msg != "" {
			return msg
		}
		p.ws()
		if !p.at(':') {
			return p.posMsg("Expected ':' after property name", p.pos)
		}
		p.pos++
		p.ws()
		if msg := p.value(); msg != "" {
			return msg
		}
		p.ws()
		switch {
		case p.at(','):
			p.pos++
			p.ws()
			if !p.at('"') {
				return p.posMsg("Expected double-quoted property name", p.pos)
			}
		case p.at('}'):
			p.pos++
			return ""
		default:
			return p.posMsg("Expected ',' or '}' after property value", p.pos)
		}
	}
}
