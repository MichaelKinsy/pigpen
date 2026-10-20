package warden

import (
	"math/rand"
	"strings"
	"testing"
)

// The reference the scanner of sanitizeDescription is held against: the original's chain of regular expressions
// (conscience.ts sanitizeDescription), translated with JavaScript's `\s`.

const refJSSpace = `[\s\x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}\v]`

var refSanitize = []struct {
	re   *lazyRegexp
	repl string
}{
	{lazyRE(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`), "[credential]"},
	{lazyRE(`\bsk-[A-Za-z0-9_-]{16,}`), "[credential]"},
	{lazyRE(`\bAKIA[0-9A-Z]{16}\b`), "[credential]"},
	{lazyRE(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), "[credential]"},
	{lazyRE(`\bAIza[0-9A-Za-z_-]{30,}`), "[credential]"},
	{lazyRE(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), "[credential]"},
	{lazyRE(`(?i)(?:TOKEN|SECRET|KEY|PASSWORD)` + refJSSpace + `*[=:]` + refJSSpace + `*['"]?[\w\-./]{8,}`), "[credential]"},
	{lazyRE(`(?:^|` + refJSSpace + `)/[\w.~\-/]+(?:` + refJSSpace + `|$)`), " "},
	{lazyRE(`~/[\w.~\-/]+`), "[path]"},
	{lazyRE(`~\w+/[\w.~\-/]+`), "[path]"},
	{lazyRE(`https?://[\w.~\-/]+`), "[url]"},
}

func refSanitizeDescription(text string) string {
	for _, r := range refSanitize {
		text = r.re.ReplaceAllLiteralString(text, r.repl)
	}
	return jsTrim(text)
}

func refHasAbsolutePath(s string) bool {
	return lazyRE(`(?:^|` + refJSSpace + `)/[\w.~\-/]+(?:` + refJSSpace + `|$)`).MatchString(s)
}

// Go-only: the scanner equals the regular expressions on the cases of the upstream tests, on shapes built to sit on
// the edges of every step (boundaries, lengths, the white space either side of a path, case), and on random mixes.
func TestSanitizeDescriptionScannerEqualsTheRegularExpressions(t *testing.T) {
	fixed := []string{
		"Read /Users/anon/project/file.ts for details", "Config at ~/config.json and ~bob/.ssh/config", "See https://docs.example.com/guide for more",
		"Test-driven development for TypeScript projects", "Contains token: sk-live-abc123def456ghi789", "/a /b /c", "/a  /b", "x /a/b/c.d, y", "/a,", " /a", "/a ", "/", " / ", "//", "~/", "~/a", "~a/b", "~/a ~b/c ~/d",
		"ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef1234567890", "xghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ", "ghp_short", "ghp_" + strings.Repeat("a", 19), "ghp_" + strings.Repeat("a", 20), "sk-" + strings.Repeat("b", 15), "sk-" + strings.Repeat("b_-", 6),
		"AKIAABCDEFGHIJKLMNOP", "AKIAABCDEFGHIJKLMNOPQ", "AKIAABCDEFGHIJKLMNO", " AKIAABCDEFGHIJKLMNOP.", "xoxb-1234567890-abc", "xoxz-1234567890-abc", "AIza" + strings.Repeat("c", 30), "AIza" + strings.Repeat("c", 29),
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk", "eyJ1234567890.1234567890", "eyJ1234567890.1234567890.123456789",
		"TOKEN=abcdefgh", "token = 'abcdefgh'", "Key:\u00a0abcdefg1", "PASSWORD:\n\"abcdefgh\"", "secret=abc", "my_secretkey: abcdefghij", "KEY KEY: abcdefghi", "apikey=abcdefghij1", "tokenize: abcdefghij", "http://a", "https://a/b?c", "ftp://a", "http://", "xhttp://y",
		"a\u2028/path\u2028b", "\ufeff/p\u3000", "é/é", "/é", "~é/a", "\u212a" + "EY=abcdefghij", "ſecret=abcdefghij",
	}
	for _, f := range fixed {
		if got, want := sanitizeDescription(f), refSanitizeDescription(f); got != want {
			// The reference's `(?i)` folds the Kelvin sign and the long s, which JavaScript's non-unicode /i does not.
			if strings.ContainsAny(f, "\u212aſ") {
				continue
			}
			t.Errorf("%q: got %q want %q", f, got, want)
		}
		if got, want := hasAbsolutePath(f), refHasAbsolutePath(f); got != want {
			t.Errorf("hasAbsolutePath(%q) = %v want %v", f, got, want)
		}
	}
	frags := []string{"ghp_", "gho_", "sk-", "AKIA", "xoxb-", "AIza", "eyJ", ".", "TOKEN", "secret", "Key", "PASSWORD", "=", ":", " ", "  ", "\t", "\n", "\u00a0", "\u2003", "'", "\"", "/", "//", "~", "~/", "~bob/", "http://", "https://", "a", "Z", "9", "_", "-", "abcdefgh", "ABCDEFGHIJKLMNOP", strings.Repeat("a", 20), strings.Repeat("b", 31), "é", "[", "]", ",", ";", "path/to/file.go"}
	rng := rand.New(rand.NewSource(7))
	for n := 0; n < 30000; n++ {
		var b strings.Builder
		for k := rng.Intn(12) + 1; k > 0; k-- {
			b.WriteString(frags[rng.Intn(len(frags))])
		}
		s := b.String()
		if got, want := sanitizeDescription(s), refSanitizeDescription(s); got != want {
			t.Fatalf("%q: got %q want %q", s, got, want)
		}
		if got, want := hasAbsolutePath(s), refHasAbsolutePath(s); got != want {
			t.Fatalf("hasAbsolutePath(%q) = %v want %v", s, got, want)
		}
	}
}
