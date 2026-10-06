// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Differential parity against the oracle's own TypeScript: parity/compare.mjs runs the original's fetch-helpers.ts and
// writes parity/expectations.json, and this file reads that file and compares byte for byte.
//
// Regenerate with (from the Package root):
//
//	node parity/compare.mjs && go test ./extensions/rpiv-web-tools -run TestParity
//
// The expectations are the upstream's output, not the port's, so a divergence fails here rather than passing
// unnoticed — which is the whole point: the twins pin behaviour the upstream states, this pins behaviour it only
// computes.

type parityCase struct {
	Name string `json:"name"`
	Got  string `json:"got"`
}

type contentTypeCase struct {
	Name   string `json:"name"`
	Got    string `json:"got"`
	IsHTML bool   `json:"isHtml"`
}

type parityExpectations struct {
	HTMLToText   []parityCase      `json:"htmlToText"`
	ExtractTitle []parityCase      `json:"extractTitle"`
	ContentTypes []contentTypeCase `json:"contentTypes"`
}

// loadExpectations reads the oracle's output, or skips when it has not been generated.
func loadExpectations(t *testing.T) parityExpectations {
	t.Helper()
	path := filepath.Join("..", "..", "parity", "expectations.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no expectations.json (%v): run node parity/compare.mjs first", err)
	}
	var out parityExpectations
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("the expectations file is unreadable: %v", err)
	}
	if len(out.HTMLToText) == 0 {
		t.Skip("the expectations file is empty")
	}
	return out
}

// fixtureHTML re-reads the fixture the expectations were generated from, so the Go side converts the same bytes.
func fixtureHTML(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "parity", "fixtures.json"))
	if err != nil {
		t.Skipf("no fixtures.json: %v", err)
	}
	var fixtures struct {
		HTML []struct {
			Name string `json:"name"`
			HTML string `json:"html"`
		} `json:"html"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatalf("the fixtures file is unreadable: %v", err)
	}
	out := map[string]string{}
	for _, f := range fixtures.HTML {
		out[f.Name] = f.HTML
	}
	return out
}

func TestParityHTMLToText(t *testing.T) {
	expectations := loadExpectations(t)
	html := fixtureHTML(t)
	for _, c := range expectations.HTMLToText {
		source, ok := html[c.Name]
		if !ok {
			t.Fatalf("the fixture %q is missing from fixtures.json", c.Name)
		}
		t.Run(c.Name, func(t *testing.T) {
			if got := htmlToText(source); got != c.Got {
				t.Errorf("htmlToText diverges from the oracle\n  go:   %q\n  node: %q", got, c.Got)
			}
		})
	}
}

func TestParityExtractTitle(t *testing.T) {
	expectations := loadExpectations(t)
	html := fixtureHTML(t)
	for _, c := range expectations.ExtractTitle {
		t.Run(c.Name, func(t *testing.T) {
			got, ok := extractTitle(html[c.Name])
			if c.Got == "" {
				if ok {
					t.Errorf("the oracle reports no title, the port reports %q", got)
				}
				return
			}
			if !ok || got != c.Got {
				t.Errorf("extractTitle diverges from the oracle\n  go:   %q (present=%v)\n  node: %q", got, ok, c.Got)
			}
		})
	}
}

func TestParityContentTypes(t *testing.T) {
	expectations := loadExpectations(t)
	for _, c := range expectations.ContentTypes {
		t.Run(c.Name, func(t *testing.T) {
			if got := isHTMLContentType(c.Name); got != c.IsHTML {
				t.Errorf("isHtmlContentType(%q): go %v, node %v", c.Name, got, c.IsHTML)
			}
			err := assertTextContentType(c.Name)
			gotMessage := ""
			if err != nil {
				gotMessage = err.Error()
			}
			if gotMessage != c.Got {
				t.Errorf("assertTextContentType(%q) diverges\n  go:   %q\n  node: %q", c.Name, gotMessage, c.Got)
			}
		})
	}
}
