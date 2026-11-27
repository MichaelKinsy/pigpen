package snapcompact

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image/png"
	"io"
	"net"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestFontsParseAndDisambiguateZeroFromO(t *testing.T) {
	// upstream: crates/natives/src/snapcompact.rs fonts_parse_ascii_coverage + digit_zero_is_disambiguated_from_letter_o
	for _, tc := range []struct {
		name   string
		ascent int
		cw, ch int
	}{
		{"5x8", 7, 5, 8},
		{"8x8", 7, 8, 8},
		{"6x12", 10, 6, 12},
		{"8x13", 11, 8, 13},
	} {
		f := ResolveFont(tc.name)
		if f == nil {
			t.Fatalf("ResolveFont(%q) = nil", tc.name)
		}
		if f.Ascent != tc.ascent || f.CellW != tc.cw || f.CellH != tc.ch {
			t.Errorf("%s: got ascent=%d cell=%dx%d, want %d %dx%d", tc.name, f.Ascent, f.CellW, f.CellH, tc.ascent, tc.cw, tc.ch)
		}
		for cp := rune(0x20); cp < 0x7f; cp++ {
			if !f.Supports(cp) {
				t.Errorf("%s: missing printable ASCII glyph U+%04X", tc.name, cp)
			}
		}

		// Disambiguate '0' vs 'O': '0' has an interior slash/bar so it inks strictly
		// more pixels in the vertical middle band than 'O'.
		width := f.CellW * 2
		grid := gridSpec{cols: 2, rows: 1, repeat: 1, cellW: f.CellW, cellH: f.CellH}
		px := renderBitmap("0O", width, f.CellH, f, grid, false, true)
		midInk := func(col0 int) int {
			n := 0
			for y := f.CellH / 4; y < f.CellH-f.CellH/4; y++ {
				for x := col0; x < col0+f.CellW; x++ {
					if px[y*width+x] != 0 {
						n++
					}
				}
			}
			return n
		}
		if z, o := midInk(0), midInk(f.CellW); z <= o {
			t.Errorf("%s: digit '0' mid-ink (%d) should exceed 'O' (%d)", tc.name, z, o)
		}
	}
}

