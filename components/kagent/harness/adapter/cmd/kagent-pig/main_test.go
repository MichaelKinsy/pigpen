package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	runtimev1 "github.com/kagent-dev/kagent/go/api/gen/kagent/runtime/v1"
	"github.com/kagent-dev/kagent/go/pkg/logging"
)

// fakePigBinary is a stand-in `pig` that only answers `version`.
func fakePigBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pig")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo '0.7.0+1.5.0'; exit 0; fi\nexit 2\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func testDeps(t *testing.T, config []byte, env map[string]string) deps {
	t.Helper()
	environ := []string{}
	for k, v := range env {
		environ = append(environ, k+"="+v)
	}
	return deps{
		getenv:  func(name string) string { return env[name] },
		lookup:  func(name string) (string, bool) { v, ok := env[name]; return v, ok },
		environ: environ,
		bootstrap: func(context.Context, func(string) string) (*bootstrapBundle, error) {
			return &runtimev1.BootstrapBundle{ConfigJson: config, AgentCardJson: fixture(t, "agent-card.json")}, nil
		},
		dataDir: filepath.Join(t.TempDir(), "data"),
		pigHome: filepath.Join(t.TempDir(), "run", "pig"),
	}
}

func TestCheckAcceptsTheSampleBootstrapAndTouchesNothingDurable(t *testing.T) {
	env := map[string]string{"OPENAI_API_KEY": "kagent-credential-injected", "KAGENT_PIG_BIN": fakePigBinary(t)}
	d := testDeps(t, fixture(t, "config.json"), env)
	if err := run(t.Context(), true, d); err != nil {
		t.Fatalf("run -check: %v", err)
	}
	if _, err := os.Stat(d.dataDir); !os.IsNotExist(err) {
		t.Fatalf("-check created the data directory: %v", err)
	}
	if _, err := os.Stat(d.pigHome); !os.IsNotExist(err) {
		t.Fatalf("-check created PIG_HOME: %v", err)
	}
}

func TestCheckFailures(t *testing.T) {
	good := fixture(t, "config.json")
	okEnv := func() map[string]string {
		return map[string]string{"OPENAI_API_KEY": "kagent-credential-injected", "KAGENT_PIG_BIN": fakePigBinary(t)}
	}
	tests := []struct {
		name   string
		config []byte
		env    func() map[string]string
		want   string
	}{
		{"unsupported field", []byte(strings.Replace(string(good), `"instruction"`, `"memory": {"ttl_days": 1}, "instruction"`, 1)), okEnv, "agent.memory"},
		{"missing provider key", good, func() map[string]string { e := okEnv(); delete(e, "OPENAI_API_KEY"); return e }, "OPENAI_API_KEY"},
		{"missing pig", good, func() map[string]string { e := okEnv(); e["KAGENT_PIG_BIN"] = "/nonexistent/pig"; return e }, "KAGENT_PIG_BIN"},
		{"pig that fails", good, func() map[string]string {
			e := okEnv()
			bad := filepath.Join(t.TempDir(), "pig")
			os.WriteFile(bad, []byte("#!/bin/sh\nexit 1\n"), 0o755)
			e["KAGENT_PIG_BIN"] = bad
			return e
		}, "version"},
		{"bad tools variable", good, func() map[string]string { e := okEnv(); e["KAGENT_PIG_TOOLS"] = "read, bash"; return e }, "KAGENT_PIG_TOOLS"},
		{"no control plane", []byte(strings.Replace(string(good), `"control_plane_url": "http://kagent-controller.kagent:8083",`, "", 1)), okEnv, "control_plane_url"},
		{"not json", []byte("nope"), okEnv, "parse"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := run(t.Context(), true, testDeps(t, tc.config, tc.env()))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want mention of %q", err, tc.want)
			}
		})
	}
}

func TestCheckRejectsABadAgentCard(t *testing.T) {
	d := testDeps(t, fixture(t, "config.json"), map[string]string{"OPENAI_API_KEY": "x", "KAGENT_PIG_BIN": fakePigBinary(t)})
	d.bootstrap = func(context.Context, func(string) string) (*bootstrapBundle, error) {
		return &runtimev1.BootstrapBundle{ConfigJson: fixture(t, "config.json"), AgentCardJson: []byte(`{"name":"  "}`)}, nil
	}
	if err := run(t.Context(), true, d); err == nil || !strings.Contains(err.Error(), "agent card name") {
		t.Fatalf("err = %v", err)
	}
}

