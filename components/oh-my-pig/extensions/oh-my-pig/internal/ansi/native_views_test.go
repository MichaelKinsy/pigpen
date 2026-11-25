package ansi

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

// ── Native tree test helpers ─────────────────────────────────────────────────

type nvFlatNode struct {
	id   string
	kind string
	node kit.Node
	text string
}

func nvWalkTree(t *testing.T, root kit.Node) ([]nvFlatNode, map[string]nvFlatNode) {
	t.Helper()
	var list []nvFlatNode
	byID := map[string]nvFlatNode{}

	var visit func(n kit.Node)
	visit = func(n kit.Node) {
		if n == nil {
			return
		}
		var id, kind, txt string
		var children []kit.Node
		switch v := n.(type) {
		case *kit.Container:
			id, kind = v.ID, "container"
			children = v.Children
		case *kit.Box:
			id, kind = v.ID, "box"
			children = v.Children
		case *kit.VStack:
			id, kind = v.ID, "vstack"
			for _, sc := range v.Children {
				switch e := sc.(type) {
				case kit.StackEntry:
					children = append(children, e.Node)
				case kit.Node:
					children = append(children, e)
				}
			}
		case *kit.HStack:
			id, kind = v.ID, "hstack"
			for _, sc := range v.Children {
				switch e := sc.(type) {
				case kit.StackEntry:
					children = append(children, e.Node)
				case kit.Node:
					children = append(children, e)
				}
			}
		case *kit.Text:
			id, kind, txt = v.ID, "text", v.Text
		case *kit.TruncatedText:
			id, kind, txt = v.ID, "truncated_text", v.Text
		case *kit.Markdown:
			id, kind, txt = v.ID, "markdown", v.Text
		case *kit.Lines:
			id, kind, txt = v.ID, "lines", strings.Join(v.Lines, "\n")
			if v.List != nil && len(v.List.Items) != len(v.Lines) {
				t.Errorf("node %q: kit.Lines has %d lines but List has %d items (host requires 1:1)", id, len(v.Lines), len(v.List.Items))
			}
		case *kit.Diff:
			id, kind, txt = v.ID, "diff", v.Diff
		case *kit.Image:
			id, kind = v.ID, "image"
		default:
			kind = fmt.Sprintf("%T", n)
		}

		// Assert no duplicate non-empty IDs and never double `#` suffixes.
		if id != "" {
			if _, dup := byID[id]; dup {
				t.Errorf("duplicate node ID %q in native view", id)
			}
			if strings.Count(id, "#") > 1 {
				t.Errorf("node ID %q has more than one '#' suffix (violates VIEW-IDS.md)", id)
			}
		}
		// Assert no ANSI escapes, C1 controls, or terminal box-drawing frame characters.
		if strings.ContainsRune(txt, '\x1b') {
			t.Errorf("node %q (%s) contains ANSI escape: %q", id, kind, txt)
		}
		for _, r := range txt {
			if (r >= 0x00 && r < 0x20 && r != '\t' && r != '\n') || (r >= 0x7f && r <= 0x9f) {
				t.Errorf("node %q (%s) contains unsanitized control U+%04X", id, kind, r)
				break
			}
		}
		for _, frameCh := range []string{"╭", "╮", "╰", "╯"} {
			if strings.Contains(txt, frameCh) {
				t.Errorf("node %q (%s) contains terminal frame chrome %q in %q", id, kind, frameCh, txt)
			}
		}
		if id == "omp.tool.head" || strings.HasPrefix(id, "omp.tool.frame.") {
			t.Errorf("native view must not emit terminal chrome node ID %q", id)
		}

		entry := nvFlatNode{id: id, kind: kind, node: n, text: txt}
		list = append(list, entry)
		if id != "" {
			byID[id] = entry
		}
		for _, ch := range children {
			visit(ch)
		}
	}
	visit(root)

	if len(list) >= 4096 {
		t.Errorf("native view has %d nodes (>= 4096 host cap)", len(list))
	}
	return list, byID
}

func nvMustCall(t *testing.T, tool string, args map[string]any, rc sdk.ToolRenderContext) ([]nvFlatNode, map[string]nvFlatNode) {
	t.Helper()
	b, ok := lookupNative(tool)
	if !ok {
		t.Fatalf("native builder not registered for %q", tool)
	}
	if b.Call == nil {
		t.Fatalf("native builder Call is nil for %q", tool)
	}
	node, err := b.Call(sdk.Context{}, args, rc, 100)
	if err != nil {
		t.Fatalf("%s Call error: %v", tool, err)
	}
	return nvWalkTree(t, node)
}

func nvMustResult(t *testing.T, tool string, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext) ([]nvFlatNode, map[string]nvFlatNode) {
	t.Helper()
	b, ok := lookupNative(tool)
	if !ok {
		t.Fatalf("native builder not registered for %q", tool)
	}
	if b.Result == nil {
		t.Fatalf("native builder Result is nil for %q", tool)
	}
	node, err := b.Result(sdk.Context{}, res, opts, rc, 100)
	if err != nil {
		t.Fatalf("%s Result error: %v", tool, err)
	}
	return nvWalkTree(t, node)
}

func nvAssertNode(t *testing.T, byID map[string]nvFlatNode, id, wantKind string, wantContains ...string) nvFlatNode {
	t.Helper()
	n, ok := byID[id]
	if !ok {
		ids := make([]string, 0, len(byID))
		for k := range byID {
			ids = append(ids, k)
		}
		t.Fatalf("missing node ID %q (present: %v)", id, ids)
	}
	if wantKind != "" && n.kind != wantKind {
		t.Errorf("node %q kind = %q, want %q", id, n.kind, wantKind)
	}
	for _, sub := range wantContains {
		if !strings.Contains(n.text, sub) {
			t.Errorf("node %q text %q does not contain %q", id, n.text, sub)
		}
	}
	return n
}

// ── Tests ────────────────────────────────────────────────────────────────────

