//go:build unix && !aix && !solaris && !illumos

package audit

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

// A FIFO at the audit path that has a reader opens without error; the descriptor check refuses it.
func TestAFIFOAtTheAuditPathIsNotWritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SessionFileName)
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skip("mkfifo unavailable")
	}
	reader, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var stderr bytes.Buffer
	l, _ := Open("warden", &profile.Audit{Sink: profile.SinkSessionFile, Required: true}, dir, &stderr)
	if err := l.Record(Event{Event: EventLoad, Package: "warden", Outcome: OutcomeOK, Reason: ReasonNone}); !profile.IsCode(err, profile.AuditUnavailable) {
		t.Fatalf("%v", err)
	}
	buf := make([]byte, 64)
	if n, _ := reader.Read(buf); n != 0 {
		t.Fatalf("the FIFO received %q", buf[:n])
	}
}