func TestRenderPNGIndexedAndStretchedAndDoc(t *testing.T) {
	// 1. Indexed bw shape (11on16-bw, Claude default).
	sh := ResolveShape(ModelTarget{ID: "claude-opus-4-7", API: "anthropic-messages"}, "auto")
	if sh.Font != "8x13" || sh.CellWidth != 11 || sh.CellHeight != 16 || sh.FrameSize != 1932 {
		t.Fatalf("unexpected Claude Opus 4.7 shape: %+v", sh)
	}
	raw, w, h, err := RenderPNG("¶user:hello world"+NewlineGlyph+"¶ai:done."+DimOn+"tool output"+DimOff, sh, sh.FrameSize)
	if err != nil {
		t.Fatalf("RenderPNG 11on16-bw: %v", err)
	}
	if w != 1932 || h < 64 {
		t.Fatalf("unexpected dims: %dx%d", w, h)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("png.Decode indexed: %v", err)
	}
	if img.Bounds().Dx() != 1932 || img.Bounds().Dy() != h {
		t.Fatalf("decoded bounds = %v, want %dx%d", img.Bounds(), 1932, h)
	}

	// 2. Stretched RGB shape (6x6u-sent).
	shStretch := ResolveShape(ModelTarget{}, "6x6u-sent")
	rawS, wS, hS, err := RenderPNG("First sentence. Second sentence! Third?", shStretch, 300)
	if err != nil {
		t.Fatalf("RenderPNG 6x6u-sent: %v", err)
	}
	imgS, err := png.Decode(bytes.NewReader(rawS))
	if err != nil {
		t.Fatalf("png.Decode stretched: %v", err)
	}
	if imgS.Bounds().Dx() != wS || imgS.Bounds().Dy() != hS {
		t.Fatalf("decoded stretch bounds = %v, want %dx%d", imgS.Bounds(), wS, hS)
	}

	// 3. Two-column doc shape (doc-8on16-sent-dim).
	shDoc := ResolveShape(ModelTarget{}, "doc-8on16-sent-dim")
	geo := ComputeGeometry(shDoc, 400)
	pages := docPages(Normalize("The quick brown fox jumps over the lazy dog. "+strings.Repeat("word ", 60), shDoc.Font), geo, true)
	if len(pages) == 0 {
		t.Fatalf("expected doc pages")
	}
	rawD, _, _, err := RenderPNG(DimStopwords(pages[0]), shDoc, 400)
	if err != nil {
		t.Fatalf("RenderPNG doc: %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(rawD)); err != nil {
		t.Fatalf("png.Decode doc: %v", err)
	}
}

func TestResolveShapeAndBudgets(t *testing.T) {
	// Gemini -> 8on22-bw @ 2048, fixed 1120 tokens, includes thinking.
	gem := ModelTarget{ID: "gemini-4-neon", Provider: "github-copilot", API: "openai-completions", Input: []string{"text", "image"}}
	shGem := ResolveShape(gem, "auto")
	if shGem.CellWidth != 8 || shGem.CellHeight != 22 || shGem.FrameSize != 2048 || shGem.FrameTokenEstimate != 1120 {
		t.Fatalf("Gemini shape = %+v", shGem)
	}
	if !ReaderIncludesThinking(gem) {
		t.Errorf("Gemini should include thinking")
	}

	// Claude Sonnet 4 -> 11on16-bw @ 1568, excludes thinking (reasoning_extraction classifier).
	son := ModelTarget{ID: "claude-sonnet-4-5", Provider: "anthropic", API: "anthropic-messages", ContextWindow: 200_000, Input: []string{"text", "image"}}
	shSon := ResolveShape(son, "auto")
	if shSon.CellWidth != 11 || shSon.CellHeight != 16 || shSon.FrameSize != 1568 {
		t.Fatalf("Claude Sonnet shape = %+v", shSon)
	}
	if ReaderIncludesThinking(son) {
		t.Errorf("Claude should exclude thinking")
	}
	if got := ProviderImageBudget(son); got != 100 {
		t.Errorf("Anthropic 200k image budget = %d, want 100", got)
	}

	// Cursor host downscales Claude/GPT to 1024px edge.
	cur := ModelTarget{ID: "claude-opus-4-7", Provider: "cursor", API: "anthropic-messages"}
	if shCur := ResolveShape(cur, "auto"); shCur.FrameSize != 1024 {
		t.Errorf("Cursor Claude frameSize = %d, want 1024", shCur.FrameSize)
	}
}

func TestNormalizeElideDataUrlsAndStopwords(t *testing.T) {
	in := "\x1b[31mHello\x1b[0m — “world” ✅\n\n├── a.go\n└── b.go"
	got := Normalize(in, "8x13")
	if !strings.Contains(got, `Hello - "world" [OK]`) {
		t.Errorf("Normalize folded punctuation/emoji unexpectedly: %q", got)
	}
	if !strings.Contains(got, NewlineGlyph) {
		t.Errorf("Normalize missing newline glyph: %q", got)
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("Normalize leaked ANSI escape: %q", got)
	}

	// Elide inline base64 data URL including markdown wrapper.
	b64 := strings.Repeat("QUJD", 20)
	md := "Look: ![shot](data:image/png;base64," + b64 + ") done."
	elided := ElideDataUrls(md, false)
	want := "Look: [data URL omitted: image/png, 80 base64 chars] done."
	if elided != want {
		t.Errorf("ElideDataUrls = %q, want %q", elided, want)
	}

	// Short prose mention in source mode stays untouched; archive mode elides it.
	prose := "example data:image/png;base64,abc end"
	if ElideDataUrls(prose, false) != prose {
		t.Errorf("source-mode prose mention was elided")
	}
	if !strings.Contains(ElideDataUrls(prose, true), "[data URL omitted: image/png, 3 base64 chars]") {
		t.Errorf("archive-mode fragment was not elided")
	}

	// Stopword dimming skips already-dim spans.
	dimmed := DimStopwords("the fox " + DimOn + "and the dog" + DimOff + " in fog")
	if !strings.Contains(dimmed, DimOn+"the"+DimOff+" fox "+DimOn+"and the dog"+DimOff+" "+DimOn+"in"+DimOff+" fog") {
		t.Errorf("DimStopwords = %q", dimmed)
	}
}

func TestSerializeConversationAndStripThinking(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "Check main.go"},
		{
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "thinking", "thinking": "Let me inspect main.go"},
				map[string]any{"type": "text", "text": "Reading file."},
				map[string]any{
					"type":      "toolCall",
					"id":        "call_1",
					"name":      "read",
					"arguments": map[string]any{"path": "main.go", "i": "Reading main.go"},
				},
			},
		},
		{
			"role":       "toolResult",
			"toolCallId": "call_1",
			"content":    []any{map[string]any{"type": "text", "text": "package main\nfunc main() {}"}},
		},
	}

	withThink := SerializeConversation(msgs, SerializeOptions{IncludeThinking: new(true)})
	if !strings.Contains(withThink, "¶user:Check main.go") ||
		!strings.Contains(withThink, "¶think:Let me inspect main.go") ||
		!strings.Contains(withThink, "¶ai:Reading file.") ||
		!strings.Contains(withThink, `¶call:read(path="main.go")//Reading main.go`) ||
		!strings.Contains(withThink, "<out>\n"+DimOn+"package main\nfunc main() {}"+DimOff+"\n</out>") {
		t.Fatalf("SerializeConversation unexpected output:\n%s", withThink)
	}

	scrubbed := StripThinkingSections(withThink)
	if strings.Contains(scrubbed, "¶think:") || strings.Contains(scrubbed, "Let me inspect") {
		t.Errorf("StripThinkingSections left reasoning behind:\n%s", scrubbed)
	}
	if !strings.Contains(scrubbed, "¶ai:Reading file.") || !strings.Contains(scrubbed, "¶user:Check main.go") {
		t.Errorf("StripThinkingSections dropped non-thinking scopes:\n%s", scrubbed)
	}
}

