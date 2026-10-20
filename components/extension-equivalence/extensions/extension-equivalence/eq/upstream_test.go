package eq

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// pigpen-websearch: pigeq scripted the model and child processes but not the HTTP APIs a
// web-access, judge or remote-agent extension talks to. A scenario can declare fake upstream
// servers: routes with canned answers, their URL in env vars and files, and every request they
// received in the trace, so a port that calls an endpoint differently fails the diff.

func startUp(t *testing.T, specs map[string]ServerSpec) (*upstreams, func() []Event) {
	t.Helper()
	rec := &recorder{step: "01-x", norm: NewNormalizer("", "", "")}
	u, err := startUpstreams(specs, rec.add)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(u.close)
	return u, func() []Event {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return append([]Event(nil), rec.events...)
	}
}

func get(t *testing.T, method, url string, body string, headers map[string]string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), res.Header
}

func TestUpstreamServesRoutesInOrderAndRecordsEveryRequest(t *testing.T) {
	u, snap := startUp(t, map[string]ServerSpec{"brave": {
		Routes: []Route{
			{Method: "GET", Path: "/res/v1/web/search", Query: map[string]string{"q": "pigs"}, JSON: map[string]any{"results": []any{"a"}}, Headers: map[string]string{"X-Rate": "1"}},
			{Method: "POST", Path: "/echo", Status: 201, Body: "created", Times: 1},
			{Path: "/files/*", Body: "any file"},
		},
		RecordHeaders: []string{"x-subscription-token"},
	}})
	base := u.URL("brave")
	code, body, hdr := get(t, "GET", base+"/res/v1/web/search?q=pigs&count=2", "", map[string]string{"X-Subscription-Token": "k1", "User-Agent": "varies"})
	if code != 200 || !strings.Contains(body, `"results"`) || hdr.Get("X-Rate") != "1" || !strings.HasPrefix(hdr.Get("Content-Type"), "application/json") {
		t.Errorf("search: %d %q %v", code, body, hdr)
	}
	if code, body, _ := get(t, "POST", base+"/echo", `{"a":1}`, map[string]string{"Content-Type": "application/json"}); code != 201 || body != "created" {
		t.Errorf("echo: %d %q", code, body)
	}
	if code, _, _ := get(t, "POST", base+"/echo", `{"a":2}`, nil); code != 404 {
		t.Errorf("a route with Times 1 must be spent after one use, got %d", code)
	}
	if code, body, _ := get(t, "GET", base+"/files/deep/x.txt", "", nil); code != 200 || body != "any file" {
		t.Errorf("prefix route: %d %q", code, body)
	}
	if code, _, _ := get(t, "GET", base+"/res/v1/web/search?q=other", "", nil); code != 404 {
		t.Errorf("query mismatch must not match: %d", code)
	}

	events := snap()
	if len(events) != 5 {
		t.Fatalf("%d events, want 5: %+v", len(events), events)
	}
	var first map[string]any
	if err := json.Unmarshal(events[0].Data, &first); err != nil {
		t.Fatal(err)
	}
	if events[0].Ch != ChHTTP || first["server"] != "brave" || first["method"] != "GET" || first["path"] != "/res/v1/web/search" || first["matched"] != true {
		t.Errorf("first event = %s", events[0].Data)
	}
	h := first["headers"].(map[string]any)
	if h["x-subscription-token"] != "k1" || h["user-agent"] != nil || len(h) != 1 {
		t.Errorf("only declared headers are recorded (user agents differ between runtimes): %v", h)
	}
	if q := first["query"].(map[string]any); q["q"].([]any)[0] != "pigs" || q["count"].([]any)[0] != "2" {
		t.Errorf("query = %v", q)
	}
	var second map[string]any
	_ = json.Unmarshal(events[1].Data, &second)
	if b, _ := second["body"].(map[string]any); b["a"] != float64(1) || second["headers"].(map[string]any)["content-type"] != "application/json" {
		t.Errorf("a JSON body is recorded decoded, content-type by default: %v", second)
	}
	var missed map[string]any
	_ = json.Unmarshal(events[2].Data, &missed)
	if missed["matched"] != false {
		t.Errorf("an unmatched request is recorded as such: %v", missed)
	}
}

