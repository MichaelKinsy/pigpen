package acp

// Protocol-level tests: a scripted ACP client speaks JSON-RPC to Serve over pipes, with the pi
// child replaced by a scripted fake. Checked against the protocol schema by
// scripts/acp-schema.test.mjs (schema 0.26.0, protocol version 1).

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type client struct {
	t     *testing.T
	in    *io.PipeWriter
	out   *bufio.Scanner
	mu    sync.Mutex
	nextI int
	// inbox holds every message from the agent in arrival order.
	inbox chan map[string]any
	// methodsSeen records every method the agent called on the client.
	methodsSeen []string
	// permission answers session/request_permission.
	permission func(params map[string]any) map[string]any
	done       chan error
}

func startServer(t *testing.T, agentSetup func(a *Agent)) *client {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &client{t: t, in: inW, out: bufio.NewScanner(outR), inbox: make(chan map[string]any, 1024), done: make(chan error, 1)}
	c.out.Buffer(make([]byte, 1<<20), 1<<26)
	go func() {
		c.done <- Serve(inR, outW, ServeOptions{NewAgent: func(conn Conn) *Agent {
			a := NewAgent(conn)
			a.store = newMemStore()
			if agentSetup != nil {
				agentSetup(a)
			}
			return a
		}})
		outW.Close()
	}()
	go func() {
		for c.out.Scan() {
			var m map[string]any
			if err := json.Unmarshal(c.out.Bytes(), &m); err != nil {
				t.Errorf("agent wrote a line that is not JSON: %q", c.out.Text())
				continue
			}
			if method, ok := m["method"].(string); ok {
				c.mu.Lock()
				c.methodsSeen = append(c.methodsSeen, method)
				c.mu.Unlock()
				if _, isReq := m["id"]; isReq && method == "session/request_permission" {
					answer := map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
					if c.permission != nil {
						answer = c.permission(m["params"].(map[string]any))
					}
					c.write(map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": answer})
				}
			}
			c.inbox <- m
		}
		close(c.inbox)
	}()
	t.Cleanup(func() { inW.Close() })
	return c
}

func (c *client) write(v any) {
	b, _ := json.Marshal(v)
	c.mu.Lock()
	defer c.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := c.in.Write(append(b, '\n')); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			c.t.Logf("write: %v", err)
		}
	case <-time.After(2 * time.Second):
		c.in.CloseWithError(io.ErrClosedPipe)
		c.t.Error("the server does not read its input")
	}
}

func (c *client) request(method string, params any) int {
	c.mu.Lock()
	c.nextI++
	id := c.nextI
	c.mu.Unlock()
	c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return id
}

