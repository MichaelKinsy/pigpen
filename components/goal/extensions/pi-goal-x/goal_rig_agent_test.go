package pi_goal_x_test

// rigAgent is the host's agent run loop around a goalRig, scripted: what the original's e2e workers get from a real Pi session
// (createAgentSession against a fixture model server) and the fake host cannot give. It delivers the events of a run in the order Pi
// does (agent_start, message_start, per request: turn_start, context, tool calls, message_end, turn_end; agent_end, agent_settled),
// answers each provider request from Model, runs goal tool calls through the registered tools, and starts a run for every
// triggerTurn message the extension sends. Time is the Probe's mock clock.
//
// The event payloads are this simulator's reading of Pi's; the GoalEvents slice adjusts them to what its handlers decode.

import (
	"encoding/json"
	"sync"
	"testing"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

type agentCall struct {
	Name string
	Args map[string]any
}

type rigAgent struct {
	t     *testing.T
	r     *goalRig
	clock *goal.FakeClock
	// Model answers provider request number n (1-based, counting work requests only): the tool calls of its answer; none is a plain
	// text answer that ends the run.
	Model func(n int) []agentCall
	// FailFirst makes the first provider request fail once (Pi's auto retry re-enters the run: another agent_start).
	FailFirst bool

	WorkRequests   int
	FailedAttempts int
	Retries        int
	Compactions    int
	// Contexts are the context handler's results, one per provider request.
	Contexts []json.RawMessage

	mu      sync.Mutex
	pending []map[string]any
	running bool
	history []any
}

func newRigAgent(t *testing.T, r *goalRig, clock *goal.FakeClock) *rigAgent {
	a := &rigAgent{t: t, r: r, clock: clock}
	r.SetSendHook(func(message, options map[string]any) {
		if trigger, _ := options["triggerTurn"].(bool); trigger {
			a.mu.Lock()
			a.pending = append(a.pending, message)
			a.mu.Unlock()
		}
	})
	r.SetIdle(func() bool { a.mu.Lock(); defer a.mu.Unlock(); return !a.running })
	return a
}

func (a *rigAgent) setRunning(v bool) { a.mu.Lock(); a.running = v; a.mu.Unlock() }

// Prompt is session.prompt(text): a user run; Drain then lets every scheduled continuation run.
func (a *rigAgent) Prompt(text string) {
	a.t.Helper()
	a.setRunning(true)
	a.r.beforeAgentStart(text)
	a.history = append(a.history, map[string]any{"role": "user", "content": text})
	a.run(map[string]any{"role": "user", "content": text})
}

// Deliver is a triggerTurn message reaching the agent (what pi.sendMessage does): a run of its own.
func (a *rigAgent) Deliver(message map[string]any) {
	a.t.Helper()
	a.setRunning(true)
	msg := map[string]any{"role": "custom"}
	for k, v := range message {
		msg[k] = v
	}
	a.history = append(a.history, msg)
	a.run(msg)
}

// Drain advances the mock clock and runs the follow-ups the extension scheduled, until none is due.
func (a *rigAgent) Drain() {
	a.t.Helper()
	for range 200 {
		a.clock.Tick(1)
		a.mu.Lock()
		if len(a.pending) == 0 {
			a.mu.Unlock()
			return
		}
		next := a.pending[0]
		a.pending = a.pending[1:]
		a.mu.Unlock()
		a.Deliver(next)
	}
	a.t.Fatal("the extension keeps scheduling runs")
}

// Compact is session.compact(): the host's compaction events around a (scripted) summarization request.
func (a *rigAgent) Compact() {
	a.r.Fire("session_before_compact", map[string]any{})
	a.Compactions++
	a.r.Fire("session_compact", map[string]any{"fromExtension": false})
}

func (a *rigAgent) run(first map[string]any) {
	r := a.r
	aborts := r.aborts()
	r.Fire("agent_start", map[string]any{})
	r.Fire("message_start", map[string]any{"message": first}) // a stale checkpoint aborts here: the run must not reach the provider
	for turn := 0; aborts == r.aborts(); turn++ {
		r.Fire("turn_start", map[string]any{"turnIndex": turn})
		a.Contexts = append(a.Contexts, r.Fire("context", map[string]any{"messages": append([]any{}, a.history...)}))
		if a.FailFirst && a.FailedAttempts == 0 {
			a.FailedAttempts++
			a.Retries++
			r.Fire("agent_start", map[string]any{}) // the retry re-enters the logical run
			continue
		}
		a.WorkRequests++
		var calls []agentCall
		if a.Model != nil {
			calls = a.Model(a.WorkRequests)
		}
		allTerminate := len(calls) > 0
		for i, call := range calls {
			gate := gufToolGate(a.t, r, call.Name, call.Args)
			if blocked, _ := gate["block"].(bool); blocked {
				allTerminate = false
				continue
			}
			terminates := false
			if r.Host.tools[call.Name] {
				raw, failure := r.Tool(call.Name, call.Args)
				if failure == "" {
					terminates = decodeToolResult(a.t, raw).Terminate
				}
			}
			allTerminate = allTerminate && terminates
			r.Fire("tool_execution_end", map[string]any{"toolName": call.Name, "toolCallId": call.Name + string(rune('a'+i))})
		}
		stop := "stop"
		if len(calls) > 0 {
			stop = "toolUse"
		}
		assistant := map[string]any{"role": "assistant", "stopReason": stop, "usage": map[string]any{"input": 0, "output": 0}}
		a.history = append(a.history, assistant)
		r.Fire("message_end", map[string]any{"message": assistant})
		r.Fire("turn_end", map[string]any{"message": assistant})
		if len(calls) == 0 || allTerminate {
			break
		}
	}
	r.Fire("agent_end", map[string]any{"messages": a.history})
	a.setRunning(false)
	r.Fire("agent_settled", map[string]any{})
}

// Requests is every provider request: the work requests, the failed attempt and the compactions.
func (a *rigAgent) Requests() int { return a.WorkRequests + a.FailedAttempts + a.Compactions }
