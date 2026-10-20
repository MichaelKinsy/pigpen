package bashanalysis

import "testing"

func TestCollectCommandTokensInterpreters(t *testing.T) {
	// a flag-supplied script contributes no token
	for _, command := range []string{
		"node -e \"\n// check which packages are installed\nconst fs = require('fs');\n\"",
		`node -e "// x"`, `node --eval "// x"`, "node --eval=//x", "node --eval='// x'", "node -p '1+1'", "node --print '1+1'",
		`bun -e "// x"`, "bun --eval '1+1'", "bun -p '1+1'", "bun --print '1+1'",
		"python3 -c \"# c\nprint(1)\"", "python -c '# c'", "perl -e '// x'", "perl -E 'say 1'", "ruby -e '# x'",
	} {
		t.Run(command+" yields no token", func(t *testing.T) { eq(t, tokensOf(t, command), s()) })
	}
	// a script file stays an operand
	for _, c := range []struct {
		command string
		want    []string
	}{
		{"node build.js /tmp/x", s("build.js", "/tmp/x")},
		{"python3 script.py /tmp/x", s("script.py", "/tmp/x")},
		{"ruby task.rb /tmp/x", s("task.rb", "/tmp/x")},
	} {
		t.Run(c.command+" yields both operands", func(t *testing.T) { eq(t, tokensOf(t, c.command), c.want) })
	}
	tw(t, tk, "reads ruby -E as the encoding flag it is, not a script flag", func(t *testing.T) {
		eq(t, tokensOf(t, "ruby -E utf-8 -e 'code'"), s("utf-8"))
	})
	tw(t, tk, "splits an unrecognized long option's embedded value", func(t *testing.T) {
		eq(t, tokensOf(t, `node --input-type=module -e "// x"`), s("module"))
	})
	tw(t, tk, "still projects a script behind a clustered flag", func(t *testing.T) {
		eq(t, tokensOf(t, "perl -pe 's|a|b|' f.txt"), s("s|a|b|", "f.txt"))
	})
	tw(t, tk, "still collects a redirect destination", func(t *testing.T) {
		eq(t, pathTokensOf(t, `node -e "x" > /tmp/out.txt`), s("/tmp/out.txt"))
	})
	tw(t, tk, "still projects the operands of a script-hosted execution", func(t *testing.T) {
		eq(t, tokensOf(t, `node -e "$(cat /etc/shadow)"`), s("/etc/shadow"))
	})
	tw(t, tk, "leaves a script-hosted execution's operand with its own attribution", func(t *testing.T) {
		node := commandNode(t, `node -e "$(cat /etc/shadow)"`)
		eq(t, tokenEffectsOf(CollectCommandTokens(node, unrebound)), []tokenEffect{{"/etc/shadow", coreRead}})
	})
}

