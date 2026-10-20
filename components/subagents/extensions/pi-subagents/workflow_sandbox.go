package pi_subagents

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

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
func newWfSandbox(port wfPort, q *workflowLoop) *wfSandbox {
	vm := goja.New()
	vm.SetMaxCallStackSize(wfMaxCallStack)
	s := &wfSandbox{
		vm: vm, port: port, q: q,
		pending:         map[int]*wfPending{},
		errorKinds:      map[*goja.Object]string{},
		trackers:        map[*goja.Object]*wfTracker{},
		targets:         map[*goja.Object]*goja.Object{},
		observedCallIDs: map[int]bool{},
		runFingerprints: map[string]string{},
		hostKeys:        map[string]bool{},
		runsAllTargets:  map[*goja.Object]*goja.Object{},
	}
	s.realm = newWfRealm(vm)
	r := s.realm
	s.arrayFrom = r.mustFn("Array.from")
	s.promiseAll = r.mustFn("Promise.all")
	s.promiseAllSettled = r.mustFn("Promise.allSettled")
	s.promiseRace = r.mustFn("Promise.race")
	s.promiseAny = r.mustFn("Promise.any")
	s.promiseResolve = r.mustFn("Promise.resolve")
	vm.SetPromiseRejectionTracker(s.rejectionTracker)
	return s
}

func (s *wfSandbox) rejectionTracker(p *goja.Promise, op goja.PromiseRejectionOperation) {
	if op == goja.PromiseRejectionReject {
		s.unhandled = append(s.unhandled, p)
		return
	}
	for i, u := range s.unhandled {
		if u == p {
			s.unhandled = append(s.unhandled[:i], s.unhandled[i+1:]...)
			return
		}
	}
}

func (s *wfSandbox) str(v string) goja.Value { return s.vm.ToValue(v) }

// throwError throws a new Error(message) in the script's realm.
func (s *wfSandbox) throwError(message string) {
	panic(s.newError(message))
}

func (s *wfSandbox) newError(message string) *goja.Object {
	ctor := s.vm.Get("Error").ToObject(s.vm)
	o, err := s.vm.New(ctor, s.str(message))
	if err != nil {
		panic(err)
	}
	return o
}

// native wraps a Go function as a script function.
func (s *wfSandbox) native(name string, length int, fn func(call goja.FunctionCall) goja.Value) *goja.Object {
	f := s.vm.ToValue(fn).ToObject(s.vm)
	_ = f.DefineDataProperty("name", s.str(name), goja.FLAG_FALSE, goja.FLAG_TRUE, goja.FLAG_FALSE)
	_ = f.DefineDataProperty("length", s.vm.ToValue(length), goja.FLAG_FALSE, goja.FLAG_TRUE, goja.FLAG_FALSE)
	return f
}

// ---- Go <-> script values ----

// toJS converts a host value (the JSON data the original sends through postMessage) to a script value.
func (s *wfSandbox) toJS(v any) goja.Value {
	switch x := v.(type) {
	case nil:
		return goja.Null()
	case undef:
		return goja.Undefined()
	case *jsObject:
		o := s.vm.NewObject()
		for _, k := range x.order() {
			_ = o.DefineDataProperty(k, s.toJS(x.vals[k]), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE)
		}
		return o
	case []any:
		items := make([]any, len(x))
		for i, e := range x {
			items[i] = s.toJS(e)
		}
		return s.vm.NewArray(items...)
	case string:
		return s.str(x)
	case bool:
		return s.vm.ToValue(x)
	case float64:
		return s.vm.ToValue(x)
	case int:
		return s.vm.ToValue(float64(x))
	case int64:
		return s.vm.ToValue(float64(x))
	}
	return s.vm.ToValue(v)
}

// toGo converts a script value that passed assertJsonValue to host data.
func (s *wfSandbox) toGo(v goja.Value) any {
	if v == nil || isUndef(v) {
		return undef{}
	}
	if goja.IsNull(v) {
		return nil
	}
	if o, ok := v.(*goja.Object); ok {
		if s.realm.array(o) {
			n := int(o.Get("length").ToInteger())
			out := make([]any, n)
			for i := range n {
				out[i] = s.toGo(o.Get(strconv.Itoa(i)))
			}
			return out
		}
		obj := newObject()
		for _, k := range s.realm.keysOf(o) {
			obj.set(k, s.toGo(o.Get(k)))
		}
		return obj
	}
	switch jsTypeof(v) {
	case "string":
		return v.String()
	case "boolean":
		return v.ToBoolean()
	case "number":
		return v.ToFloat()
	}
	return v.String()
}

