package filetools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type v4aOpType string

const (
	v4aAdd    v4aOpType = "add"
	v4aUpdate v4aOpType = "update"
	v4aDelete v4aOpType = "delete"
	v4aMove   v4aOpType = "move"
)

type v4aHunkLine struct {
	prefix  byte // ' ', '-', '+'
	content string
}

type v4aHunk struct {
	contextHint string
	lines       []v4aHunkLine
}

type v4aOperation struct {
	op      v4aOpType
	path    string
	newPath string
	hunks   []v4aHunk
}

var (
	reBeginPatch = regexp.MustCompile(`^\*\*\*\s*Begin\s+Patch\s*$`)
	reEndPatch   = regexp.MustCompile(`^\*\*\*\s*End\s+Patch\s*$`)
	reUpdateFile = regexp.MustCompile(`^\*\*\*\s*Update\s+File:\s*(.+)$`)
	reAddFile    = regexp.MustCompile(`^\*\*\*\s*Add\s+File:\s*(.+)$`)
	reDeleteFile = regexp.MustCompile(`^\*\*\*\s*Delete\s+File:\s*(.+)$`)
	reMoveFile   = regexp.MustCompile(`^\*\*\*\s*Move\s+File:\s*(.+?)\s*->\s*(.+)$`)
	reHint       = regexp.MustCompile(`^@@\s*(.+?)\s*@@`)
)

func (s *state) patchToolDef() sdk.ToolDefinition {
	return sdk.ToolDefinition{
		Name:  "patch",
		Label: "Patch File",
		Description: "Targeted find-and-replace edits or V4A multi-file patches. " +
			"Uses multi-strategy fuzzy matching (exact, line_trimmed, whitespace_normalized, indentation_flexible, escape_normalized, trimmed_boundary, unicode_normalized) and returns a unified diff.\n" +
			"REPLACE MODE (mode='replace', default): requires path, old_string, new_string.\n" +
			"PATCH MODE (mode='patch'): applies V4A multi-file patch (*** Begin Patch / *** Update File: / *** Add File: / *** Delete File: / *** Move File: / *** End Patch).",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"mode": map[string]any{
					"type":        "string",
					"enum":        []string{"replace", "patch"},
					"description": "Edit mode: 'replace' (default) or 'patch' (V4A multi-file diff).",
					"default":     "replace",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "File path to edit (required when mode='replace').",
				},
				"old_string": map[string]any{
					"type":        "string",
					"description": "Existing text to replace (required when mode='replace').",
				},
				"new_string": map[string]any{
					"type":        "string",
					"description": "Replacement text (required when mode='replace').",
				},
				"replace_all": map[string]any{
					"type":        "boolean",
					"description": "Replace all occurrences instead of requiring a unique match.",
					"default":     false,
				},
				"patch": map[string]any{
					"type":        "string",
					"description": "V4A format patch content (required when mode='patch').",
				},
			},
		},
		Execute: s.executePatch,
	}
}

func (s *state) executePatch(ctx sdk.Context, params map[string]any) (any, error) {
	mode, _ := paramString(params, "mode")
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		if p, ok := paramString(params, "patch"); ok && strings.TrimSpace(p) != "" {
			mode = "patch"
		} else {
			mode = "replace"
		}
	}

	switch mode {
	case "replace":
		return s.executePatchReplace(ctx, params)
	case "patch":
		return s.executePatchV4A(ctx, params)
	default:
		return errResult(fmt.Sprintf("patch: unknown mode %q (expected 'replace' or 'patch')", mode)), nil
	}
}

func (s *state) executePatchReplace(ctx sdk.Context, params map[string]any) (any, error) {
	rawPath, ok := paramString(params, "path")
	if !ok || strings.TrimSpace(rawPath) == "" {
		return errResult("patch: missing required field 'path' for replace mode"), nil
	}
	oldStr, ok := paramString(params, "old_string")
	if !ok {
		return errResult("patch: missing required field 'old_string' for replace mode"), nil
	}
	newStr, ok := paramString(params, "new_string")
	if !ok {
		return errResult("patch: missing required field 'new_string' for replace mode"), nil
	}
	replaceAll := paramBool(params, "replace_all", false)

	absPath, err := resolvePath(safeCwd(ctx), rawPath)
	if err != nil {
		return errResult(fmt.Sprintf("patch: %v", err)), nil
	}

	if err := s.checkWriteGuard(ctx, absPath); err != nil {
		return errResult(fmt.Sprintf("patch: %v", err)), nil
	}

	raw, err := os.ReadFile(absPath)
	if err != nil {
		return errResult(fmt.Sprintf("patch: %v", err)), nil
	}
	hasCRLF := strings.Contains(string(raw), "\r\n")
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")

	updated, count, strategy, err := fuzzyFindAndReplace(content, oldStr, newStr, replaceAll)
	if err != nil {
		return errResult(fmt.Sprintf("patch: %v", err)), nil
	}

	if strategy == "already_applied" {
		return okResult(fmt.Sprintf("No changes needed for %s (edit already applied).", absPath), map[string]any{
			"path":     absPath,
			"strategy": strategy,
			"matches":  0,
		}), nil
	}

	writeBytes := []byte(updated)
	if hasCRLF {
		writeBytes = []byte(strings.ReplaceAll(updated, "\n", "\r\n"))
	}
	if err := atomicWriteFile(absPath, writeBytes); err != nil {
		return errResult(fmt.Sprintf("patch: %v", err)), nil
	}
	if info, err := os.Stat(absPath); err == nil {
		s.recordRead(absPath, info, true)
	}

	diff := generateUnifiedDiff(rawPath, content, updated)
	return okResult(
		fmt.Sprintf("Patched %s (%d replacement(s), strategy: %s)\n%s", absPath, count, strategy, diff),
		map[string]any{
			"path":     absPath,
			"matches":  count,
			"strategy": strategy,
			"diff":     diff,
		},
	), nil
}