func TestUpstreamExpandsServerURLsAndNormalizesTheirPorts(t *testing.T) {
	u, _ := startUp(t, map[string]ServerSpec{"exa": {Routes: []Route{{Path: "/x", Body: "ok"}}}, "tavily": {Routes: []Route{{Path: "/y", Body: "ok"}}}})
	got := u.expand("EXA={{server:exa}}/mcp TAVILY={{server:tavily}}")
	if !strings.Contains(got, u.URL("exa")+"/mcp") || !strings.Contains(got, u.URL("tavily")) || strings.Contains(got, "{{") {
		t.Errorf("expand = %q", got)
	}
	n := NewNormalizer("", "", "")
	u.normalize(n)
	if s := n.str("fetched " + u.URL("exa") + "/x and " + u.URL("tavily") + "/y"); s != "fetched <server:exa>/x and <server:tavily>/y" {
		t.Errorf("normalized = %q", s)
	}
}

func TestUpstreamDelayAndDrop(t *testing.T) {
	u, snap := startUp(t, map[string]ServerSpec{"slow": {Routes: []Route{{Path: "/slow", DelayMs: 150, Body: "late"}, {Path: "/drop", Drop: true}}}})
	start := time.Now()
	if _, body, _ := get(t, "GET", u.URL("slow")+"/slow", "", nil); body != "late" || time.Since(start) < 150*time.Millisecond {
		t.Errorf("delay ignored: %q after %s", body, time.Since(start))
	}
	// (a GET would be retried once by Go's client; a POST is not)
	if _, err := http.Post(u.URL("slow")+"/drop", "text/plain", strings.NewReader("x")); err == nil {
		t.Error("a Drop route must break the connection")
	}
	events := snap()
	if len(events) != 2 {
		t.Errorf("a dropped request is still recorded: %d events", len(events))
	}
}

