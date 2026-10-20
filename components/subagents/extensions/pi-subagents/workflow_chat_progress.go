package pi_subagents

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Port of src/workflows/chat-progress.ts: whether a workflow run shows a live card in the chat (the repository policy) and the
// rows such a card is built from.

var workflowChatProgressModes = []string{"auto", "off", "live-card"}

// GitRepositoryIdentity is where a working directory's repository lives; a worktree shares the common directory of its main one.
type GitRepositoryIdentity struct {
	Root      string
	CommonDir string
}

// WorkflowChatProgressProjection is the resolved chat progress of a run.
type WorkflowChatProgressProjection struct {
	Mode         string // off or live-card
	RepoRelation string // same or other
	RepoLabel    string
}

// WorkflowChatProgressInput is the argument of resolveWorkflowChatProgress. Requested is the raw parameter: nil is unset.
type WorkflowChatProgressInput struct {
	Requested   any
	ParentCwd   string
	WorkflowCwd string
	Background  bool
}

// chatProgressGit runs `git -C cwd args` and returns its trimmed stdout; "" when git fails or prints nothing.
func chatProgressGit(cwd string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", cwd}, args...)...).Output()
	if err != nil {
		return ""
	}
	return jsTrim(string(out))
}

func chatProgressRealPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// resolveGitRepositoryIdentity is resolveGitRepositoryIdentity; nil when cwd is not inside a work tree.
func resolveGitRepositoryIdentity(cwd string) *GitRepositoryIdentity {
	if chatProgressGit(cwd, "rev-parse", "--is-inside-work-tree") != "true" {
		return nil
	}
	root := chatProgressGit(cwd, "rev-parse", "--show-toplevel")
	commonDir := chatProgressGit(cwd, "rev-parse", "--git-common-dir")
	if root == "" || commonDir == "" {
		return nil
	}
	commonPath := commonDir
	if !filepath.IsAbs(commonDir) {
		commonPath = filepath.Join(root, commonDir)
		for _, c := range []string{filepath.Join(absOr(cwd), commonDir), filepath.Join(root, commonDir)} {
			if _, err := os.Stat(c); err == nil {
				commonPath = c
				break
			}
		}
	}
	return &GitRepositoryIdentity{Root: chatProgressRealPath(root), CommonDir: chatProgressRealPath(commonPath)}
}