func captureLog(t *testing.T) (context.Context, *bytes.Buffer) {
	t.Helper()
	var buffer bytes.Buffer
	logger, err := logging.New(&buffer, "debug")
	if err != nil {
		t.Fatal(err)
	}
	return logging.IntoContext(t.Context(), logger), &buffer
}

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestPigHomeOrder(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker") // a regular file: nothing can be created below it
	os.WriteFile(blocker, nil, 0o600)
	temp := filepath.Join(root, "tmp")
	os.Mkdir(temp, 0o755)
	fallback := filepath.Join(temp, fmt.Sprintf("kagent-pig-%d", os.Getuid()))

	t.Run("explicit path wins and is private", func(t *testing.T) {
		ctx, logs := captureLog(t)
		explicit := filepath.Join(root, "explicit")
		got, err := preparePigHome(ctx, pigHomeChoice{explicit: explicit, intended: filepath.Join(root, "run"), tempDir: temp}, false)
		if err != nil || got != explicit || modeOf(t, got) != 0o700 {
			t.Fatalf("got %q, %v", got, err)
		}
		if _, err := os.Stat(filepath.Join(root, "run")); !os.IsNotExist(err) {
			t.Fatal("the intended path was touched although KAGENT_PIG_HOME was set")
		}
		if logs.Len() != 0 {
			t.Fatalf("unexpected log output: %s", logs)
		}
	})
	t.Run("an unusable explicit path fails startup instead of falling back", func(t *testing.T) {
		ctx, _ := captureLog(t)
		_, err := preparePigHome(ctx, pigHomeChoice{explicit: filepath.Join(blocker, "x"), intended: filepath.Join(root, "run2"), tempDir: temp}, false)
		if err == nil || !strings.Contains(err.Error(), "KAGENT_PIG_HOME") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("the intended path is used when writable, quietly", func(t *testing.T) {
		ctx, logs := captureLog(t)
		intended := filepath.Join(root, "run", "pig")
		got, err := preparePigHome(ctx, pigHomeChoice{intended: intended, tempDir: temp}, false)
		if err != nil || got != intended || modeOf(t, got) != 0o700 || logs.Len() != 0 {
			t.Fatalf("got %q, %v, logs %q", got, err, logs)
		}
	})
	t.Run("the temp directory is the quiet normal fallback: INFO once, no WARN", func(t *testing.T) {
		ctx, logs := captureLog(t)
		got, err := preparePigHome(ctx, pigHomeChoice{intended: filepath.Join(blocker, "pig"), tempDir: temp}, false)
		if err != nil || got != fallback || modeOf(t, got) != 0o700 {
			t.Fatalf("got %q, %v", got, err)
		}
		out := logs.String()
		if strings.Count(out, "INFO") != 1 || strings.Contains(out, "WARN") || !strings.Contains(out, "using a temporary directory") || !strings.Contains(out, "reason") {
			t.Fatalf("log = %s", out)
		}
		// the next start reuses it: contents are regenerated each turn, nothing to clean up
		again, err := preparePigHome(ctx, pigHomeChoice{intended: filepath.Join(blocker, "pig"), tempDir: temp}, false)
		if err != nil || again != got {
			t.Fatalf("second call = %q, %v", again, err)
		}
	})
	t.Run("a temp directory that cannot be used warns and fails startup", func(t *testing.T) {
		ctx, logs := captureLog(t)
		_, err := preparePigHome(ctx, pigHomeChoice{intended: filepath.Join(blocker, "pig"), tempDir: filepath.Join(blocker, "tmp")}, false)
		if err == nil || !strings.Contains(logs.String(), "WARN") || strings.Contains(logs.String(), "INFO") {
			t.Fatalf("err = %v, logs = %s", err, logs)
		}
	})
	t.Run("a symlinked fallback is refused", func(t *testing.T) {
		ctx, _ := captureLog(t)
		temp2 := filepath.Join(root, "tmp2")
		os.Mkdir(temp2, 0o755)
		os.Symlink(root, filepath.Join(temp2, fmt.Sprintf("kagent-pig-%d", os.Getuid())))
		if _, err := preparePigHome(ctx, pigHomeChoice{intended: filepath.Join(blocker, "pig"), tempDir: temp2}, false); err == nil {
			t.Fatal("a symlink in a shared temp directory was accepted")
		}
	})
	t.Run("check mode never touches the configured paths", func(t *testing.T) {
		ctx, _ := captureLog(t)
		intended := filepath.Join(root, "run-check")
		got, err := preparePigHome(ctx, pigHomeChoice{explicit: filepath.Join(root, "explicit-check"), intended: intended, tempDir: temp}, true)
		if err != nil || !strings.Contains(got, "kagent-pig-check-") {
			t.Fatalf("got %q, %v", got, err)
		}
		for _, p := range []string{intended, filepath.Join(root, "explicit-check")} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Fatalf("%s exists", p)
			}
		}
		os.RemoveAll(got)
	})
}

