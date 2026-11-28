package eval

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/approval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type wireToolResult struct {
	Content any             `json:"content"`
	Details EvalToolDetails `json:"details"`
	IsError bool            `json:"is_error"`
}

func decodeToolResult(t *testing.T, raw json.RawMessage) (text string, images []map[string]any, details EvalToolDetails, isError bool) {
	t.Helper()
	var res wireToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal tool result %s: %v", string(raw), err)
	}
	switch c := res.Content.(type) {
	case string:
		text = c
	case []any:
		var parts []string
		for _, item := range c {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if m["type"] == "text" {
				if s, ok := m["text"].(string); ok {
					parts = append(parts, s)
				}
			} else if m["type"] == "image" {
				images = append(images, m)
			}
		}
		text = strings.Join(parts, "\n")
	}
	return text, images, res.Details, res.IsError
}

func hasPython3(t *testing.T) bool {
	t.Helper()
	return checkPythonAvailability(context.Background(), t.TempDir(), "").OK
}

func hasJSRuntime(t *testing.T) bool {
	t.Helper()
	return probeJSRuntime(context.Background()).OK
}

// --- Description & Doc Topics tests ---

// upstream: packages/coding-agent/test/tools/eval-description.test.ts: "drops the agents topic but keeps wait() when the session forbids spawning"
func TestDescriptionDropsAgentsWhenSpawnsDisabled(t *testing.T) {
	opts := DescriptionOptions{Py: true, JS: true, Spawns: false, EvalTools: true, WaitTool: true}
	desc := RenderDescription(opts)
	if strings.Contains(desc, "help://eval/agents") {
		t.Fatalf("expected no help://eval/agents when spawns disabled:\n%s", desc)
	}
	if strings.Contains(desc, "workpool(") {
		t.Fatalf("expected no workpool() when spawns disabled:\n%s", desc)
	}
	if !strings.Contains(desc, "wait(handles") {
		t.Fatalf("expected wait(handles) to stay:\n%s", desc)
	}
	topics := DocTopics(opts)
	if _, ok := topics["agents"]; ok {
		t.Fatalf("expected agents topic omitted when spawns disabled")
	}
}

// upstream: packages/coding-agent/test/tools/eval-description.test.ts: "gates only tool-definition guidance, not budget or completion"
func TestDescriptionGatesEvalTools(t *testing.T) {
	enabled := RenderDescription(DescriptionOptions{Py: true, JS: true, Spawns: true, EvalTools: true})
	disabled := RenderDescription(DescriptionOptions{Py: true, JS: true, Spawns: true, EvalTools: false})
	if !strings.Contains(enabled, "@tool") {
		t.Fatalf("expected @tool when EvalTools=true")
	}
	if strings.Contains(disabled, "@tool") {
		t.Fatalf("expected no @tool when EvalTools=false")
	}
	disabledTopics := DocTopics(DescriptionOptions{Py: true, JS: true, Spawns: true, EvalTools: false})
	if !strings.Contains(disabledTopics["helpers"], "budget.total") {
		t.Fatalf("expected budget.total kept in helpers topic")
	}
	if strings.Contains(disabledTopics["helpers"], "@tool") {
		t.Fatalf("expected @tool omitted from helpers topic when EvalTools=false")
	}
	if !strings.Contains(disabledTopics["judge"], "completion(prompt") {
		t.Fatalf("expected completion(prompt kept in judge topic")
	}
	if strings.Contains(disabledTopics["judge"], "{{#if") || strings.Contains(disabledTopics["agents"], "{{#if") {
		t.Fatalf("expected rendered topics with no raw Handlebars tags:\njudge=%s\nagents=%s", disabledTopics["judge"], disabledTopics["agents"])
	}
}

// upstream: packages/coding-agent/test/tools/eval-description.test.ts: "renders helper syntax only for available runtimes"
func TestDescriptionRendersOnlyAvailableRuntimes(t *testing.T) {
	jsOnly := DocTopics(DescriptionOptions{Py: false, JS: true, EvalTools: true})
	if !strings.Contains(jsOnly["helpers"], "%bun add") || strings.Contains(jsOnly["helpers"], "%pip install") {
		t.Fatalf("jsOnly helpers mismatch:\n%s", jsOnly["helpers"])
	}
	if !strings.Contains(jsOnly["helpers"], "await budget.total()") {
		t.Fatalf("jsOnly helpers missing await budget.total()")
	}
	pyOnly := DocTopics(DescriptionOptions{Py: true, JS: false, EvalTools: true})
	if !strings.Contains(pyOnly["helpers"], "%pip install") || strings.Contains(pyOnly["helpers"], "%bun add") {
		t.Fatalf("pyOnly helpers mismatch:\n%s", pyOnly["helpers"])
	}
	if strings.Contains(pyOnly["helpers"], "await budget.total()") {
		t.Fatalf("pyOnly helpers should not contain JS await budget.total()")
	}
	// Embedded eval.md template with JS disabled keeps the single blank line produced by line 16.
	pyDesc := RenderDescription(DescriptionOptions{Py: true, JS: false})
	if !strings.Contains(pyDesc, "Top-level `await` works; `asyncio.run(…)` fails.\n\nOn error,") {
		t.Fatalf("expected blank line from eval.md line 16 when js=false:\n%s", pyDesc)
	}
}

// upstream: packages/coding-agent/test/tools/eval-description.test.ts: "inlines every topic when the session cannot open help:// links"
func TestDescriptionInlinesTopicsWithoutRead(t *testing.T) {
	linked := RenderDescription(DescriptionOptions{Py: true, JS: true, Spawns: true, EvalTools: true, InlineTopics: false})
	inlined := RenderDescription(DescriptionOptions{Py: true, JS: true, Spawns: true, EvalTools: true, InlineTopics: true})
	for _, uri := range []string{"help://eval/judge", "help://eval/helpers", "help://eval/agents"} {
		if !strings.Contains(linked, uri) || strings.Contains(inlined, uri) {
			t.Fatalf("uri %s presence mismatch: linked=%v inlined=%v", uri, strings.Contains(linked, uri), strings.Contains(inlined, uri))
		}
	}
	for _, api := range []string{"completion(prompt", "judge(state, questions", "budget.total", "agent(prompt"} {
		if strings.Contains(linked, api) || !strings.Contains(inlined, api) {
			t.Fatalf("api %s presence mismatch: linked=%v inlined=%v", api, strings.Contains(linked, api), strings.Contains(inlined, api))
		}
	}
	if strings.Contains(inlined, "{{") {
		t.Fatalf("inlined description contains unrendered Handlebars tags:\n%s", inlined)
	}
}

// upstream: packages/coding-agent/test/tools/eval-description.test.ts: "links only current enabled prelude documentation"
func TestDescriptionLinksEnabledPreludes(t *testing.T) {
	ResetPreludesForTests()
	t.Cleanup(ResetPreludesForTests)

	enabled := true
	RegisterPrelude(Prelude{
		Name:          "fixture",
		Documentation: func() string { return "Fixture summary line.\n\nFULL DOCS" },
		Enabled:       func(sdk.Context) bool { return enabled },
	})
	RegisterPrelude(Prelude{
		Name:                "inlined",
		Documentation:       func() string { return "Inlined full doc body." },
		InlineDocumentation: true,
	})

	opts := DefaultDescriptionOptions(EnabledPreludes(sdk.Context{}))
	desc := RenderDescription(opts)
	if !strings.Contains(desc, "- `fixture`: Fixture summary line. → help://eval/fixture") {
		t.Fatalf("expected fixture link in description:\n%s", desc)
	}
	if !strings.Contains(desc, "<inlined>\nGlobal `inlined`, documented here in full (no `read` needed):\nInlined full doc body.\n</inlined>") {
		t.Fatalf("expected inline prelude section in description:\n%s", desc)
	}
	// Spawns is false when no `agent` prelude is registered.
	if opts.Spawns || strings.Contains(desc, "help://eval/agents") {
		t.Fatalf("expected Spawns=false without agent prelude:\n%s", desc)
	}

	// Registering `agent` prelude flips Spawns=true without duplicating `agent` as a custom prelude link.
	RegisterPrelude(Prelude{
		Name:   "agent",
		Invoke: func(PreludeCall, json.RawMessage) (*PreludeResult, error) { return &PreludeResult{}, nil },
	})
	optsWithAgent := DefaultDescriptionOptions(EnabledPreludes(sdk.Context{}))
	descWithAgent := RenderDescription(optsWithAgent)
	if !optsWithAgent.Spawns || !strings.Contains(descWithAgent, "help://eval/agents") {
		t.Fatalf("expected Spawns=true when agent prelude registered:\n%s", descWithAgent)
	}
	if strings.Contains(descWithAgent, "help://eval/agent\n") {
		t.Fatalf("agent prelude should not be duplicated as custom link:\n%s", descWithAgent)
	}

	enabled = false
	descDisabled := RenderDescription(DefaultDescriptionOptions(EnabledPreludes(sdk.Context{})))
	if strings.Contains(descDisabled, "fixture") {
		t.Fatalf("expected disabled prelude omitted:\n%s", descDisabled)
	}
}

// Golden exact-match check of the default eval.md rendering against omp's exact template output.
func TestDescriptionGoldenDefaultRender(t *testing.T) {
	defaultOpts := DefaultDescriptionOptions(nil)
	if !defaultOpts.EvalTools {
		t.Fatalf("expected DefaultDescriptionOptions().EvalTools == true")
	}
	got := RenderDescription(DescriptionOptions{
		Py:        true,
		JS:        true,
		Spawns:    false,
		EvalTools: true,
		WaitTool:  true,
	})
	want := strings.Join([]string{
		"One cell per call; top-level state persists, including across compaction.",
		"A cell without `timeout` stops after 30s; pass a larger `timeout` for slow subprocesses, network calls or large scans.",
		"Python: plain Python subprocess, not IPython (no `get_ipython()`, `obj?`, or %matplotlib).",
		"Magics like %pip, %load, %time and `!cmd` are rewritten to Python.",
		"Top-level `await` works; `asyncio.run(…)` fails.",
		"JS: Bun (Bun.file, Bun.write, Bun.$); top-level `await`/`return` work.",
		"On error, retry only the failed step; earlier steps may have taken effect.",
		"Printing an image or figure attaches it.",
		"",
		"<prelude>",
		"Python helpers take kwargs; JS helpers take the same parameters (camelCase) as positional args or ONE trailing options object.",
		"`await` marks async helpers; the rest are sync.",
		"```",
		"print(value, ...)  log(message)  phase(title)  env(key?, value?)",
		"read(path, offset?, limit?)  write(path, content)  output(*ids, format?, query?, offset?, limit?); JS: await read(…)  await write(…)  await output(…)",
		"wait(handles, timeout?=None, raise_errors?=True); JS: await wait(…) — agent/completion barrier, ordered results; raise_errors=False retains failures",
		"await tool.<name>(args) — session tool; `args` is its parameter object",
		"sh(command, cwd?, timeout?); JS: await sh(…) or await sh`cmd ${arg}` — embedded bash engine, runs to completion (never backgrounded)",
		"  returns stdout+stderr; non-zero exit raises ShellError (.exit_code/.exitCode, .output)",
		"  JS interpolations are shell-quoted; starts in the session cwd unless cwd is given",
		"```",
		"</prelude>",
		"",
		"<namespaces>",
		"More globals; `read` the linked docs before first use:",
		"- `judge`, `judge_batch`, `completion`: classification, bulk judgment, model calls → help://eval/judge",
		"- `%load`, `%pip`, `%bun add`, `budget`, `@tool`/`tool(fn)`: setup, installs, utilities → help://eval/helpers",
		"</namespaces>",
		"",
		"<critical>",
		"NEVER repeat successful setup. Kernel-loss notice means reload setup.",
		"</critical>",
		"",
		"<examples>",
		"# Load a script with spaces without echoing its source",
		"<example>",
		`eval(language="py", code="%load \"scripts/my setup.py\"")`,
		"</example>",
		"</examples>",
	}, "\n")
	if got != want {
		t.Fatalf("golden description mismatch:\n--- GOT ---\n%s\n--- WANT ---\n%s", got, want)
	}

	// When py is disabled, the py-only example block is filtered out (tools/eval.ts:369-372).
	jsOnly := RenderDescription(DescriptionOptions{Py: false, JS: true, WaitTool: true})
	if strings.Contains(jsOnly, "<examples>") {
		t.Fatalf("expected <examples> omitted when py=false:\n%s", jsOnly)
	}
}

// --- IdleTimeout & bridge-timeout tests ---

