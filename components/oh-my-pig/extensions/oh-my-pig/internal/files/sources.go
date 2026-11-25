package files

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
)

const (
	// MaxArchiveMemberBytes matches omp's DEFAULT_ARCHIVE_LIMITS.maxMemberSize (64 MiB).
	// upstream: packages/utils/src/ar/limits.ts:33
	MaxArchiveMemberBytes = 64 * 1024 * 1024
	// DefaultArchiveDirLimit matches omp's readArchiveDirectory DEFAULT_LIMIT (500).
	// upstream: packages/coding-agent/src/tools/read-archive.ts:227
	DefaultArchiveDirLimit = 500
	// MaxArchiveEntries matches omp's DEFAULT_ARCHIVE_LIMITS.maxEntries.
	// upstream: packages/utils/src/ar/limits.ts:29
	MaxArchiveEntries = 1_000_000
)

// readNotebook parses a Jupyter .ipynb file into omp's editable cell projection.
// upstream: packages/coding-agent/src/tools/read.ts:2746
func readNotebook(raw []byte, store *hashline.Store, absPath, displayPath string, parsed ParsedSelector, immutable, unbounded bool) (sdk.ToolResult, error) {
	text, err := hashline.NotebookToEditableText(string(raw), displayPath)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	res, err := renderInMemoryText(text, displayPath, "notebook", parsed, store, absPath, 0, unbounded, immutable)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	details := ensureDetailsMap(&res)
	details["resolvedPath"] = dirs.StandardizeMacOSPath(absPath)
	setMetaSource(details, "path", absPath)
	return res, nil
}

// indexASCIIFold finds the first occurrence of lowercase ASCII needle in s using
// byte-aligned ASCII case folding (so offsets always index s safely even when s
// contains multi-byte Unicode characters whose lowercase expands/shrinks in UTF-8,
// such as U+023A 'Ⱥ' → U+2C65 'ⱥ').
func indexASCIIFold(s, lowerNeedle string, from int) int {
	n := len(lowerNeedle)
	if n == 0 || from+n > len(s) {
		return -1
	}
	for i := from; i+n <= len(s); i++ {
		matched := true
		for j := range n {
			b := s[i+j]
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if b != lowerNeedle[j] {
				matched = false
				break
			}
		}
		if matched {
			return i
		}
	}
	return -1
}

// splitArchivePath detects `<archive>.<ext>[:<member>[:<sel>]]` for stdlib-readable
// archive formats (ZIP family, TAR, TAR.GZ/TGZ), falling back to a unique workspace
// suffix match when the archive path does not exist directly.
// upstream: packages/coding-agent/src/tools/read-archive.ts:171-215, packages/utils/src/ar/registry.ts:33-36
func splitArchivePath(ctx context.Context, rawPath, cwd string, suffixCache suffixMatchCache) (archiveAbs, archiveDisplay, memberPath, selStr string, suffixRes *SuffixResolution, ok bool) {
	// Match extensions longest-first using byte-safe ASCII case folding on rawPath.
	exts := archiveReadExts()
	for _, ext := range exts {
		for idx := indexASCIIFold(rawPath, ext, 0); idx >= 0; idx = indexASCIIFold(rawPath, ext, idx+len(ext)) {
			end := idx + len(ext)
			if end != len(rawPath) && rawPath[end] != ':' {
				continue
			}
			candPath := rawPath[:end]
			abs := resolveToCwd(candPath, cwd)
			var sr *SuffixResolution
			info, err := os.Stat(abs)
			if err != nil {
				if !isNotFoundError(err) || isRemoteMountPath(abs) {
					continue
				}
				sm, sErr := findSuffixMatchCached(ctx, suffixCache, candPath, cwd)
				if sErr != nil || sm == nil {
					continue
				}
				retryInfo, retryErr := os.Stat(sm.AbsolutePath)
				if retryErr != nil || !retryInfo.Mode().IsRegular() {
					continue
				}
				abs = sm.AbsolutePath
				info = retryInfo
				sr = &SuffixResolution{From: candPath, To: sm.DisplayPath}
			}
			if !info.Mode().IsRegular() {
				continue
			}
			rest := ""
			if end < len(rawPath) {
				rest = rawPath[end+1:]
			}
			if rest != "" {
				if p, err := ParseSel(rest); err == nil && p.Kind != SelectorNone {
					return abs, formatPathRelativeToCwd(abs, cwd), "", rest, sr, true
				}
				mPath, mSel := SplitPathAndSel(rest)
				cleanMember := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(mPath, "\\", "/")), "/")
				if cleanMember == "." {
					cleanMember = ""
				}
				return abs, formatPathRelativeToCwd(abs, cwd), cleanMember, mSel, sr, true
			}
			return abs, formatPathRelativeToCwd(abs, cwd), "", "", sr, true
		}
	}
	return "", "", "", "", nil, false
}

