package pi_subagents

// The bg_wait tool: block the current turn until outstanding async runs, provider work or a remembered detached foreground
// run finishes (or needs attention), the window elapses or the turn is aborted. Ports subagent-wait.ts and auto-drain.ts.

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultWaitTimeoutMs      = 30 * 60 * 1000
	minPollIntervalMs         = 250
	defaultPollIntervalMs     = 1000
	defaultAutoDrainTimeoutMs = 30 * 60 * 1000
)

// WaitParams are the arguments of the bg_wait tool.
type WaitParams struct {
	ID              string
	NonBlocking     bool
	All             bool
	TimeoutMs       *int64
	StopOnAttention *bool
}

// WaitDetails is the machine-readable reason a wait returned without all its work finished.
type WaitDetails struct {
	Reason              string       `json:"reason"` // window_elapsed, supervisor_request
	TimedOut            bool         `json:"timedOut"`
	ActiveRunIDs        []string     `json:"activeRunIds"`
	ActiveProviderItems []ProviderID `json:"activeProviderItems"`
}

// ProviderID names one provider work item.
type ProviderID struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

// WaitResult is the outcome of a wait: its text, whether it is an error, and the details a caller can read.
type WaitResult struct {
	Text        string
	IsError     bool
	Completions []WaitCompletion
	Wait        *WaitDetails
	Usage       *Usage
}

// WaitEventBus is the event surface a wait subscribes to (pi.events).
type WaitEventBus interface {
	On(channel string, handler func(any)) func()
}

// WaitSubscribe arms a durable wake subscription for one exact run.
type WaitSubscribe func(targetKind, runID, requestedID string, timeoutMs int64) (token string, expiresAt int64, err error)

// BackgroundWorkDeps is the provider protocol seam of a wait (tests inject it).
type BackgroundWorkDeps struct {
	Snapshot     func(sessionID string, nowMs int64) (BackgroundWorkSnapshot, error)
	WakeChannels func() []string
}

// WaitDeps are the seams and settings of a wait.
type WaitDeps struct {
	State                       *SubagentState
	OnUpdate                    func(WaitResult)
	AsyncDirRoot                string
	ResultsDir                  string
	NestedRootRunID             string
	Kill                        killFunc
	Now                         func() int64
	PollIntervalMs              int64
	Enabled                     *bool
	DefaultTimeoutMs            *int64
	Sleep                       func(ms int64, done <-chan struct{})
	StopOnAttention             *bool
	FailOnFailedRuns            bool
	FailOnAttention             bool
	HasPendingSupervisorRequest func() bool
	Subscribe                   WaitSubscribe
	BackgroundWork              *BackgroundWorkDeps
	Events                      WaitEventBus
}

// nestedRunScope is nested-events.ts nestedRunScope: where the runs of a nested root live.
func nestedRunScope(rootRunID string) (asyncDirRoot, resultsDir string) {
	return filepath.Join(tempRootDir(), "nested-subagent-runs", rootRunID), filepath.Join(defaultAsyncDirs().Results, "nested", rootRunID)
}

var activeWaitStates = []string{"queued", "running"}

func defaultSleep(ms int64, done <-chan struct{}) {
	t := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer t.Stop()
	select {
	case <-t.C:
	case <-done:
	}
}

func wakeChannelsOf(deps WaitDeps) []string {
	if deps.BackgroundWork != nil && deps.BackgroundWork.WakeChannels != nil {
		return deps.BackgroundWork.WakeChannels()
	}
	ch, _ := listBackgroundWorkWakeChannels()
	return ch
}

var waitWakeChannels = []string{intercomDetachRequestEvent, subagentAsyncCompleteEvent, subagentForegroundCompleteE, subagentControlEvent, subagentControlIntercomEvt, subagentResultIntercomEvt}

// waitForWake sleeps up to ms, but wakes early when a subagent event fires on the bus or the turn aborts.
func waitForWake(ms int64, signal <-chan struct{}, deps WaitDeps) error {
	sleep := deps.Sleep
	if sleep == nil {
		sleep = defaultSleep
	}
	if deps.Events == nil {
		sleep(ms, signal)
		return nil
	}
	provider, err := listBackgroundWorkWakeChannelsOrErr(deps)
	if err != nil {
		return err
	}
	woke := make(chan struct{})
	var once sync.Once
	done := func() { once.Do(func() { close(woke) }) }
	var unsubs []func()
	seen := map[string]bool{}
	for _, c := range append(append([]string{}, waitWakeChannels...), provider...) {
		if seen[c] {
			continue
		}
		seen[c] = true
		unsubs = append(unsubs, deps.Events.On(c, func(any) { done() }))
	}
	defer func() {
		for _, u := range unsubs {
			u()
		}
	}()
	select {
	case <-signal:
		return nil
	case <-woke:
		return nil
	default:
	}
	local := make(chan struct{})
	go func() {
		select {
		case <-woke:
		case <-signal:
		}
		close(local)
	}()
	sleep(ms, local)
	done()
	return nil
}

func listBackgroundWorkWakeChannelsOrErr(deps WaitDeps) ([]string, error) {
	if deps.BackgroundWork != nil && deps.BackgroundWork.WakeChannels != nil {
		return deps.BackgroundWork.WakeChannels(), nil
	}
	return listBackgroundWorkWakeChannels()
}

func matchesID(run AsyncRunSummary, id string) bool {
	return run.ID == id || strings.HasPrefix(run.ID, id)
}

func needsAttention(run AsyncRunSummary) bool {
	if run.ActivityState == "needs_attention" {
		return true
	}
	for _, s := range run.Steps {
		if s.ActivityState == "needs_attention" {
			return true
		}
	}
	return false
}

func hasSupervisorTool(run AsyncRunSummary) bool {
	if run.CurrentTool == "contact_supervisor" || run.CurrentTool == "intercom" {
		return true
	}
	for _, s := range run.Steps {
		if s.CurrentTool == "contact_supervisor" || s.CurrentTool == "intercom" {
			return true
		}
	}
	return false
}

