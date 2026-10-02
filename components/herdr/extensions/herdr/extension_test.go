package herdr_test

// Ported case for case from components/herdr/tests/herdr.test.ts (35 tests, at
// 6685dcd): same inputs, same expectations, driven through the real Go SDK.
//
// One TypeScript feature has no Go equivalent yet: the `herdr:blocked` bus
// event, because PiG's Go SDK has no `pi.events` bridge
// (docs/extension-sdk-surface.md, "Exceptions"; the bridge is being ported in
// PiG 0.4.0, Pi 0.99.1 port family 6F). Its two cases are kept as skipped, named
// tests so the gap stays visible; the overlap counting the first one exercises
// is also covered with prompts.
//
// TestReviewAdditions holds cases the TypeScript suite did not have; each one
// fails on a mutation the ported cases let through.

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	herdr "github.com/MichaelKinsy/pigpen/components/herdr/extensions/herdr"
)

func start(t *testing.T, mode string) (*fakeHost, *fakeHerdr) {
	t.Helper()
	h := installFakeHerdr(t)
	return startHost(t, herdr.Extension(), mode), h
}

func startTUI(t *testing.T) (*fakeHost, *fakeHerdr) {
	t.Helper()
	return start(t, "tui")
}

func startSession(t *testing.T) (*fakeHost, *fakeHerdr) {
	t.Helper()
	host, h := startTUI(t)
	host.fire("session_start", map[string]any{"reason": "startup"})
	return host, h
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func mustFlag(t *testing.T, c call, name string) string {
	t.Helper()
	v, ok := flag(c, name)
	if !ok {
		t.Fatalf("%s missing from %q", name, c.args)
	}
	return v
}

func absent(t *testing.T, c call, name string) {
	t.Helper()
	if v, ok := flag(c, name); ok {
		t.Fatalf("%s = %q, want it omitted (%q)", name, v, c.args)
	}
}

func TestOutsideHerdr(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T)
	}{
		{"HERDR_ENV unset", func(t *testing.T) { unset(t, "HERDR_ENV") }},
		{"HERDR_ENV is not 1", func(t *testing.T) { t.Setenv("HERDR_ENV", "0") }},
		{"HERDR_PANE_ID unset", func(t *testing.T) { unset(t, "HERDR_PANE_ID") }},
		{"HERDR_BIN_PATH unset", func(t *testing.T) { unset(t, "HERDR_BIN_PATH") }},
		{"HERDR_BIN_PATH empty", func(t *testing.T) { t.Setenv("HERDR_BIN_PATH", "") }},
		{"HERDR_SOCKET_PATH unset", func(t *testing.T) { unset(t, "HERDR_SOCKET_PATH") }},
		{"HERDR_SOCKET_PATH empty", func(t *testing.T) { t.Setenv("HERDR_SOCKET_PATH", "") }},
	} {
		t.Run("registers nothing and calls nothing when "+tc.name, func(t *testing.T) {
			h := installFakeHerdr(t)
			tc.mutate(t)
			host := startHost(t, herdr.Extension(), "tui")
			if n := len(host.register.Handlers); n != 0 {
				t.Fatalf("registered %d handlers outside herdr", n)
			}
			if len(host.register.Tools) != 0 || len(host.register.Commands) != 0 {
				t.Fatalf("registered tools or commands outside herdr")
			}
			host.fire("session_start", map[string]any{"reason": "startup"})
			host.fire("agent_start", nil)
			settle()
			if got := h.calls(); len(got) != 0 {
				t.Fatalf("herdr called outside herdr: %v", got)
			}
		})
	}
}

func TestHeadlessModes(t *testing.T) {
	for _, mode := range []string{"rpc", "json", "print"} {
		t.Run("reports nothing in "+mode+" mode", func(t *testing.T) {
			host, h := start(t, mode)
			host.fire("session_start", map[string]any{"reason": "startup"})
			host.fire("agent_start", nil)
			host.fire("ui_prompt_start", map[string]any{"kind": "confirm"})
			settle()
			if got := h.calls(); len(got) != 0 {
				t.Fatalf("reported in %s mode: %v", mode, got)
			}
		})
	}
}

