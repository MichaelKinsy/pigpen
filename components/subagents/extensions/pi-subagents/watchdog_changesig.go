package pi_subagents

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// The repo change signature (watchdog/change-signature.ts): a hash of what the working tree holds that changed since HEAD,
// so the reviewer runs when the code changed, not when the conversation did.

const (
	wdDefaultMaxHashFileBytes   = 64 * 1024 * 1024
	wdDefaultMaxHashTotalBytes  = 64 * 1024 * 1024
	wdDefaultMaxHashEntries     = 2_000
	wdTrackedEntryProbeMaxBytes = 1_024
	wdGitMaxBuffer              = 10 * 1024 * 1024
)

var (
	wdIgnoredChangePrefixes = []string{projectSubagentsRelativeDir + "/", "tmp/", "node_modules/"}
	wdIgnoredChangePaths    = []string{projectSubagentsRelativeDir, "tmp", "node_modules"}
	wdIgnoredChangeSegments = []string{".git", "node_modules"}
)

// The file operations of the hashing, variables so a test can fail them as the original's tests patch node:fs.
var (
	wdLstat = os.Lstat
	wdWarn  = func(message string) { fmt.Fprintln(os.Stderr, message) }
	// wdHashFile is the sha256 of a file's content.
	wdHashFile = func(filePath string) (string, error) {
		f, err := os.Open(filePath)
		if err != nil {
			return "", err
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}
)

// errWdFileTooLarge is ERR_FS_FILE_TOO_LARGE: a file Node refuses to read into one buffer (it falls back to metadata quietly).
var errWdFileTooLarge = errors.New("file too large to read")

func wdPositiveEnv(name string, fallback float64) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return fallback
	}
	return v
}

type watchdogRepoChangeSignature struct {
	Root         string
	Key          string
	ChangedPaths []string
}

func wdGitRun(cwd string, args ...string) (string, bool) {
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", false
	}
	if out.Len() > wdGitMaxBuffer {
		return "", false
	}
	return out.String(), true
}

func wdComparablePath(value string) string {
	resolved, err := filepath.Abs(value)
	if err != nil {
		resolved = value
	}
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = real
	}
	if runtime.GOOS == "windows" {
		return strings.ToLower(resolved)
	}
	return resolved
}

func wdIsHomeRepoRoot(root string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	return wdComparablePath(root) == wdComparablePath(home)
}

// wdHasTrackedEntries probes the index without asking Git to inspect the working tree. The output cap keeps this bounded for a
// repository with a very large index; a result over the cap still proves that a tracked path exists. ok is false when git failed.
func wdHasTrackedEntries(root string) (has, ok bool) {
	cmd := exec.Command("git", "-C", root, "ls-files", "--cached", "--error-unmatch", "--", ".")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, false
	}
	if err := cmd.Start(); err != nil {
		return false, false
	}
	n, _ := io.Copy(io.Discard, io.LimitReader(stdout, wdTrackedEntryProbeMaxBytes+1))
	if n > wdTrackedEntryProbeMaxBytes {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return true, true
	}
	err = cmd.Wait()
	if err == nil {
		return true, true
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, true
	}
	return false, false
}

func wdNormalizeRelPath(value string) string {
	value = strings.ReplaceAll(value, string(filepath.Separator), "/")
	return strings.TrimPrefix(value, "./")
}

func wdIgnoredRelPath(rel string) bool {
	n := wdNormalizeRelPath(rel)
	if wdContains(wdIgnoredChangePaths, n) {
		return true
	}
	for _, p := range wdIgnoredChangePrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	for _, seg := range strings.Split(n, "/") {
		if wdContains(wdIgnoredChangeSegments, seg) {
			return true
		}
	}
	return false
}

type wdHashBudget struct {
	entries, maxEntries int
	bytes, maxBytes     float64
}

func (b *wdHashBudget) useEntry() bool {
	if b.entries >= b.maxEntries {
		return false
	}
	b.entries++
	return true
}

func wdLargeFileHash(info fs.FileInfo) string {
	return "large:" + strconv.FormatInt(info.Size(), 10) + ":" + strconv.FormatInt(info.ModTime().UnixMilli(), 10)
}

