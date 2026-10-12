package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/pigpen/pig-music/internal/lazyre"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

var _ music.Navigator = (*Source)(nil)

var pageIDRes = map[string]*lazyre.Regexp{
	"artist":   lazyre.New(`^(UC|MPLA)[A-Za-z0-9_-]{6,40}$`),
	"album":    lazyre.New(`^MPREb_[A-Za-z0-9_-]{6,40}$`),
	"related":  lazyre.New(`^MPTRt_[A-Za-z0-9_-]{6,40}$`),
	"playlist": lazyre.New(`^VL[A-Za-z0-9_-]{6,60}$`),
}

// Watch asks YouTube Music's watch-next endpoint about a video with its RDAMVM mix: who it is by, which album it is on, the mix
// itself and the page of related things. No cookie.
func (s *Source) Watch(ctx context.Context, videoID string) (music.Watch, error) {
	if !videoIDRe.MatchString(videoID) {
		return music.Watch{}, fmt.Errorf("%q is not a video ID", videoID)
	}
	data, err := s.post(ctx, s.NextURL, defaultNextURL, "watch next", map[string]any{"videoId": videoID, "playlistId": "RDAMVM" + videoID, "isAudioOnly": true})
	if err != nil {
		return music.Watch{}, err
	}
	return ParseWatch(data)
}

// OpenPage browses an artist, album or playlist (by its browse ID) or a track's Related page.
func (s *Source) OpenPage(ctx context.Context, e music.Entity) (music.Page, error) {
	re := pageIDRes[e.Kind]
	if re == nil || !re.MatchString(e.ID) {
		return music.Page{}, fmt.Errorf("%q is not a %s page ID", e.ID, e.Kind)
	}
	data, err := s.post(ctx, s.BrowseURL, defaultBrowseURL, "browse", map[string]any{"browseId": e.ID})
	if err != nil {
		return music.Page{}, err
	}
	return ParsePage(data, e)
}

// ParsePage reads a browse answer as a page: its title and the lists on it (shelves of songs, carousels of albums, singles,
// videos, artists, playlists) in the order YouTube Music shows them. A list with nothing usable in it is left out. An answer in
// a shape this version does not know is an error, not an empty page.
func ParsePage(data []byte, e music.Entity) (music.Page, error) {
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return music.Page{}, fmt.Errorf("YouTube Music browse did not answer with JSON (%d bytes)", len(data))
	}
	if root, _ := doc.(map[string]any); root != nil {
		if er, ok := root["error"].(map[string]any); ok {
			msg, _ := er["message"].(string)
			return music.Page{}, fmt.Errorf("YouTube Music browse failed: %s", Redact(msg))
		}
	}
	p := music.Page{Kind: e.Kind, ID: e.ID, Title: e.Title}
	var artists []string
	walkJSON(doc, func(key string, v map[string]any) {
		switch key {
		case "musicResponsiveHeaderRenderer", "musicImmersiveHeaderRenderer", "musicVisualHeaderRenderer", "musicDetailHeaderRenderer":
			if t := joinRuns(runsOf(v["title"])); t != "" && p.Subtitle == "" && p.Title == e.Title {
				p.Title = t
			}
			if p.Subtitle == "" {
				p.Subtitle = joinRuns(runsOf(v["monthlyListenerCount"]))
				if p.Subtitle == "" {
					p.Subtitle = joinRuns(runsOf(v["subtitle"]))
				}
			}
			if key == "musicResponsiveHeaderRenderer" && artists == nil {
				for _, r := range runsOf(v["straplineTextOne"]) {
					if pageTypeOf(r) == "MUSIC_PAGE_TYPE_ARTIST" {
						if text, _ := r["text"].(string); text != "" {
							artists = append(artists, strings.TrimSpace(Clean(text)))
						}
					}
				}
			}
		case "musicShelfRenderer", "musicPlaylistShelfRenderer", "musicCarouselShelfRenderer":
			title := joinRuns(runsOf(v["title"]))
			if title == "" {
				title = joinRuns(runsOf(dig(v, "header", "musicCarouselShelfBasicHeaderRenderer", "title")))
			}
			if title == "" && key != "musicCarouselShelfRenderer" { // an album's own list has no heading
				title = "Tracks"
			}
			sec := music.Section{Title: title}
			items, _ := v["contents"].([]any)
			for _, it := range items {
				m, _ := it.(map[string]any)
				for k, row := range m {
					rm, _ := row.(map[string]any)
					if k != "musicResponsiveListItemRenderer" && k != "musicTwoRowItemRenderer" || rm == nil {
						continue
					}
					if t, ent, ok := pageItem(k, rm); ok {
						if ent.ID != "" {
							sec.Entities = append(sec.Entities, ent)
						} else {
							sec.Tracks = append(sec.Tracks, t)
						}
					}
				}
			}
			if title != "" && len(sec.Tracks)+len(sec.Entities) > 0 {
				p.Sections = append(p.Sections, sec)
			}
		}
	})
	if e.Kind == "album" {
		for i := range p.Sections {
			for j := range p.Sections[i].Tracks {
				t := &p.Sections[i].Tracks[j]
				if p.Title != "" {
					t.Album = p.Title
				}
				if len(t.Artists) == 0 {
					t.Artists = append([]string(nil), artists...)
				}
			}
		}
	}
	if len(p.Sections) == 0 && !hasRenderer(doc) && !hasShelf(doc) {
		return music.Page{}, errors.New("YouTube Music browse answered in a shape this version does not know")
	}
	return p, nil
}

