package pi_subagents

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHerdrPanesSetting(t *testing.T) {
	home := withTempHome(t)
	cwd := tmp(t)
	userSettings := filepath.Join(home, ".pi", "agent", "settings.json")
	projectSettings := filepath.Join(cwd, ".pi", "settings.json")

	d, err := subagentSettingsFor(cwd, ScopeBoth)
	eq(t, err, nil, "error")
	eq(t, d.herdrPanes == nil, true, "unset by default")

	writeFile(t, userSettings, `{"subagents": {"herdrPanes": true}}`)
	d, _ = subagentSettingsFor(cwd, ScopeBoth)
	eq(t, d.herdrPanes != nil && *d.herdrPanes, true, "the user's")

	writeFile(t, projectSettings, `{"subagents": {"herdrPanes": false}}`)
	d, _ = subagentSettingsFor(cwd, ScopeBoth)
	eq(t, d.herdrPanes != nil && !*d.herdrPanes, true, "the project's wins")
	d, _ = subagentSettingsFor(cwd, ScopeUser)
	eq(t, d.herdrPanes != nil && *d.herdrPanes, true, "scope user reads no project settings")

	writeFile(t, projectSettings, `{"subagents": {"herdrPanes": "yes"}}`)
	_, err = subagentSettingsFor(cwd, ScopeBoth)
	hasMatch(t, err, "Subagent settings in '"+projectSettings+"' have invalid 'herdrPanes'; expected true or false.")
}

func TestPanesEnabled(t *testing.T) {
	yes, no := true, false
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	both := map[string]string{"HC_TERMINAL_SOCKET": "/s", "HC_TERMINAL_ID": "t1"}
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
		{"no socket", &yes, map[string]string{"HC_TERMINAL_ID": "t1"}, "darwin", false},
		{"no id", &yes, map[string]string{"HC_TERMINAL_SOCKET": "/s"}, "darwin", false},
		{"windows", &yes, both, "windows", false},
	} {
		eq(t, panesEnabled(subagentSettings{herdrPanes: c.set}, env(c.env), c.goos), c.want, c.name)
	}
}

func TestPaneArgs(t *testing.T) {
	eq(t, paneArgs([]string{"--print", "--model", "p/m", "--print-x"}, "/d", "id", "Task: t"),
		[]string{"--model", "p/m", "--print-x", "--session-dir", "/d", "--session-id", "id", "--", "Task: t"}, "args")
}

func TestPaneEnv(t *testing.T) {
	eq(t, paneEnv([]string{"A=1", "HC_TERMINAL_ID=t1", "HC_SCOPE=x", "PIG_SUBAGENT=1", "B=x=y"}),
		map[string]string{"A": "1", "PIG_SUBAGENT": "1", "B": "x=y"}, "env")
}

func TestPaneName(t *testing.T) {
	eq(t, paneName("scout", "ab12"), "scout-ab12", "plain")
	eq(t, paneName("My.Agent/X", "ab12"), "my.agent-x-ab12", "sanitized")
	long := paneName(strings.Repeat("a", 40), "ab12")
	eq(t, len(long), 32, "long length")
	eq(t, strings.HasSuffix(long, "-ab12"), true, "long suffix")
	eq(t, paneName("", "ab12"), "agent-ab12", "empty")
}

