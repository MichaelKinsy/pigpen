package pig_music_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	h.waitSnapshot("the note", hasText("pigmusic only"))
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
