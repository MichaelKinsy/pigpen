package posthorse

// Tests of the single-pass JSON reader (session_json.go) against the SDK decoder: JSON.parse
// grammar on valid and mutated text, the normalization of an entry line against the reference
// implementation it replaced, and the nesting depth the SDK refuses.

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

type jsonGen struct{ seed uint64 }

func (g *jsonGen) n(k int) int {
	g.seed ^= g.seed << 13
	g.seed ^= g.seed >> 7
	g.seed ^= g.seed << 17
	return int(g.seed % uint64(k))
}

func (g *jsonGen) pick(options ...string) string { return options[g.n(len(options))] }

func (g *jsonGen) space() string { return g.pick("", "", "", " ", "\n", "\t", "\r", "  ") }

func (g *jsonGen) str() string {
	var b strings.Builder
	b.WriteByte('"')
	for range g.n(6) {
		b.WriteString(g.pick("abc", "é", "😀", `\n`, `\"`, `\\`, `\u00e9`, `\ud83d\ude00`, `\ud83d`, `\ude00`, `\u0041`, "/", `\/`, "x y", "needle", `\t`, `\b\f\r`, "\u2028", "\u0000"[:0]+"z"))
	}
	b.WriteByte('"')
	return b.String()
}

func (g *jsonGen) number() string {
	return g.pick("0", "-0", "1", "-1", "12", "123456789012345", "1234567890123456", "123456789012345678", "1.5", "-0.25", "1e3", "1E+3", "1e-3", "0.1e2", "1e999", "-1e999", "1e-999", "9007199254740993", "0.0000001", "100", "-7")
}

func (g *jsonGen) value(depth int) string {
	switch k := g.n(10); {
	case k < 3 || depth > 3:
		return g.str()
	case k < 5:
		return g.number()
	case k == 5:
		return g.pick("true", "false", "null")
	case k < 8:
		var b strings.Builder
		b.WriteString("{" + g.space())
		for i := range g.n(4) {
			if i > 0 {
				b.WriteString("," + g.space())
			}
			b.WriteString(g.pick(`"a"`, `"b"`, `"type"`, `"text"`, `"a"`, `"\u0061"`) + g.space() + ":" + g.space() + g.value(depth+1) + g.space())
		}
		b.WriteString("}")
		return b.String()
	default:
		var b strings.Builder
		b.WriteString("[" + g.space())
		for i := range g.n(4) {
			if i > 0 {
				b.WriteString("," + g.space())
			}
			b.WriteString(g.value(depth+1) + g.space())
		}
		b.WriteString("]")
		return b.String()
	}
}

// entry is an entry line: random members of Pi's keys with values of the right and the wrong type.
func (g *jsonGen) entry() string {
	var b strings.Builder
	b.WriteString("{")
	for i := range 1 + g.n(10) {
		if i > 0 {
			b.WriteString(",")
		}
		key := g.pick("type", "id", "parentId", "timestamp", "message", "summary", "customType", "content", "details", "data", "cwd",
			"parentSession", "display", "handoff", "firstKeptEntryId", "targetId", "replacement", "extra", "id", "message")
		var value string
		switch key {
		case "message":
			value = g.pick(g.message(), g.message(), g.value(1))
		case "replacement":
			value = g.pick("null", `{"content":`+g.value(2)+`}`, `{"other":1,"content":`+g.value(2)+`}`, `{}`, g.value(1))
		case "display":
			value = g.pick("true", "false", `"true"`, "1", "null")
		case "parentId":
			value = g.pick("null", g.str(), g.number(), "{}")
		case "content", "details", "data":
			value = g.value(1)
		default:
			value = g.pick(g.str(), g.str(), g.number(), "null", "true", "[1]")
		}
		b.WriteString(g.space() + `"` + key + `"` + g.space() + ":" + g.space() + value + g.space())
	}
	b.WriteString("}")
	return b.String()
}

func (g *jsonGen) message() string {
	var b strings.Builder
	b.WriteString("{")
	for i := range g.n(8) {
		if i > 0 {
			b.WriteString(",")
		}
		key := g.pick("role", "stopReason", "errorMessage", "content", "summary", "toolName", "namespace", "toolCallId", "details",
			"nestedCalls", "isError", "command", "output", "excludeFromContext", "timestamp", "role", "content")
		var value string
		switch key {
		case "isError", "excludeFromContext":
			value = g.pick("true", "false", `"x"`, "null")
		case "content", "details", "nestedCalls":
			value = g.value(2)
		default:
			value = g.pick(g.str(), g.str(), g.number(), "null")
		}
		b.WriteString(`"` + key + `":` + value)
	}
	b.WriteString("}")
	return b.String()
}

