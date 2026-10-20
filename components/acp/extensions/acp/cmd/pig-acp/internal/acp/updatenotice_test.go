package acp

// Tests for the update notice (the original's buildUpdateNotice, agent.ts), including its
// PiG-specific parts: where "latest" comes from, which switches turn the lookup off, and how a
// PiG version with build metadata ("0.5.1+1.3.0.1402973a") is compared.

import (
	"net/http"
	"net/http/httptest"
	"os"
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

// registry serves the npm registry's "latest" document for @pi-in-go/pig and counts requests.
func registry(t *testing.T, status int, body string, delay time.Duration) (hits *atomic.Int32) {
	t.Helper()
	hits = new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if got := r.URL.EscapedPath(); got != "/@pi-in-go%2Fpig/latest" {
			t.Errorf("registry path = %q, want /@pi-in-go%%2Fpig/latest", got)
		}
		if got := r.Header.Get("Accept"); !strings.Contains(got, "application/json") {
			t.Errorf("Accept = %q", got)
		}
		time.Sleep(delay)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("npm_config_registry", srv.URL)
	t.Setenv("NPM_CONFIG_REGISTRY", "")
	// A lookup is on unless a test turns it off.
	for _, k := range []string{"PIG_OFFLINE", "PI_OFFLINE", "PI_SKIP_VERSION_CHECK"} {
		t.Setenv(k, "")
	}
	return hits
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
		{"0.5.2", "0.5.1+1.3.0.1402973a", 1},    // build metadata does not hide a patch release
		{"0.5.1+1.3.0.1402973a", "0.5.1", 0},     // and is not part of the comparison
		{"0.6.0-rc.1", "0.6.0", 0},               // a pre-release is not "greater" (the original's comment)
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
	latestDoc := func(v string) string { return `{"name":"@pi-in-go/pig","version":"` + v + `"}` }

	t.Run("a newer release on the registry gives the notice", func(t *testing.T) {
		hits := registry(t, 200, latestDoc("0.6.0"), 0)
		got := BuildUpdateNotice(fakePigPrinting(t, "0.5.0+1.3.0.1402973a"), nil)
		if want := notice("0.6.0", "0.5.0"); got != want {
			t.Errorf("notice = %q, want %q", got, want)
		}
		if hits.Load() != 1 {
			t.Errorf("registry hits = %d, want 1", hits.Load())
		}
	})
	t.Run("a patch release is noticed although pig's version carries build metadata", func(t *testing.T) {
		registry(t, 200, latestDoc("0.5.2"), 0)
		if got, want := BuildUpdateNotice(fakePigPrinting(t, "0.5.1+1.3.0.1402973a"), nil), notice("0.5.2", "0.5.1"); got != want {
			t.Errorf("notice = %q, want %q", got, want)
		}
	})
	t.Run("a leading v is dropped from both versions", func(t *testing.T) {
		registry(t, 200, latestDoc("v0.6.0"), 0)
		if got, want := BuildUpdateNotice(fakePigPrinting(t, "v0.5.0"), nil), notice("0.6.0", "0.5.0"); got != want {
			t.Errorf("notice = %q, want %q", got, want)
		}
	})
	for name, tc := range map[string]struct{ installed, latest string }{
		"the same version gives none":                 {"0.5.2+1.3.0.1402973a", "0.5.2"},
		"an installed version ahead of the registry":  {"0.7.0+1.3.0.1402973a", "0.6.0"},
		"a pre-release of the latest is not older":    {"0.6.0-rc.1", "0.6.0"},
		"a latest that is not a version gives none":   {"0.5.0", "next"},
		"an empty latest gives none":                  {"0.5.0", ""},
	} {
		t.Run(name, func(t *testing.T) {
			registry(t, 200, latestDoc(tc.latest), 0)
			if got := BuildUpdateNotice(fakePigPrinting(t, tc.installed), nil); got != "" {
				t.Errorf("notice = %q, want none", got)
			}
		})
	}
	t.Run("an installed version that is not semver skips the lookup", func(t *testing.T) {
		hits := registry(t, 200, latestDoc("9.9.9"), 0)
		if got := BuildUpdateNotice(fakePigPrinting(t, "dev"), nil); got != "" {
			t.Errorf("notice = %q", got)
		}
		if hits.Load() != 0 {
			t.Errorf("registry hits = %d, want 0", hits.Load())
		}
	})
	t.Run("a pig that does not answer --version skips the lookup", func(t *testing.T) {
		hits := registry(t, 200, latestDoc("9.9.9"), 0)
		if got := BuildUpdateNotice("no-such-pig-binary", nil); got != "" || hits.Load() != 0 {
			t.Errorf("notice = %q, hits = %d", got, hits.Load())
		}
	})
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"a registry error gives none":      {500, "boom"},
		"an unknown package gives none":    {404, `{"error":"not found"}`},
		"a body that is not JSON gives none": {200, "<html>"},
		"a document without a version":     {200, `{"name":"@pi-in-go/pig"}`},
		"a version that is not a string":   {200, `{"version":6}`},
	} {
		t.Run(name, func(t *testing.T) {
			registry(t, tc.status, tc.body, 0)
			if got := BuildUpdateNotice(fakePigPrinting(t, "0.5.0"), nil); got != "" {
				t.Errorf("notice = %q, want none", got)
			}
		})
	}
	t.Run("a registry slower than the lookup timeout gives none, in time", func(t *testing.T) {
		old := updateLookupTimeout
		updateLookupTimeout = 100 * time.Millisecond
		t.Cleanup(func() { updateLookupTimeout = old })
		registry(t, 200, latestDoc("0.6.0"), 2*time.Second)
		start := time.Now()
		if got := BuildUpdateNotice(fakePigPrinting(t, "0.5.0"), nil); got != "" {
			t.Errorf("notice = %q", got)
		}
		if took := time.Since(start); took > time.Second {
			t.Errorf("lookup took %v", took)
		}
	})
	t.Run("an unreachable registry gives none", func(t *testing.T) {
		registry(t, 200, latestDoc("0.6.0"), 0)
		t.Setenv("npm_config_registry", "http://127.0.0.1:1")
		if got := BuildUpdateNotice(fakePigPrinting(t, "0.5.0"), nil); got != "" {
			t.Errorf("notice = %q", got)
		}
	})
	t.Run("NPM_CONFIG_REGISTRY selects the registry too, and a trailing slash is fine", func(t *testing.T) {
		hits := registry(t, 200, latestDoc("0.6.0"), 0)
		url := os.Getenv("npm_config_registry")
		t.Setenv("npm_config_registry", "")
		t.Setenv("NPM_CONFIG_REGISTRY", url+"/")
		if got := BuildUpdateNotice(fakePigPrinting(t, "0.5.0"), nil); got != notice("0.6.0", "0.5.0") || hits.Load() != 1 {
			t.Errorf("notice = %q, hits = %d", got, hits.Load())
		}
	})
}

