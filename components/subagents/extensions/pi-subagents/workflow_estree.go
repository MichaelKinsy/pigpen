package pi_subagents

import (
	"math/big"
	"strings"
	"unicode/utf16"

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

func (n *esNode) get(key string) (any, bool) {
	for _, f := range n.Fields {
		if f.Key == key {
			return f.V, true
		}
	}
	return nil, false
}

func (n *esNode) node(key string) *esNode {
	v, _ := n.get(key)
	x, _ := v.(*esNode)
	return x
}

func (n *esNode) list(key string) []*esNode {
	v, _ := n.get(key)
	x, _ := v.([]*esNode)
	return x
}

func (n *esNode) str(key string) (string, bool) {
	v, _ := n.get(key)
	s, ok := v.(string)
	return s, ok
}

func (n *esNode) flag(key string) bool {
	v, _ := n.get(key)
	b, _ := v.(bool)
	return b
}

func (n *esNode) set(key string, v any) *esNode {
	n.Fields = append(n.Fields, esField{key, v})
	return n
}

// esBuilder converts one parsed program; src is the exact text that was parsed (the goja idx base is 1).
type esBuilder struct {
	src       string
	lineStart []int // byte offset of each line start
}

func newESBuilder(src string) *esBuilder {
	b := &esBuilder{src: src, lineStart: []int{0}}
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\n':
			b.lineStart = append(b.lineStart, i+1)
		case '\r':
			if i+1 < len(src) && src[i+1] == '\n' {
				i++
			}
			b.lineStart = append(b.lineStart, i+1)
		case 0xE2: // U+2028 and U+2029 end a line in acorn too
			if i+2 < len(src) && src[i+1] == 0x80 && (src[i+2] == 0xA8 || src[i+2] == 0xA9) {
				b.lineStart = append(b.lineStart, i+3)
				i += 2
			}
		}
	}
	return b
}

