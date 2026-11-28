package jobs

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

// upstream: packages/coding-agent/src/session/async-job-delivery.ts,
// packages/coding-agent/src/session/async-notify.ts,
// packages/coding-agent/src/session/harness-tags.ts,
// packages/coding-agent/src/prompts/tools/async-result.md,
// packages/coding-agent/src/prompts/tools/async-notify.md,
// packages/coding-agent/src/async/job-manager.ts

const (
	// AsyncResultMessageType is omp's customType for background job completion follow-ups.
	// upstream: session/async-job-delivery.ts:25
	AsyncResultMessageType = "async-result"

	// AsyncNotifyMessageType is omp's customType for mid-run `notify` regex matches.
	// upstream: tui/chat/messages.ts ASYNC_NOTIFY_MESSAGE_TYPE
	AsyncNotifyMessageType = "async-notify"

	// LaunchCompletionMessageType is omp's customType for supervised service exit follow-ups.
	// upstream: session/launch-completion.ts:6
	LaunchCompletionMessageType = "launch-completion"

	// CommandIdentityMaxChars is the longest command a completion header quotes before eliding its middle.
	// upstream: session/async-job-delivery.ts:114
	CommandIdentityMaxChars = 400

	// NotifyMaxLines caps matching lines per source in one async-notify message.
	// upstream: async/notify.ts:31
	NotifyMaxLines = 50

	// AsyncInlineResultMaxChars is omp's ASYNC_INLINE_RESULT_MAX_CHARS (12,000).
	// upstream: session/async-job-delivery.ts:28
	AsyncInlineResultMaxChars = 12_000
	// AsyncPreviewMaxChars is omp's ASYNC_PREVIEW_MAX_CHARS (4,000).
	// upstream: session/async-job-delivery.ts:29
	AsyncPreviewMaxChars = 4_000
	// AsyncPreviewTailChars is omp's ASYNC_PREVIEW_TAIL_CHARS (1,000).
	// upstream: session/async-job-delivery.ts:34
	AsyncPreviewTailChars = 1_000

	// deliveryRetryBase / deliveryRetryMax match async/job-manager.ts:11-12.
	deliveryRetryBase = 500 * time.Millisecond
	deliveryRetryMax  = 30 * time.Second
	deliveryMaxTries  = 8
)

// JobDetailer is an optional interface a Job may implement to expose richer
// metadata to result delivery, `wait`, and `proc://`.
type JobDetailer interface {
	// Ended returns when the job settled (zero while running).
	Ended() time.Time
	// ErrorText returns failure/cancellation error text, if any.
	ErrorText() string
	// Command returns the full command line for bash jobs (used in headers).
	Command() string
	// Intent returns the model's `i` intent on the call, if set.
	Intent() string
	// Details returns the latest details map for transcript renderers.
	Details() map[string]any
	// SkipDelivery reports whether automatic follow-up delivery is suppressed
	// (e.g., services, cancelled jobs, or foreground jobs not yet backgrounded).
	SkipDelivery() bool
}

// ServiceController is an optional interface a KindService Job may implement so
// `proc://` can inspect state, send stdin, change lifetime mode, and locate logs.
type ServiceController interface {
	ServiceState() string // "starting", "running", "ready", "exited", "stopped"
	ServiceReady() bool
	ServicePID() int
	ServiceMode() string // "session", "persist", "detached"
	SetServiceMode(mode string) error
	SendStdin(content string) error
	ServiceExitCode() *int
	ServiceExitReason() string
	ServiceLogPath() string
}

// StreamInfo describes one pipe or redirected file captured by a bash job.
// upstream: internal-urls/proc-protocol.ts:158
type StreamInfo struct {
	ID       int
	Producer string
	Consumer string
	File     bool
	Open     bool
	Total    int64
}

// StreamChunk is a slice read from one captured stream.
type StreamChunk struct {
	Data  []byte
	Start int64
}

// StreamProvider is an optional interface a KindBash Job may implement to expose
// `proc://<id>/streams[/<n>]`.
type StreamProvider interface {
	ListStreams() []StreamInfo
	ReadStream(streamID int) (StreamChunk, bool)
}

// ResultEntry describes one completed job for batch async-result formatting.
type ResultEntry struct {
	JobID          string
	Kind           Kind
	Status         string
	Label          string
	Command        string // non-empty for bash jobs; sets namedByCommand
	AgentID        string
	Request        string // non-empty for /tan tangents
	Result         string
	DurationMS     int64
	Meta           map[string]any
	ShowAgentURL   bool
	ShowHistoryURL bool
}

// NotifyReport describes one job or service's matching lines for async-notify.
type NotifyReport struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"` // "bash" or "service"
	Command string   `json:"-"`
	Pattern string   `json:"pattern"`
	Lines   []string `json:"lines"`
	Omitted int      `json:"omitted"`
	Running bool     `json:"running"`
	Stopped bool     `json:"stopped"`
}

// EvictedRecord records what became of an evicted job so `proc://<id>` can
// explain instead of saying "not found".
// upstream: async/job-manager.ts:119
type EvictedRecord struct {
	ID        string
	Kind      Kind
	Label     string
	Status    string
	EndTime   time.Time
	EvictedAt time.Time
	Delivered bool
	Reason    string // "expired" or "cleared"
}

// DeliverySink overrides SDK message delivery (primarily for tests).
type DeliverySink func(sessionID, customType, content string, display bool, opts sdk.SendMessageOptions) error

// SpillFunc writes an oversized async-result (>12k chars without an existing
// artifactId) to `local://<N>.log` and returns its URL. Nil disables spilling
// from the delivery layer (preview truncation still applies).
var SpillFunc func(ctx sdk.Context, data []byte) (url string, err error)

// IsSubagentSession reports whether sessionID belongs to a spawned child
// subagent session (optional hook wired by the integrator). When non-nil and
// returning true, PublishJobsHud skips updating Tern's top-level jobs HUD pill.
var IsSubagentSession func(sessionID string) bool

var (
	jobsHudMu      sync.Mutex
	jobsHudDirty   = map[string]bool{}
	jobsHudRunning = map[string]bool{}
)

