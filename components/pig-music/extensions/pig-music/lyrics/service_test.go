package lyrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func data(t *testing.T, name string) []byte {
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type fakeLRCLIB struct {
	*httptest.Server
	mu     sync.Mutex
	paths  []string
	agents []string
	get    []byte // /api/get answer (nil = 404)
	search []byte
	status int
}

func newFake(t *testing.T) *fakeLRCLIB {
	f := &fakeLRCLIB{get: data(t, "lrclib-get.json"), search: data(t, "lrclib-search.json")}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.agents = append(f.agents, r.Header.Get("User-Agent"))
		f.mu.Unlock()
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("a credential was sent to LRCLIB")
		}
		if f.status != 0 {
			http.Error(w, "boom", f.status)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/get") && f.get != nil:
			_, _ = w.Write(f.get)
		case strings.HasPrefix(r.URL.Path, "/api/get"):
			w.WriteHeader(404)
			_, _ = w.Write(data(t, "lrclib-notfound.json"))
		case strings.HasPrefix(r.URL.Path, "/api/search"):
			_, _ = w.Write(f.search)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

var example = music.Track{ID: "aaaaaaaaaaa", Title: "Example Song", Artists: []string{"Example Artist"}, Album: "Example Album", Duration: 200 * time.Second}

func svc(f *fakeLRCLIB, dir string) *Service {
	return &Service{Dir: dir, BaseURL: f.URL, UserAgent: "pig-music-test (https://example.invalid)"}
}

func TestSyncedLyricsComeFromLRCLIBByArtistTitleAlbumAndDuration(t *testing.T) {
	f := newFake(t)
	l, err := svc(f, "").Lyrics(context.Background(), example)
	if err != nil || !l.Synced || l.Source != "lrclib" || len(l.Lines) != 4 || l.Lines[1].Text != "Second example line" {
		t.Fatalf("%+v %v", l, err)
	}
	q := f.paths[0]
	for _, want := range []string{"artist_name=Example+Artist", "track_name=Example+Song", "album_name=Example+Album", "duration=200"} {
		if !strings.Contains(q, want) {
			t.Errorf("request lacks %q: %s", want, q)
		}
	}
	if !strings.Contains(f.agents[0], "pig-music") {
		t.Errorf("User-Agent %q", f.agents[0])
	}
}

func TestWhenGetFindsNothingTheSearchPicksTheTrackOfTheRightLength(t *testing.T) {
	f := newFake(t)
	f.get = nil
	l, err := svc(f, "").Lyrics(context.Background(), example)
	if err != nil || len(l.Lines) != 3 || l.Lines[0].Text != "Found by search" {
		t.Fatalf("%+v %v", l, err)
	}
}

func TestAnInstrumentalAnswerIsInstrumentalNotMissing(t *testing.T) {
	f := newFake(t)
	f.get = []byte(`{"id":4,"trackName":"Example Song","artistName":"Example Artist","duration":200,"instrumental":true,"plainLyrics":null,"syncedLyrics":null}`)
	l, err := svc(f, "").Lyrics(context.Background(), example)
	if err != nil || !l.Instrumental {
		t.Errorf("%+v %v", l, err)
	}
}

func TestWithNothingAtLRCLIBYouTubesOwnLyricsAreAPlainSheet(t *testing.T) {
	f := newFake(t)
	f.get, f.search = nil, []byte(`[]`)
	s := svc(f, "")
	s.Fallback = func(_ context.Context, id string) (string, error) { return "from youtube\n\nsecond\nthird", nil }
	l, err := s.Lyrics(context.Background(), example)
	if err != nil || l.Synced || l.Source != "youtube" || len(l.Lines) != 4 {
		t.Fatalf("%+v %v", l, err)
	}
}

func TestNoLyricsAnywhereIsErrNoLyricsAndIsRememberedForADay(t *testing.T) {
	f := newFake(t)
	f.get, f.search = nil, []byte(`[]`)
	dir := t.TempDir()
	s := svc(f, dir)
	now := time.Now()
	s.Now = func() time.Time { return now }
	if _, err := s.Lyrics(context.Background(), example); !errors.Is(err, music.ErrNoLyrics) {
		t.Fatal(err)
	}
	n := len(f.paths)
	if _, err := s.Lyrics(context.Background(), example); !errors.Is(err, music.ErrNoLyrics) || len(f.paths) != n {
		t.Errorf("asked again: %d -> %d", n, len(f.paths))
	}
	now = now.Add(25 * time.Hour)
	_, _ = s.Lyrics(context.Background(), example)
	if len(f.paths) == n {
		t.Error("a day-old no was believed")
	}
}

func TestAFoundSheetIsKeptOnDiskByVideoIDAndNeedsNoNetworkTheSecondTime(t *testing.T) {
	f := newFake(t)
	dir := t.TempDir()
	if _, err := svc(f, dir).Lyrics(context.Background(), example); err != nil {
		t.Fatal(err)
	}
	n := len(f.paths)
	f.status = 500
	l, err := svc(f, dir).Lyrics(context.Background(), example) // a new process
	if err != nil || len(l.Lines) != 4 || len(f.paths) != n {
		t.Errorf("%v %v %d", l, err, len(f.paths))
	}
	if st, err := os.Stat(filepath.Join(dir, "aaaaaaaaaaa.json")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("%v %v", st, err)
	}
}

func TestANetworkErrorIsAnErrorAndIsNotRemembered(t *testing.T) {
	f := newFake(t)
	f.status = 500
	dir := t.TempDir()
	if _, err := svc(f, dir).Lyrics(context.Background(), example); err == nil || errors.Is(err, music.ErrNoLyrics) {
		t.Fatalf("%v", err)
	}
	f.status = 0
	if l, err := svc(f, dir).Lyrics(context.Background(), example); err != nil || len(l.Lines) == 0 {
		t.Errorf("after the outage: %v %v", l, err)
	}
}

func TestTheCacheIsCappedInFiles(t *testing.T) {
	f := newFake(t)
	dir := t.TempDir()
	s := svc(f, dir)
	s.MaxFiles = 5
	for i := 0; i < 12; i++ {
		tr := example
		tr.ID = "track" + string(rune('A'+i)) + "xxxxxx"
		if _, err := s.Lyrics(context.Background(), tr); err != nil {
			t.Fatal(err)
		}
	}
	if left, _ := os.ReadDir(dir); len(left) > 5 {
		t.Errorf("%d files", len(left))
	}
}

func TestAnEmptyOrBadIDIsNeverAFileName(t *testing.T) {
	f := newFake(t)
	dir := t.TempDir()
	tr := example
	tr.ID = "../../etc/passwd"
	_, _ = svc(f, dir).Lyrics(context.Background(), tr)
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("%v", left)
	}
}

func TestOffsetsAreRememberedPerTrackAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	s := &Service{Dir: dir}
	if s.Offset("aaaaaaaaaaa") != 0 {
		t.Error("a default offset")
	}
	s.SetOffset("aaaaaaaaaaa", 300*time.Millisecond)
	s.SetOffset("bbbbbbbbbbb", -200*time.Millisecond)
	s.SetOffset("bbbbbbbbbbb", 0) // zero forgets it
	again := &Service{Dir: dir}
	if again.Offset("aaaaaaaaaaa") != 300*time.Millisecond || again.Offset("bbbbbbbbbbb") != 0 {
		t.Errorf("%v %v", again.Offset("aaaaaaaaaaa"), again.Offset("bbbbbbbbbbb"))
	}
}

