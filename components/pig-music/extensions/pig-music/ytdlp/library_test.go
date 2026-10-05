package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// A hand-written listing of the account's playlists, in the shape yt-dlp gives a flat playlist page. It is a guess at
// the real thing until the first real run on a signed-in machine: see the progress note.
const playlistsJSON = `{"_type":"playlist","id":"playlists","title":"Playlists","entries":[
 {"_type":"url","ie_key":"YoutubeTab","id":"PLroadtrip0000001","title":"Road trip","url":"https://www.youtube.com/playlist?list=PLroadtrip0000001","playlist_count":12},
 {"_type":"url","ie_key":"YoutubeTab","id":"PLfocus00000000002","title":"Focus","url":"https://www.youtube.com/playlist?list=PLfocus00000000002"},
 {"_type":"url","ie_key":"YoutubeTab","id":"LL","title":"Liked videos","url":"https://www.youtube.com/playlist?list=LL"},
 {"_type":"url","ie_key":"YoutubeTab","id":"LM","title":"Liked Music","url":"https://www.youtube.com/playlist?list=LM"},
 {"_type":"url","ie_key":"YoutubeTab","id":"PLnotitle00000000003","title":null},
 {"_type":"url","ie_key":"YoutubeTab","title":"No id"}]}`

const likedJSON = `{"_type":"playlist","id":"LM","title":"Liked Music","entries":[
 {"_type":"url","ie_key":"Youtube","id":"aaaaaaaaaaa","title":"First liked","duration":201},
 {"_type":"url","ie_key":"Youtube","id":"bbbbbbbbbbb","title":"Second liked","channel":"Some Artist"}]}`

type recorder struct {
	calls [][]string
	out   map[string]string // URL substring -> stdout
	err   error
}

func (r *recorder) run(_ context.Context, _ string, args []string) ([]byte, []byte, error) {
	r.calls = append(r.calls, args)
	joined := strings.Join(args, " ")
	for k, v := range r.out {
		if strings.Contains(joined, k) {
			return []byte(v), nil, r.err
		}
	}
	return nil, []byte("ERROR: unexpected " + joined), errors.New("exit status 1")
}

func (r *recorder) has(i int, flag string) bool {
	for _, a := range r.calls[i] {
		if a == flag {
			return true
		}
	}
	return false
}

func granted(spec string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return spec, nil }
}

