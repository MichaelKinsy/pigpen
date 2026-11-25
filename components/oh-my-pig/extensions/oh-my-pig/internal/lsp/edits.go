package lsp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
)

// upstream: packages/coding-agent/src/lsp/edits.ts

// ToolError marks an operational error that should surface directly to the
// model (mirroring omp's ToolError).
type ToolError struct {
	Message string
}

func (e *ToolError) Error() string { return e.Message }

var (
	localQueueMu sync.Mutex
	localQueues  = map[string]*sync.Mutex{}
)

func localFileMutex(absPath string) *sync.Mutex {
	key := filepath.Clean(absPath)
	localQueueMu.Lock()
	defer localQueueMu.Unlock()
	m, ok := localQueues[key]
	if !ok {
		m = &sync.Mutex{}
		localQueues[key] = m
	}
	return m
}

// withFileMutationQueue serializes file mutations through PiG's host file
// mutation queue when connected to a host (both subprocess and in-process fused
// deployments, regardless of PIG_EXT_SOCKET), falling back to a per-path mutex
// only when ctx is zero or not connected (standalone unit tests).
// fn is guaranteed to run at most once: panics thrown by fn itself are never
// swallowed, and a host error after fn ran never retries fn.
func withFileMutationQueue(ctx sdk.Context, absPath string, fn func() error) error {
	ran := false
	var hostErr error
	var panicVal any
	var panicked bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicVal = r
				panicked = true
			}
		}()
		hostErr = ctx.WithFileMutationQueue(absPath, func() error {
			ran = true
			return fn()
		})
	}()
	if panicked {
		if ran {
			panic(panicVal)
		}
		m := localFileMutex(absPath)
		m.Lock()
		defer m.Unlock()
		return fn()
	}
	if ran {
		return hostErr
	}
	if hostErr != nil && isNoHostConnErr(hostErr) {
		m := localFileMutex(absPath)
		m.Lock()
		defer m.Unlock()
		return fn()
	}
	return hostErr
}

func isNoHostConnErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "not connected") || strings.Contains(msg, "PIG_EXT_SOCKET")
}

// refreshHashlineSnapshot updates the calling session's hashline store after
// LSP mutates filePath so the model's next hashline edit sees the current tag.
func refreshHashlineSnapshot(sdkCtx sdk.Context, absPath string, content string) {
	_, stripped := hashline.StripBOM(content)
	normalized := hashline.NormalizeToLF(stripped)
	hashline.ForSession(sdkCtx).RecordSnapshot(absPath, normalized)
}

func refreshHashlineSnapshotFromDisk(sdkCtx sdk.Context, absPath string) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return
	}
	refreshHashlineSnapshot(sdkCtx, absPath, string(data))
}

func comparePosition(a, b Position) int {
	if a.Line != b.Line {
		if a.Line < b.Line {
			return -1
		}
		return 1
	}
	if a.Character < b.Character {
		return -1
	}
	if a.Character > b.Character {
		return 1
	}
	return 0
}

func positionsEqual(a, b Position) bool {
	return a.Line == b.Line && a.Character == b.Character
}

func rangesEqual(a, b Range) bool {
	return positionsEqual(a.Start, b.Start) && positionsEqual(a.End, b.End)
}

func isEmptyRange(r Range) bool {
	return positionsEqual(r.Start, r.End)
}

func formatRange(r Range) string {
	return fmt.Sprintf("%d:%d-%d:%d",
		r.Start.Line+1, r.Start.Character+1,
		r.End.Line+1, r.End.Character+1)
}

// rangesOverlap reports whether two ranges share any position other than a
// touching boundary.
func rangesOverlap(a, b Range) bool {
	return comparePosition(a.Start, b.End) < 0 && comparePosition(b.Start, a.End) < 0
}

func rejectSnippetTextEdits(edits []TextEdit) error {
	for _, e := range edits {
		if e.InsertTextFormat == 2 {
			return &ToolError{Message: "snippet-formatted LSP edits are unsupported"}
		}
	}
	return nil
}

