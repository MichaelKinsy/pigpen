package readashboard

import (
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	kUp    = "\x1b[A"
	kDown  = "\x1b[B"
	kRight = "\x1b[C"
	kLeft  = "\x1b[D"
	kEnter = "\r"
	kEsc   = "\x1b"
	kTab   = "\t"
)

func newTestOverlay(t *testing.T, d *dashboard, tab tabID, height int) *overlay {
	t.Helper()
	o := newOverlay(d, tab, func() int { return height })
	t.Cleanup(o.Dispose)
	return o
}

func press(t *testing.T, o *overlay, keys ...string) sdk.RemoteComponentResult {
	t.Helper()
	var res sdk.RemoteComponentResult
	for _, k := range keys {
		var err error
		res, err = o.HandleInput(k)
		if err != nil {
			t.Fatal(err)
		}
		if res.Done {
			return res
		}
	}
	return res
}

func screen(o *overlay, width int) string { return plainLines(o.Render(width)) }

func TestOverlayTabsAndInvestigationDetail(t *testing.T) {
	d, _ := newTestDashboard(t)
	addCalls(d)
	o := newTestOverlay(t, d, tabInvestigation, 30)

	out := screen(o, 100)
	for _, want := range []string{"Investigation", "Targets", "Providers", "About", "open_binary", "inspect_artifact", "list_strings", "↳"} {
		if !strings.Contains(out, want) {
			t.Errorf("timeline lacks %q:\n%s", want, out)
		}
	}

	// The selection starts on the newest call (the failing list_strings); ↑ moves to inspect_artifact, whose
	// detail leads with evidence and limitations.
	press(t, o, kUp, kEnter)
	detail := screen(o, 100)
	ev, lim, target := strings.Index(detail, "Evidence (2)"), strings.Index(detail, "Limitations (2)"), strings.Index(detail, "\nTarget\n")
	if ev < 0 || lim < 0 || target < 0 || !(ev < lim && lim < target) {
		t.Fatalf("detail order evidence=%d limitations=%d target=%d:\n%s", ev, lim, target, detail)
	}
	for _, want := range []string{"ev_aaa", "Hopper is not configured", "Symbols are stripped", "inspect_artifact", "codemode", "full result is in the transcript"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail lacks %q:\n%s", want, detail)
		}
	}

	// Esc goes back to the list, a second Esc closes.
	press(t, o, kEsc)
	if !strings.Contains(screen(o, 100), "list_strings") {
		t.Fatal("not back at the list")
	}
	if res := press(t, o, kEsc); !res.Done {
		t.Fatal("Esc in the list did not close")
	}

	// The failing call's detail shows its error.
	o = newTestOverlay(t, d, tabInvestigation, 30)
	press(t, o, kEnter)
	if got := screen(o, 100); !strings.Contains(got, "no such session") || !strings.Contains(got, "error") {
		t.Fatalf("error detail:\n%s", got)
	}
}

func TestOverlayKeysSwitchTabsAndClose(t *testing.T) {
	d, _ := newTestDashboard(t)
	addCalls(d)
	o := newTestOverlay(t, d, tabInvestigation, 24)
	for _, tc := range []struct {
		keys string
		want tabID
	}{{kTab, tabTargets}, {kRight, tabProviders}, {"\x1b[Z", tabTargets}, {kLeft, tabInvestigation}, {kLeft, tabAbout}, {"2", tabTargets}, {"4", tabAbout}, {"l", tabInvestigation}, {"h", tabAbout}} {
		press(t, o, tc.keys)
		if o.tab != tc.want {
			t.Fatalf("after %q tab = %v, want %v", tc.keys, o.tab, tc.want)
		}
	}
	for _, k := range []string{"q", "Q", "\x03", "\x1b[113u"} {
		if res := press(t, o, k); !res.Done {
			t.Errorf("%q did not close", k)
		}
	}
	// Unknown input is ignored.
	if res := press(t, o, "\x1b[99;9~", "", "\x1b[<0;1;1M"); res.Done {
		t.Fatal("noise closed the overlay")
	}
}

