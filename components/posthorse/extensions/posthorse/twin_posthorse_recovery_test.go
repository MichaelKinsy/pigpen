package posthorse

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// Twins of the recovery-record cases of test/posthorse.test.ts.

const rcFile = "posthorse"

func rcMatch(t *testing.T, s, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(s) {
		t.Errorf("expected match for %s in:\n%s", pattern, s)
	}
}

func rcNoMatch(t *testing.T, s, pattern string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(s) {
		t.Errorf("unexpected match for %s in:\n%s", pattern, s)
	}
}

func rcIndex(t *testing.T, s, sub string) int {
	t.Helper()
	i := strings.Index(s, sub)
	if i < 0 {
		t.Fatalf("%q not found", sub)
	}
	return i
}

// rcProjection is the default projection of the upstream automaticResult helper.
func rcProjection(branch []rec) []projectedEntry {
	var out []projectedEntry
	for _, entry := range branch {
		if entry["type"] == "message" {
			out = append(out, projectedEntry{sourceEntry: entry, messages: []rec{entry["message"].(rec)}})
		}
	}
	return out
}

func rcBuild(t *testing.T, entries []rec, projected []projectedEntry, maxChars int, opts autoHandoffOptions) string {
	t.Helper()
	out, err := buildAutoHandoff(entries, projected, maxChars, opts)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func rcMsg(id string, message rec) rec { return rec{"type": "message", "id": id, "message": message} }

func rcTimed(id, ts string, message rec) rec {
	return rec{"type": "message", "id": id, "timestamp": ts, "message": message}
}

func rcCall(id, name string, args rec) rec {
	return rec{"type": "toolCall", "id": id, "name": name, "arguments": args}
}

func rcSet(ids ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

func rcRepeat(s string, n int) string { return strings.Repeat(s, n) }

func rcAutomatic(id string) rec {
	return rec{
		"type": "compaction", "id": id, "firstKeptEntryId": nil,
		"details": rec{"posthorse": float64(1), "reason": "threshold"},
		"summary": "Automatic context rollover recovery record.\nSECRET_NESTED_TOOL_EVIDENCE",
	}
}

func TestTwinPosthorseRecoveryRecords(t *testing.T) {
	tw(t, rcFile, "new_context requests a turn-boundary reset and automatic rollover builds a recovery record", func(t *testing.T) {
		f := newFixture(t)
		result := f.Run("new_context", rec{"handoff": "continue here"})
		rcMatch(t, result.Text(), `Requested a fresh Pi context`)
		if got := f.Fire("session_before_compact", rec{"reason": "manual"}); got != nil {
			t.Errorf("manual /compact is left to Pi, got %v", got)
		}

		handoff := f.AutomaticHandoff([]rec{
			rcTimed("user", "2026-09-02T10:00:00Z", rec{"role": "user", "content": "keep working on the fix"}),
		})
		rcMatch(t, handoff, `^Automatic context rollover recovery record\.`)
		rcMatch(t, handoff, `owner input.*entry user`)
		rcMatch(t, handoff, `keep working on the fix`)
		rcMatch(t, handoff, `not current progress`)

		prior := f.AutomaticHandoff([]rec{
			{"type": "context_window", "id": "prior", "timestamp": "2026-09-02T10:01:00Z", "handoff": "persisted task"},
		})
		rcMatch(t, prior, `older checkpoint; possibly stale`)
		rcMatch(t, prior, `persisted task`)
		rcMatch(t, prior, `No selected current-window`)
		if rcIndex(t, prior, "No selected current-window") >= rcIndex(t, prior, "older checkpoint") {
			t.Error("the empty-input header must precede the older checkpoint")
		}

		nonRecursive := f.AutomaticHandoff([]rec{
			{
				"type": "context_window", "id": "prior-auto", "timestamp": "2026-09-02T10:02:00Z",
				"handoff": "Automatic context rollover recovery record.\nSECRET NESTED TEXT",
			},
		})
		rcMatch(t, nonRecursive, `Earlier automatic records skipped`)
		rcMatch(t, nonRecursive, `prior-auto`)
		rcNoMatch(t, nonRecursive, `SECRET NESTED TEXT`)
	})

	tw(t, rcFile, "a minimum-budget automatic record keeps a complete history pointer without nested evidence", func(t *testing.T) {
		boundary := func(id string) rec {
			return rec{
				"type": "compaction", "id": id, "firstKeptEntryId": nil,
				"details": rec{"posthorse": float64(1), "reason": "threshold"},
				"summary": "Automatic context rollover recovery record.\nOLD_EVIDENCE",
			}
		}
		many := make([]rec, 100)
		for i := range many {
			many[i] = boundary(fmt.Sprintf("record-%d", i))
		}
		branches := [][]rec{{boundary("deadbeef")}, many}
		for index, entries := range branches {
			handoff := rcBuild(t, entries, nil, 1_000, autoHandoffOptions{})
			if u16len(handoff) > 1_000 {
				t.Errorf("handoff is %d units, want <= 1000", u16len(handoff))
			}
			if index == 0 {
				rcMatch(t, handoff, `use history read.*deadbeef`)
			} else {
				rcMatch(t, handoff, `use history read.*record-99`)
			}
			rcNoMatch(t, handoff, `OLD_EVIDENCE`)
			if index == 1 {
				rcMatch(t, handoff, `99 earlier record\(s\) not listed`)
			}
		}
	})

	tw(t, rcFile, "automatic ancestry stops at the nearest owner window without resurrecting superseded intent", func(t *testing.T) {
		branch := []rec{
			rcMsg("superseded", rec{"role": "user", "content": "SUPERSEDED_OWNER_INTENT"}),
			rcAutomatic("auto-1"),
			rcTimed("nearest-owner", "2026-10-10T16:40:00Z", rec{
				"role": "user", "content": "CURRENT_OWNER_START " + rcRepeat("x", 15_000) + " CURRENT_OWNER_TAIL",
			}),
			rcAutomatic("auto-2"),
			rcAutomatic("auto-3"),
		}
		handoff := rcAutomaticHandoff(t, branch)
		if u16len(handoff) > 20_000 {
			t.Errorf("handoff is %d units, want <= 20000", u16len(handoff))
		}
		rcMatch(t, handoff, `CURRENT_OWNER_START`)
		rcMatch(t, handoff, `CURRENT_OWNER_TAIL`)
		rcMatch(t, handoff, `owner input; carried from an earlier window`)
		rcMatch(t, handoff, `2026-10-10T16:40:00Z \| entry nearest-owner`)
		rcMatch(t, handoff, `auto-3, auto-2`)
		rcNoMatch(t, handoff, `SUPERSEDED_OWNER_INTENT|SECRET_NESTED_TOOL_EVIDENCE|auto-1`)
	})

	tw(t, rcFile, "automatic recovery keeps owner anchors and visible coordination without stale transcript guesses", func(t *testing.T) {
		branch := []rec{
			rcMsg("old", rec{"role": "user", "content": "old completed task"}),
			{"type": "context_window", "id": "window-2", "timestamp": "2026-09-02T10:00:00Z", "handoff": "Original approved goal"},
			{"type": "custom_message", "id": "hidden", "timestamp": "2026-09-02T10:01:00Z", "customType": "todo-list-context", "content": "hidden state", "display": false},
			{"type": "custom_message", "id": "reminder", "timestamp": "2026-09-02T10:02:00Z", "customType": "posthorse-reminder", "content": "stale reminder", "display": true},
			rcTimed("owner-start", "2026-09-02T10:03:00Z", rec{"role": "user", "content": "FIRST OWNER REQUEST " + rcRepeat("a", 9_000) + " OWNER REQUEST TAIL"}),
			rcTimed("ordinary-tool", "2026-09-02T10:04:00Z", rec{"role": "toolResult", "toolName": "bash", "content": "assistant-derived state"}),
		}
		for index := range 8 {
			branch = append(branch, rec{
				"type": "custom_message", "id": fmt.Sprintf("coord-%d", index),
				"timestamp":  fmt.Sprintf("2026-09-02T10:%d:00Z", 10+index),
				"customType": "intercom_message",
				"content":    fmt.Sprintf("coordination %d %s", index, rcRepeat("x", 3_500)),
				"display":    true,
			})
			if index == 3 {
				branch = append(branch, rcTimed("owner-middle", "2026-09-02T10:13:30Z", rec{
					"role": "user", "content": "Do not touch the billing tables\n\nuntil I confirm",
				}))
			}
		}
		branch = append(branch,
			rcMsg("question-call", rec{"role": "assistant", "content": []any{rcCall("question", "ask_question", rec{})}}),
			rcTimed("owner-answer", "2026-09-02T10:20:00Z", rec{
				"role": "toolResult", "toolName": "ask_question", "toolCallId": "question", "isError": false,
				"content": "OWNER APPROVED SHIP",
			}),
			rec{"type": "custom_message", "id": "latest-correction", "timestamp": "2026-09-02T10:21:00Z", "customType": "agent-irc", "content": "LATEST COORDINATION CORRECTION", "display": true},
		)
		f := newFixture(t, withExtraTools("ask_question"))
		handoff := f.AutomaticHandoff(branch)
		if u16len(handoff) > 20_000 {
			t.Errorf("handoff is %d units, want <= 20000", u16len(handoff))
		}
		rcMatch(t, handoff, `Original approved goal`)
		rcMatch(t, handoff, `FIRST OWNER REQUEST`)
		rcMatch(t, handoff, `OWNER REQUEST TAIL`)
		rcMatch(t, handoff, `middle omitted`)
		rcMatch(t, handoff, `OWNER APPROVED SHIP`)
		rcMatch(t, handoff, `LATEST COORDINATION CORRECTION`)
		rcMatch(t, handoff, `not direct owner input`)
		rcMatch(t, handoff, `Other current-window inputs, oldest first \(recover with history read\):`)
		for index := range 8 {
			rcMatch(t, handoff, fmt.Sprintf(`\| entry coord-%d\]`, index))
		}
		rcNoMatch(t, handoff, `coordination \d x`)
		rcMatch(t, handoff, `\[owner input \| 2026-09-02T10:13:30Z \| entry owner-middle\] Do not touch the billing tables until I confirm`)
		if !(rcIndex(t, handoff, "entry owner-start") < rcIndex(t, handoff, "entry owner-answer")) ||
			!(rcIndex(t, handoff, "entry owner-answer") < rcIndex(t, handoff, "entry latest-correction")) ||
			!(rcIndex(t, handoff, "entry latest-correction") < rcIndex(t, handoff, "older checkpoint")) {
			t.Error("anchors must appear owner-start, owner-answer, latest-correction, then the checkpoint")
		}
		rcNoMatch(t, handoff, `old completed task|hidden state|stale reminder|assistant-derived state`)
	})

	tw(t, rcFile, "automatic handoff carries trailing synchronous results, with entry ids and no base64", func(t *testing.T) {
		image := rec{"type": "image", "data": "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo=", "mimeType": "image/png"}
		batch := []rec{
			{"type": "context_window", "id": "older-window", "handoff": "FIRST read obsolete-checkpoint.md"},
			rcTimed("owner", "1", rec{"role": "user", "content": "run the checks"}),
			rcTimed("assistant-1", "2", rec{"role": "assistant", "content": []any{
				rec{"type": "text", "text": "ASSISTANT PROSE"},
				rcCall("call-1", "bash", rec{"command": "npm test"}),
				rcCall("call-2", "read", rec{"path": "missing.txt"}),
				rcCall("call-3", "screenshot", rec{}),
			}}),
			rcTimed("result-1", "3", rec{"role": "toolResult", "toolCallId": "call-1", "toolName": "bash", "content": []any{
				rec{"type": "text", "text": "3 tests failed FAILURE DETAIL " + rcRepeat("z", 30_000) + " FAILURE TAIL"},
			}}),
			rcTimed("result-2", "4", rec{"role": "toolResult", "toolCallId": "call-2", "toolName": "read", "isError": true, "content": "ENOENT missing.txt"}),
			rcTimed("result-3", "5", rec{"role": "toolResult", "toolCallId": "call-3", "toolName": "screenshot", "content": []any{image}}),
		}
		handoff := rcAutomaticHandoff(t, batch)
		if u16len(handoff) > 20_000 {
			t.Errorf("handoff is %d units, want <= 20000", u16len(handoff))
		}
		rcMatch(t, handoff, `Tool result evidence \(current projected window; may already have been received or handled; not current progress\)`)
		rcMatch(t, handoff, `Tool-call entry assistant-1`)
		rcMatch(t, handoff, `\[result entry result-1\]\n3 tests failed FAILURE DETAIL`)
		rcMatch(t, handoff, `Call arguments: \{"command":"npm test"\}`)
		rcMatch(t, handoff, `(?s)middle omitted.*FAILURE TAIL`)
		rcMatch(t, handoff, `\[error entry result-2\]\nENOENT missing.txt`)
		rcMatch(t, handoff, `\[result entry result-3\]\n\[1 image: image/png\] — recover with history read id result-3`)
		rcNoMatch(t, handoff, `ASSISTANT PROSE|QUJDREVG`)
		if rcIndex(t, handoff, "entry result-1") >= rcIndex(t, handoff, "entry result-2") {
			t.Error("results keep their order")
		}
		if rcIndex(t, handoff, "entry owner") >= rcIndex(t, handoff, "Tool result evidence") {
			t.Error("owner precedes the tool evidence")
		}
		if rcIndex(t, handoff, "entry result-3") >= rcIndex(t, handoff, "older checkpoint") {
			t.Error("tool evidence precedes the older checkpoint")
		}
		rcMatch(t, handoff, `FIRST read obsolete-checkpoint\.md`)

		with := func(extra ...rec) []rec { return append(append([]rec{}, batch...), extra...) }
		for _, stopReason := range []string{"error", "aborted", "length"} {
			interrupted := rcAutomaticHandoff(t, with(
				rcTimed("assistant-interrupted", "6", rec{"role": "assistant", "stopReason": stopReason, "content": "I received these results"}),
			))
			rcMatch(t, interrupted, `Tool result evidence`)
			rcMatch(t, interrupted, `entry result-3`)
			rcNoMatch(t, interrupted, `no model has seen|no model has consumed`)
		}

		truncatedCall := rcAutomaticHandoff(t, with(
			rcTimed("assistant-length", "6", rec{"role": "assistant", "stopReason": "length", "content": []any{
				rcCall("truncated-call", "write", rec{"path": "secret"}),
			}}),
			rcTimed("truncated-result", "7", rec{"role": "toolResult", "toolCallId": "truncated-call", "toolName": "write", "isError": true, "content": "not executed"}),
		))
		rcMatch(t, truncatedCall, `Tool-call entry assistant-length`)
		rcMatch(t, truncatedCall, `\[error entry truncated-result\]\nnot executed`)
		rcNoMatch(t, truncatedCall, `entry result-3`)

		consumed := rcAutomaticHandoff(t, with(
			rcTimed("assistant-2", "6", rec{"role": "assistant", "stopReason": "stop", "content": "All done."}),
		))
		rcNoMatch(t, consumed, `Tool result evidence|FAILURE DETAIL|ENOENT`)
		rcMatch(t, consumed, `run the checks`)

		calls := make([]any, 300)
		resultIDs := make([]string, 300)
		for i := range calls {
			calls[i] = rcCall(fmt.Sprintf("stress-call-%d", i), "read", rec{"path": fmt.Sprintf("%d.txt", i)})
			resultIDs[i] = fmt.Sprintf("00000000-0000-7000-8000-%012d", i)
		}
		stress := []rec{
			{"type": "context_window", "id": "prior", "handoff": rcRepeat("p", 6000)},
			rcTimed("first-owner", "1", rec{"role": "user", "content": rcRepeat("a", 6000)}),
			rcTimed("latest-owner", "2", rec{"role": "user", "content": rcRepeat("b", 6000)}),
			{"type": "custom_message", "id": "latest-input", "timestamp": "3", "customType": "intercom", "content": rcRepeat("c", 6000), "display": true},
			rcTimed("stress-assistant", "4", rec{"role": "assistant", "stopReason": "toolUse", "content": calls}),
		}
		for i, call := range calls {
			stress = append(stress, rcTimed(resultIDs[i], fmt.Sprintf("%d", i+5), rec{
				"role": "toolResult", "toolCallId": call.(rec)["id"], "toolName": call.(rec)["name"],
				"content": fmt.Sprintf("result %d %s", i, rcRepeat("r", 1000)),
			}))
		}
		stressed := rcAutomaticHandoff(t, stress)
		if u16len(stressed) > 20_000 {
			t.Errorf("stressed handoff is %d units, want <= 20000", u16len(stressed))
		}
		for _, id := range resultIDs {
			rcMatch(t, stressed, "entry "+id+`\]`)
		}
	})

	tw(t, rcFile, "retained edited async receipts are excluded by source identity while discarded edits keep history pointers", func(t *testing.T) {
		call := rcMsg("call-entry", rec{"role": "assistant", "stopReason": "toolUse", "content": []any{
			rec{"type": "toolCall", "id": "receipt-call", "name": "work", "async": true, "arguments": rec{}},
		}})
		result := rcMsg("result-entry", rec{"role": "toolResult", "toolCallId": "receipt-call", "toolName": "work", "content": "PRIVATE_ORIGINAL_RECEIPT"})
		edit := rec{"type": "context_edit", "id": "edit-entry", "targetId": "result-entry", "replacement": rec{"content": "APPROVED_RECEIPT"}}
		edited := copyRec(result["message"].(rec))
		edited["content"] = "APPROVED_RECEIPT"
		projected := []projectedEntry{
			{sourceEntry: call, messages: []rec{call["message"].(rec)}},
			{sourceEntry: result, messages: []rec{edited}},
		}
		entries := []rec{call, result, edit}
		discarded := rcBuild(t, entries, projected, 20_000, autoHandoffOptions{})
		rcMatch(t, discarded, `\[result entry edit-entry\]\nAPPROVED_RECEIPT`)
		rcNoMatch(t, discarded, `PRIVATE_ORIGINAL_RECEIPT`)
		retained := rcBuild(t, entries, projected, 20_000, autoHandoffOptions{retainedEntryIds: rcSet("call-entry", "result-entry")})
		rcNoMatch(t, retained, `Tool result evidence|APPROVED_RECEIPT|PRIVATE_ORIGINAL_RECEIPT`)
	})

	tw(t, rcFile, "async receipt recovery shares the handoff header budget across earlier batches", func(t *testing.T) {
		branch := []rec{rcMsg("owner", rec{"role": "user", "content": "Check prior receipts before taking action."})}
		for index := range 600 {
			id := fmt.Sprintf("00000000-0000-7000-8000-%012d", index)
			branch = append(branch,
				rcMsg(fmt.Sprintf("call-%d", index), rec{"role": "assistant", "stopReason": "toolUse", "content": []any{
					rec{"type": "toolCall", "id": id, "name": "work", "async": true, "arguments": rec{"index": float64(index)}},
				}}),
				rcMsg(id, rec{"role": "toolResult", "toolCallId": id, "toolName": "work", "content": fmt.Sprintf("Receipt %d %s", index, rcRepeat("r", 1000))}),
				rcMsg(fmt.Sprintf("response-%d", index), rec{"role": "assistant", "stopReason": "stop", "content": "Complete"}),
			)
		}
		handoff := rcAutomaticHandoff(t, branch)
		if u16len(handoff) > 20_000 {
			t.Errorf("handoff is %d units, want <= 20000", u16len(handoff))
		}
		m := regexp.MustCompile(`Omitted (\d+) earlier tool result\(s\) whose headers could not fit`).FindStringSubmatch(handoff)
		if m == nil {
			t.Fatal("no omission notice")
		}
		var omitted int
		fmt.Sscan(m[1], &omitted)
		if !(omitted > 0 && omitted < 599) {
			t.Errorf("earlier batches remain eligible under the shared header budget, omitted=%d", omitted)
		}
		if got := len(regexp.MustCompile(`\[result entry `).FindAllString(handoff, -1)); got != 600-omitted {
			t.Errorf("result blocks = %d, want %d", got, 600-omitted)
		}
		rcMatch(t, handoff, `entry 00000000-0000-7000-8000-000000000599\]`)
		rcMatch(t, handoff, `history search/read to recover them`)
		rcMatch(t, handoff, `owner input.*entry owner`)
		rcNoMatch(t, handoff, `no model has seen|no model has consumed`)
	})
}