func TestSessionAnswer(t *testing.T) {
	asst := func(content, extra string) string {
		return `{"type":"message","message":{"role":"assistant","content":[` + content + `]` + extra + `}}` + "\n"
	}
	head := `{"type":"session","id":"id"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}` + "\n" +
		asst(`{"type":"toolCall","name":"x"}`, `,"stopReason":"toolUse"`) +
		`{"type":"message","message":{"role":"toolResult","content":[{"type":"text","text":"` + strings.Repeat("x", 2<<20) + `"}]}}` + "\n"
	answer := func(last string) (childResult, bool) {
		dir := tmp(t)
		writeFile(t, filepath.Join(dir, "1_id.jsonl"), head+last)
		return sessionAnswer(dir, "id")
	}
	r, ok := answer(asst(`{"type":"thinking","thinking":"z"},{"type":"text","text":"a"},{"type":"text","text":"b"}`, `,"stopReason":"stop"`))
	eq(t, ok, true, "ok")
	eq(t, r, childResult{Stdout: "a\nb\n"}, "stop")
	r, _ = answer(asst(``, `,"stopReason":"error","errorMessage":"boom"`))
	eq(t, r, childResult{Exit: 1, Stderr: "boom"}, "error")
	r, _ = answer(asst(``, `,"stopReason":"aborted"`))
	eq(t, r, childResult{Exit: 1, Stderr: "Request aborted"}, "aborted")
	r, _ = answer(asst(`{"type":"text","text":"part"}`, `,"stopReason":"length"`))
	eq(t, r, childResult{Stdout: "part\n", Exit: 1, Stderr: "Response was truncated: the model reached its output token limit."}, "length")

	_, ok = answer(asst(`{"type":"text","text":"part"},{"type":"toolCall","name":"x"}`, `,"stopReason":"toolUse"`))
	eq(t, ok, false, "ends mid-turn: assistant toolUse")
	_, ok = answer(asst(`{"type":"toolCall","name":"x"}`, `,"stopReason":"toolUse"`) +
		`{"type":"message","message":{"role":"toolResult","content":[{"type":"text","text":"r"}]}}` + "\n")
	eq(t, ok, false, "ends mid-turn: toolUse then toolResult")

	_, ok = sessionAnswer(tmp(t), "id")
	eq(t, ok, false, "no file")
	dir := tmp(t)
	writeFile(t, filepath.Join(dir, "1_id.jsonl"), `{"type":"session"}`+"\n")
	_, ok = sessionAnswer(dir, "id")
	eq(t, ok, false, "no assistant message")
}

// paneRig is a scripted herdr-core with a stand-in child that writes `answer` as its session transcript.
type paneRig struct {
	fake   *fakeHC
	host   *paneHost
	notes  []string
	levels []string
	req    childRequest
}

func newPaneRig(t *testing.T, parentLabels map[string]any, script []string, answer string) *paneRig {
	t.Helper()
	old := panePoll
	panePoll = time.Millisecond
	t.Cleanup(func() { panePoll = old })
	r := &paneRig{}
	var mu sync.Mutex
	step := 0
	r.fake = startFakeHC(t, func(method string, params map[string]any) (any, *hcError) {
		mu.Lock()
		defer mu.Unlock()
		switch method {
		case "hello":
			return map[string]any{}, nil
		case "terminal.get":
			if params["terminal"] == "t1" {
				return map[string]any{"address": "t1", "state": "live", "labels": parentLabels}, nil
			}
			s := script[min(step, len(script)-1)]
			step++
			if strings.HasPrefix(s, "close") {
				return nil, nil
			}
			if strings.HasPrefix(s, "{") {
				return json.RawMessage(s), nil
			}
			name, turn, _ := strings.Cut(s, "/")
			return json.RawMessage(`{"address":"t9","state":"live","status":{"state":"` + name + `"},"turn":` + turn + `}`), nil
		case "terminal.run":
			argv := params["argv"].([]any)
			var dir, id string
			for i, a := range argv {
				if a == "--session-dir" {
					dir = argv[i+1].(string)
				}
				if a == "--session-id" {
					id = argv[i+1].(string)
				}
			}
			if strings.HasPrefix(answer, "{") { // a raw transcript
				os.WriteFile(filepath.Join(dir, "1_"+id+".jsonl"), []byte(answer), 0o600)
			} else if answer != "" {
				line := `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":` + strconv.Quote(answer) + `}],"stopReason":"stop"}}` + "\n"
				os.WriteFile(filepath.Join(dir, "1_"+id+".jsonl"), []byte(line), 0o600)
			}
			return map[string]any{"address": "t9"}, nil
		case "terminal.kill":
			return map[string]any{"state": "finished"}, nil
		case "terminal.read":
			return map[string]any{"text": "one\nfatal: x\n"}, nil
		}
		return map[string]any{}, nil
	})
	r.host = &paneHost{socket: r.fake.socket, parent: "t1", notify: func(m, l string) {
		mu.Lock()
		r.notes, r.levels = append(r.notes, m), append(r.levels, l)
		mu.Unlock()
	}}
	r.req = childRequest{Bin: "pig", Args: []string{"--print", "--no-extensions"}, Stdin: "Task: t", Cwd: "/w", Env: []string{"PIG_SUBAGENT=1", "HC_TERMINAL_ID=t1"}}
	return r
}

