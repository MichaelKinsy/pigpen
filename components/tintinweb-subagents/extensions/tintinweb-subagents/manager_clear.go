package tintinweb_subagents

// clearCompleted removes the settled agents now: the session boundaries (a session starting and a session switch) call
// it so the tasks of a prior session do not persist. With skipUnconsumed a background agent whose result the model has
// not read yet is kept (it was the bug of issue #108 that wiping it made the next get_subagent_result answer "Agent not
// found"); a foreground agent's result went back inline, so it is always consumed. upstream: agent-manager.ts
// clearCompleted. The original's ten-minute sweep of old records is not ported here.
func (a *app) clearCompleted(skipUnconsumed bool) {
	m := a.mgr
	m.mu.Lock()
	var removed []string
	kept := m.order[:0:0]
	for _, id := range m.order {
		r := m.records[id]
		if r.Status == statusRunning || r.Status == statusQueued || (skipUnconsumed && r.Background && !r.Consumed) {
			kept = append(kept, id)
			continue
		}
		delete(m.records, id)
		removed = append(removed, id)
	}
	m.order = kept
	m.mu.Unlock()
	if len(removed) > 0 {
		a.ui.forget(removed)
	}
}

// forget drops the surfaces' state of agents whose records are gone, and redraws.
func (h *uiHub) forget(ids []string) {
	h.mu.Lock()
	for _, id := range ids {
		delete(h.agents, id)
	}
	h.mu.Unlock()
	h.widget.update()
	h.fleet.update()
}