// sortAndValidateTextEdits sorts edits bottom-to-top, collapses byte-identical
// non-empty duplicates, and rejects snippets or overlapping ranges.
// upstream: packages/coding-agent/src/lsp/edits.ts:sortAndValidateTextEdits
func sortAndValidateTextEdits(edits []TextEdit) ([]TextEdit, error) {
	if err := rejectSnippetTextEdits(edits); err != nil {
		return nil, err
	}
	type indexedEdit struct {
		edit  TextEdit
		index int
	}
	indexed := make([]indexedEdit, len(edits))
	for i, e := range edits {
		indexed[i] = indexedEdit{edit: e, index: i}
	}
	sort.Slice(indexed, func(i, j int) bool {
		a, b := indexed[i], indexed[j]
		if a.edit.Range.Start.Line != b.edit.Range.Start.Line {
			return a.edit.Range.Start.Line > b.edit.Range.Start.Line
		}
		if a.edit.Range.Start.Character != b.edit.Range.Start.Character {
			return a.edit.Range.Start.Character > b.edit.Range.Start.Character
		}
		return a.index > b.index
	})
	unique := make([]TextEdit, 0, len(indexed))
	for _, entry := range indexed {
		if len(unique) > 0 {
			prev := unique[len(unique)-1]
			if !isEmptyRange(entry.edit.Range) && rangesEqual(prev.Range, entry.edit.Range) && prev.NewText == entry.edit.NewText {
				continue
			}
		}
		unique = append(unique, entry.edit)
	}
	for i := range len(unique) - 1 {
		later := unique[i].Range
		earlier := unique[i+1].Range
		if comparePosition(earlier.End, later.Start) > 0 {
			return nil, &ToolError{
				Message: fmt.Sprintf("overlapping LSP edits: %s conflicts with %s; LSP produced inconsistent edits",
					formatRange(earlier), formatRange(later)),
			}
		}
	}
	return unique, nil
}

// utf16OffsetToByteIndex converts an LSP UTF-16 code-unit column offset within
// line into a UTF-8 byte index, clamping at the end of line.
func utf16OffsetToByteIndex(line string, utf16Col int) int {
	if utf16Col <= 0 {
		return 0
	}
	units := 0
	for byteIdx, r := range line {
		if units >= utf16Col {
			return byteIdx
		}
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return len(line)
}

// applyTextEditsToString applies validated text edits bottom-to-top in-memory.
// upstream: packages/coding-agent/src/lsp/edits.ts:applyTextEditsToString
func applyTextEditsToString(content string, edits []TextEdit) (string, error) {
	sorted, err := sortAndValidateTextEdits(edits)
	if err != nil {
		return "", err
	}
	lines := strings.Split(content, "\n")
	for _, edit := range sorted {
		start := edit.Range.Start
		end := edit.Range.End
		for len(lines) <= end.Line {
			lines = append(lines, "")
		}
		for len(lines) <= start.Line {
			lines = append(lines, "")
		}
		if start.Line == end.Line {
			line := lines[start.Line]
			sIdx := utf16OffsetToByteIndex(line, start.Character)
			eIdx := utf16OffsetToByteIndex(line, end.Character)
			if eIdx < sIdx {
				eIdx = sIdx
			}
			lines[start.Line] = line[:sIdx] + edit.NewText + line[eIdx:]
		} else {
			startLine := lines[start.Line]
			endLine := lines[end.Line]
			sIdx := utf16OffsetToByteIndex(startLine, start.Character)
			eIdx := utf16OffsetToByteIndex(endLine, end.Character)
			combined := startLine[:sIdx] + edit.NewText + endLine[eIdx:]
			replacement := strings.Split(combined, "\n")
			next := make([]string, 0, len(lines)-(end.Line-start.Line+1)+len(replacement))
			next = append(next, lines[:start.Line]...)
			next = append(next, replacement...)
			next = append(next, lines[end.Line+1:]...)
			lines = next
		}
	}
	return strings.Join(lines, "\n"), nil
}

func applyTextEdits(sdkCtx sdk.Context, filePath string, edits []TextEdit) error {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		absPath = filePath
	}
	return withFileMutationQueue(sdkCtx, absPath, func() error {
		content, err := readTextFromDisk(absPath)
		if err != nil {
			return err
		}
		result, err := applyTextEditsToString(content, edits)
		if err != nil {
			return err
		}
		if err := os.WriteFile(absPath, []byte(result), 0o644); err != nil {
			return err
		}
		refreshHashlineSnapshot(sdkCtx, absPath, result)
		return nil
	})
}