func TestCompactGeneratesFramesAndRecompacts(t *testing.T) {
	// Build a long transcript (> 2 edge pages at 256px frame size so middle frames are produced).
	var msgs []map[string]any
	for i := 0; i < 40; i++ {
		msgs = append(msgs,
			map[string]any{"role": "user", "content": "Investigate issue " + strings.Repeat("alpha beta gamma delta ", 12)},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "thinking", "thinking": "Step reasoning " + strings.Repeat("thought ", 10)},
				map[string]any{"type": "text", "text": "Found root cause " + strings.Repeat("details ", 12)},
			}},
		)
	}
	sh := ResolveShape(ModelTarget{ID: "gemini-4-neon", API: "openai-completions"}, "8on22-bw")
	sh.FrameSize = 256

	res, err := Compact(Preparation{
		FirstKeptEntryID:    "e-keep",
		MessagesToSummarize: msgs,
		TokensBefore:        120_000,
		FileOps: FileOps{
			Read:   []string{"src/a.go", "src/b.go"},
			Edited: []string{"src/b.go"},
		},
	}, CompactOptions{
		Model:     ModelTarget{ID: "gemini-4-neon", Provider: "github-copilot", API: "openai-completions", Input: []string{"text", "image"}},
		Shape:     &sh,
		MaxFrames: 4,
	})
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if len(res.Archive.Frames) == 0 {
		t.Fatalf("expected rendered PNG frames, got 0 (totalChars=%d)", res.Archive.TotalChars)
	}
	if len(res.Archive.Frames) > 4 {
		t.Fatalf("frames = %d, exceeded maxFrames 4", len(res.Archive.Frames))
	}
	for i, f := range res.Archive.Frames {
		raw, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			t.Fatalf("frame %d base64 decode: %v", i, err)
		}
		if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
			t.Fatalf("frame %d png decode: %v", i, err)
		}
	}
	if !strings.Contains(res.Summary, "src/a.go (Read)") || !strings.Contains(res.Summary, "src/b.go (RW)") {
		t.Errorf("summary missing FILES entries:\n%s", res.Summary)
	}
	if !strings.Contains(res.Summary, "HISTORY\n===================") {
		t.Errorf("summary missing HISTORY header:\n%s", res.Summary)
	}

	// HistoryBlocks should produce textHead + image frames + textTail with markers.
	blocks := HistoryBlocks(res.Archive, 10_000_000)
	imgCount := 0
	for _, b := range blocks {
		if b.Type == "image" {
			imgCount++
		}
	}
	if imgCount != len(res.Archive.Frames) {
		t.Errorf("HistoryBlocks image count = %d, want %d", imgCount, len(res.Archive.Frames))
	}
	if !strings.Contains(blocks[0].Text, "imaged middle below") {
		t.Errorf("first block missing imaged-middle-below marker: %q", blocks[0].Text)
	}
	if !strings.Contains(blocks[len(blocks)-1].Text, "imaged middle above") {
		t.Errorf("last block missing imaged-middle-above marker: %q", blocks[len(blocks)-1].Text)
	}

	// Re-compacting with PreviousArchive unfolds prior source and appends new messages.
	res2, err := Compact(Preparation{
		FirstKeptEntryID: "e-keep-2",
		MessagesToSummarize: []map[string]any{
			{"role": "user", "content": "Follow-up question after compaction"},
		},
		TokensBefore:    130_000,
		PreviousArchive: &res.Archive,
	}, CompactOptions{
		Model:     ModelTarget{ID: "gemini-4-neon", Provider: "github-copilot", API: "openai-completions", Input: []string{"text", "image"}},
		Shape:     &sh,
		MaxFrames: 4,
	})
	if err != nil {
		t.Fatalf("re-Compact failed: %v", err)
	}
	if !strings.Contains(ArchiveSourceText(res2.Archive), "Follow-up question after compaction") {
		t.Errorf("re-compacted archive lost new turn")
	}

	// ViewWithoutFrames produces clean text-only fallback with no frame legend.
	txtSummary, plainHist := ViewWithoutFrames(res.Summary, res.Archive, false)
	if strings.Contains(txtSummary, "Some middle sections: images") {
		t.Errorf("ViewWithoutFrames kept image legend:\n%s", txtSummary)
	}
	if strings.Contains(plainHist, "¶think:") {
		t.Errorf("ViewWithoutFrames(includeThinking=false) kept ¶think:")
	}
}

