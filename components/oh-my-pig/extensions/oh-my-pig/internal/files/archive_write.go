package files

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
)

const (
	// MaxArchiveInMemoryBytes matches omp's DEFAULT_ARCHIVE_LIMITS.maxInMemorySize (256 MiB).
	// upstream: packages/utils/src/ar/limits.ts:30
	MaxArchiveInMemoryBytes = 256 * 1024 * 1024
	// MaxArchivePathBytes matches omp's DEFAULT_ARCHIVE_LIMITS.maxPathBytes (4096).
	// upstream: packages/utils/src/ar/limits.ts:34
	MaxArchivePathBytes = 4096
	// MaxArchiveLinkDepth matches omp's DEFAULT_ARCHIVE_LIMITS.maxLinkDepth (40).
	// upstream: packages/utils/src/ar/limits.ts:35
	MaxArchiveLinkDepth = 40
	// archivePathErrorPreviewBytes matches omp's PATH_ERROR_PREVIEW_BYTES (256).
	// upstream: packages/utils/src/ar/paths.ts:3
	archivePathErrorPreviewBytes = 256
	// ZIP Unix/macOS host OS identifiers and mode masks.
	// upstream: packages/utils/src/ar/zip.ts:43-47
	zipUnixHost      = 3
	zipOSXHost       = 19
	zipFileTypeMask  = 0o170000
	zipDirectoryType = 0o040000
	zipSymlinkType   = 0o120000
)

// archiveFormatExt pairs a lowercase extension (without leading dot) with its
// omp ArchiveFormat name. Ordered longest-first so `.tar.gz` matches before `.gz`.
// upstream: packages/utils/src/ar/registry.ts:33-63
type archiveFormatExt struct {
	ext    string
	format string
}

var archiveExtensionTable = func() []archiveFormatExt {
	byFormat := []struct {
		format string
		exts   []string
	}{
		{"zip", []string{"zip", "jar", "war", "ear", "apk", "whl", "ipa", "xpi", "vsix", "nupkg", "cbz"}},
		{"tar", []string{"tar"}},
		{"tar.gz", []string{"tar.gz", "tgz"}},
		{"tar.bz2", []string{"tar.bz2", "tbz2", "tbz"}},
		{"tar.xz", []string{"tar.xz", "txz"}},
		{"tar.zst", []string{"tar.zst", "tzst"}},
		{"tar.Z", []string{"tar.z"}},
		{"asar", []string{"asar"}},
		{"rar", []string{"rar", "cbr"}},
		{"7z", []string{"7z"}},
		{"iso", []string{"iso"}},
		{"cab", []string{"cab"}},
		{"cpio", []string{"cpio"}},
		{"rpm", []string{"rpm"}},
		{"ar", []string{"ar", "a", "lib"}},
		{"deb", []string{"deb"}},
		{"lzh", []string{"lzh", "lha"}},
		{"arj", []string{"arj"}},
		{"gz", []string{"gz"}},
		{"bz2", []string{"bz2"}},
		{"xz", []string{"xz"}},
		{"zst", []string{"zst"}},
		{"Z", []string{"z"}},
		{"lzma", []string{"lzma"}},
	}
	var out []archiveFormatExt
	for _, entry := range byFormat {
		for _, ext := range entry.exts {
			out = append(out, archiveFormatExt{ext: ext, format: entry.format})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return len(out[i].ext) > len(out[j].ext)
	})
	return out
}()

// archiveFormatFromPath infers omp's ArchiveFormat from a filesystem path's extension.
// upstream: packages/utils/src/ar/registry.ts:73-79
func archiveFormatFromPath(filePath string) string {
	lower := strings.ToLower(filePath)
	for _, entry := range archiveExtensionTable {
		if strings.HasSuffix(lower, "."+entry.ext) {
			return entry.format
		}
	}
	return ""
}

// isStdlibWritableArchiveFormat reports whether this Go-stdlib port can write format.
func isStdlibWritableArchiveFormat(format string) bool {
	switch format {
	case "zip", "tar", "tar.gz":
		return true
	default:
		return false
	}
}

// isOmpWritableArchiveFormat reports whether upstream omp can write format.
// upstream: packages/utils/src/ar/write.ts:9-20
func isOmpWritableArchiveFormat(format string) bool {
	switch format {
	case "zip", "tar", "tar.gz", "tar.zst", "asar":
		return true
	default:
		return false
	}
}

// archivePathCandidate is one `<archive.ext>:<subPath>` split candidate.
// upstream: packages/utils/src/ar/open.ts:73-95
type archivePathCandidate struct {
	archivePath string
	subPath     string
}

// parseArchivePathCandidates splits filePath into every plausible
// (archivePath, subPath) pair, longest archive prefix first.
// Mirrors packages/utils/src/ar/open.ts:73-95 without regexp backtracking.
func parseArchivePathCandidates(filePath string) []archivePathCandidate {
	normalized := strings.ReplaceAll(filePath, "\\", "/")
	seen := make(map[string]struct{})
	var candidates []archivePathCandidate

	// Scan every '.' position and match the longest recognized extension at that offset
	// followed by ':' or end-of-string (matching the non-overlapping regex with longest-first alternation).
	for i := 0; i < len(normalized); i++ {
		if normalized[i] != '.' {
			continue
		}
		for _, entry := range archiveExtensionTable {
			extLen := len(entry.ext)
			end := i + 1 + extLen
			if end > len(normalized) {
				continue
			}
			if end < len(normalized) && normalized[end] != ':' {
				continue
			}
			if !asciiFoldEqual(normalized[i+1:end], entry.ext) {
				continue
			}
			archivePath := filePath[:end]
			subPath := strings.TrimLeft(normalized[end:], ":")
			key := archivePath + "\x00" + subPath
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				candidates = append(candidates, archivePathCandidate{
					archivePath: archivePath,
					subPath:     subPath,
				})
			}
			// Advance past the matched extension (non-overlapping regex semantics).
			i = end - 1
			break
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return len(candidates[i].archivePath) > len(candidates[j].archivePath)
	})
	return candidates
}

