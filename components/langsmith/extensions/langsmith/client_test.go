package langsmith

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const testRunID = "0195b2a0-7e41-7c3d-9f00-"

func runIDN(n int) string { return testRunID + strconv.Itoa(100000000000+n) }

// testRun is a `_convertToCreate` payload.
func testRun(n int) *omap {
	id := runIDN(n)
	return obj("id", id, "name", "run-"+strconv.Itoa(n), "start_time", "2024-03-01T10:00:00.010000", "end_time", undef,
		"run_type", "chain", "extra", obj("metadata", obj("turn", float64(n))), "error", undef,
		"inputs", obj("q", "question"), "outputs", undef, "session_name", "proj", "child_runs", []any{},
		"parent_run_id", undef, "trace_id", id, "dotted_order", "20240301T100000010000Z"+id, "tags", []string{"t"})
}

type testEnv struct {
	mock *mockServer
	c    *client
	logs *capture
}

func newTestClient(t *testing.T, script map[string][]mockStep, mod func(*clientOptions)) *testEnv {
	t.Helper()
	isolateEnv(t)
	m := newMock(t, script)
	capt := &capture{}
	old := swapStderr(capt)
	t.Cleanup(func() { swapStderr(old) })
	o := clientOptions{APIKey: new("key-1"), APIURL: new(m.URL)}
	if mod != nil {
		mod(&o)
	}
	c, err := newClient(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.close)
	return &testEnv{m, c, capt}
}

func (e *testEnv) logText() string {
	e.logs.mu.Lock()
	defer e.logs.mu.Unlock()
	return strings.Join(e.logs.logs, "\n")
}

