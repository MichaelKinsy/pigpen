//go:build unix

package credfile

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

func TestFIFOAtThePathFailsWithoutHanging(t *testing.T) {
	f := newFixture(t)
	if err := syscall.Mkfifo(f.path, 0o600); err != nil {
		t.Skip("mkfifo unavailable")
	}
	done := make(chan error, 1)
	go func() { _, err := f.source().Token(context.Background()); done <- err }()
	select {
	case err := <-done:
		wantCode(t, err, profile.CredentialUnavailable)
	case <-time.After(5 * time.Second):
		t.Fatal("a FIFO blocked the read")
	}
}

func TestDeviceAtThePathFailsWithoutReading(t *testing.T) {
	f := newFixture(t)
	if _, err := os.Stat("/dev/zero"); err != nil {
		t.Skip("no /dev/zero")
	}
	if err := os.Symlink("/dev/zero", f.path); err != nil {
		t.Skip("symlinks unavailable")
	}
	_, err := f.source().Token(context.Background())
	wantCode(t, err, profile.CredentialUnavailable)
}
