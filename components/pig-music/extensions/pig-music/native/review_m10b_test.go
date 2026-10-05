package native

import "testing"

// Review of M10b: an artist page's top songs name their album in the fourth column (the third is the play count, which the
// M10b fix stopped taking for the album). The owner asked to see albums in listings, and the recorded page has them.
func TestReviewM10bAnArtistsTopSongsKeepTheirAlbumFromTheFourthColumn(t *testing.T) {
	_, tracks, err := ParseEntityPage(fixture(t, "browse-artist.json"), "artist")
	if err != nil || len(tracks) != 3 {
		t.Fatalf("%v %v", err, tracks)
	}
	want := []string{"Random Access Memories", "Get Lucky (Radio Edit - feat. Pharrell Williams and Nile Rodgers)", "One More Time"}
	for i, tr := range tracks {
		if tr.Album != want[i] {
			t.Errorf("%s: album %q, want %q", tr.Title, tr.Album, want[i])
		}
	}
}
