//go:build unix

package audit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

// A device (or any non-regular file) at the audit path is not written to, even though opening it succeeds.
func TestADeviceAtTheAuditPathIsNotWritten(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("/dev/null", filepath.Join(dir, SessionFileName)); err != nil {
		t.Skip("symlinks unavailable")
	}
	var stderr bytes.Buffer
	l, _ := Open("warden", &profile.Audit{Sink: profile.SinkSessionFile, Required: true}, dir, &stderr)
	if err := l.Record(Event{Event: EventLoad, Package: "warden", Outcome: OutcomeOK, Reason: ReasonNone}); !profile.IsCode(err, profile.AuditUnavailable) {
		t.Fatalf("%v", err)
	}
}

// A symbolic link planted at the audit path (the session directory is writable by the agent's tools) is not
// followed: the file it names is neither created nor appended to.
func TestASymlinkAtTheAuditPathIsNotFollowed(t *testing.T) {
	dir, elsewhere := t.TempDir(), t.TempDir()
	victim := filepath.Join(elsewhere, "auth.json")
	if err := os.WriteFile(victim, []byte(`{"p":{"type":"api_key","key":"k"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(elsewhere, "created.json")
	for _, target := range []string{victim, missing} {
		link := filepath.Join(dir, SessionFileName)
		_ = os.Remove(link)
		if err := os.Symlink(target, link); err != nil {
			t.Skip("symlinks unavailable")
		}
		var stderr bytes.Buffer
		l, _ := Open("warden", &profile.Audit{Sink: profile.SinkSessionFile, Required: true}, dir, &stderr)
		if err := l.Record(Event{Event: EventLoad, Package: "warden", Outcome: OutcomeOK, Reason: ReasonNone}); !profile.IsCode(err, profile.AuditUnavailable) {
			t.Fatalf("%s: %v", target, err)
		}
	}
	if b, _ := os.ReadFile(victim); string(b) != `{"p":{"type":"api_key","key":"k"}}` {
		t.Fatalf("the link target was written: %q", b)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("the link target was created")
	}
}
