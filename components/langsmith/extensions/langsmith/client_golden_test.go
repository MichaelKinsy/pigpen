package langsmith

// Differential test: testdata/client_golden.json is what the real langsmith 0.10.5 Client sent to a loopback server
// for the scenarios in testdata/client_scenarios.json (generator: port/tools/gen/client-differential.mjs). The Go
// client replays each scenario against its own loopback server; the requests, the console output and the files in the
// failed-trace directory must be the same, modulo the canonicalisations the generator documents (User-Agent,
// runtime/sdk/sdk_version values, multipart boundary, temp paths and timestamps in file names).

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var shaVars = []string{"VERCEL_GIT_COMMIT_SHA", "NEXT_PUBLIC_VERCEL_GIT_COMMIT_SHA", "COMMIT_REF", "RENDER_GIT_COMMIT", "CI_COMMIT_SHA", "CIRCLE_SHA1", "CF_PAGES_COMMIT_SHA", "REACT_APP_GIT_SHA", "SOURCE_VERSION", "GITHUB_SHA", "TRAVIS_COMMIT", "GIT_COMMIT", "BUILD_VCS_NUMBER", "bamboo_planRepository_revision", "Build.SourceVersion", "BITBUCKET_COMMIT", "DRONE_COMMIT_SHA", "SEMAPHORE_GIT_SHA", "BUILDKITE_COMMIT"}

// isolateEnv removes every variable the client reads (restored at the end of the test) and points HOME and the
// profile config at an empty temp directory.
func isolateEnv(t testing.TB) string {
	t.Helper()
	tmp := t.TempDir()
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "LANGSMITH_") || strings.HasPrefix(k, "LANGCHAIN_") || strings.HasPrefix(k, "OTEL_") || slicesContains(shaVars, k) {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("LANGSMITH_CONFIG_FILE", filepath.Join(tmp, "none.json"))
	return tmp
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// ---- recording mock server ----

type mockRec struct {
	Route, Method, Path string
	Header              http.Header
	TE                  []string
	Body                []byte
}

type mockStep struct {
	Status int
	JSON   string // when non-empty, the body and Content-Type application/json
	Body   string
	Delay  time.Duration
}

type mockServer struct {
	*httptest.Server
	mu       sync.Mutex
	reqs     []mockRec
	counters map[string]int
	script   map[string][]mockStep
	inflight int
	peak     int
	// infoDelay is the answer delay of /info (see newMock).
	infoDelay time.Duration
}

func routeOf(r *http.Request) string {
	switch {
	case r.URL.Path == "/info":
		return "info"
	case r.URL.Path == "/runs/multipart":
		return "multipart"
	case r.URL.Path == "/runs/batch":
		return "batch"
	case r.URL.Path == "/runs" && r.Method == "POST":
		return "runs"
	case strings.HasPrefix(r.URL.Path, "/runs/") && r.Method == "PATCH":
		return "patch"
	}
	return "other"
}

const mockDefaultInfo = `{"version":"mockls","batch_ingest_config":{"use_multipart_endpoint":true,"size_limit":100,"size_limit_bytes":20971520},"instance_flags":{"gzip_body_enabled":false}}`

func newMock(t testing.TB, script map[string][]mockStep) *mockServer {
	m := &mockServer{counters: map[string]int{}, script: script, infoDelay: 40 * time.Millisecond}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		route := routeOf(r)
		m.mu.Lock()
		m.counters[route]++
		n := m.counters[route]
		list := m.script[route]
		if len(list) == 0 {
			list = []mockStep{{Status: 200, JSON: "{}"}}
			if route == "info" {
				list = []mockStep{{Status: 200, JSON: mockDefaultInfo}}
			}
		}
		step := list[min(n-1, len(list)-1)]
		m.inflight++
		m.peak = max(m.peak, m.inflight)
		defer func() {
			m.mu.Lock()
			m.inflight--
			m.mu.Unlock()
		}()
		m.reqs = append(m.reqs, mockRec{Route: route, Method: r.Method, Path: r.URL.RequestURI(), Header: r.Header.Clone(), TE: r.TransferEncoding, Body: body})
		m.mu.Unlock()
		if step.Delay > 0 {
			time.Sleep(step.Delay)
		}
		if route == "info" {
			// The original queues a burst of createRun calls in one event-loop turn, before any response can
			// arrive; a short answer delay gives the replay's burst the same ordering.
			m.mu.Lock()
			d := m.infoDelay
			m.mu.Unlock()
			time.Sleep(d)
		}
		payload := step.Body
		ct := "text/plain"
		if step.JSON != "" {
			payload, ct = step.JSON, "application/json"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(step.Status)
		io.WriteString(w, payload)
	}))
	t.Cleanup(m.Close)
	return m
}