// upstream: packages/tui/src/tools/read.ts (describeCall, describeResult)
func TestNativeRead(t *testing.T) {
	// CallView
	_, callIDs := nvMustCall(t, "read", map[string]any{"path": "src/main.go"}, sdk.ToolRenderContext{})
	nvAssertNode(t, callIDs, "omp.tool.read.call", "container")

	// Code read with internal/files/read.go Details shape (displayContent + meta.truncation).
	res := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "[src/main.go#A1B2]\n1:package main\n2:func main() {}\n\n[Showing lines 1-2 of 50. Use src/main.go:3- to continue]"}},
		Details: map[string]any{
			"fileSize":     1200,
			"totalLines":   50,
			"rowPrefixes":  true,
			"resolvedPath": "/tmp/src/main.go",
			"displayContent": map[string]any{
				"text":      "package main\nfunc main() {}",
				"startLine": 1,
			},
			"meta": map[string]any{
				"source": map[string]any{"type": "path", "value": "src/main.go"},
				"truncation": map[string]any{
					"direction":   "head",
					"truncatedBy": "lines",
					"totalLines":  50,
					"outputLines": 2,
					"shownRange":  map[string]any{"start": 1, "end": 2},
					"nextOffset":  3,
				},
			},
		},
	}
	// Default collapsed filesystem read (read.toolResultPreview=false):
	// matches omp Tern capture (`tool[omp.tool.read]` with NO body children).
	collapsedNodes, collapsedIDs := nvMustResult(t, "read", res, sdk.ToolRenderResultOptions{Expanded: false}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "src/main.go:1-2"},
	})
	nvAssertNode(t, collapsedIDs, "omp.tool.read", "container")
	if len(collapsedNodes) != 1 {
		t.Errorf("collapsed filesystem read must have 0 body children (matching omp's bare inline row), got %d nodes", len(collapsedNodes))
	}

	// Expanded filesystem read with default read.toolResultPreview=false STILL
	// stays body-less, matching omp's ReadToolGroupComponent.#shouldRenderPreview
	// (chat/read-tool-group.ts:1446-1448).
	expandedNoPreview, _ := nvMustResult(t, "read", res, sdk.ToolRenderResultOptions{Expanded: true}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "src/main.go:1-2"},
	})
	if len(expandedNoPreview) != 1 {
		t.Errorf("expanded filesystem read with read.toolResultPreview=false must have 0 body children, got %d nodes", len(expandedNoPreview))
	}

	// With read.toolResultPreview=true, filesystem read carries raw code + footnote.
	origSetting := Setting
	Setting = func(_ sdk.Context, key string) any {
		if key == "read.toolResultPreview" {
			return true
		}
		return nil
	}
	_, ids := nvMustResult(t, "read", res, sdk.ToolRenderResultOptions{Expanded: true}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "src/main.go:1-2"},
	})
	nvAssertNode(t, ids, "omp.tool.read", "container")
	codeNode := nvAssertNode(t, ids, "omp.tool.read.code", "lines", "package main", "func main() {}")
	if strings.Contains(codeNode.text, "1:") {
		t.Errorf("read.code should carry raw displayContent without hashline prefix: %q", codeNode.text)
	}
	nvAssertNode(t, ids, "omp.tool.notice", "text", "resolved /tmp/src/main.go", "Showing lines 1-2 of 50")

	// Markdown read: bare inline row when read.toolResultPreview=false; rendered markdown when true.
	mdRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "# Title\n\nBody text"}},
		Details: map[string]any{
			"contentType":    "text/markdown",
			"displayContent": map[string]any{"text": "# Title\n\nBody text", "startLine": 1},
		},
	}
	_, mdIDs := nvMustResult(t, "read", mdRes, sdk.ToolRenderResultOptions{Expanded: true}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "README.md"},
	})
	nvAssertNode(t, mdIDs, "omp.tool.read.markdown", "markdown", "# Title")
	Setting = origSetting
	mdCollapsed, _ := nvMustResult(t, "read", mdRes, sdk.ToolRenderResultOptions{Expanded: false}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "README.md"},
	})
	if len(mdCollapsed) != 1 {
		t.Errorf("collapsed markdown filesystem read must have 0 body children, got %d", len(mdCollapsed))
	}
	pngBytes := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
	}
	imgRes := sdk.ToolRenderResult{
		Content: []map[string]any{
			{"type": "text", "text": "Read image file [image/png]"},
			{"type": "image", "data": base64.StdEncoding.EncodeToString(pngBytes), "mimeType": "image/png"},
		},
		Details: map[string]any{"fileSize": len(pngBytes)},
	}
	_, imgIDs := nvMustResult(t, "read", imgRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "diagram.png"},
	})
	imgEntry := nvAssertNode(t, imgIDs, "omp.tool.read.image", "image")
	imgNode := imgEntry.node.(*kit.Image)
	if imgNode.MimeType != "image/png" || imgNode.Filename != "diagram.png" || len(imgNode.Data()) != len(pngBytes) {
		t.Errorf("unexpected image node fields: mime=%q filename=%q len=%d", imgNode.MimeType, imgNode.Filename, len(imgNode.Data()))
	}

	// Short single-line error (<= 120 chars) emits NO body (head carries `— <gist>`),
	// matching omp Tern capture (`tool[omp.tool.read]` with 0 children).
	shortErrRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Error: Path 'does-not-exist-proof.txt' not found"}},
	}
	shortErrNodes, shortErrIDs := nvMustResult(t, "read", shortErrRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args:    map[string]any{"path": "does-not-exist-proof.txt"},
		IsError: true,
	})
	nvAssertNode(t, shortErrIDs, "omp.tool.read", "container")
	if len(shortErrNodes) != 1 {
		t.Errorf("single-line error <=120 chars must emit no body children, got %d nodes", len(shortErrNodes))
	}

	// Multi-line or >120-char error emits omp.tool.error in the body and strips leading "Error:".
	longErrRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Error: Path 'missing.go' not found\nSecond line of detail"}},
	}
	_, errIDs := nvMustResult(t, "read", longErrRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args:    map[string]any{"path": "missing.go"},
		IsError: true,
	})
	errNode := nvAssertNode(t, errIDs, "omp.tool.error", "text", "Path 'missing.go' not found", "Second line of detail")
	if strings.HasPrefix(errNode.text, "Error:") {
		t.Errorf("expected leading Error: stripped, got %q", errNode.text)
	}
}

// upstream: packages/tui/src/tools/fetch.ts (describeReadUrlCall, describeReadUrlResult)
func TestNativeReadURLAndFetch(t *testing.T) {
	urlRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "URL: https://example.com/docs\nContent-Type: text/html\nMethod: reader\n---\n\nLine one\nLine two"}},
		Details: map[string]any{
			"kind":        "url",
			"url":         "https://example.com/",
			"finalUrl":    "https://example.com/docs",
			"contentType": "text/html",
			"method":      "reader",
			"truncated":   true,
			"notes":       []string{"cached"},
			"meta": map[string]any{
				"truncation": map[string]any{"artifactId": "art_42"},
			},
		},
	}
	_, ids := nvMustResult(t, "read", urlRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "https://example.com/"},
	})
	nvAssertNode(t, ids, "omp.tool.fetch", "container")
	nvAssertNode(t, ids, "omp.tool.fetch.content", "lines", "Line one", "Line two")
	nvAssertNode(t, ids, "omp.tool.stats", "text", "text/html", "reader", "2 lines", "cached")
	nvAssertNode(t, ids, "omp.tool.notice", "text", "Output truncated", "local://art_42.log")
}

// upstream: packages/tui/src/tools/write.ts (describeCall, describeResult)
func TestNativeWrite(t *testing.T) {
	svgContent := "<svg xmlns=\"http://www.w3.org/2000/svg\"><circle r=\"5\"/></svg>"
	_, callIDs := nvMustCall(t, "write", map[string]any{
		"path":    "icon.svg",
		"content": svgContent,
	}, sdk.ToolRenderContext{ArgsComplete: true})
	nvAssertNode(t, callIDs, "omp.tool.write.figure", "markdown", "```svg", "<circle")
	nvAssertNode(t, callIDs, "omp.tool.write.source", "lines", "<svg")

	res := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "[icon.svg#1A2B]\nSuccessfully wrote 65 bytes to icon.svg"}},
		Details: map[string]any{
			"resolvedPath": "/tmp/icon.svg",
			"diagnostics": map[string]any{
				"summary":  "1 warning",
				"errored":  false,
				"messages": []string{"icon.svg:1:1 [warning] missing viewBox (svg-lint)"},
			},
		},
	}
	_, resIDs := nvMustResult(t, "write", res, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "icon.svg", "content": svgContent},
	})
	nvAssertNode(t, resIDs, "omp.tool.write.figure", "markdown", "```svg")
	nvAssertNode(t, resIDs, "omp.tool.write.source", "lines", svgContent)
	nvAssertNode(t, resIDs, "omp.tool.diagnostics", "vstack")
	nvAssertNode(t, resIDs, "omp.tool.diagnostics.head", "text", "Diagnostics")
	nvAssertNode(t, resIDs, "omp.tool.diagnostic#0", "text", "[warning]", "1:1", "missing viewBox")
}

