package pi_subagents

import "math"

// Port of src/runs/shared/usage-reconciliation.ts and the usage sums of src/shared/utils.ts.

// usage is the original's Usage.
type usage struct {
	Input, Output, CacheRead, CacheWrite int
	Cost                                 float64
	Turns                                int
}

var usageFields = []string{"input", "output", "cacheRead", "cacheWrite", "cost"}

var usageAliases = map[string][]string{
	"input": {"input", "inputTokens"}, "output": {"output", "outputTokens"},
	"cacheRead": {"cacheRead", "cacheReadTokens"}, "cacheWrite": {"cacheWrite", "cacheWriteTokens"},
}

func validUsageNumber(v any) (float64, bool) {
	f, ok := asNumber(v)
	return f, ok && !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0
}

func usageFieldValue(u *jsObject, field string) (float64, bool) {
	if u == nil {
		return 0, false
	}
	if field == "cost" {
		if f, ok := validUsageNumber(u.vals["cost"]); ok {
			return f, true
		}
		if c, ok := asObject(u.vals["cost"]); ok {
			return validUsageNumber(c.vals["total"])
		}
		return 0, false
	}
	for _, name := range usageAliases[field] {
		if f, ok := validUsageNumber(u.vals[name]); ok {
			return f, true
		}
	}
	return 0, false
}

// usageTimestamp is a message's usable timestamp: a finite number or a non-empty string.
func usageTimestamp(m *jsObject) (any, bool) {
	switch v := m.vals["timestamp"].(type) {
	case string:
		return v, v != ""
	}
	if f, ok := asNumber(m.vals["timestamp"]); ok && !math.IsNaN(f) && !math.IsInf(f, 0) {
		return f, true
	}
	return nil, false
}

// reconcileAttemptUsage replaces the live usage of an attempt by the persisted messages' (from baseline on) wherever every
// assistant message reports a field; duplicate adjacent assistant projections count once.
func reconcileAttemptUsage(live usage, messages []any, baseline int) usage {
	if baseline < 0 || len(messages) < baseline {
		return live
	}
	var persisted [5]float64
	turns := 0
	complete := [5]bool{true, true, true, true, true}
	var previous *jsObject
	for i := baseline; i < len(messages); i++ {
		m, ok := asObject(messages[i])
		if !ok {
			previous = nil
			continue
		}
		if m.vals["role"] == "assistant" {
			ts, hasTS := usageTimestamp(m)
			duplicate := false
			if previous != nil && previous.vals["role"] == "assistant" && hasTS {
				if pts, ok := usageTimestamp(previous); ok && pts == ts {
					duplicate = true
				}
			}
			if !duplicate {
				turns++
				u, _ := asObject(m.vals["usage"])
				for fi, field := range usageFields {
					if v, ok := usageFieldValue(u, field); ok {
						persisted[fi] += v
					} else {
						complete[fi] = false
					}
				}
			}
		}
		previous = m
	}
	if turns == 0 {
		return live
	}
	out := live
	out.Turns = turns
	for fi, ok := range complete {
		if !ok || math.IsInf(persisted[fi], 0) {
			continue
		}
		switch fi {
		case 0:
			out.Input = int(persisted[fi])
		case 1:
			out.Output = int(persisted[fi])
		case 2:
			out.CacheRead = int(persisted[fi])
		case 3:
			out.CacheWrite = int(persisted[fi])
		case 4:
			out.Cost = persisted[fi]
		}
	}
	return out
}

// totalCost is the original's Details.totalCost.
type totalCost struct {
	InputTokens, OutputTokens int
	CostUsd                   float64
}

// nestedRun is the part of the original's NestedRunSummary the cost sum reads.
type nestedRun struct {
	TotalCost *totalCost
	Children  []nestedRun
	Steps     []nestedStep
}

type nestedStep struct{ Children []nestedRun }

// singleResultUsage is what the sums read of a SingleResult.
type singleResultUsage struct {
	Usage    usage
	Children []nestedRun
}

func sumResultsUsage(results []singleResultUsage) usage {
	var u usage
	for _, r := range results {
		u.Input += r.Usage.Input
		u.Output += r.Usage.Output
		u.CacheRead += r.Usage.CacheRead
		u.CacheWrite += r.Usage.CacheWrite
		u.Cost += r.Usage.Cost
		u.Turns += r.Usage.Turns
	}
	return u
}

func addNestedCost(total *totalCost, children []nestedRun) {
	for _, c := range children {
		if c.TotalCost != nil {
			total.InputTokens += c.TotalCost.InputTokens
			total.OutputTokens += c.TotalCost.OutputTokens
			total.CostUsd += c.TotalCost.CostUsd
			continue
		}
		addNestedCost(total, c.Children)
		for _, s := range c.Steps {
			addNestedCost(total, s.Children)
		}
	}
}

// sumResultsCost sums input tokens, output tokens and cost over results and their attached nested runs.
func sumResultsCost(results []singleResultUsage) totalCost {
	var t totalCost
	for _, r := range results {
		t.InputTokens += r.Usage.Input
		t.OutputTokens += r.Usage.Output
		t.CostUsd += r.Usage.Cost
		addNestedCost(&t, r.Children)
	}
	return t
}
