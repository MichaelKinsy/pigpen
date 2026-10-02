package pi_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of upstream test/session-disposal.test.ts: durable session removal and its concurrency
// boundary.

type disposalFixture struct {
	t      *testing.T
	h      *harness
	client *testkit.Client
}

func startDisposal(t *testing.T, create pi.BackendFactory, deleteFile func(string) (pi.SessionFileDeletionResult, error)) *disposalFixture {
	t.Helper()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	h := startHarness(t, harnessOptions{workingDir: workspace, sessionRoot: filepath.Join(root, "sessions"), createBackend: create, deleteFile: deleteFile})
	c := testkit.Connect(t, h.host)
	t.Cleanup(c.Close)
	c.Initialize(nextClientID(), nil)
	return &disposalFixture{t: t, h: h, client: c}
}

func (f *disposalFixture) createReady() (uri, chat string) {
	f.t.Helper()
	id := newID()
	uri, chat = wire.SessionURI(id), wire.ChatURI(id)
	f.client.Must("createSession", obj{"channel": uri})
	testkit.Eventually(f.t, "the session backend to become ready", func() bool {
		return f.h.host.Store().Session(uri).Lifecycle == ahptypes.SessionLifecycleReady
	})
	return uri, chat
}

func (f *disposalFixture) activeSessions() int64 {
	if a := f.h.host.Store().Root(wire.RootChannel).ActiveSessions; a != nil {
		return *a
	}
	return 0
}

func (f *disposalFixture) dispose(uri string) <-chan *testkit.RPCError {
	out := make(chan *testkit.RPCError, 1)
	go func() { _, err := f.client.Request("disposeSession", obj{"channel": uri}); out <- err }()
	return out
}

func wait(t *testing.T, what string, ch <-chan *testkit.RPCError) *testkit.RPCError {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(testkit.Timeout):
		t.Fatalf("timed out waiting for %s", what)
		return nil
	}
}

// disposableBackend is a backend with closures for the parts a disposal test needs to observe.
type disposableBackend struct {
	funcBackend
	selectModel func(context.Context) error
	dispose     func() error
}

func (b disposableBackend) SelectModel(ctx context.Context, _ ahptypes.ModelSelection) error {
	if b.selectModel == nil {
		return nil
	}
	return b.selectModel(ctx)
}
func (b disposableBackend) Dispose() error {
	if b.dispose == nil {
		return nil
	}
	return b.dispose()
}

