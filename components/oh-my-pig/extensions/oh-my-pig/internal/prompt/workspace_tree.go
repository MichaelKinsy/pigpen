package prompt

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/search"
)

// upstream: packages/coding-agent/src/workspace-tree.ts:1-353
// upstream: crates/natives/src/workspace.rs:1-348

const (
	// AgentsMdLimit is the hard cap on AGENTS.md files surfaced by BuildWorkspaceTree.
	// upstream: packages/coding-agent/src/workspace-tree.ts:17
	AgentsMdLimit = 200

	workspaceDefaultMaxDepth    = 3
	workspaceDefaultPerDirLimit = 12
	workspaceDefaultLineCap     = 120
	workspaceMaxEntries         = 100_000
	agentsMdMinDepth            = 1
	agentsMdMaxDepth            = 4
	agentsMdFilename            = "AGENTS.md"
)

// excludedWorkspaceDirs mirrors crates/natives/src/workspace.rs:33-45.
var excludedWorkspaceDirs = map[string]bool{
	"node_modules":  true,
	".git":          true,
	".next":         true,
	"dist":          true,
	"build":         true,
	"target":        true,
	".venv":         true,
	".cache":        true,
	".turbo":        true,
	".parcel-cache": true,
	"coverage":      true,
}

// DirectoryTree is the rendered directory tree output.
// upstream: packages/coding-agent/src/workspace-tree.ts:19-24
type DirectoryTree struct {
	RootPath   string
	Rendered   string
	Truncated  bool
	TotalLines int
}

// WorkspaceTree extends DirectoryTree with nested AGENTS.md files discovered in the same walk.
// upstream: packages/coding-agent/src/workspace-tree.ts:26-29
type WorkspaceTree struct {
	DirectoryTree
	AgentsMdFiles []string
}

// BuildWorkspaceTreeOptions configures BuildWorkspaceTree.
// upstream: packages/coding-agent/src/workspace-tree.ts:42-45
type BuildWorkspaceTreeOptions struct {
	// Timeout cancels the walk after this duration when > 0.
	Timeout time.Duration
}

// WorkspaceEntry is one filesystem entry returned by a workspace scan.
type WorkspaceEntry struct {
	Path    string // forward-slash path relative to root
	IsDir   bool
	MtimeMs int64
	Size    int64
}

// BuildWorkspaceTree builds the workspace tree shown in the system prompt plus
// directory-scoped AGENTS.md files (depths 1..=4, capped at AgentsMdLimit).
// Best-effort: returns an empty tree on root scan failure.
// upstream: packages/coding-agent/src/workspace-tree.ts:97-123
func BuildWorkspaceTree(cwd string, opts BuildWorkspaceTreeOptions) WorkspaceTree {
	rootPath, err := filepath.Abs(cwd)
	if err != nil {
		return WorkspaceTree{DirectoryTree: DirectoryTree{RootPath: cwd}}
	}
	ctx := context.Background()
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	scan, err := scanWorkspace(ctx, scanConfig{
		root:              rootPath,
		maxDepth:          workspaceDefaultMaxDepth,
		walkMaxDepth:      max(workspaceDefaultMaxDepth, agentsMdMaxDepth),
		includeHidden:     false,
		useGitignore:      true,
		pruneExcludedDirs: true,
		collectAgentsMd:   true,
	})
	if err != nil {
		return WorkspaceTree{DirectoryTree: DirectoryTree{RootPath: rootPath}}
	}

	perDir := workspaceDefaultPerDirLimit
	lineCap := workspaceDefaultLineCap
	tree := assembleTree(rootPath, scan.entries, assembleOptions{
		perDirLimit:     &perDir,
		rootLimit:       &perDir,
		lineCap:         &lineCap,
		nativeTruncated: scan.truncated,
	})
	return WorkspaceTree{
		DirectoryTree: tree,
		AgentsMdFiles: scan.agentsMdFiles,
	}
}

// ─── scanner ────────────────────────────────────────────────────────────────

