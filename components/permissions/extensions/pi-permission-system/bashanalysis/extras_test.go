package bashanalysis

import (
	"strings"
	"testing"
)

// Cases the upstream suite does not state, each pinning a behavior of the original that the
// differential (differential_test.go) records and a mutation of this port would otherwise reach.

func TestJSStringHelpers(t *testing.T) {
	t.Run("utf16Len counts astral characters as two units", func(t *testing.T) {
		eq(t, []int{utf16Len("ab"), utf16Len("é"), utf16Len("😀"), utf16Len("a😀b")}, []int{2, 1, 2, 4})
	})
	t.Run("slice16 is JavaScript's slice", func(t *testing.T) {
		eq(t, slice16("a😀b", 1, 3), "😀")
		eq(t, slice16("a😀b", 3, 4), "b")
		eq(t, slice16("héllo", 1, 3), "él")
		eq(t, slice16("héllo", -3, -1), "ll")
		eq(t, slice16("abc", 2, 1), "")
		eq(t, slice16("abc", 1, 99), "bc")
		eq(t, slice16From("a😀b", 3), "b")
	})
	t.Run("isJSSpace is the WhiteSpace and LineTerminator set", func(t *testing.T) {
		for _, r := range []rune{'\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2000, 0x2005, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff} {
			eq(t, isJSSpace(r), true, string(r))
		}
		for _, r := range []rune{'a', 0x85, 0x180e, 0x200b, 0x1fff, 0x200b + 1} {
			eq(t, isJSSpace(r), false, string(r))
		}
	})
	t.Run("jsLower lowers U+0130 to i plus a combining dot", func(t *testing.T) {
		eq(t, jsLower("G\u0130T"), "gi\u0307t")
		eq(t, Prefix(s("g\u0130t", "x")), s("g\u0130t"))
	})
	t.Run("StripBashCommentLines reads the JavaScript whitespace set", func(t *testing.T) {
		eq(t, StripBashCommentLines("\u00a0# c\nls"), "ls")
		eq(t, StripBashCommentLines("\u2005# c\nls"), "ls")
	})
}

