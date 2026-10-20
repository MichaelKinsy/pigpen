package pi_goal_x

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The test binary stands in for pig (GOALAUDIT_FAKE=1), like pi-subagents' PISUB_HELPER: it records its arguments,
// environment and stdin, prints a scripted JSON-Lines stream, and can fail, hang or ignore SIGTERM.
//
//	GOALAUDIT_OUT        file that receives {"args","env","stdin"}
//	GOALAUDIT_SCRIPT     file whose bytes are written to stdout
//	GOALAUDIT_STDERR     text written to stderr
//	GOALAUDIT_EXIT       exit code
//	GOALAUDIT_SLEEP      seconds to hang after the script
//	GOALAUDIT_IGNORE_TERM=1  ignore SIGTERM while hanging
//	GOALAUDIT_PIDFILE    file that receives the pid
func init() {
	if os.Getenv("GOALAUDIT_FAKE") != "1" {
		return
	}
	if f := os.Getenv("GOALAUDIT_PIDFILE"); f != "" {
		_ = os.WriteFile(f, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}
	if os.Getenv("GOALAUDIT_IGNORE_TERM") == "1" {
		signal.Ignore(osSIGTERM)
	}
	stdin, _ := io.ReadAll(os.Stdin)
	if f := os.Getenv("GOALAUDIT_OUT"); f != "" {
		rec, _ := json.Marshal(map[string]any{"args": os.Args[1:], "env": os.Environ(), "stdin": string(stdin)})
		_ = os.WriteFile(f, rec, 0o600)
	}
	if f := os.Getenv("GOALAUDIT_SCRIPT"); f != "" {
		b, _ := os.ReadFile(f)
		_, _ = os.Stdout.Write(b)
	}
	if s := os.Getenv("GOALAUDIT_STDERR"); s != "" {
		_, _ = os.Stderr.WriteString(s)
	}
	if s := os.Getenv("GOALAUDIT_SLEEP"); s != "" {
		n, _ := strconv.Atoi(s)
		time.Sleep(time.Duration(n) * time.Second)
	}
	code, _ := strconv.Atoi(os.Getenv("GOALAUDIT_EXIT"))
	os.Exit(code)
}

type fakePig struct {
	t       *testing.T
	out     string
	factory *pigAuditorFactory
	env     []string
}

// newFakePig builds a factory that starts this test binary as pig; script is what it prints on stdout.
func newFakePig(t *testing.T, script string, env ...string) *fakePig {
	t.Helper()
	dir := t.TempDir()
	p := &fakePig{t: t, out: filepath.Join(dir, "out.json")}
	sf := filepath.Join(dir, "script.jsonl")
	if err := os.WriteFile(sf, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p.env = append([]string{"GOALAUDIT_FAKE=1", "GOALAUDIT_OUT=" + p.out, "GOALAUDIT_SCRIPT=" + sf}, env...)
	p.factory = &pigAuditorFactory{
		bin:     func() string { return self },
		environ: func() []string { return append(append([]string{}, os.Environ()...), p.env...) },
		grace:   300 * time.Millisecond,
	}
	return p
}

type fakePigRecord struct {
	Args  []string
	Env   []string
	Stdin string
}

func (p *fakePig) record() fakePigRecord {
	p.t.Helper()
	b, err := os.ReadFile(p.out)
	if err != nil {
		p.t.Fatalf("the fake pig did not run: %v", err)
	}
	var r fakePigRecord
	if err := json.Unmarshal(b, &r); err != nil {
		p.t.Fatal(err)
	}
	return r
}

func (r fakePigRecord) envOf(k string) (string, bool) {
	for _, kv := range r.Env {
		if key, v, _ := strings.Cut(kv, "="); key == k {
			return v, true
		}
	}
	return "", false
}

func auditStream(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

const (
	evSession   = `{"type":"session","version":3,"id":"s","timestamp":"2026-01-15T14:00:00Z","cwd":"/w"}`
	evAgent     = `{"type":"agent_start"}`
	evMsgStart  = `{"type":"message_start","message":{"role":"assistant","content":[]}}`
	evTextStart = `{"type":"message_update","assistantMessageEvent":{"type":"text_start","contentIndex":0}}`
	evTextDelta = `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"Checked.\nAll good."}}`
	evTextEnd   = `{"type":"message_update","assistantMessageEvent":{"type":"text_end","contentIndex":0,"content":"Checked.\nAll good."}}`
	evToolStart = `{"type":"tool_execution_start","toolCallId":"c1","toolName":"grep","args":{"pattern":"x"}}`
	evToolEnd   = `{"type":"tool_execution_end","toolCallId":"c1","toolName":"grep","result":{"content":[]},"isError":false}`
	evMsgEnd    = `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Checked.\nAll good.\n<approved/>"}],"stopReason":"stop"}}`
	evSettled   = `{"type":"agent_settled","aborted":false}`
)

func approvedScript() string {
	return auditStream(evSession, evAgent, evMsgStart, evTextStart, evTextDelta, evTextEnd, evToolStart, evToolEnd, evMsgEnd, evSettled)
}

func auditCtx(t *testing.T) *fakeAuditorCtx {
	return &fakeAuditorCtx{dir: t.TempDir(), mdl: &auditorModel{provider: "anthropic", id: "claude-x"}}
}

func TestPigAuditorBuildArgs(t *testing.T) {
	loader := makeAuditorResourceLoader()
	base := auditorSessionOptions{
		model: &auditorModel{provider: "anthropic", id: "claude-x"}, thinkingLevel: "high",
		tools: []string{"read", "grep", "find", "ls", "bash"}, resourceLoader: loader,
	}
	cases := []struct {
		name string
		opts auditorSessionOptions
		want []string
	}{
		{"isolated auditor", base, []string{"--mode", "json", "--no-session", "--no-mcp", "--provider", "anthropic", "--model", "claude-x",
			"--thinking", "high", "--tools", "read,grep,find,ls,bash", "--system-prompt", loader.getSystemPrompt(),
			"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--no-approve"}},
		{"no model, no thinking", auditorSessionOptions{tools: []string{"read"}, resourceLoader: &auditorResourceLoader{systemPrompt: "S"}},
			[]string{"--mode", "json", "--no-session", "--no-mcp", "--tools", "read", "--system-prompt", "S",
				"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--no-approve"}},
		{"project resources", auditorSessionOptions{model: base.model, tools: []string{"read", "bash"}, resourceLoaderOptions: &auditorResourceLoaderOptions{}},
			[]string{"--mode", "json", "--no-session", "--no-mcp", "--provider", "anthropic", "--model", "claude-x", "--tools", "read,bash"}},
		{"partly isolated options", auditorSessionOptions{tools: []string{"read"}, resourceLoaderOptions: &auditorResourceLoaderOptions{noSkills: true, noThemes: true}},
			[]string{"--mode", "json", "--no-session", "--no-mcp", "--tools", "read", "--no-skills", "--no-themes"}},
		{"custom tools are not in the allowlist", auditorSessionOptions{
			tools: []string{"read", "submit_goal_oracle_advice"}, customTools: []*goalOracleTool{{name: "submit_goal_oracle_advice"}}, resourceLoader: &auditorResourceLoader{systemPrompt: "O"}},
			[]string{"--mode", "json", "--no-session", "--no-mcp", "--tools", "read", "--system-prompt", "O",
				"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--no-approve"}},
		{"only custom tools", auditorSessionOptions{tools: []string{"x"}, customTools: []*goalOracleTool{{name: "x"}}},
			[]string{"--mode", "json", "--no-session", "--no-mcp", "--no-tools"}},
	}
	for _, c := range cases {
		if got := buildPigAuditorArgs(c.opts); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, c.want)
		}
	}
}

func TestPigAuditorEnv(t *testing.T) {
	got := pigAuditorEnv([]string{"PATH=/bin", "PIG_EXT_SOCKET=/s", "PIG_EXT_CELL=c", "PIG_HARNESS_BINARY=/pig", "PIG_HARNESS_ARGS=a",
		"PIG_SUBAGENT=1", "PIG_AGENT_ROLE=r", "PI_SUBAGENT_CHILD=0", "PIG_SUBAGENT_DEPTH=2", "HOME=/h"})
	want := []string{"PATH=/bin", "HOME=/h", "PI_SUBAGENT_CHILD=1", "PIG_SUBAGENT=1", "PIG_SUBAGENT_DEPTH=3", "PI_SKIP_VERSION_CHECK=1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q want %q", got, want)
	}
	if d := pigAuditorEnv(nil); d[len(d)-2] != "PIG_SUBAGENT_DEPTH=1" {
		t.Errorf("depth without a parent: %q", d)
	}
	if d := pigAuditorEnv([]string{"PIG_SUBAGENT_DEPTH=bad"}); d[len(d)-2] != "PIG_SUBAGENT_DEPTH=1" {
		t.Errorf("depth of a bad value: %q", d)
	}
}

