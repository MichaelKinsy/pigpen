package pi_subagents

import (
	"os"
	"path/filepath"
)

// Port of the path helpers of src/shared/settings.ts, src/shared/utils.ts (resolveChildCwd) and
// src/runs/background/index-segment.ts.

// resolveChainPath expands ~, keeps absolute paths and puts a relative one under chainDir.
func resolveChainPath(p, chainDir string) string {
	expanded := expandHomePath(p)
	if filepath.IsAbs(expanded) {
		return expanded
	}
	return filepath.Join(chainDir, expanded)
}

// resolveExistingReadInstructionPaths keeps the reads that exist (checked under existenceCwd) as paths under instructionCwd.
func resolveExistingReadInstructionPaths(reads []string, instructionCwd, existenceCwd string) []string {
	var out []string
	for _, r := range reads {
		if _, err := os.Stat(resolveChainPath(r, existenceCwd)); err == nil {
			out = append(out, resolveChainPath(r, instructionCwd))
		}
	}
	return out
}

func resolveExistingReadPaths(reads []string, cwd string) []string {
	return resolveExistingReadInstructionPaths(reads, cwd, cwd)
}

// resolveChildCwd is the base cwd, or the child's cwd (relative ones resolved from the base).
func resolveChildCwd(baseCwd, childCwd string) string {
	if childCwd == "" {
		return baseCwd
	}
	if filepath.IsAbs(childCwd) {
		return childCwd
	}
	return nodeResolve(baseCwd, childCwd)
}
