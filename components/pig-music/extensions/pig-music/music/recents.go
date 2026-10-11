package music

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Recent is one entry of the Search screen's history: a search that was made (Query) or a track the user chose to play (Track).
type Recent struct {
	Query string `json:"query,omitempty"`
	Track *Track `json:"track,omitempty"`
	At    int64  `json:"at"` // unix seconds
}

// Recents keeps the Search screen's history. Every method may be called from any goroutine.
type Recents interface {
	// Load is the history, newest first.
	Load() ([]Recent, error)
	// Add puts r first (an earlier entry for the same search or track goes) and returns the history as it is now.
	Add(r Recent) ([]Recent, error)
	// Clear forgets the whole history.
	Clear() error
}

// Search screen history limits: entries kept, and the most bytes the file may have (a larger file is not read).
const (
	MaxRecents       = 50
	maxRecentsBytes  = 256 << 10
	recentsVersion   = 1
	recentsQueryRune = 200
)

// RecentsFile is Recents in a private file (0600, written atomically) of the extension's data directory. It holds what was
// searched and the metadata of what was played (IDs, titles, artists, album, length, thumbnail URL); nothing leaves the machine.
type RecentsFile struct {
	Path string
	Now  func() time.Time // nil means time.Now; tests replace it

	mu sync.Mutex
}

type recentsDisk struct {
	V     int      `json:"v"`
	Items []Recent `json:"items"`
}

var _ Recents = (*RecentsFile)(nil)

func (f *RecentsFile) Load() ([]Recent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.read()
}

func (f *RecentsFile) read() ([]Recent, error) {
	file, err := os.Open(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxRecentsBytes+1))
	if err != nil {
		return nil, err
	}
	var d recentsDisk
	if len(data) > maxRecentsBytes || json.Unmarshal(data, &d) != nil || d.V != recentsVersion {
		return nil, nil // not this version's file: an empty history, replaced by the next Add
	}
	out := make([]Recent, 0, len(d.Items))
	for _, r := range d.Items {
		if r.valid() {
			out = append(out, r)
		}
	}
	return out[:min(len(out), MaxRecents)], nil
}

func (r Recent) valid() bool {
	if r.Track != nil {
		return r.Query == "" && r.Track.ID != "" && r.Track.Title != ""
	}
	return strings.TrimSpace(r.Query) != ""
}

// same reports whether a and b are one entry: the same track, or the same words searched (case and spaces aside).
func (r Recent) same(o Recent) bool {
	if (r.Track == nil) != (o.Track == nil) {
		return false
	}
	if r.Track != nil {
		return r.Track.ID == o.Track.ID
	}
	return strings.EqualFold(strings.Join(strings.Fields(r.Query), " "), strings.Join(strings.Fields(o.Query), " "))
}

func (f *RecentsFile) Add(r Recent) ([]Recent, error) {
	if r.Track != nil {
		t := *r.Track
		t.Artists = append([]string(nil), t.Artists...)
		r.Track = &t
	} else {
		r.Query = strings.Join(strings.Fields(r.Query), " ")
		if q := []rune(r.Query); len(q) > recentsQueryRune {
			r.Query = string(q[:recentsQueryRune])
		}
	}
	if !r.valid() {
		return f.Load()
	}
	if r.At == 0 {
		now := time.Now
		if f.Now != nil {
			now = f.Now
		}
		r.At = now().Unix()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	old, _ := f.read()
	items := append(make([]Recent, 0, len(old)+1), r)
	for _, o := range old {
		if !o.same(r) && len(items) < MaxRecents {
			items = append(items, o)
		}
	}
	data, _ := json.Marshal(recentsDisk{V: recentsVersion, Items: items})
	for len(data) > maxRecentsBytes && len(items) > 1 {
		items = items[:len(items)-1]
		data, _ = json.Marshal(recentsDisk{V: recentsVersion, Items: items})
	}
	if err := writePrivate(f.Path, data); err != nil {
		return items, err
	}
	return items, nil
}

func (f *RecentsFile) Clear() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := os.Remove(f.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// writePrivate writes data to path atomically, readable by its owner only.
func writePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".recents-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp.Name(), 0o600)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// RecentsPath is the history's file in the extension's data directory.
func RecentsPath(data string) string { return filepath.Join(data, "recents.json") }
