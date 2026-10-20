package pi_subagents

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

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

func wdRunGit(root string, args ...string) wdGitResult {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return wdGitResult{ok: err == nil && out.Len() <= wdDiffMaxBuffer, stdout: out.String(), stderr: jsTrim(errOut.String())}
}

// captureWatchdogDiffBaseline records HEAD at session start, so later child commits still show in the diff.
func captureWatchdogDiffBaseline(cwd string) *watchdogDiffBaseline {
	top := wdRunGit(cwd, "rev-parse", "--show-toplevel")
	if !top.ok {
		return nil
	}
	head := wdRunGit(cwd, "rev-parse", "HEAD")
	if !head.ok {
		return nil
	}
	root, ref := jsTrim(top.stdout), jsTrim(head.stdout)
	if root == "" || ref == "" {
		return nil
	}
	return &watchdogDiffBaseline{Root: root, Ref: ref}
}

var wdPathSeparators = regexp.MustCompile(`[\\/]`)

func wdValidateDiffPath(value string) (string, error) {
	trimmed := jsTrim(value)
	if trimmed == "" {
		return "", nil
	}
	if strings.HasPrefix(trimmed, "-") {
		return "", errors.New("watchdog_diff path must not start with '-'.")
	}
	if filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) || regexp.MustCompile(`^[A-Za-z]:[\\/]`).MatchString(trimmed) {
		return "", errors.New("watchdog_diff path must be relative to the repo root.")
	}
	for _, seg := range wdPathSeparators.Split(trimmed, -1) {
		if seg == ".." {
			return "", errors.New("watchdog_diff path must not contain '..'.")
		}
	}
	return trimmed, nil
}

func wdBoundDiff(text string) string {
	n := wdLen(text)
	if n <= watchdogDiffMaxChars {
		return text
	}
	marker := "\n\n[... " + itoa(n-watchdogDiffMaxChars) + " characters omitted; call again with a narrower path ...]"
	return wdSlice(text, 0, watchdogDiffMaxChars-wdLen(marker)) + marker
}

// createWatchdogDiffTool: in a shared cwd (workingTreeAtLaunch), changes already pending when the session started also appear.
func createWatchdogDiffTool(baseline *watchdogDiffBaseline, workingTreeAtLaunch bool) *watchdogDiffTool {
	description := "Show the repository diff since the review baseline, plus untracked file paths. Optional path narrows it; stat:true returns per-file counts only."
	if baseline == nil {
		description = "Report that a Git HEAD diff baseline is unavailable in this cwd. No diff is generated."
	} else if workingTreeAtLaunch {
		description = "Show the current staged and unstaged working-tree delta against reviewer-launch HEAD, plus untracked file paths. Committed ranges are not included. Optional path narrows it; stat:true returns per-file counts only."
	}
	return &watchdogDiffTool{Name: watchdogDiffToolName, Description: description, baseline: baseline, workingTree: workingTreeAtLaunch}
}

func (t *watchdogDiffTool) execute(params watchdogDiffParams) (string, error) {
	if t.baseline == nil {
		return watchdogDiffUnavailableBaseline, nil
	}
	pathFilter, err := wdValidateDiffPath(params.Path)
	if err != nil {
		return "", err
	}
	diffArgs := []string{"diff", "--no-color", "--no-ext-diff"}
	if params.Stat {
		diffArgs = append(diffArgs, "--stat")
	}
	diffArgs = append(diffArgs, t.baseline.Ref, "--")
	lsArgs := []string{"ls-files", "--others", "--exclude-standard", "-z", "--"}
	if pathFilter != "" {
		diffArgs, lsArgs = append(diffArgs, pathFilter), append(lsArgs, pathFilter)
	}
	diff := wdRunGit(t.baseline.Root, diffArgs...)
	if !diff.ok {
		reason := diff.stderr
		if reason == "" {
			reason = "unknown error"
		}
		return "", errors.New("git diff failed: " + reason)
	}
	untrackedResult := wdRunGit(t.baseline.Root, lsArgs...)
	if t.workingTree {
		head := wdRunGit(t.baseline.Root, "rev-parse", "HEAD")
		if !head.ok || jsTrim(head.stdout) != t.baseline.Ref {
			return "Reviewer-launch HEAD changed or is unavailable. Committed changes are unsupported; relaunch the reviewer or supply a diff artifact.", nil
		}
	}
	var untracked []string
	if untrackedResult.ok {
		for _, p := range strings.Split(untrackedResult.stdout, "\x00") {
			if p != "" {
				untracked = append(untracked, p)
			}
		}
	}
	sections := []string{strings.TrimRight(diff.stdout, " \t\r\n\v\f")}
	if len(untracked) > 0 {
		shown := untracked
		if len(shown) > wdMaxUntrackedFiles {
			shown = shown[:wdMaxUntrackedFiles]
		}
		lines := []string{"Untracked files (use read to inspect):"}
		for _, f := range shown {
			lines = append(lines, " "+f)
		}
		sections = append(sections, strings.Join(lines, "\n"))
		if len(untracked) > len(shown) {
			sections = append(sections, "... "+itoa(len(untracked)-len(shown))+" more untracked files")
		}
	}
	var kept []string
	for _, s := range sections {
		if s != "" {
			kept = append(kept, s)
		}
	}
	text := wdBoundDiff(strings.Join(kept, "\n\n"))
	if text == "" {
		if t.workingTree {
			text = "No working-tree changes against reviewer-launch HEAD " + wdSlice(t.baseline.Ref, 0, 12) + ". Committed changes are not included."
		} else {
			text = "No changes since baseline " + wdSlice(t.baseline.Ref, 0, 12) + "."
		}
	}
	return text, nil
}

// watchdogDiffToolDefinition is the tool as the SDK registers it.
func watchdogDiffToolDefinition(tool *watchdogDiffTool) sdk.ToolDefinition {
	return sdk.ToolDefinition{
		Name: tool.Name, Label: "Watchdog diff", Description: tool.Description,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Restrict the diff to one file or directory, relative to the repo root."},
				"stat": map[string]any{"type": "boolean", "description": "Return per-file change counts instead of the full diff."},
			},
			"additionalProperties": false,
		},
		Execute: func(_ sdk.Context, params map[string]any) (any, error) {
			p := watchdogDiffParams{}
			p.Path, _ = params["path"].(string)
			p.Stat, _ = params["stat"].(bool)
			text, err := tool.execute(p)
			if err != nil {
				return nil, sdk.NewToolError(err.Error())
			}
			return sdk.ToolResult{Content: text, Details: map[string]any{"chars": wdLen(text)}}, nil
		},
	}
}

// registerWatchdogDiffTool registers watchdog_diff on a child extension (the reviewer launch baseline of cwd).
func registerWatchdogDiffTool(e *sdk.Extension, cwd string) {
	e.RegisterTool(watchdogDiffToolDefinition(createWatchdogDiffTool(captureWatchdogDiffBaseline(cwd), true)))
}
