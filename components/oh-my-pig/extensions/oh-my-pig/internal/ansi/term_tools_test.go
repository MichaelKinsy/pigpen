package ansi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/files"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/search"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/shell"
)

// TestRealGoToolsRenderedAtWidths20_80_200 executes real Go tool packages
// (files, search, shell, todo) against a temp workspace and renders their
// genuine sdk.ToolResult payloads through PiG SDK's view encoder at widths
// 20, 80, and 200, verifying omp's cards and strict width/node invariants.
func TestRealGoToolsRenderedAtWidths20_80_200(t *testing.T) {
	workDir := t.TempDir()
	notesPath := filepath.Join(workDir, "notes.txt")
	if err := os.WriteFile(notesPath, []byte("project: oh-my-pig\nstatus: draft\nowner: proof-harness\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	longLinePath := filepath.Join(workDir, "long.go")
	if err := os.WriteFile(longLinePath, []byte("package demo\n// needle "+strings.Repeat("x✅", 180)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := startAnsiTestHost(t, workDir, func(ext *sdk.Extension) {
		files.Register(ext)
		search.Register(ext)
		shell.Register(ext)
	})
	// 1. Real `read` (success + error) -> compact rows matching omp.screen.txt
	readRes, readErr := h.executeRegisteredTool("read", map[string]any{"path": "notes.txt"})
	if readErr {
		t.Fatalf("real read notes.txt failed: %+v", readRes)
	}
	readRID, _ := h.resolveRenderer("read")
	for _, w := range []int{20, 80, 200} {
		v := h.renderToolView(readRID, fmt.Sprintf("read-%d", w), "read", "result",
			map[string]any{"path": "notes.txt"}, &readRes, false, false, false, w)
		lines := assertTerminalViewInvariants(t, fmt.Sprintf("real-read@%d", w), v, w)
		if w == 80 {
			// Matches /tmp/omp-proof2/reference/ansi-omp/omp.screen.txt line 1: ` ✔ Read notes.txt`
			if len(lines) != 1 || StripANSI(lines[0]) != " ✔ Read notes.txt" {
				t.Errorf("collapsed read = %q, want %q", StripANSI(strings.Join(lines, "\n")), " ✔ Read notes.txt")
			}
		}
		// With read.toolResultPreview=false (omp default), global Ctrl+O expanded
		// keeps filesystem reads as compact one-line rows (read-tool-group.ts:1303-1316).
		vExpDefault := h.renderToolView(readRID, fmt.Sprintf("read-exp-default-%d", w), "read", "result",
			map[string]any{"path": "notes.txt"}, &readRes, false, true, false, w)
		expDefaultLines := assertTerminalViewInvariants(t, fmt.Sprintf("real-read-exp-default@%d", w), vExpDefault, w)
		if w == 80 {
			if len(expDefaultLines) != 1 || StripANSI(expDefaultLines[0]) != " ✔ Read notes.txt" {
				t.Errorf("expanded read with preview=false = %q, want %q", StripANSI(strings.Join(expDefaultLines, "\n")), " ✔ Read notes.txt")
			}
		}
		// With read.toolResultPreview=true, expanded reveals framed code cell with numbered lines.
		origSetting := Setting
		Setting = func(_ sdk.Context, key string) any {
			if key == "read.toolResultPreview" {
				return true
			}
			return nil
		}
		vExp := h.renderToolView(readRID, fmt.Sprintf("read-exp-%d", w), "read", "result",
			map[string]any{"path": "notes.txt"}, &readRes, false, true, false, w)
		Setting = origSetting
		expLines := assertTerminalViewInvariants(t, fmt.Sprintf("real-read-exp@%d", w), vExp, w)
		if w == 80 {
			joined := StripANSI(strings.Join(expLines, "\n"))
			if !strings.Contains(joined, "╭───") || !strings.Contains(joined, "project: oh-my-pig") {
				t.Errorf("expanded read with preview=true missing framed content:\n%s", joined)
			}
			if strings.Contains(joined, "Resolved path:") {
				t.Errorf("plain read should not show [Resolved path:] row:\n%s", joined)
			}
		}
	}

	readErrRes, readMissingErr := h.executeRegisteredTool("read", map[string]any{"path": "does-not-exist-proof.txt"})
	if !readMissingErr {
		t.Fatalf("expected error reading missing file")
	}
	vReadErr := h.renderToolView(readRID, "read-err", "read", "result",
		map[string]any{"path": "does-not-exist-proof.txt"}, &readErrRes, readMissingErr, false, false, 80)
	errLines := assertTerminalViewInvariants(t, "real-read-err@80", vReadErr, 80)
	// Matches /tmp/omp-proof2/reference/ansi-omp/omp.screen.txt line 35: ` ✘ Read does-not-exist-proof.txt`
	if len(errLines) != 1 || StripANSI(errLines[0]) != " ✘ Read does-not-exist-proof.txt" {
		t.Errorf("collapsed read error = %q, want %q", StripANSI(strings.Join(errLines, "\n")), " ✘ Read does-not-exist-proof.txt")
	}

	// 2. Real `write` -> framed card without figure or pending hourglass
	writeArgs := map[string]any{"path": "written.txt", "content": "hello\nworld\n"}
	writeRes, writeErr := h.executeRegisteredTool("write", writeArgs)
	if writeErr {
		t.Fatalf("real write failed: %+v", writeRes)
	}
	writeRID, _ := h.resolveRenderer("write")
	for _, w := range []int{20, 80, 200} {
		v := h.renderToolView(writeRID, fmt.Sprintf("write-%d", w), "write", "result",
			writeArgs, &writeRes, false, false, false, w)
		lines := assertTerminalViewInvariants(t, fmt.Sprintf("real-write@%d", w), v, w)
		if w == 80 {
			joined := StripANSI(strings.Join(lines, "\n"))
			if !strings.Contains(joined, "✎ Write: 🗒 written.txt") {
				t.Errorf("write header mismatch:\n%s", joined)
			}
		}
	}

	// 3. Real `edit` -> framed diff with omp gutter and ⟦+1/-1⟧ stats
	// Extract snapshot tag from readRes content (`[notes.txt#TAG]`).
	readText := resultText(readRes)
	firstLine, _, _ := strings.Cut(readText, "\n")
	editInput := firstLine + "\nPUT 2.=2:\n+status: verified\n"
	editArgs := map[string]any{"input": editInput}
	editRes, editErr := h.executeRegisteredTool("edit", editArgs)
	if editErr {
		t.Fatalf("real edit failed: %+v (input=%q)", editRes, editInput)
	}
	editRID, _ := h.resolveRenderer("edit")
	for _, w := range []int{20, 80, 200} {
		v := h.renderToolView(editRID, fmt.Sprintf("edit-%d", w), "edit", "result",
			editArgs, &editRes, false, false, false, w)
		lines := assertTerminalViewInvariants(t, fmt.Sprintf("real-edit@%d", w), v, w)
		if w == 80 {
			joined := StripANSI(strings.Join(lines, "\n"))
			if !strings.Contains(joined, "✎ Edit: 🗒") || !strings.Contains(joined, "⟦+1/-1⟧") {
				t.Errorf("edit header mismatch:\n%s", joined)
			}
			if !strings.Contains(joined, "│") || !strings.Contains(joined, "status: verified") {
				t.Errorf("edit diff not framed with gutter:\n%s", joined)
			}
			if strings.Contains(joined, "--- ") || strings.Contains(joined, "@@ ") {
				t.Errorf("edit card leaked raw unified diff headers:\n%s", joined)
			}
		}
	}

	// 4. Real `bash` -> no invented Bash header, Output divider present, stats only in bottom border
	bashArgs := map[string]any{"command": `printf 'alpha\nbeta\n'`}
	bashRes, bashErr := h.executeRegisteredTool("bash", bashArgs)
	bashRID, _ := h.resolveRenderer("bash")
	for _, w := range []int{20, 80, 200} {
		v := h.renderToolView(bashRID, fmt.Sprintf("bash-%d", w), "bash", "result",
			bashArgs, &bashRes, bashErr, false, false, w)
		lines := assertTerminalViewInvariants(t, fmt.Sprintf("real-bash@%d", w), v, w)
		if w == 80 {
			top := StripANSI(lines[0])
			bottom := StripANSI(lines[len(lines)-1])
			joined := StripANSI(strings.Join(lines, "\n"))
			if strings.Contains(top, "Bash") {
				t.Errorf("bash top border must not invent 'Bash' header: %q", top)
			}
			if !strings.Contains(joined, "├─── Output ─") {
				t.Errorf("bash missing Output divider:\n%s", joined)
			}
			if !strings.Contains(joined, "alpha") || !strings.Contains(joined, "beta") {
				t.Errorf("bash missing stdout lines:\n%s", joined)
			}
			if !strings.Contains(bottom, "⏱") {
				t.Errorf("bash bottom border missing wall time: %q", bottom)
			}
		}
	}

	// 5. Real `grep` with 300+ char match line containing ✅ -> clamped cleanly at 20, 80, 200
	grepArgs := map[string]any{"pattern": "needle"}
	grepRes, grepErr := h.executeRegisteredTool("grep", grepArgs)
	grepRID, _ := h.resolveRenderer("grep")
	for _, w := range []int{20, 80, 200} {
		v := h.renderToolView(grepRID, fmt.Sprintf("grep-%d", w), "grep", "result",
			grepArgs, &grepRes, grepErr, true, false, w)
		lines := assertTerminalViewInvariants(t, fmt.Sprintf("real-grep@%d", w), v, w)
		if w == 80 {
			joined := StripANSI(strings.Join(lines, "\n"))
			// Must NOT emit false "… N more matches" when all matches are shown.
			if strings.Contains(joined, "more match") {
				t.Errorf("expanded real grep emitted false more-matches row:\n%s", joined)
			}
		}
	}

	// 6. Real `glob`
	globArgs := map[string]any{"path": "*.txt"}
	globRes, globErr := h.executeRegisteredTool("glob", globArgs)
	globRID, _ := h.resolveRenderer("glob")
	for _, w := range []int{20, 80, 200} {
		v := h.renderToolView(globRID, fmt.Sprintf("glob-%d", w), "glob", "result",
			globArgs, &globRes, globErr, false, false, w)
		assertTerminalViewInvariants(t, fmt.Sprintf("real-glob@%d", w), v, w)
	}

	// 7. `todo` renderer across widths 20/80/200
	todoArgs := map[string]any{
		"op": "init",
		"list": []any{
			map[string]any{"phase": "Phase One", "items": []any{"First task", "Second task"}},
		},
	}
	todoRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Initialized 2 todos"}},
		Details: map[string]any{
			"phases": []any{
				map[string]any{
					"name": "Phase One",
					"tasks": []any{
						map[string]any{"id": "task-1", "content": "First task", "status": "in_progress"},
						map[string]any{"id": "task-2", "content": "Second task", "status": "pending"},
					},
				},
			},
		},
	}
	todoRID, _ := h.resolveRenderer("todo")
	for _, w := range []int{20, 80, 200} {
		v := h.renderToolView(todoRID, fmt.Sprintf("todo-%d", w), "todo", "result",
			todoArgs, &todoRes, false, false, false, w)
		lines := assertTerminalViewInvariants(t, fmt.Sprintf("real-todo@%d", w), v, w)
		if w == 80 {
			joined := StripANSI(strings.Join(lines, "\n"))
			if !strings.Contains(joined, "2 tasks") || !strings.Contains(joined, "├─") || !strings.Contains(joined, "└─") {
				t.Errorf("todo card missing tree connectors or literal '2 tasks':\n%s", joined)
			}
		}
	}
}

// TestBashNoticeStrippingAndGoldenCards ports omp's bash-render.test.ts expectations.
func TestBashNoticeStrippingAndGoldenCards(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)
	rid, _ := h.resolveRenderer("bash")

	// upstream: packages/tui/test/bash-render.test.ts: "renders wall time alongside the timeout label and strips the textual notice"
	res := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "hello\n\nWall time: 1.23 seconds"}},
		Details: map[string]any{"timeoutSeconds": 5.0, "wallTimeMs": 1230.0, "wallTimeNoticeEmitted": true},
	}
	v := h.renderToolView(rid, "b1", "bash", "result", map[string]any{"command": "echo hi"}, &res, false, false, false, 120)
	lines := assertTerminalViewInvariants(t, "bash-wall", v, 120)
	joined := StripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "⏱ 1.2s/5.0s") {
		t.Errorf("expected ⏱ 1.2s/5.0s in:\n%s", joined)
	}
	if strings.Contains(joined, "Wall time: 1.23 seconds") {
		t.Errorf("Wall time notice was not stripped:\n%s", joined)
	}

	// Failed command with Wall time + exit code + output notice + raw output local://7.log:
	// stripping in omp's order must strip ALL trailing notices and keep `local://7.log` verbatim.
	failRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "boom\n\n[raw output: local://7.log]\n\nWall time: 1.45 seconds\n\nCommand exited with code 2\n\n[Showing lines 1-1 of 10]"}},
		Details: map[string]any{
			"timeoutSeconds":        300.0,
			"wallTimeMs":            1450.0,
			"wallTimeNoticeEmitted": true,
			"exitCode":              2,
		},
	}
	vFail := h.renderToolView(rid, "b2", "bash", "result", map[string]any{"command": "false"}, &failRes, true, false, false, 120)
	failLines := assertTerminalViewInvariants(t, "bash-fail", vFail, 120)
	failJoined := StripANSI(strings.Join(failLines, "\n"))
	for _, unwanted := range []string{"Wall time: 1.45 seconds", "Command exited with code 2", "[Showing lines"} {
		if strings.Contains(failJoined, unwanted) {
			t.Errorf("failed bash leaked %q in:\n%s", unwanted, failJoined)
		}
	}
	if !strings.Contains(failJoined, "local://7.log") {
		t.Errorf("expected local://7.log (not artifact://7.log) in:\n%s", failJoined)
	}
	if !strings.Contains(failJoined, "✘ 2") {
		t.Errorf("expected exit code badge ✘ 2 in:\n%s", failJoined)
	}

	// Empty output STILL draws the Output divider (tools/bash.ts:899-916).
	emptyRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": ""}},
		Details: map[string]any{"wallTimeMs": 12.0, "timeoutSeconds": 300.0},
	}
	vEmpty := h.renderToolView(rid, "b3", "bash", "result", map[string]any{"command": "true"}, &emptyRes, false, false, false, 80)
	emptyLines := assertTerminalViewInvariants(t, "bash-empty", vEmpty, 80)
	if !strings.Contains(StripANSI(strings.Join(emptyLines, "\n")), "├─── Output ─") {
		t.Errorf("empty bash output must still draw Output divider:\n%s", StripANSI(strings.Join(emptyLines, "\n")))
	}
}

