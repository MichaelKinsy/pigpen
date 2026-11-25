package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/search"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// SuffixResolution records a unique workspace-suffix fallback match on a missing path.
// upstream: packages/tui/src/tools/read.ts:65
type SuffixResolution struct {
	From string
	To   string
	Raw  bool
}

func (s *SuffixResolution) toMap() map[string]any {
	if s == nil {
		return nil
	}
	m := map[string]any{
		"from": s.From,
		"to":   s.To,
	}
	if s.Raw {
		m["raw"] = true
	}
	return m
}

// markRawSuffixResolution marks a suffix resolution as raw so prependSuffixResolutionNotice
// keeps the diagnostic out of raw byte output while retaining it in details.
// upstream: packages/coding-agent/src/tools/read-format.ts:56-58
func markRawSuffixResolution(res *SuffixResolution, raw bool) {
	if res != nil && raw {
		res.Raw = true
	}
}

// prependSuffixResolutionNotice prepends the suffix-resolution banner unless raw mode is active.
// upstream: packages/coding-agent/src/tools/read-format.ts:719-727
func prependSuffixResolutionNotice(text string, res *SuffixResolution) string {
	if res == nil || res.Raw {
		return text
	}
	notice := fmt.Sprintf("[Path '%s' not found; resolved to '%s' via suffix match]", res.From, res.To)
	if text == "" {
		return notice
	}
	return notice + "\n" + text
}

// suffixMatch is a unique workspace entry matching a missing path's trailing segments.
type suffixMatch struct {
	AbsolutePath string
	DisplayPath  string
}

// suffixMatchCache memoizes findUniqueWorkspaceSuffix within a single read call.
// A nil value records a confirmed miss.
// upstream: packages/coding-agent/src/tools/read-path-resolution.ts:17-36
type suffixMatchCache map[string]*suffixMatch

const workspaceSuffixTimeout = 5 * time.Second

// isRemoteMountPath reports whether absPath lies under omp's sshfs remote mount directory.
// upstream: packages/coding-agent/src/tools/read-path-resolution.ts:7-10
func isRemoteMountPath(absPath string) bool {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		prefix := filepath.Join(home, ".omp", "remote") + string(filepath.Separator)
		if strings.HasPrefix(absPath, prefix) {
			return true
		}
	}
	tmpPrefix := filepath.Join(os.TempDir(), "omp-remote") + string(filepath.Separator)
	return strings.HasPrefix(absPath, tmpPrefix)
}

// isNotFoundError reports ENOENT or ENOTDIR.
// upstream: packages/coding-agent/src/tools/read-path-resolution.ts:11-15
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ENOTDIR)
}

func isNameTooLongError(err error) bool {
	return errors.Is(err, syscall.ENAMETOOLONG)
}

// findSuffixMatchCached memoizes findUniqueWorkspaceSuffix for one read call.
// upstream: packages/coding-agent/src/tools/read-path-resolution.ts:25-36
func findSuffixMatchCached(ctx context.Context, cache suffixMatchCache, rawPath, cwd string) (*suffixMatch, error) {
	if cache != nil {
		if hit, ok := cache[rawPath]; ok {
			return hit, nil
		}
	}
	res, err := findUniqueWorkspaceSuffix(ctx, rawPath, cwd)
	if err != nil {
		return nil, err
	}
	if cache != nil {
		cache[rawPath] = res
	}
	return res, nil
}

// findUniqueWorkspaceSuffix searches cwd for a unique entry (file, directory, or symlink)
// whose workspace-relative path ends with the trailing path segments of rawPath.
// Mirrors omp's `glob({ pattern: "**/" + escapeGlobMetachars(normalized), path: cwd, hidden: true, timeoutMs: 5000 })`.
// upstream: packages/coding-agent/src/tools/path-utils.ts:1413-1458
func findUniqueWorkspaceSuffix(ctx context.Context, rawPath, cwd string) (*suffixMatch, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	walkCtx, cancel := context.WithTimeout(ctx, workspaceSuffixTimeout)
	defer cancel()

	matches, err := search.FindSuffixMatches(walkCtx, cwd, rawPath)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		// Timeout, invalid glob (e.g. unopened `}`), or scan error returns nil,
		// matching path-utils.ts:1436-1439.
		return nil, nil
	}
	if len(matches) != 1 {
		return nil, nil
	}
	rel := matches[0]
	abs := filepath.Join(cwd, filepath.FromSlash(rel))
	if cleaned, absErr := filepath.Abs(abs); absErr == nil {
		abs = cleaned
	}
	return &suffixMatch{
		AbsolutePath: abs,
		DisplayPath:  rel,
	}, nil
}

