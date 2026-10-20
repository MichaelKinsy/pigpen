package tintinweb_subagents

// childHooks receives what a child's event stream carries. A real child (rpcChild) calls them from its reader; a
// test double may call them to stand in for a run. upstream: agent-runner.ts runAgent options (onAssistantUsage,
// onCompaction) and the session subscription the output file streams from.
type childHooks struct {
	Usage      func(lifetimeUsage)                   // once per assistant message_end
	Compaction func(reason string, tokensBefore int) // a successful compaction
	Event      func(event map[string]any)            // every non-response event, in order
}

// liveChild is what a child that can report its live state implements besides child.
type liveChild interface {
	stats() *float64            // context-window utilization, nil when unavailable
	messages() []map[string]any // the conversation so far
}
