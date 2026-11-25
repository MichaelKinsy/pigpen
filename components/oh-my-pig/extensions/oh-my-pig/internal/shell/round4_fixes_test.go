package shell

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/approval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// Round 4 oracle triage regressions (Findings 2, 4, 7 + bash/proc details).

// TestRound4_Finding2_ForegroundReleaseReusesNewestJobID verifies that a
// foreground bash call that finishes inline returns its `bg_N` id to the
// counter when it holds the newest id, matching omp's `#discardForegroundJob`
// (`async/job-manager.ts:838-841, 1191-1200`) and oracle case
// `bash-notify-streams-cancel` (#0 async -> bg_1, #1 foreground -> discarded,
// #3 async -> bg_2, #6 async -> bg_3).
func TestRound4_Finding2_ForegroundReleaseReusesNewestJobID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test")
	}
	jobs.ResetForTests()
	t.Cleanup(jobs.ResetForTests)

	tool, dir := newTestTool(t, func(cfg *Config) {
		cfg.AutoBackgroundEnabled = true
		cfg.AutoBackgroundThreshold = 30 * time.Second
	})

	// 1. First async job takes bg_1.
	res1, err := runTool(t, tool, map[string]any{
		"command": "echo first_async",
		"cwd":     dir,
		"async":   true,
	})
	if err != nil {
		t.Fatalf("async 1: %v", err)
	}
	if !strings.Contains(res1.Content, "Backgrounded as job bg_1") {
		t.Fatalf("expected bg_1, got %q", res1.Content)
	}

	// 2. Foreground call completes inline — must release its bg_2 id back!
	resFg, err := runTool(t, tool, map[string]any{
		"command": "echo fg_inline",
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("fg call: %v", err)
	}
	if !strings.Contains(resFg.Content, "fg_inline") {
		t.Fatalf("unexpected fg output: %q", resFg.Content)
	}

	// 3. Second async job MUST receive bg_2 (not bg_3!).
	res2, err := runTool(t, tool, map[string]any{
		"command": "echo second_async",
		"cwd":     dir,
		"async":   true,
	})
	if err != nil {
		t.Fatalf("async 2: %v", err)
	}
	if !strings.Contains(res2.Content, "Backgrounded as job bg_2") {
		t.Fatalf("expected bg_2 after released foreground job, got %q", res2.Content)
	}

	// 4. Out-of-order release: if a newer job (bg_4) is allocated while fg job
	// (bg_3) is still running, releasing bg_3 must NOT decrement nextID below 4.
	rt := tool.runtime
	doneCh := make(chan struct{})
	fgOld, err := rt.Jobs.register("sess-ooo", "bash", "fg-old", "sleep 1", dir, "", true, func(_ context.Context, _ string, _ func(string), _ func(map[string]any), _ func(func() []int)) (string, error) {
		<-doneCh
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("register fgOld: %v", err)
	}
	bgNewer, err := rt.Jobs.register("sess-ooo", "bash", "bg-newer", "echo hi", dir, "", false, func(_ context.Context, _ string, _ func(string), _ func(map[string]any), _ func(func() []int)) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("register bgNewer: %v", err)
	}
	close(doneCh)
	<-fgOld.done
	rt.Jobs.releaseForegroundJob(fgOld.id)
	bgAfter, err := rt.Jobs.register("sess-ooo", "bash", "bg-after", "echo hi", dir, "", false, func(_ context.Context, _ string, _ func(string), _ func(map[string]any), _ func(func() []int)) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("register bgAfter: %v", err)
	}
	if bgAfter.id == bgNewer.id {
		t.Fatalf("out-of-order foreground release reused live id %q", bgNewer.id)
	}
	if fgOld.id != "bg_3" || bgNewer.id != "bg_4" || bgAfter.id != "bg_5" {
		t.Fatalf("unexpected sequence: fgOld=%s bgNewer=%s bgAfter=%s", fgOld.id, bgNewer.id, bgAfter.id)
	}
}

// TestRound4_Finding4_BashToolPolicyDenyPrefix verifies that tool-owned bash
// pattern denials carry omp's `Tool "bash" is blocked by tool policy.\nReason: `
// prefix (`tools/approval.ts:594-598`; oracle `approvals-policy-and-patterns` #2).
func TestRound4_Finding4_BashToolPolicyDenyPrefix(t *testing.T) {
	prevSetting := Setting
	prevSession := SessionSetting
	prevApprSetting := approval.Setting
	prevApprSession := approval.SessionSetting
	t.Cleanup(func() {
		Setting = prevSetting
		SessionSetting = prevSession
		approval.Setting = prevApprSetting
		approval.SessionSetting = prevApprSession
	})

	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "tools.approvalMode":
			return "always-ask"
		case "bash.patterns":
			return []any{map[string]any{"match": "echo forbidden*", "approval": "deny"}}
		}
		return nil
	}
	SessionSetting = Setting
	approval.Setting = Setting
	approval.SessionSetting = Setting

	want := "Tool \"bash\" is blocked by tool policy.\nReason: Blocked by bash pattern: echo forbidden*"

	// Unified approval.HandleToolCallVeto (via init()-registered EvaluateBashToolDecision)
	res2, err := approval.HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "bash",
		"input":    map[string]any{"command": "echo forbidden_cmd"},
	})
	m2, _ := res2.(map[string]any)
	if err != nil || m2 == nil || m2["block"] != true || m2["reason"] != want {
		t.Fatalf("approval.HandleToolCallVeto:\ngot:  %#v (err=%v)\nwant: block=true reason=%q", res2, err, want)
	}
}

