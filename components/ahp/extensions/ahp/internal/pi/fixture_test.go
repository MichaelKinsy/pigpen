package pi_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// onePixelPNG is upstream's test/support/images.ts ONE_PIXEL_PNG.
const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

type obj = map[string]any

// event decodes a JSON literal into a Pi event.
func event(t testing.TB, literal string) mapper.Event {
	t.Helper()
	var e mapper.Event
	if err := json.Unmarshal([]byte(literal), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// say is upstream's chat-driver test script: one assistant message that says text, then settles.
func say(t testing.TB, text string) []mapper.Event {
	quoted, _ := json.Marshal(text)
	return []mapper.Event{
		event(t, `{"type":"agent_start"}`),
		event(t, `{"type":"message_start","message":{"role":"assistant"}}`),
		event(t, `{"type":"message_update","message":{"role":"assistant"},"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":`+string(quoted)+`}}`),
		event(t, `{"type":"message_end","message":{"role":"assistant"}}`),
		event(t, `{"type":"agent_end","messages":[],"willRetry":false}`),
		event(t, `{"type":"agent_settled"}`),
	}
}

// release is a gate a test can open exactly once.
type release struct {
	once sync.Once
	ch   chan struct{}
}

func newRelease() *release { return &release{ch: make(chan struct{})} }
func (r *release) open()   { r.once.Do(func() { close(r.ch) }) }

// scriptedBackend records prompts and replays a scripted event sequence for each one, so a
// turn's shape is fully determined by the test (upstream ScriptedBackend).
type scriptedBackend struct {
	mu            sync.Mutex
	prompts       []string
	promptImages  [][]mapper.Image
	steers        []string
	steerImages   [][]mapper.Image
	promptCalls   int
	aborts        int
	selections    int
	promptGate    *release
	selectionGate *release
	abortGate     *release
	listeners     map[int]func(mapper.Event)
	nextListener  int
	script        func(text string) []mapper.Event
}

func newScriptedBackend(script func(string) []mapper.Event) *scriptedBackend {
	return &scriptedBackend{script: script, listeners: map[int]func(mapper.Event){}}
}

func (b *scriptedBackend) Subscribe(l func(mapper.Event)) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextListener
	b.nextListener++
	b.listeners[id] = l
	return func() { b.mu.Lock(); delete(b.listeners, id); b.mu.Unlock() }
}

func (b *scriptedBackend) emit(e mapper.Event) {
	b.mu.Lock()
	ls := make([]func(mapper.Event), 0, len(b.listeners))
	for _, l := range b.listeners {
		ls = append(ls, l)
	}
	b.mu.Unlock()
	for _, l := range ls {
		l(e)
	}
}

func (b *scriptedBackend) Prompt(ctx context.Context, text string, images []mapper.Image) error {
	b.mu.Lock()
	b.promptCalls++
	gate := b.promptGate
	b.mu.Unlock()
	if gate != nil {
		<-gate.ch
	}
	if ctx.Err() != nil {
		return nil
	}
	b.mu.Lock()
	b.prompts = append(b.prompts, text)
	b.promptImages = append(b.promptImages, images)
	b.mu.Unlock()
	// Deliver like a real agent: the driver must not depend on events arriving inside Prompt.
	for _, e := range b.script(text) {
		b.emit(e)
	}
	return nil
}

func (b *scriptedBackend) Steer(_ context.Context, text string, images []mapper.Image) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.steers = append(b.steers, text)
	b.steerImages = append(b.steerImages, images)
	return nil
}

func (b *scriptedBackend) Abort(context.Context) error {
	b.mu.Lock()
	b.aborts++
	gate := b.abortGate
	b.mu.Unlock()
	if gate != nil {
		<-gate.ch
	}
	return nil
}

func (b *scriptedBackend) SelectModel(context.Context, ahptypes.ModelSelection) error {
	b.mu.Lock()
	b.selections++
	gate := b.selectionGate
	b.mu.Unlock()
	if gate != nil {
		<-gate.ch
	}
	return nil
}

func (b *scriptedBackend) count(field func(*scriptedBackend) int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return field(b)
}

func (b *scriptedBackend) promptList() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.prompts...)
}

// funcBackend is a backend made of closures.
type funcBackend struct {
	subscribe func(func(mapper.Event)) func()
	prompt    func(ctx context.Context, text string) error
	steer     func(ctx context.Context, text string) error
	abort     func(ctx context.Context) error
}

