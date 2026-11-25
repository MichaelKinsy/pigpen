package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// CacheKind identifies the cached view type.
// upstream: tools/github-cache.ts:39
type CacheKind string

const (
	CacheKindIssue  CacheKind = "issue"
	CacheKindPr     CacheKind = "pr"
	CacheKindPrDiff CacheKind = "pr-diff"
	CacheKindMrDiff CacheKind = "mr-diff"
)

// CacheStatus describes how a view lookup was satisfied.
// upstream: tools/github-cache.ts:746
type CacheStatus string

const (
	CacheStatusMiss      CacheStatus = "miss"
	CacheStatusFresh     CacheStatus = "fresh"
	CacheStatusRefreshed CacheStatus = "refreshed"
	CacheStatusStale     CacheStatus = "stale"
	CacheStatusDisabled  CacheStatus = "disabled"
)

const (
	// Setting keys matching omp's tools/settings.ts:892-940.
	SettingEnabled      = "github.enabled"
	SettingCacheEnabled = "github.cache.enabled"
	SettingCacheSoftTTL = "github.cache.softTtlSec"
	SettingCacheHardTTL = "github.cache.hardTtlSec"

	// CacheEnvVar overrides the JSON cache file path for this port. We do NOT
	// read omp's OMP_GITHUB_CACHE_DB so a user sharing env with omp cannot have
	// omp's SQLite DB overwritten with JSON.
	CacheEnvVar = "OH_MY_PIG_GITHUB_CACHE"

	defaultSoftTTLSec = 300
	defaultHardTTLSec = 604800

	// openStateSoftTTL caps soft freshness for open issues/PRs.
	// upstream: tools/github-cache.ts:852
	openStateSoftTTL = 15 * time.Second

	defaultCacheAuthKey = "default"
	sweepInterval       = 60 * time.Second
	maxGenIDSteps       = 1000
)

// CacheTTLSettings holds resolved github.cache.* settings.
type CacheTTLSettings struct {
	Enabled bool
	SoftTTL time.Duration
	HardTTL time.Duration
}

// CacheSettingsFn reads github.cache.* settings. The settings lane may override
// it; the default honours Enabled(ctx, "github.cache.enabled", true) and uses
// omp's default TTLs (soft=300s, hard=7d).
var CacheSettingsFn = func(ctx sdk.Context) CacheTTLSettings {
	enabled := Enabled(ctx, SettingCacheEnabled, true)
	return CacheTTLSettings{
		Enabled: enabled,
		SoftTTL: defaultSoftTTLSec * time.Second,
		HardTTL: defaultHardTTLSec * time.Second,
	}
}

type cacheEntry struct {
	AuthKey         string          `json:"authKey"`
	Repo            string          `json:"repo"`
	Kind            CacheKind       `json:"kind"`
	Number          int             `json:"number"`
	IncludeComments bool            `json:"includeComments"`
	FetchedAt       int64           `json:"fetchedAt"`
	Payload         json.RawMessage `json:"payload"`
	Rendered        string          `json:"rendered"`
	SourceURL       string          `json:"sourceUrl,omitempty"`
}

type generationEntry struct {
	cacheEntry
	RetainedAt int64 `json:"retainedAt"`
}

type diskCache struct {
	Version     int                        `json:"version"`
	Rows        map[string]cacheEntry      `json:"rows"`
	Generations map[string]generationEntry `json:"generations"`
}

type authKeyMemoEntry struct {
	envSig        string
	configPath    string
	configMtimeNs int64
	value         string
}

var (
	cacheMu           sync.Mutex
	cacheLoadedPath   string
	cacheLoadedMtime  int64
	cacheLoadedSize   int64
	cacheData         diskCache
	cachePathOverride string
	invalidationEpoch int64
	lastSweepAt       time.Time
	nowFunc           = time.Now

	authMemoMu sync.Mutex
	authMemo   = make(map[string]authKeyMemoEntry)

	inflightMu        sync.Mutex
	inflightRefreshes = make(map[string]struct{})
)

