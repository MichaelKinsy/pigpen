package dirty_repo_guard_test

import "encoding/json"

// FireAllowError is Fire for a case that expects the handler to fail: it returns
// the handler's error text instead of failing the test.
func (h *Host) FireAllowError(event string, data map[string]any) string {
	h.mu.Lock()
	id, ok := h.handlers[event]
	h.mu.Unlock()
	if !ok {
		h.t.Fatalf("no handler registered for %s", event)
	}
	if data == nil {
		data = map[string]any{}
	}
	data["type"] = event
	args, _ := json.Marshal(data)
	_, failure := h.roundTrip(map[string]any{"method": "event", "event": event, "handler_id": id, "args": json.RawMessage(args)})
	return failure
}
