package eq

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every lane wrote its own env.sh, gowork.sh and PiG source snapshot (four times in a
// day). `pigeq env` and `pigeq source` produce them, with rule 17 isolation built in.
func TestEnvScriptIsolatesAndKeepsTheToolchain(t *testing.T) {
	root := t.TempDir()
	script := EnvScript(EnvSpec{
		Root: root, GoRoot: "/opt/go", GoCache: "/cache/go-build", GoModCache: "/cache/mod",
		NodeDir: "/opt/node/bin", Pig: "/bin/pig", Pi: "/bin/pi", Source: "/src/pig", SDKDir: "/sdk", GoWork: "/w/go.work",
	})
	cmd := exec.Command("sh", "-c", ". \"$0\" && env", writeScript(t, script))
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/home/real", "PIG_SDK_GO_ROOT=/home/real/.pig/state/pigsdk/sdk", "PIG_HOME=/home/real/.pig", "PIG_CODING_AGENT_DIR=/home/real/.pig/agent", "MISE_DATA_DIR=/home/real/.local/share/mise"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	env := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			env[k] = v
		}
	}
	for k, want := range map[string]string{
		"HOME": root + "/home", "PIG_HOME": root + "/pighome", "PIG_CODING_AGENT_DIR": root + "/agent", "PI_CODING_AGENT_DIR": root + "/agent",
		"GOCACHE": "/cache/go-build", "GOMODCACHE": "/cache/mod", "GOROOT": "/opt/go", "GIT_TERMINAL_PROMPT": "0",
		"PIG_BIN": "/bin/pig", "PIGEQ_PIG": "/bin/pig", "PIGEQ_PI": "/bin/pi", "PIG_SOURCE_ROOT": "/src/pig", "PIG_SDK_DIR": "/sdk", "GOWORK": "/w/go.work",
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
	if _, leaked := env["PIG_SDK_GO_ROOT"]; leaked {
		t.Error("PIG_SDK_GO_ROOT points at the real ~/.pig cache and must be unset")
	}
	if !strings.HasPrefix(env["PATH"], "/opt/go/bin:/opt/node/bin:") {
		t.Errorf("PATH = %q: the real go and node must come first (a redirected HOME breaks version-manager shims)", env["PATH"])
	}
	for _, d := range []string{"home", "pighome", "agent"} {
		if fi, err := os.Stat(filepath.Join(root, d)); err != nil || !fi.IsDir() {
			t.Errorf("%s directory not created", d)
		}
	}
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "env.sh")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnvScriptQuotesPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "a b'c")
	cmd := exec.Command("sh", "-c", ". \"$0\" && printf %s \"$HOME\"", writeScript(t, EnvScript(EnvSpec{Root: root})))
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != root+"/home" {
		t.Errorf("%v %q", err, out)
	}
}

func TestWriteGoWorkMapsTheSDKAndModules(t *testing.T) {
	p := filepath.Join(t.TempDir(), "go.work")
	if err := WriteGoWork(p, "/sdk", []string{"/a/ext", "/a/ext/cmd/x"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "\t/sdk\n") {
		t.Errorf("the SDK must not be a use directive:\n%s", b)
	}
	// The SDK is a replace, not a use: with a third-party dependency in a module, a go.work that
	// only `use`s the SDK fails 'sdk@v0.0.0: unknown revision' (pigpen-a2a, websearch, ahp).
	for _, want := range []string{"use (", "\t/a/ext\n", "\t/a/ext/cmd/x\n", "replace github.com/MichaelKinsy/PiG/extensions/sdk => /sdk\n"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("go.work lacks %q:\n%s", want, b)
		}
	}
}

func TestSDKPathRunsPigIsolated(t *testing.T) {
	root := t.TempDir()
	fake := filepath.Join(t.TempDir(), "pig")
	_ = os.WriteFile(fake, []byte("#!/bin/sh\necho noise\necho \"$PIG_HOME/sdk\"\n"), 0o755)
	got, err := SDKPath(fake, root)
	if err != nil || got != filepath.Join(root, "pighome", "sdk") {
		t.Errorf("SDKPath = %q %v", got, err)
	}
}

