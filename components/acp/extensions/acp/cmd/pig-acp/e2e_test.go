package main

// End-to-end: a scripted ACP client drives pig-acp (in process), which starts a real
// `pig --mode rpc` child that talks to a scripted OpenAI-compatible model. Needs a pig binary:
//   PIG_ACP_E2E_PIG=/path/to/pig go test -run E2E ./...
// (npm run test:go-ports sets it from PIG_BIN). Every run uses temporary HOME, PIG_HOME and
// agent directories; nothing reads or writes the real configuration.

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func e2ePig(t *testing.T) string {
	t.Helper()
	p := os.Getenv("PIG_ACP_E2E_PIG")
	if p == "" {
		t.Skip("set PIG_ACP_E2E_PIG to a pig binary (or a built Piglet Binary) to run the end-to-end scenarios")
	}
	return p
}

// e2eHome points the process environment at fresh directories and, when llm is set, writes a
// models.json with a custom provider for it. It returns the working directory for sessions.
func e2eHome(t *testing.T, llm *fakeLLM) (work string) {
	t.Helper()
	// pig records a session's cwd as the OS resolves it, and session/list matches the cwd exactly (pi-acp does the
	// same), so the working directory must be the resolved path: macOS's TMPDIR is a symlink (/var -> /private/var).
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(root, "agent")
	work = filepath.Join(root, "work")
	for _, d := range []string{agent, work, filepath.Join(root, "home")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("PIG_HOME", filepath.Join(root, "pighome"))
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv("PIG_OFFLINE", "1")
	t.Setenv("PI_SKIP_VERSION_CHECK", "1")
	t.Setenv("PI_TELEMETRY", "0")
	if llm != nil {
		models := map[string]any{"providers": map[string]any{"acp-llm": map[string]any{
			"baseUrl": llm.baseURL(), "api": "openai-completions", "apiKey": "acp-key",
			"models": []any{map[string]any{"id": "acp-1", "name": "ACP One", "reasoning": false, "input": []string{"text"},
				"contextWindow": 100000, "maxTokens": 4096, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}},
		}}}
		b, _ := json.Marshal(models)
		if err := os.WriteFile(filepath.Join(agent, "models.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
		settings, _ := json.Marshal(map[string]any{"defaultProvider": "acp-llm", "defaultModel": "acp-1", "quietStartup": true})
		if err := os.WriteFile(filepath.Join(agent, "settings.json"), settings, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return work
}

// acpClient is a scripted ACP client over the in-process pig-acp.
type acpClient struct {
	t      *testing.T
	in     *io.PipeWriter
	mu     sync.Mutex
	nextID int
	inbox  chan map[string]any
	exit   chan int
	seen   []map[string]any
	// onRequest answers requests from the agent (session/request_permission).
	onRequest func(method string, params map[string]any) map[string]any
}

func startPigACP(t *testing.T, args ...string) *acpClient {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &acpClient{t: t, in: inW, inbox: make(chan map[string]any, 4096), exit: make(chan int, 1)}
	go func() { c.exit <- run(args, inR, outW, os.Stderr); inR.Close(); outW.Close() }()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 1<<26)
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("stdout carries a line that is not JSON: %q", sc.Text())
				continue
			}
			c.mu.Lock()
			c.seen = append(c.seen, m)
			c.mu.Unlock()
			if method, _ := m["method"].(string); method != "" && m["id"] != nil && c.onRequest != nil {
				c.write(map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": c.onRequest(method, m["params"].(map[string]any))})
			}
			c.inbox <- m
		}
		close(c.inbox)
	}()
	t.Cleanup(func() { inW.Close() })
	return c
}

func (c *acpClient) write(v any) {
	b, _ := json.Marshal(v)
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.in.Write(append(b, '\n'))
}

func (c *acpClient) request(method string, params any) int {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()
	c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return id
}

func (c *acpClient) notify(method string, params any) {
	c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// await returns the response to id and every notification that arrived before it.
func (c *acpClient) await(id int) (map[string]any, []map[string]any) {
	c.t.Helper()
	var before []map[string]any
	deadline := time.After(90 * time.Second)
	for {
		select {
		case m, ok := <-c.inbox:
			if !ok {
				c.t.Fatal("pig-acp closed its output")
			}
			if m["id"] == float64(id) && m["method"] == nil {
				return m, before
			}
			before = append(before, m)
		case <-deadline:
			c.t.Fatalf("no response to request %d; saw %d messages", id, len(before))
		}
	}
}

func (c *acpClient) call(method string, params any) map[string]any {
	c.t.Helper()
	r, _ := c.await(c.request(method, params))
	return r
}

func (c *acpClient) result(method string, params any) map[string]any {
	c.t.Helper()
	r := c.call(method, params)
	res, ok := r["result"].(map[string]any)
	if !ok {
		c.t.Fatalf("%s failed: %v", method, r)
	}
	return res
}

func (c *acpClient) updates() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for _, m := range c.seen {
		if m["method"] == "session/update" {
			out = append(out, m["params"].(map[string]any)["update"].(map[string]any))
		}
	}
	return out
}

func (c *acpClient) untilUpdate(what string, pred func(u map[string]any) bool) map[string]any {
	c.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		for _, u := range c.updates() {
			if pred(u) {
				return u
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.t.Fatalf("timed out waiting for %s; updates: %v", what, c.updates())
	return nil
}

func (c *acpClient) initialize() map[string]any {
	return c.result("initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{
		"fs": map[string]any{"readTextFile": true, "writeTextFile": true}, "terminal": true}})
}

func textPrompt(sessionID, text string) map[string]any {
	return map[string]any{"sessionId": sessionID, "prompt": []any{map[string]any{"type": "text", "text": text}}}
}

func chunksText(us []map[string]any, kind string) string {
	var b strings.Builder
	for _, u := range us {
		if u["sessionUpdate"] == kind {
			if c, _ := u["content"].(map[string]any); c["type"] == "text" {
				b.WriteString(c["text"].(string))
			}
		}
	}
	return b.String()
}

func TestE2EPromptStreamsAndReportsUsage(t *testing.T) {
	pig := e2ePig(t)
	llm := startFakeLLM(t, llmTurn{Text: "Hello from the scripted model", Chunks: 3})
	work := e2eHome(t, llm)
	c := startPigACP(t, "--pig", pig)
	c.initialize()
	sess := c.result("session/new", map[string]any{"cwd": work, "mcpServers": []any{}})
	sid, _ := sess["sessionId"].(string)
	if sid == "" {
		t.Fatalf("session/new = %v", sess)
	}
	models, _ := sess["models"].(map[string]any)
	if models["currentModelId"] != "acp-llm/acp-1" {
		t.Errorf("models = %v", models)
	}
	resp, _ := c.await(c.request("session/prompt", textPrompt(sid, "say hello")))
	if resp["result"].(map[string]any)["stopReason"] != "end_turn" {
		t.Fatalf("response = %v", resp)
	}
	us := c.updates()
	if got := chunksText(us, "agent_message_chunk"); !strings.Contains(got, "Hello from the scripted model") {
		t.Errorf("streamed %q", got)
	}
	usage := c.untilUpdate("usage_update with tokens", func(u map[string]any) bool {
		n, _ := u["used"].(float64)
		return u["sessionUpdate"] == "usage_update" && n > 0
	})
	if usage["size"] != float64(100000) {
		t.Errorf("usage = %v", usage)
	}
	if seen := llm.seen(); len(seen) != 1 || seen[0] != "say hello" {
		t.Errorf("the model saw %v", seen)
	}
	c.mu.Lock()
	for _, m := range c.seen {
		if method, _ := m["method"].(string); strings.HasPrefix(method, "fs/") || strings.HasPrefix(method, "terminal/") {
			t.Errorf("pig-acp called %s", method)
		}
	}
	c.mu.Unlock()
}

func TestE2EBashToolCall(t *testing.T) {
	pig := e2ePig(t)
	llm := startFakeLLM(t,
		llmTurn{Calls: []llmCall{{Name: "bash", Args: map[string]any{"command": "echo hi-from-bash"}}}},
		llmTurn{Text: "done"})
	work := e2eHome(t, llm)
	c := startPigACP(t, "--pig", pig)
	c.initialize()
	sid := c.result("session/new", map[string]any{"cwd": work, "mcpServers": []any{}})["sessionId"].(string)
	resp, _ := c.await(c.request("session/prompt", textPrompt(sid, "run it")))
	if resp["result"].(map[string]any)["stopReason"] != "end_turn" {
		t.Fatalf("response = %v", resp)
	}
	var call, last map[string]any
	var output strings.Builder
	for _, u := range c.updates() {
		if u["sessionUpdate"] == "tool_call" && call == nil {
			call = u
		}
		if u["sessionUpdate"] == "tool_call_update" {
			last = u
			if m, _ := u["_meta"].(map[string]any); m != nil {
				if o, ok := m["terminal_output"].(map[string]any); ok {
					output.WriteString(o["data"].(string))
				}
			}
		}
	}
	if call == nil || call["kind"] != "execute" || call["title"] != "echo hi-from-bash" {
		t.Fatalf("tool_call = %v", call)
	}
	if last["status"] != "completed" || !strings.Contains(output.String(), "hi-from-bash") {
		t.Errorf("last=%v output=%q", last, output.String())
	}
	if exit := last["_meta"].(map[string]any)["terminal_exit"].(map[string]any); exit["exit_code"] != float64(0) {
		t.Errorf("terminal_exit = %v", exit)
	}
}

func TestE2EWriteToolCallEmitsDiff(t *testing.T) {
	pig := e2ePig(t)
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("building the fixture extension needs a Go toolchain")
	}
	llm := startFakeLLM(t,
		llmTurn{Calls: []llmCall{{Name: "write", Args: map[string]any{"path": "new.txt", "content": "created\n"}}}},
		llmTurn{Text: "written"})
	work := e2eHome(t, llm)
	// pig announces a tool (tool_execution_start) and runs it without waiting for the adapter, which
	// reads the file when it sees the announcement: under load the write can land first and there is
	// no old text to diff against. The gate extension holds the write after the announcement until
	// this test has seen the adapter act on it, so the adapter always comes first.
	gate, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gate.Close() })
	if err := os.WriteFile(filepath.Join(work, "gate.addr"), []byte(gate.Addr().String()), 0o644); err != nil {
		t.Fatal(err)
	}
	held := make(chan net.Conn, 1)
	go func() {
		if conn, err := gate.Accept(); err == nil {
			held <- conn
		}
	}()
	fixture, _ := filepath.Abs("testdata/gate")
	c := startPigACP(t, "--pig", pig, "--pig-arg", "-e", "--pig-arg", fixture)
	c.initialize()
	sid := c.result("session/new", map[string]any{"cwd": work, "mcpServers": []any{}})["sessionId"].(string)
	id := c.request("session/prompt", textPrompt(sid, "write a file"))
	conn := <-held // the write is held
	c.untilUpdate("the adapter to report the write in progress", func(u map[string]any) bool {
		return u["sessionUpdate"] == "tool_call_update" && u["status"] == "in_progress"
	})
	_ = conn.Close() // release the write
	c.await(id)
	var diff map[string]any
	for _, u := range c.updates() {
		if u["sessionUpdate"] == "tool_call_update" && u["status"] == "completed" {
			for _, item := range u["content"].([]any) {
				if m := item.(map[string]any); m["type"] == "diff" {
					diff = m
				}
			}
		}
	}
	if diff == nil || diff["path"] != "new.txt" || diff["oldText"] != nil || diff["newText"] != "created\n" {
		t.Fatalf("diff = %v (updates %v)", diff, c.updates())
	}
	if b, _ := os.ReadFile(filepath.Join(work, "new.txt")); string(b) != "created\n" {
		t.Errorf("file = %q", b)
	}
}

