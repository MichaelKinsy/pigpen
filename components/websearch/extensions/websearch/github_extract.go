package websearch

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// Port of github-extract.ts: a repository, directory or file URL of github.com is cloned (gh, else
// git) into a private runtime directory and shown as a tree, a README, a directory listing or a
// file; a repository over the size threshold, a commit SHA and a failed clone fall back to the
// `gh api` view of github_api.go. Upstream line numbers are cited where a branch is not obvious.

var binaryExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true, ".ico": true, ".webp": true, ".svg": true, ".tiff": true, ".tif": true,
	".mp3": true, ".mp4": true, ".avi": true, ".mov": true, ".mkv": true, ".flv": true, ".wmv": true, ".wav": true, ".ogg": true, ".webm": true, ".flac": true, ".aac": true,
	".zip": true, ".tar": true, ".gz": true, ".bz2": true, ".xz": true, ".7z": true, ".rar": true, ".zst": true,
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".bin": true, ".o": true, ".a": true, ".lib": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
	".sqlite": true, ".db": true, ".sqlite3": true,
	".pyc": true, ".pyo": true, ".class": true, ".jar": true, ".war": true,
	".iso": true, ".img": true, ".dmg": true,
}

var noiseDirs = map[string]bool{
	"node_modules": true, "vendor": true, ".next": true, "dist": true, "build": true, "__pycache__": true,
	".venv": true, "venv": true, ".tox": true, ".mypy_cache": true, ".pytest_cache": true,
	"target": true, ".gradle": true, ".idea": true, ".vscode": true,
}

const (
	cloneRuntimeOwnerFile     = ".owner.json"
	maxCloneRuntimeOwnerBytes = 1024
	readmeMaxChars            = 8192
	// readPrefixBytesPerUnit bounds the bytes that hold n UTF-16 units: at most three bytes each.
	readPrefixBytesPerUnit = 3
	defaultClonePath       = "/tmp/pi-github-repos"
	// maxTimerMs is the largest delay Node's setTimeout honours (2^31 - 1).
	maxTimerMs = 2147483647
)

var (
	ghOwnerRE   = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
	ghRepoRE    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	ghSHARE     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	hex64RE     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	digitsRE    = regexp.MustCompile(`^\d+$`)
	envVarRE    = regexp.MustCompile(`(?i)\$([A-Z_][A-Z0-9_]*)`)
	ghRefBadRE  = regexp.MustCompile(`[\x00-\x1f\x7f]`)
	ghNonCode   = map[string]bool{}
	ghNonCodeIn = []string{"issues", "pull", "pulls", "discussions", "releases", "wiki", "actions", "settings", "security", "projects", "graphs", "compare", "commits", "tags", "branches", "stargazers", "watchers", "network", "forks", "milestone", "labels", "packages", "codespaces", "contribute", "community", "sponsors", "invitations", "notifications", "insights"}
)

func init() {
	for _, s := range ghNonCodeIn {
		ghNonCode[s] = true
	}
}

// githubURL is GitHubUrlInfo: Ref is empty for a repository root, Path for a URL without one.
type githubURL struct {
	Owner, Repo, Ref string
	RefIsFullSHA     bool
	Path, Type       string
}

// githubSegments returns the decoded path segments of a github.com URL, or nil when the URL is not
// one or a segment is not valid percent-encoded UTF-8 (decodeURIComponent throws).
func githubSegments(raw string) []string {
	u, err := ParseURL(raw, nil)
	if err != nil {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return nil
	}
	var segments []string
	for _, s := range strings.Split(u.EscapedPath(), "/") {
		if s == "" {
			continue
		}
		d, err := url.PathUnescape(s)
		if err != nil || !utf8.ValidString(d) {
			return nil
		}
		segments = append(segments, d)
	}
	return segments
}

// validGitHubName is the owner and repository check shared with github-issue-pr.ts.
func validGitHubName(owner, repo string) bool {
	return ghOwnerRE.MatchString(owner) && !strings.Contains(owner, "--") && ghRepoRE.MatchString(repo) && repo != "." && repo != ".."
}

