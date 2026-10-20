package warden

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func hasString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// twin: tests/audit-discovery.test.ts:21
func TestDiscoverRootLayout(t *testing.T) {
	p := t.TempDir()
	writeTree(t, p, map[string]string{"index.ts": "x", "utils.py": "x", "main.go": "x", "README.md": "x"})
	files := DiscoverSourceFiles(p, 200)
	for _, w := range []string{"index.ts", "utils.py", "main.go"} {
		if !hasString(files, w) {
			t.Fatalf("missing %s in %v", w, files)
		}
	}
	if hasString(files, "README.md") {
		t.Fatal("non-source included")
	}
}

// twin: tests/audit-discovery.test.ts:36
func TestDiscoverNested(t *testing.T) {
	p := t.TempDir()
	writeTree(t, p, map[string]string{"packages/server/handler.rs": "x", "packages/client/App.vue": "x", "packages/server/Cargo.toml": "x"})
	files := DiscoverSourceFiles(p, 200)
	if !hasString(files, "packages/server/handler.rs") || !hasString(files, "packages/client/App.vue") || hasString(files, "packages/server/Cargo.toml") {
		t.Fatalf("got %v", files)
	}
}

// twin: tests/audit-discovery.test.ts:50
func TestDiscoverSkipsHeavyDirs(t *testing.T) {
	p := t.TempDir()
	writeTree(t, p, map[string]string{".git/objects/pack.ts": "x", "node_modules/pkg/index.js": "x", "dist/bundle.js": "x", "build/output.ts": "x", "src/app.ts": "x"})
	if got := DiscoverSourceFiles(p, 200); !reflect.DeepEqual(got, []string{"src/app.ts"}) {
		t.Fatalf("got %v", got)
	}
}

// twin: tests/audit-discovery.test.ts:67
func TestDiscoverSkipsPiWarden(t *testing.T) {
	p := t.TempDir()
	writeTree(t, p, map[string]string{".pi-warden/audit-report.html": "x", "src/main.ts": "x"})
	if got := DiscoverSourceFiles(p, 200); !reflect.DeepEqual(got, []string{"src/main.ts"}) {
		t.Fatalf("got %v", got)
	}
}

// twin: tests/audit-discovery.test.ts:78
func TestDiscoverSkipsHidden(t *testing.T) {
	p := t.TempDir()
	writeTree(t, p, map[string]string{".vscode/settings.ts": "x", ".idea/config.js": "x", "src/app.ts": "x"})
	if got := DiscoverSourceFiles(p, 200); !reflect.DeepEqual(got, []string{"src/app.ts"}) {
		t.Fatalf("got %v", got)
	}
}

// twin: tests/audit-discovery.test.ts:91
func TestDiscoverMax(t *testing.T) {
	p := t.TempDir()
	m := map[string]string{}
	for i := 0; i < 10; i++ {
		m["file"+string(rune('0'+i))+".ts"] = "x"
	}
	writeTree(t, p, m)
	if got := DiscoverSourceFiles(p, 3); len(got) != 3 {
		t.Fatalf("got %v", got)
	}
}

