package pig_music

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// The native engine's Source lists a public playlist through the pigmusic program (WaxTap is linked there, not here).
func TestNativeEngineSourceListsPlaylistsThroughThePigmusicProgram(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake pigmusic is a shell script")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "pigmusic")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"version\":1,\"entries\":[{\"VideoID\":\"dQw4w9WgXcQ\",\"Title\":\"Never Gonna\",\"Author\":\"Rick\"}]}'\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PIG_MUSIC_SERVE": fake}
	a := newApp(deps{Getenv: func(k string) string { return env[k] }, LookPath: func(n string) (string, error) { return n, nil }})
	src, player := a.nativeEngine(music.Settings{}, music.Paths{})
	if player == nil {
		t.Fatal("no native player")
	}
	tracks, err := src.Tracks(context.Background(), "PLabcdefghijklmnop")
	if err != nil || len(tracks) != 1 || tracks[0].Title != "Never Gonna" {
		t.Fatalf("tracks = %+v, err = %v", tracks, err)
	}
	if _, err := src.Library(context.Background()); err == nil {
		t.Fatal("the native engine has no signed-in library; it must say why")
	}
}
