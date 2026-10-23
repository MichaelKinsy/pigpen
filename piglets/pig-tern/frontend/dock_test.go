package frontend

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

func editorOp(kind fe.OpKind, editor fe.Editor) fe.Op {
	return fe.Op{Kind: kind, Region: fe.RegionDock, ID: "editor", Index: 1, Node: editor}
}

func workingOp(kind fe.OpKind, working fe.Working) fe.Op {
	return fe.Op{Kind: kind, Region: fe.RegionDock, ID: "working", Node: working}
}

func footerOp(kind fe.OpKind, footer fe.Footer) fe.Op {
	return fe.Op{Kind: kind, Region: fe.RegionDock, ID: "footer", Node: footer}
}

// dockWithLines readies a session with a dock node before the editor.
func dockWithLines(t *testing.T) *harness {
	t.Helper()
	h := ready(t)
	h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock", Node: fe.Lines{Lines: []string{"status"}}})
	return h
}

// The editor is a Tern editor in the dock that takes the keys when added.
// Its text and cursor are PiG's and travel as raw ops: typing is a text
// append and a cursor set, with no render; the sendable flag is a render's
// set.
func TestEditorIsAFocusedTernEditor(t *testing.T) {
	h := dockWithLines(t)
	ops := h.applyOps(editorOp(fe.Insert, fe.Editor{Text: "hi", Cursor: 2, Placeholder: "Ask"}))
	if len(ops) != 4 || ops[0].([]any)[0] != "add" || ops[0].([]any)[1] != "dock.editor" || ops[0].([]any)[2] != "dock" {
		t.Fatalf("insert = %#v", ops)
	}
	if n := node(ops, "dock.editor"); n["k"] != "editor" || !reflect.DeepEqual(props(n), map[string]any{"key": "editor", "placeholder": "Ask"}) {
		t.Fatalf("editor node = %#v", n)
	}
	want := jsonOps(t,
		[]any{"text", "dock.editor", "replace", "hi"},
		[]any{"set", "dock.editor", map[string]any{"cursor": 2}},
		[]any{"focus", "dock.editor"},
	)
	if !reflect.DeepEqual(ops[1:], want) {
		t.Fatalf("raw ops = %#v", ops[1:])
	}
	ops = h.applyOps(editorOp(fe.Update, fe.Editor{Text: "hi!", Cursor: 3, Placeholder: "Ask"}))
	if want := jsonOps(t, []any{"text", "dock.editor", "append", "!"}, []any{"set", "dock.editor", map[string]any{"cursor": 3}}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("typing = %#v", ops)
	}
	ops = h.applyOps(editorOp(fe.Update, fe.Editor{Text: "hi!", Cursor: 3, Placeholder: "Ask", Sendable: true}))
	if want := jsonOps(t, []any{"set", "dock.editor", map[string]any{"sendable": true}}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("sendable = %#v", ops)
	}
	// A render after typing never resets the text it does not hold.
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionDock, ID: "dock", Node: fe.Lines{Lines: []string{"other"}}})
	if want := jsonOps(t, []any{"text", "dock.dock", "replace", "other"}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("dock change = %#v", ops)
	}
}

