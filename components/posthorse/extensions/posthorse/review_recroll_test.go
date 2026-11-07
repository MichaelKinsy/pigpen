package posthorse

// Review tests (recovery, retention, reminders, rollover). Each failing test demonstrates a divergence from the
// TypeScript original; the expected values come from the oracle (see port/review/recroll.md for the probes).

import (
	"bytes"
	"encoding/json"
	"testing"
)

// DIVERGENCE: recovery-results.ts:61-69 (callDetails) prints safeJsonStringify(matching.block.arguments), and
// JSON.stringify keeps the member order the model emitted. The port receives the call as a decoded
// map[string]any and prints the members sorted, so the "Call arguments:" line of every automatic recovery record
// with tool-result evidence differs from the original. Oracle (node, port/oracle/src/recovery-results.ts):
// "ok\n\nTool: read\nCall ID: c1\nCall arguments: {\"path\":\"a.txt\",\"offset\":1,\"limit\":2}".
func TestReviewRecrollCallArgumentsMemberOrder(t *testing.T) {
	t.Skip("R1: SDK finding, not worked around here: tool-call arguments reach the port as decoded map[string]any, so JSON.stringify member order is lost; needs order-preserving delivery of event payloads and session reads (see port/review/recroll.md)")
	var assistant, result rec
	if err := json.Unmarshal([]byte(`{"role":"assistant","stopReason":"toolUse","content":[{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"a.txt","offset":1,"limit":2}}]}`), &assistant); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"ok"}],"isError":false}`), &result); err != nil {
		t.Fatal(err)
	}
	batch := recoverableToolResults([]recoveryEntry{{id: "a1", hasID: true, message: assistant}, {id: "r1", hasID: true, message: result}})
	if batch == nil || len(batch.blocks) != 1 {
		t.Fatalf("want one block, got %+v", batch)
	}
	const want = "ok\n\nTool: read\nCall ID: c1\nCall arguments: {\"path\":\"a.txt\",\"offset\":1,\"limit\":2}"
	if got := batch.blocks[0].text; got != want {
		t.Errorf("recovery block text\n got %q\nwant %q (oracle)", got, want)
	}
}

// DIVERGENCE: rollover.ts:57-73 and 291-301 ([...event.entries, draft]) and the turn_end handler returning undefined
// (rollover.ts:451) leave a foreign draft object untouched, so its data/details keep their member order when Pi
// persists them. The port decodes event.entries into maps; the SDK echoes them as _pigBoundaryEntries
// (sdk/extension.go:1641) and the host adopts that list (coding/extension/host/subprocess/host.go:4576-4593), with
// the members sorted (sdk/json/encode.go:1004-1009). Installing the Go Posthorse therefore reorders the persisted
// data of every other extension whose turn_end draft precedes it, on every turn.
func TestReviewRecrollForeignDraftMemberOrder(t *testing.T) {
	t.Skip("R2: SDK/host finding, not worked around here: the SDK echoes the decoded turn_end entries as _pigBoundaryEntries and the host adopts that list with sorted data/details members; needs a raw echo of unchanged drafts (see port/review/recroll.md)")
	foreign := json.RawMessage(`[{"type":"custom","customType":"other","data":{"z":1,"a":2}}]`)
	kept := []byte(`"data":{"z":1,"a":2}`)
	t.Run("a turn Posthorse leaves alone keeps the foreign draft as it was", func(t *testing.T) {
		f := newFixture(t)
		host, leave := f.enter()
		defer leave()
		raw := f.fireFirst(host, "turn_end", rec{
			"message":     rec{"role": "assistant", "stopReason": "stop", "content": []any{}},
			"toolResults": []any{},
			"continue":    false,
			"outcome":     "completed",
			"entries":     foreign,
			"context":     rec{"pendingMessages": []any{}, "contextEntries": []any{}},
		})
		if n := bytes.Count(raw, kept); n != 1 {
			t.Errorf("foreign draft data must keep {z, a} (TS returns undefined and Pi keeps the object); reply %s", raw)
		}
	})
	t.Run("an explicit reset re-emits the foreign draft in its order", func(t *testing.T) {
		f := newFixture(t)
		f.Run("new_context", rec{"handoff": "continue here"})
		host, leave := f.enter()
		defer leave()
		raw := f.fireFirst(host, "turn_end", rec{
			"message":     rec{"role": "assistant", "stopReason": "toolUse", "content": []any{}},
			"toolResults": []any{rec{"toolName": "new_context", "toolCallId": "id"}},
			"continue":    false,
			"outcome":     "completed",
			"entries":     foreign,
			"context":     rec{"pendingMessages": []any{}},
		})
		if !bytes.Contains(raw, []byte(`"reason":"explicit"`)) {
			t.Fatalf("want an explicit compaction draft, reply %s", raw)
		}
		if n := bytes.Count(raw, kept); n != 2 {
			t.Errorf("both the echoed list and the result entries must keep {z, a} (TS appends to the same object); reply %s", raw)
		}
	})
}

// DIVERGENCE: rollover.ts:116-149 and 150-166 return the incoming message objects themselves (filter / slice), so a
// tool call's arguments keep the member order the model sent when Pi builds the next provider request. The port
// returns the decoded maps; the SDK sends the whole list (sdk/context_event.go:25-73) with sorted members, and PiG
// keeps the order it decodes (ai/types.go:118-125). Every LLM call of a window that a retained-tail rollover opened
// (and every call that drops a stale reminder) therefore sends reordered tool arguments: model-visible, and a
// prompt-cache prefix change against the requests Posthorse leaves alone.
func TestReviewRecrollContextToolArgumentOrder(t *testing.T) {
	t.Skip("R3: SDK finding, not worked around here: a changed context list is sent back as decoded maps with sorted tool-call argument members; needs order-preserving context messages (see port/review/recroll.md)")
	summary := autoHandoffPrefix + "\nContext rolled over after entry boundary."
	session := fxMemorySession("")
	session.appendMessage(rec{"role": "user", "content": "a", "timestamp": float64(1)})
	kept := session.appendMessage(rec{"role": "assistant", "content": []any{rec{"type": "toolCall", "id": "c1", "name": "read", "arguments": rec{"path": "a.txt", "offset": float64(1)}}}, "stopReason": "toolUse", "timestamp": float64(1)})
	session.appendMessage(rec{"role": "toolResult", "toolCallId": "c1", "toolName": "read", "content": []any{fxText("ok")}, "isError": false, "timestamp": float64(1)})
	session.appendCompaction(summary, kept, 100, rec{"posthorse": float64(1)})
	f := newFixture(t, withSession(session), withRealProjection())
	host, leave := f.enter()
	defer leave()
	raw := f.fireFirst(host, "context", rec{"messages": json.RawMessage(`[` +
		`{"role":"compactionSummary","summary":"Automatic context rollover recovery record.\nContext rolled over after entry boundary.","timestamp":5},` +
		`{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"a.txt","offset":1}}],"stopReason":"toolUse","timestamp":1},` +
		`{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"ok"}],"isError":false,"timestamp":1},` +
		`{"role":"user","content":"after","timestamp":1}]`)})
	if !bytes.Contains(raw, []byte(`posthorse-retained-tail`)) {
		t.Fatalf("want the retained tail reordered, reply %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"arguments":{"path":"a.txt","offset":1}`)) {
		t.Errorf("the reordered context must carry the model's argument order {path, offset}; reply %s", raw)
	}
}
