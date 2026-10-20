package pi_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Port of src/runs/background/chain-root-attachment.ts: a chain (or workflow) attached to a root run that already runs in the
// background waits for that run's result and imports it as its first step. Status and result files are ordered JSON
// (*jsObject): the imported result keeps the fields of whichever of the child's result, the status step and the status carries them.

// ImportedAsyncRoot names the attached run.
type ImportedAsyncRoot struct {
	RunID      string
	AsyncDir   string
	ResultPath string
	Index      int
}

// ImportedAsyncRootOptions are the options of WaitForImportedAsyncRoot; nil numbers take the original's defaults (a poll every
// 500 ms, 1 s of grace for a terminal status to publish its result). Now and Sleep stand for Date.now and setTimeout.
type ImportedAsyncRootOptions struct {
	PollIntervalMs        *int64
	TerminalResultGraceMs *int64
	Now                   func() int64
	Sleep                 func(ms int64)
	ShouldAbort           func() bool
	TimeoutMessage        string
}

var (
	importedTerminalStates       = map[string]bool{"complete": true, "failed": true, "partial": true, "paused": true, "stopped": true}
	importedTerminalStepStatuses = map[string]bool{"complete": true, "completed": true, "failed": true, "partial": true, "paused": true, "stopped": true}
)

func readImportedResultFile(path string) (*jsObject, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if msg := workflowJSONError(string(data)); msg != "" {
		return nil, fmt.Errorf("%s", msg)
	}
	v, err := parseJSON(data)
	if err != nil {
		return nil, err
	}
	o, _ := v.(*jsObject)
	return o, nil
}

func readImportedResult(root ImportedAsyncRoot, status *jsObject) (*jsObject, error) {
	direct, err := readImportedResultFile(root.ResultPath)
	if err != nil || direct != nil {
		return direct, err
	}
	sessionID := ""
	if status != nil {
		sessionID = receiptStringAt(status, "sessionId")
	}
	if sessionID == "" {
		return nil, nil
	}
	indexed, err := resultPayloadPathForSessionRun(filepath.Dir(root.ResultPath), sessionID, root.RunID)
	if err != nil || indexed == "" {
		return nil, err
	}
	return readImportedResultFile(indexed)
}

func selectedStatusStep(status *jsObject, index int) *jsObject {
	if status == nil {
		return nil
	}
	steps, _ := status.vals["steps"].([]any)
	if index < 0 || index >= len(steps) {
		return nil
	}
	s, _ := steps[index].(*jsObject)
	return s
}

func isTerminalImportedStatus(status *jsObject, index int) bool {
	if status == nil {
		return false
	}
	state := receiptStringAt(status, "state")
	// A workflow-owned single runner publishes its result after completing its step. Keep that window open while root process
	// proof is absent or pending; explicit terminal/unavailable proof retains the step fallback.
	if receiptStringAt(status, "mode") == "single" && receiptStringAt(status, "parentWorkflowRunId") != "" {
		proof := status.obj("processTerminal")
		if proof == nil || proof.vals["state"] == "pending" {
			return importedTerminalStates[state]
		}
	}
	if step := selectedStatusStep(status, index); step != nil && importedTerminalStepStatuses[receiptStringAt(step, "status")] {
		return true
	}
	return importedTerminalStates[state]
}

func importedResultState(result, child *jsObject) string {
	if result == nil {
		return ""
	}
	if child != nil && child.vals["stopped"] == true {
		return "stopped"
	}
	if child != nil && child.vals["success"] == true {
		return "complete"
	}
	state := receiptStringAt(result, "state")
	if child != nil && child.vals["success"] == false {
		switch state {
		case "stopped", "paused", "partial":
			return state
		}
		return "failed"
	}
	if importedTerminalStates[state] {
		return state
	}
	switch result.vals["success"] {
	case true:
		return "complete"
	case false:
		return "failed"
	}
	return ""
}

