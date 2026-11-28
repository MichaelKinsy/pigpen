package frontend

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
	"time"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
)

func TestOmpWave7BashShellNodeAndFallback(t *testing.T) {
	// 1. With BuildShellPresentation set -> native `shell` node with presentation, lang, preview, liveOrigin, cwd.
	h := readyOmpRoles(t)
	var gotCmd string
	var gotRunning bool
	h.session.buildShellPresentationFn = func(command string, running bool, exitCode *int, output string) map[string]any {
		gotCmd = command
		gotRunning = running
		return map[string]any{
			"kind":  "pipeline",
			"steps": []any{map[string]any{"command": command}},
		}
	}
	tool := fe.ToolCard{
		Name:      "bash",
		Status:    fe.ToolRunning,
		Arguments: map[string]any{"command": "echo hi | wc -c", "cwd": "/tmp/work"},
		Result:    []string{"echo hi | wc -c"},
		ResultView: &fe.View{
			Root: fe.ViewNode{
				ID:   "omp.tool.bash",
				Kind: fe.ViewKindVStack,
				Rows: 1, Width: 80,
				Children: []fe.ViewNode{
					{ID: "omp.tool.bash.command", Kind: fe.ViewKindText, Rows: 1, Width: 80},
				},
			},
		},
	}
	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "b1", Index: 0, Node: tool})
	cmdNode := node(ops, "n.b1.v0")
	if cmdNode == nil || cmdNode["k"] != "shell" {
		t.Fatalf("bash command node = %#v, want kind=shell", cmdNode)
	}
	p := props(cmdNode)
	if p["role"] != "omp.tool.bash.command" || p["lang"] != "bash" || p["liveOrigin"] != true || p["replayEpoch"] != float64(0) || p["cwd"] != nil {
		t.Fatalf("bash shell props (running) = %#v", p)
	}
	if pres, ok := p["presentation"].(map[string]any); !ok || pres["kind"] != "pipeline" {
		t.Fatalf("bash shell presentation = %#v", p["presentation"])
	}
	if !reflect.DeepEqual(p["preview"], map[string]any{"lines": float64(6)}) {
		t.Fatalf("bash shell preview = %#v", p["preview"])
	}
	if gotCmd != "echo hi | wc -c" || !gotRunning {
		t.Fatalf("callback got cmd=%q running=%v", gotCmd, gotRunning)
	}
	// 1b. When completed, cwd is emitted via patch/set on the shell node.
	doneTool := tool
	doneTool.Status = fe.ToolDone
	opsDone := h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "b1", Index: 0, Node: doneTool})
	if pDone := shownBy(opsDone)["n.b1.v0"]; pDone == nil || pDone["cwd"] != "/tmp/work" {
		t.Fatalf("completed bash shell cwd = %#v, want /tmp/work", pDone)
	}

	// 2. When BuildShellPresentation is nil -> falls back to `code` node.
	h2 := readyOmpRoles(t)
	ops2 := h2.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "b2", Index: 0, Node: tool})
	cmdNode2 := node(ops2, "n.b2.v0")
	if cmdNode2 == nil || cmdNode2["k"] != "code" || props(cmdNode2)["lang"] != "bash" {
		t.Fatalf("fallback bash command node = %#v, want kind=code", cmdNode2)
	}
}

