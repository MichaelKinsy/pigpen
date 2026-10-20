package pi_subagents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// The text half of the workflow executor (subagent-executor.ts): how a finished or failed workflow is reported, and the small
// formatters its notifications use. The executor itself is workflow_exec.go.

// MaxOutputConfig is the `maxOutput` parameter (types.ts MaxOutputConfig).
type MaxOutputConfig struct{ Bytes, Lines int }

// DefaultMaxOutput is DEFAULT_MAX_OUTPUT.
var DefaultMaxOutput = MaxOutputConfig{Bytes: 200 * 1024, Lines: 5000}

// maxOutputOf is `{ ...DEFAULT_MAX_OUTPUT, ...params.maxOutput }`.
func maxOutputOf(v any) MaxOutputConfig {
	cfg := DefaultMaxOutput
	o, ok := asObject(toDyn(v))
	if !ok {
		return cfg
	}
	if f, ok := o.vals["bytes"].(float64); ok {
		cfg.Bytes = int(f)
	}
	if f, ok := o.vals["lines"].(float64); ok {
		cfg.Lines = int(f)
	}
	return cfg
}

// truncateOutput is truncateOutput (types.ts:2969): the output cut to the configured lines and bytes, with a marker that names
// where the full output is.
func truncateOutput(output string, cfg MaxOutputConfig, artifactPath string) (text string, truncated bool) {
	lines := strings.Split(output, "\n")
	bytes := len(output)
	if bytes <= cfg.Bytes && len(lines) <= cfg.Lines {
		return output, false
	}
	kept := lines
	if len(lines) > cfg.Lines {
		kept = lines[:cfg.Lines]
	}
	result := strings.Join(kept, "\n")
	if len(result) > cfg.Bytes {
		cut := cfg.Bytes
		for cut > 0 && !utf8.RuneStart(result[cut]) {
			cut--
		}
		result = result[:cut]
	}
	marker := fmt.Sprintf("[TRUNCATED: showing first %d of %d lines, %s of %s", strings.Count(result, "\n")+1, len(lines), formatBytes(len(result)), formatBytes(bytes))
	if artifactPath != "" {
		marker += " - full output at " + artifactPath
	}
	return marker + "]\n" + result, true
}

// formatWorkflowValue is formatWorkflowValue (subagent-executor.ts:5039).
func formatWorkflowValue(v any) string {
	if _, u := v.(undef); u {
		return "(undefined)"
	}
	if s, ok := v.(string); ok {
		return s
	}
	return marshalJSON(v, "  ")
}

// workflowRunningChildrenSummary is workflowRunningChildrenSummary.
func workflowRunningChildrenSummary(children []*jsObject) string {
	var running []string
	for _, c := range children {
		if objStr(c, "state") == "running" {
			running = append(running, fmt.Sprintf("%s=%s", objStr(c, "key"), objStr(c, "runId")))
		}
	}
	if len(running) == 0 {
		return ""
	}
	noun := "runs remain"
	if len(running) == 1 {
		noun = "run remains"
	}
	return fmt.Sprintf("%d child %s running or uncollected: %s. These launch receipts contain no final child results.", len(running), noun, strings.Join(running, ", "))
}

// workflowOutputPathMappingSummary is workflowOutputPathMappingSummary.
func workflowOutputPathMappingSummary(children []*jsObject) string {
	var maps []string
	for _, c := range children {
		m := c.obj("outputPathMapping")
		if m == nil {
			continue
		}
		maps = append(maps, fmt.Sprintf("'%s': requested %s -> saved %s", objStr(c, "key"), objStr(m, "requestedPath"), objStr(m, "savedPath")))
	}
	if len(maps) == 0 {
		return ""
	}
	return " Output path mappings: " + strings.Join(maps, "; ") + "."
}

const workflowValidationPrefix = "workflowScript validation failed before child launch; no children launched."

// workflowFailureMessage is workflowFailureMessage: the error text, with the validation prefix rewritten to name the workflow.
func workflowFailureMessage(err error, workflowRunID string, children []*jsObject) string {
	text := err.Error()
	if len(children) == 0 {
		if i := strings.Index(text, workflowValidationPrefix); i >= 0 {
			return fmt.Sprintf("Workflow '%s' validation failed before child launch; no children launched.%s", workflowRunID, text[i+len(workflowValidationPrefix):])
		}
	}
	var parts []string
	for _, p := range []string{text, workflowRunningChildrenSummary(children)} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}

// workflowFailureTerminalOutcome is workflowFailureTerminalOutcome; the port has no usage budget, so only a timeout counts.
func workflowFailureTerminalOutcome(err error) *jsObject {
	var we *WorkflowScriptError
	if errors.As(err, &we) && we.Kind == WorkflowFailureTimeout {
		return jo("state", "partial", "reason", "timeout")
	}
	return nil
}

func appendWorkflowOutputWarning(text, warning string) string {
	if warning == "" {
		return text
	}
	return text + "\n\n" + warning
}

