package websearch

import "testing"

func TestJSReEngine(t *testing.T) {
	cases := []struct {
		re, in string
		want   []string // capture strings, "" for a group that did not take part; nil for no match
	}{
		{`^ab`, "abc", []string{"ab"}},
		{`^(a|b)+c`, "abac!", []string{"abac", "a"}},
		{`^a(?=b)`, "ab", []string{"a"}},
		{`^a(?=b)`, "ac", nil},
		{`^a(?!b)`, "ac", []string{"a"}},
		{`^a(?!b)`, "ab", nil},
		{`^\s(?=[A-Z\\$])`, " X", []string{" "}},
		{`^[a-z]{1,3}$`, "abcd", nil},
		{`^[a-z]{1,3}$`, "abc", []string{"abc"}},
		{`^(\+\-|\+|\-)?([0-9]+)?`, "+12x", []string{"+12", "+", "12"}},
		{`^(?:[a-zA-Z\u03B1-\u03C9]|(?:\\(?:alpha|beta)(?:\s+|\{\}|(?![a-zA-Z]))))+`, "x\\alpha y", []string{"x\\alpha "}},
		{`^[^a-z]*$`, "A1\u00a0", []string{"A1\u00a0"}},
		{`^(\d)(?:\.(\d))?`, "1.5", []string{"1.5", "1", "5"}},
		{`^(\d)(?:\.(\d))?`, "1", []string{"1", "1", ""}},
	}
	for _, c := range cases {
		caps, ok, _, matched := tmCompileRe(c.re).exec([]rune(c.in))
		if (c.want == nil) == matched {
			t.Errorf("%s on %q: matched %v", c.re, c.in, matched)
			continue
		}
		if !matched {
			continue
		}
		for i, w := range c.want {
			got := ""
			if ok[i] {
				got = caps[i]
			}
			if got != w {
				t.Errorf("%s on %q: group %d = %q, want %q", c.re, c.in, i, got, w)
			}
		}
	}
}
