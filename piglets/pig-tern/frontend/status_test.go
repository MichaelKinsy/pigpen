package frontend

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

func workingOp(kind fe.OpKind, working fe.Working) fe.Op {
	return fe.Op{Kind: kind, Region: fe.RegionDock, ID: "working", Node: working}
}

func footerOp(kind fe.OpKind, footer fe.Footer) fe.Op {
	return fe.Op{Kind: kind, Region: fe.RegionDock, ID: "footer", Node: footer}
}

// The working indicator is a Tern spinner labeled with PiG's message: a new
// message is one set of the label, an unchanged indicator (PiG's spinner
// ticks) sends no frame, a retry takes the warning tone, an extension that
// hides the spinner leaves text, and the end of work deletes the node.
func TestWorkingIsATernSpinner(t *testing.T) {
	h := ready(t)
	working := fe.Working{Kind: fe.WorkingAgent, Message: "Working", Interval: 80}
	ops := applyOps(t, h, workingOp(fe.Insert, working))
	want := []any{[]any{"add", "n.working", "dock", nil, map[string]any{"id": "n.working", "k": "spinner", "p": map[string]any{"style": "braille", "label": "Working", "role": "pig.working"}}}}
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("insert = %#v", ops)
	}
	if err := h.session.Apply(fe.Frame{Ops: []fe.Op{workingOp(fe.Update, working)}}); err != nil {
		t.Fatal(err)
	}
	if got := messages(t, h.out); len(got) != 0 {
		t.Fatalf("an unchanged indicator sent %#v", got)
	}
	working.Message = "Reading \x1b[1mfiles\x1b[22m"
	if ops := applyOps(t, h, workingOp(fe.Update, working)); !reflect.DeepEqual(ops, []any{[]any{"set", "n.working", map[string]any{"label": "Reading files"}}}) {
		t.Fatalf("message = %#v", ops)
	}
	working = fe.Working{Kind: fe.WorkingRetry, Message: "Retrying (1/3) in 2s... (escape to cancel)"}
	want = []any{[]any{"set", "n.working", map[string]any{"label": "Retrying (1/3) in 2s... (escape to cancel)", "tone": "warning"}}}
	if ops := applyOps(t, h, workingOp(fe.Update, working)); !reflect.DeepEqual(ops, want) {
		t.Fatalf("retry = %#v", ops)
	}
	working = fe.Working{Kind: fe.WorkingAgent, Message: "Working", Frames: []string{}}
	ops = applyOps(t, h, workingOp(fe.Update, working))
	if len(ops) != 2 || ops[0].([]any)[0] != "del" || ops[1].([]any)[4].(map[string]any)["k"] != "text" {
		t.Fatalf("no spinner = %#v", ops)
	}
	if ops := applyOps(t, h, fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "working", Index: 0}); !reflect.DeepEqual(ops, []any{[]any{"del", "n.working"}}) {
		t.Fatalf("remove = %#v", ops)
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

// segs indexes a status node's segments by id.
func segs(t *testing.T, node map[string]any) map[string]map[string]any {
	t.Helper()
	byID := map[string]map[string]any{}
	for _, child := range node["c"].([]any) {
		seg := child.(map[string]any)
		byID[seg["id"].(string)] = seg
	}
	return byID
}

// The footer is a Tern status strip: Pi's first line and the extension
// statuses on the left, its usage, context and model on the right, in Pi's
// words, with a context meter and the thinking level's effort glyph.
func TestFooterIsATernStatusStrip(t *testing.T) {
	h := ready(t)
	ops := applyOps(t, h, footerOp(fe.Insert, sampleFooter()))
	if len(ops) != 1 || ops[0].([]any)[0] != "add" {
		t.Fatalf("insert = %#v", ops)
	}
	node := ops[0].([]any)[4].(map[string]any)
	if node["k"] != "status" {
		t.Fatalf("footer kind = %v", node["k"])
	}
	var order []string
	for _, child := range node["c"].([]any) {
		order = append(order, child.(map[string]any)["id"].(string))
	}
	if got := strings.Join(order, ","); got != "n.footer.cwd,n.footer.branch,n.footer.session,n.footer.status.0,n.footer.usage,n.footer.context,n.footer.xp,n.footer.model" {
		t.Fatalf("segments = %s", got)
	}
	byID := segs(t, node)
	props := func(id string) map[string]any { return byID[id]["p"].(map[string]any) }
	for id, want := range map[string]map[string]any{
		"n.footer.cwd":      {"icon": "folder", "text": "~/src/pig", "priority": float64(7), "min": map[string]any{"w": "12ch"}},
		"n.footer.branch":   {"icon": "branch", "text": "main", "priority": float64(5), "hidden": false},
		"n.footer.session":  {"icon": "message", "text": "", "priority": float64(3), "hidden": true},
		"n.footer.status.0": {"text": "lint ok", "priority": float64(2)},
		"n.footer.usage":    {"side": "right", "text": "↑1.3k ↓340 R900 W10 CH60.0% $0.013 (sub)", "priority": float64(6), "hidden": false},
		"n.footer.context":  {"side": "right", "text": "25.0%/200k (auto)", "priority": float64(8)},
		"n.footer.xp":       {"side": "right", "text": "xp", "tone": "warning", "priority": float64(1), "hidden": true},
		"n.footer.model":    {"side": "right", "icon": "model", "text": "(anthropic) claude • high → haiku • low", "priority": float64(9), "min": map[string]any{"w": "12ch"}},
	} {
		if got := props(id); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s = %#v, want %#v", id, got, want)
		}
	}
	meter := byID["n.footer.context"]["c"].([]any)[0].(map[string]any)
	if meter["k"] != "meter" || meter["p"].(map[string]any)["value"] != 0.25 {
		t.Fatalf("meter = %#v", meter)
	}
	effort := byID["n.footer.model"]["c"].([]any)[0].(map[string]any)
	if effort["k"] != "effort" || !reflect.DeepEqual(effort["p"], map[string]any{"level": "high", "hidden": false}) {
		t.Fatalf("effort = %#v", effort)
	}
}

