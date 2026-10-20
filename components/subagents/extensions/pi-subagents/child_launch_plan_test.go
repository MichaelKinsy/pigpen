package pi_subagents

import (
	"path/filepath"
	"testing"
)

// Twins of child-lifecycle and child-launch-plan.

func TestChildLifecycleTwins(t *testing.T) {
	const f = "child-lifecycle"
	tw(t, f, "cancels the previous final drain when resumed work emits ${type}", func(t *testing.T) {
		for _, typ := range []string{"turn_start", "agent_start", "auto_retry_start"} {
			state := &childLifecycleState{}
			eq(t, projectChildLifecycle(childEvent{"type": "message_end"}, true, state), childStartDrain, typ+": message_end")
			eq(t, projectChildLifecycle(childEvent{"type": typ}, false, state), childCancelDrain, typ+": resumed work")
			eq(t, projectChildLifecycle(childEvent{"type": "agent_settled"}, false, state), childStartDrain, typ+": settled")
		}
	})
	tw(t, f, "does not drain compaction settlement before its retry starts", func(t *testing.T) {
		state := &childLifecycleState{}
		eq(t, projectChildLifecycle(childEvent{"type": "compaction_end", "willRetry": true}, false, state), childCancelDrain, "compaction_end")
		eq(t, projectChildLifecycle(childEvent{"type": "agent_settled"}, false, state), childNoAction, "settled before the retry")
		eq(t, projectChildLifecycle(childEvent{"type": "auto_retry_start"}, false, state), childCancelDrain, "retry")
		eq(t, state.CompactionRetryActive, false, "retry flag")
		eq(t, projectChildLifecycle(childEvent{"type": "agent_settled"}, false, state), childStartDrain, "settled after the retry")
	})
	tw(t, f, "still drains a final message or settled session without resumed work", func(t *testing.T) {
		eq(t, projectChildLifecycle(childEvent{"type": "message_end"}, true, nil), childStartDrain, "message_end")
		eq(t, projectChildLifecycle(childEvent{"type": "agent_settled"}, false, nil), childStartDrain, "settled")
		eq(t, projectChildLifecycle(childEvent{"type": "message_update"}, false, nil), childNoAction, "update")
	})
	tw(t, f, "cancels the final drain on a plain agent_end", func(t *testing.T) {
		state := &childLifecycleState{}
		eq(t, projectChildLifecycle(childEvent{"type": "message_end"}, true, state), childStartDrain, "message_end")
		eq(t, projectChildLifecycle(childEvent{"type": "agent_end"}, false, state), childCancelDrain, "agent_end")
		eq(t, projectChildLifecycle(childEvent{"type": "agent_settled"}, false, state), childStartDrain, "settled")
	})
}

func TestChildLaunchPlanTwins(t *testing.T) {
	const f = "child-launch-plan"
	agent := func() AgentConfig {
		return AgentConfig{Name: "worker", Output: "report.md", OutputMode: "file-only", DefaultReads: []string{"brief.md"}, DefaultProgress: true,
			Skills: []string{"typescript"}, Model: "openai-codex/gpt-5.6-luna", Fast: new(true)}
	}
	schema := func(field string) *jsObject {
		o := newObject()
		o.set("type", "object")
		o.set("required", []any{field})
		return o
	}
	tw(t, f, "inherits, overrides, and explicitly disables an agent output schema", func(t *testing.T) {
		inherited, override := schema("ok"), schema("value")
		a := agent()
		a.OutputSchema = inherited
		eq(t, resolveFullStepBehavior(a, childStepOverrides{}, nil).OutputSchema, any(inherited), "inherited")
		eq(t, resolveFullStepBehavior(a, childStepOverrides{OutputSchema: override}, nil).OutputSchema, any(override), "override")
		eq(t, resolveFullStepBehavior(a, childStepOverrides{OutputSchema: false}, nil).OutputSchema, nil, "disabled")
		empty := newObject()
		a.OutputSchema = empty
		eq(t, resolveFullStepBehavior(a, childStepOverrides{}, nil).OutputSchema, any(empty), "an empty schema is still a schema")
	})
	tw(t, f, "resolves cwd, behavior, skills, model, and output path without side effects", func(t *testing.T) {
		runnerCwd := filepath.Join("/tmp", "repo")
		plan := planChildLaunch(childLaunchPlanInput{Agent: agent(), StepOverrides: childStepOverrides{Skills: SkillInput{Set: true, Items: []string{"review"}}, OutputMode: "inline"},
			Task: "Implement the change", RunnerCwd: runnerCwd, RuntimeCwd: runnerCwd, StepCwdInput: "packages/cli", ChainSkills: []string{"shared"}})
		eq(t, plan.StepCwd, filepath.Join(runnerCwd, "packages/cli"), "step cwd")
		eq(t, plan.InstructionCwd, plan.StepCwd, "instruction cwd")
		eq(t, plan.ReadExistenceCwd, plan.StepCwd, "read existence cwd")
		eq(t, plan.OutputPath, filepath.Join(runnerCwd, "packages/cli", "report.md"), "output path")
		eq(t, plan.SkillNames, []string{"review", "shared"}, "skill names")
		want := resolvedStepBehavior{Output: "report.md", OutputMode: "inline", Reads: []string{"brief.md"}, Progress: true, Skills: []string{"review", "shared"}, Model: "openai-codex/gpt-5.6-luna", Fast: new(true)}
		eq(t, plan.Behavior, want, "behavior")
	})
	tw(t, f, "keeps dynamic namespace output unresolved until fanout items materialize", func(t *testing.T) {
		runnerCwd := filepath.Join("/tmp", "repo")
		plan := planChildLaunch(childLaunchPlanInput{Agent: agent(), RunnerCwd: runnerCwd, RuntimeCwd: runnerCwd, ParallelOutputNamespace: &parallelOutputNamespace{StepIndex: 2}})
		eq(t, plan.NamespaceOutputPath, true, "namespace output path")
		eq(t, plan.Behavior.Output, any("report.md"), "behavior output")
		eq(t, plan.OutputPath, filepath.Join(runnerCwd, "report.md"), "output path")
	})
	tw(t, f, "namespaces inherited relative parallel output for concrete items", func(t *testing.T) {
		runnerCwd := filepath.Join("/tmp", "repo")
		plan := planChildLaunch(childLaunchPlanInput{Agent: agent(), RunnerCwd: runnerCwd, RuntimeCwd: runnerCwd, ParallelOutputNamespace: &parallelOutputNamespace{StepIndex: 2, TaskIndex: new(4)}})
		eq(t, plan.NamespaceOutputPath, false, "namespace output path")
		eq(t, plan.Behavior.Output, any(filepath.Join("parallel-2", "4-worker", "report.md")), "behavior output")
		eq(t, plan.OutputPath, filepath.Join(runnerCwd, "parallel-2", "4-worker", "report.md"), "output path")
	})
	tw(t, f, "suppresses progress for read-only templated tasks", func(t *testing.T) {
		b := resolveFullStepBehavior(agent(), childStepOverrides{}, nil)
		plan := planChildLaunch(childLaunchPlanInput{Agent: agent(), ResolvedBehavior: &b, Task: "{task}", OriginalTask: "Review-only. Do not edit files.", RunnerCwd: "/tmp/repo", RuntimeCwd: "/tmp/repo"})
		eq(t, plan.Behavior.Progress, false, "progress")
	})
}