func TestLibraryListsLikedSongsFirstThenThePlaylists(t *testing.T) {
	r := &recorder{out: map[string]string{"feed/playlists": playlistsJSON}}
	s := &Source{Runner: r.run, Cookies: granted("chrome")}
	got, err := s.Library(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != (music.Collection{ID: "LM", Kind: "liked", Title: "Liked songs"}) ||
		got[1] != (music.Collection{ID: "PLroadtrip0000001", Kind: "playlist", Title: "Road trip", Count: 12}) ||
		got[2].ID != "PLfocus00000000002" || got[2].Count != 0 {
		t.Fatalf("%+v", got)
	}
	if len(r.calls) != 1 {
		t.Fatalf("%d yt-dlp calls for the library", len(r.calls))
	}
	joined := strings.Join(r.calls[0], " ")
	if !strings.Contains(joined, "--cookies-from-browser chrome") || !strings.Contains(joined, "--flat-playlist") {
		t.Errorf("args %q", joined)
	}
	// the cookie flag comes before the URL, and the URL is after "--"
	if strings.Index(joined, "--cookies-from-browser") > strings.Index(joined, " -- ") {
		t.Errorf("flag after the URL: %q", joined)
	}
}

func TestTracksOfACollectionAreALibraryCallAndCarryTheCookieFlag(t *testing.T) {
	r := &recorder{out: map[string]string{"list=LM": likedJSON}}
	s := &Source{Runner: r.run, Cookies: granted("firefox:work"), JSRuntime: "node"}
	tracks, err := s.Tracks(context.Background(), "LM")
	if err != nil || len(tracks) != 2 || tracks[0].Title != "First liked" {
		t.Fatalf("%v %+v", err, tracks)
	}
	joined := strings.Join(r.calls[0], " ")
	if !strings.Contains(joined, "--cookies-from-browser firefox:work") || !strings.Contains(joined, "--js-runtimes node") ||
		!strings.Contains(joined, "https://music.youtube.com/playlist?list=LM") {
		t.Errorf("args %q", joined)
	}
}

// The central promise: the cookie flag is on library calls and on nothing else.
func TestTheCookieFlagIsNeverOnSearchEnrichOrAnythingButTheLibrary(t *testing.T) {
	r := &recorder{out: map[string]string{
		"search?q=":      `{"entries":[{"id":"aaaaaaaaaaa","title":"T"}]}`,
		"watch?v=":       `{"id":"aaaaaaaaaaa","duration":10}`,
		"feed/playlists": playlistsJSON,
		"list=LM":        likedJSON,
	}}
	s := &Source{Runner: r.run, Cookies: granted("chrome")}
	ctx := context.Background()
	if _, err := s.Search(ctx, "q", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enrich(ctx, music.Track{ID: "aaaaaaaaaaa"}); err != nil {
		t.Fatal(err)
	}
	_ = s.PlayURL(music.Track{ID: "aaaaaaaaaaa"})
	for i, c := range r.calls {
		for _, a := range c {
			if strings.HasPrefix(a, "--cookies") {
				t.Errorf("call %d (%v) carries %s", i, c, a)
			}
		}
	}
	if len(r.calls) != 2 {
		t.Fatalf("%d calls", len(r.calls))
	}
	if _, err := s.Library(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tracks(ctx, "LM"); err != nil {
		t.Fatal(err)
	}
	for i := 2; i < 4; i++ {
		if !r.has(i, "--cookies-from-browser") {
			t.Errorf("library call %d lacks the flag: %v", i, r.calls[i])
		}
	}
}

func TestWithoutConsentOrABrowserNothingRunsAndTheReasonComesBack(t *testing.T) {
	r := &recorder{}
	need := &music.NeedsConsentError{Browser: "chrome", Description: "chrome (the default browser, com.google.chrome)", Notes: "Keychain"}
	for name, err := range map[string]error{"consent": need, "no browser": &music.NoBrowserError{Reason: "there is no graphical session here"}} {
		s := &Source{Runner: r.run, Cookies: func(context.Context) (string, error) { return "", err }}
		if _, got := s.Library(context.Background()); !errors.Is(got, err) && got != err {
			t.Errorf("%s: library: %v", name, got)
		}
		if _, got := s.Tracks(context.Background(), "LM"); got != err {
			t.Errorf("%s: tracks: %v", name, got)
		}
	}
	s := &Source{Runner: r.run} // no provider at all: no cookie access is configured
	var nb *music.NoBrowserError
	if _, err := s.Library(context.Background()); !errors.As(err, &nb) {
		t.Errorf("no provider: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("yt-dlp ran %d times without consent", len(r.calls))
	}
}

func TestLibraryErrorsKeepYtdlpsMessageAndNeverAnyCookie(t *testing.T) {
	r := &recorder{} // every call fails with "ERROR: unexpected ..."
	s := &Source{Runner: r.run, Cookies: granted("chrome")}
	_, err := s.Library(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ERROR: unexpected") {
		t.Errorf("%v", err)
	}
	if _, err := s.Tracks(context.Background(), "not an id!"); err == nil {
		t.Error("a bad collection ID was accepted")
	}
}

func TestMacOSBlockingTheCookieStoreIsExplainedWithTheFix(t *testing.T) {
	blocked := func(context.Context, string, []string) ([]byte, []byte, error) {
		return nil, []byte("ERROR: [Errno 1] Operation not permitted: '/Users/u/Library/Containers/com.apple.Safari/Data/Library/Cookies/Cookies.binarycookies'\n"), errors.New("exit status 1")
	}
	s := &Source{Runner: blocked, Cookies: granted("safari")}
	for name, call := range map[string]func() error{
		"library": func() error { _, err := s.Library(context.Background()); return err },
		"tracks":  func() error { _, err := s.Tracks(context.Background(), "LM"); return err },
	} {
		err := call()
		if err == nil || !strings.Contains(err.Error(), "Operation not permitted") || !strings.Contains(err.Error(), "Full Disk Access") || !strings.Contains(err.Error(), "cookieBrowser") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// other errors are untouched
	s = &Source{Runner: (&recorder{}).run, Cookies: granted("safari")}
	if _, err := s.Library(context.Background()); err == nil || strings.Contains(err.Error(), "Full Disk Access") {
		t.Errorf("%v", err)
	}
}

// M9c: every library call made yt-dlp read and decrypt the browser's whole cookie store, and the liked songs were paged
// through to the end (566 of them) before anything showed. Listings are remembered for the session, and a long collection
// is fetched a page at a time.
func TestTheLibraryIsAskedOncePerSessionUntilRefreshed(t *testing.T) {
	r := &recorder{out: map[string]string{"feed/playlists": playlistsJSON}}
	s := &Source{Runner: r.run, Cookies: granted("chrome")}
	for i := 0; i < 3; i++ {
		if _, err := s.Library(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.calls) != 1 {
		t.Fatalf("%d yt-dlp runs for three library listings", len(r.calls))
	}
	s.Refresh()
	if _, err := s.Library(context.Background()); err != nil || len(r.calls) != 2 {
		t.Fatalf("after Refresh: %d runs, %v", len(r.calls), err)
	}
}

func TestAFailedListingIsNotRemembered(t *testing.T) {
	r := &recorder{out: map[string]string{"feed/playlists": playlistsJSON}, err: errors.New("exit status 1")}
	s := &Source{Runner: r.run, Cookies: granted("chrome")}
	if _, err := s.Library(context.Background()); err == nil {
		t.Fatal("no error")
	}
	r.err = nil
	if got, err := s.Library(context.Background()); err != nil || len(got) == 0 {
		t.Fatalf("%v, %v", got, err)
	}
}

func TestTracksOfAnOpenedCollectionAreRememberedToo(t *testing.T) {
	r := &recorder{out: map[string]string{"list=LM": likedJSON}}
	s := &Source{Runner: r.run, Cookies: granted("chrome")}
	for i := 0; i < 2; i++ {
		if got, err := s.Tracks(context.Background(), "LM"); err != nil || len(got) != 2 {
			t.Fatalf("%v, %v", got, err)
		}
	}
	if len(r.calls) != 1 {
		t.Fatalf("%d runs", len(r.calls))
	}
}

// pagedRecorder answers a --playlist-items range of a 5-entry collection, as yt-dlp does.
func pagedRecorder(total int) (*recorder, Runner) {
	r := &recorder{}
	return r, func(_ context.Context, _ string, args []string) ([]byte, []byte, error) {
		r.calls = append(r.calls, args)
		from, to := 1, total
		for i, a := range args {
			if a == "--playlist-items" && i+1 < len(args) {
				var f, e int
				if _, err := fmtSscan(args[i+1], &f, &e); err == nil {
					from, to = f, min(e, total)
				}
			}
		}
		var entries []string
		for n := from; n <= to; n++ {
			entries = append(entries, `{"_type":"url","id":"`+id11(n)+`","title":"Song `+string(rune('A'+n%26))+`"}`)
		}
		return []byte(`{"_type":"playlist","id":"LM","entries":[` + strings.Join(entries, ",") + `]}`), nil, nil
	}
}

func TestACollectionIsFetchedAPageAtATimeAndRemembered(t *testing.T) {
	r, run := pagedRecorder(5)
	s := &Source{Runner: run, Cookies: granted("chrome")}
	ctx := context.Background()
	first, more, err := s.TracksPage(ctx, "LM", 0, 2)
	if err != nil || len(first) != 2 || !more {
		t.Fatalf("first page %d tracks, more=%v, %v", len(first), more, err)
	}
	if !r.has(0, "--cookies-from-browser") || !r.has(0, "--playlist-items") {
		t.Errorf("args %q", r.calls[0])
	}
	second, more, err := s.TracksPage(ctx, "LM", 2, 2)
	if err != nil || len(second) != 2 || !more || second[0].ID == first[0].ID {
		t.Fatalf("second page %v, more=%v, %v", second, more, err)
	}
	last, more, err := s.TracksPage(ctx, "LM", 4, 2)
	if err != nil || len(last) != 1 || more {
		t.Fatalf("last page %d tracks, more=%v, %v", len(last), more, err)
	}
	runs := len(r.calls)
	// a page already fetched is not fetched again
	again, more, err := s.TracksPage(ctx, "LM", 0, 2)
	if err != nil || len(again) != 2 || !more || len(r.calls) != runs {
		t.Fatalf("remembered page: %d tracks, more=%v, %d runs (was %d), %v", len(again), more, len(r.calls), runs, err)
	}
	// and the whole collection, now known, is answered without a run
	if all, err := s.Tracks(ctx, "LM"); err != nil || len(all) != 5 || len(r.calls) != runs {
		t.Fatalf("Tracks after the pages: %d tracks, %d runs (was %d), %v", len(all), len(r.calls), runs, err)
	}
}

func TestAPageNeverAsksForCookiesWithoutConsent(t *testing.T) {
	_, run := pagedRecorder(5)
	need := &music.NeedsConsentError{Browser: "chrome"}
	s := &Source{Runner: run, Cookies: func(context.Context) (string, error) { return "", need }}
	if _, _, err := s.TracksPage(context.Background(), "LM", 0, 2); !errors.As(err, &need) {
		t.Fatalf("%v", err)
	}
	if _, _, err := s.TracksPage(context.Background(), "../x", 0, 2); err == nil {
		t.Error("a bad collection ID was accepted")
	}
}

func TestAPageThatIsNotTheNextOneIsFetchedButNotMixedIn(t *testing.T) {
	r, run := pagedRecorder(10)
	s := &Source{Runner: run, Cookies: granted("chrome")}
	ctx := context.Background()
	got, _, err := s.TracksPage(ctx, "LM", 4, 2) // the cursor jumped: nothing before it is known
	if err != nil || len(got) != 2 {
		t.Fatalf("%v, %v", got, err)
	}
	all, err := s.Tracks(ctx, "LM")
	if err != nil || len(all) != 10 {
		t.Fatalf("Tracks must list the whole collection, got %d, %v", len(all), err)
	}
	_ = r
}

func id11(n int) string { return fmt.Sprintf("id%08dx", n) }

func fmtSscan(s string, from, to *int) (int, error) { return fmt.Sscanf(s, "%d-%d", from, to) }