type archiveEntry struct {
	path  string
	isDir bool
	size  int64
}

type archiveDirEntry struct {
	name  string
	isDir bool
	size  int64
}

// listArchiveDirectory returns immediate children of subPath sorted case-insensitively.
// upstream: packages/utils/src/ar/reader.ts:73
func listArchiveDirectory(entries []archiveEntry, subPath, readPath string) ([]archiveDirEntry, bool, error) {
	cleanSub := strings.Trim(strings.ReplaceAll(subPath, "\\", "/"), "/")
	// Build lookup to distinguish directory vs file when subPath != "".
	if cleanSub != "" {
		foundDir := false
		prefix := cleanSub + "/"
		for _, e := range entries {
			if e.path == cleanSub {
				if e.isDir {
					foundDir = true
				} else {
					return nil, false, nil // it is a file, not a directory
				}
			} else if strings.HasPrefix(e.path, prefix) {
				foundDir = true
			}
		}
		if !foundDir {
			// upstream: packages/coding-agent/src/tools/read-archive.ts:311
			return nil, false, fmt.Errorf("Path '%s' not found inside archive", readPath)
		}
	}

	prefix := ""
	if cleanSub != "" {
		prefix = cleanSub + "/"
	}
	children := make(map[string]archiveDirEntry)
	for _, e := range entries {
		if e.path == "" || e.path == "." {
			continue
		}
		if prefix != "" {
			if !strings.HasPrefix(e.path, prefix) || e.path == cleanSub {
				continue
			}
		}
		rel := strings.TrimPrefix(e.path, prefix)
		seg, rest, hasSlash := strings.Cut(rel, "/")
		if seg == "" {
			continue
		}
		isDir := hasSlash || e.isDir
		if prev, exists := children[seg]; exists {
			if isDir && !prev.isDir {
				children[seg] = archiveDirEntry{name: seg, isDir: true}
			}
			continue
		}
		size := e.size
		if isDir || rest != "" {
			size = 0
		}
		children[seg] = archiveDirEntry{name: seg, isDir: isDir, size: size}
	}

	out := make([]archiveDirEntry, 0, len(children))
	for _, c := range children {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		li := strings.ToLower(out[i].name)
		lj := strings.ToLower(out[j].name)
		if li != lj {
			return li < lj
		}
		return out[i].name < out[j].name
	})
	return out, true, nil
}

// formatArchiveEntryLines mirrors packages/utils/src/ar/open.ts:98.
func formatArchiveEntryLines(entries []archiveDirEntry) []string {
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.isDir {
			lines = append(lines, e.name+"/")
		} else if e.size > 0 {
			lines = append(lines, fmt.Sprintf("%s (%s)", e.name, formatBytes(e.size)))
		} else {
			lines = append(lines, e.name)
		}
	}
	return lines
}

