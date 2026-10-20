package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const missionGoalFile = "mission-goal-driver"

func goalMission(t *testing.T, f missionFixture, title, objective string) *MissionRecord {
	t.Helper()
	return mkMission(t, f.loc, MissionCreateInput{Title: title, Objective: objective, Goal: true, Budget: &MissionTokenBudget{Tokens: 100}, Status: "active", OwnerSessionID: "session-1"})
}

func notices(t *testing.T, f missionFixture, children []GoalRetainedChild, turn int) []GoalContinuationNotice {
	t.Helper()
	n, err := collectGoalContinuationNotices(f.loc, "session-1", children, turn, 0)
	if err != nil {
		t.Fatalf("notices: %v", err)
	}
	return n
}

func contains(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Errorf("%q does not contain %q", text, want)
	}
}

func TestMissionGoalDriverTwins(t *testing.T) {
	tw(t, missionGoalFile, "nudges from persisted state only while linked runs are idle and accounts their tokens", func(t *testing.T) {
		f := newMissionFixture(t)
		mission := mkMission(t, f.loc, MissionCreateInput{Title: "Ship auth refresh", Objective: "Implement and validate token refresh", Goal: true, Budget: &MissionTokenBudget{Tokens: 100}, Status: "active", OwnerSessionID: "session-1"})
		statePath, _ := missionStatePath(f.loc, mission.ID)
		writeFile(t, statePath, `{"lanes":{"review":{"status":"ready","action":"Review the token refresh diff"}}}`)
		first := notices(t, f, nil, 1)
		eq(t, len(first), 1, "first notice")
		contains(t, first[0].Message, "Remaining budget: 100 tokens")
		contains(t, first[0].Message, "Next ready action: Review the token refresh diff")

		asyncDir := filepath.Join(f.root, "run-1")
		os.Mkdir(asyncDir, 0o755)
		writeFile(t, filepath.Join(asyncDir, "status.json"), `{"state":"running","totalTokens":{"input":10,"output":5,"total":15}}`)
		updMission(t, f.loc, mission.ID, MissionUpdateInput{AddRuns: []MissionRunLink{{RunID: "workflow-1", Mode: "workflow", AsyncDir: asyncDir, Status: "running"}}})
		eq(t, len(notices(t, f, nil, 2)), 0, "no notice while a run is active")

		writeFile(t, filepath.Join(asyncDir, "status.json"), `{"state":"complete","totalTokens":{"input":25,"output":15,"total":40}}`)
		resumed := notices(t, f, []GoalRetainedChild{{RunID: "child-review", ParentRunID: "workflow-1", Agent: "reviewer", Resumable: true}}, 3)
		eq(t, len(resumed), 1, "resumed notice")
		contains(t, resumed[0].Message, "Remaining budget: 60 tokens")
		contains(t, resumed[0].Message, "Resume retained child child-review (reviewer)")
		read, _ := readMission(f.loc, mission.ID)
		eq(t, read.Usage.Tokens, int64(40), "usage")

		writeFile(t, filepath.Join(asyncDir, "status.json"), `{"state":"complete","totalTokens":{"input":60,"output":40,"total":100}}`)
		eq(t, len(notices(t, f, nil, 4)), 0, "budget spent")
		read, _ = readMission(f.loc, mission.ID)
		eq(t, read.Goal.Status, "budget-exhausted", "goal status")
		eq(t, read.Status, "active", "mission status")
	})

	tw(t, missionGoalFile, "resumes retained children from the latest linked run", func(t *testing.T) {
		f := newMissionFixture(t)
		mission := goalMission(t, f, "Ship auth refresh", "Implement and validate token refresh")
		updMission(t, f.loc, mission.ID, MissionUpdateInput{AddRuns: []MissionRunLink{{RunID: "workflow-old", Mode: "workflow", Status: "complete"}, {RunID: "workflow-new", Mode: "workflow", Status: "complete"}}})
		n := notices(t, f, []GoalRetainedChild{
			{RunID: "child-old", ParentRunID: "workflow-old", Agent: "reviewer", Resumable: true},
			{RunID: "child-new", ParentRunID: "workflow-new", Agent: "reviewer", Resumable: true},
		}, 1)
		eq(t, len(n), 1, "one notice")
		contains(t, n[0].Message, "Resume retained child child-new (reviewer)")
		eq(t, strings.Contains(n[0].Message, "child-old"), false, "older child")
	})

	tw(t, missionGoalFile, "resumes a mission-linked child older than ten unrelated children", func(t *testing.T) {
		f := newMissionFixture(t)
		mission := goalMission(t, f, "Continue linked work", "Resume the linked worker")
		updMission(t, f.loc, mission.ID, MissionUpdateInput{AddRuns: []MissionRunLink{{RunID: "workflow-mission", Mode: "workflow", Status: "complete"}}})
		var retained []GoalRetainedChild
		for offset := range 10 {
			index := 10 - offset
			retained = append(retained, GoalRetainedChild{RunID: "child-" + jsNumber(float64(index)), ParentRunID: "workflow-unrelated-" + jsNumber(float64(index)), Agent: "worker", Resumable: true})
		}
		retained = append(retained, GoalRetainedChild{RunID: "child-0", ParentRunID: "workflow-mission", Agent: "worker", Resumable: true})
		n := notices(t, f, retained, 1)
		eq(t, len(retained), 11, "children")
		eq(t, len(n), 1, "one notice")
		contains(t, n[0].Message, "Resume retained child child-0 (worker)")
		eq(t, strings.Contains(n[0].Message, "child-10"), false, "unrelated child")
	})

	tw(t, missionGoalFile, "does not resume non-resumable retained children from goal notices", func(t *testing.T) {
		f := newMissionFixture(t)
		mission := goalMission(t, f, "Ship auth refresh", "Implement and validate token refresh")
		updMission(t, f.loc, mission.ID, MissionUpdateInput{AddRuns: []MissionRunLink{{RunID: "workflow-stopped", Mode: "workflow", Status: "complete"}}})
		n := notices(t, f, []GoalRetainedChild{{RunID: "child-stopped", ParentRunID: "workflow-stopped", Agent: "worker", Resumable: false}}, 1)
		eq(t, len(n), 1, "one notice")
		eq(t, strings.Contains(n[0].Message, "Resume retained child"), false, "no resume")
		contains(t, n[0].Message, "Next ready action: Continue objective: Implement and validate token refresh")
	})

	tw(t, missionGoalFile, "supports pause, resume, and opt-out through mission.update", func(t *testing.T) {
		f := newMissionFixture(t)
		ctx := f.ctx()
		created := runAction(t, "mission.create", jo("mission", jo("title", "Goal controls", "objective", "Exercise controls", "goal", true, "budget", jo("tokens", float64(50)))), ctx)
		missionID := created.Details["missionId"].(string)
		update := func(goal any) *MissionRecord {
			return detailMission(runAction(t, "mission.update", jo("missionId", missionID, "missionUpdate", jo("goal", goal)), ctx))
		}
		eq(t, update(jo("paused", true)).Goal.Status, "paused", "paused")
		eq(t, len(notices(t, f, nil, 1)), 0, "no notice while paused")
		eq(t, update(jo("paused", false)).Goal.Status, "active", "resumed")
		eq(t, update(false).Goal == nil, true, "opted out")
	})
}
