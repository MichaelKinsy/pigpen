package tintinweb_subagents

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Twins of test/worktree-isolation-e2e.test.ts: `isolation: "worktree"` through the Agent tool, the manager, a real
// git repository and a scripted child. Adaptation: the child is a fake that writes the marker file into the directory
// it was started in (the original's child writes it with its bash tool); everything else is real.
// upstream: src/agent-manager.ts startAgent, src/worktree.ts.

const (
	e2eMarkerFile  = "agent-work.txt"
	e2eChildMarker = "CHILD-EDITED-ITS-TREE"
)

// markerChild starts children that create the marker file in their cwd and answer with e2eChildMarker.
func markerChild(r *rig) {
	r.app.mgr.start = func(_ context.Context, spec childSpec) (child, error) {
		if err := os.WriteFile(filepath.Join(spec.Cwd, e2eMarkerFile), []byte("isolated\n"), 0o666); err != nil {
			return nil, err
		}
		c := &fakeChild{spec: spec, release: make(chan struct{})}
		r.fleet.mu.Lock()
		r.fleet.children = append(r.fleet.children, c)
		r.fleet.mu.Unlock()
		c.finish(e2eChildMarker)
		return c, nil
	}
}

func TestWorktreeIsolationE2ETwin(t *testing.T) {
	const f = "worktree-isolation-e2e"
	tw(t, f, "runs the child in the copy and lands its changes on a branch, not the main checkout", func(t *testing.T) {
		repo := initGitRepo(t)
		r := startRigIn(t, repo)
		markerChild(r)
		text := r.must("Agent", obj{"prompt": "Create the marker file.", "description": "worktree work", "subagent_type": "general-purpose", "run_in_background": false, "isolation": "worktree"})
		eq(t, exists(filepath.Join(repo, e2eMarkerFile)), false)
		if !strings.Contains(text, e2eChildMarker) {
			t.Fatal(text)
		}
		m := regexp.MustCompile("Changes saved to branch `(pi-agent-[^`]+)`").FindStringSubmatch(text)
		if m == nil {
			t.Fatalf("no branch note in %q", text)
		}
		branch := m[1]
		if !strings.Contains(text, "git merge "+branch) {
			t.Fatal(text)
		}
		if !strings.Contains(gitOut(t, repo, "branch", "--list", branch), branch) || !strings.Contains(gitOut(t, repo, "ls-tree", "--name-only", branch), e2eMarkerFile) {
			t.Fatal("branch missing or without the child's file")
		}
		eq(t, len(strings.Split(gitOut(t, repo, "worktree", "list"), "\n")), 1)
		eq(t, strings.Contains(r.fleet.children[0].spec.SystemPrompt, "<worktree_isolation>"), true)
	})
	tw(t, f, "downgrades to the main checkout when the project set worktreeIsolation: false", func(t *testing.T) {
		repo := initGitRepo(t)
		mustMkdir(t, filepath.Join(repo, ".pi"))
		mustWrite(t, filepath.Join(repo, ".pi", "subagents.json"), `{"worktreeIsolation": false}`)
		t.Cleanup(func() { setWorktreeIsolationEnabled(true) })
		r := startRigIn(t, repo)
		markerChild(r)
		text := r.must("Agent", obj{"prompt": "Create the marker file.", "description": "worktree work", "subagent_type": "general-purpose", "run_in_background": false, "isolation": "worktree"})
		if !strings.Contains(text, e2eChildMarker) {
			t.Fatal(text)
		}
		eq(t, exists(filepath.Join(repo, e2eMarkerFile)), true)
		eq(t, gitOut(t, repo, "branch", "--list", "pi-agent-*"), "")
		eq(t, len(strings.Split(gitOut(t, repo, "worktree", "list"), "\n")), 1)
		eq(t, strings.Contains(text, "Changes saved to branch"), false)
	})
}
