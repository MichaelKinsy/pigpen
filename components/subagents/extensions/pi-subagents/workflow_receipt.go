package pi_subagents

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Port of src/workflows/workflow-receipt.ts: the terminal receipt of a workflow run (workflow-receipt.json in its async
// directory), written when the run ends and read back strictly, so that a later `runs.run(key, { resume })` can name a child
// of that run. A receipt is ordered JSON (*jsObject): it keeps what a reader does not know, as the original's does.

const (
	workflowReceiptVersion  = 1
	workflowReceiptFile     = "workflow-receipt.json"
	maxWorkflowReceiptBytes = 2 * 1024 * 1024
)

var (
	workflowResourceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	sha256HexPattern          = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

func assertSafeRunID(value, label string) (string, error) {
	normalized := jsTrim(value)
	if normalized == "" || filepath.Base(normalized) != normalized || normalized == "." || normalized == ".." {
		return "", fmt.Errorf("%s must be an exact workflow run id, not a path or prefix.", label)
	}
	return normalized, nil
}

func assertReceiptKey(value, label string) (string, error) {
	if !workflowKeyPattern.MatchString(value) {
		return "", fmt.Errorf("%s is invalid.", label)
	}
	return value, nil
}

// WorkflowReceiptPath is workflowReceiptPath.
func WorkflowReceiptPath(asyncDirRoot, workflowRunID string) (string, error) {
	id, err := assertSafeRunID(workflowRunID, "workflowRunId")
	if err != nil {
		return "", err
	}
	return filepath.Join(asyncDirRoot, id, workflowReceiptFile), nil
}

// WorkflowReceiptInput is the argument of buildWorkflowReceipt. Children are the results a script received.
type WorkflowReceiptInput struct {
	WorkflowRunID    string
	State            string // complete, failed, paused, stopped
	Children         []*jsObject
	ArgsDigest       string
	HostSteps        []any
	WorkflowChildren *WorkflowChildSummary
	Resource         *jsObject
	TerminalOutcome  *jsObject
	CreatedAt        *float64
}

func receiptStringAt(o *jsObject, k string) string {
	s, _ := o.vals[k].(string)
	return s
}

// childSummaryObject is a published summary as ordered JSON.
func childSummaryObject(s *WorkflowChildSummary) (*jsObject, error) {
	b, err := jsonMarshalNoEscape(s)
	if err != nil {
		return nil, err
	}
	v, err := parseJSON(b)
	if err != nil {
		return nil, err
	}
	return v.(*jsObject), nil
}

// BuildWorkflowReceipt is buildWorkflowReceipt: one metadata-only entry per child, never its output.
func BuildWorkflowReceipt(in WorkflowReceiptInput) (*jsObject, error) {
	runID, err := assertSafeRunID(in.WorkflowRunID, "workflowRunId")
	if err != nil {
		return nil, err
	}
	if in.ArgsDigest != "" && !sha256HexPattern.MatchString(in.ArgsDigest) {
		return nil, errors.New("workflow receipt argsDigest must be a lowercase SHA-256 digest.")
	}
	if in.WorkflowChildren != nil && in.WorkflowChildren.WorkflowRunID != "" && in.WorkflowChildren.WorkflowRunID != runID {
		return nil, errors.New("workflowChildren workflowRunId does not match its receipt.")
	}
	entries := newObject()
	for _, child := range in.Children {
		key, err := assertReceiptKey(receiptStringAt(child, "key"), "workflow receipt child key")
		if err != nil {
			return nil, err
		}
		if _, dup := entries.vals[key]; dup {
			return nil, fmt.Errorf("Workflow receipt has duplicate child key '%s'.", key)
		}
		laneLabel := fmt.Sprintf("workflow receipt child '%s'.lane", key)
		laneValue, has := child.vals["lane"]
		if !has {
			laneValue = undef{}
		}
		lane, err := NormalizeWorkflowLaneMetadata(laneValue, laneLabel)
		if err != nil {
			return nil, err
		}
		if err := AssertWorkflowLaneKey(lane, key, laneLabel); err != nil {
			return nil, err
		}
		var candidates []any
		if cont := child.obj("continuation"); cont != nil {
			if ids, ok := cont.vals["runIds"].([]any); ok {
				candidates = ids
			}
		}
		if _, ok := lpGet(child.obj("continuation"), "runIds"); !ok {
			if r := receiptStringAt(child, "runId"); r != "" {
				candidates = []any{r}
			}
		}
		var runIDs []any
		seen := map[string]bool{}
		for _, c := range candidates {
			s, ok := c.(string)
			if !ok || jsTrim(s) == "" {
				continue
			}
			s = jsTrim(s)
			if !seen[s] {
				seen[s] = true
				runIDs = append(runIDs, s)
			}
		}
		if runIDs == nil {
			runIDs = []any{}
		}
		latest := ""
		if len(runIDs) > 0 {
			latest = runIDs[len(runIDs)-1].(string)
		}
		resumability := child.obj("resumability")
		if resumability == nil {
			reason := "child produced no run id"
			if receiptStringAt(child, "runId") != "" {
				reason = "resumability was not recorded"
			}
			resumability = jo("state", "not-resumable", "reason", reason)
		}
		resumable := resumability.vals["state"] == "resumable"
		if resumable && latest == "" {
			return nil, fmt.Errorf("Workflow receipt child '%s' is resumable but has no retained run id.", key)
		}
		entry := jo("key", key)
		if lane != nil {
			entry.set("lane", lane)
		}
		if v, ok := lpGet(child, "terminalOutcome"); ok && jsTruthy(v) {
			entry.set("terminalOutcome", v)
		}
		for _, f := range [][2]string{{"agent", "agent"}, {"requestedContext", "requestedContext"}, {"resolvedContext", "resolvedContext"}, {"outputReference", "outputReference"}} {
			if s := receiptStringAt(child, f[0]); s != "" {
				entry.set(f[1], s)
			}
		}
		if v, ok := lpGet(child, "recovery"); ok && jsTruthy(v) {
			entry.set("acceptanceRecovery", v)
		}
		if v, ok := lpGet(child, "externalAdapter"); ok && jsTruthy(v) {
			entry.set("externalAdapter", v)
		}
		entry.set("continuation", jo("runIds", runIDs))
		if resumable {
			entry.set("latestRunId", latest)
		} else if latest != "" {
			entry.set("latestRunId", latest)
		}
		entry.set("resumability", resumability)
		entries.set(key, entry)
	}
	if len(in.HostSteps) > WorkflowHostStepMaxCount {
		return nil, fmt.Errorf("Workflow receipt has more than %d host steps.", WorkflowHostStepMaxCount)
	}
	var hostSteps []*WorkflowHostStep
	for i, h := range in.HostSteps {
		step, err := ParseWorkflowHostStep(h, fmt.Sprintf("workflow receipt hostSteps[%d]", i))
		if err != nil {
			return nil, err
		}
		hostSteps = append(hostSteps, step)
	}
	if hostSteps != nil {
		if err := AssertUniqueWorkflowHostStepIDs(hostSteps, "workflow receipt"); err != nil {
			return nil, err
		}
	}
	var resourceValue any = undef{}
	if in.Resource != nil {
		resourceValue = in.Resource
	}
	resource, err := parseWorkflowResource(resourceValue, "workflow receipt")
	if err != nil {
		return nil, err
	}
	createdAt := float64(time.Now().UnixMilli())
	if in.CreatedAt != nil {
		createdAt = *in.CreatedAt
	}
	out := jo("version", float64(workflowReceiptVersion), "workflowRunId", runID, "state", in.State, "createdAt", createdAt, "entries", entries)
	if in.ArgsDigest != "" {
		out.set("argsDigest", in.ArgsDigest)
	}
	if resource != nil {
		out.set("resource", resource)
	}
	if len(hostSteps) > 0 {
		arr := make([]any, len(hostSteps))
		for i, h := range hostSteps {
			arr[i] = h.Object()
		}
		out.set("hostSteps", arr)
	}
	if in.WorkflowChildren != nil {
		summary, err := childSummaryObject(in.WorkflowChildren)
		if err != nil {
			return nil, err
		}
		out.set("workflowChildren", summary)
	}
	if in.TerminalOutcome != nil {
		out.set("terminalOutcome", in.TerminalOutcome)
	}
	return out, nil
}

// WriteWorkflowReceipt is writeWorkflowReceipt: a private atomic write into the workflow's async directory.
func WriteWorkflowReceipt(asyncDir string, receipt *jsObject) (string, error) {
	p := filepath.Join(asyncDir, workflowReceiptFile)
	if err := asyncWritePrivateJSON(p, receipt); err != nil {
		return "", err
	}
	return p, nil
}

func readWorkflowReceiptFile(path string) (any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("workflow receipt is not a regular file.")
	}
	if st.Size() > maxWorkflowReceiptBytes {
		return nil, fmt.Errorf("workflow receipt exceeds the %d-byte limit.", maxWorkflowReceiptBytes)
	}
	buf := make([]byte, st.Size())
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, errors.New("workflow receipt ended before its declared size.")
	}
	if msg := workflowJSONError(string(buf)); msg != "" {
		return nil, errors.New(msg)
	}
	return parseJSON(buf)
}