type scanConfig struct {
	root              string
	maxDepth          int
	walkMaxDepth      int
	includeHidden     bool
	useGitignore      bool
	pruneExcludedDirs bool
	collectAgentsMd   bool
}

type scanResult struct {
	entries       []WorkspaceEntry
	agentsMdFiles []string
	truncated     bool
}

func scanWorkspace(ctx context.Context, cfg scanConfig) (scanResult, error) {
	info, err := os.Stat(cfg.root)
	if err != nil {
		return scanResult{}, fmt.Errorf("directory scan failed for %s: %w", cfg.root, err)
	}
	if !info.IsDir() {
		return scanResult{}, fmt.Errorf("directory scan failed for %s: not a directory", cfg.root)
	}

	var res scanResult
	agentsSeen := map[string]bool{}
	var rootMatcher *search.IgnoreMatcher
	if cfg.useGitignore {
		rootMatcher = search.NewIgnoreMatcher(cfg.root)
	}

	var walk func(absDir, relDir string, depth int, matcher *search.IgnoreMatcher) error
	walk = func(absDir, relDir string, depth int, matcher *search.IgnoreMatcher) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		dirEntries, err := os.ReadDir(absDir)
		if err != nil {
			return nil
		}

		for _, de := range dirEntries {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			name := de.Name()
			if name == ".DS_Store" {
				continue
			}
			relPath := name
			if relDir != "" {
				relPath = relDir + "/" + name
			}
			absPath := filepath.Join(absDir, name)

			isSymlink := de.Type()&os.ModeSymlink != 0
			isDir := de.IsDir()
			if !cfg.includeHidden && strings.HasPrefix(name, ".") {
				continue
			}
			if cfg.pruneExcludedDirs && isDir && excludedWorkspaceDirs[name] {
				continue
			}

			isAgentsMd := cfg.collectAgentsMd && name == agentsMdFilename && !isDir && isFileOrFileSymlink(absPath, isSymlink)
			if cfg.useGitignore && matcher != nil && matcher.IsIgnored(relPath, isDir) {
				// File-level ignore rules do not hide AGENTS.md, but ignored directories are pruned.
				if !isAgentsMd {
					continue
				}
			}

			if isAgentsMd {
				dirDepth := depth - 1
				if dirDepth >= agentsMdMinDepth && dirDepth <= agentsMdMaxDepth && !agentsSeen[relPath] {
					agentsSeen[relPath] = true
					res.agentsMdFiles = append(res.agentsMdFiles, relPath)
				}
			}

			if depth <= cfg.maxDepth {
				if len(res.entries) >= workspaceMaxEntries {
					res.truncated = true
				} else {
					var mtimeMs int64
					var size int64
					if fi, fiErr := de.Info(); fiErr == nil {
						mtimeMs = fi.ModTime().UnixMilli()
						// Match crates/natives/src/workspace.rs: regular files report their byte size;
						// directories and symlinks report 0 (never the lstat symlink target-path length).
						if !isDir && !isSymlink && fi.Mode().IsRegular() {
							size = fi.Size()
						}
					}
					res.entries = append(res.entries, WorkspaceEntry{
						Path:    relPath,
						IsDir:   isDir,
						MtimeMs: mtimeMs,
						Size:    size,
					})
				}
			}

			if isDir && !isSymlink {
				if depth < cfg.walkMaxDepth {
					nextMatcher := matcher
					if cfg.useGitignore && matcher != nil {
						nextMatcher = matcher.EnterDir(absPath, relPath)
					}
					if err := walk(absPath, relPath, depth+1, nextMatcher); err != nil {
						return err
					}
				} else if cfg.collectAgentsMd && depth >= agentsMdMinDepth && depth <= agentsMdMaxDepth {
					// Probe `<dir>/AGENTS.md` at walk depth limit (upstream workspace.rs:196-209).
					cand := filepath.Join(absPath, agentsMdFilename)
					if st, stErr := os.Stat(cand); stErr == nil && st.Mode().IsRegular() {
						candRel := relPath + "/" + agentsMdFilename
						if !agentsSeen[candRel] {
							agentsSeen[candRel] = true
							res.agentsMdFiles = append(res.agentsMdFiles, candRel)
						}
					}
				}
			}
		}
		return nil
	}

	if err := walk(cfg.root, "", 1, rootMatcher); err != nil {
		return scanResult{}, err
	}
	sort.Strings(res.agentsMdFiles)
	if len(res.agentsMdFiles) > AgentsMdLimit {
		res.agentsMdFiles = res.agentsMdFiles[:AgentsMdLimit]
		res.truncated = true
	}
	return res, nil
}