// Tern's edit, undo and send events on the editor become PiG's editor
// actions. An edit computed against text other than the editor's, in UTF-16
// code units, is dropped, and so is an event for another node or surface,
// or one after the editor left the dock. A focus click on the editor gets
// the focus back.
func TestEditorEventsBecomeEditorActions(t *testing.T) {
	h := dockWithLines(t)
	h.applyOps(editorOp(fe.Insert, fe.Editor{Text: "héllo 😀", Cursor: 8}))

	h.event(`{"ev":"edit","sf":"pig","id":"dock.editor","from":0,"to":5,"text":"hey","cursor":3,"len":8}`)
	h.event(`{"ev":"edit","sf":"pig","id":"dock.editor","from":0,"to":5,"text":"stale","cursor":5,"len":7}`)
	h.event(`{"ev":"edit","sf":"pig","id":"dock.dock","from":0,"to":0,"text":"x","cursor":1,"len":8}`)
	h.event(`{"ev":"edit","sf":"other","id":"dock.editor","from":0,"to":0,"text":"x","cursor":1,"len":8}`)
	h.event(`{"ev":"undo","sf":"pig","id":"dock.editor"}`)
	h.event(`{"ev":"send","sf":"pig","id":"dock.editor","text":"/model\nsecond line"}`)
	h.synced()
	if want := []fe.Edit{{From: 0, To: 5, Text: "hey", Cursor: 3, Len: 8}}; !reflect.DeepEqual(h.edits, want) {
		t.Fatalf("edits = %#v", h.edits)
	}
	if h.undos != 1 || !reflect.DeepEqual(h.sends, []string{"/model\nsecond line"}) {
		t.Fatalf("undos = %d, sends = %q", h.undos, h.sends)
	}
	h.messages()
	h.event(`{"ev":"focus","sf":"pig","id":"dock.editor"}`)
	h.synced()
	got := h.frames()
	if len(got) != 1 || !reflect.DeepEqual(got[0].body["ops"], jsonOps(t, []any{"focus", "dock.editor"})) {
		t.Fatalf("focus answer = %#v", got)
	}

	h.applyOps(fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "editor", Index: 1})
	h.event(`{"ev":"send","sf":"pig","id":"dock.editor","text":"late"}`)
	h.event(`{"ev":"undo","sf":"pig","id":"dock.editor"}`)
	h.synced()
	if h.undos != 1 || len(h.sends) != 1 {
		t.Fatalf("after removal: undos = %d, sends = %q", h.undos, h.sends)
	}
}

// The working indicator is a Tern spinner labeled with PiG's message: a new
// message is one set of the label, an unchanged indicator (PiG's spinner
// ticks) sends no frame, a retry takes the warning tone, an extension that
// hides the spinner leaves text, and the end of work deletes the node. Only
// a change between spinner and text renders.
func TestWorkingIsATernSpinner(t *testing.T) {
	h := ready(t)
	working := fe.Working{Kind: fe.WorkingAgent, Message: "Working", Interval: 80}
	ops := h.applyOps(workingOp(fe.Insert, working))
	if n := node(ops, "dock.working"); len(ops) != 1 || n["k"] != "spinner" || !reflect.DeepEqual(props(n), map[string]any{"key": "working", "style": "braille", "label": "Working", "role": "pig.working"}) {
		t.Fatalf("insert = %#v", ops)
	}
	renders := h.session.renders
	h.apply(workingOp(fe.Update, working))
	if got := h.messages(); len(got) != 0 {
		t.Fatalf("an unchanged indicator sent %#v", got)
	}
	working.Message = "Reading \x1b[1mfiles\x1b[22m"
	if ops := h.applyOps(workingOp(fe.Update, working)); !reflect.DeepEqual(ops, jsonOps(t, []any{"set", "dock.working", map[string]any{"label": "Reading files"}})) {
		t.Fatalf("message = %#v", ops)
	}
	working = fe.Working{Kind: fe.WorkingRetry, Message: "Retrying (1/3) in 2s... (escape to cancel)"}
	want := jsonOps(t, []any{"set", "dock.working", map[string]any{"label": "Retrying (1/3) in 2s... (escape to cancel)", "tone": "warning"}})
	if ops := h.applyOps(workingOp(fe.Update, working)); !reflect.DeepEqual(normalized(t, ops), normalized(t, want)) {
		t.Fatalf("retry = %#v", ops)
	}
	if h.session.renders != renders {
		t.Fatalf("label and tone changes rendered %d times", h.session.renders-renders)
	}
	working = fe.Working{Kind: fe.WorkingAgent, Message: "Working", Frames: []string{}}
	ops = h.applyOps(workingOp(fe.Update, working))
	if len(ops) != 2 || ops[0].([]any)[0] != "del" || ops[1].([]any)[4].(map[string]any)["k"] != "text" ||
		!reflect.DeepEqual(props(node(ops, "dock.working")), map[string]any{"key": "working", "role": "pig.working", "text": "Working"}) {
		t.Fatalf("no spinner = %#v", ops)
	}
	working.Message = "Waiting"
	if ops := h.applyOps(workingOp(fe.Update, working)); !reflect.DeepEqual(ops, jsonOps(t, []any{"text", "dock.working", "replace", "Waiting"})) {
		t.Fatalf("text = %#v", ops)
	}
	if ops := h.applyOps(fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "working"}); !reflect.DeepEqual(ops, jsonOps(t, []any{"del", "dock.working"})) {
		t.Fatalf("remove = %#v", ops)
	}
}