// TestRound4_Finding7_ColumnCapReservesEllipsisAndMultiChunk verifies that the
// 3-byte UTF-8 ellipsis `"…"` is counted INSIDE the per-line byte cap
// (`streaming-output.ts:1186-1210`; oracle `bash-cwd-spill` #6: 765 + "…" = 768 bytes)
// both for a single chunk and for a long line arriving across several chunks,
// and that `meta.limits.columnTruncated.selectorBase` is omitted.
func TestRound4_Finding7_ColumnCapReservesEllipsisAndMultiChunk(t *testing.T) {
	artDir := filepath.Join(t.TempDir(), "colcap")
	store := NewArtifactStore(artDir)
	t.Cleanup(store.DisposeAll)

	// 1. Single chunk of 3000 'x' with default 768 byte cap -> 765 'x' + "…" = 768 bytes (766 runes).
	sink1 := NewOutputSink(OutputSinkOptions{
		SpillThreshold: DefaultSpillThreshold,
		HeadBytes:      DefaultHeadBytes,
		MaxColumns:     DefaultMaxColumns,
		ArtifactStore:  store,
		SessionID:      "sess-col-1",
	})
	sink1.Push(strings.Repeat("x", 3000) + "\n")
	sum1 := sink1.Dump("")
	lines1 := strings.Split(sum1.Output, "\n")
	if len(lines1) == 0 {
		t.Fatalf("empty output")
	}
	wantLine := strings.Repeat("x", DefaultMaxColumns-3) + "…"
	if lines1[0] != wantLine {
		t.Fatalf("single-chunk line: lenBytes=%d lenRunes=%d, want %d bytes (%d runes)",
			len(lines1[0]), len([]rune(lines1[0])), len(wantLine), len([]rune(wantLine)))
	}
	if len(lines1[0]) != DefaultMaxColumns {
		t.Fatalf("line byte length = %d, want %d", len(lines1[0]), DefaultMaxColumns)
	}

	// Verify meta.limits.columnTruncated omits selectorBase.
	meta := buildOutputMeta(sum1)
	limits, _ := meta["limits"].(map[string]any)
	col, _ := limits["columnTruncated"].(map[string]any)
	if col == nil {
		t.Fatalf("expected meta.limits.columnTruncated, got %#v", meta)
	}
	if _, hasSelBase := col["selectorBase"]; hasSelBase {
		t.Fatalf("meta.limits.columnTruncated must NOT include selectorBase, got %#v", col)
	}

	// 2. Multi-chunk line: cap=20, chunk1=15 bytes (fits), chunk2=10 bytes
	// (overflows: remaining=5, headRoom=5-3=2 bytes kept + "…" = 20 bytes total),
	// chunk3=10 bytes (already past cap: dropped), chunk4="\nsecond\n".
	sink2 := NewOutputSink(OutputSinkOptions{
		SpillThreshold: DefaultSpillThreshold,
		HeadBytes:      DefaultHeadBytes,
		MaxColumns:     20,
		ArtifactStore:  store,
		SessionID:      "sess-col-2",
	})
	sink2.Push("abcdefghijklmno") // 15 bytes -> remaining=5
	sink2.Push("0123456789")      // overflows: keep 5-3=2 ("01") + "…" -> 20 bytes
	sink2.Push("ZZZZZZZZZZ")      // dropped
	sink2.Push("\nsecond\n")      // newline resets line counter
	sum2 := sink2.Dump("")
	lines2 := strings.Split(sum2.Output, "\n")
	wantMulti := "abcdefghijklmno01…"
	if lines2[0] != wantMulti {
		t.Fatalf("multi-chunk capped line = %q (%d bytes), want %q (%d bytes)",
			lines2[0], len(lines2[0]), wantMulti, len(wantMulti))
	}
	if len(lines2[0]) != 20 {
		t.Fatalf("multi-chunk line byte length = %d, want 20", len(lines2[0]))
	}
	if len(lines2) < 2 || lines2[1] != "second" {
		t.Fatalf("second line after newline = %q, want \"second\"", lines2)
	}

	// 3. Multi-chunk boundary when remaining < 3 (e.g. cap=10, chunk1=9 bytes, chunk2="ab"):
	// remaining=1, headRoom=max(0, 1-3)=0 -> emit "…" directly without cutting into chunk1.
	sink3 := NewOutputSink(OutputSinkOptions{
		SpillThreshold: DefaultSpillThreshold,
		HeadBytes:      DefaultHeadBytes,
		MaxColumns:     10,
		ArtifactStore:  store,
		SessionID:      "sess-col-3",
	})
	sink3.Push("123456789") // 9 bytes, fits
	sink3.Push("ab\n")      // remaining=1 -> headRoom=0 -> emits "…"
	sum3 := sink3.Dump("")
	if got := strings.Split(sum3.Output, "\n")[0]; got != "123456789…" {
		t.Fatalf("remaining<3 boundary = %q, want %q", got, "123456789…")
	}

	// 4. Multi-byte UTF-8 boundary: cap=10, line of 4-byte emoji ("😀" x 5 = 20 bytes):
	// headRoom=7 -> utf8BoundaryBackward keeps 1 emoji (4 bytes) + "…" (3 bytes) = 7 bytes valid UTF-8.
	sink4 := NewOutputSink(OutputSinkOptions{
		SpillThreshold: DefaultSpillThreshold,
		HeadBytes:      DefaultHeadBytes,
		MaxColumns:     10,
		ArtifactStore:  store,
		SessionID:      "sess-col-4",
	})
	sink4.Push("😀😀😀😀😀\n")
	sum4 := sink4.Dump("")
	if got := strings.Split(sum4.Output, "\n")[0]; got != "😀…" {
		t.Fatalf("utf8 boundary = %q, want %q", got, "😀…")
	}
}

// TestRound4_ProcKillTouchesNoFiles verifies that every `proc://<id>/kill`
// write result carries `details.touchesNoFiles: true` (service stop, running
// job cancel, jobless agent kill, evicted job cancel).
func TestRound4_ProcKillTouchesNoFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test")
	}
	jobs.ResetForTests()
	urls.ResetForTests()
	t.Cleanup(func() {
		jobs.ResetForTests()
		urls.ResetForTests()
	})

	rt := NewRuntime(t.TempDir())
	t.Cleanup(func() { rt.DisposeSessions() })
	if err := urls.Register(NewProcProtocolHandler(rt)); err != nil {
		t.Fatalf("register proc: %v", err)
	}
	tool := NewBashTool(DefaultConfig(), rt)
	dir := t.TempDir()
	urlCtx := urls.Context{Cwd: dir}

	// 1. Async job kill -> touchesNoFiles: true
	res, err := runTool(t, tool, map[string]any{
		"command": "sleep 30",
		"cwd":     dir,
		"async":   true,
	})
	if err != nil {
		t.Fatalf("start async sleep: %v", err)
	}
	if !strings.Contains(res.Content, "bg_1") {
		t.Fatalf("expected bg_1, got %q", res.Content)
	}
	killJobRes, err := urls.Write(urlCtx, "proc://bg_1/kill", "")
	if err != nil {
		t.Fatalf("kill bg_1: %v", err)
	}
	if killJobRes == nil || killJobRes.Details["touchesNoFiles"] != true {
		t.Fatalf("proc://bg_1/kill missing touchesNoFiles:true: %#v", killJobRes)
	}
	procDet, _ := killJobRes.Details["proc"].(map[string]any)
	metaDet, _ := procDet["meta"].(map[string]any)
	srcDet, _ := metaDet["source"].(map[string]any)
	if srcDet["type"] != "report" || srcDet["value"] != "background jobs snapshot" {
		t.Fatalf("proc://bg_1/kill missing proc.meta source report: %#v", procDet)
	}
	killJobs, _ := procDet["jobs"].([]map[string]any)
	if len(killJobs) != 1 {
		t.Fatalf("proc://bg_1/kill expected 1 job snapshot, got %#v", procDet["jobs"])
	}
	if _, hasSettled := killJobs[0]["settledAtMs"]; hasSettled {
		t.Fatalf("proc://bg_1/kill freshly cancelled snapshot must omit settledAtMs, got %#v", killJobs[0])
	}
	if _, hasResult := killJobs[0]["resultText"]; hasResult {
		t.Fatalf("proc://bg_1/kill consumed snapshot must omit resultText, got %#v", killJobs[0])
	}

	// 2. Service kill -> touchesNoFiles: true
	_, err = runTool(t, tool, map[string]any{
		"name":    "r4-svc",
		"command": "echo READY; sleep 30",
		"cwd":     dir,
		"ready":   map[string]any{"log": "READY", "timeout": 5},
	})
	if err != nil {
		t.Fatalf("start service: %v", err)
	}
	killSvcRes, err := urls.Write(urlCtx, "proc://r4-svc/kill", "")
	if err != nil {
		t.Fatalf("kill r4-svc: %v", err)
	}
	if killSvcRes == nil || killSvcRes.Details["touchesNoFiles"] != true {
		t.Fatalf("proc://r4-svc/kill missing touchesNoFiles:true: %#v", killSvcRes)
	}

	// 3. Jobless agent kill via KillAgentHook -> touchesNoFiles: true
	prevKill := KillAgentHook
	t.Cleanup(func() { KillAgentHook = prevKill })
	KillAgentHook = func(_, id string) (string, string) {
		if id == "ParkedAgent" {
			return "cancelled", fmt.Sprintf("Killed agent %s; its transcript stays at history://%s.", id, id)
		}
		return "not_found", ""
	}
	killAgentRes, err := urls.Write(urlCtx, "proc://ParkedAgent/kill", "")
	if err != nil {
		t.Fatalf("kill ParkedAgent: %v", err)
	}
	if killAgentRes == nil || killAgentRes.Details["touchesNoFiles"] != true {
		t.Fatalf("proc://ParkedAgent/kill missing touchesNoFiles:true: %#v", killAgentRes)
	}

	// 4. Evicted job kill -> touchesNoFiles: true
	jobs.RecordEvicted(sessionKeyFromContext(sdk.Context{}), jobs.EvictedRecord{
		ID:      "bg_99",
		Kind:    jobs.KindBash,
		Label:   "echo old",
		Status:  "completed",
		EndTime: time.Now(),
		Reason:  "expired",
	})
	killEvictedRes, err := urls.Write(urlCtx, "proc://bg_99/kill", "")
	if err != nil {
		t.Fatalf("kill bg_99: %v", err)
	}
	if killEvictedRes == nil || killEvictedRes.Details["touchesNoFiles"] != true {
		t.Fatalf("proc://bg_99/kill missing touchesNoFiles:true: %#v", killEvictedRes)
	}
}