func TestPigAuditBinary(t *testing.T) {
	t.Setenv("PIG_HARNESS_BINARY", " /opt/pig ")
	if got := pigAuditBinary(); got != "/opt/pig" {
		t.Errorf("got %q", got)
	}
	t.Setenv("PIG_HARNESS_BINARY", "")
	if got := pigAuditBinary(); got == "" {
		t.Error("no fallback binary")
	}
}

// The auditor run end to end on a child: its prompt travels on stdin, the stream becomes progress and the report.
func TestPigAuditorRunsTheAuditorOnAChild(t *testing.T) {
	p := newFakePig(t, approvedScript())
	var seen []auditorProgress
	res := runGoalCompletionAuditor(runGoalCompletionAuditorArgs{
		ctx: auditCtx(t), goal: auditorGoal(t, "Finish X"), detailedSummary: "d", createSession: p.factory,
		onProgress: func(a auditorProgress) { seen = append(seen, a) },
	})
	if !res.approved || res.disapproved || res.err != "" {
		t.Fatalf("result %+v", res)
	}
	eq(t, res.output, "Checked.\nAll good.\n<approved/>", "output")
	eq(t, res.model, "anthropic/claude-x", "model label")
	rec := p.record()
	if !strings.Contains(rec.Stdin, "Finish X") {
		t.Errorf("the auditor prompt did not reach the child: %q", rec.Stdin)
	}
	for _, a := range rec.Args {
		if a == "Finish X" {
			t.Error("the prompt is an argument")
		}
	}
	var sawGrep, sawReport, sawText bool
	for _, a := range seen {
		sawGrep = sawGrep || (a.currentTool == "grep" && a.phase == "tool_executing" && a.currentToolArgs == `{"pattern":"x"}`)
		sawReport = sawReport || a.label == "Producing report..."
		// message_update carries the partial text rebuilt from text_delta.
		sawText = sawText || reflect.DeepEqual(a.recentOutput, []string{"Checked.", "All good."})
	}
	if !sawGrep || !sawReport || !sawText {
		t.Errorf("progress lost the child's events (grep %v report %v text %v): %+v", sawGrep, sawReport, sawText, seen)
	}
	last := seen[len(seen)-1]
	eq(t, last.phase, "done", "final phase")
}

