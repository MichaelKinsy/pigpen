package langsmith

// Internal tests added by the mutation proof (port/mutations.md) for survivors in client*.go, runtree.go, convert.go and
// metadata.go that the differential tests did not reach: pinned defaults, header and environment handling, the blocking
// root update, the direct-path event filter, and the multi-replica reroot bookkeeping.

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMutClientDefaultsAreTheSDKs(t *testing.T) {
	// client.js:156-161, 1027 and the constructor defaults.
	if defaultBatchSizeLimit != 100 || defaultUncompressedBatchSizeLimitBytes != 24*1024*1024 || defaultMaxSizeBytes != 1024*1024*1024 {
		t.Errorf("batch limits %d %d %d", defaultBatchSizeLimit, defaultUncompressedBatchSizeLimitBytes, defaultMaxSizeBytes)
	}
	if serverInfoRequestTimeout != 10*time.Second || defaultRequestTimeout != 90*time.Second || defaultAutoBatchDelay != 250*time.Millisecond {
		t.Errorf("timeouts %v %v %v", serverInfoRequestTimeout, defaultRequestTimeout, defaultAutoBatchDelay)
	}
	if defaultTraceBatchConcurrency != 5 || defaultFailedTracesMaxBytes != 100*1024*1024 {
		t.Errorf("concurrency %d failed traces %d", defaultTraceBatchConcurrency, defaultFailedTracesMaxBytes)
	}
}

func TestMutServerInfoWithoutLimitsFallsBackToTheDefaults(t *testing.T) {
	e := newTestClient(t, map[string][]mockStep{"info": {{Status: 200, JSON: "{}"}}}, nil)
	if got := e.c.sizeLimit(&serverInfo{}); got != 100 {
		t.Errorf("batch size limit %v", got)
	}
	if got := e.c.sizeLimitBytes(&serverInfo{}); got != 24*1024*1024 {
		t.Errorf("batch bytes limit %v", got)
	}
}

func TestMutRequestsCarryTheUserAgentAndKey(t *testing.T) {
	e := newTestClient(t, nil, func(o *clientOptions) { o.AutoBatchTracing = new(false) })
	mustCreate(t, e.c, testRun(1))
	h := e.mock.requests()[0].Header
	if got := h.Get("User-Agent"); got != "pigpen-langsmith-go/"+portVersion {
		t.Errorf("User-Agent %q", got)
	}
	if h.Get("X-Api-Key") != "key-1" || h.Get("Authorization") != "" {
		t.Errorf("credentials %v", h)
	}
}

func TestMutHandle429(t *testing.T) {
	start := time.Now()
	if handle429(&http.Response{StatusCode: 503, Header: http.Header{"Retry-After": {"1"}}}) {
		t.Error("only a 429 is handled")
	}
	if time.Since(start) > time.Second {
		t.Error("a non-429 must not wait")
	}
	if !handle429(&http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"1"}}}) {
		t.Error("a 429 with retry-after is handled")
	}
	if d := time.Since(start); d < time.Second || d > 5*time.Second {
		t.Errorf("waited %v for retry-after: 1", d)
	}
}

