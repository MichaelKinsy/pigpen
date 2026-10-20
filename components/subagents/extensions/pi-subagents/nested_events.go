package pi_subagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
)

// Nested runs (runs/shared/nested-events.ts, nested-path.ts). A child that runs subagents itself reports them to its root
// through a route: a directory pair (events, controls) under the nested-event root with a capability token. The parent projects
// the immutable event files into a registry. Summaries are plain JSON objects (jm): the original sanitizes every field on the
// way in and the registry is read back through the same sanitizer, so the object form is the form that crosses the boundary.

const (
	maxNestedEventBytes = 64 * 1024
	maxNestedSteps      = 12
	maxNestedChildren   = 16
	maxNestedDepth      = 3
	maxNestedPathLen    = 4
	maxNestedIDLength   = 128
	nestedRouteFile     = "route.json"
	nestedRegistryFile  = "registry.json"
	nestedRouteIndexDir = ".route-index"
)

func nestedEventsDir() string { return filepath.Join(tempRootDir(), "nested-subagent-events") }

// nestedRoute is NestedRouteInfo.
type nestedRoute struct {
	RootRunID       string `json:"rootRunId"`
	EventSink       string `json:"eventSink"`
	ControlInbox    string `json:"controlInbox"`
	CapabilityToken string `json:"capabilityToken"`
}

type nestedRegistry struct {
	RootRunID       string   `json:"rootRunId"`
	UpdatedAt       float64  `json:"updatedAt"`
	Children        []jm     `json:"children"`
	ProcessedEvents []string `json:"processedEvents"`
}

type nestedEventRecord struct {
	Type            string
	TS              float64
	RootRunID       string
	ParentRunID     string
	ParentStepIndex *float64
	CapabilityToken string
	Child           jm
}

func (e nestedEventRecord) MarshalJSON() ([]byte, error) {
	m := jm{"type": e.Type, "ts": e.TS, "rootRunId": e.RootRunID, "parentRunId": e.ParentRunID, "capabilityToken": e.CapabilityToken, "child": e.Child}
	if e.ParentStepIndex != nil {
		m["parentStepIndex"] = *e.ParentStepIndex
	}
	return json.Marshal(m)
}

// ── ids and paths ──