func TestPigAuditorChildEnvironmentAndFlags(t *testing.T) {
	p := newFakePig(t, approvedScript(), "PIG_EXT_SOCKET=/secret", "PIG_HARNESS_ARGS=x", "PIG_SUBAGENT_DEPTH=1")
	res := runGoalCompletionAuditor(runGoalCompletionAuditorArgs{
		ctx: auditCtx(t), goal: auditorGoal(t, "x"), detailedSummary: "d", createSession: p.factory,
	})
	if !res.approved {
		t.Fatalf("%+v", res)
	}
	rec := p.record()
	for _, k := range []string{"PIG_EXT_SOCKET", "PIG_HARNESS_ARGS"} {
		if _, ok := rec.envOf(k); ok {
			t.Errorf("%s leaked into the child", k)
		}
	}
	for k, want := range map[string]string{"PI_SUBAGENT_CHILD": "1", "PIG_SUBAGENT": "1", "PIG_SUBAGENT_DEPTH": "2"} {
		if v, _ := rec.envOf(k); v != want {
			t.Errorf("%s=%q, want %q", k, v, want)
		}
	}
	joined := " " + strings.Join(rec.Args, " ") + " "
	for _, f := range []string{" --mode json ", " --no-session ", " --provider anthropic --model claude-x ", " --tools read,grep,find,ls,bash ", " --no-extensions "} {
		if !strings.Contains(joined, f) {
			t.Errorf("flag %q missing in %q", f, joined)
		}
	}
}

