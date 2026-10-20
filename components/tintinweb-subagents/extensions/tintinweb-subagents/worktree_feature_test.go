package tintinweb_subagents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests of the memory and worktree-isolation features through the Agent tool, the manager and the spawn RPC.
// upstream: agent-manager.ts startAgent, agent-runner.ts runAgent, index.ts (Agent tool), invocation-config.ts.

func agentFilesRepo(t *testing.T, files map[string]string, settings string) string {
	t.Helper()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	repo := initGitRepo(t)
	for name, body := range files {
		mustMkdir(t, filepath.Join(repo, ".pi", "agents"))
		mustWrite(t, filepath.Join(repo, ".pi", "agents", name+".md"), body)
	}
	if settings != "" {
		mustMkdir(t, filepath.Join(repo, ".pi"))
		mustWrite(t, filepath.Join(repo, ".pi", "subagents.json"), settings)
	}
	return repo
}

func (r *rig) spawnFG(params obj) (string, string) {
	r.t.Helper()
	p := obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "run_in_background": false}
	for k, v := range params {
		p[k] = v
	}
	return r.tool("Agent", p)
}

func TestIsolationStrictFailure(t *testing.T) {
	t.Run("a foreground call outside a git repository fails the tool call, with no record and no child", func(t *testing.T) {
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		r := startRigIn(t, t.TempDir())
		text, failure := r.spawnFG(obj{"isolation": "worktree"})
		eq(t, text, "")
		eq(t, strings.Contains(failure, worktreeStartupError), true)
		eq(t, r.fleet.count(), 0)
		eq(t, len(r.app.mgr.list()), 0)
	})
	t.Run("a background call fails the tool call too and hands its slot back", func(t *testing.T) {
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		r := startRigIn(t, t.TempDir())
		_, failure := r.tool("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan", "isolation": "worktree"})
		eq(t, strings.Contains(failure, `Cannot run with isolation: "worktree" — not a git repo, no commits yet, or `+"`git worktree add`"+` failed. Initialize git and commit at least once, or omit `+"`isolation`"+`.`), true)
		eq(t, r.app.mgr.running, 0)
		eq(t, len(r.app.mgr.list()), 0)
		// the handle goes back
		r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "Plan"})
		eq(t, r.app.mgr.list()[0].Handle, "plan")
	})
	t.Run("a queued agent that cannot get a worktree ends as an error on its record", func(t *testing.T) {
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		r := startRigIn(t, t.TempDir())
		r.app.mgr.maxConcurrent = 1
		r.must("Agent", obj{"prompt": "p", "description": "first", "subagent_type": "Plan"})
		out := r.must("Agent", obj{"prompt": "p", "description": "second", "subagent_type": "Plan", "isolation": "worktree"})
		eq(t, strings.Contains(out, "queued"), true)
		queued := r.app.mgr.list()[1]
		r.fleet.wait(t, 1)[0].finish("done")
		<-queued.done
		eq(t, queued.Status, statusError)
		eq(t, queued.Error, worktreeStartupError)
		eq(t, r.fleet.count(), 1)
	})
}

