// Package lifecycle tells every area when a session has ended, top-level or
// child, so each can release that session's state and nothing else.
//
// Why it exists: PiG builds a fresh extension instance per session while
// package-level state is shared by all sessions in the process, and a child
// (subagent) session that closes emits no session_shutdown event. Areas
// therefore cannot rely on their own session_shutdown hook alone.
//
// This file is the contract between areas. Exported names and signatures are
// fixed; add, do not change. An area that keeps per-session state calls OnEnd
// once (it is keyed by name, so calling it from every Register is safe). The
// extension's session_shutdown hook and the agents area (when it disposes a
// child session) call End. Areas may also call End from their own
// session_shutdown hook; repeated End calls for one session ID are expected
// and every registered cleanup must be idempotent.
package lifecycle

import (
	"sort"
	"sync"
)

var (
	mu       sync.Mutex
	handlers = map[string]func(sessionID string){}
)

// OnEnd installs the cleanup of the area called name, replacing an earlier
// one of the same name. fn releases only the state of sessionID; it must be
// safe to call twice for one id and for an id it has never seen, must not
// block for long, and must leave the area usable for other sessions.
func OnEnd(name string, fn func(sessionID string)) {
	mu.Lock()
	handlers[name] = fn
	mu.Unlock()
}

// End reports that a session has ended. It runs every area's cleanup, in
// name order, and returns when all have returned. A cleanup that panics does
// not stop the others.
func End(sessionID string) {
	if sessionID == "" {
		return
	}
	mu.Lock()
	names := make([]string, 0, len(handlers))
	for name := range handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	fns := make([]func(string), len(names))
	for i, name := range names {
		fns[i] = handlers[name]
	}
	mu.Unlock()
	for _, fn := range fns {
		func() {
			defer func() { _ = recover() }()
			fn(sessionID)
		}()
	}
}

// ResetForTests removes every cleanup.
func ResetForTests() {
	mu.Lock()
	handlers = map[string]func(sessionID string){}
	mu.Unlock()
}
