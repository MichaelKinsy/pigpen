package pi_subagents

// The resume target: which finished (or live) child of a run `subagent {action: "resume"}` revives. Ports
// resolveAsyncResumeTarget, validateStatusForResume and the result-file validation of runs/background/async-resume.ts.
//
// The status and the result file are read as loose JSON and every field the resolver uses is type-checked, so a damaged file
// is named in the error rather than half-used. Capability ceilings, managed worktree handoffs and workflow admission belong to
// slices the port does not have (it never writes them): a run that carries a ceiling is listed in `unsupported`, so a resume
// refuses it instead of dropping the constraint.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// resumeParams are the resume action's run selectors; Index is nil when absent and a float so a non-integer is caught.
type resumeParams struct {
	ID, RunID, Dir string
	Index          *float64
}

type resumeOptions struct {
	// RequireSessionFile defaults to true: a revived child needs the session to continue.
	RequireSessionFile *bool
	SessionID          string
}

func isWorkflowMode(m string) bool { return m == "workflow" }

func ensureObject(v any, source string) (jm, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Async result file '%s' must contain a JSON object.", source)
	}
	return m, nil
}

func optionalString(m jm, field, source, display string) (string, error) {
	v, has := m[field]
	if !has {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("Invalid async result file '%s': %s must be a string.", source, display)
	}
	return s, nil
}

// validateResultFile checks the fields a resume reads from a result file.
func validateResultFile(value any, resultPath string) (jm, error) {
	data, err := ensureObject(value, resultPath)
	if err != nil {
		return nil, err
	}
	if rv, has := data["results"]; has {
		arr, ok := rv.([]any)
		if !ok {
			return nil, fmt.Errorf("Invalid async result file '%s': results must be an array.", resultPath)
		}
		for i, e := range arr {
			child, err := ensureObject(e, fmt.Sprintf("%s results[%d]", resultPath, i))
			if err != nil {
				return nil, err
			}
			for _, f := range []string{"agent", "sessionFile", "sessionName", "intercomTarget", "model", "thinking", "launchContractDigest"} {
				if _, err := optionalString(child, f, resultPath, fmt.Sprintf("results[%d].%s", i, f)); err != nil {
					return nil, err
				}
			}
			if s, has := child["success"]; has {
				if _, ok := s.(bool); !ok {
					return nil, fmt.Errorf("Invalid async result file '%s': results[%d].success must be a boolean.", resultPath, i)
				}
			}
		}
	}
	if s, has := data["success"]; has {
		if _, ok := s.(bool); !ok {
			return nil, fmt.Errorf("Invalid async result file '%s': success must be a boolean.", resultPath)
		}
	}
	for _, f := range []string{"id", "runId", "agent", "mode", "state", "cwd", "sessionId", "sessionFile", "model", "thinking", "launchContractDigest"} {
		if _, err := optionalString(data, f, resultPath, f); err != nil {
			return nil, err
		}
	}
	return data, nil
}

func readResultFileLoose(resultPath string) (jm, error) {
	b, err := os.ReadFile(resultPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to read async result file '%s': %s", resultPath, err.Error())
	}
	v, err := parseLooseJSON(b)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse async result file '%s': %s", resultPath, jsParseErrorText(b, err))
	}
	return validateResultFile(v, resultPath)
}