func TestOverlayTargetsActions(t *testing.T) {
	d, fr := newTestDashboard(t)
	addCalls(d)
	o := newTestOverlay(t, d, tabTargets, 30)

	out := screen(o, 100)
	if !strings.Contains(out, "/Applications/Foo.app") || !strings.Contains(out, "/bin/ls") {
		t.Fatalf("targets:\n%s", out)
	}
	// The most recent target (Foo.app) is first and selected; Enter opens its menu.
	press(t, o, kEnter)
	menu := screen(o, 100)
	for _, want := range []string{"Investigate…", "Quick inspect", "Derive piglet…", "needs a local path"} {
		if !strings.Contains(menu, want) && want != "needs a local path" {
			t.Errorf("menu lacks %q:\n%s", want, menu)
		}
	}
	// Investigate closes the overlay with the target.
	res := press(t, o, kEnter)
	got, _ := res.Value.(map[string]any)
	if !res.Done || got["action"] != closeInvestigate || got["target"] != "/Applications/Foo.app" {
		t.Fatalf("investigate result %+v", res)
	}

	// Derive, from the menu and from the list shortcut.
	o = newTestOverlay(t, d, tabTargets, 30)
	res = press(t, o, kDown, kEnter, kDown, kDown, kEnter)
	got, _ = res.Value.(map[string]any)
	if !res.Done || got["action"] != closeDerive || got["target"] != "/bin/ls" {
		t.Fatalf("derive result %+v", res)
	}
	o = newTestOverlay(t, d, tabTargets, 30)
	res = press(t, o, "d")
	if got, _ = res.Value.(map[string]any); got["action"] != closeDerive || got["target"] != "/Applications/Foo.app" {
		t.Fatalf("derive shortcut %+v", res)
	}
	o = newTestOverlay(t, d, tabTargets, 30)
	res = press(t, o, "i")
	if got, _ = res.Value.(map[string]any); got["action"] != closeInvestigate {
		t.Fatalf("investigate shortcut %+v", res)
	}

	// Quick inspect of a local path runs in the background and shows a folded result.
	o = newTestOverlay(t, d, tabTargets, 40)
	press(t, o, kDown, "v")
	waitFor(t, "quick inspect", func() bool { return !d.model().Inspect["/bin/ls"].Running && len(d.model().Inspect) == 1 })
	if _, inspect := fr.counts(); inspect != 1 || fr.inspect[0] != "/bin/ls" {
		t.Fatalf("inspections: %v", fr.inspect)
	}
	folded := screen(o, 100)
	if !strings.Contains(folded, "evidence ev_") || !strings.Contains(folded, "limitation:") || !strings.Contains(folded, "▸ raw output") || strings.Contains(folded, `"evidence_id"`) {
		t.Fatalf("folded result:\n%s", folded)
	}
	press(t, o, kEnter)
	expanded := screen(o, 100)
	if !strings.Contains(expanded, "▾ raw output") || !strings.Contains(expanded, `"evidence_id"`) {
		t.Fatalf("expanded result:\n%s", expanded)
	}
	press(t, o, kEnter)
	if strings.Contains(screen(o, 100), `"evidence_id"`) {
		t.Fatal("did not fold again")
	}
	// Esc leaves the result, then the menu state, and the list closes.
	press(t, o, kEsc)
	if o.mode != modeList {
		t.Fatalf("mode = %v", o.mode)
	}

	// A URL cannot be quick-inspected: a notice, and nothing ran.
	d.store.start(toolEvent{ID: "u", Tool: "capture_web", Args: map[string]any{"url": "https://example.test/app"}}, fixedNow.Add(time.Second))
	o = newTestOverlay(t, d, tabTargets, 30)
	press(t, o, "v")
	if !strings.Contains(screen(o, 100), "local absolute path") || o.mode != modeList {
		t.Fatalf("no notice for a URL:\n%s", screen(o, 100))
	}
	if _, inspect := fr.counts(); inspect != 1 {
		t.Fatalf("inspect ran for a URL: %d", inspect)
	}
}

func TestOverlayQuickInspectWithoutServer(t *testing.T) {
	d, _ := newTestDashboard(t)
	addCalls(d)
	d.mu.Lock()
	d.server = reaServer{}
	d.mu.Unlock()
	o := newTestOverlay(t, d, tabTargets, 30)
	press(t, o, kDown, "v")
	if got := screen(o, 100); !strings.Contains(got, "REA is not registered") {
		t.Fatalf("screen:\n%s", got)
	}
}

