package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
)

// upstream: packages/coding-agent/src/tools/wait.ts,
// packages/coding-agent/src/async/job-control.ts,
// packages/coding-agent/src/prompts/tools/wait.md
const (
	waitLadderResetMs  = 60_000
	waitProgressStepMs = 500
)

// WaitSchema returns omp's empty object schema for `wait`.
// upstream: packages/coding-agent/src/tools/wait.ts:29
func WaitSchema() sdk.Schema {
	return sdk.Schema{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	}
}

// RenderWaitDescription renders omp's `prompts/tools/wait.md` for default settings.
// upstream: packages/coding-agent/src/tools/wait.ts:331
func RenderWaitDescription(topMs int, subagent, endTurn bool) string {
	if topMs <= 0 {
		topMs = 240_000
	}
	var b strings.Builder
	b.WriteString("Wait only when blocked with nothing else to do.\n")
	b.WriteString("Blocks on background jobs/services you started, or on the answer to a question you sent this run")
	if subagent {
		b.WriteString(", or on your parent's next message")
	}
	b.WriteString(".\n")
	b.WriteString("Nothing you started running and no answer owed? Returns at once; so does a wait on an agent that is idle with nothing to wake it, or that is waiting on you.\n")
	b.WriteString("- MUST wait only on your own jobs/services, or rarely the answer to a question that blocks you.\n")
	b.WriteString("- NEVER wait for another agent's progress or for messages nobody owes you.\n")
	b.WriteString("- Agent you wait on is waiting on you? Answer it first.\n")
	b.WriteString("- Result in, other jobs still running? SHOULD act on it before waiting again.\n")
	if endTurn {
		b.WriteString("- Nothing left but waiting on your own jobs/services or an owed reply? End your turn instead: each of their results and messages starts your next turn.\n")
	} else {
		fmt.Fprintf(&b, "- A wait returned with nothing new and nothing else is left? Call `wait` right away with no other tool call: it then blocks up to %s, longer for a reply.\n", formatDurationMs(int64(topMs)))
	}
	b.WriteString("Results and messages auto-deliver. NEVER poll while work remains.")
	return b.String()
}

// isWaitEndTurn reports whether ctx represents an interactive TUI session where
// every async result and message starts the next turn (omp `session.tuiTranscript === true`).
// upstream: packages/coding-agent/src/tools/wait.ts:329
func isWaitEndTurn(ctx sdk.Context) bool {
	var zero sdk.Context
	if ctx == zero {
		return false
	}
	return ctx.Mode() == "tui" && ctx.HasUI()
}

