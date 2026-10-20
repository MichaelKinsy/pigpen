package pi_subagents

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"os"
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

func wdPositiveEnv(name string, fallback float64) float64 { panic("not implemented") }

type watchdogRepoChangeSignature struct {
	Root         string
	Key          string
	ChangedPaths []string
}

func wdGitRun(cwd string, args ...string) (string, bool) { panic("not implemented") }

func wdComparablePath(value string) string { panic("not implemented") }

func wdIsHomeRepoRoot(root string) bool { panic("not implemented") }

// wdHasTrackedEntries probes the index without asking Git to inspect the working tree. The output cap keeps this bounded for a
// repository with a very large index; a result over the cap still proves that a tracked path exists. ok is false when git failed.
func wdHasTrackedEntries(root string) (has, ok bool) { panic("not implemented") }

func wdNormalizeRelPath(value string) string { panic("not implemented") }

func wdIgnoredRelPath(rel string) bool { panic("not implemented") }

type wdHashBudget struct {
	entries, maxEntries int
	bytes, maxBytes     float64
}

func (b *wdHashBudget) useEntry() bool { panic("not implemented") }

func wdLargeFileHash(info fs.FileInfo) string { panic("not implemented") }

func wdFileEntry(normalized, fullPath string, info fs.FileInfo, budget *wdHashBudget) *jsObject {
	panic("not implemented")
}

// A file racing away between lstat and read: mirror the lstat ENOENT path.

// Any other read failure (too large, EACCES, EISDIR, ...) degrades to the metadata marker so one unreadable
// file never discards the whole signature.

func wdGitWorktreeEntry(normalized, fullPath string) *jsObject { panic("not implemented") }

func wdHashPath(root, relPath string, budget *wdHashBudget) (*jsObject, error) {
	panic("not implemented")
}

type wdPorcelainEntry struct {
	status string
	paths  []string
}

func wdParsePorcelainZ(raw string) []wdPorcelainEntry { panic("not implemented") }

func wdBuildRepoChangeSignature(root, statusOutput string) (*watchdogRepoChangeSignature, error) {
	panic("not implemented")
}

func computeWatchdogRepoChangeSignature(cwd string) *watchdogRepoChangeSignature {
	panic("not implemented")
}

func wdToolNameFromMessage(message any) string { panic("not implemented") }

func wdMessageIndicatesRepoEdit(message any) bool { panic("not implemented") }

// eventIndicatesRepoEdit reports an event that carries a successful edit or write tool result.
func eventIndicatesRepoEdit(event any) bool { panic("not implemented") }
