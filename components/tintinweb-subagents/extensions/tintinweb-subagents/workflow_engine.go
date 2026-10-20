package tintinweb_subagents

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/dop251/goja"
)

// The engine the workflow scripts run in. The original runs a script in a Node worker thread inside a `vm` context;
// a Go extension has no Node, so a script runs in an in-process goja realm on its own goroutine (the "worker"), and
// talks to the run (workflow_runtime.go, the "host") by messages, exactly as the original's worker does. What the
// split buys is the same too: terminating the worker (vm.Interrupt) stops a script that is spinning or wedged.
// upstream: src/workflow/worker-source.ts (workflow_worker.js is its JavaScript).

//go:embed workflow_worker.js
var workflowWorkerSource string

// workflowPrelude runs inside the realm ahead of the script body, on the body's first line (the original puts it on a
// line of its own and compiles with lineOffset -1; goja has no offset, so the body starts on the prelude's line and
// every line number is the author's). It reassigns Date.now and Math.random in place and lexically shadows Date with
// a subclass whose zero-argument constructor throws. Determinism is enforced because a journal is replayed by prefix
// on resume. upstream: DETERMINISM_PRELUDE.
const workflowPrelude = "const Date = (function () {" +
	" const RealDate = globalThis.Date;" +
	" const die = function (what) {" +
	` throw new Error(what + " is unavailable in workflow scripts (breaks resume).` +
	" Stamp results after the workflow returns, or pass timestamps via `args`." + `");` +
	" };" +
	` RealDate.now = function () { return die("Date.now()"); };` +
	` Math.random = function () { return die("Math.random()"); };` +
	" return class WorkflowDate extends RealDate {" +
	` constructor() { if (arguments.length === 0) die("new Date()"); super(...arguments); }` +
	" };" +
	"})();"

// maxWorkflowCallStack bounds the script's recursion (a runaway recursion is a RangeError, not a crashed process).
const maxWorkflowCallStack = 10_000

// workerMessage is what the worker posts to the host. Type is "call", "progress", "complete" or "error".
type workerMessage struct {
	Type       string
	CallID     int             // call
	Method     string          // call
	Payload    map[string]any  // call
	Entries    []workflowEntry // progress
	ResultJSON *string         // complete; nil when the script returned undefined
	Message    string          // error
	Stack      string          // error
}

// workerResponse is the host's answer to a call.
type workerResponse struct {
	CallID int
	OK     bool
	Value  any // a string, nil, or a map for a workflow lookup
	Error  string
	Fatal  bool
	Spent  int
}

type workerTimer struct{ cancelled bool }

// workflowWorker is one script's goroutine, its goja realm and its task queue.
type workflowWorker struct {
	vm     *goja.Runtime
	onMsg  func(workerMessage) // delivered from the worker goroutine; must not block
	mu     sync.Mutex
	cond   *sync.Cond
	queue  []func()
	closed bool
	// worker-goroutine state
	pendingCalls int
	timers       map[int]*workerTimer
	nextTimer    int
	finished     bool
	// rejected holds the promises rejected with no handler yet, in order (Node's unhandled-rejection bookkeeping).
	rejected []*goja.Promise
	response goja.Callable
	done     chan struct{}
	// exitedUnreported is set when the worker goroutine crashed before the script reported (the original's worker
	// `exit` event without a result).
	exitedUnreported bool
}

// startWorkflowWorker starts the worker goroutine. params are the run's body, meta and args (JSON text) and caps;
// onMsg receives its messages; onExit is called once when the goroutine ends (terminated, or out of work).
func startWorkflowWorker(body, metaJSON string, argsJSON *string, itemCap, nestedCap int, onMsg func(workerMessage), onExit func(unreported bool)) *workflowWorker {
	w := &workflowWorker{onMsg: onMsg, timers: map[int]*workerTimer{}, done: make(chan struct{})}
	w.cond = sync.NewCond(&w.mu)
	w.post(func() { w.boot(body, metaJSON, argsJSON, itemCap, nestedCap) })
	go func() {
		w.loop()
		close(w.done)
		onExit(w.exitedUnreported)
	}()
	return w
}

// post queues a task for the worker goroutine (the port of postMessage / a macrotask); it never blocks.
func (w *workflowWorker) post(fn func()) {
	w.mu.Lock()
	if !w.closed {
		w.queue = append(w.queue, fn)
		w.cond.Signal()
	}
	w.mu.Unlock()
}

// terminate stops the worker, interrupting a script that never yields, and waits for the goroutine to be gone.
func (w *workflowWorker) terminate() {
	w.mu.Lock()
	w.closed = true
	w.queue = nil
	vm := w.vm
	w.cond.Broadcast()
	w.mu.Unlock()
	if vm != nil {
		vm.Interrupt("terminated")
	}
	<-w.done
}

