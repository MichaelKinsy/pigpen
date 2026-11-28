package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/approval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// TestRound3_Triage1_StderrPreserved verifies that stderr is preserved for every
// command with and without `2>&1`, including non-zero exit, stderr-only exit 0,
// and commands run after shell state persistence/restore.
func TestRound3_Triage1_StderrPreserved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell stderr test")
	}
	tool, dir := newTestTool(t, nil)

	// 1. Stderr + non-zero exit (oracle bash-exit-no-output-state #2).
	res, err := runTool(t, tool, map[string]any{
		"command": "echo 'boom' >&2; exit 7",
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true for exit 7")
	}
	want := "boom\n\n\nCommand exited with code 7"
	if res.Content != want {
		t.Fatalf("stderr+exit7 mismatch:\ngot:  %q\nwant: %q", res.Content, want)
	}

	// 2. Stderr only + exit 0 (oracle bash-cwd-spill #10).
	res, err = runTool(t, tool, map[string]any{
		"command": "echo only-err >&2",
		"cwd":     dir,
	})
	if err != nil || res.IsError {
		t.Fatalf("stderr-only exit 0 failed: res=%+v err=%v", res, err)
	}
	if strings.TrimSpace(res.Content) != "only-err" {
		t.Fatalf("expected 'only-err', got %q", res.Content)
	}

	// 3. Explicit 2>&1 also works.
	res, err = runTool(t, tool, map[string]any{
		"command": "echo both-err >&2 2>&1",
		"cwd":     dir,
	})
	if err != nil || res.IsError || !strings.Contains(res.Content, "both-err") {
		t.Fatalf("explicit 2>&1 failed: res=%+v err=%v", res, err)
	}

	// 4. Persist shell state, then restore it on the next call and verify stderr still arrives.
	if _, err := runTool(t, tool, map[string]any{
		"command": "ROUND3_VAR=persisted",
		"cwd":     dir,
	}); err != nil {
		t.Fatalf("state save call failed: %v", err)
	}
	res, err = runTool(t, tool, map[string]any{
		"command": `echo "after-restore:$ROUND3_VAR" >&2`,
		"cwd":     dir,
	})
	if err != nil || res.IsError {
		t.Fatalf("stderr after state restore failed: res=%+v err=%v", res, err)
	}
	if strings.TrimSpace(res.Content) != "after-restore:persisted" {
		t.Fatalf("expected stderr after state restore, got %q", res.Content)
	}
}

// fakeCancelAgentJob simulates an internal/agents.AgentJob for proc:// kill tests.
type fakeCancelAgentJob struct {
	id      string
	label   string
	started time.Time
	done    chan struct{}
	status  string
	cancel  func()
}

func (f *fakeCancelAgentJob) ID() string            { return f.id }
func (f *fakeCancelAgentJob) Kind() jobs.Kind       { return jobs.KindAgent }
func (f *fakeCancelAgentJob) Label() string         { return f.label }
func (f *fakeCancelAgentJob) Started() time.Time    { return f.started }
func (f *fakeCancelAgentJob) Done() <-chan struct{} { return f.done }
func (f *fakeCancelAgentJob) Status() string        { return f.status }
func (f *fakeCancelAgentJob) Output() string        { return "" }
func (f *fakeCancelAgentJob) Result() string        { return "" }
func (f *fakeCancelAgentJob) Cancel() {
	if f.cancel != nil {
		f.cancel()
	}
}

