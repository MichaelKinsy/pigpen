package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/kagent-dev/kagent/go/harness/runtime"
)

func mustRun(t *testing.T, h *harness, turn runtime.Turn, sink *recorder) runtime.Outcome {
	t.Helper()
	out, err := h.d.Run(t.Context(), turn, sink)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out
}

func TestTextTurnTranslatesEventsAndSpawnsPigCorrectly(t *testing.T) {
	h := newHarness(t, "text")
	sink := &recorder{}
	out := mustRun(t, h, h.turn("hi there"), sink)
	if out.Failure != nil || out.Pending != nil {
		t.Fatalf("outcome = %+v", out)
	}
	// a fresh continuation id is a UUID and is announced before anything else
	if len(sink.events) == 0 || !strings.HasPrefix(sink.events[0], "session:") {
		t.Fatalf("events = %q", sink.events)
	}
	id := strings.TrimPrefix(sink.events[0], "session:")
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("continuation id %q: %v", id, err)
	}
	if sink.text() != "hello world" {
		t.Fatalf("text = %q", sink.text())
	}
	if len(sink.models) != 1 {
		t.Fatalf("model calls = %d", len(sink.models))
	}
	m := sink.models[0]
	// PiG's input excludes cache reads; kagent's includes them
	if m.Provider != "openai" || m.RequestModel != "mock-model" || m.ResponseID != "resp-stop" || m.StopReason != "stop" ||
		m.InputTokens != 13 || m.CacheReadTokens != 3 || m.OutputTokens != 5 || m.ReasoningOutputTokens != 2 || m.Canceled || m.UsagePartial ||
		m.End.Sub(m.Start) != 40*time.Millisecond {
		t.Fatalf("model call = %+v", m)
	}

	start := h.logs("start")[0]
	args := toStrings(start["args"])
	for _, want := range [][]string{{"--mode", "rpc"}, {"--provider", "openai"}, {"--model", "mock-model"}, {"--session-id", id}, {"--session-dir", h.d.cfg.SessionDir},
		{"--tools", "read,bash,edit,write,grep,find,ls,ask_user,mcp__localhost__*"}} {
		if !hasPair(args, want[0], want[1]) {
			t.Errorf("args %v lack %v", args, want)
		}
	}
	for _, flag := range []string{"--offline", "--no-skills", "--no-prompt-templates", "--no-themes"} {
		if !slices.Contains(args, flag) {
			t.Errorf("args lack %s", flag)
		}
	}
	for _, banned := range []string{"-e", "--extension", "--no-extensions", "--no-session"} {
		if slices.Contains(args, banned) {
			t.Errorf("args contain %s", banned)
		}
	}
	prompt := argAfter(args, "--append-system-prompt")
	if prompt != filepath.Join(h.pigHome, "system-prompt.md") {
		t.Fatalf("system prompt arg = %q", prompt)
	}
	if got, _ := os.ReadFile(prompt); string(got) != "Be brief." {
		t.Fatalf("system prompt file = %q", got)
	}
	if cwd, _ := filepath.EvalSymlinks(start["cwd"].(string)); cwd != evalSymlinks(h.d.cfg.Workspace) {
		t.Fatalf("cwd = %s, want %s", cwd, h.d.cfg.Workspace)
	}
	if prompts := h.logs("command"); len(prompts) < 2 || prompts[0]["type"] != "get_state" || prompts[1]["type"] != "prompt" || prompts[1]["message"] != "hi there" {
		t.Fatalf("commands = %v", prompts)
	}
	// ephemeral files live in PIG_HOME, never under /data
	for _, name := range []string{"agent/models.json", "kagent-policy.json", "system-prompt.md"} {
		info, err := os.Stat(filepath.Join(h.pigHome, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", name, info, err)
		}
	}
	filepath.WalkDir(h.data, func(path string, d os.DirEntry, _ error) error {
		if !d.IsDir() && (strings.HasSuffix(path, "models.json") || strings.Contains(path, "policy")) {
			t.Errorf("generated file under the durable dir: %s", path)
		}
		return nil
	})
	h.assertReaped()
}