// A change of the footer's data is a set of the props it changes: the
// thinking level sets the model text and the effort glyph, usage sets its
// segment, a fuller context colors its segment and meter, and an unknown
// size clears the meter's value. A new extension status replaces the strip.
func TestFooterChangesAreSets(t *testing.T) {
	h := ready(t)
	footer := sampleFooter()
	applyOps(t, h, footerOp(fe.Insert, footer))
	for _, tc := range []struct {
		name   string
		change func(*fe.Footer)
		want   []any
	}{
		{"thinking", func(f *fe.Footer) { f.ThinkingLevel = "off" }, []any{
			[]any{"set", "n.footer.model", map[string]any{"text": "(anthropic) claude • thinking off → haiku • low"}},
			[]any{"set", "n.footer.model.effort", map[string]any{"level": "off"}},
		}},
		{"no reasoning", func(f *fe.Footer) { f.ThinkingLevel, f.Provider, f.Routed = "", "", fe.RoutedModel{} }, []any{
			[]any{"set", "n.footer.model", map[string]any{"text": "claude"}},
			[]any{"set", "n.footer.model.effort", map[string]any{"level": nil, "hidden": true}},
		}},
		{"usage", func(f *fe.Footer) { f.UsageTotals.Output = 12_000 }, []any{
			[]any{"set", "n.footer.usage", map[string]any{"text": "↑1.3k ↓12k R900 W10 CH60.0% $0.013 (sub)"}},
		}},
		{"zero cost", func(f *fe.Footer) { f.UsageTotals.Cost = 0 }, []any{
			[]any{"set", "n.footer.usage", map[string]any{"text": "↑1.3k ↓12k R900 W10 CH60.0% $0.000 (sub)"}},
		}},
		{"no subscription", func(f *fe.Footer) { f.UsingSubscription = false }, []any{
			[]any{"set", "n.footer.usage", map[string]any{"text": "↑1.3k ↓12k R900 W10 CH60.0%"}},
		}},
		{"outside a repository", func(f *fe.Footer) { f.GitBranch = "" }, []any{
			[]any{"set", "n.footer.branch", map[string]any{"text": "", "hidden": true}},
		}},
		// Pi's footer.ts colors the context warning above 70% and error above
		// 90%, so exactly 70 and exactly 90 keep the tone below them.
		{"context at 70", func(f *fe.Footer) {
			f.ContextUsage = fe.ContextUsage{Tokens: 140_000, Percent: 70, ContextWindow: 200_000}
		}, []any{
			[]any{"set", "n.footer.context", map[string]any{"text": "70.0%/200k (auto)"}},
			[]any{"set", "n.footer.context.meter", map[string]any{"value": 0.7}},
		}},
		{"context above 70", func(f *fe.Footer) {
			f.ContextUsage = fe.ContextUsage{Tokens: 142_000, Percent: 71, ContextWindow: 200_000}
		}, []any{
			[]any{"set", "n.footer.context", map[string]any{"text": "71.0%/200k (auto)", "tone": "warning"}},
			[]any{"set", "n.footer.context.meter", map[string]any{"value": 0.71, "tone": "warning"}},
		}},
		{"context at 90", func(f *fe.Footer) {
			f.ContextUsage = fe.ContextUsage{Tokens: 180_000, Percent: 90, ContextWindow: 200_000}
		}, []any{
			[]any{"set", "n.footer.context", map[string]any{"text": "90.0%/200k (auto)"}},
			[]any{"set", "n.footer.context.meter", map[string]any{"value": 0.9}},
		}},
		{"context above 90", func(f *fe.Footer) {
			f.ContextUsage = fe.ContextUsage{Tokens: 182_000, Percent: 91, ContextWindow: 200_000}
		}, []any{
			[]any{"set", "n.footer.context", map[string]any{"text": "91.0%/200k (auto)", "tone": "error"}},
			[]any{"set", "n.footer.context.meter", map[string]any{"value": 0.91, "tone": "error"}},
		}},
		{"unknown", func(f *fe.Footer) { f.ContextUsage = fe.ContextUsage{Unknown: true, ContextWindow: 200_000} }, []any{
			[]any{"set", "n.footer.context", map[string]any{"text": "?/200k (auto)", "tone": nil}},
			[]any{"set", "n.footer.context.meter", map[string]any{"value": nil, "tone": nil}},
		}},
	} {
		tc.change(&footer)
		h.event(`{"ev":"ack","sf":"pig","s":` + strconv.Itoa(h.session.seq) + `}`)
		if got := applyOps(t, h, footerOp(fe.Update, footer)); !reflect.DeepEqual(normalize(t, got), normalize(t, tc.want)) {
			t.Fatalf("%s: ops = %#v, want %#v", tc.name, got, tc.want)
		}
	}
	footer.ExtensionStatuses = append(footer.ExtensionStatuses, "two")
	ops := applyOps(t, h, footerOp(fe.Update, footer))
	if len(ops) != 2 || ops[0].([]any)[0] != "del" || ops[1].([]any)[0] != "add" {
		t.Fatalf("new status = %#v", ops)
	}
}

