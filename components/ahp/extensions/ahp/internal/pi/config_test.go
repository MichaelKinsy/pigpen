package pi_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of upstream test/project-trust.test.ts and test/session-config.test.ts.
//
// Project trust is a security default, not a convenience: Pi's SDK trusts a working directory
// unless told otherwise, and a host takes that directory from a client over the network.

func projectWithResources(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// A project-level extension is exactly the thing trust gates: loading it executes code from
	// the directory.
	if err := os.MkdirAll(filepath.Join(dir, ".pi", "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".pi", "extensions", "ext.ts"), []byte("export default () => {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func resolveTrust(t *testing.T, cwd string, policy pi.ProjectTrustPolicy, agentDir string) pi.TrustDecision {
	t.Helper()
	// An isolated HOME so a developer's own ~/.agents/skills cannot leak into the result.
	t.Setenv("HOME", t.TempDir())
	d, err := pi.ResolveProjectTrust(cwd, policy, agentDir)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestProjectTrust(t *testing.T) {
	bare, withResources := t.TempDir(), projectWithResources(t)
	agentDir := func() string { return t.TempDir() } // every policy case starts without a recorded decision
	tp := func(b bool) *bool { return &b }

	twin.Run(t, "project-trust", "trusts by default, matching raw pi SDK construction", func(t *testing.T) {
		if got := resolveTrust(t, withResources, "", agentDir()); got != (pi.TrustDecision{Trusted: true, Reason: "policy"}) {
			t.Fatalf("got %+v", got)
		}
	})

	twin.Run(t, "project-trust", "trusts a directory with nothing to gate", func(t *testing.T) {
		if got := resolveTrust(t, bare, pi.TrustPolicyInherit, agentDir()); got != (pi.TrustDecision{Trusted: true, Reason: "no-project-resources"}) {
			t.Fatalf("got %+v", got)
		}
	})

	twin.Run(t, "project-trust", "declines a project nobody has approved, under `inherit`", func(t *testing.T) {
		// The important case: the SDK's own default would return true here.
		if got := resolveTrust(t, withResources, pi.TrustPolicyInherit, agentDir()); got != (pi.TrustDecision{Trusted: false, Reason: "unknown-project"}) {
			t.Fatalf("got %+v", got)
		}
	})

	twin.Run(t, "project-trust", "inherits a decision the user made with pi's CLI", func(t *testing.T) {
		dir := agentDir()
		if err := pi.NewProjectTrustStore(dir).Set(withResources, tp(true)); err != nil {
			t.Fatal(err)
		}
		// Reusing Pi's store means trusting once covers both tools, and revoking in either
		// revokes in both.
		if got := resolveTrust(t, withResources, pi.TrustPolicyInherit, dir); got != (pi.TrustDecision{Trusted: true, Reason: "user-trusted"}) {
			t.Fatalf("got %+v", got)
		}
	})

	twin.Run(t, "project-trust", "honours an explicit distrust from pi's CLI", func(t *testing.T) {
		dir := agentDir()
		if err := pi.NewProjectTrustStore(dir).Set(withResources, tp(false)); err != nil {
			t.Fatal(err)
		}
		if got := resolveTrust(t, withResources, pi.TrustPolicyInherit, dir); got != (pi.TrustDecision{Trusted: false, Reason: "user-untrusted"}) {
			t.Fatalf("got %+v", got)
		}
	})

	twin.Run(t, "project-trust", "the `never` policy overrides a stored trust decision", func(t *testing.T) {
		dir := agentDir()
		if err := pi.NewProjectTrustStore(dir).Set(withResources, tp(true)); err != nil {
			t.Fatal(err)
		}
		if got := resolveTrust(t, withResources, pi.TrustPolicyNever, dir); got != (pi.TrustDecision{Trusted: false, Reason: "policy"}) {
			t.Fatalf("got %+v", got)
		}
	})

	twin.Skip(t, "project-trust", "applies the decision before pi loads project extensions",
		"needs an embedded Pi that loads project extensions from a client-chosen directory; this host serves the live PiG session only and never loads extensions itself (see PORT.md)")
}

func TestResolveSessionConfig(t *testing.T) {
	bare, withResources := t.TempDir(), projectWithResources(t)
	t.Setenv("HOME", t.TempDir())
	service := pi.NewSessionConfigService(pi.SessionConfigOptions{DefaultWorkingDirectory: bare, AgentDir: t.TempDir()})
	resolve := func(p ahptypes.ResolveSessionConfigParams) ahptypes.ResolveSessionConfigResult {
		t.Helper()
		r, err := service.Resolve(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	dir := func(path string) *string { u := wire.PathToFileURI(path); return &u }

	twin.Run(t, "session-config", "returns a schema-conforming result", func(t *testing.T) {
		testkit.AssertValid(t, "commands", "ResolveSessionConfigResult", resolve(ahptypes.ResolveSessionConfigParams{Channel: wire.RootChannel}))
	})

	twin.Run(t, "session-config", "reports what a directory with no project resources would do", func(t *testing.T) {
		result := resolve(ahptypes.ResolveSessionConfigParams{Channel: wire.RootChannel, WorkingDirectory: dir(bare)})
		if string(result.Values[pi.ProjectTrustKey]) != "true" {
			t.Fatalf("values = %v", result.Values)
		}
		if p := result.Schema.Properties[pi.ProjectTrustKey]; p.ReadOnly == nil || !*p.ReadOnly {
			t.Fatalf("property = %+v", p)
		}
	})

	twin.Run(t, "session-config", "re-resolves against whatever directory the client is asking about", func(t *testing.T) {
		// The exchange is iterative: the answer changes as the user picks a directory, which is
		// the whole reason the command exists.
		a := resolve(ahptypes.ResolveSessionConfigParams{Channel: wire.RootChannel, WorkingDirectory: dir(bare)})
		b := resolve(ahptypes.ResolveSessionConfigParams{Channel: wire.RootChannel, WorkingDirectory: dir(withResources)})
		if *a.Schema.Properties[pi.ProjectTrustKey].Description == *b.Schema.Properties[pi.ProjectTrustKey].Description {
			t.Fatal("the description must change with the directory")
		}
	})

	twin.Run(t, "session-config", "offers no dynamic completions", func(t *testing.T) {
		result, err := service.Completions(context.Background(), ahptypes.SessionConfigCompletionsParams{Channel: wire.RootChannel, Property: pi.ProjectTrustKey})
		if err != nil || len(result.Items) != 0 {
			t.Fatalf("items = %v (%v)", result.Items, err)
		}
	})
}

// selectionBackend records the model selection each turn ran with.
type selectionBackend struct {
	*recordingBackend
	mu2        chan struct{}
	selections []ahptypes.ModelSelection
	current    ahptypes.ModelSelection
}

func newSelectionBackend() *selectionBackend {
	return &selectionBackend{
		recordingBackend: &recordingBackend{}, mu2: make(chan struct{}, 1),
		current: ahptypes.ModelSelection{Id: "default-model", Config: map[string]json.RawMessage{pi.ThinkingConfigKey: json.RawMessage(`"medium"`)}},
	}
}

func (b *selectionBackend) SelectModel(_ context.Context, s ahptypes.ModelSelection) error {
	b.recordingBackend.mu.Lock()
	defer b.recordingBackend.mu.Unlock()
	b.selections = append(b.selections, s)
	b.current = s
	return nil
}

func (b *selectionBackend) CurrentSelection() *ahptypes.ModelSelection {
	b.recordingBackend.mu.Lock()
	defer b.recordingBackend.mu.Unlock()
	c := b.current
	return &c
}

func (b *selectionBackend) selectionCount() int {
	b.recordingBackend.mu.Lock()
	defer b.recordingBackend.mu.Unlock()
	return len(b.selections)
}

func TestModelSelection(t *testing.T) {
	backend := newSelectionBackend()
	h := testkit.NewHost(host.Options{})
	reg := pi.NewRegistry(pi.RegistryOptions{
		Host: h, CreateSessionManager: pi.InMemoryStorage,
		CreateBackend: func(*pi.LiveSession) (pi.Backend, error) { return backend, nil },
		// The backend is authoritative once it starts, including config values.
		DefaultSelection: func() *ahptypes.ModelSelection {
			return &ahptypes.ModelSelection{Id: "default-model", Config: map[string]json.RawMessage{pi.ThinkingConfigKey: json.RawMessage(`"low"`)}}
		},
	})
	h.Serve(host.Capabilities{Sessions: reg})
	client := testkit.Connect(t, h)
	t.Cleanup(client.Close)
	client.Initialize("model-client", nil)
	id := newID()
	chat := wire.ChatURI(id)
	client.Must("createSession", obj{"channel": wire.SessionURI(id)})
	client.Subscribe(chat)
	draft := func() *ahptypes.Message { return h.Store().Chat(chat).Draft }
	testkit.Eventually(t, "the backend model to reach the chat draft", func() bool {
		d := draft()
		return d != nil && d.Model != nil && d.Model.Id == "default-model" && string(d.Model.Config[pi.ThinkingConfigKey]) == `"medium"`
	})

	twin.Run(t, "session-config", "has a model selected before the agent has even started", func(t *testing.T) {
		// Starting an agent takes seconds. A client that subscribes in the meantime would
		// otherwise find an empty picker and be unable to send.
		h2 := testkit.NewHost(host.Options{})
		registry := pi.NewRegistry(pi.RegistryOptions{
			Host: h2, CreateSessionManager: pi.InMemoryStorage,
			DefaultSelection: func() *ahptypes.ModelSelection {
				return &ahptypes.ModelSelection{Id: "seeded-model", Config: map[string]json.RawMessage{pi.ThinkingConfigKey: json.RawMessage(`"medium"`)}}
			},
		})
		id := newID()
		if err := registry.Create(context.Background(), ahptypes.CreateSessionParams{Channel: wire.SessionURI(id)}); err != nil {
			t.Fatal(err)
		}
		if d := h2.Store().Chat(wire.ChatURI(id)).Draft; d == nil || d.Model == nil || d.Model.Id != "seeded-model" {
			t.Fatalf("draft = %+v", d)
		}
	})

	twin.Run(t, "session-config", "publishes the model and config actually in effect as the chat draft", func(t *testing.T) {
		// There is no protocol field for "the default model"; a client initialises its input from
		// draft, so this is how a host answers. The backend must also correct a stale config for
		// the same model id.
		d := draft()
		if d.Model.Id != "default-model" || string(d.Model.Config[pi.ThinkingConfigKey]) != `"medium"` {
			t.Fatalf("draft model = %+v", d.Model)
		}
	})

	twin.Run(t, "session-config", "applies the model a client picked, before the prompt runs", func(t *testing.T) {
		promptsBefore, selectionsBefore := len(backend.promptList()), backend.selectionCount()
		message := userMessage("hi")
		message["model"] = obj{"id": "picked-model", "config": obj{pi.ThinkingConfigKey: "high"}}
		client.Dispatch(chat, obj{"type": "chat/turnStarted", "turnId": "t-model", "startedAt": "2025-01-01T00:00:00.000Z", "message": message})
		testkit.Eventually(t, "the prompt to reach the backend", func() bool { return len(backend.promptList()) == promptsBefore+1 })
		if backend.selectionCount() != selectionsBefore+1 {
			t.Fatalf("selections = %d, want %d", backend.selectionCount(), selectionsBefore+1)
		}
		backend.recordingBackend.mu.Lock()
		last := backend.selections[len(backend.selections)-1]
		backend.recordingBackend.mu.Unlock()
		sameJSON(t, last, obj{"id": "picked-model", "config": obj{pi.ThinkingConfigKey: "high"}}, "selection")
		testkit.Eventually(t, "the turn to settle", func() bool { return h.Store().Chat(chat).ActiveTurn == nil })
	})

	twin.Run(t, "session-config", "runs on the current model when the message carries no selection", func(t *testing.T) {
		selectionsBefore, promptsBefore := backend.selectionCount(), len(backend.promptList())
		client.Dispatch(chat, obj{"type": "chat/turnStarted", "turnId": "t-plain", "startedAt": "2025-01-01T00:00:00.000Z", "message": userMessage("again")})
		testkit.Eventually(t, "the prompt to reach the backend", func() bool { return len(backend.promptList()) == promptsBefore+1 })
		if backend.selectionCount() != selectionsBefore {
			t.Fatal("no selection means no switch")
		}
	})
}
