package ask_user_question_test

import "testing"

const fExec = "ask-user-question.execute"

func TestExecute(t *testing.T) {
	tw(t, fExec, "returns cancelled result + ERROR_NO_UI when !hasUI", func(t *testing.T) {
		r := startRig(t, HostOptions{Mode: "print"})
		text, details := r.ask(singleArgs())
		eq(t, text, "Error: UI not available (running in non-interactive mode)", "text")
		eq(t, details, map[string]any{"answers": []any{}, "cancelled": true, "error": "no_ui"}, "details")
		eq(t, r.dialogCount(), 0, "dialogs")
	})
	tskip(t, fExec, "writes exactly one BEL after blocked=true and immediately before the TUI wait", "G10: no terminal write from a subprocess extension")
	tskip(t, fExec, "does not write BEL for non-TTY, no-UI, or invalid requests", "G10: the port never writes a BEL")
	tskip(t, fExec, "swallows a synchronous write failure and still clears blocked state", "G10: there is no write to fail; the blocked lifecycle is TestBlockedStateClearsAfterADialogFailure")
	tskip(t, fExec, "swallows a failing TTY lookup and still opens the questionnaire", "G10: the port does not look the TTY up")
	tw(t, fExec, "registers a typebox schema with a top-level questions array", func(t *testing.T) {
		r := startRig(t, rpcOpts())
		var s map[string]any
		mustJSON(t, r.ToolDef("ask_user_question").Parameters, &s)
		eq(t, s["type"], "object", "type")
		eq(t, s["required"], []any{"questions"}, "required")
		eq(t, s["properties"].(map[string]any)["questions"].(map[string]any)["type"], "array", "questions")
	})
	tw(t, fExec, "emits ASK_USER_PROMPT event via pi.events.emit before showing dialog", func(t *testing.T) {
		r := startRig(t, rpcOpts(), pick("1. A — a"))
		r.ask(singleArgs())
		ev := r.emittedEvents()
		eq(t, at(ev, 0).Channel, "rpiv:ask-user:prompt", "first event")
		eq(t, r.dialogCount(), 1, "dialogs")
	})
	tw(t, fExec, "clears ask-user blocked lifecycle after cancellation and UI rejection", func(t *testing.T) {
		r := startRig(t, rpcOpts(), dismiss())
		r.ask(singleArgs())
		eq(t, blockedLifecycle(r), []any{true, false}, "after cancellation")
		r2 := startRig(t, rpcOpts(), pick("1. A — a"))
		r2.mu.Lock()
		r2.failUI = true
		r2.mu.Unlock()
		if _, failure := r2.Host.Tool("ask_user_question", singleArgs()); failure == "" {
			t.Fatal("a failing dialog should fail the call")
		}
		eq(t, blockedLifecycle(r2), []any{true, false}, "after a dialog failure")
	})
	tw(t, fExec, "does NOT emit event when UI is unavailable", func(t *testing.T) {
		r := startRig(t, HostOptions{Mode: "print"})
		r.ask(singleArgs())
		eq(t, len(r.emittedEvents()), 0, "events")
	})
	tw(t, fExec, "does NOT emit event when validation fails", func(t *testing.T) {
		r := startRig(t, rpcOpts())
		text, details := r.ask(askArgs(questionArg("Q?", "H", false, optArg("Other", "d"), optArg("x", "d"))))
		eq(t, text, "Error: Option label is reserved (Other, Type something., Next)", "text")
		eq(t, details, map[string]any{"answers": []any{}, "cancelled": true, "error": "reserved_label"}, "details")
		eq(t, len(r.emittedEvents()), 0, "events")
		eq(t, r.dialogCount(), 0, "dialogs")
	})
	tw(t, fExec, "emits payload with all questions in order when multiple questions", func(t *testing.T) {
		r := startRig(t, rpcOpts(), pick("1. A — a"), pick("1,2"))
		args := askArgs(
			questionArg("Q1?", "One", false, map[string]any{"label": "A", "description": "a", "preview": "P"}, optArg("B", "b")),
			questionArg("Q2?", "Two", true, optArg("x", "xx"), optArg("y", "yy")))
		r.ask(args)
		eq(t, at(r.emittedEvents(), 0).Payload, map[string]any{"questions": []any{
			map[string]any{"question": "Q1?", "header": "One", "multiSelect": false, "options": []any{
				map[string]any{"label": "A", "description": "a", "hasPreview": true},
				map[string]any{"label": "B", "description": "b", "hasPreview": false}}},
			map[string]any{"question": "Q2?", "header": "Two", "multiSelect": true, "options": []any{
				map[string]any{"label": "x", "description": "xx", "hasPreview": false},
				map[string]any{"label": "y", "description": "yy", "hasPreview": false}}},
		}}, "payload")
	})
}

func TestToolIsLabelled(t *testing.T) {
	eq(t, startRig(t, rpcOpts()).ToolDef("ask_user_question").Label, "Ask User Question", "label")
}

func TestBlockedStateClearsAfterADialogFailure(t *testing.T) {
	r := startRig(t, rpcOpts())
	r.mu.Lock()
	r.failUI = true
	r.mu.Unlock()
	r.Host.Tool("ask_user_question", singleArgs())
	eq(t, blockedLifecycle(r), []any{true, false}, "lifecycle")
}

func TestValidationEnvelopesThroughTheTool(t *testing.T) {
	cases := map[string]map[string]any{
		"no_questions":           askArgs(),
		"too_many_questions":     askArgs(q2("a"), q2("b"), q2("c"), q2("d"), q2("e")),
		"empty_options":          askArgs(questionArg("Q?", "H", false, optArg("only", "d"))),
		"duplicate_question":     askArgs(q2("same"), q2("same")),
		"duplicate_option_label": askArgs(questionArg("Q?", "H", false, optArg("a", "1"), optArg("a", "2"))),
		"reserved_label":         askArgs(questionArg("Q?", "H", true, optArg("Type something.", "d"), optArg("x", "d"))),
	}
	for code, args := range cases {
		r := startRig(t, rpcOpts())
		text, details := r.ask(args)
		eq(t, details, map[string]any{"answers": []any{}, "cancelled": true, "error": code}, code)
		if head(text, 7) != "Error: " {
			t.Fatalf("%s: text %q", code, text)
		}
	}
}

func q2(text string) any { return questionArg(text, "H", false, optArg("a", "1"), optArg("b", "2")) }

func blockedLifecycle(r *rig) []any {
	var out []any
	for _, e := range r.emittedEvents() {
		if e.Channel == "rpiv:ask-user:blocked" {
			out = append(out, e.Payload["active"])
		}
	}
	return out
}
