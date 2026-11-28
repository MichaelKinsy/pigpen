package frontend

import (
	"reflect"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

func lastAddNode(ops []any, id string) map[string]any {
	var found map[string]any
	for _, raw := range ops {
		op, ok := raw.([]any)
		if !ok || len(op) < 5 || op[0] != "add" {
			continue
		}
		if n, ok := op[4].(map[string]any); ok && n["id"] == id {
			found = n
		}
	}
	return found
}

func TestOmpReadGroupCoalescesThreeOrMoreConsecutiveReads(t *testing.T) {
	h := readyOmpRoles(t)
	h.applyOps(footerOp(fe.Insert, fe.Footer{Cwd: "/tmp/work"}))

	// 1. Single read remains an individual inline omp.tool.read row (no group, no children).
	ops1 := h.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionMain, ID: "r1", Index: 0,
		Node: fe.ToolCard{Name: "read", Arguments: map[string]any{"path": "a.go:1-10"}, Status: fe.ToolDone},
	})
	r1 := lastAddNode(ops1, "n.r1")
	if r1 == nil || props(r1)["role"] != "omp.tool.read" || props(r1)["target"] != "a.go:1-10" || props(r1)["targetKind"] != "path" || r1["c"] != nil {
		t.Fatalf("single read = %#v, want standalone inline read", r1)
	}

	// 2. Two consecutive reads stay individual (below 3-read coalescing threshold).
	ops2 := h.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionMain, ID: "r2", Index: 1,
		Node: fe.ToolCard{Name: "read", Arguments: map[string]any{"path": "pkg/b.go"}, Status: fe.ToolDone},
	})
	if r2 := lastAddNode(ops2, "n.r2"); r2 == nil || props(r2)["target"] != "pkg/b.go" || r2["c"] != nil {
		t.Fatalf("second read = %#v, want standalone inline read", r2)
	}

	// 3. Third consecutive compact filesystem read coalesces all 3 into leader n.r1.
	ops3 := h.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionMain, ID: "r3", Index: 2,
		Node: fe.ToolCard{Name: "read", Arguments: map[string]any{"path": "missing.go"}, Status: fe.ToolError, Output: "Path 'missing.go' not found"},
	})
	grp := lastAddNode(ops3, "n.r1")
	if grp == nil || grp["k"] != "tool" ||
		props(grp)["role"] != "omp.tool.read" ||
		props(grp)["title"] != "Read" ||
		props(grp)["target"] != "3 files" ||
		props(grp)["targetKind"] != "text" ||
		props(grp)["frame"] != "inline" ||
		props(grp)["status"] != "error" ||
		props(grp)["tone"] != "error" ||
		props(grp)["collapsible"] != false {
		t.Fatalf("coalesced 3-read group = %#v", grp)
	}
	children, _ := grp["c"].([]any)
	if len(children) != 3 {
		t.Fatalf("expected 3 omp.tool.file rows, got %d: %#v", len(children), children)
	}
	for i, rawChild := range children {
		row, _ := rawChild.(map[string]any)
		if row == nil || row["k"] != "row" || props(row)["role"] != "omp.tool.file" {
			t.Fatalf("row[%d] = %#v, want row[omp.tool.file]", i, row)
		}
	}
	// Trailing members are replaced by hidden spacers so TSP IDs stay stable.
	for _, hiddenID := range []string{"n.r2", "n.r3"} {
		sp := lastAddNode(ops3, hiddenID)
		if sp == nil || sp["k"] != "spacer" || props(sp)["hidden"] != true {
			t.Fatalf("follower %s = %#v, want hidden spacer", hiddenID, sp)
		}
	}

	// 4. Updating a follower (e.g. missing.go recovers or changes status) updates the group.
	ops4 := h.applyOps(fe.Op{
		Kind: fe.Update, Region: fe.RegionMain, ID: "r3", Index: 2,
		Node: fe.ToolCard{Name: "read", Arguments: map[string]any{"path": "c.go"}, Status: fe.ToolDone},
	})
	if grp4 := lastAddNode(ops4, "n.r1"); grp4 != nil {
		if props(grp4)["status"] != "done" || props(grp4)["tone"] != nil {
			t.Fatalf("updated group = %#v, want status=done tone=nil", grp4)
		}
	}

	// 5. Removing one member drops the run to 2 and restores both surviving reads standalone.
	ops5 := h.applyOps(fe.Op{Kind: fe.Remove, Region: fe.RegionMain, ID: "r3", Index: 2})
	restored1 := lastAddNode(ops5, "n.r1")
	restored2 := lastAddNode(ops5, "n.r2")
	if restored1 == nil || props(restored1)["target"] != "a.go:1-10" || restored1["c"] != nil {
		t.Fatalf("restored r1 = %#v, want standalone inline read", restored1)
	}
	if restored2 == nil || props(restored2)["target"] != "pkg/b.go" || restored2["c"] != nil {
		t.Fatalf("restored r2 = %#v, want standalone inline read", restored2)
	}
}

