package warden

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Twins of tests/filter.test.ts (the two config cases, :33 and :48, belong to the config loader: see the skip list).

type judgeFunc func(ctx context.Context, r Request) (Evaluation, error)

func (f judgeFunc) Evaluate(ctx context.Context, r Request) (Evaluation, error) { return f(ctx, r) }

func filterConfig() FilterConfig { return DefaultConfig().Context.Filter }

var filterInput = FilterInput{Tool: "bash", Command: "npm test", Task: "Fix the failing parser test", Plan: "Run the suite to see which test fails."}

// filterLog is a synthetic log: numbered progress lines with a few marked lines the fake judge rates as useful.
func filterLog(n int, marked ...int) string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("line %d progress ok %s", i, strings.Repeat("·", 40))
		for _, m := range marked {
			if m == i {
				out[i] = fmt.Sprintf("line %d NEEDLE assertion failed in parser.test.ts:%d", i, i)
			}
		}
	}
	return strings.Join(out, "\n") + "\nsummary: 1 failed, 399 passed\n"
}

// scoringJudge scores each chunk from its text and records every request.
type scoringJudge struct {
	mu    sync.Mutex
	sent  []Request
	rate  func(chunk string) float64
	delay time.Duration
}

func (j *scoringJudge) Evaluate(ctx context.Context, r Request) (Evaluation, error) {
	j.mu.Lock()
	j.sent = append(j.sent, r)
	j.mu.Unlock()
	if j.delay > 0 {
		select {
		case <-time.After(j.delay):
		case <-ctx.Done():
			return Evaluation{}, ctx.Err()
		}
	}
	answers := map[string]Answer{}
	for id := range r.Questions {
		answers[id] = Answer{Type: "score", Score: j.rate(fmt.Sprint(r.State[id])), Confidence: 0.9}
	}
	return Evaluation{Model: "jev-test", ElapsedMs: int(j.delay.Milliseconds()), Answers: answers, InputTokens: 1}, nil
}

func needle(chunk string) float64 {
	if strings.Contains(chunk, "NEEDLE") {
		return 3
	}
	return 0
}

// twin: tests/filter.test.ts:56
func TestChunksEndAtLineBoundariesNearChunkCharsOnlyALongLineIsSplit(t *testing.T) {
	text := filterLog(300)
	chunks := ChunkLines(text, 2000)
	if strings.Join(chunks, "") != text {
		t.Fatal("chunks join back to the exact output")
	}
	for _, c := range chunks[:len(chunks)-1] {
		if n := utf16Len(c); n > 2000 || !strings.HasSuffix(c, "\n") || n <= 2000-60 {
			t.Fatalf("chunk of %d units, ends with newline %v", n, strings.HasSuffix(c, "\n"))
		}
	}
	long := "short\n" + strings.Repeat("x", 4500) + "\nend"
	var sizes []int
	for _, c := range ChunkLines(long, 2000) {
		sizes = append(sizes, utf16Len(c))
	}
	mustEqual(t, sizes, []int{6, 2000, 2000, 501, 3}, "split sizes")
	if strings.Join(ChunkLines(long, 2000), "") != long {
		t.Error("join")
	}
}

// twin: tests/filter.test.ts:70
func TestSelectionKeepsChunksAtOrAboveMinScoreInOrderMarksGapsAndKeepsTheTail(t *testing.T) {
	chunks := []string{"a1\na2\n", "b1\n", "c1\nc2\nc3\n", "d1\n", strings.Repeat("x", 1500) + "\n", strings.Repeat("e", 900) + "\n"}
	kept, ok := SelectChunks(chunks, []float64{1.4, 1.5, 0, 3, 0, 0}, 1.5, 6000)
	if !ok {
		t.Fatal("nothing selected")
	}
	all := strings.Join(chunks, "")
	tail := utf16Slice(all, utf16Len(all)-1000, -1)
	if want := "[… 2 lines omitted …]\nb1\n[… 3 lines omitted …]\nd1\n[… 1 line omitted …]\n" + tail; kept.Text != want {
		t.Fatalf("text %q", kept.Text)
	}
	if kept.Kept != 2 {
		t.Errorf("1.4 is below the threshold; 1.5 is kept: %d", kept.Kept)
	}
	if kept.KeptChars != 3+3+1000 {
		t.Errorf("keptChars %d", kept.KeptChars)
	}
	if _, ok := SelectChunks(chunks, []float64{0, 1, 1.49, 0, 0, 3}, 1.5, 6000); ok {
		t.Error("a chunk only inside the tail does not count as passing")
	}
}

