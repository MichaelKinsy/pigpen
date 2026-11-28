package ansi

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

func TestWave7TurnUsageAndSubagentGuard(t *testing.T) {
	surface.ResetForTests()
	origSub := IsSubagent
	defer func() {
		IsSubagent = origSub
		surface.ResetForTests()
	}()

	IsSubagent = func(_ sdk.Context) bool { return false }

	st := newChromeState()
	st.inCostPer1M = 2.0
	st.outCostPer1M = 8.0
	st.cacheReadPer1M = 0.2
	st.cacheWritePer1M = 2.5

	st.startTurn()
	// Backdate turnStartedAt so tok/s (>100ms) and ElapsedMs are measurable.
	st.mu.Lock()
	st.turnStartedAt = time.Now().Add(-250 * time.Millisecond)
	st.mu.Unlock()

	st.onTurnEnd(sdk.Context{}, map[string]any{
		"message": map[string]any{
			"usage": map[string]any{
				"input":           100.0,
				"output":          50.0,
				"cacheRead":       20.0,
				"cacheWrite":      10.0,
				"premiumRequests": 1.0,
				"cost":            map[string]any{"total": 0.0125},
			},
		},
	})
	st.onTurnEnd(sdk.Context{}, map[string]any{
		"message": map[string]any{
			"usage": map[string]any{
				"input":  200.0,
				"output": 100.0,
			},
		},
	})

	meta := surface.GetComposerMeta()
	if meta.TokensPerSec <= 0 {
		t.Fatalf("expected positive TokensPerSec after turn_end, got %v", meta.TokensPerSec)
	}
	if meta.PremiumRequests != 1.0 {
		t.Fatalf("expected PremiumRequests=1.0, got %v", meta.PremiumRequests)
	}

	st.endTurn(sdk.Context{})

	rec, ok := surface.LatestTurnUsage()
	if !ok {
		t.Fatalf("expected surface.LatestTurnUsage() to be recorded at agent_end")
	}
	if rec.TurnSeq != 1 {
		t.Errorf("TurnSeq = %d, want 1", rec.TurnSeq)
	}
	if rec.Input != 300 || rec.Output != 150 || rec.CacheRead != 20 || rec.CacheWrite != 10 {
		t.Errorf("tokens = (%d,%d,%d,%d), want (300,150,20,10)", rec.Input, rec.Output, rec.CacheRead, rec.CacheWrite)
	}
	wantCost := 0.0125 + (200.0*2.0+100.0*8.0)/1_000_000.0
	if math.Abs(rec.Cost-wantCost) > 1e-9 {
		t.Errorf("Cost = %.10f, want %.10f", rec.Cost, wantCost)
	}
	if rec.ElapsedMs < 200 {
		t.Errorf("ElapsedMs = %d, want >= 200", rec.ElapsedMs)
	}
	if rec.TimestampMs <= 0 {
		t.Errorf("TimestampMs = %d, want > 0", rec.TimestampMs)
	}

	// Subagent turns MUST NOT publish surface.RecordTurnUsage.
	surface.ClearTurnUsage()
	IsSubagent = func(_ sdk.Context) bool { return true }
	st.startTurn()
	st.onTurnEnd(sdk.Context{}, map[string]any{
		"message": map[string]any{
			"usage": map[string]any{"input": 50.0, "output": 25.0},
		},
	})
	st.endTurn(sdk.Context{})
	if _, ok := surface.LatestTurnUsage(); ok {
		t.Fatalf("expected subagent endTurn NOT to publish surface.RecordTurnUsage")
	}
}

