package websearch

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Port of storage.ts: the in-memory result store behind get_search_content, and the on-disk
// cache that keeps fetched page content out of the session file. A fetched page is written to a
// private cache file; the session entry carries only bounded metadata and a reference.

const (
	cacheTTLMs        = 60 * 60 * 1000
	fetchCacheDirName = "web-search-cache"
	fetchCacheVersion = 1
	maxMetadataText   = 8192
)

var (
	cacheKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+\.json$`)
	cacheTmpPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+\.json\.\d+\.\d+(?:\.[a-f0-9]{32})?\.tmp$`)
	cacheIDPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

const (
	defaultMaxEntries = 128
	defaultMaxBytes   = 128 * 1024 * 1024
)

// SearchResult is one source of a search.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// QueryResultData is the stored outcome of one query.
type QueryResultData struct {
	Query     string         `json:"query"`
	Answer    string         `json:"answer"`
	Results   []SearchResult `json:"results"`
	Error     *string        `json:"error"`
	Provider  string         `json:"provider,omitempty"`
	Providers []string       `json:"providers,omitempty"`
}

// Image is a base64 image block.
type Image struct {
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// VideoFrame is an extracted video frame (slice 2 fills it).
type VideoFrame struct {
	Data      string `json:"data"`
	MimeType  string `json:"mimeType"`
	Timestamp string `json:"timestamp"`
}

// ExtractedContent is one fetched page. The JSON shape is the original's, so session files are
// interchangeable between the two implementations.
type ExtractedContent struct {
	URL       string       `json:"url"`
	Title     string       `json:"title"`
	Content   string       `json:"content"`
	Error     *string      `json:"error"`
	Thumbnail *Image       `json:"thumbnail,omitempty"`
	Frames    []VideoFrame `json:"frames,omitempty"`
	Duration  *float64     `json:"duration,omitempty"`
	MimeType  string       `json:"mimeType,omitempty"`
	Status    *int         `json:"status,omitempty"`

	declaredLinks []DeclaredWebLink
}

// FetchCacheRef points a session entry at its cache file.
type FetchCacheRef struct {
	Version  int    `json:"version"`
	Key      string `json:"key"`
	StoredAt int64  `json:"storedAt"`
}

// StoredFetchURLMetadata is the bounded per-URL metadata kept in the session entry.
type StoredFetchURLMetadata struct {
	URL           string   `json:"url"`
	Title         string   `json:"title"`
	Error         *string  `json:"error"`
	ContentLength int      `json:"contentLength"`
	MimeType      string   `json:"mimeType,omitempty"`
	Status        *int     `json:"status,omitempty"`
	Duration      *float64 `json:"duration,omitempty"`
}

// StoredSearchData is one retained result (search, fetch or research).
type StoredSearchData struct {
	ID              string                   `json:"id"`
	Type            string                   `json:"type"`
	Timestamp       int64                    `json:"timestamp"`
	Queries         []QueryResultData        `json:"queries,omitempty"`
	URLs            []ExtractedContent       `json:"urls,omitempty"`
	Artifact        any                      `json:"artifact,omitempty"`
	FetchCache      *FetchCacheRef           `json:"fetchCache,omitempty"`
	URLMetadata     []StoredFetchURLMetadata `json:"urlMetadata,omitempty"`
	FetchCacheError string                   `json:"fetchCacheError,omitempty"`
}

// PartialCacheLimits override the default limits; nil keeps the default.
type PartialCacheLimits struct{ MaxEntries, MaxBytes *float64 }

// CustomEntry is a session `custom` entry.
type CustomEntry struct {
	CustomType string
	Data       json.RawMessage
}

type cacheLimits struct{ maxEntries, maxBytes int64 }

type cacheFile struct {
	name    string
	size    int64
	mtimeMs int64
	info    os.FileInfo
}

var (
	storeMu sync.Mutex
	stored  = map[string]*StoredSearchData{}
	order   []string
)

// GenerateID makes a response id: the base-36 time plus six random base-36 characters.
func GenerateID() string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	out := strconv.FormatInt(nowMs(), 36)
	for i := 0; i < 6; i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(36))
		out += string(alphabet[n.Int64()])
	}
	return out
}