// ---------------------------------------------------------------------------
// Delimited multi-path splitting and list execution
// upstream: packages/coding-agent/src/tools/path-utils.ts:588-889
// upstream: packages/coding-agent/src/tools/read.ts:923-1060,1488-1567
// ---------------------------------------------------------------------------

// normalizePathLikeInput trims whitespace and matching outer double quotes.
// upstream: packages/coding-agent/src/tools/path-utils.ts:588-590
func normalizePathLikeInput(input string) string {
	s := strings.TrimSpace(input)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	return strings.TrimSpace(s)
}

func hasGlobPathChars(p string) bool {
	return strings.ContainsAny(p, "*?[{")
}

// probeLiteralPathExists performs a three-way Lstat probe ("exists", "missing", "unknown").
// upstream: packages/coding-agent/src/tools/path-utils.ts:234-244
func probeLiteralPathExists(filePath, cwd string) string {
	resolved := resolveToCwd(filePath, cwd)
	_, err := os.Lstat(resolved)
	if err == nil {
		return "exists"
	}
	if isNotFoundError(err) || isNameTooLongError(err) {
		return "missing"
	}
	return "unknown"
}

// isInternalURLPath reports whether s looks like an internal/registered URL scheme
// (not http/https/file).
func isInternalURLPath(s string) bool {
	trimmed := normalizeAtPrefix(strings.TrimSpace(s))
	return urls.CanHandle(trimmed)
}

type delimitedSplitMode int

const (
	splitModeComma delimitedSplitMode = iota
	splitModeSemicolon
	splitModeWhitespace
	splitModeMixed
)

type delimitedResolveReq int

const (
	resolveReqNone delimitedResolveReq = iota
	resolveReqSome
	resolveReqAll
)

func isDelimitedSep(r rune, mode delimitedSplitMode) bool {
	switch mode {
	case splitModeComma:
		return r == ','
	case splitModeSemicolon:
		return r == ';'
	case splitModeWhitespace:
		return unicode.IsSpace(r)
	default:
		return r == ',' || r == ';' || unicode.IsSpace(r)
	}
}

// hasTopLevelPathDelimiter reports whether entry contains an unescaped `,`, `;`,
// or whitespace outside `{...}` braces.
// upstream: packages/coding-agent/src/tools/path-utils.ts:611-632
func hasTopLevelPathDelimiter(entry string) bool {
	braceDepth := 0
	runes := []rune(entry)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if ch == '\\' && i+1 < len(runes) {
			i++
			continue
		}
		if ch == '{' {
			braceDepth++
			continue
		}
		if ch == '}' {
			if braceDepth > 0 {
				braceDepth--
			}
			continue
		}
		if braceDepth == 0 && (ch == ',' || ch == ';' || unicode.IsSpace(ch)) {
			return true
		}
	}
	return false
}

// splitTopLevelDelimitedPath splits entry on unescaped delimiters outside `{...}` braces.
// upstream: packages/coding-agent/src/tools/path-utils.ts:634-658
func splitTopLevelDelimitedPath(entry string, mode delimitedSplitMode) []string {
	var parts []string
	braceDepth := 0
	runes := []rune(entry)
	start := 0
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if ch == '\\' && i+1 < len(runes) {
			i++
			continue
		}
		if ch == '{' {
			braceDepth++
			continue
		}
		if ch == '}' {
			if braceDepth > 0 {
				braceDepth--
			}
			continue
		}
		if braceDepth != 0 || !isDelimitedSep(ch, mode) {
			continue
		}
		parts = append(parts, string(runes[start:i]))
		start = i + 1
	}
	parts = append(parts, string(runes[start:]))
	return parts
}

