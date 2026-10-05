package ytdlp

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

var (
	_ music.TrackPager = (*Source)(nil)
	_ music.Refresher  = (*Source)(nil)
)

// libraryMemory is what the Source remembers of the account for the session, in memory only: the collections and the tracks
// fetched so far of each. Every library call makes yt-dlp read and decrypt the browser's cookie store, so a listing that was
// answered once is not asked again until Refresh. It belongs to the cookie spec (browser and profile) that answered, and is
// shown only while that browser is still allowed: withdrawing consent or choosing another browser forgets it.
type libraryMemory struct {
	mu    sync.Mutex
	spec  string // the --cookies-from-browser value that answered what is remembered
	gen   int    // bumped whenever the memory is forgotten, so an answer that was on its way when it was is not kept
	cols  []music.Collection
	have  bool
	lists map[string]*knownList
}

type knownList struct {
	tracks   []music.Track // the first len(tracks) tracks, in order
	complete bool          // nothing follows them
}

// Refresh forgets the remembered listings, so the next one asks the account again.
func (s *Source) Refresh() {
	s.forgetAccount() // the user asked again: the account gets another chance
	s.mem.mu.Lock()
	defer s.mem.mu.Unlock()
	s.mem.forget()
}

// forget drops everything remembered; the caller holds mu.
func (m *libraryMemory) forget() {
	m.cols, m.have, m.lists, m.spec = nil, false, nil, ""
	m.gen++
}

// memoryTicket is the state of the memory a listing started from: what it learns is kept only if nothing was forgotten
// in between.
type memoryTicket struct{ gen int }

// checkMemory asks the cookie access, as every library call did before anything was remembered (it reads the consent file,
// not the cookies): when the browser is no longer allowed, the memory is forgotten and the reason returned; when another
// browser or profile is in use, what the first one answered is forgotten.
func (s *Source) checkMemory(ctx context.Context) (memoryTicket, error) {
	if s.Cookies == nil {
		s.Refresh()
		return memoryTicket{}, &music.NoBrowserError{Reason: "cookie access is not configured"}
	}
	spec, err := s.Cookies(ctx)
	s.mem.mu.Lock()
	if err != nil {
		s.mem.forget()
		s.mem.mu.Unlock()
		s.forgetAccount() // the browser is no longer allowed: its cookies leave memory
		if isConsentGone(err) {
			s.dropDisk() // and withdrawn consent takes the remembered listing with it
		}
		return memoryTicket{}, err
	}
	changed := s.mem.spec != "" && spec != s.mem.spec
	if spec != s.mem.spec {
		s.mem.forget()
		s.mem.spec = spec
	}
	tk := memoryTicket{gen: s.mem.gen}
	s.mem.mu.Unlock()
	if changed {
		s.forgetAccount() // another browser or profile: the first one's cookies must not list (or be cached as) this one's library
	}
	return tk, nil
}

// forgetAccount drops the account's cookies and what it listed, and gives it another chance.
func (s *Source) forgetAccount() {
	if s.Account != nil {
		s.Account.Forget()
		s.accountOff.Store(false)
	}
}

func (s *Source) rememberedCollections() ([]music.Collection, bool) {
	s.mem.mu.Lock()
	defer s.mem.mu.Unlock()
	if !s.mem.have {
		return nil, false
	}
	return append([]music.Collection(nil), s.mem.cols...), true
}

func (s *Source) rememberCollections(tk memoryTicket, cols []music.Collection) {
	s.mem.mu.Lock()
	if tk.gen != s.mem.gen {
		s.mem.mu.Unlock()
		return
	}
	s.mem.cols, s.mem.have = append([]music.Collection(nil), cols...), true
	spec := s.mem.spec
	s.mem.mu.Unlock()
	s.saveCollections(spec, cols) // the next session's first paint; a failure to write is not the listing's
}

func (s *Source) known(id string) knownList {
	s.mem.mu.Lock()
	defer s.mem.mu.Unlock()
	if l := s.mem.lists[id]; l != nil {
		return knownList{tracks: append([]music.Track(nil), l.tracks...), complete: l.complete}
	}
	return knownList{}
}

func (s *Source) remember(tk memoryTicket, id string, l knownList) {
	s.mem.mu.Lock()
	defer s.mem.mu.Unlock()
	if tk.gen != s.mem.gen {
		return
	}
	if s.mem.lists == nil {
		s.mem.lists = map[string]*knownList{}
	}
	s.mem.lists[id] = &l
}

// TracksPage lists n tracks of a collection starting at index from. Pages are fetched with yt-dlp's --playlist-items, so the
// first rows of a long collection show after one short run, and what was fetched is remembered. A page that does not follow
// what is known (the cursor jumped) is returned but not remembered. A library call: it reads the browser cookies.
func (s *Source) TracksPage(ctx context.Context, id string, from, n int) ([]music.Track, bool, error) {
	got, more, err := s.tracksPage(ctx, id, from, n)
	if err == nil && from == 0 {
		s.saveFirstTracks(s.currentSpec(), id, got, more) // the next session's first paint
	}
	return got, more, err
}

// currentSpec is the cookie spec the memory belongs to (what the cache is kept for).
func (s *Source) currentSpec() string {
	s.mem.mu.Lock()
	defer s.mem.mu.Unlock()
	return s.mem.spec
}

func (s *Source) tracksPage(ctx context.Context, id string, from, n int) ([]music.Track, bool, error) {
	if !collectionID.MatchString(id) {
		return nil, false, fmt.Errorf("%q is not a playlist ID", id)
	}
	if from < 0 || n < 1 {
		return nil, false, fmt.Errorf("a page of %d tracks from %d", n, from)
	}
	tk, err := s.checkMemory(ctx)
	if err != nil {
		return nil, false, err
	}
	if got, more, ok := s.pageFromAccount(ctx, id, from, n); ok {
		return got, more, nil
	}
	known := s.known(id)
	end := from + n
	if len(known.tracks) >= end || (known.complete && from <= len(known.tracks)) {
		last := min(end, len(known.tracks))
		return known.tracks[min(from, last):last], last < len(known.tracks) || !known.complete, nil
	}
	start := from
	if from <= len(known.tracks) {
		start = len(known.tracks) // fetch only what is not known yet
	}
	got, err := s.fetchRange(ctx, id, start+1, end)
	if err != nil {
		return nil, false, err
	}
	more := len(got) >= end-start
	if from <= len(known.tracks) {
		known.tracks = append(known.tracks, got...)
		known.complete = !more
		s.remember(tk, id, known)
		last := min(end, len(known.tracks))
		return known.tracks[min(from, last):last], more, nil
	}
	return got, more, nil
}

// fetchRange runs yt-dlp for the 1-based items first..last of a collection.
func (s *Source) fetchRange(ctx context.Context, id string, first, last int) ([]music.Track, error) {
	out, err := s.libraryRun(ctx, "--flat-playlist", "--playlist-items", strconv.Itoa(first)+"-"+strconv.Itoa(last), "-J", "--", "https://music.youtube.com/playlist?list="+id)
	if err != nil {
		return nil, err
	}
	return ParseTracks(out)
}
