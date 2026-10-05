package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/account"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// fakeAccount is the signed-in library client of package account, scripted.
type fakeAccount struct {
	cols   []music.Collection
	total  int
	err    error
	asked  []string
	forgot int
}

func (f *fakeAccount) Collections(context.Context) ([]music.Collection, error) {
	f.asked = append(f.asked, "collections")
	return f.cols, f.err
}

func (f *fakeAccount) TracksPage(_ context.Context, id string, from, n int) ([]music.Track, bool, error) {
	f.asked = append(f.asked, fmt.Sprintf("page %s %d+%d", id, from, n))
	if f.err != nil {
		return nil, false, f.err
	}
	var out []music.Track
	for i := from; i < min(from+n, f.total); i++ {
		out = append(out, music.Track{ID: fmt.Sprintf("acc%08dx", i), Title: "Acc"})
	}
	return out, from+n < f.total, nil
}

func (f *fakeAccount) Forget() { f.forgot++ }

func accountRig(t *testing.T, f *fakeAccount) (*Source, *recorder) {
	r := &recorder{out: map[string]string{"feed/playlists": playlistsJSON, "list=LM": likedJSON}}
	return &Source{Runner: r.run, Cookies: granted("chrome"), Account: f}, r
}

func TestTheLibraryIsListedByTheAccountWithoutYtdlp(t *testing.T) {
	f := &fakeAccount{cols: []music.Collection{{ID: "PLroad", Kind: "playlist", Title: "Road trip", Count: 12}}}
	s, r := accountRig(t, f)
	got, err := s.Library(context.Background())
	if err != nil || len(got) != 2 || got[0].ID != "LM" || got[1].ID != "PLroad" {
		t.Fatalf("%v, %v", got, err)
	}
	if len(r.calls) != 0 {
		t.Errorf("yt-dlp was run: %v", r.calls)
	}
	if _, err := s.Library(context.Background()); err != nil || len(f.asked) != 1 {
		t.Errorf("asked again: %v", f.asked)
	}
}

func TestPagesAndWholeCollectionsComeFromTheAccount(t *testing.T) {
	f := &fakeAccount{total: 450}
	s, r := accountRig(t, f)
	ctx := context.Background()
	got, more, err := s.TracksPage(ctx, "LM", 0, 50)
	if err != nil || len(got) != 50 || !more || len(r.calls) != 0 {
		t.Fatalf("%d, %v, %v, %d runs", len(got), more, err, len(r.calls))
	}
	all, err := s.Tracks(ctx, "LM")
	if err != nil || len(all) != 450 || len(r.calls) != 0 {
		t.Fatalf("%d tracks, %v, %d runs", len(all), err, len(r.calls))
	}
}

func TestWhenTheAccountIsRefusedYtdlpDoesItAndTheAccountIsNotAskedAgain(t *testing.T) {
	f := &fakeAccount{err: fmt.Errorf("%w (HTTP 401)", account.ErrAuth)}
	s, r := accountRig(t, f)
	ctx := context.Background()
	if got, err := s.Library(ctx); err != nil || len(got) < 2 {
		t.Fatalf("%v, %v", got, err)
	}
	if len(r.calls) != 1 || len(f.asked) != 1 {
		t.Fatalf("%d runs, asked %v", len(r.calls), f.asked)
	}
	if _, err := s.Tracks(ctx, "LM"); err != nil {
		t.Fatal(err)
	}
	if len(f.asked) != 1 {
		t.Errorf("the account was asked again after it refused: %v", f.asked)
	}
	s.Refresh() // the user asked again: the account gets another chance
	_, _ = s.Library(ctx)
	if len(f.asked) != 2 || f.forgot != 1 {
		t.Errorf("asked %v, forgot %d", f.asked, f.forgot)
	}
}

