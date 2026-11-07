package driver

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/journal"
	"github.com/kagent-dev/kagent/go/harness/runtime"
	runtimea2a "github.com/kagent-dev/kagent/go/harness/runtime/a2a"
	"github.com/kagent-dev/kagent/go/pkg/tracing"
)

// openCalls returns the IDs of tool calls the sink saw announced but never given a result.
func (r *recorder) openCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var open []string
	for _, c := range r.calls {
		open = append(open, c.ID)
	}
	for _, res := range r.results {
		open = slices.DeleteFunc(open, func(id string) bool { return id == res.ID })
	}
	return open
}

// resultFor returns every result the sink received for a call ID.
func (r *recorder) resultFor(id string) []runtime.ToolResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []runtime.ToolResult
	for _, res := range r.results {
		if res.ID == id {
			out = append(out, res)
		}
	}
	return out
}

func wantOneResult(t *testing.T, sink *recorder, id string, status runtime.ToolStatus, isError bool) runtime.ToolResult {
	t.Helper()
	got := sink.resultFor(id)
	if len(got) != 1 || got[0].Status != status || got[0].IsError != isError {
		t.Fatalf("results for %s = %+v, want exactly one with status %q isError=%v", id, got, status, isError)
	}
	return got[0]
}

func TestParkedBatchDeclinesTheAnnouncedSiblingsAndLeavesTheGatedCallOpen(t *testing.T) {
	h := newHarness(t, "approve-batch-started")
	pending, sink := park(t, h, "approve-batch-started")
	request := h.approvalRequest(runtime.Outcome{Pending: pending})
	if request.CallID != "c2" {
		t.Fatalf("call = %s", request.CallID)
	}
	res := wantOneResult(t, sink, "c1", runtime.ToolDeclined, true)
	if res.Name != "bash" || !strings.Contains(res.Result.(string), "Not executed") {
		t.Fatalf("sibling result = %+v", res)
	}
	if got := sink.openCalls(); !slices.Equal(got, []string{"c2"}) {
		t.Fatalf("open after park = %v; only the gated call stays open for the approval UI", got)
	}
	if len(sink.resultFor("c3")) != 0 || slices.ContainsFunc(sink.calls, func(c runtime.ToolCall) bool { return c.ID == "c3" }) {
		t.Fatal("c3 was never announced by pig and must not appear")
	}
	// the continuation closes the gated call with its real result
	h.setMode("resume")
	cont := &recorder{}
	if _, err := pending.Resume(t.Context(), approve(request), cont); err != nil {
		t.Fatal(err)
	}
	wantOneResult(t, cont, "c2", runtime.ToolRan, false)
	if len(cont.resultFor("c1")) != 0 {
		t.Fatal("the siblings were closed again by the continuation")
	}
}

func TestParkedBatchGatedCallRejectedIsDeclinedAtContinuation(t *testing.T) {
	h := newHarness(t, "approve-batch-started")
	pending, _ := park(t, h, "approve-batch-started")
	request := h.approvalRequest(runtime.Outcome{Pending: pending})
	h.setMode("resume")
	cont := &recorder{}
	if _, err := pending.Resume(t.Context(), &runtime.ApprovalDecision{ID: request.ID}, cont); err != nil {
		t.Fatal(err)
	}
	wantOneResult(t, cont, "c2", runtime.ToolDeclined, true)
}

func runUntilHanging(t *testing.T, h *harness, mode string, sink *recorder) (cancel context.CancelFunc, done chan error) {
	t.Helper()
	h.setMode(mode)
	ctx, cancel := context.WithCancel(t.Context())
	done = make(chan error, 1)
	go func() {
		_, err := h.d.Run(ctx, h.turn("x"), sink)
		done <- err
	}()
	eventually(t, "pig to be mid-turn", func() bool { return len(h.logs("hanging")) > 0 })
	return cancel, done
}

