package ahp

// Review (rev-pigpen-ahp): the capability wiring of the runtime was only exercised by the
// real-pig scenarios, which are skipped unless PIGPEN_AHP_REAL=1, and those did not notice a
// default root wider than the working directory. These unit tests pin it without a pig.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/compose"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/settings"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

func TestNoCapabilityUnlessTheSettingsGrantIt(t *testing.T) {
	fs, terminals := accessOptions(settings.Access{}, "/work")
	if fs != nil || terminals != nil {
		t.Fatalf("an empty Access granted filesystem=%+v terminals=%+v", fs, terminals)
	}
}

func TestFilesystemDefaultsToTheWorkingDirectoryOnly(t *testing.T) {
	var a settings.Access
	a.Filesystem.Enabled = true
	fs, terminals := accessOptions(a, "/work")
	if terminals != nil {
		t.Fatal("enabling the filesystem must not enable terminals")
	}
	if fs == nil || fs.Unrestricted || len(fs.Roots) != 1 || fs.Roots[0] != "/work" {
		t.Fatalf("filesystem without roots must be confined to the working directory, got %+v", fs)
	}

	a.Filesystem.Roots = []string{"/a", "/b"}
	if fs, _ := accessOptions(a, "/work"); fs == nil || fs.Unrestricted || len(fs.Roots) != 2 || fs.Roots[0] != "/a" || fs.Roots[1] != "/b" {
		t.Fatalf("configured roots must be used as given, got %+v", fs)
	}

	a.Filesystem.Roots, a.Filesystem.Unrestricted = nil, true
	if fs, _ := accessOptions(a, "/work"); fs == nil || !fs.Unrestricted {
		t.Fatalf("unrestricted must be passed on only when stated, got %+v", fs)
	}
}

func TestTerminalsOnlyWhenEnabled(t *testing.T) {
	var a settings.Access
	a.Terminals.Enabled = true
	fs, terminals := accessOptions(a, "/work")
	if fs != nil || terminals == nil {
		t.Fatalf("terminals enabled alone: filesystem=%+v terminals=%+v", fs, terminals)
	}
}

// The default root, composed for real: a sibling of the working directory is refused.
func TestDefaultRootRefusesASiblingOfTheWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	work, sibling := filepath.Join(root, "work"), filepath.Join(root, "secret.txt")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{filepath.Join(work, "in.txt"): "inside", sibling: "outside"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var a settings.Access
	a.Filesystem.Enabled = true
	fs, terminals := accessOptions(a, work)
	if fs == nil {
		t.Fatal("filesystem enabled but not composed")
	}
	built, err := compose.Build(compose.Options{
		WorkingDirectory: work, Filesystem: fs, Terminals: terminals,
		CreateBackend: func(*pi.LiveSession) (pi.Backend, error) { return nil, os.ErrInvalid },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()
	read := func(path string) error {
		_, err := built.Resources.Read(context.Background(), ahptypes.ResourceReadParams{Uri: wire.PathToFileURI(path)})
		return err
	}
	if err := read(filepath.Join(work, "in.txt")); err != nil {
		t.Fatalf("a file in the working directory must be readable: %v", err)
	}
	err = read(sibling)
	var e *wire.Error
	if !errors.As(err, &e) || e.Code != wire.CodePermissionDenied {
		t.Fatalf("a sibling of the working directory must be refused with PermissionDenied, got %v", err)
	}
}

func TestReplacementNotice(t *testing.T) {
	// the listener is closed with the replaced session; say so unless --ahp brings it back
	for _, reason := range []string{"new", "resume", "fork", "reload"} {
		if n := replacementNotice(reason, true, false); n == "" {
			t.Errorf("reason %q: a listener started with /ahp start closes silently", reason)
		}
		if n := replacementNotice(reason, true, true); n != "" {
			t.Errorf("reason %q: --ahp restarts the listener, but got notice %q", reason, n)
		}
		if n := replacementNotice(reason, false, false); n != "" {
			t.Errorf("reason %q: nothing was running, but got notice %q", reason, n)
		}
	}
	if n := replacementNotice("quit", true, false); n != "" {
		t.Errorf("quitting needs no notice, got %q", n)
	}
}
