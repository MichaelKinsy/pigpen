package agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

const hubPublishCoalesceWindow = 60 * time.Millisecond

type hubBinding struct {
	sessionID    string
	sessionStack []string
	unsub        func()
	timer        *time.Timer
	closed       bool
}

var (
	hubMu        sync.Mutex
	hubPublishMu sync.Mutex
	hubBindings  = map[*Runtime]*hubBinding{}
)

// hubStatusRank matches omp's STATUS_ORDER in agent-hub-projection.ts:
// running (0) < idle (1) < parked (2) < aborted (3).
func hubStatusRank(status string) int {
	switch status {
	case "running":
		return 0
	case "idle":
		return 1
	case "parked":
		return 2
	case "aborted":
		return 3
	default:
		return 4
	}
}

// BuildHubSnapshot constructs an immutable surface.AgentHubSnapshot for all
// subagents (`Kind == "sub"`) in sessionID, ordered running-first then newest
// activity first. When no subagents exist in sessionID, returns an empty
// snapshot so the frontend HUD pill stays hidden.
func (rt *Runtime) BuildHubSnapshot(sessionID string) surface.AgentHubSnapshot {
	if rt == nil || rt.Registry == nil {
		return surface.AgentHubSnapshot{}
	}
	refs := rt.Registry.ListInSessionTree(sessionID)

	type rowCandidate struct {
		item      surface.AgentHubItem
		insertSeq uint64
	}
	candidates := make([]rowCandidate, 0, len(refs))
	var snap surface.AgentHubSnapshot

	for _, ref := range refs {
		if ref == nil || ref.Kind != "sub" {
			continue
		}
		ref.mu.Lock()
		displayName := ref.DisplayName
		if displayName == "" {
			displayName = ref.AgentType
		}
		if displayName == "" {
			displayName = ref.ID
		}
		var startedAtMs int64
		if ref.Status == "running" && !ref.TurnStartedAt.IsZero() {
			startedAtMs = ref.TurnStartedAt.UnixMilli()
		}
		var createdAtMs int64
		if !ref.CreatedAt.IsZero() {
			createdAtMs = ref.CreatedAt.UnixMilli()
		}
		var lastActiveMs int64
		if !ref.LastActive.IsZero() {
			lastActiveMs = ref.LastActive.UnixMilli()
		}
		canRevive := (ref.Status == "parked" || ref.Status == "aborted") &&
			ref.OnRevive != nil && !ref.reviving && !ref.runActive
		item := surface.AgentHubItem{
			ID:            ref.ID,
			DisplayName:   displayName,
			AgentType:     ref.AgentType,
			ParentID:      ref.ParentID,
			Status:        ref.Status,
			Task:          ref.Task,
			Activity:      ref.Activity,
			Model:         ref.Model,
			ModelRole:     ref.ModelRole,
			ThinkingLevel: ref.ThinkingLevel,
			Cost:          ref.Cost,
			DurationMs:    ref.DurationMs,
			StartedAtMs:   startedAtMs,
			Requests:      ref.Requests,
			Tools:         ref.Tools,
			Tokens:        ref.Tokens,
			ContextTokens: ref.ContextTokens,
			ContextWindow: ref.ContextWindow,
			CreatedAtMs:   createdAtMs,
			LastActiveMs:  lastActiveMs,
			SessionFile:   ref.SessionFile,
			CanRevive:     canRevive,
		}
		seq := ref.insertSeq
		ref.mu.Unlock()

		snap.InspectableCount++
		if item.Status == "running" {
			snap.RunningCount++
		}
		snap.SubagentCost += item.Cost
		snap.TotalDurationMs += item.DurationMs
		snap.TotalRequests += item.Requests
		snap.TotalTokens += item.Tokens
		snap.TotalTools += item.Tools
		candidates = append(candidates, rowCandidate{item: item, insertSeq: seq})
	}

	if snap.InspectableCount == 0 {
		return surface.AgentHubSnapshot{}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		ri, rj := hubStatusRank(candidates[i].item.Status), hubStatusRank(candidates[j].item.Status)
		if ri != rj {
			return ri < rj
		}
		if candidates[i].item.LastActiveMs != candidates[j].item.LastActiveMs {
			return candidates[i].item.LastActiveMs > candidates[j].item.LastActiveMs
		}
		if candidates[i].item.CreatedAtMs != candidates[j].item.CreatedAtMs {
			return candidates[i].item.CreatedAtMs > candidates[j].item.CreatedAtMs
		}
		if candidates[i].insertSeq != candidates[j].insertSeq {
			return candidates[i].insertSeq > candidates[j].insertSeq
		}
		return candidates[i].item.ID < candidates[j].item.ID
	})

	snap.Agents = make([]surface.AgentHubItem, len(candidates))
	for i, c := range candidates {
		snap.Agents[i] = c.item
	}
	snap.Activity = rt.Registry.ListActivities(sessionID)
	return snap
}