// upstream: packages/coding-agent/test/eval/idle-timeout.test.ts: "ignores elapsed time while paused and resumes with a fresh window"
func TestIdleTimeoutPauseAndResume(t *testing.T) {
	idle := NewIdleTimeout(context.Background(), 80*time.Millisecond)
	defer idle.Dispose()

	time.Sleep(30 * time.Millisecond)
	idle.Pause()
	time.Sleep(120 * time.Millisecond)
	if idle.TimedOut() || idle.Context().Err() != nil {
		t.Fatalf("expected IdleTimeout to stay alive while paused")
	}
	idle.Resume()
	select {
	case <-idle.Context().Done():
		if !idle.TimedOut() {
			t.Fatalf("expected TimedOut() == true after expiry")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected IdleTimeout to fire after Resume()")
	}
}

// upstream: packages/coding-agent/test/eval/idle-timeout.test.ts: "reference-counts overlapping pauses"
func TestIdleTimeoutReferenceCountsPauses(t *testing.T) {
	idle := NewIdleTimeout(context.Background(), 60*time.Millisecond)
	defer idle.Dispose()

	idle.Pause()
	idle.Pause()
	time.Sleep(100 * time.Millisecond)
	if idle.TimedOut() {
		t.Fatalf("should not fire while pause depth == 2")
	}
	idle.Resume()
	time.Sleep(100 * time.Millisecond)
	if idle.TimedOut() {
		t.Fatalf("should not fire while pause depth == 1")
	}
	idle.Resume()
	select {
	case <-idle.Context().Done():
		if !idle.TimedOut() {
			t.Fatalf("expected TimedOut() after final resume")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected expiry after final resume")
	}
}

// upstream: packages/coding-agent/test/eval/idle-timeout.test.ts: "never fires after dispose()"
func TestIdleTimeoutNeverFiresAfterDispose(t *testing.T) {
	idle := NewIdleTimeout(context.Background(), 30*time.Millisecond)
	idle.Dispose()
	time.Sleep(80 * time.Millisecond)
	if idle.TimedOut() {
		t.Fatalf("disposed IdleTimeout must not mark TimedOut")
	}
}

// upstream: packages/coding-agent/test/eval/bridge-timeout.test.ts: "resumes timeout accounting even when the operation throws"
func TestWithBridgeTimeoutPauseResumesOnError(t *testing.T) {
	var ops []string
	_, err := withBridgeTimeoutPause(
		func(ev EvalStatusEvent) { ops = append(ops, anyString(ev["op"])) },
		func() (string, error) { return "", errors.New("boom") },
	)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected boom error, got %v", err)
	}
	if len(ops) != 2 || ops[0] != EvalTimeoutPauseOp || ops[1] != EvalTimeoutResumeOp {
		t.Fatalf("expected pause then resume, got %v", ops)
	}
	if !isEvalTimeoutControlEvent(EvalStatusEvent{"op": EvalTimeoutPauseOp}) ||
		!isEvalTimeoutControlEvent(EvalStatusEvent{"op": EvalTimeoutResumeOp}) ||
		isEvalTimeoutControlEvent(EvalStatusEvent{"op": "agent"}) {
		t.Fatalf("isEvalTimeoutControlEvent classification failed")
	}
}

// --- Timeout admission & fractional timeout tests ---

// upstream: packages/coding-agent/test/tools/eval-timeout-contract.test.ts: "discloses a changed supplied deadline before execution"
func TestClampEvalTimeoutFractionalAndFormatting(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		raw       *float64
		cap       float64
		want      float64
		wantClamp bool
	}{
		{f(5400), 0, 3600, true},
		{f(30), 5, 5, true},
		{f(0.25), 0, 1, true},
		{f(-2), 0, 1, true},
		{f(1.25), 0, 1.25, false},
		{f(2.5), 0, 2.5, false},
		{nil, 0, 30, false},
		{nil, 4, 4, false},
		{f(0), 5, 0, false},
	}
	for _, tc := range cases {
		got := clampEvalTimeout(tc.raw, tc.cap)
		if got != tc.want {
			t.Fatalf("clampEvalTimeout(%v, %v) = %v, want %v", tc.raw, tc.cap, got, tc.want)
		}
		clamped := tc.raw != nil && got != *tc.raw
		if clamped != tc.wantClamp {
			t.Fatalf("clampEvalTimeout(%v, %v) clamped=%v, want %v", tc.raw, tc.cap, clamped, tc.wantClamp)
		}
	}
	// Large values must format without scientific notation (matching JS template strings).
	if s := formatTimeoutNumber(1000000); s != "1000000" {
		t.Fatalf("formatTimeoutNumber(1000000) = %q, want 1000000", s)
	}
}

// --- Percent commands & input validation tests ---

// upstream: packages/coding-agent/test/eval/input.test.ts: "rejects ambiguous commands and installer flags before installing"
func TestPrepareEvalSourceValidation(t *testing.T) {
	cwd := t.TempDir()
	cases := []struct {
		lang string
		code string
		want string
	}{
		{"js", "%load a.ts\nrun()", "standalone"},
		{"js", `%load "missing.ts`, "quote"},
		{"js", "%pip install pillow", "%bun"},
		{"js", "%load https://example.com/setup.ts", "local"},
		{"py", "%load", "Usage: %load"},
		{"js", "%environment invalid", "Usage: %environment"},
	}
	for _, tc := range cases {
		_, err := prepareEvalSource(tc.lang, tc.code, cwd, sdk.Context{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("prepareEvalSource(%q, %q) err=%v, want substring %q", tc.lang, tc.code, err, tc.want)
		}
	}
	// Python leaves %pip to runner.py magic.
	prep, err := prepareEvalSource("py", "%pip install pillow", cwd, sdk.Context{})
	if err != nil || prep.Code != "%pip install pillow" {
		t.Fatalf("expected python %%pip passed through, got %+v err=%v", prep, err)
	}
}

// --- Security: runner staging directory & bridge auth ---

// upstream: packages/coding-agent/src/eval/runner-cache.ts, utils/owner-private-dir.ts
func TestStageRunnerScriptUsesOwnerPrivatePerUIDDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX uid/0700 checks only")
	}
	staged, err := stageRunnerScript("omp-test-stage-"+newRandomID(), "py", "print('ok')\n")
	if err != nil {
		t.Fatalf("stageRunnerScript failed: %v", err)
	}
	dir := filepath.Dir(staged)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf("lstat staging dir: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("expected staging dir mode 0700, got %o", info.Mode().Perm())
	}

	// A symlink at the staging dir path must be rejected by assertOwnerPrivateDir.
	linkPath := filepath.Join(t.TempDir(), "symlink-dir")
	if err := os.Symlink(dir, linkPath); err != nil {
		t.Fatal(err)
	}
	if err := assertOwnerPrivateDir(linkPath, "test"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestBridgeRejectsMissingOrWrongBearerToken(t *testing.T) {
	srv, err := startBridgeServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.stop()

	resp, err := http.Post(srv.url+"/v1/tool", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without token, got %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodPost, srv.url+"/v1/tool", bytes.NewBufferString(`{}`))
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 with wrong token, got %d", resp2.StatusCode)
	}
}

// --- Bounded output & TruncationMeta tests ---

// upstream: packages/tui/src/tools/output-meta.ts (formatTruncationMetaNotice)
func TestBoundedOutputBufferTruncationAndUTF8Safety(t *testing.T) {
	buf := newBoundedOutputBuffer(100)
	// Write multi-byte UTF-8 lines totaling well over 100 bytes.
	for range 30 {
		buf.Append("héllo-wörld-line\n")
	}
	out, meta := buf.Finalize()
	if meta == nil {
		t.Fatalf("expected TruncationMeta on oversized output")
	}
	if meta.Direction != "tail" || meta.TruncatedBy != "bytes" {
		t.Fatalf("unexpected truncation meta: %+v", meta)
	}
	if meta.TotalLines != 30 || meta.OutputBytes > 100 {
		t.Fatalf("unexpected counts: %+v", meta)
	}
	if meta.ShownRange == nil || meta.ShownRange.End != 30 || meta.ShownRange.Start < 1 {
		t.Fatalf("unexpected shownRange: %+v", meta.ShownRange)
	}
	// Must start on a clean line boundary, never mid-rune.
	if !strings.HasPrefix(out, "héllo-wörld-line") {
		t.Fatalf("expected clean line start, got %q", out[:min(len(out), 30)])
	}
	notice := formatTruncationNotice(meta)
	if !strings.Contains(notice, "Showing lines ") || !strings.Contains(notice, " of 30 (") {
		t.Fatalf("unexpected truncation notice: %q", notice)
	}
}

// --- Gate (plan mode) and empty session ID tests ---

func TestEvalChecksGateAndRejectsEmptySessionID(t *testing.T) {
	gate.ResetForTests()
	t.Cleanup(gate.ResetForTests)

	var seen gate.Request
	gate.SetPolicy(func(_ sdk.Context, r gate.Request) error {
		seen = r
		return errors.New("plan mode forbids eval execution")
	})

	ext := sdk.New("test-eval-gate")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)
	h := startHost(t, ext, hostOptions{})

	_, errMsg := h.callTool("eval", map[string]any{"language": "py", "code": "print(1)", "title": "my-cell"})
	if !strings.Contains(errMsg, "plan mode forbids eval execution") {
		t.Fatalf("expected gate denial, got %q", errMsg)
	}
	if seen.Tool != "eval" || seen.Op != gate.OpExec || seen.Detail != "my-cell" {
		t.Fatalf("unexpected gate request: %+v", seen)
	}

	// Empty session ID must fail instead of falling back to a shared "default" session.
	gate.ResetForTests()
	ext2 := sdk.New("test-eval-nosid")
	registerWithManager(ext2, mgr)
	h2 := startHost(t, ext2, hostOptions{EmptySessionID: true})
	_, errMsg2 := h2.callTool("eval", map[string]any{"language": "py", "code": "print(1)"})
	if !strings.Contains(errMsg2, "session id") {
		t.Fatalf("expected session id error, got %q", errMsg2)
	}
}

// --- Multi-session isolation, lifecycle.End, and Register called twice ---

func TestTwoSessionsLifecycleEndAndRegisterTwice(t *testing.T) {
	if !hasPython3(t) {
		t.Skip("python3 3.9+ not found on PATH; skipping multi-session lifecycle test")
	}
	ResetPreludesForTests()
	lifecycle.ResetForTests()
	t.Cleanup(func() {
		ResetPreludesForTests()
		lifecycle.ResetForTests()
		defaultManager.ShutdownAll()
	})

	// Simulate PiG creating two fresh extension instances in one process, calling Register twice.
	ext1 := sdk.New("oh-my-pig-s1")
	Register(ext1)
	ext2 := sdk.New("oh-my-pig-s2")
	Register(ext2)

	h1 := startHost(t, ext1, hostOptions{SessionID: "sess-A"})
	h2 := startHost(t, ext2, hostOptions{SessionID: "sess-B"})

	raw1, err1 := h1.callTool("eval", map[string]any{"language": "py", "code": "import os; v = 'A'; print(os.getpid())"})
	if err1 != "" {
		t.Fatalf("sess-A cell 1 failed: %s", err1)
	}
	txt1, _, _, _ := decodeToolResult(t, raw1)
	pidA := parseIntOrZero(txt1)

	raw2, err2 := h2.callTool("eval", map[string]any{"language": "py", "code": "import os; v = 'B'; print(os.getpid())"})
	if err2 != "" {
		t.Fatalf("sess-B cell 1 failed: %s", err2)
	}
	txt2, _, _, _ := decodeToolResult(t, raw2)
	pidB := parseIntOrZero(txt2)

	if pidA <= 0 || pidB <= 0 || pidA == pidB {
		t.Fatalf("expected distinct live python PIDs for sess-A (%d) and sess-B (%d)", pidA, pidB)
	}

	// End sess-A via lifecycle.End (twice — must be idempotent) — must kill ONLY sess-A,
	// proving Register called twice still routes lifecycle.End for the FIRST instance's session.
	lifecycle.End("sess-A")
	lifecycle.End("sess-A")
	lifecycle.End("") // empty id must be a no-op

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && isProcessAlive(pidA) {
		time.Sleep(25 * time.Millisecond)
	}
	if isProcessAlive(pidA) {
		t.Fatalf("sess-A kernel pid %d survived lifecycle.End", pidA)
	}
	if !isProcessAlive(pidB) {
		t.Fatalf("sess-B kernel pid %d was killed when sess-A ended", pidB)
	}

	// sess-B still has its variable `v == 'B'` intact.
	raw2b, err2b := h2.callTool("eval", map[string]any{"language": "py", "code": "print(v)"})
	if err2b != "" {
		t.Fatalf("sess-B cell 2 failed: %s", err2b)
	}
	txt2b, _, _, isErr2b := decodeToolResult(t, raw2b)
	if isErr2b || strings.TrimSpace(txt2b) != "B" {
		t.Fatalf("expected sess-B to retain v='B', got err=%v text=%q", isErr2b, txt2b)
	}

	// Cleanup sess-B via session_shutdown event.
	_, _ = h2.emitEvent(sdk.EventSessionShutdown, map[string]any{"reason": "quit"})
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && isProcessAlive(pidB) {
		time.Sleep(25 * time.Millisecond)
	}
	if isProcessAlive(pidB) {
		t.Fatalf("sess-B kernel pid %d survived session_shutdown", pidB)
	}
}

// --- Agent / Workpool host-only prelude routing tests ---

func TestAgentAndWorkpoolPreludeRoutingAndNoReservedConflict(t *testing.T) {
	if !hasPython3(t) {
		t.Skip("python3 3.9+ not found on PATH; skipping agent/workpool prelude routing test")
	}
	ResetPreludesForTests()
	jobs.ResetForTests()
	t.Cleanup(func() {
		ResetPreludesForTests()
		jobs.ResetForTests()
	})

	// Register `agent` and `workpool` preludes with Exports matching internal/agents.
	// Must NOT conflict with pyReservedPreludeExports, and `__agent__`/`__workpool__`
	// must route directly to their Invoke functions.
	RegisterPrelude(Prelude{
		Name:    "agent",
		Exports: []string{"agent"},
		Invoke: func(call PreludeCall, params json.RawMessage) (*PreludeResult, error) {
			var p map[string]any
			_ = json.Unmarshal(params, &p)
			return &PreludeResult{
				Details: map[string]any{"id": "scout_1", "agent": "scout", "echoPrompt": p["prompt"]},
			}, nil
		},
	})
	RegisterPrelude(Prelude{
		Name:    "workpool",
		Exports: []string{"workpool"},
		Invoke: func(_ PreludeCall, params json.RawMessage) (*PreludeResult, error) {
			var p map[string]any
			_ = json.Unmarshal(params, &p)
			switch p["op"] {
			case "create":
				return &PreludeResult{
					Details: map[string]any{"name": "pool_1", "agent": "task", "limit": 2},
				}, nil
			case "push":
				return &PreludeResult{
					Details: map[string]any{"ids": []string{"item_1"}},
				}, nil
			}
			return &PreludeResult{Details: map[string]any{"ok": true}}, nil
		},
	})

	ext := sdk.New("test-eval-agents")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)
	h := startHost(t, ext, hostOptions{SessionID: "agent-route-sess"})

	// Python: omp's built-in `agent()` and `workpool()` helpers route to the registered Invoke.
	rawPy, errPy := h.callTool("eval", map[string]any{
		"language": "py",
		"code": `
h = agent("inspect repo")
pool = workpool()
ids = pool.push("task-1")
print(h.id, h.agent, pool.name, ids)
`,
	})
	if errPy != "" {
		t.Fatalf("python agent/workpool error: %s", errPy)
	}
	txtPy, _, _, isErrPy := decodeToolResult(t, rawPy)
	if isErrPy || strings.TrimSpace(txtPy) != "scout_1 scout pool_1 ['item_1']" {
		t.Fatalf("unexpected python agent/workpool output: err=%v text=%q", isErrPy, txtPy)
	}

	// JS: omp's built-in `await agent()` and `await workpool()` also route to the same Invoke.
	if hasJSRuntime(t) {
		rawJS, errJS := h.callTool("eval", map[string]any{
			"language": "js",
			"code": `
const h = await agent("inspect repo");
const pool = await workpool();
const ids = await pool.push("task-1");
print(h.id, h.agent, pool.name, JSON.stringify(ids));
`,
		})
		if errJS != "" {
			t.Fatalf("js agent/workpool error: %s", errJS)
		}
		txtJS, _, _, isErrJS := decodeToolResult(t, rawJS)
		if isErrJS || strings.TrimSpace(txtJS) != `scout_1 scout pool_1 ["item_1"]` {
			t.Fatalf("unexpected js agent/workpool output: err=%v text=%q", isErrJS, txtJS)
		}
	}
}

