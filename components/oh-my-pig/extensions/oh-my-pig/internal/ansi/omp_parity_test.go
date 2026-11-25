package ansi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/files"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

// Tests in this file assert against real omp v19.1.1 outputs captured in
// `/tmp/omp-proof9-ansi/omp.screen.txt`, `/tmp/omp-proof9-ansi/omp.raw.bin`,
// and `/tmp/omp-proof9-tern/report.md` (not self-written expectations).

// TestOmpParity_NativeReadBareRowAndInlineError verifies Finding 1:
//   - Collapsed filesystem read emits bare `omp.tool.read` with 0 body children
//     (matching omp Tern capture `03-tool-read`: `tool[omp.tool.read]`).
//   - Single-line error <= 120 chars emits bare `omp.tool.read` with 0 body
//     children so the head carries `— <gist>` (matching omp Tern capture
//     `06-tool-error`: `tool[omp.tool.read]`).
//   - Multi-line or > 120-char error emits `omp.tool.error` in the body.
//   - `read.toolResultPreview=true` via `ansi.Setting` reveals the preview body.
//
// upstream: packages/tui/src/chat/read-tool-group.ts:804-865,
//
//	packages/tui/src/chat/display-preferences.ts:19,
//	packages/tui/src/tools/native-view.ts:115-125
func TestOmpParity_NativeReadBareRowAndInlineError(t *testing.T) {
	origSetting := Setting
	t.Cleanup(func() { Setting = origSetting })
	Setting = nil

	res := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "[notes.txt#A1B2]\n1:project: oh-my-pig\n2:status: draft\n3:owner: proof-harness"}},
		Details: map[string]any{
			"displayContent": map[string]any{
				"text":      "project: oh-my-pig\nstatus: draft\nowner: proof-harness",
				"startLine": 1,
			},
		},
	}

	// Default (collapsed, read.toolResultPreview=false): bare inline row, 0 body nodes.
	nodes, byID := nvMustResult(t, "read", res, sdk.ToolRenderResultOptions{Expanded: false}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "notes.txt"},
	})
	nvAssertNode(t, byID, "omp.tool.read", "container")
	if len(nodes) != 1 {
		t.Fatalf("collapsed filesystem read emitted %d nodes, want 1 (bare omp.tool.read container)", len(nodes))
	}

	// Setting read.toolResultPreview=true reveals omp.tool.read.code even when collapsed.
	Setting = func(_ sdk.Context, key string) any {
		if key == "read.toolResultPreview" {
			return true
		}
		return nil
	}
	_, previewIDs := nvMustResult(t, "read", res, sdk.ToolRenderResultOptions{Expanded: false}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "notes.txt"},
	})
	nvAssertNode(t, previewIDs, "omp.tool.read.code", "lines", "project: oh-my-pig")
	Setting = nil

	// Single-line error <= 120 chars: 0 body nodes (matching omp Tern capture 06-tool-error).
	errShort := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Error: Path 'does-not-exist-proof.txt' not found"}},
	}
	shortNodes, shortIDs := nvMustResult(t, "read", errShort, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args:    map[string]any{"path": "does-not-exist-proof.txt"},
		IsError: true,
	})
	nvAssertNode(t, shortIDs, "omp.tool.read", "container")
	if len(shortNodes) != 1 {
		t.Fatalf("single-line error (<=120 chars) emitted %d nodes, want 1 (no omp.tool.error body)", len(shortNodes))
	}

	// Exactly 120 chars single-line: still fits in head gist (0 body nodes).
	exact120 := strings.Repeat("x", 120)
	exactNodes, _ := nvMustResult(t, "read", sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": exact120}},
	}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args:    map[string]any{"path": "missing.txt"},
		IsError: true,
	})
	if len(exactNodes) != 1 {
		t.Fatalf("120-char single-line error emitted %d nodes, want 1", len(exactNodes))
	}

	// 121 chars single-line: overflows head gist -> emits omp.tool.error body.
	over120 := strings.Repeat("y", 121)
	_, overIDs := nvMustResult(t, "read", sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": over120}},
	}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args:    map[string]any{"path": "missing.txt"},
		IsError: true,
	})
	nvAssertNode(t, overIDs, "omp.tool.error", "text", over120)
}

