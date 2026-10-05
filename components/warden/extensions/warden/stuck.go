package warden

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// The stuck guard (src/stuck.ts): a rolling memory of tool results for the current prompt, exact repeats
// decided in code, and one judge request for the rest. `stuck.evidence` (the parsed failures and edit
// diffs) is not part of this port: it runs as upstream's documented `stuck.evidence: false`.

const (
	callLimit   = 300
	outputLimit = 400
)

// Attempt is one remembered tool result. Key identifies the exact call; Call is the redacted view that may
// leave the machine.
type Attempt struct {
	Tool string
	Key  string
	// OutputKey is a hash of the normalised output, so identical failures can be told from a changed error.
	OutputKey string
	Call      string
	Failed    bool
	// Output is the tail of the tool output, redacted, where the error usually is.
	Output string
	// Changes: the call may have changed files or state (every call that is not provably read-only).
	Changes bool
	// ReadOnly: a `read`, or a shell command IsReadOnlyCommand accepts.
	ReadOnly bool
	// Poll: polling or waiting; the call is expected to run again with the same output.
	Poll bool
	// Text is the whole result text.
	Text string
}

func headText(text string, limit int) string { return truncate(text, limit) }

func tailText(text string, limit int) string {
	n := utf16Len(text)
	if n <= limit {
		return text
	}
	return "[" + strconv.Itoa(n-limit) + " earlier chars] …" + utf16Slice(text, n-limit, -1)
}

