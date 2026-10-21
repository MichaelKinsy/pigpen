package tintinweb_subagents

import "strings"

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

// settledChild is what a child that was stopped or failed can still report: the conversation its event stream
// carried and the context percent read just before it was stopped. The original keeps record.session after a stop or
// an error, so verbose, `Context:` and `<context_percent>` still have data; a child process is gone by then.
type settledChild interface {
	snapshot() (conversation string, contextPercent *float64)
}

// maxConversationBytes bounds the conversation text kept for one agent (the record, and a child's event-derived
// copy): the oldest messages are dropped first. The original holds the whole session until the manager removes the
// record (gap G18, not ported).
const maxConversationBytes = 1 << 20

// conversationLog is getAgentConversation built one message at a time with a size bound.
type conversationLog struct {
	parts   []string
	bytes   int
	dropped bool
}

func (l *conversationLog) add(msg map[string]any) {
	for _, p := range conversationParts(msg) {
		l.parts = append(l.parts, p)
		l.bytes += len(p) + 2
	}
	for l.bytes > maxConversationBytes && len(l.parts) > 1 {
		l.bytes -= len(l.parts[0]) + 2
		l.parts[0] = ""
		l.parts = l.parts[1:]
		l.dropped = true
	}
}

func (l *conversationLog) String() string {
	text := strings.Join(l.parts, "\n\n")
	if l.dropped && text != "" {
		return "[earlier messages omitted]\n\n" + text
	}
	return text
}

// conversationOf formats messages as getAgentConversation does, within maxConversationBytes.
func conversationOf(messages []map[string]any) string {
	var l conversationLog
	for _, m := range messages {
		l.add(m)
	}
	return l.String()
}
