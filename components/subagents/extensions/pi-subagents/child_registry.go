package pi_subagents

import (
	"sort"
	"sync"
)

// The child registry: every running child session of this process (foreground runs and the background runs this process
// supervises), so steering, the watchdog and the status views find a child by run and index. It is the counterpart of the
// original's registries of foreground controls and retained children.

type registeredChild struct {
	RunID      string
	Index      int
	Agent      string
	Foreground bool
	Session    childSession
}

var childRegistry = struct {
	mu        sync.Mutex
	children  map[childKey]registeredChild
	order     []childKey
	listeners map[int]func(registeredChild)
	nextL     int
}{children: map[childKey]registeredChild{}, listeners: map[int]func(registeredChild){}}

type childKey struct {
	run   string
	index int
}

// registerChild adds a running child and tells the listeners (watchdog hosts). The returned function removes it.
func registerChild(c registeredChild) (unregister func()) {
	k := childKey{c.RunID, c.Index}
	childRegistry.mu.Lock()
	if _, dup := childRegistry.children[k]; !dup {
		childRegistry.order = append(childRegistry.order, k)
	}
	childRegistry.children[k] = c
	ls := childRegistryListeners()
	childRegistry.mu.Unlock()
	for _, fn := range ls {
		fn(c)
	}
	return func() {
		childRegistry.mu.Lock()
		defer childRegistry.mu.Unlock()
		if cur, ok := childRegistry.children[k]; ok && cur.Session == c.Session {
			delete(childRegistry.children, k)
			for i, o := range childRegistry.order {
				if o == k {
					childRegistry.order = append(childRegistry.order[:i], childRegistry.order[i+1:]...)
					break
				}
			}
		}
	}
}

func childRegistryListeners() []func(registeredChild) {
	ids := make([]int, 0, len(childRegistry.listeners))
	for id := range childRegistry.listeners {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]func(registeredChild), 0, len(ids))
	for _, id := range ids {
		out = append(out, childRegistry.listeners[id])
	}
	return out
}

func lookupChild(runID string, index int) (registeredChild, bool) {
	childRegistry.mu.Lock()
	defer childRegistry.mu.Unlock()
	c, ok := childRegistry.children[childKey{runID, index}]
	return c, ok
}

// listChildren is every registered child, in registration order.
func listChildren() []registeredChild {
	childRegistry.mu.Lock()
	defer childRegistry.mu.Unlock()
	out := make([]registeredChild, 0, len(childRegistry.order))
	for _, k := range childRegistry.order {
		out = append(out, childRegistry.children[k])
	}
	return out
}

// onChildRegistered calls fn for every child registered from now on, and now for the ones already registered.
func onChildRegistered(fn func(registeredChild)) (off func()) {
	childRegistry.mu.Lock()
	id := childRegistry.nextL
	childRegistry.nextL++
	childRegistry.listeners[id] = fn
	existing := make([]registeredChild, 0, len(childRegistry.order))
	for _, k := range childRegistry.order {
		existing = append(existing, childRegistry.children[k])
	}
	childRegistry.mu.Unlock()
	for _, c := range existing {
		fn(c)
	}
	return func() {
		childRegistry.mu.Lock()
		delete(childRegistry.listeners, id)
		childRegistry.mu.Unlock()
	}
}

// steerConsumer is the steering slice's hook: superviseRun calls it once the child of a background run is up, and calls
// the returned function when the run ends. Nil until that slice assigns it.
var steerConsumer func(cfg *RunConfig, child childSession) (stop func())