// PublishJobsHud schedules an asynchronous, coalesced publication of currently
// running non-agent background jobs (bash/service/eval) for sessionID into
// surface.SetJobsHud. Safe to call while callers hold their own locks; never
// blocks on Tern frame I/O and always converges to the latest state.
func PublishJobsHud(sessionID string) {
	if sessionID == "" {
		return
	}
	jobsHudMu.Lock()
	jobsHudDirty[sessionID] = true
	if jobsHudRunning[sessionID] {
		jobsHudMu.Unlock()
		return
	}
	jobsHudRunning[sessionID] = true
	jobsHudMu.Unlock()
	go drainJobsHud(sessionID)
}

func drainJobsHud(sessionID string) {
	for {
		jobsHudMu.Lock()
		if !jobsHudDirty[sessionID] {
			delete(jobsHudRunning, sessionID)
			jobsHudMu.Unlock()
			return
		}
		delete(jobsHudDirty, sessionID)
		jobsHudMu.Unlock()
		publishJobsHudSync(sessionID)
	}
}

func publishJobsHudSync(sessionID string) {
	if sessionID == "" {
		return
	}
	if IsSubagentSession != nil && IsSubagentSession(sessionID) {
		return
	}
	all := List(sessionID)
	now := time.Now()
	items := make([]surface.JobHudItem, 0, len(all))
	for _, j := range all {
		if j == nil || j.Kind() == KindAgent {
			continue
		}
		if doneCh := j.Done(); doneCh != nil {
			select {
			case <-doneCh:
				continue
			default:
			}
		} else if j.Status() != "running" {
			continue
		}
		item := surface.JobHudItem{
			ID:        j.ID(),
			Kind:      string(j.Kind()),
			Label:     j.Label(),
			Status:    "running",
			StartedMs: j.Started().UnixMilli(),
		}
		if wp, ok := j.(WidgetProvider); ok {
			if wv := wp.WidgetView(now); wv != nil {
				if wv.Title != "" {
					item.Label = wv.Title
				}
				item.Summary = wv.Stage
				for _, blk := range wv.Blocks {
					if blk.Kind == "stat" && len(blk.Items) > 0 && item.GlanceText == "" {
						parts := make([]string, 0, len(blk.Items))
						for _, st := range blk.Items {
							if st.Label != "" && st.Text != "" {
								parts = append(parts, st.Label+" "+st.Text)
							} else if st.Text != "" {
								parts = append(parts, st.Text)
							}
						}
						item.GlanceText = strings.Join(parts, " · ")
					}
				}
			}
		}
		items = append(items, item)
	}
	surface.SetJobsHud(len(items), items)
}

type sessionBinding struct {
	ctx   sdk.Context
	epoch uint64
}

// sessionDeliveryState holds all delivery/watch/claim/consumption/eviction
// state for a single session in ONE package-level table keyed by sessionID,
// released via lifecycle.OnEnd("jobs", ...).
type sessionDeliveryState struct {
	binding      sessionBinding
	hasBinding   bool
	epoch        uint64
	claimed      map[string]uint64        // jobID -> non-zero owner claim token
	consumed     map[string]bool          // jobID -> terminal delivered/consumed state
	watched      map[string]int           // jobID -> active Watch refcount
	evicted      map[string]EvictedRecord // jobID -> tombstone
	evictedOrder []string                 // oldest-first evicted job IDs
	cleanupTimer *time.Timer
}

var (
	deliveryMu        sync.Mutex
	deliverySessions  = map[string]*sessionDeliveryState{} // ONE per-session table keyed by sessionID
	deliverySink      DeliverySink
	onSessionSwept    func(sessionID string)        // optional package-internal test hook
	onFinishHandled   func(sessionID, jobID string) // optional package-internal test hook
	onUnwatchDone     func(sessionID, jobID string) // optional package-internal test hook
	consumedListeners = map[uint64]func(sessionID, jobID string){}
	nextConsumedID    uint64
	nextClaimToken    uint64
	deliveryHookSet   bool
	waitNotifyCh      = make(chan struct{})
	retryWakeCh       = make(chan struct{})
)

const maxEvictedPerSession = 256

// sessionTombstoneGrace keeps `cleared` tombstones briefly for in-flight
// queries before freeing the ended session's entry completely.
var sessionTombstoneGrace = 5 * time.Second

func sessionStateLocked(sessionID string, create bool) *sessionDeliveryState {
	if sessionID == "" {
		return nil
	}
	st := deliverySessions[sessionID]
	if st == nil && create {
		st = &sessionDeliveryState{}
		deliverySessions[sessionID] = st
	}
	return st
}

// BindSession records the long-lived sdk.Context (from `session_start`) for
// sessionID so background completions and `notify` matches can inject follow-up
// messages. Unlike a tool-call Context, a session_start Context stays alive
// even when individual tool calls are cancelled.
func BindSession(sessionID string, ctx sdk.Context) {
	var zero sdk.Context
	if sessionID == "" || ctx == zero {
		return
	}
	deliveryMu.Lock()
	st := sessionStateLocked(sessionID, true)
	st.epoch++
	st.binding = sessionBinding{ctx: ctx, epoch: st.epoch}
	st.hasBinding = true
	// Wake any retrying delivery goroutine waiting for a live session binding.
	close(retryWakeCh)
	retryWakeCh = make(chan struct{})
	deliveryMu.Unlock()
}

// BindSessionIfMissing binds ctx only if sessionID has no live Context yet
// (used as a fallback when tests or headless hosts invoke tools without firing
// `session_start`, without clobbering a long-lived session_start Context).
func BindSessionIfMissing(sessionID string, ctx sdk.Context) {
	var zero sdk.Context
	if sessionID == "" || ctx == zero {
		return
	}
	deliveryMu.Lock()
	st := sessionStateLocked(sessionID, true)
	if st.hasBinding {
		// Keep the existing binding unless its underlying context is already dead.
		if st.binding.ctx.Err() == nil {
			deliveryMu.Unlock()
			return
		}
	}
	st.epoch++
	st.binding = sessionBinding{ctx: ctx, epoch: st.epoch}
	st.hasBinding = true
	close(retryWakeCh)
	retryWakeCh = make(chan struct{})
	deliveryMu.Unlock()
}