func TestOmpWave7EvalTableAndJobTileChart(t *testing.T) {
	h := readyOmpRoles(t)

	// 1. omp.tool.eval.table -> native `table` with cols (numeric aligned end) and rows.
	evalTool := fe.ToolCard{
		Name:   "eval",
		Status: fe.ToolDone,
		Result: []string{"k · v", "a · 1", "b · 42.5"},
		ResultView: &fe.View{
			Root: fe.ViewNode{
				ID:   "omp.tool.eval",
				Kind: fe.ViewKindVStack,
				Rows: 3, Width: 80,
				Children: []fe.ViewNode{
					{
						ID:   "omp.tool.eval.table#0.0",
						Kind: fe.ViewKindLines,
						Rows: 3, Width: 80,
						List: &fe.ViewList{
							Items: []fe.ViewListItem{
								{Label: "name", Columns: []string{"count"}, Detail: "align=start,end"},
								{Label: "alpha", Columns: []string{"12"}},
								{Label: "beta", Columns: []string{"34.5"}},
							},
						},
					},
				},
			},
		},
	}
	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "e1", Index: 0, Node: evalTool})
	tbl := node(ops, "n.e1.v0")
	if tbl == nil || tbl["k"] != "table" || props(tbl)["role"] != "omp.tool.eval.table" {
		t.Fatalf("eval table = %#v", tbl)
	}
	cols, _ := props(tbl)["cols"].([]any)
	rows, _ := props(tbl)["rows"].([]any)
	if len(cols) != 2 || len(rows) != 2 {
		t.Fatalf("table cols=%#v rows=%#v", cols, rows)
	}
	c0 := cols[0].(map[string]any)
	c1 := cols[1].(map[string]any)
	if c0["id"] != "c0" || c0["head"] != "name" || c0["align"] != nil {
		t.Fatalf("c0 = %#v", c0)
	}
	if c1["id"] != "c1" || c1["head"] != "count" || c1["align"] != "end" {
		t.Fatalf("c1 = %#v", c1)
	}
	r0 := rows[0].(map[string]any)
	if r0["id"] != "r0" || !reflect.DeepEqual(r0["cells"], map[string]any{"c0": "alpha", "c1": "12"}) {
		t.Fatalf("r0 = %#v", r0)
	}

	// 2. omp.job.tile.chart with spark series -> native `chart` (kind="spark", size="sm").
	waitTool := fe.ToolCard{
		Name:   "wait",
		Status: fe.ToolRunning,
		Result: []string{"rps 42/s", "10,20,42"},
		ResultView: &fe.View{
			Root: fe.ViewNode{
				ID:   "omp.tool.wait",
				Kind: fe.ViewKindVStack,
				Rows: 2, Width: 80,
				Children: []fe.ViewNode{
					{
						ID:   "omp.job.tile.chart#0",
						Kind: fe.ViewKindVStack,
						Rows: 2, Width: 80,
						Children: []fe.ViewNode{
							{ID: "omp.job.tile.chart.summary#0", Kind: fe.ViewKindText, Rows: 1, Width: 80},
							{ID: "omp.job.tile.chart.series#0", Kind: fe.ViewKindText, Rows: 1, Width: 80},
						},
					},
				},
			},
		},
	}
	ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "w1", Index: 1, Node: waitTool})
	chart := node(ops, "n.w1.v0")
	if chart == nil || chart["k"] != "chart" {
		t.Fatalf("job tile chart = %#v, want kind=chart", chart)
	}
	cp := props(chart)
	if cp["role"] != "omp.job.tile.chart" || cp["kind"] != "bars" || cp["size"] != "sm" || cp["summary"] != "rps 42/s" || cp["min"] != float64(10) || cp["max"] != float64(42) || cp["token"] != "accent" {
		t.Fatalf("chart props = %#v", cp)
	}
	series, _ := cp["series"].([]any)
	if len(series) != 3 || series[2].(map[string]any)["value"] != float64(42) {
		t.Fatalf("chart series = %#v", series)
	}
}