// parseGitHubURL is parseGitHubUrl (github-extract.ts:149): a repository root, blob or tree URL.
func parseGitHubURL(raw string) *githubURL {
	segments := githubSegments(raw)
	if len(segments) < 2 {
		return nil
	}
	owner, repo := segments[0], strings.TrimSuffix(segments[1], ".git")
	if !validGitHubName(owner, repo) {
		return nil
	}
	if len(segments) > 2 && ghNonCode[strings.ToLower(segments[2])] {
		return nil
	}
	if len(segments) == 2 {
		return &githubURL{Owner: owner, Repo: repo, Type: "root"}
	}
	action := segments[2]
	if action != "blob" && action != "tree" || len(segments) < 4 {
		return nil
	}
	ref := segments[3]
	if ref == "" || jsLen(ref) > 1024 || ghRefBadRE.MatchString(ref) {
		return nil
	}
	return &githubURL{Owner: owner, Repo: repo, Ref: ref, RefIsFullSHA: ghSHARE.MatchString(ref), Path: strings.Join(segments[4:], "/"), Type: action}
}

type githubCloneConfig struct {
	Enabled             bool
	MaxRepoSizeMB       float64
	CloneTimeoutSeconds float64
	ClonePath           string
}

func positiveNumber(v any, fallback float64) float64 {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
		return fallback
	}
	return f
}

// expandPath expands a leading ~ and $NAME references (github-extract.ts:86).
func expandPath(value string) string {
	expanded := value
	if strings.HasPrefix(expanded, "~/") || expanded == "~" {
		home := os.Getenv("HOME")
		if home == "" {
			home = os.Getenv("USERPROFILE")
		}
		expanded = home + expanded[1:]
	}
	return envVarRE.ReplaceAllStringFunc(expanded, func(m string) string {
		if v, ok := os.LookupEnv(m[1:]); ok {
			return v
		}
		return m
	})
}

// normalizeClonePath is a trimmed non-empty string with ~ and $NAME expanded, else the fallback.
func normalizeClonePath(value any, fallback string) string {
	s, ok := value.(string)
	if !ok {
		return fallback
	}
	s = jsTrim(s)
	if s == "" {
		return fallback
	}
	return expandPath(s)
}

type cloneDestination struct{ rootPath, localPath string }

type cloneRuntime struct{ parentPath, rootPath string }

// cachedClone is a clone in flight or done; path is "" when it failed and is set before done closes.
type cachedClone struct {
	dest cloneDestination
	done chan struct{}
	path string
}

// githubState is the module state of github-extract.ts, github-api.ts and github-issue-pr.ts of
// one process: the clone cache, the settings, the runtime directory, whether gh works. The original
// keeps it in module variables; the extension has one (defaultGitHub) and the tests make more to
// stand for more processes.
type githubState struct {
	mu sync.Mutex
	// pid is the id the runtime's owner file records (os.Getpid unless a test stands for another process).
	pid int
	// beforeSweep, when set, runs in the background cleanup before it reads the clone directory (a test gate).
	beforeSweep func()
	// sweeps counts the background cleanups.
	sweeps sync.WaitGroup
	// hintOut receives the one-time gh hint (os.Stderr unless set).
	hintOut io.Writer

	cache       map[string]*cachedClone
	cleanups    map[string]bool
	config      *githubCloneConfig
	runtime     *cloneRuntime
	ghAvailable *bool
	ghHintShown bool
	prConfig    *githubPRConfig
}

func newGitHubState() *githubState {
	return &githubState{cache: map[string]*cachedClone{}, cleanups: map[string]bool{}}
}

var defaultGitHubState atomic.Pointer[githubState]

func init() { defaultGitHubState.Store(newGitHubState()) }

// defaultGitHub is the extension's state.
func defaultGitHub() *githubState { return defaultGitHubState.Load() }

// resetGitHubState gives the extension a fresh state (tests, ResetCaches); the old one's clones go.
func resetGitHubState() {
	old := defaultGitHubState.Swap(newGitHubState())
	if old != nil {
		old.clearCloneCache()
		old.sweeps.Wait()
	}
}

// cloneConfig is loadGitHubConfig (github-extract.ts:106): memoized until clearCloneCache; an
// unreadable file is an error and nothing is memoized.
func (s *githubState) cloneConfig() (githubCloneConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config != nil {
		return *s.config, nil
	}
	cfg := githubCloneConfig{Enabled: true, MaxRepoSizeMB: 350, CloneTimeoutSeconds: 30, ClonePath: defaultClonePath}
	path := ConfigPath()
	if _, err := os.Stat(path); err != nil {
		s.config = &cfg
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return cfg, fmt.Errorf("Failed to parse %s: %s", path, jsonErrorText(err))
	}
	var gc map[string]any
	switch root := raw.(type) {
	case nil:
		return cfg, errors.New("Cannot read properties of null (reading 'githubClone')")
	case map[string]any:
		gc, _ = root["githubClone"].(map[string]any)
	}
	if v, ok := gc["enabled"].(bool); ok {
		cfg.Enabled = v
	}
	cfg.MaxRepoSizeMB = positiveNumber(gc["maxRepoSizeMB"], cfg.MaxRepoSizeMB)
	cfg.CloneTimeoutSeconds = positiveNumber(gc["cloneTimeoutSeconds"], cfg.CloneTimeoutSeconds)
	cfg.ClonePath = normalizeClonePath(gc["clonePath"], cfg.ClonePath)
	s.config = &cfg
	return cfg, nil
}