// hasShelf is true when the answer has a shelf or carousel at all, even one with nothing usable in it.
func hasShelf(doc any) bool {
	found := false
	walkJSON(doc, func(key string, _ map[string]any) {
		switch key {
		case "musicShelfRenderer", "musicCarouselShelfRenderer", "musicPlaylistShelfRenderer", "musicDescriptionShelfRenderer":
			found = true
		}
	})
	return found
}

// pageItem reads one row or tile of a shelf: a song or video (a Track) or an album, artist or playlist (an Entity, whose ID is
// set). Anything else is not usable.
func pageItem(key string, row map[string]any) (music.Track, music.Entity, bool) {
	if key == "musicResponsiveListItemRenderer" {
		if t, ok := browseTrack(row); ok {
			return t, music.Entity{}, true
		}
		title := joinRuns(flexRuns(row, 0))
		return entityOf(row, title, joinRuns(flexRuns(row, 1)))
	}
	title := joinRuns(runsOf(row["title"]))
	sub := runsOf(row["subtitle"])
	if id, _ := dig(row, "navigationEndpoint", "watchEndpoint", "videoId").(string); videoIDRe.MatchString(id) && title != "" {
		t := music.Track{ID: id, Title: title}
		for _, r := range sub {
			if pageTypeOf(r) == "MUSIC_PAGE_TYPE_ARTIST" || pageTypeOf(r) == "MUSIC_PAGE_TYPE_USER_CHANNEL" {
				text, _ := r["text"].(string)
				t.Artists = append(t.Artists, strings.TrimSpace(Clean(text)))
			}
		}
		if len(t.Artists) == 0 {
			if first, _, _ := strings.Cut(joinRuns(sub), " • "); first != "" && !strings.Contains(first, " views") {
				t.Artists = splitArtists(first)
			}
		}
		if ths, _ := dig(row, "thumbnailRenderer", "musicThumbnailRenderer", "thumbnail", "thumbnails").([]any); len(ths) > 0 {
			t.ArtURL, _ = dig(ths[len(ths)-1], "url").(string)
		}
		return t, music.Entity{}, true
	}
	return entityOf(row, title, joinRuns(sub))
}

// entityOf reads the browse link of a row or tile (on the item, or on its title) as an album, artist or playlist.
func entityOf(row map[string]any, title, sub string) (music.Track, music.Entity, bool) {
	id, _ := dig(row, "navigationEndpoint", "browseEndpoint", "browseId").(string)
	if id == "" {
		for _, r := range runsOf(row["title"]) {
			if s, _ := dig(r, "navigationEndpoint", "browseEndpoint", "browseId").(string); s != "" {
				id = s
				break
			}
		}
	}
	var kind string
	switch {
	case strings.HasPrefix(id, "MPREb_"):
		kind = "album"
	case strings.HasPrefix(id, "UC"):
		kind = "artist"
	case strings.HasPrefix(id, "VL"):
		kind = "playlist"
	default:
		return music.Track{}, music.Entity{}, false
	}
	if title == "" || kind == "playlist" && (id == "VLLM" || id == "VLLL") {
		return music.Track{}, music.Entity{}, false
	}
	for _, label := range []string{"Album • ", "Artist • ", "Playlist • ", "Single • ", "EP • "} {
		sub = strings.TrimPrefix(sub, label)
	}
	return music.Track{}, music.Entity{Kind: kind, ID: id, Title: title, Subtitle: sub}, true
}

