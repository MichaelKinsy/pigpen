package ask_user_question_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The refusals of the dialog walker, which answers a terminal host that cannot render the questionnaire (these rigs):
// not twins of the original's tests, which have no such case.

const refusalTail = " yet. Install the original extension, npm:@juicesharp/rpiv-ask-user-question@2.11.0, which runs unchanged in PiG."

func refusalHead(feature string) string {
	return "@pi-in-go/pigpen-ask-user-question (Go port) does not support " + feature
}

func writeCollapseKey(t *testing.T, json string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	dir := filepath.Join(home, ".config", "rpiv-ask-user-question")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
}

func refused(t *testing.T, r *rig, args map[string]any) string {
	t.Helper()
	_, failure := r.Host.Tool("ask_user_question", args)
	if failure == "" {
		t.Fatal("the call should have been refused")
	}
	return failure
}

func TestRefusesACustomCollapseKey(t *testing.T) {
	writeCollapseKey(t, `{"collapseKey":"alt+o"}`)
	r := startRig(t, tuiOpts())
	got := refused(t, r, singleArgs())
	want := refusalHead("the collapseKey setting (the collapse key belongs to the interactive questionnaire)") + refusalTail
	if !strings.Contains(got, want) {
		t.Fatalf("got %q, want it to contain %q", got, want)
	}
	eq(t, r.dialogCount(), 0, "no dialog is shown")
	eq(t, channels(r), "rpiv:ask-user:prompt rpiv:ask-user:blocked rpiv:ask-user:blocked", "only the questionnaire attempt emits")
}

func TestKeepsDefaultAndOffCollapseKeys(t *testing.T) {
	for _, cfg := range []string{`{}`, `{"collapseKey":"ctrl+]"}`, `{"collapseKey":"off"}`, `{"collapseKey":"ctr+]"}`, `{"collapseKey":42}`} {
		writeCollapseKey(t, cfg)
		r := startRig(t, tuiOpts(), pick("1. A — a"))
		text, _ := r.ask(singleArgs())
		if !strings.Contains(text, `"Which?"="A"`) {
			t.Fatalf("%s: %q", cfg, text)
		}
	}
}

func longPreview(n int) string { return strings.Repeat("x", n) }

func previewArgs(preview string) map[string]any {
	return askArgs(questionArg("Which?", "Pick", false,
		map[string]any{"label": "A", "description": "a", "preview": preview}, optArg("B", "b")))
}

func TestRefusesAPreviewBeyondTheDialogCut(t *testing.T) {
	writeCollapseKey(t, `{}`)
	r := startRig(t, tuiOpts())
	got := refused(t, r, previewArgs(longPreview(601)))
	want := refusalHead("option previews longer than 600 characters (the side-by-side preview pane; the dialog walker cuts them)") + refusalTail
	if !strings.Contains(got, want) {
		t.Fatalf("got %q, want it to contain %q", got, want)
	}
	eq(t, r.dialogCount(), 0, "no dialog is shown")
	eq(t, channels(r), "rpiv:ask-user:prompt rpiv:ask-user:blocked rpiv:ask-user:blocked", "only the questionnaire attempt emits")
}

func TestPreviewLengthIsCountedInUTF16Units(t *testing.T) {
	writeCollapseKey(t, `{}`)
	// 300 astral characters are 600 units: not refused. 301 are 602: refused.
	r := startRig(t, tuiOpts(), pick("1. A — a"))
	r.ask(previewArgs(strings.Repeat("😀", 300)))
	r2 := startRig(t, tuiOpts())
	refused(t, r2, previewArgs(strings.Repeat("😀", 301)))
	r3 := startRig(t, tuiOpts(), pick("1. A — a"))
	r3.ask(previewArgs(longPreview(600)))
}

func TestRefusalDoesNotMaskNoUIOrValidationErrors(t *testing.T) {
	writeCollapseKey(t, `{"collapseKey":"alt+o"}`)
	r := startRig(t, HostOptions{Mode: "print"})
	text, details := r.ask(singleArgs())
	eq(t, text, "Error: UI not available (running in non-interactive mode)", "text")
	eq(t, details["error"], "no_ui", "error")
	r2 := startRig(t, tuiOpts())
	text, details = r2.ask(askArgs())
	eq(t, details["error"], "no_questions", "error")
	_ = text
}

// channels names the events the questionnaire attempt emitted before the host resolved no component.
func channels(r *rig) string {
	var names []string
	for _, e := range r.emittedEvents() {
		names = append(names, e.Channel)
	}
	return strings.Join(names, " ")
}

func tuiOpts() HostOptions { return HostOptions{Mode: "tui"} }

// The original's RPC path never reads collapseKey and cuts a long preview at 600 characters; the walker does the same.
func TestRPCHostsAreNotRefused(t *testing.T) {
	writeCollapseKey(t, `{"collapseKey":"alt+o"}`)
	r := startRig(t, rpcOpts(), pick("1. A — a"))
	text, _ := r.ask(previewArgs(longPreview(2000)))
	if !strings.Contains(text, `"Which?"="A"`) {
		t.Fatalf("%q", text)
	}
}