func TestPigAuditorProjectResourcesLoadExtensions(t *testing.T) {
	p := newFakePig(t, approvedScript())
	cfg := &goalSettings{auditorProjectResources: true}
	res := runGoalCompletionAuditor(runGoalCompletionAuditorArgs{
		ctx: auditCtx(t), goal: auditorGoal(t, "x"), detailedSummary: "d", createSession: p.factory, settings: cfg,
	})
	if !res.approved {
		t.Fatalf("%+v", res)
	}
	for _, a := range p.record().Args {
		switch a {
		case "--no-extensions", "--no-skills", "--no-themes", "--no-prompt-templates", "--no-context-files", "--system-prompt", "--no-approve":
			t.Errorf("project resources keep %s", a)
		}
	}
}

func TestPigAuditorMalformedLinesAreSkipped(t *testing.T) {
	script := auditStream(evSession, "not json at all", "", "[1,2]", `"str"`, `{"no":"type"}`, `{"type":"unknown_thing"}`,
		`{"type":"message_update"}`, `{"type":"message_end"}`, `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":-3,"delta":"x"}}`,
		evAgent, `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"ok\n<disapproved/>"}]}}`) + `{"type":"agent_settled"`
	p := newFakePig(t, script)
	res := runGoalCompletionAuditor(runGoalCompletionAuditorArgs{ctx: auditCtx(t), goal: auditorGoal(t, "x"), detailedSummary: "d", createSession: p.factory})
	if res.err != "" || !res.disapproved || res.approved {
		t.Fatalf("%+v", res)
	}
	eq(t, res.output, "ok\n<disapproved/>", "output")
}

func TestPigAuditorLastLineWithoutNewlineIsRead(t *testing.T) {
	p := newFakePig(t, strings.TrimSuffix(auditStream(evAgent, evMsgEnd), "\n"))
	res := runGoalCompletionAuditor(runGoalCompletionAuditorArgs{ctx: auditCtx(t), goal: auditorGoal(t, "x"), detailedSummary: "d", createSession: p.factory})
	if !res.approved {
		t.Fatalf("%+v", res)
	}
}

func TestPigAuditorCrashReportsStderr(t *testing.T) {
	p := newFakePig(t, auditStream(evAgent), "GOALAUDIT_EXIT=3", "GOALAUDIT_STDERR=provider exploded\n")
	res := runGoalCompletionAuditor(runGoalCompletionAuditorArgs{ctx: auditCtx(t), goal: auditorGoal(t, "x"), detailedSummary: "d", createSession: p.factory})
	if res.approved || !res.disapproved {
		t.Fatalf("%+v", res)
	}
	eq(t, res.err, "pig exited with code 3: provider exploded", "error")
}

func TestPigAuditorCrashWithoutStderr(t *testing.T) {
	p := newFakePig(t, "", "GOALAUDIT_EXIT=7")
	res := runGoalCompletionAuditor(runGoalCompletionAuditorArgs{ctx: auditCtx(t), goal: auditorGoal(t, "x"), detailedSummary: "d", createSession: p.factory})
	eq(t, res.err, "pig exited with code 7", "error")
}

func TestPigAuditorNoPig(t *testing.T) {
	f := &pigAuditorFactory{bin: func() string { return filepath.Join(t.TempDir(), "no-such-pig") }}
	res := runGoalCompletionAuditor(runGoalCompletionAuditorArgs{ctx: auditCtx(t), goal: auditorGoal(t, "x"), detailedSummary: "d", createSession: f})
	if res.approved || !res.disapproved || !strings.HasPrefix(res.err, "could not start pig: ") {
		t.Fatalf("%+v", res)
	}
	// dispose after a failed start must not hang on the exit channel.
	s, _ := f.createSession(auditorSessionOptions{})
	if err := s.prompt("x"); err == nil {
		t.Error("no error for a missing pig")
	}
	s.dispose()
}