func absOr(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func sameGitRepositoryIdentity(l, r *GitRepositoryIdentity) bool {
	if l == nil || r == nil {
		return false
	}
	return l.CommonDir == r.CommonDir || l.Root == r.Root
}

// isSameGitRepository is isSameGitRepository.
func isSameGitRepository(leftCwd, rightCwd string) bool {
	return sameGitRepositoryIdentity(resolveGitRepositoryIdentity(leftCwd), resolveGitRepositoryIdentity(rightCwd))
}

// resolveWorkflowChatProgress is resolveWorkflowChatProgress: the projection, or the error message of a request it refuses.
func resolveWorkflowChatProgress(in WorkflowChatProgressInput) (*WorkflowChatProgressProjection, string) {
	requested := "auto"
	if in.Requested != nil {
		s, isString := in.Requested.(string)
		valid := false
		for _, m := range workflowChatProgressModes {
			valid = valid || m == s
		}
		if !isString || !valid {
			return nil, "chatProgress must be one of: " + strings.Join(workflowChatProgressModes, ", ") + "."
		}
		requested = s
	}
	parent := resolveGitRepositoryIdentity(in.ParentCwd)
	workflow := resolveGitRepositoryIdentity(in.WorkflowCwd)
	same := sameGitRepositoryIdentity(parent, workflow)
	label := ""
	if workflow != nil {
		label = filepath.Base(workflow.Root)
	}
	relation := "other"
	if same {
		relation = "same"
	}
	mode := requested
	if requested == "auto" {
		mode = "off"
		if same && !in.Background {
			mode = "live-card"
		}
	}
	if mode == "live-card" && !same {
		return nil, "chatProgress: 'live-card' is only available for workflowScript runs in the same Git repository."
	}
	if mode == "live-card" && in.Background {
		return nil, "chatProgress: 'live-card' is unavailable for async workflowScript. Async workflows have no inline live card; omit chatProgress or use auto/off. Use async:false only when the parent must block."
	}
	return &WorkflowChatProgressProjection{Mode: mode, RepoRelation: relation, RepoLabel: label}, ""
}

// WorkflowChatProgressRow is one row of the live card.
type WorkflowChatProgressRow struct {
	Key        string
	State      string // planned, running, complete, failed, detached, stopped
	Label      string
	Phase      string
	RunID      string
	DurationMs *int64
	Error      string
	Preflight  *WorkflowPreflightLane
}

// buildWorkflowChatProgressRows is buildWorkflowChatProgressRows: one row per launched run key, in launch order, from the
// latest state of its trace entries. A declared preflight lane only annotates a row; it never adds one.
func buildWorkflowChatProgressRows(trace []WorkflowScriptTraceEntry, preflight *WorkflowPreflight) []*WorkflowChatProgressRow {
	var order []string
	rows := map[string]*WorkflowChatProgressRow{}
	clean := func(v string) string { return jsTrim(v) }
	for _, e := range trace {
		if e.Operation != "run" {
			continue
		}
		existing := rows[e.Key]
		if e.State == "reused" {
			if existing != nil {
				if l := clean(e.Label); l != "" {
					existing.Label = l
				}
				if p := clean(e.Phase); p != "" {
					existing.Phase = p
				}
			}
			continue
		}
		lane := WorkflowPreflightLaneForRuntimeKey(preflight, e.Key, e.GeneratedLaneKey)
		next := existing
		if next == nil {
			next = &WorkflowChatProgressRow{Key: e.Key, State: "running"}
		}
		if lane != nil && next.Preflight == nil {
			next.Preflight = lane
		}
		switch e.State {
		case "completed":
			next.State = "complete"
		case "failed":
			next.State = "failed"
		case "detached":
			next.State = "detached"
		case "stopped":
			next.State = "stopped"
		default:
			next.State = "running"
		}
		if l := clean(e.Label); l != "" {
			next.Label = l
		}
		if p := clean(e.Phase); p != "" {
			next.Phase = p
		}
		next.RunID = e.RunID
		next.DurationMs = e.DurationMs
		next.Error = e.Error
		if existing == nil {
			order = append(order, e.Key)
		}
		rows[e.Key] = next
	}
	out := make([]*WorkflowChatProgressRow, len(order))
	for i, k := range order {
		out[i] = rows[k]
	}
	return out
}

// foregroundResultIntercomStatus is foregroundResultIntercomStatus (subagent-executor.ts:2419): the status word of a foreground
// child result; an acceptance rejection is a failure even for exit code 0. result is a SingleResult as ordered JSON.
func foregroundResultIntercomStatus(result *jsObject) string {
	in := map[string]any{}
	for _, k := range []string{"exitCode", "interrupted", "detached", "processSignal", "timedOut", "stopped", "turnBudgetExceeded"} {
		if v, ok := lpGet(result, k); ok && v != nil {
			in[k] = v
		}
	}
	if acceptance := result.obj("acceptance"); acceptance != nil && acceptance.vals["status"] == "rejected" {
		in["success"] = false
	}
	return resolveSubagentResultStatus(in)
}

// shouldSuppressRoutineResultIntercom is shouldSuppressRoutineResultIntercom: a live-card workflow hides only the result
// intercom of children that all completed.
func shouldSuppressRoutineResultIntercom(suppress bool, results []*jsObject) bool {
	if !suppress || len(results) == 0 {
		return false
	}
	for _, r := range results {
		if foregroundResultIntercomStatus(r) != "completed" {
			return false
		}
	}
	return true
}

// missionWorkflowChildStatus is missionWorkflowChildStatus (subagent-executor.ts:4480): the mission-ledger status of a child
// run from its tool result. A launch receipt of an async run (no results yet) is still running.
func missionWorkflowChildStatus(isError bool, results []*jsObject, asyncID, asyncDir string) string {
	for _, c := range results {
		if objTruthy(c, "detached") || objTruthy(c, "interrupted") {
			return "paused"
		}
	}
	failed := isError
	for _, c := range results {
		if f, ok := c.num("exitCode"); !ok || f != 0 {
			failed = true
		}
	}
	if failed {
		return "failed"
	}
	if len(results) == 0 && (asyncID != "" || asyncDir != "") {
		return "running"
	}
	return "completed"
}
