package kagentpolicy_test

// End to end with a real PiG. The test builds the Piglet Binary from piglets/kagent (or uses KAGENT_PIG_BINARY) and
// drives `pig-kagent --mode rpc` over stdin and stdout against a local model and a local MCP server, in a temporary
// HOME, PIG_HOME and PIG_CODING_AGENT_DIR (no real configuration or credential is read).
//
//	PIG_BIN          a pig executable (v0.7.0) that builds the Binary
//	PIG_SOURCE_ROOT  a PiG checkout at the same tag, for the build
//	KAGENT_PIGLET    the staged piglet.yaml to build (default dist/staged/piglets/kagent/piglet.yaml)
//	KAGENT_PIG_BINARY  skip the build and run this Piglet Binary
//	KAGENT_MOCK_MODEL_URL  use the hermetic mock model server (http://host:port) instead of the in-process model;
//	                       the scenarios that need to force an undeclared call are skipped then
//
// None of PIG_BIN and KAGENT_PIG_BINARY set: the tests skip.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	binaryOnce  sync.Once
	binaryPath  string
	binaryErr   error
	buildTime   time.Duration
	binarySize  int64
	scratchRoot string
)

func TestMain(m *testing.M) {
	code := m.Run()
	if scratchRoot != "" {
		_ = os.RemoveAll(scratchRoot)
	}
	os.Exit(code)
}

// pigletPath is the staged Piglet (run `npm run stage` first: a local Package reference is rewritten there to a
// Piglet-local one, which `pig piglet build` requires). KAGENT_PIGLET names another manifest.
func pigletPath() string {
	if p := os.Getenv("KAGENT_PIGLET"); p != "" {
		return p
	}
	return filepath.Join("..", "..", "..", "..", "dist", "staged", "piglets", "kagent", "piglet.yaml")
}

// pigletBinary returns the Piglet Binary, building it once per test run.
func pigletBinary(t *testing.T) string {
	t.Helper()
	binaryOnce.Do(func() {
		if bin := os.Getenv("KAGENT_PIG_BINARY"); bin != "" {
			binaryPath = bin
			return
		}
		pig := os.Getenv("PIG_BIN")
		if pig == "" {
			binaryErr = fmt.Errorf("skip")
			return
		}
		var err error
		scratchRoot, err = os.MkdirTemp("", "kagent-policy-e2e-")
		if err != nil {
			binaryErr = err
			return
		}
		home := filepath.Join(scratchRoot, "build-home")
		_ = os.MkdirAll(filepath.Join(home, "agent"), 0o700)
		out := filepath.Join(scratchRoot, "pig-kagent")
		cmd := exec.Command(pig, "piglet", "build", pigletPath(), "--format", "binary", "--targets", runtime.GOOS+"/"+runtime.GOARCH, "--out", out)
		cmd.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "GIT_TERMINAL_PROMPT=0")
		start := time.Now()
		if output, err := cmd.CombinedOutput(); err != nil {
			binaryErr = fmt.Errorf("pig piglet build: %v\n%s", err, output)
			return
		}
		buildTime = time.Since(start)
		if st, err := os.Stat(out); err == nil {
			binarySize = st.Size()
		}
		binaryPath = out
	})
	if binaryErr != nil && binaryErr.Error() == "skip" {
		t.Skip("set PIG_BIN (and PIG_SOURCE_ROOT) or KAGENT_PIG_BINARY to run the real-PiG tests")
	}
	if binaryErr != nil {
		t.Fatal(binaryErr)
	}
	return binaryPath
}

// ---- the MCP server ----

type mcpCall struct {
	Name string
	Args map[string]any
	Auth string
	XCmd string
}

type mcpStub struct {
	URL   string
	mu    sync.Mutex
	calls []mcpCall
	// initAuth is the Authorization header of every request that was not a tools/call.
	seen []string
}