type v4aVFSEntry struct {
	exists  bool
	content string
	hasCRLF bool
}

type v4aPlannedOp struct {
	op       v4aOpType
	absPath  string
	dstPath  string
	rawPath  string
	newPath  string
	newBytes []byte
	diff     string
}

func pathExistsOnDisk(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (s *state) executePatchV4A(ctx sdk.Context, params map[string]any) (any, error) {
	patchText, ok := paramString(params, "patch")
	if !ok || strings.TrimSpace(patchText) == "" {
		return errResult("patch: missing required field 'patch' for patch mode"), nil
	}

	ops, err := parseV4APatch(patchText)
	if err != nil {
		return errResult(fmt.Sprintf("patch: %v", err)), nil
	}
	if len(ops) == 0 {
		return errResult("patch: no operations found in V4A patch"), nil
	}

	cwd := safeCwd(ctx)
	vfs := make(map[string]v4aVFSEntry, len(ops))

	vfsExists := func(path string) (bool, error) {
		if entry, ok := vfs[path]; ok {
			return entry.exists, nil
		}
		return pathExistsOnDisk(path)
	}

	loadVirtual := func(path string) (v4aVFSEntry, error) {
		if entry, ok := vfs[path]; ok {
			return entry, nil
		}
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return v4aVFSEntry{exists: false}, nil
			}
			return v4aVFSEntry{}, err
		}
		if info.IsDir() {
			return v4aVFSEntry{}, fmt.Errorf("%q is a directory", path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return v4aVFSEntry{}, err
		}
		entry := v4aVFSEntry{
			exists:  true,
			content: strings.ReplaceAll(string(raw), "\r\n", "\n"),
			hasCRLF: strings.Contains(string(raw), "\r\n"),
		}
		vfs[path] = entry
		return entry, nil
	}

	// Phase 1: Validate all operations and compute final contents in memory before touching disk.
	planned := make([]v4aPlannedOp, 0, len(ops))
	for _, op := range ops {
		absPath, err := resolvePath(cwd, op.path)
		if err != nil {
			return errResult(fmt.Sprintf("patch: %v", err)), nil
		}
		if err := s.checkWriteGuard(ctx, absPath); err != nil {
			return errResult(fmt.Sprintf("patch: %v", err)), nil
		}

		switch op.op {
		case v4aAdd:
			exists, err := vfsExists(absPath)
			if err != nil {
				return errResult(fmt.Sprintf("patch: add %s: %v", op.path, err)), nil
			}
			if exists {
				return errResult(fmt.Sprintf("patch: add %s: file already exists", op.path)), nil
			}
			var lines []string
			for _, h := range op.hunks {
				for _, l := range h.lines {
					if l.prefix == '+' || l.prefix == ' ' {
						lines = append(lines, l.content)
					}
				}
			}
			newContent := strings.Join(lines, "\n")
			if len(lines) > 0 {
				newContent += "\n"
			}
			vfs[absPath] = v4aVFSEntry{exists: true, content: newContent, hasCRLF: false}
			planned = append(planned, v4aPlannedOp{
				op:       v4aAdd,
				absPath:  absPath,
				rawPath:  op.path,
				newBytes: []byte(newContent),
				diff:     generateUnifiedDiff(op.path, "", newContent),
			})

		case v4aDelete:
			entry, err := loadVirtual(absPath)
			if err != nil {
				return errResult(fmt.Sprintf("patch: delete %s: %v", op.path, err)), nil
			}
			if !entry.exists {
				return errResult(fmt.Sprintf("patch: delete %s: file does not exist", op.path)), nil
			}
			vfs[absPath] = v4aVFSEntry{exists: false}
			planned = append(planned, v4aPlannedOp{
				op:      v4aDelete,
				absPath: absPath,
				rawPath: op.path,
				diff:    generateUnifiedDiff(op.path, entry.content, ""),
			})

		case v4aMove:
			dstPath, err := resolvePath(cwd, op.newPath)
			if err != nil {
				return errResult(fmt.Sprintf("patch: move %s -> %s: %v", op.path, op.newPath, err)), nil
			}
			if err := s.checkWriteGuard(ctx, dstPath); err != nil {
				return errResult(fmt.Sprintf("patch: %v", err)), nil
			}
			srcEntry, err := loadVirtual(absPath)
			if err != nil {
				return errResult(fmt.Sprintf("patch: move %s -> %s: %v", op.path, op.newPath, err)), nil
			}
			if !srcEntry.exists {
				return errResult(fmt.Sprintf("patch: move %s -> %s: source file does not exist", op.path, op.newPath)), nil
			}
			if dstPath != absPath {
				dstExists, err := vfsExists(dstPath)
				if err != nil {
					return errResult(fmt.Sprintf("patch: move %s -> %s: %v", op.path, op.newPath, err)), nil
				}
				if dstExists {
					return errResult(fmt.Sprintf("patch: move %s -> %s: destination already exists", op.path, op.newPath)), nil
				}
			}
			vfs[absPath] = v4aVFSEntry{exists: false}
			vfs[dstPath] = srcEntry
			planned = append(planned, v4aPlannedOp{
				op:      v4aMove,
				absPath: absPath,
				dstPath: dstPath,
				rawPath: op.path,
				newPath: op.newPath,
				diff:    fmt.Sprintf("renamed %s -> %s", op.path, op.newPath),
			})

		case v4aUpdate:
			entry, err := loadVirtual(absPath)
			if err != nil {
				return errResult(fmt.Sprintf("patch: update %s: %v", op.path, err)), nil
			}
			if !entry.exists {
				return errResult(fmt.Sprintf("patch: update %s: file does not exist", op.path)), nil
			}
			orig := entry.content
			current := orig

			for hi, h := range op.hunks {
				var searchLines, replaceLines []string
				for _, l := range h.lines {
					if l.prefix != '+' {
						searchLines = append(searchLines, l.content)
					}
					if l.prefix != '-' {
						replaceLines = append(replaceLines, l.content)
					}
				}
				if len(searchLines) == 0 {
					// Pure addition hunk: anchor on contextHint if present, else append.
					addition := strings.Join(replaceLines, "\n")
					if h.contextHint != "" {
						idx := strings.Index(current, h.contextHint)
						if idx < 0 {
							return errResult(fmt.Sprintf("patch: update %s hunk %d: context hint %q not found", op.path, hi+1, h.contextHint)), nil
						}
						lineEnd := strings.Index(current[idx:], "\n")
						if lineEnd < 0 {
							current = current + "\n" + addition
						} else {
							insertPos := idx + lineEnd + 1
							current = current[:insertPos] + addition + "\n" + current[insertPos:]
						}
					} else {
						if current != "" && !strings.HasSuffix(current, "\n") {
							current += "\n"
						}
						current += addition + "\n"
					}
					continue
				}

				oldBlock := strings.Join(searchLines, "\n")
				newBlock := strings.Join(replaceLines, "\n")
				next, _, _, err := fuzzyFindAndReplace(current, oldBlock, newBlock, false)
				if err != nil {
					return errResult(fmt.Sprintf("patch: update %s hunk %d: %v", op.path, hi+1, err)), nil
				}
				current = next
			}

			writeBytes := []byte(current)
			if entry.hasCRLF {
				writeBytes = []byte(strings.ReplaceAll(current, "\n", "\r\n"))
			}
			vfs[absPath] = v4aVFSEntry{exists: true, content: current, hasCRLF: entry.hasCRLF}
			planned = append(planned, v4aPlannedOp{
				op:       v4aUpdate,
				absPath:  absPath,
				rawPath:  op.path,
				newBytes: writeBytes,
				diff:     generateUnifiedDiff(op.path, orig, current),
			})
		}
	}

	// Phase 2: Commit all validated operations to disk.
	diffs := make([]string, 0, len(planned))
	modified := make([]string, 0, len(planned))
	for _, p := range planned {
		switch p.op {
		case v4aAdd:
			if err := atomicWriteFile(p.absPath, p.newBytes); err != nil {
				return errResult(fmt.Sprintf("patch: add %s: %v", p.rawPath, err)), nil
			}
			if info, err := os.Stat(p.absPath); err == nil {
				s.recordRead(p.absPath, info, true)
			}
			modified = append(modified, p.absPath)

		case v4aDelete:
			if err := os.Remove(p.absPath); err != nil {
				return errResult(fmt.Sprintf("patch: delete %s: %v", p.rawPath, err)), nil
			}
			modified = append(modified, p.absPath)

		case v4aMove:
			if err := os.MkdirAll(filepath.Dir(p.dstPath), 0o755); err != nil {
				return errResult(fmt.Sprintf("patch: move %s -> %s: %v", p.rawPath, p.newPath, err)), nil
			}
			if err := os.Rename(p.absPath, p.dstPath); err != nil {
				return errResult(fmt.Sprintf("patch: move %s -> %s: %v", p.rawPath, p.newPath, err)), nil
			}
			modified = append(modified, p.dstPath)

		case v4aUpdate:
			if err := atomicWriteFile(p.absPath, p.newBytes); err != nil {
				return errResult(fmt.Sprintf("patch: update %s: %v", p.rawPath, err)), nil
			}
			if info, err := os.Stat(p.absPath); err == nil {
				s.recordRead(p.absPath, info, true)
			}
			modified = append(modified, p.absPath)
		}
		diffs = append(diffs, p.diff)
	}

	summary := strings.Join(diffs, "\n\n")
	return okResult(
		fmt.Sprintf("Applied %d V4A operation(s):\n%s", len(ops), summary),
		map[string]any{
			"operations": len(ops),
			"files":      modified,
			"diff":       summary,
		},
	), nil
}

