package bashparse

import "unicode/utf16"

// NewFakeNode builds a node for testing the pure AST helpers without a parse
// (test/helpers/fake-ts-node.ts makeTSNode). It fills only the fields those helpers read:
// children drive ChildCount and Child, the node sits at offset zero so its span is its own text,
// and it reports no parse error and no preceding sibling. Prefer a real parse when the subject is
// the shape tree-sitter actually produces; use this when the subject is the helper's behavior
// given a shape.
func NewFakeNode(typ, text string, children ...*Node) *Node {
	src := newSource(text)
	return &Node{
		typ:      typ,
		named:    true,
		start:    0,
		end:      len(utf16.Encode([]rune(src.text))),
		src:      src,
		children: children,
	}
}
