package tintinweb_subagents

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// The turn limit and the final-turn failure of a run, and how a finished run is reported. upstream:
// agent-runner.ts:1055-1066 (steer at maxTurns, abort at maxTurns+graceTurns), agent-runner.ts:577-590
// (finalTurnError), agent-manager.ts:850-862 (status precedence), status-note.ts, index.ts getStatusLabel.

func runaway(t *testing.T, turns string, spec childSpec) (*childResult, string) {
	t.Helper()
	useFakePig(t, "runaway")
	t.Setenv("FAKE_PIG_TURNS", turns)
	log := t.TempDir() + "/steer"
	t.Setenv("FAKE_PIG_STEER", log)
	spec.Prompt, spec.Cwd = "p", t.TempDir()
	c, err := startRPCChild(context.Background(), spec)
	eq(t, err, nil)
	done := make(chan *childResult, 1)
	go func() {
		res, err := c.wait()
		if err != nil {
			t.Errorf("wait: %v", err)
		}
		done <- res
	}()
	select {
	case res := <-done:
		b, _ := os.ReadFile(log)
		return res, string(b)
	case <-time.After(20 * time.Second):
		t.Fatal("the run never settled")
	}
	return nil, ""
}

func TestTurnLimitAbortsAfterTheGraceTurns(t *testing.T) {
	// maxTurns 2 and the default 5 grace turns: steered at turn 2, aborted at turn 7.
	res, log := runaway(t, "7", childSpec{MaxTurns: 2})
	eq(t, res.WrappedUp, true)
	eq(t, res.Aborted, true)
	eq(t, log, turnLimitSteer+"\n<abort>\n")

	// One turn short of the grace: steered, not aborted.
	res, log = runaway(t, "6", childSpec{MaxTurns: 2})
	eq(t, res.WrappedUp, true)
	eq(t, res.Aborted, false)
	eq(t, log, turnLimitSteer+"\n")

	// graceTurns from the settings.
	res, log = runaway(t, "3", childSpec{MaxTurns: 2, GraceTurns: 1})
	eq(t, res.Aborted, true)
	eq(t, log, turnLimitSteer+"\n<abort>\n")

	// No limit: neither.
	res, log = runaway(t, "30", childSpec{})
	eq(t, res.WrappedUp || res.Aborted, false)
	eq(t, log, "")
}

func TestAFailedFinalTurnIsAFailure(t *testing.T) {
	for mode, want := range map[string]string{
		"provider-error":       "429 rate limited",
		"provider-error-empty": "provider error with no output",
		"length-empty":         "run hit the output token limit before producing any text",
		"ok":                   "",
	} {
		useFakePig(t, mode)
		c, err := startRPCChild(context.Background(), childSpec{Prompt: "p", Cwd: t.TempDir()})
		eq(t, err, nil)
		res, err := c.wait()
		eq(t, err, nil)
		if res.Failure != want {
			t.Errorf("%s: failure %q, want %q", mode, res.Failure, want)
		}
	}
}

func TestGraceTurnsSetting(t *testing.T) {
	eq(t, sanitizeSettings(obj{"graceTurns": 3.0}).GraceTurns, 3)
	for _, bad := range []any{0.0, 1001.0, 2.5, "3", -1.0} {
		eq(t, sanitizeSettings(obj{"graceTurns": bad}).GraceTurns, 0)
	}
	eq(t, sanitizeSettings(obj{"graceTurns": 1000.0}).GraceTurns, 1000)
	cwd := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	os.WriteFile(agentDir()+"/subagents.json", []byte(`{"graceTurns": 4}`), 0o644)
	eq(t, loadSettings(cwd).GraceTurns, 4)
	os.MkdirAll(cwd+"/.pi", 0o755)
	os.WriteFile(cwd+"/.pi/subagents.json", []byte(`{"graceTurns": 9}`), 0o644)
	eq(t, loadSettings(cwd).GraceTurns, 9)
}

func (c *fakeChild) finishWith(res *childResult) {
	c.res = res
	close(c.release)
}