func TestStateReports(t *testing.T) {
	t.Run("reports idle, working, idle in order with the pig identity and an increasing seq", func(t *testing.T) {
		host, h := startTUI(t)
		host.fire("session_start", map[string]any{"reason": "startup"})
		h.waitForCalls(1)
		host.fire("agent_start", nil)
		h.waitForCalls(2)
		host.fire("agent_settled", nil)
		all := h.waitForCalls(3)

		eq(t, states(all), []string{"idle", "working", "idle"})
		for _, c := range all {
			eq(t, c.args[:3], []string{"pane", "report-agent", "p_7"})
			if got := mustFlag(t, c, "--source"); got != "custom:pig" {
				t.Fatalf("source = %q", got)
			}
			if got := mustFlag(t, c, "--agent"); got != "pig" {
				t.Fatalf("agent = %q", got)
			}
		}
		order := seqs(t, all)
		for i, n := range order {
			if n <= 0 || n > 1<<53-1 {
				t.Fatalf("seq %d is not a safe positive integer", n)
			}
			if i > 0 && n <= order[i-1] {
				t.Fatalf("seq not increasing: %v", order)
			}
		}
	})

	t.Run("does not repeat an unchanged state", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("agent_settled", nil)
		host.fire("agent_settled", nil)
		settle()
		eq(t, states(h.calls()), []string{"idle"})
	})

	t.Run("does not report idle while the agent is still running", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("agent_start", nil)
		h.waitForCalls(2)
		host.setState(func(s *hostState) { s.idle = false })
		host.fire("agent_settled", nil)
		settle()
		eq(t, states(h.calls()), []string{"idle", "working"})
	})

	t.Run("starts as working when a reload lands mid-turn", func(t *testing.T) {
		host, h := startTUI(t)
		host.setState(func(s *hostState) { s.idle = false })
		host.fire("session_start", map[string]any{"reason": "reload"})
		eq(t, states(h.waitForCalls(1)), []string{"working"})
	})

	t.Run("keeps seq increasing across reporter instances in one process", func(t *testing.T) {
		host, h := startSession(t)
		h.waitForCalls(1)
		host.fire("session_shutdown", map[string]any{"reason": "new"})
		second := startHost(t, herdr.Extension(), "tui")
		second.fire("session_start", map[string]any{"reason": "new"})
		all := h.waitForCalls(2)
		order := seqs(t, all)
		if order[1] <= order[0] {
			t.Fatalf("seq did not increase across instances: %v", order)
		}
	})

	t.Run("sends only the newest state when calls queue behind a slow herdr, still in order", func(t *testing.T) {
		host, h := startTUI(t)
		h.slow(300 * time.Millisecond)
		host.fire("session_start", map[string]any{"reason": "startup"})                     // in flight
		host.fire("agent_start", nil)                                                       // queued, then replaced
		host.fire("ui_prompt_start", map[string]any{"kind": "confirm", "title": "Run it?"}) // queued, then replaced
		host.fire("ui_prompt_end", map[string]any{"kind": "confirm", "title": "Run it?"})
		host.fire("agent_settled", nil) // the newest state
		h.waitForCalls(2)
		settle()
		final := h.calls()
		eq(t, states(final), []string{"idle", "idle"})
		if len(final) != 2 {
			t.Fatalf("intermediate states must be dropped, got %d calls", len(final))
		}
		order := seqs(t, final)
		if order[1] <= order[0] {
			t.Fatalf("seq not increasing: %v", order)
		}
	})

	t.Run("survives a herdr that fails", func(t *testing.T) {
		host, h := startTUI(t)
		h.failWith(3)
		host.fire("session_start", map[string]any{"reason": "startup"})
		host.fire("agent_start", nil)
		h.waitForCalls(1)
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		all := h.calls()
		if got := all[len(all)-1].verb(); got != "release-agent" {
			t.Fatalf("last call = %q, want release-agent", got)
		}
	})

	t.Run("survives a herdr binary that does not exist", func(t *testing.T) {
		installFakeHerdr(t)
		t.Setenv("HERDR_BIN_PATH", filepath.Join(t.TempDir(), "missing"))
		host := startHost(t, herdr.Extension(), "tui")
		host.fire("session_start", map[string]any{"reason": "startup"})
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
	})
}

