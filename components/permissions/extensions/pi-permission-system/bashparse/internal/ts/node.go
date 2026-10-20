package ts

// Port of the parts of lib/src/node.c that web-tree-sitter's Node reads: type, isNamed, isMissing,
// isExtra, hasError, startIndex/endIndex, child(i) with its field name, and previousSibling.

// Node is a TSNode: a subtree at a position of a tree. id identifies the subtree's slot in its
// parent (the C compares `const Subtree *` addresses).
type Node struct {
	tree *Tree
	id   **subtree
	sub  *subtree
	pos  Length
	// alias is the symbol the parent's production names this child as (0 for none).
	alias uint16
}

// Root is the tree's root node (ts_tree_root_node).
func (t *Tree) Root() Node {
	return Node{tree: t, id: &t.root, sub: t.root, pos: t.root.padding}
}

func (n Node) isNull() bool { return n.id == nil }

// Same reports whether two nodes are the same slot of the same tree.
func (n Node) Same(o Node) bool { return n.id == o.id }

// Data is what the node API reports about a node, positions in UTF-16 code units.
type NodeData struct {
	Type     string
	Named    bool
	HasError bool
	Missing  bool
	Extra    bool
	Start    int
	End      int
}

// Data is the node's type, flags and span.
func (n Node) Data() NodeData {
	symbol := n.alias
	if symbol == 0 {
		symbol = n.sub.symbol
	}
	symbol = n.tree.lang.publicSymbol(symbol)
	named := n.sub.named
	if n.alias != 0 {
		named = n.tree.lang.metadata(n.alias).named
	}
	return NodeData{
		Type:     n.tree.lang.symbolName(symbol),
		Named:    named,
		HasError: n.sub.errorCostOf() > 0,
		Missing:  n.sub.isMissing,
		Extra:    n.sub.extra,
		Start:    int(n.pos.bytes / 2),
		End:      int((n.pos.bytes + n.sub.size.bytes) / 2),
	}
}

func (n Node) startByte() uint32 { return n.pos.bytes }
func (n Node) endByte() uint32   { return n.pos.bytes + n.sub.size.bytes }

// childCount is ts_node_child_count: the number of visible children.
func (n Node) childCount() uint32 {
	if n.sub.childCount() > 0 {
		return n.sub.visibleChildCount
	}
	return 0
}

// isRelevant is ts_node__is_relevant(include_anonymous = true).
func (n Node) isRelevant() bool { return n.sub.visible || n.alias != 0 }

type childIterator struct {
	tree                 *Tree
	parent               *subtree
	position             Length
	childIndex           int
	structuralChildIndex int
	aliasSequence        []uint16
}

// iterate is ts_node_iterate_children.
func (n Node) iterate() childIterator {
	if n.sub.childCount() == 0 {
		return childIterator{tree: n.tree}
	}
	return childIterator{tree: n.tree, parent: n.sub, position: n.pos, aliasSequence: n.tree.lang.aliasSequence(n.sub.productionID)}
}

// next is ts_node_child_iterator_next.
func (it *childIterator) next() (Node, bool) {
	if it.parent == nil || it.childIndex == len(it.parent.children) {
		return Node{}, false
	}
	slot := &it.parent.children[it.childIndex]
	child := *slot
	var alias uint16
	if !child.extra {
		if it.aliasSequence != nil {
			alias = it.aliasSequence[it.structuralChildIndex]
		}
		it.structuralChildIndex++
	}
	if it.childIndex > 0 {
		it.position = lengthAdd(it.position, child.padding)
	}
	result := Node{tree: it.tree, id: slot, sub: child, pos: it.position, alias: alias}
	it.position = lengthAdd(it.position, child.size)
	it.childIndex++
	return result, true
}

// fieldNameFromLanguage is ts_node__field_name_from_language.
func (n Node) fieldNameFromLanguage(structuralChildIndex int) string {
	for _, e := range n.tree.lang.fieldMap(n.sub.productionID) {
		if !e.inherited && int(e.childIndex) == structuralChildIndex {
			return n.tree.lang.fieldNames[e.fieldID]
		}
	}
	return ""
}

// Child is one visible child of a node with the field name it is reported under.
type Child struct {
	Node  Node
	Field string
}

// Children are the node's visible children in order, each with ts_node_field_name_for_child's
// answer: flattening the hidden nodes between a node and its visible descendants, as ts_node_child
// does, and carrying the field of the nearest hidden ancestor that has one.
func (n Node) Children() []Child {
	var out []Child
	n.collect("", &out)
	return out
}

