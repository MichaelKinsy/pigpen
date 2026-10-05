package ytdlp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// rev-pig-music-m9c F1: the library memory answered without asking the cookie access again. Consent is withdrawn by
// deleting cookie-consent.json (README), and the browser can change; before M9c every library call checked both. A
// remembered listing may only be shown while the same browser is still allowed.
func TestTheRememberedLibraryIsNotShownAfterConsentIsWithdrawn(t *testing.T) {
	r := &recorder{out: map[string]string{"feed/playlists": playlistsJSON, "list=LM": likedJSON}}
	need := &music.NeedsConsentError{Browser: "chrome"}
	var withdrawn bool
	cookies := func(context.Context) (string, error) {
		if withdrawn {
			return "", need
		}
		return "chrome", nil
	}
	s := &Source{Runner: r.run, Cookies: cookies}
	ctx := context.Background()
	if _, err := s.Library(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tracks(ctx, "LM"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TracksPage(ctx, "LM", 0, 1); err != nil {
		t.Fatal(err)
	}
	withdrawn = true
	if cols, err := s.Library(ctx); !errors.As(err, &need) {
		t.Errorf("library after withdrawal: %v, %v", cols, err)
	}
	if got, err := s.Tracks(ctx, "LM"); !errors.As(err, &need) {
		t.Errorf("tracks after withdrawal: %v, %v", got, err)
	}
	if got, _, err := s.TracksPage(ctx, "LM", 0, 1); !errors.As(err, &need) {
		t.Errorf("a remembered page after withdrawal: %v, %v", got, err)
	}
	// and allowing again does not bring the old answers back without asking the account
	withdrawn = false
	runs := len(r.calls)
	if _, err := s.Library(ctx); err != nil || len(r.calls) != runs+1 {
		t.Errorf("after consent again: %d runs (was %d), %v", len(r.calls), runs, err)
	}
}

func TestTheRememberedLibraryBelongsToTheBrowserThatAnswered(t *testing.T) {
	r := &recorder{out: map[string]string{"feed/playlists": playlistsJSON, "list=LM": likedJSON}}
	spec := "chrome"
	s := &Source{Runner: r.run, Cookies: func(context.Context) (string, error) { return spec, nil }}
	ctx := context.Background()
	if _, err := s.Library(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tracks(ctx, "LM"); err != nil {
		t.Fatal(err)
	}
	spec = "firefox:work" // another browser (or profile), so maybe another account
	if _, err := s.Library(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tracks(ctx, "LM"); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 4 {
		t.Fatalf("%d runs: the other browser was answered from the first one's memory", len(r.calls))
	}
	for _, i := range []int{2, 3} {
		if !strings.Contains(strings.Join(r.calls[i], " "), "--cookies-from-browser firefox:work") {
			t.Errorf("run %d: %v", i, r.calls[i])
		}
	}
}

// Refresh forgets the opened collections too, not only the list of collections (r means ask the account again).
func TestRefreshForgetsTheTracksOfCollectionsToo(t *testing.T) {
	r := &recorder{out: map[string]string{"list=LM": likedJSON}}
	s := &Source{Runner: r.run, Cookies: granted("chrome")}
	ctx := context.Background()
	if _, err := s.Tracks(ctx, "LM"); err != nil {
		t.Fatal(err)
	}
	s.Refresh()
	if _, err := s.Tracks(ctx, "LM"); err != nil || len(r.calls) != 2 {
		t.Fatalf("after Refresh: %d runs, %v", len(r.calls), err)
	}
}

// A page that starts inside what is known fetches only the rows not known yet, and adds no row twice.
func TestAnOverlappingPageFetchesOnlyWhatIsNotKnown(t *testing.T) {
	r, run := pagedRecorder(10)
	s := &Source{Runner: run, Cookies: granted("chrome")}
	ctx := context.Background()
	if _, _, err := s.TracksPage(ctx, "LM", 0, 4); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.TracksPage(ctx, "LM", 2, 4)
	if err != nil || len(got) != 4 || got[0].ID != id11(3) || got[3].ID != id11(6) {
		t.Fatalf("%v, %v", got, err)
	}
	if last := strings.Join(r.calls[len(r.calls)-1], " "); !strings.Contains(last, "--playlist-items 5-6") {
		t.Errorf("fetched %q", last)
	}
	all, err := s.Tracks(ctx, "LM") // not complete yet: one listing of everything
	if err != nil || len(all) != 10 {
		t.Fatalf("%d, %v", len(all), err)
	}
}

// rev-pig-music-m9c F3: when the direct listing failed and yt-dlp failed too, only yt-dlp's error was shown, so a direct
// listing that YouTube started refusing (a client version it no longer takes, a changed answer) was never named.
func TestWhenBothSearchesFailTheDirectListingsReasonIsShownToo(t *testing.T) {
	var calls []call
	inner := &innerFake{err: errors.New("YouTube Music search answered HTTP 400")}
	s := &Source{Inner: inner, Runner: script(&calls, fail("ERROR: Unable to download API page"), fail("ERROR: Unable to download API page"))}
	_, err := s.Search(context.Background(), "x", 3)
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "Unable to download") {
		t.Fatalf("%v", err)
	}
}

// An answer that was on its way when the memory was forgotten (r pressed, consent withdrawn) is not kept.
func TestAListingThatWasOnItsWayWhenTheMemoryWasForgottenIsNotKept(t *testing.T) {
	var s *Source
	runs := 0
	s = &Source{Cookies: granted("chrome"), Runner: func(_ context.Context, _ string, args []string) ([]byte, []byte, error) {
		runs++
		if runs%2 == 1 {
			s.Refresh()                                                    // forgotten while yt-dlp was still listing (r pressed) ...
			if _, err := s.checkMemory(context.Background()); err != nil { // ... and the listing r started has begun
				t.Fatal(err)
			}
		}
		if strings.Contains(strings.Join(args, " "), "list=LM") {
			return []byte(likedJSON), nil, nil
		}
		return []byte(playlistsJSON), nil, nil
	}}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := s.Library(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := s.Tracks(ctx, "LM"); err != nil {
			t.Fatal(err)
		}
	}
	if runs != 4 {
		t.Errorf("%d runs: an answer from before the memory was forgotten was kept", runs)
	}
}
