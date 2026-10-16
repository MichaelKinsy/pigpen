package native

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func section(p music.Page, title string) *music.Section {
	for i := range p.Sections {
		if p.Sections[i].Title == title {
			return &p.Sections[i]
		}
	}
	return nil
}

func TestAnArtistPageHasItsSectionsInOrder(t *testing.T) {
	p, err := ParsePage(fixture(t, "browse-artist.json"), music.Entity{Kind: "artist", ID: "UCRr1xG_2WIDs18a6cIiCxeA"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Daft Punk" || p.Kind != "artist" {
		t.Errorf("%q %q", p.Title, p.Kind)
	}
	var titles []string
	for _, s := range p.Sections {
		titles = append(titles, s.Title)
	}
	if got := strings.Join(titles, "|"); !strings.HasPrefix(got, "Top songs|Albums|Singles & EPs|Videos") {
		t.Errorf("sections: %s", got)
	}
	top := section(p, "Top songs")
	if top == nil || len(top.Tracks) < 2 || top.Tracks[0].ID == "" || top.Tracks[0].Title == "" {
		t.Fatalf("%+v", top)
	}
	alb := section(p, "Albums")
	if alb == nil || len(alb.Entities) < 2 || alb.Entities[0].Kind != "album" || !strings.HasPrefix(alb.Entities[0].ID, "MPREb_") || alb.Entities[0].Title == "" {
		t.Fatalf("%+v", alb)
	}
	vid := section(p, "Videos")
	if vid == nil || len(vid.Tracks) < 2 || vid.Tracks[0].ID == "" || vid.Tracks[0].Title == "" {
		t.Fatalf("videos are tracks to play: %+v", vid)
	}
	fans := section(p, "Fans might also like")
	if fans == nil || len(fans.Entities) < 2 || fans.Entities[0].Kind != "artist" || !strings.HasPrefix(fans.Entities[0].ID, "UC") {
		t.Fatalf("related artists: %+v", fans)
	}
	if feat := section(p, "Featured on"); feat != nil && feat.Entities[0].Kind != "playlist" {
		t.Errorf("a playlist tile: %+v", feat.Entities[0])
	}
}

func TestAnAlbumPageIsItsTracksInOrderWithTheAlbumNamed(t *testing.T) {
	p, err := ParsePage(fixture(t, "browse-album.json"), music.Entity{Kind: "album", ID: "MPREb_x1234567"})
	if err != nil || len(p.Sections) == 0 {
		t.Fatalf("%v %+v", err, p)
	}
	tr := p.Sections[0].Tracks
	if len(tr) < 2 || tr[0].Album == "" || tr[0].Album != p.Title || len(tr[0].Artists) == 0 {
		t.Errorf("%q %+v", p.Title, tr)
	}
}

func TestTheRelatedPageHasItsShelves(t *testing.T) {
	p, err := ParsePage(fixture(t, "browse-related.json"), music.Entity{Kind: "related", ID: "MPTRt_abcdefgh"})
	if err != nil {
		t.Fatal(err)
	}
	if s := section(p, "You might also like"); s == nil || len(s.Tracks) < 2 {
		t.Errorf("songs: %+v", s)
	}
	if s := section(p, "Similar artists"); s == nil || len(s.Entities) < 2 || s.Entities[0].Kind != "artist" {
		t.Errorf("artists: %+v", s)
	}
	if s := section(p, "Recommended playlists"); s == nil || len(s.Entities) < 2 || s.Entities[0].Kind != "playlist" || !strings.HasPrefix(s.Entities[0].ID, "VL") {
		t.Errorf("playlists: %+v", s)
	}
}

func TestAnUnknownPageShapeIsAnErrorNotAnEmptyPage(t *testing.T) {
	if _, err := ParsePage([]byte(`{"contents":{"somethingNew":{}}}`), music.Entity{Kind: "artist", ID: "UCabcdefgh"}); err == nil {
		t.Error("no error")
	}
	if _, err := ParsePage([]byte(`not json`), music.Entity{Kind: "artist"}); err == nil {
		t.Error("no error")
	}
}

func TestWatchNamesTheArtistsTheAlbumTheRadioAndTheRelatedPage(t *testing.T) {
	w, err := ParseWatch(fixture(t, "next-radio-song.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Artists) < 2 || w.Artists[0].Kind != "artist" || w.Artists[0].ID != "UCRr1xG_2WIDs18a6cIiCxeA" || w.Artists[0].Title != "Daft Punk" {
		t.Errorf("artists: %+v", w.Artists)
	}
	if w.Album.Kind != "album" || w.Album.ID != "MPREb_K8qWMWVqXGi" { // the seed's album is in its menu, not its line
		t.Errorf("album: %+v", w.Album)
	}
	if len(w.Radio) != 3 || w.Radio[0].ID != "4D7u5KF7SP8" || w.Radio[1].Album != "One More Time" || w.Radio[1].Duration == 0 || w.Radio[1].Artists[0] != "Daft Punk" {
		t.Errorf("radio: %+v", w.Radio)
	}
	if !strings.HasPrefix(w.RelatedID, "MPTRt_") {
		t.Errorf("related: %q", w.RelatedID)
	}
}

func TestWatchOfAMusicVideoHasArtistsAndNoAlbum(t *testing.T) {
	w, err := ParseWatch(fixture(t, "next-radio.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Artists) != 2 || w.Artists[1].Title != "Anitta" || w.Album.ID != "" || len(w.Radio) != 5 {
		t.Errorf("%+v", w)
	}
}

func TestWatchOfAnUnknownShapeIsAnError(t *testing.T) {
	if _, err := ParseWatch([]byte(`{"contents":{}}`)); err == nil {
		t.Error("no error")
	}
}

func navServer(t *testing.T) (*Source, *[]string) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, r.URL.Path+" "+string(b))
		if r.Header.Get("Cookie")+r.Header.Get("Authorization") != "" {
			t.Error("a credential was sent")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/next"):
			_, _ = w.Write(fixture(t, "next-radio-song.json"))
		case strings.Contains(string(b), "MPTRt_"):
			_, _ = w.Write(fixture(t, "browse-related.json"))
		case strings.Contains(string(b), "MPREb_"):
			_, _ = w.Write(fixture(t, "browse-album.json"))
		default:
			_, _ = w.Write(fixture(t, "browse-artist.json"))
		}
	}))
	t.Cleanup(srv.Close)
	return &Source{NextURL: srv.URL + "/next", BrowseURL: srv.URL + "/browse"}, &bodies
}