// TestOmpParity_EditCardHeaderAndSGR verifies Finding 2 (edit card header path
// precedence: details.path beats hashline `[notes.txt#TAG]` input path) AND
// compares both visible text and truecolor titanium SGR sequences against omp's
// real captured bytes in `/tmp/omp-proof9-ansi/omp.raw.bin` (120 columns).
//
// upstream: packages/tui/src/tools/edit.ts:1552-1561
func TestOmpParity_EditCardHeaderAndSGR(t *testing.T) {
	// Real omp v19.1.1 captured edit card lines (plain text, 120 cols, from
	// /tmp/omp-proof9-ansi/diff.txt lines 25-30 / omp.raw.bin):
	const ompAbsPath = "/tmp/omp-proof9-ansi/omp/work/notes.txt"
	wantPlainLines := []string{
		"╭─── ✎ Edit: 🗒 /tmp/omp-proof9-ansi/omp/work/notes.txt ⟦+1/-1⟧ ────────────────────────────────────────────────────────╮",
		"│   1│project: oh-my-pig                                                                                               │",
		"│  -2│status: draft                                                                                                    │",
		"│   +│status: verified                                                                                                 │",
		"│   3│owner: proof-harness                                                                                             │",
		"╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯",
	}
	editRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Updated " + ompAbsPath}},
		Details: map[string]any{
			"path": ompAbsPath,
			"diff": " 1|project: oh-my-pig\n-2|status: draft\n+2|status: verified\n 3|owner: proof-harness",
		},
	}
	editArgs := map[string]any{
		"input": "[notes.txt#A1B2]\nPUT 2.=2:\n+status: verified",
	}

	h := startAnsiTestHost(t, "", nil)
	rid, _ := h.resolveRenderer("edit")
	v := h.renderToolView(rid, "omp-edit-sgr", "edit", "result", editArgs, &editRes, false, false, false, 120)
	gotLines := assertTerminalViewInvariants(t, "omp-edit-120", v, 120)
	if len(gotLines) != len(wantPlainLines) {
		t.Fatalf("edit card line count = %d, want %d:\n%s", len(gotLines), len(wantPlainLines), StripANSI(strings.Join(gotLines, "\n")))
	}
	for i, want := range wantPlainLines {
		if got := StripANSI(gotLines[i]); got != want {
			t.Errorf("edit card row %d plain mismatch:\n  got:  %q\n  want: %q", i, got, want)
		}
	}

	// SGR truecolor assertions matched directly against /tmp/omp-proof9-ansi/omp.raw.bin:
	//   - toolSuccessBg:  \x1b[48;2;15;18;22m  (#0f1216)
	//   - borderMuted:    \x1b[38;2;31;37;45m  (#1f252d)
	//   - accent:         \x1b[38;2;0;180;255m (#00b4ff)
	//   - muted/context:  \x1b[38;2;156;163;176m (#9ca3b0)
	//   - dim (brackets): \x1b[38;2;107;114;128m (#6b7280)
	//   - toolDiffAdded:  \x1b[38;2;0;255;136m (#00ff88)
	//   - toolDiffRemoved:\x1b[38;2;255;71;87m (#ff4757)
	headerRaw := gotLines[0]
	for _, seq := range []string{
		"\x1b[48;2;15;18;22m",                            // darkTitanium card bg
		"\x1b[38;2;31;37;45m╭───\x1b[39m",                // borderMuted left cap
		"\x1b[38;2;0;180;255m✎\x1b[39m",                  // accent pencil icon
		"\x1b[38;2;0;180;255mEdit\x1b[39m",               // accent title
		"\x1b[38;2;0;180;255m" + ompAbsPath + "\x1b[39m", // accent absolute path
		"\x1b[38;2;107;114;128m⟦\x1b[39m",                // dim open bracket
		"\x1b[38;2;0;255;136m+1\x1b[39m",                 // readoutGreen +1
		"\x1b[38;2;255;71;87m-1\x1b[39m",                 // alertRed -1
		"\x1b[38;2;107;114;128m⟧\x1b[39m",                // dim close bracket
		"\x1b[38;2;31;37;45m╮\x1b[39m",                   // borderMuted right corner
	} {
		if !strings.Contains(headerRaw, seq) {
			t.Errorf("edit header missing omp titanium SGR %q in:\n  %q", seq, headerRaw)
		}
	}
	if !strings.Contains(gotLines[2], "\x1b[38;2;255;71;87m") {
		t.Errorf("removed diff line missing alertRed SGR: %q", gotLines[2])
	}
	if !strings.Contains(gotLines[3], "\x1b[38;2;0;255;136m") {
		t.Errorf("added diff line missing readoutGreen SGR: %q", gotLines[3])
	}

	// Precedence check: explicit args.file_path / args.path / edits[0].path wins
	// over details.path, while details.path wins over hashline `[path#TAG]` input.
	explicitRes := h.renderToolView(rid, "omp-edit-explicit", "edit", "result",
		map[string]any{"path": "explicit/arg.txt", "input": "[hashline/arg.txt#A1B2]\nPUT 1.=1:\n+x"},
		&sdk.ToolRenderResult{
			Details: map[string]any{
				"path": "/abs/details/path.txt",
				"diff": "-1 old\n+1 x",
			},
		}, false, false, false, 120)
	expLines := assertTerminalViewInvariants(t, "omp-edit-explicit", explicitRes, 120)
	if !strings.Contains(StripANSI(expLines[0]), "explicit/arg.txt") {
		t.Errorf("args.path must take precedence over details.path, got %q", StripANSI(expLines[0]))
	}

	// Real Go edit tool end-to-end check: hashline input with relative path resolves
	// to absolute details.path in header.
	workDir := t.TempDir()
	notesPath := filepath.Join(workDir, "notes.txt")
	if err := os.WriteFile(notesPath, []byte("project: oh-my-pig\nstatus: draft\nowner: proof-harness\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hReal := startAnsiTestHost(t, workDir, func(ext *sdk.Extension) { files.Register(ext) })
	readRes, readErr := hReal.executeRegisteredTool("read", map[string]any{"path": "notes.txt"})
	if readErr {
		t.Fatalf("read failed: %+v", readRes)
	}
	firstLine, _, _ := strings.Cut(resultText(readRes), "\n")
	realEditArgs := map[string]any{"input": firstLine + "\nPUT 2.=2:\n+status: verified\n"}
	realEditRes, realEditErr := hReal.executeRegisteredTool("edit", realEditArgs)
	if realEditErr {
		t.Fatalf("edit failed: %+v", realEditRes)
	}
	realRID, _ := hReal.resolveRenderer("edit")
	realView := hReal.renderToolView(realRID, "real-edit-abs", "edit", "result", realEditArgs, &realEditRes, false, false, false, 200)
	realLines := assertTerminalViewInvariants(t, "real-edit-abs", realView, 200)
	wantShown := shortenPath(notesPath)
	if !strings.Contains(StripANSI(realLines[0]), wantShown) {
		t.Errorf("real edit header = %q, want absolute shortened path %q", StripANSI(realLines[0]), wantShown)
	}
}

// TestOmpParity_CompactThinkingLevelAndBandGauge verifies Findings 3 & 4
// against omp v19.1.1's exact status band row captured in `/tmp/omp-proof9-ansi/omp.raw.bin`:
//
//	 π > ◑ Proof 1 > 🗑 omp-proof9-ansi/omp/work ▶─0.4%───────────────────────────────────────────────╎───────┃─────────128K─
//	╰─
//
// Also verifies `statusLine.compactThinkingLevel=false` restores `⬢ Proof 1 · ◑ med`,
// and verifies the working state row `  ⎋ Working…` + active brand ` ⠋ 0s`.
//
// upstream: packages/tui/src/status-line/segments.ts:218-232,365-381,
//
//	packages/tui/src/status-line/component.ts:3201-3636,
//	packages/tui/src/components/composer/band.ts:10-46
func TestOmpParity_CompactThinkingLevelAndBandGauge(t *testing.T) {
	pct04 := 0.4
	tok := 512
	d := StatusLineData{
		ModelName:      "Proof 1",
		ModelReasoning: true,
		ThinkingLevel:  "medium",
		Cwd:            "/tmp/omp-proof9-ansi/omp/work",
		ContextTokens:  &tok,
		ContextWindow:  128000,
		ContextPercent: &pct04,
	}

	band := RenderStatusBand(sdk.UITheme{}, d, 120)
	plain := StripANSI(band)
	// Exact plain row captured from omp.raw.bin at width=120 (0.4% / 128K).
	wantPlain := " π > ◑ Proof 1 > 🗑 omp-proof9-ansi/omp/work ▶─0.4%───────────────────────────────────────────────╎───────┃─────────128K─"
	if plain != wantPlain {
		t.Errorf("RenderStatusBand(120) plain mismatch:\n  got:  %q (w=%d)\n  want: %q (w=%d)",
			plain, tui.VisibleWidth(band), wantPlain, tui.VisibleWidth(wantPlain))
	}

	// Verify omp titanium SGR sequences match omp.raw.bin for the band:
	for _, seq := range []string{
		"\x1b[48;2;15;18;22m",                                      // statusLineBg #0f1216
		"\x1b[38;2;107;114;128mπ\x1b[39m",                          // dim π
		"\x1b[38;2;42;48;56m>",                                     // statusLineSep >
		"\x1b[38;2;0;180;255m◑ Proof 1\x1b[39m",                    // statusLineModel compact
		"\x1b[38;2;232;236;244m🗑 omp-proof9-ansi/omp/work\x1b[39m", // statusLinePath
		"\x1b[38;2;15;18;22m▶\x1b[0m",                              // right cap ▶ in bg-as-fg
		"\x1b[49m\x1b[38;2;0;180;255m─0.4%",                        // borderAccent gauge + pct
		"\x1b[38;2;156;163;176m╎",                                  // muted speculation tick
		"\x1b[38;2;57;152;191m┃",                                   // dimmed accent threshold tick
		"\x1b[38;2;57;152;191m128K",                                // dimmed accent window label
	} {
		if !strings.Contains(band, seq) {
			t.Errorf("band missing omp titanium SGR %q in:\n  %q", seq, band)
		}
	}

	// Non-compact branch (`statusLine.compactThinkingLevel: false`):
	// restores `⬢ Proof 1 · ◑ med`.
	compactOff := false
	dNonCompact := d
	dNonCompact.CompactThinkingLevel = &compactOff
	nonCompactPlain := StripANSI(RenderStatusBand(sdk.UITheme{}, dNonCompact, 120))
	if !strings.Contains(nonCompactPlain, "⬢ Proof 1 · ◑ med") {
		t.Errorf("compactThinkingLevel=false band = %q, want \"⬢ Proof 1 · ◑ med\"", nonCompactPlain)
	}

	// Working state: working row `  ⎋ Working…` above the band, brand `⠋ 0s`.
	now := time.Unix(1700000000, 0)
	dWork := d
	dWork.Working = true
	dWork.WorkingStartedAt = now
	dWork.Now = now
	dWork.SpinnerGlyph = "⠋"
	workBand := RenderStatusBand(sdk.UITheme{}, dWork, 120)
	workPlain := StripANSI(workBand)
	if !strings.HasPrefix(workPlain, " ⠋ 0s > ◑ Proof 1 > ") {
		t.Errorf("working band = %q, want prefix \" ⠋ 0s > ◑ Proof 1 > \"", workPlain)
	}
	if !strings.Contains(workBand, "\x1b[38;2;0;180;255m⠋ 0s\x1b[39m") {
		t.Errorf("working brand missing accent SGR: %q", workBand)
	}
	workRow := RenderWorkingRow(sdk.UITheme{}, "Working…", 120)
	if got := StripANSI(workRow); got != "  ⎋ Working…" {
		t.Errorf("working row = %q, want %q", got, "  ⎋ Working…")
	}
	if !strings.Contains(workRow, "\x1b[38;2;156;163;176m ⎋\x1b[39m \x1b[38;2;156;163;176mWorking…\x1b[39m") {
		t.Errorf("working row SGR mismatch: %q", workRow)
	}

	// Verify setting hook reads statusLine.compactThinkingLevel=false dynamically.
	origSetting := Setting
	t.Cleanup(func() { Setting = origSetting })
	Setting = func(_ sdk.Context, key string) any {
		if key == "statusLine.compactThinkingLevel" {
			return false
		}
		return nil
	}
	h := startAnsiTestHost(t, "", nil)
	h.emitEvent(sdk.EventSessionStart, map[string]any{})
	wc := h.waitForBandWidgetCount(1, time.Second)
	if len(wc) == 0 {
		t.Fatalf("expected status-band widget render")
	}
	bandLines := stringSliceField(wc[len(wc)-1].Args, "lines")
	if len(bandLines) != 1 {
		t.Fatalf("idle status-band widget must emit 1 row, got %d: %#v", len(bandLines), bandLines)
	}
	if got := StripANSI(bandLines[0]); !strings.Contains(got, "⬢ Proof 1 · ◑ med") {
		t.Errorf("statusLine.compactThinkingLevel=false not honoured via Setting hook: %q", got)
	}
	if installs := h.editorInstallCalls(); installs != 0 {
		t.Errorf("expected 0 ui.editor.install calls (PiG stock editor kept), got %d", installs)
	}
	if n := h.setEditorTextCalls(); n != 0 {
		t.Errorf("ui.setEditorText called %d times, want 0", n)
	}
}

// TestOmpParity_PlanModeAndErrorBanner verifies Round 2 terminal fixes against
// omp's captured outputs in `/tmp/omp-proof10-ansi/omp.snap.plan.txt`:
//   - Plan mode renders `🗺 Plan` in the status band (`π > ◑ Proof 1 > 🗺 Plan > 🗑 ...`).
//   - Provider error pins `✘ <msg>` + `Dismissed when you send your next message.`
//     between full-width red horizontal rules directly above the status band.
func TestOmpParity_PlanModeAndErrorBanner(t *testing.T) {
	pct1 := 1.0
	d := StatusLineData{
		ModelName:      "Proof 1",
		ModelReasoning: true,
		ThinkingLevel:  "medium",
		ModeLabel:      "PLAN",
		Cwd:            "/tmp/omp-proof10-ansi/omp/work",
		ContextWindow:  128000,
		ContextPercent: &pct1,
		PinnedError:    "400 Proof simulated provider error (type=invalid_request_error)",
	}
	rows := RenderStatusBandWidget(sdk.UITheme{}, d, 120)
	plain := make([]string, len(rows))
	for i, r := range rows {
		plain[i] = StripANSI(r)
	}
	joined := strings.Join(plain, "\n")
	for _, want := range []string{
		strings.Repeat("─", 120),
		" ✘ 400 Proof simulated provider error (type=invalid_request_error)",
		" Dismissed when you send your next message.",
		" π > ◑ Proof 1 > 🗺 Plan > 🗑 omp-proof10-ansi/omp/work ▶─1%",
		"128K─",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("status band widget missing %q in:\n%s", want, joined)
		}
	}
	for i, r := range rows {
		if w := tui.VisibleWidth(r); w > 120 {
			t.Errorf("row %d width %d > 120: %q", i, w, plain[i])
		}
	}
}

