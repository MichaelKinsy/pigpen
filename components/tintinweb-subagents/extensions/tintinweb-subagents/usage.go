package tintinweb_subagents

import "sync"

// Usage accounting of an agent: what its assistant messages spent, accumulated, and the pool that carries it back to
// the parent session. upstream: src/usage.ts (addUsage, toReportedUsage, PendingUsagePool).
// The display readers (lifetimeUsage, getLifetimeTotal, getLifetimeCost, formatCost) are in usage_display.go.

// addUsage adds a usage delta into a target accumulator. upstream: usage.ts addUsage.
func addUsage(into *lifetimeUsage, delta lifetimeUsage) {
	into.Input += delta.Input
	into.Output += delta.Output
	into.CacheWrite += delta.CacheWrite
	into.CacheRead += delta.CacheRead
	into.Cost += delta.Cost
}

// reportedCost is the cost breakdown of a pi Usage; only Total is populated, as in the original.
type reportedCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

// reportedUsage is a pi `Usage`: every field present, because pi's addUsageToTotals dereferences cost.total.
// upstream: usage.ts ReportedUsage.
type reportedUsage struct {
	Input       int          `json:"input"`
	Output      int          `json:"output"`
	CacheRead   int          `json:"cacheRead"`
	CacheWrite  int          `json:"cacheWrite"`
	TotalTokens int          `json:"totalTokens"`
	Cost        reportedCost `json:"cost"`
}

// toReportedUsage renders an accumulator as a pi Usage, or nil when nothing was spent. cacheRead IS included here,
// unlike in getLifetimeTotal. upstream: usage.ts toReportedUsage.
func toReportedUsage(u lifetimeUsage) *reportedUsage {
	if u.Input == 0 && u.Output == 0 && u.CacheWrite == 0 && u.CacheRead == 0 && u.Cost == 0 {
		return nil
	}
	return &reportedUsage{
		Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite,
		TotalTokens: u.Input + u.Output + u.CacheRead + u.CacheWrite,
		Cost:        reportedCost{Total: u.Cost},
	}
}

// pendingUsagePool is the subagent spend the parent session has not been told about yet; drain empties it, so each
// message is reported exactly once. upstream: usage.ts PendingUsagePool.
type pendingUsagePool struct {
	mu      sync.Mutex
	pending lifetimeUsage
}

func (p *pendingUsagePool) add(delta lifetimeUsage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	addUsage(&p.pending, delta)
}

// drain takes everything accumulated so far as a pi Usage, resetting the pool; nil when nothing is pending.
func (p *pendingUsagePool) drain() *reportedUsage {
	p.mu.Lock()
	defer p.mu.Unlock()
	drained := toReportedUsage(p.pending)
	p.pending = lifetimeUsage{}
	return drained
}