func startMCP(t *testing.T) *mcpStub {
	t.Helper()
	s := &mcpStub{}
	tools := []any{
		map[string]any{"name": "kagent_probe", "description": "Returns probe:<note>.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"note": map[string]any{"type": "string"}}, "required": []string{"note"}}},
		map[string]any{"name": "echo", "description": "Returns its text.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}},
		map[string]any{"name": strings.Repeat("e", 100), "description": "A tool whose full PiG name is cut to 64 characters.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"note": map[string]any{"type": "string"}}}},
		map[string]any{"name": "secret_tool", "description": "Not on the allowlist.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var q struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		if len(q.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		reply := func(result any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": result})
		}
		switch q.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(q.Params, &p)
			s.mu.Lock()
			s.seen = append(s.seen, r.Header.Get("Authorization"))
			s.mu.Unlock()
			reply(map[string]any{"protocolVersion": p.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "stub", "version": "1"}})
		case "tools/list":
			reply(map[string]any{"tools": tools})
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(q.Params, &p)
			s.mu.Lock()
			s.calls = append(s.calls, mcpCall{Name: p.Name, Args: p.Arguments, Auth: r.Header.Get("Authorization"), XCmd: r.Header.Get("X-Cmd")})
			s.mu.Unlock()
			text := p.Name + ":" + fmt.Sprint(p.Arguments["note"], p.Arguments["text"])
			reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": false})
		default:
			reply(map[string]any{})
		}
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL + "/mcp"
	return s
}

func (s *mcpStub) toolCalls() []mcpCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mcpCall(nil), s.calls...)
}

// ---- the model ----

type modelStub struct {
	BaseURL string
	Name    string
	Forces  bool // the model can be told to call a tool that is not declared
	mu      sync.Mutex
	tools   [][]string
}

func startModel(t *testing.T) *modelStub {
	t.Helper()
	if base := os.Getenv("KAGENT_MOCK_MODEL_URL"); base != "" {
		return &modelStub{BaseURL: strings.TrimRight(base, "/") + "/v1", Name: "mock-model"}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &modelStub{BaseURL: "http://" + ln.Addr().String() + "/v1", Name: "e2e-1", Forces: true}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", m.handle)
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return m
}

func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var b strings.Builder
		for _, x := range blocks {
			b.WriteString(x.Text)
		}
		return b.String()
	}
	return ""
}