func TestPigAuditorPromptTwiceAndAfterAbort(t *testing.T) {
	p := newFakePig(t, approvedScript())
	s, _ := p.factory.createSession(auditorSessionOptions{tools: []string{"read"}})
	if err := s.prompt("one"); err != nil {
		t.Fatal(err)
	}
	if err := s.prompt("two"); err == nil {
		t.Error("a second prompt ran")
	}
	s.dispose()
	s.dispose()

	q := newFakePig(t, approvedScript())
	a, _ := q.factory.createSession(auditorSessionOptions{})
	a.abort()
	if err := a.prompt("x"); err != nil {
		t.Errorf("prompt after abort: %v", err)
	}
	if _, err := os.Stat(q.out); err == nil {
		t.Error("an aborted session started pig")
	}
}

func TestPigAuditorUnsubscribe(t *testing.T) {
	p := newFakePig(t, approvedScript())
	s, _ := p.factory.createSession(auditorSessionOptions{})
	var kept, dropped []string
	s.subscribe(func(e auditorSessionEvent) { kept = append(kept, e.typ) })
	off := s.subscribe(func(e auditorSessionEvent) { dropped = append(dropped, e.typ) })
	off()
	off()
	if err := s.prompt("x"); err != nil {
		t.Fatal(err)
	}
	s.dispose()
	if len(dropped) != 0 {
		t.Errorf("unsubscribed listener got %v", dropped)
	}
	want := []string{"agent_start", "message_update", "message_update", "message_update", "tool_execution_start", "tool_execution_end", "message_end"}
	if !reflect.DeepEqual(kept, want) {
		t.Errorf("events %v, want %v", kept, want)
	}
}

