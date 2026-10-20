package bashparse

import (
	"sync"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// grammarParser is tree-sitter-bash's own parser: its trees are exactly what the grammar produced.
type grammarParser struct {
	mu   sync.Mutex
	lang *gotreesitter.Language
	ts   *gotreesitter.Parser
}

func initGrammarParser() (Parser, error) {
	lang := grammars.BashLanguage()
	return &grammarParser{lang: lang, ts: gotreesitter.NewParser(lang)}, nil
}

func (g *grammarParser) Parse(input string) *Tree {
	src := newSource(input)
	g.mu.Lock()
	defer g.mu.Unlock()
	tree, err := g.ts.Parse([]byte(src.text))
	if err != nil || tree == nil {
		return nil
	}
	defer tree.Release()
	root := g.convert(tree.RootNode(), src, nil, "")
	return &Tree{Root: root}
}

// convert copies a gotreesitter node into the package's own immutable node.
func (g *grammarParser) convert(n *gotreesitter.Node, src *Source, prev *Node, field string) *Node {
	out := &Node{
		typ:      n.Type(g.lang),
		named:    n.IsNamed(),
		hasError: n.HasError(),
		missing:  n.IsMissing(),
		extra:    n.IsExtra(),
		start:    int(n.StartByte()),
		end:      int(n.EndByte()),
		src:      src,
		prev:     prev,
		field:    field,
	}
	count := n.ChildCount()
	out.children = make([]*Node, 0, count)
	var before *Node
	for i := 0; i < count; i++ {
		child := g.convert(n.Child(i), src, before, n.FieldNameForChild(i, g.lang))
		out.children = append(out.children, child)
		before = child
	}
	return out
}
