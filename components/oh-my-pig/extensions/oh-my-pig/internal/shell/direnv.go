package shell

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// upstream: packages/coding-agent/src/exec/direnv.ts:1-391
// upstream: packages/coding-agent/src/exec/bash-executor.ts:232-297,770-780

const (
	// DefaultDirenvTimeoutMs matches omp's bash.direnvLoadTimeoutMs default (30s).
	// upstream: packages/coding-agent/src/exec/direnv.ts:8, exec/settings.ts:231
	DefaultDirenvTimeoutMs = 30_000

	envrcCacheMax = 512
	envrcCacheTTL = 5 * time.Second
	direnvWarmTTL = 5 * time.Second
)

var safeDirenvVarNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// DirenvExportDiff splits `direnv export json` output (`{VAR: value|null}`) into
// variables to set and variables to unset.
// upstream: packages/coding-agent/src/exec/direnv.ts:26-31
type DirenvExportDiff struct {
	Set   map[string]string
	Unset []string
}

// FindEnvrc walks up from startDir to the nearest directory containing a regular `.envrc`.
// upstream: packages/coding-agent/src/exec/direnv.ts:11-24
func FindEnvrc(startDir string) string {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		dir = filepath.Clean(startDir)
	}
	for {
		candidate := filepath.Join(dir, ".envrc")
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// ParseDirenvExport parses `direnv export json` output (`{VAR: value|null}`) into
// Set and Unset halves. Invalid or empty input returns an empty diff.
// upstream: packages/coding-agent/src/exec/direnv.ts:34-52
func ParseDirenvExport(jsonText string) DirenvExportDiff {
	trimmed := strings.TrimSpace(jsonText)
	if trimmed == "" {
		return DirenvExportDiff{Set: map[string]string{}}
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return DirenvExportDiff{Set: map[string]string{}}
	}
	diff := DirenvExportDiff{
		Set: make(map[string]string, len(raw)),
	}
	for k, v := range raw {
		if v == nil {
			diff.Unset = append(diff.Unset, k)
		} else if s, ok := v.(string); ok {
			diff.Set[k] = s
		}
	}
	return diff
}

type direnvWatch struct {
	path    string
	exists  bool
	mtimeNs int64
	size    int64
}

type envrcCacheEntry struct {
	found string
	at    time.Time
}

type direnvExportCacheEntry struct {
	baseSize     int
	baseChecksum uint64
	base         map[string]string
	loaded       map[string]string
	diff         DirenvExportDiff
	mtimeNs      int64
	size         int64
	watches      []direnvWatch
	verifiedAt   time.Time
}

type direnvStatusJSON struct {
	State *struct {
		FoundRC *struct {
			Allowed any `json:"allowed"`
		} `json:"foundRC"`
	} `json:"state"`
}

var (
	direnvMu          sync.Mutex
	direnvBinLookedUp bool
	direnvBinPath     string
	direnvEnvrcCache  = map[string]envrcCacheEntry{}
	direnvExportCache = map[string]*direnvExportCacheEntry{}
	direnvLookPathFn  = exec.LookPath
	direnvNowFn       = time.Now
)

// ClearDirenvCachesForTests resets all direnv lookup, walk-up, and export caches.
// upstream: packages/coding-agent/src/exec/direnv.ts:160-164
func ClearDirenvCachesForTests() {
	direnvMu.Lock()
	direnvBinLookedUp = false
	direnvBinPath = ""
	clear(direnvEnvrcCache)
	clear(direnvExportCache)
	direnvMu.Unlock()
}

func direnvBinary() string {
	direnvMu.Lock()
	defer direnvMu.Unlock()
	if !direnvBinLookedUp {
		p, err := direnvLookPathFn("direnv")
		if err == nil {
			direnvBinPath = p
		} else {
			direnvBinPath = ""
		}
		direnvBinLookedUp = true
	}
	return direnvBinPath
}

func findEnvrcCached(startDir string) string {
	key, err := filepath.Abs(startDir)
	if err != nil {
		key = filepath.Clean(startDir)
	}
	now := direnvNowFn()

	direnvMu.Lock()
	if entry, ok := direnvEnvrcCache[key]; ok && now.Sub(entry.at) < envrcCacheTTL {
		direnvMu.Unlock()
		return entry.found
	}
	direnvMu.Unlock()

	found := FindEnvrc(key)

	direnvMu.Lock()
	if len(direnvEnvrcCache) >= envrcCacheMax {
		clear(direnvEnvrcCache)
	}
	direnvEnvrcCache[key] = envrcCacheEntry{found: found, at: now}
	direnvMu.Unlock()
	return found
}

// cleanSpawnEnv returns the parent process environment stripped of DIRENV_*
// variables plus a (size, checksum) fingerprint for cache validation.
// upstream: packages/coding-agent/src/exec/direnv.ts:188-214
func cleanSpawnEnv() (map[string]string, int, uint64) {
	environ := os.Environ()
	out := make(map[string]string, len(environ))
	var checksum uint64
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.HasPrefix(k, "DIRENV_") {
			continue
		}
		out[k] = v
		// Order-independent hash accumulation via XOR of per-entry FNV-1a hashes.
		var h uint64 = 14695981039346656037
		for i := range len(k) {
			h ^= uint64(k[i])
			h *= 1099511628211
		}
		h ^= '='
		h *= 1099511628211
		for i := range len(v) {
			h ^= uint64(v[i])
			h *= 1099511628211
		}
		checksum ^= h
	}
	return out, len(out), checksum
}

func envMapToSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func applyDirenvDiffToMap(base map[string]string, diff DirenvExportDiff) map[string]string {
	loaded := make(map[string]string, len(base)+len(diff.Set))
	for k, v := range base {
		loaded[k] = v
	}
	for k, v := range diff.Set {
		loaded[k] = v
	}
	for _, name := range diff.Unset {
		delete(loaded, name)
	}
	return loaded
}

func cloneDirenvDiff(diff DirenvExportDiff) DirenvExportDiff {
	cp := DirenvExportDiff{
		Set: make(map[string]string, len(diff.Set)),
	}
	for k, v := range diff.Set {
		cp.Set[k] = v
	}
	if len(diff.Unset) > 0 {
		cp.Unset = append([]string(nil), diff.Unset...)
	}
	return cp
}

func statDirenvWatch(watchedPath string) (direnvWatch, error) {
	linkInfo, linkErr := os.Lstat(watchedPath)
	if linkErr != nil {
		if errors.Is(linkErr, os.ErrNotExist) {
			return direnvWatch{path: watchedPath, exists: false}, nil
		}
		return direnvWatch{}, linkErr
	}
	targetInfo, targetErr := os.Stat(watchedPath)
	if targetErr != nil {
		if errors.Is(targetErr, os.ErrNotExist) {
			return direnvWatch{path: watchedPath, exists: false}, nil
		}
		return direnvWatch{}, targetErr
	}
	mtimeNs := linkInfo.ModTime().UnixNano()
	if targetNs := targetInfo.ModTime().UnixNano(); targetNs > mtimeNs {
		mtimeNs = targetNs
	}
	return direnvWatch{
		path:    watchedPath,
		exists:  true,
		mtimeNs: mtimeNs,
		size:    targetInfo.Size(),
	}, nil
}

// snapshotDirenvWatches decodes direnv's base64url+zlib JSON watch list and
// snapshots high-resolution stats for each watched path (excluding envrcPath,
// which is already stat-checked by the caller).
// upstream: packages/coding-agent/src/exec/direnv.ts:110-144
func snapshotDirenvWatches(encoded, envrcPath string) ([]direnvWatch, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(encoded)
		if err != nil {
			return nil, false
		}
	}
	zr, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, false
	}
	defer zr.Close()
	inflated, err := io.ReadAll(io.LimitReader(zr, 2*1024*1024))
	if err != nil {
		return nil, false
	}
	var decoded []struct {
		Path    *string `json:"path"`
		Modtime *int64  `json:"modtime"`
		Exists  *bool   `json:"exists"`
	}
	if err := json.Unmarshal(inflated, &decoded); err != nil || len(decoded) == 0 {
		return nil, false
	}
	watches := make([]direnvWatch, 0, len(decoded))
	for _, item := range decoded {
		if item.Path == nil || item.Modtime == nil || item.Exists == nil || !filepath.IsAbs(*item.Path) {
			return nil, false
		}
		if *item.Path == envrcPath {
			continue
		}
		w, err := statDirenvWatch(*item.Path)
		if err != nil {
			return nil, false
		}
		if w.exists != *item.Exists {
			return nil, false
		}
		if w.exists && (w.mtimeNs/1_000_000_000) != *item.Modtime {
			return nil, false
		}
		watches = append(watches, w)
	}
	return watches, true
}

