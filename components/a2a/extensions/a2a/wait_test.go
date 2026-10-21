package a2aext

// Following a remote task that outlives a2a_send (roadmap gap "client resubscribe/long-poll tool"):
// Remotes.Wait subscribes to the task's stream (SubscribeToTask) and streams progress, falls back to polling
// where the remote does not stream, and never cancels the remote task when the wait ends. SendArgs.Detach
// starts a task and returns without waiting for it.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

// gated is a worker that streams "part one ", waits for the gate, then streams "part two".
type gated struct {
	*scriptedWorker
	gate    chan struct{}
	started chan struct{}
	once    sync.Once
	ctxErr  atomic.Value
}

func newGated() *gated {
	g := &gated{gate: make(chan struct{}), started: make(chan struct{})}
	g.scriptedWorker = &scriptedWorker{run: func(ctx context.Context, tn Turn, up func(Update)) (Result, error) {
		up(Update{Text: "part one "})
		g.once.Do(func() { close(g.started) })
		select {
		case <-g.gate:
		case <-ctx.Done():
			g.ctxErr.Store(ctx.Err())
			return Result{}, ctx.Err()
		}
		up(Update{Text: "part two"})
		return Result{Text: "part one part two"}, nil
	}}
	return g
}

// peerWithGatedTask starts this package's server with a gated worker, a task on it, and the Remotes that call it.
func peerWithGatedTask(t *testing.T) (*gated, *Remotes, a2a.TaskID) {
	t.Helper()
	g := newGated()
	s := startServer(t, serverConfig(), g)
	base := "http://" + s.Addr()
	idCh := make(chan a2a.TaskID, 1)
	go func() {
		first := true
		for ev, err := range a2aClient(t, base, tokenA).SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{Message: textMessage("long job")}) {
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
	waitChan(t, g.started, "the worker")
	rs := NewRemotes(map[string]RemoteAgent{"peer": {URL: base, BearerTokenEnv: "TOKEN_A", TimeoutSeconds: 20}}, serverEnv)
	return g, rs, id
}

func TestWaitFollowsALiveTaskToItsEndAndStreamsProgress(t *testing.T) {
	g, rs, id := peerWithGatedTask(t)
	var mu sync.Mutex
	var progress strings.Builder
	got := make(chan string, 1)
	go func() {
		sum, err := rs.Wait(context.Background(), WaitArgs{Agent: "peer", TaskID: string(id)}, func(s string) { mu.Lock(); progress.WriteString(s); mu.Unlock() })
		if err != nil {
			got <- "error: " + err.Error()
			return
		}
		got <- sum.State + "|" + sum.Text
	}()
	time.Sleep(100 * time.Millisecond)
	close(g.gate)
	select {
	case r := <-got:
		if r != "completed|part one part two" {
			t.Fatalf("Wait returned %q", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Wait did not return after the task finished")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(progress.String(), "part two") {
		t.Fatalf("the text that streamed while waiting reaches the caller: %q", progress.String())
	}
}

func TestWaitRunningOutOfTimeLeavesTheRemoteTaskRunning(t *testing.T) {
	g, rs, id := peerWithGatedTask(t)
	start := time.Now()
	sum, err := rs.Wait(context.Background(), WaitArgs{Agent: "peer", TaskID: string(id), Seconds: 1}, nil)
	if err != nil {
		t.Fatalf("running out of time is an answer, not an error: %v", err)
	}
	if sum.Terminal || sum.State != "working" || sum.TaskID != string(id) {
		t.Fatalf("summary %+v", sum)
	}
	if d := time.Since(start); d < 900*time.Millisecond || d > 5*time.Second {
		t.Fatalf("waited %v for a 1 second budget", d)
	}
	if !strings.Contains(sum.Text, "part one") {
		t.Fatalf("what the task has said so far is returned: %q", sum.Text)
	}
	if e := g.ctxErr.Load(); e != nil {
		t.Fatalf("the remote task was cancelled: %v", e)
	}
	if got, err := rs.GetTask(context.Background(), "peer", string(id)); err != nil || got.State != "working" {
		t.Fatalf("after the wait the task is %+v %v", got, err)
	}
	close(g.gate)
	sum, err = rs.Wait(context.Background(), WaitArgs{Agent: "peer", TaskID: string(id), Seconds: 10}, nil)
	if err != nil || sum.State != "completed" || !strings.HasSuffix(sum.Text, "part two") {
		t.Fatalf("a second wait finishes it: %+v %v", sum, err)
	}
}

func TestWaitCancelledByTheCallerDoesNotCancelTheRemoteTask(t *testing.T) {
	g, rs, id := peerWithGatedTask(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := rs.Wait(ctx, WaitArgs{Agent: "peer", TaskID: string(id)}, nil)
		done <- err
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait ignored its context")
	}
	time.Sleep(200 * time.Millisecond)
	if e := g.ctxErr.Load(); e != nil {
		t.Fatalf("giving up on the wait cancelled the remote task: %v", e)
	}
	if got, _ := rs.GetTask(context.Background(), "peer", string(id)); got.Terminal {
		t.Fatalf("the task ended: %+v", got)
	}
}

func TestWaitOnAFinishedTaskReturnsItWithoutAnError(t *testing.T) {
	s := startServer(t, serverConfig(), &scriptedWorker{})
	base := "http://" + s.Addr()
	sent := sendTask(t, a2aClient(t, base, tokenA), &a2a.SendMessageRequest{Message: textMessage("quick")})
	rs := NewRemotes(map[string]RemoteAgent{"peer": {URL: base, BearerTokenEnv: "TOKEN_A"}}, serverEnv)
	sum, err := rs.Wait(context.Background(), WaitArgs{Agent: "peer", TaskID: string(sent.ID)}, nil)
	if err != nil || !sum.Terminal || sum.State != "completed" || sum.Text != "echo: quick" {
		t.Fatalf("%+v %v", sum, err)
	}
}

func TestWaitOnAnUnknownTaskIsAnError(t *testing.T) {
	s := startServer(t, serverConfig(), &scriptedWorker{})
	rs := NewRemotes(map[string]RemoteAgent{"peer": {URL: "http://" + s.Addr(), BearerTokenEnv: "TOKEN_A"}}, serverEnv)
	if _, err := rs.Wait(context.Background(), WaitArgs{Agent: "peer", TaskID: "nope"}, nil); err == nil || !strings.Contains(err.Error(), "peer") {
		t.Fatalf("%v", err)
	}
	if _, err := rs.Wait(context.Background(), WaitArgs{Agent: "nobody", TaskID: "x"}, nil); err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("%v", err)
	}
	if _, err := rs.Wait(context.Background(), WaitArgs{Agent: "peer"}, nil); err == nil {
		t.Fatal("a wait needs a task id")
	}
}

func TestWaitPollsARemoteThatDoesNotStream(t *testing.T) {
	old := waitPollInterval
	waitPollInterval = 20 * time.Millisecond
	defer func() { waitPollInterval = old }()
	gate := make(chan struct{})
	r := newFakeRemote(t)
	r.noStream = true
	r.execute = func(ctx context.Context, ec *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool) {
		if ec.StoredTask == nil && !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
			return
		}
		if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
			return
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return
		}
		if !yield(a2a.NewArtifactEvent(ec, a2a.NewTextPart("polled result")), nil) {
			return
		}
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
	}
	rs := remotes(t, r, nil, nil)
	sent, err := rs.Send(context.Background(), SendArgs{Agent: "peer", Message: "start", Detach: true}, nil)
	if err != nil || sent.TaskID == "" || sent.Terminal {
		t.Fatalf("a detached send returns the running task: %+v %v", sent, err)
	}
	go func() { time.Sleep(150 * time.Millisecond); close(gate) }()
	sum, err := rs.Wait(context.Background(), WaitArgs{Agent: "peer", TaskID: sent.TaskID, Seconds: 10}, nil)
	if err != nil || sum.State != "completed" || sum.Text != "polled result" {
		t.Fatalf("%+v %v", sum, err)
	}
}