func backgroundWorkIdentity(item RegisteredBackgroundWorkItem) string {
	return item.Provider + "\x00" + item.SessionID + "\x00" + item.ID
}

func backgroundWorkForSession(deps WaitDeps, nowMs int64) (BackgroundWorkSnapshot, error) {
	if deps.State.CurrentSessionID == "" {
		return BackgroundWorkSnapshot{}, errors.New("bg_wait requires an active session identity to scope background work safely.")
	}
	if deps.BackgroundWork != nil && deps.BackgroundWork.Snapshot != nil {
		return deps.BackgroundWork.Snapshot(deps.State.CurrentSessionID, nowMs)
	}
	return snapshotBackgroundWork(deps.State.CurrentSessionID, nowMs)
}

type waitScope struct{ AsyncDirRoot, ResultsDir string }

// waitRunScopes: the ordinary scope, then the nested root's scope when the wait runs for a child.
func waitRunScopes(asyncDirRoot, resultsDir, nestedRootRunID string) []waitScope {
	d := defaultAsyncDirs()
	if asyncDirRoot == "" {
		asyncDirRoot = d.Async
	}
	if resultsDir == "" {
		resultsDir = d.Results
	}
	scopes := []waitScope{{asyncDirRoot, resultsDir}}
	if nestedRootRunID != "" {
		a, r := nestedRunScope(nestedRootRunID)
		scopes = append(scopes, waitScope{a, r})
	}
	return scopes
}

func scopesOf(deps WaitDeps) []waitScope {
	return waitRunScopes(deps.AsyncDirRoot, deps.ResultsDir, deps.NestedRootRunID)
}

func collectScopedCompletions(terminal []AsyncRunSummary, deps WaitDeps, references *[]string) ([]WaitCompletion, error) {
	var out []WaitCompletion
	for _, scope := range scopesOf(deps) {
		var mine []AsyncRunSummary
		a1, _ := filepath.Abs(scope.AsyncDirRoot)
		for _, r := range terminal {
			if a2, _ := filepath.Abs(filepath.Dir(r.AsyncDir)); a1 == a2 {
				mine = append(mine, r)
			}
		}
		c, err := collectWaitCompletions(mine, deps.State, scope.ResultsDir, func(ref string) { *references = append(*references, ref) })
		if err != nil {
			return nil, err
		}
		out = append(out, c...)
	}
	return out, nil
}

func listOpts(deps WaitDeps, scope waitScope) AsyncRunListOptions {
	return AsyncRunListOptions{SessionID: deps.State.CurrentSessionID, ResultsDir: scope.ResultsDir, Kill: deps.Kill, Now: deps.Now}
}

func runsForSession(p WaitParams, deps WaitDeps, activeOnly bool) ([]AsyncRunSummary, error) {
	var out []AsyncRunSummary
	for _, scope := range scopesOf(deps) {
		o := listOpts(deps, scope)
		if activeOnly {
			o.States = activeWaitStates
		}
		o.RunID = p.ID
		runs, err := listAsyncRuns(scope.AsyncDirRoot, o)
		if err != nil {
			return nil, err
		}
		out = append(out, runs...)
	}
	if p.ID != "" {
		var f []AsyncRunSummary
		for _, r := range out {
			if matchesID(r, p.ID) {
				f = append(f, r)
			}
		}
		return f, nil
	}
	return out, nil
}

func attentionRuns(p WaitParams, deps WaitDeps, initial map[string]bool) ([]AsyncRunSummary, error) {
	runs, err := runsForSession(p, deps, true)
	if err != nil {
		return nil, err
	}
	var out []AsyncRunSummary
	for _, r := range runs {
		if needsAttention(r) && initial[r.ID] {
			out = append(out, r)
		}
	}
	return out, nil
}

func runsForIDs(ids []string, deps WaitDeps) ([]AsyncRunSummary, error) {
	var out []AsyncRunSummary
	for _, scope := range scopesOf(deps) {
		for _, id := range ids {
			o := listOpts(deps, scope)
			o.RunID, o.ExactRunID = id, true
			runs, err := listAsyncRuns(scope.AsyncDirRoot, o)
			if err != nil {
				return nil, err
			}
			out = append(out, runs...)
		}
	}
	return out, nil
}

func summarizeTerminalRuns(runs []AsyncRunSummary, providerFinished int) string {
	if len(runs) == 0 && providerFinished == 0 {
		return ""
	}
	counts := map[string]int{}
	for _, r := range runs {
		counts[r.State]++
	}
	var parts []string
	for _, s := range []string{"complete", "failed", "paused"} {
		if counts[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
		}
	}
	if providerFinished > 0 {
		parts = append(parts, fmt.Sprintf("%d provider item(s) finished", providerFinished))
	}
	return strings.Join(parts, ", ")
}

func completionUsage(cs []WaitCompletion) *Usage {
	var u Usage
	projected := false
	for _, c := range cs {
		for _, ch := range c.Results {
			if ch.Usage == nil || *ch.Usage == (Usage{}) {
				continue
			}
			projected = true
			u.Input += ch.Usage.Input
			u.Output += ch.Usage.Output
			u.CacheRead += ch.Usage.CacheRead
			u.CacheWrite += ch.Usage.CacheWrite
			u.Cost += ch.Usage.Cost
			u.Turns += ch.Usage.Turns
		}
	}
	if !projected {
		return nil
	}
	return &u
}

func waitResultOf(text string, isError bool, completions []WaitCompletion) WaitResult {
	for _, c := range completions {
		if c.WorkflowReceiptPath != "" {
			text += fmt.Sprintf("\nWorkflow receipt [%s]: %s", c.RunID, c.WorkflowReceiptPath)
		}
	}
	return WaitResult{Text: text, IsError: isError, Completions: completions, Usage: completionUsage(completions)}
}

