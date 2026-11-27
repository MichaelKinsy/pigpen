package eval

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

func saveAndRestoreEvalBridgeHooks(t *testing.T) {
	t.Helper()
	origBudget := TurnBudgetHook
	origResolve := ResolveModelRole
	origComplete := CompleteFn
	t.Cleanup(func() {
		TurnBudgetHook = origBudget
		ResolveModelRole = origResolve
		CompleteFn = origComplete
	})
}

func testBridgeRun(t *testing.T, sessionID string) *bridgeRun {
	t.Helper()
	return &bridgeRun{
		ctx:       context.Background(),
		cwd:       t.TempDir(),
		sessionID: sessionID,
		language:  "py",
	}
}

func makeCompleteTextResponse(text string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"role":       "assistant",
		"stopReason": "stop",
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
	})
	return raw
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestBudgetBridge(t *testing.T) {
	saveAndRestoreEvalBridgeHooks(t)
	run := testBridgeRun(t, "sess-budget-"+newRandomID())

	// Default (no hook) -> {total: nil, spent: 0, hard: false}.
	TurnBudgetHook = nil
	res, err := dispatchBridgeCall(run, "__budget__", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("default __budget__ failed: %v", err)
	}
	m := res.(map[string]any)
	if m["total"] != nil || m["spent"] != 0 || m["hard"] != false {
		t.Fatalf("unexpected default budget: %#v", m)
	}

	// With hook returning hard ceiling.
	TurnBudgetHook = func(_ sdk.Context) (*int, int, bool) {
		total := 50000
		return &total, 12345, true
	}
	res, err = dispatchBridgeCall(run, "__budget__", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("hooked __budget__ failed: %v", err)
	}
	m = res.(map[string]any)
	if m["total"] != 50000 || m["spent"] != 12345 || m["hard"] != true {
		t.Fatalf("unexpected hooked budget: %#v", m)
	}
}