func (m *mockServer) requests() []mockRec {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mockRec(nil), m.reqs...)
}

func (m *mockServer) count(route string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counters[route]
}

// ---- canonicalisation (mirrors canonRequest of the generator) ----

var (
	boundaryRe = regexp.MustCompile(`boundary=----LangSmithFormBoundary[0-9a-z]+`)
	traceFile  = regexp.MustCompile(`trace_\d+_[0-9a-f]{8}\.json`)
)

var canonBase, canonTmp = "\x00", "\x00"

func canonText(s string) string {
	return strings.NewReplacer(canonBase, "{base}", canonTmp, "{tmp}",
		`"sdk":"langsmith-js"`, `"sdk":"<sdk>"`, `"sdk":"pigpen-langsmith-go"`, `"sdk":"<sdk>"`,
		`"sdk_version":"0.10.5"`, `"sdk_version":"<sdkv>"`, `"sdk_version":"`+portVersion+`"`, `"sdk_version":"<sdkv>"`,
		`"runtime":"node"`, `"runtime":"<rt>"`, `"runtime":"go"`, `"runtime":"<rt>"`).Replace(s)
}

func canonCT(s string) string { return boundaryRe.ReplaceAllString(s, "boundary=<boundary>") }

func splitMultipart(body []byte, boundary string) ([]map[string]any, error) {
	delim := []byte("--" + boundary)
	rest := body[len(delim):]
	var parts []map[string]any
	for {
		if bytes.HasPrefix(rest, []byte("--")) {
			return parts, nil
		}
		if !bytes.HasPrefix(rest, []byte("\r\n")) {
			return parts, io.ErrUnexpectedEOF
		}
		rest = rest[2:]
		he := bytes.Index(rest, []byte("\r\n\r\n"))
		header := string(rest[:he])
		rest = rest[he+4:]
		end := bytes.Index(rest, append([]byte("\r\n"), delim...))
		if end < 0 {
			return parts, io.ErrUnexpectedEOF
		}
		content := rest[:end]
		rest = rest[end+2+len(delim):]
		lines := strings.Split(header, "\r\n")
		hl := make([]any, len(lines))
		for i, l := range lines {
			hl[i] = l
		}
		p := map[string]any{"header": hl}
		if last := lines[len(lines)-1]; strings.HasPrefix(last, "Content-Type: application/json") {
			// The declared length must be the part's byte length; the canonical text's length replaces it.
			if want := last[strings.LastIndex(last, "length=")+len("length="):]; want != strconv.Itoa(len(content)) {
				return parts, fmt.Errorf("part %q declares length %s, has %d bytes", lines[0], want, len(content))
			}
			text := canonText(string(content))
			hl[len(hl)-1] = last[:strings.LastIndex(last, "length=")] + "length=" + strconv.Itoa(len(text))
			p["text"] = text
		} else {
			p["b64"] = base64.StdEncoding.EncodeToString(content)
		}
		parts = append(parts, p)
	}
}

func canonRequest(t testing.TB, r mockRec) map[string]any {
	t.Helper()
	h := map[string]any{}
	if r.Header.Get("User-Agent") != "" {
		h["user-agent"] = "<ua>"
	}
	for _, n := range []string{"x-api-key", "x-tenant-id", "authorization", "content-encoding"} {
		if v := r.Header.Values(n); len(v) > 0 {
			h[n] = v[0]
		}
	}
	if v := r.Header.Get("Content-Type"); v != "" {
		h["content-type"] = canonCT(v)
	}
	if r.Header.Get("Accept") == "application/json" {
		h["accept"] = "application/json"
	}
	framing := "length"
	if len(r.TE) > 0 && r.TE[0] == "chunked" {
		framing = "chunked"
	}
	if len(r.Body) == 0 && r.Method == "GET" {
		framing = "none"
	}
	out := map[string]any{"route": r.Route, "method": r.Method, "path": r.Path, "headers": h, "framing": framing}
	raw := r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("gzip body: %v", err)
		}
		if raw, err = io.ReadAll(zr); err != nil {
			t.Fatalf("gzip body: %v", err)
		}
	}
	if i := strings.Index(r.Header.Get("Content-Type"), "boundary="); i >= 0 {
		parts, err := splitMultipart(raw, r.Header.Get("Content-Type")[i+len("boundary="):])
		if err != nil {
			t.Fatalf("multipart body: %v\n%s", err, raw)
		}
		out["parts"] = parts
	} else if len(raw) > 0 {
		out["body"] = canonText(string(raw))
	}
	return out
}

