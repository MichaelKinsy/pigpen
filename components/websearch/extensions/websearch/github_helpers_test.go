package websearch

// Helpers the GitHub twins share. The upstream tests put fake `gh` and `git` executables (Node
// scripts) ahead of PATH and run each case in a child process; here the fakes are /bin/sh scripts
// and one githubState stands for one process (the original keeps its state in module variables).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ghWorld is an isolated agent directory, a clone directory and a directory of fake executables
// that PATH reaches first.
type ghWorld struct {
	root, bin, agentDir, clonePath string
}

func skipWithoutShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh and git are /bin/sh scripts, like the upstream skip of its process-group cases on win32")
	}
}

// newGHWorld writes web-search.json with the given githubClone settings (clonePath defaults to a
// directory of the world) and puts a directory of fake executables ahead of PATH.
func newGHWorld(t *testing.T, clone map[string]any) *ghWorld {
	t.Helper()
	skipWithoutShell(t)
	_, agentDir := isolate(t)
	root := t.TempDir()
	w := &ghWorld{root: root, bin: filepath.Join(root, "bin"), agentDir: agentDir, clonePath: filepath.Join(root, "repos")}
	if err := os.MkdirAll(w.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", w.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if clone == nil {
		clone = map[string]any{}
	}
	if _, ok := clone["clonePath"]; !ok {
		clone["clonePath"] = w.clonePath
	}
	b, err := json.Marshal(map[string]any{"githubClone": clone})
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, agentDir, string(b))
	return w
}

// shim writes a fake executable.
func (w *ghWorld) shim(t *testing.T, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(w.bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// ghState is a process of the original: fresh module state that cleans up its clones.
func ghState(t *testing.T) *githubState {
	t.Helper()
	s := newGitHubState()
	s.hintOut = &discard{}
	t.Cleanup(func() {
		s.clearCloneCache()
		s.sweeps.Wait()
	})
	return s
}

type discard struct{}

func (*discard) Write(p []byte) (int, error) { return len(p), nil }

var clonedToRE = regexp.MustCompile(`(?m)^Repository cloned to: (.+)$`)

// clonedPath is the clone directory a result names ("" when it names none).
func clonedPath(r *ExtractedContent) string {
	if r == nil {
		return ""
	}
	m := clonedToRE.FindStringSubmatch(r.Content)
	if m == nil {
		return ""
	}
	return m[1]
}

func lstatExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// processAlive is kill(pid, 0); a zombie counts as gone (a container's init may reap orphans late).
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil || p.Signal(syscall.Signal(0)) != nil {
		return false
	}
	if b, err := os.ReadFile("/proc/" + itoa(pid) + "/stat"); err == nil {
		s := string(b)
		if i := strings.LastIndex(s, ") "); i >= 0 && strings.HasPrefix(s[i+2:], "Z") {
			return false
		}
	}
	return true
}

func waitForExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return !processAlive(pid)
}

// controlledGh is the upstream writeControlledGh: `gh repo clone` writes a README with the content in
// GH_CLONE_CONTENT, counts itself in GH_CLONE_COUNT_FILE, tells GH_READY_DIR/ready-N where it cloned, waits
// for GH_RELEASE_DIR/release-N and then fails when N is GH_FAIL_CLONE.
const controlledGh = `if [ "$1" = "--version" ]; then exit 0; fi
if [ "$1" != "repo" ] || [ "$2" != "clone" ]; then exit 1; fi
dest="$4"
mkdir -p "$dest"
printf '%s' "${GH_CLONE_CONTENT:-fixture}" > "$dest/README.md"
n=0
if [ -n "$GH_CLONE_COUNT_FILE" ]; then
  [ -f "$GH_CLONE_COUNT_FILE" ] && n=$(cat "$GH_CLONE_COUNT_FILE")
  n=$((n+1))
  printf '%s' "$n" > "$GH_CLONE_COUNT_FILE"
fi
if [ -n "$GH_READY_DIR" ]; then printf '%s' "$dest" > "$GH_READY_DIR/ready-$n"; fi
if [ -n "$GH_RELEASE_DIR" ]; then
  while [ ! -f "$GH_RELEASE_DIR/release-$n" ]; do sleep 0.01; done
fi
if [ "$GH_FAIL_CLONE" = "$n" ]; then exit 1; fi
exit 0`
