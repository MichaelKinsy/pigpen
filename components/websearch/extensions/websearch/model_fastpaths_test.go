package websearch

import (
	"math"
	"regexp"
	"strings"
	"testing"
)

// Fast paths of the model-provider helpers against the code they replace.

func refExtractSnippetAround(text string, start, end any) string {
	s, ok1 := start.(float64)
	e, ok2 := end.(float64)
	if !ok1 || !ok2 || text == "" {
		return ""
	}
	before := int(math.Max(0, math.Trunc(s)-100))
	after := int(math.Min(float64(jsLen(text)), math.Trunc(e)+100))
	snippet := jsTrim(markdownLinkRE.ReplaceAllString(jsSlice(text, before, after), "$1"))
	if jsLen(snippet) > 300 {
		return jsSlice(snippet, 0, 297) + "..."
	}
	return snippet
}

func TestExtractSnippetAroundMatchesTheRegexpOnlyImplementation(t *testing.T) {
	texts := []string{
		"", "plain text without a link", "See [the docs](https://example.com/docs) for more.", "[a](b)[c](d)", "[broken](", "]( stray", "[x] (y)",
		"\u00e9\U0001F600 [emoji](https://e.example/\U0001F600) tail", strings.Repeat("word ", 120) + "[l](u) " + strings.Repeat("more ", 80),
		"  padded [link](x)  ", "[](empty)", "[nested [x](y)](z)",
	}
	starts := []float64{0, 4, 20, 150, 400}
	for _, text := range texts {
		for _, s := range starts {
			for _, e := range []float64{s, s + 12, s + 600} {
				if got, want := extractSnippetAround(text, s, e), refExtractSnippetAround(text, s, e); got != want {
					t.Errorf("extractSnippetAround(%q, %v, %v) = %q, want %q", text, s, e, got, want)
				}
			}
		}
	}
	if extractSnippetAround("text", "a", 1.0) != "" || extractSnippetAround("text", 1.0, nil) != "" {
		t.Error("non-number offsets must give an empty snippet")
	}
}

func TestUUIDV4IsAVersion4Variant1UUID(t *testing.T) {
	shape := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for range 200 {
		id := uuidV4()
		if !shape.MatchString(id) {
			t.Fatalf("uuidV4 = %q", id)
		}
		if seen[id] {
			t.Fatalf("uuidV4 repeated %q", id)
		}
		seen[id] = true
	}
}
