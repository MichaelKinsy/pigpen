package pi_subagents

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Twins of test/unit/watchdog-change-signature.test.ts. The original forces the read and stat failures by patching node:fs;
// here they come through wdHashFile and wdLstat.

func wdGitRaw(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		t.Fatalf("git %s: %v %s", strings.Join(args, " "), err, msg)
	}
	return out.String()
}

func wdGit(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	return strings.TrimSpace(wdGitRaw(t, cwd, args...))
}

func wdEmptyRepo(t *testing.T) string {
	t.Helper()
	repo := tmp(t)
	wdGit(t, repo, "init")
	wdGit(t, repo, "config", "user.email", "watchdog@example.com")
	wdGit(t, repo, "config", "user.name", "Watchdog Tests")
	wdGit(t, repo, "config", "commit.gpgsign", "false")
	return repo
}

func wdRepo(t *testing.T) string {
	t.Helper()
	repo := wdEmptyRepo(t)
	writeFile(t, filepath.Join(repo, ".watchdog-baseline"), "baseline\n")
	wdGit(t, repo, "add", ".watchdog-baseline")
	wdGit(t, repo, "commit", "-m", "initial")
	return repo
}

func wdTouch(t *testing.T, path string, ms int64) {
	t.Helper()
	at := time.UnixMilli(ms)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func wdSig(t *testing.T, repo string) *watchdogRepoChangeSignature {
	t.Helper()
	return computeWatchdogRepoChangeSignature(repo)
}

func wdKey(s *watchdogRepoChangeSignature) string {
	if s == nil {
		return "<nil>"
	}
	return s.Key
}

func wdHas(list []string, v string) bool { return wdContains(list, v) }

func TestWatchdogChangeSignatureTwins(t *testing.T) {
	const f = "watchdog-change-signature"
	withTempHome(t)
	tw(t, f, "summarizes modified submodules as opaque Git worktrees", func(t *testing.T) {
		childSource := wdRepo(t)
		parent := wdRepo(t)
		checkout := filepath.Join(parent, "vendor", "child")
		ignoredFile := filepath.Join(checkout, "node_modules", "ignored.bin")
		writeFile(t, filepath.Join(childSource, ".gitignore"), "node_modules/\n")
		writeFile(t, filepath.Join(childSource, "tracked.txt"), "one\n")
		wdGit(t, childSource, "add", "-A")
		wdGit(t, childSource, "commit", "-m", "initial")
		wdGit(t, parent, "-c", "protocol.file.allow=always", "submodule", "add", childSource, "vendor/child")
		wdGit(t, parent, "commit", "-am", "add submodule")
		wdGit(t, checkout, "config", "user.email", "watchdog@example.com")
		wdGit(t, checkout, "config", "user.name", "Watchdog Tests")
		wdGit(t, checkout, "config", "commit.gpgsign", "false")
		writeFile(t, ignoredFile, "ignored one\n")
		writeFile(t, filepath.Join(checkout, "tracked.txt"), "two\n")

		first := wdSig(t, parent)
		wdEq(t, first.ChangedPaths, []string{"vendor/child"}, "changedPaths")
		writeFile(t, ignoredFile, "ignored two\n")
		wdEq(t, wdKey(wdSig(t, parent)), first.Key, "ignored-only change")
		writeFile(t, filepath.Join(checkout, "tracked.txt"), "three\n")
		wdEq(t, wdKey(wdSig(t, parent)), first.Key, "dirty submodule content is intentionally opaque")
		wdGit(t, checkout, "add", "tracked.txt")
		wdGit(t, checkout, "commit", "-m", "update tracked")
		if wdKey(wdSig(t, parent)) == first.Key {
			t.Error("submodule HEAD changes remain visible")
		}
	})
	tw(t, f, "does not inspect untracked files in an empty repository", func(t *testing.T) {
		repo := wdEmptyRepo(t)
		writeFile(t, filepath.Join(repo, "untracked.txt"), "one\n")
		first := wdSig(t, repo)
		if first == nil {
			t.Fatal("expected a signature")
		}
		wdEq(t, len(first.ChangedPaths), 0, "changedPaths")
		writeFile(t, filepath.Join(repo, "untracked.txt"), "two\n")
		wdEq(t, wdKey(wdSig(t, repo)), first.Key, "untracked-only changes must not trigger an empty-repo scan")
	})
	tw(t, f, "does not inspect untracked files when the repository root is the user home", func(t *testing.T) {
		repo := wdRepo(t)
		t.Setenv("HOME", repo)
		t.Setenv("USERPROFILE", repo)
		writeFile(t, filepath.Join(repo, "untracked.txt"), "one\n")
		first := wdSig(t, repo)
		if first == nil {
			t.Fatal("expected a signature")
		}
		wdEq(t, len(first.ChangedPaths), 0, "changedPaths")
		writeFile(t, filepath.Join(repo, ".watchdog-baseline"), "changed\n")
		tracked := wdSig(t, repo)
		wdEq(t, tracked.ChangedPaths, []string{".watchdog-baseline"}, "tracked change")
		if tracked.Key == first.Key {
			t.Error("tracked changes remain visible for a home-root repository")
		}
	})
	tw(t, f, "uses a metadata marker for files above the (lowered) threshold instead of hashing content", func(t *testing.T) {
		repo := wdRepo(t)
		t.Setenv("PI_SUBAGENTS_MAX_HASH_FILE_BYTES", "1024")
		file := filepath.Join(repo, "big.txt")
		writeFile(t, file, strings.Repeat("a", 4096))
		wdTouch(t, file, 1_600_000_000_000)
		sig := wdSig(t, repo)
		if sig == nil || !wdHas(sig.ChangedPaths, "big.txt") {
			t.Fatalf("signature: %+v", sig)
		}
		wdTouch(t, file, 1_600_000_100_000)
		if wdKey(wdSig(t, repo)) == sig.Key {
			t.Error("mtime-only change must alter the key for large files")
		}
	})
	tw(t, f, "uses metadata markers after the total content hash budget", func(t *testing.T) {
		repo := wdRepo(t)
		t.Setenv("PI_SUBAGENTS_MAX_HASH_TOTAL_BYTES", "10")
		a, b := filepath.Join(repo, "a.txt"), filepath.Join(repo, "b.txt")
		writeFile(t, a, "aaaaaaaa")
		writeFile(t, b, "bbbbbbbb")
		wdTouch(t, a, 1_600_000_000_000)
		wdTouch(t, b, 1_600_000_000_000)
		first := wdSig(t, repo)
		writeFile(t, b, "cccccccc")
		wdTouch(t, b, 1_600_000_000_000)
		wdEq(t, wdKey(wdSig(t, repo)), first.Key, "over-budget files use size+mtime metadata instead of content")
		writeFile(t, a, "dddddddd")
		wdTouch(t, a, 1_600_000_000_000)
		if wdKey(wdSig(t, repo)) == first.Key {
			t.Error("within-budget files are still content hashed")
		}
	})
	tw(t, f, "uses skipped markers after the entry budget", func(t *testing.T) {
		repo := wdRepo(t)
		t.Setenv("PI_SUBAGENTS_MAX_HASH_ENTRIES", "3")
		dir := filepath.Join(repo, "files")
		for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
			writeFile(t, filepath.Join(dir, name), name+"\n")
		}
		first := wdSig(t, repo)
		writeFile(t, filepath.Join(dir, "d.txt"), "changed d\n")
		wdEq(t, wdKey(wdSig(t, repo)), first.Key, "over-budget entries are summarized instead of hashed individually")
		writeFile(t, filepath.Join(dir, "a.txt"), "changed a\n")
		if wdKey(wdSig(t, repo)) == first.Key {
			t.Error("within-budget entries are still content hashed")
		}
	})
	tw(t, f, "produces deterministic keys keyed on size and mtime for large files", func(t *testing.T) {
		repo := wdRepo(t)
		t.Setenv("PI_SUBAGENTS_MAX_HASH_FILE_BYTES", "1024")
		file := filepath.Join(repo, "big.txt")
		writeFile(t, file, strings.Repeat("a", 4096))
		wdTouch(t, file, 1_600_000_000_000)
		first := wdSig(t, repo)
		wdEq(t, wdKey(wdSig(t, repo)), first.Key, "same size+mtime")
		wdTouch(t, file, 1_600_000_100_000)
		afterMtime := wdSig(t, repo)
		if afterMtime.Key == first.Key {
			t.Error("changed mtime must change the key")
		}
		writeFile(t, file, strings.Repeat("a", 8192))
		wdTouch(t, file, 1_600_000_100_000)
		if wdKey(wdSig(t, repo)) == afterMtime.Key {
			t.Error("changed size must change the key")
		}
	})
	tw(t, f, "handles a >2 GiB sparse file without throwing (default threshold)", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the original skips this case on win32")
		}
		repo := wdRepo(t)
		fh, err := os.Create(filepath.Join(repo, "huge.bin"))
		if err != nil {
			t.Fatal(err)
		}
		if err := fh.Truncate(2*1024*1024*1024 + 1); err != nil {
			fh.Close()
			t.Skipf("no sparse files here: %v", err)
		}
		fh.Close()
		sig := wdSig(t, repo)
		if sig == nil || !wdHas(sig.ChangedPaths, "huge.bin") {
			t.Fatalf("signature: %+v", sig)
		}
	})
	tw(t, f, "ignores node_modules below changed directories", func(t *testing.T) {
		repo := wdRepo(t)
		sub := filepath.Join(repo, "nested")
		writeFile(t, filepath.Join(sub, "keep.txt"), "one\n")
		writeFile(t, filepath.Join(sub, "node_modules", "cache.bin"), "ignored one\n")
		first := wdSig(t, repo)
		wdTrue(t, wdHas(first.ChangedPaths, "nested/keep.txt"), "nested/keep.txt listed")
		for _, p := range first.ChangedPaths {
			if strings.Contains(p, "node_modules") {
				t.Errorf("node_modules path listed: %s", p)
			}
		}
		writeFile(t, filepath.Join(sub, "node_modules", "cache.bin"), "ignored two\n")
		wdEq(t, wdKey(wdSig(t, repo)), first.Key, "ignored file change")
		writeFile(t, filepath.Join(sub, "keep.txt"), "two\n")
		if wdKey(wdSig(t, repo)) == first.Key {
			t.Error("a kept file change alters the key")
		}
	})
	tw(t, f, "applies the threshold guard recursively into untracked subdirectories", func(t *testing.T) {
		repo := wdRepo(t)
		t.Setenv("PI_SUBAGENTS_MAX_HASH_FILE_BYTES", "1024")
		file := filepath.Join(repo, "nested", "big.txt")
		writeFile(t, file, strings.Repeat("b", 4096))
		wdTouch(t, file, 1_600_000_000_000)
		sig := wdSig(t, repo)
		wdTrue(t, sig != nil && wdHas(sig.ChangedPaths, "nested/big.txt"), "nested/big.txt listed")
		wdTouch(t, file, 1_600_000_100_000)
		if wdKey(wdSig(t, repo)) == sig.Key {
			t.Error("mtime-only change must alter the key for large files in subdirs")
		}
	})
	tw(t, f, "degrades to the metadata marker when a per-file read raises ERR_FS_FILE_TOO_LARGE", func(t *testing.T) {
		repo := wdRepo(t)
		writeFile(t, filepath.Join(repo, "small.txt"), "hello\n")
		var warnings []string
		seam(t, &wdWarn, func(s string) { warnings = append(warnings, s) })
		seam(t, &wdHashFile, func(string) (string, error) { return "", errWdFileTooLarge })
		sig := wdSig(t, repo)
		wdTrue(t, sig != nil && sig.Key != "" && wdHas(sig.ChangedPaths, "small.txt"), "a single unreadable file must not discard the whole signature")
		wdEq(t, len(warnings), 0, "the too-large case is expected and must stay quiet")
	})
	tw(t, f, "keeps a valid signature when a per-file read races into ENOENT", func(t *testing.T) {
		repo := wdRepo(t)
		writeFile(t, filepath.Join(repo, "small.txt"), "hello\n")
		seam(t, &wdHashFile, func(string) (string, error) {
			return "", &fs.PathError{Op: "open", Path: "small.txt", Err: fs.ErrNotExist}
		})
		sig := wdSig(t, repo)
		wdTrue(t, sig != nil && sig.Key != "" && wdHas(sig.ChangedPaths, "small.txt"), "an ENOENT during read degrades to the deleted marker")
	})
	tw(t, f, "warns and falls back to metadata for other per-file read errors", func(t *testing.T) {
		repo := wdRepo(t)
		writeFile(t, filepath.Join(repo, "small.txt"), "hello\n")
		var warnings []string
		seam(t, &wdWarn, func(s string) { warnings = append(warnings, s) })
		seam(t, &wdHashFile, func(string) (string, error) {
			return "", &fs.PathError{Op: "open", Path: "small.txt", Err: fs.ErrPermission}
		})
		sig := wdSig(t, repo)
		wdTrue(t, sig != nil && sig.Key != "", "a single unreadable file must not discard the whole signature")
		found := false
		for _, w := range warnings {
			found = found || (strings.Contains(w, "hashFile fell back to metadata") && strings.Contains(w, "small.txt"))
		}
		wdTrue(t, found, "a warning names the file")
	})
	tw(t, f, "returns undefined instead of throwing when hashing a changed path raises", func(t *testing.T) {
		repo := wdRepo(t)
		writeFile(t, filepath.Join(repo, "x.txt"), "changed\n")
		seam(t, &wdWarn, func(string) {})
		seam(t, &wdLstat, func(string) (fs.FileInfo, error) { return nil, errors.New("forced stat failure (EIO)") })
		if sig := wdSig(t, repo); sig != nil {
			t.Errorf("a failure in the hashing region must fail safe to undefined, got %+v", sig)
		}
	})
	tw(t, f, "still hashes small files by content when under the threshold", func(t *testing.T) {
		repo := wdRepo(t)
		file := filepath.Join(repo, "small.txt")
		writeFile(t, file, "hello\n")
		wdTouch(t, file, 1_600_000_000_000)
		sig := wdSig(t, repo)
		wdTrue(t, sig != nil && wdHas(sig.ChangedPaths, "small.txt"), "small.txt listed")
		wdTouch(t, file, 1_600_000_100_000)
		wdEq(t, wdKey(wdSig(t, repo)), sig.Key, "mtime-only change must not alter key for small files")
		writeFile(t, file, "changed\n")
		if wdKey(wdSig(t, repo)) == sig.Key {
			t.Error("content change must alter key")
		}
	})
}
