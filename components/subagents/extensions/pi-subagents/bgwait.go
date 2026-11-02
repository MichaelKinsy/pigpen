package pi_subagents

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// `bg_wait` (src/runs/background/subagent-wait.ts): block the current turn
// until tracked async runs for this session reach a terminal state, the
// timeout elapses, or the turn is cancelled. The port's runs are in-process,
// so the wait is exact: it wakes on the run's own done channel rather than
// polling.

const (
	waitDefaultTimeoutMs = int64(30 * 60 * 1000) // the original's DEFAULT_TIMEOUT_MS
)

// waitSubscription is one armed non-blocking wait (the original's durable wake
// subscription, session-scoped here).
type waitSubscription struct {
	Token     string
	RunID     string
	ExpiresAt int64
}

var waitSubs = struct {
	mu   sync.Mutex
	subs []waitSubscription
}{}

// bgWaitTool is the `bg_wait` tool implementation.
func bgWaitTool(ctx sdk.Context, params map[string]any) (any, error) {
	cfg := loadSubagentConfig()
	waitCfg := resolveWaitToolConfig(cfg, os.Getenv)
	if !waitCfg.Enabled {
		return explicitOK{Content: "bg_wait is disabled by config.waitTool or PI_SUBAGENT_WAIT_TOOL_ENABLED; returning immediately without blocking background work. Active work keeps going, and you can inspect subagents with subagent({ action: \"status\" }) or rely on completion notifications.",
			Details: map[string]any{"mode": "management", "results": []any{}}}, nil
	}
	sessionID := sessionIDOf(ctx)
	if sessionID == "" {
		return nil, sdk.NewToolError("bg_wait requires an active session identity to scope background work safely.")
	}
	id, _ := params["id"].(string)
	id = jsTrim(id)
	all, _ := params["all"].(bool)
	nonBlocking, _ := params["nonBlocking"].(bool)
	stopOnAttention := true
	if v, ok := params["stopOnAttention"].(bool); ok {
		stopOnAttention = v
	}
	timeoutMs := waitCfg.DefaultTimeout
	if timeoutMs <= 0 {
		timeoutMs = waitDefaultTimeoutMs
	}
	if v, ok := int64Param(params, "timeoutMs"); ok && v > 0 {
		timeoutMs = v
	}
	if nonBlocking && id == "" {
		return nil, sdk.NewToolError("Non-blocking wait subscriptions require id so the registration can bind one exact run identity.")
	}
	if nonBlocking && all {
		return nil, sdk.NewToolError("nonBlocking cannot be combined with all; subscribe to one exact run id.")
	}

	started := nowMs()
	active := runs.activeForSession(sessionID)
	if id != "" {
		matches := []*asyncRun{}
		for _, r := range runs.listMatching(id) {
			r.mu.Lock()
			same := r.SessionID == sessionID
			r.mu.Unlock()
			if same {
				matches = append(matches, r)
			}
		}
		exact := []*asyncRun{}
		for _, r := range matches {
			if r.ID == id {
				exact = append(exact, r)
			}
		}
		if len(exact) > 0 {
			matches = exact
		}
		if len(matches) > 1 {
			ids := []string{}
			for _, r := range matches {
				ids = append(ids, r.ID)
			}
			return nil, sdk.NewToolError(fmt.Sprintf("Ambiguous subagent run id prefix %q matched %d runs: %s. Pass a longer id.", id, len(matches), strings.Join(ids, ", ")))
		}
		if len(matches) == 0 {
			if terminal := readResultFile(findResultID(id)); terminal != nil {
				return waitTerminalResult(terminal), nil
			}
			return explicitOK{
				Content: fmt.Sprintf("No active run matched %q. Nothing to wait for.", id),
				Details: map[string]any{"mode": "management", "results": []any{}},
			}, nil
		}
		active = matches[:1]
	}
	if len(active) == 0 {
		return explicitOK{
			Content: "No active async runs or registered provider work in this session. Nothing to wait for.",
			Details: map[string]any{"mode": "management", "results": []any{}},
		}, nil
	}
	if nonBlocking {
		sub := waitSubscription{Token: "wait-" + active[0].ID + "-" + formatInt(nowMs()), RunID: active[0].ID, ExpiresAt: started + timeoutMs}
		waitSubs.mu.Lock()
		waitSubs.subs = append(waitSubs.subs, sub)
		waitSubs.mu.Unlock()
		return explicitOK{
			Content: fmt.Sprintf("Armed wait subscription %s for exact async run %s. Returning immediately; this session will be woken on completion, failure, attention, reconciliation failure, or timeout. Inspect armed subscriptions with subagent({ action: \"status\" }).", sub.Token, sub.RunID),
			Details: map[string]any{"mode": "management", "results": []any{}},
		}, nil
	}

	waitForAll := id != "" || all
	initial := make([]*asyncRun, len(active))
	copy(initial, active)

	// Wake the wait the moment any tracked run finishes; the run's own done
	// channel is the original's completion event here.
	wake := make(chan struct{}, len(initial))
	for _, r := range initial {
		go func(r *asyncRun) {
			<-r.doneCh
			select {
			case wake <- struct{}{}:
			default:
			}
		}(r)
	}
	doneTurn, stopTurn := cancellation(ctx)
	defer stopTurn()
	for {
		remaining := []*asyncRun{}
		for _, r := range initial {
			r.mu.Lock()
			terminal := r.State != "queued" && r.State != "running"
			r.mu.Unlock()
			if !terminal {
				remaining = append(remaining, r)
			}
		}
		if len(remaining) == 0 {
			break
		}
		if !waitForAll && len(remaining) < len(initial) {
			break
		}
		if stopOnAttention {
			// A run blocked on its supervisor yields the wait (the original).
			for _, r := range remaining {
				r.mu.Lock()
				id, session := r.ID, r.SessionID
				r.mu.Unlock()
				if runHasPendingSupervisor(id, session) {
					ids := []any{}
					for _, x := range initial {
						ids = append(ids, x.ID)
					}
					return explicitOK{
						Content: "Wait yielded for a pending supervisor request. Background work remains active and will continue after the supervisor reply.",
						Details: map[string]any{"mode": "management", "results": []any{}, "wait": map[string]any{"reason": "supervisor_request", "timedOut": false, "activeRunIds": ids}},
					}, nil
				}
			}
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-wake:
			timer.Stop()
		case <-doneTurn:
			timer.Stop()
			return nil, sdk.NewToolError(fmt.Sprintf("Wait aborted after %s. Still active: %s.", formatDuration(nowMs()-started), joinActive(remaining)))
		case <-timer.C:
		}
	}

	terminal := []*asyncRun{}
	for _, r := range initial {
		r.mu.Lock()
		st := r.State
		r.mu.Unlock()
		if st != "queued" && st != "running" {
			terminal = append(terminal, r)
		}
	}
	outcome := summarizeTerminal(terminal)
	elapsed := formatDuration(nowMs() - started)
	failed := 0
	for _, r := range terminal {
		r.mu.Lock()
		if r.State == "failed" {
			failed++
		}
		r.mu.Unlock()
	}
	if waitForAll {
		content := fmt.Sprintf("Waited %s for %d async run(s); done. Outcome: %s. Relevant completion/control events have been observed; inspect status if a notification is not visible yet.", elapsed, len(initial), outcome)
		return explicitOK{Content: content, Details: waitDetails(initial, failed > 0)}, nil
	}
	remainder := ""
	still := len(initial) - len(terminal)
	if still > 0 {
		remainder = fmt.Sprintf(" %d run(s) still in flight — call bg_wait again to catch the next one.", still)
	} else {
		remainder = " No runs remain in flight."
	}
	content := fmt.Sprintf("Waited %s; %d of %d run(s) finished. Outcome: %s.%s Relevant completion/control events have been observed; inspect status if a notification is not visible yet.",
		elapsed, len(terminal), len(initial), outcome, remainder)
	return explicitOK{Content: content, Details: waitDetails(initial, failed > 0)}, nil
}

