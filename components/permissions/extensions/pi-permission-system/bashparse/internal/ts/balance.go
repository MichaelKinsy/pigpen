package ts

// Port of ts_parser__balance_subtree and ts_subtree_compress. Balancing rotates the hidden
// repetition nodes of the finished tree so that a long list is not a deeply left-leaning chain. The
// node API flattens hidden nodes, so no visible node changes; but where the hidden nodes sit decides
// what ts_subtree_has_trailing_empty_descendant sees, and with it the previous sibling of a
// zero-width node, so the rotation is reproduced.
//
// Both only touch a subtree that has exactly one owner (ref_count == 1), so the references are
// counted here over the finished tree: one per parent slot, and one for the root, which the parser
// holds. By then the parse stack has been cleared (ts_parser_parse does it before balancing), so
// nothing else holds a non-leaf subtree.

// refCounts counts, for every non-leaf subtree of the tree, how many places reference it.
func refCounts(root *subtree) map[*subtree]int {
	refs := map[*subtree]int{root: 1}
	stack := []*subtree{root}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, c := range s.children {
			if len(c.children) == 0 {
				continue
			}
			if refs[c] == 0 {
				stack = append(stack, c)
			}
			refs[c]++
		}
	}
	return refs
}

// balance is ts_parser__balance_subtree.
func balance(l *Language, root *subtree) {
	refs := refCounts(root)
	var stack []*subtree
	if len(root.children) > 0 && refs[root] == 1 {
		stack = append(stack, root)
	}
	for len(stack) > 0 {
		tree := stack[len(stack)-1]

		if tree.repeatDepth > 0 {
			child1 := tree.children[0]
			child2 := tree.children[len(tree.children)-1]
			repeatDelta := int(child1.repeatDepthOf()) - int(child2.repeatDepthOf())
			if repeatDelta > 0 {
				n := uint(repeatDelta)
				for i := n / 2; i > 0; i /= 2 {
					compress(l, tree, i, refs)
					n -= i
				}
			}
		}

		stack = stack[:len(stack)-1]

		for _, child := range tree.children {
			if len(child.children) > 0 && refs[child] == 1 {
				stack = append(stack, child)
			}
		}
	}
}

// own gives s a children array no other subtree shares, so it can be rewritten in place.
func own(s *subtree) { s.children = append([]*subtree(nil), s.children...) }

// compress is ts_subtree_compress.
func compress(l *Language, self *subtree, count uint, refs map[*subtree]int) {
	var stack []*subtree

	tree := self
	symbol := tree.symbol
	for i := uint(0); i < count; i++ {
		if refs[tree] > 1 || len(tree.children) < 2 {
			break
		}

		child := tree.children[0]
		if len(child.children) < 2 || refs[child] > 1 || child.symbol != symbol {
			break
		}

		grandchild := child.children[0]
		if len(grandchild.children) < 2 || refs[grandchild] > 1 || grandchild.symbol != symbol {
			break
		}

		own(tree)
		own(child)
		own(grandchild)
		tree.children[0] = grandchild
		child.children[0] = grandchild.children[len(grandchild.children)-1]
		grandchild.children[len(grandchild.children)-1] = child
		stack = append(stack, tree)
		tree = grandchild
	}

	for len(stack) > 0 {
		tree = stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		child := tree.children[0]
		grandchild := child.children[len(child.children)-1]
		grandchild.summarizeChildren(l)
		child.summarizeChildren(l)
		tree.summarizeChildren(l)
	}
}