func formatCompletionRecovery(cs []WaitCompletion) string {
	var lines []string
	for _, c := range cs {
		for _, ch := range c.Results {
			if ch.TimeoutRecovery != nil {
				lines = append(lines, formatTimeoutRecoveryLines(timeoutRecoveryMap(ch.TimeoutRecovery), "")...)
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n" + strings.Join(lines, "\n")
}

func timeoutRecoveryMap(p *TimeoutRecoveryProjection) map[string]any {
	files := make([]any, len(p.ChangedFiles))
	for i, f := range p.ChangedFiles {
		files[i] = f
	}
	m := map[string]any{"termination": p.Termination, "changedFiles": files, "truncated": p.Truncated, "recoveryNeeded": p.RecoveryNeeded}
	if p.Reason != "" {
		m["reason"] = p.Reason
	}
	if p.ReportStatus != "" {
		m["reportStatus"] = p.ReportStatus
	}
	return m
}

func windowElapsedResult(text string, activeRunIDs []string, providers []RegisteredBackgroundWorkItem) WaitResult {
	ids := []ProviderID{}
	for _, p := range providers {
		ids = append(ids, ProviderID{p.Provider, p.ID})
	}
	if activeRunIDs == nil {
		activeRunIDs = []string{}
	}
	return WaitResult{Text: text, Wait: &WaitDetails{Reason: "window_elapsed", TimedOut: true, ActiveRunIDs: activeRunIDs, ActiveProviderItems: ids}}
}

func supervisorYieldResult(activeRunIDs []string, providers []RegisteredBackgroundWorkItem) WaitResult {
	ids := []ProviderID{}
	for _, p := range providers {
		ids = append(ids, ProviderID{p.Provider, p.ID})
	}
	if activeRunIDs == nil {
		activeRunIDs = []string{}
	}
	return WaitResult{Text: "Wait yielded for a pending supervisor request. Background work remains active and will continue after the supervisor reply.", Wait: &WaitDetails{Reason: "supervisor_request", ActiveRunIDs: activeRunIDs, ActiveProviderItems: ids}}
}

type initialWaitScope struct {
	asyncRunIDs map[string]bool
	foreground  []initialForeground
	providerIDs map[string]bool
}

type initialForeground struct {
	runID, sessionID string
	detached         map[int]bool
}

func sortedBoolKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func supervisorYieldForScope(scope initialWaitScope, p WaitParams, deps WaitDeps, now int64) WaitResult {
	q := WaitParams{}
	if p.ID != "" {
		q.ID = p.ID
	}
	runs, err := runsForSession(q, deps, true)
	if err != nil {
		return waitResultOf(err.Error(), true, nil)
	}
	activeAsync := map[string]bool{}
	for _, r := range runs {
		activeAsync[r.ID] = true
	}
	active := map[string]bool{}
	for id := range scope.asyncRunIDs {
		if activeAsync[id] {
			active[id] = true
		}
	}
	for _, in := range scope.foreground {
		cur := deps.State.ForegroundRuns[in.runID]
		if cur == nil || cur.SessionID != in.sessionID {
			continue
		}
		for _, c := range cur.Children {
			if in.detached[c.Index] && c.Status == "detached" {
				active[in.runID] = true
			}
		}
	}
	var providers []RegisteredBackgroundWorkItem
	if len(scope.providerIDs) > 0 {
		snap, err := backgroundWorkForSession(deps, now)
		if err != nil {
			return waitResultOf(err.Error(), true, nil)
		}
		for _, it := range snap.Items {
			if scope.providerIDs[backgroundWorkIdentity(it)] {
				providers = append(providers, it)
			}
		}
	}
	return supervisorYieldResult(sortedBoolKeys(active), providers)
}

func asyncWaitUpdate(runs []AsyncRunSummary, providerCount int, elapsedMs int64) WaitResult {
	var activity []string
	for _, r := range runs {
		var active []AsyncRunStepSummary
		for _, s := range r.Steps {
			if s.Status == "pending" || s.Status == "running" {
				active = append(active, s)
			}
		}
		if len(active) == 0 {
			activity = append(activity, fmt.Sprintf("%s: %s", r.ID, r.State))
			continue
		}
		for _, s := range active {
			cur := s.CurrentTool
			if cur == "" {
				cur = "thinking…"
				if s.Status == "pending" {
					cur = "queued"
				}
			}
			path := ""
			if s.CurrentPath != "" {
				path = " " + bgShortenPath(s.CurrentPath)
			}
			activity = append(activity, fmt.Sprintf("%s: %s%s", s.Agent, cur, path))
		}
	}
	headline := strings.Join(append([]string{fmt.Sprintf("Waiting %s for %d async run(s) and %d provider item(s).", bgFormatDuration(elapsedMs), len(runs), providerCount)}, activity...), " · ")
	list := ""
	if len(runs) > 0 {
		list = formatAsyncRunList(runs, "")
	}
	var parts []string
	for _, s := range []string{headline, list} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return waitResultOf(strings.Join(parts, "\n"), false, nil)
}

func activeDetachedForegroundRuns(p WaitParams, deps WaitDeps) []ForegroundRunRecord {
	sessionID := deps.State.CurrentSessionID
	if sessionID == "" || deps.State.ForegroundRuns == nil {
		return nil
	}
	var runs []ForegroundRunRecord
	for _, id := range sortedKeys(deps.State.ForegroundRuns) {
		run := deps.State.ForegroundRuns[id]
		if run.SessionID != sessionID {
			continue
		}
		detached := false
		for _, c := range run.Children {
			detached = detached || c.Status == "detached"
		}
		if detached {
			cp := *run
			cp.Children = append([]ForegroundChildRecord(nil), run.Children...)
			runs = append(runs, cp)
		}
	}
	if p.ID != "" {
		var f []ForegroundRunRecord
		for _, r := range runs {
			if r.RunID == p.ID || strings.HasPrefix(r.RunID, p.ID) {
				f = append(f, r)
			}
		}
		return f
	}
	if p.All {
		return runs
	}
	return nil
}

func summarizeForegroundChildren(run ForegroundRunRecord, indices map[int]bool) string {
	counts := map[string]int{}
	var order []string
	for _, c := range run.Children {
		if !indices[c.Index] || c.Status == "detached" {
			continue
		}
		if counts[c.Status] == 0 {
			order = append(order, c.Status)
		}
		counts[c.Status]++
	}
	var parts []string
	for _, s := range order {
		parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
	}
	return strings.Join(parts, ", ")
}

func foregroundNeedingAttention(run ForegroundRunRecord, indices map[int]bool) []ForegroundChildRecord {
	var out []ForegroundChildRecord
	for _, c := range run.Children {
		if indices[c.Index] && c.Status == "detached" && c.ActivityState == "needs_attention" && c.CurrentTool == "contact_supervisor" {
			out = append(out, c)
		}
	}
	return out
}

func detachedIndices(run ForegroundRunRecord) map[int]bool {
	m := map[int]bool{}
	for _, c := range run.Children {
		if c.Status == "detached" {
			m[c.Index] = true
		}
	}
	return m
}

func waitForDetachedForegroundRun(run ForegroundRunRecord, signal <-chan struct{}, deps WaitDeps, startedAt int64, now func() int64, pollMs, timeoutMs int64, supervisorYield func() WaitResult) WaitResult {
	initial := detachedIndices(run)
	for {
		if deps.State.CurrentSessionID != run.SessionID {
			return waitResultOf(fmt.Sprintf("Wait stopped because the active session changed while remembered foreground run %q was still detached. Return to the originating session to inspect or wait for it. Reply to any pending supervisor request before resuming or launching a replacement.", run.RunID), true, nil)
		}
		cur := deps.State.ForegroundRuns[run.RunID]
		if cur == nil || cur.SessionID != run.SessionID {
			return waitResultOf(fmt.Sprintf("Remembered foreground run %q disappeared before a terminal child result was recorded. Completion cannot be confirmed; do not launch a replacement without checking the originating child session.", run.RunID), true, nil)
		}
		if deps.HasPendingSupervisorRequest != nil && deps.HasPendingSupervisorRequest() {
			return supervisorYield()
		}
		pending := 0
		for _, c := range cur.Children {
			if initial[c.Index] && c.Status == "detached" {
				pending++
			}
		}
		snapshot := ForegroundRunRecord{RunID: cur.RunID, SessionID: cur.SessionID, Children: append([]ForegroundChildRecord(nil), cur.Children...)}
		if attention := foregroundNeedingAttention(snapshot, initial); len(attention) > 0 {
			var names []string
			for _, c := range attention {
				names = append(names, fmt.Sprintf("%s#%d", c.Agent, c.Index))
			}
			return waitResultOf(fmt.Sprintf("Waited %s for remembered detached foreground run %q; attention required. %d child run(s) need attention: %s. Reply to any pending supervisor request, then call bg_wait({ id: %q }) again or inspect status; do not resume or launch a replacement while it remains detached.", bgFormatDuration(now()-startedAt), run.RunID, len(attention), strings.Join(names, ", "), run.RunID), false, nil)
		}
		if pending == 0 {
			outcome := summarizeForegroundChildren(snapshot, initial)
			if outcome == "" {
				outcome = "no recovered child status"
			}
			return waitResultOf(fmt.Sprintf("Waited %s for remembered detached foreground run %q; done. Outcome: %s. Completion event observed; inspect with subagent({ action: \"status\", id: %q }) for recovered output.", bgFormatDuration(now()-startedAt), run.RunID, outcome, run.RunID), false, nil)
		}
		if deps.OnUpdate != nil {
			lines := []string{fmt.Sprintf("Waiting for detached foreground run %q · %s", run.RunID, bgFormatDuration(now()-startedAt))}
			for _, c := range snapshot.Children {
				if initial[c.Index] && c.Status == "detached" {
					lines = append(lines, fmt.Sprintf("%s · working after supervisor handoff", c.Agent))
					if c.TranscriptPath == "" {
						lines = append(lines, "  live transcript unavailable; waiting for completion event")
					}
				}
			}
			deps.OnUpdate(waitResultOf(strings.Join(lines, "\n"), false, nil))
		}
		select {
		case <-signal:
			return waitResultOf(fmt.Sprintf("Wait aborted after %s. Remembered foreground run %q remains detached. Reply to any pending supervisor request before resuming or launching a replacement.", bgFormatDuration(now()-startedAt), run.RunID), true, nil)
		default:
		}
		if now()-startedAt >= timeoutMs {
			return windowElapsedResult(fmt.Sprintf("Wait window elapsed after %s with remembered foreground run %q still detached. Reply to any pending supervisor request, then call bg_wait({ id: %q }) again or inspect status; do not resume or launch a replacement while it remains detached.", bgFormatDuration(timeoutMs), run.RunID, run.RunID), []string{run.RunID}, nil)
		}
		if err := waitForWake(pollMs, signal, deps); err != nil {
			return waitResultOf(err.Error(), true, nil)
		}
	}
}

func waitForSessionDetachedForegroundRuns(runs []ForegroundRunRecord, signal <-chan struct{}, deps WaitDeps, startedAt int64, now func() int64, pollMs, timeoutMs int64, supervisorYield func() WaitResult) WaitResult {
	var texts []string
	for _, run := range runs {
		one := waitForDetachedForegroundRun(run, signal, deps, startedAt, now, pollMs, timeoutMs, supervisorYield)
		if one.IsError || (one.Wait != nil && one.Wait.Reason == "supervisor_request") {
			return one
		}
		if one.Wait != nil && one.Wait.Reason == "window_elapsed" {
			var active []string
			for _, initial := range runs {
				cur := deps.State.ForegroundRuns[initial.RunID]
				if cur == nil || cur.SessionID != initial.SessionID {
					continue
				}
				for _, c := range initial.Children {
					if c.Status != "detached" {
						continue
					}
					for _, cc := range cur.Children {
						if cc.Index == c.Index && cc.Status == "detached" {
							active = append(active, initial.RunID)
							break
						}
					}
				}
			}
			return windowElapsedResult(one.Text, dedupe(active), nil)
		}
		if t := strings.TrimSpace(one.Text); t != "" {
			texts = append(texts, t)
		}
	}
	if len(texts) == 0 {
		return waitResultOf(fmt.Sprintf("Waited %s for remembered detached foreground run(s); done.", bgFormatDuration(now()-startedAt)), false, nil)
	}
	return waitResultOf(strings.Join(texts, "\n"), false, nil)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// waitForSubagents is the bg_wait tool.
func waitForSubagents(p WaitParams, signal <-chan struct{}, deps WaitDeps) WaitResult {
	if deps.Enabled != nil && !*deps.Enabled {
		return waitResultOf("bg_wait is disabled by config.waitTool or PI_SUBAGENT_WAIT_TOOL_ENABLED; returning immediately without blocking background work. Active work keeps going, and you can inspect subagents with subagent({ action: \"status\" }) or rely on completion notifications.", false, nil)
	}
	if deps.State.CurrentSessionID == "" {
		return waitResultOf("bg_wait requires an active session identity to scope background work safely.", true, nil)
	}
	now := deps.Now
	if now == nil {
		now = nowMs
	}
	pollMs := deps.PollIntervalMs
	if pollMs == 0 {
		pollMs = defaultPollIntervalMs
	}
	pollMs = max(minPollIntervalMs, pollMs)
	timeoutMs := int64(defaultWaitTimeoutMs)
	if deps.DefaultTimeoutMs != nil {
		timeoutMs = *deps.DefaultTimeoutMs
	}
	if p.TimeoutMs != nil && *p.TimeoutMs > 0 {
		timeoutMs = *p.TimeoutMs
	}
	startedAt := now()
	sessionID := deps.State.CurrentSessionID
	waitForAll := p.ID != "" || p.All
	if p.NonBlocking && p.ID == "" {
		return waitResultOf("Non-blocking wait subscriptions require id so the registration can bind one exact run identity.", true, nil)
	}
	if p.NonBlocking && p.All {
		return waitResultOf("nonBlocking cannot be combined with all; subscribe to one exact run id.", true, nil)
	}

	active, err := runsForSession(p, deps, p.ID == "")
	if err != nil {
		return waitResultOf(err.Error(), true, nil)
	}
	foreground := activeDetachedForegroundRuns(p, deps)
	providerSnap := BackgroundWorkSnapshot{}
	if p.ID == "" {
		if providerSnap, err = backgroundWorkForSession(deps, startedAt); err != nil {
			return waitResultOf(err.Error(), true, nil)
		}
	}

	var selectedForeground *ForegroundRunRecord
	if p.ID != "" {
		type cand struct {
			kind string
			id   string
			run  AsyncRunSummary
			fg   ForegroundRunRecord
		}
		var cands []cand
		for _, r := range active {
			cands = append(cands, cand{kind: "async", id: r.ID, run: r})
		}
		for _, r := range foreground {
			cands = append(cands, cand{kind: "foreground", id: r.RunID, fg: r})
		}
		var exact []cand
		for _, c := range cands {
			if c.id == p.ID {
				exact = append(exact, c)
			}
		}
		matches := exact
		if len(exact) == 0 {
			matches = cands
		}
		if len(matches) > 1 {
			var ids []string
			for _, m := range matches {
				ids = append(ids, m.id)
			}
			return waitResultOf(fmt.Sprintf("Ambiguous subagent run id prefix %q matched %d runs: %s. Pass a longer id.", p.ID, len(matches), strings.Join(ids, ", ")), true, nil)
		}
		var sel *cand
		if len(matches) == 1 {
			sel = &matches[0]
		}
		if sel != nil && sel.kind == "async" && !isActiveAsyncState(sel.run.State) {
			var refs []string
			completions, cerr := collectScopedCompletions([]AsyncRunSummary{sel.run}, deps, &refs)
			if cerr != nil {
				return waitResultOf(cerr.Error(), true, nil)
			}
			refText := ""
			if len(refs) > 0 {
				refText = "\n" + strings.Join(refs, "\n")
			}
			return waitResultOf(fmt.Sprintf("Run %q is terminal. Outcome: %s.%s%s", sel.id, summarizeTerminalRuns([]AsyncRunSummary{sel.run}, 0), formatCompletionRecovery(completions), refText),
				deps.FailOnFailedRuns && (sel.run.State == "failed" || sel.run.State == "partial"), completions)
		}
		if sel != nil && p.NonBlocking {
			if deps.Subscribe == nil {
				return waitResultOf("Non-blocking wait subscriptions require a long-lived interactive subagent runtime; this runtime can only use blocking bg_wait calls.", true, nil)
			}
			token, _, serr := deps.Subscribe(sel.kind, sel.id, p.ID, timeoutMs)
			if serr != nil {
				return waitResultOf(serr.Error(), true, nil)
			}
			return waitResultOf(fmt.Sprintf("Armed wait subscription %s for exact %s run %s. Returning immediately; this session will be woken on completion, failure, attention, reconciliation failure, or timeout. Inspect armed subscriptions with subagent({ action: \"status\" }).", token, sel.kind, sel.id), false, nil)
		}
		if sel != nil && sel.kind == "foreground" {
			fg := sel.fg
			selectedForeground = &fg
		}
		active = nil
		if sel != nil && sel.kind == "async" {
			active = []AsyncRunSummary{sel.run}
		}
	}

	providerActive := providerSnap.Items
	scopedForeground := foreground
	if selectedForeground != nil {
		scopedForeground = []ForegroundRunRecord{*selectedForeground}
	} else if p.ID != "" {
		scopedForeground = nil
	}
	initialAsync := map[string]bool{}
	for _, r := range active {
		initialAsync[r.ID] = true
	}
	initialProvider := map[string]bool{}
	for _, it := range providerActive {
		initialProvider[backgroundWorkIdentity(it)] = true
	}
	scope := initialWaitScope{asyncRunIDs: initialAsync, providerIDs: initialProvider}
	for _, r := range scopedForeground {
		scope.foreground = append(scope.foreground, initialForeground{runID: r.RunID, sessionID: r.SessionID, detached: detachedIndices(r)})
	}
	supervisorYield := func() WaitResult { return supervisorYieldForScope(scope, p, deps, now()) }
	if selectedForeground != nil {
		return waitForDetachedForegroundRun(*selectedForeground, signal, deps, startedAt, now, pollMs, timeoutMs, supervisorYield)
	}
	if len(active) == 0 && len(providerActive) == 0 {
		if waitForAll && p.ID == "" && len(foreground) > 0 {
			return waitForSessionDetachedForegroundRuns(foreground, signal, deps, startedAt, now, pollMs, timeoutMs, supervisorYield)
		}
		if p.ID != "" {
			return waitResultOf(fmt.Sprintf("No active run matched %q. Nothing to wait for.", p.ID), false, nil)
		}
		return waitResultOf("No active async runs or registered provider work in this session. Nothing to wait for.", false, nil)
	}
	waitParams := p
	if p.ID != "" {
		waitParams.ID = active[0].ID
	}
	initialProviderNames := map[string]bool{}
	for _, it := range providerActive {
		initialProviderNames[it.Provider] = true
	}
	initialCount := len(initialAsync) + len(initialProvider)
	stopOnAttention := deps.StopOnAttention == nil || *deps.StopOnAttention
	if p.StopOnAttention != nil {
		stopOnAttention = *p.StopOnAttention
	}
	var attention []AsyncRunSummary
	for _, r := range active {
		if needsAttention(r) {
			attention = append(attention, r)
		}
	}
	supervisorBarrier := false

	isDone := func() bool {
		if deps.HasPendingSupervisorRequest != nil && deps.HasPendingSupervisorRequest() {
			supervisorBarrier = true
			return true
		}
		for _, r := range attention {
			if initialAsync[r.ID] && (stopOnAttention || hasSupervisorTool(r)) {
				return true
			}
		}
		activeAsync := map[string]bool{}
		for _, r := range active {
			activeAsync[r.ID] = true
		}
		activeProv := map[string]bool{}
		for _, it := range providerActive {
			activeProv[backgroundWorkIdentity(it)] = true
		}
		if waitForAll {
			for id := range initialAsync {
				if activeAsync[id] {
					return false
				}
			}
			for id := range initialProvider {
				if activeProv[id] {
					return false
				}
			}
			return true
		}
		for id := range initialAsync {
			if !activeAsync[id] {
				return true
			}
		}
		for id := range initialProvider {
			if !activeProv[id] {
				return true
			}
		}
		return false
	}

	for !isDone() {
		var activeInitial []AsyncRunSummary
		for _, r := range active {
			if initialAsync[r.ID] {
				activeInitial = append(activeInitial, r)
			}
		}
		var activeInitialProv []RegisteredBackgroundWorkItem
		for _, it := range providerActive {
			if initialProvider[backgroundWorkIdentity(it)] {
				activeInitialProv = append(activeInitialProv, it)
			}
		}
		var still []string
		for _, r := range activeInitial {
			still = append(still, fmt.Sprintf("%s (%s)", r.ID, r.State))
		}
		for _, it := range activeInitialProv {
			still = append(still, it.Provider+"/"+it.ID)
		}
		stillActive := strings.Join(still, ", ")
		if deps.OnUpdate != nil {
			deps.OnUpdate(asyncWaitUpdate(activeInitial, len(activeInitialProv), now()-startedAt))
		}
		select {
		case <-signal:
			return waitResultOf(fmt.Sprintf("Wait aborted after %s. Still active: %s.", bgFormatDuration(now()-startedAt), stillActive), true, nil)
		default:
		}
		if now()-startedAt >= timeoutMs {
			var ids []string
			for _, r := range activeInitial {
				ids = append(ids, r.ID)
			}
			return windowElapsedResult(fmt.Sprintf("Wait window elapsed after %s with %d async run(s) and %d provider item(s) still active: %s. The work keeps going; call bg_wait again or inspect subagent status.", bgFormatDuration(timeoutMs), len(activeInitial), len(activeInitialProv), stillActive), ids, activeInitialProv)
		}
		if err := waitForWake(pollMs, signal, deps); err != nil {
			return waitResultOf(err.Error(), true, nil)
		}
		if deps.State.CurrentSessionID != sessionID {
			return waitResultOf("Wait stopped because the active session changed.", true, nil)
		}
		if active, err = runsForSession(waitParams, deps, true); err != nil {
			return waitResultOf(err.Error(), true, nil)
		}
		if attention, err = attentionRuns(waitParams, deps, initialAsync); err != nil {
			return waitResultOf(err.Error(), true, nil)
		}
		if p.ID == "" {
			if providerSnap, err = backgroundWorkForSession(deps, now()); err != nil {
				return waitResultOf(err.Error(), true, nil)
			}
		}
		for name := range initialProviderNames {
			found := false
			for _, pn := range providerSnap.Providers {
				found = found || pn == name
			}
			if !found {
				return waitResultOf(fmt.Sprintf("Background-work provider '%s' disappeared while bg_wait was tracking its active work; completion cannot be confirmed.", name), true, nil)
			}
		}
		providerActive = providerSnap.Items
	}
	if supervisorBarrier {
		return supervisorYield()
	}

	activeProvIDs := map[string]bool{}
	for _, it := range providerActive {
		activeProvIDs[backgroundWorkIdentity(it)] = true
	}
	providerFinished := 0
	for id := range initialProvider {
		if !activeProvIDs[id] {
			providerFinished++
		}
	}
	allNow, err := runsForIDs(sortedBoolKeys(initialAsync), deps)
	if err != nil {
		return waitResultOf(err.Error(), true, nil)
	}
	var terminal []AsyncRunSummary
	for _, r := range allNow {
		if !isActiveAsyncState(r.State) && initialAsync[r.ID] {
			terminal = append(terminal, r)
		}
	}
	finishedAsync := len(terminal)
	failedAsync := 0
	for _, r := range terminal {
		if r.State == "failed" || r.State == "partial" {
			failedAsync++
		}
	}
	terminalSummary := summarizeTerminalRuns(terminal, providerFinished)
	resumeGuidance := formatResumeFirstFailedRunsNote(terminal)
	var references []string
	completions, err := collectScopedCompletions(terminal, deps, &references)
	if err != nil {
		return waitResultOf(err.Error(), true, nil)
	}

	var relevant []AsyncRunSummary
	for _, r := range attention {
		if initialAsync[r.ID] {
			relevant = append(relevant, r)
		}
	}
	supervisorHint := ""
	for _, r := range relevant {
		if hasSupervisorTool(r) {
			supervisorHint = " Reply to any pending supervisor request. If subagent_supervisor({ action: \"pending\" }) is empty, check intercom({ action: \"pending\" }) because an external intercom tool may own the request."
		}
	}
	attentionNote := ""
	if len(relevant) > 0 {
		var ids []string
		for _, r := range relevant {
			ids = append(ids, r.ID)
		}
		attentionNote = fmt.Sprintf(" %d run(s) need attention: %s —%s inspect with subagent({ action: \"status\" }) then steer a top-level live async child, resume a paused/completed/failed child, or interrupt explicitly.", len(relevant), strings.Join(ids, ", "), supervisorHint)
	}
	stillRunning := 0
	for _, r := range active {
		if initialAsync[r.ID] {
			stillRunning++
		}
	}
	for _, it := range providerActive {
		if initialProvider[backgroundWorkIdentity(it)] {
			stillRunning++
		}
	}
	elapsed := bgFormatDuration(now() - startedAt)
	outcome := ""
	if terminalSummary != "" {
		outcome = " Outcome: " + terminalSummary + "."
	}
	recoveryNote := formatCompletionRecovery(completions)
	if len(references) > 0 {
		recoveryNote += "\n" + strings.Join(references, "\n") + "\n"
	}

	if waitForAll {
		var fgResult *WaitResult
		if p.ID == "" && len(foreground) > 0 && len(relevant) == 0 {
			r := waitForSessionDetachedForegroundRuns(foreground, signal, deps, startedAt, now, pollMs, timeoutMs, supervisorYield)
			fgResult = &r
		}
		if fgResult != nil && (fgResult.IsError || (fgResult.Wait != nil && (fgResult.Wait.Reason == "supervisor_request" || fgResult.Wait.Reason == "window_elapsed"))) {
			return *fgResult
		}
		fgNote := ""
		if fgResult != nil {
			fgNote = "\n" + fgResult.Text
		}
		var scopeText string
		switch {
		case p.ID != "":
			scopeText = fmt.Sprintf("run %q", p.ID)
		case len(initialProvider) == 0:
			scopeText = fmt.Sprintf("%d async run(s)", len(initialAsync))
		default:
			scopeText = fmt.Sprintf("%d async run(s) and %d provider item(s)", len(initialAsync), len(initialProvider))
		}
		status := "done"
		if len(relevant) > 0 {
			status = "attention required"
		}
		return waitResultOf(fmt.Sprintf("Waited %s for %s; %s.%s%s%s%s%s Completion/control events have been observed; inspect status if a notification is not visible yet.", elapsed, scopeText, status, outcome, recoveryNote, resumeGuidance, attentionNote, fgNote),
			(deps.FailOnFailedRuns && failedAsync > 0) || (deps.FailOnAttention && len(relevant) > 0), completions)
	}

	finishedCount := finishedAsync + providerFinished
	subject := "run(s)"
	if len(initialProvider) != 0 {
		subject = "item(s)"
	}
	var remainder string
	switch {
	case stillRunning > 0:
		remainder = fmt.Sprintf(" %d %s still in flight — call bg_wait again to catch the next one.", stillRunning, subject)
	case len(relevant) > 0:
		remainder = " No other work is waitable until attention is handled."
	case len(initialProvider) == 0:
		remainder = " No runs remain in flight."
	default:
		remainder = " No work remains in flight."
	}
	progress := fmt.Sprintf("%d of %d %s finished", finishedCount, initialCount, subject)
	if len(relevant) > 0 && finishedCount == 0 {
		progress = fmt.Sprintf("%d of %d %s need attention", len(relevant), initialCount, subject)
	}
	return waitResultOf(fmt.Sprintf("Waited %s; %s.%s%s%s%s%s Relevant completion/control events have been observed; inspect status if a notification is not visible yet.", elapsed, progress, outcome, recoveryNote, resumeGuidance, attentionNote, remainder),
		(deps.FailOnFailedRuns && failedAsync > 0) || (deps.FailOnAttention && len(relevant) > 0), completions)
}

// ── auto-drain.ts ──

// AutoDrainDeps are the seams of the headless drain.
type AutoDrainDeps struct {
	State                       *SubagentState
	Events                      WaitEventBus
	AsyncDirRoot, ResultsDir    string
	NestedRootRunID             string
	TimeoutMs                   *int64
	Now                         func() int64
	Wait                        func(p WaitParams, signal <-chan struct{}, deps WaitDeps) WaitResult
	HasWork                     func(sessionID string, nowMs int64) (bool, error)
	HasPendingSupervisorRequest func() bool
}

func hasOutstandingWork(deps AutoDrainDeps, sessionID string, nowMs int64) (bool, error) {
	for _, scope := range waitRunScopes(deps.AsyncDirRoot, deps.ResultsDir, deps.NestedRootRunID) {
		runs, err := listAsyncRuns(scope.AsyncDirRoot, AsyncRunListOptions{States: activeWaitStates, SessionID: sessionID, ResultsDir: scope.ResultsDir, Now: func() int64 { return nowMs }})
		if err != nil {
			return false, err
		}
		if len(runs) > 0 {
			return true, nil
		}
	}
	snap, err := snapshotBackgroundWork(sessionID, nowMs)
	if err != nil {
		return false, err
	}
	if len(snap.Items) > 0 {
		return true, nil
	}
	for _, run := range deps.State.ForegroundRuns {
		if run.SessionID != sessionID {
			continue
		}
		for _, c := range run.Children {
			if c.Status == "detached" {
				return true, nil
			}
		}
	}
	return false, nil
}

// drainOutstandingWork drains all work owned by the current headless session, including work added while draining.
func drainOutstandingWork(deps AutoDrainDeps) error {
	sessionID := deps.State.CurrentSessionID
	if sessionID == "" {
		return errors.New("Cannot auto-drain background work without an active session identity.")
	}
	now := deps.Now
	if now == nil {
		now = nowMs
	}
	timeoutMs := int64(defaultAutoDrainTimeoutMs)
	if deps.TimeoutMs != nil {
		timeoutMs = *deps.TimeoutMs
	}
	if timeoutMs <= 0 {
		return errors.New("Auto-drain timeoutMs must be a positive finite number.")
	}
	deadlineAt := now() + timeoutMs
	hasWork := deps.HasWork
	if hasWork == nil {
		hasWork = func(id string, t int64) (bool, error) { return hasOutstandingWork(deps, id, t) }
	}
	wait := deps.Wait
	if wait == nil {
		wait = waitForSubagents
	}
	for {
		if deps.State.CurrentSessionID != sessionID {
			return errors.New("Auto-drain stopped because the active session changed.")
		}
		if deps.HasPendingSupervisorRequest != nil && deps.HasPendingSupervisorRequest() {
			return nil
		}
		work, err := hasWork(sessionID, now())
		if err != nil {
			return err
		}
		if !work {
			return nil
		}
		remaining := deadlineAt - now()
		if remaining <= 0 {
			return fmt.Errorf("Auto-drain timed out after %dms with background work still active in session '%s'.", timeoutMs, sessionID)
		}
		off := false
		res := wait(WaitParams{All: true, TimeoutMs: &remaining}, nil, WaitDeps{State: deps.State, AsyncDirRoot: deps.AsyncDirRoot, ResultsDir: deps.ResultsDir, NestedRootRunID: deps.NestedRootRunID,
			Events: deps.Events, Now: now, StopOnAttention: &off, FailOnFailedRuns: true, FailOnAttention: true, HasPendingSupervisorRequest: deps.HasPendingSupervisorRequest})
		if res.IsError {
			detail := strings.TrimSpace(res.Text)
			if detail == "" {
				detail = "bg_wait returned an error without details"
			}
			return fmt.Errorf("Auto-drain failed for session '%s': %s.", sessionID, detail)
		}
		if res.Wait != nil && res.Wait.Reason == "supervisor_request" {
			return nil
		}
		if deps.HasPendingSupervisorRequest != nil && deps.HasPendingSupervisorRequest() {
			return nil
		}
	}
}

// ── resume-guidance.ts (the parts that need no recovery descriptor reader are here; see formatAsyncReviveCommand) ──

const intercomDetachError = "detached for intercom coordination"

func isIntercomDetached(run AsyncRunSummary) bool {
	if strings.Contains(strings.ToLower(run.Error), intercomDetachError) {
		return true
	}
	for _, s := range run.Steps {
		if strings.Contains(strings.ToLower(s.Error), intercomDetachError) {
			return true
		}
		if ex := s.Extra["execution"]; strings.Contains(string(ex), `"status":"detached"`) {
			return true
		}
	}
	return false
}

func formatIntercomDetachGuidance(run AsyncRunSummary) string {
	if !isIntercomDetached(run) {
		return ""
	}
	return fmt.Sprintf("Run %q detached for intercom coordination. Reply to the supervisor request first, then wait with bg_wait({ id: %q }). Use subagent({ action: \"status\", id: %q }) to recover the result; do not resume or launch a replacement while it remains detached.", run.ID, run.ID, run.ID)
}

// formatAsyncReviveCommand is the resume call for a failed run with a persisted child session and a matching recovery descriptor.
func formatAsyncReviveCommand(run AsyncRunSummary) string {
	var step *AsyncRunStepSummary
	for i := range run.Steps {
		if run.Steps[i].Status == "failed" {
			step = &run.Steps[i]
			break
		}
	}
	if step == nil {
		return ""
	}
	sessionFile := step.SessionFile
	if sessionFile == "" && len(run.Steps) == 1 {
		sessionFile = run.SessionFile
	}
	if sessionFile == "" || !regularFileExists(sessionFile) {
		return ""
	}
	desc, err := readAsyncRecoveryDescriptor(run.AsyncDir)
	if err != nil || desc == nil || desc.SourceRunID != run.ID || desc.Agent != step.Agent {
		return ""
	}
	index := ""
	if len(run.Steps) != 1 {
		index = fmt.Sprintf(", index: %d", step.Index)
	}
	return fmt.Sprintf("subagent({ action: \"resume\", id: %q%s, message: \"Continue from the persisted child session and report the result.\" })", run.ID, index)
}

func formatResumeFirstFailedRunsNote(runs []AsyncRunSummary) string {
	var failed []AsyncRunSummary
	for _, r := range runs {
		if r.State == "failed" || r.State == "partial" {
			failed = append(failed, r)
		}
	}
	var detach []string
	for _, r := range failed {
		if g := formatIntercomDetachGuidance(r); g != "" {
			detach = append(detach, g)
		}
	}
	type entry struct {
		run     AsyncRunSummary
		command string
	}
	var resumable []entry
	for _, r := range failed {
		if isIntercomDetached(r) {
			continue
		}
		if c := formatAsyncReviveCommand(r); c != "" {
			resumable = append(resumable, entry{r, c})
		}
	}
	guidance := ""
	switch len(resumable) {
	case 0:
	case 1:
		guidance = fmt.Sprintf(" Resume-first: failed run %q has a persisted child session. Revive the original run with %s before reporting failure or launching a replacement. Launch a replacement only if revive fails or the user explicitly asks for one.", resumable[0].run.ID, resumable[0].command)
	default:
		guidance = fmt.Sprintf(" Resume-first: %d failed runs have persisted child sessions. Inspect status and revive each original run before reporting failure or launching a replacement. Launch a replacement only if revive fails or the user explicitly asks for one.", len(resumable))
	}
	d := ""
	if len(detach) > 0 {
		d = " " + strings.Join(detach, " ")
	}
	return d + guidance
}
