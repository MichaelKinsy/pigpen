package warden

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Hold feedback loop (src/holds.ts). Every hold is a prediction ("this call should not run as it stands") and every
// allowed call the opposite one; what the user does next labels them, so hold precision can be measured on real
// sessions instead of the synthetic cases the thresholds came from:
//
//   - approved: the user's reply released the hold (steer mode) or the confirm dialog allowed it. False positive.
//   - declined: the confirm dialog refused it. True positive.
//   - replanned: the user replied, the following turn ended, and nobody approved the call. True positive.
//   - regretted: the user's next message tells the agent to stop, undo, or not do an allowed call. False negative.
//   - accepted: the user's next message was checked and does not regret the allowed calls of the last turn.
//
// Approval, decline, and re-plan are code only. Regret is one question that rides the first action request under the
// new prompt (no extra request); offline a stop-word heuristic stands in. Calls the guard skipped as read-only are not
// recorded: they could never have been held. Records are redacted for the log: tool, pattern ids, scores, level,
// outcome, never the command or path. The in-memory summary serves the regret question and stays in memory.

// CallOutcome is what became of a call.
type CallOutcome string

// The outcomes.
const (
	OutcomePending   CallOutcome = "pending"
	OutcomeApproved  CallOutcome = "approved"
	OutcomeDeclined  CallOutcome = "declined"
	OutcomeReplanned CallOutcome = "replanned"
	OutcomeRegretted CallOutcome = "regretted"
	OutcomeAccepted  CallOutcome = "accepted"
)

// OutcomeVia is how the outcome was learned.
type OutcomeVia string

// The ways.
const (
	ViaRetry      OutcomeVia = "retry"
	ViaDialog     OutcomeVia = "dialog"
	ViaNextPrompt OutcomeVia = "next prompt"
	ViaJev        OutcomeVia = "jev"
	ViaText       OutcomeVia = "text"
	ViaDeny       OutcomeVia = "deny"
)

// CallScores are the judge's scores for a call, in the log's key order (holds.ts CallScores).
type CallScores struct {
	Irreversible   float64  `json:"irreversible"`
	OffTask        float64  `json:"offTask"`
	Scope          string   `json:"scope"`
	Mutates        *float64 `json:"mutates,omitempty"`
	Approved       *float64 `json:"approved,omitempty"`
	IntentMismatch *float64 `json:"intentMismatch,omitempty"`
	Visible        *float64 `json:"visible,omitempty"`
	SecurityRisk   *float64 `json:"securityRisk,omitempty"`
	LargeOutput    *float64 `json:"largeOutput,omitempty"`
}

// CallRecord is one inspected call. Its JSON is one line of the hold log, with the original's key order.
type CallRecord struct {
	// ID is the sequence number within the session; the regret question names candidates by it.
	ID    int
	At    int64
	Tool  string
	Level Level
	// Source is the verdict's source: skipped | read-only | pattern | typesafe | error.
	Source   string
	Mode     string
	Held     bool
	Patterns []string
	// Reasons are the labels as shown to the user; they name patterns and scores, never the command.
	Reasons []string
	// PlanChars is the length of the agent's stated plan sent with the call (UTF-16 units); 0 means the agent said
	// nothing before calling.
	PlanChars int
	Outcome   CallOutcome
	// CallExcerpt is tool + path (commands never reach the log).
	CallExcerpt string
	Scores      *CallScores
	OutcomeAt   *int64
	OutcomeVia  OutcomeVia
	// Regret is P(regret) from the question that labelled this call, when the judge answered it.
	Regret *float64
}

// MarshalJSON writes the keys in the order the original assigns them.
func (r CallRecord) MarshalJSON() ([]byte, error) {
	type line struct {
		ID          int         `json:"id"`
		At          int64       `json:"at"`
		Tool        string      `json:"tool"`
		Level       Level       `json:"level"`
		Source      string      `json:"source"`
		Mode        string      `json:"mode"`
		Held        bool        `json:"held"`
		Patterns    []string    `json:"patterns"`
		Reasons     []string    `json:"reasons"`
		PlanChars   int         `json:"planChars"`
		Outcome     CallOutcome `json:"outcome"`
		CallExcerpt string      `json:"callExcerpt,omitempty"`
		Scores      *CallScores `json:"scores,omitempty"`
		OutcomeAt   *int64      `json:"outcomeAt,omitempty"`
		OutcomeVia  OutcomeVia  `json:"outcomeVia,omitempty"`
		Regret      *float64    `json:"regret,omitempty"`
	}
	l := line(r)
	if l.Patterns == nil {
		l.Patterns = []string{}
	}
	if l.Reasons == nil {
		l.Reasons = []string{}
	}
	return marshalNoEscape(l)
}

