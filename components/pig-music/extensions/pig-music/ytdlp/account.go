package ytdlp

import (
	"context"
	"errors"
	"strings"

	"github.com/MichaelKinsy/pigpen/pig-music/account"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Account is the signed-in library client (package account.Library).
type Account interface {
	Collections(ctx context.Context) ([]music.Collection, error)
	TracksPage(ctx context.Context, id string, from, n int) (tracks []music.Track, more bool, err error)
	Forget()
}

func (s *Source) accountUsable() bool { return s.Account != nil && !s.accountOff.Load() }

// accountFailed decides what an error of the account means: a refusal or an unknown layout switches the account off for the
// session (yt-dlp does the listing), anything else (the network) only falls back this once.
func (s *Source) accountFailed(err error) {
	if errors.Is(err, account.ErrAuth) || errors.Is(err, account.ErrShape) {
		s.accountOff.Store(true)
	}
}

func (s *Source) collectionsFromAccount(ctx context.Context) ([]music.Collection, bool) {
	if !s.accountUsable() {
		return nil, false
	}
	cols, err := s.Account.Collections(ctx)
	if err != nil {
		s.accountFailed(err)
		return nil, false
	}
	return append([]music.Collection{likedSongs}, cols...), true
}

func (s *Source) pageFromAccount(ctx context.Context, id string, from, n int) ([]music.Track, bool, bool) {
	if !s.accountUsable() {
		return nil, false, false
	}
	got, more, err := s.Account.TracksPage(ctx, id, from, n)
	if err != nil {
		s.accountFailed(err)
		return nil, false, false
	}
	return got, more, true
}

// wholeFromAccount reads a whole collection, 200 rows at a time.
func (s *Source) wholeFromAccount(ctx context.Context, id string) ([]music.Track, bool) {
	var all []music.Track
	for from := 0; ; {
		got, more, ok := s.pageFromAccount(ctx, id, from, 200)
		if !ok {
			return nil, false
		}
		all = append(all, got...)
		from += len(got)
		if !more || len(got) == 0 {
			return all, true
		}
	}
}

// NewAccount is the production Account: yt-dlp (bin) exports the cookies of the browser that cookies names, once, into memory
// (ExportCookies), and package account signs the library requests with them. nil when getenv turns it off
// (PIG_MUSIC_LIBRARY=ytdlp), which leaves the whole library to yt-dlp.
func NewAccount(getenv func(string) string, bin string, cookies func(ctx context.Context) (string, error)) Account {
	if getenv != nil && strings.EqualFold(strings.TrimSpace(getenv("PIG_MUSIC_LIBRARY")), "ytdlp") || cookies == nil {
		return nil
	}
	return &account.Library{C: &account.Client{Jar: func(ctx context.Context) (account.Jar, error) {
		spec, err := cookies(ctx)
		if err != nil {
			return nil, err
		}
		return ExportCookies(ctx, bin, spec)
	}}}
}

var _ music.EntitySearcher = (*Source)(nil)

// SearchEntities passes to the direct source (Inner) when it can list albums and artists; yt-dlp cannot, so a Source without
// one shows songs only.
func (s *Source) SearchEntities(ctx context.Context, query string, limit int) ([]music.Entity, error) {
	if e, ok := s.Inner.(music.EntitySearcher); ok {
		return e.SearchEntities(ctx, query, limit)
	}
	return nil, nil
}

// OpenEntity passes to the direct source.
func (s *Source) OpenEntity(ctx context.Context, e music.Entity) (string, []music.Track, error) {
	if o, ok := s.Inner.(music.EntitySearcher); ok {
		return o.OpenEntity(ctx, e)
	}
	return "", nil, errors.New("albums and artists need the direct YouTube Music source (it is off: PIG_MUSIC_SEARCH=ytdlp)")
}
