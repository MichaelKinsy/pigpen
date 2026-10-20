package bashparse

import (
	"sort"
	"strconv"
	"strings"
	"testing"
)

// parse is the corrected parser's tree for command (the original's `getParser().parse`).
func parse(t testing.TB, command string) *Node {
	t.Helper()
	p, err := GetParser()
	if err != nil {
		t.Fatal(err)
	}
	tree := p.Parse(command)
	if tree == nil {
		t.Fatalf("parse returned nil for %q", command)
	}
	return tree.Root
}

// grammarParse is the grammar's own tree for command (`getGrammarParser().parse`).
func grammarParse(t testing.TB, command string) *Node {
	t.Helper()
	p, err := GrammarParser()
	if err != nil {
		t.Fatal(err)
	}
	tree := p.Parse(command)
	if tree == nil {
		t.Fatalf("grammar parse returned nil for %q", command)
	}
	return tree.Root
}

// withCorrected parses command with the grammar's own parser and hands the corrected root,
// alongside the grammar's, to read (test/helpers/bash-parse-tree.ts withCorrected): the subject is
// what a correction does to the grammar's real output, so the input must be that output.
func withCorrected[T any](t testing.TB, command string, correct func(*Node) *Node, read func(corrected, grammar *Node) T) T {
	t.Helper()
	root := grammarParse(t, command)
	return read(correct(root), root)
}

// jsonString is JSON.stringify of a string: no HTML escaping, U+2028/9 left as they are.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			b.WriteString(`\u00` + strconv.FormatInt(int64(r)+0x100, 16)[1:])
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// shape is a node's named structure as an S-expression: an inner node renders as `(type child...)`,
// and a node with no named children as its text in quotes.
func shape(node *Node) string {
	var named []string
	for _, c := range node.children {
		if c.named {
			named = append(named, shape(c))
		}
	}
	if len(named) == 0 {
		return jsonString(node.Text())
	}
	return "(" + node.typ + " " + strings.Join(named, " ") + ")"
}

// findNodes is the depth-first collection of every node of the given type, in source order.
func findNodes(node *Node, typ string) []*Node {
	var out []*Node
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.typ == typ {
			out = append(out, n)
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(node)
	return out
}

func allNodes(node *Node) []*Node {
	out := []*Node{node}
	for _, c := range node.children {
		out = append(out, allNodes(c)...)
	}
	return out
}

// spanOf is a node's range and type, which identify it across a real node and a view.
func spanOf(node *Node) string {
	if node == nil {
		return "null"
	}
	return node.typ + "@" + strconv.Itoa(node.StartIndex()) + "-" + strconv.Itoa(node.EndIndex())
}

// viewContractViolations is every way corrected breaks the contract a corrected parse tree keeps
// with the grammar's tree it was built from, or none when it keeps it (test/helpers/
// bash-parse-tree.ts): each node's text is its slice of the source, children are in source order
// and do not overlap (except that a child holding a heredoc may enclose the siblings after it),
// each child lies within its parent, every leaf of the grammar's tree appears exactly once, each
// child's previousSibling is the child before it, and no node reports a parse error.
func viewContractViolations(source string, corrected, grammar *Node) []string {
	violations := []string{}
	src := newSource(source)
	for _, node := range allNodes(corrected) {
		if node.Text() != src.Slice(node.StartIndex(), node.EndIndex()) {
			violations = append(violations, spanOf(node)+": text is not its source slice")
		}
		if node.hasError {
			violations = append(violations, spanOf(node)+": reports an error")
		}
		for i, child := range node.children {
			var before *Node
			if i > 0 {
				before = node.children[i-1]
			}
			if before != nil && !followsInSource(before, child) {
				violations = append(violations, spanOf(child)+": overlaps "+spanOf(before))
			}
			if child.StartIndex() < node.StartIndex() || child.EndIndex() > node.EndIndex() {
				violations = append(violations, spanOf(child)+": outside "+spanOf(node))
			}
			if spanOf(child.prev) != spanOf(before) {
				violations = append(violations, spanOf(child)+": previous sibling is "+spanOf(child.prev)+", not "+spanOf(before))
			}
		}
	}
	expected := leavesOf(grammar)
	actual := leavesOf(corrected)
	if strings.Join(actual, " ") != strings.Join(expected, " ") {
		violations = append(violations, "leaves differ: expected "+strings.Join(expected, " ")+", got "+strings.Join(actual, " "))
	}
	return violations
}

// followsInSource reports whether after is where a sibling following before belongs: past its end,
// or, when before holds a heredoc, anywhere past its start.
func followsInSource(before, after *Node) bool {
	if after.StartIndex() >= before.EndIndex() {
		return true
	}
	holdsHeredoc := false
	for _, n := range allNodes(before) {
		if n.typ == "heredoc_redirect" {
			holdsHeredoc = true
		}
	}
	return holdsHeredoc && after.StartIndex() >= before.StartIndex()
}

// leavesOf are the leaves of a tree, identified by span and sorted, duplicates kept.
func leavesOf(root *Node) []string {
	var out []string
	for _, n := range allNodes(root) {
		if len(n.children) == 0 {
			out = append(out, spanOf(n))
		}
	}
	sort.Strings(out)
	return out
}