func jsonRoundTrip(t testing.TB, v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// ---- scenario replay ----

func subValue(v any, base, tmp string) any {
	switch x := v.(type) {
	case string:
		return strings.NewReplacer("{base}", base, "{tmp}", tmp).Replace(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = subValue(e, base, tmp)
		}
		return out
	case *omap:
		out := newOmapCap(x.size())
		for _, k := range x.ownKeys() {
			out.set(k, subValue(x.vals[k], base, tmp))
		}
		return out
	}
	return v
}

// revive turns the generator's markers into undefined and bytes.
func revive(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = revive(e)
		}
		return out
	case *omap:
		if x.has("$undef") {
			return undef
		}
		if x.has("$bytes") {
			b, _ := base64.StdEncoding.DecodeString(x.val("$bytes").(string))
			return b
		}
		out := newOmapCap(x.size())
		for _, k := range x.ownKeys() {
			out.set(k, revive(x.vals[k]))
		}
		return out
	}
	return v
}

func ostr(m *omap, k string) *string {
	if s, ok := m.val(k).(string); ok {
		return &s
	}
	return nil
}

func destOf(v any) *destOptions {
	m, ok := v.(*omap)
	if !ok {
		return nil
	}
	return &destOptions{APIKey: ostr(m, "apiKey"), APIURL: ostr(m, "apiUrl"), WorkspaceID: ostr(m, "workspaceId")}
}

func optionsOf(m *omap) clientOptions {
	o := clientOptions{APIKey: ostr(m, "apiKey"), APIURL: ostr(m, "apiUrl"), WorkspaceID: ostr(m, "workspaceId")}
	if b, ok := m.val("autoBatchTracing").(bool); ok {
		o.AutoBatchTracing = &b
	}
	if b, ok := m.val("blockOnRootRunFinalization").(bool); ok {
		o.BlockOnRootRunFinalization = &b
	}
	if f, ok := m.val("tracingSamplingRate").(float64); ok {
		o.TracingSamplingRate = &f
	}
	if f, ok := m.val("batchSizeLimit").(float64); ok {
		n := int(f)
		o.BatchSizeLimit = &n
	}
	if f, ok := m.val("maxIngestMemoryBytes").(float64); ok {
		n := int64(f)
		o.MaxIngestMemoryBytes = &n
	}
	if f, ok := m.val("traceBatchConcurrency").(float64); ok {
		n := int(f)
		o.TraceBatchConcurrency = &n
	}
	return o
}

func scriptOf(v any) map[string][]mockStep {
	out := map[string][]mockStep{}
	m, ok := v.(*omap)
	if !ok {
		return out
	}
	for _, route := range m.ownKeys() {
		for _, e := range m.val(route).([]any) {
			sm := e.(*omap)
			st := mockStep{Status: int(sm.val("status").(float64))}
			if j, ok := sm.get("json"); ok {
				b, _, _ := stringify(j)
				st.JSON = string(b)
			}
			if b, ok := sm.val("body").(string); ok {
				st.Body = b
			}
			if d, ok := sm.val("delayMs").(float64); ok {
				st.Delay = time.Duration(d) * time.Millisecond
			}
			out[route] = append(out[route], st)
		}
	}
	return out
}

type capture struct {
	mu   sync.Mutex
	logs []string
}

func (c *capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.logs = append(c.logs, strings.TrimSuffix(string(p), "\n"))
	c.mu.Unlock()
	return len(p), nil
}

// runtimeSizeDelta is how many bytes longer the runtime values of this port are than the SDK's (divergence D1:
// runtime go/node, sdk pigpen-langsmith-go/langsmith-js, sdk_version portVersion/0.10.5); every queued run is that
// much larger in the size estimate.
var runtimeSizeDelta = len("go") + len("pigpen-langsmith-go") + len(portVersion) - len("node") - len("langsmith-js") - len("0.10.5")

var queueSizeRe = regexp.MustCompile(`Current queue size: (\d+) bytes, attempted addition: (\d+) bytes`)