func TestOverlayProvidersAndAbout(t *testing.T) {
	d, fr := newTestDashboard(t)
	o := newTestOverlay(t, d, tabProviders, 40)
	out := screen(o, 100)
	for _, want := range []string{"Hopper", "Ghidra", "IDA", "JADX", "Kuna", "not configured", "8 other doctor check"} {
		if !strings.Contains(out, want) {
			t.Errorf("providers lacks %q:\n%s", want, out)
		}
	}
	// The second provider is Ghidra: its detail names the variables REA reads.
	press(t, o, kDown, kEnter)
	detail := screen(o, 120)
	for _, want := range []string{"Ghidra", "GHIDRA_INSTALL_DIR is not set", "Environment variables REA reads", "GHIDRA_INSTALL_DIR", "REA_GHIDRA_PROJECT_PATH", "REA_ANALYSIS_PROVIDER"} {
		if !strings.Contains(detail, want) {
			t.Errorf("provider detail lacks %q:\n%s", want, detail)
		}
	}
	press(t, o, kEsc)

	// r runs the doctor again.
	press(t, o, "r")
	waitFor(t, "doctor", func() bool { n, _ := fr.counts(); return n == 1 })

	// About.
	press(t, o, "4")
	about := screen(o, 120)
	for _, want := range []string{"6.8.0", "rea-agents-6.8.0", "24.22.2", "darwin/arm64", "/p", testServer.Payload.TreeSha256, "8965", "run /rea-runtime verify", "/p/node/bin/node /p/rea/scripts/rea.mjs mcp", "0.1.0"} {
		if !strings.Contains(about, want) {
			t.Errorf("about lacks %q:\n%s", want, about)
		}
	}
}

func TestOverlayEmptyAndNotRegistered(t *testing.T) {
	d := newDashboard((&fakeRunner{t: t}).run, func() time.Time { return fixedNow })
	t.Cleanup(d.shutdown)
	d.loadedAt = fixedNow.Add(-time.Hour)
	for tab, want := range map[tabID]string{
		tabInvestigation: "No REA calls in this session yet",
		tabTargets:       "No targets yet",
		tabProviders:     "REA is not registered",
		tabAbout:         "REA is not registered",
	} {
		o := newTestOverlay(t, d, tab, 24)
		if got := screen(o, 100); !strings.Contains(got, want) {
			t.Errorf("tab %v lacks %q:\n%s", tab, want, got)
		}
		// Keys on empty lists do nothing and do not panic.
		press(t, o, kEnter, kDown, kUp, "v", "d", "i", "r", kEsc)
	}
}

func TestOverlayDoesNotPassControlSequencesThrough(t *testing.T) {
	d, _ := newTestDashboard(t)
	evil := "/tmp/\x1b]0;pwned\x07\x1b[2Jname"
	d.store.start(toolEvent{ID: "evil", Tool: "open\x1b[31m_binary", Args: map[string]any{"path": evil}}, fixedNow)
	d.store.end(toolEvent{ID: "evil", Tool: "open\x1b[31m_binary", IsError: true, Result: textResult("{\"error\":\"bad\\u001b[2J\"}")}, fixedNow)
	o := newTestOverlay(t, d, tabInvestigation, 30)
	screens := []string{strings.Join(o.Render(100), "\n")}
	press(t, o, kEnter)
	screens = append(screens, strings.Join(o.Render(100), "\n"))
	press(t, o, kEsc, "2")
	screens = append(screens, strings.Join(o.Render(100), "\n"))
	press(t, o, kEnter)
	screens = append(screens, strings.Join(o.Render(100), "\n"))
	for _, s := range append(screens, strings.Join(widgetLines(d.model(), 100), "\n"), strings.Join(headerLines(d.model(), 100), "\n")) {
		for _, bad := range []string{"\x1b]", "\x1b[2J", "\x07", "\x1b[31m_"} {
			if strings.Contains(s, bad) {
				t.Fatalf("control sequence %q reached the output:\n%q", bad, s)
			}
		}
	}
}

// Every tab and mode at awkward sizes: no panic, no row wider than the box, no more rows than the box.
func TestOverlayRendersAtNarrowSizes(t *testing.T) {
	d, _ := newTestDashboard(t)
	addCalls(d)
	d.mu.Lock()
	d.inspects["/bin/ls"] = &inspectJob{Target: "/bin/ls", Result: formatInspect(readTestdata(t, "inspect-artifact.json"), false)}
	d.mu.Unlock()
	// Reaching every mode: the tab, then keys.
	paths := map[string][]string{
		"invest list":     {},
		"invest detail":   {kEnter},
		"targets list":    {"2"},
		"targets menu":    {"2", kEnter},
		"targets result":  {"2", kDown, "v"},
		"targets rawOpen": {"2", kDown, "v", kEnter},
		"providers":       {"3"},
		"prov detail":     {"3", kEnter},
		"about":           {"4"},
	}
	for name, keys := range paths {
		for _, width := range []int{1, 2, 3, 6, 10, 20, 30, 40, 80, 200} {
			for _, height := range []int{0, 1, 2, 3, 4, 5, 8, 24, 60} {
				o := newTestOverlay(t, d, tabInvestigation, height)
				press(t, o, keys...)
				lines := o.Render(width)
				box := max(width-2, 1)
				wantRows := max(height, 24) - 2
				if height > 0 {
					wantRows = max(height-2, 1)
				}
				if len(lines) > wantRows {
					t.Fatalf("%s %dx%d: %d rows, box has %d", name, width, height, len(lines), wantRows)
				}
				for _, l := range lines {
					if w := textWidth(stripANSI(l)); w > box {
						t.Fatalf("%s %dx%d: row %q is %d cells, box is %d", name, width, height, stripANSI(l), w, box)
					}
				}
				// Keys keep working at that size.
				press(t, o, kDown, kUp, "\x1b[6~", "\x1b[5~", "\x1b[H", "\x1b[F")
				o.Render(width)
			}
		}
	}
}

