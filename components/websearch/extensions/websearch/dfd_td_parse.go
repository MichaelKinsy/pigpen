package websearch

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// dfdParseHTMLDocument parses markup the way the Markdown stage's DOM does (domino in the original: the HTML
// parsing algorithm, no limit on the depth). x/net/html follows the same algorithm but refuses a tree nested more
// than 512 elements deep; such a page is parsed by the linkedom-like parser of ldom_parse.go instead and its tree is
// converted. The two builders agree on well-formed markup, which is what a page nested that deeply is made of; they
// disagree on the error recovery of tables and misnested formatting elements (port/PORT.md).
func dfdParseHTMLDocument(markup string) *html.Node {
	if doc, err := html.Parse(strings.NewReader(markup)); err == nil {
		return doc
	}
	return dfdFromLdom(ldParseDocument(markup))
}

// dfdFromLdom copies a tree of the linkedom-like DOM into x/net/html nodes without recursion, so that the depth that
// made x/net/html refuse the markup is no obstacle.
func dfdFromLdom(root *ldNode) *html.Node {
	out := &html.Node{Type: html.DocumentNode}
	type frame struct {
		src *ldNode
		dst *html.Node
	}
	stack := []frame{{root, out}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		var kids []frame
		for c := f.src.first; c != nil; c = c.next {
			var n *html.Node
			switch c.kind {
			case ldElem:
				n = &html.Node{Type: html.ElementNode, Data: c.name, DataAtom: atom.Lookup([]byte(c.name))}
				if c.svg {
					n.Namespace = "svg"
				}
				for _, a := range c.attrs {
					n.Attr = append(n.Attr, html.Attribute{Key: a.name, Val: a.value})
				}
			case ldText:
				n = &html.Node{Type: html.TextNode, Data: c.data}
			case ldComment:
				n = &html.Node{Type: html.CommentNode, Data: c.data}
			default:
				continue
			}
			f.dst.AppendChild(n)
			if c.kind == ldElem {
				kids = append(kids, frame{c, n})
			}
		}
		for i := len(kids) - 1; i >= 0; i-- {
			stack = append(stack, kids[i])
		}
	}
	return out
}