// TestRound5_CancelledJobUsesInDuration verifies item 2: cancelled jobs (and
// all finished states: completed, failed, cancelled) display `in <duration>`
// instead of `up <duration>` in `proc://` listings, and Cancel() stamps
// endTime immediately so settledAtMs is populated even before process teardown.
func TestRound5_CancelledJobUsesInDuration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test")
	}
	jobs.ResetForTests()
	urls.ResetForTests()
	t.Cleanup(func() {
		jobs.ResetForTests()
		urls.ResetForTests()
	})

	rt := NewRuntime(t.TempDir())
	t.Cleanup(func() { rt.DisposeSessions() })
	if err := urls.Register(NewProcProtocolHandler(rt)); err != nil {
		t.Fatalf("register proc: %v", err)
	}
	tool := NewBashTool(DefaultConfig(), rt)
	dir := t.TempDir()
	urlCtx := urls.Context{Cwd: dir}

	_, err := runTool(t, tool, map[string]any{
		"command": "sleep 30",
		"cwd":     dir,
		"async":   true,
	})
	if err != nil {
		t.Fatalf("start async sleep: %v", err)
	}
	// Running listing uses "up".
	resRun, err := urls.Resolve(urlCtx, "proc://")
	if err != nil {
		t.Fatalf("proc:// while running: %v", err)
	}
	if !strings.Contains(resRun.Content, "bg_1 [bash] running up ") {
		t.Fatalf("expected running up in proc://, got %q", resRun.Content)
	}

	// Kill and read proc:// immediately: MUST say "cancelled in ", NOT "cancelled up ".
	if _, err := urls.Write(urlCtx, "proc://bg_1/kill", ""); err != nil {
		t.Fatalf("kill bg_1: %v", err)
	}
	resCancelled, err := urls.Resolve(urlCtx, "proc://")
	if err != nil {
		t.Fatalf("proc:// after kill: %v", err)
	}
	if !strings.Contains(resCancelled.Content, "bg_1 [bash] cancelled in ") {
		t.Fatalf("expected `bg_1 [bash] cancelled in ...`, got %q", resCancelled.Content)
	}
	if strings.Contains(resCancelled.Content, "cancelled up ") {
		t.Fatalf("cancelled job must not use `up`, got %q", resCancelled.Content)
	}

	// Defensive check: even a finished job whose Ended() is zero uses "in".
	st := &stubFinishedJob{id: "stub_1", status: "exited", label: "done", started: time.Now().Add(-50 * time.Millisecond)}
	row := formatJobListRow(st, time.Now())
	if !strings.Contains(row, "exited in ") {
		t.Fatalf("expected `exited in ...` for finished job with zero Ended(), got %q", row)
	}
}

type stubFinishedJob struct {
	id      string
	status  string
	label   string
	started time.Time
}

func (s *stubFinishedJob) ID() string            { return s.id }
func (s *stubFinishedJob) Kind() jobs.Kind       { return jobs.KindBash }
func (s *stubFinishedJob) Label() string         { return s.label }
func (s *stubFinishedJob) Status() string        { return s.status }
func (s *stubFinishedJob) Started() time.Time    { return s.started }
func (s *stubFinishedJob) Output() string        { return "" }
func (s *stubFinishedJob) Result() string        { return "" }
func (s *stubFinishedJob) Done() <-chan struct{} { ch := make(chan struct{}); close(ch); return ch }
func (s *stubFinishedJob) Cancel()               {}

// TestRound5_BashJobCapturesIntent verifies item 3: when `tools.intentTracing`
// is enabled, `bash` reads `intent.For(ctx, toolCallID)` and propagates it onto
// the registered async job (`JobDetailer.Intent()`, `proc://` listing, `proc://<id>`,
// and `proc://<id>/kill` report).
func TestRound5_BashJobCapturesIntent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test")
	}
	jobs.ResetForTests()
	urls.ResetForTests()
	intent.ResetForTests()
	prevEnabled := intent.Enabled
	intent.Enabled = func(_ sdk.Context) bool { return true }
	t.Cleanup(func() {
		intent.Enabled = prevEnabled
		intent.ResetForTests()
		jobs.ResetForTests()
		urls.ResetForTests()
	})

	ext := sdk.New("bash-intent-test")
	Register(ext)
	dir := t.TempDir()
	sessID := "sess-bash-intent"
	h := startShellFakeHost(t, ext, dir, sessID)
	t.Cleanup(func() { defaultRuntime.DisposeSession(sessID) })

	raw, errMsg := h.callTool("bash", map[string]any{
		"i":       "Running background job",
		"command": "sleep 30",
		"cwd":     dir,
		"async":   true,
	})
	if errMsg != "" {
		t.Fatalf("bash async with intent failed: %s (raw=%s)", errMsg, raw)
	}

	list := jobs.List(sessID)
	if len(list) != 1 {
		t.Fatalf("expected 1 job in session %s, got %d", sessID, len(list))
	}
	det, ok := list[0].(jobs.JobDetailer)
	if !ok || det.Intent() != "Running background job" {
		t.Fatalf("job Intent() = %q (ok=%v), want %q", det.Intent(), ok, "Running background job")
	}

	row := formatJobListRow(list[0], time.Now())
	if !strings.Contains(row, "Running background job — sleep 30") {
		t.Fatalf("proc:// list row missing intent: %q", row)
	}
	snap := procJobSnapshotMap(sessID, list[0], time.Now(), false)
	if snap["intent"] != "Running background job" {
		t.Fatalf("procJobSnapshotMap intent = %#v, want %q", snap["intent"], "Running background job")
	}
	list[0].Cancel()
}

