package pi_subagents

import (
	"sort"
	"sync"
)

// localBus is the in-process event bus the background modules use to wake each other (the original's pi.events inside one
// runtime). Handlers run on the emitter's goroutine, in subscription order; a handler must not block.
type localBus struct {
	mu   sync.Mutex
	subs map[string]map[int]func(any)
	next int
}

func newLocalBus() *localBus { return &localBus{subs: map[string]map[int]func(any){}} }

// On subscribes handler to channel and returns the unsubscribe function.
func (b *localBus) On(channel string, handler func(any)) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs[channel] == nil {
		b.subs[channel] = map[int]func(any){}
	}
	id := b.next
	b.next++
	b.subs[channel][id] = handler
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subs[channel], id)
	}
}

// Emit delivers payload to the handlers of channel.
func (b *localBus) Emit(channel string, payload any) {
	b.mu.Lock()
	ids := make([]int, 0, len(b.subs[channel]))
	for id := range b.subs[channel] {
		ids = append(ids, id)
	}
	handlers := make([]func(any), 0, len(ids))
	sort.Ints(ids) // subscription order: ids grow monotonically
	for _, id := range ids {
		handlers = append(handlers, b.subs[channel][id])
	}
	b.mu.Unlock()
	for _, h := range handlers {
		h(payload)
	}
}