func TestOmpWave7UserAndAssistantChromeAndActions(t *testing.T) {
	h := readyOmpRoles(t)

	// Mount editor so edit-turn and runCommand work.
	_ = h.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 0,
		Node: fe.Editor{Text: "draft", Cursor: 5, Sendable: true},
	})

	// 1. User message card emits omp.user.tools (copy + rewind) and actions.hold.
	ops := h.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionMain, ID: "u1", Index: 0,
		Node: fe.MarkdownText{Role: fe.RoleUser, Text: "Hello world"},
	})
	uCard := node(ops, "n.u1")
	if uCard == nil || uCard["k"] != "card" || props(uCard)["role"] != "omp.user" {
		t.Fatalf("user card = %#v", uCard)
	}
	uActions, _ := props(uCard)["actions"].(map[string]any)
	uHold, _ := uActions["hold"].([]any)
	if len(uHold) != 4 ||
		uHold[0].(map[string]any)["id"] != "copy-message" ||
		uHold[1].(map[string]any)["id"] != "edit-turn" ||
		uHold[2].(map[string]any)["id"] != "rewind-turn" ||
		uHold[3].(map[string]any)["id"] != "fork-turn" {
		t.Fatalf("user hold entries = %#v", uHold)
	}
	toolsRow := node(ops, "n.u1.tools")
	if toolsRow == nil || toolsRow["k"] != "row" || props(toolsRow)["role"] != "omp.user.tools" {
		t.Fatalf("user tools row = %#v", toolsRow)
	}
	if copyIcon := node(ops, "n.u1.tools.copy"); copyIcon == nil || props(copyIcon)["role"] != "omp.user.tool" ||
		!reflect.DeepEqual(props(copyIcon)["actions"], map[string]any{"click": "copy-message"}) {
		t.Fatalf("user copy icon = %#v", copyIcon)
	}
	if rewindIcon := node(ops, "n.u1.tools.rewind"); rewindIcon == nil || props(rewindIcon)["role"] != "omp.user.tool" ||
		!reflect.DeepEqual(props(rewindIcon)["actions"], map[string]any{"click": "rewind"}) {
		t.Fatalf("user rewind icon = %#v", rewindIcon)
	}

	// Clicking user copy writes OSC 52 clipboard.
	h.out.take()
	h.event(`{"ev":"action","sf":"pig","id":"n.u1.tools.copy","act":"copy-message"}`)
	h.synced()
	wantOSC52 := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("Hello world")) + "\x07"
	if got := h.out.take(); !strings.Contains(got, wantOSC52) {
		t.Fatalf("copy-message did not write OSC 52 %q; out=%q", wantOSC52, got)
	}
	// Clicking user rewind sends /rewind; hold fork-turn sends /fork.
	h.event(`{"ev":"action","sf":"pig","id":"n.u1.tools.rewind","act":"rewind"}`)
	h.event(`{"ev":"action","sf":"pig","id":"n.u1","act":"fork-turn"}`)
	h.synced()
	if !reflect.DeepEqual(h.sends, []string{"/rewind", "/fork"}) {
		t.Fatalf("sends = %#v, want [/rewind /fork]", h.sends)
	}

	// Hold edit-turn populates composer via Env.Edit.
	h.event(`{"ev":"action","sf":"pig","id":"n.u1","act":"edit-turn"}`)
	h.synced()
	if len(h.edits) != 1 || h.edits[0] != (fe.Edit{From: 0, To: 5, Text: "Hello world", Cursor: 11, Len: 5}) {
		t.Fatalf("edits = %#v", h.edits)
	}

	// 2. Assistant message: streaming -> settled -> TurnUsageRecord attaches row[omp.turn.usage] + actions.hold.
	_ = h.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionMain, ID: "a1", Index: 1,
		Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "Answer", Streaming: true},
	})
	_ = h.applyOps(fe.Op{
		Kind: fe.Update, Region: fe.RegionMain, ID: "a1", Index: 1,
		Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "Answer text", Streaming: false},
	})
	h.session.onTurnUsage(TurnUsageRecord{
		TurnSeq:   1,
		Input:     1200,
		Output:    300,
		Cost:      0.02,
		ElapsedMs: 1200,
	})
	ops = takeOps(h)
	aCol := node(ops, "n.a1")
	if aCol == nil || aCol["k"] != "col" || props(aCol)["role"] != "omp.assistant" {
		t.Fatalf("assistant col = %#v", aCol)
	}
	aActions, _ := props(aCol)["actions"].(map[string]any)
	aHold, _ := aActions["hold"].([]any)
	if len(aHold) != 3 ||
		aHold[0].(map[string]any)["id"] != "copy-answer" ||
		aHold[1].(map[string]any)["id"] != "rewind-turn" ||
		aHold[2].(map[string]any)["id"] != "fork-turn" {
		t.Fatalf("assistant hold entries = %#v", aHold)
	}
	usageRow := node(ops, "n.a1.turn-usage")
	if usageRow == nil || usageRow["k"] != "row" || props(usageRow)["role"] != "omp.turn.usage" {
		t.Fatalf("turn usage row = %#v", usageRow)
	}
	usageText := node(ops, "n.a1.turn-usage.text")
	if usageText == nil || props(usageText)["text"] != "1.2s · 1.5K tok · $0.02 this turn" {
		t.Fatalf("turn usage text = %#v", usageText)
	}
	if title, _ := props(usageRow)["title"].(string); !strings.Contains(title, "1,500 tokens (1,200 in · 300 out)") {
		t.Fatalf("turn usage title = %q", title)
	}

	// Clicking copy-answer writes OSC 52 with assistant answer text.
	h.out.take()
	h.event(`{"ev":"action","sf":"pig","id":"n.a1.turn-usage.copy","act":"copy-answer"}`)
	h.synced()
	wantAnsOSC52 := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("Answer text")) + "\x07"
	if got := h.out.take(); !strings.Contains(got, wantAnsOSC52) {
		t.Fatalf("copy-answer did not write OSC 52 %q", wantAnsOSC52)
	}
}

