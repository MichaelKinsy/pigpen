package frontend

import (
	"strings"

	"github.com/stencil-hq/tern-sdk/go/tern"
)

// PiG lends the terminal to another program for a while: an external editor
// (Ctrl+G), or the shell after a job-control stop (Ctrl+Z). Tern documents
// two ways to hand the pane back to the grid: the suspend and resume frame
// ops, and closing the surface to adopt it again. A suspended surface keeps
// its dock drawn and still sends its view events (resize, visible) to the
// pty, which the other program reads as typed text. A closed surface draws
// no dock and sends nothing at all, so the member closes it, keeping the
// transcript in place, and adopts it back: the same document, with the dock
// and the layer added again. Frames continue where they stopped, so nothing
// is sent twice. After a job-control stop the shell's prompt discards the
// surface, and Tern answers the adopt with gone: the member then opens a
// new surface and sends the whole document again from what it keeps.

// Suspend closes the surface, keeping its transcript, after handing the
// SDK everything pending: the SDK sends it past the credits before the
// close, so the transcript left in the pane is the latest. Tern then sends
// nothing about the surface until it is adopted. The terminal progress
// keepalive pauses, since the terminal is the other program's.
func (s *session) Suspend() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suspended || s.closed {
		return
	}
	s.suspended = true
	s.stopKeepalive()
	if s.timer != nil && s.tsp == nil {
		// Tern's hello reply reaches PiG only after Resume.
		s.timer.Stop()
		s.timer = nil
	}
	if s.sf == nil || s.fellBack {
		return
	}
	if s.live {
		if s.dirty {
			_ = s.sendErr(s.render(s.sf))
		}
		if len(s.pending) > 0 {
			_ = s.sendErr(s.sf.Send(s.pending...))
			clear(s.pending)
			s.pending = s.pending[:0]
		}
	}
	s.suspendScreen()
	_ = s.sendErr(s.sf.Close())
	s.lent = true
}

// Resume adopts the surface Suspend closed and draws its dock and layer
// again, or opens the surface the connection made meanwhile. Tern counted
// no ack for the frames before the close, so every frame counts as
// acknowledged, as the SDK counts them.
func (s *session) Resume() {
	s.mu.Lock()
	if !s.suspended {
		s.mu.Unlock()
		return
	}
	s.suspended = false
	if s.progress {
		s.startKeepalive()
	}
	var err error
	switch {
	case s.closed || s.fellBack:
	case s.tsp == nil:
		if s.inputReady && s.timer == nil {
			s.timer = s.afterFunc(helloTimeout, s.cancel)
		}
	case !s.lent:
		if s.sf == nil {
			err = s.openLocked()
		}
	default:
		s.lent = false
		var sf *tern.Surface
		sf, err = s.tsp.Open(tern.SurfaceOptions{ID: surfaceID, Mode: tern.Inline, Title: "pig", Role: "pig.session", Adopt: true})
		if err != nil {
			err = errRefused
			break
		}
		s.sf, s.adopting = sf, true
		s.acked = s.out.frames.Load()
		s.redrawDock()
		if s.flushLocked() != nil {
			err = errFirstFlush
		}
	}
	s.mu.Unlock()
	if err != nil {
		s.fallback(err.Error())
	}
}

// reopen opens a new surface when Tern answers an adopt with gone, and
// sends it the whole document: the transcript, the session file's node,
// the palette, the mark, the dock and the layer. It reports false for any
// other gone.
func (s *session) reopen() bool {
	s.mu.Lock()
	if !s.adopting || s.closed || s.fellBack {
		s.mu.Unlock()
		return false
	}
	if s.suspended {
		// PiG lent the terminal out again: Resume opens the surface.
		s.lent = false
		s.sf = nil
		s.redrawAll()
		s.frameBase = s.out.frames.Load()
		s.acked = s.frameBase
		s.mu.Unlock()
		return true
	}
	err := s.reopenLocked()
	s.mu.Unlock()
	if err != nil {
		s.fallback(err.Error())
	}
	return true
}

// reopenLocked opens a new surface whose frames count from 1.
func (s *session) reopenLocked() error {
	s.redrawAll()
	s.frameBase = s.out.frames.Load()
	s.acked = s.frameBase
	return s.openLocked()
}

// redrawAll queues the whole document for a new surface: every transcript
// node Tern still had in order, each final one settled again so retention
// can evict it from the new surface too, then the session file's node
// before them, and the dock, the layer, the palette and the sheets.
// Nodes Tern evicted stay out, as the old surface showed them.
func (s *session) redrawAll() {
	clear(s.pending)
	s.pending = s.pending[:0]
	for _, id := range s.order {
		e := s.sent[strings.TrimPrefix(id, "n.")]
		if e.gone {
			continue
		}
		e.settled = false
		s.pending = append(s.pending, tern.OpAdd("main", "", s.mainNode(e.id, e.node)))
	}
	for i, id := range s.order {
		s.settle(s.sent[strings.TrimPrefix(id, "n.")], i)
	}
	if s.sessionNode {
		s.sessionNode = false
		s.publishSessionFile(s.sessionFile)
	}
	s.paletteSent = false
	s.markDirty, s.markInstalled, s.overlaySheetSent = true, false, false
	s.viewSheetSent = false
	s.redrawDock()
}

// redrawDock builds every dock and overlay node again as new, so the next
// render adds each with its current props and an editor takes the keys
// again.
func (s *session) redrawDock() {
	for _, e := range s.dock {
		e.unsent = true
		s.rebuild(e, e.node, false)
	}
	for _, e := range s.overlays {
		e.unsent = true
		s.rebuild(e, e.node, false)
	}
	s.dirty = true
}