// HoldSnapshot is the counts of one session.
type HoldSnapshot struct {
	Holds     int
	Approved  int
	Declined  int
	Replanned int
	// Awaiting: holds without a label yet: the user has not replied, or the turn after the reply is still running.
	Awaiting  int
	Allowed   int
	Regretted int
	Accepted  int
	// Labels: labelled holds: approved + declined + replanned.
	Labels int
	// Precision is (declined + replanned) / labels; nil without labels.
	Precision *float64
}

type tracked struct {
	record  *CallRecord
	summary ActionSummary
	// prompts that have arrived since the call.
	prompts int
}

// regretThreshold is the P(regret) from which the question counts as regret.
const regretThreshold = 0.7

func scoresOf(v Verdict) *CallScores {
	j := v.Judgment
	if j == nil {
		return nil
	}
	return &CallScores{Irreversible: j.Irreversible, OffTask: j.OffTask, Scope: j.Scope, Mutates: j.Mutates, Approved: j.Approved,
		IntentMismatch: j.IntentMismatch, Visible: j.Visible, SecurityRisk: j.SecurityRisk, LargeOutput: j.LargeOutput}
}

// HoldOptions describe one inspected call.
type HoldOptions struct {
	Held bool
	Mode string
	// Outcome is set at once when the confirm dialog decided; a steer-mode hold starts pending.
	Outcome CallOutcome
	Via     OutcomeVia
	// At is the time of the call in milliseconds; zero means now.
	At int64
}

// HoldLedger keeps what became of each judged call of the session (holds.ts HoldLedger).
type HoldLedger struct {
	mu        sync.Mutex
	tracked   []*tracked
	untracked []*CallRecord
	nextID    int
}

// NewHoldLedger returns an empty ledger.
func NewHoldLedger() *HoldLedger { return &HoldLedger{nextID: 1} }

func nowMs() int64 { return time.Now().UnixMilli() }

// Record notes one inspected call.
func (l *HoldLedger) Record(v Verdict, o HoldOptions) *CallRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	at := o.At
	if at == 0 {
		at = nowMs()
	}
	patterns := make([]string, 0, len(v.Patterns))
	for _, p := range v.Patterns {
		patterns = append(patterns, p.ID)
	}
	outcome := o.Outcome
	if outcome == "" {
		outcome = OutcomePending
	}
	r := &CallRecord{ID: l.nextID, At: at, Tool: v.Summary.Tool, Level: v.Level, Source: v.Source, Mode: o.Mode, Held: o.Held,
		Patterns: patterns, Reasons: append([]string{}, v.Reasons...), PlanChars: utf16Len(v.Plan), Outcome: outcome}
	l.nextID++
	// Call excerpt for auditing: tool + path only (commands never reach the log).
	if v.Summary.Path != "" {
		r.CallExcerpt = v.Summary.Tool + " " + v.Summary.Path
	} else if v.Summary.Tool != "" {
		r.CallExcerpt = v.Summary.Tool
	}
	r.Scores = scoresOf(v)
	if o.Outcome != "" && o.Outcome != OutcomePending {
		r.OutcomeAt = &at
		r.OutcomeVia = o.Via
	}
	l.tracked = append(l.tracked, &tracked{record: r, summary: v.Summary})
	return r
}

func (l *HoldLedger) label(item *tracked, outcome CallOutcome, via OutcomeVia, at int64) *CallRecord {
	item.record.Outcome = outcome
	item.record.OutcomeAt = &at
	item.record.OutcomeVia = via
	return item.record
}

// Approved: the user's reply released a hold. The pending hold of the same tool is the false positive, the latest
// one when the tools differ (the retry rarely repeats the held call byte for byte, and a hold can be released
// through another tool). Nil when nothing is pending. at zero means now.
func (l *HoldLedger) Approved(tool string, via OutcomeVia, at int64) *CallRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	if via == "" {
		via = ViaRetry
	}
	if at == 0 {
		at = nowMs()
	}
	var pending []*tracked
	for _, item := range l.tracked {
		if item.record.Held && item.record.Outcome == OutcomePending {
			pending = append(pending, item)
		}
	}
	var match *tracked
	for _, item := range pending {
		if item.record.Tool == tool {
			match = item
		}
	}
	if match == nil && len(pending) > 0 {
		match = pending[len(pending)-1]
	}
	if match == nil {
		return nil
	}
	return l.label(match, OutcomeApproved, via, at)
}

