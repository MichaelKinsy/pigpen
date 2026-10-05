package ask_user_question_test

import (
	"strings"
	"testing"
)

const fRPC = "rpc-fallback"

func TestRPCDialogWalker(t *testing.T) {
	tskip(t, fRPC, "requires both select and input to be functions", "the Go Context always provides Select and Input; there is no host without them to detect")
	tw(t, fRPC, "single-select uses ctx.ui.select and returns the chosen label in the envelope", func(t *testing.T) {
		r := startRig(t, rpcOpts(), pick("2. B — b"))
		text, details := r.ask(singleArgs())
		eq(t, r.dialogCount(), 1, "dialogs")
		eq(t, r.dialogAt(0).Method, "ui.select", "method")
		eq(t, text, `User has answered your questions: "Which?"="B". You can now continue with the user's answers in mind.`, "text")
		eq(t, details["cancelled"], false, "cancelled")
	})
	tw(t, fRPC, "does NOT call ctx.ui.custom in RPC mode", func(t *testing.T) {
		r := startRig(t, rpcOpts(), pick("1. A — a"))
		r.ask(singleArgs())
		for _, m := range r.hostMethods() {
			if m == "ui.custom" {
				t.Fatalf("ui.custom was called: %v", r.hostMethods())
			}
		}
	})
	tw(t, fRPC, "emits rpiv:ask-user:blocked around the RPC dialog walker and clears it afterward", func(t *testing.T) {
		r := startRig(t, rpcOpts(), pick("1. A — a"))
		r.ask(singleArgs())
		var blocked []any
		for _, e := range r.emittedEvents() {
			if e.Channel == "rpiv:ask-user:blocked" {
				blocked = append(blocked, e.Payload["active"])
			}
		}
		eq(t, blocked, []any{true, false}, "blocked lifecycle")
	})
	tskip(t, fRPC, "writes exactly one BEL after blocked=true and before the first RPC dialog",
		"G10: an extension subprocess cannot write to the terminal (its stdout is the protocol pipe) and the Go SDK has no bell call; Pi's RPC stdout is not a TTY, so the equivalence traces never contain a BEL")
	tskip(t, fRPC, "does not write BEL when RPC stdout is not a TTY", "same as above (G10): the port writes no BEL at all, which is the original's behaviour whenever stdout is not a TTY")
	tw(t, fRPC, "appends the 'Type something.' sentinel row sourced from ROW_INTENT_META", func(t *testing.T) {
		r := startRig(t, rpcOpts(), pick("1. A — a"))
		r.ask(singleArgs())
		eq(t, r.dialogAt(0).Options, []string{"1. A — a", "2. B — b", "3. Type something."}, "options")
		eq(t, r.dialogAt(0).Title, "[Pick] Which?", "title")
	})
	tw(t, fRPC, "'Type something.' sentinel follows up with ctx.ui.input for custom text", func(t *testing.T) {
		r := startRig(t, rpcOpts(), pick("3. Type something."), pick("my own"))
		text, details := r.ask(singleArgs())
		eq(t, r.dialogAt(1).Method, "ui.input", "second dialog")
		eq(t, r.dialogAt(1).Title, "[Pick] Which?\n\nType your answer:", "input title")
		eq(t, r.dialogAt(1).Placeholder, "", "placeholder")
		eq(t, text, `User has answered your questions: "Which?"="my own". You can now continue with the user's answers in mind.`, "text")
		eq(t, answerAt(details, 0)["kind"], "custom", "kind")
	})
	tw(t, fRPC, "folds option previews into the select title and echoes the selected preview", func(t *testing.T) {
		r := startRig(t, rpcOpts(), pick("1. A — a"))
		args := askArgs(questionArg("Which?", "Pick", false,
			map[string]any{"label": "A", "description": "a", "preview": "PREVIEW-A"}, optArg("B", "b")))
		text, _ := r.ask(args)
		eq(t, r.dialogAt(0).Title, "[Pick] Which?\n\n--- 1. A preview ---\nPREVIEW-A", "title")
		if !strings.Contains(text, "selected preview: PREVIEW-A") {
			t.Fatalf("preview not echoed: %q", text)
		}
	})
	tw(t, fRPC, "multi-select uses ctx.ui.input and parses comma-separated indices into labels", func(t *testing.T) {
		r := startRig(t, rpcOpts(), pick("1, 3"))
		text, details := r.ask(multiArgs())
		d := r.dialogAt(0)
		eq(t, d.Method, "ui.input", "method")
		eq(t, d.Placeholder, "1,3", "placeholder")
		eq(t, d.Title, "[Colors] Pick colors?\n\n1. red — r\n2. green — g\n3. blue — b\n\nEnter the numbers of all that apply, comma-separated (e.g. \"1,3\"), or type a custom answer as plain text.", "title")
		eq(t, text, `User has answered your questions: "Pick colors?"="red, blue". You can now continue with the user's answers in mind.`, "text")
		eq(t, answerAt(details, 0)["selected"], []any{"red", "blue"}, "selected")
	})
	tw(t, fRPC, "multi-select treats non-index input as a typed custom answer, not a silent drop", func(t *testing.T) {
		r := startRig(t, rpcOpts(), pick("red, something else entirely"))
		text, details := r.ask(multiArgs())
		eq(t, text, `User has answered your questions: "Pick colors?"="red, something else entirely". You can now continue with the user's answers in mind.`, "text")
		eq(t, details["cancelled"], false, "cancelled")
		eq(t, answerAt(details, 0)["kind"], "custom", "kind")
		r = startRig(t, rpcOpts(), pick("  purple please  "))
		text, _ = r.ask(multiArgs())
		eq(t, text, `User has answered your questions: "Pick colors?"="purple please". You can now continue with the user's answers in mind.`, "trimmed text")
	})
	tw(t, fRPC, "multi-select empty input commits an empty selection (Next with nothing toggled)", func(t *testing.T) {
		r := startRig(t, rpcOpts(), pick("   "))
		text, details := r.ask(multiArgs())
		a := answerAt(details, 0)
		eq(t, a["selected"], []any{}, "selected")
		eq(t, a["answer"], nil, "answer")
		eq(t, text, `User has answered your questions: "Pick colors?"="(no input)". You can now continue with the user's answers in mind.`, "text")
	})
	tw(t, fRPC, "dismiss (select resolves undefined) → decline envelope", func(t *testing.T) {
		r := startRig(t, rpcOpts(), dismiss())
		text, details := r.ask(singleArgs())
		eq(t, text, "User declined to answer questions", "text")
		eq(t, details["cancelled"], true, "cancelled")
	})
	tw(t, fRPC, "walks multiple questions sequentially, one dialog each", func(t *testing.T) {
		// The original's params: the single-select question, then the multi-select one.
		r := startRig(t, rpcOpts(), pick("1. A — a"), pick("2"))
		args := askArgs(questionArg("Which?", "Pick", false, optArg("A", "a"), optArg("B", "b")),
			questionArg("Pick colors?", "Colors", true, optArg("red", "r"), optArg("green", "g"), optArg("blue", "b")))
		text, _ := r.ask(args)
		eq(t, r.dialogCount(), 2, "dialogs")
		eq(t, r.dialogAt(0).Method, "ui.select", "first dialog")
		eq(t, r.dialogAt(1).Method, "ui.input", "second dialog")
		eq(t, text, `User has answered your questions: "Which?"="A". "Pick colors?"="green". You can now continue with the user's answers in mind.`, "text")
		r = startRig(t, rpcOpts(), pick("1. A — a"), pick("2. y — y"))
		r.ask(askArgs(questionArg("Q1?", "One", false, optArg("A", "a"), optArg("B", "b")), questionArg("Q2?", "Two", false, optArg("x", "x"), optArg("y", "y"))))
		eq(t, r.dialogAt(1).Title, "[Two] Q2?", "second title")
	})
	tskip(t, fRPC, "falls back to the dialog walker when ctx.mode is unset and custom() resolves undefined",
		"the Go SDK always reports a mode ('print' when the host sends none), so the host-predates-ctx.mode path cannot occur; the undefined-custom backstop belongs to the custom() path, slice B, not ported in this revision")
	tskip(t, fRPC, "stays on the custom() path when ctx.mode is unset and custom() renders (TUI)", "same: no unset mode in Go; the TUI path is slice B")
}