// normalize round-trips ops through JSON so expected values compare as the
// wire carries them.
func normalize(t *testing.T, ops []any) any {
	t.Helper()
	data, err := json.Marshal(ops)
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

// Startup stays two frames with the dock's status nodes: footer and working
// changes that arrive before InputReady fold into the frame with the
// sendable flip, so a hidden pane still fits Tern's two credits.
func TestHiddenStartupWithStatusNodesStaysTwoFrames(t *testing.T) {
	h := newHarness(t)
	h.session.HandleInput("\x1b_tsp;r;{\"r\":\"hello\",\"v\":1,\"credits\":2}\x1b\\")
	messages(t, h.out)
	footer := sampleFooter()
	first := fe.Frame{Ops: []fe.Op{
		{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock", Index: 0, Node: fe.Lines{Lines: []string{""}}},
		{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 1, Node: fe.Editor{}},
		{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock.below", Index: 2, Node: fe.Lines{}},
		{Kind: fe.Insert, Region: fe.RegionDock, ID: "footer", Index: 3, Node: footer},
	}}
	footer.Model = "gpt-5"
	working := fe.Working{Kind: fe.WorkingAgent, Message: "Working"}
	for _, frame := range []fe.Frame{
		first,
		{Ops: []fe.Op{footerOp(fe.Update, footer)}},
		{Ops: []fe.Op{{Kind: fe.Insert, Region: fe.RegionDock, ID: "working", Index: 1, Node: working}}},
		{Ops: []fe.Op{{Kind: fe.Remove, Region: fe.RegionDock, ID: "working", Index: 1}}},
	} {
		if err := h.session.Apply(frame); err != nil {
			t.Fatal(err)
		}
	}
	h.session.InputReady()
	if err := h.session.Apply(fe.Frame{Ops: []fe.Op{{Kind: fe.Update, Region: fe.RegionDock, ID: "editor", Index: 1, Node: fe.Editor{Sendable: true}}}}); err != nil {
		t.Fatal(err)
	}
	got := messages(t, h.out)
	if len(got) != 2 {
		t.Fatalf("startup frames = %d, want two", len(got))
	}
	ops, err := json.Marshal(got[1].body["ops"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ops), `"sendable":true`) || !strings.Contains(string(ops), `gpt-5`) {
		t.Fatalf("second frame = %s, want the footer change and the sendable flip", ops)
	}
}