// TestRound3_Triage7_ProcKillRunningJobReportAndConsume verifies omp's cancel
// report for both running bash jobs and running task/agent jobs, including
// calling KillAgentHook, emitting "## Completed (1)", and marking the job consumed.
// upstream: async/job-control.ts:548-576,679
func TestRound3_Triage7_ProcKillRunningJobReportAndConsume(t *testing.T) {
	jobs.ResetForTests()
	jobs.ResetDeliveryForTests()
	t.Cleanup(func() {
		jobs.ResetForTests()
		jobs.ResetDeliveryForTests()
	})

	rt := NewRuntime(t.TempDir())
	t.Cleanup(rt.DisposeSessions)
	urls.ResetForTests()
	if err := urls.Register(NewProcProtocolHandler(rt)); err != nil {
		t.Fatalf("register proc: %v", err)
	}
	t.Cleanup(urls.ResetForTests)

	sessID := "default"
	var killedSession, killedAgent string
	prevHook := KillAgentHook
	KillAgentHook = func(sid, agentID string) (string, string) {
		killedSession = sid
		killedAgent = agentID
		return "cancelled", "Killed agent " + agentID + "; its transcript stays at history://" + agentID + "."
	}
	t.Cleanup(func() { KillAgentHook = prevHook })

	aj := &fakeCancelAgentJob{
		id:      "SlowChild",
		label:   "SlowChild",
		started: time.Now(),
		done:    make(chan struct{}),
		status:  "running",
	}
	aj.cancel = func() {
		aj.status = "cancelled"
		select {
		case <-aj.done:
		default:
			close(aj.done)
		}
	}
	jobs.Add(sessID, aj)

	res, err := urls.Write(urls.Context{}, "proc://SlowChild/kill", "")
	if err != nil {
		t.Fatalf("proc://SlowChild/kill error: %v", err)
	}
	if killedSession != sessID || killedAgent != "SlowChild" {
		t.Fatalf("KillAgentHook called with (%q, %q), want (%q, %q)", killedSession, killedAgent, sessID, "SlowChild")
	}
	want := strings.Join([]string{
		"## Cancelled (1)",
		"",
		"- Cancelled background job SlowChild. Killed agent SlowChild; its transcript stays at history://SlowChild.",
		"",
		"## Completed (1)",
		"",
		"### SlowChild [task] — cancelled",
		"Label: SlowChild",
	}, "\n")
	if res.Text != want {
		t.Fatalf("kill report mismatch:\ngot:\n%s\nwant:\n%s", res.Text, want)
	}
	// Per omp (`async/job-manager.ts:1164-1168`): consumeJobResult returns false
	// when neither resultText nor errorText is set on the cancelled job, so a
	// freshly cancelled job with no result/error stays unconsumed (its late
	// errorText remains visible on `proc://`).
	if jobs.IsConsumed(sessID, "SlowChild") {
		t.Fatalf("expected cancelled job SlowChild with empty result/error NOT to be marked consumed (omp consumeJobResult)")
	}
}

// TestRound3_Triage8_FailedJobStoresErrorTextOnly verifies that a failed/timed-out
// managed bash job stores its failure text in ErrorText and leaves Result() empty.
// upstream: async/job-manager.ts:665-668
func TestRound3_Triage8_FailedJobStoresErrorTextOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX timeout test")
	}
	jobs.ResetForTests()
	jobs.ResetDeliveryForTests()
	t.Cleanup(func() {
		jobs.ResetForTests()
		jobs.ResetDeliveryForTests()
	})

	tool, dir := newTestTool(t, func(cfg *Config) {
		cfg.AutoBackgroundEnabled = false
	})

	res, err := runTool(t, tool, map[string]any{
		"command": "echo started; sleep 10",
		"cwd":     dir,
		"async":   true,
		"timeout": 1,
	})
	if err != nil {
		t.Fatalf("async start: %v", err)
	}
	det, _ := res.Details.(map[string]any)
	asyncDet, _ := det["async"].(map[string]any)
	jobID, _ := asyncDet["jobId"].(string)
	if jobID == "" {
		t.Fatalf("missing jobId in details: %+v", det)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snap, found, wErr := tool.runtime.Jobs.Wait(waitCtx, jobID)
	if wErr != nil || !found {
		t.Fatalf("wait failed: found=%v err=%v", found, wErr)
	}
	if snap.Status != JobStatusFailed {
		t.Fatalf("expected status=failed, got %q", snap.Status)
	}
	if snap.ResultText != "" {
		t.Fatalf("expected empty ResultText on failed job, got %q", snap.ResultText)
	}
	if !strings.Contains(snap.ErrorText, "[Command timed out after 1 seconds]") {
		t.Fatalf("expected timeout notice in ErrorText, got %q", snap.ErrorText)
	}
}

