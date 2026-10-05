package sessioningest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Cases added by the adversarial review of the move.

func writeOneUserMessage(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "one.jsonl")
	line := `{"type":"message","message":{"role":"user","content":"` + text + `"}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// maxCharsPerItem shortens what is shown. It must not shrink what is searched: a
// match past the first maxCharsPerItem characters is still a hit.
func TestQueryFindsMatchesPastMaxCharsPerItem(t *testing.T) {
	path := writeOneUserMessage(t, strings.Repeat("a", 500)+" needle "+strings.Repeat("b", 500))
	for _, caseSensitive := range []bool{false, true} {
		q := run(t, map[string]any{"path": path, "mode": "query", "query": "needle", "caseSensitive": caseSensitive, "maxCharsPerItem": 100, "format": "json"}).(queryReport)
		if q.TotalHits != 1 {
			t.Fatalf("caseSensitive=%v: hits = %d, want 1 (the match is at character 501)", caseSensitive, q.TotalHits)
		}
		if !strings.Contains(q.Hits[0].Excerpt, "needle") {
			t.Errorf("caseSensitive=%v: the excerpt must be centered on the match: %q", caseSensitive, q.Hits[0].Excerpt)
		}
		if n := utf8.RuneCountInString(q.Hits[0].Excerpt); n > 100+len(" ... [truncated, 9999 chars total]") {
			t.Errorf("caseSensitive=%v: excerpt is %d characters, want it bounded by maxCharsPerItem", caseSensitive, n)
		}
	}
}

// Case folding can change byte lengths (U+0130 is 2 bytes, its lower case 3), so
// a match offset taken from strings.ToLower(text) is wrong in text itself.
func TestCaseInsensitiveExcerptUsesOffsetsOfTheOriginalText(t *testing.T) {
	path := writeOneUserMessage(t, strings.Repeat("İ", 300)+" needle "+strings.Repeat("z", 300))
	q := run(t, map[string]any{"path": path, "mode": "query", "query": "NEEDLE", "format": "json"}).(queryReport)
	if q.TotalHits != 1 {
		t.Fatalf("hits = %d, want 1", q.TotalHits)
	}
	excerpt := q.Hits[0].Excerpt
	if !strings.Contains(excerpt, "needle") || !utf8.ValidString(excerpt) {
		t.Fatalf("excerpt does not show the match, or splits a character: %q", excerpt)
	}
}
