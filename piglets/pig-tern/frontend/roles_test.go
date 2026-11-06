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
	if n := node(ops, "n.th"); n["k"] != "section" || props(n)["role"] != "omp.thinking" || props(n)["collapsible"] != true {
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
	row := node(ops, "dock.working")
	if row["k"] != "row" || props(row)["role"] != "omp.working" {
		t.Fatalf("working = %#v", ops)
	}
	kids := row["c"].([]any)
	if len(kids) != 2 || kids[0].(map[string]any)["k"] != "spinner" || props(kids[0].(map[string]any))["style"] != "starburst" ||
		kids[1].(map[string]any)["id"] != "dock.working.label" || props(kids[1].(map[string]any))["text"] != "Working" {
		t.Fatalf("working children = %#v", kids)
	}
	working.Message = "Reading files"
	ops = h.applyOps(workingOp(fe.Update, working))
	if want := jsonOps(t, []any{"set", "dock.working.label", map[string]any{"text": "Reading files"}}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("working update = %#v, want %#v", ops, want)
	}

	ops = h.applyOps(footerOp(fe.Insert, fe.Footer{Cwd: "~/src", GitBranch: "main", Model: "m"}))
	footer := node(ops, "dock.footer")
	if props(footer)["role"] != "omp.status" {
		t.Fatalf("footer = %#v", footer)
	}
	roles := map[string]any{}
	for _, c := range footer["c"].([]any) {
		seg := c.(map[string]any)
		roles[seg["id"].(string)] = props(seg)["role"]
	}
	if roles["dock.footer.cwd"] != "omp.status.path" || roles["dock.footer.branch"] != "omp.status.git" || roles["dock.footer.model"] != "omp.status.model" || roles["dock.footer.xp"] != nil {
		t.Fatalf("footer segment roles = %#v", roles)
	}
}
