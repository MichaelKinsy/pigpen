package sessioningest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestReadSessionTOCJSON(t *testing.T) {
	path := writeSessionFixture(t)
	got, err := handleReadSession(sdk.Context{}, map[string]any{
		"path":   path,
		"mode":   "toc",
		"format": "json",
		"limit":  10,
	})
	if err != nil {
		t.Fatalf("handleReadSession() error = %v", err)
	}
	report, ok := got.(tocReport)
	if !ok {
		t.Fatalf("result type = %T, want tocReport", got)
	}
	if report.TotalTurns != 2 {
		t.Fatalf("TotalTurns = %d, want 2", report.TotalTurns)
	}
	if report.Session.ID != "session-1234567890" {
		t.Fatalf("session ID = %q", report.Session.ID)
	}
	if len(report.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(report.Items))
	}
	first := report.Items[0]
	if first.TurnIndex != 1 || first.EntryIndex != 2 || first.MessageIndex != 1 {
		t.Fatalf("first indices = %+v", first)
	}
	if first.User != "Please inspect the repo" {
		t.Fatalf("first user = %q", first.User)
	}
	if strings.Join(first.Tools, ",") != "bash,read" {
		t.Fatalf("first tools = %v, want bash/read", first.Tools)
	}
	if first.AssistantMessages != 1 || first.ToolResults != 1 || first.Errors != 1 {
		t.Fatalf("first counts = %+v", first)
	}
	if first.Usage.Total != 30 || first.Usage.Cost != 0.12 {
		t.Fatalf("first usage = %+v", first.Usage)
	}
}

func TestReadSessionTOCMarkdown(t *testing.T) {
	path := writeSessionFixture(t)
	got, err := handleReadSession(sdk.Context{}, map[string]any{
		"path":   path,
		"mode":   "toc",
		"format": "compact_markdown",
		"limit":  1,
	})
	if err != nil {
		t.Fatalf("handleReadSession() error = %v", err)
	}
	result, ok := got.(sdk.ToolResult)
	if !ok {
		t.Fatalf("result type = %T, want sdk.ToolResult", got)
	}
	text := result.Content
	for _, want := range []string{"# Session TOC", "session-123", "| 1 |", "Please inspect the repo", "bash, read", "errors:1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("markdown missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "| 2 |") {
		t.Fatalf("markdown included turn 2 despite limit=1:\n%s", text)
	}
	if result.Preview == "" {
		t.Fatal("expected non-empty Preview")
	}
	if !strings.Contains(result.Preview, "2 turns") {
		t.Errorf("preview should mention total turns: %q", result.Preview)
	}
}

func TestReadSessionQueryJSON(t *testing.T) {
	path := writeSessionFixture(t)
	got, err := handleReadSession(sdk.Context{}, map[string]any{
		"path":   path,
		"mode":   "query",
		"query":  "inspect",
		"format": "json",
		"limit":  10,
	})
	if err != nil {
		t.Fatalf("handleReadSession() error = %v", err)
	}
	report, ok := got.(queryReport)
	if !ok {
		t.Fatalf("result type = %T, want queryReport", got)
	}
	if report.TotalHits != 2 {
		t.Fatalf("TotalHits = %d, want 2", report.TotalHits)
	}
	if len(report.Hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(report.Hits))
	}
	if report.Hits[0].Role != "user" || report.Hits[0].TurnIndex != 1 || !strings.Contains(report.Hits[0].Excerpt, "inspect") {
		t.Fatalf("first hit = %+v, want user turn 1 inspect", report.Hits[0])
	}
	if report.Hits[1].Role != "assistant" || report.Hits[1].TurnIndex != 1 {
		t.Fatalf("second hit = %+v, want assistant turn 1", report.Hits[1])
	}
}

func TestReadSessionQueryFindsToolResultsAndHonorsCaseSensitivity(t *testing.T) {
	path := writeSessionFixture(t)
	got, err := handleReadSession(sdk.Context{}, map[string]any{
		"path":          path,
		"mode":          "query",
		"query":         "BOOM",
		"caseSensitive": false,
		"format":        "json",
	})
	if err != nil {
		t.Fatalf("handleReadSession() error = %v", err)
	}
	report := got.(queryReport)
	if report.TotalHits != 1 || report.Hits[0].Role != "toolResult" || report.Hits[0].ToolName != "bash" {
		t.Fatalf("report = %+v, want bash toolResult hit", report)
	}

	got, err = handleReadSession(sdk.Context{}, map[string]any{
		"path":          path,
		"mode":          "query",
		"query":         "BOOM",
		"caseSensitive": true,
		"format":        "json",
	})
	if err != nil {
		t.Fatalf("handleReadSession() error = %v", err)
	}
	report = got.(queryReport)
	if report.TotalHits != 0 {
		t.Fatalf("case-sensitive TotalHits = %d, want 0", report.TotalHits)
	}
}

