package pi_subagents

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"
)

// Static validation of a workflowScript, a port of validateWorkflowScript and its helpers
// (scripted-workflow.ts:11-18, 1538-2012). The original reads an acorn tree; this reads the ESTree rebuilt from goja's parser
// (workflow_estree.go).

var workflowKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

const workflowBaseRefValidationError = "baseRef must be a valid Git ref: use HEAD or a supported named ref (for example, refs/heads/main). Full 40/64-character commit IDs and revision expressions are unsupported."

var (
	workflowFullCommitRe   = regexp.MustCompile(`(?i)^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	workflowGitRefBadChars = regexp.MustCompile("[\\[\\]\\\\~^:?*\\x00-\\x20\\x7f]")
)

// validGitRef is validGitRef (scripted-workflow.ts:13-18).
func validGitRef(ref any) bool {
	s, ok := ref.(string)
	if !ok || s == "" || s == "@" || len(s) > 1024 || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") || strings.Contains(s, "//") || strings.Contains(s, "..") || strings.Contains(s, "@{") {
		return false
	}
	if workflowFullCommitRe.MatchString(s) {
		return false
	}
	if workflowGitRefBadChars.MatchString(s) || strings.HasSuffix(s, ".") || strings.HasSuffix(s, ".lock") {
		return false
	}
	for _, c := range strings.Split(s, "/") {
		if c == "" || c == "." || c == ".." || strings.HasPrefix(c, ".") || strings.HasSuffix(c, ".") || strings.HasSuffix(c, ".lock") {
			return false
		}
	}
	return true
}

// WorkflowScriptValidationError mirrors the original's interface of the same name (kind comes first in its JSON).
type WorkflowScriptValidationError struct {
	Kind    string `json:"kind,omitempty"` // "spawn-budget" or "agent"
	Message string `json:"message"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

// WorkflowScriptValidationWarning mirrors the original's interface of the same name.
type WorkflowScriptValidationWarning struct {
	Message string `json:"message"`
	Kind    string `json:"kind"` // "dynamic-spawn-count"
}

// WorkflowScriptValidationResult mirrors the original's interface of the same name.
type WorkflowScriptValidationResult struct {
	OK       bool
	Errors   []WorkflowScriptValidationError
	Warnings []WorkflowScriptValidationWarning
}

// WorkflowScriptValidationOptions mirrors the original's interface of the same name.
type WorkflowScriptValidationOptions struct {
	MaxSubagentSpawnsPerRun *int
	// AgentNameError returns why launch-time resolution would reject this literal child agent name ("" when it would not).
	AgentNameError func(name string) string
}

// workflowWrapPrefix is the line the original prepends to the script (scripted-workflow.ts:889, 1018): script line 1 is wrapped line 2.
const (
	workflowWrapPrefix = "(async () => {\n"
	workflowWrapSuffix = "\n})()"
)

func workflowWrap(script string) string { return workflowWrapPrefix + script + workflowWrapSuffix }

// workflowParseError is a syntax error reported the way acorn reports one: message without the position, and the
// 1-based line and 0-based column of the wrapped source.
type workflowParseError struct {
	Message string
	Line    int
	Column  int
}

// workflowParse parses the wrapped source. It returns the ESTree of the program or the acorn-style error.
// Async generators are valid JavaScript that goja cannot parse: they are reported as nested async functions, as the original does.
func workflowParse(wrapped string) (*esNode, *workflowParseError) {
	prog, err := parser.ParseFile(nil, "workflow-script.js", wrapped, 0)
	if err != nil {
		return nil, workflowAcornError(err, wrapped)
	}
	// Early errors (a redeclared name, new.target outside a function) are reported by goja's compiler, by acorn's parser.
	if _, err := goja.CompileAST(prog, false); err != nil {
		return nil, workflowCompileError(err)
	}
	return newESBuilder(wrapped).program(prog), nil
}

var workflowCompileErrorAt = regexp.MustCompile(` at [^ :]*:(\d+):(\d+)$`)

// workflowCompileError maps a goja compile-time SyntaxError to the message acorn gives for the same early error.
func workflowCompileError(err error) *workflowParseError {
	var cs *goja.CompilerSyntaxError
	msg := err.Error()
	if errors.As(err, &cs) {
		msg = cs.Message
	}
	pe := &workflowParseError{Message: msg, Line: 1}
	if m := workflowCompileErrorAt.FindStringSubmatch(err.Error()); m != nil {
		pe.Line, _ = strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])
		pe.Column = max(0, col-1)
	}
	switch {
	case strings.Contains(msg, "Async generators are not supported"):
		pe.Message = "async-generator"
	case strings.HasPrefix(msg, "new.target expression is not allowed here"):
		pe.Message = "'new.target' can only be used in functions and class static block"
	case strings.HasPrefix(msg, "Unterminated group"), strings.HasPrefix(msg, "Invalid regular expression"):
		pe.Message = "Invalid regular expression: " + msg
	}
	return pe
}

