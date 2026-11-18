package pi_subagents

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRexPanesSetting(t *testing.T) {
	home := withTempHome(t)
	cwd := tmp(t)
	userSettings := filepath.Join(home, ".pi", "agent", "settings.json")
	projectSettings := filepath.Join(cwd, ".pi", "settings.json")

	d, err := subagentSettingsFor(cwd, ScopeBoth)
	eq(t, err, nil, "error")
	eq(t, d.rexPanes == nil, true, "unset by default")

	writeFile(t, userSettings, `{"subagents": {"rexPanes": true}}`)
	d, _ = subagentSettingsFor(cwd, ScopeBoth)
	eq(t, d.rexPanes != nil && *d.rexPanes, true, "the user's")
	eq(t, d.herdrPanes == nil, true, "herdrPanes stays unset")

	writeFile(t, projectSettings, `{"subagents": {"rexPanes": false}}`)
	d, _ = subagentSettingsFor(cwd, ScopeBoth)
	eq(t, d.rexPanes != nil && !*d.rexPanes, true, "the project's wins")

	writeFile(t, projectSettings, `{"subagents": {"rexPanes": "yes"}}`)
	_, err = subagentSettingsFor(cwd, ScopeBoth)
	hasMatch(t, err, "Subagent settings in '"+projectSettings+"' have invalid 'rexPanes'; expected true or false.")
}

func TestRexPanesEnabled(t *testing.T) {
	yes, no := true, false
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	both := map[string]string{"REX_SESSION": "session:s", "REX_BLOCK": "block:p"}
	for _, c := range []struct {
		name string
		set  *bool
		env  map[string]string
		goos string
		want bool
	}{
		{"darwin", &yes, both, "darwin", true},
		{"linux", &yes, both, "linux", true},
		{"unset", nil, both, "darwin", false},
		{"false", &no, both, "darwin", false},
		{"no session", &yes, map[string]string{"REX_BLOCK": "block:p"}, "darwin", false},
		{"no block", &yes, map[string]string{"REX_SESSION": "session:s"}, "darwin", false},
		{"windows", &yes, both, "windows", false},
	} {
		eq(t, rexPanesEnabled(subagentSettings{rexPanes: c.set}, env(c.env), c.goos), c.want, c.name)
	}
	eq(t, rexBinary(env(map[string]string{"REX_BIN_DIR": "/opt/rex"})), filepath.Join("/opt/rex", "rex"), "the server's own CLI")
	eq(t, rexBinary(env(nil)), "rex", "else the one on PATH")
}

func TestRexEnvScript(t *testing.T) {
	env := []string{"A=1", "REX_BLOCK=block:p", "REX_SESSION=session:s", "Q=it's\n$HOME `x`", "bad-name=1", "B=x=y", "NOVALUE"}
	script := rexEnvScript(env)
	eq(t, script, "export A='1'\nexport Q='it'\\''s\n$HOME `x`'\nexport B='x=y'\n", "script")
	if runtime.GOOS == "windows" {
		return
	}
	file := filepath.Join(tmp(t), "env")
	writeFile(t, file, script)
	cmd := exec.Command("/bin/sh", "-c", `. "$0" && printf '%s|%s|%s' "$A" "$Q" "$B"`, file)
	cmd.Env = []string{}
	out, err := cmd.Output()
	eq(t, err, nil, "sh")
	eq(t, string(out), "1|it's\n$HOME `x`|x=y", "a shell reads the values back unchanged")
}

// rexRig is a scripted rex CLI with a stand-in child that writes `answer` as its session transcript. Each script entry is
// one poll of the child: a status ("working", "" for none yet), "exit/<code>" for an ended program, or "fail".
type rexRig struct {
	mu      sync.Mutex
	host    *rexHost
	calls   [][]string
	envFile string
	env     string
	notes   []string
	levels  []string
	req     childRequest
	fail    map[string]bool // a command ("split", "parent") that fails
}