// FetchCacheDir is the cache folder. It always appends its own folder name: pruning deletes
// stale *.json files, so an override must never point pruning at a directory of user files.
func FetchCacheDir() string {
	root := os.Getenv("PI_WEB_ACCESS_CACHE_ROOT")
	if root == "" {
		root = ConfigDir()
	}
	return filepath.Join(root, fetchCacheDirName)
}

func fetchCachePath(key string) (string, bool) {
	if !cacheKeyPattern.MatchString(key) {
		return "", false
	}
	return filepath.Join(FetchCacheDir(), key), true
}

func cacheKeyForID(id string) (string, error) {
	if !cacheIDPattern.MatchString(id) {
		return "", fmt.Errorf("Invalid fetched content cache id: %s", id)
	}
	return id + ".json", nil
}

func truncateMetadataText(s string) string {
	if s == "" {
		return ""
	}
	if jsLen(s) > maxMetadataText {
		return jsSlice(s, 0, maxMetadataText) + "..."
	}
	return s
}

func metadataForURLs(urls []ExtractedContent) []StoredFetchURLMetadata {
	out := make([]StoredFetchURLMetadata, 0, len(urls))
	for _, u := range urls {
		m := StoredFetchURLMetadata{URL: truncateMetadataText(u.URL), Title: truncateMetadataText(u.Title), ContentLength: jsLen(u.Content), Status: u.Status, Duration: u.Duration}
		if u.Error != nil && *u.Error != "" {
			e := truncateMetadataText(*u.Error)
			m.Error = &e
		}
		if u.MimeType != "" {
			m.MimeType = truncateMetadataText(u.MimeType)
		}
		out = append(out, m)
	}
	return out
}

func resolveLimits(p *PartialCacheLimits) (cacheLimits, error) {
	entries, bytes := float64(defaultMaxEntries), float64(defaultMaxBytes)
	if p != nil {
		if p.MaxEntries != nil {
			entries = *p.MaxEntries
		}
		if p.MaxBytes != nil {
			bytes = *p.MaxBytes
		}
	}
	ok := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v == math.Trunc(v) && v > 0 }
	if !ok(entries) || !ok(bytes) {
		return cacheLimits{}, errors.New("Fetched content cache limits must be finite positive integers")
	}
	return cacheLimits{int64(entries), int64(bytes)}, nil
}

func enforceMode(f *os.File, mode os.FileMode) error {
	err := f.Chmod(mode)
	if err != nil && os.PathSeparator == '\\' {
		return nil
	}
	return err
}

var errNotDir = errors.New("Fetched content cache path is not a safe directory")

// safeFetchCacheDir returns the cache directory after checking that it is a real directory (not a
// link) and tightening it to 0700; ("", nil) means it does not exist and create was false.
func safeFetchCacheDir(create bool) (string, error) {
	dir := FetchCacheDir()
	if create {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
	}
	before, err := os.Lstat(dir)
	if err != nil {
		if !create && errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return "", errNotDir
	}
	if os.PathSeparator != '\\' {
		f, err := os.OpenFile(dir, os.O_RDONLY|oDirectory|oNoFollow, 0)
		if err != nil {
			return "", err
		}
		opened, err := f.Stat()
		if err != nil || !opened.IsDir() || !os.SameFile(before, opened) {
			_ = f.Close()
			return "", errors.New("Fetched content cache directory changed while opening")
		}
		err = enforceMode(f, 0o700)
		_ = f.Close()
		if err != nil {
			return "", err
		}
	}
	after, err := os.Lstat(dir)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(before, after) {
		return "", errors.New("Fetched content cache directory changed while securing it")
	}
	return dir, nil
}

func openRegularFile(path string) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, nil, errors.New("Fetched content cache entry is not a regular file")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|oNoFollow, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) {
		_ = f.Close()
		return nil, nil, errors.New("Fetched content cache entry changed while opening")
	}
	return f, info, nil
}

