package agents

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/settings"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/src/task/index.ts, types.ts, structured-subagent.ts

// AgentJob wraps a running or settled background subagent as a jobs.Job and
// implements jobs.JobDetailer so internal/jobs' single delivery listener,
// `wait`, and `proc://` share one source of truth for delivery, consumption,
// timing, and progress details.
type AgentJob struct {
	mu           sync.Mutex
	id           string
	agentURLID   string
	label        string
	started      time.Time
	ended        time.Time
	done         chan struct{}
	doneOnce     sync.Once
	status       string // "running", "completed", "failed", "cancelled"
	output       string
	result       string
	errorText    string
	cancel       context.CancelFunc
	progress     AgentProgress
	singleResult *SingleResult
	evalData     any
	hasEvalData  bool
	published    *bool
	widget       *jobs.FallbackWidgetTracker
}

// Compile-time assertions: AgentJob implements jobs.Job, jobs.JobDetailer, and jobs.WidgetProvider.
var (
	_ jobs.Job            = (*AgentJob)(nil)
	_ jobs.JobDetailer    = (*AgentJob)(nil)
	_ jobs.WidgetProvider = (*AgentJob)(nil)
)

// NewAgentJob constructs a running AgentJob.
func NewAgentJob(id, label string, cancel context.CancelFunc, initial AgentProgress) *AgentJob {
	started := time.Now()
	return &AgentJob{
		id:         id,
		agentURLID: id,
		label:      label,
		started:    started,
		done:       make(chan struct{}),
		status:     "running",
		cancel:     cancel,
		progress:   initial,
		widget:     jobs.NewFallbackWidgetTracker(started),
	}
}

func (j *AgentJob) ID() string            { return j.id }
func (j *AgentJob) Kind() jobs.Kind       { return jobs.KindAgent }
func (j *AgentJob) Label() string         { return j.label }
func (j *AgentJob) Started() time.Time    { return j.started }
func (j *AgentJob) Done() <-chan struct{} { return j.done }

func (j *AgentJob) Status() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status
}

func (j *AgentJob) Output() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.output
}

func (j *AgentJob) Result() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.result
}

// Ended implements jobs.JobDetailer.
func (j *AgentJob) Ended() time.Time {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.ended
}

// ErrorText implements jobs.JobDetailer.
func (j *AgentJob) ErrorText() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.errorText
}

// Command implements jobs.JobDetailer (empty for agent jobs).
func (j *AgentJob) Command() string { return "" }

// Intent implements jobs.JobDetailer.
func (j *AgentJob) Intent() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.progress.LastIntent
}

// Details implements jobs.JobDetailer.
func (j *AgentJob) Details() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	det := map[string]any{
		"agentId":  j.id,
		"progress": []AgentProgress{j.progress.clone()},
	}
	if j.progress.ResolvedModel != "" {
		det["resolvedModel"] = j.progress.ResolvedModel
	}
	if j.progress.ResolvedModelIdentity != "" {
		det["resolvedModelIdentity"] = j.progress.ResolvedModelIdentity
	}
	if j.progress.ResolvedThinkingLevel != "" {
		det["resolvedThinkingLevel"] = j.progress.ResolvedThinkingLevel
	}
	if j.status != "running" {
		ev := map[string]any{
			"text": j.result,
		}
		if j.hasEvalData {
			ev["data"] = j.evalData
		}
		if j.singleResult != nil {
			if j.singleResult.PatchPath != "" {
				ev["patchPath"] = j.singleResult.PatchPath
			}
			if j.singleResult.BranchName != "" {
				ev["branchName"] = j.singleResult.BranchName
			}
			if j.singleResult.HasRootChanges != nil {
				ev["hasRootChanges"] = *j.singleResult.HasRootChanges
			}
		}
		det["evalResult"] = ev
	}
	return det
}

// SkipDelivery implements jobs.JobDetailer. Watch suppression now lives in
// internal/jobs.Watch for every job kind, so AgentJob does not maintain its own
// per-type skip flag.
func (j *AgentJob) SkipDelivery() bool { return false }
func (j *AgentJob) Cancel() {
	j.mu.Lock()
	cancel := j.cancel
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (j *AgentJob) updateProgress(p AgentProgress, outputText string) {
	j.mu.Lock()
	j.progress = p.clone()
	if outputText != "" {
		j.output = outputText
	}
	j.mu.Unlock()
}

// RecordToolCall feeds one finished subagent tool result into the job's fallback
// widget tracker, matching omp's `appendToolResult` (`task/widget-inputs.ts:25-29`).
func (j *AgentJob) RecordToolCall(tool, intent, text string) {
	j.mu.Lock()
	w := j.widget
	j.mu.Unlock()
	if w != nil {
		w.RecordToolCall(tool, intent, text, time.Now())
	}
}

// WidgetView implements jobs.WidgetProvider, returning omp's deterministic
// fallback `JobWidgetView` for this task job (`job-widgets/runtime.ts:640-688`,
// `job-widgets/registry.ts:225-238`, `task/widget-inputs.ts`).
func (j *AgentJob) WidgetView(now time.Time) *jobs.WidgetView {
	j.mu.Lock()
	w := j.widget
	displayLabel := j.label
	if j.progress.LastIntent != "" {
		displayLabel = j.progress.LastIntent
	}
	settled := j.status != "running"
	started := j.started
	j.mu.Unlock()
	if w == nil {
		return jobs.BuildFallbackWidget(displayLabel, "", started)
	}
	return w.View(displayLabel, "", false, settled, now)
}

func (j *AgentJob) finish(status, resultText, errorText string, sr *SingleResult, evalData any, hasEvalData bool, published *bool) {
	j.mu.Lock()
	j.status = status
	j.result = resultText
	j.output = resultText
	j.errorText = errorText
	j.singleResult = sr
	j.evalData = evalData
	j.hasEvalData = hasEvalData
	if published != nil {
		v := *published
		j.published = &v
	} else {
		j.published = nil
	}
	j.ended = time.Now()
	// Keep j.progress synchronized with the settled result so AgentJob.Details()
	// (consumed by renderers via nvRefreshAsyncProgress) never shows stale
	// in-flight figures when no OnProgress fired after the last turn.
	// upstream: packages/coding-agent/src/task/index.ts:1628-1656
	j.applySettledProgressLocked(status, sr)
	j.mu.Unlock()
	j.doneOnce.Do(func() { close(j.done) })
}

// applySettledProgressLocked mirrors omp's final progress update in
// `task/index.ts:1628-1656,1668-1672` so a settled AgentJob's `progress[0]`
// matches the final SingleResult (or job status/duration when sr is nil).
func (j *AgentJob) applySettledProgressLocked(jobStatus string, sr *SingleResult) {
	if sr != nil {
		if sr.Aborted || jobStatus == "cancelled" {
			j.progress.Status = "aborted"
		} else if jobStatus == "failed" || sr.ExitCode != 0 || sr.Error != "" {
			j.progress.Status = "failed"
		} else {
			j.progress.Status = "completed"
		}
		j.progress.DurationMs = sr.DurationMs
		j.progress.Tokens = sr.Tokens
		j.progress.Requests = sr.Requests
		j.progress.ContextTokens = sr.ContextTokens
		j.progress.ContextWindow = sr.ContextWindow
		if sr.Usage != nil {
			j.progress.Cost = sr.Usage.Cost.Total
		} else {
			j.progress.Cost = 0
		}
		if sr.LastIntent != "" {
			j.progress.LastIntent = sr.LastIntent
		}
		j.progress.ExtractedToolData = sr.ExtractedToolData
		j.progress.RetryFailure = sr.RetryFailure
		j.progress.RetryState = nil
		j.progress.Streaming = nil
		j.progress.CurrentTool = ""
		j.progress.CurrentToolArgs = ""
		j.progress.CurrentToolArgsKey = ""
		j.progress.CurrentToolIntent = ""
		j.progress.CurrentToolStartMs = 0
		if sr.ModelRole != "" {
			j.progress.ModelRole = sr.ModelRole
		}
		if sr.ResolvedModel != "" {
			j.progress.ResolvedModel = sr.ResolvedModel
			j.progress.ResolvedModelIdentity = sr.ResolvedModelIdentity
			j.progress.ResolvedThinkingLevel = sr.ResolvedThinkingLevel
		} else {
			j.progress.ResolvedModel = ""
			j.progress.ResolvedModelIdentity = ""
			j.progress.ResolvedThinkingLevel = ""
		}
		if out := strings.TrimSpace(sr.Output); out != "" {
			lines := strings.Split(out, "\n")
			if len(lines) > 8 {
				lines = lines[len(lines)-8:]
			}
			j.progress.RecentOutput = lines
		}
		j.progress = j.progress.clone()
		return
	}

	switch jobStatus {
	case "cancelled":
		j.progress.Status = "aborted"
	case "failed":
		j.progress.Status = "failed"
	case "completed":
		j.progress.Status = "completed"
	}
	if !j.ended.IsZero() && !j.started.IsZero() {
		if ms := max(int64(0), j.ended.Sub(j.started).Milliseconds()); ms > j.progress.DurationMs {
			j.progress.DurationMs = ms
		}
	}
	j.progress.RetryState = nil
	j.progress.Streaming = nil
	j.progress.CurrentTool = ""
	j.progress.CurrentToolArgs = ""
	j.progress.CurrentToolArgsKey = ""
	j.progress.CurrentToolIntent = ""
	j.progress.CurrentToolStartMs = 0
	j.progress = j.progress.clone()
}

func (j *AgentJob) snapshot(sessionID string, now time.Time) JobSnapshot {
	return j.snapshotWithResult(now, !jobs.IsConsumed(sessionID, j.id))
}

func (j *AgentJob) snapshotWithResult(now time.Time, includeResult bool) JobSnapshot {
	j.mu.Lock()
	end := j.ended
	if end.IsZero() {
		end = now
	}
	urlID := j.agentURLID
	if urlID == "" {
		urlID = j.id
	}
	displayLabel := j.label
	if j.progress.LastIntent != "" {
		displayLabel = j.progress.LastIntent
	}
	settled := j.status != "running"
	w := j.widget
	started := j.started
	snap := JobSnapshot{
		ID:                    j.id,
		Type:                  "task",
		Status:                j.status,
		Label:                 j.label,
		Intent:                j.progress.LastIntent,
		DurationMs:            max(int64(0), end.Sub(j.started).Milliseconds()),
		ResolvedModel:         j.progress.ResolvedModel,
		ResolvedModelIdentity: j.progress.ResolvedModelIdentity,
		ResolvedThinkingLevel: j.progress.ResolvedThinkingLevel,
		AgentURLID:            urlID,
	}
	if !j.ended.IsZero() {
		snap.SettledAtMs = j.ended.UnixMilli()
	}
	if j.status == "running" {
		snap.Requests = j.progress.Requests
		snap.Cost = j.progress.Cost
		snap.ContextTokens = j.progress.ContextTokens
		snap.ContextWindow = j.progress.ContextWindow
	}
	if includeResult {
		snap.ResultText = j.result
		snap.ErrorText = j.errorText
		if j.singleResult != nil {
			snap.Structured = j.singleResult.StructuredOutput
		}
	}
	if j.published != nil {
		pub := *j.published
		snap.AgentOutputPublished = &pub
	}
	j.mu.Unlock()
	if w != nil {
		snap.Widget = w.View(displayLabel, "", false, settled, now)
	} else {
		snap.Widget = jobs.BuildFallbackWidget(displayLabel, "", started)
	}
	return snap
}

// Runtime holds all state for the agents area: registry, concurrency semaphore,
// per-session output ID managers, workpools, and wait tracking.
type Runtime struct {
	Config   Config
	Registry *Registry
	// UseSettings enables reading live settings from `internal/settings.StoreFor(ctx)`.
	// True for defaultRuntime (production); false for NewRuntime(cfg) unless enabled,
	// so unit tests that pass an explicit Config keep deterministic overrides.
	UseSettings bool

	mu              sync.Mutex
	sem             *Semaphore
	outputManagers  map[string]*OutputManager
	sessionArtDirs  map[string]sessionArtEntry
	lifecycleShown  map[string]bool
	sessionCancels  map[string][]context.CancelFunc
	sessionContexts map[string]sdk.Context
	workpools       map[string]*WorkPool
	waitStreaks     map[string]*waitIdleStreak
	taskToolSigs    map[string]string
	// DiscoverFn can be overridden in tests.
	DiscoverFn func(opts DiscoverOptions) DiscoveryResult
}

type sessionArtEntry struct {
	dir       string
	temp      bool
	releaseFn func()
}

type waitIdleStreak struct {
	endedAt  time.Time
	waitedMs int64
}

// NewRuntime constructs an isolated agents Runtime.
func NewRuntime(cfg Config) *Runtime {
	return &Runtime{
		Config:          cfg,
		Registry:        NewRegistry(),
		sem:             NewSemaphore(cfg.MaxConcurrency),
		outputManagers:  map[string]*OutputManager{},
		sessionArtDirs:  map[string]sessionArtEntry{},
		lifecycleShown:  map[string]bool{},
		sessionCancels:  map[string][]context.CancelFunc{},
		sessionContexts: map[string]sdk.Context{},
		workpools:       map[string]*WorkPool{},
		waitStreaks:     map[string]*waitIdleStreak{},
		taskToolSigs:    map[string]string{},
		DiscoverFn:      DiscoverAgents,
	}
}

func newDefaultRuntime() *Runtime {
	rt := NewRuntime(DefaultConfig())
	rt.UseSettings = true
	return rt
}

var defaultRuntime = newDefaultRuntime()

// DefaultRuntime returns the package-level Runtime registered with the extension.
func DefaultRuntime() *Runtime { return defaultRuntime }

// EffectiveConfig builds the live Config for ctx by overlaying `internal/settings`
// onto rt.Config (when rt.UseSettings is true) and resizing the concurrency
// semaphore in place.
// upstream: packages/coding-agent/src/task/settings.ts, task/index.ts:986-993
func (rt *Runtime) EffectiveConfig(ctx sdk.Context) Config {
	cfg := rt.Config
	if !rt.UseSettings {
		return cfg
	}
	var zero sdk.Context
	if ctx == zero {
		return cfg
	}
	st := settings.StoreFor(ctx)
	if st == nil {
		return cfg
	}
	cfg.AsyncEnabled = st.Bool("async.enabled")
	cfg.BatchEnabled = st.Bool("task.batch")
	cfg.Structured = st.Bool("task.structured")
	cfg.StepsEnabled = st.Bool("task.steps")
	cfg.EnableEffort = st.Bool("task.enableEffort")
	if me := st.String("task.maxEffort"); me != "" {
		cfg.MaxEffort = me
	}
	cfg.IsolationEnabled = st.Bool("task.isolation.enabled")
	cfg.IsolationApply = st.Bool("task.isolation.apply")
	if m := strings.TrimSpace(st.String("task.isolation.merge")); m != "" {
		cfg.IsolationMerge = m
	}
	if c := strings.TrimSpace(st.String("task.isolation.commits")); c != "" {
		cfg.IsolationCommits = c
	}
	if b := strings.TrimSpace(st.String("isolation.backend")); b != "" {
		cfg.IsolationBackend = b
	}
	cfg.MaxConcurrency = st.Int("task.maxConcurrency")
	cfg.MaxRecursionDepth = st.Int("task.maxRecursionDepth")
	cfg.MaxRuntimeMs = st.Int("task.maxRuntimeMs")
	cfg.SoftRequestBudget = st.Int("task.softRequestBudget")
	cfg.SoftRequestBudgetNotice = st.Bool("task.softRequestBudgetNotice")
	cfg.AgentIdleTTLMs = st.Int("task.agentIdleTtlMs")
	cfg.DisabledAgents = st.Strings("task.disabledAgents")
	cfg.CrossInstance = st.Bool("task.crossInstance")
	if rawEvalTools, ok := st.Any("eval.tools.enabled").(bool); ok {
		cfg.EvalToolsEnabled = rawEvalTools
	}
	if opPCM, ok := st.GetOperator("task.perCallModel"); ok {
		if s, ok := opPCM.(string); ok && s != "" {
			cfg.PerCallModel = s
		}
	} else if s := st.String("task.perCallModel"); s != "" {
		cfg.PerCallModel = s
	}
	if rawOv := st.Any("task.agentModelOverrides"); rawOv != nil {
		if m := parseModelOverridesSetting(rawOv); len(m) > 0 {
			cfg.ModelOverrides = m
		}
	}
	if rawBudgets := st.Any("task.agentSoftRequestBudget"); rawBudgets != nil {
		if m := parseAgentBudgetSetting(rawBudgets); len(m) > 0 {
			cfg.AgentSoftRequestBudget = m
		}
	}
	rt.sem.Resize(cfg.MaxConcurrency)
	return cfg
}

func parseModelOverridesSetting(raw any) map[string][]string {
	out := map[string][]string{}
	var iter func(k string, v any)
	iter = func(k string, v any) {
		switch val := v.(type) {
		case string:
			if s := strings.TrimSpace(val); s != "" {
				out[k] = []string{s}
			}
		case []string:
			if len(val) > 0 {
				out[k] = append([]string(nil), val...)
			}
		case []any:
			var list []string
			for _, item := range val {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					list = append(list, strings.TrimSpace(s))
				}
			}
			if len(list) > 0 {
				out[k] = list
			}
		}
	}
	switch m := raw.(type) {
	case map[string]any:
		for k, v := range m {
			iter(k, v)
		}
	case *settings.OrderedMap:
		if m != nil {
			for _, k := range m.Keys {
				iter(k, m.Values[k])
			}
		}
	}
	return out
}