// parseSearchBasePath mirrors path-utils.ts:979-998 for delimitedPathPartResolves.
func parseSearchBasePath(filePath string) string {
	norm := strings.ReplaceAll(filePath, "\\", "/")
	segs := strings.Split(norm, "/")
	firstGlob := -1
	for i, seg := range segs {
		if hasGlobPathChars(seg) {
			firstGlob = i
			break
		}
	}
	if firstGlob == -1 {
		return norm
	}
	if firstGlob == 0 {
		return "."
	}
	return strings.Join(segs[:firstGlob], "/")
}

// delimitedPathPartResolves reports whether a single candidate part resolves on disk
// (or is an internal URL).
// upstream: packages/coding-agent/src/tools/path-utils.ts:660-675
func delimitedPathPartResolves(entry, cwd string) (bool, error) {
	if isInternalURLPath(entry) {
		return true, nil
	}
	peeled, _ := SplitPathAndSel(entry)
	basePath := parseSearchBasePath(peeled)
	absBase := resolveToCwd(basePath, cwd)
	_, err := os.Stat(absBase)
	if err == nil {
		return true, nil
	}
	if isNotFoundError(err) || isNameTooLongError(err) {
		return false, nil
	}
	return false, err
}

// tryDelimitedPathSplit attempts to split entry under mode and resolve requirement.
// upstream: packages/coding-agent/src/tools/path-utils.ts:686-706
func tryDelimitedPathSplit(entry, cwd string, mode delimitedSplitMode, req delimitedResolveReq) ([]string, error) {
	rawParts := splitTopLevelDelimitedPath(entry, mode)
	if len(rawParts) < 2 {
		return nil, nil
	}
	parts := make([]string, 0, len(rawParts))
	for _, rp := range rawParts {
		if np := normalizePathLikeInput(rp); np != "" {
			parts = append(parts, np)
		}
	}
	if len(parts) == 0 {
		return nil, nil
	}
	if len(parts) < 2 && len(rawParts) == len(parts) {
		return nil, nil
	}

	if req != resolveReqNone {
		anyResolved := false
		allResolved := true
		for _, part := range parts {
			ok, err := delimitedPathPartResolves(part, cwd)
			if err != nil {
				return nil, err
			}
			if ok {
				anyResolved = true
			} else {
				allResolved = false
			}
		}
		if req == resolveReqAll && !allResolved {
			return nil, nil
		}
		if req == resolveReqSome && !anyResolved {
			return nil, nil
		}
	}
	return parts, nil
}

// splitDelimitedPathEntry splits a single path-like entry whose multiple targets were
// flattened into one string while keeping literal files intact.
// upstream: packages/coding-agent/src/tools/path-utils.ts:756-836
func splitDelimitedPathEntry(entry, cwd string, routedURLPredicate func(string) bool) ([]string, error) {
	normalized := normalizePathLikeInput(entry)
	if !hasTopLevelPathDelimiter(normalized) {
		return nil, nil
	}
	if routedURLPredicate != nil && routedURLPredicate(normalized) {
		parts, err := tryDelimitedPathSplit(normalized, cwd, splitModeSemicolon, resolveReqNone)
		if err != nil || parts == nil {
			return nil, err
		}
		for _, p := range parts {
			if !routedURLPredicate(p) {
				return nil, nil
			}
		}
		return parts, nil
	}
	if isInternalURLPath(normalized) {
		parts, err := tryDelimitedPathSplit(normalized, cwd, splitModeSemicolon, resolveReqNone)
		if err != nil || len(parts) <= 1 {
			return nil, err
		}
		for _, p := range parts {
			if !isInternalURLPath(p) {
				return nil, nil
			}
		}
		return parts, nil
	}
	if probeLiteralPathExists(normalized, cwd) != "missing" {
		return nil, nil
	}
	peeledEntry, sel := SplitPathAndSel(normalized)
	if sel != "" && probeLiteralPathExists(peeledEntry, cwd) != "missing" {
		return nil, nil
	}
	if !hasGlobPathChars(peeledEntry) {
		ok, err := delimitedPathPartResolves(normalized, cwd)
		if err != nil {
			return nil, err
		}
		if ok {
			return nil, nil
		}
	}

	if parts, err := tryDelimitedPathSplit(normalized, cwd, splitModeSemicolon, resolveReqNone); err != nil || parts != nil {
		return parts, err
	}
	if parts, err := tryDelimitedPathSplit(normalized, cwd, splitModeComma, resolveReqSome); err != nil || parts != nil {
		return parts, err
	}
	if parts, err := tryDelimitedPathSplit(normalized, cwd, splitModeWhitespace, resolveReqAll); err != nil || parts != nil {
		return parts, err
	}
	return tryDelimitedPathSplit(normalized, cwd, splitModeMixed, resolveReqAll)
}

