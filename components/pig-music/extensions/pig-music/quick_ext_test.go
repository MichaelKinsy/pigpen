package pig_music_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// /music settings: the status item cannot take a key, so a small overlay gives the basic settings.

func settingsFile(env map[string]string) string {
	return filepath.Join(env["HOME"], ".pig", "agent", "pig-music", "settings.json")
}

func TestMusicSettingsOpensASmallCenteredOverlayNotTheFullPlayer(t *testing.T) {
	h, _, _, _ := playerEnv(t)
	done := h.command("music", "settings")
	args := h.waitOpen()
	layout, _ := args["overlayOptions"].(map[string]any)
	if args["overlay"] != true || layout["anchor"] != "center" || layout["width"] == "100%" {
		t.Errorf("a small centred overlay was expected: %+v", args)
	}
	h.waitSnapshot("the quick settings", hasText("quick settings"))
	snap := h.waitSnapshot("every row", hasText("Library browser"))
	for _, want := range []string{"Volume", "Shuffle", "Repeat", "Engine", "Now playing line", "Cover art"} {
		if !strings.Contains(screenText(snap), want) {
			t.Errorf("lacks %q:\n%s", want, screenText(snap))
		}
	}
	h.input("q")
	if failure := <-done; failure != "" {
		t.Errorf("closing failed: %s", failure)
	}
}

func TestQuickSettingsChangeTheVolumeOfTheRunningPlayer(t *testing.T) {
	h, _, _, p := playerEnv(t)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	before := p.State().Volume
	done = h.command("music", "settings")
	h.waitOpenN(2)
	h.waitSnapshot("the quick settings", hasText("quick settings"))
	h.input("\x1b[D") // left: volume down by five
	waitFor(t, "the volume five lower", func() bool { return p.State().Volume == before-5 })
	h.input("q")
	<-done
}

func TestQuickSettingsSaveTheEngineWithANoteAndKeepTheOtherKeys(t *testing.T) {
	h, _, env, _ := playerEnv(t)
	writeSettings(t, env, `{"future": 1}`)
	done := h.command("music", "settings")
	h.waitOpen()
	h.waitSnapshot("the quick settings", hasText("quick settings"))
	for i := 0; i < 3; i++ {
		h.input("j") // down to Engine
	}
	h.input("l") // auto -> mpv
	h.waitSnapshot("the note", hasText("pigmusic program"))
	data, err := os.ReadFile(settingsFile(env))
	if err != nil || !strings.Contains(string(data), `"engine": "mpv"`) || !strings.Contains(string(data), `"future"`) {
		t.Errorf("%s %v", data, err)
	}
	h.input("q")
	<-done
}

func TestQuickSettingsSwitchTheNowPlayingLineToTheWidgetAtOnce(t *testing.T) {
	h, _, env, _ := playerEnv(t)
	h.emit("session_start", nil)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	waitFor(t, "the footer first", statusHas(h, "Song 1"))
	done = h.command("music", "settings")
	h.waitOpenN(2)
	h.waitSnapshot("the quick settings", hasText("quick settings"))
	for i := 0; i < 5; i++ {
		h.input("j") // down to Now playing line
	}
	h.input("l") // auto -> footer
	h.input("l") // footer -> widget
	waitFor(t, "the widget with the track", widgetHas(h, "Song 1"))
	waitFor(t, "the footer status cleared", func() bool { s, _ := h.lastStatus(); return s == "" })
	h.input("q")
	<-done
	if data, _ := os.ReadFile(settingsFile(env)); !strings.Contains(string(data), `"nowPlaying": "widget"`) {
		t.Errorf("not saved: %s", data)
	}
}

func TestSwitchingThePaletteOffInTheBoxReachesTheRunningPlayer(t *testing.T) {
	h, _, env, _ := playerEnvWith(t, map[string]string{"COLORTERM": "truecolor"})
	done := playFirstResult(t, h)
	if snap := h.waitSnapshot("painted", hasText("Up Next")); !strings.Contains(strings.Join(snap.Lines, "\n"), "48;2;") {
		t.Fatalf("the player should start painted")
	}
	h.input("q")
	<-done
	done = h.command("music", "settings")
	h.waitOpenN(2)
	h.waitSnapshot("the quick settings", hasText("quick settings"))
	for i := 0; i < 7; i++ {
		h.input("j") // down to Palette
	}
	h.input(" ")
	waitFor(t, "palette:false saved", func() bool {
		data, _ := os.ReadFile(settingsFile(env))
		return strings.Contains(string(data), `"palette": false`)
	})
	snap := h.waitSnapshot("palette off in the box", hasText("Palette"))
	if strings.Contains(screenText(snap), "takes effect after") {
		t.Errorf("the palette applies at once:\n%s", screenText(snap))
	}
	h.input("q")
	<-done
	done = h.command("music")
	h.waitOpenN(3)
	snap = h.waitSnapshot("the player again", hasText("Up Next"))
	if strings.Contains(strings.Join(snap.Lines, "\n"), "48;2;") {
		t.Errorf("the running player is still painted after the palette was switched off")
	}
	h.input("q")
	<-done
}

// TestTheSettingsBoxFollowsThePlayerWhileOpen: what the box shows of the player (its clock, the volume, shuffle) changes with no
// key, from the player's own state changes, and an open box over a player where nothing changes is not drawn again.
func TestTheSettingsBoxFollowsThePlayerWhileOpen(t *testing.T) {
	h, srv, _, p := playerEnv(t)
	done := playFirstResult(t, h)
	h.input("q")
	<-done
	ctx := context.Background()
	// the fake mpv's clock moves only when told (Advance): nothing changes but what the test changes
	done = h.command("music", "settings")
	h.waitOpenN(2)
	h.waitSnapshot("the quick settings", hasText("quick settings"))
	srv.Advance(65)
	h.waitSnapshot("the clock at 1:05", hasText("1:05/"))
	if err := p.SetVolume(ctx, 35); err != nil {
		t.Fatal(err)
	}
	h.waitSnapshot("the volume at 35", hasText("< 35 >"))
	if err := p.SetShuffle(ctx, true); err != nil {
		t.Fatal(err)
	}
	h.waitSnapshot("shuffle on", func(s snapshot) bool {
		for _, l := range s.Lines {
			if strings.Contains(l, "Shuffle") && strings.HasSuffix(strings.TrimSpace(ansi.Strip(l)), "on") {
				return true
			}
		}
		return false
	})
	n := h.snapshotCount()
	time.Sleep(400 * time.Millisecond)
	if got := h.snapshotCount(); got != n {
		t.Errorf("the box was drawn %d more times with nothing changing", got-n)
	}
	h.input("q")
	if failure := <-done; failure != "" {
		t.Errorf("closing failed: %s", failure)
	}
	srv.Advance(10) // the box is gone: nothing is handed to it
	if err := p.SetVolume(ctx, 40); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the volume at 40", func() bool { return p.State().Volume == 40 })
}
