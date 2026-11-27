package eval

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestEvalSpillFormattingAndNormalization(t *testing.T) {
	meta := &TruncationMeta{
		Direction:   "tail",
		TruncatedBy: "bytes",
		TotalLines:  2000,
		TotalBytes:  120000,
		OutputLines: 800,
		OutputBytes: 48000,
		MaxBytes:    DefaultOutputMaxBytes,
		ShownRange:  &LineRange{Start: 1201, End: 2000},
	}

	// Without artifact: preserves exact existing notice.
	noSpill := formatTruncationNotice(meta)
	wantNoSpill := "\n\n[Showing lines 1201-2000 of 2000 (50.0KB limit)]"
	if noSpill != wantNoSpill {
		t.Fatalf("without spill = %q, want %q", noSpill, wantNoSpill)
	}

	// With bare artifact id -> artifact://<id>.
	metaBare := *meta
	metaBare.ArtifactID = normalizeArtifactID("eval-7")
	gotBare := formatTruncationNotice(&metaBare, "eval-7")
	wantBare := "\n\n[Showing lines 1201-2000 of 2000 (50.0KB limit). Read artifact://eval-7 for full output]"
	if gotBare != wantBare {
		t.Fatalf("bare id spill = %q, want %q", gotBare, wantBare)
	}
	if metaBare.ArtifactID != "eval-7" {
		t.Fatalf("ArtifactID = %q, want eval-7", metaBare.ArtifactID)
	}

	// With full artifact:// URL -> preserved verbatim.
	gotArt := formatTruncationNotice(meta, "artifact://42")
	wantArt := "\n\n[Showing lines 1201-2000 of 2000 (50.0KB limit). Read artifact://42 for full output]"
	if gotArt != wantArt {
		t.Fatalf("artifact:// spill = %q, want %q", gotArt, wantArt)
	}
	if id := normalizeArtifactID("artifact://42"); id != "42" {
		t.Fatalf("normalizeArtifactID(artifact://42) = %q, want 42", id)
	}

	// With local://<N>.log URL -> preserved verbatim and normalized to numeric id.
	gotLocal := formatTruncationNotice(meta, "local://9.log")
	wantLocal := "\n\n[Showing lines 1201-2000 of 2000 (50.0KB limit). Read local://9.log for full output]"
	if gotLocal != wantLocal {
		t.Fatalf("local:// spill = %q, want %q", gotLocal, wantLocal)
	}
	if id := normalizeArtifactID("local://9.log"); id != "9" {
		t.Fatalf("normalizeArtifactID(local://9.log) = %q, want 9", id)
	}
}

func TestEvalOversizedOutputSpillsWhenHookSet(t *testing.T) {
	origSpill := WriteSpill
	t.Cleanup(func() { WriteSpill = origSpill })

	ext := sdk.New("test-eval-spill")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)
	h := startHost(t, ext, hostOptions{})

	var spillCalls int
	var spillLabel, spillContent string
	WriteSpill = func(_ sdk.Context, label, content string) string {
		spillCalls++
		spillLabel = label
		spillContent = content
		return "artifact://eval-spill-1"
	}

	// 1. Small output fits inline and must NOT invoke WriteSpill.
	smallRaw, errMsg := h.callTool("eval", map[string]any{
		"language": "py",
		"title":    "Small cell",
		"code":     "print('fits-inline')",
	})
	if errMsg != "" {
		t.Fatalf("small eval failed: %s", errMsg)
	}
	if spillCalls != 0 {
		t.Fatalf("expected 0 spill calls for small output, got %d", spillCalls)
	}
	var smallRes struct {
		Content string          `json:"content"`
		Details EvalToolDetails `json:"details"`
	}
	if err := json.Unmarshal(smallRaw, &smallRes); err != nil {
		t.Fatalf("decode small result: %v", err)
	}
	if smallRes.Details.Meta != nil && smallRes.Details.Meta.Truncation != nil {
		t.Fatalf("unexpected truncation meta on small output: %+v", smallRes.Details.Meta.Truncation)
	}

	// 2. Oversized output (> 50 KiB) spills full output and appends artifact:// hint.
	bigRaw, errMsg := h.callTool("eval", map[string]any{
		"language": "py",
		"title":    "Big cell",
		"code":     "for i in range(1500):\n    print(f'LINE_{i:04d}_' + 'x'*50)\n",
	})
	if errMsg != "" {
		t.Fatalf("big eval failed: %s", errMsg)
	}
	if spillCalls != 1 {
		t.Fatalf("expected 1 spill call for oversized output, got %d", spillCalls)
	}
	if spillLabel != "Big cell" {
		t.Fatalf("spillLabel = %q, want %q", spillLabel, "Big cell")
	}
	if !strings.Contains(spillContent, "LINE_0000_") || !strings.Contains(spillContent, "LINE_1499_") {
		t.Fatalf("spilled content must include both start and end lines (len=%d)", len(spillContent))
	}

	var bigRes struct {
		Content string          `json:"content"`
		Details EvalToolDetails `json:"details"`
	}
	if err := json.Unmarshal(bigRaw, &bigRes); err != nil {
		t.Fatalf("decode big result: %v", err)
	}
	if bigRes.Content == "" {
		t.Fatalf("expected content in big eval result")
	}
	text := bigRes.Content
	if !strings.Contains(text, "Read artifact://eval-spill-1 for full output]") {
		t.Fatalf("expected artifact spill hint in eval result text, got tail:\n%s", text[max(0, len(text)-300):])
	}
	if bigRes.Details.Meta == nil || bigRes.Details.Meta.Truncation == nil {
		t.Fatalf("expected details.meta.truncation on oversized output")
	}
	if bigRes.Details.Meta.Truncation.ArtifactID != "eval-spill-1" {
		t.Fatalf("details.meta.truncation.artifactId = %q, want eval-spill-1", bigRes.Details.Meta.Truncation.ArtifactID)
	}

	// 3. When WriteSpill is nil, existing inline truncation behavior is preserved with no artifact hint.
	WriteSpill = nil
	noHookRaw, errMsg := h.callTool("eval", map[string]any{
		"language": "py",
		"code":     "for i in range(1500):\n    print(f'NOHOOK_{i:04d}_' + 'y'*50)\n",
	})
	if errMsg != "" {
		t.Fatalf("no-hook big eval failed: %s", errMsg)
	}
	var noHookRes struct {
		Content string          `json:"content"`
		Details EvalToolDetails `json:"details"`
	}
	if err := json.Unmarshal(noHookRaw, &noHookRes); err != nil {
		t.Fatalf("decode no-hook result: %v", err)
	}
	noHookText := noHookRes.Content
	if strings.Contains(noHookText, "artifact://") || strings.Contains(noHookText, "for full output") {
		t.Fatalf("expected no artifact hint when WriteSpill is nil, got tail:\n%s", noHookText[max(0, len(noHookText)-300):])
	}
	if noHookRes.Details.Meta == nil || noHookRes.Details.Meta.Truncation == nil || noHookRes.Details.Meta.Truncation.ArtifactID != "" {
		t.Fatalf("expected empty ArtifactID when WriteSpill is nil, got %+v", noHookRes.Details.Meta)
	}
}