// splitMixedURLPathList splits a `;` list mixing URLs with existing local paths
// before URL detection claims the whole string.
// upstream: packages/coding-agent/src/tools/path-utils.ts:847-869
func splitMixedURLPathList(entry, cwd string, isURL func(string) bool) ([]string, error) {
	normalized := normalizePathLikeInput(entry)
	if !strings.Contains(normalized, ";") || !isURL(normalized) {
		return nil, nil
	}
	if !isInternalURLPath(normalized) {
		if probeLiteralPathExists(normalized, cwd) != "missing" {
			return nil, nil
		}
		peeled, sel := SplitPathAndSel(normalized)
		if sel != "" && probeLiteralPathExists(peeled, cwd) != "missing" {
			return nil, nil
		}
	}
	parts, err := tryDelimitedPathSplit(normalized, cwd, splitModeSemicolon, resolveReqNone)
	if err != nil || parts == nil {
		return nil, err
	}
	for _, part := range parts {
		if isURL(part) {
			continue
		}
		partPath, _ := SplitPathAndSel(part)
		if hasGlobPathChars(partPath) || probeLiteralPathExists(partPath, cwd) != "exists" {
			return nil, nil
		}
	}
	return parts, nil
}

// splitTopLevelSemicolons splits text on `;` outside quotes and ()/[]/{} nesting.
// upstream: packages/coding-agent/src/tools/read.ts:924-944
func splitTopLevelSemicolons(text string) []string {
	var parts []string
	depth := 0
	quoted := false
	start := 0
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if quoted {
			if ch == '\\' {
				i++
			} else if ch == '"' {
				quoted = false
			}
		} else if ch == '"' {
			quoted = true
		} else if ch == '(' || ch == '[' || ch == '{' {
			depth++
		} else if (ch == ')' || ch == ']' || ch == '}') && depth > 0 {
			depth--
		} else if ch == ';' && depth == 0 {
			if p := strings.TrimSpace(text[start:i]); p != "" {
				parts = append(parts, p)
			}
			start = i + 1
		}
	}
	if p := strings.TrimSpace(text[start:]); p != "" {
		parts = append(parts, p)
	}
	return parts
}

// splitQueryPathList splits a `;` list whose parts carry `?q=` queries before query
// routing claims the rest of the list as one filter.
// upstream: packages/coding-agent/src/tools/read.ts:950-963
func splitQueryPathList(readPath, cwd string) []string {
	if !strings.Contains(readPath, "?") || !strings.Contains(readPath, ";") {
		return nil
	}
	if probeLiteralPathExists(readPath, cwd) != "missing" {
		return nil
	}
	parts := splitTopLevelSemicolons(readPath)
	if len(parts) < 2 {
		return nil
	}
	for _, part := range parts {
		if isInternalURLPath(part) {
			continue
		}
		base := part
		if q := strings.IndexByte(part, '?'); q >= 0 {
			base = part[:q]
		}
		filePath, _ := SplitPathAndSel(base)
		if probeLiteralPathExists(filePath, cwd) != "exists" {
			return nil
		}
	}
	return parts
}

// readDetailsLinkPath resolves the filesystem link path for a read result, or nil
// for URL/internal sources that are not filesystem paths.
// upstream: packages/coding-agent/src/tools/read.ts:1056-1060
func readDetailsLinkPath(details map[string]any) any {
	if details == nil {
		return nil
	}
	if rp, ok := details["resolvedPath"].(string); ok && rp != "" {
		return rp
	}
	if meta, ok := details["meta"].(map[string]any); ok {
		if src, ok := meta["source"].(map[string]any); ok {
			if t, _ := src["type"].(string); t == "path" {
				if v, ok := src["value"].(string); ok && v != "" {
					return v
				}
			}
		}
	}
	return nil
}

