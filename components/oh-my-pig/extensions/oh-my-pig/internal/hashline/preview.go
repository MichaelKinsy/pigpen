package hashline

import (
	"fmt"
	"strings"
)

// Streaming section previews and static payload inspection.
// upstream: crates/edit/src/modes/hashline/preview.rs, crates/edit/src/modes/hashline/mod.rs

// PreviewFile holds one file's computed diff preview.
// upstream: crates/edit/src/engine.rs:167
type PreviewFile struct {
	Display          string
	Diff             string
	HasDiff          bool
	FirstChangedLine uint32
	Error            string
	Op               EngineFileOp
	Rename           string
}

func cursorLine(cursor Cursor, fileLineCount int) uint32 {
	switch cursor.Kind {
	case CursorBof:
		return 1
	case CursorEof:
		return uint32(fileLineCount) + 1
	case CursorBeforeAnchor:
		return cursor.Anchor.Line
	case CursorAfterAnchor:
		return cursor.Anchor.Line + 1
	default:
		return 1
	}
}

func streamingDiff(section *PatchSection, normalized string, clipboard *Clipboard) (string, uint32, *FileOp, string) {
	parsed, pErr := ParsePatchStreaming(section.Diff)
	if pErr != nil {
		return "", 0, nil, pErr.Error()
	}
	blockResolved, err := ResolveBlockEdits(parsed.Edits, normalized, section.Path, UnresolvedDrop, nil, nil)
	if err != nil {
		return "", 0, nil, err.Error()
	}
	fileLines := strings.Split(normalized, "\n")
	scratch := clipboard.Fork()
	resolved, err := ResolveClipboardEdits(blockResolved, fileLines, scratch, EmptyPasteDrop, nil)
	if err != nil {
		filtered := make([]Edit, 0, len(blockResolved))
		for _, e := range blockResolved {
			if e.Kind != EditCut && e.Kind != EditPaste {
				filtered = append(filtered, e)
			}
		}
		resolved = filtered
	} else {
		*clipboard = *scratch
	}
	if len(resolved) == 0 {
		if parsed.FileOp != nil {
			return "", 0, parsed.FileOp, ""
		}
		return "", 0, nil, fmt.Sprintf("No changes would be made to %s.", section.Path)
	}
	var rows []string
	var first uint32
	i := 0
	for i < len(resolved) {
		ln := resolved[i].LineNum
		j := i + 1
		for j < len(resolved) && resolved[j].LineNum == ln {
			j++
		}
		group := resolved[i:j]
		i = j
		var deletes []uint32
		var inserts []string
		var insertBase uint32
		for _, edit := range group {
			switch edit.Kind {
			case EditDelete:
				deletes = append(deletes, edit.Anchor.Line)
			case EditInsert:
				if insertBase == 0 {
					insertBase = cursorLine(edit.Cursor, len(fileLines))
				}
				inserts = append(inserts, edit.Text)
			}
		}
		for _, dl := range deletes {
			if first == 0 {
				first = dl
			}
			content := ""
			if dl >= 1 && int(dl-1) < len(fileLines) {
				content = fileLines[dl-1]
			}
			rows = append(rows, fmt.Sprintf("-%d|%s", dl, content))
		}
		insLine := insertBase
		if insLine == 0 && len(deletes) > 0 {
			insLine = deletes[0]
		}
		if insLine == 0 {
			insLine = 1
		}
		for _, txt := range inserts {
			if first == 0 {
				first = insLine
			}
			rows = append(rows, fmt.Sprintf("+%d|%s", insLine, txt))
			insLine++
		}
	}
	if len(rows) == 0 {
		return "", 0, nil, fmt.Sprintf("No changes would be made to %s.", section.Path)
	}
	return strings.Join(rows, "\n"), first, parsed.FileOp, ""
}

// PreviewPatch computes per-section previews, threading a transactional clipboard in patch order.
// upstream: crates/edit/src/modes/hashline/preview.rs:225
func PreviewPatch(patch *Patch, streaming bool, files FileSource, store *Store) []PreviewFile {
	count := len(patch.Sections)
	if streaming && count > 1 && !ContainsRecognizableHashlineOperations(patch.Sections[count-1].Diff) {
		count--
	}
	var clipboard *Clipboard
	if store != nil {
		clipboard = store.StartBatchClipboard()
	} else {
		clipboard = &Clipboard{}
	}
	var previews []PreviewFile
	for idx := range count {
		section := patch.Sections[idx]
		pv := previewSection(section, streaming, files, store, clipboard)
		if pv.Error != "" && (streaming || count > 1) && idx+1 == count {
			continue
		}
		previews = append(previews, pv)
	}
	return previews
}