// TestGrepExpandedNodeLimitAndMatchCounting verifies the 3000-file expanded
// grep regression (review finding #3) and padded gutter match counting (#12).
func TestGrepExpandedNodeLimitAndMatchCounting(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)
	rid, _ := h.resolveRenderer("grep")

	// 3000 matching files expanded -> must stay well under 4096 nodes and follow EXPANDED_TEXT_LIMIT.
	var sb strings.Builder
	for i := range 3000 {
		fmt.Fprintf(&sb, "## f%04d.go\n   *5│match line %d %s\n\n", i, i, strings.Repeat("Z", 120))
	}
	bigRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": ""}},
		Details: map[string]any{
			"matchCount":     3000,
			"fileCount":      3000,
			"displayContent": sb.String(),
		},
	}
	v := h.renderToolView(rid, "g-big", "grep", "result", map[string]any{"pattern": "match"}, &bigRes, false, true, false, 80)
	lines := assertTerminalViewInvariants(t, "grep-3000@80", v, 80)
	if v.nodeCount() > 10 {
		t.Errorf("3000-file expanded grep built %d nodes (expected single Lines node <= 10)", v.nodeCount())
	}
	if len(lines) > 35 {
		t.Errorf("expanded grep emitted %d rows, expected bounded by EXPANDED_TEXT_LIMIT (24)", len(lines))
	}

	// upstream: packages/tui/test/grep-render.test.ts: "gives each file of a directory its own branch, labelled with its full path"
	twoFiles := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": ""}},
		Details: map[string]any{
			"matchCount":     2,
			"fileCount":      2,
			"displayContent": "# src/\n## a.ts#ABCD\n   *5│needle\n## b.ts#EF01\n *100│needle",
		},
	}
	v2 := h.renderToolView(rid, "g-2", "grep", "result", map[string]any{"pattern": "needle"}, &twoFiles, false, true, false, 120)
	lines2 := assertTerminalViewInvariants(t, "grep-2@120", v2, 120)
	joined2 := StripANSI(strings.Join(lines2, "\n"))
	if !strings.Contains(joined2, "├─ src/a.ts") || !strings.Contains(joined2, "└─ src/b.ts") {
		t.Errorf("expected per-file branches src/a.ts and src/b.ts:\n%s", joined2)
	}
	if strings.Contains(joined2, "more match") {
		t.Errorf("padded gutter caused false more-matches row:\n%s", joined2)
	}
}