// TestRound5_MountedToolCommandApprovalParity verifies item 4:
//   - `lsp status` (pure mounted tool command without `$`, `<`, `>`, etc.)
//     resolves bash to `read` tier (allowed in `always-ask`), so the inner
//     `lsp` tool's own approval runs.
//   - `ast_edit -j '{"ops":[{"pat":"console.log($A)","out":"print($A)"}],"paths":["s.js"]}'`
//     contains `$` in raw token (`bash.ts:751`), so `isToolCommandLine` returns
//     false and bash stays `exec` tier, refused at the BASH call with
//     `Tool "bash" requires approval...` (oracle approvals-policy-and-patterns #10).
//   - Mixed lines (`ast_edit --help && rm x`, `lsp status && echo hi`) stay `exec`.
//   - Pure chains of mounted tool commands (`lsp status && ast_grep --help`) resolve to `read`.
//   - Direct call to `ast_edit` still refuses naming `ast_edit`.
func TestRound5_MountedToolCommandApprovalParity(t *testing.T) {
	prevCmdNames := CommandNames
	prevSetting := Setting
	prevSession := SessionSetting
	prevApprSetting := approval.Setting
	prevApprSession := approval.SessionSetting
	t.Cleanup(func() {
		CommandNames = prevCmdNames
		Setting = prevSetting
		SessionSetting = prevSession
		approval.Setting = prevApprSetting
		approval.SessionSetting = prevApprSession
	})

	CommandNames = func(_ sdk.Context) []string {
		return []string{"ast_edit", "ast_grep", "debug", "lsp"}
	}
	alwaysAsk := func(_ sdk.Context, key string) any {
		if key == "tools.approvalMode" {
			return "always-ask"
		}
		return nil
	}
	Setting = alwaysAsk
	SessionSetting = alwaysAsk
	approval.Setting = alwaysAsk
	approval.SessionSetting = alwaysAsk

	// 1. `lsp status` -> bash is read tier -> allowed under always-ask.
	if dec := EvaluateBashToolDecision(sdk.Context{}, map[string]any{"command": "lsp status"}); dec.Tier != approval.TierRead {
		t.Fatalf("lsp status: got tier %q, want read", dec.Tier)
	}
	if res, err := approval.HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "bash",
		"input":    map[string]any{"command": "lsp status"},
	}); err != nil || res != nil {
		t.Fatalf("bash `lsp status` under always-ask: got res=%#v err=%v, want allow", res, err)
	}

	// 2. Pure chained mounted tool commands -> read tier (upstream tool-command-approval-gate.test.ts:62-70).
	for _, cmd := range []string{
		"lsp status && ast_grep --help",
		"lsp status || ast_grep --help",
		"lsp status; ast_grep --help",
		"lsp status | ast_grep --help",
		"lsp status\nast_grep --help",
	} {
		if dec := EvaluateBashToolDecision(sdk.Context{}, map[string]any{"command": cmd}); dec.Tier != approval.TierRead {
			t.Fatalf("pure tool-command line %q: got tier %q, want read", cmd, dec.Tier)
		}
	}

	// 3. Oracle call #10: `ast_edit -j '{"ops":[{"pat":"console.log($A)","out":"print($A)"}],"paths":["s.js"]}'`
	// contains `$` in token.raw (upstream bash.ts:751), so bash stays exec-tier and
	// is refused at the bash call naming `bash` (NOT letting bash start and failing inside ast_edit).
	oracleCmd := `ast_edit -j '{"ops":[{"pat":"console.log($A)","out":"print($A)"}],"paths":["s.js"]}'`
	if dec := EvaluateBashToolDecision(sdk.Context{}, map[string]any{"command": oracleCmd}); dec.Tier != approval.TierExec {
		t.Fatalf("oracle ast_edit with $A: got tier %q, want exec", dec.Tier)
	}
	res, err := approval.HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "bash",
		"input":    map[string]any{"command": oracleCmd},
	})
	m, _ := res.(map[string]any)
	if err != nil || m == nil || m["block"] != true || !strings.Contains(fmt.Sprint(m["reason"]), `Tool "bash" requires approval`) || !strings.Contains(fmt.Sprint(m["reason"]), "tools.approval.bash: allow") {
		t.Fatalf("oracle bash ast_edit refusal: got res=%#v err=%v", res, err)
	}

	// 4. Mixed command lines & shell syntax -> exec tier.
	for _, cmd := range []string{
		"ast_edit --help && rm x",
		"lsp status && echo hi",
		"lsp status > /tmp/out",
		"lsp status & ast_grep --help",
		"lsp status # comment",
		"lsp $(echo status)",
	} {
		if dec := EvaluateBashToolDecision(sdk.Context{}, map[string]any{"command": cmd}); dec.Tier != approval.TierExec {
			t.Fatalf("mixed/shell command %q: got tier %q, want exec", cmd, dec.Tier)
		}
	}

	// 5. Direct call to `ast_edit` -> refused naming `ast_edit`.
	directRes, err := approval.HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "ast_edit",
		"input":    map[string]any{"paths": []any{"s.js"}},
	})
	dm, _ := directRes.(map[string]any)
	if err != nil || dm == nil || dm["block"] != true || !strings.Contains(fmt.Sprint(dm["reason"]), `Tool "ast_edit" requires approval`) {
		t.Fatalf("direct ast_edit refusal: got res=%#v err=%v", directRes, err)
	}

	// 6. Verify proc.md & proc_reference.md do not promise live streams.
	if strings.Contains(procPromptDoc, "/streams") {
		t.Fatalf("proc.md should not advertise /streams: %q", procPromptDoc)
	}
	if !strings.Contains(procReferenceDoc, "not available") {
		t.Fatalf("proc_reference.md should state /streams is not available: %q", procReferenceDoc)
	}
}

