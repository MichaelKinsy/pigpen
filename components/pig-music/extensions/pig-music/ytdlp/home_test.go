package ytdlp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/account"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// homeFakeAccount is fakeAccount that can read the account's home too.
type homeFakeAccount struct {
	fakeAccount
	home    []music.Section
	homeErr error
}

func (f *homeFakeAccount) Home(context.Context) ([]music.Section, error) {
	f.asked = append(f.asked, "home")
	return f.home, f.homeErr
}

// charts is an Inner that has a chart.
type charts struct {
	stubInner
	sec music.Section
	err error
	n   int
}

func (c *charts) Popular(context.Context) (music.Section, error) {
	c.n++
	return c.sec, c.err
}

type stubInner struct{}

func (stubInner) Search(context.Context, string, int) ([]music.Track, error) { return nil, nil }
func (stubInner) Library(context.Context) ([]music.Collection, error)        { return nil, nil }
func (stubInner) Tracks(context.Context, string) ([]music.Track, error)      { return nil, nil }
func (stubInner) PlayURL(music.Track) string                                 { return "" }

func quickPicks() []music.Section {
	return []music.Section{{Title: "Quick picks", Tracks: []music.Track{{ID: "aaaaaaaaaaa", Title: "A"}}}, {Title: "Mixed for you", Entities: []music.Entity{{Kind: "playlist", ID: "VLRDx", Title: "Mix"}}}}
}

// TestSuggestionsReadTheAccountOnlyWithTheLibrarysConsent: no account in use is ErrNoAccount; no consent is the consent error
// and the account is not asked; with consent the home is read and kept for the next session, for the same browser only.
func TestSuggestionsReadTheAccountOnlyWithTheLibrarysConsent(t *testing.T) {
	ctx := context.Background()
	if _, err := (&Source{Cookies: granted("chrome")}).Suggested(ctx); !errors.Is(err, music.ErrNoAccount) {
		t.Errorf("no account: %v", err)
	}
	if _, err := (&Source{Account: &homeFakeAccount{}}).Suggested(ctx); !errors.Is(err, music.ErrNoAccount) {
		t.Errorf("no cookie access: %v", err)
	}
	if _, err := (&Source{Account: &fakeAccount{}, Cookies: granted("chrome")}).Suggested(ctx); !errors.Is(err, music.ErrNoAccount) {
		t.Errorf("an account without a home: %v", err)
	}

	f := &homeFakeAccount{home: quickPicks()}
	need := &music.NeedsConsentError{Browser: "chrome"}
	var consent error = need
	spec := "chrome:Profile 1"
	dir := t.TempDir()
	s := &Source{Runner: (&recorder{}).run, Account: f, CacheDir: dir, Cookies: func(context.Context) (string, error) { return spec, consent }}
	if _, err := s.Suggested(ctx); !errors.As(err, &need) {
		t.Fatalf("without consent: %v", err)
	}
	if len(f.asked) != 0 {
		t.Fatalf("the account was asked without consent: %v", f.asked)
	}
	consent = nil
	got, err := s.Suggested(ctx)
	if err != nil || len(got) != 2 || got[0].Title != "Quick picks" || strings.Join(f.asked, " ") != "home" {
		t.Fatalf("%v %v, asked %v", got, err, f.asked)
	}
	next := &Source{Account: &homeFakeAccount{homeErr: errors.New("must not be asked")}, CacheDir: dir, Cookies: func(context.Context) (string, error) { return spec, consent }}
	if _, sug, ok := next.CachedHome(ctx); !ok || len(sug) != 2 || sug[1].Entities[0].Title != "Mix" {
		t.Fatalf("the next session's cache: %v %v", sug, ok)
	}
	spec = "firefox"
	if _, sug, _ := next.CachedHome(ctx); sug != nil {
		t.Errorf("another browser's account shows the first one's suggestions: %v", sug)
	}
	spec, consent = "chrome:Profile 1", need
	if _, sug, _ := next.CachedHome(ctx); sug != nil {
		t.Errorf("suggestions shown after the consent was withdrawn: %v", sug)
	}
	consent = nil
	if _, sug, _ := next.CachedHome(ctx); sug != nil {
		t.Errorf("a withdrawn consent left the suggestions on disk: %v", sug)
	}
	data, _ := os.ReadFile(filepath.Join(dir, homeFile))
	if strings.Contains(string(data), "chrome") || strings.Contains(string(data), "Profile") {
		t.Errorf("the cache names the browser: %s", data)
	}
}

