package ansi

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestTermOutWriteRendersAsAssistantProse(t *testing.T) {
	h := startAnsiTestHost(t, t.TempDir(), nil)
	rid, _ := h.resolveRenderer("write")
	if rid == "" {
		t.Fatal("write renderer not registered")
	}

	mdContent := "## Progress Update\n\n- Built step 1\n- Running step 2\n"
	outArgs := map[string]any{
		"path":    "out://",
		"content": mdContent,
	}
	res := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Shown to the user."}},
		Details: map[string]any{"touchesNoFiles": true},
	}

	// Both call and result phases render unframed Markdown prose (no Write header, no box border, no line numbers).
	for _, phase := range []string{"call", "result"} {
		var r *sdk.ToolRenderResult
		isPartial := phase == "call"
		if phase == "result" {
			r = &res
		}
		for _, w := range []int{20, 80, 120} {
			v := h.renderToolView(rid, "out-"+phase, "write", phase, outArgs, r, false, false, isPartial, w)
			lines := assertTerminalViewInvariants(t, "out-"+phase, v, w)
			joined := StripANSI(strings.Join(lines, "\n"))
			if strings.Contains(joined, "Write") || strings.Contains(joined, "out://") || strings.Contains(joined, "╭") || strings.Contains(joined, "Shown to the user") {
				t.Fatalf("%s@%d: write out:// must render as prose without Write card chrome:\n%s", phase, w, joined)
			}
			if w == 80 {
				if !strings.Contains(joined, "## Progress Update") || !strings.Contains(joined, "Built step 1") || !strings.Contains(joined, "Running step 2") {
					t.Fatalf("%s@80: missing prose body:\n%s", phase, joined)
				}
			}
		}
	}

	// Subpath out://status.md also renders as prose (out:// ignores URL path).
	subArgs := map[string]any{"path": "out://status.md", "content": "Halfway done."}
	vSub := h.renderToolView(rid, "out-subpath", "write", "result", subArgs, &res, false, false, false, 80)
	subLines := assertTerminalViewInvariants(t, "out-subpath", vSub, 80)
	subJoined := StripANSI(strings.Join(subLines, "\n"))
	if strings.Contains(subJoined, "Write") || !strings.Contains(subJoined, "Halfway done.") {
		t.Fatalf("out://status.md rendered unexpectedly:\n%s", subJoined)
	}

	// Error on out:// keeps prose (if any) and appends error detail without Write card chrome.
	errRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "out:// is off in this session"}},
	}
	vErr := h.renderToolView(rid, "out-err", "write", "result", outArgs, &errRes, true, false, false, 80)
	errLines := assertTerminalViewInvariants(t, "out-err", vErr, 80)
	errJoined := StripANSI(strings.Join(errLines, "\n"))
	if strings.Contains(errJoined, "Write") || !strings.Contains(errJoined, "out:// is off in this session") {
		t.Fatalf("out:// error rendered unexpectedly:\n%s", errJoined)
	}

	// Non-out:// write remains 100% unchanged (framed Write card with line numbers).
	fileArgs := map[string]any{"path": "notes.md", "content": "hello\nworld\n"}
	fileRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Successfully wrote 12 bytes to notes.md"}},
	}
	vFile := h.renderToolView(rid, "file-write", "write", "result", fileArgs, &fileRes, false, false, false, 80)
	fileLines := assertTerminalViewInvariants(t, "file-write", vFile, 80)
	fileJoined := StripANSI(strings.Join(fileLines, "\n"))
	if !strings.Contains(fileJoined, "✎ Write:") || !strings.Contains(fileJoined, "notes.md") || !strings.Contains(fileJoined, "  1 hello") {
		t.Fatalf("normal file write card regressed:\n%s", fileJoined)
	}
}

func TestNativeOutWriteRendersAsAssistantProse(t *testing.T) {
	mdContent := "## Interim Reply\n\nAll **checks** passed so far."

	// Call phase: omp.assistant container with omp.assistant.md markdown child (no omp.tool.write.source).
	_, callIDs := nvMustCall(t, "write", map[string]any{
		"path":    "out://",
		"content": mdContent,
	}, sdk.ToolRenderContext{ArgsComplete: true})
	nvAssertNode(t, callIDs, "omp.assistant", "container")
	nvAssertNode(t, callIDs, "omp.assistant.md", "markdown", "## Interim Reply", "All **checks** passed so far.")
	if _, hasSource := callIDs["omp.tool.write.source"]; hasSource {
		t.Fatalf("native out:// call must not emit omp.tool.write.source: %+v", callIDs)
	}

	// Result phase: same assistant prose shape, ignoring "Shown to the user.".
	res := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Shown to the user."}},
		Details: map[string]any{"touchesNoFiles": true},
	}
	_, resIDs := nvMustResult(t, "write", res, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "out://reply.md", "content": mdContent},
	})
	nvAssertNode(t, resIDs, "omp.assistant", "container")
	nvAssertNode(t, resIDs, "omp.assistant.md", "markdown", "## Interim Reply")
	if _, hasWrite := resIDs["omp.tool.write"]; hasWrite {
		t.Fatalf("native out:// result must not emit omp.tool.write: %+v", resIDs)
	}

	// Error phase: preserves prose markdown and adds omp.tool.error.
	errRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Subagents cannot reply to the user through out://"}},
	}
	_, errIDs := nvMustResult(t, "write", errRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args:    map[string]any{"path": "out://", "content": mdContent},
		IsError: true,
	})
	nvAssertNode(t, errIDs, "omp.assistant", "container")
	nvAssertNode(t, errIDs, "omp.assistant.md", "markdown", "## Interim Reply")
	nvAssertNode(t, errIDs, "omp.tool.error", "text", "Subagents cannot reply")
}