func canonLog(s, base, tmp string) string {
	s = strings.ReplaceAll(s, base, "{base}")
	s = strings.ReplaceAll(s, tmp, "{tmp}")
	s = queueSizeRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := queueSizeRe.FindStringSubmatch(m)
		cur, _ := strconv.Atoi(sub[1])
		add, _ := strconv.Atoi(sub[2])
		items := 1
		if add > 0 {
			items = max(1, cur/add)
		}
		return "Current queue size: " + strconv.Itoa(cur-items*runtimeSizeDelta) + " bytes, attempted addition: " + strconv.Itoa(add-runtimeSizeDelta) + " bytes"
	})
	return traceFile.ReplaceAllString(s, "trace_<ts>_<id>.json")
}

var jsonErrRe = regexp.MustCompile(`Unspecified status code .*`)

func canonJSONErr(s string) string {
	return jsonErrRe.ReplaceAllString(s, "Unspecified status code <json error>")
}

func loadScenarios(t testing.TB) []*omap {
	b, err := os.ReadFile("testdata/client_scenarios.json")
	if err != nil {
		t.Fatal(err)
	}
	v, ok := parseJSONOrdered(string(b))
	if !ok {
		t.Fatal("client_scenarios.json is not JSON")
	}
	var out []*omap
	for _, e := range v.([]any) {
		out = append(out, e.(*omap))
	}
	return out
}

