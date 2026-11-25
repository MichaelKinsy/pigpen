package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func newTestTool(t *testing.T, modify func(*Config)) (*BashTool, string) {
	t.Helper()
	dir := t.TempDir()
	// Resolve symlinks on macOS (/var -> /private/var) so pwd comparisons match.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	artDir := filepath.Join(t.TempDir(), "artifacts")
	cfg := DefaultConfig()
	cfg.AutoBackgroundEnabled = false
	// Disable the 1 s wall-time notice by default for tests that do not test
	// wall-time notices, so CPU contention on slow/shared runners never injects
	// an unexpected "Wall time: ..." footer into exact-output assertions.
	// Production DefaultConfig() keeps omp's 1000 ms (WALL_TIME_NOTICE_MIN_MS);
	// wall-time tests override WallTimeNoticeMinMS (and/or Config.Now).
	cfg.WallTimeNoticeMinMS = -1
	cfg.ArtifactDir = artDir
	if modify != nil {
		modify(&cfg)
	}
	rt := NewRuntime(artDir)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = rt.Jobs.Dispose(ctx)
		_ = rt.Services.Dispose(ctx)
		rt.DisposeSessions()
	})
	return NewBashTool(cfg, rt), dir
}

func runTool(t *testing.T, tool *BashTool, params map[string]any) (sdk.ToolResult, error) {
	t.Helper()
	res, err := tool.Execute(sdk.Context{}, params)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	tr, ok := res.(sdk.ToolResult)
	if !ok {
		t.Fatalf("expected sdk.ToolResult, got %T", res)
	}
	return tr, nil
}

func TestRegisterSmoke(t *testing.T) {
	e := sdk.New("test-oh-my-pig")
	Register(e)
}

// upstream: bash-failure-result.test.ts: "returns a success result with no exit-code detail for a zero exit"
func TestBashZeroExit(t *testing.T) {
	tool, dir := newTestTool(t, nil)
	res, err := runTool(t, tool, map[string]any{
		"command": "printf hi",
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected IsError=false, got true")
	}
	details, _ := res.Details.(map[string]any)
	if _, hasExit := details["exitCode"]; hasExit {
		t.Fatalf("expected no exitCode detail for zero exit, got %v", details["exitCode"])
	}
	if !strings.Contains(res.Content, "hi") {
		t.Fatalf("expected output to contain hi, got %q", res.Content)
	}
	if strings.Contains(res.Content, "Command exited with code") {
		t.Fatalf("expected no exit code notice, got %q", res.Content)
	}
}

// upstream: bash-failure-result.test.ts: "resolves with an error result carrying execution details instead of throwing"
func TestBashNonZeroExit(t *testing.T) {
	tool, dir := newTestTool(t, nil)
	res, err := runTool(t, tool, map[string]any{
		"command": "exit 3",
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("expected non-throwing error result, got error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true")
	}
	details, _ := res.Details.(map[string]any)
	if details["exitCode"] != 3 {
		t.Fatalf("expected exitCode=3, got %v", details["exitCode"])
	}
	if details["timeoutSeconds"] != 300 {
		t.Fatalf("expected timeoutSeconds=300, got %v", details["timeoutSeconds"])
	}
	if _, ok := details["wallTimeMs"].(float64); !ok {
		t.Fatalf("expected numeric wallTimeMs, got %T", details["wallTimeMs"])
	}
	if !strings.Contains(res.Content, "Command exited with code 3") {
		t.Fatalf("expected exit code notice, got %q", res.Content)
	}
}

// upstream: bash-failure-result.test.ts: "preserves final-stage output when a pipeline ends in head or tail"
func TestBashPipelineHeadTail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX seq/head/tail pipeline test")
	}
	tool, dir := newTestTool(t, nil)
	cases := []struct {
		cmd  string
		want string
	}{
		{"seq 1 5 | head -n2", "1\n2"},
		{"seq 1 5 | tail -n2", "4\n5"},
	}
	for _, tc := range cases {
		res, err := runTool(t, tool, map[string]any{"command": tc.cmd, "cwd": dir})
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.cmd, err)
		}
		if res.IsError {
			t.Fatalf("%s: expected success, got error: %q", tc.cmd, res.Content)
		}
		got := strings.TrimSpace(res.Content)
		if got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.cmd, got, tc.want)
		}
	}
}