// unlinkCacheFile is true once the entry is gone; false when the directory or entry changed
// underneath us or could not be removed.
func unlinkCacheFile(dir string, file cacheFile) bool {
	root, err := os.Lstat(dir)
	if err != nil || root.Mode()&os.ModeSymlink != 0 || !root.IsDir() {
		return false
	}
	path := filepath.Join(dir, file.name)
	current, err := os.Lstat(path)
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || (file.info != nil && !os.SameFile(file.info, current)) {
		return false
	}
	if err := os.Remove(path); err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	return true
}

type reservation struct {
	key   string
	bytes int64
}

func pruneFetchCache(now int64, limits cacheLimits, preferredKey string, res *reservation) bool {
	dir, err := safeFetchCacheDir(false)
	if err != nil {
		return false
	}
	if dir == "" {
		return true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	var files []cacheFile
	for _, e := range entries {
		name := e.Name()
		if !cacheKeyPattern.MatchString(name) && !cacheTmpPattern.MatchString(name) {
			continue
		}
		f, info, err := openRegularFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		err = enforceMode(f, 0o600)
		_ = f.Close()
		if err != nil {
			continue
		}
		file := cacheFile{name: name, size: info.Size(), mtimeMs: info.ModTime().UnixMilli(), info: info}
		if now-file.mtimeMs >= cacheTTLMs {
			if !unlinkCacheFile(dir, file) {
				return false
			}
			continue
		}
		if cacheKeyPattern.MatchString(name) {
			files = append(files, file)
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].mtimeMs != files[j].mtimeMs {
			return files[i].mtimeMs < files[j].mtimeMs
		}
		return files[i].name < files[j].name
	})
	projected := func() (int64, int64) {
		var replaced *cacheFile
		if res != nil {
			for i := range files {
				if files[i].name == res.key {
					replaced = &files[i]
				}
			}
		}
		count := int64(len(files))
		var total int64
		for _, f := range files {
			total += f.size
		}
		if res != nil {
			if replaced == nil {
				count++
				total += res.bytes
			} else {
				total += res.bytes - replaced.size
			}
		}
		return count, total
	}
	attempted := map[string]bool{}
	count, total := projected()
	for count > limits.maxEntries || total > limits.maxBytes {
		index := -1
		for i, f := range files {
			if f.name != preferredKey && !attempted[f.name] {
				index = i
				break
			}
		}
		if index < 0 {
			break
		}
		file := files[index]
		attempted[file.name] = true
		if unlinkCacheFile(dir, file) {
			files = append(files[:index], files[index+1:]...)
		}
		count, total = projected()
	}
	return count <= limits.maxEntries && total <= limits.maxBytes
}

func writeFetchCache(data *StoredSearchData) (*FetchCacheRef, error) {
	limits := cacheLimits{defaultMaxEntries, defaultMaxBytes}
	serialized, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	size := int64(len(serialized))
	if size > limits.maxBytes {
		return nil, fmt.Errorf("Fetched content cache entry exceeds %d bytes", limits.maxBytes)
	}
	dir, err := safeFetchCacheDir(true)
	if err != nil {
		return nil, err
	}
	key, err := cacheKeyForID(data.ID)
	if err != nil {
		return nil, err
	}
	if !pruneFetchCache(nowMs(), limits, key, &reservation{key, size}) {
		return nil, errors.New("Fetched content cache could not reserve space for a new entry")
	}
	finalPath := filepath.Join(dir, key)
	rnd := make([]byte, 16)
	_, _ = rand.Read(rnd)
	tmpName := fmt.Sprintf("%s.%d.%d.%s.tmp", key, os.Getpid(), nowMs(), hex.EncodeToString(rnd))
	tmpPath := filepath.Join(dir, tmpName)
	var tmp *cacheFile
	renamed := false
	cleanup := func() {
		if tmp != nil {
			name := tmp.name
			if renamed {
				name = key
			}
			unlinkCacheFile(dir, cacheFile{name: name, info: tmp.info})
		}
	}
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY|oNoFollow, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		tmp = &cacheFile{name: tmpName, info: info}
		err = enforceMode(f, 0o600)
	}
	if err == nil {
		_, err = f.Write(serialized)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		cleanup()
		return nil, err
	}
	if _, err = safeFetchCacheDir(false); err == nil {
		if err = os.Rename(tmpPath, finalPath); err == nil {
			renamed = true
			var written os.FileInfo
			written, err = os.Lstat(finalPath)
			if err == nil && (!written.Mode().IsRegular() || !os.SameFile(written, tmp.info)) {
				err = errors.New("Fetched content cache entry changed after writing")
			}
			if err == nil && !pruneFetchCache(nowMs(), limits, key, nil) {
				err = errors.New("Fetched content cache could not meet its limits after writing")
			}
		}
	}
	if err != nil {
		cleanup()
		return nil, err
	}
	return &FetchCacheRef{Version: fetchCacheVersion, Key: key, StoredAt: nowMs()}, nil
}

