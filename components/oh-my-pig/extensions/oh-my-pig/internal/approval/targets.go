package approval

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/src/tools/approval-targets.ts
// upstream: packages/coding-agent/src/tools/approval-cache.ts
// upstream: packages/coding-agent/src/tools/search-approval-targets.ts
// upstream: packages/coding-agent/src/tools/approval.ts:159-257,320-380,494-574,777-841

const (
	approvalTreeSearchPrefix = "tree\x00"
	approvalFolderScanLimit  = 200
	approvalFolderFullLimit  = 24
)

// ApprovalRestrictions holds `tools.restrictedFolders` for one call.
// upstream: packages/coding-agent/src/tools/approval.ts:320-328
type ApprovalRestrictions struct {
	RestrictedFolders []string
	Cwd               string
}

type restrictionSubject struct {
	path      string
	recursive bool
}

type approvalGrantChoice struct {
	scope       string // "folder" | "target"
	label       string
	alwaysLabel string
}

type approvalFolderListing struct {
	entries   []string
	rest      int
	folders   int
	files     int
	total     int
	truncated bool
}

var (
	approvalRootDelimsRE = regexp.MustCompile(`[;,\s]+`)
	readableURLSchemeRE  = regexp.MustCompile(`(?i)^https?://`)
	readableWWWRE        = regexp.MustCompile(`(?i)^www\.`)
	ipv4LiteralRE        = regexp.MustCompile(`^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$`)
	globMetaChars        = "*?[{"
)

// ApprovalGrantKey returns the cache grant key `<tier>\x00<subject>`.
// upstream: packages/coding-agent/src/tools/approval-cache.ts:25-27
func ApprovalGrantKey(capability Tier, subject string) string {
	return string(capability) + "\x00" + subject
}

// ApprovalTreeSearchKey wraps a directory key as a recursive tree-search key.
// upstream: packages/coding-agent/src/tools/approval-targets.ts:24-26
func ApprovalTreeSearchKey(dirKey string) string {
	return approvalTreeSearchPrefix + dirKey
}

// ApprovalTreeSearchFolderKey extracts the folder key from a tree-search key.
// upstream: packages/coding-agent/src/tools/approval-targets.ts:29-31
func ApprovalTreeSearchFolderKey(key string) (string, bool) {
	if strings.HasPrefix(key, approvalTreeSearchPrefix) {
		return key[len(approvalTreeSearchPrefix):], true
	}
	return "", false
}

func withTrailingSep(dir string) string {
	sep := string(filepath.Separator)
	if strings.HasSuffix(dir, sep) {
		return dir
	}
	return dir + sep
}

// resolveEquivalentPath resolves symlinks through the deepest existing ancestor
// so non-existent paths under a symlinked directory resolve to the real target.
// upstream: packages/utils/src/path.ts resolveEquivalentPath({ throughMissing: true })
func resolveEquivalentPath(target string) string {
	abs, err := filepath.Abs(target)
	if err != nil {
		abs = filepath.Clean(target)
	}
	if realPath, err := filepath.EvalSymlinks(abs); err == nil {
		return realPath
	}
	// Walk upward to deepest existing ancestor.
	cur := abs
	var tail []string
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
		if realParent, err := filepath.EvalSymlinks(cur); err == nil {
			parts := make([]string, 0, len(tail)+1)
			parts = append(parts, realParent)
			for i := len(tail) - 1; i >= 0; i-- {
				parts = append(parts, tail[i])
			}
			return filepath.Join(parts...)
		}
	}
}

// ApprovalCacheKeyForPath normalizes a filesystem path for cache/restriction
// comparison: absolute, symlinks resolved through missing tails, case-folded
// on Windows.
// upstream: packages/coding-agent/src/tools/approval-cache.ts:154-157
func ApprovalCacheKeyForPath(target string) string {
	resolved := resolveEquivalentPath(target)
	if runtime.GOOS == "windows" {
		return strings.ToLower(resolved)
	}
	return resolved
}

// ApprovalDirectoryKey returns ApprovalCacheKeyForPath with a trailing separator.
// upstream: packages/coding-agent/src/tools/approval-cache.ts:160-162
func ApprovalDirectoryKey(dir string) string {
	return withTrailingSep(ApprovalCacheKeyForPath(dir))
}

