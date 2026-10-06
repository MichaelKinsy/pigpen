// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The GitHub interceptor's working halves: the clone and API paths, and the local-clone content rendering. upstream:
// providers/interceptors/github.ts GitHubInterceptor (fetchGitHub, cloneRepo, fetchViaApi and its probes) and the
// module-scope helpers (buildTree, buildDirListing, readReadme, generateCloneContent, isBinaryFile, formatFileSize,
// resolveWithinRepo).
//
// The external commands sit behind one seam, `githubRunner`, so every rendering and decision path is a pure function a
// twin can drive, and no twin needs gh, git or the network.

// The rendering limits. upstream: github.ts MAX_TREE_ENTRIES, MAX_INLINE_FILE_CHARS.
const (
	maxTreeEntries     = 200
	maxInlineFileChars = 100000
	readmeCharLimit    = 8192
)

// The command timeouts. upstream: the timeout values on each execFile call.
const (
	ghVersionTimeout = 5 * time.Second
	ghAPITimeout     = 10 * time.Second
	ghTreeTimeout    = 15 * time.Second
)

// githubRunner runs the external commands the interceptor shells out to. upstream: the execFile calls, whose results
// are nil-on-error everywhere.
type githubRunner interface {
	// ghAvailable probes the gh CLI once; the interceptor caches the answer.
	ghAvailable() bool
	// ghJSON runs one `gh api` query and returns its stdout, or "" when it failed.
	ghJSON(path, jq string, timeoutSeconds float64) string
	// ghRaw runs one `gh api` query and returns its raw stdout, which is what a base64 payload needs.
	ghRaw(path, jq string, timeoutSeconds float64, maxBufferBytes int) string
	// clone runs one clone command into localPath, reporting whether it succeeded. The path is passed separately
	// rather than derived from argv: the gh form ends its argv with the flags after --, so the last argument is
	// not the destination. upstream: execClone(args, localPath, timeoutMs).
	clone(args []string, localPath string, timeoutSeconds float64) bool
}

// execRunner is the production runner, shelling out to gh and git exactly as the original does. upstream: the
// execFile calls in github.ts.
type execRunner struct {
}

// ghAvailableRunner is the probe with its error, so a test can skip rather than fail when gh is absent.
func (r execRunner) ghAvailableRunner() (bool, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return false, err
	}
	return true, nil
}

func (r execRunner) ghAvailable() bool {
	if _, err := exec.LookPath("gh"); err != nil {
		return false
	}
	_, err := r.run("gh", []string{"--version"}, ghVersionTimeout)
	return err == nil
}

func (r execRunner) ghJSON(path, jq string, timeoutSeconds float64) string {
	return r.ghRaw(path, jq, timeoutSeconds, 0)
}

func (r execRunner) ghRaw(path, jq string, timeoutSeconds float64, maxBufferBytes int) string {
	args := []string{"api", path, "--jq", jq}
	out, err := r.run("gh", args, ghAPITimeout)
	if err != nil {
		return ""
	}
	return out
}

func (r execRunner) clone(args []string, localPath string, timeoutSeconds float64) bool {
	if len(args) == 0 {
		return false
	}
	if _, err := r.run(args[0], args[1:], time.Duration(timeoutSeconds*float64(time.Second))); err != nil {
		// A partial clone is removed, so a later attempt starts clean. upstream: the rmSync in the error branch.
		_ = os.RemoveAll(localPath)
		return false
	}
	return true
}

