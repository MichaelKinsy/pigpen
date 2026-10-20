package websearch

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

// The turndown port against the original's own output: testdata/turndown-cases.json holds what the
// vendored turndown 7.2.4 (configured as extract.ts configures it) makes of each snippet,
// recorded by port/record-turndown.mjs.

func TestTurndownEqualsTheOriginal(t *testing.T) {
	data, err := os.ReadFile("testdata/turndown-cases.json")
	noErr(t, err)
	var cases []struct {
		HTML     string `json:"html"`
		Markdown string `json:"markdown"`
	}
	noErr(t, json.Unmarshal(data, &cases))
	if len(cases) < 150 {
		t.Fatalf("%d recorded cases", len(cases))
	}
	for i, c := range cases {
		t.Run(fmt.Sprintf("%03d %.40q", i, c.HTML), func(t *testing.T) {
			if got := turndown(c.HTML); got != c.Markdown {
				t.Fatalf("turndown(%q)\n got  %q\n want %q", c.HTML, got, c.Markdown)
			}
		})
	}
}

func TestTurndownEscapeAnchorsAtTheStartOfTheText(t *testing.T) {
	// turndown's anchored escapes have no `m` flag: only the start of a text node is a line start.
	for in, want := range map[string]string{
		"a\n- b":    "a\n- b",
		"- b":       `\- b`,
		"1. x":      `1\. x`,
		"a\n1. x":   "a\n1. x",
		"# h":       `\# h`,
		"####### x": "####### x",
		"~~~":       `\~~~`,
		"a_b*c":     `a\_b\*c`,
	} {
		if got := tdEscape(in); got != want {
			t.Errorf("tdEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

// A page with thousands of sibling blocks must convert in time linear in its size: joining the
// outputs must not copy what is already there for every sibling.
func TestTurndownDoesNotCopyTheOutputPerSibling(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are not meaningful under the race detector")
	}
	frag := func(blocks int) string {
		var b strings.Builder
		for i := 0; i < blocks; i++ {
			b.WriteString("<p>" + strings.Repeat("This guide explains the <strong>important</strong> parts. ", 3) + "</p><ul><li>First</li><li>Second <a href=\"https://example.com/x\">linked</a></li></ul>")
		}
		return b.String()
	}
	allocBytes := func(f func()) uint64 {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		f()
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}
	small, big := frag(60), frag(240)
	smallBytes := allocBytes(func() { _ = turndown(small) })
	bigBytes := allocBytes(func() { _ = turndown(big) })
	// Four times the content: linear growth is about 4x, quadratic growth about 16x.
	if ratio := float64(bigBytes) / float64(smallBytes); ratio > 7 {
		t.Fatalf("converting 4x the blocks allocated %.1fx the bytes (%d -> %d)", ratio, smallBytes, bigBytes)
	}
}
