package a2aext

// End to end with a real PiG: the ProcessWorker starts the actual `pig --mode rpc` (or a Piglet
// Binary) against a local OpenAI-compatible server, in a temporary HOME / PIG_HOME /
// PIG_CODING_AGENT_DIR (rule 17: no real credentials, no ~/.pig). Set PIG_A2A_E2E_BIN to run.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

type e2eLLM struct {
	ln    net.Listener
	mu    sync.Mutex
	tools [][]string
	hung  chan struct{}
}

func startE2ELLM(t *testing.T) *e2eLLM {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := &e2eLLM{ln: ln, hung: make(chan struct{}, 8)}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", l.handle)
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return l
}

func (l *e2eLLM) baseURL() string { return "http://" + l.ln.Addr().String() + "/v1" }

type msg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (m msg) text() string {
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s
	}
	var blocks []struct{ Text string }
	if json.Unmarshal(m.Content, &blocks) == nil {
		var b strings.Builder
		for _, x := range blocks {
			b.WriteString(x.Text)
		}
		return b.String()
	}
	return ""
}

func (l *e2eLLM) handle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []msg `json:"messages"`
		Tools    []struct {
			Function struct{ Name string } `json:"function"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var names []string
	for _, t := range req.Tools {
		names = append(names, t.Function.Name)
	}
	sort.Strings(names)
	l.mu.Lock()
	l.tools = append(l.tools, names)
	l.mu.Unlock()

	var lastUser, lastTool string
	var all strings.Builder
	for _, m := range req.Messages {
		all.WriteString(m.text() + "\n")
		switch m.Role {
		case "user":
			lastUser = m.text()
		case "tool":
			lastTool = m.text()
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fl, _ := w.(http.Flusher)
	send := func(delta map[string]any, finish any) {
		b, _ := json.Marshal(map[string]any{"id": "e2e", "object": "chat.completion.chunk", "created": 1, "model": "e2e-1",
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}
	done := func(finish string) {
		send(map[string]any{}, finish)
		fmt.Fprint(w, "data: {\"id\":\"e2e\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"e2e-1\",\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n")
		fl.Flush()
	}
	send(map[string]any{"role": "assistant", "content": ""}, nil)
	switch {
	case strings.Contains(lastUser, "HANG"):
		send(map[string]any{"content": "thinking..."}, nil)
		l.hung <- struct{}{}
		<-r.Context().Done() // until pig drops the connection
		return
	case strings.HasPrefix(lastUser, "read the file ") && lastTool == "":
		args, _ := json.Marshal(map[string]any{"path": strings.TrimPrefix(lastUser, "read the file ")})
		send(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function",
			"function": map[string]any{"name": "read", "arguments": string(args)}}}}, nil)
		done("tool_calls")
	case strings.HasPrefix(lastUser, "read the file "):
		send(map[string]any{"content": "The file says: " + strings.TrimSpace(lastTool)}, nil)
		done("stop")
	case strings.Contains(lastUser, "read the note") && lastTool == "":
		args, _ := json.Marshal(map[string]any{"path": "note.txt"})
		send(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function",
			"function": map[string]any{"name": "read", "arguments": string(args)}}}}, nil)
		done("tool_calls")
	case strings.Contains(lastUser, "read the note"):
		send(map[string]any{"content": "The note says: " + strings.TrimSpace(lastTool)}, nil)
		done("stop")
	case strings.Contains(lastUser, "what is my number"):
		if strings.Contains(all.String(), "my number is 42") {
			send(map[string]any{"content": "Your number is 42."}, nil)
		} else {
			send(map[string]any{"content": "I do not know your number."}, nil)
		}
		done("stop")
	default:
		send(map[string]any{"content": "Noted: " + lastUser}, nil)
		done("stop")
	}
}

type e2eEnv struct {
	worker *ProcessWorker
	server *Server
	llm    *e2eLLM
	state  string
	work   string
}

func startE2E(t *testing.T) *e2eEnv {
	t.Helper()
	bin := os.Getenv("PIG_A2A_E2E_BIN")
	if bin == "" {
		t.Skip("set PIG_A2A_E2E_BIN to a pig (or Piglet Binary) executable to run the real-PiG end-to-end tests")
	}
	home := t.TempDir()
	agent := filepath.Join(home, "pig", "agent")
	if err := os.MkdirAll(agent, 0o700); err != nil {
		t.Fatal(err)
	}
	llm := startE2ELLM(t)
	models := map[string]any{"providers": map[string]any{"e2e": map[string]any{
		"baseUrl": llm.baseURL(), "api": "openai-completions", "apiKey": "e2e-key",
		"models": []any{map[string]any{"id": "e2e-1", "name": "e2e-1", "reasoning": false, "input": []string{"text"},
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
	if err := os.WriteFile(filepath.Join(work, "note.txt"), []byte("hello from the workspace"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"PATH": os.Getenv("PATH"), "HOME": home, "PIG_HOME": filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR": agent,
		"TOKEN_A": tokenA, "TOKEN_B": tokenB,
	}
	getenv := func(k string) string { return env[k] }
	state := filepath.Join(home, "a2a-state")
	w, err := NewProcessWorker(WorkerConfig{Command: bin, Cwd: work, Provider: "e2e", Model: "e2e-1", Tools: []string{"read", "grep", "find", "ls"}, GraceSeconds: 5}, state, getenv)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(serverConfig(), w, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return &e2eEnv{worker: w, server: s, llm: llm, state: state, work: work}
}

func TestE2E_TaskRunsRealPiGWithAToolCall(t *testing.T) {
	e := startE2E(t)
	c := a2aClient(t, "http://"+e.server.Addr(), tokenA)
	task := sendTask(t, c, &a2a.SendMessageRequest{Message: textMessage("please read the note")})
	if task.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("state %s: %+v", task.Status.State, task.Status.Message)
	}
	if got := taskText(task); !strings.Contains(got, "The note says: hello from the workspace") {
		t.Fatalf("artifact %q", got)
	}
	// The worker offered exactly the read-only tools.
	e.llm.mu.Lock()
	defer e.llm.mu.Unlock()
	for _, tools := range e.llm.tools {
		if strings.Join(tools, ",") != "find,grep,ls,read" {
			t.Fatalf("the model was offered %v; the worker must be read-only by default", tools)
		}
	}
}

func TestE2E_ContextContinuesAcrossTasksAndTenantsAreIsolated(t *testing.T) {
	e := startE2E(t)
	ca := a2aClient(t, "http://"+e.server.Addr(), tokenA)
	cb := a2aClient(t, "http://"+e.server.Addr(), tokenB)
	m1 := textMessage("my number is 42")
	m1.ContextID = "conv-1"
	first := sendTask(t, ca, &a2a.SendMessageRequest{Message: m1})
	if first.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("%s", first.Status.State)
	}
	m2 := textMessage("what is my number")
	m2.ContextID = "conv-1"
	if got := taskText(sendTask(t, ca, &a2a.SendMessageRequest{Message: m2})); !strings.Contains(got, "42") {
		t.Fatalf("the same contextId must continue the PiG session: %q", got)
	}
	m3 := textMessage("what is my number")
	m3.ContextID = "conv-1"
	if got := taskText(sendTask(t, cb, &a2a.SendMessageRequest{Message: m3})); strings.Contains(got, "42") {
		t.Fatalf("another tenant's context leaked into this one: %q", got)
	}
	// Session files: one directory per principal, file names carry the derived id only.
	dirs, _ := filepath.Glob(filepath.Join(e.state, "sessions", "*", "*.jsonl"))
	if len(dirs) != 2 {
		t.Fatalf("want 2 session files (tenant team-a context, token bob context), got %v", dirs)
	}
	for _, f := range dirs {
		if strings.Contains(f, "conv-1") {
			t.Fatalf("context id in a file name: %s", f)
		}
	}
}

func TestE2E_CancelAbortsRealPiG(t *testing.T) {
	e := startE2E(t)
	c := a2aClient(t, "http://"+e.server.Addr(), tokenA)
	var id a2a.TaskID
	events := c.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{Message: textMessage("HANG please")})
	last := make(chan a2a.TaskState, 1)
	go func() {
		var s a2a.TaskState
		for ev, err := range events {
			if err != nil {
				break
			}
			switch x := ev.(type) {
			case *a2a.Task:
				id, s = x.ID, x.Status.State
			case *a2a.TaskStatusUpdateEvent:
				s = x.Status.State
			}
		}
		last <- s
	}()
	select {
	case <-e.llm.hung:
	case <-time.After(30 * time.Second):
		t.Fatal("the model was never called")
	}
	var tid a2a.TaskID
	waitFor(t, func() bool {
		list, err := c.ListTasks(context.Background(), &a2a.ListTasksRequest{})
		if err == nil && len(list.Tasks) == 1 {
			tid = list.Tasks[0].ID
			return true
		}
		return false
	}, "the task to be listed")
	got, err := c.CancelTask(context.Background(), &a2a.CancelTaskRequest{ID: tid})
	if err != nil || got.Status.State != a2a.TaskStateCanceled {
		t.Fatalf("%v %v", got, err)
	}
	select {
	case s := <-last:
		if s != a2a.TaskStateCanceled {
			t.Fatalf("stream ended in %s", s)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("stream did not end")
	}
	waitFor(t, func() bool { return e.server.ActiveTasks() == 0 }, "the worker to be reaped")
	_ = id
}
