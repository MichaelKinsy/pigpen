package stretchtext

import "sync"

// Store holds the State and runs changes on it, telling onChange whenever one changed something. The
// original's Store (src/monad.ts) is single-threaded; here a handler, the animation timer and the
// host's Markdown requests meet, so every access goes through one lock. onChange runs after the lock
// is released.
type Store struct {
	mu       sync.Mutex
	state    *State
	onChange func()
}

// NewStore returns a store holding a fresh State.
func NewStore(onChange func()) *Store { return &Store{state: NewState(), onChange: onChange} }

// Run applies change under the lock; change reports whether it changed the state.
func (s *Store) Run(change func(*State) bool) {
	s.mu.Lock()
	changed := change(s.state)
	s.mu.Unlock()
	if changed {
		s.onChange()
	}
}

// With applies a change that yields a value.
func With[A any](s *Store, change func(*State) (A, bool)) A {
	s.mu.Lock()
	value, changed := change(s.state)
	s.mu.Unlock()
	if changed {
		s.onChange()
	}
	return value
}

// View reads the state under the lock.
func (s *Store) View(read func(*State)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	read(s.state)
}
