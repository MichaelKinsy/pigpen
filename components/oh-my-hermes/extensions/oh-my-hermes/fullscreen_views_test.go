package ohmyhermes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
)

func newTestSurface(t *testing.T) (*surfaceState, string, string) {
	t.Helper()
	omhHome := t.TempDir()
	hermesHome := t.TempDir()
	SetOMHHomeOverride("")
	t.Setenv(EnvOMHHome, omhHome)
	t.Setenv("HERMES_HOME", hermesHome)
	t.Cleanup(func() { SetOMHHomeOverride("") })

	pathsFn := func() OMHPaths { return ResolvePaths(omhHome) }
	identities := NewIdentityStore(pathsFn)
	router := NewRouteRouter(pathsFn, identities)
	dags := NewDAGStore(pathsFn)
	teams := NewTeamStore(pathsFn)
	loops := NewLoopStore(pathsFn)
	surf := newSurfaceState(pathsFn, router, dags, teams, loops)
	return surf, omhHome, hermesHome
}

func assertLinesFitWidth(t *testing.T, label string, lines []string, width int) {
	t.Helper()
	if len(lines) == 0 {
		t.Fatalf("%s: Render(%d) returned 0 lines", label, width)
	}
	for i, line := range lines {
		if w := tui.VisibleWidth(line); w > width {
			t.Errorf("%s: Render(%d) line %d exceeded width (%d > %d): %q", label, width, i, w, width, line)
		}
	}
}