// writeWorkflowAggregateOutput is writeWorkflowAggregateOutput: the text saved where the workflow's output file is, unless a
// child produced that very file; the returned text is a warning ("" when nothing went wrong).
func writeWorkflowAggregateOutput(outputPath, text string, produced map[string]bool) string {
	if outputPath == "" {
		return ""
	}
	if claim, err := resolveSingleOutputClaimPath(outputPath); err == nil && produced[claim] {
		return ""
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Sprintf("Output file error: %s\n%s", outputPath, err.Error())
	}
	if err := os.WriteFile(outputPath, []byte(text), 0o644); err != nil {
		return fmt.Sprintf("Output file error: %s\n%s", outputPath, err.Error())
	}
	return ""
}

const workflowTraceErrorMaxChars = 500

// workflowTraceLine is one line of the call trace and the error it carries.
type workflowTraceLine struct{ Line, Error string }

type workflowResultTextInput struct {
	Head, Script       []string
	Trace              []workflowTraceLine
	Tail               []string
	MaxOutput          MaxOutputConfig
	AggregateOutput    string
	FullResultPath     string
	ProducedChildPaths map[string]bool
}

// formatWorkflowResultText is formatWorkflowResultText (subagent-executor.ts:3708): return, emits, console and trace errors have no
// size limit of their own, so only they are cut; the saved full result is not.
func formatWorkflowResultText(in workflowResultTextInput) string {
	traceSection := func(errText func(string) string) []string {
		if len(in.Trace) == 0 {
			return nil
		}
		lines := make([]string, len(in.Trace))
		for i, e := range in.Trace {
			lines[i] = e.Line
			if e.Error != "" {
				lines[i] += " — " + errText(e.Error)
			}
		}
		return []string{"Call trace:\n" + strings.Join(lines, "\n")}
	}
	join := func(parts ...[]string) string {
		var all []string
		for _, p := range parts {
			all = append(all, p...)
		}
		return strings.Join(all, "\n\n")
	}
	fullText := join(in.Head, in.Script, traceSection(func(s string) string { return s }), in.Tail)
	outputWarning := writeWorkflowAggregateOutput(in.AggregateOutput, fullText, in.ProducedChildPaths)
	scriptText := strings.Join(in.Script, "\n\n")
	_, scriptCut := truncateOutput(scriptText, in.MaxOutput, "")
	traceCut := false
	for _, e := range in.Trace {
		if len(jsUTF16(e.Error)) > workflowTraceErrorMaxChars {
			traceCut = true
		}
	}
	shown := scriptText
	saveWarning, savedPath := "", ""
	if scriptCut || traceCut {
		aggregateHolds := false
		if in.AggregateOutput != "" && outputWarning == "" {
			if claim, err := resolveSingleOutputClaimPath(in.AggregateOutput); err == nil && !in.ProducedChildPaths[claim] {
				aggregateHolds = true
			}
		}
		if !aggregateHolds {
			saveWarning = writeWorkflowAggregateOutput(in.FullResultPath, fullText, map[string]bool{})
		}
		switch {
		case aggregateHolds:
			savedPath = in.AggregateOutput
		case saveWarning == "":
			savedPath = in.FullResultPath
		}
		if scriptCut {
			shown, _ = truncateOutput(scriptText, in.MaxOutput, savedPath)
		}
	}
	shownTrace := traceSection(func(e string) string {
		u := jsUTF16(e)
		if len(u) > workflowTraceErrorMaxChars {
			return string(utf16.Decode(u[:workflowTraceErrorMaxChars])) + fmt.Sprintf("… (+%d chars)", len(u)-workflowTraceErrorMaxChars)
		}
		return e
	})
	var traceNote []string
	if traceCut && !scriptCut && savedPath != "" {
		traceNote = []string{fmt.Sprintf("[TRUNCATED: trace errors shortened - full output at %s]", savedPath)}
	}
	var shownScript []string
	if shown != "" {
		shownScript = []string{shown}
	}
	text := join(in.Head, shownScript, shownTrace, in.Tail, traceNote)
	return appendWorkflowOutputWarning(appendWorkflowOutputWarning(text, outputWarning), saveWarning)
}

// incrementalChild is the notification of a child that settled while its workflow goes on (notify.ts IncrementalChildCompletion).
type incrementalChild struct {
	Outcome, ChildKey, WorkflowRunID, ChildRunID, OutputReference, Error string
	WorkflowRunning                                                      bool
}

// scheduledCompletionTriggersTurn is scheduledCompletionTriggersTurn (notify.ts:370): a quiet schedule stays quiet on success.
func scheduledCompletionTriggersTurn(quiet bool, outcome string) bool {
	return !(quiet && outcome == "completed")
}

// incrementalChildCompletionTriggersTurn is incrementalChildCompletionTriggersTurn.
func incrementalChildCompletionTriggersTurn(c incrementalChild, quietSchedule bool) bool {
	if c.WorkflowRunning && c.Outcome == "completed" {
		return false
	}
	return scheduledCompletionTriggersTurn(quietSchedule, c.Outcome)
}

