package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of the original's test/schedule-params-wiring.test.ts: what the Agent tool persists when `schedule` is set. The
// job is read back through the real store, so the assertions cover persistence too.
const fScheduleParams = "schedule-params-wiring"

// scheduleCallResult is one Agent call that passes `schedule`, in a session of its own.
type scheduleCallResult struct {
	reply, failure string
	jobs           []scheduledSubagent
}

// scheduleCallRig boots the extension in a temp workspace (settings is the project's subagents.json, "" for none), binds
// session_start (the scheduler is inactive until then), makes one scheduling call and reads the jobs back off disk.
func scheduleCallRig(t *testing.T, settings string, params obj) scheduleCallResult {
	t.Helper()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	cwd := t.TempDir()
	if settings != "" {
		eq(t, os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755), nil)
		eq(t, os.WriteFile(filepath.Join(cwd, ".pi", "subagents.json"), []byte(settings), 0o644), nil)
	}
	r := startRigIn(t, cwd)
	call := obj{"prompt": "do the thing", "description": "nightly sweep", "schedule": "0 3 * * * *", "subagent_type": "general-purpose"}
	for k, v := range params {
		call[k] = v
	}
	reply, failure := r.tool("Agent", call)
	return scheduleCallResult{reply, failure, newScheduleStore(resolveStorePath(cwd, rigSessionID)).list()}
}

func TestAgentToolPersistsTheScheduledJob(t *testing.T) {
	tw(t, fScheduleParams, "persists the run-shaping params the job will fire with", func(t *testing.T) {
		got := scheduleCallRig(t, "", obj{"thinking": "high", "isolated": true})
		eq(t, len(got.jobs), 1)
		job := got.jobs[0]
		eq(t, *job.Thinking, "high")
		eq(t, *job.Isolated, true)
		eq(t, job.Prompt, "do the thing")
		eq(t, job.Enabled, true)
		// The original also passes isolation: "worktree" and reads it back. Worktree isolation is not ported (it is
		// refused, naming the feature, and nothing is scheduled), so the job could never carry it.
		refused := scheduleCallRig(t, "", obj{"isolation": "worktree"})
		eq(t, strings.Contains(refused.failure, refusalText(`worktree isolation (isolation: "worktree")`)), true)
		eq(t, len(refused.jobs), 0)
	})
	tw(t, fScheduleParams, "persists the normalized turn limit, not the raw parameter", func(t *testing.T) {
		// max_turns goes through normalizeMaxTurns and the agent-config/default fallback chain before it is stored.
		// Persisting the raw param instead would give the scheduled run a different limit from an identical immediate
		// run, and only at fire time.
		got := scheduleCallRig(t, "", obj{"max_turns": float64(0)}) // 0 means "unlimited", not "zero turns"
		eq(t, len(got.jobs), 1)
		eq(t, got.jobs[0].MaxTurns == nil, true) // normalizeMaxTurns(0)
		got = scheduleCallRig(t, "", obj{"max_turns": float64(7)})
		eq(t, *got.jobs[0].MaxTurns, 7)
	})
	tw(t, fScheduleParams, "falls back to the configured default turn limit when the call omits one", func(t *testing.T) {
		got := scheduleCallRig(t, "", obj{})
		eq(t, len(got.jobs), 1)
		eq(t, got.jobs[0].MaxTurns == nil, true) // normalizeMaxTurns(getDefaultMaxTurns()): unlimited by default
		got = scheduleCallRig(t, `{"defaultMaxTurns": 9}`, obj{})
		eq(t, *got.jobs[0].MaxTurns, 9)
	})
	tw(t, fScheduleParams, "stores the caller's own subagent_type, not the fallback substitute", func(t *testing.T) {
		// The scheduler re-resolves the type at fire time, and the stored name is what a user sees and edits in /agents.
		// Baking in today's substitute would permanently rewrite their job to an agent they never asked for.
		got := scheduleCallRig(t, "", obj{"subagent_type": "does-not-exist"})
		eq(t, len(got.jobs), 1)
		eq(t, got.jobs[0].SubagentType, "does-not-exist")
		eq(t, strings.Contains(got.reply, "Scheduled"), true)
		eq(t, strings.HasPrefix(got.reply, `Note: Unknown agent type "does-not-exist" — using general-purpose.`+"\n\n"), true)
	})
}

// README documents three combinations that scheduling refuses. Each is a published contract an orchestrator reads before
// calling. The exact strings are asserted because the ORDER of the guards decides which message a caller gets when two
// apply at once.
func TestAgentToolScheduleRestrictions(t *testing.T) {
	tw(t, fScheduleParams, "refuses `schedule` with `resume` — schedules create fresh agents", func(t *testing.T) {
		got := scheduleCallRig(t, "", obj{"resume": "agent-123"})
		eq(t, got.reply, "Cannot combine `schedule` with `resume` — schedules create fresh agents.")
		eq(t, len(got.jobs), 0) // refused, not scheduled-and-warned
	})
	tw(t, fScheduleParams, "refuses `schedule` with `inherit_context` — no parent conversation at fire time", func(t *testing.T) {
		got := scheduleCallRig(t, "", obj{"inherit_context": true})
		eq(t, got.reply, "Cannot combine `schedule` with `inherit_context` — there is no parent conversation at fire time.")
		eq(t, len(got.jobs), 0)
	})
	tw(t, fScheduleParams, "refuses `schedule` with `run_in_background: false` rather than silently coercing it", func(t *testing.T) {
		// README:91 long claimed this parameter was "forced to true". It is not: the call is refused. Silently flipping a
		// parameter the caller explicitly set is the failure mode #37 was filed about.
		got := scheduleCallRig(t, "", obj{"run_in_background": false})
		eq(t, got.reply, "Cannot combine `schedule` with `run_in_background: false` — scheduled jobs always run in background.")
		eq(t, len(got.jobs), 0)
	})
	tw(t, fScheduleParams, "accepts `run_in_background: true` and an omitted `run_in_background`", func(t *testing.T) {
		// The mirror: only an explicit `false` is refused, so a caller that sets the flag by habit is not blocked.
		for _, params := range []obj{{"run_in_background": true}, {}} {
			got := scheduleCallRig(t, "", params)
			eq(t, strings.Contains(got.reply, "Scheduled"), true)
			eq(t, len(got.jobs), 1)
		}
	})
	tw(t, fScheduleParams, "refuses scheduling entirely when the project disabled it", func(t *testing.T) {
		got := scheduleCallRig(t, `{"schedulingEnabled": false}`, obj{})
		eq(t, got.reply, "Scheduling is disabled in this project. Enable via /agents → Settings → Scheduling.")
		eq(t, len(got.jobs), 0)
	})
}
