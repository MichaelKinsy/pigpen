package bashparse

import "testing"

// Twins of test/access-intent/bash/redirect-arguments.test.ts.

const fileRedirectArguments = "access-intent/bash/redirect-arguments"

func correctedShape(t *testing.T, command string) string {
	return withCorrected(t, command, ReattachRedirectArguments, func(corrected, _ *Node) string { return shape(corrected) })
}

func expectShape(t *testing.T, command, want string) {
	t.Helper()
	if got := correctedShape(t, command); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestReattachRedirectArguments(t *testing.T) {
	tw(t, fileRedirectArguments, "hands the words back to a command body", func(t *testing.T) {
		// Nothing is left at the statement, so the corrected command takes its place, exactly as the
		// grammar parses `2>/dev/null git push --force`.
		expectShape(t, "git 2>/dev/null push --force",
			`(program (command (command_name "git") (file_redirect "2" "/dev/null") "push" "--force"))`)
	})
	tw(t, fileRedirectArguments, "keeps a redirect after the last word at the statement", func(t *testing.T) {
		expectShape(t, "cmd a > out b 2>&1",
			`(program (redirected_statement (command (command_name "cmd") "a" (file_redirect "out") "b") (file_redirect "2" "1")))`)
	})
	tw(t, fileRedirectArguments, "moves every redirect up to the last word, in source order", func(t *testing.T) {
		// `<in` carries no words of its own, but it sits before `arg1`, so it moves with the rest
		// and the command's children stay in source order.
		expectShape(t, "cmd <in 2>err arg1 >out arg2",
			`(program (command (command_name "cmd") (file_redirect "in") (file_redirect "2" "err") "arg1" (file_redirect "out") "arg2"))`)
	})
	tw(t, fileRedirectArguments, "hands the words to a close operator's command, since it has no target", func(t *testing.T) {
		expectShape(t, "cmd >&- arg", `(program (command (command_name "cmd") ">&-" "arg"))`)
	})
	tw(t, fileRedirectArguments, "hands the words to the last command of a list the grammar grouped", func(t *testing.T) {
		// The grammar hangs the redirect off the whole `&&` list; bash gives it and its words to `git`.
		expectShape(t, "cd a && b && git 2>/dev/null push",
			`(program (list (list (command (command_name "cd") "a") (command (command_name "b"))) (command (command_name "git") (file_redirect "2" "/dev/null") "push")))`)
	})
	tw(t, fileRedirectArguments, "hands the words to the last stage of a pipeline the grammar grouped", func(t *testing.T) {
		// A redirect on the last stage hangs off the whole pipeline in the grammar; bash gives it
		// and its words to that stage.
		expectShape(t, "rg -l x | xargs ls 2>&1 ~/x",
			`(program (pipeline (command (command_name "rg") "-l" "x") (command (command_name "xargs") "ls" (file_redirect "2" "1") "~/x")))`)
	})

	// A heredoc the grammar hung the command's words on.
	tw(t, fileRedirectArguments, "hands the words back to the command, after the heredoc", func(t *testing.T) {
		// The body stays in the heredoc: it is written after the words, and its substitutions
		// still run.
		expectShape(t, "git <<EOF push --force\nb\nEOF",
			`(program (command (command_name "git") (heredoc_redirect "EOF" "b\n" "EOF") "push" "--force"))`)
	})
	tw(t, fileRedirectArguments, "hands the words to the last command of a list the grammar grouped", func(t *testing.T) {
		expectShape(t, "x && git <<EOF push\nb\nEOF",
			`(program (list (command (command_name "x")) (command (command_name "git") (heredoc_redirect "EOF" "b\n" "EOF") "push")))`)
	})
	tw(t, fileRedirectArguments, "hands back a substitution and a string as words too", func(t *testing.T) {
		expectShape(t, "cat <<EOF $(rm x) \"q s\"\nb\nEOF",
			`(program (command (command_name "cat") (heredoc_redirect "EOF" "b\n" "EOF") (command_substitution (command (command_name "rm") "x")) (string "q s")))`)
	})

	t.Run("a statement nested in another node is corrected", func(t *testing.T) {
		for _, c := range []struct{ label, command, want string }{
			{"a pipeline", "git 2>/dev/null push --force | tail",
				`(program (pipeline (command (command_name "git") (file_redirect "2" "/dev/null") "push" "--force") (command (command_name "tail"))))`},
			{"a command substitution", "x=$(git 2>/dev/null push --force)",
				`(program (variable_assignment "x" (command_substitution (command (command_name "git") (file_redirect "2" "/dev/null") "push" "--force"))))`},
			{"a word moved out of another redirect", "echo 2>/dev/null $(git 2>/dev/null push)",
				`(program (command (command_name "echo") (file_redirect "2" "/dev/null") (command_substitution (command (command_name "git") (file_redirect "2" "/dev/null") "push"))))`},
		} {
			t.Run("is corrected inside "+c.label, func(t *testing.T) { expectShape(t, c.command, c.want) })
		}
	})

	t.Run("a tree with nothing to correct returns the grammar's own root", func(t *testing.T) {
		for _, c := range []struct{ label, command string }{
			{"no redirect", "git push --force"},
			{"a redirect after the last word", "git push --force 2>/dev/null"},
			{"a redirect before the command", "2>/dev/null git push --force"},
			{"a statement whose parse failed", "cat <> rw.txt extra"},
			{"a compound body bash rejects", "{ a; } 2>/dev/null b"},
			{"a heredoc whose line ends at the delimiter", "cat <<EOF\nb\nEOF"},
			{"a heredoc on a compound body bash rejects", "{ echo a; } <<EOF b\nb\nEOF"},
		} {
			t.Run(c.label, func(t *testing.T) {
				withCorrected(t, c.command, ReattachRedirectArguments, func(corrected, grammar *Node) struct{} {
					if corrected != grammar {
						t.Error("the corrected root is not the grammar's own")
					}
					return struct{}{}
				})
			})
		}
	})

	t.Run("the corrected tree keeps the view contract", func(t *testing.T) {
		for _, command := range []string{
			"git 2>/dev/null push --force",
			"cmd a > out b 2>&1",
			"cmd <in 2>err arg1 >out arg2",
			"cmd >&- arg",
			"cd a && b && git 2>/dev/null push",
			"echo é 2>/dev/null $(git 2>/dev/null push) | tail",
			"git <<EOF push --force\nb\nEOF",
			"x && git <<EOF push\nb\nEOF",
			"cat <<EOF $(rm x) \"q s\"\nb\nEOF",
		} {
			t.Run(command, func(t *testing.T) {
				withCorrected(t, command, ReattachRedirectArguments, func(corrected, grammar *Node) struct{} {
					if v := viewContractViolations(command, corrected, grammar); len(v) != 0 {
						t.Errorf("violations: %q", v)
					}
					return struct{}{}
				})
			})
		}
	})
}
