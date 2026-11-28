package snapcompact

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"strings"
	"sync"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func decodeBase64PNG(t *testing.T, data string) (int, int) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("png decode failed: %v", err)
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

func makeLargeText(prefix string, approxChars int) string {
	line := prefix + ": The quick brown fox jumps over the lazy dog with identifiers alpha_01, beta_02, gamma_03.\n"
	n := approxChars/len(line) + 1
	return strings.Repeat(line, n)
}

func TestInlineDefaultSettingsExactNoOp(t *testing.T) {
	prevSetting := Setting
	defer func() { Setting = prevSetting }()
	Setting = func(_ sdk.Context, _ string) any { return nil }

	ext := sdk.New("snap-inline-default")
	mgr := newManager()
	registerWithManager(ext, mgr)

	host, cleanup := startFakeHost(t, ext, map[string]any{
		"id":            "gemini-4-neon",
		"provider":      "github-copilot",
		"api":           "openai-completions",
		"contextWindow": 200000,
		"input":         []any{"text", "image"},
	})
	defer cleanup()

	largeSys := makeLargeText("SYS", 20000)
	largeTool := makeLargeText("TOOL", 24000)
	msgs := []any{
		map[string]any{"role": "system", "content": largeSys},
		map[string]any{"role": "user", "content": "hello"},
		map[string]any{
			"role":       "toolResult",
			"toolCallId": "tc-1",
			"toolName":   "bash",
			"content":    []any{map[string]any{"type": "text", "text": largeTool}},
		},
	}

	res := host.emit(sdk.EventContextWithSystem, map[string]any{"messages": msgs})
	if res != nil && (res["messages"] != nil || res["_pigContextUnchanged"] != true) {
		t.Fatalf("expected unchanged/nil result on default settings, got %#v", res)
	}
	// Also verify direct handler call returns exact (nil, nil).
	if direct, err := mgr.onContextWithSystem(sdk.Context{}, map[string]any{"messages": msgs}); direct != nil || err != nil {
		t.Fatalf("direct onContextWithSystem default = (%#v, %v), want (nil, nil)", direct, err)
	}
	if msgs[0].(map[string]any)["content"] != largeSys {
		t.Fatalf("system message was mutated on default no-op")
	}
	if msgs[1].(map[string]any)["content"] != "hello" {
		t.Fatalf("user message was mutated on default no-op")
	}
}

func TestInlineNonVisionModelNoOp(t *testing.T) {
	prevSetting := Setting
	defer func() { Setting = prevSetting }()
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "snapcompact.systemPrompt":
			return "all"
		case "snapcompact.toolResults":
			return true
		}
		return nil
	}

	ext := sdk.New("snap-inline-nonvision")
	mgr := newManager()
	registerWithManager(ext, mgr)

	host, cleanup := startFakeHost(t, ext, map[string]any{
		"id":            "deepseek-reasoner",
		"provider":      "deepseek",
		"api":           "openai-completions",
		"contextWindow": 128000,
		"input":         []any{"text"},
	})
	defer cleanup()

	res := host.emit(sdk.EventContextWithSystem, map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": makeLargeText("SYS", 20000)},
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{
				"role":       "toolResult",
				"toolCallId": "tc-1",
				"toolName":   "bash",
				"content":    []any{map[string]any{"type": "text", "text": makeLargeText("OUT", 24000)}},
			},
		},
	})
	if res != nil && (res["messages"] != nil || res["_pigContextUnchanged"] != true) {
		t.Fatalf("expected unchanged/nil on non-vision model, got %#v", res)
	}
}