// TestRound5_All10BashInterceptorRulesAndTwoViewEnv verifies all 10 upstream
// DEFAULT_BASH_INTERCEPTOR_RULES (`exec/settings.ts:29-110`, including the 4
// lookaround rules implemented in linear-time Go) and the strict-of-two-views
// `bash.allowedEnvVars` rule (`CI=1 npm test` prompts when project overrides
// `bash.allowedEnvVars: ["LD_PRELOAD"]`).
func TestRound5_All10BashInterceptorRulesAndTwoViewEnv(t *testing.T) {
	tools := []string{"read", "grep", "glob", "edit", "write", "bash"}

	// Echo/printf/cat write redirects block real file writes, ignore /dev sinks & quotes & fd dups.
	for _, tc := range []struct {
		cmd   string
		block bool
		tool  string
	}{
		{"echo hi > out.txt", true, "write"},
		{"printf x > f.txt", true, "write"},
		{"echo hi >> out.txt", true, "write"},
		{"echo hi >| out.txt", true, "write"},
		{"echo hi > $OUT", true, "write"},
		{"echo data > ./dev/null", true, "write"},
		{"echo data > /dev/null > out.txt", true, "write"},
		{"echo hi 2>&1 > out.txt", true, "write"},
		{"echo $(printf foo; printf bar) > out.txt", true, "write"},
		{"cat <<'EOF' > /tmp/f\nx\nEOF", true, "write"},
		{"cat > /tmp/f <<'EOF'\nx\nEOF", true, "write"},
		{"cat 2>err.log > out.txt", true, "write"},
		{"cat file.txt", true, "read"},
		{"cat 2>/dev/null file.txt", true, "read"},
		{"cat >&2 file.txt", true, "read"},
		{"echo result > /dev/null", false, ""},
		{"echo done > /dev/null 2>&1", false, ""},
		{`echo "a -> b"`, false, ""},
		{"echo x 2>err.log", false, ""},
		{"cat <<'EOF'\nx\nEOF", false, ""},
		{"sleep 5 &", true, "bash"},
		{"nohup server", true, "bash"},
		{"npm run dev", true, "bash"},
		{"vite --host 0.0.0.0", true, "bash"},
		{"docker compose up", true, "bash"},
		{"docker compose up -d", false, ""},
		{"npm test -- --watch", true, "bash"},
		{"git diff -w", false, ""},
	} {
		got := CheckBashInterception(tc.cmd, tools, DefaultInterceptorRules, tc.cmd)
		if got.Block != tc.block || (tc.block && got.SuggestedTool != tc.tool) {
			t.Errorf("CheckBashInterception(%q): got block=%v tool=%q, want block=%v tool=%q",
				tc.cmd, got.Block, got.SuggestedTool, tc.block, tc.tool)
		}
	}

	// Two-view bash.allowedEnvVars: operator allows `npm test*` with `[CI]`,
	// project sets `bash.allowedEnvVars: [LD_PRELOAD]` -> session view does NOT
	// allow `CI=1 npm test`, so stricter-of-two-views returns exec (prompts), never allow.
	opRules := []BashApprovalPatternRule{{Match: "npm test*", Approval: "allow"}}
	v := evaluateBashApprovalViews("CI=1 npm test", opRules, []string{"CI"}, opRules, []string{"LD_PRELOAD"})
	if v.Kind != "exec" {
		t.Fatalf("two-view CI=1 npm test with project allowedEnvVars=[LD_PRELOAD]: got %+v, want exec", v)
	}
	// When both views allow CI, allow stands.
	vAllow := evaluateBashApprovalViews("CI=1 npm test", opRules, []string{"CI"}, opRules, []string{"CI"})
	if vAllow.Kind != "allow" {
		t.Fatalf("both views allow CI=1 npm test: got %+v, want allow", vAllow)
	}
}

// TestRound5_CancelledJobPreservesErrorTextOnProcListing verifies the fourth
// oracle finding (`bash-notify-streams-cancel#12`): cancelling a running bash
// job leaves `errorText: "[Command cancelled]\n"` on the subsequent `proc://`
// listing snapshot (matching omp `async/job-manager.ts:665-673,1164-1168` and
// `async/job-control.ts:176,244-248`).
func TestRound5_CancelledJobPreservesErrorTextOnProcListing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test")
	}
	jobs.ResetForTests()
	t.Cleanup(jobs.ResetForTests)

	tool, dir := newTestTool(t, func(cfg *Config) {
		cfg.AutoBackgroundEnabled = false
	})
	res, err := runTool(t, tool, map[string]any{
		"command": "sleep 30",
		"cwd":     dir,
		"async":   true,
	})
	if err != nil {
		t.Fatalf("async sleep: %v", err)
	}
	if !strings.Contains(res.Content, "bg_1") {
		t.Fatalf("expected bg_1, got %q", res.Content)
	}

	// Register proc:// handler on the shared test runtime.
	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)
	if err := urls.Register(NewProcProtocolHandler(tool.runtime)); err != nil {
		t.Fatalf("register proc://: %v", err)
	}

	// Cancel bg_1 via proc://bg_1/kill.
	killRes, err := urls.Write(urls.Context{}, "proc://bg_1/kill", "")
	if err != nil {
		t.Fatalf("proc://bg_1/kill: %v", err)
	}
	// Immediate cancel snapshot omits errorText (omp has not yet resolved the promise).
	killProc, _ := killRes.Details["proc"].(map[string]any)
	killJobs, _ := killProc["jobs"].([]map[string]any)
	if len(killJobs) != 1 || killJobs[0]["status"] != "cancelled" {
		t.Fatalf("kill details jobs mismatch: %#v", killProc)
	}
	if _, hasErr := killJobs[0]["errorText"]; hasErr {
		t.Fatalf("immediate kill snapshot should omit errorText, got %#v", killJobs[0])
	}

	// Wait for the background goroutine to finish cancelling and record errorText.
	j, ok := jobs.Get(procSessionID(urls.Context{}), "bg_1")
	if !ok {
		t.Fatalf("bg_1 missing from jobs")
	}
	select {
	case <-j.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for cancelled bg_1 to settle")
	}
	// Subsequent `read proc://` MUST carry errorText: "[Command cancelled]\n".
	listRes, err := urls.Resolve(urls.Context{}, "proc://")
	if err != nil {
		t.Fatalf("read proc://: %v", err)
	}
	listProc, _ := listRes.Details["proc"].(map[string]any)
	listJobs, _ := listProc["jobs"].([]map[string]any)
	if len(listJobs) != 1 {
		t.Fatalf("expected 1 job in proc:// listing, got %#v", listProc)
	}
	if gotErr, _ := listJobs[0]["errorText"].(string); gotErr != "[Command cancelled]\n" {
		t.Fatalf("cancelled job errorText on proc:// = %q, want %q", gotErr, "[Command cancelled]\n")
	}
	if _, hasRes := listJobs[0]["resultText"]; hasRes {
		t.Fatalf("cancelled job should not have resultText: %#v", listJobs[0])
	}
}

