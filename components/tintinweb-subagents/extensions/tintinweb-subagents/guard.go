package tintinweb_subagents

import (
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// No panic may leave this extension: one unrecovered panic in any goroutine ends the process, and with it every agent
// the user has running. So every entry point the host or the runtime calls into (event handlers, tools, commands,
// renderers, bus handlers, the overlay's component, terminal input, timers) and every goroutine this package starts
// recovers, reports on stderr with the stack, and tells the user once per place. A panic is a bug here, never a flow
// control; recovering only keeps it from taking the session down.
//
// What recover cannot catch is a fatal error (a concurrent map read and write, a deadlock): those are prevented by
// design (the viewer reads copies of the transcript, state is guarded by mutexes).

// panicNotice delivers the one-time user notice of a recovered panic; the app sets it.
var panicNotice atomic.Pointer[func(message string)]

var panicSeen sync.Map // where -> struct{}: the places already announced to the user

// reportPanic logs a recovered panic and, the first time for a place, notifies the user. It never panics itself.
func reportPanic(where string, v any) {
	defer func() { _ = recover() }()
	fmt.Fprintf(os.Stderr, "[pi-subagents] recovered panic in %s: %v\n%s\n", where, v, debug.Stack())
	if _, seen := panicSeen.LoadOrStore(where, struct{}{}); seen {
		return
	}
	if f := panicNotice.Load(); f != nil {
		(*f)(fmt.Sprintf("tintinweb-subagents: internal error in %s (%v); the extension kept running. See the extension log.", where, v))
	}
}

// catch runs f and turns a panic into an error.
func catch(where string, f func()) (err error) {
	defer func() {
		if v := recover(); v != nil {
			reportPanic(where, v)
			err = fmt.Errorf("internal error in %s: %v", where, v)
		}
	}()
	f()
	return nil
}

// safeCall runs f, recovering a panic.
func safeCall(where string, f func()) { _ = catch(where, f) }

// safeGo runs f on a goroutine that recovers a panic.
func safeGo(where string, f func()) { go safeCall(where, f) }

// afterFunc is time.AfterFunc with a recovering callback.
func afterFunc(d time.Duration, where string, f func()) *time.Timer {
	return time.AfterFunc(d, func() { safeCall(where, f) })
}

type eventFn = func(ctx sdk.Context, data map[string]any) (any, error)

// protect makes an event handler or tool execution return an error instead of panicking.
func protect(where string, f eventFn) eventFn {
	return func(ctx sdk.Context, data map[string]any) (res any, err error) {
		if perr := catch(where, func() { res, err = f(ctx, data) }); perr != nil {
			return nil, perr
		}
		return res, err
	}
}

// protectCommand is protect for a slash command.
func protectCommand(where string, f func(ctx sdk.Context, args string) error) func(sdk.Context, string) error {
	return func(ctx sdk.Context, args string) (err error) {
		if perr := catch(where, func() { err = f(ctx, args) }); perr != nil {
			return perr
		}
		return err
	}
}

// protectLines is protect for a renderer: a panic renders nothing and reports an error.
func protectLines(where string, f func() ([]string, error)) (lines []string, err error) {
	if perr := catch(where, func() { lines, err = f() }); perr != nil {
		return nil, perr
	}
	return lines, err
}

// valueOf runs f and returns its result, or the zero value if it panicked. For reads a surface makes while it holds its
// own lock: a panic there would leave the lock held for good.
func valueOf[T any](where string, f func() T) (out T) {
	safeCall(where, func() { out = f() })
	return out
}
