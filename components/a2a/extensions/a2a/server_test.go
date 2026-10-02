package a2aext

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
)

// scriptedWorker is a Worker whose behaviour a test sets.
type scriptedWorker struct {
	mu      sync.Mutex
	turns   []Turn
	run     func(ctx context.Context, t Turn, up func(Update)) (Result, error)
	running atomic.Int32
	maxRun  atomic.Int32
}

func (w *scriptedWorker) Run(ctx context.Context, t Turn, up func(Update)) (Result, error) {
	w.mu.Lock()
	w.turns = append(w.turns, t)
	w.mu.Unlock()
	n := w.running.Add(1)
	defer w.running.Add(-1)
	for {
		m := w.maxRun.Load()
		if n <= m || w.maxRun.CompareAndSwap(m, n) {
			break
		}
	}
	if w.run != nil {
		return w.run(ctx, t, up)
	}
	up(Update{Text: "echo: "})
	up(Update{Text: t.Prompt})
	return Result{Text: "echo: " + t.Prompt}, nil
}

func (w *scriptedWorker) Turns() []Turn {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Turn(nil), w.turns...)
}

func serverConfig() Config {
	return Config{
		Listen: "127.0.0.1:0",
		Tokens: []TokenConfig{{Name: "alice", TokenEnv: "TOKEN_A", Tenant: "team-a"}, {Name: "bob", TokenEnv: "TOKEN_B"}},
		Name:   "pig-under-test", MaxConcurrentTasks: 4, TaskTimeoutSeconds: 60,
	}
}

var serverEnv = envFrom(map[string]string{"TOKEN_A": tokenA, "TOKEN_B": tokenB})

func startServer(t *testing.T, cfg Config, w Worker) *Server {
	t.Helper()
	s, err := NewServer(cfg, w, serverEnv)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return s
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	base := b.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r)
}

func a2aClient(t *testing.T, baseURL, token string) *a2aclient.Client {
	t.Helper()
	hc := &http.Client{Transport: bearerTransport{token: token}, Timeout: 30 * time.Second}
	c, err := a2aclient.NewFromEndpoints(context.Background(),
		[]*a2a.AgentInterface{a2a.NewAgentInterface(baseURL, a2a.TransportProtocolJSONRPC)},
		a2aclient.WithJSONRPCTransport(hc))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func textMessage(text string) *a2a.Message {
	return a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))
}

func taskText(task *a2a.Task) string {
	var b strings.Builder
	for _, art := range task.Artifacts {
		for _, p := range art.Parts {
			b.WriteString(p.Text())
		}
	}
	return b.String()
}

func mustTask(t *testing.T, res a2a.SendMessageResult, err error) *a2a.Task {
	t.Helper()
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	task, ok := res.(*a2a.Task)
	if !ok {
		t.Fatalf("want a task, got %T", res)
	}
	return task
}

func TestAgentCardIsPublicAndPinnedToProtocol10(t *testing.T) {
	cfg := serverConfig()
	s := startServer(t, cfg, &scriptedWorker{})
	resp, err := http.Get("http://" + s.Addr() + "/.well-known/agent-card.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("the agent card is public: status %d", resp.StatusCode)
	}
	var card a2a.AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	if card.Name != "pig-under-test" || !card.Capabilities.Streaming || card.Capabilities.PushNotifications {
		t.Fatalf("card %+v", card)
	}
	if len(card.SupportedInterfaces) != 1 {
		t.Fatalf("interfaces %+v", card.SupportedInterfaces)
	}
	i := card.SupportedInterfaces[0]
	if i.ProtocolVersion != "1.0" || i.ProtocolBinding != a2a.TransportProtocolJSONRPC || i.URL != "http://"+s.Addr() {
		t.Fatalf("interface %+v", i)
	}
	if len(card.SecuritySchemes) == 0 || len(card.SecurityRequirements) == 0 {
		t.Fatalf("the card must declare bearer authentication: %+v", card)
	}
	if len(card.Skills) == 0 {
		t.Fatal("a card needs at least one skill")
	}
}