func (g *jsonGen) mutate(s string) string {
	for range g.n(4) {
		if len(s) == 0 {
			return s
		}
		at := g.n(len(s))
		switch g.n(3) {
		case 0:
			s = s[:at] + s[at+1:]
		case 1:
			s = s[:at] + g.pick("{", "}", "[", "]", ",", ":", `"`, `\`, " ", "0", "e", "-", ".", "t", "n", "\n", "\x01", "+", "u", "1") + s[at:]
		default:
			s = s[:at] + g.pick("0", "}", `"`, "x") + s[min(len(s), at+1):]
		}
	}
	return s
}

func sameJSON(a, b any) bool {
	return reflect.DeepEqual(a, b) || (isNegZero(a) && isNegZero(b))
}

func isNegZero(v any) bool {
	f, ok := v.(float64)
	return ok && f == 0 && math.Signbit(f)
}

func TestJSONParseMatchesSDK(t *testing.T) {
	g := &jsonGen{seed: 0x9E3779B97F4A7C15}
	checked, valid := 0, 0
	for round := 0; round < 60_000; round++ {
		text := g.space() + g.value(0) + g.space()
		if round%2 == 1 {
			text = g.mutate(text)
		}
		if !utf8.ValidString(text) {
			continue
		}
		var want any
		wantErr := json.Unmarshal([]byte(text), &want)
		got, ok := jsonParse(text)
		checked++
		if ok != (wantErr == nil) {
			t.Fatalf("jsonParse(%q) ok = %v, SDK error %v", text, ok, wantErr)
		}
		if ok {
			valid++
			if !sameJSON(got, want) {
				t.Fatalf("jsonParse(%q) = %#v, SDK %#v", text, got, want)
			}
		}
	}
	t.Logf("%d texts compared, %d valid", checked, valid)
	if valid < 10_000 {
		t.Errorf("only %d valid texts: the generator is too weak", valid)
	}
}

func TestParseSessionLineMatchesReference(t *testing.T) {
	g := &jsonGen{seed: 0xD1B54A32D192ED03}
	valid := 0
	for round := 0; round < 60_000; round++ {
		text := g.space() + g.entry() + g.space()
		if round%3 == 2 {
			text = g.mutate(text)
		}
		if !utf8.ValidString(text) {
			continue
		}
		want, wantOK := parseSessionLineReference(text)
		got, ok := parseSessionLine(text)
		if ok != wantOK {
			t.Fatalf("parseSessionLine(%q) ok = %v, reference %v", text, ok, wantOK)
		}
		if ok {
			valid++
			if !sameJSON(got, want) {
				t.Fatalf("parseSessionLine(%q) = %#v, reference %#v", text, got, want)
			}
		}
	}
	t.Logf("%d valid entry lines compared", valid)
	if valid < 10_000 {
		t.Errorf("only %d valid lines", valid)
	}
}

func TestParseSessionLineDoesNotAliasItsInput(t *testing.T) {
	buf := []byte(`{"type":"message","id":"abc","message":{"role":"user","content":"hello"}}`)
	entry, ok := parseSessionLine(unsafeString(buf))
	if !ok {
		t.Fatal("not parsed")
	}
	for i := range buf {
		buf[i] = 'X'
	}
	message, _ := recOf(entry["message"])
	if id, _ := str(entry, "id"); id != "abc" || message["content"] != "hello" {
		t.Errorf("entry changed with the input buffer: %v", entry)
	}
}

// H4: V8's JSON.parse has no nesting limit; the SDK's decoder refuses more than 10,000 levels.
func TestJSONParseDeepNesting(t *testing.T) {
	for _, depth := range []int{9_999, 10_001, 100_000} {
		text := `{"type":"message","id":"d","content":` + strings.Repeat("[", depth) + strings.Repeat("]", depth) + `}`
		if _, ok := parseSessionLine(text); !ok {
			t.Errorf("depth %d: not parsed", depth)
		}
		if _, ok := jsonParse(text); !ok {
			t.Errorf("depth %d: jsonParse failed", depth)
		}
	}
	if _, ok := jsonParse(strings.Repeat("[", 100_000)); ok {
		t.Error("unterminated nesting parsed")
	}
}

func TestJSONParseGrammar(t *testing.T) {
	good := []string{`null`, ` true `, `-0`, `1e999`, `[]`, `{}`, `"\ud800"`, `{"a":{"b":[1,2,{"c":null}]}}`, "\t[\r\n1 ,\n2]\n", `"\u0000"`, `-1.5E-3`}
	bad := []string{``, ` `, `01`, `1.`, `.5`, `-`, `+1`, `[1,]`, `{"a":1,}`, `{a:1}`, `'x'`, `"\x"`, `"\u12"`, `"a` + "\n" + `b"`, `nul`, `[1 2]`, `{"a" 1}`, "\ufeff1", "\u00a01", `1 2`, `"\ud83d\u0041\u"`, `[`, `{"a":`, `tru`, `-e1`, `1e`, `1e+`, `"` + "\x01" + `"`}
	for _, s := range good {
		if _, ok := jsonParse(s); !ok {
			t.Errorf("jsonParse(%q) rejected", s)
		}
	}
	for _, s := range bad {
		if v, ok := jsonParse(s); ok {
			t.Errorf("jsonParse(%q) accepted: %#v", s, v)
		}
	}
	if v, _ := jsonParse(`"\ud83d\ude00 \ud83d x \ude00"`); v != "😀 \xed\xa0\xbd x \xed\xb8\x80" {
		t.Errorf("surrogate escapes = %q", v)
	}
	if v, _ := jsonParse(`-0`); !isNegZero(v) {
		t.Errorf("-0 = %v", v)
	}
	if v, _ := jsonParse(`1e999`); v != math.Inf(1) {
		t.Errorf("1e999 = %v", v)
	}
}
