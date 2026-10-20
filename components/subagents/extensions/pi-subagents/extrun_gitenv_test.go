package pi_subagents

import (
	"sort"
	"testing"
)

// Twin of test/unit/git-environment.test.ts: omitGitRoutingEnv (async_run.go) takes an environment as KEY=value entries.

func TestGitEnvironmentTwins(t *testing.T) {
	const f = "git-environment"
	routing := []string{
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=/repo/objects", "GIT_COMMON_DIR=/repo/common", "GIT_CONFIG=/repo/config", "GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.worktree", "GIT_CONFIG_PARAMETERS='core.worktree=/repo'", "GIT_CONFIG_VALUE_0=/repo", "GIT_DIR=/repo/.git",
		"GIT_GRAFT_FILE=/repo/grafts", "GIT_IMPLICIT_WORK_TREE=1", "GIT_INDEX_FILE=/repo/index", "GIT_NAMESPACE=tenant", "GIT_NO_REPLACE_OBJECTS=1",
		"GIT_OBJECT_DIRECTORY=/repo/object-dir", "GIT_PREFIX=src/", "GIT_REPLACE_REF_BASE=refs/replace/", "GIT_SHALLOW_FILE=/repo/shallow", "GIT_WORK_TREE=/repo",
	}
	tw(t, f, "removes inherited repository routing and keeps unrelated values", func(t *testing.T) {
		got := omitGitRoutingEnv(append(append([]string{}, routing...), "KEEP_ME=yes"))
		equal(t, got, []string{"KEEP_ME=yes"}, "environment")
	})
	tw(t, f, "removes every indexed Git config entry", func(t *testing.T) {
		got := omitGitRoutingEnv([]string{"GIT_CONFIG_KEY_17=user.name", "GIT_CONFIG_VALUE_17=Test", "KEEP_ME=yes"})
		equal(t, got, []string{"KEEP_ME=yes"}, "environment")
	})
	tw(t, f, "matches names case-insensitively, as Windows and Git for Windows do", func(t *testing.T) {
		got := omitGitRoutingEnv([]string{"git_dir=/repo/.git", "Git_Work_Tree=/repo", "git_config_key_0=core.bare", "KEEP_ME=yes"})
		sort.Strings(got)
		equal(t, got, []string{"KEEP_ME=yes"}, "environment")
	})
}
