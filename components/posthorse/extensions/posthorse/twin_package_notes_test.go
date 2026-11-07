package posthorse

// Twins of test/package-notes.test.ts: notesDir against real Git repositories, worktrees and symlinks.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// pnDir is `realpathSync(mkdtempSync(...))`.
func pnDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// pnAgent points sdk.AgentDir (Pi's getAgentDir) at agent: the host hands it to the extension in PIG_EXT_HOST_INFO.
func pnAgent(t *testing.T, agent string) {
	t.Helper()
	info, err := json.Marshal(map[string]string{"agentDir": agent, "configDirName": ".pi"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_EXT_HOST_INFO", string(info))
}

func pnMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func pnWrite(t *testing.T, path, content string) {
	t.Helper()
	pnMust(t, os.WriteFile(path, []byte(content), 0o644))
}

func pnRead(t *testing.T, path ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(path...))
	pnMust(t, err)
	return string(data)
}

func pnMkdir(t *testing.T, path ...string) string {
	t.Helper()
	dir := filepath.Join(path...)
	pnMust(t, os.MkdirAll(dir, 0o755))
	return dir
}

func pnExists(path ...string) bool {
	_, err := os.Stat(filepath.Join(path...))
	return err == nil
}

func pnLstat(t *testing.T, path ...string) os.FileInfo {
	t.Helper()
	info, err := os.Lstat(filepath.Join(path...))
	pnMust(t, err)
	return info
}

func pnEqual(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

// pnNotes is `notesDir(cwd)` that must not throw.
func pnNotes(t *testing.T, cwd string) string {
	t.Helper()
	dir, err := notesDir(cwd)
	pnMust(t, err)
	return dir
}

// pnStore is `projectStore(project, identity)` (package-notes.test.ts:22-30).
func pnStore(project string, identity ...string) string {
	id := "github.com/acme/tool"
	if len(identity) > 0 {
		id = identity[0]
	}
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(project, ".pi", "package-notes", hex.EncodeToString(sum[:]), "notes")
}

// pnGit is `git(cwd, ...args)` (package-notes.test.ts:31-33).
func pnGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// pnCheckout is `packageCheckout(project)` (package-notes.test.ts:34-51).
func pnCheckout(t *testing.T, project string) string {
	t.Helper()
	checkout := filepath.Join(project, ".pi", "git", "github.com", "acme", "tool")
	pnMkdir(t, checkout, "src")
	pnGit(t, checkout, "init", "-q")
	pnWrite(t, filepath.Join(checkout, "tracked.txt"), "package source")
	pnGit(t, checkout, "add", "tracked.txt")
	pnGit(t, checkout, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "initial")
	return checkout
}

// pnLegacy is `writeLegacy(checkout, content)` (package-notes.test.ts:52-57).
func pnLegacy(t *testing.T, checkout, content string) string {
	t.Helper()
	legacy := filepath.Join(checkout, ".pi", "notes")
	pnMkdir(t, legacy, "task")
	pnWrite(t, filepath.Join(legacy, "task", "state.md"), content)
	return legacy
}

// pnUpdate is `updateCheckout(checkout)`: the operations Pi's package manager runs when the selected commit changes.
func pnUpdate(t *testing.T, checkout string) {
	t.Helper()
	pnGit(t, checkout, "reset", "--hard", "HEAD")
	pnGit(t, checkout, "clean", "-fdx")
}

// pnSetup isolates the host's agent directory and returns a fresh real temporary directory.
func pnSetup(t *testing.T) string {
	t.Helper()
	pnAgent(t, filepath.Join(pnDir(t), "agent"))
	return pnDir(t)
}

func TestPackageNotesTwins(t *testing.T) {
	tw(t, "package-notes", "project package notes migrate outside the checkout and survive Pi's update cleanup", func(t *testing.T) {
		project := pnSetup(t)
		checkout := pnCheckout(t, project)
		legacy := pnLegacy(t, checkout, "before migration")
		moved := pnStore(project)
		pnEqual(t, pnNotes(t, filepath.Join(checkout, "src")), moved, "notesDir(src)")
		pnEqual(t, pnRead(t, moved, "task", "state.md"), "before migration", "migrated note")
		pnEqual(t, pnLstat(t, legacy).Mode()&os.ModeSymlink != 0, true, "legacy is a link")
		pnWrite(t, filepath.Join(legacy, "task", "older-session.md"), "through compatibility link")
		pnUpdate(t, checkout)
		pnEqual(t, pnExists(legacy), false, "legacy gone after cleanup")
		pnEqual(t, pnNotes(t, checkout), moved, "notesDir(checkout)")
		pnEqual(t, pnRead(t, moved, "task", "state.md"), "before migration", "kept note")
		pnEqual(t, pnRead(t, moved, "task", "older-session.md"), "through compatibility link", "link-written note")
		pnEqual(t, pnRead(t, checkout, "tracked.txt"), "package source", "package source")
	})

	tw(t, "package-notes", "a failed compatibility link preserves migrated notes and the conflicting entry", func(t *testing.T) {
		project := pnSetup(t)
		checkout := pnCheckout(t, project)
		legacy := pnLegacy(t, checkout, "migrated without link")
		moved := pnStore(project)
		// package-notes.test.ts:95-106: the rename succeeds, then a file takes the old name.
		original := renameSync
		t.Cleanup(func() { renameSync = original })
		renameSync = func(oldpath, newpath string) error {
			err := original(oldpath, newpath)
			if oldpath == legacy {
				pnWrite(t, legacy, "conflicting compatibility entry")
			}
			return err
		}
		pnEqual(t, pnNotes(t, checkout), moved, "notesDir(checkout)")
		pnEqual(t, pnRead(t, legacy), "conflicting compatibility entry", "conflicting entry")
		pnEqual(t, pnRead(t, moved, "task", "state.md"), "migrated without link", "migrated note")
		pnUpdate(t, checkout)
		pnEqual(t, pnNotes(t, checkout), moved, "notesDir after update")
		pnEqual(t, pnRead(t, moved, "task", "state.md"), "migrated without link", "note after update")
	})

	tw(t, "package-notes", "fresh project package notes are isolated by project, including symlinked project paths", func(t *testing.T) {
		dir := pnSetup(t)
		first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
		firstCheckout, secondCheckout := pnCheckout(t, first), pnCheckout(t, second)
		pnMust(t, os.Symlink(first, filepath.Join(dir, "alias")))
		moved := pnNotes(t, firstCheckout)
		pnEqual(t, moved, pnStore(first), "first store")
		pnEqual(t, pnNotes(t, filepath.Join(dir, "alias", ".pi", "git", "github.com", "acme", "tool", "src")), moved, "alias store")
		if pnNotes(t, secondCheckout) == moved {
			t.Fatal("the second project shares the first project's store")
		}
		pnMkdir(t, moved)
		pnWrite(t, filepath.Join(moved, "fresh.md"), "new notes")
		pnUpdate(t, firstCheckout)
		pnEqual(t, pnRead(t, pnNotes(t, firstCheckout), "fresh.md"), "new notes", "fresh note")
		pnEqual(t, pnExists(pnNotes(t, secondCheckout), "fresh.md"), false, "isolated note")
		pnEqual(t, pnExists(firstCheckout, ".pi"), false, "no .pi in the checkout")
		pnEqual(t, pnNotes(t, first), filepath.Join(first, ".pi", "notes"), "plain project")
	})

	tw(t, "package-notes", "project package migration preserves existing notes and timestamp collision entries", func(t *testing.T) {
		project := pnSetup(t)
		original := nowMillis
		t.Cleanup(func() { nowMillis = original })
		nowMillis = func() int64 { return 1234 }
		checkout := pnCheckout(t, project)
		moved := pnStore(project)
		pnMkdir(t, moved, "task")
		pnWrite(t, filepath.Join(moved, "task", "state.md"), "canonical")
		pnMkdir(t, moved, "from-checkout-1234")
		pnMust(t, os.Symlink("missing", filepath.Join(moved, "from-checkout-1234-1")))
		legacy := pnLegacy(t, checkout, "older session one")
		pnEqual(t, pnNotes(t, checkout), moved, "notesDir(checkout)")
		pnEqual(t, pnRead(t, moved, "from-checkout-1234-2", "task", "state.md"), "older session one", "collision folder")
		entries, err := os.ReadDir(filepath.Join(moved, "from-checkout-1234"))
		pnMust(t, err)
		pnEqual(t, len(entries), 0, "existing empty folder untouched")
		pnEqual(t, pnLstat(t, moved, "from-checkout-1234-1").Mode()&os.ModeSymlink != 0, true, "dangling link kept")
		pnUpdate(t, checkout)
		pnLegacy(t, checkout, "older session two")
		pnEqual(t, pnNotes(t, checkout), moved, "notesDir after update")
		pnEqual(t, pnRead(t, moved, "from-checkout-1234-3", "task", "state.md"), "older session two", "second collision folder")
		pnEqual(t, pnRead(t, legacy, "task", "state.md"), "canonical", "canonical through the link")
	})

	tw(t, "package-notes", "package storage detection leaves containers and similarly named ordinary repositories alone", func(t *testing.T) {
		project := pnSetup(t)
		pnCheckout(t, project)
		owner := filepath.Join(project, ".pi", "git", "github.com", "acme")
		pnEqual(t, pnNotes(t, owner), filepath.Join(owner, ".pi", "notes"), "container")
		ordinary := pnMkdir(t, project, ".pi", "git-other", "github.com", "acme", "tool")
		pnGit(t, ordinary, "init", "-q")
		pnEqual(t, pnNotes(t, ordinary), filepath.Join(ordinary, ".pi", "notes"), "ordinary repository")
	})

	tw(t, "package-notes", "linked project-package worktrees share relocated notes outside the outermost package tree", func(t *testing.T) {
		project := pnSetup(t)
		checkout := pnCheckout(t, project)
		worktree := filepath.Join(project, "worktree")
		pnGit(t, checkout, "worktree", "add", "--detach", worktree)
		pnEqual(t, pnNotes(t, worktree), pnNotes(t, checkout), "worktree shares notes")
		nested := pnCheckout(t, checkout)
		legacy := pnLegacy(t, nested, "nested package notes")
		moved := pnStore(project, "github.com/acme/tool/.pi/git/github.com/acme/tool")
		pnEqual(t, pnNotes(t, nested), moved, "nested package store")
		pnEqual(t, pnLstat(t, legacy).Mode()&os.ModeSymlink != 0, true, "legacy is a link")
		pnUpdate(t, nested)
		pnEqual(t, pnRead(t, moved, "task", "state.md"), "nested package notes", "nested note")
	})

	tw(t, "package-notes", "a blocked migration fails without modifying checkout notes or the destination", func(t *testing.T) {
		project := pnSetup(t)
		checkout := pnCheckout(t, project)
		legacy := pnLegacy(t, checkout, "still local")
		moved := pnStore(project)
		pnMkdir(t, filepath.Dir(moved))
		pnWrite(t, moved, "not a directory")
		if _, err := notesDir(checkout); err == nil {
			t.Fatal("a blocked migration must fail")
		}
		pnEqual(t, pnRead(t, legacy, "task", "state.md"), "still local", "checkout notes")
		pnEqual(t, pnRead(t, moved), "not a directory", "destination")
		pnEqual(t, pnLstat(t, legacy).IsDir(), true, "legacy stays a directory")
	})

	tw(t, "package-notes", "project package notes survive cleanup through a symlinked ${segment}", func(t *testing.T) {
		for _, tc := range []struct {
			segment string
			parts   []string
		}{{"container", nil}, {"host", []string{"github.com"}}, {"repository", []string{"github.com", "acme", "tool"}}} {
			t.Run(tc.segment, func(t *testing.T) {
				dir := pnSetup(t)
				project, external := filepath.Join(dir, "project"), filepath.Join(dir, "external")
				link := filepath.Join(append([]string{project, ".pi", "git"}, tc.parts...)...)
				pnMkdir(t, external)
				pnMkdir(t, filepath.Dir(link))
				pnMust(t, os.Symlink(external, link))
				checkout := pnCheckout(t, project)
				legacy := pnLegacy(t, checkout, "linked package notes")
				moved := pnStore(project)
				pnEqual(t, pnNotes(t, filepath.Join(checkout, "src")), moved, "notesDir(src)")
				pnEqual(t, pnLstat(t, legacy).Mode()&os.ModeSymlink != 0, true, "legacy is a link")
				pnUpdate(t, checkout)
				pnEqual(t, pnRead(t, moved, "task", "state.md"), "linked package notes", "note after cleanup")
				pnEqual(t, pnNotes(t, checkout), moved, "notesDir(checkout)")
				worktree := filepath.Join(dir, "worktree")
				pnGit(t, checkout, "worktree", "add", "--detach", worktree)
				real, err := filepath.EvalSymlinks(checkout)
				pnMust(t, err)
				pnEqual(t, pnNotes(t, worktree), filepath.Join(real, ".pi", "notes"), "worktree of a linked package")
				// A physical path has no backlink from which to discover the project-level symlink.
				pnEqual(t, pnNotes(t, real), filepath.Join(real, ".pi", "notes"), "physical path")
			})
		}
	})

	tw(t, "package-notes", "project packages with separate Git metadata keep shared notes in that metadata directory", func(t *testing.T) {
		project := pnSetup(t)
		checkout := pnCheckout(t, project)
		metadata := filepath.Join(project, "metadata")
		pnGit(t, checkout, "init", "--separate-git-dir="+metadata)
		moved := filepath.Join(metadata, ".pi", "notes")
		pnEqual(t, pnNotes(t, checkout), moved, "notesDir(checkout)")
		pnMkdir(t, moved)
		pnWrite(t, filepath.Join(moved, "state.md"), "external metadata notes")
		pnUpdate(t, checkout)
		pnEqual(t, pnRead(t, pnNotes(t, checkout), "state.md"), "external metadata notes", "metadata note")
	})

	tw(t, "package-notes", "global package ${segment} symlink coverage remains explicit", func(t *testing.T) {
		for _, segment := range []string{"container", "repository"} {
			t.Run(segment, func(t *testing.T) {
				dir := pnDir(t)
				agent := filepath.Join(dir, "agent")
				pnAgent(t, agent)
				external := filepath.Join(dir, "external")
				checkout := filepath.Join(agent, "git", "github.com", "acme", "tool")
				link := checkout
				if segment == "container" {
					link = filepath.Join(agent, "git")
				}
				pnMkdir(t, external)
				pnMkdir(t, filepath.Dir(link))
				pnMust(t, os.Symlink(external, link))
				pnMkdir(t, checkout)
				pnGit(t, checkout, "init", "-q")
				expected := filepath.Join(external, ".pi", "notes")
				if segment == "container" {
					expected = filepath.Join(agent, "notes", "github.com", "acme", "tool")
				}
				pnEqual(t, pnNotes(t, checkout), expected, "global package notes")
			})
		}
	})

	tw(t, "package-notes", "global-nested notes preserve the existing store with checkout link=${linked}", func(t *testing.T) {
		for _, linked := range []bool{false, true} {
			t.Run(map[bool]string{false: "false", true: "true"}[linked], func(t *testing.T) {
				dir := pnDir(t)
				agent := filepath.Join(dir, "agent")
				pnAgent(t, agent)
				outer := filepath.Join(agent, "git", "github.com", "acme", "outer")
				checkout := pnCheckout(t, outer)
				previous := filepath.Join(agent, "notes", "github.com", "acme", "outer", ".pi", "git", "github.com", "acme", "tool")
				pnMkdir(t, previous)
				pnWrite(t, filepath.Join(previous, "state.md"), "existing global descendant notes")
				if linked {
					pnMkdir(t, checkout, ".pi")
					pnMust(t, os.Symlink(previous, filepath.Join(checkout, ".pi", "notes")))
				}
				pnEqual(t, pnNotes(t, checkout), previous, "notesDir(checkout)")
				pnEqual(t, pnRead(t, pnNotes(t, checkout), "state.md"), "existing global descendant notes", "existing note")
				pnUpdate(t, checkout)
				pnEqual(t, pnNotes(t, checkout), previous, "notesDir after update")
				pnEqual(t, pnRead(t, pnNotes(t, checkout), "state.md"), "existing global descendant notes", "note after update")
				pnEqual(t, pnExists(agent, "package-notes"), false, "no package-notes store")
			})
		}
	})
}
