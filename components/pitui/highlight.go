package tui

// Ports packages/coding-agent/src/utils/syntax-highlight.ts and the highlightCode helper of packages/coding-agent/src/modes/interactive/theme/theme.ts. PiG's memo, resume state and active-theme wrappers are not carried over: a caller passes its theme.

import (
	"math"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/MichaelKinsy/pigpen/components/pitui/internal/hljs"
	"github.com/MichaelKinsy/pigpen/components/pitui/jsstring"
)

// highlightEngine is the highlight.js instance type.
type highlightEngine = *hljs.Registry

// highlightRegistry is the highlight.js instance with the languages syntax-highlight.ts registers when it loads.
var highlightRegistry = sync.OnceValue(hljs.NewRegistry)

// HighlightFormatter styles the text of one highlight.js scope.
type HighlightFormatter func(text string) string

// HighlightTheme maps highlight.js scope names to formatters; "default" styles text outside every mapped scope.
type HighlightTheme map[string]HighlightFormatter

// HighlightOptions are the options of syntax-highlight.ts highlight. An empty Language auto-detects among LanguageSubset, or every language.
type HighlightOptions struct {
	Language       string
	IgnoreIllegals bool
	LanguageSubset []string
	Theme          HighlightTheme
}

var loadAllHighlightLanguagesOnce sync.Once

// LoadAllHighlightLanguages registers every highlight.js language, as loadAllHighlightLanguages does, and returns when they are available. It runs once; later calls wait for that load. (Pi loads them in the background when its interactive mode starts; a caller that highlights code in the language of a fence calls this first.)
func LoadAllHighlightLanguages() {
	loadAllHighlightLanguagesOnce.Do(func() { highlightRegistry().LoadAllLanguages() })
}

// SupportsLanguage reports whether highlight.js has a language or alias of that name.
func SupportsLanguage(name string) bool {
	return highlightRegistry().SupportsLanguage(name)
}

// Highlight highlights code with highlight.js and renders its scopes with the theme. An error is an exception highlight.js throws, such as an unknown language.
func Highlight(code string, options HighlightOptions) (string, error) {
	out, err := highlightWith(highlightRegistry(), code, options)
	return terminalText(out), err
}

func highlightWith(registry highlightEngine, code string, options HighlightOptions) (string, error) {
	var html string
	var err error
	if options.Language != "" {
		html, err = registry.Highlight(code, options.Language, options.IgnoreIllegals)
	} else {
		html, err = registry.HighlightAuto(code, options.LanguageSubset)
	}
	if err != nil {
		return "", err
	}
	return renderHighlightedHTML(html, options.Theme)
}

const highlightClassPrefix = "hljs-"

// getScopeFromSpanTag returns the first hljs- class of a span tag without its prefix.
func getScopeFromSpanTag(tag string) (string, bool) {
	classValue, ok := spanClassValue(tag)
	if !ok || classValue == "" {
		return "", false
	}
	for _, className := range splitJSWhitespace(classValue) {
		if scope, ok := strings.CutPrefix(className, highlightClassPrefix); ok {
			return scope, true
		}
	}
	return "", false
}

// spanClassValue is the value matched by /\sclass\s*=\s*(?:"([^"]*)"|'([^']*)')/ in a tag.
func spanClassValue(tag string) (string, bool) {
	for i := 0; i < len(tag); {
		r, size := jsstring.DecodeRuneInString(tag[i:])
		if !jsstring.IsSpace(r) || !strings.HasPrefix(tag[i+size:], "class") {
			i += size
			continue
		}
		j := skipJSWhitespace(tag, i+size+len("class"))
		if j < len(tag) && tag[j] == '=' {
			j = skipJSWhitespace(tag, j+1)
			if j < len(tag) && (tag[j] == '"' || tag[j] == '\'') {
				if end := strings.IndexByte(tag[j+1:], tag[j]); end >= 0 {
					return tag[j+1 : j+1+end], true
				}
			}
		}
		i += size
	}
	return "", false
}

func skipJSWhitespace(text string, i int) int {
	for i < len(text) {
		r, size := jsstring.DecodeRuneInString(text[i:])
		if !jsstring.IsSpace(r) {
			break
		}
		i += size
	}
	return i
}

// splitJSWhitespace is String.prototype.split(/\s+/).
func splitJSWhitespace(text string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(text); {
		r, size := jsstring.DecodeRuneInString(text[i:])
		if !jsstring.IsSpace(r) {
			i += size
			continue
		}
		parts = append(parts, text[start:i])
		i = skipJSWhitespace(text, i)
		start = i
	}
	return append(parts, text[start:])
}

