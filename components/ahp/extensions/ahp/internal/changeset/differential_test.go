package changeset_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/changeset"
	"github.com/MichaelKinsy/pigpen/ahp/internal/channels"
	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// recorder is a transport that keeps every frame the host sends.
type recorder struct {
	mu     sync.Mutex
	frames []json.RawMessage
}

func (r *recorder) Send(frame []byte) error {
	r.mu.Lock()
	r.frames = append(r.frames, append(json.RawMessage(nil), frame...))
	r.mu.Unlock()
	return nil
}
func (r *recorder) Close() error { return nil }

// TestDifferential is the Go side of the protocol differential against pi-ahp
// (proof/differential/oracle-run.mjs runs the same steps on the original's AhpHost and
// ChangesetService). With AHP_DIFF_EXPECT naming the oracle's transcript, the Go host's wire
// transcript must equal it after normalization; AHP_DIFF_OUT saves the Go transcript.
func TestDifferential(t *testing.T) {
	expectPath := os.Getenv("AHP_DIFF_EXPECT")
	if expectPath == "" {
		t.Skip("set AHP_DIFF_EXPECT (and AHP_DIFF_WORKSPACE) to compare with the oracle's transcript")
	}
	workspace := os.Getenv("AHP_DIFF_WORKSPACE")
	if workspace == "" {
		t.Fatal("AHP_DIFF_WORKSPACE is required")
	}
	id := "diff-session"
	sessionChannel, chatChannel := wire.SessionURI(id), wire.ChatURI(id)
	session := changeset.Session{URI: sessionChannel, SessionID: id, WorkingDirectory: workspace}
	h := host.New(host.Options{})
	h.InstallRootChannel(nil)
	state := &ahptypes.SessionState{
		Provider: "pi", Title: "Differential", Status: ahptypes.SessionStatusIdle, Lifecycle: ahptypes.SessionLifecycleReady,
		WorkingDirectories: []string{wire.PathToFileURI(workspace)}, ActiveClients: []ahptypes.SessionActiveClient{}, Chats: []ahptypes.ChatSummary{},
	}
	_ = h.Store().Create(sessionChannel, state, wire.KindSession)
	_ = h.Store().Create(chatChannel, channels.InitialChatState(chatChannel, "Differential", nil), wire.KindChat)
	git := newControlledGit(changeset.Workspace{
		Cwd: workspace, RepositoryRoot: workspace, GitDirectory: filepath.Join(workspace, ".git"),
		CommonGitDirectory: filepath.Join(workspace, ".git"), Pathspec: ".", Head: repeat('a', 40), Parent: repeat('b', 40),
	})
	git.blockFirstCall = false
	watches := &controlledWatches{}
	service := changeset.New(h, changeset.Options{
		GetSession:       func(s string) (changeset.Session, bool) { return session, s == id },
		GetSessionByChat: func(c string) (changeset.Session, bool) { return session, c == chatChannel },
		HydrateSession:   func(_ context.Context, s string) (changeset.Session, bool, error) { return session, s == id, nil },
		Git:              git, Watch: watches.create, Debounce: 5 * time.Millisecond, StateGrace: 0,
	})
	defer service.Dispose()
	if err := service.Attach(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	h.Serve(host.Capabilities{Hydrator: service})
	rec := &recorder{}
	conn := h.Accept(rec)
	next := 0
	rpc := func(method string, params any) {
		next++
		frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": next, "method": method, "params": params})
		conn.Receive(frame)
	}
	notify := func(method string, params any) {
		frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
		conn.Receive(frame)
	}
	settle := func() { time.Sleep(50 * time.Millisecond) }
	channel := changeset.URI(id, changeset.Uncommitted)
	status := func() ahptypes.ChangesetStatus {
		if s := h.Store().Changeset(channel); s != nil {
			return s.Status
		}
		return ""
	}

	rpc("initialize", map[string]any{"channel": "ahp-root://", "clientId": "diff-client", "protocolVersions": []string{"0.9.0"}})
	settle()
	rpc("subscribe", map[string]any{"channel": sessionChannel})
	settle()
	rpc("subscribe", map[string]any{"channel": channel})
	eventually(t, "the first scan", func() bool { return status() == ahptypes.ChangesetStatusReady })
	settle()
	watches.current(t).changed(filepath.Join(workspace, "during-turn.ts"))
	eventually(t, "the refresh", func() bool { n, _ := git.calls(); return n >= 2 && status() == ahptypes.ChangesetStatusReady })
	settle()
	notify("dispatchAction", map[string]any{"channel": channel, "clientSeq": 1, "action": map[string]any{"type": "changeset/filesReviewChanged", "files": []string{"x"}, "reviewed": true}})
	settle()
	notify("unsubscribe", map[string]any{"channel": channel})
	eventually(t, "eviction", func() bool { return !h.Store().Has(channel) })
	settle()
	rpc("subscribe", map[string]any{"channel": "ahp-changeset:/" + id + "/unknown"})
	settle()
	rpc("ping", map[string]any{"channel": "ahp-root://"})
	settle()

	rec.mu.Lock()
	var got []any
	for _, f := range rec.frames {
		var v any
		_ = json.Unmarshal(f, &v)
		got = append(got, v)
	}
	rec.mu.Unlock()
	rawExpect, err := os.ReadFile(expectPath)
	if err != nil {
		t.Fatal(err)
	}
	var want []any
	if err := json.Unmarshal(rawExpect, &want); err != nil {
		t.Fatal(err)
	}
	normalize := func(frames []any) []any {
		raw, _ := json.Marshal(frames)
		text := strings.ReplaceAll(string(raw), workspace, "<WS>")
		text = strings.ReplaceAll(text, strings.TrimPrefix(wire.PathToFileURI(workspace), "file://"), "<WS>")
		var out []any
		_ = json.Unmarshal([]byte(text), &out)
		for _, f := range out {
			scrub(f)
		}
		return out
	}
	gotN, wantN := normalize(got), normalize(want)
	if out := os.Getenv("AHP_DIFF_OUT"); out != "" {
		raw, _ := json.MarshalIndent(map[string]any{"go": gotN, "oracle": wantN}, "", " ")
		_ = os.WriteFile(out, raw, 0o644)
	}
	if len(gotN) != len(wantN) {
		t.Errorf("the transcripts have %d (Go) and %d (oracle) frames", len(gotN), len(wantN))
	}
	for i := 0; i < len(gotN) && i < len(wantN); i++ {
		if !reflect.DeepEqual(gotN[i], wantN[i]) {
			g, _ := json.Marshal(gotN[i])
			w, _ := json.Marshal(wantN[i])
			t.Errorf("frame %d differs:\n go:     %s\n oracle: %s", i, g, w)
		}
	}
}

// scrub removes what legitimately differs between the two hosts: server identity, clocks and the
// optional-field spellings that carry no information.
func scrub(v any) {
	switch x := v.(type) {
	case map[string]any:
		delete(x, "serverInfo")
		for _, k := range []string{"modifiedAt", "createdAt", "startedAt", "_meta"} {
			delete(x, k)
		}
		for _, c := range x {
			scrub(c)
		}
	case []any:
		for _, c := range x {
			scrub(c)
		}
	}
}