func (m *modelStub) handle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	var names []string
	for _, tl := range req.Tools {
		names = append(names, tl.Function.Name)
	}
	m.mu.Lock()
	m.tools = append(m.tools, names)
	m.mu.Unlock()
	lastUser, toolText, afterTool := "", "", false
	for _, msg := range req.Messages {
		switch msg.Role {
		case "user":
			lastUser, toolText, afterTool = contentText(msg.Content), "", false
		case "tool":
			toolText += contentText(msg.Content)
			afterTool = true
		}
	}
	declared := func(name string) bool {
		for _, n := range names {
			if n == name {
				return true
			}
		}
		return false
	}
	reply, args := "ECHO "+lastUser, ""
	call := ""
	switch {
	case afterTool:
		reply = "DONE:" + toolText
	case strings.Contains(lastUser, "[approve]"):
		reply = "APPROVE-NOPROBE"
		for _, n := range names {
			if strings.HasSuffix(n, "kagent_probe") {
				call, args = n, `{"note":"basket"}`
				break
			}
		}
	case strings.Contains(lastUser, "[ask]") && declared("ask_user"):
		call, args = "ask_user", `{"questions":[{"question":"Which env?","choices":["dev","prod"],"multiple":false}]}`
	case strings.Contains(lastUser, "[call:"):
		rest := lastUser[strings.Index(lastUser, "[call:")+len("[call:"):]
		call = rest[:strings.Index(rest, "]")]
		args = `{"note":"x","text":"x"}`
	}
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		w.(http.Flusher).Flush()
	}
	base := map[string]any{"id": "c1", "object": "chat.completion.chunk", "created": 1, "model": "e2e-1"}
	chunk := func(delta map[string]any, finish any) map[string]any {
		c := map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
		for k, v := range base {
			c[k] = v
		}
		return c
	}
	if strings.Contains(lastUser, "[batch:") && !afterTool {
		batchReply(send, chunk, w, lastUser)
		return
	}
	if call != "" {
		send(chunk(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": call, "arguments": ""}}}}, nil))
		send(chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": args}}}}, nil))
		done := chunk(map[string]any{}, "tool_calls")
		done["usage"] = map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
		send(done)
	} else {
		send(chunk(map[string]any{"role": "assistant", "content": ""}, nil))
		send(chunk(map[string]any{"content": reply}, nil))
		done := chunk(map[string]any{}, "stop")
		done["usage"] = map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
		send(done)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// batchReply answers `[batch:a|b|c]` with one assistant message that calls the named tools in that order. A bash call
// writes ran-<index>.txt in the working directory, so a test can see which calls of the batch executed.
func batchReply(send func(any), chunk func(map[string]any, any) map[string]any, w http.ResponseWriter, prompt string) {
	rest := prompt[strings.Index(prompt, "[batch:")+len("[batch:"):]
	for i, name := range strings.Split(rest[:strings.Index(rest, "]")], "|") {
		args := `{"note":"x"}`
		switch name {
		case "bash":
			args = fmt.Sprintf(`{"command":"echo ran > ran-%d.txt"}`, i)
		case "ask_user":
			args = `{"questions":[{"question":"Q?","choices":["a","b"]}]}`
		}
		first := map[string]any{"tool_calls": []any{map[string]any{"index": i, "id": fmt.Sprintf("call_%d", i), "type": "function", "function": map[string]any{"name": name, "arguments": ""}}}}
		if i == 0 {
			first["role"] = "assistant"
		}
		send(chunk(first, nil))
		send(chunk(map[string]any{"tool_calls": []any{map[string]any{"index": i, "function": map[string]any{"arguments": args}}}}, nil))
	}
	done := chunk(map[string]any{}, "tool_calls")
	done["usage"] = map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	send(done)
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// declared returns the sorted union of the tool names the model was offered.
func (m *modelStub) declared() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := map[string]bool{}
	for _, req := range m.tools {
		for _, n := range req {
			set[n] = true
		}
	}
	var out []string
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (m *modelStub) requests() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.tools)
}

func has(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

// ---- the pig process ----

type pigProc struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan map[string]any
	exited chan struct{}
	mu     sync.Mutex
	stderr bytes.Buffer
	raw    []string
	err    error
}

type runOptions struct {
	// policy is the policy file text; "" leaves KAGENT_PIG_POLICY unset.
	policy string
	// policyPath replaces the written file's path (a missing file).
	policyPath string
	sessionDir string
	sessionID  string
	extraEnv   []string
}

func startPig(t *testing.T, model *modelStub, o runOptions) *pigProc {
	t.Helper()
	bin := pigletBinary(t)
	home := t.TempDir()
	agent := filepath.Join(home, "pig", "agent")
	if err := os.MkdirAll(agent, 0o700); err != nil {
		t.Fatal(err)
	}
	models := map[string]any{"providers": map[string]any{"e2e": map[string]any{
		"baseUrl": model.BaseURL, "api": "openai-completions", "apiKey": "e2e-key",
		"models": []any{map[string]any{"id": model.Name, "name": model.Name, "reasoning": false, "input": []string{"text"},
			"contextWindow": 100000, "maxTokens": 4096, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}},
	}}}
	b, _ := json.Marshal(models)
	if err := os.WriteFile(filepath.Join(agent, "models.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(home, "workspace")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "PIG_HOME=" + filepath.Join(home, "pig"),
		"PIG_CODING_AGENT_DIR=" + agent, "PI_CODING_AGENT_DIR=" + agent,
		"KAGENT_PIG_HDR_1=Bearer kagent-credential-injected", "KAGENT_PIG_HDR_2=!echo PWNED"}
	if o.policy != "" || o.policyPath != "" {
		path := filepath.Join(home, "policy.json")
		if err := os.WriteFile(path, []byte(o.policy), 0o600); err != nil {
			t.Fatal(err)
		}
		if o.policyPath != "" {
			path = o.policyPath
		}
		env = append(env, "KAGENT_PIG_POLICY="+path)
	}
	env = append(env, o.extraEnv...)
	args := []string{"--mode", "rpc", "--offline", "--provider", "e2e", "--model", model.Name,
		"--no-skills", "--no-context-files", "--no-prompt-templates", "--no-themes"}
	if o.sessionDir != "" {
		args = append(args, "--session-dir", o.sessionDir, "--session-id", o.sessionID)
	} else {
		args = append(args, "--no-session")
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir, cmd.Env = work, env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	p := &pigProc{t: t, cmd: cmd, stdin: stdin, events: make(chan map[string]any, 4096), exited: make(chan struct{})}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			var ev map[string]any
			p.mu.Lock()
			p.raw = append(p.raw, sc.Text())
			p.mu.Unlock()
			if json.Unmarshal(sc.Bytes(), &ev) == nil {
				p.events <- ev
			}
		}
		close(p.events)
	}()
	go func() {
		_, _ = io.Copy(&lockedWriter{p: p}, stderr)
	}()
	go func() {
		p.err = cmd.Wait()
		close(p.exited)
	}()
	t.Cleanup(func() {
		_ = stdin.Close()
		select {
		case <-p.exited:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-p.exited
		}
	})
	return p
}

type lockedWriter struct{ p *pigProc }

func (w *lockedWriter) Write(b []byte) (int, error) {
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	return w.p.stderr.Write(b)
}

func (p *pigProc) stderrText() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stderr.String()
}

func (p *pigProc) stdoutLines() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.raw...)
}