// ResultText is the text content of a tool result, without images.
func ResultText(content []ContentPart) string {
	var parts []string
	for _, p := range content {
		if p.Type == "text" {
			parts = append(parts, p.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// ResultFailed: non-zero exit codes count as failures even when the tool did not flag an error; context-mode
// reports them in the text.
func ResultFailed(isError bool, details map[string]any, content []ContentPart) bool {
	if isError {
		return true
	}
	if code, ok := details["exitCode"].(float64); ok && code != 0 {
		return true
	}
	if code, ok := details["exitCode"].(int); ok && code != 0 {
		return true
	}
	return OutputReportsFailure(ResultText(content))
}

var (
	reDuration = lazyRE(`\b\d+(?:\.\d+)?\s*(?:ms|s|m|h|µs|us|ns)\b`)
	reHex      = lazyRE(`0x[0-9a-fA-F]+`)
	reLongNum  = lazyRE(`\d{5,}`)
	reDate     = lazyRE(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?Z?`)
	readTools  = setOf("read", "grep", "find", "ls")
	rePoll     = lazyRE(`\b(?:sleep|watch|wait)\b|\bgh\s+(?:run|pr)\s+(?:watch|checks|view|list)\b|\bgit\s+status\b|\btail\s+-[fF]\b|\b(?:ps|pgrep)\b`)
)

// normaliseOutput: durations, timestamps, PIDs and addresses change between identical runs; counts and line
// numbers stay.
func normaliseOutput(text string) string {
	text = reDuration.ReplaceAllString(text, "#t")
	text = reHex.ReplaceAllString(text, "0x#")
	text = reLongNum.ReplaceAllString(text, "#")
	return reDate.ReplaceAllString(text, "#date")
}

func sha1Hex(parts ...string) string {
	h := sha1.New()
	for _, p := range parts {
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// MakeAttempt records one finished tool call.
func MakeAttempt(tool string, input map[string]any, content []ContentPart, failed bool) Attempt {
	view, hasView := CommandOf(tool, input)
	readOnlyCommand := hasView && view.Shell && IsReadOnlyCommand(view.Command)
	var call string
	switch {
	case hasView:
		call = view.Command
	default:
		if p, ok := input["path"].(string); ok {
			call = tool + " " + p
		} else {
			raw, _ := json.Marshal(input)
			call = string(raw)
		}
	}
	text := jsTrim(ResultText(content))
	raw, _ := json.Marshal(input)
	return Attempt{
		Tool:      tool,
		Key:       sha1Hex(tool, "\x00", string(raw)),
		OutputKey: sha1Hex(normaliseOutput(text)),
		Call:      Redact(headText(call, callLimit)),
		Failed:    failed,
		Text:      text,
		Output:    Redact(tailText(text, outputLimit)),
		// MCP tools, scripts and unknown tools can change state that the next call reads.
		Changes:  !readTools[tool] && !readOnlyCommand,
		ReadOnly: tool == "read" || readOnlyCommand,
		Poll:     hasView && rePoll.MatchString(view.Command),
	}
}

// QuickRepeat is the 2nd identical call with nothing changed since the 1st, decided without the judge.
type QuickRepeat struct {
	Attempt Attempt
	// CallsAgo counts tool results between the two calls, the latest included.
	CallsAgo int
}

// AttemptWindow is the rolling memory of tool results for the current prompt.
type AttemptWindow struct {
	Attempts []Attempt
	limit    int
	// judged is false until markJudged: nothing has been judged, so the cool-down has passed.
	judged        bool
	sinceJudgment int
	quickRepeated map[string]bool
}

// NewAttemptWindow keeps the last limit attempts.
func NewAttemptWindow(limit int) *AttemptWindow {
	return &AttemptWindow{limit: limit, quickRepeated: map[string]bool{}}
}

// Push remembers one attempt.
func (w *AttemptWindow) Push(a Attempt) {
	w.Attempts = append(w.Attempts, a)
	if len(w.Attempts) > w.limit {
		w.Attempts = append([]Attempt(nil), w.Attempts[len(w.Attempts)-w.limit:]...)
	}
	if w.judged {
		w.sinceJudgment++
	}
}

// Reset forgets everything: a new user prompt.
func (w *AttemptWindow) Reset() {
	w.Attempts = nil
	w.judged = false
	w.sinceJudgment = 0
	w.quickRepeated = map[string]bool{}
}

// MarkJudged starts the cool-down.
func (w *AttemptWindow) MarkJudged() { w.judged, w.sinceJudgment = true, 0 }

func (w *AttemptWindow) latest() *Attempt {
	if len(w.Attempts) == 0 {
		return nil
	}
	return &w.Attempts[len(w.Attempts)-1]
}

func (w *AttemptWindow) count(pred func(Attempt) bool) int {
	n := 0
	for _, a := range w.Attempts {
		if pred(a) {
			n++
		}
	}
	return n
}

// Failures is the number of failed attempts.
func (w *AttemptWindow) Failures() int { return w.count(func(a Attempt) bool { return a.Failed }) }

// ExactRepeats counts failed attempts that repeat the latest attempt's exact call with the same output. A
// changed error is progress, not a repeat.
func (w *AttemptWindow) ExactRepeats() int {
	l := w.latest()
	if l == nil || !l.Failed {
		return 0
	}
	return w.count(func(a Attempt) bool { return a.Failed && a.Key == l.Key && a.OutputKey == l.OutputKey })
}

// SuccessRepeats counts successful attempts that repeat the latest call with the same normalised output: the
// model is re-running instead of reading.
func (w *AttemptWindow) SuccessRepeats() int {
	l := w.latest()
	if l == nil || l.Failed {
		return 0
	}
	return w.count(func(a Attempt) bool { return !a.Failed && a.Key == l.Key && a.OutputKey == l.OutputKey })
}

// ChurnCount counts attempts (whatever their outcome) that target the same call key. When it is high the output
// changes each time, but the model is not making progress.
func (w *AttemptWindow) ChurnCount() int {
	l := w.latest()
	if l == nil {
		return 0
	}
	return w.count(func(a Attempt) bool { return a.Key == l.Key })
}

// QuickRepeat: the latest call repeats the previous call with the same key, nothing that can change state ran
// between them, and it either failed again with the same output or re-read an output the agent already has.
// It fires once per key per window, so a 3rd identical failure goes to the regular stuck check.
func (w *AttemptWindow) QuickRepeat() *QuickRepeat {
	l := w.latest()
	if l == nil || l.Poll || w.quickRepeated[l.Key] {
		return nil
	}
	idx := len(w.Attempts) - 2
	for idx >= 0 && w.Attempts[idx].Key != l.Key {
		idx--
	}
	if idx < 0 {
		return nil
	}
	prev := w.Attempts[idx]
	if prev.OutputKey != l.OutputKey || prev.Failed != l.Failed {
		return nil
	}
	if !l.Failed && !l.ReadOnly {
		return nil
	}
	for _, a := range w.Attempts[idx+1 : len(w.Attempts)-1] {
		if a.Changes {
			return nil
		}
	}
	w.quickRepeated[l.Key] = true
	return &QuickRepeat{Attempt: *l, CallsAgo: len(w.Attempts) - 1 - idx}
}

func (w *AttemptWindow) cooled(c StuckConfig) bool { return !w.judged || w.sinceJudgment >= c.Cooldown }

// ShouldJudge: the latest result failed with enough failures behind it, succeeded but repeats itself, or is
// churning on the same target, and the cool-down has passed.
func (w *AttemptWindow) ShouldJudge(c StuckConfig) bool {
	l := w.latest()
	switch {
	case l == nil:
		return false
	case l.Failed:
		return w.Failures() >= c.MinFailures && w.cooled(c)
	case w.SuccessRepeats() >= c.MinFailures:
		return w.cooled(c)
	case w.ChurnCount() >= c.ChurnThreshold:
		return w.cooled(c)
	}
	return false
}

// StuckJudgment is the judge's answer about a failing sequence.
type StuckJudgment struct {
	SameStrategy float64
	// ApproachChange is 0 identical ... 2 meaningfully different.
	ApproachChange float64
	Progress       float64
	Model          string
	ElapsedMs      int
}

// StuckVerdict is the stuck guard's decision.
type StuckVerdict struct {
	Stuck    bool
	Source   string // repeat | typesafe | error
	Failures int
	Reasons  []string
	// SuccessRepeat: the repeat that fired was a successful call printing the same output.
	SuccessRepeat bool
	// Churn: the repeat was repeated calls to one target with changing output.
	Churn     bool
	Judgment  *StuckJudgment
	Error     string
	ErrorCode string
}

// BuildStuckRequest is the one request state for the stuck guard.
func BuildStuckRequest(attempts []Attempt, task string) Request {
	taskText := "(no user request recorded in this session)"
	if t := jsTrim(task); t != "" {
		// Redacted like the action request's task (deviation D11: stuck.ts sends it as typed).
		taskText = headText(Redact(t), 1500)
	}
	list := make([]map[string]any, 0, len(attempts))
	for i, a := range attempts {
		outcome := "ok"
		if a.Failed {
			outcome = "failed"
		}
		list = append(list, map[string]any{"n": i + 1, "tool": a.Tool, "call": a.Call, "outcome": outcome, "output": a.Output})
	}
	return Request{State: map[string]any{"task": taskText, "attempts": list}, Questions: build(qs.Stuck)}
}

// StuckOptions configure EvaluateStuck.
type StuckOptions struct {
	Config StuckConfig
	// Judge is nil for offline (repeat-only) operation.
	Judge   Judge
	Timeout time.Duration
}

// EvaluateStuck decides exact repeats in code; otherwise one judge request judges the sequence.
func EvaluateStuck(ctx context.Context, w *AttemptWindow, task string, opts StuckOptions) StuckVerdict {
	failures := w.Failures()
	if repeats := w.ExactRepeats(); repeats >= opts.Config.MinFailures {
		return StuckVerdict{Stuck: true, Source: "repeat", Failures: failures, Reasons: []string{"the same call failed " + strconv.Itoa(repeats) + " times with the same output"}}
	}
	if n := w.SuccessRepeats(); n >= opts.Config.MinFailures {
		return StuckVerdict{Stuck: true, Source: "repeat", Failures: failures, Reasons: []string{"the same call succeeded " + strconv.Itoa(n) + " times with the same output"}, SuccessRepeat: true}
	}
	if n := w.ChurnCount(); n >= opts.Config.ChurnThreshold {
		return StuckVerdict{Stuck: true, Source: "repeat", Failures: failures, Reasons: []string{"the same target was called " + strconv.Itoa(n) + " times with changing output"}, Churn: true}
	}
	if opts.Judge == nil {
		return StuckVerdict{Source: "repeat", Failures: failures}
	}
	w.MarkJudged()
	res := Ask(ctx, opts.Judge, BuildStuckRequest(w.Attempts, task), opts.Timeout)
	if !res.OK {
		return StuckVerdict{Source: "error", Failures: failures, Error: res.Error, ErrorCode: res.ErrorCode}
	}
	j := &StuckJudgment{SameStrategy: res.Answers["same_strategy"].Noul, ApproachChange: res.Answers["approach_change"].Score, Progress: res.Answers["progress"].Noul, Model: res.Model, ElapsedMs: res.ElapsedMs}
	stuck := j.SameStrategy >= opts.Config.SameStrategy
	var reasons []string
	if stuck {
		reasons = []string{strconv.Itoa(failures) + " failures with the same strategy (" + toFixed(j.SameStrategy, 2) + "), approach change " + toFixed(j.ApproachChange, 1) + "/2, progress " + toFixed(j.Progress, 2)}
	}
	return StuckVerdict{Stuck: stuck, Source: "typesafe", Failures: failures, Reasons: reasons, Judgment: j}
}

// StuckNudge is the steering text for the agent. It names the pattern and asks for a change of method, not
// another retry. A successful repeat is a different disease than a failure loop: the model already has the
// answer, so it should use it.
func StuckNudge(v StuckVerdict) string {
	reasons := strings.Join(v.Reasons, "; ")
	switch {
	case v.SuccessRepeat:
		return "pi-warden: " + reasons + ". Stop re-running it: the answer is already in the last output. Act on that result, move to the next step, or tell the user why the same call has to run again."
	case v.Churn:
		return "pi-warden: " + reasons + ". The output keeps changing but the target stays the same. Either act on the latest result and move on, or try a different command entirely."
	}
	return "pi-warden: " + reasons + ". Stop retrying. Re-read the last error output carefully, state a new hypothesis about the cause, and either gather the missing information (read the relevant file, check versions or paths) or try a different method. If two different methods have failed, report the blocker to the user with the exact error instead of trying again."
}

const (
	steerCallLimit = 120
	steerLineLimit = 160
)

var (
	reEarlier  = lazyRE(`^\[\d+ earlier chars\]`)
	reErrorish = lazyRE(`(?i)error|fail|denied|not found|no such|enoent|cannot|invalid`)
)

// errorLine is the line of a failed output that names the error, else its last line.
func errorLine(output string) string {
	var lines []string
	for _, l := range strings.Split(output, "\n") {
		l = jsTrim(l)
		if l != "" && !reEarlier.MatchString(l) {
			lines = append(lines, l)
		}
	}
	line := "(no output)"
	if len(lines) > 0 {
		line = lines[len(lines)-1]
	}
	for _, l := range lines {
		if reErrorish.MatchString(l) {
			line = l
			break
		}
	}
	return headText(line, steerLineLimit)
}

// QuickRepeatNudge names the call and what the agent already has, and asks for a change first.
func QuickRepeatNudge(q QuickRepeat) string {
	call := headText(q.Attempt.Call, steerCallLimit)
	if q.Attempt.Failed {
		return "pi-warden: you already ran `" + call + "`; it failed the same way: " + errorLine(q.Attempt.Output) + ". Change something before running it again."
	}
	ago := strconv.Itoa(q.CallsAgo) + " calls ago"
	if q.CallsAgo == 1 {
		ago = "1 call ago"
	}
	return "pi-warden: you already have this output from `" + call + "` (" + ago + "); nothing changed since. Use that output instead of running the call again."
}
