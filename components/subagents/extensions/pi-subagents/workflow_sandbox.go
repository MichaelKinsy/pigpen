package pi_subagents

import (
	"fmt"

	"github.com/dop251/goja"
)

// The workflow script sandbox: what the original runs in a worker thread inside a node:vm context (scripted-workflow.ts WORKER_SOURCE,
// lines 46-1092), on goja. The script sees exactly `runs`, `Promise`, `emit`, `console`, `args` and (with a mission) `state`
// beside the language's own built-ins; code generation from strings is disabled; every launched promise is tracked so that
// the host can tell an observed run from a forgotten one.

// wfPort is the worker's side of the original's parentPort: what the script half sends to the host half.
type wfPort interface {
	call(callID int, method string, args *jsObject)
	observed(callID int, key, operation string)
	emit(value any)
	console(level, text string)
	lanePlan(lanes any)
	complete(value any)
	fail(message, kind, phase string)
}

// wfObservation is one launch a tracked promise stands for.
type wfObservation struct {
	key       string
	operation string
	callID    int
}

// wfTracker records which launches a promise stands for and whether the script has consumed it (scripted-workflow.ts:104-233).
type wfTracker struct {
	observations []wfObservation
	consumed     bool
	deps         []*wfTracker
}

type wfPending struct {
	resolve func(any) error
	reject  func(any) error
}

// wfSandbox is one script run. All fields belong to the script goroutine.
type wfSandbox struct {
	vm    *goja.Runtime
	realm *wfRealm
	port  wfPort
	q     *workflowLoop // jobs of the script goroutine

	nextCallID int
	pending    map[int]*wfPending
	errorKinds map[*goja.Object]string

	trackers        map[*goja.Object]*wfTracker
	targets         map[*goja.Object]*goja.Object
	observedCallIDs map[int]bool

	runFingerprints  map[string]string
	hostKeys         map[string]bool
	hostKeyCount     int
	warnedPermissive bool
	runsAllTargets   map[*goja.Object]*goja.Object

	arrayFrom goja.Callable
	promiseAll,
	promiseAllSettled,
	promiseRace,
	promiseAny,
	promiseResolve,
	promiseConstruct goja.Callable

	topPromise *goja.Promise
	topHandled bool
	unhandled  []*goja.Promise
	finished   bool
	depth      int
}

const (
	wfMaxLanes          = 32
	wfMaxLaneStages     = 16
	wfMaxLaneStageCount = 64
	wfMaxLaneSpecBytes  = 64 * 1024
	wfMaxLaneTaskBytes  = 1024 * 1024
	wfMaxLanePathBytes  = 32 * 1024
	wfMaxLaneBoardText  = 256
	wfMaxCallStack      = 10000
)

// workflowNestedAsyncError is NESTED_ASYNC_WORKFLOW_ERROR (scripted-workflow.ts:885).
const workflowNestedAsyncError = "workflowScript validation failed before child launch; no children launched. workflowScript does not support nested async functions. Use top-level await, plain helper functions that return runs.run(...), or explicit Promise chains so workflows stay portable across Node and Bun. Parallel plus sequential rewrite: const a = runs.run(\"a\", { agent: \"worker\", task: \"A\" }); const writer = await runs.run(\"writer\", { agent: \"worker\", task: \"Write\" }); const review = await runs.run(\"review\", { agent: \"reviewer\", task: writer.output }); const [aResult] = await Promise.all([a]); return { a: aResult.output, issue: { writerRunId: writer.runId, reviewRunId: review.runId } };"

