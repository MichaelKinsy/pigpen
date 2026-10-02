package svc

import (
	"path/filepath"
	"strings"

	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"
)

// RelativeWatchPath is path relative to root in slash form: "" for the root itself, and ok=false
// for a sibling, a parent or a prefix lookalike.
func RelativeWatchPath(root, path string) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return filepath.ToSlash(rel), true
}

// IsExcluded reports whether any exclude pattern matches path. The root ("") is never excluded.
func IsExcluded(path string, excludes []string) bool {
	if path == "" {
		return false
	}
	for _, pattern := range excludes {
		if matchesGlob(path, pattern) {
			return true
		}
	}
	return false
}

// MatchesPatterns applies include alternatives and exclude precedence; the root itself is always
// in scope, filters describe its descendants.
func MatchesPatterns(path string, includes, excludes []string) bool {
	if path == "" {
		return true
	}
	if IsExcluded(path, excludes) {
		return false
	}
	if len(includes) == 0 {
		return true
	}
	for _, pattern := range includes {
		if matchesGlob(path, pattern) {
			return true
		}
	}
	return false
}

// MergeChange coalesces two consecutive changes to one path inside a batch. The empty string is
// "nothing pending" both as input and as the result (a transient path that came and went).
func MergeChange(previous, next ahptypes.ResourceChangeType) ahptypes.ResourceChangeType {
	switch previous {
	case "":
		return next
	case ahptypes.ResourceChangeTypeAdded:
		if next == ahptypes.ResourceChangeTypeDeleted {
			return ""
		}
		return ahptypes.ResourceChangeTypeAdded
	case ahptypes.ResourceChangeTypeDeleted:
		if next == ahptypes.ResourceChangeTypeDeleted {
			return ahptypes.ResourceChangeTypeDeleted
		}
		return ahptypes.ResourceChangeTypeUpdated
	}
	if next == ahptypes.ResourceChangeTypeDeleted {
		return ahptypes.ResourceChangeTypeDeleted
	}
	return ahptypes.ResourceChangeTypeUpdated
}
