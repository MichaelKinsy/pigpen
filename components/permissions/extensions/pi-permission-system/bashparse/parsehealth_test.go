package bashparse

import (
	"reflect"
	"testing"
)

// Twins of test/access-intent/bash/parse-health.test.ts.

const fileParseHealth = "access-intent/bash/parse-health"

// unresolvedRedirects is ParseUnresolvedAt applied to each file_redirect a command carries. The
// fixtures are parsed rather than fabricated: the whole point is where error recovery puts the text
// it could not attach.
func unresolvedRedirects(t *testing.T, command string) []bool {
	t.Helper()
	out := []bool{}
	for _, n := range findNodes(parse(t, command), "file_redirect") {
		out = append(out, ParseUnresolvedAt(n))
	}
	return out
}

// unresolvedWithin is ParseUnresolvedWithin applied to each node of typ a command produces.
func unresolvedWithin(t *testing.T, command, typ string) []bool {
	t.Helper()
	out := []bool{}
	for _, n := range findNodes(parse(t, command), typ) {
		out = append(out, ParseUnresolvedWithin(n))
	}
	return out
}

func eqBools(t *testing.T, got, want []bool) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func containsTrue(bs []bool) bool {
	for _, b := range bs {
		if b {
			return true
		}
	}
	return false
}

func TestParseUnresolvedAt(t *testing.T) {
	tw(t, fileParseHealth, "answers true when the discarded text is inside the node", func(t *testing.T) {
		// `<>` has no node in the grammar; here the `>` half is recovered as an ERROR child of the
		// redirect itself.
		eqBools(t, unresolvedRedirects(t, "cat <> rw.txt"), []bool{true})
	})
	tw(t, fileParseHealth, "answers true when the discarded text is the preceding sibling", func(t *testing.T) {
		// Same command, tilde destination: the `<` half is recovered as an ERROR before an
		// otherwise clean `file_redirect "> ~/rw.txt"`, so nothing inside that node distinguishes it
		// from a genuine write.
		eqBools(t, unresolvedRedirects(t, "cat <> ~/rw.txt"), []bool{true})
	})
	t.Run("answers true for", func(t *testing.T) {
		for _, command := range []string{"cat 3<> rw.txt", "cat 0<> ~/y", "cat <>&1"} {
			t.Run(command, func(t *testing.T) {
				if !containsTrue(unresolvedRedirects(t, command)) {
					t.Error("no unresolved redirect")
				}
			})
		}
		// The question is about the parse, not about `<>`: a redirect preceded by an unrelated
		// recovery failure loses its proof too, the accepted cost. Over-refusing costs a prompt;
		// under-refusing hands a write to a read grant.
		for _, command := range []string{"cat $(( > out.txt", "echo ) > out.txt"} {
			t.Run(command+", whose redirect is itself well-formed", func(t *testing.T) {
				eqBools(t, unresolvedRedirects(t, command), []bool{true})
			})
		}
	})
	t.Run("answers false for a node tree-sitter resolved", func(t *testing.T) {
		for _, command := range []string{
			"cat a > out.txt", "cat < in.txt", "pnpm x 2>&1", "pnpm x >& out.txt", "cat a > $(mktemp)", "foo; cat a > out.txt",
		} {
			t.Run(command, func(t *testing.T) { eqBools(t, unresolvedRedirects(t, command), []bool{false}) })
		}
	})
	tw(t, fileParseHealth, "answers per redirect rather than per statement", func(t *testing.T) {
		// Three sibling file_redirect nodes: the genuine `> out.txt`, the stranded `<`, and the
		// `> ~/rw.txt` the `<` was meant to pair with. A statement-wide rule would condemn the
		// first, which really is a write.
		eqBools(t, unresolvedRedirects(t, "cat a > out.txt <> ~/rw.txt"), []bool{false, true, true})
	})
	tw(t, fileParseHealth, "leaves a resolvable redirect after an unresolvable one proven", func(t *testing.T) {
		eqBools(t, unresolvedRedirects(t, "cat <> ~/a.txt > b.txt"), []bool{true, false})
	})
}

func TestParseUnresolvedWithin(t *testing.T) {
	tw(t, fileParseHealth, "answers true when the discarded text is inside the node", func(t *testing.T) {
		eqBools(t, unresolvedWithin(t, "cat <> rw.txt", "file_redirect"), []bool{true})
	})
	tw(t, fileParseHealth, "answers true for a statement whose failure is several levels down", func(t *testing.T) {
		// The ERROR sits under `heredoc_redirect -> file_redirect`; the statement is what a walker
		// descending statements can see it through.
		eqBools(t, unresolvedWithin(t, "git commit -F - <<'MSG' 2>&1 | tail -4\nmsg\nMSG", "redirected_statement"), []bool{true})
	})
	tw(t, fileParseHealth, "answers false where parseUnresolvedAt answers true", func(t *testing.T) {
		// The one case that separates the two predicates. A redirect must refuse on a failed
		// predecessor, because error recovery strands the discarded operator ahead of the node it
		// belonged to, but a statement whose predecessor failed is not itself unparsed.
		eqBools(t, unresolvedWithin(t, "cat <> ~/rw.txt", "file_redirect"), []bool{false})
		eqBools(t, unresolvedRedirects(t, "cat <> ~/rw.txt"), []bool{true})
	})
	t.Run("answers false at every level of a fully resolved parse", func(t *testing.T) {
		for _, command := range []string{"cat a > out.txt", "cd /repo && git push", "echo hi | tail -2"} {
			t.Run(command, func(t *testing.T) {
				eqBools(t, unresolvedWithin(t, command, "program"), []bool{false})
				if containsTrue(unresolvedWithin(t, command, "command")) {
					t.Error("a command reports unresolved")
				}
			})
		}
	})
	tw(t, fileParseHealth, "answers true for the program node however local the failure", func(t *testing.T) {
		// Why a walker cannot ask this of `program`, `list`, or `pipeline`: the answer is true
		// whenever anything anywhere failed, so marking there would mark every unit of the command.
		eqBools(t, unresolvedWithin(t, "echo hi > out.txt <> rw.txt; rm -rf /tmp/y", "program"), []bool{true})
	})
}