// flattenWorkspaceTextEdits extracts all text edits grouped by URI, preserving
// server wire order via the returned order slice.
// upstream: packages/coding-agent/src/lsp/edits.ts:flattenWorkspaceTextEdits
func flattenWorkspaceTextEdits(edit *WorkspaceEdit) (map[string][]TextEdit, []string) {
	out := make(map[string][]TextEdit)
	var order []string
	push := func(uri string, edits []TextEdit) {
		if len(edits) == 0 {
			return
		}
		if _, ok := out[uri]; !ok {
			order = append(order, uri)
		}
		out[uri] = append(out[uri], edits...)
	}
	if edit == nil {
		return out, order
	}
	if len(edit.Changes) > 0 {
		for _, uri := range edit.orderedChangesURIs() {
			push(uri, edit.Changes[uri])
		}
	}
	for _, change := range edit.DocumentChanges {
		if change.TextDocument != nil && len(change.Edits) > 0 {
			push(change.TextDocument.URI, change.Edits)
		}
	}
	return out, order
}

type renameReferenceEdit struct {
	filePath string
	edits    []TextEdit
}

// applyEditsThenRename gates every affected file through internal/gate, writes
// reference edits through the file mutation queue, and renames source -> dest
// as one atomic unit (rolling back edited reference files if mkdir/rename fails).
// upstream: packages/coding-agent/src/lsp/edits.ts:applyEditsThenRename
func applyEditsThenRename(sdkCtx sdk.Context, references []renameReferenceEdit, source, dest string) error {
	absSrc, _ := filepath.Abs(source)
	absDst, _ := filepath.Abs(dest)

	// Gate every file mutation before touching disk.
	for _, ref := range references {
		absRef, _ := filepath.Abs(ref.filePath)
		if err := gate.Check(sdkCtx, gate.Request{Tool: "lsp", Op: gate.OpEdit, Path: absRef}); err != nil {
			return &ToolError{Message: err.Error()}
		}
	}
	if err := gate.Check(sdkCtx, gate.Request{Tool: "lsp", Op: gate.OpMove, Path: absSrc, Dest: absDst}); err != nil {
		return &ToolError{Message: err.Error()}
	}

	type backup struct {
		filePath string
		original []byte
	}
	backups := make([]backup, 0, len(references))
	rollback := func() {
		for _, b := range backups {
			_ = withFileMutationQueue(sdkCtx, b.filePath, func() error {
				if err := os.WriteFile(b.filePath, b.original, 0o644); err != nil {
					return err
				}
				refreshHashlineSnapshot(sdkCtx, b.filePath, string(b.original))
				return nil
			})
		}
	}

	for _, ref := range references {
		absRef, _ := filepath.Abs(ref.filePath)
		orig, err := readTextFromDisk(absRef)
		if err != nil {
			rollback()
			return err
		}
		backups = append(backups, backup{filePath: absRef, original: []byte(orig)})
		if err := applyTextEdits(sdkCtx, absRef, ref.edits); err != nil {
			rollback()
			return err
		}
	}

	moveErr := withFileMutationQueue(sdkCtx, absSrc, func() error {
		if err := os.MkdirAll(filepath.Dir(absDst), 0o755); err != nil {
			return err
		}
		if err := os.Rename(absSrc, absDst); err != nil {
			return err
		}
		if info, statErr := os.Stat(absDst); statErr == nil && !info.IsDir() {
			refreshHashlineSnapshotFromDisk(sdkCtx, absDst)
		}
		return nil
	})
	if moveErr != nil {
		rollback()
		return moveErr
	}
	return nil
}

