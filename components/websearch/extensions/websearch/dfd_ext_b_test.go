package websearch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Unit tests of the helpers the extractors of this family share. The extractors themselves are proven against the
// original by the ext-- fixtures of TestDefuddleExtractorFixturesEqualTheOriginal.

func TestExtDecodeAndEncodeURIEqualJavaScript(t *testing.T) {
	cases := []struct{ in, decoded, href string }{
		{"/a%20b", "/a b", "/a%20b"},
		{"/a%2Fb%3F%23", "/a%2Fb%3F%23", "/a%252Fb%253F%2523"},
		{"/caf%C3%A9?x=%E2%9C%93", "/café?x=✓", "/caf%C3%A9?x=%E2%9C%93"},
		{"https://x.test/p q", "https://x.test/p q", "https://x.test/p%20q"},
		{"/%41", "/A", "/A"},
		{"[]{}|", "[]{}|", "%5B%5D%7B%7D%7C"},
	}
	for _, c := range cases {
		if got := jsDecodeURI(c.in); got != c.decoded {
			t.Errorf("decodeURI(%q) = %q, want %q", c.in, got, c.decoded)
		}
		if got := jsEncodeURI(jsDecodeURI(c.in)); got != c.href {
			t.Errorf("href of %q = %q, want %q", c.in, got, c.href)
		}
	}
	for _, bad := range []string{"%", "%4", "%zz", "%C3", "%C3%28", "%80", "%F8%80%80%80%80", "%ED%A0%80", "%C0%80"} {
		th := ldTry(func() { jsDecodeURI(bad) })
		if th == nil || th.name != "URIError" || th.msg != "URI malformed" {
			t.Errorf("decodeURI(%q) did not throw a URIError: %v", bad, th)
		}
	}
}

func TestExtQueryParam(t *testing.T) {
	cases := []struct {
		search, name, want string
		ok                 bool
	}{
		{"?u=https%3A%2F%2Fexample.com%2F%3Fa%3D1", "u", "https://example.com/?a=1", true},
		{"?a=1&u=x+y&u=z", "u", "x y", true},
		{"?u", "u", "", true},
		{"?u=%zz%41", "u", "%zzA", true},
		{"?u=%E4", "u", "\uFFFD", true},
		{"", "u", "", false},
		{"?v=1", "u", "", false},
		{"?%75=q", "u", "q", true},
	}
	for _, c := range cases {
		got, ok := extQueryParam(c.search, c.name)
		if got != c.want || ok != c.ok {
			t.Errorf("get(%q, %q) = %q, %v; want %q, %v", c.search, c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestNytUndefinedToNull(t *testing.T) {
	in := `{"a":undefined,"b":[undefined],"c":"x:undefined,","d":undefined}`
	want := `{"a":null,"b":[undefined],"c":"x:null,","d":null}`
	if got := nytUndefinedToNull(in); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if got := nytUndefinedToNull("undefined:undefined"); got != "undefined:undefined" {
		t.Errorf("got %s", got)
	}
}

func TestYoutubeParseTimestamp(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"1:02:03", 3723, true}, {"12:34", 754, true}, {"0:05", 5, true}, {"5", 0, false}, {"", 0, false},
		{"a:b", 0, false}, {"1:", 60, true}, {" 1 : 2 ", 62, true}, {"1:2:3:4", 0, false},
	}
	for _, c := range cases {
		got, ok := ytParseTimestamp(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseTimestamp(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestSynchronousOnlyExtractorsNeverExtract(t *testing.T) {
	for _, name := range []string{"c2wiki", "xoembed"} {
		doc := ldParseDocument("<html><body><article data-testid=\"tweet\"></article></body></html>")
		ex := dfdExtractorCtors[name](dfdExtCtx{doc: doc, url: "https://wiki.c2.com/?X"})
		if ex.canExtract() {
			t.Errorf("%s accepts a page", name)
		}
	}
}

func TestExtractorsOfThisFamilyAreRegistered(t *testing.T) {
	for _, name := range []string{"bilibili", "bluesky", "c2wiki", "linkedin", "lwn", "mastodon", "medium", "nytimes", "reddit",
		"substack", "threads", "twitter", "wikipedia", "xarticle", "xoembed", "youtube"} {
		if dfdExtractorCtors[name] == nil {
			t.Errorf("no constructor for %s", name)
		}
	}
}

type defuddleMetaExpected struct {
	Author        string `json:"author"`
	Published     string `json:"published"`
	Description   string `json:"description"`
	Site          string `json:"site"`
	Language      string `json:"language"`
	Image         string `json:"image"`
	ExtractorType string `json:"extractorType"`
	WordCount     int    `json:"wordCount"`
}

// The metadata the extractors of this family produce (author, published, description, site, language, image, the
// extractor that answered and the word count) against the original's, recorded by port/record-defuddle-meta.mjs.
// The Markdown and the title are compared by TestDefuddleExtractorFixturesEqualTheOriginal.
func TestDefuddleExtractorMetadataEqualsTheOriginal(t *testing.T) {
	files, err := filepath.Glob("testdata/defuddle/ext--*.meta.json")
	noErr(t, err)
	if len(files) < 50 {
		t.Fatalf("%d metadata fixtures", len(files))
	}
	old := time.Local
	time.Local = time.UTC
	defer func() { time.Local = old }()
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".meta.json")
		t.Run(name, func(t *testing.T) {
			var want defuddleMetaExpected
			data, err := os.ReadFile(f)
			noErr(t, err)
			noErr(t, json.Unmarshal(data, &want))
			var exp defuddleExpected
			data, err = os.ReadFile("testdata/defuddle/" + name + ".expected.json")
			noErr(t, err)
			noErr(t, json.Unmarshal(data, &exp))
			src, err := os.ReadFile("testdata/defuddle/" + name + ".html")
			noErr(t, err)
			loc, _ := newURL(exp.URL, "")
			d := &dfdDefuddle{doc: ldParseDocument(string(src)), url: exp.URL, location: loc.href()}
			var got dfdResult
			if th := ldTry(func() { got = d.parse() }); th != nil {
				t.Fatalf("exception %s", th.msg)
			}
			check := func(field, have, wantV string) {
				if have != wantV {
					t.Errorf("%s %q, want %q", field, have, wantV)
				}
			}
			check("author", got.meta.author, want.Author)
			check("published", got.meta.published, want.Published)
			check("description", got.meta.description, want.Description)
			check("site", got.meta.site, want.Site)
			check("language", got.meta.language, want.Language)
			check("image", got.meta.image, want.Image)
			check("extractorType", got.extractorType, want.ExtractorType)
			if got.wordCount != want.WordCount {
				t.Errorf("wordCount %d, want %d", got.wordCount, want.WordCount)
			}
		})
	}
}
