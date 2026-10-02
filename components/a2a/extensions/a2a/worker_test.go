package a2aext

import (
	"context"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

var alice = Principal{Name: "alice", Tenant: "team-a"}

func newTestWorker(t *testing.T) (*ProcessWorker, string, string) {
	t.Helper()
	cfg, logPath, getenv := fakeWorkerConfig(t)
	state := t.TempDir()
	w, err := NewProcessWorker(cfg, state, getenv)
	if err != nil {
		t.Fatal(err)
	}
	return w, logPath, state
}

func TestSessionIDIsStableScopedAndSafe(t *testing.T) {
	hex := regexp.MustCompile(`^[0-9a-f]{32,64}$`)
	a := SessionID(alice, "ctx-1")
	if !hex.MatchString(a) {
		t.Fatalf("session id %q must be lowercase hex", a)
	}
	if a != SessionID(alice, "ctx-1") {
		t.Fatal("the same context must map to the same session")
	}
	if a == SessionID(alice, "ctx-2") {
		t.Fatal("different contexts must not share a session")
	}
	if a == SessionID(Principal{Name: "mallory", Tenant: "team-b"}, "ctx-1") {
		t.Fatal("the same context id under another tenant must not share a session")
	}
	if id := SessionID(alice, "../../etc/passwd"); !hex.MatchString(id) {
		t.Fatalf("a hostile context id must not reach the file name: %q", id)
	}
}

func TestWorkerPassesFlagsSessionAndCwd(t *testing.T) {
	w, logPath, state := newTestWorker(t)
	res, err := w.Run(context.Background(), Turn{Principal: alice, ContextID: "c1", TaskID: "t1", Prompt: "hello there"}, func(Update) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "echo: hello there" || res.Failure != "" {
		t.Fatalf("result %+v", res)
	}
	l := readFakeLog(t, logPath)
	for _, want := range []string{"--mode", "rpc", "--no-extensions", "--no-skills", "--no-context-files", "--no-prompt-templates", "--offline"} {
		if !contains(l.Args, want) {
			t.Errorf("argv lacks %s: %v", want, l.Args)
		}
	}
	if contains(l.Args, "--no-session") {
		t.Error("contexts continue across tasks, so the session must be kept")
	}
	if got := argValue(l.Args, "--tools"); got != "read,grep,find,ls" {
		t.Errorf("--tools = %q", got)
	}
	if got := argValue(l.Args, "--session-id"); got != SessionID(alice, "c1") {
		t.Errorf("--session-id = %q", got)
	}
	dir := argValue(l.Args, "--session-dir")
	if !strings.HasPrefix(dir, state) || dir == state {
		t.Errorf("--session-dir %q must be a per-principal directory under %q", dir, state)
	}
	if other := w.sessionDir(Principal{Name: "bob"}); other == dir {
		t.Error("principals must not share a session directory")
	}
	if l.Prompt != "hello there" {
		t.Errorf("prompt = %q", l.Prompt)
	}
	// The child reports the directory the OS resolved (macOS's TMPDIR is a symlink: /var -> /private/var).
	if got, want := realPath(t, l.Cwd), realPath(t, w.cfg.Cwd); got != want {
		t.Errorf("cwd = %q (%q), want %q (%q)", l.Cwd, got, w.cfg.Cwd, want)
	}
}

// realPath is p with symlinks resolved, so two spellings of one directory compare equal.
func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestWorkerModelAndExtraArgs(t *testing.T) {
	cfg, logPath, getenv := fakeWorkerConfig(t)
	cfg.Provider, cfg.Model, cfg.Args = "p", "m", []string{"--thinking", "off"}
	w, err := NewProcessWorker(cfg, t.TempDir(), getenv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Run(context.Background(), Turn{Principal: alice, ContextID: "c", Prompt: "x y"}, func(Update) {}); err != nil {
		t.Fatal(err)
	}
	l := readFakeLog(t, logPath)
	if argValue(l.Args, "--provider") != "p" || argValue(l.Args, "--model") != "m" || argValue(l.Args, "--thinking") != "off" {
		t.Fatalf("argv %v", l.Args)
	}
}

func TestWorkerEnvironmentIsAllowlisted(t *testing.T) {
	w, logPath, _ := newTestWorker(t)
	if _, err := w.Run(context.Background(), Turn{Principal: alice, ContextID: "c", Prompt: "ab"}, func(Update) {}); err != nil {
		t.Fatal(err)
	}
	env := readFakeLog(t, logPath).Env
	if env["PIG_A2A_WORKER"] != "1" {
		t.Error("the child must be marked as a worker so it never opens a listener")
	}
	if env["OPENAI_API_KEY"] != "sk-passthrough" || env["PIG_HOME"] != "/pig/home" || env["PIG_CODING_AGENT_DIR"] != "/pig/agent" {
		t.Errorf("configured passthrough and PiG's config directories must reach the child: %v", env)
	}
	for _, leak := range []string{"TOKEN_A", "SOMETHING_SECRET"} {
		if _, ok := env[leak]; ok {
			t.Errorf("%s leaked into the worker environment", leak)
		}
	}
}

func TestWorkerStreamsUpdatesAndFinalText(t *testing.T) {
	w, _, _ := newTestWorker(t)
	var text strings.Builder
	var tools []string
	res, err := w.Run(context.Background(), Turn{Principal: alice, ContextID: "c", Prompt: "TOOL please"}, func(u Update) {
		text.WriteString(u.Text)
		if u.Tool != "" {
			tools = append(tools, u.Tool)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "done" || text.String() != "done" || strings.Join(tools, ",") != "read" {
		t.Fatalf("res %+v streamed %q tools %v", res, text.String(), tools)
	}
}

func TestWorkerStreamedTextEqualsFinalTextWithoutDuplication(t *testing.T) {
	w, _, _ := newTestWorker(t)
	var text strings.Builder
	res, err := w.Run(context.Background(), Turn{Principal: alice, ContextID: "c", Prompt: "abcdef"}, func(u Update) { text.WriteString(u.Text) })
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != res.Text {
		t.Fatalf("streamed %q, final %q", text.String(), res.Text)
	}
}

func TestWorkerModelFailureIsAFailureWithoutProviderDetail(t *testing.T) {
	w, _, _ := newTestWorker(t)
	res, err := w.Run(context.Background(), Turn{Principal: alice, ContextID: "c", Prompt: "FAIL now"}, func(Update) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failure == "" {
		t.Fatal("a model error must fail the task")
	}
	if strings.Contains(res.Failure, "sk-123") {
		t.Fatalf("provider error text can carry secrets; it must not reach the A2A peer: %q", res.Failure)
	}
}

func TestWorkerCancelAbortsThenReaps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process liveness probe is POSIX")
	}
	w, logPath, _ := newTestWorker(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := w.Run(ctx, Turn{Principal: alice, ContextID: "c", Prompt: "SLOW task"}, func(Update) {})
		done <- err
	}()
	l := waitFakeLog(t, logPath, func(l fakeLog) bool { return l.Started })
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation must surface as an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	if l2 := readFakeLog(t, logPath); !l2.Aborted {
		t.Error("the worker must send the RPC abort before killing anything")
	}
	if processAlive(l.PID) {
		t.Error("the child process must be reaped")
	}
}

func TestWorkerKillsAChildThatIgnoresAbort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process liveness probe is POSIX")
	}
	w, logPath, _ := newTestWorker(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := w.Run(ctx, Turn{Principal: alice, ContextID: "c", Prompt: "IGNORE_ABORT"}, func(Update) {})
		done <- err
	}()
	l := waitFakeLog(t, logPath, func(l fakeLog) bool { return l.Started })
	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("a child that ignores abort must be killed after the grace period")
	}
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(l.PID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAlive(l.PID) {
		t.Fatal("child survived")
	}
}

func TestWorkerCommandThatCannotStartIsReported(t *testing.T) {
	cfg, _, getenv := fakeWorkerConfig(t)
	cfg.Command = "/nonexistent/pig"
	w, err := NewProcessWorker(cfg, t.TempDir(), getenv)
	if err != nil {
		return // refusing at construction is fine too
	}
	if _, err := w.Run(context.Background(), Turn{Principal: alice, ContextID: "c", Prompt: "x"}, func(Update) {}); err == nil {
		t.Fatal("want an error")
	}
}

func TestWorkerRefusesEmptyPrompt(t *testing.T) {
	w, _, _ := newTestWorker(t)
	if _, err := w.Run(context.Background(), Turn{Principal: alice, ContextID: "c", Prompt: "  "}, func(Update) {}); err == nil {
		t.Fatal("an empty prompt is not a task")
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
