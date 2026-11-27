package memoryskills

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	// maxSubdirHintBytes caps each injected subdirectory context file at 32 KiB.
	maxSubdirHintBytes = 32 * 1024
	// maxSubdirAncestorWalk bounds how many ancestor directories are walked per accessed path.
	maxSubdirAncestorWalk = 5
)

// subdirHintFilenames lists candidate hint filenames in priority order (first match wins per dir).
var subdirHintFilenames = []string{
	"AGENTS.override.md",
	"AGENTS.md",
	"agents.md",
	"CLAUDE.md",
	"claude.md",
	".cursorrules",
}

// subdirHintTools are the file-access tools that trigger progressive subdirectory hint discovery.
var subdirHintTools = map[string]bool{
	"read_file":    true,
	"write_file":   true,
	"patch":        true,
	"search_files": true,
}

// subdirPathArgKeys are tool argument keys that carry a target file or directory path.
var subdirPathArgKeys = []string{"path", "file_path", "workdir"}

// subdirExcludedDirNames are dependency/cache/build directories that hold copies rather than
// authoritative project context files.
var subdirExcludedDirNames = map[string]bool{
	".git":          true,
	".hg":           true,
	".svn":          true,
	"node_modules":  true,
	"vendor":        true,
	".venv":         true,
	"venv":          true,
	"__pycache__":   true,
	"dist":          true,
	"build":         true,
	"target":        true,
	".next":         true,
	".nuxt":         true,
	".cache":        true,
	".pytest_cache": true,
	".mypy_cache":   true,
	".tox":          true,
}

// SubdirectoryHintTracker tracks directories visited by file-access tools in a session
// and discovers nested context files on first access without modifying the system prompt.
type SubdirectoryHintTracker struct {
	mu            sync.Mutex
	rootDir       string
	homeLike      bool
	loadedDirs    map[string]bool
	loadedDigests map[string]bool
}

// NewSubdirectoryHintTracker creates a tracker rooted at workingDir and pre-seeds it with
// workingDir plus any root context files already injected by the system prompt.
func NewSubdirectoryHintTracker(workingDir string) *SubdirectoryHintTracker {
	t := &SubdirectoryHintTracker{
		loadedDirs:    make(map[string]bool),
		loadedDigests: make(map[string]bool),
	}
	t.Reset(workingDir)
	return t
}