func TestRPCHeaderlessQuestionHasNoPrefix(t *testing.T) {
	r := startRig(t, rpcOpts(), pick("1. A — a"))
	r.ask(askArgs(questionArg("Which?", "", false, optArg("A", "a"), optArg("B", "b"))))
	eq(t, r.dialogAt(0).Title, "Which?", "title")
}

func TestRPCPreviewIsCutAtSixHundredCharacters(t *testing.T) {
	r := startRig(t, rpcOpts(), pick("1. A — a"))
	long := strings.Repeat("x", 700)
	r.ask(askArgs(questionArg("Which?", "H", false, map[string]any{"label": "A", "description": "a", "preview": long}, optArg("B", "b"))))
	eq(t, r.dialogAt(0).Title, "[H] Which?\n\n--- 1. A preview ---\n"+strings.Repeat("x", 600), "title")
}

func TestRPCMultiSelectIndexParsing(t *testing.T) {
	for in, want := range map[string]any{
		"3, 1, 3.": []any{"blue", "red"},
		"2 2":      []any{"green"},
		"1,9":      "1,9", // out of range: custom text
		"0":        "0",
		"1.":       []any{"red"},
		"1a":       "1a",
	} {
		r := startRig(t, rpcOpts(), pick(in))
		_, details := r.ask(multiArgs())
		a := answerAt(details, 0)
		if sel, ok := want.([]any); ok {
			eq(t, a["selected"], sel, "input "+in)
		} else {
			eq(t, a["answer"], want, "input "+in)
			eq(t, a["kind"], "custom", "kind for "+in)
		}
	}
}