// formatter is a theme[scope] property read, true when the value is truthy. The theme is an object literal: an own property shadows Object.prototype even when it is undefined (a nil entry), and a scope naming an Object.prototype member finds that member. syntax-highlight.ts calls it as a strict-mode function with an undefined receiver: Object(text) is a String wrapper that concatenation reads back as text, toString returns "[object Undefined]", isPrototypeOf returns false for a string, __proto__ is an object, and the other members throw a TypeError.
func (theme HighlightTheme) formatter(scope string) (HighlightFormatter, bool) {
	if f, ok := theme[scope]; ok {
		return f, f != nil
	}
	switch scope {
	case "constructor":
		return func(text string) string { return text }, true
	case "toString":
		return func(string) string { return "[object Undefined]" }, true
	case "isPrototypeOf":
		return func(string) string { return "false" }, true
	case "toLocaleString":
		return throwHighlightThemeError("Object.prototype.toLocaleString called on null or undefined"), true
	case "__proto__":
		return throwHighlightThemeError("formatter is not a function"), true
	case "__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__", "hasOwnProperty", "propertyIsEnumerable", "valueOf":
		return throwHighlightThemeError("Cannot convert undefined or null to object"), true
	}
	return nil, false
}

func throwHighlightThemeError(message string) HighlightFormatter {
	return func(string) string { panic(highlightThemeError{message}) }
}

// highlightThemeError is the TypeError a theme formatter read from Object.prototype throws.
type highlightThemeError struct{ message string }

func (e highlightThemeError) Error() string { return e.message }

func getScopeFormatter(scope string, theme HighlightTheme) (HighlightFormatter, bool) {
	if f, ok := theme.formatter(scope); ok {
		return f, true
	}
	if before, _, ok := strings.Cut(scope, "."); ok {
		if f, ok := theme.formatter(before); ok {
			return f, true
		}
	}
	if before, _, ok := strings.Cut(scope, "-"); ok {
		if f, ok := theme.formatter(before); ok {
			return f, true
		}
	}
	return nil, false
}

// scopeEntry is a span on the scope stack; a span without an hljs- class pushes an undefined scope.
type scopeEntry struct {
	scope   string
	defined bool
}

func getActiveFormatter(scopes []scopeEntry, theme HighlightTheme) (HighlightFormatter, bool) {
	for _, scope := range slices.Backward(scopes) {
		if !scope.defined {
			continue
		}
		if f, ok := getScopeFormatter(scope.scope, theme); ok {
			return f, true
		}
	}
	return theme.formatter("default")
}

func isSpanOpenTagStart(html string, index int) bool {
	if !strings.HasPrefix(html[index:], "<span") {
		return false
	}
	if index+len("<span") >= len(html) {
		return false
	}
	switch html[index+len("<span")] {
	case '>', ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

// RenderHighlightedHtml renders highlight.js HTML as text: each run of text takes the formatter of its innermost mapped scope, or the theme's default, and HTML character references are decoded.
func RenderHighlightedHtml(html string, theme HighlightTheme) string {
	out, err := renderHighlightedHTML(html, theme)
	if err != nil {
		panic(err)
	}
	return terminalText(out)
}

// terminalText is a JavaScript string as Node writes it to a terminal: UTF-8, each lone surrogate replaced by U+FFFD. Highlighting keeps JavaScript's UTF-16 code units (WTF-8) until this boundary, because highlight.js can split a surrogate pair between two scopes.
func terminalText(text string) string {
	if utf8.ValidString(text) {
		return text
	}
	return string(jsstring.ToUTF8(text))
}

func renderHighlightedHTML(html string, theme HighlightTheme) (output string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			thrown, ok := recovered.(highlightThemeError)
			if !ok {
				panic(recovered)
			}
			err = thrown
		}
	}()
	var out, textBuffer strings.Builder
	var scopes []scopeEntry
	flushText := func() {
		if textBuffer.Len() == 0 {
			return
		}
		if f, ok := getActiveFormatter(scopes, theme); ok {
			// Each non-empty line takes the formatter on its own, so a token that spans lines (a multiline string or comment) keeps its color on every line after a terminal wraps or splits the output (#10143).
			for i, line := range strings.Split(textBuffer.String(), "\n") {
				if i > 0 {
					out.WriteByte('\n')
				}
				if line != "" {
					out.WriteString(f(line))
				}
			}
		} else {
			out.WriteString(textBuffer.String())
		}
		textBuffer.Reset()
	}
	for index := 0; index < len(html); {
		if isSpanOpenTagStart(html, index) {
			if tagEnd := strings.IndexByte(html[index+5:], '>'); tagEnd >= 0 {
				tagEnd += index + 5
				flushText()
				scope, ok := getScopeFromSpanTag(html[index : tagEnd+1])
				scopes = append(scopes, scopeEntry{scope, ok})
				index = tagEnd + 1
				continue
			}
		}
		if strings.HasPrefix(html[index:], "</span>") {
			flushText()
			if len(scopes) > 0 {
				scopes = scopes[:len(scopes)-1]
			}
			index += len("</span>")
			continue
		}
		if html[index] == '&' {
			if text, length, ok := decodeHTMLEntityAt(html, index); ok {
				textBuffer.WriteString(text)
				index += length
				continue
			}
		}
		textBuffer.WriteByte(html[index])
		index++
	}
	flushText()
	return out.String(), nil
}