func TestWatchAsksForTheRDAMVMMixOfTheVideo(t *testing.T) {
	s, bodies := navServer(t)
	if _, err := s.Watch(context.Background(), "4D7u5KF7SP8"); err != nil {
		t.Fatal(err)
	}
	if len(*bodies) != 1 || !strings.Contains((*bodies)[0], `"playlistId":"RDAMVM4D7u5KF7SP8"`) || !strings.Contains((*bodies)[0], `"videoId":"4D7u5KF7SP8"`) {
		t.Errorf("%v", *bodies)
	}
	if _, err := s.Watch(context.Background(), "../x"); err == nil {
		t.Error("a bad id was sent")
	}
}

func TestOpenPageBrowsesTheIDOfArtistsAlbumsPlaylistsAndRelated(t *testing.T) {
	s, bodies := navServer(t)
	for _, e := range []music.Entity{
		{Kind: "artist", ID: "UCRr1xG_2WIDs18a6cIiCxeA"}, {Kind: "album", ID: "MPREb_K8qWMWVqXGi"},
		{Kind: "related", ID: "MPTRt_X8pk5s2tHrH"}, {Kind: "playlist", ID: "VLPLabcdefghijk"},
	} {
		p, err := s.OpenPage(context.Background(), e)
		if err != nil || p.Kind != e.Kind || p.ID != e.ID {
			t.Errorf("%+v: %v %+v", e, err, p)
		}
	}
	if len(*bodies) != 4 || !strings.Contains((*bodies)[3], `"browseId":"VLPLabcdefghijk"`) {
		t.Errorf("%v", *bodies)
	}
	for _, bad := range []music.Entity{{Kind: "artist", ID: "x/../y"}, {Kind: "album", ID: ""}, {Kind: "radio", ID: "UCRr1xG_2WIDs18a6cIiCxeA"}} {
		if _, err := s.OpenPage(context.Background(), bad); err == nil {
			t.Errorf("%+v was sent", bad)
		}
	}
}