func asciiFoldEqual(s, lowerTarget string) bool {
	if len(s) != len(lowerTarget) {
		return false
	}
	for i := range len(s) {
		b := s[i]
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		if b != lowerTarget[i] {
			return false
		}
	}
	return true
}

// normalizeArchiveWriteSubPath validates and normalizes the target member path inside an archive.
// upstream: packages/coding-agent/src/tools/write.ts:485-509
func normalizeArchiveWriteSubPath(rawPath string) (string, error) {
	normalized := strings.ReplaceAll(rawPath, "\\", "/")
	if len(normalized) == 0 {
		return "", fmt.Errorf("Archive write path must target a file inside the archive")
	}
	if strings.HasSuffix(normalized, "/") {
		return "", fmt.Errorf("Archive write path must target a file, not a directory")
	}
	parts := strings.Split(normalized, "/")
	normalizedParts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", fmt.Errorf("Archive path cannot contain '..'")
		}
		normalizedParts = append(normalizedParts, part)
	}
	if len(normalizedParts) == 0 {
		return "", fmt.Errorf("Archive write path must target a file inside the archive")
	}
	return strings.Join(normalizedParts, "/"), nil
}

// normalizeArchiveLookupPath normalizes a lookup/link target inside an archive.
// Returns ("", true) for archive root, ("", false) when escaping via "..".
// upstream: packages/utils/src/ar/paths.ts:9-21
func normalizeArchiveLookupPath(rawPath string) (string, bool) {
	if rawPath == "" {
		return "", true
	}
	parts := strings.Split(strings.ReplaceAll(rawPath, "\\", "/"), "/")
	normalizedParts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", false
		}
		normalizedParts = append(normalizedParts, part)
	}
	return strings.Join(normalizedParts, "/"), true
}

// normalizeArchiveEntryPath normalizes a container member path; returns ("", false)
// for empty paths or paths escaping the archive root via "..".
// upstream: packages/utils/src/ar/paths.ts:27-38
func normalizeArchiveEntryPath(rawPath string) (string, bool) {
	parts := strings.Split(strings.ReplaceAll(rawPath, "\\", "/"), "/")
	normalizedParts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", false
		}
		normalizedParts = append(normalizedParts, part)
	}
	if len(normalizedParts) == 0 {
		return "", false
	}
	return strings.Join(normalizedParts, "/"), true
}

// formatArchivePathForError clamps a member path or link target to 256 UTF-8 bytes for errors.
// upstream: packages/utils/src/ar/paths.ts:46-58
func formatArchivePathForError(value string) string {
	if len(value) <= archivePathErrorPreviewBytes {
		return value
	}
	end := 0
	for idx, r := range value {
		rLen := utf8.RuneLen(r)
		if rLen < 0 {
			rLen = 1
		}
		if idx+rLen > archivePathErrorPreviewBytes-3 {
			break
		}
		end = idx + rLen
	}
	return value[:end] + "..."
}

func assertArchivePathBytes(size int, field string) error {
	if size > MaxArchivePathBytes {
		return fmt.Errorf("Archive %s exceeds %d bytes", field, MaxArchivePathBytes)
	}
	return nil
}

type resolvedArchiveWritePath struct {
	absolutePath   string
	archivePath    string
	archiveSubPath string
	exists         bool
}

func isArchivePathNotFound(err error) bool {
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return errors.Is(err, syscall.ENOTDIR)
}

