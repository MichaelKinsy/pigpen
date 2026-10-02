package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
)

// ChatDriverOptions configure a [ChatDriver].
type ChatDriverOptions struct {
	Host             *host.Host
	ChatChannel      string
	Backend          Backend
	WorkingDirectory string
	// RecordTurnAnchor is told which turn finished, so the session can remember the entry the turn
	// ends on (truncation needs it).
	RecordTurnAnchor func(turnID string)
	// AdoptForeignTurns makes a turn that no client started (someone typed a prompt into the
	// session locally) appear in the chat: it is opened from the user message that starts it.
	// Upstream's host owns its agent, so it has no such turns; the extension does not own PiG's.
	AdoptForeignTurns bool
	Log               func(format string, args ...any)
}

// ChatDriver connects one chat channel to one [Backend] (port of src/pi/chat-driver.ts).
//
// A client's chat/turnStarted reaches the backend as a prompt; the backend's events stream back
// through a [mapper.TurnMapper] as chat actions. The driver arbitrates everything in between:
// cancellation (which must settle before anything new is sent), steering, queued messages that
// start as their own turn once the chat goes idle, and quiescing for disposal.
//
// Locking: mu guards fields and is never held across a call into the host or the backend (the
// host delivers listener callbacks synchronously, and those can re-enter the driver). evMu keeps
// backend events, which arrive on the backend's goroutine, ordered.
type ChatDriver struct {
	opts ChatDriverOptions

	evMu sync.Mutex // serialises agent events

	mu                 sync.Mutex
	mapper             *mapper.TurnMapper
	turnCancel         context.CancelFunc
	turnCtx            context.Context
	turnOperation      chan struct{} // closed when the prompt call for the running turn returns
	cancellation       chan struct{} // closed when the latest cancellation has settled
	cancellationGen    int
	persistedInputTurn map[string]bool
	quiesced           bool
	pendingSteeringID  string
	steeringQueueLen   int
	inflight           int
	idle               *sync.Cond
	unsubscribe        func()
}

// NewChatDriver subscribes to the backend and returns the driver.
func NewChatDriver(opts ChatDriverOptions) *ChatDriver {
	d := &ChatDriver{opts: opts, persistedInputTurn: map[string]bool{}}
	d.idle = sync.NewCond(&d.mu)
	d.unsubscribe = opts.Backend.Subscribe(d.onAgentEvent)
	return d
}

// ChatChannel is the chat this driver serves.
func (d *ChatDriver) ChatChannel() string { return d.opts.ChatChannel }

// Busy reports whether a turn is being mapped.
func (d *ChatDriver) Busy() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.mapper != nil
}

func (d *ChatDriver) logf(format string, args ...any) {
	if d.opts.Log != nil {
		d.opts.Log(format, args...)
	}
}

func (d *ChatDriver) dispatch(action ahptypes.StateAction) {
	d.opts.Host.DispatchServerAction(d.opts.ChatChannel, action)
}

func (d *ChatDriver) dispatchAll(actions []ahptypes.StateAction) {
	for _, a := range actions {
		d.dispatch(a)
	}
}

// track runs fn as an in-flight operation that Quiesce waits for.
func (d *ChatDriver) track(fn func()) {
	d.mu.Lock()
	d.inflight++
	d.mu.Unlock()
	go func() {
		defer func() {
			d.mu.Lock()
			d.inflight--
			d.idle.Broadcast()
			d.mu.Unlock()
		}()
		fn()
	}()
}

func (d *ChatDriver) drainInFlight() {
	d.mu.Lock()
	for d.inflight > 0 {
		d.idle.Wait()
	}
	d.mu.Unlock()
}

// Quiesce stops the chat for disposal: no new work is accepted, the agent is aborted, in-flight
// operations drain, and an active turn is closed as cancelled. On failure the driver resumes.
func (d *ChatDriver) Quiesce(ctx context.Context) error {
	d.mu.Lock()
	d.quiesced = true
	cancel := d.turnCancel
	d.mu.Unlock()
	if err := d.opts.Backend.Abort(ctx); err != nil {
		d.Resume()
		return err
	}
	if cancel != nil {
		cancel()
	}
	d.drainInFlight()
	d.finishTurn(mapper.OutcomeCancelled, "")
	return nil
}

// Resume undoes a Quiesce whose disposal did not go through.
func (d *ChatDriver) Resume() {
	d.mu.Lock()
	if !d.quiesced {
		d.mu.Unlock()
		return
	}
	d.quiesced = false
	d.mu.Unlock()
	d.consumeNextQueuedMessage()
}