func parseReceiptTerminalOutcome(value any, label string) (*jsObject, error) {
	if _, u := value.(undef); u {
		return nil, nil
	}
	o, ok := value.(*jsObject)
	if !ok {
		return nil, fmt.Errorf("%s must be an object.", label)
	}
	reason, _ := o.vals["reason"].(string)
	if o.vals["state"] != "partial" || (reason != "budget_exhausted" && reason != "timeout") {
		return nil, fmt.Errorf("%s is invalid.", label)
	}
	return jo("state", "partial", "reason", reason), nil
}

func parseAcceptanceRecoveryMetadata(value any, label string) (*jsObject, error) {
	if _, u := value.(undef); u {
		return nil, nil
	}
	o, ok := value.(*jsObject)
	if !ok {
		return nil, fmt.Errorf("%s must be an object.", label)
	}
	if o.vals["status"] != "available-for-review" || o.vals["reason"] != "acceptance-metadata-rejected" {
		return nil, fmt.Errorf("%s is invalid.", label)
	}
	path, pathOK := o.vals["reportPath"].(string)
	hash, hashOK := o.vals["reportHash"].(string)
	if !pathOK || jsTrim(path) == "" || !hashOK || !sha256HexPattern.MatchString(hash) {
		return nil, fmt.Errorf("%s is invalid.", label)
	}
	return jo("status", "available-for-review", "reason", "acceptance-metadata-rejected", "reportPath", path, "reportHash", hash), nil
}

