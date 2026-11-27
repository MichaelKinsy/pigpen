package snapcompact

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

func TestSurfaceCompactionMeta_SnapcompactBeforeCompact(t *testing.T) {
	surface.ResetForTests()
	defer surface.ResetForTests()

	prevSetting := Setting
	defer func() { Setting = prevSetting }()
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "compaction.methodOrder":
			return []string{"remote", "snapcompact", "handoff", "shake", "soft"}
		case "snapcompact.shape":
			return "auto"
		}
		return nil
	}

	visionModel := map[string]any{
		"id":            "claude-sonnet-4-6",
		"provider":      "anthropic",
		"contextWindow": 200000,
		"input":         []string{"text", "image"},
	}
	ext := sdk.New("test-snap-meta")
	mgr := newManager()
	registerWithManager(ext, mgr)
	h, cleanup := startFakeHost(t, ext, visionModel)
	defer cleanup()

	surface.SetNative(true)

	res := h.emit(sdk.EventSessionBeforeCompact, map[string]any{
		"preparation": map[string]any{
			"firstKeptEntryId": "entry-keep-1",
			"tokensBefore":     66557,
			"messagesToSummarize": []any{
				map[string]any{"role": "user", "content": "Investigate snapcompact metadata " + strings.Repeat("alpha beta gamma ", 180)},
				map[string]any{"role": "assistant", "content": []any{
					map[string]any{"type": "text", "text": "Inspecting compaction rendering " + strings.Repeat("delta epsilon zeta ", 180)},
				}},
			},
			"settings": map[string]any{"reserveTokens": 16384, "keepRecentTokens": 20000},
		},
	})

	comp, ok := res["compaction"].(map[string]any)
	if !ok {
		t.Fatalf("expected snapcompact compaction result, got %#v", res)
	}
	details, _ := comp["details"].(map[string]any)
	if details == nil {
		t.Fatalf("expected details in compaction result")
	}
	tokensAfter, ok := numberField(details, "tokensAfter")
	if !ok || tokensAfter <= 0 {
		t.Fatalf("expected positive details.tokensAfter, got %#v", details["tokensAfter"])
	}
	frameCount, ok := numberField(details, "frameCount")
	if !ok || frameCount < 0 {
		t.Fatalf("expected details.frameCount, got %#v", details["frameCount"])
	}

	// Native surface should suppress the redundant text toast.
	if len(h.notifies) != 0 {
		t.Fatalf("expected no ui.notify on native surface, got %#v", h.notifies)
	}

	meta, found := surface.LookupCompaction(66557)
	if !found {
		t.Fatalf("expected surface.LookupCompaction(66557) to find snapcompact meta")
	}
	if meta.Method != "snapcompact" {
		t.Fatalf("meta.Method = %q, want snapcompact", meta.Method)
	}
	if meta.TokensBefore != 66557 || meta.TokensAfter != tokensAfter || meta.FrameCount != frameCount {
		t.Fatalf("unexpected meta counts: %+v (want before=66557 after=%d frames=%d)", meta, tokensAfter, frameCount)
	}
	if wantSummary, _ := comp["summary"].(string); meta.Summary != wantSummary || meta.Summary == "" {
		t.Fatalf("meta.Summary mismatch or empty")
	}
}

func TestSurfaceCompactionMeta_OnSessionCompactLLM(t *testing.T) {
	surface.ResetForTests()
	defer surface.ResetForTests()

	ext := sdk.New("test-snap-llm-meta")
	mgr := newManager()
	registerWithManager(ext, mgr)
	h, cleanup := startFakeHost(t, ext, map[string]any{
		"id":            "gpt-5",
		"provider":      "openai",
		"contextWindow": 128000,
		"input":         []string{"text"},
	})
	defer cleanup()

	h.emit(sdk.EventSessionCompact, map[string]any{
		"compactionEntry": map[string]any{
			"type":         "compaction",
			"tokensBefore": 48200,
			"summary":      "## Goal\nShip native Tern compaction dividers.",
			"details": map[string]any{
				"method":      "handoff",
				"tokensAfter": 3100,
				"warning":     "trimmed older tool output",
			},
		},
	})

	meta, found := surface.LookupCompaction(48200)
	if !found {
		t.Fatalf("expected surface.LookupCompaction(48200) to find LLM compaction meta")
	}
	if meta.Method != "handoff" || meta.TokensBefore != 48200 || meta.TokensAfter != 3100 || meta.Warning != "trimmed older tool output" {
		t.Fatalf("unexpected LLM meta: %+v", meta)
	}
	if !strings.Contains(meta.Summary, "Ship native Tern compaction dividers.") {
		t.Fatalf("unexpected LLM summary: %q", meta.Summary)
	}
}