func (r *rexRig) count(cmd string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c[0] == cmd {
			n++
		}
	}
	return n
}

func (r *rexRig) last() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[len(r.calls)-1]
}

func newRexRig(t *testing.T, script []string, answer string) *rexRig {
	t.Helper()
	oldPoll, oldCLI := panePoll, rexCLI
	panePoll = time.Millisecond
	t.Cleanup(func() { panePoll, rexCLI = oldPoll, oldCLI })
	r := &rexRig{fail: map[string]bool{}}
	step := 0
	rexCLI = func(bin string, args ...string) ([]byte, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		eq(t, bin, "/rex/bin/rex", "binary")
		r.calls = append(r.calls, args)
		switch args[0] {
		case "split":
			if r.fail["split"] {
				return nil, errors.New("no such session")
			}
			i := slices.Index(args, "--")
			r.envFile = args[i+4]
			env, _ := os.ReadFile(r.envFile)
			r.env = string(env)
			dir, id := args[slices.Index(args, "--session-dir")+1], args[slices.Index(args, "--session-id")+1]
			if answer != "" {
				line := `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":` + strconv.Quote(answer) + `}],"stopReason":"stop"}}` + "\n"
				os.WriteFile(filepath.Join(dir, "1_"+id+".jsonl"), []byte(line), 0o600)
			}
			return []byte(`{"anchor_id":"block:p","block_ids":["block:c"],"split_ids":["split:x"],"revision":3}`), nil
		case "api":
			eq(t, args[:4], []string{"api", "call", "-s", "session:s"}, "api call")
			method, block := strings.TrimPrefix(args[4], "com.superlogical.terminal."), args[5]
			if block == `{"block_id":"block:p"}` {
				if r.fail["parent"] {
					return nil, errors.New("connection refused")
				}
				return []byte(`{"last_exit":null,"records":[{"state":"working","app":"pig"}]}`), nil
			}
			eq(t, block, `{"block_id":"block:c"}`, "the child's block")
			s := script[min(step, len(script)-1)]
			if method == "program_status" {
				step++
				if s == "" {
					return []byte(`{"records":[]}`), nil
				}
				return []byte(`{"records":[{"state":"` + s + `","app":"pig"},{"id":"build/test","state":"error"}]}`), nil
			}
			if s == "fail" {
				return nil, errors.New("server gone")
			}
			if code, ok := strings.CutPrefix(s, "exit/"); ok {
				return []byte(`{"last_exit":{"exit_code":` + code + `,"error":"exit status"}}`), nil
			}
			return []byte(`{"last_exit":null,"foreground":{"name":"pig"}}`), nil
		case "capture":
			return []byte(strings.Repeat("line\n", 50) + "fatal: x\n\n"), nil
		}
		return nil, nil
	}
	r.host = &rexHost{bin: "/rex/bin/rex", session: "session:s", parent: "block:p", notify: func(m, l string) {
		r.notes, r.levels = append(r.notes, m), append(r.levels, l) // called outside rexCLI: no lock held
	}}
	r.req = childRequest{Bin: "pig", Args: []string{"--print", "--no-extensions"}, Stdin: "Task: t", Cwd: "/w", Env: []string{"PIG_SUBAGENT=1", "REX_BLOCK=block:p"}}
	return r
}

func hiddenMustNotRun(t *testing.T) {
	old := runChild
	t.Cleanup(func() { runChild = old })
	runChild = func(<-chan struct{}, childRequest) (childResult, error) {
		t.Fatal("hidden runner must not run")
		return childResult{}, nil
	}
}