func TestRunOutcomesAreReportedAsTheOriginalReportsThem(t *testing.T) {
	cases := []struct {
		res          *childResult
		status, out  string
		failedChanel bool
	}{
		{&childResult{Text: "final", WrappedUp: true}, statusSteered,
			"(wrapped up at the turn limit — everything the agent produced is above; the task may be unfinished).\n\nfinal", false},
		{&childResult{Text: "fragment", WrappedUp: true, Aborted: true}, statusAborted,
			"(aborted at the turn limit — everything the agent produced is above; the task is unfinished).\n\nfragment", true},
		{&childResult{Text: "half", Failure: "429 rate limited"}, statusError,
			"Agent failed: 429 rate limited\n\nPartial output before the failure:\nhalf", true},
		{&childResult{Text: "  ", Failure: "provider error with no output"}, statusError,
			"Agent failed: provider error with no output", true},
		{&childResult{Text: "done"}, statusCompleted, "token).\n\ndone", false},
	}
	for _, tc := range cases {
		r := startRig(t)
		done := make(chan string, 1)
		go func() {
			done <- r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "run_in_background": false})
		}()
		tc.res.Tokens = 10
		r.fleet.wait(t, 1)[0].finishWith(tc.res)
		out := <-done
		if !strings.HasSuffix(out, tc.out) {
			t.Errorf("%s: %q does not end with %q", tc.status, out, tc.out)
		}
		rec := r.app.mgr.list()[0]
		eq(t, rec.Status, tc.status)
		if tc.failedChanel {
			r.waitEmitted("subagents:failed")
		} else {
			r.waitEmitted("subagents:completed")
		}
	}
}

func TestBackgroundNotificationNamesTheToolCall(t *testing.T) {
	r := startRig(t)
	id := idFrom(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan"}))
	r.fleet.wait(t, 1)[0].finishWith(&childResult{Text: "x", WrappedUp: true})
	for i := 0; i < 200 && len(r.messages()) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	msgs := r.messages()
	eq(t, len(msgs), 1)
	content := msgs[0]["message"].(map[string]any)["content"].(string)
	eq(t, strings.Contains(content, "<task-id>"+id+"</task-id>\n<tool-use-id>call-1</tool-use-id>\n<status>Wrapped up (turn limit)</status>"), true)
	out := r.must("get_subagent_result", obj{"agent_id": id})
	eq(t, strings.Contains(out, "Status: steered (wrapped up at the turn limit — output may be partial) |"), true)
}

func TestNotFoundAndNotRunningTextsQuoteTheReferenceAsGiven(t *testing.T) {
	r := startRig(t)
	eq(t, r.must("get_subagent_result", obj{"agent_id": `a"b\c`}), `Agent not found: "a"b\c". It may have been cleaned up.`)
	eq(t, r.must("steer_subagent", obj{"agent_id": `a"b`, "message": "m"}), `Agent not found: "a"b". It may have been cleaned up.`)
	id := idFrom(t, r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan"}))
	r.fleet.wait(t, 1)[0].finish("x")
	r.waitEmitted("subagents:completed")
	eq(t, r.must("steer_subagent", obj{"agent_id": id, "message": "m"}), `Agent "`+id+`" is not running (status: completed). Cannot steer a non-running agent.`)
}

func TestAQueuedAgentHasARunningDuration(t *testing.T) {
	r := startRig(t)
	r.app.mgr.mu.Lock()
	r.app.mgr.maxConcurrent = 1
	r.app.mgr.mu.Unlock()
	r.must("Agent", obj{"prompt": "p", "description": "first", "subagent_type": "Plan"})
	id := idFrom(t, r.must("Agent", obj{"prompt": "p", "description": "second", "subagent_type": "Plan"}))
	out := r.must("get_subagent_result", obj{"agent_id": id})
	eq(t, strings.Contains(out, "Status: queued | Tool uses: 0 | Duration: 0.0s (running)\n"), true)
}

func TestGraceTurnsReachTheChild(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", dir)
	os.WriteFile(dir+"/subagents.json", []byte(`{"graceTurns": 3, "defaultMaxTurns": 4}`), 0o644)
	r := startRig(t)
	r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan"})
	c := r.fleet.wait(t, 1)[0]
	eq(t, c.spec.GraceTurns, 3)
	eq(t, c.spec.MaxTurns, 4)
}