// TestResumeCommand covers the published "Report the resume command" contract
// (https://herdr.dev/docs/add-herdr-support/): the command after `--`, next to
// --agent-session-id, on every state report; first word a plain command; no
// apostrophe or control character; at most 64 arguments and 8 KiB.
func TestResumeCommand(t *testing.T) {
	t.Run("attaches the exact session file to every state report, after all options", func(t *testing.T) {
		host, h := startSession(t)
		h.waitForCalls(1)
		host.fire("agent_start", nil)
		h.waitForCalls(2)
		host.fire("agent_settled", nil)
		all := h.waitForCalls(3)
		for _, c := range all {
			argv, ok := c.resume()
			if !ok {
				t.Fatalf("no resume command in %q", c.args)
			}
			eq(t, argv, []string{"pig", "--session", "/home/u/.pig/sessions/s1.jsonl"})
			if got := mustFlag(t, c, "--agent-session-id"); got != "s1" {
				t.Fatalf("session id = %q", got)
			}
			// Everything before the separator is an option of report-agent, so the separator is last-but-the-command.
			if i := len(c.args) - 4; c.args[i] != "--" {
				t.Fatalf("`--` is not directly before the command: %q", c.args)
			}
		}
	})

	t.Run("does not use a model flag (herdr's own pi integration does not)", func(t *testing.T) {
		host, h := startSession(t)
		argv, _ := h.waitForCalls(1)[0].resume()
		for _, a := range argv {
			if strings.HasPrefix(a, "--model") || strings.HasPrefix(a, "-m") {
				t.Fatalf("resume command carries a model flag: %q", argv)
			}
		}
		_ = host
	})

	t.Run("follows a session replacement", func(t *testing.T) {
		host, h := startSession(t)
		h.waitForCalls(1)
		host.setState(func(s *hostState) { s.sessionFile, s.sessionID = "/x/s2.jsonl", "s2" })
		host.fire("session_start", map[string]any{"reason": "new"})
		argv, _ := h.waitForCalls(2)[1].resume()
		eq(t, argv, []string{"pig", "--session", "/x/s2.jsonl"})
	})

	t.Run("a Windows session file is a plain argument", func(t *testing.T) {
		host, h := startTUI(t)
		host.setState(func(s *hostState) { s.sessionFile, s.sessionID = `C:\Users\me\.pig\s3.jsonl`, "s3" })
		host.fire("session_start", map[string]any{"reason": "startup"})
		argv, _ := h.waitForCalls(1)[0].resume()
		eq(t, argv, []string{"pig", "--session", `C:\Users\me\.pig\s3.jsonl`})
	})

	// `pig --no-session` has an id but no session file. `pig --session-id <id>`
	// would not bring that session back: it creates a new, persisted session with
	// the id, against the user's --no-session. So there is nothing to resume.
	t.Run("an in-memory session (an id, no session file) reports no command", func(t *testing.T) {
		host, h := startTUI(t)
		host.setState(func(s *hostState) { s.sessionFile, s.sessionID = "", "abc-123_x.y" })
		host.fire("session_start", map[string]any{"reason": "startup"})
		c := h.waitForCalls(1)[0]
		if argv, ok := c.resume(); ok {
			t.Fatalf("sent a resume command for an in-memory session: %q", argv)
		}
		if got := mustFlag(t, c, "--agent-session-id"); got != "abc-123_x.y" {
			t.Fatalf("session id = %q", got)
		}
	})

	for _, tc := range []struct {
		name, file string
	}{
		{"an apostrophe", "/home/o'brien/.pig/sessions/s1.jsonl"},
		{"a control character", "/home/u/.pig/sessions/s\x07.jsonl"},
		{"a C1 control character", "/home/u/.pig/sessions/s\u0085.jsonl"},
		{"a DEL", "/home/u/.pig/sessions/s\x7f.jsonl"},
		{"more than 8 KiB", "/" + strings.Repeat("d", 9000) + "/s1.jsonl"},
	} {
		t.Run("falls back to --session-id when the session file has "+tc.name, func(t *testing.T) {
			host, h := startTUI(t)
			host.setState(func(s *hostState) { s.sessionFile, s.sessionID = tc.file, "s1" })
			host.fire("session_start", map[string]any{"reason": "startup"})
			argv, ok := h.waitForCalls(1)[0].resume()
			if !ok {
				t.Fatal("no resume command")
			}
			eq(t, argv, []string{"pig", "--session-id", "s1"})
		})
	}

	for _, tc := range []struct{ name, file, id string }{
		{"nothing known", "", ""},
		{"relative file, no id", "rel.jsonl", ""},
		{"apostrophe file, id with a space", "/a/b'c.jsonl", "has space"},
		{"apostrophe file, id with an apostrophe", "/a/b'c.jsonl", "it's"},
		{"no file, id that starts with a dash", "", "-rf"},
		{"no file, id with a control character", "", "a\x01b"},
	} {
		t.Run("sends no command when neither the file nor the id can be used: "+tc.name, func(t *testing.T) {
			host, h := startTUI(t)
			host.setState(func(s *hostState) { s.sessionFile, s.sessionID = tc.file, tc.id })
			host.fire("session_start", map[string]any{"reason": "startup"})
			c := h.waitForCalls(1)[0]
			if argv, ok := c.resume(); ok {
				t.Fatalf("sent a resume command %q", argv)
			}
			mustFlag(t, c, "--state")
		})
	}

	t.Run("never sends a resume command with the release", func(t *testing.T) {
		host, h := startSession(t)
		h.waitForCalls(1)
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		all := h.waitForCalls(2)
		if _, ok := all[1].resume(); ok || all[1].verb() != "release-agent" {
			t.Fatalf("release carries a resume command or is not last: %q", all[1].args)
		}
	})

	t.Run("herdr before 0.9.2 rejects `--`: retries the same report once without it, then stops sending it", func(t *testing.T) {
		host, h := startTUI(t)
		h.refuseResume("old")
		host.fire("session_start", map[string]any{"reason": "startup"})
		all := h.waitForCalls(2)
		if _, ok := all[0].resume(); !ok {
			t.Fatalf("first attempt has no resume command: %q", all[0].args)
		}
		if _, ok := all[1].resume(); ok {
			t.Fatalf("retry still has the resume command: %q", all[1].args)
		}
		eq(t, states(all), []string{"idle", "idle"})
		if a, b := mustFlag(t, all[0], "--seq"), mustFlag(t, all[1], "--seq"); a != b {
			t.Fatalf("retry seq %s != first seq %s (the refused report was not applied)", b, a)
		}
		if got := mustFlag(t, all[1], "--agent-session-id"); got != "s1" {
			t.Fatalf("retry lost the session id: %q", all[1].args)
		}
		host.fire("agent_start", nil)
		h.waitForCalls(3)
		host.fire("agent_settled", nil)
		h.waitForCalls(4)
		settle()
		all = h.calls()
		if len(all) != 4 {
			t.Fatalf("expected exactly 4 calls (no further retries): %v", all)
		}
		for _, c := range all[2:] {
			if _, ok := c.resume(); ok {
				t.Fatalf("a later report still carries the resume command: %q", c.args)
			}
		}
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		rel := h.waitForCalls(5)[4]
		if rel.verb() != "release-agent" {
			t.Fatalf("calls = %v", verbs(h.calls()))
		}
	})

	t.Run("invalid_resume_argv from herdr 0.9.2+ gets the same one silent fallback", func(t *testing.T) {
		host, h := startTUI(t)
		h.refuseResume("invalid")
		host.fire("session_start", map[string]any{"reason": "startup"})
		all := h.waitForCalls(2)
		if _, ok := all[1].resume(); ok {
			t.Fatalf("retry still has the resume command: %q", all[1].args)
		}
		host.fire("agent_start", nil)
		h.waitForCalls(3)
		settle()
		all = h.calls()
		if len(all) != 3 {
			t.Fatalf("expected 3 calls: %v", all)
		}
		if _, ok := all[2].resume(); ok {
			t.Fatalf("later report carries the refused command: %q", all[2].args)
		}
	})

	t.Run("an unknown-option failure that is not about `--` is not a verdict on resume", func(t *testing.T) {
		host, h := startTUI(t)
		h.refuseResume("message-option") // herdr before 0.9.0 rejecting an attached --message=value
		host.fire("session_start", map[string]any{"reason": "startup"})
		h.waitForCalls(1)
		settle()
		if n := len(h.calls()); n != 1 {
			t.Fatalf("retried: %d calls", n)
		}
		host.fire("agent_start", nil)
		c := h.waitForCalls(2)[1]
		if _, ok := c.resume(); !ok {
			t.Fatalf("gave up on resume: %q", c.args)
		}
	})

	t.Run("an unrelated failure neither retries nor gives up on resume", func(t *testing.T) {
		host, h := startTUI(t)
		h.refuseResume("broken")
		host.fire("session_start", map[string]any{"reason": "startup"})
		h.waitForCalls(1)
		settle()
		if n := len(h.calls()); n != 1 {
			t.Fatalf("retried an unrelated failure: %d calls", n)
		}
		host.fire("agent_start", nil)
		c := h.waitForCalls(2)[1]
		if _, ok := c.resume(); !ok {
			t.Fatalf("stopped sending the resume command after an unrelated failure: %q", c.args)
		}
	})

	t.Run("a hung herdr is not retried and the timeout still ends the call", func(t *testing.T) {
		host, h := startTUI(t)
		h.hang()
		host.fire("session_start", map[string]any{"reason": "startup"})
		begin := time.Now()
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		if took := time.Since(begin); took > 8*time.Second {
			t.Fatalf("quit took %v behind a hung herdr", took)
		}
		for _, c := range h.calls() {
			if c.verb() == "report-agent" {
				t.Fatalf("a hung report was logged: %q", c.args)
			}
		}
	})
}

