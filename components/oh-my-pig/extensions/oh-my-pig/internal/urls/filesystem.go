package urls

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// upstream: packages/coding-agent/src/internal-urls/filesystem-resource.ts

// UrlContainmentError is returned when an internal URL resolves outside its scheme root.
type UrlContainmentError struct {
	Msg string
}

func (e *UrlContainmentError) Error() string { return e.Msg }

// contentTypeForPath infers MIME type from a file extension: markdown, JSON, or plain text.
// upstream: filesystem-resource.ts contentTypeForPath.
func contentTypeForPath(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".md", ".markdown", ".mdx", ".mkd", ".mkdn":
		return "text/markdown"
	case ".json":
		return "application/json"
	default:
		return "text/plain"
	}
}

// validateRelativePath rejects absolute paths and `..` traversal segments.
// upstream: filesystem-resource.ts validateRelativePath.
func validateRelativePath(relativePath, scheme string) error {
	if filepath.IsAbs(relativePath) || strings.HasPrefix(relativePath, "/") || strings.HasPrefix(relativePath, "\\") {
		return fmt.Errorf("Absolute paths are not allowed in %s:// URLs", scheme)
	}
	parts := strings.FieldsFunc(relativePath, func(r rune) bool { return r == '/' || r == '\\' })
	for _, p := range parts {
		if p == ".." {
			return fmt.Errorf("Path traversal (..) is not allowed in %s:// URLs", scheme)
		}
	}
	cleaned := filepath.ToSlash(filepath.Clean(relativePath))
	if strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, "/../") || strings.Contains(cleaned, "/..") {
		return fmt.Errorf("Path traversal (..) is not allowed in %s:// URLs", scheme)
	}
	return nil
}

// ensureWithinRoot returns UrlContainmentError unless targetPath is rootPath or lies beneath it.
// upstream: filesystem-resource.ts ensureWithinRoot.
func ensureWithinRoot(targetPath, rootPath, scheme, rawURL string) error {
	cleanTarget := filepath.Clean(targetPath)
	cleanRoot := filepath.Clean(rootPath)
	if cleanTarget == cleanRoot {
		return nil
	}
	prefix := cleanRoot
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if !strings.HasPrefix(cleanTarget, prefix) {
		suffix := ""
		if rawURL != "" {
			suffix = ": " + rawURL
		}
		return &UrlContainmentError{Msg: fmt.Sprintf("%s:// URL escapes %s root%s", scheme, scheme, suffix)}
	}
	return nil
}

// containedRealPath resolves an existing targetPath under an already-realpathed realRoot,
// checking lexical target, real parent, and real path for containment.
// Returns ("", false, nil) when target does not exist.
// upstream: filesystem-resource.ts containedRealPath.
func containedRealPath(targetPath, realRoot, scheme, rawURL string) (string, bool, error) {
	if err := ensureWithinRoot(targetPath, realRoot, scheme, rawURL); err != nil {
		return "", false, err
	}
	if filepath.Clean(targetPath) != filepath.Clean(realRoot) {
		parent := filepath.Dir(targetPath)
		realParent, err := filepath.EvalSymlinks(parent)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return "", false, err
			}
		} else {
			if err := ensureWithinRoot(realParent, realRoot, scheme, rawURL); err != nil {
				return "", false, err
			}
		}
	}
	realTarget, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	if err := ensureWithinRoot(realTarget, realRoot, scheme, rawURL); err != nil {
		return "", false, err
	}
	return realTarget, true, nil
}

// ensureCreatableWithinRoot verifies that creating targetPath under realRoot stays inside
// realRoot: the deepest existing ancestor must realpath inside realRoot, and no entry along
// the way may be a dangling symlink, symlink loop, or non-directory file.
// upstream: filesystem-resource.ts ensureCreatableWithinRoot.
func ensureCreatableWithinRoot(targetPath, realRoot, scheme, rawURL string) error {
	if err := ensureWithinRoot(targetPath, realRoot, scheme, rawURL); err != nil {
		return err
	}
	cleanRoot := filepath.Clean(realRoot)
	for current := filepath.Clean(targetPath); ; current = filepath.Dir(current) {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			return ensureWithinRoot(resolved, cleanRoot, scheme, rawURL)
		}
		if isSymlinkLoopErr(err) {
			return &UrlContainmentError{Msg: fmt.Sprintf("%s:// URL goes through a symlink loop: %s", scheme, rawURL)}
		}
		if isNotDirErr(err) {
			return fmt.Errorf("%s:// URL goes through a file, not a directory: %s", scheme, rawURL)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if fi, lerr := os.Lstat(current); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			return &UrlContainmentError{Msg: fmt.Sprintf("%s:// URL goes through a dangling symlink: %s", scheme, rawURL)}
		} else if lerr != nil && !errors.Is(lerr, os.ErrNotExist) {
			return lerr
		}
		parent := filepath.Dir(current)
		if current == cleanRoot || parent == current {
			return nil
		}
	}
}

type dirListEntry struct {
	name  string
	isDir bool
}

// formatDirectoryListing formats directories first (`name/`), then files, sorted by name.
// upstream: filesystem-resource.ts formatDirectoryListing.
func formatDirectoryListing(entries []dirListEntry) string {
	if len(entries) == 0 {
		return "(empty directory)"
	}
	cp := append([]dirListEntry(nil), entries...)
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].isDir != cp[j].isDir {
			return cp[i].isDir
		}
		return compareLocale(cp[i].name, cp[j].name) < 0
	})
	lines := make([]string, len(cp))
	for i, e := range cp {
		if e.isDir {
			lines[i] = e.name + "/"
		} else {
			lines[i] = e.name
		}
	}
	return strings.Join(lines, "\n")
}

// buildDirectoryResource builds an immutable text resource listing a filesystem directory.
// upstream: filesystem-resource.ts buildDirectoryResource.
func buildDirectoryResource(rawURL, dirPath string, notes []string) (*Resource, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}
	list := make([]dirListEntry, 0, len(entries))
	for _, e := range entries {
		list = append(list, dirListEntry{name: e.Name(), isDir: e.IsDir()})
	}
	content := formatDirectoryListing(list)
	immutable := true
	return &Resource{
		URL:         rawURL,
		Content:     content,
		ContentType: "text/plain",
		SourcePath:  dirPath,
		Immutable:   &immutable,
		IsDirectory: true,
		Notes:       notes,
	}, nil
}

// formatByteSize formats a byte count in human-readable form.
func formatByteSize(n int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(gb))
	case n >= mb:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(mb))
	case n >= kb:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(kb))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// compareLocale provides case-insensitive ordering with case tie-breaker
// matching JS String.prototype.localeCompare for ASCII names.
func compareLocale(a, b string) int {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		if la < lb {
			return -1
		}
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
