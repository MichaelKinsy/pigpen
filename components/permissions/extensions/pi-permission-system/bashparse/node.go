// Package bashparse parses bash the way the original extension does: tree-sitter-bash 0.25.1, the
// two corrections `getParser` applies (heredoc tails, redirect arguments), and the health
// predicates every walker asks of the tree. Port of access-intent/bash/{parser,parse-view,
// parse-health,heredoc-tails,redirect-arguments,heredoc-free-lines,node-text,async-cache}.ts of
// @gotgenes/pi-permission-system 39.0.3.
//
// # Indices
//
// The original parses a JavaScript string, and web-tree-sitter reports startIndex/endIndex in
// UTF-16 code units of that string. [Node.StartIndex] and [Node.EndIndex] are therefore UTF-16
// code-unit offsets, so ported JS string logic (`text.slice(a, b)`, `index - offset`) keeps its
// numbers. [Node.StartByte] and [Node.EndByte] are the same positions as UTF-8 byte offsets into
// [Node.Source], for Go slicing; [Source.Slice] slices by UTF-16 indices.
//
// A Go string holding invalid UTF-8 cannot be a JavaScript string; [Parse] reads each invalid
// byte as one U+FFFD (one UTF-16 unit), and [Node.Text] reports the sanitized text.
package bashparse

import (
	"strings"
	"unicode/utf8"
)

// Source is the text a tree was parsed from, with its UTF-16 index mapping.
type Source struct {
	text string
	// units maps a UTF-8 byte offset to the UTF-16 code-unit index of the character starting
	// there (len(text)+1 entries); nil when the text is ASCII, where both are the same.
	units []int32
	// byteAt is the inverse for a non-ASCII text: byteAt[utf16Index] = byte offset (len = units+1).
	byteAt []int32
}

// newSource sanitizes text to valid UTF-8 (each invalid byte becomes U+FFFD, as a JS string would
// hold it) and builds the index mapping.
func newSource(text string) *Source {
	ascii := true
	for i := 0; i < len(text); i++ {
		if text[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return &Source{text: text}
	}
	if !utf8.ValidString(text) {
		var b strings.Builder
		for i := 0; i < len(text); {
			r, w := utf8.DecodeRuneInString(text[i:])
			if r == utf8.RuneError && w == 1 {
				b.WriteRune(utf8.RuneError)
			} else {
				b.WriteString(text[i : i+w])
			}
			i += w
		}
		text = b.String()
	}
	s := &Source{text: text, units: make([]int32, len(text)+1)}
	unit := int32(0)
	for i := 0; i < len(text); {
		r, w := utf8.DecodeRuneInString(text[i:])
		for j := 0; j < w; j++ {
			s.units[i+j] = unit
		}
		s.byteAt = append(s.byteAt, int32(i))
		if r >= 0x10000 {
			s.byteAt = append(s.byteAt, int32(i))
			unit += 2
		} else {
			unit++
		}
		i += w
	}
	s.units[len(text)] = unit
	s.byteAt = append(s.byteAt, int32(len(text)))
	return s
}

// Text is the whole parsed text.
func (s *Source) Text() string { return s.text }

// UTF16Len is the length of the text in UTF-16 code units (JavaScript's `.length`).
func (s *Source) UTF16Len() int { return s.index(len(s.text)) }

// index converts a UTF-8 byte offset to a UTF-16 index.
func (s *Source) index(byteOffset int) int {
	if s.units == nil {
		return byteOffset
	}
	return int(s.units[byteOffset])
}

// ByteOffset converts a UTF-16 index to a UTF-8 byte offset. An index inside a surrogate pair
// maps to the start of the character.
func (s *Source) ByteOffset(index int) int {
	if s.byteAt == nil {
		return index
	}
	return int(s.byteAt[index])
}

// Slice is JavaScript's text.slice(start, end) for UTF-16 indices already within range.
func (s *Source) Slice(start, end int) string {
	return s.text[s.ByteOffset(start):s.ByteOffset(end)]
}

// Node mirrors the original's TSNode (parser.ts): the subset of web-tree-sitter's SyntaxNode the
// walkers read, for a grammar node or for a node a correction built (a "view"). Nodes are
// immutable after construction except for the previous-sibling link a correction sets when it
// adopts a node. A nil *Node is JavaScript's `null`.
type Node struct {
	typ      string
	named    bool
	hasError bool
	missing  bool
	extra    bool
	start    int // UTF-16 code-unit indices into the parsed text, like web-tree-sitter's
	end      int
	src      *Source
	children []*Node
	prev     *Node
	field    string
	view     bool // built by a correction (the original's NodeView) rather than copied from the grammar
}

// Type is the grammar's node type ("command", "word", "ERROR", an operator's own text, ...).
func (n *Node) Type() string { return n.typ }

// Text is the source slice [StartIndex, EndIndex): JavaScript's node.text.
func (n *Node) Text() string { return n.src.Slice(n.start, n.end) }

// StartIndex is the UTF-16 code-unit offset of the node's start in the parsed text.
func (n *Node) StartIndex() int { return n.start }

// EndIndex is the UTF-16 code-unit offset one past the node's end.
func (n *Node) EndIndex() int { return n.end }

// StartByte is StartIndex as a UTF-8 byte offset into [Node.Source]'s text.
func (n *Node) StartByte() int { return n.src.ByteOffset(n.start) }

// EndByte is EndIndex as a UTF-8 byte offset into [Node.Source]'s text.
func (n *Node) EndByte() int { return n.src.ByteOffset(n.end) }

// Source is the text the node's tree was parsed from.
func (n *Node) Source() *Source { return n.src }

// ChildCount is the number of children, named and anonymous.
func (n *Node) ChildCount() int { return len(n.children) }

// Child is child(index): nil when out of range.
func (n *Node) Child(index int) *Node {
	if index < 0 || index >= len(n.children) {
		return nil
	}
	return n.children[index]
}

// Children are all the children in order. The slice is shared; callers must not modify it.
func (n *Node) Children() []*Node { return n.children }

// IsNamed is false for anonymous tokens (operators, delimiters).
func (n *Node) IsNamed() bool { return n.named }

// HasError is true when this node is an error or missing token, or contains one. A node built by
// a correction reports what the parse said of the node it stands for, or false for a node the
// correction made from a resolved statement.
func (n *Node) HasError() bool { return n.hasError }

// IsMissing is true for a token the parser inserted to recover.
func (n *Node) IsMissing() bool { return n.missing }

// IsExtra is true for a node the grammar allows anywhere (comments).
func (n *Node) IsExtra() bool { return n.extra }

// PreviousSibling is the node immediately before this one under the same parent, named or not.
func (n *Node) PreviousSibling() *Node { return n.prev }

// FieldName is the grammar field name the parent gives this node, or "".
func (n *Node) FieldName() string { return n.field }

// Tree is a parse result. JavaScript's `delete()` has no Go counterpart: nodes are garbage
// collected.
type Tree struct {
	Root *Node
}

// Parser is the original's BashReparser: the one parse capability a consumer needs to re-parse a
// fragment of a command on its own. Parse returns nil when no tree could be produced.
type Parser interface {
	Parse(input string) *Tree
}

