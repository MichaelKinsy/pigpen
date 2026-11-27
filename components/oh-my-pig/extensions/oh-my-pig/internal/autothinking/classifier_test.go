package autothinking

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func reasoningModel(levels ...string) map[string]any {
	all := []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	supported := make(map[string]bool, len(levels))
	for _, l := range levels {
		supported[l] = true
	}
	levelMap := make(map[string]any, len(all))
	for _, l := range all {
		if supported[l] {
			levelMap[l] = l
		} else {
			levelMap[l] = nil
		}
	}
	return map[string]any{
		"provider":         "openai",
		"id":               "gpt-5.4",
		"reasoning":        true,
		"thinkingLevelMap": levelMap,
	}
}

func makeAssistantReply(text string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"stopReason": "stop",
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
	})
	return raw
}

func resetHooks(t *testing.T) {
	t.Helper()
	prevSetting := Setting
	prevResolve := ResolveModelRole
	prevComplete := CompleteFn
	prevIsSub := IsSubagent
	ResetCacheForTests()
	ResetSessionsForTests()
	t.Cleanup(func() {
		Setting = prevSetting
		ResolveModelRole = prevResolve
		CompleteFn = prevComplete
		IsSubagent = prevIsSub
		ResetCacheForTests()
		ResetSessionsForTests()
	})
}

func TestClassifyDifficulty_SolutionSpaceExactPromptAndLow(t *testing.T) {
	resetHooks(t)

	var capturedReq map[string]any
	var calls atomic.Int32
	CompleteFn = func(_ context.Context, _ sdk.Context, _ map[string]any, req, _ map[string]any) (json.RawMessage, error) {
		calls.Add(1)
		capturedReq = req
		return makeAssistantReply("low"), nil
	}

	model := reasoningModel("low", "medium", "high")
	got, err := ClassifyDifficulty(context.Background(), sdk.Context{}, Input{
		Request:       "some ignored full prompt",
		SolutionSpace: "one fixed reply",
		TargetModel:   model,
	})
	if err != nil {
		t.Fatalf("ClassifyDifficulty error: %v", err)
	}
	if got != "low" {
		t.Fatalf("got %q, want %q", got, "low")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	if capturedReq["tools"] != nil {
		t.Fatalf("expected tools == nil, got %#v", capturedReq["tools"])
	}
	msgs, _ := capturedReq["messages"].([]map[string]any)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	blocks, _ := msgs[0]["content"].([]map[string]any)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(blocks))
	}
	userText, _ := blocks[0]["text"].(string)
	if !strings.Contains(userText, "<solution_space>one fixed reply</solution_space>") {
		t.Fatalf("user prompt missing <solution_space> tag:\n%s", userText)
	}
	wantCue := "Answer with exactly one of: `low`, `medium`, `high`."
	if !strings.Contains(userText, wantCue) {
		t.Fatalf("user prompt missing cue %q:\n%s", wantCue, userText)
	}
	if strings.Contains(userText, "<request>") {
		t.Fatalf("solutionSpace classification should not include <request>:\n%s", userText)
	}
}

func TestClassifyDifficulty_UltrathinkAndChillaxBypass(t *testing.T) {
	resetHooks(t)

	var calls atomic.Int32
	CompleteFn = func(_ context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		calls.Add(1)
		return makeAssistantReply("low"), nil
	}

	// Model supports max; default autoThinkingMaxEffort is xhigh, but ultrathink bypasses ceiling to max.
	model := reasoningModel("low", "medium", "high", "xhigh", "max")
	got, err := ClassifyDifficulty(context.Background(), sdk.Context{}, Input{
		Request:     "please ultrathink about this distributed consensus bug",
		TargetModel: model,
	})
	if err != nil {
		t.Fatalf("ultrathink error: %v", err)
	}
	if got != "max" {
		t.Fatalf("ultrathink got %q, want %q", got, "max")
	}
	if calls.Load() != 0 {
		t.Fatalf("ultrathink should make 0 LLM calls, got %d", calls.Load())
	}

	// chillax returns low with 0 LLM calls.
	got, err = ClassifyDifficulty(context.Background(), sdk.Context{}, Input{
		Request:     "chillax and rename foo to bar",
		TargetModel: model,
	})
	if err != nil {
		t.Fatalf("chillax error: %v", err)
	}
	if got != "low" {
		t.Fatalf("chillax got %q, want %q", got, "low")
	}
	if calls.Load() != 0 {
		t.Fatalf("chillax should make 0 LLM calls, got %d", calls.Load())
	}

	// Disabling magicKeywords.ultrathink routes through the judge instead.
	Setting = func(_ sdk.Context, key string) any {
		if key == "magicKeywords.ultrathink" {
			return false
		}
		return nil
	}
	got, err = ClassifyDifficulty(context.Background(), sdk.Context{}, Input{
		Request:     "ultrathink simple rename",
		TargetModel: model,
	})
	if err != nil {
		t.Fatalf("disabled ultrathink error: %v", err)
	}
	if got != "low" {
		t.Fatalf("disabled ultrathink got %q, want %q", got, "low")
	}
	if calls.Load() != 1 {
		t.Fatalf("disabled ultrathink should invoke judge once, got %d", calls.Load())
	}
}

