package bashparse

import "testing"

// Twins of test/access-intent/bash/redirect-analysis.test.ts.

const fileRedirectAnalysis = "access-intent/bash/redirect-analysis"

// withRedirects parses a bash snippet with the grammar's own parser (this module reads a redirect
// node as tree-sitter-bash produced it, words after its target included) and returns every
// redirect node of typ it holds. A command can carry several, and an unresolvable one does not
// contaminate a resolvable neighbour.
func withRedirects(t *testing.T, command, typ string) []*Node {
	t.Helper()
	return findNodes(grammarParse(t, command), typ)
}

func withRedirect(t *testing.T, command, typ string) *Node {
	t.Helper()
	redirects := withRedirects(t, command, typ)
	if len(redirects) == 0 {
		t.Fatalf("no %s node found in: %s", typ, command)
	}
	return redirects[0]
}

// effectForFirstDestination is the effect proved for a redirect's first argument-shaped
// destination (nil when it names no file).
func effectForFirstDestination(redirect *Node) *TokenEffect {
	for _, c := range redirect.children {
		if ArgNodeTypes[c.typ] {
			if effect, ok := RedirectEffectForDestination(redirect, c); ok {
				return &effect
			}
			return nil
		}
	}
	return nil
}

func destinationEffect(t *testing.T, command string, typ ...string) *TokenEffect {
	t.Helper()
	kind := "file_redirect"
	if len(typ) > 0 {
		kind = typ[0]
	}
	return effectForFirstDestination(withRedirect(t, command, kind))
}

func expectEffect(t *testing.T, got *TokenEffect, want TokenEffect) {
	t.Helper()
	if got == nil || *got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

var (
	writeSyntax = TokenEffect{Effect: "write", Source: "syntax"}
	readSyntax  = TokenEffect{Effect: "read", Source: "syntax"}
	unproven    = TokenEffect{Effect: "unproven", Source: "unproven"}
)

func mayWrite(t *testing.T, command string) bool {
	t.Helper()
	return RedirectMayWriteFile(withRedirect(t, command, "file_redirect"))
}

func TestRedirectEffectForDestination(t *testing.T) {
	t.Run("output operators prove a write", func(t *testing.T) {
		for _, c := range []struct{ command, op string }{
			{"cat a > out.txt", ">"}, {"cat a >> out.txt", ">>"}, {"cat a >| out.txt", ">|"},
			{"cat a &> out.txt", "&>"}, {"cat a &>> out.txt", "&>>"},
		} {
			t.Run(c.command+" ("+c.op+")", func(t *testing.T) { expectEffect(t, destinationEffect(t, c.command), writeSyntax) })
		}
	})

	tw(t, fileRedirectAnalysis, "proves a read for a stdin redirect", func(t *testing.T) {
		expectEffect(t, destinationEffect(t, "cat < in.txt"), readSyntax)
	})
	tw(t, fileRedirectAnalysis, "proves a read for a herestring", func(t *testing.T) {
		expectEffect(t, destinationEffect(t, "cat <<< in.txt", "herestring_redirect"), readSyntax)
	})

	tw(t, fileRedirectAnalysis, "names no file at all when the destination is a descriptor", func(t *testing.T) {
		if got := destinationEffect(t, "pnpm x 2>&1"); got != nil {
			t.Errorf("got %v", got)
		}
	})
	tw(t, fileRedirectAnalysis, "proves a write when >& names a real file", func(t *testing.T) {
		expectEffect(t, destinationEffect(t, "pnpm x >& out.txt"), writeSyntax)
	})
	tw(t, fileRedirectAnalysis, "proves a read when <& names a real file", func(t *testing.T) {
		expectEffect(t, destinationEffect(t, "pnpm x <& in.txt"), readSyntax)
	})

	// tree-sitter-bash has no node for the read-write open `<>`; it degrades to an ERROR whose
	// placement depends on the destination's shape, so the operator that survives is a function of
	// the filename. Reading a proof off that operator produced two different answers for one
	// command, one of them a bare read for a destination the shell may truncate.
	t.Run("a redirect the parser could not resolve proves nothing", func(t *testing.T) {
		for _, command := range []string{"cat <> rw.txt", "cat <> ~/rw.txt", "cat 3<> rw.txt", "cat 0<> ~/y"} {
			t.Run(command, func(t *testing.T) { expectEffect(t, destinationEffect(t, command), unproven) })
		}
	})
	tw(t, fileRedirectAnalysis, "proves the same effect however the destination is spelled", func(t *testing.T) {
		bare, tilde := destinationEffect(t, "cat <> rw.txt"), destinationEffect(t, "cat <> ~/rw.txt")
		if bare == nil || tilde == nil || *bare != *tilde {
			t.Errorf("bare %v, tilde %v", bare, tilde)
		}
	})
	tw(t, fileRedirectAnalysis, "still names no file at all when the destination is a descriptor", func(t *testing.T) {
		// Failing to resolve the operator is a reason to withhold a proof, not a reason to invent a
		// path: a descriptor names no file whatever the syntax around it did. Answering `unproven`
		// here would emit the bare `1` of `<>&1` as a path candidate.
		redirect := withRedirect(t, "cat <>&1", "file_redirect")
		for _, c := range redirect.children {
			if descriptorNodeTypes[c.typ] {
				if got, ok := RedirectEffectForDestination(redirect, c); ok {
					t.Errorf("got %v", got)
				}
				return
			}
		}
		t.Fatal("no descriptor destination in: cat <>&1")
	})

	// An unresolvable redirect must not contaminate a resolvable neighbour in either direction.
	allEffects := func(t *testing.T, command string) []*TokenEffect {
		out := []*TokenEffect{}
		for _, r := range withRedirects(t, command, "file_redirect") {
			out = append(out, effectForFirstDestination(r))
		}
		return out
	}
	expectEffects := func(t *testing.T, got []*TokenEffect, want ...TokenEffect) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %d effects, want %d", len(got), len(want))
		}
		for i := range want {
			expectEffect(t, got[i], want[i])
		}
	}
	tw(t, fileRedirectAnalysis, "keeps the proof of a resolvable redirect before it", func(t *testing.T) {
		// Three sibling file_redirect nodes: the genuine `> out.txt`, the stranded `<` (recovered
		// with a zero-width destination of its own), and the `> ~/rw.txt` it was meant to pair with.
		expectEffects(t, allEffects(t, "cat a > out.txt <> ~/rw.txt"), writeSyntax, unproven, unproven)
	})
	tw(t, fileRedirectAnalysis, "keeps the proof of a resolvable redirect after it", func(t *testing.T) {
		expectEffects(t, allEffects(t, "cat <> ~/a.txt > b.txt"), unproven, writeSyntax)
	})
}

