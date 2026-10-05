// Package live adapts the running PiG session, seen through the public Go SDK, to the agent
// interfaces the AHP host drives (pi.Backend and pi.SessionStore). The upstream host embeds Pi
// and runs many sessions in-process; an extension lives inside ONE session, so exactly that
// session can be served. Anything else fails with a clear error.
package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
)

// ErrReplaced is returned once the PiG session this adapter was attached to has been replaced
// (new session, resume, fork): a retained SDK context belongs to the runtime it was captured in.
var ErrReplaced = errors.New("the PiG session was replaced; restart the AHP listener to serve the new one")

// forwarded are the agent events the AHP event mapper consumes.
var forwarded = []string{
	sdk.EventAgentStart, sdk.EventTurnStart, sdk.EventTurnEnd,
	sdk.EventMessageStart, sdk.EventMessageUpdate, sdk.EventMessageEnd,
	sdk.EventToolExecutionStart, sdk.EventToolExecutionUpdate, sdk.EventToolExecutionEnd,
	sdk.EventAgentEnd, sdk.EventAgentSettled,
}

// Session is the live PiG session as the AHP host sees it.
type Session struct {
	mu        sync.Mutex
	ctx       sdk.Context
	hasCtx    bool
	id        string
	cwd       string
	listeners map[int]func(mapper.Event)
	nextID    int
	steering  []string // pending steering texts, for the synthesised queue_update
	onModel   func()

	// Log receives diagnostics; nil discards them.
	Log func(format string, args ...any)
}

// New creates an adapter that has not seen a session yet.
func New() *Session { return &Session{listeners: map[int]func(mapper.Event){}} }

func (s *Session) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

// Register wires the adapter's event handlers to the extension. onStart runs on every
// session_start with the retained context, the session id and its working directory.
func (s *Session) Register(e *sdk.Extension, onStart func(ctx sdk.Context, sessionID, cwd, reason string), onShutdown func(ctx sdk.Context, reason string)) {
	e.OnSessionStart(func(ctx sdk.Context, data map[string]any) (any, error) {
		id, err := ctx.GetSessionID()
		if err != nil {
			return nil, fmt.Errorf("ahp: cannot read the session id: %w", err)
		}
		s.mu.Lock()
		s.ctx, s.hasCtx, s.id, s.cwd = ctx, true, id, ctx.Cwd()
		s.steering = nil
		s.mu.Unlock()
		reason, _ := data["reason"].(string)
		if onStart != nil {
			onStart(ctx, id, ctx.Cwd(), reason)
		}
		return nil, nil
	})
	e.OnSessionShutdown(func(ctx sdk.Context, data map[string]any) (any, error) {
		s.mu.Lock()
		s.hasCtx = false
		s.mu.Unlock()
		if onShutdown != nil {
			reason, _ := data["reason"].(string)
			onShutdown(ctx, reason)
		}
		return nil, nil
	})
	for _, name := range forwarded {
		e.OnEvent(name, func(_ sdk.Context, data map[string]any) (any, error) {
			s.deliver(name, data)
			return nil, nil
		})
	}
	e.OnEvent(sdk.EventModelSelect, func(sdk.Context, map[string]any) (any, error) {
		s.mu.Lock()
		fn := s.onModel
		s.mu.Unlock()
		if fn != nil {
			fn()
		}
		return nil, nil
	})
}

// OnModelChanged registers fn to run when the active model changes.
func (s *Session) OnModelChanged(fn func()) { s.mu.Lock(); s.onModel = fn; s.mu.Unlock() }

// ID is the id of the live session ("" before session_start).
func (s *Session) ID() string { s.mu.Lock(); defer s.mu.Unlock(); return s.id }

// WorkingDirectory is the live session's working directory.
func (s *Session) WorkingDirectory() string { s.mu.Lock(); defer s.mu.Unlock(); return s.cwd }

// Context returns the retained SDK context, or an error when there is none or its runtime ended.
func (s *Session) Context() (sdk.Context, error) {
	s.mu.Lock()
	ctx, ok := s.ctx, s.hasCtx
	s.mu.Unlock()
	if !ok {
		return sdk.Context{}, errors.New("no PiG session is running")
	}
	if err := ctx.Err(); err != nil {
		return sdk.Context{}, fmt.Errorf("%w: %v", ErrReplaced, err)
	}
	return ctx, nil
}

func (s *Session) deliver(name string, data map[string]any) {
	event := mapper.Event{}
	for k, v := range data {
		event[k] = v
	}
	event["type"] = name
	// PiG reports the system prompt as a message; it is neither conversation nor input.
	if name == sdk.EventMessageStart || name == sdk.EventMessageEnd {
		if msg, _ := event["message"].(map[string]any); msg != nil && msg["role"] == "system" {
			return
		}
	}
	var queueUpdate mapper.Event
	if name == sdk.EventMessageStart {
		if msg, _ := event["message"].(map[string]any); msg != nil && msg["role"] == "user" {
			queueUpdate = s.consumeSteering(msg)
		}
	}
	s.emit(event)
	if queueUpdate != nil {
		s.emit(queueUpdate)
	}
}

