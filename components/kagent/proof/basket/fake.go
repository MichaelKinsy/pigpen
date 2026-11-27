package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// fakeGateway speaks just enough A2A v1 JSON-RPC/SSE to drive one basket
// iteration: turn 1 pauses for approval, the approval completes it, and
// "[recall]" answers from the first marker.
type fakeGateway struct {
	mu         sync.Mutex
	markers    map[string]string // contextId -> marker
	approveCtx map[string]bool   // contextId -> the session started with [approve]
	seen       []string
	headers    http.Header
	// faults are consumed one per follow-up request (a message carrying a contextId), before it is handled:
	// notaccepted (HTTP 400 with KAGENT_SEND_NOT_ACCEPTED), rpc:<code> (HTTP 200 JSON body with a JSON-RPC error), sse:<code> (the error as an SSE event), http:<status>,
	// empty (an SSE stream with no events), hang (no answer until the client gives up).
	faults []string
	nFault int // faults served so far
}

// fakeRejectionText is the tool result a harness hands the model after a rejection (the shape the PiG adapter uses).
func fakeRejectionText(reason string) string {
	return "Tool call was rejected by user. Reason: " + reason
}

func fakeSha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func newFakeGateway() *fakeGateway {
	return &fakeGateway{markers: map[string]string{}, approveCtx: map[string]bool{}}
}

func newFakeGatewayFaults(list string) *fakeGateway {
	f := newFakeGateway()
	for _, x := range strings.Split(list, ",") {
		if x = strings.TrimSpace(x); x != "" {
			f.faults = append(f.faults, x)
		}
	}
	return f
}

// fault serves the next queued fault, if any, and reports whether it handled the request.
func (f *fakeGateway) fault(w http.ResponseWriter, r *http.Request, id string) bool {
	f.mu.Lock()
	if len(f.faults) == 0 {
		f.mu.Unlock()
		return false
	}
	kind := f.faults[0]
	f.faults = f.faults[1:]
	f.nFault++
	f.mu.Unlock()
	name, arg, _ := strings.Cut(kind, ":")
	rpcErr := func() []byte {
		code, _ := strconv.Atoi(arg)
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": "fake fault"}})
		return b
	}
	switch name {
	case "rpc":
		w.Header().Set("Content-Type", "application/json")
		w.Write(rpcErr())
	case "sse":
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", rpcErr())
	case "notaccepted": // what a gateway answers when the session is not available yet (sendStream retries it unless NoSendRetry)
		http.Error(w, `{"error":{"code":-32004,"message":"KAGENT_SEND_NOT_ACCEPTED input was not accepted"}}`, 400)
	case "http":
		code, _ := strconv.Atoi(arg)
		http.Error(w, "fake fault", code)
	case "empty":
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
	case "hang":
		<-r.Context().Done()
	}
	return true
}

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
	m := req.Params.Message
	if c, _ := m["contextId"].(string); c != "" && f.fault(w, r, req.ID) {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	fl := w.(http.Flusher)
	send := func(result any) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
		time.Sleep(2 * time.Millisecond)
	}
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
		done := "BASKET-DONE " + strings.Repeat("ab", 32)
		f.mu.Lock()
		isApprove := f.approveCtx[ctxID]
		f.mu.Unlock()
		if isApprove { // an [approve] session: APPROVE-DONE over the tool result; a rejection's result is its text
			result := "probe:basket"
			if items, _ := asMap(meta[hitlURI])["approvals"].([]any); len(items) > 0 && asMap(items[0])["approved"] == false {
				result = fakeRejectionText(asStr(asMap(items[0])["rejection_reason"]))
			}
			done = "APPROVE-DONE " + fakeSha(result)
		}
		send(status("TASK_STATE_WORKING", nil, nil))
		send(map[string]any{"artifactUpdate": map[string]any{"taskId": taskID, "contextId": ctxID, "artifact": map[string]any{"artifactId": "a", "parts": []any{
			map[string]any{"data": map[string]any{"id": "c2", "name": "mcp_kagent_kagent_probe"}, "metadata": map[string]any{partTypeKey: "function_call"}},
			map[string]any{"text": done}}}}})
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
		f.approveCtx[ctxID] = strings.Contains(text, "[approve]")
		f.mu.Unlock()
		send(map[string]any{"task": map[string]any{"id": "t1", "contextId": ctxID, "status": map[string]any{"state": "TASK_STATE_SUBMITTED"}}})
		if !strings.Contains(text, "[basket]") && !strings.Contains(text, "[approve]") { // a plain message: answered at once
			send(map[string]any{"artifactUpdate": map[string]any{"taskId": "t1", "contextId": ctxID, "artifact": map[string]any{"artifactId": "a", "parts": []any{map[string]any{"text": "ECHO " + text}}}}})
			send(status("TASK_STATE_COMPLETED", nil, nil))
			return
		}
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