// resolveArchiveWritePath detects `<archive.ext>:<member>` targets for `write`.
// upstream: packages/coding-agent/src/tools/write.ts:612-648
func resolveArchiveWritePath(writePath, cwd string) (*resolvedArchiveWritePath, error) {
	allCandidates := parseArchivePathCandidates(writePath)
	candidates := make([]archivePathCandidate, 0, len(allCandidates))
	for _, c := range allCandidates {
		if c.archivePath != writePath {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	fallbackCandidate := candidates[len(candidates)-1]
	fallbackSub, err := normalizeArchiveWriteSubPath(fallbackCandidate.subPath)
	if err != nil {
		return nil, err
	}
	fallback := &resolvedArchiveWritePath{
		absolutePath:   resolveToCwd(fallbackCandidate.archivePath, cwd),
		archivePath:    fallbackCandidate.archivePath,
		archiveSubPath: fallbackSub,
		exists:         false,
	}

	for _, c := range candidates {
		absPath := resolveToCwd(c.archivePath, cwd)
		info, statErr := os.Stat(absPath)
		if statErr != nil {
			if !isArchivePathNotFound(statErr) {
				return nil, statErr
			}
			continue
		}
		if info.IsDir() {
			continue
		}
		sub, subErr := normalizeArchiveWriteSubPath(c.subPath)
		if subErr != nil {
			return nil, subErr
		}
		return &resolvedArchiveWritePath{
			absolutePath:   absPath,
			archivePath:    c.archivePath,
			archiveSubPath: sub,
			exists:         true,
		}, nil
	}

	return fallback, nil
}

// orderedArchiveMembers preserves insertion order like JS Map<string, Uint8Array>.
type orderedArchiveMembers struct {
	order []string
	data  map[string][]byte
}

func newOrderedArchiveMembers() *orderedArchiveMembers {
	return &orderedArchiveMembers{data: make(map[string][]byte)}
}

func (m *orderedArchiveMembers) set(name string, payload []byte) {
	if _, exists := m.data[name]; !exists {
		m.order = append(m.order, name)
	}
	m.data[name] = payload
}

func (m *orderedArchiveMembers) get(name string) ([]byte, bool) {
	v, ok := m.data[name]
	return v, ok
}

// executeArchiveWrite writes or replaces a single member inside an archive atomically.
// upstream: packages/coding-agent/src/tools/write.ts:650-741, 974-1006
func executeArchiveWrite(
	ctx sdk.Context,
	cwd string,
	resolved *resolvedArchiveWritePath,
	cleanContent, rawContent string,
	stripped bool,
) (sdk.ToolResult, error) {
	if err := gate.Check(ctx, gate.Request{Tool: "write", Op: gate.OpWrite, Path: resolved.absolutePath}); err != nil {
		return sdk.ToolResult{}, err
	}

	parentDir := filepath.Dir(resolved.absolutePath)
	if parentDir != "" && parentDir != "." {
		if err := os.MkdirAll(parentDir, 0o755); err != nil {
			return sdk.ToolResult{}, err
		}
	}

	// Canonicalize target path (or canonical parent + basename when not yet present)
	// so every symlink spelling locks the same archive and detects format consistently.
	// upstream: packages/coding-agent/src/tools/write.ts:664-674
	finalPath, err := filepath.EvalSymlinks(resolved.absolutePath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return sdk.ToolResult{}, err
		}
		realParent, pErr := filepath.EvalSymlinks(parentDir)
		if pErr != nil {
			realParent = parentDir
		}
		finalPath = filepath.Join(realParent, filepath.Base(resolved.absolutePath))
	}

	inferredFormat := archiveFormatFromPath(finalPath)
	format := inferredFormat
	if format == "" {
		format = "tar"
	}
	if !isOmpWritableArchiveFormat(format) {
		// upstream: packages/coding-agent/src/tools/write.ts:672-674
		return sdk.ToolResult{}, fmt.Errorf("Writing entries inside %s archives is not supported (read-only format).", format)
	}
	if !isStdlibWritableArchiveFormat(format) {
		return sdk.ToolResult{}, fmt.Errorf("Writing entries inside %s archives is not supported in this Go standard-library port (only zip, tar, and tar.gz/tgz are writable).", format)
	}

	var resultText string
	writeFn := func() error {
		exists := resolved.exists
		var existingInfo os.FileInfo
		if info, statErr := os.Stat(finalPath); statErr == nil {
			existingInfo = info
			exists = true
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}

		entries := newOrderedArchiveMembers()
		if exists {
			if existingInfo != nil && existingInfo.IsDir() {
				// Matches omp's Bun.file(dir).bytes() failure during readArchiveEntries.
				if format == "zip" {
					return fmt.Errorf("Failed to read ZIP archive: Directories cannot be read like files")
				}
				return fmt.Errorf("Directories cannot be read like files")
			}
			if format == "tar" && existingInfo != nil {
				if err := assertArchiveInMemorySize(existingInfo.Size()); err != nil {
					return err
				}
			}
			if err := readAllArchiveMembers(finalPath, format, entries); err != nil {
				return err
			}
		}

		writeTarget := resolved.archivePath + ":" + resolved.archiveSubPath
		// Read-selector misfire guard for missing archive member with empty content.
		// upstream: packages/coding-agent/src/tools/write.ts:705-709
		if len(cleanContent) == 0 {
			if _, sel := SplitPathAndSel(writeTarget); sel != "" {
				if _, has := entries.get(resolved.archiveSubPath); !has {
					return fmt.Errorf(
						"write target '%s' ends with a read-tool selector ':%s' and no such file exists — refusing to create a literal file by that name. "+
							"If you meant to read it, use read({ path: %q }). "+
							"If you truly intend to create this file, pass its contents in `content` (a non-empty write is never blocked).",
						writeTarget, sel, writeTarget,
					)
				}
			}
		}

		// Truncated-read projection guard for existing archive member.
		// upstream: packages/coding-agent/src/tools/write.ts:710-716
		if existingBytes, has := entries.get(resolved.archiveSubPath); has && endsWithReadTruncationNotice(rawContent) {
			if utf8.Valid(existingBytes) {
				existingText := string(existingBytes)
				if err := assertNotShorterReadProjection(writeTarget, rawContent, &existingText, cleanContent); err != nil {
					return err
				}
			}
		}

		entries.set(resolved.archiveSubPath, []byte(cleanContent))

		// Atomic rewrite via temp file in same directory + rename.
		// Create with 0o666 so the process umask applies (matching Bun.write / omp write.ts:719-729).
		tmpFile, tmpPath, err := createArchiveTempFile(finalPath)
		if err != nil {
			return err
		}
		cleanupTmp := true
		defer func() {
			if cleanupTmp {
				_ = os.Remove(tmpPath)
			}
		}()

		encErr := encodeArchiveToWriter(tmpFile, format, entries)
		closeErr := tmpFile.Close()
		if encErr != nil {
			return encErr
		}
		if closeErr != nil {
			return closeErr
		}
		if err := os.Rename(tmpPath, finalPath); err != nil {
			return err
		}
		cleanupTmp = false
		outputPath := fmt.Sprintf("%s:%s", formatPathRelativeToCwd(resolved.absolutePath, cwd), resolved.archiveSubPath)
		resultText = fmt.Sprintf("Successfully wrote %d bytes to %s", len(cleanContent), outputPath)
		if stripped {
			resultText += "\nNote: auto-stripped hashline display prefixes from content before writing."
		}
		return nil
	}

	if err := withMutationQueue(ctx, finalPath, writeFn); err != nil {
		return sdk.ToolResult{}, err
	}

	return sdk.ToolResult{
		Content: resultText,
		Details: map[string]any{
			"resolvedPath": dirs.StandardizeMacOSPath(resolved.absolutePath),
		},
	}, nil
}

func assertArchiveInMemorySize(size int64) error {
	if size < 0 {
		return fmt.Errorf("Archive is too large to read safely")
	}
	if size > MaxArchiveInMemoryBytes {
		return fmt.Errorf("Archive is too large to read in memory (%s > %s limit)",
			formatBytes(size), formatBytes(MaxArchiveInMemoryBytes))
	}
	return nil
}

// readAllArchiveMembers materializes every file member into dst in archive order.
// upstream: packages/utils/src/ar/open.ts:126-143
func readAllArchiveMembers(archivePath, format string, dst *orderedArchiveMembers) error {
	switch format {
	case "zip":
		return readAllZipMembers(archivePath, dst)
	case "tar":
		return readAllTarMembers(archivePath, false, dst)
	case "tar.gz":
		return readAllTarMembers(archivePath, true, dst)
	default:
		return fmt.Errorf("Unsupported archive format: %s", format)
	}
}

// createArchiveTempFile creates an exclusive sibling temporary file with mode 0o666
// (masked by the process umask, matching Bun.write in omp's write.ts:721-725).
func createArchiveTempFile(finalPath string) (*os.File, string, error) {
	dir := filepath.Dir(finalPath)
	base := filepath.Base(finalPath)
	pid := os.Getpid()
	for range 16 {
		var rnd [8]byte
		if _, err := rand.Read(rnd[:]); err != nil {
			return nil, "", err
		}
		tmpPath := filepath.Join(dir, fmt.Sprintf("%s.tmp-%d-%s", base, pid, hex.EncodeToString(rnd[:])))
		f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
		if err == nil {
			return f, tmpPath, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return nil, "", err
	}
	return nil, "", fmt.Errorf("failed to allocate temporary archive file")
}

// archiveIndexEntry mirrors omp's ArchiveIndexEntry for whole-archive materialization.
// upstream: packages/utils/src/ar/types.ts:108-110, entries.ts, reader.ts
type archiveIndexEntry struct {
	path          string
	isDir         bool
	size          int64
	hasData       bool
	data          []byte
	sparse        bool
	isLink        bool
	linkTarget    string
	resolveTarget bool
}

// orderedArchiveIndex mirrors omp's ArchiveReader.#entries Map<string, ArchiveIndexEntry>.
type orderedArchiveIndex struct {
	order   []string
	entries map[string]*archiveIndexEntry
}

func newOrderedArchiveIndex() *orderedArchiveIndex {
	return &orderedArchiveIndex{entries: make(map[string]*archiveIndexEntry)}
}

// upsert mirrors packages/utils/src/ar/entries.ts:12-39.
func (idx *orderedArchiveIndex) upsert(entry *archiveIndexEntry) (*archiveIndexEntry, *archiveIndexEntry) {
	existing, ok := idx.entries[entry.path]
	if !ok {
		idx.order = append(idx.order, entry.path)
		idx.entries[entry.path] = entry
		return entry, nil
	}
	if existing.isDir && !entry.isDir {
		idx.entries[entry.path] = entry
		return entry, existing
	}
	if !existing.isDir && entry.isDir {
		return nil, existing
	}
	merged := *entry
	if !merged.hasData && !merged.isLink {
		merged.hasData = existing.hasData
		merged.data = existing.data
		merged.sparse = existing.sparse
		merged.isLink = existing.isLink
		merged.linkTarget = existing.linkTarget
		merged.resolveTarget = existing.resolveTarget
	}
	idx.entries[entry.path] = &merged
	return &merged, existing
}

// ensureParentDirectories mirrors packages/utils/src/ar/entries.ts:46-62.
func (idx *orderedArchiveIndex) ensureParentDirectories() error {
	if len(idx.entries) > MaxArchiveEntries {
		return fmt.Errorf("Archive has too many entries (> %d limit)", MaxArchiveEntries)
	}
	snapshot := append([]string(nil), idx.order...)
	for _, entryPath := range snapshot {
		parts := strings.Split(entryPath, "/")
		for i := 1; i < len(parts); i++ {
			dirPath := strings.Join(parts[:i], "/")
			if dirPath == "" {
				continue
			}
			if _, exists := idx.entries[dirPath]; exists {
				continue
			}
			idx.order = append(idx.order, dirPath)
			idx.entries[dirPath] = &archiveIndexEntry{
				path:  dirPath,
				isDir: true,
			}
			if len(idx.entries) > MaxArchiveEntries {
				return fmt.Errorf("Archive has too many entries (> %d limit)", MaxArchiveEntries)
			}
		}
	}
	return nil
}

// resolveArchiveLinkPath mirrors packages/utils/src/ar/entries.ts:69-97.
func resolveArchiveLinkPath(entries map[string]*archiveIndexEntry, archivePath string) (string, error) {
	resolvedPath := archivePath
	seen := make(map[string]struct{})
	rewrites := 0
	for {
		if _, dup := seen[resolvedPath]; dup {
			break
		}
		seen[resolvedPath] = struct{}{}
		var replacement string
		replaced := false
		for end := len(resolvedPath); end > 0; {
			prefix := resolvedPath[:end]
			if entry, ok := entries[prefix]; ok && entry.isLink && (entry.isDir || entry.resolveTarget) {
				suffix := ""
				if end < len(resolvedPath) {
					suffix = resolvedPath[end+1:]
				}
				if suffix != "" {
					if entry.linkTarget != "" {
						replacement = entry.linkTarget + "/" + suffix
					} else {
						replacement = suffix
					}
				} else {
					replacement = entry.linkTarget
				}
				replaced = true
				break
			}
			slash := strings.LastIndexByte(resolvedPath[:end], '/')
			if slash <= 0 {
				break
			}
			end = slash
		}
		if !replaced {
			return resolvedPath, nil
		}
		rewrites++
		if rewrites > MaxArchiveLinkDepth {
			break
		}
		resolvedPath = replacement
	}
	return "", fmt.Errorf("Archive path '%s' crosses a cyclic symlink", archivePath)
}

// materializeArchiveIndex mirrors ArchiveReader + readArchiveEntries
// (packages/utils/src/ar/reader.ts:127-161, open.ts:126-143).
func materializeArchiveIndex(idx *orderedArchiveIndex, dst *orderedArchiveMembers) error {
	if err := idx.ensureParentDirectories(); err != nil {
		return err
	}
	var total int64
	for _, entryPath := range idx.order {
		entry := idx.entries[entryPath]
		if entry == nil || entry.isDir {
			continue
		}
		resolvedPath, err := resolveArchiveLinkPath(idx.entries, entry.path)
		if err != nil {
			return err
		}
		if resolvedPath == "" {
			return fmt.Errorf("Archive path '%s' is a directory", entry.path)
		}
		target, ok := idx.entries[resolvedPath]
		if !ok {
			return fmt.Errorf("Archive file '%s' not found", entry.path)
		}
		if target.isDir {
			return fmt.Errorf("Archive path '%s' is a directory", entry.path)
		}
		if target.isLink {
			return fmt.Errorf("Archive symlink '%s' cannot be materialized from target '%s'",
				formatArchivePathForError(entry.path), formatArchivePathForError(target.linkTarget))
		}
		if !target.hasData {
			return fmt.Errorf("Archive file '%s' has no readable storage", entry.path)
		}
		if target.size > MaxArchiveMemberBytes {
			return fmt.Errorf("Archive member '%s' is too large to extract in memory (%s > %s limit)",
				formatArchivePathForError(entry.path), formatBytes(target.size), formatBytes(MaxArchiveMemberBytes))
		}
		if target.sparse {
			return fmt.Errorf("Archive member '%s' is a sparse file and cannot be read",
				formatArchivePathForError(entry.path))
		}
		total += int64(len(target.data))
		if err := assertArchiveInMemorySize(total); err != nil {
			return err
		}
		dst.set(entry.path, target.data)
	}
	return nil
}

func zipFileHeaderIsDirOrSymlink(f *zip.File) (isDir, isSymlink bool) {
	host := f.CreatorVersion >> 8
	var fileType uint32
	if host == zipUnixHost || host == zipOSXHost {
		mode := f.ExternalAttrs >> 16
		fileType = mode & zipFileTypeMask
	}
	isDir = strings.HasSuffix(f.Name, "/") || strings.HasSuffix(f.Name, "\\") ||
		fileType == zipDirectoryType || (f.ExternalAttrs&0x10) != 0
	isSymlink = fileType == zipSymlinkType && !isDir
	return isDir, isSymlink
}

func readAllZipMembers(archivePath string, dst *orderedArchiveMembers) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		if errors.Is(err, zip.ErrFormat) {
			return fmt.Errorf("Invalid ZIP archive: missing end of central directory")
		}
		return fmt.Errorf("Failed to read ZIP archive: %v", err)
	}
	defer zr.Close()
	if len(zr.File) > MaxArchiveEntries {
		return fmt.Errorf("Archive has too many entries (> %d limit)", MaxArchiveEntries)
	}

	idx := newOrderedArchiveIndex()
	for _, f := range zr.File {
		if err := assertArchivePathBytes(len(f.Name), "member path"); err != nil {
			return err
		}
		name, ok := normalizeArchiveEntryPath(f.Name)
		if !ok {
			// Matches omp packages/utils/src/ar/zip.ts:506-536: unrepresentable paths
			// (empty or escaping via '..') are dropped during indexing.
			continue
		}
		isDir, isSymlink := zipFileHeaderIsDirOrSymlink(f)
		if isDir {
			idx.upsert(&archiveIndexEntry{path: name, isDir: true})
			continue
		}
		if isSymlink {
			if err := assertArchivePathBytes(int(f.UncompressedSize64), "symlink target"); err != nil {
				return err
			}
			rc, err := f.Open()
			if err != nil {
				return fmt.Errorf("Failed to read ZIP archive: %v", err)
			}
			targetBytes, readErr := readLimited(rc, name, MaxArchivePathBytes)
			_ = rc.Close()
			if readErr != nil {
				return readErr
			}
			target := string(targetBytes)
			if err := assertArchivePathBytes(len(target), "symlink target"); err != nil {
				return err
			}
			portable := strings.ReplaceAll(target, "\\", "/")
			absolute := strings.HasPrefix(portable, "/") ||
				(len(portable) >= 2 && isASCIILetter(portable[0]) && portable[1] == ':') ||
				strings.ContainsRune(portable, 0)
			var targetPath string
			var resolveOK bool
			if !absolute {
				joined := portable
				if dir := path.Dir(name); dir != "." {
					joined = dir + "/" + portable
				}
				targetPath, resolveOK = normalizeArchiveLookupPath(joined)
			}
			entry := &archiveIndexEntry{
				path:          name,
				isLink:        true,
				resolveTarget: resolveOK,
			}
			if resolveOK {
				entry.linkTarget = targetPath
			} else {
				entry.linkTarget = portable
			}
			idx.upsert(entry)
			continue
		}

		if f.UncompressedSize64 > uint64(MaxArchiveMemberBytes) {
			return fmt.Errorf("Archive member '%s' is too large to extract in memory (%s > %s limit)",
				name, formatBytes(int64(f.UncompressedSize64)), formatBytes(MaxArchiveMemberBytes))
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("Failed to read ZIP archive: %v", err)
		}
		data, readErr := readLimited(rc, name, MaxArchiveMemberBytes)
		_ = rc.Close()
		if readErr != nil {
			return readErr
		}
		idx.upsert(&archiveIndexEntry{
			path:    name,
			size:    int64(len(data)),
			hasData: true,
			data:    data,
		})
	}
	return materializeArchiveIndex(idx, dst)
}