// workflowAcornError maps the first goja syntax error to acorn's wording.
func workflowAcornError(err error, wrapped string) *workflowParseError {
	list, ok := err.(parser.ErrorList)
	if !ok || len(list) == 0 {
		return &workflowParseError{Message: err.Error(), Line: 1}
	}
	first := list[0]
	msg := first.Message
	line := first.Position.Line
	col := first.Position.Column - 1
	if col < 0 {
		col = 0
	}
	if strings.HasPrefix(msg, "Unexpected end of input") {
		if l, c, ok := workflowUnterminatedTemplate(wrapped); ok {
			return &workflowParseError{Message: "Unterminated template", Line: l, Column: c}
		}
	}
	switch {
	case strings.Contains(msg, "Async generators are not supported"):
		return &workflowParseError{Message: "async-generator", Line: line, Column: col}
	case strings.HasPrefix(msg, "Unexpected token ILLEGAL") && col < len(wrapped) && wrapped[workflowOffset(wrapped, line, col)] == '\'' || strings.HasPrefix(msg, "Unexpected token ILLEGAL") && wrapped[workflowOffset(wrapped, line, col)] == '"':
		msg = "Unterminated string constant"
	case strings.HasPrefix(msg, "Missing catch or finally"):
		msg = "Missing catch or finally clause"
	case strings.HasPrefix(msg, "Unexpected token"), strings.HasPrefix(msg, "Unexpected end of input"), strings.HasPrefix(msg, "Unexpected identifier"), strings.HasPrefix(msg, "Unexpected number"), strings.HasPrefix(msg, "Unexpected string"), strings.HasPrefix(msg, "Unexpected reserved word"), strings.HasPrefix(msg, "Unexpected escape"):
		msg = "Unexpected token"
	case strings.Contains(msg, "String was not terminated"):
		msg = "Unterminated string constant"
	}
	return &workflowParseError{Message: msg, Line: line, Column: col}
}

// ---- tree helpers (astNode, literalString, directRunsCall, ...) ----

func literalString(n *esNode) (string, bool) {
	if n == nil {
		return "", false
	}
	if n.Type == "Literal" {
		if v, ok := n.get("value"); ok {
			if s, ok := v.(string); ok {
				return s, true
			}
		}
	}
	if n.Type == "TemplateLiteral" && len(n.list("expressions")) == 0 {
		q := n.list("quasis")
		if len(q) > 0 {
			if v, ok := q[0].get("value"); ok {
				if tv, ok := v.(*esTemplateValue); ok {
					if s, ok := tv.Cooked.(string); ok {
						return s, true
					}
				}
			}
		}
	}
	return "", false
}

func memberPropertyName(callee *esNode) (string, bool) {
	prop := callee.node("property")
	if prop == nil {
		return "", false
	}
	if callee.flag("computed") {
		return literalString(prop)
	}
	if prop.Type == "Identifier" {
		return prop.str("name")
	}
	return "", false
}

func directRunsCall(n *esNode, method string) bool {
	if n == nil || n.Type != "CallExpression" {
		return false
	}
	callee := n.node("callee")
	if callee == nil || callee.Type != "MemberExpression" {
		return false
	}
	name, ok := memberPropertyName(callee)
	if !ok || name != method {
		return false
	}
	obj := callee.node("object")
	if obj == nil || obj.Type != "Identifier" {
		return false
	}
	nm, _ := obj.str("name")
	return nm == "runs"
}

func nodeLocation(n *esNode) (line, col int) {
	if n == nil {
		return 0, 0
	}
	return max(1, n.Line-1), n.Column + 1
}

func locErr(msg string, n *esNode) WorkflowScriptValidationError {
	l, c := nodeLocation(n)
	return WorkflowScriptValidationError{Message: msg, Line: l, Column: c}
}

func isFunctionNode(n *esNode) bool {
	return n.Type == "FunctionDeclaration" || n.Type == "FunctionExpression" || n.Type == "ArrowFunctionExpression"
}

// walkAst visits n and its descendants in property order (scripted-workflow.ts:1605).
func walkAst(n *esNode, visit func(*esNode), includeNestedFunctions bool) {
	if n == nil {
		return
	}
	visit(n)
	if !includeNestedFunctions && isFunctionNode(n) {
		return
	}
	for _, f := range n.Fields {
		switch v := f.V.(type) {
		case *esNode:
			walkAst(v, visit, includeNestedFunctions)
		case []*esNode:
			for _, c := range v {
				walkAst(c, visit, includeNestedFunctions)
			}
		}
	}
}

func walkAstList(list []*esNode, visit func(*esNode), includeNestedFunctions bool) {
	for _, n := range list {
		walkAst(n, visit, includeNestedFunctions)
	}
}

func staticPropertyKey(p *esNode) (string, bool) {
	key := p.node("key")
	if p.flag("computed") {
		return literalString(key)
	}
	if s, ok := literalString(key); ok {
		return s, true
	}
	if key != nil && key.Type == "Identifier" {
		return key.str("name")
	}
	return "", false
}