func evalSymlinks(p string) string { r, _ := filepath.EvalSymlinks(p); return r }

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func hasPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func argAfter(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func TestPigEnvironmentIsAnAllowlist(t *testing.T) {
	h := newHarness(t, "text")
	h.extra = []string{"GH_TOKEN=ghp", "OTHER_SECRET=x"}
	h.d.cfg.PassEnv = append(h.d.cfg.PassEnv, "GH_TOKEN")
	h.setMode("text")
	mustRun(t, h, h.turn("x"), &recorder{})
	env := map[string]string{}
	for _, e := range toStrings(h.logs("start")[0]["env"]) {
		k, v, _ := strings.Cut(e, "=")
		env[k] = v
	}
	for k, v := range map[string]string{
		"HOME": "/var/lib/actor", "TZ": "UTC", "SSL_CERT_FILE": "/run/kagent/egress/trust-bundle.pem", "HTTPS_PROXY": "http://proxy:3128",
		"PIG_HOME": h.pigHome, "PIG_OFFLINE": "1", "PI_SKIP_VERSION_CHECK": "1", "KAGENT_PIG_POLICY": filepath.Join(h.pigHome, "kagent-policy.json"),
		"OPENAI_API_KEY": "kagent-credential-injected", "GH_TOKEN": "ghp",
	} {
		if env[k] != v {
			t.Errorf("env %s = %q, want %q", k, env[k], v)
		}
	}
	for _, k := range []string{"KAGENT_BOOTSTRAP_JSON", "SOMETHING_ELSE", "OTHER_SECRET"} {
		if _, ok := env[k]; ok {
			t.Errorf("env leaks %s", k)
		}
	}
	// header values arrive by indirection, as variables
	if env["KAGENT_PIG_HDR_1"] == "" {
		t.Errorf("KAGENT_PIG_HDR_1 missing from pig's environment")
	}
}

func TestToolEventsCarryMCPServerAndTool(t *testing.T) {
	h := newHarness(t, "tool")
	sink := &recorder{}
	mustRun(t, h, h.turn("go"), sink)
	if len(sink.calls) != 2 || len(sink.results) != 2 {
		t.Fatalf("calls %d results %d: %q", len(sink.calls), len(sink.results), sink.events)
	}
	bash, probe := sink.calls[0], sink.calls[1]
	if bash.ID != "call_bash" || bash.Name != "bash" || bash.MCPServer != "" || bash.Arguments["command"] != "echo hi" {
		t.Errorf("bash call = %+v", bash)
	}
	if probe.ID != "call_mcp" || probe.Name != fakeProbeName || probe.MCPServer != "localhost" || probe.MCPTool != "kagent_probe" || probe.Arguments["note"] != "n" {
		t.Errorf("mcp call = %+v", probe)
	}
	if sink.results[0].IsError || !sink.results[1].IsError || sink.results[1].Name != fakeProbeName {
		t.Errorf("results = %+v", sink.results)
	}
	// ordering: call before its result
	if strings.Index(strings.Join(sink.events, "|"), "call:call_bash") > strings.Index(strings.Join(sink.events, "|"), "result:call_bash") {
		t.Errorf("events = %q", sink.events)
	}
}

func TestStreamedTextIsReconciledAndSeparatedByMessage(t *testing.T) {
	h := newHarness(t, "text-reconcile")
	sink := &recorder{}
	mustRun(t, h, h.turn("x"), sink)
	if sink.text() != "hello world\n\nsecond" {
		t.Fatalf("text = %q", sink.text())
	}
}

func TestModelErrorNeverCopiesProviderText(t *testing.T) {
	h := newHarness(t, "error")
	sink := &recorder{}
	out := mustRun(t, h, h.turn("x"), sink)
	if out.Failure == nil || out.Failure.Message != failModelCall {
		t.Fatalf("outcome = %+v", out)
	}
	if strings.Contains(strings.Join(sink.events, "|"), "SECRET") || strings.Contains(sink.text(), "SECRET") {
		t.Fatalf("provider text reached the sink: %q", sink.events)
	}
	if len(sink.models) != 1 || sink.models[0].StopReason != "error" || sink.models[0].ErrorType == "" {
		t.Fatalf("model calls = %+v", sink.models)
	}
}

func TestRejectedPromptIsAFixedFailure(t *testing.T) {
	h := newHarness(t, "reject")
	out := mustRun(t, h, h.turn("x"), &recorder{})
	if out.Failure == nil || out.Failure.Message != failRejected || strings.Contains(out.Failure.Message, "SECRET") {
		t.Fatalf("outcome = %+v", out)
	}
	h.assertReaped()
}

func TestProcessCrashMidTurn(t *testing.T) {
	h := newHarness(t, "crash")
	out, err := h.d.Run(t.Context(), h.turn("x"), &recorder{})
	if err == nil || !strings.Contains(err.Error(), "boom: fake pig crashed") || out.Pending != nil {
		t.Fatalf("out = %+v err = %v", out, err)
	}
	if r, restoreErr := h.d.journal.Load(); r != nil || restoreErr != nil {
		t.Fatalf("a crash left a record: %+v %v", r, restoreErr)
	}
	h.assertReaped()
}

func TestSilentExitIsAnErrorNotASuccess(t *testing.T) {
	h := newHarness(t, "silent-exit")
	if _, err := h.d.Run(t.Context(), h.turn("x"), &recorder{}); err == nil {
		t.Fatal("a pig that exits without settling was treated as success")
	}
}

func TestOversizedFrameKillsPigAndFails(t *testing.T) {
	h := newHarness(t, "huge")
	h.d.cfg.Grace = 100 * time.Millisecond
	started := time.Now()
	_, err := h.d.Run(t.Context(), h.turn("x"), &recorder{})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(started) > 10*time.Second {
		t.Fatalf("took %s", time.Since(started))
	}
	h.assertReaped()
}

func TestRunRefusesAForkedSessionBeforeSpawning(t *testing.T) {
	h := newHarness(t, "text")
	id := "11111111-2222-4333-8444-555555555555"
	os.MkdirAll(h.d.cfg.SessionDir, 0o700)
	for _, ts := range []string{"2026-11-07T01-00-00-000Z", "2026-11-07T02-00-00-000Z"} {
		os.WriteFile(filepath.Join(h.d.cfg.SessionDir, ts+"_"+id+".jsonl"), []byte("{}\n"), 0o600)
	}
	turn := h.turn("x")
	turn.ContinuationID = id
	_, err := h.d.Run(t.Context(), turn, &recorder{})
	if err == nil || !strings.Contains(err.Error(), "forked") {
		t.Fatalf("err = %v", err)
	}
	if len(h.logs("start")) != 0 {
		t.Fatal("pig was spawned despite the duplicate session files")
	}
}

func TestWrongSessionOrModelIsRefusedBeforeThePrompt(t *testing.T) {
	for _, mode := range []string{"wrong-session", "wrong-model"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, mode)
			sink := &recorder{}
			_, err := h.d.Run(t.Context(), h.turn("x"), sink)
			if err == nil {
				t.Fatal("Run succeeded")
			}
			for _, c := range h.logs("command") {
				if c["type"] == "prompt" {
					t.Fatal("the prompt was sent to a pig on the wrong session or model")
				}
			}
			var notStarted *notStartedError
			if !errors.As(err, &notStarted) {
				t.Errorf("err = %T, want a not-started error", err)
			}
			if sink.has("session:") {
				t.Error("SessionStarted was emitted for a refused session")
			}
		})
	}
}

