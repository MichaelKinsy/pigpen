package pig_music_test

import (
	"strings"
	"testing"
	"time"

	pig_music "github.com/MichaelKinsy/pigpen/pig-music"
)

func TestCommandOpensAFullTerminalOverlayWithoutAHostFrame(t *testing.T) {
	h := startOverlayHost(t, pig_music.Extension(), "tui", 100, 30)
	done := h.command("music", "hello")
	args := h.waitOpen()
	// No legacy modal fields: the host would draw a titled box around them.
	for _, legacy := range []string{"title", "widthFraction", "heightFraction"} {
		if _, ok := args[legacy]; ok {
			t.Errorf("legacy modal field %q set: %v", legacy, args)
		}
	}
	layout, _ := args["overlayOptions"].(map[string]any)
	if args["overlay"] != true || layout["width"] != "100%" || layout["maxHeight"] != "100%" || layout["anchor"] != "top-left" || layout["margin"] != float64(0) {
		t.Fatalf("overlay request = %v", args)
	}
	snap := h.waitSnapshot("first snapshot", func(snapshot) bool { return true })
	if snap.Width != 100 || len(snap.Lines) != 30 {
		t.Fatalf("first snapshot is %d lines for width %d, want 30 lines for 100", len(snap.Lines), snap.Width)
	}
	h.input("q")
	if failure := <-done; failure != "" {
		t.Fatalf("/music failed: %s", failure)
	}
}

func TestEveryKeyIsEchoedAndQClosesTheOverlayAndReportsCounts(t *testing.T) {
	h := startOverlayHost(t, pig_music.Extension(), "tui", 80, 24)
	done := h.command("music", "hello")
	h.waitOpen()
	for _, in := range []string{"a", "\x1b[A", "\x1b[1;5C", "\x1b[97;5u", " "} {
		h.input(in)
	}
	snap := h.waitSnapshot("echo of five keys", func(s snapshot) bool {
		return strings.Contains(strings.Join(s.Lines, "\n"), "keys received 5")
	})
	text := strings.Join(snap.Lines, "\n")
	for _, want := range []string{"a  ", "up", "ctrl+right", "ctrl+a", "space"} {
		if !strings.Contains(text, want) {
			t.Errorf("screen does not echo %q:\n%s", want, text)
		}
	}
	select {
	case <-done:
		t.Fatal("the command returned before q")
	default:
	}
	h.input("q")
	if failure := <-done; failure != "" {
		t.Fatal(failure)
	}
	notes := h.notifications()
	if len(notes) != 1 || notes[0]["message"] != "pig-music hello closed: 6 keys, 0 timer redraws" || notes[0]["level"] != "info" {
		t.Fatalf("notifications = %v", notes)
	}
}

func TestEscapeAlsoCloses(t *testing.T) {
	h := startOverlayHost(t, pig_music.Extension(), "tui", 80, 24)
	done := h.command("music", "hello")
	h.waitOpen()
	h.input("\x1b")
	if failure := <-done; failure != "" {
		t.Fatal(failure)
	}
}

func TestResizeRendersAgainAtTheNewWidthAndHeight(t *testing.T) {
	h := startOverlayHost(t, pig_music.Extension(), "tui", 80, 24)
	done := h.command("music", "hello")
	h.waitOpen()
	h.waitSnapshot("80x24", func(s snapshot) bool { return s.Width == 80 && len(s.Lines) == 24 })
	h.resize(120, 0)
	wide := h.waitSnapshot("width 120", func(s snapshot) bool { return s.Width == 120 })
	if len(wide.Lines) != 24 || !strings.Contains(wide.Lines[1], "120 columns x 24 rows") {
		t.Fatalf("width change: %d lines, second line %q", len(wide.Lines), wide.Lines[1])
	}
	// The host reports a new height to the extension process only; the screen
	// notices it on its own and redraws without any input.
	h.resize(0, 40)
	tall := h.waitSnapshot("height 40", func(s snapshot) bool { return len(s.Lines) == 40 })
	if !strings.Contains(tall.Lines[1], "120 columns x 40 rows") {
		t.Fatalf("height change: second line %q", tall.Lines[1])
	}
	for _, s := range h.allSnapshots() {
		for i, line := range s.Lines {
			if len(line) > s.Width {
				t.Fatalf("snapshot seq %d line %d is %d cells for width %d: %q", s.Seq, i, len(line), s.Width, line)
			}
		}
	}
	h.input("q")
	<-done
}

func TestTheTimerRedrawsOnceASecondWithoutInput(t *testing.T) {
	h := startOverlayHost(t, pig_music.Extension(), "tui", 80, 24)
	done := h.command("music", "hello")
	h.waitOpen()
	start := time.Now()
	h.waitSnapshot("first timer redraw", func(s snapshot) bool { return strings.Contains(strings.Join(s.Lines, "\n"), "ticks 1 ") })
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond || elapsed > 2500*time.Millisecond {
		t.Errorf("first redraw after %s, want about one second", elapsed)
	}
	h.waitSnapshot("second timer redraw", func(s snapshot) bool { return strings.Contains(strings.Join(s.Lines, "\n"), "ticks 2 ") })
	h.input("q")
	<-done
}

func TestOpeningAgainShowsTheStateTheScreenWasLeftIn(t *testing.T) {
	h := startOverlayHost(t, pig_music.Extension(), "tui", 80, 24)
	done := h.command("music", "hello")
	h.waitOpen()
	h.input("z")
	h.waitSnapshot("echo", func(s snapshot) bool { return strings.Contains(strings.Join(s.Lines, "\n"), "keys received 1") })
	h.input("q")
	<-done
	before := len(h.allSnapshots())

	done = h.command("music", "hello")
	snap := h.waitSnapshot("reopened screen", func(s snapshot) bool {
		return len(h.snapshots) > before && strings.Contains(strings.Join(s.Lines, "\n"), "keys received 2")
	})
	if !strings.Contains(strings.Join(snap.Lines, "\n"), `z              "z"`) {
		t.Errorf("reopened screen lost the earlier key:\n%s", strings.Join(snap.Lines, "\n"))
	}
	h.input("q")
	<-done
}

func TestWithoutAnInteractiveTerminalTheCommandOnlySaysSo(t *testing.T) {
	for _, mode := range []string{"print", "json", "rpc"} {
		h := startOverlayHost(t, pig_music.Extension(), mode, 80, 24)
		if failure := <-h.command("music", "hello"); failure != "" {
			t.Fatalf("%s: %s", mode, failure)
		}
		notes := h.notifications()
		h.mu.Lock()
		opened := len(h.opens)
		h.mu.Unlock()
		if opened != 0 {
			t.Errorf("%s: opened a component", mode)
		}
		if len(notes) != 1 || notes[0]["level"] != "warning" {
			t.Errorf("%s: notifications = %v", mode, notes)
		}
	}
}