func directObjectPropertyValue(n *esNode, name string) *esNode {
	if n == nil || n.Type != "ObjectExpression" {
		return nil
	}
	var value *esNode
	for _, p := range n.list("properties") {
		if p == nil || p.Type != "Property" || p.node("value") == nil {
			continue
		}
		if k, ok := staticPropertyKey(p); ok && k == name {
			value = p.node("value")
		}
	}
	return value
}

func allPropertiesStatic(n *esNode) bool {
	for _, p := range n.list("properties") {
		if p == nil || p.Type != "Property" {
			return false
		}
		if _, ok := staticPropertyKey(p); !ok {
			return false
		}
	}
	return true
}

var workflowHostAllowed = map[string]bool{"kind": true, "command": true, "timeoutMs": true, "output": true, "role": true, "provider": true}

var workflowRelativePathBad = regexp.MustCompile(`^[/\\]|(?:^|[/\\])\.\.(?:[/\\]|$)`)

const workflowHostCwdHint = " The host step does not accept per-step cwd; commands and relative output paths use the workflow cwd. Set cwd on the outer subagent request, or put a trusted directory change in command (for example, 'cd /path/to/worktree && npm test')."

func validateStaticHostCall(call *esNode) []WorkflowScriptValidationError {
	args := call.list("arguments")
	var keyNode, params *esNode
	if len(args) > 0 {
		keyNode = args[0]
	}
	if len(args) > 1 {
		params = args[1]
	}
	var errs []WorkflowScriptValidationError
	key, keyOK := literalString(keyNode)
	if keyNode != nil && keyOK && !workflowKeyPattern.MatchString(key) {
		errs = append(errs, locErr("runs.host key must be 1-128 characters using letters, numbers, '.', '_' or '-', and start with a letter or number.", keyNode))
	}
	if params == nil || params.Type != "ObjectExpression" {
		return errs
	}
	for _, p := range params.list("properties") {
		if p == nil || p.Type != "Property" {
			return errs
		}
	}
	for _, p := range params.list("properties") {
		name, ok := staticPropertyKey(p)
		if ok && !workflowHostAllowed[name] {
			if name == "cwd" {
				errs = append(errs, locErr("runs.host params contain unsupported field 'cwd'."+workflowHostCwdHint, p))
			} else {
				errs = append(errs, locErr(fmt.Sprintf("runs.host params contain unsupported field '%s'.", name), p))
			}
		}
	}
	kindNode := directObjectPropertyValue(params, "kind")
	commandNode := directObjectPropertyValue(params, "command")
	timeoutNode := directObjectPropertyValue(params, "timeoutMs")
	kind, kindOK := literalString(kindNode)
	command, commandOK := literalString(commandNode)
	var timeout *float64
	if timeoutNode != nil && timeoutNode.Type == "Literal" {
		if v, ok := timeoutNode.get("value"); ok {
			if f, ok := v.(float64); ok {
				timeout = &f
			}
		}
	}
	if kindNode == nil {
		errs = append(errs, locErr("runs.host params require kind: 'command'.", params))
	} else if kindOK && kind != "command" {
		errs = append(errs, locErr("runs.host params kind must be 'command'.", kindNode))
	}
	if commandNode == nil {
		errs = append(errs, locErr("runs.host params require command.", params))
	} else if commandOK && jsTrim(command) == "" {
		errs = append(errs, locErr("runs.host params command must be non-empty.", commandNode))
	}
	if timeoutNode == nil {
		errs = append(errs, locErr("runs.host params require timeoutMs.", params))
	} else if timeout != nil && (*timeout != float64(int64(*timeout)) || *timeout < 1) {
		errs = append(errs, locErr("runs.host params timeoutMs must be a positive integer.", timeoutNode))
	}
	outputNode := directObjectPropertyValue(params, "output")
	if output, ok := literalString(outputNode); outputNode != nil && ok && (jsTrim(output) == "" || workflowRelativePathBad.MatchString(output)) {
		errs = append(errs, locErr("runs.host params output must be a non-empty relative path without traversal.", outputNode))
	}
	roleNode := directObjectPropertyValue(params, "role")
	if role, ok := literalString(roleNode); roleNode != nil && ok && role != "ci" && role != "gate" {
		errs = append(errs, locErr("runs.host params role must be 'ci' or 'gate'.", roleNode))
	}
	return errs
}

func bindingPatternContainsName(n *esNode, name string) bool {
	if n == nil {
		return false
	}
	switch n.Type {
	case "Identifier":
		s, _ := n.str("name")
		return s == name
	case "RestElement":
		return bindingPatternContainsName(n.node("argument"), name)
	case "AssignmentPattern":
		return bindingPatternContainsName(n.node("left"), name)
	case "ArrayPattern":
		for _, e := range n.list("elements") {
			if bindingPatternContainsName(e, name) {
				return true
			}
		}
	case "ObjectPattern":
		for _, p := range n.list("properties") {
			if p == nil {
				continue
			}
			if p.Type == "RestElement" {
				if bindingPatternContainsName(p.node("argument"), name) {
					return true
				}
			} else if p.Type == "Property" && bindingPatternContainsName(p.node("value"), name) {
				return true
			}
		}
	}
	return false
}