func TestInlineSystemPromptAllCachesAndDoesNotMutateInput(t *testing.T) {
	prevSetting := Setting
	defer func() { Setting = prevSetting }()
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "snapcompact.systemPrompt":
			return "all"
		case "snapcompact.toolResults":
			return false
		}
		return nil
	}

	ext := sdk.New("snap-inline-sys-all")
	mgr := newManager()
	registerWithManager(ext, mgr)

	host, cleanup := startFakeHost(t, ext, map[string]any{
		"id":            "gemini-4-neon",
		"provider":      "github-copilot",
		"api":           "openai-completions",
		"contextWindow": 200000,
		"input":         []any{"text", "image"},
	})
	defer cleanup()

	largeSys := makeLargeText("OPERATING RULE", 20000)
	origSys := map[string]any{"role": "system", "content": largeSys}
	origUserBlocks := []any{map[string]any{"type": "text", "text": "please inspect main.go"}}
	origUser := map[string]any{"role": "user", "content": origUserBlocks}
	inputMsgs := []any{origSys, origUser}

	res1 := host.emit(sdk.EventContextWithSystem, map[string]any{"messages": inputMsgs})
	if res1 == nil {
		t.Fatalf("expected transformed messages for systemPrompt=all")
	}
	outMsgs, ok := res1["messages"].([]any)
	if !ok || len(outMsgs) != 2 {
		t.Fatalf("unexpected outMessages: %#v", res1)
	}

	// Verify messages[0] still has role "system" with stub text.
	sys0 := outMsgs[0].(map[string]any)
	if sys0["role"] != "system" {
		t.Fatalf("messages[0].role = %v, want system", sys0["role"])
	}
	if sys0["content"] != inlineSystemStub {
		t.Fatalf("messages[0].content = %q, want %q", sys0["content"], inlineSystemStub)
	}

	// Verify first user message has [note, frame..., originalUserText].
	user1 := outMsgs[1].(map[string]any)
	uBlocks, ok := user1["content"].([]any)
	if !ok || len(uBlocks) < 3 {
		t.Fatalf("expected note + >=1 PNG frame + original user text, got %d blocks", len(uBlocks))
	}
	if uBlocks[0].(map[string]any)["text"] != inlineSystemFramesNote {
		t.Fatalf("unexpected frame note: %#v", uBlocks[0])
	}
	imgBlock := uBlocks[1].(map[string]any)
	if imgBlock["type"] != "image" || imgBlock["mimeType"] != "image/png" {
		t.Fatalf("expected PNG image block at index 1, got %#v", imgBlock)
	}
	w, h := decodeBase64PNG(t, imgBlock["data"].(string))
	if w != 2048 || h < 64 {
		t.Fatalf("unexpected PNG dims: %dx%d", w, h)
	}
	lastBlock := uBlocks[len(uBlocks)-1].(map[string]any)
	if lastBlock["text"] != "please inspect main.go" {
		t.Fatalf("original user text not preserved at end: %#v", lastBlock)
	}

	// Verify caller's input maps/slices were NOT mutated.
	if origSys["content"] != largeSys {
		t.Fatalf("origSys was mutated")
	}
	if len(origUserBlocks) != 1 || origUser["content"].([]any)[0].(map[string]any)["text"] != "please inspect main.go" {
		t.Fatalf("origUser was mutated")
	}

	// Verify per-session systemCache was populated and reused on second call.
	mgr.mu.Lock()
	st := mgr.sessions["sess-snap-1"]
	if st == nil || st.inline.systemCache == nil {
		mgr.mu.Unlock()
		t.Fatalf("expected systemCache entry to be populated")
	}
	firstCachePtr := st.inline.systemCache
	mgr.mu.Unlock()

	res2 := host.emit(sdk.EventContextWithSystem, map[string]any{"messages": inputMsgs})
	if res2 == nil {
		t.Fatalf("expected second call to also transform")
	}
	mgr.mu.Lock()
	secondCachePtr := mgr.sessions["sess-snap-1"].inline.systemCache
	mgr.mu.Unlock()
	if firstCachePtr != secondCachePtr {
		t.Fatalf("expected second call to reuse cached systemCache entry")
	}
}

func TestInlineSystemPromptAgentsMDReplacesOnlyContextSections(t *testing.T) {
	prevSetting := Setting
	defer func() { Setting = prevSetting }()
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "snapcompact.systemPrompt":
			return "agents-md"
		}
		return nil
	}

	ext := sdk.New("snap-inline-agentsmd")
	mgr := newManager()
	registerWithManager(ext, mgr)

	host, cleanup := startFakeHost(t, ext, map[string]any{
		"id":            "gemini-4-neon",
		"provider":      "github-copilot",
		"api":           "openai-completions",
		"contextWindow": 200000,
		"input":         []any{"text", "image"},
	})
	defer cleanup()

	preamble := "You are a helpful coding assistant.\n\n"
	repoRules := "<repo-rules>\n" + makeLargeText("REPO RULE", 16000) + "</repo-rules>"
	trailer := "\n\nFinal invariant: keep edits minimal."
	sysContent := preamble + repoRules + trailer

	res := host.emit(sdk.EventContextWithSystem, map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": sysContent},
			map[string]any{"role": "user", "content": "fix bug"},
		},
	})
	if res == nil {
		t.Fatalf("expected agents-md transform")
	}
	outMsgs := res["messages"].([]any)
	gotSys := outMsgs[0].(map[string]any)["content"].(string)
	if !strings.HasPrefix(gotSys, preamble) {
		t.Fatalf("preamble lost in system prompt: %q", gotSys[:min(80, len(gotSys))])
	}
	if !strings.HasSuffix(gotSys, trailer) {
		t.Fatalf("trailer lost in system prompt")
	}
	if !strings.Contains(gotSys, inlineContextStub) {
		t.Fatalf("context stub missing in system prompt")
	}
	if strings.Contains(gotSys, "<repo-rules>") {
		t.Fatalf("<repo-rules> should have been replaced")
	}

	userBlocks := outMsgs[1].(map[string]any)["content"].([]any)
	if userBlocks[0].(map[string]any)["text"] != inlineContextFramesNote {
		t.Fatalf("expected context frames note, got %#v", userBlocks[0])
	}
	if userBlocks[1].(map[string]any)["type"] != "image" {
		t.Fatalf("expected PNG frame at index 1")
	}
	if userBlocks[len(userBlocks)-1].(map[string]any)["text"] != "fix bug" {
		t.Fatalf("expected original user text preserved")
	}
}