func TestBlocked(t *testing.T) {
	t.Run("reports blocked with the prompt title while a confirmation is open, then resumes", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("agent_start", nil)
		h.waitForCalls(2)
		prompt := map[string]any{"reason": "ui_prompt", "kind": "confirm", "title": "Allow rm -rf build?"}
		host.fire("ui_prompt_start", prompt)
		blocked := h.waitForCalls(3)
		if got := mustFlag(t, blocked[2], "--state"); got != "blocked" {
			t.Fatalf("state = %q", got)
		}
		if got := mustFlag(t, blocked[2], "--message"); got != "Allow rm -rf build?" {
			t.Fatalf("message = %q", got)
		}

		host.fire("ui_prompt_end", prompt)
		resumed := h.waitForCalls(4)
		if got := mustFlag(t, resumed[3], "--state"); got != "working" {
			t.Fatalf("state = %q", got)
		}
		absent(t, resumed[3], "--message")
	})

	t.Run("reports blocked for an untitled custom prompt using its kind and returns to idle", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("ui_prompt_start", map[string]any{"kind": "custom"})
		all := h.waitForCalls(2)
		if got := mustFlag(t, all[1], "--message"); got != "custom" {
			t.Fatalf("message = %q", got)
		}
		host.fire("ui_prompt_end", map[string]any{"kind": "custom"})
		eq(t, states(h.waitForCalls(3)), []string{"idle", "blocked", "idle"})
	})

	t.Run("sends an ordinary message as two arguments, the only form herdr before 0.9.0 parses", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("ui_prompt_start", map[string]any{"kind": "confirm", "title": "Apply the plan?"})
		all := h.waitForCalls(2)
		i := -1
		for n, arg := range all[1].args {
			if strings.HasPrefix(arg, "--message=") {
				t.Fatalf("attached form %q is unknown to herdr before 0.9.0: %q", arg, all[1].args)
			}
			if arg == "--message" {
				i = n
			}
		}
		if i < 0 || all[1].args[i+1] != "Apply the plan?" {
			t.Fatalf("no --message <value> pair in %q", all[1].args)
		}
	})

	// Every herdr CLI (0.6 to 0.9.3) takes the argument after --message as its
	// value whatever it starts with, but herdr before 0.9.0 refuses the attached
	// --message=<text> form ("unknown option") and drops the whole report.
	for _, title := range []string{"--force?", "-n: dry run first?", "-"} {
		t.Run("sends a message that starts with a dash as --message <text>, which every herdr parses: "+title, func(t *testing.T) {
			host, h := startSession(t)
			host.fire("ui_prompt_start", map[string]any{"kind": "input", "title": title})
			all := h.waitForCalls(2)
			for n, arg := range all[1].args {
				if strings.HasPrefix(arg, "--message=") {
					t.Fatalf("attached form %q is unknown to herdr before 0.9.0: %q", arg, all[1].args)
				}
				if arg == "--message" && n+1 < len(all[1].args) && all[1].args[n+1] == title {
					return
				}
			}
			t.Fatalf("no --message %q pair in %q", title, all[1].args)
		})
	}

	t.Run("sends a message that is exactly -- attached, so herdr 0.9.2+ cannot take it for the resume separator", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("ui_prompt_start", map[string]any{"kind": "input", "title": "--"})
		c := h.waitForCalls(2)[1]
		if !slices.Contains(c.args, "--message=--") {
			t.Fatalf("no --message=-- in %q", c.args)
		}
		// herdr splits at the first `--`: it must be the separator before the command.
		argv, _ := c.resume()
		eq(t, argv, []string{"pig", "--session", "/home/u/.pig/sessions/s1.jsonl"})
	})

	t.Run("shortens a long multi-line title to one line", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("ui_prompt_start", map[string]any{"kind": "editor", "title": "first line\n" + strings.Repeat("x", 400)})
		message := mustFlag(t, h.waitForCalls(2)[1], "--message")
		if strings.Contains(message, "\n") {
			t.Fatalf("message has a newline: %q", message)
		}
		if n := len([]rune(message)); n > 120 {
			t.Fatalf("message is %d characters, want at most 120", n)
		}
		if !strings.HasPrefix(message, "first line x") {
			t.Fatalf("message = %q", message)
		}
	})

	t.Run("stays blocked until every overlapping block ends", func(t *testing.T) {
		// TypeScript overlaps a prompt with a `herdr:blocked` bus block. The Go SDK
		// has no bus, so two overlapping prompts exercise the same counting.
		host, h := startSession(t)
		host.fire("ui_prompt_start", map[string]any{"kind": "confirm", "title": "A"})
		h.waitForCalls(2)
		host.fire("ui_prompt_start", map[string]any{"kind": "select", "title": "approval"})
		h.waitForCalls(3)
		host.fire("ui_prompt_end", map[string]any{"kind": "confirm"})
		settle()
		eq(t, states(h.calls()), []string{"idle", "blocked", "blocked"})
		host.fire("ui_prompt_end", map[string]any{"kind": "select"})
		all := h.waitForCalls(4)
		eq(t, states(all), []string{"idle", "blocked", "blocked", "idle"})
		if got := mustFlag(t, all[2], "--message"); got != "approval" {
			t.Fatalf("message = %q", got)
		}
	})

	t.Run("ignores an unmatched prompt end", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("ui_prompt_end", map[string]any{"kind": "confirm"})
		settle()
		eq(t, states(h.calls()), []string{"idle"})
	})

	t.Run("herdr:blocked bus block overlaps a prompt (blocked: Go SDK has no pi.events bridge)", func(t *testing.T) {
		t.Skip("blocked: PiG's Go SDK has no pi.events bridge (docs/extension-sdk-surface.md Exceptions; PiG 0.4.0 family 6F ports it)")
	})
}

