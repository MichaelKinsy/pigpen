package pi_test

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pisession"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// restartBackend answers every prompt with an empty settled run.
type restartBackend struct {
	mu        sync.Mutex
	prompts   []string
	listeners []func(mapper.Event)
}

func (b *restartBackend) Subscribe(l func(mapper.Event)) func() {
	b.mu.Lock()
	b.listeners = append(b.listeners, l)
	b.mu.Unlock()
	return func() {}
}
func (b *restartBackend) Prompt(_ context.Context, text string, _ []mapper.Image) error {
	b.mu.Lock()
	b.prompts = append(b.prompts, text)
	ls := append([]func(mapper.Event){}, b.listeners...)
	b.mu.Unlock()
	for _, l := range ls {
		l(mapper.Event{"type": "agent_start"})
		l(mapper.Event{"type": "agent_settled"})
	}
	return nil
}
func (b *restartBackend) Steer(context.Context, string, []mapper.Image) error { return nil }
func (b *restartBackend) Abort(context.Context) error                         { return nil }

// Twin of upstream test/reconnect.test.ts "reconnect after host restart": the durable session
// written by a previous host is restored when a client that knew the old host reconnects.
func TestReconnectAfterHostRestart(t *testing.T) {
	twin.Run(t, "reconnect", "repairs VS Code's channel-less reconnect and restores a durable session", func(t *testing.T) {
		root, workspace := t.TempDir(), t.TempDir()
		id := newID()
		writer, err := pisession.Create(workspace, filepath.Join(root, "fixture"), pisession.Options{ID: id})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.AppendMessage(obj{"role": "user", "content": "before restart", "timestamp": 0}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.AppendMessage(obj{"role": "assistant", "content": []any{obj{"type": "text", "text": "persisted reply"}}, "timestamp": 0}); err != nil {
			t.Fatal(err)
		}
		backend := &restartBackend{}
		h := startHarness(t, harnessOptions{sessionRoot: root, workingDir: workspace, createBackend: func(*pi.LiveSession) (pi.Backend, error) { return backend, nil }})

		clientSession := "pi:/" + id
		clientChat := "ahp-chat://default/" + base64.RawURLEncoding.EncodeToString([]byte(clientSession))
		client := testkit.Connect(t, h.host)
		t.Cleanup(client.Close)
		// The reconnect names no channel, as VS Code's does.
		var result struct {
			Type      string
			Snapshots []struct {
				Resource string
				State    map[string]any
			}
		}
		client.Decode(client.Must("reconnect", obj{
			"clientId": "vscode-from-previous-host", "lastSeenServerSeq": 42,
			"subscriptions": []string{wire.RootChannel, clientSession}, "_meta": obj{"vscode.telemetryLevel": "off"},
		}), &result)
		if result.Type != "snapshot" {
			t.Fatalf("type = %q", result.Type)
		}
		var resources []string
		var sessionState map[string]any
		for _, s := range result.Snapshots {
			resources = append(resources, s.Resource)
			if s.Resource == clientSession {
				sessionState = s.State
			}
		}
		if !reflect.DeepEqual(sortedCopy(resources), sortedCopy([]string{wire.RootChannel, clientSession})) {
			t.Fatalf("snapshots = %v", resources)
		}
		if sessionState["defaultChat"] != clientChat {
			t.Fatalf("defaultChat = %v, want %s", sessionState["defaultChat"], clientChat)
		}

		rpcErr := client.ExpectError("disposeSession", obj{"channel": clientSession}, wire.CodeInvalidRequest)
		if !strings.Contains(rpcErr.Message, "VS Code provisional-session lifecycle bug") {
			t.Fatalf("message = %q", rpcErr.Message)
		}
		if !h.host.Store().Has(wire.SessionURI(id)) {
			t.Fatal("the durable session must survive the refused disposal")
		}

		chat := snapshotState[ahptypes.ChatState](t, client, clientChat)
		turn := chat.Turns[0]
		if turn.Message.Text != "before restart" || turn.State != ahptypes.TurnStateComplete {
			t.Fatalf("restored turn = %+v", turn)
		}
		var reply string
		for _, p := range turn.ResponseParts {
			if md, ok := p.Value.(*ahptypes.MarkdownResponsePart); ok {
				reply = md.Content
			}
		}
		if reply != "persisted reply" {
			t.Fatalf("reply = %q", reply)
		}
		if !h.host.Store().Has(wire.SessionURI(id)) || !h.host.Store().Has(wire.ChatURI(id)) {
			t.Fatal("session and chat must both be restored")
		}

		client.Dispatch(clientChat, obj{"type": "chat/turnStarted", "turnId": "after-restart", "startedAt": "2025-01-01T00:00:00.000Z", "message": userMessage("continue after restart")})
		testkit.Eventually(t, "the post-restart prompt to reach the backend", func() bool { backend.mu.Lock(); defer backend.mu.Unlock(); return len(backend.prompts) == 1 })
		if !reflect.DeepEqual(backend.prompts, []string{"continue after restart"}) {
			t.Fatalf("prompts = %v", backend.prompts)
		}
		testkit.Eventually(t, "the resumed turn to settle", func() bool {
			s := h.host.Store().Chat(wire.ChatURI(id))
			return s.ActiveTurn == nil && len(s.Turns) == 2
		})
		state := h.host.Store().Chat(wire.ChatURI(id))
		if state.Turns[1].Message.Text != "continue after restart" || state.Turns[1].State != ahptypes.TurnStateComplete {
			t.Fatalf("turns = %+v", turnShapes(state.Turns))
		}
	})
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// Twin of upstream test/session-storage.test.serial.ts.
func TestDefaultSessionStorage(t *testing.T) {
	twin.Run(t, "session-storage", "keeps the default catalogue and Pi session writer on the same isolated profile", func(t *testing.T) {
		agentDir, workspace := t.TempDir(), t.TempDir()
		catalogueRoot, create := pi.DefaultStorage(agentDir)
		sessionID := newID()
		store, err := create(workspace, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		manager := store.(*pisession.Manager)
		if _, err := manager.AppendMessage(obj{"role": "user", "content": "hello", "timestamp": 0}); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.AppendMessage(obj{"role": "assistant", "content": []any{obj{"type": "text", "text": "hello"}}, "timestamp": 0}); err != nil {
			t.Fatal(err)
		}
		file := store.File()
		if file == "" {
			t.Fatal("no session file")
		}
		found, err := pi.NewCatalogue(catalogueRoot).FindSessionFile(sessionID)
		if err != nil || found != file {
			t.Fatalf("catalogue found %q (%v), writer wrote %q", found, err, file)
		}
		if rel, err := filepath.Rel(filepath.Join(agentDir, "sessions"), file); err != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("file %s is outside the profile (%q, %v)", file, rel, err)
		}
	})
}
