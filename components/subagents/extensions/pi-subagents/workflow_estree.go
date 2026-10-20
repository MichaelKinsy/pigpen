package pi_subagents

import (
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/file"
	"github.com/dop251/goja/token"
)

// The original validates workflow scripts over an acorn (ESTree) syntax tree (scripted-workflow.ts:1538-2012). The Go port
// parses with goja's parser, whose tree has other node types, so this file rebuilds the ESTree shape the validator reads
// (node types, child property names and their order, loc.start as an acorn line and UTF-16 column) from the goja tree.

// esField is one own property of an ESTree node, in the order acorn assigns it.
// V is a *esNode, a []*esNode (nil entries are holes), a string, a bool, a float64, *big.Int or nil.
type esField struct {
	Key string
	V   any
}

// esNode is an ESTree node with acorn's loc.start.
type esNode struct {
	Type   string
	Line   int // 1-based
	Column int // 0-based, UTF-16 code units
	Fields []esField
}

func (n *esNode) get(key string) (any, bool) { panic("not implemented") }

func (n *esNode) node(key string) *esNode { panic("not implemented") }

func (n *esNode) list(key string) []*esNode { panic("not implemented") }

func (n *esNode) str(key string) (string, bool) { panic("not implemented") }

func (n *esNode) flag(key string) bool { panic("not implemented") }

func (n *esNode) set(key string, v any) *esNode { panic("not implemented") }

// esBuilder converts one parsed program; src is the exact text that was parsed (the goja idx base is 1).
type esBuilder struct {
	src       string
	lineStart []int // byte offset of each line start
}

func newESBuilder(src string) *esBuilder { panic("not implemented") }

// U+2028 and U+2029 end a line in acorn too

func (b *esBuilder) pos(idx file.Idx) (line, col int) { panic("not implemented") }

func (b *esBuilder) mk(typ string, idx file.Idx) *esNode { panic("not implemented") }

func (b *esBuilder) exprs(list []ast.Expression) []*esNode { panic("not implemented") }

func (b *esBuilder) stmts(list []ast.Statement) []*esNode { panic("not implemented") }

func tokenOp(t token.Token) string { panic("not implemented") }

// expr converts an expression; nil in, nil out.
func (b *esBuilder) expr(e ast.Expression) *esNode { panic("not implemented") }

// goja keeps the arithmetic operator of `+=`, `||=` ...; ESTree names the assignment operator

// esTemplateValue is TemplateElement.value.
type esTemplateValue struct {
	Raw    string
	Cooked any
}

// binding converts a Binding as an assignment target: the target, or an AssignmentPattern when it has an initializer.
func (b *esBuilder) binding(x *ast.Binding) *esNode { panic("not implemented") }

func (b *esBuilder) property(p ast.Property) *esNode { panic("not implemented") }

// acorn starts the function of an accessor or method at its parameter list

func (b *esBuilder) functionStart(x *ast.FunctionLiteral) file.Idx {
	panic(
		// goja records the `function` keyword; acorn starts an async function at `async`.
		"not implemented")
}

func (b *esBuilder) params(pl *ast.ParameterList) ([]*esNode, bool) { panic("not implemented") }

func (b *esBuilder) function(n *esNode, x *ast.FunctionLiteral) *esNode { panic("not implemented") }

func (b *esBuilder) class(typ string, x *ast.ClassLiteral) *esNode { panic("not implemented") }

func (b *esBuilder) declarations(kind string, list []*ast.Binding, idx file.Idx) *esNode {
	panic("not implemented")
}

func (b *esBuilder) forInto(into ast.ForInto) *esNode { panic("not implemented") }

func lexKind(t token.Token) string { panic("not implemented") }

// stmt converts a statement; nil in, nil out. Declarations hoisted by goja (var lists) are not statements and are skipped.
func (b *esBuilder) stmt(s ast.Statement) *esNode { panic("not implemented") }

// program converts the whole parsed program to an ESTree Program.
func (b *esBuilder) program(p *ast.Program) *esNode { panic("not implemented") }