// validateStatusForResume checks the status fields a resume reads.
func validateStatusForResume(st jm, source string) error {
	if st == nil {
		return nil
	}
	if _, ok := st["runId"].(string); !ok {
		return fmt.Errorf("Invalid async status '%s': runId must be a string.", source)
	}
	for _, f := range []string{"sessionId", "cwd", "sessionFile"} {
		if v, has := st[f]; has {
			if _, ok := v.(string); !ok {
				return fmt.Errorf("Invalid async status '%s': %s must be a string.", source, f)
			}
		}
	}
	if sv, has := st["steps"]; has {
		arr, ok := sv.([]any)
		if !ok {
			return fmt.Errorf("Invalid async status '%s': steps must be an array.", source)
		}
		for i, e := range arr {
			step, ok := e.(map[string]any)
			if !ok {
				return fmt.Errorf("Invalid async status '%s': steps[%d] must be an object.", source, i)
			}
			if _, ok := step["agent"].(string); !ok {
				return fmt.Errorf("Invalid async status '%s': steps[%d].agent must be a string.", source, i)
			}
			for _, f := range []string{"sessionFile", "sessionName", "model", "thinking", "launchContractDigest"} {
				if v, has := step[f]; has {
					if _, ok := v.(string); !ok {
						return fmt.Errorf("Invalid async status '%s': steps[%d].%s must be a string.", source, i, f)
					}
				}
			}
		}
	}
	return nil
}

func resultStateOf(result jm) string {
	s, _ := result["state"].(string)
	switch s {
	case "complete", "failed", "partial", "paused", "stopped", "running", "queued":
		return s
	}
	if result["success"] == true {
		return "complete"
	}
	return "failed"
}

func validateResumeSessionFile(runID, sessionFile string) (string, error) {
	if filepath.Ext(sessionFile) != ".jsonl" {
		return "", fmt.Errorf("Async run '%s' session file must be a .jsonl file: %s", runID, sessionFile)
	}
	abs, _ := filepath.Abs(sessionFile)
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("Async run '%s' session file does not exist: %s", runID, sessionFile)
	}
	return abs, nil
}

func validateResumeCwd(runID, cwd string) (string, error) {
	if cwd == "" {
		return "", nil
	}
	abs, _ := filepath.Abs(cwd)
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("Async run '%s' required cwd does not exist: %s", runID, cwd)
	}
	return abs, nil
}

// asyncReviveRequiresRecoveryDescriptor is whether a revive without a recovery descriptor must be refused: only a workflow child
// that carries its session and a capability ceiling can be revived without one.
func asyncReviveRequiresRecoveryDescriptor(t *resumeTarget) bool {
	if t.descriptor != nil {
		return false
	}
	return !(t.mode == "workflow" && t.sessionFile != "" && t.carriesCeiling)
}