// waitDetails is the original's result details for a wait over the given runs.
func waitDetails(rs []*asyncRun, failed bool) map[string]any {
	completions := []any{}
	ids := []any{}
	for _, r := range rs {
		ids = append(ids, r.ID)
		agent, status, output := r.completionSnapshot()
		completions = append(completions, map[string]any{
			"runId": r.ID, "agent": agent, "status": status, "output": output,
		})
	}
	d := map[string]any{"mode": "management", "results": []any{}}
	if len(completions) > 0 {
		d["completions"] = completions
	}
	_ = failed
	return d
}

// waitTerminalResult is the original's terminal-run answer for an exact id.
func waitTerminalResult(res *asyncResultFile) any {
	content := fmt.Sprintf("Run %q is terminal. Outcome: %s.", res.ID, summarizeTerminalStates([]string{res.State}))
	return explicitOK{Content: content, Details: map[string]any{"mode": "management", "results": []any{}, "completions": []any{}}}
}

// summarizeTerminal counts the original's outcomes (complete, failed, paused).
func summarizeTerminal(rs []*asyncRun) string {
	counts := map[string]int{}
	order := []string{}
	for _, r := range rs {
		r.mu.Lock()
		st := r.State
		r.mu.Unlock()
		label := map[string]string{"complete": "complete", "failed": "failed", "partial": "failed", "paused": "paused"}[st]
		if label == "" {
			continue
		}
		if counts[label] == 0 {
			order = append(order, label)
		}
		counts[label]++
	}
	sort.Strings(order)
	parts := []string{}
	for _, label := range []string{"complete", "failed", "paused"} {
		if counts[label] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[label], label))
		}
	}
	return strings.Join(parts, ", ")
}

func summarizeTerminalStates(states []string) string {
	counts := map[string]int{}
	for _, st := range states {
		switch st {
		case "complete", "completed":
			counts["complete"]++
		case "failed", "partial":
			counts["failed"]++
		case "paused":
			counts["paused"]++
		}
	}
	parts := []string{}
	for _, label := range []string{"complete", "failed", "paused"} {
		if counts[label] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[label], label))
		}
	}
	return strings.Join(parts, ", ")
}

// joinActive lists the still-active runs the way the original does ("<id> (<state>)").
func joinActive(rs []*asyncRun) string {
	parts := []string{}
	for _, r := range rs {
		r.mu.Lock()
		parts = append(parts, fmt.Sprintf("%s (%s)", r.ID, r.State))
		r.mu.Unlock()
	}
	return strings.Join(parts, ", ")
}

// findResultID resolves an exact result file for a prefix id, when unique.
func findResultID(prefix string) string {
	matches := []string{}
	entries, err := os.ReadDir(resultsDir())
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".json") && strings.HasPrefix(strings.TrimSuffix(name, ".json"), prefix) {
			matches = append(matches, strings.TrimSuffix(name, ".json"))
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

func int64Param(params map[string]any, name string) (int64, bool) {
	switch v := params[name].(type) {
	case float64:
		return int64(v), true
	case int:
		return int64(v), true
	case int64:
		return v, true
	}
	return 0, false
}
