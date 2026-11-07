package posthorse

// Port of src/reset-requests.ts: native call ancestry joins nested new_context requests to the
// foreground receipt that settles them.

import (
	"errors"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type requestKind int

const (
	requestPending requestKind = iota
	requestSettled
	requestFailed
)

// foregroundCall is the TS ForegroundCall (reset-requests.ts:12-15): one object shared by a
// root call and every call nested under it.
type foregroundCall struct {
	id         string
	kind       requestKind
	handoff    string
	hasHandoff bool
}

// resetRequests is `ResetRequests` (reset-requests.ts:17-88). The mutex stands for the TS
// single-threaded run of each hook.
type resetRequests struct {
	mu     sync.Mutex
	active map[string]*foregroundCall
	roots  map[string]*foregroundCall
}

// newResetRequests is `new ResetRequests(pi)`: it subscribes to the two tool-execution events.
func newResetRequests(e *sdk.Extension) *resetRequests {
	r := &resetRequests{active: map[string]*foregroundCall{}, roots: map[string]*foregroundCall{}}
	e.OnEvent(sdk.EventToolExecutionStart, func(_ sdk.Context, data map[string]any) (any, error) {
		r.start(data)
		return nil, nil
	})
	e.OnEvent(sdk.EventToolExecutionEnd, func(_ sdk.Context, data map[string]any) (any, error) {
		r.finish(data)
		return nil, nil
	})
	return r
}

// clear is `clear` (reset-requests.ts:28-31).
func (r *resetRequests) clear() {
	r.mu.Lock()
	r.clearLocked()
	r.mu.Unlock()
}

func (r *resetRequests) clearLocked() {
	clear(r.active)
	clear(r.roots)
}

// start is `#start` (reset-requests.ts:32-45).
func (r *resetRequests) start(event rec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	toolCallID, _ := str(event, "toolCallId")
	parentValue, hasParent := event["parentToolCallId"]
	var root *foregroundCall
	if !hasParent {
		root = &foregroundCall{id: toolCallID, kind: requestPending}
	} else if parent, ok := parentValue.(string); ok {
		root = r.active[parent]
	}
	if root == nil {
		return
	}
	if hasParent && r.active[root.id] == nil {
		return
	}
	r.active[toolCallID] = root
	if !hasParent {
		r.roots[root.id] = root
	}
}

// finish is `#finish` (reset-requests.ts:46-60).
func (r *resetRequests) finish(event rec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	toolCallID, _ := str(event, "toolCallId")
	root := r.active[toolCallID]
	delete(r.active, toolCallID)
	if root == nil || root.kind != requestPending {
		return
	}
	if isError, _ := boolean(event, "isError"); isError {
		root.kind, root.handoff, root.hasHandoff = requestFailed, "", false
	} else if toolCallID == root.id {
		root.kind = requestSettled
	}
}

// request is `request` (reset-requests.ts:61-69).
func (r *resetRequests) request(id, handoff string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	root := r.active[id]
	if root == nil || r.active[root.id] == nil {
		return errors.New("Request new_context from the current foreground tool batch.")
	}
	if root.kind == requestPending && !root.hasHandoff {
		root.handoff, root.hasHandoff = handoff, true
	}
	return nil
}

// take is `take` (reset-requests.ts:70-87): results are the turn_end toolResults.
func (r *resetRequests) take(results []any) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	type state struct {
		kind       requestKind
		handoff    string
		hasHandoff bool
		known      bool
	}
	states := make([]state, len(results))
	for i, result := range results {
		message, _ := recOf(result)
		if isError, _ := boolean(message, "isError"); isError {
			states[i] = state{kind: requestFailed, known: true}
			continue
		}
		id, _ := str(message, "toolCallId")
		if root := r.roots[id]; root != nil {
			states[i] = state{kind: root.kind, handoff: root.handoff, hasHandoff: root.hasHandoff, known: true}
		}
	}
	r.clearLocked()
	for _, s := range states {
		if s.known && s.kind == requestFailed {
			return "", false
		}
	}
	for _, s := range states {
		if s.known && s.kind == requestSettled && s.hasHandoff {
			return s.handoff, true
		}
	}
	return "", false
}