// UnbindSession drops the bound sdk.Context for sessionID and advances its
// epoch so in-flight delivery retries stop immediately.
func UnbindSession(sessionID string) {
	if sessionID == "" {
		return
	}
	deliveryMu.Lock()
	if st := sessionStateLocked(sessionID, false); st != nil {
		st.binding = sessionBinding{}
		st.hasBinding = false
		st.epoch++
	}
	close(retryWakeCh)
	retryWakeCh = make(chan struct{})
	deliveryMu.Unlock()
}

// SetDeliverySink installs an optional process-wide override for message
// delivery (used by tests); pass nil to restore default sdk.Context delivery.
func SetDeliverySink(fn DeliverySink) {
	deliveryMu.Lock()
	deliverySink = fn
	close(retryWakeCh)
	retryWakeCh = make(chan struct{})
	deliveryMu.Unlock()
}

// EnsureDeliveryListener installs the package-level OnFinish listener and
// `lifecycle.OnEnd("jobs", ...)` cleanup exactly once. Safe to call from every
// `Register` across multiple sessions.
func EnsureDeliveryListener() {
	deliveryMu.Lock()
	if deliveryHookSet {
		deliveryMu.Unlock()
		return
	}
	deliveryHookSet = true
	deliveryMu.Unlock()

	lifecycle.OnEnd("jobs", func(sessionID string) {
		if sessionID == "" {
			return
		}
		ClearSession(sessionID)
		UnbindSession(sessionID)
		scheduleSessionStateSweep(sessionID, sessionTombstoneGrace)
	})
	OnFinish(func(sessionID string, j Job) {
		broadcastChange()
		maybeDeliverJob(sessionID, j)
		deliveryMu.Lock()
		hook := onFinishHandled
		deliveryMu.Unlock()
		if hook != nil && j != nil {
			hook(sessionID, j.ID())
		}
	})
}

// maybeDeliverJob checks eligibility (kind, status, SkipDelivery, watch
// suppression) and kicks off deliverWithRetry for a settled job.
func maybeDeliverJob(sessionID string, j Job) {
	if sessionID == "" || j == nil {
		return
	}
	// Services and cancelled jobs are not delivered as async-result.
	if j.Kind() == KindService || j.Status() == "cancelled" {
		return
	}
	if d, ok := j.(JobDetailer); ok && d.SkipDelivery() {
		return
	}
	if IsWatched(sessionID, j.ID()) {
		return
	}
	if IsConsumed(sessionID, j.ID()) {
		return
	}
	deliverWithRetry(sessionID, j)
}

func deliverWithRetry(sessionID string, j Job) {
	deliveryMu.Lock()
	var startEpoch uint64
	if st := sessionStateLocked(sessionID, false); st != nil {
		startEpoch = st.epoch
	}
	deliveryMu.Unlock()

	backoff := deliveryRetryBase
	for range deliveryMaxTries {
		// Atomically check watch suppression and acquire the single delivery
		// claim BEFORE sending so `wait` and the listener can never double-send.
		release, ok := claimForAutoDelivery(sessionID, j.ID())
		if !ok {
			return
		}
		err := DeliverJob(sessionID, j)
		if err == nil {
			MarkConsumed(sessionID, j.ID())
			release() // no-op after MarkConsumed
			return
		}
		// Release claim on send failure so a later retry or `wait` can deliver.
		release()
		deliveryMu.Lock()
		st := sessionStateLocked(sessionID, false)
		bound := st != nil && st.hasBinding
		var curEpoch uint64
		if st != nil {
			curEpoch = st.epoch
		}
		wake := retryWakeCh
		sink := deliverySink
		deliveryMu.Unlock()
		if !bound && sink == nil && curEpoch != startEpoch {
			return
		}
		timer := time.NewTimer(backoff)
		select {
		case <-wake:
			timer.Stop()
		case <-timer.C:
		}
		if backoff < deliveryRetryMax {
			backoff *= 2
			if backoff > deliveryRetryMax {
				backoff = deliveryRetryMax
			}
		}
	}
}
func broadcastChange() {
	deliveryMu.Lock()
	close(waitNotifyCh)
	waitNotifyCh = make(chan struct{})
	deliveryMu.Unlock()
}

// DeliverJob injects a single settled job's result into sessionID's conversation
// as a follow-up custom message (TriggerTurn: true, DeliverAs: "followUp"),
// matching omp's async-result delivery (including the 12k char preview cap).
// upstream: session/async-job-delivery.ts, prompts/tools/async-result.md
func DeliverJob(sessionID string, j Job) error {
	if j == nil {
		return nil
	}
	entry := entryFromJob(j)
	entry.Result = capAsyncResultForFollowUp(sessionID, entry.Result, entry.Meta)
	return DeliverResults(sessionID, []ResultEntry{entry})
}

// capAsyncResultForFollowUp enforces omp's ASYNC_INLINE_RESULT_MAX_CHARS (12k)
// cap on delivered follow-ups (`agent-session.ts:4113-4147`).
func capAsyncResultForFollowUp(sessionID, result string, meta map[string]any) string {
	runes := []rune(result)
	if len(runes) <= AsyncInlineResultMaxChars {
		return result
	}
	// If the producing tool's output sink already saved an artifact, keep a
	// head+tail preview and link to that spill URL.
	if rawArt := artifactIDFromMeta(meta); rawArt != "" {
		headLen := AsyncPreviewMaxChars - AsyncPreviewTailChars
		if headLen < 0 {
			headLen = 0
		}
		head := string(runes[:min(headLen, len(runes))])
		tailStart := max(headLen, len(runes)-AsyncPreviewTailChars)
		tail := string(runes[tailStart:])
		elided := len(runes) - len([]rune(head)) - len([]rune(tail))
		preview := fmt.Sprintf("%s\n[…%d chars elided…]\n%s", head, elided, tail)
		url := rawArt
		if !strings.Contains(url, "://") {
			url = fmt.Sprintf("local://%s.log", rawArt)
		}
		return fmt.Sprintf("%s\nFull output: %s", preview, url)
	}
	preview := fmt.Sprintf("%s\n\n[Output truncated. Showing first %d characters.]",
		string(runes[:AsyncPreviewMaxChars]), AsyncPreviewMaxChars)
	if SpillFunc != nil {
		deliveryMu.Lock()
		var boundCtx sdk.Context
		if st := sessionStateLocked(sessionID, false); st != nil && st.hasBinding {
			boundCtx = st.binding.ctx
		}
		deliveryMu.Unlock()
		if url, err := SpillFunc(boundCtx, []byte(result)); err == nil && url != "" {
			return fmt.Sprintf("%s\nFull output: %s", preview, url)
		}
	}
	return preview
}

