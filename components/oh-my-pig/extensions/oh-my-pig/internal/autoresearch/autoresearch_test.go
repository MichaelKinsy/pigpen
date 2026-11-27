package autoresearch

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
)

func initTestGitRepo(t *testing.T, dir string) string {
	t.Helper()
	ctx := context.Background()
	mustGit := func(args ...string) {
		t.Helper()
		_, stderr, code, err := runGit(ctx, dir, args...)
		if err != nil || code != 0 {
			t.Fatalf("git %v failed (code=%d err=%v): %s", args, code, err, stderr)
		}
	}
	mustGit("init", "-q", "-b", "main")
	mustGit("config", "core.autocrlf", "false")
	mustGit("config", "user.email", "test@example.com")
	mustGit("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit("add", "-A")
	mustGit("commit", "-q", "-m", "baseline")
	return GitHeadSHA(ctx, dir)
}

func writeHarness(t *testing.T, dir, body string) {
	t.Helper()
	content := "#!/usr/bin/env bash\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, HarnessFilename), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func firstText(t *testing.T, res map[string]any) string {
	t.Helper()
	if s, ok := res["content"].(string); ok {
		return s
	}
	content, _ := res["content"].([]any)
	for _, block := range content {
		m, _ := block.(map[string]any)
		if m["type"] == "text" {
			s, _ := m["text"].(string)
			return s
		}
	}
	t.Fatalf("no text content in %+v", res)
	return ""
}

func newTestExtension() *sdk.Extension {
	e := sdk.New("autoresearch-test")
	registerWithManager(e, &manager{store: NewRuntimeStore()})
	return e
}

func isolateStorage(t *testing.T) string {
	t.Helper()
	dbDir := t.TempDir()
	t.Setenv("OMP_AUTORESEARCH_DB_DIR", dbDir)
	t.Cleanup(CloseAllAutoresearchStorages)
	return dbDir
}

// ── 1. State & MAD confidence math (upstream: autoresearch-state.test.ts) ────

func TestStateMathAndConfidence(t *testing.T) {
	makeRes := func(runNum, seg int, metric float64, status ExperimentStatus, flagged bool) ExperimentResult {
		return ExperimentResult{
			RunNumber: new(runNum),
			Segment:   seg,
			Metric:    metric,
			Status:    status,
			Flagged:   flagged,
		}
	}

	t.Run("FindBaselineMetric returns first kept run in segment", func(t *testing.T) {
		results := []ExperimentResult{
			makeRes(1, 0, 100, StatusKeep, false),
			makeRes(2, 0, 80, StatusKeep, false),
			makeRes(3, 1, 50, StatusKeep, false),
		}
		if got := FindBaselineMetric(results, 0); got == nil || *got != 100 {
			t.Fatalf("segment 0 baseline = %v, want 100", got)
		}
		if got := FindBaselineMetric(results, 1); got == nil || *got != 50 {
			t.Fatalf("segment 1 baseline = %v, want 50", got)
		}
		if got := FindBaselineRunNumber(results, 0); got == nil || *got != 1 {
			t.Fatalf("segment 0 baseline runNumber = %v, want 1", got)
		}
	})

	t.Run("FindBaselineMetric ignores flagged results", func(t *testing.T) {
		results := []ExperimentResult{
			makeRes(1, 0, 100, StatusKeep, true),
			makeRes(2, 0, 90, StatusKeep, false),
		}
		if got := FindBaselineMetric(results, 0); got == nil || *got != 90 {
			t.Fatalf("baseline = %v, want 90", got)
		}
	})

	t.Run("FindBestKeptMetric respects direction and excludes flagged", func(t *testing.T) {
		results := []ExperimentResult{
			makeRes(1, 0, 100, StatusKeep, false),
			makeRes(2, 0, 60, StatusKeep, true),
			makeRes(3, 0, 80, StatusKeep, false),
		}
		if got := FindBestKeptMetric(results, 0, DirectionLower); got == nil || *got != 80 {
			t.Fatalf("lower best = %v, want 80", got)
		}
		if got := FindBestKeptMetric(results, 0, DirectionHigher); got == nil || *got != 100 {
			t.Fatalf("higher best = %v, want 100", got)
		}
	})

	t.Run("ComputeConfidence returns nil with fewer than 3 valid runs", func(t *testing.T) {
		results := []ExperimentResult{
			makeRes(1, 0, 10, StatusKeep, false),
			makeRes(2, 0, 12, StatusKeep, false),
		}
		if got := ComputeConfidence(results, 0, DirectionLower); got != nil {
			t.Fatalf("confidence = %v, want nil", *got)
		}
	})

	t.Run("ComputeConfidence returns nil when MAD is zero", func(t *testing.T) {
		results := []ExperimentResult{
			makeRes(1, 0, 10, StatusKeep, false),
			makeRes(2, 0, 10, StatusKeep, false),
			makeRes(3, 0, 10, StatusKeep, false),
		}
		if got := ComputeConfidence(results, 0, DirectionLower); got != nil {
			t.Fatalf("confidence = %v, want nil", *got)
		}
	})

	t.Run("ComputeConfidence computes MAD score and excludes flagged runs", func(t *testing.T) {
		results := []ExperimentResult{
			makeRes(1, 0, 100, StatusKeep, false),
			makeRes(2, 0, 99, StatusKeep, false),
			makeRes(3, 0, 98, StatusKeep, false),
			makeRes(4, 0, 90, StatusKeep, false),
		}
		base := ComputeConfidence(results, 0, DirectionLower)
		if base == nil || math.Abs(*base-10.0) > 1e-9 {
			t.Fatalf("base confidence = %v, want 10.0", base)
		}
		flagged := slices.Clone(results)
		flagged[1].Flagged = true
		afterFlag := ComputeConfidence(flagged, 0, DirectionLower)
		if afterFlag == nil || *afterFlag == *base {
			t.Fatalf("flagged confidence = %v, expected different from %v", afterFlag, *base)
		}
	})
}
func TestReconstructControlState(t *testing.T) {
	mkEntry := func(mode, goal string) json.RawMessage {
		d := map[string]any{"mode": mode}
		if goal != "" {
			d["goal"] = goal
		}
		raw, _ := json.Marshal(map[string]any{
			"type":       "custom",
			"customType": "autoresearch-control",
			"data":       d,
		})
		return raw
	}

	st1 := ReconstructControlState([]json.RawMessage{mkEntry("on", "x"), mkEntry("off", "")}, nil)
	if st1.AutoresearchMode || st1.LastMode != "off" || st1.Goal == nil || *st1.Goal != "x" {
		t.Fatalf("unexpected st1: %+v", st1)
	}

	st2 := ReconstructControlState([]json.RawMessage{mkEntry("on", "x"), mkEntry("clear", "")}, nil)
	if st2.AutoresearchMode || st2.LastMode != "clear" || st2.Goal != nil {
		t.Fatalf("unexpected st2: %+v", st2)
	}
}

// ── 2. Helpers & Streaming Output Scanner ────────────────────────────────────

func TestOutputScannerAndHelpers(t *testing.T) {
	scanner := NewExperimentOutputScanner()
	scanner.Append("METRIC runtime_ms=42.5\nMETRIC __proto__=1\nASI hypo")
	scanner.Append("thesis=\"fast path\"\nASI ok=true\nASI count=3\n")
	metrics, asi := scanner.Finish()

	if metrics["runtime_ms"] != 42.5 {
		t.Fatalf("runtime_ms = %v, want 42.5", metrics["runtime_ms"])
	}
	if _, bad := metrics["__proto__"]; bad {
		t.Fatalf("denied key __proto__ should be ignored")
	}
	if asi["hypothesis"] != "fast path" || asi["ok"] != true || asi["count"] != float64(3) {
		t.Fatalf("unexpected asi: %+v", asi)
	}

	if got := InferMetricUnitFromName("latency_µs"); got != "µs" {
		t.Fatalf("unit = %q, want µs", got)
	}
	if got := FormatNum(new(1234.5), "ms"); got != "1,234.50ms" {
		t.Fatalf("FormatNum = %q", got)
	}
	if got := FormatNum(new(1000.0), "ms"); got != "1,000ms" {
		t.Fatalf("FormatNum int = %q", got)
	}
}

// ── 3. Storage round-trip & crash recovery (upstream: autoresearch-state.test.ts) ──

func TestStorageRoundTripAndCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.GetActiveSession() != nil {
		t.Fatalf("expected nil active session")
	}
	sess, err := s.OpenSession(OpenSessionParams{
		Name:             "speed",
		Goal:             new("make it fast"),
		PrimaryMetric:    "runtime_ms",
		MetricUnit:       "ms",
		Direction:        DirectionLower,
		PreferredCommand: new("bash autoresearch.sh"),
		Branch:           new("autoresearch/speed-20260501"),
		BaselineCommit:   new("abc1234"),
		MaxIterations:    new(25),
		ScopePaths:       []string{"src"},
		OffLimits:        []string{"test"},
		Constraints:      []string{"no api break"},
		SecondaryMetrics: []string{"memory_mb"},
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := s.InsertRun(InsertRunParams{
		SessionID:        sess.ID,
		Segment:          0,
		Command:          "bash autoresearch.sh",
		LogPath:          "/tmp/run.log",
		PreRunDirtyPaths: []string{},
		StartedAt:        1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.GetPendingRun(sess.ID) == nil || s.GetPendingRun(sess.ID).ID != run.ID {
		t.Fatalf("expected pending run %d", run.ID)
	}
	_, err = s.MarkRunCompleted(MarkRunCompletedParams{
		RunID:         run.ID,
		CompletedAt:   5000,
		DurationMs:    4000,
		ExitCode:      new(0),
		TimedOut:      false,
		ParsedPrimary: new(42.0),
		ParsedMetrics: NumericMetricMap{"runtime_ms": 42, "memory_mb": 12},
		ParsedASI:     ASIData{"hypothesis": "first try"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.MarkRunLogged(MarkRunLoggedParams{
		RunID:           run.ID,
		Status:          StatusKeep,
		Description:     "baseline",
		Metric:          42,
		Metrics:         NumericMetricMap{"memory_mb": 12},
		ASI:             ASIData{"hypothesis": "first try", "learning": "ok"},
		CommitHash:      new("cafef00d"),
		ModifiedPaths:   []string{"src/foo.go"},
		ScopeDeviations: []string{},
		LoggedAt:        6000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FlagRun(run.ID, "reward-hacked"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSession(sess.ID, UpdateSessionParams{HasNotes: true, Notes: "## Plan\n- step 1\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BumpSegment(sess.ID); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Append a torn/truncated JSONL line to verify crash recovery on reload.
	f, err := os.OpenFile(filepath.Join(dir, "runs.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"id":99,"sessionId":1,"command":"truncated`)
	_ = f.Close()

	// Reload from disk and verify full persistence.
	s2, err := NewStorage(dir)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	defer s2.Close()
	active := s2.GetActiveSession()
	if active == nil || active.ID != sess.ID || active.CurrentSegment != 1 || active.Notes != "## Plan\n- step 1\n" {
		t.Fatalf("reloaded session mismatch: %+v", active)
	}
	reloadedRun := s2.GetRunByID(run.ID)
	if reloadedRun == nil || !reloadedRun.Flagged || reloadedRun.FlaggedReason == nil || *reloadedRun.FlaggedReason != "reward-hacked" {
		t.Fatalf("reloaded run mismatch: %+v", reloadedRun)
	}
	if s2.GetPendingRun(sess.ID) != nil {
		t.Fatalf("expected no pending run after logging")
	}
}

// ── 4. Git & Jujutsu guardrails (upstream: autoresearch-git.test.ts) ─────────

func TestEnsureAutoresearchBranchGuardrails(t *testing.T) {
	ctx := context.Background()

	t.Run("rejects pure jj workspace", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".jj", "repo", "store"), 0o755); err != nil {
			t.Fatal(err)
		}
		res := EnsureAutoresearchBranch(ctx, dir, new("demo"))
		if res.OK || !strings.Contains(res.Error, "pure Jujutsu") || !strings.Contains(res.Error, "jj git init --colocate") {
			t.Fatalf("unexpected result: %+v", res)
		}
	})

	t.Run("creates branch in colocated jj-git workspace", func(t *testing.T) {
		dir := t.TempDir()
		initTestGitRepo(t, dir)
		if err := os.MkdirAll(filepath.Join(dir, ".jj", "repo", "store"), 0o755); err != nil {
			t.Fatal(err)
		}
		res := EnsureAutoresearchBranch(ctx, dir, new("demo goal"))
		if !res.OK || !res.Created || res.BranchName == nil || !regexp.MustCompile(`^autoresearch/demo-goal-\d{8}$`).MatchString(*res.BranchName) {
			t.Fatalf("unexpected result: %+v", res)
		}
	})

	t.Run("trims trailing dash when slug hits 48-char cap", func(t *testing.T) {
		dir := t.TempDir()
		initTestGitRepo(t, dir)
		res := EnsureAutoresearchBranch(ctx, dir, new(strings.Repeat("a", 47)+" /tail"))
		if !res.OK || res.BranchName == nil || !regexp.MustCompile(`^autoresearch/a{47}-\d{8}$`).MatchString(*res.BranchName) {
			t.Fatalf("unexpected result: %+v", res)
		}
	})

	t.Run("uses session when no characters survive sanitization", func(t *testing.T) {
		dir := t.TempDir()
		initTestGitRepo(t, dir)
		res := EnsureAutoresearchBranch(ctx, dir, new("!!!"))
		if !res.OK || res.BranchName == nil || !regexp.MustCompile(`^autoresearch/session-\d{8}$`).MatchString(*res.BranchName) {
			t.Fatalf("unexpected result: %+v", res)
		}
	})

	t.Run("soft warning outside git repo", func(t *testing.T) {
		dir := t.TempDir()
		res := EnsureAutoresearchBranch(ctx, dir, new("demo"))
		if !res.OK || res.BranchName != nil || !strings.Contains(res.Warning, "Not in a git repository") {
			t.Fatalf("unexpected result: %+v", res)
		}
	})

	t.Run("rejects pure jj nested inside outer git checkout", func(t *testing.T) {
		outer := t.TempDir()
		initTestGitRepo(t, outer)
		inner := filepath.Join(outer, "nested-jj")
		if err := os.MkdirAll(filepath.Join(inner, ".jj", "repo", "store"), 0o755); err != nil {
			t.Fatal(err)
		}
		res := EnsureAutoresearchBranch(ctx, inner, new("demo"))
		if res.OK || !strings.Contains(res.Error, "pure Jujutsu") {
			t.Fatalf("unexpected result: %+v", res)
		}
	})

	t.Run("creates branch in nested git checkout under outer jj workspace", func(t *testing.T) {
		outer := t.TempDir()
		if err := os.MkdirAll(filepath.Join(outer, ".jj", "repo", "store"), 0o755); err != nil {
			t.Fatal(err)
		}
		inner := filepath.Join(outer, "vendor")
		if err := os.MkdirAll(inner, 0o755); err != nil {
			t.Fatal(err)
		}
		initTestGitRepo(t, inner)
		res := EnsureAutoresearchBranch(ctx, inner, new("demo"))
		if !res.OK || !res.Created || res.BranchName == nil || !regexp.MustCompile(`^autoresearch/demo-\d{8}$`).MatchString(*res.BranchName) {
			t.Fatalf("unexpected result: %+v", res)
		}
	})

	t.Run("aborts when worktree is dirty", func(t *testing.T) {
		dir := t.TempDir()
		initTestGitRepo(t, dir)
		if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		res := EnsureAutoresearchBranch(ctx, dir, new("demo"))
		if res.OK || !strings.Contains(res.Error, "Worktree is dirty") {
			t.Fatalf("unexpected result: %+v", res)
		}
	})
}

// ── 5. Default session isolation & before_agent_start ────────────────────────

func TestDefaultSessionUntouched(t *testing.T) {
	isolateStorage(t)
	dir := t.TempDir()
	h := startHost(t, newTestExtension(), hostOptions{
		Cwd:         dir,
		ActiveTools: []string{"read", "bash"},
	})

	// All 4 experiment tools must register with DefaultActive == false.
	for _, name := range ExperimentToolNames {
		tl, ok := h.tools[name]
		if !ok {
			t.Fatalf("expected tool %q to be registered", name)
		}
		if tl.DefaultActive == nil || *tl.DefaultActive {
			t.Fatalf("tool %q DefaultActive = %v, want false", name, tl.DefaultActive)
		}
	}
	// No tool_call hook (upstream: autoresearch-state.test.ts:604).
	if len(h.handlers["tool_call"]) != 0 {
		t.Fatalf("expected no tool_call handler")
	}

	// session_start on a fresh session must not activate tools or touch storage.
	_, errMsg := h.emitEvent(sdk.EventSessionStart, nil)
	if errMsg != "" {
		t.Fatalf("session_start error: %s", errMsg)
	}
	if got := h.activeTools(); !slices.Equal(got, []string{"read", "bash"}) {
		t.Fatalf("active tools mutated on default session: %v", got)
	}

	// before_agent_start when OFF must not set systemPrompt (zero prompt lines added).
	if sp, ok := h.fireBeforeAgentStart(map[string]any{
		"systemPrompt":        "base prompt",
		"systemPromptOptions": map[string]any{},
	}); ok {
		t.Fatalf("expected no systemPrompt override when inactive, got %q", sp)
	}
}

// ── 6. Full end-to-end workflow in a real temporary git repo ─────────────────

func TestEndToEndAutoresearchWorkflow(t *testing.T) {
	isolateStorage(t)
	ctx := context.Background()
	repo := t.TempDir()
	initialSHA := initTestGitRepo(t, repo)

	// Seed a tracked target file to optimize.
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "algo.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, _, _ = runGit(ctx, repo, "add", "-A")
	_, _, _, _ = runGit(ctx, repo, "commit", "-q", "-m", "add algo")
	initialSHA = GitHeadSHA(ctx, repo)

	h := startHost(t, newTestExtension(), hostOptions{
		Cwd:         repo,
		ActiveTools: []string{"read", "bash"},
	})
	_, _ = h.emitEvent(sdk.EventSessionStart, nil)

	// 1. Activate via `/autoresearch speed up algo` -> creates branch, enables 4 tools, sends user message.
	if errMsg := h.runCommand("autoresearch", "speed up algo"); errMsg != "" {
		t.Fatalf("/autoresearch error: %s", errMsg)
	}
	branch := GitCurrentBranch(ctx, repo)
	if !strings.HasPrefix(branch, "autoresearch/speed-up-algo-") {
		t.Fatalf("expected autoresearch branch, got %q", branch)
	}
	active := h.activeTools()
	for _, toolName := range ExperimentToolNames {
		if !slices.Contains(active, toolName) {
			t.Fatalf("expected tool %q to be active after /autoresearch, got %v", toolName, active)
		}
	}

	// Phase 1 before_agent_start injects setup prompt (issue #3665: even when systemPrompt is unset).
	setupPrompt, ok := h.fireBeforeAgentStart(map[string]any{
		"systemPromptOptions": map[string]any{},
	})
	if !ok || !strings.Contains(setupPrompt, "Phase 1: Harness Setup") {
		t.Fatalf("expected Phase 1 setup prompt, got (ok=%v): %s", ok, setupPrompt)
	}

	// 2. init_experiment rejects when autoresearch.sh is missing.
	res, _ := h.callTool("init_experiment", map[string]any{
		"name":           "algo-speed",
		"primary_metric": "runtime_ms",
	})
	if !strings.Contains(firstText(t, res), "autoresearch.sh") {
		t.Fatalf("expected missing harness error, got: %s", firstText(t, res))
	}

	// 3. Write autoresearch.sh and call init_experiment -> auto-commits harness setup.
	writeHarness(t, repo, "echo METRIC runtime_ms=100; echo METRIC memory_mb=16; echo ASI hypothesis=baseline")
	res, errMsg := h.callTool("init_experiment", map[string]any{
		"name":              "algo-speed",
		"goal":              "speed up algo",
		"primary_metric":    "runtime_ms",
		"metric_unit":       "ms",
		"direction":         "lower",
		"scope_paths":       []any{"src"},
		"off_limits":        []any{"forbidden"},
		"secondary_metrics": []any{"memory_mb"},
	})
	if errMsg != "" {
		t.Fatalf("init_experiment error: %s", errMsg)
	}
	if !strings.Contains(firstText(t, res), "Started session #1") {
		t.Fatalf("unexpected init output: %s", firstText(t, res))
	}
	harnessSHA := GitHeadSHA(ctx, repo)
	if harnessSHA == initialSHA {
		t.Fatalf("expected harness auto-commit to advance HEAD")
	}

	// 4. Run baseline & log keep.
	run1, errMsg := h.callTool("run_experiment", map[string]any{"timeout_seconds": 10})
	if errMsg != "" {
		t.Fatalf("run_experiment #1 error: %s", errMsg)
	}
	if !strings.Contains(firstText(t, run1), "Parsed runtime_ms: 100") {
		t.Fatalf("expected parsed metric 100, got: %s", firstText(t, run1))
	}
	log1, errMsg := h.callTool("log_experiment", map[string]any{
		"metric":      100,
		"status":      "keep",
		"description": "baseline",
	})
	if errMsg != "" {
		t.Fatalf("log_experiment #1 error: %s", errMsg)
	}
	if !strings.Contains(firstText(t, log1), "Logged run #1: keep - baseline") {
		t.Fatalf("unexpected log #1 output: %s", firstText(t, log1))
	}

	// 5. Modify tracked file, improve metric to 95, run & log keep -> creates git commit.
	if err := os.WriteFile(filepath.Join(repo, "src", "algo.txt"), []byte("v2-faster\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeHarness(t, repo, "echo METRIC runtime_ms=95; echo METRIC memory_mb=15")
	// Note: keep harness clean by only editing src/algo.txt? For this test we also check commit creation.
	_, _ = h.callTool("run_experiment", map[string]any{"timeout_seconds": 10})
	log2, errMsg := h.callTool("log_experiment", map[string]any{
		"metric":      95,
		"status":      "keep",
		"description": "optimize inner loop",
	})
	if errMsg != "" {
		t.Fatalf("log_experiment #2 error: %s", errMsg)
	}
	keptSHA := GitHeadSHA(ctx, repo)
	if keptSHA == harnessSHA {
		t.Fatalf("expected keep to create a git commit")
	}
	if !strings.Contains(firstText(t, log2), "Git: committed ") {
		t.Fatalf("expected commit note in log #2, got: %s", firstText(t, log2))
	}
	if status := strings.TrimSpace(TryGitStatus(ctx, repo)); status != "" {
		t.Fatalf("expected clean worktree after keep, got %q", status)
	}

	// 6. Modify tracked file + create untracked file, run & log discard -> reverts to keptSHA.
	if err := os.WriteFile(filepath.Join(repo, "src", "algo.txt"), []byte("v3-regression\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scratch.tmp"), []byte("junk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = h.callTool("run_experiment", map[string]any{"timeout_seconds": 10})
	log3, errMsg := h.callTool("log_experiment", map[string]any{
		"metric":      102,
		"status":      "discard",
		"description": "regression",
	})
	if errMsg != "" {
		t.Fatalf("log_experiment #3 error: %s", errMsg)
	}
	if !strings.Contains(firstText(t, log3), "Confidence:") {
		t.Fatalf("expected MAD confidence score after 3 runs, got: %s", firstText(t, log3))
	}
	if GitHeadSHA(ctx, repo) != keptSHA {
		t.Fatalf("discard must not rewind prior keep commit")
	}
	algoBytes, _ := os.ReadFile(filepath.Join(repo, "src", "algo.txt"))
	if string(algoBytes) != "v2-faster\n" {
		t.Fatalf("expected algo.txt reverted to v2-faster, got %q", string(algoBytes))
	}
	if _, err := os.Stat(filepath.Join(repo, "scratch.tmp")); !os.IsNotExist(err) {
		t.Fatalf("expected untracked scratch.tmp removed on discard")
	}

	// 7. Crash & checks_failed statuses also revert worktree cleanly.
	if err := os.WriteFile(filepath.Join(repo, "src", "algo.txt"), []byte("broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = h.callTool("run_experiment", map[string]any{"timeout_seconds": 10})
	log4, _ := h.callTool("log_experiment", map[string]any{
		"metric":      0,
		"status":      "crash",
		"description": "segfault",
	})
	if !strings.Contains(firstText(t, log4), "Logged run #4: crash - segfault") {
		t.Fatalf("unexpected crash log: %s", firstText(t, log4))
	}
	algoBytes, _ = os.ReadFile(filepath.Join(repo, "src", "algo.txt"))
	if string(algoBytes) != "v2-faster\n" {
		t.Fatalf("expected algo.txt reverted on crash, got %q", string(algoBytes))
	}

	// 8. Notes persistence (`update_notes` body + append_idea) & `/autoresearch status`.
	_, _ = h.callTool("update_notes", map[string]any{"body": "## Plan\n- step one\n"})
	noteRes, _ := h.callTool("update_notes", map[string]any{"append_idea": "try SIMD"})
	if !strings.Contains(firstText(t, noteRes), "Appended idea") {
		t.Fatalf("unexpected update_notes output: %s", firstText(t, noteRes))
	}
	if errMsg := h.runCommand("autoresearch", "status"); errMsg != "" {
		t.Fatalf("/autoresearch status error: %s", errMsg)
	}

	// Phase 2 before_agent_start injects notes and recent runs.
	activePrompt, ok := h.fireBeforeAgentStart(map[string]any{
		"systemPrompt":        "BASE",
		"systemPromptOptions": map[string]any{},
	})
	if !ok || !strings.Contains(activePrompt, "BASE") || !strings.Contains(activePrompt, "try SIMD") || !strings.Contains(activePrompt, "optimize inner loop") {
		t.Fatalf("expected active prompt with base, notes, and recent runs, got (ok=%v):\n%s", ok, activePrompt)
	}

	// 9. Segment reset via init_experiment new_segment: true.
	segRes, _ := h.callTool("init_experiment", map[string]any{
		"name":           "algo-speed",
		"primary_metric": "runtime_ms",
		"metric_unit":    "ms",
		"new_segment":    true,
	})
	if !strings.Contains(firstText(t, segRes), "Bumped segment to 1") {
		t.Fatalf("expected segment bump, got: %s", firstText(t, segRes))
	}

	// 10. agent_end triggers auto-resume when armed.
	beforeMsgs := len(h.callsTo("sendMessage"))
	_, _ = h.emitEvent(sdk.EventAgentEnd, nil)
	afterMsgs := len(h.callsTo("sendMessage"))
	if afterMsgs != beforeMsgs+1 {
		t.Fatalf("expected agent_end to send auto-resume message, calls went %d -> %d", beforeMsgs, afterMsgs)
	}

	// 11. `/autoresearch off` deactivates tools and stops auto-resume.
	if errMsg := h.runCommand("autoresearch", "off"); errMsg != "" {
		t.Fatalf("/autoresearch off error: %s", errMsg)
	}
	for _, toolName := range ExperimentToolNames {
		if slices.Contains(h.activeTools(), toolName) {
			t.Fatalf("tool %q should be inactive after /autoresearch off", toolName)
		}
	}
	beforeMsgs = len(h.callsTo("sendMessage"))
	_, _ = h.emitEvent(sdk.EventAgentEnd, nil)
	if len(h.callsTo("sendMessage")) != beforeMsgs {
		t.Fatalf("expected no auto-resume after /autoresearch off")
	}

	// 12. Reloading storage from disk preserves notes and segment.
	CloseAllAutoresearchStorages()
	reopened, err := OpenAutoresearchStorageIfExists(ctx, repo)
	if err != nil || reopened == nil {
		t.Fatalf("reopen storage failed: %v", err)
	}
	reloadedSess := reopened.GetActiveSessionForBranch(new(branch))
	if reloadedSess == nil || reloadedSess.CurrentSegment != 1 || !strings.Contains(reloadedSess.Notes, "try SIMD") {
		t.Fatalf("reloaded session mismatch: %+v", reloadedSess)
	}
}

func TestUpdateNotesAppendIdeaSections(t *testing.T) {
	got := AppendIdea("## Plan\n- step one\n\n## Ideas\n- try pooling\n## Results\n- baseline\n", "try caching")
	want := "## Plan\n- step one\n\n## Ideas\n- try pooling\n- try caching\n## Results\n- baseline\n"
	if got != want {
		t.Fatalf("AppendIdea mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}

	got2 := AppendIdea("## Plan\n- step one\n\n## Ideas backlog\n- old\n\n### Ideas\n- nested\n", "try caching")
	want2 := "## Plan\n- step one\n\n## Ideas backlog\n- old\n\n### Ideas\n- nested\n\n## Ideas\n- try caching\n"
	if got2 != want2 {
		t.Fatalf("AppendIdea heading exact match failed:\ngot:\n%s\nwant:\n%s", got2, want2)
	}
}

func TestScopeDeviationsFlagRunsAndMaxIterations(t *testing.T) {
	isolateStorage(t)
	ctx := context.Background()
	repo := t.TempDir()
	initTestGitRepo(t, repo)

	h := startHost(t, newTestExtension(), hostOptions{
		Cwd:         repo,
		ActiveTools: []string{"read", "bash"},
	})
	_, _ = h.emitEvent(sdk.EventSessionStart, nil)
	_ = h.runCommand("autoresearch", "scope test")

	writeHarness(t, repo, "echo METRIC runtime_ms=50")
	_, _ = h.callTool("init_experiment", map[string]any{
		"name":           "scope-exp",
		"primary_metric": "runtime_ms",
		"scope_paths":    []any{"src"},
		"off_limits":     []any{"forbidden"},
		"max_iterations": 2,
	})

	// Run 1: modify forbidden file without justification -> warns unjustified.
	if err := os.MkdirAll(filepath.Join(repo, "forbidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "forbidden", "bad.go"), []byte("package bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = h.callTool("run_experiment", map[string]any{"timeout_seconds": 10})
	log1, _ := h.callTool("log_experiment", map[string]any{
		"metric":      50,
		"status":      "keep",
		"description": "touched forbidden",
	})
	if !strings.Contains(firstText(t, log1), "unjustified") {
		t.Fatalf("expected unjustified warning, got: %s", firstText(t, log1))
	}

	// Run 2: checks_failed + flag_runs #1 + hits max_iterations=2 -> disables mode & tools.
	_, _ = h.callTool("run_experiment", map[string]any{"timeout_seconds": 10})
	log2, _ := h.callTool("log_experiment", map[string]any{
		"metric":      45,
		"status":      "checks_failed",
		"description": "lint failed",
		"flag_runs":   []any{map[string]any{"run_id": 1, "reason": "reward-hacked"}},
	})
	txt2 := firstText(t, log2)
	if !strings.Contains(txt2, "Flagged: #1 (reward-hacked)") || !strings.Contains(txt2, "Maximum experiments reached (2)") {
		t.Fatalf("unexpected log2 output: %s", txt2)
	}
	for _, toolName := range ExperimentToolNames {
		if slices.Contains(h.activeTools(), toolName) {
			t.Fatalf("tool %q should be deactivated after max_iterations", toolName)
		}
	}

	// Verify /autoresearch clear resets worktree to baseline and closes session.
	_ = h.runCommand("autoresearch", "clear")
	if _, err := os.Stat(filepath.Join(repo, "forbidden", "bad.go")); !os.IsNotExist(err) {
		t.Fatalf("expected clear to reset worktree to baseline and remove forbidden/bad.go")
	}
	s, _ := OpenAutoresearchStorageIfExists(ctx, repo)
	if s != nil && s.GetActiveSession() != nil {
		t.Fatalf("expected active session to be closed after clear")
	}
}

func TestOffBranchDiscardRevertsOnlyRunModifiedFiles(t *testing.T) {
	isolateStorage(t)
	ctx := context.Background()
	repo := t.TempDir()
	initTestGitRepo(t, repo)

	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "edit.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, _, _ = runGit(ctx, repo, "add", "-A")
	_, _, _, _ = runGit(ctx, repo, "commit", "-q", "-m", "seed")
	writeHarness(t, repo, "echo METRIC m=10")

	h := startHost(t, newTestExtension(), hostOptions{
		Cwd:         repo,
		ActiveTools: append([]string{"read"}, ExperimentToolNames...),
	})
	// Staying on `main` (off-branch).
	_, _ = h.callTool("init_experiment", map[string]any{
		"name":           "off-branch",
		"primary_metric": "m",
		"scope_paths":    []any{"src"},
	})
	// Pre-existing untracked file must survive discard.
	if err := os.WriteFile(filepath.Join(repo, "preexisting.txt"), []byte("leave me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = h.callTool("run_experiment", map[string]any{"timeout_seconds": 10})
	// Simulate run-introduced changes.
	if err := os.WriteFile(filepath.Join(repo, "src", "edit.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "new.txt"), []byte("temp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = h.callTool("log_experiment", map[string]any{
		"metric":      12,
		"status":      "discard",
		"description": "regress",
	})
	pre, _ := os.ReadFile(filepath.Join(repo, "preexisting.txt"))
	if string(pre) != "leave me\n" {
		t.Fatalf("preexisting.txt was modified: %q", string(pre))
	}
	if _, err := os.Stat(filepath.Join(repo, "src", "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected src/new.txt removed")
	}
	ed, _ := os.ReadFile(filepath.Join(repo, "src", "edit.txt"))
	if string(ed) != "v1\n" {
		t.Fatalf("expected src/edit.txt reverted to v1, got %q", string(ed))
	}
}

// ── 7. Regression tests for peer-review findings ─────────────────────────────

func TestPrintModeStripsIntentFieldViaRegisterSync(t *testing.T) {
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")
	isolateStorage(t)

	dir := t.TempDir()
	extPrint := newTestExtension()
	hPrint := startHost(t, extPrint, hostOptions{
		Mode:        "print",
		HasUI:       new(false),
		Cwd:         dir,
		ActiveTools: []string{"read", "bash"},
	})
	_, errMsg := hPrint.emitEvent(sdk.EventSessionStart, nil)
	if errMsg != "" {
		t.Fatalf("session_start error: %s", errMsg)
	}

	for _, name := range ExperimentToolNames {
		props := hPrint.toolSchemaProps(name)
		if _, hasI := props[intent.Field]; hasI {
			t.Errorf("print-mode %q schema must omit %q, got props %v", name, intent.Field, props)
		}
	}

	// Verify TUI mode keeps `i`.
	extTUI := newTestExtension()
	hTUI := startHost(t, extTUI, hostOptions{
		Mode:        "tui",
		HasUI:       new(true),
		Cwd:         dir,
		ActiveTools: []string{"read", "bash"},
	})
	_, _ = hTUI.emitEvent(sdk.EventSessionStart, nil)
	for _, name := range ExperimentToolNames {
		props := hTUI.toolSchemaProps(name)
		if _, hasI := props[intent.Field]; !hasI {
			t.Errorf("tui-mode %q schema must include %q", name, intent.Field)
		}
	}
}

func TestRunExperimentTimeoutKillsProcessGroupAndStreamsProgress(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sleep/process-group test")
	}
	isolateStorage(t)
	repo := t.TempDir()
	initTestGitRepo(t, repo)

	origInterval := progressInterval
	progressInterval = 150 * time.Millisecond
	t.Cleanup(func() { progressInterval = origInterval })

	// Spawn a background child process (`sleep 12 & wait`) so killing only bash
	// would otherwise hang waiting for stdout/stderr pipes for 12 seconds.
	writeHarness(t, repo, "echo started\nsleep 12 &\nwait")

	h := startHost(t, newTestExtension(), hostOptions{
		Cwd:         repo,
		ActiveTools: append([]string{"read"}, ExperimentToolNames...),
	})
	_, _ = h.callTool("init_experiment", map[string]any{
		"name":           "timeout-test",
		"primary_metric": "runtime_ms",
	})

	start := time.Now()
	res, errMsg := h.callTool("run_experiment", map[string]any{"timeout_seconds": 1})
	elapsed := time.Since(start)
	if errMsg != "" {
		t.Fatalf("run_experiment unexpected error: %s", errMsg)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("timeout took %v (expected ~1-2s; orphaned child process likely leaked)", elapsed)
	}
	txt := firstText(t, res)
	if !strings.Contains(txt, "TIMEOUT") {
		t.Fatalf("expected TIMEOUT in output, got:\n%s", txt)
	}
	updates := h.updatesSnapshot()
	if len(updates) == 0 {
		t.Fatalf("expected at least one live OnUpdate progress notification during run")
	}
	firstUpd, _ := updates[0]["result"].(map[string]any)
	details, _ := firstUpd["details"].(map[string]any)
	if details["phase"] != "running" {
		t.Fatalf("expected progress details.phase == running, got %+v", details)
	}
}

func TestRunExperimentUserAbortKillsGroupAndLeavesRunUncompleted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sleep/process-group test")
	}
	isolateStorage(t)
	ctx := context.Background()
	repo := t.TempDir()
	initTestGitRepo(t, repo)

	writeHarness(t, repo, "echo running\nsleep 15 &\nwait")
	h := startHost(t, newTestExtension(), hostOptions{
		Cwd:         repo,
		ActiveTools: append([]string{"read"}, ExperimentToolNames...),
	})
	_, _ = h.callTool("init_experiment", map[string]any{
		"name":           "abort-test",
		"primary_metric": "runtime_ms",
	})

	start := time.Now()
	reqID, doneCh := h.startToolCallAsync("run_experiment", map[string]any{"timeout_seconds": 30})
	time.Sleep(300 * time.Millisecond)
	h.cancelRequest(reqID)

	select {
	case outcome := <-doneCh:
		elapsed := time.Since(start)
		if elapsed > 4*time.Second {
			t.Fatalf("abort took %v (expected <4s)", elapsed)
		}
		if !strings.Contains(strings.ToLower(outcome.errMsg), "abort") && !strings.Contains(strings.ToLower(outcome.errMsg), "cancel") {
			t.Fatalf("expected aborted/cancelled error, got res=%+v err=%q", outcome.res, outcome.errMsg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run_experiment did not return after user abort")
	}

	// Verify the run row was NOT marked completed (matches upstream run-experiment.ts:322-324).
	s, err := OpenAutoresearchStorageIfExists(ctx, repo)
	if err != nil || s == nil {
		t.Fatalf("open storage: %v", err)
	}
	sess := s.GetActiveSession()
	if sess == nil {
		t.Fatalf("expected active session")
	}
	runs := s.ListRuns(sess.ID)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run row, got %d", len(runs))
	}
	if runs[0].CompletedAt != nil {
		t.Fatalf("aborted run must remain uncompleted (CompletedAt == nil), got %d", *runs[0].CompletedAt)
	}
}

func TestJSNumberStringAndBareCRMetrics(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{42, "42"},
		{42.5, "42.5"},
		{1234567, "1234567"},
		{1234567890, "1234567890"},
		{0.00001234, "0.00001234"},
		{1e-6, "0.000001"},
		{1e-7, "1e-7"},
		{1e21, "1e+21"},
		{-0.00005, "-0.00005"},
	}
	for _, tc := range cases {
		if got := JSNumberString(tc.in); got != tc.want {
			t.Errorf("JSNumberString(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// Bare \r-delimited METRIC/ASI lines (e.g. progress bars / CR-only output).
	raw := "progress 50%\rMETRIC latency_ns=1234567\rMETRIC ratio=0.00001234\rASI note=ok\r"
	metrics := ParseMetricLines(raw)
	if metrics["latency_ns"] != 1234567 || metrics["ratio"] != 0.00001234 {
		t.Fatalf("bare CR ParseMetricLines failed: %+v", metrics)
	}
	asi := ParseASILines(raw)
	if asi["note"] != "ok" {
		t.Fatalf("bare CR ParseASILines failed: %+v", asi)
	}

	// Zero baseline delta prints +Infinity% (matching JS division by 0).
	state := CreateExperimentState()
	state.MetricName = "errors"
	state.BestMetric = new(0.0)
	exp := ExperimentResult{
		RunNumber:   new(2),
		Status:      StatusDiscard,
		Metric:      5,
		Description: "nonzero after zero baseline",
	}
	txt := buildLogText(state, exp, 2, nil, "", nil, nil)
	if !strings.Contains(txt, "(+Infinity%)") {
		t.Fatalf("expected (+Infinity%%) for zero baseline delta, got:\n%s", txt)
	}

	// UTF-16 truncation must not split multi-byte UTF-8 runes.
	emoji := strings.Repeat("🚀", 70) // 70 emoji = 140 UTF-16 code units, 280 UTF-8 bytes
	trunc := truncateASIValue(emoji)
	if !strings.HasSuffix(trunc, "...") {
		t.Fatalf("expected ellipsis on >120 UTF-16 code units, got %q", trunc)
	}
	for _, r := range trunc {
		if r == '\uFFFD' {
			t.Fatalf("truncation corrupted UTF-8 rune: %q", trunc)
		}
	}
}

func TestStorageTornTailThenAppendAndLargeASIRow(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.OpenSession(OpenSessionParams{
		Name:          "durability",
		PrimaryMetric: "ms",
		Direction:     DirectionLower,
	})
	if err != nil {
		t.Fatal(err)
	}
	r1, err := s.InsertRun(InsertRunParams{
		SessionID: sess.ID,
		Segment:   0,
		Command:   "bash autoresearch.sh",
		LogPath:   "/tmp/1.log",
		StartedAt: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Simulate crash leaving a torn trailing JSONL record WITHOUT trailing newline.
	f, err := os.OpenFile(filepath.Join(dir, "runs.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"id":99,"sessionId":1,"command":"torn-crash-line`)
	_ = f.Close()

	// Reload (must truncate torn tail), then append a new run + a >4 MiB ASI payload, then reload again!
	s2, err := NewStorage(dir)
	if err != nil {
		t.Fatalf("reload after torn tail: %v", err)
	}
	bigPayload := strings.Repeat("x", 5*1024*1024) // 5 MiB (> 4 MiB bufio.Scanner limit)
	r2, err := s2.InsertRun(InsertRunParams{
		SessionID: sess.ID,
		Segment:   0,
		Command:   "bash autoresearch.sh",
		LogPath:   "/tmp/2.log",
		StartedAt: 2,
	})
	if err != nil {
		t.Fatalf("insert run after torn tail recovery: %v", err)
	}
	if _, err := s2.MarkRunLogged(MarkRunLoggedParams{
		RunID:       r2.ID,
		Status:      StatusKeep,
		Description: "huge asi",
		Metric:      10,
		ASI:         ASIData{"blob": bigPayload},
		LoggedAt:    3,
	}); err != nil {
		t.Fatalf("mark run logged with >4MiB ASI: %v", err)
	}
	r3, err := s2.InsertRun(InsertRunParams{
		SessionID: sess.ID,
		Segment:   0,
		Command:   "bash autoresearch.sh",
		LogPath:   "/tmp/3.log",
		StartedAt: 4,
	})
	if err != nil {
		t.Fatalf("insert r3 after huge ASI: %v", err)
	}
	s2.Close()

	// Reload a third time and confirm r1, r2 (with 5 MiB ASI), and r3 ALL survived!
	s3, err := NewStorage(dir)
	if err != nil {
		t.Fatalf("third reload failed: %v", err)
	}
	defer s3.Close()
	if s3.GetRunByID(r1.ID) == nil {
		t.Fatalf("r1 lost after torn-tail recovery")
	}
	gotR2 := s3.GetRunByID(r2.ID)
	if gotR2 == nil || gotR2.ASI == nil || len(gotR2.ASI["blob"].(string)) != len(bigPayload) {
		t.Fatalf("r2 (>4 MiB ASI) lost or truncated on reload")
	}
	if s3.GetRunByID(r3.ID) == nil {
		t.Fatalf("r3 (following >4 MiB line) lost on reload")
	}
}

func TestFullStreamTotalsAcrossLargeOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX bash harness test")
	}
	dir := t.TempDir()
	// Emit ~150 KB (> 100 KB tail buffer) with 3000 lines.
	writeHarness(t, dir, `for i in $(seq 1 3000); do printf "line-%04d %040d\n" "$i" 0; done; echo "METRIC runtime_ms=1234567"`)
	logPath := filepath.Join(dir, "benchmark.log")
	res, err := executeHarness(context.Background(), dir, logPath, 15*time.Second, nil)
	if err != nil {
		t.Fatalf("executeHarness: %v", err)
	}
	if res.metrics["runtime_ms"] != 1234567 {
		t.Fatalf("metric = %v, want 1234567", res.metrics["runtime_ms"])
	}
	if res.llmTruncation.TotalLines < 3001 {
		t.Fatalf("TotalLines = %d, want >= 3001 (full stream, not 100KB tail buffer)", res.llmTruncation.TotalLines)
	}
	if res.llmTruncation.TotalBytes < 140*1024 {
		t.Fatalf("TotalBytes = %d, want >= 140KB (full stream)", res.llmTruncation.TotalBytes)
	}
	if !res.llmTruncation.Truncated {
		t.Fatalf("expected Truncated=true for large output")
	}
}

func TestRunExperimentWaitDelayPreservesExitZeroAndReapsBackgroundChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX background child pipe test")
	}
	isolateStorage(t)
	repo := t.TempDir()
	initTestGitRepo(t, repo)

	// Harness spawns a background child holding stdout/stderr open and exits 0.
	// WaitDelay (~2s) should return quickly with exitCode=0, passed=true, parsed metric a=1,
	// and reap the lingering background child process group.
	writeHarness(t, repo, "sleep 8 &\necho METRIC a=1\nexit 0")

	h := startHost(t, newTestExtension(), hostOptions{
		Cwd:         repo,
		ActiveTools: append([]string{"read"}, ExperimentToolNames...),
	})
	_, _ = h.callTool("init_experiment", map[string]any{
		"name":           "waitdelay-test",
		"primary_metric": "a",
	})

	start := time.Now()
	res, errMsg := h.callTool("run_experiment", map[string]any{"timeout_seconds": 15})
	elapsed := time.Since(start)
	if errMsg != "" {
		t.Fatalf("run_experiment error: %s", errMsg)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("expected WaitDelay to return in ~2s, took %v", elapsed)
	}
	txt := firstText(t, res)
	if !strings.Contains(txt, "PASSED in ") {
		t.Fatalf("expected PASSED output on exit 0 with lingering child, got:\n%s", txt)
	}
	if !strings.Contains(txt, "Parsed a: 1") {
		t.Fatalf("expected parsed metric a=1, got:\n%s", txt)
	}
	details, _ := res["details"].(map[string]any)
	if passed, _ := details["passed"].(bool); !passed {
		t.Fatalf("expected details.passed=true, got %+v", details)
	}
	if crashed, _ := details["crashed"].(bool); crashed {
		t.Fatalf("expected details.crashed=false, got %+v", details)
	}
	if code, ok := details["exitCode"].(float64); !ok || code != 0 {
		t.Fatalf("expected details.exitCode=0, got %+v", details["exitCode"])
	}
}