// run executes a command under a deadline and returns its stdout, treating any failure as the nil result the original
// maps everything to. upstream: the execFile calls with their timeout options in github.ts.
func (r execRunner) run(name string, args []string, timeout time.Duration) (string, error) {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

// cachedClone is one in-flight or finished clone. upstream: CachedClone.
type cachedClone struct {
	localPath   string
	cloneResult string
	ok          bool
}

// cloneCacheKey names a clone by owner, repo and optional ref. upstream: cacheKey.
func cloneCacheKey(owner, repo, ref string, hasRef bool) string {
	if hasRef {
		return owner + "/" + repo + "@" + ref
	}
	return owner + "/" + repo
}

// cloneDir is where a clone lands: <clonePath>/<owner>/<repo>[@<ref>]. upstream: cloneDir.
func cloneDir(clonePath, owner, repo, ref string, hasRef bool) string {
	dirName := repo
	if hasRef {
		dirName = repo + "@" + ref
	}
	return filepath.Join(clonePath, owner, dirName)
}

// noiseDirs are the directories a tree walk skips, because they are build output rather than source. upstream:
// github.ts NOISE_DIRS.
var noiseDirs = map[string]bool{
	"node_modules": true, "vendor": true, ".next": true, "dist": true, "build": true,
	"__pycache__": true, ".venv": true, "venv": true, ".tox": true, ".mypy_cache": true,
	".pytest_cache": true, "target": true, ".gradle": true, ".idea": true, ".vscode": true,
}

// binaryExtensions are the extensions that mark a file binary before its bytes are even read. upstream: github.ts
// BINARY_EXTENSIONS.
var binaryExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true, ".ico": true, ".webp": true,
	".svg": true, ".tiff": true, ".tif": true, ".mp3": true, ".mp4": true, ".avi": true, ".mov": true,
	".mkv": true, ".flv": true, ".wmv": true, ".wav": true, ".ogg": true, ".webm": true, ".flac": true,
	".aac": true, ".zip": true, ".tar": true, ".gz": true, ".bz2": true, ".xz": true, ".7z": true,
	".rar": true, ".zst": true, ".exe": true, ".dll": true, ".so": true, ".dylib": true, ".bin": true,
	".o": true, ".a": true, ".lib": true, ".woff": true, ".woff2": true, ".ttf": true, ".otf": true,
	".eot": true, ".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true,
	".pptx": true, ".sqlite": true, ".db": true, ".sqlite3": true, ".pyc": true, ".pyo": true,
	".class": true, ".jar": true, ".war": true, ".iso": true, ".img": true, ".dmg": true,
}

// isBinaryFile reports whether a path is binary, by extension first and then by the first 512 bytes, which is what
// catches a binary blob without an extension. upstream: github.ts isBinaryFile.
func isBinaryFile(filePath string) bool {
	if binaryExtensions[strings.ToLower(filepath.Ext(filePath))] {
		return true
	}
	// Only the first 512 bytes are inspected, so a multi-gigabyte blob costs the same as a small one: the original
	// reads a fixed 512-byte buffer, while os.ReadFile would take the whole file into memory first.
	file, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer file.Close()
	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && n == 0 {
		return false
	}
	for _, b := range buf[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}

// formatFileSize renders a size the way the original does: bytes, one decimal of KB, one of MB. upstream: github.ts
// formatFileSize.
func formatFileSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return strconv.FormatInt(bytes, 10) + " B"
	case bytes < 1024*1024:
		return strconv.FormatFloat(float64(bytes)/1024, 'f', 1, 64) + " KB"
	default:
		return strconv.FormatFloat(float64(bytes)/(1024*1024), 'f', 1, 64) + " MB"
	}
}

// resolveWithinRepo keeps a path inside the clone, refusing anything that escapes it, including through a symlink that
// points outside once resolved. upstream: github.ts resolveWithinRepo.
func resolveWithinRepo(rootPath, relativePath string) (string, bool) {
	normalizedRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return "", false
	}
	candidate := filepath.Join(normalizedRoot, relativePath)
	if !strings.HasPrefix(candidate, normalizedRoot+string(filepath.Separator)) && candidate != normalizedRoot {
		return "", false
	}
	if _, err := os.Lstat(candidate); err != nil {
		return candidate, true
	}
	realRoot, err := filepath.EvalSymlinks(normalizedRoot)
	if err != nil {
		return "", false
	}
	realCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false
	}
	if realCandidate == realRoot {
		return candidate, true
	}
	if !strings.HasPrefix(realCandidate, realRoot+string(filepath.Separator)) {
		return "", false
	}
	return candidate, true
}