// A raw op still waiting for a credit when its node goes is dropped: the
// render's delete goes out first, and Tern rejects ops on unknown ids. So
// is one waiting when the node turns into another kind under its id.
func TestRemovedNodesTakeTheirWaitingRawOps(t *testing.T) {
	for _, change := range []fe.Op{
		{Kind: fe.Remove, Region: fe.RegionDock, ID: "working"},
		workingOp(fe.Update, fe.Working{Kind: fe.WorkingAgent, Message: "Waiting", Frames: []string{}}),
	} {
		h := ready(t)
		working := fe.Working{Kind: fe.WorkingAgent, Message: "Working"}
		h.applyOps(workingOp(fe.Insert, working), editorOp(fe.Insert, fe.Editor{}))
		h.session.mu.Lock()
		h.session.credits = 0
		h.session.mu.Unlock()
		working.Message = "Reading"
		h.apply(workingOp(fe.Update, working), editorOp(fe.Update, fe.Editor{Text: "x", Cursor: 1}))
		h.apply(change)
		h.session.mu.Lock()
		h.session.credits = 64
		h.session.mu.Unlock()
		ops := h.applyOps()
		for _, op := range ops {
			if o := op.([]any); o[0] == "set" && o[1] == "dock.working" {
				t.Fatalf("%v: a raw op on the old spinner went out: %#v", change.Kind, ops)
			}
		}
		if doc := shownBy(ops); doc["dock.editor"]["text"] != "x" {
			t.Fatalf("%v: the editor's text did not go out: %#v", change.Kind, ops)
		}
	}
	// "dock.below" is a sibling of "dock", not its child, though its id
	// extends the other's.
	h := ready(t)
	lines := func(kind fe.OpKind, id, text string) fe.Op {
		return fe.Op{Kind: kind, Region: fe.RegionDock, ID: id, Node: fe.Lines{Lines: []string{text}}}
	}
	h.applyOps(lines(fe.Insert, "dock", "a"), lines(fe.Insert, "dock.below", "b"))
	h.session.mu.Lock()
	h.session.credits = 0
	h.session.mu.Unlock()
	h.apply(lines(fe.Update, "dock.below", "c"), fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "dock"})
	h.session.mu.Lock()
	h.session.credits = 64
	h.session.mu.Unlock()
	if doc := shownBy(h.applyOps()); doc["dock.dock.below"]["text"] != "c" {
		t.Fatalf("the sibling's text was dropped: %#v", doc)
	}
}