func TestIsolationResolution(t *testing.T) {
	t.Run("an agent file's isolation: worktree isolates a call that did not ask", func(t *testing.T) {
		repo := agentFilesRepo(t, map[string]string{"wt": "---\ndescription: w\nisolation: worktree\n---\nGo."}, "")
		r := startRigIn(t, repo)
		markerChild(r)
		text, failure := r.spawnFG(obj{"subagent_type": "wt"})
		eq(t, failure, "")
		eq(t, strings.Contains(text, "Changes saved to branch `pi-agent-"), true)
		eq(t, exists(filepath.Join(repo, e2eMarkerFile)), false)
	})
	t.Run("an agent file's isolation: off vetoes the call's worktree", func(t *testing.T) {
		repo := agentFilesRepo(t, map[string]string{"nowt": "---\ndescription: n\nisolation: off\n---\nGo."}, "")
		r := startRigIn(t, repo)
		markerChild(r)
		text, failure := r.spawnFG(obj{"subagent_type": "nowt", "isolation": "worktree"})
		eq(t, failure, "")
		eq(t, strings.Contains(text, "Changes saved"), false)
		eq(t, exists(filepath.Join(repo, e2eMarkerFile)), true)
	})
	t.Run("isolation: off runs in the checkout", func(t *testing.T) {
		repo := agentFilesRepo(t, nil, "")
		r := startRigIn(t, repo)
		markerChild(r)
		_, failure := r.spawnFG(obj{"isolation": "off"})
		eq(t, failure, "")
		eq(t, exists(filepath.Join(repo, e2eMarkerFile)), true)
	})
	t.Run("a worktree with no changes is removed and the result names no branch", func(t *testing.T) {
		repo := agentFilesRepo(t, nil, "")
		r := startRigIn(t, repo)
		var cwd string
		r.app.mgr.start = func(_ context.Context, spec childSpec) (child, error) {
			cwd = spec.Cwd
			c := &fakeChild{spec: spec, release: make(chan struct{})}
			c.finish("nothing to change")
			return c, nil
		}
		text, failure := r.spawnFG(obj{"isolation": "worktree"})
		eq(t, failure, "")
		eq(t, strings.Contains(text, "nothing to change") && !strings.Contains(text, "Changes saved"), true)
		eq(t, cwd != repo && cwd != "", true)
		eq(t, exists(cwd), false)
		eq(t, gitOut(t, repo, "branch", "--list", "pi-agent-*"), "")
	})
	t.Run("the system prompt names the copy and the directory it was made from", func(t *testing.T) {
		repo := agentFilesRepo(t, nil, "")
		r := startRigIn(t, repo)
		var prompt, cwd string
		r.app.mgr.start = func(_ context.Context, spec childSpec) (child, error) {
			prompt, cwd = spec.SystemPrompt, spec.Cwd
			c := &fakeChild{spec: spec, release: make(chan struct{})}
			c.finish("ok")
			return c, nil
		}
		r.spawnFG(obj{"isolation": "worktree"})
		eq(t, strings.Contains(prompt, "Working directory: "+cwd+"\n"), true)
		eq(t, strings.Contains(prompt, "<worktree_isolation>\nYour working directory is an isolated git worktree copy of "+repo+".\nWork only inside it — never in "+repo+", even if other instructions name that path as your working directory.\n</worktree_isolation>"), true)
		eq(t, strings.Contains(prompt, "Git repository: yes"), true)
	})
	t.Run("a failed child still cleans its worktree up", func(t *testing.T) {
		repo := agentFilesRepo(t, nil, "")
		r := startRigIn(t, repo)
		r.fleet.setFail(errBoom)
		text, failure := r.spawnFG(obj{"isolation": "worktree"})
		eq(t, failure, "")
		eq(t, strings.Contains(text, "Agent failed: boom"), true)
		eq(t, len(strings.Split(gitOut(t, repo, "worktree", "list"), "\n")), 1)
	})
}

func TestIsolationThroughTheSpawnRPC(t *testing.T) {
	t.Run("isolation: worktree runs the agent in a copy and the result carries the branch", func(t *testing.T) {
		repo := agentFilesRepo(t, nil, "")
		r := startRigIn(t, repo)
		markerChild(r)
		reply := r.request("subagents:rpc:spawn", obj{"type": "Plan", "prompt": "p", "options": obj{"description": "d", "isolation": "worktree"}})
		eq(t, reply["success"], true)
		rec := r.app.mgr.get(reply["data"].(map[string]any)["id"].(string))
		<-rec.done
		eq(t, strings.Contains(rec.Result, "Changes saved to branch `pi-agent-"), true)
		eq(t, exists(filepath.Join(repo, e2eMarkerFile)), false)
	})
	t.Run("a failed worktree is the reply's error", func(t *testing.T) {
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		r := startRigIn(t, t.TempDir())
		reply := r.request("subagents:rpc:spawn", obj{"type": "Plan", "prompt": "p", "options": obj{"description": "d", "isolation": "worktree"}})
		eq(t, reply["success"], false)
		eq(t, strings.Contains(reply["error"].(string), "Cannot run with isolation"), true)
	})
	t.Run("the project switch drops the option (the manager checks it, the schema cannot)", func(t *testing.T) {
		repo := agentFilesRepo(t, nil, `{"worktreeIsolation": false}`)
		r := startRigIn(t, repo)
		markerChild(r)
		reply := r.request("subagents:rpc:spawn", obj{"type": "Plan", "prompt": "p", "options": obj{"description": "d", "isolation": "worktree"}})
		eq(t, reply["success"], true)
		<-r.app.mgr.get(reply["data"].(map[string]any)["id"].(string)).done
		eq(t, exists(filepath.Join(repo, e2eMarkerFile)), true)
	})
}

