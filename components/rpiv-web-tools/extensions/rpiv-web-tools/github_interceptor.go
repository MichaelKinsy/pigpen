package rpiv_web_tools

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The GitHub URL interceptor. upstream: providers/interceptors/github.ts. It answers a repository root,
// tree or blob URL of github.com from a shallow clone (gh repo clone, else git clone), or from the REST API
// through gh when the repository is large, the URL names a commit, or the clone fails. Every failure is a
// null result, never an error, and the caller falls through to the provider's fetch.

const (
	githubTokenEnvVar        = "GITHUB_TOKEN" // upstream: github.ts:22
	githubMaxTreeEntries     = 200
	githubMaxInlineFileChars = 100_000
	githubReadmeChars        = 8192
	githubHint               = "[rpiv-web-tools] Install `gh` CLI for better GitHub repo access including private repos.\n"
	githubExploreHint        = "Use `read` and `bash` tools at the path above to explore further."
)

// ghHintWriter receives the one-time "install gh" hint, which the original prints with console.error.
var ghHintWriter io.Writer = os.Stderr

func stringSet(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, i := range items {
		m[i] = true
	}
	return m
}

var githubBinaryExtensions = stringSet(
	".png", ".jpg", ".jpeg", ".gif", ".bmp", ".ico", ".webp", ".svg", ".tiff", ".tif", ".mp3", ".mp4", ".avi", ".mov",
	".mkv", ".flv", ".wmv", ".wav", ".ogg", ".webm", ".flac", ".aac", ".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar",
	".zst", ".exe", ".dll", ".so", ".dylib", ".bin", ".o", ".a", ".lib", ".woff", ".woff2", ".ttf", ".otf", ".eot", ".pdf",
	".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".sqlite", ".db", ".sqlite3", ".pyc", ".pyo", ".class", ".jar",
	".war", ".iso", ".img", ".dmg")

var githubNoiseDirs = stringSet("node_modules", "vendor", ".next", "dist", "build", "__pycache__", ".venv", "venv", ".tox",
	".mypy_cache", ".pytest_cache", "target", ".gradle", ".idea", ".vscode")

var githubNonCodeSegments = stringSet("issues", "pull", "pulls", "discussions", "releases", "wiki", "actions", "settings",
	"security", "projects", "graphs", "compare", "commits", "tags", "branches", "stargazers", "watchers", "network", "forks",
	"milestone", "labels", "packages", "codespaces", "contribute", "community", "sponsors", "invitations", "notifications",
	"insights")

// ---- URL parsing -------------------------------------------------------------------------------

// githubURLInfo is GitHubUrlInfo. Ref and Path are "" where the original leaves them undefined.
type githubURLInfo struct {
	Owner, Repo, Ref string
	RefIsFullSha     bool
	Path             string
	Type             string // root, blob or tree
}

var fullSHARe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// githubURLParts is `new URL(raw)` for an http(s) URL as the interceptor reads it: the host as the URL
// Standard serializes it and the percent-decoded path segments (dot segments resolved, empty ones dropped).
// A URL of another scheme, which the tool refuses before the chain runs, is not parsed here.
func githubURLParts(raw string) (host string, segments []string, ok bool) {
	s := strings.TrimFunc(raw, func(r rune) bool { return r <= 0x20 })
	s = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, s)
	m := schemeRe.FindStringSubmatch(s)
	if m == nil {
		return "", nil, false
	}
	scheme := strings.ToLower(m[1])
	if scheme != "http" && scheme != "https" {
		return "", nil, false
	}
	rest := s[len(m[0]):]
	head, tail := rest, ""
	if cut := strings.IndexAny(rest, "?#"); cut >= 0 {
		head, tail = rest[:cut], rest[cut:]
	}
	head = strings.ReplaceAll(head, "\\", "/")
	u, err := parseJSURL(scheme + ":" + head + tail)
	if err != nil {
		return "", nil, false
	}
	authAndPath := strings.TrimLeft(head, "/")
	path := ""
	if i := strings.Index(authAndPath, "/"); i >= 0 {
		path = authAndPath[i:]
	}
	var parts []string
	if path != "" {
		raws := strings.Split(path, "/")[1:]
		for i, seg := range raws {
			last := i == len(raws)-1
			switch strings.ToLower(seg) {
			case ".", "%2e":
				if last {
					parts = append(parts, "")
				}
			case "..", ".%2e", "%2e.", "%2e%2e":
				if len(parts) > 0 {
					parts = parts[:len(parts)-1]
				}
				if last {
					parts = append(parts, "")
				}
			default:
				parts = append(parts, seg)
			}
		}
	}
	for _, p := range parts {
		if p == "" {
			continue
		}
		if d, ok := decodeURIComponent(p); ok {
			p = d
		}
		segments = append(segments, p)
	}
	return u.Hostname, segments, true
}