func TestSurfaceCompactionMeta_HydrateOnStartAndTree(t *testing.T) {
	surface.ResetForTests()
	defer surface.ResetForTests()

	ext := sdk.New("test-snap-hydrate")
	mgr := newManager()
	registerWithManager(ext, mgr)
	h, cleanup := startFakeHost(t, ext, map[string]any{
		"id":            "claude-sonnet-4-6",
		"provider":      "anthropic",
		"contextWindow": 200000,
		"input":         []string{"text", "image"},
	})
	defer cleanup()

	mustRaw := func(v any) json.RawMessage {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return b
	}

	h.entries = []json.RawMessage{
		mustRaw(map[string]any{
			"id":   "u1",
			"type": "message",
			"message": map[string]any{
				"role":    "user",
				"content": "hello",
			},
		}),
		mustRaw(map[string]any{
			"id":           "c1",
			"parentId":     "u1",
			"type":         "compaction",
			"tokensBefore": 51200,
			"summary":      "Earlier snapcompact summary",
			"details": map[string]any{
				"tokensAfter": 4200,
				PreserveKey: map[string]any{
					"frames": []any{
						map[string]any{"data": "AAAA", "mimeType": "image/png", "cols": 80, "rows": 40, "chars": 1000},
						map[string]any{"data": "BBBB", "mimeType": "image/png", "cols": 80, "rows": 40, "chars": 1000},
					},
					"totalChars": 2000,
				},
			},
		}),
		mustRaw(map[string]any{
			"id":           "c2",
			"parentId":     "c1",
			"type":         "compaction",
			"tokensBefore": 73400,
			"summary":      "Later soft summary",
			"details": map[string]any{
				"method":      "soft",
				"tokensAfter": 5100,
			},
		}),
	}

	h.emit(sdk.EventSessionStart, map[string]any{})

	m1, ok1 := surface.LookupCompaction(51200)
	if !ok1 {
		t.Fatalf("expected hydrated compaction c1")
	}
	if m1.Method != "snapcompact" || m1.FrameCount != 2 || m1.TokensAfter != 4200 || m1.Summary != "Earlier snapcompact summary" {
		t.Fatalf("unexpected hydrated c1: %+v", m1)
	}
	m2, ok2 := surface.LookupCompaction(73400)
	if !ok2 {
		t.Fatalf("expected hydrated compaction c2")
	}
	if m2.Method != "soft" || m2.TokensAfter != 5100 || m2.Summary != "Later soft summary" {
		t.Fatalf("unexpected hydrated c2: %+v", m2)
	}

	// Switching branches via EventSessionTree: start a fresh host reflecting the
	// new branch's entries (matching PiG's session mirror reset/branch update).
	ext2 := sdk.New("test-snap-hydrate-tree")
	mgr2 := newManager()
	registerWithManager(ext2, mgr2)
	h2, cleanup2 := startFakeHost(t, ext2, map[string]any{
		"id":            "claude-sonnet-4-6",
		"provider":      "anthropic",
		"contextWindow": 200000,
		"input":         []string{"text", "image"},
	})
	defer cleanup2()

	h2.entries = []json.RawMessage{
		mustRaw(map[string]any{
			"id":           "c3",
			"type":         "compaction",
			"tokensBefore": 91000,
			"summary":      "Branch summary",
			"details": map[string]any{
				"method":      "remote",
				"tokensAfter": 6000,
			},
		}),
	}
	h2.emit(sdk.EventSessionTree, map[string]any{})

	if _, stillOld := surface.LookupCompaction(51200); stillOld {
		t.Fatalf("expected old compaction c1 to be cleared after EventSessionTree")
	}
	m3, ok3 := surface.LookupCompaction(91000)
	if !ok3 || m3.Method != "remote" || m3.TokensAfter != 6000 || m3.Summary != "Branch summary" {
		t.Fatalf("unexpected hydrated c3 after tree switch: ok=%v meta=%+v", ok3, m3)
	}
}