// twin: tests/audit-discovery.test.ts:102
func TestDiscoverEmpty(t *testing.T) {
	if got := DiscoverSourceFiles(t.TempDir(), 200); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

// twin: tests/audit-discovery.test.ts:110
func TestDiscoverMissingDir(t *testing.T) {
	if got := DiscoverSourceFiles(filepath.Join(t.TempDir(), "nope", "nested"), 200); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

// twin: tests/audit-discovery.test.ts:117
func TestFindProjectsRoot(t *testing.T) {
	w := t.TempDir()
	writeTree(t, w, map[string]string{"package.json": "{}"})
	if !hasString(FindProjects(w), w) {
		t.Fatal("root not a project")
	}
}

// twin: tests/audit-discovery.test.ts:126
func TestFindProjectsSub(t *testing.T) {
	w := t.TempDir()
	writeTree(t, w, map[string]string{"a/package.json": "{}", "b/Cargo.toml": "[package]"})
	got := FindProjects(w)
	if !hasString(got, filepath.Join(w, "a")) || !hasString(got, filepath.Join(w, "b")) {
		t.Fatalf("got %v", got)
	}
}

// twin: tests/audit-discovery.test.ts:138
func TestFindProjectsSkips(t *testing.T) {
	w := t.TempDir()
	writeTree(t, w, map[string]string{"node_modules/pkg/package.json": "{}", ".git/config": "", "real/package.json": "{}"})
	got := FindProjects(w)
	for _, p := range got {
		if strings.Contains(p, "node_modules") || strings.Contains(p, ".git") {
			t.Fatalf("got %v", got)
		}
	}
	if !hasString(got, filepath.Join(w, "real")) {
		t.Fatalf("got %v", got)
	}
}

// twin: tests/audit-discovery.test.ts:155
func TestSourceRE(t *testing.T) {
	for _, n := range []string{"foo.ts", "foo.tsx", "foo.js", "foo.jsx", "foo.rs", "foo.py", "foo.go", "foo.vue", "foo.svelte", "foo.rb", "foo.php", "foo.java", "foo.kt", "foo.swift", "foo.cs", "foo.cpp", "foo.c", "foo.h"} {
		if !SourceRE.MatchString(n) {
			t.Fatalf("%s should match", n)
		}
	}
	for _, n := range []string{"foo.txt", "foo.md", "foo.json", "foo.yaml", "foo.css", "foo.html", "foo.toml", "foo.lock", "foo.jpg", "foo.png"} {
		if SourceRE.MatchString(n) {
			t.Fatalf("%s should not match", n)
		}
	}
}

// twin: tests/audit-discovery.test.ts:169
func TestSkipDirs(t *testing.T) {
	for _, d := range []string{".git", "node_modules", "dist", "build", ".next", "coverage", "__pycache__", ".pi-warden", "vendor"} {
		if !SkipDirs[d] {
			t.Fatalf("%s missing", d)
		}
	}
	keys := make([]string, 0, len(SkipDirs))
	for k := range SkipDirs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{".cache", ".git", ".netlify", ".next", ".output", ".pi-warden", ".svelte-kit", ".turbo", ".vercel", "__pycache__", "build", "coverage", "dist", "node_modules", "vendor"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("got %v", keys)
	}
}

// twin: tests/audit-discovery.test.ts:177
func TestBuildAuditPrompt(t *testing.T) {
	p := BuildAuditPrompt("/tmp/workspace", []string{"/tmp/workspace", "/tmp/workspace/packages/a"})
	for _, s := range []string{"Workspace Audit", "Jev", "file:line", ".pi-warden/audit-report.html", "unmeasured", "# Workspace Audit — workspace", "- workspace (0 source files)", "- a (0 source files)"} {
		if !strings.Contains(p, s) {
			t.Fatalf("missing %q", s)
		}
	}
}

// twin: tests/audit-discovery.test.ts:191
func TestReportOutcomeMissing(t *testing.T) {
	s := ReportSnapshot{}
	if ReportOutcome(s, s) != "missing" {
		t.Fatal()
	}
}

// twin: tests/audit-discovery.test.ts:196
func TestReportOutcomeNew(t *testing.T) {
	if ReportOutcome(ReportSnapshot{}, ReportSnapshot{true, 100}) != "written" {
		t.Fatal()
	}
}

// twin: tests/audit-discovery.test.ts:200
func TestReportOutcomeAdvanced(t *testing.T) {
	if ReportOutcome(ReportSnapshot{true, 100}, ReportSnapshot{true, 200}) != "written" {
		t.Fatal()
	}
}

// twin: tests/audit-discovery.test.ts:204
func TestReportOutcomeStale(t *testing.T) {
	if ReportOutcome(ReportSnapshot{true, 100}, ReportSnapshot{true, 100}) != "stale" {
		t.Fatal()
	}
}

// twin: tests/audit-discovery.test.ts:210
func TestSnapshotReport(t *testing.T) {
	p := filepath.Join(t.TempDir(), "report.html")
	os.WriteFile(p, []byte("<html>"), 0o644)
	a := SnapshotReport(p)
	if !a.Exists || a.MtimeMs <= 0 {
		t.Fatalf("%+v", a)
	}
	later := time.Now().Add(time.Hour)
	os.WriteFile(p, []byte("<html>updated</html>"), 0o644)
	os.Chtimes(p, later, later)
	if b := SnapshotReport(p); b.MtimeMs <= a.MtimeMs {
		t.Fatalf("%+v %+v", a, b)
	}
}

// twin: tests/audit-discovery.test.ts:225
func TestSnapshotReportMissing(t *testing.T) {
	if s := SnapshotReport("/nonexistent/path/to/report.html"); s.Exists || s.MtimeMs != 0 {
		t.Fatalf("%+v", s)
	}
}

// The prompt is the original's, byte for byte (testdata/audit-prompt.txt comes from port/gen-audit-prompt.ts run on the
// vendored original over the same fixture).
func TestBuildAuditPromptMatchesTheOriginal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	writeTree(t, root, map[string]string{
		"package.json": "{}", "index.ts": "x", "README.md": "x", "node_modules/p/skip.js": "x",
		"a/package.json": "{}", "a/one.ts": "x", "a/two.py": "x", "a/sub/three.go": "x",
		"b/Cargo.toml": "[package]", "b/main.rs": "x",
		"c/go.mod": "module c", "c/main.go": "x",
		"d/pyproject.toml": "[project]",
	})
	got := strings.ReplaceAll(BuildAuditPrompt(root, []string{root, filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "c"), filepath.Join(root, "d")}), root, "<cwd>")
	want, err := os.ReadFile("testdata/audit-prompt.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("prompt differs from the original's:\n%s", got)
	}
}

// discover.ts counts regular files only (Dirent.isFile is false for a symlink) and never enters a symlinked directory.
func TestDiscoverIgnoresSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	p := t.TempDir()
	writeTree(t, p, map[string]string{"real.ts": "x", "other/inner.ts": "x"})
	if err := os.Symlink(filepath.Join(p, "real.ts"), filepath.Join(p, "link.ts")); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(filepath.Join(p, "other"), filepath.Join(p, "dirlink")); err != nil {
		t.Skip(err)
	}
	got := DiscoverSourceFiles(p, 200)
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"other/inner.ts", "real.ts"}) {
		t.Fatalf("got %v", got)
	}
}

func TestFindProjectsIgnoresDotDirectoriesAndPlainFiles(t *testing.T) {
	w := t.TempDir()
	writeTree(t, w, map[string]string{".hidden/package.json": "{}", "plain/readme.md": "x", "real/go.mod": "module x"})
	if got := FindProjects(w); !reflect.DeepEqual(got, []string{filepath.Join(w, "real")}) {
		t.Fatalf("got %v", got)
	}
}