// TestOmpParity_NativeHeadMetadataRound2 verifies Round 2 item 3 native
// builder outputs against `/tmp/omp-proof10-tern/report.json`:
//   - grep emits omp.head.meta (`in .`, `2 matches · 1 file`)
//   - glob emits omp.head.meta (`2 files`, `in .`)
//   - ast_edit emits omp.head.target (`1 file`), omp.head.badge (`proposed:warning`),
//     omp.head.meta (`1 replacement`), and fallback body as `text` (not `lines`)
//   - web_search error emits omp.head.frame (`card`) and omp.head.tone (`error`)
func TestOmpParity_NativeHeadMetadataRound2(t *testing.T) {
	// grep: matches omp report.json head.meta ['in .', '2 matches · 1 file']
	_, gIDs := nvMustResult(t, "grep", sdk.ToolRenderResult{
		Details: map[string]any{
			"scopePath":      ".",
			"matchCount":     2,
			"fileCount":      1,
			"files":          []string{"generated.txt"},
			"displayContent": "# generated.txt\n*1│needle_alpha\n*2│needle_beta",
		},
	}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{Args: map[string]any{"pattern": "needle_"}})
	nvAssertNode(t, gIDs, "omp.head.meta#0", "text", "in .")
	nvAssertNode(t, gIDs, "omp.head.meta#1", "text", "2 matches · 1 file")

	// glob: matches omp report.json head.meta ['2 files', 'in .']
	_, glIDs := nvMustResult(t, "glob", sdk.ToolRenderResult{
		Details: map[string]any{
			"scopePath": ".",
			"fileCount": 2,
			"files":     []string{"generated.txt", "notes.txt"},
		},
	}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{Args: map[string]any{"path": "*.txt"}})
	nvAssertNode(t, glIDs, "omp.head.meta#0", "text", "2 files")
	nvAssertNode(t, glIDs, "omp.head.meta#1", "text", "in .")

	// ast_edit proposed fallback: matches omp report.json target='1 file',
	// badge=[{'text':'proposed','tone':'warning'}], meta=['1 replacement'],
	// fallback body kind=text (not lines/ansi).
	_, aeIDs := nvMustResult(t, "ast_edit", sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "-1 console.log(\"hi\")\n+1 print(\"hi\")"}},
		Details: map[string]any{
			"filesTouched":      1,
			"totalReplacements": 1,
			"applied":           false,
			"filesSearched":     1,
		},
	}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args: map[string]any{"ops": []any{map[string]any{"pat": "console.log($A)", "out": "print($A)"}}},
	})
	nvAssertNode(t, aeIDs, "omp.head.target", "text", "1 file")
	nvAssertNode(t, aeIDs, "omp.head.target_kind", "text", "text")
	nvAssertNode(t, aeIDs, "omp.head.meta#0", "text", "1 replacement")
	nvAssertNode(t, aeIDs, "omp.head.badge#0", "text", "proposed:warning")
	nvAssertNode(t, aeIDs, "omp.tool.context", "text", "console.log($A)")
	nvAssertNode(t, aeIDs, "omp.tool.output", "text", "console.log")

	// web_search error: matches omp report.json frame='card', tone='error'.
	_, wsIDs := nvMustResult(t, "web_search", sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "No web search backend is available on the active model's provider under --offline."}},
		Details: map[string]any{"error": "No web search backend is available on the active model's provider under --offline."},
	}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args:    map[string]any{"query": "oh-my-pig proof offline query"},
		IsError: true,
	})
	nvAssertNode(t, wsIDs, "omp.head.frame", "text", "card")
	nvAssertNode(t, wsIDs, "omp.head.tone", "text", "error")
	nvAssertNode(t, wsIDs, "omp.tool.error", "text", "No web search backend")
}

// TestOmpParity_WelcomeBannerVersionAndTips verifies Finding 8:
//   - Version row shows `v<PigletVersion>` (e.g. `v0.1.0`), never literal `oh-my-pig`.
//   - No tip mentions unported commands (`/shake`, `/tan`, `/omfg`, `/collab`,
//     `/join`, `/advisor`, `/cleanse`, `/usage reset`, `omp stats`, `omp -c`,
//     `omp auth-broker`).
//   - `{key:...}` placeholders expand to macOS keycaps (`⌃D`, `⇧⇥`) on darwin
//     and word modifiers (`Ctrl+D`, `⇧⇥`) on non-darwin platforms.
//
// upstream: packages/tui/src/prompt/welcome.ts:109-119,355-376,530-544
func TestOmpParity_WelcomeBannerVersionAndTips(t *testing.T) {
	lines := RenderWelcomeLines(sdk.UITheme{}, "", 120, 0)
	joined := StripANSI(strings.Join(lines, "\n"))
	wantVer := "v" + PigletVersion
	if !strings.Contains(joined, wantVer) {
		t.Errorf("welcome banner missing Piglet version %q:\n%s", wantVer, joined)
	}
	if strings.Contains(joined, "oh-my-pig") {
		t.Errorf("welcome banner must not print literal \"oh-my-pig\" on version row:\n%s", joined)
	}

	forbidden := []string{
		"/shake", "/tan", "/omfg", "/collab", "/join", "/advisor", "/cleanse",
		"/usage reset", "omp stats", "omp -c", "omp auth-broker", "omp auth-gateway",
		"omp cleanse", "PI_DIALECT",
	}
	for i, tip := range welcomeTips {
		for _, bad := range forbidden {
			if strings.Contains(tip, bad) {
				t.Errorf("welcomeTips[%d] mentions unported command/CLI %q: %q", i, bad, tip)
			}
		}
		if strings.Contains(tip, "Ctrl+") || strings.Contains(tip, "Alt+") || strings.Contains(tip, "Shift+") {
			t.Errorf("welcomeTips[%d] hard-codes key text instead of {key:...} placeholder: %q", i, tip)
		}
	}

	if got := expandTipKeys("Press {key:ctrl+d} and {key:shift+tab} or {key:escape}", "darwin"); got != "Press ⌃D and ⇧⇥ or ⎋" {
		t.Errorf("expandTipKeys(darwin) = %q, want %q", got, "Press ⌃D and ⇧⇥ or ⎋")
	}
	if got := expandTipKeys("Press {key:ctrl+d} and {key:shift+tab} or {key:escape}", "linux"); got != "Press Ctrl+D and ⇧⇥ or ⎋" {
		t.Errorf("expandTipKeys(linux) = %q, want %q", got, "Press Ctrl+D and ⇧⇥ or ⎋")
	}
}