func parseV4APatch(patchContent string) ([]v4aOperation, error) {
	rawLines := strings.Split(patchContent, "\n")
	lines := make([]string, len(rawLines))
	for i, l := range rawLines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}

	startIdx := -1
	endIdx := len(lines)
	for i, line := range lines {
		if reBeginPatch.MatchString(line) {
			startIdx = i
		} else if reEndPatch.MatchString(line) {
			endIdx = i
			break
		}
	}

	var ops []v4aOperation
	var curOp *v4aOperation
	var curHunk *v4aHunk

	flushHunk := func() {
		if curOp != nil && curHunk != nil && len(curHunk.lines) > 0 {
			curOp.hunks = append(curOp.hunks, *curHunk)
		}
		curHunk = nil
	}
	flushOp := func() {
		if curOp != nil {
			flushHunk()
			ops = append(ops, *curOp)
			curOp = nil
		}
	}

	for _, line := range lines[startIdx+1 : endIdx] {
		if m := reUpdateFile.FindStringSubmatch(line); m != nil {
			flushOp()
			curOp = &v4aOperation{op: v4aUpdate, path: strings.TrimSpace(m[1])}
			continue
		}
		if m := reAddFile.FindStringSubmatch(line); m != nil {
			flushOp()
			curOp = &v4aOperation{op: v4aAdd, path: strings.TrimSpace(m[1])}
			curHunk = &v4aHunk{}
			continue
		}
		if m := reDeleteFile.FindStringSubmatch(line); m != nil {
			flushOp()
			ops = append(ops, v4aOperation{op: v4aDelete, path: strings.TrimSpace(m[1])})
			continue
		}
		if m := reMoveFile.FindStringSubmatch(line); m != nil {
			flushOp()
			ops = append(ops, v4aOperation{
				op:      v4aMove,
				path:    strings.TrimSpace(m[1]),
				newPath: strings.TrimSpace(m[2]),
			})
			continue
		}
		if strings.HasPrefix(line, "@@") {
			if curOp != nil {
				flushHunk()
				hint := ""
				if m := reHint.FindStringSubmatch(line); m != nil {
					hint = strings.TrimSpace(m[1])
				}
				curHunk = &v4aHunk{contextHint: hint}
			}
			continue
		}
		if curOp != nil && line != "" {
			if curHunk == nil {
				curHunk = &v4aHunk{}
			}
			switch line[0] {
			case '+', '-', ' ':
				curHunk.lines = append(curHunk.lines, v4aHunkLine{prefix: line[0], content: line[1:]})
			case '\\':
				// "\ No newline at end of file" — skip
			default:
				curHunk.lines = append(curHunk.lines, v4aHunkLine{prefix: ' ', content: line})
			}
		}
	}
	flushOp()

	var errs []string
	for _, op := range ops {
		if op.path == "" {
			errs = append(errs, "operation with empty file path")
		}
		if op.op == v4aUpdate && len(op.hunks) == 0 {
			errs = append(errs, fmt.Sprintf("UPDATE %q: no hunks found", op.path))
		}
		if op.op == v4aMove && op.newPath == "" {
			errs = append(errs, fmt.Sprintf("MOVE %q: missing destination path", op.path))
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("parse error: %s", strings.Join(errs, "; "))
	}
	return ops, nil
}