func waitCanceled(t *testing.T, done chan error) {
	t.Helper()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestCancelMidToolClosesTheOpenCallAsUnfinished(t *testing.T) {
	h := newHarness(t, "hang-tool")
	sink := &recorder{}
	cancel, done := runUntilHanging(t, h, "hang-tool", sink)
	if got := sink.openCalls(); !slices.Equal(got, []string{"call_bash"}) {
		t.Fatalf("open before cancel = %v", got)
	}
	cancel()
	waitCanceled(t, done)
	res := wantOneResult(t, sink, "call_bash", runtime.ToolUnfinished, true)
	if res.Name != "bash" {
		t.Fatalf("name = %q", res.Name)
	}
	h.assertReaped()
}

func TestAProcessCrashMidToolAlsoClosesTheOpenCall(t *testing.T) {
	h := newHarness(t, "hang-tool")
	sink := &recorder{}
	cancel, done := runUntilHanging(t, h, "hang-tool", sink)
	defer cancel()
	// the run fails by cancel here too; the point is that no exit path leaves the call open
	cancel()
	waitCanceled(t, done)
	if got := sink.openCalls(); len(got) != 0 {
		t.Fatalf("open after the turn ended = %v", got)
	}
}

// wantNothingEmitted checks that Cancel left the sink untouched.
func wantNothingEmitted(t *testing.T, sink *recorder) {
	t.Helper()
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.events) != 0 {
		t.Fatalf("PendingTurn.Cancel emitted %q; kagent hands it a bare sink where a tool result panics", sink.events)
	}
}

func TestCancelWhileParkedEmitsNothing(t *testing.T) {
	h := newHarness(t, "approve-batch-started")
	pending, _ := park(t, h, "approve-batch-started")
	sink := &recorder{} // implements EventSink and ToolExecutionSink: the assertion Cancel used to make would succeed
	if err := pending.Cancel(t.Context(), sink); err != nil {
		t.Fatal(err)
	}
	wantNothingEmitted(t, sink)
	if r := record(t, h); r.Phase != journal.Canceled {
		t.Fatalf("phase = %s; the record is still tombstoned", r.Phase)
	}
}

func TestCancelAfterTheResultWasRecordedEmitsNothingButStillInjectsTheResult(t *testing.T) {
	h := newHarness(t, "approve")
	pending, _ := park(t, h, "approve")
	request := h.approvalRequest(runtime.Outcome{Pending: pending})
	h.setMode("resume")
	crashAt(h, "result", func() { pending.Resume(t.Context(), approve(request), &recorder{}) })
	_, _, restored, err := h.restart("resume").RestorePending()
	if err != nil || restored == nil {
		t.Fatal(err)
	}
	sink := &recorder{}
	if err := restored.Cancel(t.Context(), sink); err != nil {
		t.Fatal(err)
	}
	wantNothingEmitted(t, sink)
	if results := toolResults(h); len(results) != 1 || resultText(results[0]) != "probe:basket" {
		t.Fatalf("session results = %v", results)
	}
}

func TestCancelMidResumeNeverClosesTheGatedCallTwice(t *testing.T) {
	h := newHarness(t, "approve")
	pending, _ := park(t, h, "approve")
	request := h.approvalRequest(runtime.Outcome{Pending: pending})
	h.setMode("hang")
	ctx, cancel := context.WithCancel(t.Context())
	sink := &recorder{}
	done := make(chan error, 1)
	go func() {
		_, err := pending.Resume(ctx, approve(request), sink)
		done <- err
	}()
	eventually(t, "continuation to hang", func() bool { return len(h.logs("hanging")) > 0 })
	cancel()
	waitCanceled(t, done)
	// the continuation was accepted, so the gated call has its real result and the cancel adds nothing
	wantOneResult(t, sink, fakeCallProbe, runtime.ToolRan, false)
}

func TestCancelWhileTheApprovedCallIsInFlightClosesItAsUnfinished(t *testing.T) {
	h := newHarness(t, "approve")
	pending, _ := park(t, h, "approve")
	request := h.approvalRequest(runtime.Outcome{Pending: pending})
	h.mcp.hold = true
	h.setMode("resume")
	ctx, cancel := context.WithCancel(t.Context())
	sink := &recorder{}
	done := make(chan error, 1)
	go func() {
		_, err := pending.Resume(ctx, approve(request), sink)
		done <- err
	}()
	<-h.mcp.arrived
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, runtime.ErrSessionAbandoned) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Resume did not return")
	}
	wantOneResult(t, sink, fakeCallProbe, runtime.ToolUnfinished, true)
}

func TestAbandonedContinuationClosesTheGatedCallAsUnfinished(t *testing.T) {
	h := newHarness(t, "approve")
	pending, _ := park(t, h, "approve")
	request := h.approvalRequest(runtime.Outcome{Pending: pending})
	h.setMode("crash-before-accept") // pig dies before it accepts the continuation prompt
	sink := &recorder{}
	if _, err := pending.Resume(t.Context(), approve(request), sink); !errors.Is(err, runtime.ErrSessionAbandoned) {
		t.Fatalf("err = %v", err)
	}
	wantOneResult(t, sink, fakeCallProbe, runtime.ToolUnfinished, true)
}

