package tintinweb_subagents

import (
	"context"
	"testing"
)

// Twins of the resumeAgent and final-output cases of test/agent-runner.test.ts, against the child process protocol
// (a fake pig) instead of a mocked Pi session.
func TestRunnerResumeTwins(t *testing.T) {
	const f = "agent-runner"
	start := func(t *testing.T, mode string) child {
		t.Helper()
		useFakePig(t, mode)
		c, err := startRPCChild(context.Background(), childSpec{Prompt: "first", Cwd: t.TempDir()})
		eq(t, err, nil)
		t.Cleanup(c.close)
		return c
	}
	tw(t, f, "returns the final assistant text even when no text_delta events were streamed", func(t *testing.T) {
		c := start(t, "events") // the child reports whole messages (message_end), never deltas
		res, err := c.wait()
		eq(t, err, nil)
		eq(t, res.Text, "answer 1 to first")
	})
	tw(t, f, "resumeAgent also falls back to the final assistant message text", func(t *testing.T) {
		c := start(t, "events")
		_, err := c.wait()
		eq(t, err, nil)
		eq(t, c.resume("Continue"), nil)
		res, err := c.wait()
		eq(t, err, nil)
		eq(t, res.Text, "answer 2 to Continue")
		eq(t, res.Failure, "")
	})
	tw(t, f, "resumeAgent applies the same rule", func(t *testing.T) {
		c := start(t, "resume-fail-empty")
		_, _ = c.wait()
		eq(t, c.resume("Continue"), nil)
		res, err := c.wait()
		eq(t, err, nil)
		eq(t, res.Failure, "retries exhausted: 529 overloaded")
	})
	tw(t, f, "resume whose new turn fails empty does NOT return the previous turn's answer (#144)", func(t *testing.T) {
		c := start(t, "resume-fail-empty")
		first, _ := c.wait()
		eq(t, first.Text, "PREVIOUS ANSWER")
		eq(t, c.resume("follow-up"), nil)
		res, err := c.wait()
		eq(t, err, nil)
		eq(t, res.Failure, "retries exhausted: 529 overloaded")
		eq(t, res.Text, "") // NOT "PREVIOUS ANSWER"
	})
	tw(t, f, "resume that produces partial text before failing returns only THIS resume's text", func(t *testing.T) {
		c := start(t, "resume-fail-partial")
		_, _ = c.wait()
		eq(t, c.resume("go"), nil)
		res, err := c.wait()
		eq(t, err, nil)
		eq(t, res.Failure, "retries exhausted: 529 overloaded")
		eq(t, res.Text, "new partial") // this resume's progress, not the prior answer
	})
}