func (p *pigProc) send(v any) {
	p.t.Helper()
	b, _ := json.Marshal(v)
	if _, err := p.stdin.Write(append(b, '\n')); err != nil {
		p.t.Fatalf("write to pig: %v\nstderr: %s", err, p.stderrText())
	}
}

func (p *pigProc) prompt(text string) { p.send(map[string]any{"type": "prompt", "message": text}) }

// until reads events until pred accepts one, and fails after the timeout.
func (p *pigProc) until(what string, pred func(map[string]any) bool) map[string]any {
	p.t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case ev, ok := <-p.events:
			if !ok {
				p.t.Fatalf("pig closed its output while waiting for %s\nstderr: %s", what, p.stderrText())
			}
			if pred(ev) {
				return ev
			}
		case <-deadline:
			p.t.Fatalf("timed out waiting for %s\nstdout: %v\nstderr: %s", what, p.stdoutLines(), p.stderrText())
		}
	}
}

func isConfirm(title string) func(map[string]any) bool {
	return func(ev map[string]any) bool {
		return ev["type"] == "extension_ui_request" && ev["method"] == "confirm" && ev["title"] == title
	}
}

func isType(typ string) func(map[string]any) bool {
	return func(ev map[string]any) bool { return ev["type"] == typ }
}

// toolEnd waits for tool_execution_end and returns isError and the text of its content.
func (p *pigProc) toolEnd() (bool, string) {
	p.t.Helper()
	ev := p.until("tool_execution_end", isType("tool_execution_end"))
	isErr, _ := ev["isError"].(bool)
	result, _ := ev["result"].(map[string]any)
	var text strings.Builder
	content, _ := result["content"].([]any)
	for _, c := range content {
		if m, ok := c.(map[string]any); ok {
			text.WriteString(fmt.Sprint(m["text"]))
		}
	}
	return isErr, text.String()
}

func (p *pigProc) respond(id any, fields map[string]any) {
	fields["type"], fields["id"] = "extension_ui_response", id
	p.send(fields)
}

// settle waits for the end of the turn and returns the assistant's last text.
func (p *pigProc) settle() {
	p.t.Helper()
	p.until("agent_settled", isType("agent_settled"))
}

func (p *pigProc) waitExit(d time.Duration) error {
	p.t.Helper()
	select {
	case <-p.exited:
		return p.err
	case <-time.After(d):
		p.t.Fatalf("pig did not exit within %v\nstderr: %s", d, p.stderrText())
		return nil
	}
}

func policyJSON(mcpURL string, servers ...string) string {
	return fmt.Sprintf(`{"mcp_servers":[%s],"ask_user":true}`, strings.Join(servers, ","))
}

// gatedServer is the server of the design: one allowlisted tool, approval required.
func gatedServer(url string) string {
	return fmt.Sprintf(`{"name":"kagent-tools","url":%q,"headers":{"Authorization":"${KAGENT_PIG_HDR_1}","X-Cmd":"${KAGENT_PIG_HDR_2}"},"timeout_seconds":60,"tools":["kagent_probe"],"require_approval":true}`, url)
}

func freeServer(url string) string {
	return fmt.Sprintf(`{"name":"free","url":%q,"headers":{"Authorization":"${KAGENT_PIG_HDR_1}"}}`, url)
}

const e2eProbe = "mcp__kagent_tools__kagent_probe"

func settleQuiet(t *testing.T) { t.Helper(); time.Sleep(400 * time.Millisecond) }

// ---- scenarios ----

