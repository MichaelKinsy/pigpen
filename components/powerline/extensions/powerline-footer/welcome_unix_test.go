//go:build unix

package powerline_footer

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Only regular files are session files: opening a named pipe that ends in .jsonl would block discovery for good. The original
// asks stats.isFile().
func TestWelcomeSkipsEntriesThatAreNotRegularFiles(t *testing.T) {
	home := withWelcomeHome(t)
	root := filepath.Join(home, ".pi", "agent", "sessions")
	writeFile(t, filepath.Join(root, "real", "ok.jsonl"), `{"cwd":"/projects/real"}`+"\n")
	if err := syscall.Mkfifo(filepath.Join(root, "real", "pipe.jsonl"), 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	done := make(chan []recentSession, 1)
	go func() {
		got, _ := getRecentSessions(context.Background(), 3)
		done <- got
	}()
	select {
	case got := <-done:
		eq(t, got, []recentSession{{"real", "just now"}}, "sessions")
	case <-time.After(5 * time.Second):
		t.Fatal("discovery blocked on a named pipe")
	}
}
