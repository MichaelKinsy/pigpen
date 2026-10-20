package websearch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The original reads web-search.json on every call. The port caches the parsed file while a stat
// (size, mtime, file identity) still matches and the file is older than the racy window; these
// cases pin that an edit is always seen and that a quiet file is parsed once.

func cacheConfigPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", dir)
	ResetCaches()
	t.Cleanup(ResetCaches)
	return filepath.Join(dir, configFileName)
}

func writeAged(t *testing.T, path, content string, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func configParseCount() int {
	configMu.Lock()
	defer configMu.Unlock()
	return configParses
}

func provider(t *testing.T) string {
	t.Helper()
	root, err := ReadConfigRoot()
	if err != nil {
		t.Fatal(err)
	}
	s, _ := root["provider"].(string)
	return s
}

func TestReadConfigRootCachesAQuietFile(t *testing.T) {
	path := cacheConfigPath(t)
	writeAged(t, path, `{"provider":"aaaa"}`, time.Minute)
	before := configParseCount()
	for i := range 5 {
		if got := provider(t); got != "aaaa" {
			t.Fatalf("read %d: provider = %q", i, got)
		}
	}
	if n := configParseCount() - before; n != 1 {
		t.Fatalf("a quiet file was parsed %d times in 5 reads, want 1", n)
	}
}

func TestReadConfigRootSeesAnEditOfTheSameSize(t *testing.T) {
	path := cacheConfigPath(t)
	writeAged(t, path, `{"provider":"aaaa"}`, time.Minute)
	if got := provider(t); got != "aaaa" {
		t.Fatalf("provider = %q", got)
	}
	writeAged(t, path, `{"provider":"bbbb"}`, 30*time.Second)
	if got := provider(t); got != "bbbb" {
		t.Fatalf("an edit with the same size and a later mtime was not picked up: %q", got)
	}
}

func TestReadConfigRootSeesAReplacedFileOfTheSameSizeAndTime(t *testing.T) {
	path := cacheConfigPath(t)
	when := time.Now().Add(-time.Minute)
	if err := os.WriteFile(path, []byte(`{"provider":"aaaa"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	if got := provider(t); got != "aaaa" {
		t.Fatalf("provider = %q", got)
	}
	// A new file renamed over the old one keeps size and mtime but not identity.
	next := path + ".next"
	if err := os.WriteFile(next, []byte(`{"provider":"bbbb"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(next, when, when); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, path); err != nil {
		t.Fatal(err)
	}
	if got := provider(t); got != "bbbb" {
		t.Fatalf("a replaced file with the same size and mtime was not picked up: %q", got)
	}
}

func TestReadConfigRootNeverTrustsARecentFile(t *testing.T) {
	path := cacheConfigPath(t)
	if err := os.WriteFile(path, []byte(`{"provider":"aaaa"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := provider(t); got != "aaaa" {
		t.Fatalf("provider = %q", got)
	}
	// Same size and the same mtime on the same file: only the racy-write guard separates this
	// from a cache hit.
	if err := os.WriteFile(path, []byte(`{"provider":"bbbb"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got := provider(t); got != "bbbb" {
		t.Fatalf("a rewrite inside the racy window was served from the cache: %q", got)
	}
}

func TestReadConfigRootHitIsServedFromTheCache(t *testing.T) {
	path := cacheConfigPath(t)
	writeAged(t, path, `{"provider":"aaaa"}`, time.Minute)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := provider(t); got != "aaaa" {
		t.Fatalf("provider = %q", got)
	}
	// Same file, same size, same mtime: the stat matches, so the cached parse is returned.
	if err := os.WriteFile(path, []byte(`{"provider":"bbbb"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got := provider(t); got != "aaaa" {
		t.Fatalf("a matching stat did not hit the cache: %q", got)
	}
}

func TestReadConfigRootResetCachesDropsTheEntry(t *testing.T) {
	path := cacheConfigPath(t)
	writeAged(t, path, `{"provider":"aaaa"}`, time.Minute)
	if got := provider(t); got != "aaaa" {
		t.Fatalf("provider = %q", got)
	}
	before := configParseCount()
	ResetCaches()
	provider(t)
	if configParseCount()-before != 1 {
		t.Fatal("ResetCaches did not invalidate the parsed config")
	}
}

func TestReadConfigRootDeletedFileIsNotServedFromTheCache(t *testing.T) {
	path := cacheConfigPath(t)
	writeAged(t, path, `{"provider":"aaaa"}`, time.Minute)
	if got := provider(t); got != "aaaa" {
		t.Fatalf("provider = %q", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	root, err := ReadConfigRoot()
	if err != nil || root != nil {
		t.Fatalf("deleted file: root %v, err %v", root, err)
	}
}

func TestReadConfigRootSeesAnEditOfADifferentSizeWithTheSameMtime(t *testing.T) {
	path := cacheConfigPath(t)
	writeAged(t, path, `{"provider":"aaaa"}`, time.Minute)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := provider(t); got != "aaaa" {
		t.Fatalf("provider = %q", got)
	}
	if err := os.WriteFile(path, []byte(`{"provider":"bbbbbb"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got := provider(t); got != "bbbbbb" {
		t.Fatalf("an edit that changed only the size was not picked up: %q", got)
	}
}
