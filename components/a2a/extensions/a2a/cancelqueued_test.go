package a2aext

// A cancel that arrives before the task's worker has started: the task is queued behind the concurrency limit,
// or behind another task of the same context. It must end canceled without ever running a worker, and must not
// disturb the task ahead of it.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
)

// streamTask starts a streaming task and returns its id once the server has created it, and a channel
// carrying the last state seen when the stream ends.
func streamTask(t *testing.T, c *a2aclient.Client, m *a2a.Message) (a2a.TaskID, <-chan a2a.TaskState) {
	t.Helper()
	ids := make(chan a2a.TaskID, 1)
	last := make(chan a2a.TaskState, 1)
	go func() {
		var state a2a.TaskState
		sent := false
		for ev, err := range c.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{Message: m}) {
			if err != nil {
				break
			}
			if !sent {
				ids <- ev.TaskInfo().TaskID
				sent = true
			}
			switch e := ev.(type) {
			case *a2a.Task:
				state = e.Status.State
			case *a2a.TaskStatusUpdateEvent:
				state = e.Status.State
			}
		}
		last <- state
	}()
	select {
	case id := <-ids:
		return id, last
	case <-time.After(10 * time.Second):
		t.Fatal("the server never created the task")
		return "", nil
	}
}

func testCancelWhileQueued(t *testing.T, sameContext bool) {
	gate := make(chan struct{})
	started := make(chan struct{}, 4)
	var mu sync.Mutex
	var prompts []string
	w := &scriptedWorker{run: func(ctx context.Context, tn Turn, up func(Update)) (Result, error) {
		mu.Lock()
		prompts = append(prompts, tn.Prompt)
		mu.Unlock()
		started <- struct{}{}
		select {
		case <-gate:
			return Result{Text: "done " + tn.Prompt}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}}
	cfg := serverConfig()
	cfg.MaxConcurrentTasks = 1
	if sameContext {
		cfg.MaxConcurrentTasks = 2 // a free slot: the second task waits for the context, not for a slot
	}
	s := startServer(t, cfg, w)
	base := "http://" + s.Addr()
	ca := a2aClient(t, base, tokenA)

	first := textMessage("first")
	first.ContextID = "shared-context"
	firstID, firstLast := streamTask(t, ca, first)
	waitChan(t, chanOf(started), "the first worker")

	second := textMessage("second")
	if sameContext {
		second.ContextID = "shared-context"
	}
	secondID, secondLast := streamTask(t, ca, second)
	time.Sleep(300 * time.Millisecond) // the second task is queued: the worker has not been asked to run it

	mu.Lock()
	if len(prompts) != 1 {
		mu.Unlock()
		t.Fatalf("the queued task started a worker: %v", prompts)
	}
	mu.Unlock()

	got, err := ca.CancelTask(context.Background(), &a2a.CancelTaskRequest{ID: secondID})
	if err != nil {
		t.Fatalf("cancelling a queued task: %v", err)
	}
	if got.Status.State != a2a.TaskStateCanceled {
		t.Fatalf("queued task ended %s", got.Status.State)
	}
	select {
	case st := <-secondLast:
		if st != a2a.TaskStateCanceled {
			t.Fatalf("the queued task's stream ended in %s", st)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the queued task's stream never ended")
	}

	// The first task is untouched and finishes normally; the cancelled one never reached a worker.
	close(gate)
	select {
	case st := <-firstLast:
		if st != a2a.TaskStateCompleted {
			t.Fatalf("the first task ended %s", st)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the first task never finished")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 1 || prompts[0] != "first" {
		t.Fatalf("workers that ran: %v (task %s, cancelled %s)", prompts, firstID, secondID)
	}
}

func chanOf(c chan struct{}) <-chan struct{} {
	out := make(chan struct{})
	go func() { <-c; close(out) }()
	return out
}

func TestCancelBeforeTheWorkerStartsBehindTheConcurrencyLimit(t *testing.T) {
	testCancelWhileQueued(t, false)
}

func TestCancelBeforeTheWorkerStartsBehindTheSameContext(t *testing.T) {
	testCancelWhileQueued(t, true)
}