func artifactIDFromMeta(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	if tr, ok := meta["truncation"].(map[string]any); ok {
		if id, ok := tr["artifactId"].(string); ok && id != "" {
			return id
		}
	}
	if lim, ok := meta["limits"].(map[string]any); ok {
		if col, ok := lim["columnTruncated"].(map[string]any); ok {
			if id, ok := col["artifactId"].(string); ok && id != "" {
				return id
			}
		}
	}
	return ""
}

// DeliverResults injects one or more settled job results as a single
// `async-result` follow-up message.
func DeliverResults(sessionID string, entries []ResultEntry) error {
	if len(entries) == 0 {
		return nil
	}
	content := FormatAsyncResultBatch(entries)
	jobDetails := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		row := map[string]any{
			"jobId":  e.JobID,
			"type":   jobTypeString(e.Kind),
			"status": e.Status,
			"label":  e.Label,
		}
		if e.AgentID != "" {
			row["agentId"] = e.AgentID
		}
		if e.DurationMS > 0 {
			row["durationMs"] = e.DurationMS
		}
		if len(e.Meta) > 0 {
			row["meta"] = e.Meta
		}
		jobDetails = append(jobDetails, row)
	}
	details := map[string]any{
		"meta": map[string]any{
			"source": map[string]any{
				"type":  "report",
				"value": "background job delivery",
			},
		},
		"jobs": jobDetails,
	}
	trigger := true
	opts := sdk.SendMessageOptions{
		TriggerTurn: &trigger,
		DeliverAs:   "followUp",
		Details:     details,
	}
	return sendSessionMessage(sessionID, AsyncResultMessageType, content, true, opts)
}

// DeliverNotify injects one or more mid-run `notify` match reports as an
// `async-notify` steering message so PiG delivers it before the next LLM
// request while the job is still running (matching omp's step-boundary yield
// queue drain at `session/agent-session.ts:2706-2744, 11146-11152`) instead of
// waiting until the agent stops.
// upstream: session/async-notify.ts, prompts/tools/async-notify.md, session/agent-session.ts:11146-11152
func DeliverNotify(sessionID string, reports []NotifyReport) error {
	if len(reports) == 0 {
		return nil
	}
	merged := mergeNotifyReports(reports)
	content := FormatAsyncNotifyBatch(merged)
	details := map[string]any{
		"reports": merged,
	}
	trigger := true
	opts := sdk.SendMessageOptions{
		TriggerTurn: &trigger,
		DeliverAs:   "steer",
		Details:     details,
	}
	return sendSessionMessage(sessionID, AsyncNotifyMessageType, content, true, opts)
}

// DeliverServiceExit injects omp's `launch-completion` follow-up when a
// supervised service exits unexpectedly (not when explicitly stopped/disposed).
// upstream: session/launch-completion.ts:18, prompts/session/launch-completion.md
func DeliverServiceExit(sessionID, name, state string, exitCode *int, daemonDetails map[string]any) error {
	var msg string
	if exitCode != nil {
		msg = fmt.Sprintf("Supervised process %s %s with exit code %d.", name, state, *exitCode)
	} else {
		msg = fmt.Sprintf("Supervised process %s %s without an exit code.", name, state)
	}
	trigger := true
	opts := sdk.SendMessageOptions{
		TriggerTurn: &trigger,
		DeliverAs:   "followUp",
		Details: map[string]any{
			"daemons": []map[string]any{daemonDetails},
		},
	}
	return sendSessionMessage(sessionID, LaunchCompletionMessageType, msg, true, opts)
}

func sendSessionMessage(sessionID, customType, content string, display bool, opts sdk.SendMessageOptions) error {
	deliveryMu.Lock()
	sink := deliverySink
	var binding sessionBinding
	var hasCtx bool
	if st := sessionStateLocked(sessionID, false); st != nil && st.hasBinding {
		binding = st.binding
		hasCtx = true
	}
	deliveryMu.Unlock()

	if sink != nil {
		return sink(sessionID, customType, content, display, opts)
	}
	if !hasCtx {
		return fmt.Errorf("no bound session context for %q", sessionID)
	}
	return binding.ctx.SendMessage(customType, content, display, opts)
}

func entryFromJob(j Job) ResultEntry {
	entry := ResultEntry{
		JobID:  j.ID(),
		Kind:   j.Kind(),
		Status: j.Status(),
		Label:  j.Label(),
		Result: j.Result(),
	}
	if d, ok := j.(JobDetailer); ok {
		entry.Command = d.Command()
		if end := d.Ended(); !end.IsZero() && !j.Started().IsZero() {
			ms := end.Sub(j.Started()).Milliseconds()
			if ms < 0 {
				ms = 0
			}
			entry.DurationMS = ms
		}
		if entry.Status == "failed" && d.ErrorText() != "" {
			entry.Result = d.ErrorText()
		}
		if det := d.Details(); det != nil {
			if meta, ok := det["meta"].(map[string]any); ok {
				entry.Meta = meta
			}
		}
	}
	if entry.Result == "" {
		entry.Result = j.Output()
	}
	return entry
}

func jobTypeString(k Kind) string {
	switch k {
	case KindAgent:
		return "task"
	case KindBash:
		return "bash"
	case KindEval:
		return "eval"
	case KindService:
		return "service"
	default:
		return string(k)
	}
}

// CommandIdentity flattens a bash command onto one line, middle-eliding past
// CommandIdentityMaxChars so parallel commands sharing a prefix stay distinct.
// upstream: session/async-job-delivery.ts:120
func CommandIdentity(command string) string {
	oneLine := strings.TrimSpace(command)
	oneLine = strings.ReplaceAll(oneLine, "\r\n", `\n`)
	oneLine = strings.ReplaceAll(oneLine, "\r", `\n`)
	oneLine = strings.ReplaceAll(oneLine, "\n", `\n`)
	runes := []rune(oneLine)
	if len(runes) <= CommandIdentityMaxChars {
		return oneLine
	}
	keep := CommandIdentityMaxChars / 2
	return string(runes[:keep]) + "…" + string(runes[len(runes)-keep:])
}