func TestToolTextFollowsTheSwitch(t *testing.T) {
	resetTypes()
	t.Cleanup(resetTypes)
	specs := loadToolSpecs()
	for _, enabled := range []bool{true, false} {
		setWorktreeIsolationEnabled(enabled)
		desc := agentToolDescription(currentRegistry())
		props := agentToolSchema(currentRegistry(), specs["Agent"])["properties"].(map[string]any)
		_, has := props["isolation"]
		eq(t, has, enabled)
		eq(t, strings.Contains(desc, `- Use isolation: "worktree" to give the agent its own git worktree (safe parallel file modifications); leave it unset, or pass "off", for none. The worktree is removed when the agent finishes; if it made changes, they are committed to a branch and the branch is named in the result.`), enabled)
		if enabled {
			eq(t, strings.Contains(desc, "- Use inherit_context if the agent needs the parent conversation history.\n- Use isolation:"), true)
			eq(t, props["isolation"].(map[string]any)["description"], isolationDescription)
		}
	}
	resetTypes()
}

func TestShutdownPrunesWorktrees(t *testing.T) {
	repo := agentFilesRepo(t, nil, "")
	r := startRigIn(t, repo)
	var mu sync.Mutex
	var calls [][]string
	r.app.mgr.git = func(args []string, cwd string, timeoutMS int) execResult {
		mu.Lock()
		calls = append(calls, append([]string{cwd}, args...))
		mu.Unlock()
		return osGit(args, cwd, timeoutMS)
	}
	r.Fire("session_shutdown", obj{"reason": "quit"})
	mu.Lock()
	defer mu.Unlock()
	eq(t, calls, [][]string{{repo, "worktree", "prune"}})
}

func TestAStopWhileTheCopyIsMadeDiscardsIt(t *testing.T) {
	repo := agentFilesRepo(t, nil, "")
	r := startRigIn(t, repo)
	gate, entered := make(chan struct{}), make(chan struct{})
	r.app.mgr.git = func(args []string, cwd string, timeoutMS int) execResult {
		if len(args) > 1 && args[0] == "worktree" && args[1] == "add" {
			close(entered)
			<-gate
		}
		return osGit(args, cwd, timeoutMS)
	}
	rec := r.app.mgr.spawn("Plan", "d", "", childSpec{Prompt: "p", Cwd: repo, Worktree: true}, true)
	<-entered
	eq(t, r.app.mgr.abort(rec), true)
	close(gate)
	select {
	case <-rec.done:
	case <-time.After(10 * time.Second):
		t.Fatal("never settled")
	}
	eq(t, rec.Status, statusStopped)
	eq(t, r.fleet.count(), 0)
	eq(t, len(strings.Split(gitOut(t, repo, "worktree", "list"), "\n")), 1)
}