// parseGitHubURL is parseGitHubUrl: nil unless the URL is a repository root, or a blob or tree URL, of
// github.com or www.github.com. upstream: github.ts:128-160.
func parseGitHubURL(raw string) *githubURLInfo {
	host, segments, ok := githubURLParts(raw)
	if !ok || (host != "github.com" && host != "www.github.com") {
		return nil
	}
	if len(segments) < 2 {
		return nil
	}
	owner := segments[0]
	repo := strings.TrimSuffix(segments[1], ".git")
	if len(segments) > 2 && githubNonCodeSegments[strings.ToLower(segments[2])] {
		return nil
	}
	if len(segments) == 2 {
		return &githubURLInfo{Owner: owner, Repo: repo, Type: "root"}
	}
	action := segments[2]
	if action != "blob" && action != "tree" {
		return nil
	}
	if len(segments) < 4 {
		return nil
	}
	ref := segments[3]
	return &githubURLInfo{Owner: owner, Repo: repo, Ref: ref, RefIsFullSha: fullSHARe.MatchString(ref), Path: strings.Join(segments[4:], "/"), Type: action}
}

// ---- options -----------------------------------------------------------------------------------

// githubUserOptions is GitHubInterceptorOptions: the `interceptors.github` object, each field optional.
type githubUserOptions struct {
	Enabled             *bool
	MaxRepoSizeMB       *float64
	CloneTimeoutSeconds *float64
	ClonePath           *string
}

// resolvedGitHubOptions is ResolvedGitHubOptions.
type resolvedGitHubOptions struct {
	Enabled             bool
	MaxRepoSizeMB       float64
	CloneTimeoutSeconds float64
	ClonePath           string
}

// githubDefaults is DEFAULTS. upstream: github.ts:180-185.
var githubDefaults = resolvedGitHubOptions{MaxRepoSizeMB: 350, CloneTimeoutSeconds: 30, ClonePath: filepath.Join(os.TempDir(), "pi-github-repos")}

// resolveGitHubOptions is the two-tier opt-in: user is nil (unset), a bool or a githubUserOptions, consumer
// the embedding program's default (nil: none). The user's setting wins; the object form implies opt-in.
// upstream: github.ts:194-215.
func resolveGitHubOptions(user any, consumer *bool) resolvedGitHubOptions {
	switch u := user.(type) {
	case bool:
		r := githubDefaults
		r.Enabled = u
		return r
	case githubUserOptions:
		r := githubDefaults
		r.Enabled = true
		if u.Enabled != nil {
			r.Enabled = *u.Enabled
		}
		if u.MaxRepoSizeMB != nil {
			r.MaxRepoSizeMB = *u.MaxRepoSizeMB
		}
		if u.CloneTimeoutSeconds != nil {
			r.CloneTimeoutSeconds = *u.CloneTimeoutSeconds
		}
		if u.ClonePath != nil {
			r.ClonePath = *u.ClonePath
		}
		return r
	}
	r := githubDefaults
	r.Enabled = consumer != nil && *consumer
	return r
}

// readUserGitHubConfig is `readConfig().interceptors?.github`: nil, a bool or a githubUserOptions. The config
// reader has already dropped a wrong-typed value. upstream: github.ts:224-226.
func readUserGitHubConfig(cfg webToolsConfig) any {
	im, _ := cfg["interceptors"].(map[string]any)
	switch g := im["github"].(type) {
	case bool:
		return g
	case map[string]any:
		var o githubUserOptions
		if v, ok := g["enabled"].(bool); ok {
			o.Enabled = &v
		}
		if v, ok := g["maxRepoSizeMB"].(float64); ok {
			o.MaxRepoSizeMB = &v
		}
		if v, ok := g["cloneTimeoutSeconds"].(float64); ok {
			o.CloneTimeoutSeconds = &v
		}
		if v, ok := g["clonePath"].(string); ok {
			o.ClonePath = &v
		}
		return o
	}
	return nil
}