func wdFileEntry(normalized, fullPath string, info fs.FileInfo, budget *wdHashBudget) *jsObject {
	var hash string
	size := float64(info.Size())
	if size > wdPositiveEnv("PI_SUBAGENTS_MAX_HASH_FILE_BYTES", wdDefaultMaxHashFileBytes) || budget.bytes+size > budget.maxBytes {
		hash = wdLargeFileHash(info)
	} else {
		h, err := wdHashFile(fullPath)
		if err == nil {
			hash = h
			budget.bytes += size
		} else if errors.Is(err, fs.ErrNotExist) {
			// A file racing away between lstat and read: mirror the lstat ENOENT path.
			return wdMsg("path", normalized, "state", "deleted")
		} else {
			// Any other read failure (too large, EACCES, EISDIR, ...) degrades to the metadata marker so one unreadable
			// file never discards the whole signature.
			hash = wdLargeFileHash(info)
			if !errors.Is(err, errWdFileTooLarge) {
				wdWarn("[pi-subagents] watchdog hashFile fell back to metadata for " + normalized + ": " + err.Error())
			}
		}
	}
	return wdMsg("path", normalized, "state", "file", "mode", float64(info.Mode().Perm()), "size", size, "hash", hash)
}

func wdGitWorktreeEntry(normalized, fullPath string) *jsObject {
	status, _ := wdGitRun(fullPath, "status", "--porcelain=v1", "-z", "--untracked-files=no")
	o := wdMsg("path", normalized, "state", "git-worktree")
	if head, ok := wdGitRun(fullPath, "rev-parse", "HEAD"); ok {
		o.set("head", jsTrim(head))
	} else {
		o.set("head", undef{})
	}
	o.set("dirty", status != "")
	if status != "" {
		o.set("statusKey", wdSHA256(status))
	} else {
		o.set("statusKey", undef{})
	}
	return o
}

func wdHashPath(root, relPath string, budget *wdHashBudget) (*jsObject, error) {
	normalized := wdNormalizeRelPath(relPath)
	if !budget.useEntry() {
		return wdMsg("path", normalized, "state", "skipped", "reason", "entry-limit"), nil
	}
	fullPath := filepath.Join(root, filepath.FromSlash(normalized))
	info, err := wdLstat(fullPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return wdMsg("path", normalized, "state", "deleted"), nil
		}
		return nil, err
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(fullPath)
		if err != nil {
			return nil, err
		}
		return wdMsg("path", normalized, "state", "symlink", "target", target), nil
	case info.IsDir():
		if _, err := os.Stat(filepath.Join(fullPath, ".git")); err == nil {
			return wdGitWorktreeEntry(normalized, fullPath), nil
		}
		dirEntries, err := os.ReadDir(fullPath)
		if err != nil {
			return nil, err
		}
		var entries []string
		for _, e := range dirEntries {
			entry := wdNormalizeRelPath(path.Join(normalized, e.Name()))
			if !wdIgnoredRelPath(entry) {
				entries = append(entries, entry)
			}
		}
		sort.Strings(entries)
		remaining := budget.maxEntries - budget.entries
		if remaining < 0 {
			remaining = 0
		}
		selected := entries
		if len(selected) > remaining {
			selected = selected[:remaining]
		}
		children := make([]any, 0, len(selected)+1)
		for _, entry := range selected {
			child, err := wdHashPath(root, entry, budget)
			if err != nil {
				return nil, err
			}
			children = append(children, child)
		}
		if len(selected) < len(entries) {
			children = append(children, wdMsg("path", normalized, "state", "skipped-children", "reason", "entry-limit", "count", float64(len(entries)-len(selected))))
		}
		return wdMsg("path", normalized, "state", "dir", "entries", children), nil
	case info.Mode().IsRegular():
		return wdFileEntry(normalized, fullPath, info, budget), nil
	}
	return wdMsg("path", normalized, "state", "other", "mode", float64(uint32(info.Mode()))), nil
}

type wdPorcelainEntry struct {
	status string
	paths  []string
}

func wdParsePorcelainZ(raw string) []wdPorcelainEntry {
	var tokens []string
	for _, t := range strings.Split(raw, "\x00") {
		if t != "" {
			tokens = append(tokens, t)
		}
	}
	var entries []wdPorcelainEntry
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if len(token) < 4 {
			continue
		}
		status, rel := token[:2], token[3:]
		paths := []string{rel}
		if status[0] == 'R' || status[0] == 'C' {
			i++
			if i < len(tokens) && tokens[i] != "" {
				paths = append(paths, tokens[i])
			}
		}
		entries = append(entries, wdPorcelainEntry{status, paths})
	}
	return entries
}