func TestWrapperEdges(t *testing.T) {
	exec := func(u string) (string, bool) { return ExecutedUnitOf(u, words(u)) }
	t.Run("find -ok is an exec flag", func(t *testing.T) {
		eq(t, ClassifyWrapperWords(words("find . -ok rm {} ;")), WrapperIndirection)
		eq(t, ClassifyWrapperWords(words("find . -okdir rm {} ;")), WrapperIndirection)
	})
	t.Run("a long option containing c is not a -c cluster", func(t *testing.T) {
		eq(t, ClassifyWrapperWords(words("bash --norc script")), WrapperKind(""))
	})
	t.Run("a payload index counts every peeled layer, up to four", func(t *testing.T) {
		eq(t, InlineShellPayloadIndex(words("sudo sudo sudo bash -c x")), 5)
		eq(t, InlineShellPayloadIndex(words("sudo sudo sudo sudo bash -c x")), -1)
	})
	t.Run("a payload index stops at the exec terminator", func(t *testing.T) {
		eq(t, InlineShellPayloadIndex(words("find . -exec sh {} ; -c x")), -1)
	})
	t.Run("an ordinary command has no payload even behind a wrapper word", func(t *testing.T) {
		eq(t, InlineShellPayloadIndex(words("grep -c x f")), -1)
	})
	t.Run("an executed unit that starts with an option names nothing", func(t *testing.T) {
		_, ok := exec("bash -c -x")
		eq(t, ok, false)
	})
	t.Run("a lone quote is its own payload", func(t *testing.T) {
		got, ok := exec("bash -c '")
		eq(t, []any{got, ok}, []any{"'", true})
	})
	t.Run("a separate value after -I is skipped", func(t *testing.T) {
		got, _ := exec("xargs -I {} rm {}")
		eq(t, got, "rm {}")
	})
	t.Run("an escaped terminator ends the exec clause", func(t *testing.T) {
		got, _ := exec(`find . -exec rm {} \;`)
		eq(t, got, "rm {}")
	})
	t.Run("a terminator before the exec flag is not the clause's", func(t *testing.T) {
		got, ok := exec("find ';' -exec rm x")
		eq(t, []any{got, ok}, []any{"rm x", true})
	})
	t.Run("an environment assignment starts with a letter or underscore", func(t *testing.T) {
		got, _ := exec("env 1A=b ls")
		eq(t, got, "1A=b ls")
		got, _ = exec("env A1=b ls")
		eq(t, got, "ls")
		got, _ = exec("env _A=b ls")
		eq(t, got, "ls")
	})
	t.Run("an unterminated name is not an assignment", func(t *testing.T) {
		got, _ := exec("env AB ls")
		eq(t, got, "AB ls")
	})
	t.Run("wrapper names match on the basename", func(t *testing.T) {
		eq(t, ClassifyWrapperWords(words("/usr/bin/sudo ls")), WrapperIndirection)
		eq(t, ClassifyWrapperWords(words("/usr/bin/ ls")), WrapperKind(""))
	})
	t.Run("offsets stay UTF-16 through a peel", func(t *testing.T) {
		got, _ := exec("sudo 😀 ls")
		eq(t, got, "😀 ls")
		got, _ = exec("env A=😀 B=é grep x")
		eq(t, got, "grep x")
	})
	t.Run("a timeout operand and a flock operand are skipped", func(t *testing.T) {
		got, _ := exec("flock /tmp/l -c ls")
		eq(t, got, "ls")
		got, _ = exec("timeout 5 ls")
		eq(t, got, "ls")
	})
	t.Run("an option is skipped but not a bare word", func(t *testing.T) {
		got, _ := exec("nice -n 5 ls -l")
		eq(t, got, "ls -l")
		got, _ = exec("sudo -- -l")
		eq(t, got, "")
	})
}

func TestCommandEffectEdges(t *testing.T) {
	t.Run("a long option containing a guarded letter is not a cluster", func(t *testing.T) {
		eq(t, prove("sort", "--random-sort", "f"), coreRead)
	})
	t.Run("the returned roster is a copy", func(t *testing.T) {
		r := PureReaderCore()
		delete(r, "cat")
		eq(t, PureReaderCore()["cat"], true)
	})
	t.Run("a cluster must start with a single dash", func(t *testing.T) {
		eq(t, prove("sort", "o"), coreRead)
		eq(t, prove("sort", "-"), coreRead)
	})
	t.Run("a stem abbreviation needs the whole name", func(t *testing.T) {
		eq(t, prove("sort", "--outputs"), coreRead)
	})
}

func TestAwkEdges(t *testing.T) {
	lit := literalArgWords
	t.Run("an option after the program withdraws the claim", func(t *testing.T) {
		eq(t, AwkWithdrawsReadClaim(lit("{print}", "-F:", "data")), true)
		eq(t, AwkWithdrawsReadClaim(lit("{print}", "-v", "x=1")), true)
	})
	t.Run("a word after -- is the program even when it leads with a dash", func(t *testing.T) {
		eq(t, AwkWithdrawsReadClaim(lit("--", "-x", "data")), false)
	})
	t.Run("a value option consumes the next word", func(t *testing.T) {
		eq(t, AwkWithdrawsReadClaim(lit("-v", "n=3", "NR>=1", "d")), true)
		eq(t, AwkWithdrawsReadClaim(lit("-F", "|", "{print $1}", "d")), false)
	})
	t.Run("the first positional is the program", func(t *testing.T) {
		eq(t, AwkWithdrawsReadClaim(lit("{print}", "NR>=1")), false)
		eq(t, AwkWithdrawsReadClaim(lit("NR>=1", "{print}")), true)
	})
	t.Run("a missing program withdraws the claim", func(t *testing.T) {
		eq(t, AwkWithdrawsReadClaim(nil), true)
	})
}