func TestParseCompactArgs(t *testing.T) {
	cases := []struct {
		in    string
		mode  string
		instr string
		err   bool
	}{
		{"", "", "", false},
		{"snapcompact", "snapcompact", "", false},
		{"SNAPCOMPACT", "snapcompact", "", false},
		{"snapcompact keep tests", "", "", true},
		{"soft", "soft", "", false},
		{"soft preserve API names", "soft", "preserve API names", false},
		{"handoff focus on auth", "handoff", "focus on auth", false},
		{"remote", "remote", "", false},
		{"preserve database schema", "", "preserve database schema", false},
	}
	for _, tc := range cases {
		got := ParseCompactArgs(tc.in)
		if (got.Err != "") != tc.err {
			t.Errorf("ParseCompactArgs(%q) err=%q, wantErr=%v", tc.in, got.Err, tc.err)
			continue
		}
		if !tc.err && (got.Mode != tc.mode || got.Instructions != tc.instr) {
			t.Errorf("ParseCompactArgs(%q) = (%q, %q), want (%q, %q)", tc.in, got.Mode, got.Instructions, tc.mode, tc.instr)
		}
	}
}

// ── End-to-end wire harness tests (EventSessionBeforeCompact + EventContext + EventInput) ──

type fakeHost struct {
	t            *testing.T
	conn         net.Conn
	done         chan error
	handlers     map[string]int
	model        map[string]any
	entries      []json.RawMessage
	notifies     []string
	compactCalls []map[string]any
}

type wireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
	} `json:"register,omitempty"`
	Call *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"call,omitempty"`
	Response *struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response,omitempty"`
}

