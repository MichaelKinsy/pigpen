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

// buildAgentPrompt is an agent's whole system prompt. In "append" mode it is the parent's system prompt (or a
// generic base) with the agent's instructions added; in "replace" mode the agent's own. The text is the original's.
// upstream: src/prompts.ts buildAgentPrompt (without the memory, skill, worktree and workflow extras).
func buildAgentPrompt(c *agentConfig, cwd string, env envInfo, parentSystemPrompt string) string {
	tag := "<active_agent name=\"" + c.Name + "\"/>\n\n"
	repo := "Not a git repository"
	if env.IsGitRepo {
		repo = "Git repository: yes\nBranch: " + env.Branch
	}
	envBlock := "# Environment\nWorking directory: " + cwd + "\n" + repo + "\nPlatform: " + env.Platform
	if c.PromptMode == "append" {
		identity := parentSystemPrompt
		if identity == "" {
			identity = genericBase
		}
		custom := ""
		if strings.TrimSpace(c.SystemPrompt) != "" {
			custom = "\n\n<agent_instructions>\n" + c.SystemPrompt + "\n</agent_instructions>"
		}
		return identity + "\n\n" + subAgentBridge + "\n\n" + tag + envBlock + custom
	}
	header := "You are a pi coding agent sub-agent.\nYou have been invoked to handle a specific task autonomously.\n\n" + envBlock
	return tag + header + "\n\n" + c.SystemPrompt
}
