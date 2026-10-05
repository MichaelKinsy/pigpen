package account

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../native/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// browseServer answers the browse endpoint the way YouTube Music does for the fixtures and records what it was sent.
type browseServer struct {
	*httptest.Server
	mu    sync.Mutex
	calls []map[string]any
	heads []http.Header
	deny  bool
}

func newBrowseServer(t *testing.T) *browseServer {
	t.Helper()
	bs := &browseServer{}
	first, cont, grid := fixture(t, "browse-playlist.json"), fixture(t, "browse-playlist-cont.json"), fixture(t, "browse-library-playlists.json")
	bs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		bs.mu.Lock()
		bs.calls = append(bs.calls, body)
		bs.heads = append(bs.heads, r.Header.Clone())
		deny := bs.deny
		bs.mu.Unlock()
		if deny {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		switch {
		case body["continuation"] != nil:
			_, _ = w.Write(cont)
		case body["browseId"] == "FEmusic_liked_playlists":
			_, _ = w.Write(grid)
		default:
			_, _ = w.Write(first)
		}
	}))
	t.Cleanup(bs.Close)
	return bs
}

func testLibrary(bs *browseServer) (*Library, *int) {
	jar, _ := ParseNetscape(strings.NewReader(netscape), now)
	loads := 0
	c := &Client{
		Jar:       func(context.Context) (Jar, error) { loads++; return jar, nil },
		BrowseURL: bs.URL,
		Now:       func() time.Time { return now },
	}
	return &Library{C: c}, &loads
}

func TestARequestIsSignedAndCarriesTheCookiesToTheirOriginOnly(t *testing.T) {
	bs := newBrowseServer(t)
	lib, _ := testLibrary(bs)
	if _, err := lib.Collections(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := bs.heads[0]
	if !strings.HasPrefix(h.Get("Authorization"), "SAPISIDHASH 1700000000_1d8ef72e") {
		t.Errorf("Authorization %q", h.Get("Authorization"))
	}
	if !strings.Contains(h.Get("Cookie"), "SAPISID=SAPISIDVALUE123") || strings.Contains(h.Get("Cookie"), "NOTMINE") {
		t.Errorf("Cookie %q", h.Get("Cookie"))
	}
	if h.Get("X-Origin") != "https://music.youtube.com" || h.Get("Origin") != "https://music.youtube.com" || h.Get("X-Goog-AuthUser") != "0" {
		t.Errorf("headers %v", h)
	}
}

func TestTheLibraryListsPlaylistsFromTheGrid(t *testing.T) {
	bs := newBrowseServer(t)
	lib, _ := testLibrary(bs)
	cols, err := lib.Collections(context.Background())
	if err != nil || len(cols) != 5 || cols[0].ID != "PLroadtrip0000001" {
		t.Fatalf("%v, %v", cols, err)
	}
	if bs.calls[0]["browseId"] != "FEmusic_liked_playlists" {
		t.Errorf("asked %v", bs.calls[0])
	}
}

func TestLikedSongsComeAPageAtATimeThroughContinuations(t *testing.T) {
	bs := newBrowseServer(t)
	lib, loads := testLibrary(bs)
	ctx := context.Background()
	got, more, err := lib.TracksPage(ctx, "LM", 0, 4)
	if err != nil || len(got) != 4 || !more {
		t.Fatalf("%d, %v, %v", len(got), more, err)
	}
	if bs.calls[0]["browseId"] != "VLLM" || len(bs.calls) != 1 {
		t.Errorf("asked %v", bs.calls)
	}
	got, more, err = lib.TracksPage(ctx, "LM", 4, 6) // 2 more of the first page, then the continuation
	if err != nil || len(got) != 6 {
		t.Fatalf("%d, %v, %v", len(got), more, err)
	}
	if len(bs.calls) != 2 || bs.calls[1]["continuation"] == nil {
		t.Errorf("calls %v", bs.calls)
	}
	got, more, _ = lib.TracksPage(ctx, "LM", 0, 10) // all of it is known now
	if len(got) != 10 || more || len(bs.calls) != 2 {
		t.Errorf("%d tracks, more %v, %d calls", len(got), more, len(bs.calls))
	}
	if *loads != 1 {
		t.Errorf("the cookies were obtained %d times", *loads)
	}
}

func TestForgetMakesTheNextListingAskAgainAndTheCookiesAreAskedForAgain(t *testing.T) {
	bs := newBrowseServer(t)
	lib, loads := testLibrary(bs)
	ctx := context.Background()
	_, _, _ = lib.TracksPage(ctx, "LM", 0, 2)
	lib.Forget()
	_, _, _ = lib.TracksPage(ctx, "LM", 0, 2)
	if len(bs.calls) != 2 || *loads != 2 {
		t.Errorf("%d calls, %d cookie loads", len(bs.calls), *loads)
	}
}

func TestAnAnswerOf401IsAnAuthErrorAndTheCookiesAreDropped(t *testing.T) {
	bs := newBrowseServer(t)
	bs.deny = true
	lib, loads := testLibrary(bs)
	_, err := lib.Collections(context.Background())
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("%v", err)
	}
	if strings.Contains(err.Error(), "SAPISID") || strings.Contains(err.Error(), "VALUE") {
		t.Errorf("the error shows a secret: %v", err)
	}
	bs.deny = false
	if _, err := lib.Collections(context.Background()); err != nil || *loads != 2 {
		t.Errorf("after the cookies were dropped: %v, %d loads", err, *loads)
	}
}

