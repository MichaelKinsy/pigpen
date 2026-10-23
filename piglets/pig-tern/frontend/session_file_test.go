package frontend

import (
	"reflect"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

func (h *harness) applyFrameOps(frame fe.Frame) []any {
	h.t.Helper()
	if err := h.session.Apply(frame); err != nil {
		h.t.Fatal(err)
	}
	var all []any
	for _, f := range h.frames() {
		all = append(all, f.body["ops"].([]any)...)
	}
	return all
}

// The first frame's session file becomes a hidden node in main, ahead of
// the transcript; a change is one set of its href, which a session that is
// not persisted lacks, and a frame without one leaves it alone.
func TestSessionFileIsAHiddenNodeAheadOfTheTranscript(t *testing.T) {
	h := ready(t)
	a, none, b := "/sessions/a.jsonl", "", "/sessions/b.jsonl"
	ops := h.applyFrameOps(fe.Frame{SessionFile: &a, Ops: []fe.Op{
		{Kind: fe.Insert, Region: fe.RegionMain, ID: "x", Index: 0, Node: fe.Lines{Lines: []string{"x"}}},
	}})
	want := jsonOps(t,
		[]any{"add", "pig.session", "main", nil, map[string]any{"id": "pig.session", "k": "text", "p": map[string]any{"hidden": true, "role": "pig.session", "text": "", "href": a}}},
		[]any{"add", "n.x", "main", nil, map[string]any{"id": "n.x", "k": "ansi", "p": map[string]any{"hidden": false, "text": "x"}}},
	)
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("first frame = %#v, want %#v", ops, want)
	}
	// A node PiG puts first goes before the transcript, after the session
	// node, and settles: a node follows it. The session node never settles.
	ops = h.applyFrameOps(fe.Frame{Ops: []fe.Op{{Kind: fe.Insert, Region: fe.RegionMain, ID: "w", Index: 0, Node: fe.Lines{Lines: []string{"w"}}}}})
	if len(ops) != 2 || ops[0].([]any)[3] != "n.x" || !reflect.DeepEqual(ops[1], []any{"settle", "n.w"}) || len(opsOn(ops, "pig.session")) != 0 {
		t.Fatalf("insert at the top = %#v", ops)
	}
	for _, tc := range []struct {
		file *string
		want []any
	}{
		{&none, jsonOps(t, []any{"set", "pig.session", map[string]any{"href": nil}})},
		{&b, jsonOps(t, []any{"set", "pig.session", map[string]any{"href": b}})},
	} {
		if ops := h.applyFrameOps(fe.Frame{SessionFile: tc.file}); !reflect.DeepEqual(ops, tc.want) {
			t.Fatalf("switch to %q = %#v, want %#v", *tc.file, ops, tc.want)
		}
	}
}

// A session file that arrives after the transcript still goes ahead of it.
func TestLateSessionFileGoesAheadOfTheTranscript(t *testing.T) {
	h := ready(t)
	h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "x", Index: 0, Node: fe.Lines{Lines: []string{"x"}}})
	file := "/sessions/a.jsonl"
	ops := h.applyFrameOps(fe.Frame{SessionFile: &file})
	if len(ops) != 1 || ops[0].([]any)[0] != "add" || ops[0].([]any)[1] != "pig.session" || ops[0].([]any)[3] != "n.x" {
		t.Fatalf("late session file = %#v", ops)
	}
}
