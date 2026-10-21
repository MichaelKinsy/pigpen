package tui

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

func mdCaseSetup(t *testing.T) {
	t.Helper()
	previous := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previous) })
	SetCapabilities(TerminalCapabilities{TrueColor: true})
}
func mdChalk(name string) func(string) string {
	codes := map[string][2]string{"bold": {"\x1b[1m", "\x1b[22m"}, "dim": {"\x1b[2m", "\x1b[22m"}, "italic": {"\x1b[3m", "\x1b[23m"}, "underline": {"\x1b[4m", "\x1b[24m"}, "strike": {"\x1b[9m", "\x1b[29m"}, "blue": {"\x1b[34m", "\x1b[39m"}, "cyan": {"\x1b[36m", "\x1b[39m"}, "yellow": {"\x1b[33m", "\x1b[39m"}, "green": {"\x1b[32m", "\x1b[39m"}, "gray": {"\x1b[90m", "\x1b[39m"}, "magenta": {"\x1b[35m", "\x1b[39m"}}
	pair := codes[name]
	return func(text string) string {
		if text == "" {
			return ""
		}
		return ansiSpan(pair[0], pair[1], text)
	}
}

// .upstream/v0.87.1/packages/tui/test/test-themes.ts:18-33
func mdUpstreamTheme() *MarkdownTheme {
	return &MarkdownTheme{Heading: func(text string) string { return mdChalk("bold")(mdChalk("cyan")(text)) }, Link: mdChalk("blue"), LinkUrl: mdChalk("dim"), Code: mdChalk("yellow"), CodeBlock: mdChalk("green"), CodeBlockBorder: mdChalk("dim"), Quote: mdChalk("italic"), QuoteBorder: mdChalk("dim"), Hr: mdChalk("dim"), ListBullet: mdChalk("cyan"), Bold: mdChalk("bold"), Italic: mdChalk("italic"), Strikethrough: mdChalk("strike"), Underline: mdChalk("underline")}
}
func mdAssert(t *testing.T, value any, condition string) {
	t.Helper()
	ok := false
	switch v := value.(type) {
	case bool:
		ok = v
	case string:
		ok = v != ""
	case int:
		ok = v != 0
	default:
		ok = value != nil
	}
	if !ok {
		t.Fatalf("upstream assertion failed: %s", condition)
	}
}
func mdEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
func mdNotEqual(t *testing.T, got, want any) {
	t.Helper()
	if reflect.DeepEqual(got, want) {
		t.Fatalf("got unexpected %#v", got)
	}
}
func mdLen(value any) int {
	switch v := value.(type) {
	case string:
		return len(utf16.Encode([]rune(v)))
	case []string:
		return len(v)
	default:
		panic("unexpected upstream length operand")
	}
}
func mdReplace(s, pattern, replacement string) string {
	return regexp.MustCompile(pattern).ReplaceAllString(s, replacement)
}
func mdMatch(pattern, s string) bool       { return regexp.MustCompile(pattern).MatchString(s) }
func mdMatches(s, pattern string) []string { return regexp.MustCompile(pattern).FindAllString(s, -1) }
func mdStripSGR(s string) string           { return mdReplace(s, `\x1b\[[0-9;]*m`, "") }
func mdMap(values []string, fn func(string) string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = fn(v)
	}
	return out
}
func mdFilter(values []string, fn func(string) bool) []string {
	var out []string
	for _, v := range values {
		if fn(v) {
			out = append(out, v)
		}
	}
	return out
}
func mdFind(values []string, fn func(string) bool) string {
	for _, v := range values {
		if fn(v) {
			return v
		}
	}
	return ""
}
func mdAt(values []string, index int) string {
	if index < 0 {
		index += len(values)
	}
	if index < 0 || index >= len(values) {
		return ""
	}
	return values[index]
}
func mdSlice(values []string, start int, end ...int) []string {
	if start < 0 {
		start += len(values)
	}
	stop := len(values)
	if len(end) > 0 {
		stop = end[0]
		if stop < 0 {
			stop += len(values)
		}
	}
	start = max(0, min(start, len(values)))
	stop = max(start, min(stop, len(values)))
	return values[start:stop]
}
func mdStringSlice(value string, start, end int) string {
	return string(utf16.Decode(utf16.Encode([]rune(value))[start:end]))
}
func mdPlain(lines []string) []string {
	return mdMap(lines, func(line string) string { return widthx.JSTrimEnd(mdStripSGR(line)) })
}
func mdColumn(line, needle string, after int) int {
	runes := []rune(line)
	if after < 0 || after > len(runes) {
		return -1
	}
	part := string(runes[after:])
	before, _, ok := strings.Cut(part, needle)
	if !ok {
		return -1
	}
	return after + utf8.RuneCountInString(before)
}
func mdLastColumn(line, needle string) int {
	index := strings.LastIndex(line, needle)
	if index < 0 {
		return -1
	}
	return utf8.RuneCountInString(line[:index])
}