func TestInlineToolResultsSwapPreserveSourceImagesSkipSmallAndErrorsAndEvictCache(t *testing.T) {
	prevSetting := Setting
	defer func() { Setting = prevSetting }()
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "snapcompact.systemPrompt":
			return "none"
		case "snapcompact.toolResults":
			return true
		}
		return nil
	}

	ext := sdk.New("snap-inline-tools")
	mgr := newManager()
	registerWithManager(ext, mgr)

	host, cleanup := startFakeHost(t, ext, map[string]any{
		"id":            "gemini-4-neon",
		"provider":      "github-copilot",
		"api":           "openai-completions",
		"contextWindow": 200000,
		"input":         []any{"text", "image"},
	})
	defer cleanup()

	largeOut := makeLargeText("GREP MATCH", 20000)
	smallOut := "ok: 3 lines"
	srcImg := map[string]any{"type": "image", "data": "iVBORw0KGgo=", "mimeType": "image/png"}

	msgs := []any{
		map[string]any{"role": "system", "content": "sys"},
		map[string]any{"role": "user", "content": "search"},
		// Eligible large tool result with a co-resident source image.
		map[string]any{
			"role":       "toolResult",
			"toolCallId": "tc-large",
			"toolName":   "grep",
			"content": []any{
				map[string]any{"type": "text", "text": largeOut},
				srcImg,
			},
		},
		// Small tool result (<3000 tokens) -> must stay text.
		map[string]any{
			"role":       "toolResult",
			"toolCallId": "tc-small",
			"toolName":   "bash",
			"content":    []any{map[string]any{"type": "text", "text": smallOut}},
		},
		// Large error tool result -> must stay text.
		map[string]any{
			"role":       "toolResult",
			"toolCallId": "tc-err",
			"toolName":   "bash",
			"isError":    true,
			"content":    []any{map[string]any{"type": "text", "text": largeOut}},
		},
		// Large `read` result on Gemini (does not opt in to ReaderImagesReadResults) -> must stay text.
		map[string]any{
			"role":       "toolResult",
			"toolCallId": "tc-read",
			"toolName":   "read",
			"content":    []any{map[string]any{"type": "text", "text": largeOut}},
		},
	}

	res := host.emit(sdk.EventContextWithSystem, map[string]any{"messages": msgs})
	if res == nil {
		t.Fatalf("expected transformed messages")
	}
	outMsgs := res["messages"].([]any)

	// tc-large should be swapped to [toolResultNote, PNG..., "[Original source image 1...]", srcImg].
	largeBlocks := outMsgs[2].(map[string]any)["content"].([]any)
	if largeBlocks[0].(map[string]any)["text"] != inlineToolResultNote {
		t.Fatalf("unexpected toolResult note: %#v", largeBlocks[0])
	}
	if largeBlocks[1].(map[string]any)["type"] != "image" {
		t.Fatalf("expected PNG frame at index 1")
	}
	decodeBase64PNG(t, largeBlocks[1].(map[string]any)["data"].(string))

	// Last two blocks must be the source-image label + original source image.
	n := len(largeBlocks)
	if !strings.Contains(largeBlocks[n-2].(map[string]any)["text"].(string), "Original source image 1") {
		t.Fatalf("expected source image label at %d, got %#v", n-2, largeBlocks[n-2])
	}
	if largeBlocks[n-1].(map[string]any)["data"] != "iVBORw0KGgo=" {
		t.Fatalf("expected preserved source image at %d, got %#v", n-1, largeBlocks[n-1])
	}

	// tc-small, tc-err, tc-read must remain text.
	for _, idx := range []int{3, 4, 5} {
		blks := outMsgs[idx].(map[string]any)["content"].([]any)
		if len(blks) != 1 || blks[0].(map[string]any)["type"] != "text" {
			t.Fatalf("message %d should have stayed text, got %#v", idx, blks)
		}
	}

	// Verify tc-large is cached; then emit a compacted context without tc-large and verify eviction.
	mgr.mu.Lock()
	if _, ok := mgr.sessions["sess-snap-1"].inline.toolCache["tc-large"]; !ok {
		mgr.mu.Unlock()
		t.Fatalf("expected tc-large in toolCache")
	}
	mgr.mu.Unlock()

	host.emit(sdk.EventContextWithSystem, map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "sys"},
			map[string]any{"role": "user", "content": "next turn"},
		},
	})
	mgr.mu.Lock()
	if _, ok := mgr.sessions["sess-snap-1"].inline.toolCache["tc-large"]; ok {
		mgr.mu.Unlock()
		t.Fatalf("expected tc-large to be evicted from toolCache when absent from context")
	}
	mgr.mu.Unlock()
}

