package pi_subagents

// Completion notification: the message that wakes the session when a background run ends. Ports notify.ts for the results this
// port writes (single children): buildCompletionDetails, the single and grouped formats, the parser the renderer uses, and the
// notifier with its dedupe, batching and send registry.

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	childOutputPreviewMaxBytes = 4 * 1024
	childOutputPreviewCount    = 8
	previewTruncationMarker    = "...[preview truncated]"
	completionSendTTLMs        = 10 * 60 * 1000
	completionSendRegistryCap  = 4096
	notifyMessageType          = "subagent-notify"
)

// NotifyDetails is what the completion message tells about one run.
type NotifyDetails struct {
	WorkflowReceiptPath string
	AsyncDir            string
	Agent               string
	Status              string // completed, failed, paused, stopped
	Source              string // async or foreground
	TaskInfo            string
	ResultPreview       string
	DurationMs          *int64
	SessionLabel        string
	SessionValue        string
	HandoffPath         string
	WorkflowRunID       string
	ChildRuns           []NotifyChildRun
	ReconciledChild     string
}

// NotifyChildRun names a child run of a workflow in a completion message.
type NotifyChildRun struct{ RunID, WorkflowKey, Agent, Status string }

func truncateUTF8Head(value string, maxBytes int) (string, bool) {
	if len(value) <= maxBytes {
		return value, false
	}
	content := max(0, maxBytes-len(previewTruncationMarker))
	end := content
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + previewTruncationMarker, true
}

const binaryContentPlaceholder = "[binary content omitted for safe display]"

func isUnsafeTerminalCodePoint(c rune) bool {
	terminalControl := (c <= 0x1f && c != 0x09 && c != 0x0a) || (c >= 0x7f && c <= 0x9f)
	bidi := c == 0x061c || c == 0x200e || c == 0x200f || (c >= 0x202a && c <= 0x202e) || (c >= 0x2066 && c <= 0x2069)
	private := (c >= 0xe000 && c <= 0xf8ff) || (c >= 0xf0000 && c <= 0xffffd) || (c >= 0x100000 && c <= 0x10fffd)
	invalid := c >= 0xd800 && c <= 0xdfff
	nonChar := (c >= 0xfdd0 && c <= 0xfdef) || c&0xffff == 0xfffe || c&0xffff == 0xffff
	return terminalControl || bidi || private || invalid || nonChar
}

func looksLikeBinaryContent(text string) bool {
	if strings.ContainsRune(text, 0) {
		return true
	}
	controls, replacements, total := 0, 0, 0
	for _, c := range text {
		total++
		if c <= 0x08 || (c >= 0x0e && c <= 0x1f) {
			controls++
		}
		if c == 0xfffd {
			replacements++
		}
	}
	if total == 0 {
		return false
	}
	return (controls >= 4 && float64(controls)/float64(total) >= 0.1) || (replacements >= 3 && float64(replacements)/float64(total) >= 0.1)
}