func TestRedirectMayWriteFile(t *testing.T) {
	t.Run("a redirect that writes", func(t *testing.T) {
		for _, command := range []string{"cat a > out.txt", "cat a >> out.txt", "cat a &> out.txt", "pnpm x 2> err.log", "pnpm x >& out.txt"} {
			t.Run("answers true for "+command, func(t *testing.T) {
				if !mayWrite(t, command) {
					t.Error("answered false")
				}
			})
		}
	})
	tw(t, fileRedirectAnalysis, "does not read a file descriptor source as a destination", func(t *testing.T) {
		// `2` is the redirected descriptor and `err.log` the destination; a reader that stopped at
		// the first named child would clear this wrongly.
		if !mayWrite(t, "pnpm x 2> err.log") {
			t.Error("answered false")
		}
	})
	t.Run("a redirect that provably only reads", func(t *testing.T) {
		for _, command := range []string{"cat < in.txt", "pnpm x 2>&1", "pnpm x <& in.txt"} {
			t.Run("answers false for "+command, func(t *testing.T) {
				if mayWrite(t, command) {
					t.Error("answered true")
				}
			})
		}
	})
	// The answer is a refusal, not the negation of a write proof. These name a file chosen at run
	// time, so the parse says nothing about which, and the caller is deciding whether to remove a
	// guard.
	t.Run("a destination the parse cannot resolve", func(t *testing.T) {
		for _, c := range []struct{ command, label string }{
			{"cat a > $OUT", "an unquoted variable"},
			{"cat a >${OUT}", "a brace expansion"},
			{"cat a > $(mktemp)", "a command substitution"},
			{"cat a > ${DIR}/log", "an expansion concatenated with a literal"},
			{"cat <> rw.txt", "a read-write open the grammar could not parse"},
			{"cat <> ~/rw.txt", "a read-write open whose halves the parse split"},
			{"cat <>&1", "a read-write open naming no argument-shaped destination"},
		} {
			t.Run("answers true for "+c.command+" ("+c.label+")", func(t *testing.T) {
				if !mayWrite(t, c.command) {
					t.Error("answered false")
				}
			})
		}
	})
}