func cloneCacheKey(owner, repo, ref string) string {
	if ref != "" {
		return owner + "/" + repo + "@" + ref
	}
	return owner + "/" + repo
}

func resolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return abs
}

// ---- the clone runtime and its owner file -------------------------------------------------

type cloneRuntimeOwner struct {
	Version   int    `json:"version"`
	PID       int    `json:"pid"`
	Platform  string `json:"platform"`
	BootID    string `json:"bootId,omitempty"`
	StartTime string `json:"startTime,omitempty"`
}

type linuxProcessInfo struct {
	state     string
	startTime string
	dead      bool
}

func readLinuxBootID() string {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return jsTrim(string(b))
}

func parseLinuxProcessInfo(stat string) *linuxProcessInfo {
	i := strings.LastIndex(stat, ") ")
	if i < 0 {
		return nil
	}
	fields := strings.Fields(stat[i+2:])
	if len(fields) < 20 || fields[0] == "" || !digitsRE.MatchString(fields[19]) {
		return nil
	}
	return &linuxProcessInfo{state: fields[0], startTime: fields[19]}
}

// readLinuxProcessInfo reads /proc/<pid>/stat: a process that does not exist is dead, an unreadable
// one is unknown (nil).
func readLinuxProcessInfo(pid int) *linuxProcessInfo {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &linuxProcessInfo{dead: true}
		}
		return nil
	}
	return parseLinuxProcessInfo(string(b))
}

func (s *githubState) currentOwner() cloneRuntimeOwner {
	pid := s.pid
	if pid == 0 {
		pid = os.Getpid()
	}
	owner := cloneRuntimeOwner{Version: 1, PID: pid, Platform: nodePlatform()}
	if runtime.GOOS == "linux" {
		bootID := readLinuxBootID()
		if info := readLinuxProcessInfo(pid); bootID != "" && info != nil && info.startTime != "" {
			owner.BootID, owner.StartTime = bootID, info.startTime
		}
	}
	return owner
}