func parseWorkflowResource(value any, source string) (*jsObject, error) {
	if _, u := value.(undef); u {
		return nil, nil
	}
	o, ok := value.(*jsObject)
	if !ok {
		return nil, fmt.Errorf("Invalid workflow receipt '%s': resource must be an object.", source)
	}
	name, nameOK := o.vals["name"].(string)
	id, idOK := o.vals["id"].(string)
	version, isNum := o.vals["version"].(float64)
	if o.vals["kind"] != "workflow" || !nameOK || !workflowKeyPattern.MatchString(name) || !isNum || version != math.Trunc(version) || version < 1 || version > 1000000 ||
		o.vals["invocation"] != "named" || o.vals["expansion"] != "resolved" || !idOK || !workflowResourceIDPattern.MatchString(id) {
		return nil, fmt.Errorf("Invalid workflow receipt '%s': resource is invalid.", source)
	}
	return jo("kind", "workflow", "name", name, "version", version, "invocation", "named", "expansion", "resolved", "id", id), nil
}

func receiptEntryError(source, key, format string, a ...any) error {
	return fmt.Errorf("Invalid workflow receipt '%s': entry '%s' %s", source, key, fmt.Sprintf(format, a...))
}

func parseReceiptEntry(value any, key, source string) (*jsObject, error) {
	entry, ok := value.(*jsObject)
	if !ok {
		return nil, receiptEntryError(source, key, "must be an object.")
	}
	if entry.vals["key"] != key {
		return nil, receiptEntryError(source, key, "has a mismatched key.")
	}
	latest, hasLatest := entry.vals["latestRunId"]
	if _, u := latest.(undef); u {
		hasLatest = false
	}
	latestRunID, latestIsString := latest.(string)
	if hasLatest && (!latestIsString || jsTrim(latestRunID) == "") {
		return nil, receiptEntryError(source, key, "latestRunId must be non-empty.")
	}
	cont, ok := entry.vals["continuation"].(*jsObject)
	var runIDs []any
	if ok {
		runIDs, ok = cont.vals["runIds"].([]any)
	}
	if !ok {
		return nil, receiptEntryError(source, key, "continuation is missing.")
	}
	for _, r := range runIDs {
		if s, isStr := r.(string); !isStr || jsTrim(s) == "" {
			return nil, receiptEntryError(source, key, "continuation contains an invalid run id.")
		}
	}
	if hasLatest && (len(runIDs) == 0 || runIDs[len(runIDs)-1] != latestRunID) {
		return nil, fmt.Errorf("Workflow receipt '%s' entry '%s' is stale: latestRunId does not match its continuation lineage.", source, key)
	}
	resumability, ok := entry.vals["resumability"].(*jsObject)
	if !ok {
		return nil, receiptEntryError(source, key, "resumability is missing.")
	}
	state, _ := resumability.vals["state"].(string)
	if state != "resumable" && state != "not-resumable" {
		return nil, receiptEntryError(source, key, "resumability state is invalid.")
	}
	reason, reasonIsString := resumability.vals["reason"].(string)
	if state == "resumable" && !hasLatest {
		return nil, receiptEntryError(source, key, "resumable entry has no retained run id.")
	}
	if state == "not-resumable" && (!reasonIsString || jsTrim(reason) == "") {
		return nil, receiptEntryError(source, key, "non-resumable reason is missing.")
	}
	outcomeValue := anyOrUndef(entry, "terminalOutcome")
	terminalOutcome, err := parseReceiptTerminalOutcome(outcomeValue, fmt.Sprintf("Invalid workflow receipt '%s': entry '%s' terminalOutcome", source, key))
	if err != nil {
		return nil, err
	}
	recovery, err := parseAcceptanceRecoveryMetadata(anyOrUndef(entry, "acceptanceRecovery"), fmt.Sprintf("Invalid workflow receipt '%s': entry '%s' acceptanceRecovery", source, key))
	if err != nil {
		return nil, err
	}
	if err := parseExternalCliReceiptMetadata(anyOrUndef(entry, "externalAdapter"), key, source); err != nil {
		return nil, err
	}
	laneLabel := fmt.Sprintf("Invalid workflow receipt '%s': entry '%s'.lane", source, key)
	lane, err := NormalizeWorkflowLaneMetadata(anyOrUndef(entry, "lane"), laneLabel)
	if err != nil {
		return nil, err
	}
	if err := AssertWorkflowLaneKey(lane, key, laneLabel); err != nil {
		return nil, err
	}
	out := entry.clone()
	if lane != nil {
		out.set("lane", lane)
	}
	if terminalOutcome != nil {
		out.set("terminalOutcome", terminalOutcome)
	}
	if recovery != nil {
		out.set("acceptanceRecovery", recovery)
	}
	return out, nil
}

