package tintinweb_subagents

import (
	"path/filepath"
	"strings"
	"testing"
)

// Not twins: the original has no refusals. A feature the port does not do (a worktree, a schema, a resume of a
// finished session, an unported agent-file field) must fail the whole run with the refusal text, through the real
// extension, the real adapter and the fake child fleet. A script's try/catch must not turn it into a result.

// wfRefusalRun runs script (after the meta header) in the background and returns the notification details.
func wfRefusalRun(t *testing.T, w *wfRig, script string, children func()) obj {
	t.Helper()
	taskID := w.taskID(w.run(obj{"script": inlineScript + script}))
	if children != nil {
		children()
	}
	sent := w.notification(taskID)
	return sent["message"].(map[string]any)
}

func wfWantRefused(t *testing.T, msg obj, feature string) {
	t.Helper()
	details := msg["details"].(map[string]any)
	eq(t, details["status"], "error")
	preview := asString(details["resultPreview"])
	eq(t, strings.Contains(preview, refusalText(feature)), true)
	eq(t, strings.Contains(asString(msg["content"]), refusalText(feature)), true)
}

func TestWorkflowRefusals(t *testing.T) {
	t.Run("isolation worktree fails the run with the refusal text", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		w.start()
		msg := wfRefusalRun(t, w, `const r = await agent("p", { isolation: "worktree" }); return "got:" + r;`, nil)
		wfWantRefused(t, msg, `worktree isolation (isolation: "worktree")`)
		eq(t, w.fleet.count(), 0)
	})
	t.Run("a schema fails the run with the refusal text", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		w.start()
		msg := wfRefusalRun(t, w, `const r = await agent("p", { schema: { type: "object" } }); return "got:" + r;`, nil)
		wfWantRefused(t, msg, "structured output (agent({ schema }))")
		eq(t, w.fleet.count(), 0)
	})
	t.Run("resuming a finished agent fails the run with the refusal text", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		w.start()
		msg := wfRefusalRun(t, w, `await agent("first", { label: "one" }); const r = await agent("again", { resume: "one" }); return "got:" + r;`, func() {
			w.fleet.wait(t, 1)[0].finish("first done")
		})
		wfWantRefused(t, msg, "resuming a finished agent's session (resume)")
	})
	t.Run("an unported agent-file feature fails the run with the refusal text", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		writeFile(t, filepath.Join(w.dir, ".pi", "agents", "wt.md"), "---\ndescription: Worktree agent\nisolation: worktree\n---\nIsolated.")
		w.start()
		msg := wfRefusalRun(t, w, `const r = await agent("p", { agentType: "wt" }); return "got:" + r;`, nil)
		wfWantRefused(t, msg, "worktree isolation (isolation: worktree in the agent file)")
		eq(t, w.fleet.count(), 0)
	})
	t.Run("a try/catch in the script does not swallow it", func(t *testing.T) {
		w := startWfRig(t, wfRigOptions{})
		w.start()
		msg := wfRefusalRun(t, w, `try { await agent("p", { isolation: "worktree" }); } catch (e) { return "swallowed"; } return "went on";`, nil)
		wfWantRefused(t, msg, `worktree isolation (isolation: "worktree")`)
		eq(t, strings.Contains(asString(msg["content"]), "swallowed"), false)
		eq(t, strings.Contains(asString(msg["content"]), "went on"), false)
	})
}
