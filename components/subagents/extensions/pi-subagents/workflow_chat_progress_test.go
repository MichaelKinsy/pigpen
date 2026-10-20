package pi_subagents

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of test/unit/workflow-chat-progress.test.ts.

func cpGit(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", cwd}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func cpRepo(t *testing.T, prefix string) string {
	t.Helper()
	repo := tmp(t)
	cpGit(t, repo, "init")
	cpGit(t, repo, "config", "user.email", "tests@example.com")
	cpGit(t, repo, "config", "user.name", "Workflow Progress Tests")
	writeFile(t, filepath.Join(repo, "tracked.txt"), "initial\n")
	cpGit(t, repo, "add", "-A")
	cpGit(t, repo, "commit", "-m", "initial")
	return repo
}

func TestWorkflowChatProgressTwins(t *testing.T) {
	const f = "workflow-chat-progress"
	mode := func(p *WorkflowChatProgressProjection) string {
		if p == nil {
			return "<none>"
		}
		return p.Mode
	}
	tw(t, f, "treats managed worktrees as same repo and sibling repos as other repo", func(t *testing.T) {
		repo := cpRepo(t, "pi-workflow-progress-repo-")
		other := cpRepo(t, "pi-workflow-progress-other-")
		worktree := filepath.Join(tmp(t), "wt")
		if err := os.Mkdir(filepath.Join(repo, "packages"), 0o755); err != nil {
			t.Fatal(err)
		}
		cpGit(t, repo, "worktree", "add", "-b", "chat-progress-test", worktree, "HEAD")
		t.Cleanup(func() { _ = exec.Command("git", "-C", repo, "worktree", "remove", "--force", worktree).Run() })
		eq(t, isSameGitRepository(repo, filepath.Join(repo, "packages")), true, "subdirectory")
		eq(t, isSameGitRepository(repo, worktree), true, "worktree")
		eq(t, isSameGitRepository(repo, other), false, "sibling")

		p, _ := resolveWorkflowChatProgress(WorkflowChatProgressInput{Requested: "auto", ParentCwd: repo, WorkflowCwd: worktree})
		eq(t, mode(p), "live-card", "auto, same repo")
		p, _ = resolveWorkflowChatProgress(WorkflowChatProgressInput{Requested: "auto", ParentCwd: repo, WorkflowCwd: worktree, Background: true})
		eq(t, mode(p), "off", "auto, background")
		p, _ = resolveWorkflowChatProgress(WorkflowChatProgressInput{Requested: "auto", ParentCwd: repo, WorkflowCwd: other})
		eq(t, mode(p), "off", "auto, other repo")
		_, msg := resolveWorkflowChatProgress(WorkflowChatProgressInput{Requested: "live-card", ParentCwd: repo, WorkflowCwd: other})
		mustMatch(t, `(?i)same Git repository`, msg)
		_, msg = resolveWorkflowChatProgress(WorkflowChatProgressInput{Requested: "live-card", ParentCwd: repo, WorkflowCwd: repo, Background: true})
		mustMatch(t, `(?i)omit chatProgress or use auto/off.*async:false only when the parent must block`, msg)
		_, msg = resolveWorkflowChatProgress(WorkflowChatProgressInput{Requested: "terminal", ParentCwd: repo, WorkflowCwd: repo})
		mustMatch(t, `(?i)one of: auto, off, live-card`, msg)
		p, _ = resolveWorkflowChatProgress(WorkflowChatProgressInput{ParentCwd: repo, WorkflowCwd: repo})
		eq(t, p.RepoRelation+" "+p.RepoLabel, "same "+filepath.Base(repo), "an omitted request is auto, and labels the repository")
	})
	tw(t, f, "annotates an authoritative dotted child without adding its declared root", func(t *testing.T) {
		pre := &WorkflowPreflight{Version: 1, Coverage: "partial", Lanes: []WorkflowPreflightLane{{Key: "pr14", Mode: "review"}}}
		rows := buildWorkflowChatProgressRows([]WorkflowScriptTraceEntry{{Operation: "run", Key: "pr14.quality", GeneratedLaneKey: "pr14", State: "started", Agent: "reviewer"}}, pre)
		var got [][3]string
		for _, r := range rows {
			mode := ""
			if r.Preflight != nil {
				mode = r.Preflight.Mode
			}
			got = append(got, [3]string{r.Key, r.State, mode})
		}
		eq(t, got, [][3]string{{"pr14.quality", "running", "review"}}, "rows")
	})
	tw(t, f, "prefers a specific preflight lane over an earlier broad generated alias", func(t *testing.T) {
		pre := &WorkflowPreflight{Version: 1, Coverage: "partial", Lanes: []WorkflowPreflightLane{{Key: "writer", Mode: "mutation"}, {Key: "writer.quality", Mode: "review"}}}
		rows := buildWorkflowChatProgressRows([]WorkflowScriptTraceEntry{{Operation: "run", Key: "writer.quality.deep", GeneratedLaneKey: "writer", State: "started", Agent: "reviewer"}}, pre)
		eq(t, rows[0].Preflight.Mode, "review", "mode")
	})
	tw(t, f, "suppresses only successful routine child result intercom for live-card workflows", func(t *testing.T) {
		completed := jo("agent", "delegate", "exitCode", float64(0), "outputState", "present")
		failed := jo("agent", "delegate", "exitCode", float64(1), "outputState", "present")
		rejected := jo("agent", "delegate", "exitCode", float64(0), "acceptance", jo("status", "rejected"), "outputState", "present")
		eq(t, shouldSuppressRoutineResultIntercom(true, []*jsObject{completed}), true, "completed")
		eq(t, shouldSuppressRoutineResultIntercom(true, []*jsObject{failed}), false, "failed")
		eq(t, shouldSuppressRoutineResultIntercom(true, []*jsObject{rejected}), false, "rejected")
		eq(t, shouldSuppressRoutineResultIntercom(false, []*jsObject{completed}), false, "not asked")
		eq(t, shouldSuppressRoutineResultIntercom(true, nil), false, "no results")
	})
	tw(t, f, "marks acceptance-rejected foreground intercom results as failed", func(t *testing.T) {
		eq(t, foregroundResultIntercomStatus(jo("agent", "delegate", "exitCode", float64(0), "acceptance", jo("status", "rejected"), "outputState", "present")), "failed", "status")
	})
	tw(t, f, "keeps async workflow launch receipts running in the mission ledger", func(t *testing.T) {
		eq(t, missionWorkflowChildStatus(false, nil, "run-1", "/tmp/run-1"), "running", "async launch receipt")
		eq(t, missionWorkflowChildStatus(false, []*jsObject{jo("exitCode", float64(0))}, "run-1", ""), "completed", "finished")
		eq(t, missionWorkflowChildStatus(false, []*jsObject{jo("exitCode", float64(0), "detached", true)}, "", ""), "paused", "detached")
		eq(t, missionWorkflowChildStatus(true, []*jsObject{jo("exitCode", float64(0))}, "", ""), "failed", "error")
	})
	const render = "the terminal rendering of the workflow card is the terminal-views slice's (tui/render.ts renderSubagentResult, a TUI component tree); the row logic it renders from is covered by TestWorkflowChatProgressRows"
	tskip(t, f, "keeps advisory preflight lanes in plan metadata instead of runtime rows", render)
	tskip(t, f, "renders detached workflow trace rows as paused attention", render)
	tskip(t, f, "renders stable rows keyed by workflow trace entries", render)
	tskip(t, f, "uses the compact plan preview for collapsed workflow launch output", render)
	tskip(t, f, "keeps each expanded preflight warning visible as a bounded row", render)
	tskip(t, f, "keeps mixed detached and failed workflow traces failed", render)
	tskip(t, f, "applies main-window density settings to collapsed workflow live cards", render)
	tskip(t, f, "bounds workflow live-card rows and keeps old failed children visible", render)
	tskip(t, f, "keeps mixed workflow child error output visible", render)
	tskip(t, f, "shows final workflow output after live-card progress completes", render)
}