func TestClassifyDifficulty_MaxEffortCeilingSetting(t *testing.T) {
	resetHooks(t)

	model := reasoningModel("low", "medium", "high", "xhigh", "max")
	var lastUser string
	CompleteFn = func(_ context.Context, _ sdk.Context, _ map[string]any, req, _ map[string]any) (json.RawMessage, error) {
		msgs := req["messages"].([]map[string]any)
		blocks := msgs[0]["content"].([]map[string]any)
		lastUser = blocks[0]["text"].(string)
		if strings.Contains(lastUser, "`max`") {
			return makeAssistantReply("max"), nil
		}
		return makeAssistantReply("xhigh"), nil
	}

	// Default providers.autoThinkingMaxEffort caps at xhigh (no `max` offered).
	got, err := ClassifyDifficulty(context.Background(), sdk.Context{}, Input{
		Request:     "diagnose intermittent race condition",
		TargetModel: model,
	})
	if err != nil {
		t.Fatalf("default ceiling error: %v", err)
	}
	if got != "xhigh" {
		t.Fatalf("default ceiling got %q, want %q", got, "xhigh")
	}
	if strings.Contains(lastUser, "`max`") {
		t.Fatalf("default ceiling must not offer max:\n%s", lastUser)
	}

	// Setting providers.autoThinkingMaxEffort = "max" offers and allows max.
	Setting = func(_ sdk.Context, key string) any {
		if key == "providers.autoThinkingMaxEffort" {
			return "max"
		}
		return nil
	}
	got, err = ClassifyDifficulty(context.Background(), sdk.Context{}, Input{
		Request:     "diagnose intermittent race condition",
		TargetModel: model,
	})
	if err != nil {
		t.Fatalf("max ceiling error: %v", err)
	}
	if got != "max" {
		t.Fatalf("max ceiling got %q, want %q", got, "max")
	}
	if !strings.Contains(lastUser, "Answer with exactly one of: `low`, `medium`, `high`, `xhigh`, `max`.") {
		t.Fatalf("max ceiling prompt missing max option:\n%s", lastUser)
	}
}

func TestClassifyDifficulty_ShortCircuitsZeroLLMCalls(t *testing.T) {
	resetHooks(t)

	var calls atomic.Int32
	CompleteFn = func(_ context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		calls.Add(1)
		return makeAssistantReply("high"), nil
	}

	// Non-reasoning model -> "", nil, 0 calls.
	nonReasoning := map[string]any{"provider": "openai", "id": "gpt-4o-mini", "reasoning": false}
	got, err := ClassifyDifficulty(context.Background(), sdk.Context{}, Input{
		Request:     "anything",
		TargetModel: nonReasoning,
	})
	if err != nil || got != "" {
		t.Fatalf("non-reasoning: got (%q, %v), want (\"\", nil)", got, err)
	}

	// Single-tier reasoning model -> returns that tier directly with 0 calls.
	singleTier := reasoningModel("medium")
	got, err = ClassifyDifficulty(context.Background(), sdk.Context{}, Input{
		Request:     "anything",
		TargetModel: singleTier,
	})
	if err != nil || got != "medium" {
		t.Fatalf("single-tier: got (%q, %v), want (\"medium\", nil)", got, err)
	}

	// Model with only minimal (below low) -> clamps to minimal with 0 calls.
	onlyMinimal := reasoningModel("minimal")
	got, err = ClassifyDifficulty(context.Background(), sdk.Context{}, Input{
		Request:     "anything",
		TargetModel: onlyMinimal,
	})
	if err != nil || got != "minimal" {
		t.Fatalf("only-minimal: got (%q, %v), want (\"minimal\", nil)", got, err)
	}

	if calls.Load() != 0 {
		t.Fatalf("short-circuit cases must make 0 LLM calls, got %d", calls.Load())
	}
}

