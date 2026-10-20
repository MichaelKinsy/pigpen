package bashparse

// The redirect-node readers redirect-arguments.ts takes from redirect-analysis.ts. They live here
// because the correction needs them and bashparse cannot import the root package;
// redirect-analysis' own port reads them from this package.

// LiteralNodeTypes are the node types tree-sitter-bash 0.25.1 parses a literal word as: its
// `_literal` rule, which a file redirect's destinations and a heredoc's trailing words are both
// declared as (redirect-analysis.ts LITERAL_NODE_TYPES).
var LiteralNodeTypes = map[string]bool{
	"concatenation":        true,
	"word":                 true,
	"string":               true,
	"raw_string":           true,
	"translated_string":    true,
	"ansi_c_string":        true,
	"number":               true,
	"expansion":            true,
	"simple_expansion":     true,
	"command_substitution": true,
	"process_substitution": true,
	"arithmetic_expansion": true,
	"brace_expression":     true,
}

// closeOperators close a descriptor, naming no file (`>&-`, `<&-`).
var closeOperators = map[string]bool{">&-": true, "<&-": true}

// RedirectOperatorIndex is the index of redirect's operator, its only unnamed child.
func RedirectOperatorIndex(redirect *Node) (int, bool) {
	for i, c := range redirect.children {
		if !c.named {
			return i, true
		}
	}
	return 0, false
}

// namedChildIndexAfter is the index of the first named child of node after index after.
func namedChildIndexAfter(node *Node, after int) (int, bool) {
	for i := after + 1; i < len(node.children); i++ {
		if node.children[i].named {
			return i, true
		}
	}
	return 0, false
}

// RedirectTargetIndex is the child index of the node redirect reads or writes (its first named
// child after the operator), or false when it names none: nothing follows the operator, or the
// operator closes a descriptor (`>&-`, `<&-`). Only the first: tree-sitter-bash 0.25.1 declares the
// destination repeat1, so the words after it parse as further destinations while bash passes them
// to the redirected command (#977).
func RedirectTargetIndex(redirect *Node) (int, bool) {
	operator, ok := RedirectOperatorIndex(redirect)
	if !ok {
		return 0, false
	}
	if closeOperators[redirect.children[operator].typ] {
		return 0, false
	}
	return namedChildIndexAfter(redirect, operator)
}

// TrailingArgumentIndex is the child index of the first word the grammar appended after
// redirect's own target, or false when none follows. Every LiteralNodeTypes child from there on is
// a word bash passes to the redirected command rather than a destination of the redirect (#977);
// a heredoc carries them the same way, and its body, or any other tail, is not a word (#979).
func TrailingArgumentIndex(redirect *Node) (int, bool) {
	operator, ok := RedirectOperatorIndex(redirect)
	if !ok {
		return 0, false
	}
	after := operator
	if target, ok := RedirectTargetIndex(redirect); ok {
		after = target
	}
	index, ok := namedChildIndexAfter(redirect, after)
	if !ok {
		return 0, false
	}
	if LiteralNodeTypes[redirect.children[index].typ] {
		return index, true
	}
	return 0, false
}