// PublishHub builds and publishes sessionID's AgentHubSnapshot to surface.
// Serialized by hubPublishMu so concurrent builds cannot publish stale
// snapshots out of order or resurrect a cleared hub.
func (rt *Runtime) PublishHub(sessionID string) {
	if rt == nil {
		return
	}
	hubPublishMu.Lock()
	defer hubPublishMu.Unlock()
	hubMu.Lock()
	b, exists := hubBindings[rt]
	if exists && b != nil && b.sessionID != "" && b.sessionID != sessionID {
		hubMu.Unlock()
		return
	}
	hubMu.Unlock()
	surface.SetAgentHubSnapshot(rt.BuildHubSnapshot(sessionID))
}

func (rt *Runtime) scheduleHubPublish(immediate bool) {
	if rt == nil {
		return
	}
	if immediate {
		hubMu.Lock()
		b := hubBindings[rt]
		var activeSess string
		if b != nil && !b.closed {
			if b.timer != nil {
				b.timer.Stop()
				b.timer = nil
			}
			activeSess = b.sessionID
		}
		hubMu.Unlock()
		if activeSess != "" {
			rt.PublishHub(activeSess)
		}
		return
	}

	hubMu.Lock()
	b := hubBindings[rt]
	if b == nil || b.closed || b.sessionID == "" {
		hubMu.Unlock()
		return
	}
	if b.timer != nil {
		hubMu.Unlock()
		return
	}
	b.timer = time.AfterFunc(hubPublishCoalesceWindow, func() {
		hubMu.Lock()
		cur := hubBindings[rt]
		var activeSess string
		if cur != nil && !cur.closed {
			cur.timer = nil
			activeSess = cur.sessionID
		}
		hubMu.Unlock()
		if activeSess != "" {
			rt.PublishHub(activeSess)
		}
	})
	hubMu.Unlock()
}

func (rt *Runtime) installHubHandlers(sessionID string) {
	surface.SetAgentHubHandlers(surface.AgentHubHandlers{
		LoadTranscript: func(agentID string) []surface.AgentHubTranscriptEntry {
			return rt.LoadTranscriptEntries(sessionID, agentID)
		},
		Kill: func(agentID string) (string, error) {
			return rt.KillHubAgent(sessionID, agentID)
		},
		Revive: func(agentID string) (string, error) {
			return rt.ReviveAgent(sessionID, agentID)
		},
		SendMessage: func(agentID, body string) error {
			return rt.SendHubMessage(sessionID, agentID, body)
		},
	})
}

// BindHubSession binds sessionID as the active top-level Agent Hub session,
// subscribes to Registry hub changes to keep surface.AgentHubSnapshot current,
// registers surface.AgentHubHandlers, and publishes the initial snapshot.
func (rt *Runtime) BindHubSession(sessionID string) {
	if rt == nil || rt.Registry == nil {
		return
	}
	hubMu.Lock()
	b, ok := hubBindings[rt]
	if !ok {
		b = &hubBinding{sessionID: sessionID}
		hubBindings[rt] = b
		b.unsub = rt.Registry.OnHubChange(func(immediate bool) {
			rt.scheduleHubPublish(immediate)
		})
	} else {
		if b.sessionID != "" && b.sessionID != sessionID {
			b.sessionStack = append(b.sessionStack, b.sessionID)
		}
		b.sessionID = sessionID
	}
	hubMu.Unlock()

	rt.installHubHandlers(sessionID)
	rt.PublishHub(sessionID)
}

