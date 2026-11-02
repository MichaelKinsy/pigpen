package pi_subagents

// The live child detail the widget, fleet and status render: the tool in flight, the child's
// token usage and the bounded activity tail — the original's current-tool, recent-output and
// usage fields on the Go port's surfaces.

import (
	"fmt"
	"strings"
	"testing"
)

func TestNoteChildEventTracksToolsAndUsage(t *testing.T) {
	r := &asyncRun{ID: "run-live", Agent: "w"}

	r.noteChildEvent(map[string]any{
		"type": "tool_execution_start", "toolCallId": "c1",
		"toolName": "read", "args": map[string]any{"path": "src/a.ts"},
	})
	r.mu.Lock()
	tool := r.toolLineLocked(nowMs())
	r.mu.Unlock()
	if !strings.HasPrefix(tool, "read: src/a.ts | ") {
		t.Errorf("tool line %q", tool)
	}

	// The stream's usage sets the window and the pending total; a message that carries no
	// usage of its own contributes the pending one, once.
	r.noteChildEvent(map[string]any{"type": "message_update", "usage": map[string]any{"input": 100.0, "output": 40.0, "cacheRead": 500.0}})
	r.noteChildEvent(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant"}})
	// A message's own usage is authoritative.
	r.noteChildEvent(map[string]any{"type": "message_end", "message": map[string]any{
		"role": "assistant", "usage": map[string]any{"input": 10.0, "output": 5.0, "cacheRead": 20.0},
	}})
	r.mu.Lock()
	spent, window := r.TokensSpent, r.TokensWindow
	r.mu.Unlock()
	eq(t, spent, int64(155), "spent: pending once, then the message's own usage")
	eq(t, window, int64(30), "window follows the latest usage")

	// The tool ends and the latest activity line takes over the detail; the tail stays bounded.
	r.noteChildEvent(map[string]any{"type": "tool_execution_end", "toolCallId": "c1"})
	for i := 0; i < recentLineCap+3; i++ {
		r.noteOutputLine(fmt.Sprintf("line %d", i))
	}
	r.mu.Lock()
	afterTool := r.toolLineLocked(nowMs())
	detail := r.liveDetailLocked(nowMs())
	lines := len(r.RecentLines)
	r.mu.Unlock()
	eq(t, afterTool, "", "tool detail clears on tool_execution_end")
	eq(t, detail, fmt.Sprintf("line %d", recentLineCap+2), "latest activity line")
	eq(t, lines, recentLineCap, "the tail is bounded")
}

func TestAsyncWidgetLinesShowsLiveDetail(t *testing.T) {
	now := int64(1000000)
	r := &asyncRun{
		ID: "r1", Agent: "w", State: "running",
		StartedAt: now - 5000, LastUpdate: now, TokensSpent: 10000,
		ActiveTools: map[string]activeToolState{"c1": {Display: "bash: ls", StartedAt: now - 2000}},
		RecentLines: []string{"answered earlier"},
	}
	lines := asyncWidgetLines([]*asyncRun{r}, now)
	eq(t, strings.Join(lines, "\n"),
		"w · running · 5.0s · active now · 10k spent\n  ⎿ bash: ls | 2.0s",
		"header with freshness and spend, current tool beneath")

	// Without a tool in flight the latest activity line shows what the child is doing.
	r.ActiveTools = nil
	lines = asyncWidgetLines([]*asyncRun{r}, now)
	eq(t, strings.Join(lines, "\n"),
		"w · running · 5.0s · active now · 10k spent\n  ⎿ answered earlier",
		"header and latest activity line")
}

func TestFormatRunStatusShowsToolAndTokens(t *testing.T) {
	isolateTemp(t)
	v := &runView{
		ID: "r1", State: "running", Mode: "single", Dir: "dir",
		StartedAt: 1000, LastUpdate: 2000, StepCount: 1,
		Steps:       []*asyncStep{{Agent: "helper", Status: "running"}},
		ToolLine:    "bash: ls | 2.0s",
		TokensWindow: 3000, TokensSpent: 10000,
	}
	out := formatRunStatus(v)
	for _, want := range []string{"Tokens: ", " window", "10k spent", "Tool: bash: ls | 2.0s"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
}