func TestAPlaceholderSheetOnLRCLIBIsNotBelievedAndTheSearchIsTried(t *testing.T) {
	f := newFake(t)
	f.get = []byte(`{"id":9,"trackName":"Example Song","artistName":"Example Artist","duration":200,"instrumental":false,"plainLyrics":"probe","syncedLyrics":"[00:00.00]probe"}`)
	f.search = []byte(`[{"trackName":"Example Song","artistName":"Example Artist","duration":260,"syncedLyrics":"[00:01.00] a\n[00:02.00] b\n[00:03.00] c"},{"trackName":"Example Song","artistName":"Example Artist","duration":200,"syncedLyrics":"[00:01.00] right\n[00:02.00] one\n[00:03.00] here"}]`)
	l, err := svc(f, "").Lyrics(context.Background(), example)
	if err != nil || len(l.Lines) != 3 || l.Lines[0].Text != "right" {
		t.Errorf("%+v %v", l, err)
	}
}

func TestWhenLRCLIBIsDownYouTubesLyricsAreStillTriedButADownLRCLIBWithNothingElseIsAnError(t *testing.T) {
	f := newFake(t)
	f.status = 503
	s := svc(f, t.TempDir())
	s.Fallback = func(context.Context, string) (string, error) { return "one\ntwo\nthree", nil }
	l, err := s.Lyrics(context.Background(), example)
	if err != nil || l.Source != "youtube" {
		t.Fatalf("%+v %v", l, err)
	}
	other := example
	other.ID = "bbbbbbbbbbb"
	s.Fallback = func(context.Context, string) (string, error) { return "", music.ErrNoLyrics }
	if _, err := s.Lyrics(context.Background(), other); err == nil || errors.Is(err, music.ErrNoLyrics) {
		t.Errorf("a down service must not be remembered as 'no lyrics': %v", err)
	}
}
