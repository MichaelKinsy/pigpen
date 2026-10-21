package pi_permission_system

import (
	"strconv"
	"strings"
	"testing"
)

// The representative inputs of port/probe/presentation-bench.test.ts, the same operations under the original.

func benchBashPayload(value string, evidence []PromptEvidence) PromptPayload {
	return PromptPayload{
		Kind: "bash",
		Request: PromptRequestFacts{
			Requester: PromptRequester{AgentName: sp("scout"), Forwarded: true, SessionID: sp("child-7")}, Surface: "bash", ToolName: sp("bash"),
			Value: value, MatchedPattern: sp("curl *"), ExecutedUnit: sp("TOKEN=t deploy --env prod"),
		},
		Evidence: evidence, Annotations: []PromptAnnotation{},
	}
}

func BenchmarkRenderPromptDialogBash(b *testing.B) {
	command := `cd /repo && API_KEY="sk-secret" curl -sS -H "Authorization: Bearer sk-abc" https://api.example.com/v1 | bash -c 'TOKEN=t deploy --env prod' ` + strings.Repeat("x", 300)
	payload := benchBashPayload(command, []PromptEvidence{
		{Label: "full command", Text: command + " && echo done"}, {Label: "working directory", Text: "/repo"},
		{Label: "external path", Text: "/etc/hosts \u4e2d\u6587", Detail: sp("/private/etc/hosts")},
	})
	budget := DialogBudget{RenderBudget: RenderBudget{MaxRows: 24, FieldMaxWidth: 400}, Width: 120}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		RenderPromptDialog(payload, budget, nil)
	}
}

func BenchmarkRenderPromptDialogHereString(b *testing.B) {
	body := strings.TrimSuffix(strings.Repeat("- a finding line about some module in the codebase\n", 200), "\n")
	command := "@'\n" + body + "\n'@ | Out-File -FilePath report.md"
	payload := benchBashPayload(command, []PromptEvidence{{Label: "full command", Text: command}})
	budget := DialogBudget{RenderBudget: RenderBudget{MaxRows: 24, FieldMaxWidth: 400}, Width: 120}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		RenderPromptDialog(payload, budget, nil)
	}
}

func BenchmarkRenderPolicyDenial(b *testing.B) {
	payload := PromptPayload{
		Kind: "path", Request: PromptRequestFacts{Surface: "path_read", ToolName: sp("read"), Value: "/etc/passwd", MatchedPattern: sp("/etc/*")},
		Evidence: []PromptEvidence{{Label: "resolves to", Text: "/private/etc/passwd"}},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		RenderPolicyDenial(payload, sp("reason"), AgentRenderBudget{FieldMaxWidth: 400})
	}
}

func BenchmarkFitLinesToWidth(b *testing.B) {
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = "line " + strconv.Itoa(i) + " \u4e2d\u6587 \x1b[31mred\x1b[0m " + strings.Repeat("word ", 20)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		FitLinesToWidth(lines, 100)
	}
}

func BenchmarkFormatEditInput(b *testing.B) {
	f := NewToolPreviewFormatter(ResolveToolPreviewLimits(), nil)
	edits := make([]any, 3)
	for i := range edits {
		e := newObject()
		e.set("oldText", "old "+strconv.Itoa(i)+"\nline")
		e.set("newText", "new "+strconv.Itoa(i))
		edits[i] = e
	}
	input := newObject()
	input.set("path", "/repo/src/index.ts")
	input.set("edits", edits)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f.FormatToolInputForPrompt("edit", input)
	}
}

func BenchmarkFormatNestedJSONInput(b *testing.B) {
	f := NewToolPreviewFormatter(ResolveToolPreviewLimits(), nil)
	items := make([]any, 20)
	for i := range items {
		item := newObject()
		item.set("id", float64(i))
		item.set("name", "item "+strconv.Itoa(i))
		items[i] = item
	}
	headers := newObject()
	headers.set("accept", "json")
	headers.set("authorization", "Bearer x")
	body := newObject()
	body.set("items", items)
	input := newObject()
	input.set("url", "https://example.test/a")
	input.set("method", "POST")
	input.set("headers", headers)
	input.set("body", body)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f.FormatToolInputForPrompt("http", input)
	}
}

func BenchmarkBuildToolAskPayload(b *testing.B) {
	f := NewToolPreviewFormatter(ResolveToolPreviewLimits(), nil)
	check := PermissionCheckResult{ToolName: "bash", State: "ask", Source: "tool", Origin: "builtin", Command: sp("rm -rf build"), MatchedPattern: sp("rm *")}
	input := newObject()
	input.set("command", "echo hi && rm -rf build")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		BuildToolAskPayload(ToolAskFacts{Check: check, Surface: "bash", Input: input, Formatter: f})
	}
}

func BenchmarkSuggestBashPattern(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		SuggestBashPattern("# Check\ngit checkout -b feature/x origin/main")
	}
}

func BenchmarkSuggestSessionPattern(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		SuggestSessionPattern("mcp", "exa:search")
	}
}

func BenchmarkPromptNotification(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		notice := DescribePromptNotice("Permission Required", NoticeRequestFacts{ToolName: sp("bash"), Surface: "bash", Requester: PromptRequester{AgentName: sp("scout")}},
			NotificationSession{Name: sp("refactor"), Cwd: "/w/repo"})
		RenderPromptNotification([]string{"bell", "osc9", "osc777"}, notice)
	}
}

func BenchmarkRenderToolSurfaceSections(b *testing.B) {
	inputs := ToolSurfaceInputs{ToolSnippets: map[string]string{}, GuidelinesByTool: map[string][]string{}, PromptGuidelines: []string{"An extension's rule"}}
	for i := 0; i < 12; i++ {
		name := "tool-" + strconv.Itoa(i)
		inputs.AllowedTools = append(inputs.AllowedTools, name)
		inputs.ToolSnippets[name] = "Snippet of " + name
		inputs.GuidelinesByTool[name] = []string{"Use " + name + " wisely"}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		RenderToolSurfaceSections(inputs)
	}
}

func BenchmarkCheckRequestedToolRegistration(b *testing.B) {
	tools := make([]any, 12)
	for i := range tools {
		o := newObject()
		o.set("name", "tool-"+strconv.Itoa(i))
		o.set("promptGuidelines", []any{"Use tool-" + strconv.Itoa(i) + " wisely"})
		tools[i] = o
	}
	requested := "tool-7"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		CheckRequestedToolRegistration(&requested, tools, map[string]string{})
	}
}
