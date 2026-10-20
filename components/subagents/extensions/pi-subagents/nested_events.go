package pi_subagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Port of src/runs/shared/nested-events.ts: the file protocol that lets a tree of nested subagent runs report to its root and be
// controlled from it. A root run owns a route (an event sink and a control inbox under one capability token); a nested run writes
// immutable event files into the sink, the root projects them into a registry, and a control request (interrupt, resume) is a
// file in the inbox answered by a result file in the sink.

const (
	nestedRouteFile     = "route.json"
	nestedRegistryFile  = "registry.json"
	nestedRouteIndexDir = ".route-index"
	maxNestedEventBytes = 64 * 1024
	maxNestedSteps      = 12
	maxNestedChildren   = 16
	maxNestedDepth      = 3
	maxNestedProcessed  = 1000
)

// nestedRoute is NestedRouteInfo: where the events of a nested run tree are written and where control requests for its runs are
// dropped, under one capability token.
type nestedRoute struct {
	RootRunID       string `json:"rootRunId"`
	EventSink       string `json:"eventSink"`
	ControlInbox    string `json:"controlInbox"`
	CapabilityToken string `json:"capabilityToken"`
}

func nestedEventsDir() string { return filepath.Join(tempRootDir(), "nested-subagent-events") }

func isSafeNestedIDValue(v any) bool { return isSafeNestedPathID(v) }

func assertSafeNestedID(label, value string) error {
	if !isSafeNestedIDValue(value) {
		return fmt.Errorf("%s must be a non-empty safe id token.", label)
	}
	return nil
}

func containedPath(base, candidate string) bool {
	b, _ := filepath.Abs(base)
	c, _ := filepath.Abs(candidate)
	return c == b || strings.HasPrefix(c, b+string(filepath.Separator))
}

func commonRouteRoot(r *nestedRoute) string {
	abs, _ := filepath.Abs(r.EventSink)
	return filepath.Dir(abs)
}

func nestedRouteIndexRoot() string {
	return filepath.Join(nestedEventsDir(), nestedRouteIndexDir, "roots")
}
func nestedRouteIndexDirFor(rootRunID string) string {
	return filepath.Join(nestedRouteIndexRoot(), jsEncodeURI(rootRunID))
}
func nestedRouteIndexPath(rootRunID, token string) string {
	return filepath.Join(nestedRouteIndexDirFor(rootRunID), jsEncodeURI(token)+".json")
}

// jsEncodeURI is encodeURIComponent (the safe ids this module takes have no lone surrogates).
func jsEncodeURI(s string) string {
	enc, _ := jsEncodeURIComponent(s)
	return enc
}

func validateNestedRouteShape(r *nestedRoute) error {
	if err := assertSafeNestedID("rootRunId", r.RootRunID); err != nil {
		return err
	}
	if err := assertSafeNestedID("capabilityToken", r.CapabilityToken); err != nil {
		return err
	}
	root := nestedEventsDir()
	if !containedPath(root, r.EventSink) {
		return errors.New("Nested event sink is outside the subagent nested event root.")
	}
	if !containedPath(root, r.ControlInbox) {
		return errors.New("Nested control inbox is outside the subagent nested event root.")
	}
	ci, _ := filepath.Abs(r.ControlInbox)
	if commonRouteRoot(r) != filepath.Dir(ci) {
		return errors.New("Nested event sink and control inbox must share one route root.")
	}
	return nil
}

// createNestedRoute makes the route of a root run: its directories (0700), its index entry and its route file.
func createNestedRoute(rootRunID string) (*nestedRoute, error) {
	if err := assertSafeNestedID("rootRunId", rootRunID); err != nil {
		return nil, err
	}
	token := randomUUID()
	routeRoot := filepath.Join(nestedEventsDir(), rootRunID+"-"+token)
	r := &nestedRoute{RootRunID: rootRunID, EventSink: filepath.Join(routeRoot, "events"), ControlInbox: filepath.Join(routeRoot, "controls"), CapabilityToken: token}
	for _, d := range []string{r.EventSink, r.ControlInbox, nestedRouteIndexDirFor(rootRunID)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	created := nowMs()
	if err := asyncWriteJSON(nestedRouteIndexPath(rootRunID, token), map[string]any{"rootRunId": rootRunID, "capabilityToken": token, "routeRoot": filepath.Base(routeRoot), "createdAt": created}); err != nil {
		return nil, err
	}
	if err := asyncWriteJSON(filepath.Join(routeRoot, nestedRouteFile), map[string]any{"rootRunId": rootRunID, "capabilityToken": token, "createdAt": created}); err != nil {
		return nil, err
	}
	return r, nil
}

// resolveNestedRoute validates a route handed to a child against its on-disk metadata.
func resolveNestedRoute(r *nestedRoute) (*nestedRoute, error) {
	if err := validateNestedRouteShape(r); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(commonRouteRoot(r), nestedRouteFile))
	if err != nil {
		return nil, err
	}
	var meta struct {
		RootRunID       any `json:"rootRunId"`
		CapabilityToken any `json:"capabilityToken"`
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		return nil, err
	}
	if meta.RootRunID != r.RootRunID || meta.CapabilityToken != r.CapabilityToken {
		return nil, errors.New("Nested event route metadata does not match the provided root id and capability token.")
	}
	return r, nil
}

// resolveInheritedNestedRoute is resolveNestedRoute for a route a child inherited: an invalid one is reported and ignored.
func resolveInheritedNestedRoute(r *nestedRoute) *nestedRoute {
	resolved, err := resolveNestedRoute(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ignoring invalid nested subagent event route:", err)
		return nil
	}
	return resolved
}

// nestedParentAddress is where a child stands in the run tree.
type nestedParentAddress struct {
	ParentRunID   string
	ParentStepIdx *int
	Depth         int
	Path          []nestedPathEntry
}

// resolveNestedAsyncDir is the run directory a nested summary names, only when it lies in the nested run's own directory.
func resolveNestedAsyncDir(rootRunID string, run *nestedRunSummary) string {
	if run.AsyncDir == "" {
		return ""
	}
	resolved, _ := filepath.Abs(run.AsyncDir)
	nested, _ := filepath.Abs(filepath.Join(tempRootDir(), "nested-subagent-runs", rootRunID, run.ID))
	rel, err := filepath.Rel(nested, resolved)
	if resolved == nested || (err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)) {
		return resolved
	}
	return ""
}

// ---- summaries ----

