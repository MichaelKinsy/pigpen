package pi_subagents

import (
	"strings"
	"testing"
)

// The gaps goja has against the original's node:vm context (port/PORT.md, "Workflow sandbox"): each one, as a script a model
// could write and what it sees.

func gapRun(t *testing.T, script string) any {
	t.Helper()
	return wfRun(t, wfOptions(script)).Value
}

func TestSandboxHostGlobalsAreAbsentAsInTheOriginal(t *testing.T) {
	// A node:vm context holds only the ECMAScript builtins and what the workflow adds: the Node globals are not there.
	got := gapRun(t, `return ["structuredClone","TextEncoder","TextDecoder","URL","fetch","process","require","setTimeout","queueMicrotask","Buffer","module","SharedArrayBuffer"].map((n) => typeof globalThis[n]);`)
	jsonEq(t, got, `["undefined","undefined","undefined","undefined","undefined","undefined","undefined","undefined","undefined","undefined","undefined","undefined"]`)
}

func TestSandboxWeakRefAndArrayFromAsync(t *testing.T) {
	got := gapRun(t, `const target = { a: 1 }; const ref = new WeakRef(target);
const fromArray = await Array.fromAsync([Promise.resolve(1), 2, Promise.resolve(3)], (v) => v * 2);
return { deref: ref.deref() === target, tag: Object.prototype.toString.call(ref), fromArray, kind: typeof FinalizationRegistry };`)
	jsonEq(t, got, `{"deref":true,"tag":"[object WeakRef]","fromArray":[2,4,6],"kind":"function"}`)
}

func TestSandboxAsyncGeneratorsAreRejectedLikeEveryAsyncFunction(t *testing.T) {
	// The original rejects any async function but the wrapper, an async generator included; goja cannot parse one, and says so the same way.
	for _, script := range []string{"async function* g() { yield 1; }\nreturn 1;", "const o = { async *g() { yield 1; } };\nreturn 1;"} {
		err := wfFail(t, wfOptions(script))
		if err.Kind != WorkflowFailureValidation || !strings.Contains(err.Message, "workflowScript does not support nested async functions") {
			t.Errorf("%q: kind %q, message %q", script, err.Kind, err.Message)
		}
		v := ValidateWorkflowScript(script, WorkflowScriptValidationOptions{})
		if v.OK || len(v.Errors) == 0 || !strings.Contains(v.Errors[0].Message, "nested async functions") {
			t.Errorf("%q: validation %+v", script, v)
		}
	}
}

func TestSandboxStackOverflowEndsTheWorkflowWithAV8RangeError(t *testing.T) {
	// goja bounds the call stack (SetMaxCallStackSize) but its overflow is not catchable from the script, as V8's RangeError is:
	// the workflow fails with V8's message instead (a difference, recorded in port/PORT.md).
	err := wfFail(t, wfOptions("function f() { return f() + 1; }\nf();\nreturn 1;"))
	if err.Kind != WorkflowFailureScript || !strings.Contains(err.Message, "RangeError: Maximum call stack size exceeded") {
		t.Errorf("kind %q, message %q", err.Kind, err.Message)
	}
}

// goja cannot tell null from undefined in this message: `null.x` reads "of undefined" (a difference, recorded in port/PORT.md).
func TestSandboxRuntimeErrorsThatReachTheModelReadLikeV8(t *testing.T) {
	cases := []struct{ script, want string }{
		{`const o = undefined;\nreturn o.x;`, "TypeError: Cannot read properties of undefined (reading 'x')"},
		{`const o = {};\nreturn o.f();`, "TypeError: o.f is not a function"},
		{`return JSON.parse("{bad");`, "SyntaxError: Expected property name or '}' in JSON at position 1"},
		{`return undefinedVariable;`, "ReferenceError: undefinedVariable is not defined"},
	}
	for _, c := range cases {
		err := wfFail(t, wfOptions(strings.ReplaceAll(c.script, `\n`, "\n")))
		if err.Kind != WorkflowFailureScript || !strings.Contains(err.Message, c.want) {
			t.Errorf("%s\n kind %q, message %q\n want it to contain %q", c.script, err.Kind, err.Message, c.want)
		}
	}
}

func TestSandboxCodeGenerationIsBlocked(t *testing.T) {
	got := gapRun(t, `const tries = [() => eval("1"), () => new Function("return 1")(), () => ({}).constructor.constructor("return 1")(), () => (function* () {}).constructor("yield 1"), () => [].map.constructor("return 1")()];
return tries.map((f) => { try { f(); return "ran"; } catch (e) { return e.constructor.name; } });`)
	jsonEq(t, got, `["EvalError","EvalError","EvalError","EvalError","EvalError"]`)
}
