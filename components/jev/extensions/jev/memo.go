package jev

import (
	"sync"
	"time"
)

const memoLimit = 64

// memo caches responses for a window and lets identical concurrent requests share
// one in-flight call (sibling tool calls of one assistant message). Errors are
// never cached. The cache stores the raw response, so a changed threshold applies
// to it instead of being baked into an old verdict.
type memo struct {
	mu      sync.Mutex
	entries map[string]memoEntry
	order   []string
	flights map[string]*flight
}

type memoEntry struct {
	at   time.Time
	resp *response
}

type flight struct {
	done chan struct{}
	resp *response
	err  error
}

func newMemo() *memo { return &memo{entries: map[string]memoEntry{}, flights: map[string]*flight{}} }

func (m *memo) do(key string, ttl time.Duration, now func() time.Time, fn func() (*response, error)) (*response, error) {
	m.mu.Lock()
	if e, ok := m.entries[key]; ok && ttl > 0 && now().Sub(e.at) <= ttl {
		m.mu.Unlock()
		return e.resp, nil
	}
	if f, ok := m.flights[key]; ok {
		m.mu.Unlock()
		<-f.done
		return f.resp, f.err
	}
	f := &flight{done: make(chan struct{})}
	m.flights[key] = f
	m.mu.Unlock()

	f.resp, f.err = fn()

	m.mu.Lock()
	delete(m.flights, key)
	if f.err == nil && ttl > 0 {
		if _, ok := m.entries[key]; !ok {
			m.order = append(m.order, key)
		}
		m.entries[key] = memoEntry{now(), f.resp}
		m.prune(ttl, now())
	}
	m.mu.Unlock()
	close(f.done)
	return f.resp, f.err
}

func (m *memo) prune(ttl time.Duration, at time.Time) {
	if len(m.entries) <= memoLimit {
		return
	}
	kept := m.order[:0]
	for _, k := range m.order {
		if at.Sub(m.entries[k].at) > ttl {
			delete(m.entries, k)
		} else {
			kept = append(kept, k)
		}
	}
	m.order = kept
	for len(m.entries) > memoLimit && len(m.order) > 0 {
		delete(m.entries, m.order[0])
		m.order = m.order[1:]
	}
}

func (m *memo) size() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}