// assertJsonValue is assertJsonValue (scripted-workflow.ts:920-941); it returns the message of the first violation.
func (s *wfSandbox) assertJsonValue(v goja.Value, path string, seen map[*goja.Object]bool) string {
	if goja.IsNull(v) {
		return ""
	}
	t := jsTypeof(v)
	switch t {
	case "string", "boolean":
		return ""
	case "number":
		if f := v.ToFloat(); math.IsNaN(f) || math.IsInf(f, 0) {
			return path + " must contain only finite JSON numbers."
		}
		return ""
	}
	if t != "object" {
		return path + " must be a JSON value; received " + t + "."
	}
	obj := v.(*goja.Object)
	if seen[obj] {
		return path + " must not contain cycles."
	}
	seen[obj] = true
	defer delete(seen, obj)
	if s.realm.array(obj) {
		n := int(obj.Get("length").ToInteger())
		for i := range n {
			if !s.realm.hasOwnProp(obj, strconv.Itoa(i)) {
				return path + " must not contain sparse array entries."
			}
			if m := s.assertJsonValue(obj.Get(strconv.Itoa(i)), path+"["+strconv.Itoa(i)+"]", seen); m != "" {
				return m
			}
		}
		return ""
	}
	proto := s.realm.protoOf(obj)
	if proto != nil && !proto.SameAs(s.realm.objectProto) {
		return path + " must contain only plain JSON objects."
	}
	if len(s.realm.symbolKeys(obj)) > 0 {
		return path + " must not contain symbol keys."
	}
	for _, k := range s.realm.keysOf(obj) {
		if m := s.assertJsonValue(obj.Get(k), path+"."+k, seen); m != "" {
			return m
		}
	}
	return ""
}

func (s *wfSandbox) checkJSON(v goja.Value, path string) {
	if m := s.assertJsonValue(v, path, map[*goja.Object]bool{}); m != "" {
		s.throwError(m)
	}
}

func (s *wfSandbox) isPlainObject(v goja.Value) bool {
	o, ok := v.(*goja.Object)
	if !ok || goja.IsNull(v) || s.realm.array(o) {
		return false
	}
	if _, callable := goja.AssertFunction(o); callable {
		return false
	}
	p := s.realm.protoOf(o)
	return p == nil || p.SameAs(s.realm.objectProto)
}

// unwrapRunsAllResults strips the runs.all array proxy (scripted-workflow.ts:949).
func (s *wfSandbox) unwrapRunsAllResults(v goja.Value, seen map[*goja.Object]goja.Value) goja.Value {
	// a cycle of plain objects recurses without end in the original too, and ends in a RangeError there
	s.depth++
	defer func() { s.depth-- }()
	if s.depth > 2000 {
		ctor := s.vm.Get("RangeError").ToObject(s.vm)
		o, _ := s.vm.New(ctor, s.str("Maximum call stack size exceeded"))
		panic(o)
	}
	o, ok := v.(*goja.Object)
	if !ok || goja.IsNull(v) {
		return v
	}
	target, wrapped := s.runsAllTargets[o]
	if !wrapped {
		target = o
	}
	if c, ok := seen[target]; ok {
		return c
	}
	if s.realm.array(target) {
		n := int(target.Get("length").ToInteger())
		out := s.vm.NewArray()
		seen[target] = out
		changed := wrapped
		for i := range n {
			idx := strconv.Itoa(i)
			e := target.Get(idx)
			if e == nil {
				e = goja.Undefined()
			}
			u := s.unwrapRunsAllResults(e, seen)
			_ = out.Set(idx, u)
			if !u.SameAs(e) {
				changed = true
			}
		}
		if changed {
			return out
		}
		return target
	}
	if !s.isPlainObject(target) || len(s.realm.symbolKeys(target)) > 0 {
		return target
	}
	changed := false
	type kv struct {
		k string
		v goja.Value
	}
	var entries []kv
	for _, k := range s.realm.keysOf(target) {
		e := target.Get(k)
		if e == nil {
			e = goja.Undefined()
		}
		u := s.unwrapRunsAllResults(e, seen)
		if !u.SameAs(e) {
			changed = true
		}
		entries = append(entries, kv{k, u})
	}
	if !changed {
		return target
	}
	out := s.vm.NewObject()
	for _, e := range entries {
		_ = out.DefineDataProperty(e.k, e.v, goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE)
	}
	return out
}

// omitUndefined is omitUndefinedWorkflowValues (scripted-workflow.ts:974).
func (s *wfSandbox) omitUndefined(v goja.Value, seen map[*goja.Object]bool) goja.Value {
	o, ok := v.(*goja.Object)
	if !ok || goja.IsNull(v) {
		return v
	}
	if seen[o] {
		return v
	}
	seen[o] = true
	defer delete(seen, o)
	if s.realm.array(o) {
		n := int(o.Get("length").ToInteger())
		items := make([]any, n)
		for i := range n {
			e := o.Get(strconv.Itoa(i))
			if e == nil || isUndef(e) {
				items[i] = goja.Null()
			} else {
				items[i] = s.omitUndefined(e, seen)
			}
		}
		return s.vm.NewArray(items...)
	}
	if s.isPlainObject(o) && len(s.realm.symbolKeys(o)) == 0 {
		out := s.vm.NewObject()
		for _, k := range s.realm.keysOf(o) {
			e := o.Get(k)
			if e == nil || isUndef(e) {
				continue
			}
			_ = out.DefineDataProperty(k, s.omitUndefined(e, seen), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE)
		}
		return out
	}
	return v
}