// ClearHubIfActive clears the published Agent Hub snapshot when the active
// top-level session shuts down, or restores the previous bound session if one
// was stacked.
func (rt *Runtime) ClearHubIfActive(sessionID string) {
	if rt == nil || sessionID == "" {
		return
	}
	hubPublishMu.Lock()
	defer hubPublishMu.Unlock()

	hubMu.Lock()
	b := hubBindings[rt]
	if b == nil {
		hubMu.Unlock()
		return
	}
	// Remove sessionID from stack if it was a backgrounded session.
	if b.sessionID != sessionID {
		kept := b.sessionStack[:0]
		for _, s := range b.sessionStack {
			if s != sessionID {
				kept = append(kept, s)
			}
		}
		b.sessionStack = kept
		hubMu.Unlock()
		return
	}
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	if len(b.sessionStack) > 0 {
		prev := b.sessionStack[len(b.sessionStack)-1]
		b.sessionStack = b.sessionStack[:len(b.sessionStack)-1]
		b.sessionID = prev
		hubMu.Unlock()
		rt.installHubHandlers(prev)
		surface.SetAgentHubSnapshot(rt.BuildHubSnapshot(prev))
		return
	}
	b.closed = true
	unsub := b.unsub
	delete(hubBindings, rt)
	hubMu.Unlock()

	if unsub != nil {
		unsub()
	}
	surface.SetAgentHubSnapshot(surface.AgentHubSnapshot{})
	surface.SetAgentHubHandlers(surface.AgentHubHandlers{})
}

func resetHubBindingsForTests() {
	hubPublishMu.Lock()
	defer hubPublishMu.Unlock()
	hubMu.Lock()
	for rt, b := range hubBindings {
		if b != nil {
			b.closed = true
			if b.timer != nil {
				b.timer.Stop()
				b.timer = nil
			}
			if b.unsub != nil {
				b.unsub()
			}
		}
		delete(hubBindings, rt)
	}
	hubMu.Unlock()
}

// KillHubAgent kills a subagent anywhere in topSessionID's session tree and
// cancels any matching background agent job so queued or starting agents abort
// immediately.
func (rt *Runtime) KillHubAgent(topSessionID, agentID string) (string, error) {
	if rt == nil || rt.Registry == nil {
		return "", errors.New("agent runtime unavailable")
	}
	ref, ok := rt.Registry.LookupInSessionTree(topSessionID, agentID)
	targetSession := topSessionID
	targetID := agentID
	if ok && ref != nil {
		if ref.Kind != "sub" {
			return "", fmt.Errorf("agent %s is not a subagent", agentID)
		}
		targetSession = ref.SessionID
		targetID = ref.ID
	}
	status, msg := rt.Registry.KillAgent(targetSession, targetID)
	// Also cancel any matching background agent job (queued or running) so
	// semaphore-waiting or starting subagents are aborted immediately.
	if j, found := jobs.Get(targetSession, targetID); found && j != nil && j.Kind() == jobs.KindAgent {
		j.Cancel()
		if status == "not_found" {
			status = "cancelled"
			msg = fmt.Sprintf("Cancelled background agent job %s.", targetID)
		}
	}
	rt.PublishHub(topSessionID)
	if status == "not_found" {
		return "", errors.New(msg)
	}
	return msg, nil
}