func (b *esBuilder) pos(idx file.Idx) (line, col int) {
	off := int(idx) - 1
	if off < 0 {
		off = 0
	}
	if off > len(b.src) {
		off = len(b.src)
	}
	lo, hi := 0, len(b.lineStart)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if b.lineStart[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1, len(utf16.Encode([]rune(b.src[b.lineStart[lo]:off])))
}

func (b *esBuilder) mk(typ string, idx file.Idx) *esNode {
	l, c := b.pos(idx)
	return &esNode{Type: typ, Line: l, Column: c}
}

func (b *esBuilder) exprs(list []ast.Expression) []*esNode {
	out := make([]*esNode, len(list))
	for i, e := range list {
		out[i] = b.expr(e)
	}
	return out
}

func (b *esBuilder) stmts(list []ast.Statement) []*esNode {
	out := make([]*esNode, 0, len(list))
	for _, s := range list {
		if n := b.stmt(s); n != nil {
			out = append(out, n)
		}
	}
	return out
}

func tokenOp(t token.Token) string { return t.String() }

// expr converts an expression; nil in, nil out.
func (b *esBuilder) expr(e ast.Expression) *esNode {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ast.Identifier:
		return b.mk("Identifier", x.Idx).set("name", x.Name.String())
	case *ast.PrivateIdentifier:
		return b.mk("PrivateIdentifier", x.Idx).set("name", x.Name.String())
	case *ast.StringLiteral:
		return b.mk("Literal", x.Idx).set("value", x.Value.String()).set("raw", x.Literal)
	case *ast.NumberLiteral:
		n := b.mk("Literal", x.Idx)
		switch v := x.Value.(type) {
		case *big.Int:
			return n.set("value", nil).set("raw", x.Literal).set("bigint", v.String())
		case int64:
			return n.set("value", float64(v)).set("raw", x.Literal)
		case float64:
			return n.set("value", v).set("raw", x.Literal)
		}
		return n.set("value", nil).set("raw", x.Literal)
	case *ast.BooleanLiteral:
		return b.mk("Literal", x.Idx).set("value", x.Value).set("raw", x.Literal)
	case *ast.NullLiteral:
		return b.mk("Literal", x.Idx).set("value", nil).set("raw", x.Literal)
	case *ast.RegExpLiteral:
		return b.mk("Literal", x.Idx).set("value", nil).set("raw", x.Literal).set("regex", true)
	case *ast.ThisExpression:
		return b.mk("ThisExpression", x.Idx)
	case *ast.SuperExpression:
		return b.mk("Super", x.Idx)
	case *ast.ArrayLiteral:
		return b.mk("ArrayExpression", x.LeftBracket).set("elements", b.exprs(x.Value))
	case *ast.ObjectLiteral:
		props := make([]*esNode, len(x.Value))
		for i, p := range x.Value {
			props[i] = b.property(p)
		}
		return b.mk("ObjectExpression", x.LeftBrace).set("properties", props)
	case *ast.SpreadElement:
		return b.mk("SpreadElement", x.Idx0()).set("argument", b.expr(x.Expression))
	case *ast.CallExpression:
		return b.mk("CallExpression", x.Idx0()).set("callee", b.expr(x.Callee)).set("arguments", b.exprs(x.ArgumentList)).set("optional", false)
	case *ast.NewExpression:
		return b.mk("NewExpression", x.New).set("callee", b.expr(x.Callee)).set("arguments", b.exprs(x.ArgumentList))
	case *ast.DotExpression:
		return b.mk("MemberExpression", x.Idx0()).set("object", b.expr(x.Left)).set("property", b.expr(&x.Identifier)).set("computed", false).set("optional", false)
	case *ast.PrivateDotExpression:
		return b.mk("MemberExpression", x.Idx0()).set("object", b.expr(x.Left)).set("property", b.expr(&x.Identifier)).set("computed", false).set("optional", false)
	case *ast.BracketExpression:
		return b.mk("MemberExpression", x.Idx0()).set("object", b.expr(x.Left)).set("property", b.expr(x.Member)).set("computed", true).set("optional", false)
	case *ast.OptionalChain:
		inner := b.expr(x.Expression)
		return b.mk("ChainExpression", x.Idx0()).set("expression", inner)
	case *ast.Optional:
		n := b.expr(x.Expression)
		if n != nil {
			for i := range n.Fields {
				if n.Fields[i].Key == "optional" {
					n.Fields[i].V = true
				}
			}
		}
		return n
	case *ast.AssignExpression:
		typ := "AssignmentExpression"
		n := b.mk(typ, x.Idx0())
		op := tokenOp(x.Operator)
		if x.Operator != token.ASSIGN {
			op += "=" // goja keeps the arithmetic operator of `+=`, `||=` ...; ESTree names the assignment operator
		}
		return n.set("operator", op).set("left", b.expr(x.Left)).set("right", b.expr(x.Right))
	case *ast.BinaryExpression:
		typ := "BinaryExpression"
		switch x.Operator {
		case token.LOGICAL_AND, token.LOGICAL_OR, token.COALESCE:
			typ = "LogicalExpression"
		}
		return b.mk(typ, x.Idx0()).set("left", b.expr(x.Left)).set("operator", tokenOp(x.Operator)).set("right", b.expr(x.Right))
	case *ast.UnaryExpression:
		switch x.Operator {
		case token.INCREMENT, token.DECREMENT:
			return b.mk("UpdateExpression", x.Idx0()).set("operator", tokenOp(x.Operator)).set("prefix", !x.Postfix).set("argument", b.expr(x.Operand))
		}
		return b.mk("UnaryExpression", x.Idx0()).set("operator", tokenOp(x.Operator)).set("prefix", true).set("argument", b.expr(x.Operand))
	case *ast.ConditionalExpression:
		return b.mk("ConditionalExpression", x.Idx0()).set("test", b.expr(x.Test)).set("consequent", b.expr(x.Consequent)).set("alternate", b.expr(x.Alternate))
	case *ast.SequenceExpression:
		return b.mk("SequenceExpression", x.Idx0()).set("expressions", b.exprs(x.Sequence))
	case *ast.AwaitExpression:
		return b.mk("AwaitExpression", x.Await).set("argument", b.expr(x.Argument))
	case *ast.YieldExpression:
		return b.mk("YieldExpression", x.Yield).set("delegate", x.Delegate).set("argument", b.expr(x.Argument))
	case *ast.TemplateLiteral:
		quasis := make([]*esNode, len(x.Elements))
		for i, el := range x.Elements {
			q := b.mk("TemplateElement", el.Idx)
			cooked := any(nil)
			if el.Valid {
				cooked = el.Parsed.String()
			}
			quasis[i] = q.set("value", &esTemplateValue{Raw: el.Literal, Cooked: cooked})
		}
		tl := b.mk("TemplateLiteral", x.OpenQuote).set("expressions", b.exprs(x.Expressions)).set("quasis", quasis)
		if x.Tag != nil {
			return b.mk("TaggedTemplateExpression", x.Tag.Idx0()).set("tag", b.expr(x.Tag)).set("quasi", tl)
		}
		return tl
	case *ast.FunctionLiteral:
		n := b.mk("FunctionExpression", b.functionStart(x))
		return b.function(n, x)
	case *ast.ArrowFunctionLiteral:
		n := b.mk("ArrowFunctionExpression", x.Start)
		n.set("id", nil)
		params, _ := b.params(x.ParameterList)
		n.set("params", params)
		switch body := x.Body.(type) {
		case *ast.ExpressionBody:
			n.set("body", b.expr(body.Expression)).set("expression", true)
		case *ast.BlockStatement:
			n.set("body", b.stmt(body)).set("expression", false)
		}
		return n.set("generator", false).set("async", x.Async)
	case *ast.ClassLiteral:
		return b.class("ClassExpression", x)
	case *ast.MetaProperty:
		return b.mk("MetaProperty", x.Idx).set("meta", b.expr(x.Meta)).set("property", b.expr(x.Property))
	case *ast.ObjectPattern:
		props := make([]*esNode, 0, len(x.Properties)+1)
		for _, p := range x.Properties {
			props = append(props, b.property(p))
		}
		if x.Rest != nil {
			props = append(props, b.mk("RestElement", x.Rest.Idx0()).set("argument", b.expr(x.Rest)))
		}
		return b.mk("ObjectPattern", x.LeftBrace).set("properties", props)
	case *ast.ArrayPattern:
		els := b.exprs(x.Elements)
		if x.Rest != nil {
			els = append(els, b.mk("RestElement", x.Rest.Idx0()).set("argument", b.expr(x.Rest)))
		}
		return b.mk("ArrayPattern", x.LeftBracket).set("elements", els)
	case *ast.Binding:
		return b.binding(x)
	case *ast.BadExpression:
		return b.mk("Identifier", x.From).set("name", "")
	}
	return nil
}