// harnessTagRE matches `<` opening or closing a harness tag (`irc`,
// `tangent-notice`, or `system-*`), case-insensitively.
// RE2 lacks lookahead, so we check the trailing boundary manually.
// upstream: session/harness-tags.ts:6
var harnessTagRE = regexp.MustCompile(`(?i)<(\s*/?\s*(?:irc|tangent-notice|system-[a-z][a-z-]*))`)

// EscapeHarnessTags prevents job-authored output from closing `<system-notice>`
// or forging harness tags such as `<irc>` or `<system-interrupt>`.
// upstream: session/harness-tags.ts:14
func EscapeHarnessTags(text string) string {
	if !strings.ContainsRune(text, '<') {
		return text
	}
	matches := harnessTagRE.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text) + len(matches)*3)
	last := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		if end < len(text) && isHarnessWordOrDash(text[end]) {
			continue
		}
		b.WriteString(text[last:start])
		b.WriteString("&lt;")
		b.WriteString(text[start+1 : end])
		last = end
	}
	b.WriteString(text[last:])
	return b.String()
}

func isHarnessWordOrDash(c byte) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '_' || c == '-'
}

// FormatAsyncResultMessage renders omp's `<system-notice>` follow-up wrapper
// for a single completed background job.
// upstream: prompts/tools/async-result.md
func FormatAsyncResultMessage(jobID, command, result string) string {
	return FormatAsyncResultBatch([]ResultEntry{{
		JobID:   jobID,
		Kind:    KindBash,
		Command: command,
		Result:  result,
	}})
}

// FormatAsyncResultBatch renders omp's `async-result.md` template for one or
// more completed jobs.
// upstream: packages/coding-agent/src/prompts/tools/async-result.md
func FormatAsyncResultBatch(entries []ResultEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<system-notice>\n")
	multiple := len(entries) > 1
	if multiple {
		fmt.Fprintf(&b, "%d background jobs have completed. Resume your work using the results below.\n\n", len(entries))
	} else {
		first := entries[0]
		headerLabel := headerLabelForEntry(first)
		fmt.Fprintf(&b, "Background job %s", first.JobID)
		if first.Command != "" && headerLabel != "" {
			fmt.Fprintf(&b, " (%s)", EscapeHarnessTags(headerLabel))
		}
		b.WriteString(" has completed.")
		if first.Request == "" {
			b.WriteString("\nResume your work using the result below.")
		}
		b.WriteByte('\n')
	}

	for idx, e := range entries {
		if multiple {
			headerLabel := headerLabelForEntry(e)
			fmt.Fprintf(&b, "── Job %s", e.JobID)
			if headerLabel != "" {
				fmt.Fprintf(&b, " (%s)", EscapeHarnessTags(headerLabel))
			}
			b.WriteString(" ──\n")
		}
		if e.Request != "" {
			b.WriteString("User's /tan tangent, run by a separate agent.\n")
			b.WriteString("Not your task or a new instruction: NEVER continue, redo, or act on it.\n")
			b.WriteString("Briefly tell the user it finished; keep any work in progress unchanged.\n\n")
			b.WriteString("User's request:\n")
			b.WriteString(EscapeHarnessTags(e.Request))
			b.WriteString("\n\nResult:\n")
		}
		b.WriteString(EscapeHarnessTags(e.Result))
		agentURLID := e.AgentID
		if agentURLID == "" {
			agentURLID = e.JobID
		}
		if e.ShowAgentURL {
			fmt.Fprintf(&b, "\n\nAgent: agent://%s, history://%s", agentURLID, agentURLID)
		} else if e.ShowHistoryURL {
			fmt.Fprintf(&b, "\n\nAgent: history://%s", agentURLID)
		}
		if idx < len(entries)-1 {
			b.WriteByte('\n')
		}
	}
	b.WriteString("\n</system-notice>")
	return b.String()
}

func headerLabelForEntry(e ResultEntry) string {
	if e.Command != "" {
		return CommandIdentity(e.Command)
	}
	return e.Label
}

func mergeNotifyReports(reports []NotifyReport) []NotifyReport {
	type key struct {
		typ string
		id  string
	}
	order := make([]key, 0, len(reports))
	byKey := make(map[key]*NotifyReport, len(reports))
	for _, r := range reports {
		k := key{typ: r.Type, id: r.ID}
		existing, ok := byKey[k]
		if !ok {
			cp := r
			cp.Lines = append([]string(nil), r.Lines...)
			byKey[k] = &cp
			order = append(order, k)
			continue
		}
		existing.Lines = append(existing.Lines, r.Lines...)
		existing.Omitted += r.Omitted
		existing.Stopped = existing.Stopped || r.Stopped
		existing.Running = r.Running
	}
	out := make([]NotifyReport, 0, len(order))
	for _, k := range order {
		r := byKey[k]
		if overflow := len(r.Lines) - NotifyMaxLines; overflow > 0 {
			r.Lines = r.Lines[:NotifyMaxLines]
			r.Omitted += overflow
		}
		out = append(out, *r)
	}
	return out
}

// FormatAsyncNotifyBatch renders omp's `async-notify.md` template for one or
// more mid-run notify reports.
// upstream: packages/coding-agent/src/prompts/tools/async-notify.md
func FormatAsyncNotifyBatch(reports []NotifyReport) string {
	if len(reports) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<system-notice>\n")
	for _, r := range reports {
		if r.Type == "service" {
			fmt.Fprintf(&b, "Supervised process %s", r.ID)
		} else {
			cmd := EscapeHarnessTags(CommandIdentity(r.Command))
			fmt.Fprintf(&b, "Background job %s (%s)", r.ID, cmd)
		}
		if r.Running {
			b.WriteString(" is still running")
		} else {
			b.WriteString(" has since ended")
		}
		fmt.Fprintf(&b, ". Its output matched /%s/:\n", EscapeHarnessTags(r.Pattern))
		for _, line := range r.Lines {
			b.WriteString(EscapeHarnessTags(line))
			b.WriteByte('\n')
		}
		if r.Omitted > 0 {
			fmt.Fprintf(&b, "… plus %d not shown; read proc://%s for the output.\n", r.Omitted, r.ID)
		}
		if r.Stopped {
			b.WriteString("It matched too often: `notify` is off for it and later matches are not reported.\n")
		}
	}
	b.WriteString("</system-notice>")
	return b.String()
}

