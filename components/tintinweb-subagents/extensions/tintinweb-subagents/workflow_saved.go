package tintinweb_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Resolving `SubagentWorkflow({ name })` to a script on disk. A saved workflow is a plain .js file whose contents are
// exactly what `script` would have carried; extractMeta still runs over it at the call site. Roots mirror the agent
// loader: project .pi is the authority, the shared .agents workspace an extra read location, the user's agent dir the
// fallback; the first hit wins. Symlinks are rejected and the name is whitelisted before it is ever joined to a path.
// upstream: src/workflow/saved.ts.

// Extension a saved workflow file carries. upstream: WORKFLOW_EXTENSION.
const workflowExtension = ".js"

// maxWorkflowScriptLength matches the `script` field's maxLength in the tool schema. upstream: runtime.ts MAX_SCRIPT_LENGTH.
const maxWorkflowScriptLength = 524_288

var workflowSafeNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// wfIsUnsafeName reports a name that must never be joined to a path. upstream: memory.ts isUnsafeName.
func wfIsUnsafeName(name string) bool {
	if name == "" || wfUTF16Len(name) > 128 {
		return true
	}
	return !workflowSafeNamePattern.MatchString(name)
}

// wfIsSymlink reports a path that is a symlink (defense against symlink attacks). upstream: memory.ts isSymlink.
func wfIsSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// wfSafeReadFile reads a file, rejecting symlinks; ok is false when it does not exist, is a symlink or cannot be
// read. upstream: memory.ts safeReadFile.
func wfSafeReadFile(path string) (string, bool) {
	if _, err := os.Stat(path); err != nil || wfIsSymlink(path) {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// savedWorkflowRoots are the roots a name is looked up in, highest priority first. upstream: savedWorkflowRoots.
func savedWorkflowRoots(cwd string) []string {
	return []string{filepath.Join(cwd, ".pi", "workflows"), filepath.Join(cwd, ".agents", "workflows"), filepath.Join(agentDir(), "workflows")}
}

// savedWorkflow is a resolved script: OK with Script and Path, or Message.
type savedWorkflow struct {
	OK      bool
	Script  string
	Path    string
	Message string
}

// readSavedWorkflow reads the saved workflow called name. A failure carries the roots that were searched and the
// names that do exist, so a model that guessed the name can correct itself. upstream: readSavedWorkflow.
func readSavedWorkflow(name, cwd string) savedWorkflow {
	trimmed := strings.TrimSpace(name)
	if wfIsUnsafeName(trimmed) {
		return savedWorkflow{Message: fmt.Sprintf(`"%s" is not a usable workflow name. Use letters, digits, dots, hyphens and underscores only `+
			"— a path is what `scriptPath` is for.", name)}
	}
	roots := savedWorkflowRoots(cwd)
	for _, root := range roots {
		if wfIsSymlink(root) {
			continue // reject a symlinked root entirely, as the skill loader does
		}
		path := filepath.Join(root, trimmed+workflowExtension)
		script, ok := wfSafeReadFile(path)
		if !ok {
			continue
		}
		// Found the file, so stop looking: a shadowing name that is not a workflow is worth reporting.
		if !hasMetaDeclaration(script) {
			return savedWorkflow{Message: fmt.Sprintf(`"%s" is not a workflow script — it has no `+"`export const meta = { name, description }`"+
				" declaration. Nothing was run.", path)}
		}
		return savedWorkflow{OK: true, Script: script, Path: path}
	}
	known := listSavedWorkflows(cwd)
	tail := "Save one as `<name>.js` in one of those directories, or pass `script`/`scriptPath` instead."
	if len(known) > 0 {
		tail = "Available: " + strings.Join(known, ", ") + "."
	}
	return savedWorkflow{Message: fmt.Sprintf(`No saved workflow named "%s". Looked in: %s. %s`, trimmed, strings.Join(roots, ", "), tail)}
}

// workflowRef is how a script names another workflow: a saved name, or a path to a file.
type workflowRef struct{ Name, ScriptPath string }

// resolveWorkflowSource resolves a reference — a saved name, or a path — to source: the one place that decides what a
// reference means, so the tool's name / scriptPath parameters and a script's nested workflow() cannot drift apart.
// upstream: resolveWorkflowSource.
func resolveWorkflowSource(ref workflowRef, cwd string) savedWorkflow {
	if path := strings.TrimSpace(ref.ScriptPath); path != "" {
		resolved := path
		if !filepath.IsAbs(path) {
			resolved = filepath.Join(cwd, path)
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return savedWorkflow{Message: fmt.Sprintf(`Could not read workflow script "%s": %s`, resolved, nodeFSError(err))}
		}
		return savedWorkflow{OK: true, Script: string(data), Path: resolved}
	}
	if name := strings.TrimSpace(ref.Name); name != "" {
		return readSavedWorkflow(name, cwd)
	}
	return savedWorkflow{Message: "A workflow reference needs a `name` or a `scriptPath`."}
}

// nodeFSError words a file error as Node does ("ENOENT: no such file or directory, open '<path>'").
func nodeFSError(err error) string {
	var pe *os.PathError
	if e, ok := err.(*os.PathError); ok {
		pe = e
	}
	if pe == nil {
		return err.Error()
	}
	switch {
	case os.IsNotExist(pe.Err):
		return fmt.Sprintf("ENOENT: no such file or directory, %s '%s'", pe.Op, pe.Path)
	case os.IsPermission(pe.Err):
		return fmt.Sprintf("EACCES: permission denied, %s '%s'", pe.Op, pe.Path)
	}
	if strings.Contains(pe.Err.Error(), "is a directory") {
		return "EISDIR: illegal operation on a directory, read"
	}
	return err.Error()
}

// listSavedWorkflows is every saved workflow name, de-duplicated across roots and sorted. upstream: listSavedWorkflows.
func listSavedWorkflows(cwd string) []string {
	names := map[string]bool{}
	for _, root := range savedWorkflowRoots(cwd) {
		if _, err := os.Stat(root); err != nil || wfIsSymlink(root) {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue // an unreadable root is not worth failing a lookup over
		}
		for _, entry := range entries {
			file := entry.Name()
			if !strings.HasSuffix(file, workflowExtension) {
				continue
			}
			name := strings.TrimSuffix(file, workflowExtension)
			if wfIsUnsafeName(name) {
				continue
			}
			if isWorkflowFile(filepath.Join(root, file)) {
				names[name] = true
			}
		}
	}
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// isWorkflowFile reports whether the file is a workflow rather than some other script; size-guarded before reading.
func isWorkflowFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxWorkflowScriptLength {
		return false
	}
	source, ok := wfSafeReadFile(path)
	return ok && hasMetaDeclaration(source)
}

type workflowScriptParams struct{ Script, ScriptPath, Name string }

type resolvedWorkflowScript struct {
	OK         bool
	Script     string
	ScriptPath string // set when the source came from a file
	Message    string
}

// resolveWorkflowScript resolves which source a SubagentWorkflow call runs: scriptPath wins over script, which wins
// over name (Claude Code's order), and at least one is required. upstream: resolveWorkflowScript.
func resolveWorkflowScript(p workflowScriptParams, cwd string) resolvedWorkflowScript {
	if path := strings.TrimSpace(p.ScriptPath); path != "" {
		r := resolveWorkflowSource(workflowRef{ScriptPath: path}, cwd)
		if r.OK {
			return resolvedWorkflowScript{OK: true, Script: r.Script, ScriptPath: r.Path}
		}
		return resolvedWorkflowScript{Message: r.Message}
	}
	if strings.TrimSpace(p.Script) != "" {
		return resolvedWorkflowScript{OK: true, Script: p.Script}
	}
	// A saved workflow is the same source by another route, so it reports its file as scriptPath: the "edit the file
	// and re-run" loop then works on a named workflow.
	if name := strings.TrimSpace(p.Name); name != "" {
		r := resolveWorkflowSource(workflowRef{Name: name}, cwd)
		if r.OK {
			return resolvedWorkflowScript{OK: true, Script: r.Script, ScriptPath: r.Path}
		}
		return resolvedWorkflowScript{Message: r.Message}
	}
	known := listSavedWorkflows(cwd)
	msg := "Provide `script` (inline source), `scriptPath` (a file to read), or `name` (a saved workflow). " +
		"`scriptPath` takes precedence, then `script`, then `name`."
	if len(known) > 0 {
		msg += " Saved workflows: " + strings.Join(known, ", ") + "."
	}
	return resolvedWorkflowScript{Message: msg}
}
