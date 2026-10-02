package contextinfo

import (
	"sync"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestFmtNum(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1_000, "1.0k"},
		{1_500, "1.5k"},
		{10_000, "10.0k"},
		{999_999, "1000.0k"},
		{1_000_000, "1.0M"},
		{2_500_000, "2.5M"},
	}
	for _, tc := range cases {
		got := fmtNum(tc.in)
		if got != tc.want {
			t.Errorf("fmtNum(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFmtCost(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "$0.00"},
		{0.005, "$0.0050"},
		{0.001, "$0.0010"},
		{0.01, "$0.01"},
		{0.10, "$0.10"},
		{1.0, "$1.00"},
		{12.345, "$12.35"},
	}
	for _, tc := range cases {
		got := fmtCost(tc.in)
		if got != tc.want {
			t.Errorf("fmtCost(%f) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCommaFmt(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1_000, "1,000"},
		{10_000, "10,000"},
		{100_000, "100,000"},
		{1_000_000, "1,000,000"},
		{1_234_567, "1,234,567"},
	}
	for _, tc := range cases {
		got := commaFmt(tc.in)
		if got != tc.want {
			t.Errorf("commaFmt(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCharsToTokens(t *testing.T) {
	// Default charsPerToken = 3.7
	charsPerToken = 3.7
	cases := []struct {
		chars int
		want  int
	}{
		{0, 0},
		{1, 1},     // ceil(1/3.7) = 1
		{4, 2},     // ceil(4/3.7) = ceil(1.08) = 2
		{37, 10},   // ceil(37/3.7) = 10
		{370, 100}, // exact
	}
	for _, tc := range cases {
		got := charsToTokens(tc.chars)
		if got != tc.want {
			t.Errorf("charsToTokens(%d) = %d, want %d", tc.chars, got, tc.want)
		}
	}
}

func TestAnalyzeSystemPrompt(t *testing.T) {
	prompt := "You are helpful.\n<skill name=\"test\">some skill content</skill>\nDo things."
	a := analyzeSystemPrompt(prompt)

	if a.skillCount != 1 {
		t.Errorf("skillCount = %d, want 1", a.skillCount)
	}
	if a.totalChars != len(prompt) {
		t.Errorf("totalChars = %d, want %d", a.totalChars, len(prompt))
	}
	// skillChars = len(`<skill name="test">some skill content</skill>`) = 45
	if a.skillChars != 45 {
		t.Errorf("skillChars = %d, want 45", a.skillChars)
	}
	if a.baseChars != len(prompt)-45 {
		t.Errorf("baseChars = %d, want %d", a.baseChars, len(prompt)-45)
	}
}

func TestAnalyzeSystemPrompt_NoSkills(t *testing.T) {
	prompt := "Simple prompt with no skills."
	a := analyzeSystemPrompt(prompt)

	if a.skillCount != 0 {
		t.Errorf("skillCount = %d, want 0", a.skillCount)
	}
	if a.baseChars != len(prompt) {
		t.Errorf("baseChars = %d, want %d", a.baseChars, len(prompt))
	}
}

func TestAnalyzeConversation(t *testing.T) {
	branch := []sdk.BranchEntry{
		{Type: "message", Role: "user", Content: "hello"},
		{Type: "message", Role: "assistant", Content: "hi there", Thinking: "let me think",
			ToolCalls: []sdk.ToolCallInfo{{Name: "bash", Args: `{"command":"ls"}`}}},
		{Type: "message", Role: "toolResult", ToolName: "bash", ToolCallID: "tc1", Content: "file.go"},
		{Type: "other"},
	}

	a := analyzeConversation(branch)

	if a.messageCount != 3 {
		t.Errorf("messageCount = %d, want 3", a.messageCount)
	}
	if a.toolCallCount != 1 {
		t.Errorf("toolCallCount = %d, want 1", a.toolCallCount)
	}
	if a.toolResultCount != 1 {
		t.Errorf("toolResultCount = %d, want 1", a.toolResultCount)
	}
	if a.thinkingChars != len("let me think") {
		t.Errorf("thinkingChars = %d, want %d", a.thinkingChars, len("let me think"))
	}
}

func TestAnalyzeConversation_CompactionUsesFirstKeptEntryID(t *testing.T) {
	charsPerToken = 1
	branch := []sdk.BranchEntry{
		{ID: "old", Type: "message", Role: "user", Content: "oldoldold"},
		{ID: "kept", Type: "message", Role: "user", Content: "kept"},
		{ID: "compact", Type: "compaction", Content: "sum", FirstKeptEntryID: "kept"},
		{ID: "new", Type: "message", Role: "assistant", Content: "new"},
	}

	a := analyzeConversation(branch)
	if a.textChars != len("sum")+len("kept")+len("new") {
		t.Fatalf("textChars = %d, want summary + kept + new", a.textChars)
	}
	if a.messageCount != 3 {
		t.Fatalf("messageCount = %d, want summary + kept + new", a.messageCount)
	}
}

func TestGetSessionCostData(t *testing.T) {
	invalidateCostCache()
	branch := []sdk.BranchEntry{
		{Role: "assistant", ModelID: "test-model", Usage: &sdk.UsageInfo{Input: 1000, Output: 500, CacheRead: 200, CacheWrite: 100}},
		{Role: "assistant", ModelID: "test-model", Usage: &sdk.UsageInfo{Input: 2000, Output: 1000}},
		{Role: "user"}, // no usage — skipped
	}
	model := &sdk.ModelInfo{
		ID:                  "test-model",
		InputCostPer1M:      3.0,
		OutputCostPer1M:     15.0,
		CacheReadCostPer1M:  0.3,
		CacheWriteCostPer1M: 3.75,
	}

	d := getSessionCostData(branch, model)

	if d.input != 3000 {
		t.Errorf("input = %d, want 3000", d.input)
	}
	if d.output != 1500 {
		t.Errorf("output = %d, want 1500", d.output)
	}
	if d.cacheRead != 200 {
		t.Errorf("cacheRead = %d, want 200", d.cacheRead)
	}
	if d.cacheWrite != 100 {
		t.Errorf("cacheWrite = %d, want 100", d.cacheWrite)
	}
	// Cost: 3000*3/1M = 0.009, 1500*15/1M = 0.0225, 200*0.3/1M = 0.00006, 100*3.75/1M = 0.000375
	wantInput := 0.009
	if d.cost.input != wantInput {
		t.Errorf("cost.input = %f, want %f", d.cost.input, wantInput)
	}
	if d.cost.total == 0 {
		t.Error("cost.total should be > 0")
	}
}

func TestGetSessionCostData_FallsBackToCurrentModelForOldSessions(t *testing.T) {
	invalidateCostCache()
	branch := []sdk.BranchEntry{
		{Role: "assistant", Usage: &sdk.UsageInfo{Input: 1000, Output: 500, TotalTokens: 1500}},
	}
	model := &sdk.ModelInfo{ID: "claude-sonnet-4.5", Provider: "github-copilot"}

	d := getSessionCostData(branch, model)
	if d.cost.total == 0 {
		t.Fatal("cost.total = 0, want fallback Copilot pricing for current model")
	}
	if len(d.byModel) != 1 || d.byModel[0].modelID != "claude-sonnet-4.5" {
		t.Fatalf("byModel = %+v", d.byModel)
	}
	if d.totalTokens != 1500 {
		t.Fatalf("totalTokens = %d, want totalTokens from usage", d.totalTokens)
	}
}

func TestGetSessionCostData_ProviderQualifiedModelID(t *testing.T) {
	invalidateCostCache()
	branch := []sdk.BranchEntry{
		{Role: "assistant", ModelID: "github-copilot/claude-sonnet-4.5", Usage: &sdk.UsageInfo{Input: 1000, Output: 500}},
	}

	d := getSessionCostData(branch, nil)
	if d.cost.total == 0 {
		t.Fatal("cost.total = 0, want pricing for provider-qualified model id")
	}
	if len(d.byModel) != 1 || d.byModel[0].modelID != "claude-sonnet-4.5" {
		t.Fatalf("byModel = %+v", d.byModel)
	}
}

func TestGetSessionCostData_CacheKeyIncludesLeafID(t *testing.T) {
	invalidateCostCache()
	model := &sdk.ModelInfo{ID: "claude-sonnet-4.5", Provider: "github-copilot"}
	first := []sdk.BranchEntry{{ID: "leaf-a", Role: "assistant", ModelID: "claude-sonnet-4.5", Usage: &sdk.UsageInfo{Input: 1000, Output: 100}}}
	second := []sdk.BranchEntry{{ID: "leaf-b", Role: "assistant", ModelID: "claude-sonnet-4.5", Usage: &sdk.UsageInfo{Input: 2000, Output: 200}}}

	_ = getSessionCostData(first, model)
	got := getSessionCostData(second, model)
	if got.input != 2000 || got.output != 200 {
		t.Fatalf("cached stale cost data: input/output = %d/%d", got.input, got.output)
	}
}

func TestGetSessionCostData_NoModel(t *testing.T) {
	invalidateCostCache()
	branch := []sdk.BranchEntry{
		{Role: "assistant", Usage: &sdk.UsageInfo{Input: 1000, Output: 500}},
	}
	d := getSessionCostData(branch, nil)

	if d.input != 1000 {
		t.Errorf("input = %d, want 1000", d.input)
	}
	if d.cost.total != 0 {
		t.Errorf("cost.total = %f, want 0 (no model pricing)", d.cost.total)
	}
}

func TestAddAssistantUsageToCostCacheConcurrent(t *testing.T) {
	invalidateCostCache()
	model := &sdk.ModelInfo{ID: "claude-sonnet-4.5", Provider: "github-copilot", InputCostPer1M: 3, OutputCostPer1M: 15}

	const workers = 8
	const perWorker = 200
	var wg sync.WaitGroup
	wg.Add(workers * 2)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				addAssistantUsageToCostCache("claude-sonnet-4.5", sdk.UsageInfo{Input: 10, Output: 5, TotalTokens: 15}, model)
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				_, _ = cachedSessionCostData()
			}
		}()
	}
	wg.Wait()

	got, ok := cachedSessionCostData()
	if !ok {
		t.Fatal("cost cache missing after concurrent updates")
	}
	wantInput := workers * perWorker * 10
	wantOutput := workers * perWorker * 5
	if got.input != wantInput || got.output != wantOutput {
		t.Fatalf("lost updates under concurrency: input=%d (want %d) output=%d (want %d)", got.input, wantInput, got.output, wantOutput)
	}
	if len(got.byModel) != 1 || got.byModel[0].msgs != workers*perWorker {
		t.Fatalf("byModel = %+v, want one bucket with %d messages", got.byModel, workers*perWorker)
	}
}

func TestAddAssistantUsageToCostCacheUpdatesWithoutBranchRebuild(t *testing.T) {
	invalidateCostCache()
	model := &sdk.ModelInfo{ID: "claude-sonnet-4.5", Provider: "github-copilot"}
	_ = getSessionCostData([]sdk.BranchEntry{
		{ID: "leaf-a", Role: "assistant", ModelID: "claude-sonnet-4.5", Usage: &sdk.UsageInfo{Input: 1000, Output: 500, TotalTokens: 1500}},
	}, model)

	addAssistantUsageToCostCache("claude-sonnet-4.5", sdk.UsageInfo{Input: 2000, Output: 1000, TotalTokens: 3000}, model)
	got, ok := cachedSessionCostData()
	if !ok {
		t.Fatal("cost cache missing after incremental update")
	}
	if got.input != 3000 || got.output != 1500 || got.totalTokens != 4500 {
		t.Fatalf("incremental totals = input:%d output:%d total:%d", got.input, got.output, got.totalTokens)
	}
	if len(got.byModel) != 1 || got.byModel[0].msgs != 2 {
		t.Fatalf("byModel = %+v, want one bucket with two messages", got.byModel)
	}
}

func TestAssistantMapFromEventAcceptsNestedAndFlatShapes(t *testing.T) {
	cases := []map[string]any{
		{"message": map[string]any{"Assistant": map[string]any{"role": "assistant", "model": "m"}}},
		{"message": map[string]any{"assistant": map[string]any{"role": "assistant", "model": "m"}}},
		{"message": map[string]any{"role": "assistant", "model": "m"}},
	}
	for _, tc := range cases {
		got, ok := assistantMapFromEvent(tc)
		if !ok || got["model"] != "m" {
			t.Fatalf("assistantMapFromEvent(%#v) = %#v, %v", tc, got, ok)
		}
	}
	if _, ok := assistantMapFromEvent(map[string]any{"message": map[string]any{"User": map[string]any{"role": "user"}}}); ok {
		t.Fatal("user message parsed as assistant")
	}
}

func TestUsageInfoFromMapAcceptsProviderAndSDKNames(t *testing.T) {
	got := usageInfoFromMap(map[string]any{
		"input_tokens":  float64(10),
		"output_tokens": float64(20),
		"cache_read":    float64(3),
		"cache_write":   float64(4),
		"totalTokens":   float64(37),
	})
	if got.Input != 10 || got.Output != 20 || got.CacheRead != 3 || got.CacheWrite != 4 || got.TotalTokens != 37 {
		t.Fatalf("usageInfoFromMap = %+v", got)
	}
}

func TestGetSessionCostData_CacheKeyIncludesCurrentModel(t *testing.T) {
	invalidateCostCache()
	branch := []sdk.BranchEntry{
		{ID: "leaf", Role: "assistant", Usage: &sdk.UsageInfo{Input: 1000, Output: 500, TotalTokens: 1500}},
	}
	claude := &sdk.ModelInfo{ID: "claude-sonnet-4.5", Provider: "github-copilot"}
	gptMini := &sdk.ModelInfo{ID: "gpt-5-mini", Provider: "github-copilot"}

	first := getSessionCostData(branch, claude)
	second := getSessionCostData(branch, gptMini)

	if len(first.byModel) != 1 || first.byModel[0].modelID != "claude-sonnet-4.5" {
		t.Fatalf("first byModel = %+v", first.byModel)
	}
	if len(second.byModel) != 1 || second.byModel[0].modelID != "gpt-5-mini" {
		t.Fatalf("cache ignored current model; second byModel = %+v", second.byModel)
	}
	if first.cost.total == second.cost.total {
		t.Fatalf("cost cache reused stale model pricing: first=%f second=%f", first.cost.total, second.cost.total)
	}
}

func TestReconstructInitialToolCountsKeepsEmptyBranchFallbackArmed(t *testing.T) {
	reconstructInitialToolCounts(nil)
	if !needsBranchCountReconstruction() {
		t.Fatal("empty initial branch must keep before_agent_start reconstruction fallback armed")
	}

	reconstructInitialToolCounts([]sdk.BranchEntry{{ID: "u1", Role: "user"}})
	if needsBranchCountReconstruction() {
		t.Fatal("populated initial branch should satisfy reconstruction")
	}
}

func TestReconstructToolCountsFromBranch(t *testing.T) {
	branch := []sdk.BranchEntry{
		{Role: "assistant", ToolCalls: []sdk.ToolCallInfo{{Name: "bash"}, {Name: "read"}}},
		{Role: "user", ToolCalls: []sdk.ToolCallInfo{{Name: "ignored"}}},
		{Role: "assistant", ToolCalls: []sdk.ToolCallInfo{{Name: "bash"}, {}}},
	}

	reconstructToolCountsFromBranch(branch)

	mu.Lock()
	defer mu.Unlock()
	if totalToolCalls != 4 {
		t.Fatalf("totalToolCalls = %d, want 4", totalToolCalls)
	}
	if toolCallCounts["bash"] != 2 || toolCallCounts["read"] != 1 || toolCallCounts["unknown"] != 1 {
		t.Fatalf("toolCallCounts = %#v", toolCallCounts)
	}
}

func TestEstimateToolDefTokens(t *testing.T) {
	tools := []sdk.ToolInfo{
		{Name: "bash", Description: "Execute a command"},
		{Name: "read", Description: "Read a file"},
	}
	tokens, chars, _, total := estimateToolDefTokens(tools)
	if total != 2 {
		t.Errorf("totalCount = %d, want 2", total)
	}
	wantChars := len("bash") + len("Execute a command") + len("read") + len("Read a file")
	if chars != wantChars {
		t.Errorf("chars = %d, want %d", chars, wantChars)
	}
	if tokens == 0 {
		t.Error("tokens should be > 0")
	}
}