// upstream: packages/tui/src/tools/edit.ts (describeCall, describeResult)
func TestNativeEdit(t *testing.T) {
	// Multi-file hashline CallView
	_, callIDs := nvMustCall(t, "edit", map[string]any{
		"input": "[a.go#1A2B]\nPUT 1.=1:\n+package a\n[b.go#3C4D]\nPUT 1.=1:\n+package b",
	}, sdk.ToolRenderContext{})
	nvAssertNode(t, callIDs, "omp.tool.edit.file#0", "vstack")
	nvAssertNode(t, callIDs, "omp.tool.edit.file.head#0", "text", "a.go")
	nvAssertNode(t, callIDs, "omp.tool.edit.change#0", "markdown", "+package a")
	nvAssertNode(t, callIDs, "omp.tool.edit.file#1", "vstack")
	nvAssertNode(t, callIDs, "omp.tool.edit.file.head#1", "text", "b.go")
	nvAssertNode(t, callIDs, "omp.tool.edit.change#1", "markdown", "+package b")

	// Multi-file ResultView matching internal/files/edit.go aggregateEditDetails
	multiRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Updated 2 files"}},
		Details: map[string]any{
			"perFileResults": []any{
				map[string]any{
					"path":             "/ws/a.go",
					"op":               "update",
					"diff":             "@@ -1,1 +1,1 @@\n-old\n+new\n",
					"firstChangedLine": 1,
				},
				map[string]any{
					"path":       "/ws/c.go",
					"sourcePath": "/ws/b.go",
					"move":       "/ws/c.go",
					"op":         "update",
					"diff":       "@@ -1,1 +1,2 @@\n keep\n+added\n",
				},
			},
		},
	}
	_, resIDs := nvMustResult(t, "edit", multiRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{Cwd: "/ws"})
	nvAssertNode(t, resIDs, "omp.tool.edit.file#0", "vstack")
	nvAssertNode(t, resIDs, "omp.tool.edit.file.head#0", "text", "a.go", "+1", "\u22121")
	nvAssertNode(t, resIDs, "omp.tool.edit.diff#0", "diff", "+new")
	nvAssertNode(t, resIDs, "omp.tool.edit.file#1", "vstack")
	nvAssertNode(t, resIDs, "omp.tool.edit.file.head#1", "text", "b.go", "→ c.go", "+1")
	nvAssertNode(t, resIDs, "omp.tool.edit.diff#1", "diff", "+added")
}

// upstream: packages/tui/src/tools/bash.ts (describeCall, describeResult, stripBashNotices)
func TestNativeBash(t *testing.T) {
	// CallView
	dur := int64(250)
	_, callIDs := nvMustCall(t, "bash", map[string]any{
		"command": "echo hi",
		"cwd":     "/ws/sub",
		"env":     map[string]any{"FOO": "bar"},
	}, sdk.ToolRenderContext{Cwd: "/ws", ExecutionStarted: true, DurationMs: &dur})
	cmdNode := nvAssertNode(t, callIDs, "omp.tool.bash.command", "lines", `cd sub && FOO="bar" echo hi`)
	if strings.HasPrefix(cmdNode.text, "$ ") {
		t.Errorf("native bash command must not have terminal '$ ' prefix: %q", cmdNode.text)
	}
	nvAssertNode(t, callIDs, "omp.run.state", "text", "Running")
	nvAssertNode(t, callIDs, "omp.run.time", "text", "250ms")

	// ResultView: strips Wall time + Command exited with code 2 in omp's order!
	res := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "\x1b[32mhello\x1b[0m\n\x9b31mworld\n\nWall time: 1.50 seconds\n\nCommand exited with code 2\n\n[Showing lines 1-2 of 10]"}},
		Details: map[string]any{
			"exitCode":              2,
			"wallTimeMs":            1500.0,
			"wallTimeNoticeEmitted": true,
			"async":                 map[string]any{"state": "completed", "jobId": "bg_1"},
			"steps": []any{
				map[string]any{"label": "echo", "note": "0.1s"},
			},
			"meta": map[string]any{
				"truncation": map[string]any{
					"direction":   "tail",
					"truncatedBy": "lines",
					"totalLines":  10,
					"outputLines": 2,
					"artifactId":  "art_9",
				},
			},
		},
	}
	_, resIDs := nvMustResult(t, "bash", res, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args:    map[string]any{"command": "echo hi"},
		IsError: true,
	})
	nvAssertNode(t, resIDs, "omp.run", "vstack")
	nvAssertNode(t, resIDs, "omp.tool.bash.step#0", "hstack")
	nvAssertNode(t, resIDs, "omp.tool.bash.step.label#0", "text", "echo")
	nvAssertNode(t, resIDs, "omp.tool.bash.step.note#0", "text", "0.1s")
	outNode := nvAssertNode(t, resIDs, "omp.tool.bash.output", "lines", "hello", "31mworld")
	if strings.Contains(outNode.text, "Wall time:") || strings.Contains(outNode.text, "Command exited") {
		t.Errorf("bash notices were not stripped in omp order: %q", outNode.text)
	}
	nvAssertNode(t, resIDs, "omp.run.foot", "hstack")
	nvAssertNode(t, resIDs, "omp.run.state", "text", "exit 2")
	nvAssertNode(t, resIDs, "omp.run.time", "text", "1.5s")
	nvAssertNode(t, resIDs, "omp.run.facts", "text", "local://art_9.log")
}

// upstream: packages/tui/src/tools/eval.ts (describeCall, describeResult)
func TestNativeEval(t *testing.T) {
	res := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "42"}},
		Details: map[string]any{
			"notice": "kernel reset",
			"cells": []any{
				map[string]any{
					"index":      0,
					"title":      "Load",
					"code":       "x = 42\nprint(x)",
					"language":   "python",
					"output":     "42\nCOL_TABLE\n",
					"status":     "complete",
					"durationMs": 320,
					"views": []any{
						map[string]any{
							"text": "COL_TABLE",
							"view": map[string]any{
								"kind":    "table",
								"columns": []any{map[string]any{"head": "k"}, map[string]any{"head": "v", "numeric": true}},
								"rows":    []any{[]any{"a", "1"}},
							},
						},
					},
					"statusEvents": []any{
						map[string]any{"op": "read", "path": "foo.go", "preview": "line1\nline2"},
					},
				},
				map[string]any{
					"index":      1,
					"title":      "Check",
					"code":       "assert x == 42",
					"language":   "python",
					"output":     "",
					"status":     "error",
					"durationMs": 40,
					"exitCode":   1,
				},
			},
		},
	}
	_, ids := nvMustResult(t, "eval", res, sdk.ToolRenderResultOptions{Expanded: true}, sdk.ToolRenderContext{})
	nvAssertNode(t, ids, "omp.run#0", "vstack")
	nvAssertNode(t, ids, "omp.run.caption#0", "text", "1/2 Load")
	nvAssertNode(t, ids, "omp.tool.eval.input#0", "lines", "x = 42")
	nvAssertNode(t, ids, "omp.tool.eval.output#0.0", "lines", "42")
	nvAssertNode(t, ids, "omp.tool.eval.table#0.1", "lines", "k · v", "a · 1")
	nvAssertNode(t, ids, "omp.run.status#0", "vstack")
	nvAssertNode(t, ids, "omp.tool.eval.status.event#0.0", "vstack")
	nvAssertNode(t, ids, "omp.tool.eval.status#0.0", "text", "read", "path=foo.go")
	nvAssertNode(t, ids, "omp.tool.eval.status.preview#0.0", "lines", "line1", "line2")
	nvAssertNode(t, ids, "omp.run.state#0", "text", "Done")
	nvAssertNode(t, ids, "omp.run.time#0", "text", "320ms")
	nvAssertNode(t, ids, "omp.run#1", "vstack")
	nvAssertNode(t, ids, "omp.run.state#1", "text", "exit 1")
	nvAssertNode(t, ids, "omp.run.facts#1", "text", "kernel reset")
}

