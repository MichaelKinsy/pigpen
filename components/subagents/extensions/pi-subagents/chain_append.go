package pi_subagents

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Port of src/runs/background/chain-append.ts: steps appended to a running async chain. A request is a file in the run's
// append-requests directory; the runner consumes it between steps. Runner steps and the run's status are ordered JSON
// (*jsObject), so a status keeps every field this code does not know.

const (
	appendRequestsDir             = "append-requests"
	maxStatusStepDescriptionChars = 160
	childSessionNameMaxChars      = 80
	taskExcerptMaxChars           = 60
)

// ChainAppendRequest is one queued append.
type ChainAppendRequest struct {
	ID        string
	CreatedAt float64
	Steps     []*jsObject
}

func (r ChainAppendRequest) object() *jsObject {
	steps := make([]any, len(r.Steps))
	for i, s := range r.Steps {
		steps[i] = s
	}
	return jo("id", r.ID, "createdAt", r.CreatedAt, "steps", steps)
}

// ChainAppendResult is what enqueueing reports; BookkeepingError names the follow-up writes that failed after the request was
// persisted (the append itself is accepted).
type ChainAppendResult struct {
	Request          ChainAppendRequest
	PendingCount     int
	BookkeepingError string
}

func appendDir(asyncDir string) string { return filepath.Join(asyncDir, appendRequestsDir) }

func appendRequestPath(asyncDir string, r ChainAppendRequest) string {
	return filepath.Join(appendDir(asyncDir), jsNumber(r.CreatedAt)+"-"+r.ID+".json")
}

