package native

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// search-explicit.json, search-albums.json, search-artists.json, browse-album.json and browse-artist.json are real WEB_REMIX
// answers (cut to a few rows, tracking data removed).
func TestSongsCarryTheExplicitBadge(t *testing.T) {
	tracks, err := parseSearch(fixture(t, "search-explicit.json"), 10)
	if err != nil || len(tracks) < 3 {
		t.Fatalf("%v %v", tracks, err)
	}
	if tracks[0].Title != "Lose Yourself" || !tracks[0].Explicit {
		t.Errorf("first %+v", tracks[0])
	}
	// the same song without the badge is not marked
	plain, _ := parseSearch(fixture(t, "search-music.json"), 10)
	for _, p := range plain {
		if p.Explicit {
			t.Errorf("%s marked explicit", p.Title)
		}
	}
}

func TestAlbumsFromASearchHaveTheirBrowseIDTitleAndArtist(t *testing.T) {
	got, err := ParseEntities(fixture(t, "search-albums.json"), "album")
	if err != nil || len(got) != 3 {
		t.Fatalf("%v %v", got, err)
	}
	a := got[0]
	if a.Kind != "album" || a.ID != "MPREb_K8qWMWVqXGi" || a.Title != "Random Access Memories" || a.Subtitle != "Daft Punk • 2013" || a.ArtURL == "" {
		t.Errorf("%+v", a)
	}
}

func TestArtistsFromASearchHaveTheirChannelIDAndAudience(t *testing.T) {
	got, err := ParseEntities(fixture(t, "search-artists.json"), "artist")
	if err != nil || len(got) != 3 {
		t.Fatalf("%v %v", got, err)
	}
	a := got[0]
	if a.Kind != "artist" || a.ID != "UCRr1xG_2WIDs18a6cIiCxeA" || a.Title != "Daft Punk" || !strings.Contains(a.Subtitle, "monthly audience") {
		t.Errorf("%+v", a)
	}
}

func TestEntitiesOfAnUnknownShapeIsAnError(t *testing.T) {
	if _, err := ParseEntities([]byte(`{"contents":{}}`), "album"); err == nil {
		t.Error("no error")
	}
}

func TestAnAlbumPageGivesItsTracksNamedForTheAlbum(t *testing.T) {
	title, tracks, err := ParseEntityPage(fixture(t, "browse-album.json"), "album")
	if err != nil || len(tracks) != 4 {
		t.Fatalf("%v %d %v", err, len(tracks), tracks)
	}
	if title != "Random Access Memories" {
		t.Errorf("title %q", title)
	}
	for _, tr := range tracks {
		if tr.Album != "Random Access Memories" || tr.ID == "" || tr.Duration == 0 {
			t.Errorf("%+v", tr)
		}
	}
	if tracks[0].Title != "Give Life Back to Music" || tracks[0].Artists[0] != "Daft Punk" {
		t.Errorf("first %+v", tracks[0])
	}
}

func TestAnArtistPageGivesItsTopSongs(t *testing.T) {
	title, tracks, err := ParseEntityPage(fixture(t, "browse-artist.json"), "artist")
	if err != nil || len(tracks) == 0 {
		t.Fatalf("%v %v", err, tracks)
	}
	if title != "Daft Punk" {
		t.Errorf("title %q", title)
	}
}

type entityServer struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []string
}

func newEntityServer(t *testing.T) *entityServer {
	es := &entityServer{}
	es.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		es.mu.Lock()
		es.bodies = append(es.bodies, r.URL.Path+" "+string(b))
		es.mu.Unlock()
		if got := r.Header.Get("Cookie") + r.Header.Get("Authorization"); got != "" {
			t.Errorf("a credential was sent: %q", got)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/search") && strings.Contains(string(b), "EgWKAQIYAW"):
			_, _ = w.Write(fixture(t, "search-albums.json"))
		case strings.HasSuffix(r.URL.Path, "/search"):
			_, _ = w.Write(fixture(t, "search-artists.json"))
		case strings.Contains(string(b), "MPREb_"):
			_, _ = w.Write(fixture(t, "browse-album.json"))
		default:
			_, _ = w.Write(fixture(t, "browse-artist.json"))
		}
	}))
	t.Cleanup(es.Close)
	return es
}

