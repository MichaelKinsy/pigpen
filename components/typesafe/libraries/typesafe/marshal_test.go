package typesafe

import (
	"bytes"
	"encoding/json"
	"testing"
)

// encoding/json passes the output of a Marshaler (and a json.RawMessage) through compact, and Go 1.26
// copies U+2028 and U+2029 through unescaped there when HTML escaping is off, while Go 1.27 always escapes
// them (as it always has for a plain string). A body must not depend on the toolchain that built the client
// (its Content-Length would not), so marshalPlain escapes them everywhere; a parser decodes both forms alike.
func TestMarshalPlainEscapesLineSeparatorsOnEveryGoVersion(t *testing.T) {
	raw := json.RawMessage("{\"k\":\"a\u2028b\u2029c\"}")
	cases := map[string]any{
		"string":      "a\u2028b\u2029c",
		"raw message": raw,
		"entry":       Value(raw),
		"nested":      map[string]any{"state": Value(raw), "text": Text("a\u2028b")},
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := marshalPlain(v)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(got, []byte("\u2028")) || bytes.Contains(got, []byte("\u2029")) {
				t.Fatalf("raw U+2028/U+2029 left in %q", got)
			}
			var back any
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// On Go 1.27 encoding/json never hands marshalPlain a raw separator, so the test above cannot see the escaping
// step there; this one feeds it the raw bytes Go 1.26 produces, so the step is checked on every toolchain.
func TestEscapeLineSeparatorsReplacesTheRawBytesGo126Leaves(t *testing.T) {
	got := string(escapeLineSeparators([]byte("{\"k\":\"a\u2028b\u2029c\\\\u2028\"}")))
	if want := `{"k":"a\u2028b\u2029c\\u2028"}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestMarshalPlainKeepsHTMLAndAnEscapedBackslashIntact(t *testing.T) {
	// A backslash followed by "u2028" is the text \u2028, not a separator, and must stay as written.
	got, err := marshalPlain(map[string]any{"a": "<b>&\\u2028"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":"<b>&\\u2028"}`; string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