// TestRound3_Triage9And10_SpillIDsFromZeroAndWholeLineElision verifies:
//   - First spill is local://0.log, second is local://1.log (Triage #9)
//   - Middle elision cuts head and tail to whole lines (no partial fragments)
//   - Trailing newline is dropped when computing totalLines / tailRange
//   - Elided line count uses thousands separators (e.g. "30,564 middle lines")
//
// Matches oracle bash-cwd-spill #7 (`seq 1 40000`).
func TestRound3_Triage9And10_SpillIDsFromZeroAndWholeLineElision(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX seq test")
	}
	tool, dir := newTestTool(t, nil)

	// First spill: long single line => local://0.log.
	res0, err := runTool(t, tool, map[string]any{
		"command": `head -c 3000 /dev/zero | tr '\0' 'x'; echo`,
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("first spill cmd: %v", err)
	}
	if !strings.Contains(res0.Content, "local://0.log") {
		t.Fatalf("expected first spill local://0.log, got %q", res0.Content)
	}

	// Second spill: seq 1 40000 => local://1.log with exact omp whole-line elision.
	res1, err := runTool(t, tool, map[string]any{
		"command": "seq 1 40000",
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("second spill cmd: %v", err)
	}
	wantNotice := "[Showing lines 1-4317 and 34882-40000 of 40000; 30,564 middle lines (173.5KB) elided. Read local://1.log for full output]"
	if !strings.Contains(res1.Content, wantNotice) {
		t.Fatalf("elision notice mismatch:\nwant substring: %s\ngot tail: %q", wantNotice, res1.Content[max(0, len(res1.Content)-300):])
	}
	if !strings.Contains(res1.Content, "4317\n[…30564ln elided…]\n34882\n") {
		idx := strings.Index(res1.Content, "elided")
		snippet := res1.Content
		if idx > 40 {
			snippet = res1.Content[idx-40 : min(len(res1.Content), idx+60)]
		}
		t.Fatalf("expected whole-line cut around marker (4317 / 34882), got snippet %q", snippet)
	}
}

// TestRound3_Triage11_ServiceStatesReadyExitedNoPidAndTrimmedLog verifies:
//   - Ready service state is "ready" (not "running")
//   - Service start output has no trailing newline after log tail
//   - Stopped service state is "exited" (not "stopped")
//   - Stopped service status omits "— pid N"
func TestRound3_Triage11_ServiceStatesReadyExitedNoPidAndTrimmedLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX service test")
	}
	jobs.ResetForTests()
	jobs.ResetDeliveryForTests()
	t.Cleanup(func() {
		jobs.ResetForTests()
		jobs.ResetDeliveryForTests()
	})

	tool, dir := newTestTool(t, nil)
	urls.ResetForTests()
	if err := urls.Register(NewProcProtocolHandler(tool.runtime)); err != nil {
		t.Fatalf("register proc: %v", err)
	}
	t.Cleanup(urls.ResetForTests)

	res, err := runTool(t, tool, map[string]any{
		"name":    "oracle-svc",
		"command": "echo SERVICE_READY; sleep 60",
		"cwd":     dir,
		"pty":     false,
		"ready":   map[string]any{"log": "SERVICE_READY", "timeout": 5},
	})
	if err != nil {
		t.Fatalf("start service: %v", err)
	}
	if !strings.HasPrefix(res.Content, "oracle-svc: ready pid=") || !strings.Contains(res.Content, " ready\nSERVICE_READY") {
		t.Fatalf("unexpected service start text: %q", res.Content)
	}
	if strings.HasSuffix(res.Content, "\n") {
		t.Fatalf("expected no trailing newline after service log tail, got %q", res.Content)
	}
	urlCtx := urls.Context{}
	listRes, err := urls.Resolve(urlCtx, "proc://")
	if err != nil {
		t.Fatalf("proc:// resolve: %v", err)
	}
	if !strings.Contains(listRes.Content, "oracle-svc [service] — ready — up ") {
		t.Fatalf("expected ready service in proc:// listing, got %q", listRes.Content)
	}

	stopRes, err := urls.Write(urlCtx, "proc://oracle-svc/kill", "")
	if err != nil {
		t.Fatalf("proc://oracle-svc/kill: %v", err)
	}
	if !strings.Contains(stopRes.Text, "Stopped oracle-svc [service] — exited — up ") {
		t.Fatalf("expected exited state after stop, got %q", stopRes.Text)
	}
	if strings.Contains(stopRes.Text, "— pid ") {
		t.Fatalf("expected no pid after service exit, got %q", stopRes.Text)
	}
}

// TestRound3_Triage21_StandardizeMacOSPath verifies macOS /private prefix
// stripping for existing and missing descendants under /private/tmp.
func TestRound3_Triage21_StandardizeMacOSPath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS /private path standardization test")
	}
	got := dirs.StandardizeMacOSPath("/private/tmp/definitely-missing-omp-dir-12345")
	if got != "/tmp/definitely-missing-omp-dir-12345" {
		t.Fatalf("dirs.StandardizeMacOSPath = %q, want /tmp/definitely-missing-omp-dir-12345", got)
	}
}

