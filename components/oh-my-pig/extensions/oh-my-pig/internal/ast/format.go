package ast

import (
	"fmt"
	"os"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
)

// Formatting helpers mirroring omp's grouped-file-output.ts, match-line-format.ts,
// hashline-format.ts, and render-utils.ts.

const (
	parseErrorsLimit     = 20
	maxSnapshotFileBytes = 4 * 1024 * 1024
	astEditLineClip      = 120
)

type pathTreeFile struct {
	name string
	key  string
}

type pathTreeSubdir struct {
	name string
	node *pathTreeNode
}

type pathTreeNode struct {
	files     []pathTreeFile
	fileNames map[string]struct{}
	subdirs   []pathTreeSubdir
	dirIndex  map[string]*pathTreeNode
}

type pathTreeInput struct {
	path  string
	isDir bool
	key   string
}

type groupedTreeEvent struct {
	kind  string // "dir" or "file"
	depth int
	name  string
	key   string
}

func newPathTreeNode() *pathTreeNode {
	return &pathTreeNode{
		fileNames: make(map[string]struct{}),
		dirIndex:  make(map[string]*pathTreeNode),
	}
}

func (n *pathTreeNode) addFile(name, key string) {
	if _, exists := n.fileNames[name]; exists {
		return
	}
	n.fileNames[name] = struct{}{}
	n.files = append(n.files, pathTreeFile{name: name, key: key})
}

func buildPathTree(entries []pathTreeInput) *pathTreeNode {
	root := newPathTreeNode()
	for _, entry := range entries {
		normalized := strings.ReplaceAll(entry.path, "\\", "/")
		fileKey := entry.key
		if fileKey == "" {
			fileKey = entry.path
		}
		if isURLLikePath(normalized) {
			root.addFile(normalized, fileKey)
			continue
		}
		trimmed := strings.TrimSuffix(normalized, "/")
		if trimmed == "" {
			continue
		}
		segments := strings.Split(trimmed, "/")
		dirCount := len(segments) - 1
		if entry.isDir {
			dirCount = len(segments)
		}
		node := root
		for i := range dirCount {
			seg := segments[i]
			child, ok := node.dirIndex[seg]
			if !ok {
				child = newPathTreeNode()
				node.dirIndex[seg] = child
				node.subdirs = append(node.subdirs, pathTreeSubdir{name: seg, node: child})
			}
			node = child
		}
		if !entry.isDir {
			node.addFile(segments[len(segments)-1], fileKey)
		}
	}
	return root
}

func walkPathTree(node *pathTreeNode, depth int, emit func(groupedTreeEvent)) {
	for _, f := range node.files {
		emit(groupedTreeEvent{kind: "file", depth: depth, name: f.name, key: f.key})
	}
	for _, sub := range node.subdirs {
		dirNode := sub.node
		parts := []string{sub.name}
		for len(dirNode.files) == 0 && len(dirNode.subdirs) == 1 {
			only := dirNode.subdirs[0]
			parts = append(parts, only.name)
			dirNode = only.node
		}
		emit(groupedTreeEvent{kind: "dir", depth: depth, name: strings.Join(parts, "/")})
		walkPathTree(dirNode, depth+1, emit)
	}
}

type groupedFileSection struct {
	headerSuffix string
	modelLines   []string
	displayLines []string
	skip         bool
}

type groupedFilesOutput struct {
	model   []string
	display []string
}

// formatGroupedFiles renders a list of files as omp's prefix-folded directory tree.
// upstream: packages/tui/src/tools/grouped-file-output.ts:65-107
func formatGroupedFiles(files []string, renderFile func(filePath string) groupedFileSection) groupedFilesOutput {
	sections := make(map[string]groupedFileSection, len(files))
	var inputs []pathTreeInput
	for _, fp := range files {
		if _, seen := sections[fp]; seen {
			continue
		}
		sec := renderFile(fp)
		if sec.skip {
			continue
		}
		sections[fp] = sec
		inputs = append(inputs, pathTreeInput{path: fp, isDir: false, key: fp})
	}
	tree := buildPathTree(inputs)
	var model, display []string
	emitted := false
	walkPathTree(tree, 0, func(ev groupedTreeEvent) {
		hashes := strings.Repeat("#", ev.depth+1)
		if emitted && (ev.depth == 0 || ev.kind == "dir") {
			model = append(model, "")
			display = append(display, "")
		}
		emitted = true
		if ev.kind == "dir" {
			header := hashes + " " + ev.name + "/"
			model = append(model, header)
			display = append(display, header)
			return
		}
		sec := sections[ev.key]
		header := hashes + " " + ev.name + sec.headerSuffix
		model = append(model, header)
		model = append(model, sec.modelLines...)
		display = append(display, header)
		if sec.displayLines != nil {
			display = append(display, sec.displayLines...)
		} else {
			display = append(display, sec.modelLines...)
		}
	})
	return groupedFilesOutput{model: model, display: display}
}

type fileMatchSection struct {
	model   []string
	display []string
	tag     string
}

