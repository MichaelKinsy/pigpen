package search

import (
	"context"
	"fmt"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// searchURLFS adapts internal/urls for search tools (grep, glob, find).
// File-backed schemes locate their backing host path via urls.ResolveTarget/urls.Locate;
// virtual schemes enumerate leaves via urls.Enumerator, list via urls.DirectoryReader,
// or resolve single resources via urls.Resolve.
// upstream: packages/coding-agent/src/internal-urls/url-filesystem.ts
type searchURLFS struct {
	ctx      urls.Context
	leavesMu sync.Mutex
	leaves   map[string][]urls.Leaf
}

func newSearchURLFS(ctx urls.Context) *searchURLFS {
	ctx.SkipDirectoryListing = true
	return &searchURLFS{
		ctx:    ctx,
		leaves: make(map[string][]urls.Leaf),
	}
}

// urlNodeKind classifies a resolved internal URL target.
type urlNodeKind int

const (
	urlNodeHostFile urlNodeKind = iota
	urlNodeHostDir
	urlNodeVirtualFile
	urlNodeVirtualDir
)

type urlNode struct {
	kind      urlNodeKind
	url       string
	spec      urls.Spec
	hostPath  string
	content   []byte
	size      int64
	mtime     time.Time
	absent    bool
	immutable bool
	leaves    []urls.Leaf // for enumerated virtual directories
}

// isRegisteredURL reports whether raw (after single-slash alias normalization)
// targets a registered internal URL scheme.
func isRegisteredURL(raw string) bool {
	return urls.CanHandle(normalizeInternalURL(raw))
}

// normalizeInternalURL rewrites single-slash aliases (scheme:/x) and path-prefixed
// URLs (/abs/scheme://x) for schemes declaring SingleSlashAlias.
// upstream: packages/coding-agent/src/internal-urls/router.ts:208-220
func normalizeInternalURL(input string) string {
	// Check for filesystem path prefixed onto scheme:// (e.g. /repo/local://x).
	if !hasURLPrefix(input) {
		for i := range len(input) {
			if input[i] != '/' && input[i] != '\\' {
				continue
			}
			rest := input[i+1:]
			scheme, after, ok := strings.Cut(rest, "://")
			if !ok || !isValidSchemeName(scheme) {
				continue
			}
			if len(after) > 0 && after[0] == '/' {
				continue
			}
			if h, found := urls.Lookup(scheme); found && h.Spec().SingleSlashAlias {
				return rest
			}
		}
	}
	// Check for single-slash alias scheme:/x (not scheme://).
	if colon := strings.IndexByte(input, ':'); colon > 0 && colon+1 < len(input) && input[colon+1] == '/' {
		if colon+2 >= len(input) || input[colon+2] != '/' {
			scheme := input[:colon]
			if isValidSchemeName(scheme) {
				if h, found := urls.Lookup(scheme); found && h.Spec().SingleSlashAlias {
					return scheme + "://" + input[colon+2:]
				}
			}
		}
	}
	return input
}

func hasURLPrefix(input string) bool {
	colon := strings.IndexByte(input, ':')
	if colon <= 0 {
		return false
	}
	// Windows drive letter (C:\ or C:/) is not a URL scheme.
	if colon == 1 && colon+1 < len(input) && (input[colon+1] == '/' || input[colon+1] == '\\') {
		return false
	}
	return isValidSchemeName(input[:colon])
}

func isValidSchemeName(s string) bool {
	if len(s) == 0 {
		return false
	}
	c0 := s[0]
	if !((c0 >= 'a' && c0 <= 'z') || (c0 >= 'A' && c0 <= 'Z')) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '+' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

// isInternalURLGlob reports whether a registered URL contains glob metacharacters
// outside any query string or port-authority host segment.
// upstream: packages/coding-agent/src/internal-urls/router.ts
func isInternalURLGlob(raw string) bool {
	norm := normalizeInternalURL(raw)
	if !urls.CanHandle(norm) {
		return false
	}
	u, err := urls.Parse(norm)
	if err != nil {
		return false
	}
	h, ok := urls.Lookup(u.Scheme)
	if !ok {
		return false
	}
	check := norm[len(u.Scheme)+3:]
	// Ignore query strings (`?key=val`).
	if q := strings.IndexByte(check, '?'); q >= 0 {
		afterQ := check[q+1:]
		if eq := strings.IndexByte(afterQ, '='); eq >= 0 && isQueryKey(afterQ[:eq]) {
			check = check[:q]
		}
	}
	if h.Spec().PortAuthority {
		if slash := strings.IndexByte(check, '/'); slash >= 0 {
			check = check[slash+1:]
		} else {
			return false
		}
	}
	return hasGlobChars(check)
}

func isQueryKey(s string) bool {
	for i := range len(s) {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

// splitInternalURLSel peels trailing selector-shaped chunks from an internal URL
// if the registered scheme declares Selectors == SelectorsLines.
// upstream: packages/tui/src/tools/read.ts:245-282
func splitInternalURLSel(raw string) (base string, sel string, hasSel bool) {
	norm := normalizeInternalURL(raw)
	u, err := urls.Parse(norm)
	if err != nil {
		return raw, "", false
	}
	h, ok := urls.Lookup(u.Scheme)
	if !ok {
		return raw, "", false
	}
	spec := h.Spec()
	if spec.Selectors != urls.SelectorsLines {
		return norm, "", false
	}
	schemeEnd := len(u.Scheme) + 3
	rest := norm[schemeEnd:]
	if spec.PortAuthority && !strings.Contains(rest, "/") {
		return norm, "", false
	}
	cur := norm
	var chunks []string
	for {
		colon := strings.LastIndexByte(cur, ':')
		if colon < schemeEnd {
			break
		}
		part := cur[colon+1:]
		if !isInternalURLSelectorPart(part) {
			break
		}
		chunks = append(chunks, part)
		cur = cur[:colon]
	}
	if len(chunks) == 0 {
		return norm, "", false
	}
	for i, j := 0, len(chunks)-1; i < j; i, j = i+1, j-1 {
		chunks[i], chunks[j] = chunks[j], chunks[i]
	}
	return cur, strings.Join(chunks, ":"), true
}

func isInternalURLSelectorPart(part string) bool {
	lower := strings.ToLower(part)
	if lower == "raw" || lower == "conflicts" || lower == "img" {
		return true
	}
	if _, ok := parseLineRanges(part); ok {
		return true
	}
	// Permissive malformed tail shape like -10 or -100-5 so grep rejects it cleanly.
	if strings.HasPrefix(part, "-") && len(part) > 1 {
		rest := part[1:]
		for i := range len(rest) {
			c := rest[i]
			if !((c >= '0' && c <= '9') || c == '-' || c == '+') {
				return false
			}
		}
		return true
	}
	return false
}

// isReadSelectorGrammar mirrors grep.ts:144-156.
func isReadSelectorGrammar(sel string) ([]lineRange, bool) {
	if strings.Contains(sel, ":") {
		parts := strings.Split(sel, ":")
		if len(parts) != 2 {
			return nil, false
		}
		aRaw := strings.EqualFold(parts[0], "raw")
		bRaw := strings.EqualFold(parts[1], "raw")
		var rangeChunk string
		switch {
		case aRaw && !bRaw:
			rangeChunk = parts[1]
		case bRaw && !aRaw:
			rangeChunk = parts[0]
		default:
			return nil, false
		}
		ranges, ok := parseLineRanges(rangeChunk)
		return ranges, ok
	}
	lower := strings.ToLower(sel)
	if lower == "raw" || lower == "conflicts" {
		return nil, true
	}
	ranges, ok := parseLineRanges(sel)
	return ranges, ok
}

// joinURLPath appends a forward-slash relative path onto a URL base,
// percent-encoding special characters in each segment.
// upstream: packages/coding-agent/src/internal-urls/url-filesystem.ts:157-165
func joinURLPath(base, rel string) string {
	if rel == "" {
		return base
	}
	segs := strings.Split(rel, "/")
	var enc []string
	for _, s := range segs {
		if s == "" {
			continue
		}
		enc = append(enc, encodeURLSegment(s))
	}
	if len(enc) == 0 {
		return base
	}
	trimmed := strings.TrimRight(base, "/")
	if strings.HasSuffix(trimmed, ":") && isValidSchemeName(trimmed[:len(trimmed)-1]) {
		return trimmed + "//" + strings.Join(enc, "/")
	}
	return trimmed + "/" + strings.Join(enc, "/")
}

func encodeURLSegment(name string) string {
	var b strings.Builder
	for i := range len(name) {
		c := name[i]
		if c <= 0x20 || c == 0x7f || strings.IndexByte(`"#%<>?[\]^`+"`"+`{|} `, c) >= 0 {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

func decodeURLGlobSegment(seg, input string) (string, error) {
	if !strings.Contains(seg, "%") {
		return seg, nil
	}
	var out strings.Builder
	for i := 0; i < len(seg); {
		if seg[i] == '%' {
			j := i
			for j+2 < len(seg) && seg[j] == '%' && isHexDigit(seg[j+1]) && isHexDigit(seg[j+2]) {
				j += 3
			}
			if j == i {
				return "", fmt.Errorf("Invalid URL encoding in glob pattern: %s", input)
			}
			decoded, err := url.PathUnescape(seg[i:j])
			if err != nil {
				return "", fmt.Errorf("Invalid URL encoding in glob pattern: %s", input)
			}
			for _, r := range decoded {
				switch r {
				case '*', '?', '[', '{':
					out.WriteByte('[')
					out.WriteRune(r)
					out.WriteByte(']')
				default:
					out.WriteRune(r)
				}
			}
			i = j
			continue
		}
		out.WriteByte(seg[i])
		i++
	}
	return out.String(), nil
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// resolve resolves a registered internal URL to either a host file/dir or a virtual file/dir.
func (f *searchURLFS) resolve(rawURL string) (*urlNode, error) {
	norm := normalizeInternalURL(rawURL)
	u, err := urls.Parse(norm)
	if err != nil {
		return nil, err
	}
	h, ok := urls.Lookup(u.Scheme)
	if !ok {
		return nil, fmt.Errorf("Unknown protocol: %s://", u.Scheme)
	}
	spec := h.Spec()

	// File-backed schemes: locate via urls.Locate (directory form preferred as in url-filesystem.ts:976-981)
	// and urls.ResolveTarget.
	if spec.Backing == urls.BackingFile {
		hostPath, found, locErr := urls.Locate(f.ctx, norm, urls.LocateOptions{Directory: true})
		if locErr != nil {
			return nil, locErr
		}
		if !found {
			if target, tErr := urls.ResolveTarget(f.ctx, norm); tErr == nil && target != nil && target.Kind == "file" && target.Path != "" {
				hostPath = target.Path
				found = true
			}
		}
		if found && hostPath != "" {
			info, statErr := os.Stat(hostPath)
			if statErr != nil {
				return nil, statErr
			}
			kind := urlNodeHostFile
			if info.IsDir() {
				kind = urlNodeHostDir
			}
			return &urlNode{
				kind:      kind,
				url:       norm,
				spec:      spec,
				hostPath:  hostPath,
				size:      info.Size(),
				mtime:     info.ModTime(),
				immutable: spec.Immutable,
			}, nil
		}
	}

	// Enumerable virtual containers (e.g. omp://, help://, custom virtual schemes).
	if enum, isEnum := h.(urls.Enumerator); isEnum {
		leaves, enumErr := f.enumeratedLeaves(u.Scheme, enum)
		if enumErr != nil {
			return nil, enumErr
		}
		// Exact leaf match?
		for _, leaf := range leaves {
			if leaf.URL == norm {
				b := []byte(leaf.Content)
				return &urlNode{
					kind:      urlNodeVirtualFile,
					url:       norm,
					spec:      spec,
					content:   b,
					size:      int64(len(b)),
					immutable: spec.Immutable,
				}, nil
			}
		}
		// Container prefix match?
		prefix := norm
		if !strings.HasSuffix(prefix, "://") && !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		var subLeaves []urls.Leaf
		for _, leaf := range leaves {
			if strings.HasPrefix(leaf.URL, prefix) {
				subLeaves = append(subLeaves, leaf)
			}
		}
		if len(subLeaves) > 0 || norm == u.Scheme+"://" {
			return &urlNode{
				kind:      urlNodeVirtualDir,
				url:       norm,
				spec:      spec,
				immutable: spec.Immutable,
				leaves:    subLeaves,
			}, nil
		}
	}

	// Fallback: single-resource resolve via urls.Resolve.
	res, resErr := urls.Resolve(f.ctx, norm)
	if resErr != nil {
		return nil, resErr
	}
	if res == nil {
		return nil, fmt.Errorf("Path not found: %s", norm)
	}
	immutable := spec.Immutable
	if res.Immutable != nil {
		immutable = *res.Immutable
	}
	if res.IsDirectory {
		if res.SourcePath != "" {
			if info, err := os.Stat(res.SourcePath); err == nil && info.IsDir() {
				return &urlNode{
					kind:      urlNodeHostDir,
					url:       norm,
					spec:      spec,
					hostPath:  res.SourcePath,
					mtime:     info.ModTime(),
					immutable: immutable,
				}, nil
			}
		}
		return &urlNode{
			kind:      urlNodeVirtualDir,
			url:       norm,
			spec:      spec,
			immutable: immutable,
		}, nil
	}
	var data []byte
	if res.Bytes != nil {
		data = res.Bytes
	} else {
		data = []byte(res.Content)
	}
	return &urlNode{
		kind:      urlNodeVirtualFile,
		url:       norm,
		spec:      spec,
		hostPath:  res.SourcePath,
		content:   data,
		size:      int64(len(data)),
		absent:    res.Absent,
		immutable: immutable,
	}, nil
}

func (f *searchURLFS) enumeratedLeaves(scheme string, enum urls.Enumerator) ([]urls.Leaf, error) {
	f.leavesMu.Lock()
	if cached, ok := f.leaves[scheme]; ok {
		f.leavesMu.Unlock()
		return cached, nil
	}
	f.leavesMu.Unlock()

	rootURL, err := urls.Parse(scheme + "://")
	if err != nil {
		return nil, err
	}
	leaves, err := enum.Enumerate(f.ctx, rootURL)
	if err != nil {
		return nil, err
	}

	f.leavesMu.Lock()
	if cached, ok := f.leaves[scheme]; ok {
		f.leavesMu.Unlock()
		return cached, nil
	}
	f.leaves[scheme] = leaves
	f.leavesMu.Unlock()
	return leaves, nil
}

// virtualWalkEntry represents a file or directory discovered under a virtual URL container.
type virtualWalkEntry struct {
	url     string
	relPath string
	isDir   bool
	content []byte
	size    int64
}

// walkVirtualDir enumerates entries under a virtual directory node, applying glob/exclude/recursive filters.
func (f *searchURLFS) walkVirtualDir(ctx context.Context, node *urlNode, include *globMatcher, excludes []*globMatcher, recursive, includeDir, dirOnly bool) ([]virtualWalkEntry, error) {
	base := node.url
	prefix := base
	if !strings.HasSuffix(prefix, "://") && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	// Enumerator-backed containers already have every leaf URL and content.
	if len(node.leaves) > 0 || node.url == node.specBackingRoot(node.url) {
		dirsSeen := make(map[string]struct{})
		var out []virtualWalkEntry
		for _, leaf := range node.leaves {
			if ctx != nil && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if !strings.HasPrefix(leaf.URL, prefix) {
				continue
			}
			rel := leaf.URL[len(prefix):]
			if rel == "" {
				continue
			}
			// Collect intermediate directories for glob directory entries.
			parts := strings.Split(rel, "/")
			if includeDir {
				for i := 1; i < len(parts); i++ {
					if !recursive && i > 1 {
						break
					}
					dRel := strings.Join(parts[:i], "/")
					if _, seen := dirsSeen[dRel]; seen {
						continue
					}
					dirsSeen[dRel] = struct{}{}
					if isExcluded(excludes, dRel, parts[i-1], true) {
						continue
					}
					if include != nil && !include.match(dRel) && !include.match(dRel+"/") {
						continue
					}
					out = append(out, virtualWalkEntry{
						url:     joinURLPath(base, dRel),
						relPath: dRel,
						isDir:   true,
					})
				}
			}
			if dirOnly {
				continue
			}
			if !recursive && len(parts) > 1 {
				continue
			}
			baseName := parts[len(parts)-1]
			if isExcluded(excludes, rel, baseName, false) {
				continue
			}
			// Also check if any ancestor dir is excluded.
			ancestorExcluded := false
			for i := 1; i < len(parts); i++ {
				dRel := strings.Join(parts[:i], "/")
				if isExcluded(excludes, dRel, parts[i-1], true) {
					ancestorExcluded = true
					break
				}
			}
			if ancestorExcluded {
				continue
			}
			if include != nil && !include.match(rel) {
				continue
			}
			data := []byte(leaf.Content)
			out = append(out, virtualWalkEntry{
				url:     leaf.URL,
				relPath: rel,
				isDir:   false,
				content: data,
				size:    int64(len(data)),
			})
		}
		sort.Slice(out, func(i, j int) bool {
			return out[i].relPath < out[j].relPath
		})
		return out, nil
	}

	// DirectoryReader-backed virtual/remote container.
	u, err := urls.Parse(node.url)
	if err != nil {
		return nil, err
	}
	h, ok := urls.Lookup(u.Scheme)
	if !ok {
		return nil, nil
	}
	dr, isDR := h.(urls.DirectoryReader)
	if !isDR {
		return nil, nil
	}

	var out []virtualWalkEntry
	var visit func(dirURL, relDir string) error
	visit = func(dirURL, relDir string) error {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		du, err := urls.Parse(dirURL)
		if err != nil {
			return err
		}
		entries, err := dr.ReadDirectory(f.ctx, du)
		if err != nil {
			return err
		}
		for _, e := range entries {
			relPath := e.Name
			if relDir != "" {
				relPath = relDir + "/" + e.Name
			}
			childURL := joinURLPath(base, relPath)
			if isExcluded(excludes, relPath, e.Name, e.IsDir) {
				continue
			}
			if e.IsDir {
				if includeDir && (include == nil || include.match(relPath) || include.match(relPath+"/")) {
					out = append(out, virtualWalkEntry{
						url:     childURL,
						relPath: relPath,
						isDir:   true,
					})
				}
				if recursive {
					if err := visit(childURL, relPath); err != nil {
						return err
					}
				}
				continue
			}
			if dirOnly {
				continue
			}
			if include != nil && !include.match(relPath) {
				continue
			}
			out = append(out, virtualWalkEntry{
				url:     childURL,
				relPath: relPath,
				isDir:   false,
				size:    e.Size,
			})
		}
		return nil
	}
	if err := visit(base, ""); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].relPath < out[j].relPath
	})
	return out, nil
}

func (n *urlNode) specBackingRoot(raw string) string {
	if idx := strings.Index(raw, "://"); idx > 0 {
		return raw[:idx+3]
	}
	return ""
}

// snapshotHostPath returns the backing host file path for snapshot-tag recording,
// or "" if the URL is immutable or has no local backing file.
// upstream: packages/coding-agent/src/tools/file-recorder.ts:48-58
func (f *searchURLFS) snapshotHostPath(resultPath, cwd string) string {
	if !isRegisteredURL(resultPath) {
		return resolvePath(resultPath, cwd)
	}
	norm := normalizeInternalURL(resultPath)
	u, err := urls.Parse(norm)
	if err != nil {
		return ""
	}
	h, ok := urls.Lookup(u.Scheme)
	if !ok || h.Spec().Immutable {
		return ""
	}
	loc, found, err := urls.Locate(f.ctx, norm, urls.LocateOptions{})
	if err != nil || !found {
		return ""
	}
	return loc
}

// displayTarget returns the local backing file path for hyperlink metadata (displayTargets),
// or "" when the URL is not linkable or has no local backing.
func (f *searchURLFS) displayTarget(rawURL string) string {
	if !isRegisteredURL(rawURL) {
		return ""
	}
	norm := normalizeInternalURL(rawURL)
	u, err := urls.Parse(norm)
	if err != nil {
		return ""
	}
	h, ok := urls.Lookup(u.Scheme)
	if !ok || !h.Spec().Linkable {
		return ""
	}
	loc, found, err := urls.Locate(f.ctx, norm, urls.LocateOptions{})
	if err != nil || !found {
		return ""
	}
	return dirs.StandardizeMacOSPath(loc)
}

// probeURLTarget reports whether a raw internal URL (or its glob base directory)
// resolves to an existing non-absent entry. Used when deciding whether a `;`
// inside a URL-prefixed list should split or stay whole.
// upstream: packages/coding-agent/src/tools/path-utils.ts:720-754
func (f *searchURLFS) probeURLTarget(entry string) bool {
	peeled, _, _ := splitInternalURLSel(entry)
	if isInternalURLGlob(peeled) {
		parsed, err := parseURLSearchTarget(peeled)
		if err != nil || parsed == nil {
			return false
		}
		if parsed.hasGlob {
			tail := peeled[len(parsed.basePath):]
			if len(splitTopLevelSemicolon(tail)) == 1 {
				node, err := f.resolve(parsed.basePath)
				if err != nil {
					return false
				}
				return !node.absent && (node.kind == urlNodeHostDir || node.kind == urlNodeVirtualDir)
			}
		}
	}
	node, err := f.resolve(peeled)
	if err != nil {
		return false
	}
	return !node.absent
}

// isSkillURL reports whether p targets the skill:// scheme (which follows contained symlinks).
func isSkillURL(p string) bool {
	return strings.HasPrefix(strings.ToLower(p), "skill://")
}

// readPrefix reads up to maxBytes of either a host file or a registered internal URL.
// upstream: packages/coding-agent/src/internal-urls/url-filesystem.ts:431-441
func (f *searchURLFS) readPrefix(target string, maxBytes int) ([]byte, bool, error) {
	if isRegisteredURL(target) {
		node, err := f.resolve(target)
		if err != nil {
			return nil, false, err
		}
		if node.kind == urlNodeHostDir || node.kind == urlNodeVirtualDir {
			return nil, false, fmt.Errorf("Is a directory: %s", target)
		}
		if node.kind == urlNodeVirtualFile {
			if len(node.content) > maxBytes {
				return node.content[:maxBytes], true, nil
			}
			return node.content, false, nil
		}
		return readHostFilePrefix(node.hostPath, maxBytes)
	}
	return readHostFilePrefix(target, maxBytes)
}

func readHostFilePrefix(hostPath string, maxBytes int) ([]byte, bool, error) {
	file, err := os.Open(hostPath)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()

	want := maxBytes + 1
	if info, statErr := file.Stat(); statErr == nil && info.Mode().IsRegular() {
		if sz := info.Size(); sz >= 0 && sz < int64(want) {
			want = int(sz) + 1
		}
	}
	// Read up to maxBytes+1 to detect truncation accurately without over-allocating.
	buf := make([]byte, want)
	total := 0
	for total < len(buf) {
		n, readErr := file.Read(buf[total:])
		total += n
		if readErr != nil {
			break
		}
		if n == 0 {
			break
		}
	}
	if total > maxBytes {
		return buf[:maxBytes], true, nil
	}
	return buf[:total], false, nil
}

// resolveURLHostRelative builds the display path for a file discovered inside a
// file-backed internal URL directory (e.g. skill://demo/references/guide.md).
func resolveURLHostRelative(urlBase, hostBase, hostAbs string, isDir bool) string {
	rel, err := filepath.Rel(hostBase, hostAbs)
	if err != nil || rel == "." {
		out := urlBase
		if isDir && !strings.HasSuffix(out, "/") {
			out += "/"
		}
		return out
	}
	out := joinURLPath(urlBase, filepath.ToSlash(rel))
	if isDir && !strings.HasSuffix(out, "/") {
		out += "/"
	}
	return out
}
