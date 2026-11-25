// Package surface says which drawing surface the session is on: Tern's native
// surface, or a terminal. omp has two code paths per tool, a terminal renderer
// and a native description; a kit view can serve only one of them well, so
// the renderers ask here which one to build.
//
// It is a public package of the extension module, not under internal/, so
// that the Piglet's frontend member (a separate Go module fused into the same
// Binary) can import it and report what it is doing. This file is the
// contract between that member and the renderers. Exported names and
// signatures are fixed; add, do not change.
package surface

import (
	"os"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
)

// Version is the oh-my-pig release version, the one place the terminal banner
// and the Tern welcome card take it from. It equals `version` in the Package's
// package.json and `release.version` in the Piglet's piglet.yaml; a test in the
// module root fails when it drifts from package.json.
const Version = "0.1.0"

// state is 0 until the frontend member reports, then 1 (terminal) or 2 (native).
var state atomic.Int32

var (
	mu        sync.Mutex
	listeners = map[int]func(native bool){}
	nextID    int
	// modelNames maps a model id to the name the extension shows for it.
	modelNames = map[string]string{}
)

// maxModelNames bounds modelNames; a session uses a handful of models.
const maxModelNames = 256

// SetModelDisplayName records the name the extension shows for a model id, so
// that the frontend member, which the host hands only the id, can show the
// same name. An empty id or name is ignored.
func SetModelDisplayName(id, name string) {
	if id == "" || name == "" {
		return
	}
	mu.Lock()
	if _, known := modelNames[id]; !known && len(modelNames) >= maxModelNames {
		clear(modelNames)
	}
	modelNames[id] = name
	mu.Unlock()
}

// ModelDisplayName is the name recorded for a model id, or "" when none was.
func ModelDisplayName(id string) string {
	mu.Lock()
	defer mu.Unlock()
	return modelNames[id]
}

// DisplayPath is p as omp spells it for the user and the model: on macOS
// without the leading /private when both spellings name the same place. The
// frontend member uses it for the paths and links it draws, so that they match
// what the extension's tools print.
func DisplayPath(p string) string { return dirs.StandardizeMacOSPath(p) }

// SetNative is called by the frontend member: true once the native surface is
// open and drawing, false when it has fallen back to PiG's terminal renderer
// or closed.
func SetNative(native bool) {
	next := int32(1)
	if native {
		next = 2
	}
	if state.Swap(next) == next {
		return
	}
	mu.Lock()
	fns := make([]func(bool), 0, len(listeners))
	for _, fn := range listeners {
		fns = append(fns, fn)
	}
	mu.Unlock()
	for _, fn := range fns {
		fn(native)
	}
}

// Native reports whether results are being drawn on Tern's native surface.
// Before the frontend member has reported (or in a Binary without it) it
// answers from the environment the way the member decides to try: inside a
// Tern pane and not switched off.
func Native() bool {
	switch state.Load() {
	case 1:
		return false
	case 2:
		return true
	}
	if os.Getenv("PIG_TUI_NATIVE") == "0" {
		return false
	}
	return os.Getenv("TERM_PROGRAM") == "tern" && os.Getenv("TERN_PANE") != ""
}

// OnChange registers fn to be called when the surface changes, and returns
// the function that removes it. Renderers use it to invalidate what they drew
// for the other surface.
func OnChange(fn func(native bool)) func() {
	mu.Lock()
	nextID++
	id := nextID
	listeners[id] = fn
	mu.Unlock()
	return func() {
		mu.Lock()
		delete(listeners, id)
		mu.Unlock()
	}
}

// ResetForTests forgets what the member reported and every listener.
func ResetForTests() {
	state.Store(0)
	mu.Lock()
	listeners = map[int]func(bool){}
	clear(modelNames)
	mu.Unlock()
}