func readArchive(archiveAbs, archiveDisplay, readPath, memberPath string, parsed ParsedSelector, suffixRes *SuffixResolution) (sdk.ToolResult, error) {
	lower := strings.ToLower(archiveAbs)
	isTar := strings.HasSuffix(lower, ".tar") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz")
	isGzip := strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz")

	var entries []archiveEntry
	var err error
	if isTar {
		entries, err = listTarEntries(archiveAbs, isGzip)
	} else {
		entries, err = listZipEntries(archiveAbs)
	}
	if err != nil {
		return sdk.ToolResult{}, fmt.Errorf("Cannot read archive '%s': %v", archiveDisplay, err)
	}

	// Check if memberPath is empty or a directory within the archive.
	// upstream: packages/coding-agent/src/tools/read-archive.ts:219
	dirEntries, isDir, dirErr := listArchiveDirectory(entries, memberPath, readPath)
	if dirErr != nil {
		return sdk.ToolResult{}, dirErr
	}
	if isDir {
		if parsed.IsMultiRange() {
			return sdk.ToolResult{}, fmt.Errorf("Multi-range line selectors are not supported for archive listings.")
		}
		resolved := parsed.ResolveTail(len(dirEntries))
		offset := 1
		limit := 0
		hasLimit := false
		if resolved.Kind == SelectorLines && len(resolved.Ranges) == 1 {
			r := resolved.Ranges[0]
			if r.StartLine > 1 {
				offset = r.StartLine
			}
			if r.EndLine > 0 {
				limit = r.EndLine - r.StartLine + 1
				hasLimit = true
			}
		}
		effectiveLimit := DefaultArchiveDirLimit
		if hasLimit {
			effectiveLimit = limit
		}
		startIdx := min(offset-1, len(dirEntries))
		sliced := dirEntries[startIdx:]
		limited := sliced
		if len(limited) > effectiveLimit {
			limited = limited[:effectiveLimit]
		}
		lines := formatArchiveEntryLines(limited)
		output := "(empty archive directory)"
		if len(lines) > 0 {
			output = strings.Join(lines, "\n")
		}
		if !hasLimit && len(sliced) > len(limited) {
			remaining := len(sliced) - len(limited)
			next := offset + len(limited)
			output += fmt.Sprintf("\n\n[%d more entries in listing. Use :%d- to continue]", remaining, next)
		}
		output = prependSuffixResolutionNotice(output, suffixRes)
		details := map[string]any{
			"isDirectory":  true,
			"resolvedPath": dirs.StandardizeMacOSPath(archiveAbs),
			"meta": map[string]any{
				"source": map[string]any{"type": "path", "value": dirs.StandardizeMacOSPath(archiveAbs)},
			},
		}
		if suffixRes != nil {
			details["suffixResolution"] = suffixRes.toMap()
		}
		return sdk.ToolResult{
			Content: output,
			Details: details,
		}, nil
	}

	var data []byte
	if isTar {
		data, err = readTarMember(archiveAbs, isGzip, memberPath)
	} else {
		data, err = readZipMember(archiveAbs, memberPath)
	}
	if err != nil {
		return sdk.ToolResult{}, err
	}

	display := archiveDisplay + ":" + memberPath
	if !parsed.IsRaw() {
		if mime := detectImageMIME(data); mime != "" {
			if len(data) > MaxImageBytes {
				return sdk.ToolResult{}, fmt.Errorf("Image file too large: %s exceeds %s limit.", formatBytes(int64(len(data))), formatBytes(MaxImageBytes))
			}
			details := map[string]any{
				"resolvedPath": dirs.StandardizeMacOSPath(archiveAbs),
				"meta": map[string]any{
					"source": map[string]any{"type": "path", "value": dirs.StandardizeMacOSPath(archiveAbs)},
				},
			}
			if suffixRes != nil {
				details["suffixResolution"] = suffixRes.toMap()
			}
			return sdk.ToolResult{
				Content: prependSuffixResolutionNotice(fmt.Sprintf("Read image file [%s]", mime), suffixRes),
				Images:  []sdk.ImageContent{{Data: base64.StdEncoding.EncodeToString(data), MimeType: mime}},
				Details: details,
			}, nil
		}
		if isProbablyBinary(data) {
			details := map[string]any{
				"resolvedPath": dirs.StandardizeMacOSPath(archiveAbs),
				"meta": map[string]any{
					"source": map[string]any{"type": "path", "value": dirs.StandardizeMacOSPath(archiveAbs)},
				},
			}
			if suffixRes != nil {
				details["suffixResolution"] = suffixRes.toMap()
			}
			return sdk.ToolResult{
				Content: prependSuffixResolutionNotice(fmt.Sprintf("[Cannot read binary file '%s' (%s); not valid UTF-8 text. Use ':raw' to read bytes verbatim.]", display, formatBytes(int64(len(data)))), suffixRes),
				Details: details,
			}, nil
		}
	}
	res, err := renderInMemoryText(string(data), display, "file", parsed, nil, "", 0, false, true)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	if suffixRes != nil {
		res.Content = prependSuffixResolutionNotice(res.Content, suffixRes)
	}
	details := ensureDetailsMap(&res)
	details["resolvedPath"] = dirs.StandardizeMacOSPath(archiveAbs)
	if suffixRes != nil {
		details["suffixResolution"] = suffixRes.toMap()
	}
	setMetaSource(details, "path", archiveAbs)
	return res, nil
}