func TestCompletionBridgeLifecycleAndSchema(t *testing.T) {
	saveAndRestoreEvalBridgeHooks(t)
	sid := "sess-comp-" + newRandomID()
	t.Cleanup(func() { lifecycle.End(sid) })
	run := testBridgeRun(t, sid)

	ResolveModelRole = func(_ sdk.Context, sel string) (string, string, string, bool) {
		if sel == "smol" || sel == "default" {
			return "anthropic", "claude-haiku-test", "low", true
		}
		return "", "", "", false
	}

	// 1. Plain text completion + wait + status(attempts).
	CompleteFn = func(_ context.Context, _ sdk.Context, modelObj, req, _ map[string]any) (json.RawMessage, error) {
		if modelObj["id"] != "claude-haiku-test" {
			return nil, errors.New("unexpected model")
		}
		return makeCompleteTextResponse("hello from completion"), nil
	}

	handleRes, err := dispatchBridgeCall(run, "__completion__", mustJSON(t, map[string]any{
		"prompt": "say hi",
		"model":  "smol",
	}))
	if err != nil {
		t.Fatalf("__completion__ failed: %v", err)
	}
	id := handleRes.(map[string]any)["id"].(string)
	if !strings.HasPrefix(id, "cmp-") {
		t.Fatalf("expected cmp- prefix, got %q", id)
	}

	waitRes, err := dispatchBridgeCall(run, "__wait__", mustJSON(t, map[string]any{
		"items": []map[string]any{{"kind": "completion", "id": id}},
	}))
	if err != nil {
		t.Fatalf("__wait__ failed: %v", err)
	}
	snaps := waitRes.(map[string]any)["items"].([]map[string]any)
	if len(snaps) != 1 || snaps[0]["status"] != "completed" || snaps[0]["text"] != "hello from completion" {
		t.Fatalf("unexpected wait snapshot: %#v", snaps)
	}

	statusRes, err := dispatchBridgeCall(run, "__status__", mustJSON(t, map[string]any{
		"item": map[string]any{"kind": "completion", "id": id},
	}))
	if err != nil {
		t.Fatalf("__status__ failed: %v", err)
	}
	stMap := statusRes.(map[string]any)
	attempts, ok := stMap["attempts"].([]completionAttempt)
	if !ok || len(attempts) != 1 || attempts[0].Outcome != "succeeded" || attempts[0].Model != "anthropic/claude-haiku-test" {
		t.Fatalf("unexpected attempts: %#v", stMap["attempts"])
	}

	// 2. Structured schema completion extracts JSON from fenced output into data.
	CompleteFn = func(_ context.Context, _ sdk.Context, _, req, _ map[string]any) (json.RawMessage, error) {
		sys, _ := req["systemPrompt"].(string)
		if !strings.Contains(sys, "JSON Schema") {
			return nil, errors.New("missing schema instruction in systemPrompt")
		}
		return makeCompleteTextResponse("```json\n{\"ok\":true,\"count\":3}\n```"), nil
	}
	handleRes, err = dispatchBridgeCall(run, "__completion__", mustJSON(t, map[string]any{
		"prompt": "return json",
		"model":  "smol",
		"schema": map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}},
	}))
	if err != nil {
		t.Fatalf("schema __completion__ failed: %v", err)
	}
	schemaID := handleRes.(map[string]any)["id"].(string)
	waitRes, err = dispatchBridgeCall(run, "__wait__", mustJSON(t, map[string]any{
		"items": []map[string]any{{"kind": "completion", "id": schemaID}},
	}))
	if err != nil {
		t.Fatalf("schema __wait__ failed: %v", err)
	}
	snap := waitRes.(map[string]any)["items"].([]map[string]any)[0]
	if snap["status"] != "completed" {
		t.Fatalf("expected completed schema snap, got %#v", snap)
	}
	dataMap, ok := snap["data"].(map[string]any)
	if !ok || dataMap["ok"] != true || dataMap["count"] != float64(3) {
		t.Fatalf("unexpected schema data: %#v", snap["data"])
	}

	// 3. Error propagation.
	CompleteFn = func(_ context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		return nil, errors.New("provider boom")
	}
	handleRes, err = dispatchBridgeCall(run, "__completion__", mustJSON(t, map[string]any{
		"prompt": "fail",
		"model":  "smol",
	}))
	if err != nil {
		t.Fatalf("error __completion__ start failed: %v", err)
	}
	errID := handleRes.(map[string]any)["id"].(string)
	waitRes, err = dispatchBridgeCall(run, "__wait__", mustJSON(t, map[string]any{
		"items": []map[string]any{{"kind": "completion", "id": errID}},
	}))
	if err != nil {
		t.Fatalf("error __wait__ failed: %v", err)
	}
	snap = waitRes.(map[string]any)["items"].([]map[string]any)[0]
	if snap["status"] != "failed" || !strings.Contains(snap["error"].(string), "provider boom") {
		t.Fatalf("expected failed snap with provider boom, got %#v", snap)
	}

	// 4. Timeout and cancel.
	blockCh := make(chan struct{})
	t.Cleanup(func() { close(blockCh) })
	CompleteFn = func(goCtx context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		select {
		case <-goCtx.Done():
			return nil, goCtx.Err()
		case <-blockCh:
			return makeCompleteTextResponse("late"), nil
		}
	}
	handleRes, err = dispatchBridgeCall(run, "__completion__", mustJSON(t, map[string]any{
		"prompt": "slow",
		"model":  "smol",
	}))
	if err != nil {
		t.Fatalf("slow __completion__ failed: %v", err)
	}
	slowID := handleRes.(map[string]any)["id"].(string)

	// Wait with tiny timeout returns running snapshot.
	waitRes, err = dispatchBridgeCall(run, "__wait__", mustJSON(t, map[string]any{
		"items":     []map[string]any{{"kind": "completion", "id": slowID}},
		"timeoutMs": 15,
	}))
	if err != nil {
		t.Fatalf("timeout __wait__ failed: %v", err)
	}
	snap = waitRes.(map[string]any)["items"].([]map[string]any)[0]
	if snap["status"] != "running" {
		t.Fatalf("expected running on timeout, got %#v", snap)
	}

	// Cancel marks cancelled.
	cancelRes, err := dispatchBridgeCall(run, "__cancel__", mustJSON(t, map[string]any{
		"item": map[string]any{"kind": "completion", "id": slowID},
	}))
	if err != nil {
		t.Fatalf("__cancel__ failed: %v", err)
	}
	if cancelRes.(map[string]any)["cancelled"] != true {
		t.Fatalf("expected cancelled=true, got %#v", cancelRes)
	}
	waitRes, err = dispatchBridgeCall(run, "__wait__", mustJSON(t, map[string]any{
		"items": []map[string]any{{"kind": "completion", "id": slowID}},
	}))
	if err != nil {
		t.Fatalf("post-cancel __wait__ failed: %v", err)
	}
	snap = waitRes.(map[string]any)["items"].([]map[string]any)[0]
	if snap["status"] != "cancelled" {
		t.Fatalf("expected cancelled status, got %#v", snap)
	}

	// Session isolation & lifecycle cleanup.
	otherRun := testBridgeRun(t, "other-sess-"+newRandomID())
	if _, err := dispatchBridgeCall(otherRun, "__status__", mustJSON(t, map[string]any{
		"item": map[string]any{"kind": "completion", "id": id},
	})); err == nil {
		t.Fatal("expected cross-session completion lookup to fail")
	}
	lifecycle.End(sid)
	if _, ok := getCompletionHandle(sid, id); ok {
		t.Fatal("expected lifecycle.End to remove completion handles")
	}
}

