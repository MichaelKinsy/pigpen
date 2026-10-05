package a2aext

// Stream resubscription while the task is live (roadmap: "stream resubscription ... in both directions").
// After a restart nothing can be resubscribed: that is TestGap_SubscribeToTaskAfterRestart.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func TestSubscribeToALiveTaskDeliversTheRest(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{})
	w := &scriptedWorker{run: func(ctx context.Context, tn Turn, up func(Update)) (Result, error) {
		up(Update{Text: "part one "})
		close(started)
		select {
		case <-gate:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
		up(Update{Text: "part two"})
		return Result{Text: "part one part two"}, nil
	}}
	s := startServer(t, serverConfig(), w)
	base := "http://" + s.Addr()
	first := a2aClient(t, base, tokenA)

	taskID := make(chan a2a.TaskID, 1)
	go func() {
		sent := false
		for ev, err := range first.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{Message: textMessage("long")}) {
			if err != nil {
				return
			}
			if !sent {
				taskID <- ev.TaskInfo().TaskID
				sent = true
			}
		}
	}()
	id := <-taskID
	waitChan(t, started, "the worker")

	// Another caller is not the owner: the task does not exist for them. Their subscription must end with
	// an error and never deliver an event, not even after the task moves on (a2a-go v2.6.0 alone would attach them).
	bob := a2aClient(t, base, tokenB)
	var bobEvents atomic.Int32
	var bobErr atomic.Value
	bobDone := make(chan struct{})
	bobCtx, stopBob := context.WithTimeout(context.Background(), 15*time.Second)
	defer stopBob()
	go func() {
		defer close(bobDone)
		for _, err := range bob.SubscribeToTask(bobCtx, &a2a.SubscribeToTaskRequest{ID: id}) {
			if err != nil {
				bobErr.Store(err)
				return
			}
			bobEvents.Add(1)
		}
	}()
	waitChan(t, bobDone, "the refused subscription to end")
	if bobEvents.Load() != 0 || bobErr.Load() == nil || !errors.Is(bobErr.Load().(error), a2a.ErrTaskNotFound) {
		t.Fatalf("a caller outside the task's tenant subscribed to it: %d events, err %v", bobEvents.Load(), bobErr.Load())
	}

	// The owner, on a fresh connection, gets the remaining events and the terminal state.
	again := a2aClient(t, base, tokenA)
	var got strings.Builder
	var final a2a.TaskState
	done := make(chan struct{})
	subscribed := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		for ev, err := range again.SubscribeToTask(context.Background(), &a2a.SubscribeToTaskRequest{ID: id}) {
			if err != nil {
				t.Logf("subscribe error: %v", err)
				return
			}
			switch e := ev.(type) {
			case *a2a.Task: // the snapshot the subscription starts with
				for _, art := range e.Artifacts {
					for _, p := range art.Parts {
						got.WriteString(p.Text())
					}
				}
				final = e.Status.State
			case *a2a.TaskArtifactUpdateEvent:
				for _, p := range e.Artifact.Parts {
					got.WriteString(p.Text())
				}
			case *a2a.TaskStatusUpdateEvent:
				final = e.Status.State
			}
			once.Do(func() { close(subscribed) }) // the snapshot proves the subscription is live
		}
	}()
	waitChan(t, subscribed, "the subscription's first event")
	close(gate)
	waitChan(t, done, "the subscription to end")
	if final != a2a.TaskStateCompleted || !strings.Contains(got.String(), "part two") {
		t.Fatalf("final %s, text %q", final, got.String())
	}
}
