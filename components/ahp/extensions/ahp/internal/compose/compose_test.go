package compose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

type idleBackend struct{}

func (idleBackend) Subscribe(func(mapper.Event)) func()                  { return func() {} }
func (idleBackend) Prompt(context.Context, string, []mapper.Image) error { return nil }
func (idleBackend) Steer(context.Context, string, []mapper.Image) error  { return nil }
func (idleBackend) Abort(context.Context) error                          { return nil }

func TestComposition(t *testing.T) {
	// Twin of upstream test/pi-host.test.ts. Upstream's resource read works without roots; here the
	// unrestricted filesystem must be asked for explicitly (see PORT.md).
	twin.Run(t, "pi-host", "wires product services through createPiHost", func(t *testing.T) {
		workspace, _ := filepath.EvalSymlinks(t.TempDir())
		sessionRoot := t.TempDir()
		if err := os.WriteFile(filepath.Join(workspace, "notes.md"), []byte("# Notes\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		built, err := Build(Options{
			ServerInfo: &ahptypes.Implementation{Name: "pi-ahp"}, WorkingDirectory: workspace, SessionRoot: sessionRoot,
			CreateSessionManager: pi.PersistentStorage(sessionRoot),
			CreateBackend:        func(*pi.LiveSession) (pi.Backend, error) { return idleBackend{}, nil },
			DeleteFile:           func(string) (pi.SessionFileDeletionResult, error) { return pi.SessionFileDeletionResult{OK: true}, nil },
			Filesystem:           &FilesystemOptions{Unrestricted: true},
			Terminals:            &TerminalOptions{},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer built.Close()
		c := testkit.Connect(t, built.Host)
		defer c.Close()
		init := c.Initialize("composition-client", map[string]any{"initialSubscriptions": []string{wire.RootChannel}})
		if !reflect.DeepEqual(init.CompletionTriggerCharacters, []string{"@"}) {
			t.Fatalf("trigger characters %v", init.CompletionTriggerCharacters)
		}

		var resource ahptypes.ResourceReadResult
		c.Decode(c.Must("resourceRead", map[string]any{"channel": wire.RootChannel, "uri": wire.PathToFileURI(filepath.Join(workspace, "notes.md"))}), &resource)
		if resource.Data != "# Notes\n" {
			t.Fatalf("%+v", resource)
		}

		var config struct {
			Schema struct{ Properties map[string]any }
		}
		c.Decode(c.Must("resolveSessionConfig", map[string]any{"channel": wire.RootChannel, "workingDirectory": wire.PathToFileURI(workspace)}), &config)
		if config.Schema.Properties[pi.ProjectTrustKey] == nil {
			t.Fatalf("no project trust property in %v", config.Schema.Properties)
		}

		id := "0a3c1e52-6b7d-4f3a-9d10-000000000001"
		session, chat := wire.SessionURI(id), wire.ChatURI(id)
		c.Must("createSession", map[string]any{"channel": session})
		c.Must("subscribe", map[string]any{"channel": chat})
		testkit.Eventually(t, "the composed session backend to become ready", func() bool {
			s := built.Host.Store().Session(session)
			return s != nil && s.Lifecycle == ahptypes.SessionLifecycleReady
		})

		var completions ahptypes.CompletionsResult
		c.Decode(c.Must("completions", map[string]any{"channel": chat, "kind": "userMessage", "text": "see @not", "offset": 8}), &completions)
		var inserted []string
		for _, item := range completions.Items {
			inserted = append(inserted, item.InsertText)
		}
		if !reflect.DeepEqual(inserted, []string{"@notes.md"}) {
			raw, _ := json.Marshal(completions)
			t.Fatalf("completions %s", raw)
		}
	})
}

func TestNothingIsExposedByDefault(t *testing.T) {
	// Additions: fs and terminals are opt-in, and an unconfined filesystem must be explicit.
	built, err := Build(Options{WorkingDirectory: t.TempDir(), CreateBackend: func(*pi.LiveSession) (pi.Backend, error) { return idleBackend{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()
	if built.Resources != nil || built.Watches != nil || built.Terminals != nil {
		t.Fatal("no filesystem or terminal service may exist without opt-in")
	}
	c := testkit.Connect(t, built.Host)
	defer c.Close()
	c.Initialize("nothing", nil)
	c.ExpectError("resourceRead", map[string]any{"channel": wire.RootChannel, "uri": "file:///etc/hostname"}, wire.CodeMethodNotFound)
	c.ExpectError("createTerminal", map[string]any{"channel": "ahp-terminal:/x", "claim": map[string]any{"kind": "client", "clientId": "nothing"}}, wire.CodeMethodNotFound)
	c.ExpectError("createResourceWatch", map[string]any{"channel": wire.RootChannel, "uri": "file:///tmp"}, wire.CodeMethodNotFound)

	if _, err := Build(Options{CreateBackend: func(*pi.LiveSession) (pi.Backend, error) { return idleBackend{}, nil }, Filesystem: &FilesystemOptions{}}); err == nil {
		t.Fatal("a filesystem with no roots and no explicit Unrestricted must be refused")
	}
	confined, err := Build(Options{WorkingDirectory: t.TempDir(), CreateBackend: func(*pi.LiveSession) (pi.Backend, error) { return idleBackend{}, nil }, Filesystem: &FilesystemOptions{Roots: []string{t.TempDir()}}})
	if err != nil {
		t.Fatal(err)
	}
	defer confined.Close()
	cc := testkit.Connect(t, confined.Host)
	defer cc.Close()
	cc.Initialize("confined", nil)
	cc.ExpectError("resourceRead", map[string]any{"channel": wire.RootChannel, "uri": "file:///etc/hostname"}, wire.CodePermissionDenied)
}