func (r resolvedGitHubOptions) asUser() githubUserOptions {
	return githubUserOptions{Enabled: &r.Enabled, MaxRepoSizeMB: &r.MaxRepoSizeMB, CloneTimeoutSeconds: &r.CloneTimeoutSeconds, ClonePath: &r.ClonePath}
}

// ---- the interceptor ---------------------------------------------------------------------------

// clonePromise is the pending result of a clone: path and ok are valid once done is closed.
type clonePromise struct {
	done chan struct{}
	path string
	ok   bool
}

func newClonePromise() *clonePromise { return &clonePromise{done: make(chan struct{})} }

func (p *clonePromise) resolve(path string, ok bool) {
	p.path, p.ok = path, ok
	close(p.done)
}

type cachedClone struct {
	localPath string
	promise   *clonePromise
}

// githubInterceptor is GitHubInterceptor. It keeps the clone cache and the `gh` probe across requests and is
// safe for concurrent requests: a second request for a repository being cloned waits for that clone.
type githubInterceptor struct {
	options resolvedGitHubOptions

	mu          sync.Mutex
	ghAvailable *bool
	ghHintShown bool
	cache       map[string]*cachedClone
}

func newGitHubInterceptor(o githubUserOptions) *githubInterceptor {
	return &githubInterceptor{options: resolveGitHubOptions(o, nil), cache: map[string]*cachedClone{}}
}

func (g *githubInterceptor) name() string { return "github" }

func (g *githubInterceptor) intercept(ctx context.Context, url string, raw bool) *fetchResponse {
	return g.fetchGitHub(ctx, url, false)
}

func githubCacheKey(owner, repo, ref string) string {
	if ref != "" {
		return owner + "/" + repo + "@" + ref
	}
	return owner + "/" + repo
}

func githubCloneDir(clonePath, owner, repo, ref string) string {
	dirName := repo
	if ref != "" {
		dirName = repo + "@" + ref
	}
	return filepath.Join(clonePath, owner, dirName)
}

// seedCache puts an entry into the clone cache; a nil result is a clone that failed. For tests only.
func (g *githubInterceptor) seedCache(key, localPath string, result *string) {
	p := newClonePromise()
	if result != nil {
		p.resolve(*result, true)
	} else {
		p.resolve("", false)
	}
	g.mu.Lock()
	g.cache[key] = &cachedClone{localPath: localPath, promise: p}
	g.mu.Unlock()
}

func (g *githubInterceptor) cacheKeys() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	keys := make([]string, 0, len(g.cache))
	for k := range g.cache {
		keys = append(keys, k)
	}
	return keys
}

// reset clears the clone cache, removes every cloned directory and forgets the `gh` probe.
func (g *githubInterceptor) reset() {
	g.mu.Lock()
	entries := g.cache
	g.cache = map[string]*cachedClone{}
	g.ghAvailable = nil
	g.ghHintShown = false
	g.mu.Unlock()
	for _, e := range entries {
		os.RemoveAll(e.localPath)
	}
}

func (g *githubInterceptor) cached(key string) *cachedClone {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cache[key]
}

// checkGhAvailable runs `gh --version` once and remembers the answer. A probe that failed only because the
// request was cancelled is not remembered.
func (g *githubInterceptor) checkGhAvailable(ctx context.Context) bool {
	g.mu.Lock()
	if g.ghAvailable != nil {
		v := *g.ghAvailable
		g.mu.Unlock()
		return v
	}
	g.mu.Unlock()
	_, err := runExec(ctx, "gh", []string{"--version"}, execOptions{Timeout: 5 * time.Second})
	avail := err == nil
	if !avail && ctx.Err() != nil {
		return false
	}
	g.mu.Lock()
	g.ghAvailable = &avail
	g.mu.Unlock()
	return avail
}