// --- Bridged tool failures & policy tests ---

// upstream: packages/coding-agent/src/eval/js/tool-bridge.ts:97-105, 339-341, 428-434
func TestBridgedToolFailuresRaiseExceptionsAndBlockedToolsRefused(t *testing.T) {
	if !hasPython3(t) {
		t.Skip("python3 3.9+ not found on PATH; skipping bridged tool failure test")
	}
	ext := sdk.New("test-eval-tool-err")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)

	h := startHost(t, ext, hostOptions{
		OnExecTool: func(name string, args map[string]any) sdk.AgentToolCallOutcome {
			if name == "soft_err" {
				return sdk.AgentToolCallOutcome{
					ToolCall: sdk.ToolCall{Type: "toolCall", ID: "tc-soft", Name: name, Arguments: args},
					Result: sdk.AgentToolResult{
						Content: []sdk.ToolResultContent{{Type: "text", Text: "soft fail"}},
						Details: map[string]any{"isError": true, "exitCode": 2},
						IsError: true,
					},
					IsError: true,
				}
			}
			// Simulate unknown tool / validation error / thrown tool error folded into outcome.IsError.
			return sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: "tc-err", Name: name, Arguments: args},
				Result: sdk.AgentToolResult{
					Content: []sdk.ToolResultContent{{Type: "text", Text: "unknown tool: " + name}},
					IsError: true,
				},
				IsError: true,
			}
		},
	})

	raw, errMsg := h.callTool("eval", map[string]any{
		"language": "py",
		"code": `
caught_nope = None
try:
    await tool.nope()
except Exception as e:
    caught_nope = str(e)

caught_blocked = None
try:
    await tool.checkpoint()
except Exception as e:
    caught_blocked = str(e)

soft = await tool.soft_err()
print(caught_nope, "|", caught_blocked, "|", soft.get("hasError"), soft.get("text"))
`,
	})
	if errMsg != "" {
		t.Fatalf("eval error: %s", errMsg)
	}
	text, _, _, isErr := decodeToolResult(t, raw)
	if isErr {
		t.Fatalf("expected cell to catch exceptions, got error: %s", text)
	}
	if !strings.Contains(text, "unknown tool: nope") {
		t.Fatalf("expected tool.nope() to raise, got %q", text)
	}
	if !strings.Contains(text, "cannot run through the eval bridge") {
		t.Fatalf("expected checkpoint policy rejection, got %q", text)
	}
	if !strings.Contains(text, "True soft fail") {
		t.Fatalf("expected soft_err with details.isError to return hasError=True, got %q", text)
	}
}

// --- Live Python kernel tests ---

// upstream: packages/coding-agent/test/eval/python-eval-state.test.ts
func TestPythonStatePersistenceResetAndShutdown(t *testing.T) {
	if !hasPython3(t) {
		t.Skip("python3 3.9+ not found on PATH; skipping live Python kernel test")
	}
	ResetPreludesForTests()
	t.Cleanup(ResetPreludesForTests)

	ext := sdk.New("test-eval-py")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)

	cwd := t.TempDir()
	h := startHost(t, ext, hostOptions{Cwd: cwd})

	raw1, errMsg := h.callTool("eval", map[string]any{
		"language": "py",
		"code":     "import os\nx = 40\nprint(os.getpid())",
		"title":    "init",
	})
	if errMsg != "" {
		t.Fatalf("cell 1 error: %s", errMsg)
	}
	text1, _, details1, isErr1 := decodeToolResult(t, raw1)
	if isErr1 {
		t.Fatalf("cell 1 failed: %s", text1)
	}
	if details1.Language != LangPython || len(details1.Cells) != 1 || details1.Cells[0].Status != "complete" {
		t.Fatalf("unexpected details1: %+v", details1)
	}
	pid1 := parseIntOrZero(text1)
	if pid1 <= 0 {
		t.Fatalf("expected positive python pid, got %q", text1)
	}

	raw2, errMsg := h.callTool("eval", map[string]any{
		"language": "py",
		"code":     "import asyncio\nawait asyncio.sleep(0.01)\nx + 2",
	})
	if errMsg != "" {
		t.Fatalf("cell 2 error: %s", errMsg)
	}
	text2, _, _, isErr2 := decodeToolResult(t, raw2)
	if isErr2 || strings.TrimSpace(text2) != "42" {
		t.Fatalf("expected 42 from persisted state + top-level await, got err=%v text=%q", isErr2, text2)
	}

	raw3, errMsg := h.callTool("eval", map[string]any{
		"language": "py",
		"code":     "import os\nprint('x' in globals(), os.getpid())",
		"reset":    true,
	})
	if errMsg != "" {
		t.Fatalf("cell 3 error: %s", errMsg)
	}
	text3, _, _, isErr3 := decodeToolResult(t, raw3)
	if isErr3 || !strings.HasPrefix(strings.TrimSpace(text3), "False ") {
		t.Fatalf("expected reset to wipe x, got err=%v text=%q", isErr3, text3)
	}
	pid2 := parseIntOrZero(strings.Fields(text3)[1])
	if pid2 == pid1 || pid2 <= 0 {
		t.Fatalf("expected new pid after reset, got old=%d new=%d", pid1, pid2)
	}

	_, _ = h.emitEvent(sdk.EventSessionShutdown, map[string]any{"reason": "quit"})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && isProcessAlive(pid2) {
		time.Sleep(25 * time.Millisecond)
	}
	if isProcessAlive(pid2) {
		t.Fatalf("python kernel pid %d survived session shutdown", pid2)
	}
}

// upstream: packages/coding-agent/test/eval/kernel-restart-notice.test.ts: "py: the first cell after the runtime is killed between cells says its state is gone"
func TestPythonKernelMidExecDeathAndRestartNotice(t *testing.T) {
	if !hasPython3(t) {
		t.Skip("python3 3.9+ not found on PATH; skipping Python kernel death test")
	}
	ext := sdk.New("test-eval-py-death")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)
	h := startHost(t, ext, hostOptions{})

	// Kill the Python process from inside the cell to test mid-exec kernel death text.
	raw1, err1 := h.callTool("eval", map[string]any{
		"language": "py",
		"code":     "import os; os._exit(9)",
	})
	if err1 != "" {
		t.Fatalf("cell 1 error: %s", err1)
	}
	text1, _, details1, isErr1 := decodeToolResult(t, raw1)
	if !isErr1 {
		t.Fatalf("expected error on kernel death, got ok: %q", text1)
	}
	wantNotice := "[Kernel died during execution; completion is uncertain. The cell was not replayed; check for partial side effects before retrying.]"
	if !strings.HasPrefix(text1, wantNotice+"\n") {
		t.Fatalf("expected bracketed mid-exec death notice first, got %q", text1)
	}
	if !strings.Contains(text1, "[kernel] Python kernel exited with code 9") {
		t.Fatalf("expected [kernel] exit prefix after notice, got %q", text1)
	}
	if len(details1.Cells) != 1 || details1.Cells[0].ExitCode != nil {
		t.Fatalf("expected nil exitCode on cancelled mid-exec death, got %+v", details1.Cells)
	}

	// Next cell should announce exact omp Python kernel restart notice.
	raw2, err2 := h.callTool("eval", map[string]any{
		"language": "py",
		"code":     "print('alive')",
	})
	if err2 != "" {
		t.Fatalf("cell 2 error: %s", err2)
	}
	text2, _, _, isErr2 := decodeToolResult(t, raw2)
	if isErr2 || !strings.HasPrefix(text2, "Kernel restarted: the previous Python kernel is dead; state is unavailable. Re-run needed setup") {
		t.Fatalf("expected exact Python restart notice, got err=%v text=%q", isErr2, text2)
	}
}

// upstream: packages/coding-agent/test/eval-preludes.test.ts: "re-resolves live enablement so a captured call loses access when disabled"
func TestPythonPreludeToolShImagesAndLiveUpdates(t *testing.T) {
	if !hasPython3(t) {
		t.Skip("python3 3.9+ not found on PATH; skipping Python bridge test")
	}
	ResetPreludesForTests()
	t.Cleanup(ResetPreludesForTests)

	var invokedCount atomic.Int32
	pngBytes := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	pngB64 := base64.StdEncoding.EncodeToString(pngBytes)

	RegisterPrelude(Prelude{
		Name:          "fake_ext",
		Documentation: func() string { return "Fake prelude for round-trip testing." },
		Exports:       []string{"fake_ext"},
		Python: `
class _FakeExt:
    async def ping(self, msg):
        return await __omp_prelude__("fake_ext", {"msg": msg})
fake_ext = _FakeExt()
`,
		Invoke: func(call PreludeCall, parameters json.RawMessage) (*PreludeResult, error) {
			invokedCount.Add(1)
			var p struct {
				Msg string `json:"msg"`
			}
			_ = json.Unmarshal(parameters, &p)
			return &PreludeResult{
				Text:    "pong:" + p.Msg,
				Details: map[string]any{"lang": call.Language, "echo": p.Msg},
				Images:  []PreludeImage{{MimeType: "image/png", Data: pngB64}},
			}, nil
		},
		Status: func(_ json.RawMessage, res *PreludeResult) string {
			return "pinged:" + res.Text
		},
	})

	ext := sdk.New("test-eval-py-bridge")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)

	h := startHost(t, ext, hostOptions{
		OnExecTool: func(name string, args map[string]any) sdk.AgentToolCallOutcome {
			return sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: "tc-1", Name: name, Arguments: args},
				Result: sdk.AgentToolResult{
					Content: []sdk.ToolResultContent{{Type: "text", Text: "tool-reply:" + name + ":" + anyString(args["q"])}},
				},
			}
		},
	})

	raw, errMsg := h.callTool("eval", map[string]any{
		"language": "py",
		"code": `
import time
res = await fake_ext.ping("hello")
t_res = await tool.echo(q="world")
sh_out = sh("echo hi-from-sh").strip()
print(res["text"], res["details"]["lang"], t_res, sh_out, flush=True)
time.sleep(0.08)
`,
	})
	if errMsg != "" {
		t.Fatalf("eval error: %s", errMsg)
	}
	text, images, details, isErr := decodeToolResult(t, raw)
	if isErr {
		t.Fatalf("cell failed: %s", text)
	}
	if invokedCount.Load() != 1 {
		t.Fatalf("expected prelude invoked once, got %d", invokedCount.Load())
	}
	if !strings.Contains(text, "pong:hello py tool-reply:echo:world hi-from-sh") {
		t.Fatalf("unexpected output: %q", text)
	}
	if len(images) != 1 || images[0]["mimeType"] != "image/png" || images[0]["data"] != pngB64 {
		t.Fatalf("expected 1 png image attachment, got %+v", images)
	}
	if len(details.Images) != 1 {
		t.Fatalf("expected 1 image in details, got %+v", details.Images)
	}
	// Verify throttled live OnUpdate frames were delivered during execution.
	if len(h.getUpdates()) == 0 {
		t.Fatalf("expected at least one live tool_update frame during execution")
	}
}

// upstream: packages/coding-agent/test/tools/eval-timeout.test.ts: "bounds a compute cell (no agent/completion) by a plain wall-clock timeout"
func TestPythonTimeoutAndTimeoutPauseDuringPrelude(t *testing.T) {
	if !hasPython3(t) {
		t.Skip("python3 3.9+ not found on PATH; skipping Python timeout test")
	}
	ResetPreludesForTests()
	t.Cleanup(ResetPreludesForTests)

	RegisterPrelude(Prelude{
		Name:          "slow_host",
		Documentation: func() string { return "Sleeps on host." },
		Exports:       []string{"slow_host"},
		Python: `
async def slow_host():
    return await __omp_prelude__("slow_host", {})
`,
		Invoke: func(_ PreludeCall, _ json.RawMessage) (*PreludeResult, error) {
			time.Sleep(350 * time.Millisecond)
			return &PreludeResult{Text: "host-done"}, nil
		},
	})

	ext := sdk.New("test-eval-py-timeout")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	mgr.overrideTimeoutForTest = func(sec float64) time.Duration {
		if sec == 1 {
			return 180 * time.Millisecond
		}
		return time.Duration(sec * float64(time.Second))
	}
	registerWithManager(ext, mgr)
	h := startHost(t, ext, hostOptions{})

	// Warm up the kernel so cold Python startup does not eat into the 180ms test deadline.
	if _, werr := h.callTool("eval", map[string]any{"language": "py", "code": "pass", "timeout": 10}); werr != "" {
		t.Fatalf("warmup error: %s", werr)
	}

	raw1, errMsg := h.callTool("eval", map[string]any{
		"language": "py",
		"code":     "print(await slow_host())",
		"timeout":  1,
	})
	if errMsg != "" {
		t.Fatalf("slow_host error: %s", errMsg)
	}
	text1, _, _, isErr1 := decodeToolResult(t, raw1)
	if isErr1 || strings.TrimSpace(text1) != "host-done" {
		t.Fatalf("expected slow_host to complete while watchdog paused, got err=%v text=%q", isErr1, text1)
	}

	raw2, errMsg := h.callTool("eval", map[string]any{
		"language": "py",
		"code":     "import time; time.sleep(5); print('never')",
		"timeout":  1,
	})
	if errMsg != "" {
		t.Fatalf("timeout test error: %s", errMsg)
	}
	text2, _, details2, isErr2 := decodeToolResult(t, raw2)
	if !isErr2 {
		t.Fatalf("expected timeout cell to fail, got ok: %q", text2)
	}
	wantPrefix := "[eval cell timed out after 1s; kernel interrupted but remains running. Reset the kernel via { reset: true } if state appears corrupted.]\n"
	if !strings.HasPrefix(text2, wantPrefix) || strings.Contains(text2, "never") {
		t.Fatalf("expected bracketed timeout notice first before traceback, got %q", text2)
	}
	// KeyboardInterrupt traceback must follow the bracketed timeout notice.
	if !strings.Contains(text2[len(wantPrefix):], "KeyboardInterrupt") {
		t.Fatalf("expected KeyboardInterrupt preserved after timeout notice, got %q", text2)
	}
	if len(details2.Cells) != 1 || details2.Cells[0].ExitCode != nil {
		t.Fatalf("expected nil exitCode on timed-out cell, got %+v", details2.Cells)
	}
}