// OnConsumed registers a callback fired whenever MarkConsumed records that a
// job's result reached the model (delivery or `wait`), so managers can re-arm
// the job's eviction timer to the shorter consumed-eviction grace
// (`async/job-manager.ts:1166-1187`). Returns an unsubscribe function.
func OnConsumed(fn func(sessionID, jobID string)) func() {
	if fn == nil {
		return func() {}
	}
	deliveryMu.Lock()
	nextConsumedID++
	id := nextConsumedID
	consumedListeners[id] = fn
	deliveryMu.Unlock()
	return func() {
		deliveryMu.Lock()
		delete(consumedListeners, id)
		deliveryMu.Unlock()
	}
}

// Claim atomically reserves delivery/handover of (sessionID, jobID) for one
// caller, bound to ctx (nil allowed for no context cancellation). Returns
// (release, true) for at most one caller until either release() / Unclaim
// releases it, ctx is cancelled, or MarkConsumed transitions it to the terminal
// consumed state. Safe across all job kinds (`bash`, `agent`, `eval`).
//
// The returned release function is idempotent and a no-op once MarkConsumed
// has been called for (sessionID, jobID). If ctx cancels while the claim is
// still held and unconsumed, the claim is automatically released and any
// unwatched settled job is dispatched through the normal delivery path so an
// abandoned caller cannot strand the result until session end.
func Claim(ctx context.Context, sessionID, jobID string) (release func(), ok bool) {
	if sessionID == "" || jobID == "" {
		return nil, false
	}
	if ctx != nil && ctx.Err() != nil {
		return nil, false
	}
	deliveryMu.Lock()
	st := sessionStateLocked(sessionID, true)
	if st.consumed[jobID] || st.claimed[jobID] != 0 {
		deliveryMu.Unlock()
		return nil, false
	}
	if st.claimed == nil {
		st.claimed = map[string]uint64{}
	}
	nextClaimToken++
	tok := nextClaimToken
	st.claimed[jobID] = tok
	deliveryMu.Unlock()

	var (
		once   sync.Once
		stopMu sync.Mutex
		stopFn func() bool
	)
	releaseToken := func() {
		once.Do(func() {
			stopMu.Lock()
			stop := stopFn
			stopFn = nil
			stopMu.Unlock()
			if stop != nil {
				stop()
			}
			var shouldRedeliver bool
			deliveryMu.Lock()
			if st := sessionStateLocked(sessionID, false); st != nil {
				if st.claimed != nil && st.claimed[jobID] == tok {
					delete(st.claimed, jobID)
					if !st.consumed[jobID] && st.watched[jobID] == 0 {
						shouldRedeliver = true
					}
				}
			}
			deliveryMu.Unlock()
			if shouldRedeliver {
				if j, found := Get(sessionID, jobID); found && j != nil {
					status := j.Status()
					if status == "completed" || status == "failed" {
						go maybeDeliverJob(sessionID, j)
					}
				}
			}
		})
	}
	release = releaseToken

	if ctx != nil && ctx.Done() != nil {
		stop := context.AfterFunc(ctx, releaseToken)
		stopMu.Lock()
		stopFn = stop
		stopMu.Unlock()
	}
	return release, true
}

// claimForAutoDelivery atomically checks that jobID is neither watched,
// consumed, nor already claimed, and claims it for the delivery listener.
func claimForAutoDelivery(sessionID, jobID string) (func(), bool) {
	if sessionID == "" || jobID == "" {
		return nil, false
	}
	deliveryMu.Lock()
	st := sessionStateLocked(sessionID, true)
	if st.consumed[jobID] || st.claimed[jobID] != 0 || st.watched[jobID] > 0 {
		deliveryMu.Unlock()
		return nil, false
	}
	if st.claimed == nil {
		st.claimed = map[string]uint64{}
	}
	nextClaimToken++
	tok := nextClaimToken
	st.claimed[jobID] = tok
	deliveryMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			deliveryMu.Lock()
			if st := sessionStateLocked(sessionID, false); st != nil && st.claimed != nil && st.claimed[jobID] == tok {
				delete(st.claimed, jobID)
			}
			deliveryMu.Unlock()
		})
	}, true
}

// Unclaim releases a non-consumed claim held for (sessionID, jobID) so another
// attempt (retry or `wait`) can deliver the job's result. No-op if the job is
// already MarkConsumed. Prefer calling the release func returned by Claim.
func Unclaim(sessionID, jobID string) {
	if sessionID == "" || jobID == "" {
		return
	}
	deliveryMu.Lock()
	if st := sessionStateLocked(sessionID, false); st != nil && st.claimed != nil {
		delete(st.claimed, jobID)
	}
	deliveryMu.Unlock()
}

// IsWatched reports whether jobID is currently watched by an active `wait` call.
func IsWatched(sessionID, jobID string) bool {
	if sessionID == "" || jobID == "" {
		return false
	}
	deliveryMu.Lock()
	defer deliveryMu.Unlock()
	st := sessionStateLocked(sessionID, false)
	return st != nil && st.watched[jobID] > 0
}