// TestOmpParity_TerminalRound3Cards verifies every Round 3 terminal card fix
// against omp's exact captured lines from `/tmp/omp-proof11-ansi/omp.scrollback.txt`:
//   - Ask answered card: `│  ✔ Fast ...│` (tools/ask.ts:137-145).
//   - Write card: `╭─── ✎ Write: 🗒 generated.txt · 3 lines ───...╮` with
//     3-column right-aligned line numbers `  1 `, `  2 `, `  3 ` (tools/write.ts:179-202,385-399).
//   - Task card: assignment section + `├───...───┤` divider + settled row without durationMs.
//   - Bash sole `reject` dispatch: five-line inverse Discard banner inside the
//     bash card (`│  ✘ Discard: 1 replacement in 1 file ⟦AST Edit⟧ ...│` +
//     `│  Proof preview only ...│`) with no duplicate `Output` section (tools/bash.ts:867-916).
//   - Grep native `omp.tool.search.meta#0` emitting `"*1,*2"` for Tern code marks.
func TestOmpParity_TerminalRound3Cards(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)

	// 1. Answered ask card at 120 columns.
	askRID, _ := h.resolveRenderer("ask")
	askRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"answers": []any{
				map[string]any{"name": "mode", "question": "Choose a mode:", "answer": "Fast"},
			},
		},
	}
	askView := h.renderToolView(askRID, "r3-ask", "ask", "result", nil, &askRes, false, false, false, 120)
	askLines := assertTerminalViewInvariants(t, "r3-ask", askView, 120)
	wantAskRow := "│  ✔ Fast" + strings.Repeat(" ", 110) + "│"
	foundAsk := false
	for _, l := range askLines {
		if StripANSI(l) == wantAskRow {
			foundAsk = true
		}
	}
	if !foundAsk {
		t.Errorf("ask answered row mismatch:\ngot:\n%s\nwant row:\n%q", StripANSI(strings.Join(askLines, "\n")), wantAskRow)
	}

	// 2. Write card at 120 columns (exact match to omp.scrollback.txt L141-144).
	writeRID, _ := h.resolveRenderer("write")
	writeRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Successfully wrote generated.txt"}},
	}
	writeArgs := map[string]any{
		"path":    "generated.txt",
		"content": "needle_alpha\nneedle_beta\n",
	}
	writeView := h.renderToolView(writeRID, "r3-write", "write", "result", writeArgs, &writeRes, false, false, false, 120)
	writeLines := assertTerminalViewInvariants(t, "r3-write", writeView, 120)
	wantWrite := []string{
		"╭─── ✎ Write: 🗒 generated.txt · 3 lines ───────────────────────────────────────────────────────────────────────────────╮",
		"│   1 needle_alpha                                                                                                     │",
		"│   2 needle_beta                                                                                                      │",
		"│   3                                                                                                                  │",
		"╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯",
	}
	if len(writeLines) != len(wantWrite) {
		t.Fatalf("write card lines = %d, want %d:\n%s", len(writeLines), len(wantWrite), StripANSI(strings.Join(writeLines, "\n")))
	}
	for i, w := range wantWrite {
		if got := StripANSI(writeLines[i]); got != w {
			t.Errorf("write card row %d:\n got: %q\nwant: %q", i, got, w)
		}
	}

	// 3. Task card with assignment + settled progress row (exact match to omp.scrollback.txt L117-121).
	taskRID, _ := h.resolveRenderer("task")
	taskRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"progress": []any{
				map[string]any{
					"id":       "ProofChild",
					"agent":    "task",
					"status":   "completed",
					"requests": 1,
				},
			},
		},
	}
	taskArgs := map[string]any{"task": "Proof subagent check."}
	taskView := h.renderToolView(taskRID, "r3-task", "task", "result", taskArgs, &taskRes, false, false, false, 120)
	taskLines := assertTerminalViewInvariants(t, "r3-task", taskView, 120)
	taskPlain := StripANSI(strings.Join(taskLines, "\n"))
	for _, want := range []string{
		"│ Proof subagent check.",
		"├──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤",
		"│ ✔ ProofChild · 1 req",
	} {
		if !strings.Contains(taskPlain, want) {
			t.Errorf("task card missing %q in:\n%s", want, taskPlain)
		}
	}

	// 4. Bash sole `reject` dispatch card (exact match to omp.scrollback.txt L172-177).
	bashRID, _ := h.resolveRenderer("bash")
	bashRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Discarded: AST Edit: 1 replacement in 1 file. Reason: Proof preview only"}},
		Details: map[string]any{
			"toolCommandsSole": true,
			"toolCommands": []any{
				map[string]any{
					"tool":     "reject",
					"mode":     "execute",
					"exitCode": 0,
					"inner": map[string]any{
						"action": "discard",
						"label":  "AST Edit: 1 replacement in 1 file",
						"reason": "Proof preview only",
					},
				},
			},
		},
	}
	bashArgs := map[string]any{"command": `reject "Proof preview only"`}
	bashView := h.renderToolView(bashRID, "r3-bash-reject", "bash", "result", bashArgs, &bashRes, false, false, false, 80)
	bashLines := assertTerminalViewInvariants(t, "r3-bash-reject", bashView, 80)
	bashPlain := StripANSI(strings.Join(bashLines, "\n"))
	for _, want := range []string{
		"│  ✘ Discard: 1 replacement in 1 file ⟦AST Edit⟧",
		"│  Proof preview only",
	} {
		if !strings.Contains(bashPlain, want) {
			t.Errorf("bash reject card missing %q in:\n%s", want, bashPlain)
		}
	}
	if strings.Contains(bashPlain, "├─── Output") {
		t.Errorf("bash sole reject card should suppress duplicate Output section:\n%s", bashPlain)
	}
	_, gIDs := nvMustResult(t, "grep", sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "## generated.txt (2 matches)\n*1│needle_alpha\n*2│needle_beta\n"}},
		Details: map[string]any{"matchCount": 2, "fileCount": 1},
	}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args: map[string]any{"pattern": "needle_", "path": "."},
	})
	nvAssertNode(t, gIDs, "omp.tool.search.meta#0", "text", "*1,*2")
}