func (s *githubState) writeCloneRuntimeOwner(runtimePath string) error {
	b, err := json.Marshal(s.currentOwner())
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(runtimePath, cloneRuntimeOwnerFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// parseCloneRuntimeOwner validates an owner file (github-extract.ts:290); nil when it is not one.
func parseCloneRuntimeOwner(text string) *cloneRuntimeOwner {
	var record map[string]any
	if err := json.Unmarshal([]byte(text), &record); err != nil || record == nil {
		return nil
	}
	if v, ok := record["version"].(float64); !ok || v != 1 {
		return nil
	}
	pid, ok := record["pid"].(float64)
	if !ok || pid != math.Trunc(pid) || pid <= 0 || pid > 1<<53-1 {
		return nil
	}
	platform, ok := record["platform"].(string)
	if !ok || platform == "" || jsLen(platform) > 32 {
		return nil
	}
	owner := &cloneRuntimeOwner{Version: 1, PID: int(pid), Platform: platform}
	if v, present := record["bootId"]; present {
		s, ok := v.(string)
		if !ok || s == "" || jsLen(s) > 256 {
			return nil
		}
		owner.BootID = s
	}
	if v, present := record["startTime"]; present {
		s, ok := v.(string)
		if !ok || !digitsRE.MatchString(s) {
			return nil
		}
		owner.StartTime = s
	}
	return owner
}

// readCloneRuntimeOwner reads the owner file of a runtime directory: a regular file of at most 1 KiB,
// opened without following a symlink.
func readCloneRuntimeOwner(runtimePath string) *cloneRuntimeOwner {
	path := filepath.Join(runtimePath, cloneRuntimeOwnerFile)
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxCloneRuntimeOwnerBytes {
		return nil
	}
	f, err := openNoFollow(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Size() > maxCloneRuntimeOwnerBytes {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(f, maxCloneRuntimeOwnerBytes+1))
	if err != nil {
		return nil
	}
	return parseCloneRuntimeOwner(string(b))
}

// cloneRuntimeOwnerIsDead is true only on proof: another platform's owner, an owner without the
// Linux boot id and start time, or an unreadable /proc entry preserves the runtime (a PID can be reused).
func cloneRuntimeOwnerIsDead(owner *cloneRuntimeOwner, bootID string) bool {
	if owner.Platform != nodePlatform() {
		return false
	}
	if runtime.GOOS != "linux" {
		return processGone(owner.PID)
	}
	if owner.BootID == "" || owner.StartTime == "" || bootID == "" {
		return false
	}
	info := readLinuxProcessInfo(owner.PID)
	if info == nil {
		return false
	}
	if info.dead || info.state == "Z" || info.state == "X" {
		return true
	}
	return info.startTime != owner.StartTime || bootID != owner.BootID
}

// removeCloneRuntimeChecked removes a runtime directory that is a real direct child of parent.
func removeCloneRuntimeChecked(parent, runtimePath string) {
	parent, runtimePath = resolvePath(parent), resolvePath(runtimePath)
	if filepath.Dir(runtimePath) != parent || !strings.HasPrefix(filepath.Base(runtimePath), "runtime-") {
		return
	}
	real, err := filepath.EvalSymlinks(runtimePath)
	if err != nil || filepath.Dir(real) != parent || filepath.Base(real) != filepath.Base(runtimePath) {
		return
	}
	if st, err := os.Lstat(runtimePath); err != nil || st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return
	}
	_ = os.RemoveAll(runtimePath)
}

// sweepStaleCloneRuntimes removes the runtimes whose owner is provably dead. Anything else in the
// clone directory (files, ownerless or malformed or symlinked runtimes) stays.
func sweepStaleCloneRuntimes(parent string) {
	parent = resolvePath(parent)
	bootID := ""
	if runtime.GOOS == "linux" {
		bootID = readLinuxBootID()
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "runtime-") || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		runtimePath := resolvePath(filepath.Join(parent, entry.Name()))
		if st, err := os.Lstat(runtimePath); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			continue
		}
		real, err := filepath.EvalSymlinks(runtimePath)
		if err != nil || filepath.Dir(real) != parent || filepath.Base(real) != filepath.Base(runtimePath) {
			continue
		}
		owner := readCloneRuntimeOwner(runtimePath)
		if owner == nil || !cloneRuntimeOwnerIsDead(owner, bootID) {
			continue
		}
		removeCloneRuntimeChecked(parent, runtimePath)
	}
}

// startStaleCloneRuntimeCleanup runs the sweep once per clone directory, in the background: runtime
// creation never waits for it. Called with s.mu held.
func (s *githubState) startStaleCloneRuntimeCleanup(parent string) {
	parent = resolvePath(parent)
	if s.cleanups[parent] {
		return
	}
	s.cleanups[parent] = true
	s.sweeps.Add(1)
	go func() {
		defer s.sweeps.Done()
		defer func() { _ = recover() }()
		if s.beforeSweep != nil {
			s.beforeSweep()
		}
		sweepStaleCloneRuntimes(parent)
	}()
}

const tempNameChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// mkdtempRuntime is mkdtempSync(join(parent, "runtime-")): six random letters or digits, mode 0700.
func mkdtempRuntime(parent string) (string, error) {
	for range 100 {
		var b [6]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		for i := range b {
			b[i] = tempNameChars[int(b[i])%len(tempNameChars)]
		}
		path := filepath.Join(parent, "runtime-"+string(b[:]))
		err := os.Mkdir(path, 0o700)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", errors.New("could not create a unique runtime directory")
}

// cloneRuntimeRoot is getCloneRuntimeRoot (github-extract.ts:446): this process's private
// runtime directory under the real clone directory, created on first use. Called with s.mu held.
func (s *githubState) cloneRuntimeRoot(cfg githubCloneConfig) (string, bool) {
	if s.runtime != nil {
		return s.runtime.rootPath, true
	}
	var parent, runtimePath string
	fail := func() (string, bool) {
		if parent != "" && runtimePath != "" {
			removeCloneRuntimeDir(parent, runtimePath)
		}
		return "", false
	}
	configured := resolvePath(cfg.ClonePath)
	if err := os.MkdirAll(configured, 0o777); err != nil {
		return fail()
	}
	real, err := filepath.EvalSymlinks(configured)
	if err != nil {
		return fail()
	}
	parent = real
	s.startStaleCloneRuntimeCleanup(parent)
	if runtimePath, err = mkdtempRuntime(parent); err != nil {
		runtimePath = ""
		return fail()
	}
	if err := os.Chmod(runtimePath, 0o700); err != nil {
		return fail()
	}
	rootPath, err := filepath.EvalSymlinks(runtimePath)
	if err != nil {
		return fail()
	}
	if filepath.Dir(rootPath) != parent {
		removeCloneRuntimeDir(parent, runtimePath)
		return "", false
	}
	if err := s.writeCloneRuntimeOwner(rootPath); err != nil {
		return fail()
	}
	s.runtime = &cloneRuntime{parentPath: parent, rootPath: rootPath}
	return rootPath, true
}

// removeCloneRuntimeDir is removeCloneRuntime: a direct child "runtime-*" of parent, a symlink
// unlinked and a directory removed.
func removeCloneRuntimeDir(parent, runtimePath string) {
	parent, runtimePath = resolvePath(parent), resolvePath(runtimePath)
	if filepath.Dir(runtimePath) != parent || !strings.HasPrefix(filepath.Base(runtimePath), "runtime-") {
		return
	}
	st, err := os.Lstat(runtimePath)
	if err != nil {
		return
	}
	if st.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(runtimePath)
		return
	}
	_ = os.RemoveAll(runtimePath)
}