// TestRound5_BashNotifyStreamsCancelExactSequence reproduces the exact oracle
// `bash-notify-streams-cancel` kill sequence 200 times in a loop with a fresh
// async `sleep 30` job each iteration and ZERO delay between `write proc://<id>/kill`
// returning and `read proc://`. Every iteration must:
//   - omit `errorText` on the immediate `kill` response snapshot, and
//   - deterministically observe `errorText: "[Command cancelled]\n"` on the very
//     next `read proc://` call with no sleep or polling.
//
// upstream: exec/bash-executor.ts:1008-1037, async/job-manager.ts:665-673,1164-1168,
//
//	async/job-control.ts:176,244-248
func TestRound5_BashNotifyStreamsCancelExactSequence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test")
	}
	jobs.ResetForTests()
	t.Cleanup(jobs.ResetForTests)

	tool, dir := newTestTool(t, func(cfg *Config) {
		cfg.AutoBackgroundEnabled = true
		cfg.AutoBackgroundThreshold = 30 * time.Second
	})
	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)
	if err := urls.Register(NewProcProtocolHandler(tool.runtime)); err != nil {
		t.Fatalf("register proc://: %v", err)
	}

	const iterations = 200
	for i := 1; i <= iterations; i++ {
		jobID := fmt.Sprintf("bg_%d", i)
		startRes, err := runTool(t, tool, map[string]any{
			"command": "sleep 30",
			"cwd":     dir,
			"async":   true,
		})
		if err != nil || !strings.Contains(startRes.Content, jobID) {
			t.Fatalf("iter %d async sleep 30: res=%+v err=%v", i, startRes, err)
		}

		killRes, err := urls.Write(urls.Context{}, "proc://"+jobID+"/kill", "")
		if err != nil {
			t.Fatalf("iter %d write proc://%s/kill: %v", i, jobID, err)
		}
		killProc, _ := killRes.Details["proc"].(map[string]any)
		killJobs, _ := killProc["jobs"].([]map[string]any)
		if len(killJobs) != 1 || killJobs[0]["status"] != "cancelled" {
			t.Fatalf("iter %d kill details jobs mismatch: %#v", i, killProc)
		}
		if _, hasErr := killJobs[0]["errorText"]; hasErr {
			t.Fatalf("iter %d immediate kill snapshot must omit errorText, got %#v", i, killJobs[0])
		}

		// ZERO delay: read proc:// immediately after kill returns.
		listRes, err := urls.Resolve(urls.Context{}, "proc://")
		if err != nil {
			t.Fatalf("iter %d post-kill read proc://: %v", i, err)
		}
		listProc, _ := listRes.Details["proc"].(map[string]any)
		listJobs, _ := listProc["jobs"].([]map[string]any)
		var found map[string]any
		for _, snap := range listJobs {
			if snap["id"] == jobID {
				found = snap
				break
			}
		}
		if found == nil {
			t.Fatalf("iter %d job %s missing from proc:// listing", i, jobID)
		}
		if gotErr, _ := found["errorText"].(string); gotErr != "[Command cancelled]\n" {
			t.Fatalf("iter %d post-kill read proc:// errorText = %q, want %q (zero delay)", i, gotErr, "[Command cancelled]\n")
		}
		if _, hasRes := found["resultText"]; hasRes {
			t.Fatalf("iter %d cancelled job must not carry resultText: %#v", i, found)
		}
	}
}

// TestRound5_BashNotifyStreamsCancelFakeHost runs the exact oracle sequence
// through `shellFakeHost` (`bash` async -> foreground sleep -> `read proc://` ->
// `write proc://<id>/kill` -> zero-delay `read proc://`) with a real session ID.
func TestRound5_BashNotifyStreamsCancelFakeHost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test")
	}
	jobs.ResetForTests()
	urls.ResetForTests()
	t.Cleanup(func() {
		jobs.ResetForTests()
		urls.ResetForTests()
	})

	ext := sdk.New("bash-cancel-fakehost-test")
	Register(ext)
	var postRes *urls.Resource
	var killRes *urls.WriteResult
	var preRes *urls.Resource
	ext.RegisterTool(sdk.ToolDefinition{
		Name:       "read_proc_test",
		Parameters: map[string]any{"type": "object"},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			path, _ := params["path"].(string)
			r, err := urls.Resolve(urls.Context{SDK: ctx, Cwd: ctx.Cwd()}, path)
			if err != nil {
				return nil, err
			}
			if params["phase"] == "pre" {
				preRes = r
			} else {
				postRes = r
			}
			return sdk.ToolResult{Content: r.Content, Details: r.Details}, nil
		},
	})
	ext.RegisterTool(sdk.ToolDefinition{
		Name:       "write_proc_test",
		Parameters: map[string]any{"type": "object"},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			path, _ := params["path"].(string)
			r, err := urls.Write(urls.Context{SDK: ctx, Cwd: ctx.Cwd()}, path, "")
			if err != nil {
				return nil, err
			}
			killRes = r
			return sdk.ToolResult{Content: r.Text, Details: r.Details}, nil
		},
	})

	dir := t.TempDir()
	sessID := "sess-cancel-det"
	h := startShellFakeHost(t, ext, dir, sessID)
	t.Cleanup(func() { defaultRuntime.DisposeSession(sessID) })

	// Start async sleep 30 via tool call through fakeHost.
	rawStart, errMsg := h.callTool("bash", map[string]any{
		"command": "sleep 30",
		"cwd":     dir,
		"async":   true,
	})
	if errMsg != "" {
		t.Fatalf("async sleep 30 failed: %s (raw=%s)", errMsg, rawStart)
	}
	list := jobs.List(sessID)
	if len(list) != 1 {
		t.Fatalf("expected 1 running job in session %s, got %d", sessID, len(list))
	}
	jobID := list[0].ID()

	// Short foreground sleep (exercises foreground id release under fakeHost too).
	if _, errMsg := h.callTool("bash", map[string]any{
		"command": "sleep 0.1; echo before_kill",
		"cwd":     dir,
	}); errMsg != "" {
		t.Fatalf("foreground sleep failed: %s", errMsg)
	}

	// Read proc:// -> running.
	if _, errMsg := h.callTool("read_proc_test", map[string]any{"path": "proc://", "phase": "pre"}); errMsg != "" {
		t.Fatalf("pre-kill proc:// failed: %s", errMsg)
	}
	if preRes == nil || !strings.Contains(preRes.Content, jobID+" [bash] running") {
		t.Fatalf("pre-kill proc:// mismatch: res=%+v", preRes)
	}

	// Kill via proc://<jobID>/kill -> kill response omits errorText.
	if _, errMsg := h.callTool("write_proc_test", map[string]any{"path": "proc://" + jobID + "/kill"}); errMsg != "" {
		t.Fatalf("write proc://%s/kill failed: %s", jobID, errMsg)
	}
	killProc, _ := killRes.Details["proc"].(map[string]any)
	killJobs, _ := killProc["jobs"].([]map[string]any)
	if len(killJobs) != 1 || killJobs[0]["status"] != "cancelled" {
		t.Fatalf("kill snapshot mismatch: %#v", killProc)
	}
	if _, hasErr := killJobs[0]["errorText"]; hasErr {
		t.Fatalf("kill snapshot must omit errorText: %#v", killJobs[0])
	}

	// ZERO delay: immediate read proc:// through fakeHost MUST carry errorText: "[Command cancelled]\n".
	if _, errMsg := h.callTool("read_proc_test", map[string]any{"path": "proc://", "phase": "post"}); errMsg != "" {
		t.Fatalf("post-kill read proc:// failed: %s", errMsg)
	}
	postProc, _ := postRes.Details["proc"].(map[string]any)
	postJobs, _ := postProc["jobs"].([]map[string]any)
	if len(postJobs) != 1 {
		t.Fatalf("expected 1 job in post-kill proc://, got %#v", postProc)
	}
	if gotErr, _ := postJobs[0]["errorText"].(string); gotErr != "[Command cancelled]\n" {
		t.Fatalf("fakeHost zero-delay read proc:// errorText = %q, want %q", gotErr, "[Command cancelled]\n")
	}
}

