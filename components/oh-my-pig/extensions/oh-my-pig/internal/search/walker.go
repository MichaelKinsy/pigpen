package search

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"strings"
	"time"
	"unicode/utf8"
)

// walkEntry is a single filesystem entry produced by walkDir.
type walkEntry struct {
	absPath   string
	relPath   string // forward-slash path relative to walk root
	isDir     bool
	isSymlink bool
	mtime     time.Time
	size      int64
}

type walkOptions struct {
	hidden             bool
	gitignore          bool
	recursive          bool
	followLinks        bool           // follow symlinks with cycle detection (e.g. skill://)
	includeSymlinks    bool           // report symlinks as entries without descending (glob tool)
	excludes           []*globMatcher // !glob exclusions rebased onto the walk root
	include            *globMatcher   // optional positive glob filter
	includeDir         bool           // whether to yield matching directories (glob tool)
	dirOnly            bool           // yield directories only (trailing-slash globs)
	includeNodeModules bool           // allow descending into node_modules
	includeGit         bool           // allow descending into .git
	maxResults         int            // optional early-stop cap (>0 stops walk once reached)
}

// ignoreMatch mirrors crates/xfs/src/ignore.rs Match.
type ignoreMatch int

const (
	ignoreNone ignoreMatch = iota
	ignoreExcluded
	ignoreWhitelisted
)

// gitignoreRule is a single parsed line from an ignore file.
type gitignoreRule struct {
	negated  bool
	dirOnly  bool
	anchored bool
	matcher  *globMatcher
}

type ignoreRuleSet struct {
	anchored bool
	rules    []gitignoreRule
}

// ignoreFrame mirrors crates/xfs/src/ignore.rs Frame:
// files[0] = .ignore, files[1] = .gitignore, files[2] = .git/info/exclude.
// Precedence: .ignore > .gitignore > .git/info/exclude > global excludes.
type ignoreFrame struct {
	parent  *ignoreFrame
	files   [3]*ignoreRuleSet // 0: .ignore, 1: .gitignore, 2: .git/info/exclude
	baseRel string            // frame dir relative to walk root ("" for root)
	upRel   string            // for ancestor frames above root: walk root relative to frame dir
	hasRepo bool              // directory has .git or .jj
	anyRepo bool              // this frame or an ancestor is a repo root
}

// ignorer evaluates omp's full ignore chain (crates/xfs/src/ignore.rs).
type ignorer struct {
	global *ignoreRuleSet
	cwd    string
	root   string
}

func (rs *ignoreRuleSet) matched(relPath string, isDir bool) ignoreMatch {
	if rs == nil || len(rs.rules) == 0 {
		return ignoreNone
	}
	baseName := relPath
	if idx := strings.LastIndexByte(relPath, '/'); idx != -1 {
		baseName = relPath[idx+1:]
	}
	res := ignoreNone
	for _, rule := range rs.rules {
		if rule.dirOnly && !isDir {
			continue
		}
		var ok bool
		if rule.anchored {
			ok = rule.matcher.match(relPath)
		} else {
			ok = rule.matcher.match(baseName) || rule.matcher.match(relPath)
		}
		if ok {
			if rule.negated {
				res = ignoreWhitelisted
			} else {
				res = ignoreExcluded
			}
		}
	}
	return res
}

// matchedPathOrParents checks every parent directory prefix (as a dir) and then relPath itself.
// upstream: crates/xfs/src/ignore.rs:195-213
func (rs *ignoreRuleSet) matchedPathOrParents(relPath string, isDir bool) ignoreMatch {
	if rs == nil || len(rs.rules) == 0 {
		return ignoreNone
	}
	for i := range len(relPath) {
		if relPath[i] == '/' && i > 0 {
			if m := rs.matched(relPath[:i], true); m == ignoreExcluded {
				return ignoreExcluded
			}
		}
	}
	return rs.matched(relPath, isDir)
}