// Reset re-anchors the tracker to workingDir (e.g. on session start) and seeds root digests.
func (t *SubdirectoryHintTracker) Reset(workingDir string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.loadedDirs = make(map[string]bool)
	t.loadedDigests = make(map[string]bool)
	t.rootDir = ""
	t.homeLike = false

	dir := strings.TrimSpace(workingDir)
	if dir == "" {
		return
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(absDir); err == nil {
		absDir = resolved
	}
	absDir = filepath.Clean(absDir)

	t.rootDir = absDir
	t.homeLike = isHomeLikeWorkingDir(absDir)
	t.loadedDirs[absDir] = true

	// Seed digests of any root project context files already injected in the system prompt
	// so they are never duplicated as subdirectory hints.
	for _, cf := range DiscoverProjectContextFiles(absDir) {
		if trimmed := strings.TrimSpace(cf.Content); trimmed != "" {
			t.loadedDigests[sha256Hex(trimmed)] = true
		}
		if cf.Path != "" {
			if dirPath := filepath.Clean(filepath.Dir(cf.Path)); dirPath != "" {
				if resolvedDir, err := filepath.EvalSymlinks(dirPath); err == nil {
					dirPath = filepath.Clean(resolvedDir)
				}
				t.loadedDirs[dirPath] = true
			}
		}
	}
	// Also seed the first hint file directly in workingDir if present.
	if _, content, ok := firstHintFileInDir(absDir, absDir); ok && content != "" {
		t.loadedDigests[sha256Hex(content)] = true
	}
}

// CheckToolCall inspects a tool call's name and input arguments, discovering any newly
// visited subdirectory context files. Returns formatted <subdirectory_context> blocks
// (or empty string if nothing new was discovered).
func (t *SubdirectoryHintTracker) CheckToolCall(toolName string, input map[string]any) string {
	if !subdirHintTools[strings.TrimSpace(toolName)] {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.rootDir == "" || t.homeLike || len(input) == 0 {
		return ""
	}

	candidates := t.extractCandidateDirsLocked(input)
	if len(candidates) == 0 {
		return ""
	}

	var blocks []string
	for _, dir := range candidates {
		if block := t.loadHintForDirLocked(dir); block != "" {
			blocks = append(blocks, block)
		}
	}
	if len(blocks) == 0 {
		return ""
	}
	return strings.Join(blocks, "\n\n")
}

func (t *SubdirectoryHintTracker) extractCandidateDirsLocked(input map[string]any) []string {
	seen := make(map[string]bool)
	var dirs []string

	for _, key := range subdirPathArgKeys {
		raw, ok := input[key].(string)
		if !ok {
			continue
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		t.collectAncestorsLocked(raw, seen, &dirs)
	}
	// Stable shallowest-to-deepest ordering so parent hints appear before child hints.
	sort.Slice(dirs, func(i, j int) bool {
		if len(dirs[i]) != len(dirs[j]) {
			return len(dirs[i]) < len(dirs[j])
		}
		return dirs[i] < dirs[j]
	})
	return dirs
}

func (t *SubdirectoryHintTracker) collectAncestorsLocked(rawPath string, seen map[string]bool, out *[]string) {
	p := expandTildePath(rawPath)
	if !filepath.IsAbs(p) {
		p = filepath.Join(t.rootDir, p)
	}
	p = filepath.Clean(p)

	// Resolve symlinks if the target (or its parent) exists.
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = filepath.Clean(resolved)
	} else if parentResolved, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		p = filepath.Join(filepath.Clean(parentResolved), filepath.Base(p))
	}

	// Determine the starting directory: if p is a file or has a file extension / doesn't exist as a dir, start at parent.
	info, err := os.Stat(p)
	if err == nil {
		if !info.IsDir() {
			p = filepath.Dir(p)
		}
	} else {
		p = filepath.Dir(p)
	}
	p = filepath.Clean(p)

	for range maxSubdirAncestorWalk {
		if t.loadedDirs[p] {
			break
		}
		if t.isValidSubdirLocked(p) && !seen[p] {
			seen[p] = true
			*out = append(*out, p)
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
}

func (t *SubdirectoryHintTracker) isValidSubdirLocked(dir string) bool {
	if dir == "" || t.loadedDirs[dir] {
		return false
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	if !isPathWithinRoot(dir, t.rootDir) {
		return false
	}
	return !isExcludedSubdir(dir, t.rootDir)
}

func (t *SubdirectoryHintTracker) loadHintForDirLocked(dir string) string {
	t.loadedDirs[dir] = true
	if t.homeLike || !isPathWithinRoot(dir, t.rootDir) {
		return ""
	}

	hintPath, content, ok := firstHintFileInDir(dir, t.rootDir)
	if !ok || content == "" {
		return ""
	}

	digest := sha256Hex(content)
	if t.loadedDigests[digest] {
		return ""
	}
	t.loadedDigests[digest] = true

	relPath := relOrBase(t.rootDir, hintPath)
	content = truncateHintBytes(content, relPath, maxSubdirHintBytes)
	return fmt.Sprintf("<subdirectory_context path=%q>\n%s\n</subdirectory_context>", relPath, content)
}

// registerSubdirHintsHook registers the sdk.EventToolResult hook (plus session-start reset)
// on ext so progressive subdirectory hints are appended to file-access tool results
// without mutating the cached system prompt.
func registerSubdirHintsHook(ext *sdk.Extension) {
	tracker := NewSubdirectoryHintTracker("")

	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		tracker.Reset(safeCwd(ctx))
		return nil, nil
	})

	ext.OnEvent(sdk.EventToolResult, func(ctx sdk.Context, data map[string]any) (any, error) {
		if data == nil {
			return nil, nil
		}
		toolName, _ := data["toolName"].(string)
		if !subdirHintTools[strings.TrimSpace(toolName)] {
			return nil, nil
		}
		// Lazily initialize tracker if session_start didn't fire (e.g. unit test or resumed session).
		tracker.mu.Lock()
		needsInit := tracker.rootDir == ""
		tracker.mu.Unlock()
		if needsInit {
			if cwd := safeCwd(ctx); cwd != "" {
				tracker.Reset(cwd)
			}
		}

		input, _ := data["input"].(map[string]any)
		hint := tracker.CheckToolCall(toolName, input)
		if hint == "" {
			return nil, nil
		}
		modified := appendHintToToolResultContent(data["content"], hint)
		if modified == nil {
			return nil, nil
		}
		return map[string]any{"content": modified}, nil
	})
}

// appendHintToToolResultContent appends a hint block to a PiG tool_result content payload,
// supporting both []any content block arrays (PiG standard) and plain string content.
func appendHintToToolResultContent(existing any, hint string) any {
	if hint == "" {
		return nil
	}
	switch v := existing.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return hint
		}
		return v + "\n\n" + hint
	case []any:
		out := make([]any, len(v), len(v)+1)
		copy(out, v)
		out = append(out, map[string]any{
			"type": "text",
			"text": "\n\n" + hint,
		})
		return out
	case []map[string]any:
		out := make([]any, 0, len(v)+1)
		for _, item := range v {
			out = append(out, item)
		}
		out = append(out, map[string]any{
			"type": "text",
			"text": "\n\n" + hint,
		})
		return out
	default:
		return []any{
			map[string]any{
				"type": "text",
				"text": hint,
			},
		}
	}
}