// upstream: packages/tui/src/tools/{grep,glob,find,ast-grep,ast-edit}.ts
func TestNativeSearchTools(t *testing.T) {
	// grep: padded gutter `*5│` matches omp's GROUPED_NUMBERED_LINE_RE
	grepRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"matchCount": 3,
			"fileCount":  3,
			"files":      []string{"src/a.go", "src/b.go", "src/c.go"},
			"displayContent": strings.Join([]string{
				"# src/",
				"## a.go#1A2B",
				"   *5│foo()",
				"    6│bar()",
				"## b.go#3C4D",
				" *100│foo()",
				"## c.go#5E6F",
				"   *1│foo()",
			}, "\n"),
		},
	}
	_, gIDs := nvMustResult(t, "grep", grepRes, sdk.ToolRenderResultOptions{Expanded: false}, sdk.ToolRenderContext{})
	nvAssertNode(t, gIDs, "omp.tool.search.file#0", "vstack")
	f0 := nvAssertNode(t, gIDs, "omp.tool.file#0", "text", "src/a.go")
	if f0.text != "src/a.go" {
		t.Errorf("grep omp.tool.file#0 must be clean path without count, got %q", f0.text)
	}
	l0 := nvAssertNode(t, gIDs, "omp.tool.search.lines#0", "lines", "foo()", "bar()")
	if strings.Contains(l0.text, "│") || strings.Contains(l0.text, "*5") {
		t.Errorf("grep omp.tool.search.lines#0 must not carry terminal gutter: %q", l0.text)
	}
	nvAssertNode(t, gIDs, "omp.tool.search.file#1", "vstack")
	f1 := nvAssertNode(t, gIDs, "omp.tool.file#1", "text", "src/b.go")
	if f1.text != "src/b.go" {
		t.Errorf("grep omp.tool.file#1 must be clean path without count, got %q", f1.text)
	}
	if _, ok := gIDs["omp.tool.search.file#2"]; ok {
		t.Errorf("collapsed grep should cap at 2 files")
	}
	nvAssertNode(t, gIDs, "omp.tool.stats", "text", "1 more file")

	// glob <= 6 files -> VStack of omp.tool.file
	globSmall := sdk.ToolRenderResult{
		Details: map[string]any{
			"fileCount": 2,
			"files":     []string{"a.go", "dir/"},
		},
	}
	_, gsIDs := nvMustResult(t, "glob", globSmall, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	nvAssertNode(t, gsIDs, "omp.tool.files", "vstack")
	nvAssertNode(t, gsIDs, "omp.tool.file#0", "text", "a.go")
	nvAssertNode(t, gsIDs, "omp.tool.file#1", "text", "dir/")

	// glob > 6 files -> HStack of omp.tool.chip
	globMany := sdk.ToolRenderResult{
		Details: map[string]any{
			"fileCount": 8,
			"files":     []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go", "g.go", "h.go"},
		},
	}
	_, gmIDs := nvMustResult(t, "glob", globMany, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	nvAssertNode(t, gmIDs, "omp.tool.files", "hstack")
	nvAssertNode(t, gmIDs, "omp.tool.chip#0", "text", "a.go")
	nvAssertNode(t, gmIDs, "omp.tool.chip#7", "text", "h.go")

	// find
	findRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"query":    "auth middleware",
			"keywords": []string{"auth", "middleware"},
			"stats":    map[string]any{"filesRead": 5, "requests": 2, "failed": 0},
			"hits": []any{
				map[string]any{
					"rel":          "pkg/auth.go",
					"contentScore": 0.92,
					"linesSeen":    120,
					"ranges": []any{
						map[string]any{"start": 10, "end": 20, "p": 0.92, "snippet": "func Auth() {}"},
					},
				},
			},
		},
	}
	_, fIDs := nvMustResult(t, "find", findRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	nvAssertNode(t, fIDs, "omp.tool.find.hit#0", "vstack")
	nvAssertNode(t, fIDs, "omp.tool.file#0", "text", "pkg/auth.go", "0.92", "120 lines judged")
	nvAssertNode(t, fIDs, "omp.tool.find.range#0.0", "text", "L10-20 (0.92)", "func Auth() {}")
	nvAssertNode(t, fIDs, "omp.tool.stats", "text", "keywords: auth, middleware")

	// ast_grep
	agRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"matchCount":     1,
			"fileCount":      1,
			"filesSearched":  4,
			"limitReached":   true,
			"displayContent": "# pkg/\n## x.go\n*12│return err",
		},
	}
	_, agIDs := nvMustResult(t, "ast_grep", agRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	nvAssertNode(t, agIDs, "omp.tool.search.file#0", "vstack")
	nvAssertNode(t, agIDs, "omp.tool.file#0", "text", "pkg/x.go")
	nvAssertNode(t, agIDs, "omp.tool.notice", "text", "limit reached")

	// ast_edit (multi-file + single rewrite context)
	aeRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"totalReplacements": 2,
			"filesTouched":      2,
			"filesSearched":     10,
			"scopePath":         "src",
			"applied":           true,
			"displayContent": strings.Join([]string{
				"# src/",
				"## a.go (1 replacement)",
				"-10│old()",
				"+10│new()",
				"## b.go (1 replacement)",
				"-20│old()",
				"+20│new()",
			}, "\n"),
		},
	}
	_, aeIDs := nvMustResult(t, "ast_edit", aeRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args: map[string]any{"ops": []any{map[string]any{"pat": "old()", "out": "new()"}}},
	})
	nvAssertNode(t, aeIDs, "omp.tool.context", "text", "old()")
	nvAssertNode(t, aeIDs, "omp.tool.ast_edit.file#0", "vstack")
	nvAssertNode(t, aeIDs, "omp.tool.ast_edit.file.head#0", "text", "src/a.go", "+1", "\u22121")
	nvAssertNode(t, aeIDs, "omp.tool.ast_edit.diff#0", "diff", "-old()", "+new()")
	nvAssertNode(t, aeIDs, "omp.tool.stats", "text", "in src", "searched 10 files")
}

