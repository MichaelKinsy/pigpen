package ytdlp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func diskRig(t *testing.T, dir string, f *fakeAccount) *Source {
	t.Helper()
	return &Source{Runner: (&recorder{}).run, Cookies: granted("chrome:Profile 1"), Account: f, CacheDir: dir}
}

func TestALibraryListedInOneSessionIsOnDiskForTheNext(t *testing.T) {
	dir := t.TempDir()
	f := &fakeAccount{cols: []music.Collection{{ID: "PLroad", Kind: "playlist", Title: "Road trip", Count: 12}}, total: 120}
	first := diskRig(t, dir, f)
	ctx := context.Background()
	if _, err := first.Library(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := first.TracksPage(ctx, "LM", 0, 50); err != nil {
		t.Fatal(err)
	}
	// a new process: nothing in memory, no account call
	second := diskRig(t, dir, &fakeAccount{err: errors.New("must not be asked")})
	cols, ok := second.CachedLibrary(ctx)
	if !ok || len(cols) != 2 || cols[0].ID != "LM" || cols[1].Title != "Road trip" {
		t.Fatalf("%v %v", cols, ok)
	}
	tracks, more, ok := second.CachedTracks(ctx, "LM", 50)
	if !ok || len(tracks) != 50 || !more {
		t.Fatalf("%d tracks, more %v, ok %v", len(tracks), more, ok)
	}
	if tracks, _, _ := second.CachedTracks(ctx, "LM", 10); len(tracks) != 10 {
		t.Errorf("n is not honoured: %d", len(tracks))
	}
	if _, _, ok := second.CachedTracks(ctx, "PLunknown", 10); ok {
		t.Error("tracks of a collection that was never opened")
	}
}

func TestTheCacheHoldsMetadataOnlyAndIsPrivate(t *testing.T) {
	dir := t.TempDir()
	f := &fakeAccount{total: 3}
	s := diskRig(t, dir, f)
	_, _, _ = s.TracksPage(context.Background(), "LM", 0, 50)
	_, _ = s.Library(context.Background())
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("files %v", entries)
	}
	info, _ := entries[0].Info()
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode())
	}
	data, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	for _, bad := range []string{"SAPISID", "Cookie", "chrome", "Profile 1", "Authorization"} {
		if strings.Contains(string(data), bad) {
			t.Errorf("the cache file mentions %q", bad)
		}
	}
}

func TestTheCacheIsNotShownWhenConsentIsGoneOrTheBrowserChanged(t *testing.T) {
	dir := t.TempDir()
	f := &fakeAccount{total: 3}
	_, _ = diskRig(t, dir, f).Library(context.Background())
	gone := diskRig(t, dir, f)
	gone.Cookies = func(context.Context) (string, error) { return "", &music.NeedsConsentError{Browser: "chrome"} }
	if _, ok := gone.CachedLibrary(context.Background()); ok {
		t.Error("shown without consent")
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("the cache stayed after consent was withdrawn: %v", left)
	}
	_, _ = diskRig(t, dir, f).Library(context.Background())
	other := diskRig(t, dir, f)
	other.Cookies = granted("firefox")
	if _, ok := other.CachedLibrary(context.Background()); ok {
		t.Error("another browser's account was shown")
	}
}

func TestACorruptOrForeignCacheIsIgnored(t *testing.T) {
	for _, content := range []string{"not json", `{"v":99,"cols":[{"ID":"x"}]}`, ``} {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "library.json"), []byte(content), 0o600)
		if cols, ok := diskRig(t, dir, &fakeAccount{}).CachedLibrary(context.Background()); ok {
			t.Errorf("%q gave %v", content, cols)
		}
	}
}

func TestTheCacheIsCappedInTracksPerCollectionAndInSize(t *testing.T) {
	dir := t.TempDir()
	f := &fakeAccount{total: 5000}
	s := diskRig(t, dir, f)
	_, _, _ = s.TracksPage(context.Background(), "LM", 0, 5000)
	tracks, more, ok := s.CachedTracks(context.Background(), "LM", 5000)
	if !ok || len(tracks) != maxCachedTracks || !more {
		t.Errorf("%d tracks, more %v", len(tracks), more)
	}
	for i := 0; i < 80; i++ { // many collections, each full of long titles
		f.total = 200
		_, _, _ = s.TracksPage(context.Background(), fmt.Sprintf("PL%014d", i), 0, 200)
	}
	st, _ := os.Stat(filepath.Join(dir, "library.json"))
	if st.Size() > maxCacheBytes {
		t.Errorf("%d bytes", st.Size())
	}
	if _, _, ok := s.CachedTracks(context.Background(), "LM", 10); !ok {
		t.Error("the liked songs were dropped to make room")
	}
}

func TestALaterPageOfACollectionDoesNotOverwriteItsFirstPageInTheCache(t *testing.T) {
	dir := t.TempDir()
	s := diskRig(t, dir, &fakeAccount{total: 120})
	_, _, _ = s.TracksPage(context.Background(), "LM", 0, 50)
	_, _, _ = s.TracksPage(context.Background(), "LM", 50, 50)
	if tracks, _, _ := s.CachedTracks(context.Background(), "LM", 200); len(tracks) != 50 {
		t.Errorf("%d tracks", len(tracks))
	}
}

func TestTheCacheNeverDelaysOrBreaksTheLibraryWhenItsDirectoryCannotBeWritten(t *testing.T) {
	f := &fakeAccount{total: 3}
	s := diskRig(t, filepath.Join(t.TempDir(), "missing", "deeper"), f)
	s.CacheDir = "/proc/does-not-exist/x"
	if _, err := s.Library(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// The cache is written at most 1 MiB; a larger file is not ours (or was tampered with) and is not read into memory.
func TestACacheFileOverTheCapIsNotRead(t *testing.T) {
	dir := t.TempDir()
	big := diskLibrary{V: cacheVersion, Owner: ownerOf("chrome:Profile 1"), Cols: []music.Collection{{ID: "PLx", Kind: "playlist", Title: strings.Repeat("x", 3<<20)}}}
	data, _ := json.Marshal(big)
	if err := os.WriteFile(filepath.Join(dir, "library.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := diskRig(t, dir, &fakeAccount{}).CachedLibrary(context.Background()); ok {
		t.Error("a 3 MiB cache file was read")
	}
}
