package acp

// Tests for the update notice (the original's buildUpdateNotice, agent.ts), including its
// PiG-specific parts: which package `npm view` asks about, which switches turn the lookup off, and
// how a PiG version with build metadata ("0.5.1+1.3.0.1402973a") is compared.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func notice(latest, installed string) string {
	return "New version available: v" + latest + " (installed v" + installed +
		"). Run: `pig update` (installed with npm: `npm i -g @pi-in-go/pig`)"
}

// fakePigPrinting writes an executable that prints out for any arguments (`pig --version`).
func fakePigPrinting(t *testing.T, out string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake pig is a POSIX shell script")
	}
	script := filepath.Join(t.TempDir(), "fakepig")
	write(t, script, "#!/bin/sh\necho '"+out+"'\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

// npmStub puts an `npm` first on PATH that records its arguments, then sleeps, prints out and exits
// with code. The returned func reports how many times npm was started and with which arguments.
func npmStub(t *testing.T, out string, code int, delay time.Duration) (calls func() []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake npm is a POSIX shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	body := "#!/bin/sh\necho \"$*\" >> '" + log + "'\n"
	if delay > 0 {
		body += fmt.Sprintf("sleep %.2f\n", delay.Seconds())
	}
	body += "printf '%s' '" + out + "'\nexit " + fmt.Sprint(code) + "\n"
	write(t, filepath.Join(dir, "npm"), body)
	if err := os.Chmod(filepath.Join(dir, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// A lookup is on unless a test turns it off.
	for _, k := range []string{"PIG_OFFLINE", "PI_OFFLINE", "PI_SKIP_VERSION_CHECK"} {
		t.Setenv(k, "")
	}
	return func() []string {
		raw, _ := os.ReadFile(log)
		if strings.TrimSpace(string(raw)) == "" {
			return nil
		}
		return strings.Split(strings.TrimSpace(string(raw)), "\n")
	}
}

func TestCompareSemver(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0.6.0", "0.5.9", 1},
		{"0.5.9", "0.6.0", -1},
		{"1.0.0", "1.0.0", 0},
		{"1.2.10", "1.2.9", 1},
		{"0.5.2", "0.5.1+1.3.0.1402973a", 1}, // build metadata does not hide a patch release
		{"0.5.1+1.3.0.1402973a", "0.5.1", 0}, // and is not part of the comparison
		{"0.6.0-rc.1", "0.6.0", 0},           // a pre-release is not "greater" (the original's comment)
		{"1.0.0", "0.99.99-beta.2", 1},
	} {
		if got := compareSemver(tc.a, tc.b); got != tc.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestIsSemver(t *testing.T) {
	for v, want := range map[string]bool{
		"1.2.3": true, "0.5.1+1.3.0.1402973a": true, "1.0.0-rc.1": true,
		"1.2": false, "v1.2.3": false, "dev": false, "": false, "1.2.3.4": false, "a.b.c": false,
	} {
		if got := isSemver(v); got != want {
			t.Errorf("isSemver(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestUpdateNotice(t *testing.T) {
	t.Run("a newer release on npm gives the notice, from `npm view @pi-in-go/pig version`", func(t *testing.T) {
		calls := npmStub(t, "0.6.0\n", 0, 0)
		got := BuildUpdateNotice(fakePigPrinting(t, "0.5.0+1.3.0.1402973a"), nil)
		if want := notice("0.6.0", "0.5.0"); got != want {
			t.Errorf("notice = %q, want %q", got, want)
		}
		if c := calls(); len(c) != 1 || c[0] != "view @pi-in-go/pig version" {
			t.Errorf("npm calls = %q", c)
		}
	})
	t.Run("a patch release is noticed although pig's version carries build metadata", func(t *testing.T) {
		npmStub(t, "0.5.2", 0, 0)
		if got, want := BuildUpdateNotice(fakePigPrinting(t, "0.5.1+1.3.0.1402973a"), nil), notice("0.5.2", "0.5.1"); got != want {
			t.Errorf("notice = %q, want %q", got, want)
		}
	})
	t.Run("a leading v is dropped from both versions", func(t *testing.T) {
		npmStub(t, "v0.6.0", 0, 0)
		if got, want := BuildUpdateNotice(fakePigPrinting(t, "v0.5.0"), nil), notice("0.6.0", "0.5.0"); got != want {
			t.Errorf("notice = %q, want %q", got, want)
		}
	})
	for name, tc := range map[string]struct{ installed, latest string }{
		"the same version gives none":                     {"0.5.2+1.3.0.1402973a", "0.5.2"},
		"an installed version ahead of npm":               {"0.7.0+1.3.0.1402973a", "0.6.0"},
		"a pre-release of the latest is not older":        {"0.6.0-rc.1", "0.6.0"},
		"a latest that is not a version gives none":       {"0.5.0", "next"},
		"an empty latest gives none":                      {"0.5.0", ""},
		"a latest with fewer than three parts gives none": {"0.5.0", "9"},
		"npm's error text is not a version":               {"0.5.0", "npm ERR! 404 Not Found"},
	} {
		t.Run(name, func(t *testing.T) {
			npmStub(t, tc.latest, 0, 0)
			if got := BuildUpdateNotice(fakePigPrinting(t, tc.installed), nil); got != "" {
				t.Errorf("notice = %q, want none", got)
			}
		})
	}
	t.Run("an installed version that is not semver skips the lookup", func(t *testing.T) {
		calls := npmStub(t, "9.9.9", 0, 0)
		if got := BuildUpdateNotice(fakePigPrinting(t, "dev"), nil); got != "" {
			t.Errorf("notice = %q", got)
		}
		if len(calls()) != 0 {
			t.Errorf("npm calls = %q, want none", calls())
		}
	})
	t.Run("a pig that does not answer --version skips the lookup", func(t *testing.T) {
		calls := npmStub(t, "9.9.9", 0, 0)
		if got := BuildUpdateNotice("no-such-pig-binary", nil); got != "" || len(calls()) != 0 {
			t.Errorf("notice = %q, npm calls = %q", got, calls())
		}
	})
	t.Run("npm failing gives none", func(t *testing.T) {
		npmStub(t, "9.9.9", 1, 0)
		if got := BuildUpdateNotice(fakePigPrinting(t, "0.5.0"), nil); got != "" {
			t.Errorf("notice = %q, want none", got)
		}
	})
	t.Run("npm slower than the lookup timeout gives none, in time", func(t *testing.T) {
		old := updateLookupTimeout
		updateLookupTimeout = 100 * time.Millisecond
		t.Cleanup(func() { updateLookupTimeout = old })
		npmStub(t, "0.6.0", 0, 3*time.Second)
		start := time.Now()
		if got := BuildUpdateNotice(fakePigPrinting(t, "0.5.0"), nil); got != "" {
			t.Errorf("notice = %q", got)
		}
		if took := time.Since(start); took > time.Second {
			t.Errorf("lookup took %v", took)
		}
	})
	t.Run("no npm on PATH gives none", func(t *testing.T) {
		pig := fakePigPrinting(t, "0.5.0")
		for _, k := range []string{"PIG_OFFLINE", "PI_OFFLINE", "PI_SKIP_VERSION_CHECK"} {
			t.Setenv(k, "")
		}
		t.Setenv("PATH", filepath.Dir(pig)+string(os.PathListSeparator)+"/bin:/usr/bin")
		if _, err := exec.LookPath("npm"); err == nil {
			t.Skip("npm is installed in /bin or /usr/bin")
		}
		if got := BuildUpdateNotice(pig, nil); got != "" {
			t.Errorf("notice = %q", got)
		}
	})
}

func TestUpdateNoticeOfflineSwitches(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		args []string
	}{
		"PIG_OFFLINE=1":           {env: map[string]string{"PIG_OFFLINE": "1"}},
		"PIG_OFFLINE=true":        {env: map[string]string{"PIG_OFFLINE": "true"}},
		"PIG_OFFLINE=YES":         {env: map[string]string{"PIG_OFFLINE": "YES"}},
		"PI_OFFLINE=1":            {env: map[string]string{"PI_OFFLINE": "1"}},
		"PI_SKIP_VERSION_CHECK=1": {env: map[string]string{"PI_SKIP_VERSION_CHECK": "1"}},
		"--offline passed to pig": {args: []string{"--no-themes", "--offline"}},
	} {
		t.Run(name+" turns the lookup off", func(t *testing.T) {
			calls := npmStub(t, "9.9.9", 0, 0)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := BuildUpdateNotice(fakePigPrinting(t, "0.5.0"), tc.args); got != "" || len(calls()) != 0 {
				t.Errorf("notice = %q, npm calls = %q", got, calls())
			}
		})
	}
	t.Run("PIG_OFFLINE=0 and an unrelated argument leave it on", func(t *testing.T) {
		calls := npmStub(t, "9.9.9", 0, 0)
		t.Setenv("PIG_OFFLINE", "0")
		got := BuildUpdateNotice(fakePigPrinting(t, "0.5.0"), []string{"--piglet", "offline"})
		if got != notice("9.9.9", "0.5.0") || len(calls()) != 1 {
			t.Errorf("notice = %q, npm calls = %q", got, calls())
		}
	})
}

func TestStartupInfoCarriesTheUpdateNotice(t *testing.T) {
	const n = "New version available: v9.9.9 (installed v1.0.0). Run: `x`"
	t.Run("the full startup block ends with a rule and the notice", func(t *testing.T) {
		withAgentDir(t, t.TempDir())
		t.Setenv("HOME", t.TempDir())
		got := BuildStartupInfo(t.TempDir(), "no-such-pig-binary", n)
		if got != "---\n"+n+"\n" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("the notice follows the sections", func(t *testing.T) {
		agent, project := t.TempDir(), t.TempDir()
		withAgentDir(t, agent)
		t.Setenv("HOME", t.TempDir())
		write(t, filepath.Join(project, "AGENTS.md"), "x")
		got := BuildStartupInfo(project, "no-such-pig-binary", n)
		if want := "## Context\n- " + filepath.Join(project, "AGENTS.md") + "\n\n---\n" + n + "\n"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestNewSessionUpdateNotice(t *testing.T) {
	const n = "New version available: v9.9.9 (installed v1.0.0). Run: `x`"
	setup := func(t *testing.T, quiet bool) (*Agent, *scheduler, *fakeSession) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "settings.json"), jl(map[string]any{"quietStartup": quiet}))
		withAgentDir(t, dir)
		t.Setenv("HOME", t.TempDir())
		proc := newFakeProc()
		proc.getLevelsFn = func() ([]string, error) { return allLevels, nil }
		proc.getModelsFn = func() (map[string]any, error) { return models([3]string{"test", "model", "model"}), nil }
		proc.getStateFn = func() (map[string]any, error) {
			return map[string]any{"thinkingLevel": "medium", "model": map[string]any{"provider": "test", "id": "model"}}, nil
		}
		proc.getCommandsFn = func() (map[string]any, error) { return map[string]any{"commands": []any{}}, nil }
		sess := &fakeSession{id: "s1", cwd: cwdNow(t), proc: proc}
		a, sch := testAgent(newFakeConn())
		a.sessions = &fakeSessions{session: sess}
		a.updateNotice = func() string { return n }
		return a, sch, sess
	}
	t.Run("quietStartup=true still surfaces a New version available notice", func(t *testing.T) {
		a, sch, sess := setup(t, true)
		res, err := a.NewSession(newSessionReq(cwdNow(t)))
		if err != nil {
			t.Fatal(err)
		}
		if got := startupInfoOf(res.Meta); got != n+"\n" {
			t.Errorf("startupInfo = %#v", got)
		}
		if len(sess.startupInfo) != 1 || sess.startupInfo[0] != n+"\n" {
			t.Errorf("setStartupInfo calls = %q", sess.startupInfo)
		}
		if sch.count() != 2 {
			t.Errorf("%d scheduled tasks, want the startup info and the available_commands_update", sch.count())
		}
	})
	t.Run("without quietStartup the notice ends the startup block", func(t *testing.T) {
		a, sch, sess := setup(t, false)
		res, err := a.NewSession(newSessionReq(cwdNow(t)))
		if err != nil {
			t.Fatal(err)
		}
		got, _ := startupInfoOf(res.Meta).(string)
		if !strings.HasSuffix(got, "\n---\n"+n+"\n") && got != "---\n"+n+"\n" {
			t.Errorf("startupInfo = %q", got)
		}
		if len(sess.startupInfo) != 1 || sess.startupInfo[0] != got || sch.count() != 2 {
			t.Errorf("setStartupInfo = %q, tasks = %d", sess.startupInfo, sch.count())
		}
	})
	t.Run("no notice and quietStartup=true: no startup info at all", func(t *testing.T) {
		a, sch, sess := setup(t, true)
		a.updateNotice = func() string { return "" }
		res, err := a.NewSession(newSessionReq(cwdNow(t)))
		if err != nil {
			t.Fatal(err)
		}
		if startupInfoOf(res.Meta) != nil || len(sess.startupInfo) != 0 || sch.count() != 1 {
			t.Errorf("startupInfo = %v, calls = %q, tasks = %d", res.Meta, sess.startupInfo, sch.count())
		}
	})
	t.Run("session/load never looks for an update", func(t *testing.T) {
		// The twin "does not emit startup info on loadSession" covers the response; here the lookup itself.
		var called atomic.Bool
		a, _, _ := setup(t, false)
		a.updateNotice = func() string { called.Store(true); return n }
		a.store = newMemStore(StoredSession{SessionID: "s1", Cwd: "/tmp/project", SessionFile: "/tmp/s.jsonl"})
		a.spawn = func(SpawnParams) (Proc, error) { return newFakeProc(), nil }
		a.sessions = NewSessionManager(a.spawn, a.store)
		_, _ = a.LoadSession(LoadSessionRequest{SessionID: "s1", Cwd: "/tmp/project", McpServers: []any{}})
		if called.Load() {
			t.Error("LoadSession asked for an update notice")
		}
	})
}

// BenchmarkBuildUpdateNotice measures the whole lookup against a stub npm and a stub pig: the two child processes
// the original also starts.
func BenchmarkBuildUpdateNotice(b *testing.B) {
	if runtime.GOOS == "windows" {
		b.Skip("the fakes are POSIX shell scripts")
	}
	dir := b.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "npm"), []byte("#!/bin/sh\nprintf 0.6.0\n"), 0o755); err != nil {
		b.Fatal(err)
	}
	pig := filepath.Join(dir, "fakepig")
	if err := os.WriteFile(pig, []byte("#!/bin/sh\necho '0.5.0+1.3.0.1402973a'\n"), 0o755); err != nil {
		b.Fatal(err)
	}
	for _, k := range []string{"PIG_OFFLINE", "PI_OFFLINE", "PI_SKIP_VERSION_CHECK"} {
		b.Setenv(k, "")
	}
	b.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if BuildUpdateNotice(pig, nil) == "" {
			b.Fatal("no notice")
		}
	}
}

func BenchmarkCompareSemver(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if compareSemver("0.6.0", "0.5.1+1.3.0.1402973a") != 1 || !isSemver("0.5.1+1.3.0.1402973a") {
			b.Fatal("wrong")
		}
	}
}