func TestOmpWave7ComposerMetaMarksAndBillingSummary(t *testing.T) {
	// 1. Billing summary: zero meta identical to Wave 6; premium requests & advisor spend formatted.
	f := fe.Footer{UsingSubscription: true, UsageTotals: fe.UsageTotals{Cost: 0.54}}
	if got := ompCostSummaryWithMeta(f, 1.2, ComposerMeta{}); got != ompCostSummary(f, 1.2) {
		t.Fatalf("zero meta mismatch: %q vs %q", got, ompCostSummary(f, 1.2))
	}
	gotPremium := ompCostSummaryWithMeta(f, 0, ComposerMeta{PremiumRequests: 12})
	if want := ompSubscriptionIcon + " 0.54 ★ 12"; gotPremium != want {
		t.Fatalf("premium summary = %q, want %q", gotPremium, want)
	}
	gotAdvisor := ompCostSummaryWithMeta(
		fe.Footer{UsageTotals: fe.UsageTotals{Cost: 1.5}},
		0,
		ComposerMeta{PremiumRequests: 3, AdvisorCost: 0.25, AdvisorSub: false},
	)
	if want := "$1.50 ★ 3 + " + ompAdvisorIcon + " 0.25"; gotAdvisor != want {
		t.Fatalf("advisor summary = %q, want %q", gotAdvisor, want)
	}
	gotAdvisorDiffUnit := ompCostSummaryWithMeta(
		fe.Footer{UsingSubscription: true, UsageTotals: fe.UsageTotals{Cost: 0.50}},
		0,
		ComposerMeta{AdvisorCost: 0.15, AdvisorSub: false},
	)
	if want := ompSubscriptionIcon + " 0.50 + " + ompAdvisorIcon + " $0.15"; gotAdvisorDiffUnit != want {
		t.Fatalf("advisor diff unit = %q, want %q", gotAdvisorDiffUnit, want)
	}

	// 2. Context meter marks, parts, and Auto-compact at N% title.
	h := readyOmpRoles(t)
	footer := sampleFooter()
	footer.AutoCompact = true
	footer.ContextUsage = fe.ContextUsage{Tokens: 160000, ContextWindow: 200000, Percent: 80}
	_ = h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{Sendable: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "footer", Index: 1, Node: footer},
	)
	h.session.onComposerMetaChange(ComposerMeta{
		HasBoundaries:      true,
		AutoCompactEnabled: true,
		ThresholdPercent:   85,
		SpeculationPercent: 75,
		SpeculationState:   "running",
	})
	ops := takeOps(h)
	ctx := shownBy(ops)["dock.editor.editor.context"]
	if ctx == nil {
		t.Fatalf("missing context meter update: %#v", ops)
	}
	title, _ := ctx["title"].(string)
	if !strings.Contains(title, "Auto-compact at 85%") || !strings.Contains(title, "Compaction summary in progress") {
		t.Fatalf("context title = %q", title)
	}
	marks, _ := ctx["marks"].([]any)
	if len(marks) != 2 {
		t.Fatalf("context marks = %#v, want 2", marks)
	}
	m0 := marks[0].(map[string]any)
	m1 := marks[1].(map[string]any)
	if m0["icon"] != "context.speculation" || m0["tone"] != "accent" || m0["at"] != 0.75 {
		t.Fatalf("speculation mark = %#v", m0)
	}
	if m1["icon"] != "context.compaction" || m1["at"] != 0.85 || m1["title"] != "Auto-compact at 85%" {
		t.Fatalf("compaction mark = %#v", m1)
	}
	parts, _ := ctx["parts"].([]any)
	if len(parts) != 2 || parts[1].(map[string]any)["token"] != "accent" {
		t.Fatalf("context parts = %#v", parts)
	}
}