// esTemplateValue is TemplateElement.value.
type esTemplateValue struct {
	Raw    string
	Cooked any
}

// binding converts a Binding as an assignment target: the target, or an AssignmentPattern when it has an initializer.
func (b *esBuilder) binding(x *ast.Binding) *esNode {
	target := b.expr(x.Target)
	if x.Initializer == nil {
		return target
	}
	n := b.mk("AssignmentPattern", x.Target.Idx0())
	return n.set("left", target).set("right", b.expr(x.Initializer))
}

func (b *esBuilder) property(p ast.Property) *esNode {
	switch x := p.(type) {
	case *ast.PropertyShort:
		key := b.expr(&x.Name)
		var value *esNode
		if x.Initializer != nil {
			value = b.mk("AssignmentPattern", x.Name.Idx).set("left", b.expr(&x.Name)).set("right", b.expr(x.Initializer))
		} else {
			value = b.expr(&x.Name)
		}
		return b.mk("Property", x.Name.Idx).set("method", false).set("shorthand", true).set("computed", false).set("key", key).set("kind", "init").set("value", value)
	case *ast.PropertyKeyed:
		kind := "init"
		method := false
		switch x.Kind {
		case ast.PropertyKindGet:
			kind = "get"
		case ast.PropertyKindSet:
			kind = "set"
		case ast.PropertyKindMethod:
			method = true
		}
		value := b.expr(x.Value)
		if value != nil && (kind != "init" || method) && value.Type == "FunctionExpression" {
			// acorn starts the function of an accessor or method at its parameter list
			if fl, ok := x.Value.(*ast.FunctionLiteral); ok && fl.ParameterList != nil {
				value.Line, value.Column = b.pos(fl.ParameterList.Opening)
			}
		}
		return b.mk("Property", x.Key.Idx0()).set("method", method).set("shorthand", false).set("computed", x.Computed).set("key", b.expr(x.Key)).set("value", value).set("kind", kind)
	case *ast.SpreadElement:
		return b.expr(x)
	}
	return nil
}

func (b *esBuilder) functionStart(x *ast.FunctionLiteral) file.Idx {
	// goja records the `function` keyword; acorn starts an async function at `async`.
	if x.Async {
		if i := strings.LastIndex(b.src[:max(0, int(x.Function)-1)], "async"); i >= 0 && strings.TrimSpace(b.src[i+len("async"):max(0, int(x.Function)-1)]) == "" {
			return file.Idx(i + 1)
		}
	}
	return x.Function
}