// PromptArrived notes a new user prompt. Every pending record has seen one more prompt. A hold still pending after
// the prompt that could have approved it (the reply) and the turn that followed is a re-plan: the agent found
// another way or the user said no. at zero means now.
func (l *HoldLedger) PromptArrived(at int64) []*CallRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	if at == 0 {
		at = nowMs()
	}
	var changed []*CallRecord
	for _, item := range l.tracked {
		if item.record.Outcome != OutcomePending {
			continue
		}
		item.prompts++
		if item.record.Held && item.prompts >= 2 {
			changed = append(changed, l.label(item, OutcomeReplanned, ViaNextPrompt, at))
		}
	}
	return changed
}

func clipText(text string, limit int) string {
	if utf16Len(text) <= limit {
		return text
	}
	return utf16Slice(text, 0, limit) + "…"
}

func (l *HoldLedger) candidateItems() []*tracked {
	var out []*tracked
	for _, item := range l.tracked {
		if !item.record.Held && item.record.Outcome == OutcomePending && item.prompts == 1 {
			out = append(out, item)
		}
	}
	return out
}

// Candidates are the allowed calls of the turn the user just replied to, oldest first: the calls the reply could regret.
func (l *HoldLedger) Candidates() []PreviousAction {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []PreviousAction{}
	for _, item := range l.candidateItems() {
		a := PreviousAction{ID: "a" + strconv.Itoa(item.record.ID), Tool: item.summary.Tool, Path: item.summary.Path}
		if item.summary.Command != "" {
			a.Command = clipText(item.summary.Command, 300)
		}
		out = append(out, a)
	}
	return out
}

// RegretResult is how the user's reply was read for regret.
type RegretResult struct {
	Regretted bool
	// Target names the candidate ("a<id>") the judge located; empty falls on the latest candidate.
	Target      string
	Probability *float64
	Via         OutcomeVia
}

// Regret: the reply was read. With regret, the located call (or the latest candidate) is the miss and the rest are
// accepted; without it, every candidate is accepted. It returns the records that changed. at zero means now.
func (l *HoldLedger) Regret(r RegretResult, at int64) []*CallRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	if at == 0 {
		at = nowMs()
	}
	candidates := l.candidateItems()
	if len(candidates) == 0 {
		return nil
	}
	var target *tracked
	if r.Regretted {
		for _, item := range candidates {
			if "a"+strconv.Itoa(item.record.ID) == r.Target {
				target = item
				break
			}
		}
		if target == nil {
			target = candidates[len(candidates)-1]
		}
	}
	out := make([]*CallRecord, 0, len(candidates))
	for _, item := range candidates {
		outcome := OutcomeAccepted
		if item == target {
			outcome = OutcomeRegretted
		}
		rec := l.label(item, outcome, r.Via, at)
		if r.Probability != nil {
			rec.Regret = r.Probability
		}
		out = append(out, rec)
	}
	return out
}

// Records are every record, tracked ones first, then the rules verdicts. They are copies: the ledger keeps
// labelling its own.
func (l *HoldLedger) Records() []CallRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recordsLocked()
}

func (l *HoldLedger) recordsLocked() []CallRecord {
	out := make([]CallRecord, 0, len(l.tracked)+len(l.untracked))
	for _, item := range l.tracked {
		out = append(out, *item.record)
	}
	for _, r := range l.untracked {
		out = append(out, *r)
	}
	return out
}

// RulesFinding is one flagged rule of a rules verdict: its name and P(violation).
type RulesFinding struct {
	Name      string
	Violation float64
}

// RulesRecord is the observability record of a rules verdict (holds.recordRules' input).
type RulesRecord struct {
	Source   string
	Findings []RulesFinding
	Error    string
}

