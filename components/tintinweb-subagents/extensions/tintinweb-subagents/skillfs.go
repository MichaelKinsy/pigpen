package tintinweb_subagents

import (
	"os"
	"regexp"
)

// The file helpers of src/memory.ts that skill preloading uses. (Persistent agent memory, which owns them upstream, is
// a separate slice with the same names; on merge keep one copy.)

var safeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// isUnsafeName reports a name with characters not allowed in agent and skill names: only alphanumerics, hyphens,
// underscores and dots, no leading dot, at most 128 characters. upstream: memory.ts isUnsafeName.
func isUnsafeName(name string) bool {
	if name == "" || len(name) > 128 {
		return true
	}
	return !safeName.MatchString(name)
}

// isSymlink reports a symlink (defense against symlink attacks). upstream: memory.ts isSymlink.
func isSymlink(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

// safeReadFile reads a file, refusing symlinks; ok is false for a file that does not exist, is a symlink or cannot be
// read. upstream: memory.ts safeReadFile.
func safeReadFile(path string) (string, bool) {
	if _, err := os.Stat(path); err != nil || isSymlink(path) {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}