func TestOverlayScrollsLongPanes(t *testing.T) {
	d, _ := newTestDashboard(t)
	o := newTestOverlay(t, d, tabProviders, 10)
	press(t, o, kDown, kEnter) // Ghidra detail is longer than 8 rows
	first := screen(o, 100)
	press(t, o, "\x1b[F") // End
	last := screen(o, 100)
	if first == last || !strings.Contains(last, "harness installs nothing") {
		t.Fatalf("pane did not scroll:\n%s", last)
	}
	press(t, o, "\x1b[H")
	if screen(o, 100) != first {
		t.Fatal("Home did not return to the top")
	}
}

func TestOverlayMouse(t *testing.T) {
	d, _ := newTestDashboard(t)
	addCalls(d)
	o := newTestOverlay(t, d, tabInvestigation, 30)
	o.Render(100)
	click := func(x, y, count int) sdk.RemoteComponentResult {
		res, err := o.HandleMouse(sdk.MouseEvent{Type: sdk.MouseClick, Button: sdk.MouseButtonLeft, X: x, Y: y, ClickCount: count})
		if err != nil {
			t.Fatal(err)
		}
		o.Render(100)
		return res
	}
	// The Targets tab label.
	span := o.tabSpans[tabTargets]
	click(span[0]+1, 0, 1)
	if o.tab != tabTargets {
		t.Fatalf("tab = %v", o.tab)
	}
	// Click the second target row, double-click opens its menu.
	click(3, o.bodyTop+1, 1)
	if o.sel[tabTargets] != 1 {
		t.Fatalf("sel = %d", o.sel[tabTargets])
	}
	click(3, o.bodyTop+1, 2)
	if o.mode != modeMenu {
		t.Fatalf("mode = %v", o.mode)
	}
	// Wheel moves the menu cursor; clicks outside the list change nothing.
	o.HandleMouse(sdk.MouseEvent{Type: sdk.MouseWheel, WheelDelta: 1})
	if o.menuSel != 1 {
		t.Fatalf("menuSel = %d", o.menuSel)
	}
	click(0, 999, 1)
	press(t, o, kEsc)
	o.HandleMouse(sdk.MouseEvent{Type: sdk.MouseWheel, WheelDelta: 0})
	o.HandleMouse(sdk.MouseEvent{Type: sdk.MouseMove})
	click(0, 0, 1) // a tab bar click on the active label changes nothing
}

func TestOverlayFollowsNewCallsUnlessMovedAway(t *testing.T) {
	d, _ := newTestDashboard(t)
	addCalls(d)
	o := newTestOverlay(t, d, tabInvestigation, 30)
	o.Render(100)
	if o.sel[tabInvestigation] != 2 {
		t.Fatalf("sel = %d", o.sel[tabInvestigation])
	}
	d.store.start(toolEvent{ID: "n1", Tool: "x"}, fixedNow)
	o.Render(100)
	if o.sel[tabInvestigation] != 3 {
		t.Fatalf("did not follow: %d", o.sel[tabInvestigation])
	}
	press(t, o, kUp)
	d.store.start(toolEvent{ID: "n2", Tool: "x"}, fixedNow)
	o.Render(100)
	if o.sel[tabInvestigation] != 2 {
		t.Fatalf("moved away but selection jumped: %d", o.sel[tabInvestigation])
	}
}

func TestOverlayInvalidatesOnChangesAndDetaches(t *testing.T) {
	d, _ := newTestDashboard(t)
	o := newTestOverlay(t, d, tabInvestigation, 24)
	calls := make(chan struct{}, 8)
	o.SetInvalidate(func() { calls <- struct{}{} })
	d.notifySubs()
	select {
	case <-calls:
	default:
		t.Fatal("no invalidation")
	}
	o.SetInvalidate(nil)
	d.notifySubs()
	select {
	case <-calls:
		t.Fatal("invalidated after detach")
	default:
	}
	o.SetInvalidate(func() { calls <- struct{}{} })
	o.Dispose()
	o.Dispose()
	d.notifySubs()
	select {
	case <-calls:
		t.Fatal("invalidated after dispose")
	default:
	}
}