func TestAgentCardAdvertisesExternalURL(t *testing.T) {
	cfg := serverConfig()
	cfg.ExternalURL = "https://pig.example.test/a2a"
	s := startServer(t, cfg, &scriptedWorker{})
	resp, err := http.Get("http://" + s.Addr() + "/.well-known/agent-card.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var card a2a.AgentCard
	_ = json.NewDecoder(resp.Body).Decode(&card)
	if len(card.SupportedInterfaces) != 1 || card.SupportedInterfaces[0].URL != "https://pig.example.test/a2a" {
		t.Fatalf("%+v", card.SupportedInterfaces)
	}
}

func TestUnauthenticatedRequestIsRejectedBeforeTheWorker(t *testing.T) {
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	for name, token := range map[string]string{"none": "", "wrong": "nope-nope-nope-nope-nope-nope-nope"} {
		c := a2aClient(t, "http://"+s.Addr(), token)
		if _, err := c.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: textMessage("hi")}); err == nil {
			t.Fatalf("%s: want an error", name)
		}
	}
	if len(w.Turns()) != 0 {
		t.Fatal("an unauthenticated request reached the worker")
	}
}

func TestSendMessageRunsATurnAndReturnsACompletedTask(t *testing.T) {
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	res, err := c.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: textMessage("hello pig")})
	task := mustTask(t, res, err)
	if task.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("state %s", task.Status.State)
	}
	if taskText(task) != "echo: hello pig" {
		t.Fatalf("artifact text %q", taskText(task))
	}
	if task.ID == "" || task.ContextID == "" {
		t.Fatalf("ids %q %q", task.ID, task.ContextID)
	}
	turns := w.Turns()
	if len(turns) != 1 || turns[0].Prompt != "hello pig" || turns[0].Principal.Name != "alice" || turns[0].ContextID != task.ContextID || turns[0].TaskID != string(task.ID) {
		t.Fatalf("turn %+v", turns)
	}
}

func TestSameContextIDContinuesTheSameWorkerContext(t *testing.T) {
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	first := sendTask(t, c, &a2a.SendMessageRequest{Message: textMessage("one")})
	m2 := textMessage("two")
	m2.ContextID = first.ContextID
	second := sendTask(t, c, &a2a.SendMessageRequest{Message: m2})
	if second.ContextID != first.ContextID || second.ID == first.ID {
		t.Fatalf("second task %s/%s in first %s/%s: a context spans tasks", second.ID, second.ContextID, first.ID, first.ContextID)
	}
	turns := w.Turns()
	if len(turns) != 2 || turns[0].ContextID != turns[1].ContextID {
		t.Fatalf("turns %+v", turns)
	}
}

func mustSend(c *a2aclient.Client, req *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	return c.SendMessage(context.Background(), req)
}

func TestTenantsDoNotShareContextsOrTasks(t *testing.T) {
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	ca := a2aClient(t, "http://"+s.Addr(), tokenA)
	cb := a2aClient(t, "http://"+s.Addr(), tokenB)
	ma := textMessage("from alice")
	ma.ContextID = "shared-context"
	taskA := sendTask(t, ca, &a2a.SendMessageRequest{Message: ma})
	mb := textMessage("from bob")
	mb.ContextID = "shared-context"
	sendTask(t, cb, &a2a.SendMessageRequest{Message: mb})
	turns := w.Turns()
	if len(turns) != 2 || turns[0].Principal.Key() == turns[1].Principal.Key() {
		t.Fatalf("the same context id under two principals must reach the worker as two identities: %+v", turns)
	}
	if _, err := cb.GetTask(context.Background(), &a2a.GetTaskRequest{ID: taskA.ID}); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Fatalf("bob read alice's task: %v", err)
	}
	if _, err := cb.CancelTask(context.Background(), &a2a.CancelTaskRequest{ID: taskA.ID}); err == nil {
		t.Fatal("bob cancelled alice's task")
	}
	list, err := cb.ListTasks(context.Background(), &a2a.ListTasksRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range list.Tasks {
		if task.ID == taskA.ID {
			t.Fatal("bob's task list shows alice's task")
		}
	}
	if got, err := ca.GetTask(context.Background(), &a2a.GetTaskRequest{ID: taskA.ID}); err != nil || got.ID != taskA.ID {
		t.Fatalf("alice must read her own task: %v", err)
	}
}

