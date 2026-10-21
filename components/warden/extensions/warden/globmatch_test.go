package warden

import (
	"math/rand/v2"
	"testing"
)

// The state walk against the expression compileGlob builds, over generated patterns and paths (including paths with a
// newline or a non-ASCII character, which go to the expression, and patterns with regexp metacharacters).
func TestGlobMatcherAgreesWithTheCompiledExpression(t *testing.T) {
	patterns := []string{"**/", "**", "*", "?", "/", "a", "b", "ab", ".", "ts", "src", ".env", "*.ts", "~", "é", "[", "(", "\\", "+", "^", "$", "|"}
	texts := []string{"a", "b", "/", "//", "src/", ".ts", "x.ts", "a/b", ".env", "é", "\n", " ", "-", "~", "[", "(", "\\", "+", ".", "ts"}
	r := rand.New(rand.NewPCG(41, 41))
	for i := range 120000 {
		pattern, text := genText(r, patterns, 6), genText(r, texts, 8)
		matcher := newGlobMatcher(pattern)
		if matcher == nil || !plainPath(text) {
			continue
		}
		re := compileGlob(pattern)
		if got, want := matcher.match(text), re.Regexp.MatchString(text); got != want {
			t.Fatalf("case %d glob %q path %q: walk %v expression %v", i, pattern, text, got, want)
		}
		if got, want := re.MatchString(text), re.Regexp.MatchString(text); got != want {
			t.Fatalf("case %d glob %q path %q: gated %v expression %v", i, pattern, text, got, want)
		}
	}
	long := ""
	for range 70 {
		long += "ab"
	}
	if newGlobMatcher(long) != nil {
		t.Error("a pattern of more than 63 states has no walk")
	}
}