var (
	ghTokenEnvVars     = []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"}
	gitlabTokenEnvVars = []string{"GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN"}
)

func resolveCacheFilePath(ctx sdk.Context) string {
	if cachePathOverride != "" {
		return cachePathOverride
	}
	if envPath := os.Getenv(CacheEnvVar); envPath != "" {
		return envPath
	}
	var zero sdk.Context
	if ctx != zero {
		if home, err := ctx.ConfigHome(); err == nil && home != "" {
			return filepath.Join(home, "cache", "github-cache.json")
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".pig", "cache", "github-cache.json")
	}
	return filepath.Join(os.TempDir(), "oh-my-pig-github-cache.json")
}

func normalizeCacheRepo(repo string) string {
	ref := ParseRepoRef(strings.ToLower(repo))
	if ref.Host != "" && ref.Host != defaultGhHost() {
		return ref.Host + "/" + ref.Slug
	}
	return ref.Slug
}

func rowKey(authKey, repo string, kind CacheKind, number int, includeComments bool) string {
	ic := 0
	if includeComments {
		ic = 1
	}
	return fmt.Sprintf("%s|%s|%s|%d|%d", authKey, normalizeCacheRepo(repo), kind, number, ic)
}

func genKey(authKey, repo string, kind CacheKind, number int, includeComments bool, fetchedAt int64) string {
	return fmt.Sprintf("%s|%d", rowKey(authKey, repo, kind, number, includeComments), fetchedAt)
}

func emptyDiskCache() diskCache {
	return diskCache{
		Version:     4,
		Rows:        make(map[string]cacheEntry),
		Generations: make(map[string]generationEntry),
	}
}

func readDiskCacheFile(path string) (diskCache, int64, int64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return emptyDiskCache(), 0, 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return emptyDiskCache(), info.ModTime().UnixNano(), info.Size(), false
	}
	var parsed diskCache
	if err := json.Unmarshal(data, &parsed); err != nil || parsed.Version != 4 {
		return emptyDiskCache(), info.ModTime().UnixNano(), info.Size(), false
	}
	if parsed.Rows == nil {
		parsed.Rows = make(map[string]cacheEntry)
	}
	if parsed.Generations == nil {
		parsed.Generations = make(map[string]generationEntry)
	}
	return parsed, info.ModTime().UnixNano(), info.Size(), true
}

// ensureCacheFreshLocked reloads the cache file whenever the path changed or
// another process modified it on disk (stat mtime/size check).
func ensureCacheFreshLocked(path string) {
	info, err := os.Stat(path)
	if err != nil {
		if cacheLoadedPath != path {
			cacheLoadedPath = path
			cacheLoadedMtime = 0
			cacheLoadedSize = 0
			cacheData = emptyDiskCache()
		}
		return
	}
	mtime := info.ModTime().UnixNano()
	size := info.Size()
	if cacheLoadedPath == path && cacheLoadedMtime == mtime && cacheLoadedSize == size {
		return
	}
	parsed, m, sz, ok := readDiskCacheFile(path)
	cacheLoadedPath = path
	cacheLoadedMtime = m
	cacheLoadedSize = sz
	if ok {
		cacheData = parsed
	} else if cacheData.Rows == nil {
		cacheData = emptyDiskCache()
	}
}

// saveCacheLocked writes atomically using a unique temp file in the target
// directory (never a fixed `.tmp` name) with 0600 permissions so concurrent
// processes cannot corrupt each other's temp files.
func saveCacheLocked(path string) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	data, err := json.Marshal(cacheData)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, "github-cache-*.tmp")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return
	}
	if info, err := os.Stat(path); err == nil {
		cacheLoadedPath = path
		cacheLoadedMtime = info.ModTime().UnixNano()
		cacheLoadedSize = info.Size()
	}
}

