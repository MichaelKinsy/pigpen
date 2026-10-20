package bashanalysis

import (
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

const tk = "access-intent/bash/token-collection"

// unrebound is a reader for a program that rebinds nothing, so its words read at their startup values.
var unrebound = bashparse.NewWordReader(bashparse.UnreboundVariables)

func tokenTexts(tokens []PathToken) []string {
	out := make([]string, len(tokens))
	for i, tok := range tokens {
		out[i] = tok.Token
	}
	return out
}

// tokenEffect is a token paired with the effect it carries, and nothing else a token holds.
type tokenEffect struct {
	Token  string
	Effect bashparse.TokenEffect
}

func tokenEffectsOf(tokens []PathToken) []tokenEffect {
	out := make([]tokenEffect, len(tokens))
	for i, tok := range tokens {
		out[i] = tokenEffect{tok.Token, tok.Effect}
	}
	return out
}

// nodeOf parses a bash snippet and returns its first node of typ.
func nodeOf(t *testing.T, cmd, typ string) *bashparse.Node {
	t.Helper()
	node := findFirst(parseRoot(t, cmd), typ)
	if node == nil {
		t.Fatalf("no %s node found in: %s", typ, cmd)
	}
	return node
}

func commandNode(t *testing.T, cmd string) *bashparse.Node { return nodeOf(t, cmd, "command") }

func redirectNode(t *testing.T, cmd string) *bashparse.Node { return nodeOf(t, cmd, "file_redirect") }

// tokensOf is the token texts of the first command node of cmd.
func tokensOf(t *testing.T, cmd string) []string {
	t.Helper()
	return tokenTexts(CollectCommandTokens(commandNode(t, cmd), unrebound))
}

// pathTokensOf is the token texts of a whole snippet, for the surfaces that sit outside the command.
func pathTokensOf(t *testing.T, cmd string) []string {
	t.Helper()
	return tokenTexts(CollectPathCandidateTokens(parseRoot(t, cmd), unrebound))
}

func redirectTokensOf(t *testing.T, cmd string) []string {
	t.Helper()
	return tokenTexts(CollectRedirectTokens(redirectNode(t, cmd), unrebound))
}

func TestExtractCommandName(t *testing.T) {
	name := func(cmd string) string {
		got, ok := ExtractCommandName(commandNode(t, cmd), unrebound)
		if !ok {
			t.Fatalf("no name for %s", cmd)
		}
		return got
	}
	tw(t, tk, "returns the basename for a bare command", func(t *testing.T) { eq(t, name("sed 's/x/y/' file.txt"), "sed") })
	tw(t, tk, "strips the directory prefix from an absolute command path", func(t *testing.T) {
		eq(t, name("/usr/bin/sed 's/x/y/' file.txt"), "sed")
	})
	tw(t, tk, "returns the substitution text when the command name is a command substitution", func(t *testing.T) {
		eq(t, name("$(which sed) 's/x/y/' file.txt"), "$(which sed)")
	})
}

func TestCollectCommandTokensPatternFirst(t *testing.T) {
	tw(t, tk, "sed: skips the first positional (inline pattern) and collects the rest", func(t *testing.T) {
		eq(t, tokensOf(t, "sed 's/x/y/' a.txt b.txt"), s("a.txt", "b.txt"))
	})
	tw(t, tk, "sed -e: skips the explicit script arg-consuming flag and collects positionals", func(t *testing.T) {
		eq(t, tokensOf(t, "sed -e 's/x/y/' file.txt"), s("file.txt"))
	})
	tw(t, tk, "sed -f: treats the next argument as a file path (file-consuming flag)", func(t *testing.T) {
		eq(t, tokensOf(t, "sed -f /scripts/script.sed file.txt"), s("/scripts/script.sed", "file.txt"))
	})
	tw(t, tk, "grep: skips the first positional (pattern) and collects file arguments", func(t *testing.T) {
		eq(t, tokensOf(t, "grep pattern /etc/hosts /etc/passwd"), s("/etc/hosts", "/etc/passwd"))
	})
	tw(t, tk, "grep -e: with explicit -e flag, all positionals are file arguments", func(t *testing.T) {
		eq(t, tokensOf(t, "grep -e pattern /etc/hosts"), s("/etc/hosts"))
	})
	tw(t, tk, "grep: end-of-flags (--) causes subsequent args to be treated as positionals", func(t *testing.T) {
		eq(t, tokensOf(t, "grep -- pattern /etc/hosts"), s("/etc/hosts"))
	})
	tw(t, tk, "sd: skips the first two positionals (FIND and REPLACE_WITH) as patterns", func(t *testing.T) {
		eq(t, tokensOf(t, "sd find replace file.txt other.txt"), s("file.txt", "other.txt"))
	})
	tw(t, tk, "rg: skips the pattern positional and collects file/dir arguments", func(t *testing.T) {
		eq(t, tokensOf(t, "rg pattern /etc/"), s("/etc/"))
	})

	// a consumed flag argument, whatever node type it is (#823)
	for _, command := range []string{
		"grep -A 3 pattern /etc/passwd", "grep -B 2 pattern /etc/passwd", "grep -C 4 pattern /etc/passwd",
		"grep -m 5 pattern /etc/passwd", "rg -C 10 pattern /etc/passwd",
	} {
		t.Run("collects the file operand of "+command, func(t *testing.T) { eq(t, tokensOf(t, command), s("/etc/passwd")) })
	}
	tw(t, tk, "collects the file operand past a variable-expansion argument", func(t *testing.T) {
		eq(t, tokensOf(t, "grep -A $N pattern /etc/passwd"), s("/etc/passwd"))
		eq(t, tokensOf(t, "grep -A ${N} pattern /etc/passwd"), s("/etc/passwd"))
	})
	tw(t, tk, "collects the file operand past a command-substitution argument", func(t *testing.T) {
		eq(t, tokensOf(t, "grep -A $(echo 3) pattern /etc/passwd"), s("/etc/passwd"))
	})
	tw(t, tk, "still projects the operands of a command hosted in a consumed argument", func(t *testing.T) {
		eq(t, tokensOf(t, "grep -A $(cat /etc/shadow) pattern /etc/passwd"), s("/etc/shadow", "/etc/passwd"))
	})
	tw(t, tk, "collects a file-consuming flag's nested execution operands", func(t *testing.T) {
		eq(t, tokensOf(t, "grep -f $(echo x) /etc/passwd"), s("x", "/etc/passwd"))
	})
	tw(t, tk, "collects the file operand of sd past a numeric argument", func(t *testing.T) {
		eq(t, tokensOf(t, "sd -n 3 find replace /etc/hosts"), s("/etc/hosts"))
	})
	tw(t, tk, "projects a quoted substitution's operands past a script flag", func(t *testing.T) {
		eq(t, tokensOf(t, `sed -e "$(cat /etc/shadow)" f.txt`), s("/etc/shadow", "f.txt"))
	})
	tw(t, tk, "projects a quoted substitution's operands past a value flag", func(t *testing.T) {
		eq(t, tokensOf(t, `grep -A "$(cat /etc/shadow)" pattern /etc/passwd`), s("/etc/shadow", "/etc/passwd"))
	})
	tw(t, tk, "projects a quoted substitution's operands past a script-file flag", func(t *testing.T) {
		eq(t, tokensOf(t, `grep -f "$(echo x)" /etc/passwd`), s("x", "$(echo x)", "/etc/passwd"))
	})
	tw(t, tk, "projects a quoted substitution's operands past a declined suffix flag", func(t *testing.T) {
		eq(t, tokensOf(t, `sed -i "" -e "$(cat /etc/shadow)" f.txt`), s("/etc/shadow", "f.txt"))
	})
	tw(t, tk, "reads nothing from a single-quoted argument, which runs nothing", func(t *testing.T) {
		eq(t, tokensOf(t, "grep -e '$(cat /etc/shadow)' f.txt"), s("f.txt"))
	})

	// a pattern positional the parser does not type as an argument (#823)
	for _, c := range [][2]string{
		{"a bare number", "grep 42 /etc/passwd"}, {"a variable expansion", "grep $PATTERN /etc/passwd"},
		{"a braced expansion", "grep ${PATTERN} /etc/passwd"}, {"an arithmetic expansion", "grep $((1 + 2)) /etc/passwd"},
		{"an ANSI-C string", "grep $'x' /etc/passwd"}, {"a number under rg", "rg 3 /etc/passwd"},
	} {
		t.Run("collects the operand past "+c[0], func(t *testing.T) { eq(t, tokensOf(t, c[1]), s("/etc/passwd")) })
	}
	tw(t, tk, "spends both of sd's pattern positionals on numbers", func(t *testing.T) {
		eq(t, tokensOf(t, "sd 1 2 /etc/hosts"), s("/etc/hosts"))
	})
	tw(t, tk, "collects the operand past a substitution pattern, and the nested operand", func(t *testing.T) {
		eq(t, tokensOf(t, "grep $(cat /tmp/p) /etc/passwd"), s("/tmp/p", "/etc/passwd"))
	})
	tw(t, tk, "does not spend the pattern slot on a redirect hosted by the command", func(t *testing.T) {
		eq(t, tokensOf(t, "grep <<< text pattern /etc/passwd"), s("/etc/passwd"))
	})

	// an execution hosted in a quoted positional or operand (#945)
	tw(t, tk, "projects the operands of a substitution spent as the pattern", func(t *testing.T) {
		eq(t, tokensOf(t, `grep "$(cat /etc/shadow)" f.txt`), s("/etc/shadow", "f.txt"))
	})
	tw(t, tk, "projects the operands of a substitution collected as an operand", func(t *testing.T) {
		eq(t, tokensOf(t, `grep pat "$(cat /etc/shadow)"`), s("/etc/shadow", "$(cat /etc/shadow)"))
	})
}