func (s *wfSandbox) deepFreeze(v goja.Value) {
	o, ok := v.(*goja.Object)
	if !ok || goja.IsNull(v) {
		return
	}
	if s.realm.call(s.realm.isFrozen, goja.Undefined(), o).ToBoolean() {
		return
	}
	for _, k := range s.realm.keysOf(o) {
		s.deepFreeze(o.Get(k))
	}
	s.realm.call(s.realm.freeze, goja.Undefined(), o)
}

// ---- promise tracking (scripted-workflow.ts:57-281) ----

// directScriptCall reports whether the running native function was called by script code itself, as opposed to the
// engine resolving a thenable or a combinator: the original's isDirectWorkflowScriptPromiseHandlerCall.
func (s *wfSandbox) directScriptCall() bool {
	for _, f := range s.vm.CaptureCallStack(0, nil) {
		if f.SrcName() == "workflow-script.js" {
			return true
		}
	}
	return false
}

func (s *wfSandbox) promiseTracker(v goja.Value) *wfTracker {
	o, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	return s.trackers[o]
}

func (s *wfSandbox) promiseTarget(v goja.Value) goja.Value {
	if o, ok := v.(*goja.Object); ok {
		if t, ok := s.targets[o]; ok {
			return t
		}
	}
	return v
}

func (s *wfSandbox) addDependency(t, dep *wfTracker) {
	if dep == nil {
		return
	}
	for _, d := range t.deps {
		if d == dep {
			goto present
		}
	}
	t.deps = append(t.deps, dep)
present:
	if t.consumed {
		s.markConsumed(dep, map[*wfTracker]bool{})
	}
}

func (s *wfSandbox) addObservations(t *wfTracker, obs []wfObservation) {
	for _, o := range obs {
		dup := false
		for _, e := range t.observations {
			if e.callID == o.callID {
				dup = true
				break
			}
		}
		if !dup {
			t.observations = append(t.observations, o)
		}
	}
	if !t.consumed {
		return
	}
	for _, o := range t.observations {
		if s.observedCallIDs[o.callID] {
			continue
		}
		s.observedCallIDs[o.callID] = true
		s.port.observed(o.callID, o.key, o.operation)
	}
}

func (s *wfSandbox) markConsumed(t *wfTracker, seen map[*wfTracker]bool) {
	if seen[t] {
		return
	}
	seen[t] = true
	t.consumed = true
	s.addObservations(t, nil)
	for _, d := range t.deps {
		s.markConsumed(d, seen)
	}
}

// consume is consumeTrackedObservations: a then/catch/finally the script itself wrote does not consume (only what is chained
// from it can), the engine's own (await, thenable resolution) does.
func (s *wfSandbox) consume(t *wfTracker) {
	if s.directScriptCall() {
		return
	}
	s.markConsumed(t, map[*wfTracker]bool{})
}

// callFn calls f and rethrows a script exception as one.
func (s *wfSandbox) callFn(f goja.Callable, this goja.Value, args ...goja.Value) goja.Value {
	return s.realm.call(f, this, args...)
}

// trackPromise is trackObservationTracker (scripted-workflow.ts:195): a proxy over the native promise whose then/catch/finally
// consume the tracker and return new tracked promises chained to it.
func (s *wfSandbox) trackPromise(t *wfTracker, promise goja.Value, allowFuture bool) goja.Value {
	target := s.promiseTarget(promise)
	tobj, ok := target.(*goja.Object)
	if !ok {
		return promise
	}
	if _, callable := goja.AssertFunction(tobj.Get("then")); (!allowFuture && len(t.observations) == 0) || !callable {
		return promise
	}
	chain := func() *wfTracker { return &wfTracker{observations: t.observations, deps: []*wfTracker{t}} }
	wrap := func(h goja.Value, chainTracker *wfTracker) goja.Value {
		hf, ok := goja.AssertFunction(h)
		if !ok {
			return h
		}
		return s.native("trackedThenHandler", 1, func(call goja.FunctionCall) goja.Value {
			v := s.callFn(hf, call.This, call.Arguments...)
			if chainTracker != nil {
				s.addDependency(chainTracker, s.promiseTracker(v))
			}
			return s.promiseTarget(v)
		})
	}
	var proxy goja.Proxy
	proxy = s.vm.NewProxy(tobj, &goja.ProxyTrapConfig{
		Get: func(pt *goja.Object, prop string, receiver goja.Value) goja.Value {
			switch prop {
			case "then":
				return s.native("promiseThen", 2, func(call goja.FunctionCall) goja.Value {
					s.consume(t)
					ct := chain()
					res := s.callFn(s.realm.promiseThen, pt, wrap(call.Argument(0), ct), wrap(call.Argument(1), ct))
					return s.trackPromise(ct, res, true)
				})
			case "catch":
				return s.native("promiseCatch", 1, func(call goja.FunctionCall) goja.Value {
					s.consume(t)
					res := s.callFn(s.callableOf(pt.Get("catch")), pt, call.Argument(0))
					return s.trackPromise(chain(), res, true)
				})
			case "finally":
				return s.native("promiseFinally", 1, func(call goja.FunctionCall) goja.Value {
					s.consume(t)
					res := s.callFn(s.callableOf(pt.Get("finally")), pt, call.Argument(0))
					return s.trackPromise(chain(), res, true)
				})
			}
			return pt.Get(prop)
		},
	})
	pv := s.vm.ToValue(proxy)
	po := pv.ToObject(s.vm)
	s.trackers[po] = t
	s.targets[po] = tobj
	return pv
}