func (c *client) notify(method string, params any) {
	c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// next returns the next message from the agent.
func (c *client) next() map[string]any {
	c.t.Helper()
	select {
	case m, ok := <-c.inbox:
		if !ok {
			c.t.Fatal("agent closed its output")
		}
		return m
	case <-time.After(3 * time.Second):
		c.t.Fatal("no message from the agent")
		return nil
	}
}

// response waits for the response to id, returning the notifications that arrived before it.
func (c *client) response(id int) (resp map[string]any, before []map[string]any) {
	c.t.Helper()
	for {
		m := c.next()
		if m["id"] == float64(id) && m["method"] == nil {
			return m, before
		}
		before = append(before, m)
	}
}

func (c *client) call(method string, params any) map[string]any {
	c.t.Helper()
	r, _ := c.response(c.request(method, params))
	return r
}

func errCodeOf(resp map[string]any) int {
	if e, ok := resp["error"].(map[string]any); ok {
		return int(e["code"].(float64))
	}
	return 0
}

func scriptedProc(t *testing.T) *fakeProc {
	p := newFakeProc()
	p.getStateFn = func() (map[string]any, error) {
		return map[string]any{"sessionId": "sess-1", "sessionFile": "", "thinkingLevel": "medium", "model": map[string]any{"provider": "test", "id": "model"}}, nil
	}
	p.getModelsFn = func() (map[string]any, error) { return models([3]string{"test", "model", "Model"}), nil }
	p.getLevelsFn = func() ([]string, error) { return []string{"off", "medium"}, nil }
	p.getCommandsFn = func() (map[string]any, error) {
		return map[string]any{"commands": []any{map[string]any{"name": "skill:foo", "description": "Foo", "source": "skill"}}}, nil
	}
	return p
}

func TestServeInitialize(t *testing.T) {
	t.Run("initialize advertises exactly the implemented capabilities", func(t *testing.T) {
		c := startServer(t, nil)
		resp := c.call("initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}})
		result, _ := resp["result"].(map[string]any)
		if result["protocolVersion"] != float64(1) {
			t.Fatalf("result = %v", resp)
		}
		jsonEqual(t, result["agentCapabilities"], map[string]any{
			"loadSession":         true,
			"mcpCapabilities":     map[string]any{"http": false, "sse": false},
			"promptCapabilities":  map[string]any{"image": true, "audio": false, "embeddedContext": false},
			"sessionCapabilities": map[string]any{"list": map[string]any{}, "delete": map[string]any{}},
		})
		info, _ := result["agentInfo"].(map[string]any)
		if info["name"] != "pig-acp" || info["title"] != "PiG ACP adapter" || info["version"] == "" {
			t.Errorf("agentInfo = %v", info)
		}
		methods, _ := result["authMethods"].([]any)
		if len(methods) != 1 {
			t.Errorf("authMethods = %v", methods)
		}
	})

	t.Run("initialize answers protocol version 1 to any requested version", func(t *testing.T) {
		for _, requested := range []int{0, 1, 2, 99} {
			c := startServer(t, nil)
			resp := c.call("initialize", map[string]any{"protocolVersion": requested})
			if v := resp["result"].(map[string]any)["protocolVersion"]; v != float64(1) {
				t.Errorf("requested %d: answered %v", requested, v)
			}
		}
	})

	t.Run("initialize without protocolVersion is invalid params", func(t *testing.T) {
		c := startServer(t, nil)
		if code := errCodeOf(c.call("initialize", map[string]any{})); code != -32602 {
			t.Errorf("code = %d", code)
		}
	})

	t.Run("an unknown method is Method not found", func(t *testing.T) {
		c := startServer(t, nil)
		for _, m := range []string{"foo/bar", "session/fork", "session/resume", "session/close", "fs/read_text_file", "terminal/create"} {
			resp := c.call(m, map[string]any{"sessionId": "s"})
			if errCodeOf(resp) != -32601 {
				t.Errorf("%s: response = %v", m, resp)
			}
		}
	})

	t.Run("authenticate succeeds with an empty result", func(t *testing.T) {
		c := startServer(t, nil)
		resp := c.call("authenticate", map[string]any{"methodId": "pi_terminal_login"})
		jsonEqual(t, resp["result"], map[string]any{})
	})

	t.Run("session/new with a relative cwd is invalid params", func(t *testing.T) {
		c := startServer(t, nil)
		resp := c.call("session/new", map[string]any{"cwd": "relative/dir", "mcpServers": []any{}})
		e, _ := resp["error"].(map[string]any)
		if errCodeOf(resp) != -32602 || !strings.Contains(e["message"].(string), "cwd must be an absolute path") {
			t.Errorf("response = %v", resp)
		}
	})

	t.Run("session/new without cwd is invalid params", func(t *testing.T) {
		c := startServer(t, nil)
		if code := errCodeOf(c.call("session/new", map[string]any{"mcpServers": []any{}})); code != -32602 {
			t.Errorf("code = %d", code)
		}
	})
}

func TestServeSessionFlow(t *testing.T) {
	newFlow := func(t *testing.T, proc *fakeProc) (*client, string) {
		cwd := t.TempDir()
		c := startServer(t, func(a *Agent) {
			a.spawn = func(SpawnParams) (Proc, error) { return proc, nil }
			a.sessions = NewSessionManager(a.spawn, a.store)
		})
		c.call("initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{
			"fs": map[string]any{"readTextFile": true, "writeTextFile": true}, "terminal": true}})
		return c, cwd
	}

	t.Run("session/new answers first, then advertises commands, and never calls fs or terminal", func(t *testing.T) {
		proc := scriptedProc(t)
		c, cwd := newFlow(t, proc)
		id := c.request("session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}})
		resp, before := c.response(id)
		if len(before) != 0 {
			t.Fatalf("notifications before the response: %v", before)
		}
		result, _ := resp["result"].(map[string]any)
		if result["sessionId"] != "sess-1" {
			t.Fatalf("result = %v", result)
		}
		if _, ok := result["configOptions"].([]any); !ok {
			t.Errorf("no configOptions: %v", result)
		}
		// After the response: the startup info chunk and the available commands.
		var kinds []string
		deadline := time.After(3 * time.Second)
		for len(kinds) < 2 {
			select {
			case m := <-c.inbox:
				if m["method"] == "session/update" {
					u := m["params"].(map[string]any)["update"].(map[string]any)
					kinds = append(kinds, u["sessionUpdate"].(string))
					if u["sessionUpdate"] == "available_commands_update" {
						cmds := u["availableCommands"].([]any)
						if cmds[0].(map[string]any)["name"] != "skill:foo" || len(cmds) != 9 {
							t.Errorf("commands = %v", cmds)
						}
					}
				}
			case <-deadline:
				t.Fatalf("saw %v", kinds)
			}
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, m := range c.methodsSeen {
			if strings.HasPrefix(m, "fs/") || strings.HasPrefix(m, "terminal/") {
				t.Errorf("the adapter called %s although it advertises no delegation", m)
			}
		}
	})

	t.Run("session/prompt streams updates before the response and ends with end_turn", func(t *testing.T) {
		proc := scriptedProc(t)
		proc.promptHook = func() {
			go func() {
				proc.emit(Event{"type": "agent_start"})
				proc.emit(Event{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "Hello"}})
				proc.emit(Event{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": " world"}})
				proc.emit(Event{"type": "agent_end"})
				proc.emit(Event{"type": "agent_settled"})
			}()
		}
		c, cwd := newFlow(t, proc)
		c.call("session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}})
		id := c.request("session/prompt", map[string]any{"sessionId": "sess-1", "prompt": []any{map[string]any{"type": "text", "text": "hi"}}})
		resp, before := c.response(id)
		if resp["result"].(map[string]any)["stopReason"] != "end_turn" {
			t.Fatalf("response = %v", resp)
		}
		var text string
		for _, m := range before {
			if m["method"] != "session/update" {
				continue
			}
			u := m["params"].(map[string]any)["update"].(map[string]any)
			if u["sessionUpdate"] == "agent_message_chunk" {
				if s := u["content"].(map[string]any)["text"].(string); s == "Hello" || s == " world" {
					text += s
				}
			}
		}
		if text != "Hello world" {
			t.Errorf("streamed %q", text)
		}
		if got := promptsOf(t, proc, 1); len(got) != 1 || got[0].Message != "hi" {
			t.Errorf("prompts = %v", got)
		}
	})

	t.Run("session/cancel is a notification that aborts the running turn", func(t *testing.T) {
		proc := scriptedProc(t)
		c, cwd := newFlow(t, proc)
		c.call("session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}})
		id := c.request("session/prompt", map[string]any{"sessionId": "sess-1", "prompt": []any{map[string]any{"type": "text", "text": "long"}}})
		eventually(t, "the prompt", func() bool { return len(proc.promptList()) == 1 })
		c.notify("session/cancel", map[string]any{"sessionId": "sess-1"})
		eventually(t, "the abort", func() bool { return proc.aborts() == 1 })
		proc.emit(Event{"type": "agent_settled"})
		resp, _ := c.response(id)
		if resp["result"].(map[string]any)["stopReason"] != "cancelled" {
			t.Errorf("response = %v", resp)
		}
	})

	t.Run("an extension select becomes session/request_permission and the answer reaches pi", func(t *testing.T) {
		proc := scriptedProc(t)
		proc.promptHook = func() {
			go func() {
				proc.emit(Event{"type": "extension_ui_request", "id": "ui-9", "method": "select", "title": "Pick", "options": []any{"A", "B"}})
			}()
		}
		c, cwd := newFlow(t, proc)
		c.permission = func(params map[string]any) map[string]any {
			if params["sessionId"] != "sess-1" {
				t.Errorf("params = %v", params)
			}
			return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "choice-1"}}
		}
		c.call("session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}})
		id := c.request("session/prompt", map[string]any{"sessionId": "sess-1", "prompt": []any{map[string]any{"type": "text", "text": "go"}}})
		eventually(t, "the UI response", func() bool { return len(proc.uiResponses()) == 1 })
		jsonEqual(t, proc.uiResponses(), []any{map[string]any{"id": "ui-9", "value": "B"}})
		proc.emit(Event{"type": "agent_settled"})
		c.response(id)
	})

	t.Run("prompt for an unknown session is invalid params", func(t *testing.T) {
		c, _ := newFlow(t, scriptedProc(t))
		resp := c.call("session/prompt", map[string]any{"sessionId": "nope", "prompt": []any{}})
		if errCodeOf(resp) != -32602 {
			t.Errorf("response = %v", resp)
		}
	})

	t.Run("closing stdin disposes every pi child and ends Serve", func(t *testing.T) {
		proc := scriptedProc(t)
		c, cwd := newFlow(t, proc)
		c.call("session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}})
		c.in.Close()
		select {
		case <-c.done:
		case <-time.After(3 * time.Second):
			t.Fatal("Serve did not return after the client closed stdin")
		}
		if proc.disposeCount() != 1 {
			t.Errorf("pi child disposed %d times", proc.disposeCount())
		}
	})
}