// anyOrUndef is `object[key]`: undef{} for an absent or undefined property.
func anyOrUndef(o *jsObject, k string) any {
	if v, ok := o.vals[k]; ok {
		return v
	}
	return undef{}
}

func parseReceiptRecovery(value any, workflowRunID string, entries *jsObject, source string) ([]any, error) {
	if _, u := value.(undef); u {
		return nil, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("Invalid workflow receipt '%s': recovery must be an array.", source)
	}
	out := make([]any, 0, len(list))
	for i, item := range list {
		action, ok := item.(*jsObject)
		if !ok {
			return nil, fmt.Errorf("Invalid workflow receipt '%s': recovery[%d] must be an object.", source, i)
		}
		key, keyOK := action.vals["key"].(string)
		resume, resumeOK := action.vals["resume"].(*jsObject)
		var entry *jsObject
		if keyOK {
			entry, _ = entries.vals[key].(*jsObject)
		}
		if !keyOK || entry == nil || action.vals["call"] != "runs.run" || action.vals["taskRequired"] != true || !resumeOK {
			return nil, fmt.Errorf("Invalid workflow receipt '%s': recovery[%d] is invalid.", source, i)
		}
		if resume.vals["workflowRunId"] != workflowRunID || resume.vals["key"] != key || resume.vals["latest"] != true || entry.obj("resumability").vals["state"] != "resumable" {
			return nil, fmt.Errorf("Invalid workflow receipt '%s': recovery[%d] does not identify a resumable entry.", source, i)
		}
		out = append(out, jo("key", key, "call", "runs.run", "resume", jo("workflowRunId", workflowRunID, "key", key, "latest", true), "taskRequired", true))
	}
	return out, nil
}

