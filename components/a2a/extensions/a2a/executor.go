package a2aext

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

const maxPromptBytes = 256 << 10

type stopReason int32

const (
	stopNone stopReason = iota
	stopCanceled
	stopTimeout
	stopShutdown
)

// running is one task between Execute's registration and its worker's exit.
type running struct {
	cancel context.CancelFunc
	reason atomic.Int32
	done   chan struct{}
}

// executor is the a2asrv.AgentExecutor that runs A2A tasks as PiG turns.
type executor struct {
	worker  Worker
	timeout time.Duration
	base    context.Context
	sem     chan struct{}
	logf    func(format string, args ...any)

	mu       sync.Mutex
	tasks    map[a2a.TaskID]*running
	ctxLocks map[string]*ctxLock
	active   atomic.Int32
}

type ctxLock struct {
	ch   chan struct{}
	refs int
}

func newExecutor(base context.Context, w Worker, maxConcurrent int, timeout time.Duration, logf func(string, ...any)) *executor {
	return &executor{worker: w, timeout: timeout, base: base, sem: make(chan struct{}, maxConcurrent), logf: logf,
		tasks: map[a2a.TaskID]*running{}, ctxLocks: map[string]*ctxLock{}}
}

func principalOf(ec *a2asrv.ExecutorContext) (Principal, error) {
	if ec.User == nil || !ec.User.Authenticated {
		return Principal{}, a2a.ErrUnauthenticated
	}
	name, _ := ec.User.Attributes["name"].(string)
	tenant, _ := ec.User.Attributes["tenant"].(string)
	p := Principal{Name: name, Tenant: tenant}
	if p.Key() != ec.User.Name {
		return Principal{}, a2a.ErrUnauthenticated
	}
	return p, nil
}

// promptOf accepts text parts only. A part it cannot represent is refused, never dropped.
func promptOf(m *a2a.Message) (string, error) {
	if m == nil {
		return "", fmt.Errorf("message is required: %w", a2a.ErrInvalidParams)
	}
	var texts []string
	for _, p := range m.Parts {
		if p == nil {
			continue
		}
		if _, ok := p.Content.(a2a.Text); !ok {
			return "", fmt.Errorf("only text parts are supported: %w", a2a.ErrUnsupportedContentType)
		}
		texts = append(texts, p.Text())
	}
	prompt := strings.Join(texts, "\n")
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("the message has no text: %w", a2a.ErrInvalidParams)
	}
	if len(prompt) > maxPromptBytes {
		return "", fmt.Errorf("the message exceeds %d bytes: %w", maxPromptBytes, a2a.ErrInvalidParams)
	}
	return prompt, nil
}

func (e *executor) lockContext(ctx context.Context, key string) (func(), error) {
	e.mu.Lock()
	l := e.ctxLocks[key]
	if l == nil {
		l = &ctxLock{ch: make(chan struct{}, 1)}
		e.ctxLocks[key] = l
	}
	l.refs++
	e.mu.Unlock()
	release := func() {
		e.mu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(e.ctxLocks, key)
		}
		e.mu.Unlock()
	}
	select {
	case l.ch <- struct{}{}:
		return func() { <-l.ch; release() }, nil
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	}
}

func (e *executor) status(ec *a2asrv.ExecutorContext, state a2a.TaskState, text string) *a2a.TaskStatusUpdateEvent {
	var msg *a2a.Message
	if text != "" {
		msg = a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart(text))
	}
	return a2a.NewStatusUpdateEvent(ec, state, msg)
}

type runEvent struct {
	update Update
	done   bool
	result Result
	err    error
}