func TestScenarioValidatesServersAndEnv(t *testing.T) {
	ok := func(mod func(*Scenario)) error {
		sc := &Scenario{Name: "a", Steps: []Step{{RPC: map[string]any{"type": "get_state"}}},
			Servers: map[string]ServerSpec{"api": {Routes: []Route{{Path: "/x", Body: "ok"}}}}, Env: map[string]string{"API_URL": "{{server:api}}"}}
		mod(sc)
		return sc.Validate()
	}
	if err := ok(func(*Scenario) {}); err != nil {
		t.Fatalf("valid scenario rejected: %v", err)
	}
	for name, mod := range map[string]func(*Scenario){
		"unknown server referenced": func(s *Scenario) { s.Env["X"] = "{{server:nope}}" },
		"server name not lowercase": func(s *Scenario) { s.Servers["Api"] = s.Servers["api"] },
		"no routes":                 func(s *Scenario) { s.Servers["api"] = ServerSpec{} },
		"path without slash":        func(s *Scenario) { s.Servers["api"] = ServerSpec{Routes: []Route{{Path: "x"}}} },
		"body and json":             func(s *Scenario) { s.Servers["api"] = ServerSpec{Routes: []Route{{Path: "/x", Body: "a", JSON: 1}}} },
		"bad status":                func(s *Scenario) { s.Servers["api"] = ServerSpec{Routes: []Route{{Path: "/x", Status: 42}}} },
		"env overrides the shim":    func(s *Scenario) { s.Env["EQ_SHIM_SOCK"] = "x" },
		"env overrides HOME":        func(s *Scenario) { s.Env["HOME"] = "/root" },
		// The harness gives every lane a hermetic git identity, a fixed locale and terminal, and the Go toolchain;
		// exec keeps the last duplicate, so a scenario variable of the same name would silently replace them.
		"env overrides git's global config":  func(s *Scenario) { s.Env["GIT_CONFIG_GLOBAL"] = "/home/u/.gitconfig" },
		"env overrides the git date":         func(s *Scenario) { s.Env["GIT_AUTHOR_DATE"] = "2026-01-01T00:00:00Z" },
		"env overrides LANG":                 func(s *Scenario) { s.Env["LANG"] = "de_DE.UTF-8" },
		"env overrides TERM":                 func(s *Scenario) { s.Env["TERM"] = "xterm-256color" },
		"env overrides NO_COLOR":             func(s *Scenario) { s.Env["NO_COLOR"] = "" },
		"env overrides GOFLAGS":              func(s *Scenario) { s.Env["GOFLAGS"] = "-tags=fake" },
		"env overrides GOTOOLCHAIN":          func(s *Scenario) { s.Env["GOTOOLCHAIN"] = "go1.22.0" },
		"env sets GOWORK for the port build": func(s *Scenario) { s.Env["GOWORK"] = "off" },
		"reference in a file":                func(s *Scenario) { s.Files = map[string]string{"c.json": "{{server:nope}}"} },
	} {
		if err := ok(mod); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := ok(func(s *Scenario) {
		s.Files = map[string]string{"c.json": `{"base":"{{server:api}}"}`}
		s.Args = []string{"--x={{server:api}}"}
	}); err != nil {
		t.Errorf("references in files and args: %v", err)
	}
	// An extension's own variables stay allowed, including ones that merely start like a harness prefix.
	if err := ok(func(s *Scenario) { s.Env["GOOGLE_API_KEY"] = "k"; s.Env["LANGUAGE_MODEL"] = "m" }); err != nil {
		t.Errorf("extension variables: %v", err)
	}
}

// pigpen-jev: an extension's user-level configuration lives in the agent directory
// (<agent dir>/pi-jev.json); `files` writes only into the workspace and `env` cannot move the
// agent directory. `agentFiles` are written under the lane's agent directory before the host
// starts, with {{server:NAME}} expanded like files.
func TestScenarioValidatesAgentFiles(t *testing.T) {
	ok := func(mod func(*Scenario)) error {
		sc := &Scenario{Name: "a", Steps: []Step{{RPC: map[string]any{"type": "get_state"}}},
			Servers:    map[string]ServerSpec{"api": {Routes: []Route{{Path: "/x", Body: "ok"}}}},
			AgentFiles: map[string]string{"pi-jev.json": `{"endpoint":"{{server:api}}/v1"}`}}
		mod(sc)
		return sc.Validate()
	}
	if err := ok(func(*Scenario) {}); err != nil {
		t.Fatalf("valid agentFiles rejected: %v", err)
	}
	for name, mod := range map[string]func(*Scenario){
		"absolute path":        func(s *Scenario) { s.AgentFiles["/etc/x"] = "x" },
		"escaping path":        func(s *Scenario) { s.AgentFiles["../x.json"] = "x" },
		"unknown server":       func(s *Scenario) { s.AgentFiles["c.json"] = "{{server:nope}}" },
		"the harness's models": func(s *Scenario) { s.LLM = []Turn{{Text: "x"}}; s.AgentFiles["models.json"] = "{}" },
	} {
		if err := ok(mod); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := ok(func(s *Scenario) { s.AgentFiles["sub/dir/c.json"] = "{}" }); err != nil {
		t.Errorf("nested agent file: %v", err)
	}
}

func TestWriteFilesUnderExpandsServersAndCreatesDirectories(t *testing.T) {
	u, _ := startUp(t, map[string]ServerSpec{"api": {Routes: []Route{{Path: "/x", Body: "ok"}}}})
	agent := t.TempDir()
	if err := writeFilesUnder(agent, map[string]string{"pi-jev.json": `{"endpoint":"{{server:api}}"}`, "extensions/x/config.json": "{}"}, u); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(agent, "pi-jev.json"))
	if err != nil || !strings.Contains(string(b), u.URL("api")) || strings.Contains(string(b), "{{") {
		t.Errorf("%v %s", err, b)
	}
	if _, err := os.Stat(filepath.Join(agent, "extensions/x/config.json")); err != nil {
		t.Error(err)
	}
}

// pigpen-warden: an opt-in extension is off until an environment variable turns it on, so a scenario
// must be able to set that variable for BOTH lanes (the original under Pi and the port under PiG)
// without touching the hermetic environment. The host is a script that records its environment.
func TestScenarioEnvReachesEveryLaneAndKeepsTheHostHermetic(t *testing.T) {
	realHome := "/home/eq-real-user"
	t.Setenv("HOME", realHome)
	t.Setenv("PIGPEN_WARDEN_ENABLED", "leaked-from-the-test-process") // a variable outside the scenario must not leak in
	sc := &Scenario{Name: "optin", Steps: []Step{{RPC: map[string]any{"type": "get_state"}}},
		Servers: map[string]ServerSpec{"api": {Routes: []Route{{Path: "/x", Body: "ok"}}}},
		Env:     map[string]string{"PIGPEN_WARDEN_ENABLED": "1", "WARDEN_JUDGE_URL": "{{server:api}}/v1"}}
	if err := sc.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"pi", "pig"} {
		dir := t.TempDir()
		bin := filepath.Join(t.TempDir(), host)
		// One file per invocation: the harness also runs `<host> --version`, which is not the scenario run.
		script := "#!/bin/sh\nenv > '" + dir + "'/env.$$\nexit 0\n"
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := RunLane(sc, Lane{Name: host + "-lane", Host: host, Bin: bin}, Options{}); err != nil {
			t.Fatalf("%s: %v", host, err) // the fake host speaks no protocol, but the run must still complete
		}
		files, _ := filepath.Glob(filepath.Join(dir, "env.*"))
		var env string
		for _, f := range files {
			b, _ := os.ReadFile(f)
			if strings.Contains(string(b), "PIGPEN_WARDEN_ENABLED=1") {
				env = string(b)
			}
		}
		if env == "" {
			t.Fatalf("%s: no host invocation saw the scenario's variable (%d invocations)", host, len(files))
		}
		// The Go toolchain variables (GOPATH, GOMODCACHE, GOCACHE) are passed on purpose (detectToolchain) and
		// default to paths under HOME when the caller does not export them; they are not a leak of the home.
		var hostOwn []string
		for _, line := range strings.Split(env, "\n") {
			if !strings.HasPrefix(line, "GO") {
				hostOwn = append(hostOwn, line)
			}
		}
		if strings.Contains(env, "leaked-from-the-test-process") || strings.Contains(strings.Join(hostOwn, "\n"), realHome) {
			t.Errorf("%s: the harness's own environment leaked into the hermetic host:\n%s", host, env)
		}
		if !regexp.MustCompile(`(?m)^WARDEN_JUDGE_URL=http://127\.0\.0\.1:\d+/v1$`).MatchString(env) {
			t.Errorf("%s: {{server:api}} was not expanded in env:\n%s", host, env)
		}
		if !strings.Contains(env, "PIG_HOME=") || !strings.Contains(env, "NO_COLOR=1") {
			t.Errorf("%s: the harness's fixed variables are missing:\n%s", host, env)
		}
	}
}