// ExecuteWait blocks on the calling session's background jobs of every kind
// registered in `internal/jobs` (`bash`, `service`, `agent`, `eval`).
//
// Behaviour matches omp's WaitTool:
//   - Returns immediately if an unconsumed completed/failed job is already ready.
//   - Returns immediately ("Nothing to wait for: ...") when no job is running.
//   - Blocks until the first job settles (then collects briefly for the settle
//     window so near-simultaneous completions share one result), or until the
//     idle ladder window elapses (5s first wait, 4m back-to-back, or 4m in TUI),
//     or until ctx is cancelled.
//   - Marks settled jobs consumed so they are recovered once without duplicates,
//     and re-delivers any watched job that settled while unwatched (e.g. abort/steer).
//
// upstream: packages/coding-agent/src/tools/wait.ts:338-679
func (rt *Runtime) ExecuteWait(ctx sdk.Context) (sdk.ToolResult, error) {
	sessKey := rt.sessionKey(ctx)
	jobs.BindSessionIfMissing(sessKey, rt.sessionContextFor(sessKey, ctx))
	callStartedAt := time.Now()
	endTurn := isWaitEndTurn(ctx)

	// Bind a Go context.Context to this wait call's lifetime and sdk.Context
	// cancellation so any Watch or Claim acquired here is automatically released
	// when ExecuteWait returns or the host cancels the tool call.
	waitCtx, cancelWaitCtx := context.WithCancel(context.Background())
	defer cancelWaitCtx()
	if doneCh := ctx.Done(); doneCh != nil {
		go func() {
			select {
			case <-doneCh:
				cancelWaitCtx()
			case <-waitCtx.Done():
			}
		}()
	}

	// Pop any active idle streak (re-saved only if this wait also times out).
	rt.mu.Lock()
	prevStreak := rt.waitStreaks[sessKey]
	delete(rt.waitStreaks, sessKey)
	rt.mu.Unlock()
	if prevStreak != nil && time.Since(prevStreak.endedAt) >= waitLadderResetMs*time.Millisecond {
		prevStreak = nil
	}

	all := jobs.List(sessKey)
	settledAtEntry := map[string]bool{}
	var running []jobs.Job
	var undelivered []jobs.Job

	for _, j := range all {
		st := j.Status()
		if st == "running" {
			running = append(running, j)
		} else {
			settledAtEntry[j.ID()] = true
			if (st == "completed" || st == "failed") && !jobs.IsConsumed(sessKey, j.ID()) {
				undelivered = append(undelivered, j)
			}
		}
	}

	if len(undelivered) > 0 {
		// Claim each undelivered settled job atomically BEFORE reporting it so a
		// concurrent auto-delivery send in flight cannot hand over the same job
		// twice. If every settled candidate was already claimed/delivered by the
		// listener, fall through to waiting on any still-running jobs (or empty).
		var claimed []jobs.Job
		claimedSet := map[string]bool{}
		for _, j := range undelivered {
			if release, ok := jobs.Claim(waitCtx, sessKey, j.ID()); ok {
				defer release()
				claimed = append(claimed, j)
				claimedSet[j.ID()] = true
			}
		}
		if len(claimed) > 0 {
			combined := append(claimed, running...)
			return rt.buildWaitResult(waitCtx, sessKey, combined, settledAtEntry, callStartedAt, claimedSet), nil
		}
	}
	if len(running) == 0 {
		return sdk.ToolResult{
			Content: "Nothing to wait for: no background job or service you started is running, and no agent you messaged this run owes a reply. Other agents' results and messages arrive on their own.",
			Details: CoordinationDetails{Op: "wait", Jobs: []JobSnapshot{}},
		}, nil
	}

	// Suppress automatic follow-up delivery for EVERY watched job (bash, agent,
	// eval, etc.) via internal/jobs.Watch so `wait` hands over settled results
	// directly without duplicate follow-ups. On leaving `wait` (unwatch),
	// internal/jobs dispatches the listener's normal retry-backed delivery for
	// any watched job that settled unclaimed and unconsumed (e.g. abort/steer
	// during the settle window, matching omp `tools/wait.ts:466,637`).
	watchedIDs := make([]string, len(running))
	for i, j := range running {
		watchedIDs[i] = j.ID()
	}
	unwatch := jobs.Watch(waitCtx, sessKey, watchedIDs)
	defer unwatch()
	floorMs := rt.Config.WaitFloorMs
	if floorMs <= 0 {
		floorMs = 5_000
	}
	topMs := rt.Config.WaitTopMs
	if topMs <= 0 {
		topMs = 240_000
	}
	settleMs := rt.Config.WaitSettleMs
	if settleMs < 0 {
		settleMs = 0
	}

	// Match omp (`tools/wait.ts:470`): interactive TUI job waits jump straight to
	// top window; print mode uses floor (5s) then top (4m) on back-to-back waits.
	windowMs := floorMs
	if prevStreak != nil || (endTurn && len(running) > 0) {
		windowMs = topMs
	}

	// Emit initial progress snapshot.
	emitProgress := func() {
		_ = ctx.OnUpdate(sdk.ToolResult{
			Content: "",
			Details: CoordinationDetails{
				Op:   "wait",
				Jobs: rt.snapshotJobList(sessKey, running),
			},
		})
	}
	emitProgress()

	// Race every running job's Done() channel against the window timer and ctx.Done().
	jobSettledCh := make(chan struct{}, len(running))
	stopWatch := make(chan struct{})
	for _, j := range running {
		j := j
		go func() {
			select {
			case <-j.Done():
				select {
				case jobSettledCh <- struct{}{}:
				default:
				}
			case <-stopWatch:
			}
		}()
	}
	defer close(stopWatch)

	windowTimer := time.NewTimer(time.Duration(windowMs) * time.Millisecond)
	defer windowTimer.Stop()
	progTicker := time.NewTicker(waitProgressStepMs * time.Millisecond)
	defer progTicker.Stop()

	doneCh := ctx.Done()
	wake := ""
	for wake == "" {
		select {
		case <-jobSettledCh:
			wake = "job"
		case <-windowTimer.C:
			wake = "timeout"
		case <-doneCh:
			wake = "abort"
		case <-progTicker.C:
			emitProgress()
		}
	}

	// Brief settle window when multiple jobs are running so near-simultaneous
	// completions land in one wait result.
	if wake == "job" && settleMs > 0 && anyStillRunning(running) {
		settleTimer := time.NewTimer(time.Duration(settleMs) * time.Millisecond)
		allDoneCh := make(chan struct{})
		go func() {
			for _, j := range running {
				select {
				case <-j.Done():
				case <-stopWatch:
					return
				}
			}
			close(allDoneCh)
		}()
		select {
		case <-allDoneCh:
		case <-settleTimer.C:
		case <-doneCh:
			wake = "abort"
		}
		settleTimer.Stop()
	}

	if wake == "abort" {
		if reason := ctx.CancellationReason(); reason != "" {
			return sdk.ToolResult{
				Content: "Wait interrupted by message.",
				Details: CoordinationDetails{Op: "wait", Jobs: []JobSnapshot{}, Interrupted: true},
			}, nil
		}
		return sdk.ToolResult{
			Content: "Wait aborted.",
			Details: CoordinationDetails{Op: "wait", Jobs: rt.snapshotJobList(sessKey, running), Interrupted: true},
			IsError: true,
		}, nil
	}

	res := rt.buildWaitResult(waitCtx, sessKey, running, settledAtEntry, callStartedAt, nil)
	if wake == "timeout" {
		waitedMs := int64(windowMs)
		if prevStreak != nil {
			waitedMs += prevStreak.waitedMs
		}
		rt.mu.Lock()
		rt.waitStreaks[sessKey] = &waitIdleStreak{endedAt: time.Now(), waitedMs: waitedMs}
		rt.mu.Unlock()

		var elapsedLine string
		if endTurn {
			elapsedLine = fmt.Sprintf(
				"No job finished within %s. Nothing else left? End your turn: results and messages start your next one.",
				formatDurationMs(int64(windowMs)),
			)
		} else {
			elapsedLine = fmt.Sprintf(
				"No job finished within %s. Nothing else left? Call `wait` again right away: it then blocks up to %s.",
				formatDurationMs(int64(windowMs)),
				formatDurationMs(int64(topMs)),
			)
		}
		res.Content = res.Content + "\n\n" + elapsedLine
		if prevStreak != nil {
			res.Content += "\n\n" + fmt.Sprintf(
				"<system-notice>\nNothing new after %s of back-to-back waits.\nWaiting is not progress: each response MUST advance the task.\nMake as much progress as you can on everything that does not need what you are waiting on,\nand NEVER start `sleep` jobs to wait longer.\nResults and messages arrive on their own.\n</system-notice>",
				formatDurationMs(waitedMs),
			)
		}
	}
	return res, nil
}