func TestSession(t *testing.T) {
	t.Run("reports the session file and id with every state", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("agent_start", nil)
		for _, c := range h.waitForCalls(2) {
			if got := mustFlag(t, c, "--agent-session-path"); got != "/home/u/.pig/sessions/s1.jsonl" {
				t.Fatalf("session path = %q", got)
			}
			if got := mustFlag(t, c, "--agent-session-id"); got != "s1" {
				t.Fatalf("session id = %q", got)
			}
		}
	})

	t.Run("follows a session replacement", func(t *testing.T) {
		host, h := startSession(t)
		h.waitForCalls(1)
		host.setState(func(s *hostState) { s.sessionFile, s.sessionID = "/x/s2.jsonl", "s2" })
		host.fire("session_start", map[string]any{"reason": "new"})
		all := h.waitForCalls(2)
		if got := mustFlag(t, all[1], "--agent-session-path"); got != "/x/s2.jsonl" {
			t.Fatalf("session path = %q", got)
		}
		if got := mustFlag(t, all[1], "--agent-session-id"); got != "s2" {
			t.Fatalf("session id = %q", got)
		}
	})

	t.Run("omits a relative session path and an unavailable session", func(t *testing.T) {
		host, h := startTUI(t)
		host.setState(func(s *hostState) { s.sessionFile, s.sessionID = "relative.jsonl", "" })
		host.fire("session_start", map[string]any{"reason": "startup"})
		first := h.waitForCalls(1)[0]
		absent(t, first, "--agent-session-path")
		absent(t, first, "--agent-session-id")
	})

	t.Run("tolerates a session manager that throws", func(t *testing.T) {
		host, h := startTUI(t)
		host.setState(func(s *hostState) { s.sessionFailure = true })
		host.fire("session_start", map[string]any{"reason": "startup"})
		eq(t, states(h.waitForCalls(1)), []string{"idle"})
	})
}