// workflowHardening runs before the script. goja cannot switch code generation off (the original's
// `codeGeneration: { strings: false }`), so every route to eval and the Function constructors is replaced.
const workflowHardening = `(function (global) {
  "use strict";
  var deny = function () { throw new EvalError("Code generation from strings disallowed for this context"); };
  var hide = function (target, key, value) {
    Object.defineProperty(target, key, { value: value, writable: true, configurable: true, enumerable: false });
  };
  var fnProtos = [Function.prototype, Object.getPrototypeOf(async function () {}), Object.getPrototypeOf(function* () {})];
  var denyCtor = function Function() { return deny(); };
  var denyAsync = function AsyncFunction() { return deny(); };
  var denyGen = function GeneratorFunction() { return deny(); };
  hide(Function.prototype, "constructor", denyCtor);
  hide(fnProtos[1], "constructor", denyAsync);
  hide(fnProtos[2], "constructor", denyGen);
  Object.defineProperty(denyCtor, "prototype", { value: Function.prototype });
  hide(global, "Function", denyCtor);
  var evalFn = function () { return deny(); };
  Object.defineProperty(evalFn, "name", { value: "eval" });
  hide(global, "eval", evalFn);
  // goja has no Intl: Number.prototype.toLocaleString gives en-US grouping (the minimum and maximum fraction digit options are honored)
  hide(Number.prototype, "toLocaleString", function toLocaleString(locales, options) {
    var n = Number.prototype.valueOf.call(this);
    if (n !== n) return "NaN";
    if (n === Infinity) return "\u221e";
    if (n === -Infinity) return "-\u221e";
    var minF = 0, maxF = 3;
    if (options && typeof options === "object") {
      if (options.minimumFractionDigits !== undefined) { minF = Number(options.minimumFractionDigits); if (options.maximumFractionDigits === undefined && maxF < minF) maxF = minF; }
      if (options.maximumFractionDigits !== undefined) { maxF = Number(options.maximumFractionDigits); if (minF > maxF) minF = maxF; }
    }
    var neg = n < 0 || (n === 0 && 1 / n < 0);
    var abs = Math.abs(n);
    var fixed = abs >= 1e21 ? String(abs) : abs.toFixed(maxF);
    var parts = fixed.split(".");
    var frac = parts[1] || "";
    while (frac.length > minF && frac.charAt(frac.length - 1) === "0") frac = frac.slice(0, -1);
    var whole = parts[0].replace(/\B(?=(\d{3})+(?!\d))/g, ",");
    return (neg ? "-" : "") + whole + (frac ? "." + frac : "");
  });
  if (typeof Array.fromAsync === "undefined") {
    hide(Array, "fromAsync", async function fromAsync(items, mapFn, thisArg) {
      if (mapFn !== undefined && typeof mapFn !== "function") throw new TypeError(String(mapFn) + " is not a function");
      var out = [];
      var i = 0;
      if (items != null && (items[Symbol.asyncIterator] != null || items[Symbol.iterator] != null)) {
        for await (var v of items) { out.push(mapFn ? await mapFn.call(thisArg, v, i) : v); i++; }
      } else {
        var arrayLike = Object(items);
        var len = Math.min(Math.max(Math.trunc(Number(arrayLike.length)) || 0, 0), Number.MAX_SAFE_INTEGER);
        for (; i < len; i++) { var e = await arrayLike[i]; out.push(mapFn ? await mapFn.call(thisArg, e, i) : e); }
      }
      return out;
    });
  }
  if (typeof global.WeakRef === "undefined") {
    var WeakRef = function WeakRef(target) {
      if (new.target === undefined) throw new TypeError("Constructor WeakRef requires 'new'");
      if (target === null || (typeof target !== "object" && typeof target !== "function")) throw new TypeError("WeakRef: invalid target");
      Object.defineProperty(this, "__target", { value: target });
    };
    Object.defineProperty(WeakRef.prototype, "deref", { value: function deref() { return this.__target; }, writable: true, configurable: true });
    Object.defineProperty(WeakRef.prototype, Symbol.toStringTag, { value: "WeakRef", configurable: true });
    hide(global, "WeakRef", WeakRef);
  }
  if (typeof global.FinalizationRegistry === "undefined") {
    var FinalizationRegistry = function FinalizationRegistry(cleanup) {
      if (new.target === undefined) throw new TypeError("Constructor FinalizationRegistry requires 'new'");
      if (typeof cleanup !== "function") throw new TypeError("FinalizationRegistry: cleanup must be callable");
    };
    Object.defineProperty(FinalizationRegistry.prototype, "register", { value: function register() {}, writable: true, configurable: true });
    Object.defineProperty(FinalizationRegistry.prototype, "unregister", { value: function unregister() { return false; }, writable: true, configurable: true });
    Object.defineProperty(FinalizationRegistry.prototype, Symbol.toStringTag, { value: "FinalizationRegistry", configurable: true });
    hide(global, "FinalizationRegistry", FinalizationRegistry);
  }
})(this);`