// decodeHTMLEntityAt ports utils/html.ts decodeHtmlEntityAt: the reference ends at the next semicolon within 16 UTF-16 code units. length is in bytes of html.
func decodeHTMLEntityAt(html string, index int) (text string, length int, ok bool) {
	semicolon := strings.IndexByte(html[index+1:], ';')
	if semicolon < 0 {
		return "", 0, false
	}
	semicolon += index + 1
	if len(jsstring.ToUTF16(html[index:semicolon])) > 16 {
		return "", 0, false
	}
	decoded, ok := decodeHTMLEntity(html[index+1 : semicolon])
	if !ok {
		return "", 0, false
	}
	return decoded, semicolon - index + 1, true
}

// decodeHTMLEntity ports utils/html.ts decodeHtmlEntity.
func decodeHTMLEntity(entity string) (string, bool) {
	switch entity {
	case "amp":
		return "&", true
	case "lt":
		return "<", true
	case "gt":
		return ">", true
	case "quot":
		return `"`, true
	case "apos":
		return "'", true
	}
	if rest, ok := strings.CutPrefix(entity, "#x"); ok {
		return decodeCodePoint(jsParseInt(rest, 16))
	}
	if rest, ok := strings.CutPrefix(entity, "#X"); ok {
		return decodeCodePoint(jsParseInt(rest, 16))
	}
	if rest, ok := strings.CutPrefix(entity, "#"); ok {
		return decodeCodePoint(jsParseInt(rest, 10))
	}
	return "", false
}

// decodeCodePoint is String.fromCodePoint for an integer code point; a surrogate code point stays a lone UTF-16 unit (WTF-8).
func decodeCodePoint(codePoint float64) (string, bool) {
	if math.IsNaN(codePoint) || math.IsInf(codePoint, 0) || codePoint != math.Trunc(codePoint) || codePoint < 0 || codePoint > 0x10ffff {
		return "", false
	}
	r := rune(codePoint)
	if r >= 0xd800 && r <= 0xdfff {
		return string([]byte{byte(0xe0 | (r >> 12)), byte(0x80 | ((r >> 6) & 0x3f)), byte(0x80 | (r & 0x3f))}), true
	}
	return string(r), true
}

// jsParseInt is Number.parseInt(text, radix) for radix 10 or 16.
func jsParseInt(text string, radix int) float64 {
	text = text[skipJSWhitespace(text, 0):]
	sign := 1.0
	if text != "" && (text[0] == '+' || text[0] == '-') {
		if text[0] == '-' {
			sign = -1
		}
		text = text[1:]
	}
	if radix == 16 && len(text) >= 2 && text[0] == '0' && (text[1] == 'x' || text[1] == 'X') {
		text = text[2:]
	}
	value, digits := 0.0, 0
	for _, c := range []byte(text) {
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case radix == 16 && c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case radix == 16 && c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			d = radix
		}
		if d >= radix {
			break
		}
		value = float64(value*float64(radix)) + float64(d)
		digits++
	}
	if digits == 0 {
		return math.NaN()
	}
	return sign * value
}

// HighlightMarkdownCode is getMarkdownTheme().highlightCode for a theme given as its cli-highlight theme (theme.ts
// buildCliHighlightTheme) and its mdCodeBlock style: code with no language, in a language highlight.js does not
// support, or that highlight.js fails on takes the codeBlock style, line by line; Pi never auto-detects.
// LoadAllHighlightLanguages decides which languages are supported, so the caller runs it first.
func HighlightMarkdownCode(code, lang string, cli HighlightTheme, codeBlock func(string) string) []string {
	registry := highlightRegistry()
	if lang == "" || !registry.SupportsLanguage(lang) {
		return codeBlockLines(code, codeBlock)
	}
	highlighted, err := highlightWith(registry, code, HighlightOptions{Language: lang, IgnoreIllegals: true, Theme: cli})
	if err != nil {
		return codeBlockLines(code, codeBlock)
	}
	return strings.Split(terminalText(highlighted), "\n")
}

func codeBlockLines(code string, codeBlock func(string) string) []string {
	lines := strings.Split(code, "\n")
	for i, line := range lines {
		lines[i] = codeBlock(line)
	}
	return lines
}
