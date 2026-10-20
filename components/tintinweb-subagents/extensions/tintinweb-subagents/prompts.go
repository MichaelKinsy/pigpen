package tintinweb_subagents

import (
	"runtime"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// envInfo is what an agent's system prompt says about where it runs. upstream: src/env.ts.
type envInfo struct {
	IsGitRepo bool
	Branch    string
	Platform  string
}

// platformName is Node's process.platform for a GOOS.
func platformName(goos string) string {
	if goos == "windows" {
		return "win32"
	}
	return goos
}

// gitRunner runs git in a directory with a timeout and reports its stdout and exit code.
type gitRunner func(args []string, cwd string, timeoutMs int) (stdout string, code int, err error)

// detectEnv asks git, through the host, whether the workspace is a repository and on which branch.
func detectEnv(ctx sdk.Context, cwd string) envInfo {
	return detectEnvWith(func(args []string, cwd string, timeoutMs int) (string, int, error) {
		res, err := ctx.ExecWithOptions("git", args, sdk.ExecOptions{Cwd: cwd, Timeout: float64(timeoutMs)})
		if err != nil {
			return "", 0, err
		}
		return res.Stdout, res.ExitCode, nil
	}, cwd)
}

// detectEnvWith is detectEnv over any way of running git. upstream: src/env.ts detectEnv.
func detectEnvWith(git gitRunner, cwd string) envInfo {
	env := envInfo{Platform: platformName(runtime.GOOS)}
	out, code, err := git([]string{"rev-parse", "--is-inside-work-tree"}, cwd, 5000)
	env.IsGitRepo = err == nil && code == 0 && strings.TrimSpace(out) == "true"
	if env.IsGitRepo {
		env.Branch = "unknown"
		if out, code, err := git([]string{"branch", "--show-current"}, cwd, 5000); err == nil && code == 0 {
			env.Branch = strings.TrimSpace(out)
		}
	}
	return env
}

const genericBase = `# Role
You are a general-purpose coding agent for complex, multi-step tasks.
You have full access to read, write, edit files, and execute commands.
Do what has been asked; nothing more, nothing less.`

const subAgentBridge = `<sub_agent_context>
You are operating as a sub-agent invoked to handle a specific task.
- Use the read tool instead of cat/head/tail
- Use the edit tool instead of sed/awk
- Use the write tool instead of echo/heredoc
- Use the find tool instead of bash find/ls for file search
- Use the grep tool instead of bash grep/rg for content search
- Make independent tool calls in parallel
- Use absolute file paths
- Do not use emojis
- Be concise but complete
</sub_agent_context>`

// promptExtras are the sections added to an agent's system prompt besides its own. upstream: src/prompts.ts PromptExtras.
type promptExtras struct {
	MemoryBlock   string           // persistent memory content (first 200 lines of MEMORY.md and the instructions)
	SkillBlocks   []preloadedSkill // preloaded skill contents
	WorktreeBase  string           // the directory a worktree copy was made from: tells the agent to stay in the copy
	WorkflowChild bool             // a workflow's own child without a StructuredOutput tool: its final message is a return value
}

// buildAgentPrompt is an agent's whole system prompt. In "append" mode it is the parent's system prompt (or a
// generic base) with the agent's instructions added; in "replace" mode the agent's own. The text is the original's.
// upstream: src/prompts.ts buildAgentPrompt.
func buildAgentPrompt(c *agentConfig, cwd string, env envInfo, parentSystemPrompt string, extras promptExtras) string {
	tag := "<active_agent name=\"" + c.Name + "\"/>\n\n"
	repo := "Not a git repository"
	if env.IsGitRepo {
		repo = "Git repository: yes\nBranch: " + env.Branch
	}
	envBlock := "# Environment\nWorking directory: " + cwd + "\n" + repo + "\nPlatform: " + env.Platform
	worktreeBlock := ""
	if extras.WorktreeBase != "" {
		worktreeBlock = "\n\n<worktree_isolation>\nYour working directory is an isolated git worktree copy of " + extras.WorktreeBase + ".\n" +
			"Work only inside it — never in " + extras.WorktreeBase + ", even if other instructions name that path as your working directory.\n</worktree_isolation>"
	}
	workflowBlock := ""
	if extras.WorkflowChild {
		workflowBlock = "\n\n<workflow_child>\nYour final message IS the return value of this task. A workflow script captures it and passes it to the next stage; no person reads it.\n" +
			"Return only the answer, in exactly the shape the prompt asks for — no preamble, no summary of what you did, no offer to continue.\n</workflow_child>"
	}
	var sections []string
	if extras.MemoryBlock != "" {
		sections = append(sections, extras.MemoryBlock)
	}
	for _, skill := range extras.SkillBlocks {
		sections = append(sections, "\n# Preloaded Skill: "+skill.Name+"\n"+skill.Content)
	}
	suffix := ""
	if len(sections) > 0 {
		suffix = "\n\n" + strings.Join(sections, "\n")
	}
	if c.PromptMode == "append" {
		identity := parentSystemPrompt
		if identity == "" {
			identity = genericBase
		}
		custom := ""
		if strings.TrimSpace(c.SystemPrompt) != "" {
			custom = "\n\n<agent_instructions>\n" + c.SystemPrompt + "\n</agent_instructions>"
		}
		return identity + "\n\n" + subAgentBridge + "\n\n" + tag + envBlock + worktreeBlock + workflowBlock + custom + suffix
	}
	header := "You are a pi coding agent sub-agent.\nYou have been invoked to handle a specific task autonomously.\n\n" + envBlock
	return tag + header + worktreeBlock + workflowBlock + "\n\n" + c.SystemPrompt + suffix
}