func definitelyReassignsBinding(n *esNode, name string) bool {
	for n != nil && n.Type == "AssignmentExpression" {
		op, _ := n.str("operator")
		if op == "&&=" || op == "||=" || op == "??=" {
			return false
		}
		if bindingPatternContainsName(n.node("left"), name) {
			return true
		}
		n = n.node("right")
	}
	return false
}

func lexicalDeclarationContainsName(n *esNode, name string) bool {
	if n == nil {
		return false
	}
	if n.Type == "VariableDeclaration" {
		if k, _ := n.str("kind"); k != "var" {
			for _, d := range n.list("declarations") {
				if d != nil && bindingPatternContainsName(d.node("id"), name) {
					return true
				}
			}
		}
		return false
	}
	return (n.Type == "FunctionDeclaration" || n.Type == "ClassDeclaration") && bindingPatternContainsName(n.node("id"), name)
}

func scopeShadowsIdentifier(n *esNode, name string) bool {
	switch n.Type {
	case "BlockStatement":
		for _, s := range n.list("body") {
			if lexicalDeclarationContainsName(s, name) {
				return true
			}
		}
	case "CatchClause":
		return bindingPatternContainsName(n.node("param"), name)
	case "ForStatement":
		return lexicalDeclarationContainsName(n.node("init"), name)
	case "ForInStatement", "ForOfStatement":
		return lexicalDeclarationContainsName(n.node("left"), name)
	case "SwitchStatement":
		for _, c := range n.list("cases") {
			for _, s := range c.list("consequent") {
				if lexicalDeclarationContainsName(s, name) {
					return true
				}
			}
		}
	}
	return false
}

func walkAstWithoutShadowedIdentifier(n *esNode, name string, visit func(*esNode)) {
	if n == nil || scopeShadowsIdentifier(n, name) {
		return
	}
	visit(n)
	if isFunctionNode(n) {
		return
	}
	for _, f := range n.Fields {
		switch v := f.V.(type) {
		case *esNode:
			walkAstWithoutShadowedIdentifier(v, name, visit)
		case []*esNode:
			for _, c := range v {
				walkAstWithoutShadowedIdentifier(c, name, visit)
			}
		}
	}
}

// definitelyNonJson is definitelyNonJson (scripted-workflow.ts:1674).
func definitelyNonJson(n *esNode, normalizeUndefined bool) string {
	switch n.Type {
	case "Literal":
		if _, ok := n.get("bigint"); ok {
			return "BigInt values are not JSON-representable"
		}
		if _, ok := n.get("regex"); ok {
			return "regular expressions are not JSON-representable"
		}
		return ""
	case "FunctionExpression", "ArrowFunctionExpression":
		return "functions are not JSON-representable"
	case "Identifier":
		if nm, _ := n.str("name"); nm == "undefined" && !normalizeUndefined {
			return "undefined is not JSON-representable"
		}
		return ""
	case "UnaryExpression":
		if op, _ := n.str("operator"); op == "void" && !normalizeUndefined {
			return "undefined is not JSON-representable"
		}
		return ""
	case "ArrayExpression":
		els := n.list("elements")
		for _, e := range els {
			if e == nil {
				return "sparse arrays are not JSON-representable"
			}
		}
		for _, e := range els {
			if m := definitelyNonJson(e, normalizeUndefined); m != "" {
				return m
			}
		}
	case "ObjectExpression":
		values := map[string]*esNode{}
		var order []string
		for _, p := range n.list("properties") {
			if p == nil || p.Type != "Property" || p.node("value") == nil {
				return ""
			}
			if k, _ := p.str("kind"); k != "init" {
				return ""
			}
			key, ok := staticPropertyKey(p)
			if !ok {
				return ""
			}
			if _, seen := values[key]; !seen {
				order = append(order, key)
			}
			values[key] = p.node("value")
		}
		for _, k := range order {
			if m := definitelyNonJson(values[k], normalizeUndefined); m != "" {
				return m
			}
		}
	}
	return ""
}

func validateStaticBaseRef(params *esNode, owner string) []WorkflowScriptValidationError {
	if params == nil || params.Type != "ObjectExpression" {
		return nil
	}
	props := params.list("properties")
	for i := len(props) - 1; i >= 0; i-- {
		p := props[i]
		if p == nil || p.Type != "Property" {
			return nil
		}
		key, ok := staticPropertyKey(p)
		if !ok {
			return nil
		}
		if key != "baseRef" {
			continue
		}
		if k, _ := p.str("kind"); k != "init" || p.node("value") == nil {
			return nil
		}
		valueNode := p.node("value")
		value, isStr := literalString(valueNode)
		if (isStr || valueNode.Type == "Literal") && !validGitRef(boolStr(isStr, value)) {
			return []WorkflowScriptValidationError{locErr(owner+" "+workflowBaseRefValidationError, valueNode)}
		}
		return nil
	}
	return nil
}