// ghAPI runs `gh api <path> --jq <filter>` when gh is available.
func (g *githubInterceptor) ghAPI(ctx context.Context, path, filter string, timeout time.Duration, maxBuffer int) (string, bool) {
	if !g.checkGhAvailable(ctx) {
		return "", false
	}
	out, err := runExec(ctx, "gh", []string{"api", path, "--jq", filter}, execOptions{Timeout: timeout, MaxBuffer: maxBuffer})
	if err != nil {
		return "", false
	}
	return out, true
}

// checkRepoSize is the repository's size in KB from the API; ok is false when it is unknown.
func (g *githubInterceptor) checkRepoSize(ctx context.Context, owner, repo string) (float64, bool) {
	out, ok := g.ghAPI(ctx, "repos/"+owner+"/"+repo, ".size", 10*time.Second, 0)
	if !ok {
		return 0, false
	}
	return jsParseInt10(jsTrim(out))
}

func (g *githubInterceptor) showGhHint() {
	g.mu.Lock()
	shown := g.ghHintShown
	g.ghHintShown = true
	g.mu.Unlock()
	if !shown {
		io.WriteString(ghHintWriter, githubHint)
	}
}

func (g *githubInterceptor) getDefaultBranch(ctx context.Context, owner, repo string) (string, bool) {
	out, ok := g.ghAPI(ctx, "repos/"+owner+"/"+repo, ".default_branch", 10*time.Second, 0)
	if !ok {
		return "", false
	}
	b := jsTrim(out)
	return b, b != ""
}

func (g *githubInterceptor) fetchTreeViaAPI(ctx context.Context, owner, repo, ref string) (string, bool) {
	out, ok := g.ghAPI(ctx, "repos/"+owner+"/"+repo+"/git/trees/"+ref+"?recursive=1", ".tree[].path", 15*time.Second, 5*1024*1024)
	if !ok {
		return "", false
	}
	var paths []string
	for _, p := range strings.Split(jsTrim(out), "\n") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return "", false
	}
	if len(paths) > githubMaxTreeEntries {
		return strings.Join(paths[:githubMaxTreeEntries], "\n") + fmt.Sprintf("\n... (%d total entries)", len(paths)), true
	}
	return strings.Join(paths, "\n"), true
}

func truncateREADME(s string) string {
	if utf16Len(s) > githubReadmeChars {
		return utf16Prefix(s, githubReadmeChars) + "\n\n[README truncated at 8K chars]"
	}
	return s
}

func (g *githubInterceptor) fetchReadmeViaAPI(ctx context.Context, owner, repo, ref string) (string, bool) {
	out, ok := g.ghAPI(ctx, "repos/"+owner+"/"+repo+"/readme?ref="+ref, ".content", 10*time.Second, 0)
	if !ok {
		return "", false
	}
	return truncateREADME(decodeUTF8Lossy(decodeBase64Lenient(jsTrim(out)))), true
}

func (g *githubInterceptor) fetchFileViaAPI(ctx context.Context, owner, repo, path, ref string) (string, bool) {
	out, ok := g.ghAPI(ctx, "repos/"+owner+"/"+repo+"/contents/"+path+"?ref="+ref, ".content", 10*time.Second, 2*1024*1024)
	if !ok {
		return "", false
	}
	return decodeUTF8Lossy(decodeBase64Lenient(jsTrim(out))), true
}

// truncateInline adds content to lines, cut at 100K UTF-16 units with the given notice.
func truncateInline(lines []string, content, notice string) []string {
	if utf16Len(content) > githubMaxInlineFileChars {
		return append(lines, utf16Prefix(content, githubMaxInlineFileChars), notice)
	}
	return append(lines, content)
}

