package pi_subagents

import (
	"regexp"

	"github.com/dop251/goja"
)

// Error texts the model sees. The script runs in goja, whose messages and stack frames differ from V8's; the original's
// worker reports V8's. These helpers put goja's output in V8's form for the cases a workflow script plausibly hits.

var workflowGojaFrame = regexp.MustCompile(`^\s*at (.*?)(?:\((\d+)\))?$`)

// workflowV8Stack turns a goja stack ("\tat f (file:2:9(3))") into V8's ("    at f (file:2:9)"). Frame text before the
// position is kept; the pc suffix goja adds is dropped.
func workflowV8Stack(stack string) string { panic("not implemented") }

// frames of host functions and of the sandbox's own setup are not the script's

var (
	workflowReadPropRe   = regexp.MustCompile(`^Cannot read property '(.*)' of (undefined|null)$`)
	workflowSetPropRe    = regexp.MustCompile(`^Cannot set property '(.*)' of (undefined|null)$`)
	workflowReadOnlyRe   = regexp.MustCompile(`^Cannot assign to read only property '(.*)'$`)
	workflowNotFunctionR = regexp.MustCompile(`^(?:Object|.*) has no member '(.*)'$`)
)

// workflowV8Message rewrites the goja message of an error to V8's wording where the two differ.
func workflowV8Message(name, msg string) string { panic("not implemented") }

// workflowErrorHeader is the first line V8 puts in a stack: "<name>: <message>" (just the name without a message).
func workflowErrorHeader(name, msg string) string { panic("not implemented") }

var (
	workflowFirstFrame   = regexp.MustCompile(`\n\s*at workflow-script\.js:(\d+):(\d+)`)
	workflowCalleeChain  = regexp.MustCompile(`^[A-Za-z_$][\w$]*(?:\??\.[A-Za-z_$][\w$]*)*$`)
	workflowNotAFunction = regexp.MustCompile(`^(?:Object has no member '.*'|Value is not an object: .*|Value is not a function: .*)$`)
	workflowStaticV8     = map[string]string{
		"No initial value":     "Reduce of empty array with no initial value",
		"Malformed URI":        "URI malformed",
		"Invalid array length": "Invalid array length",
		"Class constructor cannot be invoked without 'new'": "Class constructor cannot be invoked without 'new'",
	}
	workflowBigIntRe = regexp.MustCompile(`^Cannot convert (.*) to a BigInt$`)
)

// calleeText is the source text V8 would print for the callee of the call at (line, col): goja reports the position of the
// opening parenthesis. Only plain identifier and member chains are rendered; anything else is left to goja's message.
func (r *wfRealm) calleeText(line, col int) (string, bool) { panic("not implemented") }

// workflowFormatError is formatWorkflowScriptError (scripted-workflow.ts:865): the stack when it holds the message, else the
// message and the stack.
func (r *wfRealm) formatError(v goja.Value) string { panic("not implemented") }

// safeString is String(v), "[object Object]" style, never throwing.
func (r *wfRealm) safeString(v goja.Value) (s string) { panic("not implemented") }

// ---- JSON.parse with V8's error messages ----

// workflowJSONError is the message V8 gives for a JSON.parse that fails, or "" when text is valid JSON. It follows
// V8's JsonParser (src/json/json-parser.cc) for the shapes a script meets: truncated or malformed child output.
func workflowJSONError(text string) string { panic("not implemented") }

type workflowJSONParser struct {
	src  []uint16
	text string
	pos  int
}

func (p *workflowJSONParser) posMsg(msg string, pos int) string { panic("not implemented") }

func (p *workflowJSONParser) unexpected(pos int) string { panic("not implemented") }

func (p *workflowJSONParser) ws() { panic("not implemented") }

func (p *workflowJSONParser) parse() string { panic("not implemented") }

func (p *workflowJSONParser) literal(word string) string { panic("not implemented") }

func (p *workflowJSONParser) value() string { panic("not implemented") }

func (p *workflowJSONParser) digit() bool { panic("not implemented") }

func (p *workflowJSONParser) number() string { panic("not implemented") }

func (p *workflowJSONParser) str() string { panic("not implemented") }

func (p *workflowJSONParser) at(c uint16) bool { panic("not implemented") }

func (p *workflowJSONParser) array() string { panic("not implemented") }

func (p *workflowJSONParser) object() string { panic("not implemented") }