func TestAPromptPigRefusesStillClosesTheGatedCall(t *testing.T) {
	h := newHarness(t, "approve")
	pending, _ := park(t, h, "approve")
	request := h.approvalRequest(runtime.Outcome{Pending: pending})
	h.setMode("reject")
	sink := &recorder{}
	out, err := pending.Resume(t.Context(), approve(request), sink)
	if err != nil || out.Failure == nil {
		t.Fatalf("Resume = %+v, %v", out, err)
	}
	wantOneResult(t, sink, fakeCallProbe, runtime.ToolRan, false)
}

func TestCancelOfAnAbandonedHandleEmitsNothing(t *testing.T) {
	h := newHarness(t, "approve")
	pending, _ := park(t, h, "approve")
	r := record(t, h)
	r.Phase = journal.Abandoned
	if err := h.d.journal.Save(r); err != nil {
		t.Fatal(err)
	}
	if _, _, p, err := h.d.RestorePending(); p != nil || !errors.Is(err, runtime.ErrSessionAbandoned) {
		t.Fatalf("RestorePending = %v, %v", p, err)
	}
	sink := &recorder{}
	if err := pending.Cancel(t.Context(), sink); !errors.Is(err, runtime.ErrSessionAbandoned) {
		t.Fatalf("Cancel = %v", err)
	}
	wantNothingEmitted(t, sink)
}

// TestExecutorCancelsARestoredParkedHandle is kagent's TestCancelParkedTurn with the adapter's own handle: the
// executor cancels a parked task with a bare sink that has no request context, so a handle that emits a tool result
// there panics on a nil ExecutorContext.
func TestExecutorCancelsARestoredParkedHandle(t *testing.T) {
	phases := map[string]func(t *testing.T, h *harness, pending runtime.PendingTurn, request *runtime.ApprovalRequest){
		"waiting": func(*testing.T, *harness, runtime.PendingTurn, *runtime.ApprovalRequest) {},
		"result": func(t *testing.T, h *harness, pending runtime.PendingTurn, request *runtime.ApprovalRequest) {
			h.setMode("resume")
			crashAt(h, "result", func() { pending.Resume(t.Context(), approve(request), &recorder{}) })
		},
		"abandoned": func(t *testing.T, h *harness, _ runtime.PendingTurn, _ *runtime.ApprovalRequest) {
			r := record(t, h)
			r.Phase = journal.Abandoned
			if err := h.d.journal.Save(r); err != nil {
				t.Fatal(err)
			}
		},
	}
	for phase, prepare := range phases {
		t.Run(phase, func(t *testing.T) {
			h := newHarness(t, "approve")
			pending, _ := park(t, h, "approve")
			prepare(t, h, pending, h.approvalRequest(runtime.Outcome{Pending: pending}))
			// Substrate restores /data into a new process: a new driver and executor find the parked task.
			executor, err := runtimea2a.New(h.restart("resume"), &memoryContinuation{}, tracing.RuntimeTelemetry{})
			if err != nil {
				t.Fatal(err)
			}
			message := a2atype.NewMessage(a2atype.MessageRoleUser, a2atype.NewTextPart("cancel"))
			message.TaskID, message.ContextID = "task-1", "ctx-1"
			events, errs := collectEvents(executor.Cancel(t.Context(), &a2asrv.ExecutorContext{TaskID: "task-1", ContextID: "ctx-1", Message: message}))
			if phase == "abandoned" {
				// RestorePending reports the session as abandoned and there is no handle to cancel.
				if len(errs) != 1 || !errors.Is(errs[0], runtime.ErrSessionAbandoned) {
					t.Fatalf("events = %v errs = %v", events, errs)
				}
				return
			}
			if len(errs) != 0 || finalState(events) != a2atype.TaskStateCanceled {
				t.Fatalf("events = %v errs = %v, want a canceled task", events, errs)
			}
			if r := record(t, h); r.Phase != journal.Canceled {
				t.Fatalf("phase = %s", r.Phase)
			}
		})
	}
}

