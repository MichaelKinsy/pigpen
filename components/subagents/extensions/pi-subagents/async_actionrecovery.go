package pi_subagents

// The recovery text of an unknown `subagent` action, and the abort-recovery plan of a child. Ports unknownSubagentActionMessage
// (subagent-executor.ts) with SUBAGENT_ACTIONS (types.ts), and abort-recovery.ts.

import (
	"fmt"
	"regexp"
	"strings"
)

// subagentActions is SUBAGENT_ACTIONS.
var subagentActions = []string{"list", "get", "models", "children.list", "guide", "validate", "create", "update", "delete", "eject", "disable", "enable", "reset",
	"mission.create", "mission.list", "mission.show", "mission.update", "mission.resolve-decision", "mission.attach-run", "mission.close", "worktree.discard", "worktree.cleanup",
	"lane.status", "lane.recordMerge", "lane.recordSupersession", "refine", "refine.show", "refine.rollback", "inspector.open", "inspector.command", "inspector.status",
	"inspector.close", "project.open", "project.status", "project.close", "status", "debug.run", "grant-spawn-budget", "interrupt", "resume", "steer", "stop", "dismiss", "doctor",
	"watchdog.status", "watchdog.check", "watchdog.configure", "watchdog.recommend-model", "schedule.create", "schedule.list", "schedule.show", "schedule.history",
	"schedule.pause", "schedule.resume", "schedule.run", "schedule.run-due", "schedule.delete"}

// actionsOutsideTheList are the actions the executor handles before it checks SUBAGENT_ACTIONS.
var actionsOutsideTheList = map[string]bool{"append-step": true}

var destructiveManagementActions = map[string]bool{"delete": true, "eject": true, "disable": true, "reset": true, "mission.close": true, "worktree.discard": true,
	"refine.rollback": true, "inspector.close": true, "project.close": true, "stop": true, "interrupt": true, "schedule.delete": true}

func isKnownSubagentAction(name string) bool {
	if actionsOutsideTheList[name] {
		return true
	}
	for _, a := range subagentActions {
		if a == name {
			return true
		}
	}
	return false
}

// editDistance is the Levenshtein distance over UTF-16 code units (JavaScript string indexing).
func editDistance(left, right string) int {
	l, r := jsUTF16(left), jsUTF16(right)
	prev := make([]int, len(r)+1)
	for i := range prev {
		prev[i] = i
	}
	for li := 1; li <= len(l); li++ {
		diagonal := prev[0]
		prev[0] = li
		for ri := 1; ri <= len(r); ri++ {
			above := prev[ri]
			if l[li-1] == r[ri-1] {
				prev[ri] = diagonal
			} else {
				prev[ri] = min(diagonal, above, prev[ri-1]) + 1
			}
			diagonal = above
		}
	}
	return prev[len(r)]
}

func hasSingleAdjacentTransposition(left, right string) bool {
	l, r := jsUTF16(left), jsUTF16(right)
	if len(l) != len(r) {
		return false
	}
	mismatch := -1
	for i := range l {
		if l[i] != r[i] {
			mismatch = i
			break
		}
	}
	if mismatch < 0 || mismatch+1 >= len(r) {
		return false
	}
	if l[mismatch] != r[mismatch+1] || l[mismatch+1] != r[mismatch] {
		return false
	}
	for i := mismatch + 2; i < len(l); i++ {
		if l[i] != r[i] {
			return false
		}
	}
	return true
}

// unknownSubagentActionMessage suggests a benign near-miss (never a destructive one) and lists the valid actions.
func unknownSubagentActionMessage(action string) string {
	requested := strings.ToLower(action)
	suggestion := ""
	for _, candidate := range subagentActions {
		d := editDistance(requested, candidate)
		var ok bool
		if destructiveManagementActions[candidate] {
			ok = d == 1 && len(jsUTF16(requested)) >= len(jsUTF16(candidate))-1
		} else {
			ok = d <= max(1, len(jsUTF16(candidate))/4) || hasSingleAdjacentTransposition(requested, candidate)
		}
		if ok {
			suggestion = candidate
			break
		}
	}
	next := `Use subagent({ action: "status" }) to inspect runs or subagent({ action: "list" }) to inspect agents.`
	valid := "Valid: " + strings.Join(subagentActions, ", ") + "."
	if suggestion != "" {
		return fmt.Sprintf("Unknown action: %s. Did you mean %s? %s %s", action, suggestion, next, valid)
	}
	return fmt.Sprintf("Unknown action: %s. %s %s", action, next, valid)
}

// ── abort-recovery.ts ──

const abortRecoveryPrompt = "The prior run ended from a provider/transport abort after useful progress. Continue from the current files and transcript. Do not restart. Fix any validation failure or write the required report. Finish with final output."

// AbortRecoveryPlan is whether a child that ended in an abort should be resumed once.
type AbortRecoveryPlan struct {
	Action     string // resume or settle
	Prompt     string
	Reason     string
	Diagnostic string
}

// AbortRecoveryInput is what planAbortRecovery looks at.
type AbortRecoveryInput struct {
	Messages                  []map[string]any
	Error                     string
	ProcessSignal             string
	SessionAvailable          bool
	AlreadyResumed            bool
	Stopped, Interrupted      bool
	TimedOut                  bool
	ToolBudgetExhausted       bool
	UsageBudgetExhausted      bool
	StructuredOutputFailed    bool
	AcceptanceFailed          bool
	CurrentTool               string
	AfterCompactionSettlement bool
}

