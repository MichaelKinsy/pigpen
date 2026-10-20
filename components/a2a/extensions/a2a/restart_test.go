package a2aext

// Tasks across a restart of the listener: the stored task is served again, ownership holds, an interrupted
// task is not reported as running, SubscribeToTask answers for stored tasks.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
)

const tokenC = "cccccccccccccccccccccccccccccccc"

func persistentConfig(t *testing.T) Config {
	t.Helper()
	cfg := serverConfig()
	cfg.StateDir = t.TempDir()
	// carol shares alice's tenant, so a tenant's tasks can be told from a principal's.
	cfg.Tokens = append(cfg.Tokens, TokenConfig{Name: "carol", TokenEnv: "TOKEN_C", Tenant: "team-a"})
	return cfg
}

var restartEnv = envFrom(map[string]string{"TOKEN_A": tokenA, "TOKEN_B": tokenB, "TOKEN_C": tokenC})

// startAt starts a listener on cfg and returns it with a function that stops it.
func startAt(t *testing.T, cfg Config, w Worker) (*Server, func()) {
	t.Helper()
	s, err := NewServer(cfg, w, restartEnv)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	}
	t.Cleanup(stop)
	return s, stop
}

func TestTaskSurvivesAListenerRestart(t *testing.T) {
	cfg := persistentConfig(t)
	first, stop := startAt(t, cfg, &scriptedWorker{})
	c := a2aClient(t, "http://"+first.Addr(), tokenA)
	sent := sendTask(t, c, &a2a.SendMessageRequest{Message: textMessage("remember me")})
	stop()

	second, _ := startAt(t, cfg, &scriptedWorker{})
	c2 := a2aClient(t, "http://"+second.Addr(), tokenA)
	got, err := c2.GetTask(context.Background(), &a2a.GetTaskRequest{ID: sent.ID})
	if err != nil {
		t.Fatalf("GetTask after the restart: %v", err)
	}
	if got.Status.State != a2a.TaskStateCompleted || got.ContextID != sent.ContextID || taskText(got) != "echo: remember me" {
		t.Fatalf("stored task came back as %s %q (context %s)", got.Status.State, taskText(got), got.ContextID)
	}
	list, err := c2.ListTasks(context.Background(), &a2a.ListTasksRequest{ContextID: sent.ContextID, IncludeArtifacts: true})
	if err != nil || len(list.Tasks) != 1 || list.Tasks[0].ID != sent.ID {
		t.Fatalf("ListTasks after the restart: %+v %v", list, err)
	}
	files, _ := filepath.Glob(filepath.Join(cfg.StateDir, "tasks", "*"))
	if len(files) != 1 {
		t.Fatalf("one task file under stateDir/tasks, got %v", files)
	}
}

func TestRestartKeepsTaskOwnership(t *testing.T) {
	cfg := persistentConfig(t)
	first, stop := startAt(t, cfg, &scriptedWorker{})
	sent := sendTask(t, a2aClient(t, "http://"+first.Addr(), tokenA), &a2a.SendMessageRequest{Message: textMessage("alice's")})
	stop()

	w2 := &scriptedWorker{}
	second, _ := startAt(t, cfg, w2)
	base := "http://" + second.Addr()
	bob := a2aClient(t, base, tokenB)
	if _, err := bob.GetTask(context.Background(), &a2a.GetTaskRequest{ID: sent.ID}); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Errorf("another tenant reads a stored task: %v", err)
	}
	if list, err := bob.ListTasks(context.Background(), &a2a.ListTasksRequest{}); err != nil || len(list.Tasks) != 0 {
		t.Errorf("another tenant lists a stored task: %+v %v", list, err)
	}
	if _, err := bob.CancelTask(context.Background(), &a2a.CancelTaskRequest{ID: sent.ID}); err == nil {
		t.Error("another tenant cancelled a stored task")
	}
	for _, err := range bob.SubscribeToTask(context.Background(), &a2a.SubscribeToTaskRequest{ID: sent.ID}) {
		if !errors.Is(err, a2a.ErrTaskNotFound) {
			t.Errorf("another tenant subscribed to a stored task: %v", err)
		}
		break
	}
	follow := textMessage("attach to it")
	follow.TaskID, follow.ContextID = sent.ID, sent.ContextID
	if _, err := bob.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: follow}); err == nil || len(w2.Turns()) != 0 {
		t.Errorf("another tenant continued a stored task: %v", err)
	}
	// A token of the same tenant is the same owner, across the restart.
	carol := a2aClient(t, base, tokenC)
	if got, err := carol.GetTask(context.Background(), &a2a.GetTaskRequest{ID: sent.ID}); err != nil || got.Status.State != a2a.TaskStateCompleted {
		t.Errorf("a token of the owner's tenant must see the task: %v %v", got, err)
	}
}