// firstPresent is `a ?? b ?? c` over properties of objects that may be nil: undefined and null are skipped.
func firstPresent(key string, objs ...*jsObject) (any, bool) {
	for _, o := range objs {
		if o == nil {
			continue
		}
		if v, ok := lpGet(o, key); ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

func setIfTruthy(out *jsObject, key string, v any, ok bool) {
	if ok && jsTruthy(v) {
		out.set(key, v)
	}
}

func fromStep(out *jsObject, step *jsObject, keys ...string) {
	for _, k := range keys {
		if step == nil {
			return
		}
		if v, ok := lpGet(step, k); ok && jsTruthy(v) {
			out.set(k, v)
		}
	}
}

func importedAgent(root ImportedAsyncRoot, status, step, child *jsObject) string {
	if child != nil {
		if a := receiptStringAt(child, "agent"); a != "" {
			return a
		}
	}
	if step != nil {
		if a := receiptStringAt(step, "agent"); a != "" {
			return a
		}
	}
	if s := selectedStatusStep(status, root.Index); s != nil {
		if a := receiptStringAt(s, "agent"); a != "" {
			return a
		}
	}
	return "subagent"
}

func outputFromTerminalStatus(root ImportedAsyncRoot, status, step *jsObject) *jsObject {
	agent := importedAgent(root, status, step, nil)
	timedOut := (step != nil && step.vals["timedOut"] == true) || status.vals["timedOut"] == true
	stopped := (step != nil && step.vals["stopped"] == true) || status.vals["stopped"] == true || receiptStringAt(status, "state") == "stopped"
	message := ""
	if m, ok := firstPresent("error", step, status); ok {
		message, _ = m.(string)
	} else if stopped {
		message = "Subagent stopped by user."
	} else {
		message = fmt.Sprintf("Attached async root %s ended without a result file at %s.", root.RunID, root.ResultPath)
	}
	var execution any
	if step != nil {
		if e, ok := lpGet(step, "execution"); ok && e != nil {
			execution = e
		}
	}
	if execution == nil && (receiptStringAt(status, "state") == "partial" || (step != nil && receiptStringAt(step, "status") == "partial")) {
		execution = jo("status", "partial", "success", false, "exitCode", float64(1), "error", message)
	}
	out := jo("agent", agent, "output", message, "success", false, "exitCode", float64(1), "error", message)
	if timedOut {
		out.set("timedOut", true)
	}
	if stopped {
		out.set("stopped", true)
	}
	fromStep(out, step, "sessionName")
	if f, ok := firstPresent("sessionFile", step, status); ok && jsTruthy(f) {
		out.set("sessionFile", f)
	}
	fromStep(out, step, "model", "requestedModel")
	if step != nil && jsTruthy(step.vals["contextOverflow"]) {
		out.set("contextOverflow", true)
	}
	fromStep(out, step, "totalCost")
	if step != nil {
		if so, ok := lpGet(step, "structuredOutput"); ok {
			out.set("structuredOutput", so)
		}
	}
	fromStep(out, step, "structuredOutputPath", "structuredOutputSchemaPath", "acceptance")
	if execution != nil {
		out.set("execution", execution)
	}
	fromStep(out, step, "effects", "transcriptPath")
	return out
}

func outputFromTimeout(root ImportedAsyncRoot, status *jsObject, message string) *jsObject {
	step := selectedStatusStep(status, root.Index)
	out := jo("agent", importedAgent(root, status, step, nil), "output", message, "success", false, "exitCode", float64(1), "error", message, "timedOut", true)
	fromStep(out, step, "sessionName")
	if f, ok := firstPresent("sessionFile", step, status); ok && jsTruthy(f) {
		out.set("sessionFile", f)
	}
	fromStep(out, step, "model", "requestedModel")
	if step != nil && jsTruthy(step.vals["contextOverflow"]) {
		out.set("contextOverflow", true)
	}
	fromStep(out, step, "totalCost", "transcriptPath")
	return out
}

func buildImportedResult(root ImportedAsyncRoot, status, result *jsObject) *jsObject {
	var child *jsObject
	if list, ok := result.vals["results"].([]any); ok && root.Index >= 0 && root.Index < len(list) {
		child, _ = list[root.Index].(*jsObject)
	}
	step := selectedStatusStep(status, root.Index)
	state := importedResultState(result, child)
	flag := func(o *jsObject, k string) bool { return o != nil && o.vals[k] == true }
	agent := importedAgent(root, status, step, child)
	output := ""
	if o, ok := firstPresent("output", child); ok {
		output, _ = o.(string)
	} else if s, ok := firstPresent("summary", result); ok {
		output, _ = s.(string)
	}
	timedOut := flag(child, "timedOut") || flag(step, "timedOut") || flag(result, "timedOut") || flag(status, "timedOut")
	stopped := flag(child, "stopped") || flag(step, "stopped") || flag(result, "stopped") || flag(status, "stopped") || state == "stopped"
	success := state == "complete" && !timedOut && !stopped
	var errText string
	if e, ok := firstPresent("error", child); ok {
		errText, _ = e.(string)
	} else if !success {
		switch {
		case stopped:
			errText = "Subagent stopped by user."
		default:
			if e, ok := firstPresent("error", result); ok {
				errText, _ = e.(string)
			} else if e, ok := firstPresent("summary", result); ok {
				errText, _ = e.(string)
			} else if e, ok := firstPresent("error", status); ok {
				errText, _ = e.(string)
			} else {
				errText = fmt.Sprintf("Attached async root %s did not complete successfully.", root.RunID)
			}
		}
	}
	publication := newObject()
	if s, ok := result.vals["sessionId"].(string); ok {
		publication.set("sessionId", s)
	}
	if s, ok := result.vals["toolCallId"].(string); ok {
		publication.set("toolCallId", s)
	}
	exit := float64(1)
	if success {
		exit = 0
	}
	shown := output
	if !success && output == "" {
		shown = errText
	}
	out := jo("agent", agent, "importedPublication", publication, "output", shown, "success", success, "exitCode", exit)
	if errText != "" {
		out.set("error", errText)
	}
	if timedOut {
		out.set("timedOut", true)
	}
	if stopped {
		out.set("stopped", true)
	}
	pick := func(key string, objs ...*jsObject) {
		if v, ok := firstPresent(key, objs...); ok && jsTruthy(v) {
			out.set(key, v)
		}
	}
	pick("sessionName", child, step)
	pick("sessionFile", child, step, status)
	if child != nil {
		if v, ok := lpGet(child, "intercomTarget"); ok && jsTruthy(v) {
			out.set("intercomTarget", v)
		}
	}
	pick("model", child, step)
	pick("requestedModel", child, step)
	if flag(child, "contextOverflow") || flag(step, "contextOverflow") || (child != nil && jsTruthy(child.vals["contextOverflow"])) || (step != nil && jsTruthy(step.vals["contextOverflow"])) {
		out.set("contextOverflow", true)
	}
	pick("totalCost", child, step)
	if child != nil {
		if u, ok := lpGet(child, "usage"); ok && jsTruthy(u) {
			out.set("usage", u)
		}
	}
	if child != nil && child.has("structuredOutput") {
		out.set("structuredOutput", child.vals["structuredOutput"])
	} else if step != nil && step.has("structuredOutput") {
		out.set("structuredOutput", step.vals["structuredOutput"])
	}
	pick("structuredOutputPath", child, step)
	pick("structuredOutputSchemaPath", child, step)
	pick("acceptance", child, step)
	for _, k := range []string{"artifactPaths", "savedOutputPath", "outputSaveError"} {
		if child != nil {
			if v, ok := lpGet(child, k); ok && jsTruthy(v) {
				out.set(k, v)
			}
		}
	}
	pick("transcriptPath", child, step)
	if child != nil {
		if v, ok := lpGet(child, "transcriptError"); ok && jsTruthy(v) {
			out.set("transcriptError", v)
		}
	}
	if state != "" {
		est := state
		if state == "complete" {
			est = "completed"
		}
		ex := jo("status", est, "success", success, "exitCode", exit)
		if errText != "" {
			ex.set("error", errText)
		}
		out.set("execution", ex)
	}
	pick("effects", child, step)
	return out
}

// WaitForImportedAsyncRoot is waitForImportedAsyncRoot: polls the root's status and result until a result file appears, its
// status is terminal for longer than the grace (then the status stands in for the result), or the caller aborts it. The
// result is the imported step as ordered JSON.
func WaitForImportedAsyncRoot(root ImportedAsyncRoot, options ImportedAsyncRootOptions) (*jsObject, error) {
	poll := int64(500)
	if options.PollIntervalMs != nil {
		poll = *options.PollIntervalMs
	}
	grace := int64(1000)
	if options.TerminalResultGraceMs != nil {
		grace = *options.TerminalResultGraceMs
	}
	now := options.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	sleep := options.Sleep
	if sleep == nil {
		sleep = func(ms int64) { time.Sleep(time.Duration(ms) * time.Millisecond) }
	}
	var terminalSince *int64
	for {
		status, err := readStatusObject(root.AsyncDir)
		if err != nil {
			return nil, err
		}
		if options.ShouldAbort != nil && options.ShouldAbort() {
			msg := options.TimeoutMessage
			if msg == "" {
				msg = "Subagent timed out."
			}
			return outputFromTimeout(root, status, msg), nil
		}
		result, err := readImportedResult(root, status)
		if err != nil {
			return nil, err
		}
		if result != nil {
			return buildImportedResult(root, status, result), nil
		}
		if isTerminalImportedStatus(status, root.Index) {
			if terminalSince == nil {
				t := now()
				terminalSince = &t
			}
			if now()-*terminalSince >= grace {
				return outputFromTerminalStatus(root, status, selectedStatusStep(status, root.Index)), nil
			}
		} else {
			terminalSince = nil
		}
		if status == nil {
			if _, err := os.Stat(root.AsyncDir); err != nil {
				return nil, fmt.Errorf("Attached async root '%s' directory does not exist: %s", root.RunID, root.AsyncDir)
			}
		}
		sleep(poll)
	}
}

// ResolveAsyncRootResultPath is resolveAsyncRootResultPath.
func ResolveAsyncRootResultPath(resultsDir, runID string) string {
	return resultFilePath(resultsDir, runID)
}
