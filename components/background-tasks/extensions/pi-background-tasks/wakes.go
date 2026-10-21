package pi_background_tasks

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
)

// Wake events: output and exit wakes, their budget, voiding and records. upstream: extensions/wake-events.ts.

var notifyModes = []string{notifyAlways, notifyTransition, notifyFirstMatchOnly}

func emptyOutputWakeBudget() *outputWakeBudget { return &outputWakeBudget{} }

// normalizeOutputWakeBudget is a budget read from persistence, a snapshot field or live state, as a fresh value no other
// holder shares. A field of the wrong type reads as its zero, whatever the other fields say. v is a map[string]any (a JSON
// object), an outputWakeBudget or a pointer to one, or nil.
func normalizeOutputWakeBudget(v any) *outputWakeBudget {
	switch b := v.(type) {
	case *outputWakeBudget:
		if b == nil {
			return emptyOutputWakeBudget()
		}
		return normalizeBudgetFields(float64(b.Wakes), float64(b.Bytes), b.Exhausted, b.AnnouncedAt)
	case outputWakeBudget:
		return normalizeBudgetFields(float64(b.Wakes), float64(b.Bytes), b.Exhausted, b.AnnouncedAt)
	case map[string]any:
		count := func(key string) float64 {
			if f, ok := b[key].(float64); ok && !math.IsNaN(f) && !math.IsInf(f, 0) {
				return f
			}
			return 0
		}
		var announced *int64
		if f, ok := b["announcedAt"].(float64); ok {
			n := int64(f)
			announced = &n
		}
		exhausted, _ := b["exhausted"].(bool)
		return normalizeBudgetFields(count("wakes"), count("bytes"), exhausted, announced)
	}
	return emptyOutputWakeBudget()
}

func normalizeBudgetFields(wakes, bytes float64, exhausted bool, announced *int64) *outputWakeBudget {
	out := &outputWakeBudget{Wakes: int(math.Max(0, math.Floor(wakes))), Bytes: int(math.Max(0, math.Floor(bytes))), Exhausted: exhausted}
	if announced != nil {
		n := *announced
		out.AnnouncedAt = &n
	}
	return out
}

// ensureOutputWakeBudget replaces the task's budget with its normalized copy and returns it.
func ensureOutputWakeBudget(t *task) *outputWakeBudget {
	t.OutputWakeBudget = normalizeOutputWakeBudget(t.OutputWakeBudget)
	return t.OutputWakeBudget
}

// outputWakeBudgetLimits are the caps of the budget guard; 0 disables an arm.
type outputWakeBudgetLimits struct{ maxWakes, maxBytes int }

// wouldExhaustOutputWakeBudget reports whether one more output wake of nextWakeBytes would exceed either arm.
func wouldExhaustOutputWakeBudget(b *outputWakeBudget, limits outputWakeBudgetLimits, nextWakeBytes int) bool {
	if b.Exhausted {
		return true
	}
	wakeCap := max(0, limits.maxWakes)
	byteCap := max(0, limits.maxBytes)
	if wakeCap > 0 && b.Wakes+1 > wakeCap {
		return true
	}
	if byteCap > 0 && b.Bytes+max(0, nextWakeBytes) > byteCap {
		return true
	}
	return false
}

func normalizeNotifyMode(v string) string {
	switch v {
	case notifyTransition, notifyFirstMatchOnly, notifyAlways:
		return v
	}
	return notifyAlways
}

// defaultNotifyMode: first-match-only when a pattern is set, transition otherwise.
func defaultNotifyMode(pattern string) string {
	if jsTrim(pattern) != "" {
		return notifyFirstMatchOnly
	}
	return notifyTransition
}

func resolveNotifyMode(value, pattern string) string {
	switch value {
	case notifyAlways, notifyTransition, notifyFirstMatchOnly:
		return value
	}
	return defaultNotifyMode(pattern)
}