func TestPlanInlineSwapsCapBackstopAndSavingsGate(t *testing.T) {
	shape := ResolveShape(ModelTarget{ID: "gemini-4-neon", API: "openai-completions"}, "auto")
	// Gemini frameTokenEstimate = 1120 (verified).
	// Savings gate: frames * 1120 <= textTokens * 0.9.
	// 2 frames = 2240; textTokens = 2400 -> 2400 * 0.9 = 2160 < 2240 -> rejected.
	planReject := PlanInlineSwaps(InlinePlanInput{
		Options: InlineOptions{
			RenderSystemPrompt: InlineSystemPromptAll,
			RenderToolResults:  true,
		},
		Shape:          shape,
		ImageLimit:     10,
		ExistingImages: 0,
		SystemPrompt:   &InlineSystemPromptCandidate{TextTokens: 2400, Frames: 2, ImagesThrough: 0},
		ToolResults: []InlineToolResultCandidate{
			{ID: "t1", TextTokens: 3100, Frames: 3, ToolName: "bash", ImagesThrough: 0}, // 3360 > 2790 -> rejected
		},
		HasUserMessage: true,
	})
	if planReject.SystemPrompt != nil {
		t.Fatalf("expected system prompt rejected by savings gate, got %+v", planReject.SystemPrompt)
	}
	if len(planReject.ToolResults) != 0 {
		t.Fatalf("expected tool result rejected by savings gate, got %+v", planReject.ToolResults)
	}

	// Cap backstop: imageLimit = 5.
	// System prompt takes 2 frames, t1 takes 2 frames, t2 takes 1 frame (total 5 frames).
	// Suppose 2 existing images arrived at the end of the context (ExistingImages = 2).
	// Total would be 2 + 5 = 7 > 5 -> must retract newest swaps first: t2 (1) then t1 (2) -> remaining 2+2=4 <= 5.
	planCap := PlanInlineSwaps(InlinePlanInput{
		Options: InlineOptions{
			RenderSystemPrompt: InlineSystemPromptAll,
			RenderToolResults:  true,
		},
		Shape:          shape,
		ImageLimit:     5,
		ExistingImages: 2,
		SystemPrompt:   &InlineSystemPromptCandidate{TextTokens: 10000, Frames: 2, ImagesThrough: 0},
		ToolResults: []InlineToolResultCandidate{
			{ID: "t1", TextTokens: 10000, Frames: 2, ToolName: "bash", ImagesThrough: 0},
			{ID: "t2", TextTokens: 10000, Frames: 1, ToolName: "bash", ImagesThrough: 0},
		},
		HasUserMessage: true,
	})
	if planCap.SystemPrompt == nil {
		t.Fatalf("expected system prompt kept after tool retractions")
	}
	if len(planCap.ToolResults) != 0 {
		t.Fatalf("expected both t2 and t1 retracted, got kept=%+v", planCap.ToolResults)
	}
	if len(planCap.RetractedToolResults) != 2 || planCap.RetractedToolResults[0].ID != "t2" || planCap.RetractedToolResults[1].ID != "t1" {
		t.Fatalf("expected retraction order [t2, t1], got %+v", planCap.RetractedToolResults)
	}

	// If ExistingImages = 4 and imageLimit = 5, even system prompt (2 frames) must be retracted.
	planCapSys := PlanInlineSwaps(InlinePlanInput{
		Options:        InlineOptions{RenderSystemPrompt: InlineSystemPromptAll},
		Shape:          shape,
		ImageLimit:     5,
		ExistingImages: 4,
		SystemPrompt:   &InlineSystemPromptCandidate{TextTokens: 10000, Frames: 2, ImagesThrough: 0},
		HasUserMessage: true,
	})
	if planCapSys.SystemPrompt != nil || planCapSys.RetractedSystemPrompt == nil {
		t.Fatalf("expected system prompt retracted when existingImages + frames > imageLimit")
	}
}