func (b *esBuilder) params(pl *ast.ParameterList) ([]*esNode, bool) {
	if pl == nil {
		return nil, false
	}
	out := make([]*esNode, 0, len(pl.List)+1)
	for _, p := range pl.List {
		out = append(out, b.binding(p))
	}
	if pl.Rest != nil {
		out = append(out, b.mk("RestElement", pl.Rest.Idx0()).set("argument", b.expr(pl.Rest)))
	}
	return out, true
}

func (b *esBuilder) function(n *esNode, x *ast.FunctionLiteral) *esNode {
	var id *esNode
	if x.Name != nil {
		id = b.expr(x.Name)
	}
	params, _ := b.params(x.ParameterList)
	n.set("id", id).set("expression", false).set("generator", x.Generator).set("async", x.Async).set("params", params)
	var body *esNode
	if x.Body != nil {
		body = b.stmt(x.Body)
	}
	return n.set("body", body)
}

func (b *esBuilder) class(typ string, x *ast.ClassLiteral) *esNode {
	n := b.mk(typ, x.Class)
	var id *esNode
	if x.Name != nil {
		id = b.expr(x.Name)
	}
	body := b.mk("ClassBody", x.Class)
	elements := make([]*esNode, 0, len(x.Body))
	for _, el := range x.Body {
		switch e := el.(type) {
		case *ast.FieldDefinition:
			elements = append(elements, b.mk("PropertyDefinition", e.Idx).set("static", e.Static).set("computed", e.Computed).set("key", b.expr(e.Key)).set("value", b.expr(e.Initializer)))
		case *ast.MethodDefinition:
			start := e.Body.Function
			if e.Body.ParameterList != nil {
				start = e.Body.ParameterList.Opening
			}
			fn := b.function(b.mk("FunctionExpression", start), e.Body)
			kind := "method"
			switch e.Kind {
			case ast.PropertyKindGet:
				kind = "get"
			case ast.PropertyKindSet:
				kind = "set"
			}
			elements = append(elements, b.mk("MethodDefinition", e.Idx).set("static", e.Static).set("computed", e.Computed).set("key", b.expr(e.Key)).set("kind", kind).set("value", fn))
		case *ast.ClassStaticBlock:
			elements = append(elements, b.mk("StaticBlock", e.Static).set("body", b.stmts(e.Block.List)))
		}
	}
	body.set("body", elements)
	return n.set("id", id).set("superClass", b.expr(x.SuperClass)).set("body", body)
}

func (b *esBuilder) declarations(kind string, list []*ast.Binding, idx file.Idx) *esNode {
	decls := make([]*esNode, len(list))
	for i, bd := range list {
		d := b.mk("VariableDeclarator", bd.Target.Idx0())
		var init *esNode
		if bd.Initializer != nil {
			init = b.expr(bd.Initializer)
		}
		decls[i] = d.set("id", b.expr(bd.Target)).set("init", init)
	}
	return b.mk("VariableDeclaration", idx).set("declarations", decls).set("kind", kind)
}

func (b *esBuilder) forInto(into ast.ForInto) *esNode {
	switch x := into.(type) {
	case *ast.ForIntoVar:
		return b.declarations("var", []*ast.Binding{x.Binding}, x.Binding.Target.Idx0())
	case *ast.ForDeclaration:
		kind := "let"
		if x.IsConst {
			kind = "const"
		}
		return b.declarations(kind, []*ast.Binding{{Target: x.Target}}, x.Idx)
	case *ast.ForIntoExpression:
		return b.expr(x.Expression)
	}
	return nil
}

func lexKind(t token.Token) string {
	if t == token.CONST {
		return "const"
	}
	return "let"
}

