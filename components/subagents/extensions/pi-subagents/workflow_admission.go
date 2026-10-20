package pi_subagents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The admission half of worktree isolation (subagent-executor.ts preflightWorkflowWorktrees and worktree.ts
// preflightWorktreeSource): a read-only check, before any child launches, that every managed-worktree source is a clean git
// repository. Allocating the worktrees themselves is the worktree slice's.

const projectSubagentsRelativeDir = ".pi/subagents" // PROJECT_SUBAGENTS_RELATIVE_DIR

const setupCommandMaxBuffer = 1024 * 1024

// gitProbe runs `git -C cwd args...` and returns its stdout. A status outside accepted is an error carrying git's stderr.
func gitProbe(ctx context.Context, deadlineAt *float64, cwd string, args []string, accepted ...int) (stdout string, status int, err error) {
	if ctx.Err() != nil {
		return "", 0, errors.New("Worktree setup aborted")
	}
	if deadlineAt != nil && float64(time.Now().UnixMilli()) >= *deadlineAt {
		return "", 0, errors.New("Worktree setup deadline exceeded")
	}
	if deadlineAt != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, time.UnixMilli(int64(*deadlineAt)))
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	runErr := cmd.Run()
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", 0, errors.New("Worktree setup deadline exceeded")
		}
		return "", 0, errors.New("Worktree setup aborted")
	}
	status = 0
	if runErr != nil {
		var exit *exec.ExitError
		if !errors.As(runErr, &exit) {
			return "", 0, runErr
		}
		status = exit.ExitCode()
	}
	if out.Len() > setupCommandMaxBuffer || errOut.Len() > setupCommandMaxBuffer {
		return "", status, errors.New(probeFailure(errOut.String()))
	}
	ok := false
	if len(accepted) == 0 {
		accepted = []int{0}
	}
	for _, a := range accepted {
		ok = ok || a == status
	}
	if !ok {
		return "", status, errors.New(probeFailure(errOut.String()))
	}
	return out.String(), status, nil
}

func probeFailure(stderr string) string {
	s := jsTrim(stderr)
	if len(s) > 2000 {
		s = string(utf16Slice(s, 2000))
	}
	if s == "" {
		return "Worktree source probe failed"
	}
	return s
}

// preflightWorktreeSource is the read-only admission check of one source directory; allocation repeats it because the
// source can change.
func preflightWorktreeSource(ctx context.Context, cwd string, deadlineAt *float64) error {
	inside, _, err := gitProbe(ctx, deadlineAt, cwd, []string{"rev-parse", "--is-inside-work-tree"}, 0, 128)
	if err != nil {
		return err
	}
	if jsTrim(inside) != "true" {
		return errors.New("worktree isolation requires a git repository")
	}
	top, _, err := gitProbe(ctx, deadlineAt, cwd, []string{"rev-parse", "--show-toplevel"})
	if err != nil {
		return err
	}
	toplevel := jsTrim(top)
	// pi-subagents writes durable runtime state under .pi/subagents/ by default; it must not make isolation unusable.
	status, _, err := gitProbe(ctx, deadlineAt, toplevel, []string{"status", "--porcelain", "--", ":!" + projectSubagentsRelativeDir})
	if err != nil {
		return err
	}
	if jsTrim(status) != "" {
		return errors.New("worktree isolation requires a clean git working tree. Commit or stash changes first.")
	}
	return nil
}

// WorkflowAdmissionCall is one child of an admission request.
type WorkflowAdmissionCall struct {
	Key    string
	Params *jsObject
}

// WorkflowWorktreeAdmission is the input of preflightWorkflowWorktrees.
type WorkflowWorktreeAdmission struct {
	WorkflowDefaults *jsObject
	DefaultWorktree  *bool
	Calls            []WorkflowAdmissionCall
	CtxCwd           string
	DeadlineAt       *float64
}

// preflightWorkflowWorktrees rechecks the effective source of every managed-worktree child: a retained resume is skipped (it is
// resolved after admission), the rest use the workflow defaults, and each distinct source is probed once.
func preflightWorkflowWorktrees(ctx context.Context, in WorkflowWorktreeAdmission) error {
	var order []string
	sources := map[string][]string{}
	for _, call := range in.Calls {
		if call.Params.has("resume") {
			continue
		}
		effective, err := prepareWorkflowLaunchParams(in.WorkflowDefaults, call.Params, "preflight", call.Key, WorkflowLaunchOptions{})
		if err != nil {
			return err
		}
		wt, set := effective.vals["worktree"].(bool)
		if !set {
			wt = in.DefaultWorktree != nil && *in.DefaultWorktree
		}
		if !wt {
			continue
		}
		cwd := in.CtxCwd
		if c, ok := effective.vals["cwd"].(string); ok && c != "" {
			cwd = filepath.Join(in.CtxCwd, c)
			if filepath.IsAbs(c) {
				cwd = filepath.Clean(c)
			}
		}
		if _, seen := sources[cwd]; !seen {
			order = append(order, cwd)
		}
		sources[cwd] = append(sources[cwd], call.Key)
	}
	for _, cwd := range order {
		if err := preflightWorktreeSource(ctx, cwd, in.DeadlineAt); err != nil {
			quoted := make([]string, len(sources[cwd]))
			for i, k := range sources[cwd] {
				quoted[i] = "'" + k + "'"
			}
			return fmt.Errorf("Worktree admission failed for %s at %s: %s Select the correct cwd or arrange an operator-approved commit/stash.", strings.Join(quoted, ", "), cwd, err.Error())
		}
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return nil
}