func (n Node) collect(inherited string, out *[]Child) {
	it := n.iterate()
	for {
		child, ok := it.next()
		if !ok {
			return
		}
		if child.isRelevant() {
			field := ""
			if !child.sub.extra {
				field = n.fieldNameFromLanguage(it.structuralChildIndex - 1)
				if field == "" {
					field = inherited
				}
			}
			*out = append(*out, Child{child, field})
		} else if child.childCount() > 0 {
			next := inherited
			if f := n.fieldNameFromLanguage(it.structuralChildIndex - 1); f != "" {
				next = f
			}
			child.collect(next, out)
		}
	}
}

// subtreeHasTrailingEmptyDescendant is ts_subtree_has_trailing_empty_descendant.
func subtreeHasTrailingEmptyDescendant(self, other *subtree) bool {
	for i := len(self.children) - 1; i >= 0; i-- {
		child := self.children[i]
		if child.totalBytes() > 0 {
			break
		}
		if child == other || subtreeHasTrailingEmptyDescendant(child, other) {
			return true
		}
	}
	return false
}

// childWithDescendant is ts_node_child_with_descendant.
func childWithDescendant(self, descendant Node) Node {
	startByte := descendant.startByte()
	endByte := descendant.endByte()
	isEmpty := startByte == endByte

	for {
		it := self.iterate()
		for {
			next, ok := it.next()
			if !ok || next.startByte() > startByte {
				return Node{}
			}
			self = next
			if self.id == descendant.id {
				return self
			}

			// If the descendant is empty, and the end byte is within self, check whether self
			// contains it or not.
			if isEmpty && it.position.bytes >= endByte && self.childCount() > 0 {
				child := childWithDescendant(self, descendant)
				if !child.isNull() {
					if self.isRelevant() {
						return self
					}
					return child
				}
			}

			var again bool
			if isEmpty {
				again = it.position.bytes <= endByte
			} else {
				again = it.position.bytes < endByte
			}
			if !(again || self.childCount() == 0) {
				break
			}
		}
		if self.isRelevant() {
			return self
		}
	}
}

// Parent is ts_node_parent: the visible node that contains this one, or a null node for the root.
func (n Node) parent() Node {
	node := n.tree.Root()
	if node.id == n.id {
		return Node{}
	}
	for {
		next := childWithDescendant(node, n)
		if next.id == n.id || next.isNull() {
			break
		}
		node = next
	}
	return node
}

// PrevSibling is ts_node_prev_sibling: the node immediately before this one under the same
// parent, named or not. ok is false when there is none.
func (n Node) PrevSibling() (Node, bool) {
	r := n.prevSibling()
	return r, !r.isNull()
}

// prevSibling is ts_node__prev_sibling(self, include_anonymous = true).
func (self Node) prevSibling() Node {
	selfIsEmpty := self.sub.totalBytes() == 0
	targetEndByte := self.endByte()

	node := self.parent()
	var earlierNode Node
	earlierNodeIsRelevant := false

	for !node.isNull() {
		var earlierChild Node
		earlierChildIsRelevant := false
		foundChildContainingTarget := false

		var child Node
		it := node.iterate()
		for {
			c, ok := it.next()
			if !ok {
				break
			}
			child = c
			if child.id == self.id {
				break
			}
			if it.position.bytes > targetEndByte {
				foundChildContainingTarget = true
				break
			}

			if it.position.bytes == targetEndByte &&
				(!selfIsEmpty || subtreeHasTrailingEmptyDescendant(child.sub, self.sub)) {
				foundChildContainingTarget = true
				break
			}

			if child.isRelevant() {
				earlierChild = child
				earlierChildIsRelevant = true
			} else if child.childCount() > 0 {
				earlierChild = child
				earlierChildIsRelevant = false
			}
		}

		switch {
		case foundChildContainingTarget:
			if !earlierChild.isNull() {
				earlierNode = earlierChild
				earlierNodeIsRelevant = earlierChildIsRelevant
			}
			node = child
		case earlierChildIsRelevant:
			return earlierChild
		case !earlierChild.isNull():
			node = earlierChild
		case earlierNodeIsRelevant:
			return earlierNode
		default:
			node = earlierNode
			earlierNode = Node{}
			earlierNodeIsRelevant = false
		}
	}
	return Node{}
}