// pigpen-websearch: the model's tool-call arguments must be able to name a fake server (fetch_content's
// url), so {{server:NAME}} is expanded in the LLM script too: turn text and every string value of the
// arguments, at any depth. The scenario is shared by the lanes and must stay unexpanded.
func TestExpandTurnsExpandsServersInTextAndNestedArgumentsWithoutChangingTheScenario(t *testing.T) {
	u, _ := startUp(t, map[string]ServerSpec{"web": {Routes: []Route{{Path: "/x", Body: "ok"}}}, "api": {Routes: []Route{{Path: "/y", Body: "ok"}}}})
	turns := []Turn{
		{Text: "fetching {{server:web}}/page", ToolCalls: []ToolCall{
			{Name: "fetch_content", Arguments: map[string]any{
				"url":    "{{server:web}}/page",
				"urls":   []any{"{{server:web}}/a", "{{server:api}}/b", 7, true, nil},
				"nested": map[string]any{"deep": []any{map[string]any{"u": "x {{server:api}} y"}}},
				"n":      3,
			}},
			{Name: "get_search_content"}, // no arguments
		}},
		{Text: "done"},
	}
	got := u.expandTurns(turns)
	web, api := u.URL("web"), u.URL("api")
	b, _ := json.Marshal(got)
	want := `[{"text":"fetching ` + web + `/page","toolCalls":[{"name":"fetch_content","arguments":{"n":3,"nested":{"deep":[{"u":"x ` + api + ` y"}]},"url":"` + web + `/page","urls":["` + web + `/a","` + api + `/b",7,true,null]}},{"name":"get_search_content","arguments":null}]},{"text":"done"}]`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
	orig, _ := json.Marshal(turns)
	if !strings.Contains(string(orig), "{{server:web}}/page") || strings.Contains(string(orig), web) {
		t.Errorf("the shared scenario was changed: %s", orig)
	}
	// An unknown name is left as written (validation rejects it earlier).
	if g := u.expandTurns([]Turn{{Text: "{{server:nope}}"}}); g[0].Text != "{{server:nope}}" {
		t.Errorf("unknown reference: %q", g[0].Text)
	}
}