func expandTildePath(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func resolvePathToCwd(rawPath, cwd string) string {
	s := expandTildePath(rawPath)
	if strings.HasPrefix(s, "@") {
		withoutAt := s[1:]
		if strings.HasPrefix(withoutAt, "/") || withoutAt == "~" || strings.HasPrefix(withoutAt, "~/") || filepath.IsAbs(withoutAt) {
			s = expandTildePath(withoutAt)
		}
	}
	if strings.HasPrefix(strings.ToLower(s), "file://") {
		rest := s[len("file://"):]
		if strings.HasPrefix(rest, "localhost/") {
			rest = rest[len("localhost"):]
		}
		if rest != "" {
			s = rest
		}
	}
	if filepath.IsAbs(s) {
		return filepath.Clean(s)
	}
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	return filepath.Join(cwd, s)
}

func isReadableURLPath(s string) bool {
	return readableURLSchemeRE.MatchString(s) || readableWWWRE.MatchString(s)
}

// isApprovalPublicURL mirrors tools/approval-targets.ts:34-76.
func isApprovalPublicURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		u, err = url.Parse("https://" + raw)
		if err != nil || u.Hostname() == "" {
			return false
		}
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimRight(host, ".")
	if host == "" {
		return false
	}
	if !strings.Contains(host, ".") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".internal") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			a, b := v4[0], v4[1]
			if a == 0 || a == 10 || a == 127 || a >= 224 {
				return false
			}
			if a == 192 && b == 168 {
				return false
			}
			if a == 172 && b >= 16 && b <= 31 {
				return false
			}
			if a == 169 && b == 254 {
				return false
			}
			if a == 100 && b >= 64 && b <= 127 {
				return false
			}
			return true
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() || ip.IsUnspecified() {
			return false
		}
		if strings.HasPrefix(host, "fe80:") || strings.HasPrefix(host, "fc") || strings.HasPrefix(host, "fd") || strings.HasPrefix(host, "::ffff:") {
			return false
		}
		return true
	}
	if ipv4LiteralRE.MatchString(host) {
		return false
	}
	return true
}

// upstreamUnregisteredSchemes are hierarchical internal URL schemes registered
// in omp's InternalUrlRouter (`internal-urls/router.ts:110-130`) that oh-my-pig
// omits from `internal/urls` (see `internal/urls/UPSTREAM.md`). Recognizing
// them here ensures cache keys and file-vs-URL classification match omp instead
// of resolving `ssh://host/etc/passwd` or `vault://x` as relative host paths.
var upstreamUnregisteredSchemes = map[string]bool{
	"ssh":      true,
	"vault":    true,
	"memory":   true,
	"omp":      true,
	"security": true,
}

func isInternalOrUpstreamURL(input string) (string, bool) {
	norm := urls.Normalize(input)
	if urls.CanHandle(norm) {
		return norm, true
	}
	scheme, _, ok := urls.SplitScheme(norm)
	if ok && upstreamUnregisteredSchemes[strings.ToLower(scheme)] {
		return norm, true
	}
	return norm, false
}

func approvalTargetNamesFile(target string) bool {
	if strings.HasPrefix(target, approvalTreeSearchPrefix) {
		return false
	}
	if _, isURL := isInternalOrUpstreamURL(target); isURL || isReadableURLPath(target) {
		return false
	}
	if info, err := os.Stat(target); err == nil {
		return !info.IsDir()
	}
	return true
}

// ApprovalTargetKey mirrors tools/approval-targets.ts:102-117.
func ApprovalTargetKey(cwd, target string) (string, bool) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return "", false
	}
	if norm, isURL := isInternalOrUpstreamURL(trimmed); isURL {
		return norm, true
	}
	if isReadableURLPath(trimmed) {
		return trimmed, true
	}
	return ApprovalCacheKeyForPath(resolvePathToCwd(target, cwd)), true
}