// plantedPig is a `pig` that records that it ran.
func plantedPig(t *testing.T, dir, marker string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "pig")
	script := "#!/bin/sh\necho ran >> " + marker + "\nif [ \"$1\" = version ]; then echo '0.7.0+1.5.0'; fi\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPigIsNeverResolvedThroughPATH(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "ran")
	data := filepath.Join(root, "data")
	local, gobin := filepath.Join(data, ".local", "bin"), filepath.Join(data, "go", "bin")
	planted := plantedPig(t, local, marker)
	plantedPig(t, gobin, marker)
	t.Setenv("PATH", local+string(os.PathListSeparator)+gobin+string(os.PathListSeparator)+os.Getenv("PATH"))

	env := func(bin string) map[string]string {
		e := map[string]string{"OPENAI_API_KEY": "x"}
		if bin != "" {
			e["KAGENT_PIG_BIN"] = bin
		}
		return e
	}
	check := func(bin string) error {
		d := testDeps(t, fixture(t, "config.json"), env(bin))
		d.dataDir = data
		return run(t.Context(), true, d)
	}
	ran := func() bool { _, err := os.Stat(marker); return err == nil }

	// No override: only the fixed absolute default is tried. Whether it exists here or not, the planted ones never run.
	if err := check(""); err != nil && !strings.Contains(err.Error(), defaultPig) {
		t.Fatalf("default: %v", err)
	}
	if ran() {
		t.Fatal("a pig planted in the data directory ran for the default path")
	}
	// A bare name is not looked up on PATH.
	if err := check("pig"); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("bare name: err = %v, want a refusal that names the absolute-path rule", err)
	}
	// An override under the data directory is refused, as given and through a symlink.
	if err := check(planted); err == nil || !strings.Contains(err.Error(), "durable data directory") {
		t.Fatalf("override under data: err = %v", err)
	}
	link := filepath.Join(t.TempDir(), "pig")
	if err := os.Symlink(planted, link); err != nil {
		t.Fatal(err)
	}
	if err := check(link); err == nil || !strings.Contains(err.Error(), "durable data directory") {
		t.Fatalf("symlink into data: err = %v", err)
	}
	if ran() {
		t.Fatal("a pig under the data directory ran")
	}
	// A good absolute override outside the data directory still works with the planted ones first on PATH.
	if err := check(fakePigBinary(t)); err != nil {
		t.Fatalf("good override: %v", err)
	}
	if ran() {
		t.Fatal("a planted pig ran next to a good override")
	}
}

func TestResolvePigRejectsWhatIsNotAnExecutableFile(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"directory": dir, "not executable": plain, "missing": filepath.Join(dir, "none")} {
		if _, err := resolvePig(path, filepath.Join(t.TempDir(), "data")); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Startup must not wait for, or depend on, MCP discovery: kagent's Go ADK lists MCP tools when an invocation is
// prepared, and this harness does the same on first use. A bound server that is down, slow or hung when the Actor
// starts must neither fail startup nor receive a request from it.
func TestStartupNeverContactsABoundMCPServer(t *testing.T) {
	var requests atomic.Int32
	hung := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case <-hung:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(hung); server.Close() })
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	config := fmt.Sprintf(`{"runtime":{"control_plane_url":"http://kagent-controller.kagent:8083"},
	 "agent":{"name":"pig_demo","instruction":"x","model":{"model":"mock-model","openai":{"base_url":"http://kagent-mock.kagent:8080/v1"}},
	 "http_tools":[{"params":{"url":%q}},{"params":{"url":%q,"timeout":"600s"},"require_approval":true},{"params":{"url":%q},"tools":["kagent_probe"]}]}}`,
		server.URL+"/mcp", server.URL+"/other", closedURL+"/mcp")
	env := map[string]string{"OPENAI_API_KEY": "kagent-credential-injected", "KAGENT_PIG_BIN": fakePigBinary(t)}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := run(ctx, true, testDeps(t, []byte(config), env)); err != nil {
		t.Fatalf("run -check with unreachable and hung MCP servers: %v", err)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("startup sent %d requests to a bound MCP server", n)
	}
}