// Watch suppresses automatic async-result delivery for the given job IDs in
// sessionID (mirroring omp's `AsyncJobManager.watchJobs` / `unwatchJobs`,
// `async/job-manager.ts:863-893`), bound to ctx (nil allowed for no context
// cancellation). Overlapping Watch calls are refcounted per job ID.
//
// The returned unwatch function is idempotent. When unwatch() is called OR
// ctx.Done() fires and the last watcher on an ID leaves, any settled
// (`completed`/`failed`), unclaimed, unconsumed job still registered in
// sessionID is dispatched through the listener's normal delivery path in a
// goroutine (so `wait` needs no sender of its own).
func Watch(ctx context.Context, sessionID string, ids []string) (unwatch func()) {
	if sessionID == "" || len(ids) == 0 {
		return func() {}
	}
	if ctx != nil && ctx.Err() != nil {
		return func() {}
	}
	unique := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return func() {}
	}

	deliveryMu.Lock()
	st := sessionStateLocked(sessionID, true)
	if st.watched == nil {
		st.watched = map[string]int{}
	}
	for _, id := range unique {
		st.watched[id]++
	}
	deliveryMu.Unlock()

	var (
		once   sync.Once
		stopMu sync.Mutex
		stopFn func() bool
	)
	unwatch = func() {
		once.Do(func() {
			stopMu.Lock()
			stop := stopFn
			stopFn = nil
			stopMu.Unlock()
			if stop != nil {
				stop()
			}

			var toCheck []string
			deliveryMu.Lock()
			st := sessionStateLocked(sessionID, false)
			if st != nil && st.watched != nil {
				for _, id := range unique {
					n := st.watched[id]
					if n <= 1 {
						delete(st.watched, id)
						if !st.consumed[id] && st.claimed[id] == 0 {
							toCheck = append(toCheck, id)
						}
					} else {
						st.watched[id] = n - 1
					}
				}
			}
			deliveryMu.Unlock()

			for _, id := range toCheck {
				j, ok := Get(sessionID, id)
				if !ok || j == nil {
					continue
				}
				status := j.Status()
				if status != "completed" && status != "failed" {
					continue
				}
				if j.Kind() == KindService {
					continue
				}
				if d, ok := j.(JobDetailer); ok && d.SkipDelivery() {
					continue
				}
				job := j
				go func() {
					maybeDeliverJob(sessionID, job)
					deliveryMu.Lock()
					hook := onUnwatchDone
					deliveryMu.Unlock()
					if hook != nil {
						hook(sessionID, job.ID())
					}
				}()
			}
		})
	}
	if ctx != nil && ctx.Done() != nil {
		stop := context.AfterFunc(ctx, unwatch)
		stopMu.Lock()
		stopFn = stop
		stopMu.Unlock()
	}
	return unwatch
}

// MarkConsumed records that sessionID's job result has already reached the
// model (via auto-delivery or a `wait` snapshot), clears any pending claim,
// and notifies OnConsumed listeners so retention timers can shorten to the
// consumed grace.
func MarkConsumed(sessionID, jobID string) {
	if sessionID == "" || jobID == "" {
		return
	}
	deliveryMu.Lock()
	st := sessionStateLocked(sessionID, true)
	if st.consumed == nil {
		st.consumed = map[string]bool{}
	}
	already := st.consumed[jobID]
	st.consumed[jobID] = true
	delete(st.claimed, jobID)
	var listeners []func(string, string)
	if !already && len(consumedListeners) > 0 {
		listeners = make([]func(string, string), 0, len(consumedListeners))
		for _, fn := range consumedListeners {
			listeners = append(listeners, fn)
		}
	}
	deliveryMu.Unlock()
	for _, fn := range listeners {
		fn(sessionID, jobID)
	}
}

// IsConsumed reports whether jobID's result has already been delivered or read
// by `wait`.
func IsConsumed(sessionID, jobID string) bool {
	if sessionID == "" || jobID == "" {
		return false
	}
	deliveryMu.Lock()
	defer deliveryMu.Unlock()
	st := sessionStateLocked(sessionID, false)
	return st != nil && st.consumed[jobID]
}

// RecordEvicted stores an eviction tombstone for a removed job so `proc://<id>`
// can explain what became of it.
func RecordEvicted(sessionID string, rec EvictedRecord) {
	if sessionID == "" || rec.ID == "" {
		return
	}
	deliveryMu.Lock()
	defer deliveryMu.Unlock()
	if rec.EvictedAt.IsZero() {
		rec.EvictedAt = time.Now()
	}
	st := sessionStateLocked(sessionID, true)
	if st.evicted == nil {
		st.evicted = map[string]EvictedRecord{}
	}
	if _, existed := st.evicted[rec.ID]; !existed {
		st.evictedOrder = append(st.evictedOrder, rec.ID)
	}
	st.evicted[rec.ID] = rec
	for len(st.evictedOrder) > maxEvictedPerSession {
		oldest := st.evictedOrder[0]
		st.evictedOrder = st.evictedOrder[1:]
		delete(st.evicted, oldest)
	}
}

// GetEvicted returns the eviction record for jobID, if any.
func GetEvicted(sessionID, jobID string) (EvictedRecord, bool) {
	deliveryMu.Lock()
	defer deliveryMu.Unlock()
	st := sessionStateLocked(sessionID, false)
	if st == nil || st.evicted == nil {
		return EvictedRecord{}, false
	}
	rec, ok := st.evicted[jobID]
	return rec, ok
}

// DescribeEvicted formats omp's one-line eviction notice.
// upstream: async/job-control.ts:580
func DescribeEvicted(rec EvictedRecord, now time.Time) string {
	settled := ""
	if !rec.EndTime.IsZero() {
		d := now.Sub(rec.EndTime)
		if d < 0 {
			d = 0
		}
		settled = " " + FormatDuration(d) + " ago"
	}
	result := "its result was never read"
	if rec.Delivered {
		result = "its result reached you then (a follow-up message or a wait result)"
	} else if rec.Reason == "cleared" {
		result = "its undelivered result was dropped"
	}
	gone := "its row has since expired"
	if rec.Reason == "cleared" {
		gone = "its row was cleared when your session was reset, replaced, or switched"
	}
	label := strings.Join(strings.Fields(rec.Label), " ")
	return fmt.Sprintf("%s [%s] %s%s — %s; %s; %s.", rec.ID, jobTypeString(rec.Kind), rec.Status, settled, label, result, gone)
}

// Running returns all currently running jobs of sessionID, oldest first.
func Running(sessionID string) []Job {
	all := List(sessionID)
	out := make([]Job, 0, len(all))
	for _, j := range all {
		if j.Status() == "running" {
			out = append(out, j)
		}
	}
	return out
}