// TestTaskEmptyResultsShowsErrorAndHeaderIcon verifies review finding #9.
func TestTaskEmptyResultsShowsErrorAndHeaderIcon(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)
	rid, _ := h.resolveRenderer("task")

	// Validation error from internal/agents: IsError=true with non-nil Details{results:[], totalDurationMs:0}.
	errRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": `Unknown agent "nope". Available: task, scout`}},
		Details: map[string]any{"results": []any{}, "totalDurationMs": 0},
	}
	v := h.renderToolView(rid, "t-err", "task", "result", map[string]any{"agent": "nope"}, &errRes, true, false, false, 80)
	lines := assertTerminalViewInvariants(t, "task-err@80", v, 80)
	joined := StripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, `Unknown agent "nope"`) {
		t.Errorf("task validation error message was dropped:\n%s", joined)
	}

	// Settled success shows ✔ (not ⇶), collapsed caps at 4 agents, ⟦done⟧ badge, A>B ids.
	var results []any
	for i := range 6 {
		results = append(results, map[string]any{
			"id":             fmt.Sprintf("Parent.Child%d", i+1),
			"agent":          "scout",
			"exitCode":       0,
			"durationMs":     250,
			"contextPercent": 12.5,
		})
	}
	okRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "done"}},
		Details: map[string]any{"results": results, "totalDurationMs": 1500},
	}
	vOk := h.renderToolView(rid, "t-ok", "task", "result", nil, &okRes, false, false, false, 100)
	okLines := assertTerminalViewInvariants(t, "task-ok@100", vOk, 100)
	okJoined := StripANSI(strings.Join(okLines, "\n"))
	if !strings.Contains(okLines[0], "✔") || !strings.Contains(okLines[0], "6 agents") {
		t.Errorf("settled task header should show ✔ and '6 agents': %q", StripANSI(okLines[0]))
	}
	if !strings.Contains(okJoined, "Parent>Child1") || !strings.Contains(okJoined, "⟦done⟧") || !strings.Contains(okJoined, "… 2 more agents") {
		t.Errorf("task result missing A>B id, ⟦done⟧ badge, or 4-agent collapsed cap:\n%s", okJoined)
	}

	// Partial/running shows ⇶ dispatch glyph.
	vRun := h.renderToolView(rid, "t-run", "task", "result", nil, &sdk.ToolRenderResult{
		Details: map[string]any{
			"results": []any{},
			"progress": []any{
				map[string]any{"id": "Worker1", "agent": "task", "status": "running", "currentTool": "grep"},
			},
		},
	}, false, false, true, 80)
	runLines := assertTerminalViewInvariants(t, "task-run@80", vRun, 80)
	if !strings.Contains(StripANSI(runLines[0]), "⇶") {
		t.Errorf("running task header should show ⇶ dispatch glyph: %q", StripANSI(runLines[0]))
	}
}