func TestOmpWave7JobsHudPillAndDockProgressHud(t *testing.T) {
	h := readyOmpRoles(t)
	now := time.Date(2026, 11, 27, 12, 0, 10, 0, time.UTC)
	h.session.now = func() time.Time { return now }

	// Mount dock with editor.
	_ = h.applyOps(fe.Op{
		Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 0,
		Node: fe.Editor{Sendable: true},
	})
	// Empty progressHud container and its 3 empty col children exist in dock.
	var progressSlot *tern.Node
	for _, dn := range h.session.view().Dock.(tern.Nodes) {
		n := dn.Node()
		if n.Props["key"] == "progressHud" {
			progressSlot = &n
			break
		}
	}
	if progressSlot == nil || len(progressSlot.Children) != 3 {
		t.Fatalf("dock progressHud slot = %#v, want 3 children", progressSlot)
	}
	wantRoles := []string{"omp.hud.judge", "omp.hud.downloads", "omp.hud.compaction"}
	for i, wantRole := range wantRoles {
		ch := progressSlot.Children[i].Node()
		if ch.Kind != tern.KindCol || ch.Props["role"] != wantRole {
			t.Fatalf("progressHud child[%d] = %#v, want col[%s]", i, ch, wantRole)
		}
	}

	// Publish running agents + running jobs + active judge/download/compaction HUDs.
	h.session.onAgentHubSnapshot(AgentHubSnapshot{RunningCount: 1, InspectableCount: 1})
	_ = takeOps(h)
	h.session.onProgressHudChange(ProgressHudSnapshot{
		RunningJobs: 2,
		Jobs: []JobHudItem{
			{ID: "bg_1", Kind: "bash", Label: "bench", Summary: "running bench", GlanceText: "42 req/s", SparkVals: []float64{10, 25, 42}},
		},
		JudgeBatches: []JudgeBatchHudItem{
			{ID: "jb1", Intent: "Grading outputs", Done: 3, Total: 10, Failed: 1, Cost: 0.04},
		},
		Downloads: []DownloadHudItem{
			{ID: "dl1", Label: "Installing ripgrep", State: "running", Loaded: 5120, Total: 10240, Unit: "bytes"},
		},
		Compaction: CompactionHudItem{
			Active:      true,
			Label:       "Compacting context in the background",
			StartedAtMs: now.Add(-4 * time.Second).UnixMilli(),
		},
	})
	ops := takeOps(h)

	// Both agents and jobs segments appear inside omp.hud.pill.
	jobsSeg := node(ops, "dock.hud.running.jobs")
	if jobsSeg == nil || props(jobsSeg)["role"] != "omp.hud.seg" ||
		!reflect.DeepEqual(props(jobsSeg)["actions"], map[string]any{"click": "jobs.open"}) {
		t.Fatalf("jobs seg = %#v", jobsSeg)
	}
	if glance := node(ops, "dock.hud.running.jobs.glance"); glance == nil || props(glance)["role"] != "omp.hud.glance" {
		t.Fatalf("jobs glance = %#v", glance)
	}
	if spark := node(ops, "dock.hud.running.jobs.spark"); spark == nil || spark["k"] != "chart" || props(spark)["role"] != "omp.hud.spark" {
		t.Fatalf("jobs spark = %#v", spark)
	}
	if jRow := node(ops, "dock.progressHud.judge.jb1"); jRow == nil || props(jRow)["role"] != "omp.hud.progress" {
		t.Fatalf("judge progress row = %#v", jRow)
	}
	if dRow := node(ops, "dock.progressHud.downloads.dl1"); dRow == nil || props(dRow)["role"] != "omp.hud.progress" {
		t.Fatalf("download progress row = %#v", dRow)
	}
	if cRow := node(ops, "dock.progressHud.compaction.run"); cRow == nil || props(cRow)["role"] != "omp.hud.progress" {
		t.Fatalf("compaction progress row = %#v", cRow)
	}

	// Clicking jobs segment sends /jobs.
	h.event(`{"ev":"action","sf":"pig","id":"dock.hud.running.jobs","act":"jobs.open"}`)
	h.synced()
	if !reflect.DeepEqual(h.sends, []string{"/jobs"}) {
		t.Fatalf("jobs click sends = %#v, want [/jobs]", h.sends)
	}
}

