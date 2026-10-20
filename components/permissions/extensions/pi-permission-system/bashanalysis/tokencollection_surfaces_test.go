package bashanalysis

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

var bashparseUnproven = bashparse.UnprovenEffect

func contains(t *testing.T, got []string, want string) {
	t.Helper()
	if !slices.Contains(got, want) {
		t.Fatalf("%q not in %q", want, got)
	}
}

func notContains(t *testing.T, got []string, want string) {
	t.Helper()
	if slices.Contains(got, want) {
		t.Fatalf("%q in %q", want, got)
	}
}

func TestCollectRedirectTokens(t *testing.T) {
	tw(t, tk, "collects the destination path from a stdout redirect", func(t *testing.T) {
		eq(t, redirectTokensOf(t, "cat /etc/hosts > /tmp/out.txt"), s("/tmp/out.txt"))
	})
	tw(t, tk, "collects the destination path from an append redirect", func(t *testing.T) {
		eq(t, redirectTokensOf(t, "echo hello >> /tmp/log.txt"), s("/tmp/log.txt"))
	})
	tw(t, tk, "collects the source path from a stdin redirect", func(t *testing.T) {
		eq(t, redirectTokensOf(t, "cat < /etc/hosts"), s("/etc/hosts"))
	})
	tw(t, tk, "collects the operand of a substitution used as the destination", func(t *testing.T) {
		eq(t, redirectTokensOf(t, "echo hi > $(cat /etc/shadow)"), s("/etc/shadow"))
	})
	tw(t, tk, "collects the operand of a process substitution read as input", func(t *testing.T) {
		eq(t, redirectTokensOf(t, "cat < <(cat /etc/shadow)"), s("/etc/shadow"))
	})
	tw(t, tk, "collects both the destination text and a concatenated operand", func(t *testing.T) {
		eq(t, redirectTokensOf(t, "echo hi > /tmp/$(cat /etc/shadow)"), s("/tmp/$(cat /etc/shadow)", "/etc/shadow"))
	})
}

func TestCollectPathCandidateTokens(t *testing.T) {
	tw(t, tk, "collects all argument tokens from a simple command via the program root", func(t *testing.T) {
		eq(t, pathTokensOf(t, "cat /etc/hosts"), s("/etc/hosts"))
	})
	tw(t, tk, "collects redirect destinations as well as command arguments", func(t *testing.T) {
		eq(t, pathTokensOf(t, "cat /etc/hosts > /tmp/out.txt"), s("/etc/hosts", "/tmp/out.txt"))
	})
	tw(t, tk, "returns empty array for heredoc-only content (SKIP_SUBTREE_TYPES)", func(t *testing.T) {
		notContains(t, pathTokensOf(t, "cat <<EOF\nhello\nEOF"), "hello")
	})
	tw(t, tk, "collects the operand of an interpolating heredoc body", func(t *testing.T) {
		eq(t, pathTokensOf(t, "cat <<EOF\n$(cat /etc/shadow)\nEOF"), s("/etc/shadow"))
	})
	for _, c := range [][2]string{
		{"single-quoted", "cat <<'EOF'\n$(cat /etc/shadow)\nEOF"},
		{"double-quoted", "cat <<\"EOF\"\n$(cat /etc/shadow)\nEOF"},
	} {
		t.Run("collects nothing from a "+c[0]+" heredoc body", func(t *testing.T) { eq(t, pathTokensOf(t, c[1]), s()) })
	}
	tw(t, tk, "never collects heredoc prose, even alongside a substitution", func(t *testing.T) {
		eq(t, pathTokensOf(t, "cat <<EOF\n/etc/passwd is prose\n$(cat /etc/shadow)\nEOF"), s("/etc/shadow"))
	})
	tw(t, tk, "collects the operand of a herestring substitution", func(t *testing.T) {
		eq(t, pathTokensOf(t, "cat <<< $(cat /etc/shadow)"), s("/etc/shadow"))
	})
	tw(t, tk, "recurses into command substitution to collect nested tokens", func(t *testing.T) {
		contains(t, pathTokensOf(t, "cat $(echo /etc/hosts)"), "/etc/hosts")
	})
}