func TestAConsentErrorIsNotAFallbackAndTheAccountIsNeverAsked(t *testing.T) {
	f := &fakeAccount{}
	need := &music.NeedsConsentError{Browser: "chrome"}
	s := &Source{Runner: (&recorder{}).run, Cookies: func(context.Context) (string, error) { return "", need }, Account: f}
	if _, err := s.Library(context.Background()); !errors.As(err, &need) {
		t.Fatalf("%v", err)
	}
	if len(f.asked) != 0 {
		t.Errorf("the account was asked without consent: %v", f.asked)
	}
}

func TestANetworkErrorFromTheAccountFallsBackButDoesNotSwitchItOff(t *testing.T) {
	f := &fakeAccount{err: errors.New("connection reset")}
	s, _ := accountRig(t, f)
	_, _ = s.Library(context.Background())
	s.Refresh()
	f.err = nil
	f.cols = []music.Collection{{ID: "PLx", Kind: "playlist", Title: "X"}}
	if got, _ := s.Library(context.Background()); len(got) != 2 || got[1].ID != "PLx" {
		t.Errorf("%v", got)
	}
}

// The account holds the cookies of the browser that was in use when it was first asked. When another browser is chosen, or
// the browser is no longer allowed, those cookies must go: the library of the first browser's account must not be listed
// (and cached) as the second one's, and cookies whose consent was withdrawn must not stay in memory.
func TestChoosingAnotherBrowserOrLosingConsentDropsTheAccountsCookies(t *testing.T) {
	f := &fakeAccount{cols: []music.Collection{{ID: "PLroad", Kind: "playlist", Title: "Road trip"}}}
	spec := "chrome"
	var gone error
	s := &Source{Runner: (&recorder{}).run, Account: f, Cookies: func(context.Context) (string, error) { return spec, gone }}
	ctx := context.Background()
	if _, err := s.Library(ctx); err != nil {
		t.Fatal(err)
	}
	before := f.forgot
	spec = "firefox"
	_, _ = s.Library(ctx)
	if f.forgot == before {
		t.Error("another browser is in use and the first one's cookies are still used")
	}
	before = f.forgot
	gone = &music.NeedsConsentError{Browser: "firefox"}
	_, _ = s.Library(ctx)
	if f.forgot == before {
		t.Error("consent is gone and the cookies stay in memory")
	}
}

// Withdrawn consent deletes the remembered listing on the next library call of any kind, not only when the cache is read.
func TestWithdrawnConsentDeletesTheCacheOnAnyLibraryCall(t *testing.T) {
	dir := t.TempDir()
	f := &fakeAccount{total: 3}
	_, _ = diskRig(t, dir, f).Library(context.Background())
	if left, _ := os.ReadDir(dir); len(left) == 0 {
		t.Fatal("nothing was cached")
	}
	gone := diskRig(t, dir, f)
	gone.Cookies = func(context.Context) (string, error) { return "", &music.NeedsConsentError{Browser: "chrome"} }
	_, _ = gone.Library(context.Background())
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("the cache stayed after consent was withdrawn: %v", left)
	}
}

// When the cookies cannot be exported (no signed-in YouTube session in the browser, a keyring that does not answer), every
// later library call must not run the export again before running the yt-dlp listing: scrolling a collection would cost two
// yt-dlp runs a page. The account is off for the session, as after a refusal, until the user refreshes.
func TestAFailedCookieExportIsNotRepeatedOnEveryLibraryCall(t *testing.T) {
	exports := 0
	acc := &account.Library{C: &account.Client{Jar: func(context.Context) (account.Jar, error) {
		exports++
		return nil, errors.New("yt-dlp exported no YouTube cookies: ERROR: could not find chrome cookies database")
	}}}
	r := &recorder{out: map[string]string{"feed/playlists": playlistsJSON, "list=LM": likedJSON}}
	s := &Source{Runner: r.run, Cookies: granted("chrome"), Account: acc}
	ctx := context.Background()
	if _, err := s.Library(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, _, _ = s.TracksPage(ctx, "LM", i*2, 2)
	}
	if exports != 1 {
		t.Errorf("the cookies were exported %d times", exports)
	}
}
