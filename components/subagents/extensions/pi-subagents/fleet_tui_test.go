package pi_subagents

// The fleet inspector's TUI must look like the original's SubagentFleetComponent (port/oracle/src/tui/fleet.ts: render,
// rosterLines, the footer). These tests pin the frame. The theme stub renders plain text: the layout is what is pinned,
// the host's sdk.UITheme adds the original's colours at runtime.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type plainTheme struct{}

func (plainTheme) Fg(_ string, text string) string { return text }
func (plainTheme) Bold(text string) string         { return text }

// inspectorFor builds a component over the registry with fixed geometry.
func inspectorFor(t *testing.T, rows int) *fleetInspector {
	t.Helper()
	return &fleetInspector{
		theme:            func() fleetTheme { return plainTheme{} },
		rows:             func() int { return rows },
		refresh:          time.Hour, // no ticker in tests
		detailAutoFollow: true,
	}
}

func TestFleetInspectorFrameLooksLikeTheOriginal(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	c := inspectorFor(t, 10) // bodyHeight = max(2, 10*85/100 - 6) = 2
	got := c.Render(130)
	space := strings.Repeat
	want := []string{
		"╭" + space("─", 128) + "╮",
		"│ " + "Subagent fleet inspector · live controls" + space(" ", 75) + "no children " + "│",
		"├" + space("─", 46) + "┬" + space("─", 81) + "┤",
		"│" + "No tracked children" + space(" ", 27) + "│" + space(" ", 81) + "│",
		"│" + space(" ", 46) + "│" + space(" ", 81) + "│",
		"├" + space("─", 46) + "┴" + space("─", 81) + "┤",
		"│" + " ↑/k/↓/j agent · p Prompt Audit · Enter/H Inspect · s steer · D stop · x/X/ctrl+o tools · r/R refresh · Esc/ctrl+c/q close · 0/0" + "│",
		"╰" + space("─", 128) + "╯",
	}
	eq(t, got, want, "empty fleet frame")
}

func TestFleetInspectorRosterAndDetailLookLikeTheOriginal(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	r := fleet.beginRun("single", "scout", "Find the bug", "/work")
	s := r.addStep("scout", "Find the bug", "")
	s.state = stateRunning
	s.startedAt = time.Unix(1700000000, 0)
	s.output = "All good"
	s.session = "C:/s/child.jsonl"
	c := inspectorFor(t, 12) // bodyHeight = 4
	c.refreshSnapshotLocked()
	got := c.Render(130)
	space := strings.Repeat
	left := "› ● scout · " + r.id[:min(8, len(r.id))]
	status := "● scout · running "
	footer := " ↑/k/↓/j agent · p Prompt Audit · Enter/H Inspect · s steer · D stop · x/X/ctrl+o tools · r/R refresh · Esc/ctrl+c/q close · 1/1"
	want := []string{
		"╭" + space("─", 128) + "╮",
		"│ " + "Subagent fleet inspector · live controls" + space(" ", 69) + status + "│",
		"├" + space("─", 46) + "┬" + space("─", 81) + "┤",
		"│" + left + space(" ", 22) + "running│" + "Session: C:/s/child.jsonl" + space(" ", 56) + "│",
		"│" + space(" ", 46) + "│" + space(" ", 81) + "│",
		"│" + space(" ", 46) + "│" + "Live output:" + space(" ", 69) + "│",
		"│" + space(" ", 46) + "│" + "  All good" + space(" ", 71) + "│",
		"├" + space("─", 46) + "┴" + space("─", 81) + "┤",
		"│" + footer + "│",
		"╰" + space("─", 128) + "╯",
	}
	eq(t, got, want, "roster and detail")
}

func TestFleetInspectorNarrowTerminal(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	c := inspectorFor(t, 10)
	eq(t, c.Render(35), []string{"Subagent fleet needs at least 36 co"}, "narrow")
}

func TestFleetInspectorKeys(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	mk := func(agent string) *stepRecord {
		r := fleet.beginRun("single", agent, "t", "/work")
		s := r.addStep(agent, "t", "")
		s.state = stateRunning
		return s
	}
	mk("scout")
	mk("worker")
	c := inspectorFor(t, 10)
	c.refreshSnapshotLocked()

	res, err := c.HandleInput("j")
	eq(t, err, nil, "j err")
	eq(t, res.Done, false, "j keeps open")
	eq(t, c.selected, 1, "j selects down")
	res, _ = c.HandleInput("k")
	eq(t, c.selected, 0, "k selects up")
	_, _ = c.HandleInput("\x1b[B")
	eq(t, c.selected, 1, "arrow down")
	_, _ = c.HandleInput("\x1b[H")
	eq(t, c.selected, 0, "home")
	_, _ = c.HandleInput("\x1b[F")
	eq(t, c.selected, 1, "end")

	_, _ = c.HandleInput("s")
	if c.steerDraft == nil {
		t.Error("s opens the steer compose line")
	}
	answerRunner(t, "pivoted")
	for _, k := range strings.Split("look left", "") {
		_, _ = c.HandleInput(k)
	}
	eq(t, *c.steerDraft, "look left", "typing composes the message")
	_, _ = c.HandleInput("\r")
	if c.steerDraft != nil {
		t.Error("Enter closes the compose line")
	}
	if !strings.Contains(c.notice, "Steering queued for run run-2") {
		t.Errorf("steer notice: %q", c.notice)
	}
	_, _ = c.HandleInput("j")
	eq(t, c.notice, "", "moving clears the notice")
	_, _ = c.HandleInput("\x1b[H") // home: the first run, still live
	_, _ = c.HandleInput("D")
	if !strings.Contains(c.notice, "Stop requested for run run-1") {
		t.Errorf("stop notice: %q", c.notice)
	}

	res, _ = c.HandleInput("\x1b")
	eq(t, res.Done, true, "Esc closes")
	res, _ = c.HandleInput("q")
	eq(t, res.Done, true, "q closes")
	res, _ = c.HandleInput("\x03")
	eq(t, res.Done, true, "ctrl+c closes")
	res, _ = c.HandleInput("\x1b[A:1u")
	eq(t, res.Done, false, "a key release is dropped")
}

