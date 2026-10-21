package tintinweb_subagents

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Benchmarks of the hot paths this slice adds: the child's event stream (every event passes the hooks), the
// transcript flush, the notification text and the conversation text.

func benchRecord() *agentRecord {
	start := time.Now()
	return &agentRecord{ID: "abc", Description: "bench", Status: statusCompleted, Result: strings.Repeat("result ", 100), ToolUses: 7,
		Usage: lifetimeUsage{Input: 1000, Output: 200, Cost: 0.0123}, CompactionCount: 2, OutputFile: "/tmp/x.output",
		StartedAt: start, CompletedAt: start.Add(3 * time.Second)}
}

func BenchmarkFormatTaskNotification(b *testing.B) {
	r := benchRecord()
	pct := 41.5
	r.ContextPercent = &pct
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = formatTaskNotification(r, 500, true)
	}
}

// A streamed delta goes straight to the stream (the manager passes it in as the child's Event hook): one mutex, no
// manager lock.
func BenchmarkChildEventThroughHooks(b *testing.B) {
	hook := newTranscriptStream(filepath.Join(b.TempDir(), "a.output"), "a", "/w", 1).onEvent
	delta := obj{"type": "message_update", "delta": "x"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		hook(delta)
	}
}

func BenchmarkTranscriptTurn(b *testing.B) {
	path := filepath.Join(b.TempDir(), "a.output")
	s := newTranscriptStream(path, "a", "/w", 1)
	msg := obj{"role": "assistant", "content": []any{obj{"type": "text", "text": strings.Repeat("answer ", 50)}}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.onEvent(obj{"type": "message_end", "message": msg})
		s.onEvent(obj{"type": "turn_end"})
	}
}

func BenchmarkUsageHook(b *testing.B) {
	m := newManager(nil, nil)
	r := &agentRecord{ID: "a"}
	hooks := m.hooksFor(r)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		hooks.Usage(lifetimeUsage{Input: 30, Output: 12, CacheRead: 5, Cost: 0.01})
	}
}

func BenchmarkUsageOfMessage(b *testing.B) {
	u := obj{"input": 30.0, "output": 12.0, "cacheRead": 5.0, "cacheWrite": 3.0, "cost": obj{"total": 0.01}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = usageOf(u)
	}
}

func BenchmarkAgentConversation(b *testing.B) {
	var msgs []map[string]any
	for i := 0; i < 50; i++ {
		msgs = append(msgs, obj{"role": "user", "content": "question"}, obj{"role": "assistant", "content": []any{obj{"type": "text", "text": "answer"}, obj{"type": "toolCall", "name": "bash"}}},
			obj{"role": "toolResult", "toolName": "bash", "content": []any{obj{"type": "text", "text": strings.Repeat("o", 300)}}})
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = getAgentConversation(msgs)
	}
}

func BenchmarkSchemaCheck(b *testing.B) {
	c := compileWorkflowSchema(structuredSchema).Compiled
	v := obj{"file": "a.ts", "line": 3.0}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = c.Check(v)
	}
}

func BenchmarkFormatCost(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = formatCost(0.0123)
	}
}

// The start-path cost of a transcript: the path (directories ensured) and the initial entry.
func BenchmarkAttachTranscriptFiles(b *testing.B) {
	b.Setenv("TMPDIR", b.TempDir())
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		path, err := createOutputFilePath("/work/project", "agent-"+strconv.Itoa(i), "session-1")
		if err != nil {
			b.Fatal(err)
		}
		writeInitialEntry(path, "a", "do the thing", "/work/project")
	}
}