func readLimited(r io.Reader, memberPath string, maxBytes int64) ([]byte, error) {
	lr := io.LimitReader(r, maxBytes+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("Archive member '%s' is too large to extract in memory (> %s limit)", memberPath, formatBytes(maxBytes))
	}
	return data, nil
}

func listZipEntries(archiveAbs string) ([]archiveEntry, error) {
	zr, err := zip.OpenReader(archiveAbs)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	if len(zr.File) > MaxArchiveEntries {
		return nil, fmt.Errorf("Archive has too many entries (> %d limit)", MaxArchiveEntries)
	}
	out := make([]archiveEntry, 0, len(zr.File))
	for _, f := range zr.File {
		name := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(f.Name, "\\", "/")), "/")
		if name == "" || name == "." {
			continue
		}
		out = append(out, archiveEntry{
			path:  name,
			isDir: f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/"),
			size:  int64(f.UncompressedSize64),
		})
	}
	return out, nil
}

func readZipMember(archiveAbs, memberPath string) ([]byte, error) {
	zr, err := zip.OpenReader(archiveAbs)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	want := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(memberPath, "\\", "/")), "/")
	for _, f := range zr.File {
		got := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(f.Name, "\\", "/")), "/")
		if got == want {
			if f.UncompressedSize64 > uint64(MaxArchiveMemberBytes) {
				return nil, fmt.Errorf("Archive member '%s' is too large to extract in memory (%s > %s limit)",
					memberPath, formatBytes(int64(f.UncompressedSize64)), formatBytes(MaxArchiveMemberBytes))
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return readLimited(rc, memberPath, MaxArchiveMemberBytes)
		}
	}
	return nil, fmt.Errorf("Archive file '%s' not found", memberPath)
}

func listTarEntries(archiveAbs string, gzipped bool) ([]archiveEntry, error) {
	f, err := os.Open(archiveAbs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if gzipped {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	}
	tr := tar.NewReader(r)
	var out []archiveEntry
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(out) >= MaxArchiveEntries {
			return nil, fmt.Errorf("Archive has too many entries (> %d limit)", MaxArchiveEntries)
		}
		name := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(hdr.Name, "\\", "/")), "/")
		if name == "" || name == "." {
			continue
		}
		out = append(out, archiveEntry{
			path:  name,
			isDir: hdr.Typeflag == tar.TypeDir,
			size:  hdr.Size,
		})
	}
	return out, nil
}

func readTarMember(archiveAbs string, gzipped bool, memberPath string) ([]byte, error) {
	f, err := os.Open(archiveAbs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if gzipped {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	}
	tr := tar.NewReader(r)
	want := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(memberPath, "\\", "/")), "/")
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		got := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(hdr.Name, "\\", "/")), "/")
		if got == want {
			if hdr.Size > MaxArchiveMemberBytes {
				return nil, fmt.Errorf("Archive member '%s' is too large to extract in memory (%s > %s limit)",
					memberPath, formatBytes(hdr.Size), formatBytes(MaxArchiveMemberBytes))
			}
			return readLimited(tr, memberPath, MaxArchiveMemberBytes)
		}
	}
	return nil, fmt.Errorf("Archive file '%s' not found", memberPath)
}

func ensureDetailsMap(res *sdk.ToolResult) map[string]any {
	if res.Details != nil {
		if m, ok := res.Details.(map[string]any); ok {
			return m
		}
	}
	m := map[string]any{}
	res.Details = m
	return m
}