// =============================================================================
// Workspace Edit Application
// =============================================================================

type workspaceEditOp struct {
	kind          string // "text", "create", "rename", "delete"
	uri           string
	edits         []TextEdit
	oldURI        string
	newURI        string
	createOptions *CreateFileOptions
	renameOptions *RenameFileOptions
	deleteOptions *DeleteFileOptions
}

type ExecutedWorkspaceChange struct {
	Kind   string // "edit", "create", "rename", "delete"
	URI    string
	OldURI string
	NewURI string
}

type WorkspaceEditResult struct {
	Applied  []string
	Executed []ExecutedWorkspaceChange
}

func planDocumentChanges(documentChanges []DocumentChange) []workspaceEditOp {
	var ops []workspaceEditOp
	pending := make(map[string][]TextEdit)
	var pendingOrder []string

	flushURI := func(uri string) {
		edits, ok := pending[uri]
		if !ok {
			return
		}
		delete(pending, uri)
		ops = append(ops, workspaceEditOp{kind: "text", uri: uri, edits: edits})
	}

	flushSubtree := func(uri string) {
		prefix := uri
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		var matches []string
		for _, candidate := range pendingOrder {
			if _, ok := pending[candidate]; !ok {
				continue
			}
			if candidate == uri || strings.HasPrefix(candidate, prefix) {
				matches = append(matches, candidate)
			}
		}
		for _, target := range matches {
			flushURI(target)
		}
	}

	for _, change := range documentChanges {
		if change.TextDocument != nil && len(change.Edits) > 0 {
			uri := change.TextDocument.URI
			if _, ok := pending[uri]; !ok {
				pendingOrder = append(pendingOrder, uri)
			}
			pending[uri] = append(pending[uri], change.Edits...)
		} else if change.Kind != "" {
			switch change.Kind {
			case "create":
				flushURI(change.URI)
				ops = append(ops, workspaceEditOp{kind: "create", uri: change.URI, createOptions: change.CreateOptions})
			case "rename":
				flushSubtree(change.OldURI)
				flushSubtree(change.NewURI)
				ops = append(ops, workspaceEditOp{
					kind:          "rename",
					oldURI:        change.OldURI,
					newURI:        change.NewURI,
					renameOptions: change.RenameOptions,
				})
			case "delete":
				flushSubtree(change.URI)
				ops = append(ops, workspaceEditOp{kind: "delete", uri: change.URI, deleteOptions: change.DeleteOptions})
			}
		}
	}

	for _, uri := range pendingOrder {
		flushURI(uri)
	}
	return ops
}

// gateWorkspaceEdit checks every planned file mutation against internal/gate
// before any file is modified on disk.
func gateWorkspaceEdit(sdkCtx sdk.Context, edit *WorkspaceEdit) error {
	if edit == nil {
		return nil
	}
	if len(edit.DocumentChanges) > 0 {
		for _, op := range planDocumentChanges(edit.DocumentChanges) {
			switch op.kind {
			case "text":
				abs, _ := filepath.Abs(uriToFile(op.uri))
				if err := gate.Check(sdkCtx, gate.Request{Tool: "lsp", Op: gate.OpEdit, Path: abs}); err != nil {
					return &ToolError{Message: err.Error()}
				}
			case "create":
				abs, _ := filepath.Abs(uriToFile(op.uri))
				if err := gate.Check(sdkCtx, gate.Request{Tool: "lsp", Op: gate.OpWrite, Path: abs}); err != nil {
					return &ToolError{Message: err.Error()}
				}
			case "rename":
				absOld, _ := filepath.Abs(uriToFile(op.oldURI))
				absNew, _ := filepath.Abs(uriToFile(op.newURI))
				if err := gate.Check(sdkCtx, gate.Request{Tool: "lsp", Op: gate.OpMove, Path: absOld, Dest: absNew}); err != nil {
					return &ToolError{Message: err.Error()}
				}
			case "delete":
				abs, _ := filepath.Abs(uriToFile(op.uri))
				if err := gate.Check(sdkCtx, gate.Request{Tool: "lsp", Op: gate.OpDelete, Path: abs}); err != nil {
					return &ToolError{Message: err.Error()}
				}
			}
		}
		return nil
	}
	for _, uri := range edit.orderedChangesURIs() {
		if len(edit.Changes[uri]) == 0 {
			continue
		}
		abs, _ := filepath.Abs(uriToFile(uri))
		if err := gate.Check(sdkCtx, gate.Request{Tool: "lsp", Op: gate.OpEdit, Path: abs}); err != nil {
			return &ToolError{Message: err.Error()}
		}
	}
	return nil
}