func direnvWatchesUnchanged(watches []direnvWatch) bool {
	for _, w := range watches {
		cur, err := statDirenvWatch(w.path)
		if err != nil {
			return false
		}
		if cur.exists != w.exists || cur.mtimeNs != w.mtimeNs || cur.size != w.size {
			return false
		}
	}
	return true
}

func runDirenvCommand(ctx context.Context, bin string, args []string, dir string, timeout time.Duration, env map[string]string) (exitCode int, stdout, stderr string, err error) {
	if timeout <= 0 {
		timeout = time.Duration(DefaultDirenvTimeoutMs) * time.Millisecond
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, bin, args...)
	configureProcessGroup(cmd)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			killProcessTreeImmediate(cmd.Process)
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = dir
	cmd.Env = envMapToSlice(env)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if err := cmd.Start(); err != nil {
		return -1, "", "", err
	}
	attachProcessGroup(cmd.Process.Pid)
	runErr := cmd.Wait()
	stdout = outBuf.String()
	stderr = errBuf.String()
	if runErr == nil || (errors.Is(runErr, exec.ErrWaitDelay) && runCtx.Err() == nil) {
		return 0, stdout, stderr, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode(), stdout, stderr, nil
	}
	return -1, stdout, stderr, runErr
}

func exportDirenv(ctx context.Context, bin, dir string, timeout time.Duration, env map[string]string) (*DirenvExportDiff, bool) {
	exitCode, stdout, stderr, err := runDirenvCommand(ctx, bin, []string{"export", "json"}, dir, timeout, env)
	if err != nil {
		return nil, false
	}
	if exitCode == 0 && !strings.Contains(stderr, "is blocked") {
		diff := ParseDirenvExport(stdout)
		return &diff, true
	}
	return nil, false
}

func direnvDenied(ctx context.Context, bin, dir string, timeout time.Duration, env map[string]string) bool {
	exitCode, stdout, _, err := runDirenvCommand(ctx, bin, []string{"status", "--json"}, dir, timeout, env)
	if err != nil || exitCode != 0 {
		return false
	}
	var status direnvStatusJSON
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		return false
	}
	if status.State == nil || status.State.FoundRC == nil {
		return false
	}
	switch v := status.State.FoundRC.Allowed.(type) {
	case float64:
		return v == 2
	case int:
		return v == 2
	}
	return false
}

func allDirenvBookkeepingKeys(m map[string]string) bool {
	for k := range m {
		if !strings.HasPrefix(k, "DIRENV_") {
			return false
		}
	}
	return true
}