// boolStr is value when set, else nil (a JavaScript undefined).
func boolStr(set bool, s string) any {
	if set {
		return s
	}
	return nil
}

// staticChildAgentNode is staticChildAgentNode (scripted-workflow.ts:1744).
func staticChildAgentNode(params *esNode) *esNode {
	if params == nil || params.Type != "ObjectExpression" {
		return nil
	}
	var agent *esNode
	for _, p := range params.list("properties") {
		if p == nil || p.Type != "Property" {
			return nil
		}
		key, ok := staticPropertyKey(p)
		if !ok || key == "cwd" || key == "agentScope" || key == "resume" {
			return nil
		}
		if key == "agent" {
			if k, _ := p.str("kind"); k == "init" && p.node("value") != nil {
				agent = p.node("value")
			} else {
				agent = nil
			}
		}
	}
	if _, ok := literalString(agent); !ok {
		return nil
	}
	return agent
}

func arrayElements(n *esNode) []*esNode {
	if n != nil && n.Type == "ArrayExpression" {
		return n.list("elements")
	}
	return nil
}

func validateStaticChildAgents(call *esNode, agentNameError func(string) string) []WorkflowScriptValidationError {
	args := call.list("arguments")
	arg := func(i int) *esNode {
		if i < len(args) {
			return args[i]
		}
		return nil
	}
	staticLane := func(lane *esNode) bool {
		if lane == nil || lane.Type != "ObjectExpression" {
			return false
		}
		return allPropertiesStatic(lane)
	}
	type child struct {
		owner  string
		params *esNode
	}
	var children []child
	switch {
	case directRunsCall(call, "run"):
		children = append(children, child{"runs.run", arg(1)})
	case directRunsCall(call, "all"):
		for _, p := range arrayElements(arg(0)) {
			children = append(children, child{"runs.all item", p})
		}
	case directRunsCall(call, "lanes"):
		for _, lane := range arrayElements(arg(0)) {
			if staticLane(lane) {
				for _, p := range arrayElements(directObjectPropertyValue(lane, "stages")) {
					children = append(children, child{"runs.lanes stage", p})
				}
			}
		}
	}
	var errs []WorkflowScriptValidationError
	for _, c := range children {
		agentNode := staticChildAgentNode(c.params)
		name, ok := literalString(agentNode)
		if !ok {
			continue
		}
		if msg := agentNameError(name); msg != "" && agentNode != nil {
			e := locErr(c.owner+": "+msg, agentNode)
			e.Kind = "agent"
			errs = append(errs, e)
		}
	}
	return errs
}

type directKey struct {
	key  string
	node *esNode
}

func directRunsAllKeys(call *esNode) []directKey {
	args := call.list("arguments")
	var items []*esNode
	if len(args) > 0 {
		items = arrayElements(args[0])
	}
	var out []directKey
	for _, item := range items {
		if item == nil {
			continue
		}
		keyNode := directObjectPropertyValue(item, "key")
		if key, ok := literalString(keyNode); keyNode != nil && ok {
			out = append(out, directKey{key, keyNode})
		}
	}
	return out
}

func containsWorkflowLaunch(n *esNode) bool {
	found := false
	walkAst(n, func(c *esNode) {
		if directRunsCall(c, "run") || directRunsCall(c, "all") || directRunsCall(c, "lanes") {
			found = true
		}
	}, true)
	return found
}

func containsRunsIdentifier(n *esNode) bool {
	found := false
	walkAst(n, func(c *esNode) {
		if nm, _ := c.str("name"); c.Type == "Identifier" && nm == "runs" {
			found = true
		}
	}, true)
	return found
}

func invalidatesRunsBinding(body *esNode) bool {
	invalidated := false
	walkAst(body, func(n *esNode) {
		switch n.Type {
		case "VariableDeclarator", "FunctionDeclaration", "FunctionExpression", "ClassDeclaration", "ClassExpression":
			if containsRunsIdentifier(n.node("id")) {
				invalidated = true
			}
		}
		if isFunctionNode(n) {
			for _, p := range n.list("params") {
				if containsRunsIdentifier(p) {
					invalidated = true
				}
			}
		}
		if n.Type == "CatchClause" && containsRunsIdentifier(n.node("param")) {
			invalidated = true
		}
		if n.Type == "AssignmentExpression" && containsRunsIdentifier(n.node("left")) {
			invalidated = true
		}
		if n.Type == "UpdateExpression" && containsRunsIdentifier(n.node("argument")) {
			invalidated = true
		}
	}, true)
	return invalidated
}

func containsAbruptControl(n *esNode) bool {
	found := false
	walkAst(n, func(c *esNode) {
		if c != n && (c.Type == "ReturnStatement" || c.Type == "ThrowStatement" || c.Type == "BreakStatement" || c.Type == "ContinueStatement") {
			found = true
		}
	}, false)
	return found
}