// applyWorkspaceEdit applies all text edits and resource operations in edit,
// validating all text edit batches and checking internal/gate before writing any file.
// upstream: packages/coding-agent/src/lsp/edits.ts:applyWorkspaceEdit
func applyWorkspaceEdit(sdkCtx sdk.Context, edit *WorkspaceEdit, cwd string, onExecuted func(ExecutedWorkspaceChange)) (WorkspaceEditResult, error) {
	var res WorkspaceEditResult
	if edit == nil {
		return res, nil
	}
	record := func(change ExecutedWorkspaceChange) {
		res.Executed = append(res.Executed, change)
		if onExecuted != nil {
			onExecuted(change)
		}
	}

	if err := gateWorkspaceEdit(sdkCtx, edit); err != nil {
		return res, err
	}

	if len(edit.DocumentChanges) > 0 {
		ops := planDocumentChanges(edit.DocumentChanges)
		for _, op := range ops {
			if op.kind == "text" {
				if _, err := sortAndValidateTextEdits(op.edits); err != nil {
					return res, err
				}
			}
		}
		for _, op := range ops {
			switch op.kind {
			case "text":
				filePath := uriToFile(op.uri)
				if err := applyTextEdits(sdkCtx, filePath, op.edits); err != nil {
					return res, err
				}
				res.Applied = append(res.Applied, fmt.Sprintf("Applied %d edit(s) to %s", len(op.edits), formatPathRelativeToCwd(filePath, cwd)))
				record(ExecutedWorkspaceChange{Kind: "edit", URI: op.uri})

			case "create":
				filePath := uriToFile(op.uri)
				absPath, _ := filepath.Abs(filePath)
				skipped := false
				err := withFileMutationQueue(sdkCtx, absPath, func() error {
					if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
						return err
					}
					overwrite := op.createOptions != nil && op.createOptions.Overwrite
					ignoreIfExists := op.createOptions != nil && op.createOptions.IgnoreIfExists
					if overwrite {
						if err := os.WriteFile(absPath, nil, 0o644); err != nil {
							return err
						}
					} else {
						f, err := os.OpenFile(absPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
						if err != nil {
							if ignoreIfExists && errors.Is(err, os.ErrExist) {
								skipped = true
								return nil
							}
							return err
						}
						_ = f.Close()
					}
					refreshHashlineSnapshot(sdkCtx, absPath, "")
					return nil
				})
				if err != nil {
					return res, err
				}
				if skipped {
					continue
				}
				res.Applied = append(res.Applied, "Created "+formatPathRelativeToCwd(filePath, cwd))
				record(ExecutedWorkspaceChange{Kind: "create", URI: op.uri})

			case "rename":
				oldPath := uriToFile(op.oldURI)
				newPath := uriToFile(op.newURI)
				absOld, _ := filepath.Abs(oldPath)
				absNew, _ := filepath.Abs(newPath)
				skipped := false
				err := withFileMutationQueue(sdkCtx, absOld, func() error {
					if err := os.MkdirAll(filepath.Dir(absNew), 0o755); err != nil {
						return err
					}
					overwrite := op.renameOptions != nil && op.renameOptions.Overwrite
					ignoreIfExists := op.renameOptions != nil && op.renameOptions.IgnoreIfExists
					if absOld != absNew {
						var displacedDir, displacedFile string
						if targetStat, err := os.Lstat(absNew); err == nil {
							if !overwrite {
								if ignoreIfExists {
									skipped = true
									return nil
								}
								return &ToolError{Message: "rename target already exists: " + formatPathRelativeToCwd(newPath, cwd)}
							}
							sourceStat, srcErr := os.Lstat(absOld)
							if srcErr != nil {
								return srcErr
							}
							if !sameInode(sourceStat, targetStat) {
								holdDir, mkErr := os.MkdirTemp(filepath.Dir(absNew), ".omp-displaced-")
								if mkErr != nil {
									return mkErr
								}
								holdFile := filepath.Join(holdDir, filepath.Base(absNew))
								if mvErr := os.Rename(absNew, holdFile); mvErr != nil {
									_ = os.RemoveAll(holdDir)
									return mvErr
								}
								displacedDir = holdDir
								displacedFile = holdFile
							}
						} else if !os.IsNotExist(err) {
							return err
						}
						if err := os.Rename(absOld, absNew); err != nil {
							if displacedFile != "" {
								if restoreErr := os.Rename(displacedFile, absNew); restoreErr != nil {
									record(ExecutedWorkspaceChange{Kind: "delete", URI: op.newURI})
								}
								_ = os.RemoveAll(displacedDir)
							}
							return err
						}
						if displacedDir != "" {
							_ = os.RemoveAll(displacedDir)
						}
					}
					if info, statErr := os.Stat(absNew); statErr == nil && !info.IsDir() {
						refreshHashlineSnapshotFromDisk(sdkCtx, absNew)
					}
					return nil
				})
				if err != nil {
					return res, err
				}
				if skipped {
					continue
				}
				res.Applied = append(res.Applied, fmt.Sprintf("Renamed %s → %s",
					formatPathRelativeToCwd(oldPath, cwd),
					formatPathRelativeToCwd(newPath, cwd)))
				record(ExecutedWorkspaceChange{Kind: "rename", OldURI: op.oldURI, NewURI: op.newURI})

			case "delete":
				filePath := uriToFile(op.uri)
				absPath, _ := filepath.Abs(filePath)
				skipped := false
				err := withFileMutationQueue(sdkCtx, absPath, func() error {
					recursive := op.deleteOptions != nil && op.deleteOptions.Recursive
					ignoreIfNotExists := op.deleteOptions != nil && op.deleteOptions.IgnoreIfNotExists
					stat, err := os.Lstat(absPath)
					if err != nil {
						if ignoreIfNotExists && os.IsNotExist(err) {
							skipped = true
							return nil
						}
						return err
					}
					if stat.IsDir() && stat.Mode()&os.ModeSymlink == 0 && !recursive {
						return os.Remove(absPath)
					}
					if recursive {
						return os.RemoveAll(absPath)
					}
					return os.Remove(absPath)
				})
				if err != nil {
					return res, err
				}
				if skipped {
					continue
				}
				res.Applied = append(res.Applied, "Deleted "+formatPathRelativeToCwd(filePath, cwd))
				record(ExecutedWorkspaceChange{Kind: "delete", URI: op.uri})
			}
		}
	} else if len(edit.Changes) > 0 {
		uris := edit.orderedChangesURIs()
		for _, uri := range uris {
			if _, err := sortAndValidateTextEdits(edit.Changes[uri]); err != nil {
				return res, err
			}
		}
		for _, uri := range uris {
			edits := edit.Changes[uri]
			if len(edits) == 0 {
				continue
			}
			filePath := uriToFile(uri)
			if err := applyTextEdits(sdkCtx, filePath, edits); err != nil {
				return res, err
			}
			res.Applied = append(res.Applied, fmt.Sprintf("Applied %d edit(s) to %s", len(edits), formatPathRelativeToCwd(filePath, cwd)))
			record(ExecutedWorkspaceChange{Kind: "edit", URI: uri})
		}
	}
	return res, nil
}