func isSafeNestedPathID(v any) bool {
	s, ok := v.(string)
	return ok && s != "" && len(utf16.Encode([]rune(s))) <= maxNestedIDLength && !filepath.IsAbs(s) && !strings.HasPrefix(s, "/") && !strings.ContainsAny(s, `/\`) && !strings.Contains(s, "..")
}

func nestedIDOK(v any) bool { return isSafeNestedPathID(v) }

func assertSafeNestedID(label string, v string) error {
	if !nestedIDOK(v) {
		return fmt.Errorf("%s must be a non-empty safe id token.", label)
	}
	return nil
}

// jsSliceUnits is String.prototype.slice(0, n): n UTF-16 code units.
func jsSliceUnits(s string, n int) string {
	u := utf16.Encode([]rune(s))
	if len(u) <= n {
		return s
	}
	return string(utf16.Decode(u[:n]))
}

func nestedString(v any, max int) (string, bool) {
	s, ok := v.(string)
	if !ok || s == "" {
		return "", false
	}
	return jsSliceUnits(s, max), true
}

func sanitizeNestedPath(v any) []any {
	list, ok := v.([]any)
	if !ok {
		return []any{}
	}
	out := []any{}
	for _, part := range list {
		rec, ok := part.(map[string]any)
		if !ok || !isSafeNestedPathID(rec["runId"]) {
			continue
		}
		e := jm{"runId": rec["runId"]}
		if n, ok := finiteNum(rec["stepIndex"]); ok {
			e["stepIndex"] = n
		}
		if s, ok := nestedString(rec["agent"], 128); ok {
			e["agent"] = s
		}
		out = append(out, e)
		if len(out) == maxNestedPathLen {
			break
		}
	}
	return out
}

func nestedContained(base, candidate string) bool {
	b, c := filepath.Clean(base), filepath.Clean(candidate)
	if !filepath.IsAbs(b) {
		b, _ = filepath.Abs(b)
	}
	if !filepath.IsAbs(c) {
		c, _ = filepath.Abs(c)
	}
	return c == b || strings.HasPrefix(c, b+string(filepath.Separator))
}

func nestedCommonRouteRoot(r nestedRoute) string {
	abs, _ := filepath.Abs(r.EventSink)
	return filepath.Dir(abs)
}

func nestedRouteIndexRoot() string {
	return filepath.Join(nestedEventsDir(), nestedRouteIndexDir, "roots")
}

func nestedRouteIndexDirOf(rootRunID string) string {
	return filepath.Join(nestedRouteIndexRoot(), encodeURIComponentJS(rootRunID))
}

// encodeURIComponentJS is encodeURIComponent: everything but A-Z a-z 0-9 - _ . ! ~ * ' ( ) is percent-encoded.
func encodeURIComponentJS(s string) string {
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")
	for _, c := range []string{"!", "'", "(", ")", "*"} {
		e = strings.ReplaceAll(e, url.QueryEscape(c), c)
	}
	return e
}

func nestedRouteIndexPath(rootRunID, token string) string {
	return filepath.Join(nestedRouteIndexDirOf(rootRunID), encodeURIComponentJS(token)+".json")
}

func validateNestedRouteShape(r nestedRoute) error {
	if err := assertSafeNestedID("rootRunId", r.RootRunID); err != nil {
		return err
	}
	if err := assertSafeNestedID("capabilityToken", r.CapabilityToken); err != nil {
		return err
	}
	if !nestedContained(nestedEventsDir(), r.EventSink) {
		return errors.New("Nested event sink is outside the subagent nested event root.")
	}
	if !nestedContained(nestedEventsDir(), r.ControlInbox) {
		return errors.New("Nested control inbox is outside the subagent nested event root.")
	}
	inbox, _ := filepath.Abs(r.ControlInbox)
	if nestedCommonRouteRoot(r) != filepath.Dir(inbox) {
		return errors.New("Nested event sink and control inbox must share one route root.")
	}
	return nil
}

func createNestedRoute(rootRunID string) (nestedRoute, error) {
	if err := assertSafeNestedID("rootRunId", rootRunID); err != nil {
		return nestedRoute{}, err
	}
	token := randomUUID()
	routeRoot := filepath.Join(nestedEventsDir(), rootRunID+"-"+token)
	r := nestedRoute{RootRunID: rootRunID, EventSink: filepath.Join(routeRoot, "events"), ControlInbox: filepath.Join(routeRoot, "controls"), CapabilityToken: token}
	for _, d := range []string{r.EventSink, r.ControlInbox, nestedRouteIndexDirOf(rootRunID)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nestedRoute{}, err
		}
	}
	created := nowMs()
	if err := asyncWriteJSON(nestedRouteIndexPath(rootRunID, token), jm{"rootRunId": rootRunID, "capabilityToken": token, "routeRoot": filepath.Base(routeRoot), "createdAt": created}); err != nil {
		return nestedRoute{}, err
	}
	if err := asyncWriteJSON(filepath.Join(routeRoot, nestedRouteFile), jm{"rootRunId": rootRunID, "capabilityToken": token, "createdAt": created}); err != nil {
		return nestedRoute{}, err
	}
	return r, nil
}

// resolveNestedRoute validates a route handed to a child against its on-disk metadata.
func resolveNestedRoute(r nestedRoute) (nestedRoute, error) {
	if err := validateNestedRouteShape(r); err != nil {
		return nestedRoute{}, err
	}
	var meta struct {
		RootRunID       any `json:"rootRunId"`
		CapabilityToken any `json:"capabilityToken"`
	}
	b, err := os.ReadFile(filepath.Join(nestedCommonRouteRoot(r), nestedRouteFile))
	if err != nil {
		return nestedRoute{}, err
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		return nestedRoute{}, err
	}
	if meta.RootRunID != r.RootRunID || meta.CapabilityToken != r.CapabilityToken {
		return nestedRoute{}, errors.New("Nested event route metadata does not match the provided root id and capability token.")
	}
	return r, nil
}

// nestedRouteLog receives the diagnostics of an ignored route (console.error in the original).
var nestedRouteLog = func(msg string, err error) { fmt.Fprintln(os.Stderr, msg, err) }

func resolveInheritedNestedRoute(r nestedRoute) *nestedRoute {
	res, err := resolveNestedRoute(r)
	if err != nil {
		nestedRouteLog("Ignoring invalid nested subagent event route:", err)
		return nil
	}
	return &res
}

// childRuntimeNested is the part of a child runtime the nested helpers read.
type childRuntimeNested struct {
	NestedRoute  *nestedRoute
	NestedParent *nestedParent
}

type nestedParent struct {
	ParentRunID      string
	ParentChildIndex *float64
	Depth            float64
	Path             []any
}

type nestedParentAddress struct {
	ParentRunID     string
	ParentStepIndex *float64
	Depth           float64
	Path            []any
}

func inheritedNestedRouteOf(rt *childRuntimeNested) *nestedRoute {
	if rt == nil || rt.NestedRoute == nil {
		return nil
	}
	return resolveInheritedNestedRoute(*rt.NestedRoute)
}

func inheritedNestedParentAddressOf(rt *childRuntimeNested) *nestedParentAddress {
	if rt == nil || rt.NestedParent == nil || !nestedIDOK(rt.NestedParent.ParentRunID) {
		return nil
	}
	p := rt.NestedParent
	depth := math.Min(math.Max(1, p.Depth), maxNestedDepth)
	path := p.Path
	if len(path) == 0 {
		e := jm{"runId": p.ParentRunID}
		if p.ParentChildIndex != nil {
			e["stepIndex"] = *p.ParentChildIndex
		}
		path = []any{e}
	}
	return &nestedParentAddress{ParentRunID: p.ParentRunID, ParentStepIndex: p.ParentChildIndex, Depth: depth, Path: path}
}

func resolveNestedAsyncDir(rootRunID string, run jm) string {
	d, _ := run["asyncDir"].(string)
	if d == "" {
		return ""
	}
	resolved, _ := filepath.Abs(d)
	nestedRoot, _ := filepath.Abs(filepath.Join(tempRootDir(), "nested-subagent-runs", rootRunID, strOf(run["id"])))
	rel, err := filepath.Rel(nestedRoot, resolved)
	if resolved == nestedRoot || (err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)) {
		return resolved
	}
	return ""
}

// ── sanitizers ──

func sanitizeNestedTokenUsage(v any) jm {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	in, ok1 := finiteNum(raw["input"])
	out, ok2 := finiteNum(raw["output"])
	total, ok3 := finiteNum(raw["total"])
	if !ok1 || !ok2 || !ok3 {
		return nil
	}
	m := jm{"input": in, "output": out, "total": total}
	if w, ok := finiteNum(raw["window"]); ok {
		m["window"] = w
	}
	if w, ok := finiteNum(raw["windowPeak"]); ok {
		m["windowPeak"] = w
	}
	return m
}

func sanitizeNestedCost(v any) jm {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	a, ok1 := finiteNum(raw["inputTokens"])
	b, ok2 := finiteNum(raw["outputTokens"])
	c, ok3 := finiteNum(raw["costUsd"])
	if !ok1 || !ok2 || !ok3 {
		return nil
	}
	return jm{"inputTokens": a, "outputTokens": b, "costUsd": c}
}

func nestedStringList(v any, keep func(string) bool, limit int) []any {
	out := []any{}
	if list, ok := v.([]any); ok {
		for _, it := range list {
			if s, ok := it.(string); ok && keep(s) {
				out = append(out, s)
			}
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func sanitizeNestedLaunchResolvedExtensions(v any) jm {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	disable, isBool := raw["disableAmbientExtensions"].(bool)
	if ver, _ := raw["version"].(float64); ver != 1 || raw["source"] != "launch-resolved" || !isBool {
		return nil
	}
	hash := func(s string) bool { return len(s) == 23 && strings.HasPrefix(s, "sha256:") && isLowerHex(s[7:]) }
	name := func(s string) bool { return nestedRequiredName(s) }
	omitted, _ := raw["omitted"].(map[string]any)
	count := func(k string) float64 {
		n, _ := finiteNum(omitted[k])
		return math.Max(0, math.Floor(n))
	}
	return jm{"version": float64(1), "source": "launch-resolved", "disableAmbientExtensions": disable,
		"runtime": nestedStringList(raw["runtime"], hash, 32), "configured": nestedStringList(raw["configured"], hash, 32),
		"required": nestedStringList(raw["required"], name, 32), "effective": nestedStringList(raw["effective"], hash, 32),
		"omitted": jm{"runtime": count("runtime"), "configured": count("configured"), "required": count("required"), "effective": count("effective")}}
}

func isLowerHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return s != ""
}

// nestedRequiredName is /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.
func nestedRequiredName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i, c := range s {
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if i == 0 && !alnum || !alnum && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func nestedAckIDOK(s string) bool {
	if s == "" || len(s) > 128 || strings.Contains(s, "..") || strings.ContainsAny(s, `/\`) {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._:@+-", c)) {
			return false
		}
	}
	return true
}

func sanitizeNestedRuntimeAck(v any) jm {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	list, isList := raw["ids"].([]any)
	if ver, _ := raw["version"].(float64); ver != 1 || raw["source"] != "child-runtime" || !isList {
		return nil
	}
	ids := []any{}
	seen := map[string]bool{}
	for _, it := range list {
		s, ok := it.(string)
		if !ok || !nestedAckIDOK(s) || seen[s] {
			continue
		}
		seen[s] = true
		ids = append(ids, s)
	}
	if len(ids) == 0 {
		return nil
	}
	om, _ := finiteNum(raw["omitted"])
	omitted := math.Max(0, float64(len(ids)-32)) + math.Max(0, math.Floor(om))
	if len(ids) > 32 {
		ids = ids[:32]
	}
	return jm{"version": float64(1), "source": "child-runtime", "ids": ids, "omitted": omitted}
}

func sanitizeNestedTurnBudget(v any) jm {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	maxT, ok1 := finiteNum(raw["maxTurns"])
	grace, ok2 := finiteNum(raw["graceTurns"])
	count, ok3 := finiteNum(raw["turnCount"])
	outcome, _ := raw["outcome"].(string)
	switch outcome {
	case "within-budget", "wrap-up-requested", "termination-deferred", "exceeded":
	default:
		return nil
	}
	if !ok1 || !ok2 || !ok3 {
		return nil
	}
	m := jm{"maxTurns": maxT, "graceTurns": grace, "turnCount": count, "outcome": outcome}
	for _, k := range []string{"wrapUpRequestedAtTurn", "terminationDeferredAtTurn", "exceededAtTurn"} {
		if n, ok := finiteNum(raw[k]); ok {
			m[k] = n
		}
	}
	return m
}

func sanitizeNestedState(v any, fallback string) string {
	if s, ok := v.(string); ok {
		switch s {
		case "queued", "running", "complete", "failed", "partial", "paused", "stopped", "rejected":
			return s
		}
	}
	return fallback
}

func nestedThinking(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	for _, l := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if l == s {
			return s, true
		}
	}
	return "", false
}

func setNestedStr(dst jm, src map[string]any, key string, max int) {
	if s, ok := nestedString(src[key], max); ok {
		dst[key] = s
	}
}

func setNestedNum(dst jm, src map[string]any, key string) {
	if n, ok := finiteNum(src[key]); ok {
		dst[key] = n
	}
}

func setNestedActivity(dst jm, src map[string]any) {
	if a := src["activityState"]; a == "active_long_running" || a == "needs_attention" {
		dst["activityState"] = a
	}
}

func setNestedFlags(dst jm, src map[string]any) {
	for _, k := range []string{"timedOut", "stopped", "turnBudgetExceeded", "wrapUpRequested"} {
		if src[k] == true {
			dst[k] = true
		}
	}
	if tb := sanitizeNestedTurnBudget(src["turnBudget"]); tb != nil {
		dst["turnBudget"] = tb
	}
	if l := sanitizeNestedLaunchResolvedExtensions(src["launchResolvedExtensions"]); l != nil {
		dst["launchResolvedExtensions"] = l
	}
	if a := sanitizeNestedRuntimeAck(src["runtimeAcknowledgedExtensions"]); a != nil {
		dst["runtimeAcknowledgedExtensions"] = a
	}
}

func sanitizeNestedStep(v any, depth int) jm {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	agent, ok := nestedString(raw["agent"], 128)
	if !ok {
		return nil
	}
	status := "pending"
	if s, _ := raw["status"].(string); s != "" {
		switch s {
		case "pending", "running", "complete", "completed", "failed", "partial", "paused", "stopped", "rejected":
			status = s
		}
	}
	m := jm{"agent": agent, "status": status}
	setNestedStr(m, raw, "model", 512)
	if t, ok := nestedThinking(raw["thinking"]); ok {
		m["thinking"] = t
	}
	setNestedStr(m, raw, "sessionName", 256)
	setNestedStr(m, raw, "sessionFile", 2048)
	setNestedActivity(m, raw)
	setNestedNum(m, raw, "lastActivityAt")
	setNestedStr(m, raw, "currentTool", 128)
	setNestedNum(m, raw, "currentToolStartedAt")
	setNestedStr(m, raw, "currentPath", 2048)
	for _, k := range []string{"turnCount", "toolCount", "startedAt", "endedAt"} {
		setNestedNum(m, raw, k)
	}
	setNestedStr(m, raw, "error", 1024)
	setNestedFlags(m, raw)
	if kids, ok := raw["children"].([]any); ok && depth < maxNestedDepth {
		m["children"] = sanitizeNestedChildren(kids, depth+1)
	}
	return m
}

func sanitizeNestedChildren(list []any, depth int) []jm {
	out := []jm{}
	for _, c := range list {
		if s := sanitizeNestedSummary(c, depth); s != nil {
			out = append(out, s)
		}
	}
	if len(out) > maxNestedChildren {
		out = out[:maxNestedChildren]
	}
	return out
}

func sanitizeNestedSummary(input any, depth int) jm {
	raw, ok := input.(map[string]any)
	if !ok || !nestedIDOK(raw["id"]) || !nestedIDOK(raw["parentRunId"]) {
		return nil
	}
	m := jm{"id": raw["id"], "parentRunId": raw["parentRunId"]}
	setNestedNum(m, raw, "parentStepIndex")
	setNestedStr(m, raw, "parentAgent", 128)
	d, _ := finiteNum(raw["depth"])
	m["depth"] = math.Min(math.Max(0, d), maxNestedDepth)
	m["path"] = sanitizeNestedPath(raw["path"])
	m["state"] = sanitizeNestedState(raw["state"], "running")
	setNestedStr(m, raw, "sessionName", 256)
	setNestedStr(m, raw, "model", 512)
	if t, ok := nestedThinking(raw["thinking"]); ok {
		m["thinking"] = t
	}
	setNestedStr(m, raw, "asyncDir", 2048)
	if n, ok := finiteNum(raw["pid"]); ok && n > 0 && n == math.Trunc(n) {
		m["pid"] = n
	}
	setNestedStr(m, raw, "sessionId", 256)
	setNestedStr(m, raw, "sessionFile", 2048)
	for _, k := range []string{"intercomTarget", "ownerIntercomTarget", "leafIntercomTarget"} {
		setNestedStr(m, raw, k, 256)
	}
	if o := raw["ownerState"]; o == "live" || o == "gone" || o == "unknown" {
		m["ownerState"] = o
	}
	setNestedStr(m, raw, "controlInbox", 2048)
	setNestedStr(m, raw, "capabilityToken", 128)
	if md := raw["mode"]; md == "single" || md == "parallel" || md == "chain" {
		m["mode"] = md
	}
	setNestedStr(m, raw, "agent", 128)
	if agents, ok := raw["agents"].([]any); ok {
		out := []any{}
		for _, a := range agents {
			if s, ok := nestedString(a, 128); ok {
				out = append(out, s)
			}
		}
		if len(out) > maxNestedSteps {
			out = out[:maxNestedSteps]
		}
		m["agents"] = out
	}
	setNestedNum(m, raw, "currentStep")
	setNestedNum(m, raw, "chainStepCount")
	setNestedActivity(m, raw)
	setNestedNum(m, raw, "lastActivityAt")
	setNestedStr(m, raw, "currentTool", 128)
	setNestedNum(m, raw, "currentToolStartedAt")
	setNestedStr(m, raw, "currentPath", 2048)
	setNestedNum(m, raw, "turnCount")
	setNestedNum(m, raw, "toolCount")
	if t := sanitizeNestedTokenUsage(raw["totalTokens"]); t != nil {
		m["totalTokens"] = t
	}
	if c := sanitizeNestedCost(raw["totalCost"]); c != nil {
		m["totalCost"] = c
	}
	for _, k := range []string{"startedAt", "endedAt", "lastUpdate", "timeoutMs", "deadlineAt"} {
		setNestedNum(m, raw, k)
	}
	setNestedStr(m, raw, "error", 1024)
	setNestedFlags(m, raw)
	if steps, ok := raw["steps"].([]any); ok {
		out := []jm{}
		for _, s := range steps {
			if st := sanitizeNestedStep(s, depth+1); st != nil {
				out = append(out, st)
			}
		}
		if len(out) > maxNestedSteps {
			out = out[:maxNestedSteps]
		}
		if len(out) > 0 {
			m["steps"] = out
		}
	}
	if kids, ok := raw["children"].([]any); ok && depth < maxNestedDepth {
		m["children"] = sanitizeNestedChildren(kids, depth+1)
	}
	return m
}

// ── records ──

func parseNestedRecord(content string, r nestedRoute) *nestedEventRecord {
	if len(content) > maxNestedEventBytes {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(content), &raw); err != nil || raw == nil {
		return nil
	}
	typ, _ := raw["type"].(string)
	if typ != "subagent.nested.started" && typ != "subagent.nested.updated" && typ != "subagent.nested.completed" {
		return nil
	}
	if raw["rootRunId"] != r.RootRunID || raw["capabilityToken"] != r.CapabilityToken || !nestedIDOK(raw["parentRunId"]) {
		return nil
	}
	ts, ok := finiteNum(raw["ts"])
	if !ok {
		return nil
	}
	child := sanitizeNestedSummary(raw["child"], 0)
	if child == nil || child["id"] == r.RootRunID {
		return nil
	}
	child["controlInbox"], child["capabilityToken"] = r.ControlInbox, r.CapabilityToken
	if _, has := child["ownerState"]; !has {
		child["ownerState"] = "unknown"
	}
	rec := &nestedEventRecord{Type: typ, TS: ts, RootRunID: r.RootRunID, ParentRunID: raw["parentRunId"].(string), CapabilityToken: r.CapabilityToken, Child: child}
	if n, ok := finiteNum(raw["parentStepIndex"]); ok {
		rec.ParentStepIndex = &n
	}
	return rec
}

// parseNestedEventRecords reads only the complete JSONL lines of content (a record without a newline is one whole file).
func parseNestedEventRecords(content string, r nestedRoute) []nestedEventRecord {
	out := []nestedEventRecord{}
	if !strings.Contains(content, "\n") {
		if rec := parseNestedRecord(strings.TrimSpace(content), r); rec != nil {
			out = append(out, *rec)
		}
		return out
	}
	lines := strings.Split(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if rec := parseNestedRecord(l, r); rec != nil {
			out = append(out, *rec)
		}
	}
	return out
}

func nestedTerminal(state any) bool {
	switch state {
	case "complete", "failed", "partial", "paused", "rejected", "stopped":
		return true
	}
	return false
}

func copyJM(m jm) jm {
	out := make(jm, len(m)+2)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func asJMList(v any) []jm {
	switch l := v.(type) {
	case []jm:
		return l
	case []any:
		out := make([]jm, 0, len(l))
		for _, x := range l {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func capJM(l []jm, n int) []jm {
	if len(l) > n {
		return l[:n]
	}
	return l
}

func mergeBoundedChildren(existing, incoming []jm, hasIncoming bool) ([]jm, bool) {
	if !hasIncoming {
		if existing == nil {
			return nil, false
		}
		return capJM(existing, maxNestedChildren), true
	}
	byID := map[string]jm{}
	for _, c := range incoming {
		byID[strOf(c["id"])] = c
	}
	merged := []jm{}
	for _, c := range existing {
		if in, ok := byID[strOf(c["id"])]; ok {
			merged = append(merged, in)
		} else {
			merged = append(merged, c)
		}
	}
	for _, c := range incoming {
		found := false
		for _, p := range existing {
			if p["id"] == c["id"] {
				found = true
			}
		}
		if !found {
			merged = append(merged, c)
		}
	}
	return capJM(merged, maxNestedChildren), true
}

func mergeNestedStep(existing, incoming jm) jm {
	if existing == nil || existing["agent"] != incoming["agent"] {
		out := copyJM(incoming)
		if kids := asJMList(incoming["children"]); incoming["children"] != nil {
			out["children"] = capJM(kids, maxNestedChildren)
		}
		return out
	}
	out := copyJM(existing)
	for _, k := range []string{"status", "activityState", "lastActivityAt", "currentTool", "currentToolStartedAt", "currentPath", "turnCount", "toolCount", "children"} {
		delete(out, k)
	}
	for k, v := range incoming {
		out[k] = v
	}
	if kids, ok := mergeBoundedChildren(asJMList(existing["children"]), asJMList(incoming["children"]), incoming["children"] != nil); ok {
		out["children"] = kids
	}
	return out
}

func numOr(m jm, k string, def float64) float64 {
	if n, ok := m[k].(float64); ok {
		return n
	}
	return def
}

func mergeNestedSummary(existing jm, ev nestedEventRecord) jm {
	incoming := copyJM(ev.Child)
	state := ev.Child["state"]
	if ev.Type == "subagent.nested.completed" && state == "running" {
		state = "complete"
	}
	incoming["state"] = state
	incoming["lastUpdate"] = numOr(ev.Child, "lastUpdate", ev.TS)
	if existing == nil {
		return incoming
	}
	existingUpdate := numOr(existing, "lastUpdate", 0)
	incomingUpdate := numOr(incoming, "lastUpdate", ev.TS)
	if incomingUpdate < existingUpdate {
		return existing
	}
	if nestedTerminal(existing["state"]) && !nestedTerminal(incoming["state"]) {
		return existing
	}
	if nestedTerminal(existing["state"]) && nestedTerminal(incoming["state"]) && incomingUpdate == existingUpdate {
		return existing
	}
	out := copyJM(existing)
	for k, v := range incoming {
		out[k] = v
	}
	if incoming["steps"] == nil {
		if existing["steps"] != nil {
			out["steps"] = existing["steps"]
		}
	} else {
		es, is := asJMList(existing["steps"]), asJMList(incoming["steps"])
		n := min(maxNestedSteps, max(len(es), len(is)))
		steps := []jm{}
		for i := range n {
			var e jm
			if i < len(es) {
				e = es[i]
			}
			switch {
			case i < len(is):
				steps = append(steps, mergeNestedStep(e, is[i]))
			case e != nil:
				steps = append(steps, e)
			}
		}
		out["steps"] = steps
	}
	if kids, ok := mergeBoundedChildren(asJMList(existing["children"]), asJMList(incoming["children"]), incoming["children"] != nil); ok {
		out["children"] = kids
	}
	out["state"] = incoming["state"]
	out["lastUpdate"] = math.Max(existingUpdate, incomingUpdate)
	return out
}

func attachNestedChild(children []jm, ev nestedEventRecord) []jm {
	updated := false
	var walk func(items []jm) []jm
	walk = func(items []jm) []jm {
		out := make([]jm, len(items))
		for i, item := range items {
			if item["id"] == ev.ParentRunID {
				existing := asJMList(item["children"])
				idx := -1
				for j, c := range existing {
					if c["id"] == ev.Child["id"] {
						idx = j
					}
				}
				var prior jm
				if idx >= 0 {
					prior = existing[idx]
				}
				next := mergeNestedSummary(prior, ev)
				nextChildren := append([]jm{}, existing...)
				if idx >= 0 {
					nextChildren[idx] = next
				} else {
					nextChildren = append(nextChildren, next)
				}
				updated = true
				cp := copyJM(item)
				cp["children"] = capJM(nextChildren, maxNestedChildren)
				cp["lastUpdate"] = math.Max(numOr(item, "lastUpdate", 0), ev.TS)
				out[i] = cp
				continue
			}
			kids := asJMList(item["children"])
			if len(kids) == 0 {
				out[i] = item
				continue
			}
			cp := copyJM(item)
			cp["children"] = walk(kids)
			out[i] = cp
		}
		return out
	}
	next := walk(children)
	if updated {
		return next
	}
	idx := -1
	for j, c := range next {
		if c["id"] == ev.Child["id"] {
			idx = j
		}
	}
	var prior jm
	if idx >= 0 {
		prior = next[idx]
	}
	merged := mergeNestedSummary(prior, ev)
	if idx >= 0 {
		next[idx] = merged
		return next
	}
	return capJM(append(next, merged), maxNestedChildren)
}

func applyNestedEvent(reg nestedRegistry, ev nestedEventRecord) nestedRegistry {
	reg.UpdatedAt = math.Max(reg.UpdatedAt, ev.TS)
	reg.Children = attachNestedChild(reg.Children, ev)
	return reg
}

func nestedRegistryPath(r nestedRoute) string {
	return filepath.Join(nestedCommonRouteRoot(r), nestedRegistryFile)
}

func readNestedRegistry(r nestedRoute) (nestedRegistry, error) {
	if err := validateNestedRouteShape(r); err != nil {
		return nestedRegistry{}, err
	}
	empty := nestedRegistry{RootRunID: r.RootRunID, Children: []jm{}, ProcessedEvents: []string{}}
	b, err := os.ReadFile(nestedRegistryPath(r))
	if err != nil {
		if isNotExist(err) {
			return empty, nil
		}
		return nestedRegistry{}, err
	}
	var parsed struct {
		UpdatedAt       any   `json:"updatedAt"`
		Children        any   `json:"children"`
		ProcessedEvents []any `json:"processedEvents"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		return nestedRegistry{}, err
	}
	reg := empty
	if n, ok := parsed.UpdatedAt.(float64); ok {
		reg.UpdatedAt = n
	}
	if list, ok := parsed.Children.([]any); ok {
		for _, c := range list {
			if s := sanitizeNestedSummary(c, 0); s != nil {
				reg.Children = append(reg.Children, s)
			}
		}
	}
	for _, p := range parsed.ProcessedEvents {
		if s, ok := p.(string); ok {
			reg.ProcessedEvents = append(reg.ProcessedEvents, s)
		}
	}
	return reg, nil
}