func TestSendDetachedReturnsBeforeTheTaskFinishes(t *testing.T) {
	g := newGated()
	s := startServer(t, serverConfig(), g)
	rs := NewRemotes(map[string]RemoteAgent{"peer": {URL: "http://" + s.Addr(), BearerTokenEnv: "TOKEN_A", TimeoutSeconds: 20}}, serverEnv)
	start := time.Now()
	sum, err := rs.Send(context.Background(), SendArgs{Agent: "peer", Message: "background job", Detach: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.TaskID == "" || sum.ContextID == "" || sum.Terminal {
		t.Fatalf("summary %+v", sum)
	}
	if sum.State != "submitted" && sum.State != "working" {
		t.Fatalf("state %q", sum.State)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("a detached send waited for the task")
	}
	waitChan(t, g.started, "the worker")
	time.Sleep(100 * time.Millisecond)
	if e := g.ctxErr.Load(); e != nil {
		t.Fatalf("returning from a detached send cancelled the remote task: %v", e)
	}
	close(g.gate)
	done, err := rs.Wait(context.Background(), WaitArgs{Agent: "peer", TaskID: sum.TaskID, Seconds: 10}, nil)
	if err != nil || done.State != "completed" || done.ContextID != sum.ContextID {
		t.Fatalf("%+v %v", done, err)
	}
}

func TestSendWithoutDetachStillWaitsForTheEnd(t *testing.T) {
	s := startServer(t, serverConfig(), &scriptedWorker{})
	rs := NewRemotes(map[string]RemoteAgent{"peer": {URL: "http://" + s.Addr(), BearerTokenEnv: "TOKEN_A"}}, serverEnv)
	sum, err := rs.Send(context.Background(), SendArgs{Agent: "peer", Message: "plain"}, nil)
	if err != nil || sum.State != "completed" || sum.Text != "echo: plain" {
		t.Fatalf("%+v %v", sum, err)
	}
}