// ReviveAgent revives a parked or aborted subagent in topSessionID's session
// tree if CanRevive().
func (rt *Runtime) ReviveAgent(topSessionID, agentID string) (string, error) {
	if rt == nil || rt.Registry == nil {
		return "", errors.New("agent runtime unavailable")
	}
	ref, ok := rt.Registry.LookupInSessionTree(topSessionID, agentID)
	if !ok || ref == nil || ref.Kind != "sub" {
		return "", fmt.Errorf("agent %s not found", agentID)
	}
	ref.mu.Lock()
	onRevive := ref.OnRevive
	st := ref.Status
	reviving := ref.reviving
	runActive := ref.runActive
	refID := ref.ID
	ref.mu.Unlock()

	if reviving {
		return "", fmt.Errorf("agent %s is already reviving", refID)
	}
	if runActive {
		return "", fmt.Errorf("agent %s is still shutting down", refID)
	}
	if st != "parked" && st != "aborted" {
		return "", fmt.Errorf("agent %s cannot be revived (status: %s)", refID, st)
	}
	if onRevive == nil {
		return "", fmt.Errorf("agent %s cannot be revived (no revive handler)", refID)
	}
	msg, err := onRevive()
	rt.PublishHub(topSessionID)
	return msg, err
}

// SendHubMessage delivers an IRC/steer message from Main to agentID in
// topSessionID's session tree. If the target subagent is parked or aborted and
// revivable, it is revived first before delivering the message.
func (rt *Runtime) SendHubMessage(topSessionID, agentID, body string) error {
	if rt == nil || rt.Registry == nil {
		return errors.New("agent runtime unavailable")
	}
	ref, ok := rt.Registry.LookupInSessionTree(topSessionID, agentID)
	targetSession := topSessionID
	targetID := agentID
	if ok && ref != nil && ref.Kind == "sub" {
		targetSession = ref.SessionID
		targetID = ref.ID
		if ref.CanRevive() {
			if _, err := rt.ReviveAgent(topSessionID, targetID); err != nil {
				return fmt.Errorf("failed to revive %s before messaging: %w", targetID, err)
			}
		}
	}
	text, _, isErr := rt.Registry.SendMessageInSession(targetSession, MainAgentID, targetID, body, false, "", false)
	rt.PublishHub(topSessionID)
	if isErr {
		return errors.New(text)
	}
	return nil
}

// LoadTranscriptEntries loads and converts agentID's transcript messages into
// structured AgentHubTranscriptEntry items for the Agent Hub transcript viewer.
func (rt *Runtime) LoadTranscriptEntries(topSessionID, agentID string) []surface.AgentHubTranscriptEntry {
	if rt == nil || rt.Registry == nil || strings.TrimSpace(agentID) == "" {
		return nil
	}
	ref, ok := rt.Registry.LookupInSessionTree(topSessionID, strings.TrimSpace(agentID))
	if !ok || ref == nil || ref.Kind != "sub" {
		return nil
	}
	msgs, _ := ref.SnapshotMessages()
	return convertMessagesToTranscriptEntries(msgs)
}

