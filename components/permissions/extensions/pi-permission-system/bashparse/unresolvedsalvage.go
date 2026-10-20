package bashparse

// WithSalvagedRoots runs use over the roots of every region the primary parse could not resolve
// but which re-parses cleanly on its own (unresolved-salvage.ts withSalvagedRoots).
//
// tree-sitter-bash 0.25.1 cannot parse a heredoc redirect combined with `2>&1` and a pipe, though
// each pairing alone is fine. Its recovery hangs an ERROR node holding only the `|` under
// `heredoc_redirect -> file_redirect` and leaves the piped command's words as plain siblings of it,
// and heredoc_redirect is an execution host, descended for the substitutions it may carry and never
// read for text. So `git commit -F - <<'MSG' 2>&1 | rm -rf /tmp/x` enumerates `git commit -F` and
// nothing else, and a configured `bash: {"rm -rf *": "deny"}` is never evaluated against a command
// that really runs (#875).
//
// Re-parsing the dropped region's own source text recovers it, because the grammar gap is in the
// combination: `2>&1 | rm -rf /tmp/x` parses perfectly on its own.
//
// A heredoc tail the grammar cannot parse at all (`cat <<EOF ; rm -rf x`) leaves no region whose
// own text re-parses: the innermost unresolved node is the heredoc redirect, whose text fails the
// same way. So each such heredoc's line is offered too, spelled without its heredoc operators
// ([HeredocFreeLinesWithin]), after the regions.
//
// Salvaging is purely additive: a caller enumerates these roots in addition to the primary one, so
// the result can only ever be more restrictive. The trees need no explicit release in Go.
func WithSalvagedRoots[T any](primary *Node, reparser Parser, use func(salvaged []*Node) T) T {
	var roots []*Node
	candidates := []string{}
	for _, region := range unresolvedRegionsWithin(primary) {
		candidates = append(candidates, region.Text())
	}
	candidates = append(candidates, HeredocFreeLinesWithin(primary)...)
	for _, text := range candidates {
		tree := reparser.Parse(text)
		if tree == nil {
			continue
		}
		// The whole safety argument: tree-sitter's error recovery invents the structure inside an
		// unresolved region (#742), and invented structure does not re-parse. Without this check
		// `cat <> rw.txt` salvages a command unit whose text is `">"`, matched against the bash rules
		// like any real command. A heredoc-free line is derived rather than sliced, but only by
		// removing the operators the scanner tokenized, and it is held to the same condition.
		if ParseUnresolvedWithin(tree.Root) {
			continue
		}
		roots = append(roots, tree.Root)
	}
	return use(roots)
}

// unresolvedRegionsWithin are the innermost nodes beneath root whose subtree the parser could not
// resolve, in source order.
//
// Three exclusions shape the answer. An ERROR node is never a candidate and is never descended in
// search of one: its interior is recovery's invention rather than anything observed (#742), so the
// region worth re-parsing is the node that holds it. Only the innermost such node is offered: an
// enclosing statement reports the error too, and its text re-parses to the same failure. root itself
// is never a candidate, for the same reason taken to its limit.
func unresolvedRegionsWithin(root *Node) []*Node {
	var found []*Node
	collectInnermostUnresolved(root, root, &found)
	return found
}

func collectInnermostUnresolved(node, root *Node, found *[]*Node) {
	if !ParseUnresolvedWithin(node) {
		return
	}
	before := len(*found)
	for _, child := range node.children {
		if child.typ != "ERROR" {
			collectInnermostUnresolved(child, root, found)
		}
	}
	foundDeeper := len(*found) > before
	if !foundDeeper && node != root && node.typ != "ERROR" {
		*found = append(*found, node)
	}
}