func TestJudgeBridge(t *testing.T) {
	saveAndRestoreEvalBridgeHooks(t)
	sid := "sess-judge-" + newRandomID()
	run := testBridgeRun(t, sid)

	ResolveModelRole = func(_ sdk.Context, sel string) (string, string, string, bool) {
		if sel == "judge" {
			return "openai", "gpt-judge-mini", "", true
		}
		return "", "", "", false
	}
	CompleteFn = func(_ context.Context, _ sdk.Context, modelObj, _, _ map[string]any) (json.RawMessage, error) {
		if modelObj["id"] != "gpt-judge-mini" {
			return nil, errors.New("expected @judge model")
		}
		return makeCompleteTextResponse(`{"sentiment":"positive","safe":true,"quality":2}`), nil
	}

	questions := map[string]any{
		"sentiment": map[string]any{
			"type":         "choice",
			"instructions": "Classify sentiment",
			"criteria":     map[string]any{"positive": "happy", "negative": "sad"},
		},
		"safe": map[string]any{
			"type":         "bool",
			"instructions": "Is the text safe?",
			"criteria":     map[string]any{"true": "safe", "false": "unsafe"},
		},
		"quality": map[string]any{
			"type":         "score",
			"instructions": "Rate quality",
			"criteria":     []any{"poor", "ok", "great"},
		},
	}

	res, err := dispatchBridgeCall(run, "__judge__", mustJSON(t, map[string]any{
		"state":     "Everything worked cleanly!",
		"questions": questions,
	}))
	if err != nil {
		t.Fatalf("__judge__ failed: %v", err)
	}
	m := res.(map[string]any)
	if m["model"] != "openai/gpt-judge-mini" {
		t.Fatalf("unexpected judge model: %v", m["model"])
	}
	answers := m["answers"].(map[string]any)

	sent := answers["sentiment"].(map[string]any)
	if sent["type"] != "choice" || sent["choice"] != "positive" {
		t.Fatalf("unexpected choice answer: %#v", sent)
	}
	safe := answers["safe"].(map[string]any)
	if safe["type"] != "bool" || safe["bool"] != 1.0 {
		t.Fatalf("unexpected bool answer: %#v", safe)
	}
	qual := answers["quality"].(map[string]any)
	if qual["type"] != "score" || qual["score"] != 2.0 {
		t.Fatalf("unexpected score answer: %#v", qual)
	}

	// Invalid choice label from model returns an error.
	CompleteFn = func(_ context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		return makeCompleteTextResponse(`{"sentiment":"unknown","safe":true,"quality":2}`), nil
	}
	if _, err := dispatchBridgeCall(run, "__judge__", mustJSON(t, map[string]any{
		"state":     "hi",
		"questions": questions,
	})); err == nil {
		t.Fatal("expected error for invalid choice option")
	}
}