// upstream: packages/tui/src/tools/todo.ts, ask.ts, native/ask-card.ts, task.ts, wait.ts
func TestNativeStructuredTools(t *testing.T) {
	// todo
	todoRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"phases": []any{
				map[string]any{
					"name": "Setup",
					"tasks": []any{
						map[string]any{"content": "Init repo", "status": "completed"},
					},
				},
				map[string]any{
					"name": "Build",
					"tasks": []any{
						map[string]any{"content": "Write tests", "status": "in_progress"},
						map[string]any{"content": "Ship", "status": "pending"},
					},
				},
			},
		},
	}
	_, tIDs := nvMustResult(t, "todo", todoRes, sdk.ToolRenderResultOptions{Expanded: true}, sdk.ToolRenderContext{})
	nvAssertNode(t, tIDs, "omp.todo.checklist", "vstack")
	nvAssertNode(t, tIDs, "omp.todo.phase#0", "vstack")
	head0 := nvAssertNode(t, tIDs, "omp.todo.phase.head#0", "text", "Setup")
	if strings.Contains(head0.text, "I.") {
		t.Errorf("todo phase head must be plain title without Roman numeral, got %q", head0.text)
	}
	nvAssertNode(t, tIDs, "omp.todo.item#0.0", "text", "☑ Init repo")
	nvAssertNode(t, tIDs, "omp.todo.phase.head#1", "text", "Build")
	nvAssertNode(t, tIDs, "omp.todo.item#1.0", "text", "◐ Write tests")
	nvAssertNode(t, tIDs, "omp.todo.item#1.1", "text", "☐ Ship")

	// ask CallView (full form card)
	formHTML := `<form>
		<fieldset><legend>Rollout strategy?</legend>
			<input type="radio" name="rollout" value="Canary" checked>
			<input type="radio" name="rollout" value="BlueGreen">
			<input type="radio" name="rollout" value="Immediate">
			<input type="radio" name="rollout" value="DarkLaunch">
			<input type="radio" name="rollout" value="Shadow">
		</fieldset>
		<label>Notes</label><input type="text" name="note" placeholder="Optional reason">
	</form>`
	_, askCallIDs := nvMustCall(t, "ask", map[string]any{"html": formHTML}, sdk.ToolRenderContext{})
	nvAssertNode(t, askCallIDs, "omp.ask.card", "vstack")
	nvAssertNode(t, askCallIDs, "omp.ask.card.head", "hstack")
	nvAssertNode(t, askCallIDs, "omp.ask.card.tag", "text", "ask")
	nvAssertNode(t, askCallIDs, "omp.ask.card.progress", "text", "1 of 2 answered")
	nvAssertNode(t, askCallIDs, "omp.ask.open#0", "vstack")
	nvAssertNode(t, askCallIDs, "omp.ask.fold.mark#0", "text", "✓")
	nvAssertNode(t, askCallIDs, "omp.ask.fold.question#0", "text", "Rollout strategy?")
	nvAssertNode(t, askCallIDs, "omp.ask.pills#0", "hstack")
	nvAssertNode(t, askCallIDs, "omp.ask.pill#0.0", "text", "Canary")
	nvAssertNode(t, askCallIDs, "omp.ask.pill.more#0", "text", "+1")
	nvAssertNode(t, askCallIDs, "omp.ask.fold#1", "vstack")
	nvAssertNode(t, askCallIDs, "omp.ask.fold.well#1", "text", "Optional reason")

	// ask ResultView (with password masking + option note + timedOut)
	askRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"timedOut": true,
			"form": map[string]any{
				"fields": []any{
					map[string]any{"name": "rollout", "kind": "choice", "question": "Rollout?"},
					map[string]any{"name": "secret", "kind": "text", "type": "password", "question": "API Key"},
					map[string]any{"name": "empty", "kind": "text", "question": "Extra"},
				},
			},
			"answers": []any{
				map[string]any{"name": "rollout", "question": "Rollout?", "answer": "Canary"},
				map[string]any{"name": "secret", "question": "API Key", "answer": "supersecret"},
				map[string]any{"name": "empty", "question": "Extra", "answer": ""},
			},
			"notes": []any{
				map[string]any{"name": "rollout", "question": "Rollout?", "option": "Canary", "value": "canary", "text": "10% first"},
			},
		},
	}
	_, askResIDs := nvMustResult(t, "ask", askRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	if _, hasMark := askResIDs["omp.ask.fold.mark#0"]; hasMark {
		t.Errorf("ask ResultView must not emit duplicate omp.ask.fold.mark alongside omp.tool.answer")
	}
	nvAssertNode(t, askResIDs, "omp.ask.fold.question#0", "text", "Rollout?")
	nvAssertNode(t, askResIDs, "omp.tool.answer#0", "text", "Canary")
	nvAssertNode(t, askResIDs, "omp.tool.answer.note#0.0", "text", "Canary: 10% first")
	pwNode := nvAssertNode(t, askResIDs, "omp.tool.answer#1", "text", "•••••••••••")
	if strings.Contains(pwNode.text, "supersecret") {
		t.Errorf("password answer leaked plaintext: %q", pwNode.text)
	}
	nvAssertNode(t, askResIDs, "omp.tool.answer.off#2", "text", "(no answer)")
	nvAssertNode(t, askResIDs, "omp.tool.notice", "text", "auto-selected after timeout")
	// task: validation error with empty results MUST still show error text
	taskErr := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": `Unknown agent "nope". Available: scout, task`}},
		Details: map[string]any{"results": []any{}, "totalDurationMs": 0},
	}
	_, taskErrIDs := nvMustResult(t, "task", taskErr, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{IsError: true})
	nvAssertNode(t, taskErrIDs, "omp.tool.error", "text", `Unknown agent "nope"`)

	// task: settled + running progress agents
	taskRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"totalDurationMs": 4200,
			"results": []any{
				map[string]any{
					"id":                    "Scout1",
					"agent":                 "scout",
					"task":                  "Inspect parser",
					"exitCode":              0,
					"output":                "Found 3 callers",
					"durationMs":            2100,
					"requests":              4,
					"tokens":                12000,
					"resolvedModelIdentity": "claude-sonnet",
				},
			},
			"progress": []any{
				map[string]any{
					"id":          "Worker2",
					"agent":       "task",
					"status":      "running",
					"currentTool": "grep",
					"durationMs":  1100,
				},
			},
		},
	}
	_, taskIDs := nvMustResult(t, "task", taskRes, sdk.ToolRenderResultOptions{Expanded: true}, sdk.ToolRenderContext{
		Args: map[string]any{"context": "Audit error handling"},
	})
	nvAssertNode(t, taskIDs, "omp.tool.context", "text", "Audit error handling")
	nvAssertNode(t, taskIDs, "omp.task.agent#0", "vstack")
	nvAssertNode(t, taskIDs, "omp.task.agent.row#0", "text", "Scout1", "done", "claude-sonnet", "12K tok")
	nvAssertNode(t, taskIDs, "omp.task.assignment#0", "markdown", "Inspect parser")
	nvAssertNode(t, taskIDs, "omp.task.output#0", "markdown", "Found 3 callers")
	nvAssertNode(t, taskIDs, "omp.task.agent#1", "vstack")
	nvAssertNode(t, taskIDs, "omp.task.agent.row#1", "text", "Worker2", "running", "grep")
	nvAssertNode(t, taskIDs, "omp.tool.stats", "text", "1 succeeded", "4 req", "4.2s")

	// wait: tiles + widget blocks + active agent
	waitRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"op": "wait",
			"jobs": []any{
				map[string]any{
					"id":                "bg_1",
					"type":              "bash",
					"status":            "running",
					"label":             "go test ./...",
					"durationMs":        3100,
					"completionPercent": 60,
					"widget": map[string]any{
						"state":    "live",
						"title":    "Benchmark",
						"stage":    "running shard 3",
						"headline": map[string]any{"text": "186 tok/s", "label": "decode"},
						"blocks": []any{
							map[string]any{"kind": "meter", "label": "trials", "value": 0.6, "text": "6/10 trials"},
							map[string]any{"kind": "stat", "items": []any{map[string]any{"label": "p50", "text": "41 ms"}}},
						},
					},
				},
			},
			"agents": []any{
				map[string]any{"id": "Scout1", "parentId": "Main", "activity": "reading files", "ageMs": 2000, "live": true},
			},
		},
	}
	_, wIDs := nvMustResult(t, "wait", waitRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	nvAssertNode(t, wIDs, "omp.job.tiles", "vstack")
	nvAssertNode(t, wIDs, "omp.job.tile#0", "vstack")
	nvAssertNode(t, wIDs, "omp.job.tile.title#0", "text", "Benchmark")
	nvAssertNode(t, wIDs, "omp.job.tile.id#0", "text", "bg_1")
	nvAssertNode(t, wIDs, "omp.job.tile.ring#0", "lines", "60%")
	nvAssertNode(t, wIDs, "omp.job.tile.figure#0", "text", "decode 186 tok/s")
	nvAssertNode(t, wIDs, "omp.job.tile.meter#0", "lines", "6/10 trials")
	nvAssertNode(t, wIDs, "omp.job.tile.stats#0.1", "text", "p50 41 ms")
	nvAssertNode(t, wIDs, "omp.job.tile.stage#0", "text", "running shard 3")
	nvAssertNode(t, wIDs, "omp.wait.agent#0", "text", "Scout1", "reading files", "← Main")
}

