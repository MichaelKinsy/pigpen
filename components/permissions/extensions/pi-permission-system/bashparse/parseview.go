package bashparse

// A view of tree-sitter-bash's parse tree that a correction pass can rewrite (parse-view.ts).
//
// The grammar hangs command-line material on the wrong node in more than one production, and each
// correction is a pass that rewrites the redirected_statements it recognizes. Each pass builds its
// nodes from the primitives here, so a later pass reads an earlier one's output as the same kind of
// view and can re-parent its nodes in turn. A view reads its text from the source by absolute
// offsets, so every caller's StartIndex/EndIndex slicing still holds.

// rewriteFunc returns the replacement for a redirected_statement whose children are already
// rewritten, or nil to leave it as it is (JavaScript's `undefined`).
type rewriteFunc func(statement *Node, children []*Node) *Node

// rewriteStatements is root with each redirected_statement whose parse resolved replaced by what
// rewrite returns for it, or root itself when nothing beneath it changed.
//
// The walk is bottom-up. A statement whose parse failed is never handed to rewrite: its units are
// floored already, and moving a node out of an unresolvable region would hand it a proof in place
// of that region's refusal to prove one (#814).
func rewriteStatements(root *Node, rewrite rewriteFunc) *Node {
	if r := rewriteBeneath(root, rewrite); r != nil {
		return r
	}
	return root
}

// rewriteBeneath is the rewritten node, or nil when nothing beneath node changed.
func rewriteBeneath(node *Node, rewrite rewriteFunc) *Node {
	children := make([]*Node, 0, len(node.children))
	changed := false
	for _, child := range node.children {
		rewritten := rewriteBeneath(child, rewrite)
		if rewritten != nil {
			changed = true
			children = append(children, rewritten)
		} else {
			children = append(children, child)
		}
	}
	if node.typ == "redirected_statement" && !ParseUnresolvedWithin(node) {
		if rewritten := rewrite(node, children); rewritten != nil {
			return rewritten
		}
	}
	if changed {
		return adoptingView(nodeFields{node.typ, node.named, node.start, node.end}, children, ParseUnresolvedWithin(node), node.src)
	}
	return nil
}

// rewrittenNode is a node a correction built: type and namedness of original, children, and the
// span [start, end) in UTF-8 bytes of the source (JavaScript passes UTF-16 indices and a slicer;
// the text of a view is always the source's slice of its span).
func rewrittenNode(typ string, named bool, children []*Node, start, end int, src *Source) *Node {
	return adoptingView(nodeFields{typ, named, start, end}, children, false, src)
}

// childrenOf copies node's children (parse-view.ts childrenOf).
func childrenOf(node *Node) []*Node {
	return append([]*Node(nil), node.children...)
}

// lastNamedChild is the last named child, or nil.
func lastNamedChild(node *Node) *Node {
	for i := len(node.children) - 1; i >= 0; i-- {
		if node.children[i].named {
			return node.children[i]
		}
	}
	return nil
}

// nodeFields are the fields a view copies from the node it stands for.
type nodeFields struct {
	typ        string
	named      bool
	start, end int
}

// adoptingView is a view whose children are views too, so each one's PreviousSibling is its
// neighbor in the rewritten tree rather than the one the grammar gave it.
func adoptingView(fields nodeFields, children []*Node, hasError bool, src *Source) *Node {
	adopted := make([]*Node, len(children))
	for i, child := range children {
		adopted[i] = asView(child)
	}
	return newView(fields, adopted, hasError, src)
}

// asView is node as a view, so a new parent can give it a new previous sibling. Its own children
// stay the grammar's nodes: nothing beneath it moved, so their siblings are still the grammar's.
func asView(node *Node) *Node {
	if node.view {
		return node
	}
	return newView(nodeFields{node.typ, node.named, node.start, node.end}, childrenOf(node), ParseUnresolvedWithin(node), node.src)
}

// newView is NodeView's constructor: it adopts its view children, setting each one's previous
// sibling to the child before it (nil for the first).
func newView(fields nodeFields, children []*Node, hasError bool, src *Source) *Node {
	v := &Node{
		typ:      fields.typ,
		named:    fields.named,
		hasError: hasError,
		start:    fields.start,
		end:      fields.end,
		src:      src,
		children: children,
		view:     true,
	}
	for i, child := range children {
		if child.view {
			if i == 0 {
				child.prev = nil
			} else {
				child.prev = children[i-1]
			}
		}
	}
	return v
}

// at is JavaScript's Array.prototype.at for a node list.
func at(nodes []*Node, i int) *Node {
	if i < 0 {
		i += len(nodes)
	}
	if i < 0 || i >= len(nodes) {
		return nil
	}
	return nodes[i]
}

// jsSlice is Array.prototype.slice(from, to), including negative indices.
func jsSlice(nodes []*Node, from, to int) []*Node {
	n := len(nodes)
	clamp := func(i int) int {
		if i < 0 {
			i += n
			if i < 0 {
				i = 0
			}
		}
		if i > n {
			i = n
		}
		return i
	}
	from, to = clamp(from), clamp(to)
	if from >= to {
		return nil
	}
	return append([]*Node(nil), nodes[from:to]...)
}

// withReplaced is Array.prototype.with(index, node): a copy with one element replaced; a negative
// index counts from the end. Indices out of range throw in JavaScript; here they panic and the
// caller's recover turns that into no tree.
func withReplaced(nodes []*Node, index int, node *Node) []*Node {
	if index < 0 {
		index += len(nodes)
	}
	if index < 0 || index >= len(nodes) {
		panic("bashparse: with(): index out of range")
	}
	out := append([]*Node(nil), nodes...)
	out[index] = node
	return out
}
