// Package dirty_repo_guard is a Go port of Pi's dirty-repo-guard example
// extension (packages/coding-agent/examples/extensions/dirty-repo-guard.ts,
// Pi v0.87.1): it blocks a session switch or fork while the git working tree has
// uncommitted changes, unless the user allows it.
package dirty_repo_guard

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	allowOption  = "Yes, proceed anyway"
	refuseOption = "No, let me commit first"
)

// Extension returns the guard.
func Extension() *sdk.Extension {
	e := sdk.New("dirty-repo-guard")

	// pi.on("session_before_switch", ...): reason "new" reads "new session",
	// every other reason (resume, ...) reads "switch session".
	e.OnEvent(sdk.EventSessionBeforeSwitch, func(ctx sdk.Context, data map[string]any) (any, error) {
		action := "switch session"
		if reason, _ := data["reason"].(string); reason == "new" {
			action = "new session"
		}
		return checkDirtyRepo(ctx, action)
	})

	// pi.on("session_before_fork", ...)
	e.OnEvent(sdk.EventSessionBeforeFork, func(ctx sdk.Context, _ map[string]any) (any, error) {
		return checkDirtyRepo(ctx, "fork")
	})
	return e
}

// checkDirtyRepo returns {cancel: true} to block the action and nil to allow it.
//
// Pi's pi.exec never rejects: a command that cannot start resolves with code 1.
// PiG's host reports the same through the result, so a non-zero code is the
// "not a git repository" branch. An error from Exec is a host or transport
// failure and is returned, so the host shows it instead of the guard guessing.
func checkDirtyRepo(ctx sdk.Context, action string) (any, error) {
	res, err := ctx.Exec("git", []string{"status", "--porcelain"})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, fmt.Errorf("dirty-repo-guard: host returned no result for git status")
	}
	v, changed := decide(res.ExitCode, res.Stdout, ctx.HasUI())
	switch v {
	case allow:
		return nil, nil
	case cancel:
		// In non-interactive mode, block by default.
		return map[string]any{"cancel": true}, nil
	}
	choice, ok, err := ctx.Select(fmt.Sprintf("You have %d uncommitted file(s). %s anyway?", changed, action), []string{allowOption, refuseOption})
	if err != nil {
		return nil, err
	}
	// A dismissed dialog is `undefined` in Pi, which is not the allow option.
	if !ok || choice != allowOption {
		ctx.Notify("Commit your changes first", "warning")
		return map[string]any{"cancel": true}, nil
	}
	return nil, nil
}