func (s *wfSandbox) callableOf(v goja.Value) goja.Callable {
	f, ok := goja.AssertFunction(v)
	if !ok {
		panic(s.vm.NewTypeError("not a function"))
	}
	return f
}

// trackRun is trackRunObservation.
func (s *wfSandbox) trackRun(obs []wfObservation, promise goja.Value) goja.Value {
	t := s.promiseTracker(promise)
	if t == nil {
		t = &wfTracker{}
	}
	s.addObservations(t, obs)
	return s.trackPromise(t, promise, false)
}

// trackCombinator is trackPromiseCombinator.
func (s *wfSandbox) trackCombinator(items goja.Value, create func(values goja.Value) goja.Value) goja.Value {
	arr := s.callFn(s.arrayFrom, goja.Undefined(), items).ToObject(s.vm)
	n := int(arr.Get("length").ToInteger())
	var deps []*wfTracker
	vals := make([]any, n)
	for i := range n {
		e := arr.Get(strconv.Itoa(i))
		if t := s.promiseTracker(e); t != nil {
			dup := false
			for _, d := range deps {
				if d == t {
					dup = true
				}
			}
			if !dup {
				deps = append(deps, t)
			}
		}
		vals[i] = s.promiseTarget(e)
	}
	promise := create(s.vm.NewArray(vals...))
	if len(deps) == 0 {
		return promise
	}
	return s.trackPromise(&wfTracker{deps: deps}, promise, true)
}

// workflowPromise is the sandbox's Promise: the real one behind a proxy that tracks promises created, combined and resolved
// through it (scripted-workflow.ts:243-281).
func (s *wfSandbox) workflowPromise() goja.Value {
	target := s.realm.promiseCtor
	bind := func(f goja.Callable) goja.Value {
		return s.native("", 0, func(call goja.FunctionCall) goja.Value { return s.callFn(f, target, call.Arguments...) })
	}
	hasInstance := s.native("[Symbol.hasInstance]", 1, func(call goja.FunctionCall) goja.Value {
		return s.vm.ToValue(s.vm.InstanceOf(call.Argument(0), target))
	})
	symHasInstance, _ := s.vm.RunString("Symbol.hasInstance")
	proxy := s.vm.NewProxy(target, &goja.ProxyTrapConfig{
		GetSym: func(pt *goja.Object, prop *goja.Symbol, receiver goja.Value) goja.Value {
			// the engine's instanceof asks the proxy itself, which it cannot answer
			if sym, ok := symHasInstance.(*goja.Symbol); ok && sym == prop {
				return hasInstance
			}
			return pt.GetSymbol(prop)
		},
		Construct: func(pt *goja.Object, args []goja.Value, newTarget *goja.Object) *goja.Object {
			var executor goja.Value = goja.Undefined()
			if len(args) > 0 {
				executor = args[0]
			}
			ef, ok := goja.AssertFunction(executor)
			if !ok {
				o, err := s.vm.New(pt, args...)
				if err != nil {
					s.rethrow(err)
				}
				return o
			}
			t := &wfTracker{}
			settled := false
			inner := s.native("", 2, func(call goja.FunctionCall) goja.Value {
				resolve, reject := call.Argument(0), call.Argument(1)
				rf, _ := goja.AssertFunction(resolve)
				jf, _ := goja.AssertFunction(reject)
				_, err := ef(goja.Undefined(),
					s.native("resolve", 1, func(c goja.FunctionCall) goja.Value {
						if settled {
							return goja.Undefined()
						}
						settled = true
						v := c.Argument(0)
						s.addDependency(t, s.promiseTracker(v))
						return s.callFn(rf, goja.Undefined(), s.promiseTarget(v))
					}),
					s.native("reject", 1, func(c goja.FunctionCall) goja.Value {
						if settled {
							return goja.Undefined()
						}
						settled = true
						return s.callFn(jf, goja.Undefined(), c.Argument(0))
					}))
				if err != nil {
					settled = true
					s.rethrow(err)
				}
				return goja.Undefined()
			})
			p, err := s.vm.New(pt, inner)
			if err != nil {
				s.rethrow(err)
			}
			return s.trackPromise(t, p, true).ToObject(s.vm)
		},
		Get: func(pt *goja.Object, prop string, receiver goja.Value) goja.Value {
			switch prop {
			case "all":
				return s.native("all", 1, func(c goja.FunctionCall) goja.Value {
					return s.trackCombinator(c.Argument(0), func(vals goja.Value) goja.Value { return s.callFn(s.promiseAll, pt, vals) })
				})
			case "allSettled":
				return s.native("allSettled", 1, func(c goja.FunctionCall) goja.Value {
					return s.trackCombinator(c.Argument(0), func(vals goja.Value) goja.Value { return s.callFn(s.promiseAllSettled, pt, vals) })
				})
			case "race":
				return s.native("race", 1, func(c goja.FunctionCall) goja.Value {
					return s.trackCombinator(c.Argument(0), func(vals goja.Value) goja.Value { return s.callFn(s.promiseRace, pt, vals) })
				})
			case "any":
				return s.native("any", 1, func(c goja.FunctionCall) goja.Value {
					return s.trackCombinator(c.Argument(0), func(vals goja.Value) goja.Value { return s.callFn(s.promiseAny, pt, vals) })
				})
			case "resolve":
				return s.native("resolve", 1, func(c goja.FunctionCall) goja.Value {
					v := c.Argument(0)
					dep := s.promiseTracker(v)
					p := s.callFn(s.promiseResolve, pt, s.promiseTarget(v))
					if dep == nil {
						return p
					}
					return s.trackPromise(&wfTracker{deps: []*wfTracker{dep}}, p, true)
				})
			}
			v := pt.Get(prop)
			if f, ok := goja.AssertFunction(v); ok {
				if _, isClass := v.(*goja.Object); isClass && prop != "prototype" {
					return bind(f)
				}
			}
			return v
		},
	})
	return s.vm.ToValue(proxy)
}