func TestRealPigApprovalRoundTrip(t *testing.T) {
	mcp, model := startMCP(t), startModel(t)
	p := startPig(t, model, runOptions{policy: policyJSON(mcp.URL, gatedServer(mcp.URL))})
	p.prompt("[approve] call the probe")
	req := p.until("the approval confirm", isConfirm("kagent-approval"))

	var msg struct {
		V          int            `json:"v"`
		Kind       string         `json:"kind"`
		ToolCallID string         `json:"toolCallId"`
		ToolName   string         `json:"toolName"`
		Input      map[string]any `json:"input"`
	}
	text, _ := req["message"].(string)
	if err := json.Unmarshal([]byte(text), &msg); err != nil {
		t.Fatalf("confirm message %q is not JSON: %v", text, err)
	}
	if msg.V != 1 || msg.Kind != "approval" || msg.ToolName != e2eProbe || msg.ToolCallID == "" || msg.Input["note"] != "basket" || len(msg.Input) != 1 {
		t.Fatalf("confirm message = %s", text)
	}
	want := fmt.Sprintf(`{"v":1,"kind":"approval","toolCallId":%q,"toolName":%q,"input":{"note":"basket"}}`, msg.ToolCallID, e2eProbe)
	if text != want {
		t.Fatalf("message is not the exact compact form:\n got %s\nwant %s", text, want)
	}

	// Nothing reaches the MCP server before approval.
	settleQuiet(t)
	if calls := mcp.toolCalls(); len(calls) != 0 {
		t.Fatalf("%d tools/call before approval: %+v", len(calls), calls)
	}
	p.respond(req["id"], map[string]any{"confirmed": true})
	isErr, result := p.toolEnd()
	if isErr || !strings.Contains(result, "kagent_probe:basket") {
		t.Fatalf("approved call: isError=%v result=%q", isErr, result)
	}
	p.settle()

	calls := mcp.toolCalls()
	if len(calls) != 1 || calls[0].Name != "kagent_probe" || calls[0].Args["note"] != "basket" {
		t.Fatalf("tools/call = %+v", calls)
	}
	// Header values arrive as the environment holds them: the placeholder, and a value that starts with ! literally.
	if calls[0].Auth != "Bearer kagent-credential-injected" || calls[0].XCmd != "!echo PWNED" {
		t.Fatalf("headers on tools/call: Authorization=%q X-Cmd=%q", calls[0].Auth, calls[0].XCmd)
	}
	// The mock model server does not report the tools it was offered; the in-process model does. With the mock, the
	// call itself proves the tool was declared (the mock picks the probe from the request's tool list).
	if model.Forces {
		declared := model.declared()
		if !has(declared, e2eProbe) || !has(declared, "ask_user") {
			t.Fatalf("exposure direct did not declare the tool to the model: %v", declared)
		}
		for _, hidden := range []string{"mcp__kagent_tools__echo", "mcp__kagent_tools__secret_tool"} {
			if has(declared, hidden) {
				t.Fatalf("%s is declared although it is not on the allowlist: %v", hidden, declared)
			}
		}
	}
}

func TestRealPigRejection(t *testing.T) {
	for name, response := range map[string]map[string]any{
		"confirmed false": {"confirmed": false},
		"cancelled":       {"cancelled": true},
	} {
		t.Run(name, func(t *testing.T) {
			mcp, model := startMCP(t), startModel(t)
			p := startPig(t, model, runOptions{policy: policyJSON(mcp.URL, gatedServer(mcp.URL))})
			p.prompt("[approve] call the probe")
			req := p.until("the approval confirm", isConfirm("kagent-approval"))
			p.respond(req["id"], response)
			isErr, result := p.toolEnd()
			if !isErr || !strings.Contains(result, "Tool call was rejected by user.") {
				t.Fatalf("rejected call: isError=%v result=%q", isErr, result)
			}
			p.settle()
			if calls := mcp.toolCalls(); len(calls) != 0 {
				t.Fatalf("a rejected call reached the MCP server: %+v", calls)
			}
		})
	}
}

func TestRealPigAllowlistedOutToolIsNotFound(t *testing.T) {
	mcp, model := startMCP(t), startModel(t)
	if !model.Forces {
		t.Skip("needs a model that can be told to call an undeclared tool")
	}
	p := startPig(t, model, runOptions{policy: policyJSON(mcp.URL, gatedServer(mcp.URL))})
	p.prompt("[call:mcp__kagent_tools__echo] try the hidden tool")
	isErr, result := p.toolEnd()
	if !isErr || !strings.Contains(result, "not found") {
		t.Fatalf("hidden tool: isError=%v result=%q", isErr, result)
	}
	p.settle()
	if calls := mcp.toolCalls(); len(calls) != 0 {
		t.Fatalf("a tool outside the allowlist reached the MCP server: %+v", calls)
	}
	for _, line := range p.stdoutLines() {
		if strings.Contains(line, `"method":"confirm"`) {
			t.Fatalf("an undeclared tool asked for approval: %s", line)
		}
	}
}