func TestUsageThatArrivesAfterTheCancelGraceIsStillReported(t *testing.T) {
	h := newHarness(t, "late-usage")
	h.d.cfg.Grace = 150 * time.Millisecond
	sink := &recorder{}
	cancel, done := runUntilHanging(t, h, "late-usage", sink)
	cancel()
	waitCanceled(t, done)
	sink.mu.Lock()
	models := slices.Clone(sink.models)
	sink.mu.Unlock()
	if len(models) != 1 || !models[0].Canceled || models[0].InputTokens != 9 {
		t.Fatalf("model calls = %+v; usage written while pig shuts down must reach the sink", models)
	}
	h.assertReaped()
}

func TestRejectionTextFollowsKagentsWording(t *testing.T) {
	for reason, want := range map[string]string{
		"":               "Tool call was rejected by user.",
		"  \n\t":         "Tool call was rejected by user.",
		"too risky":      "Tool call was rejected by user. Reason: too risky",
		"  too risky \n": "Tool call was rejected by user. Reason: too risky",
	} {
		if got := RejectionText(reason); got != want {
			t.Errorf("RejectionText(%q) = %q, want %q", reason, got, want)
		}
	}
}

func TestAConflictingDecisionIsAValidationErrorAndDoesNotAbandon(t *testing.T) {
	h := newHarness(t, "approve")
	pending, _ := park(t, h, "approve")
	request := h.approvalRequest(runtime.Outcome{Pending: pending})
	h.setMode("resume")
	crashAt(h, "result", func() { pending.Resume(t.Context(), approve(request), &recorder{}) })
	_, _, restored, err := h.restart("resume").RestorePending()
	if err != nil || restored == nil {
		t.Fatal(err)
	}
	sink := &recorder{}
	_, err = restored.Resume(t.Context(), &runtime.ApprovalDecision{ID: request.ID, Approved: false, RejectionReason: "changed my mind"}, sink)
	var invalid *validationError
	if !errors.As(err, &invalid) || errors.Is(err, runtime.ErrSessionAbandoned) {
		t.Fatalf("err = %v, want a validation error that is not an abandon", err)
	}
	if r := record(t, h); r.Phase != journal.Result {
		t.Fatalf("phase = %s; a refused response must leave the record alone", r.Phase)
	}
	if len(sink.results) != 0 {
		t.Fatalf("a refused response closed a tool artifact: %+v", sink.results)
	}
	// the same decision as recorded still resumes
	if _, err := restored.Resume(t.Context(), approve(request), &recorder{}); err != nil {
		t.Fatalf("matching decision after the refusal: %v", err)
	}
}

func TestSystemDirectoriesLeadThePathOfPigAndItsTools(t *testing.T) {
	data := "/data"
	got := systemFirstPath("/data/.local/bin:/data/go/bin:/opt/tool/bin::relative/bin:/usr/bin:/data/other/../.local/bin", data)
	want := "/usr/local/bin:/usr/bin:/bin:/usr/local/sbin:/usr/sbin:/sbin:/opt/tool/bin:/data/.local/bin:/data/go/bin"
	if got != want {
		t.Fatalf("PATH = %q, want %q", got, want)
	}
	if got := systemFirstPath("", data); got != "/usr/local/bin:/usr/bin:/bin:/usr/local/sbin:/usr/sbin:/sbin" {
		t.Fatalf("PATH without an inherited one = %q", got)
	}
	// "/database" is not under "/data"
	if got := systemFirstPath("/database/bin", data); !strings.HasPrefix(got, "/usr/local/bin") || !strings.HasSuffix(got, ":/database/bin") {
		t.Fatalf("PATH = %q", got)
	}
}

func TestPigChildPathPutsTheDataDirectoriesLast(t *testing.T) {
	h := newHarness(t, "text")
	h.extra = []string{"PATH=" + h.data + "/.local/bin:" + h.data + "/go/bin:/usr/bin:/bin"}
	h.setMode("text")
	mustRun(t, h, h.turn("x"), &recorder{})
	var path string
	for _, e := range toStrings(h.logs("start")[0]["env"]) {
		if v, ok := strings.CutPrefix(e, "PATH="); ok {
			path = v
		}
	}
	want := "/usr/local/bin:/usr/bin:/bin:/usr/local/sbin:/usr/sbin:/sbin:" + h.data + "/.local/bin:" + h.data + "/go/bin"
	if path != want {
		t.Fatalf("pig's PATH = %q, want %q", path, want)
	}
}