type wfStart struct {
	script       string
	args         *jsObject
	stateEnabled bool
}

// newWfSandbox builds the realm on the script goroutine.
func newWfSandbox(port wfPort, q *workflowLoop) *wfSandbox { panic("not implemented") }

func (s *wfSandbox) rejectionTracker(p *goja.Promise, op goja.PromiseRejectionOperation) {
	panic("not implemented")
}

func (s *wfSandbox) str(v string) goja.Value { panic("not implemented") }

// throwError throws a new Error(message) in the script's realm.
func (s *wfSandbox) throwError(message string) { panic("not implemented") }

func (s *wfSandbox) newError(message string) *goja.Object { panic("not implemented") }

// native wraps a Go function as a script function.
func (s *wfSandbox) native(name string, length int, fn func(call goja.FunctionCall) goja.Value) *goja.Object {
	panic("not implemented")
}

// ---- Go <-> script values ----

// toJS converts a host value (the JSON data the original sends through postMessage) to a script value.
func (s *wfSandbox) toJS(v any) goja.Value { panic("not implemented") }

// toGo converts a script value that passed assertJsonValue to host data.
func (s *wfSandbox) toGo(v goja.Value) any { panic("not implemented") }

// assertJsonValue is assertJsonValue (scripted-workflow.ts:920-941); it returns the message of the first violation.
func (s *wfSandbox) assertJsonValue(v goja.Value, path string, seen map[*goja.Object]bool) string {
	panic("not implemented")
}

func (s *wfSandbox) checkJSON(v goja.Value, path string) { panic("not implemented") }

func (s *wfSandbox) isPlainObject(v goja.Value) bool { panic("not implemented") }

// unwrapRunsAllResults strips the runs.all array proxy (scripted-workflow.ts:949).
func (s *wfSandbox) unwrapRunsAllResults(v goja.Value, seen map[*goja.Object]goja.Value) goja.Value {
	panic(
		// a cycle of plain objects recurses without end in the original too, and ends in a RangeError there
		"not implemented")
}

// omitUndefined is omitUndefinedWorkflowValues (scripted-workflow.ts:974).
func (s *wfSandbox) omitUndefined(v goja.Value, seen map[*goja.Object]bool) goja.Value {
	panic("not implemented")
}

func (s *wfSandbox) deepFreeze(v goja.Value) { panic("not implemented") }

// ---- promise tracking (scripted-workflow.ts:57-281) ----

// directScriptCall reports whether the running native function was called by script code itself, as opposed to the
// engine resolving a thenable or a combinator: the original's isDirectWorkflowScriptPromiseHandlerCall.
func (s *wfSandbox) directScriptCall() bool { panic("not implemented") }

func (s *wfSandbox) promiseTracker(v goja.Value) *wfTracker { panic("not implemented") }

func (s *wfSandbox) promiseTarget(v goja.Value) goja.Value { panic("not implemented") }

func (s *wfSandbox) addDependency(t, dep *wfTracker) { panic("not implemented") }

func (s *wfSandbox) addObservations(t *wfTracker, obs []wfObservation) { panic("not implemented") }

func (s *wfSandbox) markConsumed(t *wfTracker, seen map[*wfTracker]bool) { panic("not implemented") }

// consume is consumeTrackedObservations: a then/catch/finally the script itself wrote does not consume (only what is chained
// from it can), the engine's own (await, thenable resolution) does.
func (s *wfSandbox) consume(t *wfTracker) { panic("not implemented") }

// callFn calls f and rethrows a script exception as one.
func (s *wfSandbox) callFn(f goja.Callable, this goja.Value, args ...goja.Value) goja.Value {
	panic("not implemented")
}

// trackPromise is trackObservationTracker (scripted-workflow.ts:195): a proxy over the native promise whose then/catch/finally
// consume the tracker and return new tracked promises chained to it.
func (s *wfSandbox) trackPromise(t *wfTracker, promise goja.Value, allowFuture bool) goja.Value {
	panic("not implemented")
}