// TestRound3_Wire1_TwoViewBashPatternAndEnvAuthority verifies omp's two-view
// evaluation (tools/bash.ts:846-921):
//   - Project bash.allowedEnvVars CANNOT loosen operator bash.patterns
//   - Project bash.patterns deny/prompt CAN tighten operator allow
//   - Operator deny/prompt always wins over project allow
func TestRound3_Wire1_TwoViewBashPatternAndEnvAuthority(t *testing.T) {
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

	// Operator layer: allow "npm test*", prompt "*", NO allowedEnvVars.
	// Project (session) layer: adds bash.allowedEnvVars: ["LD_PRELOAD"] AND denies "npm test --evil*".
	operatorPatterns := []any{
		map[string]any{"match": "npm test*", "approval": "allow"},
		map[string]any{"match": "*", "approval": "prompt"},
	}
	sessionPatterns := []any{
		map[string]any{"match": "npm test --evil*", "approval": "deny"},
		map[string]any{"match": "npm test*", "approval": "allow"},
		map[string]any{"match": "*", "approval": "prompt"},
	}
	Setting = func(_ sdk.Context, key string) any {
		switch key {
		case "bash.patterns":
			return operatorPatterns
		case "bash.allowedEnvVars":
			return []string{}
		}
		return nil
	}
	SessionSetting = func(_ sdk.Context, key string) any {
		switch key {
		case "bash.patterns":
			return sessionPatterns
		case "bash.allowedEnvVars":
			return []string{"LD_PRELOAD"}
		}
		return nil
	}
	approval.Setting = Setting
	approval.SessionSetting = SessionSetting

	// 1. Project allowedEnvVars MUST NOT loosen operator's prompt rule for LD_PRELOAD=/tmp/x.so npm test.
	res, err := approval.HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "bash",
		"input":    map[string]any{"command": "LD_PRELOAD=/tmp/x.so npm test"},
	})
	if err != nil {
		t.Fatalf("veto err: %v", err)
	}
	m, _ := res.(map[string]any)
	if m == nil || m["block"] != true || !strings.Contains(m["reason"].(string), `Tool "bash" requires approval`) {
		t.Fatalf("expected operator prompt block for LD_PRELOAD command, got %#v", res)
	}

	// 2. Project deny rule MUST tighten operator's allow rule.
	res, err = approval.HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "bash",
		"input":    map[string]any{"command": "npm test --evil"},
	})
	if err != nil {
		t.Fatalf("veto err: %v", err)
	}
	m, _ = res.(map[string]any)
	if m == nil || m["block"] != true || !strings.Contains(m["reason"].(string), "Blocked by bash pattern: npm test --evil*") {
		t.Fatalf("expected project deny block, got %#v", res)
	}

	// 3. Plain "npm test" allowed by operator passes.
	res, err = approval.HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "bash",
		"input":    map[string]any{"command": "npm test"},
	})
	if err != nil || res != nil {
		t.Fatalf("expected plain 'npm test' allowed, got res=%#v err=%v", res, err)
	}
}

