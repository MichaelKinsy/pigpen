package search

import (
	"context"
	"strings"
)

// escapeGlobMetachars mirrors packages/coding-agent/src/tools/path-utils.ts:1409-1411.
func escapeGlobMetachars(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for i := range len(value) {
		switch value[i] {
		case '*', '?', '[', '{':
			b.WriteByte('[')
			b.WriteByte(value[i])
			b.WriteByte(']')
		default:
			b.WriteByte(value[i])
		}
	}
	return b.String()
}

// FindSuffixMatches walks root using the shared glob walker (`walkDir`) with omp's
// unique-suffix glob options (`pattern: "**/" + escapeGlobMetachars(normalized)`,
// `hidden: true`, `gitignore: true`, `recursive: true`, `includeDir: true`,
// `includeSymlinks: true`), stopping early once 2 matches are found.
// Returns relative forward-slash paths from root.
// upstream: packages/coding-agent/src/tools/path-utils.ts:1413-1445, crates/xfs/src/glob.rs:79-80,239-262
func FindSuffixMatches(ctx context.Context, root, rawPath string) ([]string, error) {
	normalized := strings.ReplaceAll(rawPath, "\\", "/")
	normalized = strings.TrimPrefix(normalized, "./")
	normalized = strings.TrimRight(normalized, "/")
	if normalized == "" {
		return nil, nil
	}

	pattern := "**/" + escapeGlobMetachars(normalized)
	matcher, err := compileGlob(pattern)
	if err != nil {
		return nil, err
	}

	entries, err := walkDir(ctx, root, walkOptions{
		hidden:             true,
		gitignore:          true,
		recursive:          true,
		includeSymlinks:    true,
		include:            matcher,
		includeDir:         true,
		includeNodeModules: strings.Contains(pattern, "node_modules"),
		includeGit:         pathMentionsSegment(pattern, ".git"),
		maxResults:         2,
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.relPath)
	}
	return out, nil
}
