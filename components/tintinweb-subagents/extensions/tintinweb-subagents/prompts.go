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

// detectEnv asks git, through the host, whether the workspace is a repository and on which branch.
func detectEnv(ctx sdk.Context, cwd string) envInfo {
	env := envInfo{Platform: platformName(runtime.GOOS)}
	res, err := ctx.ExecWithOptions("git", []string{"rev-parse", "--is-inside-work-tree"}, sdk.ExecOptions{Cwd: cwd, Timeout: 5000})
	env.IsGitRepo = err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) == "true"
	if env.IsGitRepo {
		env.Branch = "unknown"
		if res, err := ctx.ExecWithOptions("git", []string{"branch", "--show-current"}, sdk.ExecOptions{Cwd: cwd, Timeout: 5000}); err == nil && res.ExitCode == 0 {
			env.Branch = strings.TrimSpace(res.Stdout)
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

// promptExtras are the sections added to an agent's system prompt. upstream: src/prompts.ts PromptExtras.
type promptExtras struct {
	// MemoryBlock is the persistent memory section (the first 200 lines of MEMORY.md and the instructions).
	MemoryBlock string
	// WorktreeBase is the directory an isolated agent's worktree copy was made from; set only for
	// `isolation: "worktree"`, it triggers the block that tells the agent not to work there.
	WorktreeBase string
}

// buildAgentPrompt is an agent's whole system prompt. In "append" mode it is the parent's system prompt (or a
// generic base) with the agent's instructions added; in "replace" mode the agent's own. The text is the original's.
// upstream: src/prompts.ts buildAgentPrompt (the memory and worktree extras; skills and workflow extras are not part of this port yet).
func buildAgentPrompt(c *agentConfig, cwd string, env envInfo, parentSystemPrompt string, extras promptExtras) string {
	tag := "<active_agent name=\"" + c.Name + "\"/>\n\n"
	repo := "Not a git repository"
	if env.IsGitRepo {
		repo = "Git repository: yes\nBranch: " + env.Branch
	}
	envBlock := "# Environment\nWorking directory: " + cwd + "\n" + repo + "\nPlatform: " + env.Platform
	// A worktree agent is told its cwd twice (the env block: the copy; the inherited parent prompt or the task: the
	// main checkout); it follows the latter and works in the shared tree (#187), so this resolves the contradiction.
	worktree := ""
	if extras.WorktreeBase != "" {
		worktree = "\n\n<worktree_isolation>\nYour working directory is an isolated git worktree copy of " + extras.WorktreeBase +
			".\nWork only inside it — never in " + extras.WorktreeBase + ", even if other instructions name that path as your working directory.\n</worktree_isolation>"
	}
	suffix := ""
	if extras.MemoryBlock != "" {
		suffix = "\n\n" + extras.MemoryBlock
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
		return identity + "\n\n" + subAgentBridge + "\n\n" + tag + envBlock + worktree + custom + suffix
	}
	header := "You are a pi coding agent sub-agent.\nYou have been invoked to handle a specific task autonomously.\n\n" + envBlock
	return tag + header + worktree + "\n\n" + c.SystemPrompt + suffix
}
