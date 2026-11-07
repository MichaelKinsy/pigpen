package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// fakeGateway speaks just enough A2A v1 JSON-RPC/SSE to drive one basket
// iteration: turn 1 pauses for approval, the approval completes it, and
// "[recall]" answers from the first marker.
type fakeGateway struct {
	mu      sync.Mutex
	markers map[string]string // contextId -> marker
	seen    []string
	headers http.Header
}

func newFakeGateway() *fakeGateway { return &fakeGateway{markers: map[string]string{}} }

func (f *fakeGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/.well-known/agent-card.json") {
		w.Write([]byte(`{"name":"fake"}`))
		return
	}
	f.mu.Lock()
	f.headers = r.Header.Clone()
	f.mu.Unlock()
	var req struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params struct {
			Message map[string]any `json:"message"`
		} `json:"params"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	fl := w.(http.Flusher)
	send := func(result any) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
		time.Sleep(2 * time.Millisecond)
	}
	m := req.Params.Message
	text := ""
	for _, p := range m["parts"].([]any) {
		text += p.(map[string]any)["text"].(string)
	}
	ctxID, _ := m["contextId"].(string)
	taskID, _ := m["taskId"].(string)
	meta, _ := m["metadata"].(map[string]any)
	f.mu.Lock()
	f.seen = append(f.seen, text)
	f.mu.Unlock()
	status := func(state string, msg map[string]any, md map[string]any) map[string]any {
		s := map[string]any{"state": state}
		if msg != nil {
			s["message"] = msg
		}
		u := map[string]any{"taskId": taskID, "contextId": ctxID, "status": s}
		if md != nil {
			u["metadata"] = md
		}
		return map[string]any{"statusUpdate": u}
	}
	switch {
	case meta != nil && meta[hitlURI] != nil: // approval
		send(status("TASK_STATE_WORKING", nil, nil))
		send(map[string]any{"artifactUpdate": map[string]any{"taskId": taskID, "contextId": ctxID, "artifact": map[string]any{"artifactId": "a", "parts": []any{
			map[string]any{"data": map[string]any{"id": "c2", "name": "mcp_kagent_kagent_probe"}, "metadata": map[string]any{partTypeKey: "function_call"}},
			map[string]any{"text": "BASKET-DONE " + strings.Repeat("ab", 32)}}}}})
		send(status("TASK_STATE_COMPLETED", nil, map[string]any{usageURI: map[string]any{"inputTokens": 12, "outputTokens": 5}}))
	case strings.Contains(text, "[recall]"):
		f.mu.Lock()
		mk := f.markers[ctxID]
		f.mu.Unlock()
		send(map[string]any{"task": map[string]any{"id": "t2", "contextId": ctxID, "status": map[string]any{"state": "TASK_STATE_SUBMITTED"}}})
		send(map[string]any{"artifactUpdate": map[string]any{"taskId": "t2", "contextId": ctxID, "artifact": map[string]any{"artifactId": "b", "parts": []any{map[string]any{"text": "RECALL " + mk}}}}})
		taskID = "t2"
		send(status("TASK_STATE_COMPLETED", nil, nil))
	default: // first message
		ctxID = "ctx-" + text[:10]
		taskID = "t1"
		f.mu.Lock()
		f.markers[ctxID] = strings.Fields(text)[0]
		f.mu.Unlock()
		send(map[string]any{"task": map[string]any{"id": "t1", "contextId": ctxID, "status": map[string]any{"state": "TASK_STATE_SUBMITTED"}}})
		send(status("TASK_STATE_WORKING", nil, nil))
		send(map[string]any{"artifactUpdate": map[string]any{"taskId": "t1", "contextId": ctxID, "artifact": map[string]any{"artifactId": "a", "parts": []any{
			map[string]any{"data": map[string]any{"id": "c1", "name": "bash"}, "metadata": map[string]any{partTypeKey: "function_call"}}}}}})
		send(status("TASK_STATE_INPUT_REQUIRED", map[string]any{
			"parts":      []any{map[string]any{"text": "Approval is required for tool(s): mcp_kagent_kagent_probe"}},
			"extensions": []any{hitlURI},
			"metadata": map[string]any{hitlURI: map[string]any{"type": "tool_approval_request",
				"tools": []any{map[string]any{"id": "tool-1", "call_id": "c2", "name": "mcp_kagent_kagent_probe", "args": map[string]any{"note": "basket"}}}}},
		}, nil))
	}
}
