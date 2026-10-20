package websearch

import (
	"encoding/json"
	"os"
	"testing"
)

// The math helpers of the Defuddle port against the original's output (port/record-defuddle-math.mjs, run under
// Node with mathml-to-latex 1.8.0 and temml 0.13.4): every case is compared byte for byte.

func readMathCases(t *testing.T, name string, into any) {
	t.Helper()
	data, err := os.ReadFile("testdata/defuddle-math/" + name)
	noErr(t, err)
	noErr(t, json.Unmarshal(data, into))
}

func TestMathMLToLatexEqualsTheOriginal(t *testing.T) {
	var cases []struct {
		In  string  `json:"in"`
		Out *string `json:"out"`
		Err string  `json:"err"`
	}
	readMathCases(t, "mathml-expected.json", &cases)
	if len(cases) < 200 {
		t.Fatalf("%d recorded cases", len(cases))
	}
	for i, c := range cases {
		got, ok := dfdMathMLToLatex(c.In)
		switch {
		case c.Out == nil && ok:
			t.Errorf("case %d %q: got %q, want an error (%s)", i, c.In, got, c.Err)
		case c.Out != nil && !ok:
			t.Errorf("case %d %q: error, want %q", i, c.In, *c.Out)
		case c.Out != nil && got != *c.Out:
			t.Errorf("case %d %q:\n got %q\nwant %q", i, c.In, got, *c.Out)
		}
	}
}

func TestDecodeURIEqualsTheOriginal(t *testing.T) {
	var cases []struct {
		In           string  `json:"in"`
		URI          *string `json:"uri"`
		URIErr       string  `json:"uriErr"`
		Component    *string `json:"component"`
		ComponentErr string  `json:"componentErr"`
	}
	readMathCases(t, "uri-expected.json", &cases)
	for _, c := range cases {
		got, ok := jsURIDecode(c.In, jsURIReserved)
		if (c.URI == nil) == ok || (ok && got != *c.URI) {
			t.Errorf("decodeURI(%q) = %q, %v; want %v %s", c.In, got, ok, c.URI, c.URIErr)
		}
		got, ok = jsDecodeURIComponent(c.In)
		if (c.Component == nil) == ok || (ok && got != *c.Component) {
			t.Errorf("decodeURIComponent(%q) = %q, %v; want %v %s", c.In, got, ok, c.Component, c.ComponentErr)
		}
		if c.URI == nil {
			if th := ldTry(func() { jsDecodeURI(c.In) }); th == nil || th.name != "URIError" {
				t.Errorf("jsDecodeURI(%q) did not throw a URIError", c.In)
			}
		}
	}
}

func TestTemmlRenderEqualsTheOriginal(t *testing.T) {
	var cases []struct {
		Latex   string  `json:"latex"`
		Display bool    `json:"display"`
		Out     *string `json:"out"`
		Err     string  `json:"err"`
	}
	readMathCases(t, "latex-expected.json", &cases)
	if len(cases) < 1000 {
		t.Fatalf("%d recorded cases", len(cases))
	}
	failures := 0
	for i, c := range cases {
		got := dfdRenderLatexToMathML(c.Latex, c.Display)
		want := ""
		if c.Out != nil {
			want = *c.Out
		}
		if got != want {
			failures++
			if failures <= 40 {
				t.Errorf("case %d %q (display %v):\n got %q\nwant %q (%s)", i, c.Latex, c.Display, got, want, c.Err)
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d cases differ", failures, len(cases))
	}
}