func loadGolden(t testing.TB) map[string]any {
	b, err := os.ReadFile("testdata/client_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g map[string]any
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func noSleep(context.Context, time.Duration) error { return nil }

func runGoScenario(t *testing.T, sc *omap) map[string]any {
	tmp := isolateEnv(t)
	mock := newMock(t, scriptOf(sc.val("server")))
	if sc.val("noInfoDelay") == true { // scenarios whose timing is spaced out do not use the burst delay
		mock.mu.Lock()
		mock.infoDelay = 0
		mock.mu.Unlock()
	}
	base := mock.URL
	canonBase, canonTmp = base, tmp
	if env, ok := sc.val("env").(*omap); ok {
		for _, k := range env.ownKeys() {
			t.Setenv(k, subValue(env.val(k), base, tmp).(string))
		}
	}
	if files, ok := sc.val("files").([]any); ok {
		for _, f := range files {
			fm := f.(*omap)
			fp := subValue(fm.val("path"), base, tmp).(string)
			if err := os.MkdirAll(filepath.Dir(fp), 0o777); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fp, []byte(subValue(fm.val("text"), base, tmp).(string)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if p, ok := sc.val("prefill").(*omap); ok {
		d := subValue(p.val("dir"), base, tmp).(string)
		if err := os.MkdirAll(d, 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, p.val("file").(string)), bytes.Repeat([]byte("a"), int(p.val("bytes").(float64))), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	capt := &capture{}
	old := swapStderr(capt)
	defer swapStderr(old)

	out := map[string]any{"errors": []any{}, "ctorError": nil}
	opts := optionsOf(subValue(sc.val("options"), base, tmp).(*omap))
	if f, ok := sc.val("aggregationDelayMs").(float64); ok {
		d := time.Duration(f) * time.Millisecond
		opts.AutoBatchAggregationDelay = &d
	}
	c, err := newClient(opts)
	if err != nil {
		out["ctorError"] = err.Error()
	} else {
		defer c.close()
		if sc.val("fastRetry") == true {
			c.caller.sleep = noSleep
			c.batchCaller.sleep = noSleep
		}
		ctx := context.Background()
		var errs []any
		for _, o := range sc.val("ops").([]any) {
			op := o.(*omap)
			switch op.val("op") {
			case "wait":
				time.Sleep(time.Duration(op.val("ms").(float64)) * time.Millisecond)
			case "await":
				if err := c.awaitPendingTraceBatches(ctx); err != nil {
					t.Fatalf("awaitPendingTraceBatches: %v", err)
				}
			case "create":
				run := revive(subValue(op.val("run"), base, tmp)).(*omap)
				if err := c.createRun(ctx, run, destOf(subValue(op.val("dest"), base, tmp))); err != nil {
					errs = append(errs, errString(err))
				}
			case "update":
				run := revive(subValue(op.val("run"), base, tmp)).(*omap)
				if err := c.updateRun(ctx, op.val("id").(string), run, destOf(subValue(op.val("dest"), base, tmp))); err != nil {
					errs = append(errs, errString(err))
				}
			}
		}
		if err := c.awaitPendingTraceBatches(ctx); err != nil {
			t.Fatalf("final awaitPendingTraceBatches: %v", err)
		}
		if errs != nil {
			out["errors"] = errs
		}
	}
	reqs := []any{}
	for _, r := range mock.requests() {
		reqs = append(reqs, canonRequest(t, r))
	}
	out["requests"] = reqs
	logs := []any{}
	capt.mu.Lock()
	for _, l := range capt.logs {
		logs = append(logs, canonLog(l, base, tmp))
	}
	capt.mu.Unlock()
	out["logs"] = logs
	if fd, ok := sc.val("failedDir").(string); ok {
		d := subValue(fd, base, tmp).(string)
		entries, _ := os.ReadDir(d)
		files := []any{}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			info, _ := os.Stat(filepath.Join(d, name))
			if !traceFile.MatchString(name) || info.Size() > 100000 {
				files = append(files, map[string]any{"name": traceFile.ReplaceAllString(name, "trace_<ts>_<id>.json"), "size": info.Size()})
				continue
			}
			raw, _ := os.ReadFile(filepath.Join(d, name))
			var env map[string]any
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatalf("fallback file: %v", err)
			}
			body, _ := base64.StdEncoding.DecodeString(env["body_base64"].(string))
			ct := env["headers"].(map[string]any)["Content-Type"].(string)
			parts, err := splitMultipart(body, ct[strings.Index(ct, "boundary=")+len("boundary="):])
			if err != nil {
				t.Fatalf("fallback body: %v", err)
			}
			keys := []any{}
			// the key order of the envelope as written
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.Token()
			for dec.More() {
				k, _ := dec.Token()
				keys = append(keys, k)
				var skip json.RawMessage
				dec.Decode(&skip)
			}
			files = append(files, map[string]any{"name": "trace_<ts>_<id>.json", "keys": keys, "version": env["version"], "endpoint": env["endpoint"],
				"headers": map[string]any{"Content-Type": canonCT(ct)}, "parts": parts})
		}
		out["failedFiles"] = files
		out["failedDirExists"] = len(files) > 0
	}
	return out
}

func TestClientDifferential(t *testing.T) {
	golden := loadGolden(t)
	for _, sc := range loadScenarios(t) {
		name := sc.val("name").(string)
		t.Run(name, func(t *testing.T) {
			want := golden[name].(map[string]any)
			got := jsonRoundTrip(t, runGoScenario(t, sc)).(map[string]any)
			unordered := sc.val("unordered") == true
			norm := func(m map[string]any) map[string]any {
				cp := map[string]any{}
				for k, v := range m {
					cp[k] = v
				}
				if logs, ok := cp["logs"].([]any); ok {
					nl := make([]any, len(logs))
					for i, l := range logs {
						switch x := l.(type) {
						case []any: // generator: [kind, text]
							nl[i] = canonJSONErr(x[1].(string))
						case string:
							nl[i] = canonJSONErr(x)
						}
					}
					cp["logs"] = nl
				}
				if unordered {
					if rs, ok := cp["requests"].([]any); ok {
						sorted := append([]any(nil), rs...)
						sort.Slice(sorted, func(i, j int) bool {
							a, _ := json.Marshal(sorted[i])
							b, _ := json.Marshal(sorted[j])
							return string(a) < string(b)
						})
						cp["requests"] = sorted
					}
				}
				return cp
			}
			w, g := norm(want), norm(got)
			for _, key := range []string{"requests", "logs", "errors", "ctorError", "failedFiles", "failedDirExists"} {
				wb, _ := json.MarshalIndent(w[key], "", " ")
				gb, _ := json.MarshalIndent(g[key], "", " ")
				if !bytes.Equal(wb, gb) {
					t.Errorf("%s differs\n%s", key, firstDiff(string(wb), string(gb)))
				}
			}
		})
	}
}

// firstDiff shows the first differing line of two texts (JS golden, Go) with a few lines of context.
func firstDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	i := 0
	for i < len(wl) && i < len(gl) && wl[i] == gl[i] {
		i++
	}
	lo := max(0, i-4)
	show := func(lines []string) string {
		hi := min(len(lines), i+4)
		if lo >= hi {
			return "<end>"
		}
		return strings.Join(lines[lo:hi], "\n")
	}
	return "line " + strconv.Itoa(i+1) + "\n--- JS (golden)\n" + show(wl) + "\n--- Go\n" + show(gl)
}