// Dispose releases the backend.
func (d *ChatDriver) Dispose() {
	if d.unsubscribe != nil {
		d.unsubscribe()
	}
	if disposer, ok := d.opts.Backend.(Disposer); ok {
		if err := disposer.Dispose(); err != nil {
			d.logf("backend disposal failed: %v", err)
		}
	}
}

// Truncate asks the backend to move back to an entry (false when it cannot or refuses).
func (d *ChatDriver) Truncate(ctx context.Context, entryID string) bool {
	t, ok := d.opts.Backend.(Truncater)
	if !ok {
		return false
	}
	var applied bool
	done := make(chan struct{})
	d.track(func() {
		defer close(done)
		var err error
		applied, err = t.Truncate(ctx, entryID)
		if err != nil {
			d.logf("truncate failed: %v", err)
			applied = false
		}
	})
	<-done
	return applied
}

// HandleClientAction reacts to an action a client dispatched on this chat. The action is already
// reduced; this carries out its side effect.
func (d *ChatDriver) HandleClientAction(channel string, action ahptypes.StateAction) bool {
	if channel != d.opts.ChatChannel {
		return false
	}
	switch a := action.Value.(type) {
	case *ahptypes.ChatTurnStartedAction:
		d.startTurn(a.TurnId, a.Message)
	case *ahptypes.ChatTurnCancelledAction:
		d.cancelTurn(a.TurnId)
	case *ahptypes.ChatPendingMessageSetAction:
		if a.Kind == ahptypes.PendingMessageKindSteering {
			d.mu.Lock()
			d.pendingSteeringID = a.Id
			ctx := d.turnCtx
			d.mu.Unlock()
			message := a.Message
			d.track(func() { d.steer(ctx, message) })
		} else {
			d.consumeNextQueuedMessage()
		}
	}
	return true
}

func (d *ChatDriver) steer(ctx context.Context, message ahptypes.Message) {
	if ctx == nil {
		ctx = context.Background()
	}
	d.mu.Lock()
	cancellation := d.cancellation
	d.mu.Unlock()
	if cancellation != nil {
		<-cancellation
	}
	if ctx.Err() != nil {
		return
	}
	input, err := messageInput(message)
	if err == nil {
		err = d.opts.Backend.Steer(ctx, input.Text, input.Images)
	}
	if err != nil {
		d.logf("steer failed: %v", err)
		d.clearPendingSteering()
	}
}

func (d *ChatDriver) newMapper(turnID string) *mapper.TurnMapper {
	return mapper.NewTurnMapper(turnID, time.Now().UnixMilli(), mapper.Options{WorkingDirectory: d.opts.WorkingDirectory})
}

func (d *ChatDriver) startTurn(turnID string, message ahptypes.Message) {
	m := d.newMapper(turnID)
	ctx, cancel := context.WithCancel(context.Background())
	d.mu.Lock()
	d.persistedInputTurn = map[string]bool{}
	d.mapper, d.turnCtx, d.turnCancel = m, ctx, cancel
	d.mu.Unlock()
	d.runPrompt(m, message, "prompt", ctx)
}

func (d *ChatDriver) runPrompt(m *mapper.TurnMapper, message ahptypes.Message, label string, ctx context.Context) {
	operation := make(chan struct{})
	d.mu.Lock()
	d.turnOperation = operation
	cancellation := d.cancellation
	d.mu.Unlock()
	d.track(func() {
		defer func() {
			close(operation)
			d.mu.Lock()
			if d.turnOperation == operation {
				d.turnOperation = nil
			}
			d.mu.Unlock()
		}()
		if cancellation != nil {
			<-cancellation
		}
		if !d.stillCurrent(m, ctx) {
			return
		}
		if message.Model != nil {
			if selector, ok := d.opts.Backend.(ModelSelector); ok {
				if err := selector.SelectModel(ctx, *message.Model); err != nil {
					d.logf("selectModel failed: %v", err)
				}
			}
		}
		if !d.stillCurrent(m, ctx) {
			return
		}
		input, err := messageInput(message)
		if err == nil {
			err = d.opts.Backend.Prompt(ctx, input.Text, input.Images)
		}
		if err != nil {
			d.mu.Lock()
			current := d.mapper == m
			d.mu.Unlock()
			if !current {
				return
			}
			d.logf("%s failed: %v", label, err)
			d.finishTurn(mapper.OutcomeError, err.Error())
		}
	})
}

