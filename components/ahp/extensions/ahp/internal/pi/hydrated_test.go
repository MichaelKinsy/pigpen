package pi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// writeSession writes a Pi session file containing one full turn with a tool call, then a second
// user message (upstream test/support/hydrated-session.ts).
func writeSession(t testing.TB, root, id, cwd string, includeImage bool) string {
	t.Helper()
	dir := fixtureSessionDirectory(t, root, cwd)
	at := "2026-01-01T00:00:00.000Z"
	line := func(v map[string]any) string { raw, _ := json.Marshal(v); return string(raw) }
	lines := []string{line(map[string]any{"type": "session", "id": id, "parentId": nil, "timestamp": at, "version": 3, "cwd": cwd})}
	var parent any
	n := 0
	push := func(entry map[string]any) {
		n++
		entryID := fmt.Sprintf("e%d", n)
		entry["id"], entry["parentId"], entry["timestamp"] = entryID, parent, at
		lines = append(lines, line(entry))
		parent = entryID
	}
	var userContent any = "Read note.txt"
	if includeImage {
		userContent = []any{obj{"type": "text", "text": "Read note.txt"}, obj{"type": "image", "data": onePixelPNG, "mimeType": "image/png"}}
	}
	push(obj{"type": "message", "message": obj{"role": "user", "content": userContent, "timestamp": 0}})
	push(obj{"type": "message", "message": obj{
		"role": "assistant",
		"content": []any{obj{"type": "thinking", "thinking": "I should read it."},
			obj{"type": "toolCall", "id": "tc-1", "name": "read", "arguments": obj{"path": "note.txt"}}},
		"usage": obj{"input": 10, "output": 5, "cacheRead": 0}, "provider": "fixture", "model": "test-model", "timestamp": 0,
	}})
	push(obj{"type": "message", "message": obj{
		"role": "toolResult", "toolCallId": "tc-1", "toolName": "read", "content": []any{obj{"type": "text", "text": "ALPHA"}}, "timestamp": 0,
	}})
	push(obj{"type": "message", "message": obj{
		"role": "assistant", "content": []any{obj{"type": "text", "text": "It says ALPHA."}}, "provider": "fixture", "model": "test-model", "timestamp": 0,
	}})
	push(obj{"type": "message", "message": obj{"role": "user", "content": "Thanks", "timestamp": 0}})
	path := filepath.Join(dir, "2026-01-01T00-00-00-000Z_"+id+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// recordingBackend records what a resumed session actually asks the agent to do.
type recordingBackend struct {
	mu        sync.Mutex
	prompts   []string
	listeners []func(mapper.Event)
}

func (b *recordingBackend) Subscribe(l func(mapper.Event)) func() {
	b.mu.Lock()
	b.listeners = append(b.listeners, l)
	b.mu.Unlock()
	return func() {}
}

func (b *recordingBackend) Prompt(_ context.Context, text string, _ []mapper.Image) error {
	b.mu.Lock()
	b.prompts = append(b.prompts, text)
	ls := append([]func(mapper.Event){}, b.listeners...)
	b.mu.Unlock()
	for _, typ := range []string{"agent_start", "agent_settled"} {
		for _, l := range ls {
			l(mapper.Event{"type": typ})
		}
	}
	return nil
}
func (b *recordingBackend) Steer(context.Context, string, []mapper.Image) error { return nil }
func (b *recordingBackend) Abort(context.Context) error                         { return nil }

func (b *recordingBackend) promptList() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.prompts...)
}

type hydratedOptions struct {
	deleteFile   func(path string) (pi.SessionFileDeletionResult, error)
	includeImage bool
}

type hydratedFixture struct {
	t         *testing.T
	host      *host.Host
	services  *pi.Services
	client    *testkit.Client
	sessionID string
	root      string
	workspace string
	backend   *recordingBackend
	mu        sync.Mutex
	deleted   []string
	extra     int
}

func (f *hydratedFixture) deletedFiles() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}

func (f *hydratedFixture) session() string { return wire.SessionURI(f.sessionID) }
func (f *hydratedFixture) chat() string    { return wire.ChatURI(f.sessionID) }

// connectAsVSCode attaches another client that identifies itself as an editor window.
func (f *hydratedFixture) connectAsVSCode() *testkit.Client {
	f.t.Helper()
	f.extra++
	c := testkit.Connect(f.t, f.host)
	f.t.Cleanup(c.Close)
	c.Initialize(fmt.Sprintf("vscode-client-%d", f.extra+1), obj{"clientInfo": obj{"name": "vscode-editor-window", "title": "VS Code"}})
	return c
}

func startHydrated(t *testing.T, o hydratedOptions) *hydratedFixture {
	t.Helper()
	f := &hydratedFixture{t: t, root: t.TempDir(), backend: &recordingBackend{}}
	f.workspace = filepath.Join(t.TempDir(), "pi ahp hydrate cwd")
	if err := os.MkdirAll(f.workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	f.sessionID = newID()
	writeSession(t, f.root, f.sessionID, f.workspace, o.includeImage)
	f.host = testkit.NewHost(host.Options{})
	f.host.Store() // root installed by NewHost
	f.services = pi.NewServices(pi.ServicesOptions{
		Host: f.host, SessionRoot: f.root, DefaultWorkingDirectory: f.workspace,
		CreateBackend:        func(*pi.LiveSession) (pi.Backend, error) { return f.backend, nil },
		CreateSessionManager: pi.PersistentStorage(filepath.Join(f.root, "created")),
		DefaultSelection: func() *ahptypes.ModelSelection {
			return &ahptypes.ModelSelection{Id: "fallback-model", Config: map[string]json.RawMessage{"thinkingLevel": json.RawMessage(`"medium"`)}}
		},
		DeleteFile: func(path string) (pi.SessionFileDeletionResult, error) {
			f.mu.Lock()
			f.deleted = append(f.deleted, path)
			f.mu.Unlock()
			if o.deleteFile != nil {
				return o.deleteFile(path)
			}
			_ = os.Remove(path)
			return pi.SessionFileDeletionResult{OK: true}, nil
		},
	})
	f.host.Serve(f.services.Capabilities())
	f.client = testkit.Connect(t, f.host)
	t.Cleanup(f.client.Close)
	f.client.Initialize("hydrate-client", nil)
	return f
}