func directLaunchCall(expr *esNode) *esNode {
	if expr == nil {
		return nil
	}
	candidate := expr
	if expr.Type == "AwaitExpression" && expr.node("argument") != nil {
		candidate = expr.node("argument")
	}
	if directRunsCall(candidate, "run") || directRunsCall(candidate, "all") {
		return candidate
	}
	return nil
}

type workflowLaunchPlan struct {
	keys    []string
	dynamic bool
}

func staticWorkflowLaunchPlan(body *esNode) workflowLaunchPlan {
	if body.Type != "BlockStatement" || invalidatesRunsBinding(body) {
		return workflowLaunchPlan{dynamic: containsWorkflowLaunch(body)}
	}
	var keys []string
	seen := map[string]bool{}
	addKey := func(k string) {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	dynamic := false
	remainderUncertain := false
	addCall := func(call *esNode) {
		args := call.list("arguments")
		nested := false
		for _, a := range args {
			walkAst(a, func(n *esNode) {
				if n != call && (directRunsCall(n, "run") || directRunsCall(n, "all") || directRunsCall(n, "lanes")) {
					nested = true
				}
			}, true)
		}
		if nested {
			dynamic = true
			return
		}
		if directRunsCall(call, "run") {
			var first *esNode
			if len(args) > 0 {
				first = args[0]
			}
			if key, ok := literalString(first); ok {
				addKey(key)
			} else {
				dynamic = true
			}
			return
		}
		var array *esNode
		if len(args) > 0 && args[0] != nil && args[0].Type == "ArrayExpression" {
			array = args[0]
		}
		if array == nil {
			dynamic = true
			return
		}
		var batch []string
		for _, item := range array.list("elements") {
			if item == nil || item.Type != "ObjectExpression" || !allPropertiesStatic(item) {
				dynamic = true
				return
			}
			key, ok := literalString(directObjectPropertyValue(item, "key"))
			if !ok {
				dynamic = true
				return
			}
			batch = append(batch, key)
		}
		for _, k := range batch {
			addKey(k)
		}
	}
	for _, st := range body.list("body") {
		if st == nil {
			continue
		}
		if remainderUncertain {
			if containsWorkflowLaunch(st) {
				dynamic = true
			}
			continue
		}
		var exprs []*esNode
		hasExprs := false
		switch st.Type {
		case "VariableDeclaration":
			for _, d := range st.list("declarations") {
				if d != nil {
					exprs = append(exprs, d.node("init"))
					hasExprs = true
				}
			}
		case "ExpressionStatement":
			exprs = append(exprs, st.node("expression"))
			hasExprs = true
		case "ReturnStatement", "ThrowStatement":
			exprs = append(exprs, st.node("argument"))
			hasExprs = true
		}
		handled := false
		for _, e := range exprs {
			if call := directLaunchCall(e); call != nil {
				addCall(call)
				handled = true
			} else if containsWorkflowLaunch(e) {
				dynamic = true
			}
		}
		if !handled && !hasExprs && containsWorkflowLaunch(st) {
			dynamic = true
		}
		if st.Type == "ReturnStatement" || st.Type == "ThrowStatement" || containsAbruptControl(st) {
			remainderUncertain = true
		}
	}
	return workflowLaunchPlan{keys: keys, dynamic: dynamic}
}

// workflowArrayInPrototypeChain are the properties `property in Array.from({length})` finds besides the indexes (V8, Node 24).
var workflowArrayInPrototypeChain = func() map[string]bool {
	m := map[string]bool{}
	for _, k := range strings.Fields("length constructor at concat copyWithin fill find findIndex findLast findLastIndex lastIndexOf pop push reverse shift unshift slice sort splice includes indexOf join keys entries values forEach filter flat flatMap map every some reduce reduceRight toLocaleString toString toReversed toSorted toSpliced with __defineGetter__ __defineSetter__ hasOwnProperty __lookupGetter__ __lookupSetter__ isPrototypeOf propertyIsEnumerable valueOf __proto__") {
		m[k] = true
	}
	return m
}()

func isArrayIndexString(s string) (int, bool) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
		if n > 1<<31 {
			return 0, false
		}
	}
	return n, true
}