func parseAgentBudgetSetting(raw any) map[string]int {
	out := map[string]int{}
	add := func(k string, v any) {
		if n, ok := asInt(v); ok {
			out[k] = n
		}
	}
	switch m := raw.(type) {
	case map[string]any:
		for k, v := range m {
			add(k, v)
		}
	case *settings.OrderedMap:
		if m != nil {
			for _, k := range m.Keys {
				add(k, m.Values[k])
			}
		}
	}
	return out
}

// BindSessionContext records the long-lived sdk.Context from `session_start` for
// sessionID so background subagents and workpools outliving their originating
// tool call never depend on a cancelled request context.
func (rt *Runtime) BindSessionContext(sessionID string, ctx sdk.Context) {
	var zero sdk.Context
	if sessionID == "" || ctx == zero {
		return
	}
	rt.mu.Lock()
	rt.sessionContexts[sessionID] = ctx
	rt.mu.Unlock()
}

// sessionContextFor returns the long-lived session_start Context for sessionID,
// falling back to fallback when session_start was not seen (e.g. unit tests).
func (rt *Runtime) sessionContextFor(sessionID string, fallback sdk.Context) sdk.Context {
	if sessionID != "" {
		rt.mu.Lock()
		c, ok := rt.sessionContexts[sessionID]
		rt.mu.Unlock()
		var zero sdk.Context
		if ok && c != zero && c.Err() == nil {
			return c
		}
	}
	return fallback
}

func (rt *Runtime) discover(cwd string) DiscoveryResult {
	fn := rt.DiscoverFn
	if fn == nil {
		fn = DiscoverAgents
	}
	return fn(DiscoverOptions{Cwd: cwd})
}

// resolveSessionID extracts the PiG session ID from ctx, returning "" if unavailable.
func (rt *Runtime) resolveSessionID(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	if id, err := ctx.SessionManager().GetSessionID(); err == nil && id != "" {
		return id
	}
	return ""
}

func (rt *Runtime) sessionKey(ctx sdk.Context) string {
	if id := rt.resolveSessionID(ctx); id != "" {
		return id
	}
	return "_default"
}
func (rt *Runtime) sessionArtifactsDir(ctx sdk.Context) (string, bool) {
	sessKey := rt.sessionKey(ctx)
	var zero sdk.Context
	if ctx != zero {
		if sf, err := ctx.SessionManager().GetSessionFile(); err == nil && sf != nil && *sf != "" {
			dir := artifactsDirForSession(*sf)
			_ = os.MkdirAll(dir, 0o755)
			return dir, false
		}
	}
	rt.mu.Lock()
	if ent, ok := rt.sessionArtDirs[sessKey]; ok && ent.dir != "" {
		rt.mu.Unlock()
		_ = os.MkdirAll(ent.dir, 0o755)
		return ent.dir, ent.temp
	}
	rt.mu.Unlock()

	dir, err := makeOmpTempArtifactsDir("omp-task")
	if err != nil {
		return "", false
	}
	release := rt.Registry.RegisterArtifactsDirForSession(sessKey, dir)
	rt.mu.Lock()
	if ent, ok := rt.sessionArtDirs[sessKey]; ok && ent.dir != "" {
		rt.mu.Unlock()
		release()
		_ = os.RemoveAll(dir)
		return ent.dir, ent.temp
	}
	rt.sessionArtDirs[sessKey] = sessionArtEntry{dir: dir, temp: true, releaseFn: release}
	rt.mu.Unlock()
	return dir, true
}

// makeOmpTempArtifactsDir creates `<os.TempDir()>/<prefix>-<16 hex>` matching
// omp's `omp-task-${Snowflake.next()}` directory naming
// (`task/structured-subagent.ts:714`, `packages/utils/src/snowflake.ts:46`).
func makeOmpTempArtifactsDir(prefix string) (string, error) {
	base := os.TempDir()
	for range 16 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		dir := filepath.Join(base, prefix+"-"+hex.EncodeToString(b[:]))
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return dir, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
	}
	return os.MkdirTemp(base, prefix+"-*")
}