// The dock's lines are an ansi node added with its text; later text and
// visibility are raw ops: grown text is appended, other text replaced, and
// blank lines hide it, all without a render.
func TestDockLinesAreRawText(t *testing.T) {
	h := ready(t)
	lines := func(kind fe.OpKind, lines ...string) fe.Op {
		return fe.Op{Kind: kind, Region: fe.RegionDock, ID: "dock", Node: fe.Lines{Lines: lines}}
	}
	ops := h.applyOps(lines(fe.Insert, "a"))
	if len(ops) != 1 || !reflect.DeepEqual(props(node(ops, "dock.dock")), map[string]any{"key": "dock", "text": "a"}) {
		t.Fatalf("insert = %#v", ops)
	}
	renders := h.session.renders
	for _, tc := range []struct {
		lines []string
		want  []any
	}{
		{[]string{"a", "b"}, jsonOps(t, []any{"text", "dock.dock", "append", "\nb"})},
		{[]string{"a", "b"}, nil},
		{[]string{"c"}, jsonOps(t, []any{"text", "dock.dock", "replace", "c"})},
		{[]string{" "}, jsonOps(t, []any{"text", "dock.dock", "replace", ""}, []any{"set", "dock.dock", map[string]any{"hidden": true}})},
		{[]string{"d"}, jsonOps(t, []any{"text", "dock.dock", "replace", "d"}, []any{"set", "dock.dock", map[string]any{"hidden": nil}})},
	} {
		if ops := h.applyOps(lines(fe.Update, tc.lines...)); !reflect.DeepEqual(ops, tc.want) {
			t.Fatalf("%q: ops = %#v, want %#v", tc.lines, ops, tc.want)
		}
	}
	if h.session.renders != renders {
		t.Fatalf("line changes rendered %d times", h.session.renders-renders)
	}
}

func sampleFooter() fe.Footer {
	rate := 60.0
	return fe.Footer{
		Cwd:                "~/src/pig",
		GitBranch:          "main",
		UsageTotals:        fe.UsageTotals{Input: 1250, Output: 340, CacheRead: 900, CacheWrite: 10, Cost: 0.0125},
		LatestCacheHitRate: &rate,
		UsingSubscription:  true,
		ContextUsage:       fe.ContextUsage{Tokens: 50_000, Percent: 25, ContextWindow: 200_000},
		AutoCompact:        true,
		Model:              "claude",
		Provider:           "anthropic",
		ThinkingLevel:      "high",
		Routed:             fe.RoutedModel{Model: "haiku", ThinkingLevel: "low"},
		ExtensionStatuses:  []string{"\x1b[32mlint ok\x1b[39m"},
	}
}

// The footer is a Tern status strip: Pi's first line and the extension
// statuses on the left, its usage, context and model on the right, in Pi's
// words, with a context meter and the thinking level's effort glyph. One
// add carries it whole.
func TestFooterIsATernStatusStrip(t *testing.T) {
	h := ready(t)
	ops := h.applyOps(footerOp(fe.Insert, sampleFooter()))
	n := node(ops, "dock.footer")
	if len(ops) != 1 || n["k"] != "status" || props(n)["role"] != "pig.footer" {
		t.Fatalf("footer = %#v", ops)
	}
	var order []string
	for _, child := range n["c"].([]any) {
		order = append(order, child.(map[string]any)["id"].(string))
	}
	if got := strings.Join(order, ","); got != "dock.footer.cwd,dock.footer.branch,dock.footer.session,dock.footer.status.0,dock.footer.usage,dock.footer.context,dock.footer.xp,dock.footer.model" {
		t.Fatalf("segments = %s", got)
	}
	doc := shownBy(ops)
	for id, want := range map[string]map[string]any{
		"cwd": {"icon": "folder", "spans": []any{map[string]any{"t": "~/src/pig", "s": "path dim"}}, "priority": 7, "min": map[string]any{"w": "12ch"},
			"href": "~/src/pig", "actions": map[string]any{"click": "open", "menu": []any{"open", "copy"}}},
		"branch":   {"icon": "branch", "text": "main", "priority": 5},
		"session":  {"icon": "message", "priority": 3, "hidden": true},
		"status.0": {"text": "lint ok", "priority": 2},
		"usage":    {"side": "right", "text": "↑1.3k ↓340 R900 W10 CH60.0% $0.013 (sub)", "priority": 6},
		"context":  {"side": "right", "text": "25.0%/200k (auto)", "priority": 8},
		"xp":       {"side": "right", "text": "xp", "tone": "warning", "priority": 1, "hidden": true},
		"model":    {"side": "right", "icon": "model", "text": "(anthropic) claude • high → haiku • low", "priority": 9, "min": map[string]any{"w": "12ch"}},
	} {
		want["key"] = id
		if got := doc["dock.footer."+id]; !reflect.DeepEqual(normalized(t, got), normalized(t, want)) {
			t.Fatalf("%s = %#v, want %#v", id, got, want)
		}
	}
	if meter := node(ops, "dock.footer.context.meter"); meter["k"] != "meter" || doc["dock.footer.context.meter"]["value"] != 0.25 {
		t.Fatalf("meter = %#v", doc["dock.footer.context.meter"])
	}
	if effort := node(ops, "dock.footer.model.effort"); effort["k"] != "effort" || !reflect.DeepEqual(doc["dock.footer.model.effort"], map[string]any{"key": "effort", "level": "high"}) {
		t.Fatalf("effort = %#v", doc["dock.footer.model.effort"])
	}
}

