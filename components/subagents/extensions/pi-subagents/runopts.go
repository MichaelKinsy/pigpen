package pi_subagents

// The single-run options the port honors: `timeoutMs` / `maxRuntimeMs` (the run's wall-clock deadline),
// `checkpointBeforeDeadlineMs` (the pre-deadline checkpoint steer) and `output` (where the answer is persisted). The
// original resolves and applies them in its executor, its async runner and its single-output helper
// (runs/foreground/subagent-executor.ts, runs/background/subagent-runner.ts, runs/shared/single-output.ts); the rules
// and texts below are those. The extension config file's defaults (`config.timeoutMs`,
// `config.checkpointBeforeDeadlineMs`, `singleRunOutputBaseDir`) are not ported: the port reads no such file, so a
// single run's deadline falls back to the built-in 30 minutes as the original does when its config sets nothing.

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	// defaultRunTimeoutMs is the original's DEFAULT_FOREGROUND_TIMEOUT_MS and DEFAULT_ASYNC_TIMEOUT_MS: every single
	// run has a deadline — the call's timeoutMs, else this.
	defaultRunTimeoutMs = 30 * 60 * 1000
	// maxTimerDelayMs is the original's MAX_TIMER_DELAY_MS: a timer cannot hold a longer delay (it would overflow and
	// fire at once), so a longer deadline is refused rather than run.
	maxTimerDelayMs = 2147483647
)

// registerRunParams marks execution parameters as ported (refuse.go), from the file that honors them.
func init() {
	registerRunParams("timeoutMs", "maxRuntimeMs", "checkpointBeforeDeadlineMs", "output")
}

// jsInteger is a whole number from a JSON tool call (float64) or from a test (an int of some width). A value above the
// timer ceiling comes back as ceiling+1, so a caller's range check sees it as too large instead of as a type error.
func jsInteger(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint64:
		return int64(n), true
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
			return 0, false
		}
		if n > float64(maxTimerDelayMs) {
			return int64(maxTimerDelayMs) + 1, true
		}
		return int64(n), true
	}
	return 0, false
}

// runTimeoutParam resolves `timeoutMs` and `maxRuntimeMs` for one single run: the original's resolveForegroundTimeout
// over resolveSingleAgentLaunchTimeout's single-agent default — each a positive integer no larger than the timer
// ceiling, the two aliases only when they agree, else the 30-minute default (its config default is not ported).
func runTimeoutParam(params map[string]any) (int, string) {
	values := map[string]int64{}
	set := map[string]bool{}
	for _, name := range []string{"timeoutMs", "maxRuntimeMs"} {
		v, ok := params[name]
		if !ok || v == nil { // a JSON null is unset, as elsewhere in the port
			continue
		}
		n, isInteger := jsInteger(v)
		if !isInteger || n <= 0 {
			return 0, name + " must be a positive integer."
		}
		if n > maxTimerDelayMs {
			return 0, fmt.Sprintf("%s must be a positive integer no larger than %d.", name, maxTimerDelayMs)
		}
		values[name], set[name] = n, true
	}
	if set["timeoutMs"] && set["maxRuntimeMs"] && values["timeoutMs"] != values["maxRuntimeMs"] {
		return 0, "timeoutMs and maxRuntimeMs are aliases; provide only one value or use the same value for both."
	}
	if set["timeoutMs"] {
		return int(values["timeoutMs"]), ""
	}
	if set["maxRuntimeMs"] {
		return int(values["maxRuntimeMs"]), ""
	}
	return defaultRunTimeoutMs, ""
}

// checkpointParam resolves `checkpointBeforeDeadlineMs`: a positive integer no larger than the timer ceiling, else
// none. The original's schema bounds the call value the same way (the host rejects one outside it before the
// extension runs); this is its config loader's text for the same bound, since the original has no call-level message.
func checkpointParam(params map[string]any) (int, string) {
	v, ok := params["checkpointBeforeDeadlineMs"]
	if !ok || v == nil {
		return 0, ""
	}
	n, isInteger := jsInteger(v)
	if !isInteger || n <= 0 || n > maxTimerDelayMs {
		return 0, fmt.Sprintf("checkpointBeforeDeadlineMs must be a positive integer no larger than %d.", maxTimerDelayMs)
	}
	return int(n), ""
}

// outputParam resolves a single run's output file the way the original does: the call's `output` when it is set, else
// the agent's own (its `rawOutput`), through its normalizeSingleOutputOverride — false (or "false") turns the output
// file off, true (or "true") takes the agent's default, a non-empty string is the path, and an empty string clears the
// agent's default. A type the schema does not allow is refused rather than dropped: the port never accepts and
// ignores an input. The path itself needs the run id and is resolved by singleOutputPath.
func outputParam(params map[string]any, agentDefault string) (string, string) {
	raw, ok := params["output"]
	if !ok || raw == nil {
		raw = agentDefault
	}
	switch v := raw.(type) {
	case nil:
		return "", ""
	case bool:
		if !v {
			return "", ""
		}
		return agentOutputPath(agentDefault), ""
	case string:
		switch v {
		case "false":
			return "", ""
		case "true":
			return agentOutputPath(agentDefault), ""
		}
		return v, ""
	}
	return "", "output must be a string or a boolean when provided."
}