func TestWave7CompactionBoundariesMath(t *testing.T) {
	// 1. 128K text-only model with defaults: ThresholdPercent == 85.0, SpeculationPercent == 74.375
	defaultPol := compactionPolicy{
		Enabled:          true,
		ThresholdTokens:  -1,
		ThresholdPercent: -1,
		AsyncEnabled:     true,
		MethodOrder:      defaultCompactionMethodOrder,
	}
	has, thresh, spec := computeCompactionBoundaries(defaultPol, 128000, false)
	if !has {
		t.Fatalf("expected hasBoundaries=true for 128K default text-only model")
	}
	if math.Abs(thresh-85.0) > 1e-9 {
		t.Errorf("128K text-only ThresholdPercent = %v, want 85.0", thresh)
	}
	if math.Abs(spec-74.375) > 1e-9 {
		t.Errorf("128K text-only SpeculationPercent = %v, want 74.375", spec)
	}

	// 2. Vision model with default methodOrder (snapcompact precedes handoff): SpeculationPercent == 0
	has, thresh, spec = computeCompactionBoundaries(defaultPol, 128000, true)
	if !has {
		t.Fatalf("expected hasBoundaries=true for 128K vision model")
	}
	if math.Abs(thresh-85.0) > 1e-9 {
		t.Errorf("128K vision ThresholdPercent = %v, want 85.0", thresh)
	}
	if spec != 0 {
		t.Errorf("128K vision SpeculationPercent = %v, want 0 (snapcompact leads)", spec)
	}

	// 3. Custom thresholdPercent = 70
	pctPol := defaultPol
	pctPol.ThresholdPercent = 70
	has, thresh, spec = computeCompactionBoundaries(pctPol, 128000, false)
	if !has || math.Abs(thresh-70.0) > 1e-9 {
		t.Errorf("thresholdPercent=70 => has=%v thresh=%v, want true, 70.0", has, thresh)
	}
	// threshold = 89600, lead = floor(89600*0.125)=11200, (89600-11200)/128000*100 = 61.25
	if math.Abs(spec-61.25) > 1e-9 {
		t.Errorf("thresholdPercent=70 SpeculationPercent = %v, want 61.25", spec)
	}

	// 4. Custom thresholdTokens = 64000
	tokPol := defaultPol
	tokPol.ThresholdTokens = 64000
	has, thresh, spec = computeCompactionBoundaries(tokPol, 128000, false)
	if !has || math.Abs(thresh-50.0) > 1e-9 {
		t.Errorf("thresholdTokens=64000 => has=%v thresh=%v, want true, 50.0", has, thresh)
	}
	// lead = max(8192, floor(64000*0.125)=8000) = 8192 -> (64000-8192)/128000*100 = 43.6
	wantSpec := (float64(64000-8192) / 128000.0) * 100.0
	if math.Abs(spec-wantSpec) > 1e-9 {
		t.Errorf("thresholdTokens=64000 SpeculationPercent = %v, want %v", spec, wantSpec)
	}

	// 5. compaction.enabled = false -> no boundaries
	offPol := defaultPol
	offPol.Enabled = false
	has, thresh, spec = computeCompactionBoundaries(offPol, 128000, false)
	if has || thresh != 0 || spec != 0 {
		t.Errorf("compaction.enabled=false => (%v, %v, %v), want (false, 0, 0)", has, thresh, spec)
	}

	// 6. Verify Setting integration via publishComposerMeta
	surface.ResetForTests()
	origSetting := Setting
	origSub := IsSubagent
	defer func() {
		Setting = origSetting
		IsSubagent = origSub
		surface.ResetForTests()
	}()
	IsSubagent = nil
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "compaction.enabled":
			return true
		case "compaction.asyncEnabled":
			return true
		}
		return nil
	}
	st := newChromeState()
	st.modelWindow = 128000
	st.modelHasVision = false
	st.publishComposerMeta(sdk.Context{})
	cm := surface.GetComposerMeta()
	if !cm.HasBoundaries || !cm.AutoCompactEnabled || math.Abs(cm.ThresholdPercent-85.0) > 1e-9 || math.Abs(cm.SpeculationPercent-74.375) > 1e-9 {
		t.Fatalf("publishComposerMeta mismatch: %+v", cm)
	}
}

