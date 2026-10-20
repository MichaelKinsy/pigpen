package bashparse

import (
	"strings"
	"testing"
)

// Twins of test/access-intent/bash/heredoc-tails.test.ts.

const fileHeredocTails = "access-intent/bash/heredoc-tails"

func withHoisted[T any](t *testing.T, command string, read func(hoisted, grammar *Node) T) T {
	t.Helper()
	return withCorrected(t, command, HoistHeredocTails, read)
}

func hoistedShape(t *testing.T, command string) string {
	return withHoisted(t, command, func(hoisted, _ *Node) string { return shape(hoisted) })
}

// heredoc is a heredoc line closed by a one-line body and its delimiter.
func heredoc(line string) string { return line + "\nb\nEOF" }

func TestHoistHeredocTails(t *testing.T) {
	tw(t, fileHeredocTails, "becomes a sibling after the heredoc", func(t *testing.T) {
		want := `(program (redirected_statement (command (command_name "cat")) (heredoc_redirect "EOF" "b\n" "EOF") (file_redirect "/tmp/o")))`
		if got := hoistedShape(t, heredoc("cat <<EOF > /tmp/o")); got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	})
	tw(t, fileHeredocTails, "hoists a herestring the same way", func(t *testing.T) {
		// No `< in` oracle here: the grammar misparses `cat < in <<< x` itself, reading the `<<<`
		// as `<<` and a redirect.
		want := `(program (redirected_statement (command (command_name "cat")) (heredoc_redirect "EOF" "b\n" "EOF") (herestring_redirect "x")))`
		if got := hoistedShape(t, heredoc("cat <<EOF <<< x")); got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	})
	tw(t, fileHeredocTails, "joins a piped command to the redirected statement", func(t *testing.T) {
		want := `(program (pipeline (redirected_statement (command (command_name "cat")) (heredoc_redirect "EOF" "b\n" "EOF")) (command (command_name "rm") "-rf" "/tmp/x")))`
		if got := hoistedShape(t, heredoc("cat <<EOF | rm -rf /tmp/x")); got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	})
	tw(t, fileHeredocTails, "joins a `&&` command as a list", func(t *testing.T) {
		want := `(program (list (redirected_statement (command (command_name "cat")) (heredoc_redirect "EOF" "b\n" "EOF")) (command (command_name "rm") "-rf" "/tmp/x")))`
		if got := hoistedShape(t, heredoc("cat <<EOF && rm -rf /tmp/x")); got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	})

	// The shape the grammar gives the line with `< in` in its place.
	t.Run("the shape the grammar gives the line with `< in` in its place", func(t *testing.T) {
		for _, c := range []struct{ label, line string }{
			{"a redirect", "cat <<EOF > /tmp/o"},
			{"two redirects", "cat <<EOF > a 2> b"},
			{"a redirect carrying a word", "cat <<EOF 2>/dev/null arg"},
			{"a redirect, then a statement", "cat <<EOF > a && rm x"},
			{"a pipe", "cat <<EOF | rm -rf /tmp/x"},
			{"a pipe with stderr", "cat <<EOF |& rm x"},
			{"an `&&`", "cat <<EOF && rm x"},
			{"an `||`", "cat <<EOF || rm x"},
			{"a pipe into a list", "cat <<EOF | a && b"},
			{"an `&&` into a list", "cat <<EOF && a || b"},
			{"a pipe into a pipeline", "cat <<EOF | a | b"},
			{"a pipe into a redirected list", "cat <<EOF | a && b > o"},
			{"an `&&` into a redirected command", "cat <<EOF && a > o"},
			{"an `&&` into a pipeline", "cat <<EOF && b | c"},
			{"an `||` into a pipeline", "cat <<EOF || b | c"},
			{"an `&&` into a pipeline whose first stage redirects", "cat <<EOF && a > o | b"},
			{"a pipe into a pipeline whose first stage redirects", "cat <<EOF | a > o | b"},
			{"an `&&` into a list whose last element is such a pipeline", "cat <<EOF && x && a > o | b"},
			{"an `&&` into a list whose first element is such a pipeline", "cat <<EOF && a > o | b && c"},
			{"an `&&` into a pipeline ending in a redirect", "cat <<EOF && a | b | c > o"},
			{"a pipe into a pipeline ending in a redirect", "cat <<EOF | a | b | c > o"},
			{"an `&&` into a pipeline ending in a subshell's redirect", "cat <<EOF && a | b | (c) > o"},
			{"an `&&` into a pipeline ending in a redirect carrying a word", "cat <<EOF && a | b | c 2>/dev/null y"},
			{"a list body", "x && cat <<EOF | rm y"},
			{"a compound body", "{ cat; } <<EOF | rm x"},
			{"a list body with a redirect", "cd a && cat <<EOF > /tmp/x"},
		} {
			t.Run("is reproduced for "+c.label, func(t *testing.T) {
				hoisted := strings.Replace(hoistedShape(t, heredoc(c.line)), `(heredoc_redirect "EOF" "b\n" "EOF")`, "REDIRECT", 1)
				oracle := strings.Replace(shape(grammarParse(t, strings.Replace(c.line, "<<EOF", "< in", 1))), `(file_redirect "in")`, "REDIRECT", 1)
				if hoisted != oracle {
					t.Errorf("hoisted %s\noracle  %s", hoisted, oracle)
				}
			})
		}
	})

	// Where the grammar groups the `< in` spelling unlike bash: each keeps bash's grouping, or
	// charges a write to more units than the `< in` spelling does, so none is looser than it.
	tw(t, fileHeredocTails, "keeps a pipe tail's `&&` outside the pipeline", func(t *testing.T) {
		// The grammar parses `cat < in | a | b && c` as `cat < in | (a | b && c)`.
		want := `(program (list (pipeline (redirected_statement (command (command_name "cat")) (heredoc_redirect "EOF" "b\n" "EOF")) (command (command_name "a")) (command (command_name "b"))) (command (command_name "c"))))`
		if got := hoistedShape(t, heredoc("cat <<EOF | a | b && c")); got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	})
	tw(t, fileHeredocTails, "charges a mid-pipeline redirect to the heredoc's stage too", func(t *testing.T) {
		// The grammar charges `> o` to `a | b` alone in `{ cat; } < in | a | b > o | c`.
		want := `(program (pipeline (redirected_statement (pipeline (redirected_statement (compound_statement (command (command_name "cat"))) (heredoc_redirect "EOF" "b\n" "EOF")) (command (command_name "a")) (command (command_name "b"))) (file_redirect "o")) (command (command_name "c"))))`
		if got := hoistedShape(t, heredoc("{ cat; } <<EOF | a | b > o | c")); got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	})

	tw(t, fileHeredocTails, "is hoisted inside a command substitution", func(t *testing.T) {
		want := `(program (command (command_name "echo") (command_substitution (pipeline (redirected_statement (command (command_name "cat")) (heredoc_redirect "EOF" "b\n" "EOF")) (command (command_name "sh"))))))`
		if got := hoistedShape(t, "echo $(cat <<EOF | sh\nb\nEOF\n)"); got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	})

	t.Run("a tree with nothing to hoist returns the grammar's own root", func(t *testing.T) {
		for _, c := range []struct{ label, command string }{
			{"a heredoc whose line ends at the delimiter", heredoc("cat <<EOF")},
			{"a heredoc that absorbed its command's operand", "git commit -F - <<'EOF'\nfeat: x\nEOF"},
			{"words, which the reattachment pass moves", heredoc("git <<EOF push")},
			{"a statement whose parse failed", heredoc("cat <<EOF > /tmp/o | wc")},
			{"a redirect and a pipe the grammar cannot combine", "cat <<'MSG' 2>&1 | tail -4\nmsg\nMSG"},
		} {
			t.Run(c.label, func(t *testing.T) {
				withHoisted(t, c.command, func(hoisted, grammar *Node) struct{} {
					if hoisted != grammar {
						t.Error("the hoisted root is not the grammar's own")
					}
					return struct{}{}
				})
			})
		}
	})

	t.Run("the hoisted tree keeps the view contract", func(t *testing.T) {
		for _, command := range []string{
			heredoc("cat <<EOF > /tmp/o"),
			heredoc("cat <<EOF > a && rm x"),
			heredoc("cat <<EOF | rm -rf /tmp/x"),
			heredoc("cat <<EOF | a && b > o"),
			heredoc("cat <<EOF && b | c"),
			heredoc("cat <<EOF && a > o | b"),
			heredoc("cat <<EOF && x && a | b | c > o"),
			heredoc("x && cat <<EOF | rm y"),
			heredoc("cat <<EOF 2>/dev/null arg"),
			"echo é $(cat <<EOF | sh\nb\nEOF\n)",
		} {
			t.Run(command, func(t *testing.T) {
				withHoisted(t, command, func(hoisted, grammar *Node) struct{} {
					if v := viewContractViolations(command, hoisted, grammar); len(v) != 0 {
						t.Errorf("violations: %q", v)
					}
					return struct{}{}
				})
			})
		}
	})
}