// stmt converts a statement; nil in, nil out. Declarations hoisted by goja (var lists) are not statements and are skipped.
func (b *esBuilder) stmt(s ast.Statement) *esNode {
	if s == nil {
		return nil
	}
	switch x := s.(type) {
	case *ast.BlockStatement:
		return b.mk("BlockStatement", x.LeftBrace).set("body", b.stmts(x.List))
	case *ast.ExpressionStatement:
		return b.mk("ExpressionStatement", x.Expression.Idx0()).set("expression", b.expr(x.Expression))
	case *ast.EmptyStatement:
		return b.mk("EmptyStatement", x.Semicolon)
	case *ast.DebuggerStatement:
		return b.mk("DebuggerStatement", x.Debugger)
	case *ast.ReturnStatement:
		return b.mk("ReturnStatement", x.Return).set("argument", b.expr(x.Argument))
	case *ast.ThrowStatement:
		return b.mk("ThrowStatement", x.Throw).set("argument", b.expr(x.Argument))
	case *ast.IfStatement:
		return b.mk("IfStatement", x.If).set("test", b.expr(x.Test)).set("consequent", b.stmt(x.Consequent)).set("alternate", b.stmt(x.Alternate))
	case *ast.WhileStatement:
		return b.mk("WhileStatement", x.While).set("test", b.expr(x.Test)).set("body", b.stmt(x.Body))
	case *ast.DoWhileStatement:
		return b.mk("DoWhileStatement", x.Do).set("body", b.stmt(x.Body)).set("test", b.expr(x.Test))
	case *ast.ForStatement:
		var init *esNode
		switch i := x.Initializer.(type) {
		case *ast.ForLoopInitializerExpression:
			init = b.expr(i.Expression)
		case *ast.ForLoopInitializerVarDeclList:
			init = b.declarations("var", i.List, i.Var)
		case *ast.ForLoopInitializerLexicalDecl:
			init = b.declarations(lexKind(i.LexicalDeclaration.Token), i.LexicalDeclaration.List, i.LexicalDeclaration.Idx)
		}
		return b.mk("ForStatement", x.For).set("init", init).set("test", b.expr(x.Test)).set("update", b.expr(x.Update)).set("body", b.stmt(x.Body))
	case *ast.ForInStatement:
		return b.mk("ForInStatement", x.For).set("left", b.forInto(x.Into)).set("right", b.expr(x.Source)).set("body", b.stmt(x.Body))
	case *ast.ForOfStatement:
		return b.mk("ForOfStatement", x.For).set("await", x.Await).set("left", b.forInto(x.Into)).set("right", b.expr(x.Source)).set("body", b.stmt(x.Body))
	case *ast.BranchStatement:
		typ := "BreakStatement"
		switch x.Token {
		case token.CONTINUE:
			typ = "ContinueStatement"
		}
		var label *esNode
		if x.Label != nil {
			label = b.expr(x.Label)
		}
		return b.mk(typ, x.Idx).set("label", label)
	case *ast.LabelledStatement:
		return b.mk("LabeledStatement", x.Label.Idx).set("body", b.stmt(x.Statement)).set("label", b.expr(x.Label))
	case *ast.SwitchStatement:
		cases := make([]*esNode, len(x.Body))
		for i, c := range x.Body {
			cases[i] = b.mk("SwitchCase", c.Case).set("consequent", b.stmts(c.Consequent)).set("test", b.expr(c.Test))
		}
		return b.mk("SwitchStatement", x.Switch).set("discriminant", b.expr(x.Discriminant)).set("cases", cases)
	case *ast.TryStatement:
		n := b.mk("TryStatement", x.Try).set("block", b.stmt(x.Body))
		var handler *esNode
		if x.Catch != nil {
			handler = b.mk("CatchClause", x.Catch.Catch)
			var param *esNode
			if x.Catch.Parameter != nil {
				param = b.expr(x.Catch.Parameter)
			}
			handler.set("param", param).set("body", b.stmt(x.Catch.Body))
		}
		var fin *esNode
		if x.Finally != nil {
			fin = b.stmt(x.Finally)
		}
		return n.set("handler", handler).set("finalizer", fin)
	case *ast.VariableStatement:
		return b.declarations("var", x.List, x.Var)
	case *ast.LexicalDeclaration:
		return b.declarations(lexKind(x.Token), x.List, x.Idx)
	case *ast.WithStatement:
		return b.mk("WithStatement", x.With).set("object", b.expr(x.Object)).set("body", b.stmt(x.Body))
	case *ast.FunctionDeclaration:
		n := b.mk("FunctionDeclaration", b.functionStart(x.Function))
		return b.function(n, x.Function)
	case *ast.ClassDeclaration:
		return b.class("ClassDeclaration", x.Class)
	case *ast.BadStatement:
		return nil
	}
	return nil
}

// program converts the whole parsed program to an ESTree Program.
func (b *esBuilder) program(p *ast.Program) *esNode {
	return b.mk("Program", 1).set("body", b.stmts(p.Body)).set("sourceType", "script")
}