func TestRealPigUngatedServerRunsWithoutAsking(t *testing.T) {
	mcp, model := startMCP(t), startModel(t)
	if !model.Forces {
		t.Skip("needs a model that can be told which tool to call")
	}
	p := startPig(t, model, runOptions{policy: policyJSON(mcp.URL, gatedServer(mcp.URL), freeServer(mcp.URL))})
	p.prompt("[call:mcp__free__echo] echo")
	isErr, result := p.toolEnd()
	if isErr || !strings.Contains(result, "echo:x") {
		t.Fatalf("ungated call: isError=%v result=%q", isErr, result)
	}
	p.settle()
	calls := mcp.toolCalls()
	if len(calls) != 1 || calls[0].Name != "echo" {
		t.Fatalf("tools/call = %+v", calls)
	}
	// With no allowlist every tool of the server is declared.
	declared := model.declared()
	if !has(declared, "mcp__free__echo") || !has(declared, "mcp__free__secret_tool") || !has(declared, "mcp__free__kagent_probe") {
		t.Fatalf("an empty allowlist did not expose every tool: %v", declared)
	}
	for _, line := range p.stdoutLines() {
		if strings.Contains(line, `"method":"confirm"`) {
			t.Fatalf("an ungated tool asked for approval: %s", line)
		}
	}
}

// Closing stdin while the confirm is outstanding is how the adapter parks: pig exits cleanly, the session file ends
// at the assistant message with the tool call and holds no result, and nothing reached the MCP server.
func TestRealPigParkLeavesTheSessionAtTheToolCall(t *testing.T) {
	mcp, model := startMCP(t), startModel(t)
	sessions := t.TempDir()
	const id = "0b6d2f7e-5c1a-4f0e-9a52-3d5f6c7b8a90"
	p := startPig(t, model, runOptions{policy: policyJSON(mcp.URL, gatedServer(mcp.URL)), sessionDir: sessions, sessionID: id})
	p.prompt("[approve] call the probe")
	p.until("the approval confirm", isConfirm("kagent-approval"))
	_ = p.stdin.Close()
	if err := p.waitExit(10 * time.Second); err != nil {
		t.Fatalf("pig did not exit cleanly after stdin closed: %v\nstderr: %s", err, p.stderrText())
	}
	if calls := mcp.toolCalls(); len(calls) != 0 {
		t.Fatalf("tools/call after the park: %+v", calls)
	}
	var file string
	_ = filepath.Walk(sessions, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(path, ".jsonl") {
			file = path
		}
		return nil
	})
	if file == "" {
		t.Fatal("no session file")
	}
	data, _ := os.ReadFile(file)
	var roles []string
	var lastToolCall bool
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil || entry.Type != "message" {
			continue
		}
		roles = append(roles, entry.Message.Role)
		lastToolCall = false
		for _, c := range entry.Message.Content {
			if c.Type == "toolCall" {
				lastToolCall = true
			}
		}
	}
	if strings.Join(roles, ",") != "user,assistant" || !lastToolCall {
		t.Fatalf("session roles = %v, last message has a toolCall = %v\n%s", roles, lastToolCall, data)
	}
	if strings.Contains(string(data), `"toolResult"`) {
		t.Fatalf("the parked session holds a tool result:\n%s", data)
	}
}

