package pi_subagents

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Safety of the running addition (review rev-port-popular-5): how a child is started, what it inherits, how many run, how
// deep they nest, what comes back and what happens when the call is cancelled. The port's own tests, written before the code.

// TestMain lets the test binary stand in for a child pig (PISUB_HELPER=1): it reads its prompt from stdin and answers
// with what the PISUB_* variables say, so the real runner is tested against a real process.
func TestMain(m *testing.M) {
	if os.Getenv("PISUB_HELPER") == "1" {
		helperChild()
		return
	}
	os.Exit(m.Run())
}

func helperChild() {
	if os.Getenv("PISUB_IGNORE_TERM") == "1" {
		signal.Ignore(syscall.SIGTERM)
	}
	if p := os.Getenv("PISUB_PIDFILE"); p != "" {
		_ = os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())), 0o644)
	}
	if os.Getenv("PISUB_ECHO_STDIN") == "1" {
		in, _ := io.ReadAll(os.Stdin)
		fmt.Fprint(os.Stdout, "stdin:"+string(in))
	}
	n, _ := strconv.Atoi(os.Getenv("PISUB_REPEAT"))
	if n == 0 {
		n = 1
	}
	fmt.Fprint(os.Stdout, strings.Repeat(os.Getenv("PISUB_OUT"), n))
	fmt.Fprint(os.Stderr, os.Getenv("PISUB_ERR"))
	if s, _ := strconv.Atoi(os.Getenv("PISUB_SLEEP")); s > 0 {
		time.Sleep(time.Duration(s) * time.Second)
	}
	code, _ := strconv.Atoi(os.Getenv("PISUB_EXIT"))
	os.Exit(code)
}

func helperRequest(t *testing.T, stdin string, env ...string) childRequest {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return childRequest{Bin: exe, Stdin: stdin, Cwd: t.TempDir(), Env: append(append(os.Environ(), "PISUB_HELPER=1"), env...)}
}

// fakeChildren replaces the runner with one that records each request and answers from outputs by task text.
func fakeChildren(t *testing.T, outputs map[string]string) *[]childRequest {
	t.Helper()
	old := runChild
	t.Cleanup(func() { runChild = old })
	var mu sync.Mutex
	var got []childRequest
	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		mu.Lock()
		got = append(got, req)
		mu.Unlock()
		task := strings.TrimPrefix(req.Stdin, "Task: ")
		for k, v := range outputs {
			if strings.Contains(task, k) {
				return childResult{Stdout: v}, nil
			}
		}
		return childResult{Stdout: "ok"}, nil
	}
	return &got
}

func TestTaskTravelsOnStdin(t *testing.T) {
	// A task is not a command-line argument: pig would read "--version" or "- fix it" as an option and "@notes.txt" as a
	// file to attach, and a long task would exceed the per-argument limit. The child reads it from stdin, in the form the
	// original prompts its children with ("Task: ...").
	calls := fakeChildren(t, nil)
	for _, task := range []string{"--version", "- fix the list", "@notes.txt", strings.Repeat("x", 300*1024)} {
		if _, err := runAgent(nil, ".", &AgentConfig{Name: "a", SystemPromptMode: "replace"}, task, ""); err != nil {
			t.Fatal(err)
		}
		req := (*calls)[len(*calls)-1]
		eq(t, req.Stdin, "Task: "+task, "stdin")
		for _, a := range req.Args {
			if strings.Contains(a, task) {
				t.Errorf("the task %.20q is on the command line: %q", task, req.Args)
			}
		}
	}
	eq(t, childPrompt("do it"), "Task: do it", "prompt")
}

func TestThinkingSuffixOfModelOverrideWins(t *testing.T) {
	// The schema: "Suffix :off/minimal/low/medium/high/xhigh/max overrides agent thinking default."
	a := &AgentConfig{Name: "a", Thinking: "high", SystemPromptMode: "replace"}
	eq(t, buildChildArgs(a, "prov/m:low"), []string{"--no-extensions", "--print", "--model", "prov/m:low"}, "suffix overrides")
	eq(t, buildChildArgs(a, "prov/m"), []string{"--no-extensions", "--print", "--model", "prov/m", "--thinking", "high"}, "no suffix")
	eq(t, buildChildArgs(a, "prov/m:latest"), []string{"--no-extensions", "--print", "--model", "prov/m:latest", "--thinking", "high"}, "not a level")
}