// TestAllToolsMatrixWidths20_80_200 sweeps every registered terminal renderer
// (both Call and Result, collapsed and expanded) at widths 20, 80, and 200
// with realistic Details payloads and hostile C1/emoji/long-line content.
func TestAllToolsMatrixWidths20_80_200(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)

	type toolCase struct {
		name    string
		args    map[string]any
		res     sdk.ToolRenderResult
		isError bool
		want    string // expected substring at width 80
	}

	cases := []toolCase{
		{
			name: "read",
			args: map[string]any{"path": "https://example.com/docs/intro"},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": "URL: https://example.com/docs/intro\n---\n\nHello web page\nSecond line"}},
				Details: map[string]any{
					"kind":        "url",
					"url":         "https://example.com/docs/intro",
					"finalUrl":    "https://example.com/docs/intro",
					"contentType": "text/markdown",
					"method":      "fetch",
					"truncated":   false,
					"notes":       []any{},
				},
			},
			want: "Content-Type:",
		},
		{
			name: "read",
			args: map[string]any{"path": "proc://"},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": ""}},
				Details: map[string]any{
					"jobs": []any{
						map[string]any{"id": "bg_1", "type": "bash", "status": "running", "label": "npm test", "durationMs": 3200},
					},
					"daemons": []any{
						map[string]any{"id": "srv1", "name": "srv1", "state": "ready", "pids": []any{4242}, "policy": map[string]any{"hold": "persist"}},
					},
				},
			},
			want: "srv1",
		},
		{
			name: "read",
			args: map[string]any{"path": "cfg://model"},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": "model.default: claude\nmodel.thinking: medium"}},
				Details: map[string]any{"path": "model", "count": 2, "modified": 1},
			},
			want: "2 settings",
		},
		{
			name: "write",
			args: map[string]any{"path": "agent://WorkerA", "content": "please check line 42\n" + strings.Repeat("x", 120)},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": "Delivered"}},
				Details: map[string]any{
					"to":       "WorkerA",
					"receipts": []any{map[string]any{"to": "WorkerA", "outcome": "injected"}},
				},
			},
			want: "IRC ➤ WorkerA",
		},
		{
			name: "find",
			args: map[string]any{"query": "session cleanup lifecycle"},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": ""}},
				Details: map[string]any{
					"query":     "session cleanup lifecycle",
					"coverage":  "complete",
					"elapsedMs": 420,
					"hits": []any{
						map[string]any{
							"rel": "internal/lifecycle/api.go", "contentScore": 0.92, "linesSeen": 67, "truncated": false,
							"ranges": []any{map[string]any{"start": 30, "end": 34, "p": 0.92, "snippet": "func OnEnd(name string, fn func(string))"}},
						},
					},
				},
			},
			want: "0.92",
		},
		{
			name: "ast_grep",
			args: map[string]any{"pat": "fmt.Println($A)", "path": "internal"},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": ""}},
				Details: map[string]any{
					"matchCount": 1, "fileCount": 1, "filesSearched": 12,
					"displayContent": "# internal/\n## main.go\n   *7│fmt.Println(hello)",
				},
			},
			want: "AST Grep",
		},
		{
			name: "ast_edit",
			args: map[string]any{"ops": []any{map[string]any{"pat": "old($X)", "rewrite": "new($X)"}}},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": ""}},
				Details: map[string]any{
					"totalReplacements": 2, "filesTouched": 1, "filesSearched": 5, "applied": false,
					"displayContent": "# src/\n## app.go (2 replacements)\n-10│old(a)\n+10│new(a)",
				},
			},
			want: "proposed",
		},
		{
			name: "ask",
			args: map[string]any{"html": "<form><input name='ok'></form>"},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": "ok: yes"}},
				Details: map[string]any{
					"answers":  []any{map[string]any{"name": "ok", "question": "Proceed?", "answer": "Yes"}},
					"timedOut": true,
				},
			},
			want: "Proceed?",
		},
		{
			name: "eval",
			args: map[string]any{"language": "py", "code": "print(1+1)"},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": "2"}},
				Details: map[string]any{
					"language": "python",
					"cells": []any{
						map[string]any{
							"language":   "python",
							"title":      "calc",
							"code":       "print(1+1)",
							"status":     "complete",
							"durationMs": 200,
							"output":     "2\n" + strings.Repeat("row\n", 15),
						},
					},
				},
			},
			want: "(200ms)",
		},
		{
			name: "web_search",
			args: map[string]any{"query": "golang 1.25 release"},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": "answer"}},
				Details: map[string]any{
					"response": map[string]any{
						"provider": "openai",
						"authMode": "api_key",
						"answer":   "Go 1.25 adds improvements.",
						"sources": []any{
							map[string]any{"title": "Go Release Notes", "url": "https://go.dev/doc/go1.25", "ageSeconds": 7200.0},
						},
					},
				},
			},
			want: "OpenAI API",
		},
		{
			name: "lsp",
			args: map[string]any{"action": "diagnostics", "file": "main.go"},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": "1 error(s), 1 warning(s)\nmain.go:10:5 [error] undefined: foo\nmain.go:12:1 [warning] unused var"}},
				Details: map[string]any{"action": "diagnostics"},
			},
			want: "1 error · 1 warning",
		},
		{
			name: "github",
			args: map[string]any{"op": "run_watch", "repo": "org/repo", "run": "999"},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": "failed"}},
				Details: map[string]any{
					"watch": map[string]any{
						"mode": "run", "state": "done", "repo": "org/repo",
						"run": map[string]any{
							"id": 999, "workflowName": "CI", "branch": "main",
							"jobs": []any{
								map[string]any{
									"id": 1, "name": "test", "conclusion": "failure", "durationSeconds": 42,
									"steps": []any{
										map[string]any{"number": 1, "name": "go test", "conclusion": "failure", "durationSeconds": 10},
									},
								},
							},
						},
						"failedLogs": []any{
							map[string]any{"runId": 999, "workflowName": "CI", "jobName": "test", "available": true, "tail": "FAIL TestFoo\nexit 1"},
						},
					},
				},
			},
			isError: true,
			want:    "failed logs",
		},
		{
			name: "debug",
			args: map[string]any{"action": "stack_trace"},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": "#0 main.go:42"}},
				Details: map[string]any{
					"action": "stack_trace",
					"snapshot": map[string]any{
						"id": "dbg-1", "adapter": "dlv", "status": "stopped", "cwd": "/work",
						"source": map[string]any{"path": "main.go"}, "line": 42,
					},
				},
			},
			want: "Location: main.go:42",
		},
		{
			name: "wait",
			args: map[string]any{},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": ""}},
				Details: map[string]any{
					"jobs": []any{
						map[string]any{"id": "bg_2", "type": "bash", "status": "completed", "label": "build", "durationMs": 500},
						map[string]any{"id": "bg_1", "type": "bash", "status": "failed", "label": "lint", "exitCode": 1, "errorText": "lint failed", "durationMs": 200},
					},
				},
			},
			want: "bg_1 failed",
		},
		{
			name: "resolve",
			args: map[string]any{"action": "apply", "reason": "Looks good"},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": "applied"}},
				Details: map[string]any{"action": "apply", "label": "ast_edit: rename foo", "reason": "Looks good"},
			},
			want: "Accept:",
		},
		{
			name: "goal",
			args: map[string]any{"op": "create", "objective": "Ship v1"},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": "created"}},
				Details: map[string]any{
					"op": "create",
					"goal": map[string]any{
						"id": "g1", "objective": "Ship v1", "status": "active",
						"tokensUsed": 1200, "tokenBudget": 10000, "timeUsedSeconds": 45.0,
					},
					"completionBudgetReport": "On track",
				},
			},
			want: "Goal: set",
		},
		{
			name: "yield",
			args: map[string]any{"data": map[string]any{"ok": true, "count": 3}},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": "Result submitted."}},
				Details: map[string]any{
					"data":             map[string]any{"ok": true, "count": 3},
					"schemaOverridden": true,
				},
			},
			want: "Submit Result",
		},
		{
			name: "checkpoint",
			args: map[string]any{"label": "pre-refactor", "scope": "all"},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": `{"id":"cp-1","files":3}`}},
			},
			want: "label=\"pre-refactor\", scope=\"all\"",
		},
		{
			name: "retain",
			args: map[string]any{"items": []any{map[string]any{"content": "Prefers tabs"}}},
			res: sdk.ToolRenderResult{
				Content: []map[string]any{{"type": "text", "text": "1 memory stored."}},
			},
			want: "Prefers tabs",
		},
		{
			name: "think",
			args: map[string]any{"thoughts": "Checking edge cases."},
			res:  sdk.ToolRenderResult{},
			want: "",
		},
	}

	for ci, tc := range cases {
		rid, use := h.resolveRenderer(tc.name)
		if use != "own" {
			t.Fatalf("case %d (%s): resolveRenderer use=%q, want own", ci, tc.name, use)
		}
		for _, w := range []int{20, 80, 200} {
			// Call phase
			vCall := h.renderToolView(rid, fmt.Sprintf("m-%d-c-%d", ci, w), tc.name, "call", tc.args, nil, false, false, true, w)
			assertTerminalViewInvariants(t, fmt.Sprintf("%s/call@%d", tc.name, w), vCall, w)

			// Result phase (collapsed & expanded)
			for _, exp := range []bool{false, true} {
				vRes := h.renderToolView(rid, fmt.Sprintf("m-%d-r-%d-%v", ci, w, exp), tc.name, "result", tc.args, &tc.res, tc.isError, exp, false, w)
				lines := assertTerminalViewInvariants(t, fmt.Sprintf("%s/res(exp=%v)@%d", tc.name, exp, w), vRes, w)
				if w == 80 && !exp && tc.want != "" {
					joined := StripANSI(strings.Join(lines, "\n"))
					if !strings.Contains(joined, tc.want) {
						t.Errorf("%s@80 missing %q in:\n%s", tc.name, tc.want, joined)
					}
				}
			}
		}
	}
}