var (
	providerAbortPattern = regexp.MustCompile(`(?i)(?:provider|transport|connection|stream|socket|request).*(?:abort|closed|reset|ended|terminated|error|fail)|(?:abort|closed|reset|ended|terminated|error|fail).*(?:provider|transport|connection|stream|socket|request)`)
	abortErrorPattern    = regexp.MustCompile(`(?i)\b(?:operation|request|response|stream|connection|transport|provider)?\s*(?:was\s+)?aborted\b`)
	toolFailurePrefix    = regexp.MustCompile(`(?i)^[\w.:@/-]+ failed (?:(?:\(exit \d+\):)|(?:with exit code \d+))(?:\s|$)`)
)

func isProviderAbortError(e string) bool {
	return !toolFailurePrefix.MatchString(jsTrim(e)) && (providerAbortPattern.MatchString(e) || abortErrorPattern.MatchString(e))
}

func contentParts(m map[string]any) []any {
	c, _ := m["content"].([]any)
	return c
}

func terminalAssistant(messages []map[string]any) (map[string]any, int) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i]["role"] == "assistant" {
			return messages[i], i
		}
	}
	return nil, len(messages)
}

func zeroOutputUsage(m map[string]any) bool {
	u, ok := m["usage"].(map[string]any)
	if !ok {
		return false
	}
	out := u["output"]
	if out == nil {
		out = u["outputTokens"]
	}
	if out == nil {
		return true
	}
	f, _ := out.(float64)
	return f == 0
}

func hasUsefulProgress(messages []map[string]any, terminalIndex int) bool {
	for i := 0; i < terminalIndex && i < len(messages); i++ {
		m := messages[i]
		if m["role"] == "assistant" && len(contentParts(m)) > 0 {
			return true
		}
		if m["role"] == "toolResult" && m["isError"] != true {
			return true
		}
	}
	return false
}

func hasUnresolvedToolCall(messages []map[string]any) bool {
	pending := map[string]bool{}
	for _, m := range messages {
		switch m["role"] {
		case "assistant":
			for _, p := range contentParts(m) {
				if part, ok := p.(map[string]any); ok && part["type"] == "toolCall" {
					if id, ok := part["id"].(string); ok && id != "" {
						pending[id] = true
					}
				}
			}
		case "toolResult":
			if id, ok := m["toolCallId"].(string); ok {
				delete(pending, id)
			}
		}
	}
	return len(pending) > 0
}

// planAbortRecovery decides whether an abort after compaction is safe to continue from the retained session.
func planAbortRecovery(in AbortRecoveryInput) AbortRecoveryPlan {
	message, terminalIndex := terminalAssistant(in.Messages)
	terminalError, _ := message["errorMessage"].(string)
	emptyZeroUsageTerminal := message != nil && len(contentParts(message)) == 0 && zeroOutputUsage(message)
	terminalAssistantAbort := message["stopReason"] == "aborted" || (message["stopReason"] == "error" && terminalError != "" && isProviderAbortError(terminalError))
	abortCandidate := emptyZeroUsageTerminal && terminalAssistantAbort
	compactionAbortCandidate := in.AfterCompactionSettlement && abortCandidate
	settle := func(reason string) AbortRecoveryPlan {
		p := AbortRecoveryPlan{Action: "settle", Reason: reason}
		if compactionAbortCandidate {
			p.Diagnostic = fmt.Sprintf("Compaction-induced child abort could not be resumed safely: %s.", reason)
		}
		return p
	}
	switch {
	case in.AlreadyResumed:
		return settle("resume already attempted")
	case !in.SessionAvailable:
		return settle("retained session unavailable")
	case in.Stopped || in.Interrupted:
		return settle("explicit stop or interrupt")
	// a terminal drain reports a process signal for a child a compaction abort left open: with both markers it is not authoritative
	case in.ProcessSignal != "" && !compactionAbortCandidate:
		return settle("process terminated by signal")
	case in.TimedOut:
		return settle("elapsed timeout")
	case in.ToolBudgetExhausted || in.UsageBudgetExhausted:
		return settle("budget exhausted")
	case in.StructuredOutputFailed:
		return settle("structured output failure")
	case in.AcceptanceFailed:
		return settle("acceptance failure")
	case in.CurrentTool != "":
		t := in.CurrentTool
		if u := jsUTF16(t); len(u) > 128 {
			t = string(utf16Runes(u[:128]))
		}
		return settle(fmt.Sprintf("active tool '%s' remains in flight", t))
	case hasUnresolvedToolCall(in.Messages):
		return settle("unresolved tool call remains in transcript")
	case !in.AfterCompactionSettlement:
		return settle("compaction settlement not verified")
	case !abortCandidate:
		return settle("terminal assistant abort evidence not verified")
	}
	limit := len(in.Messages)
	if emptyZeroUsageTerminal {
		limit = terminalIndex
	}
	if !hasUsefulProgress(in.Messages, limit) {
		return settle("no useful prior progress")
	}
	return AbortRecoveryPlan{Action: "resume", Prompt: abortRecoveryPrompt}
}

func utf16Runes(u []uint16) []rune {
	var out []rune
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		if c >= 0xd800 && c < 0xdc00 && i+1 < len(u) {
			out = append(out, 0x10000+(c-0xd800)<<10+rune(u[i+1])-0xdc00)
			i++
			continue
		}
		out = append(out, c)
	}
	return out
}