func firstStr(vals ...any) string {
	for _, v := range vals {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func carriesCapabilityCeiling(st, result jm) bool {
	has := func(m jm, k string) bool { v, ok := m[k]; return ok && v != nil }
	if st != nil {
		if has(st, "capabilityCeiling") || has(st, "admissionCapabilityCeiling") {
			return true
		}
		for _, s := range mapList(st["steps"]) {
			if has(s, "capabilityCeiling") {
				return true
			}
		}
	}
	if result != nil {
		if has(result, "capabilityCeiling") || has(result, "admissionCapabilityCeiling") {
			return true
		}
		for _, s := range mapList(result["results"]) {
			if has(s, "capabilityCeiling") {
				return true
			}
		}
	}
	return false
}

func readStatusLoose(asyncDir string) jm {
	if asyncDir == "" {
		return nil
	}
	return readLooseJSON(filepath.Join(asyncDir, "status.json"))
}

// resolveAsyncResumeTarget chooses the child to revive (or the live one), checking everything the revival relies on.
func resolveAsyncResumeTarget(p resumeParams, dirs asyncDirs, o resumeOptions) (*resumeTarget, error) {
	requireSession := o.RequireSessionFile == nil || *o.RequireSessionFile
	loc, err := resolveAsyncRunLocation(RunLocationParams{ID: p.ID, RunID: p.RunID, Dir: p.Dir}, dirs.Async, dirs.Results)
	if err != nil {
		return nil, err
	}
	if loc.AsyncDir == "" && loc.ResultPath == "" {
		return nil, errRunNotFound
	}
	statusSource := "status.json"
	if loc.AsyncDir != "" {
		statusSource = filepath.Join(loc.AsyncDir, "status.json")
	}
	var status jm
	if loc.AsyncDir != "" {
		// the status is validated before the reconciler reads it into its typed form, which would name a damaged field differently
		if pre := readStatusLoose(loc.AsyncDir); pre != nil {
			if err := validateStatusForResume(pre, statusSource); err != nil {
				return nil, err
			}
		}
		if _, err := reconcileAsyncRun(loc.AsyncDir, ReconcileOptions{ResultsDir: dirs.Results}); err != nil {
			return nil, err
		}
		status = readStatusLoose(loc.AsyncDir)
		if err := validateStatusForResume(status, statusSource); err != nil {
			return nil, err
		}
	}
	desc, err := readAsyncRecoveryDescriptor(loc.AsyncDir)
	if err != nil {
		return nil, err
	}
	var result jm
	if loc.ResultPath != "" {
		if result, err = readResultFileLoose(loc.ResultPath); err != nil {
			return nil, err
		}
	}
	runID := firstStr(status["runId"], result["runId"], result["id"], loc.ResolvedID)
	if runID == "" {
		if loc.AsyncDir != "" {
			runID = filepath.Base(loc.AsyncDir)
		} else {
			runID = "unknown"
		}
	}
	mode := firstStr(status["mode"])
	if mode == "" {
		if m, _ := result["mode"].(string); m == "single" || m == "parallel" || m == "chain" || m == "workflow" {
			mode = m
		}
	}
	if o.SessionID != "" && ((status != nil && strOf(status["sessionId"]) != o.SessionID) || (result != nil && strOf(result["sessionId"]) != o.SessionID)) {
		return nil, fmt.Errorf("Async run '%s' was not found in the active session.", runID)
	}
	if desc != nil && desc.SourceRunID != runID {
		return nil, fmt.Errorf("Async run '%s' has a recovery descriptor for a different source run.", runID)
	}
	state := strOf(status["state"])
	if state == "" && result != nil {
		state = resultStateOf(result)
	}
	if state == "" {
		return nil, fmt.Errorf("Status file not found for async run '%s'.", runID)
	}
	if state == "stopped" {
		return nil, fmt.Errorf("Async run '%s' was stopped and cannot be resumed. Start a new run instead.", runID)
	}

	statusSteps := mapList(status["steps"])
	resultSteps := mapList(result["results"])
	stepCount := len(statusSteps)
	if stepCount == 0 {
		stepCount = len(resultSteps)
	}
	if stepCount == 0 && strOf(result["agent"]) != "" {
		stepCount = 1
	}
	requested := -1
	if p.Index != nil {
		f := *p.Index
		if f != math.Trunc(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("Async run '%s' index must be an integer.", runID)
		}
		requested = int(f)
	}
	hasIndex := p.Index != nil
	unsupported := func() []string {
		var u []string
		if desc != nil {
			u = resumeUnsupportedFields(desc)
		}
		if carriesCapabilityCeiling(status, result) {
			u = append(u, "capabilityCeiling")
		}
		sort.Strings(u)
		return u
	}
	stepAt := func(steps []jm, i int) jm {
		if i >= 0 && i < len(steps) {
			return steps[i]
		}
		return nil
	}
	live := func(step jm, index int) *resumeTarget {
		digest := firstStr(step["launchContractDigest"], stepAt(resultSteps, index)["launchContractDigest"], result["launchContractDigest"])
		if digest == "" && desc != nil {
			digest = desc.LaunchContractDigest
		}
		return &resumeTarget{kind: "live", live: true, runID: runID, asyncDir: loc.AsyncDir, state: state, mode: mode, agent: strOf(step["agent"]), index: index,
			sessionName: firstStr(step["sessionName"], stepAt(resultSteps, index)["sessionName"]),
			cwd:         firstStr(status["cwd"], result["cwd"]), sessionFile: firstStr(step["sessionFile"], status["sessionFile"], result["sessionFile"]),
			model: strOf(step["model"]), thinking: strOf(step["thinking"]), launchContractDigest: digest, descriptor: desc, unsupported: unsupported(), carriesCeiling: carriesCapabilityCeiling(status, result)}
	}
	terminalStepStatuses := map[string]bool{"complete": true, "completed": true, "failed": true, "paused": true}
	if state == "running" {
		if hasIndex {
			if requested < 0 || requested >= stepCount {
				return nil, fmt.Errorf("Async run '%s' has %d children. Index %d is out of range.", runID, stepCount, requested)
			}
			if sel := stepAt(statusSteps, requested); sel != nil {
				switch status := strOf(sel["status"]); {
				case status == "running":
					return live(sel, requested), nil
				case status == "pending":
					return nil, fmt.Errorf("Async run '%s' child %d is pending and has not started yet. Wait for it to run or complete before resuming.", runID, requested)
				case !terminalStepStatuses[status]:
					return nil, fmt.Errorf("Async run '%s' child %d is %s and cannot be revived yet.", runID, requested, status)
				}
			}
		} else {
			var running []int
			for i, s := range statusSteps {
				if s["status"] == "running" {
					running = append(running, i)
				}
			}
			if len(running) != 1 {
				return nil, fmt.Errorf("Async run '%s' has %d running children. Provide index to choose one.", runID, len(running))
			}
			return live(statusSteps[running[0]], running[0]), nil
		}
	}
	if stepCount > 1 && !hasIndex {
		return nil, fmt.Errorf("Async run '%s' has %d children. Provide index to choose one.", runID, stepCount)
	}
	index := 0
	if hasIndex {
		index = requested
	}
	if index < 0 || index >= stepCount {
		return nil, fmt.Errorf("Async run '%s' has %d children. Index %d is out of range.", runID, stepCount, index)
	}
	sel := stepAt(statusSteps, index)
	if sel != nil && (sel["status"] == "stopped" || sel["stopped"] == true) {
		return nil, fmt.Errorf("Async run '%s' child %d was stopped and cannot be resumed. Start a new run instead.", runID, index)
	}
	agent := firstStr(sel["agent"], stepAt(resultSteps, index)["agent"], result["agent"])
	if agent == "" {
		return nil, fmt.Errorf("Could not determine child agent for async run '%s'.", runID)
	}
	if desc != nil && desc.Agent != agent {
		return nil, fmt.Errorf("Async run '%s' has a recovery descriptor for '%s', not '%s'.", runID, desc.Agent, agent)
	}
	rs := stepAt(resultSteps, index)
	sessionFile := firstStr(sel["sessionFile"], rs["sessionFile"])
	if sessionFile == "" && stepCount == 1 {
		sessionFile = firstStr(status["sessionFile"], result["sessionFile"])
	}
	if sessionFile == "" && requireSession {
		return nil, fmt.Errorf("Async run '%s' child %d does not have a persisted session file to resume from.", runID, index)
	}
	resolvedSession := ""
	if sessionFile != "" {
		if resolvedSession, err = validateResumeSessionFile(runID, sessionFile); err != nil {
			return nil, err
		}
	}
	single := func(key string) string {
		if stepCount == 1 {
			return strOf(result[key])
		}
		return ""
	}
	model := firstStr(sel["model"], rs["model"], single("model"))
	thinking := firstStr(sel["thinking"], rs["thinking"], single("thinking"))
	descCwd := ""
	if desc != nil {
		descCwd = desc.Cwd
	}
	cwd, err := validateResumeCwd(runID, firstStr(status["cwd"], result["cwd"], descCwd))
	if err != nil {
		return nil, err
	}
	digest := firstStr(sel["launchContractDigest"], rs["launchContractDigest"], result["launchContractDigest"])
	if digest == "" && desc != nil {
		digest = desc.LaunchContractDigest
	}
	return &resumeTarget{kind: "revive", runID: runID, asyncDir: loc.AsyncDir, state: state, mode: mode, agent: agent, index: index, cwd: cwd, sessionFile: resolvedSession,
		sessionName: firstStr(sel["sessionName"], rs["sessionName"]), model: model, thinking: thinking, launchContractDigest: digest, descriptor: desc, unsupported: unsupported(),
		carriesCeiling: carriesCapabilityCeiling(status, result)}, nil
}
