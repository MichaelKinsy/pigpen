package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/internal/lazyre"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// UserAgent is what LRCLIB is told about this program (its API asks clients to identify themselves).
const UserAgent = "pig-music (https://github.com/MichaelKinsy/pigpen; a PiG music player)"

const (
	defaultBase     = "https://lrclib.net"
	defaultMaxFiles = 400
	noRememberFor   = 24 * time.Hour
	maxOffsets      = 500
	durationSlack   = 3.0 // seconds a search hit's length may differ from the track's
)

var idRe = lazyre.New(`^[A-Za-z0-9_-]{6,32}$`)

// Service finds lyrics: LRCLIB first (synced when it has them), then Fallback (YouTube Music's own, plain). Results are kept in
// Dir by video ID, and so is a "none" for a day. It is a music.LyricsProvider.
type Service struct {
	Dir       string // cache directory; "" keeps nothing (and offsets are not remembered)
	HTTP      *http.Client
	BaseURL   string // tests
	UserAgent string
	// Fallback returns YouTube Music's plain lyrics for a video, or an error that wraps music.ErrNoLyrics.
	Fallback func(ctx context.Context, videoID string) (string, error)
	MaxFiles int
	Now      func() time.Time

	mu      sync.Mutex
	offsets map[string]int64
	order   []string
	loaded  bool
}

var _ music.LyricsProvider = (*Service)(nil)

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

type cached struct {
	Saved  int64        `json:"saved"`
	None   bool         `json:"none,omitempty"`
	Lyrics music.Lyrics `json:"lyrics"`
}

func (s *Service) file(id string) string {
	if s.Dir == "" || !idRe.MatchString(id) {
		return ""
	}
	return filepath.Join(s.Dir, id+".json")
}

// Lyrics returns the track's lyrics, from the cache when it has them.
func (s *Service) Lyrics(ctx context.Context, t music.Track) (music.Lyrics, error) {
	if t.Title == "" {
		return music.Lyrics{}, music.ErrNoLyrics
	}
	if path := s.file(t.ID); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var c cached
			if json.Unmarshal(data, &c) == nil {
				switch {
				case c.None && s.now().Sub(time.Unix(c.Saved, 0)) < noRememberFor:
					return music.Lyrics{}, music.ErrNoLyrics
				case !c.None:
					return c.Lyrics, nil
				}
			}
		}
	}
	l, err := s.find(ctx, t)
	switch {
	case err == nil:
		s.keep(t.ID, cached{Saved: s.now().Unix(), Lyrics: l})
	case errors.Is(err, music.ErrNoLyrics):
		s.keep(t.ID, cached{Saved: s.now().Unix(), None: true})
	}
	return l, err
}

func (s *Service) find(ctx context.Context, t music.Track) (music.Lyrics, error) {
	l, err := s.fromLRCLIB(ctx, t)
	if err == nil {
		return l, nil
	}
	// LRCLIB had none (ErrNoLyrics) or was down (any other error): YouTube Music's own lyrics are the next try
	if s.Fallback != nil && idRe.MatchString(t.ID) {
		text, ferr := s.Fallback(ctx, t.ID)
		if ferr == nil && strings.TrimSpace(text) != "" {
			if l := ParsePlain(text); enough(l) {
				l.Source = "youtube"
				return l, nil
			}
		}
		if ferr != nil && !errors.Is(ferr, music.ErrNoLyrics) && errors.Is(err, music.ErrNoLyrics) {
			return music.Lyrics{}, ferr // LRCLIB had none and YouTube could not be asked: an error, not a "none"
		}
	}
	return music.Lyrics{}, err
}

type lrcRecord struct {
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	Plain        *string `json:"plainLyrics"`
	Synced       *string `json:"syncedLyrics"`
}

// minLines is how many lines of text a sheet needs to count: LRCLIB holds placeholders ("probe") that someone submitted.
const minLines = 3

func enough(l music.Lyrics) bool {
	n := 0
	for _, ln := range l.Lines {
		if strings.TrimSpace(ln.Text) != "" {
			n++
		}
	}
	return n >= minLines
}

func (r lrcRecord) lyrics() (music.Lyrics, bool) {
	if r.Instrumental {
		return music.Lyrics{Source: "lrclib", Instrumental: true}, true
	}
	if r.Synced != nil && strings.TrimSpace(*r.Synced) != "" {
		if l := ParseLRC(*r.Synced); l.Synced && enough(l) {
			l.Source = "lrclib"
			return l, true
		}
	}
	if r.Plain != nil && strings.TrimSpace(*r.Plain) != "" {
		if l := ParsePlain(*r.Plain); enough(l) {
			l.Source = "lrclib"
			return l, true
		}
	}
	return music.Lyrics{}, false
}

