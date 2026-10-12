package ytdlp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

var _ music.HomeFeed = (*Source)(nil)

// homeAccount is an Account that can read the account's home (account.Library).
type homeAccount interface {
	Home(ctx context.Context) ([]music.Section, error)
}

// popularSource is an Inner that lists the charts (native.Source).
type popularSource interface {
	Popular(ctx context.Context) (music.Section, error)
}

const (
	homeFile         = "home.json"
	homeVersion      = 1
	maxHomeSections  = 4       // sections of the account's home kept and shown
	maxSectionItems  = 12      // tracks or entities per section
	maxPopularTracks = 50      // tracks of the chart kept
	maxHomeBytes     = 1 << 20 // the whole file
)

// diskHome is the Search screen's home as the last session saw it: the chart (public), and the account's suggestions with a
// short hash of which browser answered (ownerOf), so another browser's account is not shown. Metadata only, like the library
// cache: no cookie, no header, no stream URL.
type diskHome struct {
	V         int             `json:"v"`
	Popular   music.Section   `json:"popular"`
	Owner     string          `json:"owner,omitempty"`
	Suggested []music.Section `json:"suggested,omitempty"`
}

// Popular is the chart of the direct source (no cookie), kept on disk for the next session's first paint.
func (s *Source) Popular(ctx context.Context) (music.Section, error) {
	p, ok := s.Inner.(popularSource)
	if !ok {
		return music.Section{}, errors.New("the charts need the direct YouTube Music source (it is off: PIG_MUSIC_SEARCH=ytdlp)")
	}
	sec, err := p.Popular(ctx)
	if err != nil {
		return music.Section{}, err
	}
	sec.Tracks = sec.Tracks[:min(len(sec.Tracks), maxPopularTracks)]
	s.saveHome(func(d *diskHome) { d.Popular = sec })
	return sec, nil
}

// Suggested is the account's home, read like the library: only after the consent check (which never prompts: the Library tab
// asks), signed with the cookies the account already holds or exports once. A withdrawn consent drops the saved suggestions.
func (s *Source) Suggested(ctx context.Context) ([]music.Section, error) {
	h, ok := s.Account.(homeAccount)
	if !ok || s.Cookies == nil {
		return nil, music.ErrNoAccount
	}
	if _, err := s.checkMemory(ctx); err != nil {
		if isConsentGone(err) {
			s.saveHome(func(d *diskHome) { d.Owner, d.Suggested = "", nil })
		}
		return nil, err
	}
	s.mem.mu.Lock()
	spec := s.mem.spec
	s.mem.mu.Unlock()
	if !s.accountUsable() {
		return nil, errors.New("YouTube Music did not accept the signed-in session (r on the Library tab tries again)")
	}
	secs, err := h.Home(ctx)
	if err != nil {
		s.accountFailed(err)
		return nil, err
	}
	secs = trimSections(secs)
	owner := ownerOf(spec)
	s.saveHome(func(d *diskHome) { d.Owner, d.Suggested = owner, secs })
	return secs, nil
}

// trimSections keeps the first sections of a home with something in them, each cut to a few items.
func trimSections(secs []music.Section) []music.Section {
	out := make([]music.Section, 0, maxHomeSections)
	for _, sec := range secs {
		if len(out) == maxHomeSections {
			break
		}
		sec.Tracks = sec.Tracks[:min(len(sec.Tracks), maxSectionItems)]
		sec.Entities = sec.Entities[:min(len(sec.Entities), maxSectionItems)]
		if len(sec.Tracks)+len(sec.Entities) > 0 {
			out = append(out, sec)
		}
	}
	return out
}

// CachedHome is what the last session saved: the chart always, the suggestions while the same browser may still be read.
func (s *Source) CachedHome(ctx context.Context) (music.Section, []music.Section, bool) {
	d, ok := s.readHome()
	if !ok {
		return music.Section{}, nil, false
	}
	var suggested []music.Section
	if d.Owner != "" && s.Cookies != nil {
		spec, err := s.Cookies(ctx)
		switch {
		case err == nil && ownerOf(spec) == d.Owner:
			suggested = d.Suggested
		case isConsentGone(err):
			s.saveHome(func(d *diskHome) { d.Owner, d.Suggested = "", nil })
		}
	}
	if len(d.Popular.Tracks) == 0 && len(suggested) == 0 {
		return music.Section{}, nil, false
	}
	return d.Popular, suggested, true
}

func (s *Source) homePath() string {
	if s.CacheDir == "" {
		return ""
	}
	return filepath.Join(s.CacheDir, homeFile)
}

func (s *Source) readHome() (diskHome, bool) {
	path := s.homePath()
	if path == "" {
		return diskHome{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return diskHome{}, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxHomeBytes+1))
	if err != nil || len(data) > maxHomeBytes {
		return diskHome{}, false
	}
	var d diskHome
	if json.Unmarshal(data, &d) != nil || d.V != homeVersion {
		return diskHome{}, false
	}
	return d, true
}

// saveHome applies change to the saved home and writes it back atomically; a failure is not an error of the listing.
func (s *Source) saveHome(change func(*diskHome)) {
	path := s.homePath()
	if path == "" {
		return
	}
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	d, ok := s.readHome()
	if !ok {
		d = diskHome{V: homeVersion}
	}
	change(&d)
	data, err := json.Marshal(d)
	if err != nil || len(data) > maxHomeBytes {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".home-*.tmp")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Chmod(tmp.Name(), 0o600) != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}