// RecordRules is observability for the rules guard: its verdicts never act, so they do not join the regret
// labelling, but they land in the hold log (tool "rules") with the per-rule violation probabilities so eval runs can
// diagnose misses.
func (l *HoldLedger) RecordRules(in RulesRecord) *CallRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	at := nowMs()
	level := LevelAllow
	if len(in.Findings) > 0 {
		level = LevelWarn
	}
	r := &CallRecord{ID: l.nextID, At: at, Tool: "rules", Level: level, Source: in.Source, Mode: "steer", Held: false,
		Patterns: []string{}, Reasons: []string{}, Outcome: OutcomeAccepted, OutcomeAt: &at}
	l.nextID++
	for _, f := range in.Findings {
		r.Patterns = append(r.Patterns, f.Name)
		r.Reasons = append(r.Reasons, f.Name+" "+toFixed(f.Violation, 2))
	}
	if in.Error != "" {
		r.Reasons = append(r.Reasons, "error: "+in.Error)
	}
	l.untracked = append(l.untracked, r)
	return r
}

// Snapshot counts the session.
func (l *HoldLedger) Snapshot() HoldSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	var s HoldSnapshot
	for _, r := range l.recordsLocked() {
		if r.Held {
			s.Holds++
			switch r.Outcome {
			case OutcomeApproved:
				s.Approved++
			case OutcomeDeclined:
				s.Declined++
			case OutcomeReplanned:
				s.Replanned++
			case OutcomePending:
				s.Awaiting++
			}
			continue
		}
		s.Allowed++
		switch r.Outcome {
		case OutcomeRegretted:
			s.Regretted++
		case OutcomeAccepted:
			s.Accepted++
		}
	}
	s.Labels = s.Approved + s.Declined + s.Replanned
	if s.Labels > 0 {
		s.Precision = new(float64(s.Declined+s.Replanned) / float64(s.Labels))
	}
	return s
}

// Reset starts a new session. The rules verdicts go too: one session's diagnostics must not inflate the next one's counts.
func (l *HoldLedger) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tracked, l.untracked = nil, nil
	l.nextID = 1
}

// RegretsAt reports whether P(regret) from the question counts as regret.
func RegretsAt(probability float64) bool { return probability >= regretThreshold }

var (
	regretOpening = lazyRE(`(?i)^(?:wait|stop\b|nope\b|hold on\b|don[’']t\b|do not\b|undo\b|revert\b|roll ?back\b|why did you\b|you (?:shouldn[’']t|should not) have\b|that (?:was|is) (?:wrong|not what)\b|no[,.!])`)
	regretUndo    = lazyRE(`(?i)\b(?:undo|revert|roll ?back) (?:that|this|it|the last|those|what you)\b`)
	regretForbid  = lazyRE(`(?i)\b(?:don[’']t|do not|never) (?:do|run|touch|push|delete|remove|commit) that\b`)
)

// TextRegrets is the offline stand-in for the regret question: the reply opens by stopping, undoing, or forbidding
// what was just done.
func TextRegrets(prompt string) bool {
	text := jsTrim(prompt)
	if text == "" {
		return false
	}
	opening := utf16Slice(text, 0, 160)
	// RE2 has no lookahead: "wait" opens a regret unless it is "wait for|until|till".
	if regretOpening.MatchString(opening) && !waitsFor(opening) {
		return true
	}
	return regretUndo.MatchString(opening) || regretForbid.MatchString(opening)
}

// OutcomeNote is one line for the trace entry of a call whose outcome landed.
func OutcomeNote(r CallRecord) string {
	switch r.Outcome {
	case OutcomeApproved:
		how := "released on retry"
		if r.OutcomeVia == ViaDialog {
			how = "confirm dialog"
		}
		return "outcome: approved by the user (" + how + "); the hold was a false positive"
	case OutcomeDeclined:
		if r.OutcomeVia == ViaDeny {
			return "outcome: blocked by a deny rule; the call never ran"
		}
		return "outcome: declined by the user in the confirm dialog; the hold stood"
	case OutcomeReplanned:
		return "outcome: never approved after the user replied; the agent re-planned, the hold stood"
	case OutcomeRegretted:
		p := ""
		if r.Regret != nil {
			p = " (" + toFixed(*r.Regret, 2) + ")"
		}
		return "outcome: the user's next message regrets this call" + p + "; it should have been held"
	case OutcomeAccepted:
		p := ""
		if r.Regret != nil {
			p = " (" + toFixed(*r.Regret, 2) + ")"
		}
		return "outcome: the user's next message does not regret this call" + p
	}
	return "outcome: pending"
}