func (h *fakeHost) readFrame() wireFrame {
	h.t.Helper()
	var hdr [4]byte
	if _, err := io.ReadFull(h.conn, hdr[:]); err != nil {
		h.t.Fatalf("read frame header: %v", err)
	}
	n := binary.BigEndian.Uint32(hdr[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(h.conn, buf); err != nil {
		h.t.Fatalf("read frame body: %v", err)
	}
	var f wireFrame
	if err := json.Unmarshal(buf, &f); err != nil {
		h.t.Fatalf("unmarshal frame: %v", err)
	}
	return f
}

func (h *fakeHost) writeFrame(v any) {
	h.t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		h.t.Fatalf("marshal frame: %v", err)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(raw)))
	_, _ = h.conn.Write(hdr[:])
	_, _ = h.conn.Write(raw)
}

func startFakeHost(t *testing.T, ext *sdk.Extension, model map[string]any) (*fakeHost, func()) {
	t.Helper()
	hostSide, extSide := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ext.RunWithConn(extSide) }()

	h := &fakeHost{
		t:        t,
		conn:     hostSide,
		done:     done,
		handlers: map[string]int{},
		model:    model,
	}
	reg := h.readFrame()
	if reg.Type != "register" || reg.Register == nil {
		t.Fatalf("expected register, got %+v", reg)
	}
	for _, hnd := range reg.Register.Handlers {
		h.handlers[hnd.Event] = hnd.HandlerID
	}
	h.writeFrame(map[string]any{
		"type":  "ready",
		"ready": map[string]any{"cwd": t.TempDir(), "width": 120},
	})

	cleanup := func() {
		h.writeFrame(map[string]any{"type": "shutdown"})
		_ = hostSide.Close()
		<-done
	}
	return h, cleanup
}

func (h *fakeHost) emit(event string, payload map[string]any) map[string]any {
	h.t.Helper()
	hid, ok := h.handlers[event]
	if !ok {
		h.t.Fatalf("no handler for %s", event)
	}
	reqID := "req-" + event
	rawArgs, _ := json.Marshal(payload)
	h.writeFrame(map[string]any{
		"type": "request",
		"id":   reqID,
		"request": map[string]any{
			"method":     "event",
			"event":      event,
			"handler_id": hid,
			"args":       json.RawMessage(rawArgs),
		},
	})
	for {
		env := h.readFrame()
		if env.Type == "call" && env.Call != nil {
			var res any
			switch env.Call.Method {
			case "getSessionID":
				res = map[string]any{"sessionId": "sess-snap-1"}
			case "getModelInfo":
				res = h.model
			case "getModelRegistryState":
				res = map[string]any{
					"models":    []any{h.model},
					"providers": map[string]any{},
				}
			case "getEntries":
				res = h.entries
			case "getBranch":
				res = map[string]any{"entries": h.entries}
			case "watchSessionLog", "getSessionLog":
				var a map[string]any
				_ = json.Unmarshal(env.Call.Args, &a)
				cursor := 0
				if c, ok := a["cursor"].(float64); ok {
					cursor = int(c)
				}
				slice := []json.RawMessage{}
				if cursor < len(h.entries) {
					slice = h.entries[cursor:]
				}
				leafID := ""
				if len(h.entries) > 0 {
					var last map[string]any
					if json.Unmarshal(h.entries[len(h.entries)-1], &last) == nil {
						leafID, _ = last["id"].(string)
					}
				}
				res = map[string]any{
					"entries":    slice,
					"entryCount": len(h.entries),
					"hasMore":    false,
					"leafId":     leafID,
				}
			case "ui.notify":
				var a map[string]any
				_ = json.Unmarshal(env.Call.Args, &a)
				if msg, _ := a["message"].(string); msg != "" {
					h.notifies = append(h.notifies, msg)
				}
				res = map[string]any{"ok": true}
			case "compact":
				var a map[string]any
				_ = json.Unmarshal(env.Call.Args, &a)
				h.compactCalls = append(h.compactCalls, a)
				res = map[string]any{"ok": true}
			default:
				res = map[string]any{}
			}
			h.writeFrame(map[string]any{
				"type":        "call_result",
				"id":          env.ID,
				"call_result": map[string]any{"result": res},
			})
			continue
		}
		if env.Type == "response" && env.ID == reqID {
			if env.Response != nil && env.Response.Error != nil {
				h.t.Fatalf("event %s error: %s", event, env.Response.Error.Message)
			}
			var out map[string]any
			if env.Response != nil && len(env.Response.Result) > 0 && string(env.Response.Result) != "null" {
				_ = json.Unmarshal(env.Response.Result, &out)
			}
			return out
		}
	}
}