func mustCreate(t testing.TB, c *client, run *omap) {
	t.Helper()
	if err := c.createRun(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
}

func mustAwait(t testing.TB, c *client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.awaitPendingTraceBatches(ctx); err != nil {
		t.Fatal(err)
	}
}

// ---- constructor ----

func TestNewClientConfig(t *testing.T) {
	type check func(t *testing.T, c *client)
	cases := []struct {
		name string
		env  map[string]string
		opts clientOptions
		ok   check
		err  string
	}{
		{name: "defaults", ok: func(t *testing.T, c *client) {
			if c.apiURL != defaultAPIURL || c.apiKey != nil || c.workspaceID != nil || c.timeout != 90*time.Second ||
				c.traceBatchConcurrency != 5 || !c.autoBatchTracing || c.blockOnRootRunFinalization || c.hideInputs ||
				c.tracingSampleRate != nil || c.autoBatchDelay != 250*time.Millisecond || c.failedTracesMaxBytes != 100<<20 {
				t.Errorf("unexpected defaults: %+v", c)
			}
		}},
		{name: "endpoint trimmed, quoted, slash", env: map[string]string{"LANGSMITH_ENDPOINT": ` "https://x.test/api/" `}, ok: func(t *testing.T, c *client) {
			if c.apiURL != "https://x.test/api" {
				t.Error(c.apiURL)
			}
		}},
		{name: "langchain endpoint fallback", env: map[string]string{"LANGCHAIN_ENDPOINT": "https://lc.test"}, ok: func(t *testing.T, c *client) {
			if c.apiURL != "https://lc.test" {
				t.Error(c.apiURL)
			}
		}},
		{name: "langsmith wins, empty falls through", env: map[string]string{"LANGSMITH_ENDPOINT": "https://ls.test", "LANGCHAIN_ENDPOINT": "https://lc.test", "LANGSMITH_API_KEY": "", "LANGCHAIN_API_KEY": "lc"}, ok: func(t *testing.T, c *client) {
			if c.apiURL != "https://ls.test" || c.apiKey == nil || *c.apiKey != "lc" {
				t.Error(c.apiURL, c.apiKey)
			}
		}},
		{name: "blank key is no key", env: map[string]string{"LANGSMITH_API_KEY": "  \t"}, ok: func(t *testing.T, c *client) {
			if c.apiKey != nil {
				t.Error(*c.apiKey)
			}
		}},
		{name: "option beats env", env: map[string]string{"LANGSMITH_API_KEY": "env", "LANGSMITH_ENDPOINT": "https://env.test", "LANGSMITH_WORKSPACE_ID": "wenv"},
			opts: clientOptions{APIKey: new("'opt'"), APIURL: new("https://opt.test/"), WorkspaceID: new("wopt")}, ok: func(t *testing.T, c *client) {
				if *c.apiKey != "opt" || c.apiURL != "https://opt.test" || *c.workspaceID != "wopt" {
					t.Error(*c.apiKey, c.apiURL, *c.workspaceID)
				}
			}},
		{name: "hide flags", env: map[string]string{"LANGCHAIN_HIDE_INPUTS": "true", "LANGSMITH_HIDE_OUTPUTS": "true", "LANGSMITH_HIDE_METADATA": "TRUE"}, ok: func(t *testing.T, c *client) {
			if !c.hideInputs || !c.hideOutputs || c.hideMetadata {
				t.Error(c.hideInputs, c.hideOutputs, c.hideMetadata)
			}
		}},
		{name: "tracing background false blocks", env: map[string]string{"LANGSMITH_TRACING_BACKGROUND": "false"}, ok: func(t *testing.T, c *client) {
			if !c.blockOnRootRunFinalization {
				t.Error("not blocking")
			}
		}},
		{name: "tracing background true does not", env: map[string]string{"LANGSMITH_TRACING_BACKGROUND": "true"}, ok: func(t *testing.T, c *client) {
			if c.blockOnRootRunFinalization {
				t.Error("blocking")
			}
		}},
		{name: "failed traces mb", env: map[string]string{"LANGSMITH_FAILED_TRACES_DIR": "/x", "LANGSMITH_FAILED_TRACES_MAX_MB": " 2x"}, ok: func(t *testing.T, c *client) {
			if c.failedTracesDir != "/x" || c.failedTracesMaxBytes != 2<<20 {
				t.Error(c.failedTracesDir, c.failedTracesMaxBytes)
			}
		}},
		{name: "failed traces mb invalid", env: map[string]string{"LANGSMITH_FAILED_TRACES_MAX_MB": "-4"}, ok: func(t *testing.T, c *client) {
			if c.failedTracesMaxBytes != 100<<20 {
				t.Error(c.failedTracesMaxBytes)
			}
		}},
		{name: "streaming and compression flags", env: map[string]string{"LANGSMITH_DISABLE_MULTIPART_STREAMING": "true", "LANGCHAIN_DISABLE_RUN_COMPRESSION": "true"}, ok: func(t *testing.T, c *client) {
			if !c.multipartStreamingDisabled.Load() || !c.runCompressionDisabled {
				t.Error("flags not set")
			}
		}},
		{name: "sampling rate", env: map[string]string{"LANGSMITH_TRACING_SAMPLING_RATE": " 0.25abc"}, ok: func(t *testing.T, c *client) {
			if c.tracingSampleRate == nil || *c.tracingSampleRate != 0.25 {
				t.Error(c.tracingSampleRate)
			}
		}},
		{name: "sampling rate option wins", env: map[string]string{"LANGSMITH_TRACING_SAMPLING_RATE": "5"}, opts: clientOptions{TracingSamplingRate: new(0.5)}, ok: func(t *testing.T, c *client) {
			if *c.tracingSampleRate != 0.5 {
				t.Error(*c.tracingSampleRate)
			}
		}},
		{name: "sampling rate NaN passes", env: map[string]string{"LANGSMITH_TRACING_SAMPLING_RATE": "abc"}, ok: func(t *testing.T, c *client) {
			if !math_IsNaN(*c.tracingSampleRate) {
				t.Error(*c.tracingSampleRate)
			}
		}},
		{name: "sampling rate negative", env: map[string]string{"LANGSMITH_TRACING_SAMPLING_RATE": "-0.1"}, err: "LANGSMITH_TRACING_SAMPLING_RATE must be between 0 and 1 if set. Got: -0.1"},
		{name: "sampling rate above one", env: map[string]string{"LANGCHAIN_TRACING_SAMPLING_RATE": "2"}, err: "LANGSMITH_TRACING_SAMPLING_RATE must be between 0 and 1 if set. Got: 2"},
		{name: "sampling rate infinity", env: map[string]string{"LANGSMITH_TRACING_SAMPLING_RATE": "Infinity"}, err: "Got: Infinity"},
		{name: "concurrency", opts: clientOptions{TraceBatchConcurrency: new(0)}, err: "Trace batch concurrency must be positive."},
		{name: "otel mode refused", env: map[string]string{"LANGSMITH_TRACING_MODE": "otel"}, err: "OTEL tracing mode is not supported"},
		{name: "otel mode upper case refused", env: map[string]string{"LANGCHAIN_TRACING_MODE": "OTEL"}, err: "OTEL tracing mode is not supported"},
		{name: "otel enabled refused", env: map[string]string{"LANGSMITH_OTEL_ENABLED": "true"}, err: "LANGSMITH_OTEL_ENABLED"},
		{name: "legacy otel enabled refused", env: map[string]string{"OTEL_ENABLED": "true"}, err: "OTEL_ENABLED"},
		{name: "otel option refused", opts: clientOptions{TracingMode: new("otel")}, err: "OTEL tracing mode is not supported"},
		{name: "invalid mode", env: map[string]string{"LANGSMITH_TRACING_MODE": "x"}, err: `Invalid LANGSMITH_TRACING_MODE="x". Must be one of: langsmith, otel`},
		{name: "langsmith mode", env: map[string]string{"LANGSMITH_TRACING_MODE": "langsmith"}, ok: func(t *testing.T, c *client) {}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			c, err := newClient(tc.opts)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want containing %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer c.close()
			tc.ok(t, c)
		})
	}
}