// upstream: bash-failure-result.test.ts: "returns a warning-state timeout result with one timeout notice"
// Also verifies that timeout kills child processes of the command and leaves no process behind (checked by PID).
func TestBashTimeoutKillsChildProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX child PID verification")
	}
	// Disable PersistentState so the baseline state-wrapper preamble does not
	// eat into the 1 s timeout on heavily loaded machines before the child pid
	// file is written, and use a 2 s timeout so the command always reaches `wait`.
	tool, dir := newTestTool(t, func(cfg *Config) {
		cfg.PersistentState = false
	})
	pidFile := filepath.Join(dir, "child.pid")
	cmd := fmt.Sprintf("sleep 60 & echo $! > %s; wait", singleQuotePosix(pidFile))

	res, err := runTool(t, tool, map[string]any{
		"command": cmd,
		"cwd":     dir,
		"timeout": 2,
	})
	if err != nil {
		t.Fatalf("expected non-throwing timeout result, got error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true on timeout")
	}
	details, _ := res.Details.(map[string]any)
	if details["timedOut"] != true {
		t.Fatalf("expected timedOut=true, got %v", details["timedOut"])
	}
	if count := strings.Count(res.Content, "[Command timed out after 2 seconds]"); count != 1 {
		t.Fatalf("expected exactly 1 timeout notice, got %d in %q", count, res.Content)
	}
	// Verify child PID was recorded and is no longer alive.
	rawPID, rErr := os.ReadFile(pidFile)
	if rErr != nil {
		t.Fatalf("child pid file not written: %v", rErr)
	}
	childPID, pErr := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if pErr != nil || childPID <= 0 {
		t.Fatalf("invalid child pid %q: %v", string(rawPID), pErr)
	}
	// Give the OS a brief moment to reap the killed child.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && isProcessAlive(childPID) {
		time.Sleep(20 * time.Millisecond)
	}
	if isProcessAlive(childPID) {
		forceKillPID(childPID)
		t.Fatalf("child process %d still alive after timeout", childPID)
	}
}

// upstream: bash-failure-result.test.ts: "preserves the executor cancellation notice without classifying it as a timeout"
func TestExecuteCommandCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sleep cancellation test")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var pidFn func() []int
	var mu sync.Mutex

	done := make(chan ExecResult, 1)
	go func() {
		res, _ := executeCommand(ctx, ExecOptions{
			Command: "printf ready; sleep 30",
			Cwd:     dir,
			OnStart: func(pids func() []int) {
				mu.Lock()
				pidFn = pids
				mu.Unlock()
			},
			OnPreview: func(text string) {
				if strings.Contains(text, "ready") {
					select {
					case <-started:
					default:
						close(started)
					}
				}
			},
		})
		done <- res
	}()

	select {
	case <-started:
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatalf("command did not start in time")
	}

	mu.Lock()
	var startedPID int
	if pidFn != nil {
		if pids := pidFn(); len(pids) > 0 {
			startedPID = pids[0]
		}
	}
	mu.Unlock()

	cancel()
	res := <-done
	if !res.Cancelled || res.TimedOut {
		t.Fatalf("expected Cancelled=true TimedOut=false, got Cancelled=%v TimedOut=%v", res.Cancelled, res.TimedOut)
	}
	_, err := buildCompletedResult(res, nil, DefaultTimeoutSec, nil, 10)
	if err == nil {
		t.Fatalf("expected error for cancelled command")
	}
	if strings.Count(err.Error(), "[Command cancelled]") != 1 {
		t.Fatalf("expected single [Command cancelled] notice, got %q", err.Error())
	}
	if strings.Contains(err.Error(), "Command aborted") {
		t.Fatalf("expected no duplicate Command aborted notice, got %q", err.Error())
	}
	if startedPID > 0 {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) && isProcessAlive(startedPID) {
			time.Sleep(20 * time.Millisecond)
		}
		if isProcessAlive(startedPID) {
			t.Fatalf("cancelled process %d still alive", startedPID)
		}
	}
}

