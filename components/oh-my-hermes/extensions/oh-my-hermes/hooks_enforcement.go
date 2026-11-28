package ohmyhermes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/events"
)

const (
	repeatCallLoopThreshold = 8
	repeatCallIdleExpiry    = 300 * time.Second
)

func isPollingTool(toolName string) bool {
	name := strings.ToLower(strings.TrimSpace(toolName))
	return name == "process_manage" || strings.HasSuffix(name, "_poll") || strings.HasSuffix(name, "_get_result")
}

type repeatCallTracker struct {
	mu   sync.Mutex
	last map[string]repeatCallState
	now  func() time.Time
}

type repeatCallState struct {
	key            string
	count          int
	lastAt         time.Time
	lastResultHash string
	hasResult      bool
}

func newRepeatCallTracker() *repeatCallTracker {
	return &repeatCallTracker{
		last: make(map[string]repeatCallState),
		now:  time.Now,
	}
}

func (t *repeatCallTracker) record(sessionID, toolName string, input any) int {
	if isPollingTool(toolName) {
		return 0
	}
	key := toolName + ":" + canonicalInputHash(input)
	now := time.Now()
	if t.now != nil {
		now = t.now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.last[sessionID]
	if st.key == key && (st.lastAt.IsZero() || now.Sub(st.lastAt) <= repeatCallIdleExpiry) {
		st.count++
		st.lastAt = now
	} else {
		st = repeatCallState{key: key, count: 1, lastAt: now}
	}
	t.last[sessionID] = st
	return st.count
}

func (t *repeatCallTracker) recordResult(sessionID, toolName string, resultContent any) {
	if isPollingTool(toolName) || resultContent == nil {
		return
	}
	text := extractToolResultText(resultContent)
	if text == "" {
		return
	}
	hash := canonicalInputHash(text)
	now := time.Now()
	if t.now != nil {
		now = t.now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.last[sessionID]
	if !ok || !strings.HasPrefix(st.key, toolName+":") {
		return
	}
	if st.hasResult && st.lastResultHash != hash {
		st.count = 1
	}
	st.lastResultHash = hash
	st.hasResult = true
	st.lastAt = now
	t.last[sessionID] = st
}

func extractToolResultText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if txt, ok := m["text"].(string); ok {
					parts = append(parts, txt)
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	case []map[string]any:
		var parts []string
		for _, m := range v {
			if txt, ok := m["text"].(string); ok {
				parts = append(parts, txt)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return fmt.Sprintf("%v", content)
	}
	return string(raw)
}

func (t *repeatCallTracker) resetSession(sessionID string) {
	t.mu.Lock()
	delete(t.last, sessionID)
	t.mu.Unlock()
}

func canonicalInputHash(input any) string {
	raw, err := json.Marshal(input)
	if err != nil {
		raw = []byte(fmt.Sprintf("%v", input))
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// childSessionRegistry tracks active delegated child session IDs emitted over
// pig-hermes-core/events (ChannelSubagentStart / ChannelSubagentStop).
type childSessionRegistry struct {
	mu       sync.RWMutex
	sessions map[string]bool
}

func newChildSessionRegistry() *childSessionRegistry {
	return &childSessionRegistry{sessions: make(map[string]bool)}
}

func (r *childSessionRegistry) add(sessionID string) {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return
	}
	r.mu.Lock()
	r.sessions[id] = true
	r.mu.Unlock()
}

func (r *childSessionRegistry) remove(sessionID string) {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return
	}
	r.mu.Lock()
	delete(r.sessions, id)
	r.mu.Unlock()
}

func (r *childSessionRegistry) has(sessionID string) bool {
	id := strings.TrimSpace(sessionID)
	if id == "" || r == nil {
		return false
	}
	r.mu.RLock()
	ok := r.sessions[id]
	r.mu.RUnlock()
	return ok
}

func isWorkspaceMutatingTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "write_file", "patch":
		return true
	default:
		return false
	}
}

func (s *surfaceState) renderDAGWidgetLines() []string {
	runs, err := s.dags.List()
	if err != nil {
		return nil
	}
	var latestActive *DAGRun
	for _, r := range runs {
		if r.Status == "active" {
			if latestActive == nil || r.UpdatedAt >= latestActive.UpdatedAt {
				latestActive = r
			}
		}
	}
	if latestActive == nil {
		return nil
	}
	return renderDAGWidget(latestActive, s.router, s.currentTheme())
}

func refreshDAGNodeFromSubagentStop(dags *DAGStore, p events.SubagentStopPayload) {
	if dags == nil {
		return
	}
	taskID := strings.TrimSpace(p.TaskID)
	if taskID == "" {
		return
	}
	nodeStatus := "completed"
	if p.Status == "error" || p.Status == "failed" || p.Status == "cancelled" {
		nodeStatus = "failed"
	}
	resultMsg := ""
	if nodeStatus == "completed" {
		resultMsg = fmt.Sprintf("subagent %s completed (%dms)", p.ChildSessionID, p.DurationMs)
	}

	// 1. Direct "<run_id>:<node_id>" format.
	if runID, nodeID, ok := strings.Cut(taskID, ":"); ok && runID != "" && nodeID != "" {
		_, _ = dags.UpdateNode(runID, nodeID, nodeStatus, resultMsg, p.Error)
		return
	}

	// 2. Fallback: search active DAG runs for a node whose ID matches TaskID.
	runs, err := dags.List()
	if err != nil {
		return
	}
	for _, r := range runs {
		if r.Status != "active" {
			continue
		}
		for _, n := range r.Nodes {
			if n.ID == taskID {
				_, _ = dags.UpdateNode(r.RunID, n.ID, nodeStatus, resultMsg, p.Error)
				return
			}
		}
	}
}

func registerOrchestrationHooks(
	ext *sdk.Extension,
	surf *surfaceState,
	dags *DAGStore,
	todoMeta *todoMetaStore,
	children *childSessionRegistry,
) {
	if children == nil {
		children = newChildSessionRegistry()
	}
	surf.children = children
	loopTracker := newRepeatCallTracker()

	// Track delegated child sessions and latch DAG node status on subagent stop.
	_, _ = ext.Events().On(events.ChannelSubagentStart, func(ctx sdk.Context, data any) error {
		var payload events.SubagentStartPayload
		if raw, err := json.Marshal(data); err == nil {
			if json.Unmarshal(raw, &payload) == nil {
				children.add(payload.ChildSessionID)
			}
		}
		return nil
	})
	_, _ = ext.Events().On(events.ChannelSubagentStop, func(ctx sdk.Context, data any) error {
		var payload events.SubagentStopPayload
		if raw, err := json.Marshal(data); err == nil {
			if json.Unmarshal(raw, &payload) == nil {
				children.remove(payload.ChildSessionID)
				refreshDAGNodeFromSubagentStop(dags, payload)
			}
		}
		return nil
	})

	// Clear per-session repeat-call tracker on session shutdown.
	ext.OnEvent(sdk.EventSessionShutdown, func(ctx sdk.Context, data map[string]any) (any, error) {
		loopTracker.resetSession(resolveSessionID(ctx))
		return nil, nil
	})

	// Live EventToolCall enforcement: plan-stage edit guard + repeat-call loop breaker.
	ext.OnEvent(sdk.EventToolCall, func(ctx sdk.Context, data map[string]any) (any, error) {
		toolName, _ := data["toolName"].(string)
		sessionID := resolveSessionID(ctx)

		// 1. Plan-stage edit guard.
		if todoMeta != nil && isWorkspaceMutatingTool(toolName) {
			meta := todoMeta.get(sessionID)
			if strings.EqualFold(strings.TrimSpace(meta.PlanStage), "awaiting_acceptance") {
				return map[string]any{
					"block":  true,
					"reason": "OMH plan_stage is awaiting_acceptance; obtain user plan approval or advance plan_stage before mutating workspace files",
				}, nil
			}
		}

		// 2. Repeat-call loop breaker (exempt polling tools).
		if toolName != "" && !isPollingTool(toolName) {
			count := loopTracker.record(sessionID, toolName, data["input"])
			if count >= repeatCallLoopThreshold {
				return map[string]any{
					"block": true,
					"reason": fmt.Sprintf(
						"OMH loop breaker: tool %q was called %d consecutive times with identical arguments without progress. Change strategy, inspect state, or ask the user instead of repeating the same call.",
						toolName, count,
					),
				}, nil
			}
		}

		return nil, nil
	})

	// Live EventToolResult UI refresh for orchestration tools.
	ext.OnEvent(sdk.EventToolResult, func(ctx sdk.Context, data map[string]any) (any, error) {
		toolName, _ := data["toolName"].(string)
		if toolName != "" {
			if resPayload, ok := data["content"]; ok {
				loopTracker.recordResult(resolveSessionID(ctx), toolName, resPayload)
			} else if resPayload, ok := data["result"]; ok {
				loopTracker.recordResult(resolveSessionID(ctx), toolName, resPayload)
			}
		}
		switch toolName {
		case "omh_dag", "omh_team", "omh_loop":
			ctx.SetStatus("omh", surf.statusText())
			if ctx.HasUI() {
				if lines := surf.renderDAGWidgetLines(); len(lines) > 0 {
					_ = ctx.SetWidget(omhDAGWidgetKey, lines, sdk.WidgetOptions{"placement": "belowEditor"})
				} else {
					_ = ctx.SetWidget(omhDAGWidgetKey, nil)
				}
			}
		}
		return nil, nil
	})
}