func TestFleetInspectorSelectionSurvivesRefresh(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	mk := func(agent string) {
		r := fleet.beginRun("single", agent, "t", "/work")
		s := r.addStep(agent, "t", "")
		s.state = stateRunning
	}
	mk("scout")
	mk("worker")
	c := inspectorFor(t, 10)
	c.refreshSnapshotLocked()
	c.selectAt(1)
	r := fleet.beginRun("single", "reviewer", "t", "/work")
	r.addStep("reviewer", "t", "").state = stateRunning
	c.refreshSnapshot()
	eq(t, c.items[c.selected].agent, "worker", "selection follows its child")
}

// The detail pane shows the child's live transcript and the progress rows the original shows (its
// renderedTranscript over the child's transcript events, its foregroundActiveDetail rows above).
func TestFleetInspectorDetailShowsTheLiveTranscript(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	dir := tmp(t)
	session := filepath.Join(dir, "1_c1.jsonl")
	writeFile(t, session, transcriptFixture)
	r := fleet.beginRun("single", "scout", "find it", "/work")
	s := r.addStep("scout", "find it", "")
	s.state = stateRunning
	s.startedAt = time.Unix(1700000000, 0)
	s.sessionDir, s.sessionID = dir, "c1"
	c := inspectorFor(t, 20)
	c.refreshSnapshotLocked()
	eq(t, c.detailLines(200), []string{
		"● scout · running",
		"Run: " + r.id + " · Step 1",
		"Task: find it",
		"Started: 2023-11-14T22:13:20Z",
		"Model: m",
		"Turns: 2",
		"Tools: 1",
		"Tokens: 250",
		"Session: " + shortenPath(session),
		"",
		"Conversation · supervisor message",
		"├─ ✓ read a.txt",
		"│   file body",
		"◆ Assistant · p/m",
		"│ found it",
		"│",
		"◇ Supervisor",
		"│ thanks",
	}, "the live transcript and progress rows")
}

// A settled child with no transcript keeps the result tail (the original's formatRememberedForegroundTranscript).
func TestFleetInspectorDetailShowsTheResultTailWhenSettled(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	r := fleet.beginRun("single", "scout", "t", "/work")
	s := r.addStep("scout", "t", "")
	s.state = stateComplete
	s.output = "done\n"
	c := inspectorFor(t, 20)
	c.refreshSnapshotLocked()
	detail := strings.Join(c.detailLines(200), "\n")
	if !strings.Contains(detail, "Result transcript tail:\n  done") {
		t.Errorf("result tail:\n%s", detail)
	}
}

// A child's bash tool result carries real line endings (\r\n). The detail pane draws that text on one
// row of an alt-screen frame, so a bare CR in a rendered line is a rendering bug, not content: the
// terminal returns to column 0 mid-row and the rest of the row's padding erases the border, roster and
// divider already painted on it (a row with no left border and the roster entry missing).
func TestFleetInspectorRenderedLinesCarryNoBareControlBytes(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	dir := tmp(t)
	session := filepath.Join(dir, "1_c1.jsonl")
	// A bash result as the child really writes one: CRLF between lines, LF at the end.
	writeFile(t, session, transcriptFixture+
		`{"type":"message","id":"m7","timestamp":"2024-01-01T00:00:06Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_2","name":"bash","arguments":{"command":"mypy ."}}],"stopReason":"toolUse","model":"p/m","usage":{"totalTokens":10}}}`+"\n"+
		`{"type":"message","id":"m8","timestamp":"2024-01-01T00:00:07Z","message":{"role":"toolResult","toolCallId":"call_2","toolName":"bash","content":[{"type":"text","text":"Success: no issues found in 39 source files\r\nAll checks passed!\n"}],"isError":false,"durationMs":400}}`+"\n")
	r := fleet.beginRun("single", "scout", "find it", "/work")
	s := r.addStep("scout", "find it", "")
	s.state = stateRunning
	s.startedAt = time.Unix(1700000000, 0)
	s.sessionDir, s.sessionID = dir, "c1"
	c := inspectorFor(t, 40)
	c.refreshSnapshotLocked()

	for _, line := range c.Render(160) {
		if strings.ContainsAny(line, "\r\n\t") {
			t.Fatalf("Render emitted a bare control byte (the terminal would jump mid-row): %q", line)
		}
	}
	// The CRLF join is content the reader should still see, minus the control bytes.
	if !strings.Contains(strings.Join(c.detailLines(200), "\n"), "Success: no issues found in 39 source files") {
		t.Errorf("the tool result text is missing:\n%s", strings.Join(c.detailLines(200), "\n"))
	}
}
