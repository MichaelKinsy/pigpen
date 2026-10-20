package pi_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Twins of steering.test.ts.

type fifoEntry struct{ id, text string }

func fifoText(e fifoEntry) string { return e.text }

func TestSteeringLedgerTwins(t *testing.T) {
	const f = "steering"
	routed := func(idx int) steeringTarget { return steeringTarget{Index: idx, State: "routed"} }

	tw(t, f, "redacts, bounds, and separates steering message previews", func(t *testing.T) {
		secret := "ghp_1234567890abcdef"
		message := "Use " + secret + "\n" + strings.Repeat("x", 200)
		preview := steeringMessagePreview(message)
		receipt := steeringReceipt(message, "Steering delivered.")
		if len(preview) > 160 {
			t.Fatalf("preview length %d", len(preview))
		}
		polMatches(t, preview, `\[redacted\]`)
		if strings.Contains(preview, secret) || strings.Contains(receipt, secret) {
			t.Fatal("the secret leaked")
		}
		polMatches(t, receipt, `\[redacted\]`)
		polMatches(t, receipt, "^Steering delivered\\.\n\nMessage sent:\n```text\n[\\s\\S]+\n```$")
		polMatches(t, steeringReceipt("review this:\n```\ntext\n```", "Steering queued."), "^Steering queued\\.\n\nMessage sent:\n````text\n[\\s\\S]+\n````$")
	})

	tw(t, f, "retains 20 recent requests while aggregate totals remain monotonic", func(t *testing.T) {
		s := createSteeringStatus()
		for i := range 21 {
			recordSteeringRequest(s, steerRequestInput{ID: "request-" + strconv.Itoa(i), RequestedAt: int64(i + 1), Message: "guidance", Targets: []steeringTarget{routed(0)}})
		}
		equal(t, s.Requested, 21, "requested")
		equal(t, s.Pending, 21, "pending")
		equal(t, len(s.Recent), 20, "recent")
		equal(t, s.Recent[0].ID, "request-1", "first")
	})

	tw(t, f, "matches equal-text accepted steers in FIFO order exactly once", func(t *testing.T) {
		accepted := []fifoEntry{{"first", "same guidance"}, {"second", "same guidance"}, {"other", "different"}}
		ids := func(es []fifoEntry) []string {
			var out []string
			for _, e := range es {
				out = append(out, e.id)
			}
			return out
		}
		m, rest, ok := takeMatchingAcceptedSteer(accepted, fifoText, "same guidance")
		equal(t, [2]any{ok, m.id}, [2]any{true, "first"}, "first match")
		equal(t, ids(rest), []string{"second", "other"}, "rest")
		m, rest, ok = takeMatchingAcceptedSteer(rest, fifoText, "same guidance")
		equal(t, [2]any{ok, m.id}, [2]any{true, "second"}, "second match")
		_, rest2, ok := takeMatchingAcceptedSteer(rest, fifoText, "same guidance")
		equal(t, ok, false, "no third match")
		equal(t, ids(rest2), []string{"other"}, "remaining")
	})

	tw(t, f, "describes unconsumed requests by delivery mode, not aggregate queue state", func(t *testing.T) {
		equal(t, unconsumedSteerReason(""), "child completed before consuming steering", "unset")
		equal(t, unconsumedSteerReason("steer"), "child completed before consuming steering", "steer")
		equal(t, unconsumedSteerReason("auto"), "child completed before consuming steering", "auto")
		equal(t, unconsumedSteerReason("follow_up"), "child completed before consuming follow-up", "follow_up")
	})

	tw(t, f, "does not double-count pending when a routed target becomes queued then delivered", func(t *testing.T) {
		s := createSteeringStatus()
		recordSteeringRequest(s, steerRequestInput{ID: "one", RequestedAt: 1, Message: "guidance", Targets: []steeringTarget{routed(0)}})
		equal(t, s.Pending, 1, "routed")
		updateSteeringTarget(s, "one", 0, "queued", 2, steeringTargetFields{})
		equal(t, s.Pending, 1, "queued")
		updateSteeringTarget(s, "one", 0, "delivered", 3, steeringTargetFields{})
		equal(t, s.Pending, 0, "delivered pending")
		equal(t, s.Delivered, 1, "delivered")
	})

	tw(t, f, "classifies mixed target outcomes as partial", func(t *testing.T) {
		s := createSteeringStatus()
		recordSteeringRequest(s, steerRequestInput{ID: "mixed", RequestedAt: 1, Message: "correct course", Targets: []steeringTarget{routed(0), routed(1)}})
		updateSteeringTarget(s, "mixed", 0, "delivered", 2, steeringTargetFields{})
		updateSteeringTarget(s, "mixed", 1, "failed", 3, steeringTargetFields{Reason: "unsupported"})
		partial := actionResultFromSteeringStatus(s, "run", "mixed", "")
		equal(t, partial.State, "partial", "state")
		equal(t, []string{partial.Targets[0].State, partial.Targets[1].State}, []string{"delivered", "failed"}, "targets")
	})

	tw(t, f, "classifies terminal mixed outcomes regardless of acknowledgment order", func(t *testing.T) {
		for _, order := range [][2]string{{"failed", "delivered"}, {"delivered", "failed"}} {
			s := createSteeringStatus()
			recordSteeringRequest(s, steerRequestInput{ID: "notice", RequestedAt: 1, Message: "correct course", Targets: []steeringTarget{routed(0), routed(1)}})
			updateSteeringTarget(s, "notice", 0, order[0], 2, steeringTargetFields{})
			equal(t, terminalSteeringNoticeState(s, "notice"), "", "after the first")
			updateSteeringTarget(s, "notice", 1, order[1], 3, steeringTargetFields{})
			equal(t, terminalSteeringNoticeState(s, "notice"), "partial", "after both")
		}
	})

	tw(t, f, "classifies an immediately failed request as terminal", func(t *testing.T) {
		s := createSteeringStatus()
		recordSteeringRequest(s, steerRequestInput{ID: "unsupported", RequestedAt: 1, Message: "correct course", Targets: []steeringTarget{{Index: 0, State: "failed", Reason: "unsupported"}}})
		equal(t, terminalSteeringNoticeState(s, "unsupported"), "failed", "state")
	})

	tw(t, f, "allows only one committed recovery claim per source run", func(t *testing.T) {
		dir := t.TempDir()
		claim, marker, err := claimSteeringRecovery(dir, steeringClaim{RequestID: "first", SourceRunID: "source", CommittedAt: 1})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{claim, marker} {
			info, err := os.Stat(p)
			if err != nil {
				t.Fatalf("%s: %v", p, err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				t.Errorf("%s mode %o", p, info.Mode().Perm())
			}
		}
		_, _, err = claimSteeringRecovery(dir, steeringClaim{RequestID: "second", SourceRunID: "source", CommittedAt: 2})
		hasMatch(t, err, "Another steering recovery is already committed")
		if filepath.Base(marker) != base64URL("first")+".json" {
			t.Errorf("marker name %s", marker)
		}
	})

	tw(t, f, "keeps recovery authoritative when delivery is acknowledged late", func(t *testing.T) {
		s := createSteeringStatus()
		recordSteeringRequest(s, steerRequestInput{ID: "recover", RequestedAt: 1, Message: "guidance", Targets: []steeringTarget{routed(0)}})
		updateSteeringTarget(s, "recover", 0, "recovered", 4, steeringTargetFields{ReplacementRunID: "replacement"})
		updateSteeringTarget(s, "recover", 0, "late", 5, steeringTargetFields{Reason: "after recovery commit"})
		r := actionResultFromSteeringStatus(s, "source", "recover", "replacement")
		equal(t, [3]string{r.State, r.ReplacementRunID, r.Targets[0].State}, [3]string{"recovered", "replacement", "recovered"}, "result")
		equal(t, r.Targets[0].ReplacementRunID, "replacement", "target replacement")
		equal(t, *r.Targets[0].LateDeliveredAt, int64(5), "late delivery time")
	})

	tw(t, f, "preserves only remaining deadline and tool budgets", func(t *testing.T) {
		budget := &resolvedToolBudget{Soft: 45, HasSoft: true, Hard: 65, Block: []string{"read"}}
		limits, err := remainingSteeringRecoveryLimits(new(int64(10000)), budget, 50, 4000)
		if err != nil {
			t.Fatal(err)
		}
		equal(t, *limits.TimeoutMs, int64(6000), "timeout")
		equal(t, *limits.AbsoluteDeadlineAt, int64(10000), "deadline")
		equal(t, *limits.ToolBudget, resolvedToolBudget{Hard: 15, Block: []string{"read"}}, "tool budget")
		paused := &AsyncStatus{Extra: map[string]json.RawMessage{"toolBudget": json.RawMessage(`{"soft":45,"hard":65,"block":["read"],"toolCount":50,"outcome":"soft-reached"}`)}}
		equal(t, statusConsumedToolCount(paused), 50, "consumed count from the budget")
		equal(t, statusConsumedToolCount(&AsyncStatus{ToolCount: new(7)}), 7, "consumed count from the status")
	})

	tw(t, f, "reconstructs the original agent contract without leaking changed current fields", func(t *testing.T) {
		current := AgentConfig{Name: "worker", Description: "current", Model: "current/model", Thinking: "high", Tools: []string{"write"},
			AllowNestedSubagents: new(true), Extensions: []string{"current-extension"}, SubagentOnlyExtensions: []string{"current-child-extension"},
			McpDirectTools: []string{"current_mcp"}, SystemPrompt: "current prompt", SystemPromptMode: "append", InheritProjectContext: true, InheritSkills: true,
			Skills: []string{"current-skill"}, SkillPath: []string{"current-skill-path"}, FilePath: "/current/agent.md",
			Memory: &AgentMemoryConfig{Scope: "user", Path: "/current/memory.md"}, Output: "/current/output.md",
			ToolBudget: js(t, `{"hard":99,"block":"*"}`), MaxSubagentDepth: new(9)}
		d := &RecoveryDescriptor{Version: 1, SourceRunID: "source", Agent: "worker", Cwd: "/original", Model: "original/model", Tools: []string{"read"},
			AllowNestedSubagents: new(false), SystemPrompt: "original prompt", SystemPromptMode: "replace", OutputMode: "inline", MaxSubagentDepth: 2,
			Carried: map[string]json.RawMessage{"initialToolBudget": json.RawMessage(`{"hard":7,"block":["read"]}`)}}
		got, err := applySteeringRecoveryAgentConfig(current, d)
		if err != nil {
			t.Fatal(err)
		}
		equal(t, got.Model, "original/model", "model")
		equal(t, got.Tools, []string{"read"}, "tools")
		equal(t, *got.AllowNestedSubagents, false, "nested")
		equal(t, got.SystemPrompt, "original prompt", "prompt")
		equal(t, got.InheritProjectContext, false, "inherit project context")
		equal(t, got.InheritSkills, false, "inherit skills")
		budget, _ := asObject(got.ToolBudget)
		equal(t, [2]any{budget.vals["hard"], stringsOf(budget.vals["block"])}, [2]any{float64(7), []string{"read"}}, "tool budget")
		equal(t, *got.MaxSubagentDepth, 2, "depth")
		equal(t, got.Thinking == nil, true, "thinking does not leak")
		equal(t, got.Extensions == nil && got.SubagentOnlyExtensions == nil && got.McpDirectTools == nil && got.Skills == nil && got.SkillPath == nil, true, "lists do not leak")
		equal(t, got.FilePath == "" && got.Memory == nil && got.Output == "", true, "file path, memory and output do not leak")
	})

	tw(t, f, "intersects persisted thinking ceilings with the current agent ceiling", func(t *testing.T) {
		current := AgentConfig{Name: "worker", Description: "current", Thinking: "high", MaxThinking: "low", SystemPrompt: "current prompt", SystemPromptMode: "replace", Source: "project", FilePath: "/current/agent.md"}
		d := &RecoveryDescriptor{Version: 1, SourceRunID: "source", Agent: "worker", Cwd: "/original", Thinking: "xhigh", ThinkingCeiling: "xhigh", SystemPromptMode: "replace", OutputMode: "inline", MaxSubagentDepth: 2}
		got, err := applySteeringRecoveryAgentConfig(current, d)
		if err != nil {
			t.Fatal(err)
		}
		equal(t, got.Thinking, "xhigh", "thinking")
		equal(t, got.MaxThinking, "low", "ceiling")
	})

	tw(t, f, "rejects recovery when a configured deadline or tool budget is exhausted", func(t *testing.T) {
		_, err := remainingSteeringRecoveryLimits(new(int64(5)), nil, 0, 5)
		hasMatch(t, err, "deadline budget")
		_, err = remainingSteeringRecoveryLimits(nil, &resolvedToolBudget{Hard: 2, AllBlocked: true}, 2, nowMs())
		hasMatch(t, err, "tool budget")
	})
}