func TestPythonCancellation(t *testing.T) {
	if !hasPython3(t) {
		t.Skip("python3 3.9+ not found on PATH; skipping Python cancellation test")
	}
	ext := sdk.New("test-eval-py-cancel")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)
	h := startHost(t, ext, hostOptions{})

	_, errMsg := h.callTool("eval", map[string]any{"language": "py", "code": "x = 1"})
	if errMsg != "" {
		t.Fatalf("warmup failed: %s", errMsg)
	}

	reqID, ch := h.startToolCallAsync("eval", map[string]any{
		"language": "py",
		"code":     "import time; time.sleep(10)",
		"timeout":  30,
	})
	time.Sleep(150 * time.Millisecond)
	h.cancelRequest(reqID)

	select {
	case raw := <-ch:
		text, _, _, isErr := decodeToolResult(t, raw)
		if !isErr {
			t.Fatalf("expected cancelled cell to be error, got ok: %q", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("cancelled python cell did not settle within 5s")
	}
}

// upstream: packages/coding-agent/test/eval/helpers-local-roots.test.ts: "writes and reads local:// under the injected root"
func TestPercentLoadAndLocalURLHelpers(t *testing.T) {
	if !hasPython3(t) {
		t.Skip("python3 3.9+ not found on PATH; skipping %load test")
	}
	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)

	localDir := t.TempDir()
	scriptPath := filepath.Join(localDir, "my setup.py")
	if err := os.WriteFile(scriptPath, []byte("LOADED_VAL = 99\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := urls.Register(&fakeLocalHandler{root: localDir}); err != nil {
		t.Fatal(err)
	}

	ext := sdk.New("test-eval-load")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)
	h := startHost(t, ext, hostOptions{})

	raw1, errMsg := h.callTool("eval", map[string]any{
		"language": "py",
		"code":     `%load "local://my setup.py"`,
	})
	if errMsg != "" {
		t.Fatalf("load error: %s", errMsg)
	}
	_, _, _, isErr1 := decodeToolResult(t, raw1)
	if isErr1 {
		t.Fatalf("load cell failed")
	}

	raw2, errMsg := h.callTool("eval", map[string]any{
		"language": "py",
		"code":     "print(LOADED_VAL)",
	})
	if errMsg != "" {
		t.Fatalf("read loaded val error: %s", errMsg)
	}
	text2, _, _, isErr2 := decodeToolResult(t, raw2)
	if isErr2 || strings.TrimSpace(text2) != "99" {
		t.Fatalf("expected 99 from %%load local://, got err=%v text=%q", isErr2, text2)
	}

	// Write/read local:// and verify traversal is rejected.
	raw3, errMsg := h.callTool("eval", map[string]any{
		"language": "py",
		"code": `
write("local://note.txt", "abc")
got = read("local://note.txt")
trav = None
try:
    write("local://../escape.txt", "bad")
except Exception as e:
    trav = str(e)
print(got, "|", trav)
`,
	})
	if errMsg != "" {
		t.Fatalf("local helper error: %s", errMsg)
	}
	text3, _, _, isErr3 := decodeToolResult(t, raw3)
	if isErr3 || !strings.Contains(text3, "abc |") || !strings.Contains(strings.ToLower(text3), "unsafe") {
		t.Fatalf("expected abc and traversal rejection, got err=%v text=%q", isErr3, text3)
	}
	onDisk, err := os.ReadFile(filepath.Join(localDir, "note.txt"))
	if err != nil || string(onDisk) != "abc" {
		t.Fatalf("expected note.txt on disk with 'abc', got %q err=%v", string(onDisk), err)
	}
}

func TestPythonExplicitInterpreterSetting(t *testing.T) {
	if !hasPython3(t) {
		t.Skip("python3 3.9+ not found on PATH; skipping python.interpreter test")
	}
	pyPath, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}

	ext := sdk.New("test-eval-interp")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)

	h := startHost(t, ext, hostOptions{
		Settings: map[string]any{
			"python.interpreter": pyPath,
		},
	})
	raw, errMsg := h.callTool("eval", map[string]any{
		"language": "py",
		"code":     "import sys; print(sys.version_info.major)",
	})
	if errMsg != "" {
		t.Fatalf("eval error: %s", errMsg)
	}
	text, _, _, isErr := decodeToolResult(t, raw)
	if isErr || strings.TrimSpace(text) != "3" {
		t.Fatalf("expected 3, got err=%v text=%q", isErr, text)
	}
}

// --- Live JS kernel tests (including all reviewer-found JS rewriter/pipe bugs) ---

// upstream: packages/coding-agent/test/eval/console-output.test.ts: "returns nothing for a trailing assignment"
func TestJSRewriterStatementsExpressionsRegexAndSharedState(t *testing.T) {
	if !hasJSRuntime(t) {
		t.Skip("neither bun nor node 22+ found on PATH; skipping JS rewriter test")
	}
	ext := sdk.New("test-eval-js-rewrite")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)
	h := startHost(t, ext, hostOptions{})

	run := func(code string) (string, bool) {
		t.Helper()
		raw, errMsg := h.callTool("eval", map[string]any{"language": "js", "code": code})
		if errMsg != "" {
			t.Fatalf("eval error for %q: %s", code, errMsg)
		}
		text, _, _, isErr := decodeToolResult(t, raw)
		return text, isErr
	}

	// 1. Multi-line binary operator statement must NOT be corrupted by line-based echo rewrite.
	txt, isErr := run("const msg = \"a\" +\n  \"b\";")
	if isErr || txt != "(no output)" {
		t.Fatalf("multi-line concat should print nothing, got err=%v text=%q", isErr, txt)
	}
	txt, isErr = run("msg")
	if isErr || txt != "'ab'" && txt != "ab" {
		t.Fatalf("expected msg === 'ab', got err=%v text=%q", isErr, txt)
	}
	txt, isErr = run("var z1 = 0 ||\n  7")
	if isErr || txt != "(no output)" {
		t.Fatalf("multi-line || should print nothing, got err=%v text=%q", isErr, txt)
	}
	txt, isErr = run("z1")
	if isErr || txt != "7" {
		t.Fatalf("expected z1 === 7, got err=%v text=%q", isErr, txt)
	}

	// 2. Single-line `const k = 2; k * 21` and multi-line trailing expression echo.
	txt, isErr = run("const k = 2; k * 21")
	if isErr || txt != "42" {
		t.Fatalf("expected 42, got err=%v text=%q", isErr, txt)
	}
	txt, isErr = run("[1, 2, 3]\n  .map(x => x * 2)\n  .join(',')")
	if isErr || txt != "2,4,6" {
		t.Fatalf("expected multi-line expression echo 2,4,6, got err=%v text=%q", isErr, txt)
	}

	// 3. Compound & indexed assignments are suppressed (omp console-output.test.ts).
	txt, isErr = run("let q = 1; q += 41")
	if isErr || txt != "(no output)" {
		t.Fatalf("expected trailing compound assignment suppressed, got err=%v text=%q", isErr, txt)
	}
	txt, isErr = run("const arr = [0]; arr[0] = 5")
	if isErr || txt != "(no output)" {
		t.Fatalf("expected trailing indexed assignment suppressed, got err=%v text=%q", isErr, txt)
	}

	// 4. Function with nested `return` shares mutable state across cells!
	_, isErr = run("let count = 0;\nfunction inc() { count += 1; return count; }")
	if isErr {
		t.Fatalf("failed defining count/inc")
	}
	txt, isErr = run("inc();\ninc();\ncount")
	if isErr || txt != "2" {
		t.Fatalf("expected count === 2 after inc() twice, got err=%v text=%q", isErr, txt)
	}
	txt, isErr = run("count = 10;\ninc()")
	if isErr || txt != "11" {
		t.Fatalf("expected inc() === 11 after count = 10, got err=%v text=%q", isErr, txt)
	}

	// 5. Async cell globalization: multi-declarator, nested destructuring, nested var.
	_, isErr = run("const a4 = await Promise.resolve(1), b4 = 2;\nconst { x: { y5 } } = await Promise.resolve({ x: { y5: 5 } });\nif (true) { var z6 = await Promise.resolve(6); }")
	if isErr {
		t.Fatalf("async declarations cell failed")
	}
	txt, isErr = run("`${a4},${b4},${y5},${z6}`")
	if isErr || txt != "1,2,5,6" {
		t.Fatalf("expected 1,2,5,6 from async cell declarations, got err=%v text=%q", isErr, txt)
	}

	// 6. Regex literals containing quotes/brackets must not break subsequent const redeclaration.
	_, isErr = run("const re = /\"/g;\nconst b3 = 2;\nconst r9 = /[{]/;\nconst after9 = 1;")
	if isErr {
		t.Fatalf("regex literal cell failed")
	}
	txt, isErr = run("const b3 = 3; const after9 = 9; `${b3},${after9}`")
	if isErr || txt != "3,9" {
		t.Fatalf("expected redeclared consts after regex literals to yield 3,9, got err=%v text=%q", isErr, txt)
	}
}

// upstream: packages/coding-agent/test/eval/process-stdio-capture.test.ts: "routes process.stdout.write into the cell output with exact bytes and no added newline"
func TestJSProcessStdioAndProtocolPipeIsolation(t *testing.T) {
	if !hasJSRuntime(t) {
		t.Skip("neither bun nor node 22+ found on PATH; skipping JS stdio capture test")
	}
	ext := sdk.New("test-eval-js-stdio")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)
	h := startHost(t, ext, hostOptions{})

	raw1, err1 := h.callTool("eval", map[string]any{
		"language": "js",
		"code":     "process.stdout.write('a'); process.stdout.write('b'); process.stderr.write('c'); undefined;",
	})
	if err1 != "" {
		t.Fatalf("stdio write error: %s", err1)
	}
	txt1, _, _, isErr1 := decodeToolResult(t, raw1)
	if isErr1 || txt1 != "abc" {
		t.Fatalf("expected exact 'abc', got err=%v text=%q", isErr1, txt1)
	}

	if runtime.GOOS != "windows" {
		// Direct writeSync to fd 1 must be captured as cell output and must NOT spoof a done frame.
		raw2, err2 := h.callTool("eval", map[string]any{
			"language": "js",
			"code": `
const fs = require("node:fs");
fs.writeSync(1, "RAW-FD1\n");
fs.writeSync(1, '{"type":"done","id":"fake","ok":true}\n');
await new Promise(r => setTimeout(r, 30));
print("AFTER-RAW");
`,
		})
		if err2 != "" {
			t.Fatalf("raw fd1 error: %s", err2)
		}
		txt2, _, _, isErr2 := decodeToolResult(t, raw2)
		if isErr2 || !strings.Contains(txt2, "RAW-FD1") || !strings.Contains(txt2, "AFTER-RAW") {
			t.Fatalf("expected RAW-FD1 and AFTER-RAW both captured without breaking protocol, got err=%v text=%q", isErr2, txt2)
		}
	}
}

// upstream: packages/coding-agent/test/eval/console-table.test.ts: "emits a table view of the rendered box, with numeric columns flagged"
func TestJSConsoleTableAndJsonViews(t *testing.T) {
	if !hasJSRuntime(t) {
		t.Skip("neither bun nor node 22+ found on PATH; skipping JS console.table test")
	}
	ext := sdk.New("test-eval-js-table")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)
	h := startHost(t, ext, hostOptions{})

	raw, errMsg := h.callTool("eval", map[string]any{
		"language": "js",
		"code":     "console.table([{ name: 'Ada', score: -1.5e3 }, { name: 'Linus', score: NaN }]);",
	})
	if errMsg != "" {
		t.Fatalf("console.table error: %s", errMsg)
	}
	txt, _, details, isErr := decodeToolResult(t, raw)
	if isErr || !strings.Contains(txt, "Ada") || !strings.Contains(txt, "┌") {
		t.Fatalf("expected table box output, got err=%v text=%q", isErr, txt)
	}
	if len(details.Cells) != 1 || len(details.Cells[0].Views) != 1 {
		t.Fatalf("expected 1 table view in details, got %+v", details.Cells)
	}
}