func convertMessagesToTranscriptEntries(msgs []map[string]any) []surface.AgentHubTranscriptEntry {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]surface.AgentHubTranscriptEntry, 0, len(msgs))
	toolIndexByCallID := map[string]int{}

	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		role, _ := msg["role"].(string)
		ts := extractMessageTimestampMs(msg)

		switch role {
		case "user":
			text := extractPlainContent(msg["content"])
			if strings.TrimSpace(text) != "" {
				out = append(out, surface.AgentHubTranscriptEntry{
					Role:        "user",
					Text:        text,
					TimestampMs: ts,
				})
			}

		case "assistant":
			switch c := msg["content"].(type) {
			case string:
				if strings.TrimSpace(c) != "" {
					out = append(out, surface.AgentHubTranscriptEntry{
						Role:        "assistant",
						Text:        c,
						TimestampMs: ts,
					})
				}
			case []any:
				var textParts []string
				var thinkParts []string
				type callSpec struct {
					id   string
					name string
					args string
				}
				var calls []callSpec
				for _, raw := range c {
					blk, ok := raw.(map[string]any)
					if !ok {
						continue
					}
					bType, _ := blk["type"].(string)
					switch bType {
					case "text":
						if t, ok := blk["text"].(string); ok && t != "" {
							textParts = append(textParts, t)
						}
					case "thinking":
						if t, ok := blk["thinking"].(string); ok && t != "" {
							thinkParts = append(thinkParts, t)
						} else if t, ok := blk["text"].(string); ok && t != "" {
							thinkParts = append(thinkParts, t)
						}
					case "toolCall", "tool_use":
						name, _ := blk["name"].(string)
						callID, _ := blk["id"].(string)
						rawArgs := blk["arguments"]
						if rawArgs == nil {
							rawArgs = blk["input"]
						}
						argsStr := formatTranscriptToolArgs(rawArgs)
						calls = append(calls, callSpec{id: callID, name: name, args: argsStr})
					}
				}
				if len(textParts) > 0 || len(thinkParts) > 0 {
					out = append(out, surface.AgentHubTranscriptEntry{
						Role:        "assistant",
						Text:        strings.Join(textParts, "\n"),
						Thinking:    strings.Join(thinkParts, "\n"),
						TimestampMs: ts,
					})
				}
				for _, call := range calls {
					idx := len(out)
					out = append(out, surface.AgentHubTranscriptEntry{
						Role:        "tool",
						ToolName:    call.name,
						ToolArgs:    call.args,
						TimestampMs: ts,
					})
					if call.id != "" {
						toolIndexByCallID[call.id] = idx
					}
				}
			}

		case "toolResult", "tool":
			toolName, _ := msg["toolName"].(string)
			callID, _ := msg["toolCallId"].(string)
			if callID == "" {
				callID, _ = msg["tool_use_id"].(string)
			}
			isErr, _ := msg["isError"].(bool)
			if !isErr {
				isErr, _ = msg["is_error"].(bool)
			}
			output := extractPlainContent(msg["content"])
			if callID != "" {
				if idx, ok := toolIndexByCallID[callID]; ok && idx >= 0 && idx < len(out) && out[idx].ToolOutput == "" {
					if out[idx].ToolName == "" {
						out[idx].ToolName = toolName
					}
					out[idx].ToolOutput = output
					out[idx].IsError = isErr
					continue
				}
			}
			out = append(out, surface.AgentHubTranscriptEntry{
				Role:        "tool",
				ToolName:    toolName,
				ToolOutput:  output,
				IsError:     isErr,
				TimestampMs: ts,
			})

		case "system", "custom":
			text := extractPlainContent(msg["content"])
			if strings.TrimSpace(text) != "" {
				out = append(out, surface.AgentHubTranscriptEntry{
					Role:        "system",
					Text:        text,
					TimestampMs: ts,
				})
			}

		case "compactionSummary", "branchSummary":
			if sum, ok := msg["summary"].(string); ok && strings.TrimSpace(sum) != "" {
				out = append(out, surface.AgentHubTranscriptEntry{
					Role:        "system",
					Text:        sum,
					TimestampMs: ts,
				})
			}
		}
	}
	return out
}

func extractMessageTimestampMs(msg map[string]any) int64 {
	raw, ok := msg["timestamp"]
	if !ok || raw == nil {
		raw = msg["ts"]
	}
	switch v := raw.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case string:
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t.UnixMilli()
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}