// ReadWorkflowReceipt is readWorkflowReceipt: the receipt of a terminal workflow, strictly validated; a stale or malformed
// file is an error naming why.
func ReadWorkflowReceipt(asyncDirRoot, workflowRunID string) (*jsObject, error) {
	receiptPath, err := WorkflowReceiptPath(asyncDirRoot, workflowRunID)
	if err != nil {
		return nil, err
	}
	value, err := readWorkflowReceiptFile(receiptPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			dir := filepath.Dir(receiptPath)
			for _, f := range []string{"status.json", "events.jsonl"} {
				if _, statErr := os.Stat(filepath.Join(dir, f)); statErr == nil {
					return nil, fmt.Errorf("Workflow receipt '%s' is not available because the workflow may still be active or terminal receipt writing failed. Use direct child run IDs from status/events for direct resume after the normal retained-child checks.", workflowRunID)
				}
			}
			return nil, fmt.Errorf("Workflow receipt '%s' was not found.", workflowRunID)
		}
		return nil, fmt.Errorf("Workflow receipt '%s' could not be read: %s", workflowRunID, err.Error())
	}
	receipt, ok := value.(*jsObject)
	if !ok {
		return nil, fmt.Errorf("Invalid workflow receipt '%s': expected an object.", receiptPath)
	}
	if receipt.vals["version"] != float64(workflowReceiptVersion) {
		return nil, fmt.Errorf("Invalid workflow receipt '%s': unsupported version.", receiptPath)
	}
	if receipt.vals["workflowRunId"] != workflowRunID {
		return nil, fmt.Errorf("Workflow receipt '%s' is stale: workflowRunId does not match.", receiptPath)
	}
	state, _ := receipt.vals["state"].(string)
	if state != "complete" && state != "failed" && state != "paused" && state != "stopped" {
		return nil, fmt.Errorf("Workflow receipt '%s' is stale: workflow is not terminal.", receiptPath)
	}
	createdAt, isNum := receipt.vals["createdAt"].(float64)
	if !isNum || math.IsNaN(createdAt) || math.IsInf(createdAt, 0) {
		return nil, fmt.Errorf("Invalid workflow receipt '%s': createdAt is invalid.", receiptPath)
	}
	rawEntries, ok := receipt.vals["entries"].(*jsObject)
	if !ok {
		return nil, fmt.Errorf("Invalid workflow receipt '%s': entries must be an object.", receiptPath)
	}
	entries := newObject()
	for _, k := range rawEntries.order() {
		if _, err := assertReceiptKey(k, "workflow receipt key"); err != nil {
			return nil, err
		}
		e, err := parseReceiptEntry(rawEntries.vals[k], k, receiptPath)
		if err != nil {
			return nil, err
		}
		entries.set(k, e)
	}
	summary, err := ParseWorkflowChildSummary(anyOrUndef(receipt, "workflowChildren"))
	if err != nil {
		return nil, err
	}
	if summary != nil && summary.WorkflowRunID != workflowRunID {
		return nil, fmt.Errorf("Workflow receipt '%s' is stale: workflowChildren.workflowRunId does not match.", receiptPath)
	}
	var hostSteps []any
	if hs, has := receipt.vals["hostSteps"]; has {
		if _, u := hs.(undef); !u {
			list, ok := hs.([]any)
			if !ok {
				return nil, fmt.Errorf("Invalid workflow receipt '%s': hostSteps must be an array.", receiptPath)
			}
			if len(list) > WorkflowHostStepMaxCount {
				return nil, fmt.Errorf("Invalid workflow receipt '%s': hostSteps exceeds %d entries.", receiptPath, WorkflowHostStepMaxCount)
			}
			var steps []*WorkflowHostStep
			for i, h := range list {
				s, err := ParseWorkflowHostStep(h, fmt.Sprintf("%s hostSteps[%d]", receiptPath, i))
				if err != nil {
					return nil, err
				}
				steps = append(steps, s)
				hostSteps = append(hostSteps, s.Object())
			}
			if err := AssertUniqueWorkflowHostStepIDs(steps, receiptPath); err != nil {
				return nil, err
			}
		}
	}
	var resolution any
	if r, has := receipt.vals["workflowResolution"]; has {
		if _, u := r.(undef); !u {
			if r != "settled-awaiting-resume" && r != "failed-child" && r != "interrupted-child" {
				return nil, fmt.Errorf("Invalid workflow receipt '%s': workflowResolution is invalid.", receiptPath)
			}
			resolution = r
		}
	}
	argsDigest := ""
	if d, has := receipt.vals["argsDigest"]; has {
		if _, u := d.(undef); !u {
			s, isStr := d.(string)
			if !isStr || !sha256HexPattern.MatchString(s) {
				return nil, fmt.Errorf("Invalid workflow receipt '%s': argsDigest is invalid.", receiptPath)
			}
			argsDigest = s
		}
	}
	resource, err := parseWorkflowResource(anyOrUndef(receipt, "resource"), receiptPath)
	if err != nil {
		return nil, err
	}
	terminalOutcome, err := parseReceiptTerminalOutcome(anyOrUndef(receipt, "terminalOutcome"), fmt.Sprintf("Invalid workflow receipt '%s': terminalOutcome", receiptPath))
	if err != nil {
		return nil, err
	}
	recovery, err := parseReceiptRecovery(anyOrUndef(receipt, "recovery"), workflowRunID, entries, receiptPath)
	if err != nil {
		return nil, err
	}
	out := jo("version", float64(1), "workflowRunId", workflowRunID, "state", state, "createdAt", createdAt, "entries", entries)
	if argsDigest != "" {
		out.set("argsDigest", argsDigest)
	}
	if resource != nil {
		out.set("resource", resource)
	}
	if len(hostSteps) > 0 {
		out.set("hostSteps", hostSteps)
	}
	if summary != nil {
		s, err := childSummaryObject(summary)
		if err != nil {
			return nil, err
		}
		out.set("workflowChildren", s)
	}
	if resolution != nil {
		out.set("workflowResolution", resolution)
	}
	if terminalOutcome != nil {
		out.set("terminalOutcome", terminalOutcome)
	}
	if recovery != nil {
		out.set("recovery", recovery)
	}
	return out, nil
}