func math_IsNaN(f float64) bool { return f != f }

func TestNewClientDebugPrintsAPIURLSource(t *testing.T) {
	isolateEnv(t)
	t.Setenv("LANGSMITH_DEBUG", "true")
	capt := &capture{}
	defer swapStderr(swapStderr(capt))
	t.Setenv("LANGCHAIN_ENDPOINT", "https://lc.test/")
	c, err := newClient(clientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c.close()
	c2, _ := newClient(clientOptions{APIURL: new("https://opt.test")})
	c2.close()
	want := []string{
		"LangSmith API URL https://lc.test resolved from LANGSMITH_ENDPOINT / LANGCHAIN_ENDPOINT environment variable",
		"LangSmith API URL https://opt.test resolved from apiUrl option",
	}
	if strings.Join(capt.logs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("logs: %q", capt.logs)
	}
}

// ---- direct paths ----

func TestCreateRunDirectDefaultsStartTimeAndSessionName(t *testing.T) {
	e := newTestClient(t, nil, func(o *clientOptions) { o.AutoBatchTracing = new(false) })
	before := nowMs()
	run := obj("id", runIDN(1), "name", "n", "project_name", "pp")
	if err := e.c.createRun(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	reqs := e.mock.requests()
	if len(reqs) != 1 || reqs[0].Route != "runs" {
		t.Fatalf("requests: %+v", reqs)
	}
	body := string(reqs[0].Body)
	if !strings.HasPrefix(body, `{"session_name":"pp","id":"`+runIDN(1)+`","name":"n","start_time":`) {
		t.Fatalf("body: %s", body)
	}
	i := strings.Index(body, `"start_time":`) + len(`"start_time":`)
	j := strings.IndexAny(body[i:], ",}")
	st, err := strconv.ParseInt(body[i:i+j], 10, 64)
	if err != nil || st < before || st > nowMs() {
		t.Fatalf("start_time %q not Date.now()", body[i:i+j])
	}
	if run.has("project_name") {
		t.Fatal("project_name must be deleted from the run, as the original does")
	}
	if reqs[0].Header.Get("User-Agent") != userAgent || reqs[0].Header.Get("X-Api-Key") != "key-1" || reqs[0].Header.Get("Content-Type") != "application/json" {
		t.Fatalf("headers: %v", reqs[0].Header)
	}
}

func TestDirectPostWithDestOptions(t *testing.T) {
	e := newTestClient(t, nil, func(o *clientOptions) { o.AutoBatchTracing = new(false); o.WorkspaceID = new("ws-own") })
	other := newMock(t, nil)
	run := testRun(1)
	if err := e.c.createRun(context.Background(), run, &destOptions{APIKey: new("k2"), APIURL: new(other.URL), WorkspaceID: new("ws2")}); err != nil {
		t.Fatal(err)
	}
	if len(e.mock.requests()) != 0 || len(other.requests()) != 1 {
		t.Fatal("the replica destination was not used")
	}
	h := other.requests()[0].Header
	if h.Get("X-Api-Key") != "k2" || h.Get("X-Tenant-Id") != "ws2" {
		t.Fatalf("headers: %v", h)
	}
	if err := e.c.createRun(context.Background(), testRun(2), nil); err != nil {
		t.Fatal(err)
	}
	if h := e.mock.requests()[0].Header; h.Get("X-Tenant-Id") != "ws-own" {
		t.Fatalf("workspace header: %v", h)
	}
}

func TestDirectTimeoutIsNotRetried(t *testing.T) {
	e := newTestClient(t, map[string][]mockStep{"runs": {{Status: 200, JSON: "{}", Delay: 400 * time.Millisecond}}}, func(o *clientOptions) {
		o.AutoBatchTracing = new(false)
		o.TimeoutMs = new(60)
	})
	err := e.c.createRun(context.Background(), testRun(1), nil)
	if err == nil || errString(err) != "TimeoutError: The operation was aborted due to timeout" {
		t.Fatalf("err = %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if n := e.mock.count("runs"); n != 1 {
		t.Fatalf("%d requests, a timeout must not be retried", n)
	}
}

func TestDirectRetriesAndContext(t *testing.T) {
	e := newTestClient(t, map[string][]mockStep{"runs": {{Status: 503}, {Status: 429}, {Status: 200}}}, func(o *clientOptions) { o.AutoBatchTracing = new(false) })
	e.c.caller.sleep = noSleep
	if err := e.c.createRun(context.Background(), testRun(1), nil); err != nil {
		t.Fatal(err)
	}
	if n := e.mock.count("runs"); n != 3 {
		t.Fatalf("%d attempts", n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.c.createRun(ctx, testRun(2), nil); err == nil {
		t.Fatal("a cancelled context must fail the call")
	}
}

func TestUpdateRunDirectBodyHasNoID(t *testing.T) {
	e := newTestClient(t, nil, func(o *clientOptions) { o.AutoBatchTracing = new(false) })
	// no trace_id: the direct PATCH body is the run itself (client.js:2204), with the runtime merged into extra
	if err := e.c.updateRun(context.Background(), runIDN(3), obj("outputs", obj("a", 1.0), "extra", obj("metadata", obj("m", 1.0))), nil); err != nil {
		t.Fatal(err)
	}
	r := e.mock.requests()[0]
	if r.Route != "patch" || r.Path != "/runs/"+runIDN(3) {
		t.Fatalf("request %+v", r)
	}
	if got := canonText(string(r.Body)); !strings.HasPrefix(got, `{"outputs":{"a":1},"extra":{"metadata":{"m":1},"runtime":{"library":"langsmith","runtime":"<rt>","sdk":"<sdk>","sdk_version":"<sdkv>"}}}`) {
		t.Fatalf("body %s", got)
	}
	// without extra the run is sent as is
	if err := e.c.updateRun(context.Background(), runIDN(3), obj("outputs", obj("a", 1.0)), nil); err != nil {
		t.Fatal(err)
	}
	if got := string(e.mock.requests()[1].Body); got != `{"outputs":{"a":1}}` {
		t.Fatalf("body %s", got)
	}
	if err := e.c.updateRun(context.Background(), "nope", obj(), nil); err == nil || err.Error() != "Invalid UUID: nope" {
		t.Fatalf("err = %v", err)
	}
}

func TestUnserializableValueIsReplaced(t *testing.T) {
	e := newTestClient(t, nil, func(o *clientOptions) { o.AutoBatchTracing = new(false) })
	run := testRun(1)
	run.set("inputs", make(chan int))
	if err := e.c.createRun(context.Background(), run, nil); err != nil {
		t.Fatal(err)
	}
	if got := string(e.mock.requests()[0].Body); got != "[Unserializable]" {
		t.Fatalf("body %q", got)
	}
	if want := "[WARNING]: LangSmith received unserializable value.\nContext: Creating run with id: " + runIDN(1); e.logText() != want {
		t.Fatalf("log %q", e.logText())
	}
}

// ---- batching ----

func TestSingleFlightServerInfoAndOneBatch(t *testing.T) {
	e := newTestClient(t, map[string][]mockStep{"info": {{Status: 200, JSON: mockDefaultInfo, Delay: 100 * time.Millisecond}}}, nil)
	for i := range 50 {
		mustCreate(t, e.c, testRun(i))
	}
	mustAwait(t, e.c)
	if n := e.mock.count("info"); n != 1 {
		t.Fatalf("%d /info requests, the probe is single-flight", n)
	}
	if n := e.mock.count("multipart"); n != 1 {
		t.Fatalf("%d multipart requests, want one batch of 50", n)
	}
	body := string(e.mock.requests()[1].Body)
	if n := strings.Count(body, `name="post.`+testRunID) - strings.Count(body, `.inputs"`) - strings.Count(body, `.extra"`); n != 50 {
		t.Fatalf("%d runs in the batch", n)
	}
}

func TestBatchWorkersAreBounded(t *testing.T) {
	e := newTestClient(t, map[string][]mockStep{"multipart": {{Status: 200, JSON: "{}", Delay: 80 * time.Millisecond}}}, func(o *clientOptions) { o.BatchSizeLimit = new(1) })
	for i := range 30 {
		mustCreate(t, e.c, testRun(i))
	}
	mustAwait(t, e.c)
	if n := e.mock.count("multipart"); n != 30 {
		t.Fatalf("%d multipart requests, want 30 (one per run)", n)
	}
	e.mock.mu.Lock()
	peak := e.mock.peak
	e.mock.mu.Unlock()
	if peak > 5 || peak < 2 {
		t.Fatalf("peak concurrency %d, want 2..5 (traceBatchConcurrency)", peak)
	}
	e.c.mu.Lock()
	w := e.c.workers
	e.c.mu.Unlock()
	if w != 0 {
		t.Fatalf("%d workers still alive after the queue drained", w)
	}
}

func TestLargeBodyStreamsAndGzipsIntact(t *testing.T) {
	for _, gz := range []bool{false, true} {
		info := `{"batch_ingest_config":{"use_multipart_endpoint":true,"size_limit":100,"size_limit_bytes":104857600},"instance_flags":{"gzip_body_enabled":` + strconv.FormatBool(gz) + `}}`
		e := newTestClient(t, map[string][]mockStep{"info": {{Status: 200, JSON: info}}}, nil)
		big := strings.Repeat("line of text é😀 ", 400_000) // ~7 MB
		run := testRun(1)
		run.set("inputs", obj("big", big))
		mustCreate(t, e.c, run)
		mustAwait(t, e.c)
		reqs := e.mock.requests()
		r := reqs[len(reqs)-1]
		if gz != (r.Header.Get("Content-Encoding") == "gzip") || len(r.TE) == 0 || r.TE[0] != "chunked" {
			t.Fatalf("gzip=%v: encoding %q TE %v", gz, r.Header.Get("Content-Encoding"), r.TE)
		}
		body := r.Body
		if gz {
			zr, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			if body, err = io.ReadAll(zr); err != nil {
				t.Fatal(err)
			}
		}
		ct := r.Header.Get("Content-Type")
		parts, err := splitMultipart(body, ct[strings.Index(ct, "boundary=")+len("boundary="):])
		if err != nil {
			t.Fatal(err)
		}
		var inputs string
		for _, p := range parts {
			if h := p["header"].([]any); strings.HasSuffix(h[0].(string), `.inputs"`) {
				inputs = p["text"].(string)
			}
		}
		want, _, _ := stringify(obj("big", big))
		if sha256.Sum256([]byte(inputs)) != sha256.Sum256(want) {
			t.Fatalf("gzip=%v: the inputs part is not the stringified payload (%d vs %d bytes)", gz, len(inputs), len(want))
		}
	}
}

func TestManualFlushMode(t *testing.T) {
	e := newTestClient(t, nil, func(o *clientOptions) { o.ManualFlushMode = new(true) })
	for i := range 3 {
		mustCreate(t, e.c, testRun(i))
	}
	time.Sleep(400 * time.Millisecond)
	if n := len(e.mock.requests()); n != 0 {
		t.Fatalf("%d requests before flush", n)
	}
	mustAwait(t, e.c)
	if !strings.Contains(e.logText(), "[WARNING]: When tracing in manual flush mode, you must call `await client.flush()` manually to submit trace batches.") {
		t.Fatalf("log %q", e.logText())
	}
	if err := e.c.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.mock.count("multipart") != 1 {
		t.Fatalf("flush sent %d batches", e.mock.count("multipart"))
	}
}

func TestAwaitPendingHonoursContext(t *testing.T) {
	e := newTestClient(t, map[string][]mockStep{"multipart": {{Status: 200, JSON: "{}", Delay: 600 * time.Millisecond}}}, nil)
	mustCreate(t, e.c, testRun(1))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := e.c.awaitPendingTraceBatches(ctx); err != context.DeadlineExceeded {
		t.Fatalf("err = %v", err)
	}
	mustAwait(t, e.c) // the batch still completes
	if e.mock.count("multipart") != 1 {
		t.Fatal("batch lost")
	}
}

func TestPopBelowOneByteIsReported(t *testing.T) {
	e := newTestClient(t, nil, func(o *clientOptions) {
		o.BatchSizeBytesLimit = new(int64(0))
		o.AutoBatchAggregationDelay = new(20 * time.Millisecond)
	})
	mustCreate(t, e.c, testRun(1))
	time.Sleep(200 * time.Millisecond)
	if !strings.Contains(e.logText(), "Error: Number of bytes to pop off may not be less than 1.") {
		t.Fatalf("log %q", e.logText())
	}
	if n := e.mock.count("multipart"); n != 0 {
		t.Fatalf("%d batches sent", n)
	}
}

func TestMultipartRejectsRunsWithoutTraceFields(t *testing.T) {
	e := newTestClient(t, nil, nil)
	_, err := e.c.buildMultipart([]*omap{obj("id", "a", "trace_id", "t")}, nil)
	if err == nil || err.Error() != `Multipart ingest requires "trace_id" and "dotted_order" to be set when creating a run` {
		t.Fatal(err)
	}
	_, err = e.c.buildMultipart(nil, []*omap{obj("id", "a", "dotted_order", "d")})
	if err == nil || err.Error() != `Multipart ingest requires "trace_id" and "dotted_order" to be set when updating a run` {
		t.Fatal(err)
	}
	// null passes the `=== undefined` check
	mp, err := e.c.buildMultipart([]*omap{obj("id", "a", "trace_id", nil, "dotted_order", nil)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mp.release()
	// through the batch worker the failure is logged, as `console.error("Error exporting batch:", e)`
	items := []*queueItem{{action: actionCreate, payload: obj("id", "a"), size: 1}}
	e.c.processBatch(batchJob{items: items, bs: &batchState{items: items, remaining: 1, drain: &drainState{remaining: 1}}})
	if !strings.Contains(e.logText(), `Error exporting batch: Error: Multipart ingest requires "trace_id" and "dotted_order" to be set when creating a run`) {
		t.Fatalf("log %q", e.logText())
	}
}

func TestMergeCreatesAndUpdates(t *testing.T) {
	creates := []*omap{obj("id", "a", "k", 1.0), obj("k", 2.0), obj("id", "b", "k", 3.0), obj("id", "a", "k", 4.0, "extra", "x"), obj("id", "", "k", 5.0)}
	updates := []*omap{obj("id", "b", "k", undef, "u", 1.0), obj("id", "zzz", "k", 9.0), obj("k", 8.0), obj("id", "a", "u", 2.0)}
	mc, mu := mergeCreatesAndUpdates(creates, updates)
	var got []string
	for _, m := range mc {
		b, _, _ := stringify(m)
		got = append(got, string(b))
	}
	if want := `{"id":"a","k":4,"extra":"x","u":2}|{"id":"b","u":1}`; strings.Join(got, "|") != want {
		t.Fatalf("merged creates: %s", strings.Join(got, "|"))
	}
	got = nil
	for _, m := range mu {
		b, _, _ := stringify(m)
		got = append(got, string(b))
	}
	if want := `{"id":"zzz","k":9}|{"k":8}`; strings.Join(got, "|") != want {
		t.Fatalf("standalone updates: %s", strings.Join(got, "|"))
	}
	// one side empty: nothing is merged and nothing is dropped
	c2, u2 := mergeCreatesAndUpdates([]*omap{obj("k", 1.0)}, nil)
	if len(c2) != 1 || len(u2) != 0 {
		t.Fatal("a create without id must survive when there are no updates")
	}
}

func TestAttachments(t *testing.T) {
	e := newTestClient(t, nil, nil)
	run := testRun(1)
	run.set("attachments", obj("a", []any{"text/plain", []byte("hi")}, "b", obj("mimeType", "image/png", "data", []byte{1, 2, 3}), "c", []any{"text/x", "str"}, "d.e", []any{"x", []byte("no")}))
	mp, err := e.c.buildMultipart([]*omap{run}, []*omap{obj("id", run.val("id"), "trace_id", "t", "dotted_order", "d", "attachments", obj("z", []any{"x", []byte("ignored")}))})
	if err != nil {
		t.Fatal(err)
	}
	defer mp.release()
	var all bytes.Buffer
	cur := segCursor{mp: mp}
	for p := cur.next(1 << 20); p != nil; p = cur.next(1 << 20) {
		all.Write(p)
	}
	s := all.String()
	for _, want := range []string{
		"name=\"attachment." + runIDN(1) + ".a\"\r\nContent-Type: text/plain; length=2\r\n\r\nhi\r\n",
		"name=\"attachment." + runIDN(1) + ".b\"\r\nContent-Type: image/png; length=3\r\n\r\n\x01\x02\x03\r\n",
		"name=\"attachment." + runIDN(1) + ".c\"\r\nContent-Type: text/x; length=undefined\r\n\r\nstr\r\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %q", want, s)
		}
	}
	if strings.Contains(s, ".d.e") || strings.Contains(s, "ignored") {
		t.Error("an invalid attachment name or an update's attachments were sent")
	}
	if !strings.Contains(e.logText(), "Skipping attachment 'd.e' for run "+runIDN(1)+": Invalid attachment name. Attachment names must not contain periods ('.'). Please rename the attachment and try again.") {
		t.Errorf("log %q", e.logText())
	}
}

func TestFailedTraceDirectoryErrors(t *testing.T) {
	isolateEnv(t)
	tmp := t.TempDir()
	file := filepath.Join(tmp, "afile")
	os.WriteFile(file, []byte("x"), 0o644)
	t.Setenv("LANGSMITH_FAILED_TRACES_DIR", filepath.Join(file, "sub"))
	e := newTestClient(t, map[string][]mockStep{"multipart": {{Status: 400, Body: "no"}}}, nil)
	t.Setenv("LANGSMITH_FAILED_TRACES_DIR", filepath.Join(file, "sub")) // newTestClient isolates the env again
	c, err := newClient(clientOptions{APIKey: new("k"), APIURL: new(e.mock.URL)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	mustCreate(t, c, testRun(1))
	mustAwait(t, c)
	if !strings.Contains(e.logText(), "LangSmith tracing error: could not write trace to fallback dir "+filepath.Join(file, "sub")+": ") {
		t.Fatalf("log %q", e.logText())
	}
}

func TestFailedTraceFileIsReplayable(t *testing.T) {
	isolateEnv(t)
	dir := filepath.Join(t.TempDir(), "failed", "nested")
	e := newTestClient(t, map[string][]mockStep{"multipart": {{Status: 400, Body: "no"}}}, nil)
	t.Setenv("LANGSMITH_FAILED_TRACES_DIR", dir)
	c, err := newClient(clientOptions{APIKey: new("k"), APIURL: new(e.mock.URL)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	for i := range 2 {
		mustCreate(t, c, testRun(i))
		mustAwait(t, c)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries %v, err %v", entries, err)
	}
	info, _ := entries[0].Info()
	if info.Mode().Perm() != 0o600 && runtime.GOOS != "windows" {
		t.Fatalf("mode %v", info.Mode())
	}
	if strings.HasSuffix(entries[0].Name(), ".tmp") {
		t.Fatal("temp file left behind")
	}
}

// ---- lifecycle ----

func settleGoroutines(base int) int {
	n := runtime.NumGoroutine()
	for i := 0; i < 50 && n > base; i++ {
		time.Sleep(20 * time.Millisecond)
		n = runtime.NumGoroutine()
	}
	return n
}

func TestCloseStopsTimersAndGoroutines(t *testing.T) {
	isolateEnv(t)
	defer swapStderr(swapStderr(&capture{}))
	m := newMock(t, nil)
	http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	base := runtime.NumGoroutine()
	c, err := newClient(clientOptions{APIKey: new("k"), APIURL: new(m.URL), AutoBatchAggregationDelay: new(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, c, testRun(1))
	time.Sleep(150 * time.Millisecond) // the /info answer arrives (after 40 ms), the hour timer is set
	start := time.Now()
	c.close()
	c.close() // idempotent
	if time.Since(start) > time.Second {
		t.Fatal("close was slow")
	}
	if err := c.createRun(context.Background(), testRun(2), nil); err != nil {
		t.Fatal(err)
	}
	if n := len(m.requests()); n != 1 {
		t.Fatalf("%d requests: only the /info probe was expected", n)
	}
	m.Close()
	if n := settleGoroutines(base); n > base {
		buf := make([]byte, 1<<16)
		t.Fatalf("goroutines: %d > %d\n%s", n, base, buf[:runtime.Stack(buf, true)])
	}
}

func TestCloseCancelsInFlightRequests(t *testing.T) {
	isolateEnv(t)
	defer swapStderr(swapStderr(&capture{}))
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/info" {
			io.WriteString(w, mockDefaultInfo)
			return
		}
		<-release
	}))
	defer srv.Close()
	defer close(release)
	c, err := newClient(clientOptions{APIKey: new("k"), APIURL: new(srv.URL), AutoBatchAggregationDelay: new(10 * time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, c, testRun(1))
	time.Sleep(300 * time.Millisecond) // the batch request hangs in the server
	done := make(chan struct{})
	go func() { c.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not cancel the in-flight request")
	}
}

func TestConcurrentCreateAndUpdate(t *testing.T) {
	e := newTestClient(t, nil, func(o *clientOptions) { o.AutoBatchAggregationDelay = new(15 * time.Millisecond) })
	const workers, per = 8, 60
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range per {
				n := w*per + i
				run := testRun(n)
				mustCreate(t, e.c, run)
				if err := e.c.updateRun(context.Background(), runIDN(n), obj("outputs", obj("o", 1.0), "trace_id", run.val("trace_id"), "dotted_order", run.val("dotted_order")), nil); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	mustAwait(t, e.c)
	seen := map[string]int{}
	for _, r := range e.mock.requests() {
		if r.Route != "multipart" {
			continue
		}
		ct := r.Header.Get("Content-Type")
		parts, err := splitMultipart(r.Body, ct[strings.Index(ct, "boundary=")+len("boundary="):])
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range parts {
			name := p["header"].([]any)[0].(string)
			if strings.Contains(name, ".outputs\"") {
				seen[name]++
			}
		}
	}
	if len(seen) != workers*per {
		t.Fatalf("%d distinct runs with outputs, want %d", len(seen), workers*per)
	}
}

// ---- request body ownership, HTTP/1.1 ----

// smallBufListener gives every accepted connection an 8 KiB receive buffer, so a client with a large body is still
// writing when the server has already answered.
type smallBufListener struct{ net.Listener }

func (l smallBufListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		if tc, ok := c.(*net.TCPConn); ok {
			tc.SetReadBuffer(8 << 10)
		}
	}
	return c, err
}

// TestEarlyResponseDoesNotRaceThePooledBody: net/http may keep reading (and later close) a request body after the
// round trip returned when the server answers before reading it. The pooled buffers must not be reused until then.
// The race detector is what fails this test if a buffer is released early.
func TestEarlyResponseDoesNotRaceThePooledBody(t *testing.T) {
	for _, mode := range []string{"create", "update", "batch-endpoint"} {
		t.Run(mode, func(t *testing.T) {
			isolateEnv(t)
			defer swapStderr(swapStderr(&capture{}))
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/info" {
					io.WriteString(w, `{"batch_ingest_config":{"use_multipart_endpoint":false}}`)
					return
				}
				w.WriteHeader(http.StatusUnauthorized) // without reading the 3 MiB body
			}))
			srv.Listener = smallBufListener{srv.Listener}
			srv.Start()
			defer srv.Close()
			hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
				if tc, ok := c.(*net.TCPConn); ok {
					tc.SetWriteBuffer(8 << 10)
				}
				return c, err
			}}}
			defer hc.CloseIdleConnections()
			c, err := newClient(clientOptions{APIKey: new("k"), APIURL: new(srv.URL), HTTPClient: hc, AutoBatchTracing: new(mode == "batch-endpoint"),
				AutoBatchAggregationDelay: new(5 * time.Millisecond)})
			if err != nil {
				t.Fatal(err)
			}
			defer c.close()
			big := strings.Repeat("0123456789abcdef", 3<<16) // 3 MiB
			run := testRun(1)
			run.set("inputs", obj("big", big))
			switch mode {
			case "create":
				if err := c.createRun(context.Background(), run, nil); err == nil || !strings.Contains(err.Error(), "[401]") {
					t.Fatalf("err = %v", err)
				}
			case "update":
				if err := c.updateRun(context.Background(), runIDN(1), obj("inputs", obj("big", big)), nil); err == nil || !strings.Contains(err.Error(), "[401]") {
					t.Fatalf("err = %v", err)
				}
			default:
				if err := c.createRun(context.Background(), run, nil); err != nil {
					t.Fatal(err)
				}
				mustAwait(t, c)
			}
			// churn the pool while the transport may still be reading
			deadline := time.Now().Add(300 * time.Millisecond)
			for time.Now().Before(deadline) {
				b := getBuf()
				b.b = append(b.b[:0], make([]byte, 1<<20)...)
				for i := range b.b {
					b.b[i] = 'x'
				}
				putBuf(b)
			}
		})
	}
}

func TestRawBodyReleasedOnLastClose(t *testing.T) {
	b := getBuf()
	b.b = append(b.b, "hello"...)
	mp := newRawBody(b)
	r := mp.newReader(false)
	mp.release()
	if mp.buf == nil {
		t.Fatal("released while a reader is open")
	}
	got, _ := io.ReadAll(r)
	if string(got) != "hello" {
		t.Fatalf("read %q", got)
	}
	r.Close()
	if mp.buf != nil {
		t.Fatal("not released after the last close")
	}
}

func TestTransportSpeaksHTTP11OverTLS(t *testing.T) {
	isolateEnv(t)
	type seen struct {
		path, proto string
		te          []string
	}
	var mu sync.Mutex
	var reqs []seen
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		mu.Lock()
		reqs = append(reqs, seen{r.URL.Path, r.Proto, r.TransferEncoding})
		mu.Unlock()
		if r.URL.Path == "/info" {
			io.WriteString(w, mockDefaultInfo)
			return
		}
		io.WriteString(w, "{}")
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	cfg := &tls.Config{RootCAs: pool}

	// control: a stock transport negotiates HTTP/2 with this server (it appends "h2" to the config it is given, so it
	// gets a copy)
	ctl := http.DefaultTransport.(*http.Transport).Clone()
	ctl.TLSClientConfig = cfg.Clone()
	ctl.ForceAttemptHTTP2 = true
	resp, err := (&http.Client{Transport: ctl}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Proto != "HTTP/2.0" {
		t.Fatalf("control proto %q: the server does not offer HTTP/2, the test proves nothing", resp.Proto)
	}
	ctl.CloseIdleConnections()
	mu.Lock()
	reqs = nil // the control's own request
	mu.Unlock()

	c, err := newClient(clientOptions{APIKey: new("k"), APIURL: new(srv.URL)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	c.ownTransport.TLSClientConfig = cfg.Clone()
	mustCreate(t, c, testRun(1))
	mustAwait(t, c)
	mu.Lock()
	defer mu.Unlock()
	var multipart bool
	for _, r := range reqs {
		if r.proto != "HTTP/1.1" {
			t.Errorf("%s over %s", r.path, r.proto)
		}
		if r.path == "/runs/multipart" {
			multipart = true
			if len(r.te) == 0 || r.te[0] != "chunked" {
				t.Errorf("multipart transfer encoding %v", r.te)
			}
		}
	}
	if !multipart {
		t.Fatalf("no multipart request: %+v", reqs)
	}
}