type pendingTarLink struct {
	kind       string // "hard link" or "symlink"
	targetPath string
}

// resolvePendingTarLinks mirrors packages/utils/src/ar/tar.ts:497-582.
func resolvePendingTarLinks(idx *orderedArchiveIndex, pendingLinks map[*archiveIndexEntry]*pendingTarLink) error {
	if len(pendingLinks) == 0 {
		return nil
	}
	directoryPrefixes := make(map[string]struct{})
	for entryPath := range idx.entries {
		for cut := strings.LastIndexByte(entryPath, '/'); cut > 0; cut = strings.LastIndexByte(entryPath[:cut], '/') {
			prefix := entryPath[:cut]
			if _, ok := directoryPrefixes[prefix]; ok {
				break
			}
			directoryPrefixes[prefix] = struct{}{}
		}
	}

	unresolved := make(map[*archiveIndexEntry]struct{}, len(pendingLinks))
	queue := make([]*archiveIndexEntry, 0, len(pendingLinks))
	for _, p := range idx.order {
		e := idx.entries[p]
		if _, ok := pendingLinks[e]; ok {
			unresolved[e] = struct{}{}
			queue = append(queue, e)
		}
	}
	dependents := make(map[*archiveIndexEntry][]*archiveIndexEntry)
	findUnresolvedBlocker := func(targetPath string) *archiveIndexEntry {
		for end := len(targetPath); end > 0; {
			if prefixEntry, ok := idx.entries[targetPath[:end]]; ok {
				if _, isUnres := unresolved[prefixEntry]; isUnres {
					return prefixEntry
				}
			}
			slash := strings.LastIndexByte(targetPath[:end], '/')
			if slash <= 0 {
				break
			}
			end = slash
		}
		return nil
	}

	for len(queue) > 0 {
		entry := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if _, isUnres := unresolved[entry]; !isUnres {
			continue
		}
		pending := pendingLinks[entry]
		blocker := findUnresolvedBlocker(pending.targetPath)
		targetPath := pending.targetPath
		if blocker == nil {
			if rp, err := resolveArchiveLinkPath(idx.entries, targetPath); err == nil {
				targetPath = rp
			}
			if targetPath != pending.targetPath {
				blocker = findUnresolvedBlocker(targetPath)
			}
		}
		if blocker != nil && blocker != entry {
			dependents[blocker] = append(dependents[blocker], entry)
			continue
		}
		delete(unresolved, entry)
		if settled := dependents[entry]; len(settled) > 0 {
			delete(dependents, entry)
			queue = append(queue, settled...)
		}
		if blocker == entry {
			if pending.kind == "hard link" {
				return fmt.Errorf("Archive hard link '%s' has a cyclic target '%s'",
					formatArchivePathForError(entry.path), formatArchivePathForError(pending.targetPath))
			}
			entry.isLink = true
			entry.linkTarget = pending.targetPath
			entry.resolveTarget = false
			continue
		}
		target := idx.entries[targetPath]
		if target != nil && (target.hasData || target.isLink) && !target.isDir {
			if _, targetUnres := unresolved[target]; !targetUnres {
				entry.size = target.size
				entry.hasData = target.hasData
				entry.data = target.data
				entry.sparse = target.sparse
				entry.isLink = target.isLink
				entry.linkTarget = target.linkTarget
				entry.resolveTarget = target.resolveTarget
				continue
			}
		}
		_, isDirPrefix := directoryPrefixes[targetPath]
		targetIsDir := targetPath == "" || (target != nil && target.isDir) || isDirPrefix
		if !targetIsDir {
			if pending.kind == "symlink" {
				entry.isLink = true
				entry.linkTarget = pending.targetPath
				entry.resolveTarget = false
				continue
			}
			reason := "missing member"
			if target != nil {
				reason = "unreadable member"
			}
			return fmt.Errorf("Archive hard link '%s' targets %s '%s'",
				formatArchivePathForError(entry.path), reason, formatArchivePathForError(pending.targetPath))
		}
		if pending.kind == "hard link" {
			return fmt.Errorf("Archive hard link '%s' targets directory '%s'",
				formatArchivePathForError(entry.path), formatArchivePathForError(pending.targetPath))
		}
		entry.isDir = true
		entry.isLink = true
		entry.linkTarget = pending.targetPath
		entry.resolveTarget = false
	}
	if len(unresolved) > 0 {
		return fmt.Errorf("Archive contains cyclic or unsupported links")
	}
	return nil
}

