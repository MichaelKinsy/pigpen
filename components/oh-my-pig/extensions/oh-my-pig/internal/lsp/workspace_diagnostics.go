package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// upstream: packages/coding-agent/src/lsp/workspace-diagnostics.ts

type ProjectType struct {
	Type        string
	Command     []string
	Description string
}

const maxConcurrentCheckers = 2

func goWorkspaceBuildPattern(diskPath string) string {
	trimmed := strings.TrimSpace(diskPath)
	if trimmed == "" {
		return ""
	}
	isAbs := filepath.IsAbs(trimmed) || (len(trimmed) >= 2 && isASCIIAlpha(trimmed[0]) && trimmed[1] == ':')
	norm := strings.TrimRight(strings.ReplaceAll(trimmed, "\\", "/"), "/")
	if norm == "" {
		norm = "."
	}
	if norm == "." {
		return "./..."
	}
	if strings.HasSuffix(norm, "/...") {
		return norm
	}
	if isAbs || strings.HasPrefix(norm, "./") || strings.HasPrefix(norm, "../") {
		return norm + "/..."
	}
	return "./" + norm + "/..."
}

func parseGoWorkspaceBuildPatterns(output string) []string {
	var parsed struct {
		Use []struct {
			DiskPath string `json:"DiskPath"`
		} `json:"Use"`
	}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	var patterns []string
	for _, entry := range parsed.Use {
		if pat := goWorkspaceBuildPattern(entry.DiskPath); pat != "" {
			if _, ok := seen[pat]; !ok {
				seen[pat] = struct{}{}
				patterns = append(patterns, pat)
			}
		}
	}
	return patterns
}

func resolveGoWorkspaceDiagnosticsCommand(ctx context.Context, cwd string) []string {
	fallback := []string{"go", "build", "./..."}
	cmd := exec.CommandContext(ctx, "go", "work", "edit", "-json")
	cmd.Dir = cwd
	configureCmdCancel(cmd)
	out, err := cmd.Output()
	if err != nil {
		return fallback
	}
	patterns := parseGoWorkspaceBuildPatterns(string(out))
	if len(patterns) == 0 {
		return fallback
	}
	return append([]string{"go", "build"}, patterns...)
}

// detectProjectTypes collects every toolchain present at cwd in omp's priority order.
// upstream: packages/coding-agent/src/lsp/workspace-diagnostics.ts:detectProjectTypes
func detectProjectTypes(ctx context.Context, cwd string) []ProjectType {
	marker := func(name string) bool {
		_, err := os.Stat(filepath.Join(cwd, name))
		return err == nil
	}
	var detected []ProjectType
	if marker("Cargo.toml") {
		detected = append(detected, ProjectType{
			Type:        "rust",
			Command:     []string{"cargo", "check", "--message-format=short"},
			Description: "Rust (cargo check)",
		})
	}
	if marker("tsconfig.json") {
		detected = append(detected, ProjectType{
			Type:        "typescript",
			Command:     []string{"npx", "tsc", "--noEmit"},
			Description: "TypeScript (tsc --noEmit)",
		})
	}
	if marker("go.work") {
		detected = append(detected, ProjectType{
			Type:        "go",
			Command:     resolveGoWorkspaceDiagnosticsCommand(ctx, cwd),
			Description: "Go workspace (go build)",
		})
	} else if marker("go.mod") {
		detected = append(detected, ProjectType{
			Type:        "go",
			Command:     []string{"go", "build", "./..."},
			Description: "Go (go build)",
		})
	}
	if marker("pyproject.toml") || marker("pyrightconfig.json") {
		detected = append(detected, ProjectType{
			Type:        "python",
			Command:     []string{"pyright"},
			Description: "Python (pyright)",
		})
	}
	if len(detected) == 0 {
		return []ProjectType{{Type: "unknown", Description: "Unknown project type"}}
	}
	return detected
}

func interpretEmptyDiagnosticsResult(exitCode int, command []string) string {
	if exitCode == 0 {
		return "No issues found"
	}
	return fmt.Sprintf("Failed to run %s: the checker exited with code %d without reporting anything, so the workspace was not verified",
		strings.Join(command, " "), exitCode)
}

func runProjectDiagnostics(ctx context.Context, cwd string, pt ProjectType) (string, error) {
	if len(pt.Command) == 0 {
		return "Cannot detect project type. Supported: Rust (Cargo.toml), TypeScript (tsconfig.json), Go (go.work/go.mod), Python (pyproject.toml)", nil
	}
	cmd := exec.CommandContext(ctx, pt.Command[0], pt.Command[1:]...)
	cmd.Dir = cwd
	configureCmdCancel(cmd)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	combined := strings.TrimSpace(outBuf.String() + errBuf.String())
	if combined == "" {
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return interpretEmptyDiagnosticsResult(exitErr.ExitCode(), pt.Command), nil
			}
			return fmt.Sprintf("Failed to run %s: %v", strings.Join(pt.Command, " "), err), nil
		}
		return "No issues found", nil
	}
	lines := strings.Split(combined, "\n")
	if len(lines) > 50 {
		return fmt.Sprintf("%s\n[…%dln elided…]", strings.Join(lines[:50], "\n"), len(lines)-50), nil
	}
	return combined, nil
}

// runWorkspaceDiagnostics executes project-level build/typecheckers for `lsp diagnostics *`.
// upstream: packages/coding-agent/src/lsp/workspace-diagnostics.ts:runWorkspaceDiagnostics
func runWorkspaceDiagnostics(ctx context.Context, cwd string) (output string, description string, err error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	types := detectProjectTypes(ctx, cwd)
	if len(types) == 1 {
		description = types[0].Description
	} else {
		descs := make([]string, len(types))
		for i, t := range types {
			descs[i] = t.Description
		}
		description = strings.Join(descs, " + ")
	}

	type section struct {
		description string
		output      string
		err         error
	}
	sections := make([]section, len(types))
	sem := make(chan struct{}, maxConcurrentCheckers)
	var wg sync.WaitGroup
	for i, pt := range types {
		wg.Add(1)
		go func(idx int, t ProjectType) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out, runErr := runProjectDiagnostics(ctx, cwd, t)
			sections[idx] = section{description: t.Description, output: out, err: runErr}
		}(i, pt)
	}
	wg.Wait()

	for _, sec := range sections {
		if sec.err != nil {
			return "", "", sec.err
		}
	}
	if len(sections) == 1 {
		return sections[0].output, description, nil
	}
	parts := make([]string, len(sections))
	for i, sec := range sections {
		parts[i] = fmt.Sprintf("=== %s ===\n%s", sec.description, sec.output)
	}
	return strings.Join(parts, "\n\n"), description, nil
}