var bothLabels = map[string]any{"herdr.space": "s1", "herdr.tab": "b1"}

func TestPaneRun(t *testing.T) {
	r := newPaneRig(t, bothLabels, []string{"idle/0", "working/0", "blocked/1", "blocked/1", "working/1", "done/2"}, "answer")
	r.host.sessionDir = ""
	res, err := r.host.run(nil, r.req, "scout")
	eq(t, err, nil, "error")
	eq(t, childResult{Stdout: res.Stdout, Stderr: res.Stderr, Exit: res.Exit}, childResult{Stdout: "answer"}, "result")
	if !regexp.MustCompile(`1_[0-9a-f-]+\.jsonl$`).MatchString(res.Session) {
		t.Errorf("the child's session file: %q", res.Session)
	}
	p := r.fake.calls("terminal.run")[0]
	argv := p["argv"].([]any)
	eq(t, argv[0], "pig", "bin")
	eq(t, argv[len(argv)-2:], []any{"--", "Task: t"}, "prompt")
	for _, a := range argv {
		eq(t, a == "--print", false, "no --print")
	}
	env := p["env"].(map[string]any)
	_, hasHC := env["HC_TERMINAL_ID"]
	eq(t, hasHC, false, "no HC_")
	eq(t, env["PIG_SUBAGENT"], "1", "PIG_SUBAGENT")
	eq(t, p["labels"], map[string]any{"herdr.space": "s1", "herdr.tab": "b1"}, "labels")
	eq(t, p["parent"], "t1", "parent")
	eq(t, p["no_notify"], true, "no_notify")
	eq(t, p["cwd"], "/w", "cwd")
	if !regexp.MustCompile(`^scout-[0-9a-f]{6}$`).MatchString(p["name"].(string)) {
		t.Fatalf("name %v", p["name"])
	}
	eq(t, len(r.notes), 1, "one notification")
	eq(t, r.levels[0], "warning", "level")
	if !strings.Contains(r.notes[0], "scout") || !strings.Contains(r.notes[0], "t9") {
		t.Fatalf("note %q", r.notes[0])
	}
	m := r.fake.methods()
	eq(t, m[len(m)-2:], []string{"terminal.kill", "terminal.rm"}, "cleanup")
	eq(t, r.fake.calls("terminal.kill")[0]["terminal"], "t9", "kill target")
	eq(t, r.fake.calls("terminal.rm")[0]["terminal"], "t9", "rm target")
	// the temporary session directory is gone
	argvDir := argv[indexOf(argv, "--session-dir")+1].(string)
	if _, err := os.Stat(argvDir); !os.IsNotExist(err) {
		t.Fatalf("session dir %s still exists", argvDir)
	}
}

func indexOf(argv []any, s string) int {
	for i, a := range argv {
		if a == s {
			return i
		}
	}
	return -1
}

func TestPaneRunParentWithoutTab(t *testing.T) {
	r := newPaneRig(t, map[string]any{"herdr.space": "s1"}, []string{"done/1"}, "answer")
	_, err := r.host.run(nil, r.req, "scout")
	eq(t, err, nil, "error")
	eq(t, r.fake.calls("terminal.run")[0]["labels"], map[string]any{}, "labels")
}

func TestPaneRunExitBeforeTurn(t *testing.T) {
	r := newPaneRig(t, bothLabels, []string{`{"address":"t9","state":"finished","turn":0,"exit_code":3}`}, "")
	res, err := r.host.run(nil, r.req, "scout")
	eq(t, err, nil, "error")
	eq(t, res.Exit, 3, "exit")
	if !strings.Contains(res.Stderr, "fatal: x") {
		t.Fatalf("stderr %q", res.Stderr)
	}
	eq(t, r.fake.calls("terminal.read")[0]["lines"], float64(40), "lines")
	eq(t, len(r.fake.calls("terminal.kill")), 0, "no kill")
	eq(t, len(r.fake.calls("terminal.rm")), 1, "one rm")
}