func TestRexRun(t *testing.T) {
	hiddenMustNotRun(t)
	r := newRexRig(t, []string{"", "idle", "working", "blocked", "blocked", "working", "done"}, "answer")
	res, err := r.host.run(nil, r.req, "scout")
	eq(t, err, nil, "error")
	eq(t, res, childResult{Stdout: "answer"}, "result")

	var split []string
	for _, c := range r.calls {
		if c[0] == "split" {
			split = c
		}
	}
	i := slices.Index(split, "--")
	flags, argv := split[:i], split[i+1:]
	eq(t, flags[:9], []string{"split", "-s", "session:s", "-b", "block:p", "--focus=false", "--shell=none", "--keep-open", "--cwd"}, "flags")
	eq(t, flags[9], "/w", "cwd")
	if !regexp.MustCompile(`^scout-[0-9a-f]{6}$`).MatchString(flags[slices.Index(flags, "--label")+1]) {
		t.Fatalf("label in %v", flags)
	}
	eq(t, slices.Contains(flags, "--json"), true, "--json")
	eq(t, argv[:3], []string{"/bin/sh", "-c", `. "$0" && rm -f "$0" && exec "$@"`}, "the shell that reads the environment")
	eq(t, argv[4], "pig", "bin")
	eq(t, argv[len(argv)-2:], []string{"--", "Task: t"}, "prompt")
	eq(t, slices.Contains(argv, "--print"), false, "no --print")
	eq(t, r.env, "export PIG_SUBAGENT='1'\n", "the environment file: no REX_")
	for _, a := range split {
		eq(t, strings.Contains(a, "PIG_SUBAGENT"), false, "no environment on the command line")
	}

	eq(t, r.notes, []string{"scout needs you in its Rex pane (block:c)."}, "one notification for the block")
	eq(t, r.levels, []string{"warning"}, "level")
	eq(t, r.last(), []string{"block", "close", "-s", "session:s", "block:c"}, "cleanup")
	eq(t, r.count("block"), 1, "one close")
	eq(t, r.count("capture"), 0, "an answered child's screen is not read")
	for _, p := range []string{r.envFile, argv[slices.Index(argv, "--session-dir")+1]} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s still exists", p)
		}
	}
}

func TestRexRunTurnEnd(t *testing.T) {
	hiddenMustNotRun(t)
	r := newRexRig(t, []string{"idle", "idle", "working", "idle", "working"}, "answer")
	_, err := r.host.run(nil, r.req, "scout")
	eq(t, err, nil, "error")
	eq(t, r.count("api"), 2+2*4, "idle ends the turn only after the child was seen working")

	r = newRexRig(t, []string{"error", "working"}, "")
	res, _ := r.host.run(nil, r.req, "scout")
	eq(t, r.count("api"), 2+2, "error ends it")
	eq(t, res.Exit, 1, "no answer")
	if !strings.Contains(res.Stderr, "no answer in scout's session file") {
		t.Fatalf("stderr %q", res.Stderr)
	}
	eq(t, r.count("capture"), 0, "a live child's screen is not read")
}

func TestRexRunExit(t *testing.T) {
	hiddenMustNotRun(t)
	r := newRexRig(t, []string{"working", "exit/3"}, "")
	res, err := r.host.run(nil, r.req, "scout")
	eq(t, err, nil, "error")
	eq(t, res.Exit, 3, "exit")
	eq(t, strings.HasSuffix(res.Stderr, "line\nfatal: x"), true, "the screen's end")
	eq(t, strings.Count(res.Stderr, "\n"), 39, "40 lines")
	eq(t, r.count("api"), 2+2+1, "an ended program's status is not read")
	eq(t, r.count("block"), 1, "the kept-open block is closed")

	r = newRexRig(t, []string{"exit/0"}, "")
	res, _ = r.host.run(nil, r.req, "scout")
	eq(t, res.Exit, 1, "a clean exit without an answer is a failure")

	r = newRexRig(t, []string{"exit/0"}, "answer")
	res, _ = r.host.run(nil, r.req, "scout")
	eq(t, res, childResult{Stdout: "answer"}, "an answer before the exit counts")
}