// TestNoNewlineInAnyRow verifies N3: multi-line wait fallback, multi-line reflect,
// newline argument keys in default renderer, and multi-line grep missingPaths all
// split cleanly into single-line kit.Lines rows (zero embedded \r or \n).
func TestNoNewlineInAnyRow(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)

	waitRID, _ := h.resolveRenderer("wait")
	vWait := h.renderToolView(waitRID, "n3-wait", "wait", "result", nil, &sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "No background jobs.\nNothing to wait for.\nthird line"}},
	}, false, false, false, 80)
	waitLines := assertTerminalViewInvariants(t, "n3-wait", vWait, 80)
	if len(waitLines) < 4 {
		t.Errorf("multi-line wait fallback should split into >=4 rows, got %d: %#v", len(waitLines), waitLines)
	}

	reflRID, _ := h.resolveRenderer("reflect")
	vRefl := h.renderToolView(reflRID, "n3-reflect", "reflect", "result",
		map[string]any{"query": "multi\nline"},
		&sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "line one\nline two\r\nline three"}}},
		false, false, false, 80)
	assertTerminalViewInvariants(t, "n3-reflect", vRefl, 80)

	defRID, _ := h.resolveRenderer("checkpoint")
	vDef := h.renderToolView(defRID, "n3-def", "checkpoint", "result",
		map[string]any{"kevil\nkey": "val\n2"},
		&sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "ok"}}},
		false, true, false, 80)
	assertTerminalViewInvariants(t, "n3-def", vDef, 80)

	grepRID, _ := h.resolveRenderer("grep")
	vGrep := h.renderToolView(grepRID, "n3-grep", "grep", "result",
		map[string]any{"pattern": "x"},
		&sdk.ToolRenderResult{
			Content: []map[string]any{{"type": "text", "text": ""}},
			Details: map[string]any{
				"matchCount":   0,
				"fileCount":    0,
				"missingPaths": []any{"dir/a\nb.go"},
			},
		}, false, false, false, 80)
	assertTerminalViewInvariants(t, "n3-grep", vGrep, 80)
}

// TestBashBackgroundNoticeAndSpillURL verifies N4:
// 1. Unbracketed `Backgrounded as job bg_1 (no deadline); ... Do NOT poll ...` is stripped.
// 2. Bare numeric artifactId formats as `local://<id>.log` with omp's exact reference phrasing.
func TestBashBackgroundNoticeAndSpillURL(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)
	rid, _ := h.resolveRenderer("bash")

	// 1. Background notice (exact format emitted by internal/shell/output.go:1065 and tools/bash.ts:69-83).
	bgNotice := "Backgrounded as job bg_1 (killed once it has run 300s in total; `timeout: 0` disables the deadline); its output is injected into the conversation as a follow-up the moment it finishes. Do NOT poll for it (no `sleep`, `ps`, `pgrep`, `top`, `pidwait`, log tailing): every poll is a wasted turn. Do other work, or end your reply and wait to be woken."
	bgRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "starting server...\n\n" + bgNotice}},
		Details: map[string]any{
			"async": map[string]any{"state": "running", "jobId": "bg_1"},
		},
	}
	vBg := h.renderToolView(rid, "n4-bg", "bash", "result", map[string]any{"command": "serve"}, &bgRes, false, false, false, 100)
	bgLines := assertTerminalViewInvariants(t, "n4-bg", vBg, 100)
	bgJoined := StripANSI(strings.Join(bgLines, "\n"))
	if strings.Contains(bgJoined, "Do NOT poll") || strings.Contains(bgJoined, "Backgrounded as job") {
		t.Errorf("background notice was not stripped:\n%s", bgJoined)
	}
	if !strings.Contains(bgJoined, "starting server...") || !strings.Contains(bgJoined, "bg_1") {
		t.Errorf("expected command output and bg_1 footer badge:\n%s", bgJoined)
	}

	// 2. Truncated output with bare numeric artifactId ("7") -> `Read local://7.log for full output` (never artifact://7).
	truncRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "line1\nline2"}},
		Details: map[string]any{
			"wallTimeMs": 50.0,
			"meta": map[string]any{
				"truncation": map[string]any{
					"direction":   "tail",
					"totalLines":  100000,
					"outputLines": 3000,
					"artifactId":  "7",
				},
			},
		},
	}
	vTrunc := h.renderToolView(rid, "n4-trunc", "bash", "result", map[string]any{"command": "seq 100000"}, &truncRes, false, false, false, 120)
	truncLines := assertTerminalViewInvariants(t, "n4-trunc", vTrunc, 120)
	truncJoined := StripANSI(strings.Join(truncLines, "\n"))
	if strings.Contains(truncJoined, "artifact://") {
		t.Errorf("spill URL must not use artifact://:\n%s", truncJoined)
	}
	if !strings.Contains(truncJoined, "Read local://7.log for full output") {
		t.Errorf("expected omp spill reference 'Read local://7.log for full output':\n%s", truncJoined)
	}

	// Elided-middle sample phrasing (packages/tui/src/tools/output-meta.ts:176).
	if got := formatFullOutputReference("9", 2048); got != "Read local://9.log for a head/tail sample of the output; 2.0KB from its middle was not saved" {
		t.Errorf("formatFullOutputReference(9, 2048) = %q", got)
	}
	// GB branch in formatBytes (packages/utils/src/format.ts:66).
	if got := formatBytes(2 * 1024 * 1024 * 1024); got != "2.0GB" {
		t.Errorf("formatBytes(2GiB) = %q, want 2.0GB", got)
	}
}

