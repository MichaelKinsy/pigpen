package readashboard

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// toolPrefix is how REA's MCP tools reach the model and the tool_execution_* events: mcp__<server>__<tool>.
const toolPrefix = "mcp__rea__"

// entryType is the custom session entry that persists one finished REA call.
const entryType = "rea.call"

// maxCalls bounds the timeline; the oldest finished calls fall off first.
const maxCalls = 500

type callStatus int

const (
	statusRunning callStatus = iota
	statusOK
	statusError
)

// call is one REA tool call, live or restored from a persisted entry.
type call struct {
	ID       string
	Parent   string // the calling tool's call id: set for a call made inside a codemode script
	Tool     string // the REA tool name without the mcp__rea__ prefix
	Args     string // bounded one-line summary of the arguments
	Target   string
	Status   callStatus
	Started  time.Time
	Duration time.Duration // set once finished
	Restored bool          // read back from a persisted entry

	// Counted is true when the result carried evidence or limitations, so the counts mean something.
	Counted          bool
	Evidence         int
	Limitations      int
	EvidenceSample   []string
	LimitationSample []string
	Error            string
}

func (c call) nested() bool { return c.Parent != "" }

// family is the first word of the tool name: inspect_artifact is in family inspect.
func family(tool string) string {
	family, _, _ := strings.Cut(tool, "_")
	return family
}

// callEntry is the bounded summary AppendEntry stores. It never holds a full result.
type callEntry struct {
	V                int      `json:"v"`
	ID               string   `json:"id"`
	Parent           string   `json:"parent,omitempty"`
	Tool             string   `json:"tool"`
	Args             string   `json:"args,omitempty"`
	Target           string   `json:"target,omitempty"`
	OK               bool     `json:"ok"`
	DurationMs       int64    `json:"ms"`
	Started          string   `json:"at,omitempty"`
	Evidence         *int     `json:"evidence,omitempty"`
	Limitations      *int     `json:"limitations,omitempty"`
	EvidenceSample   []string `json:"evidenceSample,omitempty"`
	LimitationSample []string `json:"limitationSample,omitempty"`
	Error            string   `json:"error,omitempty"`
}

const (
	maxArgsLen   = 240
	maxTargetLen = 300
	maxSampleLen = 160
	maxSamples   = 3
	maxErrorLen  = 300
)

func (c call) entry() callEntry {
	e := callEntry{
		V: 1, ID: c.ID, Parent: c.Parent, Tool: c.Tool,
		Args: short(c.Args, maxArgsLen), Target: short(c.Target, maxTargetLen),
		OK: c.Status == statusOK, DurationMs: c.Duration.Milliseconds(),
		EvidenceSample: c.EvidenceSample, LimitationSample: c.LimitationSample,
		Error: short(c.Error, maxErrorLen),
	}
	if !c.Started.IsZero() {
		e.Started = c.Started.UTC().Format(time.RFC3339)
	}
	if c.Counted {
		evidence, limitations := c.Evidence, c.Limitations
		e.Evidence, e.Limitations = &evidence, &limitations
	}
	return e
}

func (e callEntry) call() call {
	c := call{
		ID: e.ID, Parent: e.Parent, Tool: e.Tool, Args: e.Args, Target: e.Target,
		Status: statusError, Duration: time.Duration(e.DurationMs) * time.Millisecond, Restored: true,
		EvidenceSample: e.EvidenceSample, LimitationSample: e.LimitationSample, Error: e.Error,
	}
	if e.OK {
		c.Status = statusOK
	}
	if t, err := time.Parse(time.RFC3339, e.Started); err == nil {
		c.Started = t
	}
	if e.Evidence != nil || e.Limitations != nil {
		c.Counted = true
		if e.Evidence != nil {
			c.Evidence = *e.Evidence
		}
		if e.Limitations != nil {
			c.Limitations = *e.Limitations
		}
	}
	return c
}

// store is the timeline: calls in start order, safe for concurrent use.
type store struct {
	mu    sync.Mutex
	calls []*call
	byID  map[string]*call
}

func newStore() *store { return &store{byID: map[string]*call{}} }

// toolEvent is the part of a tool_execution_* event the store reads.
type toolEvent struct {
	ID, Parent, Tool string
	Args             map[string]any
	Result           any
	IsError          bool
	DurationMs       *int64
}

// isREA reports whether name is one of REA's tools.
func isREA(name string) bool {
	return strings.HasPrefix(name, toolPrefix) && len(name) > len(toolPrefix)
}

// decodeToolEvent reads an event payload. ok is false for a call that is not a REA tool call.
func decodeToolEvent(data map[string]any) (toolEvent, bool) {
	name, _ := data["toolName"].(string)
	if !isREA(name) {
		return toolEvent{}, false
	}
	ev := toolEvent{Tool: strings.TrimPrefix(name, toolPrefix)}
	ev.ID, _ = data["toolCallId"].(string)
	ev.Parent, _ = data["parentToolCallId"].(string)
	ev.Args, _ = data["args"].(map[string]any)
	ev.IsError, _ = data["isError"].(bool)
	ev.Result = data["result"]
	if ms, ok := data["durationMs"].(float64); ok && ms >= 0 {
		v := int64(ms)
		ev.DurationMs = &v
	}
	return ev, ev.ID != ""
}

