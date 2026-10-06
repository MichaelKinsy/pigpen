//go:build unix

package safefile

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadFIFODoesNotBlockAndIsRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Skip("mkfifo unavailable")
	}
	done := make(chan error, 1)
	go func() { _, err := Read(p, 16); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotRegular) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read of a FIFO blocked")
	}
}

func TestReadDeviceIsRejected(t *testing.T) {
	if _, err := Read("/dev/zero", 16); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("got %v", err)
	}
}