// agentOutputPath is an agent's own `output` frontmatter as a path: "" when it sets none (its `false`, its `true`, or
// nothing at all).
func agentOutputPath(defaultOutput string) string {
	if defaultOutput == "" || defaultOutput == "false" || defaultOutput == "true" {
		return ""
	}
	return defaultOutput
}

// singleOutputPath is the original's resolveSingleOutputPath over resolveSingleRunOutputBaseDir: an absolute output
// is used as written, a relative one lands under the run's artifacts outputs directory — the session's own
// `subagent-artifacts` when the session has a directory, the port's temp artifacts root otherwise (its config
// `singleRunOutputBaseDir` is not ported; the root is the original's shape, `.../artifacts`, without its temp-scope
// id). An output of "" (none, false or true without an agent default) resolves to no path.
func singleOutputPath(output, runID, sessionDir string) string {
	if output == "" || output == "false" || output == "true" {
		return ""
	}
	if filepath.IsAbs(output) {
		return filepath.Clean(output)
	}
	return filepath.Join(singleOutputBase(runID, sessionDir), filepath.FromSlash(output))
}

// singleOutputBase is `<artifacts>/outputs/<run id>` (the original's sanitizeRunPathSegment keeps the id one segment).
func singleOutputBase(runID, sessionDir string) string {
	return filepath.Join(artifactsDirFor(sessionDir), "outputs", sanitizeRunPathSegment(runID))
}

// artifactsDirFor is the original's getArtifactsDir for the "session" preference: the session's subagent-artifacts
// directory, and its temp root when the session has no directory (a headless child, a test).
func artifactsDirFor(sessionDir string) string {
	if sessionDir != "" {
		return filepath.Join(sessionDir, "subagent-artifacts")
	}
	return filepath.Join(os.TempDir(), "pi-subagents", "artifacts")
}

var (
	invalidRunSegment = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	edgeUnderscore    = regexp.MustCompile(`^_+|_+$`)
)

// sanitizeRunPathSegment is the original's: a run id becomes one path segment, and an empty or over-long one becomes
// "unknown" or its first 120 bytes (all ASCII after the substitution, so bytes are characters).
func sanitizeRunPathSegment(value string) string {
	sanitized := edgeUnderscore.ReplaceAllString(invalidRunSegment.ReplaceAllString(strings.TrimSpace(value), "_"), "")
	if sanitized == "" {
		return "unknown"
	}
	if len(sanitized) <= 120 {
		return sanitized
	}
	if cut := strings.TrimRight(sanitized[:120], "_"); cut != "" {
		return cut
	}
	return "unknown"
}

// readOnlyOutputTools are the original's READ_ONLY_OUTPUT_TOOLS: a child that has only these (with no MCP tool of its
// own) cannot write the output file itself, so it is told the runtime persists its answer instead.
var readOnlyOutputTools = map[string]bool{
	"read": true, "grep": true, "find": true, "ls": true, "web_search": true, "fetch_content": true,
	"get_search_content": true, "source_check": true, "intercom": true, "contact_supervisor": true,
	"structured_output": true, "watchdog_diff": true,
}

// hasOutputWriteCapability is the original's: unknown tools (none declared) count as writable, and one MCP tool is
// enough.
func hasOutputWriteCapability(a *AgentConfig) bool {
	if len(a.McpDirectTools) > 0 || a.Tools == nil {
		return true
	}
	for _, t := range a.Tools {
		if !readOnlyOutputTools[t] {
			return true
		}
	}
	return false
}

// formatOutputPathInstruction is the original's: what the child is told about the output path, in its words, with the
// read-only wording for a child that cannot write the file itself.
func formatOutputPathInstruction(outputPath string, writeCapable bool) string {
	delivery := "Write your findings to exactly this path: " + outputPath
	if !writeCapable {
		delivery = strings.Join([]string{
			"Return the complete artifact in your final response.",
			"The runtime will persist it to exactly this path: " + outputPath,
			"Do not call contact_supervisor merely because no write-capable tool is available.",
		}, "\n")
	}
	return strings.Join([]string{
		delivery,
		"This path is authoritative for this run.",
		"Ignore any other output filename or output path mentioned elsewhere, including output destinations in the base agent prompt, system prompt, or task instructions.",
	}, "\n")
}