// upstream: packages/coding-agent/test/eval/shell-helper.test.ts: "js: quotes template values, starts each command in the session cwd or `cwd`, and throws ShellError without backgrounding"
func TestJSShHelperQuotingCwdAndShellError(t *testing.T) {
	if !hasJSRuntime(t) || runtime.GOOS == "windows" {
		t.Skip("requires POSIX + bun/node")
	}
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	ext := sdk.New("test-eval-js-sh")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)
	h := startHost(t, ext, hostOptions{Cwd: cwd})

	raw, errMsg := h.callTool("eval", map[string]any{
		"language": "js",
		"code": `
const quoted = await sh` + "`printf '%s|' ${['a b', \"c'd\"]} ${'$HOME'}`" + `;
const subPwd = (await sh("pwd -P", { cwd: "sub" })).trim();
let failure;
try { await sh("echo boom; exit 3"); } catch (e) { failure = { name: e.name, code: e.exitCode, output: e.output }; }
print(JSON.stringify({ quoted, subEnd: subPwd.endsWith("/sub"), failure }));
`,
	})
	if errMsg != "" {
		t.Fatalf("sh helper error: %s", errMsg)
	}
	txt, _, _, isErr := decodeToolResult(t, raw)
	if isErr {
		t.Fatalf("sh cell failed: %s", txt)
	}
	var got struct {
		Quoted  string `json:"quoted"`
		SubEnd  bool   `json:"subEnd"`
		Failure struct {
			Name   string `json:"name"`
			Code   int    `json:"code"`
			Output string `json:"output"`
		} `json:"failure"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(txt)), &got); err != nil {
		t.Fatalf("decode result %q: %v", txt, err)
	}
	if got.Quoted != "a b|c'd|$HOME|" || !got.SubEnd || got.Failure.Name != "ShellError" || got.Failure.Code != 3 || got.Failure.Output != "boom\n" {
		t.Fatalf("unexpected sh result: %+v", got)
	}
}

// upstream: packages/coding-agent/test/eval/kernel-restart-notice.test.ts: "js: the first cell after the runtime is killed between cells says its state is gone"
func TestJSStateResetPreludeToolTimeoutAndExactRestartNotice(t *testing.T) {
	if !hasJSRuntime(t) {
		t.Skip("neither bun nor node 22+ found on PATH; skipping live JS kernel test")
	}
	ResetPreludesForTests()
	t.Cleanup(ResetPreludesForTests)

	var invoked atomic.Int32
	RegisterPrelude(Prelude{
		Name:          "fake_js",
		Documentation: func() string { return "Fake JS prelude." },
		Exports:       []string{"fake_js"},
		JavaScript: `
globalThis.fake_js = {
  greet: async name => await globalThis.__omp_prelude__("fake_js", { name }),
};
`,
		Invoke: func(call PreludeCall, parameters json.RawMessage) (*PreludeResult, error) {
			invoked.Add(1)
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(parameters, &p)
			return &PreludeResult{
				Text:    "hi " + p.Name,
				Details: map[string]any{"lang": call.Language},
			}, nil
		},
	})

	ext := sdk.New("test-eval-js")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	mgr.overrideTimeoutForTest = func(sec float64) time.Duration {
		if sec == 1 {
			return 200 * time.Millisecond
		}
		return time.Duration(sec * float64(time.Second))
	}
	registerWithManager(ext, mgr)

	h := startHost(t, ext, hostOptions{
		OnExecTool: func(name string, args map[string]any) sdk.AgentToolCallOutcome {
			return sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: "tc-js", Name: name, Arguments: args},
				Result: sdk.AgentToolResult{
					Content: []sdk.ToolResultContent{{Type: "text", Text: "js-tool:" + name + ":" + anyString(args["v"])}},
				},
			}
		},
	})

	raw1, errMsg := h.callTool("eval", map[string]any{
		"language": "js",
		"code":     "const x = 10; print(process.pid);",
	})
	if errMsg != "" {
		t.Fatalf("js cell 1 error: %s", errMsg)
	}
	text1, _, details1, isErr1 := decodeToolResult(t, raw1)
	if isErr1 || details1.Language != LangJS {
		t.Fatalf("js cell 1 failed: err=%v details=%+v text=%q", isErr1, details1, text1)
	}
	pid1 := parseIntOrZero(text1)
	if pid1 <= 0 {
		t.Fatalf("expected positive js pid, got %q", text1)
	}

	raw2, errMsg := h.callTool("eval", map[string]any{
		"language": "js",
		"code": `
const p = await fake_js.greet("omp");
const tRes = await tool.ping({ v: "ok" });
const sRes = (await sh("echo from-js-sh")).trim();
print(x + 5, p.text, p.details.lang, tRes, sRes);
`,
	})
	if errMsg != "" {
		t.Fatalf("js cell 2 error: %s", errMsg)
	}
	text2, _, _, isErr2 := decodeToolResult(t, raw2)
	if isErr2 || invoked.Load() != 1 || !strings.Contains(text2, "15 hi omp js js-tool:ping:ok from-js-sh") {
		t.Fatalf("unexpected js output: err=%v invoked=%d text=%q", isErr2, invoked.Load(), text2)
	}

	raw3, errMsg := h.callTool("eval", map[string]any{
		"language": "js",
		"code":     `print(typeof x, process.pid);`,
		"reset":    true,
	})
	if errMsg != "" {
		t.Fatalf("js cell 3 error: %s", errMsg)
	}
	text3, _, _, isErr3 := decodeToolResult(t, raw3)
	if isErr3 || !strings.HasPrefix(strings.TrimSpace(text3), "undefined ") {
		t.Fatalf("expected x undefined after reset, got err=%v text=%q", isErr3, text3)
	}
	pid2 := parseIntOrZero(strings.Fields(text3)[1])
	if pid2 == pid1 || pid2 <= 0 {
		t.Fatalf("expected new js pid after reset, old=%d new=%d", pid1, pid2)
	}

	// Timeout force-kills worker and reports omp's exact JS timeout text.
	raw4, errMsg := h.callTool("eval", map[string]any{
		"language": "js",
		"code":     "await new Promise(r => setTimeout(r, 5000)); print('never');",
		"timeout":  1,
	})
	if errMsg != "" {
		t.Fatalf("js cell 4 error: %s", errMsg)
	}
	text4, _, details4, isErr4 := decodeToolResult(t, raw4)
	if !isErr4 || !strings.Contains(text4, "The JS worker was force-killed and its VM state was reset; variables from earlier cells are gone.") || strings.Contains(text4, "never") {
		t.Fatalf("expected omp js timeout annotation, got err=%v text=%q", isErr4, text4)
	}
	if len(details4.Cells) != 1 || details4.Cells[0].ExitCode != nil {
		t.Fatalf("expected nil exitCode on js timeout, got %+v", details4.Cells)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && isProcessAlive(pid2) {
		time.Sleep(25 * time.Millisecond)
	}
	if isProcessAlive(pid2) {
		t.Fatalf("timed-out js worker pid %d still alive", pid2)
	}

	// Next cell announces omp's exact "JS worker" restart notice (not "JavaScript worker").
	raw5, errMsg := h.callTool("eval", map[string]any{
		"language": "js",
		"code":     "print(process.pid);",
	})
	if errMsg != "" {
		t.Fatalf("js cell 5 error: %s", errMsg)
	}
	text5, _, _, isErr5 := decodeToolResult(t, raw5)
	if isErr5 || !strings.HasPrefix(text5, "Kernel restarted: the previous JS worker is dead; state is unavailable. Re-run needed setup") {
		t.Fatalf("expected exact JS worker restart notice, got err=%v text=%q", isErr5, text5)
	}
	lines5 := strings.Split(strings.TrimSpace(text5), "\n")
	pid3 := parseIntOrZero(lines5[len(lines5)-1])

	_, _ = h.emitEvent(sdk.EventSessionShutdown, map[string]any{"reason": "quit"})
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && isProcessAlive(pid3) {
		time.Sleep(25 * time.Millisecond)
	}
	if isProcessAlive(pid3) {
		t.Fatalf("js worker pid %d survived session shutdown", pid3)
	}
}

func TestJSUnavailableReportsMessageAndPythonStillWorks(t *testing.T) {
	if !hasPython3(t) {
		t.Skip("python3 3.9+ not found on PATH; skipping JS-unavailable fallback test")
	}
	out, err := exec.Command("python3", "-c", "import sys; print(sys.executable)").Output()
	if err != nil {
		t.Skip("python3 not runnable")
	}
	pyPath := strings.TrimSpace(string(out))

	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)
	jsAvailMu.Lock()
	jsAvailCache = map[string]jsAvailability{}
	jsAvailMu.Unlock()

	ext := sdk.New("test-eval-no-js")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)

	h := startHost(t, ext, hostOptions{
		Settings: map[string]any{
			"python.interpreter": pyPath,
		},
	})

	_, errMsg := h.callTool("eval", map[string]any{
		"language": "js",
		"code":     "print(1)",
	})
	if !strings.Contains(errMsg, "JavaScript backend is unavailable in this session") {
		t.Fatalf("expected JS unavailable error, got %q", errMsg)
	}

	raw, pyErr := h.callTool("eval", map[string]any{
		"language": "py",
		"code":     "print(2 + 2)",
	})
	if pyErr != "" {
		t.Fatalf("expected python to still work when JS is unavailable, got %q", pyErr)
	}
	text, _, _, isErr := decodeToolResult(t, raw)
	if isErr || strings.TrimSpace(text) != "4" {
		t.Fatalf("expected 4 from python, got err=%v text=%q", isErr, text)
	}
}

// upstream: packages/coding-agent/src/eval/handle-bridge.ts (agentSnapshot with evalResult.data)
func TestWaitHelperWithJobsRegistryAndStructuredData(t *testing.T) {
	if !hasPython3(t) {
		t.Skip("python3 3.9+ not found on PATH; skipping wait() jobs bridge test")
	}
	jobs.ResetForTests()
	t.Cleanup(jobs.ResetForTests)

	ext := sdk.New("test-eval-wait")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)

	sid := "wait-session-1"
	h := startHost(t, ext, hostOptions{SessionID: sid})

	j := newFakeJob("agent_1", "finished-payload", map[string]any{"score": 99})
	jobs.Add(sid, j)
	go func() {
		time.Sleep(50 * time.Millisecond)
		j.complete()
	}()

	raw, errMsg := h.callTool("eval", map[string]any{
		"language": "py",
		"code":     `print(wait("agent://agent_1")[0]["score"])`,
	})
	if errMsg != "" {
		t.Fatalf("wait error: %s", errMsg)
	}
	text, _, _, isErr := decodeToolResult(t, raw)
	if isErr || strings.TrimSpace(text) != "99" {
		t.Fatalf("expected structured score 99 from wait(), got err=%v text=%q", isErr, text)
	}
}

// --- test helpers ---

func anyString(v any) string {
	s, _ := v.(string)
	return s
}

type fakeLocalHandler struct {
	root string
}

func (f *fakeLocalHandler) Scheme() string { return "local" }
func (f *fakeLocalHandler) Spec() urls.Spec {
	return urls.Spec{Backing: urls.BackingFile, Write: &urls.WritePolicy{Via: "filesystem"}}
}
func (f *fakeLocalHandler) Resolve(_ urls.Context, u urls.URL) (*urls.Resource, error) {
	rel := strings.TrimPrefix(u.Host+u.Path, "/")
	data, err := os.ReadFile(filepath.Join(f.root, rel))
	if err != nil {
		return nil, err
	}
	return &urls.Resource{URL: u.Href, Content: string(data)}, nil
}
func (f *fakeLocalHandler) Locate(_ urls.Context, u urls.URL, opts urls.LocateOptions) (string, bool, error) {
	rel := strings.TrimPrefix(u.Host+u.Path, "/")
	if rel == "" || opts.Directory {
		return f.root, true, nil
	}
	return filepath.Join(f.root, rel), true, nil
}

type fakeJob struct {
	id      string
	result  string
	data    any
	started time.Time
	done    chan struct{}
	status  atomic.Value
}

func newFakeJob(id, result string, data any) *fakeJob {
	j := &fakeJob{id: id, result: result, data: data, started: time.Now(), done: make(chan struct{})}
	j.status.Store("running")
	return j
}
func (j *fakeJob) complete() {
	j.status.Store("completed")
	close(j.done)
}
func (j *fakeJob) ID() string            { return j.id }
func (j *fakeJob) Kind() jobs.Kind       { return jobs.KindAgent }
func (j *fakeJob) Label() string         { return j.id }
func (j *fakeJob) Started() time.Time    { return j.started }
func (j *fakeJob) Done() <-chan struct{} { return j.done }
func (j *fakeJob) Status() string        { return j.status.Load().(string) }
func (j *fakeJob) Output() string        { return j.result }
func (j *fakeJob) Result() string        { return j.result }
func (j *fakeJob) Cancel()               { j.status.Store("cancelled") }
func (j *fakeJob) Ended() time.Time      { return time.Time{} }
func (j *fakeJob) ErrorText() string     { return "" }
func (j *fakeJob) Command() string       { return "" }
func (j *fakeJob) Intent() string        { return "" }
func (j *fakeJob) SkipDelivery() bool    { return false }
func (j *fakeJob) Details() map[string]any {
	ev := map[string]any{"text": j.result}
	if j.data != nil {
		ev["data"] = j.data
	}
	return map[string]any{"evalResult": ev}
}

// Verifies that registering a prelude whose Enabled(ctx) dereferences ctx
// (via ctx.GetSettings()) BEFORE eval.Register neither panics at registration
// nor during pre-session PrepareLoadout, and renders the prelude link only for
// sessions where Enabled(ctx) returns true.
func TestRegisterDoesNotCallPreludeEnabledWithZeroContextAndRendersPerSession(t *testing.T) {
	ResetPreludesForTests()
	t.Cleanup(ResetPreludesForTests)

	var enabledCalls atomic.Int32
	RegisterPrelude(Prelude{
		Name:          "ctx_sensitive",
		Summary:       "Context-sensitive test prelude",
		Documentation: func() string { return "Context-sensitive test prelude\n\nFull docs." },
		Enabled: func(ctx sdk.Context) bool {
			enabledCalls.Add(1)
			// Dereferences ctx.ext; panics immediately if called with a zero sdk.Context.
			settings, err := ctx.GetSettings()
			if err != nil || settings == nil {
				return false
			}
			v, _ := settings["testPreludeEnabled"].(bool)
			return v
		},
	})

	// Register BEFORE starting any session; must not call Enabled(ctx) or panic.
	extEnabled := sdk.New("test-eval-reg-enabled")
	mgrEnabled := NewManager()
	t.Cleanup(mgrEnabled.ShutdownAll)
	registerWithManager(extEnabled, mgrEnabled)

	extDisabled := sdk.New("test-eval-reg-disabled")
	mgrDisabled := NewManager()
	t.Cleanup(mgrDisabled.ShutdownAll)
	registerWithManager(extDisabled, mgrDisabled)

	if got := enabledCalls.Load(); got != 0 {
		t.Fatalf("Register called prelude Enabled hook %d times, want 0", got)
	}

	// Session 1: prelude enabled via replicated settings.
	h1 := startHost(t, extEnabled, hostOptions{
		Settings: map[string]any{"testPreludeEnabled": true},
	})
	// PrepareLoadout before session_start must also never call Enabled with zero ctx.
	if _, errMsg := h1.prepareLoadout("eval", []string{"read", "eval"}); errMsg != "" {
		t.Fatalf("pre-session PrepareLoadout failed: %s", errMsg)
	}
	if got := enabledCalls.Load(); got != 0 {
		t.Fatalf("pre-session PrepareLoadout called Enabled %d times, want 0", got)
	}
	if _, errMsg := h1.emitEvent("session_start", nil); errMsg != "" {
		t.Fatalf("session_start (enabled) failed: %s", errMsg)
	}
	h1.mu.Lock()
	regDesc1 := h1.tools["eval"].Description
	h1.mu.Unlock()
	if !strings.Contains(regDesc1, "- `ctx_sensitive`: Context-sensitive test prelude → help://eval/ctx_sensitive") {
		t.Fatalf("expected enabled prelude in refreshed tool description:\n%s", regDesc1)
	}
	changes1, errMsg := h1.prepareLoadout("eval", []string{"read", "grep", "eval"})
	if errMsg != "" {
		t.Fatalf("PrepareLoadout (enabled) failed: %s", errMsg)
	}
	desc1 := changes1.Descriptions["eval"]
	if !strings.Contains(desc1, "- `ctx_sensitive`: Context-sensitive test prelude → help://eval/ctx_sensitive") {
		t.Fatalf("expected enabled prelude in PrepareLoadout description:\n%s", desc1)
	}
	if !strings.Contains(desc1, "Session tool names: read, eval, grep.") {
		t.Fatalf("expected enabled tool names in omp order in PrepareLoadout description:\n%s", desc1)
	}

	// Session 2: prelude disabled via replicated settings.
	h2 := startHost(t, extDisabled, hostOptions{
		Settings: map[string]any{"testPreludeEnabled": false},
	})
	if _, errMsg := h2.emitEvent("session_start", nil); errMsg != "" {
		t.Fatalf("session_start (disabled) failed: %s", errMsg)
	}
	h2.mu.Lock()
	regDesc2 := h2.tools["eval"].Description
	h2.mu.Unlock()
	if strings.Contains(regDesc2, "ctx_sensitive") {
		t.Fatalf("expected disabled prelude omitted from refreshed tool description:\n%s", regDesc2)
	}
	changes2, errMsg := h2.prepareLoadout("eval", []string{"read", "eval"})
	if errMsg != "" {
		t.Fatalf("PrepareLoadout (disabled) failed: %s", errMsg)
	}
	if strings.Contains(changes2.Descriptions["eval"], "ctx_sensitive") {
		t.Fatalf("expected disabled prelude omitted from PrepareLoadout description:\n%s", changes2.Descriptions["eval"])
	}
}

// Verifies that the eval tool schema (both at initial registration and after
// session_start re-registration, including narrowed variants) matches omp
// v19.1.1's captured schema content in internal/schemaorder/omp_schemas.json.
func TestEvalSchemaMatchesOmpContent(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "schemaorder", "omp_schemas.json"))
	if err != nil {
		t.Fatalf("read omp_schemas.json: %v", err)
	}
	var all map[string]struct {
		Parameters map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatalf("unmarshal omp_schemas.json: %v", err)
	}
	ompEntry, ok := all["eval"]
	if !ok {
		t.Fatalf("omp_schemas.json missing eval entry")
	}

	t.Setenv("PI_PY", "")
	t.Setenv("PI_JS", "")

	ext := sdk.New("test-eval-schema-content")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)
	h := startHost(t, ext, hostOptions{})

	assertMatchesOmp := func(label string, got map[string]any) {
		t.Helper()
		if !reflect.DeepEqual(got, ompEntry.Parameters) {
			gotJSON, _ := json.MarshalIndent(got, "", "  ")
			wantJSON, _ := json.MarshalIndent(ompEntry.Parameters, "", "  ")
			t.Fatalf("%s schema content mismatch:\n--- GOT ---\n%s\n--- WANT (omp) ---\n%s", label, gotJSON, wantJSON)
		}
	}

	// 1. Initial registration schema.
	h.mu.Lock()
	initialParams := h.tools["eval"].Parameters
	h.mu.Unlock()
	assertMatchesOmp("initial registration", initialParams)

	// 2. Default session_start re-registered schema.
	if _, errMsg := h.emitEvent("session_start", nil); errMsg != "" {
		t.Fatalf("session_start failed: %s", errMsg)
	}
	h.mu.Lock()
	reRegParams := h.tools["eval"].Parameters
	h.mu.Unlock()
	assertMatchesOmp("session_start re-registration", reRegParams)

	// 3. Narrowed session_start variant (eval.js=false, tools.maxTimeout=60) still carries additionalProperties: false.
	extNarrow := sdk.New("test-eval-schema-narrow")
	mgrNarrow := NewManager()
	t.Cleanup(mgrNarrow.ShutdownAll)
	registerWithManager(extNarrow, mgrNarrow)
	hNarrow := startHost(t, extNarrow, hostOptions{
		Settings: map[string]any{
			"eval":  map[string]any{"js": false},
			"tools": map[string]any{"maxTimeout": 60},
		},
	})
	if _, errMsg := hNarrow.emitEvent("session_start", nil); errMsg != "" {
		t.Fatalf("narrow session_start failed: %s", errMsg)
	}
	hNarrow.mu.Lock()
	narrowParams := hNarrow.tools["eval"].Parameters
	hNarrow.mu.Unlock()
	if ap, exists := narrowParams["additionalProperties"]; !exists || ap != false {
		t.Fatalf("narrowed session_start schema missing additionalProperties=false: %#v", narrowParams)
	}
}

// Verifies JS cell stack frames use omp's `js-cell-<uuid>.js` naming (eval/js/executor.ts:133).
func TestJSCellFilenameUsesUUID(t *testing.T) {
	if !hasJSRuntime(t) {
		t.Skip("neither bun nor node 22+ found on PATH; skipping JS filename test")
	}
	ext := sdk.New("test-eval-js-uuid")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)
	h := startHost(t, ext, hostOptions{})

	raw, errMsg := h.callTool("eval", map[string]any{
		"language": "js",
		"code":     "throw new Error('js_boom');",
	})
	if errMsg != "" {
		t.Fatalf("eval error: %s", errMsg)
	}
	text, _, _, isErr := decodeToolResult(t, raw)
	if !isErr {
		t.Fatalf("expected error cell, got ok: %q", text)
	}
	idx := strings.Index(text, "js-cell-")
	if idx < 0 {
		t.Fatalf("expected js-cell-<uuid>.js in stack trace, got:\n%s", text)
	}
	rest := text[idx+len("js-cell-"):]
	end := strings.Index(rest, ".js")
	if end < 0 {
		t.Fatalf("expected .js suffix after js-cell-, got:\n%s", text)
	}
	uuid := rest[:end]
	// RFC-4122 v4 UUID: 8-4-4-4-12 hex chars with version '4' and variant [89ab].
	if len(uuid) != 36 || uuid[8] != '-' || uuid[13] != '-' || uuid[18] != '-' || uuid[23] != '-' || uuid[14] != '4' {
		t.Fatalf("expected RFC-4122 v4 UUID in js-cell-<uuid>.js, got %q in:\n%s", uuid, text)
	}
	switch uuid[19] {
	case '8', '9', 'a', 'b':
	default:
		t.Fatalf("expected RFC-4122 variant [89ab] in UUID %q", uuid)
	}
}

// Verifies eval's `Session tool names` lists only enabled tools (advertised top-level
// tools followed by mounted tool commands) in omp's BUILTIN_TOOLS order, even when
// toolcmd keeps mounted tools (`ast_edit`, `debug`, `lsp`) and special commands
// (`resolve`, `reject`, `propose`, `report_issue`) in PiG's active `Declared` list
// for direct-call fallback dispatch.
// upstream: packages/coding-agent/src/session/session-tools.ts:894-901,964-975
func TestPrepareLoadoutListsOnlyEnabledSessionToolsInOmpOrder(t *testing.T) {
	prevMounted := MountedToolNames
	prevHidden := HiddenCommandNames
	t.Cleanup(func() {
		MountedToolNames = prevMounted
		HiddenCommandNames = prevHidden
	})
	MountedToolNames = func(sdk.Context) []string {
		// Deliberately out of omp order to verify sorting.
		return []string{"lsp", "debug", "ast_edit"}
	}
	HiddenCommandNames = func(sdk.Context) []string {
		// Mounted tools + special commands kept active in PiG for direct-call fallback.
		return []string{"ast_edit", "debug", "lsp", "resolve", "reject", "propose", "report_issue"}
	}

	ext := sdk.New("test-eval-enabled-tools")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)
	h := startHost(t, ext, hostOptions{})

	if _, errMsg := h.emitEvent("session_start", nil); errMsg != "" {
		t.Fatalf("session_start failed: %s", errMsg)
	}

	// Declared simulates PiG's active list with mounted & special tools still active
	// (kept for direct-call fallback and stripped from provider requests), plus
	// disabled tools (`ast_grep`, `github`) and blocked bridge tools (`checkpoint`, `yield`)
	// in Callable.
	declared := []string{
		"read", "bash", "edit", "ast_edit", "debug", "eval", "glob", "grep",
		"lsp", "task", "wait", "todo", "web_search", "write",
		"resolve", "reject", "propose", "report_issue",
	}
	callable := append(append([]string(nil), declared...), "ast_grep", "github", "checkpoint", "yield")
	changes, errMsg := h.prepareLoadoutWithDeclared("eval", declared, callable)
	if errMsg != "" {
		t.Fatalf("PrepareLoadout failed: %s", errMsg)
	}
	desc := changes.Descriptions["eval"]
	wantLine := "Session tool names: read, bash, edit, eval, glob, grep, task, wait, todo, web_search, write, ast_edit, debug, lsp."
	if !strings.Contains(desc, wantLine) {
		t.Fatalf("expected %q in eval description, got:\n%s", wantLine, desc)
	}
	// Description must stay identical whether or not mounted tools appear in Declared.
	topLevelOnly := []string{"read", "bash", "edit", "write", "grep", "glob", "todo", "web_search", "eval", "task", "wait"}
	changes2, errMsg := h.prepareLoadoutWithDeclared("eval", topLevelOnly, callable)
	if errMsg != "" {
		t.Fatalf("PrepareLoadout (top-level only) failed: %s", errMsg)
	}
	if changes2.Descriptions["eval"] != desc {
		t.Fatalf("eval description changed when mounted tools were active vs inactive:\n--- with mounted active ---\n%s\n--- top-level only ---\n%s", desc, changes2.Descriptions["eval"])
	}
	for _, forbidden := range []string{"ast_grep", "resolve", "reject", "github", "propose", "report_issue", "checkpoint", "yield"} {
		for _, line := range strings.Split(desc, "\n") {
			if strings.HasPrefix(line, "Session tool names:") && strings.Contains(line, forbidden) {
				t.Fatalf("disabled/special/blocked tool %q leaked into %q", forbidden, line)
			}
		}
	}
	// Verify <examples> block is also present on the PrepareLoadout description.
	if !strings.Contains(desc, "<examples>\n# Load a script with spaces without echoing its source\n<example>\neval(language=\"py\", code=\"%load \\\"scripts/my setup.py\\\"\")\n</example>\n</examples>") {
		t.Fatalf("expected omp <examples> block in PrepareLoadout description:\n%s", desc)
	}
}

// Verifies eval.Setting overrides ctx.GetSettings() both at session_start (description
// & JSON schema) and at call time (eval.py, eval.js, tools.maxTimeout, python.interpreter,
// python.kernelMode).
func TestEvalSettingHookControlsDescriptionSchemaAndExecution(t *testing.T) {
	prevSetting := Setting
	t.Cleanup(func() { Setting = prevSetting })

	store := map[string]any{
		"eval.py":           false,
		"eval.js":           true,
		"tools.maxTimeout":  7.0,
		"python.kernelMode": "per-call",
	}
	Setting = func(_ sdk.Context, key string) any {
		return store[key]
	}

	ext := sdk.New("test-eval-setting-hook")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)

	// Host settings deliberately disagree with Setting hook (eval.py=true, tools.maxTimeout=999).
	h := startHost(t, ext, hostOptions{
		Settings: map[string]any{
			"eval.py":          true,
			"tools.maxTimeout": 999,
		},
	})
	if _, errMsg := h.emitEvent("session_start", nil); errMsg != "" {
		t.Fatalf("session_start failed: %s", errMsg)
	}

	// 1. Description and schema reflect Setting hook (py=false, maxTimeout=7).
	h.mu.Lock()
	regTool := h.tools["eval"]
	h.mu.Unlock()
	if strings.Contains(regTool.Description, "Python: plain Python subprocess") {
		t.Fatalf("expected py=false from Setting hook to omit Python description:\n%s", regTool.Description)
	}
	if !strings.Contains(regTool.Description, "stops after 7s") {
		t.Fatalf("expected tools.maxTimeout=7 in description:\n%s", regTool.Description)
	}
	if strings.Contains(regTool.Description, "<examples>") {
		t.Fatalf("expected py-only <examples> omitted when eval.py=false via Setting hook:\n%s", regTool.Description)
	}
	props, _ := regTool.Parameters["properties"].(map[string]any)
	langProp, _ := props["language"].(map[string]any)
	enumVals, _ := langProp["enum"].([]any)
	if len(enumVals) != 1 || enumVals[0] != "js" {
		t.Fatalf("expected language enum [js] from Setting hook, got %#v", enumVals)
	}
	timeoutProp, _ := props["timeout"].(map[string]any)
	if desc, _ := timeoutProp["description"].(string); !strings.Contains(desc, "Configured tools.maxTimeout: 7s (effective ceiling 7s).") {
		t.Fatalf("expected tools.maxTimeout=7 in schema description, got %q", desc)
	}

	// 2. Call-time enforcement: py is disabled despite host Settings having eval.py=true.
	_, pyErr := h.callTool("eval", map[string]any{
		"language": "py",
		"code":     "print(1)",
	})
	if !strings.Contains(pyErr, "Python backend is disabled") {
		t.Fatalf("expected Python backend disabled via Setting hook, got %q", pyErr)
	}

	// 3. Call-time tools.maxTimeout=7 clamps timeout: 100 to 7s and emits clamp notice.
	if hasJSRuntime(t) {
		raw, jsErr := h.callTool("eval", map[string]any{
			"language": "js",
			"code":     "print('ok')",
			"timeout":  100,
		})
		if jsErr != "" {
			t.Fatalf("js call failed: %s", jsErr)
		}
		text, _, details, isErr := decodeToolResult(t, raw)
		if isErr {
			t.Fatalf("expected ok js result, got error: %q", text)
		}
		wantNotice := "Eval timeout clamped from 100s to 7s."
		if details.Notice != wantNotice || !strings.HasPrefix(text, wantNotice) {
			t.Fatalf("expected timeout clamp notice %q, got details.Notice=%q text=%q", wantNotice, details.Notice, text)
		}

		// 4. Mid-session override of eval.js=false takes effect immediately at call time.
		store["eval.js"] = false
		_, jsDisabledErr := h.callTool("eval", map[string]any{
			"language": "js",
			"code":     "print('blocked')",
		})
		if !strings.Contains(jsDisabledErr, "JavaScript backend is disabled") {
			t.Fatalf("expected JavaScript backend disabled after Setting flip, got %q", jsDisabledErr)
		}
	}
}

// Verifies Prelude.DocumentationFor receives each session's sdk.Context across two
// independent sessions for both the eval tool description (including PrepareLoadout)
// and `help://eval/<name>` resolution.
func TestPreludeDocumentationForPerSession(t *testing.T) {
	ResetPreludesForTests()
	t.Cleanup(ResetPreludesForTests)
	urls.ResetForTests()
	urls.ResetHelpTopicsForTests()
	t.Cleanup(func() {
		urls.ResetForTests()
		urls.ResetHelpTopicsForTests()
	})
	if err := urls.Register(urls.HelpHandler{}); err != nil {
		t.Fatalf("register help handler: %v", err)
	}

	RegisterPrelude(Prelude{
		Name: "sess_doc",
		DocumentationFor: func(c sdk.Context) string {
			id, err := c.SessionManager().GetSessionID()
			if err != nil {
				return "err-summary"
			}
			return "Summary for " + id + "\n\nFull docs for " + id
		},
	})

	var ctxA, ctxB sdk.Context
	extA := sdk.New("test-eval-docfor-a")
	extA.OnSessionStart(func(c sdk.Context, _ map[string]any) (any, error) {
		ctxA = c
		return nil, nil
	})
	mgrA := NewManager()
	t.Cleanup(mgrA.ShutdownAll)
	registerWithManager(extA, mgrA)

	extB := sdk.New("test-eval-docfor-b")
	extB.OnSessionStart(func(c sdk.Context, _ map[string]any) (any, error) {
		ctxB = c
		return nil, nil
	})
	mgrB := NewManager()
	t.Cleanup(mgrB.ShutdownAll)
	registerWithManager(extB, mgrB)

	hA := startHost(t, extA, hostOptions{SessionID: "sess-alpha"})
	hB := startHost(t, extB, hostOptions{SessionID: "sess-beta"})

	if _, errMsg := hA.emitEvent("session_start", nil); errMsg != "" {
		t.Fatalf("session_start A failed: %s", errMsg)
	}
	if _, errMsg := hB.emitEvent("session_start", nil); errMsg != "" {
		t.Fatalf("session_start B failed: %s", errMsg)
	}

	// Check eval description (registered + PrepareLoadout) for each session.
	hA.mu.Lock()
	regDescA := hA.tools["eval"].Description
	hA.mu.Unlock()
	hB.mu.Lock()
	regDescB := hB.tools["eval"].Description
	hB.mu.Unlock()

	wantLineA := "- `sess_doc`: Summary for sess-alpha → help://eval/sess_doc"
	wantLineB := "- `sess_doc`: Summary for sess-beta → help://eval/sess_doc"
	if !strings.Contains(regDescA, wantLineA) || strings.Contains(regDescA, "sess-beta") {
		t.Fatalf("session A registered description mismatch:\n%s", regDescA)
	}
	if !strings.Contains(regDescB, wantLineB) || strings.Contains(regDescB, "sess-alpha") {
		t.Fatalf("session B registered description mismatch:\n%s", regDescB)
	}

	changesA, errMsg := hA.prepareLoadout("eval", []string{"read", "eval"})
	if errMsg != "" {
		t.Fatalf("PrepareLoadout A failed: %s", errMsg)
	}
	changesB, errMsg := hB.prepareLoadout("eval", []string{"read", "eval"})
	if errMsg != "" {
		t.Fatalf("PrepareLoadout B failed: %s", errMsg)
	}
	if !strings.Contains(changesA.Descriptions["eval"], wantLineA) || strings.Contains(changesA.Descriptions["eval"], "sess-beta") {
		t.Fatalf("session A PrepareLoadout description mismatch:\n%s", changesA.Descriptions["eval"])
	}
	if !strings.Contains(changesB.Descriptions["eval"], wantLineB) || strings.Contains(changesB.Descriptions["eval"], "sess-alpha") {
		t.Fatalf("session B PrepareLoadout description mismatch:\n%s", changesB.Descriptions["eval"])
	}

	// Check help://eval/sess_doc resolves each session's own docs.
	resA, err := urls.Resolve(urls.Context{SDK: ctxA}, "help://eval/sess_doc")
	if err != nil {
		t.Fatalf("help://eval/sess_doc for session A failed: %v", err)
	}
	resB, err := urls.Resolve(urls.Context{SDK: ctxB}, "help://eval/sess_doc")
	if err != nil {
		t.Fatalf("help://eval/sess_doc for session B failed: %v", err)
	}
	if resA.Content != "Summary for sess-alpha\n\nFull docs for sess-alpha" {
		t.Fatalf("session A help://eval/sess_doc = %q", resA.Content)
	}
	if resB.Content != "Summary for sess-beta\n\nFull docs for sess-beta" {
		t.Fatalf("session B help://eval/sess_doc = %q", resB.Content)
	}
}

// upstream: packages/coding-agent/src/tools/eval.ts:184-210,348,
// packages/coding-agent/src/prompts/tools/eval.md:75-80,
// packages/coding-agent/src/session/session-tools.ts:2070-2141
func TestFirstCallPreludeDescriptionAndOneTimeGuideInjection(t *testing.T) {
	ResetPreludesForTests()
	t.Cleanup(ResetPreludesForTests)

	RegisterPrelude(Prelude{
		Name: "browser",
		Documentation: func() string {
			return "Drive real browser tabs; start with `const tab = await browser.open({ name, url })`.\n\nFull docs."
		},
		FirstCallDocumentation: "Short browser guide.",
		JavaScript:             "globalThis.browser = { open: (args) => __omp_prelude__('browser', args) };",
		Python:                 "class _B:\n  def open(self, **kw):\n    return _omp_prelude('browser', kw)\nbrowser = _B()\n",
		Exports:                []string{"browser"},
		Invoke: func(_ PreludeCall, _ json.RawMessage) (*PreludeResult, error) {
			return &PreludeResult{Text: `{"ok":true}`}, nil
		},
	})

	opts := DefaultDescriptionOptions(EnabledPreludes(sdk.Context{}))
	if !opts.FirstCallGuides {
		t.Fatalf("DefaultDescriptionOptions must enable FirstCallGuides")
	}
	desc := RenderDescription(opts)
	wantHeader := "No `read` needed before first use; the guide follows the first call's result (full reference linked):"
	wantLine := "- `browser`: Drive real browser tabs; start with `const tab = await browser.open({ name, url })`. → `help://eval/browser`"
	if !strings.Contains(desc, wantHeader+"\n"+wantLine) {
		t.Fatalf("eval description missing first-call prelude block:\n%s", desc)
	}
}

// TestPreludeApprovalForDispatch verifies that ApprovalFor dynamically selects
// the approval tier per invocation (e.g. "read" vs "exec") at the eval bridge,
// and blocks execution without running Invoke when denied.
func TestPreludeApprovalForDispatch(t *testing.T) {
	ResetPreludesForTests()
	t.Cleanup(ResetPreludesForTests)

	prevSetting := approval.Setting
	prevSession := approval.SessionSetting
	t.Cleanup(func() {
		approval.Setting = prevSetting
		approval.SessionSetting = prevSession
	})
	// Mode "write" allows tier "read" and "write" and prompts (failing closed headless) on "exec".
	settingFn := func(_ sdk.Context, key string) any {
		if key == "tools.approvalMode" {
			return "write"
		}
		return nil
	}
	approval.Setting = settingFn
	approval.SessionSetting = settingFn

	invoked := 0
	RegisterPrelude(Prelude{
		Name: "dynapp",
		ApprovalFor: func(parameters json.RawMessage) string {
			var m map[string]any
			if err := json.Unmarshal(parameters, &m); err != nil {
				return "exec"
			}
			if act, _ := m["action"].(string); act == "read_op" {
				return "read"
			}
			return "exec"
		},
		Invoke: func(_ PreludeCall, _ json.RawMessage) (*PreludeResult, error) {
			invoked++
			return &PreludeResult{Text: "ok"}, nil
		},
	})

	run := &bridgeRun{
		ctx:      context.Background(),
		language: "py",
	}

	// Read-tier action passes under read-only mode.
	val, err := dispatchPreludeCall(run, json.RawMessage(`{"name":"dynapp","parameters":{"action":"read_op"}}`))
	if err != nil {
		t.Fatalf("read_op failed under read-only mode: %v", err)
	}
	if val != "ok" || invoked != 1 {
		t.Fatalf("got val=%v invoked=%d, want ok / 1", val, invoked)
	}

	// Exec-tier action is blocked under read-only mode (headless -> prompt error) and never calls Invoke.
	if _, err := dispatchPreludeCall(run, json.RawMessage(`{"name":"dynapp","parameters":{"action":"commit"}}`)); err == nil {
		t.Fatalf("expected exec action to be denied/prompted under read-only mode")
	}
	if invoked != 1 {
		t.Fatalf("Invoke should not have run on blocked exec action; invoked=%d", invoked)
	}

	// Invalid JSON parameters default to exec tier and are also blocked.
	if _, err := dispatchPreludeCall(run, json.RawMessage(`{"name":"dynapp","parameters":"bad"}`)); err == nil {
		t.Fatalf("expected invalid params to be blocked under read-only mode")
	}
	if invoked != 1 {
		t.Fatalf("Invoke should not have run on invalid params; invoked=%d", invoked)
	}
}

// Verifies eval.tools.enabled defaults to true and gates `@tool`/`tool(fn)` in
// session_start description + help://eval/helpers when set to false.
func TestEvalToolsSettingDefaultAndDisable(t *testing.T) {
	prevSetting := Setting
	t.Cleanup(func() { Setting = prevSetting })

	store := map[string]any{}
	Setting = func(_ sdk.Context, key string) any {
		return store[key]
	}

	ext := sdk.New("test-eval-tools-setting")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)

	var capturedCtx sdk.Context
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		capturedCtx = ctx
		return nil, nil
	})

	h := startHost(t, ext, hostOptions{})
	if _, errMsg := h.emitEvent("session_start", nil); errMsg != "" {
		t.Fatalf("session_start failed: %s", errMsg)
	}
	if !IsEvalToolsEnabled(capturedCtx) {
		t.Fatalf("expected IsEvalToolsEnabled default true")
	}
	h.mu.Lock()
	enabledDesc := h.tools["eval"].Description
	h.mu.Unlock()
	if !strings.Contains(enabledDesc, "`@tool`/`tool(fn)`") {
		t.Fatalf("expected default eval description to advertise @tool/tool(fn):\n%s", enabledDesc)
	}
	enabledTopics := DocTopics(descriptionOptionsForSession(capturedCtx, nil))
	if !strings.Contains(enabledTopics["helpers"], "@tool / tool(fn") {
		t.Fatalf("expected default helpers topic to include @tool docs:\n%s", enabledTopics["helpers"])
	}

	// Disable eval.tools.enabled=false and verify session_start description + helpers hide @tool.
	store["eval.tools.enabled"] = false
	if IsEvalToolsEnabled(capturedCtx) {
		t.Fatalf("expected IsEvalToolsEnabled false when setting disabled")
	}
	if _, errMsg := h.emitEvent("session_start", nil); errMsg != "" {
		t.Fatalf("second session_start failed: %s", errMsg)
	}
	h.mu.Lock()
	disabledDesc := h.tools["eval"].Description
	h.mu.Unlock()
	if strings.Contains(disabledDesc, "@tool") {
		t.Fatalf("expected disabled eval description to omit @tool:\n%s", disabledDesc)
	}
	disabledTopics := DocTopics(descriptionOptionsForSession(capturedCtx, nil))
	if strings.Contains(disabledTopics["helpers"], "@tool") {
		t.Fatalf("expected disabled helpers topic to omit @tool:\n%s", disabledTopics["helpers"])
	}
}

