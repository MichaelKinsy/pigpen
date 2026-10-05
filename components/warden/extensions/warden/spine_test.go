package warden

import (
	"strings"
	"testing"
)

func userTurn(s string) BranchMessage { return BranchMessage{Type: "message", Role: "user", Text: s} }

// twin: tests/shape.test.ts:131
func TestTaskSpineOneTurnHasNoSpine(t *testing.T) {
	if TaskSpineOf([]BranchMessage{userTurn("fix the login bug")}, "") != nil {
		t.Error("one turn")
	}
	if TaskSpineOf([]BranchMessage{userTurn("fix the login bug")}, "fix the login bug") != nil {
		t.Error("the supplied copy of the only turn")
	}
	if TaskSpineOf(nil, "fix the login bug") != nil {
		t.Error("a first prompt not yet in the branch")
	}
	mustEqual(t, TaskSpineOf([]BranchMessage{userTurn("fix the login bug"), userTurn("go")}, ""), &TaskSpine{Goal: "fix the login bug", Task: "go", History: []string{}}, "two turns")
}

// twin: tests/shape.test.ts:138 (the upstream case builds the branch with Pi's SessionManager; the
// compaction entry is represented as a non-message branch entry, which is what the spine ignores).
func TestTaskSpineAfterCompactionTheGoalIsStillTheFirstUserTurn(t *testing.T) {
	branch := []BranchMessage{
		userTurn("add a rate limiter"), userTurn("wire it into the app"), userTurn("now the tests"), userTurn("run the suite"),
		{Type: "compaction", Text: "Summary: the user asked for something else entirely"},
		userTurn("check the coverage"),
	}
	s := TaskSpineOf(branch, "")
	mustEqual(t, s, &TaskSpine{Goal: "add a rate limiter", Task: "check the coverage", History: []string{"run the suite", "now the tests", "wire it into the app"}}, "spine")
}

// twin: tests/shape.test.ts:155
func TestTaskSpineNoUserTurn(t *testing.T) {
	if TaskSpineOf(nil, "") != nil || TaskSpineOf([]BranchMessage{userTurn("   ")}, "") != nil {
		t.Error("no user turn: no spine")
	}
}

// twin: tests/shape.test.ts:161
func TestTaskSpineSixTurns(t *testing.T) {
	var entries []BranchMessage
	for _, s := range []string{"one", "two", "three", "four", "five", "six"} {
		entries = append(entries, userTurn(s))
	}
	mustEqual(t, TaskSpineOf(entries, ""), &TaskSpine{Goal: "one", Task: "six", History: []string{"five", "four", "three", "two"}}, "six turns")
}

// twin: tests/shape.test.ts:169
func TestTaskSpineOversizedTurns(t *testing.T) {
	goal := strings.Repeat("g", 500)
	task := strings.Repeat("t", 100)
	s := TaskSpineOf([]BranchMessage{userTurn(goal), userTurn(strings.Repeat("a", 400)), userTurn(strings.Repeat("b", 400)), userTurn(strings.Repeat("c", 400)), userTurn(task)}, "")
	if s.Task != task || s.Goal != goal {
		t.Fatal("task and goal untouched while history can still give way")
	}
	mustEqual(t, s.History, []string{strings.Repeat("c", 400), strings.Repeat("b", 200)}, "newest turns keep their text, the oldest give way")
	if len(s.Goal)+len(s.Task)+len(strings.Join(s.History, "")) > 1200 {
		t.Error("over the cap")
	}
	huge := TaskSpineOf([]BranchMessage{userTurn(strings.Repeat("h", 2000)), userTurn(strings.Repeat("m", 300)), userTurn(strings.Repeat("t", 50))}, "")
	if huge.Task != strings.Repeat("t", 50) || len(huge.History) != 0 || huge.Goal != strings.Repeat("h", 1150) {
		t.Errorf("a goal that cannot fit is clipped; task still is not: %d %d", len(huge.Goal), len(huge.History))
	}
}

// twin: tests/shape.test.ts:185
func TestTaskSpineSkipsBlankTurnsAndNonTextParts(t *testing.T) {
	entries := []BranchMessage{
		userTurn("build the search feature"),
		userTurn("now add tests"),
		{Type: "message", Role: "user", Text: "  "}, // an image-only turn has no text
		{Type: "model_change"},
		userTurn("run the suite"),
	}
	mustEqual(t, TaskSpineOf(entries, ""), &TaskSpine{Goal: "build the search feature", Task: "run the suite", History: []string{"now add tests"}}, "spine")
}

// twin: tests/shape.test.ts:199
func TestTaskSpineASuppliedLatestTurnReplacesTheBranchCopy(t *testing.T) {
	entries := []BranchMessage{userTurn("first goal"), userTurn("check the logs")}
	mustEqual(t, TaskSpineOf(entries, "check the logs"), &TaskSpine{Goal: "first goal", Task: "check the logs", History: []string{}}, "same latest turn")
	mustEqual(t, TaskSpineOf(entries, "new prompt"), &TaskSpine{Goal: "first goal", Task: "new prompt", History: []string{"check the logs"}}, "a prompt not yet in the branch")
}

// twin: tests/shape.test.ts:206
func TestTaskSpineGoalAndHistoryLeaveRedacted(t *testing.T) {
	s := TaskSpineOf([]BranchMessage{userTurn("use TOKEN=supersecretvalue1 to log in"), userTurn("go")}, "")
	mustNotContain(t, s.Goal, "supersecretvalue1", "goal")
	if s.Task != "go" {
		t.Errorf("task %q", s.Task)
	}
}
