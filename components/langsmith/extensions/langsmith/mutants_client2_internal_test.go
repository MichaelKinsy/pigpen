package langsmith

// More internal tests added by the mutation proof (port/mutations.md): batching thresholds and timers, destination
// routing, error logging at close, gzip, queue and caller boundaries.

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func mutWaitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func TestMutSampledByIDIsStrictlyBelowTheRate(t *testing.T) {
	const target = 500000
	var id string
	for i := 0; i < 50_000_000; i++ {
		if s := "id-" + strconv.Itoa(i); samplingBucket(s) == target {
			id = s
			break
		}
	}
	if id == "" {
		t.Fatal("no identifier with bucket 500000 found")
	}
	rate := 0.5
	if isSampledByID(id, &rate) {
		t.Error("bucket/1e6 == rate is not below the rate")
	}
	rate = 0.500001
	if !isSampledByID(id, &rate) {
		t.Error("a rate above the bucket samples")
	}
}

func TestMutQueueAcceptsUpToTheSoftLimit(t *testing.T) {
	q := autoBatchQueue{maxSizeBytes: 10}
	if !q.push(&queueItem{size: 4}) || !q.push(&queueItem{size: 6}) {
		t.Fatal("a queue exactly at its limit accepts")
	}
	if q.push(&queueItem{size: 1}) {
		t.Fatal("a queue over its limit refuses")
	}
}

func TestMutSerializeUndefined(t *testing.T) {
	if got := string(serializeForTracing(nil, undef, nil)); got != "undefined" {
		t.Fatalf("%q", got)
	}
}

func rootUpdate(parent any, withEnd bool) *omap {
	id := runIDN(7)
	run := obj("trace_id", id, "dotted_order", "20240301T100000010000Z"+id, "parent_run_id", parent, "outputs", obj("o", 1.0))
	if withEnd {
		run.set("end_time", float64(1709287200000))
	}
	return run
}

func TestMutDroppedItemDoesNotBlockARootUpdate(t *testing.T) {
	e := newTestClient(t, nil, func(o *clientOptions) {
		o.BlockOnRootRunFinalization = new(true)
		o.AutoBatchAggregationDelay = new(time.Hour)
		o.MaxIngestMemoryBytes = new(int64(1))
	})
	mustCreate(t, e.c, testRun(1)) // the first item is accepted by the empty queue
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if err := e.c.updateRun(ctx, runIDN(7), rootUpdate(undef, true), nil); err != nil {
		t.Fatalf("an update the queue dropped has nothing to wait for: %v", err)
	}
	if !strings.Contains(e.logText(), "AutoBatchQueue size limit (1 bytes) exceeded. Dropping run with id:") {
		t.Fatalf("log %q", e.logText())
	}
}

func TestMutAfterLimitsOnAClosedClientSchedulesNothing(t *testing.T) {
	e := newTestClient(t, nil, nil)
	e.c.close()
	e.c.mu.Lock()
	e.c.queue.push(&queueItem{size: 1, payload: obj()})
	e.c.mu.Unlock()
	e.c.afterLimits(1e9, 1e9)
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	if e.c.timer != nil || len(e.c.timers) != 0 {
		t.Fatal("a closed client starts no timer")
	}
}

func TestMutByteLimitFlushesAtOnce(t *testing.T) {
	e := newTestClient(t, nil, func(o *clientOptions) {
		o.BatchSizeBytesLimit = new(int64(1))
		o.AutoBatchAggregationDelay = new(time.Hour)
	})
	mustCreate(t, e.c, testRun(1))
	if !mutWaitFor(5*time.Second, func() bool { return e.mock.count("multipart") == 1 }) {
		t.Fatal("a queue over the byte limit drains without waiting for the timer")
	}
}

func TestMutCountLimitIsExclusive(t *testing.T) {
	e := newTestClient(t, nil, func(o *clientOptions) {
		o.BatchSizeLimit = new(2)
		o.AutoBatchAggregationDelay = new(time.Hour)
	})
	mustCreate(t, e.c, testRun(1))
	mustCreate(t, e.c, testRun(2))
	time.Sleep(400 * time.Millisecond)
	if n := e.mock.count("multipart"); n != 0 {
		t.Fatalf("two runs do not exceed a limit of two: %d batches", n)
	}
	mustCreate(t, e.c, testRun(3))
	if !mutWaitFor(5*time.Second, func() bool { return e.mock.count("multipart") >= 1 }) {
		t.Fatal("three runs exceed a limit of two")
	}
}