// tryReadDelimitedPaths attempts to split readPath into multiple targets and read each.
// upstream: packages/coding-agent/src/tools/read.ts:1488-1496
func tryReadDelimitedPaths(
	ctx sdk.Context,
	store *hashline.Store,
	cwd, readPath string,
	routedURLPredicate func(string) bool,
) (sdk.ToolResult, bool, error) {
	parts, err := splitDelimitedPathEntry(readPath, cwd, routedURLPredicate)
	if err != nil {
		return sdk.ToolResult{}, false, err
	}
	if len(parts) == 0 {
		return sdk.ToolResult{}, false, nil
	}
	res, err := readDelimitedParts(ctx, store, cwd, parts)
	return res, true, err
}

// readDelimitedParts reads each part in order and joins their text, images, notes,
// displayReadTargets, and displayReadTargetLinks into one ToolResult.
// upstream: packages/coding-agent/src/tools/read.ts:990-1048,1502-1567
func readDelimitedParts(
	ctx sdk.Context,
	store *hashline.Store,
	cwd string,
	parts []string,
) (sdk.ToolResult, error) {
	notice := fmt.Sprintf("Note: interpreted as %d paths: %s", len(parts), strings.Join(parts, ", "))
	notes := []string{notice}
	displayTargets := make([]string, 0, len(parts))
	displayLinks := make([]any, 0, len(parts))

	var textParts []string
	var images []sdk.ImageContent
	textParts = append(textParts, notice)

	sig := safeRunSignal(ctx)

	for _, part := range parts {
		if sig.Err() != nil {
			return sdk.ToolResult{}, sig.Err()
		}
		res, err := executeReadInner(ctx, store, cwd, part)
		if err != nil {
			if sig.Err() != nil {
				return sdk.ToolResult{}, sig.Err()
			}
			errorNote := fmt.Sprintf("Could not read %s: %s", part, err.Error())
			notes = append(notes, errorNote)
			displayTargets = append(displayTargets, part)
			displayLinks = append(displayLinks, nil)
			textParts = append(textParts, "["+errorNote+"]")
			continue
		}

		if res.Content != "" {
			textParts = append(textParts, res.Content)
		}
		if len(res.Images) > 0 {
			images = append(images, res.Images...)
		}

		d, _ := res.Details.(map[string]any)
		if nestedTargets, ok := d["displayReadTargets"].([]string); ok && len(nestedTargets) > 0 {
			nestedLinks, _ := d["displayReadTargetLinks"].([]any)
			for i, nt := range nestedTargets {
				displayTargets = append(displayTargets, nt)
				if i < len(nestedLinks) {
					displayLinks = append(displayLinks, nestedLinks[i])
				} else {
					displayLinks = append(displayLinks, nil)
				}
			}
		} else {
			target := part
			if sr, ok := d["suffixResolution"].(map[string]any); ok {
				if to, ok := sr["to"].(string); ok && to != "" {
					target = to
				}
			}
			displayTargets = append(displayTargets, target)
			displayLinks = append(displayLinks, readDetailsLinkPath(d))
		}
	}

	details := map[string]any{
		"notes":                  notes,
		"displayReadTargets":     displayTargets,
		"displayReadTargetLinks": displayLinks,
	}
	return sdk.ToolResult{
		Content: strings.Join(textParts, "\n\n"),
		Images:  images,
		Details: details,
	}, nil
}

// isReadableTargetURL reports whether part is an external http(s) URL or a registered
// internal URL, matching read.ts:2146.
func isReadableTargetURL(part string) bool {
	if isInternalURLPath(part) {
		return true
	}
	if u, err := parseReadURLTarget(part); err == nil && u != nil {
		return true
	}
	return false
}

// preferLiteralSplit mirrors splitPathAndSelPreferringLiteral using Lstat three-way probe.
// upstream: packages/coding-agent/src/tools/path-utils.ts:256-268
func preferLiteralSplit(rawPath, cwd string) (basePath, sel string) {
	strictBase, strictSel := SplitPathAndSel(rawPath)
	if strictSel == "" {
		return strictBase, ""
	}
	probe := probeLiteralPathExists(rawPath, cwd)
	if probe == "exists" || (probe == "unknown" && runtime.GOOS != "windows") {
		return rawPath, ""
	}
	return strictBase, strictSel
}