// start records a call that began, or refreshes one already known.
func (s *store) start(ev toolEvent, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure(ev, now)
}

// update marks progress on a running call; an update for an unknown call starts it.
func (s *store) update(ev toolEvent, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure(ev, now)
}

func (s *store) ensure(ev toolEvent, now time.Time) *call {
	c := s.byID[ev.ID]
	if c == nil {
		c = &call{ID: ev.ID, Parent: ev.Parent, Tool: ev.Tool, Started: now, Status: statusRunning}
		s.byID[ev.ID] = c
		s.calls = append(s.calls, c)
		s.trim()
	}
	if c.Args == "" && len(ev.Args) > 0 {
		c.Args = summarizeArgs(ev.Args)
		c.Target = extractTarget(ev.Args)
	}
	return c
}

// end finishes a call and returns the entry to persist.
func (s *store) end(ev toolEvent, now time.Time) callEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	started := now
	if ev.DurationMs != nil {
		started = now.Add(-time.Duration(*ev.DurationMs) * time.Millisecond)
	}
	c := s.byID[ev.ID]
	if c == nil {
		c = s.ensure(ev, started)
		c.Started = started
	}
	if ev.DurationMs != nil {
		c.Duration = time.Duration(*ev.DurationMs) * time.Millisecond
	} else {
		c.Duration = max(now.Sub(c.Started), 0)
	}
	c.Status = statusOK
	if ev.IsError {
		c.Status = statusError
	}
	sum := summarizeResult(ev.Result)
	c.Counted, c.Evidence, c.Limitations = sum.Counted, sum.Evidence, sum.Limitations
	c.EvidenceSample, c.LimitationSample = sum.EvidenceSample, sum.LimitationSample
	c.Error = ""
	if c.Status == statusError {
		c.Error = sum.Error
	}
	return c.entry()
}

// trim drops the oldest finished calls beyond maxCalls. The caller holds the lock.
func (s *store) trim() {
	for len(s.calls) > maxCalls {
		drop := -1
		for i, c := range s.calls {
			if c.Status != statusRunning {
				drop = i
				break
			}
		}
		if drop < 0 {
			return
		}
		delete(s.byID, s.calls[drop].ID)
		s.calls = append(s.calls[:drop], s.calls[drop+1:]...)
	}
}

// restore replaces the finished calls with those the session entries persisted, in session order. A call
// still running in this process stays. keep, when not nil, filters entries by session entry id (the current
// branch).
func (s *store) restore(entries []json.RawMessage, keep func(entryID string) bool) {
	var restored []*call
	seen := map[string]bool{}
	for _, raw := range entries {
		var env struct {
			ID         string          `json:"id"`
			Type       string          `json:"type"`
			CustomType string          `json:"customType"`
			Data       json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &env) != nil || env.Type != "custom" || env.CustomType != entryType {
			continue
		}
		if keep != nil && !keep(env.ID) {
			continue
		}
		var e callEntry
		if json.Unmarshal(env.Data, &e) != nil || e.ID == "" || e.Tool == "" || seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		c := e.call()
		restored = append(restored, &c)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var live []*call
	for _, c := range s.calls {
		if c.Status == statusRunning && !seen[c.ID] {
			live = append(live, c)
		}
	}
	s.calls = append(restored, live...)
	s.byID = make(map[string]*call, len(s.calls))
	for _, c := range s.calls {
		s.byID[c.ID] = c
	}
	s.trim()
}

// snapshot copies the timeline.
func (s *store) snapshot() []call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]call, len(s.calls))
	for i, c := range s.calls {
		out[i] = *c
	}
	return out
}

// targetKeys are the argument names that hold what a REA tool works on, in priority order.
var targetKeys = []string{
	"path", "file", "file_path", "input_path", "target", "application", "app", "package", "url", "source",
	"binary", "executable", "apk", "document", "cdp_endpoint", "left", "origin",
}

// extractTarget finds the best target in a call's arguments: a named field first, then any string that looks
// like a path or URL.
func extractTarget(args map[string]any) string {
	for _, k := range targetKeys {
		if v, ok := args[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v, ok := args[k].(string); ok && looksLikeTarget(v) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func looksLikeTarget(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || strings.ContainsAny(v, "\n\r") {
		return false
	}
	return strings.HasPrefix(v, "/") || strings.HasPrefix(v, "~/") || strings.Contains(v, "://") ||
		len(v) > 2 && v[1] == ':' && (v[2] == '\\' || v[2] == '/')
}

// summarizeArgs renders arguments as one bounded line: the target-like fields first, then the rest by name.
func summarizeArgs(args map[string]any) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	priority := func(k string) int {
		for i, t := range targetKeys {
			if k == t {
				return i
			}
		}
		return len(targetKeys)
	}
	sort.Slice(keys, func(i, j int) bool {
		if pi, pj := priority(keys[i]), priority(keys[j]); pi != pj {
			return pi < pj
		}
		return keys[i] < keys[j]
	})
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+argValue(args[k]))
	}
	return short(strings.Join(parts, " "), maxArgsLen)
}