// upstream: packages/tui/src/tools/{web-search,lsp,github,debug,proc-render,cfg-render,resolve,memory,think,goal,yield,default-renderer}.ts
func TestNativeExtraAndURLTools(t *testing.T) {
	// web_search (with sources and excerpts when no answer)
	wsRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"response": map[string]any{
				"provider": "exa",
				"sources": []any{
					map[string]any{
						"title":      "Go Release Notes",
						"url":        "https://go.dev/doc/go1.22",
						"snippet":    "Range over integers\nEnhanced routing",
						"ageSeconds": 7200,
					},
				},
				"usage": map[string]any{"searchRequests": 1, "totalTokens": 300},
			},
		},
	}
	_, wsIDs := nvMustResult(t, "web_search", wsRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	nvAssertNode(t, wsIDs, "omp.tool.sources", "vstack")
	nvAssertNode(t, wsIDs, "omp.tool.source#0", "vstack")
	nvAssertNode(t, wsIDs, "omp.tool.source.head#0", "text", "Go Release Notes", "go.dev", "2h")
	nvAssertNode(t, wsIDs, "omp.tool.excerpt#0", "text", "Range over integers")
	nvAssertNode(t, wsIDs, "omp.tool.stats", "text", "total 300", "search 1")

	// lsp diagnostics
	lspRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "1 error(s)\nmain.go:12:4 [error] undefined: foo (gopls)"}},
		Details: map[string]any{"action": "diagnostics", "success": true},
	}
	_, lspIDs := nvMustResult(t, "lsp", lspRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args: map[string]any{"action": "diagnostics", "file": "main.go"},
	})
	nvAssertNode(t, lspIDs, "omp.tool.diagnostic#0", "text", "[error]", "main.go:12:4", "[gopls]", "undefined: foo")

	// github run_watch
	ghRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"watch": map[string]any{
				"mode":  "run",
				"state": "completed",
				"repo":  "org/repo",
				"run": map[string]any{
					"id": 99,
					"jobs": []any{
						map[string]any{"id": 1, "name": "test", "conclusion": "failure", "durationSeconds": 12},
					},
				},
				"failedLogs": []any{
					map[string]any{"runId": 99, "jobName": "test", "available": true, "tail": "FAIL TestFoo"},
				},
			},
		},
	}
	_, ghIDs := nvMustResult(t, "github", ghRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	nvAssertNode(t, ghIDs, "omp.tool.github.watch", "vstack")
	nvAssertNode(t, ghIDs, "omp.tool.github.job#0", "text", "test", "failure", "12s")
	nvAssertNode(t, ghIDs, "omp.tool.github.log#0", "lines", "test · run #99", "FAIL TestFoo")

	// debug
	dbgRes := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Stopped at main.go:10"}},
		Details: map[string]any{
			"action": "continue",
			"snapshot": map[string]any{
				"id":         "dbg-1",
				"adapter":    "dlv",
				"status":     "stopped",
				"sourcePath": "main.go",
				"line":       10,
			},
		},
	}
	_, dbgIDs := nvMustResult(t, "debug", dbgRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	nvAssertNode(t, dbgIDs, "omp.tool.debug.snapshot", "lines", "Session · dbg-1", "Location · main.go:10")
	nvAssertNode(t, dbgIDs, "omp.tool.debug.output", "lines", "Stopped at main.go:10")

	// proc:// read (table) & write (cancel)
	procRead := sdk.ToolRenderResult{
		Details: map[string]any{
			"proc": map[string]any{
				"jobs":    []any{map[string]any{"id": "bg_1", "type": "bash", "status": "running", "label": "make test", "durationMs": 1200}},
				"daemons": []any{map[string]any{"name": "web", "state": "ready", "pid": 4242, "mode": "persist"}},
				"agents":  []any{},
			},
		},
	}
	_, prIDs := nvMustResult(t, "read", procRead, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "proc://"},
	})
	nvAssertNode(t, prIDs, "omp.tool.proc.table", "lines", "[bash] bg_1", "service web")

	procCancel := sdk.ToolRenderResult{
		Details: map[string]any{
			"proc": map[string]any{
				"op":        "cancel",
				"jobs":      []any{map[string]any{"id": "bg_1", "type": "bash", "status": "cancelled", "label": "make test"}},
				"cancelled": []any{map[string]any{"id": "bg_1", "status": "cancelled"}},
			},
		},
	}
	_, pcIDs := nvMustResult(t, "write", procCancel, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "proc://bg_1/kill", "content": ""},
	})
	nvAssertNode(t, pcIDs, "omp.tool.proc.cancelled", "lines", "[bash] bg_1", "cancelled")

	// agent:// write (IRC receipts)
	ircRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"message": map[string]any{
				"to": "all",
				"receipts": []any{
					map[string]any{"to": "WorkerA", "outcome": "injected"},
					map[string]any{"to": "WorkerB", "outcome": "failed", "error": "parked"},
				},
			},
		},
	}
	_, ircIDs := nvMustResult(t, "write", ircRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "agent://all", "content": "sync up"},
	})
	nvAssertNode(t, ircIDs, "omp.tool.proc.preview", "lines", "sync up")
	nvAssertNode(t, ircIDs, "omp.tool.irc.receipts", "lines", "WorkerA · injected", "WorkerB · parked · failed")

	// cfg:// read & write
	cfgRead := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "theme: dark\n"}},
		Details: map[string]any{"cfg": map[string]any{"path": "theme", "count": 1}},
	}
	_, crIDs := nvMustResult(t, "read", cfgRead, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "cfg://theme"},
	})
	nvAssertNode(t, crIDs, "omp.tool.cfg.listing", "lines", "theme: dark")

	cfgWrite := sdk.ToolRenderResult{
		Details: map[string]any{
			"cfg": map[string]any{
				"path":      "theme",
				"previous":  "dark",
				"value":     "light",
				"effective": "dark",
			},
		},
	}
	_, cwIDs := nvMustResult(t, "write", cfgWrite, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "cfg://theme/save", "content": "light"},
	})
	nvAssertNode(t, cwIDs, "omp.tool.cfg.change", "text", "dark → light")
	nvAssertNode(t, cwIDs, "omp.tool.notice", "text", "still dark")

	// resolve, propose, retain, recall, reflect, think, goal, yield, checkpoint, rewind
	_, resIDs := nvMustResult(t, "resolve", sdk.ToolRenderResult{
		Details: map[string]any{"action": "apply", "reason": "looks clean"},
	}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	nvAssertNode(t, resIDs, "omp.tool.stats", "text", "looks clean")

	_, thIDs := nvMustCall(t, "think", map[string]any{"thoughts": "checking edge cases"}, sdk.ToolRenderContext{})
	nvAssertNode(t, thIDs, "omp.think", "markdown", "checking edge cases")

	_, goalIDs := nvMustResult(t, "goal", sdk.ToolRenderResult{
		Details: map[string]any{
			"op": "status",
			"goal": map[string]any{
				"objective":       "Ship v1",
				"status":          "active",
				"tokensUsed":      5000,
				"tokenBudget":     20000,
				"timeUsedSeconds": 12,
			},
		},
	}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	nvAssertNode(t, goalIDs, "omp.tool.context", "text", `"Ship v1"`)
	nvAssertNode(t, goalIDs, "omp.tool.stats", "text", "5K / 20K tokens", "15K left", "12s elapsed")

	_, yIDs := nvMustResult(t, "yield", sdk.ToolRenderResult{
		Details: map[string]any{"status": "success", "data": map[string]any{"ok": true}},
	}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	nvAssertNode(t, yIDs, "omp.tool.yield.data", "lines", `"ok": true`)

	_, cpCallIDs := nvMustCall(t, "checkpoint", map[string]any{"goal": "explore cache"}, sdk.ToolRenderContext{})
	nvAssertNode(t, cpCallIDs, "omp.tool.args", "text", `goal="explore cache"`)
	_, rwIDs := nvMustResult(t, "rewind", sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Rewind requested."}},
	}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	nvAssertNode(t, rwIDs, "omp.tool.output", "lines", "Rewind requested.")
}

// TestNativeNodeBudgetCap verifies large expanded results stay strictly under
// PiG host's 4096-node limit with zero duplicate IDs.
func TestNativeNodeBudgetCap(t *testing.T) {
	// 3000-file grep expanded (the exact OmpAnsi review repro)
	var b strings.Builder
	for i := range 3000 {
		fmt.Fprintf(&b, "## file_%04d.go\n*1│match %d\n\n", i, i)
	}
	grepBig := sdk.ToolRenderResult{
		Details: map[string]any{
			"matchCount":     3000,
			"fileCount":      3000,
			"displayContent": b.String(),
		},
	}
	nodes, _ := nvMustResult(t, "grep", grepBig, sdk.ToolRenderResultOptions{Expanded: true}, sdk.ToolRenderContext{})
	if len(nodes) >= 4096 {
		t.Fatalf("expanded 3000-file grep produced %d nodes (>= 4096)", len(nodes))
	}

	// 2500-file glob
	files := make([]string, 2500)
	for i := range files {
		files[i] = fmt.Sprintf("pkg/file_%04d.go", i)
	}
	globBig := sdk.ToolRenderResult{
		Details: map[string]any{"fileCount": len(files), "files": files},
	}
	globNodes, _ := nvMustResult(t, "glob", globBig, sdk.ToolRenderResultOptions{Expanded: true}, sdk.ToolRenderContext{})
	if len(globNodes) >= 4096 {
		t.Fatalf("2500-file glob produced %d nodes (>= 4096)", len(globNodes))
	}
}

// TestNativeSurfaceDispatch verifies RenderersForTool routes to native builders
// when surface.Native() is true and never panics across all registered tools.
func TestNativeSurfaceDispatch(t *testing.T) {
	surface.ResetForTests()
	t.Cleanup(surface.ResetForTests)
	surface.SetNative(true)

	tools := []string{
		"read", "fetch", "write", "edit", "apply_patch",
		"bash", "eval",
		"grep", "glob", "find", "ast_grep", "ast_edit",
		"todo", "ask", "task", "subagent", "wait",
		"web_search", "lsp", "github", "debug",
		"resolve", "reject", "propose", "report_issue",
		"retain", "recall", "reflect", "think", "goal", "yield",
		"checkpoint", "rewind", "browser",
	}
	for _, name := range tools {
		r := RenderersForTool(name)
		if r == nil {
			t.Fatalf("RenderersForTool(%q) returned nil", name)
		}
		callView, err := r.CallView(sdk.Context{}, map[string]any{}, sdk.ToolRenderContext{IsPartial: true}, 80)
		if err != nil {
			t.Errorf("%s CallView error: %v", name, err)
		}
		nvWalkTree(t, callView.Root)

		resView, err := r.ResultView(sdk.Context{}, sdk.ToolRenderResult{}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{}, 80)
		if err != nil {
			t.Errorf("%s ResultView error: %v", name, err)
		}
		nvWalkTree(t, resView.Root)
	}
}

// TestNativePass2Regressions tests every item from pass-2 review findings N7 and N8.
func TestNativePass2Regressions(t *testing.T) {
	// N7(1): ask error / unanswered timeout with empty non-nil Answers slice must NOT be empty.
	askEmptyErr := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Error: the form has no named controls."}},
		Details: map[string]any{"answers": []any{}},
	}
	_, askErrIDs := nvMustResult(t, "ask", askEmptyErr, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{IsError: true})
	nvAssertNode(t, askErrIDs, "omp.tool.error", "text", "no named controls")

	askEmptyTimeout := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "The ask timed out before the user answered its required fields."}},
		Details: map[string]any{"answers": []any{}, "timedOut": true},
	}
	_, askToIDs := nvMustResult(t, "ask", askEmptyTimeout, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	nvAssertNode(t, askToIDs, "omp.tool.notice", "text", "timed out")

	// N7(2): tabs must be preserved verbatim in code and diff payloads (Makefile / Go files).
	origSettingTab := Setting
	Setting = func(_ sdk.Context, key string) any {
		if key == "read.toolResultPreview" {
			return true
		}
		return nil
	}
	tabCode := "all:\n\tgo test ./...\n"
	_, readIDs := nvMustResult(t, "read", sdk.ToolRenderResult{
		Details: map[string]any{"displayContent": map[string]any{"text": tabCode, "startLine": 1}},
	}, sdk.ToolRenderResultOptions{Expanded: true}, sdk.ToolRenderContext{Args: map[string]any{"path": "Makefile"}})
	Setting = origSettingTab
	if got := nvAssertNode(t, readIDs, "omp.tool.read.code", "lines").text; !strings.Contains(got, "\tgo test") {
		t.Errorf("read.code corrupted literal tab: %q", got)
	}
	_, writeIDs := nvMustResult(t, "write", sdk.ToolRenderResult{}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args: map[string]any{"path": "Makefile", "content": tabCode},
	})
	if got := nvAssertNode(t, writeIDs, "omp.tool.write.source", "lines").text; !strings.Contains(got, "\tgo test") {
		t.Errorf("write.source corrupted literal tab: %q", got)
	}

	tabDiff := "@@ -1,1 +1,1 @@\n-\told\n+\tnew\n"
	_, editIDs := nvMustResult(t, "edit", sdk.ToolRenderResult{
		Details: map[string]any{"path": "Makefile", "diff": tabDiff},
	}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	if got := nvAssertNode(t, editIDs, "omp.tool.edit.diff", "diff").text; !strings.Contains(got, "+\tnew") {
		t.Errorf("edit.diff corrupted literal tab: %q", got)
	}

	_, evalIDs := nvMustResult(t, "eval", sdk.ToolRenderResult{
		Details: map[string]any{
			"cells": []any{map[string]any{"index": 0, "code": "if True:\n\tprint(1)", "output": "a\tb\n", "status": "complete"}},
		},
	}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	if got := nvAssertNode(t, evalIDs, "omp.tool.eval.input", "lines").text; !strings.Contains(got, "\tprint(1)") {
		t.Errorf("eval.input corrupted literal tab: %q", got)
	}
	if got := nvAssertNode(t, evalIDs, "omp.tool.eval.output", "lines").text; !strings.Contains(got, "a\tb") {
		t.Errorf("eval.output corrupted literal tab: %q", got)
	}

	// N7(4): no double-hash IDs on multi-file edit diagnostics or task abort+error.
	multiDiagEdit := sdk.ToolRenderResult{
		Details: map[string]any{
			"perFileResults": []any{
				map[string]any{"path": "a.go", "diff": "@@ -1 +1 @@\n-a\n+b", "diagnostics": map[string]any{"messages": []string{"a.go:1:1 [error] bad a"}}},
				map[string]any{"path": "b.go", "diff": "@@ -1 +1 @@\n-c\n+d", "diagnostics": map[string]any{"messages": []string{"b.go:2:1 [error] bad b"}}},
			},
		},
	}
	_, mdIDs := nvMustResult(t, "edit", multiDiagEdit, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	nvAssertNode(t, mdIDs, "omp.tool.diagnostic#0.0", "text", "bad a")
	nvAssertNode(t, mdIDs, "omp.tool.diagnostic#1.0", "text", "bad b")

	taskBothErr := sdk.ToolRenderResult{
		Details: map[string]any{
			"results": []any{
				map[string]any{"id": "W1", "aborted": true, "abortReason": "timeout", "error": "cancelled"},
			},
		},
	}
	_, tbIDs := nvMustResult(t, "task", taskBothErr, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	nvAssertNode(t, tbIDs, "omp.tool.error#0.0", "text", "timeout")
	nvAssertNode(t, tbIDs, "omp.tool.error#0.1", "text", "cancelled")

	// N7(5): edit CallView non-diff replacement uses omp.tool.write.source, not omp.tool.edit.diff.
	_, editCallIDs := nvMustCall(t, "edit", map[string]any{"path": "a.go", "newText": "func f() {\n\treturn\n}"}, sdk.ToolRenderContext{})
	if _, isDiff := editCallIDs["omp.tool.edit.diff"]; isDiff {
		t.Errorf("edit CallView must not emit raw newText under omp.tool.edit.diff")
	}
	if got := nvAssertNode(t, editCallIDs, "omp.tool.write.source", "lines").text; !strings.Contains(got, "\treturn") {
		t.Errorf("edit CallView newText missing tab: %q", got)
	}

	// N7(5): multiple <select> elements keep their own options scoped.
	multiSelectHTML := `<form>
		<label>Color</label><select name="color"><option>Red</option><option>Blue</option></select>
		<label>Size</label><select name="size"><option>Small</option><option>Large</option></select>
	</form>`
	_, msIDs := nvMustCall(t, "ask", map[string]any{"html": multiSelectHTML}, sdk.ToolRenderContext{})
	nvAssertNode(t, msIDs, "omp.ask.pill#0.0", "text", "Red")
	nvAssertNode(t, msIDs, "omp.ask.pill#0.1", "text", "Blue")
	if _, leaked := msIDs["omp.ask.pill#0.2"]; leaked {
		t.Errorf("first <select> leaked options from second <select>")
	}
	nvAssertNode(t, msIDs, "omp.ask.pill#1.0", "text", "Small")
	nvAssertNode(t, msIDs, "omp.ask.pill#1.1", "text", "Large")

	// N4 (native bash): unbracketed "Backgrounded as job <id>..." notice is stripped.
	bgBash := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "server started\n\nBackgrounded as job bg_7 (no deadline); its output is injected into the conversation as a follow-up the moment it finishes. Do NOT poll for it."}},
		Details: map[string]any{"async": map[string]any{"state": "running", "jobId": "bg_7"}},
	}
	_, bgIDs := nvMustResult(t, "bash", bgBash, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{Args: map[string]any{"command": "serve"}})
	bgOut := nvAssertNode(t, bgIDs, "omp.tool.bash.output", "lines", "server started")
	if strings.Contains(bgOut.text, "Do NOT poll") {
		t.Errorf("unbracketed background notice leaked into bash output: %q", bgOut.text)
	}

	// N8: wait tiles: widget.blocks counted against node budget + widget.state === "live" guard + glyphs ✕/■.
	bigBlocks := make([]any, 3000)
	for i := range bigBlocks {
		bigBlocks[i] = map[string]any{"kind": "text", "text": fmt.Sprintf("step %d", i)}
	}
	waitBig := sdk.ToolRenderResult{
		Details: map[string]any{
			"op": "wait",
			"jobs": []any{
				map[string]any{"id": "j1", "type": "bash", "status": "failed", "label": "a", "widget": map[string]any{"state": "live", "blocks": bigBlocks}},
				map[string]any{"id": "j2", "type": "bash", "status": "cancelled", "label": "b", "widget": map[string]any{"state": "live", "blocks": bigBlocks}},
			},
		},
	}
	bigNodes, bigIDs := nvMustResult(t, "wait", waitBig, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	if len(bigNodes) >= 4096 {
		t.Fatalf("wait with 2x3000 widget.blocks produced %d nodes (>= 4096)", len(bigNodes))
	}
	nvAssertNode(t, bigIDs, "omp.job.tile.dot#0", "text", "✕")
	nvAssertNode(t, bigIDs, "omp.job.tile.dot#1", "text", "■")

	// Non-live widget (authoring/fallback) must NOT emit figure or blocks (job-widget.ts:404).
	waitFallback := sdk.ToolRenderResult{
		Details: map[string]any{
			"op": "wait",
			"jobs": []any{
				map[string]any{
					"id": "j3", "type": "bash", "status": "running", "label": "bench",
					"widget": map[string]any{
						"state":    "authoring",
						"title":    "Bench",
						"stage":    "warming up",
						"headline": map[string]any{"text": "999 ops/s"},
						"blocks":   []any{map[string]any{"kind": "meter", "value": 0.5, "text": "5/10"}},
					},
				},
			},
		},
	}
	_, wfIDs := nvMustResult(t, "wait", waitFallback, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	if _, hasFig := wfIDs["omp.job.tile.figure#0"]; hasFig {
		t.Errorf("non-live widget must not emit omp.job.tile.figure")
	}
	if _, hasMeter := wfIDs["omp.job.tile.meter#0"]; hasMeter {
		t.Errorf("non-live widget must not emit omp.job.tile.meter")
	}
	nvAssertNode(t, wfIDs, "omp.job.tile.lead#0", "text", "warming up")
}

// TestNativePass3M4AndOverflowNotes covers pass-3 M4 native items (explicit
// yield data:null, benign interrupt_skipped default results) and verifies that
// node-budget caps emit explicit `+N more ...` notes rather than silently dropping items.
func TestNativePass3M4AndOverflowNotes(t *testing.T) {
	// M4(6): yield with explicit data: null (key present) renders "null" instead of absent-data notice.
	_, yCallNull := nvMustCall(t, "yield", map[string]any{"data": nil}, sdk.ToolRenderContext{})
	nvAssertNode(t, yCallNull, "omp.tool.yield.data", "lines", "null")

	_, yResNull := nvMustResult(t, "yield", sdk.ToolRenderResult{
		Details: map[string]any{"status": "success"},
	}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{
		Args: map[string]any{"data": nil},
	})
	nvAssertNode(t, yResNull, "omp.tool.yield.data", "lines", "null")
	if _, hasNotice := yResNull["omp.tool.notice"]; hasNotice {
		t.Errorf("yield with explicit data:null should render null, not empty-data notice")
	}

	// M4(5): default renderer treats benign interrupt_skipped results as output (not error).
	skipSynth := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Skipped due to interrupt"}},
		Details: map[string]any{"source": "interrupt_skipped", "__synthetic": true},
	}
	_, skipSynthIDs := nvMustResult(t, "checkpoint", skipSynth, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{IsError: true})
	nvAssertNode(t, skipSynthIDs, "omp.tool.output", "lines", "Skipped due to interrupt")
	if _, isErr := skipSynthIDs["omp.tool.error"]; isErr {
		t.Errorf("benign synthetic interrupt_skipped must not render as omp.tool.error")
	}

	skipStarted := sdk.ToolRenderResult{
		Content: []map[string]any{{"type": "text", "text": "Interrupted after start"}},
		Details: map[string]any{"source": "interrupt_skipped", "__interrupted": true, "execution": "started"},
	}
	_, skipStartedIDs := nvMustResult(t, "rewind", skipStarted, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{IsError: true})
	nvAssertNode(t, skipStartedIDs, "omp.tool.output", "lines", "Interrupted after start")
	if _, isErr := skipStartedIDs["omp.tool.error"]; isErr {
		t.Errorf("benign started interrupt_skipped must not render as omp.tool.error")
	}

	// N7(5): node budget caps emit explicit `+N more ...` notes across todo, task, web_search, wait.
	bigPhases := make([]any, 2000)
	for i := range bigPhases {
		bigPhases[i] = map[string]any{
			"name":  fmt.Sprintf("P%d", i),
			"tasks": []any{map[string]any{"content": "item", "status": "pending"}},
		}
	}
	todoNodes, todoIDs := nvMustResult(t, "todo", sdk.ToolRenderResult{
		Details: map[string]any{"phases": bigPhases},
	}, sdk.ToolRenderResultOptions{Expanded: true}, sdk.ToolRenderContext{})
	if len(todoNodes) >= 4096 {
		t.Fatalf("2000-phase todo produced %d nodes (>= 4096)", len(todoNodes))
	}
	nvAssertNode(t, todoIDs, "omp.tool.stats", "text", "more phases")

	bigAgents := make([]any, 2000)
	for i := range bigAgents {
		bigAgents[i] = map[string]any{"id": fmt.Sprintf("A%d", i), "exitCode": 0, "output": "ok"}
	}
	taskNodes, taskIDs := nvMustResult(t, "task", sdk.ToolRenderResult{
		Details: map[string]any{"results": bigAgents},
	}, sdk.ToolRenderResultOptions{Expanded: true}, sdk.ToolRenderContext{})
	if len(taskNodes) >= 4096 {
		t.Fatalf("2000-agent task produced %d nodes (>= 4096)", len(taskNodes))
	}
	nvAssertNode(t, taskIDs, "omp.tool.stats", "text", "more agents")

	bigSources := make([]any, 2000)
	for i := range bigSources {
		bigSources[i] = map[string]any{"title": fmt.Sprintf("S%d", i), "url": "https://example.com", "snippet": "text"}
	}
	wsNodes, wsIDs := nvMustResult(t, "web_search", sdk.ToolRenderResult{
		Details: map[string]any{"response": map[string]any{"provider": "exa", "sources": bigSources}},
	}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	if len(wsNodes) >= 4096 {
		t.Fatalf("2000-source web_search produced %d nodes (>= 4096)", len(wsNodes))
	}
	nvAssertNode(t, wsIDs, "omp.tool.stats", "text", "more sources")

	bigWaitAgents := make([]any, 4000)
	for i := range bigWaitAgents {
		bigWaitAgents[i] = map[string]any{"id": fmt.Sprintf("W%d", i), "live": true}
	}
	waNodes, waIDs := nvMustResult(t, "wait", sdk.ToolRenderResult{
		Details: map[string]any{"op": "wait", "agents": bigWaitAgents},
	}, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	if len(waNodes) >= 4096 {
		t.Fatalf("4000-agent wait produced %d nodes (>= 4096)", len(waNodes))
	}
	nvAssertNode(t, waIDs, "omp.tool.stats", "text", "more agents")
}