// ValidateWorkflowScript parses a workflowScript and applies only the rules that are decidable from its local syntax
// (scripted-workflow.ts:1904-2012).
func ValidateWorkflowScript(script string, options WorkflowScriptValidationOptions) WorkflowScriptValidationResult {
	if jsTrim(script) == "" {
		return WorkflowScriptValidationResult{OK: false, Errors: []WorkflowScriptValidationError{{Message: "workflowScript must not be empty."}}}
	}
	root, perr := workflowParse(workflowWrap(script))
	if perr != nil {
		msg := perr.Message
		if msg == "async-generator" {
			// valid JavaScript the parser cannot read: the original rejects every async function (nested-async rule below)
			msg = "workflowScript does not support nested async functions. Use top-level await, plain helper functions that return runs.run(...), or explicit Promise chains."
			return WorkflowScriptValidationResult{OK: false, Errors: []WorkflowScriptValidationError{{Message: msg, Line: max(1, perr.Line-1), Column: perr.Column + 1}}}
		}
		return WorkflowScriptValidationResult{OK: false, Errors: []WorkflowScriptValidationError{{Message: msg, Line: max(1, perr.Line-1), Column: perr.Column + 1}}}
	}
	var errs []WorkflowScriptValidationError

	var wrapper *esNode
	if body := root.list("body"); len(body) > 0 && body[0] != nil {
		if expr := body[0].node("expression"); expr != nil {
			wrapper = expr.node("callee")
		}
	}
	workflowBody := root
	if wrapper != nil && wrapper.node("body") != nil {
		workflowBody = wrapper.node("body")
	}
	agentNameError := options.AgentNameError
	if agentNameError != nil && invalidatesRunsBinding(workflowBody) {
		agentNameError = nil
	}
	walkAst(workflowBody, func(n *esNode) {
		if n != wrapper && n.flag("async") && isFunctionNode(n) {
			errs = append(errs, locErr("workflowScript does not support nested async functions. Use top-level await, plain helper functions that return runs.run(...), or explicit Promise chains.", n))
		}
		if directRunsCall(n, "run") {
			args := n.list("arguments")
			var keyNode *esNode
			if len(args) > 0 {
				keyNode = args[0]
			}
			if key, ok := literalString(keyNode); keyNode != nil && ok && !workflowKeyPattern.MatchString(key) {
				errs = append(errs, locErr("runs.run key must be 1-128 characters using letters, numbers, '.', '_' or '-', and start with a letter or number.", keyNode))
			}
			if len(args) > 1 && args[1] != nil {
				errs = append(errs, validateStaticBaseRef(args[1], "runs.run")...)
				if m := definitelyNonJson(args[1], false); m != "" {
					errs = append(errs, locErr("runs.run params are invalid: "+m+".", args[1]))
				}
			}
		}
		if directRunsCall(n, "all") {
			for _, e := range directRunsAllKeys(n) {
				if !workflowKeyPattern.MatchString(e.key) {
					errs = append(errs, locErr("runs.all item key must be 1-128 characters using letters, numbers, '.', '_' or '-', and start with a letter or number.", e.node))
				}
			}
			args := n.list("arguments")
			if len(args) > 0 {
				for _, item := range arrayElements(args[0]) {
					if item == nil {
						continue
					}
					errs = append(errs, validateStaticBaseRef(item, "runs.all item")...)
					if m := definitelyNonJson(item, false); m != "" {
						errs = append(errs, locErr("runs.all item params are invalid: "+m+".", item))
					}
				}
			}
		}
		if directRunsCall(n, "host") {
			errs = append(errs, validateStaticHostCall(n)...)
		}
		if agentNameError != nil {
			errs = append(errs, validateStaticChildAgents(n, agentNameError)...)
		}
		var boundary *esNode
		if n.Type == "CallExpression" {
			callee := n.node("callee")
			args := n.list("arguments")
			if callee != nil && callee.Type == "Identifier" {
				if nm, _ := callee.str("name"); nm == "emit" && len(args) > 0 && args[0] != nil {
					boundary = args[0]
				}
			} else if callee != nil && callee.Type == "MemberExpression" {
				obj, prop := callee.node("object"), callee.node("property")
				if obj != nil && obj.Type == "Identifier" && prop != nil && prop.Type == "Identifier" {
					on, _ := obj.str("name")
					pn, _ := prop.str("name")
					if on == "state" && pn == "set" && len(args) > 1 && args[1] != nil {
						boundary = args[1]
					}
				}
			}
		}
		if boundary != nil {
			if m := definitelyNonJson(boundary, false); m != "" {
				errs = append(errs, locErr("workflowScript boundary value is invalid: "+m+".", boundary))
			}
		}
	}, true)
	walkAst(workflowBody, func(n *esNode) {
		if n.Type != "ReturnStatement" || n.node("argument") == nil {
			return
		}
		if m := definitelyNonJson(n.node("argument"), true); m != "" {
			errs = append(errs, locErr("workflowScript boundary value is invalid: "+m+".", n.node("argument")))
		}
	}, false)

	if workflowBody.Type == "BlockStatement" {
		stmts := workflowBody.list("body")
		for i, st := range stmts {
			if st == nil || st.Type != "VariableDeclaration" {
				continue
			}
			kind, _ := st.str("kind")
			for _, d := range st.list("declarations") {
				if d == nil || d.node("id") == nil || d.node("id").Type != "Identifier" || d.node("init") == nil || d.node("init").Type != "AwaitExpression" {
					continue
				}
				arg := d.node("init").node("argument")
				if arg == nil || !directRunsCall(arg, "all") {
					continue
				}
				name, _ := d.node("id").str("name")
				keys := map[string]bool{}
				for _, e := range directRunsAllKeys(arg) {
					keys[e.key] = true
				}
				if len(keys) == 0 {
					continue
				}
				itemCount := 0
				if args := arg.list("arguments"); len(args) > 0 {
					itemCount = len(arrayElements(args[0]))
				}
				for _, later := range stmts[i+1:] {
					walkAstWithoutShadowedIdentifier(later, name, func(n *esNode) {
						if n.Type != "MemberExpression" {
							return
						}
						obj := n.node("object")
						if obj == nil || obj.Type != "Identifier" {
							return
						}
						if on, _ := obj.str("name"); on != name {
							return
						}
						prop, ok := memberPropertyName(n)
						if !ok || prop == "" {
							return
						}
						if idx, isIdx := isArrayIndexString(prop); (isIdx && idx < itemCount) || workflowArrayInPrototypeChain[prop] {
							return
						}
						if keys[prop] {
							errs = append(errs, locErr(fmt.Sprintf("runs.all returns an ordered array; '%s.%s' is keyed access. Use an index, destructuring, or map(...).", name, prop), n))
						}
					})
					if kind != "const" && later != nil && later.Type == "ExpressionStatement" && definitelyReassignsBinding(later.node("expression"), name) {
						break
					}
				}
			}
		}
	}

	var warnings []WorkflowScriptValidationWarning
	if options.MaxSubagentSpawnsPerRun != nil {
		budget := *options.MaxSubagentSpawnsPerRun
		plan := staticWorkflowLaunchPlan(workflowBody)
		if len(plan.keys) > budget {
			quoted := make([]string, len(plan.keys))
			for i, k := range plan.keys {
				quoted[i] = "'" + k + "'"
			}
			errs = append(errs, WorkflowScriptValidationError{Kind: "spawn-budget", Message: fmt.Sprintf("workflowScript statically requires child launches %s; minimum required: %d; configured: %d.", strings.Join(quoted, ", "), len(plan.keys), budget)})
		}
		if plan.dynamic {
			warnings = append(warnings, WorkflowScriptValidationWarning{Kind: "dynamic-spawn-count", Message: fmt.Sprintf("workflowScript contains dynamic child launches; static validation proved %d launch(es), so runtime fan-out enforcement remains authoritative for the configured budget of %d.", len(plan.keys), budget)})
		}
	}
	var unique []WorkflowScriptValidationError
	for _, e := range errs {
		dup := false
		for _, u := range unique {
			if u.Message == e.Message && u.Line == e.Line && u.Column == e.Column {
				dup = true
				break
			}
		}
		if !dup {
			unique = append(unique, e)
		}
	}
	return WorkflowScriptValidationResult{OK: len(unique) == 0, Errors: unique, Warnings: warnings}
}