func TestWave7EvalTableAndJobTileChartNativeMeta(t *testing.T) {
	// 1. nvEvalTableNode encodes numeric column alignments on List.Items[0].Detail
	// while keeping plain Lines ("k · v") intact for fallback & native_views_test.
	res := sdk.ToolRenderResult{
		Details: map[string]any{
			"cells": []any{
				map[string]any{
					"index":    0,
					"code":     "print(tbl)",
					"language": "python",
					"output":   "k v\na 1",
					"status":   "complete",
					"views": []any{
						map[string]any{
							"text": "k v\na 1",
							"view": map[string]any{
								"kind": "table",
								"columns": []any{
									map[string]any{"head": "k", "numeric": false},
									map[string]any{"head": "v", "numeric": true},
								},
								"rows": []any{[]any{"a", "1"}},
							},
						},
					},
				},
			},
		},
	}
	_, ids := nvMustResult(t, "eval", res, sdk.ToolRenderResultOptions{Expanded: true}, sdk.ToolRenderContext{})
	tblNode := nvAssertNode(t, ids, "omp.tool.eval.table#0.0", "lines", "k · v", "a · 1")
	kl, ok := tblNode.node.(*kit.Lines)
	if !ok || kl.List == nil || len(kl.List.Items) != 2 {
		t.Fatalf("expected *kit.Lines with 2 List.Items, got %#v", tblNode.node)
	}
	if kl.List.Items[0].Detail != "align=start,end" {
		t.Errorf("header ListItem.Detail = %q, want %q", kl.List.Items[0].Detail, "align=start,end")
	}

	// 2. nvDescribeJobTileVisual emits vstack(omp.job.tile.chart#0) with summary + CSV series
	waitRes := sdk.ToolRenderResult{
		Details: map[string]any{
			"op": "wait",
			"jobs": []any{
				map[string]any{
					"id":     "bg_1",
					"type":   "bash",
					"status": "running",
					"label":  "bench",
					"widget": map[string]any{
						"state": "live",
						"title": "Bench",
						"blocks": []any{
							map[string]any{
								"kind":   "spark",
								"label":  "tok/s",
								"last":   "42",
								"values": []any{10.0, 25.5, 42.0},
							},
						},
					},
				},
			},
		},
	}
	_, wIDs := nvMustResult(t, "wait", waitRes, sdk.ToolRenderResultOptions{}, sdk.ToolRenderContext{})
	nvAssertNode(t, wIDs, "omp.job.tile.chart#0", "vstack")
	nvAssertNode(t, wIDs, "omp.job.tile.chart.summary#0", "text", "tok/s 42")
	nvAssertNode(t, wIDs, "omp.job.tile.chart.series#0", "text", "10,25.5,42")
}

func TestWave7JudgeBatchHudLifecycle(t *testing.T) {
	surface.ResetForTests()
	defer surface.ResetForTests()

	seenUpdates := make(chan surface.ProgressHudSnapshot, 16)
	unsub := surface.OnProgressHudChange(func(snap surface.ProgressHudSnapshot) {
		select {
		case seenUpdates <- snap:
		default:
		}
	})
	defer unsub()

	// Exercise surface judge_batch HUD upsert/update/remove lifecycle directly
	// and verify eval's CompleteFn-backed judgment batch publishes through surface.
	origComplete := eval.CompleteFn
	defer func() { eval.CompleteFn = origComplete }()
	eval.CompleteFn = func(_ context.Context, _ sdk.Context, _, _, _ map[string]any) (json.RawMessage, error) {
		return json.RawMessage(`{"content":[{"type":"text","text":"{\"ok\":{\"value\":true}}"}]}`), nil
	}

	surface.UpdateJudgeBatchHud(surface.JudgeBatchHudItem{
		ID:     "jdgb-test-1",
		Intent: "Scoring items",
		Done:   0,
		Total:  3,
	})
	snap := surface.GetProgressHudSnapshot()
	if len(snap.JudgeBatches) != 1 || snap.JudgeBatches[0].Total != 3 || snap.JudgeBatches[0].Done != 0 {
		t.Fatalf("unexpected initial judge batch HUD: %+v", snap.JudgeBatches)
	}
	surface.UpdateJudgeBatchHud(surface.JudgeBatchHudItem{
		ID:     "jdgb-test-1",
		Intent: "Scoring items",
		Done:   2,
		Total:  3,
		Failed: 1,
	})
	snap = surface.GetProgressHudSnapshot()
	if len(snap.JudgeBatches) != 1 || snap.JudgeBatches[0].Done != 2 || snap.JudgeBatches[0].Failed != 1 {
		t.Fatalf("unexpected updated judge batch HUD: %+v", snap.JudgeBatches)
	}
	surface.RemoveJudgeBatchHud("jdgb-test-1")
	snap = surface.GetProgressHudSnapshot()
	if len(snap.JudgeBatches) != 0 {
		t.Fatalf("expected judge batch HUD removed, got %+v", snap.JudgeBatches)
	}
}

type fakeWave7Job struct {
	id      string
	kind    jobs.Kind
	label   string
	started time.Time
	done    chan struct{}
	status  string
	widget  *jobs.WidgetView
}

