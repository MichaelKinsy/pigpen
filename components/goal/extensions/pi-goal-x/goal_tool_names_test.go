package pi_goal_x

import (
	"os"
	"slices"
	"strings"
	"testing"
)

const gtnF = "goal-tool-names"

var (
	gtnCore     = []string{"create_goal", "get_goal", "update_goal"}
	gtnDrafting = []string{"goal_question", "goal_questionnaire", "propose_goal_draft"}
	// Removed steady-state lifecycle tools: none may exist in the module.
	gtnRemoved = []string{"propose_goal_tweak", "step_complete", "abort_goal", "propose_task_list", "complete_task", "skip_task", "complete_goal", "pause_goal"}
)

func TestGoalToolNamesTwins(t *testing.T) {
	tw(t, gtnF, "the five public tool names are preserved", func(t *testing.T) {
		eq(t, createGoalToolName, "create_goal", "create")
		eq(t, getGoalToolName, "get_goal", "get")
		eq(t, updateGoalToolName, "update_goal", "update")
		eq(t, setGoalTasksToolName, "set_goal_tasks", "set tasks")
		eq(t, updateGoalTaskToolName, "update_goal_task", "update task")
	})
	tw(t, gtnF, "fixed profiles: core three, task two, all five registered", func(t *testing.T) {
		eq(t, coreGoalToolNames, gtnCore, "core names")
		eq(t, taskToolNames, []string{"set_goal_tasks", "update_goal_task"}, "task names")
		eq(t, fiveGoalTools, append(append([]string{}, gtnCore...), taskToolNames...), "five")
		eq(t, coreGoalTools, gtnCore, "core tools")
		eq(t, draftingGoalTools, gtnDrafting, "drafting")
		eq(t, allRegisteredGoalTools, append(append([]string{}, fiveGoalTools...), draftingGoalTools...), "all")
	})
	tw(t, gtnF, "the module declares drafting names only in the transient profile", func(t *testing.T) {
		eq(t, questionToolName, "goal_question", "question")
		eq(t, questionnaireToolName, "goal_questionnaire", "questionnaire")
		eq(t, proposeDraftToolName, "propose_goal_draft", "propose")
		for _, name := range gtnDrafting {
			for what, list := range map[string][]string{"core": coreGoalToolNames, "task": taskToolNames, "work": goalWorkToolNames, "progress": goalProgressToolNames} {
				if slices.Contains(list, name) {
					t.Errorf("%s must not be a %s tool", name, what)
				}
			}
		}
	})
	tw(t, gtnF, "no steady-state lifecycle tools or phase heuristics remain", func(t *testing.T) {
		src, err := os.ReadFile("goal_tool_names.go")
		if err != nil {
			t.Fatal(err)
		}
		source := string(src)
		for _, removed := range gtnRemoved {
			camel := ""
			for _, p := range strings.Split(removed, "_") {
				camel += strings.ToUpper(p[:1]) + p[1:]
			}
			camel = strings.ToLower(camel[:1]) + camel[1:]
			if strings.Contains(source, "const "+camel+"ToolName") || strings.Contains(source, camel+"ToolName ") {
				t.Errorf("removed constant %s must not exist in goal_tool_names.go", removed)
			}
		}
		for _, bad := range []string{"GoalToolPhase", "goalToolPhase", "lifecycleToolNamesForGoalStatus", "isQuestionLikeToolName"} {
			if strings.Contains(source, bad) {
				t.Errorf("%s must be gone", bad)
			}
		}
	})
	tw(t, gtnF, "progress tool set excludes read-only surface tools and workhorse includes them", func(t *testing.T) {
		for _, name := range []string{"get_goal", "create_goal"} {
			eq(t, slices.Contains(goalProgressToolNames, name), false, name)
		}
		for _, name := range []string{updateGoalToolName, updateGoalTaskToolName, "write", "edit", "bash", "read"} {
			eq(t, slices.Contains(goalProgressToolNames, name), true, name)
		}
	})
	tw(t, gtnF, "work tool set covers the five goal tools plus common host work tools", func(t *testing.T) {
		for _, name := range fiveGoalTools {
			if !slices.Contains(goalWorkToolNames, name) {
				t.Errorf("work set must include %s", name)
			}
		}
		for _, name := range []string{"bash", "write", "read", "edit", "grep", "find", "ls"} {
			if !slices.Contains(goalWorkToolNames, name) {
				t.Errorf("work set must include %s", name)
			}
		}
		for _, removed := range gtnRemoved {
			if slices.Contains(goalWorkToolNames, removed) {
				t.Errorf("work set must not include %s", removed)
			}
		}
		for _, name := range gtnDrafting {
			if slices.Contains(goalWorkToolNames, name) {
				t.Errorf("steady work set must not include drafting tool %s", name)
			}
		}
	})
	tw(t, gtnF, "POST_STOP_ALLOWED_TOOLS only includes get_goal", func(t *testing.T) {
		eq(t, len(postStopAllowedTools), 1, "length")
		eq(t, postStopAllowedTools[0], "get_goal", "name")
	})
}