func (b funcBackend) Subscribe(l func(mapper.Event)) func() {
	if b.subscribe == nil {
		return func() {}
	}
	return b.subscribe(l)
}
func (b funcBackend) Prompt(ctx context.Context, text string, _ []mapper.Image) error {
	if b.prompt == nil {
		return nil
	}
	return b.prompt(ctx, text)
}
func (b funcBackend) Steer(ctx context.Context, text string, _ []mapper.Image) error {
	if b.steer == nil {
		return nil
	}
	return b.steer(ctx, text)
}
func (b funcBackend) Abort(ctx context.Context) error {
	if b.abort == nil {
		return nil
	}
	return b.abort(ctx)
}

// fixture is a host with a session registry over in-memory storage and one connected client.
type fixture struct {
	t              *testing.T
	host           *host.Host
	sessions       *pi.Registry
	client         *testkit.Client
	sessionChannel string
	chatChannel    string
	id             string
}

var uuidCounter struct {
	sync.Mutex
	n int
}

func newID() string {
	uuidCounter.Lock()
	defer uuidCounter.Unlock()
	uuidCounter.n++
	return "0190a1b2-c3d4-7e5f-8a9b-" + pad(uuidCounter.n)
}

func pad(n int) string {
	s := "000000000000"
	d := []byte(s)
	for i := len(d) - 1; i >= 0 && n > 0; i-- {
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d)
}

func (f *fixture) chat() *ahptypes.ChatState       { return f.host.Store().Chat(f.chatChannel) }
func (f *fixture) session() *ahptypes.SessionState { return f.host.Store().Session(f.sessionChannel) }

// start creates the host and registry (no session yet).
func startWith(t *testing.T, opts pi.RegistryOptions) (*host.Host, *pi.Registry) {
	t.Helper()
	h := testkit.NewHost(host.Options{})
	opts.Host = h
	if opts.CreateSessionManager == nil {
		opts.CreateSessionManager = pi.InMemoryStorage
	}
	reg := pi.NewRegistry(opts)
	h.Serve(host.Capabilities{Sessions: reg, TurnPaging: fetchTurns{reg}})
	return h, reg
}

type fetchTurns struct{ r *pi.Registry }

func (f fetchTurns) FetchTurns(ctx context.Context, p ahptypes.FetchTurnsParams) error {
	return f.r.FetchTurns(ctx, p)
}

// startFixture is upstream's chat-driver startFixture: a created session with both channels
// subscribed.
func startFixture(t *testing.T, backend pi.Backend) *fixture {
	t.Helper()
	h, reg := startWith(t, pi.RegistryOptions{CreateBackend: func(*pi.LiveSession) (pi.Backend, error) { return backend, nil }})
	client := testkit.Connect(t, h)
	t.Cleanup(client.Close)
	client.Initialize("driver-client", nil)
	id := newID()
	f := &fixture{t: t, host: h, sessions: reg, client: client, id: id, sessionChannel: wire.SessionURI(id), chatChannel: wire.ChatURI(id)}
	client.Must("createSession", obj{"channel": f.sessionChannel})
	client.Subscribe(f.sessionChannel)
	client.Subscribe(f.chatChannel)
	// The session is ready once its backend is attached.
	testkit.Eventually(t, "the session to become ready", func() bool {
		s := f.session()
		return s != nil && s.Lifecycle == ahptypes.SessionLifecycleReady
	})
	return f
}

func userMessage(text string) obj {
	return obj{"text": text, "origin": obj{"kind": "user"}}
}

func embeddedText(text, label string) obj {
	return obj{"type": "embeddedResource", "label": label, "contentType": "text/plain", "data": base64.StdEncoding.EncodeToString([]byte(text))}
}

func embeddedImage(label string) obj {
	return obj{"type": "embeddedResource", "label": label, "displayKind": "image", "contentType": "image/png", "data": onePixelPNG}
}

func (f *fixture) turnStarted(turnID string, message obj) {
	f.client.Dispatch(f.chatChannel, obj{"type": "chat/turnStarted", "turnId": turnID, "startedAt": "2025-01-01T00:00:00.000Z", "message": message})
}

func (f *fixture) pending(kind, id string, message obj) {
	f.client.Dispatch(f.chatChannel, obj{"type": "chat/pendingMessageSet", "kind": kind, "id": id, "message": message})
}

func (f *fixture) cancel(turnID string) {
	f.client.Dispatch(f.chatChannel, obj{"type": "chat/turnCancelled", "turnId": turnID, "duration": 0})
}

type turnShape struct {
	ID, Text string
	State    ahptypes.TurnState
}

func turnShapes(turns []ahptypes.Turn) []turnShape {
	out := []turnShape{}
	for _, t := range turns {
		out = append(out, turnShape{t.Id, t.Message.Text, t.State})
	}
	return out
}