// loop runs the worker's tasks until it is terminated. Like the original's worker (whose parentPort listener keeps its
// event loop alive), it never ends by running out of work: a script that awaits a promise nothing will settle stays
// parked until the run is aborted. It ends early only on a crash.
func (w *workflowWorker) loop() {
	defer func() {
		if r := recover(); r != nil {
			// A crash, not a termination: the original's worker `exit` without a report.
			w.mu.Lock()
			w.closed = true
			w.exitedUnreported = !w.finished
			w.mu.Unlock()
		}
	}()
	for {
		w.mu.Lock()
		for len(w.queue) == 0 && !w.closed {
			w.cond.Wait()
		}
		if w.closed {
			w.mu.Unlock()
			return
		}
		fn := w.queue[0]
		w.queue = w.queue[1:]
		w.mu.Unlock()
		fn()
		w.flushRejections()
	}
}

func (w *workflowWorker) sendResponse(r workerResponse) {
	w.post(func() {
		if w.pendingCalls > 0 {
			w.pendingCalls--
		}
		msg := w.vm.NewObject()
		_ = msg.Set("callId", r.CallID)
		_ = msg.Set("ok", r.OK)
		_ = msg.Set("error", r.Error)
		_ = msg.Set("fatal", r.Fatal)
		_ = msg.Set("spent", r.Spent)
		_ = msg.Set("value", w.toJS(r.Value))
		if _, err := w.response(goja.Undefined(), msg); err != nil {
			w.failUncaught(err)
		}
	})
}

// toJS builds a realm value from a Go string, nil or map (a workflow lookup's answer).
func (w *workflowWorker) toJS(v any) goja.Value {
	switch x := v.(type) {
	case nil:
		return goja.Null()
	case string:
		return w.vm.ToValue(x)
	case map[string]any:
		o := w.vm.NewObject()
		for k, e := range x {
			_ = o.Set(k, w.toJS(e))
		}
		return o
	}
	return w.vm.ToValue(v)
}

// compileError throws a SyntaxError of the realm for a source that does not compile.
func (w *workflowWorker) compileError(err error) {
	msg := err.Error()
	var cse *goja.CompilerSyntaxError
	if errors.As(err, &cse) {
		msg = strings.TrimPrefix(msg, "SyntaxError: ")
	}
	obj, cerr := w.vm.New(w.vm.Get("SyntaxError"), w.vm.ToValue(msg))
	if cerr != nil {
		panic(w.vm.NewGoError(err))
	}
	panic(obj)
}

func (w *workflowWorker) boot(body, metaJSON string, argsJSON *string, itemCap, nestedCap int) {
	vm := goja.New()
	vm.SetMaxCallStackSize(maxWorkflowCallStack)
	w.mu.Lock()
	if w.closed { // terminated before the realm existed
		w.mu.Unlock()
		return
	}
	w.vm = vm
	w.mu.Unlock()
	vm.SetPromiseRejectionTracker(func(p *goja.Promise, op goja.PromiseRejectionOperation) {
		switch op {
		case goja.PromiseRejectionReject:
			w.rejected = append(w.rejected, p)
		case goja.PromiseRejectionHandle:
			for i, q := range w.rejected {
				if q == p {
					w.rejected = append(w.rejected[:i], w.rejected[i+1:]...)
					break
				}
			}
		}
	})
	host := vm.NewObject()
	_ = host.Set("call", func(call goja.FunctionCall) goja.Value {
		id := int(call.Argument(0).ToInteger())
		method := call.Argument(1).String()
		payload, _ := call.Argument(2).Export().(map[string]any)
		w.pendingCalls++
		w.onMsg(workerMessage{Type: "call", CallID: id, Method: method, Payload: payload})
		return goja.Undefined()
	})
	_ = host.Set("progress", func(call goja.FunctionCall) goja.Value {
		list, _ := call.Argument(0).Export().([]any)
		entries := make([]workflowEntry, 0, len(list))
		for _, item := range list {
			m, _ := item.(map[string]any)
			switch m["type"] {
			case "workflow_phase":
				idx, _ := wfToInt(m["index"])
				title, _ := m["title"].(string)
				entries = append(entries, newPhaseEntry(idx, title))
			case "workflow_log":
				msg, _ := m["message"].(string)
				entries = append(entries, newLogEntry(msg))
			}
		}
		w.onMsg(workerMessage{Type: "progress", Entries: entries})
		return goja.Undefined()
	})
	_ = host.Set("complete", func(call goja.FunctionCall) goja.Value {
		w.finished = true
		var result *string
		if a := call.Argument(0); !goja.IsUndefined(a) {
			result = new(a.String())
		}
		w.onMsg(workerMessage{Type: "complete", ResultJSON: result})
		return goja.Undefined()
	})
	_ = host.Set("error", func(call goja.FunctionCall) goja.Value {
		w.finished = true
		stack := ""
		if a := call.Argument(1); !goja.IsUndefined(a) {
			stack = a.String()
		}
		w.onMsg(workerMessage{Type: "error", Message: call.Argument(0).String(), Stack: stack})
		return goja.Undefined()
	})
	_ = host.Set("defer", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			panic(vm.NewTypeError("defer expects a function"))
		}
		w.nextTimer++
		id := w.nextTimer
		t := &workerTimer{}
		w.timers[id] = t
		w.post(func() {
			delete(w.timers, id)
			if !t.cancelled {
				if _, err := fn(goja.Undefined()); err != nil {
					w.failUncaught(err)
				}
			}
		})
		return vm.ToValue(id)
	})
	_ = host.Set("cancel", func(call goja.FunctionCall) goja.Value {
		id := int(call.Argument(0).ToInteger())
		if t := w.timers[id]; t != nil {
			t.cancelled = true
		}
		return goja.Undefined()
	})
	_ = host.Set("run", func(call goja.FunctionCall) goja.Value {
		program, err := goja.Compile(call.Argument(1).String(), call.Argument(0).String(), false)
		if err != nil {
			w.compileError(err)
		}
		v, err := vm.RunProgram(program)
		if err != nil {
			panic(err)
		}
		return v
	})
	_ = host.Set("compile", func(call goja.FunctionCall) goja.Value {
		program, err := goja.Compile(call.Argument(1).String(), call.Argument(0).String(), false)
		if err != nil {
			w.compileError(err)
		}
		v, err := vm.RunProgram(program)
		if err != nil {
			panic(err)
		}
		return v
	})

	params := vm.NewObject()
	_ = params.Set("itemCap", itemCap)
	_ = params.Set("nestedCap", nestedCap)
	_ = params.Set("prelude", workflowPrelude)
	_ = params.Set("body", body)
	_ = params.Set("metaJson", metaJSON)
	if argsJSON != nil {
		_ = params.Set("argsJson", *argsJSON)
	}

	factory, err := vm.RunScript("workflow-worker.js", workflowWorkerSource)
	if err != nil {
		w.fail(fmt.Errorf("the workflow worker did not start: %v", err))
		return
	}
	bootstrap, ok := goja.AssertFunction(factory)
	if !ok {
		w.fail(errors.New("the workflow worker did not start: not a function"))
		return
	}
	api, err := bootstrap(goja.Undefined(), host, params)
	if err != nil {
		w.fail(fmt.Errorf("the workflow worker did not start: %v", err))
		return
	}
	obj := api.ToObject(vm)
	w.response, _ = goja.AssertFunction(obj.Get("response"))
	if main, ok := goja.AssertFunction(obj.Get("main")); ok {
		if _, err := main(goja.Undefined()); err != nil {
			w.fail(err)
		}
	}
}

