package bashparse

// HoistHeredocTails is tree-sitter-bash's parse with the rest of each heredoc's line moved out of
// the heredoc, to where the grammar puts it when the heredoc is written as a plain input redirect
// (heredoc-tails.ts).
//
// The grammar (0.25.1) lets a heredoc_redirect carry whatever follows its delimiter on the same
// line: further redirects (`cat <<EOF > /tmp/o`), a `| ...` statement, or an `&& ...` / `|| ...`
// statement. They parse as children of the heredoc, which every walker reads only for the
// substitutions it hosts, so `cat <<EOF | rm -rf /tmp/x` would enumerate as `cat` alone and
// `cat <<EOF > /tmp/o` would project no path (#979).
//
// The corrected tree is the grammar's own parse of the same line with `< in` in place of `<<EOF`:
// a redirect becomes a sibling after the heredoc, and a `| ...` or `&& ...` statement is joined to
// the redirected statement the way the grammar joins it to `cat < in`. Words after the delimiter
// stay in the heredoc: [ReattachRedirectArguments] hands them to the command. The heredoc keeps
// its body, which is written after the rest of the line, so it ends after the nodes moved out of
// it.
//
// Returns root itself when no heredoc carries such a tail.
func HoistHeredocTails(root *Node) *Node {
	return rewriteStatements(root, hoistStatement)
}

// hoistStatement is statement with its heredoc's tail moved out, or nil when no heredoc among
// children carries one.
func hoistStatement(statement *Node, children []*Node) *Node {
	heredocIndex := -1
	for i, child := range children {
		if child.typ == "heredoc_redirect" && tailOf(child) != nil {
			heredocIndex = i
			break
		}
	}
	// children.at(-1) is the last child when none matched, as in the original.
	heredoc := at(children, heredocIndex)
	if heredoc == nil {
		return nil
	}
	tail := tailOf(heredoc)
	if tail == nil {
		return nil
	}

	after := jsSlice(children, heredocIndex+1, len(children))
	// A statement tail runs to the end of the line, so nothing of the statement can follow it;
	// anything that does is a shape this correction does not know.
	if tail.joined != nil {
		for _, child := range after {
			if child.named {
				return nil
			}
		}
	}

	src := statement.src
	hoisted := []*Node{}
	hoisted = append(hoisted, jsSlice(children, 0, heredocIndex)...)
	hoisted = append(hoisted, rewrittenNode(heredoc.typ, heredoc.named, tail.kept, heredoc.start, heredoc.end, src))
	hoisted = append(hoisted, tail.redirects...)
	hoisted = append(hoisted, after...)
	redirected := rewrittenNode(statement.typ, statement.named, hoisted, statement.start, statement.end, src)
	if tail.joined == nil {
		return redirected
	}
	return join(redirected, tail.joined.operator, withTrailingRedirectsOutermost(tail.joined.statement, src), src)
}

// withTrailingRedirectsOutermost is statement with a redirect written at its end hung off all of
// it, as the grammar hangs it when the same text is not a heredoc's tail.
//
// At the top level `a | b | c > o` parses as `(a | b | c) > o`, but in a heredoc's tail the grammar
// parses it as `a | ((b | c) > o)`. Left that way, the joined write would not reach the heredoc's
// own command.
func withTrailingRedirectsOutermost(statement *Node, src *Source) *Node {
	if statement.typ == "redirected_statement" {
		return statement
	}
	body, redirects, ok := detachTrailingRedirects(statement, src)
	if !ok {
		return statement
	}
	kids := append([]*Node{body}, redirects...)
	return rewrittenNode("redirected_statement", true, kids, statement.start, statement.end, src)
}

// detachTrailingRedirects is node without the redirected_statement that ends it, plus that
// statement's redirects, or ok=false when node does not end in one.
//
// The walk follows the last element of each list and pipeline, and a pipeline left holding a
// pipeline is flattened into one, as the grammar parses `a | b | c`.
func detachTrailingRedirects(node *Node, src *Source) (body *Node, redirects []*Node, ok bool) {
	children := childrenOf(node)
	if node.typ == "redirected_statement" {
		bodyIndex := firstNamed(children)
		return at0(children, bodyIndex), jsSlice(children, bodyIndex+1, len(children)), true
	}
	if node.typ != "list" && node.typ != "pipeline" {
		return nil, nil, false
	}
	lastIndex := lastNamedIndex(children)
	innerBody, innerRedirects, innerOK := detachTrailingRedirects(mustNode(at0(children, lastIndex)), src)
	if !innerOK {
		return nil, nil, false
	}
	var replaced []*Node
	if node.typ == "pipeline" && innerBody.typ == "pipeline" {
		replaced = append(jsSlice(children, 0, lastIndex), childrenOf(innerBody)...)
	} else {
		replaced = withReplaced(children, lastIndex, innerBody)
	}
	return rewrittenNode(node.typ, node.named, replaced, node.start, innerBody.end, src), innerRedirects, true
}