func strPtr(s string) *string { return &s }

func createFetchSessionData(data *StoredSearchData, ref *FetchCacheRef, cacheError string) *StoredSearchData {
	out := &StoredSearchData{ID: data.ID, Type: "fetch", Timestamp: data.Timestamp, URLMetadata: metadataForURLs(data.URLs)}
	if ref != nil {
		out.FetchCache = ref
	}
	if cacheError != "" {
		out.FetchCacheError = truncateMetadataText(cacheError)
	}
	return out
}

func fetchURLMetadata(data *StoredSearchData) []StoredFetchURLMetadata {
	if data.URLMetadata != nil {
		return data.URLMetadata
	}
	if isInlineFetchData(data) {
		return metadataForURLs(data.URLs)
	}
	return nil
}

func isInlineFetchData(data *StoredSearchData) bool { return data.Type == "fetch" && data.URLs != nil }

func unavailableFetchData(data *StoredSearchData, reason string) *StoredSearchData {
	out := *data
	out.URLs = []ExtractedContent{}
	for _, m := range fetchURLMetadata(data) {
		out.URLs = append(out.URLs, ExtractedContent{URL: m.URL, Title: m.Title, Content: "", Error: strPtr(reason), MimeType: m.MimeType, Status: m.Status, Duration: m.Duration})
	}
	return &out
}

const errExpired = "Cached fetched content is missing or expired"

func readCachedFetchData(data *StoredSearchData) *StoredSearchData {
	if data.Type != "fetch" {
		return data
	}
	if nowMs()-data.Timestamp >= cacheTTLMs {
		return unavailableFetchData(data, errExpired)
	}
	if isInlineFetchData(data) {
		return data
	}
	if data.FetchCache == nil {
		reason := data.FetchCacheError
		if reason == "" {
			reason = "Cached fetched content is unavailable"
		}
		return unavailableFetchData(data, reason)
	}
	path, ok := fetchCachePath(data.FetchCache.Key)
	if !ok {
		return unavailableFetchData(data, errExpired)
	}
	dir, err := safeFetchCacheDir(false)
	if err != nil {
		return unavailableFetchData(data, "Cached fetched content could not be read: "+err.Error())
	}
	if dir == "" {
		return unavailableFetchData(data, errExpired)
	}
	f, _, err := openRegularFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return unavailableFetchData(data, errExpired)
		}
		return unavailableFetchData(data, "Cached fetched content could not be read: "+err.Error())
	}
	defer f.Close()
	if err := enforceMode(f, 0o600); err != nil {
		return unavailableFetchData(data, "Cached fetched content could not be read: "+err.Error())
	}
	raw := make([]byte, 0, 4096)
	buf := make([]byte, 32*1024)
	for {
		n, rerr := f.Read(buf)
		raw = append(raw, buf[:n]...)
		if rerr != nil {
			break
		}
	}
	parsed, valid := decodeStoredData(raw)
	if !valid || parsed == nil {
		var probe any
		if json.Unmarshal(raw, &probe) != nil {
			return unavailableFetchData(data, "Cached fetched content could not be read: invalid JSON in the cache file")
		}
		return unavailableFetchData(data, "Cached fetched content is invalid")
	}
	if parsed.Type != "fetch" || parsed.ID != data.ID || !isInlineFetchData(parsed) {
		return unavailableFetchData(data, "Cached fetched content is invalid")
	}
	parsed.FetchCache = data.FetchCache
	parsed.URLMetadata = data.URLMetadata
	return parsed
}