func TestPaneRunKilledWithoutCode(t *testing.T) {
	midTurn := `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"part"},{"type":"toolCall","name":"x"}],"stopReason":"toolUse"}}` + "\n"
	r := newPaneRig(t, bothLabels, []string{`{"address":"t9","state":"finished","turn":1,"exit_code":null}`}, midTurn)
	res, err := r.host.run(nil, r.req, "scout")
	eq(t, err, nil, "error")
	eq(t, res.Exit, -1, "exit")
	eq(t, res.Stdout, "", "no partial answer")
	if !strings.Contains(res.Stderr, "fatal: x") {
		t.Fatalf("stderr %q", res.Stderr)
	}
	eq(t, len(r.fake.calls("terminal.kill")), 0, "no kill")
	eq(t, len(r.fake.calls("terminal.rm")), 1, "one rm")
}

func TestPaneRunQuitWithoutAnswer(t *testing.T) {
	r := newPaneRig(t, bothLabels, []string{`{"address":"t9","state":"finished","turn":0,"exit_code":0}`}, "")
	res, err := r.host.run(nil, r.req, "scout")
	eq(t, err, nil, "error")
	eq(t, res.Exit, 1, "a clean exit without an answer is a failure")
	if !strings.Contains(res.Stderr, "fatal: x") {
		t.Fatalf("stderr %q", res.Stderr)
	}
}

func TestPaneRunEmptyAddress(t *testing.T) {
	r := newPaneRig(t, bothLabels, []string{"done/1"}, "answer")
	r.fake = startFakeHC(t, func(method string, _ map[string]any) (any, *hcError) {
		switch method {
		case "terminal.get":
			return map[string]any{"address": "t1", "state": "live", "labels": bothLabels}, nil
		case "terminal.run":
			return map[string]any{"address": ""}, nil
		}
		return map[string]any{}, nil
	})
	r.host.socket = r.fake.socket
	_, err := r.host.run(nil, r.req, "scout")
	if err == nil || !strings.Contains(err.Error(), "herdr-core") {
		t.Fatalf("error %v", err)
	}
	eq(t, len(r.fake.calls("terminal.get")), 1, "only the parent was read")
	eq(t, len(r.fake.calls("terminal.kill"))+len(r.fake.calls("terminal.rm")), 0, "nothing to clean")
}

func TestPaneRunCancel(t *testing.T) {
	r := newPaneRig(t, bothLabels, []string{"working/0"}, "")
	done := make(chan struct{})
	go func() {
		for len(r.fake.calls("terminal.get")) < 2 { // the parent, then the first poll
			time.Sleep(time.Millisecond)
		}
		close(done)
	}()
	_, err := r.host.run(done, r.req, "scout")
	eq(t, errors.Is(err, errCancelled), true, "cancelled")
	m := r.fake.methods()
	eq(t, m[len(m)-2:], []string{"terminal.kill", "terminal.rm"}, "cleanup")

	closed := make(chan struct{})
	close(closed)
	r2 := newPaneRig(t, bothLabels, []string{"working/0"}, "")
	_, err = r2.host.run(closed, r2.req, "scout")
	eq(t, errors.Is(err, errCancelled), true, "cancelled before start")
	eq(t, len(r2.fake.methods()), 0, "no calls")
}