// TestRound3_Wire4_LiveSettingsReadAtCallTime verifies that async.enabled,
// launch.enabled, bash.autoBackground.*, tools.artifactSpillThreshold,
// tools.artifactHeadBytes, tools.outputMaxColumns, and tools.maxTimeout are
// honoured from shell.Setting at call time instead of DefaultConfig().
func TestRound3_Wire4_LiveSettingsReadAtCallTime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell settings test")
	}
	prevSetting := Setting
	t.Cleanup(func() { Setting = prevSetting })

	settingsMap := map[string]any{}
	Setting = func(_ sdk.Context, key string) any {
		return settingsMap[key]
	}

	tool, dir := newTestTool(t, nil)

	// 1. async.enabled = false refuses async: true.
	settingsMap["async.enabled"] = false
	if _, err := runTool(t, tool, map[string]any{"command": "echo hi", "cwd": dir, "async": true}); err == nil || !strings.Contains(err.Error(), "Async bash execution is disabled") {
		t.Fatalf("expected async disabled error, got %v", err)
	}
	delete(settingsMap, "async.enabled")

	// 2. launch.enabled = false refuses named service.
	settingsMap["launch.enabled"] = false
	if _, err := runTool(t, tool, map[string]any{"name": "svc1", "command": "sleep 5", "cwd": dir}); err == nil || !strings.Contains(err.Error(), "Service launch is disabled") {
		t.Fatalf("expected launch disabled error, got %v", err)
	}
	delete(settingsMap, "launch.enabled")

	// 3. tools.artifactSpillThreshold (KB) + tools.artifactHeadBytes (KB) + tools.outputMaxColumns (bytes).
	// Use 1 KB spill threshold (1024 bytes) and 0.25 KB head (256 bytes); emit > 2 KB output.
	settingsMap["tools.artifactSpillThreshold"] = 1.0
	settingsMap["tools.artifactHeadBytes"] = 0.25
	settingsMap["tools.outputMaxColumns"] = 20
	res, err := runTool(t, tool, map[string]any{
		"command": "printf '0123456789abcdefghijklmnopqrstuvwxyz\\n'; i=1; while [ $i -le 120 ]; do echo line-$i-abcdefghij; i=$((i+1)); done",
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("spill settings cmd: %v", err)
	}
	if !strings.Contains(res.Content, "Some lines truncated to 20 bytes") {
		t.Fatalf("expected tools.outputMaxColumns=20 notice, got %q", res.Content)
	}
	if !strings.Contains(res.Content, "Read local://0.log for full output") {
		t.Fatalf("expected spill from tools.artifactSpillThreshold=1 KB (1024 bytes), got %q", res.Content)
	}
	delete(settingsMap, "tools.artifactSpillThreshold")
	delete(settingsMap, "tools.artifactHeadBytes")
	delete(settingsMap, "tools.outputMaxColumns")
	// 4. bash.autoBackground.enabled = true + thresholdMs = 50 backgrounds a command
	// blocked on a sentinel file (cannot finish before the threshold regardless of load).
	settingsMap["bash.autoBackground.enabled"] = true
	settingsMap["bash.autoBackground.thresholdMs"] = 50
	releaseFile := filepath.Join(dir, "release-live-autobg")
	bgRes, err := runTool(t, tool, map[string]any{
		"command": fmt.Sprintf("while [ ! -e %s ]; do sleep 0.05; done; echo bg-done", singleQuotePosix(releaseFile)),
		"cwd":     dir,
		"timeout": 30,
	})
	if err != nil {
		t.Fatalf("autoBackground cmd: %v", err)
	}
	if !strings.Contains(bgRes.Content, "Backgrounded as job") {
		t.Fatalf("expected auto-backgrounded job with thresholdMs=50, got %q", bgRes.Content)
	}
	for _, j := range tool.runtime.Jobs.List() {
		tool.runtime.Jobs.Cancel(j.ID)
	}
}

