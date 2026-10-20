package websearch

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"strings"
	"testing"
)

// The generic decoder behind every provider response (unmarshalAny) must give exactly what
// json.Unmarshal into an `any` gives: the value for a document it parses itself, and the standard
// decoder's own value or error for everything else.

func standardAny(text string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return nil, err
	}
	return v, nil
}

// checkSameDecode fails unless unmarshalAny agrees with the standard decoder on text, and reports
// whether the direct parser took the document.
func checkSameDecode(t *testing.T, name, text string) (fast bool) {
	t.Helper()
	want, wantErr := standardAny(text)
	got, gotErr := unmarshalAny(text)
	if (wantErr == nil) != (gotErr == nil) || (wantErr != nil && wantErr.Error() != gotErr.Error()) {
		t.Fatalf("%s: error %v, standard decoder %v\ninput: %.200q", name, gotErr, wantErr, text)
	}
	if wantErr == nil && !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: value %#v, standard decoder %#v\ninput: %.200q", name, got, want, text)
	}
	if v, ok := fastParseJSON(text); ok {
		if wantErr != nil {
			t.Fatalf("%s: the direct parser accepted a document the standard decoder rejects (%v)\ninput: %.200q", name, wantErr, text)
		}
		if !reflect.DeepEqual(v, want) {
			t.Fatalf("%s: direct parser value %#v, standard decoder %#v\ninput: %.200q", name, v, want, text)
		}
		return true
	}
	return false
}

// fixtureDocuments collects every JSON document of the recorded provider fixtures: each file whole,
// each object or array inside it, and each string value that holds a JSON document (the recorded
// request bodies).
func fixtureDocuments(t *testing.T) map[string]string {
	t.Helper()
	files := []string{
		"testdata/gemini-wire.json",
		"testdata/provider-requests.json",
		"testdata/provider-wire.json",
	}
	docs := map[string]string{}
	var walk func(label string, v any)
	walk = func(label string, v any) {
		switch x := v.(type) {
		case map[string]any:
			b, _ := json.Marshal(x)
			docs[label] = string(b)
			pretty, _ := json.MarshalIndent(x, "", "  ")
			docs[label+" (indented)"] = string(pretty)
			for k, e := range x {
				walk(label+"."+k, e)
			}
		case []any:
			b, _ := json.Marshal(x)
			docs[label] = string(b)
			for i, e := range x {
				walk(fmt.Sprintf("%s[%d]", label, i), e)
			}
		case string:
			if s := strings.TrimSpace(x); (strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")) && json.Valid([]byte(s)) {
				docs[label+" (string)"] = x
			}
		}
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		docs[f] = string(raw)
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		walk(f, v)
	}
	return docs
}

func TestFastJSONMatchesStandardOnRecordedFixtures(t *testing.T) {
	docs := fixtureDocuments(t)
	if len(docs) < 200 {
		t.Fatalf("only %d documents in the fixture corpus", len(docs))
	}
	slow := 0
	for name, text := range docs {
		if !checkSameDecode(t, name, text) {
			slow++
		}
	}
	// Every recorded document is well formed and plain: none may fall back, or the speed-up is lost
	// for the responses these providers actually send.
	if slow > 0 {
		t.Fatalf("%d of %d recorded documents were not taken by the direct parser", slow, len(docs))
	}
}