func TestSnapshotSourceIsAGitCheckoutOfOneRevision(t *testing.T) {
	git := func(dir string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	repo := t.TempDir()
	git(repo, "init", "-q")
	_ = os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one"), 0o644)
	git(repo, "add", "-A")
	git(repo, "commit", "-qm", "one")
	rev := git(repo, "rev-parse", "HEAD")
	_ = os.WriteFile(filepath.Join(repo, "a.txt"), []byte("two"), 0o644)
	git(repo, "commit", "-qam", "two")

	out := filepath.Join(t.TempDir(), "snap")
	if err := SnapshotSource(repo, rev, out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "a.txt")); string(b) != "one" {
		t.Errorf("a.txt = %q, want the pinned revision", b)
	}
	if n := git(out, "rev-list", "--count", "HEAD"); n != "1" {
		t.Errorf("snapshot has %s commits, want 1 (a git checkout, as PiG's native Binary builder needs)", n)
	}
	if git(out, "status", "--porcelain") != "" {
		t.Error("snapshot must be clean")
	}
	if err := SnapshotSource(repo, rev, out); err == nil {
		t.Error("an existing output directory must not be overwritten")
	}
	if err := SnapshotSource(repo, "deadbeef", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("an unknown revision was accepted")
	}
}

// pigpen-a2a: `go 1.26` in a go.work sorts below `go 1.26.0`, so a module that requires a
// dependency built for 1.26.0 failed "module . listed in go.work file requires go >= 1.26.0, but
// go.work lists go 1.26". The go line is the highest among the SDK and the used modules.
func TestGoWorkTakesTheHighestGoDirective(t *testing.T) {
	mk := func(directive string) string {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo "+directive+"\n"), 0o644)
		return dir
	}
	sdk, low, high := mk("1.26"), mk("1.26"), mk("1.26.3")
	if got := GoWorkFor(sdk, []string{low, high}); !strings.HasPrefix(got, "go 1.26.3\n") {
		t.Errorf("go.work:\n%s", got)
	}
	if got := GoWorkFor(mk("1.27"), []string{low}); !strings.HasPrefix(got, "go 1.27\n") {
		t.Errorf("SDK directive ignored:\n%s", got)
	}
	if got := GoWorkFor("/missing/sdk", []string{"/missing/mod"}); !strings.HasPrefix(got, "go 1.26\n") {
		t.Errorf("no go.mod files: %q", got)
	}
	if !goVersionLess("1.26", "1.26.0") || goVersionLess("1.26.0", "1.26") || !goVersionLess("1.9.5", "1.10") || goVersionLess("1.27", "1.26.9") {
		t.Error("goVersionLess is wrong")
	}
}

// pigpen-jev and pigpen-typesafe-client: after `HOME=<tmp>` a `~/...` path in the same shell goes
// to the temp home; pigpen-pig-snake: lanes chose the same /tmp/<name> and overwrote each other's
// env.sh; pigpen-jev: the Pi oracle path in the docs was empty and nothing said which Pi to use.
func TestEnvScriptKeepsTheRealHomeAvailable(t *testing.T) {
	script := EnvScript(EnvSpec{Root: t.TempDir()})
	real, redirect := strings.Index(script, "PIGEQ_REAL_HOME"), strings.Index(script, "export HOME=")
	if real < 0 || redirect < 0 || real > redirect {
		t.Errorf("the real home must be saved before HOME is redirected:\n%s", script)
	}
	if !strings.Contains(script, "absolute") {
		t.Errorf("the script should say to use absolute paths after sourcing:\n%s", script)
	}
}

