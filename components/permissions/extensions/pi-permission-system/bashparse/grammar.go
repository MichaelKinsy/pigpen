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
	return &Tree{Root: convert(tree.Root(), src, nil, "")}
}

// convert copies a ts node into the package's own immutable node.
func convert(n ts.Node, src *Source, prev *Node, field string) *Node {
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
	kids := n.Children()
	out.children = make([]*Node, len(kids))
	for i, k := range kids {
		var before *Node
		if i > 0 {
			before = out.children[i-1]
		}
		out.children[i] = convert(k.Node, src, before, k.Field)
	}
	// The grammar's previousSibling is the node's own (ts_node_prev_sibling), which is the previous
	// visible sibling; it is checked, not assumed, so a zero-width case that breaks the
	// equivalence is replaced by the C's answer.
	for i, k := range kids {
		p, ok := k.Node.PrevSibling()
		var want *Node
		if ok {
			for j := 0; j < i; j++ {
				if kids[j].Node.Same(p) {
					want = out.children[j]
				}
			}
			if want == nil {
				for j := i + 1; j < len(kids); j++ {
					if kids[j].Node.Same(p) {
						want = out.children[j]
					}
				}
			}
		}
		out.children[i].prev = want
	}
	return out
}