// detectTarSparsePaths scans raw tar headers for old-GNU sparse ('S') and PAX
// GNU.sparse.* members before Go's archive/tar normalizes them away, so
// rewrite produces omp's exact sparse-member refusal error.
// upstream: packages/utils/src/ar/tar.ts:253-257, 688-696
func detectTarSparsePaths(raw []byte) map[string]bool {
	const blockSize = 512
	sparse := make(map[string]bool)
	var nextLongName string
	var localPax map[string]string
	globalPax := make(map[string]string)
	pos := 0
	for pos+blockSize <= len(raw) {
		hdr := raw[pos : pos+blockSize]
		pos += blockSize
		allZero := true
		for _, b := range hdr {
			if b != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			break
		}
		tf := hdr[156]
		if tf == 0 {
			tf = '0'
		}
		size := parseTarOctalOrBinarySize(hdr[124:136])
		if size < 0 {
			break
		}
		if tf == 'L' || tf == 'x' || tf == 'X' || tf == 'g' {
			padded := ((size + blockSize - 1) / blockSize) * blockSize
			if pos+int(size) > len(raw) || pos+int(padded) > len(raw) {
				break
			}
			meta := raw[pos : pos+int(size)]
			pos += int(padded)
			if tf == 'L' {
				if nul := bytes.IndexByte(meta, 0); nul >= 0 {
					meta = meta[:nul]
				}
				nextLongName = string(meta)
			} else if tf == 'x' || tf == 'X' {
				localPax = parseSimplePaxRecords(meta)
			} else if tf == 'g' {
				for k, v := range parseSimplePaxRecords(meta) {
					if v == "" {
						delete(globalPax, k)
					} else {
						globalPax[k] = v
					}
				}
			}
			continue
		}
		name := readNullTermString(hdr[0:100])
		if string(hdr[257:263]) == "ustar\x00" {
			if pfx := readNullTermString(hdr[345:500]); pfx != "" {
				name = pfx + "/" + name
			}
		}
		if nextLongName != "" {
			name = nextLongName
		}
		isSparse := tf == 'S'
		if v, ok := localPax["path"]; ok {
			name = v
		} else if v, ok := globalPax["path"]; ok {
			name = v
		}
		if v, ok := localPax["GNU.sparse.name"]; ok {
			name = v
			isSparse = true
		} else if v, ok := globalPax["GNU.sparse.name"]; ok {
			name = v
			isSparse = true
		}
		for k := range localPax {
			if strings.HasPrefix(k, "GNU.sparse.") {
				isSparse = true
				break
			}
		}
		if !isSparse {
			for k := range globalPax {
				if strings.HasPrefix(k, "GNU.sparse.") {
					isSparse = true
					break
				}
			}
		}
		if v, ok := localPax["size"]; ok {
			if ps, err := parseNonNegDecimal(v); err == nil {
				size = ps
			}
		}
		if tf == 'S' && hdr[482] == 1 {
			for pos+blockSize <= len(raw) {
				ext := raw[pos : pos+blockSize]
				pos += blockSize
				if ext[504] != 1 {
					break
				}
			}
		}
		padded := ((size + blockSize - 1) / blockSize) * blockSize
		if padded < 0 || pos+int(padded) > len(raw) {
			break
		}
		pos += int(padded)
		nextLongName = ""
		localPax = nil
		if isSparse {
			if norm, ok := normalizeArchiveEntryPath(name); ok {
				sparse[norm] = true
			}
		}
	}
	return sparse
}

