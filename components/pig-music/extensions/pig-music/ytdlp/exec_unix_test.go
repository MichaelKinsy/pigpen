//go:build !windows

package ytdlp

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A yt-dlp that times out is stopped with everything it started: a wrapper
// script's yt-dlp, or the JavaScript runtime yt-dlp runs for YouTube.
func TestATimedOutRunLeavesNoChildBehind(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child")
	child := func() int {
		data, _ := os.ReadFile(pidFile)
		n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		return n
	}
	t.Cleanup(func() {
		if pid := child(); pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := ExecRunner(ctx, "sh", []string{"-c", "sleep 30 & echo $! > " + pidFile + "; wait"})
	if err == nil {
		t.Fatal("a timed-out run succeeded")
	}
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Errorf("the run took %s after its deadline (its child held the output open)", took)
	}
	pid := child()
	if pid == 0 {
		t.Fatal("the child never started")
	}
	deadline := time.Now().Add(2 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the child %d of a timed-out yt-dlp is still running", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