func TestFastJSONMatchesStandardOnEdgeCases(t *testing.T) {
	deep := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	cases := []struct {
		name, text string
		fast       bool
	}{
		{"object", `{"a":1,"b":[true,false,null,"x"],"c":{"d":-0.5e+3}}`, true},
		{"whitespace around", " \t\r\n{ \"a\" : [ 1 , 2 ] }\n ", true},
		{"empty containers", `{"a":{},"b":[],"c":""}`, true},
		{"top-level scalars", `42`, true},
		{"top-level string", `"x"`, true},
		{"top-level null", `null`, true},
		{"negative zero", `-0`, true},
		{"fraction and exponent", `[0.1,1E2,1e-2,12345678901234567890,0e0]`, true},
		{"escapes", `"a\"b\\c\/d\b\f\n\r\t"`, true},
		{"unicode escape", `"caf\u00e9 \u4e2d \u0000 \u001f"`, true},
		{"surrogate pair", `"\ud83d\ude00"`, true},
		{"raw utf-8", `"café 中 😀 \u2028"`, true},
		{"empty key", `{"":1}`, true},
		{"depth within limit", deep(150), true},
		{"depth beyond limit", deep(300), false},
		{"duplicate key", `{"a":{"x":1},"a":{"y":2}}`, false},
		{"duplicate key scalar", `{"a":1,"a":2}`, false},
		{"lone high surrogate", `"\ud800"`, false},
		{"lone low surrogate", `"\udc00x"`, false},
		{"reversed surrogates", `"\ude00\ud83d"`, false},
		{"high surrogate then other escape", `"\ud83d\u0041"`, false},
		{"invalid utf-8 byte", "\"a\xffb\"", false},
		{"truncated utf-8", "\"a\xe4\xb8\"", false},
		{"number out of range", `1e999`, false},
		{"leading zero", `01`, false},
		{"plus sign", `+1`, false},
		{"bare dot", `.5`, false},
		{"trailing dot", `1.`, false},
		{"bare exponent", `1e`, false},
		{"trailing comma object", `{"a":1,}`, false},
		{"trailing comma array", `[1,]`, false},
		{"missing value", `{"a":}`, false},
		{"missing colon", `{"a" 1}`, false},
		{"unquoted key", `{a:1}`, false},
		{"single quotes", `{'a':1}`, false},
		{"trailing data", `{"a":1} x`, false},
		{"two documents", `{"a":1}{"b":2}`, false},
		{"empty", ``, false},
		{"spaces only", `   `, false},
		{"byte order mark", "\ufeff{}", false},
		{"unterminated string", `"abc`, false},
		{"unterminated object", `{"a":1`, false},
		{"unterminated array", `[1,2`, false},
		{"raw newline in string", "\"a\nb\"", false},
		{"raw tab in string", "\"a\tb\"", false},
		{"bad escape", `"\x"`, false},
		{"short unicode escape", `"\u12"`, false},
		{"bad hex", `"\u12zz"`, false},
		{"nan", `NaN`, false},
		{"truncated literal", `tru`, false},
		{"literal prefix", `truex`, false},
		{"comment", `{"a":1}//x`, false},
	}
	for _, c := range cases {
		if got := checkSameDecode(t, c.name, c.text); got != c.fast {
			t.Errorf("%s: direct parser took the document = %v, want %v", c.name, got, c.fast)
		}
	}
}

// randomDocument builds a random value and renders it with random whitespace and escape choices.
type docGen struct{ r *rand.Rand }

var docStrings = []string{
	"", "a", "title", "https://example.com/a?x=1&y=2", "caf\u00e9", "\u4e2d\u6587", "\U0001F600", "tab\there", "line\nbreak",
	"quote\"inside", "back\\slash", "<html>&amp;</html>", "\u2028\u2029", "\x00\x01\x1f", "a/b", "uri\u007f", "\ufffd",
}

func (g docGen) str() string {
	if g.r.IntN(4) == 0 {
		var b strings.Builder
		for range g.r.IntN(12) {
			b.WriteString(docStrings[g.r.IntN(len(docStrings))])
		}
		return b.String()
	}
	return docStrings[g.r.IntN(len(docStrings))]
}