func TestDAGPagerComponentRenderAndNavigation(t *testing.T) {
	surf, _, _ := newTestSurface(t)

	// Seed a diamond DAG: plan (completed) -> {api (running), ui (pending)} -> ship (pending)
	run, _, err := surf.dags.Create("sess-dag", "dag-fs-1", "Release Pipeline", []DAGNode{
		{ID: "plan", Title: "Design Plan", Category: "architect", WriteScope: []string{"docs/plan.md"}},
		{ID: "api", Title: "Build API", Category: "deep", DependsOn: []string{"plan"}, WriteScope: []string{"api/server.go"}},
		{ID: "ui", Title: "Build UI", Category: "visual-engineering", DependsOn: []string{"plan"}},
		{ID: "ship", Title: "Ship Release", Category: "quick", DependsOn: []string{"api", "ui"}},
	})
	if err != nil {
		t.Fatalf("create DAG failed: %v", err)
	}
	if _, err := surf.dags.UpdateNode(run.RunID, "plan", "completed", "Architecture approved", ""); err != nil {
		t.Fatalf("update plan node failed: %v", err)
	}
	if _, err := surf.dags.UpdateNode(run.RunID, "api", "running", "Implementing endpoints", ""); err != nil {
		t.Fatalf("update api node failed: %v", err)
	}

	var comp sdk.RemoteComponent = newDAGPagerComponent(surf, run.RunID)
	dag := comp.(*dagPagerComponent)

	// Render at 120 and 60 columns.
	lines120 := comp.Render(120)
	assertLinesFitWidth(t, "dagPager", lines120, 120)
	joined120 := strings.Join(lines120, "\n")
	for _, want := range []string{
		"OMH / DAG · 4 tasks",
		"Release Pipeline",
		"Done 1/4",
		"1 running",
		"╭", "╰", "▼",
		"[-] > Build API",
		"Start node",
		"← plan",
		"e Evidence  Tab/n next  Space/Enter fold  d Details  ↑↓ Scroll  q Close",
		"delegation graph · rows",
	} {
		if !strings.Contains(joined120, want) {
			t.Errorf("Render(120) missing %q\n%s", want, joined120)
		}
	}

	lines60 := comp.Render(60)
	assertLinesFitWidth(t, "dagPager", lines60, 60)

	// Space folds the selected node ("api") into single-line [+] form.
	res, err := comp.HandleInput(" ")
	if err != nil || res.Done {
		t.Fatalf("Space should fold without closing: res=%+v err=%v", res, err)
	}
	afterFold := strings.Join(comp.Render(120), "\n")
	if !strings.Contains(afterFold, "[+] > ● Build API · Running") {
		t.Errorf("expected folded single-line node box, got:\n%s", afterFold)
	}

	// Enter unfolds it back.
	_, _ = comp.HandleInput("\r")
	afterUnfold := strings.Join(comp.Render(120), "\n")
	if !strings.Contains(afterUnfold, "[-] > Build API") {
		t.Errorf("expected unfolded node box after Enter, got:\n%s", afterUnfold)
	}

	// Tab / n advances to next node; Shift+Tab / p returns to previous.
	firstSelected := dag.selectedNodeIDLocked()
	_, _ = comp.HandleInput("\t")
	secondSelected := dag.selectedNodeIDLocked()
	if secondSelected == firstSelected {
		t.Fatalf("expected Tab to move selection away from %q", firstSelected)
	}
	_, _ = comp.HandleInput("p")
	if got := dag.selectedNodeIDLocked(); got != firstSelected {
		t.Errorf("expected 'p' to return to %q, got %q", firstSelected, got)
	}

	// 'd' toggles Node details section off and back on.
	_, _ = comp.HandleInput("d")
	if dag.showDetails {
		t.Fatalf("expected 'd' to toggle showDetails off")
	}
	if strings.Contains(strings.Join(dag.buildGraphContentLocked(120), "\n"), "Node details") {
		t.Errorf("expected 'd' to hide Node details section")
	}
	_, _ = comp.HandleInput("d")
	if !dag.showDetails {
		t.Fatalf("expected second 'd' to toggle showDetails back on")
	}
	if !strings.Contains(strings.Join(dag.buildGraphContentLocked(120), "\n"), "Node details") {
		t.Errorf("expected second 'd' to restore Node details section")
	}
	// Scroll down to bottom with 'G' and confirm Node details renders in viewport.
	_, _ = comp.HandleInput("G")
	if !strings.Contains(strings.Join(comp.Render(120), "\n"), "Node details") {
		t.Errorf("expected Node details visible after scrolling to bottom with G")
	}
	// 'e' toggles evidence view and 'r' refreshes it.
	_, _ = comp.HandleInput("e")
	evText := strings.Join(comp.Render(120), "\n")
	if !strings.Contains(evText, "node evidence · rows") || !strings.Contains(evText, "Implementing endpoints") {
		t.Errorf("expected evidence view with observed result, got:\n%s", evText)
	}
	_, _ = comp.HandleInput("r")
	_, _ = comp.HandleInput("e")
	if !strings.Contains(strings.Join(comp.Render(120), "\n"), "delegation graph · rows") {
		t.Errorf("expected second 'e' to return to delegation graph view")
	}

	// 'q' closes cleanly.
	res, err = comp.HandleInput("q")
	if err != nil || !res.Done {
		t.Fatalf("expected 'q' to close dagPager, got res=%+v err=%v", res, err)
	}

	// Inline folding lane tree widget renderer check.
	updatedRun, _ := surf.dags.Get(run.RunID)
	widgetLines := renderDAGWidget(updatedRun, surf.router)
	widgetText := strings.Join(widgetLines, "\n")
	for _, want := range []string{"OMH / DAG · Release Pipeline", "▾ layer 1", "✓ Design Plan", "● Build API", "deep ·"} {
		if !strings.Contains(widgetText, want) {
			t.Errorf("renderDAGWidget missing %q\n%s", want, widgetText)
		}
	}
}