func TestCollectCommandTokensFlagSpellings(t *testing.T) {
	for _, c := range []struct {
		command string
		want    []string
	}{
		{"grep --regexp harmless /etc/passwd", s("/etc/passwd")}, {"grep --regexp=harmless /etc/passwd", s("/etc/passwd")},
		{"grep -eharmless /etc/passwd", s("/etc/passwd")}, {"grep -e harmless /etc/passwd", s("/etc/passwd")},
		{"sed --expression 's/a/b/' /etc/hosts", s("/etc/hosts")}, {"sed --expression=s/a/b/ /etc/hosts", s("/etc/hosts")},
		{"sed -e s/a/b/ /etc/hosts", s("/etc/hosts")}, {"gawk --source '{print}' /etc/passwd", s("/etc/passwd")},
	} {
		t.Run(c.command+" marks the script supplied and yields no pattern token", func(t *testing.T) { eq(t, tokensOf(t, c.command), c.want) })
	}
	for _, command := range []string{
		"grep --file /tmp/patterns /etc/passwd", "grep --file=/tmp/patterns /etc/passwd", "grep -f/tmp/patterns /etc/passwd",
		"grep -f /tmp/patterns /etc/passwd",
	} {
		t.Run(command+" yields the pattern file and the operand", func(t *testing.T) { eq(t, tokensOf(t, command), s("/tmp/patterns", "/etc/passwd")) })
	}
	for _, command := range []string{
		"grep -A3 pattern /etc/passwd", "grep --after-context=3 pattern /etc/passwd", "grep --after-context 3 pattern /etc/passwd",
		"rg --glob '!docs' pattern /etc/passwd", "rg -g!docs pattern /etc/passwd", "rg --replace X pattern /etc/passwd",
		"rg -tpy pattern /etc/passwd",
	} {
		t.Run(command+" consumes its value without supplying the script", func(t *testing.T) { eq(t, tokensOf(t, command), s("/etc/passwd")) })
	}
	tw(t, tk, "reads sd's -f as regex flags, not as a script file", func(t *testing.T) {
		eq(t, tokensOf(t, "sd -f i find replace /etc/hosts"), s("/etc/hosts"))
		eq(t, tokensOf(t, "sd --flags i find replace /etc/hosts"), s("/etc/hosts"))
	})
	tw(t, tk, "consumes an empty suffix argument (the BSD idiom)", func(t *testing.T) {
		eq(t, tokensOf(t, "sed -i '' 's/a/b/' /etc/hosts"), s("/etc/hosts"))
	})
	tw(t, tk, "declines a non-empty argument, which GNU reads as the script", func(t *testing.T) {
		eq(t, tokensOf(t, "sed -i 's/a/b/' /etc/hosts"), s("/etc/hosts"))
	})
	tw(t, tk, "leaves a glued suffix alone", func(t *testing.T) {
		eq(t, tokensOf(t, "sed -i.bak 's/a/b/' /etc/hosts"), s("/etc/hosts"))
	})
	tw(t, tk, "reads --context per tool, since grep's is optional-argument and rg's is not", func(t *testing.T) {
		eq(t, tokensOf(t, "grep --context 2 pattern /etc/passwd"), s("pattern", "/etc/passwd"))
		eq(t, tokensOf(t, "rg --context 2 pattern /etc/passwd"), s("/etc/passwd"))
		eq(t, tokensOf(t, "grep -C 2 pattern /etc/passwd"), s("/etc/passwd"))
		eq(t, tokensOf(t, "grep --context=2 pattern /etc/passwd"), s("2", "/etc/passwd"))
	})
	tw(t, tk, "claims no arity for awk's long forms, whose parser the bare name does not fix", func(t *testing.T) {
		eq(t, tokensOf(t, "awk --field-separator 1 script.awk data.txt"), s("script.awk", "data.txt"))
		eq(t, tokensOf(t, "nawk --assign x=1 script.awk data.txt"), s("x=1", "script.awk", "data.txt"))
		eq(t, tokensOf(t, "awk --file script.awk data.txt"), s("script.awk", "data.txt"))
		eq(t, tokensOf(t, "awk --source '{print}' data.txt"), s("{print}", "data.txt"))
		eq(t, tokensOf(t, "gawk --field-separator , '{print}' /etc/passwd"), s("/etc/passwd"))
		eq(t, tokensOf(t, "gawk --file /tmp/prog.awk data.txt"), s("/tmp/prog.awk", "data.txt"))
		eq(t, tokensOf(t, "awk -F, '{print $1}' /etc/passwd"), s("/etc/passwd"))
		eq(t, tokensOf(t, "awk -v x=1 '{print}' /etc/passwd"), s("/etc/passwd"))
	})
	tw(t, tk, "reads a quoted glued value on a recognized flag as a flag (#957)", func(t *testing.T) {
		eq(t, tokensOf(t, "rg -g'!docs' pattern /etc/passwd"), s("/etc/passwd"))
		eq(t, tokensOf(t, "awk -F':' '/k/{print $2}' f.yml"), s("f.yml"))
		eq(t, tokensOf(t, "sed -i'.bak' 's/a/b/' f.txt"), s("f.txt"))
		eq(t, tokensOf(t, "grep --regexp='/etc/passwd' f.txt"), s("f.txt"))
	})
	tw(t, tk, "reads a recognized flag whose quote opens inside its name", func(t *testing.T) {
		eq(t, tokensOf(t, "grep -'e' /etc/passwd f.txt"), s("f.txt"))
		eq(t, tokensOf(t, "grep --reg'exp=/etc/passwd' f.txt"), s("f.txt"))
	})
	tw(t, tk, "leaves a wholly quoted leading-`-` pattern its positional", func(t *testing.T) {
		eq(t, tokensOf(t, "sd '-old' '-new' file.txt"), s("file.txt"))
		eq(t, tokensOf(t, "sd '-old' 'b' f.txt"), s("f.txt"))
		eq(t, tokensOf(t, "sd '-o'ld '-n'ew file.txt"), s("file.txt"))
		eq(t, tokensOf(t, "grep -i'foo' pattern f.txt"), s("pattern", "f.txt"))
	})
	tw(t, tk, "leaves a cluster whose consuming flag is not first unrecognized", func(t *testing.T) {
		eq(t, tokensOf(t, "grep -ie pattern /etc/passwd"), s("/etc/passwd"))
	})
}

func TestCollectCommandTokensGeneric(t *testing.T) {
	tw(t, tk, "collects all argument tokens after the command name", func(t *testing.T) {
		eq(t, tokensOf(t, "cat /etc/hosts /etc/passwd"), s("/etc/hosts", "/etc/passwd"))
	})
	tw(t, tk, "skips variable assignment prefixes", func(t *testing.T) { eq(t, tokensOf(t, "FOO=/bar cat /etc/hosts"), s("/etc/hosts")) })
	tw(t, tk, "collects no tokens for a bare command with no arguments", func(t *testing.T) { eq(t, tokensOf(t, "ls"), s()) })
	tw(t, tk, "collects the operand of a substitution in command-name position", func(t *testing.T) {
		eq(t, tokensOf(t, "$(cat /etc/shadow)"), s("/etc/shadow"))
	})
	tw(t, tk, "collects the operand of a substitution in a prefix assignment", func(t *testing.T) {
		eq(t, tokensOf(t, "FOO=$(cat /etc/shadow) echo hi"), s("/etc/shadow", "hi"))
	})
	tw(t, tk, "collects a prefix-hosted operand for a pattern-first command too", func(t *testing.T) {
		eq(t, tokensOf(t, "FOO=$(cat /etc/shadow) grep -f p x"), s("/etc/shadow", "p", "x"))
	})
	tw(t, tk, "leaves a prefix assignment's literal value uncollected", func(t *testing.T) {
		eq(t, tokensOf(t, "FOO=/etc/shadow echo hi"), s("hi"))
	})
	tw(t, tk, "projects the operands of a substitution passed as an argument", func(t *testing.T) {
		eq(t, tokensOf(t, `echo "$(cat /etc/shadow)"`), s("/etc/shadow", "$(cat /etc/shadow)"))
	})
	tw(t, tk, "projects the operands of a substitution inside a concatenation", func(t *testing.T) {
		eq(t, tokensOf(t, `cat "prefix$(cat /etc/shadow)"`), s("/etc/shadow", "prefix$(cat /etc/shadow)"))
	})
}
