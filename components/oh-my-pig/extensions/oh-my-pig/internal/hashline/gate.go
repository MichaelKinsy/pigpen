package hashline

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
)

// CheckStagedGate enforces the installed gate.Policy across every staged file
// (and every move destination) before any write takes place.
// One refusal rejects the entire multi-file edit atomically.
// upstream: crates/edit/src/session.rs:352, crates/edit/src/path_policy.rs:176
func CheckStagedGate(ctx sdk.Context, staged []StagedFile, urlMap map[string]string) *EditError {
	for i := range staged {
		item := &staged[i]
		if item.Op == EngineFileOpNoop {
			continue
		}
		var reqURL string
		if urlMap != nil {
			reqURL = urlMap[item.Canonical]
		}
		req := gate.Request{
			Tool: "edit",
			Path: item.Absolute,
			URL:  reqURL,
		}
		switch {
		case item.Op == EngineFileOpDelete:
			req.Op = gate.OpDelete
		case item.MoveTo != nil:
			req.Op = gate.OpMove
			req.Dest = item.MoveTo.Absolute
		default:
			req.Op = gate.OpEdit
		}
		if err := gate.Check(ctx, req); err != nil {
			return PlanError(err.Error())
		}
		if item.MoveTo != nil {
			destCanonical := CanonicalPathKey(item.MoveTo.Absolute)
			var destURL string
			if urlMap != nil {
				destURL = urlMap[destCanonical]
			}
			if err := gate.Check(ctx, gate.Request{
				Tool: "edit",
				Op:   gate.OpWrite,
				Path: item.MoveTo.Absolute,
				URL:  destURL,
			}); err != nil {
				return PlanError(err.Error())
			}
		}
	}
	return nil
}