func TestMutAggregationTimerUsesTheConfiguredDelay(t *testing.T) {
	e := newTestClient(t, nil, func(o *clientOptions) { o.AutoBatchAggregationDelay = new(500 * time.Millisecond) })
	start := time.Now()
	mustCreate(t, e.c, testRun(1))
	time.Sleep(250 * time.Millisecond)
	if e.mock.count("multipart") != 0 {
		t.Fatal("sent before the aggregation delay")
	}
	if !mutWaitFor(900*time.Millisecond-time.Since(start), func() bool { return e.mock.count("multipart") == 1 }) {
		t.Fatalf("not sent %v after the run, the delay is 500 ms", time.Since(start))
	}
}

func TestMutCancelledExportIsNotLogged(t *testing.T) {
	// The legacy /runs/batch endpoint returns its errors (the multipart one warns and swallows them), so an export
	// error is logged unless the client was closed under the request.
	e := newTestClient(t, map[string][]mockStep{
		"info":  {{Status: 200, JSON: `{"batch_ingest_config":{"use_multipart_endpoint":false}}`}},
		"batch": {{Status: 200, JSON: "{}", Delay: 2 * time.Second}},
	}, nil)
	mustCreate(t, e.c, testRun(1))
	if !mutWaitFor(5*time.Second, func() bool { return e.mock.count("batch") == 1 }) {
		t.Fatal("no request")
	}
	e.c.close()
	if strings.Contains(e.logText(), "Error exporting batch") {
		t.Fatalf("a request cancelled by close is not an export error: %q", e.logText())
	}
}

func TestMutEmptyMultipartBuildsNothing(t *testing.T) {
	e := newTestClient(t, nil, nil)
	mp, err := e.c.buildMultipart(nil, nil)
	if mp != nil || err != nil {
		t.Fatalf("%v %v", mp, err)
	}
}

func TestMutMultipartFailureContextJoinsRunsWithASemicolon(t *testing.T) {
	e := newTestClient(t, map[string][]mockStep{"multipart": {{Status: 400, Body: "no"}}}, nil)
	mustCreate(t, e.c, testRun(1))
	mustCreate(t, e.c, testRun(2))
	mustAwait(t, e.c)
	if want := "Context: trace=" + runIDN(1) + ",id=" + runIDN(1) + "; trace=" + runIDN(2) + ",id=" + runIDN(2); !strings.Contains(e.logText(), want) {
		t.Fatalf("log %q", e.logText())
	}
}

func TestMutDestinationOptionsRouteTheBatch(t *testing.T) {
	for _, multipart := range []bool{true, false} {
		script := map[string][]mockStep{}
		route := "multipart"
		if !multipart {
			route = "batch"
			script["info"] = []mockStep{{Status: 200, JSON: `{"batch_ingest_config":{"use_multipart_endpoint":false}}`}}
		}
		e := newTestClient(t, script, nil)
		other := newMock(t, nil)
		if err := e.c.createRun(context.Background(), testRun(1), &destOptions{APIURL: new(other.URL), APIKey: new("k2")}); err != nil {
			t.Fatal(err)
		}
		mustAwait(t, e.c)
		if other.count(route) != 1 || e.mock.count(route) != 0 {
			t.Fatalf("multipart=%v: %s requests: other %d, primary %d", multipart, route, other.count(route), e.mock.count(route))
		}
		if got := other.requests()[0].Header.Get("X-Api-Key"); got != "k2" {
			t.Errorf("multipart=%v: x-api-key %q", multipart, got)
		}
	}
}

func TestMutBatchIngestPreparesTheRuns(t *testing.T) {
	e := newTestClient(t, nil, func(o *clientOptions) { o.HideInputs = new(true) })
	if err := e.c.batchIngestRuns(context.Background(), []*omap{testRun(1)}, nil, sendOpts{}); err != nil {
		t.Fatal(err)
	}
	body := string(e.mock.requests()[0].Body)
	if !strings.Contains(body, `"inputs":{}`) || strings.Contains(body, "question") {
		t.Fatalf("%s", body)
	}
}