// cloneDestinationFor is cloneDestination: the clone directory named by the digest of the
// repository and ref inside the runtime. Called with s.mu held.
func (s *githubState) cloneDestinationFor(cfg githubCloneConfig, owner, repo, ref string) (cloneDestination, bool) {
	rootPath, ok := s.cloneRuntimeRoot(cfg)
	if !ok {
		return cloneDestination{}, false
	}
	var refValue any
	if ref != "" {
		refValue = ref
	}
	b, err := marshalNoEscape([]any{owner, repo, refValue})
	if err != nil {
		return cloneDestination{}, false
	}
	// JSON.stringify leaves U+2028 and U+2029 as they are; Go's encoder escapes them.
	text := strings.NewReplacer(`\u2028`, "\u2028", `\u2029`, "\u2029").Replace(string(b))
	sum := sha256.Sum256([]byte(text))
	localPath := resolvePath(filepath.Join(rootPath, hex.EncodeToString(sum[:])))
	if filepath.Dir(localPath) != rootPath {
		return cloneDestination{}, false
	}
	return cloneDestination{rootPath: rootPath, localPath: localPath}, true
}

// removeCloneDestination removes a clone directory that is a direct child named by a digest of
// its runtime (a symlink is unlinked, never followed); false when it could not be removed.
func removeCloneDestination(d cloneDestination) bool {
	root, local := resolvePath(d.rootPath), resolvePath(d.localPath)
	if filepath.Dir(local) != root || !hex64RE.MatchString(filepath.Base(local)) {
		return false
	}
	st, err := os.Lstat(local)
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return os.Remove(local) == nil
	}
	return os.RemoveAll(local) == nil
}

// cloneRepo is cloneRepo: `gh repo clone` when gh works, else `git clone`, shallow and single-branch,
// with the configured timeout. "" on any failure, after removing what the clone left.
func (s *githubState) cloneRepo(ctx context.Context, owner, repo, ref string, cfg githubCloneConfig, dest cloneDestination) string {
	if !removeCloneDestination(dest) {
		return ""
	}
	ms := math.Min(cfg.CloneTimeoutSeconds*1000, maxTimerMs)
	timeout := time.Duration(ms * float64(time.Millisecond))
	var args []string
	if s.checkGhAvailable(ctx) {
		args = []string{"gh", "repo", "clone", owner + "/" + repo, dest.localPath, "--", "--depth", "1", "--single-branch"}
		if ref != "" {
			args = append(args, "--branch", ref)
		}
	} else {
		s.showGhHint()
		args = []string{"git", "clone", "--depth", "1", "--single-branch"}
		if ref != "" {
			args = append(args, "--branch", ref)
		}
		args = append(args, "https://github.com/"+owner+"/"+repo+".git", dest.localPath)
	}
	if !runClone(ctx, args, timeout) {
		removeCloneDestination(dest)
		return ""
	}
	return dest.localPath
}

// ---- reading the clone -----------------------------------------------------------------

// jsExtname is path.extname: the last dot of the final component, none for a leading dot.
func jsExtname(p string) string {
	base := p
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		base = p[i+1:]
	}
	i := strings.LastIndex(base, ".")
	if i <= 0 || base == ".." {
		return ""
	}
	return base[i:]
}