func readNullTermString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func parseTarOctalOrBinarySize(b []byte) int64 {
	if len(b) == 0 {
		return 0
	}
	if b[0]&0x80 != 0 {
		var v int64
		v = int64(b[0] & 0x7f)
		for _, c := range b[1:] {
			v = (v << 8) | int64(c)
		}
		return v
	}
	var v int64
	for _, c := range b {
		if c >= '0' && c <= '7' {
			v = v*8 + int64(c-'0')
		}
	}
	return v
}

func parseNonNegDecimal(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	var v int64
	for i := range len(s) {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-digit")
		}
		v = v*10 + int64(c-'0')
	}
	return v, nil
}

func parseSimplePaxRecords(meta []byte) map[string]string {
	out := make(map[string]string)
	pos := 0
	for pos < len(meta) {
		sp := bytes.IndexByte(meta[pos:], ' ')
		if sp <= 0 {
			break
		}
		recLen, err := parseNonNegDecimal(string(meta[pos : pos+sp]))
		if err != nil || recLen <= 0 || pos+int(recLen) > len(meta) {
			break
		}
		rec := meta[pos+sp+1 : pos+int(recLen)]
		rec = bytes.TrimSuffix(rec, []byte("\n"))
		if eq := bytes.IndexByte(rec, '='); eq >= 0 {
			out[string(rec[:eq])] = string(rec[eq+1:])
		}
		pos += int(recLen)
	}
	return out
}