func (rt *Runtime) outputManager(sessionKey string, artifactsDir string, crossInstance bool) *OutputManager {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if m, ok := rt.outputManagers[sessionKey]; ok {
		m.SetCrossInstance(crossInstance)
		return m
	}
	m := NewOutputManager(func() string { return artifactsDir }, "", crossInstance)
	rt.outputManagers[sessionKey] = m
	return m
}

func (rt *Runtime) trackCancel(sessionKey string, cancel context.CancelFunc) {
	rt.mu.Lock()
	rt.sessionCancels[sessionKey] = append(rt.sessionCancels[sessionKey], cancel)
	rt.mu.Unlock()
}

// ensureMainRef registers or refreshes the Main AgentRef for sessKey with an
// OnDeliver callback bound to longLivedCtx so `write agent://Main` delivers
// directly into the parent session via `SendMessage("irc:incoming", ...)`.
// upstream: packages/coding-agent/src/session/irc-bridge.ts:188-292
func (rt *Runtime) ensureMainRef(sessKey string, longLivedCtx sdk.Context, artifactsDir string) {
	var zero sdk.Context
	var sessFile string
	if longLivedCtx != zero {
		if sf, err := longLivedCtx.SessionManager().GetSessionFile(); err == nil && sf != nil {
			sessFile = *sf
		}
	}
	onDeliver := func(msg IRCMessage) IrcDeliveryReceipt {
		targetCtx := rt.sessionContextFor(sessKey, longLivedCtx)
		if targetCtx == zero || targetCtx.Err() != nil {
			return IrcDeliveryReceipt{To: MainAgentID, Outcome: "failed", Error: "no live parent session context"}
		}
		idle, idleErr := targetCtx.IsIdle()
		streaming := idleErr == nil && !idle
		quiet := msg.Broadcast || msg.Progress
		formatted := FormatIncomingIRCWithOptions(msg, false, streaming && !quiet)
		trigger := true
		deliverAs := "followUp"
		if streaming && !quiet {
			deliverAs = "steer"
		}
		details := map[string]any{
			"id":      msg.ID,
			"from":    msg.From,
			"message": msg.Body,
		}
		if msg.ReplyTo != "" {
			details["replyTo"] = msg.ReplyTo
		}
		if msg.WakeRelay {
			details["wakeRelay"] = true
		}
		if msg.Broadcast {
			details["broadcast"] = true
		}
		if msg.Progress {
			details["progress"] = true
		}
		err := targetCtx.SendMessage("irc:incoming", formatted, true, sdk.SendMessageOptions{
			TriggerTurn: &trigger,
			DeliverAs:   deliverAs,
			Details:     details,
		})
		if err != nil {
			return IrcDeliveryReceipt{To: MainAgentID, Outcome: "failed", Error: err.Error()}
		}
		if streaming {
			return IrcDeliveryReceipt{To: MainAgentID, Outcome: "injected"}
		}
		return IrcDeliveryReceipt{To: MainAgentID, Outcome: "woken"}
	}
	mainRef := &AgentRef{
		ID:          MainAgentID,
		DisplayName: "main",
		Kind:        "main",
		Status:      "running",
		SessionID:   sessKey,
		SessionFile: sessFile,
		OutputDir:   artifactsDir,
		OnDeliver:   onDeliver,
	}
	rt.Registry.RegisterMain(sessKey, mainRef)
}

// EndSession releases ONLY sessionID's state (cancels its subagents and
// workpools, removes its temp artifacts dir, clears its per-session maps, and
// unbinds its session context). Safe to call repeatedly or for an unknown/empty
// sessionID; never touches any other session.
func (rt *Runtime) EndSession(sessionID string) {
	if sessionID == "" {
		return
	}
	rt.mu.Lock()
	cancels := rt.sessionCancels[sessionID]
	delete(rt.sessionCancels, sessionID)
	delete(rt.sessionContexts, sessionID)
	delete(rt.outputManagers, sessionID)
	delete(rt.lifecycleShown, sessionID)
	delete(rt.waitStreaks, sessionID)
	delete(rt.taskToolSigs, sessionID)
	artEntry, hasArt := rt.sessionArtDirs[sessionID]
	delete(rt.sessionArtDirs, sessionID)
	prefix := sessionID + "\x00"
	var pools []*WorkPool
	for k, wp := range rt.workpools {
		if strings.HasPrefix(k, prefix) {
			pools = append(pools, wp)
			delete(rt.workpools, k)
		}
	}
	rt.mu.Unlock()

	for _, wp := range pools {
		wp.Close()
	}
	for _, c := range cancels {
		c()
	}
	rt.Registry.CancelAllRunning(sessionID)
	rt.Registry.UnregisterChildSession(sessionID)
	if hasArt {
		if artEntry.releaseFn != nil {
			artEntry.releaseFn()
		}
		if artEntry.temp && artEntry.dir != "" {
			_ = os.RemoveAll(artEntry.dir)
		}
	}
	for _, j := range jobs.List(sessionID) {
		if j.Kind() == jobs.KindAgent && j.Status() == "running" {
			j.Cancel()
		}
	}
	jobs.UnbindSession(sessionID)
}

// CancelSession cancels every running subagent and workpool owned by sessionKey.
// An empty sessionKey is a no-op (use ResetForTests in tests to clear all state).
func (rt *Runtime) CancelSession(sessionKey string) {
	if sessionKey == "" {
		return
	}
	rt.EndSession(sessionKey)
}

// cancelAllForTests cancels every session in rt (only for ResetForTests).
func (rt *Runtime) cancelAllForTests() {
	rt.mu.Lock()
	var cancels []context.CancelFunc
	for k, list := range rt.sessionCancels {
		cancels = append(cancels, list...)
		delete(rt.sessionCancels, k)
	}
	var pools []*WorkPool
	for k, wp := range rt.workpools {
		pools = append(pools, wp)
		delete(rt.workpools, k)
	}
	var arts []sessionArtEntry
	for k, ent := range rt.sessionArtDirs {
		arts = append(arts, ent)
		delete(rt.sessionArtDirs, k)
	}
	rt.sessionContexts = map[string]sdk.Context{}
	rt.outputManagers = map[string]*OutputManager{}
	rt.lifecycleShown = map[string]bool{}
	rt.waitStreaks = map[string]*waitIdleStreak{}
	rt.taskToolSigs = map[string]string{}
	rt.mu.Unlock()

	for _, wp := range pools {
		wp.Close()
	}
	for _, c := range cancels {
		c()
	}
	for _, ent := range arts {
		if ent.releaseFn != nil {
			ent.releaseFn()
		}
		if ent.temp && ent.dir != "" {
			_ = os.RemoveAll(ent.dir)
		}
	}
	rt.Registry.CancelAllRunning("")
}

// TaskSchema builds omp's JSON Schema for the `task` tool under cfg and defaultAgent.
// Default shape is batch `{context, tasks[]}` matching omp v19.1.1 (`task.batch: true`).
// upstream: packages/coding-agent/src/task/types.ts:152
func TaskSchema(cfg Config, defaultAgent string) sdk.Schema {
	if defaultAgent == "" {
		defaultAgent = DefaultSpawnAgent
	}
	itemProps := map[string]any{
		"name":          map[string]any{"type": "string"},
		"agent":         map[string]any{"type": "string"},
		"solutionSpace": map[string]any{"type": "string"},
	}
	itemRequired := []string{"solutionSpace"}
	if cfg.StepsEnabled {
		itemProps["steps"] = map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
		}
		itemRequired = append([]string{"steps"}, itemRequired...)
	} else {
		itemProps["task"] = map[string]any{"type": "string"}
		itemRequired = append([]string{"task"}, itemRequired...)
	}
	if cfg.EnableEffort {
		itemProps["effort"] = map[string]any{"type": "string", "enum": []string{"lo", "med", "hi"}}
	}
	if cfg.Structured {
		itemProps["outputSchema"] = map[string]any{
			"anyOf": []any{
				map[string]any{"type": "object"},
				map[string]any{"type": "boolean"},
				map[string]any{"type": "string"},
				map[string]any{"type": "null"},
			},
		}
		itemProps["schemaMode"] = map[string]any{"type": "string", "enum": []string{"permissive", "strict"}}
	}
	if cfg.EvalToolsEnabled {
		itemProps["tools"] = map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
		}
	}
	if cfg.PerCallModel == "on" {
		itemProps["model"] = map[string]any{"type": "string"}
	}
	if cfg.IsolationEnabled {
		itemProps["isolated"] = map[string]any{"type": "boolean"}
	}

	if cfg.BatchEnabled {
		return sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"context": map[string]any{"type": "string"},
				"tasks": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":                 "object",
						"properties":           itemProps,
						"required":             itemRequired,
						"additionalProperties": false,
					},
				},
			},
			"required":             []string{"context", "tasks"},
			"additionalProperties": false,
		}
	}

	return sdk.Schema{
		"type":                 "object",
		"properties":           itemProps,
		"required":             itemRequired,
		"additionalProperties": false,
	}
}

