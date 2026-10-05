package ytdlp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type call struct{ args []string }

// script answers each run in order and records the arguments.
func script(calls *[]call, answers ...func() ([]byte, []byte, error)) Runner {
	i := 0
	return func(ctx context.Context, bin string, args []string) ([]byte, []byte, error) {
		*calls = append(*calls, call{append([]string{bin}, args...)})
		if i >= len(answers) {
			return nil, nil, errors.New("unexpected extra run")
		}
		a := answers[i]
		i++
		return a()
	}
}

func ok(b []byte) func() ([]byte, []byte, error) {
	return func() ([]byte, []byte, error) { return b, nil, nil }
}

func fail(stderr string) func() ([]byte, []byte, error) {
	return func() ([]byte, []byte, error) { return nil, []byte(stderr), errors.New("exit status 1") }
}

func TestParseKeepsOnlyPlayableVideosOnce(t *testing.T) {
	tracks, err := ParseTracks(fixture(t, "search-music.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tr := range tracks {
		ids = append(ids, tr.ID)
	}
	// The playlist, the channel, the private video, the duplicate and the 5-character ID are gone.
	if want := []string{"aaaaaaaaaaa", "bbbbbbbbbbb", "ddddddddddd", "eeeeeeeeeee"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids %v, want %v", ids, want)
	}
	a := tracks[0]
	if a.Title != "Night Drive" || !reflect.DeepEqual(a.Artists, []string{"Example Artist"}) || a.Duration != 215*time.Second ||
		a.ArtURL != "https://i.ytimg.com/vi/aaaaaaaaaaa/large.jpg" {
		t.Errorf("first track %+v", a)
	}
	if tracks[1].Artists[0] != "Second Artist" || tracks[1].Duration != 301*time.Second {
		t.Errorf("a ' - Topic' channel is not the artist name: %+v", tracks[1])
	}
	if tracks[2].Duration != 0 || tracks[2].Artists[0] != "Third Uploader" {
		t.Errorf("a missing duration or an uploader-only entry: %+v", tracks[2])
	}
	if tracks[3].Title != "Ünïcode Títle ♪ 夜" || tracks[3].Duration != 180500*time.Millisecond {
		t.Errorf("unicode or fractional duration: %+v", tracks[3])
	}
}

func TestParseASingleVideoUsesTrackArtistsAndAlbum(t *testing.T) {
	tracks, err := ParseTracks(fixture(t, "video.json"))
	if err != nil || len(tracks) != 1 {
		t.Fatalf("%v %v", tracks, err)
	}
	want := music.Track{ID: "kkkkkkkkkkk", Title: "Full Title", Artists: []string{"Artist Three", "Guest Artist"}, Album: "The Album", Duration: 187 * time.Second, ArtURL: "https://i.ytimg.com/vi/kkkkkkkkkkk/maxresdefault.jpg"}
	if !reflect.DeepEqual(tracks[0], want) {
		t.Fatalf("%+v\nwant %+v", tracks[0], want)
	}
}

func TestParseSplitsACommaJoinedArtistField(t *testing.T) {
	tracks, err := ParseTracks([]byte(`{"id":"lllllllllll","title":"x","artist":"A, B,  C"}`))
	if err != nil || !reflect.DeepEqual(tracks[0].Artists, []string{"A", "B", "C"}) {
		t.Fatalf("%+v %v", tracks, err)
	}
}

func TestParseRefusesWhatIsNotAListing(t *testing.T) {
	for _, in := range []string{``, `not json`, `[]`, `{}`, `{"title":"no id"}`} {
		if _, err := ParseTracks([]byte(in)); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
	// An empty result is not an error: there is just nothing.
	tracks, err := ParseTracks([]byte(`{"_type":"playlist","entries":[]}`))
	if err != nil || len(tracks) != 0 {
		t.Errorf("empty entries: %v %v", tracks, err)
	}
}

func TestSearchAsksTheMusicSongsSearchFirst(t *testing.T) {
	var calls []call
	s := &Source{Bin: "/opt/yt-dlp", Runner: script(&calls, ok(fixture(t, "search-music.json")))}
	tracks, err := s.Search(context.Background(), "night drive & more", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 3 {
		t.Fatalf("limit 3 gave %d tracks", len(tracks))
	}
	if len(calls) != 1 {
		t.Fatalf("%d runs", len(calls))
	}
	want := []string{"/opt/yt-dlp", "--ignore-config", "--no-warnings", "--no-progress", "--flat-playlist", "--playlist-end", "3", "-J", "--", "https://music.youtube.com/search?q=night+drive+%26+more#songs"}
	if !reflect.DeepEqual(calls[0].args, want) {
		t.Fatalf("args\n got %q\nwant %q", calls[0].args, want)
	}
}

func TestSearchFallsBackToYoutubeSearchWhenTheSongsSearchFailsOrIsEmpty(t *testing.T) {
	for name, first := range map[string]func() ([]byte, []byte, error){
		"fails":      fail("ERROR: Unable to download webpage"),
		"empty":      ok([]byte(`{"_type":"playlist","entries":[]}`)),
		"unreadable": ok([]byte(`<html>`)),
	} {
		t.Run(name, func(t *testing.T) {
			var calls []call
			s := &Source{Runner: script(&calls, first, ok(fixture(t, "search-ytsearch.json")))}
			tracks, err := s.Search(context.Background(), "-weird query", 5)
			if err != nil || len(tracks) != 2 || tracks[0].Artists[0] != "Some Band" {
				t.Fatalf("%+v %v", tracks, err)
			}
			if got := calls[1].args[len(calls[1].args)-2:]; !reflect.DeepEqual(got, []string{"--", "ytsearch5:-weird query"}) {
				t.Fatalf("fallback args end %q", got)
			}
		})
	}
}

func TestSearchReportsBothFailuresWithYtdlpsOwnWords(t *testing.T) {
	var calls []call
	s := &Source{Runner: script(&calls, fail("WARNING: x\nERROR: HTTP Error 429: Too Many Requests"), fail("ERROR: the fallback broke"))}
	_, err := s.Search(context.Background(), "q", 5)
	if err == nil || !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "the fallback broke") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchValidatesAndClampsItsInput(t *testing.T) {
	var calls []call
	s := &Source{Runner: script(&calls, ok(fixture(t, "search-music.json")))}
	if _, err := s.Search(context.Background(), "   ", 5); err == nil {
		t.Error("empty query accepted")
	}
	if len(calls) != 0 {
		t.Error("yt-dlp ran for an empty query")
	}
	if _, err := s.Search(context.Background(), "q", 1000); err != nil {
		t.Fatal(err)
	}
	if n := calls[0].args[len(calls[0].args)-4]; n != "50" {
		t.Errorf("limit not clamped: --playlist-end %s", n)
	}
	calls = nil
	s = &Source{Runner: script(&calls, ok(fixture(t, "search-music.json")))}
	if _, err := s.Search(context.Background(), "q", 0); err != nil || calls[0].args[len(calls[0].args)-4] != "10" {
		t.Errorf("default limit: %v %v", err, calls)
	}
}

func TestAMissingYtdlpNamesWhatToInstallAndSkipsTheFallback(t *testing.T) {
	runs := 0
	s := &Source{Bin: "yt-dlp", Runner: func(context.Context, string, []string) ([]byte, []byte, error) {
		runs++
		return nil, nil, &exec.Error{Name: "yt-dlp", Err: exec.ErrNotFound}
	}}
	_, err := s.Search(context.Background(), "q", 5)
	var missing *music.MissingError
	if !errors.As(err, &missing) || missing.Names[0] != "yt-dlp" || runs != 1 {
		t.Fatalf("err %v after %d runs", err, runs)
	}
	if !strings.Contains(err.Error(), "yt-dlp") || !strings.Contains(err.Error(), "install") && !strings.Contains(err.Error(), "Install") {
		t.Errorf("message does not say what to install: %v", err)
	}
	_, err = (&Source{Bin: "/no/such/yt-dlp", Cookies: granted("chrome")}).Tracks(context.Background(), "PLabc")
	if !errors.As(err, &missing) {
		t.Errorf("a missing absolute path: %v", err)
	}
}

func TestATimeoutIsReportedAsOne(t *testing.T) {
	s := &Source{Cookies: granted("chrome"), Timeout: 50 * time.Millisecond, LibraryTimeout: 50 * time.Millisecond, Runner: func(ctx context.Context, _ string, _ []string) ([]byte, []byte, error) {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}}
	start := time.Now()
	_, err := s.Tracks(context.Background(), "PLabc")
	if err == nil || !strings.Contains(err.Error(), "did not finish") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
}

func TestYtdlpsStderrBecomesTheErrorMessage(t *testing.T) {
	s := &Source{Cookies: granted("chrome"), Runner: func(context.Context, string, []string) ([]byte, []byte, error) {
		return nil, fixture(t, "error-sign-in.txt"), errors.New("exit status 1")
	}}
	_, err := s.Tracks(context.Background(), "PLabc")
	if err == nil || !strings.HasPrefix(err.Error(), "yt-dlp: ERROR: [youtube] kkkkkkkkkkk: Sign in to confirm") {
		t.Fatalf("err = %v", err)
	}
}

func TestTracksOfAPlaylist(t *testing.T) {
	var calls []call
	s := &Source{Cookies: granted("chrome"), Runner: script(&calls, ok(fixture(t, "playlist.json")))}
	tracks, err := s.Tracks(context.Background(), "PLmockplaylist000000000000000000000")
	if err != nil || len(tracks) != 2 || tracks[1].Title != "Second Song" {
		t.Fatalf("%+v %v", tracks, err)
	}
	if last := calls[0].args[len(calls[0].args)-1]; last != "https://music.youtube.com/playlist?list=PLmockplaylist000000000000000000000" {
		t.Errorf("url %s", last)
	}
	for _, bad := range []string{"", "x", "--exec=rm -rf", "a b", "PL/../x", "https://x.example/?list=1"} {
		if _, err := s.Tracks(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "not a playlist ID") {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}
}

func TestPlayURL(t *testing.T) {
	got := (&Source{}).PlayURL(music.Track{ID: "aaaaaaaaaaa"})
	if got != "https://music.youtube.com/watch?v=aaaaaaaaaaa" {
		t.Fatal(got)
	}
}

func TestExecRunnerRunsAProgramAndBoundsIt(t *testing.T) {
	out, errOut, err := ExecRunner(context.Background(), "sh", []string{"-c", "echo out; echo err >&2"})
	if err != nil || string(out) != "out\n" || string(errOut) != "err\n" {
		t.Fatalf("%q %q %v", out, errOut, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, _, err := ExecRunner(ctx, "sh", []string{"-c", "sleep 30"}); err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("a timed-out run: %v after %s", err, time.Since(start))
	}
	if _, _, err := ExecRunner(context.Background(), "pig-music-no-such-program", nil); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("missing program: %v", err)
	}
}

func TestParseRealYtdlpOutput(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile("testdata/recorded/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	songs, err := ParseTracks(read("music-songs.json"))
	if err != nil || len(songs) != 5 {
		t.Fatalf("songs: %d %v", len(songs), err)
	}
	// A songs-section entry is an ID and a title, nothing else.
	if songs[0].ID != "yy9cLbSiz_E" || songs[0].Title != "Night Drive" || len(songs[0].Artists) != 0 || songs[0].Duration != 0 {
		t.Errorf("first song %+v", songs[0])
	}
	// The unfiltered page opens with three browse entries without a title: not tracks.
	mixed, err := ParseTracks(read("music-search.json"))
	if err != nil || len(mixed) != 2 || mixed[0].ID != "4elrJ_jp0AI" || mixed[1].Title != "Night Drive" {
		t.Errorf("unfiltered page: %+v %v", mixed, err)
	}
	web, err := ParseTracks(read("ytsearch.json"))
	if err != nil || len(web) != 3 || web[0].ID != "HUUy3mnAhCE" || web[0].Artists[0] != "skeler." || web[0].Duration != 9361*time.Second {
		t.Errorf("ytsearch: %+v %v", web, err)
	}
	// Two of the five playlist entries are unavailable videos: no title, no duration. They are dropped.
	pl, err := ParseTracks(read("playlist.json"))
	if err != nil || len(pl) != 3 || pl[0].ID != "A7uNvvAKsYU" || pl[0].Artists[0] != "Mimi Lofi Chill" || pl[0].Duration != 89174*time.Second ||
		!strings.Contains(pl[0].Title, "Chill Vibes Night") {
		t.Errorf("playlist: %+v %v", pl, err)
	}
	// What yt-dlp prints for a playlist that does not exist is turned into the error text.
	s := &Source{Cookies: granted("chrome"), Runner: func(context.Context, string, []string) ([]byte, []byte, error) {
		return nil, read("bad-playlist.err"), errors.New("exit status 1")
	}}
	if _, err := s.Tracks(context.Background(), "PLzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"); err == nil || !strings.Contains(err.Error(), "HTTP Error 400") {
		t.Errorf("bad playlist: %v", err)
	}
}

func TestJSRuntimeIsPassedToEveryRun(t *testing.T) {
	var got []string
	s := &Source{JSRuntime: "node:/opt/node", Runner: func(_ context.Context, _ string, args []string) ([]byte, []byte, error) {
		got = args
		return []byte(`{"entries":[]}`), nil, nil
	}}
	_, _ = s.Search(context.Background(), "x", 3)
	if len(got) < 2 || got[0] != "--js-runtimes" || got[1] != "node:/opt/node" {
		t.Fatalf("args %v", got)
	}
}