func TestMemoryThroughTheAgentTool(t *testing.T) {
	run := func(t *testing.T, frontmatter string) (spec childSpec, repo string, home string) {
		t.Helper()
		repo = agentFilesRepo(t, map[string]string{"mem": "---\ndescription: m\n" + frontmatter + "\n---\nRemember."}, "")
		home = os.Getenv("HOME")
		r := startRigIn(t, repo)
		r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "mem"})
		return r.fleet.wait(t, 1)[0].spec, repo, home
	}
	t.Run("project memory of an agent that can write: block in the prompt, directory made", func(t *testing.T) {
		spec, repo, _ := run(t, "memory: project")
		dir := filepath.Join(repo, ".pi", "agent-memory", "mem")
		eq(t, exists(dir), true)
		eq(t, strings.Contains(spec.SystemPrompt, "# Agent Memory\n\nYou have a persistent memory directory at: "+dir+"/\nMemory scope: project"), true)
		eq(t, strings.HasSuffix(spec.SystemPrompt, "- You have Read, Write, and Edit tools available for managing memory files."), true)
		eq(t, spec.Tools, builtinToolNames) // every built-in tool is already there
	})
	t.Run("an existing MEMORY.md is injected", func(t *testing.T) {
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		repo := agentFilesRepo(t, map[string]string{"mem": "---\ndescription: m\nmemory: local\n---\nRemember."}, "")
		mustMkdir(t, filepath.Join(repo, ".pi", "agent-memory-local", "mem"))
		mustWrite(t, filepath.Join(repo, ".pi", "agent-memory-local", "mem", "MEMORY.md"), "- remember the milk")
		r := startRigIn(t, repo)
		r.must("Agent", obj{"prompt": "p", "description": "d", "subagent_type": "mem"})
		eq(t, strings.Contains(r.fleet.wait(t, 1)[0].spec.SystemPrompt, "## Current MEMORY.md\n- remember the milk"), true)
	})
	t.Run("an agent that can only read gets the read-only block and no directory", func(t *testing.T) {
		spec, repo, _ := run(t, "tools: bash, grep\nmemory: project")
		eq(t, exists(filepath.Join(repo, ".pi", "agent-memory", "mem")), false)
		eq(t, strings.Contains(spec.SystemPrompt, "# Agent Memory (read-only)\n\nMemory scope: project"), true)
		eq(t, strings.Contains(spec.SystemPrompt, "Memory Instructions"), false)
		eq(t, spec.Tools, []string{"bash", "grep", "read"})
	})
	t.Run("a writer with a short tool list gets the missing memory tools", func(t *testing.T) {
		spec, _, _ := run(t, "tools: write\nmemory: project")
		eq(t, spec.Tools, []string{"write", "read", "edit"})
		eq(t, strings.Contains(spec.SystemPrompt, "# Agent Memory\n"), true)
	})
	t.Run("a denied write tool does not count as write access", func(t *testing.T) {
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		cfg := &agentConfig{Name: "m", Memory: "project", BuiltinToolNames: []string{"read", "write", "edit"}, DisallowedTools: []string{"write", "edit"}}
		block, tools, err := memoryExtras(cfg, "m", cfg.BuiltinToolNames, t.TempDir())
		eq(t, err, nil)
		eq(t, strings.HasPrefix(block, "# Agent Memory (read-only)"), true)
		eq(t, tools, []string{"read", "write", "edit"})
	})
	t.Run("user scope goes under the agent dir", func(t *testing.T) {
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
		spec, _, _ := run(t, "memory: user")
		eq(t, strings.Contains(spec.SystemPrompt, filepath.Join(agentDir(), "agent-memory", "mem")+"/"), true)
	})
	t.Run("a symlinked memory directory fails the agent, not the call", func(t *testing.T) {
		repo := agentFilesRepo(t, map[string]string{"mem": "---\ndescription: m\nmemory: project\n---\nRemember."}, "")
		mustMkdir(t, filepath.Join(repo, ".pi", "agent-memory"))
		mustMkdir(t, filepath.Join(repo, "elsewhere"))
		link := filepath.Join(repo, ".pi", "agent-memory", "mem")
		mustSymlink(t, filepath.Join(repo, "elsewhere"), link)
		r := startRigIn(t, repo)
		text, failure := r.spawnFG(obj{"subagent_type": "mem"})
		eq(t, failure, "")
		eq(t, text, "Agent failed: Refusing to use symlinked memory directory: "+link)
		eq(t, r.fleet.count(), 0)
	})
	t.Run("project memory of an isolated agent lives in the worktree and lands on the branch", func(t *testing.T) {
		repo := agentFilesRepo(t, map[string]string{"mem": "---\ndescription: m\nmemory: project\nisolation: worktree\n---\nRemember."}, "")
		r := startRigIn(t, repo)
		r.app.mgr.start = func(_ context.Context, spec childSpec) (child, error) {
			dir := filepath.Join(spec.Cwd, ".pi", "agent-memory", "mem")
			mustWrite(t, filepath.Join(dir, "MEMORY.md"), "- learned")
			c := &fakeChild{spec: spec, release: make(chan struct{})}
			c.finish("done")
			return c, nil
		}
		text, _ := r.spawnFG(obj{"subagent_type": "mem"})
		m := strings.Split(strings.SplitN(text, "branch `", 2)[1], "`")[0]
		eq(t, strings.Contains(gitOut(t, repo, "ls-tree", "-r", "--name-only", m), ".pi/agent-memory/mem/MEMORY.md"), true)
		eq(t, exists(filepath.Join(repo, ".pi", "agent-memory", "mem")), false)
	})
}