func TestOmpReadGroupSkipsNonFilesystemAndInterruptedRuns(t *testing.T) {
	h := readyOmpRoles(t)

	// proc:// and cfg:// reads are NOT compact filesystem reads and break a run.
	ops := h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "r1", Index: 0, Node: fe.ToolCard{Name: "read", Arguments: map[string]any{"path": "a.go"}, Status: fe.ToolDone}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "r2", Index: 1, Node: fe.ToolCard{Name: "read", Arguments: map[string]any{"path": "proc://"}, Status: fe.ToolDone}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "r3", Index: 2, Node: fe.ToolCard{Name: "read", Arguments: map[string]any{"path": "b.go"}, Status: fe.ToolDone}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "r4", Index: 3, Node: fe.ToolCard{Name: "read", Arguments: map[string]any{"path": "c.go"}, Status: fe.ToolDone}},
	)
	if n := lastAddNode(ops, "n.r1"); n == nil || props(n)["target"] != "a.go" {
		t.Fatalf("interrupted r1 = %#v, want standalone a.go", n)
	}
	if n := lastAddNode(ops, "n.r3"); n == nil || props(n)["target"] != "b.go" {
		t.Fatalf("2-read tail r3 = %#v, want standalone b.go", n)
	}
}

func TestOmpOutReplyWriteRendersAsAssistantProse(t *testing.T) {
	h := readyOmpRoles(t)
	ops := h.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionMain, ID: "out1", Index: 0,
		Node: fe.ToolCard{
			Name:      "write",
			Arguments: map[string]any{"path": "out://", "content": "## Update\n\nHalfway done."},
			Status:    fe.ToolDone,
			Output:    "Shown to the user.",
		},
	})
	n := lastAddNode(ops, "n.out1")
	if n == nil || n["k"] != "col" || props(n)["role"] != "omp.assistant" {
		t.Fatalf("write out:// node = %#v, want col[omp.assistant]", n)
	}
	children, _ := n["c"].([]any)
	if len(children) != 1 {
		t.Fatalf("write out:// children = %#v, want 1 md child", children)
	}
	mdChild, _ := children[0].(map[string]any)
	if mdChild == nil || mdChild["k"] != "md" || props(mdChild)["text"] != "## Update\n\nHalfway done." {
		t.Fatalf("write out:// md child = %#v", mdChild)
	}

	// Regular write stays a tool[omp.tool.write] card.
	ops2 := h.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionMain, ID: "w1", Index: 1,
		Node: fe.ToolCard{
			Name:      "write",
			Arguments: map[string]any{"path": "generated.txt", "content": "alpha\nbeta\n"},
			Status:    fe.ToolDone,
		},
	})
	w := lastAddNode(ops2, "n.w1")
	if w == nil || w["k"] != "tool" || props(w)["role"] != "omp.tool.write" ||
		props(w)["target"] != "generated.txt" || !reflect.DeepEqual(props(w)["meta"], []any{"3 lines"}) {
		t.Fatalf("regular write card = %#v", w)
	}
}