// TestOmpParity_AuditFindings4_5_6 verifies Allowlist Audit Findings 4, 5, and 6:
//   - Finding 4: `[Resolved path: ...]` (terminal) and `resolved ...` footnote
//     (native) render whenever `details.resolvedPath` is present (including plain
//     directories/files/binaries), matching packages/tui/src/tools/read.ts:671-675,835.
//   - Finding 5: a later successful non-partial `todo` snapshot invalidates the
//     earlier live `todo` card and re-renders it empty (terminal and native);
//     a failed follow-up does not displace the last-good card
//     (event-controller.ts:2376-2386, tool-execution.ts:119-128).
//   - Finding 6: RenderErrorBanner unpacks PiG's raw HTTP JSON provider error
//     (`400: {"message":"Proof simulated provider error","type":"invalid_request_error"}`)
//     into omp's `400 Proof simulated provider error (type=invalid_request_error)`
//     (packages/ai/src/utils/http-inspector.ts:317-346).
func TestOmpParity_AuditFindings4_5_6(t *testing.T) {
	// ── Finding 6: provider error banner JSON unpacking ─────────────────────
	cases := []struct {
		in   string
		want string
	}{
		{
			in:   `400: {"message":"Proof simulated provider error","type":"invalid_request_error"}`,
			want: `400 Proof simulated provider error (type=invalid_request_error)`,
		},
		{
			in:   `429: {"error":{"message":"Rate limit reached","type":"rate_limit_error","param":"model","code":"rate_limit_exceeded"}}`,
			want: `429 Rate limit reached (type=rate_limit_error param=model code=rate_limit_exceeded)`,
		},
		{
			in:   `Error: 500 {"error":"internal failure"}`,
			want: `500 internal failure`,
		},
		{
			in:   `400 Proof simulated provider error (type=invalid_request_error)`,
			want: `400 Proof simulated provider error (type=invalid_request_error)`,
		},
	}
	for _, tc := range cases {
		if got := formatProviderErrorMessage(tc.in); got != tc.want {
			t.Errorf("formatProviderErrorMessage(%q) =\n  got:  %q\n  want: %q", tc.in, got, tc.want)
		}
	}
	bannerRows := RenderErrorBanner(sdk.UITheme{}, `400: {"message":"Proof simulated provider error","type":"invalid_request_error"}`, 120)
	bannerPlain := StripANSI(strings.Join(bannerRows, "\n"))
	wantBanner := " ✘ 400 Proof simulated provider error (type=invalid_request_error)"
	if !strings.Contains(bannerPlain, wantBanner) {
		t.Errorf("RenderErrorBanner missing formatted error %q:\n%s", wantBanner, bannerPlain)
	}

	// ── Finding 4: [Resolved path: ...] / resolved ... whenever details.resolvedPath is set ──
	origSetting := Setting
	t.Cleanup(func() { Setting = origSetting })
	Setting = func(_ sdk.Context, key string) any {
		if key == "read.toolResultPreview" {
			return true
		}
		return nil
	}
	h := startAnsiTestHost(t, "", nil)
	h.emitEvent(sdk.EventSessionStart, map[string]any{})
	readRID, _ := h.resolveRenderer("read")
	dirRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "alpha.txt\nbeta.txt\n"}},
		Details: map[string]any{
			"isDirectory":  true,
			"resolvedPath": "/tmp/proof-dir/sub",
		},
	}
	dirView := h.renderToolView(readRID, "read-dir-rp", "read", "result",
		map[string]any{"path": "sub"}, &dirRes, false, true, false, 100)
	dirLines := assertTerminalViewInvariants(t, "read-dir-rp", dirView, 100)
	dirPlain := StripANSI(strings.Join(dirLines, "\n"))
	if !strings.Contains(dirPlain, "⟦Resolved path: /tmp/proof-dir/sub⟧") {
		t.Errorf("terminal read missing ⟦Resolved path: ...⟧ for directory details.resolvedPath:\n%s", dirPlain)
	}
	// Native read also emits `resolved /tmp/proof-dir/sub` footnote.
	_, rdIDs := nvMustResult(t, "read", dirRes, sdk.ToolRenderResultOptions{Expanded: true}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "sub"},
	})
	nvAssertNode(t, rdIDs, "omp.tool.stats", "text", "resolved /tmp/proof-dir/sub")

	// ── Finding 5: displaceable todo cards (terminal & native) ──────────────
	todoRID, _ := h.resolveRenderer("todo")
	todo1 := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "initialized"}},
		Details: map[string]any{
			"op": "init",
			"phases": []any{
				map[string]any{
					"name": "Verify",
					"tasks": []any{
						map[string]any{"content": "Check read card", "status": "pending"},
					},
				},
			},
		},
	}
	todo2 := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "completed"}},
		Details: map[string]any{
			"op": "done",
			"phases": []any{
				map[string]any{
					"name": "Verify",
					"tasks": []any{
						map[string]any{"content": "Check read card", "status": "completed"},
					},
				},
			},
		},
	}
	todoErr := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Task not found"}},
		Details: map[string]any{"error": "Task not found"},
	}

	// First todo card renders normally.
	v1 := h.renderToolView(todoRID, "todo-card-1", "todo", "result", map[string]any{"op": "init"}, &todo1, false, false, false, 80)
	lines1 := assertTerminalViewInvariants(t, "todo-card-1", v1, 80)
	if !strings.Contains(StripANSI(strings.Join(lines1, "\n")), "Check read card") {
		t.Fatalf("first todo card did not render tasks: %v", lines1)
	}

	// A failed follow-up todo does NOT displace todo-card-1.
	_ = h.renderToolView(todoRID, "todo-card-err", "todo", "result", map[string]any{"op": "done"}, &todoErr, true, false, false, 80)
	v1Still := h.renderToolView(todoRID, "todo-card-1", "todo", "result", map[string]any{"op": "init"}, &todo1, false, false, false, 80)
	lines1Still := assertTerminalViewInvariants(t, "todo-card-1-after-err", v1Still, 80)
	if !strings.Contains(StripANSI(strings.Join(lines1Still, "\n")), "Check read card") {
		t.Fatalf("failed todo follow-up must not displace previous good todo card")
	}

	// A second successful todo displaces todo-card-1: host receives tool_render_invalidate for todo-card-1,
	// and re-rendering todo-card-1 returns an empty container (0 lines).
	v2 := h.renderToolView(todoRID, "todo-card-2", "todo", "result", map[string]any{"op": "done"}, &todo2, false, false, false, 80)
	lines2 := assertTerminalViewInvariants(t, "todo-card-2", v2, 80)
	if !strings.Contains(StripANSI(strings.Join(lines2, "\n")), "☑ Check read card") {
		t.Fatalf("second todo card missing completed task: %v", lines2)
	}
	time.Sleep(15 * time.Millisecond)
	h.mu.Lock()
	invCard1 := false
	for _, n := range h.notifs {
		if n.Method == "tool_render_invalidate" && strField(n.Args, "card") == "todo-card-1" {
			invCard1 = true
		}
	}
	h.mu.Unlock()
	if !invCard1 {
		t.Errorf("second successful todo did not send tool_render_invalidate for todo-card-1")
	}
	v1Displaced := h.renderToolView(todoRID, "todo-card-1", "todo", "result", map[string]any{"op": "init"}, &todo1, false, false, false, 80)
	if v1Displaced.Root.Kind != "container" || len(v1Displaced.Root.Children) != 0 || len(v1Displaced.renderedLines(80)) != 0 {
		t.Errorf("displaced todo-card-1 must re-render as empty container (0 lines), got %#v", v1Displaced)
	}
	// Native surface also returns an empty container for the displaced card.
	surface.SetNative(true)
	t.Cleanup(surface.ResetForTests)
	v1DisplacedNative := h.renderToolView(todoRID, "todo-card-1", "todo", "result", map[string]any{"op": "init"}, &todo1, false, false, false, 80)
	if v1DisplacedNative.Root.Kind != "container" || len(v1DisplacedNative.Root.Children) != 0 {
		t.Errorf("displaced native todo-card-1 must re-render as empty container, got %#v", v1DisplacedNative)
	}
}