func TestClassifyDifficulty_CacheHitAndErrorHandling(t *testing.T) {
	resetHooks(t)

	var calls atomic.Int32
	CompleteFn = func(_ context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		calls.Add(1)
		return makeAssistantReply("`high`."), nil
	}

	model := reasoningModel("low", "medium", "high")
	got1, err := ClassifyDifficulty(context.Background(), sdk.Context{}, Input{
		SolutionSpace: "several   viable\ndesigns",
		TargetModel:   model,
	})
	if err != nil || got1 != "high" {
		t.Fatalf("first call: got (%q, %v), want (\"high\", nil)", got1, err)
	}
	got2, err := ClassifyDifficulty(context.Background(), sdk.Context{}, Input{
		SolutionSpace: "several viable designs",
		TargetModel:   model,
	})
	if err != nil || got2 != "high" {
		t.Fatalf("cached call: got (%q, %v), want (\"high\", nil)", got2, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected cache hit (1 LLM call total), got %d", calls.Load())
	}

	// Timeout / provider error returns an error cleanly so caller can fall back.
	CompleteFn = func(_ context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		return nil, context.DeadlineExceeded
	}
	_, err = ClassifyDifficulty(context.Background(), sdk.Context{}, Input{
		SolutionSpace: "uncached timeout case",
		TargetModel:   model,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded error, got %v", err)
	}

	// Unparseable output returns error cleanly.
	CompleteFn = func(_ context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		return makeAssistantReply("I cannot decide"), nil
	}
	_, err = ClassifyDifficulty(context.Background(), sdk.Context{}, Input{
		SolutionSpace: "uncached garbage reply",
		TargetModel:   model,
	})
	if err == nil {
		t.Fatalf("expected error on unparseable judge reply, got nil")
	}
}

func TestPreprocessTinyMessage(t *testing.T) {
	// M2: ANSI stripping, fenced code block stripping, hash shortening, and 2000-char middle truncation
	input := "\x1b[31mFix the bug\x1b[0m in commit deadbeef1234567890\n```go\nfunc ignored() {}\n```\n<think>secret</think>"
	got := preprocessTinyMessage(input)
	if strings.Contains(got, "\x1b[") {
		t.Errorf("expected ANSI stripped, got %q", got)
	}
	if strings.Contains(got, "func ignored") {
		t.Errorf("expected fenced code block stripped, got %q", got)
	}
	if strings.Contains(got, "secret") {
		t.Errorf("expected paired XML block stripped, got %q", got)
	}
	if !strings.Contains(got, "deadbee") || strings.Contains(got, "deadbeef1234567890") {
		t.Errorf("expected hash shortened to 7 chars, got %q", got)
	}

	huge := strings.Repeat("word ", 1000)
	trunc := preprocessTinyMessage(huge)
	if len([]rune(trunc)) > maxTinyMessageChars {
		t.Errorf("expected truncated length <= %d, got %d", maxTinyMessageChars, len([]rune(trunc)))
	}
	if !strings.Contains(trunc, "chars omitted") {
		t.Errorf("expected middle-truncation marker, got %q", trunc)
	}
}

func TestMagicKeywordPrecision(t *testing.T) {
	// M3: case-sensitive lowercase prose only; masked code/XML and path boundaries rejected
	nonTriggers := []string{
		"ULTRATHINK about this",
		"please `ultrathink` about this",
		"see path/ultrathink-notes.md",
		"```\nultrathink\n```",
		"<codeblock>ultrathink</codeblock>",
		"foo.ultrathink",
		"ultrathink()",
		"ultrathink.md",
		"ns::ultrathink",
	}
	for _, tc := range nonTriggers {
		if containsMagicKeyword(tc, "ultrathink") {
			t.Errorf("expected %q NOT to trigger ultrathink", tc)
		}
	}
	triggers := []string{
		"please ultrathink about this",
		"ultrathink.",
		"please ultrathink, thanks",
		"please chillax on this one",
	}
	for _, tc := range triggers {
		kw := "ultrathink"
		if strings.Contains(tc, "chillax") {
			kw = "chillax"
		}
		if !containsMagicKeyword(tc, kw) {
			t.Errorf("expected %q to trigger %s", tc, kw)
		}
	}
}

func TestClassifyDifficulty_CacheKeyIncludesEligibleLadder(t *testing.T) {
	// L3: models with different effort ladders must not collide in the cache
	resetHooks(t)
	var calls atomic.Int32
	CompleteFn = func(_ context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		n := calls.Add(1)
		if n == 1 {
			return makeAssistantReply("high"), nil
		}
		return makeAssistantReply("xhigh"), nil
	}
	modelA := reasoningModel("low", "medium", "high")
	modelB := reasoningModel("low", "medium", "high", "xhigh")
	gotA, err := ClassifyDifficulty(context.Background(), sdk.Context{}, Input{
		Request:     "design a distributed lock",
		TargetModel: modelA,
	})
	if err != nil || gotA != "high" {
		t.Fatalf("modelA: got (%q, %v), want (high, nil)", gotA, err)
	}
	gotB, err := ClassifyDifficulty(context.Background(), sdk.Context{}, Input{
		Request:     "design a distributed lock",
		TargetModel: modelB,
	})
	if err != nil || gotB != "xhigh" {
		t.Fatalf("modelB: got (%q, %v), want (xhigh, nil)", gotB, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 separate judge calls for different effort ladders, got %d", calls.Load())
	}
}