func TestReadSessionQueryMarkdown(t *testing.T) {
	path := writeSessionFixture(t)
	got, err := handleReadSession(sdk.Context{}, map[string]any{
		"path":   path,
		"mode":   "query",
		"query":  "Done",
		"format": "compact_markdown",
	})
	if err != nil {
		t.Fatalf("handleReadSession() error = %v", err)
	}
	result, ok := got.(sdk.ToolResult)
	if !ok {
		t.Fatalf("result type = %T, want sdk.ToolResult", got)
	}
	text := result.Content
	for _, want := range []string{"# Session query", "Done", "| 1 | 2 | assistant", "mode=turn turn=2"} {
		if !strings.Contains(text, want) {
			t.Fatalf("markdown missing %q:\n%s", want, text)
		}
	}
	if result.Preview == "" {
		t.Fatal("expected non-empty Preview")
	}
}

func TestReadSessionSliceJSONWithRoleFilter(t *testing.T) {
	path := writeSessionFixture(t)
	got, err := handleReadSession(sdk.Context{}, map[string]any{
		"path":   path,
		"mode":   "slice",
		"format": "json",
		"start":  0,
		"limit":  2,
		"roles":  []any{"assistant"},
	})
	if err != nil {
		t.Fatalf("handleReadSession() error = %v", err)
	}
	report, ok := got.(sliceReport)
	if !ok {
		t.Fatalf("result type = %T, want sliceReport", got)
	}
	if report.FilteredTotal != 2 || len(report.Messages) != 2 {
		t.Fatalf("filtered/messages = %d/%d, want 2/2", report.FilteredTotal, len(report.Messages))
	}
	if report.Messages[0].Role != "assistant" || report.Messages[0].TurnIndex != 1 || len(report.Messages[0].ToolCalls) != 2 {
		t.Fatalf("first slice message = %+v", report.Messages[0])
	}
	if report.Messages[1].TurnIndex != 2 || report.Messages[1].Text[0] != "Done." {
		t.Fatalf("second slice message = %+v", report.Messages[1])
	}
}

func TestReadSessionSliceMarkdown(t *testing.T) {
	path := writeSessionFixture(t)
	got, err := handleReadSession(sdk.Context{}, map[string]any{
		"path":   path,
		"mode":   "slice",
		"format": "compact_markdown",
		"start":  1,
		"limit":  2,
	})
	if err != nil {
		t.Fatalf("handleReadSession() error = %v", err)
	}
	result, ok := got.(sdk.ToolResult)
	if !ok {
		t.Fatalf("result type = %T, want sdk.ToolResult", got)
	}
	text := result.Content
	for _, want := range []string{"# Session slice", "message 2 turn 1 assistant", "tool_calls: bash, read", "message 3 turn 1 toolResult"} {
		if !strings.Contains(text, want) {
			t.Fatalf("markdown missing %q:\n%s", want, text)
		}
	}
	if result.Preview == "" {
		t.Fatal("expected non-empty Preview")
	}
}

func TestReadSessionQueryRequiresQuery(t *testing.T) {
	path := writeSessionFixture(t)
	_, err := handleReadSession(sdk.Context{}, map[string]any{"path": path, "mode": "query"})
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("err = %v, want query required", err)
	}
}

func TestReadSessionRejectsUnknownMode(t *testing.T) {
	_, err := handleReadSession(sdk.Context{}, map[string]any{"path": "x", "mode": "missing"})
	if err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("err = %v, want unknown mode", err)
	}
}

func TestReadSessionReportsMissingPath(t *testing.T) {
	_, err := handleReadSession(sdk.Context{}, map[string]any{"mode": "toc"})
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("err = %v, want path required", err)
	}
}

func writeSessionFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := strings.Join([]string{
		`{"type":"session","id":"session-1234567890","version":"0.75.4","cwd":"/repo","timestamp":"2026-05-24T10:00:00.000Z"}`,
		`{"type":"message","timestamp":"2026-05-24T10:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"Please inspect the repo"}]}}`,
		`{"type":"message","timestamp":"2026-05-24T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"I will inspect it."},{"type":"toolCall","id":"call-1","name":"bash","arguments":{"command":"ls"}},{"type":"toolCall","id":"call-2","name":"read","arguments":{"path":"README.md"}}],"usage":{"input":10,"output":20,"totalTokens":30,"cost":{"total":0.12}}}}`,
		`{"type":"message","timestamp":"2026-05-24T10:00:03.000Z","message":{"role":"toolResult","toolName":"bash","isError":true,"content":[{"type":"text","text":"boom"}]}}`,
		`{"type":"message","timestamp":"2026-05-24T10:00:04.000Z","message":{"role":"user","content":[{"type":"text","text":"Continue"}]}}`,
		`{"type":"message","timestamp":"2026-05-24T10:00:05.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Done."}],"usage":{"input":1,"output":2,"totalTokens":3}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
