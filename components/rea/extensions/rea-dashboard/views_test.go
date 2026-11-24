package readashboard

import (
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestHeaderWithServerAbsent(t *testing.T) {
	d := newDashboard((&fakeRunner{t: t}).run, func() time.Time { return fixedNow })
	t.Cleanup(d.shutdown)
	d.loadedAt = fixedNow.Add(-time.Hour)

	got := plainLines(headerLines(d.model(), 80))
	if !strings.Contains(got, "REA not registered") {
		t.Fatalf("header = %q", got)
	}
	if strings.Contains(got, "Hopper") || strings.Contains(got, "checking") {
		t.Fatalf("provider row shown without a server: %q", got)
	}
	if n := len(headerLines(d.model(), 80)); n != 1 {
		t.Fatalf("%d header lines without a server", n)
	}

	// Inside the grace period of a first run it reads "starting".
	d.loadedAt = fixedNow.Add(-time.Second)
	if got := plainLines(headerLines(d.model(), 80)); !strings.Contains(got, "starting") || strings.Contains(got, "not registered") {
		t.Fatalf("during grace: %q", got)
	}
	// The summary says so too.
	if s := d.model().summary(); !strings.HasPrefix(s, "REA not registered") {
		t.Fatalf("summary = %q", s)
	}
}

func TestHeaderWithProviders(t *testing.T) {
	d, _ := newTestDashboard(t)
	lines := headerLines(d.model(), 100)
	got := plainLines(lines)
	for _, want := range []string{"REA 6.8.0", "229 tools", "Hopper ○", "Ghidra ○", "IDA ○", "JADX ○", "Kuna ○", "not configured"} {
		if !strings.Contains(got, want) {
			t.Errorf("header lacks %q:\n%s", want, got)
		}
	}

	// A ready and a failing provider.
	d.mu.Lock()
	d.doctor.Report.Providers[0].State = provReady
	d.doctor.Report.Providers[1].State = provFailing
	d.mu.Unlock()
	got = plainLines(headerLines(d.model(), 100))
	if !strings.Contains(got, "Hopper ✓") || !strings.Contains(got, "Ghidra ✗") {
		t.Fatalf("header = %q", got)
	}

	// Without a harness version the word is omitted.
	d.harness = ""
	if got := plainLines(headerLines(d.model(), 100)); strings.Contains(got, "harness") {
		t.Fatalf("harness shown when unknown: %q", got)
	}

	// Doctor states.
	d.mu.Lock()
	d.doctor = doctorState{Status: doctorRunning}
	d.mu.Unlock()
	if got := plainLines(headerLines(d.model(), 100)); !strings.Contains(got, "checking") {
		t.Fatalf("running: %q", got)
	}
	d.mu.Lock()
	d.doctor = doctorState{Status: doctorFailed, Err: "timed out"}
	d.mu.Unlock()
	if got := plainLines(headerLines(d.model(), 100)); !strings.Contains(got, "doctor failed") || !strings.Contains(got, "timed out") {
		t.Fatalf("failed: %q", got)
	}
	// The version still shows when only the payload is known.
	if got := plainLines(headerLines(d.model(), 100)); !strings.Contains(got, "REA 6.8.0") {
		t.Fatalf("version lost: %q", got)
	}
}

func TestHeaderFitsNarrowWidths(t *testing.T) {
	d, _ := newTestDashboard(t)
	absent := newDashboard((&fakeRunner{t: t}).run, func() time.Time { return fixedNow })
	t.Cleanup(absent.shutdown)
	for _, dash := range []*dashboard{d, absent} {
		for _, w := range []int{1, 2, 5, 12, 20, 40, 80} {
			for _, l := range headerLines(dash.model(), w) {
				if textWidth(stripANSI(l)) > w {
					t.Errorf("width %d: %q is %d cells", w, l, textWidth(stripANSI(l)))
				}
			}
		}
	}
}

func TestWidgetHiddenWithoutCallsAndLimitedToFiveLines(t *testing.T) {
	d, _ := newTestDashboard(t)
	if lines := widgetLines(d.model(), 80); lines != nil {
		t.Fatalf("widget with no calls: %v", lines)
	}
	addCalls(d)
	if n := len(widgetLines(d.model(), 80)); n != 3 {
		t.Fatalf("%d lines for 3 calls", n)
	}
	for i := range 10 {
		id := "more" + string(rune('a'+i))
		d.store.start(toolEvent{ID: id, Tool: "list_names", Args: map[string]any{"path": "/bin/ls"}}, fixedNow.Add(-time.Duration(i)*time.Second))
		if i < 6 {
			d.store.end(toolEvent{ID: id, Tool: "list_names", Result: textResult("{}")}, fixedNow)
		}
	}
	lines := widgetLines(d.model(), 80)
	if len(lines) != maxWidgetLines {
		t.Fatalf("%d lines", len(lines))
	}
	if got := stripANSI(lines[len(lines)-1]); !strings.Contains(got, "earlier REA calls") {
		t.Fatalf("no overflow line: %q", got)
	}
	// The four running calls are shown before older finished ones.
	running := 0
	for _, l := range lines {
		if strings.Contains(stripANSI(l), "◐") || strings.Contains(stripANSI(l), "◓") || strings.Contains(stripANSI(l), "◑") || strings.Contains(stripANSI(l), "◒") {
			running++
		}
	}
	if running != 4 {
		t.Fatalf("%d running rows:\n%s", running, plainLines(lines))
	}
}

func TestWidgetLineContent(t *testing.T) {
	d, _ := newTestDashboard(t)
	addCalls(d)
	d.store.start(toolEvent{ID: "live", Parent: "p", Tool: "decompile", Args: map[string]any{"path": "/bin/ls", "address": "0x1000"}}, fixedNow.Add(-75*time.Second))
	got := plainLines(widgetLines(d.model(), 100))
	for _, want := range []string{"✓ inspect_artifact", "/bin/ls", "✗ list_strings", "↳", "decompile", "1m15s", "ev 2 · lim 2"} {
		if !strings.Contains(got, want) {
			t.Errorf("widget lacks %q:\n%s", want, got)
		}
	}
	for _, w := range []int{1, 3, 10, 25, 40} {
		for _, l := range widgetLines(d.model(), w) {
			if textWidth(stripANSI(l)) > w {
				t.Errorf("width %d: %q", w, stripANSI(l))
			}
		}
	}
}

func TestFmtDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Second: "0ms", 120 * time.Millisecond: "120ms", 2500 * time.Millisecond: "2.5s", 42 * time.Second: "42s", 75 * time.Second: "1m15s",
	} {
		if got := fmtDuration(d); got != want {
			t.Errorf("fmtDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestToolRendererResolver(t *testing.T) {
	nextCalled := false
	keep := &sdk.ToolRenderers{}
	got := resolveRenderers("bash", func() *sdk.ToolRenderers { nextCalled = true; return keep })
	if !nextCalled || got != keep {
		t.Fatal("a non-REA tool was not left to the host")
	}
	nextCalled = false
	own := resolveRenderers("mcp__rea__inspect_artifact", func() *sdk.ToolRenderers { nextCalled = true; return keep })
	if nextCalled || own == nil || own.Call == nil || own.Result == nil {
		t.Fatalf("REA tool: %+v", own)
	}
	lines, err := own.Call(sdk.Context{}, map[string]any{"path": "/Applications/Foo.app/Contents/MacOS/Foo"}, sdk.ToolRenderContext{}, 80)
	if err != nil || len(lines) != 1 {
		t.Fatalf("lines=%v err=%v", lines, err)
	}
	if got := stripANSI(lines[0]); !strings.Contains(got, "REA inspect/inspect_artifact") || !strings.Contains(got, "Foo.app") {
		t.Fatalf("call line %q", got)
	}
}

func TestRenderResultLines(t *testing.T) {
	ms := int64(1200)
	result := sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": resultJSON}}}
	lines := renderResult(result, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{DurationMs: &ms}, 80)
	if got := stripANSI(lines[0]); got != "✓ ok · 2 evidence · 2 limitations · 1.2s" || len(lines) != 1 {
		t.Fatalf("lines = %q", lines)
	}
	lines = renderResult(result, sdk.ToolRenderResultOptions{Expanded: true}, sdk.ToolRenderContext{}, 80)
	if got := plainLines(lines); !strings.Contains(got, "limitation: Hopper is not configured") || !strings.Contains(got, "evidence: ev_aaa") {
		t.Fatalf("expanded = %q", got)
	}
	errResult := sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": `{"error":{"message":"no session"}}`}}}
	if got := stripANSI(renderResult(errResult, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{IsError: true}, 80)[0]); got != "✗ error: no session" {
		t.Fatalf("error line %q", got)
	}
	if got := stripANSI(renderResult(result, sdk.ToolRenderResultOptions{IsPartial: true}, sdk.ToolRenderContext{}, 80)[0]); !strings.Contains(got, "running") {
		t.Fatalf("partial %q", got)
	}
	// Plain text results show ok without counts.
	plain := sdk.ToolRenderResult{Content: []map[string]any{{"type": "text", "text": "hello"}}}
	if got := stripANSI(renderResult(plain, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{}, 80)[0]); got != "✓ ok" {
		t.Fatalf("plain %q", got)
	}
	for _, w := range []int{1, 4, 12} {
		for _, l := range renderResult(result, sdk.ToolRenderResultOptions{Expanded: true}, sdk.ToolRenderContext{}, w) {
			if textWidth(stripANSI(l)) > w {
				t.Errorf("width %d: %q", w, stripANSI(l))
			}
		}
	}
}
