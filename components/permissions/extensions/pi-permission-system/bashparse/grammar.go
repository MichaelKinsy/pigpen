package bashparse

import (
	"sync"
	"unicode/utf16"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse/internal/ts"
)

// grammarParser is tree-sitter-bash 0.25.1's own parser: its trees are exactly what the grammar
// produced, run by the Go port of the tree-sitter 0.26.9 runtime in internal/ts.
type grammarParser struct {
	mu     sync.Mutex
	parser *ts.Parser
}

func initGrammarParser() (Parser, error) {
	lang, err := ts.BashLanguage()
	if err != nil {
		return nil, err
	}
	return &grammarParser{parser: ts.NewParser(lang)}, nil
}

func (g *grammarParser) Parse(input string) *Tree {
	src := newSource(input)
	units := utf16.Encode([]rune(src.text))
	g.mu.Lock()
	tree := g.parser.Parse(units)
	g.mu.Unlock()
	if tree == nil {
		return nil
	}
	return &Tree{Root: convert(tree.Root(), src, nil, "", map[ts.NodeID]*Node{})}
}

// convert copies a ts node into the package's own immutable node.
func convert(n ts.Node, src *Source, prev *Node, field string, byID map[ts.NodeID]*Node) *Node {
	d := n.Data()
	out := &Node{
		typ:      d.Type,
		named:    d.Named,
		hasError: d.HasError,
		missing:  d.Missing,
		extra:    d.Extra,
		start:    d.Start,
		end:      d.End,
		src:      src,
		prev:     prev,
		field:    field,
	}
	byID[n.ID()] = out
	kids := n.Children()
	out.children = make([]*Node, len(kids))
	for i, k := range kids {
		var before *Node
		if i > 0 {
			before = out.children[i-1]
		}
		out.children[i] = convert(k.Node, src, before, k.Field, byID)
	}
	// The previous sibling of a node that has width is the child before it: ts_node_prev_sibling
	// returns the last visible node ahead of it under the same parent, and nothing but a
	// zero-width node can sit anywhere else. A zero-width node (a MISSING token, an empty scanner
	// token) is asked of the runtime's own algorithm, which locates its parent by position and
	// resolves ties between zero-width neighbours as the C does.
	for i, k := range kids {
		if d := k.Node.Data(); d.End > d.Start {
			continue // out.children[i].prev is already children[i-1]
		}
		// The runtime's answer need not be a sibling: for a zero-width node it can be a descendant
		// of the sibling before it (two MISSING tokens recovered at one position are the same
		// subtree to ts_subtree_has_trailing_empty_descendant), and the original reports exactly
		// that. Everything before this node in pre-order is already converted.
		var want *Node
		if p, ok := k.Node.PrevSibling(); ok {
			want = byID[p.ID()]
		}
		out.children[i].prev = want
	}
	return out
}
