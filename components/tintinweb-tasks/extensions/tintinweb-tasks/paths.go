package tintinweb_tasks

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Where session task files live. upstream: task-paths.ts. The `session` scope keeps them in the workspace;
// `session-global` keeps them under the agent directory, beside PiG's own per-workspace session logs. The
// choice only decides where a new file is created: a session that already has a workspace file keeps it.

var separators = regexp.MustCompile(`[/\\:]`)

// projectKey names a workspace the way pi names it in its own session logs. upstream: task-paths.ts:25-27.
func projectKey(cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	if len(abs) > 0 && (abs[0] == '/' || abs[0] == '\\') { // the first leading separator only
		abs = abs[1:]
	}
	return "--" + separators.ReplaceAllString(abs, "-") + "--"
}

func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[1:])
	}
	return p
}

// agentDir is PiG's writable agent directory: PIG_CODING_AGENT_DIR, else <PIG_HOME or ~/.pig>/agent. With
// PIG_USE_PI_DIRS=1 it is Pi's: PI_CODING_AGENT_DIR, else ~/.pi/agent. PiG's own rule (divergence D2), not
// the fixed Pi directory of the original's getAgentDir().
func agentDir() string {
	if os.Getenv("PIG_USE_PI_DIRS") == "1" {
		if v := os.Getenv("PI_CODING_AGENT_DIR"); v != "" {
			return expandTilde(v)
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".pi", "agent")
	}
	if v := os.Getenv("PIG_CODING_AGENT_DIR"); v != "" {
		return expandTilde(v)
	}
	if v := os.Getenv("PIG_HOME"); v != "" {
		return filepath.Join(expandTilde(v), "agent")
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(expandTilde(v), "pig", "agent")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pig", "agent")
}

// globalSessionTasksDir is where session-global collects one workspace's session files, resolved per call
// (the environment is read each time). upstream: task-paths.ts:31-33.
func globalSessionTasksDir(cwd string) string {
	return filepath.Join(agentDir(), "tasks", "sessions", projectKey(cwd))
}

// workspaceSessionTaskFile is the in-workspace location, unchanged since session scope was introduced.
func workspaceSessionTaskFile(cwd, sessionID string) string {
	return filepath.Join(cwd, ".pi", "tasks", "tasks-"+sessionID+".json")
}

// sessionTaskFile is the file backing one persisted session. Under session-global the workspace is still
// consulted first, which is why no migration is needed. upstream: task-paths.ts:46-50.
func sessionTaskFile(cwd, sessionID, scope string) string {
	inWorkspace := workspaceSessionTaskFile(cwd, sessionID)
	if scope != "session-global" {
		return inWorkspace
	}
	if _, err := os.Stat(inWorkspace); err == nil {
		return inWorkspace
	}
	return filepath.Join(globalSessionTasksDir(cwd), "tasks-"+sessionID+".json")
}

// reclaimGlobalSessionTasksDir removes a workspace's global session directory once it holds nothing. Only
// the global tree is reclaimed: `<workspace>/.pi/tasks/` is left alone. upstream: task-paths.ts:59-61.
func reclaimGlobalSessionTasksDir(cwd string) { _ = os.Remove(globalSessionTasksDir(cwd)) }
