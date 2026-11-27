package commit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// upstream: packages/coding-agent/src/slash-commands/builtin-commit.ts:31-34
func TestLogSubjects(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if _, err := runGit(context.Background(), dir, nil, nil, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test User")

	// Unborn repo returns error (no commits yet) — caller ignores and treats as empty.
	if _, err := LogSubjects(context.Background(), dir, 5); err == nil {
		t.Fatalf("expected error on unborn repo")
	}

	// Zero / negative limit returns nil, nil without running git.
	if subs, err := LogSubjects(context.Background(), dir, 0); err != nil || len(subs) != 0 {
		t.Fatalf("LogSubjects(0) = (%v, %v), want (nil, nil)", subs, err)
	}

	for i, msg := range []string{"feat: one", "fix: two", "chore: three", "docs: four", "test: five", "refactor: six"} {
		p := filepath.Join(dir, "f.txt")
		if err := os.WriteFile(p, []byte(msg), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", "f.txt")
		run("commit", "-m", msg)
		_ = i
	}

	got, err := LogSubjects(context.Background(), dir, 5)
	if err != nil {
		t.Fatalf("LogSubjects: %v", err)
	}
	want := []string{"refactor: six", "test: five", "docs: four", "chore: three", "fix: two"}
	if len(got) != len(want) {
		t.Fatalf("got %d subjects %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("subject[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