// fetchViaAPI is the API-only view. upstream: github.ts:337-392.
func (g *githubInterceptor) fetchViaAPI(ctx context.Context, owner, repo string, info *githubURLInfo, sizeNote string) *fetchResponse {
	ref := info.Ref
	if ref == "" {
		var ok bool
		if ref, ok = g.getDefaultBranch(ctx, owner, repo); !ok {
			return nil
		}
	}
	var lines []string
	if sizeNote != "" {
		lines = append(lines, sizeNote, "")
	}
	if info.Type == "blob" && info.Path != "" {
		content, ok := g.fetchFileViaAPI(ctx, owner, repo, info.Path, ref)
		if !ok || content == "" {
			return nil
		}
		lines = append(lines, "## "+info.Path)
		lines = truncateInline(lines, content, "\n[File truncated at 100K chars]")
		return &fetchResponse{Text: strings.Join(lines, "\n"), Title: owner + "/" + repo + " - " + info.Path, ContentType: "text/plain"}
	}
	var tree, readme string
	var treeOK, readmeOK bool
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); tree, treeOK = g.fetchTreeViaAPI(ctx, owner, repo, ref) }()
	go func() { defer wg.Done(); readme, readmeOK = g.fetchReadmeViaAPI(ctx, owner, repo, ref) }()
	wg.Wait()
	treeOK = treeOK && tree != ""
	readmeOK = readmeOK && readme != ""
	if !treeOK && !readmeOK {
		return nil
	}
	if treeOK {
		lines = append(lines, "## Structure", tree, "")
	}
	if readmeOK {
		lines = append(lines, "## README.md", readme, "")
	}
	lines = append(lines, "This is an API-only view. Clone the repo or use `read`/`bash` for deeper exploration.")
	title := owner + "/" + repo
	if info.Path != "" {
		title += " - " + info.Path
	}
	return &fetchResponse{Text: strings.Join(lines, "\n"), Title: title, ContentType: "text/plain"}
}

// execClone runs one clone command; a failure removes what it left. upstream: github.ts:394-420.
func (g *githubInterceptor) execClone(ctx context.Context, name string, args []string, localPath string, timeout time.Duration) (string, bool) {
	if _, err := runExec(ctx, name, args, execOptions{Timeout: timeout}); err != nil {
		os.RemoveAll(localPath)
		return "", false
	}
	return localPath, true
}

func (g *githubInterceptor) cloneRepo(ctx context.Context, owner, repo, ref string) (string, bool) {
	localPath := githubCloneDir(g.options.ClonePath, owner, repo, ref)
	os.RemoveAll(localPath)
	timeout := time.Duration(g.options.CloneTimeoutSeconds * float64(time.Second))
	if g.checkGhAvailable(ctx) {
		args := []string{"repo", "clone", owner + "/" + repo, localPath, "--", "--depth", "1", "--single-branch"}
		if ref != "" {
			args = append(args, "--branch", ref)
		}
		return g.execClone(ctx, "gh", args, localPath, timeout)
	}
	g.showGhHint()
	args := []string{"clone", "--depth", "1", "--single-branch"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, "https://github.com/"+owner+"/"+repo+".git", localPath)
	return g.execClone(ctx, "git", args, localPath, timeout)
}

func cloneResponse(localPath string, info *githubURLInfo) *fetchResponse {
	title := info.Owner + "/" + info.Repo
	if info.Path != "" {
		title += " - " + info.Path
	}
	return &fetchResponse{Text: generateCloneContent(localPath, info), Title: title, ContentType: "text/plain"}
}

