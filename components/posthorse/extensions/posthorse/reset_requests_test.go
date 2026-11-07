package posthorse

import (
	"strings"
	"testing"
)

func newTestRequests() *resetRequests {
	return &resetRequests{active: map[string]*foregroundCall{}, roots: map[string]*foregroundCall{}}
}

func startCall(r *resetRequests, id string, parent ...string) {
	event := rec{"toolCallId": id}
	if len(parent) > 0 {
		event["parentToolCallId"] = parent[0]
	}
	r.start(event)
}

func endCall(r *resetRequests, id string, isError bool) {
	r.finish(rec{"toolCallId": id, "isError": isError})
}

func resultsOf(ids ...string) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = rec{"toolCallId": id}
	}
	return out
}

func TestResetRequestSettlesWithTheForegroundCall(t *testing.T) {
	r := newTestRequests()
	startCall(r, "A")
	if err := r.request("A", "carry on"); err != nil {
		t.Fatal(err)
	}
	endCall(r, "A", false)
	if handoff, ok := r.take(resultsOf("A")); !ok || handoff != "carry on" {
		t.Errorf("take = %q, %v", handoff, ok)
	}
	// take clears the state.
	if _, ok := r.take(resultsOf("A")); ok {
		t.Error("a second take finds nothing")
	}
}

func TestResetRequestOutsideAForegroundBatchFails(t *testing.T) {
	r := newTestRequests()
	err := r.request("missing", "x")
	if err == nil || err.Error() != "Request new_context from the current foreground tool batch." {
		t.Errorf("request error = %v", err)
	}
	startCall(r, "A")
	endCall(r, "A", false)
	if err := r.request("A", "late"); err == nil {
		t.Error("a finished call is no longer active")
	}
}

func TestResetRequestFirstHandoffWins(t *testing.T) {
	r := newTestRequests()
	startCall(r, "A")
	_ = r.request("A", "first")
	_ = r.request("A", "second")
	endCall(r, "A", false)
	if handoff, _ := r.take(resultsOf("A")); handoff != "first" {
		t.Errorf("handoff = %q", handoff)
	}
}

func TestResetRequestNestedCallsJoinTheirRoot(t *testing.T) {
	r := newTestRequests()
	startCall(r, "A")
	startCall(r, "B", "A")
	startCall(r, "C", "B")
	if err := r.request("C", "from the grandchild"); err != nil {
		t.Fatal(err)
	}
	endCall(r, "C", false)
	endCall(r, "B", false)
	if _, ok := r.take(resultsOf("A")); ok {
		t.Error("the root is still pending until it ends")
	}
	// take cleared everything; replay with the root ending.
	r = newTestRequests()
	startCall(r, "A")
	startCall(r, "B", "A")
	_ = r.request("B", "nested")
	endCall(r, "B", false)
	endCall(r, "A", false)
	if handoff, ok := r.take(resultsOf("A")); !ok || handoff != "nested" {
		t.Errorf("take = %q, %v", handoff, ok)
	}
}

func TestResetRequestFailureCancelsTheRequest(t *testing.T) {
	r := newTestRequests()
	startCall(r, "A")
	startCall(r, "B", "A")
	_ = r.request("B", "nested")
	endCall(r, "B", true)
	endCall(r, "A", false)
	if _, ok := r.take(resultsOf("A")); ok {
		t.Error("a failed nested call fails the whole request")
	}
	// A failing result in the batch cancels a sibling's request.
	r = newTestRequests()
	startCall(r, "A")
	_ = r.request("A", "x")
	endCall(r, "A", false)
	startCall(r, "S")
	endCall(r, "S", true)
	results := []any{rec{"toolCallId": "A"}, rec{"toolCallId": "S", "isError": true}}
	if _, ok := r.take(results); ok {
		t.Error("a failed sibling result cancels the reset")
	}
}

func TestResetRequestIgnoresUnknownParents(t *testing.T) {
	r := newTestRequests()
	startCall(r, "B", "ghost")
	if len(r.active) != 0 {
		t.Error("a call under an unknown parent is not tracked")
	}
	r.start(rec{"toolCallId": "N", "parentToolCallId": nil})
	if len(r.active) != 0 {
		t.Error("a null parent is a defined parent, and unknown")
	}
}

func TestResetRequestFirstSettledHandoffInResultOrder(t *testing.T) {
	r := newTestRequests()
	for _, id := range []string{"A", "B"} {
		startCall(r, id)
		_ = r.request(id, "handoff "+id)
		endCall(r, id, false)
	}
	if handoff, _ := r.take(resultsOf("B", "A")); handoff != "handoff B" {
		t.Errorf("the first result's handoff wins: %q", handoff)
	}
}

func TestResetRequestClear(t *testing.T) {
	r := newTestRequests()
	startCall(r, "A")
	r.clear()
	if err := r.request("A", "x"); err == nil || !strings.Contains(err.Error(), "foreground") {
		t.Errorf("clear forgets active calls: %v", err)
	}
}

func BenchmarkResetRequests(b *testing.B) {
	r := newTestRequests()
	b.ReportAllocs()
	for range b.N {
		startCall(r, "A")
		startCall(r, "B", "A")
		_ = r.request("B", "h")
		endCall(r, "B", false)
		endCall(r, "A", false)
		_, _ = r.take(resultsOf("A"))
	}
}