func (s *wfSandbox) rethrow(err error) {
	if ex, ok := err.(*goja.Exception); ok {
		panic(ex.Value())
	}
	panic(s.vm.NewGoError(err))
}

// ---- host calls ----

// hostCall is hostCall (scripted-workflow.ts:283): a pending promise answered by the host.
func (s *wfSandbox) hostCall(method string, args *jsObject, observe *wfObservation) goja.Value {
	s.nextCallID++
	id := s.nextCallID
	p, resolve, reject := s.vm.NewPromise()
	s.pending[id] = &wfPending{resolve: resolve, reject: reject}
	s.port.call(id, method, args)
	var pv goja.Value = s.vm.ToValue(p)
	if observe != nil {
		obs := *observe
		obs.callID = id
		return s.trackRun([]wfObservation{obs}, pv)
	}
	return pv
}

type wfLaunched struct {
	key     string
	callID  int
	promise goja.Value
}

// runHostCall is runHostCall (scripted-workflow.ts:294).
func (s *wfSandbox) runHostCall(key string, params *jsObject, collectFailure bool, batch *jsObject, generatedLaneKey string) wfLaunched {
	s.nextCallID++
	id := s.nextCallID
	p, resolve, reject := s.vm.NewPromise()
	s.pending[id] = &wfPending{resolve: resolve, reject: reject}
	args := newObject()
	args.set("key", key)
	args.set("params", params)
	if collectFailure {
		args.set("collectFailure", true)
	}
	if batch != nil {
		args.set("batch", batch)
	}
	if generatedLaneKey != "" {
		args.set("generatedLaneKey", generatedLaneKey)
	}
	s.port.call(id, "run", args)
	return wfLaunched{key: key, callID: id, promise: s.vm.ToValue(p)}
}

// respond delivers the host's answer to a pending call (the "response" message, scripted-workflow.ts:994).
func (s *wfSandbox) respond(callID int, ok bool, value any, errMessage, errKind string) {
	entry := s.pending[callID]
	if entry == nil {
		return
	}
	delete(s.pending, callID)
	if ok {
		_ = entry.resolve(s.toJS(value))
		return
	}
	e := s.newError(errMessage)
	if errKind == "child" || errKind == "detached-child" {
		s.errorKinds[e] = errKind
	}
	_ = entry.reject(e)
}

// ---- run params ----

