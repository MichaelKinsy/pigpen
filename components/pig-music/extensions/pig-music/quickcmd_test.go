package pig_music_test

import (
	"strings"
	"testing"
)

// The quick commands run without opening the overlay and answer with one line (queue: a short list) in a notification.

func notices(h *overlayHost) int { return len(h.notifications()) }

// say runs `/music <words>` and returns the text of the notification it produced.
func say(t *testing.T, h *overlayHost, words ...string) string {
	t.Helper()
	before := notices(h)
	if failure := <-h.command("music", words...); failure != "" {
		t.Fatalf("/music %s failed: %s", strings.Join(words, " "), failure)
	}
	waitFor(t, "a notification for /music "+strings.Join(words, " "), func() bool { return notices(h) > before })
	return lastNotice(h)
}

func TestQuickCommandsDriveThePlayerAndNeverOpenTheOverlay(t *testing.T) {
	h, srv, _, p := playerEnv(t)
	if out := say(t, h, "play", "night"); !strings.Contains(out, "playing") || !strings.Contains(out, "Song 1") || strings.Contains(out, "\n") {
		t.Fatalf("play: %q", out)
	}
	if out := say(t, h, "pause"); !strings.Contains(out, "paused") {
		t.Errorf("pause: %q", out)
	}
	if out := say(t, h, "resume"); !strings.Contains(out, "playing") {
		t.Errorf("resume: %q", out)
	}
	if out := say(t, h, "toggle"); !strings.Contains(out, "paused") {
		t.Errorf("toggle: %q", out)
	}
	say(t, h, "resume")
	if out := say(t, h, "next"); !strings.Contains(out, "Song 2") {
		t.Errorf("next: %q", out)
	}
	if out := say(t, h, "prev"); !strings.Contains(out, "Song 1") {
		t.Errorf("prev: %q", out)
	}
	if out := say(t, h, "vol", "30"); !strings.Contains(out, "volume 30") {
		t.Errorf("vol: %q", out)
	}
	waitFor(t, "the player at 30", func() bool { return p.State().Volume == 30 })
	if out := say(t, h, "now"); !strings.Contains(out, "playing  Some Artist - Song 1") || strings.Contains(out, "\n") {
		t.Errorf("now: %q", out)
	}
	if out := say(t, h, "shuffle", "on"); !strings.Contains(out, "shuffle") {
		t.Errorf("shuffle: %q", out)
	}
	if out := say(t, h, "repeat", "all"); !strings.Contains(out, "repeat all") {
		t.Errorf("repeat: %q", out)
	}
	_ = srv
	h.mu.Lock()
	opened := len(h.opens)
	h.mu.Unlock()
	if opened != 0 {
		t.Errorf("a quick command opened the overlay %d times", opened)
	}
}

func TestQueueIsAShortListWithTheCurrentTrackMarked(t *testing.T) {
	h, _, _, _ := playerEnv(t)
	say(t, h, "play", "night")
	out := say(t, h, "queue")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 3 || len(lines) > 10 || lines[0] != "pig-music queue:" {
		t.Fatalf("%d lines: %q", len(lines), out)
	}
	if !strings.HasPrefix(lines[1], ">") || !strings.Contains(lines[1], "Song 1") {
		t.Errorf("the current track is not marked first: %q", out)
	}
}

func TestPlayWithANumberUsesTheLastSearchMadeInThePlayerToo(t *testing.T) {
	h, _, _, _ := playerEnv(t)
	done := h.command("music")
	h.waitOpen()
	for _, in := range []string{"/", "n", "i", "g", "h", "t", "\r"} {
		h.input(in)
	}
	h.waitSnapshot("results", hasText("Song 5"))
	h.input("q")
	<-done
	if out := say(t, h, "play", "3"); !strings.Contains(out, "Song 3") {
		t.Errorf("play 3: %q", out)
	}
}

func TestQuickCommandMistakesGetOneShortLineNotTheUsage(t *testing.T) {
	h, _, _, _ := playerEnv(t)
	for _, words := range [][]string{{"vol"}, {"vol", "loud"}, {"shuffle", "maybe"}, {"repeat", "sideways"}} {
		out := say(t, h, words...)
		if strings.Contains(out, "usage:") || strings.Contains(out, "\n") || out == "" {
			t.Errorf("/music %v: %q", words, out)
		}
	}
}

func TestQuickCommandsSayPlainlyWhenNothingIsPlaying(t *testing.T) {
	h, _, _, _ := playerEnv(t)
	// the fake mpv is running but idle: pause has nothing to pause, and the answer is the idle status, not a crash
	out := say(t, h, "now")
	if out == "" || strings.Contains(out, "\n") {
		t.Errorf("%q", out)
	}
}