// A change of the footer's data is a set of the props it changes, sent raw
// with no render: the thinking level sets the model text and the effort
// glyph, usage sets its segment, a fuller context colors its segment and
// meter, and an unknown size clears the meter's value. A new extension
// status renders a segment with its text; one removed and added again gets
// its text anew.
func TestFooterChangesAreSets(t *testing.T) {
	h := ready(t)
	footer := sampleFooter()
	h.applyOps(footerOp(fe.Insert, footer))
	renders := h.session.renders
	for _, tc := range []struct {
		name   string
		change func(*fe.Footer)
		want   []any
	}{
		{"thinking", func(f *fe.Footer) { f.ThinkingLevel = "off" }, []any{
			[]any{"set", "dock.footer.model", map[string]any{"text": "(anthropic) claude • thinking off → haiku • low"}},
			[]any{"set", "dock.footer.model.effort", map[string]any{"level": "off"}},
		}},
		{"no reasoning", func(f *fe.Footer) { f.ThinkingLevel, f.Provider, f.Routed = "", "", fe.RoutedModel{} }, []any{
			[]any{"set", "dock.footer.model", map[string]any{"text": "claude"}},
			[]any{"set", "dock.footer.model.effort", map[string]any{"level": nil, "hidden": true}},
		}},
		{"usage", func(f *fe.Footer) { f.UsageTotals.Output = 12_000 }, []any{
			[]any{"set", "dock.footer.usage", map[string]any{"text": "↑1.3k ↓12k R900 W10 CH60.0% $0.013 (sub)"}},
		}},
		{"zero cost", func(f *fe.Footer) { f.UsageTotals.Cost = 0 }, []any{
			[]any{"set", "dock.footer.usage", map[string]any{"text": "↑1.3k ↓12k R900 W10 CH60.0% $0.000 (sub)"}},
		}},
		{"no subscription", func(f *fe.Footer) { f.UsingSubscription = false }, []any{
			[]any{"set", "dock.footer.usage", map[string]any{"text": "↑1.3k ↓12k R900 W10 CH60.0%"}},
		}},
		{"outside a repository", func(f *fe.Footer) { f.GitBranch = "" }, []any{
			[]any{"set", "dock.footer.branch", map[string]any{"text": nil, "hidden": true}},
		}},
		// Pi's footer.ts colors the context warning above 70% and error above
		// 90%, so exactly 70 and exactly 90 keep the tone below them.
		{"context at 70", func(f *fe.Footer) {
			f.ContextUsage = fe.ContextUsage{Tokens: 140_000, Percent: 70, ContextWindow: 200_000}
		}, []any{
			[]any{"set", "dock.footer.context", map[string]any{"text": "70.0%/200k (auto)"}},
			[]any{"set", "dock.footer.context.meter", map[string]any{"value": 0.7}},
		}},
		{"context above 70", func(f *fe.Footer) {
			f.ContextUsage = fe.ContextUsage{Tokens: 142_000, Percent: 71, ContextWindow: 200_000}
		}, []any{
			[]any{"set", "dock.footer.context", map[string]any{"text": "71.0%/200k (auto)", "tone": "warning"}},
			[]any{"set", "dock.footer.context.meter", map[string]any{"value": 0.71, "tone": "warning"}},
		}},
		{"context at 90", func(f *fe.Footer) {
			f.ContextUsage = fe.ContextUsage{Tokens: 180_000, Percent: 90, ContextWindow: 200_000}
		}, []any{
			[]any{"set", "dock.footer.context", map[string]any{"text": "90.0%/200k (auto)"}},
			[]any{"set", "dock.footer.context.meter", map[string]any{"value": 0.9}},
		}},
		{"context above 90", func(f *fe.Footer) {
			f.ContextUsage = fe.ContextUsage{Tokens: 182_000, Percent: 91, ContextWindow: 200_000}
		}, []any{
			[]any{"set", "dock.footer.context", map[string]any{"text": "91.0%/200k (auto)", "tone": "error"}},
			[]any{"set", "dock.footer.context.meter", map[string]any{"value": 0.91, "tone": "error"}},
		}},
		{"unknown", func(f *fe.Footer) { f.ContextUsage = fe.ContextUsage{Unknown: true, ContextWindow: 200_000} }, []any{
			[]any{"set", "dock.footer.context", map[string]any{"text": "?/200k (auto)", "tone": nil}},
			[]any{"set", "dock.footer.context.meter", map[string]any{"value": nil, "tone": nil}},
		}},
		{"experimental", func(f *fe.Footer) { f.Experimental = true }, []any{
			[]any{"set", "dock.footer.xp", map[string]any{"hidden": nil}},
		}},
		{"session name", func(f *fe.Footer) { f.SessionName = "refactor" }, []any{
			[]any{"set", "dock.footer.session", map[string]any{"text": "refactor", "hidden": nil}},
		}},
		// The folder is a path span linked to the folder, which a click opens
		// in Tern's Files pane; a new folder replaces both, a literal "%"
		// escaped because Tern decodes %XX in a link.
		{"folder", func(f *fe.Footer) { f.Cwd = "~/src/100%" }, []any{
			[]any{"set", "dock.footer.cwd", map[string]any{"spans": []any{map[string]any{"t": "~/src/100%", "s": "path dim"}}, "href": "~/src/100%25"}},
		}},
		{"status", func(f *fe.Footer) { f.ExtensionStatuses = []string{"lint failed"} }, []any{
			[]any{"set", "dock.footer.status.0", map[string]any{"text": "lint failed"}},
		}},
		{"unchanged", func(*fe.Footer) {}, nil},
	} {
		tc.change(&footer)
		if got := h.applyOps(footerOp(fe.Update, footer)); !reflect.DeepEqual(normalized(t, got), normalized(t, tc.want)) {
			t.Fatalf("%s: ops = %#v, want %#v", tc.name, got, tc.want)
		}
	}
	if h.session.renders != renders {
		t.Fatalf("footer data changes rendered %d times", h.session.renders-renders)
	}
	// The render adds the new segment with its text, and resends none of
	// the values changed above: the segments kept keep their views.
	footer.ExtensionStatuses = []string{"lint failed", "two"}
	want := jsonOps(t,
		[]any{"add", "dock.footer.status.1", "dock.footer", "dock.footer.usage", map[string]any{"id": "dock.footer.status.1", "k": "seg", "p": map[string]any{"key": "status.1", "priority": 2, "text": "two"}}},
	)
	if ops := h.applyOps(footerOp(fe.Update, footer)); !reflect.DeepEqual(normalized(t, ops), normalized(t, want)) {
		t.Fatalf("new status = %#v", ops)
	}
	footer.ExtensionStatuses = footer.ExtensionStatuses[:1]
	if ops := h.applyOps(footerOp(fe.Update, footer)); !reflect.DeepEqual(ops, jsonOps(t, []any{"del", "dock.footer.status.1"})) {
		t.Fatalf("removed status = %#v", ops)
	}
	footer.ExtensionStatuses = []string{"lint failed", "two"}
	if ops := h.applyOps(footerOp(fe.Update, footer)); !reflect.DeepEqual(normalized(t, ops), normalized(t, want)) {
		t.Fatalf("status added again = %#v", ops)
	}
	if h.session.renders != renders+3 {
		t.Fatalf("status count changes rendered %d times, want 3", h.session.renders-renders)
	}
}

