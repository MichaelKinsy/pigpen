package pi_subagents

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestHCCall(t *testing.T) {
	f := startFakeHC(t, func(method string, params map[string]any) (any, *hcError) {
		return json.RawMessage(`{"address":"t1","labels":{"herdr.space":"s","herdr.tab":"b"},"status":{"state":"working"},"turn":2}`), nil
	})
	h, err := dialHC(f.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	var term hcTerminal
	if err := h.call("terminal.get", map[string]any{"terminal": "t1"}, &term); err != nil {
		t.Fatal(err)
	}
	eq(t, term.Address, "t1", "address")
	eq(t, term.Status != nil && term.Status.State == "working", true, "status")
	eq(t, term.Turn, uint64(2), "turn")
	eq(t, term.Labels["herdr.tab"], "b", "label")
	if err := h.call("terminal.get", nil, &term); err != nil {
		t.Fatal(err)
	}
	eq(t, f.methods(), []string{"terminal.get", "terminal.get"}, "methods")
	eq(t, f.calls("terminal.get")[0]["terminal"], "t1", "params")
	lines := f.lines()
	for i, l := range lines {
		var req map[string]any
		if err := json.Unmarshal([]byte(l), &req); err != nil {
			t.Fatal(err)
		}
		eq(t, req["jsonrpc"], "2.0", "jsonrpc")
		eq(t, req["id"], float64(i+1), "id")
	}
}

func TestHCError(t *testing.T) {
	f := startFakeHC(t, func(string, map[string]any) (any, *hcError) {
		return nil, &hcError{Kind: "not_found", Message: `no terminal "x"`}
	})
	h, _ := dialHC(f.socket)
	defer h.Close()
	err := h.call("terminal.get", nil, nil)
	var he *hcError
	if !errors.As(err, &he) {
		t.Fatalf("not an *hcError: %v", err)
	}
	eq(t, he.Error(), `herdr-core: no terminal "x" (not_found)`, "Error()")
}

func TestHCClosed(t *testing.T) {
	f := startFakeHC(t, func(string, map[string]any) (any, *hcError) { return nil, nil })
	h, _ := dialHC(f.socket)
	defer h.Close()
	if h.call("x", nil, nil) == nil {
		t.Error("want an error")
	}
}