// firstHintFileInDir returns (hintPath, trimmedContent, true) for the first readable,
// symlink-contained, non-empty hint file in dir.
func firstHintFileInDir(dir, rootDir string) (string, string, bool) {
	for _, name := range subdirHintFilenames {
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		target, ok := resolveContainedHintTarget(candidate, rootDir)
		if !ok {
			continue
		}
		raw, err := os.ReadFile(target)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(strings.TrimPrefix(string(raw), "\ufeff"))
		if content == "" {
			continue
		}
		return candidate, content, true
	}
	return "", "", false
}

// resolveContainedHintTarget resolves symlinks on candidate and verifies the target
// stays strictly inside rootDir (preventing symlink escapes like sub/AGENTS.md -> ~/.aws/credentials).
func resolveContainedHintTarget(candidate, rootDir string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false
	}
	resolved = filepath.Clean(resolved)
	resolvedRoot := filepath.Clean(rootDir)
	if r, err := filepath.EvalSymlinks(resolvedRoot); err == nil {
		resolvedRoot = filepath.Clean(r)
	}
	if !isPathWithinRoot(resolved, resolvedRoot) {
		return "", false
	}
	return resolved, true
}

func isPathWithinRoot(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isExcludedSubdir(dir, rootDir string) bool {
	rel, err := filepath.Rel(rootDir, dir)
	if err != nil || rel == "." {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if subdirExcludedDirNames[part] {
			return true
		}
	}
	return false
}

func isHomeLikeWorkingDir(absDir string) bool {
	clean := filepath.Clean(absDir)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		homeClean := filepath.Clean(home)
		if resolvedHome, err := filepath.EvalSymlinks(homeClean); err == nil {
			homeClean = filepath.Clean(resolvedHome)
		}
		if clean == homeClean {
			return true
		}
	}
	switch filepath.ToSlash(clean) {
	case "/", "/home", "/root", "/workspace":
		return true
	}
	for _, part := range strings.Split(filepath.ToSlash(clean), "/") {
		if part == ".home" || part == "host-home" || part == "host_home" {
			return true
		}
	}
	return false
}

func expandTildePath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func truncateHintBytes(content, relPath string, maxBytes int) string {
	if maxBytes <= 0 || len(content) <= maxBytes {
		return content
	}
	headBytes := (maxBytes * 70) / 100
	tailBytes := (maxBytes * 20) / 100
	if headBytes+tailBytes >= len(content) {
		return content
	}
	marker := fmt.Sprintf(
		"\n\n[...truncated %s: kept %d+%d of %d bytes. Read the full file with read_file(%q) if needed.]\n\n",
		relPath, headBytes, tailBytes, len(content), relPath,
	)
	return content[:headBytes] + marker + content[len(content)-tailBytes:]
}