// TestOmpParity_PathStandardizationAndErrorCardBg verifies:
//   - Framed error cards (e.g. Web Search offline error) paint omp's titanium
//     `toolErrorBg` (`\x1b[48;2;26;15;16m`, `#1a0f10`) just like omp's `framedToolCard`.
//   - (a) Cwd-derived paths standardize the working directory once via
//     `dirs.StandardizeMacOSPath(cwd)` (matching omp's startup `getProjectDir()`):
//     a relative path (`../sibling/notes.txt`, `sub`) resolved against a cwd under
//     `/private/var/...` (or `/private/tmp/...` on darwin) draws the short spelling,
//     and status-line / `nvDisplayPath` / `formatToolWorkingDirectory` handle the
//     standardized cwd cleanly.
//   - (b) An already-absolute `/private/var/.../omp-local/x/note.md` passed in
//     details/args (e.g. `read` `details.resolvedPath` or `edit` `details.path`)
//     is drawn UNCHANGED — `shortenPath` NEVER strips `/private`.
//   - (c) Home directory replacement (`home -> ~`) is unchanged and matches omp's
//     `shortenPath` boundary rules (`render-utils.ts:927-986`).
func TestOmpParity_PathStandardizationAndErrorCardBg(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)

	// 1. Framed error card (Web Search offline error) emits titanium toolErrorBg #1a0f10.
	wsRID, _ := h.resolveRenderer("web_search")
	wsRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "No web search backend is available on the active model's provider under --offline."}},
		Details: map[string]any{
			"error": "No web search backend is available on the active model's provider under --offline.",
		},
	}
	wsView := h.renderToolView(wsRID, "ws-err-bg", "web_search", "result", map[string]any{"query": "test"}, &wsRes, true, false, false, 80)
	wsLines := assertTerminalViewInvariants(t, "ws-err-bg", wsView, 80)
	for i, row := range wsLines {
		if !strings.Contains(row, "\x1b[48;2;26;15;16m") {
			t.Errorf("web_search error row %d missing titanium toolErrorBg (\\x1b[48;2;26;15;16m): %q", i, row)
		}
	}

	// 2(c). Home replacement (`home -> ~`) matches omp render-utils.ts:927-986.
	if got := shortenPathWithHome("/Users/alice", "/Users/alice"); got != "~" {
		t.Errorf("shortenPathWithHome exact home = %q, want ~", got)
	}
	if got := shortenPathWithHome("/Users/alice/work/notes.txt", "/Users/alice"); got != "~/work/notes.txt" {
		t.Errorf("shortenPathWithHome child = %q, want ~/work/notes.txt", got)
	}
	if got := shortenPathWithHome("/Users/alice/work/notes.txt", "/Users/alice/"); got != "~/work/notes.txt" {
		t.Errorf("shortenPathWithHome trailing-slash home = %q, want ~/work/notes.txt", got)
	}
	if got := shortenPathWithHome("/Users/alice_other/notes.txt", "/Users/alice"); got != "/Users/alice_other/notes.txt" {
		t.Errorf("shortenPathWithHome prefix-collision = %q, want unchanged", got)
	}
	if got := shortenPathWithHome(`C:\Users\Alice\proj\a.go`, `C:\Users\Alice`); got != "~/proj/a.go" {
		t.Errorf("shortenPathWithHome windows = %q, want ~/proj/a.go", got)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		child := filepath.Join(home, "omp-home-check", "notes.txt")
		if got := shortenPath(child); got != "~/omp-home-check/notes.txt" {
			t.Errorf("shortenPath(%q) = %q, want ~/omp-home-check/notes.txt", child, got)
		}
	}

	// 2(b). Absolute `/private/.../omp-local/x/note.md` in details/args is drawn UNCHANGED.
	// Test both an existing darwin `/private/var/...` path (via t.TempDir()) and a synthetic one.
	baseTmp := t.TempDir()
	privBase := baseTmp
	if realBase, err := filepath.EvalSymlinks(baseTmp); err == nil && strings.HasPrefix(realBase, "/private/") {
		privBase = realBase
	}
	localAbs := filepath.Join(privBase, "omp-local", "x", "note.md")
	if err := os.MkdirAll(filepath.Dir(localAbs), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(localAbs, []byte("hello local\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := shortenPath(localAbs); got != localAbs {
		t.Errorf("shortenPath(%q) = %q, want unchanged (must never strip /private)", localAbs, got)
	}
	// Read card with `local://note.md` (with read.toolResultPreview=true so the framed card
	// body + warning lines render) and `details.resolvedPath` pointing at
	// `/private/.../omp-local/x/note.md` must preserve the exact `/private/...` spelling
	// in `⟦Resolved path: ...⟧`.
	origSetting := Setting
	Setting = func(_ sdk.Context, key string) any {
		if key == "read.toolResultPreview" {
			return true
		}
		return nil
	}
	t.Cleanup(func() { Setting = origSetting })
	readRID, _ := h.resolveRenderer("read")
	readRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "hello local\n"}},
		Details: map[string]any{
			"resolvedPath":   localAbs,
			"displayContent": map[string]any{"text": "hello local", "startLine": 1},
		},
	}
	readView := h.renderToolView(readRID, "read-local-priv", "read", "result",
		map[string]any{"path": "local://note.md"}, &readRes, false, true, false, 200)
	readLines := assertTerminalViewInvariants(t, "read-local-priv", readView, 200)
	readPlain := StripANSI(strings.Join(readLines, "\n"))
	if !strings.Contains(readPlain, "⟦Resolved path: "+localAbs+"⟧") {
		t.Errorf("read local:// card altered details.resolvedPath:\n  want: %q\n  got:\n%s", "⟦Resolved path: "+localAbs+"⟧", readPlain)
	}
	Setting = origSetting
	// Edit card with an absolute `/private/...` details.path also draws it verbatim (tool owns details.path spelling).
	editRID, _ := h.resolveRenderer("edit")
	editResPriv := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Updated " + localAbs}},
		Details: map[string]any{"path": localAbs, "diff": "-1|a\n+1|b"},
	}
	editViewPriv := h.renderToolView(editRID, "edit-abs-priv", "edit", "result",
		map[string]any{"input": "[local://note.md#A1B2]\nPUT 1.=1:\n+b"}, &editResPriv, false, false, false, 200)
	editPrivLines := assertTerminalViewInvariants(t, "edit-abs-priv", editViewPriv, 200)
	if got := StripANSI(editPrivLines[0]); !strings.Contains(got, localAbs) {
		t.Errorf("edit header altered absolute details.path:\n  got:  %q\n  want substring: %q", got, localAbs)
	}
	// And when details.path carries omp's short cwd-derived spelling (`/var/.../notes.txt`), Edit draws it verbatim.
	shortNotes := strings.TrimPrefix(filepath.Join(privBase, "notes.txt"), "/private")
	editResShort := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Updated " + shortNotes}},
		Details: map[string]any{"path": shortNotes, "diff": "-1|status: draft\n+1|status: verified"},
	}
	editViewShort := h.renderToolView(editRID, "edit-short-cwd", "edit", "result",
		map[string]any{"input": "[notes.txt#A1B2]\nPUT 1.=1:\n+status: verified"}, &editResShort, false, false, false, 200)
	editShortLines := assertTerminalViewInvariants(t, "edit-short-cwd", editViewShort, 200)
	if got := StripANSI(editShortLines[0]); strings.Contains(got, "/private") || !strings.Contains(got, shortNotes) {
		t.Errorf("edit header with omp short details.path = %q, want substring %q", got, shortNotes)
	}

	// 2(a). Cwd-derived relative paths under `/private/var/...` (darwin t.TempDir())
	// standardize the working directory once and draw the short spelling.
	if strings.HasPrefix(privBase, "/private/") {
		workSub := filepath.Join(privBase, "work")
		sibSub := filepath.Join(privBase, "sibling")
		if err := os.MkdirAll(workSub, 0o755); err != nil {
			t.Fatalf("MkdirAll work: %v", err)
		}
		if err := os.MkdirAll(sibSub, 0o755); err != nil {
			t.Fatalf("MkdirAll sibling: %v", err)
		}
		wantSibShort := strings.TrimPrefix(sibSub, "/private")
		// formatToolWorkingDirectory with relative `../sibling` resolved against `/private/var/.../work`
		// standardizes cwd once and returns the short `/var/.../sibling`.
		if got := formatToolWorkingDirectory("../sibling", workSub); got != wantSibShort {
			t.Errorf("formatToolWorkingDirectory(../sibling, %q) = %q, want %q", workSub, got, wantSibShort)
		}
		// formatToolWorkingDirectory with relative `notes.txt` resolved via bash cwd `../sibling/notes.txt`:
		wantNotesShort := strings.TrimPrefix(filepath.Join(sibSub, "notes.txt"), "/private")
		if got := formatToolWorkingDirectory("../sibling/notes.txt", workSub); got != wantNotesShort {
			t.Errorf("formatToolWorkingDirectory(../sibling/notes.txt, %q) = %q, want %q", workSub, got, wantNotesShort)
		}
		// Same directory (short workdir vs /private projectDir) yields "" (omitted cd prefix).
		if got := formatToolWorkingDirectory(strings.TrimPrefix(workSub, "/private"), workSub); got != "" {
			t.Errorf("formatToolWorkingDirectory same dir short vs /private = %q, want empty", got)
		}
		// Absolute `/private/.../omp-local/x` outside cwd stays `/private/...` verbatim.
		if got := formatToolWorkingDirectory(filepath.Dir(localAbs), workSub); got != filepath.Dir(localAbs) {
			t.Errorf("formatToolWorkingDirectory absolute outside cwd = %q, want %q", got, filepath.Dir(localAbs))
		}
		// nvDisplayPath: short cwd-derived file inside /private cwd resolves to relative `notes.txt`,
		// while absolute `/private/.../omp-local/x/note.md` outside cwd stays unchanged.
		shortInside := filepath.Join(strings.TrimPrefix(workSub, "/private"), "notes.txt")
		if got := nvDisplayPath(shortInside, workSub); got != "notes.txt" {
			t.Errorf("nvDisplayPath(%q, %q) = %q, want notes.txt", shortInside, workSub, got)
		}
		if got := nvDisplayPath(localAbs, workSub); got != localAbs {
			t.Errorf("nvDisplayPath(%q, %q) = %q, want unchanged %q", localAbs, workSub, got, localAbs)
		}
		// Status-line project dir display standardizes /private cwd cleanly (no `/private` prefix).
		scratch, disp := getProjectDirDisplay(workSub)
		if !scratch || strings.Contains(disp, "/private") {
			t.Errorf("getProjectDirDisplay(%q) = (scratch=%v, %q), want scratch=true without /private", workSub, scratch, disp)
		}
	}
}