func TestBotsBoardComponent3LevelDrilldown(t *testing.T) {
	surf, _, hermesHome := newTestSurface(t)

	// Create a second profile with MEMORY.md and USER.md so gauges and multi-profile cards are populated.
	altMemDir := filepath.Join(hermesHome, "profiles", "research", "memories")
	if err := os.MkdirAll(altMemDir, 0o755); err != nil {
		t.Fatalf("mkdir alt profile memories: %v", err)
	}
	if err := os.WriteFile(filepath.Join(altMemDir, "MEMORY.md"), []byte(strings.Repeat("a", 1100)), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(altMemDir, "USER.md"), []byte(strings.Repeat("b", 500)), 0o600); err != nil {
		t.Fatalf("write USER.md: %v", err)
	}

	// Seed a team and usage session so default profile has Level 2 & Level 3 content.
	if _, err := surf.teams.Create("sess-bots-1", "team-bots-1", "Parallel Audit", []TeamLane{
		{LaneID: "lane-1", Title: "Check auth", Category: "review"},
	}); err != nil {
		t.Fatalf("create team failed: %v", err)
	}
	usage.Default().Add("sess-bots-1", "", "claude-sonnet-5-5", 1200, 450, 0.025, 3, 5)

	var comp sdk.RemoteComponent = newBotsBoardComponent(surf)
	board := comp.(*botsBoardComponent)

	// Level 1 render at 120 and 60 columns.
	l1Wide := comp.Render(120)
	assertLinesFitWidth(t, "botsBoard-L1", l1Wide, 120)
	l1Text := strings.Join(l1Wide, "\n")
	for _, want := range []string{
		"Hermes bots · 2 profiles",
		"❯ ● default",
		"research",
		"▰", "▱",
		"OMH",
	} {
		if !strings.Contains(l1Text, want) {
			t.Errorf("L1 Render(120) missing %q\n%s", want, l1Text)
		}
	}
	assertLinesFitWidth(t, "botsBoard-L1-60", comp.Render(60), 60)

	// Navigate down to second profile and back up to default.
	_, _ = comp.HandleInput("j")
	if board.profileIdx != 1 {
		t.Fatalf("expected 'j' to move profileIdx to 1, got %d", board.profileIdx)
	}
	_, _ = comp.HandleInput("k")
	if board.profileIdx != 0 {
		t.Fatalf("expected 'k' to move profileIdx back to 0, got %d", board.profileIdx)
	}

	// Drill down L1 -> L2 with Enter.
	res, err := comp.HandleInput("\r")
	if err != nil || res.Done || board.level != 2 {
		t.Fatalf("expected Enter to drill to L2: level=%d res=%+v err=%v", board.level, res, err)
	}
	l2Wide := comp.Render(120)
	assertLinesFitWidth(t, "botsBoard-L2", l2Wide, 120)
	assertLinesFitWidth(t, "botsBoard-L2-60", comp.Render(60), 60)
	l2Text := strings.Join(l2Wide, "\n")
	if !strings.Contains(l2Text, "Sessions & Delegations") || !strings.Contains(l2Text, "❯ ") {
		t.Errorf("L2 render missing sessions table or selector:\n%s", l2Text)
	}

	// Drill down L2 -> L3 with Right arrow.
	_, _ = comp.HandleInput("\x1b[C")
	if board.level != 3 {
		t.Fatalf("expected Right arrow to drill to L3, got level=%d", board.level)
	}
	l3Wide := comp.Render(120)
	assertLinesFitWidth(t, "botsBoard-L3", l3Wide, 120)
	assertLinesFitWidth(t, "botsBoard-L3-60", comp.Render(60), 60)
	l3Text := strings.Join(l3Wide, "\n")
	if !strings.Contains(l3Text, "Messages (last ") || !strings.Contains(l3Text, "│") {
		t.Errorf("L3 render missing messages box:\n%s", l3Text)
	}

	// Refresh in L3 and navigate top/bottom with g/G.
	_, _ = comp.HandleInput("r")
	_, _ = comp.HandleInput("G")
	_, _ = comp.HandleInput("g")

	// Back up L3 -> L2 -> L1 -> Close.
	res, _ = comp.HandleInput("\x1b[D") // Left arrow
	if res.Done || board.level != 2 {
		t.Fatalf("expected Left from L3 -> L2, got level=%d done=%v", board.level, res.Done)
	}
	res, _ = comp.HandleInput("\x1b") // Esc
	if res.Done || board.level != 1 {
		t.Fatalf("expected Esc from L2 -> L1, got level=%d done=%v", board.level, res.Done)
	}
	res, _ = comp.HandleInput("q")
	if !res.Done {
		t.Fatalf("expected 'q' on L1 to close board")
	}
}

