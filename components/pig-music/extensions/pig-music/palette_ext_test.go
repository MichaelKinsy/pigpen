package pig_music_test

import (
	"strings"
	"testing"
	"time"
)

// The palette reaches the real overlay: colour from the environment (COLORTERM), the switch in settings.json, NO_COLOR.

func openedLines(t *testing.T, env map[string]string, settings string) string {
	t.Helper()
	h, _, e, _ := playerEnvWith(t, env)
	if settings != "" {
		writeSettings(t, e, settings)
	}
	done := h.command("music")
	h.waitOpen()
	snap := h.waitSnapshot("the player", hasText("Press / to search"))
	h.input("q")
	<-done
	return strings.Join(snap.Lines, "\n")
}

func TestThePlayerIsPaintedWhenTheTerminalHasColour(t *testing.T) {
	out := openedLines(t, map[string]string{"COLORTERM": "truecolor"}, "")
	if !strings.Contains(out, "48;2;") {
		t.Errorf("no gradient background:\n%q", firstLine(out))
	}
}

func TestThePaletteSettingAndNoColourTurnItOff(t *testing.T) {
	for name, c := range map[string]struct {
		env      map[string]string
		settings string
	}{
		"palette false": {map[string]string{"COLORTERM": "truecolor"}, `{"palette": false}`},
		"NO_COLOR":      {map[string]string{"COLORTERM": "truecolor", "NO_COLOR": "1"}, ""},
		"no colour":     {map[string]string{}, ""},
	} {
		out := openedLines(t, c.env, c.settings)
		if strings.Contains(out, "48;2;") || strings.Contains(out, "48;5;") {
			t.Errorf("%s: a background was painted: %q", name, firstLine(out))
		}
	}
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

func hasCommand(srv interface{ Commands() []string }, part string) bool {
	for _, c := range srv.Commands() {
		if strings.Contains(c, part) {
			return true
		}
	}
	return false
}

// The pulse reaches the real player: the mpv filter that measures is added while the music plays in a shown overlay, and
// removed when the overlay is hidden.
func TestMeasuringStartsWhilePlayingInAShownPlayerAndStopsWhenItIsHidden(t *testing.T) {
	h, srv, _, _ := playerEnvWith(t, map[string]string{"COLORTERM": "truecolor"})
	done := playFirstResult(t, h)
	waitFor(t, "the astats filter added", func() bool { return hasCommand(srv, "astats") })
	h.input("q")
	<-done
	waitFor(t, "the filter removed on hide", func() bool { return len(srv.Filters()) == 0 }) // not just any remove: one is sent at start
}

func TestNothingIsMeasuredWithCalmThePulseSwitchOrNoColour(t *testing.T) {
	for name, c := range map[string]struct {
		env      map[string]string
		settings string
	}{
		"calm":      {map[string]string{"COLORTERM": "truecolor"}, `{"calm": true}`},
		"pulse off": {map[string]string{"COLORTERM": "truecolor"}, `{"pulse": false}`},
		"no colour": {map[string]string{}, ""},
		"NO_COLOR":  {map[string]string{"COLORTERM": "truecolor", "NO_COLOR": "1"}, ""},
	} {
		h, srv, env, _ := playerEnvWith(t, c.env)
		if c.settings != "" {
			writeSettings(t, env, c.settings)
		}
		done := playFirstResult(t, h)
		time.Sleep(300 * time.Millisecond)
		h.input("q")
		<-done
		if hasCommand(srv, "astats") {
			t.Errorf("%s: the measuring filter was added", name)
		}
	}
}