func TestBobCannotAttachToAliceTaskWithAMessage(t *testing.T) {
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	ca := a2aClient(t, "http://"+s.Addr(), tokenA)
	cb := a2aClient(t, "http://"+s.Addr(), tokenB)
	taskA := sendTask(t, ca, &a2a.SendMessageRequest{Message: textMessage("x y")})
	m := textMessage("hijack")
	m.TaskID, m.ContextID = taskA.ID, taskA.ContextID
	before := len(w.Turns())
	if _, err := cb.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: m}); err == nil {
		t.Fatal("bob continued alice's task")
	}
	if len(w.Turns()) != before {
		t.Fatal("the worker ran for a foreign task")
	}
}

func TestRequestTenantMustMatchTheTokensTenant(t *testing.T) {
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	if _, err := c.SendMessage(context.Background(), &a2a.SendMessageRequest{Tenant: "team-b", Message: textMessage("x y")}); err == nil {
		t.Fatal("a caller must not name another tenant than its credential's")
	}
	if len(w.Turns()) != 0 {
		t.Fatal("worker ran for a foreign tenant")
	}
	if _, err := c.SendMessage(context.Background(), &a2a.SendMessageRequest{Tenant: "team-a", Message: textMessage("x y")}); err != nil {
		t.Fatalf("its own tenant is fine: %v", err)
	}
}

