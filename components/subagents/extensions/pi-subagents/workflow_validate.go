package pi_subagents

import (
	"regexp"

	"strings"
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
func validGitRef(ref any) bool { panic("not implemented") }

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

func workflowWrap(script string) string { panic("not implemented") }

// workflowParseError is a syntax error reported the way acorn reports one: message without the position, and the
// 1-based line and 0-based column of the wrapped source.
type workflowParseError struct {
	Message string
	Line    int
	Column  int
}

// workflowParse parses the wrapped source. It returns the ESTree of the program or the acorn-style error.
// Async generators are valid JavaScript that goja cannot parse: they are reported as nested async functions, as the original does.
func workflowParse(wrapped string) (*esNode, *workflowParseError) { panic("not implemented") }

// Early errors (a redeclared name, new.target outside a function) are reported by goja's compiler, by acorn's parser.

var workflowCompileErrorAt = regexp.MustCompile(` at [^ :]*:(\d+):(\d+)$`)

// workflowCompileError maps a goja compile-time SyntaxError to the message acorn gives for the same early error.
func workflowCompileError(err error) *workflowParseError { panic("not implemented") }

// workflowAcornError maps the first goja syntax error to acorn's wording.
func workflowAcornError(err error, wrapped string) *workflowParseError { panic("not implemented") }

// ---- tree helpers (astNode, literalString, directRunsCall, ...) ----

func literalString(n *esNode) (string, bool) { panic("not implemented") }

func memberPropertyName(callee *esNode) (string, bool) { panic("not implemented") }

func directRunsCall(n *esNode, method string) bool { panic("not implemented") }

func nodeLocation(n *esNode) (line, col int) { panic("not implemented") }

func locErr(msg string, n *esNode) WorkflowScriptValidationError { panic("not implemented") }

func isFunctionNode(n *esNode) bool { panic("not implemented") }

// walkAst visits n and its descendants in property order (scripted-workflow.ts:1605).
func walkAst(n *esNode, visit func(*esNode), includeNestedFunctions bool) { panic("not implemented") }

func walkAstList(list []*esNode, visit func(*esNode), includeNestedFunctions bool) {
	panic("not implemented")
}

func staticPropertyKey(p *esNode) (string, bool) { panic("not implemented") }

func directObjectPropertyValue(n *esNode, name string) *esNode { panic("not implemented") }

func allPropertiesStatic(n *esNode) bool { panic("not implemented") }

var workflowHostAllowed = map[string]bool{"kind": true, "command": true, "timeoutMs": true, "output": true, "role": true, "provider": true}

var workflowRelativePathBad = regexp.MustCompile(`^[/\\]|(?:^|[/\\])\.\.(?:[/\\]|$)`)

const workflowHostCwdHint = " The host step does not accept per-step cwd; commands and relative output paths use the workflow cwd. Set cwd on the outer subagent request, or put a trusted directory change in command (for example, 'cd /path/to/worktree && npm test')."

func validateStaticHostCall(call *esNode) []WorkflowScriptValidationError { panic("not implemented") }

func bindingPatternContainsName(n *esNode, name string) bool { panic("not implemented") }

func definitelyReassignsBinding(n *esNode, name string) bool { panic("not implemented") }

func lexicalDeclarationContainsName(n *esNode, name string) bool { panic("not implemented") }

func scopeShadowsIdentifier(n *esNode, name string) bool { panic("not implemented") }

func walkAstWithoutShadowedIdentifier(n *esNode, name string, visit func(*esNode)) {
	panic("not implemented")
}

// definitelyNonJson is definitelyNonJson (scripted-workflow.ts:1674).
func definitelyNonJson(n *esNode, normalizeUndefined bool) string { panic("not implemented") }

func validateStaticBaseRef(params *esNode, owner string) []WorkflowScriptValidationError {
	panic("not implemented")
}

// boolStr is value when set, else nil (a JavaScript undefined).
func boolStr(set bool, s string) any { panic("not implemented") }

// staticChildAgentNode is staticChildAgentNode (scripted-workflow.ts:1744).
func staticChildAgentNode(params *esNode) *esNode { panic("not implemented") }

func arrayElements(n *esNode) []*esNode { panic("not implemented") }

func validateStaticChildAgents(call *esNode, agentNameError func(string) string) []WorkflowScriptValidationError {
	panic("not implemented")
}

type directKey struct {
	key  string
	node *esNode
}

func directRunsAllKeys(call *esNode) []directKey { panic("not implemented") }

func containsWorkflowLaunch(n *esNode) bool { panic("not implemented") }

func containsRunsIdentifier(n *esNode) bool { panic("not implemented") }

func invalidatesRunsBinding(body *esNode) bool { panic("not implemented") }

func containsAbruptControl(n *esNode) bool { panic("not implemented") }

func directLaunchCall(expr *esNode) *esNode { panic("not implemented") }

type workflowLaunchPlan struct {
	keys    []string
	dynamic bool
}

func staticWorkflowLaunchPlan(body *esNode) workflowLaunchPlan { panic("not implemented") }

// workflowArrayInPrototypeChain are the properties `property in Array.from({length})` finds besides the indexes (V8, Node 24).
var workflowArrayInPrototypeChain = func() map[string]bool {
	m := map[string]bool{}
	for _, k := range strings.Fields("length constructor at concat copyWithin fill find findIndex findLast findLastIndex lastIndexOf pop push reverse shift unshift slice sort splice includes indexOf join keys entries values forEach filter flat flatMap map every some reduce reduceRight toLocaleString toString toReversed toSorted toSpliced with __defineGetter__ __defineSetter__ hasOwnProperty __lookupGetter__ __lookupSetter__ isPrototypeOf propertyIsEnumerable valueOf __proto__") {
		m[k] = true
	}
	return m
}()

func isArrayIndexString(s string) (int, bool) { panic("not implemented") }

// ValidateWorkflowScript parses a workflowScript and applies only the rules that are decidable from its local syntax
// (scripted-workflow.ts:1904-2012).
func ValidateWorkflowScript(script string, options WorkflowScriptValidationOptions) WorkflowScriptValidationResult {
	panic("not implemented")
}

// valid JavaScript the parser cannot read: the original rejects every async function (nested-async rule below)

// workflowOffset is the byte offset of a 1-based line and 0-based column (counted in runes) of src, clamped to a valid index.
func workflowOffset(src string, line, col int) int { panic("not implemented") }

// workflowUnterminatedTemplate finds the opening backtick of a template literal that never closes, as acorn reports it. It
// skips comments, strings and nested ${} substitutions; it does not tell a regular expression literal from division, and
// gives up (false) when it meets one it cannot place.
func workflowUnterminatedTemplate(src string) (line, col int, ok bool) { panic("not implemented") }

// inside a template's text (false: inside a ${ } substitution)
// open braces inside a substitution
