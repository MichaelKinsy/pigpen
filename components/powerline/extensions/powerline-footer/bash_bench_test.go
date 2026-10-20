package powerline_footer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Benchmarks of the paths bash mode runs while the user types: a ghost suggestion (debounced, off the input path) and the
// shell's output lines.

func BenchmarkBashGhostSuggestionProjectHistory(b *testing.B) {
	root := b.TempDir()
	b.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "agent"))
	b.Setenv("HISTFILE", filepath.Join(root, "none"))
	cwd := filepath.Join(root, "cwd")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		appendProjectHistory(cwd, "git commit -m message "+bashItoa(i), cwd)
	}
	engine := &bashCompletionEngine{}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.getGhostSuggestion(ctx, "git commit -m message 49", cwd, "/bin/zsh")
	}
}

func BenchmarkBashHandleChunk(b *testing.B) {
	store := newBashTranscriptStore(bashTranscriptLimits{MaxLines: 2000, MaxBytes: 512 * 1024})
	session, _ := newManagedShellSession("/bin/bash", "/w", store, func() {}, func(string, string) {})
	defer session.dispose()
	session.handleChunk("__PI_READY__:/w\n")
	store.startCommand("cmd-1", "yes", "/w")
	session.handleChunk("__PI_CMD_START__:cmd-1:/w\n")
	chunk := strings.Repeat("\x1b[32mline of command output\x1b[0m\n", 50)
	b.ReportAllocs()
	b.SetBytes(int64(len(chunk)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		session.handleChunk(chunk)
	}
}

func BenchmarkBashTranscriptAppend(b *testing.B) {
	store := newBashTranscriptStore(bashTranscriptLimits{MaxLines: 2000, MaxBytes: 512 * 1024})
	store.startCommand("a", "cmd", "/w")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		store.appendOutput("a", "a line of output")
	}
}