func TestE2ECancel(t *testing.T) {
	pig := e2ePig(t)
	hold, started := make(chan struct{}), make(chan struct{})
	llm := startFakeLLM(t, llmTurn{Text: "partial answer that never finishes", Chunks: 4, Hold: hold, Started: started})
	work := e2eHome(t, llm)
	c := startPigACP(t, "--pig", pig)
	c.initialize()
	sid := c.result("session/new", map[string]any{"cwd": work, "mcpServers": []any{}})["sessionId"].(string)
	id := c.request("session/prompt", textPrompt(sid, "go on forever"))
	select {
	case <-started:
	case <-time.After(60 * time.Second):
		t.Fatal("the model was never asked")
	}
	c.notify("session/cancel", map[string]any{"sessionId": sid})
	resp, _ := c.await(id)
	if resp["result"].(map[string]any)["stopReason"] != "cancelled" {
		t.Fatalf("response = %v", resp)
	}
	close(hold)
}

func TestE2ELoadAndListSessions(t *testing.T) {
	pig := e2ePig(t)
	llm := startFakeLLM(t, llmTurn{Text: "first answer"})
	work := e2eHome(t, llm)
	c := startPigACP(t, "--pig", pig)
	c.initialize()
	sid := c.result("session/new", map[string]any{"cwd": work, "mcpServers": []any{}})["sessionId"].(string)
	c.await(c.request("session/prompt", textPrompt(sid, "remember the word pineapple")))
	c.call("session/prompt", textPrompt(sid, "/name Pineapple session"))
	c.in.Close()
	if code := <-c.exit; code != 0 {
		t.Fatalf("first adapter exited %d", code)
	}

	// A new adapter process (a new editor window) lists and loads the session.
	c2 := startPigACP(t, "--pig", pig)
	c2.initialize()
	list := c2.result("session/list", map[string]any{"cwd": work})
	var found map[string]any
	for _, s := range list["sessions"].([]any) {
		if s.(map[string]any)["sessionId"] == sid {
			found = s.(map[string]any)
		}
	}
	if found == nil || found["title"] != "Pineapple session" {
		t.Fatalf("session/list = %v", list)
	}
	c2.result("session/load", map[string]any{"sessionId": sid, "cwd": work, "mcpServers": []any{}})
	us := c2.updates()
	if !strings.Contains(chunksText(us, "user_message_chunk"), "remember the word pineapple") || !strings.Contains(chunksText(us, "agent_message_chunk"), "first answer") {
		t.Errorf("history not replayed: %v", us)
	}
	if res := c2.call("session/delete", map[string]any{"sessionId": sid}); res["error"] != nil {
		t.Errorf("delete: %v", res)
	}
	list = c2.result("session/list", map[string]any{"cwd": work})
	for _, s := range list["sessions"].([]any) {
		if s.(map[string]any)["sessionId"] == sid {
			t.Error("the deleted session is still listed")
		}
	}
}

