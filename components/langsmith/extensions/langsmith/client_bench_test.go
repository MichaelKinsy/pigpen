package langsmith

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// benchRun is a run the size of a typical agent turn: an LLM call with messages and a tool result.
func benchRun(n int) *omap {
	id := runIDN(n)
	msgs := make([]any, 0, 6)
	for i := range 6 {
		msgs = append(msgs, obj("role", "user", "content", strings.Repeat("message body é ", 20)+strconv.Itoa(i), "timestamp", float64(1700000000000+i)))
	}
	return obj("id", id, "name", "turn", "start_time", "2024-03-01T10:00:00.010000", "end_time", "2024-03-01T10:00:09.010000",
		"run_type", "llm", "reference_example_id", undef,
		"extra", obj("metadata", obj("model", "m", "provider", "p", "thinking", "high", "turn", float64(n)), "runtime", obj("library", "langsmith")),
		"serialized", undef, "error", undef, "inputs", obj("messages", msgs, "tools", []any{}), "outputs", obj("output", obj("role", "assistant", "content", strings.Repeat("answer ", 60))),
		"session_name", "proj", "child_runs", []any{}, "parent_run_id", undef, "trace_id", id, "dotted_order", "20240301T100000010000Z"+id,
		"tags", []string{"pi", "t"}, "attachments", undef, "events", undef)
}

// BenchmarkCreateRunEnqueue is the hot path of createRun: prepare, runtime merge, size estimate and queue push.
// The queue is drained by a worker against a null server every 2000 runs so that memory stays flat.
func BenchmarkCreateRunEnqueue(b *testing.B) {
	isolateEnv(b)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/info" {
			io.WriteString(w, mockDefaultInfo)
			return
		}
		io.WriteString(w, "{}")
	}))
	defer srv.Close()
	c, err := newClient(clientOptions{APIKey: new("k"), APIURL: new(srv.URL), BatchSizeLimit: new(2000), BatchSizeBytesLimit: new(int64(1 << 30))})
	if err != nil {
		b.Fatal(err)
	}
	defer c.close()
	ctx := context.Background()
	runs := make([]*omap, b.N)
	for i := range runs {
		runs[i] = benchRun(i)
	}
	if err := c.createRun(ctx, benchRun(-1), nil); err != nil { // learn the server info
		b.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		if err := c.createRun(ctx, runs[i], nil); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	mustAwait(b, c)
}

// BenchmarkMultipartAssemble builds the multipart body of a 100-run batch (create + update merged), the
// serialisation half of a batch export.
func BenchmarkMultipartAssemble(b *testing.B) {
	isolateEnv(b)
	c, err := newClient(clientOptions{APIKey: new("k"), APIURL: new("http://127.0.0.1:1")})
	if err != nil {
		b.Fatal(err)
	}
	defer c.close()
	b.ReportAllocs()
	var total int64
	for range b.N {
		b.StopTimer()
		creates := make([]*omap, 100)
		updates := make([]*omap, 100)
		for i := range creates {
			creates[i] = benchRun(i)
			updates[i] = obj("id", creates[i].val("id"), "trace_id", creates[i].val("trace_id"), "dotted_order", creates[i].val("dotted_order"),
				"end_time", "2024-03-01T10:00:09.010000", "outputs", obj("o", "x"), "extra", obj("metadata", obj("done", true)))
		}
		b.StartTimer()
		mp, err := c.buildMultipart(creates, updates)
		if err != nil {
			b.Fatal(err)
		}
		total += mp.total
		mp.release()
	}
	b.SetBytes(total / int64(b.N))
}

// BenchmarkEndToEnd sends 1000 runs (create, then update) through the whole client to a loopback server.
func BenchmarkEndToEnd(b *testing.B) {
	isolateEnv(b)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/info" {
			io.WriteString(w, mockDefaultInfo)
			return
		}
		io.WriteString(w, "{}")
	}))
	defer srv.Close()
	c, err := newClient(clientOptions{APIKey: new("k"), APIURL: new(srv.URL), AutoBatchAggregationDelay: new(5 * time.Millisecond)})
	if err != nil {
		b.Fatal(err)
	}
	defer c.close()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		for i := range 1000 {
			run := benchRun(i)
			_ = c.createRun(ctx, run, nil)
			_ = c.updateRun(ctx, runIDN(i), obj("outputs", obj("o", "x"), "end_time", "2024-03-01T10:00:09.010000", "trace_id", run.val("trace_id"), "dotted_order", run.val("dotted_order")), nil)
		}
		if err := c.awaitPendingTraceBatches(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEstimateSerializedSize(b *testing.B) {
	run := benchRun(1)
	b.ReportAllocs()
	for range b.N {
		estimateSerializedSize(run)
	}
}

func BenchmarkMergeRuntimeEnv(b *testing.B) {
	isolateEnv(b)
	env := getLangSmithEnvVarsMetadata()
	b.ReportAllocs()
	for i := range b.N {
		mergeRuntimeEnvIntoRun(benchRun(i), env, false, nil)
	}
}