// TestEditHeaderAndTodoGlyphsMatchUpstream verifies N5:
//  1. Edit header never appends `:N` suffix and middle-truncates long paths keeping stats visible.
//  2. Todo uses ☐ (not ⟳/⦸) for in_progress and abandoned, with two-space `closed/total` progress
//     counting abandoned tasks, and folded phases without checkbox prefixes.
func TestEditHeaderAndTodoGlyphsMatchUpstream(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)

	editRID, _ := h.resolveRenderer("edit")
	editRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Edited"}},
		Details: map[string]any{
			"path":             "notes.txt",
			"firstChangedLine": 2,
			"diff":             " 1|a\n-2|old\n+2|new\n",
		},
	}
	vEdit := h.renderToolView(editRID, "n5-edit", "edit", "result",
		map[string]any{"path": "notes.txt"}, &editRes, false, false, false, 80)
	editLines := assertTerminalViewInvariants(t, "n5-edit", vEdit, 80)
	top := StripANSI(editLines[0])
	if strings.Contains(top, "notes.txt:2") {
		t.Errorf("edit header must not append :firstChangedLine suffix: %q", top)
	}
	if !strings.Contains(top, "notes.txt") || !strings.Contains(top, "⟦+1/-1⟧") {
		t.Errorf("edit header missing path or stats: %q", top)
	}

	// Long path middle-truncates so ⟦+1/-1⟧ stats stay visible in the top border.
	longPath := "very/deeply/nested/directory/structure/with/many/segments/component_handler_impl.go"
	longEditRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Edited"}},
		Details: map[string]any{"path": longPath, "diff": "-1|a\n+1|b\n"},
	}
	vLong := h.renderToolView(editRID, "n5-edit-long", "edit", "result",
		map[string]any{"path": longPath}, &longEditRes, false, false, false, 60)
	longLines := assertTerminalViewInvariants(t, "n5-edit-long", vLong, 60)
	longTop := StripANSI(longLines[0])
	if !strings.Contains(longTop, "⟦+1/-1⟧") {
		t.Errorf("long edit header lost stats instead of middle-truncating path: %q", longTop)
	}

	// Todo glyphs, phase progress (`  closed/total`), folded summary (no checkbox), and init touching all phases.
	todoRID, _ := h.resolveRenderer("todo")
	todoRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": ""}},
		Details: map[string]any{
			"phases": []any{
				map[string]any{
					"name": "Build",
					"tasks": []any{
						map[string]any{"content": "done task", "status": "completed"},
						map[string]any{"content": "dropped task", "status": "abandoned"},
						map[string]any{"content": "active task", "status": "in_progress"},
						map[string]any{"content": "later task", "status": "pending"},
					},
				},
				map[string]any{
					"name": "Verify",
					"tasks": []any{
						map[string]any{"content": "check tests", "status": "pending"},
					},
				},
			},
		},
	}
	vTodo := h.renderToolView(todoRID, "n5-todo", "todo", "result",
		map[string]any{"op": "update", "phase": "Build"}, &todoRes, false, false, false, 90)
	todoLines := assertTerminalViewInvariants(t, "n5-todo", vTodo, 90)
	todoJoined := StripANSI(strings.Join(todoLines, "\n"))
	if strings.Contains(todoJoined, "⟳") || strings.Contains(todoJoined, "⦸") {
		t.Errorf("todo must use ☐ for in_progress and abandoned, not ⟳/⦸:\n%s", todoJoined)
	}
	if !strings.Contains(todoJoined, "☐ active task") || !strings.Contains(todoJoined, "☐ dropped task") {
		t.Errorf("todo missing ☐ glyphs for in_progress/abandoned:\n%s", todoJoined)
	}
	if !strings.Contains(todoJoined, "I. Build  2/4") {
		t.Errorf("todo phase progress should be two spaces + closed/total (2/4 counting abandoned):\n%s", todoJoined)
	}
	if !strings.Contains(todoJoined, "│ II. Verify  0/1") {
		t.Errorf("folded phase summary should have no checkbox prefix:\n%s", todoJoined)
	}
}

// TestTaskStatsAndCollapsedOutput verifies N6:
// reads `requests`, `contextTokens`, `contextWindow`, `usage.cost.total`, omits
// `⟦task⟧` badge for default agent, keeps supplemental progress rows, and shows
// 3-line Output section even when collapsed (12 when expanded).
func TestTaskStatsAndCollapsedOutput(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)
	rid, _ := h.resolveRenderer("task")

	res := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "done"}},
		Details: map[string]any{
			"totalDurationMs": 4200,
			"results": []any{
				map[string]any{
					"index":         0,
					"id":            "Anna.Bob",
					"agent":         "explore",
					"exitCode":      0,
					"durationMs":    4200,
					"requests":      3,
					"contextTokens": 24000,
					"contextWindow": 200000,
					"usage":         map[string]any{"cost": map[string]any{"total": 0.12}},
					"output":        "line 1\nline 2\nline 3\nline 4\nline 5",
				},
				map[string]any{
					"index":      1,
					"id":         "DefaultWorker",
					"agent":      "task",
					"exitCode":   0,
					"durationMs": 1000,
				},
			},
			"progress": []any{
				map[string]any{
					"index":             2,
					"id":                "AsyncRunner",
					"agent":             "task",
					"status":            "running",
					"requests":          2,
					"contextTokens":     10000,
					"contextWindow":     100000,
					"currentTool":       "grep",
					"currentToolIntent": "Searching routes",
					"durationMs":        1500,
				},
			},
		},
	}

	vCollapsed := h.renderToolView(rid, "n6-col", "task", "result", nil, &res, false, false, false, 120)
	colLines := assertTerminalViewInvariants(t, "n6-col", vCollapsed, 120)
	colJoined := StripANSI(strings.Join(colLines, "\n"))

	for _, want := range []string{
		"Anna>Bob",
		"⟦explore⟧",
		"3 req",
		"12.0%/200K",
		"$0.12",
		"4.2s",
		"Output",
		"line 1",
		"line 3",
		"… 2 more lines",
		"AsyncRunner",
		"└ grep: Searching routes",
	} {
		if !strings.Contains(colJoined, want) {
			t.Errorf("collapsed task result missing %q in:\n%s", want, colJoined)
		}
	}
	if strings.Contains(colJoined, "⟦task⟧") {
		t.Errorf("default agent 'task' must not show ⟦task⟧ badge:\n%s", colJoined)
	}
	if strings.Contains(colJoined, "line 4") {
		t.Errorf("collapsed task output should cap at 3 lines, found line 4:\n%s", colJoined)
	}
	// Duration ascending order: DefaultWorker (1.0s) before Anna>Bob (4.2s).
	if strings.Index(colJoined, "DefaultWorker") > strings.Index(colJoined, "Anna>Bob") {
		t.Errorf("results should be ordered by durationMs ascending:\n%s", colJoined)
	}

	vExpanded := h.renderToolView(rid, "n6-exp", "task", "result", nil, &res, false, true, false, 120)
	expLines := assertTerminalViewInvariants(t, "n6-exp", vExpanded, 120)
	expJoined := StripANSI(strings.Join(expLines, "\n"))
	if !strings.Contains(expJoined, "line 5") {
		t.Errorf("expanded task output should show line 5:\n%s", expJoined)
	}
}

