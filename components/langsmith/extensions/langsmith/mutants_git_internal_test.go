package langsmith

// Internal test added by the mutation proof (port/mutations.md): the git invocations of getGitInfo.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMutGitInvocationsAndOrder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the recording git is a shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\necho \"$@\" >> \"" + log + "\"\necho x\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	getGitInfo(context.Background(), t.TempDir())
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "config --get remote.origin.url\nrev-parse --abbrev-ref HEAD\nrev-parse HEAD\n"
	if string(data) != want {
		t.Fatalf("git invocations:\n%s\nwant:\n%s", strings.TrimSpace(string(data)), strings.TrimSpace(want))
	}
}