func TestPaneRunFallback(t *testing.T) {
	check := func(name string, r *paneRig, wantCalls bool) {
		var got childRequest
		old := runChild
		t.Cleanup(func() { runChild = old })
		runChild = func(_ <-chan struct{}, req childRequest) (childResult, error) {
			got = req
			return childResult{Stdout: "hidden"}, nil
		}
		res, err := r.host.run(nil, r.req, "scout")
		eq(t, err, nil, name+" error")
		eq(t, res.Stdout, "hidden", name+" result")
		eq(t, got, r.req, name+" request unchanged")
		eq(t, len(r.levels), 1, name+" one notification")
		eq(t, r.levels[0], "warning", name+" level")
		if !strings.Contains(r.notes[0], "scout") {
			t.Fatalf("%s: note %q", name, r.notes[0])
		}
		eq(t, len(r.fake.methods()) > 0, wantCalls, name+" fake calls")
		eq(t, len(r.fake.calls("terminal.run")), 0, name+" no terminal.run")
	}
	r := newPaneRig(t, bothLabels, []string{"done/1"}, "answer")
	r.host.socket = filepath.Join(tmp(t), "nope")
	check("no socket", r, false)

	r = newPaneRig(t, bothLabels, []string{"done/1"}, "answer")
	bad := startFakeHC(t, func(method string, _ map[string]any) (any, *hcError) {
		if method == "terminal.get" {
			return nil, &hcError{Kind: "not_found", Message: "no such terminal"}
		}
		return map[string]any{}, nil
	})
	r.host.socket = bad.socket
	r.fake = bad
	check("parent not found", r, true)

	r = newPaneRig(t, bothLabels, []string{"done/1"}, "answer")
	r.req.Stdin = strings.Repeat("x", 100<<10+1)
	check("big task", r, false)
}

func TestPaneRunLostCore(t *testing.T) {
	r := newPaneRig(t, bothLabels, []string{"working/0", "close"}, "")
	res, err := r.host.run(nil, r.req, "scout")
	eq(t, err, nil, "error")
	eq(t, res.Exit, 1, "exit")
	if !strings.Contains(res.Stderr, "herdr-core") {
		t.Fatalf("stderr %q", res.Stderr)
	}
	eq(t, len(r.fake.calls("terminal.rm")), 1, "cleanup over a fresh connection")
}

func TestPaneRunCap(t *testing.T) {
	text := strings.Repeat("line of the answer\n", 300<<10/19)
	r := newPaneRig(t, bothLabels, []string{"done/1"}, strings.TrimSuffix(text, "\n"))
	res, _ := r.host.run(nil, r.req, "scout")
	w := &headWriter{max: outputMaxBytes}
	w.Write([]byte(text))
	eq(t, res.Stdout, w.text(), "capped like the hidden runner")
	eq(t, len(res.Stdout) < len(text), true, "actually capped")
}

func TestPaneRunStartFails(t *testing.T) {
	r := newPaneRig(t, bothLabels, []string{"done/1"}, "answer")
	r.fake = startFakeHC(t, func(method string, _ map[string]any) (any, *hcError) {
		switch method {
		case "terminal.get":
			return map[string]any{"address": "t1", "state": "live", "labels": bothLabels}, nil
		case "terminal.run":
			return nil, &hcError{Kind: "spawn_failed", Message: "no such program"}
		}
		return map[string]any{}, nil
	})
	r.host.socket = r.fake.socket
	old := runChild
	t.Cleanup(func() { runChild = old })
	runChild = func(<-chan struct{}, childRequest) (childResult, error) {
		t.Fatal("hidden runner must not run")
		return childResult{}, nil
	}
	_, err := r.host.run(nil, r.req, "scout")
	if err == nil || !strings.Contains(err.Error(), "no such program") {
		t.Fatalf("error %v", err)
	}
	eq(t, len(r.fake.calls("terminal.kill")), 0, "no kill")
	eq(t, len(r.fake.calls("terminal.rm")), 0, "no rm")
}

func TestRunAgentPanes(t *testing.T) {
	old := runChild
	t.Cleanup(func() { runChild = old })
	hidden := 0
	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		hidden++
		return childResult{Stdout: "hidden"}, nil
	}
	a := &AgentConfig{Name: "scout", SystemPromptMode: "replace"}
	r := newPaneRig(t, bothLabels, []string{"idle/0", "working/0", "blocked/1", "blocked/1", "working/1", "done/2"}, "answer")

	out, err := runAgent(nil, ".", a, "t", "", nil)
	eq(t, []any{out, err, hidden}, []any{"hidden", nil, 1}, "nil panes: the hidden runner")
	eq(t, len(r.fake.methods()), 0, "no herdr-core call")

	out, err = runAgent(nil, ".", a, "t", "", r.host)
	eq(t, []any{out, err, hidden}, []any{"answer", nil, 1}, "panes: the pane runner")
	eq(t, len(r.fake.calls("terminal.run")), 1, "terminal.run")
}