func isBinaryFile(filePath string) bool {
	if binaryExtensions[strings.ToLower(jsExtname(filePath))] {
		return true
	}
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return false
	}
	for _, c := range buf[:n] {
		if c == 0 {
			return true
		}
	}
	return false
}

func formatFileSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return strconv.FormatInt(bytes, 10) + " B"
	case bytes < 1024*1024:
		return toFixed1(bytes, 1024) + " KB"
	}
	return toFixed1(bytes, 1024*1024) + " MB"
}

// resolveWithinRepo is resolveWithinRepo (github-extract.ts:649): the path under the root, or false
// when it leaves it, lexically or through a symlink.
func resolveWithinRepo(rootPath, rel string) (string, bool) {
	root := resolvePath(rootPath)
	var candidate string
	if filepath.IsAbs(rel) {
		candidate = filepath.Clean(rel)
	} else {
		candidate = filepath.Clean(filepath.Join(root, rel))
	}
	sep := string(filepath.Separator)
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
	prefix := realRoot
	if !strings.HasSuffix(prefix, sep) {
		prefix += sep
	}
	if strings.HasPrefix(realCandidate, prefix) {
		return candidate, true
	}
	return "", false
}

// readTextFile is readFileSync(path, "utf-8") for the first maxUnits UTF-16 units: a UTF-16 unit
// takes at most three bytes, so a longer prefix cannot change the first maxUnits units. A file
// shorter than the prefix is read whole.
func readTextFile(path string, maxUnits int) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	limit := int64(maxUnits)*readPrefixBytesPerUnit + 10
	b, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return "", false
	}
	return decodeUTF8Lossy(b), true
}

