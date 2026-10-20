package tui

import "sync/atomic"

// Component is a renderable piece of terminal UI. Mirrors pi-tui's Component (render and invalidate).
type Component interface {
	Render(width int) []string
	// Invalidate marks the component as needing a redraw on the next tick.
	Invalidate()
}

// invalidatable provides Invalidate, IsDirty and NeedsRedraw.
type invalidatable struct {
	dirty atomic.Bool
}

func (i *invalidatable) Invalidate()       { i.dirty.Store(true) }
func (i *invalidatable) IsDirty() bool     { return i.dirty.Load() }
func (i *invalidatable) NeedsRedraw() bool { return i.dirty.Swap(false) }