// upstream: bash-session-state.test.ts: variable, function, and alias persist across calls while cwd resets to session dir
func TestBashPersistentShellStateAndCwdReset(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX bash state persistence test")
	}
	tool, dir := newTestTool(t, nil)
	sub := filepath.Join(dir, "subdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// Explicit cwd parameter.
	res, err := runTool(t, tool, map[string]any{"command": "pwd", "cwd": sub})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(res.Content) != sub {
		t.Fatalf("got pwd %q, want %q", strings.TrimSpace(res.Content), sub)
	}

	// Leading `cd <path> && ...` extraction.
	res, err = runTool(t, tool, map[string]any{"command": fmt.Sprintf("cd %s && pwd", singleQuotePosix(sub))})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(res.Content) != sub {
		t.Fatalf("leading cd got %q, want %q", strings.TrimSpace(res.Content), sub)
	}

	// `cd` in one call does NOT persist to the next call (cwd resets to session dir).
	sessionDir, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatal(wdErr)
	}
	if resolved, rErr := filepath.EvalSymlinks(sessionDir); rErr == nil {
		sessionDir = resolved
	}
	res, err = runTool(t, tool, map[string]any{"command": fmt.Sprintf("cd %s", singleQuotePosix(sub))})
	if err != nil {
		t.Fatalf("cd failed: %v", err)
	}
	if res.Content != "(no output, exit code 0)" {
		t.Fatalf("cd alone got %q, want %q", res.Content, "(no output, exit code 0)")
	}
	res, err = runTool(t, tool, map[string]any{"command": "pwd"})
	if err != nil {
		t.Fatalf("pwd failed: %v", err)
	}
	gotPwd := strings.TrimSpace(res.Content)
	if resolved, rErr := filepath.EvalSymlinks(gotPwd); rErr == nil {
		gotPwd = resolved
	}
	if gotPwd != sessionDir {
		t.Fatalf("cwd persisted across calls: got %q, want session dir %q", gotPwd, sessionDir)
	}

	// Variable (exported & unexported), function, and alias persist across calls.
	res, err = runTool(t, tool, map[string]any{
		"command": `export OMP_ORACLE_VAR=1; OMP_LOCAL_VAR=kept; omp_fn() { printf 'fn:%s' "$1"; }; alias omp_greet='printf alias-ok'`,
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("state setup failed: %v", err)
	}
	if res.Content != "(no output, exit code 0)" {
		t.Fatalf("state setup got %q, want %q", res.Content, "(no output, exit code 0)")
	}

	res, err = runTool(t, tool, map[string]any{
		"command": `printf 'exp=%s loc=%s ' "${OMP_ORACLE_VAR:-unset}" "${OMP_LOCAL_VAR:-unset}"; omp_fn hi; printf ' '; omp_greet`,
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("state read failed: %v", err)
	}
	want := "exp=1 loc=kept fn:hi alias-ok"
	if res.Content != want {
		t.Fatalf("persistent state mismatch: got %q, want %q", res.Content, want)
	}

	// A killed/timed-out command must NOT corrupt previously saved session state.
	res, err = runTool(t, tool, map[string]any{
		"command": "export OMP_ORACLE_VAR=corrupted; sleep 5",
		"cwd":     dir,
		"timeout": 1,
	})
	if err != nil {
		t.Fatalf("timeout call error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected timeout IsError=true")
	}
	res, err = runTool(t, tool, map[string]any{
		"command": `printf '%s' "${OMP_ORACLE_VAR:-unset}"`,
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("post-timeout state read failed: %v", err)
	}
	if res.Content != "1" {
		t.Fatalf("timed-out command corrupted session state: got %q, want %q", res.Content, "1")
	}
}

// Exact-text test for defect 2: no output with exit 0 vs non-zero exit.
func TestBashEmptyOutputExactText(t *testing.T) {
	tool, dir := newTestTool(t, nil)

	res, err := runTool(t, tool, map[string]any{"command": "true", "cwd": dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected IsError=false")
	}
	if res.Content != "(no output, exit code 0)" {
		t.Fatalf("empty exit 0: got %q, want %q", res.Content, "(no output, exit code 0)")
	}

	res, err = runTool(t, tool, map[string]any{"command": "exit 3", "cwd": dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true")
	}
	wantErr := "(no output)\n\nCommand exited with code 3"
	if res.Content != wantErr {
		t.Fatalf("empty exit 3: got %q, want %q", res.Content, wantErr)
	}
}

// Exact-text test for defect 3: `sleep 5; echo late` with `timeout: 2` under
// default auto-backgrounding returns non-error background notice at ~1s, and
// delivers `[Command timed out after 2 seconds]\nWall time: X.XX seconds` via
// omp's `<system-notice>` follow-up when the 2s deadline fires.
func TestBashTimeoutAutoBackgroundAndFollowUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sleep auto-background timeout test")
	}
	tool, dir := newTestTool(t, func(cfg *Config) {
		cfg.AutoBackgroundEnabled = true
		cfg.AutoBackgroundThreshold = DefaultAutoBackgroundThreshold
		// Production threshold (1000 ms): the 2 s timeout guarantees wallTime >= 2000 ms
		// on every machine (load only increases wallTime, never shrinks below the 2 s timeout).
		cfg.WallTimeNoticeMinMS = WallTimeNoticeMinMS
	})
	type delivery struct {
		jobID string
		text  string
		msg   string
		snap  JobSnapshot
	}
	deliveredCh := make(chan delivery, 1)
	tool.runtime.Jobs.SetDeliverySink(func(jobID, text string, snap JobSnapshot) {
		deliveredCh <- delivery{
			jobID: jobID,
			text:  text,
			msg:   FormatAsyncResultMessage(jobID, text),
			snap:  snap,
		}
	})

	pidFile := filepath.Join(dir, "sleep.pid")
	cmd := fmt.Sprintf("sleep 5 & echo $! > %s; wait; echo late", singleQuotePosix(pidFile))

	res, err := runTool(t, tool, map[string]any{
		"command": cmd,
		"cwd":     dir,
		"timeout": 2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected non-error backgrounded result, got IsError=true: %q", res.Content)
	}
	details, _ := res.Details.(map[string]any)
	asyncInfo, _ := details["async"].(map[string]any)
	jobID, _ := asyncInfo["jobId"].(string)
	if !strings.HasPrefix(jobID, "bg_") {
		t.Fatalf("expected bg_N jobId, got %v", details)
	}
	wantStart := fmt.Sprintf(
		"Backgrounded as job %s (killed once it has run 2s in total; `timeout: 0` disables the deadline); its output is injected into the conversation as a follow-up the moment it finishes. Do NOT poll for it (no `sleep`, `ps`, `pgrep`, `top`, `pidwait`, log tailing): every poll is a wasted turn. Do other work, or end your reply and wait to be woken.",
		jobID,
	)
	if res.Content != wantStart {
		t.Fatalf("background start text mismatch:\ngot:  %q\nwant: %q", res.Content, wantStart)
	}

	select {
	case d := <-deliveredCh:
		if d.jobID != jobID {
			t.Fatalf("delivery jobId=%q, want %q", d.jobID, jobID)
		}
		// OutputSink.Dump prepends "[Command timed out after 2 seconds]\n", and
		// buildCompletedResult joins notices with "" (matching tools/bash.ts:1317).
		if !strings.HasPrefix(d.text, "[Command timed out after 2 seconds]\n\n\nWall time: ") &&
			!strings.HasPrefix(d.text, "[Command timed out after 2 seconds]\n\nWall time: ") {
			t.Fatalf("unexpected timeout delivery text: %q", d.text)
		}
		if !strings.HasPrefix(d.msg, fmt.Sprintf("<system-notice>\nBackground job %s has completed.\nResume your work using the result below.\n[Command timed out after 2 seconds]", jobID)) ||
			!strings.HasSuffix(d.msg, "\n</system-notice>") {
			t.Fatalf("unexpected follow-up message format: %q", d.msg)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for backgrounded timeout delivery")
	}

	// Verify child sleep process was killed.
	if rawPID, rErr := os.ReadFile(pidFile); rErr == nil {
		if pid, pErr := strconv.Atoi(strings.TrimSpace(string(rawPID))); pErr == nil && pid > 0 {
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) && isProcessAlive(pid) {
				time.Sleep(20 * time.Millisecond)
			}
			if isProcessAlive(pid) {
				forceKillPID(pid)
				t.Fatalf("child process %d still alive after background timeout", pid)
			}
		}
	}
}

func TestBashCwdErrors(t *testing.T) {
	tool, dir := newTestTool(t, nil)
	missing := filepath.Join(dir, "does-not-exist")
	_, err := runTool(t, tool, map[string]any{"command": "echo hi", "cwd": missing})
	if err == nil || !strings.Contains(err.Error(), "Working directory does not exist") {
		t.Fatalf("expected missing cwd error, got %v", err)
	}

	filePath := filepath.Join(dir, "plain.txt")
	if wErr := os.WriteFile(filePath, []byte("x"), 0o644); wErr != nil {
		t.Fatal(wErr)
	}
	_, err = runTool(t, tool, map[string]any{"command": "echo hi", "cwd": filePath})
	if err == nil || !strings.Contains(err.Error(), "Working directory is not a directory") {
		t.Fatalf("expected non-dir cwd error, got %v", err)
	}
}

func TestBashOutputTruncationAndArtifactSpill(t *testing.T) {
	tool, dir := newTestTool(t, func(cfg *Config) {
		cfg.SpillThreshold = 2048
		cfg.HeadBytes = 512
		cfg.MaxColumns = 0
	})
	// Generate ~200 lines (~8 KB), well over the 2 KB spill threshold.
	res, err := runTool(t, tool, map[string]any{
		"command": "i=1; while [ $i -le 200 ]; do printf 'line-%03d-abcdefghijklmnopqrstuvwxyz0123456789\\n' $i; i=$((i+1)); done",
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %q", res.Content)
	}
	if !strings.Contains(res.Content, "line-001-") {
		t.Fatalf("expected head line-001 in truncated output")
	}
	if !strings.Contains(res.Content, "line-200-") {
		t.Fatalf("expected tail line-200 in truncated output")
	}
	if !strings.Contains(res.Content, "elided") {
		t.Fatalf("expected middle elision marker in output, got %q", res.Content)
	}
	if !strings.Contains(res.Content, "Read local://0.log for full output") {
		t.Fatalf("expected local://0.log notice in output, got %q", res.Content)
	}
	artPath, ok := tool.runtime.Artifacts.Path("0")
	if !ok {
		t.Fatalf("expected artifact 0 to be registered")
	}
	fullBytes, rErr := os.ReadFile(artPath)
	if rErr != nil {
		t.Fatalf("failed reading spilled artifact: %v", rErr)
	}
	if !strings.Contains(string(fullBytes), "line-100-") {
		t.Fatalf("expected spilled artifact to contain full middle content (line-100)")
	}
}

// upstream: bash-job-lifecycle.test.ts: async job start, completion delivery, and collection
func TestBashAsyncJobFinishAndCollect(t *testing.T) {
	tool, dir := newTestTool(t, nil)

	deliveredCh := make(chan struct {
		id   string
		text string
		snap JobSnapshot
	}, 1)
	tool.runtime.Jobs.SetDeliverySink(func(jobID string, text string, snap JobSnapshot) {
		deliveredCh <- struct {
			id   string
			text string
			snap JobSnapshot
		}{jobID, text, snap}
	})

	pidFile := filepath.Join(dir, "async.pid")
	cmd := fmt.Sprintf("echo $$ > %s; sleep 0.1; echo async-done", singleQuotePosix(pidFile))
	res, err := runTool(t, tool, map[string]any{
		"command": cmd,
		"cwd":     dir,
		"async":   true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected non-error start result")
	}
	details, _ := res.Details.(map[string]any)
	asyncInfo, _ := details["async"].(map[string]any)
	jobID, _ := asyncInfo["jobId"].(string)
	if !strings.HasPrefix(jobID, "bg_") {
		t.Fatalf("expected bg_N jobId, got %q", jobID)
	}
	if !strings.Contains(res.Content, "Backgrounded as job "+jobID) {
		t.Fatalf("expected background notice, got %q", res.Content)
	}

	// Collect via Wait and verify delivery callback fired.
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snap, found, wErr := tool.runtime.Jobs.Wait(waitCtx, jobID)
	if wErr != nil || !found {
		t.Fatalf("Wait failed: found=%v err=%v", found, wErr)
	}
	if snap.Status != JobStatusCompleted {
		t.Fatalf("expected completed status, got %s (err=%q)", snap.Status, snap.ErrorText)
	}
	if !strings.Contains(snap.ResultText, "async-done") {
		t.Fatalf("expected async-done in result, got %q", snap.ResultText)
	}

	select {
	case d := <-deliveredCh:
		if d.id != jobID || !strings.Contains(d.text, "async-done") {
			t.Fatalf("unexpected delivery: id=%q text=%q", d.id, d.text)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for async delivery callback")
	}

	// Ensure process is gone.
	if rawPID, rErr := os.ReadFile(pidFile); rErr == nil {
		if pid, pErr := strconv.Atoi(strings.TrimSpace(string(rawPID))); pErr == nil && pid > 0 {
			if isProcessAlive(pid) {
				t.Fatalf("async process %d still alive after completion", pid)
			}
		}
	}
}

// upstream: bash-job-lifecycle.test.ts: "allows a cancelled bash job to run its TERM cleanup"
func TestBashAsyncJobCancelRunsTermCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX SIGTERM trap test")
	}
	tool, dir := newTestTool(t, nil)
	script := filepath.Join(dir, "cleanup.sh")
	cleanedFile := filepath.Join(dir, "cleaned")
	readyFile := filepath.Join(dir, "ready")
	body := fmt.Sprintf(
		"trap 'printf cleaned > %s; exit 0' TERM\nprintf ready > %s\nwhile :; do sleep 0.05; done\n",
		singleQuotePosix(cleanedFile),
		singleQuotePosix(readyFile),
	)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := runTool(t, tool, map[string]any{
		"command": "sh " + singleQuotePosix(script),
		"cwd":     dir,
		"async":   true,
		"timeout": 0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	details, _ := res.Details.(map[string]any)
	asyncInfo, _ := details["async"].(map[string]any)
	jobID, _ := asyncInfo["jobId"].(string)

	// Wait for script to install trap.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(readyFile); err == nil {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if _, err := os.Stat(readyFile); err != nil {
		t.Fatalf("script never reached ready state")
	}

	if !tool.runtime.Jobs.Cancel(jobID) {
		t.Fatalf("expected Cancel(%s) to return true", jobID)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	snap, _, wErr := tool.runtime.Jobs.Wait(waitCtx, jobID)
	if wErr != nil {
		t.Fatalf("Wait failed: %v", wErr)
	}
	if snap.Status != JobStatusCancelled {
		t.Fatalf("expected cancelled status, got %s", snap.Status)
	}
	// Abort returns promptly after 100ms while graceful SIGTERM->SIGKILL reap
	// finishes asynchronously over termCleanupGrace (2s); poll cleanedFile for
	// up to 5s so a slow-scheduled TERM trap handler under heavy CPU load still
	// completes and is observed deterministically.
	var data []byte
	var rErr error
	cleanDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(cleanDeadline) {
		data, rErr = os.ReadFile(cleanedFile)
		if rErr == nil && string(data) == "cleaned" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rErr != nil || string(data) != "cleaned" {
		t.Fatalf("expected TERM cleanup file 'cleaned', got %q (err=%v)", string(data), rErr)
	}
}

func TestBashAutoBackgroundPromotion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sleep auto-background test")
	}
	tool, dir := newTestTool(t, func(cfg *Config) {
		cfg.AutoBackgroundEnabled = true
		cfg.AutoBackgroundThreshold = 100 * time.Millisecond
	})

	delivered := make(chan string, 1)
	tool.runtime.Jobs.SetDeliverySink(func(jobID, text string, _ JobSnapshot) {
		delivered <- jobID + ":" + text
	})

	// Block on a sentinel file (not a short sleep) so the command cannot finish
	// before the 100 ms auto-background threshold; release it only after
	// auto-backgrounding is observed.
	releaseFile := filepath.Join(dir, "release-autobg")
	res, err := runTool(t, tool, map[string]any{
		"command": fmt.Sprintf("while [ ! -e %s ]; do sleep 0.05; done; echo bg-finished", singleQuotePosix(releaseFile)),
		"cwd":     dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	details, _ := res.Details.(map[string]any)
	asyncInfo, _ := details["async"].(map[string]any)
	jobID, _ := asyncInfo["jobId"].(string)
	if !strings.HasPrefix(jobID, "bg_") {
		t.Fatalf("expected auto-backgrounded job id, got %v", details)
	}
	if err := os.WriteFile(releaseFile, []byte("go\n"), 0o600); err != nil {
		t.Fatalf("create release file: %v", err)
	}

	select {
	case got := <-delivered:
		if !strings.Contains(got, "bg-finished") {
			t.Fatalf("expected bg-finished in delivery, got %q", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for auto-backgrounded job delivery")
	}
}

func TestBashServiceModeAndStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX service test")
	}
	tool, dir := newTestTool(t, nil)

	res, err := runTool(t, tool, map[string]any{
		"name":    "demo-svc",
		"command": "echo 'SERVER READY'; sleep 30",
		"cwd":     dir,
		"ready": map[string]any{
			"log":     "SERVER READY",
			"timeout": 20,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Content, "demo-svc: ready") || !strings.Contains(res.Content, "ready") {
		t.Fatalf("unexpected service output: %q", res.Content)
	}
	snap, ok := tool.runtime.Services.Get("demo-svc")
	if !ok || snap.PID <= 0 {
		t.Fatalf("expected registered service with PID, got %+v ok=%v", snap, ok)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stopped, sErr := tool.runtime.Services.Stop(stopCtx, "demo-svc")
	if sErr != nil || !stopped {
		t.Fatalf("Stop failed: stopped=%v err=%v", stopped, sErr)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && isProcessAlive(snap.PID) {
		time.Sleep(20 * time.Millisecond)
	}
	if isProcessAlive(snap.PID) {
		t.Fatalf("service process %d still alive after Stop", snap.PID)
	}
}

func TestBashModeValidationAndTimeoutClamping(t *testing.T) {
	tool, dir := newTestTool(t, nil)

	// Service mode rejects async/timeout when no ready condition is given (v19.1.1).
	if _, err := runTool(t, tool, map[string]any{
		"name":    "svc",
		"command": "echo hi",
		"cwd":     dir,
		"async":   true,
	}); err == nil || !strings.Contains(err.Error(), "`name` starts a long-lived service") {
		t.Fatalf("expected service+async error, got %v", err)
	}
	if _, err := runTool(t, tool, map[string]any{
		"name":    "svc",
		"command": "echo hi",
		"cwd":     dir,
		"timeout": 10,
	}); err == nil || !strings.Contains(err.Error(), "`name` starts a long-lived service") {
		t.Fatalf("expected service+timeout error, got %v", err)
	}
	// Ready without name emits notice and still runs.
	res, err := runTool(t, tool, map[string]any{
		"command": "printf ok",
		"cwd":     dir,
		"ready":   map[string]any{"log": "ready"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Content, "Ignored ready: service-only, and no service name was given.") {
		t.Fatalf("expected ignored ready notice, got %q", res.Content)
	}

	// Timeout above max is clamped with notice.
	res, err = runTool(t, tool, map[string]any{
		"command": "printf ok",
		"cwd":     dir,
		"timeout": 9999,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Content, "Timeout clamped to 3600s (requested 9999s; allowed range 1-3600s).") {
		t.Fatalf("expected timeout clamp notice, got %q", res.Content)
	}
	res, err = runTool(t, tool, map[string]any{
		"command": "printf ok",
		"cwd":     dir,
		"timeout": 0.4,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Content, "Timeout clamped to 1s (requested 0.4s; allowed range 1-3600s).") {
		t.Fatalf("expected fractional timeout clamp notice, got %q", res.Content)
	}

	// Finite command with pty: true always emits omp's fallback notice and runs without a terminal.
	res, err = runTool(t, tool, map[string]any{
		"command": "if [ -t 1 ]; then echo IS_TTY; else echo NOT_TTY; fi",
		"cwd":     dir,
		"pty":     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Content, "pty requested but unavailable in this environment; ran without a terminal") || !strings.Contains(res.Content, "NOT_TTY") {
		t.Fatalf("expected pty fallback notice without terminal, got %q", res.Content)
	}
}

// upstream: tools/bash-interceptor.test.ts: "BashTool interception" & "compound command interception"
func TestBashInterceptor(t *testing.T) {
	rules := []InterceptorRule{
		{
			Pattern: `^\s*git\s+commit\b`,
			Tool:    "commit",
			Message: "Use the commit tool instead.",
		},
	}

	for _, cmd := range []string{
		"git commit -m message",
		"git add file && git commit -m message",
		"git add file; git commit -m message",
		"git add file || git commit -m message",
		"git add file & git commit -m message",
		"git add file\ngit commit -m message",
	} {
		if !CheckBashInterception(cmd, []string{"commit"}, rules, cmd).Block {
			t.Errorf("expected block for %q", cmd)
		}
	}

	// Downstream pipe stage is not intercepted; || starts a fresh command.
	if CheckBashInterception("git add file | git commit -m message", []string{"commit"}, rules, "").Block {
		t.Errorf("did not expect piped stage to be blocked")
	}
	if !CheckBashInterception("git add file || git commit -m message", []string{"commit"}, rules, "").Block {
		t.Errorf("expected || stage to be blocked")
	}

	// Leading env assignments stripped.
	if !CheckBashInterception(`GIT_AUTHOR_EMAIL="a@example.com" GIT_AUTHOR_NAME=Dev git commit -m message`, []string{"commit"}, rules, "").Block {
		t.Errorf("expected env-prefixed command to be blocked")
	}

	// Quoted, escaped, commented, redirected, or nested syntax not falsely matched.
	for _, cmd := range []string{
		`printf '%s\n' "git add file && git commit -m message"`,
		`echo "git commit"`,
		`echo git\ commit`,
		`echo ok # git commit -m message`,
		`echo hi >|git commit -m message`,
		`echo hi >& git commit -m message`,
		`echo hi <&3 git commit -m message`,
		`echo "$(git commit -m message)"`,
		"echo `git commit -m message`",
		`( git commit -m message )`,
		`echo start; { true; git commit -m message; }`,
		"cat <<'EOF'\ngit commit -m message\nEOF",
	} {
		if CheckBashInterception(cmd, []string{"commit"}, rules, cmd).Block {
			t.Errorf("did not expect block for %q", cmd)
		}
	}

	// Unavailable tool does not block.
	if CheckBashInterception("git add file && git commit -m message", nil, rules, "").Block {
		t.Errorf("did not expect block when suggested tool is unavailable")
	}

	// Tool integration: checks original command before cd normalization and normalized command after.
	tool, dir := newTestTool(t, func(cfg *Config) {
		cfg.InterceptorEnabled = true
		cfg.AvailableTools = []string{"read"}
		cfg.InterceptorRules = []InterceptorRule{
			{Pattern: `^\s*cat\s+`, Tool: "read", Message: "Use read instead."},
		}
	})
	orig := fmt.Sprintf("cd %s && cat package.json", dir)
	_, err := runTool(t, tool, map[string]any{"command": orig})
	if err == nil || !strings.Contains(err.Error(), "Use read instead.\n\nOriginal command: "+orig) {
		t.Fatalf("expected interception error with original command, got %v", err)
	}
}