func nestedEventEntries(sink string, sorted bool) ([]string, error) {
	ents, err := os.ReadDir(sink)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if n := e.Name(); strings.HasSuffix(n, ".json") || strings.HasSuffix(n, ".jsonl") {
			out = append(out, n)
		}
	}
	if sorted {
		sort.Strings(out)
	}
	return out, nil
}

func readNestedEventFile(sink, entry string) (string, bool) {
	p := filepath.Join(sink, entry)
	if !nestedContained(sink, p) {
		return "", false
	}
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

// projectNestedEvents folds the new event files into the registry (the only writer of registry.json) and drops the status
// files its bounded cursor no longer lists, so an evicted name is never replayed.
func projectNestedEvents(r nestedRoute) (nestedRegistry, error) {
	if err := validateNestedRouteShape(r); err != nil {
		return nestedRegistry{}, err
	}
	reg, err := readNestedRegistry(r)
	if err != nil {
		return nestedRegistry{}, err
	}
	seen := map[string]bool{}
	order := append([]string{}, reg.ProcessedEvents...)
	for _, e := range reg.ProcessedEvents {
		seen[e] = true
	}
	entries, err := nestedEventEntries(r.EventSink, true)
	if err != nil {
		return nestedRegistry{}, err
	}
	changed := false
	for _, entry := range entries {
		if seen[entry] {
			continue
		}
		content, ok := readNestedEventFile(r.EventSink, entry)
		if !ok {
			continue
		}
		records := parseNestedEventRecords(content, r)
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
		if len(order) > 1000 {
			retained, evicted = order[len(order)-1000:], order[:len(order)-1000]
		}
		reg.ProcessedEvents = retained
		if err := asyncWriteJSON(nestedRegistryPath(r), reg); err != nil {
			return nestedRegistry{}, err
		}
		for _, entry := range evicted {
			content, ok := readNestedEventFile(r.EventSink, entry)
			if !ok {
				continue
			}
			hasStatus := len(parseNestedEventRecords(content, r)) > 0
			hasControl := false
			for _, l := range strings.Split(content, "\n") {
				if t := strings.TrimSpace(l); t != "" && parseNestedControlResult(t, r) != nil {
					hasControl = true
				}
			}
			if hasStatus && !hasControl {
				_ = os.Remove(filepath.Join(r.EventSink, entry))
			}
		}
	}
	return reg, nil
}

func writeNestedRouteRecord(dir string, ts float64, payload any) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	content := append(b, '\n')
	if len(content) > maxNestedEventBytes {
		return "", errors.New("Nested route record exceeds the maximum size.")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%013d-%s.json", int64(ts), randomUUID())
	tmpPath, final := filepath.Join(dir, "."+name+".tmp"), filepath.Join(dir, name)
	if err := os.WriteFile(tmpPath, content, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, final); err != nil {
		return "", err
	}
	return final, nil
}

// writeNestedEvent writes one status event: the record is validated through the same parser the parent reads it with.
func writeNestedEvent(r nestedRoute, typ string, ts float64, parentRunID string, parentStepIndex *float64, child jm) error {
	if err := validateNestedRouteShape(r); err != nil {
		return err
	}
	rec := jm{"type": typ, "ts": ts, "parentRunId": parentRunID, "child": child, "rootRunId": r.RootRunID, "capabilityToken": r.CapabilityToken}
	if parentStepIndex != nil {
		rec["parentStepIndex"] = *parentStepIndex
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	parsed := parseNestedRecord(string(b), r)
	if parsed == nil {
		return errors.New("Nested event record failed validation.")
	}
	_, err = writeNestedRouteRecord(r.EventSink, parsed.TS, parsed)
	return err
}

// ── control records ──

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

func parseNestedControlRequest(content string, r nestedRoute) *nestedControlRequest {
	if len(content) > maxNestedEventBytes {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal([]byte(content), &raw) != nil || raw == nil || raw["type"] != "subagent.nested.control-request" {
		return nil
	}
	if raw["rootRunId"] != r.RootRunID || raw["capabilityToken"] != r.CapabilityToken || !nestedIDOK(raw["requestId"]) || !nestedIDOK(raw["targetRunId"]) {
		return nil
	}
	action, _ := raw["action"].(string)
	ts, ok := finiteNum(raw["ts"])
	if (action != "interrupt" && action != "resume") || !ok {
		return nil
	}
	req := &nestedControlRequest{Type: "subagent.nested.control-request", TS: ts, RootRunID: r.RootRunID, CapabilityToken: r.CapabilityToken,
		RequestID: raw["requestId"].(string), TargetRunID: raw["targetRunId"].(string), Action: action}
	if m, ok := nestedString(raw["message"], 16000); ok {
		req.Message = m
	}
	return req
}

func parseNestedControlResult(content string, r nestedRoute) *nestedControlResult {
	if len(content) > maxNestedEventBytes {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal([]byte(content), &raw) != nil || raw == nil || raw["type"] != "subagent.nested.control-result" {
		return nil
	}
	if raw["rootRunId"] != r.RootRunID || raw["capabilityToken"] != r.CapabilityToken || !nestedIDOK(raw["requestId"]) || !nestedIDOK(raw["targetRunId"]) {
		return nil
	}
	ts, ok := finiteNum(raw["ts"])
	okv, isBool := raw["ok"].(bool)
	if !ok || !isBool {
		return nil
	}
	msg, has := nestedString(raw["message"], 16000)
	if !has {
		msg = "Control request failed."
		if okv {
			msg = "Control request completed."
		}
	}
	return &nestedControlResult{Type: "subagent.nested.control-result", TS: ts, RootRunID: r.RootRunID, CapabilityToken: r.CapabilityToken,
		RequestID: raw["requestId"].(string), TargetRunID: raw["targetRunId"].(string), OK: okv, Message: msg}
}

func writeNestedControlRequest(r nestedRoute, req nestedControlRequest) (string, error) {
	if err := validateNestedRouteShape(r); err != nil {
		return "", err
	}
	if err := assertSafeNestedID("requestId", req.RequestID); err != nil {
		return "", err
	}
	if err := assertSafeNestedID("targetRunId", req.TargetRunID); err != nil {
		return "", err
	}
	req.Type, req.RootRunID, req.CapabilityToken = "subagent.nested.control-request", r.RootRunID, r.CapabilityToken
	b, _ := json.Marshal(req)
	parsed := parseNestedControlRequest(string(b), r)
	if parsed == nil {
		return "", errors.New("Nested control request failed validation.")
	}
	return writeNestedRouteRecord(r.ControlInbox, parsed.TS, parsed)
}

func readNestedControlRequests(r nestedRoute) ([]nestedControlRequest, error) {
	if err := validateNestedRouteShape(r); err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(r.ControlInbox)
	if err != nil && !isNotExist(err) {
		return nil, err
	}
	var names []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []nestedControlRequest
	for _, n := range names {
		content, ok := readNestedEventFile(r.ControlInbox, n)
		if !ok {
			continue
		}
		if req := parseNestedControlRequest(content, r); req != nil {
			req.FilePath = filepath.Join(r.ControlInbox, n)
			out = append(out, *req)
		}
	}
	return out, nil
}

func writeNestedControlResult(r nestedRoute, res nestedControlResult) error {
	if err := validateNestedRouteShape(r); err != nil {
		return err
	}
	if err := assertSafeNestedID("requestId", res.RequestID); err != nil {
		return err
	}
	if err := assertSafeNestedID("targetRunId", res.TargetRunID); err != nil {
		return err
	}
	res.Type, res.RootRunID, res.CapabilityToken = "subagent.nested.control-result", r.RootRunID, r.CapabilityToken
	b, _ := json.Marshal(res)
	parsed := parseNestedControlResult(string(b), r)
	if parsed == nil {
		return errors.New("Nested control result failed validation.")
	}
	_, err := writeNestedRouteRecord(r.EventSink, parsed.TS, parsed)
	return err
}

func readNestedControlResultsFromFile(r nestedRoute, entry string) []nestedControlResult {
	content, ok := readNestedEventFile(r.EventSink, entry)
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
		if res := parseNestedControlResult(l, r); res != nil {
			out = append(out, *res)
		}
	}
	return out
}

func snapshotNestedEventFiles(r nestedRoute) (map[string]bool, error) {
	if err := validateNestedRouteShape(r); err != nil {
		return nil, err
	}
	names, err := nestedEventEntries(r.EventSink, false)
	out := map[string]bool{}
	for _, n := range names {
		out[n] = true
	}
	return out, err
}

func findNestedControlResult(r nestedRoute, requestID, targetRunID string, ignored map[string]bool) *nestedControlResult {
	names, _ := nestedEventEntries(r.EventSink, true)
	for i := len(names) - 1; i >= 0; i-- {
		if ignored[names[i]] {
			continue
		}
		for _, res := range readNestedControlResultsFromFile(r, names[i]) {
			if res.RequestID == requestID && res.TargetRunID == targetRunID {
				return &res
			}
		}
	}
	return nil
}

func readNestedControlResults(r nestedRoute) []nestedControlResult {
	names, _ := nestedEventEntries(r.EventSink, true)
	var out []nestedControlResult
	for _, n := range names {
		out = append(out, readNestedControlResultsFromFile(r, n)...)
	}
	return out
}

// ── route lookup ──

func nestedRouteFromIndexEntry(rootRunID, filePath string) *nestedRoute {
	var meta struct {
		RootRunID       any `json:"rootRunId"`
		CapabilityToken any `json:"capabilityToken"`
		RouteRoot       any `json:"routeRoot"`
		CreatedAt       any `json:"createdAt"`
	}
	b, err := os.ReadFile(filePath)
	if err != nil || json.Unmarshal(b, &meta) != nil {
		return nil
	}
	token, ok1 := meta.CapabilityToken.(string)
	rr, ok2 := meta.RouteRoot.(string)
	if meta.RootRunID != rootRunID || !ok1 || !ok2 {
		return nil
	}
	routeRoot := filepath.Join(nestedEventsDir(), filepath.Base(rr))
	r := nestedRoute{RootRunID: rootRunID, EventSink: filepath.Join(routeRoot, "events"), ControlInbox: filepath.Join(routeRoot, "controls"), CapabilityToken: token}
	if validateNestedRouteShape(r) != nil {
		return nil
	}
	for _, d := range []string{r.EventSink, r.ControlInbox} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			return nil
		}
	}
	routeFilePath := filepath.Join(routeRoot, nestedRouteFile)
	rb, err := os.ReadFile(routeFilePath)
	if err == nil {
		var rf struct {
			RootRunID       any `json:"rootRunId"`
			CapabilityToken any `json:"capabilityToken"`
		}
		if json.Unmarshal(rb, &rf) != nil || rf.RootRunID != rootRunID || rf.CapabilityToken != token {
			return nil
		}
	} else if isNotExist(err) {
		created, ok := meta.CreatedAt.(float64)
		if !ok {
			created = float64(nowMs())
		}
		_ = asyncWriteJSON(routeFilePath, jm{"rootRunId": rootRunID, "capabilityToken": token, "createdAt": created})
	} else {
		return nil
	}
	return &r
}

func findNestedRouteForRootID(rootRunID string) (*nestedRoute, error) {
	if err := assertSafeNestedID("rootRunId", rootRunID); err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(nestedRouteIndexDirOf(rootRunID))
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, e := range ents {
		if r := nestedRouteFromIndexEntry(rootRunID, filepath.Join(nestedRouteIndexDirOf(rootRunID), e.Name())); r != nil {
			return r, nil
		}
	}
	return nil, nil
}

func listNestedRoutes() []nestedRoute {
	roots, err := os.ReadDir(nestedRouteIndexRoot())
	if err != nil {
		return nil
	}
	var routes []nestedRoute
	for _, re := range roots {
		rootRunID, err := url.PathUnescape(re.Name())
		if err != nil {
			continue
		}
		ents, err := os.ReadDir(filepath.Join(nestedRouteIndexRoot(), re.Name()))
		if err != nil {
			continue
		}
		for _, e := range ents {
			if r := nestedRouteFromIndexEntry(rootRunID, filepath.Join(nestedRouteIndexRoot(), re.Name(), e.Name())); r != nil {
				routes = append(routes, *r)
			}
		}
	}
	return routes
}

// buildNestedRouteIndex has one route per root run id (which of two duplicate directories wins is not specified).
func buildNestedRouteIndex() map[string]nestedRoute {
	idx := map[string]nestedRoute{}
	for _, r := range listNestedRoutes() {
		if _, ok := idx[r.RootRunID]; !ok {
			idx[r.RootRunID] = r
		}
	}
	return idx
}

func projectNestedRegistryForRoot(rootRunID string) (*nestedRegistry, error) {
	r, err := findNestedRouteForRootID(rootRunID)
	if err != nil || r == nil {
		return nil, err
	}
	reg, err := projectNestedEvents(*r)
	return &reg, err
}

func nestedStepChildren(run jm) []jm {
	var out []jm
	for _, s := range asJMList(run["steps"]) {
		out = append(out, asJMList(s["children"])...)
	}
	return out
}

func findNestedRun(children []jm, id string) jm {
	for _, c := range children {
		if c["id"] == id {
			return c
		}
		if n := findNestedRun(asJMList(c["children"]), id); n != nil {
			return n
		}
		if n := findNestedRun(nestedStepChildren(c), id); n != nil {
			return n
		}
	}
	return nil
}

type nestedRunMatch struct {
	RootRunID string
	Route     nestedRoute
	Run       jm
}

type nestedRunDescendantOf struct {
	ParentRunID     string
	ParentStepIndex *float64
}

type nestedRunResolutionScope struct {
	Routes       []nestedRoute
	HasRoutes    bool
	DescendantOf *nestedRunDescendantOf
}

func collectNestedRuns(children []jm, out []jm) []jm {
	for _, c := range children {
		out = append(out, c)
		out = collectNestedRuns(asJMList(c["children"]), out)
		out = collectNestedRuns(nestedStepChildren(c), out)
	}
	return out
}

func collectScopedNestedRuns(children []jm, scope *nestedRunDescendantOf, out []jm) []jm {
	if scope == nil {
		return collectNestedRuns(children, out)
	}
	for _, c := range children {
		if c["parentRunId"] == scope.ParentRunID && (scope.ParentStepIndex == nil || c["parentStepIndex"] == *scope.ParentStepIndex) {
			out = collectNestedRuns([]jm{c}, out)
			continue
		}
		out = collectScopedNestedRuns(asJMList(c["children"]), scope, out)
		out = collectScopedNestedRuns(nestedStepChildren(c), scope, out)
	}
	return out
}

func findNestedRunMatchesByID(id string, prefix bool, scope *nestedRunResolutionScope) ([]nestedRunMatch, error) {
	if err := assertSafeNestedID("id", id); err != nil {
		return nil, err
	}
	routes := listNestedRoutes()
	if scope != nil && scope.HasRoutes {
		routes = scope.Routes
	}
	var descendant *nestedRunDescendantOf
	if scope != nil {
		descendant = scope.DescendantOf
	}
	var matches []nestedRunMatch
	for _, r := range routes {
		reg, err := projectNestedEvents(r)
		if err != nil {
			continue
		}
		for _, run := range collectScopedNestedRuns(reg.Children, descendant, nil) {
			rid := strOf(run["id"])
			if (prefix && strings.HasPrefix(rid, id)) || (!prefix && rid == id) {
				matches = append(matches, nestedRunMatch{RootRunID: r.RootRunID, Route: r, Run: run})
			}
		}
	}
	return matches, nil
}

func findNestedRunByID(id string) (string, jm) {
	m, _ := findNestedRunMatchesByID(id, false, nil)
	if len(m) == 0 {
		return "", nil
	}
	return m[0].RootRunID, m[0].Run
}

// ── projection into parent state ──

// attachRootChildrenToSteps puts each root child under the step of its parent step index (a step's own index, else its position).
func attachRootChildrenToSteps(rootRunID string, steps []AsyncRunStepSummary, children []jm) {
	if len(steps) == 0 {
		return
	}
	for i := range steps {
		steps[i].Children = nil
	}
	for _, c := range children {
		idx, ok := c["parentStepIndex"].(float64)
		if c["parentRunId"] != rootRunID || !ok {
			continue
		}
		for i := range steps {
			if float64(steps[i].Index) != idx {
				continue
			}
			kept := []jm{}
			for _, e := range steps[i].Children {
				if e["id"] != c["id"] {
					kept = append(kept, e)
				}
			}
			steps[i].Children = capJM(append(kept, c), maxNestedChildren)
			break
		}
	}
}

func updateAsyncJobNestedProjection(job *AsyncJobState) error {
	if job.NestedRoute == nil {
		return nil
	}
	reg, err := projectNestedEvents(*job.NestedRoute)
	if err != nil {
		return err
	}
	job.NestedChildren = reg.Children
	attachRootChildrenToSteps(job.AsyncID, job.Steps, reg.Children)
	return nil
}

func updateForegroundNestedProjection(rec *ForegroundRunRecord) error {
	if rec.NestedRoute == nil {
		return nil
	}
	reg, err := projectNestedEvents(*rec.NestedRoute)
	if err != nil {
		return err
	}
	rec.NestedChildren = reg.Children
	return nil
}

func hasLiveNestedDescendants(children []jm) bool {
	for _, c := range children {
		if !nestedTerminal(c["state"]) || hasLiveNestedDescendants(asJMList(c["children"])) || hasLiveNestedDescendants(nestedStepChildren(c)) {
			return true
		}
	}
	return false
}

// nestedSummaryFromAsyncStatus is the registry summary of a nested run's async status.
func nestedSummaryFromAsyncStatus(st *AsyncStatus, asyncDir string, fb nestedSummaryFallback) jm {
	runID := firstNonEmpty(st.RunID, fb.ID)
	m := jm{"id": runID, "parentRunId": fb.ParentRunID, "depth": fb.Depth, "asyncDir": asyncDir}
	if fb.ParentStepIndex != nil {
		m["parentStepIndex"] = *fb.ParentStepIndex
	}
	if fb.Path != nil {
		m["path"] = fb.Path
	} else {
		e := jm{"runId": fb.ParentRunID}
		if fb.ParentStepIndex != nil {
			e["stepIndex"] = *fb.ParentStepIndex
		}
		m["path"] = []any{e}
	}
	if st.PID != nil && *st.PID != 0 {
		m["pid"] = float64(*st.PID)
	}
	if st.SessionID != "" {
		m["sessionId"] = st.SessionID
	}
	m["mode"] = firstNonEmpty(st.Mode, fb.Mode)
	if len(st.Steps) == 1 {
		if st.Steps[0].Model != "" {
			m["model"] = st.Steps[0].Model
		}
		if st.Steps[0].Thinking != "" {
			m["thinking"] = st.Steps[0].Thinking
		}
		if st.Steps[0].SessionName != "" {
			m["sessionName"] = st.Steps[0].SessionName
		}
	}
	if st.ProcessTerminal != nil {
		m["processTerminal"] = sanitizeProcessTerminal(st.ProcessTerminal, runID, st.ProcessTerminal.RunnerProcessInstanceID, asyncDir+"/status.json")
	}
	if a := st.Extra["launchResolvedExtensions"]; a != nil {
		var v any
		_ = json.Unmarshal(a, &v)
		m["launchResolvedExtensions"] = v
	}
	if a := st.Extra["runtimeAcknowledgedExtensions"]; a != nil {
		var v any
		_ = json.Unmarshal(a, &v)
		if s := sanitizeNestedRuntimeAck(v); s != nil {
			m["runtimeAcknowledgedExtensions"] = s
		}
	}
	m["state"] = st.State
	setIntPtr := func(k string, v *int) {
		if v != nil {
			m[k] = float64(*v)
		}
	}
	setMs := func(k string, v *ms) {
		if v != nil {
			m[k] = float64(*v)
		}
	}
	setIntPtr("currentStep", st.CurrentStep)
	setIntPtr("chainStepCount", st.ChainStepCount)
	if st.ActivityState != "" {
		m["activityState"] = st.ActivityState
	}
	setMs("lastActivityAt", st.LastActivityAt)
	if st.CurrentTool != "" {
		m["currentTool"] = st.CurrentTool
	}
	setMs("currentToolStartedAt", st.CurrentToolStartedAt)
	if st.CurrentPath != "" {
		m["currentPath"] = st.CurrentPath
	}
	setIntPtr("turnCount", st.TurnCount)
	setIntPtr("toolCount", st.ToolCount)
	if st.TotalTokens != nil {
		b, _ := json.Marshal(st.TotalTokens)
		var v any
		_ = json.Unmarshal(b, &v)
		m["totalTokens"] = v
	}
	setMs("timeoutMs", st.TimeoutMs)
	setMs("deadlineAt", st.DeadlineAt)
	if st.TimedOut != nil {
		m["timedOut"] = *st.TimedOut
	}
	if st.Stopped != nil {
		m["stopped"] = *st.Stopped
	}
	if st.Error != "" {
		m["error"] = st.Error
	}
	if st.StartedAt != 0 {
		m["startedAt"] = float64(st.StartedAt)
	} else {
		m["startedAt"] = fb.TS
	}
	setMs("endedAt", st.EndedAt)
	if st.LastUpdate != nil {
		m["lastUpdate"] = float64(*st.LastUpdate)
	} else {
		m["lastUpdate"] = fb.TS
	}
	if st.SessionFile != "" {
		m["sessionFile"] = st.SessionFile
	}
	if len(st.Steps) > 0 {
		steps := []jm{}
		for i, s := range st.Steps {
			if i == maxNestedSteps {
				break
			}
			sm := jm{"agent": s.Agent, "status": s.Status}
			if s.SessionName != "" {
				sm["sessionName"] = s.SessionName
			}
			if s.Model != "" {
				sm["model"] = s.Model
			}
			if s.Thinking != "" {
				sm["thinking"] = s.Thinking
			}
			if s.SessionFile != "" {
				sm["sessionFile"] = s.SessionFile
			}
			if s.ActivityState != "" {
				sm["activityState"] = s.ActivityState
			}
			if s.LastActivityAt != nil {
				sm["lastActivityAt"] = float64(*s.LastActivityAt)
			}
			if s.CurrentTool != "" {
				sm["currentTool"] = s.CurrentTool
			}
			if s.StartedAt != nil {
				sm["startedAt"] = float64(*s.StartedAt)
			}
			if s.EndedAt != nil {
				sm["endedAt"] = float64(*s.EndedAt)
			}
			if s.Error != "" {
				sm["error"] = s.Error
			}
			if a := s.Extra["runtimeAcknowledgedExtensions"]; a != nil {
				var v any
				_ = json.Unmarshal(a, &v)
				if san := sanitizeNestedRuntimeAck(v); san != nil {
					sm["runtimeAcknowledgedExtensions"] = san
				}
			}
			if s.ProcessTerminal != nil {
				sm["processTerminal"] = sanitizeProcessTerminal(s.ProcessTerminal, runID, s.ProcessTerminal.RunnerProcessInstanceID, fmt.Sprintf("%s/status.json step %d", asyncDir, i))
			}
			steps = append(steps, sm)
		}
		m["steps"] = steps
	}
	return m
}

type nestedSummaryFallback struct {
	ID              string
	ParentRunID     string
	ParentStepIndex *float64
	Depth           float64
	Path            []any
	Mode            string
	TS              float64
}

func nestedRunScopeChecked(rootRunID string) (asyncDirRoot, resultsDir string, err error) {
	if err = assertSafeNestedID("rootRunId", rootRunID); err != nil {
		return
	}
	asyncDirRoot, resultsDir = nestedRunScope(rootRunID)
	return
}

func nestedResultsPath(rootRunID, id string) (string, error) {
	if err := assertSafeNestedID("id", id); err != nil {
		return "", err
	}
	_, results, err := nestedRunScopeChecked(rootRunID)
	if err != nil {
		return "", err
	}
	return filepath.Join(results, id+".json"), nil
}
