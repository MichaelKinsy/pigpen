package pi_subagents

import (
	"sync"
)

// localBus is the in-process event bus the background modules use to wake each other (the original's pi.events inside one
// runtime). Handlers run on the emitter's goroutine, in subscription order; a handler must not block.
type localBus struct {
	mu   sync.Mutex
	subs map[string]map[int]func(any)
	next int
}

func newLocalBus() *localBus { panic("not ported yet") }

// On subscribes handler to channel and returns the unsubscribe function.
func (b *localBus) On(channel string, handler func(any)) func() { panic("not ported yet") }

// Emit delivers payload to the handlers of channel.
func (b *localBus) Emit(channel string, payload any) { panic("not ported yet") }

// subscription order: ids grow monotonically