func TestStatementOperands(t *testing.T) {
	effects := func(command string) []tokenEffect {
		return tokenEffectsOf(CollectPathCandidateTokens(parseRoot(t, command), unrebound))
	}
	unproven := func(token string) []tokenEffect { return []tokenEffect{{token, bashparseUnproven}} }
	read := func(token string) []tokenEffect { return []tokenEffect{{token, coreRead}} }

	tw(t, tk, "collects the operand a for loop names directly", func(t *testing.T) {
		eq(t, pathTokensOf(t, "for f in /etc/shadow; do cat $f; done"), s("/etc/shadow"))
	})
	tw(t, tk, "collects the operand a select statement names directly", func(t *testing.T) {
		eq(t, pathTokensOf(t, "select f in /etc/shadow; do echo $f; done"), s("/etc/shadow"))
	})
	tw(t, tk, "collects every operand of a multi-entry word list, in source order", func(t *testing.T) {
		eq(t, pathTokensOf(t, "for f in /tmp/a /tmp/b; do echo; done"), s("/tmp/a", "/tmp/b"))
	})
	tw(t, tk, "removes quotes from a quoted operand", func(t *testing.T) {
		eq(t, pathTokensOf(t, `for f in "/etc/shadow"; do echo; done`), s("/etc/shadow"))
	})
	tw(t, tk, "leaves the loop variable uncollected", func(t *testing.T) {
		eq(t, pathTokensOf(t, "for f in /etc/shadow; do echo; done"), s("/etc/shadow"))
	})
	tw(t, tk, "still collects the loop body's own operands", func(t *testing.T) {
		eq(t, pathTokensOf(t, "for f in a; do cat /etc/shadow; done"), s("a", "/etc/shadow"))
	})
	tw(t, tk, "collects nothing new from a word-list-less for loop", func(t *testing.T) {
		eq(t, pathTokensOf(t, "for f; do cat /etc/shadow; done"), s("/etc/shadow"))
	})
	tw(t, tk, "descends a substitution in the word list rather than reading its text", func(t *testing.T) {
		eq(t, pathTokensOf(t, "for f in $(cat /etc/shadow); do echo $f; done"), s("/etc/shadow"))
	})
	tw(t, tk, "attributes an operand no command owns as unproven", func(t *testing.T) {
		eq(t, effects("for f in /etc/shadow; do echo; done"), unproven("/etc/shadow"))
	})
	tw(t, tk, "leaves a nested command's operand with its own attribution", func(t *testing.T) {
		eq(t, effects("for f in $(cat /etc/shadow); do echo; done"), read("/etc/shadow"))
	})
	tw(t, tk, "collects the subject a case statement names directly", func(t *testing.T) {
		eq(t, pathTokensOf(t, "case /etc/shadow in a) true;; esac"), s("/etc/shadow"))
	})
	tw(t, tk, "removes quotes from a quoted subject", func(t *testing.T) {
		eq(t, pathTokensOf(t, `case "/etc/shadow" in a) true;; esac`), s("/etc/shadow"))
	})
	tw(t, tk, "leaves an arm's pattern uncollected", func(t *testing.T) {
		eq(t, pathTokensOf(t, "case $x in /etc/passwd) true;; esac"), s())
	})
	tw(t, tk, "still collects an arm's own command operands", func(t *testing.T) {
		eq(t, pathTokensOf(t, "case $x in a) cat /etc/shadow;; esac"), s("/etc/shadow"))
	})
	tw(t, tk, "attributes a subject no command owns as unproven", func(t *testing.T) {
		eq(t, effects("case /etc/shadow in a) true;; esac"), unproven("/etc/shadow"))
	})
	tw(t, tk, "descends a substitution in the subject rather than reading its text", func(t *testing.T) {
		eq(t, effects("case $(cat /etc/shadow) in a) true;; esac"), read("/etc/shadow"))
	})
}

func TestEmbeddedOptionValueExtraction(t *testing.T) {
	tw(t, tk, "emits the value of a long option carrying an inline path", func(t *testing.T) {
		contains(t, tokensOf(t, "grep --file=/tmp/patterns target"), "/tmp/patterns")
	})
	tw(t, tk, "emits the embedded value for a non-pattern-first command too", func(t *testing.T) {
		contains(t, tokensOf(t, "tar --directory=/etc -xf a.tar"), "/etc")
	})
	tw(t, tk, "preserves the original flag token", func(t *testing.T) {
		contains(t, tokensOf(t, "cat --file=/tmp/x"), "--file=/tmp/x")
	})
	tw(t, tk, "emits a bare value, leaving it for the shape gates to drop", func(t *testing.T) {
		contains(t, tokensOf(t, "cat --format=json"), "json")
	})
	tw(t, tk, "splits the single-dash form", func(t *testing.T) { contains(t, tokensOf(t, "cat -o=/tmp/out"), "/tmp/out") })
	tw(t, tk, "does not split a flag with no value", func(t *testing.T) {
		tokens := tokensOf(t, "grep --recursive target")
		notContains(t, tokens, "")
		notContains(t, tokens, "--recursive")
	})
}