func TestPigThatCannotStartIsANotStartedError(t *testing.T) {
	h := newHarness(t, "text", func(c *Config) { c.Executable = "/nonexistent/pig" })
	_, err := h.d.Run(t.Context(), h.turn("x"), &recorder{})
	var notStarted *notStartedError
	if !errors.As(err, &notStarted) {
		t.Fatalf("err = %v", err)
	}
}

func TestEmptyPromptIsRejected(t *testing.T) {
	h := newHarness(t, "text")
	if _, err := h.d.Run(t.Context(), h.turn("  "), &recorder{}); err == nil {
		t.Fatal("empty prompt accepted")
	}
}

func TestToolsLeftRunningAreKilledWithTheProcessGroup(t *testing.T) {
	h := newHarness(t, "text-with-child")
	mustRun(t, h, h.turn("x"), &recorder{})
	raw, err := os.ReadFile(h.child)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(raw))
	eventually(t, "the child of pig to be killed", func() bool { return !alive(pid) })
}

func TestCancelMidTurnAbortsOverRPC(t *testing.T) {
	h := newHarness(t, "hang")
	ctx, cancel := context.WithCancel(t.Context())
	sink := &recorder{}
	done := make(chan error, 1)
	go func() {
		_, err := h.d.Run(ctx, h.turn("x"), sink)
		done <- err
	}()
	eventually(t, "pig to be mid-turn", func() bool { return len(h.logs("hanging")) > 0 })
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	aborted := false
	for _, c := range h.logs("command") {
		aborted = aborted || c["type"] == "abort"
	}
	if !aborted {
		t.Fatal("pig never received an abort command")
	}
	sink.mu.Lock()
	models := slices.Clone(sink.models)
	sink.mu.Unlock()
	if len(models) != 1 || !models[0].Canceled || models[0].StopReason != "aborted" || models[0].InputTokens != 7 {
		t.Fatalf("model calls = %+v (usage reported while cancelled must reach the sink)", models)
	}
	if sink.text() != "partial" {
		t.Fatalf("text = %q, want only what streamed before the cancel", sink.text())
	}
	h.assertReaped()
}