// The expanded URL is what the model script puts on the wire, and a URL the extension echoes back is
// normalized to <server:NAME>, so the trace of each lane (its own ports) is the same.
func TestExpandedScriptReachesTheWireAndNormalizesToTheServerName(t *testing.T) {
	u, _ := startUp(t, map[string]ServerSpec{"web": {Routes: []Route{{Path: "/x", Body: "ok"}}}})
	llm, err := newFakeLLM(u.expandTurns([]Turn{{ToolCalls: []ToolCall{{Name: "fetch_content", Arguments: map[string]any{"url": "{{server:web}}/page"}}}}}), func(string, any) {})
	if err != nil {
		t.Fatal(err)
	}
	defer llm.close()
	resp, err := http.Post(llm.baseURL()+"/chat/completions", "application/json", strings.NewReader(`{"messages":[{"role":"user","content":"go"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(out), `\"url\":\"`+u.URL("web")+`/page\"`) {
		t.Errorf("the wire lacks the expanded URL: %s", out)
	}
	n := NewNormalizer("", "", "")
	u.normalize(n)
	if s := string(n.Data(map[string]any{"arguments": `{"url":"` + u.URL("web") + `/page"}`})); !strings.Contains(s, `<server:web>/page`) || strings.Contains(s, "127.0.0.1") {
		t.Errorf("normalized = %s", s)
	}
}

func TestScenarioValidatesServerReferencesInTheLLMScript(t *testing.T) {
	ok := func(mod func(*Scenario)) error {
		sc := &Scenario{Name: "a", Steps: []Step{{RPC: map[string]any{"type": "prompt", "message": "x"}}},
			Servers: map[string]ServerSpec{"web": {Routes: []Route{{Path: "/x", Body: "ok"}}}},
			LLM:     []Turn{{Text: "see {{server:web}}", ToolCalls: []ToolCall{{Name: "fetch_content", Arguments: map[string]any{"url": "{{server:web}}/page"}}}}, {Text: "done"}}}
		mod(sc)
		return sc.Validate()
	}
	if err := ok(func(*Scenario) {}); err != nil {
		t.Fatalf("valid script rejected: %v", err)
	}
	for name, c := range map[string]struct {
		mod  func(*Scenario)
		want string
	}{
		"unknown server in turn text":   {func(s *Scenario) { s.LLM[1].Text = "{{server:nope}}" }, "llm turn 2 text"},
		"unknown server in an argument": {func(s *Scenario) { s.LLM[0].ToolCalls[0].Arguments["url"] = "{{server:nope}}/p" }, `llm turn 1 tool call 1 (fetch_content) arguments refers to server "nope"`},
		"unknown server in a nested array": {func(s *Scenario) {
			s.LLM[0].ToolCalls[0].Arguments["urls"] = []any{map[string]any{"u": []any{"{{server:nope}}"}}}
		}, `server "nope"`},
		"server reference with no servers": {func(s *Scenario) { s.Servers = nil }, `server "web"`},
	} {
		err := ok(c.mod)
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q lacks %q", name, err, c.want)
		}
	}
}