func TestUpdateNoticeOfflineSwitches(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		args []string
	}{
		"PIG_OFFLINE=1":              {env: map[string]string{"PIG_OFFLINE": "1"}},
		"PIG_OFFLINE=true":           {env: map[string]string{"PIG_OFFLINE": "true"}},
		"PIG_OFFLINE=YES":            {env: map[string]string{"PIG_OFFLINE": "YES"}},
		"PI_OFFLINE=1":               {env: map[string]string{"PI_OFFLINE": "1"}},
		"PI_SKIP_VERSION_CHECK=1":    {env: map[string]string{"PI_SKIP_VERSION_CHECK": "1"}},
		"--offline passed to pig":    {args: []string{"--no-themes", "--offline"}},
	} {
		t.Run(name+" turns the lookup off", func(t *testing.T) {
			hits := registry(t, 200, `{"version":"9.9.9"}`, 0)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := BuildUpdateNotice(fakePigPrinting(t, "0.5.0"), tc.args); got != "" || hits.Load() != 0 {
				t.Errorf("notice = %q, hits = %d", got, hits.Load())
			}
		})
	}
	t.Run("PIG_OFFLINE=0 and an unrelated argument leave it on", func(t *testing.T) {
		hits := registry(t, 200, `{"version":"9.9.9"}`, 0)
		t.Setenv("PIG_OFFLINE", "0")
		got := BuildUpdateNotice(fakePigPrinting(t, "0.5.0"), []string{"--piglet", "offline"})
		if got != notice("9.9.9", "0.5.0") || hits.Load() != 1 {
			t.Errorf("notice = %q, hits = %d", got, hits.Load())
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