// fail reports a worker that could not run at all, as the original's worker `error` event does.
func (w *workflowWorker) fail(err error) {
	w.finished = true
	w.onMsg(workerMessage{Type: "error", Message: wfErrorDetail(err)})
}

// failUncaught is fail for an error that escaped a task the worker ran (a response, a timer): the original's worker
// `error` event for an uncaught exception, which fails the run. The one error it ignores is the interrupt that ends
// the worker (terminate), which is not a failure. A stack overflow inside a job is uncatchable in goja and arrives
// here, where it fails the run with V8's message instead of leaving the script parked on a promise nothing settles.
func (w *workflowWorker) failUncaught(err error) {
	var interrupted *goja.InterruptedError
	if err == nil || w.finished || errors.As(err, &interrupted) {
		return
	}
	w.fail(err)
}

// flushRejections runs after each task, once its promise jobs have drained: a promise still rejected with no handler
// fails the run, as an unhandled rejection does in the original's worker (an uncaught `error` event). The first one
// wins. An Error reports its message; any other reason reports Node's ERR_UNHANDLED_REJECTION text.
func (w *workflowWorker) flushRejections() {
	if len(w.rejected) == 0 {
		return
	}
	first := w.rejected[0]
	w.rejected = nil
	if w.finished {
		return
	}
	reason := first.Result()
	message := ""
	if o, ok := reason.(*goja.Object); ok {
		if m := o.Get("message"); m != nil && !goja.IsUndefined(m) {
			message = m.String()
		}
	}
	if message == "" {
		message = `This error originated either by throwing inside of an async function without a catch block, or by rejecting a promise which was not handled with .catch(). The promise rejected with the reason "` + reason.String() + `".`
	}
	w.finished = true
	w.onMsg(workerMessage{Type: "error", Message: message})
}

// wfToInt reads a number a realm exported (an int64 or a float64) as an int.
func wfToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int64:
		return int(n), true
	case int:
		return n, true
	case float64:
		return int(n), true
	}
	return 0, false
}

// compileWorkerSource compiles the worker JavaScript and the prelude, so a syntax error in either is a test failure
// rather than a failed run. upstream: the "parses as JavaScript" test of worker-source.ts.
func compileWorkerSource() (*goja.Program, error) {
	if _, err := goja.Compile("workflow-prelude.js", "(async () => {"+workflowPrelude+"\n})", true); err != nil {
		return nil, err
	}
	return goja.Compile("workflow-worker.js", workflowWorkerSource, true)
}