func TestOmpWave7TurnUsageOrderingAndGating(t *testing.T) {
	// (a) onTurnUsage arrives BEFORE Streaming:false update -> deferred and attached on settle.
	h := readyOmpRoles(t)
	_ = h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "u1", Index: 0, Node: fe.MarkdownText{Role: fe.RoleUser, Text: "hi"}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a1", Index: 1, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "part", Streaming: true}},
	)
	h.session.onTurnUsage(TurnUsageRecord{TurnSeq: 1, Input: 100, CacheWrite: 50, Output: 25, Cost: 0.01})
	if ops := takeOps(h); node(ops, "n.a1.turn-usage") != nil {
		t.Fatalf("usage attached while still streaming: %#v", ops)
	}
	ops := h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "a1", Index: 1, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "part done", Streaming: false}})
	usageRow := node(ops, "n.a1.turn-usage")
	if usageRow == nil {
		t.Fatalf("expected deferred turn usage on settle, ops=%#v", ops)
	}
	if title, _ := props(usageRow)["title"].(string); !strings.Contains(title, "175 tokens (150 in · 25 out)") {
		t.Fatalf("cache-write token count title = %q", title)
	}

	// (b) Cost-only record (0 tokens, Cost > 0) still attaches.
	_ = h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "u2", Index: 2, Node: fe.MarkdownText{Role: fe.RoleUser, Text: "second"}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a2", Index: 3, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "ok", Streaming: false}},
	)
	h.session.onTurnUsage(TurnUsageRecord{TurnSeq: 2, Cost: 0.03})
	if ops2 := takeOps(h); node(ops2, "n.a2.turn-usage") == nil {
		t.Fatalf("cost-only turn usage not attached: %#v", ops2)
	}

	// (c) text -> tool -> final-text turn attaches usage ONLY to final text.
	_ = h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "u3", Index: 4, Node: fe.MarkdownText{Role: fe.RoleUser, Text: "run tool"}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a3pre", Index: 5, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "let me check", Streaming: false}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "t3", Index: 6, Node: fe.ToolCard{Name: "read", Status: fe.ToolDone}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a3fin", Index: 7, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "all good", Streaming: true}},
	)
	h.session.onTurnUsage(TurnUsageRecord{TurnSeq: 3, Input: 200, Output: 50})
	if ops3 := takeOps(h); node(ops3, "n.a3pre.turn-usage") != nil {
		t.Fatalf("usage leaked onto pre-tool assistant text: %#v", ops3)
	}
	ops3Fin := h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "a3fin", Index: 7, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "all good!", Streaming: false}})
	if node(ops3Fin, "n.a3fin.turn-usage") == nil {
		t.Fatalf("usage not attached to final assistant text: %#v", ops3Fin)
	}

	// (d) Empty final text never leaks onto previous turn's reply.
	_ = h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "u4", Index: 8, Node: fe.MarkdownText{Role: fe.RoleUser, Text: "empty turn"}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a4", Index: 9, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "   ", Streaming: false}},
	)
	h.session.onTurnUsage(TurnUsageRecord{TurnSeq: 4, Input: 10, Output: 5})
	if ops4 := takeOps(h); len(ops4) != 0 {
		t.Fatalf("empty final text leaked usage: %#v", ops4)
	}

	// (e) onTurnUsage arrives BEFORE the assistant reply node is inserted at all -> stashed on RoleUser and attached on direct settled Insert.
	_ = h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "u5", Index: 10, Node: fe.MarkdownText{Role: fe.RoleUser, Text: "fast turn"}},
	)
	h.session.onTurnUsage(TurnUsageRecord{TurnSeq: 5, Input: 120, Output: 35, ElapsedMs: 18})
	ops5 := h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "a5", Index: 11, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "fast reply", Streaming: false}},
	)
	if node(ops5, "n.a5.turn-usage") == nil {
		t.Fatalf("expected stashed turn usage to attach on direct settled insert, ops=%#v", ops5)
	}
}

func TestOmpWave7EditTurnUTF16NonASCII(t *testing.T) {
	h := readyOmpRoles(t)
	// Emoji (2 UTF-16 code units) + CJK (1 UTF-16 code unit each).
	draft := "🔥日本語" // UTF-16 len = 2 + 3 = 5 (byte len = 13)
	msg := "✨你好"    // UTF-16 len = 1 + 2 = 3 (byte len = 9)
	_ = h.applyOps(
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{Text: draft, Cursor: 5, Sendable: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "u1", Index: 0, Node: fe.MarkdownText{Role: fe.RoleUser, Text: msg}},
	)
	h.event(`{"ev":"action","sf":"pig","id":"n.u1","act":"edit-turn"}`)
	h.synced()
	want := fe.Edit{From: 0, To: 5, Text: msg, Cursor: 3, Len: 5}
	if len(h.edits) != 1 || h.edits[0] != want {
		t.Fatalf("non-ASCII edit-turn = %#v, want %#v", h.edits, want)
	}
}
