package pi_subagents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

const promptWorkflowsFile = "prompt-workflows"

func writePrompt(t *testing.T, dir, name, content string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, name+".md"), content)
}

type promptEnv struct {
	notes []string
	sent  []string
	runs  []map[string]any
	env   promptCommandEnv
}

func newPromptEnv(cwd string) *promptEnv {
	p := &promptEnv{}
	p.env = promptCommandEnv{
		Cwd:    cwd,
		Notify: func(m, level string) { p.notes = append(p.notes, level+": "+m) },
		Send:   func(c string) { p.sent = append(p.sent, c) },
		Run:    func(params map[string]any) error { p.runs = append(p.runs, params); return nil },
	}
	return p
}

func promptFixture(t *testing.T) (agentDirPath, cwd string) {
	t.Helper()
	temp := tmp(t)
	withTempHome(t)
	agentDirPath = filepath.Join(temp, "agent")
	cwd = filepath.Join(temp, "repo")
	os.MkdirAll(cwd, 0o755)
	t.Setenv("PI_CODING_AGENT_DIR", agentDirPath)
	return
}

func findPrompt(ws []PromptWorkflow, name string) *PromptWorkflow {
	return findPromptWorkflow(ws, name)
}

func TestPromptWorkflowsTwins(t *testing.T) {
	tw(t, promptWorkflowsFile, "discovers project workflows over user workflows", func(t *testing.T) {
		agentDirPath, cwd := promptFixture(t)
		writePrompt(t, filepath.Join(agentDirPath, "prompts"), "native-test", "---\ndescription: User version\nsubagent: reviewer\n---\nUser body\n")
		writePrompt(t, filepath.Join(cwd, ".pi", "prompts"), "native-test", "---\ndescription: Project version\nsubagent: worker\nmodel: openai/gpt-5-mini\n---\nProject body $1\n")
		ws, err := discoverPromptWorkflows(cwd)
		eq(t, err, nil, "discover")
		w := findPrompt(ws, "native-test")
		eq(t, w.Description, "Project version", "description")
		eq(t, w.Agent, "worker", "agent")
		eq(t, w.Model, "openai/gpt-5-mini", "model")
	})

	tw(t, promptWorkflowsFile, "discovers symlinked prompt files and skips dangling links", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks need privileges on Windows")
		}
		agentDirPath, cwd := promptFixture(t)
		temp := filepath.Dir(cwd)
		promptsDir := filepath.Join(agentDirPath, "prompts")
		writePrompt(t, filepath.Join(temp, "dotfiles"), "linked", "---\ndescription: Linked prompt\n---\nSay hi\n")
		os.MkdirAll(promptsDir, 0o755)
		os.Symlink(filepath.Join(temp, "dotfiles", "linked.md"), filepath.Join(promptsDir, "linked.md"))
		os.Symlink(filepath.Join(temp, "dotfiles", "missing.md"), filepath.Join(promptsDir, "dangling.md"))
		ws, err := discoverPromptWorkflows(cwd)
		eq(t, err, nil, "discover")
		eq(t, findPrompt(ws, "linked") != nil, true, "linked")
		eq(t, findPrompt(ws, "dangling") == nil, true, "dangling")
		os.Symlink(filepath.Join(promptsDir, "loop.md"), filepath.Join(promptsDir, "loop.md"))
		_, err = discoverPromptWorkflows(cwd)
		eq(t, errors.Is(err, syscall.ELOOP), true, "a symlink loop is an ELOOP error")
	})

	tw(t, promptWorkflowsFile, "runs a named workflow through native subagent execution", func(t *testing.T) {
		_, cwd := promptFixture(t)
		writePrompt(t, filepath.Join(cwd, ".pi", "prompts"), "native-run", "---\ndescription: Run native prompt\nsubagent: reviewer\nmodel: anthropic/claude-sonnet-4\nskill: deslop,typescript-code\n---\nReview $1 with $ARGUMENTS\n")
		p := newPromptEnv(cwd)
		eq(t, runPromptWorkflowCommand(p.env, "native-run target --fork"), nil, "command")
		eq(t, len(p.sent), 0, "nothing sent")
		eq(t, len(p.runs), 1, "one run")
		params := p.runs[0]
		for _, k := range []string{"agent", "task", "clarify", "model", "skill", "context"} {
			if _, has := params[k]; has {
				t.Errorf("params.%s is set", k)
			}
		}
		eq(t, params["agentScope"], "both", "agentScope")
		eq(t, params["async"], false, "async")
		script, _ := params["workflowScript"].(string)
		for _, want := range []string{`runs.run("prompt-1-native-run"`, `"agent":"reviewer"`, `"model":"anthropic/claude-sonnet-4"`, `"skill":["deslop","typescript-code"]`, `"context":"fork"`, "Review target with target"} {
			contains(t, script, want)
		}
	})

	tw(t, promptWorkflowsFile, "runs declared prompt sequences through workflowScript", func(t *testing.T) {
		_, cwd := promptFixture(t)
		dir := filepath.Join(cwd, ".pi", "prompts")
		writePrompt(t, dir, "native-analyze", "---\ndescription: Analyze\nsubagent: scout\nchain: native-analyze -> native-fix\n---\nAnalyze $@\n")
		writePrompt(t, dir, "native-fix", "---\ndescription: Fix\nsubagent: worker\n---\nFix from {previous}: $@\n")
		p := newPromptEnv(cwd)
		eq(t, runPromptWorkflowCommand(p.env, "native-analyze bug report"), nil, "command")
		eq(t, len(p.runs), 1, "one run")
		params := p.runs[0]
		for _, k := range []string{"agent", "task", "clarify"} {
			if _, has := params[k]; has {
				t.Errorf("params.%s is set", k)
			}
		}
		eq(t, params["agentScope"], "both", "agentScope")
		eq(t, params["async"], false, "async")
		script, _ := params["workflowScript"].(string)
		contains(t, script, `runs.run("prompt-1-native-analyze"`)
		contains(t, script, `runs.run("prompt-2-native-fix"`)
		contains(t, script, `replaceAll("{previous}"`)
		eq(t, reservedPromptCommandNames["chain-prompts"], true, "chain-prompts is not a prompt workflow")

		var launches []string
		o := wfOptions(script)
		o.Launch = func(_ context.Context, key string, params *jsObject, _ WorkflowAdmission) *WorkflowFuture[*jsObject] {
			task, _ := params.str("task")
			launches = append(launches, key+"="+task)
			out := "fixed output"
			if key == "prompt-1-native-analyze" {
				out = "analysis output"
			}
			return WorkflowResolved(okChild(key, out))
		}
		res := wfRun(t, o)
		eq(t, strings.Join(launches, "|"), "prompt-1-native-analyze=Analyze bug report|prompt-2-native-fix=Fix from analysis output: bug report", "launches")
		eq(t, res.Value, "fixed output", "value")
	})
}