func TestEncodeReadForFramesIndentBracketsAndHeaderLift(t *testing.T) {
	src := strings.Join([]string{
		"[src/app.go#A1B2]",
		"1:package main",
		"2:\tfunc run() {",
		"3:\t\t  return",
		"4:[·4]literal bracket",
		"5:200|[»1]looks like prefix+bracket",
		"6:plain",
	}, "\n")

	res := EncodeReadForFrames(src, nil)
	if !strings.Contains(res.Note, "[src/app.go#A1B2]") {
		t.Fatalf("expected lifted header in note, got %q", res.Note)
	}
	if !strings.Contains(res.Note, inlineReadIndentLegend) {
		t.Fatalf("expected indentation legend in note, got %q", res.Note)
	}
	if strings.Contains(res.Body, "[src/app.go#A1B2]") {
		t.Fatalf("header should be stripped from body, got %q", res.Body)
	}
	wantLines := []string{
		"1:package main",
		"2:[»1]func run() {",
		"3:[»2·2]return",
		"4:[0][·4]literal bracket",
		"5:[0]200|[»1]looks like prefix+bracket",
		"6:plain",
	}
	gotLines := strings.Split(res.Body, "\n")
	if len(gotLines) != len(wantLines) {
		t.Fatalf("line count mismatch: got %d, want %d\n%s", len(gotLines), len(wantLines), res.Body)
	}
	for i := range wantLines {
		if gotLines[i] != wantLines[i] {
			t.Errorf("line %d: got %q, want %q", i+1, gotLines[i], wantLines[i])
		}
	}

	// Raw read (rowPrefixes=false): does NOT lift header-shaped line and treats leading digits as content.
	rawFalse := false
	rawSrc := "[not/a/header#ABCD]\n  indented\n12:  raw line"
	rawRes := EncodeReadForFrames(rawSrc, &rawFalse)
	if strings.Contains(rawRes.Note, "[not/a/header#ABCD]") {
		t.Fatalf("rowPrefixes=false should not lift header")
	}
	if !strings.HasPrefix(rawRes.Body, "[not/a/header#ABCD]\n[·2]indented\n12:  raw line") {
		t.Fatalf("unexpected raw body: %q", rawRes.Body)
	}
}

func TestReaderImagesReadResults(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"claude-opus-5-5", true},
		{"claude-opus-5.5", true},
		{"claude-opus-6", true},
		{"claude-opus-4-7", false},
		{"claude-sonnet-5-5", false},
		{"gemini-4-neon", false},
		{"gpt-5.4", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := ReaderImagesReadResults(ModelTarget{ID: tc.id}); got != tc.want {
			t.Errorf("ReaderImagesReadResults(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

func TestInlineConcurrencyAcrossSessions(t *testing.T) {
	mgr := newManager()
	shape := ResolveShape(ModelTarget{ID: "gemini-4-neon", API: "openai-completions"}, "auto")
	skey := shapeCacheKey(shape)
	text := makeLargeText("CONCURRENT", 6000)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sid := "sess-conc-" + string(rune('a'+id%3))
			frames, err := mgr.getOrRenderToolFrames(sid, "tc-1", text, shape, skey)
			if err != nil || len(frames) == 0 {
				t.Errorf("getOrRenderToolFrames failed: %v, len=%d", err, len(frames))
			}
			sframes, err := mgr.getOrRenderSystemFrames(sid, text, shape, skey)
			if err != nil || len(sframes) == 0 {
				t.Errorf("getOrRenderSystemFrames failed: %v, len=%d", err, len(sframes))
			}
			mgr.evictStaleToolCache(sid, map[string]struct{}{"tc-1": {}})
			if id%2 == 0 {
				mgr.clearInlineCaches(sid, true, false)
			}
		}(i)
	}
	wg.Wait()
}