// formatFileMatches renders per-file search matches in grouped or flat mode.
// upstream: packages/tui/src/tools/grouped-file-output.ts:122-154
func formatFileMatches(files []string, grouped bool, renderFile func(filePath string) fileMatchSection) groupedFilesOutput {
	if grouped {
		return formatGroupedFiles(files, func(filePath string) groupedFileSection {
			sec := renderFile(filePath)
			suffix := ""
			if sec.tag != "" {
				suffix = "#" + sec.tag
			}
			return groupedFileSection{
				headerSuffix: suffix,
				modelLines:   sec.model,
				displayLines: sec.display,
				skip:         len(sec.model) == 0,
			}
		})
	}
	var model, display []string
	for _, fp := range files {
		sec := renderFile(fp)
		if len(sec.model) == 0 {
			continue
		}
		if len(model) > 0 {
			model = append(model, "")
			display = append(display, "")
		}
		if sec.tag != "" {
			model = append(model, formatHashlineHeader(fp, sec.tag))
		}
		model = append(model, sec.model...)
		display = append(display, sec.display...)
	}
	return groupedFilesOutput{model: model, display: display}
}

// formatHashlineHeader formats `[path#TAG]`.
// upstream: packages/tui/src/tools/hashline-format.ts:30
func formatHashlineHeader(filePath, tag string) string {
	return "[" + filePath + "#" + tag + "]"
}

// formatMatchLine formats `*LINE:text` (hashline) or `*LINE|text` (plain).
// upstream: packages/tui/src/tools/match-line-format.ts:9-20
func formatMatchLine(lineNumber int, line string, isMatch bool, useHashLines bool) string {
	marker := " "
	if isMatch {
		marker = "*"
	}
	if useHashLines {
		return fmt.Sprintf("%s%d:%s", marker, lineNumber, line)
	}
	return fmt.Sprintf("%s%d|%s", marker, lineNumber, line)
}

// formatCodeFrameLine formats a code-frame display row with `│` gutter.
// upstream: packages/tui/src/render/render-utils.ts:468-478
func formatCodeFrameLine(marker string, lineNumber int, content string, lineNumberWidth int) string {
	markerText := strings.TrimSpace(marker)
	lineNumberText := fmt.Sprintf("%d", lineNumber)
	gutter := lineNumberText
	if markerText != "" {
		gutter = markerText + lineNumberText
	}
	padWidth := lineNumberWidth + 1
	if len(gutter) < padWidth {
		gutter = strings.Repeat(" ", padWidth-len(gutter)) + gutter
	}
	return gutter + "│" + content
}

// singleLine expands tabs to 3 spaces and collapses CR/LF runs to a single space.
// upstream: packages/tui/src/utils.ts:287-294
func singleLine(s string) string {
	s = strings.ReplaceAll(s, "\t", "   ")
	var b strings.Builder
	b.Grow(len(s))
	inBreak := false
	for i := range len(s) {
		if s[i] == '\r' || s[i] == '\n' {
			if !inBreak {
				b.WriteByte(' ')
				inBreak = true
			}
			continue
		}
		inBreak = false
		b.WriteByte(s[i])
	}
	return b.String()
}

// formatCount returns singular/plural count string (`1 replacement` / `2 replacements`).
func formatCount(word string, count int) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", count, word)
}

// dedupeParseErrors deduplicates parse errors preserving order.
// upstream: packages/tui/src/render/render-utils.ts:1105-1115
func dedupeParseErrors(errs []string) []string {
	if len(errs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(errs))
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		if _, ok := seen[e]; ok {
			continue
		}
		seen[e] = struct{}{}
		out = append(out, e)
	}
	return out
}

// capParseErrors caps deduplicated parse errors at PARSE_ERRORS_LIMIT (20).
// upstream: packages/tui/src/render/render-utils.ts:1133-1139
func capParseErrors(errs []string) ([]string, int) {
	deduped := dedupeParseErrors(errs)
	total := len(deduped)
	if total > parseErrorsLimit {
		return deduped[:parseErrorsLimit], total
	}
	return deduped, total
}

// formatParseErrors formats parse issues header and bullet lines.
// upstream: packages/tui/src/render/render-utils.ts:1118-1125
func formatParseErrors(errs []string, total int) []string {
	deduped := dedupeParseErrors(errs)
	if len(deduped) == 0 {
		return nil
	}
	fullCount := total
	if fullCount < len(deduped) {
		fullCount = len(deduped)
	}
	capped := deduped
	if len(capped) > parseErrorsLimit {
		capped = capped[:parseErrorsLimit]
	}
	header := "Parse issues:"
	if fullCount > len(capped) {
		header = fmt.Sprintf("Parse issues (%d / %d):", len(capped), fullCount)
	}
	lines := make([]string, 0, len(capped)+1)
	lines = append(lines, header)
	for _, e := range capped {
		lines = append(lines, "- "+e)
	}
	return lines
}

// clipFirstLine takes the first line of s and caps it at maxLen runes (matching JS .slice(0, 120)).
func clipFirstLine(s string, maxLen int) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	s = strings.TrimSuffix(s, "\r")
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen])
	}
	return s
}

// recordSnapshotForFile records a snapshot in the calling session's shared
// internal/hashline store using Record(absPath, norm, nil, false) — matching
// omp's `recordSnapshot(path, fullText)` (crates/edit/src/store.rs:260,
// ast-edit.ts:372, 522, ast-grep.ts:330) — so no authored/unrestricted sentinel
// is attached and a later partial read or RecordSeenLinesFromBody restricts
// provenance to only the displayed lines. Returns the 4-hex tag, or "" on
// oversized/unreadable files.
func recordSnapshotForFile(ctx sdk.Context, absPath string) string {
	info, err := os.Stat(absPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSnapshotFileBytes {
		return ""
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return ""
	}
	_, rest := hashline.StripBOM(string(data))
	norm := hashline.NormalizeToLF(rest)
	return hashline.ForSession(ctx).Record(absPath, norm, nil, false)
}
