package bashparse

import (
	"reflect"
	"testing"
)

// Twins of test/access-intent/bash/unresolved-salvage.test.ts.

const fileSalvage = "access-intent/bash/unresolved-salvage"

// reportedCommand is the reported command: a heredoc redirect, `2>&1`, and a pipe together.
const reportedCommand = "git add -A . && git commit -F - <<'MSG' 2>&1 | rm -rf /tmp/x\nmsg\nMSG"

// salvagedTextOf runs WithSalvagedRoots over a real parse of command, returning the source text of
// each salvaged root. Each root is its own re-parsed program, so the text is what identifies the
// region that was salvaged.
func salvagedTextOf(t *testing.T, command string) []string {
	t.Helper()
	p := mustGetParser(t)
	tree := p.Parse(command)
	if tree == nil {
		t.Fatal("parse returned nil")
	}
	return WithSalvagedRoots(tree.Root, p, func(roots []*Node) []string {
		out := []string{}
		for _, r := range roots {
			out = append(out, r.Text())
		}
		return out
	})
}

func expectSalvaged(t *testing.T, command string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if got := salvagedTextOf(t, command); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// countingParser counts the trees WithSalvagedRoots asks the reparser for.
type countingParser struct {
	inner   Parser
	created int
	cleanly int
}

func (c *countingParser) Parse(input string) *Tree {
	tree := c.inner.Parse(input)
	if tree != nil {
		c.created++
		if !ParseUnresolvedWithin(tree.Root) {
			c.cleanly++
		}
	}
	return tree
}

func TestWithSalvagedRoots(t *testing.T) {
	tw(t, fileSalvage, "salvages the redirect holding the command tree-sitter dropped", func(t *testing.T) {
		expectSalvaged(t, reportedCommand, "2>&1 | rm -rf /tmp/x", "git add -A . && git commit -F - 2>&1 | rm -rf /tmp/x")
	})
	tw(t, fileSalvage, "salvages a region whose dropped command reads a path", func(t *testing.T) {
		expectSalvaged(t, "cat <<'MSG' 2>&1 | cat /etc/shadow\nmsg\nMSG", "2>&1 | cat /etc/shadow", "cat 2>&1 | cat /etc/shadow")
	})
	tw(t, fileSalvage, "salvages the innermost unresolved node, not an enclosing one", func(t *testing.T) {
		// `redirected_statement` and `heredoc_redirect` both report the error too, and each carries
		// the whole command line; re-parsing either fails again for the same reason and salvages
		// nothing.
		salvaged := salvagedTextOf(t, reportedCommand)
		for _, s := range salvaged {
			if s == reportedCommand {
				t.Error("salvaged the whole command")
			}
		}
		if len(salvaged) == 0 || salvaged[0] != "2>&1 | rm -rf /tmp/x" {
			t.Errorf("salvaged = %q", salvaged)
		}
	})

	tw(t, fileSalvage, "salvages the line spelled without its heredoc", func(t *testing.T) {
		// The innermost unresolved node is the heredoc redirect itself, whose own text fails again,
		// so only the heredoc-free spelling recovers `rm`.
		expectSalvaged(t, "cat <<EOF ; rm -rf x\nb\nEOF", "cat ; rm -rf x")
	})
	tw(t, fileSalvage, "salvages nothing from a heredoc-free spelling whose re-parse fails", func(t *testing.T) {
		// The grammar lexes `<<B` as `< <B`, so it is no heredoc to cut, and the spelling
		// `cat <<B ; rm -rf x` fails again; only the region salvage recovers `rm -rf x` here.
		expectSalvaged(t, "cat <<A <<B ; rm -rf x\na\nA\nb\nB", "<B ; rm -rf x")
	})
	tw(t, fileSalvage, "offers the regions ahead of the heredoc-free lines", func(t *testing.T) {
		expectSalvaged(t, "cat <<EOF > /tmp/o | rm -rf x\nb\nEOF", "> /tmp/o | rm -rf x", "cat > /tmp/o | rm -rf x")
	})

	t.Run("a region whose own re-parse fails salvages nothing", func(t *testing.T) {
		// Without the clean-re-parse guard these emit `">"` and `"$(("` as command units: invented
		// text matched against the bash rules (#814).
		for _, c := range []struct{ label, command string }{
			{"a read-write open inside the redirect", "cat <> rw.txt"},
			{"a read-write open beside a resolved redirect", "echo hi > out.txt <> rw.txt; rm -rf /tmp/y"},
			{"a stranded arithmetic opener", "cat $(( > out.txt"},
		} {
			t.Run(c.label, func(t *testing.T) { expectSalvaged(t, c.command) })
		}
	})

	tw(t, fileSalvage, "salvages an unterminated heredoc's line and nothing from its body", func(t *testing.T) {
		// The body re-parses as garbage, and the heredoc-free spelling of the line ends before it.
		expectSalvaged(t, "cat <<'EOF'\nsee `rm -rf x` here", "cat")
	})
	t.Run("a failure the whole program carries salvages nothing", func(t *testing.T) {
		// The root is the only unresolved node, and re-parsing it reproduces the same failure, so
		// tree-sitter's invented structure (#742) is never re-read as commands.
		for _, c := range []struct{ label, command string }{
			{"an unbalanced quote", `echo "$(rm x)`},
			{"an unterminated control-flow statement", "for f in a; do rm $f"},
			{"an unterminated brace group", "{ echo hi"},
		} {
			t.Run(c.label, func(t *testing.T) { expectSalvaged(t, c.command) })
		}
	})
	t.Run("a program that parsed cleanly salvages nothing", func(t *testing.T) {
		for _, c := range []struct{ label, command string }{
			{"a lone command", "echo hi"},
			{"a chain", "cd /repo && git push"},
			{"a heredoc without the failing combination", "cat <<'M'\nbody\nM"},
		} {
			t.Run(c.label, func(t *testing.T) { expectSalvaged(t, c.command) })
		}
	})

	tw(t, fileSalvage, "salvages a region no redirect holds", func(t *testing.T) {
		// A stub node tree, so the rule can be exercised on a shape tree-sitter-bash 0.25.1 does
		// not currently produce: no real command distinguishes "the innermost unresolved node" from
		// "the innermost unresolved file_redirect". The distinction is the whole point: ADR 0013's
		// fail-closed clause is triggered by the parse's health rather than by a node type.
		stub := func(typ, text string, hasError bool, children ...*Node) *Node {
			n := NewFakeNode(typ, text, children...)
			n.hasError = hasError || typ == "ERROR"
			for _, c := range children {
				n.hasError = n.hasError || c.hasError
			}
			return n
		}
		region := stub("some_future_node", "rm -rf /tmp/x", false, stub("ERROR", "|", false))
		root := stub("program", "rm -rf /tmp/x", false, region)

		got := WithSalvagedRoots(root, mustGetParser(t), func(roots []*Node) []string {
			out := []string{}
			for _, r := range roots {
				out = append(out, r.Text())
			}
			return out
		})
		if !reflect.DeepEqual(got, []string{"rm -rf /tmp/x"}) {
			t.Errorf("got %q", got)
		}
	})

	tskip(t, fileSalvage, "deletes every salvage tree before returning",
		"Go has no tree handle to delete: nodes are garbage collected, so there is no `delete()` for the reparser to count; the admitted-tree count the original's second case pins is checked by TestWithSalvagedRootsAdmitsOneTreePerCandidate")
	tskip(t, fileSalvage, "deletes them even when the caller throws",
		"Go has no tree handle to delete: nodes are garbage collected, so there is no `delete()` for the reparser to count; a panic in the callback propagating is checked by TestWithSalvagedRootsPropagatesACallbackPanic")
}

// One tree per admitted candidate: the region and the heredoc-free line (the count the original's
// "deletes them even when the caller throws" case pins).
func TestWithSalvagedRootsAdmitsOneTreePerCandidate(t *testing.T) {
	counting := &countingParser{inner: mustGetParser(t)}
	tree := counting.inner.Parse(reportedCommand)
	roots := WithSalvagedRoots(tree.Root, counting, func(roots []*Node) int { return len(roots) })
	if counting.created == 0 || roots != 2 || counting.cleanly != 2 {
		t.Errorf("created %d trees, %d clean, %d roots", counting.created, counting.cleanly, roots)
	}
}

func TestWithSalvagedRootsPropagatesACallbackPanic(t *testing.T) {
	p := mustGetParser(t)
	tree := p.Parse(reportedCommand)
	defer func() {
		if r := recover(); r != "boom" {
			t.Errorf("recovered %v, want boom", r)
		}
	}()
	WithSalvagedRoots(tree.Root, p, func([]*Node) []*Node { panic("boom") })
	t.Error("the panic did not propagate")
}