func TestRelease(t *testing.T) {
	t.Run("releases the pane on quit as the last call, with a higher seq", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("agent_start", nil)
		h.waitForCalls(2)
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		all := h.calls()
		release := all[len(all)-1]
		eq(t, release.args[:3], []string{"pane", "release-agent", "p_7"})
		if got := mustFlag(t, release, "--source"); got != "custom:pig" {
			t.Fatalf("source = %q", got)
		}
		if got := mustFlag(t, release, "--agent"); got != "pig" {
			t.Fatalf("agent = %q", got)
		}
		order := seqs(t, all)
		var most int64 = math.MinInt64
		for _, n := range order[:len(order)-1] {
			most = max(most, n)
		}
		if order[len(order)-1] <= most {
			t.Fatalf("release seq %d is not above the reports %v", order[len(order)-1], order)
		}
	})

	t.Run("waits for an in-flight report and drops a queued one so the release is last", func(t *testing.T) {
		host, h := startTUI(t)
		h.slow(300 * time.Millisecond)
		host.fire("session_start", map[string]any{"reason": "startup"}) // in flight
		host.fire("agent_start", nil)                                   // queued; must be dropped
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		eq(t, verbs(h.calls()), []string{"report-agent", "release-agent"})
	})

	t.Run("sends no report after the release", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		host.fire("agent_start", nil)
		host.fire("agent_settled", nil)
		settle()
		eq(t, verbs(h.calls()), []string{"report-agent", "release-agent"})
	})

	for _, reason := range []string{"reload", "new", "resume", "fork"} {
		t.Run("does not release on "+reason+", and goes quiet so it cannot race its successor", func(t *testing.T) {
			host, h := startSession(t)
			h.waitForCalls(1)
			host.fire("session_shutdown", map[string]any{"reason": reason})
			host.fire("agent_start", nil)
			settle()
			eq(t, verbs(h.calls()), []string{"report-agent"})
		})
	}

	t.Run("stops listening for herdr:blocked on shutdown (blocked: Go SDK has no pi.events bridge)", func(t *testing.T) {
		t.Skip("blocked: PiG's Go SDK has no pi.events bridge, so there is no bus listener to unsubscribe (PiG 0.4.0 family 6F ports it)")
	})

	t.Run("does not release a pane it never reported on", func(t *testing.T) {
		host, h := start(t, "print")
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		settle()
		if got := h.calls(); len(got) != 0 {
			t.Fatalf("released a pane it never reported on: %v", got)
		}
	})
}