func isFileOrFileSymlink(absPath string, isSymlink bool) bool {
	if !isSymlink {
		return true
	}
	st, err := os.Stat(absPath)
	return err == nil && st.Mode().IsRegular()
}

// ─── tree assembly and rendering ────────────────────────────────────────────

type wsNode struct {
	name         string
	isDir        bool
	mtimeMs      int64
	size         int64
	depth        int
	children     []*wsNode
	droppedCount int
}

type wsRenderedLine struct {
	label  string
	depth  int
	isRoot bool
	size   string
	age    string
}

type assembleOptions struct {
	perDirLimit     *int
	rootLimit       *int
	lineCap         *int
	nativeTruncated bool
}

// compareLocaleWS mirrors JS String.prototype.localeCompare for filename tie-breaking
// (case-insensitive primary order with lowercase-before-uppercase tie-break, symbols like `_`
// sorting before letters as in ICU en-US default collation).
// upstream: packages/coding-agent/src/workspace-tree.ts:252-254
func compareLocaleWS(a, b string) int {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		// Compare rune-by-rune with non-alphanumeric punctuation (e.g. `_`, `-`, `.`)
		// ordering before alphanumeric characters, matching en-US ICU collation.
		ra, rb := []rune(la), []rune(lb)
		n := min(len(ra), len(rb))
		for i := range n {
			if ra[i] == rb[i] {
				continue
			}
			pa, pb := wsCollationRank(ra[i]), wsCollationRank(rb[i])
			if pa != pb {
				if pa < pb {
					return -1
				}
				return 1
			}
			if ra[i] < rb[i] {
				return -1
			}
			return 1
		}
		if len(ra) < len(rb) {
			return -1
		}
		return 1
	}
	// Primary-equal strings: JS en-US localeCompare sorts lowercase before uppercase ("alpha.txt" < "Alpha.txt").
	ra, rb := []rune(a), []rune(b)
	n := min(len(ra), len(rb))
	for i := range n {
		if ra[i] == rb[i] {
			continue
		}
		aLower := ra[i] >= 'a' && ra[i] <= 'z'
		bLower := rb[i] >= 'a' && rb[i] <= 'z'
		if aLower != bLower {
			if aLower {
				return -1
			}
			return 1
		}
		if ra[i] < rb[i] {
			return -1
		}
		return 1
	}
	if len(ra) < len(rb) {
		return -1
	}
	if len(ra) > len(rb) {
		return 1
	}
	return 0
}

func wsCollationRank(r rune) int {
	switch {
	case r == '_':
		return 0
	case r == '-':
		return 1
	case r == '.':
		return 2
	case r < '0' || (r > '9' && r < 'a') || (r > 'z' && r < 128):
		return 3
	case r >= '0' && r <= '9':
		return 4
	default:
		return 5
	}
}

