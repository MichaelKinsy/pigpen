package bashparse

import (
	"reflect"
	"testing"
)

// Twins of test/access-intent/bash/heredoc-free-lines.test.ts.

const fileHeredocFreeLines = "access-intent/bash/heredoc-free-lines"

// heredocFreeLinesOf is the heredoc-free lines of a real parse of command.
func heredocFreeLinesOf(t *testing.T, command string) []string {
	t.Helper()
	return HeredocFreeLinesWithin(parse(t, command))
}

func expectLines(t *testing.T, command string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if got := heredocFreeLinesOf(t, command); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHeredocFreeLinesWithin(t *testing.T) {
	t.Run("a heredoc tail the grammar cannot parse spells the line without its heredoc", func(t *testing.T) {
		for _, c := range []struct{ label, command, line string }{
			{"a `;` command", "cat <<EOF ; rm -rf x\nb\nEOF", "cat ; rm -rf x"},
			{"an `&` command", "cat <<EOF & rm -rf x\nb\nEOF", "cat & rm -rf x"},
			{"words and a redirect", "cat <<EOF arg > /tmp/o\nb\nEOF", "cat arg > /tmp/o"},
			{"a redirect then a pipe", "git commit -F - <<'MSG' 2>&1 | rm -rf x\nb\nMSG", "git commit -F - 2>&1 | rm -rf x"},
		} {
			t.Run(c.label, func(t *testing.T) { expectLines(t, c.command, c.line) })
		}
	})

	t.Run("the operator it cuts", func(t *testing.T) {
		for _, c := range []struct{ label, command string }{
			{"a descriptor", "cat 2<<EOF ; rm -rf x\nb\nEOF"},
			{"a tab-stripping heredoc", "cat <<-EOF ; rm -rf x\nb\nEOF"},
			{"a quoted delimiter", "cat <<\"EOF\" ; rm -rf x\nb\nEOF"},
		} {
			t.Run("cuts "+c.label+" whole", func(t *testing.T) { expectLines(t, c.command, "cat ; rm -rf x") })
		}
		tw(t, fileHeredocFreeLines, "cuts a descriptor the grammar lexed into the delimiter", func(t *testing.T) {
			// `0<<EOF` lands as one heredoc_start inside a top-level ERROR, so no statement holds it
			// and the line is the span.
			expectLines(t, "cat 0<<EOF | rm -rf x\nb\nEOF", "cat | rm -rf x")
		})
	})

	t.Run("the span it spells", func(t *testing.T) {
		tw(t, fileHeredocFreeLines, "starts at the heredoc's statement, which may hold a list", func(t *testing.T) {
			expectLines(t, "echo hi && cat <<EOF ; rm -rf x\nb\nEOF", "echo hi && cat ; rm -rf x")
		})
		tw(t, fileHeredocFreeLines, "starts at the statement rather than the line inside a compound", func(t *testing.T) {
			// The line alone re-parses as an unterminated `if`.
			expectLines(t, "if true; then cat <<EOF ; rm x\nb\nEOF\nfi", "cat ; rm x")
		})
		tw(t, fileHeredocFreeLines, "spells each unresolved heredoc's line, in source order", func(t *testing.T) {
			expectLines(t, "cat <<A ; rm x\na\nA\ncat <<B & rm y\nb\nB", "cat ; rm x", "cat & rm y")
		})
		tw(t, fileHeredocFreeLines, "leaves an unterminated heredoc's body out", func(t *testing.T) {
			expectLines(t, "cat <<'EOF'\nsee `rm -rf x` here", "cat")
		})
	})

	t.Run("a heredoc whose statement parsed spells nothing", func(t *testing.T) {
		for _, c := range []struct{ label, command string }{
			{"a clean tail", "cat <<EOF | rm -rf x\nb\nEOF"},
			{"a failure in another statement", "cat <<EOF\nb\nEOF\necho 'x"},
			{"no heredoc at all", "echo hi > out.txt <> rw.txt"},
		} {
			t.Run(c.label, func(t *testing.T) { expectLines(t, c.command) })
		}
	})
}