// TestOmpParity_FramedCardBackgroundsTable verifies omp's unified framed-card
// background rule (`packages/tui/src/render/output-block.ts:94-105`,
// `render/tool-card.ts:233-246`, `render/utils.ts:105-109`) across every framed
// card kind:
//   - Cards with `applyBg: true` (default): `toolSuccessBg` (`#0f1216` = 48;2;15;18;22),
//     `toolErrorBg` (`#1a0f10` = 48;2;26;15;16), `toolPendingBg` (`#0f1216` = 48;2;15;18;22).
//   - Cards with `applyBg: false` in omp (`bash` result, `todo` success, `debug`,
//     `fetch` success, `github`): no card background (`\x1b[48;2;...` absent).
func TestOmpParity_FramedCardBackgroundsTable(t *testing.T) {
	origSetting := Setting
	Setting = func(_ sdk.Context, key string) any {
		if key == "read.toolResultPreview" {
			return true
		}
		return nil
	}
	t.Cleanup(func() { Setting = origSetting })

	h := startAnsiTestHost(t, "", nil)
	const (
		bgSuccess = "\x1b[48;2;15;18;22m"
		bgPending = "\x1b[48;2;15;18;22m"
		bgError   = "\x1b[48;2;26;15;16m"
	)
	cases := []struct {
		name    string
		tool    string
		phase   string
		args    map[string]any
		res     *sdk.ToolRenderResult
		isError bool
		wantBg  string // "" means applyBg: false (no 48;2; bg)
	}{
		{
			name:  "ask-success",
			tool:  "ask",
			phase: "result",
			args:  map[string]any{"questions": []any{map[string]any{"id": "mode", "question": "Choose a mode"}}},
			res: &sdk.ToolRenderResult{
				Details: map[string]any{
					"answers": []any{map[string]any{"id": "mode", "question": "Choose a mode", "answer": "Fast"}},
				},
			},
			wantBg: bgSuccess,
		},
		{
			name:  "ast_edit-success",
			tool:  "ast_edit",
			phase: "result",
			args:  map[string]any{"ops": []any{map[string]any{"pat": "console.log($A)", "out": "print($A)"}}, "paths": []any{"sample.js"}},
			res: &sdk.ToolRenderResult{
				Details: map[string]any{
					"scopePath":         "sample.js",
					"totalReplacements": 1,
					"filesTouched":      1,
					"filesSearched":     1,
					"applied":           false,
					"displayContent":    "# sample.js\n-1│console.log(\"hi\")\n+1│print(\"hi\")",
				},
			},
			wantBg: bgSuccess,
		},
		{
			name:    "ast_edit-error",
			tool:    "ast_edit",
			phase:   "result",
			args:    map[string]any{"paths": []any{"sample.js"}},
			res:     &sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "invalid pattern"}}},
			isError: true,
			wantBg:  bgError,
		},
		{
			name:  "eval-success",
			tool:  "eval",
			phase: "result",
			args:  map[string]any{"language": "py", "code": "print('eval_ok', 6 * 7)", "title": "Proof Python eval"},
			res: &sdk.ToolRenderResult{
				Details: map[string]any{
					"language": "python",
					"cells": []any{map[string]any{
						"language":   "python",
						"title":      "Proof Python eval",
						"code":       "print('eval_ok', 6 * 7)",
						"status":     "complete",
						"durationMs": 227,
						"output":     "eval_ok 42",
					}},
				},
			},
			wantBg: bgSuccess,
		},
		{
			name:    "eval-error",
			tool:    "eval",
			phase:   "result",
			args:    map[string]any{"language": "py", "code": "1/0", "title": "Fail"},
			res:     &sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "ZeroDivisionError"}}},
			isError: true,
			wantBg:  bgError,
		},
		{
			name:  "task-success",
			tool:  "task",
			phase: "result",
			args:  map[string]any{"context": "Proof subagent check."},
			res: &sdk.ToolRenderResult{
				Details: map[string]any{
					"progress": []any{map[string]any{
						"id":       "ProofChild",
						"status":   "completed",
						"requests": 1,
						"task":     "Reply with PROOF_CHILD_OK.",
					}},
				},
			},
			wantBg: bgSuccess,
		},
		{
			name:    "task-error",
			tool:    "task",
			phase:   "result",
			args:    map[string]any{"context": "Bad task"},
			res:     &sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "unknown agent"}}},
			isError: true,
			wantBg:  bgError,
		},
		{
			name:   "write-call-pending",
			tool:   "write",
			phase:  "call",
			args:   map[string]any{"path": "generated.txt", "content": "needle_alpha\n"},
			wantBg: bgPending,
		},
		{
			name:   "write-success",
			tool:   "write",
			phase:  "result",
			args:   map[string]any{"path": "generated.txt", "content": "needle_alpha\nneedle_beta\n"},
			res:    &sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "Wrote generated.txt"}}},
			wantBg: bgSuccess,
		},
		{
			name:    "write-error",
			tool:    "write",
			phase:   "result",
			args:    map[string]any{"path": "generated.txt", "content": "x"},
			res:     &sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "permission denied"}}},
			isError: true,
			wantBg:  bgError,
		},
		{
			name:  "edit-success",
			tool:  "edit",
			phase: "result",
			args:  map[string]any{"path": "notes.txt"},
			res: &sdk.ToolRenderResult{
				Details: map[string]any{"path": "notes.txt", "diff": "-1|a\n+1|b"},
			},
			wantBg: bgSuccess,
		},
		{
			name:  "read-expanded-code-success",
			tool:  "read",
			phase: "result",
			args:  map[string]any{"path": "notes.txt"},
			res: &sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": "hello\n"}},
				Details: map[string]any{"displayContent": map[string]any{"text": "hello", "startLine": 1}},
			},
			wantBg: bgSuccess,
		},
		{
			name:  "read-expanded-markdown-success",
			tool:  "read",
			phase: "result",
			args:  map[string]any{"path": "doc.md"},
			res: &sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": "# Title\nbody\n"}},
				Details: map[string]any{"displayContent": map[string]any{"text": "# Title\nbody", "startLine": 1}},
			},
			wantBg: bgSuccess,
		},
		{
			name:  "web_search-success",
			tool:  "web_search",
			phase: "result",
			args:  map[string]any{"query": "omp"},
			res: &sdk.ToolRenderResult{
				Details: map[string]any{
					"response": map[string]any{
						"provider": "openai",
						"answer":   "An answer.",
						"sources":  []any{map[string]any{"title": "Doc", "url": "https://example.com"}},
					},
				},
			},
			wantBg: bgSuccess,
		},
		{
			name:    "web_search-error",
			tool:    "web_search",
			phase:   "result",
			args:    map[string]any{"query": "omp"},
			res:     &sdk.ToolRenderResult{Details: map[string]any{"error": "offline"}},
			isError: true,
			wantBg:  bgError,
		},
		{
			name:  "goal-success",
			tool:  "goal",
			phase: "result",
			args:  map[string]any{"op": "status"},
			res: &sdk.ToolRenderResult{
				Details: map[string]any{
					"goal": map[string]any{"status": "active", "objective": "Ship parity", "tokensUsed": 100},
				},
			},
			wantBg: bgSuccess,
		},
		// Cards with applyBg: false in omp:
		{
			name:   "bash-result-no-bg",
			tool:   "bash",
			phase:  "result",
			args:   map[string]any{"command": "echo hi"},
			res:    &sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "hi"}}},
			wantBg: "",
		},
		{
			name:  "todo-success-no-bg",
			tool:  "todo",
			phase: "result",
			args:  map[string]any{"op": "init"},
			res: &sdk.ToolRenderResult{
				Details: map[string]any{
					"phases": []any{map[string]any{"name": "Verify", "tasks": []any{map[string]any{"id": "1", "content": "A", "status": "completed"}}}},
				},
			},
			wantBg: "",
		},
		{
			name:    "todo-error-has-bg",
			tool:    "todo",
			phase:   "result",
			args:    map[string]any{"op": "init"},
			res:     &sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "bad todo"}}},
			isError: true,
			wantBg:  bgError,
		},
		{
			name:   "debug-result-no-bg",
			tool:   "debug",
			phase:  "result",
			args:   map[string]any{"action": "status"},
			res:    &sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "stopped"}}},
			wantBg: "",
		},
		{
			name:   "github-result-no-bg",
			tool:   "github",
			phase:  "result",
			args:   map[string]any{"op": "pr_view"},
			res:    &sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "line 1\nline 2"}}},
			wantBg: "",
		},
		{
			name:  "fetch-success-no-bg",
			tool:  "fetch",
			phase: "result",
			args:  map[string]any{"url": "https://example.com"},
			res: &sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": "hello world"}},
				Details: map[string]any{"url": "https://example.com", "finalUrl": "https://example.com", "contentType": "text/plain", "method": "GET"},
			},
			wantBg: "",
		},
		{
			name:    "fetch-error-has-bg",
			tool:    "fetch",
			phase:   "result",
			args:    map[string]any{"url": "https://example.com"},
			res:     &sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "404 Not Found"}}},
			isError: true,
			wantBg:  bgError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rid, _ := h.resolveRenderer(tc.tool)
			if rid == "" {
				t.Fatalf("no renderer for %s", tc.tool)
			}
			isPartial := tc.phase == "call"
			v := h.renderToolView(rid, "bg-"+tc.name, tc.tool, tc.phase, tc.args, tc.res, tc.isError, false, isPartial, 80)
			rows := assertTerminalViewInvariants(t, tc.name, v, 80)
			if len(rows) < 2 {
				t.Fatalf("expected framed card (>=2 rows), got %d: %q", len(rows), rows)
			}
			for i, row := range rows {
				if tc.wantBg != "" {
					if !strings.Contains(row, tc.wantBg) {
						t.Errorf("row %d missing expected bg %q:\n  %q", i, tc.wantBg, row)
					}
				} else if strings.Contains(row, "\x1b[48;2;") {
					t.Errorf("row %d should have applyBg:false (no 48;2; bg), got:\n  %q", i, row)
				}
			}
		})
	}
}

