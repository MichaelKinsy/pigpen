package powerline_footer

import (
	"context"
	"strings"
	"sync"
)

// The auto follow-up input handler. upstream: index.ts 1995-2062 (the input handler and restoreRoutingInputs) and 2159-2180
// (getRecentAgentContext).

// followUpHost is what the handler needs from the host's context for one input event.
type followUpHost interface {
	HasUI() bool
	Notify(message, level string)
	EditorText() (string, error)
	SetEditorText(text string)
	// Branch is ctx.sessionManager.getBranch(); nil when the host cannot give it.
	Branch() []map[string]any
	// Signal is ctx.signal, the cancellation of the run in progress; nil while no run is active.
	Signal() context.Context
	SendUserMessage(content any, deliverAs string) error
}

// inputEvent is the part of Pi's input event the handler reads.
type inputEvent struct {
	Text              string
	Source            string
	StreamingBehavior string
	Images            []any
}

// routingInput is a message still waiting on Jev (index.ts:2004). Messages waiting on Jev have not reached Pi's queue, so Esc
// cannot restore them; the list does it in submission order.
type routingInput struct {
	text     string
	signal   context.Context
	restored bool
	removed  bool          // the handler is past its Jev call: an abort now restores nothing (the listener is removed)
	stop     chan struct{} // closed when the handler is done with the abort watcher
}

type autoFollowUp struct {
	jev    jevClient
	getenv func(string) string

	// mu guards everything below, and is held across the host calls of a restore so that the handler, which takes it to leave,
	// returns "handled" only after the editor has its messages back.
	mu         sync.Mutex
	chain      chan struct{} // closed when the latest queued Jev decision is finished (autoFollowUpChain)
	routing    []*routingInput
	generation uint64 // sessionGeneration
	lastPrompt string // lastUserPrompt
}

func newAutoFollowUp(jev jevClient, getenv func(string) string) *autoFollowUp {
	done := make(chan struct{})
	close(done)
	return &autoFollowUp{jev: jev, getenv: getenv, chain: done}
}

// sessionStart is the session_start handler's part: a new session has no task and a new generation (index.ts:1836, 1845).
func (f *autoFollowUp) sessionStart() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generation++
	f.lastPrompt = ""
}

// sessionShutdown is the session_shutdown handler's part (index.ts:1902).
func (f *autoFollowUp) sessionShutdown() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generation++
}

// beforeAgentStart records the task Jev is told about (index.ts:1995).
func (f *autoFollowUp) beforeAgentStart(prompt string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastPrompt = prompt
}

// routingCount is the number of messages waiting on Jev.
func (f *autoFollowUp) routingCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.routing)
}

// restoreRoutingInputsLocked puts the messages of the aborted run back into the editor, ahead of the draft (index.ts:2006-2011).
func (f *autoFollowUp) restoreRoutingInputsLocked(h followUpHost, signal context.Context) {
	var texts []string
	for _, input := range f.routing {
		if input.signal == signal && !input.restored {
			input.restored = true
			texts = append(texts, input.text)
		}
	}
	if len(texts) == 0 {
		return
	}
	draft, err := h.EditorText()
	if err != nil {
		return // upstream's getEditorText would throw here: the editor is left as it is
	}
	var parts []string
	for _, text := range append(texts, draft) {
		if jsTrim(text) != "" {
			parts = append(parts, text)
		}
	}
	h.SetEditorText(strings.Join(parts, "\n\n"))
}

