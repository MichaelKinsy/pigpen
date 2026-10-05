package mpv

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// mpv's yt-dlp hook runs yt-dlp for every track. Without --ignore-config it reads
// the user's yt-dlp config, and a --cookies-from-browser there makes playback read
// browser cookies, which pig-music must not do before the owner's consent
// (milestone 5); it also lets that config pick formats or runtimes the doctor did
// not check. The Source and the doctor ignore that config; so does playback.
func TestMpvsYtdlHookIgnoresTheUsersYtdlpConfig(t *testing.T) {
	p := New(Config{Paths: music.PathsIn(t.TempDir()), ExtraArgs: []string{"--ytdl-raw-options-append=js-runtimes=node", "--ao=null"}})
	args := p.mpvArgs()
	at := -1
	for i, a := range args {
		if a == "--ytdl-raw-options-append=ignore-config=" {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("mpv is started without --ytdl-raw-options-append=ignore-config=: %v", args)
	}
	for _, a := range args[at+1:] {
		if strings.HasPrefix(a, "--ytdl-raw-options=") {
			t.Errorf("%s after it replaces the whole list and drops ignore-config", a)
		}
	}
}