// ensureWakeState normalizes the wake fields of a task in place.
func ensureWakeState(t *task) {
	t.NotifyMode = normalizeNotifyMode(t.NotifyMode)
	if t.WakeSequence < 0 {
		t.WakeSequence = 0
	}
	if t.WakeEvents == nil {
		t.WakeEvents = []wakeEvent{}
	}
	if t.PendingWakes == nil {
		t.PendingWakes = []wakePending{}
	}
	if t.LastOutputDedupeBy == nil {
		t.LastOutputDedupeBy = map[string]string{}
	}
	if t.voidedWakes == nil {
		t.voidedWakes = map[int]bool{}
	}
	for _, s := range t.VoidedWakeSequences {
		t.voidedWakes[s] = true
	}
	t.VoidedWakeSequences = sortedVoided(t.voidedWakes)
	ensureOutputWakeBudget(t)
}

// canEmitOutputWake: only a running task nobody asked to stop.
func canEmitOutputWake(t *task) bool { return t.Status == statusRunning && t.stopReason == "" }

func nextWakeSequence(t *task) int {
	ensureWakeState(t)
	t.WakeSequence++
	return t.WakeSequence
}

func scheduleTaskWake(t *task, eventType string, eventAt int64) wakePending {
	ensureWakeState(t)
	pending := wakePending{EventAt: eventAt, EventType: eventType, Sequence: nextWakeSequence(t)}
	t.PendingWakes = append(append([]wakePending{}, t.PendingWakes...), pending)
	return pending
}

func forgetPendingWake(t *task, sequence int) {
	ensureWakeState(t)
	kept := []wakePending{}
	for _, w := range t.PendingWakes {
		if w.Sequence != sequence {
			kept = append(kept, w)
		}
	}
	t.PendingWakes = kept
}

func isWakeVoided(t *task, sequence int) bool {
	ensureWakeState(t)
	if t.voidedWakes[sequence] {
		return true
	}
	for _, s := range t.VoidedWakeSequences {
		if s == sequence {
			return true
		}
	}
	return false
}

// wakeDiagnostic is a record of a wake decision for the diagnostics log.
type wakeDiagnostic struct {
	Action         string `json:"action,omitempty"`
	DedupeKey      string `json:"dedupeKey,omitempty"`
	DeliveredAt    *int64 `json:"deliveredAt,omitempty"`
	EventAt        *int64 `json:"eventAt,omitempty"`
	EventType      string `json:"eventType,omitempty"`
	MatchedPattern string `json:"matchedPattern,omitempty"`
	Reason         string `json:"reason"`
	Sequence       *int   `json:"sequence,omitempty"`
	StopReason     string `json:"stopReason,omitempty"`
	TaskID         string `json:"taskId"`
	TaskStatus     string `json:"taskStatus"`
	Timestamp      int64  `json:"timestamp"`
}

// voidPendingTaskWakes voids every pending wake, so a stop, clear or shutdown turns a wake already scheduled into a
// no-op. It returns how many it voided.
func voidPendingTaskWakes(t *task, action string, logDiagnostic func(wakeDiagnostic), now clockFunc) int {
	ensureWakeState(t)
	pending := t.PendingWakes
	if len(pending) == 0 {
		return 0
	}
	for _, w := range pending {
		t.voidedWakes[w.Sequence] = true
		if logDiagnostic != nil {
			logDiagnostic(wakeDiagnostic{Action: action, EventAt: new(w.EventAt), EventType: w.EventType, Reason: "wake-voided", Sequence: new(w.Sequence), TaskID: t.ID, TaskStatus: t.Status, Timestamp: now()})
		}
	}
	t.VoidedWakeSequences = sortedVoided(t.voidedWakes)
	t.PendingWakes = []wakePending{}
	return len(pending)
}

func recordWakeEvent(t *task, record wakeEvent) {
	ensureWakeState(t)
	events := append(append([]wakeEvent{}, t.WakeEvents...), record)
	if len(events) > maxWakeEvents {
		events = events[len(events)-maxWakeEvents:]
	}
	t.WakeEvents = events
	if record.DroppedReason == "voided" {
		t.voidedWakes[record.Sequence] = true
		t.VoidedWakeSequences = sortedVoided(t.voidedWakes)
	}
}

