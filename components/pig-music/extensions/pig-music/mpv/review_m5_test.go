package mpv

import (
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// mpv reads the user's mpv.conf (and its auto profiles, script-opts and scripts) unless told not to. A line such as
// ytdl-raw-options=cookies-from-browser=firefox there, a common mpv set-up, makes the yt-dlp hook read the browser's
// cookies for every track, and the appended ignore-config= does not remove it (yt-dlp's --ignore-config only skips
// yt-dlp's own config files). The owner's rule is that playback and mpv's hook never carry cookies, so pig-music's mpv
// loads no user configuration at all.
func TestMpvLoadsNoUserConfigurationSoItsYtdlHookCarriesNoCookies(t *testing.T) {
	p := New(Config{Paths: music.PathsIn(t.TempDir()), ExtraArgs: []string{"--ao=null"}})
	for _, a := range p.mpvArgs() {
		if a == "--no-config" || a == "--config=no" {
			return
		}
	}
	t.Fatalf("mpv is started with the user's mpv.conf: %v", p.mpvArgs())
}