// Execute implements a2asrv.AgentExecutor.
func (e *executor) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		principal, err := principalOf(ec)
		if err != nil {
			yield(nil, err)
			return
		}
		prompt, err := promptOf(ec.Message)
		if err != nil {
			yield(nil, err)
			return
		}
		if ec.StoredTask == nil && !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
			return
		}

		runCtx, cancelRun := context.WithCancel(e.base)
		defer cancelRun()
		stop := context.AfterFunc(ctx, cancelRun)
		defer stop()
		r := &running{cancel: cancelRun, done: make(chan struct{})}
		e.mu.Lock()
		e.tasks[ec.TaskID] = r
		e.mu.Unlock()
		e.active.Add(1)
		defer func() {
			e.mu.Lock()
			if e.tasks[ec.TaskID] == r {
				delete(e.tasks, ec.TaskID)
			}
			e.mu.Unlock()
			e.active.Add(-1)
			close(r.done)
		}()

		// Wait for a slot, then for the context's session file. The task stays submitted meanwhile.
		select {
		case e.sem <- struct{}{}:
			defer func() { <-e.sem }()
		case <-runCtx.Done():
			e.finishStopped(ec, r, yield, runCtx)
			return
		}
		unlock, err := e.lockContext(runCtx, principal.Key()+"\x00"+ec.ContextID)
		if err != nil {
			e.finishStopped(ec, r, yield, runCtx)
			return
		}
		defer unlock()
		if runCtx.Err() != nil {
			e.finishStopped(ec, r, yield, runCtx)
			return
		}
		if !yield(e.status(ec, a2a.TaskStateWorking, ""), nil) {
			return
		}

		timeoutTimer := time.AfterFunc(e.timeout, func() {
			r.reason.CompareAndSwap(int32(stopNone), int32(stopTimeout))
			cancelRun()
		})
		defer timeoutTimer.Stop()

		events := make(chan runEvent, 128)
		go func() {
			res, err := e.worker.Run(runCtx, Turn{Principal: principal, ContextID: ec.ContextID, TaskID: string(ec.TaskID), Prompt: prompt},
				func(u Update) {
					select {
					case events <- runEvent{update: u}:
					case <-runCtx.Done():
					}
				})
			events <- runEvent{done: true, result: res, err: err}
		}()

		var artifactID a2a.ArtifactID
		for ev := range events {
			if !ev.done {
				switch {
				case ev.update.Text != "":
					var out *a2a.TaskArtifactUpdateEvent
					if artifactID == "" {
						out = a2a.NewArtifactEvent(ec, a2a.NewTextPart(ev.update.Text))
						artifactID = out.Artifact.ID
					} else {
						out = a2a.NewArtifactUpdateEvent(ec, artifactID, a2a.NewTextPart(ev.update.Text))
					}
					if !yield(out, nil) {
						cancelRun()
						drain(events)
						return
					}
				case ev.update.Tool != "":
					if !yield(e.status(ec, a2a.TaskStateWorking, "using tool "+ev.update.Tool), nil) {
						cancelRun()
						drain(events)
						return
					}
				}
				continue
			}
			switch {
			case ev.err != nil && runCtx.Err() != nil:
				e.finishStopped(ec, r, yield, runCtx)
			case ev.err != nil:
				e.logf("task %s failed: %v", ec.TaskID, ev.err)
				yield(e.status(ec, a2a.TaskStateFailed, "PiG could not complete the task."), nil)
			case ev.result.Failure != "":
				yield(e.status(ec, a2a.TaskStateFailed, "PiG could not complete the task: "+ev.result.Failure), nil)
			default:
				yield(e.status(ec, a2a.TaskStateCompleted, ""), nil)
			}
			return
		}
	}
}

func drain(ch <-chan runEvent) {
	go func() {
		for range ch {
		}
	}()
}

// finishStopped reports a task that was stopped before or during its run.
func (e *executor) finishStopped(ec *a2asrv.ExecutorContext, r *running, yield func(a2a.Event, error) bool, runCtx context.Context) {
	switch stopReason(r.reason.Load()) {
	case stopTimeout:
		yield(e.status(ec, a2a.TaskStateFailed, fmt.Sprintf("The task exceeded its %s time limit.", e.timeout)), nil)
	case stopCanceled:
		// Cancel emits the canceled status itself; a second terminal event would race it.
	default:
		yield(e.status(ec, a2a.TaskStateCanceled, ""), nil)
	}
}

// Cancel implements a2asrv.AgentExecutor: stop the worker, wait for it, then report canceled.
func (e *executor) Cancel(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		e.mu.Lock()
		r := e.tasks[ec.TaskID]
		e.mu.Unlock()
		if r != nil {
			r.reason.CompareAndSwap(int32(stopNone), int32(stopCanceled))
			r.cancel()
			select {
			case <-r.done:
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			case <-time.After(30 * time.Second):
				yield(nil, errors.New("the worker did not stop"))
				return
			}
		}
		yield(e.status(ec, a2a.TaskStateCanceled, ""), nil)
	}
}

// shutdown stops every running task and waits for them.
func (e *executor) shutdown(ctx context.Context) error {
	e.mu.Lock()
	all := make([]*running, 0, len(e.tasks))
	for _, r := range e.tasks {
		all = append(all, r)
	}
	e.mu.Unlock()
	for _, r := range all {
		r.reason.CompareAndSwap(int32(stopNone), int32(stopShutdown))
		r.cancel()
	}
	for _, r := range all {
		select {
		case <-r.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