// upstream: packages/coding-agent/src/eval/py/executor.ts:539-577, py/runner.py:2798-2919
// Verifies Python @tool describe, sync & async calls, concurrent parallel calls,
// nested tool.read host bridge callback inside @tool, cancellation, and dead/reset kernel errors.
func TestPythonKernelEvalToolsEndToEnd(t *testing.T) {
	if !hasPython3(t) {
		t.Skip("python3 unavailable")
	}
	ext := sdk.New("test-py-eval-tools")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)

	var capturedCtx sdk.Context
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		capturedCtx = ctx
		return nil, nil
	})
	// Host proxy tool simulating a child custom tool handler whose sdk.Context has toolCallID.
	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "invoke_kernel_tool",
		Description: "test proxy",
		Parameters:  sdk.Schema{"type": "object", "properties": map[string]any{}},
		Execute: func(toolCtx sdk.Context, params map[string]any) (any, error) {
			lang, _ := params["lang"].(string)
			name, _ := params["name"].(string)
			args, _ := params["args"].(map[string]any)
			res, err := mgr.CallKernelToolWithBridgeContext(context.Background(), capturedCtx, toolCtx, lang, name, args)
			if err != nil {
				return nil, err
			}
			if !res.OK {
				return sdk.ToolResult{Content: res.Error, IsError: true}, nil
			}
			s, _ := res.Value.(string)
			return sdk.ToolResult{Content: s}, nil
		},
	})

	cwd := t.TempDir()
	h := startHost(t, ext, hostOptions{
		Cwd: cwd,
		OnExecTool: func(name string, args map[string]any) sdk.AgentToolCallOutcome {
			if name == "read" {
				p, _ := args["path"].(string)
				return sdk.AgentToolCallOutcome{
					Result: sdk.AgentToolResult{
						Content: []sdk.ToolResultContent{{Type: "text", Text: "bridged:" + p}},
					},
				}
			}
			return sdk.AgentToolCallOutcome{IsError: true, Result: sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: "unknown tool"}}}}
		},
	})
	if _, errMsg := h.emitEvent("session_start", nil); errMsg != "" {
		t.Fatalf("session_start failed: %s", errMsg)
	}

	// Before any Python cell runs, DescribeKernelTools and CallKernelTool fail with ErrKernelNotRunning.
	if _, err := mgr.DescribeKernelTools(context.Background(), capturedCtx, "py", []string{"add"}); !errors.Is(err, ErrKernelNotRunning) {
		t.Fatalf("expected ErrKernelNotRunning before start, got %v", err)
	}
	if _, err := mgr.CallKernelTool(context.Background(), capturedCtx, "py", "add", nil); !errors.Is(err, ErrKernelNotRunning) {
		t.Fatalf("expected ErrKernelNotRunning on call before start, got %v", err)
	}

	defineCode := strings.Join([]string{
		"import asyncio",
		"from typing import Annotated",
		"LOCK = asyncio.Lock()",
		"",
		"@tool",
		"def add(a: int, b: Annotated[int, 'second'] = 2) -> int:",
		"    '''Add two integers.'''",
		"    return a + b",
		"",
		"@tool(name='async_echo', description='Async echo tool')",
		"async def _echo(tag: str, delay: float = 0.02) -> dict:",
		"    async with LOCK:",
		"        await asyncio.sleep(delay)",
		"    return {'tag': tag, 'ok': True}",
		"",
		"@tool",
		"async def read_bridge(path: str) -> str:",
		"    res = await tool.read({'path': path})",
		"    return res['text'] if isinstance(res, dict) else res",
		"@tool",
		"def fail_tool() -> str:",
		"    raise ValueError('boom py')",
		"",
		"@tool",
		"async def slow_cancel() -> str:",
		"    await asyncio.sleep(30)",
		"    return 'done'",
	}, "\n")
	raw, errMsg := h.callTool("eval", map[string]any{"language": "py", "code": defineCode})
	if errMsg != "" {
		t.Fatalf("define cell failed: %s", errMsg)
	}
	text, _, _, isErr := decodeToolResult(t, raw)
	if isErr {
		t.Fatalf("define cell returned error: %s", text)
	}

	// Describe requested tools including a missing name.
	desc, err := mgr.DescribeKernelTools(context.Background(), capturedCtx, "py", []string{"add", "async_echo", "no_such"})
	if err != nil {
		t.Fatalf("DescribeKernelTools failed: %v", err)
	}
	if len(desc.Tools) != 2 || len(desc.Missing) != 1 || desc.Missing[0] != "no_such" {
		t.Fatalf("unexpected describe result: %+v", desc)
	}
	if desc.Tools[0].Name != "add" || desc.Tools[0].Description != "Add two integers." || desc.Tools[0].Language != LangPython {
		t.Fatalf("unexpected add descriptor: %+v", desc.Tools[0])
	}

	// Sync call.
	addRes, err := mgr.CallKernelTool(context.Background(), capturedCtx, "py", "add", map[string]any{"a": 40})
	if err != nil || !addRes.OK || addRes.Value != float64(42) {
		t.Fatalf("unexpected add result: res=%+v err=%v", addRes, err)
	}

	// Concurrent async calls sharing asyncio.Lock on _TOOL_LOOP.
	const workers = 5
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tag := "w" + string(rune('0'+i))
			r, callErr := mgr.CallKernelTool(context.Background(), capturedCtx, "py", "async_echo", map[string]any{"tag": tag})
			if callErr != nil || !r.OK {
				errs <- errors.New("async_echo failed: " + r.Error)
				return
			}
			m, _ := r.Value.(map[string]any)
			if m["tag"] != tag || m["ok"] != true {
				errs <- errors.New("async_echo payload mismatch")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}

	// Nested tool.read bridge callback from inside @tool (invoked with an active tool-call sdk.Context).
	bridgeRaw, bridgeErrMsg := h.callTool("invoke_kernel_tool", map[string]any{
		"lang": "py",
		"name": "read_bridge",
		"args": map[string]any{"path": "notes.txt"},
	})
	if bridgeErrMsg != "" {
		t.Fatalf("nested tool.read inside Python @tool failed: %s", bridgeErrMsg)
	}
	bridgeText, _, _, bridgeIsErr := decodeToolResult(t, bridgeRaw)
	if bridgeIsErr || bridgeText != "bridged:notes.txt" {
		t.Fatalf("nested tool.read inside Python @tool mismatch: isErr=%v text=%q", bridgeIsErr, bridgeText)
	}

	// Exception inside @tool becomes OK=false result without killing kernel.
	failRes, err := mgr.CallKernelTool(context.Background(), capturedCtx, "py", "fail_tool", nil)
	if err != nil || failRes.OK || !strings.Contains(failRes.Error, "boom py") {
		t.Fatalf("expected boom py error result, got res=%+v err=%v", failRes, err)
	}

	// Cancellation sends tool_cancel and returns promptly without killing kernel.
	cancelCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	cancelRes, err := mgr.CallKernelTool(cancelCtx, capturedCtx, "py", "slow_cancel", nil)
	if err != nil || cancelRes.OK || !strings.Contains(strings.ToLower(cancelRes.Error), "timed out") {
		t.Fatalf("expected timeout error result on cancelled async tool, got res=%+v err=%v", cancelRes, err)
	}
	// Kernel is still alive after tool_cancel.
	afterRes, err := mgr.CallKernelTool(context.Background(), capturedCtx, "py", "add", map[string]any{"a": 1, "b": 2})
	if err != nil || !afterRes.OK || afterRes.Value != float64(3) {
		t.Fatalf("kernel should stay alive after tool_cancel: res=%+v err=%v", afterRes, err)
	}

	// Resetting kernel drops previously defined tools.
	_, resetErr := h.callTool("eval", map[string]any{"language": "py", "code": "x = 1", "reset": true})
	if resetErr != "" {
		t.Fatalf("reset cell failed: %s", resetErr)
	}
	afterReset, err := mgr.DescribeKernelTools(context.Background(), capturedCtx, "py", []string{"add"})
	if err != nil || len(afterReset.Tools) != 0 || len(afterReset.Missing) != 1 {
		t.Fatalf("expected add missing after reset, got %+v err=%v", afterReset, err)
	}

	// Shutting down session causes ErrKernelNotRunning.
	mgr.ShutdownSession(h.opts.SessionID)
	if _, err := mgr.CallKernelTool(context.Background(), capturedCtx, "py", "add", nil); !errors.Is(err, ErrKernelNotRunning) {
		t.Fatalf("expected ErrKernelNotRunning after ShutdownSession, got %v", err)
	}
}

