//go:build unix

package pi_subagents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Port of test/support/cursor-smoke-inputs.ts: the operator-supplied roots of the live Cursor smoke run. Test support in the
// original and here; the live smoke test needs a real Cursor Agent and is not part of the twins.

type cursorSmokeInputsResult struct {
	Workspace, StateRoot, CanaryPath, PromptDirectory string
}

func cursorSmokeExistingDirectory(value, name string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required.", name)
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("%s must point to an existing directory.", name)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("%s must point to an existing directory.", name)
	}
	if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s must point to an existing directory.", name)
	}
	return resolved, nil
}

func cursorSmokePathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && (rel == "" || rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)))
}

func cursorSmokeInputs(env map[string]string) (cursorSmokeInputsResult, error) {
	var none cursorSmokeInputsResult
	if env["PI_SUBAGENTS_CURSOR_SMOKE_DISPOSABLE"] != "1" {
		return none, errors.New("PI_SUBAGENTS_CURSOR_SMOKE_DISPOSABLE=1 is required to attest that the operator-managed workspace is disposable.")
	}
	workspace, err := cursorSmokeExistingDirectory(env["PI_SUBAGENTS_CURSOR_SMOKE_WORKSPACE"], "PI_SUBAGENTS_CURSOR_SMOKE_WORKSPACE")
	if err != nil {
		return none, err
	}
	stateRoot, err := cursorSmokeExistingDirectory(env["PI_SUBAGENTS_CURSOR_SMOKE_STATE_ROOT"], "PI_SUBAGENTS_CURSOR_SMOKE_STATE_ROOT")
	if err != nil {
		return none, err
	}
	if cursorSmokePathWithin(workspace, stateRoot) || cursorSmokePathWithin(stateRoot, workspace) {
		return none, errors.New("Cursor smoke workspace and state root must be separate directories so the production --add-dir launch shape is exercised.")
	}
	canaryPath := filepath.Join(workspace, "pi-subagents-cursor-write-canary.txt")
	promptDirectory := filepath.Join(stateRoot, "external-0.cursor-prompt")
	if _, err := os.Lstat(canaryPath); err == nil {
		return none, fmt.Errorf("Cursor smoke canary path must not exist before launch: %s", canaryPath)
	}
	info, err := os.Lstat(promptDirectory)
	if err != nil {
		return none, fmt.Errorf("Cursor smoke prompt directory must be an existing operator-trusted directory: %s", promptDirectory)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return none, fmt.Errorf("Cursor smoke prompt directory must be an existing directory, not a symlink: %s", promptDirectory)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return none, fmt.Errorf("Cursor smoke prompt directory must be owned by the current operator: %s", promptDirectory)
	}
	entries, err := os.ReadDir(promptDirectory)
	if err != nil {
		return none, err
	}
	if len(entries) > 0 {
		return none, fmt.Errorf("Cursor smoke prompt directory must be empty before launch: %s", promptDirectory)
	}
	return cursorSmokeInputsResult{Workspace: workspace, StateRoot: stateRoot, CanaryPath: canaryPath, PromptDirectory: promptDirectory}, nil
}
