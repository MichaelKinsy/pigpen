package ast

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// Path resolution, globbing, and gitignore-aware file discovery for ast_grep and ast_edit.
// upstream: packages/coding-agent/src/tools/path-utils.ts, crates/ast/src/search/mod.rs,
//           crates/xfs/src/ignore.rs

type resolvedTarget struct {
	raw          string
	basePath     string // absolute host filesystem path
	urlPrefix    string // non-empty when derived from an internal URL (e.g. "local://dir")
	globPattern  string // optional glob relative to basePath
	hasGlob      bool
	isDirectory  bool
	isURL        bool
	urlImmutable bool
	recursive    bool
}

type scopeResolution struct {
	searchPath   string
	scopePath    string
	isDirectory  bool
	targets      []resolvedTarget
	missingPaths []string
}

type candidateFile struct {
	absPath     string
	displayPath string // cwd-relative host path, absolute if outside cwd, or full internal URL
	lang        string // inferred or explicit canonical language
	isURL       bool
	immutable   bool
}

var urlLikePathRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)

func isURLLikePath(p string) bool {
	return urlLikePathRe.MatchString(p)
}

var externalURLRe = regexp.MustCompile(`^(?i)(?:https?|ftp|ws|wss)://`)

func hasGlobChars(s string) bool {
	return strings.ContainsAny(s, "*?[{")
}

// toPathList normalizes a path input (single string, JSON string array).
// upstream: packages/tui/src/render/render-utils.ts (toPathList)
func toPathList(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		var arr []string
		if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
			out := make([]string, 0, len(arr))
			out = append(out, arr...)
			return out
		}
	}
	return []string{trimmed}
}

func normalizePathLikeInput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

type delimMode int

const (
	delimSemicolon delimMode = iota
	delimComma
	delimWhitespace
	delimMixed
)

func isDelimChar(ch byte, mode delimMode) bool {
	switch mode {
	case delimSemicolon:
		return ch == ';'
	case delimComma:
		return ch == ','
	case delimWhitespace:
		return unicode.IsSpace(rune(ch))
	default:
		return ch == ';' || ch == ',' || unicode.IsSpace(rune(ch))
	}
}