func TestPiMatchesThePigItIsComparedWith(t *testing.T) {
	bin := t.TempDir()
	mk := func(name, out string) string {
		p := filepath.Join(bin, name)
		_ = os.WriteFile(p, []byte("#!/bin/sh\necho '"+out+"'\n"), 0o755)
		return p
	}
	pig, pi, wrong := mk("pig", "0.3.0+0.87.1"), mk("pi", "0.87.1"), mk("pi-old", "0.84.0")
	if err := CheckPiVersion(pi, pig); err != nil {
		t.Errorf("matching Pi rejected: %v", err)
	}
	if err := CheckPiVersion(wrong, pig); err == nil || !strings.Contains(err.Error(), "0.84.0") || !strings.Contains(err.Error(), "0.87.1") {
		t.Errorf("a Pi of another version was accepted: %v", err)
	}
	if err := CheckPiVersion(pi, mk("pig-plain", "0.3.0")); err != nil {
		t.Errorf("a pig that does not report its Pi version cannot be checked, not rejected: %v", err)
	}
}

// pigpen-jev: `pigeq check --pig <a built Piglet Binary>` loaded the extension a second time
// (-e), so the Binary itself could not be proved against the goldens. --builtin: the port is
// already inside the Pig binary.
func TestBuiltinPortRunsWithoutLoadingAnExtension(t *testing.T) {
	lane, kind, err := (Config{Pig: "/bin/pig", Self: true, Builtin: true}).oracle()
	if err != nil || kind != OracleSelf || lane.Ext != "" {
		t.Errorf("lane %+v kind %q err %v", lane, kind, err)
	}
	lane, _, _ = (Config{Pig: "/bin/pig", Self: true, Go: "/x/ext"}).oracle()
	if lane.Ext != "/x/ext" {
		t.Errorf("a port loaded from a directory must still be loaded: %+v", lane)
	}
}

// pigpen-a2a ran `check --builtin` on goldens recorded with the extension loaded (-e): the four
// scenarios with a scripted model failed at the first `llm` event (the loaded port's request is
// diffed against a host without it; the fused Binary has no such host). The two modes each
// need their own golden, and the mismatch is named instead of shown as a trace diff.
func TestGoldenModeMustMatchHowThePortIsRun(t *testing.T) {
	loaded := Header{Lane: OracleSelf, Extension: "sha256:abc"}
	builtin := Header{Lane: OracleSelf, Extension: "none"}
	if err := goldenModeMatches("s", loaded, false); err != nil {
		t.Errorf("loaded golden, loaded check: %v", err)
	}
	if err := goldenModeMatches("s", builtin, true); err != nil {
		t.Errorf("builtin golden, builtin check: %v", err)
	}
	if err := goldenModeMatches("s", loaded, true); err == nil || !strings.Contains(err.Error(), "record --self --builtin") {
		t.Errorf("a loaded golden checked as builtin must be refused with the fix: %v", err)
	}
	if err := goldenModeMatches("s", builtin, false); err == nil || !strings.Contains(err.Error(), "--builtin") {
		t.Errorf("a builtin golden checked as loaded must be refused: %v", err)
	}
	// only a golden of the port itself has a mode; an original's golden is compared as before
	if err := goldenModeMatches("s", Header{Lane: OracleTS, Extension: "sha256:ts"}, true); err != nil {
		t.Errorf("an original's golden has no port mode: %v", err)
	}
}

// A module that requires another workspace module at v0.0.0 (a library Package used by a library Package)
// cannot resolve it when the SDK is a replace: every used module also gets a versioned replace to its own directory.
func TestGoWorkReplacesUsedModulesAtV0(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "go.mod"), []byte("module example.com/pigpen/lib\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	work := GoWork("/sdk", []string{lib, filepath.Join(dir, "no-go-mod")})
	if !strings.Contains(work, "replace example.com/pigpen/lib v0.0.0 => "+lib+"\n") {
		t.Fatalf("go.work lacks the versioned replace:\n%s", work)
	}
	if strings.Count(work, "replace ") != 2 {
		t.Fatalf("a directory without go.mod gets no replace:\n%s", work)
	}
}