func TestSedEdges(t *testing.T) {
	lit := literalArgWords
	t.Run("every flag letter and flag word is allowed", func(t *testing.T) {
		for _, f := range []string{"-n", "-E", "-r", "-s", "-u", "-z", "-nEr", "--quiet", "--silent", "--regexp-extended", "--separate",
			"--unbuffered", "--null-data", "--posix", "--debug", "--sandbox"} {
			eq(t, SedWithdrawsReadClaim(lit(f, "p", "f")), false, f)
		}
	})
	t.Run("a flag letter outside the allowlist withdraws", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("-x", "p", "f")), true)
	})
	t.Run("a word after -- is not an option", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("--", "-e", "p")), true)
		eq(t, SedWithdrawsReadClaim(lit("-n", "--", "p", "-i")), false)
	})
	t.Run("-e carries its script attached", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("-ep", "f")), false)
		eq(t, SedWithdrawsReadClaim(lit("-nep", "f")), false)
	})
	t.Run("--expression needs a script", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("--expression")), true)
		eq(t, SedWithdrawsReadClaim(lit("--expression=")), false)
	})
	t.Run("commands that write or run one withdraw with no argument too", func(t *testing.T) {
		for _, c := range []string{"w", "W", "r", "R", "e", "v", "a", "i", "c", "s", "y", "k"} {
			eq(t, SedWithdrawsReadClaim(lit("-n", c)), true, c)
		}
	})
	t.Run("the numbered commands take an optional number", func(t *testing.T) {
		for _, c := range []string{"l5", "q5", "Q5", "l 5", "q", "Q"} {
			eq(t, SedWithdrawsReadClaim(lit("-n", c)), false, c)
		}
		eq(t, SedWithdrawsReadClaim(lit("-n", "p5")), true)
	})
	t.Run("a substitution flag that writes or runs withdraws", func(t *testing.T) {
		for _, c := range []string{"s/a/b/w", "s/a/b/e", "s/a/b/W", "s/a/b/x"} {
			eq(t, SedWithdrawsReadClaim(lit(c)), true, c)
		}
		for _, c := range []string{"s/a/b/", "s/a/b/g", "s/a/b/pIiMm", "s/a/b/9", "s/a/b/0"} {
			eq(t, SedWithdrawsReadClaim(lit(c)), false, c)
		}
	})
	t.Run("blocks must balance", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("-n", "};{")), true)
		eq(t, SedWithdrawsReadClaim(lit("-n", "{p")), true)
		eq(t, SedWithdrawsReadClaim(lit("-n", "{p}")), false)
		eq(t, SedWithdrawsReadClaim(lit("-n", "{p};{p}")), false)
		eq(t, SedWithdrawsReadClaim(lit("-n", "{{p}}")), false)
		eq(t, SedWithdrawsReadClaim(lit("-n", "{p}p")), true)
		eq(t, SedWithdrawsReadClaim(lit("-n", "{p;}")), false)
	})
	t.Run("a command may be negated repeatedly", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("-n", "1!!p")), false)
		eq(t, SedWithdrawsReadClaim(lit("-n", "1 ! p")), false)
		eq(t, SedWithdrawsReadClaim(lit("-n", "!")), true)
	})
	t.Run("a script is a sequence of separated commands", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("-n", "p\np")), false)
		eq(t, SedWithdrawsReadClaim(lit("-n", "p;;p")), false)
		eq(t, SedWithdrawsReadClaim(lit("-n", " \tp ")), false)
		eq(t, SedWithdrawsReadClaim(lit("-n", "pp")), true)
		eq(t, SedWithdrawsReadClaim(lit("-n", "")), false)
	})
	t.Run("addresses", func(t *testing.T) {
		for _, c := range []string{"$p", "1p", "12p", "1,3p", "1,$p", "1 , 3 p", "2~3p", "1~p", "/x/p", "/x/Ip", "/x/IMp", "\\,x,p", "/x/,/y/p", "1,+2p", "1,~2p", "0~2p"} {
			eq(t, SedWithdrawsReadClaim(lit("-n", c)), false, c)
		}
		for _, c := range []string{"1,p", "1,+p", "1,~p", "/x", "\\", "\\xp", "/x/Xp", "1,/y", "1~"} {
			eq(t, SedWithdrawsReadClaim(lit("-n", c)), true, c)
		}
	})
	t.Run("regexes and replacements", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit(`s/[[:alpha:]/]/c/`)), true)
		eq(t, SedWithdrawsReadClaim(lit(`s/[[:alpha:]]/c/`)), false)
		eq(t, SedWithdrawsReadClaim(lit(`s/[[:alpha/c/`)), true)
		eq(t, SedWithdrawsReadClaim(lit(`s/[[.a.]]/c/`)), false)
		eq(t, SedWithdrawsReadClaim(lit(`s/[[=a=]]/c/`)), false)
		eq(t, SedWithdrawsReadClaim(lit(`s/[[x]]/c/`)), false)
		eq(t, SedWithdrawsReadClaim(lit(`s/[^]]/c/`)), false)
		eq(t, SedWithdrawsReadClaim(lit(`s/[]/]/c/`)), true)
		eq(t, SedWithdrawsReadClaim(lit(`s/a\/b/c/`)), false)
		eq(t, SedWithdrawsReadClaim(lit(`s/a/\//`)), false)
		eq(t, SedWithdrawsReadClaim(lit(`s/a/b\n/`)), false)
		eq(t, SedWithdrawsReadClaim(lit(`s/a/b`+"\n"+`/`)), true)
		eq(t, SedWithdrawsReadClaim(lit(`s/a`+"\n"+`b/c/`)), true)
		eq(t, SedWithdrawsReadClaim(lit(`y/abc/\n\\/`)), false)
		eq(t, SedWithdrawsReadClaim(lit(`y/a`+"\n"+`/b/`)), true)
		eq(t, SedWithdrawsReadClaim(lit(`y/a/b/ p`)), true)
		eq(t, SedWithdrawsReadClaim(lit(`y/a/b/ ;p`)), false)
	})
	t.Run("delimiters", func(t *testing.T) {
		for _, d := range []string{"!", ",", "|", "#", "%", ":", ";", "@", "[", "]", "^", "_", "`", "{", "}", "~", ".", "-"} {
			eq(t, SedWithdrawsReadClaim(lit("s"+d+"a"+d+"b"+d)), false, d)
		}
		for _, d := range []string{"a", "5", " ", "\\", "\t"} {
			eq(t, SedWithdrawsReadClaim(lit("s"+d+"a"+d+"b"+d)), true, d)
		}
		eq(t, SedWithdrawsReadClaim(lit("s")), true)
		eq(t, SedWithdrawsReadClaim(lit("y")), true)
	})
	t.Run("labels", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit(":a;ba")), false)
		eq(t, SedWithdrawsReadClaim(lit(":a\nta")), false)
		eq(t, SedWithdrawsReadClaim(lit("Ta")), false)
		eq(t, SedWithdrawsReadClaim(lit(":")), true)
		eq(t, SedWithdrawsReadClaim(lit("b")), false)
		eq(t, SedWithdrawsReadClaim(lit("{b}")), true)
		eq(t, SedWithdrawsReadClaim(lit(": a;p")), false)
	})
	t.Run("comments run to the newline", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("#c w x")), false)
		eq(t, SedWithdrawsReadClaim(lit("#c\nw x")), true)
		eq(t, SedWithdrawsReadClaim(lit("p #c")), true)
	})
	t.Run("scripts are read by UTF-16 unit", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("s/😀/x/")), false)
		eq(t, SedWithdrawsReadClaim(lit("s😀a😀b😀")), true)
		eq(t, SedWithdrawsReadClaim(lit(`s/\😀/x/`)), false)
		eq(t, SedWithdrawsReadClaim(lit("s/é/x/g")), false)
	})
	t.Run("a script that is only a label or only blanks", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit(strings.Repeat(" ", 3))), false)
	})
}