// buildTree is the sorted, noise-free structure listing, capped at 200 entries with a truncation line. upstream:
// github.ts buildTree.
func buildTree(rootPath string) string {
	entries := []string{}
	var walk func(dir, relPath string)
	walk = func(dir, relPath string) {
		if len(entries) >= maxTreeEntries {
			return
		}
		items, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		names := make([]string, 0, len(items))
		for _, item := range items {
			names = append(names, item.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			if len(entries) >= maxTreeEntries {
				return
			}
			if name == ".git" {
				continue
			}
			rel := name
			if relPath != "" {
				rel = relPath + "/" + name
			}
			safePath, ok := resolveWithinRepo(rootPath, rel)
			if !ok {
				entries = append(entries, rel+"  [outside repo skipped]")
				continue
			}
			stat, err := os.Lstat(safePath)
			if err != nil {
				continue
			}
			if stat.IsDir() {
				if noiseDirs[name] {
					entries = append(entries, rel+"/  [skipped]")
					continue
				}
				entries = append(entries, rel+"/")
				walk(safePath, rel)
				continue
			}
			entries = append(entries, rel)
		}
	}
	walk(rootPath, "")
	if len(entries) >= maxTreeEntries {
		entries = append(entries, fmt.Sprintf("... (truncated at %d entries)", maxTreeEntries))
	}
	return strings.Join(entries, "\n")
}

// buildDirListing is the listing of one directory, with sizes and the escape notices. upstream: github.ts
// buildDirListing.
func buildDirListing(rootPath, subPath string) string {
	targetPath, ok := resolveWithinRepo(rootPath, subPath)
	if !ok {
		return "(path escapes repository root)"
	}
	items, err := os.ReadDir(targetPath)
	if err != nil {
		return "(directory not readable)"
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name())
	}
	sort.Strings(names)
	lines := []string{}
	for _, name := range names {
		if name == ".git" {
			continue
		}
		rel := name
		if subPath != "" {
			rel = subPath + "/" + name
		}
		safePath, inside := resolveWithinRepo(rootPath, rel)
		if !inside {
			lines = append(lines, "  "+name+"  (outside repo)")
			continue
		}
		stat, err := os.Lstat(safePath)
		if err != nil {
			lines = append(lines, "  "+name+"  (unreadable)")
			continue
		}
		if stat.IsDir() {
			lines = append(lines, "  "+name+"/")
			continue
		}
		lines = append(lines, "  "+name+"  ("+formatFileSize(stat.Size())+")")
	}
	return strings.Join(lines, "\n")
}

// readReadme is the first README it finds among the five names upstream tries, truncated at 8K characters. upstream:
// github.ts readReadme.
func readReadme(localPath string) (string, bool) {
	for _, name := range []string{"README.md", "readme.md", "README", "README.txt", "README.rst"} {
		content, err := os.ReadFile(filepath.Join(localPath, name))
		if err != nil {
			continue
		}
		text := string(content)
		if len(text) > readmeCharLimit {
			return text[:readmeCharLimit] + "\n\n[README truncated at 8K chars]", true
		}
		return text, true
	}
	return "", false
}