func TestSessionDisposal(t *testing.T) {
	twin.Run(t, "session-disposal", "keeps the session usable when durable deletion fails", func(t *testing.T) {
		var attempts atomic.Int32
		f := startDisposal(t, nil, func(string) (pi.SessionFileDeletionResult, error) {
			attempts.Add(1)
			return pi.SessionFileDeletionResult{Error: "permission denied"}, nil
		})
		uri, _ := f.createReady()
		before := f.activeSessions()
		if before != 1 {
			t.Fatalf("activeSessions = %d", before)
		}
		f.client.ExpectError("disposeSession", obj{"channel": uri}, wire.CodeInternalError)
		_, rpcErr := f.client.Request("disposeSession", obj{"channel": uri})
		if rpcErr == nil || !strings.Contains(rpcErr.Message, "permission denied") {
			t.Fatalf("error = %v", rpcErr)
		}
		if attempts.Load() != 2 { // one per request above; each settles before the next starts
			t.Fatalf("deletion attempts = %d", attempts.Load())
		}
		if !f.h.services.Registry.Has(uri) || f.activeSessions() != before {
			t.Fatal("the session must remain")
		}
		var listed struct{ Items []obj }
		f.client.Decode(f.client.Must("listSessions", obj{"channel": wire.RootChannel}), &listed)
		if len(listed.Items) != 1 || listed.Items[0]["resource"] != uri {
			t.Fatalf("listing = %v", listed.Items)
		}
		f.client.Dispatch(uri, obj{"type": "session/titleChanged", "title": "Still here"})
		f.client.Ping()
		if got := f.h.host.Store().Session(uri).Title; got != "Still here" {
			t.Fatalf("title = %q", got)
		}
	})

	twin.Run(t, "session-disposal", "coalesces concurrent requests and rejects new work until deletion settles", func(t *testing.T) {
		var attempts atomic.Int32
		finish := make(chan bool)
		defer func() {
			select {
			case finish <- false:
			default:
			}
		}()
		f := startDisposal(t, nil, func(string) (pi.SessionFileDeletionResult, error) {
			attempts.Add(1)
			return pi.SessionFileDeletionResult{OK: <-finish}, nil
		})
		uri, _ := f.createReady()
		f.client.Subscribe(uri)
		titleBefore := f.h.host.Store().Session(uri).Title

		first := f.dispose(uri)
		testkit.Eventually(t, "durable deletion to start", func() bool { return attempts.Load() == 1 })
		second := f.dispose(uri)
		testkit.Eventually(t, "the second request to coalesce", func() bool { return f.h.services.Registry.DisposalWaiters(uri) == 1 })
		seq := f.client.Dispatch(uri, obj{"type": "session/titleChanged", "title": "Too late"})
		env := f.client.NextEnvelope(uri, seq)
		if env.RejectionReason == nil || *env.RejectionReason == "" {
			t.Fatal("new work during deletion must be rejected")
		}
		if got := f.h.host.Store().Session(uri).Title; got != titleBefore {
			t.Fatalf("title = %q", got)
		}
		finish <- true
		if err := wait(t, "the first disposal", first); err != nil {
			t.Fatal(err)
		}
		if err := wait(t, "the coalesced disposal", second); err != nil {
			t.Fatal(err)
		}
		if attempts.Load() != 1 {
			t.Fatalf("deletion attempts = %d", attempts.Load())
		}
		if f.h.host.Store().Has(uri) {
			t.Fatal("the session channel remains")
		}
	})

	twin.Run(t, "session-disposal", "quiesces an active backend and commits removal despite cleanup failure", func(t *testing.T) {
		var mu sync.Mutex
		var promptStarted, promptSettled, aborted, disposed, sawQuiescence bool
		release := newRelease()
		defer release.open()
		backend := disposableBackend{
			funcBackend: funcBackend{
				prompt: func(context.Context, string) error {
					mu.Lock()
					promptStarted = true
					mu.Unlock()
					<-release.ch
					mu.Lock()
					promptSettled = true
					mu.Unlock()
					return nil
				},
				abort: func(context.Context) error {
					mu.Lock()
					aborted = true
					mu.Unlock()
					release.open()
					return nil
				},
			},
			dispose: func() error {
				mu.Lock()
				disposed = true
				mu.Unlock()
				return errors.New("cleanup failed after deletion")
			},
		}
		f := startDisposal(t, func(*pi.LiveSession) (pi.Backend, error) { return backend, nil }, func(string) (pi.SessionFileDeletionResult, error) {
			mu.Lock()
			sawQuiescence = aborted && promptSettled
			mu.Unlock()
			return pi.SessionFileDeletionResult{OK: true}, nil
		})
		uri, chat := f.createReady()
		f.client.Subscribe(chat)
		f.client.Dispatch(chat, obj{"type": "chat/turnStarted", "turnId": "turn-to-delete", "startedAt": "2025-01-01T00:00:00.000Z", "message": userMessage("keep running")})
		testkit.Eventually(t, "the active prompt to start", func() bool { mu.Lock(); defer mu.Unlock(); return promptStarted })
		f.client.Must("disposeSession", obj{"channel": uri})
		mu.Lock()
		defer mu.Unlock()
		if !sawQuiescence {
			t.Fatal("deletion ran before the backend was quiescent")
		}
		if !disposed || f.h.host.Store().Has(uri) {
			t.Fatalf("disposed=%v channelPresent=%v", disposed, f.h.host.Store().Has(uri))
		}
	})

	twin.Run(t, "session-disposal", "resumes queued work when backend quiescence fails", func(t *testing.T) {
		var mu sync.Mutex
		var listeners []func(mapper.Event)
		var prompts []string
		var attempts atomic.Int32
		backend := funcBackend{
			subscribe: func(l func(mapper.Event)) func() {
				mu.Lock()
				listeners = append(listeners, l)
				mu.Unlock()
				return func() {}
			},
			prompt: func(_ context.Context, text string) error {
				mu.Lock()
				prompts = append(prompts, text)
				mu.Unlock()
				return nil
			},
			abort: func(context.Context) error {
				mu.Lock()
				ls := append([]func(mapper.Event){}, listeners...)
				mu.Unlock()
				for _, l := range ls {
					l(mapper.Event{"type": "agent_settled"})
				}
				return errors.New("abort failed")
			},
		}
		f := startDisposal(t, func(*pi.LiveSession) (pi.Backend, error) { return backend, nil }, func(string) (pi.SessionFileDeletionResult, error) {
			attempts.Add(1)
			return pi.SessionFileDeletionResult{OK: true}, nil
		})
		uri, chat := f.createReady()
		f.client.Subscribe(chat)
		f.client.Dispatch(chat, obj{"type": "chat/turnStarted", "turnId": "active", "startedAt": "2025-01-01T00:00:00.000Z", "message": userMessage("active")})
		testkit.Eventually(t, "the active prompt to start", func() bool { mu.Lock(); defer mu.Unlock(); return len(prompts) == 1 })
		f.client.Dispatch(chat, obj{"type": "chat/pendingMessageSet", "kind": "queued", "id": "after-failure", "message": userMessage("after failure")})
		f.client.Ping()
		rpcErr := f.client.ExpectError("disposeSession", obj{"channel": uri}, wire.CodeInternalError)
		if !strings.Contains(rpcErr.Message, "abort failed") {
			t.Fatalf("error = %v", rpcErr)
		}
		testkit.Eventually(t, "queued work to resume", func() bool {
			mu.Lock()
			defer mu.Unlock()
			for _, p := range prompts {
				if p == "after failure" {
					return true
				}
			}
			return false
		})
		if attempts.Load() != 0 || !f.h.services.Registry.Has(uri) {
			t.Fatalf("deletion attempts %d, live %v", attempts.Load(), f.h.services.Registry.Has(uri))
		}
	})

	twin.Run(t, "session-disposal", "drains an in-flight model selection before deleting", func(t *testing.T) {
		var selectionStarted, selectionSettled, deletedEarly atomic.Bool
		var prompts, attempts atomic.Int32
		var aborted atomic.Bool
		selection := newRelease()
		defer selection.open()
		backend := disposableBackend{
			funcBackend: funcBackend{
				prompt: func(context.Context, string) error { prompts.Add(1); return nil },
				abort:  func(context.Context) error { aborted.Store(true); return nil },
			},
			selectModel: func(context.Context) error {
				selectionStarted.Store(true)
				<-selection.ch
				selectionSettled.Store(true)
				return nil
			},
		}
		f := startDisposal(t, func(*pi.LiveSession) (pi.Backend, error) { return backend, nil }, func(string) (pi.SessionFileDeletionResult, error) {
			attempts.Add(1)
			if !selectionSettled.Load() {
				deletedEarly.Store(true)
			}
			return pi.SessionFileDeletionResult{OK: true}, nil
		})
		uri, chat := f.createReady()
		f.client.Subscribe(chat)
		message := userMessage("switch first")
		message["model"] = obj{"id": "pi/other-model"}
		f.client.Dispatch(chat, obj{"type": "chat/turnStarted", "turnId": "turn-selecting", "startedAt": "2025-01-01T00:00:00.000Z", "message": message})
		testkit.Eventually(t, "model selection to start", selectionStarted.Load)

		disposing := f.dispose(uri)
		// Disposal runs on its own goroutine here (upstream's request is handled in order), so
		// wait until it has quiesced the agent before checking that deletion is held back.
		testkit.Eventually(t, "disposal to quiesce the backend", aborted.Load)
		f.client.Ping()
		if attempts.Load() != 0 {
			t.Fatalf("deletion started while a session-writing model selection was in flight")
		}
		selection.open()
		if err := wait(t, "the disposal", disposing); err != nil {
			t.Fatal(err)
		}
		if attempts.Load() != 1 || deletedEarly.Load() {
			t.Fatalf("attempts %d, deletion overtook the model selection: %v", attempts.Load(), deletedEarly.Load())
		}
		if prompts.Load() != 0 {
			t.Fatal("a prompt started after disposal quiesced its turn")
		}
	})

	twin.Run(t, "session-disposal", "disposes a backend that finishes starting after removal", func(t *testing.T) {
		start := make(chan struct{})
		var disposals atomic.Int32
		f := startDisposal(t, func(*pi.LiveSession) (pi.Backend, error) {
			<-start
			return disposableBackend{dispose: func() error { disposals.Add(1); return nil }}, nil
		}, nil)
		uri := wire.SessionURI(newID())
		f.client.Must("createSession", obj{"channel": uri})
		f.client.Must("disposeSession", obj{"channel": uri})
		close(start)
		testkit.Eventually(t, "the late backend to be disposed", func() bool { return disposals.Load() == 1 })
		if f.h.host.Store().Has(uri) {
			t.Fatal("the session channel remains")
		}
		time.Sleep(30 * time.Millisecond)
		if disposals.Load() != 1 {
			t.Fatalf("backend disposed %d times", disposals.Load())
		}
	})
}
