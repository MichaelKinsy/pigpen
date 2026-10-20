package bashparse

// ReattachRedirectArguments is tree-sitter-bash's parse with each word it hung on a redirect handed
// back to the command it belongs to (redirect-arguments.ts).
//
// The grammar (0.25.1) declares a file redirect's destination repeat1, so in
// `git 2>/dev/null push --force` the words `push --force` parse as further destinations of the
// statement's redirect, where bash passes them to `git` (tree-sitter/tree-sitter-bash#233). It is
// corrected once, here, where the tree enters the package (#977).
//
// The corrected shape is the one the grammar already produces for a redirect written before the
// command: the redirect becomes a child of the command, between its words. Every redirect from the
// body up to the last one carrying words moves into the command, each truncated after its own
// target and followed by its words; a redirect after the last word stays at the statement, and a
// statement left with no redirect gives way to its body.
//
// A heredoc carries words the same way (`git <<EOF push --force`), and they move into the command
// after it; its body stays in the heredoc, which is written after the words and so ends after them
// (#979). Every other tail a heredoc can carry is [HoistHeredocTails]'s to move.
//
// Three kinds of statement are left exactly as the grammar produced them: one whose parse failed
// (#814), one whose body is not a command (`{ a; } 2>/dev/null b`), and one with no words after
// any redirect's target.
//
// Returns root itself when nothing in the tree needs correcting.
func ReattachRedirectArguments(root *Node) *Node {
	return rewriteStatements(root, reattachStatement)
}

// reattachStatement moves the words the grammar hung on statement's redirects into the command they
// belong to, or returns nil when there are none or no command to take them. children are the
// statement's children, already corrected.
func reattachStatement(statement *Node, children []*Node) *Node {
	bodyIndex := firstNamed(children)
	body := at(children, bodyIndex)
	if body == nil || !reachesCommand(body) {
		return nil
	}

	lastCarrier := -1
	for i := len(children) - 1; i >= 0; i-- {
		if wordCarrying(children[i].typ) {
			if _, ok := TrailingArgumentIndex(children[i]); ok {
				lastCarrier = i
				break
			}
		}
	}
	if lastCarrier == -1 {
		return nil
	}

	src := statement.src
	var moved []*Node
	for _, child := range jsSlice(children, bodyIndex+1, lastCarrier+1) {
		moved = append(moved, splitRedirect(child, src)...)
	}
	newBody := appendToRightmostCommand(body, moved, src)
	rest := jsSlice(children, lastCarrier+1, len(children))
	hasNamed := false
	for _, child := range rest {
		if child.named {
			hasNamed = true
			break
		}
	}
	if !hasNamed {
		return newBody
	}
	kids := append(jsSlice(children, 0, bodyIndex), newBody)
	kids = append(kids, rest...)
	return rewrittenNode(statement.typ, statement.named, kids, statement.start, statement.end, src)
}

// reachesCommand reports whether the words after a redirect on body belong to a command: body is
// one, or is a list or pipeline whose last element reaches one. The grammar hangs a redirect on the
// last command of `cd a && git 2>/dev/null push` or `rg x | xargs ls 2>&1 ~/x` off the whole list
// or pipeline, while bash gives it, and its words, to that last command.
func reachesCommand(body *Node) bool {
	if body.typ == "command" {
		return true
	}
	if body.typ != "list" && body.typ != "pipeline" {
		return false
	}
	last := lastNamedChild(body)
	return last != nil && reachesCommand(last)
}

// wordCarrying are the redirects the grammar can hang a command's words on.
func wordCarrying(typ string) bool {
	return typ == "file_redirect" || typ == "heredoc_redirect"
}

// splitRedirect is redirect as it belongs in the command: without the words the grammar appended
// to it, and followed by them. A redirect carrying no words, or a node that cannot carry any,
// moves as it is. A file redirect's words are the rest of its children, so it is truncated after
// its target; a heredoc keeps its body and delimiter, which follow the words in the source, so it
// keeps its own end.
func splitRedirect(redirect *Node, src *Source) []*Node {
	trailing := 0
	ok := false
	if wordCarrying(redirect.typ) {
		trailing, ok = TrailingArgumentIndex(redirect)
	}
	if !ok {
		return []*Node{redirect}
	}

	var kept, words []*Node
	for i, child := range redirect.children {
		if i >= trailing && LiteralNodeTypes[child.typ] {
			words = append(words, child)
		} else {
			kept = append(kept, child)
		}
	}
	end := redirect.start
	if len(kept) > 0 {
		end = kept[len(kept)-1].end
	}
	return append([]*Node{rewrittenNode(redirect.typ, redirect.named, kept, redirect.start, end, src)}, words...)
}

// appendToRightmostCommand is body with moved appended to its rightmost command: the command
// itself, or the last element of a list or pipeline, recursively. Each node on the way grows to the
// furthest end among the moved nodes, which is a heredoc's when one moved, since its body is
// written after the words that follow it.
func appendToRightmostCommand(body *Node, moved []*Node, src *Source) *Node {
	children := childrenOf(body)
	end := body.end
	for _, n := range moved {
		end = max(end, n.end)
	}
	if body.typ == "command" {
		return rewrittenNode(body.typ, body.named, append(children, moved...), body.start, end, src)
	}
	lastIndex := lastNamedIndex(children)
	last := mustNode(at0(children, lastIndex))
	replaced := withReplaced(children, lastIndex, appendToRightmostCommand(last, moved, src))
	return rewrittenNode(body.typ, body.named, replaced, body.start, end, src)
}