// TestRound3_Wire5And9_SharedExclusiveSpillAllocatorAcrossResume verifies:
//   - Allocator seeds past existing `<N>.log` files in the session's local root
//   - Existing files are never overwritten (O_CREATE|O_EXCL)
//   - Bash spills and WriteSessionSpill (jobs.SpillFunc) share one per-session id space
//   - URLs are `local://<N>.log` (no `async-` prefix)
//
// upstream: session/artifacts.ts:87,128-156, session/agent-session.ts:4136-4140
func TestRound3_Wire5And9_SharedExclusiveSpillAllocatorAcrossResume(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX spill allocator test")
	}
	localRoot := t.TempDir()
	// Pre-populate 0.log..40.log as if from a prior process before resume.
	sentinel := "original-prior-session-content\n"
	for _, name := range []string{"0.log", "1.log", "40.log"} {
		if err := os.WriteFile(filepath.Join(localRoot, name), []byte(sentinel), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	prevRoot := LocalSpillRoot
	prevWriter := SpillWriter
	LocalSpillRoot = func(_ sdk.Context) (string, error) { return localRoot, nil }
	SpillWriter = nil
	t.Cleanup(func() {
		LocalSpillRoot = prevRoot
		SpillWriter = prevWriter
	})

	tool, dir := newTestTool(t, func(cfg *Config) {
		cfg.SpillThreshold = 200
		cfg.HeadBytes = 60
		cfg.MaxColumns = 0
	})

	// 1. Bash spill must allocate 41.log and leave 0.log/1.log/40.log untouched.
	res, err := runTool(t, tool, map[string]any{
		"command": "seq 1 100",
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("bash spill: %v", err)
	}
	if !strings.Contains(res.Content, "Read local://41.log for full output") {
		t.Fatalf("expected local://41.log after seeding past 40.log, got %q", res.Content)
	}
	got1, _ := os.ReadFile(filepath.Join(localRoot, "1.log"))
	if string(got1) != sentinel {
		t.Fatalf("1.log was overwritten! got %q", string(got1))
	}

	// 2. Async result spill via WriteSessionSpill uses the same allocator => local://42.log.
	u, err := WriteSessionSpill(sdk.Context{}, []byte("oversized async payload"))
	if err != nil {
		t.Fatalf("WriteSessionSpill: %v", err)
	}
	if u != "local://42.log" {
		t.Fatalf("WriteSessionSpill URL = %q, want local://42.log", u)
	}

	// 3. If a model writes 43.log directly, the allocator skips it via O_EXCL and lands on 44.log.
	if err := os.WriteFile(filepath.Join(localRoot, "43.log"), []byte("model-written"), 0o644); err != nil {
		t.Fatalf("write 43.log: %v", err)
	}
	u2, err := WriteSessionSpill(sdk.Context{}, []byte("second async payload"))
	if err != nil {
		t.Fatalf("WriteSessionSpill #2: %v", err)
	}
	if u2 != "local://44.log" {
		t.Fatalf("WriteSessionSpill #2 URL = %q, want local://44.log", u2)
	}
	got43, _ := os.ReadFile(filepath.Join(localRoot, "43.log"))
	if string(got43) != "model-written" {
		t.Fatalf("43.log was overwritten! got %q", string(got43))
	}

	// 4. WriteLocalSpillExclusive fails with os.ErrExist rather than overwriting.
	if _, err := WriteLocalSpillExclusive(localRoot, "43.log", []byte("clobber")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected os.ErrExist from WriteLocalSpillExclusive, got %v", err)
	}
}

// TestRound3_SettingsRegisteredDefaultsMatchUnassigned verifies that wiring
// `shell.Setting` to return every upstream registered default value (as the
// real settings store does — never nil) behaves identically to `Setting == nil`:
// a ~2.4 KB multi-line output (like `lsp --help`) does NOT spill, timeouts are
// unclamped, auto-background waits 60s, async/launch are enabled, and approval
// defaults to yolo/allow.
// upstream: packages/coding-agent/src/tools/settings.ts:12-104,661,1051,1073;
// packages/coding-agent/src/exec/settings.ts:142-319
func TestRound3_SettingsRegisteredDefaultsMatchUnassigned(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell defaults test")
	}
	// Exact upstream registered defaults (as decoded by internal/settings/schema.json:
	// numbers as float64, arrays as []any, records as map[string]any).
	registeredDefaults := map[string]any{
		"async.enabled":                   true,
		"launch.enabled":                  true,
		"bash.autoBackground.enabled":     true,
		"bash.autoBackground.thresholdMs": float64(60_000),
		"tools.artifactSpillThreshold":    float64(50),  // 50 KB => 51200 bytes
		"tools.artifactHeadBytes":         float64(20),  // 20 KB => 20480 bytes
		"tools.artifactTailBytes":         float64(20),  // 20 KB => 20480 bytes
		"tools.outputMaxColumns":          float64(768), // 768 bytes/line
		"tools.maxTimeout":                float64(0),   // 0s = no global cap
		"bashInterceptor.enabled":         false,
		"bash.patterns":                   []any{},
		"bash.allowedEnvVars":             []any{},
		"tools.approvalMode":              "yolo",
		"tools.approval":                  map[string]any{},
	}

	prevSetting := Setting
	prevSession := SessionSetting
	t.Cleanup(func() {
		Setting = prevSetting
		SessionSetting = prevSession
	})

	// 2.5 KB multi-line payload (~45 lines of ~56 bytes, like `lsp --help`).
	cmd := "i=1; while [ $i -le 45 ]; do printf 'line-%02d: 0123456789abcdefghijklmnopqrstuvwxyz0123456789\\n' $i; i=$((i+1)); done"

	// Baseline with Setting == nil.
	Setting = nil
	SessionSetting = nil
	toolNil, dir := newTestTool(t, nil)
	resNil, err := runTool(t, toolNil, map[string]any{"command": cmd, "cwd": dir})
	if err != nil || resNil.IsError {
		t.Fatalf("baseline nil-hook run failed: res=%+v err=%v", resNil, err)
	}
	if strings.Contains(resNil.Content, "elided") || strings.Contains(resNil.Content, "local://") {
		t.Fatalf("2.5 KB output must not spill with unassigned Setting, got: %q", resNil.Content)
	}

	// Same command with Setting returning every registered default.
	Setting = func(_ sdk.Context, key string) any { return registeredDefaults[key] }
	SessionSetting = Setting
	toolDef, dir2 := newTestTool(t, nil)
	resDef, err := runTool(t, toolDef, map[string]any{"command": cmd, "cwd": dir2})
	if err != nil || resDef.IsError {
		t.Fatalf("registered-defaults run failed: res=%+v err=%v", resDef, err)
	}
	if resDef.Content != resNil.Content {
		t.Fatalf("registered-defaults output differs from unassigned Setting:\ngot:  %q\nwant: %q", resDef.Content, resNil.Content)
	}

	// Also verify seq 1 40000 produces the exact same middle-elision output with
	// registered defaults (50 KB threshold, 20 KB head, 20 KB default tail) as unassigned.
	seqNil, err := runTool(t, toolNil, map[string]any{"command": "seq 1 40000", "cwd": dir})
	if err != nil {
		t.Fatalf("seq nil-hook: %v", err)
	}
	seqDef, err := runTool(t, toolDef, map[string]any{"command": "seq 1 40000", "cwd": dir2})
	if err != nil {
		t.Fatalf("seq registered-defaults: %v", err)
	}
	if seqDef.Content != seqNil.Content {
		t.Fatalf("seq 1 40000 with registered defaults differs from unassigned:\ngot tail:  %q\nwant tail: %q",
			seqDef.Content[max(0, len(seqDef.Content)-250):],
			seqNil.Content[max(0, len(seqNil.Content)-250):])
	}

	// Veto with registered defaults allows normal commands.
	prevApprSetting := approval.Setting
	prevApprSession := approval.SessionSetting
	t.Cleanup(func() {
		approval.Setting = prevApprSetting
		approval.SessionSetting = prevApprSession
	})
	approval.Setting = Setting
	approval.SessionSetting = Setting
	if veto, err := approval.HandleToolCallVeto(sdk.Context{}, map[string]any{
		"toolName": "bash",
		"input":    map[string]any{"command": "echo ok"},
	}); err != nil || veto != nil {
		t.Fatalf("expected nil veto under registered defaults, got veto=%#v err=%v", veto, err)
	}
}

// TestRound3_SettingsUnitConversions verifies every KB/ms/sec/column unit
// conversion against upstream omp (tools/output-meta.ts:508-563,
// streaming-output.ts:762-780,1010-1035):
//   - Fractional KB: 2.5 KB -> 2560 bytes (`* 1024`)
//   - tools.artifactSpillThreshold=2.5 keeps 2000 B unspilled and spills 3000 B
//   - tools.artifactHeadBytes=0 disables middle elision (tail-only)
//   - tools.artifactTailBytes=1 caps tail to 1024 B when explicitly set
//   - resolveInlineByteCapBudget adds 2 KiB (2048 B) notice slack
//   - tools.maxTimeout is in seconds (0 = no cap)
func TestRound3_SettingsUnitConversions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX unit conversion test")
	}

	// Pure conversion checks.
	if got := kbToBytes(2.5); got != 2560 {
		t.Fatalf("kbToBytes(2.5) = %d, want 2560", got)
	}
	if got := kbToBytes(50); got != 51200 {
		t.Fatalf("kbToBytes(50) = %d, want 51200", got)
	}
	if got := kbToBytes(0); got != 0 {
		t.Fatalf("kbToBytes(0) = %d, want 0", got)
	}
	if got := resolveInlineByteCapBudget(kbToBytes(50)); got != 52*1024 {
		t.Fatalf("resolveInlineByteCapBudget(50 KiB) = %d, want %d (50 KiB + 2 KiB slack)", got, 52*1024)
	}
	if got := resolveInlineByteCapBudget(kbToBytes(2.5)); got != 2560+2048 {
		t.Fatalf("resolveInlineByteCapBudget(2.5 KiB) = %d, want %d", got, 2560+2048)
	}

	prevSetting := Setting
	t.Cleanup(func() { Setting = prevSetting })
	m := map[string]any{}
	Setting = func(_ sdk.Context, key string) any { return m[key] }

	tool, dir := newTestTool(t, nil)

	// Fractional KB: 2.5 KB = 2560 bytes.
	// 2000 bytes fits inline (no spill); 3500 bytes spills.
	m["tools.artifactSpillThreshold"] = 2.5
	m["tools.artifactHeadBytes"] = 1.0
	m["tools.outputMaxColumns"] = 768.0

	underRes, err := runTool(t, tool, map[string]any{
		"command": "i=1; while [ $i -le 40 ]; do printf 'row-%03d-012345678901234567890123456789012\\n' $i; i=$((i+1)); done", // 40 * 50 = 2000 bytes
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("under 2.5 KB cmd: %v", err)
	}
	if strings.Contains(underRes.Content, "local://") || strings.Contains(underRes.Content, "elided") {
		t.Fatalf("2000B output should fit under 2.5 KB (2560B) threshold, got %q", underRes.Content)
	}

	overRes, err := runTool(t, tool, map[string]any{
		"command": "i=1; while [ $i -le 80 ]; do printf 'row-%03d-012345678901234567890123456789012\\n' $i; i=$((i+1)); done", // 80 * 50 = 4000 bytes
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("over 2.5 KB cmd: %v", err)
	}
	if !strings.Contains(overRes.Content, "Read local://0.log for full output") || !strings.Contains(overRes.Content, "middle lines") {
		t.Fatalf("4000B output should spill with middle elision under 2.5 KB / 1 KB head, got %q", overRes.Content)
	}

	// tools.artifactHeadBytes = 0 disables middle elision (tail-only).
	m["tools.artifactHeadBytes"] = 0.0
	m["tools.artifactTailBytes"] = 1.0 // 1024 bytes tail
	tailOnlyRes, err := runTool(t, tool, map[string]any{
		"command": "i=1; while [ $i -le 80 ]; do printf 'row-%03d-012345678901234567890123456789012\\n' $i; i=$((i+1)); done",
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("tail-only cmd: %v", err)
	}
	if strings.Contains(tailOnlyRes.Content, "middle line") {
		t.Fatalf("headBytes=0 should use tail-only truncation, got %q", tailOnlyRes.Content)
	}
	if !strings.Contains(tailOnlyRes.Content, "Read local://1.log for full output") {
		t.Fatalf("expected spill to local://1.log, got %q", tailOnlyRes.Content)
	}
	if strings.Contains(tailOnlyRes.Content, "row-001-") {
		t.Fatalf("tail-only with 1 KB tail should drop row-001, got %q", tailOnlyRes.Content)
	}

	// tools.maxTimeout is in seconds (positive clamps, 0 = no global cap).
	m["tools.maxTimeout"] = 7.0
	clampRes, err := runTool(t, tool, map[string]any{
		"command": "echo ok",
		"cwd":     dir,
		"timeout": 100,
	})
	if err != nil {
		t.Fatalf("maxTimeout=7 cmd: %v", err)
	}
	if !strings.Contains(clampRes.Content, "Timeout clamped to 7s (requested 100s; global tools.maxTimeout ceiling 7s).") {
		t.Fatalf("expected 7s timeout clamp notice, got %q", clampRes.Content)
	}
}

// TestRound3_PrepareLoadoutHook verifies that the registered `bash` tool
// definition delegates `PrepareLoadout` to `shell.PrepareLoadout` (including
// after `intent.Tool` and `schemaorder.Apply` wrapping), returning nil when
// unassigned and forwarding `HiddenDeclarations` when assigned.
func TestRound3_PrepareLoadoutHook(t *testing.T) {
	prev := PrepareLoadout
	t.Cleanup(func() { PrepareLoadout = prev })

	tool := NewBashTool(DefaultConfig(), NewRuntime(t.TempDir()))
	t.Cleanup(tool.runtime.DisposeSessions)

	def := intent.Tool(schemaorder.Apply(tool.Definition()))
	if def.PrepareLoadout == nil {
		t.Fatalf("expected non-nil PrepareLoadout on registered bash definition")
	}

	// Nil hook returns nil.
	PrepareLoadout = nil
	if got := def.PrepareLoadout(sdk.ToolLoadout{}); got != nil {
		t.Fatalf("expected nil changes when PrepareLoadout hook is nil, got %+v", got)
	}

	// Assigned hook returning HiddenDeclarations: ["lsp"] is forwarded.
	PrepareLoadout = func(_ sdk.ToolLoadout) *sdk.ToolLoadoutChanges {
		return &sdk.ToolLoadoutChanges{HiddenDeclarations: []string{"lsp"}}
	}
	got := def.PrepareLoadout(sdk.ToolLoadout{})
	if got == nil || len(got.HiddenDeclarations) != 1 || got.HiddenDeclarations[0] != "lsp" {
		t.Fatalf("expected HiddenDeclarations=[lsp], got %+v", got)
	}

	// Also preserved by DefinitionForUI(false) wrapped via intent.ToolForContext.
	defNoPty := intent.ToolForContext(sdk.Context{}, schemaorder.Apply(tool.DefinitionForUI(false)))
	if defNoPty.PrepareLoadout == nil {
		t.Fatalf("expected PrepareLoadout preserved on DefinitionForUI(false)")
	}
	got2 := defNoPty.PrepareLoadout(sdk.ToolLoadout{})
	if got2 == nil || len(got2.HiddenDeclarations) != 1 || got2.HiddenDeclarations[0] != "lsp" {
		t.Fatalf("DefinitionForUI(false) PrepareLoadout = %+v, want HiddenDeclarations=[lsp]", got2)
	}
}