func extractPlainContent(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		parts := make([]string, 0, len(c))
		for _, raw := range c {
			blk, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			bType, _ := blk["type"].(string)
			switch bType {
			case "text":
				if t, ok := blk["text"].(string); ok && t != "" {
					parts = append(parts, t)
				}
			case "image":
				parts = append(parts, "[image]")
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func formatTranscriptToolArgs(raw any) string {
	if raw == nil {
		return ""
	}
	if s, ok := raw.(string); ok {
		return s
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return fmt.Sprint(raw)
	}
	return string(b)
}

// handleAgentsCommand implements the `/agents` slash command.
// With no arguments on a native Tern surface, it opens the native Agent Hub
// overlay via surface.OpenAgentHub(). Otherwise (terminal fallback or explicit
// `/agents <agentID>`), it renders a formatted roster or transcript summary.
func (rt *Runtime) handleAgentsCommand(ctx sdk.Context, args string) error {
	arg := strings.TrimSpace(args)
	if arg == "" && surface.OpenAgentHub() {
		return nil
	}

	sessKey := rt.sessionKey(ctx)
	if arg != "" {
		ref, ok := rt.Registry.LookupInSessionTree(sessKey, arg)
		if !ok || ref == nil || ref.Kind != "sub" {
			ctx.Notify(fmt.Sprintf("Subagent %q not found in this session.", arg), "warning")
			return nil
		}
		entries := rt.LoadTranscriptEntries(sessKey, ref.ID)
		rendered := formatAgentTranscriptFallback(ref.ID, entries)
		ctx.Notify(rendered, "info")
		return nil
	}

	snap := rt.BuildHubSnapshot(sessKey)
	if snap.InspectableCount == 0 {
		ctx.Notify("No subagents in this session.", "info")
		return nil
	}
	rendered := formatAgentHubFallbackSummary(snap)
	ctx.Notify(rendered, "info")
	return nil
}

func formatAgentHubFallbackSummary(snap surface.AgentHubSnapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Agent Hub (%d running / %d total · $%.4f · %s · %d req · %d tok)\n",
		snap.RunningCount, snap.InspectableCount, snap.SubagentCost,
		formatDurationMs(snap.TotalDurationMs), snap.TotalRequests, snap.TotalTokens)
	for _, a := range snap.Agents {
		model := a.Model
		if model == "" {
			model = "default"
		}
		fmt.Fprintf(&b, "- %s [%s] (%s, %s) · $%.4f · %s · %d req · %d tok",
			a.ID, a.Status, a.AgentType, model, a.Cost,
			formatDurationMs(a.DurationMs), a.Requests, a.Tokens)
		if a.Activity != "" {
			fmt.Fprintf(&b, " — %s", a.Activity)
		}
		b.WriteByte('\n')
	}
	b.WriteString("Use `/agents <id>` to inspect a subagent transcript.")
	return b.String()
}

const (
	maxFallbackTranscriptBytes = 8 * 1024
	maxFallbackTranscriptLines = 120
	maxFallbackToolOutputChars = 400
	maxFallbackThinkingChars   = 200
)

func truncateChars(s string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxChars {
		return s
	}
	return string(r[:maxChars]) + "…"
}

func formatAgentTranscriptFallback(agentID string, entries []surface.AgentHubTranscriptEntry) string {
	if len(entries) == 0 {
		return fmt.Sprintf("Transcript for %s: (no messages yet)", agentID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Transcript for %s (%d entries; full log at history://%s):\n", agentID, len(entries), agentID)
	lines := 1
	truncated := false
	appendBlock := func(block string) bool {
		blockLines := strings.Count(block, "\n")
		if b.Len()+len(block) > maxFallbackTranscriptBytes || lines+blockLines > maxFallbackTranscriptLines {
			truncated = true
			return false
		}
		b.WriteString(block)
		lines += blockLines
		return true
	}
	for _, e := range entries {
		var chunk strings.Builder
		switch e.Role {
		case "user":
			fmt.Fprintf(&chunk, "\n[user]\n%s\n", truncateChars(e.Text, maxFallbackToolOutputChars))
		case "assistant":
			if e.Thinking != "" {
				fmt.Fprintf(&chunk, "\n[thinking]\n%s\n", truncateChars(e.Thinking, maxFallbackThinkingChars))
			}
			if e.Text != "" {
				fmt.Fprintf(&chunk, "\n[assistant]\n%s\n", truncateChars(e.Text, maxFallbackToolOutputChars))
			}
		case "tool":
			status := ""
			if e.IsError {
				status = " [error]"
			}
			fmt.Fprintf(&chunk, "\n[tool:%s%s]", e.ToolName, status)
			if e.ToolArgs != "" {
				fmt.Fprintf(&chunk, " %s", OneLineLabel(e.ToolArgs))
			}
			chunk.WriteByte('\n')
			if e.ToolOutput != "" {
				chunk.WriteString(truncateChars(e.ToolOutput, maxFallbackToolOutputChars))
				chunk.WriteByte('\n')
			}
		case "system":
			fmt.Fprintf(&chunk, "\n[system]\n%s\n", truncateChars(e.Text, maxFallbackToolOutputChars))
		}
		if chunk.Len() > 0 && !appendBlock(chunk.String()) {
			break
		}
	}
	if truncated {
		fmt.Fprintf(&b, "\n… (truncated; inspect full transcript via history://%s)", agentID)
	}
	return strings.TrimSpace(b.String())
}