func anyStillRunning(list []jobs.Job) bool {
	for _, j := range list {
		if j.Status() == "running" {
			return true
		}
	}
	return false
}

func (rt *Runtime) snapshotJobList(sessionID string, list []jobs.Job) []JobSnapshot {
	return rt.snapshotJobListWithClaims(sessionID, list, nil)
}

// snapshotJobListWithClaims snapshots list for either live progress (claimed == nil)
// or final wait reporting (claimed != nil, where ResultText/ErrorText/Structured are
// included ONLY for settled jobs whose delivery claim this wait won).
func (rt *Runtime) snapshotJobListWithClaims(sessionID string, list []jobs.Job, claimed map[string]bool) []JobSnapshot {
	now := time.Now()
	snaps := make([]JobSnapshot, 0, len(list))
	seen := map[string]bool{}
	for _, j := range list {
		if seen[j.ID()] {
			continue
		}
		seen[j.ID()] = true
		includeResult := func(id string) bool {
			if claimed != nil {
				return claimed[id]
			}
			return !jobs.IsConsumed(sessionID, id)
		}
		if aj, ok := j.(*AgentJob); ok {
			snap := aj.snapshotWithResult(now, includeResult(j.ID()))
			if snap.Widget == nil {
				displayLabel := snap.Label
				if snap.Intent != "" {
					displayLabel = snap.Intent
				}
				snap.Widget = jobs.BuildFallbackWidget(displayLabel, "", aj.Started())
			}
			snaps = append(snaps, snap)
			continue
		}
		jType := string(j.Kind())
		if j.Kind() == jobs.KindAgent {
			jType = "task"
		}
		st := j.Status()
		durMs := max(int64(0), now.Sub(j.Started()).Milliseconds())
		var settledAtMs int64
		var jobIntent string
		if d, ok := j.(jobs.JobDetailer); ok {
			jobIntent = d.Intent()
			if end := d.Ended(); !end.IsZero() {
				durMs = max(int64(0), end.Sub(j.Started()).Milliseconds())
				settledAtMs = end.UnixMilli()
			}
		}
		var widget *jobs.WidgetView
		if wp, hasW := j.(jobs.WidgetProvider); hasW {
			widget = wp.WidgetView(now)
		} else if j.Kind() == jobs.KindBash || j.Kind() == jobs.KindAgent {
			displayLabel := j.Label()
			if jobIntent != "" {
				displayLabel = jobIntent
			}
			widget = jobs.BuildFallbackWidget(displayLabel, "", j.Started())
		}
		snap := JobSnapshot{
			ID:          j.ID(),
			Type:        jType,
			Status:      st,
			Label:       j.Label(),
			Intent:      jobIntent,
			DurationMs:  durMs,
			SettledAtMs: settledAtMs,
			Widget:      widget,
		}
		if st != "running" && includeResult(j.ID()) {
			if st == "failed" {
				if d, ok := j.(jobs.JobDetailer); ok && d.ErrorText() != "" {
					snap.ErrorText = d.ErrorText()
				} else if res := j.Result(); res != "" {
					snap.ResultText = res
				} else {
					snap.ErrorText = j.Output()
				}
			} else {
				snap.ResultText = j.Result()
				if d, ok := j.(jobs.JobDetailer); ok && d.ErrorText() != "" {
					snap.ErrorText = d.ErrorText()
				}
			}
		}
		snaps = append(snaps, snap)
	}
	return snaps
}