func TestChildEnvironmentDropsTheParentsInternals(t *testing.T) {
	base := []string{"HOME=/h", "PIG_EXT_SOCKET_PI_SUBAGENTS=/run/s.sock", "PIG_EXT_PACKED_CELL=1", "PIG_EXT_ACTIVE_MEMBERS=x",
		"PIG_HARNESS_BINARY=/bin/pig", "PIG_HARNESS_ARGV_FILE=/run/argv.json", "OPENAI_API_KEY=k", "PIG_SUBAGENT=1",
		"PIG_SUBAGENT_DEPTH=1", "PIG_AGENT_ROLE=old"}
	eq(t, childEnv(base, 1, "scout"), []string{"HOME=/h", "OPENAI_API_KEY=k", "PIG_SUBAGENT=1", "PIG_SUBAGENT_DEPTH=2", "PIG_AGENT_ROLE=scout"}, "env")
}

func TestPigBinaryOrder(t *testing.T) {
	t.Setenv("PIG_SUBAGENT_PIG_BINARY", "/opt/override/pig")
	t.Setenv("PIG_HARNESS_BINARY", "/opt/harness/pig")
	eq(t, pigBinary(), "/opt/override/pig", "the override first")
	t.Setenv("PIG_SUBAGENT_PIG_BINARY", "")
	eq(t, pigBinary(), "/opt/harness/pig", "then the pig that runs this extension")
}

func TestNestingDepth(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	for _, c := range []struct {
		env        map[string]string
		depth, max int
	}{
		{map[string]string{}, 0, 2},
		{map[string]string{"PIG_SUBAGENT": "1"}, 1, 2},
		{map[string]string{"PIG_SUBAGENT": "1", "PIG_SUBAGENT_DEPTH": "2"}, 2, 2},
		{map[string]string{"PIG_SUBAGENT_DEPTH": "x", "PIG_SUBAGENT": "1"}, 1, 2},
		{map[string]string{"PI_SUBAGENT_MAX_DEPTH": "0"}, 0, 0},
		{map[string]string{"PI_SUBAGENT_MAX_DEPTH": "-1", "PIG_SUBAGENT_DEPTH": "1"}, 1, 2},
	} {
		d, m := subagentDepth(env(c.env))
		eq(t, []int{d, m}, []int{c.depth, c.max}, fmt.Sprint(c.env))
	}
	eq(t, nestedBlocked(env(map[string]string{"PIG_SUBAGENT_DEPTH": "1"})), "", "depth 1 of 2 may delegate")
	eq(t, nestedBlocked(env(map[string]string{"PIG_SUBAGENT_DEPTH": "2"})),
		"Nested subagent call blocked (depth=2, max=2). You are running at the maximum subagent nesting depth. Complete your current task directly without delegating to further subagents.",
		"the original's text")
}

func TestAtMostFourChildrenAtOnce(t *testing.T) {
	old := runChild
	t.Cleanup(func() { runChild = old })
	var live, peak int32
	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		n := atomic.AddInt32(&live, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(60 * time.Millisecond)
		atomic.AddInt32(&live, -1)
		return childResult{Stdout: "ok"}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := runAgent(nil, ".", &AgentConfig{Name: "a", SystemPromptMode: "replace"}, "t", ""); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	eq(t, atomic.LoadInt32(&peak), int32(4), "peak concurrent children (the original's MAX_CONCURRENCY)")
}

func TestCancelWhileWaitingForASlot(t *testing.T) {
	old := runChild
	t.Cleanup(func() { runChild = old })
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		started <- struct{}{}
		<-release
		return childResult{Stdout: "ok"}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = runAgent(nil, ".", &AgentConfig{Name: "a"}, "t", "") }()
		<-started
	}
	done := make(chan struct{})
	close(done)
	errc := make(chan error, 1)
	go func() { _, err := runAgent(done, ".", &AgentConfig{Name: "w"}, "t", ""); errc <- err }()
	select {
	case err := <-errc:
		hasMatch(t, err, "w was cancelled")
	case <-time.After(5 * time.Second):
		t.Error("a cancelled call kept waiting for a slot")
	}
	close(release)
	wg.Wait()
}

