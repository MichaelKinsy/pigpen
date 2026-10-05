package rpiv_todo

import "testing"

const fEnv = "tool/response-envelope"

func TestFormatContent(t *testing.T) {
	tw(t, fEnv, "create — 'Created #id: subject (pending)'", func(t *testing.T) {
		eq(t, formatContent(op{Kind: "create", TaskID: 1}, stateWith(tk(1, "alpha"))), "Created #1: alpha (pending)", "text")
	})
	tw(t, fEnv, "update — emits transition tuple when statuses differ", func(t *testing.T) {
		s := stateWith(tk(1, "x", withStatus(statusInProgress)))
		eq(t, formatContent(op{Kind: "update", ID: 1, FromStatus: "pending", ToStatus: "in_progress", Changed: true}, s), "Updated #1 (pending → in_progress)", "text")
	})
	tw(t, fEnv, "update — omits transition when from === to but fields changed (e.g. blockedBy-only update)", func(t *testing.T) {
		eq(t, formatContent(op{Kind: "update", ID: 1, FromStatus: "pending", ToStatus: "pending", Changed: true}, stateWith(tk(1, "x"))), "Updated #1", "text")
	})
	tw(t, fEnv, "update — reports 'No change' when changed is false (no-effect update)", func(t *testing.T) {
		eq(t, formatContent(op{Kind: "update", ID: 1, FromStatus: "pending", ToStatus: "pending", Changed: false}, stateWith(tk(1, "x"))),
			"No change: #1 already matches the requested values (status: pending)", "text")
	})
	tw(t, fEnv, "delete — 'Deleted #id: subject'", func(t *testing.T) {
		eq(t, formatContent(op{Kind: "delete", ID: 1, Subject: "ship"}, stateWith(tk(1, "ship", withStatus(statusDeleted)))), "Deleted #1: ship", "text")
	})
	tw(t, fEnv, "clear — emits prior count", func(t *testing.T) {
		eq(t, formatContent(op{Kind: "clear", Count: 4}, stateWith()), "Cleared 4 tasks", "text")
	})
	tw(t, fEnv, "list — 'No tasks' when filtered view is empty", func(t *testing.T) {
		eq(t, formatContent(op{Kind: "list"}, stateWith(tk(1, "x", withStatus(statusDeleted)))), "No tasks", "text")
	})
	tw(t, fEnv, "list — joins per-task '[status] #id subject' lines", func(t *testing.T) {
		s := stateWith(tk(1, "a"), tk(2, "b", withStatus(statusInProgress), withActiveForm("Building")))
		eq(t, formatContent(op{Kind: "list"}, s), "[pending] #1 a\n[in_progress] #2 b (Building)", "text")
	})
	tw(t, fEnv, "get — multi-line task block with description/blockedBy/owner", func(t *testing.T) {
		s := stateWith(tk(1, "root"), tk(2, "leaf", withDescription("details"), withBlockedBy(1), withOwner("Sergii")))
		eq(t, formatContent(op{Kind: "get", Task: at(s.Tasks, 1)}, s), "#2 [pending] leaf\n  description: details\n  blockedBy: #1\n  owner: Sergii", "text")
	})
	tw(t, fEnv, "get — emits 'blocks: #id,…' reverse-edge line when other tasks block on it", func(t *testing.T) {
		s := stateWith(tk(1, "ship", withBlockedBy(2, 3)), tk(2, "test"), tk(3, "lint"))
		eq(t, formatContent(op{Kind: "get", Task: at(s.Tasks, 1)}, s), "#2 [pending] test\n  blocks: #1", "text")
	})
	tw(t, fEnv, "get — emits activeForm line for in_progress task", func(t *testing.T) {
		s := stateWith(tk(1, "build", withStatus(statusInProgress), withActiveForm("Building")))
		eq(t, formatContent(op{Kind: "get", Task: at(s.Tasks, 0)}, s), "#1 [in_progress] build\n  activeForm: Building", "text")
	})
	tw(t, fEnv, "list — statusFilter narrows to a single status", func(t *testing.T) {
		s := stateWith(tk(1, "a", withStatus(statusPending)), tk(2, "b", withStatus(statusInProgress), withActiveForm("Working")), tk(3, "c", withStatus(statusCompleted)))
		eq(t, formatContent(op{Kind: "list", StatusFilter: "in_progress"}, s), "[in_progress] #2 b (Working)", "text")
	})
	tw(t, fEnv, "list — includeDeleted=true surfaces tombstoned rows", func(t *testing.T) {
		eq(t, formatContent(op{Kind: "list", IncludeDeleted: true}, stateWith(tk(1, "x", withStatus(statusDeleted)))), "[deleted] #1 x", "text")
	})
	tw(t, fEnv, "list — '⛓ #id,…' suffix appears when task has blockedBy", func(t *testing.T) {
		s := stateWith(tk(1, "leaf"), tk(2, "task", withBlockedBy(1)))
		eq(t, formatContent(op{Kind: "list"}, s), "[pending] #1 leaf\n[pending] #2 task ⛓ #1", "text")
	})
	tw(t, fEnv, "create — defensive fallback when op.taskId is unknown to state", func(t *testing.T) {
		eq(t, formatContent(op{Kind: "create", TaskID: 999}, stateWith()), "Created #999", "text")
	})
	tw(t, fEnv, "error — 'Error: <message>'", func(t *testing.T) {
		eq(t, formatContent(errOp("subject required for create"), stateWith()), "Error: subject required for create", "text")
	})
}