// injectSingleOutputInstruction is the original's: the task tells the child where its answer goes, and that the path
// beats every other output destination the prompt mentions.
func injectSingleOutputInstruction(task, outputPath string, a *AgentConfig) string {
	if outputPath == "" {
		return task
	}
	return task + "\n\n---\n**Output:**\n" + formatOutputPathInstruction(outputPath, hasOutputWriteCapability(a))
}

// outputSnapshot is the output file before the child starts (the original's captureSingleOutputSnapshot): a file the
// child writes itself is read back as the answer instead of being overwritten.
type outputSnapshot struct {
	exists bool
	mtime  time.Time
	size   int64
}

func captureOutputSnapshot(path string) outputSnapshot {
	if path == "" {
		return outputSnapshot{}
	}
	st, err := os.Stat(path)
	if err != nil {
		return outputSnapshot{}
	}
	return outputSnapshot{exists: true, mtime: st.ModTime(), size: st.Size()}
}

// persistOutput is the original's resolveSingleOutput over finalizeSingleOutput for one single run: a file that
// changed since the child started (the child wrote it) is read back as the answer, otherwise the answer is written to
// the path, and a successful save appends the original's reference line. Only a successful child gets an output file —
// the original resolves output when the exit code is 0 — and a save failure is reported in the answer, as its
// `Output file error:` line is.
func persistOutput(outputPath, answer string, before outputSnapshot, exit int) string {
	if outputPath == "" || exit != 0 {
		return answer
	}
	full := answer
	changed := false
	st, err := os.Stat(outputPath)
	switch {
	case err == nil:
		changed = !before.exists || !before.mtime.Equal(st.ModTime()) || before.size != st.Size()
	case errors.Is(err, fs.ErrNotExist):
		// the file does not exist: the answer is written below
	default:
		return answer + outputSaveError(outputPath, "Failed to inspect output file: "+err.Error())
	}
	if changed {
		data, readErr := os.ReadFile(outputPath)
		if readErr != nil {
			return answer + outputSaveError(outputPath, "Failed to read changed output file: "+readErr.Error())
		}
		full = string(data)
	} else if err := writeOutputFile(outputPath, answer); err != nil {
		return answer + outputSaveError(outputPath, err.Error())
	}
	reference := savedOutputReferenceLine(outputPath, full)
	if strings.Contains(full, reference) {
		return full
	}
	return full + "\n\n" + reference
}

// writeOutputFile is the original's persistSingleOutput: the directory is created and the file written as it is.
func writeOutputFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// outputSaveError is the original's `Output file error:` line in the answer.
func outputSaveError(outputPath, message string) string {
	return fmt.Sprintf("\n\nOutput file error: %s\n%s", outputPath, message)
}

// savedOutputReferenceLine is the original's formatSavedOutputReference's message: where the answer went, how big it
// is, and that the file is the thing to read.
func savedOutputReferenceLine(savedPath, fullOutput string) string {
	absolute := savedPath
	if abs, err := filepath.Abs(savedPath); err == nil {
		absolute = abs
	}
	lines := countOutputLines(fullOutput)
	return fmt.Sprintf("Output saved to: %s (%s, %d %s). Read this file if needed.",
		absolute, formatOutputBytes(len(fullOutput)), lines, lineWord(lines))
}

// lineWord is the original's plural of "line" in its reference message.
func lineWord(lines int) string {
	if lines == 1 {
		return "line"
	}
	return "lines"
}

// formatOutputBytes is single-output.ts's formatByteSize (note the space, unlike the truncation marker's formatBytes).
func formatOutputBytes(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(bytes) / 1024
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	return jsToFixed(value, 1) + " " + units[unit]
}

// countOutputLines is single-output.ts's countLines: line breaks, and a last line that has none.
func countOutputLines(text string) int {
	if text == "" {
		return 0
	}
	lines := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\n':
			lines++
		case '\r':
			lines++
			if i+1 < len(text) && text[i+1] == '\n' {
				i++ // \r\n is one break
			}
		}
	}
	if last := text[len(text)-1]; last != '\n' && last != '\r' {
		lines++
	}
	return lines
}

// setLaunchOptions records what the call asked of the run: its deadline, the pre-deadline checkpoint lead (an async
// single run only) and the output file. It is set before the run starts, so the run goroutine reads it as written.
func (r *runRecord) setLaunchOptions(timeoutMs, checkpointMs int, outputPath string) {
	r.ctrlMu.Lock()
	r.timeoutMs, r.checkpointMs, r.outputPath = timeoutMs, checkpointMs, outputPath
	r.ctrlMu.Unlock()
}

// outputFile is the run's output path (setLaunchOptions's reader).
func (r *runRecord) outputFile() string {
	r.ctrlMu.Lock()
	defer r.ctrlMu.Unlock()
	return r.outputPath
}

