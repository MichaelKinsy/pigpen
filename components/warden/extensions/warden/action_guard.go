package warden

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Conversation is what the session says about a call: the latest user prompt, recent messages for scope, the
// task spine, the sibling calls of the same assistant message and the agent's own words before the call.
type Conversation struct {
	// Task is the latest user prompt. Approval is judged from it, never from Context.
	Task    string
	Context []TaskMessage
	Spine   *TaskSpine
	// Siblings are the tool calls of the same assistant message, this one included; they are judged together.
	Siblings []ToolCallRef
	// Plan is the agent's own words in that message. It explains, never authorizes.
	Plan string
}

// InspectOptions configure ActionGuard.Inspect.
type InspectOptions struct {
	Config ActionConfig
	Cwd    string
	// Judge is nil for offline pattern checks only (no consent, no network).
	Judge   Judge
	Timeout time.Duration
	Git     GitRunner
}

type prejudged struct {
	key string
	// started closes when the judge request has been handed to the judge (or the call needed none).
	started chan struct{}
	done    chan struct{}
	verdict Verdict
	used    bool
}

// ActionGuard is the action guard for one session. EvaluateAction judges a single call; this owns what
// spans calls:
//
//   - Hold and approval. After a hold, the next guarded call that runs under a new user prompt asks the judge
//     whether that prompt approves it. The retry rarely repeats the held string byte for byte, so approval is
//     judged against the action itself. Without a judge, a reply that reads as approval stands in.
//   - Sibling prejudging. Calls of one assistant message are judged as soon as the first of them is
//     inspected, so their requests go out together. A judgment is used once and only for the input it was made
//     for; prejudgments do not outlive their turn.
//
// It is safe for concurrent use.
type ActionGuard struct {
	mu             sync.Mutex
	inflight       sync.WaitGroup
	prejudged      map[string]*prejudged
	lastHoldPrompt string
	holdPending    bool
}

// NewActionGuard returns an empty guard.
func NewActionGuard() *ActionGuard { return &ActionGuard{prejudged: map[string]*prejudged{}} }

// startNotifier tells when a request reaches the judge, so Inspect can return with every sibling request in
// flight, as the calls of one assistant message go out together.
type startNotifier struct {
	Judge
	once    sync.Once
	started chan struct{}
}

func (s *startNotifier) Evaluate(ctx context.Context, r Request) (Evaluation, error) {
	s.once.Do(func() { close(s.started) })
	return s.Judge.Evaluate(ctx, r)
}

func inputKey(input map[string]any) string {
	b, _ := json.Marshal(input)
	return string(b)
}

func (p *prejudged) wait(ctx context.Context) (Verdict, bool) {
	select {
	case <-p.done:
		return p.verdict, true
	case <-ctx.Done():
		return Verdict{}, false
	}
}

// Inspect judges one call. The verdict's ApprovedByUser means a pending hold was released by the user's reply.
func (g *ActionGuard) Inspect(ctx context.Context, call ToolCallRef, conv Conversation, opts InspectOptions) Verdict {
	g.mu.Lock()
	// A hold happened under an earlier prompt and the user has since replied: ask whether the reply approves it.
	retryAfterHold := g.holdPending && g.lastHoldPrompt != conv.Task
	judgeCall := func(tool string, input map[string]any) Verdict {
		return EvaluateAction(ctx, ActionInput{Tool: tool, Input: input, Cwd: opts.Cwd, Task: conv.Task, Context: conv.Context, Plan: conv.Plan, Spine: conv.Spine},
			EvaluateOptions{Config: opts.Config, Judge: opts.Judge, RetryAfterHold: retryAfterHold, Timeout: opts.Timeout, Git: opts.Git})
	}
	var started []*prejudged
	// A retry after a hold stays sequential: an approval consumed by one sibling changes the question for the next.
	if opts.Judge != nil && !retryAfterHold {
		for _, sib := range conv.Siblings {
			if sib.ID == call.ID || g.prejudged[sib.ID] != nil || !toolGuarded(opts.Config, sib.Tool) {
				continue
			}
			p := &prejudged{key: inputKey(sib.Input), started: make(chan struct{}), done: make(chan struct{})}
			g.prejudged[sib.ID] = p
			bg := context.WithoutCancel(ctx)
			notifier := &startNotifier{Judge: opts.Judge, started: p.started}
			started = append(started, p)
			g.inflight.Add(1)
			go func(sib ToolCallRef) {
				defer g.inflight.Done()
				defer close(p.done)
				p.verdict = EvaluateAction(bg, ActionInput{Tool: sib.Tool, Input: sib.Input, Cwd: opts.Cwd, Task: conv.Task, Context: conv.Context, Plan: conv.Plan, Spine: conv.Spine},
					EvaluateOptions{Config: opts.Config, Judge: notifier, Timeout: opts.Timeout, Git: opts.Git})
			}(sib)
		}
	}
	for _, p := range started {
		select {
		case <-p.started:
		case <-p.done:
		case <-ctx.Done():
		}
	}
	key := inputKey(call.Input)
	ready := g.prejudged[call.ID]
	var verdict Verdict
	if ready != nil && !ready.used && ready.key == key && !retryAfterHold {
		ready.used = true
		g.mu.Unlock()
		v, ok := ready.wait(ctx)
		if !ok {
			v = judgeCall(call.Tool, call.Input)
		}
		verdict = v
	} else {
		g.prejudged[call.ID] = &prejudged{key: key, used: true, done: closedChan()}
		g.mu.Unlock()
		verdict = judgeCall(call.Tool, call.Input)
	}
	if retryAfterHold && opts.Judge == nil && verdict.Level == LevelConfirm && TextApproves(conv.Task) {
		verdict.Level = LevelAllow
		verdict.ApprovedByUser = true
		verdict.Reasons = append([]string{"user approved in the latest message"}, verdict.Reasons...)
	}
	if verdict.ApprovedByUser {
		g.mu.Lock()
		g.holdPending = false
		g.mu.Unlock()
	}
	return verdict
}

func closedChan() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

func toolGuarded(c ActionConfig, tool string) bool {
	for _, t := range c.Tools {
		if t == tool {
			return true
		}
	}
	return false
}

// Hold records that the call inspected under task was held: the next inspection under a different prompt asks
// whether that prompt approves it.
func (g *ActionGuard) Hold(task string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.holdPending {
		g.lastHoldPrompt = task
	}
	g.holdPending = true
}

// Wait blocks until every sibling request in flight has finished (a prejudgment nobody used is not
// cancelled: it ends by its own timeout).
func (g *ActionGuard) Wait() { g.inflight.Wait() }

// TurnEnd drops siblings that were never inspected (an earlier one terminated the batch, or the user pressed
// Esc): they do not outlive their turn.
func (g *ActionGuard) TurnEnd() {
	g.mu.Lock()
	g.prejudged = map[string]*prejudged{}
	g.mu.Unlock()
}

// Reset forgets everything: a new session.
func (g *ActionGuard) Reset() {
	g.mu.Lock()
	g.prejudged = map[string]*prejudged{}
	g.lastHoldPrompt = ""
	g.holdPending = false
	g.mu.Unlock()
}