func assembleTree(rootPath string, entries []WorkspaceEntry, opts assembleOptions) DirectoryTree {
	byParent := make(map[string][]*wsNode)
	for _, entry := range entries {
		slash := strings.LastIndexByte(entry.Path, '/')
		name := entry.Path
		parentPath := ""
		if slash >= 0 {
			name = entry.Path[slash+1:]
			parentPath = entry.Path[:slash]
		}
		depth := 1
		if parentPath != "" {
			depth = strings.Count(parentPath, "/") + 2
		}
		node := &wsNode{
			name:    name,
			isDir:   entry.IsDir,
			mtimeMs: entry.MtimeMs,
			size:    entry.Size,
			depth:   depth,
		}
		byParent[parentPath] = append(byParent[parentPath], node)
	}

	root := &wsNode{name: ".", isDir: true, depth: 0}
	truncated := opts.nativeTruncated

	type stackItem struct {
		node    *wsNode
		relPath string
	}
	stack := []stackItem{{node: root, relPath: ""}}
	for len(stack) > 0 {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		all := append([]*wsNode(nil), byParent[item.relPath]...)
		sort.Slice(all, func(i, j int) bool {
			if all[i].mtimeMs != all[j].mtimeMs {
				return all[i].mtimeMs > all[j].mtimeMs
			}
			return compareLocaleWS(all[i].name, all[j].name) < 0
		})

		limit := opts.perDirLimit
		if item.node.depth == 0 {
			limit = opts.rootLimit
		}
		if limit != nil && len(all) > *limit {
			lim := *limit
			if lim <= 1 {
				if lim < 0 {
					lim = 0
				}
				item.node.children = all[:lim]
			} else {
				kept := make([]*wsNode, 0, lim)
				kept = append(kept, all[:lim-1]...)
				kept = append(kept, all[len(all)-1])
				item.node.children = kept
			}
			item.node.droppedCount = len(all) - lim
			truncated = true
		} else {
			item.node.children = all
		}

		for _, child := range item.node.children {
			if !child.isDir {
				continue
			}
			childRel := child.name
			if item.relPath != "" {
				childRel = item.relPath + "/" + child.name
			}
			stack = append(stack, stackItem{node: child, relPath: childRel})
		}
	}

	var rawLines []wsRenderedLine
	renderWSNode(root, &rawLines)
	lines, elidedCount := applyWSLineCap(rawLines, opts.lineCap)

	return DirectoryTree{
		RootPath:   rootPath,
		Rendered:   formatWSLines(lines),
		Truncated:  truncated || elidedCount > 0,
		TotalLines: len(lines),
	}
}

// formatMtimeStable formats UTC `YYYY-MM-DD HH:MM` (cache-stable across sessions).
// upstream: packages/coding-agent/src/workspace-tree.ts:277-280
func formatMtimeStable(mtimeMs int64) string {
	if mtimeMs == 0 {
		return ""
	}
	return time.UnixMilli(mtimeMs).UTC().Format("2006-01-02 15:04")
}

// formatFixed1HalfUp matches JS Number.prototype.toFixed(1) round-half-away-from-zero
// (unlike Go's fmt.Sprintf("%.1f") which uses IEEE round-half-to-even: e.g. 2.25 -> 2.3, 1.25 -> 1.3).
// upstream: packages/utils/src/format.ts:62-67
func formatFixed1HalfUp(v float64) string {
	tenths := math.Floor(v*10.0 + 0.5)
	return fmt.Sprintf("%.1f", tenths/10.0)
}

func formatWSBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return formatFixed1HalfUp(float64(n)/1024.0) + "KB"
	case n < 1024*1024*1024:
		return formatFixed1HalfUp(float64(n)/(1024.0*1024.0)) + "MB"
	default:
		return formatFixed1HalfUp(float64(n)/(1024.0*1024.0*1024.0)) + "GB"
	}
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

func padEndUTF16(s string, width int) string {
	l := utf16Len(s)
	if l >= width {
		return s
	}
	return s + strings.Repeat(" ", width-l)
}

func renderWSNode(node *wsNode, out *[]wsRenderedLine) {
	if node.depth == 0 {
		*out = append(*out, wsRenderedLine{label: node.name, depth: 0, isRoot: true})
	} else {
		indent := strings.Repeat("  ", node.depth)
		suffix := ""
		if node.isDir {
			suffix = "/"
		}
		size := ""
		if !node.isDir {
			size = formatWSBytes(node.size)
		}
		*out = append(*out, wsRenderedLine{
			label:  fmt.Sprintf("%s- %s%s", indent, node.name, suffix),
			depth:  node.depth,
			isRoot: false,
			size:   size,
			age:    formatMtimeStable(node.mtimeMs),
		})
	}

	if node.droppedCount == 0 {
		for _, child := range node.children {
			renderWSNode(child, out)
		}
		return
	}

	recent := node.children[:len(node.children)-1]
	oldest := node.children[len(node.children)-1]
	for _, child := range recent {
		renderWSNode(child, out)
	}
	childDepth := node.depth + 1
	*out = append(*out, wsRenderedLine{
		label:  fmt.Sprintf("%s- … %d more", strings.Repeat("  ", childDepth), node.droppedCount),
		depth:  childDepth,
		isRoot: false,
	})
	if oldest != nil {
		renderWSNode(oldest, out)
	}
}