func TestRexRunLostServer(t *testing.T) {
	hiddenMustNotRun(t)
	r := newRexRig(t, []string{"working", "fail"}, "answer")
	res, err := r.host.run(nil, r.req, "scout")
	eq(t, err, nil, "error")
	eq(t, res.Exit, 1, "exit")
	if !strings.Contains(res.Stderr, "Rex stopped answering while scout ran in block:c: server gone") {
		t.Fatalf("stderr %q", res.Stderr)
	}
	eq(t, r.count("block"), 1, "the close is still tried")
}

func TestRexRunSplitFails(t *testing.T) {
	hiddenMustNotRun(t)
	r := newRexRig(t, []string{"done"}, "answer")
	r.fail["split"] = true
	_, err := r.host.run(nil, r.req, "scout")
	if err == nil || !strings.Contains(err.Error(), "rex: no such session") {
		t.Fatalf("error %v", err)
	}
	eq(t, r.count("block"), 0, "nothing to close")

	r = newRexRig(t, []string{"done"}, "answer")
	inner := rexCLI
	rexCLI = func(bin string, args ...string) ([]byte, error) {
		out, err := inner(bin, args...)
		if args[0] == "split" {
			out = []byte(`{"block_ids":[]}`)
		}
		return out, err
	}
	_, err = r.host.run(nil, r.req, "scout")
	if err == nil || !strings.Contains(err.Error(), "without a block") {
		t.Fatalf("error %v", err)
	}
	eq(t, r.count("block"), 0, "nothing to close")
}

func TestRexRunCancel(t *testing.T) {
	hiddenMustNotRun(t)
	r := newRexRig(t, []string{"working"}, "")
	done := make(chan struct{})
	go func() {
		for r.count("api") < 4 { // the parent, then the first poll
			time.Sleep(time.Millisecond)
		}
		close(done)
	}()
	_, err := r.host.run(done, r.req, "scout")
	eq(t, errors.Is(err, errCancelled), true, "cancelled")
	eq(t, r.last(), []string{"block", "close", "-s", "session:s", "block:c"}, "cleanup")

	closed := make(chan struct{})
	close(closed)
	r = newRexRig(t, []string{"working"}, "")
	_, err = r.host.run(closed, r.req, "scout")
	eq(t, errors.Is(err, errCancelled), true, "cancelled before start")
	eq(t, len(r.calls), 0, "no calls")
}

func TestRexRunFallback(t *testing.T) {
	check := func(name string, r *rexRig, wantCalls int) {
		var got childRequest
		old := runChild
		t.Cleanup(func() { runChild = old })
		runChild = func(_ <-chan struct{}, req childRequest) (childResult, error) {
			got = req
			return childResult{Stdout: "hidden"}, nil
		}
		res, err := r.host.run(nil, r.req, "scout")
		eq(t, err, nil, name+": error")
		eq(t, res.Stdout, "hidden", name+": the hidden runner ran")
		eq(t, got.Stdin, r.req.Stdin, name+": with the same request")
		eq(t, r.levels, []string{"warning"}, name+": one warning")
		eq(t, len(r.calls), wantCalls, name+": calls")
	}
	r := newRexRig(t, []string{"done"}, "answer")
	r.fail["parent"] = true
	check("unreachable", r, 1) // the parent's process, which failed
	if !strings.Contains(r.notes[0], "Rex is not reachable (connection refused); scout runs without a pane.") {
		t.Fatalf("note %q", r.notes[0])
	}

	r = newRexRig(t, []string{"done"}, "answer")
	r.req.Stdin = strings.Repeat("x", paneTaskMax+1)
	check("task size", r, 0)
	if !strings.Contains(r.notes[0], "over 100 KiB") {
		t.Fatalf("note %q", r.notes[0])
	}
}

func TestRunAgentRexPanes(t *testing.T) {
	hiddenMustNotRun(t)
	r := newRexRig(t, []string{"working", "done"}, "answer")
	out, err := runAgent(nil, ".", &AgentConfig{Name: "scout", SystemPromptMode: "replace"}, "t", "", r.host)
	eq(t, []any{out, err}, []any{"answer", nil}, "the pane runner")
	eq(t, r.count("split"), 1, "split")
}
