package langsmith

// sdkdiff_test.go replays the op scripts of port/sdkdiff/ops through the Go port (runtree.go, client*.go) against a
// mockls loopback endpoint and compares the recorded wire requests with the golden logs the real JavaScript SDK
// (langsmith 0.10.5, driven by port/tools/sdkdrive.mjs) produced for the same scripts. The comparison is lsdiff's:
// identical after its documented normalisations N1-N6 (ids, times, boundary, divergence D1, host key order, paths).
//
// Regenerate the goldens (and the op scripts) on xd6702:
//
//	ls run "cd components/langsmith/port/tools && node gen/sdkdiff-ops.mjs && ./sdkdiff-regen.sh"
//
// The test needs `go` (it builds mockls and lsdiff from port/tools, a separate module) and skips without it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	sdkdiffDir     = "../../port/sdkdiff"
	sdkdiffToolDir = "../../port/tools"
)

// buildSdkdiffTools builds mockls and lsdiff from the separate tools module into a temp directory.
func buildSdkdiffTools(t *testing.T) (mockls, lsdiff string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH: cannot build mockls and lsdiff")
	}
	dir := t.TempDir()
	for _, name := range []string{"mockls", "lsdiff"} {
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, name), "./cmd/"+name)
		cmd.Dir = sdkdiffToolDir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build ./cmd/%s: %v\n%s", name, err, out)
		}
	}
	return filepath.Join(dir, "mockls"), filepath.Join(dir, "lsdiff")
}

// sdkdrive's result file (the JavaScript run's console output, op errors and files).
type sdkResult struct {
	Endpoint   string                         `json:"endpoint"`
	Console    []struct{ Level, Text string } `json:"console"`
	Errors     []sdkOpError                   `json:"errors"`
	FilesAfter map[string]*string             `json:"filesAfter"`
}

type sdkOpError struct {
	Op    int    `json:"op"`
	Name  string `json:"name"`
	Error string `json:"error"`
}

func TestSDKDiffGolden(t *testing.T) {
	opsFiles, err := filepath.Glob(filepath.Join(sdkdiffDir, "ops", "*.json"))
	if err != nil || len(opsFiles) == 0 {
		t.Fatalf("no op scripts in %s/ops (%v)", sdkdiffDir, err)
	}
	sort.Strings(opsFiles)
	mockls, lsdiff := buildSdkdiffTools(t)
	for _, opsFile := range opsFiles {
		name := strings.TrimSuffix(filepath.Base(opsFile), ".json")
		t.Run(name, func(t *testing.T) { runSDKDiffScript(t, name, opsFile, mockls, lsdiff) })
	}
}

