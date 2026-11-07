package posthorse

import "testing"

// rcAutomaticHandoff is the upstream automaticHandoff helper: session_before_compact on the
// fixture host with the default projection and a 100,000-token window.
func rcAutomaticHandoff(t *testing.T, branch []rec) string {
	t.Helper()
	return newFixture(t).AutomaticHandoff(branch)
}

func rcNotesCall(id, op, path, content string) rec {
	return rcCall(id, "notes", rec{"op": op, "path": path, "content": content})
}

func rcNotesResult(id, toolCallID, toolName, text string) rec {
	return rcMsg(id, rec{"role": "toolResult", "toolName": toolName, "toolCallId": toolCallID, "isError": false, "content": text})
}

func TestTwinPosthorseRecoveryNotes(t *testing.T) {
	tw(t, rcFile, "automatic recovery tracks session notes across tools, codemode, context edits, clears, and explicit resets", func(t *testing.T) {
		const durable = "/workspace/.pi/notes/pi-durable-pr.md"
		entries := []rec{
			rcMsg("pre-reset-write", rec{"role": "assistant", "content": []any{rcNotesCall("call-pre", "write", "obsolete.md", "old task")}}),
			{"type": "compaction", "id": "explicit-reset", "summary": "Fresh explicit task window.", "firstKeptEntryId": nil,
				"details": rec{"posthorse": float64(1), "reason": "explicit"}},
			rcTimed("owner-1", "2026-11-01T00:00:00Z", rec{"role": "user", "content": "Ship the durable recovery fix."}),
			rcMsg("codemode-res", rec{
				"role": "toolResult", "toolName": "codemode", "toolCallId": "cm-1", "isError": false, "content": "read note",
				"nestedCalls": rec{"calls": []any{rec{
					"name": "notes", "arguments": rec{"op": "read", "path": "durable-session-recovery.md"},
					"result": "File: /workspace/.pi/notes/durable-session-recovery.md\nprior context",
				}}},
			}),
			rcMsg("compacted-write-call", rec{"role": "assistant", "content": []any{rcNotesCall("nw-1", "write", "pi-durable-pr.md", "# Goal\nShip PR")}}),
			rcNotesResult("compacted-write-res", "nw-1", "notes", "Wrote "+durable),
			rcMsg("temp-calls", rec{"role": "assistant", "content": []any{
				rcNotesCall("nw-temp", "write", "scratch.md", "temp"),
				rcNotesCall("nw-clear", "write", "scratch.md", ""),
			}}),
			rcNotesResult("temp-write", "nw-temp", "notes", "Wrote /workspace/.pi/notes/scratch.md"),
			rcNotesResult("temp-clear", "nw-clear", "notes", "Wrote /workspace/.pi/notes/scratch.md"),
			rcNotesResult("omitted-write", "nw-omit", "notes", "Wrote /workspace/.pi/notes/redacted.md"),
			{"type": "context_edit", "id": "edit-omit", "targetId": "omitted-write", "replacement": nil},
			rcMsg("failed-edit-call", rec{"role": "assistant", "content": []any{
				rcCall("rt-fail", "replace_text", rec{"files": []any{rec{"path": "/workspace/.pi/notes/failed-note.md", "edits": []any{}}}}),
				rcCall("rt-preview", "replace_text", rec{"preview": true, "files": []any{rec{"path": "/workspace/.pi/notes/preview-only.md", "edits": []any{}}}}),
			}}),
			rcMsg("failed-edit-res", rec{"role": "toolResult", "toolName": "replace_text", "toolCallId": "rt-fail", "isError": true, "content": "Anchor not found"}),
			rcMsg("retained-partial-call", rec{"role": "assistant", "content": []any{
				rcCall("rt-ok", "replace_text", rec{"files": []any{rec{"path": durable, "edits": []any{}}}}),
			}}),
			rcMsg("retained-partial-res", rec{"role": "toolResult", "toolName": "replace_text", "toolCallId": "rt-ok", "isError": false, "content": "Updated 1 file"}),
		}
		var projected []projectedEntry
		for _, entry := range entries {
			if entry["type"] == "message" && entry["id"] != "omitted-write" {
				projected = append(projected, projectedEntry{sourceEntry: entry, messages: []rec{entry["message"].(rec)}})
			}
		}

		// Case 1: only a partial edit (replace_text) is in the retained tail; the full write was compacted.
		partialRetained := rcBuild(t, entries, projected, 20_000, autoHandoffOptions{
			retainedEntryIds: rcSet("retained-partial-call", "retained-partial-res"),
		})
		rcNoMatch(t, partialRetained, `obsolete\.md|scratch\.md|redacted\.md|failed-note\.md|preview-only\.md`)
		rcMatch(t, partialRetained, `Context rolled over after entry retained-partial-res\. Earlier turns before entry retained-partial-call were compacted into session history; entries retained-partial-call through retained-partial-res above are the carried-over recent tail`)
		rcMatch(t, partialRetained, `- Active note \(written\): pi-durable-pr\.md \(/workspace/\.pi/notes/pi-durable-pr\.md\) \[only partial edits are in the retained tail above at entry retained-partial-call — read full note now\]`)
		rcMatch(t, partialRetained, `- Referenced note \(read\): durable-session-recovery\.md \(/workspace/\.pi/notes/durable-session-recovery\.md\) \[compacted before entry retained-partial-call\]`)
		rcMatch(t, partialRetained, `Read pi-durable-pr\.md \(notes read or read on its path\) before continuing`)

		// Case 2: the full write itself is inside the retained tail.
		fullRetained := rcBuild(t, entries, projected, 20_000, autoHandoffOptions{
			retainedEntryIds: rcSet("compacted-write-call", "compacted-write-res", "retained-partial-call", "retained-partial-res"),
		})
		rcMatch(t, fullRetained, `- Active note \(written\): pi-durable-pr\.md \(/workspace/\.pi/notes/pi-durable-pr\.md\) \[latest full write is in the retained tail above at entry compacted-write-call, updated at entry retained-partial-call\]`)
		rcMatch(t, fullRetained, `Your latest full write to pi-durable-pr\.md is already visible above at entry compacted-write-call; do not rewrite it unless new findings after entry retained-partial-res require it\.`)

		replacement := []rec{
			rcMsg("replacement-call", rec{"role": "assistant", "content": []any{rcNotesCall("replacement", "write", "pi-durable-pr.md", "New state")}}),
			rcNotesResult("replacement-receipt", "replacement", "notes", "Wrote "+durable),
		}
		renderFollowing := func(following []rec) string {
			branch := append(append([]rec{}, entries...), following...)
			var proj []projectedEntry
			for _, entry := range branch {
				if message, ok := entry["message"].(rec); ok {
					proj = append(proj, projectedEntry{sourceEntry: entry, messages: []rec{message}})
				}
			}
			ids := []string{"compacted-write-call", "compacted-write-res", "retained-partial-call", "retained-partial-res"}
			for _, entry := range following {
				id, _ := entry["id"].(string)
				ids = append(ids, id)
			}
			return rcBuild(t, branch, proj, 20_000, autoHandoffOptions{retainedEntryIds: rcSet(ids...)})
		}
		replaced := renderFollowing(replacement)
		rcMatch(t, replaced, `latest full write is in the retained tail above at entry replacement-call\]`)
		rcNoMatch(t, replaced, `updated at entry retained-partial-call`)
		observed := rcMsg("observed-receipt", rec{
			"role": "toolResult", "toolName": "codemode", "toolCallId": "observed", "isError": false,
			"content": "Saved a newer checkpoint.",
			"nestedCalls": rec{"calls": []any{rec{
				"name": "notes", "arguments": rec{"op": "write", "path": "pi-durable-pr.md", "content": "Newer state"},
				"result": "Wrote " + durable,
			}}},
		})
		effectOnly := renderFollowing(append(append([]rec{}, replacement...), observed))
		rcMatch(t, effectOnly, `pi-durable-pr\.md.*verified effect at entry observed-receipt.*read this note now`)
		rcNoMatch(t, effectOnly, `latest full write is in the retained tail`)
		restored := renderFollowing(append([]rec{observed}, replacement...))
		rcMatch(t, restored, `latest full write is in the retained tail above at entry replacement-call\]`)
		rcNoMatch(t, restored, `verified effect at entry observed-receipt`)
	})

	tw(t, rcFile, "automatic recovery keeps verified paths from a partially failed editing receipt", func(t *testing.T) {
		const source = "/workspace/.pi/notes/source.md"
		const destination = "/workspace/.pi/notes/copied.md"
		patch := "*** Begin Patch\n*** Update File: " + source + "\n*** Move to: " + destination +
			"\n@@\n-old\n+new\n*** Delete File: /workspace/.pi/notes/unattempted.md\n*** End Patch"
		entries := []rec{
			rcMsg("move-call", rec{"role": "assistant", "content": []any{rcCall("move", "apply_patch", rec{"input": patch})}}),
			rcMsg("move-receipt", rec{
				"role": "toolResult", "toolName": "apply_patch", "toolCallId": "move", "isError": true,
				"content": "The destination committed but the move did not finish.",
				"details": rec{
					"modifiedFiles": []any{destination},
					"files": []any{
						rec{"path": source, "moveTo": destination, "operation": "move", "status": "failed"},
						rec{"path": "/workspace/.pi/notes/unattempted.md", "operation": "delete", "status": "unattempted"},
						rec{"path": "/workspace/.pi/notes/uncertain.md", "operation": "edit", "status": "uncertain"},
					},
				},
			}),
		}
		projected := make([]projectedEntry, len(entries))
		for i, entry := range entries {
			projected[i] = projectedEntry{sourceEntry: entry, messages: []rec{entry["message"].(rec)}}
		}
		handoff := rcBuild(t, entries, projected, 20_000, autoHandoffOptions{})
		rcMatch(t, handoff, `Active note \(written\): copied\.md`)
		rcNoMatch(t, handoff, `(?m)^- (?:Active|Referenced) note.*(?:source\.md|unattempted\.md|uncertain\.md)`)
	})
}