func coveringFolderKey(target string) (string, bool) {
	if !filepath.IsAbs(target) {
		return "", false
	}
	if strings.HasSuffix(target, "/") || strings.HasSuffix(target, `\`) {
		return target, true
	}
	parent := filepath.Dir(target)
	return withTrailingSep(parent), true
}

// coveringGrantKeys mirrors tools/approval.ts:180-185.
func coveringGrantKeys(target string, capability Tier) []string {
	folder, hasFolder := coveringFolderKey(target)
	subjects := []string{target}
	if hasFolder && folder != target {
		subjects = append(subjects, folder)
	}
	capRank := TierRank(capability)
	allTiers := []Tier{TierRead, TierWrite, TierExec}
	var keys []string
	for _, t := range allTiers {
		if TierRank(t) >= capRank {
			for _, sub := range subjects {
				keys = append(keys, ApprovalGrantKey(t, sub))
			}
		}
	}
	return keys
}

func shortenHomePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	sep := string(filepath.Separator)
	prefix := home
	if !strings.HasSuffix(prefix, sep) {
		prefix += sep
	}
	if strings.HasPrefix(p, prefix) {
		return "~" + p[len(home):]
	}
	return p
}

func isFilesystemRoot(p string) bool {
	cleaned := filepath.Clean(p)
	return filepath.Dir(cleaned) == cleaned
}

func approvalFolderCandidate(targets []string) (key, label string, ok bool) {
	var common string
	for _, target := range targets {
		folder, has := coveringFolderKey(target)
		if !has || (common != "" && folder != common) {
			return "", "", false
		}
		common = folder
	}
	if common == "" {
		return "", "", false
	}
	display := common
	if !isFilesystemRoot(common) {
		display = strings.TrimRight(common, `/\`)
	}
	return common, shortenHomePath(display), true
}

func readApprovalFolderListing(directory string, limit int) *approvalFolderListing {
	f, err := os.Open(directory)
	if err != nil {
		return nil
	}
	defer f.Close()
	entries, err := f.ReadDir(approvalFolderScanLimit + 1)
	if err != nil && !errors.Is(err, io.EOF) && len(entries) == 0 {
		return nil
	}
	truncated := len(entries) > approvalFolderScanLimit
	if truncated {
		entries = entries[:approvalFolderScanLimit]
	}
	scanned := make([]string, 0, len(entries))
	folders := 0
	for _, e := range entries {
		isDir := e.IsDir()
		if isDir {
			folders++
			scanned = append(scanned, e.Name()+"/")
		} else {
			scanned = append(scanned, e.Name())
		}
	}
	sort.Strings(scanned)
	total := len(scanned)
	shown := scanned
	if len(shown) > limit {
		shown = shown[:limit]
	}
	return &approvalFolderListing{
		entries:   shown,
		rest:      total - len(shown),
		folders:   folders,
		files:     total - folders,
		total:     total,
		truncated: truncated,
	}
}

func countWord(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func formatApprovalFolderBlock(folderLabel string, listing *approvalFolderListing, limit int) string {
	if folderLabel == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("Folder: ")
	b.WriteString(folderLabel)
	if listing != nil {
		b.WriteString(" — ")
		if listing.truncated {
			fmt.Fprintf(&b, "%d+ entries", listing.total)
		} else {
			fmt.Fprintf(&b, "%s (%s, %s)",
				countWord(listing.total, "entry", "entries"),
				countWord(listing.folders, "folder", "folders"),
				countWord(listing.files, "file", "files"))
		}
		shown := listing.entries
		if len(shown) > limit {
			shown = shown[:limit]
		}
		for _, e := range shown {
			b.WriteString("\n  - ")
			b.WriteString(e)
		}
		if hidden := listing.total - len(shown); hidden > 0 {
			fmt.Fprintf(&b, "\n  … %d more", hidden)
		}
	}
	return b.String()
}

// approvalGrantChoices mirrors tools/approval.ts:818-841.
func approvalGrantChoices(targets []string, folderKey, folderLabel string, hasFolder bool) []approvalGrantChoice {
	var grants []approvalGrantChoice
	if hasFolder {
		grants = append(grants, approvalGrantChoice{
			scope:       "folder",
			label:       "Approve Directory: " + folderLabel,
			alwaysLabel: "Always Approve Directory: " + folderLabel,
		})
	}
	hasNonFolderTarget := false
	for _, t := range targets {
		if !hasFolder || t != folderKey {
			hasNonFolderTarget = true
			break
		}
	}
	if hasNonFolderTarget {
		allFiles := true
		for _, t := range targets {
			if !approvalTargetNamesFile(t) {
				allFiles = false
				break
			}
		}
		if allFiles {
			grants = append(grants, approvalGrantChoice{
				scope:       "target",
				label:       "Approve File",
				alwaysLabel: "Always Approve File",
			})
		} else {
			grants = append(grants, approvalGrantChoice{
				scope:       "target",
				label:       "Approve",
				alwaysLabel: "Always Approve",
			})
		}
	}
	return grants
}

// ── Per-tool cache target derivation ─────────────────────────────────────────

func normalizePathLike(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	return s
}

func approvalRootCandidates(rawRoot string) []string {
	parts := approvalRootDelimsRE.Split(rawRoot, -1)
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if len(nonEmpty) <= 1 {
		return []string{rawRoot}
	}
	out := make([]string, 0, len(nonEmpty)+1)
	out = append(out, rawRoot)
	out = append(out, nonEmpty...)
	return out
}

func isExclusionEntry(entry, cwd string) bool {
	if !strings.HasPrefix(entry, "!") || len(entry) <= 1 || strings.Contains(entry, "://") {
		return false
	}
	// If literal path starting with `!` exists, it's not an exclusion.
	if _, err := os.Lstat(resolvePathToCwd(entry, cwd)); err == nil {
		return false
	}
	return true
}

func splitGlobBase(entry string) (basePath string, globPattern string) {
	cleaned := strings.TrimSpace(entry)
	if cleaned == "" {
		return ".", ""
	}
	idx := strings.IndexAny(cleaned, globMetaChars)
	if idx < 0 {
		return cleaned, ""
	}
	prefix := cleaned[:idx]
	lastSep := strings.LastIndexAny(prefix, `/\`)
	if lastSep < 0 {
		return ".", cleaned
	}
	base := prefix[:lastSep]
	if base == "" {
		base = "/"
	}
	return base, cleaned[lastSep+1:]
}

func splitReadSelector(rawPath, cwd string) (selectorless, sel string) {
	// If literal path exists, do not peel selector (`read.ts:1281`).
	if _, err := os.Lstat(resolvePathToCwd(rawPath, cwd)); err == nil {
		return rawPath, ""
	}
	// Guard Windows drive prefix `C:\...` or `C:/...`
	minColon := 0
	if len(rawPath) >= 3 && ((rawPath[0] >= 'a' && rawPath[0] <= 'z') || (rawPath[0] >= 'A' && rawPath[0] <= 'Z')) &&
		rawPath[1] == ':' && (rawPath[2] == '\\' || rawPath[2] == '/') {
		minColon = 2
	}
	lastColon := strings.LastIndexByte(rawPath, ':')
	if lastColon < minColon {
		return rawPath, ""
	}
	suffix := rawPath[lastColon+1:]
	prefix := rawPath[:lastColon]
	if prevColon := strings.LastIndexByte(prefix, ':'); prevColon >= minColon {
		mid := prefix[prevColon+1:]
		if strings.EqualFold(mid, "raw") || strings.EqualFold(suffix, "raw") {
			if isReadLineSelector(mid) && isReadLineSelector(suffix) {
				return prefix[:prevColon], mid + ":" + suffix
			}
		}
	}
	if isReadLineSelector(suffix) || strings.EqualFold(suffix, "conflicts") {
		return prefix, suffix
	}
	return rawPath, ""
}

func searchRootApprovalKey(root, cwd string, recursive bool) (string, bool) {
	if root == "" {
		return "", false
	}
	if isReadableURLPath(root) {
		if isApprovalPublicURL(root) {
			return "", false
		}
		return ApprovalTargetKey(cwd, root)
	}
	if _, isURL := isInternalOrUpstreamURL(root); isURL {
		return ApprovalTargetKey(cwd, root)
	}
	abs := resolvePathToCwd(root, cwd)
	info, err := os.Stat(abs)
	if err != nil {
		return "", false
	}
	if !info.IsDir() {
		return ApprovalTargetKey(cwd, root)
	}
	dirKey := ApprovalDirectoryKey(abs)
	if recursive {
		return ApprovalTreeSearchKey(dirKey), true
	}
	return dirKey, true
}

func searchApprovalCacheTargets(
	roots []string,
	cwd string,
	splitBase func(string) string,
	isRecursive func(string) bool,
	skipCandidate func(candidate string, wholeList bool) bool,
) []string {
	effective := roots
	if len(effective) == 0 {
		effective = []string{"."}
	}
	seen := map[string]bool{}
	var keys []string
	for _, rawRoot := range effective {
		root := normalizePathLike(rawRoot)
		if root == "" {
			continue
		}
		candidates := approvalRootCandidates(root)
		for idx, cand := range candidates {
			if skipCandidate != nil && skipCandidate(cand, len(candidates) > 1 && idx == 0) {
				continue
			}
			base := normalizePathLike(splitBase(cand))
			rec := true
			if isRecursive != nil {
				rec = isRecursive(cand)
			}
			if key, ok := searchRootApprovalKey(base, cwd, rec); ok && !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	return keys
}

// toolApprovalCacheTargets computes omp's `tool.approvalCacheTargets(args)` for
// the tools that declare it: `read`, `write`, `edit`, `glob`, `grep`, `find`,
// `ast_grep` (and `ast_edit` restriction paths).
// upstream: read.ts:1274, write.ts:563, edit/index.ts:484, glob.ts:169,
//
//	grep.ts:572, jfind/index.ts:76, ast-grep.ts:148
func toolApprovalCacheTargets(toolName string, input map[string]any, cwd string) []string {
	switch toolName {
	case "read":
		rawPath, _ := input["path"].(string)
		if rawPath == "" {
			return nil
		}
		withoutQ := rawPath
		if idx := strings.Index(withoutQ, "?q="); idx >= 0 {
			withoutQ = withoutQ[:idx]
		}
		var selectorless, sel string
		if _, isURL := isInternalOrUpstreamURL(withoutQ); isURL {
			selectorless, sel = urls.Split(withoutQ)
		} else {
			selectorless, sel = splitReadSelector(withoutQ, cwd)
		}
		isPdfMember := sel == "" && pdfImageMemberRE.MatchString(withoutQ)
		if isReadableURLPath(selectorless) && !isPdfMember {
			if isApprovalPublicURL(selectorless) {
				return nil
			}
		}
		key, ok := ApprovalTargetKey(cwd, selectorless)
		if !ok {
			return nil
		}
		if filepath.IsAbs(key) && !approvalTargetNamesFile(key) {
			return []string{ApprovalDirectoryKey(key)}
		}
		return []string{key}

	case "write":
		rawPath, _ := input["path"].(string)
		if rawPath == "" {
			return nil
		}
		key, ok := ApprovalTargetKey(cwd, hashline.UnwrapHeaderPath(rawPath))
		if !ok {
			return nil
		}
		return []string{key}

	case "edit":
		raw, _ := input["input"].(string)
		if strings.TrimSpace(raw) == "" {
			return nil
		}
		paths, moves := extractEditPathsAndMoves(raw)
		targets := append(append([]string(nil), paths...), moves...)
		var keys []string
		for _, t := range targets {
			if key, ok := ApprovalTargetKey(cwd, t); ok {
				keys = append(keys, key)
			}
		}
		return keys

	case "glob":
		raw := globSearchPath(input)
		roots := extractStringPathList(raw)
		return searchApprovalCacheTargets(
			roots,
			cwd,
			func(e string) string { base, _ := splitGlobBase(e); return base },
			func(e string) bool {
				_, pat := splitGlobBase(e)
				return pat == "" || strings.Contains(pat, "**")
			},
			nil,
		)

	case "grep":
		roots := extractPathArgList(input)
		hasExcl := false
		for _, r := range roots {
			norm := normalizePathLike(r)
			if isExclusionEntry(norm, cwd) {
				hasExcl = true
				break
			}
			for _, part := range approvalRootDelimsRE.Split(norm, -1) {
				if part != "" && isExclusionEntry(part, cwd) {
					hasExcl = true
					break
				}
			}
			if hasExcl {
				break
			}
		}
		if hasExcl {
			roots = append(append([]string(nil), roots...), ".")
		}
		return searchApprovalCacheTargets(
			roots,
			cwd,
			func(e string) string {
				p, _ := splitReadSelector(e, cwd)
				base, _ := splitGlobBase(p)
				return base
			},
			nil,
			func(cand string, wholeList bool) bool {
				if wholeList {
					parts := approvalRootCandidates(cand)
					hasPartExcl := false
					for _, p := range parts[1:] {
						if isExclusionEntry(p, cwd) {
							hasPartExcl = true
							break
						}
					}
					if !hasPartExcl {
						return false
					}
					p, _ := splitReadSelector(cand, cwd)
					_, err := os.Lstat(resolvePathToCwd(p, cwd))
					return os.IsNotExist(err)
				}
				return isExclusionEntry(cand, cwd)
			},
		)

	case "find":
		var roots []string
		if p, ok := input["path"].(string); ok && p != "" {
			roots = []string{p}
		}
		return searchApprovalCacheTargets(roots, cwd, func(e string) string { return e }, nil, nil)

	case "ast_grep":
		roots := extractPathArgList(input)
		return searchApprovalCacheTargets(
			roots,
			cwd,
			func(e string) string { base, _ := splitGlobBase(e); return base },
			nil,
			nil,
		)
	}
	return nil
}

func globSearchPath(input map[string]any) any {
	if input == nil {
		return nil
	}
	if p, ok := input["path"]; ok && p != nil {
		return p
	}
	if p, ok := input["paths"]; ok && p != nil {
		return p
	}
	return nil
}

// ── Restricted folders (tools.restrictedFolders) ─────────────────────────────

// isWithinFolder reports whether candidate is root or lies below it.
// Both paths are cache-normalized (symlinks resolved, Windows case-folded).
// upstream: packages/coding-agent/src/tools/approval.ts:495-500
func isWithinFolder(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if rel == "." || rel == "" {
		return true
	}
	sep := string(filepath.Separator)
	return !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+sep)
}

// restrictionSubjects ports omp's `restrictionSubjects` (`tools/approval.ts:509-542`).
func restrictionSubjects(toolName string, input map[string]any, cwd string) []restrictionSubject {
	// ast_edit names paths[] directly (does not declare approvalCacheTargets in omp,
	// but touches those workspace paths; include them alongside cwd fallback).
	targets := toolApprovalCacheTargets(toolName, input, cwd)
	if len(targets) > 0 {
		var subjects []restrictionSubject
		for _, target := range targets {
			treeFolder, isTree := ApprovalTreeSearchFolderKey(target)
			sub := target
			if isTree {
				sub = treeFolder
			}
			if filepath.IsAbs(sub) {
				subjects = append(subjects, restrictionSubject{
					path:      ApprovalCacheKeyForPath(sub),
					recursive: isTree,
				})
			}
		}
		return subjects
	}
	var subjects []restrictionSubject
	if cwd == "" {
		return subjects
	}
	workingDir := cwd
	if input != nil {
		if reqCwd, ok := input["cwd"].(string); ok && strings.TrimSpace(reqCwd) != "" {
			trimmed := strings.TrimSpace(reqCwd)
			if !urls.CanHandle(trimmed) {
				workingDir = resolvePathToCwd(trimmed, cwd)
			}
		}
	}
	subjects = append(subjects, restrictionSubject{
		path:      ApprovalCacheKeyForPath(workingDir),
		recursive: false,
	})
	return subjects
}

// RestrictedFolderReason ports omp's `restrictedFolderReason`
// (`tools/approval.ts:551-574`): returns `Restricted folder: <label>` when any
// subject touched by the call lies inside (or recursively searches over) any
// configured restricted folder, or "" otherwise.
func RestrictedFolderReason(toolName string, input map[string]any, restrictions *ApprovalRestrictions) string {
	if restrictions == nil || len(restrictions.RestrictedFolders) == 0 {
		return ""
	}
	base := restrictions.Cwd
	if base == "" {
		if wd, err := os.Getwd(); err == nil {
			base = wd
		}
	}
	type folderEntry struct {
		key   string
		label string
	}
	var folders []folderEntry
	for _, entry := range restrictions.RestrictedFolders {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		expanded := expandTildePath(trimmed)
		var label string
		if filepath.IsAbs(expanded) {
			label = filepath.Clean(expanded)
		} else {
			label = filepath.Join(base, expanded)
		}
		folders = append(folders, folderEntry{
			key:   ApprovalCacheKeyForPath(label),
			label: label,
		})
	}
	if len(folders) == 0 {
		return ""
	}
	for _, sub := range restrictionSubjects(toolName, input, restrictions.Cwd) {
		for _, f := range folders {
			if isWithinFolder(f.key, sub.path) || (sub.recursive && isWithinFolder(sub.path, f.key)) {
				return "Restricted folder: " + shortenHomePath(f.label)
			}
		}
	}
	return ""
}

// HeadlessRestrictionError formats omp's exact fail-closed refusal for a
// restricted folder when no interactive UI is available.
// upstream: packages/coding-agent/src/extensibility/extensions/wrapper.ts:477-482
func HeadlessRestrictionError(toolName, reason string) string {
	return fmt.Sprintf(
		"Tool %q requires approval but no interactive UI available.\n"+
			"Reason: %s\n"+
			"A restricted folder (tools.restrictedFolders) asks in every approval mode.",
		toolName, reason,
	)
}
