package frontend

import (
	"reflect"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// readyOmpRoles is ready with the omp role trial on.
func readyOmpRoles(t *testing.T) *harness {
	t.Helper()
	return readyEnv(t, map[string]string{ompRolesEnv: "omp"})
}

// In the omp role trial, transcript nodes take omp's roles and shapes, and
// streamed assistant text patches the md inside the omp.assistant col.
func TestOmpRolesTranscript(t *testing.T) {
	h := readyOmpRoles(t)
	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "u", Node: fe.MarkdownText{Role: fe.RoleUser, Text: "hi"}})
	if n := node(ops, "n.u"); n["k"] != "card" || props(n)["role"] != "omp.user" || props(n)["tone"] != "user" {
		t.Fatalf("user = %#v", ops)
	}

	ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Index: 1, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "Hel", Streaming: true}})
	want := jsonOps(t, []any{"add", "n.a", "main", nil, map[string]any{"id": "n.a", "k": "col", "p": map[string]any{"role": "omp.assistant"}, "c": []any{
		map[string]any{"id": "n.a.md", "k": "md", "p": map[string]any{"text": "Hel", "stream": true}},
	}}})
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("assistant =\n%#v\nwant\n%#v", ops, want)
	}
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "a", Index: 1, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "Hello"}})
	want = jsonOps(t,
		[]any{"text", "n.a.md", "append", "lo"},
		[]any{"set", "n.a.md", map[string]any{"stream": false}},
		[]any{"settle", "n.a"},
	)
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("assistant update =\n%#v\nwant\n%#v", ops, want)
	}

	ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "th", Index: 2, Node: fe.Thinking{Text: "hmm", Streaming: true}})
	if n := node(ops, "n.th"); n["k"] != "section" || props(n)["role"] != "omp.thinking.live" || props(n)["collapsible"] != true {
		t.Fatalf("thinking = %#v", ops)
	}
}

// In the omp role trial, the working indicator is a row with role
// omp.working holding a starburst spinner and the label, whose message
// changes go to the label; the footer is omp's status strip.
func TestOmpRolesDock(t *testing.T) {
	h := readyOmpRoles(t)
	working := fe.Working{Kind: fe.WorkingAgent, Message: "Working"}
	ops := h.applyOps(workingOp(fe.Insert, working))
	act := node(ops, "dock.activity")
	if act == nil || act["k"] != "row" || props(act)["role"] != "omp.hud.activity" {
		t.Fatalf("activity = %#v", ops)
	}
	statusCol := node(ops, "dock.activity.status")
	if statusCol == nil || statusCol["k"] != "col" || props(statusCol)["role"] != "omp.hud.status" {
		t.Fatalf("status col = %#v", ops)
	}
	row := node(ops, "dock.activity.status.working")
	if row == nil || row["k"] != "row" || props(row)["role"] != "omp.working" {
		t.Fatalf("working = %#v", ops)
	}
	kids := row["c"].([]any)
	if len(kids) != 5 || kids[0].(map[string]any)["k"] != "el" || props(kids[0].(map[string]any))["tag"] != "span" || props(kids[0].(map[string]any))["role"] != "omp.working.pig" ||
		kids[1].(map[string]any)["k"] != "elapsed" ||
		kids[2].(map[string]any)["id"] != "dock.activity.status.working.sep" || props(kids[2].(map[string]any))["role"] != "omp.working.sep" ||
		kids[3].(map[string]any)["id"] != "dock.activity.status.working.label" || kids[3].(map[string]any)["k"] != "shimmer" ||
		!reflect.DeepEqual(props(kids[3].(map[string]any))["spans"], []any{map[string]any{"t": "Working", "s": "muted"}}) ||
		kids[4].(map[string]any)["id"] != "dock.activity.status.working.fill" {
		t.Fatalf("working children = %#v", kids)
	}
	working.Message = "Reading files"
	ops = h.applyOps(workingOp(fe.Update, working))
	if want := jsonOps(t, []any{"set", "dock.activity.status.working.label", map[string]any{"spans": []any{map[string]any{"t": "Reading files", "s": "muted"}}}}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("working update = %#v, want %#v", ops, want)
	}

	// Under ompRoles, dock.footer is always suppressed (matching omp's
	// status-line/component.ts:3810: "on a TSP terminal, which draws no status
	// strip"); its facts feed the composer bar once the editor mounts.
	ops = h.applyOps(footerOp(fe.Insert, fe.Footer{Cwd: "~/src", GitBranch: "main", Model: "m"}))
	if footer := node(ops, "dock.footer"); footer != nil {
		t.Fatalf("dock.footer must remain suppressed under ompRoles, got %#v", footer)
	}

	ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 1, Node: fe.Editor{Sendable: true}})
	if node(ops, "dock.editor.editor") == nil || props(node(ops, "dock.editor.editor"))["role"] != "omp.editor" {
		t.Fatalf("composer = %#v", ops)
	}
	if got := props(node(ops, "dock.editor.editor.bar.model.name"))["spans"]; !reflect.DeepEqual(got, []any{map[string]any{"t": "m", "s": "statusLineModel"}}) {
		t.Fatalf("composer model spans = %#v", got)
	}
}