// splitTopLevelDelimited splits entry on delimiters outside `{...}` braces.
// upstream: packages/coding-agent/src/tools/path-utils.ts:634-658
func splitTopLevelDelimited(entry string, mode delimMode) []string {
	var parts []string
	braceDepth := 0
	start := 0
	for i := 0; i < len(entry); i++ {
		ch := entry[i]
		if ch == '\\' && i+1 < len(entry) {
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
		if braceDepth == 0 && isDelimChar(ch, mode) {
			parts = append(parts, entry[start:i])
			start = i + 1
		}
	}
	parts = append(parts, entry[start:])
	return parts
}

func partResolves(part, cwd string) bool {
	expanded := expandPath(part)
	if urls.CanHandle(expanded) {
		return true
	}
	base, _, _, _ := splitGlobFromPath(part, cwd)
	_, err := os.Stat(base)
	return err == nil
}

// tryDelimitedSplit attempts to split entry according to omp's delimiter policy:
// semicolon = "none" (always split), comma = "some" (at least one part exists),
// whitespace/mixed = "all" (every part exists).
// upstream: packages/coding-agent/src/tools/path-utils.ts:686-706, 830-835
func tryDelimitedSplit(entry, cwd string, mode delimMode, req string) []string {
	rawParts := splitTopLevelDelimited(entry, mode)
	if len(rawParts) < 2 {
		return nil
	}
	parts := make([]string, 0, len(rawParts))
	for _, p := range rawParts {
		np := normalizePathLikeInput(p)
		if np != "" {
			parts = append(parts, np)
		}
	}
	if len(parts) == 0 || (len(parts) < 2 && len(rawParts) == len(parts)) {
		return nil
	}
	switch req {
	case "some":
		anyOK := false
		for _, p := range parts {
			if partResolves(p, cwd) {
				anyOK = true
				break
			}
		}
		if !anyOK {
			return nil
		}
	case "all":
		for _, p := range parts {
			if !partResolves(p, cwd) {
				return nil
			}
		}
	}
	return parts
}

func expandDelimitedEntries(inputs []string, cwd string) ([]string, error) {
	var out []string
	for _, raw := range inputs {
		entry := normalizePathLikeInput(raw)
		if entry == "" {
			return nil, fmt.Errorf("Search scope entries must be non-empty paths or globs")
		}
		// Keep literal existing paths intact even if they contain delimiters.
		if !isURLLikePath(entry) {
			if _, err := os.Stat(resolveToCwd(entry, cwd)); err == nil {
				out = append(out, entry)
				continue
			}
		}
		if parts := tryDelimitedSplit(entry, cwd, delimSemicolon, "none"); parts != nil {
			out = append(out, parts...)
			continue
		}
		if !isURLLikePath(entry) {
			if parts := tryDelimitedSplit(entry, cwd, delimComma, "some"); parts != nil {
				out = append(out, parts...)
				continue
			}
			if parts := tryDelimitedSplit(entry, cwd, delimWhitespace, "all"); parts != nil {
				out = append(out, parts...)
				continue
			}
			if parts := tryDelimitedSplit(entry, cwd, delimMixed, "all"); parts != nil {
				out = append(out, parts...)
				continue
			}
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Search scope entries must be non-empty paths or globs")
	}
	return out, nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func expandPath(raw string) string {
	p := raw
	if strings.HasPrefix(p, "@") {
		rest := p[1:]
		if strings.HasPrefix(rest, "/") || rest == "~" || strings.HasPrefix(rest, "~/") || urls.CanHandle(rest) {
			p = rest
		}
	}
	if strings.HasPrefix(strings.ToLower(p), "file://") {
		p = p[len("file://"):]
	}
	return expandHome(p)
}

func resolveToCwd(filePath, cwd string) string {
	expanded := expandPath(filePath)
	trimmedSlash := strings.Trim(expanded, "/")
	if expanded != "" && trimmedSlash == "" {
		// Bare "/" aliases workspace root, matching omp path-utils.ts:340.
		return cwd
	}
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded)
	}
	return filepath.Clean(filepath.Join(cwd, expanded))
}

func formatPathRelativeToCwd(absPath, cwd string) string {
	if isURLLikePath(absPath) {
		return absPath
	}
	cleanCwd := filepath.Clean(cwd)
	cleanAbs := filepath.Clean(absPath)
	if cleanAbs == cleanCwd {
		return "."
	}
	rel, err := filepath.Rel(cleanCwd, cleanAbs)
	if err == nil && rel != "" && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	// Resolve symlinks on cwd and target for macOS /var <-> /private/var consistency.
	if realCwd, err1 := filepath.EvalSymlinks(cleanCwd); err1 == nil {
		if realAbs, err2 := filepath.EvalSymlinks(cleanAbs); err2 == nil {
			if realAbs == realCwd {
				return "."
			}
			if rel2, err3 := filepath.Rel(realCwd, realAbs); err3 == nil && rel2 != "" && !filepath.IsAbs(rel2) && rel2 != ".." && !strings.HasPrefix(rel2, ".."+string(filepath.Separator)) {
				return filepath.ToSlash(rel2)
			}
		}
	}
	return filepath.ToSlash(dirs.StandardizeMacOSPath(cleanAbs))
}

// splitGlobFromPath splits an entry like `packages/pkg-*/src/**/*.ts` into basePath and globPattern.
// upstream: packages/coding-agent/src/tools/path-utils.ts (parseSearchPath)
func splitGlobFromPath(entry, cwd string) (basePath, globPattern string, hasGlob, recursive bool) {
	norm := filepath.ToSlash(expandPath(entry))
	if !hasGlobChars(norm) {
		return resolveToCwd(entry, cwd), "", false, true
	}
	lit := resolveToCwd(entry, cwd)
	if _, err := os.Stat(lit); err == nil {
		return lit, "", false, true
	}
	if !strings.Contains(norm, "/") {
		return resolveToCwd(".", cwd), "**/" + norm, true, true
	}
	segments := strings.Split(norm, "/")
	globIdx := -1
	for i, seg := range segments {
		if hasGlobChars(seg) {
			globIdx = i
			break
		}
	}
	baseRaw := strings.Join(segments[:globIdx], "/")
	if baseRaw == "" {
		if strings.HasPrefix(norm, "/") {
			baseRaw = "/"
		} else {
			baseRaw = "."
		}
	}
	pat := strings.Join(segments[globIdx:], "/")
	rec := strings.Contains(pat, "**") || strings.Contains(pat, "/")
	return resolveToCwd(baseRaw, cwd), pat, true, rec
}

// splitGlobFromURL splits an internal URL like `local://src/**/*.ts` into baseURL and globPattern.
func splitGlobFromURL(rawURL string) (baseURL, globPattern string, hasGlob, recursive bool) {
	if !hasGlobChars(rawURL) {
		return rawURL, "", false, true
	}
	sep := strings.Index(rawURL, "://")
	if sep < 0 {
		return rawURL, "", false, true
	}
	schemePrefix := rawURL[:sep+3]
	rest := rawURL[sep+3:]
	segments := strings.Split(rest, "/")
	globIdx := -1
	for i, seg := range segments {
		if hasGlobChars(seg) {
			globIdx = i
			break
		}
	}
	if globIdx <= 0 {
		return schemePrefix, rest, true, true
	}
	base := schemePrefix + strings.Join(segments[:globIdx], "/")
	pat := strings.Join(segments[globIdx:], "/")
	rec := strings.Contains(pat, "**") || strings.Contains(pat, "/")
	return base, pat, true, rec
}

func resolveToolScope(
	ctx sdk.Context,
	cwd string,
	rawInputs []string,
	action string,
	fileWritableOnly bool,
) (*scopeResolution, error) {
	entries, err := expandDelimitedEntries(rawInputs, cwd)
	if err != nil {
		return nil, err
	}

	uc := urls.Context{SDK: ctx, Cwd: cwd}
	var valid []resolvedTarget
	var missing []string
	var scopeDisplay []string

	for _, entry := range entries {
		if externalURLRe.MatchString(entry) {
			if fileWritableOnly {
				return nil, fmt.Errorf("Cannot rewrite external URL: %s. Use `read` or `search` to inspect fetched web content; ast_edit only applies to local files.", entry)
			}
			return nil, fmt.Errorf("Cannot %s external URL: %s. Use `read` to fetch web content, then search the returned text.", action, entry)
		}

		expanded := expandPath(entry)
		if urls.CanHandle(expanded) {
			baseURL, globPat, hasG, rec := splitGlobFromURL(expanded)
			u, parseErr := urls.Parse(baseURL)
			if parseErr != nil {
				return nil, fmt.Errorf("Cannot %s %s: %v", action, entry, parseErr)
			}
			h, ok := urls.Lookup(u.Scheme)
			if !ok {
				return nil, fmt.Errorf("Cannot %s %s: unknown internal scheme %s://", action, entry, u.Scheme)
			}
			spec := h.Spec()
			if fileWritableOnly {
				if spec.Write == nil || spec.Write.Via != "file" {
					return nil, fmt.Errorf("Cannot %s %s: %s:// URLs are not editable files", action, entry, u.Scheme)
				}
			}
			located, found, locErr := urls.Locate(uc, baseURL, urls.LocateOptions{})
			if locErr != nil {
				return nil, fmt.Errorf("Cannot %s %s: %v", action, entry, locErr)
			}
			if !found || located == "" {
				return nil, fmt.Errorf("Cannot %s %s: %s:// has no local file backing", action, entry, u.Scheme)
			}
			info, statErr := os.Stat(located)
			if statErr != nil {
				if len(entries) > 1 && os.IsNotExist(statErr) {
					missing = append(missing, entry)
					continue
				}
				return nil, fmt.Errorf("Cannot %s %s: %v", action, entry, statErr)
			}
			valid = append(valid, resolvedTarget{
				raw:          entry,
				basePath:     filepath.Clean(located),
				urlPrefix:    strings.TrimRight(baseURL, "/"),
				globPattern:  globPat,
				hasGlob:      hasG,
				isDirectory:  info.IsDir(),
				isURL:        true,
				urlImmutable: spec.Immutable,
				recursive:    rec,
			})
			scopeDisplay = append(scopeDisplay, entry)
			continue
		}

		basePath, globPat, hasG, rec := splitGlobFromPath(entry, cwd)
		info, statErr := os.Stat(basePath)
		if statErr != nil {
			if len(entries) > 1 && os.IsNotExist(statErr) {
				missing = append(missing, entry)
				continue
			}
			return nil, fmt.Errorf("Path not found: %s", formatPathRelativeToCwd(basePath, cwd))
		}
		valid = append(valid, resolvedTarget{
			raw:         entry,
			basePath:    basePath,
			globPattern: globPat,
			hasGlob:     hasG,
			isDirectory: info.IsDir(),
			recursive:   rec,
		})
		scopeDisplay = append(scopeDisplay, formatPathRelativeToCwd(basePath, cwd))
	}

	if len(valid) == 0 {
		return nil, fmt.Errorf("Path not found: %s", strings.Join(missing, ", "))
	}

	searchPath := valid[0].basePath
	isDir := valid[0].isDirectory
	if len(valid) > 1 {
		isDir = true
		searchPath = cwd
	}
	scopePath := strings.Join(scopeDisplay, ", ")
	if len(valid) == 1 && !valid[0].isURL {
		scopePath = formatPathRelativeToCwd(valid[0].basePath, cwd)
	}

	return &scopeResolution{
		searchPath:   searchPath,
		scopePath:    scopePath,
		isDirectory:  isDir,
		targets:      valid,
		missingPaths: missing,
	}, nil
}

// discoverCandidates walks all resolved targets and collects deduplicated candidate files
// matching language and glob filters, respecting .gitignore (including parent directories,
// .git/info/exclude, and global excludes), skipping .git, symlinks (follow_links=false),
// and node_modules (unless the glob mentions node_modules).
// upstream: crates/natives/src/ast.rs:312-344, crates/ast/src/search/mod.rs:198-273
func discoverCandidates(ctx context.Context, cwd string, scope *scopeResolution, explicitLang string) ([]candidateFile, error) {
	seen := make(map[string]struct{})
	var candidates []candidateFile

	for _, t := range scope.targets {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
		}

		if !t.isDirectory {
			absKey := canonicalKey(t.basePath)
			if _, ok := seen[absKey]; ok {
				continue
			}
			fileLang := explicitLang
			if fileLang == "" {
				inferred, ok := inferLangFromPath(t.basePath)
				if !ok {
					// Unknown extension without explicit lang: skipped in discovery.
					continue
				}
				fileLang = inferred
			}
			seen[absKey] = struct{}{}
			disp := formatPathRelativeToCwd(t.basePath, cwd)
			if t.isURL {
				disp = t.urlPrefix
			}
			candidates = append(candidates, candidateFile{
				absPath:     t.basePath,
				displayPath: disp,
				lang:        fileLang,
				isURL:       t.isURL,
				immutable:   t.urlImmutable,
			})
			continue
		}

		var includeMatcher *globMatcher
		if t.hasGlob {
			m, err := compileGlob(t.globPattern)
			if err != nil {
				return nil, fmt.Errorf("Invalid glob pattern: %v", err)
			}
			includeMatcher = m
		}
		includeNodeModules := strings.Contains(t.globPattern, "node_modules")

		entries, err := walkDirectory(ctx, t.basePath, includeMatcher, t.recursive, includeNodeModules)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			absKey := canonicalKey(entry.absPath)
			if _, ok := seen[absKey]; ok {
				continue
			}
			inferred, hasInferred := inferLangFromPath(entry.absPath)
			var fileLang string
			if explicitLang != "" {
				// upstream: crates/ast/src/search/mod.rs:254-258
				// Explicit language keeps unknown/extensionless files and compatible languages,
				// skipping files clearly belonging to another language.
				if hasInferred && !langParsesAs(inferred, explicitLang) {
					continue
				}
				fileLang = explicitLang
			} else {
				if !hasInferred {
					continue
				}
				fileLang = inferred
			}
			seen[absKey] = struct{}{}
			disp := formatPathRelativeToCwd(entry.absPath, cwd)
			if t.isURL {
				disp = t.urlPrefix + "/" + entry.relPath
			}
			candidates = append(candidates, candidateFile{
				absPath:     entry.absPath,
				displayPath: disp,
				lang:        fileLang,
				isURL:       t.isURL,
				immutable:   t.urlImmutable,
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].displayPath < candidates[j].displayPath
	})
	return candidates, nil
}