func rawRPC(t *testing.T, url, token, version, method string, params any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if version != "" {
		req.Header.Set("A2A-Version", version)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestProtocolVersionIsPinned(t *testing.T) {
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	params := map[string]any{"message": map[string]any{"messageId": "m1", "role": "ROLE_USER", "parts": []any{map[string]any{"text": "hello there"}}}}
	for name, version := range map[string]string{"0.3": "0.3", "2.0": "2.0", "absent (spec: 0.3)": "", "garbage": "one"} {
		_, out := rawRPC(t, "http://"+s.Addr(), tokenA, version, "SendMessage", params)
		e, _ := out["error"].(map[string]any)
		if e == nil {
			t.Fatalf("%s: want a JSON-RPC error, got %v", name, out)
		}
		if msg, _ := e["message"].(string); !strings.Contains(strings.ToLower(msg), "version") {
			t.Fatalf("%s: error should say the version is unsupported: %v", name, e)
		}
	}
	if len(w.Turns()) != 0 {
		t.Fatal("a request with an unsupported protocol version reached the worker")
	}
	_, out := rawRPC(t, "http://"+s.Addr(), tokenA, "1.0", "SendMessage", params)
	if out["error"] != nil || out["result"] == nil {
		t.Fatalf("1.0 must work: %v", out)
	}
}

func TestStreamingDeliversArtifactChunksBeforeCompletion(t *testing.T) {
	gate := make(chan struct{})
	w := &scriptedWorker{run: func(ctx context.Context, tn Turn, up func(Update)) (Result, error) {
		up(Update{Text: "first "})
		select {
		case <-gate:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
		up(Update{Text: "second"})
		return Result{Text: "first second"}, nil
	}}
	s := startServer(t, serverConfig(), w)
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	var seenChunkBeforeGate bool
	var final a2a.TaskState
	var text strings.Builder
	for ev, err := range c.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{Message: textMessage("stream me")}) {
		if err != nil {
			t.Fatal(err)
		}
		switch e := ev.(type) {
		case *a2a.TaskArtifactUpdateEvent:
			for _, p := range e.Artifact.Parts {
				text.WriteString(p.Text())
			}
			if strings.Contains(text.String(), "first") && !seenChunkBeforeGate {
				seenChunkBeforeGate = true
				close(gate)
			}
		case *a2a.TaskStatusUpdateEvent:
			final = e.Status.State
		}
	}
	if !seenChunkBeforeGate {
		t.Fatal("no artifact chunk arrived before the worker finished")
	}
	if final != a2a.TaskStateCompleted || text.String() != "first second" {
		t.Fatalf("final %s text %q", final, text.String())
	}
}

func TestCancelTaskAbortsTheWorkerAndEndsCanceled(t *testing.T) {
	started := make(chan struct{})
	var ctxErr atomic.Value
	w := &scriptedWorker{run: func(ctx context.Context, tn Turn, up func(Update)) (Result, error) {
		close(started)
		<-ctx.Done()
		ctxErr.Store(ctx.Err())
		return Result{}, ctx.Err()
	}}
	s := startServer(t, serverConfig(), w)
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	var taskID a2a.TaskID
	events := c.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{Message: textMessage("long job")})
	done := make(chan a2a.TaskState, 1)
	go func() {
		var last a2a.TaskState
		for ev, err := range events {
			if err != nil {
				break
			}
			switch e := ev.(type) {
			case *a2a.Task:
				taskID, last = e.ID, e.Status.State
			case *a2a.TaskStatusUpdateEvent:
				last = e.Status.State
			}
		}
		done <- last
	}()
	waitChan(t, started, "the worker to start")
	var task *a2a.Task
	deadline := time.Now().Add(5 * time.Second)
	for task == nil && time.Now().Before(deadline) {
		list, err := c.ListTasks(context.Background(), &a2a.ListTasksRequest{})
		if err == nil && len(list.Tasks) == 1 {
			task = list.Tasks[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	if task == nil {
		t.Fatal("running task not listed")
	}
	got, err := c.CancelTask(context.Background(), &a2a.CancelTaskRequest{ID: task.ID})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got.Status.State != a2a.TaskStateCanceled {
		t.Fatalf("state %s", got.Status.State)
	}
	select {
	case last := <-done:
		if last != a2a.TaskStateCanceled {
			t.Fatalf("stream ended in %s", last)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stream did not end")
	}
	if !errors.Is(ctxErr.Load().(error), context.Canceled) {
		t.Fatalf("worker context error %v", ctxErr.Load())
	}
	_ = taskID
	waitFor(t, func() bool { return s.ActiveTasks() == 0 }, "active tasks to drain")
}

func waitFor(t *testing.T, ok func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWorkerFailureBecomesAFailedTask(t *testing.T) {
	w := &scriptedWorker{run: func(context.Context, Turn, func(Update)) (Result, error) {
		return Result{Failure: "the model call failed"}, nil
	}}
	s := startServer(t, serverConfig(), w)
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	task := sendTask(t, c, &a2a.SendMessageRequest{Message: textMessage("x y")})
	if task.Status.State != a2a.TaskStateFailed {
		t.Fatalf("state %s", task.Status.State)
	}
	if task.Status.Message == nil || !strings.Contains(task.Status.Message.Parts[0].Text(), "the model call failed") {
		t.Fatalf("status message %+v", task.Status.Message)
	}
}

func TestWorkerErrorDetailIsNotSentToThePeer(t *testing.T) {
	w := &scriptedWorker{run: func(context.Context, Turn, func(Update)) (Result, error) {
		return Result{}, errors.New("exec /opt/secret/pig: permission denied")
	}}
	s := startServer(t, serverConfig(), w)
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	task := sendTask(t, c, &a2a.SendMessageRequest{Message: textMessage("x y")})
	if task.Status.State != a2a.TaskStateFailed {
		t.Fatalf("state %s", task.Status.State)
	}
	raw, _ := json.Marshal(task)
	if strings.Contains(string(raw), "/opt/secret") {
		t.Fatalf("internal error detail leaked to the peer: %s", raw)
	}
}

func TestConcurrencyLimitQueuesTasks(t *testing.T) {
	release := make(chan struct{})
	w := &scriptedWorker{run: func(ctx context.Context, tn Turn, up func(Update)) (Result, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return Result{Text: "ok"}, nil
	}}
	cfg := serverConfig()
	cfg.MaxConcurrentTasks = 1
	s := startServer(t, cfg, w)
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: textMessage("job job")})
		}()
	}
	waitFor(t, func() bool { return w.running.Load() == 1 }, "the first task to start")
	time.Sleep(200 * time.Millisecond)
	if w.running.Load() != 1 {
		t.Fatalf("%d tasks running with a limit of 1", w.running.Load())
	}
	close(release)
	wg.Wait()
	if w.maxRun.Load() != 1 {
		t.Fatalf("max concurrent %d", w.maxRun.Load())
	}
}

func TestTasksInOneContextRunOneAtATime(t *testing.T) {
	w := &scriptedWorker{run: func(ctx context.Context, tn Turn, up func(Update)) (Result, error) {
		time.Sleep(100 * time.Millisecond)
		return Result{Text: "ok"}, nil
	}}
	s := startServer(t, serverConfig(), w)
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := textMessage("same ctx")
			m.ContextID = "one-context"
			_, _ = c.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: m})
		}()
	}
	wg.Wait()
	if w.maxRun.Load() != 1 || len(w.Turns()) != 3 {
		t.Fatalf("a session file has one writer: max concurrent %d, turns %d", w.maxRun.Load(), len(w.Turns()))
	}
}

