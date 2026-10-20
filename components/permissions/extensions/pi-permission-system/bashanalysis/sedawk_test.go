package bashanalysis

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

const (
	sedf = "access-intent/bash/sed-invocation"
	awkf = "access-intent/bash/awk-invocation"
)

func jsonName(prefix string, v any) string {
	b, _ := json.Marshal(v)
	return prefix + string(b)
}

func cat2(a []bashparse.ArgWord, rest ...[]bashparse.ArgWord) []bashparse.ArgWord {
	for _, r := range rest {
		a = append(a, r...)
	}
	return a
}

func TestSedWithdrawsReadClaim(t *testing.T) {
	lit := literalArgWords
	for _, args := range [][]string{
		{"-n", "1,80p", "f.md"}, {"-n", "-e", "1p", "-e", "$p", "f.md"}, {"-ne", "5q", "f.md"}, {"-nE", "10,20p", "f.md"},
		{"--quiet", "10,20p", "f.md"}, {"--expression=3p", "f.md"}, {"--expression", "3p", "f.md"}, {"1d;$d", "f.md"},
		{"-n", "0~4p", "f.md"}, {"-n", "5,+3p", "f.md"}, {"-n", "5,~4p", "f.md"}, {"-n", "$!N;P;D", "f.md"},
		{"-n", "l 40", "f.md"}, {"-n", "=", "f.md"}, {"", "f.md"}, {"-n", "1p", "--", "-n"}, {"-n", "1p", "-"},
	} {
		t.Run(jsonName("keeps the claim for sed ", args), func(t *testing.T) { eq(t, SedWithdrawsReadClaim(lit(args...)), false) })
	}
	for _, args := range [][]string{
		{"-n", "/x/,/y/p", "f.md"}, {"-n", "/```mermaid/,/```/p", "f.md"}, {"-n", "/^export/Ip", "f.md"}, {"-n", `\|a/b|p`, "f.md"},
		{"-n", "0,/re/p", "f.md"}, {"s/a/b/g", "f.md"}, {"s|a|b|", "f.md"}, {"-n", "s/a/b/2p", "f.md"},
		{"-E", `s/(a)\/b/\1/gI`, "f.md"}, {`s/\.[0-9]\+/[]/g`, "f.md"}, {"s/[[:space:]]*$//", "f.md"}, {"y/abc/xyz/", "f.md"},
		{"/^#/d", "f.md"}, {"-n", "/start/,/end/{/skip/!p}", "f.md"}, {"-n", "/a/{p;q}", "f.md"}, {":a;N;$!ba;s/\\n/ /g", "f.md"},
		{"-n", "/x/{s/a/b/;t done;p;:done\n}", "f.md"}, {"-n", "# a comment\n1p", "f.md"},
	} {
		t.Run(jsonName("keeps the claim for the extended script in sed ", args), func(t *testing.T) {
			eq(t, SedWithdrawsReadClaim(lit(args...)), false)
		})
	}
	tw(t, sedf, "keeps the claim across a multi-line script", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("-n", "1p\n$p", "f.md")), false)
	})
	for _, args := range [][]string{
		{"-i", "s/a/b/", "f"}, {"-i.bak", "1d", "f"}, {"-i", "", "1d", "f"}, {"-ni", "1p", "f"}, {"-I", "", "1d", "f"},
		{"--in-place", "1d", "f"}, {"--in-place=.bak", "1d", "f"}, {"--in", "1d", "f"}, {"--qui", "1p", "f"},
		{"-f", "script.sed", "f"}, {"--file=script.sed", "f"}, {"-l", "5", "1p", "f"}, {"--version"}, {"-n", "1p", "f", "-i"},
	} {
		t.Run(jsonName("withdraws the claim for sed ", args), func(t *testing.T) { eq(t, SedWithdrawsReadClaim(lit(args...)), true) })
	}
	tw(t, sedf, "withdraws the claim when an -e follows a positional", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("p", "-e", "q", "f")), true)
	})
	tw(t, sedf, "withdraws the claim when -e has no script to take", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("-n", "-e")), true)
	})
	tw(t, sedf, "withdraws the claim when there is no script at all", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("-n")), true)
	})
	tw(t, sedf, "withdraws the claim for a computed script", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(cat2(lit("-n"), []bashparse.ArgWord{computedArgWord("$range", true)}, lit("f.md"))), true)
	})
	tw(t, sedf, "withdraws the claim for a computed file, which could spell -i", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(cat2(lit("-n", "1p"), []bashparse.ArgWord{computedArgWord("$f", true)})), true)
	})
	tw(t, sedf, "withdraws the claim for a computed word that cannot lead with a dash", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(cat2(lit("-n"), []bashparse.ArgWord{computedArgWord("p", false)}, lit("f.md"))), true)
	})
	for _, script := range []string{"w out", "1w out", "W out", "r other", "R other", "e rm -rf x", "v", "a text", "1i text", "c text", "p;w out", "1,2", "1pw", "}"} {
		t.Run(jsonName("withdraws the claim for the script ", script), func(t *testing.T) {
			eq(t, SedWithdrawsReadClaim(lit("-n", script, "f.md")), true)
		})
	}
	for _, script := range []string{
		"s/a/b/w out", "s/a/b/gw out", "s/a/b/e", "s/a/b/x", "s/a/b", "s/a/b/;w out", "saxayaw out", `s\a\b\`, "y/ab/c",
		"/x/w out", "/x", "{p", "b end;w out", "s/[]/]x/y/", "s,[^,]*,x,", "/x/{p;:done}",
	} {
		t.Run(jsonName("withdraws the claim for the extended script ", script), func(t *testing.T) {
			eq(t, SedWithdrawsReadClaim(lit("-n", script, "f.md")), true)
		})
	}
	tw(t, sedf, "withdraws the claim for a delimiter inside a bracket expression", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("s/[/]/x/w out", "f.md")), true)
	})
	tw(t, sedf, "withdraws the claim where only GNU's reading writes", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("s/[/]/w out/", "f.md")), true)
	})
	tw(t, sedf, "withdraws the claim for a delimiter inside an address bracket", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("-n", "/[/]/p", "f.md")), true)
	})
	tw(t, sedf, "withdraws the claim for an unterminated bracket expression", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("-n", "/[ab/p", "f.md")), true)
	})
	tw(t, sedf, "withdraws the claim for a command glued to the next one", func(t *testing.T) {
		eq(t, SedWithdrawsReadClaim(lit("-n", "pd", "f.md")), true)
	})
}

func TestAwkWithdrawsReadClaim(t *testing.T) {
	lit := literalArgWords
	for _, args := range [][]string{
		{"{print $1}", "data"}, {"-F:", "{print $2}", "data"}, {"-F", ":", "{print $2}", "data"}, {"-v", "n=3", "NR==n", "data"},
		{"-vn=3", "NR==n", "data"}, {"/```mermaid/,/```/", "f.md"}, {"BEGIN{f=0} /^```/{f=!f; next} f", "f.md"},
		{"NR<=10", "f.md"}, {"{ n++ } END { print n }", "a", "b"}, {"--", "{print}", "data"}, {"{print}", "-"},
		{"{print}", "x=1", "data"}, {"{print}"},
	} {
		t.Run(jsonName("keeps the claim for awk ", args), func(t *testing.T) { eq(t, AwkWithdrawsReadClaim(lit(args...)), false) })
	}
	for _, program := range []string{
		`{print > "out"}`, "{print >> FILENAME}", `{printf "%s", $0 > "/dev/stderr"}`, `{print | "sh"}`, `"date" | getline d`,
		`{print |& "coproc"}`, `BEGIN{system("rm x")}`, `@include "lib.awk"`, "NR>=100",
	} {
		t.Run(jsonName("withdraws the claim for the program ", program), func(t *testing.T) {
			eq(t, AwkWithdrawsReadClaim(lit(program, "data")), true)
		})
	}
	for _, args := range [][]string{
		{"-f", "prog.awk", "data"}, {"-i", "inplace", "{print}", "data"}, {"-e", "{print}", "data"}, {"--source", "{print}", "data"},
		{"--file=prog.awk", "data"}, {"--field-separator", ":", "{print}", "data"}, {"-W", "version"}, {"--version"}, {"-F"},
		{"{print}", "data", "-f"},
	} {
		t.Run(jsonName("withdraws the claim for awk ", args), func(t *testing.T) { eq(t, AwkWithdrawsReadClaim(lit(args...)), true) })
	}
	tw(t, awkf, "withdraws the claim when there is no program", func(t *testing.T) { eq(t, AwkWithdrawsReadClaim(lit("-F:")), true) })
	tw(t, awkf, "withdraws the claim for a computed program", func(t *testing.T) {
		eq(t, AwkWithdrawsReadClaim(cat2([]bashparse.ArgWord{computedArgWord("$prog", true)}, lit("data"))), true)
	})
	tw(t, awkf, "withdraws the claim for a computed file", func(t *testing.T) {
		eq(t, AwkWithdrawsReadClaim(cat2(lit("{print}"), []bashparse.ArgWord{computedArgWord("$f", true)})), true)
	})
	tw(t, awkf, "withdraws the claim for a computed word that cannot lead with a dash", func(t *testing.T) {
		eq(t, AwkWithdrawsReadClaim(cat2(lit("{print}"), []bashparse.ArgWord{computedArgWord(`"x$f"`, false)})), true)
	})
}
