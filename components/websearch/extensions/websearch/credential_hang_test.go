//go:build unix

package websearch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The credential command is run through `sh -c`. The deadline kills the shell, so a child the
// shell left holding stdout used to block the run for as long as that child lived. The original
// (child_process.exec with a timeout) returns at its timeout; a web search must not hang on a
// credential helper (gpg-agent, op, a pipeline).

func runWithin(t *testing.T, limit time.Duration, command string, timeout time.Duration) (string, time.Duration, error) {
	t.Helper()
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		out, err := defaultRunCommand(bg(), command, CredentialCommandOptions{Timeout: timeout, MaxOutputBytes: 1024})
		done <- result{out, err}
	}()
	select {
	case r := <-done:
		return r.out, time.Since(start), r.err
	case <-time.After(limit):
		t.Fatalf("defaultRunCommand(%q) still blocked after %v (its timeout is %v)", command, limit, timeout)
		return "", 0, nil
	}
}

func TestCredentialCommandTimesOutWhileAChildHoldsStdout(t *testing.T) {
	_, took, err := runWithin(t, 4*time.Second, "sleep 30 | cat", 300*time.Millisecond)
	var ce *CommandError
	if !errors.As(err, &ce) || ce.Code != "ETIMEDOUT" || !ce.Killed {
		t.Fatalf("want the ETIMEDOUT CommandError, got %v", err)
	}
	if took > 3*time.Second {
		t.Fatalf("returned after %v, want about the 300ms timeout", took)
	}
}

func TestCredentialCommandSucceedsWhenABackgroundChildKeepsStdoutOpen(t *testing.T) {
	// A daemon started by the helper inherits stdout and outlives it.
	out, took, err := runWithin(t, 4*time.Second, "sleep 30 & echo synthetic-key", 5*time.Second)
	if err != nil || strings.TrimSpace(out) != "synthetic-key" {
		t.Fatalf("got %q, %v; want the key the command printed", out, err)
	}
	if took > 3*time.Second {
		t.Fatalf("returned after %v, want within the pipe grace period", took)
	}
}

func TestCredentialCommandTimeoutKillsTheWholeProcessGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "survivor")
	// The subshell would create the marker if it outlived the timeout.
	command := "(sleep 1.5; touch " + marker + ") & wait"
	_, _, err := runWithin(t, 4*time.Second, command, 200*time.Millisecond)
	if err == nil {
		t.Fatal("want a timeout error")
	}
	time.Sleep(2500 * time.Millisecond)
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("a child of the credential command survived its timeout")
	}
}