func TestMutFallbackDirSizeCountsTraceFilesOnly(t *testing.T) {
	dir := t.TempDir()
	for name, n := range map[string]int{"trace_a.json": 10, "other.json": 100, "trace_b.txt": 5} {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got, ok := fallbackDirSize(dir); !ok || got != 10 {
		t.Fatalf("%d %v", got, ok)
	}
}

func TestMutGzipBodyIsCompressed(t *testing.T) {
	info := `{"batch_ingest_config":{"use_multipart_endpoint":true,"size_limit":100,"size_limit_bytes":104857600},"instance_flags":{"gzip_body_enabled":true}}`
	e := newTestClient(t, map[string][]mockStep{"info": {{Status: 200, JSON: info}}}, nil)
	run := testRun(1)
	run.set("inputs", obj("big", strings.Repeat("a", 1_000_000)))
	mustCreate(t, e.c, run)
	mustAwait(t, e.c)
	reqs := e.mock.requests()
	if r := reqs[len(reqs)-1]; len(r.Body) > 20_000 {
		t.Fatalf("a megabyte of one letter gzips to far less than %d bytes", len(r.Body))
	}
}

func TestMutReadOnAClosedBodyFails(t *testing.T) {
	mp := newTestMultipart(t, 1)
	r := mp.newReader(false)
	r.Close()
	if _, err := r.Read(make([]byte, 8)); err == nil {
		t.Fatal("reading a closed request body is an error")
	}
	mp.release()
}

func TestMutNetworkErrorMessages(t *testing.T) {
	if !isNetworkError(&fetchTypeError{Message: "error sending request for url (http://x)"}) {
		t.Error("the Deno message is a network error")
	}
	if isNetworkError(&fetchTypeError{Message: "error sending request: boom"}) {
		t.Error("only the Deno prefix `error sending request for url` counts")
	}
}

func TestMutErrorWithoutStatusIsRetried(t *testing.T) {
	c := newAsyncCaller(callerParams{MaxRetries: 4, MaxQueueSizeBytes: -1})
	c.sleep = func(context.Context, time.Duration) error { return nil }
	calls := 0
	_, err := c.call(context.Background(), func(context.Context) (*http.Response, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("boom")
		}
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("%d calls, err %v: an error that carries no status is retried", calls, err)
	}
}

func TestMutGoSendOnAClosedClientRunsInline(t *testing.T) {
	e := newTestClient(t, nil, nil)
	e.c.close()
	j := batchJob{bs: &batchState{remaining: 1, drain: &drainState{remaining: 1}}}
	var returned, inline atomic.Bool
	done := make(chan struct{})
	e.c.goSend(j, func() error {
		time.Sleep(50 * time.Millisecond)
		inline.Store(!returned.Load())
		close(done)
		return nil
	})
	returned.Store(true)
	<-done
	if !inline.Load() {
		t.Fatal("on a closed client the send runs on the caller's goroutine")
	}
}

func TestMutDrainSplitsGroupsByWorkspace(t *testing.T) {
	e := newTestClient(t, nil, nil)
	u, k, w1, w2 := "https://x.example", "k", "w1", "w2"
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	e.c.queue.push(&queueItem{action: actionCreate, payload: obj(), size: 1, dest: destOptions{APIURL: &u, APIKey: &k, WorkspaceID: &w1}})
	e.c.queue.push(&queueItem{action: actionCreate, payload: obj(), size: 1, dest: destOptions{APIURL: &u, APIKey: &k, WorkspaceID: &w2}})
	_, jobs, err := e.c.drainLocked(1e9, 100, false)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("%d groups, err %v: two workspaces are two destinations", len(jobs), err)
	}
}

func TestMutServerInfoIsFetchedOnce(t *testing.T) {
	e := newTestClient(t, nil, nil)
	for i := range 2 {
		mustCreate(t, e.c, testRun(i))
		mustAwait(t, e.c)
	}
	if n := e.mock.count("info"); n != 1 {
		t.Fatalf("%d /info requests: a successful probe is cached", n)
	}
}

func TestMutAwaitWaitsForTheQueuedRun(t *testing.T) {
	e := newTestClient(t, nil, nil)
	mustCreate(t, e.c, testRun(1))
	mustAwait(t, e.c)
	if n := e.mock.count("multipart"); n != 1 {
		t.Fatalf("awaitPendingTraceBatches returned with the run still queued: %d batches sent", n)
	}
}