func tarHeaderIsSparse(hdr *tar.Header, name string, rawSparse map[string]bool) bool {
	if hdr.Typeflag == tar.TypeGNUSparse || rawSparse[name] {
		return true
	}
	for k := range hdr.PAXRecords {
		if strings.HasPrefix(k, "GNU.sparse.") {
			return true
		}
	}
	return false
}
func readAllTarMembers(archivePath string, gzipped bool, dst *orderedArchiveMembers) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	var r io.Reader = f
	if gzipped {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		// Bound decompressed stream so a gzip bomb cannot exhaust memory during rewrite.
		r = io.LimitReader(gz, MaxArchiveInMemoryBytes+1)
	}

	// Read raw tar bytes so we can verify terminator / format validity on plain files.
	rawTar, err := io.ReadAll(io.LimitReader(r, MaxArchiveInMemoryBytes+1))
	if err != nil {
		return err
	}
	if int64(len(rawTar)) > MaxArchiveInMemoryBytes {
		return assertArchiveInMemorySize(int64(len(rawTar)))
	}
	if len(rawTar) < 512 {
		return fmt.Errorf("Not a valid tar archive: missing terminating zero block")
	}
	rawSparse := detectTarSparsePaths(rawTar)
	tr := tar.NewReader(bytes.NewReader(rawTar))
	idx := newOrderedArchiveIndex()
	pendingLinks := make(map[*archiveIndexEntry]*pendingTarLink)
	addEntry := func(entry *archiveIndexEntry, pending *pendingTarLink) error {
		indexed, prev := idx.upsert(entry)
		if indexed == nil {
			return nil
		}
		if prev != nil {
			delete(pendingLinks, prev)
		}
		if pending != nil {
			pendingLinks[indexed] = pending
		}
		if len(idx.entries) > MaxArchiveEntries {
			return fmt.Errorf("Archive has too many entries (> %d limit)", MaxArchiveEntries)
		}
		return nil
	}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == 'M' {
			return fmt.Errorf("GNU multi-volume tar members are not supported")
		}
		// Archive/tar automatically consumes TypeXHeader ('x'), TypeXGlobalHeader ('g'),
		// TypeGNULongName ('L'), and TypeGNULongLink ('K') into the subsequent header.
		rawName := hdr.Name
		if sparseName, ok := hdr.PAXRecords["GNU.sparse.name"]; ok && sparseName != "" {
			rawName = sparseName
		}
		isDir := hdr.Typeflag == tar.TypeDir || strings.HasSuffix(rawName, "/")
		name, ok := normalizeArchiveEntryPath(rawName)
		if !ok {
			continue
		}
		if err := assertArchivePathBytes(len(name), "member path"); err != nil {
			return err
		}
		if isDir {
			if err := addEntry(&archiveIndexEntry{path: name, isDir: true}, nil); err != nil {
				return err
			}
			continue
		}
		if hdr.Typeflag == tar.TypeLink || hdr.Typeflag == tar.TypeSymlink {
			kind := "symlink"
			if hdr.Typeflag == tar.TypeLink {
				kind = "hard link"
			}
			portableLink := strings.ReplaceAll(hdr.Linkname, "\\", "/")
			if err := assertArchivePathBytes(len(portableLink), "link target"); err != nil {
				return err
			}
			var targetPath string
			var valid bool
			if hdr.Typeflag == tar.TypeLink {
				targetPath, valid = normalizeArchiveEntryPath(portableLink)
			} else if !strings.HasPrefix(portableLink, "/") {
				joined := portableLink
				if dir := path.Dir(name); dir != "." {
					joined = dir + "/" + portableLink
				}
				targetPath, valid = normalizeArchiveLookupPath(joined)
			}
			entry := &archiveIndexEntry{path: name}
			if !valid || len(targetPath) > MaxArchivePathBytes {
				if kind == "hard link" {
					return fmt.Errorf("Archive hard link '%s' has an invalid target", formatArchivePathForError(name))
				}
				entry.isLink = true
				entry.linkTarget = portableLink
				entry.resolveTarget = false
				if err := addEntry(entry, nil); err != nil {
					return err
				}
				continue
			}
			if err := addEntry(entry, &pendingTarLink{kind: kind, targetPath: targetPath}); err != nil {
				return err
			}
			continue
		}
		// Skip non-file special records (FIFO, char/block devices, etc.), matching omp tar.ts:745.
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA &&
			hdr.Typeflag != tar.TypeCont && hdr.Typeflag != tar.TypeGNUSparse {
			continue
		}
		if tarHeaderIsSparse(hdr, name, rawSparse) {
			// Record sparse member so materialization produces omp's exact sparse error.
			if err := addEntry(&archiveIndexEntry{
				path:    name,
				size:    hdr.Size,
				hasData: true,
				sparse:  true,
			}, nil); err != nil {
				return err
			}
			continue
		}
		if hdr.Size > MaxArchiveMemberBytes {
			return fmt.Errorf("Archive member '%s' is too large to extract in memory (%s > %s limit)",
				name, formatBytes(hdr.Size), formatBytes(MaxArchiveMemberBytes))
		}
		data, err := readLimited(tr, name, MaxArchiveMemberBytes)
		if err != nil {
			return err
		}
		if err := addEntry(&archiveIndexEntry{
			path:    name,
			size:    int64(len(data)),
			hasData: true,
			data:    data,
		}, nil); err != nil {
			return err
		}
	}

	if err := resolvePendingTarLinks(idx, pendingLinks); err != nil {
		return err
	}
	return materializeArchiveIndex(idx, dst)
}

