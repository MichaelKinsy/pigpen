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

// Most bodies hold neither separator. The replacement then must not copy the body (bytes.ReplaceAll returns a
// fresh copy even when nothing matches, and every Entry in a request goes through it).
func TestEscapeLineSeparatorsDoesNotCopyACleanBody(t *testing.T) {
	clean := []byte(`{"state":"plain text","n":1}`)
	if allocs := testing.AllocsPerRun(100, func() { _ = escapeLineSeparators(clean) }); allocs != 0 {
		t.Fatalf("escapeLineSeparators allocated %.0f times for a body without U+2028 or U+2029", allocs)
	}
	got := escapeLineSeparators([]byte("{\"a\":\"x\u2028y\u2029z\"}"))
	if want := `{"a":"x\u2028y\u2029z"}`; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A key is written the way marshalPlain writes it, but a plain one (the SDK's own field names, and a question
// name such as "destructive") does not need an encoder: it is quoted directly.
func TestObjectKeyMatchesMarshalPlainAndSkipsTheEncoderForPlainNames(t *testing.T) {
	for _, name := range []string{"", "state", "beyond_scope", "with space", `quo"te`, `back\slash`, "tab\there", "ünï", "<tag>&", "a\u2028b", "\x7f", "emoji 🐖"} {
		o := newObject()
		o.key(name)
		want, _ := marshalPlain(name)
		if got := o.buf.String(); got != "{"+string(want)+":" {
			t.Errorf("key(%q) wrote %q, want %q", name, got, "{"+string(want)+":")
		}
	}
	if allocs := testing.AllocsPerRun(100, func() { o := newObject(); o.key("destructive") }); allocs > 3 {
		t.Fatalf("a plain key allocated %.0f times; the object and its buffer are the only allocations it needs", allocs)
	}
}

// Below debug level nothing reads the request headers or the response body that debug logging prints, so they
// are not built: logBody parsed the whole response as JSON for a log line that the level filter then dropped.
func TestDebugLogArgumentsAreNotBuiltBelowDebugLevel(t *testing.T) {
	body := []byte(`{"model":"m","answers":{"a":{"type":"noul","noul":0.5}},"usage":{}}`)
	for _, level := range []LogLevel{LogOff, LogError, LogWarn, LogInfo} {
		c, err := NewClient(Config{APIKey: "k", BaseURL: "http://127.0.0.1:1", Getenv: noEnv, LogLevel: level})
		noErr(t, err)
		raw := &RawResponse{Status: 200, Body: body}
		if allocs := testing.AllocsPerRun(50, func() { c.logBody(raw) }); allocs != 0 {
			t.Errorf("level %s: logBody allocated %.0f times; the body must not be parsed for a debug line that is dropped", level, allocs)
		}
	}
	c, err := NewClient(Config{APIKey: "k", BaseURL: "http://127.0.0.1:1", Getenv: noEnv, LogLevel: LogDebug, Logger: &captureLogger{}})
	noErr(t, err)
	c.logBody(&RawResponse{Status: 200, Body: body, tag: "#1 POST /x"})
	if cl := c.logger.(levelLogger).sink.(*captureLogger); len(cl.debug) != 1 {
		t.Fatalf("debug level must still log the parsed body, got %v", cl.debug)
	}
}

type captureLogger struct{ debug []string }

func (l *captureLogger) Debug(m string, _ ...any) { l.debug = append(l.debug, m) }
func (l *captureLogger) Info(string, ...any)      {}
func (l *captureLogger) Warn(string, ...any)      {}
func (l *captureLogger) Error(string, ...any)     {}