func parseIgnoreLine(raw string) (gitignoreRule, bool) {
	line := strings.TrimRight(raw, "\r")
	if line == "" || strings.HasPrefix(line, "#") {
		return gitignoreRule{}, false
	}
	negated := false
	if strings.HasPrefix(line, "!") {
		negated = true
		line = line[1:]
	} else if strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`) {
		line = line[1:]
	}
	line = trimTrailingUnescapedSpaces(line)
	if line == "" {
		return gitignoreRule{}, false
	}
	dirOnly := strings.HasSuffix(line, "/")
	if dirOnly {
		line = strings.TrimSuffix(line, "/")
	}
	anchored := strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	if line == "" {
		return gitignoreRule{}, false
	}
	m, err := compileIgnoreGlob(line)
	if err != nil {
		return gitignoreRule{}, false
	}
	return gitignoreRule{
		negated:  negated,
		dirOnly:  dirOnly,
		anchored: anchored,
		matcher:  m,
	}, true
}

func trimTrailingUnescapedSpaces(line string) string {
	end := len(line)
	for end > 0 && (line[end-1] == ' ' || line[end-1] == '\t') {
		if end >= 2 && line[end-2] == '\\' {
			// Keep escaped trailing space and strip its backslash.
			return line[:end-2] + line[end-1:end]
		}
		end--
	}
	return line[:end]
}

// parseIgnoreBytes parses a gitignore-format buffer.
// Strips UTF-8 BOM on line 1, handles CRLF, and stops at invalid UTF-8 lines
// matching crates/xfs/src/ignore.rs parse_framing.
func parseIgnoreBytes(data []byte) *ignoreRuleSet {
	if bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		data = data[3:]
	}
	var rules []gitignoreRule
	anchored := false
	for len(data) > 0 {
		nl := bytes.IndexByte(data, '\n')
		var lineBytes []byte
		if nl < 0 {
			lineBytes = data
			data = nil
		} else {
			lineBytes = data[:nl]
			data = data[nl+1:]
		}
		if !utf8.Valid(lineBytes) {
			break
		}
		if rule, ok := parseIgnoreLine(string(lineBytes)); ok {
			if rule.anchored {
				anchored = true
			}
			rules = append(rules, rule)
		}
	}
	if len(rules) == 0 {
		return nil
	}
	return &ignoreRuleSet{anchored: anchored, rules: rules}
}

// loadIgnoreFile reads path and parses ignore rules.
func loadIgnoreFile(path string) *ignoreRuleSet {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil
	}
	return parseIgnoreBytes(data)
}

// loadAncestorIgnoreFile compiles an ancestor's ignore file, dropping any single
// rule that would ignore the walk root at upRel.
// upstream: crates/xfs/src/ignore.rs:720-746
func loadAncestorIgnoreFile(path, upRel string) *ignoreRuleSet {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil
	}
	rs := parseIgnoreBytes(data)
	if rs == nil {
		return nil
	}
	if rs.matchedPathOrParents(upRel, true) != ignoreExcluded {
		return rs
	}
	var kept []gitignoreRule
	anchored := false
	for _, r := range rs.rules {
		single := &ignoreRuleSet{anchored: r.anchored, rules: []gitignoreRule{r}}
		if single.matchedPathOrParents(upRel, true) != ignoreExcluded {
			if r.anchored {
				anchored = true
			}
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return &ignoreRuleSet{anchored: anchored, rules: kept}
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// loadGlobalExcludes reads git's global excludes file (core.excludesFile from
// $GIT_CONFIG_GLOBAL, ~/.gitconfig, $XDG_CONFIG_HOME/git/config,
// $GIT_CONFIG_SYSTEM or /etc/gitconfig; fallback $XDG_CONFIG_HOME/git/ignore).
// upstream: crates/xfs/src/ignore.rs:598-615,758-833
func loadGlobalExcludes() *ignoreRuleSet {
	home, _ := os.UserHomeDir()
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" && home != "" {
		configHome = filepath.Join(home, ".config")
	}

	var configs []string
	if v := os.Getenv("GIT_CONFIG_GLOBAL"); v != "" {
		configs = append(configs, v)
	}
	if home != "" {
		configs = append(configs, filepath.Join(home, ".gitconfig"))
	}
	if configHome != "" {
		configs = append(configs, filepath.Join(configHome, "git", "config"))
	}
	if v := os.Getenv("GIT_CONFIG_SYSTEM"); v != "" {
		configs = append(configs, v)
	} else {
		configs = append(configs, "/etc/gitconfig")
	}

	var excludesPath string
	for _, cfgPath := range configs {
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			continue
		}
		if p := parseGitExcludesFile(data, home); p != "" {
			excludesPath = p
			break
		}
	}
	if excludesPath == "" && configHome != "" {
		excludesPath = filepath.Join(configHome, "git", "ignore")
	}
	if excludesPath == "" {
		return nil
	}
	return loadIgnoreFile(excludesPath)
}

var gitExcludesLineRe = regexp.MustCompile(`(?im)^\s*excludesfile\s*=\s*"?\s*(\S+?)\s*"?\s*$`)

func parseGitExcludesFile(data []byte, home string) string {
	m := gitExcludesLineRe.FindSubmatch(data)
	if len(m) < 2 {
		return ""
	}
	val := string(m[1])
	if home != "" && strings.Contains(val, "~") {
		val = strings.ReplaceAll(val, "~", home)
	}
	return val
}

// buildIgnorerAndRootFrame builds the global ignorer and ancestor frames up to the
// nearest repository root (.git or .jj). When no ancestor is a repository root,
// no ancestor ignore frames are loaded (matching crates/xfs/src/ignore.rs:650-716).
func buildIgnorerAndRootFrame(root string) (*ignorer, *ignoreFrame) {
	cwd, _ := os.Getwd()
	ig := &ignorer{
		global: loadGlobalExcludes(),
		cwd:    filepath.ToSlash(filepath.Clean(cwd)),
		root:   filepath.ToSlash(filepath.Clean(root)),
	}

	// Collect ancestors above root until we hit a repo marker (.git or .jj).
	type anc struct {
		dir   string
		upRel string
	}
	var ancestors []anc
	cur := filepath.Clean(root)
	upRel := ""
	repoIdx := -1
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		name := filepath.Base(cur)
		if upRel == "" {
			upRel = name
		} else {
			upRel = name + "/" + upRel
		}
		ancestors = append(ancestors, anc{dir: parent, upRel: upRel})
		if pathExists(filepath.Join(parent, ".git")) || pathExists(filepath.Join(parent, ".jj")) {
			repoIdx = len(ancestors) - 1
			break
		}
		cur = parent
	}

	// Build ancestor chain outermost-first ONLY when an ancestor is a repo root.
	var frame *ignoreFrame
	if repoIdx >= 0 {
		for i := repoIdx; i >= 0; i-- {
			a := ancestors[i]
			hasRepo := i == repoIdx
			var files [3]*ignoreRuleSet
			files[0] = loadAncestorIgnoreFile(filepath.Join(a.dir, ".ignore"), a.upRel)
			files[1] = loadAncestorIgnoreFile(filepath.Join(a.dir, ".gitignore"), a.upRel)
			if hasRepo && pathExists(filepath.Join(a.dir, ".git")) {
				files[2] = loadAncestorIgnoreFile(filepath.Join(a.dir, ".git", "info", "exclude"), a.upRel)
			}
			if hasRepo || files[0] != nil || files[1] != nil || files[2] != nil {
				anyRepo := hasRepo || (frame != nil && frame.anyRepo)
				frame = &ignoreFrame{
					parent:  frame,
					files:   files,
					upRel:   a.upRel,
					hasRepo: hasRepo,
					anyRepo: anyRepo,
				}
			}
		}
	}

	// Add the walk root directory's own ignore files and repo markers.
	frame = pushDirIgnoreFrame(frame, root, "")
	return ig, frame
}

func pushDirIgnoreFrame(parent *ignoreFrame, absDir, relDir string) *ignoreFrame {
	hasGit := pathExists(filepath.Join(absDir, ".git"))
	hasJJ := pathExists(filepath.Join(absDir, ".jj"))
	hasRepo := hasGit || hasJJ

	var files [3]*ignoreRuleSet
	files[0] = loadIgnoreFile(filepath.Join(absDir, ".ignore"))
	files[1] = loadIgnoreFile(filepath.Join(absDir, ".gitignore"))
	if hasGit {
		files[2] = loadIgnoreFile(filepath.Join(absDir, ".git", "info", "exclude"))
	}
	if !hasRepo && files[0] == nil && files[1] == nil && files[2] == nil {
		return parent
	}
	anyRepo := hasRepo || (parent != nil && parent.anyRepo)
	return &ignoreFrame{
		parent:  parent,
		files:   files,
		baseRel: relDir,
		hasRepo: hasRepo,
		anyRepo: anyRepo,
	}
}

func (f *ignoreFrame) relFor(relPath string) string {
	if f.upRel != "" {
		return f.upRel + "/" + relPath
	}
	if f.baseRel == "" {
		return relPath
	}
	prefix := f.baseRel + "/"
	return strings.TrimPrefix(relPath, prefix)
}

// isIgnored evaluates omp's precedence chain:
// for each kind (0: .ignore, 1: .gitignore, 2: .git/info/exclude), the nearest
// directory with a matching rule decides; .ignore beats .gitignore beats
// .git/info/exclude beats global excludes. Exclude files above a nested repo
// boundary are not consulted; global excludes apply only inside a repo.
// upstream: crates/xfs/src/ignore.rs:540-595
func (ig *ignorer) isIgnored(frame *ignoreFrame, relPath string, isDir bool) bool {
	if frame == nil {
		return false
	}
	var found [3]ignoreMatch
	sawRepo := false
	for cur := frame; cur != nil; cur = cur.parent {
		for k := range 3 {
			if found[k] != ignoreNone {
				continue
			}
			// .git/info/exclude (k==2) applies only inside a repo and never above a nested repo root.
			if k == 2 && (!frame.anyRepo || sawRepo) {
				continue
			}
			if rs := cur.files[k]; rs != nil {
				found[k] = rs.matched(cur.relFor(relPath), isDir)
			}
		}
		if cur.hasRepo {
			sawRepo = true
		}
	}
	for k := range 3 {
		if found[k] != ignoreNone {
			return found[k] == ignoreExcluded
		}
	}
	if frame.anyRepo && ig.global != nil {
		p := relPath
		if ig.global.anchored {
			full := strings.TrimRight(ig.root, "/") + "/" + relPath
			cwdPrefix := strings.TrimRight(ig.cwd, "/")
			if cwdPrefix != "" && strings.HasPrefix(full, cwdPrefix+"/") {
				p = full[len(cwdPrefix)+1:]
			} else {
				p = full
			}
		}
		return ig.global.matched(p, isDir) == ignoreExcluded
	}
	return false
}

// IgnoreMatcher wraps omp's full ignore chain (.ignore > .gitignore > .git/info/exclude >
// global excludes, including ancestor repo rules and character-class globs) for reuse by
// callers such as internal/prompt's workspace tree builder.
// upstream: crates/xfs/src/ignore.rs
type IgnoreMatcher struct {
	ig    *ignorer
	frame *ignoreFrame
}

// NewIgnoreMatcher initializes an IgnoreMatcher rooted at root (walking up to any enclosing
// .git/.jj repository root and loading global excludes).
func NewIgnoreMatcher(root string) *IgnoreMatcher {
	ig, frame := buildIgnorerAndRootFrame(root)
	return &IgnoreMatcher{ig: ig, frame: frame}
}

// EnterDir returns a child IgnoreMatcher incorporating any .ignore / .gitignore /
// .git/info/exclude files in absDir (at forward-slash relDir beneath the walk root).
func (m *IgnoreMatcher) EnterDir(absDir, relDir string) *IgnoreMatcher {
	if m == nil {
		return nil
	}
	return &IgnoreMatcher{
		ig:    m.ig,
		frame: pushDirIgnoreFrame(m.frame, absDir, relDir),
	}
}

// IsIgnored reports whether relPath (forward-slash path relative to walk root) is ignored.
func (m *IgnoreMatcher) IsIgnored(relPath string, isDir bool) bool {
	if m == nil || m.ig == nil {
		return false
	}
	return m.ig.isIgnored(m.frame, relPath, isDir)
}

// walkDir traverses root respecting gitignore (.ignore, .gitignore, .git/info/exclude,
// global excludes), hidden-file, node_modules, symlink, and exclusion rules.
// On cancellation or deadline expiry it returns the partial entries gathered so far
// together with ctx.Err(), so callers like glob can surface partial results on timeout.
// upstream: crates/xfs/src/walk/mod.rs, crates/xfs/src/glob.rs
func walkDir(ctx context.Context, root string, opts walkOptions) ([]walkEntry, error) {
	var results []walkEntry
	var ig *ignorer
	var rootFrame *ignoreFrame
	if opts.gitignore {
		ig, rootFrame = buildIgnorerAndRootFrame(root)
	}

	visitedReal := make(map[string]struct{})
	if opts.followLinks {
		if realRoot, err := filepath.EvalSymlinks(root); err == nil {
			visitedReal[realRoot] = struct{}{}
		}
	}

	var visit func(absDir, relDir string, frame *ignoreFrame) error
	visit = func(absDir, relDir string, frame *ignoreFrame) error {
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
		for _, d := range entries {
			if ctx != nil {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
			}
			name := d.Name()
			if name == ".git" && !opts.includeGit {
				continue
			}
			if !opts.hidden && strings.HasPrefix(name, ".") {
				continue
			}
			isSymlink := d.Type()&fs.ModeSymlink != 0
			absPath := filepath.Join(absDir, name)
			isDir := d.IsDir()
			var resolvedInfo fs.FileInfo

			if isSymlink {
				if !opts.followLinks && !opts.includeSymlinks {
					continue
				}
				if opts.followLinks {
					info, statErr := os.Stat(absPath)
					if statErr != nil {
						continue
					}
					resolvedInfo = info
					isDir = info.IsDir()
				}
			}

			relPath := name
			if relDir != "" {
				relPath = relDir + "/" + name
			}

			// Prune node_modules independently of .gitignore unless explicitly requested.
			// upstream: crates/xfs/src/grep/run.rs:687-691, crates/xfs/src/glob.rs:240-241
			if isDir && name == "node_modules" && !opts.includeNodeModules {
				continue
			}

			if opts.gitignore && ig.isIgnored(frame, relPath, isDir) {
				continue
			}
			if isExcluded(opts.excludes, relPath, name, isDir) {
				continue
			}

			// Symlink entry in glob mode (not descending unless followLinks is set).
			// upstream: crates/xfs/src/glob.rs:164-168,298-328
			if isSymlink && !opts.followLinks {
				if opts.dirOnly {
					targetInfo, statErr := os.Stat(absPath)
					if statErr != nil || !targetInfo.IsDir() {
						continue
					}
					if opts.include != nil && !opts.include.match(relPath) && !opts.include.match(relPath+"/") {
						continue
					}
					results = append(results, walkEntry{
						absPath:   absPath,
						relPath:   relPath,
						isDir:     true,
						isSymlink: true,
						mtime:     targetInfo.ModTime(),
					})
					continue
				}
				if opts.include != nil && !opts.include.match(relPath) {
					continue
				}
				info, _ := d.Info()
				var mt time.Time
				if info != nil {
					mt = info.ModTime()
				}
				results = append(results, walkEntry{
					absPath:   absPath,
					relPath:   relPath,
					isDir:     false,
					isSymlink: true,
					mtime:     mt,
				})
				if opts.maxResults > 0 && len(results) >= opts.maxResults {
					return errWalkLimitReached
				}
				continue
			}
			if isDir {
				if opts.includeDir && (opts.include == nil || opts.include.match(relPath) || opts.include.match(relPath+"/")) {
					info := resolvedInfo
					if info == nil {
						info, _ = d.Info()
					}
					var mt time.Time
					if info != nil {
						mt = info.ModTime()
					}
					results = append(results, walkEntry{
						absPath: absPath,
						relPath: relPath,
						isDir:   true,
						mtime:   mt,
					})
					if opts.maxResults > 0 && len(results) >= opts.maxResults {
						return errWalkLimitReached
					}
				}
				if opts.recursive {
					if opts.followLinks && isSymlink {
						realDir, err := filepath.EvalSymlinks(absPath)
						if err != nil {
							continue
						}
						if _, seen := visitedReal[realDir]; seen {
							continue
						}
						visitedReal[realDir] = struct{}{}
					}
					childFrame := frame
					if opts.gitignore {
						childFrame = pushDirIgnoreFrame(frame, absPath, relPath)
					}
					if err := visit(absPath, relPath, childFrame); err != nil {
						return err
					}
				}
				continue
			}

			if opts.dirOnly {
				continue
			}
			if opts.include != nil && !opts.include.match(relPath) {
				continue
			}
			info := resolvedInfo
			if info == nil {
				info, _ = d.Info()
			}
			var mt time.Time
			var sz int64
			if info != nil {
				mt = info.ModTime()
				sz = info.Size()
			}
			results = append(results, walkEntry{
				absPath: absPath,
				relPath: relPath,
				isDir:   false,
				mtime:   mt,
				size:    sz,
			})
			if opts.maxResults > 0 && len(results) >= opts.maxResults {
				return errWalkLimitReached
			}
		}
		return nil
	}

	err := visit(root, "", rootFrame)
	if err == errWalkLimitReached {
		return results, nil
	}
	return results, err
}

var errWalkLimitReached = fmt.Errorf("walk limit reached")

func isExcluded(excludes []*globMatcher, relPath, baseName string, isDir bool) bool {
	for _, ex := range excludes {
		if ex.anchored {
			if ex.match(relPath) || (isDir && ex.match(relPath+"/")) {
				return true
			}
			continue
		}
		if ex.match(relPath) || ex.match(baseName) {
			return true
		}
		if isDir && ex.match(relPath+"/") {
			return true
		}
	}
	return false
}

// pathMentionsSegment reports whether p contains seg as a whole path segment
// (split on `/` or `\`, ignoring trailing slashes).
func pathMentionsSegment(p, seg string) bool {
	norm := strings.ReplaceAll(p, "\\", "/")
	for _, part := range strings.Split(norm, "/") {
		if part == seg {
			return true
		}
	}
	return false
}

// globMatcher compiles a glob pattern supporting *, ?, **, [abc], and {a,b} into a Go regexp.
type globMatcher struct {
	re       *regexp.Regexp
	anchored bool
}

func compileGlob(pattern string) (*globMatcher, error) {
	re, err := globToRegexp(pattern, false)
	if err != nil {
		return nil, err
	}
	return &globMatcher{re: re}, nil
}

// compileIgnoreGlob compiles a gitignore-dialect glob where `{`/`}` are literal
// and an unclosed `[` is literal (crates/xfs/src/ignore.rs:58-59).
func compileIgnoreGlob(pattern string) (*globMatcher, error) {
	re, err := globToRegexp(pattern, true)
	if err != nil {
		return nil, err
	}
	return &globMatcher{re: re}, nil
}

func compileGlobAnchored(pattern string) (*globMatcher, error) {
	re, err := globToRegexp(pattern, false)
	if err != nil {
		return nil, err
	}
	return &globMatcher{re: re, anchored: true}, nil
}

// compileGrepScopeGlob compiles a grep scope glob that matches both direct files
// and any file nested under a matched directory (matching crates/xfs/src/glob.rs:87-97).
func compileGrepScopeGlob(pattern string, bareGlob bool) (*globMatcher, error) {
	p := strings.ReplaceAll(pattern, "\\", "/")
	if bareGlob && !strings.Contains(p, "/") && !strings.HasPrefix(p, "**") && !isExactBraceUnion(p) {
		p = "**/" + p
	}
	base := strings.TrimRight(p, "/")
	if base == "**" || strings.HasSuffix(base, "/**") {
		return compileGlob(p)
	}
	re1, err := globFragment(p, false)
	if err != nil {
		return nil, globPatternError(p, err)
	}
	re2, err := globFragment(base+"/**", false)
	if err != nil {
		return nil, globPatternError(p, err)
	}
	re, err := regexp.Compile("^(?:" + re1 + "|" + re2 + ")$")
	if err != nil {
		return nil, globPatternError(p, err)
	}
	return &globMatcher{re: re}, nil
}

func isExactBraceUnion(pattern string) bool {
	if !strings.HasPrefix(pattern, "{") || !strings.HasSuffix(pattern, "}") {
		return false
	}
	inner := pattern[1 : len(pattern)-1]
	return inner != "" && !strings.ContainsAny(inner, "*?[]{}")
}

func globToRegexp(pattern string, ignoreDialect bool) (*regexp.Regexp, error) {
	frag, err := globFragment(pattern, ignoreDialect)
	if err != nil {
		return nil, globPatternError(pattern, err)
	}
	re, err := regexp.Compile("^" + frag + "$")
	if err != nil {
		return nil, globPatternError(pattern, err)
	}
	return re, nil
}

// globSyntaxError is a reason crates/xfs/src/pattern.rs gives for a malformed pattern (parse, parse_class).
type globSyntaxError string

func (e globSyntaxError) Error() string { return string(e) }

// globPatternError names the whole pattern in front of the reason, as PatternError's Display does
// (crates/xfs/src/pattern.rs:58-61). A reversed class range is left to the regexp engine to find.
func globPatternError(pattern string, err error) error {
	var se *syntax.Error
	if errors.As(err, &se) && se.Code == syntax.ErrInvalidCharRange {
		return fmt.Errorf("%s: invalid character range", pattern)
	}
	return fmt.Errorf("%s: %w", pattern, err)
}

func globFragment(pattern string, ignoreDialect bool) (string, error) {
	p := strings.ReplaceAll(pattern, "\\", "/")
	if !ignoreDialect {
		// Close any unclosed brace groups as crates/xfs/src/glob.rs:57-59 does.
		opens := strings.Count(p, "{")
		closes := strings.Count(p, "}")
		if opens > closes {
			p += strings.Repeat("}", opens-closes)
		}
	}
	var b strings.Builder
	n := len(p)
	for i := 0; i < n; {
		c := p[i]
		// Preserve multi-byte UTF-8 runes intact instead of widening each byte to a rune.
		if c >= 0x80 {
			r, size := utf8.DecodeRuneInString(p[i:])
			b.WriteString(regexp.QuoteMeta(string(r)))
			i += size
			continue
		}
		switch c {
		case '*':
			if i+1 < n && p[i+1] == '*' {
				if i+2 < n && p[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 3
				} else {
					b.WriteString(".*")
					i += 2
				}
			} else {
				b.WriteString("[^/]*")
				i++
			}
		case '?':
			b.WriteString("[^/]")
			i++
		case '[':
			j := i + 1
			if j < n && (p[j] == '!' || p[j] == '^') {
				j++
			}
			if j < n && p[j] == ']' {
				j++
			}
			for j < n && p[j] != ']' {
				j++
			}
			if j >= n {
				if !ignoreDialect {
					// Only the gitignore dialect keeps an unclosed `[` as a literal (pattern.rs parse_class).
					return "", globSyntaxError("unclosed character class")
				}
				b.WriteString(`\[`)
				i++
			} else {
				class := p[i+1 : j]
				if strings.HasPrefix(class, "!") {
					class = "^" + class[1:]
				}
				b.WriteString("[")
				b.WriteString(class)
				b.WriteString("]")
				i = j + 1
			}
		case '{':
			if ignoreDialect {
				b.WriteString(`\{`)
				i++
				continue
			}
			end := findMatchingBrace(p, i)
			if end == -1 {
				b.WriteString(`\{`)
				i++
			} else {
				alts := splitBraceAlternatives(p[i+1 : end])
				b.WriteString("(?:")
				for idx, alt := range alts {
					if idx > 0 {
						b.WriteString("|")
					}
					sub, err := globFragment(alt, false)
					if err != nil {
						return "", err
					}
					b.WriteString(sub)
				}
				b.WriteString(")")
				i = end + 1
			}
		case '}':
			if ignoreDialect {
				b.WriteString(`\}`)
				i++
				continue
			}
			// crates/xfs/src/pattern.rs:245-252: a `}` with no open alternation is a pattern error.
			return "", globSyntaxError("unopened alternation")
		default:
			b.WriteString(regexp.QuoteMeta(string(rune(c))))
			i++
		}
	}
	return b.String(), nil
}

func findMatchingBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		if s[i] == '{' {
			depth++
		} else if s[i] == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitBraceAlternatives(inner string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	for i := range len(inner) {
		c := inner[i]
		switch c {
		case '{':
			depth++
			cur.WriteByte(c)
		case '}':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(c)
		case ',':
			if depth == 0 {
				out = append(out, cur.String())
				cur.Reset()
				continue
			}
			cur.WriteByte(c)
		default:
			cur.WriteByte(c)
		}
	}
	out = append(out, cur.String())
	return out
}

func (g *globMatcher) match(s string) bool {
	if g == nil || g.re == nil {
		return true
	}
	return g.re.MatchString(s)
}