func TestModelTableComponentEditSaveAndCancel(t *testing.T) {
	surf, omhHome, _ := newTestSurface(t)

	// --- Scenario A: Edit head model & effort, then Save with Enter ---
	var comp sdk.RemoteComponent = newModelTableComponent(surf)
	tbl := comp.(*modelTableComponent)

	wide := comp.Render(120)
	assertLinesFitWidth(t, "modelTable-120", wide, 120)
	wideText := strings.Join(wide, "\n")
	for _, want := range []string{
		"⚚ OMH · Model chains",
		"CATEGORY", "PURPOSE", "HEAD MODEL", "EFFORT", "STATE",
		"❯", "◂", "▸", "■", "· built-in",
		"↑↓ category   ←→ head model   −/+ effort   d default   ⏎ save   esc cancel",
	} {
		if !strings.Contains(wideText, want) {
			t.Errorf("Render(120) missing %q\n%s", want, wideText)
		}
	}

	// At width 60, PURPOSE column is omitted cleanly and lines fit.
	narrow := comp.Render(60)
	assertLinesFitWidth(t, "modelTable-60", narrow, 60)
	if strings.Contains(strings.Join(narrow, "\n"), "PURPOSE") {
		t.Errorf("Render(60) should hide PURPOSE column when width < 100")
	}

	// Move down to row 1 ("deep"), cycle head model right, step effort up.
	_, _ = comp.HandleInput("j")
	targetCat := tbl.rows[tbl.cursor].Category
	origModel := tbl.rows[tbl.cursor].CurrentChain[0].Model
	origEffort := tbl.rows[tbl.cursor].CurrentChain[0].ThinkingLevel

	_, _ = comp.HandleInput("l") // right -> cycle head model
	newModel := tbl.rows[tbl.cursor].CurrentChain[0].Model
	if newModel == origModel {
		t.Fatalf("expected 'l' to cycle head model from %q", origModel)
	}
	_, _ = comp.HandleInput("+") // step effort up
	newEffort := tbl.rows[tbl.cursor].CurrentChain[0].ThinkingLevel
	if newEffort == origEffort {
		t.Fatalf("expected '+' to step thinking effort from %q", origEffort)
	}

	editedText := strings.Join(comp.Render(120), "\n")
	if !strings.Contains(editedText, "● edited") || !strings.Contains(editedText, "1 unsaved change(s)") {
		t.Errorf("expected edited glyph and unsaved count in render:\n%s", editedText)
	}

	// Enter saves atomically to routing/model-chains.json and closes.
	res, err := comp.HandleInput("\r")
	if err != nil || !res.Done {
		t.Fatalf("expected Enter to save and close: res=%+v err=%v", res, err)
	}
	chain, overridden, err := surf.router.ChainForCategory(targetCat)
	if err != nil || !overridden || len(chain) == 0 {
		t.Fatalf("expected saved override for %q: chain=%+v overridden=%v err=%v", targetCat, chain, overridden, err)
	}
	if chain[0].Model != newModel || chain[0].ThinkingLevel != newEffort {
		t.Errorf("saved chain mismatch: got %s:%s, want %s:%s", chain[0].Model, chain[0].ThinkingLevel, newModel, newEffort)
	}
	if _, err := os.Stat(filepath.Join(omhHome, "routing", "model-chains.json")); err != nil {
		t.Fatalf("expected routing/model-chains.json on disk: %v", err)
	}

	// --- Scenario B: Re-open, verify ◆ customized, edit, then Cancel with Esc (no-op) ---
	comp2 := newModelTableComponent(surf)
	reopened := strings.Join(comp2.Render(120), "\n")
	if !strings.Contains(reopened, "◆ customized") {
		t.Errorf("expected saved category to display '◆ customized':\n%s", reopened)
	}
	_, _ = comp2.HandleInput("j") // select targetCat
	_, _ = comp2.HandleInput("d") // reset to default in memory
	res, err = comp2.HandleInput("\x1b") // Esc cancels without saving
	if err != nil || !res.Done {
		t.Fatalf("expected Esc to cancel and close: res=%+v err=%v", res, err)
	}
	// Disk override must still be intact after cancel.
	stillChain, stillOverridden, err := surf.router.ChainForCategory(targetCat)
	if err != nil || !stillOverridden || stillChain[0].Model != newModel {
		t.Fatalf("Esc cancel should not persist reset: chain=%+v overridden=%v err=%v", stillChain, stillOverridden, err)
	}
}