func TestAJarWithoutSAPISIDIsNotSignedIn(t *testing.T) {
	bs := newBrowseServer(t)
	c := &Client{Jar: func(context.Context) (Jar, error) { return Jar{{Name: "SID", Value: "x", Domain: "google.com"}}, nil }, BrowseURL: bs.URL}
	if _, err := (&Library{C: c}).Collections(context.Background()); !errors.Is(err, ErrAuth) || len(bs.calls) != 0 {
		t.Errorf("%v, %d requests", err, len(bs.calls))
	}
}

func TestAnUnknownShapeIsReportedSoTheCallerCanFallBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"contents":{}}`)) }))
	defer srv.Close()
	jar, _ := ParseNetscape(strings.NewReader(netscape), now)
	lib := &Library{C: &Client{Jar: func(context.Context) (Jar, error) { return jar, nil }, BrowseURL: srv.URL}}
	if _, _, err := lib.TracksPage(context.Background(), "LM", 0, 5); !errors.Is(err, ErrShape) {
		t.Errorf("%v", err)
	}
}

// Playing a long collection fetches its rest in the background while the cursor's pager fetches the next page of the same
// collection: two calls that both need the same continuation must not both append it.
func TestTwoCallersThatNeedTheSameContinuationFetchItOnce(t *testing.T) {
	bs := newBrowseServer(t)
	bs.Config.Handler = slowContinuation(bs.Config.Handler)
	lib, _ := testLibrary(bs)
	ctx := context.Background()
	if _, _, err := lib.TracksPage(ctx, "LM", 0, 2); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, from := range []int{6, 7} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = lib.TracksPage(ctx, "LM", from, 3)
		}()
	}
	wg.Wait()
	all, _, err := lib.TracksPage(ctx, "LM", 0, 100)
	seen := map[string]bool{}
	for _, tr := range all {
		if seen[tr.ID] {
			t.Errorf("%s twice", tr.ID)
		}
		seen[tr.ID] = true
	}
	if err != nil || len(all) != 10 {
		t.Errorf("%d tracks, %v", len(all), err)
	}
}

func slowContinuation(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		h.ServeHTTP(w, r)
	})
}

// The library grid lists albums too (browse ID MPREb_...): an album is browsed by its own ID, not as a "VL" playlist.
func TestAnAlbumIsBrowsedByItsOwnID(t *testing.T) {
	bs := newBrowseServer(t)
	lib, _ := testLibrary(bs)
	_, _, _ = lib.TracksPage(context.Background(), "MPREb_abcdefghijk", 0, 2)
	if len(bs.calls) != 1 || bs.calls[0]["browseId"] != "MPREb_abcdefghijk" {
		t.Errorf("asked %v", bs.calls)
	}
}