func sweepIfDueLocked(path string, hardTTL time.Duration, now time.Time) {
	if !lastSweepAt.IsZero() && now.Sub(lastSweepAt) < sweepInterval {
		return
	}
	lastSweepAt = now
	cutoff := now.Add(-hardTTL).UnixMilli()
	changed := false
	for k, row := range cacheData.Rows {
		if row.FetchedAt < cutoff {
			delete(cacheData.Rows, k)
			changed = true
		}
	}
	for k, gen := range cacheData.Generations {
		if gen.RetainedAt < cutoff {
			delete(cacheData.Generations, k)
			changed = true
		}
	}
	if changed {
		saveCacheLocked(path)
	}
}

func getGhConfigDir() string {
	if override := os.Getenv("GH_CONFIG_DIR"); override != "" {
		return override
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "gh")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "gh")
}

func getGlabConfigPath() string {
	if override := os.Getenv("GLAB_CONFIG_DIR"); override != "" {
		return filepath.Join(override, "config.yml")
	}
	home, _ := os.UserHomeDir()
	candidates := []string{filepath.Join(home, ".config", "glab-cli", "config.yml")}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		candidates = append(candidates, filepath.Join(xdg, "glab-cli", "config.yml"))
	} else if runtime.GOOS == "darwin" {
		candidates = append(candidates, filepath.Join(home, "Library", "Application Support", "glab-cli", "config.yml"))
	} else if runtime.GOOS == "windows" {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		candidates = append(candidates, filepath.Join(localAppData, "glab-cli", "config.yml"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return candidates[0]
}

func hashCacheIdentity(parts []string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{'|'})
		}
		fmt.Fprintf(h, "%d:%s", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// resolveCLICacheAuthKey mirrors tools/github-cache.ts:256-299, memoizing by
// env signature + config path + config mtime so hot-path reads only stat.
func resolveCLICacheAuthKey(memoKey, host string, tokenEnvVars []string, configPath, configLabel string) string {
	var envSigB strings.Builder
	for _, name := range tokenEnvVars {
		if v := os.Getenv(name); v != "" {
			fmt.Fprintf(&envSigB, "%s=%d:%s\x00", name, len(v), v)
		}
	}
	envSig := envSigB.String()
	var mtimeNs int64 = -1
	if info, err := os.Stat(configPath); err == nil {
		mtimeNs = info.ModTime().UnixNano()
	}

	authMemoMu.Lock()
	if m, ok := authMemo[memoKey]; ok && m.envSig == envSig && m.configPath == configPath && m.configMtimeNs == mtimeNs {
		authMemoMu.Unlock()
		return m.value
	}
	authMemoMu.Unlock()

	parts := []string{"host:" + host}
	hasCreds := false
	for _, name := range tokenEnvVars {
		if v := os.Getenv(name); v != "" {
			hasCreds = true
			parts = append(parts, name+":"+v)
		}
	}
	if b, err := os.ReadFile(configPath); err == nil {
		hasCreds = true
		parts = append(parts, configLabel+":"+string(b))
	}
	val := ""
	if hasCreds {
		val = host + ":" + hashCacheIdentity(parts)
	}

	authMemoMu.Lock()
	authMemo[memoKey] = authKeyMemoEntry{
		envSig:        envSig,
		configPath:    configPath,
		configMtimeNs: mtimeNs,
		value:         val,
	}
	authMemoMu.Unlock()
	return val
}

func resolveGithubCacheAuthKey(host string) string {
	if host == "" {
		host = defaultGhHost()
	}
	return resolveCLICacheAuthKey("github:"+host, host, ghTokenEnvVars, filepath.Join(getGhConfigDir(), "hosts.yml"), "hosts")
}

func resolveGitlabCacheAuthKey(host string) string {
	return resolveCLICacheAuthKey("gitlab:"+host, host, gitlabTokenEnvVars, getGlabConfigPath(), "glab-config")
}

// FreshView is returned by a cache-miss fetch callback.
type FreshView[T any] struct {
	Rendered  string
	SourceURL string
	Payload   T
}

// ViewLookupResult is the result of a cache-aware issue/PR/diff lookup.
// upstream: tools/gh-view.ts:493-499
type ViewLookupResult[T any] struct {
	Rendered  string
	SourceURL string
	Payload   T
	Status    CacheStatus
	FetchedAt int64
}

type cacheLookupOptions[T any] struct {
	sdkCtx          sdk.Context
	repo            string
	kind            CacheKind
	number          int
	includeComments bool
	authKey         string
	bypassCache     bool
	fetchFresh      func() (FreshView[T], error)
}

func isOpenView(kind CacheKind, payloadRaw json.RawMessage) bool {
	if kind != CacheKindIssue && kind != CacheKindPr {
		return false
	}
	var s struct {
		State string `json:"state"`
	}
	if json.Unmarshal(payloadRaw, &s) != nil {
		return false
	}
	return strings.EqualFold(s.State, "OPEN")
}

func getOrFetchView[T any](opts cacheLookupOptions[T]) (ViewLookupResult[T], error) {
	ttl := CacheSettingsFn(opts.sdkCtx)
	now := nowFunc()
	nowMs := now.UnixMilli()

	if !ttl.Enabled || opts.bypassCache || opts.authKey == "" {
		fresh, err := opts.fetchFresh()
		if err != nil {
			return ViewLookupResult[T]{}, err
		}
		return ViewLookupResult[T]{
			Rendered:  fresh.Rendered,
			SourceURL: fresh.SourceURL,
			Payload:   fresh.Payload,
			Status:    CacheStatusDisabled,
			FetchedAt: nowMs,
		}, nil
	}

	path := resolveCacheFilePath(opts.sdkCtx)
	key := rowKey(opts.authKey, opts.repo, opts.kind, opts.number, opts.includeComments)

	cacheMu.Lock()
	ensureCacheFreshLocked(path)
	sweepIfDueLocked(path, ttl.HardTTL, now)
	cached, hasCached := cacheData.Rows[key]
	startEpoch := invalidationEpoch
	cacheMu.Unlock()

	if hasCached {
		var payload T
		if json.Unmarshal(cached.Payload, &payload) == nil {
			age := time.Duration(nowMs-cached.FetchedAt) * time.Millisecond
			soft := ttl.SoftTTL
			if isOpenView(opts.kind, cached.Payload) && openStateSoftTTL < soft {
				soft = openStateSoftTTL
			}
			if age > ttl.HardTTL {
				// Past hard TTL: drop eagerly and bump invalidation epoch just
				// as omp's invalidate() does (github-cache.ts:889-894).
				cacheMu.Lock()
				ensureCacheFreshLocked(path)
				invalidationEpoch++
				startEpoch = invalidationEpoch
				delete(cacheData.Rows, key)
				saveCacheLocked(path)
				cacheMu.Unlock()
			} else if age <= soft {
				return ViewLookupResult[T]{
					Rendered:  cached.Rendered,
					SourceURL: cached.SourceURL,
					Payload:   payload,
					Status:    CacheStatusFresh,
					FetchedAt: cached.FetchedAt,
				}, nil
			} else if opts.kind == CacheKindPrDiff || opts.kind == CacheKindMrDiff {
				scheduleBackgroundRefresh(path, key, opts)
				return ViewLookupResult[T]{
					Rendered:  cached.Rendered,
					SourceURL: cached.SourceURL,
					Payload:   payload,
					Status:    CacheStatusStale,
					FetchedAt: cached.FetchedAt,
				}, nil
			} else {
				res, err := fetchAndStore(path, key, startEpoch, opts, CacheStatusRefreshed)
				if err == nil {
					return res, nil
				}
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return ViewLookupResult[T]{}, err
				}
				return ViewLookupResult[T]{
					Rendered:  cached.Rendered,
					SourceURL: cached.SourceURL,
					Payload:   payload,
					Status:    CacheStatusStale,
					FetchedAt: cached.FetchedAt,
				}, nil
			}
		}
	}

	return fetchAndStore(path, key, startEpoch, opts, CacheStatusMiss)
}

func fetchAndStore[T any](
	path, key string,
	startEpoch int64,
	opts cacheLookupOptions[T],
	status CacheStatus,
) (ViewLookupResult[T], error) {
	fresh, err := opts.fetchFresh()
	if err != nil {
		return ViewLookupResult[T]{}, err
	}
	fetchedAt := nowFunc().UnixMilli()
	payloadRaw, err := json.Marshal(fresh.Payload)
	if err == nil {
		cacheMu.Lock()
		ensureCacheFreshLocked(path)
		if startEpoch == invalidationEpoch {
			cacheData.Rows[key] = cacheEntry{
				AuthKey:         opts.authKey,
				Repo:            normalizeCacheRepo(opts.repo),
				Kind:            opts.kind,
				Number:          opts.number,
				IncludeComments: opts.includeComments,
				FetchedAt:       fetchedAt,
				Payload:         payloadRaw,
				Rendered:        fresh.Rendered,
				SourceURL:       fresh.SourceURL,
			}
			saveCacheLocked(path)
		}
		cacheMu.Unlock()
	}
	return ViewLookupResult[T]{
		Rendered:  fresh.Rendered,
		SourceURL: fresh.SourceURL,
		Payload:   fresh.Payload,
		Status:    status,
		FetchedAt: fetchedAt,
	}, nil
}

func scheduleBackgroundRefresh[T any](path, key string, opts cacheLookupOptions[T]) {
	inflightMu.Lock()
	if _, busy := inflightRefreshes[key]; busy {
		inflightMu.Unlock()
		return
	}
	inflightRefreshes[key] = struct{}{}
	inflightMu.Unlock()

	cacheMu.Lock()
	startEpoch := invalidationEpoch
	cacheMu.Unlock()

	go func() {
		defer func() {
			inflightMu.Lock()
			delete(inflightRefreshes, key)
			inflightMu.Unlock()
		}()
		_, _ = fetchAndStore(path, key, startEpoch, opts, CacheStatusRefreshed)
	}()
}

// RetainPrDiffGeneration stores an immutable pr-diff generation for `?at=<gen>`.
// upstream: tools/github-cache.ts:415-475
func RetainPrDiffGeneration(
	sdkCtx sdk.Context,
	repo string,
	number int,
	fetchedAt int64,
	rendered string,
	files []PrDiffFile,
) (int64, bool) {
	authKey := resolveGithubCacheAuthKey("")
	if authKey == "" {
		return 0, false
	}
	path := resolveCacheFilePath(sdkCtx)
	payloadRaw, err := json.Marshal(PrDiffPayload{Files: files})
	if err != nil {
		return 0, false
	}
	if fetchedAt <= 0 {
		fetchedAt = nowFunc().UnixMilli()
	}
	retainedAt := nowFunc().UnixMilli()

	cacheMu.Lock()
	defer cacheMu.Unlock()
	ensureCacheFreshLocked(path)
	for range maxGenIDSteps {
		gk := genKey(authKey, repo, CacheKindPrDiff, number, false, fetchedAt)
		if existing, ok := cacheData.Generations[gk]; ok {
			if string(existing.Payload) == string(payloadRaw) && existing.Rendered == rendered {
				existing.RetainedAt = retainedAt
				cacheData.Generations[gk] = existing
				saveCacheLocked(path)
				return fetchedAt, true
			}
			fetchedAt++
			continue
		}
		cacheData.Generations[gk] = generationEntry{
			cacheEntry: cacheEntry{
				AuthKey:         authKey,
				Repo:            normalizeCacheRepo(repo),
				Kind:            CacheKindPrDiff,
				Number:          number,
				IncludeComments: false,
				FetchedAt:       fetchedAt,
				Payload:         payloadRaw,
				Rendered:        rendered,
			},
			RetainedAt: retainedAt,
		}
		saveCacheLocked(path)
		return fetchedAt, true
	}
	return 0, false
}

// GetPinnedPrDiff loads one retained pr-diff generation, honoring cache enabled
// and hard TTL from retainedAt.
// upstream: tools/gh-pr-diff.ts:514-538
func GetPinnedPrDiff(sdkCtx sdk.Context, repo string, number int, fetchedAt int64) (ViewLookupResult[PrDiffPayload], bool) {
	authKey := resolveGithubCacheAuthKey("")
	if authKey == "" {
		return ViewLookupResult[PrDiffPayload]{}, false
	}
	ttl := CacheSettingsFn(sdkCtx)
	path := resolveCacheFilePath(sdkCtx)
	now := nowFunc()

	cacheMu.Lock()
	defer cacheMu.Unlock()
	ensureCacheFreshLocked(path)
	sweepIfDueLocked(path, ttl.HardTTL, now)
	if !ttl.Enabled {
		return ViewLookupResult[PrDiffPayload]{}, false
	}
	gk := genKey(authKey, repo, CacheKindPrDiff, number, false, fetchedAt)
	gen, ok := cacheData.Generations[gk]
	if !ok {
		return ViewLookupResult[PrDiffPayload]{}, false
	}
	if time.Duration(now.UnixMilli()-gen.RetainedAt)*time.Millisecond > ttl.HardTTL {
		delete(cacheData.Generations, gk)
		saveCacheLocked(path)
		return ViewLookupResult[PrDiffPayload]{}, false
	}
	var payload PrDiffPayload
	if json.Unmarshal(gen.Payload, &payload) != nil {
		return ViewLookupResult[PrDiffPayload]{}, false
	}
	payload.Unified = gen.Rendered
	return ViewLookupResult[PrDiffPayload]{
		Rendered:  gen.Rendered,
		SourceURL: gen.SourceURL,
		Payload:   payload,
		Status:    CacheStatusFresh,
		FetchedAt: gen.FetchedAt,
	}, true
}

// InvalidateAllForNumber drops every cached row for a given issue/PR number.
// upstream: tools/github-cache.ts:639-652
func InvalidateAllForNumber(sdkCtx sdk.Context, number int, repo string) {
	path := resolveCacheFilePath(sdkCtx)
	normRepo := ""
	if repo != "" {
		normRepo = normalizeCacheRepo(repo)
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	invalidationEpoch++
	ensureCacheFreshLocked(path)
	changed := false
	for k, row := range cacheData.Rows {
		if row.Number == number && (normRepo == "" || row.Repo == normRepo) {
			delete(cacheData.Rows, k)
			changed = true
		}
	}
	if changed {
		saveCacheLocked(path)
	}
}

// FormatFreshnessNote mirrors tools/github-cache.ts:962-975.
func FormatFreshnessNote(status CacheStatus, fetchedAtMs int64, now time.Time) string {
	if status == CacheStatusMiss || status == CacheStatusRefreshed {
		return "Fetched live"
	}
	if status == CacheStatusDisabled {
		return "Cache disabled; fetched live"
	}
	ageSec := int(math.Max(0, math.Round(float64(now.UnixMilli()-fetchedAtMs)/1000.0)))
	var human string
	switch {
	case ageSec < 60:
		human = fmt.Sprintf("%ds ago", ageSec)
	case ageSec < 3600:
		human = fmt.Sprintf("%dm ago", int(math.Round(float64(ageSec)/60.0)))
	default:
		human = fmt.Sprintf("%dh ago", int(math.Round(float64(ageSec)/3600.0)))
	}
	if status == CacheStatusStale {
		return fmt.Sprintf("WARNING: showing cached content from %s; live refresh failed or is still running", human)
	}
	return fmt.Sprintf("Cached: %s", human)
}

// ResetForTests clears in-memory cache state, auth key memo, and memoized repo lookups.
func ResetForTests() {
	cacheMu.Lock()
	invalidationEpoch++
	cacheLoadedPath = ""
	cacheLoadedMtime = 0
	cacheLoadedSize = 0
	cacheData = emptyDiskCache()
	lastSweepAt = time.Time{}
	cachePathOverride = ""
	nowFunc = time.Now
	cacheMu.Unlock()
	authMemoMu.Lock()
	clear(authMemo)
	authMemoMu.Unlock()
	clearGhTrustedHostsCache("")
	defaultRepoMemo.clear()
	defaultGitlabProjectMemo.clear()
}
