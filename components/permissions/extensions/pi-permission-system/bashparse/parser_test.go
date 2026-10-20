package bashparse

import (
	"strings"
	"testing"
)

// Twins of test/access-intent/bash/parser.test.ts.

const fileParser = "access-intent/bash/parser"

// firstStatement is the named child types under the first statement of command's parse.
func firstStatement(t *testing.T, p Parser, command string) []string {
	t.Helper()
	tree := p.Parse(command)
	if tree == nil {
		t.Fatal("parser.Parse returned nil")
	}
	statement := tree.Root.Child(0)
	types := []string{statement.typ}
	for _, c := range statement.children {
		if c.named {
			types = append(types, c.typ)
		}
	}
	return types
}

func mustGetParser(t *testing.T) Parser {
	t.Helper()
	p, err := GetParser()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustGrammarParser(t *testing.T) Parser {
	t.Helper()
	p, err := GrammarParser()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGetParser(t *testing.T) {
	tw(t, fileParser, "parses a simple bash command and returns a non-null root node", func(t *testing.T) {
		tree := mustGetParser(t).Parse("echo hi")
		if tree == nil || tree.Root == nil || tree.Root.Type() != "program" {
			t.Fatalf("tree = %+v", tree)
		}
	})
	tw(t, fileParser, "returns the same memoized parser instance on repeated calls", func(t *testing.T) {
		first, second := mustGetParser(t), mustGetParser(t)
		if first != second {
			t.Error("GetParser returned two instances")
		}
	})
}

func TestGetGrammarParser(t *testing.T) {
	tw(t, fileParser, "returns the same memoized parser instance on repeated calls", func(t *testing.T) {
		first, second := mustGrammarParser(t), mustGrammarParser(t)
		if first != second {
			t.Error("GrammarParser returned two instances")
		}
	})
}

func TestWordsAfterARedirectsTarget(t *testing.T) {
	tw(t, fileParser, "are the command's own words through getParser", func(t *testing.T) {
		eq(t, firstStatement(t, mustGetParser(t), "git 2>/dev/null push --force"),
			[]string{"command", "command_name", "file_redirect", "word", "word"})
	})
	tw(t, fileParser, "stay on the redirect through getGrammarParser", func(t *testing.T) {
		eq(t, firstStatement(t, mustGrammarParser(t), "git 2>/dev/null push --force"),
			[]string{"redirected_statement", "command", "file_redirect"})
	})
	tw(t, fileParser, "are the command's own words after a heredoc, through getParser", func(t *testing.T) {
		eq(t, firstStatement(t, mustGetParser(t), "git <<EOF push --force\nb\nEOF"),
			[]string{"command", "command_name", "heredoc_redirect", "word", "word"})
	})
	tw(t, fileParser, "join a piped statement after a heredoc, through getParser", func(t *testing.T) {
		eq(t, firstStatement(t, mustGetParser(t), "cat <<EOF | rm -rf /tmp/x\nb\nEOF"),
			[]string{"pipeline", "redirected_statement", "command"})
	})
	tw(t, fileParser, "stay in the heredoc through getGrammarParser", func(t *testing.T) {
		eq(t, firstStatement(t, mustGrammarParser(t), "cat <<EOF | rm -rf /tmp/x\nb\nEOF"),
			[]string{"redirected_statement", "command", "heredoc_redirect"})
	})
}

// TestRestOfAHeredocsLine: the rest of a heredoc's line, through getParser, reaches what the same
// line with `< in` in place of `<<EOF` reaches.
func TestRestOfAHeredocsLine(t *testing.T) {
	correctedShape := func(t *testing.T, command, redirect string) string {
		return strings.Replace(shape(parse(t, command)), redirect, "REDIRECT", 1)
	}
	for _, line := range []string{
		"git <<EOF push --force",
		"cat <<EOF ~/x/in",
		"cat <<EOF > /tmp/o",
		"cat <<EOF 2>/dev/null arg",
		"cat <<EOF | rm -rf /tmp/x",
		"cat <<EOF && rm -rf /tmp/x",
		"cat <<EOF > a && rm x",
		"cat <<EOF | a && cd /tmp",
		"x && git <<EOF 2>/dev/null push",
	} {
		t.Run("reaches what the same line with `< in` in place of `<<EOF` reaches: "+line, func(t *testing.T) {
			got := correctedShape(t, line+"\nb\nEOF", `(heredoc_redirect "EOF" "b\n" "EOF")`)
			want := correctedShape(t, strings.Replace(line, "<<EOF", "< in", 1), `(file_redirect "in")`)
			if got != want {
				t.Errorf("got  %s\nwant %s", got, want)
			}
		})
	}
}

func TestWarmParser(t *testing.T) {
	reset := func(t *testing.T) {
		ResetWarmBashParser()
		t.Cleanup(ResetWarmBashParser)
	}
	tw(t, fileParser, "returns null before the parser is warmed", func(t *testing.T) {
		reset(t)
		if GetWarmBashParser() != nil {
			t.Error("a cold parser was handed out")
		}
	})
	tw(t, fileParser, "exposes the parser synchronously after warm-up", func(t *testing.T) {
		reset(t)
		WarmBashParser()
		p := GetWarmBashParser()
		if p == nil {
			t.Fatal("still cold after WarmBashParser")
		}
		if tree := p.Parse("echo hi"); tree == nil || tree.Root.Type() != "program" {
			t.Errorf("tree = %+v", tree)
		}
	})
	tw(t, fileParser, "hands out the same memoized parser as getParser", func(t *testing.T) {
		reset(t)
		WarmBashParser()
		if GetWarmBashParser() != mustGetParser(t) {
			t.Error("the warm parser is not GetParser's")
		}
	})
	tw(t, fileParser, "resetWarmBashParser clears the cached parser", func(t *testing.T) {
		reset(t)
		WarmBashParser()
		if GetWarmBashParser() == nil {
			t.Fatal("not warm")
		}
		ResetWarmBashParser()
		if GetWarmBashParser() != nil {
			t.Error("reset left the parser warm")
		}
	})
}
