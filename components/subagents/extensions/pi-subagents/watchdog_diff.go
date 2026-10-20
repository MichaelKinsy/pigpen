package pi_subagents

import (
	"regexp"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The watchdog_diff tool (watchdog/diff-tool.ts): the repository diff a reviewer reads, read-only.

const (
	watchdogDiffToolName = "watchdog_diff"
	watchdogDiffMaxChars = 24_000
	wdMaxUntrackedFiles  = 50
	wdDiffMaxBuffer      = 16 * 1024 * 1024
)

const watchdogDiffUnavailableBaseline = "watchdog_diff unavailable: this cwd has no valid Git HEAD baseline. No diff can be shown; inspect files with independent read-only tools instead."

type watchdogDiffBaseline struct{ Root, Ref string }

type watchdogDiffParams struct {
	Path string
	Stat bool
}

type watchdogDiffTool struct {
	Name        string
	Description string
	baseline    *watchdogDiffBaseline
	workingTree bool
}

type wdGitResult struct {
	ok             bool
	stdout, stderr string
}

func wdRunGit(root string, args ...string) wdGitResult { panic("not implemented") }

// captureWatchdogDiffBaseline records HEAD at session start, so later child commits still show in the diff.
func captureWatchdogDiffBaseline(cwd string) *watchdogDiffBaseline { panic("not implemented") }

var wdPathSeparators = regexp.MustCompile(`[\\/]`)

func wdValidateDiffPath(value string) (string, error) { panic("not implemented") }

func wdBoundDiff(text string) string { panic("not implemented") }

// createWatchdogDiffTool: in a shared cwd (workingTreeAtLaunch), changes already pending when the session started also appear.
func createWatchdogDiffTool(baseline *watchdogDiffBaseline, workingTreeAtLaunch bool) *watchdogDiffTool {
	panic("not implemented")
}

func (t *watchdogDiffTool) execute(params watchdogDiffParams) (string, error) {
	panic("not implemented")
}

// watchdogDiffToolDefinition is the tool as the SDK registers it.
func watchdogDiffToolDefinition(tool *watchdogDiffTool) sdk.ToolDefinition { panic("not implemented") }

// registerWatchdogDiffTool registers watchdog_diff on a child extension (the reviewer launch baseline of cwd).
func registerWatchdogDiffTool(e *sdk.Extension, cwd string) { panic("not implemented") }