// safeTerminalText escapes untrusted text before it reaches a terminal, keeping safe newlines and tabs (display-text.ts).
func safeTerminalText(value string) string {
	n := strings.ReplaceAll(value, "\r\n", "\n")
	if looksLikeBinaryContent(n) {
		return binaryContentPlaceholder
	}
	var b strings.Builder
	for _, c := range n {
		if isUnsafeTerminalCodePoint(c) {
			fmt.Fprintf(&b, "[U+%04X]", c)
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func isDegenerateOutput(text string) bool {
	t := jsTrim(text)
	return t == "" || t == "</think>"
}

func structuredOutputText(v any) string {
	if v == nil {
		return ""
	}
	b := marshalJSON(v, "  ")
	return b
}

// resolveSubagentResultStatus is result-intercom.ts: the one status word a result carries.
func resolveSubagentResultStatus(c map[string]any) string {
	state, _ := c["state"].(string)
	if c["detached"] == true {
		return "detached"
	}
	if c["stopped"] == true || state == "stopped" {
		return "stopped"
	}
	if c["interrupted"] == true || state == "paused" {
		return "paused"
	}
	if state == "running" || state == "queued" {
		return "running"
	}
	if c["success"] == true {
		return "completed"
	}
	exit, hasExit := finiteNumber(c["exitCode"])
	if isUnexplainedProcessSignal(c) && !(hasExit && exit == 0) {
		return "stopped"
	}
	if c["success"] == false {
		return "failed"
	}
	switch state {
	case "complete":
		return "completed"
	case "failed":
		return "failed"
	}
	if hasExit {
		if exit == 0 {
			return "completed"
		}
		return "failed"
	}
	return "failed"
}

func isUnexplainedProcessSignal(c map[string]any) bool {
	sig, _ := c["processSignal"].(string)
	return sig != "" && c["interrupted"] != true && c["timedOut"] != true && c["stopped"] != true && c["turnBudgetExceeded"] != true && c["forcedDrainAfterFinalSuccess"] != true
}

func childInlinePreview(child map[string]any) (preview string, truncated bool, unavailable string) {
	output, _ := child["output"].(string)
	ref := ""
	switch r := child["outputReference"].(type) {
	case string:
		ref = jsTrim(r)
	case map[string]any:
		ref = jsTrim(nonEmptyString(r["path"]))
	}
	referenceOnly := ref != "" && strings.HasPrefix(jsTrim(output), "Output saved to:")
	var raw string
	switch {
	case child["outputState"] == "absent" || referenceOnly:
		raw = structuredOutputText(child["structuredOutput"])
	case isDegenerateOutput(output):
		raw = structuredOutputText(child["structuredOutput"])
		if raw == "" {
			raw = output
		}
	default:
		raw = output
	}
	if jsTrim(raw) == "" {
		if referenceOnly {
			return "", false, "saved output is file-only"
		}
		return "", false, "no safe inline output"
	}
	safe := safeTerminalText(raw)
	if jsTrim(safe) == "" {
		return "", false, "no safe inline output"
	}
	p, t := truncateUTF8Head(safe, childOutputPreviewMaxBytes)
	return p, t, ""
}

// buildCompletionDetails is buildCompletionDetails for one result payload.
func buildCompletionDetails(result map[string]any) NotifyDetails {
	agent := nonEmptyString(result["agent"])
	if agent == "" {
		agent = "unknown"
	}
	summary, _ := result["summary"].(string)
	var children []map[string]any
	if arr, ok := result["results"].([]any); ok {
		for _, e := range arr {
			if m, ok := e.(map[string]any); ok {
				children = append(children, m)
			}
		}
	}
	state, _ := result["state"].(string)
	exit, hasExit := finiteNumber(result["exitCode"])
	stopped := result["stopped"] == true || state == "stopped" || (result["success"] != true && !(hasExit && exit == 0) && isUnexplainedProcessSignal(result))
	for _, c := range children {
		cexit, cHas := finiteNumber(c["exitCode"])
		if c["stopped"] == true || c["status"] == "stopped" || (c["success"] != true && !(cHas && cexit == 0) && isUnexplainedProcessSignal(c)) {
			stopped = true
		}
	}
	paused := !stopped && result["success"] != true && ((hasExit && exit == 0) || state == "paused" || result["interrupted"] == true || strings.HasPrefix(summary, "Paused after interrupt."))
	status := "failed"
	switch {
	case stopped:
		status = "stopped"
	case paused:
		status = "paused"
	case result["success"] == true:
		status = "completed"
	}
	d := NotifyDetails{Agent: agent, Status: status, AsyncDir: jsTrimNonEmpty(result["asyncDir"]), Source: nonEmptyString(result["source"])}
	if idx, ok := finiteNumber(result["taskIndex"]); ok {
		if total, ok := finiteNumber(result["totalTasks"]); ok {
			d.TaskInfo = fmt.Sprintf(" (%s/%s)", jsNumber(idx+1), jsNumber(total))
		}
	}
	if rec, ok := result["workflowReceipt"].(map[string]any); ok {
		d.WorkflowReceiptPath = nonEmptyString(rec["path"])
	}
	if ph, ok := result["parallelHandoff"].(map[string]any); ok {
		d.HandoffPath, _ = ph["path"].(string)
	}
	preview := summary
	if len(children) == 1 && result["mode"] != "workflow" && agent != "workflow" {
		child := children[0]
		if sp, _, _ := childInlinePreview(child); sp != "" {
			directSummary := jsTrim(summary)
			directAgent := agent
			if a, ok := child["agent"].(string); ok {
				directAgent = a
			}
			body := directSummary
			if directAgent != "" && strings.HasPrefix(strings.TrimLeft(summary, " \t\r\n"), directAgent+":\n") {
				body = strings.TrimLeft(summary, " \t\r\n")[len(directAgent)+2:]
			}
			childOut, _ := child["output"].(string)
			degenerate := isDegenerateOutput(childOut) && isDegenerateOutput(body) && structuredOutputText(child["structuredOutput"]) != ""
			noOutput := directSummary == "" || directSummary == "(no output)" || (directAgent != "" && directSummary == directAgent+":\n(no output)")
			if noOutput || degenerate {
				preview = "Structured output:\n" + sp
			}
		}
	}
	d.ResultPreview = preview
	if f, ok := finiteNumber(result["durationMs"]); ok {
		d.DurationMs = new(int64(f))
	}
	d.ReconciledChild, _ = result["reconciledFromDetachedChild"].(string)
	switch {
	case nonEmptyString(result["shareUrl"]) != "":
		d.SessionLabel, d.SessionValue = "Session", nonEmptyString(result["shareUrl"])
	case nonEmptyString(result["shareError"]) != "":
		d.SessionLabel, d.SessionValue = "Session share error", nonEmptyString(result["shareError"])
	case nonEmptyString(result["sessionFile"]) != "":
		d.SessionLabel, d.SessionValue = "Session file", nonEmptyString(result["sessionFile"])
	}
	return d
}

func jsTrimNonEmpty(v any) string {
	s, _ := v.(string)
	return jsTrim(s)
}

func boundedSafeText(value string, maxBytes int) string {
	t, _ := truncateUTF8Head(strings.ReplaceAll(safeTerminalText(value), "\n", `\n`), maxBytes)
	return t
}

func formatResultPreview(d NotifyDetails) string {
	if jsTrim(d.ResultPreview) == "" {
		return "(no output)"
	}
	return d.ResultPreview
}

func formatSessionLine(d NotifyDetails) string {
	if d.SessionValue == "" {
		return ""
	}
	if d.SessionLabel != "" {
		return d.SessionLabel + ": " + d.SessionValue
	}
	return d.SessionValue
}

func formatCorrelationLines(d NotifyDetails) []string {
	var lines []string
	if d.AsyncDir != "" {
		lines = append(lines, "Retention-managed async directory: "+boundedSafeText(d.AsyncDir, 1024))
	}
	if d.WorkflowRunID != "" {
		lines = append(lines, "Workflow run: "+d.WorkflowRunID)
	}
	if len(d.ChildRuns) > 0 {
		var parts []string
		for _, c := range d.ChildRuns {
			label := firstNonEmpty(c.WorkflowKey, c.Agent)
			s := ""
			if c.Status != "" {
				s = " (" + c.Status + ")"
			}
			if label != "" {
				label += "="
			}
			parts = append(parts, label+c.RunID+s)
		}
		lines = append(lines, "Child runs: "+strings.Join(parts, ", "))
	}
	if d.ReconciledChild != "" {
		lines = append(lines, "Reconciled detached child: "+d.ReconciledChild)
	}
	return lines
}

// formatSingleCompletion is the message of one finished run.
func formatSingleCompletion(d NotifyDetails) string {
	session := formatSessionLine(d)
	correlation := formatCorrelationLines(d)
	kind := "Background task"
	if d.Source == "foreground" {
		kind = "Detached foreground task"
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("%s %s: **%s**%s", kind, d.Status, d.Agent, d.TaskInfo))
	if d.WorkflowReceiptPath != "" {
		lines = append(lines, "Workflow receipt: "+d.WorkflowReceiptPath)
	}
	lines = append(lines, "", formatResultPreview(d))
	if d.HandoffPath != "" {
		lines = append(lines, "", "Parallel handoff: "+d.HandoffPath)
	}
	if len(correlation) > 0 && d.HandoffPath == "" {
		lines = append(lines, "")
	}
	lines = append(lines, correlation...)
	if session != "" {
		lines = append(lines, "", session)
	}
	return strings.Join(lines, "\n")
}

// formatGroupedCompletion is the message of several runs that finished together.
func formatGroupedCompletion(details []NotifyDetails) string {
	var heads []string
	for _, d := range details {
		heads = append(heads, fmt.Sprintf("**%s**%s", d.Agent, d.TaskInfo))
	}
	blocks := []string{fmt.Sprintf("Background tasks completed (%d): %s", len(details), strings.Join(heads, ", ")), ""}
	for i, d := range details {
		blocks = append(blocks, fmt.Sprintf("%d. %s%s", i+1, d.Agent, d.TaskInfo))
		if d.WorkflowReceiptPath != "" {
			blocks = append(blocks, "Workflow receipt: "+d.WorkflowReceiptPath)
		}
		blocks = append(blocks, formatResultPreview(d))
		if d.HandoffPath != "" {
			blocks = append(blocks, "Parallel handoff: "+d.HandoffPath)
		}
		blocks = append(blocks, formatCorrelationLines(d)...)
		if s := formatSessionLine(d); s != "" {
			blocks = append(blocks, s)
		}
		blocks = append(blocks, "")
	}
	return strings.TrimRight(strings.Join(blocks, "\n"), " \t\r\n")
}

var notifyHeaderPattern = regexp.MustCompile(`^(Background task|Detached foreground task) (completed|failed|paused|stopped): \*\*(.+?)\*\*(?:\s+(\([^)]*\)))?$`)
var notifySessionLinePattern = regexp.MustCompile(`^(Session|Session file|Session share error):\s+`)

// parseSubagentNotifyContent recovers the details from a notification's text (the renderer's fallback); nil when it is not one.
func parseSubagentNotifyContent(content string) *NotifyDetails {
	lines := strings.Split(content, "\n")
	m := notifyHeaderPattern.FindStringSubmatch(lines[0])
	if m == nil {
		return nil
	}
	receiptHeader := len(lines) > 2 && strings.HasPrefix(lines[1], "Workflow receipt: ") && lines[2] == ""
	d := &NotifyDetails{Agent: m[3], Status: m[2], TaskInfo: m[4]}
	if m[1] == "Detached foreground task" {
		d.Source = "foreground"
	}
	skip := 2
	if receiptHeader {
		d.WorkflowReceiptPath = strings.TrimPrefix(lines[1], "Workflow receipt: ")
		skip = 3
	}
	body := []string{}
	if len(lines) > skip {
		body = lines[skip:]
	}
	sessionIndex := -1
	for i := len(body) - 1; i >= 1; i-- {
		if jsTrim(body[i-1]) == "" && notifySessionLinePattern.MatchString(body[i]) {
			sessionIndex = i
			break
		}
	}
	find := func(prefix string) int {
		for i, l := range body {
			if strings.HasPrefix(l, prefix) {
				return i
			}
		}
		return -1
	}
	handoff, wf, child, rec := find("Parallel handoff: "), find("Workflow run: "), find("Child runs: "), find("Reconciled detached child: ")
	first := len(body)
	for _, i := range []int{sessionIndex, handoff, wf, child, rec} {
		if i >= 0 && i < first {
			first = i
		}
	}
	end := first
	if first > 0 && jsTrim(body[first-1]) == "" {
		end = first - 1
	}
	d.ResultPreview = jsTrim(strings.Join(body[:end], "\n"))
	if d.ResultPreview == "" {
		d.ResultPreview = "(no output)"
	}
	if handoff >= 0 {
		d.HandoffPath = jsTrim(strings.TrimPrefix(body[handoff], "Parallel handoff: "))
	}
	if wf >= 0 {
		d.WorkflowRunID = jsTrim(strings.TrimPrefix(body[wf], "Workflow run: "))
	}
	if rec >= 0 {
		d.ReconciledChild = jsTrim(strings.TrimPrefix(body[rec], "Reconciled detached child: "))
	}
	if sessionIndex >= 0 {
		line := body[sessionIndex]
		sep := strings.Index(line, ":")
		d.SessionLabel = strings.ToLower(line[:sep])
		d.SessionValue = jsTrim(line[sep+1:])
	}
	return d
}

// ── the notifier ──

type pendingCompletion struct {
	key         string
	details     NotifyDetails
	sessionID   string
	ownerID     string
	triggerTurn bool
	resolve     func(bool)
}

// completionSendClaim is the claim of one notification's send (the registry keeps two paths from sending it twice).
type completionSendClaim struct {
	owned   bool
	outcome <-chan bool
	settle  func(bool)
}

type completionSendRegistry struct {
	mu      sync.Mutex
	entries map[string]*sendEntry
	ttl     int64
	cap     int
}

type sendEntry struct {
	outcome     chan bool
	deliveredAt *int64
	settled     bool
}

func newCompletionSendRegistry(ttlMs int64, cap int) *completionSendRegistry {
	return &completionSendRegistry{entries: map[string]*sendEntry{}, ttl: ttlMs, cap: cap}
}

func (r *completionSendRegistry) prune(now int64) {
	for k, e := range r.entries {
		if e.deliveredAt != nil && now-*e.deliveredAt > r.ttl {
			delete(r.entries, k)
		}
	}
	if len(r.entries) <= r.cap {
		return
	}
	for k, e := range r.entries {
		if len(r.entries) <= r.cap {
			break
		}
		if e.deliveredAt != nil {
			delete(r.entries, k)
		}
	}
}

func (r *completionSendRegistry) claim(key string, now int64) completionSendClaim {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(now)
	if e, ok := r.entries[key]; ok {
		return completionSendClaim{owned: false, outcome: e.outcome}
	}
	e := &sendEntry{outcome: make(chan bool, 1)}
	r.entries[key] = e
	return completionSendClaim{owned: true, outcome: e.outcome, settle: func(accepted bool) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.entries[key] != e || e.settled {
			return
		}
		e.settled = true
		if accepted {
			e.deliveredAt = &now
			r.prune(now)
		} else {
			delete(r.entries, key)
		}
		e.outcome <- accepted
		close(e.outcome)
	}}
}

// processCompletionSends is shared by every notifier of this process: a reload's new notifier cannot resend what the old one sent.
var processCompletionSends = newCompletionSendRegistry(completionSendTTLMs, completionSendRegistryCap)

// SendMessageFunc delivers a notification message to the session (ctx.SendCustomMessage with the trigger option).
type SendMessageFunc func(customType, content string, display, triggerTurn bool) error

// NotifierOptions configure a completion notifier.
type NotifierOptions struct {
	BatchConfig map[string]any
	Timers      batchTimers
	Now         func() int64
	Registry    *completionSendRegistry
	// Owns says whether this runtime owns the result of (session, completion owner).
	Owns func(sessionID, ownerID string) bool
	// CurrentSessionID is the active session (foreground results are owned by it).
	CurrentSessionID func() string
}

// CompletionNotifier delivers completion notifications.
type CompletionNotifier struct {
	send     SendMessageFunc
	opts     NotifierOptions
	now      func() int64
	registry *completionSendRegistry
	mu       sync.Mutex
	seen     map[string]int64
	pending  map[string]chan bool
	batchers map[string]*CompletionBatcher[*pendingCompletion]
	cfg      CompletionBatchConfig
	disposed bool
}

func newCompletionNotifier(send SendMessageFunc, o NotifierOptions) *CompletionNotifier {
	n := &CompletionNotifier{send: send, opts: o, now: o.Now, registry: o.Registry, seen: map[string]int64{}, pending: map[string]chan bool{}, batchers: map[string]*CompletionBatcher[*pendingCompletion]{},
		cfg: resolveCompletionBatchConfig(o.BatchConfig, nil)}
	if n.now == nil {
		n.now = nowMs
	}
	if n.registry == nil {
		n.registry = processCompletionSends
	}
	return n
}

func completionBatchKey(result map[string]any) string {
	if s := jsTrimNonEmpty(result["sessionId"]); s != "" {
		return "session:" + s
	}
	if c := jsTrimNonEmpty(result["cwd"]); c != "" {
		return "cwd:" + c
	}
	return "unknown"
}

func (n *CompletionNotifier) settle(items []*pendingCompletion, accepted bool) {
	for _, it := range items {
		n.mu.Lock()
		delete(n.pending, it.key)
		if accepted {
			markSeenWithTTL(n.seen, it.key, n.now(), completionSendTTLMs)
		}
		n.mu.Unlock()
		it.resolve(accepted)
	}
}

func (n *CompletionNotifier) sendItems(items []*pendingCompletion) bool {
	if len(items) == 0 {
		return true
	}
	var details []NotifyDetails
	trigger, display := false, false
	for _, it := range items {
		details = append(details, it.details)
		trigger = trigger || it.triggerTurn
		display = display || it.details.Source == "foreground" || it.details.Status != "completed"
	}
	content := formatGroupedCompletion(details)
	if len(details) == 1 {
		content = formatSingleCompletion(details[0])
	}
	return n.send(notifyMessageType, content, display, trigger) == nil
}

func (n *CompletionNotifier) emit(items []*pendingCompletion) {
	var accepted, rejected []*pendingCompletion
	for _, it := range items {
		var owned bool
		if it.details.Source == "foreground" {
			owned = n.opts.CurrentSessionID != nil && it.sessionID == n.opts.CurrentSessionID()
		} else {
			owned = n.opts.Owns != nil && n.opts.Owns(it.sessionID, it.ownerID)
		}
		if owned {
			accepted = append(accepted, it)
		} else {
			rejected = append(rejected, it)
		}
	}
	n.settle(rejected, false)
	type claimed struct {
		it    *pendingCompletion
		claim completionSendClaim
	}
	var mine []claimed
	for _, it := range accepted {
		c := n.registry.claim(it.key, n.now())
		if c.owned {
			mine = append(mine, claimed{it, c})
			continue
		}
		it := it
		go func() { n.settle([]*pendingCompletion{it}, <-c.outcome) }()
	}
	var items2 []*pendingCompletion
	for _, m := range mine {
		items2 = append(items2, m.it)
	}
	sent := n.sendItems(items2)
	for _, m := range mine {
		m.claim.settle(sent)
	}
	n.settle(items2, sent)
}

func (n *CompletionNotifier) batcher(result map[string]any) *CompletionBatcher[*pendingCompletion] {
	key := completionBatchKey(result)
	b := n.batchers[key]
	if b == nil {
		b = newCompletionBatcher(n.cfg, n.emit, n.opts.Timers, n.now)
		n.batchers[key] = b
	}
	return b
}

// Deliver delivers the completion of a result payload; the channel reports whether the session accepted it.
func (n *CompletionNotifier) Deliver(result map[string]any) <-chan bool {
	out := make(chan bool, 1)
	done := func(v bool) { out <- v; close(out) }
	n.mu.Lock()
	sessionID, hasSession := result["sessionId"].(string)
	if n.disposed || !hasSession {
		n.mu.Unlock()
		done(false)
		return out
	}
	ownerID, _ := result["completionOwnerId"].(string)
	source, _ := result["source"].(string)
	if source == "foreground" {
		if n.opts.CurrentSessionID == nil || sessionID != n.opts.CurrentSessionID() {
			n.mu.Unlock()
			done(false)
			return out
		}
	} else if n.opts.Owns == nil || !n.opts.Owns(sessionID, ownerID) {
		n.mu.Unlock()
		done(false)
		return out
	}
	if result["intercomDelivered"] == true {
		n.mu.Unlock()
		done(true)
		return out
	}
	key := buildCompletionKey(result, "notify")
	if at, ok := n.seen[key]; ok {
		if n.now()-at <= completionSendTTLMs {
			n.mu.Unlock()
			done(true)
			return out
		}
		delete(n.seen, key)
	}
	if inFlight, ok := n.pending[key]; ok {
		n.mu.Unlock()
		go func() { v := <-inFlight; done(v) }()
		return out
	}
	details := buildCompletionDetails(result)
	waiter := make(chan bool, 1)
	n.pending[key] = waiter
	it := &pendingCompletion{key: key, details: details, sessionID: sessionID, ownerID: ownerID, triggerTurn: result["triggerTurn"] != false,
		resolve: func(v bool) { waiter <- v; close(waiter); done(v) }}
	foreground := details.Source == "foreground"
	var b *CompletionBatcher[*pendingCompletion]
	if !foreground {
		b = n.batcher(result)
	}
	n.mu.Unlock()
	switch {
	case foreground:
		n.emit([]*pendingCompletion{it})
	case details.Status != "completed":
		b.Flush()
		n.emit([]*pendingCompletion{it})
	default:
		b.Push(it)
	}
	return out
}

// HasPendingDelivery reports whether a notification is held or being sent.
func (n *CompletionNotifier) HasPendingDelivery() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.pending) > 0
}

// Dispose drops the batched notifications (their results stay on disk for the next session) and stops delivering.
func (n *CompletionNotifier) Dispose() {
	n.mu.Lock()
	if n.disposed {
		n.mu.Unlock()
		return
	}
	n.disposed = true
	bs := make([]*CompletionBatcher[*pendingCompletion], 0, len(n.batchers))
	for _, b := range n.batchers {
		bs = append(bs, b)
	}
	n.batchers = map[string]*CompletionBatcher[*pendingCompletion]{}
	n.mu.Unlock()
	for _, b := range bs {
		n.settle(b.Dispose(), false)
	}
}