func stableRunJSON(v any) string {
	switch x := v.(type) {
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = stableRunJSON(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case *jsObject:
		keys := make([]string, 0, len(x.vals))
		for k, val := range x.vals {
			if _, u := val.(undef); u {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = marshalJSON(k, "") + ":" + stableRunJSON(x.vals[k])
		}
		return "{" + strings.Join(parts, ",") + "}"
	case undef:
		return "undefined"
	}
	return marshalJSON(v, "")
}

// canonicalRunParams drops `acceptance: false` from a gated launch (scripted-workflow.ts:92).
func canonicalRunParams(params *jsObject) *jsObject {
	if !params.has("gate") {
		return params
	}
	if b, ok := params.flag("acceptance"); !ok || b {
		return params
	}
	c := newObject()
	for _, k := range params.keys {
		if k != "acceptance" {
			c.set(k, params.vals[k])
		}
	}
	return c
}

func (s *wfSandbox) ownHas(o *goja.Object, name string) bool { return s.realm.hasOwnProp(o, name) }

func (s *wfSandbox) validateKey(v goja.Value, owner string) string {
	k, ok := isJSString(v)
	if !ok || !workflowKeyPattern.MatchString(k) {
		s.throwError(owner + " key must be 1-128 characters using letters, numbers, '.', '_' or '-', and start with a letter or number.")
	}
	return k
}

func laneByteLength(v string) int { return len(v) }

func (s *wfSandbox) laneBoardText(v string) string {
	if v == "" {
		return ""
	}
	if laneByteLength(v) <= wfMaxLaneBoardText {
		return v
	}
	b := []byte(v)[:wfMaxLaneBoardText-3]
	// TextDecoder replaces a cut sequence with U+FFFD
	return strings.ToValidUTF8(string(b), "\ufffd") + "..."
}

func (s *wfSandbox) laneBoardPath(v string) string {
	if v == "" || laneByteLength(v) > wfMaxLaneBoardText {
		return ""
	}
	return v
}

var wfGateKeys = map[string]bool{"command": true, "output": true, "schema": true, "timeoutMs": true}

// gateShapeError is describeGateShapeError (scripted-workflow.ts:639), a mirror of parseGateInput.
func (s *wfSandbox) gateShapeError(gate goja.Value) string {
	shape := "gate must be a non-empty command string or { command, output?: \"json\", schema?, timeoutMs? }."
	if str, ok := isJSString(gate); ok {
		if jsTrim(str) != "" {
			return ""
		}
		return shape
	}
	o, ok := gate.(*goja.Object)
	if !ok || goja.IsNull(gate) || s.realm.array(o) {
		return shape
	}
	for _, k := range s.realm.keysOf(o) {
		if !wfGateKeys[k] {
			return "gate." + k + " is not supported."
		}
	}
	if cmd, ok := isJSString(o.Get("command")); !ok || jsTrim(cmd) == "" {
		return shape
	}
	output := o.Get("output")
	if !isUndef(output) {
		if out, ok := isJSString(output); !ok || out != "json" {
			return "gate.output must be \"json\" when present."
		}
	}
	if schema := o.Get("schema"); !isUndef(schema) {
		if out, ok := isJSString(output); !ok || out != "json" {
			return "gate.schema requires gate.output: \"json\"."
		}
		so, ok := schema.(*goja.Object)
		if !ok || goja.IsNull(schema) || s.realm.array(so) {
			return "gate.schema must be a JSON Schema object."
		}
	}
	if to := o.Get("timeoutMs"); !isUndef(to) {
		if !isJSInteger(to) || to.ToFloat() < 1 {
			return "gate.timeoutMs must be an integer >= 1."
		}
	}
	return ""
}

func (s *wfSandbox) describeGateAcceptanceConflict(gate, acceptance goja.Value) string {
	render := func(v goja.Value) (out string) {
		defer func() {
			if recover() != nil {
				out = s.realm.safeString(v)
			}
		}()
		r, err := s.realm.jsonStringfy(goja.Undefined(), v)
		if err != nil || isUndef(r) {
			return s.realm.safeString(v)
		}
		e := r.String()
		if jsLen(e) > 120 {
			e = string(utf16Slice(e, 120)) + "..."
		}
		return e
	}
	return " Both fields were present: gate=" + render(gate) + " acceptance=" + render(acceptance) + "."
}

func (s *wfSandbox) validateExtensionBindings(v goja.Value, label string) {
	if isUndef(v) {
		return
	}
	o, ok := v.(*goja.Object)
	if !ok || goja.IsNull(v) || s.realm.array(o) {
		s.throwError(label + " extensionBindings must be a plain JSON object.")
	}
	keys := s.realm.keysOf(o)
	if len(keys) > 16 {
		s.throwError(label + " extensionBindings supports at most 16 namespaces.")
	}
	for _, k := range keys {
		if !workflowBindingNamespace.MatchString(k) {
			s.throwError(label + " extensionBindings namespace '" + k + "' must use a package-like name followed by '/<positive-version>'.")
		}
	}
	s.checkJSON(v, label+" extensionBindings")
	count := 0
	var visit func(e goja.Value, depth int)
	visit = func(e goja.Value, depth int) {
		eo, ok := e.(*goja.Object)
		if !ok || goja.IsNull(e) {
			return
		}
		if depth > 16 {
			s.throwError(label + " extensionBindings exceeds the maximum nesting depth of 16.")
		}
		if s.realm.array(eo) {
			n := int(eo.Get("length").ToInteger())
			for i := range n {
				visit(eo.Get(strconv.Itoa(i)), depth+1)
			}
			return
		}
		for _, k := range s.realm.keysOf(eo) {
			count++
			if count > 256 {
				s.throwError(label + " extensionBindings exceeds 256 total properties.")
			}
			visit(eo.Get(k), depth+1)
		}
	}
	visit(v, 0)
	if len(stableRunJSON(s.toGo(v))) > 16384 {
		s.throwError(label + " extensionBindings canonical JSON exceeds 16384 bytes.")
	}
}

func (s *wfSandbox) validateLaneMetadata(v goja.Value, label, workflowKey string, hasKey bool) {
	if isUndef(v) {
		return
	}
	o, ok := v.(*goja.Object)
	if !ok || goja.IsNull(v) || s.realm.array(o) {
		s.throwError(label + " must be a plain JSON object.")
	}
	allowed := map[string]bool{"version": true, "key": true, "mode": true, "sourceRef": true, "claims": true, "outputPaths": true}
	var unknown []string
	for _, k := range s.realm.keysOf(o) {
		if !allowed[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		s.throwError(label + " has unsupported fields: " + strings.Join(unknown, ", ") + ".")
	}
	if ver := o.Get("version"); !isJSNumberEq(ver, 1) {
		s.throwError(label + ".version must be 1.")
	}
	key, ok := isJSString(o.Get("key"))
	if !ok || jsTrim(key) == "" || !workflowKeyPattern.MatchString(jsTrim(key)) {
		s.throwError(label + ".key is invalid.")
	}
	if hasKey && jsTrim(key) != workflowKey {
		s.throwError(label + ".key must match workflow key '" + workflowKey + "'.")
	}
	if mode := o.Get("mode"); !isUndef(mode) {
		m, ok := isJSString(mode)
		if !ok || (m != "mutation" && m != "review" && m != "scout" && m != "gate") {
			s.throwError(label + ".mode is invalid.")
		}
	}
	bounded := func(e goja.Value, maxBytes int) bool {
		str, ok := isJSString(e)
		return ok && jsTrim(str) != "" && len(jsTrim(str)) <= maxBytes && !strings.ContainsAny(str, "\r\n\x00")
	}
	if sr := o.Get("sourceRef"); !isUndef(sr) && !bounded(sr, 128) {
		s.throwError(label + ".sourceRef is invalid.")
	}
	for _, f := range []struct {
		name     string
		maxItems int
		maxLen   int
	}{{"claims", 20, 160}, {"outputPaths", 10, 256}} {
		val := o.Get(f.name)
		if isUndef(val) {
			continue
		}
		vo, ok := val.(*goja.Object)
		invalid := !ok || !s.realm.array(vo)
		if !invalid {
			n := int(vo.Get("length").ToInteger())
			invalid = n > f.maxItems
			for i := 0; i < n && !invalid; i++ {
				invalid = !bounded(vo.Get(strconv.Itoa(i)), f.maxLen)
			}
		}
		if invalid {
			s.throwError(label + "." + f.name + " is invalid.")
		}
	}
}

func isJSNumberEq(v goja.Value, n float64) bool {
	f, ok := isJSNumber(v)
	return ok && f == n
}

// validateRunCall is validateRunCall (scripted-workflow.ts:669-706).
func (s *wfSandbox) validateRunCall(keyV, paramsV goja.Value, label string, fingerprints map[string]string) {
	key, ok := isJSString(keyV)
	if !ok || !workflowKeyPattern.MatchString(key) {
		s.throwError(label + " has an invalid key.")
	}
	if s.hostKeys[key] {
		s.throwError("Workflow key '" + key + "' is already used by runs.host.")
	}
	params, ok := paramsV.(*goja.Object)
	if !ok || goja.IsNull(paramsV) || s.realm.array(params) {
		s.throwError(label + " requires a params object.")
	}
	for _, f := range []string{"action", "workflowScript", "globalConcurrencyLimit", "maxSubagentSpawnsPerRun", "tasks", "chain", "parallel", "concurrency", "chainDir"} {
		if s.ownHas(params, f) {
			hint := "."
			if label == "runs.run" {
				hint = "; use runs.all(...) and JavaScript control flow for orchestration."
			}
			s.throwError(label + " accepts one child via { agent, task } and execution controls only" + hint)
		}
	}
	if s.ownHas(params, "clarify") {
		s.throwError(label + " does not support clarify UI.")
	}
	if wt := params.Get("worktree"); !isUndef(wt) {
		if _, isBool := isJSBool(wt); !isBool {
			s.throwError(label + " worktree must be true or false.")
		}
	}
	if br := params.Get("baseRef"); !isUndef(br) {
		str, isStr := isJSString(br)
		if !isStr || !validGitRef(str) {
			s.throwError(label + " " + workflowBaseRefValidationError)
		}
	}
	s.validateLaneMetadata(params.Get("lane"), label+" lane", key, true)
	gate := params.Get("gate")
	acceptance := params.Get("acceptance")
	resume := params.Get("resume")
	if !isUndef(gate) {
		if e := s.gateShapeError(gate); e != "" {
			s.throwError(label + " " + e)
		}
	}
	if !isUndef(gate) && !isUndef(acceptance) {
		if b, isBool := isJSBool(acceptance); !isBool || b {
			s.throwError(label + " gate cannot be combined with acceptance; use one gate command or acceptance.verify." + s.describeGateAcceptanceConflict(gate, acceptance))
		}
	}
	if !isUndef(gate) && !isUndef(resume) {
		s.throwError(label + " gate is not supported with retained resume.")
	}
	if !isUndef(params.Get("extensionBindings")) && !isUndef(resume) {
		s.throwError(label + " extensionBindings is not supported with retained resume; resume uses the original retained child binding.")
	}
	if _, isStr := isJSString(resume); !isUndef(resume) && !isStr {
		ref, ok := resume.(*goja.Object)
		if !ok || goja.IsNull(resume) || s.realm.array(ref) {
			s.throwError(label + " resume must be a retained run id or keyed workflow receipt reference.")
		}
		for _, f := range s.realm.keysOf(ref) {
			if f != "workflowRunId" && f != "key" && f != "latest" {
				s.throwError(label + " keyed resume contains unsupported fields.")
			}
		}
		if id, ok := isJSString(ref.Get("workflowRunId")); !ok || jsTrim(id) == "" {
			s.throwError(label + " keyed resume workflowRunId must be non-empty.")
		}
		if k, ok := isJSString(ref.Get("key")); !ok || !workflowKeyPattern.MatchString(k) {
			s.throwError(label + " keyed resume key is invalid.")
		}
		if b, ok := isJSBool(ref.Get("latest")); !ok || !b {
			s.throwError(label + " keyed resume requires latest: true.")
		}
	}
	if str, isStr := isJSString(resume); isStr && jsTrim(str) == "" {
		s.throwError(label + " resume must be a non-empty retained run id.")
	}
	if !isUndef(resume) && !isUndef(params.Get("agent")) {
		s.throwError(label + " resume and agent are mutually exclusive.")
	}
	if !isUndef(resume) {
		if task, ok := isJSString(params.Get("task")); !ok || jsTrim(task) == "" {
			s.throwError(label + " resume requires a non-empty task follow-up.")
		}
	}
	s.validateExtensionBindings(params.Get("extensionBindings"), label)
	s.checkJSON(params, label+" params")
	fingerprint := stableRunJSON(canonicalRunParams(s.toGo(params).(*jsObject)))
	if existing, ok := fingerprints[key]; ok && existing != fingerprint {
		s.throwError("Duplicate workflow key '" + key + "' used with incompatible launch params.")
	}
	fingerprints[key] = fingerprint
}

// decorateChild gives a child result its non-enumerable toString (scripted-workflow.ts:587).
func (s *wfSandbox) decorateChild(v goja.Value) goja.Value {
	o, ok := v.(*goja.Object)
	if !ok || goja.IsNull(v) || s.realm.array(o) {
		return v
	}
	ts := s.native("toString", 0, func(call goja.FunctionCall) goja.Value {
		return s.str(s.formatChildResultString(call.This))
	})
	_ = o.DefineDataProperty("toString", ts, goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_FALSE)
	return v
}

func (s *wfSandbox) formatRef(v goja.Value) string {
	o, ok := v.(*goja.Object)
	if !ok || goja.IsNull(v) {
		s.throwError("runs.ref(result) requires a run result object.")
	}
	key := "unknown"
	if k := o.Get("key"); k != nil && k.ToBoolean() {
		key = s.realm.safeString(k)
	}
	parts := []string{"run " + key}
	if id := o.Get("runId"); id != nil && id.ToBoolean() {
		str := s.realm.safeString(id)
		parts = append(parts, "id="+string(utf16Slice(str, 8)))
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

func (s *wfSandbox) formatChildResultString(v goja.Value) string {
	if o, ok := v.(*goja.Object); ok {
		if out, ok := isJSString(o.Get("output")); ok {
			if t := jsTrim(out); t != "" {
				return t
			}
		}
	}
	return s.formatRef(v)
}

// utf16Slice is str.slice(0, n) in UTF-16 code units.
func utf16Slice(str string, n int) []rune {
	out := make([]rune, 0, n)
	units := 0
	for _, r := range str {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > n {
			break
		}
		out = append(out, r)
		units += w
	}
	return out
}

var _ = fmt.Sprint
