package tintinweb_subagents

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The workflowManager over this port's agent manager. An agent here is a child `pig --mode rpc` process (PORT.md D1),
// so what the original's manager does in-process is mapped as far as a child process can: the spawn, the wait, the
// abort, the record. What a child process cannot do yet is refused (workflowRefusalError), never approximated:
// isolation: "worktree", agent({ schema }) and resume of a finished agent's session fail the run with the refusal text.
// upstream: src/agent-manager.ts spawnAndWait, abort, resume.

type managerWorkflowAdapter struct {
	a   *app
	ctx sdk.Context
}

func (m managerWorkflowAdapter) toRecord(r *agentRecord) workflowRecord {
	m.a.mgr.mu.Lock()
	defer m.a.mgr.mu.Unlock()
	return workflowRecord{ID: r.ID, Status: r.Status, Result: r.Result, Error: r.Error, ToolUses: r.ToolUses,
		LifetimeTokens: r.Tokens, OutputTokens: r.OutputTokens}
}

func (m managerWorkflowAdapter) SpawnAndWait(typ, prompt string, opts workflowSpawnOptions, onSpawned func(id string)) (workflowRecord, error) {
	switch {
	case opts.Isolation != "":
		return workflowRecord{}, &workflowRefusalError{`worktree isolation (isolation: "` + opts.Isolation + `")`}
	case opts.StructuredOutput != nil:
		return workflowRecord{}, &workflowRefusalError{"structured output (agent({ schema }))"}
	}
	r, err := m.a.startOwned(m.ctx, opts.WorkflowID, typ, prompt, opts.Description, "", opts.Model, opts.ThinkingLevel, 0, false, false)
	if err != nil {
		return workflowRecord{}, err
	}
	if onSpawned != nil {
		onSpawned(r.ID)
	}
	ctx := opts.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-r.done:
	case <-ctx.Done():
		m.a.mgr.abort(r)
		<-r.done
	}
	return m.toRecord(r), nil
}

func (m managerWorkflowAdapter) Abort(id string) {
	if r := m.a.mgr.get(id); r != nil {
		m.a.mgr.abort(r)
	}
}

func (m managerWorkflowAdapter) Resume(context.Context, string, string) (workflowRecord, bool, error) {
	return workflowRecord{}, false, &workflowRefusalError{"resuming a finished agent's session (resume)"}
}

func (m managerWorkflowAdapter) GetRecord(id string) (workflowRecord, bool) {
	r := m.a.mgr.get(id)
	if r == nil {
		return workflowRecord{}, false
	}
	return m.toRecord(r), true
}

// sdkModelRegistry is the session's model registry as workflow resolution reads it.
type sdkModelRegistry struct{ r sdk.ModelRegistry }

func wfModelOf(m map[string]any) *workflowModel {
	if m == nil {
		return nil
	}
	id, _ := m["id"].(string)
	provider, _ := m["provider"].(string)
	name, _ := m["name"].(string)
	return &workflowModel{Provider: provider, ID: id, Name: name}
}

func (s sdkModelRegistry) Find(provider, id string) *workflowModel {
	return wfModelOf(s.r.Find(provider, id))
}

func (s sdkModelRegistry) list(get func() ([]map[string]any, error)) []*workflowModel {
	models, err := get()
	if err != nil {
		return nil
	}
	out := make([]*workflowModel, 0, len(models))
	for _, m := range models {
		out = append(out, wfModelOf(m))
	}
	return out
}

func (s sdkModelRegistry) GetAll() []*workflowModel {
	if all := s.list(s.r.GetAll); all != nil {
		return all
	}
	return []*workflowModel{}
}

func (s sdkModelRegistry) GetAvailable() []*workflowModel { return s.list(s.r.GetAvailable) }

// wfExec runs a command through the host's executor (pi.exec). timeout is in milliseconds on the wire.
func wfExec(ctx sdk.Context) func(context.Context, string, []string, string, time.Duration) (workflowExecResult, error) {
	return func(_ context.Context, command string, args []string, cwd string, timeout time.Duration) (workflowExecResult, error) {
		res, err := ctx.ExecWithOptions(command, args, sdk.ExecOptions{Cwd: cwd, Timeout: float64(timeout.Milliseconds())})
		if err != nil {
			return workflowExecResult{}, err
		}
		return workflowExecResult{Stdout: res.Stdout, Stderr: res.Stderr, Code: res.ExitCode, Killed: res.Killed}, nil
	}
}

// workflowSessionTaskDir is the per-session scratch directory, created if missing: /tmp/pi-subagents-<uid>/<cwd>/<session>/tasks,
// the layout Claude Code uses; shared with the original's agent transcripts, a workflow persists each invocation's
// script here so iterating is edit-the-file-then-rerun. upstream: output-file.ts sessionTaskDir.
func workflowSessionTaskDir(cwd, sessionID string) (string, error) {
	encoded := strings.ReplaceAll(strings.ReplaceAll(cwd, "/", "-"), "\\", "-")
	if len(encoded) > 2 && encoded[1] == '-' && (encoded[0] >= 'A' && encoded[0] <= 'Z' || encoded[0] >= 'a' && encoded[0] <= 'z') {
		encoded = encoded[2:]
	}
	encoded = strings.TrimLeft(encoded, "-")
	root := filepath.Join(os.TempDir(), "pi-subagents-"+wfUID())
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(root, 0o700) // no-op on Windows
	dir := filepath.Join(root, encoded, sessionID, "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func wfUID() string {
	if uid := os.Getuid(); uid >= 0 {
		return strconv.Itoa(uid)
	}
	return "0"
}

// workflowChildBlock tells a workflow's own child that its final text is a return value. A workflow child's final text
// is not read by a human: it is the value agent() resolves to, and the script interpolates it straight into the next
// stage's prompt. Not applied to every subagent: an ordinary agent's output IS read by a human, so terse raw data would
// be the wrong answer there. upstream: prompts.ts PromptExtras.workflowChild.
const workflowChildBlock = "\n\n<workflow_child>\n" +
	"Your final message IS the return value of this task. A workflow script captures it and passes it to the next stage; no person reads it.\n" +
	"Return only the answer, in exactly the shape the prompt asks for — no preamble, no summary of what you did, no offer to continue.\n" +
	"</workflow_child>"

// withWorkflowChildBlock puts the workflow_child block where buildAgentPrompt puts it: straight after the environment
// block (and the worktree block, in the original), before the agent's own instructions. It is spliced into the text
// buildAgentPrompt built, rather than passed to it, so the prompt builder stays the one file other slices edit; when
// the builder takes extras, this becomes `extras.workflowChild`.
func withWorkflowChildBlock(system string, cfg *agentConfig, cwd string, env envInfo) string {
	repo := "Not a git repository"
	if env.IsGitRepo {
		repo = "Git repository: yes\nBranch: " + env.Branch
	}
	envBlock := "# Environment\nWorking directory: " + cwd + "\n" + repo + "\nPlatform: " + env.Platform
	i := strings.Index(system, envBlock)
	if i < 0 {
		return system
	}
	end := i + len(envBlock)
	return system[:end] + workflowChildBlock + system[end:]
}
