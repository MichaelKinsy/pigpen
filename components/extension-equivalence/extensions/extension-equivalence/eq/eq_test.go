package eq

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestScenarioValidation(t *testing.T) {
	ok := `{"name":"a-b","steps":[{"rpc":{"type":"prompt","message":"x"}}]}`
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if _, err := LoadScenario(write("ok.json", ok)); err != nil {
		t.Fatalf("valid scenario rejected: %v", err)
	}
	bad := map[string]string{
		"unknown field":     `{"name":"a","steps":[{"rpc":{"type":"x"}}],"bogus":1}`,
		"bad name":          `{"name":"A b","steps":[{"rpc":{"type":"x"}}]}`,
		"no steps":          `{"name":"a","steps":[]}`,
		"rpc id":            `{"name":"a","steps":[{"rpc":{"type":"x","id":"1"}}]}`,
		"no rpc type":       `{"name":"a","steps":[{"rpc":{}}]}`,
		"escaping file":     `{"name":"a","files":{"../x":"y"},"steps":[{"rpc":{"type":"x"}}]}`,
		"absolute file":     `{"name":"a","files":{"/x":"y"},"steps":[{"rpc":{"type":"x"}}]}`,
		"command with path": `{"name":"a","commands":{"a/b":{}},"steps":[{"rpc":{"type":"x"}}]}`,
		"command mode":      `{"name":"a","commands":{"git":{"mode":"fake"}},"steps":[{"rpc":{"type":"x"}}]}`,
		"two answers":       `{"name":"a","steps":[{"rpc":{"type":"x"},"ui":[{"value":"a","cancelled":true}]}]}`,
		"no answer":         `{"name":"a","steps":[{"rpc":{"type":"x"},"ui":[{}]}]}`,
		"wait":              `{"name":"a","steps":[{"rpc":{"type":"x"},"wait":"soon"}]}`,
	}
	for name, body := range bad {
		if _, err := LoadScenario(write("bad.json", body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestStepWaitDefaults(t *testing.T) {
	cases := []struct {
		st   Step
		want string
	}{
		{Step{RPC: map[string]any{"type": "prompt", "message": "hello"}}, "agent_end"},
		{Step{RPC: map[string]any{"type": "prompt", "message": "/x"}}, "response"},
		{Step{RPC: map[string]any{"type": "prompt", "message": "/x"}, UI: []UIAnswer{{Cancelled: true}}}, "ui"},
		{Step{RPC: map[string]any{"type": "get_state"}}, "response"},
		{Step{RPC: map[string]any{"type": "prompt", "message": "hello"}, Wait: "response"}, "response"},
	}
	for _, c := range cases {
		if got := c.st.wait(); got != c.want {
			t.Errorf("%v: wait = %s, want %s", c.st.RPC, got, c.want)
		}
	}
}

func TestNormalizerRules(t *testing.T) {
	n := NewNormalizer("/tmp/eq1/work", "/tmp/eq1", "/tmp/eq1/home")
	got := string(n.Data(map[string]any{
		"path": "/tmp/eq1/work/a.txt", "agent": "/tmp/eq1/agent/x", "home": "/tmp/eq1/home/.pi",
		"id": "0d0c7e0e-1c3e-4d8a-9b57-8a1e5d9d9c11", "at": "2026-09-29T16:30:24.531Z", "keep": "extension text",
	}))
	want := `{"agent":"<tmp>/agent/x","at":"<time>","home":"<home>/.pi","id":"<uuid>","keep":"extension text","path":"<cwd>/a.txt"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestProjectHostKeepsExtensionEffectsOnly(t *testing.T) {
	n := NewNormalizer("/w", "/t", "/h")
	project := func(rec string) (string, string, bool) {
		v, err := decodeAny([]byte(rec))
		if err != nil {
			t.Fatal(err)
		}
		ch, data, ok := n.ProjectHost(v.(map[string]any), DefaultCapture)
		return ch, string(data), ok
	}
	ch, data, ok := project(`{"type":"extension_ui_request","id":"abc","method":"notify","message":"m","notifyType":"warning"}`)
	if !ok || ch != ChUI || data != `{"message":"m","method":"notify","notifyType":"warning"}` {
		t.Errorf("ui: %s %s %v", ch, data, ok)
	}
	_, data, _ = project(`{"type":"response","id":"eq-1","command":"new_session","success":true,"data":{"cancelled":true,"sessionId":"x","sessionFile":"/w/s"}}`)
	if data != `{"command":"new_session","data":{"cancelled":true},"success":true}` {
		t.Errorf("response: %s", data)
	}
	// Tool results are extension-authored: every key survives, including ones N4 drops elsewhere.
	_, data, _ = project(`{"type":"tool_execution_end","toolName":"t","isError":false,"toolCallId":"c1","result":{"content":[{"type":"text","text":"ok"}],"details":{"model":"m","usage":1}}}`)
	if !strings.Contains(data, `"model":"m"`) || !strings.Contains(data, `"usage":1`) || strings.Contains(data, "c1") {
		t.Errorf("tool_execution_end: %s", data)
	}
	if _, _, ok := project(`{"type":"message_update","x":1}`); ok {
		t.Error("message_update should not be captured")
	}
}

func TestDiffFindsFirstDivergence(t *testing.T) {
	ev := func(step, ch, data string) Event { return Event{Step: step, Ch: ch, Data: json.RawMessage(data)} }
	a := &Trace{Events: []Event{ev("s", ChUI, `{"a":1}`), ev("s", ChExec, `{"b":2}`)}}
	same := &Trace{Events: []Event{ev("s", ChUI, `{"a":1}`), ev("s", ChExec, `{"b":2}`)}}
	if d := Diff(a, same); d != nil {
		t.Fatalf("identical traces differ: %s", d)
	}
	reordered := &Trace{Events: []Event{ev("s", ChExec, `{"b":2}`), ev("s", ChUI, `{"a":1}`)}}
	if d := Diff(a, reordered); d == nil || d.Index != 0 {
		t.Errorf("reordering not detected: %v", d)
	}
	short := &Trace{Events: a.Events[:1]}
	if d := Diff(a, short); d == nil || d.Index != 1 || d.Got != nil {
		t.Errorf("missing event not detected: %v", d)
	}
	changed := &Trace{Events: []Event{a.Events[0], ev("other", ChExec, `{"b":2}`)}}
	if d := Diff(a, changed); d == nil || d.Index != 1 {
		t.Errorf("step change not detected: %v", d)
	}
}

func TestTraceRoundTrip(t *testing.T) {
	tr := &Trace{Header: Header{Scenario: "s", Lane: "l", Normalizer: NormalizerVersion}, Events: []Event{{Step: "1", Ch: ChUI, Data: json.RawMessage(`{"a":1}`)}}}
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := tr.WriteFile(path); err != nil {
		t.Fatal(err)
	}
	back, err := ReadTrace(path)
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(tr, back); d != nil || back.Header.Scenario != "s" {
		t.Errorf("round trip changed the trace: %v %+v", d, back.Header)
	}
	if err := os.WriteFile(path, []byte(`{"step":"1"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTrace(path); err == nil {
		t.Error("a file without a header was accepted")
	}
}

func TestFakeLLMScriptAndRecording(t *testing.T) {
	var recorded []string
	llm, err := newFakeLLM([]Turn{{Text: "hi"}, {ToolCalls: []ToolCall{{Name: "bash", Arguments: map[string]any{"command": "ls"}}}}},
		func(ch string, data any) { b, _ := json.Marshal(data); recorded = append(recorded, ch+" "+string(b)) })
	if err != nil {
		t.Fatal(err)
	}
	defer llm.close()
	post := func(body string) (int, string) {
		resp, err := http.Post(llm.baseURL()+"/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return resp.StatusCode, buf.String()
	}
	code, out := post(`{"messages":[{"role":"system","content":"s"},{"role":"user","content":"hello"}],"tools":[{"function":{"name":"read"}}]}`)
	if code != 200 || !strings.Contains(out, `"content":"hi"`) || !strings.HasSuffix(strings.TrimSpace(out), "[DONE]") {
		t.Errorf("turn 1: %d %s", code, out)
	}
	_, out = post(`{"messages":[{"role":"user","content":[{"type":"text","text":"again"}]}]}`)
	if !strings.Contains(out, `"finish_reason":"tool_calls"`) || !strings.Contains(out, `\"command\":\"ls\"`) {
		t.Errorf("turn 2: %s", out)
	}
	if code, _ = post(`{"messages":[]}`); code != 500 {
		t.Errorf("exhausted script returned %d", code)
	}
	if len(recorded) != 4 || recorded[0] != `llm {"messages":[{"role":"user","text":"hello"}],"tools":[{"name":"read"}]}` || !strings.HasPrefix(recorded[3], "error ") {
		t.Errorf("recorded = %q", recorded)
	}
}

// A port that keeps a tool's name but changes its description or schema, or
// that changes what it adds to the system prompt, changes what the model is
// told: the trace must show it.
func TestFakeLLMRecordsToolDefinitionsAndSystemDelta(t *testing.T) {
	var recorded []string
	llm, err := newFakeLLM([]Turn{{Text: "a"}, {Text: "b"}},
		func(ch string, data any) { b, _ := json.Marshal(data); recorded = append(recorded, string(b)) })
	if err != nil {
		t.Fatal(err)
	}
	defer llm.close()
	llm.system = func(text string) (any, bool) { return systemDelta("HOST PROMPT\n<cwd>", text) }
	post := func(body string) {
		resp, err := http.Post(llm.baseURL()+"/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	post(`{"messages":[{"role":"system","content":"HOST PROMPT\n<cwd>"},{"role":"user","content":"x"}],"tools":[{"function":{"name":"t","description":"d","parameters":{"type":"object","properties":{"n":{"type":"number"}}}}}]}`)
	post(`{"messages":[{"role":"system","content":"HOST PROMPT\nBe terse.\n<cwd>"},{"role":"user","content":"x"}]}`)
	if len(recorded) != 2 {
		t.Fatalf("recorded %q", recorded)
	}
	if want := `"tools":[{"description":"d","name":"t","parameters":{"properties":{"n":{"type":"number"}},"type":"object"}}]`; !strings.Contains(recorded[0], want) || strings.Contains(recorded[0], `"system"`) {
		t.Errorf("request 1 = %s", recorded[0])
	}
	if want := `"system":{"inserted":"Be terse.\n","removed":""}`; !strings.Contains(recorded[1], want) {
		t.Errorf("request 2 = %s, want %s", recorded[1], want)
	}
}

func TestSystemDelta(t *testing.T) {
	if _, ok := systemDelta("same", "same"); ok {
		t.Error("equal texts reported a delta")
	}
	cases := []struct{ base, got, want string }{
		{"base", "base\nextra", `{"inserted":"\nextra","removed":""}`},
		{"a <x> b", "a <y> b", `{"inserted":"y","removed":"x"}`},
		{"héllo wörld", "héllo 🌍 wörld", `{"inserted":"🌍 ","removed":""}`},
		{"host text", "custom", `{"inserted":"custom","removed":"host text"}`},
	}
	for _, c := range cases {
		v, ok := systemDelta(c.base, c.got)
		if got := string(canonical(v)); !ok || got != c.want {
			t.Errorf("systemDelta(%q, %q) = %s, want %s", c.base, c.got, got, c.want)
		}
	}
}

func TestExecCoverage(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) string {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ts := write("orig/ext.ts", "// pi.exec(\"commented\")\nconst r = await pi.exec(\"git\", [\"status\"]);\nexecSync(\"tmux ls\");\n")
	goDir := filepath.Join(dir, "port")
	write("port/ext.go", "package p\nfunc f(ctx sdk.Context) { ctx.Exec(\"git\", nil); exec.CommandContext(c, \"tmux\", \"ls\") }\n")
	write("port/ext_test.go", "package p\nfunc g() { exec.Command(\"notcounted\") }\n")
	scs := []*Scenario{{Name: "a", Commands: map[string]CommandSpec{"git": {}}}, {Name: "b", Commands: map[string]CommandSpec{"tmux": {Mode: "canned"}}}}
	r, err := execCoverage(scs, ts, goDir)
	if err != nil || !r.Pass {
		t.Fatalf("declared commands failed: %v %+v", err, r)
	}
	if !strings.Contains(r.Detail, "git, tmux") {
		t.Errorf("detail = %s", r.Detail)
	}
	// The port starts a process the original does not, and nobody declared it.
	write("port/extra.go", "package p\nfunc h(ctx sdk.Context) { ctx.Exec(\"touch\", []string{\"x\"}) }\n")
	if r, _ := execCoverage(scs, ts, goDir); r.Pass || !strings.Contains(r.Detail, `"touch"`) {
		t.Errorf("undeclared command passed: %+v", r)
	}
	_ = os.Remove(filepath.Join(goDir, "extra.go"))
	// A path bypasses the PATH shims.
	write("port/abs.go", "package p\nfunc h(ctx sdk.Context) { ctx.Exec(\"/usr/bin/git\", nil) }\n")
	if r, _ := execCoverage(scs, ts, goDir); r.Pass || !strings.Contains(r.Detail, "by path") {
		t.Errorf("absolute command passed: %+v", r)
	}
}

// A command the original starts in code no scenario reaches (a development launcher, a feature the
// first slice defers, an SQL statement handed to a database driver's exec) is named and excused by
// its owner in accepted-gaps.json under "exec:<name>", the same way an SDK gap is. It stays listed.
func TestExecCoverageAcceptsANamedCommandWithItsReason(t *testing.T) {
	dir := t.TempDir()
	ts := filepath.Join(dir, "ext.ts")
	if err := os.WriteFile(ts, []byte("await pi.exec(\"git\", []);\nexecSync(\"sqlite3 db\");\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scs := []*Scenario{{Name: "a", Commands: map[string]CommandSpec{"git": {}}}}
	if r, _ := execCoverageAccepting(scs, nil, ts); r.Pass || !strings.Contains(r.Detail, `"sqlite3"`) {
		t.Fatalf("an undeclared command passed: %+v", r)
	}
	accepted := map[string]string{"exec:sqlite3": "cookie extraction is a deferred slice; no scenario reaches it"}
	r, err := execCoverageAccepting(scs, accepted, ts)
	if err != nil || !r.Pass {
		t.Fatalf("an accepted command failed: %v %+v", err, r)
	}
	if !strings.Contains(r.Detail, "sqlite3") || !strings.Contains(r.Detail, "deferred slice") {
		t.Errorf("the accepted command and its reason must stay listed: %q", r.Detail)
	}
	// An acceptance for another command excuses nothing.
	if r, _ := execCoverageAccepting(scs, map[string]string{"exec:tmux": "x"}, ts); r.Pass {
		t.Errorf("an unrelated acceptance excused sqlite3: %+v", r)
	}
	// An acceptance never excuses a path, which the PATH shims cannot record.
	if err := os.WriteFile(ts, []byte("execSync(\"/usr/bin/sqlite3 db\");\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r, _ := execCoverageAccepting(scs, map[string]string{"exec:/usr/bin/sqlite3": "x", "exec:sqlite3": "x"}, ts); r.Pass {
		t.Errorf("a path was excused: %+v", r)
	}
}

func TestGapScannerSeesIndirectBusAccessAndDirectories(t *testing.T) {
	for _, src := range []string{
		"const bus = pi.events;\nbus.on(\"x\", f)",
		"const { events } = pi;\nevents.on(\"x\", f)",
		"pi[\"events\"].emit(\"x\")",
		"pi.events?.on(\"x\", f)",
	} {
		gaps := ScanGaps(src, "")
		found := false
		for _, g := range gaps {
			if g.Symbol == IndirectBusSymbol && g.Severity == "missing" {
				found = true
			}
		}
		if !found {
			t.Errorf("indirect bus use not flagged: %q -> %+v", src, gaps)
		}
	}
	// Direct use is reported once, by its own row.
	for _, g := range ScanGaps(`pi.events.on("x", f)`, "") {
		if g.Symbol == IndirectBusSymbol {
			t.Errorf("direct use reported twice: %+v", g)
		}
	}
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "lib"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "index.ts"), []byte(`import { wire } from "./lib/bus"; export default function (pi) { wire(pi); }`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "lib", "bus.ts"), []byte("export function wire(pi) {\n\tpi.events.on(\"herdr:blocked\", () => {});\n}\n"), 0o644)
	gaps, err := ScanGapsPath(dir, "")
	if err != nil || len(gaps) != 1 || gaps[0].Symbol != "pi.events.on" || gaps[0].File != "lib/bus.ts" || gaps[0].Lines[0] != 2 {
		t.Errorf("multi-file extension: %v %+v", err, gaps)
	}
	if open := Unaccepted(gaps, map[string]string{"pi.events.on": "owner, issue #1"}); len(open) != 0 {
		t.Errorf("accepted gap still blocks: %+v", open)
	}
}

func TestTailDefault(t *testing.T) {
	for in, want := range map[int]int{0: 1000, -1: 0, 250: 250} {
		if got := (&Scenario{TailMs: in}).tail(); got != want {
			t.Errorf("tail(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestGapScannerFlagsMissingAndPartialAPIs(t *testing.T) {
	src := `
// pi.events.on("x") in a comment is not a use
export default function (pi) {
	pi.events.on("herdr:blocked", () => {});
	pi.registerCommand("c", { handler: async (_a, ctx) => {
		await ctx.newSession({ withSession: async (c) => c.ui.notify("x") });
		await ctx.ui.select("t", ["a"], { timeout: 5 });
		await ctx.ui.select("t", ["a"]);
	}});
	pi.on("cache_warming_decision", () => {});
}`
	gaps := ScanGaps(src, "")
	got := map[string]Gap{}
	for _, g := range gaps {
		got[g.Symbol] = g
	}
	for _, sym := range []string{"pi.events.on", "ctx.newSession(options.withSession)", `pi.on("cache_warming_decision")`} {
		if g, ok := got[sym]; !ok || g.Severity != "missing" {
			t.Errorf("%s not flagged as missing: %+v", sym, g)
		}
	}
	if g, ok := got["pi.events.on"]; !ok || len(g.Lines) != 1 || g.Lines[0] != 4 {
		t.Errorf("pi.events.on lines = %v, want [4] (the comment must not count)", g.Lines)
	}
	if g, ok := got["ctx.ui.select(opts.timeout)"]; !ok || g.Severity != "partial" {
		t.Errorf("select timeout: %+v", g)
	}
	if !Blocking(gaps) {
		t.Error("missing gaps must block")
	}
	clean := `export default function (pi) { pi.on("session_start", async (_e, ctx) => { ctx.ui.notify("x", "info"); await pi.exec("git", ["status"]); }); }`
	if g := ScanGaps(clean, ""); len(g) != 0 {
		t.Errorf("clean source flagged: %+v", g)
	}
	// pi.events.emit is the other half of the bus.
	if g := ScanGaps(`pi.events.emit("x", 1)`, ""); len(g) != 1 || g[0].Symbol != "pi.events.emit" {
		t.Errorf("emit: %+v", g)
	}
}

func TestMutationListValidation(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "m.json")
		_ = os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	if _, err := LoadMutations(write(`[{"name":"a","file":"f.go","find":"x","replace":"y"}]`)); err != nil {
		t.Errorf("valid list: %v", err)
	}
	for name, body := range map[string]string{
		"no-op":     `[{"name":"a","file":"f.go","find":"x","replace":"x"}]`,
		"duplicate": `[{"name":"a","file":"f.go","find":"x","replace":"y"},{"name":"a","file":"f.go","find":"x","replace":"z"}]`,
		"no name":   `[{"file":"f.go","find":"x","replace":"y"}]`,
	} {
		if _, err := LoadMutations(write(body)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestStripCommentsKeepsLinesAndStrings(t *testing.T) {
	got := stripComments("a // x\n/* y\nz */ b \"//not\"\n")
	if got != "a \n\n b \"//not\"\n" {
		t.Errorf("got %q", got)
	}
}

func TestShimRecordsCallsAndPlaysCannedAndRealCommands(t *testing.T) {
	root, err := os.MkdirTemp("", "eqt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	var got []string
	var mu sync.Mutex
	record := func(ch string, data any) {
		b, _ := json.Marshal(data)
		mu.Lock()
		got = append(got, ch+" "+string(b))
		mu.Unlock()
	}
	specs := map[string]CommandSpec{"fakecmd": {Mode: "canned", Stdout: "out", Stderr: "err", Exit: 3}, "sh": {Mode: "real"}}
	s, err := newShimSet(root, specs, os.Getenv("PATH"), record)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	run := func(name string, args ...string) (string, int) {
		cmd := exec.Command(filepath.Join(s.dir, name), args...)
		cmd.Env = append(os.Environ(), "EQ_SHIM_SOCK="+s.sockPath)
		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return out.String(), code
	}
	if out, code := run("fakecmd", "a", "b"); out != "out" || code != 3 {
		t.Errorf("canned: %q %d", out, code)
	}
	if out, code := run("sh", "-c", "printf real; exit 7"); out != "real" || code != 7 {
		t.Errorf("real: %q %d", out, code)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{`exec {"args":["a","b"],"cmd":"fakecmd"`, `exec_end {"cmd":"fakecmd","exit":3}`, `exec {"args":["-c","printf real; exit 7"],"cmd":"sh"`, `exec_end {"cmd":"sh","exit":7}`}
	if len(got) != 4 {
		t.Fatalf("recorded %q", got)
	}
	for i, w := range want {
		if !strings.HasPrefix(got[i], w) {
			t.Errorf("record %d = %s, want prefix %s", i, got[i], w)
		}
	}
	if p := pathWithout("/bin"+string(os.PathListSeparator)+"/nonexistent", []string{"sh"}); strings.Contains(p, "/bin") {
		t.Errorf("pathWithout kept a directory holding the command: %q", p)
	}
}

// Go-original mode: a relocation of a Go extension has no Pi oracle. The original Go
// extension under PiG is the oracle lane (recorded as pig-go-upstream); the config must
// not also name a TypeScript original or Pi, and needs PiG.
func TestGoOracleModeConfig(t *testing.T) {
	if err := (Config{GoOracle: "/o", Pig: "/pig"}).CheckGoOracle(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for name, cfg := range map[string]Config{
		"with a TypeScript original": {GoOracle: "/o", TS: "/x.ts", Pig: "/pig"},
		"with pi":                    {GoOracle: "/o", Pi: "/pi", Pig: "/pig"},
		"without pig":                {GoOracle: "/o"},
	} {
		if err := cfg.CheckGoOracle(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if GoOracleLane != "pig-go-upstream" {
		t.Errorf("lane name = %q", GoOracleLane)
	}
}

// A mutant of an extension that shares a library module through go.work must still resolve
// it: the copy keeps the relative layout of every module the go.work uses.
func TestMutantTreeKeepsWorkspaceSiblings(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lib/go.mod", "module example.com/lib\n\ngo 1.26\n")
	write("lib/sub/lib.go", "package sub\n")
	write("game/extensions/thing/go.mod", "module example.com/thing\n\ngo 1.26\n")
	write("game/extensions/thing/go.work", "go 1.26\n\nuse (\n\t.\n\t../../../lib\n)\n")
	write("game/extensions/thing/a.go", "package thing\n")

	dir, modules, cleanup, err := prepareMutantTree(filepath.Join(root, "game/extensions/thing"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if filepath.Base(dir) != "thing" {
		t.Errorf("the copy must keep the extension's directory name, got %s", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.go")); err != nil {
		t.Errorf("port file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "../../../lib/sub/lib.go")); err != nil {
		t.Errorf("the sibling module is not where go.work says: %v", err)
	}
	if len(modules) != 1 || filepath.Base(modules[0]) != "lib" {
		t.Errorf("sibling modules = %v", modules)
	}
	// A port with no go.work needs no siblings.
	write("plain/go.mod", "module example.com/plain\n\ngo 1.26\n")
	dir2, modules2, cleanup2, err := prepareMutantTree(filepath.Join(root, "plain"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup2()
	if len(modules2) != 0 || filepath.Base(dir2) != "plain" {
		t.Errorf("plain port: %s %v", dir2, modules2)
	}
}

func TestGoWorkUses(t *testing.T) {
	got := goWorkUses("go 1.26\n\n// comment\nuse ./one // trailing\nuse (\n\t.\n\t../../../pig-play\n\t\"quoted dir\"\n)\nreplace a => b\n")
	want := []string{"./one", ".", "../../../pig-play", "quoted dir"}
	if len(got) != len(want) {
		t.Fatalf("uses = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("use %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// pigpen-acp had to copy the scripted model server because it is package-private. `pigeq llm`
// serves it standalone: a port whose test drives a real host or adapter end to end gets the same
// scripted, recording server the scenarios use.
func TestServeLLMServesTheScriptAndLogsRequests(t *testing.T) {
	var log bytes.Buffer
	url, stop, err := ServeLLM([]Turn{{Text: "scripted"}}, &log)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.HasSuffix(url, "/v1") {
		t.Errorf("url = %q", url)
	}
	resp, err := http.Post(url+"/chat/completions", "application/json", strings.NewReader(`{"messages":[{"role":"user","content":"q"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, _ = out.ReadFrom(resp.Body)
	resp.Body.Close()
	if !strings.Contains(out.String(), `"content":"scripted"`) {
		t.Errorf("response = %s", out.String())
	}
	if l := log.String(); !strings.Contains(l, `"ch":"llm"`) || !strings.Contains(l, `"text":"q"`) || strings.Count(l, "\n") != 1 {
		t.Errorf("log = %q", l)
	}
}

// A mutation may edit the port copy or a copy of a module its go.work uses, and nothing
// else: the path in mutations.json is joined to the scratch copy, so enough "../" reaches
// any file on the machine (the equivalence_run tool runs mutate for a model, too).
func TestMutationCannotEditOutsideTheMutantTree(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) string {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("lib/go.mod", "module example.com/lib\n\ngo 1.26\n")
	write("lib/lib.go", "package lib\n\nconst V = 1\n")
	write("game/extensions/thing/go.mod", "module example.com/thing\n\ngo 1.26\n")
	write("game/extensions/thing/go.work", "go 1.26\n\nuse (\n\t.\n\t../../../lib\n)\n")
	write("game/extensions/thing/a.go", "package thing\n\nconst A = 1\n")
	outside := write("outside.txt", "secret X\n")
	cfg := Config{Go: filepath.Join(root, "game/extensions/thing"), Pig: "pig"}

	escape := strings.Repeat("../", 64) + strings.TrimPrefix(filepath.ToSlash(outside), "/")
	for name, file := range map[string]string{
		"climbs out of the scratch copy": escape,
		"next to the port, not a module": "../other/x.go",
	} {
		m := []Mutation{{Name: "escape", File: file, Find: "secret X", Replace: "changed"}}
		if _, err := Mutate(nil, cfg, t.TempDir(), m, nil); err == nil {
			t.Errorf("%s: a mutation outside the port and its workspace modules was accepted", name)
		}
	}
	if data, _ := os.ReadFile(outside); string(data) != "secret X\n" {
		t.Fatalf("a mutation edited a file outside the mutant tree: %q", data)
	}
	// The port and its go.work modules stay editable.
	for _, file := range []string{"a.go", "../../../lib/lib.go"} {
		find := map[string]string{"a.go": "A = 1", "../../../lib/lib.go": "V = 1"}[file]
		m := []Mutation{{Name: "inside", File: file, Find: find, Replace: find + "0"}}
		if _, err := Mutate(nil, cfg, t.TempDir(), m, nil); err != nil {
			t.Errorf("%s: %v", file, err)
		}
	}
}