func TestRPCDismissedInputsDecline(t *testing.T) {
	r := startRig(t, rpcOpts(), dismiss())
	_, details := r.ask(multiArgs())
	eq(t, details["cancelled"], true, "multi input dismissed")
	r = startRig(t, rpcOpts(), pick("3. Type something."), dismiss())
	_, details = r.ask(singleArgs())
	eq(t, details["cancelled"], true, "custom input dismissed")
	r = startRig(t, rpcOpts(), pick("Postgres"))
	_, details = r.ask(singleArgs())
	eq(t, details["cancelled"], true, "a select answer that is not an option line")
}

func TestRPCPartialAnswersStayInTheDetails(t *testing.T) {
	r := startRig(t, rpcOpts(), pick("1. A — a"), dismiss())
	args := askArgs(questionArg("Q1?", "One", false, optArg("A", "a"), optArg("B", "b")), questionArg("Q2?", "Two", false, optArg("x", "x"), optArg("y", "y")))
	text, details := r.ask(args)
	eq(t, text, "User declined to answer questions", "text")
	eq(t, len(answersOf(details)), 1, "answers kept")
}

func TestRPCSelectAnswerParsedLikeParseInt(t *testing.T) {
	// JavaScript's parseInt: leading white space and a sign are accepted; the rest of the line is ignored.
	for in, label := range map[string]string{" 2. B — b": "B", "+1. A — a": "A", "2": "B", "1abc": "A"} {
		r := startRig(t, rpcOpts(), pick(in))
		text, _ := r.ask(singleArgs())
		eq(t, text, `User has answered your questions: "Which?"="`+label+`". You can now continue with the user's answers in mind.`, "answer "+in)
	}
	for _, in := range []string{"-1. A — a", "0. A", "4. x", "99999999999999999999. x", "A", ""} {
		r := startRig(t, rpcOpts(), pick(in))
		text, _ := r.ask(singleArgs())
		eq(t, text, "User declined to answer questions", "declined "+in)
	}
}

func TestRPCCustomAnswerIsNotTrimmed(t *testing.T) {
	r := startRig(t, rpcOpts(), pick("3. Type something."), pick("  padded  "))
	_, details := r.ask(singleArgs())
	eq(t, answerAt(details, 0)["answer"], "  padded  ", "answer")
}

func TestRPCPreviewIsCutInUTF16Units(t *testing.T) {
	// 598 units + one astral character (two units) is exactly 600: all of it stays, the rest is cut.
	preview := strings.Repeat("x", 598) + "😀" + "tail"
	r := startRig(t, rpcOpts(), pick("1. A — a"))
	r.ask(askArgs(questionArg("Which?", "H", false, map[string]any{"label": "A", "description": "a", "preview": preview}, optArg("B", "b"))))
	eq(t, r.dialogAt(0).Title, "[H] Which?\n\n--- 1. A preview ---\n"+strings.Repeat("x", 598)+"😀", "title")
}

func TestRPCPreviewCutNeverSplitsAnAstralCharacter(t *testing.T) {
	// 599 units + one astral character: JavaScript's slice(0, 600) keeps 599 units and the lone high surrogate,
	// which a Go (UTF-8) string cannot hold; the port stops before the character, one unit short.
	preview := strings.Repeat("x", 599) + "😀" + "tail"
	r := startRig(t, rpcOpts(), pick("1. A — a"))
	r.ask(askArgs(questionArg("Which?", "H", false, map[string]any{"label": "A", "description": "a", "preview": preview}, optArg("B", "b"))))
	eq(t, r.dialogAt(0).Title, "[H] Which?\n\n--- 1. A preview ---\n"+strings.Repeat("x", 599), "title")
}