func TestSubscribeToAStoredTaskAfterRestart(t *testing.T) {
	cfg := persistentConfig(t)
	first, stop := startAt(t, cfg, &scriptedWorker{})
	sent := sendTask(t, a2aClient(t, "http://"+first.Addr(), tokenA), &a2a.SendMessageRequest{Message: textMessage("done already")})
	stop()
	second, _ := startAt(t, cfg, &scriptedWorker{})
	alice := a2aClient(t, "http://"+second.Addr(), tokenA)
	// The specification refuses to subscribe to a finished task; that answer is for the owner. It is neither
	// "not found" (the task is stored) nor a stream that never ends.
	var got error
	for _, err := range alice.SubscribeToTask(context.Background(), &a2a.SubscribeToTaskRequest{ID: sent.ID}) {
		got = err
		break
	}
	if !errors.Is(got, a2a.ErrUnsupportedOperation) {
		t.Fatalf("the owner subscribing to a finished stored task: %v", got)
	}
	if _, err := alice.GetTask(context.Background(), &a2a.GetTaskRequest{ID: "no-such-task"}); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Fatalf("an unknown task: %v", err)
	}
	for _, err := range alice.SubscribeToTask(context.Background(), &a2a.SubscribeToTaskRequest{ID: "no-such-task"}) {
		if !errors.Is(err, a2a.ErrTaskNotFound) {
			t.Fatalf("subscribing to an unknown task: %v", err)
		}
		break
	}
}

