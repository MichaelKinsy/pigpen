package websearch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Defuddle port against the original's output: every file of testdata/defuddle (the fixtures of the Defuddle
// project, MIT, see CREDITS.md) is run through extractWithDefuddle and compared byte for byte with what the original
// made of it (<name>.expected.json, recorded by port/record-defuddle.mjs): the title, the Markdown and the error.

type defuddleExpected struct {
	URL     string  `json:"url"`
	Title   *string `json:"title"`
	Content *string `json:"content"`
	Error   *string `json:"error"`
}

// defuddleFixture is one recorded page: the HTML file and the original's output for it.
type defuddleFixture struct{ name, html, expected string }

// defuddleFixtures lists both corpora: testdata/defuddle (<name>.expected.json) and the Readability fixtures of
// testdata/html (<name>.defuddle.json).
func defuddleFixtures(t *testing.T) []defuddleFixture {
	t.Helper()
	var fixtures []defuddleFixture
	for _, c := range []struct{ dir, suffix string }{{"testdata/defuddle/", ".expected.json"}, {"testdata/html/", ".defuddle.json"}} {
		files, err := filepath.Glob(c.dir + "*" + c.suffix)
		noErr(t, err)
		for _, f := range files {
			name := strings.TrimSuffix(filepath.Base(f), c.suffix)
			fixtures = append(fixtures, defuddleFixture{name: filepath.Base(c.dir) + "/" + name, html: c.dir + name + ".html", expected: f})
		}
	}
	return fixtures
}

func TestDefuddleFixturesEqualTheOriginal(t *testing.T) {
	fixtures := defuddleFixtures(t)
	if len(fixtures) < 450 {
		t.Fatalf("%d recorded fixtures", len(fixtures))
	}
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			var want defuddleExpected
			data, err := os.ReadFile(fx.expected)
			noErr(t, err)
			noErr(t, json.Unmarshal(data, &want))
			src, err := os.ReadFile(fx.html)
			noErr(t, err)
			got, gotErr := extractWithDefuddle(decodeText(src, "text/html"), want.URL)
			if want.Error != nil {
				if gotErr == nil || gotErr.Error() != *want.Error {
					t.Fatalf("error %v, want %q", gotErr, *want.Error)
				}
				return
			}
			if gotErr != nil {
				t.Fatalf("error %v, want none", gotErr)
			}
			if got == nil {
				if want.Content != nil {
					t.Fatalf("no result, want %q", *want.Content)
				}
				return
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