func pruneExpiredFetchedResults(now int64) {
	for _, id := range order {
		data := stored[id]
		if data.Type == "fetch" && now-data.Timestamp >= cacheTTLMs {
			stored[id] = unavailableFetchData(data, errExpired)
		}
	}
}

// PruneExpiredFetchCache reclaims expired in-memory fetch payloads and prunes the cache files by
// age, count and size. now == 0 means the current time.
func PruneExpiredFetchCache(now int64, partial *PartialCacheLimits) error {
	limits, err := resolveLimits(partial)
	if err != nil {
		return err
	}
	if now == 0 {
		now = nowMs()
	}
	storeMu.Lock()
	pruneExpiredFetchedResults(now)
	storeMu.Unlock()
	pruneFetchCache(now, limits, "", nil)
	return nil
}

// StoreResult retains a result in memory (search and research results).
func StoreResult(id string, data *StoredSearchData) {
	storeMu.Lock()
	defer storeMu.Unlock()
	setLocked(id, data)
}

func setLocked(id string, data *StoredSearchData) {
	if _, exists := stored[id]; !exists {
		order = append(order, id)
	}
	stored[id] = data
}

// StoreFetchedContentResult retains fetched pages, writes them to the private cache and returns
// the bounded entry for the session history. A failed cache write keeps the payload in memory
// for this session and records the reason in the entry.
func StoreFetchedContentResult(id string, data *StoredSearchData) *StoredSearchData {
	storeMu.Lock()
	pruneExpiredFetchedResults(nowMs())
	storeMu.Unlock()
	ref, err := writeFetchCache(data)
	cacheError := ""
	if err != nil {
		cacheError = "Failed to write fetched content cache: " + err.Error()
	}
	memory := *data
	if ref != nil {
		memory.FetchCache = ref
		memory.URLMetadata = metadataForURLs(data.URLs)
	} else {
		memory.FetchCacheError = cacheError
	}
	storeMu.Lock()
	setLocked(id, &memory)
	storeMu.Unlock()
	return createFetchSessionData(data, ref, cacheError)
}

// GetResult returns a retained result, loading and expiring cache-backed fetches lazily.
func GetResult(id string) *StoredSearchData {
	storeMu.Lock()
	defer storeMu.Unlock()
	data, ok := stored[id]
	if !ok {
		return nil
	}
	loaded := readCachedFetchData(data)
	if loaded != data {
		stored[id] = loaded
	}
	return loaded
}

// GetAllResults lists retained results in insertion order.
func GetAllResults() []*StoredSearchData {
	storeMu.Lock()
	defer storeMu.Unlock()
	out := make([]*StoredSearchData, 0, len(order))
	for _, id := range order {
		out = append(out, stored[id])
	}
	return out
}

// DeleteResult forgets a result and removes its cache file when the directory is safe.
func DeleteResult(id string) bool {
	storeMu.Lock()
	defer storeMu.Unlock()
	data, ok := stored[id]
	if ok && data.FetchCache != nil {
		if dir, err := safeFetchCacheDir(false); err == nil && dir != "" {
			if path, ok := fetchCachePath(data.FetchCache.Key); ok {
				if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() {
					unlinkCacheFile(dir, cacheFile{name: data.FetchCache.Key, size: info.Size(), mtimeMs: info.ModTime().UnixMilli(), info: info})
				}
			}
		}
	}
	if !ok {
		return false
	}
	delete(stored, id)
	for i, k := range order {
		if k == id {
			order = append(order[:i], order[i+1:]...)
			break
		}
	}
	return true
}

