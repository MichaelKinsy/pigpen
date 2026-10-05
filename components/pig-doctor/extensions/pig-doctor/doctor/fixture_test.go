//go:build unix

package doctor

// Fixture homes. Every test builds its own PiG home under t.TempDir(); nothing
// reads the real ~/.pig or the process environment (rule 17).

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

const sdkModule = "github.com/MichaelKinsy/PiG/extensions/sdk"

// canary is the content of every credential file in the fixtures. No doctor
// output may contain it and no doctor code may read it.
const canary = "CANARY-SECRET-DO-NOT-READ"

type fixture struct {
	t                            *testing.T
	root, home, agent, user, bin string
	now                          time.Time
	procs                        *fakeProcs
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	f := &fixture{t: t, root: root, home: filepath.Join(root, "pig"), user: filepath.Join(root, "user"), bin: filepath.Join(root, "bin"), now: time.Now(), procs: &fakeProcs{}}
	f.agent = filepath.Join(f.home, "agent")
	for _, d := range []string{f.home, f.agent, f.user, f.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.fakeGo("go1.26.1", "go1.26.1", "")
	return f
}

func (f *fixture) opts() Options {
	return Options{
		Home: f.home, AgentDir: f.agent, UserHome: f.user,
		Env:     []string{"PATH=" + f.bin + ":/usr/bin:/bin", "HOME=" + f.user},
		Now:     func() time.Time { return f.now },
		SelfPID: 999999,
		Procs:   f.procs,
		Exec:    execCommand,
	}
}

func (f *fixture) put(path, content string) string {
	f.t.Helper()
	if !filepath.IsAbs(path) {
		path = filepath.Join(f.home, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
	return path
}

func (f *fixture) mkdir(path string) string {
	f.t.Helper()
	if !filepath.IsAbs(path) {
		path = filepath.Join(f.home, path)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return path
}

// age sets the modification time of path and everything below it (never following symlinks).
func (f *fixture) age(path string, days float64) {
	f.t.Helper()
	if !filepath.IsAbs(path) {
		path = filepath.Join(f.home, path)
	}
	when := f.now.Add(-time.Duration(days * 24 * float64(time.Hour)))
	var paths []string
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil {
			paths = append(paths, p)
		}
		return nil
	})
	for i := len(paths) - 1; i >= 0; i-- {
		if info, err := os.Lstat(paths[i]); err != nil || info.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		if err := os.Chtimes(paths[i], when, when); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *fixture) fakeGo(goVersion, compileVersion, goroot string) {
	f.t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
"version") echo "go version %[1]s linux/amd64" ;;
"tool compile -V") echo "compile version %[2]s" ;;
"env GOROOT") if [ -n "$GOROOT" ]; then echo "$GOROOT"; else echo "%[3]s"; fi ;;
*) echo "unexpected: $*" >&2; exit 1 ;;
esac
`, goVersion, compileVersion, orDefault(goroot, "/opt/"+goVersion))
	f.put(filepath.Join(f.bin, "go"), script)
	if err := os.Chmod(filepath.Join(f.bin, "go"), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// goExt writes a Go extension directory.
func (f *fixture) goExt(dir, module, sdkPath string) {
	f.t.Helper()
	f.put(filepath.Join(dir, "go.mod"), fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire %s v0.4.0\n", module, sdkPath))
	f.put(filepath.Join(dir, "extension.go"), fmt.Sprintf("package ext\n\nimport sdk %q\n\nvar _ = sdk.New\n", sdkPath))
}

// brokenExt writes an extension that cannot build: a go.mod and no Go source.
func (f *fixture) brokenExt(dir, module string) {
	f.t.Helper()
	f.put(filepath.Join(dir, "go.mod"), fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire %s v0.0.0\n", module, sdkModule))
}

func (f *fixture) settings(body string) string {
	f.t.Helper()
	return f.put(filepath.Join(f.agent, "settings.json"), body)
}

func (f *fixture) readSettings() string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.agent, "settings.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

// pkg writes a Package checkout with the given extension names.
func (f *fixture) pkg(dir, name string, exts ...string) string {
	f.t.Helper()
	f.put(filepath.Join(dir, "package.json"), fmt.Sprintf(`{"name":%q,"version":"1.0.0","pi":{"extensions":["extensions/*"]}}`, name))
	for _, e := range exts {
		f.brokenExt(filepath.Join(dir, "extensions", e), "example.com/"+e)
	}
	return dir
}

var thirteen = []string{"ask", "context-info", "session-ingest", "subagent", "web-search", "multi-edit", "pigish", "secret-paste", "token-optimizer", "worktree", "herdr-state", "grill", "overlay"}

// Paths of the real-case fixture.
type realCase struct {
	*fixture
	pkgA, pkgB string
}

// realCase reproduces the owner's audited ~/.pig (see the task text) inside a fixture.
func newRealCase(t *testing.T) *realCase {
	f := newFixture(t)
	rc := &realCase{fixture: f}
	rc.pkgA = f.pkg(filepath.Join(f.user, "utils", "pig-stuff"), "pig-stuff", "ask")
	rc.pkgB = f.pkg(filepath.Join(f.user, "utils-spec-revision", "pig-stuff"), "pig-stuff", thirteen...)
	f.settings(fmt.Sprintf(`{
    "defaultProvider": "anthropic",
    "theme": "dark",
    "packages": [
        {
            "source": %q,
            "extensions": ["extensions/ask"]
        },
        %q
    ],
    "pigHighScore": 12
}
`, rc.pkgA, rc.pkgB))
	f.put("state/extension-logs/ask.log", "starting\ncompile: version \"go1.26.7\" does not match go tool version \"go1.26.1\"\n")
	f.fakeGo("go1.26.1", "go1.26.7", "")

	// legacy directory and extensions.toml
	for _, n := range []string{"ask", "context-info", "session-ingest", "subagent", "web-search"} {
		f.brokenExt(filepath.Join("extensions", n), "example.com/legacy-"+n)
	}
	f.put("extensions.toml", "[extensions.ask]\nhandler_timeout = \"30s\"\n")
	// a user extension in the agent directory
	f.goExt(filepath.Join(f.agent, "extensions", "mine"), "example.com/mine", sdkModule)

	// caches
	for _, n := range []string{"old1", "old2"} {
		f.cell("cache/cells/go/"+n, 100, true)
	}
	f.cell("cache/cells/go/recent", 2, true)
	f.cell("cache/cells/go/inuse", 100, true)
	f.put("cache/cells/go/.locks/inuse.usage.lock", "")
	f.put("cache/cells/go/.locks/old1.usage.lock", "")
	f.cell("cache/cells/go/incomplete-old", 5, false)
	f.cell("cache/cells/go/incomplete-new", 0, false)
	f.cell("cache/ext/001-aaaa", 90, true)
	f.cell("cache/ext/api-surface-bbbb", 90, true)
	f.cell("cache/ext/recent-cccc", 1, true)
	f.put("cache/toolchains/x.json", "{}")
	f.put("artifacts/piglets/kinsy/d1/unversioned/linux-amd64/bin", strings.Repeat("x", 4096))
	f.put("artifacts/piglets/kinsy/d2/unversioned/linux-amd64/bin", strings.Repeat("x", 4096))
	f.put("artifacts/piglets/pig-standard/d1/unversioned/linux-amd64/bin", strings.Repeat("x", 4096))
	f.put("artifacts/piglets/pig-standard/d2/unversioned/linux-amd64/bin", strings.Repeat("x", 4096))
	f.put("artifacts/piglets/pig-standard/d3/unversioned/linux-amd64/bin", strings.Repeat("x", 4096))
	f.age("artifacts/piglets/kinsy/d1", 90)
	f.age("artifacts/piglets/kinsy/d2", 1)
	f.age("artifacts/piglets/pig-standard/d1", 90)
	f.age("artifacts/piglets/pig-standard/d2", 60)
	f.age("artifacts/piglets/pig-standard/d3", 1)
	f.put("piglet-binary-cells/isolated/go/subagent-388b/runner", strings.Repeat("y", 2048))
	f.age("piglet-binary-cells/isolated/go/subagent-388b", 60)
	f.mkdir("piglet-cells")

	// orphaned agent dirs and automation output
	f.put("launch-workers-agent/auth.json", canary)
	f.put("launch-workers-agent/auth.json.lock", "")
	f.put("launch-workers-agent/settings.json", `{"defaultModel":"x"}`)
	f.put("launch-workers-agent/sessions/--a--/s.jsonl", "{}\n")
	f.age("launch-workers-agent", 60)
	f.put("kagent-pr-agent/settings.json", `{"theme":"dark"}`)
	f.put("kagent-pr-agent/models-store.json", "{}")
	f.age("kagent-pr-agent", 30)
	f.put("pigpen-agent/sessions/--b--/s.jsonl", "{}\n")
	f.put("pigpen-agent/settings.json", "{}")
	f.age("pigpen-agent", 20)
	for i := 0; i < 20; i++ {
		f.put(fmt.Sprintf("subagent-output/out-%02d.txt", i), "output")
	}
	f.age("subagent-output", 45)
	f.put("worktree-claims/wt-1.json", "{}")
	f.age("worktree-claims", 45)
	f.put("recent-agent/settings.json", "{}") // young: not an orphan

	// unknown to the doctor: reported, never touched
	f.put("harness-overlay/overlay.json", "{}")
	f.put("vendor.json", "{}")
	f.put("piglet-cli/bin", "x")
	f.put("receipts/piglets/x/receipt.json", "{}")
	f.put("marketplace.json", "{}")
	f.put("marketplace/index.json", "{}")
	for _, u := range []string{"harness-overlay", "vendor.json", "piglet-cli", "receipts", "marketplace.json", "marketplace"} {
		f.age(u, 400) // old, so only the "unknown" rule protects them
	}

	// files the doctor must never touch or read
	f.put(filepath.Join(f.agent, "auth.json"), canary)
	f.put(filepath.Join(f.agent, "auth.json.lock"), "")
	f.put(filepath.Join(f.agent, "trust.json"), `{"trusted":[]}`)
	f.put(filepath.Join(f.agent, "models.json"), `{"providers":{}}`)
	f.put(filepath.Join(f.agent, "models-store.json"), `{}`)
	f.put(filepath.Join(f.agent, "sessions", "--p--", "s.jsonl"), "{}\n")
	f.put(filepath.Join(f.agent, "skills", "foo", "SKILL.md"), "# skill\n")
	f.put(filepath.Join(f.agent, "prompts", "p.md"), "prompt\n")
	f.put(filepath.Join(f.agent, "AGENTS.md"), "rules\n")
	f.age(f.agent+"/auth.json", 200)
	f.age(f.agent+"/sessions", 200)
	f.age(f.agent+"/skills", 200)
	f.age(f.agent+"/prompts", 200)
	f.age(f.agent+"/trust.json", 200)
	f.age(f.agent+"/models.json", 200)
	f.age("state", 1)

	// the old ones must look old
	for _, n := range []string{"old1", "old2", "inuse"} {
		f.age("cache/cells/go/"+n, 100)
	}
	f.age("extensions", 400)
	f.age("extensions.toml", 400)
	return rc
}

// cell writes a cache entry the way PiG does: ready.json and usage.json.
func (f *fixture) cell(rel string, daysSinceUse float64, ready bool) {
	f.t.Helper()
	f.put(rel+"/payload", strings.Repeat("z", 1000))
	if ready {
		f.put(rel+"/ready.json", `{"artifact":"runner"}`)
		last := f.now.Add(-time.Duration(daysSinceUse * 24 * float64(time.Hour))).Unix()
		f.put(rel+"/usage.json", fmt.Sprintf(`{"lastUsed":%d}`, last))
	}
	f.age(rel, daysSinceUse)
}

// holdLock takes an flock on path (creating it) until the returned release is called.
func (f *fixture) holdLock(path string) func() {
	f.t.Helper()
	if !filepath.IsAbs(path) {
		path = filepath.Join(f.home, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := syscall.Flock(int(fh.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		f.t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_ = syscall.Flock(int(fh.Fd()), syscall.LOCK_UN)
			_ = fh.Close()
		}
	}
	f.t.Cleanup(release)
	return release
}

// fakeProcs is a ProcSource with a fixed list.
type fakeProcs struct{ list []Proc }

func (p *fakeProcs) List() ([]Proc, error) { return p.list, nil }

// snapshot records every entry below the roots without following symlinks.
func snapshot(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			switch {
			case info.Mode()&fs.ModeSymlink != 0:
				target, _ := os.Readlink(p)
				out[p] = "symlink->" + target
			case info.IsDir():
				out[p] = "dir"
			default:
				b, _ := os.ReadFile(p)
				sum := sha256.Sum256(b)
				out[p] = fmt.Sprintf("file %o %s", info.Mode().Perm(), hex.EncodeToString(sum[:8]))
			}
			return nil
		})
	}
	return out
}

func diffSnapshots(a, b map[string]string) []string {
	var d []string
	for k, v := range a {
		if w, ok := b[k]; !ok {
			d = append(d, "removed: "+k)
		} else if v != w {
			d = append(d, "changed: "+k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			d = append(d, "added: "+k)
		}
	}
	sort.Strings(d)
	return d
}

func mustCheck(t *testing.T, o Options) *Report {
	t.Helper()
	r, err := Check(o)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return r
}

func mustPlan(t *testing.T, r *Report, groups ...string) *Plan {
	t.Helper()
	p, err := BuildPlan(r, Selection{Groups: groups})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	return p
}

func yes(Group) (bool, error) { return true, nil }

func groupByID(p *Plan, id string) *Group {
	for i := range p.Groups {
		if p.Groups[i].ID == id {
			return &p.Groups[i]
		}
	}
	return nil
}

func hasOp(g *Group, kind OpKind, path string) bool {
	if g == nil {
		return false
	}
	for _, o := range g.Ops {
		if o.Kind == kind && o.Path == path {
			return true
		}
	}
	return false
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