// formatIncrementalChildCompletion is formatIncrementalChildCompletion.
func formatIncrementalChildCompletion(c incrementalChild) string {
	status := map[string]string{"completed": "completed", "failed": "failed", "paused": "paused (needs attention)"}[c.Outcome]
	if status == "" {
		status = "stopped"
	}
	wf := "workflow finished"
	if c.WorkflowRunning {
		wf = "workflow still running"
	}
	lines := []string{fmt.Sprintf("Workflow child %s: **%s**", status, c.ChildKey), "Workflow run: " + c.WorkflowRunID}
	if c.ChildRunID != "" {
		lines = append(lines, "Child run: "+c.ChildRunID)
	}
	if c.OutputReference != "" {
		lines = append(lines, "Output: "+c.OutputReference)
	}
	if c.Error != "" {
		lines = append(lines, "Error: "+c.Error)
	}
	return strings.Join(append(lines, "Status: "+wf), "\n")
}

// workflowChildAccounting is workflowChildAccountingFields: the usage and session of the results a child carries.
func workflowChildAccounting(child *jsObject) []any {
	results, _ := child.vals["results"].([]any)
	if len(results) == 0 {
		return nil
	}
	sum := map[string]float64{}
	sessionFile := ""
	for _, r := range results {
		ro, _ := r.(*jsObject)
		if ro == nil {
			continue
		}
		if u := ro.obj("usage"); u != nil {
			for _, k := range []string{"input", "output", "cacheRead", "cacheWrite", "cost", "turns"} {
				if f, ok := u.vals[k].(float64); ok {
					sum[k] += f
				}
			}
		}
		if sessionFile == "" {
			sessionFile = objStr(ro, "sessionFile")
		}
	}
	var out []any
	for _, k := range []string{"input", "output", "cacheRead", "cacheWrite", "cost", "turns"} {
		if sum[k] != 0 {
			u := newObject()
			for _, kk := range []string{"input", "output", "cacheRead", "cacheWrite", "cost", "turns"} {
				u.set(kk, sum[kk])
			}
			out = append(out, "usage", u)
			break
		}
	}
	if sessionFile != "" {
		out = append(out, "sessionFile", sessionFile)
	}
	if rec := child.obj("recovery"); rec != nil {
		out = append(out, "recovery", rec)
	}
	return out
}

// workflowResultChildren is workflowResultChildren: the children of the result file of a workflow.
func workflowResultChildren(children []*jsObject, sessionName func(key string) string, statusState string, includeFailureFields bool) []any {
	out := make([]any, 0, len(children))
	for _, c := range children {
		r := newObject()
		key := objStr(c, "key")
		r.set("workflowKey", key)
		setIf(r, "agent", objStr(c, "agent"))
		setIf(r, "runId", objStr(c, "runId"))
		if n := sessionName(key); n != "" {
			r.set("sessionName", n)
		}
		acct := workflowChildAccounting(c)
		for i := 0; i+1 < len(acct); i += 2 {
			r.set(acct[i].(string), acct[i+1])
		}
		output := objStr(c, "output")
		r.set("output", output)
		present := jsTrim(output) != ""
		if v, ok := c.vals["structuredOutput"]; ok && !isUndefinedValue(v) {
			present = true
		}
		if present {
			r.set("outputState", "present")
		} else {
			r.set("outputState", "absent")
		}
		if v, ok := c.vals["structuredOutput"]; ok && !isUndefinedValue(v) {
			r.set("structuredOutput", v)
		}
		if objStr(c, "state") == "running" {
			r.set("state", "running")
		} else {
			r.set("success", objBool(c, "ok"))
		}
		setIf(r, "asyncDir", objStr(c, "asyncDir"))
		setIf(r, "outputReference", objStr(c, "outputReference"))
		if includeFailureFields {
			if t := c.obj("terminalOutcome"); t != nil {
				r.set("terminalOutcome", t)
			}
		}
		if m := c.obj("outputPathMapping"); m != nil {
			r.set("outputPathMapping", m)
		}
		if objBool(c, "stopped") {
			r.set("stopped", true)
		}
		if objBool(c, "interrupted") {
			r.set("interrupted", true)
		}
		if includeFailureFields && objBool(c, "detached") && statusState != "complete" {
			r.set("detached", true)
		}
		if p := firstNonEmpty(objStr(c, "outputArtifactPath"), objStr(c, "outputReference")); p != "" {
			r.set("artifactPaths", jo("outputPath", p))
		}
		out = append(out, r)
	}
	return out
}

// workflowDetailsResults is workflowDetailsResults: the single results of every child, each tagged with its workflow key.
func workflowDetailsResults(children []*jsObject) []any {
	out := []any{}
	for _, c := range children {
		results, _ := c.vals["results"].([]any)
		for _, r := range results {
			ro, ok := r.(*jsObject)
			if !ok {
				continue
			}
			if !ro.has("workflowKey") {
				ro = ro.clone()
				ro.set("workflowKey", objStr(c, "key"))
			}
			out = append(out, ro)
		}
	}
	return out
}

func isUndefinedValue(v any) bool { _, u := v.(undef); return u }