func (s *wfSandbox) callableOf(v goja.Value) goja.Callable { panic("not implemented") }

// trackRun is trackRunObservation.
func (s *wfSandbox) trackRun(obs []wfObservation, promise goja.Value) goja.Value {
	panic("not implemented")
}

// trackCombinator is trackPromiseCombinator.
func (s *wfSandbox) trackCombinator(items goja.Value, create func(values goja.Value) goja.Value) goja.Value {
	panic("not implemented")
}

// workflowPromise is the sandbox's Promise: the real one behind a proxy that tracks promises created, combined and resolved
// through it (scripted-workflow.ts:243-281).
func (s *wfSandbox) workflowPromise() goja.Value { panic("not implemented") }

// the engine's instanceof asks the proxy itself, which it cannot answer

func (s *wfSandbox) rethrow(err error) { panic("not implemented") }

// ---- host calls ----

// hostCall is hostCall (scripted-workflow.ts:283): a pending promise answered by the host.
func (s *wfSandbox) hostCall(method string, args *jsObject, observe *wfObservation) goja.Value {
	panic("not implemented")
}

type wfLaunched struct {
	key     string
	callID  int
	promise goja.Value
}

// runHostCall is runHostCall (scripted-workflow.ts:294).
func (s *wfSandbox) runHostCall(key string, params *jsObject, collectFailure bool, batch *jsObject, generatedLaneKey string) wfLaunched {
	panic("not implemented")
}

// respond delivers the host's answer to a pending call (the "response" message, scripted-workflow.ts:994).
func (s *wfSandbox) respond(callID int, ok bool, value any, errMessage, errKind string) {
	panic("not implemented")
}

// ---- run params ----

func stableRunJSON(v any) string { panic("not implemented") }

// canonicalRunParams drops `acceptance: false` from a gated launch (scripted-workflow.ts:92).
func canonicalRunParams(params *jsObject) *jsObject { panic("not implemented") }

func (s *wfSandbox) ownHas(o *goja.Object, name string) bool { panic("not implemented") }

func (s *wfSandbox) validateKey(v goja.Value, owner string) string { panic("not implemented") }

func laneByteLength(v string) int { panic("not implemented") }

func (s *wfSandbox) laneBoardText(v string) string { panic("not implemented") }

// TextDecoder replaces a cut sequence with U+FFFD

func (s *wfSandbox) laneBoardPath(v string) string { panic("not implemented") }

var wfGateKeys = map[string]bool{"command": true, "output": true, "schema": true, "timeoutMs": true}

// gateShapeError is describeGateShapeError (scripted-workflow.ts:639), a mirror of parseGateInput.
func (s *wfSandbox) gateShapeError(gate goja.Value) string { panic("not implemented") }

func (s *wfSandbox) describeGateAcceptanceConflict(gate, acceptance goja.Value) string {
	panic("not implemented")
}

func (s *wfSandbox) validateExtensionBindings(v goja.Value, label string) { panic("not implemented") }

func (s *wfSandbox) validateLaneMetadata(v goja.Value, label, workflowKey string, hasKey bool) {
	panic("not implemented")
}

func isJSNumberEq(v goja.Value, n float64) bool { panic("not implemented") }

// validateRunCall is validateRunCall (scripted-workflow.ts:669-706).
func (s *wfSandbox) validateRunCall(keyV, paramsV goja.Value, label string, fingerprints map[string]string) {
	panic("not implemented")
}

// decorateChild gives a child result its non-enumerable toString (scripted-workflow.ts:587).
func (s *wfSandbox) decorateChild(v goja.Value) goja.Value { panic("not implemented") }

func (s *wfSandbox) formatRef(v goja.Value) string { panic("not implemented") }

func (s *wfSandbox) formatChildResultString(v goja.Value) string { panic("not implemented") }

// utf16Slice is str.slice(0, n) in UTF-16 code units.
func utf16Slice(str string, n int) []rune { panic("not implemented") }

var _ = fmt.Sprint