func TestMutDebugAndBackgroundEnvironment(t *testing.T) {
	isolateEnv(t)
	capt := &capture{}
	defer swapStderr(swapStderr(capt))
	t.Setenv("LANGSMITH_DEBUG", "false")
	c, err := newClient(clientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c.debug {
		t.Error(`LANGSMITH_DEBUG=false is not debug`)
	}
	c.close()
	t.Setenv("LANGSMITH_DEBUG", "true")
	c, _ = newClient(clientOptions{})
	c.close()
	if got := strings.Join(capt.logs, "\n"); got != "LangSmith API URL https://api.smith.langchain.com resolved from built-in default" {
		t.Errorf("logs %q", got)
	}
	for bg, want := range map[string]bool{"false": true, "true": false, "1": false, "": false} {
		t.Setenv("LANGSMITH_DEBUG", "")
		t.Setenv("LANGSMITH_TRACING_BACKGROUND", bg)
		c, err := newClient(clientOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if c.blockOnRootRunFinalization != want {
			t.Errorf("LANGSMITH_TRACING_BACKGROUND=%q: block %v, want %v", bg, c.blockOnRootRunFinalization, want)
		}
		c.close()
	}
}

func TestMutProfileURLSourceAndKeyPrecedence(t *testing.T) {
	isolateEnv(t)
	profileEnv(t, oauthConfig("https://profile.example", "tok", "", jsISOString(nowMs()+3_600_000)))
	cfg, err := getDefaultClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.apiURL != "https://profile.example" || cfg.apiURLSource != "profile config" || cfg.profileAuth == nil {
		t.Errorf("profile config %+v", cfg)
	}
	c, err := newClient(clientOptions{APIKey: new("explicit")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	if c.profileAuth != nil {
		t.Error("an explicit API key wins: the profile's credentials are not used")
	}
}

func TestMutFetchErrorTexts(t *testing.T) {
	e := newTestClient(t, nil, nil)
	ctx := context.Background()
	if _, err := e.c.fetch(ctx, ctx, "GET", "://bad", http.Header{}, nil, 0); err == nil || err.Error() != "Failed to parse URL from ://bad" {
		t.Errorf("bad URL: %v", err)
	}
	if _, err := e.c.fetch(ctx, ctx, "GET", "http://127.0.0.1:1/", http.Header{}, nil, 0); err == nil || err.Error() != "fetch failed" {
		t.Errorf("connection refused: %v", err)
	}
}

func TestMutFetchLogsWithTheDebugEnvironment(t *testing.T) {
	e := newTestClient(t, nil, nil)
	t.Setenv("LANGCHAIN_DEBUG", "true")
	ctx := context.Background()
	resp, err := e.c.fetch(ctx, ctx, "GET", e.mock.URL+"/info", http.Header{}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if text := e.logText(); !strings.Contains(text, "→ GET "+e.mock.URL+"/info") || !strings.Contains(text, "← 200 OK "+e.mock.URL+"/info") {
		t.Errorf("debug log %q", text)
	}
}

func TestMutDirectUpdateFiltersNewTokenEvents(t *testing.T) {
	e := newTestClient(t, nil, func(o *clientOptions) { o.AutoBatchTracing = new(false) })
	events := []any{obj("name", "new_token", "kwargs", obj("a", 1.0)), obj("name", "other", "kwargs", obj("b", 2.0))}
	if err := e.c.updateRun(context.Background(), runIDN(1), obj("events", events), nil); err != nil {
		t.Fatal(err)
	}
	if got := string(e.mock.requests()[0].Body); got != `{"events":[{"name":"new_token"},{"name":"other","kwargs":{"b":2}}]}` {
		t.Fatalf("%s", got)
	}
}

func TestMutPrepareRunInPlaceFiltersEventsAndHidesOutputs(t *testing.T) {
	e := newTestClient(t, nil, func(o *clientOptions) { o.HideOutputs = new(true) })
	run := obj("outputs", obj("a", 1.0), "events", []any{obj("name", "new_token", "kwargs", obj("a", 1.0))})
	e.c.prepareRunInPlace(run)
	if b, _, _ := stringify(run); string(b) != `{"outputs":{},"events":[{"name":"new_token"}]}` {
		t.Fatalf("%s", b)
	}
}

func TestMutBlockingRootUpdate(t *testing.T) {
	update := func(parent any, withEnd bool) *omap {
		id := runIDN(7)
		run := obj("trace_id", id, "dotted_order", "20240301T100000010000Z"+id, "parent_run_id", parent, "outputs", obj("o", 1.0))
		if withEnd {
			run.set("end_time", float64(1709287200000))
		}
		return run
	}
	block := func(o *clientOptions) {
		o.BlockOnRootRunFinalization = new(true)
		o.AutoBatchAggregationDelay = new(time.Hour)
	}
	call := func(e *testEnv, run *omap) error {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		return e.c.updateRun(ctx, runIDN(7), run, nil)
	}
	// A root update with an end time waits until it is sent (here: until the context gives up, the timer is an hour).
	if err := call(newTestClient(t, nil, block), update(undef, true)); err == nil {
		t.Error("a finished root run blocks until its batch is sent")
	}
	// A child run, or a root without an end time, does not wait.
	if err := call(newTestClient(t, nil, block), update(runIDN(8), true)); err != nil {
		t.Errorf("a child update must not block: %v", err)
	}
	if err := call(newTestClient(t, nil, block), update(undef, false)); err != nil {
		t.Errorf("an update without an end time must not block: %v", err)
	}
	// With the default delay the blocking update returns after the batch went out.
	e := newTestClient(t, nil, func(o *clientOptions) { o.BlockOnRootRunFinalization = new(true) })
	if err := call(e, update(undef, true)); err != nil {
		t.Fatal(err)
	}
	if e.mock.count("multipart") != 1 {
		t.Errorf("the blocking update returned before the batch was sent: %d", e.mock.count("multipart"))
	}
}

func TestMutMergeRuntimeEnvReadsTheEnvironmentWithoutACache(t *testing.T) {
	isolateEnv(t)
	t.Setenv("LANGSMITH_FOO", "bar")
	out := mergeRuntimeEnvIntoRun(obj("extra", obj()), nil, false, nil)
	extra, _ := out.val("extra").(*omap)
	md, _ := extra.val("metadata").(*omap)
	if md.val("LANGSMITH_FOO") != "bar" {
		t.Fatalf("metadata %v", md)
	}
}

type mutRecClient struct{ created []*omap }

func (c *mutRecClient) createRun(_ context.Context, run *omap, _ *destOptions) error {
	c.created = append(c.created, run)
	return nil
}
func (c *mutRecClient) updateRun(context.Context, string, *omap, *destOptions) error { return nil }

func TestMutEventWithoutKwargsHasNoKwargsKey(t *testing.T) {
	var clock int64 = 1700000000123
	var buf bytes.Buffer
	defer resetRtSeams(&clock, newOmap(), &buf)()
	t.Setenv("LANGSMITH_RUNS_ENDPOINTS", "")
	r, err := newRunTree(runTreeConfig{Name: "n", ProjectName: "p", Client: rtNopClient{}, Replicas: []replicaConfig{}})
	if err != nil {
		t.Fatal(err)
	}
	r.addEvent("x", nil)
	if b, _, _ := stringify(r.events[0]); strings.Contains(string(b), "kwargs") {
		t.Fatalf("%s", b)
	}
}

func TestMutUpdateCarriesTheConfiguredParentRunID(t *testing.T) {
	var clock int64 = 1700000000123
	var buf bytes.Buffer
	defer resetRtSeams(&clock, newOmap(), &buf)()
	r, err := newRunTree(runTreeConfig{Name: "n", ProjectName: "p", Client: rtNopClient{}, Replicas: []replicaConfig{}, ParentRunID: "parent-x"})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.nonReplicaUpdate(false).val("parent_run_id"); got != "parent-x" {
		t.Fatalf("parent_run_id %v", got)
	}
}

func TestMutRerootOfTwoReplicasKeepsBoth(t *testing.T) {
	var clock int64 = 1700000000123
	var buf bytes.Buffer
	defer resetRtSeams(&clock, newOmap(), &buf)()
	t.Setenv("LANGSMITH_RUNS_ENDPOINTS", "")
	rec := &mutRecClient{}
	yes, a, b := true, "A", "B"
	root, err := newRunTree(runTreeConfig{Name: "root", ProjectName: "p", Client: rec, Replicas: []replicaConfig{}})
	if err != nil {
		t.Fatal(err)
	}
	mid, err := root.createChild(runTreeConfig{Name: "mid", Replicas: []replicaConfig{{ProjectName: &a, Reroot: &yes}, {ProjectName: &b, Reroot: &yes}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mid.postRun(ctx)
	leaf, err := mid.createChild(runTreeConfig{Name: "leaf"})
	if err != nil {
		t.Fatal(err)
	}
	rec.created = nil
	leaf.postRun(ctx)
	if len(rec.created) != 2 {
		t.Fatalf("%d payloads", len(rec.created))
	}
	// Both replicas rerooted at mid: below it each trace id is the deterministic id of mid in that replica's project.
	for i, project := range []string{"A", "B"} {
		if got, want := strVal(rec.created[i], "trace_id"), nonCryptographicUuid7Deterministic(mid.ID, project); got != want {
			t.Errorf("replica %s: trace_id %s, want %s", project, got, want)
		}
	}
}

func TestMutRemoteProviderPrefersGithub(t *testing.T) {
	if _, provider, _, ok := parseRemote("https://github.gitlab.example/o/r.git"); !ok || provider != "github" {
		t.Fatalf("provider %q", provider)
	}
}

func TestMutGeminiTextPartWinsOverFunctionCall(t *testing.T) {
	payload := map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{
		map[string]any{"text": "t", "functionCall": map[string]any{"name": "f"}},
	}}}}
	got, err := convertProviderPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if b, _, _ := stringify(got); string(b) != `{"messages":[{"role":"user","content":[{"type":"text","text":"t","functionCall":{"name":"f"}}]}]}` {
		t.Fatalf("%s", b)
	}
}
