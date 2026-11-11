package pi_subagents

// The fleet inspector's TUI must look like the original's SubagentFleetComponent (port/oracle/src/tui/fleet.ts: render,
// rosterLines, the footer). These tests pin the frame. The theme stub renders plain text: the layout is what is pinned,
// the host's sdk.UITheme adds the original's colours at runtime.

import (
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
		"│" + space(" ", 46) + "│" + "Result transcript tail:" + space(" ", 58) + "│",
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
	if !strings.Contains(c.notice, "Steered run") {
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