// LoadDirenvEnv resolves the nearest `.envrc` from cwd and returns its `direnv export json`
// diff relative to the clean process environment. Returns nil when there is no `.envrc`,
// `direnv` is not installed, the `.envrc` is blocked/denied, or the export fails/times out.
//
// Stat-caches `.envrc` mtime/size (plus any DIRENV_WATCHES paths) per `.envrc` directory so
// repeated bash calls within the warm TTL cost zero subprocesses.
// upstream: packages/coding-agent/src/exec/direnv.ts:320-391
func LoadDirenvEnv(ctx context.Context, cwd string, timeout time.Duration) *DirenvExportDiff {
	envrcPath := findEnvrcCached(cwd)
	if envrcPath == "" {
		return nil
	}
	bin := direnvBinary()
	if bin == "" {
		return nil
	}

	dir := filepath.Dir(envrcPath)
	if timeout <= 0 {
		timeout = time.Duration(DefaultDirenvTimeoutMs) * time.Millisecond
	}
	base, baseSize, baseChecksum := cleanSpawnEnv()

	// Capture .envrc stat BEFORE export so an edit during the export invalidates the cache.
	envrcInfo, statErr := os.Stat(envrcPath)
	hasStat := statErr == nil && envrcInfo.Mode().IsRegular()

	direnvMu.Lock()
	cached := direnvExportCache[dir]
	delete(direnvExportCache, dir)
	direnvMu.Unlock()

	var previous *direnvExportCacheEntry
	if cached != nil &&
		hasStat &&
		cached.baseSize == baseSize &&
		cached.baseChecksum == baseChecksum &&
		cached.mtimeNs == envrcInfo.ModTime().UnixNano() &&
		cached.size == envrcInfo.Size() &&
		direnvWatchesUnchanged(cached.watches) {
		previous = cached
	}

	now := direnvNowFn()
	if previous != nil {
		if now.Sub(previous.verifiedAt) < direnvWarmTTL {
			direnvMu.Lock()
			direnvExportCache[dir] = previous
			direnvMu.Unlock()
			out := cloneDirenvDiff(previous.diff)
			return &out
		}
		warm, ok := exportDirenv(ctx, bin, dir, timeout, previous.loaded)
		if !ok {
			return nil
		}
		if len(warm.Set) == 0 && len(warm.Unset) == 0 {
			previous.verifiedAt = direnvNowFn()
			direnvMu.Lock()
			direnvExportCache[dir] = previous
			direnvMu.Unlock()
			out := cloneDirenvDiff(previous.diff)
			return &out
		}
	}

	diff, ok := exportDirenv(ctx, bin, dir, timeout, base)
	if !ok {
		return nil
	}
	if len(diff.Unset) == 0 && allDirenvBookkeepingKeys(diff.Set) && direnvDenied(ctx, bin, dir, timeout, base) {
		return nil
	}

	loaded := applyDirenvDiffToMap(base, *diff)
	if hasStat {
		var watches []direnvWatch
		canCache := true
		if encoded, hasWatches := loaded["DIRENV_WATCHES"]; hasWatches && encoded != "" {
			if _, hasDiff := loaded["DIRENV_DIFF"]; hasDiff {
				w, wOK := snapshotDirenvWatches(encoded, envrcPath)
				if !wOK {
					canCache = false
				} else {
					watches = w
				}
			}
		}
		if canCache {
			entry := &direnvExportCacheEntry{
				baseSize:     baseSize,
				baseChecksum: baseChecksum,
				base:         base,
				loaded:       loaded,
				diff:         cloneDirenvDiff(*diff),
				mtimeNs:      envrcInfo.ModTime().UnixNano(),
				size:         envrcInfo.Size(),
				watches:      watches,
				verifiedAt:   direnvNowFn(),
			}
			direnvMu.Lock()
			if len(direnvExportCache) >= envrcCacheMax {
				clear(direnvExportCache)
			}
			direnvExportCache[dir] = entry
			direnvMu.Unlock()
		}
	}
	out := cloneDirenvDiff(*diff)
	return &out
}

// resolveDirenvSetting reads `bash.direnv` (enum "auto"|"off", default "auto"; also accepts
// bool for defensive compatibility).
func resolveDirenvSetting(raw any) string {
	switch v := raw.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "off", "false":
			return "off"
		case "auto", "on", "true":
			return "auto"
		}
	case bool:
		if !v {
			return "off"
		}
		return "auto"
	}
	return "auto"
}

// resolveDirenvLoadTimeout computes the effective direnv load timeout clamped by an optional
// positive command deadline, matching `applyDirenvPreflight` in bash-executor.ts:277-280.
func resolveDirenvLoadTimeout(configuredMs float64, callerTimeout *time.Duration) time.Duration {
	budgetMs := float64(DefaultDirenvTimeoutMs)
	if configuredMs > 0 {
		budgetMs = configuredMs
	}
	budget := time.Duration(budgetMs) * time.Millisecond
	if callerTimeout != nil && *callerTimeout > 0 && *callerTimeout < budget {
		return *callerTimeout
	}
	return budget
}

// applyDirenvPreflight loads the repo's direnv diff when enabled and returns the diff
// (or nil when off / no `.envrc` / direnv missing / failed) plus any `unset -v` command prefix
// for variables removed by `.envrc`.
// upstream: packages/coding-agent/src/exec/bash-executor.ts:267-297
func applyDirenvPreflight(ctx context.Context, command, cwd, direnvSetting string, loadTimeout time.Duration) (string, *DirenvExportDiff) {
	if direnvSetting == "off" || cwd == "" {
		return command, nil
	}
	diff := LoadDirenvEnv(ctx, cwd, loadTimeout)
	if diff == nil {
		return command, nil
	}
	var unsets []string
	for _, name := range diff.Unset {
		if safeDirenvVarNameRE.MatchString(name) {
			unsets = append(unsets, name)
		}
	}
	if len(unsets) > 0 {
		command = "unset -v " + strings.Join(unsets, " ") + "; " + command
	}
	return command, diff
}