// The whole path in one lane: RunLane starts the scenario's fake servers, expands the LLM script with
// their URLs, and records the model request through the scenario's N7 rules. The host is a script that
// asks the scripted model once, like a real host would.
func TestRunLaneExpandsTheScriptNormalizesRulesAndTheModelAddress(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("the fake host needs curl")
	}
	sc := &Scenario{Name: "wire", Steps: []Step{{RPC: map[string]any{"type": "set_model", "provider": "eq-llm", "modelId": "eq-1"}}, {RPC: map[string]any{"type": "prompt", "message": "go"}}},
		Servers: map[string]ServerSpec{"web": {Routes: []Route{{Path: "/page", Body: "ok"}}}},
		LLM:     []Turn{{Text: "see {{server:web}}", ToolCalls: []ToolCall{{Name: "fetch_content", Arguments: map[string]any{"url": "{{server:web}}/page"}}}}},
		TailMs:  -1, SettleMs: 10, Normalize: []NormalizeRule{{Pattern: `r-[0-9a-f]{6}`, Replacement: "<rid>"}}}
	if err := sc.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"pi", "pig"} {
		dir := t.TempDir()
		bin := filepath.Join(t.TempDir(), host)
		// A minimal RPC host: it answers get_state, and on a prompt asks the scripted model once and ends the run.
		script := `#!/bin/sh
[ "$1" = "--version" ] && exit 0
printf '%s' "$PI_CODING_AGENT_DIR" > '` + dir + `'/agentdir.$$
base=$(sed -n 's/.*"baseUrl":"\([^"]*\)".*/\1/p' "$PI_CODING_AGENT_DIR/models.json")
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  case "$line" in
    *'"type":"get_state"'*) printf '{"type":"response","id":"%s","command":"get_state","success":true}\n' "$id" ;;
    *'"type":"set_model"'*) printf '{"type":"response","id":"%s","command":"set_model","success":true,"data":{"id":"eq-1","baseUrl":"%s","note":"dial %s"}}\n' "$id" "$base" "${base#http://}" ;;
    *'"type":"prompt"'*)
      printf '{"type":"response","id":"%s","command":"prompt","success":true}\n' "$id"
      curl -s -X POST "$base/chat/completions" -d '{"messages":[{"role":"user","content":"responseId r-1a2b3c"}]}' > '` + dir + `'/answer.$$
      printf '{"type":"agent_end"}\n' ;;
  esac
done
`
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		tr, err := RunLane(sc, Lane{Name: host + "-lane", Host: host, Bin: bin}, Options{})
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		// Every host start (the baseline run included) sees an agent directory of the same shape and length.
		dirs, _ := filepath.Glob(filepath.Join(dir, "agentdir.*"))
		if len(dirs) < 2 {
			t.Errorf("%s: expected the lane and its baseline run, got %d host starts", host, len(dirs))
		}
		for _, f := range dirs {
			b, _ := os.ReadFile(f)
			if !regexp.MustCompile(`/eq[0-9a-f]{8}/agent$`).Match(b) {
				t.Errorf("%s: agent directory %q is not <tmp>/eq<8 hex>/agent", host, b)
			}
		}
		var answers string
		files, _ := filepath.Glob(filepath.Join(dir, "answer.*"))
		for _, f := range files {
			b, _ := os.ReadFile(f)
			answers += string(b)
		}
		if !regexp.MustCompile(`see http://127\.0\.0\.1:\d+`).MatchString(answers) || !regexp.MustCompile(`\\"url\\":\\"http://127\.0\.0\.1:\d+/page\\"`).MatchString(answers) {
			t.Errorf("%s: the model's answer lacks the expanded server URL (%d answers): %s", host, len(files), answers)
		}
		var llm []string
		for _, e := range tr.Events {
			if e.Ch == ChLLM {
				llm = append(llm, string(e.Data))
			}
		}
		// The host echoes the model's base URL in a set_model response; each lane's scripted model has its own port.
		var setModel string
		for _, e := range tr.Events {
			if e.Ch == ChResponse && strings.Contains(string(e.Data), `"set_model"`) {
				setModel = string(e.Data)
			}
		}
		if !strings.Contains(setModel, `"baseUrl":"<llm>/v1"`) || !strings.Contains(setModel, `"note":"dial <llm>/v1"`) || strings.Contains(setModel, "127.0.0.1") {
			t.Errorf("%s: the scripted model's address was not normalized: %s", host, setModel)
		}
		if len(llm) != 1 || !strings.Contains(llm[0], "responseId <rid>") || strings.Contains(llm[0], "r-1a2b3c") {
			t.Errorf("%s: the model request was not normalized by the scenario's rule: %q", host, llm)
		}
	}
}