// Wait blocks until the named job finishes or ctx is cancelled.
func Wait(ctx context.Context, sessionID, id string) (Job, bool, error) {
	j, ok := Get(sessionID, id)
	if !ok {
		return nil, false, nil
	}
	select {
	case <-j.Done():
		return j, true, nil
	case <-ctx.Done():
		return j, true, ctx.Err()
	}
}

// WaitAny blocks until any currently running non-service job of sessionID
// settles, returning it. Returns (nil, false, nil) immediately if none are
// running.
func WaitAny(ctx context.Context, sessionID string) (Job, bool, error) {
	for {
		deliveryMu.Lock()
		ch := waitNotifyCh
		deliveryMu.Unlock()

		var running []Job
		for _, j := range List(sessionID) {
			if j.Kind() == KindService {
				continue
			}
			if j.Status() == "running" {
				running = append(running, j)
			}
		}
		if len(running) == 0 {
			return nil, false, nil
		}
		for _, j := range running {
			select {
			case <-j.Done():
				return j, true, nil
			default:
			}
		}
		select {
		case <-ch:
			for _, j := range running {
				select {
				case <-j.Done():
					return j, true, nil
				default:
				}
			}
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
}

// CancelAll cancels every running job in sessionID.
func CancelAll(sessionID string) int {
	n := 0
	for _, j := range List(sessionID) {
		if j.Status() == "running" {
			j.Cancel()
			n++
		}
	}
	return n
}

// ClearSession removes all jobs for sessionID, cancelling running ones and
// recording eviction tombstones for settled non-service jobs.
func ClearSession(sessionID string) {
	if sessionID == "" {
		return
	}
	all := List(sessionID)
	for _, j := range all {
		if j.Status() == "running" {
			j.Cancel()
		}
		if j.Kind() != KindService {
			var endTime time.Time
			if d, ok := j.(JobDetailer); ok {
				endTime = d.Ended()
			}
			RecordEvicted(sessionID, EvictedRecord{
				ID:        j.ID(),
				Kind:      j.Kind(),
				Label:     j.Label(),
				Status:    j.Status(),
				EndTime:   endTime,
				Delivered: IsConsumed(sessionID, j.ID()),
				Reason:    "cleared",
			})
		}
		Remove(sessionID, j.ID())
	}
	deliveryMu.Lock()
	if st := sessionStateLocked(sessionID, false); st != nil {
		st.consumed = nil
		st.claimed = nil
		st.watched = nil
	}
	deliveryMu.Unlock()
}

// PurgeSessionState immediately deletes all per-session delivery, claim, watch,
// consumed, tombstone, and epoch state for sessionID.
func PurgeSessionState(sessionID string) {
	if sessionID == "" {
		return
	}
	deliveryMu.Lock()
	if st := deliverySessions[sessionID]; st != nil {
		if st.cleanupTimer != nil {
			st.cleanupTimer.Stop()
			st.cleanupTimer = nil
		}
		if !st.hasBinding {
			delete(deliverySessions, sessionID)
		} else {
			st.claimed = nil
			st.consumed = nil
			st.watched = nil
			st.evicted = nil
			st.evictedOrder = nil
		}
	}
	deliveryMu.Unlock()
}

// scheduleSessionStateSweep frees an ended session's entry in deliverySessions
// after grace (cancelling if the session ID is later rebound).
func scheduleSessionStateSweep(sessionID string, grace time.Duration) {
	if sessionID == "" {
		return
	}
	deliveryMu.Lock()
	st := deliverySessions[sessionID]
	if st == nil {
		deliveryMu.Unlock()
		return
	}
	if st.cleanupTimer != nil {
		st.cleanupTimer.Stop()
		st.cleanupTimer = nil
	}
	epoch := st.epoch
	if grace <= 0 {
		if !st.hasBinding {
			delete(deliverySessions, sessionID)
		} else {
			st.claimed = nil
			st.consumed = nil
			st.watched = nil
			st.evicted = nil
			st.evictedOrder = nil
		}
		deliveryMu.Unlock()
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(grace, func() {
		deliveryMu.Lock()
		cur := deliverySessions[sessionID]
		if cur == nil || cur.cleanupTimer != timer {
			deliveryMu.Unlock()
			return
		}
		cur.cleanupTimer = nil
		// Do not sweep if the same session ID was rebound after End.
		if cur.hasBinding || cur.epoch != epoch {
			deliveryMu.Unlock()
			return
		}
		delete(deliverySessions, sessionID)
		hook := onSessionSwept
		deliveryMu.Unlock()
		if hook != nil {
			hook(sessionID)
		}
	})
	st.cleanupTimer = timer
	deliveryMu.Unlock()
}

// ResetDeliveryForTests clears delivery/watch/claim/eviction/consumed state
// (call alongside ResetForTests).
func ResetDeliveryForTests() {
	deliveryMu.Lock()
	for _, st := range deliverySessions {
		if st.cleanupTimer != nil {
			st.cleanupTimer.Stop()
		}
	}
	deliverySessions = map[string]*sessionDeliveryState{}
	deliverySink = nil
	onSessionSwept = nil
	onFinishHandled = nil
	onUnwatchDone = nil
	consumedListeners = map[uint64]func(sessionID, jobID string){}
	deliveryHookSet = false
	deliveryMu.Unlock()
}

// FormatDuration formats a duration compactly matching omp's render-utils formatDuration.
// upstream: packages/tui/src/render/render-utils.ts formatDuration
func FormatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	ms := d.Milliseconds()
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	secs := float64(ms) / 1000.0
	if secs < 60 {
		return fmt.Sprintf("%.1fs", secs)
	}
	totalSecs := int(ms / 1000)
	mins := totalSecs / 60
	remSecs := totalSecs % 60
	if mins < 60 {
		if remSecs == 0 {
			return fmt.Sprintf("%dm", mins)
		}
		return fmt.Sprintf("%dm%ds", mins, remSecs)
	}
	hours := mins / 60
	remMins := mins % 60
	if remMins == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%dm", hours, remMins)
}

// ListByKind returns jobs of sessionID matching kind, oldest first.
func ListByKind(sessionID string, kind Kind) []Job {
	all := List(sessionID)
	out := make([]Job, 0, len(all))
	for _, j := range all {
		if j.Kind() == kind {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Started().Before(out[k].Started()) })
	return out
}