// ClearResults forgets every retained result (not the cache files).
func ClearResults() {
	storeMu.Lock()
	stored = map[string]*StoredSearchData{}
	order = nil
	storeMu.Unlock()
}

// decodeStoredData validates raw JSON like isValidStoredData and decodes it.
func decodeStoredData(raw []byte) (*StoredSearchData, bool) {
	var probe map[string]any
	if json.Unmarshal(raw, &probe) != nil || !validStoredProbe(probe) {
		return nil, false
	}
	var data StoredSearchData
	if json.Unmarshal(raw, &data) != nil {
		return nil, false
	}
	return &data, true
}

func validStoredProbe(d map[string]any) bool {
	id, ok := d["id"].(string)
	if !ok || id == "" {
		return false
	}
	typ, _ := d["type"].(string)
	if typ != "search" && typ != "fetch" && typ != "research" {
		return false
	}
	if _, ok := d["timestamp"].(float64); !ok {
		return false
	}
	switch typ {
	case "search":
		_, ok := d["queries"].([]any)
		return ok
	case "fetch":
		if urls, ok := d["urls"].([]any); ok {
			for _, u := range urls {
				if !isInlineFetchedURL(u) {
					return false
				}
			}
			return true
		}
		meta, ok := d["urlMetadata"].([]any)
		if !ok {
			return false
		}
		for _, m := range meta {
			if !isStoredMetadata(m) {
				return false
			}
		}
		if ref, present := d["fetchCache"]; present {
			return isFetchCacheRefProbe(ref)
		}
		if e, present := d["fetchCacheError"]; present {
			_, isStr := e.(string)
			return isStr
		}
		return true
	case "research":
		_, ok := d["artifact"].(map[string]any)
		return ok
	}
	return true
}

func isInlineFetchedURL(v any) bool {
	u, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, a := u["url"].(string)
	_, b := u["title"].(string)
	_, c := u["content"].(string)
	e, present := u["error"]
	_, isStr := e.(string)
	return a && b && c && present && (e == nil || isStr)
}

func isStoredMetadata(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, a := m["url"].(string)
	_, b := m["title"].(string)
	e, present := m["error"]
	_, isStr := e.(string)
	n, isNum := m["contentLength"].(float64)
	if !(a && b && present && (e == nil || isStr) && isNum && n >= 0 && !math.IsInf(n, 0)) {
		return false
	}
	if x, ok := m["mimeType"]; ok {
		if _, isStr := x.(string); !isStr {
			return false
		}
	}
	for _, k := range []string{"status", "duration"} {
		if x, ok := m[k]; ok {
			if _, isNum := x.(float64); !isNum {
				return false
			}
		}
	}
	return true
}

func isFetchCacheRefProbe(v any) bool {
	r, ok := v.(map[string]any)
	if !ok {
		return false
	}
	version, _ := r["version"].(float64)
	key, isKey := r["key"].(string)
	stored, isNum := r["storedAt"].(float64)
	return version == fetchCacheVersion && isKey && cacheKeyPattern.MatchString(key) && isNum && !math.IsInf(stored, 0)
}

// RestoreFromEntries rebuilds the in-memory store from the session branch: every
// `web-search-results` custom entry that is valid and younger than the result lifetime.
func RestoreFromEntries(entries []CustomEntry) {
	storeMu.Lock()
	stored = map[string]*StoredSearchData{}
	order = nil
	now := nowMs()
	storeMu.Unlock()
	_ = PruneExpiredFetchCache(now, nil)
	for _, e := range entries {
		if e.CustomType != "web-search-results" {
			continue
		}
		data, ok := decodeStoredData(e.Data)
		if ok && now-data.Timestamp < cacheTTLMs {
			StoreResult(data.ID, data)
		}
	}
}

var _ = strings.TrimSpace