// renderInMemoryText renders in-memory text (from internal resources, notebooks, or archives)
// with omp's selector, context expansion, raw mode, and optional hashline snapshot recording.
// upstream: packages/coding-agent/src/tools/read-format.ts:buildInMemorySelectorResult
func renderInMemoryText(
	rawText string,
	displayPath string,
	entityLabel string,
	parsed ParsedSelector,
	store *hashline.Store,
	absPath string,
	sourceStartLine int,
	unbounded bool,
	immutable bool,
) (sdk.ToolResult, error) {
	if entityLabel == "" {
		entityLabel = "resource"
	}
	isExcerpt := sourceStartLine > 0
	sourceOffset := 0
	if isExcerpt {
		sourceOffset = sourceStartLine - 1
	}
	_, stripped := hashline.StripBOM(rawText)
	normalized := hashline.NormalizeToLF(stripped)
	hasTrailingNL := strings.HasSuffix(normalized, "\n")

	var allLines []string
	if normalized != "" {
		allLines = hashline.SplitAddressableFileLines(normalized)
	}
	totalLines := len(allLines)
	rawMode := parsed.IsRaw()

	// When sourceStartLine is set (e.g. conflict://N excerpts), an excerpt must
	// NEVER replace the backing file's full edit snapshot: hash and record the
	// backing file on disk and offset seen lines into file coordinates.
	// upstream: packages/coding-agent/src/tools/read-format.ts:86-99, 222-237
	snapshotText := normalized
	haveSnapshotText := !isExcerpt
	if isExcerpt && !immutable && absPath != "" && store != nil {
		if backingNormalized, ok := readBackingFileNormalized(absPath); ok {
			snapshotText = backingNormalized
			haveSnapshotText = true
		}
	}

	var header string
	var tag string
	if !rawMode && !immutable && absPath != "" && store != nil && haveSnapshotText {
		tag = hashline.FileHash(snapshotText)
		header = hashline.FormatHeader(displayPath, tag)
	}

	resolvedSel := parsed.ResolveTail(totalLines)
	if resolvedSel.IsMultiRange() {
		text, displayedLines, dispContent, colCut := renderMultiRangeWithOffset(allLines, resolvedSel, displayPath, header, hasTrailingNL, !immutable, 0, sourceOffset)
		if !immutable && absPath != "" && store != nil && haveSnapshotText {
			if !rawMode || len(displayedLines) > 0 {
				store.RecordReadLines(absPath, snapshotText, displayedLines)
			}
		}
		if colCut != nil {
			text += formatColumnCutNotice(colCut, "")
		}
		// upstream: read.ts:3601-3604 never appends resource.notes to text output.
		details := map[string]any{
			"totalLines":  totalLines,
			"rowPrefixes": !rawMode && !immutable,
		}
		if rawMode {
			details["raw"] = true
		}
		if dispContent != nil {
			details["displayContent"] = dispContent
		}
		if colCut != nil {
			setMetaColumnTruncated(details, colCut, "", DefaultMaxBytes)
		}
		return sdk.ToolResult{Content: text, Details: details}, nil
	}

	var offset, limit int
	hasOffset := false
	hasLimit := false
	if resolvedSel.Kind == SelectorLines && len(resolvedSel.Ranges) == 1 {
		r := resolvedSel.Ranges[0]
		offset = r.StartLine
		hasOffset = true
		if r.EndLine > 0 {
			limit = r.EndLine - r.StartLine + 1
			hasLimit = true
		}
	}

	requestedStart := 0
	if hasOffset && offset > 1 {
		requestedStart = offset - 1
	}

	if hasOffset && requestedStart >= totalLines {
		suggestion := fmt.Sprintf("The %s is empty.", entityLabel)
		if totalLines > 0 {
			suggestion = fmt.Sprintf("Use :1- to read from the start, or :%d to read the last line.", totalLines)
		}
		return sdk.ToolResult{
			Content: fmt.Sprintf("Line %d is beyond end of %s (%d lines total). %s", requestedStart+1, entityLabel, totalLines, suggestion),
		}, nil
	}

	expandStart := !rawMode && hasOffset && offset > 1
	expandEnd := !rawMode && hasLimit
	leadingCtx := 0
	if expandStart {
		leadingCtx = min(requestedStart, RangeLeadingContextLines)
	}
	trailingCtx := 0
	if expandEnd {
		trailingCtx = RangeTrailingContextLines
	}

	startIdx := requestedStart - leadingCtx
	// Upstream read-format.ts:386-391: in-memory reads (notebooks, URL resources, archive
	// members) have no 300-line default limit; they are bounded by DEFAULT_MAX_LINES (3000)
	// and the byte budget (DEFAULT_MAX_BYTES / spillThreshold minus prefix reserve).
	var maxLinesToCollect int
	if hasLimit {
		maxLinesToCollect = min(limit+leadingCtx+trailingCtx, DefaultMaxLines)
	} else {
		maxLinesToCollect = min(totalLines-startIdx, DefaultMaxLines)
	}
	maxBytesForRead := DefaultMaxBytes
	if !unbounded {
		prefixWidth := 0
		if !rawMode && !immutable {
			prefixWidth = len(fmt.Sprintf("%d", startIdx+maxLinesToCollect)) + 1
		}
		reserve := min(maxLinesToCollect*prefixWidth, DefaultSpillThreshold/2)
		maxBytesForRead = min(DefaultMaxBytes, DefaultSpillThreshold-reserve)
	}
	if unbounded {
		maxLinesToCollect = totalLines - startIdx
		if hasLimit && limit+leadingCtx+trailingCtx < maxLinesToCollect {
			maxLinesToCollect = limit + leadingCtx + trailingCtx
		}
		maxBytesForRead = 1 << 30
	}
	var collected []string
	collectedBytes := 0
	stoppedByByteLimit := false
	for i := startIdx; i < totalLines && len(collected) < maxLinesToCollect; i++ {
		line := allLines[i]
		lineBytes := len(line)
		if len(collected) > 0 {
			lineBytes++
		}
		if !unbounded && len(collected) > 0 && collectedBytes+lineBytes > maxBytesForRead {
			stoppedByByteLimit = true
			break
		}
		if !unbounded && len(collected) == 0 && lineBytes > maxBytesForRead {
			stoppedByByteLimit = true
			break
		}
		collected = append(collected, line)
		collectedBytes += lineBytes
	}

	startLineDisplay := startIdx + 1 + sourceOffset
	if totalLines == 0 {
		if rawMode {
			return sdk.ToolResult{Content: "", Details: map[string]any{"raw": true, "totalLines": 0}}, nil
		}
		out := ""
		details := map[string]any{"totalLines": 0, "rowPrefixes": header != ""}
		if header != "" {
			ln := 1 + sourceOffset
			out = fmt.Sprintf("%s\n%d:", header, ln)
			if haveSnapshotText {
				store.RecordReadLines(absPath, snapshotText, []int{ln})
			}
			details["totalLines"] = 1
			details["displayContent"] = map[string]any{
				"text":        "",
				"startLine":   ln,
				"lineNumbers": []any{ln},
			}
		}
		if !unbounded && len(out) <= DefaultSpillThreshold {
			setMetaPagedSource(details)
		}
		return sdk.ToolResult{Content: out, Details: details}, nil
	}

	// Empty-window byte overflow.
	if len(collected) == 0 && stoppedByByteLimit && startIdx < totalLines {
		firstBytes := len(allLines[startIdx])
		if !rawMode && !immutable {
			return sdk.ToolResult{
				Content: fmt.Sprintf("[Line %d is %s, exceeds %s limit. Hashline output requires full lines; cannot emit an editable numbered preview for a truncated line.]",
					startLineDisplay, formatBytes(int64(firstBytes)), formatBytes(int64(maxBytesForRead))),
				Details: map[string]any{"totalLines": totalLines},
			}, nil
		}
		snippet := truncateUTF8Bytes(allLines[startIdx], maxBytesForRead)
		return sdk.ToolResult{Content: snippet, Details: map[string]any{"raw": rawMode, "totalLines": totalLines}}, nil
	}

	var out string
	var dispContent map[string]any
	addPrefixes := !rawMode && !immutable
	endIdx := startIdx + len(collected)
	selectedWindowLines := totalLines - startIdx
	if rawMode && hasLimit {
		selectedWindowLines = min(limit, selectedWindowLines)
	}
	rawTruncated := rawMode && !unbounded && len(collected) < selectedWindowLines
	if rawMode {
		out = strings.Join(collected, "\n")
		// Keep last line's terminator whenever it closes the line (not at EOF, or EOF has '\n').
		// upstream: packages/coding-agent/src/tools/read-format.ts:191-202
		if len(collected) > 0 && (endIdx < totalLines || hasTrailingNL) {
			out += "\n"
		}
		if len(collected) > 0 {
			dispContent = map[string]any{
				"text":        out,
				"startLine":   startLineDisplay,
				"lineNumbers": contiguousLineNumAny(startLineDisplay, countTextLines(out)),
			}
		}
		if !immutable && absPath != "" && store != nil && haveSnapshotText && len(collected) > 0 {
			seen := contiguousLineNums(startLineDisplay, len(collected))
			store.RecordReadLines(absPath, snapshotText, seen)
		}
	} else {
		plainJoined := strings.Join(collected, "\n")
		dispContent = map[string]any{
			"text":        plainJoined,
			"startLine":   startLineDisplay,
			"lineNumbers": contiguousLineNumAny(startLineDisplay, len(collected)),
		}
		if addPrefixes {
			body := hashline.FormatNumberedLines(plainJoined, startLineDisplay)
			if header != "" {
				out = header + "\n" + body
			} else {
				out = body
			}
			if absPath != "" && store != nil && haveSnapshotText {
				if !isExcerpt && startIdx == 0 && len(collected) == totalLines {
					store.RecordReadWholeFile(absPath, snapshotText)
				} else {
					store.RecordReadLines(absPath, snapshotText, contiguousLineNums(startLineDisplay, len(collected)))
				}
			}
		} else {
			out = plainJoined
		}
	}
	details := map[string]any{
		"totalLines":  totalLines,
		"rowPrefixes": addPrefixes,
	}
	if rawMode {
		details["raw"] = true
	}
	if dispContent != nil {
		details["displayContent"] = dispContent
	}
	if (!rawMode && endIdx < totalLines) || rawTruncated {
		nextOffset := endIdx + 1
		truncBy := "lines"
		if stoppedByByteLimit {
			truncBy = "bytes"
			out += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Use :%d- to continue]",
				startLineDisplay, endIdx, totalLines, formatBytes(int64(maxBytesForRead)), nextOffset)
		} else if !rawMode && hasLimit {
			remaining := totalLines - endIdx
			out += fmt.Sprintf("\n\n[%d more lines in %s. Use :%d- to continue]", remaining, entityLabel, nextOffset)
		} else {
			out += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use :%d- to continue]",
				startLineDisplay, endIdx, totalLines, nextOffset)
		}
		if stoppedByByteLimit || rawTruncated || !hasLimit {
			// upstream: packages/coding-agent/src/tools/read-format.ts:540-549, output-meta.ts:188
			details["truncation"] = map[string]any{
				"truncated":             true,
				"truncatedBy":           truncBy,
				"totalLines":            selectedWindowLines,
				"totalBytes":            len(normalized),
				"outputLines":           len(collected),
				"outputBytes":           collectedBytes,
				"lastLinePartial":       false,
				"firstLineExceedsLimit": false,
			}
			truncMeta := map[string]any{
				"direction":   "head",
				"truncatedBy": truncBy,
				"totalLines":  totalLines,
				"totalBytes":  len(normalized),
				"outputLines": len(collected),
				"outputBytes": collectedBytes,
				"maxBytes":    maxBytesForRead,
				"shownRange":  map[string]any{"start": startLineDisplay, "end": endIdx},
				"nextOffset":  nextOffset,
			}
			setMetaTruncation(details, truncMeta)
		}
	}
	if !unbounded && len(out) <= DefaultSpillThreshold {
		setMetaPagedSource(details)
	}
	return sdk.ToolResult{Content: out, Details: details}, nil
}

// readBackingFileNormalized reads a backing file from disk (up to SnapshotMaxBytes)
// and returns its BOM-stripped LF-normalized text for excerpt snapshotting.
// upstream: packages/coding-agent/src/tools/read-format.ts:95-99, crates/edit/src/store.rs
func readBackingFileNormalized(absPath string) (string, bool) {
	info, err := os.Stat(absPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > SnapshotMaxBytes {
		return "", false
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", false
	}
	_, stripped := hashline.StripBOM(string(data))
	return hashline.NormalizeToLF(stripped), true
}