func TestReviewAdditions(t *testing.T) {
	t.Run("reports again after a session replacement in the same extension instance", func(t *testing.T) {
		// A host may keep the extension alive across /new, /resume, /fork and
		// /reload: the shutdown silences it, and the next session_start must
		// open a fresh reporting epoch.
		host, h := startSession(t)
		h.waitForCalls(1)
		host.fire("session_shutdown", map[string]any{"reason": "new"})
		host.setState(func(s *hostState) { s.sessionFile, s.sessionID = "/x/s2.jsonl", "s2" })
		host.fire("session_start", map[string]any{"reason": "new"})
		all := h.waitForCalls(2)
		if got := mustFlag(t, all[1], "--agent-session-id"); got != "s2" {
			t.Fatalf("session id = %q", got)
		}
		host.fire("agent_start", nil)
		eq(t, states(h.waitForCalls(3)), []string{"idle", "idle", "working"})
	})

	t.Run("an unmatched prompt end does not hide the next prompt", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("ui_prompt_end", map[string]any{"kind": "confirm"})
		host.fire("ui_prompt_start", map[string]any{"kind": "confirm", "title": "Run it?"})
		all := h.waitForCalls(2)
		eq(t, states(all), []string{"idle", "blocked"})
	})

	t.Run("seeds seq from the clock so it keeps increasing across restarts", func(t *testing.T) {
		// herdr keeps the highest seq per source across agent restarts; a
		// restarted process must not start below it.
		before := time.Now().UnixMicro()
		_, h := startSession(t)
		if got := seqs(t, h.waitForCalls(1))[0]; got < before {
			t.Fatalf("seq %d is below the clock %d (microseconds)", got, before)
		}
	})

	t.Run("refreshes the session identity when a turn starts", func(t *testing.T) {
		host, h := startSession(t)
		h.waitForCalls(1)
		host.setState(func(s *hostState) { s.sessionFile, s.sessionID = "/x/s3.jsonl", "s3" })
		host.fire("agent_start", nil)
		working := h.waitForCalls(2)[1]
		if got := mustFlag(t, working, "--agent-session-path"); got != "/x/s3.jsonl" {
			t.Fatalf("session path = %q", got)
		}
	})

	t.Run("bounds a hung herdr call so quit still releases", func(t *testing.T) {
		// Inherently about 3 s: the bound under test is the 3 s call timeout.
		host, h := startTUI(t)
		h.hang()
		host.fire("session_start", map[string]any{"reason": "startup"}) // hangs until killed
		begin := time.Now()
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		if took := time.Since(begin); took > 8*time.Second {
			t.Fatalf("quit took %v behind a hung herdr", took)
		}
		all := h.calls()
		if len(all) == 0 || all[len(all)-1].verb() != "release-agent" {
			t.Fatalf("calls = %v, want the release last", all)
		}
	})
}

func unset(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "") // registers the restore
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}