// workflowOffset is the byte offset of a 1-based line and 0-based column (counted in runes) of src, clamped to a valid index.
func workflowOffset(src string, line, col int) int {
	off := 0
	for l := 1; l < line; l++ {
		i := strings.IndexByte(src[off:], '\n')
		if i < 0 {
			return max(0, len(src)-1)
		}
		off += i + 1
	}
	for c := 0; c < col && off < len(src); c++ {
		_, n := utf8.DecodeRuneInString(src[off:])
		off += n
	}
	return min(off, max(0, len(src)-1))
}

// workflowUnterminatedTemplate finds the opening backtick of a template literal that never closes, as acorn reports it. It
// skips comments, strings and nested ${} substitutions; it does not tell a regular expression literal from division, and
// gives up (false) when it meets one it cannot place.
func workflowUnterminatedTemplate(src string) (line, col int, ok bool) {
	type frame struct {
		template bool // inside a template's text (false: inside a ${ } substitution)
		depth    int  // open braces inside a substitution
		start    int
	}
	var stack []frame
	i := 0
	for i < len(src) {
		c := src[i]
		top := len(stack) - 1
		inText := top >= 0 && stack[top].template
		switch {
		case inText && c == '\\':
			i += 2
			continue
		case inText && c == '`':
			stack = stack[:top]
		case inText && c == '$' && i+1 < len(src) && src[i+1] == '{':
			stack = append(stack, frame{})
			i += 2
			continue
		case inText:
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return 0, 0, false
			}
			i += end + 4
			continue
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(src) && src[j] != c && src[j] != '\n' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			i = j + 1
			continue
		case c == '`':
			stack = append(stack, frame{template: true, start: i})
		case c == '{' && top >= 0:
			stack[top].depth++
		case c == '}' && top >= 0:
			if stack[top].depth > 0 {
				stack[top].depth--
			} else {
				stack = stack[:top]
			}
		}
		i++
	}
	for j := len(stack) - 1; j >= 0; j-- {
		if stack[j].template {
			l := 1 + strings.Count(src[:stack[j].start], "\n")
			last := strings.LastIndexByte(src[:stack[j].start], '\n')
			return l, len(utf16.Encode([]rune(src[last+1:stack[j].start]))) + 1, true
		}
	}
	return 0, 0, false
}
