package pig_music

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

type oneTrackSource struct{ music.Source }

func (oneTrackSource) Search(context.Context, string, int) ([]music.Track, error) {
	return []music.Track{{ID: "aaaaaaaaaaa", Title: "From the player"}}, nil
}

// rev-pig-music-m7, surviving mutant: the extension tests hand in a Source, which goes through savingSource, so nothing
// tested that the real path (guard, around yt-dlp) keeps the player's last search for `/music play <number>`.
func TestTheRealSourcePathKeepsTheLastSearchForPlayByNumber(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"HOME": dir, "PIG_CODING_AGENT_DIR": dir, "XDG_RUNTIME_DIR": dir}
	a := newApp(deps{Getenv: func(k string) string { return env[k] }})
	if _, err := a.guarded(oneTrackSource{}).Search(context.Background(), "x", 5); err != nil {
		t.Fatal(err)
	}
	paths, _, _ := a.load()
	got, err := music.LoadSearch(paths.Search)
	if err != nil || len(got) != 1 || got[0].Title != "From the player" {
		t.Fatalf("%v %v", got, err)
	}
}