// TestRound5_PtyUnavailableWithWallTimeNoticeOrder verifies omp's exact notice
// ordering (`tools/bash.ts:1303-1320`): when a foreground command requests
// `pty: true` (unavailable fallback) AND exceeds the 1s wall-time notice
// threshold (`WallTimeNoticeMinMS`), `Wall time: ...` is emitted BEFORE
// `pty requested but unavailable in this environment; ran without a terminal`,
// separated from the body (`outputLines = [body, "", ...notices]`) by a blank line.
func TestRound5_PtyUnavailableWithWallTimeNoticeOrder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test")
	}
	// Use an injected deterministic clock (`Config.Now`) with omp's real 1000 ms
	// threshold (`WallTimeNoticeMinMS`) so both sides of the boundary (999 ms
	// below -> no notice; 1050 ms above -> `Wall time: 1.05 seconds` before the
	// pty notice) are tested deterministically regardless of machine speed.
	base := time.Unix(1_700_000_000, 0)
	var ticks []time.Time
	tickIdx := 0
	tool, dir := newTestTool(t, func(cfg *Config) {
		cfg.WallTimeNoticeMinMS = WallTimeNoticeMinMS
		cfg.Now = func() time.Time {
			tVal := ticks[tickIdx]
			if tickIdx < len(ticks)-1 {
				tickIdx++
			}
			return tVal
		}
	})

	// 1. Below threshold (999 ms < 1000 ms): NO Wall time notice, only pty notice.
	ticks = []time.Time{base, base.Add(999 * time.Millisecond)}
	tickIdx = 0
	resFast, err := runTool(t, tool, map[string]any{
		"command": "echo notty",
		"cwd":     dir,
		"pty":     true,
	})
	if err != nil {
		t.Fatalf("pty fast command: %v", err)
	}
	fastDet, _ := resFast.Details.(map[string]any)
	if emitted, _ := fastDet["wallTimeNoticeEmitted"].(bool); emitted {
		t.Fatalf("expected wallTimeNoticeEmitted=false at 999ms, details=%#v", fastDet)
	}
	wantFast := "notty\n\n\npty requested but unavailable in this environment; ran without a terminal"
	if resFast.Content != wantFast {
		t.Fatalf("fast pty content:\ngot:  %q\nwant: %q", resFast.Content, wantFast)
	}

	// 2. At/above threshold (1050 ms >= 1000 ms): `Wall time: 1.05 seconds` emitted
	// BEFORE `pty requested but unavailable...`, separated from body by a blank line.
	ticks = []time.Time{base, base.Add(1050 * time.Millisecond)}
	tickIdx = 0
	res, err := runTool(t, tool, map[string]any{
		"command": "echo notty",
		"cwd":     dir,
		"pty":     true,
	})
	if err != nil {
		t.Fatalf("pty slow command: %v", err)
	}
	details, _ := res.Details.(map[string]any)
	if emitted, _ := details["wallTimeNoticeEmitted"].(bool); !emitted {
		t.Fatalf("expected wallTimeNoticeEmitted=true at 1050ms, details=%#v", details)
	}
	wantSlow := "notty\n\n\nWall time: 1.05 seconds\npty requested but unavailable in this environment; ran without a terminal"
	if res.Content != wantSlow {
		t.Fatalf("slow pty notice layout/order:\ngot:  %q\nwant: %q", res.Content, wantSlow)
	}
}

// TestRound6_TimeoutAndCancelNeverLeakShellTerminationMessage runs 300 fresh
// shells under timeout and 300 fresh shells under cancel (`echo started; sleep 20`)
// across parallel workers and asserts that captured output NEVER contains any
// shell job-termination message (`Terminated`, `Killed`, `15`, `sleep 20`) caused
// by our own SIGTERM/SIGKILL (`exec/bash-executor.ts:1008-1037`).
func TestRound6_TimeoutAndCancelNeverLeakShellTerminationMessage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell signal test")
	}
	dir := t.TempDir()

	const (
		iterations = 300
		workers    = 12
	)
	t.Run("timeout_300x", func(t *testing.T) {
		indices := make(chan int, iterations)
		for i := 1; i <= iterations; i++ {
			indices <- i
		}
		close(indices)
		errCh := make(chan string, iterations)
		doneCh := make(chan struct{}, workers)
		for w := 0; w < workers; w++ {
			go func() {
				defer func() { doneCh <- struct{}{} }()
				for idx := range indices {
					res, err := executeCommand(context.Background(), ExecOptions{
						Command: "echo started; sleep 20",
						Cwd:     dir,
						Timeout: 60 * time.Millisecond,
					})
					if err != nil {
						errCh <- fmt.Sprintf("timeout iter %d: exec error: %v", idx, err)
						continue
					}
					if !res.TimedOut {
						errCh <- fmt.Sprintf("timeout iter %d: expected TimedOut=true, got %+v", idx, res)
						continue
					}
					for _, bad := range []string{"Terminated", "Killed", "sleep 20", "Hangup", "Abort"} {
						if strings.Contains(res.Output, bad) {
							errCh <- fmt.Sprintf("timeout iter %d leaked %q in output: %q", idx, bad, res.Output)
							break
						}
					}
				}
			}()
		}
		for w := 0; w < workers; w++ {
			<-doneCh
		}
		close(errCh)
		for msg := range errCh {
			t.Fatal(msg)
		}
	})

	t.Run("cancel_300x", func(t *testing.T) {
		indices := make(chan int, iterations)
		for i := 1; i <= iterations; i++ {
			indices <- i
		}
		close(indices)
		errCh := make(chan string, iterations)
		doneCh := make(chan struct{}, workers)
		for w := 0; w < workers; w++ {
			go func() {
				defer func() { doneCh <- struct{}{} }()
				for idx := range indices {
					ctx, cancel := context.WithCancel(context.Background())
					started := make(chan struct{}, 1)
					go func() {
						select {
						case <-started:
							cancel()
						case <-time.After(60 * time.Millisecond):
							cancel()
						}
					}()
					res, err := executeCommand(ctx, ExecOptions{
						Command: "echo started; sleep 20",
						Cwd:     dir,
						OnChunk: func(chunk string) {
							if strings.Contains(chunk, "started") {
								select {
								case started <- struct{}{}:
								default:
								}
							}
						},
					})
					cancel()
					if err != nil {
						errCh <- fmt.Sprintf("cancel iter %d: exec error: %v", idx, err)
						continue
					}
					if !res.Cancelled || res.TimedOut {
						errCh <- fmt.Sprintf("cancel iter %d: expected Cancelled=true TimedOut=false, got %+v", idx, res)
						continue
					}
					for _, bad := range []string{"Terminated", "Killed", "sleep 20", "Hangup", "Abort"} {
						if strings.Contains(res.Output, bad) {
							errCh <- fmt.Sprintf("cancel iter %d leaked %q in output: %q", idx, bad, res.Output)
							break
						}
					}
				}
			}()
		}
		for w := 0; w < workers; w++ {
			<-doneCh
		}
		close(errCh)
		for msg := range errCh {
			t.Fatal(msg)
		}
	})
}