// twin: tests/filter.test.ts:80
func TestAboveMaxKeptCharsTheHighestScoresWinAndOriginalOrderIsRestored(t *testing.T) {
	chunks := make([]string, 8)
	for i := range chunks {
		chunks[i] = strings.Repeat(fmt.Sprint(i), 999) + "\n"
	}
	kept, ok := SelectChunks(chunks, []float64{2, 3, 1.5, 2.5, 0, 0, 0, 0}, 1.5, 3000)
	if !ok {
		t.Fatal("nothing selected")
	}
	// Budget 3000 - 1000 tail = 2000: chunks 1 (3) and 3 (2.5) fit, in original order.
	if kept.Kept != 2 || strings.Index(kept.Text, "111") > strings.Index(kept.Text, "333") {
		t.Errorf("kept %d", kept.Kept)
	}
	if strings.Contains(kept.Text, "000") || strings.Contains(kept.Text, "222") {
		t.Error("lower scores kept")
	}
	if kept.KeptChars > 3000 {
		t.Errorf("keptChars %d", kept.KeptChars)
	}
	all := strings.Join(chunks, "")
	if !strings.HasSuffix(kept.Text, utf16Slice(all, utf16Len(all)-1000, -1)) {
		t.Error("tail")
	}
}

// twin: tests/filter.test.ts:92
func TestBatchesStayUnderTheRequestSizeAndQuestionLimitsAndCoverEachChunkOnce(t *testing.T) {
	chunks := ChunkLines(filterLog(1200), 2000)
	batches := BuildFilterBatches(chunks, filterInput, FilterRequestBytes)
	if len(batches) < 2 {
		t.Fatal("a 50k output needs more than one request")
	}
	var covered []int
	for _, b := range batches {
		if n := requestBytes(b.State, b.Questions); n > FilterRequestBytes {
			t.Errorf("request of %d bytes", n)
		}
		if len(b.Questions) > FilterMaxQuestions {
			t.Errorf("%d questions", len(b.Questions))
		}
		if b.State["task"] != filterInput.Task || b.State["plan"] != filterInput.Plan || b.State["command"] != "npm test" {
			t.Errorf("state %v", b.State)
		}
		covered = append(covered, b.IDs...)
	}
	want := make([]int, len(chunks))
	for i := range want {
		want[i] = i
	}
	mustEqual(t, covered, want, "every chunk in exactly one request")
	raw, _ := json.Marshal(chunks)
	if limit := (len(raw)+FilterRequestBytes-1)/FilterRequestBytes + 1; len(batches) > limit {
		t.Errorf("as few requests as the limit allows: %d > %d", len(batches), limit)
	}
	q := batches[0].Questions["c1"]
	if q.Type != "score" {
		t.Errorf("type %s", q.Type)
	}
	mustEqual(t, q.Levels, FilterRubric, "rubric")
	mustMatch(t, q.Instructions, `agent's current task`, "instructions")
	// Tiny chunks hit the question limit before the size limit.
	tiny := make([]string, 40)
	for i := range tiny {
		tiny[i] = fmt.Sprintf("%d\n", i)
	}
	var counts []int
	for _, b := range BuildFilterBatches(tiny, filterInput, FilterRequestBytes) {
		counts = append(counts, len(b.IDs))
	}
	mustEqual(t, counts, []int{32, 8}, "question limit")
}

// twin: tests/filter.test.ts:113
func TestChunkTaskPlanAndCommandAreRedactedBeforeTheyLeave(t *testing.T) {
	secret := "ghp_" + "Qk7mZ2xR9vT4bN8cL1pW6sD3fH5jK0aYuE2i"
	in := filterInput
	in.Task, in.Plan, in.Command = "use "+secret, "print "+secret, "echo "+secret
	batches := BuildFilterBatches([]string{"token " + secret + "\n"}, in, FilterRequestBytes)
	raw, _ := json.Marshal(map[string]any{"s": batches[0].State, "q": batches[0].Questions})
	if strings.Contains(string(raw), secret) {
		t.Fatal("secret left")
	}
}