// encodeArchiveToWriter serializes members to w in omp's deterministic format.
// upstream: packages/utils/src/ar/write.ts:26-46, zip.ts:607-716, tar.ts:949-982
func encodeArchiveToWriter(w io.Writer, format string, members *orderedArchiveMembers) error {
	switch format {
	case "zip":
		return encodeZipToWriter(w, members)
	case "tar":
		return encodeTarToWriter(w, members)
	case "tar.gz":
		gz := gzip.NewWriter(w)
		if err := encodeTarToWriter(gz, members); err != nil {
			_ = gz.Close()
			return err
		}
		return gz.Close()
	default:
		return fmt.Errorf("Writing entries inside %s archives is not supported (read-only format).", format)
	}
}

// encodeZipToWriter writes a deterministic ZIP archive with Unix 0644 mode and fixed DOS epoch timestamp.
// upstream: packages/utils/src/ar/zip.ts:607-716
func encodeZipToWriter(w io.Writer, members *orderedArchiveMembers) error {
	zw := zip.NewWriter(w)
	fixedTime := time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

	for _, name := range members.order {
		data := members.data[name]
		portableName := strings.ReplaceAll(name, "\\", "/")
		normalizedName, ok := normalizeArchiveEntryPath(portableName)
		if !ok ||
			normalizedName != strings.TrimPrefix(portableName, "./") ||
			strings.HasPrefix(portableName, "/") ||
			(len(portableName) >= 2 && isASCIILetter(portableName[0]) && portableName[1] == ':') ||
			strings.ContainsRune(portableName, 0) {
			_ = zw.Close()
			return fmt.Errorf("Cannot write unsafe ZIP member path '%s'", name)
		}
		hdr := &zip.FileHeader{
			Name:     normalizedName,
			Method:   zip.Deflate,
			Modified: fixedTime,
		}
		if len(data) == 0 {
			hdr.Method = zip.Store
		}
		hdr.SetMode(0o644)
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			_ = zw.Close()
			return fmt.Errorf("Failed to encode ZIP archive: %v", err)
		}
		if len(data) > 0 {
			if _, err := fw.Write(data); err != nil {
				_ = zw.Close()
				return fmt.Errorf("Failed to encode ZIP archive: %v", err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("Failed to encode ZIP archive: %v", err)
	}
	return nil
}

// encodeTarToWriter writes a deterministic tar archive with synthesized parent directories,
// mode 0755 for directories, 0644 for files, and Unix epoch mtime (0).
// upstream: packages/utils/src/ar/tar.ts:949-982
func encodeTarToWriter(w io.Writer, members *orderedArchiveMembers) error {
	tw := tar.NewWriter(w)
	kinds := make(map[string]string)
	epoch := time.Unix(0, 0).UTC()

	for _, rawPath := range members.order {
		data := members.data[rawPath]
		normalized, ok := normalizeArchiveEntryPath(rawPath)
		if !ok {
			_ = tw.Close()
			return fmt.Errorf("Invalid tar member path '%s'", rawPath)
		}
		segments := strings.Split(normalized, "/")
		for i := 1; i < len(segments); i++ {
			parent := strings.Join(segments[:i], "/")
			kind := kinds[parent]
			if kind == "file" {
				_ = tw.Close()
				return fmt.Errorf("Tar member '%s' is not a directory", parent)
			}
			if kind == "directory" {
				continue
			}
			kinds[parent] = "directory"
			if err := tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeDir,
				Name:     parent + "/",
				Mode:     0o755,
				Size:     0,
				ModTime:  epoch,
				Format:   tar.FormatUSTAR,
			}); err != nil {
				// Fall back to PAX when path exceeds USTAR limits.
				if err2 := tw.WriteHeader(&tar.Header{
					Typeflag: tar.TypeDir,
					Name:     parent + "/",
					Mode:     0o755,
					Size:     0,
					ModTime:  epoch,
					Format:   tar.FormatPAX,
				}); err2 != nil {
					_ = tw.Close()
					return err2
				}
			}
		}
		if existing, exists := kinds[normalized]; exists {
			_ = tw.Close()
			if existing == "directory" {
				return fmt.Errorf("Duplicate tar member path '%s'", normalized)
			}
			return fmt.Errorf("Duplicate tar member path '%s'", normalized)
		}
		kinds[normalized] = "file"
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     normalized,
			Mode:     0o644,
			Size:     int64(len(data)),
			ModTime:  epoch,
			Format:   tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			hdr.Format = tar.FormatPAX
			if err2 := tw.WriteHeader(hdr); err2 != nil {
				_ = tw.Close()
				return err2
			}
		}
		if len(data) > 0 {
			if _, err := tw.Write(data); err != nil {
				_ = tw.Close()
				return err
			}
		}
	}
	return tw.Close()
}

// isReadableArchiveExt reports whether ext (including leading dot) is readable by this port.
func archiveReadExts() []string {
	return []string{
		".tar.gz", ".tgz", ".tar",
		".zip", ".jar", ".war", ".ear", ".apk", ".whl", ".ipa", ".xpi", ".vsix", ".nupkg", ".cbz",
	}
}
