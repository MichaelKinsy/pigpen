package herdr_test

// The seven cases of herdr-pig's test/herdr-agent-state.test.ts (at 3ec680471e82), one twin each, with the original's
// inputs and expectations. The original drives the extension through a stub `pi` (handlers by name, a `herdr:blocked`
// bus handler) against a fake herdr socket; here the real Go SDK runs behind a fake PiG host and the fake herdr is a
// unix socket server (harness_test.go). The original's ctx() is the host state's default (a TUI session, idle,
// session file /home/user/.pig/agent/sessions/one.jsonl, id session-one) and its pane id is w1:p1.

import (
	"os"
	"slices"
	"testing"

	herdr "github.com/MichaelKinsy/pigpen/components/herdr/extensions/herdr"
)

const upstreamFile = "herdr-agent-state"

func startTUI(t *testing.T) (*fakeHost, *fakeHerdr) {
	t.Helper()
	h := installFakeHerdr(t)
	return startHost(t, herdr.ExtensionFor(os.Getenv), "tui"), h
}

func equalStrings(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestHerdrAgentStateTwins(t *testing.T) {
	tw(t, upstreamFile, "does nothing outside Herdr", func(t *testing.T) {
		installFakeHerdr(t)
		for _, name := range []string{"HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
			t.Setenv(name, "")
			if err := os.Unsetenv(name); err != nil {
				t.Fatal(err)
			}
		}
		host := startHost(t, herdr.ExtensionFor(os.Getenv), "tui")

		if n := len(host.register.Handlers); n != 0 {
			t.Fatalf("registered %d handlers outside herdr", n)
		}
	})

	tw(t, upstreamFile, "reports idle with a resume command when a TUI session starts", func(t *testing.T) {
		host, h := startTUI(t)

		host.fire("session_start", map[string]any{"reason": "startup"})
		requests := h.waitFor(1)

		if requests[0].Method != "pane.report_agent" {
			t.Fatalf("method = %q", requests[0].Method)
		}
		want := map[string]any{"pane_id": "w1:p1", "source": "pig", "agent": "pig", "state": "idle"}
		for key, value := range want {
			if requests[0].Params[key] != value {
				t.Fatalf("%s = %v, want %v (%s)", key, requests[0].Params[key], value, requests[0].Raw)
			}
		}
		resume, _ := requests[0].Params["resume_argv"].([]any)
		if len(resume) != 3 || resume[0] != "pig" || resume[1] != "--session" || resume[2] != "/home/user/.pig/agent/sessions/one.jsonl" {
			t.Fatalf("resume_argv = %v", resume)
		}
	})

	tw(t, upstreamFile, "reports working then idle across a turn with increasing seq", func(t *testing.T) {
		host, h := startTUI(t)

		host.fire("session_start", nil)
		h.waitFor(1)
		host.fire("agent_start", nil)
		h.waitFor(2)
		host.fire("agent_settled", nil)
		requests := h.waitFor(3)

		equalStrings(t, states(requests), []string{"idle", "working", "idle"})
		order := seqs(t, requests)
		if !(order[1] > order[0]) || !(order[2] > order[1]) {
			t.Fatalf("seq not increasing: %v", order)
		}
	})

	tw(t, upstreamFile, "stays silent in non-TUI modes", func(t *testing.T) {
		h := installFakeHerdr(t)
		host := startHost(t, herdr.ExtensionFor(os.Getenv), "rpc")

		host.fire("session_start", nil)
		host.fire("agent_start", nil)
		host.settle()

		if n := len(h.requests()); n != 0 {
			t.Fatalf("%d requests in rpc mode: %v", n, rawLines(h.requests()))
		}
	})

	tw(t, upstreamFile, "reports blocked while another extension holds the herdr:blocked signal", func(t *testing.T) {
		host, h := startTUI(t)

		host.fire("session_start", nil)
		h.waitFor(1)
		host.emit("herdr:blocked", map[string]any{"active": true, "label": "approve command"})
		h.waitFor(2)
		host.emit("herdr:blocked", map[string]any{"active": false})
		requests := h.waitFor(3)

		if requests[1].Params["state"] != "blocked" || requests[1].Params["message"] != "approve command" {
			t.Fatalf("second report = %s", requests[1].Raw)
		}
		if requests[2].Params["state"] != "idle" {
			t.Fatalf("third report = %s", requests[2].Raw)
		}
	})

	tw(t, upstreamFile, "releases the pane only when PiG quits", func(t *testing.T) {
		host, h := startTUI(t)

		host.fire("session_start", nil)
		h.waitFor(1)
		host.fire("session_shutdown", map[string]any{"reason": "resume"})
		host.settle()
		if n := len(h.requests()); n != 1 {
			t.Fatalf("%d requests after a resume shutdown, want 1", n)
		}

		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		requests := h.waitFor(2)

		if requests[1].Method != "pane.release_agent" {
			t.Fatalf("method = %q", requests[1].Method)
		}
		for key, value := range map[string]any{"pane_id": "w1:p1", "source": "pig", "agent": "pig"} {
			if requests[1].Params[key] != value {
				t.Fatalf("%s = %v, want %v (%s)", key, requests[1].Params[key], value, requests[1].Raw)
			}
		}
		order := seqs(t, requests)
		if !(order[1] > order[0]) {
			t.Fatalf("release seq %v is not above the report's %v", order[1], order[0])
		}
	})

	tw(t, upstreamFile, "builds a resume command Herdr will accept", func(t *testing.T) {
		str := func(s string) *string { return &s }
		withSession := func(file, id *string) []string { return herdr.ResumeArgvFor(file, id) }

		if herdr.IsSafeResumeArg("it's") {
			t.Fatal(`isSafeResumeArg("it's") = true`)
		}
		if herdr.IsSafeResumeArg("line\nbreak") {
			t.Fatal(`isSafeResumeArg("line\nbreak") = true`)
		}
		if herdr.IsSafeResumeArg("") {
			t.Fatal(`isSafeResumeArg("") = true`)
		}

		// An apostrophe in the path would make Herdr reject the whole report.
		equalStrings(t, withSession(str("/home/o'brien/s.jsonl"), str("abc")), []string{"pig", "--session", "abc"})
		equalStrings(t, withSession(str("relative.jsonl"), str("abc")), []string{"pig", "--session", "abc"})
		if got := withSession(nil, nil); got != nil {
			t.Fatalf("no session: %q, want none", got)
		}
		// The original's session manager whose getSessionFile throws: only the id is left.
		equalStrings(t, withSession(nil, str("abc")), []string{"pig", "--session", "abc"})
	})
}