// twin: tests/filter.test.ts:119
func TestFilterKeepsUsefulPassagesWordForWordInOrderWithTheTailAndRunsBatchesInParallel(t *testing.T) {
	text := filterLog(1200, 100, 700)
	judge := &scoringJudge{rate: needle, delay: 100 * time.Millisecond}
	started := time.Now()
	result := FilterOutput(context.Background(), text, filterInput, FilterOptions{Config: filterConfig(), Judge: judge})
	if !result.OK {
		t.Fatalf("%+v", result)
	}
	if len(judge.sent) < 2 {
		t.Fatalf("%d requests", len(judge.sent))
	}
	if time.Since(started) >= time.Duration(len(judge.sent))*100*time.Millisecond {
		t.Error("batches run in parallel")
	}
	if result.Requests != len(judge.sent) || result.Kept != 2 {
		t.Errorf("requests %d kept %d", result.Requests, result.Kept)
	}
	mustMatch(t, result.Text, fmt.Sprintf(`^\[pi-warden: filtered; %d original characters, %d lines\. Passages selected for the current task; omitted text is in the full-output file\.\]\n\[… \d+ lines omitted …\]\n`, utf16Len(text), strings.Count(text, "\n")+1), "header")
	if strings.Index(result.Text, "line 100 NEEDLE") > strings.Index(result.Text, "line 700 NEEDLE") || strings.Index(result.Text, "line 100 NEEDLE") < 0 {
		t.Error("order")
	}
	if !strings.HasSuffix(result.Text, utf16Slice(text, utf16Len(text)-1000, -1)) {
		t.Error("the final status is always kept")
	}
	for _, line := range strings.Split(result.Text, "\n") {
		if line != "" && !strings.HasPrefix(line, "[") && !strings.Contains(text, line) {
			t.Errorf("not verbatim: %q", line)
		}
	}
	if result.KeptChars > filterConfig().MaxKeptChars {
		t.Errorf("keptChars %d", result.KeptChars)
	}
}

// twin: tests/filter.test.ts:136
func TestEveryJudgeFailureAndAnEmptySelectionFallBackWithAReason(t *testing.T) {
	text := filterLog(400, 10)
	failing := func(code string) Judge {
		return judgeFunc(func(context.Context, Request) (Evaluation, error) {
			return Evaluation{}, &IntegrationError{Code: code, Message: code}
		})
	}
	for _, c := range []struct{ code, reason string }{{"timeout", "timeout"}, {"budget", "budget"}, {"http", "error"}} {
		result := FilterOutput(context.Background(), text, filterInput, FilterOptions{Config: filterConfig(), Judge: failing(c.code)})
		if result.OK || string(result.Reason) != c.reason || result.Requests < 1 {
			t.Errorf("%s: %+v", c.code, result)
		}
	}
	none := FilterOutput(context.Background(), text, filterInput, FilterOptions{Config: filterConfig(), Judge: &scoringJudge{rate: func(string) float64 { return 1 }}})
	if none.OK || none.Reason != FallbackNonePassed {
		t.Errorf("none: %+v", none)
	}
	// One failed batch among several discards the whole attempt: partial scores would bias the selection.
	var calls atomic.Int32
	inner := &scoringJudge{rate: needle}
	flaky := judgeFunc(func(ctx context.Context, r Request) (Evaluation, error) {
		if calls.Add(1) == 2 {
			return Evaluation{}, &IntegrationError{Code: "timeout", Message: "timeout"}
		}
		return inner.Evaluate(ctx, r)
	})
	partial := FilterOutput(context.Background(), filterLog(1200, 5), filterInput, FilterOptions{Config: filterConfig(), Judge: flaky})
	if partial.OK || partial.Reason != FallbackTimeout {
		t.Errorf("partial: %+v", partial)
	}
}

// twin: tests/filter.test.ts:154
func TestFilterDeadlineIsTimeoutMs(t *testing.T) {
	hang := judgeFunc(func(ctx context.Context, r Request) (Evaluation, error) {
		<-ctx.Done()
		return Evaluation{}, ctx.Err()
	})
	cfg := filterConfig()
	cfg.TimeoutMs = 50
	started := time.Now()
	result := FilterOutput(context.Background(), filterLog(400), filterInput, FilterOptions{Config: cfg, Judge: hang})
	if result.OK || result.Reason != FallbackTimeout {
		t.Fatalf("%+v", result)
	}
	if time.Since(started) >= 2*time.Second {
		t.Error("slow")
	}
}