func TestJudgeBatchBridge(t *testing.T) {
	saveAndRestoreEvalBridgeHooks(t)
	sid := "sess-jbatch-" + newRandomID()
	t.Cleanup(func() { lifecycle.End(sid) })
	run := testBridgeRun(t, sid)

	ResolveModelRole = func(_ sdk.Context, sel string) (string, string, string, bool) {
		if sel == "smol" {
			return "openai", "gpt-smol", "", true
		}
		return "", "", "", false
	}

	var calls atomic.Int32
	CompleteFn = func(_ context.Context, _ sdk.Context, _, req, _ map[string]any) (json.RawMessage, error) {
		calls.Add(1)
		msgs := req["messages"].([]map[string]any)
		blocks := msgs[0]["content"].([]map[string]any)
		prompt := blocks[0]["text"].(string)
		if strings.Contains(prompt, "BAD_ITEM") {
			return nil, errors.New("item error")
		}
		return makeCompleteTextResponse(`{"ok":true}`), nil
	}

	questions := map[string]any{
		"ok": map[string]any{
			"type":         "bool",
			"instructions": "Is it ok?",
		},
	}

	createRes, err := dispatchBridgeCall(run, "__judge_batch__", mustJSON(t, map[string]any{
		"op": "create",
		"items": []map[string]any{
			{"key": "a", "state": "good 1"},
			{"key": "b", "state": "BAD_ITEM"},
			{"key": "c", "state": "good 2"},
		},
		"questions":   questions,
		"concurrency": 2,
		"retries":     0,
		"minOk":       1,
		"intent":      "Checking items",
	}))
	if err != nil {
		t.Fatalf("judge_batch create failed: %v", err)
	}
	st := createRes.(map[string]any)
	batchID := st["id"].(string)
	if st["total"] != 3 || st["intent"] != "Checking items" {
		t.Fatalf("unexpected create status: %#v", st)
	}

	// Drain all items until empty.
	var drained []judgmentBatchItem
	for range 10 {
		dr, err := dispatchBridgeCall(run, "__judge_batch__", mustJSON(t, map[string]any{
			"op":        "drain",
			"id":        batchID,
			"timeoutMs": 2000,
		}))
		if err != nil {
			t.Fatalf("drain failed: %v", err)
		}
		items := dr.(map[string]any)["items"].([]judgmentBatchItem)
		if len(items) == 0 {
			break
		}
		drained = append(drained, items...)
	}
	if len(drained) != 3 {
		t.Fatalf("expected 3 drained items, got %d", len(drained))
	}

	// Status, results, failed.
	statusRes, err := dispatchBridgeCall(run, "__judge_batch__", mustJSON(t, map[string]any{
		"op": "status",
		"id": batchID,
	}))
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	st = statusRes.(map[string]any)
	if st["done"] != 3 || st["failed"] != 1 || st["running"] != false {
		t.Fatalf("unexpected final status: %#v", st)
	}

	resOut, err := dispatchBridgeCall(run, "__judge_batch__", mustJSON(t, map[string]any{
		"op": "results",
		"id": batchID,
	}))
	if err != nil {
		t.Fatalf("results failed: %v", err)
	}
	resultsMap := resOut.(map[string]any)["results"].(map[string]map[string]any)
	if len(resultsMap) != 2 || resultsMap["a"] == nil || resultsMap["c"] == nil {
		t.Fatalf("unexpected results map: %#v", resultsMap)
	}

	failOut, err := dispatchBridgeCall(run, "__judge_batch__", mustJSON(t, map[string]any{
		"op": "failed",
		"id": batchID,
	}))
	if err != nil {
		t.Fatalf("failed op failed: %v", err)
	}
	failedMap := failOut.(map[string]any)["failed"].(map[string]string)
	if len(failedMap) != 1 || !strings.Contains(failedMap["b"], "item error") {
		t.Fatalf("unexpected failed map: %#v", failedMap)
	}

	// Close removes batch.
	closeOut, err := dispatchBridgeCall(run, "__judge_batch__", mustJSON(t, map[string]any{
		"op": "close",
		"id": batchID,
	}))
	if err != nil || closeOut.(map[string]any)["closed"] != true {
		t.Fatalf("close failed: %v %#v", err, closeOut)
	}
	if _, err := dispatchBridgeCall(run, "__judge_batch__", mustJSON(t, map[string]any{
		"op": "status",
		"id": batchID,
	})); err == nil {
		t.Fatal("expected status after close to fail")
	}

	// Cancel + lifecycle.End cleanup test.
	var mu sync.Mutex
	started := make(chan struct{}, 2)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	CompleteFn = func(goCtx context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		mu.Lock()
		select {
		case started <- struct{}{}:
		default:
		}
		mu.Unlock()
		select {
		case <-goCtx.Done():
			return nil, goCtx.Err()
		case <-block:
			return makeCompleteTextResponse(`{"ok":true}`), nil
		}
	}

	c2, err := dispatchBridgeCall(run, "__judge_batch__", mustJSON(t, map[string]any{
		"op":        "create",
		"items":     []map[string]any{{"key": 0, "state": "s0"}, {"key": 1, "state": "s1"}},
		"questions": questions,
	}))
	if err != nil {
		t.Fatalf("second create failed: %v", err)
	}
	id2 := c2.(map[string]any)["id"].(string)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("batch worker did not start")
	}

	cancelOut, err := dispatchBridgeCall(run, "__judge_batch__", mustJSON(t, map[string]any{
		"op": "cancel",
		"id": id2,
	}))
	if err != nil || cancelOut.(map[string]any)["cancelled"] != true {
		t.Fatalf("cancel failed: %v %#v", err, cancelOut)
	}

	lifecycle.End(sid)
	if _, ok := getJudgmentBatch(sid, id2); ok {
		t.Fatal("expected lifecycle.End to clean up judge batches")
	}
}