// sortUTF16 sorts like Array.prototype.sort on strings: by UTF-16 code units.
func sortUTF16(items []string) {
	ascii := true
	for _, s := range items {
		for i := 0; i < len(s); i++ {
			if s[i] >= 0x80 {
				ascii = false
			}
		}
	}
	if ascii {
		sort.Strings(items)
		return
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := utf16.Encode([]rune(items[i])), utf16.Encode([]rune(items[j]))
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
}

func readDirNames(dir string) ([]string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	sortUTF16(names)
	return names, true
}

// buildTree lists up to 200 entries of the clone depth first, skipping .git and marking noise directories.
func buildTree(rootPath string) string {
	var entries []string
	var walk func(dir, relPath string)
	walk = func(dir, relPath string) {
		if len(entries) >= maxTreeEntries {
			return
		}
		items, ok := readDirNames(dir)
		if !ok {
			return
		}
		for _, item := range items {
			if len(entries) >= maxTreeEntries {
				return
			}
			if item == ".git" {
				continue
			}
			rel := item
			if relPath != "" {
				rel = relPath + "/" + item
			}
			safe, ok := resolveWithinRepo(rootPath, rel)
			if !ok {
				entries = append(entries, rel+"  [outside repo skipped]")
				continue
			}
			st, err := os.Stat(safe)
			if err != nil {
				continue
			}
			if st.IsDir() {
				if noiseDirs[item] {
					entries = append(entries, rel+"/  [skipped]")
					continue
				}
				entries = append(entries, rel+"/")
				walk(safe, rel)
			} else {
				entries = append(entries, rel)
			}
		}
	}
	walk(rootPath, "")
	if len(entries) >= maxTreeEntries {
		entries = append(entries, fmt.Sprintf("... (truncated at %d entries)", maxTreeEntries))
	}
	return strings.Join(entries, "\n")
}

func buildDirListing(rootPath, subPath string) string {
	target, ok := resolveWithinRepo(rootPath, subPath)
	if !ok {
		return "(path escapes repository root)"
	}
	items, ok := readDirNames(target)
	if !ok {
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
		safe, ok := resolveWithinRepo(rootPath, rel)
		if !ok {
			lines = append(lines, "  "+item+"  (outside repo)")
			continue
		}
		st, err := os.Stat(safe)
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

func readReadme(localPath string) string {
	for _, name := range []string{"README.md", "readme.md", "README", "README.txt", "README.rst"} {
		p := filepath.Join(localPath, name)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		content, ok := readTextFile(p, readmeMaxChars+1)
		if !ok {
			continue
		}
		if jsLen(content) > readmeMaxChars {
			return jsSlice(content, 0, readmeMaxChars) + readmeTruncated
		}
		return content
	}
	return ""
}

const exploreHint = "Use `read` and `bash` tools at the path above to explore further."

// generateContent is generateContent (github-extract.ts:782).
func generateContent(localPath string, info *githubURL) string {
	lines := []string{"Repository cloned to: " + localPath, ""}
	switch info.Type {
	case "root":
		lines = append(lines, "## Structure", buildTree(localPath), "")
		if readme := readReadme(localPath); readme != "" {
			lines = append(lines, "## README.md", readme, "")
		}
		lines = append(lines, exploreHint)
	case "tree":
		dirPath := info.Path
		full, ok := resolveWithinRepo(localPath, dirPath)
		if _, err := os.Stat(full); !ok || err != nil {
			lines = append(lines, "Path `"+dirPath+"` not found in clone. Showing repository root instead.", "", "## Structure", buildTree(localPath))
		} else {
			heading := dirPath
			if heading == "" {
				heading = "/"
			}
			lines = append(lines, "## "+heading, buildDirListing(localPath, dirPath))
		}
		lines = append(lines, "", exploreHint)
	case "blob":
		lines = blobContent(lines, localPath, info)
	}
	return strings.Join(lines, "\n")
}

func blobContent(lines []string, localPath string, info *githubURL) []string {
	filePath := info.Path
	full, ok := resolveWithinRepo(localPath, filePath)
	_, statErr := os.Stat(full)
	if !ok || statErr != nil {
		return append(lines, "Path `"+filePath+"` not found in clone. Showing repository root instead.", "", "## Structure", buildTree(localPath), "", exploreHint)
	}
	st, err := os.Stat(full)
	if err != nil {
		return append(lines, "Could not inspect `"+filePath+"`: "+err.Error(), "", exploreHint)
	}
	if st.IsDir() {
		heading := filePath
		if heading == "" {
			heading = "/"
		}
		return append(lines, "## "+heading, buildDirListing(localPath, filePath), "", exploreHint)
	}
	if isBinaryFile(full) {
		ext := strings.Replace(jsExtname(filePath), ".", "", 1)
		return append(lines, "## "+filePath, "Binary file ("+ext+", "+formatFileSize(st.Size())+"). Use `read` or `bash` tools at the path above to inspect.")
	}
	content, ok := readTextFile(full, maxInlineFileChars+1)
	if !ok {
		return append(lines, "Could not read `"+filePath+"` as UTF-8 text.", "", exploreHint)
	}
	lines = append(lines, "## "+filePath)
	if jsLen(content) > maxInlineFileChars {
		lines = append(lines, jsSlice(content, 0, maxInlineFileChars), "", "[File truncated at 100K chars. Full file: "+full+"]")
	} else {
		lines = append(lines, content)
	}
	return append(lines, "", exploreHint)
}

// ---- extraction ---------------------------------------------------------------------------

func (s *githubState) cachedCloneFor(key string) *cachedClone {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cache[key]
}

func (s *githubState) dropClone(key string, entry *cachedClone) {
	s.mu.Lock()
	if s.cache[key] == entry {
		delete(s.cache, key)
	}
	s.mu.Unlock()
}

func cloneResult(rawURL, localPath string, info *githubURL) *ExtractedContent {
	return &ExtractedContent{URL: rawURL, Title: repoTitle(info.Owner, info.Repo, info), Content: generateContent(localPath, info)}
}

// awaitCachedClone is awaitCachedClone: the result of a clone another call started (or finished),
// the API view when that clone failed.
func (s *githubState) awaitCachedClone(ctx context.Context, cached *cachedClone, rawURL string, info *githubURL) *ExtractedContent {
	if ctx.Err() != nil {
		return nil
	}
	select {
	case <-cached.done:
	case <-ctx.Done():
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	if cached.path != "" {
		return cloneResult(rawURL, cached.path, info)
	}
	return s.fetchViaAPI(ctx, rawURL, info.Owner, info.Repo, info, "")
}

// extractGitHub is extractGitHub (github-extract.ts:906): nil when the URL is not for this
// extractor, the extractor is disabled, the call was cancelled, or nothing worked (the caller then
// fetches the page); an error only for an unreadable configuration.
func (s *githubState) extractGitHub(ctx context.Context, rawURL string, forceClone bool) (*ExtractedContent, error) {
	info := parseGitHubURL(rawURL)
	if info == nil || ctx.Err() != nil {
		return nil, nil
	}
	cfg, err := s.cloneConfig()
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, nil
	}
	owner, repo := info.Owner, info.Repo
	key := cloneCacheKey(owner, repo, info.Ref)
	if cached := s.cachedCloneFor(key); cached != nil {
		return s.awaitCachedClone(ctx, cached, rawURL, info), nil
	}
	if info.RefIsFullSHA {
		return s.fetchViaAPI(ctx, rawURL, owner, repo, info, "Note: Commit SHA URLs use the GitHub API instead of cloning."), nil
	}
	if !forceClone {
		sizeKB, known := s.checkRepoSize(ctx, owner, repo)
		if ctx.Err() != nil {
			return nil, nil
		}
		if known {
			if sizeMB := sizeKB / 1024; sizeMB > cfg.MaxRepoSizeMB {
				note := fmt.Sprintf("Note: Repository is %sMB (threshold: %sMB). ", formatNumber(math.Floor(sizeMB+0.5)), formatNumber(cfg.MaxRepoSizeMB)) +
					"Showing API-fetched content instead of full clone. Ask the user if they'd like to clone the full repo -- " +
					"if yes, call fetch_content again with the same URL and add forceClone: true to the params."
				return s.fetchViaAPI(ctx, rawURL, owner, repo, info, note), nil
			}
		}
	}
	if ctx.Err() != nil {
		return nil, nil
	}
	// Another call may have started this clone while the size check ran.
	s.mu.Lock()
	if cached := s.cache[key]; cached != nil {
		s.mu.Unlock()
		return s.awaitCachedClone(ctx, cached, rawURL, info), nil
	}
	dest, ok := s.cloneDestinationFor(cfg, owner, repo, info.Ref)
	if !ok {
		s.mu.Unlock()
		return s.fetchViaAPI(ctx, rawURL, owner, repo, info, ""), nil
	}
	entry := &cachedClone{dest: dest, done: make(chan struct{})}
	s.cache[key] = entry
	s.mu.Unlock()

	entry.path = s.cloneRepo(ctx, owner, repo, info.Ref, cfg, dest)
	close(entry.done)
	if entry.path == "" {
		s.dropClone(key, entry)
	}
	if ctx.Err() != nil {
		return nil, nil
	}
	if entry.path == "" {
		return s.fetchViaAPI(ctx, rawURL, owner, repo, info, ""), nil
	}
	return cloneResult(rawURL, entry.path, info), nil
}

// clearCloneCache is clearCloneCache: the clones and this process's runtime directory go, and the
// configuration is read again next time.
func (s *githubState) clearCloneCache() {
	s.mu.Lock()
	entries := make([]*cachedClone, 0, len(s.cache))
	for _, e := range s.cache {
		entries = append(entries, e)
	}
	s.cache = map[string]*cachedClone{}
	rt := s.runtime
	s.runtime = nil
	s.config = nil
	s.mu.Unlock()
	for _, e := range entries {
		removeCloneDestination(e.dest)
	}
	if rt != nil {
		removeCloneRuntimeDir(rt.parentPath, rt.rootPath)
	}
}

// nodePlatform is process.platform for this build.
func nodePlatform() string {
	switch runtime.GOOS {
	case "windows":
		return "win32"
	case "solaris", "illumos":
		return "sunos"
	}
	return runtime.GOOS
}

func extractGitHub(ctx context.Context, rawURL string, forceClone bool) (*ExtractedContent, error) {
	return defaultGitHub().extractGitHub(ctx, rawURL, forceClone)
}

// clearCloneCache is the session start and shutdown hook.
func clearCloneCache() { defaultGitHub().clearCloneCache() }

// extractGitHubURL is the routing of extract.ts:719-742: the issue and pull request extractor, then
// the repository one. A result ends the fetch (the original then also checks for a cancellation);
// a cancellation is "Aborted"; an unreadable configuration is the URL's error; any other failure
// leaves the URL to the page fetch. done is false when the fetch goes on.
func extractGitHubURL(ctx context.Context, rawURL string, o ExtractOptions) (res ExtractedContent, done bool) {
	step := func(r *ExtractedContent, err error) (ExtractedContent, bool) {
		if err != nil {
			switch {
			case isAbortError(err):
				return abortedResult(rawURL), true
			case isConfigParseError(err.Error()):
				return failed(rawURL, err.Error()), true
			}
			return ExtractedContent{}, false
		}
		if r != nil {
			return *r, true
		}
		if ctx.Err() != nil {
			return abortedResult(rawURL), true
		}
		return ExtractedContent{}, false
	}
	if res, done = step(extractGitHubIssuePR(ctx, rawURL, o)); done {
		return res, true
	}
	return step(extractGitHub(ctx, rawURL, o.ForceClone))
}