// The row logic behind the rendering twins that are skipped above.
func TestWorkflowChatProgressRows(t *testing.T) {
	pre := &WorkflowPreflight{Version: 1, Coverage: "partial", Lanes: []WorkflowPreflightLane{{Key: "writer", Mode: "mutation", Claims: []string{"src/workflows"}}, {Key: "review", Mode: "review", ExpectedOutput: "review.md"}}}
	eq(t, len(buildWorkflowChatProgressRows(nil, pre)), 0, "advisory lanes are not rows")
	eq(t, strings.Contains(FormatWorkflowPreflightPlanSummary(pre, ""), "Plan: 2 lanes · writer, review"), true, "plan summary")
	trace := []WorkflowScriptTraceEntry{
		{Operation: "run", Key: "scout", State: "completed", RunID: "run-scout", Phase: "Validation", Label: "Found renderer seam"},
		{Operation: "run", Key: "tests", State: "started", Phase: "Validation", Label: "focused integration suite"},
		{Operation: "run", Key: "review", State: "failed", Phase: "Validation", Label: "fresh-context UX review", Error: "needs fixes"},
		{Operation: "run", Key: "stale", State: "stopped", Phase: "Validation", Label: "superseded exact-head review", Error: "Workflow stopped by user."},
		{Operation: "run", Key: "detaches", State: "detached", RunID: "child-detached-123456"},
		{Operation: "run", Key: "tests", State: "reused", Label: " renamed "},
		{Operation: "host", Key: "ci", State: "started"},
	}
	var got [][2]string
	for _, r := range buildWorkflowChatProgressRows(trace, nil) {
		got = append(got, [2]string{r.Key, r.State})
	}
	eq(t, got, [][2]string{{"scout", "complete"}, {"tests", "running"}, {"review", "failed"}, {"stale", "stopped"}, {"detaches", "detached"}}, "states in launch order")
	rows := buildWorkflowChatProgressRows(trace, nil)
	eq(t, rows[1].Label, "renamed", "a reused entry renames the row")
}