func (s *Service) fromLRCLIB(ctx context.Context, t music.Track) (music.Lyrics, error) {
	artist := ""
	if len(t.Artists) > 0 {
		artist = t.Artists[0]
	}
	q := url.Values{"track_name": {t.Title}}
	if artist != "" {
		q.Set("artist_name", artist)
	}
	get := url.Values{"track_name": {t.Title}, "artist_name": {artist}}
	if t.Album != "" {
		get.Set("album_name", t.Album)
	}
	if t.Duration > 0 {
		get.Set("duration", strconv.Itoa(int(math.Round(t.Duration.Seconds()))))
	}
	if artist != "" && t.Duration > 0 {
		body, status, err := s.request(ctx, "/api/get?"+get.Encode())
		if err != nil {
			return music.Lyrics{}, err
		}
		if status == http.StatusOK {
			var r lrcRecord
			if json.Unmarshal(body, &r) == nil {
				if l, ok := r.lyrics(); ok {
					return l, nil
				}
			}
		}
	}
	body, status, err := s.request(ctx, "/api/search?"+q.Encode())
	if err != nil {
		return music.Lyrics{}, err
	}
	if status != http.StatusOK {
		return music.Lyrics{}, music.ErrNoLyrics
	}
	var hits []lrcRecord
	if json.Unmarshal(body, &hits) != nil {
		return music.Lyrics{}, music.ErrNoLyrics
	}
	// synced ones first, then the length nearest to the track's
	sort.SliceStable(hits, func(i, j int) bool {
		si, sj := hits[i].Synced != nil && *hits[i].Synced != "", hits[j].Synced != nil && *hits[j].Synced != ""
		if si != sj {
			return si
		}
		return math.Abs(hits[i].Duration-t.Duration.Seconds()) < math.Abs(hits[j].Duration-t.Duration.Seconds())
	})
	for _, h := range hits {
		if t.Duration > 0 && h.Duration > 0 && math.Abs(h.Duration-t.Duration.Seconds()) > durationSlack {
			continue
		}
		if l, ok := h.lyrics(); ok {
			return l, nil
		}
	}
	return music.Lyrics{}, music.ErrNoLyrics
}

// request makes one LRCLIB GET: the body and status. A 404 (or 400) is a status, not an error; any other refusal, a server error
// or a network failure is.
func (s *Service) request(ctx context.Context, path string) ([]byte, int, error) {
	base := s.BaseURL
	if base == "" {
		base = defaultBase
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, 0, err
	}
	ua := s.UserAgent
	if ua == "" {
		ua = UserAgent
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Lrclib-Client", ua)
	client := s.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("LRCLIB: %w", redact(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("LRCLIB: %w", redact(err))
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNotFound, http.StatusBadRequest:
	default: // down, rate limited (429) or refusing: not an answer about the track, so not remembered as "none"
		return nil, 0, fmt.Errorf("LRCLIB answered HTTP %d", resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

// redact keeps only the cause of a network error (a *url.Error carries the full URL, which holds the track's name).
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// keep writes a cache entry and trims the directory to MaxFiles, oldest first. A failure is not the lookup's.
func (s *Service) keep(id string, c cached) {
	path := s.file(id)
	if path == "" {
		return
	}
	data, err := json.Marshal(c)
	if err != nil || os.MkdirAll(s.Dir, 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(s.Dir, ".lyr-*.tmp")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Chmod(tmp.Name(), 0o600) != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	s.prune()
}

func (s *Service) prune() {
	max := s.MaxFiles
	if max <= 0 {
		max = defaultMaxFiles
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return
	}
	type f struct {
		path string
		at   time.Time
	}
	var files []f
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || e.Name() == "offsets.json" {
			continue
		}
		if info, err := e.Info(); err == nil {
			files = append(files, f{filepath.Join(s.Dir, e.Name()), info.ModTime()})
		}
	}
	if len(files) <= max {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].at.Before(files[j].at) })
	for _, old := range files[:len(files)-max] {
		_ = os.Remove(old.path)
	}
}

// ── offsets ─────────────────────────────────────────────────────────────────────────────────────────────────────────

func (s *Service) offsetsPath() string {
	if s.Dir == "" {
		return ""
	}
	return filepath.Join(s.Dir, "offsets.json")
}

func (s *Service) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	s.offsets = map[string]int64{}
	if p := s.offsetsPath(); p != "" {
		if data, err := os.ReadFile(p); err == nil {
			var disk struct {
				Order   []string         `json:"order"`
				Offsets map[string]int64 `json:"offsets"`
			}
			if json.Unmarshal(data, &disk) == nil {
				s.offsets, s.order = disk.Offsets, disk.Order
				if s.offsets == nil {
					s.offsets = map[string]int64{}
				}
			}
		}
	}
}

// Offset is the nudge the user gave this track's lyrics (positive: later), 0 when there is none.
func (s *Service) Offset(id string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	return time.Duration(s.offsets[id]) * time.Millisecond
}

// SetOffset remembers a nudge for the track; zero forgets it. At most maxOffsets tracks are kept, the oldest forgotten first.
func (s *Service) SetOffset(id string, d time.Duration) {
	if !idRe.MatchString(id) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	delete(s.offsets, id)
	for i, o := range s.order {
		if o == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	if d != 0 {
		s.offsets[id] = d.Milliseconds()
		s.order = append(s.order, id)
		for len(s.order) > maxOffsets {
			delete(s.offsets, s.order[0])
			s.order = s.order[1:]
		}
	}
	p := s.offsetsPath()
	if p == "" || os.MkdirAll(s.Dir, 0o700) != nil {
		return
	}
	data, _ := json.Marshal(struct {
		Order   []string         `json:"order"`
		Offsets map[string]int64 `json:"offsets"`
	}{s.order, s.offsets})
	if tmp, err := os.CreateTemp(s.Dir, ".off-*.tmp"); err == nil {
		_, werr := tmp.Write(data)
		cerr := tmp.Close()
		if werr != nil || cerr != nil || os.Chmod(tmp.Name(), 0o600) != nil || os.Rename(tmp.Name(), p) != nil {
			_ = os.Remove(tmp.Name())
		}
	}
}