// normalized round-trips through JSON and sorts nothing: maps compare by
// content, numbers as float64.
func normalized(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// formatTokens and toFixed round as Pi's JavaScript does: an exact tie goes
// up, where Go's formatting rounds it to even.
func TestFormatTokensAndToFixedMatchPi(t *testing.T) {
	for count, want := range map[int]string{
		0: "0", 999: "999", 1000: "1.0k", 1250: "1.3k", 9999: "10.0k", 10_000: "10k", 15_500: "16k",
		999_499: "999k", 999_500: "1000k", 1_000_000: "1.0M", 1_250_000: "1.3M", 12_500_000: "13M",
	} {
		if got := formatTokens(count); got != want {
			t.Errorf("formatTokens(%d) = %q, want %q", count, got, want)
		}
	}
	for _, tc := range []struct {
		x      float64
		digits int
		want   string
	}{
		{0.0625, 3, "0.063"}, {12.25, 1, "12.3"}, {0.0125, 3, "0.013"}, {0, 3, "0.000"}, {1.005, 2, "1.00"},
		{-0.0625, 3, "-0.063"}, {-0.0001, 3, "0.000"}, {99.95, 1, "100.0"}, {math.NaN(), 3, "NaN"},
	} {
		if got := toFixed(tc.x, tc.digits); got != tc.want {
			t.Errorf("toFixed(%v, %d) = %q, want %q", tc.x, tc.digits, got, tc.want)
		}
	}
	if got := stripANSI("\x1b[32mok\x1b[39m \x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\"); got != "ok link" {
		t.Errorf("stripANSI = %q", got)
	}
}

// Startup stays within the credits with the dock's status nodes: footer and
// working changes before PiG's first frame after InputReady fold into it,
// with the sendable flip, and nothing goes out on a node removed meanwhile.
func TestHiddenStartupWithStatusNodesStaysTwoFrames(t *testing.T) {
	h := newHarness(t)
	h.reply(`,"credits":2`)
	h.messages()
	footer := sampleFooter()
	first := []fe.Op{
		{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock", Index: 0, Node: fe.Lines{Lines: []string{""}}},
		{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 1, Node: fe.Editor{}},
		{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock.below", Index: 2, Node: fe.Lines{}},
		{Kind: fe.Insert, Region: fe.RegionDock, ID: "footer", Index: 3, Node: footer},
	}
	footer.Model = "gpt-5"
	working := fe.Working{Kind: fe.WorkingAgent, Message: "Working"}
	h.apply(first...)
	h.apply(footerOp(fe.Update, footer))
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "working", Index: 1, Node: working})
	h.apply(fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "working", Index: 1})
	h.session.InputReady()
	h.apply(fe.Op{Kind: fe.Update, Region: fe.RegionDock, ID: "editor", Index: 1, Node: fe.Editor{Sendable: true}})
	got := h.frames()
	if len(got) == 0 || len(got) > 2 {
		t.Fatalf("startup frames = %d, want one or two", len(got))
	}
	first0, _ := json.Marshal(got[0].body)
	all, _ := json.Marshal(got)
	if !strings.Contains(string(first0), `"sendable":true`) || !strings.Contains(string(first0), `gpt-5`) ||
		strings.Contains(string(all), `claude`) || strings.Contains(string(all), `dock.working`) {
		t.Fatalf("frames = %s, want the footer change and the sendable flip first, and nothing on the working node", all)
	}
}