// generateCloneContent is the model-facing body for a clone: the tree and README for a root, a directory listing for a
// tree URL, and the file (or a binary/unreadable note) for a blob URL. upstream: github.ts generateCloneContent.
func generateCloneContent(localPath string, info gitHubURLInfo) string {
	lines := []string{"Repository cloned to: " + localPath, ""}

	switch info.Type {
	case githubURLRoot:
		lines = append(lines, "## Structure", buildTree(localPath), "")
		if readme, ok := readReadme(localPath); ok {
			lines = append(lines, "## README.md", readme, "")
		}
		lines = append(lines, "Use `read` and `bash` tools at the path above to explore further.")
		return strings.Join(lines, "\n")

	case githubURLTree:
		dirPath := info.Path
		fullDirPath, ok := resolveWithinRepo(localPath, dirPath)
		if !ok {
			return generateCloneFallback(localPath, lines, dirPath)
		}
		if _, err := os.Stat(fullDirPath); err != nil {
			return generateCloneFallback(localPath, lines, dirPath)
		}
		heading := dirPath
		if heading == "" {
			heading = "/"
		}
		lines = append(lines, "## "+heading, buildDirListing(localPath, dirPath), "")
		lines = append(lines, "Use `read` and `bash` tools at the path above to explore further.")
		return strings.Join(lines, "\n")

	default: // blob
		filePath := info.Path
		fullFilePath, ok := resolveWithinRepo(localPath, filePath)
		if !ok {
			return generateCloneFallback(localPath, lines, filePath)
		}
		// The original checks existence separately from the path guard, so a missing file takes the not-found
		// fallback while an inspection failure takes the "could not inspect" branch below.
		if _, err := os.Stat(fullFilePath); err != nil {
			return generateCloneFallback(localPath, lines, filePath)
		}
		stat, err := os.Lstat(fullFilePath)
		if err != nil {
			lines = append(lines, fmt.Sprintf("Could not inspect `%s`: %s", filePath, err),
				"", "Use `read` and `bash` tools at the path above to explore further.")
			return strings.Join(lines, "\n")
		}
		if stat.IsDir() {
			lines = append(lines, "## "+filePath, buildDirListing(localPath, filePath), "")
			lines = append(lines, "Use `read` and `bash` tools at the path above to explore further.")
			return strings.Join(lines, "\n")
		}
		if isBinaryFile(fullFilePath) {
			ext := strings.TrimPrefix(filepath.Ext(filePath), ".")
			lines = append(lines, "## "+filePath,
				fmt.Sprintf("Binary file (%s, %s). Use `read` or `bash` tools at the path above to inspect.", ext, formatFileSize(stat.Size())))
			return strings.Join(lines, "\n")
		}
		content, err := os.ReadFile(fullFilePath)
		if err != nil {
			lines = append(lines, "Could not read `"+filePath+"` as UTF-8 text.", "",
				"Use `read` and `bash` tools at the path above to explore further.")
			return strings.Join(lines, "\n")
		}
		text := string(content)
		lines = append(lines, "## "+filePath)
		if len(text) > maxInlineFileChars {
			lines = append(lines, text[:maxInlineFileChars],
				fmt.Sprintf("\n[File truncated at 100K chars. Full file: %s]", fullFilePath))
		} else {
			lines = append(lines, text)
		}
		lines = append(lines, "", "Use `read` and `bash` tools at the path above to explore further.")
		return strings.Join(lines, "\n")
	}
}

// generateCloneFallback is the shared "that path is not in the clone" body: the root tree instead. upstream: the three
// identical not-found branches in generateCloneContent.
func generateCloneFallback(localPath string, lines []string, missingPath string) string {
	return strings.Join(append(lines,
		fmt.Sprintf("Path `%s` not found in clone. Showing repository root instead.", missingPath),
		"",
		"## Structure",
		buildTree(localPath),
		"",
		"Use `read` and `bash` tools at the path above to explore further.",
	), "\n")
}

// decodeBase64Content is the README and file payload the API returns base64-encoded, truncated at 8K for a README. upstream:
// the Buffer.from(stdout.trim(), "base64") decoding in the API helpers.
func decodeBase64Content(encoded string, limit int) (string, bool) {
	trimmed := strings.TrimSpace(encoded)
	if trimmed == "" {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return "", false
	}
	if limit > 0 && len(decoded) > limit {
		return string(decoded[:limit]) + "\n\n[README truncated at 8K chars]", true
	}
	return string(decoded), true
}

// treeViaAPI renders the recursive tree listing, capped with the total-count line. upstream: fetchTreeViaApi.
func treeViaAPI(listing string) (string, bool) {
	paths := []string{}
	for _, line := range strings.Split(strings.TrimSpace(listing), "\n") {
		if line != "" {
			paths = append(paths, line)
		}
	}
	if len(paths) == 0 {
		return "", false
	}
	if len(paths) > maxTreeEntries {
		return strings.Join(paths[:maxTreeEntries], "\n") +
			fmt.Sprintf("\n... (%d total entries)", len(paths)), true
	}
	return strings.Join(paths, "\n"), true
}

// repoSizeMB converts the API's kilobyte size to megabytes. upstream: the sizeKB / 1024 comparison in fetchGitHub.
func repoSizeMB(sizeKB float64) float64 { return sizeKB / 1024 }