func TestSearchEntitiesAsksForAlbumsAndArtistsWithoutCredentials(t *testing.T) {
	es := newEntityServer(t)
	s := &Source{SearchURL: es.URL + "/search"}
	got, err := s.SearchEntities(context.Background(), "daft punk", 5)
	if err != nil {
		t.Fatal(err)
	}
	var albums, artists int
	for _, e := range got {
		switch e.Kind {
		case "album":
			albums++
		case "artist":
			artists++
		}
	}
	if albums != 3 || artists != 3 || len(es.bodies) != 2 {
		t.Errorf("%d albums %d artists, %d requests", albums, artists, len(es.bodies))
	}
}

func TestOpenEntityBrowsesByItsIDAndNamesTheTracksForTheAlbum(t *testing.T) {
	es := newEntityServer(t)
	s := &Source{BrowseURL: es.URL + "/browse"}
	title, tracks, err := s.OpenEntity(context.Background(), music.Entity{Kind: "album", ID: "MPREb_K8qWMWVqXGi", Title: "x"})
	if err != nil || title != "Random Access Memories" || len(tracks) != 4 {
		t.Fatalf("%q %d %v", title, len(tracks), err)
	}
	if !strings.Contains(es.bodies[0], `"browseId":"MPREb_K8qWMWVqXGi"`) {
		t.Errorf("asked %s", es.bodies[0])
	}
	if _, _, err := s.OpenEntity(context.Background(), music.Entity{Kind: "artist", ID: "../x"}); err == nil {
		t.Error("a bad id was sent")
	}
}

func TestAnArtistPagesPlayCountIsNotTakenForAnAlbum(t *testing.T) {
	_, tracks, _ := ParseEntityPage(fixture(t, "browse-artist.json"), "artist")
	for _, tr := range tracks {
		if strings.Contains(tr.Album, "plays") {
			t.Errorf("%q is the album of %s", tr.Album, tr.Title)
		}
	}
}

// An album's rows leave out the artist when it is the album's own: the header names it. (Hand-written: the recorded rows all
// name their artist, a live album with a one-artist row had an empty line.)
func TestAnAlbumRowWithoutAnArtistGetsTheAlbumsArtistFromItsHeader(t *testing.T) {
	doc := `{"contents":{"twoColumnBrowseResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{"contents":[{"musicResponsiveHeaderRenderer":{
	  "title":{"runs":[{"text":"Discovery"}]},
	  "straplineTextOne":{"runs":[{"text":"Daft Punk","navigationEndpoint":{"browseEndpoint":{"browseId":"UCRr1xG_2WIDs18a6cIiCxeA","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_ARTIST"}}}}}]}}}]}}}}],
	  "secondaryContents":{"sectionListRenderer":{"contents":[{"musicShelfRenderer":{"contents":[{"musicResponsiveListItemRenderer":{
	  "flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"One More Time","navigationEndpoint":{"watchEndpoint":{"videoId":"FGBhQbmPwH8"}}}]}}},{"musicResponsiveListItemFlexColumnRenderer":{"text":{}}}],
	  "fixedColumns":[{"musicResponsiveListItemFixedColumnRenderer":{"text":{"runs":[{"text":"5:21"}]}}}]}}]}}]}}}}}`
	title, tracks, err := ParseEntityPage([]byte(doc), "album")
	if err != nil || title != "Discovery" || len(tracks) != 1 {
		t.Fatalf("%q %v %v", title, tracks, err)
	}
	if len(tracks[0].Artists) != 1 || tracks[0].Artists[0] != "Daft Punk" || tracks[0].Album != "Discovery" {
		t.Errorf("%+v", tracks[0])
	}
}