// FormatHolds is one line for /warden status.
func FormatHolds(s HoldSnapshot, logPath string) string {
	if s.Holds == 0 && s.Allowed == 0 {
		return "Holds: no guarded call judged yet this session."
	}
	plural := func(n int, word string) string {
		if n == 1 {
			return strconv.Itoa(n) + " " + word
		}
		return strconv.Itoa(n) + " " + word + "s"
	}
	parts := []string{plural(s.Holds, "hold")}
	if s.Holds > 0 {
		parts = append(parts, strconv.Itoa(s.Approved)+" approved by you, "+strconv.Itoa(s.Declined)+" declined, "+strconv.Itoa(s.Replanned)+" re-planned, "+strconv.Itoa(s.Awaiting)+" awaiting your reply")
	}
	if s.Precision == nil {
		parts = append(parts, "precision not yet measurable")
	} else {
		parts = append(parts, "precision "+strconv.Itoa(jsRound(*s.Precision*100))+"% over "+plural(s.Labels, "label"))
	}
	parts = append(parts, strconv.Itoa(s.Allowed)+" allowed ("+strconv.Itoa(s.Regretted)+" regretted by you, "+strconv.Itoa(s.Accepted)+" accepted)")
	out := "Holds: " + strings.Join(parts, "; ") + "."
	if logPath != "" {
		out += " Log: " + logPath + "."
	}
	return out
}

// SessionFileID is the session id as a file name part: no separators or dots, so it cannot leave the directory it is
// joined to. pid is the fallback for an id that is empty once cleaned.
func SessionFileID(sessionID string, pid int) string {
	id := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return -1
	}, sessionID)
	if len(id) > 40 {
		id = id[:40]
	}
	if id == "" {
		return strconv.Itoa(pid)
	}
	return id
}

// HoldLogPath is the per-session file beside the user config: `<data dir>/holds/<date>-<session>.jsonl`.
func HoldLogPath(dataDir, sessionID string, at time.Time, pid int) string {
	return filepath.Join(dataDir, "holds", at.UTC().Format("2006-01-02")+"-"+SessionFileID(sessionID, pid)+".jsonl")
}

// HoldLog rewrites the session's records as JSON lines, owner-only, one write at a time so outcomes never
// interleave. The rewrite goes through a rename: a reader during an in-place rewrite could get a line cut short.
type HoldLog struct {
	// Path is the log file.
	Path string

	mu      sync.Mutex
	failure string
}

// NewHoldLog returns a log writing to path.
func NewHoldLog(path string) *HoldLog { return &HoldLog{Path: path} }

// Save replaces the file with the records. A failure is remembered (LastFailure), never raised.
func (h *HoldLog) Save(records []CallRecord) {
	var buf bytes.Buffer
	for _, r := range records {
		b, err := json.Marshal(r)
		if err != nil {
			h.setFailure(err.Error())
			return
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	_, err := Serialize(h.Path, func() (struct{}, error) { return struct{}{}, WriteFileAtomic(h.Path, buf.Bytes()) })
	if err != nil {
		h.setFailure(err.Error())
		return
	}
	h.setFailure("")
}

func (h *HoldLog) setFailure(s string) {
	h.mu.Lock()
	h.failure = s
	h.mu.Unlock()
}

// LastFailure is the last write error, if the most recent save failed.
func (h *HoldLog) LastFailure() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.failure
}

// waitsFor is /^wait (?:for|until|till)\b/i: "wait for CI" is not a regret.
func waitsFor(text string) bool {
	lower := strings.ToLower(text)
	for _, w := range []string{"wait for", "wait until", "wait till"} {
		if strings.HasPrefix(lower, w) {
			rest := lower[len(w):]
			if rest == "" {
				return true
			}
			r, _ := utf8.DecodeRuneInString(rest)
			return !(r == '_' || unicode.IsLetter(r) && r < 128 || unicode.IsDigit(r) && r < 128)
		}
	}
	return false
}

// HeldCall is one held action as the compaction evidence reads it: the tool, its redacted excerpt (tool and path,
// never the command) and what became of it.
type HeldCall struct{ Tool, Preview, Outcome string }

// HeldCalls lists the held calls of the session, oldest first (extension.ts evidenceSnapshot: the records with held
// true). The tools lane's `holdRecords` adapter returns them as its HeldEntry values.
func (l *HoldLedger) HeldCalls() []HeldCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []HeldCall
	for _, r := range l.recordsLocked() {
		if r.Held {
			out = append(out, HeldCall{Tool: r.Tool, Preview: r.CallExcerpt, Outcome: string(r.Outcome)})
		}
	}
	return out
}