func TestRealPigAskUser(t *testing.T) {
	for name, tc := range map[string]struct {
		response map[string]any
		isErr    bool
		want     string
	}{
		"answered":  {map[string]any{"confirmed": "prod"}, false, "prod"},
		"cancelled": {map[string]any{"cancelled": true}, true, "The user declined to answer."},
	} {
		t.Run(name, func(t *testing.T) {
			mcp, model := startMCP(t), startModel(t)
			if !model.Forces {
				t.Skip("needs a model that can be told to call ask_user")
			}
			p := startPig(t, model, runOptions{policy: policyJSON(mcp.URL, gatedServer(mcp.URL))})
			p.prompt("[ask] which environment?")
			req := p.until("the ask_user confirm", isConfirm("kagent-ask-user"))
			text, _ := req["message"].(string)
			var msg struct {
				V          int    `json:"v"`
				Kind       string `json:"kind"`
				ToolCallID string `json:"toolCallId"`
				ToolName   string `json:"toolName"`
			}
			if err := json.Unmarshal([]byte(text), &msg); err != nil || msg.ToolCallID == "" {
				t.Fatalf("message %q: %v", text, err)
			}
			want := fmt.Sprintf(`{"v":1,"kind":"ask_user","toolCallId":%q,"toolName":"ask_user","input":{"questions":[{"question":"Which env?","choices":["dev","prod"],"multiple":false}]}}`, msg.ToolCallID)
			if text != want {
				t.Fatalf("message:\n got %s\nwant %s", text, want)
			}
			p.respond(req["id"], tc.response)
			isErr, result := p.toolEnd()
			if isErr != tc.isErr || !strings.Contains(result, tc.want) {
				t.Fatalf("ask_user result: isError=%v %q", isErr, result)
			}
			p.settle()
		})
	}
}

func TestRealPigInertWithoutPolicy(t *testing.T) {
	mcp, model := startMCP(t), startModel(t)
	p := startPig(t, model, runOptions{})
	p.prompt("[approve] call the probe")
	p.settle()
	declared := model.declared()
	for _, n := range declared {
		if strings.HasPrefix(n, "mcp__") || n == "ask_user" {
			t.Fatalf("an inert extension declared %s: %v", n, declared)
		}
	}
	if calls := mcp.toolCalls(); len(calls) != 0 {
		t.Fatalf("tools/call = %+v", calls)
	}
	for _, line := range p.stdoutLines() {
		if strings.Contains(line, `"method":"confirm"`) {
			t.Fatalf("an inert extension asked for confirmation: %s", line)
		}
	}
}

// An unusable policy must not give an ungated session: pig refuses to start, prints the reason and never calls the model.
func TestRealPigInvalidPolicyFailsClosed(t *testing.T) {
	mcp, model := startMCP(t), startModel(t)
	missing := filepath.Join(t.TempDir(), "absent.json")
	cases := map[string]runOptions{
		"invalid json":   {policy: `{"mcp_servers":`},
		"unknown key":    {policy: `{"mcp_servers":[],"gate":"off"}`},
		"literal header": {policy: fmt.Sprintf(`{"mcp_servers":[{"name":"a","url":%q,"headers":{"Authorization":"!echo hi"},"require_approval":true}]}`, mcp.URL)},
		"unset variable": {policy: fmt.Sprintf(`{"mcp_servers":[{"name":"a","url":%q,"headers":{"Authorization":"${NO_SUCH_VAR}"}}]}`, mcp.URL)},
		"missing file":   {policyPath: missing},
	}
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			p := startPig(t, model, o)
			err := p.waitExit(15 * time.Second)
			if err == nil {
				t.Fatalf("pig exited 0 with an invalid policy\nstdout: %v", p.stdoutLines())
			}
			if !strings.Contains(p.stderrText(), "refusing to start ungated") {
				t.Fatalf("stderr does not explain the refusal: %s", p.stderrText())
			}
			if strings.Contains(p.stderrText(), "!echo hi") {
				t.Fatalf("stderr repeats a header value: %s", p.stderrText())
			}
		})
	}
	if n := model.requests(); n != 0 && model.Forces {
		t.Fatalf("the model was called %d times by a refused session", n)
	}
}

// PiG evaluates every tool_call hook of an assistant message before it runs any call of it, so a park at the first
// confirm leaves nothing of the batch executed and the session ends at the assistant message. ask_user asks in its
// hook for the same reason: a dialog in execute would run the calls listed before it first.
func TestRealPigParkedBatchRunsNothing(t *testing.T) {
	for _, batch := range []string{"bash|" + e2eProbe, "bash|ask_user", "ask_user|bash", "bash|bash|" + e2eProbe + "|bash", "bash|ask_user|" + e2eProbe} {
		t.Run(batch, func(t *testing.T) {
			mcp, model := startMCP(t), startModel(t)
			if !model.Forces {
				t.Skip("needs a model that can be told to call several tools")
			}
			sessions := t.TempDir()
			p := startPig(t, model, runOptions{policy: policyJSON(mcp.URL, gatedServer(mcp.URL)), sessionDir: sessions, sessionID: "0b6d2f7e-5c1a-4f0e-9a52-3d5f6c7b8a90"})
			p.prompt("[batch:" + batch + "] go")
			p.until("a confirm", func(ev map[string]any) bool {
				if ev["type"] == "tool_execution_end" {
					t.Fatalf("a call ended before the confirm: %v", ev)
				}
				return ev["type"] == "extension_ui_request" && ev["method"] == "confirm"
			})
			_ = p.stdin.Close()
			if err := p.waitExit(10 * time.Second); err != nil {
				t.Fatalf("pig did not exit cleanly: %v\nstderr: %s", err, p.stderrText())
			}
			if ran, _ := filepath.Glob(filepath.Join(p.cmd.Dir, "ran-*.txt")); len(ran) != 0 {
				t.Fatalf("bash ran before the park: %v", ran)
			}
			if calls := mcp.toolCalls(); len(calls) != 0 {
				t.Fatalf("tools/call before the park: %+v", calls)
			}
			if roles := sessionRoles(t, sessions); roles != "user,assistant" {
				t.Fatalf("session roles = %s, want the assistant message to be the tail", roles)
			}
		})
	}
}