func applyWSLineCap(lines []wsRenderedLine, lineCap *int) ([]wsRenderedLine, int) {
	if lineCap == nil || len(lines) <= *lineCap {
		return append([]wsRenderedLine(nil), lines...), 0
	}
	const protectedDepth = 1
	target := *lineCap - 1
	if target < 1 {
		target = 1
	}
	type indexed struct {
		line  wsRenderedLine
		index int
	}
	var removable []indexed
	for i, l := range lines {
		if !l.isRoot && l.depth > protectedDepth {
			removable = append(removable, indexed{line: l, index: i})
		}
	}
	if len(removable) == 0 {
		return append([]wsRenderedLine(nil), lines...), 0
	}
	sort.Slice(removable, func(i, j int) bool {
		if removable[i].line.depth != removable[j].line.depth {
			return removable[i].line.depth > removable[j].line.depth
		}
		return removable[i].index > removable[j].index
	})
	need := len(lines) - target
	if need < len(removable) {
		removable = removable[:need]
	}
	removed := make(map[int]bool, len(removable))
	for _, item := range removable {
		removed[item.index] = true
	}
	kept := make([]wsRenderedLine, 0, len(lines)-len(removed)+1)
	for i, l := range lines {
		if !removed[i] {
			kept = append(kept, l)
		}
	}
	kept = append(kept, wsRenderedLine{
		label:  fmt.Sprintf("[…%dln elided…]", len(removable)),
		depth:  0,
		isRoot: false,
	})
	return kept, len(removable)
}

func formatWSLines(lines []wsRenderedLine) string {
	maxLabel := 0
	for _, l := range lines {
		if w := utf16Len(l.label); w > maxLabel {
			maxLabel = w
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if l.age == "" {
			out[i] = l.label
			continue
		}
		labelPad := padEndUTF16(l.label, maxLabel+2)
		sizePad := padEndUTF16(l.size, 8)
		agePad := padEndUTF16(l.age, 4)
		out[i] = strings.TrimRight(fmt.Sprintf("%s%s  %s", labelPad, sizePad, agePad), " ")
	}
	return strings.Join(out, "\n")
}

// RenderDirContext renders the `<dir-context>` block for nested AGENTS.md files.
// upstream: packages/coding-agent/src/prompts/system/project-prompt.md:18-24
func RenderDirContext(agentsMdFiles []string) string {
	if len(agentsMdFiles) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<dir-context>\nSome directories may have rules; deeper rules override higher ones.\nBefore changes in these directories, MUST read:")
	for _, f := range agentsMdFiles {
		b.WriteString("\n- ")
		b.WriteString(f)
	}
	b.WriteString("\n</dir-context>")
	return b.String()
}

// RenderWorkspaceTreeBlock renders the `<workspace-tree>` block when includeWorkspaceTree is enabled.
// upstream: packages/coding-agent/src/prompts/system/project-prompt.md:30-40
func RenderWorkspaceTreeBlock(tree WorkspaceTree, hasGlob, hasRead bool) string {
	if tree.Rendered == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("<workspace-tree>\nWorking-directory layout: newest mtime first; depth ≤ 3.\n")
	b.WriteString(tree.Rendered)
	if tree.Truncated && hasGlob && hasRead {
		b.WriteString("\nSome entries elided to shorten tree — use `glob`/`read` to drill in.")
	}
	b.WriteString("\n</workspace-tree>")
	return b.String()
}
