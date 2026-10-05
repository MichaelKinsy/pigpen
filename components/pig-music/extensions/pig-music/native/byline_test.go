package native

import (
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func plainRuns(parts ...string) []run {
	var out []run
	for i, p := range parts {
		if i > 0 {
			out = append(out, run{Text: " • "})
		}
		out = append(out, run{Text: p})
	}
	return out
}

// Review of M9c (F8): with no run linking to a page, the second segment was taken as the album, which for a video is "1.8B views".
func TestAViewCountOrAYearIsNotTakenForTheAlbum(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parts []string
		album string
	}{
		{"video views", []string{"Some Artist", "1.8B views", "3:32"}, ""},
		{"plays", []string{"Some Artist", "12M plays", "3:32"}, ""},
		{"year", []string{"Some Artist", "2009", "3:32"}, ""},
		{"a real album", []string{"Some Artist", "Whenever You Need Somebody", "3:32"}, "Whenever You Need Somebody"},
		{"labelled song", []string{"Song", "Some Artist", "Some Album", "3:32"}, "Some Album"},
	} {
		var tr music.Track
		details(&tr, plainRuns(tc.parts...))
		if tr.Album != tc.album {
			t.Errorf("%s: album %q, want %q", tc.name, tr.Album, tc.album)
		}
		if len(tr.Artists) != 1 || tr.Artists[0] != "Some Artist" {
			t.Errorf("%s: artists %v", tc.name, tr.Artists)
		}
		if tr.Duration == 0 {
			t.Errorf("%s: no duration", tc.name)
		}
	}
}