// at0 is children[index] in JavaScript: undefined (nil) for an index out of range, including -1.
func at0(nodes []*Node, index int) *Node {
	if index < 0 || index >= len(nodes) {
		return nil
	}
	return nodes[index]
}

// mustNode stands for a JavaScript property read on a value that the grammar guarantees exists; a
// nil there is a TypeError in the original, here a panic the parser's recover turns into no tree.
func mustNode(n *Node) *Node {
	if n == nil {
		panic("bashparse: unexpected missing node")
	}
	return n
}

func firstNamed(nodes []*Node) int {
	for i, n := range nodes {
		if n.named {
			return i
		}
	}
	return -1
}

func lastNamedIndex(nodes []*Node) int {
	for i := len(nodes) - 1; i >= 0; i-- {
		if nodes[i].named {
			return i
		}
	}
	return -1
}

// heredocTail is what a heredoc carries after its delimiter, split by where each part goes.
type heredocTail struct {
	// kept are the heredoc's own children: operator, delimiter, any words, and body.
	kept []*Node
	// redirects are written after the delimiter.
	redirects []*Node
	// joined is a `| ...`, `|& ...`, `&& ...`, or `|| ...` statement after them, if any.
	joined *joinedTail
}

type joinedTail struct {
	operator  *Node
	statement *Node
}

// tailOf is heredoc's tail, or nil when it carries neither a redirect nor a statement after its
// delimiter.
//
// The grammar spells a `| ...` tail as one pipeline child holding the operator and the statement,
// and an `&& ...` / `|| ...` tail as the operator token and the statement as two children of the
// heredoc itself. That statement can be a pipeline too (`cat <<EOF && ls | rm x`), so a pipeline
// child is the `| ...` form only when no operator came before it.
func tailOf(heredoc *Node) *heredocTail {
	var kept, redirects []*Node
	var operator, statement *Node
	for _, child := range childrenOf(heredoc) {
		switch {
		case child.typ == "file_redirect" || child.typ == "herestring_redirect":
			redirects = append(redirects, child)
		case child.typ == "pipeline" && operator == nil:
			kids := childrenOf(child)
			operator = at0(kids, 0)
			statement = nil
			for _, k := range kids {
				if k.named {
					statement = k
					break
				}
			}
		case (child.typ == "&&" || child.typ == "||") && !child.named:
			operator = child
		case operator != nil && statement == nil && child.named:
			statement = child
		default:
			kept = append(kept, child)
		}
	}
	if operator != nil && statement == nil {
		return nil
	}
	var joined *joinedTail
	if operator != nil && statement != nil {
		joined = &joinedTail{operator, statement}
	}
	if len(redirects) == 0 && joined == nil {
		return nil
	}
	return &heredocTail{kept: kept, redirects: redirects, joined: joined}
}

// join is redirected joined to statement by operator, grouped as the grammar groups
// `cat < in <operator> <statement>`.
//
// The grammar hangs a redirect off everything before it, so a joined redirected_statement keeps its
// redirects outermost. That holds inside a pipeline too: `cat < in && a > o | b` parses as
// `(cat < in && a > o) | b`, so a pipeline whose first stage is redirected is joined at that stage.
// A list is left-associative and binds looser than a pipe, so the join reaches its first element.
// A pipe tail onto any other pipeline extends it. Anything else becomes the operator's own
// two-element node.
//
// Each rebuilt node spans from redirected's start to the furthest end beneath it, which is the
// heredoc's, since its body is written after the tail.
func join(redirected, operator, statement *Node, src *Source) *Node {
	isPipe := operator.typ == "|" || operator.typ == "|&"
	children := childrenOf(statement)
	end := max(redirected.end, statement.end)
	rebuild := func(typ string, named bool, kids []*Node) *Node {
		return rewrittenNode(typ, named, kids, redirected.start, end, src)
	}

	first := firstNamed(children)
	if joinsAtFirstElement(statement, at0(children, first)) {
		return rebuild(statement.typ, statement.named,
			withReplaced(children, first, join(redirected, operator, mustNode(at0(children, first)), src)))
	}
	if isPipe && statement.typ == "pipeline" {
		kids := append([]*Node{redirected, operator}, children...)
		return rebuild(statement.typ, statement.named, kids)
	}
	typ := "list"
	if isPipe {
		typ = "pipeline"
	}
	return rebuild(typ, true, []*Node{redirected, operator, statement})
}

// joinsAtFirstElement reports whether the grammar joins what precedes statement to its first
// element rather than to statement as a whole.
func joinsAtFirstElement(statement, first *Node) bool {
	if statement.typ == "redirected_statement" || statement.typ == "list" {
		return true
	}
	return statement.typ == "pipeline" && first != nil && first.typ == "redirected_statement"
}