// WorkflowReceiptResumeReference is the keyed reference `{ workflowRunId, key, latest: true }` of a resume.
type WorkflowReceiptResumeReference struct {
	WorkflowRunID string
	Key           string
	Latest        bool
}

// ResolveWorkflowReceiptResumeEntry is resolveWorkflowReceiptResumeEntry: the resumable entry a keyed reference names.
// assertResumable, when set, may veto the retained run id.
func ResolveWorkflowReceiptResumeEntry(ref WorkflowReceiptResumeReference, asyncDirRoot string, assertResumable func(runID string) error) (*jsObject, error) {
	if !ref.Latest {
		return nil, errors.New("Keyed workflow receipt resume requires latest: true.")
	}
	key, err := assertReceiptKey(ref.Key, "keyed resume key")
	if err != nil {
		return nil, err
	}
	receipt, err := ReadWorkflowReceipt(asyncDirRoot, jsTrim(ref.WorkflowRunID))
	if err != nil {
		return nil, err
	}
	id := receiptStringAt(receipt, "workflowRunId")
	entry, _ := receipt.obj("entries").vals[key].(*jsObject)
	if entry == nil {
		return nil, fmt.Errorf("Workflow receipt '%s' has no child key '%s'.", id, key)
	}
	res := entry.obj("resumability")
	if res.vals["state"] != "resumable" {
		return nil, fmt.Errorf("Workflow receipt '%s' child '%s' is not resumable: %s.", id, key, receiptStringAt(res, "reason"))
	}
	latest := receiptStringAt(entry, "latestRunId")
	if latest == "" {
		return nil, fmt.Errorf("Workflow receipt '%s' child '%s' has no retained run id.", id, key)
	}
	if assertResumable != nil {
		if err := assertResumable(latest); err != nil {
			return nil, err
		}
	}
	return entry, nil
}

// ResolveWorkflowReceiptResume is resolveWorkflowReceiptResume: the retained run id a keyed reference resumes.
func ResolveWorkflowReceiptResume(ref WorkflowReceiptResumeReference, asyncDirRoot string, assertResumable func(runID string) error) (string, error) {
	entry, err := ResolveWorkflowReceiptResumeEntry(ref, asyncDirRoot, assertResumable)
	if err != nil {
		return "", err
	}
	return receiptStringAt(entry, "latestRunId"), nil
}