// TestRound7_FallbackWidgetOnProcReadAndKill verifies Finding 10:
// bash background jobs carry omp's deterministic fallback `widget`
// (`state: "fallback"`, `title`, `stage`, `blocks`, `updatedAt`) on `proc://`
// listing, single-job `proc://<id>`, and `proc://<id>/kill` snapshots.
func TestRound7_FallbackWidgetOnProcReadAndKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test")
	}
	jobs.ResetForTests()
	urls.ResetForTests()
	t.Cleanup(func() {
		jobs.ResetForTests()
		urls.ResetForTests()
	})

	tool, dir := newTestTool(t, func(cfg *Config) {
		cfg.AutoBackgroundEnabled = false
	})
	if err := urls.Register(NewProcProtocolHandler(tool.runtime)); err != nil {
		t.Fatalf("register proc://: %v", err)
	}

	// 1. Completed bash job emits stage = last non-empty line and lines stat block.
	if _, err := runTool(t, tool, map[string]any{
		"command": "echo first_line; echo second_line",
		"cwd":     dir,
		"async":   true,
	}); err != nil {
		t.Fatalf("async 1: %v", err)
	}
	j1, ok := jobs.Get(procSessionID(urls.Context{}), "bg_1")
	if !ok {
		t.Fatalf("bg_1 missing")
	}
	select {
	case <-j1.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for bg_1")
	}

	listRes, err := urls.Resolve(urls.Context{}, "proc://")
	if err != nil {
		t.Fatalf("read proc://: %v", err)
	}
	listProc, _ := listRes.Details["proc"].(map[string]any)
	listJobs, _ := listProc["jobs"].([]map[string]any)
	if len(listJobs) != 1 {
		t.Fatalf("expected 1 job, got %#v", listProc)
	}
	w, _ := listJobs[0]["widget"].(map[string]any)
	if w == nil {
		t.Fatalf("expected fallback widget on proc:// job snapshot, got %#v", listJobs[0])
	}
	if w["state"] != "fallback" || w["title"] != "echo first_line; echo second_line" || w["stage"] != "second_line" {
		t.Fatalf("unexpected widget header: %#v", w)
	}
	blocks, _ := w["blocks"].([]map[string]any)
	if len(blocks) != 1 || blocks[0]["kind"] != "stat" {
		t.Fatalf("unexpected widget blocks: %#v", w["blocks"])
	}
	items, _ := blocks[0]["items"].([]map[string]any)
	if len(items) != 2 || items[0]["label"] != "lines" || items[0]["text"] != "2" || items[1]["label"] != "lines/s" {
		t.Fatalf("unexpected widget stat items: %#v", items)
	}

	// 2. Cancelled silent job (`sleep 30`) emits fallback widget with empty blocks on kill & read.
	if _, err := runTool(t, tool, map[string]any{
		"command": "sleep 30",
		"cwd":     dir,
		"async":   true,
	}); err != nil {
		t.Fatalf("async 2: %v", err)
	}
	killRes, err := urls.Write(urls.Context{}, "proc://bg_2/kill", "")
	if err != nil {
		t.Fatalf("kill bg_2: %v", err)
	}
	killProc, _ := killRes.Details["proc"].(map[string]any)
	killJobs, _ := killProc["jobs"].([]map[string]any)
	kw, _ := killJobs[0]["widget"].(map[string]any)
	if kw == nil || kw["state"] != "fallback" || kw["title"] != "sleep 30" {
		t.Fatalf("unexpected kill snapshot widget: %#v", kw)
	}
	if kblocks, _ := kw["blocks"].([]map[string]any); len(kblocks) != 0 {
		t.Fatalf("expected empty blocks for silent cancelled sleep, got %#v", kblocks)
	}
}

// TestRound8_ProcJobSnapshotPropagatesAgentResolvedModel verifies that proc://
// read and kill snapshots include resolvedModel, resolvedModelIdentity, and
// resolvedThinkingLevel from an agent job's JobDetailer.Details(), matching omp
// `async/job-control.ts:188-236` (`task-delayed-cancel#2`).
func TestRound8_ProcJobSnapshotPropagatesAgentResolvedModel(t *testing.T) {
	sessID := "sess-proc-agent-model"
	jobs.ResetForTests()
	t.Cleanup(jobs.ResetForTests)

	aj := &fakeAgentModelJob{
		id:      "SlowChild",
		label:   "SlowChild",
		started: time.Now(),
		done:    make(chan struct{}),
		status:  "running",
		details: map[string]any{
			"agentId":               "SlowChild",
			"resolvedModel":         "proof/proof-1",
			"resolvedModelIdentity": "proof/proof-1",
		},
	}
	jobs.Add(sessID, aj)

	snap := procJobSnapshotMap(sessID, aj, time.Now(), true)
	if snap["resolvedModel"] != "proof/proof-1" || snap["resolvedModelIdentity"] != "proof/proof-1" {
		t.Fatalf("procJobSnapshotMap missing resolvedModel*: %#v", snap)
	}
	if snap["agentUrlId"] != "SlowChild" {
		t.Fatalf("procJobSnapshotMap agentUrlId = %#v, want SlowChild", snap["agentUrlId"])
	}
}

type fakeAgentModelJob struct {
	id      string
	label   string
	started time.Time
	done    chan struct{}
	status  string
	details map[string]any
}

func (f *fakeAgentModelJob) ID() string              { return f.id }
func (f *fakeAgentModelJob) Kind() jobs.Kind         { return jobs.KindAgent }
func (f *fakeAgentModelJob) Label() string           { return f.label }
func (f *fakeAgentModelJob) Started() time.Time      { return f.started }
func (f *fakeAgentModelJob) Done() <-chan struct{}   { return f.done }
func (f *fakeAgentModelJob) Status() string          { return f.status }
func (f *fakeAgentModelJob) Output() string          { return "" }
func (f *fakeAgentModelJob) Result() string          { return "" }
func (f *fakeAgentModelJob) Cancel()                 { f.status = "cancelled" }
func (f *fakeAgentModelJob) Ended() time.Time        { return time.Time{} }
func (f *fakeAgentModelJob) ErrorText() string       { return "" }
func (f *fakeAgentModelJob) Command() string         { return "" }
func (f *fakeAgentModelJob) Intent() string          { return "" }
func (f *fakeAgentModelJob) Details() map[string]any { return f.details }
func (f *fakeAgentModelJob) SkipDelivery() bool      { return false }