func TestE2EConfigOptions(t *testing.T) {
	pig := e2ePig(t)
	llm := startFakeLLM(t)
	work := e2eHome(t, llm)
	c := startPigACP(t, "--pig", pig)
	c.initialize()
	sess := c.result("session/new", map[string]any{"cwd": work, "mcpServers": []any{}})
	sid := sess["sessionId"].(string)
	modes := sess["modes"].(map[string]any)
	if modes["currentModeId"] == "" || len(modes["availableModes"].([]any)) == 0 {
		t.Fatalf("modes = %v", modes)
	}
	res := c.result("session/set_config_option", map[string]any{"sessionId": sid, "configId": "model", "value": "acp-llm/acp-1"})
	if len(res["configOptions"].([]any)) < 2 {
		t.Errorf("configOptions = %v", res)
	}
	if r := c.call("session/set_config_option", map[string]any{"sessionId": sid, "configId": "model", "value": "acp-llm/missing"}); r["error"] == nil {
		t.Errorf("an unknown model was accepted: %v", r)
	}
	c.untilUpdate("config_option_update", func(u map[string]any) bool { return u["sessionUpdate"] == "config_option_update" })
}

func TestE2EExtensionSelectBecomesPermissionRequest(t *testing.T) {
	pig := e2ePig(t)
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("building the fixture extension needs a Go toolchain")
	}
	llm := startFakeLLM(t,
		llmTurn{Calls: []llmCall{{Name: "ask", Args: map[string]any{}}}},
		llmTurn{Text: "deployed"})
	work := e2eHome(t, llm)
	fixture, _ := filepath.Abs("testdata/ask")
	c := startPigACP(t, "--pig", pig, "--pig-arg", "-e", "--pig-arg", fixture)
	var asked map[string]any
	c.onRequest = func(method string, params map[string]any) map[string]any {
		if method != "session/request_permission" {
			return map[string]any{}
		}
		asked = params
		return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "choice-1"}}
	}
	c.initialize()
	sid := c.result("session/new", map[string]any{"cwd": work, "mcpServers": []any{}})["sessionId"].(string)
	resp, _ := c.await(c.request("session/prompt", textPrompt(sid, "deploy it")))
	if resp["result"] == nil {
		t.Fatalf("response = %v", resp)
	}
	if asked == nil {
		t.Fatal("no permission request reached the client")
	}
	tc := asked["toolCall"].(map[string]any)
	if tc["title"] != "Deploy where?" || len(asked["options"].([]any)) != 2 {
		t.Errorf("request = %v", asked)
	}
	got := c.untilUpdate("the extension's notification", func(u map[string]any) bool {
		return u["sessionUpdate"] == "agent_message_chunk" && strings.Contains(chunksText([]map[string]any{u}, "agent_message_chunk"), "chose production")
	})
	if m, _ := got["_meta"].(map[string]any); m["piAcp"].(map[string]any)["notify"].(map[string]any)["level"] != "info" {
		t.Errorf("notify meta = %v", got["_meta"])
	}
}