func runSDKDiffScript(t *testing.T, name, opsFile, mockls, lsdiff string) {
	// Absolute: the replay changes the working directory (an empty HOME) before lsdiff runs.
	golden, err := filepath.Abs(filepath.Join(sdkdiffDir, "golden", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	resultFile := filepath.Join(filepath.Dir(golden), name+".result.json")
	raw, err := os.ReadFile(opsFile)
	if err != nil {
		t.Fatal(err)
	}
	var want sdkResult
	rb, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatalf("golden result missing (run port/tools/sdkdiff-regen.sh): %v", err)
	}
	if err := json.Unmarshal(rb, &want); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(golden); err != nil {
		t.Fatalf("golden request log missing: %v", err)
	}

	// mockls with the script's config.
	var head struct {
		Mockls         json.RawMessage `json:"mockls"`
		UnorderedDrain bool            `json:"unorderedDrain"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	cfgPath := filepath.Join(work, "mockls.json")
	cfg := head.Mockls
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	if err := os.WriteFile(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	logPath, portPath := filepath.Join(work, "got.jsonl"), filepath.Join(work, "port")
	srv := exec.Command(mockls, "-addr", "127.0.0.1:0", "-config", cfgPath, "-log", logPath, "-port-file", portPath)
	var srvErr bytes.Buffer
	srv.Stderr = &srvErr
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = srv.Process.Signal(os.Interrupt)
		_ = srv.Wait()
	}
	t.Cleanup(stop)
	var port string
	for i := 0; i < 200 && port == ""; i++ {
		if b, err := os.ReadFile(portPath); err == nil && strings.HasSuffix(string(b), "\n") {
			port = strings.TrimSpace(string(b))
		} else {
			time.Sleep(25 * time.Millisecond)
		}
	}
	if port == "" {
		t.Fatalf("mockls did not start: %s", srvErr.String())
	}
	endpoint := "http://127.0.0.1:" + port

	// The script, with {{endpoint}} expanded, parsed keeping key order.
	parsed, err := rtParseJSON(strings.ReplaceAll(string(raw), "{{endpoint}}", endpoint))
	if err != nil {
		t.Fatal(err)
	}
	script := parsed.(*omap)

	home := t.TempDir()
	restore := sdkdiffEnv(t, script, home)
	defer restore()

	var stderr bytes.Buffer
	old := swapStderr(&stderr)
	warnedMu.Lock()
	warnedMessages = map[string]struct{}{}
	warnedMu.Unlock()
	opErrs := replaySDKOps(t, script)
	swapStderr(old)

	stop()
	if srvErr.Len() > 0 && strings.Contains(srvErr.String(), "log:") {
		t.Fatalf("mockls: %s", srvErr.String())
	}

	// Wire.
	lsArgs := []string{"-max", "5", "-path-prefix", endpoint}
	if head.UnorderedDrain {
		lsArgs = append(lsArgs, "-unordered-drain")
	}
	if want.Endpoint != "" {
		lsArgs = append(lsArgs, "-path-prefix", want.Endpoint)
	}
	out, err := exec.Command(lsdiff, append(lsArgs, golden, logPath)...).CombinedOutput()
	if err != nil {
		t.Errorf("wire differs from the JavaScript SDK's (A = golden, B = Go):\n%s", out)
	}

	// Console output of the SDK (warnings and errors it prints).
	var wantConsole []string
	for _, c := range want.Console {
		wantConsole = append(wantConsole, c.Text)
	}
	wantText := strings.Join(wantConsole, "\n")
	if wantText != "" {
		wantText += "\n"
	}
	gotText := strings.ReplaceAll(stderr.String(), endpoint, want.Endpoint)
	if gotText != wantText {
		t.Errorf("stderr differs from the JavaScript console output:\n--- JS\n%s--- Go\n%s", wantText, gotText)
	}

	// Ops that threw.
	if fmt.Sprint(opErrs) != fmt.Sprint(want.Errors) {
		t.Errorf("op errors differ:\n JS: %v\n Go: %v", want.Errors, opErrs)
	}

	// Files the SDK rewrote (OAuth refresh): bytes equal but the expiry, which is now + expires_in.
	reExpires := regexp.MustCompile(`"expires_at": "[^"]*"`)
	var rel []string
	for k := range want.FilesAfter {
		rel = append(rel, k)
	}
	sort.Strings(rel)
	for _, k := range rel {
		gotB, gerr := os.ReadFile(filepath.Join(home, k))
		var got *string
		if gerr == nil {
			s := string(gotB)
			got = &s
		}
		w := want.FilesAfter[k]
		if (got == nil) != (w == nil) {
			t.Errorf("file %s: JS present=%v Go present=%v", k, w != nil, got != nil)
			continue
		}
		if got != nil {
			g := reExpires.ReplaceAllString(strings.ReplaceAll(*got, endpoint, "<endpoint>"), `"expires_at": "<t>"`)
			x := reExpires.ReplaceAllString(strings.ReplaceAll(*w, want.Endpoint, "<endpoint>"), `"expires_at": "<t>"`)
			if g != x {
				t.Errorf("file %s differs:\n--- JS\n%s--- Go\n%s", k, x, g)
			}
		}
	}
}

// sdkdiffEnv gives the replay the environment sdkdrive.mjs gives the JavaScript SDK: no LANGSMITH_*, LANGCHAIN_*,
// OTEL_* variables, HOME an empty directory that is also the working directory, the script's files written there
// and its env applied. The returned function undoes it.
func sdkdiffEnv(t *testing.T, script *omap, home string) func() {
	t.Helper()
	saved := map[string]*string{}
	save := func(k string) {
		if _, done := saved[k]; done {
			return
		}
		if v, ok := os.LookupEnv(k); ok {
			saved[k] = &v
		} else {
			saved[k] = nil
		}
	}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "LANGSMITH_") || strings.HasPrefix(k, "LANGCHAIN_") || strings.HasPrefix(k, "OTEL_") || k == "HOME" || k == "USERPROFILE" {
			save(k)
			os.Unsetenv(k)
		}
	}
	save("HOME")
	os.Setenv("HOME", home)
	if files, ok := script.val("files").(*omap); ok {
		for _, rel := range files.ownKeys() {
			p := filepath.Join(home, rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			content, isStr := files.val(rel).(string)
			if !isStr {
				b, _, err := stringify(files.val(rel))
				if err != nil {
					t.Fatal(err)
				}
				// sdkdrive writes JSON.stringify(content, null, 2).
				var buf bytes.Buffer
				if err := json.Indent(&buf, b, "", "  "); err != nil {
					t.Fatal(err)
				}
				content = buf.String()
			}
			if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if env, ok := script.val("env").(*omap); ok {
		for _, k := range env.ownKeys() {
			save(k)
			os.Setenv(k, fmt.Sprint(env.val(k)))
		}
	}
	t.Chdir(home)
	return func() {
		for k, v := range saved {
			if v == nil {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, *v)
			}
		}
	}
}

// ---- op replay ----

func sdkStr(m *omap, key string) (string, bool) {
	v, ok := m.get(key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func sdkStrPtr(t *testing.T, m *omap, key string) *string {
	t.Helper()
	v, ok := m.get(key)
	if !ok {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("%s must be a string, got %T", key, v)
	}
	return &s
}

func sdkReplicas(t *testing.T, v any) []replicaConfig {
	t.Helper()
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("replicas must be an array, got %T", v)
	}
	out := make([]replicaConfig, 0, len(list))
	for _, e := range list {
		m, ok := e.(*omap)
		if !ok {
			t.Fatalf("replica must be an object, got %T", e)
		}
		var r replicaConfig
		for _, k := range m.ownKeys() {
			switch k {
			case "apiUrl":
				r.APIURL = sdkStrPtr(t, m, k)
			case "apiKey":
				r.APIKey = sdkStrPtr(t, m, k)
			case "projectName":
				r.ProjectName = sdkStrPtr(t, m, k)
			case "workspaceId":
				r.WorkspaceID = sdkStrPtr(t, m, k)
			case "updates":
				u, ok := m.val(k).(*omap)
				if !ok {
					t.Fatalf("replica updates must be an object")
				}
				r.Updates = u
			case "primary":
				b, _ := m.val(k).(bool)
				r.Primary = &b
			case "reroot":
				b, _ := m.val(k).(bool)
				r.Reroot = &b
			default:
				t.Fatalf("sdkdiff: replica option %q is not supported by the replay", k)
			}
		}
		out = append(out, r)
	}
	return out
}

// sdkRunConfig converts an op's RunTree / createChild config.
func sdkRunConfig(t *testing.T, cfg *omap, cl runClient) runTreeConfig {
	t.Helper()
	rc := runTreeConfig{Client: cl, DefaultProject: true}
	for _, k := range cfg.ownKeys() {
		v := cfg.val(k)
		switch k {
		case "id":
			rc.ID, _ = v.(string)
		case "name":
			rc.Name, _ = v.(string)
		case "run_type":
			rc.RunType, _ = v.(string)
		case "start_time":
			f, ok := v.(float64)
			if !ok {
				t.Fatalf("start_time must be a number")
			}
			ms := int64(f)
			rc.StartTimeMs = &ms
		case "project_name":
			rc.ProjectName, _ = v.(string)
			rc.DefaultProject = false
		case "inputs":
			if v == nil {
				rc.Inputs = inputsNull
			} else {
				rc.Inputs = v
			}
		case "metadata":
			m, ok := v.(*omap)
			if !ok {
				t.Fatalf("metadata must be an object")
			}
			rc.Metadata = m
		case "extra":
			m, ok := v.(*omap)
			if !ok {
				t.Fatalf("extra must be an object")
			}
			rc.Extra = m
		case "tags":
			list, _ := v.([]any)
			for _, e := range list {
				s, _ := e.(string)
				rc.Tags = append(rc.Tags, s)
			}
		case "replicas":
			rc.Replicas = sdkReplicas(t, v)
		default:
			t.Fatalf("sdkdiff: run option %q is not supported by the replay (add it to sdkdiff_test.go)", k)
		}
	}
	return rc
}

// sdkMissing aborts one op whose client or run does not exist.
type sdkMissing struct{}

func replaySDKOps(t *testing.T, script *omap) []sdkOpError {
	t.Helper()
	ctx := context.Background()
	clients := map[string]*client{}
	runs := map[string]*runTree{}
	var errs []sdkOpError
	ops, _ := script.val("ops").([]any)
	for i, o := range ops {
		op := o.(*omap)
		kind, _ := sdkStr(op, "op")
		fail := func(err error) {
			errs = append(errs, sdkOpError{Op: i, Name: kind, Error: errString(err)})
		}
		// sdkdrive.mjs throws `op refers to unknown client "x"` for a name nothing defined (a constructor that threw
		// earlier leaves it undefined) and records it as the op's error; the replay does the same.
		getClient := func(key string) *client {
			name, _ := sdkStr(op, key)
			c := clients[name]
			if c == nil {
				fail(fmt.Errorf("op refers to unknown client %q", name))
				panic(sdkMissing{})
			}
			return c
		}
		getRun := func(key string) *runTree {
			name, _ := sdkStr(op, key)
			r := runs[name]
			if r == nil {
				fail(fmt.Errorf("op refers to unknown run %q", name))
				panic(sdkMissing{})
			}
			return r
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					if _, ok := r.(sdkMissing); !ok {
						panic(r)
					}
				}
			}()
			switch kind {
			case "client":
				opts := clientOptions{}
				if cfg, ok := op.val("config").(*omap); ok {
					for _, k := range cfg.ownKeys() {
						switch k {
						case "apiUrl":
							opts.APIURL = sdkStrPtr(t, cfg, k)
						case "apiKey":
							opts.APIKey = sdkStrPtr(t, cfg, k)
						case "autoBatchTracing":
							b, _ := cfg.val(k).(bool)
							opts.AutoBatchTracing = &b
						default:
							t.Fatalf("sdkdiff: client option %q is not supported by the replay", k)
						}
					}
				}
				c, err := newClient(opts)
				if err != nil {
					fail(err)
					break
				}
				name, _ := sdkStr(op, "as")
				clients[name] = c
			case "runTree":
				var cl runClient
				if _, ok := sdkStr(op, "client"); ok {
					cl = getClient("client")
				}
				cfg, _ := op.val("config").(*omap)
				r, err := newRunTree(sdkRunConfig(t, cfg, cl))
				if err != nil {
					fail(err)
					break
				}
				name, _ := sdkStr(op, "as")
				runs[name] = r
			case "createChild":
				cfg, _ := op.val("config").(*omap)
				r, err := getRun("parent").createChild(sdkRunConfig(t, cfg, nil))
				if err != nil {
					fail(err)
					break
				}
				name, _ := sdkStr(op, "as")
				runs[name] = r
			case "end":
				var outputs any = undef
				if v, ok := op.get("outputs"); ok {
					outputs = v
				}
				var endMs *int64
				if f, ok := op.val("endTime").(float64); ok {
					ms := int64(f)
					endMs = &ms
				}
				md, _ := op.val("metadata").(*omap)
				getRun("run").endAt(outputs, sdkStrPtr(t, op, "error"), endMs, md)
			case "addEvent":
				ev, _ := op.val("event").(*omap)
				name, _ := sdkStr(ev, "name")
				kw, _ := ev.val("kwargs").(*omap)
				getRun("run").addEvent(name, kw)
			case "postRun":
				exclude := true
				if b, ok := op.val("excludeChildRuns").(bool); ok {
					exclude = b
				}
				getRun("run").postRunWith(ctx, exclude)
			case "patchRun":
				if err := getRun("run").patchRun(ctx); err != nil {
					fail(err)
				}
			case "createRun":
				run, _ := op.val("run").(*omap)
				if err := getClient("client").createRun(ctx, run, nil); err != nil {
					fail(err)
				}
			case "updateRun":
				run, _ := op.val("run").(*omap)
				id, _ := sdkStr(op, "id")
				if err := getClient("client").updateRun(ctx, id, run, nil); err != nil {
					fail(err)
				}
			case "awaitPendingTraceBatches":
				if err := getClient("client").awaitPendingTraceBatches(ctx); err != nil {
					fail(err)
				}
			case "sleep":
				f, _ := op.val("ms").(float64)
				time.Sleep(time.Duration(f) * time.Millisecond)
			default:
				t.Fatalf("op %d: unknown op %q", i, kind)
			}
		}()
	}
	// What a process exit flushes: the same as sdkdrive.mjs does after the last op.
	names := make([]string, 0, len(clients))
	for n := range clients {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := clients[n].awaitPendingTraceBatches(ctx); err != nil {
			t.Errorf("final await: %v", err)
		}
		clients[n].close()
	}
	return errs
}