func (f *fakeWave7Job) ID() string                            { return f.id }
func (f *fakeWave7Job) Kind() jobs.Kind                       { return f.kind }
func (f *fakeWave7Job) Label() string                         { return f.label }
func (f *fakeWave7Job) Started() time.Time                    { return f.started }
func (f *fakeWave7Job) Done() <-chan struct{}                 { return f.done }
func (f *fakeWave7Job) Status() string                        { return f.status }
func (f *fakeWave7Job) Output() string                        { return "" }
func (f *fakeWave7Job) Result() string                        { return "" }
func (f *fakeWave7Job) Cancel()                               {}
func (f *fakeWave7Job) WidgetView(time.Time) *jobs.WidgetView { return f.widget }

func TestWave7JobsHudPublishesNonAgentRunningJobs(t *testing.T) {
	surface.ResetForTests()
	jobs.ResetForTests()
	jobs.ResetDeliveryForTests()
	defer func() {
		jobs.ResetForTests()
		jobs.ResetDeliveryForTests()
		surface.ResetForTests()
	}()

	sessID := "sess-wave7-hud"
	now := time.Now()
	bashJob := &fakeWave7Job{
		id:      "bg_1",
		kind:    jobs.KindBash,
		label:   "go test ./...",
		started: now,
		done:    make(chan struct{}),
		status:  "running",
		widget: &jobs.WidgetView{
			Title: "Running tests",
			Stage: "ok pkg/foo",
			Blocks: []jobs.WidgetBlock{
				{Kind: "stat", Items: []jobs.WidgetStatItem{{Label: "lines", Text: "42"}}},
			},
		},
	}
	svcJob := &fakeWave7Job{
		id:      "dev-server",
		kind:    jobs.KindService,
		label:   "vite dev",
		started: now.Add(time.Second),
		done:    make(chan struct{}),
		status:  "running",
	}
	agentJob := &fakeWave7Job{
		id:      "Scout1",
		kind:    jobs.KindAgent,
		label:   "Explore repo",
		started: now.Add(2 * time.Second),
		done:    make(chan struct{}),
		status:  "running",
	}
	doneEvalJob := &fakeWave7Job{
		id:      "eval_1",
		kind:    jobs.KindEval,
		label:   "python cell",
		started: now.Add(3 * time.Second),
		done:    make(chan struct{}),
		status:  "completed",
	}
	close(doneEvalJob.done)

	jobs.Add(sessID, bashJob)
	jobs.Add(sessID, svcJob)
	jobs.Add(sessID, agentJob)
	jobs.Add(sessID, doneEvalJob)

	var snap surface.ProgressHudSnapshot
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap = surface.GetProgressHudSnapshot()
		if snap.RunningJobs == 2 && len(snap.Jobs) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if snap.RunningJobs != 2 || len(snap.Jobs) != 2 {
		t.Fatalf("expected 2 running non-agent jobs, got RunningJobs=%d Jobs=%+v", snap.RunningJobs, snap.Jobs)
	}
	if snap.Jobs[0].ID != "bg_1" || snap.Jobs[0].Kind != "bash" || snap.Jobs[0].Label != "Running tests" || snap.Jobs[0].Summary != "ok pkg/foo" || snap.Jobs[0].GlanceText != "lines 42" {
		t.Errorf("unexpected bash job HUD item: %+v", snap.Jobs[0])
	}
	if snap.Jobs[1].ID != "dev-server" || snap.Jobs[1].Kind != "service" {
		t.Errorf("unexpected service job HUD item: %+v", snap.Jobs[1])
	}

	// Finishing bashJob updates the HUD to only svcJob.
	bashJob.status = "completed"
	close(bashJob.done)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap = surface.GetProgressHudSnapshot()
		if snap.RunningJobs == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if snap.RunningJobs != 1 || len(snap.Jobs) != 1 || snap.Jobs[0].ID != "dev-server" {
		t.Fatalf("expected only dev-server after bg_1 settled, got %+v", snap)
	}

	// Removing svcJob clears the HUD.
	jobs.Remove(sessID, "dev-server")
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap = surface.GetProgressHudSnapshot()
		if snap.RunningJobs == 0 && len(snap.Jobs) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if snap.RunningJobs != 0 || len(snap.Jobs) != 0 {
		t.Fatalf("expected 0 running jobs after Remove, got %+v", snap)
	}
}