func argValue(v any) string {
	switch v := v.(type) {
	case string:
		return short(v, 80)
	case nil:
		return "null"
	case bool, float64:
		return fmt.Sprint(v)
	case []any:
		return fmt.Sprintf("[%d items]", len(v))
	case map[string]any:
		return fmt.Sprintf("{%d keys}", len(v))
	}
	return "…"
}

// resultSummary is what the dashboard keeps of a call's result.
type resultSummary struct {
	Counted          bool
	Evidence         int
	Limitations      int
	EvidenceSample   []string
	LimitationSample []string
	Error            string
}

// summarizeResult reads REA's evidence and limitations out of a tool result: the structuredContent when the
// event carries it, else the JSON text of the first content block. A result that is not that JSON (for
// example one the host cut in the middle) has no counts.
func summarizeResult(result any) resultSummary {
	var sum resultSummary
	res, _ := result.(map[string]any)
	if res == nil {
		return sum
	}
	text := resultText(res)
	sum.Error = short(firstLine(text), maxErrorLen)
	doc, _ := res["structuredContent"].(map[string]any)
	if doc == nil {
		_ = json.Unmarshal([]byte(strings.TrimSpace(text)), &doc)
	}
	if inner, ok := doc["result"].(map[string]any); ok {
		doc = inner
	}
	if doc == nil {
		return sum
	}
	if msg := errorMessage(doc["error"]); msg != "" {
		sum.Error = short(msg, maxErrorLen)
	}
	switch v := doc["evidence"].(type) {
	case []any:
		sum.Counted, sum.Evidence = true, len(v)
		for _, item := range v {
			sum.EvidenceSample = appendSample(sum.EvidenceSample, evidenceLabel(item))
		}
	case map[string]any:
		sum.Counted, sum.Evidence = true, 1
		sum.EvidenceSample = appendSample(sum.EvidenceSample, evidenceLabel(v))
	default:
		if id, ok := doc["evidence_id"].(string); ok && id != "" {
			sum.Counted, sum.Evidence = true, 1
			sum.EvidenceSample = appendSample(nil, id)
		}
	}
	switch v := doc["limitations"].(type) {
	case []any:
		sum.Counted, sum.Limitations = true, len(v)
		for _, item := range v {
			sum.LimitationSample = appendSample(sum.LimitationSample, limitationLabel(item))
		}
	case string:
		if v != "" {
			sum.Counted, sum.Limitations = true, 1
			sum.LimitationSample = appendSample(nil, v)
		}
	}
	return sum
}

func appendSample(samples []string, s string) []string {
	if s == "" || len(samples) >= maxSamples {
		return samples
	}
	return append(samples, short(s, maxSampleLen))
}

func evidenceLabel(item any) string {
	switch v := item.(type) {
	case string:
		return v
	case map[string]any:
		return firstString(v, "evidence_id", "id", "summary", "title", "claim", "kind")
	}
	return ""
}

func limitationLabel(item any) string {
	switch v := item.(type) {
	case string:
		return v
	case map[string]any:
		return firstString(v, "message", "detail", "description", "summary", "code")
	}
	return ""
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func errorMessage(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case map[string]any:
		return firstString(v, "message", "code")
	}
	return ""
}

// resultText joins the text content blocks of a tool result.
func resultText(res map[string]any) string {
	blocks, _ := res["content"].([]any)
	var parts []string
	for _, b := range blocks {
		block, _ := b.(map[string]any)
		if block == nil {
			continue
		}
		if text, ok := block["text"].(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// target is a binary, app or URL that REA calls touched.
type target struct {
	Value string
	Count int
	Last  time.Time
	Tools []string
}

// local reports whether the target is a local absolute path, the only kind a quick inspect can take.
func (t target) local() bool { return localPath(t.Value) }

// targets lists the distinct targets of calls, most recently used first.
func targets(calls []call) []target {
	index := map[string]int{}
	var out []target
	for i, c := range calls {
		if c.Target == "" {
			continue
		}
		at := c.Started
		if at.IsZero() {
			at = time.Unix(int64(i), 0)
		}
		j, ok := index[c.Target]
		if !ok {
			j = len(out)
			index[c.Target] = j
			out = append(out, target{Value: c.Target})
		}
		t := &out[j]
		t.Count++
		if !at.Before(t.Last) {
			t.Last = at
		}
		seen := false
		for _, tool := range t.Tools {
			seen = seen || tool == c.Tool
		}
		if !seen && len(t.Tools) < 6 {
			t.Tools = append(t.Tools, c.Tool)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}

// counts tallies the timeline.
func counts(calls []call) (running, ok, failed int) {
	for _, c := range calls {
		switch c.Status {
		case statusRunning:
			running++
		case statusOK:
			ok++
		default:
			failed++
		}
	}
	return
}
