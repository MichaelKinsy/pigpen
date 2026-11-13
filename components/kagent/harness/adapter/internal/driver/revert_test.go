//go:build unix

package driver

import (
	"os"
	"testing"

	"github.com/kagent-dev/kagent/go/harness/runtime"

	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/journal"
)

// snapshotData copies the durable directory, as a Substrate VOLUMES snapshot taken at suspend does.
func snapshotData(t *testing.T, h *harness) string {
	t.Helper()
	snapshot := t.TempDir() + "/snapshot"
	if err := os.MkdirAll(h.data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(snapshot, os.DirFS(h.data)); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// revertData puts the durable directory back to a snapshot and starts a new driver over it. This is what kagent's
// SessionService.RevertSession leaves behind for a crashed Actor: the same Actor, /data as of its last durable
// snapshot, every process gone, nothing replayed.
func revertData(t *testing.T, h *harness, snapshot, mode string) *Driver {
	t.Helper()
	if err := os.RemoveAll(h.data); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(h.data, os.DirFS(snapshot)); err != nil {
		t.Fatal(err)
	}
	return h.restart(mode)
}

// A session that was parked at a suspend keeps its pending approval across a crash and a revert: the work done after
// the snapshot is gone, including a result recorded for the approved call, and the approval is asked for again.
func TestRevertedActorRestoresTheParkedApprovalTheSnapshotHolds(t *testing.T) {
	for _, point := range []string{"executing", "called", "result", "injected", "continuing"} {
		t.Run("crash after "+point, func(t *testing.T) {
			h := newHarness(t, "approve")
			pending, _ := park(t, h, "approve")
			request := h.approvalRequest(runtime.Outcome{Pending: pending})
			snapshot := snapshotData(t, h)
			before := h.sessionRows()

			h.setMode("resume")
			if !crashAt(h, point, func() { pending.Resume(t.Context(), approve(request), &recorder{}) }) {
				t.Fatalf("the driver never reached %q", point)
			}
			wantCalls := map[string]int{"executing": 0, "called": 1, "result": 1, "injected": 1, "continuing": 1}[point]
			if h.mcp.callCount() != wantCalls {
				t.Fatalf("MCP calls at the crash = %d, want %d", h.mcp.callCount(), wantCalls)
			}

			d := revertData(t, h, snapshot, "resume")
			if r := record(t, h); r.Phase != journal.Waiting || r.Decision != nil || r.Result != nil {
				t.Fatalf("reverted record = %+v", r)
			}
			if after := h.sessionRows(); len(after) != len(before) {
				t.Fatalf("the reverted session has %d entries, the snapshot had %d", len(after), len(before))
			}
			task, ctxID, restored, err := d.RestorePending()
			if err != nil || restored == nil || task != "task-1" || ctxID != "ctx-1" {
				t.Fatalf("RestorePending after the revert = %q %q %v, %v", task, ctxID, restored, err)
			}
			if got := restored.Request().(*runtime.ApprovalRequest); got.ID != request.ID {
				t.Fatalf("restored request = %+v, want %s", got, request.ID)
			}
			sink := &recorder{}
			out, err := restored.Resume(t.Context(), approve(request), sink)
			if err != nil || out.Pending != nil || out.Failure != nil {
				t.Fatalf("Resume after the revert = %+v, %v", out, err)
			}
			// A revert restores no remote side effect: a call made before the crash stays made, and the approval that
			// the snapshot still holds is the only thing that runs the call again.
			if h.mcp.callCount() != wantCalls+1 {
				t.Fatalf("MCP calls after the resumed approval = %d, want %d", h.mcp.callCount(), wantCalls+1)
			}
			if results := toolResults(h); len(results) != 1 || resultText(results[0]) != "probe:basket" {
				t.Fatalf("tool results = %v", results)
			}
			if r := record(t, h); r.Phase != journal.Complete {
				t.Fatalf("phase = %s", r.Phase)
			}
			if sink.text() != "hello world" {
				t.Fatalf("answer = %q", sink.text())
			}
		})
	}
}

// A runtime without a durable snapshot returns to its initial state: nothing is pending and a new turn starts a
// new session.
func TestRevertToTheInitialStateLeavesNothingPendingAndTakesNewWork(t *testing.T) {
	h := newHarness(t, "approve")
	snapshot := snapshotData(t, h)
	park(t, h, "approve")
	if record(t, h).Phase != journal.Waiting {
		t.Fatal("the turn did not park")
	}

	d := revertData(t, h, snapshot, "approve")
	task, _, restored, err := d.RestorePending()
	if task != "" || restored != nil || err != nil {
		t.Fatalf("RestorePending on the initial state = %q %v, %v", task, restored, err)
	}
	if r, err := h.d.journal.Load(); r != nil || err != nil {
		t.Fatalf("journal = %+v, %v", r, err)
	}
	pending, _ := park(t, h, "approve")
	if pending == nil || len(h.sessionRows()) == 0 {
		t.Fatal("the reverted Actor did not take a new turn")
	}
}