// TestARefusedAccountSwitchesItOffForTheSessionAsTheLibraryDoes.
func TestARefusedAccountSwitchesItOffForTheSessionAsTheLibraryDoes(t *testing.T) {
	f := &homeFakeAccount{homeErr: account.ErrAuth}
	s := &Source{Runner: (&recorder{}).run, Account: f, Cookies: granted("chrome")}
	if _, err := s.Suggested(context.Background()); !errors.Is(err, account.ErrAuth) {
		t.Fatalf("%v", err)
	}
	if _, err := s.Suggested(context.Background()); err == nil || len(f.asked) != 1 {
		t.Errorf("asked again after a refusal: %v (%v)", f.asked, err)
	}
	s.Refresh()
	f.homeErr, f.home = nil, quickPicks()
	if got, err := s.Suggested(context.Background()); err != nil || len(got) != 2 {
		t.Errorf("after r: %v %v", got, err)
	}
}

// TestTheHomeIsCutToAFewSectionsOfAFewItems.
func TestTheHomeIsCutToAFewSectionsOfAFewItems(t *testing.T) {
	var secs []music.Section
	for i := range 7 {
		sec := music.Section{Title: string(rune('A' + i))}
		for range 30 {
			sec.Tracks = append(sec.Tracks, music.Track{ID: "aaaaaaaaaaa", Title: "x"})
		}
		secs = append(secs, sec)
	}
	secs = append([]music.Section{{Title: "empty"}}, secs...)
	got := trimSections(secs)
	if len(got) != maxHomeSections || got[0].Title != "A" || len(got[0].Tracks) != maxSectionItems {
		t.Errorf("%d sections, first %q of %d", len(got), got[0].Title, len(got[0].Tracks))
	}
}

// TestTheChartComesFromTheDirectSourceAndIsKeptForTheNextSession; offline, the error is the answer and the kept chart stays.
func TestTheChartComesFromTheDirectSourceAndIsKeptForTheNextSession(t *testing.T) {
	ctx := context.Background()
	if _, err := (&Source{}).Popular(ctx); err == nil || !strings.Contains(err.Error(), "PIG_MUSIC_SEARCH=ytdlp") {
		t.Errorf("without the direct source: %v", err)
	}
	dir := t.TempDir()
	tracks := make([]music.Track, 80)
	for i := range tracks {
		tracks[i] = music.Track{ID: "aaaaaaaaaaa", Title: "T"}
	}
	in := &charts{sec: music.Section{Title: "Top 100", Tracks: tracks}}
	s := &Source{Inner: in, CacheDir: dir}
	got, err := s.Popular(ctx)
	if err != nil || got.Title != "Top 100" || len(got.Tracks) != maxPopularTracks {
		t.Fatalf("%q %d %v", got.Title, len(got.Tracks), err)
	}
	in.err = errors.New("dial tcp: no route to host")
	if _, err := s.Popular(ctx); err == nil {
		t.Fatal("offline is not an error")
	}
	next := &Source{CacheDir: dir}
	pop, sug, ok := next.CachedHome(ctx)
	if !ok || pop.Title != "Top 100" || len(pop.Tracks) != maxPopularTracks || sug != nil {
		t.Errorf("the next session's chart: %q %d %v %v", pop.Title, len(pop.Tracks), sug, ok)
	}
	if info, err := os.Stat(filepath.Join(dir, homeFile)); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the cache file: %v %v", info, err)
	}
	if _, _, ok := (&Source{CacheDir: t.TempDir()}).CachedHome(ctx); ok {
		t.Error("an empty cache is something")
	}
}