func TestRealChildAnswerIsStdout(t *testing.T) {
	res, err := execChild(nil, helperRequest(t, "Task: hi", "PISUB_ECHO_STDIN=1", "PISUB_OUT= done", "PISUB_ERR=[pig] a warning"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, res, childResult{Stdout: "stdin:Task: hi done", Stderr: "[pig] a warning", Exit: 0}, "stdout is the answer, stderr apart")

	res, err = execChild(nil, helperRequest(t, "", "PISUB_OUT=partial", "PISUB_ERR=boom", "PISUB_EXIT=3"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, res, childResult{Stdout: "partial", Stderr: "boom", Exit: 3}, "exit code")

	req := helperRequest(t, "")
	req.Bin = filepath.Join(t.TempDir(), "no-such-pig")
	if _, err := execChild(nil, req, time.Second); err == nil {
		t.Error("a missing program is an error")
	}
}

func TestRealChildRunsWhereAndWithWhatItIsGiven(t *testing.T) {
	req := helperRequest(t, "")
	dir := tmp(t)
	req.Cwd = dir
	req.Env = append(req.Env, "PISUB_PIDFILE=pid.txt", "PISUB_OUT=x")
	if _, err := execChild(nil, req, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pid.txt")); err != nil {
		t.Errorf("the child did not run in its cwd: %v", err)
	}
	// only the given environment: a variable of this process that the request leaves out is not inherited
	t.Setenv("PISUB_OUT", "leaked")
	req = helperRequest(t, "")
	var env []string
	for _, kv := range req.Env {
		if !strings.HasPrefix(kv, "PISUB_OUT=") {
			env = append(env, kv)
		}
	}
	req.Env = env
	res, err := execChild(nil, req, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, res.Stdout, "", "environment")
}

func TestRealChildOutputIsBounded(t *testing.T) {
	// The original's DEFAULT_MAX_OUTPUT (200 KB, 5000 lines) and its truncation marker (shared/types.ts truncateOutput);
	// the expected texts are what that function returns for the same output.
	for _, c := range []struct {
		out, repeat string
		marker      string
		length      int
	}{
		{"x", strconv.Itoa(300 * 1024), "[TRUNCATED: showing first 1 of 1 lines, 200.0KB of 300.0KB]\n", 60 + 200*1024},
		{"ab\n", "6000", "[TRUNCATED: showing first 5000 of 6001 lines, 14.6KB of 17.6KB]\n", 15063},
		{"é", strconv.Itoa(150 * 1024), "[TRUNCATED: showing first 1 of 1 lines, 200.0KB of 300.0KB]\n", 60 + 200*1024},
		{"aé", strconv.Itoa(100 * 1024), "[TRUNCATED: showing first 1 of 1 lines, 200.0KB of 300.0KB]\n", 204859}, // the bound falls inside an é
	} {
		res, err := execChild(nil, helperRequest(t, "", "PISUB_OUT="+c.out, "PISUB_REPEAT="+c.repeat), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(res.Stdout, c.marker) || len(res.Stdout) != c.length {
			t.Errorf("%q x %s: %.80q (%d bytes)", c.out, c.repeat, res.Stdout, len(res.Stdout))
		}
	}
	res, err := execChild(nil, helperRequest(t, "", "PISUB_ERR="+strings.Repeat("e", 100*1024), "PISUB_EXIT=1"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stderr) > 8*1024 {
		t.Errorf("stderr kept %d bytes", len(res.Stderr))
	}
}

func TestRunAgentReportsStderrOnFailure(t *testing.T) {
	old := runChild
	t.Cleanup(func() { runChild = old })
	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		return childResult{Stdout: "half an answer", Stderr: "Error: no model", Exit: 1}, nil
	}
	_, err := runAgent(nil, ".", &AgentConfig{Name: "a"}, "t", "")
	hasMatch(t, err, "a exited with code 1: Error: no model")
	_, err = runChain(nil, ".", &ChainConfig{Name: "c", Steps: []ChainStep{{Agent: "a"}}}, []AgentConfig{{Name: "a"}}, "t", func(string) {})
	hasMatch(t, err, "step 1 (a) exited with code 1: Error: no model")
}

func TestRunCwd(t *testing.T) {
	base := tmp(t)
	writeFile(t, filepath.Join(base, "sub", "x"), "")
	writeFile(t, filepath.Join(base, "file"), "")
	for _, c := range []struct{ requested, want, err string }{
		{"", base, ""},
		{"sub", filepath.Join(base, "sub"), ""},
		{filepath.Join(base, "sub") + "/", filepath.Join(base, "sub"), ""},
		{"nope", "", "Subagent launch aborted: cwd does not exist: " + filepath.Join(base, "nope") + "\n(resolved from \"nope\")"},
		{filepath.Join(base, "nope"), "", "Subagent launch aborted: cwd does not exist: " + filepath.Join(base, "nope")},
		{"file", "", "Subagent launch aborted: cwd is not a directory: " + filepath.Join(base, "file") + "\n(resolved from \"file\")"},
	} {
		got, err := resolveRunCwd(base, c.requested)
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		eq(t, []string{got, msg}, []string{c.want, c.err}, "cwd "+c.requested)
	}
}

func TestEitherDone(t *testing.T) {
	a, b := make(chan struct{}), make(chan struct{})
	d, stop := eitherDone(a, b)
	close(b)
	select {
	case <-d:
	case <-time.After(5 * time.Second):
		t.Fatal("the second channel did not cancel")
	}
	stop()
	d, stop = eitherDone(nil, nil)
	if d != nil {
		t.Error("nothing to wait for is nil")
	}
	stop()
	only := make(chan struct{})
	d, stop = eitherDone(only, nil)
	close(only)
	select {
	case <-d:
	case <-time.After(5 * time.Second):
		t.Fatal("the only channel did not cancel")
	}
	stop()
	_ = errors.New
}