// buildWaitResult formats completed and still-running jobs matching omp's `buildJobResult`.
// Any settled job not already in preClaimed is atomically claimed via jobs.Claim
// BEFORE snapshotting/reporting so the delivery listener and wait never both hand
// over the same job; every claimed job is marked consumed before returning.
// upstream: packages/coding-agent/src/async/job-control.ts:371
func (rt *Runtime) buildWaitResult(waitCtx context.Context, sessionID string, list []jobs.Job, settledAtEntry map[string]bool, callStartedAt time.Time, preClaimed map[string]bool) sdk.ToolResult {
	claimed := map[string]bool{}
	for id, ok := range preClaimed {
		if ok {
			claimed[id] = true
		}
	}
	alreadyConsumed := map[string]bool{}
	for _, j := range list {
		st := j.Status()
		if st == "running" {
			continue
		}
		id := j.ID()
		if claimed[id] {
			continue
		}
		if st == "cancelled" {
			jobs.MarkConsumed(sessionID, id)
			continue
		}
		if release, ok := jobs.Claim(waitCtx, sessionID, id); ok {
			defer release()
			claimed[id] = true
		} else {
			alreadyConsumed[id] = true
		}
	}

	snaps := rt.snapshotJobListWithClaims(sessionID, list, claimed)

	for id := range claimed {
		jobs.MarkConsumed(sessionID, id)
	}
	var completed []JobSnapshot
	var running []JobSnapshot
	for _, s := range snaps {
		if s.Status == "running" {
			running = append(running, s)
		} else {
			completed = append(completed, s)
		}
	}

	var lines []string
	if len(completed) > 0 {
		lines = append(lines, fmt.Sprintf("## Completed (%d)\n", len(completed)))
		for _, j := range completed {
			lines = append(lines, fmt.Sprintf("### %s [%s] — %s", j.ID, j.Type, j.Status))
			if j.Intent != "" {
				lines = append(lines, fmt.Sprintf("Intent: %s", j.Intent))
			}
			lines = append(lines, fmt.Sprintf("Label: %s", j.Label))
			// Match omp (`session/async-job-delivery.ts:104-111`, `async/job-control.ts:423-430`):
			// print `Agent:` ONLY when agentUrlId differs from jobId.
			if j.Type == "task" && j.AgentURLID != "" && j.AgentURLID != j.ID {
				if j.Status == "completed" && (j.AgentOutputPublished == nil || *j.AgentOutputPublished) {
					lines = append(lines, fmt.Sprintf("Agent: agent://%s, history://%s", j.AgentURLID, j.AgentURLID))
				} else {
					lines = append(lines, fmt.Sprintf("Agent: history://%s", j.AgentURLID))
				}
			}
			if j.Status != "cancelled" {
				if alreadyConsumed[j.ID] {
					lines = append(lines, "Delivery: already delivered or recovered.")
				} else if !settledAtEntry[j.ID] {
					lines = append(lines, "Delivery: delivered by this wait — it finished while you were waiting.")
				} else {
					ageSuffix := ""
					if j.SettledAtMs > 0 && callStartedAt.UnixMilli() > j.SettledAtMs {
						ageSuffix = fmt.Sprintf(" — it had already finished %s before this call", formatDurationMs(callStartedAt.UnixMilli()-j.SettledAtMs))
					}
					lines = append(lines, "Delivery: not auto-delivered; recovered by this snapshot"+ageSuffix+".")
				}
			}
			if j.ResultText != "" {
				lines = append(lines, "```", j.ResultText, "```")
			}
			if j.ErrorText != "" {
				lines = append(lines, "Error: "+j.ErrorText)
			}
			// Match omp (`async/job-control.ts:449-464`) structured output summary.
			if j.Structured != nil {
				urlID := j.AgentURLID
				if urlID == "" {
					urlID = j.ID
				}
				header := "Structured output: " + structuredStatusLabel(j.Structured.Status)
				if j.Structured.Error != "" {
					header += ": " + j.Structured.Error
				}
				if j.Structured.HasData {
					header += fmt.Sprintf("; full payload at agent://%s, fields via agent://%s/<field>", urlID, urlID)
				}
				lines = append(lines, header)
				if j.Structured.Status != "valid" && j.Structured.HasData {
					if raw, err := json.MarshalIndent(j.Structured.Data, "", "  "); err == nil {
						lines = append(lines, "```json", string(raw), "```")
					}
				}
			}
			lines = append(lines, "")
		}
	}

	if len(running) > 0 {
		lines = append(lines, fmt.Sprintf("## Still Running (%d)\n", len(running)))
		for _, j := range running {
			intentPrefix := ""
			if j.Intent != "" {
				intentPrefix = j.Intent + " — "
			}
			suffix := formatRunningProgressSuffix(j)
			lines = append(lines, fmt.Sprintf("- `%s` [%s] — %s%s%s", j.ID, j.Type, intentPrefix, j.Label, suffix))
		}
	}

	if len(lines) == 0 {
		lines = append(lines, "No background jobs.")
	}

	return sdk.ToolResult{
		Content: strings.TrimRight(strings.Join(lines, "\n"), "\n"),
		Details: CoordinationDetails{
			Op:   "wait",
			Meta: map[string]any{"source": map[string]any{"type": "report", "value": "background jobs snapshot"}},
			Jobs: snaps,
		},
	}
}

func formatRunningProgressSuffix(j JobSnapshot) string {
	if j.Type != "task" || j.Status != "running" {
		return ""
	}
	var parts []string
	if j.Requests > 0 {
		parts = append(parts, fmt.Sprintf("%d req", j.Requests))
	}
	if j.Cost > 0 {
		if j.Cost < 0.005 {
			parts = append(parts, "<$0.01")
		} else {
			parts = append(parts, fmt.Sprintf("$%.2f", j.Cost))
		}
	}
	if j.ContextTokens > 0 {
		if j.ContextWindow > 0 {
			parts = append(parts, fmt.Sprintf("ctx %d/%d", j.ContextTokens, j.ContextWindow))
		} else {
			parts = append(parts, fmt.Sprintf("ctx %d", j.ContextTokens))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	parts = append([]string{formatDurationMs(j.DurationMs)}, parts...)
	return " — " + strings.Join(parts, " · ")
}

// structuredStatusLabel matches omp's session/async-job-delivery.ts:92-94.
func structuredStatusLabel(status string) string {
	if status == "unavailable" {
		return "unavailable"
	}
	return "schema " + status
}
