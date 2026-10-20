package warden

import (
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// auditState is the /warden audit running flag the input event reads (extension.ts auditRunning).
type auditState struct{ running atomic.Bool }

// auditReport mirrors extension.ts `report`: a notification with a UI, a visible message without.
func (w *warden) auditReport(ctx sdk.Context, text, level string) {
	if ctx.HasUI() {
		ctx.Notify(text, level)
		return
	}
	w.show(ctx, text)
}

// audit is `/warden audit`: it asks the session model to audit the workspace and writes nothing itself; the outcome is
// read off the report file the agent was told to write (extension.ts 2896-2925).
func (w *warden) audit(ctx sdk.Context) error {
	if !ctx.HasUI() {
		w.auditReport(ctx, "Audit needs an interactive session to run.", "warning")
		return nil
	}
	ok, err := ctx.Confirm("Run workspace audit?", "This runs an agent-driven audit of "+Redact(ctx.Cwd())+". It uses the session model, reads source code, and writes a report. It may take several minutes and use real tokens. Measured comparisons need the agent's TypeSafe tool, which is enabled per session with `/typesafe enable`; without it findings will be marked unmeasured.")
	if err != nil || !ok {
		return err
	}
	cwd := ctx.Cwd()
	prompt := BuildAuditPrompt(cwd, FindProjects(cwd))
	reportPath := AuditReportPath(cwd)
	before := SnapshotReport(reportPath)
	w.auditState.running.Store(true)
	ctx.Notify("pi-warden: Running workspace audit...", "info")
	func() {
		defer w.auditState.running.Store(false)
		for attempt := 0; attempt < 40; attempt++ {
			if idle, err := ctx.IsIdle(); err != nil || idle {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		err := ctx.SendUserMessage(prompt, "")
		if err == nil {
			err = ctx.WaitForIdle()
		}
		if err != nil {
			w.auditReport(ctx, "pi-warden audit failed: "+err.Error()+".", "error")
		}
	}()
	switch ReportOutcome(before, SnapshotReport(reportPath)) {
	case "written":
		w.auditReport(ctx, "Audit report written to "+reportPath, "info")
	case "stale":
		w.auditReport(ctx, "Audit finished but the report file was not updated — the agent may have reported findings in chat instead.", "info")
	case "missing":
		w.auditReport(ctx, "Agent did not write an audit report. The model may have reported findings in chat instead.", "info")
	}
	return nil
}

// onAuditInput blocks new user messages while /warden audit runs. The audit's own prompt (source "extension") passes.
func (w *warden) onAuditInput(ctx sdk.Context, data map[string]any) (any, error) {
	if data["source"] == "extension" || !w.auditState.running.Load() {
		return nil, nil
	}
	ctx.Notify("pi-warden is running an audit. Please wait...", "warning")
	return map[string]any{"action": "handled"}, nil
}
