package bashparse

import (
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse/internal/ts"
)

// grammarParser is tree-sitter-bash 0.25.1's own parser: its trees are exactly what the grammar
// produced, run by the Go port of the tree-sitter 0.26.9 runtime in internal/ts.
//
// A parse is serialized on one ts.Parser, and the parser keeps the memory a parse used for the
// next one, so the converted tree is built before the parser is handed back. Parsers come from a
// pool: concurrent callers each get their own, and a pool the collector emptied costs one warm-up.
type grammarParser struct {
	pool sync.Pool
}

func newGrammarParser(lang *ts.Language) *grammarParser {
	g := &grammarParser{}
	g.pool.New = func() any { return &parseScratch{parser: ts.NewParser(lang)} }
	return g
}

func initGrammarParser() (Parser, error) {
	lang, err := ts.BashLanguage()
	if err != nil {
		return nil, err
	}
	return newGrammarParser(lang), nil
}

// parseScratch is what one parse reuses: the parser, the input in UTF-16 units, and the
// conversion's working space.
type parseScratch struct {
	parser *ts.Parser
	units  []uint16
	conv   converter
}

func (g *grammarParser) Parse(input string) *Tree {
	src := newSource(input)
	sc := g.pool.Get().(*parseScratch)
	sc.units = appendUTF16(sc.units[:0], src.text)
	tree := sc.parser.Parse(sc.units)
	var out *Tree
	if tree != nil {
		out = &Tree{Root: sc.conv.convert(tree, src)}
	}
	sc.parser.Recycle()
	sc.conv.release()
	if cap(sc.units) > maxRetainedUnits {
		sc.units = nil
	}
	g.pool.Put(sc)
	return out
}

// maxRetainedUnits bounds the input buffer a pooled parser keeps (UTF-16 units).
const maxRetainedUnits = 1 << 16

// appendUTF16 is utf16.Encode([]rune(text)) appended to dst, for text that is valid UTF-8.
func appendUTF16(dst []uint16, text string) []uint16 {
	for i := 0; i < len(text); {
		c := text[i]
		if c < utf8.RuneSelf {
			dst = append(dst, uint16(c))
			i++
			continue
		}
		r, w := utf8.DecodeRuneInString(text[i:])
		i += w
		if r >= 0x10000 {
			r1, r2 := utf16.EncodeRune(r)
			dst = append(dst, uint16(r1), uint16(r2))
		} else {
			dst = append(dst, uint16(r))
		}
	}
	return dst
}

// nodeRef is a converted node and the runtime node it came from.
type nodeRef struct {
	id  ts.NodeID
	out *Node
}

// converter copies a ts tree into the package's own immutable nodes. The nodes and the child
// pointer arrays come from two allocations sized from the tree (a tree has as many nodes as its
// root counts visible descendants), so a tree costs a handful of allocations, not one or two per node.
type converter struct {
	src   *Source
	nodes []Node
	ptrs  []*Node
	kids  []ts.Child // working stack of the children being converted
	refs  []nodeRef  // every converted node in pre-order, for the zero-width fix-up below
	byID  map[ts.NodeID]*Node
	live  bool // byID holds every node of refs
}

// convert is the node tree of a parse.
func (c *converter) convert(tree *ts.Tree, src *Source) *Node {
	c.src = src
	c.live = false
	n := tree.NodeCount()
	c.nodes = make([]Node, 0, n)
	c.ptrs = make([]*Node, 0, n)
	return c.node(tree.Root(), nil, "")
}

// release drops what the last tree pointed at.
func (c *converter) release() {
	c.src, c.nodes, c.ptrs = nil, nil, nil
	for i := range c.kids {
		c.kids[i] = ts.Child{}
	}
	c.kids = c.kids[:0]
	for i := range c.refs {
		c.refs[i] = nodeRef{}
	}
	if cap(c.refs) > maxRetainedRefs {
		c.refs = nil
	} else {
		c.refs = c.refs[:0]
	}
	if c.live {
		clear(c.byID)
		c.live = false
	}
}

const maxRetainedRefs = 1 << 15

func (c *converter) newNode() *Node {
	if len(c.nodes) == cap(c.nodes) {
		c.nodes = make([]Node, 0, 64)
	}
	c.nodes = c.nodes[:len(c.nodes)+1]
	return &c.nodes[len(c.nodes)-1]
}

func (c *converter) newPtrs(n int) []*Node {
	if cap(c.ptrs)-len(c.ptrs) < n {
		size := 64
		if n > size {
			size = n
		}
		c.ptrs = make([]*Node, 0, size)
	}
	l := len(c.ptrs)
	c.ptrs = c.ptrs[:l+n]
	return c.ptrs[l : l+n : l+n]
}

// makeLive indexes every node converted so far by its runtime identity.
func (c *converter) makeLive() {
	if c.byID == nil {
		c.byID = make(map[ts.NodeID]*Node, len(c.refs))
	}
	for _, r := range c.refs {
		c.byID[r.id] = r.out
	}
	c.live = true
}

func (c *converter) node(n ts.Node, prev *Node, field string) *Node {
	d := n.Data()
	out := c.newNode()
	*out = Node{
		typ:      d.Type,
		named:    d.Named,
		hasError: d.HasError,
		missing:  d.Missing,
		extra:    d.Extra,
		start:    d.Start,
		end:      d.End,
		src:      c.src,
		prev:     prev,
		field:    field,
	}
	id := n.ID()
	c.refs = append(c.refs, nodeRef{id, out})
	if c.live {
		c.byID[id] = out
	}
	base := len(c.kids)
	c.kids = n.AppendChildren(c.kids)
	count := len(c.kids) - base
	out.children = c.newPtrs(count)
	for i := 0; i < count; i++ {
		var before *Node
		if i > 0 {
			before = out.children[i-1]
		}
		k := c.kids[base+i]
		out.children[i] = c.node(k.Node, before, k.Field)
	}
	// The previous sibling of a node that has width is the child before it: ts_node_prev_sibling
	// returns the last visible node ahead of it under the same parent, and nothing but a
	// zero-width node can sit anywhere else. A zero-width node (a MISSING token, an empty scanner
	// token) is asked of the runtime's own algorithm, which locates its parent by position and
	// resolves ties between zero-width neighbours as the C does.
	for i := 0; i < count; i++ {
		if out.children[i].end > out.children[i].start {
			continue // out.children[i].prev is already children[i-1]
		}
		// The runtime's answer need not be a sibling: for a zero-width node it can be a descendant
		// of the sibling before it (two MISSING tokens recovered at one position are the same
		// subtree to ts_subtree_has_trailing_empty_descendant), and the original reports exactly
		// that. Everything before this node in pre-order is already converted.
		if !c.live {
			c.makeLive()
		}
		var want *Node
		if p, ok := c.kids[base+i].Node.PrevSibling(); ok {
			want = c.byID[p.ID()]
		}
		out.children[i].prev = want
	}
	c.kids = c.kids[:base]
	return out
}