func TestCancelEscalatesToProcessGroupKill(t *testing.T) {
	h := newHarness(t, "hang-stubborn")
	h.d.cfg.Grace = 150 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := h.d.Run(ctx, h.turn("x"), &recorder{})
		done <- err
	}()
	eventually(t, "pig to hang", func() bool { return len(h.logs("hanging")) > 0 })
	cancel()
	select {
	case err := <-done:
		var cancellation *runtime.CancellationError
		if !errors.Is(err, context.Canceled) || errors.As(err, &cancellation) {
			t.Fatalf("a killed but reaped pig is a clean cancellation, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}
	h.assertReaped()
}

func TestCancelReportsCancellationErrorWhenPigCannotBeReaped(t *testing.T) {
	h := newHarness(t, "hang-stubborn")
	h.d.cfg.Grace = 50 * time.Millisecond
	h.d.cfg.ReapTimeout = time.Nanosecond
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := h.d.Run(ctx, h.turn("x"), &recorder{})
		done <- err
	}()
	eventually(t, "pig to hang", func() bool { return len(h.logs("hanging")) > 0 })
	cancel()
	select {
	case err := <-done:
		var cancellation *runtime.CancellationError
		if !errors.As(err, &cancellation) {
			t.Fatalf("err = %v, want a CancellationError", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}
	h.assertReaped()
}

func TestExtensionDialogsTheHarnessCannotAnswerAreCancelled(t *testing.T) {
	h := newHarness(t, "approve-garbage")
	sink := &recorder{}
	out := mustRun(t, h, h.turn("x"), sink)
	if out.Failure != nil || out.Pending != nil || sink.text() != "after dialogs" {
		t.Fatalf("out = %+v text = %q", out, sink.text())
	}
	cancelled := 0
	for _, c := range h.logs("command") {
		if c["type"] == "extension_ui_response" && c["cancelled"] == true {
			cancelled++
		}
	}
	if cancelled != 2 {
		t.Fatalf("cancelled responses = %d, want 2 (malformed confirm and select)", cancelled)
	}
}

func TestHeaderValuesAndPolicyAreWrittenPerTurn(t *testing.T) {
	h := newHarness(t, "text")
	mustRun(t, h, h.turn("x"), &recorder{})
	policy, err := os.ReadFile(filepath.Join(h.pigHome, "kagent-policy.json"))
	if err != nil || !strings.Contains(string(policy), `"${KAGENT_PIG_HDR_`) || strings.Contains(string(policy), "kagent-credential-injected") {
		t.Fatalf("policy = %s %v", policy, err)
	}
	os.Remove(filepath.Join(h.pigHome, "kagent-policy.json"))
	mustRun(t, h, runtime.Turn{TaskID: "t2", ContextID: "c", Prompt: "again", ContinuationID: continuationOf(t, h)}, &recorder{})
	if _, err := os.Stat(filepath.Join(h.pigHome, "kagent-policy.json")); err != nil {
		t.Fatal("the policy file was not regenerated for the next turn")
	}
}

func continuationOf(t *testing.T, h *harness) string {
	t.Helper()
	path := h.sessionFile()
	base := filepath.Base(path)
	return strings.TrimSuffix(base[strings.Index(base, "_")+1:], ".jsonl")
}
