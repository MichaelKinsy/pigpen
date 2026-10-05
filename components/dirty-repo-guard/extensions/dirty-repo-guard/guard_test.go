package dirty_repo_guard

import "testing"

// Expected values are what Pi's dirty-repo-guard.ts computes, derived from the
// source (`stdout.trim().length > 0`, `stdout.trim().split("\n").filter(Boolean).length`,
// `if (code !== 0) return; ... if (!ctx.hasUI) return { cancel: true }`) and
// from JavaScript's String.prototype.trim.

func TestJSTrim(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"ascii", " \t\r\nabc \n", "abc"},
		{"nbsp", "\u00a0abc\u00a0", "abc"},
		{"byte order mark", "\ufeffabc\ufeff", "abc"},
		{"line and paragraph separators", "\u2028abc\u2029", "abc"},
		{"ideographic and en spaces", "\u3000\u2003abc\u2003\u3000", "abc"},
		{"vertical tab and form feed", "\v\fabc\f\v", "abc"},
		// U+0085 is not JavaScript whitespace, although Go's unicode.IsSpace says it is.
		{"next line is kept", "\u0085abc\u0085", "\u0085abc\u0085"},
		// U+180E was removed from Zs in Unicode 6.3; JavaScript no longer trims it.
		{"mongolian vowel separator is kept", "\u180eabc", "\u180eabc"},
		{"interior space is kept", " a b ", "a b"},
		{"only whitespace", " \u00a0\ufeff\n\t", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := jsTrim(c.in); got != c.want {
			t.Errorf("%s: jsTrim(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestCountChangedFiles(t *testing.T) {
	cases := []struct {
		name, in string
		want     int
	}{
		{"empty", "", 0},
		{"whitespace only", " \u00a0\ufeff\n\t\n", 0},
		{"one file", " M a.txt\n", 1},
		{"two files", " M a.txt\n?? b.txt\n", 2},
		{"blank lines are dropped", "\n  \n M a.txt\n\n?? b.txt\n\n\n?? c d.txt\n  \n", 3},
		// filter(Boolean) keeps a line that is only "\r"; trim only removes the ends.
		{"carriage return lines", "a\r\n\r\nb", 3},
		{"crlf ends are trimmed", " M a\r\n\r\n", 1},
		{"no trailing newline", " M a.txt\n?? b.txt", 2},
		// An interior whitespace-only line is not empty, so filter(Boolean) keeps it.
		{"interior spaces line counts", "a\n \nb", 3},
	}
	for _, c := range cases {
		if got := countChangedFiles(c.in); got != c.want {
			t.Errorf("%s: countChangedFiles(%q) = %d, want %d", c.name, c.in, got, c.want)
		}
	}
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name        string
		code        int
		stdout      string
		hasUI       bool
		want        verdict
		wantChanged int
	}{
		{"not a repo", 128, "", true, allow, 0},
		{"non-zero exit ignores output", 1, " M a.txt\n", true, allow, 0},
		{"non-zero exit without UI", 128, " M a.txt\n", false, allow, 0},
		{"clean", 0, "", true, allow, 0},
		{"clean without UI", 0, "\n", false, allow, 0},
		{"whitespace only counts as clean", 0, " \u00a0\ufeff\n", true, allow, 0},
		{"dirty with UI asks", 0, " M a.txt\n?? b.txt\n", true, ask, 2},
		{"dirty without UI cancels", 0, " M a.txt\n", false, cancel, 0},
	}
	for _, c := range cases {
		got, changed := decide(c.code, c.stdout, c.hasUI)
		if got != c.want || changed != c.wantChanged {
			t.Errorf("%s: decide(%d, %q, %v) = (%v, %d), want (%v, %d)", c.name, c.code, c.stdout, c.hasUI, got, changed, c.want, c.wantChanged)
		}
	}
}