// nestedStepSummary is NestedStepSummary (the fields a nested run reports for one step).
type nestedStepSummary struct {
	Agent                         string              `json:"agent"`
	SessionName                   string              `json:"sessionName,omitempty"`
	Status                        string              `json:"status"`
	Model                         string              `json:"model,omitempty"`
	Thinking                      string              `json:"thinking,omitempty"`
	SessionFile                   string              `json:"sessionFile,omitempty"`
	ActivityState                 string              `json:"activityState,omitempty"`
	LastActivityAt                *float64            `json:"lastActivityAt,omitempty"`
	CurrentTool                   string              `json:"currentTool,omitempty"`
	CurrentToolStartedAt          *float64            `json:"currentToolStartedAt,omitempty"`
	CurrentPath                   string              `json:"currentPath,omitempty"`
	TurnCount                     *float64            `json:"turnCount,omitempty"`
	ToolCount                     *float64            `json:"toolCount,omitempty"`
	StartedAt                     *float64            `json:"startedAt,omitempty"`
	EndedAt                       *float64            `json:"endedAt,omitempty"`
	Error                         string              `json:"error,omitempty"`
	TimedOut                      bool                `json:"timedOut,omitempty"`
	Stopped                       bool                `json:"stopped,omitempty"`
	TurnBudget                    json.RawMessage     `json:"turnBudget,omitempty"`
	TurnBudgetExceeded            bool                `json:"turnBudgetExceeded,omitempty"`
	WrapUpRequested               bool                `json:"wrapUpRequested,omitempty"`
	LaunchResolvedExtensions      json.RawMessage     `json:"launchResolvedExtensions,omitempty"`
	RuntimeAcknowledgedExtensions json.RawMessage     `json:"runtimeAcknowledgedExtensions,omitempty"`
	Children                      []*nestedRunSummary `json:"children,omitempty"`
}

// nestedRunSummary is NestedRunSummary.
type nestedRunSummary struct {
	ID                            string              `json:"id"`
	ParentRunID                   string              `json:"parentRunId"`
	ParentStepIndex               *float64            `json:"parentStepIndex,omitempty"`
	ParentAgent                   string              `json:"parentAgent,omitempty"`
	Depth                         int                 `json:"depth"`
	Path                          []nestedPathEntry   `json:"path"`
	State                         string              `json:"state"`
	SessionName                   string              `json:"sessionName,omitempty"`
	Model                         string              `json:"model,omitempty"`
	Thinking                      string              `json:"thinking,omitempty"`
	AsyncDir                      string              `json:"asyncDir,omitempty"`
	PID                           *float64            `json:"pid,omitempty"`
	SessionID                     string              `json:"sessionId,omitempty"`
	SessionFile                   string              `json:"sessionFile,omitempty"`
	IntercomTarget                string              `json:"intercomTarget,omitempty"`
	OwnerIntercomTarget           string              `json:"ownerIntercomTarget,omitempty"`
	LeafIntercomTarget            string              `json:"leafIntercomTarget,omitempty"`
	OwnerState                    string              `json:"ownerState,omitempty"`
	ControlInbox                  string              `json:"controlInbox,omitempty"`
	CapabilityToken               string              `json:"capabilityToken,omitempty"`
	Mode                          string              `json:"mode,omitempty"`
	Agent                         string              `json:"agent,omitempty"`
	Agents                        []string            `json:"agents,omitempty"`
	CurrentStep                   *float64            `json:"currentStep,omitempty"`
	ChainStepCount                *float64            `json:"chainStepCount,omitempty"`
	ParallelGroups                json.RawMessage     `json:"parallelGroups,omitempty"`
	ActivityState                 string              `json:"activityState,omitempty"`
	LastActivityAt                *float64            `json:"lastActivityAt,omitempty"`
	CurrentTool                   string              `json:"currentTool,omitempty"`
	CurrentToolStartedAt          *float64            `json:"currentToolStartedAt,omitempty"`
	CurrentPath                   string              `json:"currentPath,omitempty"`
	TurnCount                     *float64            `json:"turnCount,omitempty"`
	ToolCount                     *float64            `json:"toolCount,omitempty"`
	TotalTokens                   *nestedTokenUsage   `json:"totalTokens,omitempty"`
	TotalCost                     *nestedCost         `json:"totalCost,omitempty"`
	StartedAt                     *float64            `json:"startedAt,omitempty"`
	EndedAt                       *float64            `json:"endedAt,omitempty"`
	LastUpdate                    *float64            `json:"lastUpdate,omitempty"`
	TimeoutMs                     *float64            `json:"timeoutMs,omitempty"`
	DeadlineAt                    *float64            `json:"deadlineAt,omitempty"`
	TimedOut                      bool                `json:"timedOut,omitempty"`
	Stopped                       bool                `json:"stopped,omitempty"`
	TurnBudget                    json.RawMessage     `json:"turnBudget,omitempty"`
	TurnBudgetExceeded            bool                `json:"turnBudgetExceeded,omitempty"`
	WrapUpRequested               bool                `json:"wrapUpRequested,omitempty"`
	Error                         string              `json:"error,omitempty"`
	LaunchResolvedExtensions      json.RawMessage     `json:"launchResolvedExtensions,omitempty"`
	RuntimeAcknowledgedExtensions json.RawMessage     `json:"runtimeAcknowledgedExtensions,omitempty"`
	Steps                         []nestedStepSummary `json:"steps,omitempty"`
	Children                      []*nestedRunSummary `json:"children,omitempty"`
}

type nestedTokenUsage struct {
	Input      float64  `json:"input"`
	Output     float64  `json:"output"`
	Total      float64  `json:"total"`
	Window     *float64 `json:"window,omitempty"`
	WindowPeak *float64 `json:"windowPeak,omitempty"`
}

type nestedCost struct {
	InputTokens  float64 `json:"inputTokens"`
	OutputTokens float64 `json:"outputTokens"`
	CostUSD      float64 `json:"costUsd"`
}

func numPtr(v any) *float64 {
	if f, ok := finiteNum(v); ok {
		return &f
	}
	return nil
}

// strVal is stringValue(value, max): a non-empty string cut to max UTF-16 units.
func strVal(v any, max int) string {
	s, ok := v.(string)
	if !ok || s == "" {
		return ""
	}
	return jsSlice(s, max)
}

