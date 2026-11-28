// Package jobs is the per-session registry of background work: bash jobs and
// services, subagents, eval cells. The areas that start background work add
// it here; `wait`, proc:// and result delivery read it.
//
// This file is the contract between areas. Exported names and signatures are
// fixed; add, do not change.
//
// upstream: packages/coding-agent/src/async (job manager), tools/wait
package jobs

import (
	"sort"
	"sync"
	"time"
)

// Kind is what a job is.
type Kind string

const (
	KindBash    Kind = "bash"
	KindService Kind = "service"
	KindAgent   Kind = "agent"
	KindEval    Kind = "eval"
)

// Job is one unit of background work. Implementations are safe for
// concurrent use.
type Job interface {
	// ID is the identifier the model uses (`bg_3`, an agent name, a service name).
	ID() string
	Kind() Kind
	// Label is the short description shown in listings.
	Label() string
	// Started is when the job began.
	Started() time.Time
	// Done is closed when the job has finished and Result is final.
	Done() <-chan struct{}
	// Status is "running", "completed", "failed" or "cancelled".
	Status() string
	// Output is the output so far; Result is the model-facing final result.
	Output() string
	Result() string
	// Cancel stops the job.
	Cancel()
}

// Listener is called once, from its own goroutine, when a job finishes.
type Listener func(sessionID string, j Job)

var (
	mu        sync.Mutex
	bySession = map[string]map[string]Job{}
	listeners = map[int]Listener{}
	nextID    int
)

// Add registers a job of a session and calls the listeners when it finishes.
// A job with the same ID replaces the earlier one.
func Add(sessionID string, j Job) {
	mu.Lock()
	set := bySession[sessionID]
	if set == nil {
		set = map[string]Job{}
		bySession[sessionID] = set
	}
	set[j.ID()] = j
	mu.Unlock()
	PublishJobsHud(sessionID)
	go func() {
		<-j.Done()
		mu.Lock()
		fns := make([]Listener, 0, len(listeners))
		for _, fn := range listeners {
			fns = append(fns, fn)
		}
		mu.Unlock()
		PublishJobsHud(sessionID)
		for _, fn := range fns {
			fn(sessionID, j)
		}
	}()
}

// Get returns a session's job by ID.
func Get(sessionID, id string) (Job, bool) {
	mu.Lock()
	defer mu.Unlock()
	j, ok := bySession[sessionID][id]
	return j, ok
}

// List returns a session's jobs, oldest first.
func List(sessionID string) []Job {
	mu.Lock()
	out := make([]Job, 0, len(bySession[sessionID]))
	for _, j := range bySession[sessionID] {
		out = append(out, j)
	}
	mu.Unlock()
	sort.Slice(out, func(i, k int) bool { return out[i].Started().Before(out[k].Started()) })
	return out
}

// Remove forgets a job. It does not cancel it.
func Remove(sessionID, id string) {
	mu.Lock()
	delete(bySession[sessionID], id)
	mu.Unlock()
	PublishJobsHud(sessionID)
}

// OnFinish registers a listener for every job that finishes and returns the
// function that removes it.
func OnFinish(fn Listener) func() {
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

// ResetForTests empties the registry.
func ResetForTests() {
	mu.Lock()
	bySession = map[string]map[string]Job{}
	listeners = map[int]Listener{}
	mu.Unlock()
}