func previewSection(section *PatchSection, streaming bool, files FileSource, store *Store, clipboard *Clipboard) PreviewFile {
	res := PreviewFile{Display: section.Path}
	initial, err := files.Resolve(section.Path)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	target := section
	resolved := initial
	if recSec, recRes, recErr := recoverTarget(section, initial, files, store); recErr != nil {
		res.Error = recErr.Error()
		return res
	} else if recSec != nil {
		target = recSec
		resolved = *recRes
	}
	res.Display = target.Path
	read, err := files.TryRead(resolved)
	if err != nil {
		if err.IsInvalidUtf8() {
			parsed, pErr := target.Parse()
			if pErr == nil && target.HasHash && parsed.FileOp != nil && parsed.FileOp.Kind == FileOpDelete {
				res.Op = EngineFileOpDelete
				res.HasDiff = true
				return res
			}
		}
		res.Error = err.Error()
		return res
	}
	if read == nil {
		res.Error = fmt.Sprintf("File not found: %s", target.Path)
		return res
	}
	if streaming {
		diff, first, fileOp, sErr := streamingDiff(target, read.Text, clipboard)
		if sErr != "" {
			res.Error = sErr
			return res
		}
		res.Diff = diff
		res.HasDiff = true
		res.FirstChangedLine = first
		if fileOp != nil && fileOp.Kind == FileOpDelete {
			res.Op = EngineFileOpDelete
		} else {
			res.Op = EngineFileOpUpdate
		}
		return res
	}
	parsed, err := target.Parse()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if !target.HasHash {
		res.Error = mismatchForSection(target, read.Canonical, read.Text, "", store).Error()
		return res
	}
	applied, err := applyWithRecovery(target, read.Canonical, read.Text, parsed.Edits, clipboard, store, false, 0)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if parsed.FileOp != nil && parsed.FileOp.Kind == FileOpDelete {
		res.Op = EngineFileOpDelete
	} else {
		res.Op = EngineFileOpUpdate
	}
	if parsed.FileOp != nil && parsed.FileOp.Kind == FileOpMove {
		res.Rename = parsed.FileOp.Dest
	}
	if applied.Text == read.Text || (parsed.FileOp != nil && parsed.FileOp.Kind == FileOpDelete) {
		if parsed.FileOp != nil {
			res.HasDiff = true
			return res
		}
		res.Error = fmt.Sprintf("No changes would be made to %s.", target.Path)
		return res
	}
	diffOut := GenerateDiffString(read.Text, applied.Text, 2, false)
	res.Diff = diffOut.Diff
	res.HasDiff = true
	res.FirstChangedLine = applied.FirstChangedLine
	if res.FirstChangedLine == 0 {
		res.FirstChangedLine = diffOut.FirstChangedLine
	}
	return res
}

// Inspection is a static projection of a hashline payload without filesystem access.
// upstream: crates/edit/src/engine.rs:181, crates/edit/src/modes/hashline/mod.rs:129
type Inspection struct {
	Paths   []string
	Entries []InspectionEntry
}

// InspectionEntry pairs a target path with its added-lines digest.
type InspectionEntry struct {
	Path   string
	Digest string
}

// InspectInput extracts target paths and added-line digests from a (possibly partial) payload.
// upstream: crates/edit/src/modes/hashline/mod.rs:129
func InspectInput(input string) Inspection {
	type hdr struct {
		path      string
		bodyStart int
		start     int
	}
	var headers []hdr
	offset := 0
	for line := range strings.SplitSeq(input, "\n") {
		rawLen := len(line)
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inner := trimmed[1 : len(trimmed)-1]
			if len(inner) >= 5 && inner[len(inner)-5] == '#' {
				hex := inner[len(inner)-4:]
				allHex := true
				for i := range 4 {
					if !isASCIIHex(hex[i]) {
						allHex = false
						break
					}
				}
				if allHex {
					inner = inner[:len(inner)-5]
				}
			}
			path := strings.TrimSpace(applyPatchPathNoiseRe.ReplaceAllString(inner, ""))
			if path != "" {
				bodyStart := offset + rawLen
				if bodyStart < len(input) && input[bodyStart] == '\n' {
					bodyStart++
				}
				headers = append(headers, hdr{path: path, bodyStart: bodyStart, start: offset})
			}
		}
		offset += rawLen + 1
	}

	paths := make([]string, len(headers))
	for i, h := range headers {
		paths[i] = h.path
	}
	var entries []InspectionEntry
	for i, h := range headers {
		bodyEnd := len(input)
		if i+1 < len(headers) {
			bodyEnd = headers[i+1].start
		}
		if h.bodyStart > bodyEnd {
			continue
		}
		var added []string
		for row := range strings.SplitSeq(input[h.bodyStart:bodyEnd], "\n") {
			r := strings.TrimSuffix(row, "\r")
			if rest, ok := strings.CutPrefix(r, "+"); ok {
				added = append(added, rest)
			}
		}
		if len(added) == 0 {
			continue
		}
		digest := strings.Join(added, "\n")
		merged := false
		for j := range entries {
			if entries[j].Path == h.path {
				entries[j].Digest += "\n" + digest
				merged = true
				break
			}
		}
		if !merged {
			entries = append(entries, InspectionEntry{Path: h.path, Digest: digest})
		}
	}
	return Inspection{Paths: paths, Entries: entries}
}