// recordScheduledOutputDrop records that a scheduled output wake ended without being sent.
func recordScheduledOutputDrop(t *task, pending wakePending, reason string, extra wakeDiagnostic, logDiagnostic func(wakeDiagnostic), now clockFunc) {
	timestamp := now()
	forgetPendingWake(t, pending.Sequence)
	recordWakeEvent(t, wakeEvent{DroppedReason: reason, EventAt: pending.EventAt, EventType: eventOutput, Sequence: pending.Sequence, TaskStatusAtEmit: t.Status})
	d := extra
	d.EventAt, d.EventType, d.Reason, d.Sequence, d.TaskID, d.TaskStatus, d.Timestamp = new(pending.EventAt), eventOutput, reason, new(pending.Sequence), t.ID, t.Status, timestamp
	logDiagnostic(d)
}

func recordWakeDrop(t *task, pending wakePending, reason string, logDiagnostic func(wakeDiagnostic), now clockFunc, extra wakeDiagnostic) {
	timestamp := now()
	forgetPendingWake(t, pending.Sequence)
	recordWakeEvent(t, wakeEvent{DroppedReason: reason, EventAt: pending.EventAt, EventType: pending.EventType, Sequence: pending.Sequence, TaskStatusAtEmit: t.Status})
	logged := reason
	if reason == "voided" {
		logged = "voided-wake-fired"
	}
	d := extra
	d.EventAt, d.EventType, d.Reason, d.Sequence, d.TaskID, d.TaskStatus, d.Timestamp = new(pending.EventAt), pending.EventType, logged, new(pending.Sequence), t.ID, t.Status, timestamp
	logDiagnostic(d)
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// outputDecisionInput is OutputWakeDecisionInput.
type outputDecisionInput struct {
	wakeBudgetLimits *outputWakeBudgetLimits
	dedupeHashes     map[string]string
	eventAt          int64
	logDiagnostic    func(wakeDiagnostic)
	newOutput        string
	newOutputTail    string
	now              clockFunc
	patternMatched   bool
	sequence         *int
}

func outputDedupeKey(t *task) string {
	ensureWakeState(t)
	if k := jsTrim(t.DedupeKey); k != "" {
		return k
	}
	return t.ID
}

// shouldEmitOutputWake decides whether an output wake is sent, and says why not through logDiagnostic.
func shouldEmitOutputWake(t *task, in outputDecisionInput) bool {
	ensureWakeState(t)
	now := in.now
	if now == nil {
		now = realNow
	}
	log := func(reason string, extra wakeDiagnostic) {
		if in.logDiagnostic == nil {
			return
		}
		d := extra
		d.EventAt, d.EventType, d.Sequence, d.TaskID, d.TaskStatus, d.Timestamp, d.Reason = new(in.eventAt), eventOutput, in.sequence, t.ID, t.Status, now(), reason
		in.logDiagnostic(d)
	}
	if !t.NotifyOnOutput {
		log("notify-output-disabled", wakeDiagnostic{})
		return false
	}
	if !canEmitOutputWake(t) {
		log("output-after-stop-suppressed", wakeDiagnostic{StopReason: t.stopReason})
		return false
	}
	if jsTrim(in.newOutput) == "" {
		log("empty-output", wakeDiagnostic{})
		return false
	}
	if !in.patternMatched {
		log("notify-pattern-no-match", wakeDiagnostic{MatchedPattern: t.NotifyPattern})
		return false
	}
	mode := normalizeNotifyMode(t.NotifyMode)
	if mode == notifyFirstMatchOnly && t.NotifyPattern != "" && t.OutputPatternMatched {
		log("first-match-only-suppressed", wakeDiagnostic{MatchedPattern: t.NotifyPattern})
		return false
	}
	if mode == notifyTransition {
		key := outputDedupeKey(t)
		hash := sha256Hex(in.newOutputTail)
		previous, had := "", false
		if in.dedupeHashes != nil {
			previous, had = in.dedupeHashes[key]
		}
		if !had {
			previous, had = t.LastOutputDedupeBy[key]
		}
		t.LastOutputDedupeHash = hash
		next := make(map[string]string, len(t.LastOutputDedupeBy)+1)
		for k, v := range t.LastOutputDedupeBy {
			next[k] = v
		}
		next[key] = hash
		t.LastOutputDedupeBy = next
		if in.dedupeHashes != nil {
			in.dedupeHashes[key] = hash
		}
		if had && previous == hash {
			log("output-transition-dedupe", wakeDiagnostic{DedupeKey: key})
			return false
		}
	}
	if in.wakeBudgetLimits != nil {
		budget := ensureOutputWakeBudget(t)
		if wouldExhaustOutputWakeBudget(budget, *in.wakeBudgetLimits, len(in.newOutputTail)) {
			log("wake-budget-exhausted", wakeDiagnostic{})
			return false
		}
	}
	return true
}

// noteOutputWakeSent marks a first-match-only task as matched once its wake went out.
func noteOutputWakeSent(t *task) {
	ensureWakeState(t)
	if normalizeNotifyMode(t.NotifyMode) == notifyFirstMatchOnly && t.NotifyPattern != "" {
		t.OutputPatternMatched = true
	}
}

// customMessage is the pi.sendMessage message of a wake or notice.
type customMessage struct {
	Content    string `json:"content"`
	CustomType string `json:"customType"`
	Details    any    `json:"details"`
	Display    bool   `json:"display"`
}

// sendOptions is the delivery of a wake.
type sendOptions struct {
	DeliverAs   string `json:"deliverAs"`
	TriggerTurn bool   `json:"triggerTurn"`
}

// eventDetails is BackgroundTaskEventDetails, in the original's key order.
type eventDetails struct {
	DeliveredAt         int64           `json:"deliveredAt"`
	EventAt             int64           `json:"eventAt"`
	EventType           string          `json:"eventType"`
	MatchedPattern      string          `json:"matchedPattern,omitempty"`
	OutputTail          string          `json:"outputTail"`
	OutputTailTruncated bool            `json:"outputTailTruncated"`
	Sequence            int             `json:"sequence"`
	Task                compactSnapshot `json:"task"`
	TaskStatusAtEmit    string          `json:"taskStatusAtEmit"`
}

// sendTaskWakeDeps is SendTaskWakeDeps.
type sendTaskWakeDeps struct {
	isShuttingDown   func() bool
	logDiagnostic    func(wakeDiagnostic)
	messageType      string
	now              clockFunc
	outputTail       func(*task) string
	rememberSnapshot func(*task) *snapshot
	sendMessage      func(customMessage, sendOptions)
}

// sendTaskWakeOptions is SendTaskWakeOptions.
type sendTaskWakeOptions struct {
	eventAt        *int64
	matchedPattern string
	newOutputTail  string
	sequence       *int
}

func pickOutputTail(deps sendTaskWakeDeps, t *task, o sendTaskWakeOptions) (string, bool) {
	if o.newOutputTail != "" {
		return o.newOutputTail, hasPrefix(o.newOutputTail, "[...truncated]")
	}
	tail := deps.outputTail(t)
	return tail, hasPrefix(tail, "[...truncated]")
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

// compactBackgroundTaskSnapshot is the transcript-safe view of a snapshot: text fields bounded, internal state dropped.
func compactBackgroundTaskSnapshot(s *snapshot) compactSnapshot {
	bound := func(v string) string { return truncateForTranscript(v, wakeManifestFieldMaxChars) }
	c := compactSnapshot{
		Command: bound(s.Command), Cwd: bound(s.Cwd), DedupeKey: bound(s.DedupeKey), ExitCode: s.ExitCode, ExitNotified: s.ExitNotified,
		ExpiresAt: s.ExpiresAt, ID: s.ID, LastOutputAt: s.LastOutputAt, LogFile: bound(s.LogFile), NotifyMode: s.NotifyMode,
		NotifyOnExit: s.NotifyOnExit, NotifyOnOutput: s.NotifyOnOutput, NotifyPattern: bound(s.NotifyPattern), OutputBytes: s.OutputBytes,
		Pid: s.Pid, SessionID: s.SessionID, StartedAt: s.StartedAt, Status: s.Status, TerminationReason: s.TerminationReason,
		Title: bound(s.Title), UpdatedAt: s.UpdatedAt,
	}
	if rc := s.ResourceControl; rc != nil {
		c.ResourceControl = &resourceControlMeta{Mode: rc.Mode, RequestedMode: rc.RequestedMode, UnitName: bound(rc.UnitName), Warning: bound(rc.Warning)}
	}
	return c
}

// sendTaskWake sends one wake (an output or an exit) unless it was voided, the session is shutting down, the event is
// disabled or the task is stopping. It reports whether a message went out.
func sendTaskWake(deps sendTaskWakeDeps, eventType string, t *task, o sendTaskWakeOptions) bool {
	ensureWakeState(t)
	now := deps.now
	if now == nil {
		now = realNow
	}
	var pending wakePending
	pending.EventType = eventType
	switch {
	case o.eventAt != nil:
		pending.EventAt = *o.eventAt
	case eventType == eventOutput && t.LastOutputAt != nil:
		pending.EventAt = *t.LastOutputAt
	case eventType == eventOutput:
		pending.EventAt = now()
	default:
		pending.EventAt = t.UpdatedAt
	}
	if o.sequence != nil {
		pending.Sequence = *o.sequence
	} else {
		pending.Sequence = nextWakeSequence(t)
	}

	switch {
	case isWakeVoided(t, pending.Sequence):
		recordWakeDrop(t, pending, "voided", deps.logDiagnostic, now, wakeDiagnostic{})
		return false
	case deps.isShuttingDown():
		recordWakeDrop(t, pending, "shutting-down", deps.logDiagnostic, now, wakeDiagnostic{})
		return false
	case eventType == eventOutput && !t.NotifyOnOutput:
		recordWakeDrop(t, pending, "notify-output-disabled", deps.logDiagnostic, now, wakeDiagnostic{})
		return false
	case eventType == eventOutput && !canEmitOutputWake(t):
		recordWakeDrop(t, pending, "output-after-stop-suppressed", deps.logDiagnostic, now, wakeDiagnostic{StopReason: t.stopReason})
		return false
	case eventType == eventExit && !t.NotifyOnExit:
		recordWakeDrop(t, pending, "notify-exit-disabled", deps.logDiagnostic, now, wakeDiagnostic{})
		return false
	}

	if eventType == eventOutput {
		noteOutputWakeSent(t)
	}
	deliveredAt := now()
	recordWakeEvent(t, wakeEvent{DeliveredAt: &deliveredAt, EventAt: pending.EventAt, EventType: eventType, Sequence: pending.Sequence, TaskStatusAtEmit: t.Status})
	forgetPendingWake(t, pending.Sequence)

	tail, truncated := pickOutputTail(deps, t, o)
	if eventType == eventOutput {
		budget := ensureOutputWakeBudget(t)
		budget.Wakes++
		budget.Bytes += len(tail)
	}
	compact := compactBackgroundTaskSnapshot(deps.rememberSnapshot(t))
	details := eventDetails{
		DeliveredAt: deliveredAt, EventAt: pending.EventAt, EventType: eventType,
		MatchedPattern: truncateForTranscript(o.matchedPattern, wakeManifestFieldMaxChars),
		OutputTail:     tail, OutputTailTruncated: truncated, Sequence: pending.Sequence, Task: compact, TaskStatusAtEmit: t.Status,
	}
	headline := "Background task " + t.ID + " emitted new output."
	if eventType == eventExit {
		headline = "Background task " + t.ID + " finished."
	}
	commandPreview := truncateForTranscript(t.Command, wakeContentCommandMaxChars)
	msg := customMessage{Content: headline + "\nCommand: " + commandPreview, CustomType: deps.messageType, Details: details, Display: true}
	if eventType == eventExit {
		deps.sendMessage(msg, sendOptions{DeliverAs: "followUp", TriggerTurn: true})
	} else {
		deps.sendMessage(msg, sendOptions{DeliverAs: "steer", TriggerTurn: true})
	}
	return true
}

// sendTaskNoticeDeps is SendTaskNoticeDeps.
type sendTaskNoticeDeps struct {
	logDiagnostic    func(wakeDiagnostic)
	messageType      string
	now              clockFunc
	rememberSnapshot func(*task) *snapshot
	sendMessage      func(customMessage, sendOptions)
}

// matcherTimeoutDetails is the details of the once-only matcher deadline notice.
type matcherTimeoutDetails struct {
	DeliveredAt    int64           `json:"deliveredAt"`
	EventType      string          `json:"eventType"`
	Reason         string          `json:"reason"`
	Error          string          `json:"error"`
	MatchedPattern string          `json:"matchedPattern"`
	LogFile        string          `json:"logFile"`
	Task           compactSnapshot `json:"task"`
}

// sendOutputMatcherTimeoutNotice reports the matcher's deadline failure without consuming an output wake.
func sendOutputMatcherTimeoutNotice(deps sendTaskNoticeDeps, t *task, errText string) {
	now := deps.now
	if now == nil {
		now = realNow
	}
	timestamp := now()
	bound := func(v string) string { return truncateForTranscript(v, wakeManifestFieldMaxChars) }
	logFile, pattern, e := bound(t.LogFile), bound(t.NotifyPattern), bound(errText)
	deps.sendMessage(customMessage{
		Content:    "Background task " + t.ID + ": " + e + "\nPattern: " + pattern + "\nFull log: " + logFile,
		CustomType: deps.messageType,
		Details: matcherTimeoutDetails{DeliveredAt: timestamp, EventType: "output-matcher-timeout", Reason: "notify-pattern-timeout", Error: e,
			MatchedPattern: pattern, LogFile: logFile, Task: compactBackgroundTaskSnapshot(deps.rememberSnapshot(t))},
		Display: true,
	}, sendOptions{DeliverAs: "steer", TriggerTurn: true})
}

type wakeBudgetDetail struct {
	AnnouncedAt *int64 `json:"announcedAt"`
	Bytes       int    `json:"bytes"`
	MaxBytes    int    `json:"maxBytes"`
	MaxWakes    int    `json:"maxWakes"`
	Wakes       int    `json:"wakes"`
}

type budgetExhaustedDetails struct {
	DeliveredAt int64            `json:"deliveredAt"`
	EventType   string           `json:"eventType"`
	LogFile     string           `json:"logFile"`
	OutputBytes int64            `json:"outputBytes"`
	Task        compactSnapshot  `json:"task"`
	WakeBudget  wakeBudgetDetail `json:"wakeBudget"`
}

// sendOutputWakeBudgetExhaustedNotice reports budget exhaustion once and points at the full on-disk log. It reports whether
// the notice went out now.
func sendOutputWakeBudgetExhaustedNotice(deps sendTaskNoticeDeps, t *task, limits outputWakeBudgetLimits) bool {
	budget := ensureOutputWakeBudget(t)
	if budget.AnnouncedAt != nil {
		return false
	}
	now := deps.now
	if now == nil {
		now = realNow
	}
	timestamp := now()
	budget.Exhausted = true
	budget.AnnouncedAt = &timestamp
	logFile := truncateForTranscript(t.LogFile, wakeManifestFieldMaxChars)
	maxWakes, maxBytes := max(0, limits.maxWakes), max(0, limits.maxBytes)
	content := "Background task " + t.ID + " output wake budget exhausted; further output wakes suppressed.\n" +
		"Inspect the full log with bg_task log id: " + t.ID + " (or pid: " + itoa(t.Pid) + "); on disk at " + logFile + ".\n" +
		"Budget caps: " + itoa(maxWakes) + " wakes / " + itoa(maxBytes) + " inline bytes."
	compact := compactBackgroundTaskSnapshot(deps.rememberSnapshot(t))
	announced := timestamp
	deps.sendMessage(customMessage{
		Content: content, CustomType: deps.messageType,
		Details: budgetExhaustedDetails{DeliveredAt: timestamp, EventType: "output-budget-exhausted", LogFile: logFile, OutputBytes: t.OutputBytes, Task: compact,
			WakeBudget: wakeBudgetDetail{AnnouncedAt: &announced, Bytes: budget.Bytes, MaxBytes: maxBytes, MaxWakes: maxWakes, Wakes: budget.Wakes}},
		Display: true,
	}, sendOptions{DeliverAs: "steer", TriggerTurn: true})
	deps.logDiagnostic(wakeDiagnostic{EventType: eventOutput, Reason: "wake-budget-exhausted", TaskID: t.ID, TaskStatus: t.Status, Timestamp: timestamp})
	return true
}

var _ = sort.Ints
