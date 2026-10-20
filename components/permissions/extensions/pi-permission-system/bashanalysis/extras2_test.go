package bashanalysis

import "testing"

func TestSedScannerEdges(t *testing.T) {
	lit := literalArgWords
	withdraws := func(script string) bool { return SedWithdrawsReadClaim(lit("-n", script)) }
	t.Run("a backslash address needs a delimiter", func(t *testing.T) {
		eq(t, withdraws(`\p`), true)
		eq(t, withdraws(`\,x,p`), false)
	})
	t.Run("a delimiter inside a bracket after ^ and a literal ] ends the proof", func(t *testing.T) {
		eq(t, withdraws("s/[^]/]/;p"), true)
		eq(t, withdraws("s/[]/]/;p"), true)
		eq(t, withdraws("s/[^]]/c/"), false)
	})
	t.Run("a newline inside a bracket ends the proof", func(t *testing.T) {
		eq(t, withdraws("s/[a\nb]/x/"), true)
	})
	t.Run("a bracket class may hold a newline, and a class marker is a colon, dot or equals", func(t *testing.T) {
		eq(t, withdraws("s/[[=a\n=]]/x/"), false)
		eq(t, withdraws("s/[[:a\n:]]/x/"), false)
		eq(t, withdraws("s/[[.a\n.]]/x/"), false)
	})
	t.Run("a delimiter inside a bracket class ends the proof", func(t *testing.T) {
		eq(t, withdraws("s/[[:a/:]]/x/"), true)
		eq(t, withdraws("s/[[.a/.]]/x/"), true)
		eq(t, withdraws("s/[[=a/=]]/x/"), true)
	})
	t.Run("a bracket class is skipped whole", func(t *testing.T) {
		eq(t, withdraws("s/[[:alpha:]/]/;p"), true)
		eq(t, withdraws("s/[[:alpha:]]//;p"), false)
	})
	t.Run("a number after a command takes digits only", func(t *testing.T) {
		eq(t, withdraws("p5"), true)
		eq(t, withdraws("l5;p"), false)
		eq(t, withdraws("l 5 ;p"), false)
	})
}

func TestTokenClassificationEdges(t *testing.T) {
	px, wn := PosixPathFlavor, Win32PathFlavor
	t.Run("a drive path needs a letter, a colon and a separator", func(t *testing.T) {
		pathIs(t, false, "C:", "1:/x", ":/x", `1:\x`, "C", "CC:/x")
		ruleIs(t, px, false, "C:", `1:\x`, `:\x`)
		ruleIs(t, wn, false, "C:")
		pathIs(t, true, "C:/", `C:\`, "z:/x", "Z:/x")
	})
	t.Run("a scheme starts with a letter and holds letters, digits, plus, dot and dash", func(t *testing.T) {
		ruleIs(t, px, true, "1a://x", "+a://x", "_a://x")
		ruleIs(t, px, false, "a-b://x", "a.b://x", "a+b://x", "a1://x", "A://x", "git+ssh://h/x")
		ruleIs(t, px, true, "a_b://x", "a b://x", "a:/x", "a:x/y")
	})
	t.Run("an @ followed by a slash is not a scope", func(t *testing.T) {
		ruleIs(t, px, true, "@/foo/bar")
		bareIs(t, true, "@/x", "@/")
		bareIs(t, false, "@", "@x")
	})
	t.Run("an assignment needs its = before any /", func(t *testing.T) {
		bareIs(t, false, "A=b", "A=/x", "=x")
		bareIs(t, true, "a/b=c", "/=", "x/y=z")
	})
	t.Run("a .. segment is bounded by separators or the token edge", func(t *testing.T) {
		pathIs(t, true, "..", `..\`, "../", `a\..`, "a/..", `a\..\b`, "/..")
		pathIs(t, false, "a..", "..a", "a..b", "...", "a...", "...a", ".")
	})
}

func TestCollectionEdges(t *testing.T) {
	t.Run("a long option may carry any non-space name and a one-line value", func(t *testing.T) {
		eq(t, tokensOf(t, "cat --opt=a=b"), s("--opt=a=b", "a=b"))
		eq(t, tokensOf(t, `cat "--op t=/x"`), s("--op t=/x"))
		eq(t, tokensOf(t, "cat \"--op\u00a0t=/x\""), s("--op\u00a0t=/x"))
		eq(t, tokensOf(t, "cat --opt=a\u2028b"), s("--opt=a\u2028b"))
		eq(t, tokensOf(t, "cat --opt=a\u00e9b"), s("--opt=a\u00e9b", "a\u00e9b"))
	})
	t.Run("a pattern-first command splits a long option the same way", func(t *testing.T) {
		eq(t, tokensOf(t, `grep "--op t=/x" p f`), s("--op t=/x", "p", "f")[1:])
	})
	t.Run("an operand argument's execution is hosted", func(t *testing.T) {
		eq(t, pathTokensOf(t, `for f in "$(cat /etc/shadow)"; do echo; done`), s("$(cat /etc/shadow)", "/etc/shadow"))
		eq(t, pathTokensOf(t, `case "$(cat /etc/shadow)" in a) true;; esac`), s("$(cat /etc/shadow)", "/etc/shadow"))
	})
}

func TestEnumerationEdges(t *testing.T) {
	units := func(command string) []string {
		got := syncCommands(t, command)
		out := make([]string, len(got))
		for i, u := range got {
			out[i] = u.Text
		}
		return out
	}
	t.Run("a hosted redirect joins its neighbours with one space, an absent one keeps the gap", func(t *testing.T) {
		eq(t, units("2>/dev/null git   push"), s("git   push"))
		eq(t, units("git   push > out"), s("git   push"))
		eq(t, units("git   push   2>&1"), s("git   push"))
		eq(t, units("git 2>/dev/null  push"), s("git push"))
		eq(t, units("git  push 2>/dev/null  --force"), s("git  push --force"))
	})
	t.Run("a compound statement's test and declaration children are statements", func(t *testing.T) {
		eq(t, units("if [[ -f x ]]; then ls; fi"), s("if [[ -f x ]]; then ls; fi", "[[ -f x ]]", "ls"))
	})
}