func TestEndToEndSessionBeforeCompactAndEventContext(t *testing.T) {
	prevSetting := Setting
	prevHandoff := HandoffPrompt
	defer func() {
		Setting = prevSetting
		HandoffPrompt = prevHandoff
	}()
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "compaction.methodOrder":
			return []string{"remote", "snapcompact", "handoff", "shake", "soft"}
		case "snapcompact.shape":
			return "auto"
		}
		return nil
	}
	HandoffPrompt = func(focus string) string { return "HANDOFF:" + focus }

	ext := sdk.New("snap-test")
	mgr := newManager()
	registerWithManager(ext, mgr)

	visionModel := map[string]any{
		"id":            "gemini-4-neon",
		"name":          "Gemini 4 Neon",
		"provider":      "github-copilot",
		"api":           "openai-completions",
		"contextWindow": 1000000,
		"input":         []string{"text", "image"},
	}
	h, cleanup := startFakeHost(t, ext, visionModel)
	defer cleanup()

	// Build enough messages to produce PNG frames at 2048px Gemini shape (~120k chars).
	var bigMsgs []any
	chunk := strings.Repeat("Implementing feature alpha beta gamma delta epsilon zeta. ", 25)
	for i := 0; i < 60; i++ {
		bigMsgs = append(bigMsgs,
			map[string]any{"role": "user", "content": chunk},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": chunk}}},
		)
	}

	// 1. Default compaction (no focus instructions) runs snapcompact locally!
	res := h.emit(sdk.EventSessionBeforeCompact, map[string]any{
		"type":   "session_before_compact",
		"reason": "manual",
		"preparation": map[string]any{
			"firstKeptEntryId":    "e-keep",
			"tokensBefore":        180000,
			"messagesToSummarize": bigMsgs,
			"turnPrefixMessages":  []any{},
			"fileOps": map[string]any{
				"read":    []string{"main.go"},
				"written": []string{},
				"edited":  []string{"main.go"},
			},
			"settings": map[string]any{
				"reserveTokens":    16384,
				"keepRecentTokens": 20000,
			},
		},
		"branchEntries": []any{},
	})
	comp, ok := res["compaction"].(map[string]any)
	if !ok {
		t.Fatalf("expected snapcompact CompactionResult, got %#v", res)
	}
	summary, _ := comp["summary"].(string)
	details, _ := comp["details"].(map[string]any)
	if !strings.Contains(summary, "HISTORY\n===================") {
		t.Fatalf("unexpected snapcompact summary:\n%s", summary)
	}
	archive, ok := ExtractArchiveFromDetails(details)
	if !ok || len(archive.Frames) == 0 {
		t.Fatalf("expected persisted snapcompact frames in details, got ok=%v frames=%d", ok, len(archive.Frames))
	}

	// 2. EventContext rewrites compactionSummary into a multimodal user message with PNG frames.
	ctxRes := h.emit(sdk.EventContext, map[string]any{
		"type": "context",
		"messages": []any{
			map[string]any{
				"role":         "compactionSummary",
				"summary":      summary,
				"tokensBefore": 180000,
				"timestamp":    1700000000000,
			},
			map[string]any{
				"role":    "user",
				"content": "continue",
			},
		},
	})
	msgsOut, ok := ctxRes["messages"].([]any)
	if !ok || len(msgsOut) != 2 {
		t.Fatalf("EventContext returned unexpected messages: %#v", ctxRes)
	}
	firstMsg := msgsOut[0].(map[string]any)
	if firstMsg["role"] != "user" {
		t.Fatalf("rewritten compactionSummary role = %v, want user", firstMsg["role"])
	}
	blocks, _ := firstMsg["content"].([]any)
	imgBlocks := 0
	for _, rawB := range blocks {
		bm, _ := rawB.(map[string]any)
		if bm["type"] == "image" && bm["mimeType"] == "image/png" && bm["data"] != "" {
			imgBlocks++
		}
	}
	if imgBlocks != len(archive.Frames) {
		t.Fatalf("EventContext attached %d image blocks, want %d", imgBlocks, len(archive.Frames))
	}

	// 3. Switching to a text-only model automatically degrades EventContext to text-only fallback!
	h.model = map[string]any{
		"id":            "text-only-llm",
		"provider":      "custom",
		"api":           "openai-completions",
		"contextWindow": 128000,
		"input":         []string{"text"},
	}
	ctxTextOnly := h.emit(sdk.EventContext, map[string]any{
		"type": "context",
		"messages": []any{
			map[string]any{
				"role":         "compactionSummary",
				"summary":      summary,
				"tokensBefore": 180000,
			},
		},
	})
	textOnlyFirst := ctxTextOnly["messages"].([]any)[0].(map[string]any)
	for _, rawB := range textOnlyFirst["content"].([]any) {
		if bm, _ := rawB.(map[string]any); bm["type"] == "image" {
			t.Fatalf("text-only model must not receive image blocks")
		}
	}

	// 4. `/compact soft` triggers ctx.Compact and bypasses snapcompact (falls back to PiG LLM summary).
	h.model = visionModel
	inSoft := h.emit(sdk.EventInput, map[string]any{"text": "/compact soft preserve errors"})
	if inSoft["action"] != "handled" {
		t.Fatalf("/compact soft action = %v, want handled", inSoft["action"])
	}
	if len(h.compactCalls) != 1 || h.compactCalls[0]["customInstructions"] != "preserve errors" {
		t.Fatalf("expected ctx.Compact with customInstructions, got %#v", h.compactCalls)
	}
	softRes := h.emit(sdk.EventSessionBeforeCompact, map[string]any{
		"type":               "session_before_compact",
		"customInstructions": "preserve errors",
		"preparation": map[string]any{
			"firstKeptEntryId": "e-keep",
			"tokensBefore":     10000,
		},
	})
	if softRes != nil {
		t.Fatalf("/compact soft should fall through to PiG default compaction, got %#v", softRes)
	}

	// 5. `/compact snapcompact focus` is rejected with an error notification.
	inBad := h.emit(sdk.EventInput, map[string]any{"text": "/compact snapcompact extra focus"})
	if inBad["action"] != "handled" {
		t.Fatalf("/compact snapcompact extra focus action = %v, want handled", inBad["action"])
	}
	if len(h.notifies) == 0 || !strings.Contains(h.notifies[len(h.notifies)-1], "does not take focus instructions") {
		t.Fatalf("expected focus rejection notice, got %#v", h.notifies)
	}

	// 6. Cold resume (cache empty) resolves archive from session entries JSONL.
	mgr.clearCache("sess-snap-1")
	entryRaw, _ := json.Marshal(map[string]any{
		"id":      "c-persisted",
		"type":    "compaction",
		"summary": summary,
		"details": details,
	})
	h.entries = []json.RawMessage{entryRaw}
	ctxCold := h.emit(sdk.EventContext, map[string]any{
		"type": "context",
		"messages": []any{
			map[string]any{"role": "compactionSummary", "summary": summary, "tokensBefore": 180000},
		},
	})
	coldBlocks := ctxCold["messages"].([]any)[0].(map[string]any)["content"].([]any)
	coldImgs := 0
	for _, rawB := range coldBlocks {
		if bm, _ := rawB.(map[string]any); bm["type"] == "image" {
			coldImgs++
		}
	}
	if coldImgs != len(archive.Frames) {
		t.Fatalf("cold resume EventContext attached %d images, want %d", coldImgs, len(archive.Frames))
	}
}

