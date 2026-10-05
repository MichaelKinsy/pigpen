package ytdlp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

var _ music.LibraryCache = (*Source)(nil)

const (
	cacheFile       = "library.json"
	cacheVersion    = 1
	maxCachedTracks = 200     // tracks kept per collection: the first rows, which is what a first paint needs
	maxCachedLists  = 40      // collections whose tracks are kept
	maxCachedCols   = 1000    // collections kept in the list
	maxCacheBytes   = 1 << 20 // the whole file; the oldest collections go first, the liked songs last
)

// diskLibrary is the file: metadata the library listing returned (IDs, titles, artists, album, length, thumbnail URL) and
// nothing else. No cookie, no header, no browser name or profile (only a short hash of which one answered, so another
// browser's account is not shown), no URL of a stream.
type diskLibrary struct {
	V     int                 `json:"v"`
	Owner string              `json:"owner"` // hash of the cookie spec that answered
	Cols  []music.Collection  `json:"collections"`
	Lists map[string]diskList `json:"lists"`
}

type diskList struct {
	At     int64         `json:"at"`
	More   bool          `json:"more"`
	Tracks []music.Track `json:"tracks"`
}

type diskState struct{ mu sync.Mutex }

func ownerOf(spec string) string {
	sum := sha256.Sum256([]byte("pig-music library cache\x00" + spec))
	return hex.EncodeToString(sum[:8])
}

func (s *Source) cachePath() string {
	if s.CacheDir == "" {
		return ""
	}
	return filepath.Join(s.CacheDir, cacheFile)
}

// readDisk loads the cache when it is readable, of this version and the browser that is in use now.
func (s *Source) readDisk(owner string) (diskLibrary, bool) {
	path := s.cachePath()
	if path == "" {
		return diskLibrary{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return diskLibrary{}, false
	}
	defer f.Close()
	// what is written is at most maxCacheBytes: a larger file is not this cache's, and is not read into memory
	data, err := io.ReadAll(io.LimitReader(f, maxCacheBytes+1))
	if err != nil || len(data) > maxCacheBytes {
		return diskLibrary{}, false
	}
	var d diskLibrary
	if json.Unmarshal(data, &d) != nil || d.V != cacheVersion || d.Owner != owner {
		return diskLibrary{}, false
	}
	return d, true
}

// CachedLibrary is what the last session left of the collection list, when reading the account is still allowed (it asks the
// consent, not the cookies) and the same browser is in use.
func (s *Source) CachedLibrary(ctx context.Context) ([]music.Collection, bool) {
	d, ok := s.cacheForNow(ctx)
	if !ok || len(d.Cols) == 0 {
		return nil, false
	}
	return d.Cols, true
}

// CachedTracks is the first n remembered tracks of a collection.
func (s *Source) CachedTracks(ctx context.Context, id string, n int) ([]music.Track, bool, bool) {
	d, ok := s.cacheForNow(ctx)
	if !ok {
		return nil, false, false
	}
	l, ok := d.Lists[id]
	if !ok || len(l.Tracks) == 0 {
		return nil, false, false
	}
	return l.Tracks[:min(n, len(l.Tracks))], l.More || n < len(l.Tracks), true
}

func (s *Source) cacheForNow(ctx context.Context) (diskLibrary, bool) {
	if s.cachePath() == "" || s.Cookies == nil {
		return diskLibrary{}, false
	}
	spec, err := s.Cookies(ctx)
	if err != nil {
		if isConsentGone(err) {
			s.dropDisk() // withdrawn consent takes the remembered listing with it
		}
		return diskLibrary{}, false
	}
	return s.readDisk(ownerOf(spec))
}

func isConsentGone(err error) bool {
	var need *music.NeedsConsentError
	return errors.As(err, &need)
}

func (s *Source) dropDisk() {
	if p := s.cachePath(); p != "" {
		s.disk.mu.Lock()
		_ = os.Remove(p)
		s.disk.mu.Unlock()
	}
}

// saveDisk applies change to the cache of the browser in use and writes it back, atomically. A failure is not an error of the
// listing: the cache is an extra.
func (s *Source) saveDisk(spec string, change func(*diskLibrary)) {
	path := s.cachePath()
	if path == "" {
		return
	}
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	owner := ownerOf(spec)
	d, ok := s.readDisk(owner)
	if !ok {
		d = diskLibrary{V: cacheVersion, Owner: owner}
	}
	if d.Lists == nil {
		d.Lists = map[string]diskList{}
	}
	change(&d)
	trim := func() []byte {
		data, _ := json.Marshal(d)
		return data
	}
	data := trim()
	for len(data) > maxCacheBytes && evictOldest(&d) {
		data = trim()
	}
	if len(data) > maxCacheBytes {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".library-*.tmp")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Chmod(tmp.Name(), 0o600) != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// evictOldest drops the collection that was saved longest ago, never the liked songs; false when there is none left to drop.
func evictOldest(d *diskLibrary) bool {
	ids := make([]string, 0, len(d.Lists))
	for id := range d.Lists {
		if id != likedSongs.ID {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return false
	}
	sort.Slice(ids, func(i, j int) bool { return d.Lists[ids[i]].At < d.Lists[ids[j]].At })
	delete(d.Lists, ids[0])
	return true
}

func (s *Source) saveCollections(spec string, cols []music.Collection) {
	if len(cols) > maxCachedCols {
		cols = cols[:maxCachedCols]
	}
	s.saveDisk(spec, func(d *diskLibrary) { d.Cols = append([]music.Collection(nil), cols...) })
}

// saveFirstTracks remembers the first rows of a collection (a page that starts at 0); later pages are not kept.
func (s *Source) saveFirstTracks(spec, id string, tracks []music.Track, more bool) {
	if len(tracks) == 0 {
		return
	}
	if len(tracks) > maxCachedTracks {
		tracks, more = tracks[:maxCachedTracks], true
	}
	s.saveDisk(spec, func(d *diskLibrary) {
		d.Lists[id] = diskList{At: time.Now().UnixNano(), More: more, Tracks: append([]music.Track(nil), tracks...)}
		for len(d.Lists) > maxCachedLists && evictOldest(d) {
		}
	})
}