func TestPigEventMapper(t *testing.T) {
	m := &pigEventMapper{}
	map1 := func(l string) (auditorSessionEvent, bool, string) { return m.mapLine([]byte(l)) }
	texts := func(e auditorSessionEvent) []string {
		var out []string
		for _, p := range e.message.content {
			if p.typ == "text" {
				out = append(out, *p.text)
			} else {
				out = append(out, "<"+p.typ+">")
			}
		}
		return out
	}
	if _, ok, _ := map1(evMsgStart); ok {
		t.Error("message_start is not forwarded")
	}
	e, ok, _ := map1(`{"type":"message_update","assistantMessageEvent":{"type":"thinking_start","contentIndex":0}}`)
	if !ok || e.streamType != "thinking_start" || !reflect.DeepEqual(texts(e), []string{"<other>"}) {
		t.Errorf("thinking_start: %+v", e)
	}
	map1(`{"type":"message_update","assistantMessageEvent":{"type":"text_start","contentIndex":1}}`)
	map1(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":1,"delta":"ab"}}`)
	e, _, _ = map1(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":1,"delta":"cd"}}`)
	if e.message.role != "assistant" || !reflect.DeepEqual(texts(e), []string{"<other>", "abcd"}) {
		t.Errorf("deltas accumulate per index: %v", texts(e))
	}
	e, _, _ = map1(`{"type":"message_update","assistantMessageEvent":{"type":"text_end","contentIndex":1,"content":"final"}}`)
	if !reflect.DeepEqual(texts(e), []string{"<other>", "final"}) {
		t.Errorf("text_end replaces the text: %v", texts(e))
	}
	// A new assistant message starts a new partial.
	map1(evMsgStart)
	e, _, _ = map1(evTextStart)
	if !reflect.DeepEqual(texts(e), []string{""}) {
		t.Errorf("partial survived message_start: %v", texts(e))
	}
	// A user message_start does not reset it.
	map1(`{"type":"message_start","message":{"role":"user"}}`)
	e, _, _ = map1(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"z"}}`)
	if !reflect.DeepEqual(texts(e), []string{"z"}) {
		t.Errorf("user message_start reset the partial: %v", texts(e))
	}
	// Huge content indexes are not allocated.
	e, ok, _ = map1(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":99999999,"delta":"z"}}`)
	if !ok || len(e.message.content) != 1 {
		t.Errorf("content index bound: %+v", e)
	}
	// message_end: text parts, the joined text of an assistant message, string content.
	e, ok, txt := map1(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hm"},{"type":"text","text":"a"},{"type":"text","text":"b"}]}}`)
	if !ok || txt != "a\nb" || e.message.role != "assistant" || len(e.message.content) != 3 {
		t.Errorf("message_end: %+v %q", e, txt)
	}
	e, _, txt = map1(`{"type":"message_end","message":{"role":"user","content":"plain"}}`)
	if txt != "" || e.message.role != "user" || *e.message.content[0].text != "plain" {
		t.Errorf("user message_end: %+v %q", e, txt)
	}
	// tool events.
	e, _, _ = map1(evToolStart)
	if e.typ != "tool_execution_start" || e.toolName != "grep" || auditorArgsText(e.args) != `{"pattern":"x"}` {
		t.Errorf("tool start: %+v", e)
	}
	e, _, _ = map1(evToolEnd)
	if e.typ != "tool_execution_end" || e.toolName != "grep" {
		t.Errorf("tool end: %+v", e)
	}
	for _, skipped := range []string{evSession, evSettled, `{"type":"turn_start"}`, `{"type":"message_update"}`, `{"type":"message_end"}`, "[]", "x"} {
		if _, ok, _ := map1(skipped); ok {
			t.Errorf("%s was forwarded", skipped)
		}
	}
}

func TestPigLineWriter(t *testing.T) {
	var lines []string
	w := &pigLineWriter{onLine: func(l []byte) { lines = append(lines, string(l)) }, maxLine: 8}
	w.Write([]byte("ab"))
	w.Write([]byte("c\r\n\n12345678\nlongerthan8chars"))
	w.Write([]byte(" still long\nok\n"))
	w.Write([]byte("tail"))
	w.flush()
	want := []string{"abc", "12345678", "ok", "tail"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("got %q want %q", lines, want)
	}
	lines = nil
	w = &pigLineWriter{onLine: func(l []byte) { lines = append(lines, string(l)) }, maxLine: 4}
	w.Write([]byte("toolongtail"))
	w.flush()
	w.Write([]byte("a\n"))
	if !reflect.DeepEqual(lines, []string{"a"}) {
		t.Errorf("a dropped partial line leaked: %q", lines)
	}
	if (&pigLineWriter{}).limit() != pigAuditMaxLine {
		t.Error("default line limit")
	}
}

func TestPigTailWriter(t *testing.T) {
	w := &pigTailWriter{max: 4}
	w.Write([]byte("abcdef"))
	w.Write([]byte("gh"))
	if got := w.String(); got != "efgh" {
		t.Errorf("got %q", got)
	}
}

// Without a native custom-tool channel the Oracle's submit tool is emulated: the last block of the final reply is the call.
func oracleCtxIn(t *testing.T) *fakeAuditorCtx {
	c := oracleFakeCtx()
	c.dir = t.TempDir()
	return c
}

func TestPigAuditorEmulatesCustomTools(t *testing.T) {
	reply := "Thinking <submit_goal_oracle_advice>{bad</submit_goal_oracle_advice> ok\n<submit_goal_oracle_advice>\n" + actionableAdviceJSON + "\n</submit_goal_oracle_advice>"
	msg, _ := json.Marshal(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": reply}}}})
	p := newFakePig(t, auditStream(evAgent, string(msg)))
	res := runBlockerOracle(runBlockerOracleArgs{
		ctx: oracleCtxIn(t), goal: oracleGoal(), reason: "stuck", settings: oracleSettings("anthropic", "claude-strong"), createSession: p.factory,
	})
	if !res.ok || res.advice == nil || res.advice.disposition != "actionable" {
		t.Fatalf("%+v", res)
	}
	rec := p.record()
	if !strings.Contains(rec.Stdin, "<submit_goal_oracle_advice>") || !strings.Contains(rec.Stdin, `"recommendedIndex"`) {
		t.Errorf("the submission protocol is missing from the prompt: %q", rec.Stdin)
	}
	joined := " " + strings.Join(rec.Args, " ") + " "
	if !strings.Contains(joined, " --tools read,grep,find,ls ") {
		t.Errorf("tools: %q", joined)
	}
}

func TestPigAuditorCustomToolNotSubmitted(t *testing.T) {
	msg, _ := json.Marshal(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": "no block"}}}})
	p := newFakePig(t, auditStream(string(msg)))
	res := runBlockerOracle(runBlockerOracleArgs{
		ctx: oracleCtxIn(t), goal: oracleGoal(), reason: "stuck", settings: oracleSettings("anthropic", "claude-strong"), createSession: p.factory,
	})
	if res.ok || res.errorCode != "invalid_output" {
		t.Fatalf("%+v", res)
	}
}

func TestPigRunCustomToolBlocks(t *testing.T) {
	var got []*jsObject
	tool := &goalOracleTool{name: "t", execute: func(_ string, p *jsObject) oracleToolResult { got = append(got, p); return oracleToolResult{} }}
	pigRunCustomToolBlocks([]*goalOracleTool{tool}, "<t>{\"a\":1}</t> then <t>{\"a\":2}</t>")
	pigRunCustomToolBlocks([]*goalOracleTool{tool}, "</t> <t>{\"a\":3}") // closer before opener: no call
	pigRunCustomToolBlocks([]*goalOracleTool{tool}, "<t>[1]</t>")        // not an object: no call
	pigRunCustomToolBlocks([]*goalOracleTool{tool}, "<t>{\"a\":4}")      // unterminated: no call
	if len(got) != 1 || marshalJSON(got[0], "") != `{"a":2}` {
		t.Errorf("calls: %v", got)
	}
	if s := pigCustomToolSuffix([]*goalOracleTool{{name: "t", description: "does it"}}); !strings.Contains(s, "`t`") || !strings.Contains(s, "does it") || strings.Contains(s, "Schema") {
		t.Errorf("suffix without schema: %q", s)
	}
}

func TestPigAuditorAbortStopsAHangingChild(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	p := newFakePig(t, auditStream(evAgent), "GOALAUDIT_SLEEP=60", "GOALAUDIT_PIDFILE="+pidfile)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan goalAuditorResult, 1)
	go func() {
		out <- runGoalCompletionAuditor(runGoalCompletionAuditorArgs{
			ctx: auditCtx(t), goal: auditorGoal(t, "x"), detailedSummary: "d", signal: ctx, createSession: p.factory,
		})
	}()
	pid := waitPid(t, pidfile)
	cancel()
	select {
	case res := <-out:
		eq(t, res.err, "Auditor aborted.", "error")
		eq(t, res.disapproved, true, "disapproved")
	case <-time.After(10 * time.Second):
		t.Fatal("abort did not stop the audit")
	}
	if !processGone(pid) {
		t.Error("the child is still running")
	}
}

func TestPigAuditorAbortKillsAStubbornChild(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	p := newFakePig(t, auditStream(evAgent), "GOALAUDIT_SLEEP=60", "GOALAUDIT_IGNORE_TERM=1", "GOALAUDIT_PIDFILE="+pidfile)
	s, _ := p.factory.createSession(auditorSessionOptions{})
	errc := make(chan error, 1)
	go func() { errc <- s.prompt("x") }()
	pid := waitPid(t, pidfile)
	time.Sleep(100 * time.Millisecond) // the child installs its handler before it writes the pid, but give the scheduler a moment
	start := time.Now()
	s.abort()
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("an aborted prompt returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the stubborn child was not killed")
	}
	if time.Since(start) < 250*time.Millisecond {
		t.Error("the child was killed before the grace period")
	}
	s.dispose()
	if !processGone(pid) {
		t.Error("the child is still running")
	}
}

func TestPigAuditorDisposeStopsARunningChild(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	p := newFakePig(t, "", "GOALAUDIT_SLEEP=60", "GOALAUDIT_PIDFILE="+pidfile)
	s, _ := p.factory.createSession(auditorSessionOptions{})
	errc := make(chan error, 1)
	go func() { errc <- s.prompt("x") }()
	pid := waitPid(t, pidfile)
	s.dispose()
	if err := <-errc; err != nil {
		t.Errorf("a disposed prompt returned %v", err)
	}
	if !processGone(pid) {
		t.Error("the child is still running")
	}
}

func waitPid(t *testing.T, file string) int {
	t.Helper()
	for range 400 {
		if b, err := os.ReadFile(file); err == nil && len(b) > 0 {
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			return pid
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("the child did not start")
	return 0
}
