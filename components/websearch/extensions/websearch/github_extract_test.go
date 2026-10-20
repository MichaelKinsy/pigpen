package websearch

// Twins of github-extract.test.mjs.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const ghExtractFile = "github-extract"

func TestUpstream_github_extract(t *testing.T) {
	F := ghExtractFile

	tw(t, F, "parseGitHubUrl rejects malformed identifiers and preserves normal GitHub paths", func(t *testing.T) {
		for _, u := range []string{"https://github.com/%2E%2E%2Fvictim/repo", "https://github.com/owner/repo%2Fother", "https://github.com/owner%ZZ/repo"} {
			if got := parseGitHubURL(u); got != nil {
				t.Fatalf("%s: %+v", u, got)
			}
		}
		for u, want := range map[string]githubURL{
			"https://github.com/owner/repo.git":                           {Owner: "owner", Repo: "repo", Type: "root"},
			"https://github.com/owner/repo/blob/main/src/file.ts":         {Owner: "owner", Repo: "repo", Ref: "main", Path: "src/file.ts", Type: "blob"},
			"https://github.com/owner/repo/tree/feature%2Fbranch/src":     {Owner: "owner", Repo: "repo", Ref: "feature/branch", Path: "src", Type: "tree"},
		} {
			got := parseGitHubURL(u)
			if got == nil || *got != want {
				t.Fatalf("%s: got %+v, want %+v", u, got, want)
			}
		}
	})

	tw(t, F, "malformed GitHub identifiers cannot delete outside the clone cache", func(t *testing.T) {
		w := newGHWorld(t, nil)
		victim := filepath.Join(w.root, "victim", "repo")
		writeFile(t, filepath.Join(victim, "marker.txt"), "preserve")
		s := ghState(t)
		res, err := s.extractGitHub(bg(), "https://github.com/..%2Fvictim/repo", false)
		if err != nil || res != nil {
			t.Fatalf("got %+v, %v", res, err)
		}
		if readFile(t, filepath.Join(victim, "marker.txt")) != "preserve" {
			t.Fatal("the marker changed")
		}
	})

	tw(t, F, "clearCloneCache removes only its runtime directory", func(t *testing.T) {
		w := newGHWorld(t, nil)
		sibling := filepath.Join(w.clonePath, "preserve.txt")
		writeFile(t, sibling, "preserve")
		w.shim(t, "gh", "exit 1")
		w.shim(t, "git", `for a; do dest="$a"; done
mkdir -p "$dest"
printf fixture > "$dest/README.md"`)
		s := ghState(t)
		first, err := s.extractGitHub(bg(), "https://github.com/owner/repo", false)
		noErr(t, err)
		firstPath := clonedPath(first)
		if firstPath == "" {
			t.Fatalf("no clone: %+v", first)
		}
		firstRuntime := filepath.Dir(firstPath)
		if !pathExists(firstRuntime) {
			t.Fatal("the runtime directory is missing before the clear")
		}
		s.clearCloneCache()
		if pathExists(firstRuntime) {
			t.Fatal("the runtime directory survived the clear")
		}
		second, err := s.extractGitHub(bg(), "https://github.com/owner/repo", false)
		noErr(t, err)
		secondPath := clonedPath(second)
		if secondPath == "" || filepath.Dir(secondPath) == firstRuntime {
			t.Fatalf("second clone %q reused %q", secondPath, firstRuntime)
		}
		s.clearCloneCache()
		if pathExists(filepath.Dir(secondPath)) {
			t.Fatal("the second runtime directory survived the clear")
		}
		if readFile(t, sibling) != "preserve" {
			t.Fatal("the sibling file changed")
		}
	})

	tw(t, F, "clone cleanup unlinks a direct-child symlink without deleting its target", func(t *testing.T) {
		w := newGHWorld(t, nil)
		outside := filepath.Join(w.root, "outside")
		writeFile(t, filepath.Join(outside, "marker.txt"), "preserve")
		destinationFile := filepath.Join(w.root, "destination.txt")
		t.Setenv("OUTSIDE_TARGET", outside)
		t.Setenv("DESTINATION_FILE", destinationFile)
		w.shim(t, "gh", "exit 1")
		w.shim(t, "git", `for a; do dest="$a"; done
ln -s "$OUTSIDE_TARGET" "$dest"
printf '%s' "$dest" > "$DESTINATION_FILE"
exit 1`)
		s := ghState(t)
		if _, err := s.extractGitHub(bg(), "https://github.com/owner/repo", false); err != nil {
			t.Fatal(err)
		}
		if pathExists(readFile(t, destinationFile)) {
			t.Fatal("the symlink survived the failed clone")
		}
		if readFile(t, filepath.Join(outside, "marker.txt")) != "preserve" {
			t.Fatal("the symlink target was deleted")
		}
	})

	tw(t, F, "a failed clone cannot clean up another process's in-flight destination", func(t *testing.T) {
		w := newGHWorld(t, nil)
		ctl := filepath.Join(w.root, "ctl")
		if err := os.MkdirAll(ctl, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GH_CLONE_COUNT_FILE", filepath.Join(ctl, "count"))
		t.Setenv("GH_READY_DIR", ctl)
		t.Setenv("GH_RELEASE_DIR", ctl)
		t.Setenv("GH_FAIL_CLONE", "2")
		t.Setenv("GH_CLONE_CONTENT", "runtime-a")
		w.shim(t, "gh", controlledGh)
		a, b := ghState(t), ghState(t)
		var aRes, bRes *ExtractedContent
		var wg sync.WaitGroup
		release := func(n string) { writeFile(t, filepath.Join(ctl, "release-"+n), "release") }
		t.Cleanup(func() { release("1"); release("2"); wg.Wait() })
		wg.Add(1)
		go func() {
			defer wg.Done()
			aRes, _ = a.extractGitHub(bg(), "https://github.com/owner/repo", true)
		}()
		waitUntil(t, "the first clone", func() bool { return pathExists(filepath.Join(ctl, "ready-1")) })
		wg.Add(1)
		go func() {
			defer wg.Done()
			bRes, _ = b.extractGitHub(bg(), "https://github.com/owner/repo", true)
		}()
		waitUntil(t, "the second clone", func() bool { return pathExists(filepath.Join(ctl, "ready-2")) })
		aPath, bPath := readFile(t, filepath.Join(ctl, "ready-1")), readFile(t, filepath.Join(ctl, "ready-2"))
		release("2")
		waitUntil(t, "the failed clone's cleanup", func() bool { return !pathExists(bPath) })
		if !pathExists(aPath) {
			t.Fatal("the failure cleaned up the other process's destination")
		}
		release("1")
		wg.Wait()
		if aPath == bPath {
			t.Fatal("both processes cloned into one directory")
		}
		if bRes != nil {
			t.Fatalf("the failed clone returned %+v", bRes)
		}
		if clonedPath(aRes) != aPath || !pathExists(aPath) || !strings.Contains(aRes.Content, "runtime-a") {
			t.Fatalf("first result %+v", aRes)
		}
	})

	tw(t, F, "runtime cache reuse and cleanup stay isolated across processes", func(t *testing.T) {
		w := newGHWorld(t, nil)
		count := filepath.Join(w.root, "count")
		t.Setenv("GH_CLONE_COUNT_FILE", count)
		w.shim(t, "gh", controlledGh)
		a, b := ghState(t), ghState(t)
		t.Setenv("GH_CLONE_CONTENT", "runtime-a")
		first, err := a.extractGitHub(bg(), "https://github.com/owner/repo", true)
		noErr(t, err)
		second, err := a.extractGitHub(bg(), "https://github.com/owner/repo", true)
		noErr(t, err)
		firstPath := clonedPath(first)
		if firstPath == "" || firstPath != clonedPath(second) {
			t.Fatalf("paths %q, %q", firstPath, clonedPath(second))
		}
		if !strings.Contains(first.Content, "runtime-a") || !strings.Contains(second.Content, "runtime-a") {
			t.Fatal("the cached result lost the first clone's content")
		}
		if readFile(t, count) != "1" {
			t.Fatalf("the cache cloned %s times", readFile(t, count))
		}
		t.Setenv("GH_CLONE_CONTENT", "runtime-b")
		res, err := b.extractGitHub(bg(), "https://github.com/owner/repo", true)
		noErr(t, err)
		bPath := clonedPath(res)
		if bPath == "" || bPath == firstPath || !strings.Contains(res.Content, "runtime-b") {
			t.Fatalf("second process %+v", res)
		}
		if readFile(t, count) != "2" {
			t.Fatalf("clones: %s", readFile(t, count))
		}
		if runtime.GOOS != "windows" {
			st, err := os.Stat(filepath.Dir(bPath))
			if err != nil || st.Mode().Perm()&0o077 != 0 {
				t.Fatalf("runtime mode %v, %v", st, err)
			}
		}
		b.clearCloneCache()
		if pathExists(filepath.Dir(bPath)) {
			t.Fatal("the second process's runtime survived its clear")
		}
		if !pathExists(firstPath) || readFile(t, filepath.Join(firstPath, "README.md")) != "runtime-a" {
			t.Fatal("the first process's clone was touched")
		}
		if st, err := os.Stat(filepath.Dir(firstPath)); err != nil || st.Mode().Perm()&0o077 != 0 {
			t.Fatalf("first runtime mode %v, %v", st, err)
		}
	})

	tw(t, F, "clone runtime creation proceeds while background cleanup is gated", func(t *testing.T) {
		w := newGHWorld(t, nil)
		writeFile(t, filepath.Join(w.clonePath, "runtime-unknown"), "preserve")
		w.shim(t, "gh", controlledGh)
		s := ghState(t)
		started, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		s.beforeSweep = func() {
			once.Do(func() { close(started) })
			<-release
		}
		t.Cleanup(func() { once.Do(func() { close(started) }); select { case <-release: default: close(release) } })
		res, err := s.extractGitHub(bg(), "https://github.com/owner/repo", true)
		noErr(t, err)
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("the background cleanup never started")
		}
		p := clonedPath(res)
		if p == "" || !pathExists(p) {
			t.Fatalf("no clone while the cleanup was gated: %+v", res)
		}
		close(release)
		s.sweeps.Wait()
		if readFile(t, filepath.Join(w.clonePath, "runtime-unknown")) != "preserve" {
			t.Fatal("the unknown entry changed")
		}
	})

	tw(t, F, "background cleanup removes dead runtimes after unrelated files and ownerless directories", func(t *testing.T) {
		w := newGHWorld(t, nil)
		for i := range 512 {
			if err := os.MkdirAll(filepath.Join(w.clonePath, "runtime-preserved-"+pad3(i)), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for i := range 1024 {
			writeFile(t, filepath.Join(w.clonePath, "unrelated-"+pad4(i)), "preserve")
		}
		w.shim(t, "gh", controlledGh)
		// A process that owned a runtime and died: a child that is alive while its runtime is made and reaped after.
		child := exec.Command("sleep", "60")
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		stale := ghState(t)
		stale.pid = child.Process.Pid
		res, err := stale.extractGitHub(bg(), "https://github.com/owner/repo", true)
		noErr(t, err)
		staleRuntime := filepath.Dir(clonedPath(res))
		owner := readFile(t, filepath.Join(staleRuntime, ".owner.json"))
		var stalePaths []string
		for i := range 3 {
			p := filepath.Join(w.clonePath, "runtime-late-dead-"+pad3(i))
			writeFile(t, filepath.Join(p, ".owner.json"), owner)
			stalePaths = append(stalePaths, p)
		}
		_ = child.Process.Kill()
		_ = child.Wait()
		current := ghState(t)
		cur, err := current.extractGitHub(bg(), "https://github.com/owner/repo", true)
		noErr(t, err)
		current.sweeps.Wait()
		for _, p := range append([]string{staleRuntime}, stalePaths...) {
			if pathExists(p) {
				t.Fatalf("the dead runtime %s survived the cleanup", p)
			}
		}
		if !pathExists(clonedPath(cur)) {
			t.Fatal("the current runtime was removed")
		}
		for _, n := range []string{"runtime-preserved-000", "runtime-preserved-511"} {
			if !pathExists(filepath.Join(w.clonePath, n)) {
				t.Fatalf("%s was removed", n)
			}
		}
	})

	tw(t, F, "oversized malformed owner metadata is preserved during extraction", func(t *testing.T) {
		w := newGHWorld(t, nil)
		malformed := filepath.Join(w.clonePath, "runtime-oversized-owner")
		dead := filepath.Join(w.clonePath, "runtime-dead")
		writeFile(t, filepath.Join(malformed, ".owner.json"), "{"+strings.Repeat("x", 4095))
		writeFile(t, filepath.Join(dead, ".owner.json"), deadOwner())
		w.shim(t, "gh", controlledGh)
		s := ghState(t)
		res, err := s.extractGitHub(bg(), "https://github.com/owner/repo", true)
		noErr(t, err)
		s.sweeps.Wait()
		if pathExists(dead) {
			t.Fatal("the dead runtime survived")
		}
		if !pathExists(clonedPath(res)) || !pathExists(malformed) {
			t.Fatal("a live or malformed runtime was removed")
		}
	})

	tw(t, F, "clone runtime initialization preserves live, unknown, and symlink runtimes", func(t *testing.T) {
		w := newGHWorld(t, nil)
		unknown := filepath.Join(w.clonePath, "runtime-unknown")
		dead := filepath.Join(w.clonePath, "runtime-dead")
		outside := filepath.Join(w.root, "outside")
		symlink := filepath.Join(w.clonePath, "runtime-symlink")
		if err := os.MkdirAll(unknown, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dead, ".owner.json"), deadOwner())
		writeFile(t, filepath.Join(outside, "marker.txt"), "preserve")
		if err := os.Symlink(outside, symlink); err != nil {
			t.Fatal(err)
		}
		w.shim(t, "gh", controlledGh)
		live := ghState(t)
		liveRes, err := live.extractGitHub(bg(), "https://github.com/owner/repo", true)
		noErr(t, err)
		live.sweeps.Wait()
		current := ghState(t)
		if _, err := current.extractGitHub(bg(), "https://github.com/owner/repo", true); err != nil {
			t.Fatal(err)
		}
		current.sweeps.Wait()
		if pathExists(dead) {
			t.Fatal("the dead runtime survived")
		}
		for _, p := range []string{filepath.Dir(clonedPath(liveRes)), unknown, symlink} {
			if !pathExists(p) {
				t.Fatalf("%s was removed", p)
			}
		}
		if readFile(t, filepath.Join(outside, "marker.txt")) != "preserve" {
			t.Fatal("the symlink target changed")
		}
	})

	tw(t, F, "normalizeClonePath expands ~ to HOME", func(t *testing.T) {
		w := newGHWorld(t, map[string]any{"clonePath": "~/test-repos"})
		home := os.Getenv("HOME")
		assertExpandedClonePath(t, w, filepath.Join(home, "test-repos"))
	})

	tw(t, F, "normalizeClonePath expands $HOME and other env vars", func(t *testing.T) {
		w := newGHWorld(t, map[string]any{"clonePath": "$HOME/my-repos"})
		assertExpandedClonePath(t, w, filepath.Join(os.Getenv("HOME"), "my-repos"))
		t.Setenv("PI_WEB_TEST_DIR", filepath.Join(w.root, "elsewhere"))
		if got := normalizeClonePath("$PI_WEB_TEST_DIR/x/$PI_WEB_TEST_UNSET", "fallback"); got != filepath.Join(w.root, "elsewhere")+"/x/$PI_WEB_TEST_UNSET" {
			t.Fatalf("got %q", got)
		}
	})

	tw(t, F, "normalizeClonePath handles absolute paths without expansion", func(t *testing.T) {
		w := newGHWorld(t, map[string]any{"clonePath": "/tmp/my-repos"})
		cfg, err := ghState(t).cloneConfig()
		noErr(t, err)
		if cfg.ClonePath != "/tmp/my-repos" {
			t.Fatalf("clonePath %q", cfg.ClonePath)
		}
		_ = w
		for in, want := range map[string]string{"  ": "fb", "": "fb", " /a/b ": "/a/b"} {
			if got := normalizeClonePath(in, "fb"); got != want {
				t.Fatalf("%q: got %q, want %q", in, got, want)
			}
		}
		if got := normalizeClonePath(3.0, "fb"); got != "fb" {
			t.Fatalf("a number gave %q", got)
		}
	})

	tw(t, F, "GitHub clones disable interactive credential prompts", func(t *testing.T) {
		w := newGHWorld(t, map[string]any{"cloneTimeoutSeconds": 4})
		envFile := filepath.Join(w.root, "clone-env.json")
		t.Setenv("CLONE_ENV_FILE", envFile)
		w.shim(t, "gh", "exit 1")
		w.shim(t, "git", `for a; do dest="$a"; done
mkdir -p "$dest"
printf fixture > "$dest/README.md"
printf '{"gitTerminalPrompt":"%s","gcmInteractive":"%s","ghPromptDisabled":"%s"}' "$GIT_TERMINAL_PROMPT" "$GCM_INTERACTIVE" "$GH_PROMPT_DISABLED" > "$CLONE_ENV_FILE"`)
		res, err := ghState(t).extractGitHub(bg(), "https://github.com/test/repo", false)
		noErr(t, err)
		if res == nil {
			t.Fatal("no result")
		}
		if got := readFile(t, envFile); got != `{"gitTerminalPrompt":"0","gcmInteractive":"Never","ghPromptDisabled":"1"}` {
			t.Fatalf("clone environment %s", got)
		}
	})

	tw(t, F, "GitHub clone timeout force-kills the SIGTERM-resistant process group", func(t *testing.T) {
		w := newGHWorld(t, map[string]any{"cloneTimeoutSeconds": 1})
		pidFile := filepath.Join(w.root, "processes")
		t.Setenv("CLONE_PROCESS_PID_FILE", pidFile)
		w.shim(t, "gh", "exit 1")
		w.shim(t, "git", `trap '' TERM
sh -c 'trap "" TERM; while :; do sleep 1; done' &
printf '%s %s' "$$" "$!" > "$CLONE_PROCESS_PID_FILE"
while :; do sleep 1; done`)
		res, err := ghState(t).extractGitHub(bg(), "https://github.com/test/repo", false)
		noErr(t, err)
		if res != nil {
			t.Fatalf("a killed clone returned %+v", res)
		}
		var rootPID, helperPID int
		if _, err := fmt.Sscan(readFile(t, pidFile), &rootPID, &helperPID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			for _, pid := range []int{rootPID, helperPID} {
				if p, err := os.FindProcess(pid); err == nil && processAlive(pid) {
					_ = p.Kill()
				}
			}
		})
		if !waitForExit(rootPID, 2*time.Second) {
			t.Fatalf("clone process %d survived the SIGKILL fallback", rootPID)
		}
		if !waitForExit(helperPID, 2*time.Second) {
			t.Fatalf("clone helper %d survived the SIGKILL fallback", helperPID)
		}
	})

	tw(t, F, "githubClone.enabled false skips GitHub clone/API specialization", func(t *testing.T) {
		newGHWorld(t, map[string]any{"enabled": false})
		res, err := ghState(t).extractGitHub(bg(), "https://github.com/owner/repo", false)
		if err != nil || res != nil {
			t.Fatalf("got %+v, %v", res, err)
		}
	})
}

// assertExpandedClonePath checks that the configured path is expanded and that a clone attempt
// creates its runtime directory there (no gh, no git: nothing is cloned and nothing is fetched).
func assertExpandedClonePath(t *testing.T, w *ghWorld, want string) {
	t.Helper()
	s := ghState(t)
	cfg, err := s.cloneConfig()
	noErr(t, err)
	if cfg.ClonePath != want {
		t.Fatalf("clonePath %q, want %q", cfg.ClonePath, want)
	}
	t.Setenv("PATH", filepath.Join(w.root, "empty"))
	res, err := s.extractGitHub(bg(), "https://github.com/test/repo", false)
	if err != nil || res != nil {
		t.Fatalf("got %+v, %v", res, err)
	}
	entries, err := os.ReadDir(want)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no runtime directory under %s: %v", want, err)
	}
}

func deadOwner() string {
	return `{"version":1,"pid":999999999,"platform":"` + nodePlatform() + `","bootId":"test","startTime":"1"}`
}

func pad3(i int) string { return pad(i, 3) }
func pad4(i int) string { return pad(i, 4) }
func pad(i, n int) string {
	s := itoa(i)
	for len(s) < n {
		s = "0" + s
	}
	return s
}