func objectOf(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

var (
	sha16Pattern  = regexp.MustCompile(`^sha256:[a-f0-9]{16}$`)
	requiredExtRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

func sanitizeTokenUsage(v any) *nestedTokenUsage {
	m := objectOf(v)
	if m == nil {
		return nil
	}
	in, ok1 := finiteNum(m["input"])
	out, ok2 := finiteNum(m["output"])
	total, ok3 := finiteNum(m["total"])
	if !ok1 || !ok2 || !ok3 {
		return nil
	}
	return &nestedTokenUsage{Input: in, Output: out, Total: total, Window: numPtr(m["window"]), WindowPeak: numPtr(m["windowPeak"])}
}

func sanitizeCost(v any) *nestedCost {
	m := objectOf(v)
	if m == nil {
		return nil
	}
	in, ok1 := finiteNum(m["inputTokens"])
	out, ok2 := finiteNum(m["outputTokens"])
	cost, ok3 := finiteNum(m["costUsd"])
	if !ok1 || !ok2 || !ok3 {
		return nil
	}
	return &nestedCost{InputTokens: in, OutputTokens: out, CostUSD: cost}
}

func sanitizeLaunchResolvedExtensions(v any) json.RawMessage {
	m := objectOf(v)
	if m == nil || m["version"] != float64(1) || m["source"] != "launch-resolved" {
		return nil
	}
	disable, ok := m["disableAmbientExtensions"].(bool)
	if !ok {
		return nil
	}
	list := func(in any) []string {
		out := []string{}
		if arr, ok := in.([]any); ok {
			for _, it := range arr {
				if s, ok := it.(string); ok && sha16Pattern.MatchString(s) && len(out) < 32 {
					out = append(out, s)
				}
			}
		}
		return out
	}
	required := []string{}
	if arr, ok := m["required"].([]any); ok {
		for _, it := range arr {
			if s, ok := it.(string); ok && requiredExtRe.MatchString(s) && len(required) < 32 {
				required = append(required, s)
			}
		}
	}
	omitted := objectOf(m["omitted"])
	count := func(k string) float64 {
		f, _ := finiteNum(omitted[k])
		return math.Max(0, math.Floor(f))
	}
	b, _ := json.Marshal(map[string]any{"version": 1, "source": "launch-resolved", "disableAmbientExtensions": disable, "runtime": list(m["runtime"]),
		"configured": list(m["configured"]), "required": required, "effective": list(m["effective"]),
		"omitted": map[string]any{"runtime": count("runtime"), "configured": count("configured"), "required": count("required"), "effective": count("effective")}})
	return b
}

func sanitizeNestedRuntimeAck(v any) json.RawMessage {
	m := objectOf(v)
	if m == nil || m["version"] != float64(1) || m["source"] != "child-runtime" {
		return nil
	}
	arr, ok := m["ids"].([]any)
	if !ok {
		return nil
	}
	var ids []string
	seen := map[string]bool{}
	for _, it := range arr {
		s, ok := it.(string)
		if !ok || s == "" || jsLength(s) > 128 || !runtimeAckIDRe.MatchString(s) || strings.Contains(s, "..") || strings.ContainsAny(s, `/\`) || seen[s] {
			continue
		}
		seen[s] = true
		ids = append(ids, s)
	}
	if len(ids) == 0 {
		return nil
	}
	extra, _ := finiteNum(m["omitted"])
	omitted := math.Max(0, float64(len(ids)-32)) + math.Max(0, math.Floor(extra))
	if len(ids) > 32 {
		ids = ids[:32]
	}
	b, _ := json.Marshal(map[string]any{"version": 1, "source": "child-runtime", "ids": ids, "omitted": omitted})
	return b
}

func sanitizeTurnBudget(v any) json.RawMessage {
	m := objectOf(v)
	if m == nil {
		return nil
	}
	maxTurns, ok1 := finiteNum(m["maxTurns"])
	grace, ok2 := finiteNum(m["graceTurns"])
	count, ok3 := finiteNum(m["turnCount"])
	outcome, _ := m["outcome"].(string)
	switch outcome {
	case "within-budget", "wrap-up-requested", "termination-deferred", "exceeded":
	default:
		return nil
	}
	if !ok1 || !ok2 || !ok3 {
		return nil
	}
	out := map[string]any{"maxTurns": maxTurns, "graceTurns": grace, "turnCount": count, "outcome": outcome}
	for _, k := range []string{"wrapUpRequestedAtTurn", "terminationDeferredAtTurn", "exceededAtTurn"} {
		if f, ok := finiteNum(m[k]); ok {
			out[k] = f
		}
	}
	b, _ := json.Marshal(out)
	return b
}

func sanitizeNestedState(v any, fallback string) string {
	switch s, _ := v.(string); s {
	case "queued", "running", "complete", "failed", "partial", "paused", "stopped", "rejected":
		return s
	}
	return fallback
}

func thinkingLevelOf(v any) string {
	s, _ := v.(string)
	for _, l := range thinkingLevelList {
		if l == s {
			return s
		}
	}
	return ""
}

func activityStateOf(v any) string {
	if s, _ := v.(string); s == "active_long_running" || s == "needs_attention" {
		return s
	}
	return ""
}

func sanitizeNestedStep(input any, depth int) *nestedStepSummary {
	raw := objectOf(input)
	if raw == nil {
		return nil
	}
	agent := strVal(raw["agent"], 128)
	if agent == "" {
		return nil
	}
	status := "pending"
	switch s, _ := raw["status"].(string); s {
	case "pending", "running", "complete", "completed", "failed", "partial", "paused", "stopped", "rejected":
		status = s
	}
	st := &nestedStepSummary{Agent: agent, Status: status, Model: strVal(raw["model"], 512), Thinking: thinkingLevelOf(raw["thinking"]), SessionName: strVal(raw["sessionName"], 256),
		SessionFile: strVal(raw["sessionFile"], 2048), ActivityState: activityStateOf(raw["activityState"]), LastActivityAt: numPtr(raw["lastActivityAt"]),
		CurrentTool: strVal(raw["currentTool"], 128), CurrentToolStartedAt: numPtr(raw["currentToolStartedAt"]), CurrentPath: strVal(raw["currentPath"], 2048),
		TurnCount: numPtr(raw["turnCount"]), ToolCount: numPtr(raw["toolCount"]), StartedAt: numPtr(raw["startedAt"]), EndedAt: numPtr(raw["endedAt"]), Error: strVal(raw["error"], 1024),
		TimedOut: raw["timedOut"] == true, Stopped: raw["stopped"] == true, TurnBudget: sanitizeTurnBudget(raw["turnBudget"]), TurnBudgetExceeded: raw["turnBudgetExceeded"] == true,
		WrapUpRequested: raw["wrapUpRequested"] == true, LaunchResolvedExtensions: sanitizeLaunchResolvedExtensions(raw["launchResolvedExtensions"]),
		RuntimeAcknowledgedExtensions: sanitizeNestedRuntimeAck(raw["runtimeAcknowledgedExtensions"])}
	if arr, ok := raw["children"].([]any); ok && depth < maxNestedDepth {
		st.Children = []*nestedRunSummary{}
		for _, c := range arr {
			if s := sanitizeNestedSummary(c, depth+1); s != nil && len(st.Children) < maxNestedChildren {
				st.Children = append(st.Children, s)
			}
		}
	}
	return st
}

func sanitizeNestedSummary(input any, depth int) *nestedRunSummary {
	raw := objectOf(input)
	if raw == nil || !isSafeNestedIDValue(raw["id"]) || !isSafeNestedIDValue(raw["parentRunId"]) {
		return nil
	}
	s := &nestedRunSummary{ID: raw["id"].(string), ParentRunID: raw["parentRunId"].(string), ParentStepIndex: numPtr(raw["parentStepIndex"]), ParentAgent: strVal(raw["parentAgent"], 128)}
	d, _ := finiteNum(raw["depth"])
	s.Depth = int(math.Min(math.Max(0, d), maxNestedDepth))
	s.Path = sanitizeNestedPath(raw["path"])
	if s.Path == nil {
		s.Path = []nestedPathEntry{}
	}
	s.State = sanitizeNestedState(raw["state"], "running")
	s.SessionName = strVal(raw["sessionName"], 256)
	s.Model = strVal(raw["model"], 512)
	s.Thinking = thinkingLevelOf(raw["thinking"])
	s.AsyncDir = strVal(raw["asyncDir"], 2048)
	if pid, ok := finiteNum(raw["pid"]); ok && pid > 0 && pid == math.Trunc(pid) {
		s.PID = &pid
	}
	s.SessionID = strVal(raw["sessionId"], 256)
	s.SessionFile = strVal(raw["sessionFile"], 2048)
	s.IntercomTarget = strVal(raw["intercomTarget"], 256)
	s.OwnerIntercomTarget = strVal(raw["ownerIntercomTarget"], 256)
	s.LeafIntercomTarget = strVal(raw["leafIntercomTarget"], 256)
	if o, _ := raw["ownerState"].(string); o == "live" || o == "gone" || o == "unknown" {
		s.OwnerState = o
	}
	s.ControlInbox = strVal(raw["controlInbox"], 2048)
	s.CapabilityToken = strVal(raw["capabilityToken"], 128)
	if m, _ := raw["mode"].(string); m == "single" || m == "parallel" || m == "chain" {
		s.Mode = m
	}
	s.Agent = strVal(raw["agent"], 128)
	if arr, ok := raw["agents"].([]any); ok {
		s.Agents = []string{}
		for _, a := range arr {
			if n := strVal(a, 128); n != "" && len(s.Agents) < maxNestedSteps {
				s.Agents = append(s.Agents, n)
			}
		}
	}
	s.CurrentStep, s.ChainStepCount = numPtr(raw["currentStep"]), numPtr(raw["chainStepCount"])
	s.ActivityState = activityStateOf(raw["activityState"])
	s.LastActivityAt, s.CurrentToolStartedAt = numPtr(raw["lastActivityAt"]), numPtr(raw["currentToolStartedAt"])
	s.CurrentTool, s.CurrentPath = strVal(raw["currentTool"], 128), strVal(raw["currentPath"], 2048)
	s.TurnCount, s.ToolCount = numPtr(raw["turnCount"]), numPtr(raw["toolCount"])
	s.TotalTokens, s.TotalCost = sanitizeTokenUsage(raw["totalTokens"]), sanitizeCost(raw["totalCost"])
	s.StartedAt, s.EndedAt, s.LastUpdate = numPtr(raw["startedAt"]), numPtr(raw["endedAt"]), numPtr(raw["lastUpdate"])
	s.TimeoutMs, s.DeadlineAt = numPtr(raw["timeoutMs"]), numPtr(raw["deadlineAt"])
	s.TimedOut, s.Stopped = raw["timedOut"] == true, raw["stopped"] == true
	s.TurnBudget = sanitizeTurnBudget(raw["turnBudget"])
	s.TurnBudgetExceeded, s.WrapUpRequested = raw["turnBudgetExceeded"] == true, raw["wrapUpRequested"] == true
	s.Error = strVal(raw["error"], 1024)
	s.LaunchResolvedExtensions = sanitizeLaunchResolvedExtensions(raw["launchResolvedExtensions"])
	s.RuntimeAcknowledgedExtensions = sanitizeNestedRuntimeAck(raw["runtimeAcknowledgedExtensions"])
	if arr, ok := raw["steps"].([]any); ok {
		for _, st := range arr {
			if step := sanitizeNestedStep(st, depth+1); step != nil && len(s.Steps) < maxNestedSteps {
				s.Steps = append(s.Steps, *step)
			}
		}
	}
	if arr, ok := raw["children"].([]any); ok && depth < maxNestedDepth {
		s.Children = []*nestedRunSummary{}
		for _, c := range arr {
			if ch := sanitizeNestedSummary(c, depth+1); ch != nil && len(s.Children) < maxNestedChildren {
				s.Children = append(s.Children, ch)
			}
		}
	}
	return s
}

// ---- event records ----

// nestedEventRecord is NestedEventRecord.
type nestedEventRecord struct {
	Type            string            `json:"type"`
	TS              float64           `json:"ts"`
	RootRunID       string            `json:"rootRunId"`
	ParentRunID     string            `json:"parentRunId"`
	ParentStepIndex *float64          `json:"parentStepIndex,omitempty"`
	CapabilityToken string            `json:"capabilityToken"`
	Child           *nestedRunSummary `json:"child"`
}

// nestedControlResult is NestedControlResultRecord.
type nestedControlResult struct {
	Type            string  `json:"type"`
	TS              float64 `json:"ts"`
	RootRunID       string  `json:"rootRunId"`
	CapabilityToken string  `json:"capabilityToken"`
	RequestID       string  `json:"requestId"`
	TargetRunID     string  `json:"targetRunId"`
	OK              bool    `json:"ok"`
	Message         string  `json:"message"`
}

// nestedControlRequest is NestedControlRequestRecord (FilePath is set by readNestedControlRequests).
type nestedControlRequest struct {
	Type            string  `json:"type"`
	TS              float64 `json:"ts"`
	RootRunID       string  `json:"rootRunId"`
	CapabilityToken string  `json:"capabilityToken"`
	RequestID       string  `json:"requestId"`
	TargetRunID     string  `json:"targetRunId"`
	Action          string  `json:"action"`
	Message         string  `json:"message,omitempty"`
	FilePath        string  `json:"-"`
}

// nestedRegistry is NestedRegistry: the projection of a route's events.
type nestedRegistry struct {
	RootRunID       string              `json:"rootRunId"`
	UpdatedAt       float64             `json:"updatedAt"`
	Children        []*nestedRunSummary `json:"children"`
	ProcessedEvents []string            `json:"processedEvents"`
}

func decodeJSONObject(content string) map[string]any {
	if len(content) > maxNestedEventBytes {
		return nil
	}
	var v any
	if json.Unmarshal([]byte(content), &v) != nil {
		return nil
	}
	return objectOf(v)
}

func parseNestedRecord(content string, route *nestedRoute) *nestedEventRecord {
	raw := decodeJSONObject(content)
	if raw == nil {
		return nil
	}
	typ, _ := raw["type"].(string)
	if typ != "subagent.nested.started" && typ != "subagent.nested.updated" && typ != "subagent.nested.completed" {
		return nil
	}
	if raw["rootRunId"] != route.RootRunID || raw["capabilityToken"] != route.CapabilityToken || !isSafeNestedIDValue(raw["parentRunId"]) {
		return nil
	}
	ts, ok := finiteNum(raw["ts"])
	if !ok {
		return nil
	}
	child := sanitizeNestedSummary(raw["child"], 0)
	if child == nil || child.ID == route.RootRunID {
		return nil
	}
	child.ControlInbox, child.CapabilityToken = route.ControlInbox, route.CapabilityToken
	if child.OwnerState == "" {
		child.OwnerState = "unknown"
	}
	return &nestedEventRecord{Type: typ, TS: ts, RootRunID: route.RootRunID, ParentRunID: raw["parentRunId"].(string), ParentStepIndex: numPtr(raw["parentStepIndex"]),
		CapabilityToken: route.CapabilityToken, Child: child}
}

func parseNestedEventRecords(content string, route *nestedRoute) []*nestedEventRecord {
	if !strings.Contains(content, "\n") {
		if r := parseNestedRecord(strings.TrimSpace(content), route); r != nil {
			return []*nestedEventRecord{r}
		}
		return nil
	}
	lines := strings.Split(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	var out []*nestedEventRecord
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if r := parseNestedRecord(line, route); r != nil {
			out = append(out, r)
		}
	}
	return out
}

func nestedTerminal(state string) bool {
	switch state {
	case "complete", "failed", "partial", "paused", "rejected", "stopped":
		return true
	}
	return false
}

func mergeBoundedChildren(existing, incoming []*nestedRunSummary, hasIncoming bool) []*nestedRunSummary {
	cut := func(in []*nestedRunSummary) []*nestedRunSummary {
		if len(in) > maxNestedChildren {
			return in[:maxNestedChildren]
		}
		return in
	}
	if !hasIncoming {
		return cut(existing)
	}
	byID := map[string]*nestedRunSummary{}
	for _, c := range incoming {
		byID[c.ID] = c
	}
	merged := make([]*nestedRunSummary, 0, len(existing)+len(incoming))
	known := map[string]bool{}
	for _, c := range existing {
		known[c.ID] = true
		if in := byID[c.ID]; in != nil {
			merged = append(merged, in)
		} else {
			merged = append(merged, c)
		}
	}
	for _, c := range incoming {
		if !known[c.ID] {
			merged = append(merged, c)
		}
	}
	return cut(merged)
}

func mergeNestedStep(existing *nestedStepSummary, incoming nestedStepSummary) nestedStepSummary {
	if existing == nil || existing.Agent != incoming.Agent {
		out := incoming
		out.Children = nil
		if incoming.Children != nil {
			out.Children = mergeBoundedChildren(nil, incoming.Children, true)
		}
		return out
	}
	out := *existing
	// the live fields come from the incoming report, the rest of the metadata stays
	out.Status, out.ActivityState, out.LastActivityAt, out.CurrentTool, out.CurrentToolStartedAt, out.CurrentPath, out.TurnCount, out.ToolCount, out.Children =
		incoming.Status, "", nil, "", nil, "", nil, nil, nil
	overlayNestedStep(&out, incoming)
	out.Children = mergeBoundedChildren(existing.Children, incoming.Children, incoming.Children != nil)
	return out
}

// overlayNestedStep is `{ ...metadata, ...incoming }`: every field incoming sets replaces the metadata's.
func overlayNestedStep(dst *nestedStepSummary, in nestedStepSummary) {
	setS := func(d *string, s string) {
		if s != "" {
			*d = s
		}
	}
	setP := func(d **float64, s *float64) {
		if s != nil {
			*d = s
		}
	}
	setS(&dst.Agent, in.Agent)
	setS(&dst.SessionName, in.SessionName)
	setS(&dst.Status, in.Status)
	setS(&dst.Model, in.Model)
	setS(&dst.Thinking, in.Thinking)
	setS(&dst.SessionFile, in.SessionFile)
	setS(&dst.ActivityState, in.ActivityState)
	setP(&dst.LastActivityAt, in.LastActivityAt)
	setS(&dst.CurrentTool, in.CurrentTool)
	setP(&dst.CurrentToolStartedAt, in.CurrentToolStartedAt)
	setS(&dst.CurrentPath, in.CurrentPath)
	setP(&dst.TurnCount, in.TurnCount)
	setP(&dst.ToolCount, in.ToolCount)
	setP(&dst.StartedAt, in.StartedAt)
	setP(&dst.EndedAt, in.EndedAt)
	setS(&dst.Error, in.Error)
	dst.TimedOut = dst.TimedOut || in.TimedOut
	dst.Stopped = dst.Stopped || in.Stopped
	if in.TurnBudget != nil {
		dst.TurnBudget = in.TurnBudget
	}
	dst.TurnBudgetExceeded = dst.TurnBudgetExceeded || in.TurnBudgetExceeded
	dst.WrapUpRequested = dst.WrapUpRequested || in.WrapUpRequested
	if in.LaunchResolvedExtensions != nil {
		dst.LaunchResolvedExtensions = in.LaunchResolvedExtensions
	}
	if in.RuntimeAcknowledgedExtensions != nil {
		dst.RuntimeAcknowledgedExtensions = in.RuntimeAcknowledgedExtensions
	}
}

// overlayNestedSummary is `{ ...existing, ...incoming }` for the fields incoming carries.
func overlayNestedSummary(dst, in *nestedRunSummary) {
	setS := func(d *string, s string) {
		if s != "" {
			*d = s
		}
	}
	setP := func(d **float64, s *float64) {
		if s != nil {
			*d = s
		}
	}
	setS(&dst.ParentAgent, in.ParentAgent)
	setP(&dst.ParentStepIndex, in.ParentStepIndex)
	dst.Depth, dst.Path, dst.State = in.Depth, in.Path, in.State
	setS(&dst.SessionName, in.SessionName)
	setS(&dst.Model, in.Model)
	setS(&dst.Thinking, in.Thinking)
	setS(&dst.AsyncDir, in.AsyncDir)
	setP(&dst.PID, in.PID)
	setS(&dst.SessionID, in.SessionID)
	setS(&dst.SessionFile, in.SessionFile)
	setS(&dst.IntercomTarget, in.IntercomTarget)
	setS(&dst.OwnerIntercomTarget, in.OwnerIntercomTarget)
	setS(&dst.LeafIntercomTarget, in.LeafIntercomTarget)
	setS(&dst.OwnerState, in.OwnerState)
	setS(&dst.ControlInbox, in.ControlInbox)
	setS(&dst.CapabilityToken, in.CapabilityToken)
	setS(&dst.Mode, in.Mode)
	setS(&dst.Agent, in.Agent)
	if in.Agents != nil {
		dst.Agents = in.Agents
	}
	setP(&dst.CurrentStep, in.CurrentStep)
	setP(&dst.ChainStepCount, in.ChainStepCount)
	setS(&dst.ActivityState, in.ActivityState)
	setP(&dst.LastActivityAt, in.LastActivityAt)
	setS(&dst.CurrentTool, in.CurrentTool)
	setP(&dst.CurrentToolStartedAt, in.CurrentToolStartedAt)
	setS(&dst.CurrentPath, in.CurrentPath)
	setP(&dst.TurnCount, in.TurnCount)
	setP(&dst.ToolCount, in.ToolCount)
	if in.TotalTokens != nil {
		dst.TotalTokens = in.TotalTokens
	}
	if in.TotalCost != nil {
		dst.TotalCost = in.TotalCost
	}
	setP(&dst.StartedAt, in.StartedAt)
	setP(&dst.EndedAt, in.EndedAt)
	setP(&dst.LastUpdate, in.LastUpdate)
	setP(&dst.TimeoutMs, in.TimeoutMs)
	setP(&dst.DeadlineAt, in.DeadlineAt)
	dst.TimedOut = dst.TimedOut || in.TimedOut
	dst.Stopped = dst.Stopped || in.Stopped
	if in.TurnBudget != nil {
		dst.TurnBudget = in.TurnBudget
	}
	dst.TurnBudgetExceeded = dst.TurnBudgetExceeded || in.TurnBudgetExceeded
	dst.WrapUpRequested = dst.WrapUpRequested || in.WrapUpRequested
	setS(&dst.Error, in.Error)
	if in.LaunchResolvedExtensions != nil {
		dst.LaunchResolvedExtensions = in.LaunchResolvedExtensions
	}
	if in.RuntimeAcknowledgedExtensions != nil {
		dst.RuntimeAcknowledgedExtensions = in.RuntimeAcknowledgedExtensions
	}
}

func copySummary(s *nestedRunSummary) *nestedRunSummary {
	c := *s
	return &c
}

func mergeNestedSummary(existing *nestedRunSummary, ev *nestedEventRecord) *nestedRunSummary {
	incomingState := ev.Child.State
	if ev.Type == "subagent.nested.completed" && ev.Child.State == "running" {
		incomingState = "complete"
	}
	incoming := copySummary(ev.Child)
	incoming.State = incomingState
	if incoming.LastUpdate == nil {
		incoming.LastUpdate = &ev.TS
	}
	if existing == nil {
		return incoming
	}
	existingUpdate := 0.0
	if existing.LastUpdate != nil {
		existingUpdate = *existing.LastUpdate
	}
	incomingUpdate := *incoming.LastUpdate
	if incomingUpdate < existingUpdate {
		return existing
	}
	if nestedTerminal(existing.State) && !nestedTerminal(incoming.State) {
		return existing
	}
	if nestedTerminal(existing.State) && nestedTerminal(incoming.State) && incomingUpdate == existingUpdate {
		return existing
	}
	out := copySummary(existing)
	overlayNestedSummary(out, incoming)
	if incoming.Steps != nil {
		n := min(maxNestedSteps, max(len(existing.Steps), len(incoming.Steps)))
		var steps []nestedStepSummary
		for i := range n {
			switch {
			case i < len(incoming.Steps):
				var prior *nestedStepSummary
				if i < len(existing.Steps) {
					prior = &existing.Steps[i]
				}
				steps = append(steps, mergeNestedStep(prior, incoming.Steps[i]))
			case i < len(existing.Steps):
				steps = append(steps, existing.Steps[i])
			}
		}
		out.Steps = steps
	}
	out.Children = mergeBoundedChildren(existing.Children, incoming.Children, incoming.Children != nil)
	out.State = incoming.State
	last := math.Max(existingUpdate, incomingUpdate)
	out.LastUpdate = &last
	return out
}

func attachNestedChild(children []*nestedRunSummary, ev *nestedEventRecord) []*nestedRunSummary {
	updated := false
	var walk func(items []*nestedRunSummary) []*nestedRunSummary
	walk = func(items []*nestedRunSummary) []*nestedRunSummary {
		out := make([]*nestedRunSummary, len(items))
		for i, item := range items {
			if item.ID == ev.ParentRunID {
				idx := -1
				for j, c := range item.Children {
					if c.ID == ev.Child.ID {
						idx = j
					}
				}
				var prior *nestedRunSummary
				if idx >= 0 {
					prior = item.Children[idx]
				}
				next := mergeNestedSummary(prior, ev)
				kids := append([]*nestedRunSummary{}, item.Children...)
				if idx >= 0 {
					kids[idx] = next
				} else {
					kids = append(kids, next)
				}
				if len(kids) > maxNestedChildren {
					kids = kids[:maxNestedChildren]
				}
				c := copySummary(item)
				c.Children = kids
				lu := math.Max(floatOr0(item.LastUpdate), ev.TS)
				c.LastUpdate = &lu
				out[i] = c
				updated = true
				continue
			}
			if len(item.Children) == 0 {
				out[i] = item
				continue
			}
			next := walk(item.Children)
			c := copySummary(item)
			c.Children = next
			out[i] = c
		}
		return out
	}
	next := walk(children)
	if updated {
		return next
	}
	idx := -1
	for j, c := range next {
		if c.ID == ev.Child.ID {
			idx = j
		}
	}
	var prior *nestedRunSummary
	if idx >= 0 {
		prior = next[idx]
	}
	merged := mergeNestedSummary(prior, ev)
	if idx >= 0 {
		next[idx] = merged
		return next
	}
	next = append(next, merged)
	if len(next) > maxNestedChildren {
		next = next[:maxNestedChildren]
	}
	return next
}

func floatOr0(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func applyNestedEvent(reg *nestedRegistry, ev *nestedEventRecord) *nestedRegistry {
	out := *reg
	out.UpdatedAt = math.Max(reg.UpdatedAt, ev.TS)
	out.Children = attachNestedChild(reg.Children, ev)
	return &out
}

func nestedRegistryPath(r *nestedRoute) string {
	return filepath.Join(commonRouteRoot(r), nestedRegistryFile)
}

func readNestedRegistry(route *nestedRoute) (*nestedRegistry, error) {
	if err := validateNestedRouteShape(route); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(nestedRegistryPath(route))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &nestedRegistry{RootRunID: route.RootRunID, Children: []*nestedRunSummary{}, ProcessedEvents: []string{}}, nil
		}
		return nil, err
	}
	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		return nil, err
	}
	reg := &nestedRegistry{RootRunID: route.RootRunID, Children: []*nestedRunSummary{}, ProcessedEvents: []string{}}
	reg.UpdatedAt, _ = finiteNum(parsed["updatedAt"])
	if arr, ok := parsed["children"].([]any); ok {
		for _, c := range arr {
			if s := sanitizeNestedSummary(c, 0); s != nil {
				reg.Children = append(reg.Children, s)
			}
		}
	}
	if arr, ok := parsed["processedEvents"].([]any); ok {
		for _, e := range arr {
			if s, ok := e.(string); ok {
				reg.ProcessedEvents = append(reg.ProcessedEvents, s)
			}
		}
	}
	return reg, nil
}

func listNestedEventFiles(route *nestedRoute) ([]string, error) {
	if err := validateNestedRouteShape(route); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(route.EventSink)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") || strings.HasSuffix(e.Name(), ".jsonl") {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// readBoundedFile reads a regular file no larger than a nested record may be; ok is false for anything else.
func readBoundedFile(p string) (string, bool) {
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxNestedEventBytes {
		return "", false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// projectNestedEvents folds the route's new event files into its registry sidecar (the only writer of it) and returns it. Only a
// bounded cursor of file names is kept; the old immutable status records behind the cursor are removed once it is durable.
func projectNestedEvents(route *nestedRoute) (*nestedRegistry, error) {
	reg, err := readNestedRegistry(route)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	order := append([]string{}, reg.ProcessedEvents...)
	for _, e := range reg.ProcessedEvents {
		seen[e] = true
	}
	entries, err := listNestedEventFiles(route)
	if err != nil {
		return nil, err
	}
	sort.Strings(entries)
	changed := false
	for _, entry := range entries {
		if seen[entry] {
			continue
		}
		p := filepath.Join(route.EventSink, entry)
		if !containedPath(route.EventSink, p) {
			continue
		}
		content, ok := readBoundedFile(p)
		if !ok {
			continue
		}
		records := parseNestedEventRecords(content, route)
		if len(records) == 0 {
			continue
		}
		for _, ev := range records {
			reg = applyNestedEvent(reg, ev)
		}
		seen[entry] = true
		order = append(order, entry)
		changed = true
	}
	if changed {
		retained, evicted := order, []string(nil)
		if len(order) > maxNestedProcessed {
			retained, evicted = order[len(order)-maxNestedProcessed:], order[:len(order)-maxNestedProcessed]
		}
		out := *reg
		out.ProcessedEvents = retained
		reg = &out
		if err := asyncWriteJSON(nestedRegistryPath(route), reg); err != nil {
			return nil, err
		}
		for _, entry := range evicted {
			p := filepath.Join(route.EventSink, entry)
			if !containedPath(route.EventSink, p) {
				continue
			}
			content, ok := readBoundedFile(p)
			if !ok {
				continue
			}
			hasStatus := len(parseNestedEventRecords(content, route)) > 0
			hasResult := false
			for _, line := range strings.Split(content, "\n") {
				if strings.TrimSpace(line) != "" && parseNestedControlResult(strings.TrimSpace(line), route) != nil {
					hasResult = true
				}
			}
			if hasStatus && !hasResult {
				_ = os.Remove(p)
			}
		}
	}
	return reg, nil
}

func writeNestedRouteRecord(dir string, ts float64, payload any) (string, error) {
	b, err := marshalNoEscape(payload)
	if err != nil {
		return "", err
	}
	content := b + "\n"
	if len(content) > maxNestedEventBytes {
		return "", errors.New("Nested route record exceeds the maximum size.")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%013s-%s.json", jsNumber(ts), randomUUID())
	name = strings.Replace(name, " ", "0", -1)
	tmp := filepath.Join(dir, "."+name+".tmp")
	final := filepath.Join(dir, name)
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return final, nil
}

// writeNestedEvent writes one immutable status event of a nested run (it is parsed and sanitized exactly as the projection will).
func writeNestedEvent(route *nestedRoute, typ string, ts float64, parentRunID string, parentStepIndex *float64, child map[string]any) error {
	if err := validateNestedRouteShape(route); err != nil {
		return err
	}
	rec := map[string]any{"type": typ, "ts": ts, "parentRunId": parentRunID, "child": child, "rootRunId": route.RootRunID, "capabilityToken": route.CapabilityToken}
	if parentStepIndex != nil {
		rec["parentStepIndex"] = *parentStepIndex
	}
	b, _ := json.Marshal(rec)
	sanitized := parseNestedRecord(string(b), route)
	if sanitized == nil {
		return errors.New("Nested event record failed validation.")
	}
	_, err := writeNestedRouteRecord(route.EventSink, sanitized.TS, sanitized)
	return err
}

func parseNestedControlRequest(content string, route *nestedRoute) *nestedControlRequest {
	raw := decodeJSONObject(content)
	if raw == nil || raw["type"] != "subagent.nested.control-request" || raw["rootRunId"] != route.RootRunID || raw["capabilityToken"] != route.CapabilityToken {
		return nil
	}
	if !isSafeNestedIDValue(raw["requestId"]) || !isSafeNestedIDValue(raw["targetRunId"]) {
		return nil
	}
	action, _ := raw["action"].(string)
	if action != "interrupt" && action != "resume" {
		return nil
	}
	ts, ok := finiteNum(raw["ts"])
	if !ok {
		return nil
	}
	return &nestedControlRequest{Type: "subagent.nested.control-request", TS: ts, RootRunID: route.RootRunID, CapabilityToken: route.CapabilityToken,
		RequestID: raw["requestId"].(string), TargetRunID: raw["targetRunId"].(string), Action: action, Message: strVal(raw["message"], 16_000)}
}

func parseNestedControlResult(content string, route *nestedRoute) *nestedControlResult {
	raw := decodeJSONObject(content)
	if raw == nil || raw["type"] != "subagent.nested.control-result" || raw["rootRunId"] != route.RootRunID || raw["capabilityToken"] != route.CapabilityToken {
		return nil
	}
	if !isSafeNestedIDValue(raw["requestId"]) || !isSafeNestedIDValue(raw["targetRunId"]) {
		return nil
	}
	ts, ok := finiteNum(raw["ts"])
	okField, isBool := raw["ok"].(bool)
	if !ok || !isBool {
		return nil
	}
	msg := strVal(raw["message"], 16_000)
	if msg == "" {
		msg = "Control request failed."
		if okField {
			msg = "Control request completed."
		}
	}
	return &nestedControlResult{Type: "subagent.nested.control-result", TS: ts, RootRunID: route.RootRunID, CapabilityToken: route.CapabilityToken,
		RequestID: raw["requestId"].(string), TargetRunID: raw["targetRunId"].(string), OK: okField, Message: msg}
}

// writeNestedControlRequest drops a control request (interrupt or resume of a nested run) into the route's inbox.
func writeNestedControlRequest(route *nestedRoute, ts float64, requestID, targetRunID, action, message string) (string, error) {
	if err := validateNestedRouteShape(route); err != nil {
		return "", err
	}
	if err := assertSafeNestedID("requestId", requestID); err != nil {
		return "", err
	}
	if err := assertSafeNestedID("targetRunId", targetRunID); err != nil {
		return "", err
	}
	rec := map[string]any{"type": "subagent.nested.control-request", "ts": ts, "requestId": requestID, "targetRunId": targetRunID, "action": action,
		"rootRunId": route.RootRunID, "capabilityToken": route.CapabilityToken}
	if message != "" {
		rec["message"] = message
	}
	b, _ := json.Marshal(rec)
	sanitized := parseNestedControlRequest(string(b), route)
	if sanitized == nil {
		return "", errors.New("Nested control request failed validation.")
	}
	return writeNestedRouteRecord(route.ControlInbox, sanitized.TS, sanitized)
}

// readNestedControlRequests lists the valid requests of the inbox, oldest first.
func readNestedControlRequests(route *nestedRoute) ([]nestedControlRequest, error) {
	if err := validateNestedRouteShape(route); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(route.ControlInbox)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []nestedControlRequest
	for _, n := range names {
		p := filepath.Join(route.ControlInbox, n)
		if !containedPath(route.ControlInbox, p) {
			continue
		}
		content, ok := readBoundedFile(p)
		if !ok {
			continue
		}
		if r := parseNestedControlRequest(content, route); r != nil {
			r.FilePath = p
			out = append(out, *r)
		}
	}
	return out, nil
}

// writeNestedControlResult answers a request (a file in the event sink).
func writeNestedControlResult(route *nestedRoute, ts float64, requestID, targetRunID string, ok bool, message string) error {
	if err := validateNestedRouteShape(route); err != nil {
		return err
	}
	if err := assertSafeNestedID("requestId", requestID); err != nil {
		return err
	}
	if err := assertSafeNestedID("targetRunId", targetRunID); err != nil {
		return err
	}
	rec := map[string]any{"type": "subagent.nested.control-result", "ts": ts, "requestId": requestID, "targetRunId": targetRunID, "ok": ok, "message": message,
		"rootRunId": route.RootRunID, "capabilityToken": route.CapabilityToken}
	b, _ := json.Marshal(rec)
	sanitized := parseNestedControlResult(string(b), route)
	if sanitized == nil {
		return errors.New("Nested control result failed validation.")
	}
	_, err := writeNestedRouteRecord(route.EventSink, sanitized.TS, sanitized)
	return err
}

func readNestedControlResultsFromFile(route *nestedRoute, p string) []nestedControlResult {
	if !containedPath(route.EventSink, p) {
		return nil
	}
	content, ok := readBoundedFile(p)
	if !ok {
		return nil
	}
	lines := []string{content}
	if strings.Contains(content, "\n") {
		lines = nil
		for _, l := range strings.Split(content, "\n") {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, l)
			}
		}
	}
	var out []nestedControlResult
	for _, l := range lines {
		if r := parseNestedControlResult(l, route); r != nil {
			out = append(out, *r)
		}
	}
	return out
}

// snapshotNestedEventFiles is the set of event files there now (a control wait ignores everything older than its request).
func snapshotNestedEventFiles(route *nestedRoute) (map[string]bool, error) {
	files, err := listNestedEventFiles(route)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, f := range files {
		set[f] = true
	}
	return set, nil
}

// findNestedControlResult is the answer to a request among the event files that are not in ignored, newest file first.
func findNestedControlResult(route *nestedRoute, requestID, targetRunID string, ignored map[string]bool) (*nestedControlResult, error) {
	files, err := listNestedEventFiles(route)
	if err != nil {
		return nil, err
	}
	var entries []string
	for _, f := range files {
		if !ignored[f] {
			entries = append(entries, f)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(entries)))
	for _, e := range entries {
		for _, r := range readNestedControlResultsFromFile(route, filepath.Join(route.EventSink, e)) {
			if r.RequestID == requestID && r.TargetRunID == targetRunID {
				res := r
				return &res, nil
			}
		}
	}
	return nil, nil
}

func readNestedControlResults(route *nestedRoute) ([]nestedControlResult, error) {
	files, err := listNestedEventFiles(route)
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []nestedControlResult
	for _, f := range files {
		out = append(out, readNestedControlResultsFromFile(route, filepath.Join(route.EventSink, f))...)
	}
	return out, nil
}

func hasLiveNestedDescendants(children []*nestedRunSummary) bool {
	for _, c := range children {
		if !nestedTerminal(c.State) || hasLiveNestedDescendants(c.Children) {
			return true
		}
		var stepKids []*nestedRunSummary
		for _, s := range c.Steps {
			stepKids = append(stepKids, s.Children...)
		}
		if hasLiveNestedDescendants(stepKids) {
			return true
		}
	}
	return false
}

// findNestedRun looks a nested run up by id in a tree (children, then the children of its steps).
func findNestedRun(children []*nestedRunSummary, id string) *nestedRunSummary {
	for _, c := range children {
		if c.ID == id {
			return c
		}
		if n := findNestedRun(c.Children, id); n != nil {
			return n
		}
		var stepKids []*nestedRunSummary
		for _, s := range c.Steps {
			stepKids = append(stepKids, s.Children...)
		}
		if n := findNestedRun(stepKids, id); n != nil {
			return n
		}
	}
	return nil
}

func nestedResultsPath(rootRunID, id string) (string, error) {
	if err := assertSafeNestedID("id", id); err != nil {
		return "", err
	}
	_, results := nestedRunScope(rootRunID)
	return filepath.Join(results, id+".json"), assertSafeNestedID("rootRunId", rootRunID)
}

// routeFromIndexEntry reads one entry of the route index back into a validated route (nil when it is not one).
func routeFromIndexEntry(rootRunID, filePath string) *nestedRoute {
	b, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	var meta map[string]any
	if json.Unmarshal(b, &meta) != nil || meta["rootRunId"] != rootRunID {
		return nil
	}
	token, ok1 := meta["capabilityToken"].(string)
	routeRootName, ok2 := meta["routeRoot"].(string)
	if !ok1 || !ok2 {
		return nil
	}
	routeRoot := filepath.Join(nestedEventsDir(), filepath.Base(routeRootName))
	r := &nestedRoute{RootRunID: rootRunID, EventSink: filepath.Join(routeRoot, "events"), ControlInbox: filepath.Join(routeRoot, "controls"), CapabilityToken: token}
	if validateNestedRouteShape(r) != nil {
		return nil
	}
	for _, d := range []string{r.EventSink, r.ControlInbox} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			return nil
		}
	}
	routeFilePath := filepath.Join(routeRoot, nestedRouteFile)
	if rb, err := os.ReadFile(routeFilePath); err == nil {
		var rf map[string]any
		if json.Unmarshal(rb, &rf) != nil || rf["rootRunId"] != rootRunID || rf["capabilityToken"] != token {
			return nil
		}
	} else if errors.Is(err, fs.ErrNotExist) {
		created, ok := finiteNum(meta["createdAt"])
		if !ok {
			created = float64(nowMs())
		}
		_ = asyncWriteJSON(routeFilePath, map[string]any{"rootRunId": rootRunID, "capabilityToken": token, "createdAt": created})
	} else {
		return nil
	}
	return r
}

// findNestedRouteForRootID is the route a root run id has in the index (nil when it has none).
func findNestedRouteForRootID(rootRunID string) (*nestedRoute, error) {
	if err := assertSafeNestedID("rootRunId", rootRunID); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(nestedRouteIndexDirFor(rootRunID))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if r := routeFromIndexEntry(rootRunID, filepath.Join(nestedRouteIndexDirFor(rootRunID), e.Name())); r != nil {
			return r, nil
		}
	}
	return nil, nil
}

func listNestedRoutes() []*nestedRoute {
	roots, err := os.ReadDir(nestedRouteIndexRoot())
	if err != nil {
		return nil
	}
	var routes []*nestedRoute
	for _, root := range roots {
		rootRunID, err := url.PathUnescape(root.Name())
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(nestedRouteIndexRoot(), root.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if r := routeFromIndexEntry(rootRunID, filepath.Join(nestedRouteIndexRoot(), root.Name(), e.Name())); r != nil {
				routes = append(routes, r)
			}
		}
	}
	return routes
}

// nestedRunScopeFilter narrows a lookup of nested runs to the routes and the descendants of one run.
type nestedRunResolutionScope struct {
	Routes []*nestedRoute
	// DescendantOf restricts matches to the runs below (parentRunId, parentStepIndex).
	DescendantOf *struct {
		ParentRunID     string
		ParentStepIndex *float64
	}
}

type nestedRunMatch struct {
	RootRunID string
	Route     *nestedRoute
	Run       *nestedRunSummary
}

func collectNestedRuns(children []*nestedRunSummary, out *[]*nestedRunSummary) {
	for _, c := range children {
		*out = append(*out, c)
		collectNestedRuns(c.Children, out)
		var stepKids []*nestedRunSummary
		for _, s := range c.Steps {
			stepKids = append(stepKids, s.Children...)
		}
		collectNestedRuns(stepKids, out)
	}
}

func collectScopedNestedRuns(children []*nestedRunSummary, scope *struct {
	ParentRunID     string
	ParentStepIndex *float64
}, out *[]*nestedRunSummary) {
	if scope == nil {
		collectNestedRuns(children, out)
		return
	}
	for _, c := range children {
		if c.ParentRunID == scope.ParentRunID && (scope.ParentStepIndex == nil || (c.ParentStepIndex != nil && *c.ParentStepIndex == *scope.ParentStepIndex)) {
			collectNestedRuns([]*nestedRunSummary{c}, out)
			continue
		}
		collectScopedNestedRuns(c.Children, scope, out)
		var stepKids []*nestedRunSummary
		for _, s := range c.Steps {
			stepKids = append(stepKids, s.Children...)
		}
		collectScopedNestedRuns(stepKids, scope, out)
	}
}

// findNestedRunMatchesByID finds the nested runs with an id (or an id prefix) over the routes of the scope (every route there is
// when none is given); a route that cannot be read is skipped.
func findNestedRunMatchesByID(id string, prefix bool, scope *nestedRunResolutionScope) ([]nestedRunMatch, error) {
	if err := assertSafeNestedID("id", id); err != nil {
		return nil, err
	}
	var routes []*nestedRoute
	if scope != nil {
		routes = scope.Routes
	} else {
		routes = listNestedRoutes()
	}
	var matches []nestedRunMatch
	for _, route := range routes {
		reg, err := projectNestedEvents(route)
		if err != nil {
			continue
		}
		var runs []*nestedRunSummary
		if scope != nil {
			collectScopedNestedRuns(reg.Children, scope.DescendantOf, &runs)
		} else {
			collectScopedNestedRuns(reg.Children, nil, &runs)
		}
		for _, run := range runs {
			if (prefix && strings.HasPrefix(run.ID, id)) || (!prefix && run.ID == id) {
				matches = append(matches, nestedRunMatch{route.RootRunID, route, run})
			}
		}
	}
	return matches, nil
}