func TestE2ENoModelsIsAuthRequired(t *testing.T) {
	pig := e2ePig(t)
	work := e2eHome(t, nil) // no provider configured
	c := startPigACP(t, "--pig", pig)
	c.initialize()
	r := c.call("session/new", map[string]any{"cwd": work, "mcpServers": []any{}})
	e, _ := r["error"].(map[string]any)
	if e == nil || e["code"] != float64(-32000) {
		t.Fatalf("response = %v", r)
	}
	methods, _ := e["data"].(map[string]any)["authMethods"].([]any)
	if len(methods) != 1 || methods[0].(map[string]any)["id"] != "pi_terminal_login" {
		t.Errorf("authMethods = %v", methods)
	}
}

func TestE2EMissingPigIsAnInternalError(t *testing.T) {
	work := e2eHome(t, nil)
	c := startPigACP(t, "--pig", "pig-does-not-exist-12345")
	c.initialize()
	r := c.call("session/new", map[string]any{"cwd": work, "mcpServers": []any{}})
	e, _ := r["error"].(map[string]any)
	if e == nil || e["code"] != float64(-32603) || !strings.Contains(strings.ToLower(e["message"].(string)), "executable not found") {
		t.Fatalf("response = %v", r)
	}
}

func TestE2EMcpServersAreAcceptedButNotStarted(t *testing.T) {
	pig := e2ePig(t)
	llm := startFakeLLM(t)
	work := e2eHome(t, llm)
	c := startPigACP(t, "--pig", pig)
	c.initialize()
	res := c.result("session/new", map[string]any{"cwd": work, "mcpServers": []any{
		map[string]any{"name": "fs", "command": "/nonexistent/mcp-server", "args": []any{}, "env": []any{}}}})
	if res["sessionId"] == "" {
		t.Errorf("session/new = %v", res)
	}
}

func TestE2EClosingStdinStopsThePigChild(t *testing.T) {
	pig := e2ePig(t)
	llm := startFakeLLM(t)
	work := e2eHome(t, llm)
	c := startPigACP(t, "--pig", pig)
	c.initialize()
	c.result("session/new", map[string]any{"cwd": work, "mcpServers": []any{}})
	c.in.Close()
	select {
	case code := <-c.exit:
		if code != 0 {
			t.Errorf("exit %d", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("pig-acp did not exit after stdin closed")
	}
}