func (g docGen) quote(s string) string {
	if g.r.IntN(3) == 0 {
		// Escape every non-ASCII rune (surrogate pairs above the BMP) and a few ASCII ones.
		var b strings.Builder
		b.WriteByte('"')
		for _, r := range s {
			switch {
			case r == '"' || r == '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case r < 0x20:
				fmt.Fprintf(&b, "\\u%04x", r)
			case r >= 0x10000:
				r -= 0x10000
				fmt.Fprintf(&b, "\\u%04X\\u%04X", 0xD800+(r>>10), 0xDC00+(r&0x3ff))
			case r >= 0x80 || g.r.IntN(10) == 0:
				fmt.Fprintf(&b, "\\u%04x", r)
			default:
				b.WriteRune(r)
			}
		}
		b.WriteByte('"')
		return b.String()
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func (g docGen) ws() string {
	if g.r.IntN(3) != 0 {
		return ""
	}
	return []string{" ", "\n", "\t", "  ", "\r\n"}[g.r.IntN(5)]
}

func (g docGen) number() string {
	switch g.r.IntN(8) {
	case 0:
		return "0"
	case 1:
		return fmt.Sprint(g.r.IntN(1000) - 500)
	case 2:
		return fmt.Sprintf("%g", g.r.NormFloat64()*1000)
	case 3:
		return fmt.Sprintf("%.3f", g.r.Float64())
	case 4:
		return fmt.Sprintf("%de%d", g.r.IntN(9)+1, g.r.IntN(40)-20)
	case 5:
		return fmt.Sprintf("%d.%dE+%d", g.r.IntN(9), g.r.IntN(1000), g.r.IntN(300))
	case 6:
		return "-0.0"
	}
	return fmt.Sprint(g.r.Int64())
}

func (g docGen) value(depth int) string {
	kind := g.r.IntN(9)
	if depth >= 5 && kind >= 6 {
		kind = g.r.IntN(6)
	}
	switch kind {
	case 0, 1:
		return g.quote(g.str())
	case 2, 3:
		return g.number()
	case 4:
		return []string{"true", "false"}[g.r.IntN(2)]
	case 5:
		return "null"
	case 6, 7:
		n := g.r.IntN(5)
		parts := make([]string, n)
		for i := range parts {
			parts[i] = g.ws() + g.value(depth+1) + g.ws()
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	n := g.r.IntN(5)
	parts := make([]string, n)
	used := map[string]bool{}
	for i := range parts {
		k := g.str()
		if used[k] {
			k += fmt.Sprint(i)
		}
		used[k] = true
		parts[i] = g.ws() + g.quote(k) + g.ws() + ":" + g.ws() + g.value(depth+1) + g.ws()
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func TestFastJSONMatchesStandardOnRandomDocuments(t *testing.T) {
	g := docGen{rand.New(rand.NewPCG(20261020, 1))}
	fast := 0
	const documents = 4000
	const damage = "{}[],:\"\\ 0e.-+\x80\xff"
	for i := range documents {
		text := g.ws() + g.value(0) + g.ws()
		if checkSameDecode(t, fmt.Sprintf("document %d", i), text) {
			fast++
		}
		// The same document with one byte damaged: mostly rejected, sometimes still valid; either
		// way the two decoders must agree on the value or on the error text.
		if len(text) > 0 {
			b := []byte(text)
			b[g.r.IntN(len(b))] = damage[g.r.IntN(len(damage))]
			checkSameDecode(t, fmt.Sprintf("damaged document %d", i), string(b))
			cut := g.r.IntN(len(text) + 1)
			checkSameDecode(t, fmt.Sprintf("truncated document %d", i), text[:cut])
		}
	}
	if fast < documents*9/10 {
		t.Fatalf("the direct parser took %d of %d well-formed documents", fast, documents)
	}
}

func FuzzFastJSONMatchesStandard(f *testing.F) {
	for _, s := range []string{`{"a":[1,2,{"b":"c"}]}`, `"\ud83d\ude00"`, `{"a":1,"a":2}`, `1e999`, "\"\xff\"", ``, `[`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) { checkSameDecode(t, "fuzz", text) })
}