// ParseWatch reads a watch-next answer: the seed track's artists and album (from its line, or from its menu's "Go to" entries
// when the line names none), the tracks of the mix, and the Related tab's browse ID.
func ParseWatch(data []byte) (music.Watch, error) {
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return music.Watch{}, fmt.Errorf("YouTube Music watch next did not answer with JSON (%d bytes)", len(data))
	}
	var w music.Watch
	seen := map[string]bool{}
	var seed map[string]any
	walkJSON(doc, func(key string, v map[string]any) {
		switch key {
		case "playlistPanelVideoRenderer":
			t, ok := panelTrack(v)
			if !ok || seen[t.ID] {
				return
			}
			seen[t.ID] = true
			if seed == nil {
				seed = v
			}
			w.Radio = append(w.Radio, t)
		case "tabRenderer":
			cfg := dig(v, "endpoint", "browseEndpoint")
			if dig(cfg, "browseEndpointContextSupportedConfigs", "browseEndpointContextMusicConfig", "pageType") == "MUSIC_PAGE_TYPE_TRACK_RELATED" && w.RelatedID == "" {
				w.RelatedID, _ = dig(cfg, "browseId").(string)
			}
		}
	})
	if seed == nil {
		return music.Watch{}, errors.New("YouTube Music watch next answered in a shape this version does not know")
	}
	for _, r := range runsOf(seed["longBylineText"]) {
		text, _ := r["text"].(string)
		id, _ := dig(r, "navigationEndpoint", "browseEndpoint", "browseId").(string)
		switch pageTypeOf(r) {
		case "MUSIC_PAGE_TYPE_ARTIST", "MUSIC_PAGE_TYPE_USER_CHANNEL":
			w.Artists = append(w.Artists, music.Entity{Kind: "artist", ID: id, Title: strings.TrimSpace(Clean(text))})
		case "MUSIC_PAGE_TYPE_ALBUM":
			if w.Album.ID == "" {
				w.Album = music.Entity{Kind: "album", ID: id, Title: strings.TrimSpace(Clean(text))}
			}
		}
	}
	items, _ := dig(seed, "menu", "menuRenderer", "items").([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		r, _ := m["menuNavigationItemRenderer"].(map[string]any)
		be := dig(r, "navigationEndpoint", "browseEndpoint")
		id, _ := dig(be, "browseId").(string)
		switch dig(be, "browseEndpointContextSupportedConfigs", "browseEndpointContextMusicConfig", "pageType") {
		case "MUSIC_PAGE_TYPE_ALBUM":
			if w.Album.ID == "" {
				w.Album = music.Entity{Kind: "album", ID: id, Title: seedAlbumName(w.Radio)}
			}
		case "MUSIC_PAGE_TYPE_ARTIST":
			if len(w.Artists) == 0 && id != "" {
				w.Artists = append(w.Artists, music.Entity{Kind: "artist", ID: id, Title: strings.Join(w.Radio[0].Artists, ", ")})
			}
		}
	}
	return w, nil
}

// seedAlbumName is the album the seed's menu links to when its line has no name for it: not known, so the seed's own title
// stands in for the heading until the page itself is read.
func seedAlbumName(radio []music.Track) string {
	if len(radio) > 0 && radio[0].Album != "" {
		return radio[0].Album
	}
	return ""
}

// panelTrack reads one entry of the watch-next queue.
func panelTrack(p map[string]any) (music.Track, bool) {
	id, _ := p["videoId"].(string)
	title := joinRuns(runsOf(p["title"]))
	if !videoIDRe.MatchString(id) || title == "" {
		return music.Track{}, false
	}
	t := music.Track{ID: id, Title: title}
	line := runsOf(p["longBylineText"])
	for _, r := range line {
		text, _ := r["text"].(string)
		switch pageTypeOf(r) {
		case "MUSIC_PAGE_TYPE_ARTIST", "MUSIC_PAGE_TYPE_USER_CHANNEL":
			t.Artists = append(t.Artists, strings.TrimSpace(Clean(text)))
		case "MUSIC_PAGE_TYPE_ALBUM":
			t.Album = strings.TrimSpace(Clean(text))
		}
	}
	if len(t.Artists) == 0 {
		if first, _, _ := strings.Cut(joinRuns(line), " • "); first != "" {
			t.Artists = splitArtists(first)
		}
	}
	if s := joinRuns(runsOf(p["lengthText"])); durationRe.MatchString(s) {
		t.Duration = parseClock(s)
	}
	if ths, _ := dig(p, "thumbnail", "thumbnails").([]any); len(ths) > 0 {
		t.ArtURL, _ = dig(ths[len(ths)-1], "url").(string)
	}
	if bs, ok := p["badges"].([]any); ok {
		for _, b := range bs {
			if dig(b, "musicInlineBadgeRenderer", "icon", "iconType") == "MUSIC_EXPLICIT_BADGE" {
				t.Explicit = true
			}
		}
	}
	return t, true
}