func TestEmbeddedFontsExist(t *testing.T) {
	// Ensure font files exist in embed FS and never accidentally get stripped.
	for _, name := range []string{"fonts/5x8.bdf.gz", "fonts/6x12.bdf.gz", "fonts/8x13.bdf.gz", "fonts/unscii-8.hex.gz"} {
		data, err := embeddedFonts.ReadFile(name)
		if err != nil || len(data) == 0 {
			t.Fatalf("embedded font %s missing or empty: err=%v len=%d", name, err, len(data))
		}
	}
}

func TestClaudeThinkingScrubAndMethodOrderFallback(t *testing.T) {
	prevSetting := Setting
	defer func() { Setting = prevSetting }()

	methodOrder := []string{"soft"}
	Setting = func(_ sdk.Context, key string) any {
		if key == "compaction.methodOrder" {
			return methodOrder
		}
		return nil
	}

	ext := sdk.New("snap-policy-test")
	mgr := newManager()
	registerWithManager(ext, mgr)

	claudeModel := map[string]any{
		"id":            "claude-sonnet-4-5",
		"provider":      "anthropic",
		"api":           "anthropic-messages",
		"contextWindow": 200000,
		"input":         []string{"text", "image"},
	}
	h, cleanup := startFakeHost(t, ext, claudeModel)
	defer cleanup()

	// 1. methodOrder=["soft"] skips snapcompact automatically.
	resSoftOrder := h.emit(sdk.EventSessionBeforeCompact, map[string]any{
		"type":   "session_before_compact",
		"reason": "threshold",
		"preparation": map[string]any{
			"firstKeptEntryId": "e1",
			"tokensBefore":     150000,
			"messagesToSummarize": []any{
				map[string]any{"role": "user", "content": "hi"},
			},
		},
	})
	if resSoftOrder != nil {
		t.Fatalf("expected nil fallback when methodOrder=[soft], got %#v", resSoftOrder)
	}

	// 2. methodOrder=["snapcompact","soft"] on Claude excludes ¶think: from summary and archive.
	methodOrder = []string{"snapcompact", "soft"}
	resClaude := h.emit(sdk.EventSessionBeforeCompact, map[string]any{
		"type":   "session_before_compact",
		"reason": "manual",
		"preparation": map[string]any{
			"firstKeptEntryId": "e1",
			"tokensBefore":     150000,
			"messagesToSummarize": []any{
				map[string]any{"role": "user", "content": "Analyze bug"},
				map[string]any{"role": "assistant", "content": []any{
					map[string]any{"type": "thinking", "thinking": "SECRET CHAIN OF THOUGHT"},
					map[string]any{"type": "text", "text": "Fixed the bug."},
				}},
			},
		},
	})
	comp, ok := resClaude["compaction"].(map[string]any)
	if !ok {
		t.Fatalf("expected snapcompact compaction for Claude, got %#v", resClaude)
	}
	summary, _ := comp["summary"].(string)
	if strings.Contains(summary, "¶think:") {
		t.Errorf("Claude summary should omit ¶think: legend:\n%s", summary)
	}
	archive, ok := ExtractArchiveFromDetails(comp["details"])
	if !ok {
		t.Fatalf("missing archive in Claude compaction details")
	}
	if strings.Contains(ArchiveSourceText(*archive), "SECRET CHAIN OF THOUGHT") {
		t.Errorf("Claude archive leaked assistant reasoning")
	}

	// 3. Non-vision model with strict `/compact snapcompact` cancels with an error notice.
	h.model = map[string]any{
		"id":            "text-only-small",
		"provider":      "custom",
		"api":           "openai-completions",
		"contextWindow": 64000,
		"input":         []string{"text"},
	}
	_ = h.emit(sdk.EventInput, map[string]any{"text": "/compact snapcompact"})
	resStrict := h.emit(sdk.EventSessionBeforeCompact, map[string]any{
		"type":   "session_before_compact",
		"reason": "manual",
		"preparation": map[string]any{
			"firstKeptEntryId": "e1",
			"tokensBefore":     50000,
			"messagesToSummarize": []any{
				map[string]any{"role": "user", "content": "hi"},
			},
		},
	})
	if cancel, _ := resStrict["cancel"].(bool); !cancel {
		t.Fatalf("expected cancel=true on strict snapcompact for text-only model, got %#v", resStrict)
	}
}