func canonicalKey(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

// --- Gitignore + glob walker matching crates/xfs/src/ignore.rs & crates/xfs/src/walk/mod.rs ---

type walkedFile struct {
	absPath string
	relPath string
}

type gitignoreRule struct {
	negated  bool
	dirOnly  bool
	anchored bool
	matcher  *globMatcher
}

type ignoreFrame struct {
	// baseRel is "" for walk root, "sub/dir" for subdirectories, or ":prefix"
	// for an ancestor directory above the walk root (where prefix is root relative
	// to the ancestor).
	baseRel string
	rules   []gitignoreRule
}

// ignoreChain holds the layered ignore sources with omp/ignore-crate precedence:
// .ignore > .gitignore > .git/info/exclude > global excludes.
// upstream: crates/xfs/src/ignore.rs:5-18
type ignoreChain struct {
	ignoreFrames    []ignoreFrame // .ignore (per dir & ancestors)
	gitignoreFrames []ignoreFrame // .gitignore (per dir & ancestors)
	excludeFrames   []ignoreFrame // .git/info/exclude (nearest repo root)
	globalRules     []gitignoreRule
	inRepo          bool
}

func loadIgnoreFile(path string) []gitignoreRule {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var rules []gitignoreRule
	scanner := bufio.NewScanner(f)
	first := true
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if first {
			line = strings.TrimPrefix(line, "\uFEFF")
			first = false
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		negated := false
		if strings.HasPrefix(line, "!") {
			negated = true
			line = line[1:]
		} else if strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`) {
			line = line[1:]
		}
		line = strings.TrimRight(line, " \t")
		if line == "" {
			continue
		}
		dirOnly := strings.HasSuffix(line, "/")
		if dirOnly {
			line = strings.TrimSuffix(line, "/")
		}
		anchored := strings.Contains(line, "/")
		line = strings.TrimPrefix(line, "/")
		if line == "" {
			continue
		}
		m, err := compileGlob(line)
		if err != nil {
			continue
		}
		rules = append(rules, gitignoreRule{
			negated:  negated,
			dirOnly:  dirOnly,
			anchored: anchored,
			matcher:  m,
		})
	}
	return rules
}

// resolveGitDir returns the git metadata directory for a repository root (handling
// both normal `.git` directories and worktree `.git` gitdir files).
func resolveGitDir(repoRoot string) string {
	dotGit := filepath.Join(repoRoot, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return dotGit
	}
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir:") {
		return ""
	}
	dir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if dir == "" {
		return ""
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repoRoot, dir)
	}
	return filepath.Clean(dir)
}

func hasRepoMarker(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, ".jj")); err == nil {
		return true
	}
	return false
}

// loadGlobalExcludes locates git's global excludes file following
// crates/xfs/src/ignore.rs:508-514 & 600-615:
// `core.excludesFile` from $GIT_CONFIG_GLOBAL, ~/.gitconfig,
// $XDG_CONFIG_HOME/git/config, $GIT_CONFIG_SYSTEM or /etc/gitconfig,
// falling back to $XDG_CONFIG_HOME/git/ignore (or ~/.config/git/ignore).
func loadGlobalExcludes() []gitignoreRule {
	home, _ := os.UserHomeDir()
	xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if xdg == "" && home != "" {
		xdg = filepath.Join(home, ".config")
	}

	var configs []string
	if v := strings.TrimSpace(os.Getenv("GIT_CONFIG_GLOBAL")); v != "" {
		configs = append(configs, v)
	} else {
		if home != "" {
			configs = append(configs, filepath.Join(home, ".gitconfig"))
		}
		if xdg != "" {
			configs = append(configs, filepath.Join(xdg, "git", "config"))
		}
	}
	if v := strings.TrimSpace(os.Getenv("GIT_CONFIG_SYSTEM")); v != "" {
		configs = append(configs, v)
	} else if runtime.GOOS != "windows" {
		configs = append(configs, "/etc/gitconfig")
	}

	var excludesPath string
	for _, cfg := range configs {
		if p := parseGitExcludesFile(cfg, home); p != "" {
			excludesPath = p
			break
		}
	}
	if excludesPath == "" && xdg != "" {
		excludesPath = filepath.Join(xdg, "git", "ignore")
	}
	if excludesPath == "" {
		return nil
	}
	return loadIgnoreFile(excludesPath)
}

func parseGitExcludesFile(configPath, home string) string {
	f, err := os.Open(configPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	inCore := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sec := strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			inCore = sec == "core"
			continue
		}
		if !inCore {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:eq]))
		if key != "excludesfile" {
			continue
		}
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		if strings.HasPrefix(val, "~/") && home != "" {
			val = filepath.Join(home, val[2:])
		}
		return val
	}
	return ""
}

// buildInitialIgnoreChain loads ancestor `.ignore`, `.gitignore`, `.git/info/exclude`
// up to the nearest repository root (`.git` or `.jj`), plus global excludes when
// inside a repository. Rules that would ignore the walk root itself are filtered
// out, matching crates/xfs/src/ignore.rs:15-16.
func buildInitialIgnoreChain(root string) ignoreChain {
	type ancestor struct {
		dir       string
		prefixRel string
		isRepo    bool
	}
	var chain []ancestor
	cur := filepath.Clean(root)
	inRepo := false

	for {
		rel, err := filepath.Rel(cur, root)
		if err != nil {
			break
		}
		prefix := ""
		if rel != "." {
			prefix = filepath.ToSlash(rel)
		}
		isRepo := hasRepoMarker(cur)
		chain = append(chain, ancestor{dir: cur, prefixRel: prefix, isRepo: isRepo})
		if isRepo {
			inRepo = true
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}

	var ic ignoreChain
	ic.inRepo = inRepo
	// Apply outermost ancestor first; if not inside a repo, only load walk root's own
	// `.gitignore`/`.ignore` (matching crates/xfs/src/ignore.rs:651-653: ancestor VCS
	// ignore files apply up to the nearest repository root; none when no ancestor is one).
	startIdx := len(chain) - 1
	if !inRepo {
		startIdx = 0
	}
	for i := startIdx; i >= 0; i-- {
		anc := chain[i]
		baseRel := ""
		if anc.prefixRel != "" {
			baseRel = ":" + anc.prefixRel
		}
		filterRootIgnoring := func(rules []gitignoreRule) []gitignoreRule {
			if anc.prefixRel == "" || len(rules) == 0 {
				return rules
			}
			// Drop ancestor rules that match the walk root itself so narrowing path
			// into an explicitly requested directory still searches inside it.
			var kept []gitignoreRule
			for _, r := range rules {
				if !r.negated && ruleMatchesPath(r, anc.prefixRel, true) {
					continue
				}
				kept = append(kept, r)
			}
			return kept
		}
		if rules := filterRootIgnoring(loadIgnoreFile(filepath.Join(anc.dir, ".ignore"))); len(rules) > 0 {
			ic.ignoreFrames = append(ic.ignoreFrames, ignoreFrame{baseRel: baseRel, rules: rules})
		}
		if rules := filterRootIgnoring(loadIgnoreFile(filepath.Join(anc.dir, ".gitignore"))); len(rules) > 0 {
			ic.gitignoreFrames = append(ic.gitignoreFrames, ignoreFrame{baseRel: baseRel, rules: rules})
		}
		if anc.isRepo {
			if gitDir := resolveGitDir(anc.dir); gitDir != "" {
				if rules := filterRootIgnoring(loadIgnoreFile(filepath.Join(gitDir, "info", "exclude"))); len(rules) > 0 {
					ic.excludeFrames = append(ic.excludeFrames, ignoreFrame{baseRel: baseRel, rules: rules})
				}
			}
		}
	}
	if inRepo {
		ic.globalRules = loadGlobalExcludes()
	}
	return ic
}

func ruleMatchesPath(rule gitignoreRule, subRel string, isDir bool) bool {
	if rule.dirOnly && !isDir {
		return false
	}
	if rule.anchored {
		return rule.matcher.match(subRel)
	}
	baseName := subRel
	if idx := strings.LastIndexByte(subRel, '/'); idx >= 0 {
		baseName = subRel[idx+1:]
	}
	return rule.matcher.match(baseName) || rule.matcher.match(subRel)
}

type ignoreVerdict int

const (
	verdictNone ignoreVerdict = iota
	verdictIgnore
	verdictWhitelist
)

func evalFrames(frames []ignoreFrame, relPath string, isDir bool) ignoreVerdict {
	// Nearest directory with a matching rule wins; within one file the last rule wins.
	for i := len(frames) - 1; i >= 0; i-- {
		frame := frames[i]
		var subRel string
		switch {
		case strings.HasPrefix(frame.baseRel, ":"):
			subRel = frame.baseRel[1:] + "/" + relPath
		case frame.baseRel != "":
			prefix := frame.baseRel + "/"
			if !strings.HasPrefix(relPath, prefix) {
				continue
			}
			subRel = relPath[len(prefix):]
		default:
			subRel = relPath
		}
		v := verdictNone
		for _, r := range frame.rules {
			if ruleMatchesPath(r, subRel, isDir) {
				if r.negated {
					v = verdictWhitelist
				} else {
					v = verdictIgnore
				}
			}
		}
		if v != verdictNone {
			return v
		}
	}
	return verdictNone
}

func (ic *ignoreChain) isIgnored(relPath string, isDir bool) bool {
	// Precedence: .ignore > .gitignore > .git/info/exclude > global excludes.
	if v := evalFrames(ic.ignoreFrames, relPath, isDir); v != verdictNone {
		return v == verdictIgnore
	}
	if v := evalFrames(ic.gitignoreFrames, relPath, isDir); v != verdictNone {
		return v == verdictIgnore
	}
	if v := evalFrames(ic.excludeFrames, relPath, isDir); v != verdictNone {
		return v == verdictIgnore
	}
	if len(ic.globalRules) > 0 {
		v := verdictNone
		for _, r := range ic.globalRules {
			if ruleMatchesPath(r, relPath, isDir) {
				if r.negated {
					v = verdictWhitelist
				} else {
					v = verdictIgnore
				}
			}
		}
		return v == verdictIgnore
	}
	return false
}

func walkDirectory(ctx context.Context, root string, include *globMatcher, recursive, includeNodeModules bool) ([]walkedFile, error) {
	var out []walkedFile
	initial := buildInitialIgnoreChain(root)

	var visit func(absDir, relDir string, ic ignoreChain) error
	visit = func(absDir, relDir string, ic ignoreChain) error {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
		entries, err := os.ReadDir(absDir)
		if err != nil {
			return nil
		}
		if relDir != "" {
			if rules := loadIgnoreFile(filepath.Join(absDir, ".ignore")); len(rules) > 0 {
				ic.ignoreFrames = append(ic.ignoreFrames[:len(ic.ignoreFrames):len(ic.ignoreFrames)], ignoreFrame{baseRel: relDir, rules: rules})
			}
			if rules := loadIgnoreFile(filepath.Join(absDir, ".gitignore")); len(rules) > 0 {
				ic.gitignoreFrames = append(ic.gitignoreFrames[:len(ic.gitignoreFrames):len(ic.gitignoreFrames)], ignoreFrame{baseRel: relDir, rules: rules})
			}
		}
		for _, de := range entries {
			name := de.Name()
			if name == ".git" {
				continue
			}
			if !includeNodeModules && name == "node_modules" {
				continue
			}
			// Never follow symlinks during directory walks (matches omp crates/natives/src/ast.rs:328
			// follow_links: false and crates/ast/src/search/mod.rs:240-262 keeping only FileType::Dir/File).
			entryType := de.Type()
			if entryType&fs.ModeSymlink != 0 {
				continue
			}
			isDir := entryType.IsDir()
			if !isDir && !entryType.IsRegular() {
				continue
			}
			rel := name
			if relDir != "" {
				rel = relDir + "/" + name
			}
			if ic.isIgnored(rel, isDir) {
				continue
			}
			abs := filepath.Join(absDir, name)
			if isDir {
				if recursive {
					if err := visit(abs, rel, ic); err != nil {
						return err
					}
				}
				continue
			}
			if include != nil && !include.match(rel) {
				continue
			}
			out = append(out, walkedFile{absPath: abs, relPath: rel})
		}
		return nil
	}

	if err := visit(root, "", initial); err != nil {
		return nil, err
	}
	return out, nil
}

// globMatcher compiles a glob pattern supporting *, **, ?, [...], and {a,b}.
type globMatcher struct {
	re *regexp.Regexp
}

func (g *globMatcher) match(p string) bool {
	if g == nil || g.re == nil {
		return true
	}
	return g.re.MatchString(p)
}

func compileGlob(pattern string) (*globMatcher, error) {
	expr := "^" + globToRegex(filepath.ToSlash(pattern)) + "$"
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	return &globMatcher{re: re}, nil
}

func globToRegex(pat string) string {
	var b strings.Builder
	braceDepth := 0
	for i := 0; i < len(pat); i++ {
		c := pat[i]
		switch c {
		case '*':
			if i+1 < len(pat) && pat[i+1] == '*' {
				if i+2 < len(pat) && pat[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			end := i + 1
			if end < len(pat) && (pat[end] == '!' || pat[end] == '^') {
				end++
			}
			if end < len(pat) && pat[end] == ']' {
				end++
			}
			for end < len(pat) && pat[end] != ']' {
				end++
			}
			if end >= len(pat) {
				b.WriteString(`\[`)
			} else {
				cls := pat[i+1 : end]
				if strings.HasPrefix(cls, "!") {
					cls = "^" + cls[1:]
				}
				b.WriteByte('[')
				b.WriteString(cls)
				b.WriteByte(']')
				i = end
			}
		case '{':
			braceDepth++
			b.WriteString("(?:")
		case '}':
			if braceDepth > 0 {
				braceDepth--
				b.WriteByte(')')
			} else {
				b.WriteString(`\}`)
			}
		case ',':
			if braceDepth > 0 {
				b.WriteByte('|')
			} else {
				b.WriteByte(',')
			}
		case '\\':
			if i+1 < len(pat) {
				i++
				b.WriteString(regexp.QuoteMeta(string(pat[i])))
			} else {
				b.WriteString(`\\`)
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	for braceDepth > 0 {
		b.WriteByte(')')
		braceDepth--
	}
	return b.String()
}
