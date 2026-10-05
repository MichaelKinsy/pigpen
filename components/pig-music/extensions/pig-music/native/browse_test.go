package native

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// browse-playlist.json and browse-playlist-cont.json are real answers of YouTube Music's unauthenticated browse for a public
// playlist (cut to a few rows, tracking data and visitor data removed); the library's liked songs come in the same shape.
func TestAPlaylistPageGivesItsTracksAndTheNextToken(t *testing.T) {
	page, err := ParseBrowse(fixture(t, "browse-playlist.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tracks) != 6 || page.Next == "" {
		t.Fatalf("%d tracks, next %q", len(page.Tracks), page.Next)
	}
	first := page.Tracks[0]
	if first.ID != "fOT0BUpITw8" || first.Title != "BELLAKEO" || len(first.Artists) != 2 || first.Artists[0] != "Peso Pluma" || first.Artists[1] != "Anitta" {
		t.Errorf("first track %+v", first)
	}
	if first.Duration != 3*time.Minute+55*time.Second || first.ArtURL == "" {
		t.Errorf("duration %v, art %q", first.Duration, first.ArtURL)
	}
	for _, tr := range page.Tracks {
		if tr.ID == "" || tr.Title == "" || tr.Duration == 0 || len(tr.Artists) == 0 {
			t.Errorf("incomplete row %+v", tr)
		}
	}
}

func TestAContinuationPageGivesMoreTracksAndNoToken(t *testing.T) {
	page, err := ParseBrowse(fixture(t, "browse-playlist-cont.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tracks) != 4 || page.Next != "" {
		t.Fatalf("%d tracks, next %q", len(page.Tracks), page.Next)
	}
}

// browse-library-playlists.json is written by hand (no signed-in account was available to record it): the grid of the library's
// playlists, with tiles in the shape of the real musicTwoRowItemRenderer of a public answer (a related-playlists shelf,
// recorded 2026-10-05: the browse endpoint on the title run and on the tile, the page type, the thumbnail). The parts that
// matter are the titles, the browse IDs, the counts ("1,234 songs") and the Liked Music entry that is skipped.
func TestTheLibraryGridGivesPlaylistsAndAlbums(t *testing.T) {
	page, err := ParseBrowse(fixture(t, "browse-library-playlists.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range page.Collections {
		got = append(got, c.Kind+":"+c.ID+":"+c.Title)
	}
	want := []string{"playlist:PLroadtrip0000001:Road trip", "playlist:PLfocus00000000002:Focus", "album:MPREb_abcdefghijk:Some Album",
		"playlist:PLeverything00003:Everything", "playlist:PLtitleonly000004:Title link only"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %q, want %q", i, got[i], want[i])
		}
	}
	if page.Next != "GRIDNEXTTOKEN" { // a grid continues with nextContinuationData
		t.Errorf("next %q", page.Next)
	}
	if page.Collections[0].Count != 12 || page.Collections[3].Count != 1234 {
		t.Errorf("counts %d, %d", page.Collections[0].Count, page.Collections[3].Count)
	}
}

func TestAnAnswerInAnUnknownShapeIsAnErrorNotAnEmptyLibrary(t *testing.T) {
	for _, in := range []string{`{}`, `{"contents":{}}`, `not json`, `{"error":{"message":"nope"}}`} {
		if page, err := ParseBrowse([]byte(in)); err == nil {
			t.Errorf("%s: no error, %+v", in, page)
		}
	}
}

// A real playlist page carries two tokens: the playlist shelf's own (its next rows) and the section list's (the "related
// playlists" or "suggestions" shelf that follows the playlist). browse-playlist.json keeps both, as recorded. Only the shelf's
// token continues the playlist; following the other one ends the listing at the first page (or adds rows that are not in it).
// The document is walked in Go's random map order, so this is checked many times.
func TestThePlaylistsOwnTokenIsTakenNeverTheSectionListsToken(t *testing.T) {
	const shelf = "4qmFsgKHARIkVkxQTEZn"
	data := fixture(t, "browse-playlist.json")
	for i := 0; i < 200; i++ {
		page, err := ParseBrowse(data)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(page.Next, shelf) {
			t.Fatalf("parse %d took the token %.24q, not the playlist shelf's", i, page.Next)
		}
	}
}

// A carousel beside the playlist (related playlists, suggested songs) is not part of it.
func TestRowsOfACarouselBesideThePlaylistAreNotItsTracks(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(fixture(t, "browse-playlist.json"), &doc); err != nil {
		t.Fatal(err)
	}
	section := dig(doc, "contents", "twoColumnBrowseResultsRenderer", "secondaryContents", "sectionListRenderer").(map[string]any)
	shelfRows := dig(section["contents"].([]any)[0], "musicPlaylistShelfRenderer", "contents").([]any)
	var row map[string]any // a copy of the first row, with another video
	b, _ := json.Marshal(shelfRows[0])
	_ = json.Unmarshal([]byte(strings.ReplaceAll(string(b), "fOT0BUpITw8", "SUGGESTED01")), &row)
	section["contents"] = append(section["contents"].([]any), map[string]any{"musicCarouselShelfRenderer": map[string]any{
		"contents": []any{row, map[string]any{"musicTwoRowItemRenderer": map[string]any{
			"title":              map[string]any{"runs": []any{map[string]any{"text": "Related"}}},
			"navigationEndpoint": map[string]any{"browseEndpoint": map[string]any{"browseId": "VLPLrelated000000001"}},
		}}},
	}})
	data, _ := json.Marshal(doc)
	page, err := ParseBrowse(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range page.Tracks {
		if tr.ID == "SUGGESTED01" {
			t.Error("a suggested song was taken for a row of the playlist")
		}
	}
	if len(page.Tracks) != 6 || len(page.Collections) != 0 {
		t.Errorf("%d tracks, collections %v", len(page.Tracks), page.Collections)
	}
}

// The album of a library row comes with the row (its third column, linked to the album page): the Library shows it without
// a request per row. Rows without one have none.
func TestARowsAlbumComesFromItsAlbumColumn(t *testing.T) {
	want := map[string]string{"s6cdjJvGM2o": "La Diabla", "Ke9A6XV2H38": "Federal Contraband 2", "_db-mfu8y1k": "THE WORLD EP.FIN : WILL", "fOT0BUpITw8": ""}
	got := map[string]string{}
	for _, f := range []string{"browse-playlist.json", "browse-playlist-cont.json"} {
		page, err := ParseBrowse(fixture(t, f))
		if err != nil {
			t.Fatal(err)
		}
		for _, tr := range page.Tracks {
			got[tr.ID] = tr.Album
		}
	}
	for id, album := range want {
		if got[id] != album {
			t.Errorf("%s: album %q, want %q", id, got[id], album)
		}
	}
}

// browse-signed-out.json is YouTube Music's real answer (HTTP 200) to the library's browse without a session, as cookies
// that expired or were signed out get: a "Sign in" message and no list. It must be an error (the library falls back to
// yt-dlp), never an empty library.
func TestASignedOutAnswerIsAnErrorNotAnEmptyLibrary(t *testing.T) {
	if page, err := ParseBrowse(fixture(t, "browse-signed-out.json")); err == nil {
		t.Errorf("no error: %+v", page)
	}
}