// armDeadline starts the run's deadline timer and, with a checkpoint lead, the checkpoint steer that precedes it.
// One run arms once; a run with no deadline (a chain's, a resume's) arms nothing.
func (r *runRecord) armDeadline() {
	r.ctrlMu.Lock()
	if r.deadlineArmed || r.timeoutMs <= 0 {
		r.ctrlMu.Unlock()
		return
	}
	r.deadlineArmed = true
	timeout := time.Duration(r.timeoutMs) * time.Millisecond
	lead := time.Duration(r.checkpointMs) * time.Millisecond
	r.deadlineAt = time.Now().Add(timeout)
	r.deadlineStop = make(chan struct{})
	stop := r.deadlineStop
	r.deadlineTimer = time.AfterFunc(timeout, r.expire)
	r.ctrlMu.Unlock()
	if lead > 0 {
		go r.deadlineCheckpoint(timeout, lead, stop)
	}
}

// stopDeadline stops the run's timers when it settles: a completed run neither fails later nor keeps a timer alive.
func (r *runRecord) stopDeadline() {
	r.ctrlMu.Lock()
	if r.deadlineStopped {
		r.ctrlMu.Unlock()
		return
	}
	r.deadlineStopped = true
	timer, stop := r.deadlineTimer, r.deadlineStop
	r.deadlineTimer, r.deadlineStop = nil, nil
	r.ctrlMu.Unlock()
	if timer != nil {
		timer.Stop()
	}
	if stop != nil {
		close(stop)
	}
}

// expire is the run's deadline firing: the child is stopped and the step fails with the original's timeout text
// (subagent-runner.ts's timeoutRunner over execution.ts's formatTimeoutMessage), never with "cancelled".
func (r *runRecord) expire() {
	r.ctrlMu.Lock()
	defer r.ctrlMu.Unlock()
	if r.terminated || r.deadlineStopped {
		return
	}
	r.terminated = true
	r.timedOut = true
	r.endIntent = stateFailed
	close(r.done)
}

// didTimeout reports whether the run's deadline fired.
func (r *runRecord) didTimeout() bool {
	r.ctrlMu.Lock()
	defer r.ctrlMu.Unlock()
	return r.timedOut
}

// timeoutMessage is the original's formatTimeoutMessage for this run's deadline.
func (r *runRecord) timeoutMessage() string {
	r.ctrlMu.Lock()
	defer r.ctrlMu.Unlock()
	return fmt.Sprintf("Subagent timed out after %dms.", r.timeoutMs)
}

// stepRunning is whether one of the run's steps is running — the original's guard before it steers the checkpoint.
func (r *runRecord) stepRunning() bool {
	fleet.mu.Lock()
	defer fleet.mu.Unlock()
	for _, s := range r.steps {
		if s.state == stateRunning {
			return true
		}
	}
	return false
}

// deadlineCheckpoint is the original's pre-deadline checkpoint steer (subagent-runner.ts): `lead` before the deadline,
// only when that leaves at least a second (its "does not schedule a checkpoint less than one second after launch"),
// only while a step is still running, and only while the run lives. The message goes over the same route any steer
// takes — into the running child's turn at its next safe point — so its receipt is the normal steering lifecycle's;
// a child with no pinned session cannot be reached mid-turn, and the port skips the steer rather than queue it for a
// turn that the deadline will have ended (the original calls the whole mechanism best-effort).
func (r *runRecord) deadlineCheckpoint(timeout, lead time.Duration, stop <-chan struct{}) {
	delay := checkpointDelay(timeout, lead)
	if delay == 0 {
		return
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-stop:
		return
	}
	if r.didTimeout() || !r.stepRunning() {
		return
	}
	r.ctrlMu.Lock()
	remaining := time.Until(r.deadlineAt)
	r.ctrlMu.Unlock()
	seconds := int(math.Round(remaining.Seconds()))
	if seconds < 0 {
		seconds = 0
	}
	if r.runningChildSession() == "" {
		return
	}
	steerMessage(r, checkpointMessage(seconds), "steer")
}

// checkpointDelay is how long after launch the checkpoint steer is scheduled: the lead before the deadline, and never
// less than a second after launch (the original's "does not schedule a checkpoint less than one second after launch");
// 0 means it is not scheduled at all.
func checkpointDelay(timeout, lead time.Duration) time.Duration {
	delay := timeout - lead
	if delay < time.Second {
		return 0
	}
	return delay
}

// checkpointMessage is the original's deadline-checkpoint steer text.
func checkpointMessage(seconds int) string {
	return fmt.Sprintf("Deadline checkpoint from the runner: this run is killed in about %d seconds. "+
		"Finish the current tool call only, then stop and reply with a handoff: changed files, build/test state, "+
		"remaining work, and commit/PR state. Do not start new work.", seconds)
}
