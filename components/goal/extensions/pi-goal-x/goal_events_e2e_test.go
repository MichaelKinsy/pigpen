package pi_goal_x_test

// Twins of tests/e2e/goal-prompt-cache-sdk.test.ts (a named skip: it needs the Node SDK's own provider serializers) and
// tests/e2e/network-recovery-rpc.test.ts.
//
// The original's network recovery e2e spawns a live `pi --mode rpc` process against a mock HTTP provider that always answers 503 (or
// 429) and reads the extension's notifications off the RPC stream: after Pi's own retries are spent the extension announces
// "Retrying the goal in Ns (recovery M, unbounded)" with escalating delays, and delivers a checkpoint continuation that restarts the
// turn. The twins below deliver the same run to the extension as the events Pi emits for it (an assistant message that ends in an
// error, then agent_end and agent_settled) on the rig's mock clock, with the original's assertions. The live host run, the real
// provider answering 503, 429 and 401 over HTTP, is checked by the pigeq scenarios net-503-ladder, net-429-ladder, net-503-recovers,
// net-429-allowance and net-401-not-recovered (port/scenarios, recorded from the original under Pi 1402973a). Under that Pi a recovery
// checkpoint reaches before_agent_start as a custom trigger whose event.prompt is empty, so the original treats the run as user driven and
// resets the recovery counter: a live run never escalates past "5s (recovery 1)" (see port/PORT.md). The twins therefore deliver the
// checkpoint marker as the prompt, the shape the e2e's assertions expect, and the scenarios compare the live behavior.

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const reported429 = `429: {"message":"Provider returned error","code":429,"metadata":{"raw":"stealth/ox-alpha is temporarily rate-limited upstream. Please retry shortly.","provider_name":"Stealth","is_byok":false,"limit_source":"upstream_provider_shared_pool"}}`

var recoveryNoticeRE = regexp.MustCompile(`Retrying the goal in (\d+)s \(recovery (\d+), unbounded\)`)

// outageRecovery is runOutageScenario and assertEscalatingUnbounded of the original's e2e: two failed runs, the second one started by the
// checkpoint the first recovery delivered.
func outageRecovery(t *testing.T, providerError string) {
	t.Helper()
	e := newEvRig(t, `{"autoSelectSingleGoal": true, "maxAutonomousRuns": 100}`, nil)
	clock := e.Probe.FakeClock(1_787_659_200_000)
	t.Cleanup(clock.Restore)
	e.startSession()

	failedRun := func(prompt string) {
		t.Helper()
		e.beforeAgentStart(prompt)
		e.fireIdle("agent_end", map[string]any{"messages": []any{errMessage("Retry failed after 3 attempts: " + providerError)}})
		e.fireIdle("agent_settled", map[string]any{})
	}
	var notices []string // recovery notices with their level, as the e2e collects them
	collect := func() {
		t.Helper()
		notices = notices[:0]
		for _, n := range e.Notices() {
			if recoveryNoticeRE.MatchString(n) {
				notices = append(notices, n)
			}
		}
	}

	// "The first notification proves recovery engaged after Pi's own retries."
	failedRun("continue working on the task")
	collect()
	if len(notices) != 1 {
		t.Fatalf("one recovery notice after the first failed run, got %q", e.Notices())
	}
	if n := e.checkpoints(); n != 0 {
		t.Fatalf("the first recovery waits out its delay: %d checkpoints", n)
	}
	// "A checkpoint continuation must be delivered after the first recovery."
	clock.Tick(5_000)
	if n := e.checkpoints(); n != 1 {
		t.Fatalf("a checkpoint continuation must be delivered after the first recovery: %d", n)
	}
	sent := e.Sent()
	checkpoint := str(sent[len(sent)-1].Message["content"])
	if !strings.Contains(checkpoint, "kind=\"checkpoint\"") {
		t.Fatalf("the delivered message is the checkpoint marker: %q", checkpoint)
	}
	// "The second proves the delivered continuation restarted the loop."
	failedRun(checkpoint)
	collect()
	if len(notices) != 2 {
		t.Fatalf("expected at least 2 recovery notifications; got %q", e.Notices())
	}

	var delays, attempts []int
	for _, n := range notices {
		m := recoveryNoticeRE.FindStringSubmatch(n)
		d, _ := strconv.Atoi(m[1])
		a, _ := strconv.Atoi(m[2])
		delays, attempts = append(delays, d), append(attempts, a)
		if !strings.HasPrefix(n, "warning: ") {
			t.Errorf("recovery notices are warnings: %q", n)
		}
	}
	if !(delays[1] > delays[0]) {
		t.Errorf("delays must escalate: %v", delays)
	}
	if attempts[0] != 1 {
		t.Errorf("the counter starts at recovery 1 in a fresh session: %v", attempts)
	}
	if delays[0] != 5 || delays[1] != 10 || attempts[1] != 2 {
		t.Errorf("the ladder is 5 s, 10 s with the counter at 2: delays %v attempts %v", delays, attempts)
	}
}

func TestGoalEventsE2E(t *testing.T) {
	tskip(t, "e2e/goal-prompt-cache-sdk", "real SDK: explicit and implicit caching preserve request prefixes",
		"runs pi's own provider serializers (ModelRuntime.streamSimple/convertToLlm of the Node SDK); the Go side of the prefix property is covered by the goal-prompt-cache and goal-live-retention twins and TestBeforeProviderRequestMovesTheCacheMarker")
	tw(t, "e2e/network-recovery-rpc", "e2e: real pi recovers from sustained 503 outages with escalating unbounded backoff", func(t *testing.T) {
		outageRecovery(t, reported503)
	})
	tw(t, "e2e/network-recovery-rpc", "e2e: real pi recovers from sustained 429 rate-limit outages", func(t *testing.T) {
		outageRecovery(t, reported429)
	})
}