func TestBuildToolResult(t *testing.T) {
	tw(t, fEnv, "envelope.details mirrors the canonical TaskDetails shape on success", func(t *testing.T) {
		s := stateWith(tk(1, "alpha"))
		env := buildToolResult("create", params{"subject": "alpha"}, s, op{Kind: "create", TaskID: 1})
		eq(t, env, toolEnvelope{Text: "Created #1: alpha (pending)", Details: taskDetails{Action: "create", Params: params{"subject": "alpha"}, Tasks: s.Tasks, NextID: s.NextID}}, "envelope")
	})
	tw(t, fEnv, "envelope.details carries error message on op.kind === 'error'", func(t *testing.T) {
		env := buildToolResult("create", params{"subject": ""}, stateWith(), errOp("subject required for create"))
		eq(t, env.Details.Error, "subject required for create", "details.error")
		eq(t, env.Text, "Error: subject required for create", "text")
	})
}

func TestFormatContentControlCharacters(t *testing.T) {
	tw(t, fEnv, "get — strips escape sequences from subject, description, and owner", func(t *testing.T) {
		s := stateWith(tk(1, "safe\u001b[2J\u001b[Hsubject", withDescription("line1\nline2"), withOwner("who\u009b31mami")))
		eq(t, formatContent(op{Kind: "get", Task: at(s.Tasks, 0)}, s), "#1 [pending] safesubject\n  description: line1 line2\n  owner: whoami", "text")
	})
	tw(t, fEnv, "create/list — strips escape sequences from the echoed subject and activeForm", func(t *testing.T) {
		s := stateWith(tk(1, "evil\u001b[31m", withStatus(statusInProgress), withActiveForm("clear\u001b[2Jing")))
		eq(t, formatContent(op{Kind: "create", TaskID: 1}, s), "Created #1: evil (pending)", "create")
		eq(t, formatContent(op{Kind: "list"}, s), "[in_progress] #1 evil (clearing)", "list")
	})
}

func TestToolResultDetailsJSON(t *testing.T) {
	// The persisted snapshot keeps the original's field names and drops absent optionals;
	// replay depends on this exact shape (tool/types.ts:41-55).
	env := buildToolResult("create", params{"action": "create", "subject": "a"}, stateWith(tk(1, "a", withDescription("d"), withBlockedBy(2))), op{Kind: "create", TaskID: 1})
	got, err := marshalDetails(env.Details)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, string(got), `{"action":"create","params":{"action":"create","subject":"a"},"tasks":[{"id":1,"subject":"a","description":"d","status":"pending","blockedBy":[2]}],"nextId":2}`, "details JSON")
	empty, _ := marshalDetails(buildToolResult("clear", params{"action": "clear"}, emptyState(), op{Kind: "clear"}).Details)
	eq(t, string(empty), `{"action":"clear","params":{"action":"clear"},"tasks":[],"nextId":1}`, "empty list is an array")
}

const fSan = "tool/sanitize"

func TestSanitizeTerminalText(t *testing.T) {
	tw(t, fSan, "drops complete ANSI/C1 escape sequences without printable remnants", func(t *testing.T) {
		eq(t, sanitizeTerminalText("safe\u001b[31mred\u001b[0m\u009b2J"), "safered", "text")
	})
	tw(t, fSan, "drops OSC sequences including their payload", func(t *testing.T) {
		eq(t, sanitizeTerminalText("a\u001b]0;evil title\u0007b\u001b]8;;http://x\u001b\\c"), "abc", "text")
	})
	tw(t, fSan, "keeps task fields on one terminal line", func(t *testing.T) {
		eq(t, sanitizeTerminalText("one\ntwo\tthree\r"), "one two three ", "controls")
		eq(t, sanitizeTerminalText("a\u2028b\u2029c"), "a b c", "separators")
	})
	tw(t, fSan, "removes bare control characters and bidi overrides", func(t *testing.T) {
		eq(t, sanitizeTerminalText("a\u0007b\u007fc\u202egfedcba\u202c"), "abcgfedcba", "text")
	})
}

// JavaScript's `.` does not match a line terminator (\n, \r, U+2028, U+2029), so the original's
// `/\u001b./g` leaves an ESC that precedes one; the control and separator passes then drop the ESC and
// turn the terminator into a space. Node: sanitizeTerminalText("a\u001b\nb") === "a b".
func TestSanitizeEscapeBeforeALineTerminator(t *testing.T) {
	for in, want := range map[string]string{
		"a\u001b\nb":     "a b",
		"a\u001b\rb":     "a b",
		"a\u001b\u2028b": "a b",
		"a\u001b\u2029b": "a b",
		"a\u001bxb":      "ab",
	} {
		eq(t, sanitizeTerminalText(in), want, "sanitize "+in)
	}
}