func (s *Session) emit(event mapper.Event) {
	s.mu.Lock()
	listeners := make([]func(mapper.Event), 0, len(s.listeners))
	for i := 0; i < s.nextID; i++ {
		if l, ok := s.listeners[i]; ok {
			listeners = append(listeners, l)
		}
	}
	s.mu.Unlock()
	for _, l := range listeners {
		l(event)
	}
}

// consumeSteering drops a delivered steering message from the pending list and returns the
// queue_update Pi would have sent (the SDK carries no such event).
func (s *Session) consumeSteering(message map[string]any) mapper.Event {
	text := mapper.TextFromPiUserContent(message["content"])
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, pending := range s.steering {
		if pending == text {
			s.steering = append(append([]string(nil), s.steering[:i]...), s.steering[i+1:]...)
			return s.queueUpdateLocked()
		}
	}
	return nil
}

func (s *Session) queueUpdateLocked() mapper.Event {
	steering := make([]any, len(s.steering))
	for i, t := range s.steering {
		steering[i] = t
	}
	return mapper.Event{"type": "queue_update", "steering": steering, "followUp": []any{}}
}

// ── pi.Backend ──────────────────────────────────────────────────────────

// Subscribe implements pi.Backend.
func (s *Session) Subscribe(listener func(mapper.Event)) func() {
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	s.listeners[id] = listener
	s.mu.Unlock()
	return func() { s.mu.Lock(); delete(s.listeners, id); s.mu.Unlock() }
}

func content(text string, images []mapper.Image) (any, error) {
	if len(images) == 0 {
		return text, nil
	}
	prepared, err := pi.PrepareImagesForPi(images, true)
	if err != nil {
		return nil, err
	}
	blocks := []map[string]any{{"type": "text", "text": text}}
	for _, img := range prepared {
		blocks = append(blocks, map[string]any{"type": "image", "data": img.Data, "mimeType": img.MimeType})
	}
	return blocks, nil
}

// Prompt implements pi.Backend.
func (s *Session) Prompt(ctx context.Context, text string, images []mapper.Image) error {
	return s.send(ctx, text, images, "")
}

// Steer implements pi.Backend: the message is delivered to the running turn.
func (s *Session) Steer(ctx context.Context, text string, images []mapper.Image) error {
	s.mu.Lock()
	s.steering = append(s.steering, text)
	s.mu.Unlock()
	err := s.send(ctx, text, images, "steer")
	if err != nil {
		s.mu.Lock()
		for i, pending := range s.steering {
			if pending == text {
				s.steering = append(s.steering[:i:i], s.steering[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
	}
	return err
}

func (s *Session) send(ctx context.Context, text string, images []mapper.Image, deliverAs string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sdkCtx, err := s.Context()
	if err != nil {
		return err
	}
	body, err := content(text, images)
	if err != nil {
		return err
	}
	return sdkCtx.SendUserMessage(body, deliverAs)
}

// Abort implements pi.Backend.
func (s *Session) Abort(context.Context) error {
	sdkCtx, err := s.Context()
	if err != nil {
		return err
	}
	sdkCtx.Abort()
	return nil
}

// SelectModel implements pi.ModelSelector.
func (s *Session) SelectModel(_ context.Context, selection ahptypes.ModelSelection) error {
	sdkCtx, err := s.Context()
	if err != nil {
		return err
	}
	if ok, err := sdkCtx.SetModel(selection.Id); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("the model %s is not available", selection.Id)
	}
	if raw, ok := selection.Config[pi.ThinkingConfigKey]; ok {
		var level string
		if json.Unmarshal(raw, &level) == nil && level != "" {
			sdkCtx.SetThinkingLevel(level)
		}
	}
	return nil
}

// CurrentSelection implements pi.SelectionReporter.
func (s *Session) CurrentSelection() *ahptypes.ModelSelection {
	sdkCtx, err := s.Context()
	if err != nil {
		return nil
	}
	info, err := sdkCtx.GetModelInfo()
	if err != nil || info == nil {
		return nil
	}
	selection := &ahptypes.ModelSelection{Id: pi.ModelSelectionID(info.Provider, info.ID)}
	if level, err := sdkCtx.GetThinkingLevel(); err == nil && level != "" {
		raw, _ := json.Marshal(level)
		selection.Config = map[string]json.RawMessage{pi.ThinkingConfigKey: raw}
	}
	return selection
}

// Models lists the models the running session can use.
func (s *Session) Models() []pi.Model {
	sdkCtx, err := s.Context()
	if err != nil {
		return nil
	}
	available, err := sdkCtx.ModelRegistry().GetAvailable()
	if err != nil {
		s.logf("cannot list models: %v", err)
		return nil
	}
	var models []pi.Model
	for _, raw := range available {
		if m, err := pi.ModelFromMap(raw); err == nil {
			models = append(models, m)
		}
	}
	return models
}

// BackendFactory is the pi.BackendFactory of the composition: only the live session has a
// backend.
func (s *Session) BackendFactory(session *pi.LiveSession) (pi.Backend, error) {
	if id := s.ID(); id == "" || session.SessionID != id {
		return nil, fmt.Errorf("this host serves the running PiG session only; session %s is not running", session.SessionID)
	}
	return s, nil
}
