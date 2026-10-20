package pi_subagents

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// Port of src/runs/shared/tool-budget.ts.

var defaultToolBudgetBlock = []string{"read", "grep", "find", "ls"}

// resolvedToolBudget is the original's ResolvedToolBudget: Block is "*" (AllBlocked) or tool names.
type resolvedToolBudget struct {
	Hard       int
	Soft       int
	HasSoft    bool
	AllBlocked bool
	Block      []string
}

// toolBudgetState is the original's ToolBudgetState.
type toolBudgetState struct {
	resolvedToolBudget
	ToolCount     int
	Outcome       string // within-budget, soft-reached, hard-blocked
	SoftReachedAt int
	HardReachedAt int
	BlockedTool   string
	HasSoftAt     bool
	HasHardAt     bool
}

// normalizeToolBudgetBlock turns the block setting into the resolved one: nil/undefined is the default list, "*" is all,
// a list is trimmed, emptied of blanks and de-duplicated.
func normalizeToolBudgetBlock(block any) (all bool, names []string) {
	if block == "*" {
		return true, nil
	}
	if isUndefined(block) {
		return false, append([]string{}, defaultToolBudgetBlock...)
	}
	seen := map[string]bool{}
	names = []string{}
	for _, t := range stringsOf(block) {
		t = jsTrim(t)
		if t != "" && !seen[t] {
			seen[t] = true
			names = append(names, t)
		}
	}
	return false, names
}

// validateToolBudgetConfig is the original's validateToolBudgetConfig: the resolved budget, or the first problem. raw nil
// (undefined) is no budget and no error. minimumHard is 1 (0 only for internal callers).
func validateToolBudgetConfig(raw any, label string, minimumHard int) (budget *resolvedToolBudget, errMsg string) {
	if isUndefined(raw) {
		return nil, ""
	}
	value, ok := asObject(raw)
	if !ok {
		return nil, label + " must be an object with hard and optional soft/block."
	}
	hard, _ := asNumber(value.vals["hard"])
	if !isIntegerValue(value.vals["hard"]) || hard < float64(minimumHard) {
		return nil, fmt.Sprintf("%s.hard must be an integer >= %d.", label, minimumHard)
	}
	softV, hasSoft := value.get("soft")
	hasSoft = hasSoft && !isUndefined(softV)
	soft, _ := asNumber(softV)
	if hasSoft && (!isIntegerValue(softV) || soft < 1) {
		return nil, label + ".soft must be an integer >= 1 when provided."
	}
	if hasSoft && soft > hard {
		return nil, fmt.Sprintf("%s.soft must be <= %s.hard.", label, label)
	}
	block, hasBlock := value.get("block")
	hasBlock = hasBlock && !isUndefined(block)
	if hasBlock && block != "*" {
		arr, isArr := asArray(block)
		if !isArr {
			return nil, label + `.block must be "*" or an array of tool names.`
		}
		if len(arr) == 0 {
			return nil, label + ".block must contain at least one tool name."
		}
		for _, item := range arr {
			if s, isStr := item.(string); !isStr || jsTrim(s) == "" {
				return nil, label + ".block must contain non-empty tool names."
			}
		}
	}
	b := &resolvedToolBudget{Hard: int(hard), Soft: int(soft), HasSoft: hasSoft}
	if hasBlock {
		b.AllBlocked, b.Block = normalizeToolBudgetBlock(block)
	} else {
		b.AllBlocked, b.Block = normalizeToolBudgetBlock(nil)
	}
	return b, ""
}

func initialToolBudgetState(b resolvedToolBudget) toolBudgetState {
	return toolBudgetState{resolvedToolBudget: b, Outcome: "within-budget"}
}

// toolBudgetStateFor is the original's toolBudgetState(budget, toolCount, blockedTool).
func toolBudgetStateFor(b resolvedToolBudget, toolCount int, blockedTool string) toolBudgetState {
	overHard := toolCount > b.Hard
	overSoft := b.HasSoft && toolCount >= b.Soft
	s := toolBudgetState{resolvedToolBudget: b, ToolCount: toolCount, Outcome: "within-budget"}
	switch {
	case overHard:
		s.Outcome = "hard-blocked"
	case overSoft:
		s.Outcome = "soft-reached"
	}
	if overSoft {
		s.SoftReachedAt, s.HasSoftAt = b.Soft, true
	}
	if overHard {
		s.HardReachedAt, s.HasHardAt, s.BlockedTool = b.Hard, true, blockedTool
	}
	return s
}

// shouldBlockToolForBudget: past the hard limit, the blocked tools (or all) are refused.
func shouldBlockToolForBudget(b resolvedToolBudget, tool string, nextToolCount int) bool {
	if nextToolCount <= b.Hard {
		return false
	}
	if b.AllBlocked {
		return true
	}
	for _, t := range b.Block {
		if t == tool {
			return true
		}
	}
	return false
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func toolBudgetSoftNudge(b resolvedToolBudget, toolCount int) string {
	return fmt.Sprintf("Tool budget soft limit reached after %d tool call%s (soft %d, hard %d). Stop starting new browsing/search work and finalize from the context you already have.", toolCount, plural(toolCount), b.Soft, b.Hard)
}

func toolBudgetBlockedMessage(b resolvedToolBudget, tool string, toolCount int) string {
	return fmt.Sprintf("Tool budget hard limit reached after %d tool call%s (hard %d). The '%s' tool is blocked so you can finalize from the context you already have.", toolCount, plural(toolCount), b.Hard, tool)
}

var toolBudgetBlockedPattern = regexp.MustCompile(`^Tool budget hard limit reached after (\d+) tool calls? \(hard (\d+)\)\. The '([^']+)' tool is blocked so you can finalize from the context you already have\.$`)

// isToolBudgetBlockedMessage recognizes only the exact message toolBudgetBlockedMessage writes for this budget and tool; a
// phrase inside ordinary tool output does not count.
func isToolBudgetBlockedMessage(b resolvedToolBudget, resultText, blockedTool string) bool {
	tool := jsTrim(blockedTool)
	if tool == "" {
		return false
	}
	m := toolBudgetBlockedPattern.FindStringSubmatch(jsTrim(resultText))
	if m == nil {
		return false
	}
	count, _ := strconv.ParseFloat(m[1], 64)
	hard, _ := strconv.ParseFloat(m[2], 64)
	return hard == float64(b.Hard) && count > float64(b.Hard) && m[3] == tool && !math.IsNaN(count)
}