func TestTaskInterruptedByARestartIsReportedFailed(t *testing.T) {
	cfg := persistentConfig(t)
	// What a listener that was killed mid-task leaves behind: a task that is still "working".
	owner := Principal{Name: "alice", Tenant: "team-a"}
	seed, err := openTaskStore(storeOptions{Dir: filepath.Join(cfg.StateDir, "tasks"), Now: time.Now,
		Auth: func(context.Context) (string, error) { return owner.Key(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := seed.Create(ctx, &a2a.Task{ID: "killed", ContextID: "c", Status: a2a.TaskStatus{State: a2a.TaskStateWorking}}); err != nil {
		t.Fatal(err)
	}
	s, _ := startAt(t, cfg, &scriptedWorker{})
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	got, err := c.GetTask(ctx, &a2a.GetTaskRequest{ID: "killed"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status.State != a2a.TaskStateFailed || got.Status.Message == nil || !strings.Contains(got.Status.Message.Parts[0].Text(), "restarted") {
		t.Fatalf("a task nobody is running is not working: %+v", got.Status)
	}
	if s.ActiveTasks() != 0 {
		t.Fatalf("active tasks %d", s.ActiveTasks())
	}
	found := false
	for _, l := range s.RecentLogs() {
		found = found || (strings.Contains(l, "1 task") && strings.Contains(l, "interrupted"))
	}
	if !found {
		t.Fatalf("the operator is told about it: %v", s.RecentLogs())
	}
	// A follow-up to a finished task is refused, and the context itself continues in a new task.
	again := sendTask(t, c, &a2a.SendMessageRequest{Message: func() *a2a.Message { m := textMessage("carry on"); m.ContextID = "c"; return m }()})
	if again.ContextID != "c" || again.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("a new task in the context: %+v", again.Status)
	}
}

func TestShutdownLeavesNoTaskRunningInTheStore(t *testing.T) {
	cfg := persistentConfig(t)
	started := make(chan struct{})
	w := &scriptedWorker{run: func(ctx context.Context, tn Turn, up func(Update)) (Result, error) {
		up(Update{Text: "partial"})
		close(started)
		<-ctx.Done()
		return Result{}, ctx.Err()
	}}
	s, stop := startAt(t, cfg, w)
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	idCh := make(chan a2a.TaskID, 1)
	go func() {
		first := true
		for ev, err := range c.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{Message: textMessage("long")}) {
			if err != nil {
				return
			}
			if first {
				idCh <- ev.TaskInfo().TaskID
				first = false
			}
		}
	}()
	id := <-idCh
	waitChan(t, started, "the worker")
	stop()

	reopened, err := openTaskStore(storeOptions{Dir: filepath.Join(cfg.StateDir, "tasks"), Now: time.Now,
		Auth: func(context.Context) (string, error) { return Principal{Name: "alice", Tenant: "team-a"}.Key(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	st, err := reopened.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Task.Status.State.Terminal() {
		t.Fatalf("after Shutdown the stored task is %s", st.Task.Status.State)
	}
}

func TestStoredTaskIsOwnerOnlyOnDisk(t *testing.T) {
	cfg := persistentConfig(t)
	s, stop := startAt(t, cfg, &scriptedWorker{})
	sendTask(t, a2aClient(t, "http://"+s.Addr(), tokenA), &a2a.SendMessageRequest{Message: textMessage("private")})
	stop()
	files, _ := filepath.Glob(filepath.Join(cfg.StateDir, "tasks", "*"))
	if len(files) == 0 {
		t.Fatal("no task file")
	}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if !strings.Contains(string(b), "private") {
			t.Errorf("the task file holds the conversation: %s", f)
		}
		if runtime.GOOS == "windows" {
			continue
		}
		if fi, _ := os.Stat(f); fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v", f, fi.Mode())
		}
	}
}

// a Server without a stateDir keeps tasks in memory: that is what the other tests rely on.
func TestServerWithoutAStateDirStillServes(t *testing.T) {
	s := startServer(t, serverConfig(), &scriptedWorker{})
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	sent := sendTask(t, c, &a2a.SendMessageRequest{Message: textMessage("ephemeral")})
	if got, err := c.GetTask(context.Background(), &a2a.GetTaskRequest{ID: sent.ID}); err != nil || got.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("%v %v", got, err)
	}
}

var _ taskstore.Store = (*fileStore)(nil)

// Every RPC that names a task, driven as a second principal against a task that is stored (finished, from before
// a restart) and one that is live: the answer is "not found", the worker is not started and the owner's task is
// untouched.
func TestSecondPrincipalIsRefusedOnEveryTaskRPC(t *testing.T) {
	cfg := persistentConfig(t)
	first, stop := startAt(t, cfg, &scriptedWorker{})
	stored := sendTask(t, a2aClient(t, "http://"+first.Addr(), tokenA), &a2a.SendMessageRequest{Message: textMessage("alice's finished task")})
	stop()

	g := newGated()
	s, _ := startAt(t, cfg, g)
	base := "http://" + s.Addr()
	idCh := make(chan a2a.TaskID, 1)
	go func() {
		first := true
		for ev, err := range a2aClient(t, base, tokenA).SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{Message: textMessage("alice's live task")}) {
			if err != nil {
				return
			}
			if first {
				idCh <- ev.TaskInfo().TaskID
				first = false
			}
		}
	}()
	live := <-idCh
	waitChan(t, g.started, "the worker")
	turns := len(g.Turns())

	follow := func(id a2a.TaskID, ctxID string) map[string]any {
		return map[string]any{"message": map[string]any{"messageId": "m-bob", "taskId": string(id), "contextId": ctxID, "role": "ROLE_USER", "parts": []any{map[string]any{"text": "attach to it"}}}}
	}
	for name, id := range map[string]a2a.TaskID{"stored": stored.ID, "live": live} {
		calls := map[string]map[string]any{
			"GetTask":              {"id": string(id)},
			"CancelTask":           {"id": string(id)},
			"SubscribeToTask":      {"id": string(id)},
			"SendMessage":          follow(id, "c"),
			"SendStreamingMessage": follow(id, "c"),
		}
		for method, params := range calls {
			_, out := rawRPC(t, base, tokenB, "1.0", method, params)
			if code, msg := rpcError(t, out); code != -32001 {
				t.Errorf("%s of the %s task as another principal: code %d %q, want task-not-found", method, name, code, msg)
			}
		}
		_, out := rawRPC(t, base, tokenB, "1.0", "ListTasks", map[string]any{})
		if res, _ := out["result"].(map[string]any); res == nil || len(res["tasks"].([]any)) != 0 {
			t.Errorf("another principal lists the %s task: %v", name, out)
		}
	}
	if len(g.Turns()) != turns {
		t.Fatal("a worker ran for another principal's request")
	}
	alice := a2aClient(t, base, tokenA)
	if got, err := alice.GetTask(context.Background(), &a2a.GetTaskRequest{ID: live}); err != nil || got.Status.State != a2a.TaskStateWorking {
		t.Fatalf("the live task was touched: %v %v", got, err)
	}
	if got, err := alice.GetTask(context.Background(), &a2a.GetTaskRequest{ID: stored.ID}); err != nil || got.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("the stored task was touched: %v %v", got, err)
	}
	close(g.gate)
}
