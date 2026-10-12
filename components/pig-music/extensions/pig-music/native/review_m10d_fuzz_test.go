package native

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// FuzzParsePageAndWatch: whatever a browse or watch-next answer holds, reading it does not panic, and every page entity it
// offers is one OpenPage accepts or refuses by its ID (never an empty kind).
func FuzzParsePageAndWatch(f *testing.F) {
	for _, name := range []string{"browse-album.json", "browse-artist.json", "browse-related.json", "browse-playlist.json", "next-radio.json", "next-radio-song.json", "next-song.json"} {
		if b, err := os.ReadFile("testdata/" + name); err == nil {
			f.Add(b)
		}
	}
	f.Add([]byte(`{"contents":{"musicShelfRenderer":{"contents":[{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"x"}]},"navigationEndpoint":{"browseEndpoint":{"browseId":"UC"}}}}]}}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, kind := range []string{"album", "artist", "related", "playlist"} {
			p, err := ParsePage(data, music.Entity{Kind: kind, ID: "MPREb_xxxxxxxx", Title: "t"})
			if err != nil {
				continue
			}
			for _, s := range p.Sections {
				for _, e := range s.Entities {
					if e.Kind == "" || e.ID == "" {
						t.Fatalf("entity %+v", e)
					}
				}
			}
		}
		_, _ = ParseWatch(data)
	})
}