// RenderTaskDescription renders omp's `prompts/tools/task.md` description
// for the discovered agents and active settings.
// upstream: packages/coding-agent/src/task/index.ts:166
func RenderTaskDescription(cfg Config, discovered []AgentDefinition, parentSpawns string) string {
	policy := ResolveSpawnPolicy(parentSpawns)
	var filtered []AgentDefinition
	if policy.Enabled {
		for _, ag := range discovered {
			if slices.Contains(cfg.DisabledAgents, ag.Name) {
				continue
			}
			if !policy.Allows(ag.Name) {
				continue
			}
			filtered = append(filtered, ag)
		}
	}

	hasBlocking := false
	hasStructured := false
	for _, ag := range filtered {
		if ag.Blocking {
			hasBlocking = true
		}
		if !cfg.Structured && ag.Structured {
			hasStructured = true
		}
	}

	var b strings.Builder
	if cfg.AsyncEnabled {
		if cfg.BatchEnabled {
			b.WriteString("Spawn `tasks[]` concurrently; IDs return immediately.\n")
		} else {
			b.WriteString("Spawn one agent; ID returns immediately.\n")
		}
		if hasBlocking {
			b.WriteString("BLOCKING agents return inline.\n")
		}
		if cfg.Structured || hasStructured {
			b.WriteString("\n# Results\n")
			if cfg.Structured {
				b.WriteString("`outputSchema` parsed payload, even invalid: agent://<id> (field /<field>, nested /reports/0/data); invalid preview inline.\n")
			} else {
				b.WriteString("STRUCTURED agents' parsed payload, even invalid: agent://<id> (field /<field>, nested /reports/0/data); invalid preview inline.\n")
			}
		}
		b.WriteString("\nResults auto-deliver; NEVER poll. Completely blocked? Call `wait` until it returns the first settled job you started.\n")
		b.WriteString("`completed`: subagent yielded successfully; claimed artifacts unverified.\n")
	} else {
		if cfg.BatchEnabled {
			b.WriteString("Run `tasks[]` synchronously.\n")
		} else {
			b.WriteString("Run one agent synchronously.\n")
		}
	}

	b.WriteString("\n# Inputs\n")
	if cfg.BatchEnabled {
		b.WriteString("- `agent`: most specific, inside each `tasks[]` item, NEVER at the top level.\n")
	} else {
		b.WriteString("- `agent`: most specific.\n")
	}
	fmt.Fprintf(&b, "  - Omit only for the default (`%s`); NEVER name it.\n", policy.DefaultAgent)
	if IsScoutSpawnable(cfg.DisabledAgents, parentSpawns) {
		b.WriteString("  - Read-only research MUST use `scout` only when files unknown.\n")
	}
	b.WriteString("  - Prefer one agent to investigate + edit.\n")
	if cfg.BatchEnabled {
		b.WriteString("- `context`: shared (`# Goal`, `# Contract` interfaces); NEVER repeat per task.\n")
	}
	if cfg.StepsEnabled {
		b.WriteString("- `steps`: ordered stages for one agent; each is sent only after it finishes the previous one. Earlier stages' reports arrive as progress messages; the last stage's result is returned.\n")
		if cfg.BatchEnabled {
			b.WriteString("  - First step carries `# Target` files/non-goals beyond `context`.\n")
		} else {
			b.WriteString("  - First step carries `# Target` files/non-goals.\n")
		}
		b.WriteString("  - Each step: one `# Change` stage.\n")
		b.WriteString("  - Last step adds `# Acceptance` observable result.\n")
		b.WriteString("  - Children start blank; parent IRC steers immediately; large payloads via local://<path>, NEVER inline.\n")
		b.WriteString("  - Independent work → separate tasks.\n")
	} else {
		b.WriteString("- `task`: self-contained (`# Target` files/non-goals, `# Change` steps/APIs, `# Acceptance` observable result).\n")
		b.WriteString("  - Children start blank; parent IRC steers immediately; large payloads via local://<path>, NEVER inline.\n")
	}
	b.WriteString("- `name`: CamelCase ≤32, auto-generated if omitted; address agent by name.")
	if cfg.Structured {
		b.WriteString(" `outputSchema` overrides agent/session schemas.")
	}
	b.WriteString("\n")
	b.WriteString("- `solutionSpace`: how open-ended the child's problem is: fix or design given, or which causes or designs remain open.\n")
	b.WriteString("  - Volume of work does not widen it; NEVER mention sibling agents or coordination.\n")
	b.WriteString("  - Examples: `one fix: rename, names given`; `one fix: slice end in paginate`; `single-flight cache load; races easy to miss`;\n")
	b.WriteString("    `several retry API shapes; error classes to choose`; `deadlock cause open, no repro`\n")
	if cfg.EvalToolsEnabled {
		b.WriteString("- `tools`: eval-defined, run in your kernel.\n")
	}
	if cfg.EnableEffort {
		b.WriteString("- `effort`: `\"lo\"`|`\"med\"`|`\"hi\"` by how open-ended the problem is.\n")
	}
	if cfg.Structured {
		b.WriteString("- `schemaMode`: default permissive warns after retries; strict fails.\n")
	}
	if cfg.IsolationEnabled {
		if strings.EqualFold(strings.TrimSpace(cfg.IsolationMerge), "branch") {
			b.WriteString("- `isolated`: worktree; changes captured on `omp-task/<id>` branch.\n")
		} else if cfg.IsolationApply {
			b.WriteString("- `isolated`: worktree; successful changes apply to parent.\n")
		} else {
			b.WriteString("- `isolated`: worktree; changes retained, not applied.\n")
		}
	}
	b.WriteString("\nShared edits need one integration owner; give each task its files")
	if !cfg.BatchEnabled {
		b.WriteString(" and interfaces")
	}
	b.WriteString(".\n")
	b.WriteString("Every task MUST skip build/lint/tests/formatters mid-flight; run once afterward.\n")

	b.WriteString("\n# Available Agents\n")
	if !policy.Enabled {
		b.WriteString("Agent spawning is currently disabled.\n")
	} else {
		for _, ag := range filtered {
			b.WriteString("- `")
			b.WriteString(ag.Name)
			b.WriteString("`")
			if IsReadOnlyAgent(ag) {
				b.WriteString(" (read-only, no edits)")
			}
			if ag.Blocking {
				b.WriteString(" (BLOCKING; inline result)")
			}
			if !cfg.Structured && ag.Structured {
				b.WriteString(" (STRUCTURED; schema result)")
			}
			b.WriteString(": ")
			b.WriteString(ag.Description)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// DecodeTaskParams parses the raw map[string]any from PiG into a TaskParams.
func DecodeTaskParams(raw map[string]any) TaskParams {
	var p TaskParams
	if raw == nil {
		return p
	}
	if _, ok := raw["schema"]; ok {
		p.HasSchema = true
	}
	if v, ok := raw["context"]; ok && v != nil {
		p.HasContext = true
		p.Context, _ = v.(string)
	}
	if v, ok := raw["agent"]; ok {
		p.HasAgent = true
		p.Agent, _ = v.(string)
	}
	if v, ok := raw["name"]; ok && v != nil {
		p.Name, _ = v.(string)
	}
	if v, ok := raw["task"]; ok && v != nil {
		p.HasTask = true
		p.Task, _ = v.(string)
	}
	if v, ok := raw["steps"]; ok && v != nil {
		p.HasSteps = true
		p.Steps = parseStringList(v)
	}
	if v, ok := raw["solutionSpace"]; ok && v != nil {
		p.SolutionSpace, _ = v.(string)
	}
	if v, ok := raw["outputSchema"]; ok {
		p.HasOutput = true
		p.OutputSchema = v
	}
	if v, ok := raw["schemaMode"]; ok && v != nil {
		p.SchemaMode, _ = v.(string)
	}
	if v, ok := raw["tools"]; ok && v != nil {
		p.Tools = parseStringList(v)
	}
	if v, ok := raw["effort"]; ok && v != nil {
		p.Effort, _ = v.(string)
	}
	if v, ok := raw["isolated"]; ok && v != nil {
		if b, ok := v.(bool); ok {
			p.Isolated = &b
		}
	}
	if v, ok := raw["model"]; ok && v != nil {
		if !isBlankModel(v) {
			p.HasModel = true
			p.Model = v
		}
	}
	if rawTasks, ok := raw["tasks"]; ok && rawTasks != nil {
		p.HasTasks = true
		if list, ok := rawTasks.([]any); ok {
			for _, elem := range list {
				m, ok := elem.(map[string]any)
				if !ok {
					p.Tasks = append(p.Tasks, TaskItem{})
					continue
				}
				var item TaskItem
				if v, ok := m["name"].(string); ok {
					item.Name = v
				}
				if v, ok := m["agent"].(string); ok {
					item.Agent = v
				}
				if v, ok := m["task"]; ok && v != nil {
					item.HasTask = true
					item.Task, _ = v.(string)
				}
				if v, ok := m["steps"]; ok && v != nil {
					item.HasSteps = true
					item.Steps = parseStringList(v)
				}
				if v, ok := m["solutionSpace"].(string); ok {
					item.SolutionSpace = v
				}
				if v, ok := m["outputSchema"]; ok {
					item.HasOutput = true
					item.OutputSchema = v
				}
				if v, ok := m["schemaMode"].(string); ok {
					item.SchemaMode = v
				}
				if v, ok := m["tools"]; ok && v != nil {
					item.Tools = parseStringList(v)
				}
				if v, ok := m["effort"].(string); ok {
					item.Effort = v
				}
				if v, ok := m["isolated"].(bool); ok {
					b := v
					item.Isolated = &b
				}
				if v, ok := m["model"]; ok && v != nil && !isBlankModel(v) {
					item.HasModel = true
					item.Model = v
				}
				p.Tasks = append(p.Tasks, item)
			}
		}
	}
	return p
}

func parseStringList(v any) []string {
	switch tv := v.(type) {
	case []string:
		return append([]string(nil), tv...)
	case []any:
		out := make([]string, 0, len(tv))
		for _, item := range tv {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func isBlankModel(v any) bool {
	switch tv := v.(type) {
	case string:
		return strings.TrimSpace(tv) == ""
	case []any:
		return len(tv) == 0
	case []string:
		return len(tv) == 0
	}
	return false
}

// ValidateTaskParams enforces omp's task shape and spawn validation rules.
// upstream: packages/coding-agent/src/task/index.ts:222-336
func ValidateTaskParams(cfg Config, p TaskParams) string {
	if p.HasSchema {
		return "The task tool uses `outputSchema`; rename the stale `schema` field."
	}
	if cfg.BatchEnabled && p.HasTasks && p.HasAgent {
		return "Top-level `agent` is not part of the batch shape. Set `tasks[].agent` instead."
	}
	if cfg.BatchEnabled && p.HasTasks && p.HasModel {
		return "Top-level `model` is not part of the batch shape. Set `tasks[].model` instead."
	}
	if !cfg.BatchEnabled {
		var disallowed []string
		if p.HasTasks {
			disallowed = append(disallowed, "`tasks`")
		}
		if p.HasContext {
			disallowed = append(disallowed, "`context`")
		}
		if len(disallowed) > 0 {
			return fmt.Sprintf("task.batch is disabled, so the task tool does not accept %s. Spawn one agent per call with `task`, or enable the task.batch setting.", strings.Join(disallowed, " or "))
		}
	}

	validateInput := func(hasTask bool, task string, hasSteps bool, steps []string, label string) string {
		if hasTask && hasSteps {
			return label + " takes `task` or `steps`, not both."
		}
		if hasSteps {
			valid := len(steps) > 0
			for _, st := range steps {
				if strings.TrimSpace(st) == "" {
					valid = false
					break
				}
			}
			if !valid {
				return label + " has invalid `steps`: provide a non-empty list of non-empty instructions, one per stage."
			}
			return ""
		}
		if hasTask && strings.TrimSpace(task) != "" {
			return ""
		}
		if cfg.StepsEnabled && !hasTask {
			if label == "The call" {
				return "Missing `steps`. Provide the agent's ordered stages, one instruction each."
			}
			return label + " is missing `steps`. Every task needs its ordered stages, one instruction each."
		}
		if label == "The call" {
			return "Missing `task`. Provide complete, self-contained instructions for the agent."
		}
		return label + " is missing `task`. Every task needs complete, self-contained instructions."
	}

	validateEffort := func(effort, label string) string {
		if effort == "" || effort == "lo" || effort == "med" || effort == "hi" {
			return ""
		}
		b, _ := json.Marshal(effort)
		return fmt.Sprintf("%s has an invalid `effort` value %s. Use \"lo\", \"med\", or \"hi\".", label, string(b))
	}

	validateModel := func(hasModel bool, model any, label string) string {
		if !hasModel {
			return ""
		}
		if cfg.PerCallModel != "on" {
			return fmt.Sprintf("Per-call model is disabled for task calls (task.perCallModel: %s). Omit model to use the agent's configured model.", cfg.PerCallModel)
		}
		s, ok := model.(string)
		if !ok || !strings.Contains(strings.TrimSpace(s), "/") {
			b, _ := json.Marshal(model)
			return fmt.Sprintf("%s has an invalid `model` value %s. Use one exact provider/model-id selector, optionally followed by a supported :level. Role aliases, patterns, and candidate arrays are not accepted.", label, string(b))
		}
		return ""
	}

	if cfg.BatchEnabled && p.HasTasks {
		if len(p.Tasks) == 0 {
			field := "task"
			if cfg.StepsEnabled {
				field = "steps"
			}
			return fmt.Sprintf("Missing `tasks`. Provide at least one task item with `%s`.", field)
		}
		if p.HasTask || p.HasSteps {
			return "Top-level `task` or `steps` is not part of the batch shape. Put the work in `tasks[]` items."
		}
		seenNames := map[string]string{}
		for i, item := range p.Tasks {
			label := fmt.Sprintf("Task %d", i+1)
			if strings.TrimSpace(item.Name) != "" {
				label += " (`" + strings.TrimSpace(item.Name) + "`)"
			}
			if err := validateInput(item.HasTask, item.Task, item.HasSteps, item.Steps, label); err != "" {
				return err
			}
			if err := validateEffort(item.Effort, label); err != "" {
				return err
			}
			if err := validateModel(item.HasModel, item.Model, label); err != "" {
				return err
			}
			if n := strings.TrimSpace(item.Name); n != "" {
				key := strings.ToLower(n)
				if prev, dup := seenNames[key]; dup {
					if prev == n {
						return fmt.Sprintf("Duplicate task name `%s`. Provided names must be unique within a call (case-insensitive).", n)
					}
					return fmt.Sprintf("Duplicate task name `%s` / `%s`. Provided names must be unique within a call (case-insensitive).", prev, n)
				}
				seenNames[key] = n
			}
		}
		if !p.HasContext || strings.TrimSpace(p.Context) == "" {
			return "Missing `context`. Provide the shared background for this batch — goal, constraints, and any contract the tasks share."
		}
		return ""
	}

	if cfg.BatchEnabled && !p.HasTask && !p.HasSteps {
		return "Missing `tasks`. Provide a `tasks` array (one subagent per item) with a shared `context`."
	}

	if err := validateInput(p.HasTask, p.Task, p.HasSteps, p.Steps, "The call"); err != "" {
		return err
	}
	if err := validateEffort(p.Effort, "The call"); err != "" {
		return err
	}
	if err := validateModel(p.HasModel, p.Model, "The call"); err != "" {
		return err
	}
	return ""
}

type resolvedSpawn struct {
	item                       TaskItem
	agent                      AgentDefinition
	assignment                 string
	outputSchema               any
	schemaMode                 string
	schemaSource               string
	outputSchemaOverridesAgent bool
	modelOverride              []string
	effort                     string
	softBudget                 int
	customTools                []sdk.ToolDefinition
}
type spawnPlan struct {
	index    int
	agentID  string
	spec     resolvedSpawn
	blocking bool
	progress AgentProgress
}

func shortenHomePath(p string) string {
	if p == "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	cleanHome := filepath.Clean(home)
	cleanP := filepath.Clean(p)
	if cleanP == cleanHome {
		return "~"
	}
	prefix := cleanHome + string(filepath.Separator)
	if strings.HasPrefix(cleanP, prefix) {
		return "~" + string(filepath.Separator) + strings.TrimPrefix(cleanP, prefix)
	}
	return p
}

func formatSearchedDirs(dirs []string) string {
	if len(dirs) == 0 {
		return "none"
	}
	short := make([]string, len(dirs))
	for i, d := range dirs {
		short[i] = shortenHomePath(d)
	}
	return strings.Join(short, ", ")
}

func formatAvailableAgents(agents []AgentDefinition, disabled []string) string {
	var names []string
	for _, a := range agents {
		if slices.Contains(disabled, a.Name) {
			continue
		}
		names = append(names, a.Name)
	}
	if len(names) == 0 {
		return ""
	}
	return strings.Join(names, ", ")
}

func formatDisabledAgentError(agentName string, agents []AgentDefinition, disabled []string) string {
	msg := fmt.Sprintf(`Agent "%s" is disabled in settings. Enable it via /agents, or use a different agent type.`, agentName)
	if avail := formatAvailableAgents(agents, disabled); avail != "" {
		msg += " Available: " + avail
	}
	return msg
}

// ExecuteTask runs omp's `task` tool.
// upstream: packages/coding-agent/src/task/index.ts (#dispatch)
func (rt *Runtime) ExecuteTask(ctx sdk.Context, rawParams map[string]any) (sdk.ToolResult, error) {
	cfg := rt.EffectiveConfig(ctx)
	p := RepairTaskParams(DecodeTaskParams(rawParams))
	if msg := ValidateTaskParams(cfg, p); msg != "" {
		return taskModeError(msg), nil
	}
	// Normalize items list.
	var items []TaskItem
	if p.HasTasks && len(p.Tasks) > 0 {
		items = p.Tasks
	} else {
		items = []TaskItem{{
			Name:          p.Name,
			Agent:         p.Agent,
			Task:          p.Task,
			HasTask:       p.HasTask,
			Steps:         p.Steps,
			HasSteps:      p.HasSteps,
			SolutionSpace: p.SolutionSpace,
			OutputSchema:  p.OutputSchema,
			HasOutput:     p.HasOutput,
			SchemaMode:    p.SchemaMode,
			Tools:         p.Tools,
			Effort:        p.Effort,
			Isolated:      p.Isolated,
			Model:         p.Model,
			HasModel:      p.HasModel,
		}}
	}

	// Preflight every item before dispatching any child.
	cwd := ctx.Cwd()
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	sessKey := rt.sessionKey(ctx)
	longLivedCtx := rt.sessionContextFor(sessKey, ctx)
	jobs.BindSessionIfMissing(sessKey, longLivedCtx)
	disc := rt.discover(cwd)
	planMode := gate.PlanActive(ctx)
	taskDepth := rt.Registry.TaskDepthFor(sessKey)
	effMaxDepth := EffectiveMaxRecursionDepth(cfg.MaxRecursionDepth)
	parentSpawns := rt.Registry.SessionSpawnsFor(sessKey)
	spawnPolicy := ResolveSpawnPolicy(parentSpawns)
	parentAgentID := MainAgentID
	if meta, ok := rt.Registry.ChildMeta(sessKey); ok && meta.AgentID != "" {
		parentAgentID = meta.AgentID
	}
	parentLocalRoot, _ := urls.LocalRoot(longLivedCtx)
	// Preflight all requested eval-defined tools before resolving individual items,
	// matching upstream packages/coding-agent/src/task/index.ts:1081-1093.
	var allEvalToolNames []string
	for _, item := range items {
		allEvalToolNames = append(allEvalToolNames, item.Tools...)
	}
	var evalToolsByName map[string]sdk.ToolDefinition
	if len(dedupeNonEmptyStrings(allEvalToolNames)) > 0 {
		if planMode {
			return taskModeError("Task execution failed: " + errEvalToolsPlanMode), nil
		}
		if !cfg.EvalToolsEnabled {
			return taskModeError("Task execution failed: " + errEvalToolsDisabled), nil
		}
		preflightCtx, preflightCancel := context.WithCancel(context.Background())
		defer preflightCancel()
		if done := ctx.Done(); done != nil {
			go func() {
				select {
				case <-done:
					preflightCancel()
				case <-preflightCtx.Done():
				}
			}()
		}
		resolvedTools, err := ResolveEvalCustomTools(preflightCtx, longLivedCtx, allEvalToolNames, planMode)
		if err != nil {
			return taskModeError("Task execution failed: " + err.Error()), nil
		}
		evalToolsByName = make(map[string]sdk.ToolDefinition, len(resolvedTools))
		for _, td := range resolvedTools {
			evalToolsByName[td.Name] = td
		}
	}
	resolved := make([]resolvedSpawn, 0, len(items))
	var preflightErrs []string

	for i, item := range items {
		label := fmt.Sprintf("Task #%d", i+1)
		if n := strings.TrimSpace(item.Name); n != "" {
			label = fmt.Sprintf("Task %s", n)
		}
		// Plan-mode control checks (`task/structured-subagent.ts:317-332`).
		if planMode {
			if len(item.Tools) > 0 {
				preflightErrs = append(preflightErrs, fmt.Sprintf("%s failed preflight: %s", label, errEvalToolsPlanMode))
				continue
			}
			if item.Isolated != nil {
				preflightErrs = append(preflightErrs, fmt.Sprintf("%s failed preflight: Subagent isolation, apply, and merge controls are unavailable in plan mode.", label))
				continue
			}
		}
		if !CanSpawnAtDepth(effMaxDepth, taskDepth) {
			preflightErrs = append(preflightErrs, fmt.Sprintf("%s failed preflight: Cannot spawn another agent at task depth %d; maximum depth is %d.", label, taskDepth, effMaxDepth))
			continue
		}
		agentName := strings.TrimSpace(item.Agent)
		if agentName == "" {
			agentName = spawnPolicy.DefaultAgent
		}
		if !spawnPolicy.Allows(agentName) {
			preflightErrs = append(preflightErrs, fmt.Sprintf("%s failed preflight: Cannot spawn '%s'. Allowed: %s", label, agentName, spawnPolicy.AllowedErrorText))
			continue
		}
		ag, ok := GetAgent(disc.Agents, agentName)
		if !ok {
			avail := "none"
			if len(disc.Agents) > 0 {
				names := make([]string, len(disc.Agents))
				for j, a := range disc.Agents {
					names[j] = a.Name
				}
				avail = strings.Join(names, ", ")
			}
			preflightErrs = append(preflightErrs, fmt.Sprintf(`%s failed preflight: Unknown agent "%s". Available: %s. Searched: %s`, label, agentName, avail, formatSearchedDirs(disc.SearchedDirs)))
			continue
		}
		if slices.Contains(cfg.DisabledAgents, agentName) {
			preflightErrs = append(preflightErrs, fmt.Sprintf("%s failed preflight: %s", label, formatDisabledAgentError(agentName, disc.Agents, cfg.DisabledAgents)))
			continue
		}
		if item.Isolated != nil && *item.Isolated && !cfg.IsolationEnabled {
			preflightErrs = append(preflightErrs, fmt.Sprintf("%s failed preflight: Subagent isolated execution requires task.isolation.enabled; it is currently false.", label))
			continue
		}
		if item.Isolated != nil && *item.Isolated {
			if _, err := ResolveGitRepoRoot(context.Background(), cwd); err != nil {
				preflightErrs = append(preflightErrs, fmt.Sprintf("%s failed preflight: %s", label, err.Error()))
				continue
			}
		}
		if planMode {
			ag = CreatePlanModeAgent(ag)
		}
		assignment := strings.TrimSpace(item.Task)
		if len(item.Steps) > 0 {
			assignment = renderStepsBrief(item.Steps)
		}

		// Resolve output schema according to task.structured + agent.structured.
		var outSchema any
		schemaSource := "none"
		overridesAgent := false
		schemaMode := item.SchemaMode
		if schemaMode == "" {
			schemaMode = "permissive"
		}
		if cfg.Structured || ag.Structured {
			if item.HasOutput && item.OutputSchema != nil {
				outSchema = item.OutputSchema
				schemaSource = "caller"
				overridesAgent = true
			} else if ag.Output != nil {
				outSchema = ag.Output
				schemaSource = "agent"
			}
		}

		// Model override precedence: per-call > config override > agent frontmatter.
		var modelOverride []string
		if item.HasModel {
			if s, ok := item.Model.(string); ok && strings.TrimSpace(s) != "" {
				modelOverride = []string{strings.TrimSpace(s)}
			}
		} else if ov := cfg.ModelOverrides[agentName]; len(ov) > 0 {
			modelOverride = ov
		} else if len(ag.Model) > 0 {
			modelOverride = ag.Model
		}

		var effort string
		if cfg.EnableEffort {
			effort = item.Effort
		}

		var itemCustomTools []sdk.ToolDefinition
		if len(item.Tools) > 0 && len(evalToolsByName) > 0 {
			for _, name := range dedupeNonEmptyStrings(item.Tools) {
				if td, ok := evalToolsByName[name]; ok {
					itemCustomTools = append(itemCustomTools, td)
				}
			}
		}

		resolved = append(resolved, resolvedSpawn{
			item:                       item,
			agent:                      ag,
			assignment:                 assignment,
			outputSchema:               outSchema,
			schemaMode:                 schemaMode,
			schemaSource:               schemaSource,
			outputSchemaOverridesAgent: overridesAgent,
			modelOverride:              modelOverride,
			effort:                     effort,
			softBudget:                 ResolveSoftRequestBudget(ag.Name, cfg.SoftRequestBudget, cfg.AgentSoftRequestBudget),
			customTools:                itemCustomTools,
		})
	}

	if len(preflightErrs) > 0 {
		if !cfg.BatchEnabled {
			return taskModeError("Task execution failed: " + strings.TrimPrefix(preflightErrs[0], "Task #1 failed preflight: ")), nil
		}
		return taskModeError(strings.Join(preflightErrs, "\n")), nil
	}

	artifactsDir, _ := rt.sessionArtifactsDir(ctx)
	outMgr := rt.outputManager(sessKey, artifactsDir, cfg.CrossInstance)
	callStartedAt := time.Now()
	sharedContext := ""
	if cfg.BatchEnabled {
		sharedContext = strings.TrimSpace(p.Context)
	}

	// Register Main with a live delivery handler bound to this top-level session
	// so `write agent://Main` from subagents delivers immediately via SendMessage.
	if taskDepth == 0 {
		rt.ensureMainRef(sessKey, longLivedCtx, artifactsDir)
	}

	// Partition items into async vs blocking (inline).
	plans := make([]spawnPlan, len(resolved))
	var asyncPlans []*spawnPlan
	var syncPlans []*spawnPlan
	for i, spec := range resolved {
		rawName := SanitizeAgentID(spec.item.Name)
		if rawName == "" {
			rawName = GenerateTaskName()
		}
		agentID := outMgr.Allocate(rawName)
		blocking := spec.agent.Blocking || !cfg.AsyncEnabled
		plans[i] = spawnPlan{
			index:    i,
			agentID:  agentID,
			spec:     spec,
			blocking: blocking,
			progress: AgentProgress{
				Index:        i,
				ID:           agentID,
				Agent:        spec.agent.Name,
				AgentSource:  spec.agent.Source,
				Status:       "pending",
				Task:         RenderSubagentUserPrompt(spec.assignment),
				Assignment:   spec.assignment,
				RecentTools:  []RecentTool{},
				RecentOutput: []string{},
			},
		}
	}
	// Pre-register every child in this batch BEFORE starting any goroutine so
	// each child's `# Peers` roster sees all siblings deterministically without
	// racing individual RunSubagent entry (`executor.go:708,730`).
	// Each sibling gets its own `time.Now()` (guaranteed > Main's registration time)
	// matching omp's per-child `agentRegistry.register` (`sdk.ts:4794`, `agent-registry.ts:219-230`).
	for i := range plans {
		sp := &plans[i]
		now := time.Now()
		ref := &AgentRef{
			ID:          sp.agentID,
			DisplayName: sp.spec.agent.Name,
			Kind:        "sub",
			ParentID:    parentAgentID,
			Status:      "running",
			SessionID:   sessKey,
			OutputDir:   artifactsDir,
			CreatedAt:   now,
			LastActive:  now,
		}
		if artifactsDir != "" {
			ref.SessionFile = filepath.Join(artifactsDir, sp.agentID+".jsonl")
		}
		rt.Registry.Register(ref)
	}
	for i := range plans {
		if plans[i].blocking {
			syncPlans = append(syncPlans, &plans[i])
		} else {
			asyncPlans = append(asyncPlans, &plans[i])
		}
	}

	var projDirPtr *string
	if disc.ProjectAgentsDir != "" {
		d := disc.ProjectAgentsDir
		projDirPtr = &d
	}

	// Pure synchronous path (async disabled or all items blocking).
	if len(asyncPlans) == 0 {
		return rt.runSyncFanout(ctx, longLivedCtx, cfg, sessKey, parentAgentID, parentLocalRoot, taskDepth, planMode, cwd, artifactsDir, sharedContext, projDirPtr, callStartedAt, syncPlans), nil
	}

	// Async path (possibly mixed with inline blocking items).
	var aggMu sync.Mutex
	settledCount := 0
	failedCount := 0
	var syncResults []SingleResult
	var syncUsage *Usage
	var syncOutputPaths []string
	primaryJobID := asyncPlans[0].agentID

	buildDetails := func() TaskToolDetails {
		aggMu.Lock()
		defer aggMu.Unlock()
		progs := make([]AgentProgress, len(plans))
		for i := range plans {
			progs[i] = plans[i].progress.clone()
		}
		state := "running"
		if settledCount >= len(asyncPlans) {
			if failedCount > 0 {
				state = "failed"
			} else {
				state = "completed"
			}
		}
		resCopy := append([]SingleResult(nil), syncResults...)
		if resCopy == nil {
			resCopy = []SingleResult{}
		}
		return TaskToolDetails{
			ProjectAgentsDir: projDirPtr,
			Results:          resCopy,
			TotalDurationMs:  time.Since(callStartedAt).Milliseconds(),
			Usage:            syncUsage,
			OutputPaths:      syncOutputPaths,
			Progress:         progs,
			Async: &TaskAsyncState{
				State: state,
				JobID: primaryJobID,
				Type:  "task",
			},
		}
	}

	type startedEntry struct {
		agentID string
		jobID   string
	}
	// Snapshot initial pending details BEFORE launching any async goroutine so
	// the returned `task` tool result deterministically reports `status: "pending"`
	// (matching omp `task/index.ts:1244-1293,1340-1350`) without racing goroutine start.
	initialDetails := buildDetails()

	started := make([]startedEntry, 0, len(asyncPlans))
	for _, sp := range asyncPlans {
		sp := sp
		jobCtx, jobCancel := context.WithCancel(context.Background())
		rt.trackCancel(sessKey, jobCancel)

		job := NewAgentJob(sp.agentID, sp.agentID, jobCancel, sp.progress)
		jobs.Add(sessKey, job)
		started = append(started, startedEntry{agentID: sp.agentID, jobID: sp.agentID})

		go func() {
			defer jobCancel()
			// Guarantee the pre-registered AgentRef never stays "running" on any
			// exit path (sem.Acquire abort, RunSubagent early return, or panic).
			defer rt.Registry.SettleIfRunning(sessKey, sp.agentID, "aborted")
			defer func() {
				if r := recover(); r != nil {
					errStr := fmt.Sprintf("subagent panicked: %v", r)
					aggMu.Lock()
					sp.progress.Status = "failed"
					settledCount++
					failedCount++
					aggMu.Unlock()
					job.finish("failed", errStr, "", nil, nil, false, nil)
				}
			}()
			if err := rt.sem.Acquire(jobCtx); err != nil {
				aggMu.Lock()
				sp.progress.Status = "aborted"
				settledCount++
				failedCount++
				aggMu.Unlock()
				job.finish("cancelled", "Aborted before execution", "Aborted before execution", nil, nil, false, nil)
				return
			}
			defer rt.sem.Release()
			aggMu.Lock()
			sp.progress.Status = "running"
			aggMu.Unlock()

			noticeEnabled := cfg.SoftRequestBudgetNotice
			runReq := RunRequest{
				SDK:                        longLivedCtx,
				ParentSessionID:            sessKey,
				ParentAgentID:              parentAgentID,
				ParentLocalRoot:            parentLocalRoot,
				Cwd:                        cwd,
				TaskDepth:                  taskDepth,
				Index:                      sp.index,
				ID:                         sp.agentID,
				Agent:                      sp.spec.agent,
				Assignment:                 sp.spec.assignment,
				Context:                    sharedContext,
				Steps:                      sp.spec.item.Steps,
				SolutionSpace:              sp.spec.item.SolutionSpace,
				OutputSchema:               sp.spec.outputSchema,
				SchemaMode:                 sp.spec.schemaMode,
				SchemaSource:               sp.spec.schemaSource,
				OutputSchemaOverridesAgent: sp.spec.outputSchemaOverridesAgent,
				ModelOverride:              sp.spec.modelOverride,
				Effort:                     sp.spec.effort,
				MaxEffort:                  cfg.MaxEffort,
				ArtifactsDir:               artifactsDir,
				MaxRuntimeMs:               cfg.MaxRuntimeMs,
				SoftBudget:                 sp.spec.softBudget,
				SoftBudgetNotice:           &noticeEnabled,
				AgentIdleTTLMs:             cfg.AgentIdleTTLMs,
				MaxDepth:                   cfg.MaxRecursionDepth,
				IRCEnabled:                 !planMode,
				KeepAlive:                  true,
				CustomTools:                sp.spec.customTools,
				Semaphore:                  rt.sem,
				OnProgress: func(p AgentProgress) {
					aggMu.Lock()
					sp.progress = p.clone()
					aggMu.Unlock()
					job.updateProgress(p, fmt.Sprintf("Running agent %s...", p.ID))
				},
				OnToolResult: func(tool, intent, text string) {
					job.RecordToolCall(tool, intent, text)
				},
			}
			var sr SingleResult
			var mergeSummary string
			if sp.spec.item.Isolated != nil && *sp.spec.item.Isolated {
				sr, mergeSummary = RunIsolatedSubagent(jobCtx, rt.Registry, runReq, cfg.IsolationMerge, cfg.IsolationApply)
			} else {
				sr = RunSubagent(jobCtx, rt.Registry, runReq)
			}

			failed := sr.Aborted || sr.ExitCode != 0 || sr.Error != ""
			aggMu.Lock()
			if sr.Aborted {
				sp.progress.Status = "aborted"
			} else if failed {
				sp.progress.Status = "failed"
			} else {
				sp.progress.Status = "completed"
			}
			sp.progress.DurationMs = sr.DurationMs
			sp.progress.Tokens = sr.Tokens
			sp.progress.Requests = sr.Requests
			sp.progress.ContextTokens = sr.ContextTokens
			sp.progress.ContextWindow = sr.ContextWindow
			if sr.Usage != nil {
				sp.progress.Cost = sr.Usage.Cost.Total
			} else {
				sp.progress.Cost = 0
			}
			sp.progress.ExtractedToolData = sr.ExtractedToolData
			sp.progress.RetryFailure = sr.RetryFailure
			sp.progress.RetryState = nil
			sp.progress.Streaming = nil
			sp.progress.CurrentTool = ""
			sp.progress.CurrentToolArgs = ""
			sp.progress.CurrentToolArgsKey = ""
			sp.progress.CurrentToolIntent = ""
			sp.progress.CurrentToolStartMs = 0
			if sr.ModelRole != "" {
				sp.progress.ModelRole = sr.ModelRole
			}
			if sr.ResolvedModel != "" {
				sp.progress.ResolvedModel = sr.ResolvedModel
				sp.progress.ResolvedModelIdentity = sr.ResolvedModelIdentity
				sp.progress.ResolvedThinkingLevel = sr.ResolvedThinkingLevel
			} else {
				sp.progress.ResolvedModel = ""
				sp.progress.ResolvedModelIdentity = ""
				sp.progress.ResolvedThinkingLevel = ""
			}
			settledCount++
			if failed {
				failedCount++
			}
			aggMu.Unlock()

			ref, _ := rt.Registry.GetInSession(sessKey, sp.agentID)
			resumable := ref.IsResumable() && !sr.Isolated
			summary := FormatTaskResultSummary(sr, sr.DurationMs, mergeSummary, resumable)
			hint := FormatFollowUpHint(sp.agentID, sr.Aborted, sr.Isolated, true, resumable, ref != nil)
			deliveryText := summary + "\n\n" + hint

			// Match omp (`task/index.ts:1622-1662`): only a job cancelled via its own
			// cancel handle (proc kill / session end) is `cancelled` (suppressed from
			// delivery). Timeouts, budget aborts, provider aborts, and `yield {error}`
			// are `failed` so their `<task-result status="cancelled">` envelope and
			// `<abort-reason>` are delivered to the parent.
			jobStatus := "completed"
			if jobCtx.Err() != nil {
				jobStatus = "cancelled"
			} else if failed {
				jobStatus = "failed"
			}
			// Match omp (`task/index.ts:1663`): initial task spawn completions do not
			// set `agentOutputPublished`; only wake turns (`executor.ts:3851,3870`) do.
			job.finish(jobStatus, deliveryText, "", &sr, nil, false, nil)
		}()
	}

	// Render omp's task-spawn-feedback.md text (`prompts/tools/task-spawn-feedback.md`).
	rt.mu.Lock()
	showLifecycle := !rt.lifecycleShown[sessKey]
	rt.lifecycleShown[sessKey] = true
	rt.mu.Unlock()

	agentLabels := []string{}
	for _, sp := range asyncPlans {
		if !slices.Contains(agentLabels, sp.spec.agent.Name) {
			agentLabels = append(agentLabels, sp.spec.agent.Name)
		}
	}
	renderFeedback := func(mixed bool) string {
		var b strings.Builder
		singular := len(started) == 1
		if mixed {
			fmt.Fprintf(&b, "Spawned %d background agent", len(started))
			if !singular {
				b.WriteByte('s')
			}
			b.WriteByte('.')
		} else if len(plans) == 1 {
			fmt.Fprintf(&b, "Spawned agent `%s` (job `%s`).", started[0].agentID, started[0].jobID)
		} else {
			fmt.Fprintf(&b, "Spawned %d background agent", len(started))
			if !singular {
				b.WriteByte('s')
			}
			fmt.Fprintf(&b, " using %s.", strings.Join(agentLabels, ", "))
		}
		if mixed || len(plans) > 1 {
			b.WriteByte('\n')
			for _, st := range started {
				fmt.Fprintf(&b, "- `%s` (job `%s`)\n", st.agentID, st.jobID)
			}
		}
		if showLifecycle {
			// Match omp's `prompts/tools/task-spawn-feedback.md`: always one blank
			// line before the lifecycle paragraph (both single-call and listing).
			if strings.HasSuffix(b.String(), "\n") {
				b.WriteByte('\n')
			} else {
				b.WriteString("\n\n")
			}
			b.WriteString("read proc:// lists jobs/services; read proc://<id> inspects status/output without consuming delivery; write proc://<id>/kill with empty `content` cancels/stops.\n")
			b.WriteString("Job IDs are process-local; delivered results expire shortly (~30s), unconsumed results within ~5min. Agent output/transcripts remain readable at agent://<id> / history://<id>. write agent://<id> messages a live agent.")
		}
		return strings.TrimSpace(b.String())
	}

	advisory := composeSpawnAdvisory(resolved, len(asyncPlans), true, CanSpawnAtDepth(effMaxDepth, taskDepth), IsScoutSpawnable(cfg.DisabledAgents, "*"))

	if len(syncPlans) == 0 {
		text := renderFeedback(false)
		if advisory != "" {
			text += "\n\n" + advisory
		}
		_ = ctx.OnUpdate(sdk.ToolResult{
			Content: fmt.Sprintf("Spawned %d agents...", len(started)),
			Details: initialDetails,
		})
		return sdk.ToolResult{
			Content: text,
			Details: initialDetails,
		}, nil
	}

	// Mixed call: run blocking items inline while async items run in background.
	inlineRes := rt.runSyncFanout(ctx, longLivedCtx, cfg, sessKey, parentAgentID, parentLocalRoot, taskDepth, planMode, cwd, artifactsDir, sharedContext, projDirPtr, callStartedAt, syncPlans)
	if det, ok := inlineRes.Details.(TaskToolDetails); ok {
		aggMu.Lock()
		syncResults = det.Results
		syncUsage = det.Usage
		syncOutputPaths = det.OutputPaths
		for _, r := range det.Results {
			for i := range plans {
				if plans[i].agentID == r.ID {
					if r.Aborted {
						plans[i].progress.Status = "aborted"
					} else if r.ExitCode == 0 && r.Error == "" {
						plans[i].progress.Status = "completed"
					} else {
						plans[i].progress.Status = "failed"
					}
					plans[i].progress.DurationMs = r.DurationMs
				}
			}
		}
		aggMu.Unlock()
	}

	parts := []string{}
	if strings.TrimSpace(inlineRes.Content) != "" {
		parts = append(parts, inlineRes.Content)
	}
	if fb := renderFeedback(true); fb != "" {
		parts = append(parts, fb)
	}
	if advisory != "" {
		parts = append(parts, advisory)
	}
	return sdk.ToolResult{
		Content: strings.Join(parts, "\n\n"),
		Details: buildDetails(),
		IsError: inlineRes.IsError,
	}, nil
}

func (rt *Runtime) runSyncFanout(
	ctx sdk.Context,
	longLivedCtx sdk.Context,
	cfg Config,
	sessKey string,
	parentAgentID string,
	parentLocalRoot string,
	taskDepth int,
	planMode bool,
	cwd string,
	artifactsDir string,
	sharedContext string,
	projDirPtr *string,
	startTime time.Time,
	plans []*spawnPlan,
) sdk.ToolResult {
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.trackCancel(sessKey, cancel)

	if done := ctx.Done(); done != nil {
		go func() {
			select {
			case <-done:
				cancel()
			case <-runCtx.Done():
			}
		}()
	}

	var (
		mu             sync.Mutex
		results        = make([]SingleResult, len(plans))
		mergeSummaries = make([]string, len(plans))
		wg             sync.WaitGroup
	)

	emitCombined := func() {
		mu.Lock()
		progs := make([]AgentProgress, len(plans))
		for i, sp := range plans {
			progs[i] = sp.progress.clone()
		}
		mu.Unlock()
		_ = ctx.OnUpdate(sdk.ToolResult{
			Content: fmt.Sprintf("Running %d agents...", len(plans)),
			Details: TaskToolDetails{
				ProjectAgentsDir: projDirPtr,
				Results:          []SingleResult{},
				TotalDurationMs:  time.Since(startTime).Milliseconds(),
				Progress:         progs,
			},
		})
	}

	for pos, sp := range plans {
		pos, sp := pos, sp
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Guarantee the pre-registered AgentRef never stays "running" on any
			// exit path (sem.Acquire abort, RunSubagent early return, or panic).
			defer rt.Registry.SettleIfRunning(sessKey, sp.agentID, "aborted")
			if err := rt.sem.Acquire(runCtx); err != nil {
				results[pos] = SingleResult{
					Index:       sp.index,
					ID:          sp.agentID,
					Agent:       sp.spec.agent.Name,
					AgentSource: sp.spec.agent.Source,
					Task:        RenderSubagentUserPrompt(sp.spec.assignment),
					Assignment:  sp.spec.assignment,
					ExitCode:    1,
					Stderr:      "Cancelled before start",
					Error:       "Cancelled before start",
					Aborted:     true,
					AbortReason: "Cancelled before start",
				}
				return
			}
			defer rt.sem.Release()
			noticeEnabled := cfg.SoftRequestBudgetNotice
			runReq := RunRequest{
				SDK:                        longLivedCtx,
				ParentSessionID:            sessKey,
				ParentAgentID:              parentAgentID,
				ParentLocalRoot:            parentLocalRoot,
				Cwd:                        cwd,
				TaskDepth:                  taskDepth,
				Index:                      sp.index,
				ID:                         sp.agentID,
				Agent:                      sp.spec.agent,
				Assignment:                 sp.spec.assignment,
				Context:                    sharedContext,
				Steps:                      sp.spec.item.Steps,
				SolutionSpace:              sp.spec.item.SolutionSpace,
				OutputSchema:               sp.spec.outputSchema,
				SchemaMode:                 sp.spec.schemaMode,
				SchemaSource:               sp.spec.schemaSource,
				OutputSchemaOverridesAgent: sp.spec.outputSchemaOverridesAgent,
				ModelOverride:              sp.spec.modelOverride,
				Effort:                     sp.spec.effort,
				MaxEffort:                  cfg.MaxEffort,
				ArtifactsDir:               artifactsDir,
				MaxRuntimeMs:               cfg.MaxRuntimeMs,
				SoftBudget:                 sp.spec.softBudget,
				SoftBudgetNotice:           &noticeEnabled,
				AgentIdleTTLMs:             cfg.AgentIdleTTLMs,
				MaxDepth:                   cfg.MaxRecursionDepth,
				IRCEnabled:                 !planMode,
				KeepAlive:                  false,
				CustomTools:                sp.spec.customTools,
				Semaphore:                  rt.sem,
				OnProgress: func(p AgentProgress) {
					mu.Lock()
					sp.progress = p.clone()
					mu.Unlock()
					emitCombined()
				},
			}
			if sp.spec.item.Isolated != nil && *sp.spec.item.Isolated {
				results[pos], mergeSummaries[pos] = RunIsolatedSubagent(runCtx, rt.Registry, runReq, cfg.IsolationMerge, cfg.IsolationApply)
			} else {
				results[pos] = RunSubagent(runCtx, rt.Registry, runReq)
			}
		}()
	}
	wg.Wait()

	var (
		contentParts []string
		outputPaths  []string
		usageTotals  Usage
		hasUsage     bool
		anyError     bool
	)
	for i, r := range results {
		if r.ExitCode != 0 || r.Error != "" || r.Aborted {
			anyError = true
		}
		if r.Usage != nil {
			usageTotals.add(*r.Usage)
			hasUsage = true
		}
		if r.OutputPath != "" {
			outputPaths = append(outputPaths, r.OutputPath)
		}
		ref, _ := rt.Registry.GetInSession(sessKey, r.ID)
		contentParts = append(contentParts, FormatTaskResultSummary(r, r.DurationMs, mergeSummaries[i], ref.IsResumable() && !r.Isolated))
	}

	det := TaskToolDetails{
		ProjectAgentsDir: projDirPtr,
		Results:          results,
		TotalDurationMs:  time.Since(startTime).Milliseconds(),
		OutputPaths:      outputPaths,
	}
	if hasUsage {
		det.Usage = &usageTotals
	}
	return sdk.ToolResult{
		Content: strings.Join(contentParts, "\n\n"),
		Details: det,
		IsError: anyError,
	}
}

func taskModeError(text string) sdk.ToolResult {
	return sdk.ToolResult{
		Content: text,
		IsError: true,
		Details: TaskToolDetails{
			ProjectAgentsDir: nil,
			Results:          []SingleResult{},
			TotalDurationMs:  0,
		},
	}
}

func renderStepsBrief(steps []string) string {
	var b strings.Builder
	for i, step := range steps {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d. %s", i+1, strings.TrimSpace(step))
	}
	return b.String()
}

func composeSpawnAdvisory(
	resolved []resolvedSpawn,
	asyncCount int,
	ircEnabled bool,
	depthCapacity bool,
	scoutAvailable bool,
) string {
	if !depthCapacity {
		return ""
	}
	var parts []string
	var generics []string
	seenGeneric := map[string]bool{}
	for _, r := range resolved {
		if r.agent.Name == "task" || r.agent.Name == "sonic" {
			generics = append(generics, r.agent.Name)
			seenGeneric[r.agent.Name] = true
		}
	}
	if len(generics) >= 2 {
		var unique []string
		for _, g := range []string{"task", "sonic"} {
			if seenGeneric[g] {
				unique = append(unique, "`"+g+"`")
			}
		}
		tip := fmt.Sprintf("Tip: this call spawned %d generic workers (%s). Check the agent list for a closer specialist type", len(generics), strings.Join(unique, ", "))
		if scoutAvailable {
			tip += " — e.g. read-only research belongs on `agent: \"scout\"`, which runs on a faster model"
		}
		tip += "."
		parts = append(parts, tip)
	}
	if ircEnabled && asyncCount >= 2 {
		parts = append(parts, fmt.Sprintf("%d siblings run together. Overlapping files? Give each file one owner via `write agent://<id>`; siblings message only to unblock, never to announce edits. `read proc://` shows running jobs and agents.", asyncCount))
	}
	return strings.Join(parts, "\n\n")
}

// Helper for finding files in artifactsDir without importing extra packages elsewhere.
func findArtifactFile(dir, name string) string {
	p := filepath.Join(dir, name)
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return p
	}
	return ""
}

// DetachedSpawnRequest describes a background subagent spawn initiated by a
// slash command (`/tan`, `/cleanse`) rather than a model `task` tool call.
type DetachedSpawnRequest struct {
	Name             string
	AgentDisplayName string
	AgentType        string
	Label            string
	Context          string
	Task             string
	SolutionSpace    string
	ModelSelector    string
	ShareParentLocal bool
}

// SpawnDetached launches a detached background subagent under the default
// runtime, registering it as a jobs.Job (`proc://<jobID>`) and agent
// (`agent://<agentID>`).
func SpawnDetached(ctx sdk.Context, req DetachedSpawnRequest) (jobID, agentID string, err error) {
	rt := DefaultRuntime()
	if rt.resolveSessionID(ctx) == "" {
		return "", "", fmt.Errorf("no active session for detached spawn")
	}
	sessKey := rt.sessionKey(ctx)
	cfg := rt.EffectiveConfig(ctx)
	taskDepth := rt.Registry.TaskDepthFor(sessKey)
	effMaxDepth := EffectiveMaxRecursionDepth(cfg.MaxRecursionDepth)
	if !CanSpawnAtDepth(effMaxDepth, taskDepth) {
		return "", "", fmt.Errorf("cannot spawn detached agent at depth %d (max %d)", taskDepth, effMaxDepth)
	}

	cwd := ctx.Cwd()
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	agentType := strings.TrimSpace(req.AgentType)
	if agentType == "" {
		agentType = DefaultSpawnAgent
	}
	disc := rt.discover(cwd)
	ag, ok := GetAgent(disc.Agents, agentType)
	if !ok {
		return "", "", fmt.Errorf("unknown agent %q", agentType)
	}
	if gate.PlanActive(ctx) {
		ag = CreatePlanModeAgent(ag)
	}

	var modelSel []string
	if ms := strings.TrimSpace(req.ModelSelector); ms != "" {
		modelSel = []string{ms}
	} else if ov := cfg.ModelOverrides[agentType]; len(ov) > 0 {
		modelSel = ov
	} else {
		modelSel = ag.Model
	}

	longLivedCtx := rt.sessionContextFor(sessKey, ctx)
	artifactsDir, _ := rt.sessionArtifactsDir(ctx)
	outMgr := rt.outputManager(sessKey, artifactsDir, cfg.CrossInstance)
	rawID := SanitizeAgentID(req.Name)
	if rawID == "" {
		rawID = GenerateTaskName()
	}
	agentID = outMgr.Allocate(rawID)
	jobID = agentID

	parentAgentID := MainAgentID
	if meta, ok := rt.Registry.ChildMeta(sessKey); ok && meta.AgentID != "" {
		parentAgentID = meta.AgentID
	}
	jobs.BindSessionIfMissing(sessKey, longLivedCtx)
	var parentLocalRoot string
	if req.ShareParentLocal {
		parentLocalRoot, _ = urls.LocalRoot(longLivedCtx)
	}
	if taskDepth == 0 {
		rt.ensureMainRef(sessKey, longLivedCtx, artifactsDir)
	}

	assignment := strings.TrimSpace(req.Task)
	if strings.TrimSpace(req.Context) != "" {
		assignment = strings.TrimSpace(req.Context) + "\n\n" + assignment
	}
	displayLabel := strings.TrimSpace(req.Label)
	if displayLabel == "" {
		displayLabel = agentID
	}
	displayAgent := strings.TrimSpace(req.AgentDisplayName)
	if displayAgent == "" {
		displayAgent = ag.Name
	}

	jobCtx, jobCancel := context.WithCancel(context.Background())
	rt.trackCancel(sessKey, jobCancel)
	initial := AgentProgress{
		ID:          agentID,
		Agent:       displayAgent,
		AgentSource: ag.Source,
		Status:      "running",
		Task:        RenderSubagentUserPrompt(assignment),
		Assignment:  assignment,
	}
	job := NewAgentJob(jobID, displayLabel, jobCancel, initial)
	jobs.Add(sessKey, job)

	softBudget := ResolveSoftRequestBudget(ag.Name, cfg.SoftRequestBudget, cfg.AgentSoftRequestBudget)
	noticeEnabled := cfg.SoftRequestBudgetNotice
	go func() {
		defer jobCancel()
		defer func() {
			if r := recover(); r != nil {
				errStr := fmt.Sprintf("subagent panicked: %v", r)
				job.finish("failed", errStr, errStr, nil, nil, false, nil)
			}
		}()
		if err := rt.sem.Acquire(jobCtx); err != nil {
			job.finish("cancelled", "Aborted before execution", "Aborted before execution", nil, nil, false, nil)
			return
		}
		defer rt.sem.Release()

		sr := RunSubagent(jobCtx, rt.Registry, RunRequest{
			SDK:              longLivedCtx,
			ParentSessionID:  sessKey,
			ParentAgentID:    parentAgentID,
			ParentLocalRoot:  parentLocalRoot,
			Cwd:              cwd,
			TaskDepth:        taskDepth,
			ID:               agentID,
			Agent:            ag,
			Assignment:       assignment,
			SolutionSpace:    req.SolutionSpace,
			ModelOverride:    modelSel,
			MaxEffort:        cfg.MaxEffort,
			ArtifactsDir:     artifactsDir,
			MaxRuntimeMs:     cfg.MaxRuntimeMs,
			SoftBudget:       softBudget,
			SoftBudgetNotice: &noticeEnabled,
			AgentIdleTTLMs:   cfg.AgentIdleTTLMs,
			MaxDepth:         cfg.MaxRecursionDepth,
			IRCEnabled:       !gate.PlanActive(longLivedCtx),
			KeepAlive:        true,
			Semaphore:        rt.sem,
			OnProgress: func(p AgentProgress) {
				job.updateProgress(p, fmt.Sprintf("Running %s %s...", displayAgent, p.ID))
			},
			OnToolResult: func(tool, intent, text string) {
				job.RecordToolCall(tool, intent, text)
			},
		})
		status := "completed"
		var errStr string
		if jobCtx.Err() != nil {
			status = "cancelled"
			errStr = sr.AbortReason
		} else if sr.Aborted || sr.ExitCode != 0 || sr.Error != "" {
			status = "failed"
			errStr = sr.Error
			if errStr == "" {
				errStr = sr.AbortReason
			}
			if errStr == "" {
				errStr = sr.Stderr
			}
		}
		job.finish(status, sr.Output, errStr, &sr, nil, false, nil)
	}()
	return jobID, agentID, nil
}