// TestFinding17BuildersParity checks parity fixes from the line-by-line audit of
// web_search, lsp, github, wait, resolve, goal, yield, and default renderers.
func TestFinding17BuildersParity(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)

	// 1. web_search without details.response renders omp's `⚠ Response` tree fallback.
	wsRID, _ := h.resolveRenderer("web_search")
	vWS := h.renderToolView(wsRID, "f17-ws", "web_search", "result", nil, &sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "raw line 1\nraw line 2"}},
	}, false, false, false, 80)
	wsJoined := StripANSI(strings.Join(assertTerminalViewInvariants(t, "f17-ws", vWS, 80), "\n"))
	if !strings.Contains(wsJoined, "⚠ Response") || !strings.Contains(wsJoined, "raw line 1") {
		t.Errorf("web_search fallback mismatch:\n%s", wsJoined)
	}

	// 2. lsp request status puts target in description (`references main.go:10 (Foo)`),
	//    diagnostics collapse at 3 items, symbols collapse at 3 with `line N`.
	lspRID, _ := h.resolveRenderer("lsp")
	vLspCall := h.renderToolView(lspRID, "f17-lsp-call", "lsp", "call",
		map[string]any{"action": "references", "file": "main.go", "line": 10, "symbol": "Foo"},
		nil, false, false, true, 100)
	lspCallText := StripANSI(strings.Join(assertTerminalViewInvariants(t, "f17-lsp-call", vLspCall, 100), "\n"))
	if !strings.Contains(lspCallText, "LSP: references main.go:10 (Foo)") {
		t.Errorf("lsp call header = %q, want 'LSP: references main.go:10 (Foo)'", lspCallText)
	}

	// 3. github op title table + one-line unframed success.
	ghRID, _ := h.resolveRenderer("github")
	vGh := h.renderToolView(ghRID, "f17-gh", "github", "result",
		map[string]any{"op": "pr_checkout", "pr": "42", "repo": "org/repo"},
		&sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "Checked out PR #42"}}},
		false, false, false, 100)
	ghLines := assertTerminalViewInvariants(t, "f17-gh", vGh, 100)
	ghJoined := StripANSI(strings.Join(ghLines, "\n"))
	if !strings.Contains(ghJoined, "GitHub PR Checkout") || !strings.Contains(ghJoined, "#42") {
		t.Errorf("github op title/meta mismatch:\n%s", ghJoined)
	}
	if strings.Contains(ghJoined, "╭───") {
		t.Errorf("trivial one-line github success should render unframed:\n%s", ghJoined)
	}

	// 4. wait compact view omits header when settled jobs exist; renders standalone agents roster.
	waitRID, _ := h.resolveRenderer("wait")
	vWait := h.renderToolView(waitRID, "f17-wait", "wait", "result", nil, &sdk.ToolRenderResult{
		Details: map[string]any{
			"jobs": []any{
				map[string]any{"id": "bg_1", "type": "bash", "status": "completed", "label": "ok", "durationMs": 100},
			},
			"agents": []any{
				map[string]any{"id": "Scout1", "live": true, "activity": "reading", "ageMs": 5000, "parentId": "Main"},
			},
		},
	}, false, false, false, 100)
	waitJoined := StripANSI(strings.Join(assertTerminalViewInvariants(t, "f17-wait", vWait, 100), "\n"))
	if strings.Contains(waitJoined, "1 job settled") {
		t.Errorf("collapsed compact wait with settled jobs should omit header:\n%s", waitJoined)
	}
	if !strings.Contains(waitJoined, "Scout1") || !strings.Contains(waitJoined, "← Main") {
		t.Errorf("wait missing standalone agents roster:\n%s", waitJoined)
	}

	// 5. default renderer skips JSON tree for 16+ digit unsafe integer IDs.
	defRID, _ := h.resolveRenderer("checkpoint")
	vDef := h.renderToolView(defRID, "f17-def", "checkpoint", "result", nil, &sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": `{"id":9007199254740993}`}},
	}, false, true, false, 100)
	defJoined := StripANSI(strings.Join(assertTerminalViewInvariants(t, "f17-def", vDef, 100), "\n"))
	if !strings.Contains(defJoined, "9007199254740993") {
		t.Errorf("unsafe 64-bit integer was rounded instead of falling back to raw text:\n%s", defJoined)
	}
}

// TestPass3M1SanitizeContentAllTools verifies M1: hostile Content carrying
// OSC 0, OSC 8, C1 CSI, DEL, and NUL is sanitized at the root for every
// registered terminal renderer (including retain).
func TestPass3M1SanitizeContentAllTools(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)
	hostile := "# src/\n## a.go \x1b]0;title\x07 \x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\ \u009b31mC1 \x7f \x00ok"
	tools := []string{
		"retain", "recall", "reflect", "read", "write", "edit", "bash",
		"grep", "glob", "find", "ast_grep", "ast_edit", "todo", "ask",
		"task", "eval", "web_search", "lsp", "github", "debug", "wait",
		"resolve", "reject", "propose", "report_issue", "think", "goal",
		"yield", "checkpoint", "rewind", "browser", "fetch",
	}
	for _, name := range tools {
		rid, use := h.resolveRenderer(name)
		if use != "own" {
			t.Fatalf("resolveRenderer(%s) use=%q", name, use)
		}
		res := sdk.ToolRenderResult{
			Content: []map[string]any{{"type": "text", "text": hostile}},
		}
		v := h.renderToolView(rid, "m1-"+name, name, "result", map[string]any{"items": []any{map[string]any{"content": "mem"}}}, &res, false, false, false, 80)
		assertTerminalViewInvariants(t, "m1-"+name, v, 80)
	}
}