func listAppendRequestFiles(asyncDir string) ([]string, error) {
	dir := appendDir(asyncDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// CountPendingChainAppendRequests is countPendingChainAppendRequests.
func CountPendingChainAppendRequests(asyncDir string) (int, error) {
	files, err := listAppendRequestFiles(asyncDir)
	return len(files), err
}

// RunnerStepOutputNames is runnerStepOutputNames: the output names a list of runner steps reserves.
func RunnerStepOutputNames(steps []*jsObject) []string {
	var names []string
	for _, step := range steps {
		switch {
		case isParallelGroup(step):
			for _, t := range step.vals["parallel"].([]any) {
				if to, ok := t.(*jsObject); ok {
					if n, ok := to.vals["outputName"].(string); ok && n != "" {
						names = append(names, n)
					}
				}
			}
		case isDynamicRunnerGroup(step):
			if as, ok := step.obj("collect").vals["as"].(string); ok && as != "" {
				names = append(names, as)
			}
		default:
			if n, ok := step.vals["outputName"].(string); ok && n != "" {
				names = append(names, n)
			}
		}
	}
	return names
}

func readStatusObject(asyncDir string) (*jsObject, error) {
	data, err := os.ReadFile(filepath.Join(asyncDir, "status.json"))
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if msg := workflowJSONError(string(data)); msg != "" {
		return nil, errors.New(msg)
	}
	v, err := parseJSON(data)
	if err != nil {
		return nil, err
	}
	o, _ := v.(*jsObject)
	return o, nil
}

// EnqueueChainAppendRequest is enqueueChainAppendRequest. admit, when set, persists the request on the caller's terms (a
// budget claim that commits the write); it must call persist exactly once.
func EnqueueChainAppendRequest(asyncDir, runID string, steps []*jsObject, now *int64, admit func(persist func() error) error) (ChainAppendResult, error) {
	status, err := readStatusObject(asyncDir)
	if err != nil {
		return ChainAppendResult{}, err
	}
	if status == nil {
		return ChainAppendResult{}, fmt.Errorf("No async run status found for '%s'.", runID)
	}
	if got := receiptStringAt(status, "runId"); got != runID {
		return ChainAppendResult{}, fmt.Errorf("Async run id mismatch: expected '%s', found '%s'.", runID, got)
	}
	mode, state := receiptStringAt(status, "mode"), receiptStringAt(status, "state")
	if mode != "chain" {
		return ChainAppendResult{}, fmt.Errorf("Run '%s' is %s; only active chain runs accept appended steps.", runID, mode)
	}
	if state != "running" {
		return ChainAppendResult{}, fmt.Errorf("Run '%s' is %s; only running chain runs accept appended steps.", runID, state)
	}
	inProgress := false
	if list, ok := status.vals["steps"].([]any); ok {
		for _, s := range list {
			if so, ok := s.(*jsObject); ok {
				st := receiptStringAt(so, "status")
				inProgress = inProgress || st == "running" || st == "pending"
			}
		}
	}
	if p, ok := status.num("pendingAppends"); ok && p > 0 {
		inProgress = true
	}
	if !inProgress {
		return ChainAppendResult{}, fmt.Errorf("Run '%s' has no running or pending chain steps left; append-step must target an in-progress chain.", runID)
	}
	if len(steps) == 0 {
		return ChainAppendResult{}, errors.New("append-step requires one chain step.")
	}
	created := float64(time.Now().UnixMilli())
	if now != nil {
		created = float64(*now)
	}
	request := ChainAppendRequest{ID: randomUUID(), CreatedAt: created, Steps: steps}
	if err := os.MkdirAll(appendDir(asyncDir), 0o755); err != nil {
		return ChainAppendResult{}, err
	}
	persist := func() error { return asyncWriteJSON(appendRequestPath(asyncDir, request), request.object()) }
	if admit != nil {
		err = admit(persist)
	} else {
		err = persist()
	}
	if err != nil {
		return ChainAppendResult{}, err
	}
	pending := 1
	var bookkeeping []string
	if n, err := CountPendingChainAppendRequests(asyncDir); err != nil {
		bookkeeping = append(bookkeeping, "pending append count failed: "+err.Error())
	} else {
		pending = n
	}
	updated := status.clone()
	updated.set("pendingAppends", float64(pending))
	updated.set("lastUpdate", request.CreatedAt)
	if err := asyncWriteJSON(filepath.Join(asyncDir, "status.json"), updated); err != nil {
		bookkeeping = append(bookkeeping, "status update failed: "+err.Error())
	}
	event := marshalJSON(jo("type", "subagent.chain.append.requested", "ts", request.CreatedAt, "runId", runID, "requestId", request.ID, "stepCount", float64(len(steps)), "pendingAppends", float64(pending)), "")
	if err := appendJSONL(filepath.Join(asyncDir, "events.jsonl"), event); err != nil {
		bookkeeping = append(bookkeeping, "event append failed: "+err.Error())
	}
	return ChainAppendResult{Request: request, PendingCount: pending, BookkeepingError: strings.Join(bookkeeping, "; ")}, nil
}

func readAppendRequest(filePath string) (*ChainAppendRequest, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	if msg := workflowJSONError(string(data)); msg != "" {
		return nil, errors.New(msg)
	}
	v, err := parseJSON(data)
	if err != nil {
		return nil, err
	}
	raw, ok := v.(*jsObject)
	if !ok {
		return nil, nil
	}
	id, _ := raw.vals["id"].(string)
	created, isNum := raw.vals["createdAt"].(float64)
	steps, isList := raw.vals["steps"].([]any)
	if id == "" || !isNum || math.IsNaN(created) || math.IsInf(created, 0) || !isList || len(steps) == 0 {
		return nil, nil
	}
	req := &ChainAppendRequest{ID: id, CreatedAt: created}
	for _, s := range steps {
		if so, ok := s.(*jsObject); ok {
			req.Steps = append(req.Steps, so)
		}
	}
	return req, nil
}

func sortAppendRequests(reqs []ChainAppendRequest) {
	sort.SliceStable(reqs, func(i, j int) bool {
		if reqs[i].CreatedAt != reqs[j].CreatedAt {
			return reqs[i].CreatedAt < reqs[j].CreatedAt
		}
		return localeCompare(reqs[i].ID, reqs[j].ID) < 0
	})
}

// ReadPendingChainAppendRequests is readPendingChainAppendRequests: the queue in order, without consuming it.
func ReadPendingChainAppendRequests(asyncDir string) ([]ChainAppendRequest, error) {
	files, err := listAppendRequestFiles(asyncDir)
	if err != nil {
		return nil, err
	}
	var out []ChainAppendRequest
	for _, f := range files {
		r, err := readAppendRequest(f)
		if err != nil {
			return nil, err
		}
		if r != nil {
			out = append(out, *r)
		}
	}
	sortAppendRequests(out)
	return out, nil
}

// ConsumeChainAppendRequests is consumeChainAppendRequests: the queue in order, each file removed so a request never runs twice.
func ConsumeChainAppendRequests(asyncDir string) ([]ChainAppendRequest, error) {
	files, err := listAppendRequestFiles(asyncDir)
	if err != nil {
		return nil, err
	}
	var out []ChainAppendRequest
	for _, f := range files {
		r, rerr := readAppendRequest(f)
		_ = os.Remove(f)
		if rerr != nil {
			return nil, rerr
		}
		if r != nil {
			out = append(out, *r)
		}
	}
	sortAppendRequests(out)
	return out, nil
}

// StatusStepDescription is statusStepDescription: the bounded one-line description persisted for a step; the task text itself
// is never persisted. "" when the task is blank.
func StatusStepDescription(task string) string {
	if jsTrim(task) == "" {
		return ""
	}
	d := promptRedacted
	if jsLength(d) > maxStatusStepDescriptionChars {
		return string(utf16Slice(d, maxStatusStepDescriptionChars-1)) + "…"
	}
	return d
}

func truncateDisplay(value string, maxLength int) string {
	if maxLength <= 0 {
		return ""
	}
	if jsLength(value) <= maxLength {
		return value
	}
	return string(utf16Slice(value, maxLength))
}

func previewDisplay(value string, maxLength int) string {
	n := wfSanitizeDisplayText(value)
	if jsLength(n) <= maxLength {
		return n
	}
	if maxLength <= 3 {
		return truncateDisplay(n, maxLength)
	}
	return truncateDisplay(n, maxLength-3) + "..."
}

// deriveChildSessionName is deriveChildSessionName (child-session-name.ts): the display name of a child session.
func deriveChildSessionName(agent, task, label string) string {
	agent, rawLabel, rawTask := jsTrim(agent), jsTrim(label), jsTrim(task)
	source := ""
	switch {
	case rawLabel != "" && rawLabel != promptRedacted:
		source = rawLabel
	case rawTask != "" && rawTask != promptRedacted:
		source = rawTask
	}
	excerpt := ""
	if source != "" {
		excerpt = previewDisplay(source, taskExcerptMaxChars)
	}
	base := agent
	switch {
	case agent != "" && excerpt != "":
		base = agent + ": " + excerpt
	case agent == "":
		base = excerpt
	}
	if base == "" {
		return ""
	}
	return previewDisplay(base, childSessionNameMaxChars)
}

func setDefined(o *jsObject, k string, v any) {
	if _, u := v.(undef); u || v == nil {
		return
	}
	o.set(k, v)
}

func statusStepForTask(task *jsObject) *jsObject {
	sessionName, _ := task.vals["sessionName"].(string)
	if sessionName == "" {
		sessionName = deriveChildSessionName(receiptStringAt(task, "agent"), receiptStringAt(task, "task"), receiptStringAt(task, "label"))
	}
	step := jo("agent", task.vals["agent"])
	if sessionName != "" {
		step.set("sessionName", sessionName)
	}
	if d := StatusStepDescription(receiptStringAt(task, "task")); d != "" {
		step.set("description", d)
	}
	if c := receiptStringAt(task, "context"); c != "" {
		step.set("context", c)
	}
	for _, k := range []string{"phase", "label", "outputName", "structured"} {
		setDefined(step, k, task.vals[k])
	}
	step.set("status", "pending")
	if f := receiptStringAt(task, "sessionFile"); f != "" {
		step.set("sessionFile", f)
	}
	for _, k := range []string{"skills", "model"} {
		setDefined(step, k, task.vals[k])
	}
	if v, ok := task.vals["contextLimit"]; ok {
		setDefined(step, "contextLimit", v)
	}
	setDefined(step, "thinking", task.vals["thinking"])
	setDefined(step, "requestedModel", task.vals["requestedModel"])
	step.set("recentTools", []any{})
	step.set("recentOutput", []any{})
	return step
}

func statusStepsForRunnerStep(step *jsObject) []*jsObject {
	if isParallelGroup(step) {
		var out []*jsObject
		for _, t := range step.vals["parallel"].([]any) {
			if to, ok := t.(*jsObject); ok {
				out = append(out, statusStepForTask(to))
			}
		}
		return out
	}
	if isDynamicRunnerGroup(step) {
		parallel, collect := step.obj("parallel"), step.obj("collect")
		as := jsCoerce(collect.vals["as"])
		s := jo("agent", "expand:"+jsCoerce(parallel.vals["agent"]))
		if c := receiptStringAt(parallel, "context"); c != "" {
			s.set("context", c)
		}
		phase := step.vals["phase"]
		if !jsTruthy(phase) {
			phase = parallel.vals["phase"]
		}
		setDefined(s, "phase", phase)
		label := receiptStringAt(step, "label")
		if label == "" {
			label = receiptStringAt(parallel, "label")
		}
		if label == "" {
			label = "Dynamic fanout (" + as + ")"
		}
		s.set("label", label)
		s.set("outputName", as)
		s.set("structured", jsTruthy(collect.vals["outputSchema"]))
		if v, ok := parallel.vals["contextLimit"]; ok {
			setDefined(s, "contextLimit", v)
		}
		s.set("status", "pending")
		s.set("recentTools", []any{})
		s.set("recentOutput", []any{})
		return []*jsObject{s}
	}
	return []*jsObject{statusStepForTask(step)}
}

func pushPhase(graph *jsObject, phase string, nodeID string) {
	if phase == "" {
		return
	}
	phases, _ := graph.vals["phases"].([]any)
	for _, p := range phases {
		if po, ok := p.(*jsObject); ok && po.vals["title"] == phase {
			ids, _ := po.vals["nodeIds"].([]any)
			po.set("nodeIds", append(ids, nodeID))
			return
		}
	}
	graph.set("phases", append(phases, jo("title", phase, "nodeIds", []any{nodeID})))
}

func labelOr(label, fallback1, fallback2 string) string {
	if l := jsTrim(label); l != "" {
		return l
	}
	if fallback1 != "" {
		return fallback1
	}
	return fallback2
}

func graphNodeForSequential(step *jsObject, stepIndex, flatIndex int) *jsObject {
	n := jo("id", "step-"+itoa(stepIndex), "kind", "step", "agent", step.vals["agent"])
	setDefined(n, "phase", step.vals["phase"])
	n.set("label", labelOr(receiptStringAt(step, "label"), receiptStringAt(step, "agent"), "Step "+itoa(stepIndex+1)))
	n.set("status", "pending")
	n.set("flatIndex", float64(flatIndex))
	n.set("stepIndex", float64(stepIndex))
	setDefined(n, "outputName", step.vals["outputName"])
	setDefined(n, "structured", step.vals["structured"])
	return n
}

func graphNodeForParallel(step *jsObject, stepIndex, flatIndex int, graph *jsObject) *jsObject {
	tasks := step.vals["parallel"].([]any)
	children := make([]any, 0, len(tasks))
	for taskIndex, t := range tasks {
		task, _ := t.(*jsObject)
		if task == nil {
			continue
		}
		childID := fmt.Sprintf("step-%d-agent-%d", stepIndex, taskIndex)
		pushPhase(graph, receiptStringAt(task, "phase"), childID)
		c := jo("id", childID, "kind", "agent", "agent", task.vals["agent"])
		setDefined(c, "phase", task.vals["phase"])
		c.set("label", labelOr(receiptStringAt(task, "label"), receiptStringAt(task, "agent"), "Agent "+itoa(taskIndex+1)))
		c.set("status", "pending")
		c.set("flatIndex", float64(flatIndex+taskIndex))
		c.set("stepIndex", float64(stepIndex))
		setDefined(c, "outputName", task.vals["outputName"])
		setDefined(c, "structured", task.vals["structured"])
		children = append(children, c)
	}
	label := "Parallel group (" + itoa(len(tasks)) + ")"
	if len(tasks) == 1 {
		label = "Parallel task"
	}
	return jo("id", "step-"+itoa(stepIndex), "kind", "parallel-group", "label", label, "status", "pending", "stepIndex", float64(stepIndex), "children", children)
}

func graphNodeForDynamic(step *jsObject, stepIndex int) *jsObject {
	collect, expand, parallel := step.obj("collect"), step.obj("expand"), step.obj("parallel")
	as := jsCoerce(collect.vals["as"])
	item := "item"
	if i, ok := lpGet(expand, "item"); ok && i != nil {
		item = jsCoerce(i)
	}
	dynamic := jo("sourceOutput", expand.obj("from").vals["output"], "sourcePath", expand.obj("from").vals["path"], "itemName", item)
	setDefined(dynamic, "maxItems", expand.vals["maxItems"])
	dynamic.set("collectAs", as)
	return jo("id", "step-"+itoa(stepIndex), "kind", "dynamic-parallel-group", "label", labelOr(receiptStringAt(step, "label"), jsTrim(receiptStringAt(parallel, "label")), "Dynamic fanout ("+as+")"),
		"status", "pending", "stepIndex", float64(stepIndex), "outputName", as, "structured", jsTruthy(collect.vals["outputSchema"]), "dynamic", dynamic, "children", []any{})
}

func appendWorkflowNode(graph *jsObject, step *jsObject, stepIndex, flatIndex int) {
	if graph == nil {
		return
	}
	nodes, _ := graph.vals["nodes"].([]any)
	switch {
	case isParallelGroup(step):
		graph.set("nodes", append(nodes, graphNodeForParallel(step, stepIndex, flatIndex, graph)))
	case isDynamicRunnerGroup(step):
		graph.set("nodes", append(nodes, graphNodeForDynamic(step, stepIndex)))
	default:
		node := graphNodeForSequential(step, stepIndex, flatIndex)
		graph.set("nodes", append(nodes, node))
		pushPhase(graph, receiptStringAt(step, "phase"), node.vals["id"].(string))
	}
}

// AppendRunnerStepsToStatus is appendRunnerStepsToStatus: the appended steps become pending status steps, parallel groups and
// workflow graph nodes of status, which is changed in place.
func AppendRunnerStepsToStatus(status *jsObject, steps []*jsObject, now *int64, pendingAppends int) (addedChainSteps, addedFlatSteps int) {
	graph, _ := status.vals["workflowGraph"].(*jsObject)
	for _, step := range steps {
		existing, _ := status.vals["steps"].([]any)
		stepIndex := len(existing)
		if c, ok := status.num("chainStepCount"); ok {
			stepIndex = int(c)
		}
		flatIndex := len(existing)
		statusSteps := statusStepsForRunnerStep(step)
		for _, s := range statusSteps {
			existing = append(existing, s)
		}
		status.set("steps", existing)
		switch {
		case isParallelGroup(step):
			groups, _ := status.vals["parallelGroups"].([]any)
			status.set("parallelGroups", append(groups, jo("start", float64(flatIndex), "count", float64(len(step.vals["parallel"].([]any))), "stepIndex", float64(stepIndex))))
		case isDynamicRunnerGroup(step):
			groups, _ := status.vals["parallelGroups"].([]any)
			status.set("parallelGroups", append(groups, jo("start", float64(flatIndex), "count", float64(1), "stepIndex", float64(stepIndex))))
		}
		appendWorkflowNode(graph, step, stepIndex, flatIndex)
		status.set("chainStepCount", float64(stepIndex+1))
		addedChainSteps++
		addedFlatSteps += len(statusSteps)
	}
	status.set("pendingAppends", float64(pendingAppends))
	last := float64(time.Now().UnixMilli())
	if now != nil {
		last = float64(*now)
	}
	status.set("lastUpdate", last)
	return
}
