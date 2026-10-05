package pig_music_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The owner runs context-info, which installs its own footer (ctx.SetFooter) and so hides every extension status. With
// nowPlaying set to "widget" the same line shows as a one-line widget above the editor instead.

func writeSettings(t *testing.T, env map[string]string, body string) {
	t.Helper()
	p := filepath.Join(env["HOME"], ".pig", "agent", "pig-music", "settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func widgetHas(h *overlayHost, want string) func() bool {
	return func() bool {
		ev, ok := h.lastWidget("pig-music")
		return ok && len(ev.lines) == 1 && strings.Contains(ev.lines[0], want)
	}
}

func widgetCleared(h *overlayHost) func() bool {
	return func() bool { ev, ok := h.lastWidget("pig-music"); return ok && ev.lines == nil }
}

func TestTheNowPlayingLineIsAWidgetWhenSettingsSayWidgetAndNeverAFooterStatus(t *testing.T) {
	h, srv, env, _ := playerEnv(t)
	writeSettings(t, env, `{"nowPlaying": "widget"}`)
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	if ev, ok := h.lastWidget("pig-music"); ok && ev.lines != nil {
		t.Fatalf("a widget %q while the screen is open", ev.lines)
	}
	h.input("q")
	<-done
	waitFor(t, "the widget with the spinner and the track", widgetHas(h, " Song 1 - Some Artist"))
	srv.Advance(30)
	waitFor(t, "the position in the widget", widgetHas(h, "0:30/3:20"))
	if ev, _ := h.lastWidget("pig-music"); len(ev.lines) != 1 || strings.ContainsAny(ev.lines[0], "\x1b\x07\n") {
		t.Errorf("the widget must be one clean line: %q", ev.lines)
	}
	for _, s := range h.allStatuses() {
		if s != "" {
			t.Errorf("a footer status %q although nowPlaying is widget", s)
		}
	}
	// opening the screen clears it, hiding it brings it back
	done = h.command("music")
	h.waitOpenN(2)
	waitFor(t, "the widget cleared as the screen opens", widgetCleared(h))
	h.input("q")
	<-done
	waitFor(t, "the widget back after hiding", widgetHas(h, "Song 1"))
}

func TestStoppingClearsTheWidget(t *testing.T) {
	h, _, env, _ := playerEnv(t)
	writeSettings(t, env, `{"nowPlaying": "widget"}`)
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	waitFor(t, "the widget", widgetHas(h, "Song 1"))
	if failure := <-h.command("music", "stop"); failure != "" {
		t.Fatal(failure)
	}
	waitFor(t, "the widget cleared on stop", widgetCleared(h))
}

func TestPausedShowsTheStillMarkInTheWidget(t *testing.T) {
	h, _, env, _ := playerEnv(t)
	writeSettings(t, env, `{"nowPlaying": "widget"}`)
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	h.input(" ")
	h.waitSnapshot("paused", hasText("Paused"))
	h.input("q")
	<-done
	waitFor(t, "paused in the widget", widgetHas(h, "|| Song 1"))
}

func TestTheFooterStaysTheDefaultAndAutoMeansTheFooter(t *testing.T) {
	for _, body := range []string{``, `{"nowPlaying": "auto"}`, `{"nowPlaying": "footer"}`} {
		h, _, env, _ := playerEnv(t)
		if body != "" {
			writeSettings(t, env, body)
		}
		h.emit("session_start", nil)
		done := playFirstResult(t, h)
		h.input("q")
		<-done
		waitFor(t, "the footer", statusHas(h, "Song 1"))
		time.Sleep(100 * time.Millisecond)
		if n := h.widgetCount(); n != 0 {
			t.Errorf("%q: %d widget writes with the footer in use", body, n)
		}
	}
}

func TestFooterStatusFalseSilencesTheWidgetToo(t *testing.T) {
	h, _, env, _ := playerEnv(t)
	writeSettings(t, env, `{"nowPlaying": "widget", "footerStatus": false}`)
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	time.Sleep(300 * time.Millisecond)
	if ev, ok := h.lastWidget("pig-music"); ok && ev.lines != nil {
		t.Errorf("a widget %q although footerStatus is false", ev.lines)
	}
}