func (d *ChatDriver) stillCurrent(m *mapper.TurnMapper, ctx context.Context) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.mapper == m && !d.quiesced && ctx.Err() == nil
}

func (d *ChatDriver) onAgentEvent(event mapper.Event) {
	d.evMu.Lock()
	defer d.evMu.Unlock()
	if event["type"] == "queue_update" {
		d.reconcileSteering(event)
		return
	}
	d.mu.Lock()
	m := d.mapper
	d.mu.Unlock()
	if m == nil {
		m = d.adoptForeignTurn(event)
		if m == nil {
			return
		}
	}
	d.dispatchAll(m.Handle(event))
	if event["type"] == "message_end" {
		if msg, _ := event["message"].(map[string]any); msg["role"] == "user" {
			d.mu.Lock()
			d.persistedInputTurn[m.TurnID()] = true
			d.mu.Unlock()
		}
	}
	if m.Finished() {
		if d.opts.RecordTurnAnchor != nil {
			d.opts.RecordTurnAnchor(m.TurnID())
		}
		d.mu.Lock()
		d.persistedInputTurn = map[string]bool{}
		if d.mapper == m {
			d.mapper, d.turnCancel, d.turnCtx = nil, nil, nil
		}
		d.mu.Unlock()
		d.consumeNextQueuedMessage()
	}
}

var foreignTurnCounter atomic.Int64

// adoptForeignTurn opens a chat turn for a prompt that did not come from a client: only a user
// message arriving while no turn is mapped, no cancellation is settling and the chat is not being
// disposed can start one, so the tail of a cancelled run is never mistaken for a new turn.
func (d *ChatDriver) adoptForeignTurn(event mapper.Event) *mapper.TurnMapper {
	if !d.opts.AdoptForeignTurns || event["type"] != "message_start" {
		return nil
	}
	message, _ := event["message"].(map[string]any)
	if message == nil || message["role"] != "user" {
		return nil
	}
	d.mu.Lock()
	if d.quiesced || d.cancellation != nil || d.mapper != nil {
		d.mu.Unlock()
		return nil
	}
	turnID := fmt.Sprintf("local-%d-%d", time.Now().UnixMilli(), foreignTurnCounter.Add(1))
	m := d.newMapper(turnID)
	ctx, cancel := context.WithCancel(context.Background())
	d.persistedInputTurn = map[string]bool{}
	d.mapper, d.turnCtx, d.turnCancel = m, ctx, cancel
	d.mu.Unlock()
	d.dispatch(ahptypes.StateAction{Value: &ahptypes.ChatTurnStartedAction{
		Type: ahptypes.ActionTypeChatTurnStarted, TurnId: turnID, StartedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Message: ahptypes.Message{Text: mapper.TextFromPiUserContent(message["content"]), Origin: ahptypes.MessageOrigin{Kind: ahptypes.MessageKindUser}},
	}})
	// the run began before its user message was seen
	d.dispatchAll(m.Handle(mapper.Event{"type": "agent_start"}))
	return m
}

func (d *ChatDriver) reconcileSteering(event mapper.Event) {
	steering, _ := event["steering"].([]any)
	d.mu.Lock()
	consumed := len(steering) < d.steeringQueueLen
	d.steeringQueueLen = len(steering)
	d.mu.Unlock()
	if consumed {
		d.clearPendingSteering()
	}
}

func (d *ChatDriver) clearPendingSteering() {
	d.mu.Lock()
	id := d.pendingSteeringID
	d.pendingSteeringID = ""
	d.mu.Unlock()
	if id == "" {
		return
	}
	d.dispatch(ahptypes.StateAction{Value: &ahptypes.ChatPendingMessageRemovedAction{
		Type: ahptypes.ActionTypeChatPendingMessageRemoved, Kind: ahptypes.PendingMessageKindSteering, Id: id,
	}})
}

