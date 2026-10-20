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

func defuddleFixtureNames(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("testdata/defuddle/*.expected.json")
	noErr(t, err)
	var names []string
	for _, f := range files {
		names = append(names, strings.TrimSuffix(filepath.Base(f), ".expected.json"))
	}
	return names
}

func TestDefuddleFixturesEqualTheOriginal(t *testing.T) {
	names := defuddleFixtureNames(t)
	if len(names) < 200 {
		t.Fatalf("%d recorded fixtures", len(names))
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
