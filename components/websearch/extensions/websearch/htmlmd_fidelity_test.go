package websearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The HTML pipeline against the original's output: every file of testdata/html is served through the
// port's fetch path and compared with what oracle/extract.ts extractContent made of the same file
// (<name>.expected.json, recorded by port/record-html-extract.mjs): title, content and the error
// before the provider checklist. A fixture listed in htmlFixtureDifferences is a recorded difference
// (port/PORT.md): the test then requires that it still differs, so the list cannot go stale.

type htmlExpected struct {
	Title   string  `json:"title"`
	Content string  `json:"content"`
	Error   *string `json:"error"`
}

type htmlMeta struct {
	ContentType string            `json:"contentType"`
	Headers     map[string]string `json:"headers"`
	Status      int               `json:"status"`
}

// htmlFixtureDifferences maps a fixture to why the port's output differs from the original's.
var htmlFixtureDifferences = map[string]string{
	"malformed-misnested": "linkedom (htmlparser2) and x/net/html build different trees for misnested inline tags and an unclosed table: the original keeps \"italic only\" unstyled and turns the table cells into paragraphs, the port applies the HTML adoption agency and loses the table",
}

func fixtureNames(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("testdata/html/*.expected.json")
	noErr(t, err)
	var names []string
	for _, f := range files {
		names = append(names, strings.TrimSuffix(filepath.Base(f), ".expected.json"))
	}
	return names
}

// extractFixture runs one fixture through ExtractContent and returns title, content and the error
// before the provider checklist.
func extractFixture(t *testing.T, name string) htmlExpected {
	t.Helper()
	body, err := os.ReadFile("testdata/html/" + name + ".html")
	noErr(t, err)
	meta := htmlMeta{ContentType: "text/html; charset=utf-8", Status: 200}
	if data, err := os.ReadFile("testdata/html/" + name + ".meta.json"); err == nil {
		noErr(t, json.Unmarshal(data, &meta))
		if meta.ContentType == "" {
			meta.ContentType = "text/html; charset=utf-8"
		}
		if meta.Status == 0 {
			meta.Status = 200
		}
	}
	extractEnv(t, "")
	t.Cleanup(SetPageFetch(func(ctx context.Context, u *url.URL, _ RequestInit) (*http.Response, error) {
		h := http.Header{"Content-Type": {meta.ContentType}}
		for k, v := range meta.Headers {
			h.Set(k, v)
		}
		return &http.Response{StatusCode: meta.Status, Status: http.StatusText(meta.Status), Header: h, Body: io.NopCloser(strings.NewReader(string(body))), Request: &http.Request{URL: u}}, nil
	}))
	res := ExtractContent(bg(), "http://fixtures.test/"+name, ExtractOptions{Lookup: func(context.Context, string) ([]LookupAddress, error) {
		return []LookupAddress{{Address: "93.184.216.34", Family: 4}}, nil
	}})
	got := htmlExpected{Title: res.Title, Content: res.Content}
	if res.Error != nil {
		msg, _, _ := strings.Cut(*res.Error, "\n\nFallback options:")
		got.Error = &msg
	}
	return got
}

func TestHTMLFixturesEqualTheOriginal(t *testing.T) {
	names := fixtureNames(t)
	if len(names) < 25 {
		t.Fatalf("%d recorded fixtures", len(names))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			var want htmlExpected
			data, err := os.ReadFile("testdata/html/" + name + ".expected.json")
			noErr(t, err)
			noErr(t, json.Unmarshal(data, &want))
			got := extractFixture(t, name)
			same := got.Title == want.Title && got.Content == want.Content && (got.Error == nil) == (want.Error == nil) && (got.Error == nil || *got.Error == *want.Error)
			if reason, known := htmlFixtureDifferences[name]; known {
				if same {
					t.Fatalf("%s is listed as a difference (%s) but now matches: remove it from htmlFixtureDifferences and PORT.md", name, reason)
				}
				t.Logf("RECORDED DIFFERENCE: %s", reason)
				return
			}
			if got.Title != want.Title {
				t.Errorf("title %q, want %q", got.Title, want.Title)
			}
			if got.Content != want.Content {
				t.Errorf("content differs:\n%s", firstDifference(got.Content, want.Content))
			}
			if (got.Error == nil) != (want.Error == nil) || (got.Error != nil && *got.Error != *want.Error) {
				t.Errorf("error %v, want %v", derefStr(got.Error), derefStr(want.Error))
			}
		})
	}
}

func derefStr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// firstDifference shows where two strings first differ, with the context around it.
func firstDifference(got, want string) string {
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	from := max(0, i-60)
	return "first difference at byte " + itoa(i) + " (got " + itoa(len(got)) + " bytes, want " + itoa(len(want)) + ")\n got:  " + quote(got[from:min(len(got), i+80)]) + "\n want: " + quote(want[from:min(len(want), i+80)])
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

// BenchmarkExtractFixture is the port's side of port/bench-html.mjs: ExtractContent on two fixtures, the fetch served from memory.
func BenchmarkExtractFixture(b *testing.B) {
	for _, name := range []string{"article-basic", "large-page"} {
		body, err := os.ReadFile("testdata/html/" + name + ".html")
		if err != nil {
			b.Fatal(err)
		}
		b.Run(name, func(b *testing.B) {
			restore := SetPageFetch(func(ctx context.Context, u *url.URL, _ RequestInit) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Status: "OK", Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: &http.Request{URL: u}}, nil
			})
			defer restore()
			opts := ExtractOptions{Lookup: func(context.Context, string) ([]LookupAddress, error) {
				return []LookupAddress{{Address: "93.184.216.34", Family: 4}}, nil
			}}
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if res := ExtractContent(context.Background(), "http://fixtures.test/"+name, opts); res.Error != nil {
					b.Fatal(*res.Error)
				}
			}
		})
	}
}