// finishTurn closes the running turn from the host's side: a failed prompt, or disposal.
func (d *ChatDriver) finishTurn(outcome mapper.Outcome, message string) {
	d.evMu.Lock()
	d.mu.Lock()
	m := d.mapper
	d.mu.Unlock()
	if m == nil {
		d.evMu.Unlock()
		return
	}
	d.dispatchAll(m.Finish(outcome, message))
	d.evMu.Unlock()
	d.mu.Lock()
	persisted := d.persistedInputTurn[m.TurnID()]
	d.persistedInputTurn = map[string]bool{}
	if d.mapper == m {
		d.mapper, d.turnCancel, d.turnCtx = nil, nil, nil
	}
	d.mu.Unlock()
	if persisted && d.opts.RecordTurnAnchor != nil {
		d.opts.RecordTurnAnchor(m.TurnID())
	}
	d.clearPendingSteering()
	d.consumeNextQueuedMessage()
}

// consumeNextQueuedMessage starts the head of the queue as its own turn, once the chat is idle.
func (d *ChatDriver) consumeNextQueuedMessage() {
	d.mu.Lock()
	if d.quiesced || d.cancellation != nil || d.mapper != nil {
		d.mu.Unlock()
		return
	}
	state := d.opts.Host.Store().Chat(d.opts.ChatChannel)
	if state == nil || len(state.QueuedMessages) == 0 {
		d.mu.Unlock()
		return
	}
	next := state.QueuedMessages[0]
	turnID := "turn-" + next.Id
	m := d.newMapper(turnID)
	ctx, cancel := context.WithCancel(context.Background())
	d.persistedInputTurn = map[string]bool{}
	d.mapper, d.turnCtx, d.turnCancel = m, ctx, cancel
	d.mu.Unlock()

	queued := next.Id
	d.dispatch(ahptypes.StateAction{Value: &ahptypes.ChatTurnStartedAction{
		Type: ahptypes.ActionTypeChatTurnStarted, TurnId: turnID,
		StartedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), Message: next.Message, QueuedMessageId: &queued,
	}})
	d.runPrompt(m, next.Message, "queued prompt", ctx)
}

func (d *ChatDriver) cancelTurn(turnID string) {
	d.mu.Lock()
	m := d.mapper
	if m != nil && m.TurnID() != turnID {
		d.mu.Unlock()
		return
	}
	turnOperation := d.turnOperation
	inputPersisted := d.persistedInputTurn[turnID]
	d.persistedInputTurn = map[string]bool{}
	if d.turnCancel != nil {
		d.turnCancel()
	}
	d.turnCancel, d.turnCtx, d.mapper = nil, nil, nil
	d.cancellationGen++
	gen := d.cancellationGen
	previous := d.cancellation
	done := make(chan struct{})
	d.cancellation = done
	d.mu.Unlock()

	d.clearPendingSteering()
	if st := d.opts.Host.Store().Chat(d.opts.ChatChannel); st != nil && st.Activity != nil {
		d.dispatch(ahptypes.StateAction{Value: &ahptypes.ChatActivityChangedAction{Type: ahptypes.ActionTypeChatActivityChanged}})
	}

	d.track(func() {
		if previous != nil {
			<-previous
		}
		if err := d.opts.Backend.Abort(context.Background()); err != nil {
			d.logf("abort failed: %v", err)
		}
		if turnOperation != nil {
			<-turnOperation
		}
		if inputPersisted && d.opts.RecordTurnAnchor != nil {
			d.opts.RecordTurnAnchor(turnID)
		}
		d.mu.Lock()
		latest := gen == d.cancellationGen
		if latest {
			d.cancellation = nil
		}
		d.mu.Unlock()
		close(done)
		if latest {
			d.consumeNextQueuedMessage()
		}
	})
}

// PublishDefaultSelection tells the client which model and reasoning effort are in effect. There
// is no protocol field for a "default model", but a client initialises its input from the chat's
// draft, so seeding the draft's selection is how a host answers.
func (d *ChatDriver) PublishDefaultSelection() {
	reporter, ok := d.opts.Backend.(SelectionReporter)
	if !ok {
		return
	}
	selection := reporter.CurrentSelection()
	if selection == nil {
		return
	}
	state := d.opts.Host.Store().Chat(d.opts.ChatChannel)
	text := ""
	if state != nil && state.Draft != nil {
		text = state.Draft.Text
		if a, b := jsonOf(state.Draft.Model), jsonOf(selection); a == b {
			return
		}
	}
	d.dispatch(ahptypes.StateAction{Value: &ahptypes.ChatDraftChangedAction{
		Type:  ahptypes.ActionTypeChatDraftChanged,
		Draft: &ahptypes.Message{Text: text, Origin: ahptypes.MessageOrigin{Kind: ahptypes.MessageKindUser}, Model: selection},
	}})
}

func jsonOf(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
