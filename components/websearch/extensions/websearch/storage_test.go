package websearch

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// Twins of test/fetch-cache-storage.test.mjs that exercise storage directly. The cases that go
// through the fetch_content and get_search_content tools live in tools_test.go (same file key).

func withClock(t *testing.T, ms int64) {
	t.Helper()
	prev := nowMs
	nowMs = func() int64 { return ms }
	t.Cleanup(func() { nowMs = prev })
}

func storageEnv(t *testing.T) string {
	t.Helper()
	_, agent := isolate(t)
	ResetCaches()
	ClearResults()
	t.Cleanup(ClearResults)
	return agent
}

func fetchedData(id, content string) *StoredSearchData {
	return &StoredSearchData{
		ID: id, Type: "fetch", Timestamp: nowMs(),
		URLs: []ExtractedContent{{URL: "https://example.com/" + id, Title: id, Content: content}},
	}
}

func restoreEntry(t *testing.T, data any) {
	t.Helper()
	raw, err := json.Marshal(data)
	noErr(t, err)
	RestoreFromEntries([]CustomEntry{{CustomType: "web-search-results", Data: raw}})
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	noErr(t, err)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func ptr[T any](v T) *T { return &v }

const ttl = 60 * 60 * 1000

func TestUpstream_fetch_cache_storage(t *testing.T) {
	const f = "fetch-cache-storage"

	tw(t, f, "legacy inline fetched session entries remain readable", func(t *testing.T) {
		storageEnv(t)
		restoreEntry(t, map[string]any{"id": "legacy-fetch", "type": "fetch", "timestamp": nowMs(),
			"urls": []any{map[string]any{"url": "https://example.com/legacy", "title": "Legacy", "content": "legacy inline content", "error": nil}}})
		got := GetResult("legacy-fetch")
		if got == nil || got.URLs[0].Content != "legacy inline content" {
			t.Fatalf("%+v", got)
		}
	})

	tw(t, f, "cache pruning reclaims expired in-memory fetch payloads without changing the retrieval window or history", func(t *testing.T) {
		storageEnv(t)
		startedAt := time.Now().UnixMilli()
		withClock(t, startedAt)
		sessionEntry := StoreFetchedContentResult("expired", fetchedData("expired", "expired payload"))
		snapshot, _ := json.Marshal(sessionEntry)
		cachePath := filepath.Join(FetchCacheDir(), sessionEntry.FetchCache.Key)
		when := time.UnixMilli(startedAt)
		noErr(t, os.Chtimes(cachePath, when, when))
		search := &StoredSearchData{ID: "search", Type: "search", Timestamp: startedAt, Queries: []QueryResultData{}}
		research := &StoredSearchData{ID: "research", Type: "research", Timestamp: startedAt, Artifact: map[string]any{"answer": "keep"}}
		StoreResult(search.ID, search)
		StoreResult(research.ID, research)

		withClock(t, startedAt+ttl-1)
		noErr(t, PruneExpiredFetchCache(0, nil))
		find := func(id string) *StoredSearchData {
			for _, d := range GetAllResults() {
				if d.ID == id {
					return d
				}
			}
			return nil
		}
		if find("expired").URLs[0].Content != "expired payload" || GetResult("expired").URLs[0].Content != "expired payload" {
			t.Fatal("still inside the window")
		}
		if !contains(names(t, FetchCacheDir()), sessionEntry.FetchCache.Key) {
			t.Fatal("cache file must survive before the ttl")
		}
		StoreFetchedContentResult("fresh", fetchedData("fresh", "fresh payload"))

		withClock(t, startedAt+ttl)
		noErr(t, PruneExpiredFetchCache(0, nil))
		if contains(names(t, FetchCacheDir()), sessionEntry.FetchCache.Key) {
			t.Fatal("expired cache file must be removed")
		}
		results := GetAllResults() // inspect before GetResult can lazily expire the payload itself
		if len(results) != 4 {
			t.Fatalf("%d", len(results))
		}
		expired := find("expired")
		if expired.URLs[0].Content != "" || deref(expired.URLs[0].Error) != "Cached fetched content is missing or expired" {
			t.Fatalf("%+v", expired.URLs[0])
		}
		if !reflect.DeepEqual(expired.URLMetadata, sessionEntry.URLMetadata) {
			t.Fatal("metadata changed")
		}
		if find("fresh").URLs[0].Content != "fresh payload" || find("search") != search || find("research") != research {
			t.Fatal("unrelated results changed")
		}
		after, _ := json.Marshal(sessionEntry)
		if string(after) != string(snapshot) {
			t.Fatal("session history entry changed")
		}
		if deref(GetResult("expired").URLs[0].Error) != "Cached fetched content is missing or expired" {
			t.Fatal()
		}
	})

	tw(t, f, "storing a later fetch reclaims expired in-memory payloads before retrieval", func(t *testing.T) {
		storageEnv(t)
		startedAt := time.Now().UnixMilli()
		withClock(t, startedAt)
		StoreFetchedContentResult("earlier", fetchedData("earlier", "earlier payload"))
		withClock(t, startedAt+ttl)
		StoreFetchedContentResult("later", fetchedData("later", "later payload"))
		var earlier, later string
		for _, d := range GetAllResults() {
			switch d.ID {
			case "earlier":
				earlier = d.URLs[0].Content
			case "later":
				later = d.URLs[0].Content
			}
		}
		if earlier != "" || later != "later payload" {
			t.Fatalf("%q %q", earlier, later)
		}
	})

	tw(t, f, "cache pruning reclaims expired inline payloads even without a disk cache", func(t *testing.T) {
		storageEnv(t)
		startedAt := time.Now().UnixMilli()
		withClock(t, startedAt)
		legacy := fetchedData("legacy", "legacy payload")
		restoreEntry(t, legacy)
		// An invalid cache id exercises the in-memory fallback after a cache write failure.
		failed := StoreFetchedContentResult("failed/id", fetchedData("failed/id", "fallback payload"))
		if failed.FetchCacheError == "" {
			t.Fatal("expected a cache error")
		}
		noErr(t, os.RemoveAll(FetchCacheDir()))

		contents := func() []string {
			var out []string
			for _, d := range GetAllResults() {
				out = append(out, d.URLs[0].Content)
			}
			return out
		}
		noErr(t, PruneExpiredFetchCache(startedAt+ttl-1, nil))
		if !reflect.DeepEqual(contents(), []string{"legacy payload", "fallback payload"}) {
			t.Fatalf("%v", contents())
		}
		noErr(t, PruneExpiredFetchCache(startedAt+ttl, nil))
		if !reflect.DeepEqual(contents(), []string{"", ""}) {
			t.Fatalf("%v", contents())
		}
		for _, d := range GetAllResults() {
			if deref(d.URLs[0].Error) != "Cached fetched content is missing or expired" {
				t.Fatalf("%+v", d.URLs[0])
			}
		}
		if legacy.URLs[0].Content != "legacy payload" {
			t.Fatal("the restored input must not be mutated")
		}
	})

	tw(t, f, "PI_WEB_ACCESS_CACHE_ROOT moves only the fetched-content cache, into a folder it owns", func(t *testing.T) {
		agent := storageEnv(t)
		root := t.TempDir()
		userFile := filepath.Join(root, "notes.json")
		noErr(t, os.WriteFile(userFile, []byte("{}"), 0o600))
		epoch := time.Unix(0, 0)
		noErr(t, os.Chtimes(userFile, epoch, epoch))
		t.Setenv("PI_WEB_ACCESS_CACHE_ROOT", root)
		sessionEntry := StoreFetchedContentResult("isolated", fetchedData("isolated", "isolated payload"))
		noErr(t, PruneExpiredFetchCache(0, nil))
		if !reflect.DeepEqual(names(t, filepath.Join(root, "web-search-cache")), []string{"isolated.json"}) ||
			!reflect.DeepEqual(names(t, root), []string{"notes.json", "web-search-cache"}) ||
			contains(names(t, agent), "web-search-cache") {
			t.Fatal("cache root misplaced")
		}
		ClearResults()
		restoreEntry(t, sessionEntry)
		if got := GetResult("isolated"); got == nil || len(got.URLs) == 0 || got.URLs[0].Content != "isolated payload" {
			t.Fatalf("%+v", got)
		}
	})

	tw(t, f, "cache pruning evicts the oldest entries by count and bytes", func(t *testing.T) {
		storageEnv(t)
		dir := FetchCacheDir()
		noErr(t, os.MkdirAll(dir, 0o700))
		now := time.Now().UnixMilli()
		for _, c := range []struct {
			name, content string
			age           int64
		}{{"oldest.json", "1111", 30}, {"middle.json", "222222", 20}, {"newest.json", "33333333", 10}} {
			p := filepath.Join(dir, c.name)
			noErr(t, os.WriteFile(p, []byte(c.content), 0o600))
			when := time.UnixMilli(now - c.age*1000)
			noErr(t, os.Chtimes(p, when, when))
		}
		noErr(t, PruneExpiredFetchCache(now, &PartialCacheLimits{MaxEntries: ptr(2.0), MaxBytes: ptr(1024.0)}))
		if !reflect.DeepEqual(names(t, dir), []string{"middle.json", "newest.json"}) {
			t.Fatalf("%v", names(t, dir))
		}
		noErr(t, PruneExpiredFetchCache(now, &PartialCacheLimits{MaxEntries: ptr(10.0), MaxBytes: ptr(8.0)}))
		if !reflect.DeepEqual(names(t, dir), []string{"newest.json"}) {
			t.Fatalf("%v", names(t, dir))
		}
		wantErr(t, PruneExpiredFetchCache(now, &PartialCacheLimits{MaxEntries: ptr(0.0)}), `finite positive integers`)
		wantErr(t, PruneExpiredFetchCache(now, &PartialCacheLimits{MaxBytes: ptr(math.Inf(1))}), `finite positive integers`)
	})

	tw(t, f, "default cache pruning keeps the newest 128 entries", func(t *testing.T) {
		storageEnv(t)
		dir := FetchCacheDir()
		noErr(t, os.MkdirAll(dir, 0o700))
		now := time.Now().UnixMilli()
		for i := 0; i < 129; i++ {
			p := filepath.Join(dir, "entry-"+pad3(i)+".json")
			noErr(t, os.WriteFile(p, []byte("{}"), 0o600))
			when := time.UnixMilli(now - int64(129-i)*1000)
			noErr(t, os.Chtimes(p, when, when))
		}
		noErr(t, PruneExpiredFetchCache(now, nil))
		remaining := names(t, dir)
		if len(remaining) != 128 || contains(remaining, "entry-000.json") || !contains(remaining, "entry-128.json") {
			t.Fatalf("%d", len(remaining))
		}
	})

	tw(t, f, "default cache pruning enforces the 128 MiB byte limit", func(t *testing.T) {
		storageEnv(t)
		dir := FetchCacheDir()
		noErr(t, os.MkdirAll(dir, 0o700))
		now := time.Now().UnixMilli()
		for _, c := range []struct {
			name string
			age  int64
		}{{"older.json", 20}, {"newer.json", 10}} {
			p := filepath.Join(dir, c.name)
			fh, err := os.Create(p)
			noErr(t, err)
			noErr(t, fh.Truncate(65*1024*1024))
			noErr(t, fh.Close())
			when := time.UnixMilli(now - c.age*1000)
			noErr(t, os.Chtimes(p, when, when))
		}
		noErr(t, PruneExpiredFetchCache(now, nil))
		if !reflect.DeepEqual(names(t, dir), []string{"newer.json"}) {
			t.Fatalf("%v", names(t, dir))
		}
	})

	tw(t, f, "cache pruning removes only stale owned temp files", func(t *testing.T) {
		storageEnv(t)
		dir := FetchCacheDir()
		noErr(t, os.MkdirAll(dir, 0o700))
		now := time.Now().UnixMilli()
		stale := []string{"old.json.123.456.tmp", "new.json.123.456." + strings.Repeat("a", 32) + ".tmp"}
		fresh := "fresh.json.123.456." + strings.Repeat("b", 32) + ".tmp"
		preserved := []string{"foreign.tmp", "old.json.not-ours.tmp", fresh}
		for _, name := range append(append([]string{}, stale...), preserved...) {
			p := filepath.Join(dir, name)
			noErr(t, os.WriteFile(p, []byte(name), 0o600))
			if name != fresh {
				when := time.UnixMilli(now - 2*60*60*1000)
				noErr(t, os.Chtimes(p, when, when))
			}
		}
		noErr(t, PruneExpiredFetchCache(now, nil))
		sort.Strings(preserved)
		if !reflect.DeepEqual(names(t, dir), preserved) {
			t.Fatalf("%v", names(t, dir))
		}
	})

	tw(t, f, "cache pruning corrects directory and entry permissions", func(t *testing.T) {
		if os.PathSeparator == '\\' {
			t.Skip("POSIX modes")
		}
		storageEnv(t)
		dir := FetchCacheDir()
		noErr(t, os.MkdirAll(dir, 0o700))
		entry := filepath.Join(dir, "permissions.json")
		noErr(t, os.WriteFile(entry, []byte("{}"), 0o600))
		noErr(t, os.Chmod(dir, 0o777))
		noErr(t, os.Chmod(entry, 0o666))
		noErr(t, PruneExpiredFetchCache(0, nil))
		di, _ := os.Stat(dir)
		ei, _ := os.Stat(entry)
		if di.Mode().Perm() != 0o700 || ei.Mode().Perm() != 0o600 {
			t.Fatalf("%o %o", di.Mode().Perm(), ei.Mode().Perm())
		}
	})

	tw(t, f, "cache write and rename failures degrade without leaving temp files", func(t *testing.T) {
		storageEnv(t)
		// Adapted: the original makes JSON.stringify fail with a circular reference; a Go value
		// cannot be circular, so an unrepresentable number (NaN) fails the serialization instead.
		bad := fetchedData("unwritable", "x")
		bad.URLs[0].Duration = ptr(math.NaN())
		failed := StoreFetchedContentResult("unwritable", bad)
		if failed.FetchCache != nil || !strings.Contains(strings.ToLower(failed.FetchCacheError), "unsupported value") {
			t.Fatalf("%+v", failed)
		}
		if contains(names(t, filepath.Dir(FetchCacheDir())), "web-search-cache") {
			t.Fatal("no cache directory may be created for a failed serialization")
		}
		written := StoreFetchedContentResult("normal", fetchedData("normal", "cached content"))
		if written.FetchCache == nil {
			t.Fatal("normal write must work")
		}
		noErr(t, os.Mkdir(filepath.Join(FetchCacheDir(), "blocked.json"), 0o700))
		blocked := StoreFetchedContentResult("blocked", fetchedData("blocked", "cached content"))
		if blocked.FetchCache != nil {
			t.Fatal("a directory in the way must fail the write")
		}
		for _, n := range names(t, FetchCacheDir()) {
			if strings.HasSuffix(n, ".tmp") {
				t.Fatalf("temp file left: %s", n)
			}
		}
	})

	tw(t, f, "corrupt cache files remain unavailable without throwing", func(t *testing.T) {
		storageEnv(t)
		dir := FetchCacheDir()
		noErr(t, os.MkdirAll(dir, 0o700))
		noErr(t, os.WriteFile(filepath.Join(dir, "corrupt.json"), []byte("not json"), 0o600))
		restoreEntry(t, map[string]any{"id": "corrupt", "type": "fetch", "timestamp": nowMs(),
			"fetchCache":  map[string]any{"version": 1, "key": "corrupt.json", "storedAt": nowMs()},
			"urlMetadata": []any{map[string]any{"url": "https://example.com/corrupt", "title": "corrupt", "error": nil, "contentLength": 8}}})
		got := GetResult("corrupt")
		if got.URLs[0].Content != "" || !strings.Contains(deref(got.URLs[0].Error), "could not be read") {
			t.Fatalf("%+v", got.URLs[0])
		}
	})

	tw(t, f, "cache file symlinks are not followed", func(t *testing.T) {
		if os.PathSeparator == '\\' {
			t.Skip("POSIX symlinks")
		}
		agent := storageEnv(t)
		dir := FetchCacheDir()
		noErr(t, os.MkdirAll(dir, 0o700))
		target := filepath.Join(agent, "outside.json")
		raw, _ := json.Marshal(fetchedData("linked", "cached content"))
		noErr(t, os.WriteFile(target, raw, 0o600))
		noErr(t, os.Symlink(target, filepath.Join(dir, "linked.json")))
		restoreEntry(t, map[string]any{"id": "linked", "type": "fetch", "timestamp": nowMs(),
			"fetchCache":  map[string]any{"version": 1, "key": "linked.json", "storedAt": nowMs()},
			"urlMetadata": []any{map[string]any{"url": "https://example.com/linked", "title": "linked", "error": nil, "contentLength": 14}}})
		got := GetResult("linked")
		if got.URLs[0].Content != "" || !strings.Contains(deref(got.URLs[0].Error), "not a regular file") {
			t.Fatalf("%+v", got.URLs[0])
		}
		if b, _ := os.ReadFile(target); !strings.Contains(string(b), "cached content") {
			t.Fatal("the link target must be untouched")
		}
	})

	tw(t, f, "cache directory symlinks are rejected for writes and deletes", func(t *testing.T) {
		if os.PathSeparator == '\\' {
			t.Skip("POSIX symlinks")
		}
		agent := storageEnv(t)
		dir := FetchCacheDir()
		outside := filepath.Join(agent, "outside-cache")
		noErr(t, os.MkdirAll(filepath.Dir(dir), 0o700))
		noErr(t, os.Mkdir(outside, 0o700))
		noErr(t, os.Symlink(outside, dir))
		rejected := StoreFetchedContentResult("dir-link", fetchedData("dir-link", "cached content"))
		if rejected.FetchCache != nil || !strings.Contains(rejected.FetchCacheError, "not a safe directory") {
			t.Fatalf("%+v", rejected)
		}
		if len(names(t, outside)) != 0 {
			t.Fatal("nothing may be written through the link")
		}
		noErr(t, os.Remove(dir))
		stored := StoreFetchedContentResult("delete-link", fetchedData("delete-link", "cached content"))
		if stored.FetchCache == nil {
			t.Fatal("store")
		}
		noErr(t, os.RemoveAll(dir))
		noErr(t, os.MkdirAll(outside, 0o700))
		noErr(t, os.WriteFile(filepath.Join(outside, stored.FetchCache.Key), []byte("outside"), 0o600))
		noErr(t, os.Symlink(outside, dir))
		if !DeleteResult("delete-link") {
			t.Fatal("the in-memory entry must be deleted")
		}
		if b, _ := os.ReadFile(filepath.Join(outside, stored.FetchCache.Key)); string(b) != "outside" {
			t.Fatal("the file behind the link must survive")
		}
	})

	// The three cases that run through the fetch_content and get_search_content tools are twinned in tools_test.go.
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func pad3(i int) string { return fmt.Sprintf("%03d", i) }