// upstream: packages/coding-agent/src/eval/js/worker-core.ts:421-475, context-manager.ts:244-313
// Verifies JS tool(fn, opts) describe, sync & async calls, concurrent calls while a cell is
// parked on an active bridge call (proving no processingQueue deadlock!), nested tool.read,
// exception handling, tool_cancel, and shutdown behavior.
func TestJSKernelEvalToolsEndToEndAndNoDeadlock(t *testing.T) {
	if !hasJSRuntime(t) {
		t.Skip("no bun or node 22+ runtime found")
	}
	ext := sdk.New("test-js-eval-tools")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)

	var capturedCtx sdk.Context
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		capturedCtx = ctx
		return nil, nil
	})
	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "invoke_kernel_tool",
		Description: "test proxy",
		Parameters:  sdk.Schema{"type": "object", "properties": map[string]any{}},
		Execute: func(toolCtx sdk.Context, params map[string]any) (any, error) {
			lang, _ := params["lang"].(string)
			name, _ := params["name"].(string)
			args, _ := params["args"].(map[string]any)
			timeoutMs, _ := params["timeoutMs"].(float64)
			callCtx := context.Background()
			var cancel context.CancelFunc
			if timeoutMs > 0 {
				callCtx, cancel = context.WithTimeout(callCtx, time.Duration(timeoutMs)*time.Millisecond)
				defer cancel()
			}
			res, err := mgr.CallKernelToolWithBridgeContext(callCtx, capturedCtx, toolCtx, lang, name, args)
			if err != nil {
				return nil, err
			}
			if !res.OK {
				return sdk.ToolResult{Content: res.Error, IsError: true}, nil
			}
			s, _ := res.Value.(string)
			return sdk.ToolResult{Content: s}, nil
		},
	})

	blockGate := make(chan struct{})
	inBlockedTool := make(chan struct{}, 1)
	cwd := t.TempDir()
	h := startHost(t, ext, hostOptions{
		Cwd: cwd,
		OnExecTool: func(name string, args map[string]any) sdk.AgentToolCallOutcome {
			switch name {
			case "read":
				p, _ := args["path"].(string)
				return sdk.AgentToolCallOutcome{
					Result: sdk.AgentToolResult{
						Content: []sdk.ToolResultContent{{Type: "text", Text: "js-bridged:" + p}},
					},
				}
			case "hold_cell":
				select {
				case inBlockedTool <- struct{}{}:
				default:
				}
				<-blockGate
				return sdk.AgentToolCallOutcome{
					Result: sdk.AgentToolResult{
						Content: []sdk.ToolResultContent{{Type: "text", Text: "released"}},
					},
				}
			}
			return sdk.AgentToolCallOutcome{IsError: true, Result: sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: "unknown"}}}}
		},
	})
	if _, errMsg := h.emitEvent("session_start", nil); errMsg != "" {
		t.Fatalf("session_start failed: %s", errMsg)
	}

	if _, err := mgr.DescribeKernelTools(context.Background(), capturedCtx, "js", []string{"mul"}); !errors.Is(err, ErrKernelNotRunning) {
		t.Fatalf("expected ErrKernelNotRunning before JS start, got %v", err)
	}

	defineCode := strings.Join([]string{
		"tool(function mul({ a, b }) { return a * b; }, {",
		"  description: 'Multiply two numbers',",
		"  parameters: { type: 'object', properties: { a: { type: 'number' }, b: { type: 'number' } }, required: ['a', 'b'] }",
		"});",
		"tool(async function readViaBridge({ path }) {",
		"  const res = await tool.read({ path });",
		"  return typeof res === 'string' ? res : res.text;",
		"});",
		"tool(function boom() { throw new Error('boom js'); });",
		"tool(function retUndef() {});",
		"tool(async function blockBridge() {",
		"  const res = await tool.hold_cell({});",
		"  return typeof res === 'string' ? res : res.text;",
		"});",
	}, "\n")
	raw, errMsg := h.callTool("eval", map[string]any{"language": "js", "code": defineCode})
	if errMsg != "" {
		t.Fatalf("JS define cell failed: %s", errMsg)
	}
	text, _, _, isErr := decodeToolResult(t, raw)
	if isErr {
		t.Fatalf("JS define cell returned error: %s", text)
	}

	desc, err := mgr.DescribeKernelTools(context.Background(), capturedCtx, "js", []string{"mul", "missing_js"})
	if err != nil {
		t.Fatalf("DescribeKernelTools js failed: %v", err)
	}
	if len(desc.Tools) != 1 || desc.Tools[0].Name != "mul" || desc.Tools[0].Language != LangJS || len(desc.Missing) != 1 || desc.Missing[0] != "missing_js" {
		t.Fatalf("unexpected JS describe: %+v", desc)
	}

	// Sync call + undefined return value + nested tool.read + thrown error.
	mulRes, err := mgr.CallKernelTool(context.Background(), capturedCtx, "js", "mul", map[string]any{"a": 6, "b": 7})
	if err != nil || !mulRes.OK || mulRes.Value != float64(42) {
		t.Fatalf("unexpected mul result: %+v err=%v", mulRes, err)
	}
	undefRes, err := mgr.CallKernelTool(context.Background(), capturedCtx, "js", "retUndef", nil)
	if err != nil || !undefRes.OK || undefRes.Value != nil {
		t.Fatalf("unexpected retUndef result: %+v err=%v", undefRes, err)
	}
	bridgeRaw, bridgeErrMsg := h.callTool("invoke_kernel_tool", map[string]any{
		"lang": "js",
		"name": "readViaBridge",
		"args": map[string]any{"path": "hello.md"},
	})
	if bridgeErrMsg != "" {
		t.Fatalf("nested tool.read inside JS tool() failed: %s", bridgeErrMsg)
	}
	bridgeText, _, _, bridgeIsErr := decodeToolResult(t, bridgeRaw)
	if bridgeIsErr || bridgeText != "js-bridged:hello.md" {
		t.Fatalf("nested tool.read inside JS tool() mismatch: isErr=%v text=%q", bridgeIsErr, bridgeText)
	}
	boomRes, err := mgr.CallKernelTool(context.Background(), capturedCtx, "js", "boom", nil)
	if err != nil || boomRes.OK || !strings.Contains(boomRes.Error, "boom js") {
		t.Fatalf("expected boom js error result, got %+v err=%v", boomRes, err)
	}

	// Start a JS cell that awaits a blocking host bridge call (simulating `await wait(agent(...))`).
	// While that cell is parked inside `runCell`, concurrent `tool` RPCs MUST complete immediately
	// without blocking on `processingQueue`!
	_, cellCh := h.startToolCallAsync("eval", map[string]any{
		"language": "js",
		"code":     "await tool.hold_cell({})",
	})
	select {
	case <-inBlockedTool:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for JS cell to enter hold_cell")
	}

	// Invoke mul AND readViaBridge concurrently while the outer JS cell is still running!
	var wg sync.WaitGroup
	concErrs := make(chan error, 4)
	for i := range 4 {
		n := i + 1
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := mgr.CallKernelTool(context.Background(), capturedCtx, "js", "mul", map[string]any{"a": n, "b": 10})
			if e != nil || !r.OK || r.Value != float64(n*10) {
				concErrs <- errors.New("concurrent mul failed during active cell")
			}
		}()
	}
	wg.Wait()
	close(concErrs)
	for e := range concErrs {
		t.Fatal(e)
	}
	concReadRaw, concReadErr := h.callTool("invoke_kernel_tool", map[string]any{
		"lang": "js",
		"name": "readViaBridge",
		"args": map[string]any{"path": "while-waiting.txt"},
	})
	if concReadErr != "" {
		t.Fatalf("concurrent JS tool bridge call during active cell failed: %s", concReadErr)
	}
	concReadText, _, _, concReadIsErr := decodeToolResult(t, concReadRaw)
	if concReadIsErr || concReadText != "js-bridged:while-waiting.txt" {
		t.Fatalf("concurrent JS tool bridge call mismatch: isErr=%v text=%q", concReadIsErr, concReadText)
	}

	// Release the blocked outer JS cell and confirm it finishes cleanly.
	close(blockGate)
	select {
	case cellRaw := <-cellCh:
		cellTxt, _, _, cellIsErr := decodeToolResult(t, cellRaw)
		if cellIsErr || !strings.Contains(cellTxt, "released") {
			t.Fatalf("unexpected outer JS cell result: isErr=%v text=%q", cellIsErr, cellTxt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for outer JS cell to finish")
	}

	// Verify tool_cancel aborts a pending JS tool call promptly and leaves kernel alive.
	blockGate = make(chan struct{})
	cancelRaw, cancelErrMsg := h.callTool("invoke_kernel_tool", map[string]any{
		"lang":      "js",
		"name":      "blockBridge",
		"timeoutMs": 150,
	})
	close(blockGate)
	if cancelErrMsg != "" {
		t.Fatalf("unexpected protocol error on cancelled JS tool call: %s", cancelErrMsg)
	}
	cancelText, _, _, cancelIsErr := decodeToolResult(t, cancelRaw)
	if !cancelIsErr || !strings.Contains(strings.ToLower(cancelText), "timed out") {
		t.Fatalf("expected timeout on cancelled JS tool call, got isErr=%v text=%q", cancelIsErr, cancelText)
	}
	aliveRes, err := mgr.CallKernelTool(context.Background(), capturedCtx, "js", "mul", map[string]any{"a": 2, "b": 3})
	if err != nil || !aliveRes.OK || aliveRes.Value != float64(6) {
		t.Fatalf("JS kernel should stay alive after tool_cancel: %+v err=%v", aliveRes, err)
	}
}