func (g *githubInterceptor) awaitCachedClone(ctx context.Context, c *cachedClone, info *githubURLInfo) *fetchResponse {
	if ctx.Err() != nil {
		return nil
	}
	select {
	case <-c.promise.done:
	case <-ctx.Done():
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	if c.promise.ok {
		return cloneResponse(c.promise.path, info)
	}
	return g.fetchViaAPI(ctx, info.Owner, info.Repo, info, "")
}

// fetchGitHub is the interceptor's whole flow. upstream: github.ts:441-520.
func (g *githubInterceptor) fetchGitHub(ctx context.Context, url string, forceClone bool) *fetchResponse {
	info := parseGitHubURL(url)
	if info == nil || ctx.Err() != nil || !g.options.Enabled {
		return nil
	}
	owner, repo := info.Owner, info.Repo
	key := githubCacheKey(owner, repo, info.Ref)
	if c := g.cached(key); c != nil {
		return g.awaitCachedClone(ctx, c, info)
	}
	if info.RefIsFullSha {
		if ctx.Err() != nil {
			return nil
		}
		return g.fetchViaAPI(ctx, owner, repo, info, "Note: Commit SHA URLs use the GitHub API instead of cloning.")
	}
	if !forceClone {
		kb, ok := g.checkRepoSize(ctx, owner, repo)
		if ctx.Err() != nil {
			return nil
		}
		if ok {
			sizeMB := kb / 1024
			if sizeMB > g.options.MaxRepoSizeMB {
				note := fmt.Sprintf("Note: Repository is %sMB (threshold: %sMB). ", strconv.FormatFloat(jsRound(sizeMB), 'f', -1, 64), strconv.FormatFloat(g.options.MaxRepoSizeMB, 'f', -1, 64)) +
					"Showing API-fetched content instead of full clone. Ask the user if they'd like to clone the full repo — " +
					"if yes, call web_fetch again with the same URL."
				return g.fetchViaAPI(ctx, owner, repo, info, note)
			}
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	promise := newClonePromise()
	g.mu.Lock()
	if c := g.cache[key]; c != nil {
		g.mu.Unlock()
		return g.awaitCachedClone(ctx, c, info)
	}
	g.cache[key] = &cachedClone{localPath: githubCloneDir(g.options.ClonePath, owner, repo, info.Ref), promise: promise}
	g.mu.Unlock()

	path, ok := g.cloneRepo(ctx, owner, repo, info.Ref)
	promise.resolve(path, ok)
	if ctx.Err() != nil {
		if !ok {
			g.dropCache(key, promise)
		}
		return nil
	}
	if !ok {
		g.dropCache(key, promise)
		return g.fetchViaAPI(ctx, owner, repo, info, "")
	}
	return cloneResponse(path, info)
}

// dropCache removes the entry of a clone that failed, unless another clone has replaced it.
func (g *githubInterceptor) dropCache(key string, p *clonePromise) {
	g.mu.Lock()
	if c := g.cache[key]; c != nil && c.promise == p {
		delete(g.cache, key)
	}
	g.mu.Unlock()
}

// ---- local clone content -----------------------------------------------------------------------

func isBinaryFile(filePath string) bool {
	if githubBinaryExtensions[strings.ToLower(posixExtname(filepath.ToSlash(filePath)))] {
		return true
	}
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	return slices.Contains(buf[:n], 0)
}

// resolveWithinRepo is the candidate path for rel inside root, or false when it leaves the root, also through
// a symlink. upstream: github.ts:640-660.
func resolveWithinRepo(rootPath, rel string) (string, bool) {
	root, err := filepath.Abs(rootPath)
	if err != nil {
		return "", false
	}
	var candidate string
	if filepath.IsAbs(rel) {
		candidate = filepath.Clean(rel)
	} else {
		candidate = filepath.Join(root, rel)
	}
	sep := string(os.PathSeparator)
	if candidate != root {
		prefix := root
		if !strings.HasSuffix(prefix, sep) {
			prefix += sep
		}
		if !strings.HasPrefix(candidate, prefix) {
			return "", false
		}
	}
	if _, err := os.Stat(candidate); err != nil {
		return candidate, true
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	realCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false
	}
	if realCandidate == realRoot {
		return candidate, true
	}
	realPrefix := realRoot
	if !strings.HasSuffix(realPrefix, sep) {
		realPrefix += sep
	}
	if strings.HasPrefix(realCandidate, realPrefix) {
		return candidate, true
	}
	return "", false
}

// readDirSorted is readdirSync(dir).sort().
func readDirSorted(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(ents))
	for i, e := range ents {
		names[i] = e.Name()
	}
	slices.SortFunc(names, compareUTF16)
	return names, nil
}

func buildTree(rootPath string) string {
	var entries []string
	var walk func(dir, relPath string)
	walk = func(dir, relPath string) {
		if len(entries) >= githubMaxTreeEntries {
			return
		}
		items, err := readDirSorted(dir)
		if err != nil {
			return
		}
		for _, item := range items {
			if len(entries) >= githubMaxTreeEntries {
				return
			}
			if item == ".git" {
				continue
			}
			rel := item
			if relPath != "" {
				rel = relPath + "/" + item
			}
			safePath, ok := resolveWithinRepo(rootPath, rel)
			if !ok {
				entries = append(entries, rel+"  [outside repo skipped]")
				continue
			}
			st, err := os.Stat(safePath)
			if err != nil {
				continue
			}
			if st.IsDir() {
				if githubNoiseDirs[item] {
					entries = append(entries, rel+"/  [skipped]")
					continue
				}
				entries = append(entries, rel+"/")
				walk(safePath, rel)
			} else {
				entries = append(entries, rel)
			}
		}
	}
	walk(rootPath, "")
	if len(entries) >= githubMaxTreeEntries {
		entries = append(entries, fmt.Sprintf("... (truncated at %d entries)", githubMaxTreeEntries))
	}
	return strings.Join(entries, "\n")
}

func buildDirListing(rootPath, subPath string) string {
	target, ok := resolveWithinRepo(rootPath, subPath)
	if !ok {
		return "(path escapes repository root)"
	}
	items, err := readDirSorted(target)
	if err != nil {
		return "(directory not readable)"
	}
	var lines []string
	for _, item := range items {
		if item == ".git" {
			continue
		}
		rel := item
		if subPath != "" {
			rel = subPath + "/" + item
		}
		safePath, ok := resolveWithinRepo(rootPath, rel)
		if !ok {
			lines = append(lines, "  "+item+"  (outside repo)")
			continue
		}
		st, err := os.Stat(safePath)
		switch {
		case err != nil:
			lines = append(lines, "  "+item+"  (unreadable)")
		case st.IsDir():
			lines = append(lines, "  "+item+"/")
		default:
			lines = append(lines, "  "+item+"  ("+formatFileSize(st.Size())+")")
		}
	}
	return strings.Join(lines, "\n")
}

func readReadme(localPath string) (string, bool) {
	for _, name := range []string{"README.md", "readme.md", "README", "README.txt", "README.rst"} {
		p := filepath.Join(localPath, name)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		return truncateREADME(decodeUTF8Lossy(data)), true
	}
	return "", false
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// generateCloneContent renders a clone for the URL: the tree and README, a directory listing or a file.
// upstream: github.ts:757-890.
func generateCloneContent(localPath string, info *githubURLInfo) string {
	lines := []string{"Repository cloned to: " + localPath, ""}
	if info.Type == "root" {
		lines = append(lines, "## Structure", buildTree(localPath), "")
		if readme, ok := readReadme(localPath); ok && readme != "" {
			lines = append(lines, "## README.md", readme, "")
		}
		lines = append(lines, githubExploreHint)
		return strings.Join(lines, "\n")
	}
	if info.Type == "tree" {
		dirPath := info.Path
		full, ok := resolveWithinRepo(localPath, dirPath)
		if !ok || !fileExists(full) {
			lines = append(lines, "Path `"+dirPath+"` not found in clone. Showing repository root instead.", "", "## Structure", buildTree(localPath))
		} else {
			title := dirPath
			if title == "" {
				title = "/"
			}
			lines = append(lines, "## "+title, buildDirListing(localPath, dirPath))
		}
		lines = append(lines, "", githubExploreHint)
		return strings.Join(lines, "\n")
	}
	filePath := info.Path
	full, ok := resolveWithinRepo(localPath, filePath)
	if !ok || !fileExists(full) {
		lines = append(lines, "Path `"+filePath+"` not found in clone. Showing repository root instead.", "", "## Structure", buildTree(localPath), "", githubExploreHint)
		return strings.Join(lines, "\n")
	}
	st, err := os.Stat(full)
	if err != nil {
		lines = append(lines, "Could not inspect `"+filePath+"`: "+err.Error(), "", githubExploreHint)
		return strings.Join(lines, "\n")
	}
	if st.IsDir() {
		title := filePath
		if title == "" {
			title = "/"
		}
		lines = append(lines, "## "+title, buildDirListing(localPath, filePath), "", githubExploreHint)
		return strings.Join(lines, "\n")
	}
	if isBinaryFile(full) {
		ext := strings.Replace(posixExtname(filePath), ".", "", 1)
		lines = append(lines, "## "+filePath, "Binary file ("+ext+", "+formatFileSize(st.Size())+"). Use `read` or `bash` tools at the path above to inspect.")
		return strings.Join(lines, "\n")
	}
	data, err := os.ReadFile(full)
	if err != nil {
		lines = append(lines, "Could not read `"+filePath+"` as UTF-8 text.", "", githubExploreHint)
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "## "+filePath)
	lines = truncateInline(lines, decodeUTF8Lossy(data), "\n[File truncated at 100K chars. Full file: "+full+"]")
	lines = append(lines, "", githubExploreHint)
	return strings.Join(lines, "\n")
}

