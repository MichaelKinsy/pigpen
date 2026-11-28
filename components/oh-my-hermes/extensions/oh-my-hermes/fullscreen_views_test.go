package ohmyhermes

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/delegation"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/events"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
)

var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiEscapeRe.ReplaceAllString(s, "")
}

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
			t.Errorf("%s: Render(%d) line %d exceeded width (%d > %d): %q", label, width, i, w, width, stripANSI(line))
		}
	}
}

func assertBoxBordersExactWidth(t *testing.T, label string, lines []string, expectedWidth int) {
	t.Helper()
	checked := 0
	for i, line := range lines {
		plain := stripANSI(line)
		if strings.HasPrefix(plain, "╭") || strings.HasPrefix(plain, "│") || strings.HasPrefix(plain, "┃") || strings.HasPrefix(plain, "╰") {
			if w := tui.VisibleWidth(line); w != expectedWidth {
				t.Errorf("%s: box line %d visual width = %d, want exact %d: %q", label, i, w, expectedWidth, plain)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Errorf("%s: expected at least one box line to check border alignment", label)
	}
}

func TestThemeFidelitySkyAndCrimson(t *testing.T) {
	sky := lookupThemeSpec("sky")
	if sky.Accent != "#00CED1" {
		t.Errorf("omh-sky accent = %q, want #00CED1", sky.Accent)
	}
	crimson := lookupThemeSpec("crimson")
	if crimson.Accent != "#FF6A3D" {
		t.Errorf("omh-crimson accent = %q, want #FF6A3D", crimson.Accent)
	}
	if crimson.Error != "#FF3D6E" {
		t.Errorf("omh-crimson error = %q, want #FF3D6E", crimson.Error)
	}
	if crimson.Accent == crimson.Error {
		t.Errorf("omh-crimson accent and error must remain visually distinct")
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
	// Seed a delegation record so elapsed duration & token summary appear on the node box.
	surf.teams.OnSubagentStart(events.SubagentStartPayload{
		TaskID:         "api",
		ChildSessionID: "child-api",
		Category:       "deep",
		Model:          "claude-sonnet-5-5",
	})
	surf.teams.OnSubagentStop(events.SubagentStopPayload{
		TaskID:         "api",
		ChildSessionID: "child-api",
		Status:         "running",
		InputTokens:    1200,
		OutputTokens:   300,
		DurationMs:     4500,
	})

	var comp sdk.RemoteComponent = newDAGPagerComponent(surf, run.RunID)
	dag := comp.(*dagPagerComponent)
	defer dag.Dispose()

	// Render at 120 and 60 columns.
	lines120 := comp.Render(120)
	assertLinesFitWidth(t, "dagPager", lines120, 120)
	raw120 := strings.Join(lines120, "\n")
	if !strings.Contains(raw120, "\x1b[38;2;") {
		t.Errorf("expected truecolor ANSI styling in /dag render")
	}
	// Check sky accent RGB (0, 206, 209) and running blue RGB (88, 166, 255).
	if !strings.Contains(raw120, "\x1b[1;38;2;0;206;209m") && !strings.Contains(raw120, "\x1b[38;2;0;206;209m") {
		t.Errorf("expected omh-sky accent #00CED1 in /dag render")
	}
	if !strings.Contains(raw120, "\x1b[38;2;88;166;255m") {
		t.Errorf("expected running status blue #58A6FF in /dag render")
	}

	joined120 := stripANSI(raw120)
	for _, want := range []string{
		"OMH / DAG · 4 tasks",
		"Release Pipeline",
		"Done 1/4",
		"1 running",
		"╭", "╰", "▼",
		"[-] > Build API",
		"Start node",
		"← plan",
		"1.5k tok",
		"e Evidence  Tab/n next  Space/Enter fold  d Details  ↑↓ Scroll  q Close",
		"delegation graph · rows",
	} {
		if !strings.Contains(joined120, want) {
			t.Errorf("Render(120) missing %q\n%s", want, joined120)
		}
	}
	// Verify real diamond midpoint edge routing: fan-out (╭ / ┴ / ╮) and fan-in (╰ / ┬ / ╯).
	if !strings.Contains(joined120, "╭") || !strings.Contains(joined120, "┴") || !strings.Contains(joined120, "╮") {
		t.Errorf("expected fan-out bus connectors (╭ ┴ ╮) in diamond DAG:\n%s", joined120)
	}
	if !strings.Contains(joined120, "╰") || !strings.Contains(joined120, "┬") || !strings.Contains(joined120, "╯") {
		t.Errorf("expected fan-in bus connectors (╰ ┬ ╯) in diamond DAG:\n%s", joined120)
	}

	lines60 := comp.Render(60)
	assertLinesFitWidth(t, "dagPager", lines60, 60)

	// Space folds the selected node ("api") into single-line [+] form.
	res, err := comp.HandleInput(" ")
	if err != nil || res.Done {
		t.Fatalf("Space should fold without closing: res=%+v err=%v", res, err)
	}
	afterFold := stripANSI(strings.Join(comp.Render(120), "\n"))
	if !strings.Contains(afterFold, "[+] > ● Build API · Running") {
		t.Errorf("expected folded single-line node box, got:\n%s", afterFold)
	}

	// Enter unfolds it back.
	_, _ = comp.HandleInput("\r")
	afterUnfold := stripANSI(strings.Join(comp.Render(120), "\n"))
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
	if strings.Contains(stripANSI(strings.Join(dag.buildGraphContentLocked(120), "\n")), "Node details") {
		t.Errorf("expected 'd' to hide Node details section")
	}
	_, _ = comp.HandleInput("d")
	if !dag.showDetails {
		t.Fatalf("expected second 'd' to toggle showDetails back on")
	}
	if !strings.Contains(stripANSI(strings.Join(dag.buildGraphContentLocked(120), "\n")), "Node details") {
		t.Errorf("expected second 'd' to restore Node details section")
	}
	// Scroll down to bottom with 'G' and confirm Node details renders in viewport.
	_, _ = comp.HandleInput("G")
	if !strings.Contains(stripANSI(strings.Join(comp.Render(120), "\n")), "Node details") {
		t.Errorf("expected Node details visible after scrolling to bottom with G")
	}
	// 'e' toggles evidence view and 'r' refreshes it.
	_, _ = comp.HandleInput("e")
	evText := stripANSI(strings.Join(comp.Render(120), "\n"))
	if !strings.Contains(evText, "node evidence · rows") || !strings.Contains(evText, "Implementing endpoints") {
		t.Errorf("expected evidence view with observed result, got:\n%s", evText)
	}
	_, _ = comp.HandleInput("r")
	_, _ = comp.HandleInput("e")
	if !strings.Contains(stripANSI(strings.Join(comp.Render(120), "\n")), "delegation graph · rows") {
		t.Errorf("expected second 'e' to return to delegation graph view")
	}

	// 'q' closes cleanly and disposes ticker.
	res, err = comp.HandleInput("q")
	if err != nil || !res.Done {
		t.Fatalf("expected 'q' to close dagPager, got res=%+v err=%v", res, err)
	}

	// Inline folding lane tree widget renderer check (with ANSI styling).
	updatedRun, _ := surf.dags.Get(run.RunID)
	widgetLines := renderDAGWidget(updatedRun, surf.router, "sky")
	widgetRaw := strings.Join(widgetLines, "\n")
	if !strings.Contains(widgetRaw, "\x1b[") {
		t.Errorf("expected ANSI colorized renderDAGWidget output")
	}
	widgetText := stripANSI(widgetRaw)
	for _, want := range []string{"OMH / DAG · Release Pipeline", "▾ layer 1", "✓ Design Plan", "● Build API", "deep ·"} {
		if !strings.Contains(widgetText, want) {
			t.Errorf("renderDAGWidget missing %q\n%s", want, widgetText)
		}
	}
}

func TestDAGCrossingEdgesFallback(t *testing.T) {
	surf, _, _ := newTestSurface(t)
	// Crossed edges: a -> d, b -> c (where layer0=[a,b], layer1=[c,d]).
	run, _, err := surf.dags.Create("sess-cross", "dag-cross", "Crossed DAG", []DAGNode{
		{ID: "a", Title: "Node A", Category: "quick"},
		{ID: "b", Title: "Node B", Category: "quick"},
		{ID: "c", Title: "Node C", Category: "quick", DependsOn: []string{"b"}},
		{ID: "d", Title: "Node D", Category: "quick", DependsOn: []string{"a"}},
	})
	if err != nil {
		t.Fatalf("create crossed DAG failed: %v", err)
	}
	dag := newDAGPagerComponent(surf, run.RunID)
	defer dag.Dispose()
	plain := stripANSI(strings.Join(dag.Render(120), "\n"))
	if !strings.Contains(plain, "╎") || !strings.Contains(plain, "▽") {
		t.Errorf("expected centered ╎/▽ fallback for crossing edges:\n%s", plain)
	}
}

func TestLiveRefreshSetInvalidateAndDispose(t *testing.T) {
	surf, _, _ := newTestSurface(t)
	run, _, err := surf.dags.Create("sess-live", "dag-live", "Live Ticker DAG", []DAGNode{
		{ID: "n1", Title: "Step 1", Category: "quick"},
	})
	if err != nil {
		t.Fatalf("create DAG failed: %v", err)
	}

	dag := newDAGPagerComponent(surf, run.RunID)
	var dagTicks atomic.Int32
	dag.SetInvalidate(func() { dagTicks.Add(1) })
	// Wait briefly for at least 1 tick (1s ticker), then dispose twice to ensure idempotent teardown.
	deadline := time.Now().Add(2500 * time.Millisecond)
	for dagTicks.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if dagTicks.Load() == 0 {
		t.Errorf("expected dagPagerComponent 1s ticker to invoke SetInvalidate callback")
	}
	dag.Dispose()
	dag.Dispose() // idempotent
	afterDAG := dagTicks.Load()
	time.Sleep(150 * time.Millisecond)
	if dagTicks.Load() != afterDAG {
		t.Errorf("dagPagerComponent ticked after Dispose()")
	}

	// Verify botsBoardComponent SetInvalidate + Dispose lifecycle is race-free and idempotent.
	board := newBotsBoardComponent(surf)
	var botTicks atomic.Int32
	board.SetInvalidate(func() { botTicks.Add(1) })
	board.SetInvalidate(func() { botTicks.Add(1) }) // second call must not leak a second goroutine
	board.Dispose()
	board.Dispose()
	if !board.disposed {
		t.Errorf("expected botsBoardComponent to be marked disposed")
	}
}

func TestBotsBoardComponent3LevelDrilldown(t *testing.T) {
	surf, _, hermesHome := newTestSurface(t)

	// Create a second profile with MEMORY.md and USER.md so gauges and multi-profile cards are populated.
	altHome := filepath.Join(hermesHome, "profiles", "research")
	altMemDir := filepath.Join(altHome, "memories")
	if err := os.MkdirAll(altMemDir, 0o755); err != nil {
		t.Fatalf("mkdir alt profile memories: %v", err)
	}
	if err := os.WriteFile(filepath.Join(altMemDir, "MEMORY.md"), []byte(strings.Repeat("a", 1100)), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(altMemDir, "USER.md"), []byte(strings.Repeat("b", 500)), 0o600); err != nil {
		t.Fatalf("write USER.md: %v", err)
	}
	// Seed a JSONL fallback session in research profile (and optionally a real sqlite3 state.db).
	altSessDir := filepath.Join(altHome, "sessions")
	if err := os.MkdirAll(altSessDir, 0o755); err != nil {
		t.Fatalf("mkdir alt sessions: %v", err)
	}
	if err := os.WriteFile(filepath.Join(altSessDir, "jsonl-sess-1.jsonl"),
		[]byte(`{"role":"user","content":"Analyze benchmark results","timestamp":"2026-11-28T10:00:00Z"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write fallback jsonl: %v", err)
	}

	// If sqlite3 is installed, seed a real state.db in default profile to verify read-only SQLite loading.
	if sqliteBin, err := exec.LookPath("sqlite3"); err == nil {
		dbPath := filepath.Join(hermesHome, "state.db")
		nowSec := float64(time.Now().Unix())
		sqlInit := `
CREATE TABLE sessions (
	id TEXT PRIMARY KEY,
	title TEXT,
	model TEXT,
	source TEXT,
	started_at REAL,
	ended_at REAL,
	message_count INTEGER,
	input_tokens INTEGER,
	output_tokens INTEGER
);
CREATE TABLE messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT,
	role TEXT,
	content TEXT,
	timestamp REAL
);
INSERT INTO sessions VALUES ('sqlite-sess-1', 'SQLite Audit Session', 'claude-opus-5-5', 'cli', ` +
			strings.TrimSpace(time.Unix(int64(nowSec)-60, 0).Format("1700000000")) + `, ` +
			"strftime('%s','now'), 2, 4200, 1800);" + `
INSERT INTO messages (session_id, role, content, timestamp) VALUES ('sqlite-sess-1', 'user', 'Check sqlite read-only path', strftime('%s','now'));
INSERT INTO messages (session_id, role, content, timestamp) VALUES ('sqlite-sess-1', 'assistant', 'SQLite messages loaded cleanly', strftime('%s','now'));
`
		if out, err := exec.Command(sqliteBin, dbPath, sqlInit).CombinedOutput(); err != nil {
			t.Fatalf("seed sqlite state.db failed: %v (%s)", err, string(out))
		}
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
	defer board.Dispose()

	// Level 1 render at 120 and 60 columns with exact box border alignment.
	l1Wide := comp.Render(120)
	assertLinesFitWidth(t, "botsBoard-L1", l1Wide, 120)
	assertBoxBordersExactWidth(t, "botsBoard-L1-120", l1Wide, 120)
	l1Raw := strings.Join(l1Wide, "\n")
	if !strings.Contains(l1Raw, "\x1b[38;2;") {
		t.Errorf("expected truecolor ANSI styling in /bots render")
	}
	l1Text := stripANSI(l1Raw)
	for _, want := range []string{
		"Hermes bots · 2 profiles",
		"❯ ● default",
		"research",
		"▰", "▱",
		"OMH",
		"every 5 s · read-only",
		"│", "┷",
	} {
		if !strings.Contains(l1Text, want) {
			t.Errorf("L1 Render(120) missing %q\n%s", want, l1Text)
		}
	}
	// Verify deterministic 8×4 half-block identicon avatar glyphs appear at width 120 and 60.
	if !strings.ContainsAny(l1Text, "█▀▄") {
		t.Errorf("expected half-block identicon avatar in L1 Render(120):\n%s", l1Text)
	}
	l1Narrow := comp.Render(60)
	assertLinesFitWidth(t, "botsBoard-L1-60", l1Narrow, 60)
	assertBoxBordersExactWidth(t, "botsBoard-L1-60", l1Narrow, 60)
	if !strings.ContainsAny(stripANSI(strings.Join(l1Narrow, "\n")), "█▀▄") {
		t.Errorf("expected half-block identicon avatar in L1 Render(60)")
	}

	// Verify deterministic identicon output for same seed.
	pal := lookupThemeSpec("sky")
	av1 := renderIdenticonAvatar("default", pal)
	av2 := renderIdenticonAvatar("default", pal)
	if len(av1) != 4 || strings.Join(av1, "\n") != strings.Join(av2, "\n") {
		t.Errorf("expected deterministic 4-row identicon avatar")
	}
	for rowIdx, row := range av1 {
		if w := tui.VisibleWidth(row); w != 8 {
			t.Errorf("identicon row %d visual width = %d, want 8", rowIdx, w)
		}
	}

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
	assertBoxBordersExactWidth(t, "botsBoard-L2-120", l2Wide, 120)
	l2Narrow := comp.Render(60)
	assertLinesFitWidth(t, "botsBoard-L2-60", l2Narrow, 60)
	assertBoxBordersExactWidth(t, "botsBoard-L2-60", l2Narrow, 60)
	l2Text := stripANSI(strings.Join(l2Wide, "\n"))
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
	assertBoxBordersExactWidth(t, "botsBoard-L3-120", l3Wide, 120)
	l3Narrow := comp.Render(60)
	assertLinesFitWidth(t, "botsBoard-L3-60", l3Narrow, 60)
	assertBoxBordersExactWidth(t, "botsBoard-L3-60", l3Narrow, 60)
	l3Text := stripANSI(strings.Join(l3Wide, "\n"))
	if !strings.Contains(l3Text, "Messages (last ") || !strings.Contains(l3Text, "│") {
		t.Errorf("L3 render missing messages box:\n%s", l3Text)
	}
	if _, err := exec.LookPath("sqlite3"); err == nil {
		if !strings.Contains(l3Text, "SQLite messages loaded cleanly") {
			t.Errorf("expected real SQLite messages in L3 when sqlite3 is available:\n%s", l3Text)
		}
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

	// Configure only Anthropic provider so non-Claude models get flagged with red `!`.
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("COPILOT_API_KEY", "")

	// Seed per-model effort ladders including a fixed-effort model (empty ladder).
	routingDir := filepath.Join(omhHome, "routing")
	if err := os.MkdirAll(routingDir, 0o755); err != nil {
		t.Fatalf("mkdir routing dir: %v", err)
	}
	effortsJSON := `{"claude-haiku-5-5": [], "claude-sonnet-5-5": ["low", "medium", "high"]}`
	if err := os.WriteFile(filepath.Join(routingDir, "model-efforts.json"), []byte(effortsJSON), 0o600); err != nil {
		t.Fatalf("write model-efforts.json: %v", err)
	}

	// Seed a custom identity profile so the Custom identities section and Space toggle are exercised.
	if _, err := surf.router.identities.Set(IdentityProfile{
		Name:     "sec-auditor",
		Role:     "reviewer",
		Category: "review",
		ModelChain: []IdentityModelEntry{
			{ModelID: "claude-opus-5-5", ReasoningEffort: "high"},
		},
	}); err != nil {
		t.Fatalf("seed custom identity failed: %v", err)
	}

	// --- Scenario A: Edit head model & effort, toggle custom identity with Space, then Save with Enter ---
	var comp sdk.RemoteComponent = newModelTableComponent(surf)
	tbl := comp.(*modelTableComponent)

	wide := comp.Render(120)
	assertLinesFitWidth(t, "modelTable-120", wide, 120)
	wideRaw := strings.Join(wide, "\n")
	if !strings.Contains(wideRaw, "\x1b[38;2;") {
		t.Errorf("expected truecolor ANSI styling in /omh-model render")
	}
	wideText := stripANSI(wideRaw)
	for _, want := range []string{
		"⚚ OMH · Model chains",
		"providers: anthropic",
		"CATEGORY", "PURPOSE", "HEAD MODEL", "EFFORT", "STATE",
		"❯", "◂", "▸", "■", "· built-in",
		"── Custom identities ──",
		"custom:sec-auditor",
		"● enabled",
		"fixed",
		"! ",
		"↑↓ category   ←→ head model   −/+ effort   Space toggle   d default   ⏎ save   esc cancel",
	} {
		if !strings.Contains(wideText, want) {
			t.Errorf("Render(120) missing %q\n%s", want, wideText)
		}
	}

	// At width 60, PURPOSE column is omitted cleanly and lines fit.
	narrow := comp.Render(60)
	assertLinesFitWidth(t, "modelTable-60", narrow, 60)
	if strings.Contains(stripANSI(strings.Join(narrow, "\n")), "PURPOSE") {
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

	// Navigate down to custom:sec-auditor row and toggle disabled with Space.
	for tbl.cursor < len(tbl.rows)-1 && !tbl.rows[tbl.cursor].IsIdentity {
		_, _ = comp.HandleInput("j")
	}
	if !tbl.rows[tbl.cursor].IsIdentity {
		t.Fatalf("expected to reach custom identity row")
	}
	_, _ = comp.HandleInput(" ")
	if tbl.rows[tbl.cursor].Enabled {
		t.Fatalf("expected Space to toggle custom identity enabled -> false")
	}

	editedText := stripANSI(strings.Join(comp.Render(120), "\n"))
	if !strings.Contains(editedText, "● edited") || !strings.Contains(editedText, "× disabled") || !strings.Contains(editedText, "2 unsaved change(s)") {
		t.Errorf("expected edited glyph, disabled identity, and 2 unsaved changes in render:\n%s", editedText)
	}

	// Enter saves atomically to routing/model-chains.json and routing/identities.json and closes.
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
	savedProf, found, err := surf.router.identities.Get("sec-auditor")
	if err != nil || !found {
		t.Fatalf("expected custom identity sec-auditor on disk: found=%v err=%v", found, err)
	}
	if savedProf.IsEnabled() {
		t.Errorf("expected persisted sec-auditor Enabled=false after Space + Enter")
	}

	// --- Scenario B: Re-open, verify ◆ customized & × disabled, edit, then Cancel with Esc (no-op) ---
	comp2 := newModelTableComponent(surf)
	reopened := stripANSI(strings.Join(comp2.Render(120), "\n"))
	if !strings.Contains(reopened, "◆ customized") || !strings.Contains(reopened, "× disabled") {
		t.Errorf("expected saved category '◆ customized' and disabled identity '× disabled':\n%s", reopened)
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

func TestDAGPagerSelectionNotYankedFromNodeZeroOnReload(t *testing.T) {
	surf, _, _ := newTestSurface(t)
	run, _, err := surf.dags.Create("sess-focus", "dag-focus", "Focus Test", []DAGNode{
		{ID: "plan", Title: "Plan", Category: "architect"},
		{ID: "api", Title: "API", Category: "deep", DependsOn: []string{"plan"}},
	})
	if err != nil {
		t.Fatalf("create DAG failed: %v", err)
	}
	if _, err := surf.dags.UpdateNode(run.RunID, "plan", "completed", "ok", ""); err != nil {
		t.Fatalf("update plan failed: %v", err)
	}
	if _, err := surf.dags.UpdateNode(run.RunID, "api", "running", "working", ""); err != nil {
		t.Fatalf("update api failed: %v", err)
	}

	dag := newDAGPagerComponent(surf, run.RunID)
	defer dag.Dispose()
	// Initial focus lands on the running node ("api", index 1).
	if got := dag.selectedNodeIDLocked(); got != "api" {
		t.Fatalf("expected initial focus on running node 'api', got %q", got)
	}
	// User navigates back to node 0 ("plan").
	_, _ = dag.HandleInput("k")
	if got := dag.selectedNodeIDLocked(); got != "plan" {
		t.Fatalf("expected 'k' to select node 0 'plan', got %q", got)
	}
	// Subsequent reload (as triggered by the 1s ticker) must NOT yank focus back to 'api'.
	dag.mu.Lock()
	dag.reloadLocked()
	got := dag.selectedNodeIDLocked()
	dag.mu.Unlock()
	if got != "plan" {
		t.Fatalf("reloadLocked yanked selection off node 0 'plan' to %q", got)
	}
}

func TestDisabledIdentitySkippedInRoutingAndPreservedOnSet(t *testing.T) {
	surf, _, _ := newTestSurface(t)
	_, err := surf.router.identities.Set(IdentityProfile{
		Name:     "sec-auditor",
		Role:     "reviewer",
		Category: "review",
		ModelChain: []IdentityModelEntry{
			{ModelID: "custom-disabled-model", ReasoningEffort: "high"},
		},
	})
	if err != nil {
		t.Fatalf("set identity failed: %v", err)
	}
	if err := surf.router.identities.SetEnabled("sec-auditor", false); err != nil {
		t.Fatalf("SetEnabled(false) failed: %v", err)
	}
	// Updating the profile with Enabled == nil must preserve Enabled=false.
	updated, err := surf.router.identities.Set(IdentityProfile{
		Name:     "sec-auditor",
		Role:     "reviewer",
		Category: "review",
		Prompt:   "updated prompt",
		ModelChain: []IdentityModelEntry{
			{ModelID: "custom-disabled-model", ReasoningEffort: "high"},
		},
	})
	if err != nil {
		t.Fatalf("second Set failed: %v", err)
	}
	if updated.IsEnabled() {
		t.Fatalf("expected IdentityStore.Set with nil Enabled to preserve existing Enabled=false")
	}
	// RouteRouter.Resolve must skip the disabled custom identity.
	dec, ok, err := surf.router.Resolve(delegation.SpawnRequest{
		Identity: "sec-auditor",
		Category: "quick",
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if !ok || dec.Model == "custom-disabled-model" {
		t.Fatalf("expected disabled identity to be skipped in routing, got ok=%v dec=%+v", ok, dec)
	}
}

func TestLoadProfileJSONLSessionsBoundedAndLazyMessages(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	base := time.Now().UTC().Add(-2 * time.Hour)
	for i := range 45 {
		p := filepath.Join(sessDir, "sess-"+string(rune('a'+i/26))+string(rune('a'+i%26))+".jsonl")
		body := `{"role":"user","content":"hello","timestamp":"2026-11-28T10:00:00Z"}` + "\n" +
			`{"role":"assistant","content":"world","timestamp":"2026-11-28T10:01:00Z"}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write jsonl %d: %v", i, err)
		}
		mt := base.Add(time.Duration(i) * time.Minute)
		_ = os.Chtimes(p, mt, mt)
	}
	rows, _ := loadProfileJSONLSessions(dir, time.Now().UTC())
	if len(rows) != 40 {
		t.Fatalf("expected at most 40 newest JSONL sessions, got %d", len(rows))
	}
	if len(rows[0].Messages) != 0 {
		t.Fatalf("expected Level 1/2 metadata scan to defer full message parsing, got %d messages", len(rows[0].Messages))
	}
	if rows[0].Msgs != 2 || rows[0].JSONLPath == "" {
		t.Fatalf("expected line count=2 and JSONLPath set, got msgs=%d path=%q", rows[0].Msgs, rows[0].JSONLPath)
	}
	msgs, ok := loadJSONLSessionMessages(rows[0].JSONLPath)
	if !ok || len(msgs) != 2 || msgs[1].Text != "world" {
		t.Fatalf("expected lazy JSONL load to return 2 messages, got ok=%v msgs=%+v", ok, msgs)
	}
}