func wdBuildRepoChangeSignature(root, statusOutput string) (*watchdogRepoChangeSignature, error) {
	var entries []wdPorcelainEntry
	for _, e := range wdParsePorcelainZ(statusOutput) {
		var paths []string
		for _, p := range e.paths {
			if n := wdNormalizeRelPath(p); !wdIgnoredRelPath(n) {
				paths = append(paths, n)
			}
		}
		if len(paths) > 0 {
			entries = append(entries, wdPorcelainEntry{e.status, paths})
		}
	}
	sortKey := func(e wdPorcelainEntry) string { return e.status + " " + strings.Join(e.paths, "\x00") }
	sort.SliceStable(entries, func(i, j int) bool { return localeCompare(sortKey(entries[i]), sortKey(entries[j])) < 0 })
	seen := map[string]bool{}
	changed := []string{}
	for _, e := range entries {
		for _, p := range e.paths {
			if !seen[p] {
				seen[p] = true
				changed = append(changed, p)
			}
		}
	}
	sort.Strings(changed)
	budget := &wdHashBudget{maxEntries: int(wdPositiveEnv("PI_SUBAGENTS_MAX_HASH_ENTRIES", wdDefaultMaxHashEntries)), maxBytes: wdPositiveEnv("PI_SUBAGENTS_MAX_HASH_TOTAL_BYTES", wdDefaultMaxHashTotalBytes)}
	payload := make([]any, 0, len(entries))
	for _, e := range entries {
		paths := make([]any, len(e.paths))
		content := make([]any, len(e.paths))
		for i, p := range e.paths {
			paths[i] = p
			c, err := wdHashPath(root, p, budget)
			if err != nil {
				return nil, err
			}
			content[i] = c
		}
		payload = append(payload, wdMsg("status", e.status, "paths", paths, "content", content))
	}
	return &watchdogRepoChangeSignature{Root: root, Key: wdSHA256(marshalJSON(payload, "")), ChangedPaths: changed}, nil
}

func computeWatchdogRepoChangeSignature(cwd string) *watchdogRepoChangeSignature {
	top, ok := wdGitRun(cwd, "rev-parse", "--show-toplevel")
	root := jsTrim(top)
	if !ok || root == "" {
		return nil
	}
	hasTracked, probed := wdHasTrackedEntries(root)
	skipUntracked := wdIsHomeRepoRoot(root) || (probed && !hasTracked)
	untracked := "all"
	if skipUntracked {
		untracked = "no"
	}
	statusOutput, ok := wdGitRun(root, "status", "--porcelain=v1", "-z", "--untracked-files="+untracked)
	if !ok {
		return nil
	}
	sig, err := wdBuildRepoChangeSignature(root, statusOutput)
	if err != nil {
		wdWarn("[pi-subagents] watchdog repo change signature failed: " + err.Error())
		return nil
	}
	return sig
}

func wdToolNameFromMessage(message any) string {
	if s, ok := wdNullish(wdPropV(message, "toolName"), wdPropV(message, "name")).(string); ok {
		return s
	}
	return ""
}

func wdMessageIndicatesRepoEdit(message any) bool {
	if !wdIsObject(message) {
		return false
	}
	role := wdPropV(message, "role")
	if role != "toolResult" && role != "tool" {
		return false
	}
	name := wdToolNameFromMessage(message)
	_, hasError := wdProp(message, "error")
	return (name == "edit" || name == "write") && wdPropV(message, "isError") != true && !hasError
}

// eventIndicatesRepoEdit reports an event that carries a successful edit or write tool result.
func eventIndicatesRepoEdit(event any) bool {
	if !wdIsObject(event) {
		return false
	}
	evType := func(name string) bool { return wdPropV(event, "type") == name || wdPropV(event, "event") == name }
	if evType("turn_end") {
		candidates := []any{wdPropV(event, "message")}
		if results, ok := wdPropV(event, "toolResults").([]any); ok {
			candidates = append(candidates, results...)
		}
		for _, c := range candidates {
			if wdMessageIndicatesRepoEdit(c) {
				return true
			}
		}
		return false
	}
	if evType("tool_result") {
		merged := wdMsg("role", "toolResult")
		for _, k := range event.(*jsObject).order() {
			merged.set(k, event.(*jsObject).vals[k])
		}
		return wdMessageIndicatesRepoEdit(merged)
	}
	if !evType("tool_result_end") {
		return false
	}
	return wdMessageIndicatesRepoEdit(wdPropV(event, "message"))
}