func TestNonTextPartsAreRejected(t *testing.T) {
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	m := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewRawPart([]byte("binary")))
	if _, err := c.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: m}); err == nil {
		t.Fatal("PiG tasks take text; a file part must be refused, not dropped")
	}
	if len(w.Turns()) != 0 {
		t.Fatal("worker ran")
	}
}

func TestEmptyMessageIsRejected(t *testing.T) {
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	if _, err := c.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: textMessage("   ")}); err == nil {
		t.Fatal("empty prompt")
	}
	if len(w.Turns()) != 0 {
		t.Fatal("worker ran")
	}
}

func TestTaskTimeoutFailsTheTask(t *testing.T) {
	var ctxDone atomic.Bool
	w := &scriptedWorker{run: func(ctx context.Context, tn Turn, up func(Update)) (Result, error) {
		<-ctx.Done()
		ctxDone.Store(true)
		return Result{}, ctx.Err()
	}}
	cfg := serverConfig()
	cfg.TaskTimeoutSeconds = 1
	s := startServer(t, cfg, w)
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	task := sendTask(t, c, &a2a.SendMessageRequest{Message: textMessage("never ends")})
	if task.Status.State != a2a.TaskStateFailed || !ctxDone.Load() {
		t.Fatalf("state %s ctxDone %v", task.Status.State, ctxDone.Load())
	}
	if task.Status.Message == nil || !strings.Contains(strings.ToLower(task.Status.Message.Parts[0].Text()), "time") {
		t.Fatalf("the failure should say the task timed out: %+v", task.Status.Message)
	}
}

func TestShutdownCancelsRunningTasksAndStopsListening(t *testing.T) {
	started := make(chan struct{})
	var canceled atomic.Bool
	w := &scriptedWorker{run: func(ctx context.Context, tn Turn, up func(Update)) (Result, error) {
		close(started)
		<-ctx.Done()
		canceled.Store(true)
		return Result{}, ctx.Err()
	}}
	s, err := NewServer(serverConfig(), w, serverEnv)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	addr := s.Addr()
	c := a2aClient(t, "http://"+addr, tokenA)
	go func() {
		_, _ = c.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: textMessage("long job")})
	}()
	waitChan(t, started, "the worker to start")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !canceled.Load() {
		t.Fatal("shutdown must cancel running turns")
	}
	if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		conn.Close()
		t.Fatal("still listening after shutdown")
	}
	if s.ActiveTasks() != 0 {
		t.Fatalf("%d active tasks after shutdown", s.ActiveTasks())
	}
}

func TestStartFailsOnAnAddressInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cfg := serverConfig()
	cfg.Listen = ln.Addr().String()
	s, err := NewServer(cfg, &scriptedWorker{}, serverEnv)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err == nil {
		t.Fatal("want a bind error, not a silent no-op")
	}
}

func TestOversizedRequestBodyIsRefused(t *testing.T) {
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	big := strings.Repeat("a", 3<<20)
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "SendMessage", "params": map[string]any{
		"message": map[string]any{"messageId": "m", "role": "ROLE_USER", "parts": []any{map[string]any{"text": big}}}}})
	req, _ := http.NewRequest(http.MethodPost, "http://"+s.Addr(), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tokenA)
	req.Header.Set("A2A-Version", "1.0")
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode == 200 {
			var out map[string]any
			_ = out
		}
	}
	if len(w.Turns()) != 0 {
		t.Fatal("a 3 MiB request reached the worker; bodies are capped")
	}
}

func selfSignedPair(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return
}

func TestTLSListener(t *testing.T) {
	certFile, keyFile, pool := selfSignedPair(t)
	cfg := serverConfig()
	cfg.TLS = &TLSConfig{CertFile: certFile, KeyFile: keyFile}
	s := startServer(t, cfg, &scriptedWorker{})
	hc := &http.Client{Transport: bearerTransport{token: tokenA, base: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}}
	c, err := a2aclient.NewFromEndpoints(context.Background(),
		[]*a2a.AgentInterface{a2a.NewAgentInterface("https://"+s.Addr(), a2a.TransportProtocolJSONRPC)}, a2aclient.WithJSONRPCTransport(hc))
	if err != nil {
		t.Fatal(err)
	}
	task := sendTask(t, c, &a2a.SendMessageRequest{Message: textMessage("over tls")})
	if task.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("state %s", task.Status.State)
	}
	if plain, err := http.Get("http://" + s.Addr() + "/.well-known/agent-card.json"); err == nil {
		defer plain.Body.Close()
		if plain.StatusCode == 200 {
			t.Fatal("plain HTTP must not be served on a TLS listener")
		}
	}
}

func sendTask(t *testing.T, c *a2aclient.Client, req *a2a.SendMessageRequest) *a2a.Task {
	t.Helper()
	res, err := c.SendMessage(context.Background(), req)
	return mustTask(t, res, err)
}

func waitChan(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestUnreadableTLSKeyPairFailsStart(t *testing.T) { // tls-load-failure-ignored
	cfg := serverConfig()
	cfg.TLS = &TLSConfig{CertFile: filepath.Join(t.TempDir(), "missing.pem"), KeyFile: filepath.Join(t.TempDir(), "missing.key")}
	s, err := NewServer(cfg, &scriptedWorker{}, serverEnv)
	if err != nil {
		return // refused at construction is fine too
	}
	if err := s.Start(); err == nil {
		_ = s.Shutdown(context.Background())
		t.Fatal("a listener that cannot load its certificate must not start (nor fall back to plain HTTP)")
	}
}

// http.Server only closes listeners its Serve goroutine has registered; a Shutdown that wins the race with that
// goroutine must still close the port before it returns (seen as a flaky reload test at -race -count=24 under load).
func TestShutdownClosesThePortEvenRightAfterStart(t *testing.T) {
	for i := 0; i < 300; i++ {
		s, err := NewServer(serverConfig(), &scriptedWorker{}, serverEnv)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Start(); err != nil {
			t.Fatal(err)
		}
		addr := s.Addr()
		if err := s.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			c.Close()
			t.Fatalf("iteration %d: %s still accepts connections after Shutdown returned", i, addr)
		}
	}
}