// handleInput returns nil to let Pi carry on with the input, or {"action":"handled"}. Plain Enter while the agent works arrives
// as steering; it moves to the follow-up queue when Jev is confident it can wait. enabled is config.autoFollowUp.
func (f *autoFollowUp) handleInput(h followUpHost, enabled bool, ev inputEvent) any {
	if !enabled || ev.Source != "interactive" || ev.StreamingBehavior != "steer" {
		return nil
	}
	if strings.HasPrefix(strings.TrimLeftFunc(ev.Text, isJSSpace), "/") {
		return nil
	}
	apiKey := jsTrim(f.getenv("TYPESAFE_API_KEY"))
	if apiKey == "" {
		h.Notify("Auto follow-up needs TYPESAFE_API_KEY; sent as steering", "warning")
		return nil
	}

	f.mu.Lock()
	currentTask, generation := f.lastPrompt, f.generation
	f.mu.Unlock()
	state := followUpRouteState{CurrentTask: currentTask, AgentLatestText: recentAgentContext(h.Branch()), NewMessage: ev.Text}

	// Only the run this message was typed into counts: the agent can also go idle by finishing normally, and that must still send.
	var routing *routingInput
	if h.HasUI() {
		if signal := h.Signal(); signal != nil {
			routing = &routingInput{text: ev.Text, signal: signal, stop: make(chan struct{})}
		}
	}
	if routing != nil {
		f.mu.Lock()
		f.routing = append(f.routing, routing)
		f.mu.Unlock()
		if routing.signal.Err() == nil { // a listener on an aborted signal never fires
			go f.watchAbort(h, generation, routing)
		}
	}

	// Jev routing is serialized, so a quick second message cannot be queued before the first.
	f.mu.Lock()
	previous := f.chain
	mine := make(chan struct{})
	f.chain = mine
	f.mu.Unlock()
	followUp, err := func() (bool, error) {
		defer close(mine)
		<-previous
		return f.jev.canWaitAsFollowUp(state, apiKey)
	}()

	f.mu.Lock()
	restored := false
	if routing != nil {
		routing.removed = true
		restored = routing.restored
		for i, input := range f.routing {
			if input == routing {
				f.routing = append(f.routing[:i], f.routing[i+1:]...)
				break
			}
		}
		close(routing.stop)
	}
	current := f.generation
	f.mu.Unlock()

	// A session switch during the Jev call must not route this message into the new session.
	if generation != current {
		return nil
	}
	if restored {
		return map[string]any{"action": "handled"}
	}
	if err != nil {
		h.Notify("Auto follow-up unavailable ("+err.Error()+"); sent as steering", "warning")
		return nil
	}
	if !followUp {
		return nil
	}

	var content any = ev.Text
	if len(ev.Images) > 0 {
		content = append([]any{map[string]any{"type": "text", "text": ev.Text}}, ev.Images...)
	}
	if err := h.SendUserMessage(content, "followUp"); err != nil {
		h.Notify("Auto follow-up unavailable ("+err.Error()+"); sent as steering", "warning")
		return nil
	}
	return map[string]any{"action": "handled"}
}

// watchAbort is the `abort` listener of index.ts:2028-2031: when the run aborts while the message waits on Jev, and the session
// is the same one, the messages of that run return to the editor.
func (f *autoFollowUp) watchAbort(h followUpHost, generation uint64, routing *routingInput) {
	select {
	case <-routing.signal.Done():
	case <-routing.stop:
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if routing.removed || generation != f.generation {
		return
	}
	f.restoreRoutingInputsLocked(h, routing.signal)
}

// recentAgentContext is getRecentAgentContext: the first 200 UTF-16 units of the latest assistant text block, "" when none.
func recentAgentContext(branch []map[string]any) string {
	for i := len(branch) - 1; i >= 0; i-- {
		entry := branch[i]
		if entry["type"] != "message" {
			continue
		}
		message := asMap(entry["message"])
		if message["role"] != "assistant" {
			continue
		}
		content, ok := message["content"].([]any)
		if !ok {
			continue
		}
		for _, item := range content {
			block := asMap(item)
			if block["type"] != "text" {
				continue
			}
			text, _ := block["text"].(string)
			if text == "" {
				continue
			}
			if text = jsTrim(text); text != "" {
				return jsSliceUnits(text, 200)
			}
		}
	}
	return ""
}
