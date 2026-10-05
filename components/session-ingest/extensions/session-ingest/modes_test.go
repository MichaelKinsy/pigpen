package sessioningest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Cases for the modes the retrieval hints point at: turn, tools and stats.

func writeRichFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rich.jsonl")
	content := strings.Join([]string{
		`{"type":"session","id":"session-rich-000000","version":"3","cwd":"/repo","timestamp":"2026-05-24T10:00:00.000Z"}`,
		`{"type":"message","timestamp":"2026-05-24T10:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"Fix the parser"}]}}`,
		`{"type":"message","timestamp":"2026-05-24T10:00:02.000Z","message":{"role":"assistant","provider":"acme","model":"m-1","content":[{"type":"thinking","thinking":"secret reasoning"},{"type":"text","text":"Reading first."},{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"parser.go"}},{"type":"toolCall","id":"c2","name":"bash","arguments":{"command":"go test ./..."}}],"usage":{"input":100,"output":50,"cacheRead":10,"cacheWrite":5,"totalTokens":165,"cost":{"total":0.5}}}}`,
		`{"type":"message","timestamp":"2026-05-24T10:00:03.000Z","message":{"role":"toolResult","toolName":"read","toolCallId":"c1","content":[{"type":"text","text":"package parser"}]}}`,
		`{"type":"message","timestamp":"2026-05-24T10:00:04.000Z","message":{"role":"toolResult","toolName":"bash","toolCallId":"c2","isError":true,"content":[{"type":"text","text":"FAIL parser"}]}}`,
		`{"type":"message","timestamp":"2026-05-24T10:00:05.000Z","message":{"role":"assistant","provider":"acme","model":"m-1","content":[{"type":"toolCall","id":"c3","name":"edit","arguments":{"path":"parser.go","edits":[]}}],"usage":{"input":200,"output":20,"totalTokens":220,"cost":{"total":0.25}}}}`,
		`{"type":"message","timestamp":"2026-05-24T10:00:06.000Z","message":{"role":"toolResult","toolName":"edit","toolCallId":"c3","content":[{"type":"text","text":"edited"}]}}`,
		`{"type":"message","timestamp":"2026-05-24T10:00:07.000Z","message":{"role":"user","content":"Thanks"}}`,
		`{"type":"message","timestamp":"2026-05-24T10:00:10.000Z","message":{"role":"assistant","provider":"acme","model":"m-2","stopReason":"error","errorMessage":"rate limited","content":[]}}`,
		`{"type":"model_change","provider":"acme","modelId":"m-2","timestamp":"2026-05-24T10:00:08.000Z"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func run(t *testing.T, params map[string]any) any {
	t.Helper()
	got, err := handleReadSession(sdk.Context{}, params)
	if err != nil {
		t.Fatalf("handleReadSession(%v) error = %v", params, err)
	}
	return got
}

func TestTurnModeJSONReturnsTheWholeTurn(t *testing.T) {
	report, ok := run(t, map[string]any{"path": writeRichFixture(t), "mode": "turn", "turn": 1, "format": "json"}).(turnReport)
	if !ok {
		t.Fatal("result is not a turnReport")
	}
	if report.Turn != 1 || report.TotalTurns != 2 {
		t.Fatalf("turn/total = %d/%d, want 1/2", report.Turn, report.TotalTurns)
	}
	roles := []string{}
	for _, m := range report.Messages {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "user,assistant,toolResult,toolResult,assistant,toolResult" {
		t.Fatalf("roles = %v", roles)
	}
}

func TestTurnModeDefaultsAreTokenSafe(t *testing.T) {
	res := run(t, map[string]any{"path": writeRichFixture(t), "mode": "turn", "turn": 1}).(sdk.ToolResult)
	text := res.Content
	for _, want := range []string{"# Session turn 1 of 2", "Fix the parser", "Reading first.", "read", "bash", "errors"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	// Raw thinking and tool results are hidden unless asked for.
	for _, hidden := range []string{"secret reasoning", "package parser", "FAIL parser"} {
		if strings.Contains(text, hidden) {
			t.Errorf("default output leaks %q:\n%s", hidden, text)
		}
	}
	if res.Preview == "" {
		t.Error("expected a Preview")
	}
}

func TestTurnModeIncludeOptIns(t *testing.T) {
	res := run(t, map[string]any{
		"path": writeRichFixture(t), "mode": "turn", "turn": 1,
		"include": []any{"text", "thinking", "tool_results", "usage"},
	}).(sdk.ToolResult)
	for _, want := range []string{"secret reasoning", "package parser", "FAIL parser", "$0.5000"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("missing %q:\n%s", want, res.Content)
		}
	}
}

func TestTurnModeHonorsMaxCharsPerItem(t *testing.T) {
	res := run(t, map[string]any{
		"path": writeRichFixture(t), "mode": "turn", "turn": 1, "include": []any{"text", "tool_results"}, "maxCharsPerItem": 4,
	}).(sdk.ToolResult)
	if strings.Contains(res.Content, "package parser") || !strings.Contains(res.Content, "truncated") {
		t.Fatalf("expected truncated tool result:\n%s", res.Content)
	}
}

func TestTurnModeValidatesTurn(t *testing.T) {
	path := writeRichFixture(t)
	for name, params := range map[string]map[string]any{
		"missing": {"path": path, "mode": "turn"},
		"zero":    {"path": path, "mode": "turn", "turn": 0},
		"past":    {"path": path, "mode": "turn", "turn": 3},
	} {
		if _, err := handleReadSession(sdk.Context{}, params); err == nil || !strings.Contains(err.Error(), "turn") {
			t.Errorf("%s: err = %v, want a turn error", name, err)
		}
	}
}

func TestToolsModeCountsCallsResultsErrorsAndFiles(t *testing.T) {
	report, ok := run(t, map[string]any{"path": writeRichFixture(t), "mode": "tools", "format": "json"}).(toolsReport)
	if !ok {
		t.Fatal("result is not a toolsReport")
	}
	byName := map[string]toolUsage{}
	for _, u := range report.Tools {
		byName[u.Name] = u
	}
	if u := byName["bash"]; u.Calls != 1 || u.Results != 1 || u.Errors != 1 {
		t.Errorf("bash = %+v", u)
	}
	if u := byName["read"]; u.Calls != 1 || u.Results != 1 || u.Errors != 0 || u.FirstTurn != 1 {
		t.Errorf("read = %+v", u)
	}
	if report.TotalCalls != 3 {
		t.Errorf("TotalCalls = %d, want 3", report.TotalCalls)
	}
	files := map[string]string{}
	for _, f := range report.Files {
		files[f.Path] = strings.Join(f.Tools, "+")
	}
	if files["parser.go"] != "edit+read" {
		t.Errorf("files = %v, want parser.go touched by edit+read", files)
	}
}

func TestToolsModeMarkdownAndTurnFilter(t *testing.T) {
	res := run(t, map[string]any{"path": writeRichFixture(t), "mode": "tools", "turn": 2}).(sdk.ToolResult)
	if !strings.Contains(res.Content, "# Session tools") || !strings.Contains(res.Content, "No tool calls") {
		t.Fatalf("turn 2 has no tools:\n%s", res.Content)
	}
	res = run(t, map[string]any{"path": writeRichFixture(t), "mode": "tools"}).(sdk.ToolResult)
	for _, want := range []string{"| bash |", "| read |", "parser.go"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("missing %q:\n%s", want, res.Content)
		}
	}
}

func TestStatsModeTotals(t *testing.T) {
	report, ok := run(t, map[string]any{"path": writeRichFixture(t), "mode": "stats", "format": "json"}).(statsReport)
	if !ok {
		t.Fatal("result is not a statsReport")
	}
	if report.Turns != 2 || report.Messages != 8 {
		t.Errorf("turns/messages = %d/%d, want 2/8", report.Turns, report.Messages)
	}
	want := map[string]int{"user": 2, "assistant": 3, "toolResult": 3}
	for role, n := range want {
		if report.Roles[role] != n {
			t.Errorf("roles[%s] = %d, want %d", role, report.Roles[role], n)
		}
	}
	if report.Usage.Input != 300 || report.Usage.Output != 70 || report.Usage.Total != 385 || report.Usage.Cost != 0.75 {
		t.Errorf("usage = %+v", report.Usage)
	}
	if report.ToolCalls != 3 || report.Errors != 2 {
		t.Errorf("toolCalls/errors = %d/%d, want 3/2 (one tool error, one provider error)", report.ToolCalls, report.Errors)
	}
	models := map[string]int{}
	for _, m := range report.Models {
		models[m.Model] = m.Messages
	}
	if models["m-1"] != 2 || models["m-2"] != 1 {
		t.Errorf("models = %v", models)
	}
	if report.DurationSeconds != 9 {
		t.Errorf("duration = %d, want 9 (first to last message)", report.DurationSeconds)
	}
}

func TestStatsModeMarkdown(t *testing.T) {
	res := run(t, map[string]any{"path": writeRichFixture(t), "mode": "stats"}).(sdk.ToolResult)
	for _, want := range []string{"# Session stats", "turns: 2", "tokens", "$0.7500", "m-1", "m-2"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("missing %q:\n%s", want, res.Content)
		}
	}
}

func TestSchemaHasNoUnusedFields(t *testing.T) {
	props := readSessionSchema()["properties"].(map[string]any)
	if _, ok := props["contextRadius"]; ok {
		t.Error("contextRadius is not used by any mode and must not be in the schema")
	}
}

func TestContinuationHintOnlyWhenThereIsMore(t *testing.T) {
	path := writeRichFixture(t)
	all := run(t, map[string]any{"path": path, "mode": "toc", "limit": 10}).(sdk.ToolResult).Content
	if strings.Contains(all, "[Next:") {
		t.Errorf("all turns fit but a Next hint was added:\n%s", all)
	}
	exact := run(t, map[string]any{"path": path, "mode": "toc", "limit": 2}).(sdk.ToolResult).Content
	if strings.Contains(exact, "[Next:") {
		t.Errorf("the page ends exactly at the last turn but a Next hint was added:\n%s", exact)
	}
	partial := run(t, map[string]any{"path": path, "mode": "toc", "limit": 1}).(sdk.ToolResult).Content
	if !strings.Contains(partial, `"start":1`) {
		t.Errorf("partial page needs a hint for start=1:\n%s", partial)
	}
	for _, mode := range []string{"stats"} {
		out := run(t, map[string]any{"path": path, "mode": mode}).(sdk.ToolResult).Content
		if strings.Contains(out, "[Next:") {
			t.Errorf("mode %s has no pages but got a Next hint:\n%s", mode, out)
		}
	}
}

func TestTruncationNeverSplitsARune(t *testing.T) {
	path := filepath.Join(t.TempDir(), "u.jsonl")
	line := `{"type":"message","message":{"role":"user","content":"` + strings.Repeat("é", 50) + `"}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := run(t, map[string]any{"path": path, "mode": "turn", "turn": 1, "maxCharsPerItem": 11}).(sdk.ToolResult)
	if !utf8.ValidString(res.Content) {
		t.Fatalf("output is not valid UTF-8: %q", res.Content)
	}
	q := run(t, map[string]any{"path": path, "mode": "query", "query": "é", "maxCharsPerItem": 11, "format": "json"}).(queryReport)
	if !utf8.ValidString(q.Hits[0].Excerpt) {
		t.Fatalf("excerpt is not valid UTF-8: %q", q.Hits[0].Excerpt)
	}
}

func TestOversizedTurnExplainsHowToNarrow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.jsonl")
	line := `{"type":"message","message":{"role":"user","content":"` + strings.Repeat("x", 60*1024) + `"}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := run(t, map[string]any{"path": path, "mode": "turn", "turn": 1}).(sdk.ToolResult).Content
	if !strings.Contains(out, "maxCharsPerItem") {
		t.Errorf("truncated turn should say how to narrow it:\n%s", out[len(out)-300:])
	}
	if len(out) > 51*1024 {
		t.Errorf("output is %d bytes, over the 50 KB cap", len(out))
	}
}