func TestRedirectTargetIndex(t *testing.T) {
	// targetText is the text of the child RedirectTargetIndex names, or ok=false.
	targetText := func(t *testing.T, command string) (string, bool) {
		redirect := withRedirect(t, command, "file_redirect")
		index, ok := RedirectTargetIndex(redirect)
		if !ok {
			return "", false
		}
		return redirect.Child(index).Text(), true
	}
	expectTarget := func(t *testing.T, command, want string) {
		t.Helper()
		if got, ok := targetText(t, command); !ok || got != want {
			t.Errorf("got %q, %v; want %q", got, ok, want)
		}
	}
	expectNone := func(t *testing.T, command string) {
		t.Helper()
		if got, ok := targetText(t, command); ok {
			t.Errorf("named %q", got)
		}
	}

	tw(t, fileRedirectAnalysis, "names the destination of a plain redirect", func(t *testing.T) { expectTarget(t, "cat a > out.txt", "out.txt") })
	tw(t, fileRedirectAnalysis, "names the destination rather than the descriptor ahead of the operator", func(t *testing.T) {
		expectTarget(t, "pnpm x 2> err.log", "err.log")
	})
	tw(t, fileRedirectAnalysis, "names only the first destination when words follow it", func(t *testing.T) {
		// tree-sitter-bash parses `f.txt` as a second destination; bash passes it to `grep` as an
		// argument.
		expectTarget(t, "grep pat 2>/dev/null f.txt", "/dev/null")
	})
	tw(t, fileRedirectAnalysis, "names the duplicated descriptor of a duplication", func(t *testing.T) { expectTarget(t, "pnpm x 2>&1", "1") })
	tw(t, fileRedirectAnalysis, "names nothing for a redirect that closes a descriptor", func(t *testing.T) { expectNone(t, "echo hi >&-") })
	t.Run("names nothing, whose word belongs to the command", func(t *testing.T) {
		// The grammar reads the word as the close operator's destination; the operator closes a
		// descriptor and names no file, so bash passes `arg` to the command.
		for _, command := range []string{"cmd >&- arg", "cmd <&- arg"} {
			t.Run(command, func(t *testing.T) { expectNone(t, command) })
		}
	})
}

func TestTrailingArgumentIndex(t *testing.T) {
	trailingText := func(t *testing.T, command, typ string) (string, bool) {
		redirect := withRedirect(t, command, typ)
		index, ok := TrailingArgumentIndex(redirect)
		if !ok {
			return "", false
		}
		return redirect.Child(index).Text(), true
	}

	t.Run("a redirect the grammar hung the command's words on names the first word after the target", func(t *testing.T) {
		for _, c := range []struct{ command, text string }{
			{"grep pat 2>/dev/null f.txt", "f.txt"},
			{"find ~/x 2>/dev/null -delete -print", "-delete"},
			{"pnpm x 2>&1 arg", "arg"},
			{"cmd >&- arg", "arg"},
			{"cmd <&- arg", "arg"},
		} {
			t.Run(c.command, func(t *testing.T) {
				if got, ok := trailingText(t, c.command, "file_redirect"); !ok || got != c.text {
					t.Errorf("got %q, %v", got, ok)
				}
			})
		}
	})
	t.Run("a redirect with nothing after its target names nothing", func(t *testing.T) {
		for _, command := range []string{"cat a > out.txt", "pnpm x 2>&1", "echo hi >&-"} {
			t.Run(command, func(t *testing.T) {
				if got, ok := trailingText(t, command, "file_redirect"); ok {
					t.Errorf("named %q", got)
				}
			})
		}
	})
	t.Run("a heredoc the grammar hung the command's words on names the first word after the delimiter", func(t *testing.T) {
		for _, c := range []struct{ command, text string }{
			{"git <<EOF push --force\nb\nEOF", "push"},
			{"cat <<EOF $(rm x) \"q s\"\nb\nEOF", "$(rm x)"},
		} {
			t.Run(c.command, func(t *testing.T) {
				if got, ok := trailingText(t, c.command, "heredoc_redirect"); !ok || got != c.text {
					t.Errorf("got %q, %v", got, ok)
				}
			})
		}
	})
	tw(t, fileRedirectAnalysis, "names nothing for a heredoc whose line ends at the delimiter", func(t *testing.T) {
		// The body follows the delimiter as the next named child, and it is not a word of the command.
		if got, ok := trailingText(t, "cat <<EOF\nb\nEOF", "heredoc_redirect"); ok {
			t.Errorf("named %q", got)
		}
	})
}

