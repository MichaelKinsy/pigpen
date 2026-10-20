package websearch

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The JavaScript date parser against V8: testdata/jsdate.expected.json is recorded by port/record-js-date.mjs under
// TZ=UTC.
func TestJSDateParserEqualsV8(t *testing.T) {
	old := time.Local
	time.Local = time.UTC
	defer func() { time.Local = old }()
	data, err := os.ReadFile("testdata/jsdate.expected.json")
	noErr(t, err)
	var cases []struct {
		Input string   `json:"input"`
		MS    *float64 `json:"ms"`
	}
	noErr(t, json.Unmarshal(data, &cases))
	if len(cases) < 100 {
		t.Fatalf("%d cases", len(cases))
	}
	for _, c := range cases {
		got := jsDateFromString(c.Input)
		switch {
		case c.MS == nil && !math.IsNaN(got):
			t.Errorf("%q: %v, want invalid", c.Input, got)
		case c.MS != nil && got != *c.MS:
			t.Errorf("%q: %v, want %v", c.Input, got, *c.MS)
		}
	}
}

func TestExtractorRegistryHasEveryConstructorInOrder(t *testing.T) {
	want := []string{"xarticle", "twitter", "xoembed", "gmail", "reddit", "youtube", "bilibili", "hackernews", "chatgpt", "claude",
		"grok", "gemini", "github", "linkedin", "threads", "bluesky", "medium", "c2wiki", "substack", "nytimes", "wikipedia",
		"mastodon", "discourse", "leetcode", "lwn", "bbcodedata"}
	if len(dfdExtractorMappings) != len(want) {
		t.Fatalf("%d mappings", len(dfdExtractorMappings))
	}
	for i, m := range dfdExtractorMappings {
		if m.name != want[i] {
			t.Errorf("mapping %d is %s, want %s", i, m.name, want[i])
		}
	}
	for _, name := range []string{"gmail", "hackernews", "chatgpt", "claude", "grok", "gemini", "github", "discourse", "leetcode", "bbcodedata"} {
		if dfdExtractorCtors[name] == nil {
			t.Errorf("no constructor for %s", name)
		}
	}
}

func TestExtractorPatterns(t *testing.T) {
	cases := []struct {
		url  string
		want []string // the mappings that match, in order
	}{
		{"https://chatgpt.com/c/abc", []string{"chatgpt", "bbcodedata"}},
		{"https://chatgpt.com/", []string{"bbcodedata"}},
		{"https://claude.ai/", []string{"claude", "bbcodedata"}},
		{"https://grok.com/chat", []string{"grok", "bbcodedata"}},
		{"https://grok.com/chatx", []string{"bbcodedata"}},
		{"https://gemini.google.com/app/x", []string{"gemini", "bbcodedata"}},
		{"https://github.com/a/b/issues/1", []string{"github", "bbcodedata"}},
		{"https://forum.org/t/slug/12", []string{"discourse", "bbcodedata"}},
		{"https://news.ycombinator.com/item?id=1", []string{"hackernews", "bbcodedata"}},
		{"https://mail.google.com/mail/u/0/", []string{"gmail", "bbcodedata"}},
		{"https://leetcode.com/problems/x", []string{"leetcode", "bbcodedata"}},
		{"https://x.com/a/status/1", []string{"xarticle", "twitter", "xoembed", "bbcodedata"}},
		{"https://example.org/@user/123", []string{"mastodon", "bbcodedata"}},
	}
	for _, c := range cases {
		u, ok := newURL(c.url, "")
		if !ok {
			t.Fatal(c.url)
		}
		var got []string
		for i := range dfdExtractorMappings {
			if dfdExtractorMappings[i].matches(u.hostname(), c.url) {
				got = append(got, dfdExtractorMappings[i].name)
			}
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: %v, want %v", c.url, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: %v, want %v", c.url, got, c.want)
			}
		}
	}
}

// The fixtures that trigger the extractors of this file's families (the ext-- fixtures written for them and the
// project's own), against what the original made of each, byte for byte. The whole corpus is
// TestDefuddleFixturesEqualTheOriginal.
func TestDefuddleExtractorFixturesEqualTheOriginal(t *testing.T) {
	names := []string{
		"codeblocks--chatgpt-codemirror", "comments--news.ycombinator.com-item-id=12345678", "extractor--bbcode-data",
		"extractor--chatgpt-citations", "extractor--chatgpt-post-thought-content", "general--github.com-issue-56",
		"general--github.com-test-owner-test-repo-pull-42", "general--news.ycombinator.com-item-id=12345678",
		"gmail--mail.google.com-mail-u-0-thread", "listing--news.ycombinator.com-news",
	}
	files, err := filepath.Glob("testdata/defuddle/ext--*.expected.json")
	noErr(t, err)
	if len(files) < 26 {
		t.Fatalf("%d ext fixtures", len(files))
	}
	for _, f := range files {
		names = append(names, strings.TrimSuffix(filepath.Base(f), ".expected.json"))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			var want defuddleExpected
			data, err := os.ReadFile("testdata/defuddle/" + name + ".expected.json")
			noErr(t, err)
			noErr(t, json.Unmarshal(data, &want))
			src, err := os.ReadFile("testdata/defuddle/" + name + ".html")
			noErr(t, err)
			got, gotErr := extractWithDefuddle(string(src), want.URL)
			if want.Error != nil {
				if gotErr == nil || gotErr.Error() != *want.Error {
					t.Fatalf("error %v, want %q", gotErr, *want.Error)
				}
				return
			}
			if gotErr != nil {
				t.Fatalf("error %v, want none", gotErr)
			}
			if want.Title != nil && got.Title != *want.Title {
				t.Errorf("title %q, want %q", got.Title, *want.Title)
			}
			if want.Content != nil && got.Content != *want.Content {
				t.Errorf("content differs:\n%s", firstDifference(got.Content, *want.Content))
			}
		})
	}
}