// pigpen-warden: a port keeps its configuration under PIG_HOME, which `agentFiles` cannot reach (the agent
// directory is <root>/agent, PIG_HOME is <root>/pighome, and `..` is refused). `homeFiles` is the scoped way:
// the lane's own host home, PIG_HOME for pig and ~/.pi for pi, and nothing outside it.
func TestScenarioValidatesHomeFiles(t *testing.T) {
	ok := func(mod func(*Scenario)) error {
		sc := &Scenario{Name: "a", Steps: []Step{{RPC: map[string]any{"type": "get_state"}}},
			Servers:   map[string]ServerSpec{"api": {Routes: []Route{{Path: "/x", Body: "ok"}}}},
			HomeFiles: map[string]string{"warden/config.json": `{"url":"{{server:api}}"}`}}
		mod(sc)
		return sc.Validate()
	}
	if err := ok(func(*Scenario) {}); err != nil {
		t.Fatalf("valid homeFiles rejected: %v", err)
	}
	for name, mod := range map[string]func(*Scenario){
		"absolute path":   func(s *Scenario) { s.HomeFiles["/etc/x"] = "x" },
		"escaping path":   func(s *Scenario) { s.HomeFiles["../agent/x.json"] = "x" },
		"nested escape":   func(s *Scenario) { s.HomeFiles["a/../../x"] = "x" },
		"the home itself": func(s *Scenario) { s.HomeFiles["."] = "x" },
		"empty path":      func(s *Scenario) { s.HomeFiles[""] = "x" },
		"unknown server":  func(s *Scenario) { s.HomeFiles["c.json"] = "{{server:nope}}" },
	} {
		if err := ok(mod); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestWriteFilesUnderRefusesEscapesEvenWithoutValidation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pighome")
	for _, rel := range []string{"../escaped", "/tmp/eq-abs", "a/../../escaped", "."} {
		if err := writeFilesUnder(dir, map[string]string{rel: "x"}, &upstreams{}); err == nil {
			t.Errorf("%q written", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "escaped")); err == nil {
		t.Error("a file escaped the directory")
	}
}

// Each lane's host finds homeFiles in its own home: PIG_HOME on a pig lane, $HOME/.pi on a pi lane. Neither lane
// sees the other's, and the agent directory stays separate.
func TestHomeFilesLandInTheLanesOwnHome(t *testing.T) {
	sc := &Scenario{Name: "homefiles", Steps: []Step{{RPC: map[string]any{"type": "get_state"}}},
		Servers:    map[string]ServerSpec{"api": {Routes: []Route{{Path: "/x", Body: "ok"}}}},
		HomeFiles:  map[string]string{"warden/config.json": `{"url":"{{server:api}}/v1"}`},
		AgentFiles: map[string]string{"settings.json": "{}"},
		TailMs:     -1, SettleMs: 10}
	if err := sc.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"pi", "pig"} {
		dir := t.TempDir()
		bin := filepath.Join(t.TempDir(), host)
		// Record what each home holds when the host starts; skip the `--version` probe.
		script := `#!/bin/sh
[ "$1" = "--version" ] && exit 0
n=$$
[ -f "$PIG_HOME/warden/config.json" ] && cp "$PIG_HOME/warden/config.json" '` + dir + `'/pighome.$n
[ -f "$HOME/.pi/warden/config.json" ] && cp "$HOME/.pi/warden/config.json" '` + dir + `'/pihome.$n
[ -f "$PI_CODING_AGENT_DIR/warden/config.json" ] && cp "$PI_CODING_AGENT_DIR/warden/config.json" '` + dir + `'/agent.$n
[ -f "$PI_CODING_AGENT_DIR/settings.json" ] && : > '` + dir + `'/settings.$n
exit 0
`
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := RunLane(sc, Lane{Name: host + "-lane", Host: host, Bin: bin}, Options{}); err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		glob := func(kind string) []string { m, _ := filepath.Glob(filepath.Join(dir, kind+".*")); return m }
		own, other := "pighome", "pihome"
		if host == "pi" {
			own, other = other, own
		}
		files := glob(own)
		if len(files) == 0 {
			t.Fatalf("%s: the host did not find homeFiles in its own home", host)
		}
		b, _ := os.ReadFile(files[0])
		if !regexp.MustCompile(`^\{"url":"http://127\.0\.0\.1:\d+/v1"\}$`).Match(b) {
			t.Errorf("%s: homeFiles content not expanded: %s", host, b)
		}
		if len(glob(other)) != 0 || len(glob("agent")) != 0 {
			t.Errorf("%s: homeFiles leaked into the other lane's home or the agent directory", host)
		}
		if len(glob("settings")) == 0 {
			t.Errorf("%s: agentFiles were lost", host)
		}
	}
}