// TestPass3M3WaitAndM4DefaultYieldParity verifies pass-3 M3 (wait) and M4
// (default/yield/json-tree) fixes against omp v19.1.1.
func TestPass3M3WaitAndM4DefaultYieldParity(t *testing.T) {
	h := startAnsiTestHost(t, "", nil)

	// M3(1): interrupted wait renders one info row `ⓘ Wait · interrupted by message`.
	waitRID, _ := h.resolveRenderer("wait")
	vInter := h.renderToolView(waitRID, "m3-inter", "wait", "result", nil, &sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Wait interrupted by message."}},
		Details: map[string]any{"interrupted": true},
	}, false, false, false, 80)
	interLines := assertTerminalViewInvariants(t, "m3-inter", vInter, 80)
	if len(interLines) != 1 || !strings.Contains(StripANSI(interLines[0]), "Wait") || !strings.Contains(StripANSI(interLines[0]), "interrupted by message") {
		t.Errorf("interrupted wait = %#v, want single info row", interLines)
	}

	// M3(2,3,4,7,8): expanded job row preserves duration on head row, moves long
	// label to continuation rows, prints `(no label)` when empty, trims blank
	// preview lines, cuts intent at `: `, suppresses duplicate root artifactError,
	// and budgets agent roster ids.
	vWaitExp := h.renderToolView(waitRID, "m3-exp", "wait", "result", nil, &sdk.ToolRenderResult{
		Details: map[string]any{
			"meta": map[string]any{"artifactError": "write"},
			"jobs": []any{
				map[string]any{
					"id":         "job-1",
					"type":       "bash",
					"status":     "running",
					"label":      "run the full integration suite across all workspaces and packages now",
					"durationMs": 2000,
					"resultText": "\n\n  first real preview line\nsecond line",
					"meta":       map[string]any{"artifactError": "write"},
				},
				map[string]any{
					"id":         "job-2",
					"type":       "bash",
					"status":     "completed",
					"intent":     "Running tests: pkg/foo",
					"label":      "go test ./...",
					"durationMs": 500,
				},
				map[string]any{
					"id":         "job-3",
					"type":       "bash",
					"status":     "completed",
					"label":      "",
					"durationMs": 100,
				},
			},
			"agents": []any{
				map[string]any{"id": strings.Repeat("LongAgent", 10), "live": true, "ageMs": 1000},
			},
		},
	}, false, true, false, 60)
	expWaitLines := assertTerminalViewInvariants(t, "m3-exp", vWaitExp, 60)
	expWaitJoined := StripANSI(strings.Join(expWaitLines, "\n"))
	if !strings.Contains(expWaitJoined, "2.0s") {
		t.Errorf("expanded wait head row lost duration 2.0s:\n%s", expWaitJoined)
	}
	if !strings.Contains(expWaitJoined, "first real preview line") {
		t.Errorf("wait preview with leading blank lines was dropped:\n%s", expWaitJoined)
	}
	if !strings.Contains(expWaitJoined, "Running tests") || strings.Contains(expWaitJoined, "pkg/foo") {
		t.Errorf("wait job label should use intent prefix before ': ':\n%s", expWaitJoined)
	}
	if !strings.Contains(expWaitJoined, "(no label)") {
		t.Errorf("empty job label should render (no label):\n%s", expWaitJoined)
	}
	if strings.Count(expWaitJoined, "Full output was not saved completely") != 1 {
		t.Errorf("duplicate root artifactError should be suppressed when job owns it:\n%s", expWaitJoined)
	}

	// M3(5,6): collapsed multi-line label takes first line only; no-jobs fallback wraps.
	vWaitCol := h.renderToolView(waitRID, "m3-col", "wait", "result", nil, &sdk.ToolRenderResult{
		Details: map[string]any{
			"jobs": []any{
				map[string]any{"id": "j1", "type": "bash", "status": "completed", "label": "first line\nsecond line", "durationMs": 100},
			},
		},
	}, false, false, false, 80)
	colWaitJoined := StripANSI(strings.Join(assertTerminalViewInvariants(t, "m3-col", vWaitCol, 80), "\n"))
	if !strings.Contains(colWaitJoined, "first line") || strings.Contains(colWaitJoined, "second line") {
		t.Errorf("collapsed wait should take first line of multi-line label only:\n%s", colWaitJoined)
	}

	vWaitWrap := h.renderToolView(waitRID, "m3-wrap", "wait", "result", nil, &sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "No background jobs are currently running in this session right now."}},
	}, false, false, false, 30)
	wrapLines := assertTerminalViewInvariants(t, "m3-wrap", vWaitWrap, 30)
	if !strings.Contains(StripANSI(strings.Join(wrapLines, "\n")), "right now.") {
		t.Errorf("no-jobs fallback clipped instead of wrapping:\n%#v", wrapLines)
	}

	// M4(1,3,4,5,6): hooked JSON tree + inline containers, capitalized Checkpoint title,
	// expanded wrapping, benign interrupt_skipped, whitespace-only output, yield data: null.
	cpRID, _ := h.resolveRenderer("checkpoint")
	vCp := h.renderToolView(cpRID, "m4-cp", "checkpoint", "result",
		map[string]any{
			"goal": "x",
			"n":    3,
			"list": []any{1, 2},
			"obj":  map[string]any{"a": 1},
		},
		&sdk.ToolRenderResult{
			Content: []map[string]any{{"type": "text", "text": "prefix " + strings.Repeat("word ", 20) + "TAILMARKER"}},
		}, false, true, false, 50)
	cpLines := assertTerminalViewInvariants(t, "m4-cp", vCp, 50)
	cpJoined := StripANSI(strings.Join(cpLines, "\n"))
	for _, want := range []string{
		"✔ Checkpoint",
		"└ • goal: \"x\"",
		"• list: [1, 2]",
		"• obj: {a: 1}",
		"TAILMARKER",
	} {
		if !strings.Contains(cpJoined, want) {
			t.Errorf("checkpoint expanded card missing %q:\n%s", want, cpJoined)
		}
	}

	// Benign interrupt_skipped renders neutral info icon (`ⓘ`) even with isError=true.
	vSkip := h.renderToolView(cpRID, "m4-skip", "checkpoint", "result", map[string]any{}, &sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "   \t  "}},
		Details: map[string]any{"source": "interrupt_skipped", "__synthetic": true},
	}, true, false, false, 60)
	skipJoined := StripANSI(strings.Join(assertTerminalViewInvariants(t, "m4-skip", vSkip, 60), "\n"))
	if !strings.Contains(skipJoined, "ⓘ") || !strings.Contains(skipJoined, "(no output)") {
		t.Errorf("benign skip + whitespace-only output mismatch:\n%s", skipJoined)
	}

	// Yield with explicit data: null renders `null` (not absent-data note).
	yieldRID, _ := h.resolveRenderer("yield")
	vYieldNull := h.renderToolView(yieldRID, "m4-yield-null", "yield", "result",
		map[string]any{"data": nil},
		&sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "ok"}}},
		false, false, false, 80)
	ynJoined := StripANSI(strings.Join(assertTerminalViewInvariants(t, "m4-yield-null", vYieldNull, 80), "\n"))
	if !strings.Contains(ynJoined, "null") || strings.Contains(ynJoined, "Ends with the sections") {
		t.Errorf("yield with explicit data: null should render null, not absent-data note:\n%s", ynJoined)
	}
}