// TestOmpParity_ForegroundSGRFixes verifies the exact per-cell foreground SGR
// fixes from /tmp/omp-proof20-ansi against omp v19.1.1's real SGR output:
//   - ast_edit header scope `sample.js`: muted (156;163;176), only `in` dim (107;114;128)
//   - eval card: all LANG_BRAND_COLORS (Python 55;118;171, JS 247;223;30, Ruby 204;52;45,
//     Julia 149;88;178) and default-fg ` · ` separator before dim `(227ms)`
//   - task card: first subagent `ProofChild` in readoutGreen (0;255;136)
//   - write card: muted space after 🗒 (via wrapForeground) and default-fg plain body lines
//   - inline grep: muted scope `.` (156;163;176) and muted match gutter `*1│` (156;163;176)
func TestOmpParity_ForegroundSGRFixes(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)

	const (
		fgMuted   = "\x1b[38;2;156;163;176m"
		fgDim     = "\x1b[38;2;107;114;128m"
		fgAccent  = "\x1b[38;2;0;180;255m"
		fgSuccess = "\x1b[38;2;0;255;136m"
		fgPython  = "\x1b[38;2;55;118;171m"
		fgJS      = "\x1b[38;2;247;223;30m"
		fgRuby    = "\x1b[38;2;204;52;45m"
		fgJulia   = "\x1b[38;2;149;88;178m"
	)

	// 1. All 4 omp LANG_BRAND_COLORS in langIconStyled (theme-class.ts:133-138).
	p := palette{titanium: true, mode: "truecolor"}
	for _, langCase := range []struct {
		lang string
		want string
	}{
		{"python", fgPython + "🐍\x1b[39m"},
		{"javascript", fgJS + "🟨\x1b[39m"},
		{"ruby", fgRuby + "💎\x1b[39m"},
		{"julia", fgJulia + "Ⓙ\x1b[39m"},
		{"go", fgMuted + "🐹\x1b[39m"}, // non-brand language falls back to muted
	} {
		if got := p.langIconStyled(langCase.lang); got != langCase.want {
			t.Errorf("langIconStyled(%q) = %q, want %q", langCase.lang, got, langCase.want)
		}
	}

	// 2. ast_edit header: `console.log($A) ` muted, `in` dim, ` sample.js` muted.
	astRID, _ := h.resolveRenderer("ast_edit")
	astRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"scopePath": "sample.js", "totalReplacements": 1, "filesTouched": 1, "filesSearched": 1, "applied": false,
			"displayContent": "# sample.js\n-1│console.log(\"hi\")\n+1│print(\"hi\")",
		},
	}
	astV := h.renderToolView(astRID, "fg-ast-edit", "ast_edit", "result",
		map[string]any{"ops": []any{map[string]any{"pat": "console.log($A)", "out": "print($A)"}}, "paths": []any{"sample.js"}},
		&astRes, false, false, false, 80)
	astRows := assertTerminalViewInvariants(t, "fg-ast-edit", astV, 80)
	if !strings.Contains(astRows[0], fgDim+"in\x1b[39m"+fgMuted+" sample.js\x1b[39m") {
		t.Errorf("ast_edit header scope not muted after dim 'in':\n  %q", astRows[0])
	}

	// 3. eval card: Python blue icon + uncolored ` · ` separator before dim `(227ms)`.
	evalRID, _ := h.resolveRenderer("eval")
	evalRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"language": "python",
			"cells": []any{map[string]any{
				"language": "python", "title": "Proof Python eval", "code": "print('eval_ok', 6 * 7)",
				"status": "complete", "durationMs": 227, "output": "eval_ok 42",
			}},
		},
	}
	evalV := h.renderToolView(evalRID, "fg-eval", "eval", "result",
		map[string]any{"language": "py", "code": "print('eval_ok', 6 * 7)", "title": "Proof Python eval"},
		&evalRes, false, false, false, 80)
	evalRows := assertTerminalViewInvariants(t, "fg-eval", evalV, 80)
	if !strings.Contains(evalRows[0], fgPython+"🐍\x1b[39m") {
		t.Errorf("eval header missing Python blue icon:\n  %q", evalRows[0])
	}
	if !strings.Contains(evalRows[0], "Proof Python eval\x1b[39m · "+fgDim+"(227ms)\x1b[39m") {
		t.Errorf("eval header separator ' · ' should be default fg before dim (227ms):\n  %q", evalRows[0])
	}

	// 4. task card: ProofChild agent name painted in readoutGreen (0;255;136).
	taskRID, _ := h.resolveRenderer("task")
	taskRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"progress": []any{map[string]any{
				"id": "ProofChild", "status": "completed", "requests": 1, "task": "Reply with PROOF_CHILD_OK.",
			}},
		},
	}
	taskV := h.renderToolView(taskRID, "fg-task", "task", "result",
		map[string]any{"context": "Proof subagent check."}, &taskRes, false, true, false, 80)
	taskRows := assertTerminalViewInvariants(t, "fg-task", taskV, 80)
	if !strings.Contains(strings.Join(taskRows, "\n"), fgSuccess+"ProofChild\x1b[39m") {
		t.Errorf("task card missing readoutGreen ProofChild:\n  %q", taskRows)
	}

	// 5. write card: muted space after 🗒 and default-fg plain body line `needle_alpha`.
	writeRID, _ := h.resolveRenderer("write")
	writeRes := sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "Wrote generated.txt"}}}
	writeV := h.renderToolView(writeRID, "fg-write", "write", "result",
		map[string]any{"path": "generated.txt", "content": "needle_alpha\nneedle_beta\n"},
		&writeRes, false, false, false, 80)
	writeRows := assertTerminalViewInvariants(t, "fg-write", writeV, 80)
	if !strings.Contains(writeRows[0], fgMuted+"🗒\x1b[39m"+fgMuted+" "+fgAccent+"generated.txt") {
		t.Errorf("write header space after 🗒 not muted via wrapForeground:\n  %q", writeRows[0])
	}
	if !strings.Contains(writeRows[1], fgDim+"  1 \x1b[39mneedle_alpha") {
		t.Errorf("write plain code body should be default fg after dim gutter, not muted:\n  %q", writeRows[1])
	}

	// 6. inline grep: muted scope `.` and muted match gutter `*1│` (NOT accent).
	grepRID, _ := h.resolveRenderer("grep")
	grepRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"matchCount":     2,
			"fileCount":      1,
			"scopePath":      ".",
			"displayContent": "# generated.txt\n*1│needle_alpha\n*2│needle_beta",
		},
	}
	grepV := h.renderToolView(grepRID, "fg-grep", "grep", "result",
		map[string]any{"pattern": "needle_", "path": "."}, &grepRes, false, false, false, 80)
	grepRows := assertTerminalViewInvariants(t, "fg-grep", grepV, 80)
	if !strings.Contains(grepRows[0], fgDim+"in\x1b[39m"+fgMuted+" .\x1b[39m") {
		t.Errorf("grep header scope '.' should be muted after dim 'in':\n  %q", grepRows[0])
	}
	joinedGrep := strings.Join(grepRows, "\n")
	if !strings.Contains(joinedGrep, fgMuted+"*1│\x1b[39m") || strings.Contains(joinedGrep, fgAccent+"*1│") {
		t.Errorf("grep match gutter '*1│' should be muted (156;163;176), not accent:\n  %q", joinedGrep)
	}
}