// A server name of the longest length the policy allows makes PiG cut a long tool name to 64 characters; the tool is
// still gated. A longer server name is refused when the policy is loaded.
func TestRealPigLongServerNameIsStillGated(t *testing.T) {
	mcp, model := startMCP(t), startModel(t)
	if !model.Forces {
		t.Skip("needs a model that can be told which tool to call")
	}
	server := strings.Repeat("s", 40)
	policy := fmt.Sprintf(`{"mcp_servers":[{"name":%q,"url":%q,"headers":{"Authorization":"${KAGENT_PIG_HDR_1}"},"require_approval":true}]}`, server, mcp.URL)
	p := startPig(t, model, runOptions{policy: policy})
	tool := pigToolName(server, strings.Repeat("e", 100))
	if len(tool) != 64 {
		t.Fatalf("tool name %q is not a cut name", tool)
	}
	p.prompt("[call:" + tool + "] go")
	p.until("a confirm for the cut tool name", func(ev map[string]any) bool {
		if ev["type"] == "tool_execution_end" {
			t.Fatalf("a cut tool name was not gated: %v", ev)
		}
		return isConfirm("kagent-approval")(ev)
	})
	if calls := mcp.toolCalls(); len(calls) != 0 {
		t.Fatalf("tools/call before approval: %+v", calls)
	}
}

// pigToolName is PiG's name for an MCP tool: sanitized, and cut to 64 characters with a hash suffix when longer.
func pigToolName(server, tool string) string {
	var b strings.Builder
	for _, r := range "mcp__" + server + "__" + tool {
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	name := b.String()
	if len(name) <= 64 {
		return name
	}
	digest := sha256.Sum256([]byte(server + "\x00" + tool))
	return name[:55] + "_" + hex.EncodeToString(digest[:])[:8]
}

// sessionRoles lists the roles of the message entries of the one session file under dir, system messages left out.
func sessionRoles(t *testing.T, dir string) string {
	t.Helper()
	var file string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(path, ".jsonl") {
			file = path
		}
		return nil
	})
	if file == "" {
		t.Fatal("no session file")
	}
	data, _ := os.ReadFile(file)
	var roles []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Type == "message" && entry.Message.Role != "system" {
			roles = append(roles, entry.Message.Role)
		}
	}
	return strings.Join(roles, ",")
}

// TestRealPigFirstResponse reports how long the Binary takes to answer its first RPC command.
func TestRealPigFirstResponse(t *testing.T) {
	mcp, model := startMCP(t), startModel(t)
	const runs = 15
	var samples []time.Duration
	for range runs {
		start := time.Now()
		p := startPig(t, model, runOptions{policy: policyJSON(mcp.URL, gatedServer(mcp.URL))})
		p.send(map[string]any{"type": "get_state", "id": "s"})
		p.until("get_state", func(ev map[string]any) bool { return ev["type"] == "response" && ev["command"] == "get_state" })
		samples = append(samples, time.Since(start))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	t.Logf("Binary %s, %d bytes, build %s; first response over %d runs: median %s, min %s, max %s",
		binaryPath, binarySize, buildTime.Round(time.Second), runs, samples[runs/2].Round(time.Millisecond), samples[0].Round(time.Millisecond), samples[runs-1].Round(time.Millisecond))
	if samples[runs/2] > 3*time.Second {
		t.Fatalf("median first response %s", samples[runs/2])
	}
}
