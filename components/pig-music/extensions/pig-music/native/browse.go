package native

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/pig-music/internal/lazyre"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// BrowsePage is what one YouTube Music browse answer holds: tracks (a playlist, the liked songs), collections (the library's
// playlists and albums) and the token that asks for the page after it.
type BrowsePage struct {
	Tracks      []music.Track
	Collections []music.Collection
	Next        string
}

// ParseBrowse reads a browse or browse-continuation answer. It walks the whole document for the renderers it knows rather
// than following one layout, because the same rows sit under different parents in the first page, a continuation and the
// library's tabs. An answer with none of them, or an error, is an error: an unknown shape must not look like an empty library.
func ParseBrowse(data []byte) (BrowsePage, error) {
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return BrowsePage{}, fmt.Errorf("YouTube Music browse did not answer with JSON (%d bytes)", len(data))
	}
	root, _ := doc.(map[string]any)
	if e, ok := root["error"].(map[string]any); ok {
		msg, _ := e["message"].(string)
		return BrowsePage{}, fmt.Errorf("YouTube Music browse failed: %s", Redact(msg))
	}
	var page BrowsePage
	seen := map[string]bool{}
	walkBrowse(doc, "", func(key string, v map[string]any) {
		switch key {
		case "musicResponsiveListItemRenderer":
			if t, ok := browseTrack(v); ok {
				if !seen[t.ID] {
					seen[t.ID] = true
					page.Tracks = append(page.Tracks, t)
				}
			} else if c, ok := browseCollection(v, "title"); ok {
				page.Collections = append(page.Collections, c)
			}
		case "musicTwoRowItemRenderer":
			if c, ok := browseCollection(v, "title"); ok {
				page.Collections = append(page.Collections, c)
			}
		case "nextContinuationData":
			if s, _ := v["continuation"].(string); s != "" && page.Next == "" {
				page.Next = s
			}
		case "continuationCommand":
			if s, _ := v["token"].(string); s != "" && page.Next == "" {
				page.Next = s
			}
		}
	})
	if len(page.Tracks) == 0 && len(page.Collections) == 0 && !hasRenderer(doc) {
		return BrowsePage{}, errors.New("YouTube Music browse answered in a shape this version does not know")
	}
	return page, nil
}

// hasRenderer is true when the answer holds a list or grid at all, even an empty one (an empty playlist is not an error).
func hasRenderer(doc any) bool {
	found := false
	walkJSON(doc, func(key string, _ map[string]any) {
		switch key {
		case "musicPlaylistShelfRenderer", "musicShelfRenderer", "gridRenderer", "musicPlaylistShelfContinuation", "appendContinuationItemsAction":
			found = true
		}
	})
	return found
}

// walkBrowse is walkJSON without what sits beside the list: a carousel (related playlists, suggested songs) is not part of the
// playlist or the library, and the section list's continuation token asks for those carousels, not for the list's next rows
// (the list's own token is on the shelf or the grid). Recorded real answers carry both tokens.
func walkBrowse(v any, parent string, f func(key string, obj map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			if k == "musicCarouselShelfRenderer" || (k == "continuations" && (parent == "sectionListRenderer" || parent == "sectionListContinuation")) {
				continue
			}
			if m, ok := c.(map[string]any); ok {
				f(k, m)
			}
			walkBrowse(c, k, f)
		}
	case []any:
		for _, c := range x {
			walkBrowse(c, parent, f)
		}
	}
}

// walkJSON calls f for every object that is the value of a key, with the key.
func walkJSON(v any, f func(key string, obj map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			if m, ok := c.(map[string]any); ok {
				f(k, m)
			}
			walkJSON(c, f)
		}
	case []any:
		for _, c := range x {
			walkJSON(c, f)
		}
	}
}

func runsOf(v any) []map[string]any {
	m, _ := v.(map[string]any)
	rs, _ := m["runs"].([]any)
	var out []map[string]any
	for _, r := range rs {
		if rm, ok := r.(map[string]any); ok {
			out = append(out, rm)
		}
	}
	return out
}

func joinRuns(rs []map[string]any) string {
	var b strings.Builder
	for _, r := range rs {
		s, _ := r["text"].(string)
		b.WriteString(s)
	}
	return strings.TrimSpace(Clean(b.String()))
}

func dig(v any, path ...string) any {
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[p]
	}
	return v
}

func pageTypeOf(run map[string]any) string {
	s, _ := dig(run, "navigationEndpoint", "browseEndpoint", "browseEndpointContextSupportedConfigs", "browseEndpointContextMusicConfig", "pageType").(string)
	return s
}

// flexRuns are the text runs of column i of a list row (the title, the artists, the album).
func flexRuns(row map[string]any, i int) []map[string]any {
	cols, _ := row["flexColumns"].([]any)
	if i >= len(cols) {
		return nil
	}
	return runsOf(dig(cols[i], "musicResponsiveListItemFlexColumnRenderer", "text"))
}

func browseTrack(row map[string]any) (music.Track, bool) {
	id, _ := dig(row, "playlistItemData", "videoId").(string)
	title := flexRuns(row, 0)
	if id == "" {
		for _, r := range title {
			if s, _ := dig(r, "navigationEndpoint", "watchEndpoint", "videoId").(string); s != "" {
				id = s
			}
		}
	}
	if !videoIDRe.MatchString(id) {
		return music.Track{}, false
	}
	t := music.Track{ID: id, Title: joinRuns(title)}
	if bs, ok := row["badges"].([]any); ok {
		for _, b := range bs {
			if dig(b, "musicInlineBadgeRenderer", "icon", "iconType") == "MUSIC_EXPLICIT_BADGE" {
				t.Explicit = true
			}
		}
	}
	if t.Title == "" {
		return music.Track{}, false
	}
	for _, r := range flexRuns(row, 1) {
		text, _ := r["text"].(string)
		switch pageTypeOf(r) {
		case "MUSIC_PAGE_TYPE_ARTIST", "MUSIC_PAGE_TYPE_USER_CHANNEL":
			t.Artists = append(t.Artists, strings.TrimSpace(Clean(text)))
		case "MUSIC_PAGE_TYPE_ALBUM":
			t.Album = strings.TrimSpace(Clean(text))
		}
	}
	if len(t.Artists) == 0 { // an artist with no page of its own: the plain text before any separator
		if line := joinRuns(flexRuns(row, 1)); line != "" {
			for _, a := range splitArtists(line) {
				t.Artists = append(t.Artists, a)
			}
		}
	}
	if t.Album == "" {
		for _, r := range flexRuns(row, 2) {
			if text, _ := r["text"].(string); pageTypeOf(r) == "MUSIC_PAGE_TYPE_ALBUM" || (t.Album == "" && !countOrYear(strings.TrimSpace(text))) {
				t.Album = strings.TrimSpace(Clean(text))
			}
		}
	}
	for i := 3; t.Album == "" && i < 6; i++ { // an artist's top songs: title, artists, play count, album
		for _, r := range flexRuns(row, i) {
			if text, _ := r["text"].(string); pageTypeOf(r) == "MUSIC_PAGE_TYPE_ALBUM" && t.Album == "" {
				t.Album = strings.TrimSpace(Clean(text))
			}
		}
	}
	if fixed, ok := row["fixedColumns"].([]any); ok && len(fixed) > 0 {
		if s := joinRuns(runsOf(dig(fixed[0], "musicResponsiveListItemFixedColumnRenderer", "text"))); durationRe.MatchString(s) {
			t.Duration = parseClock(s)
		}
	}
	if ths, _ := dig(row, "thumbnail", "musicThumbnailRenderer", "thumbnail", "thumbnails").([]any); len(ths) > 0 {
		t.ArtURL, _ = dig(ths[len(ths)-1], "url").(string)
	}
	return t, true
}

func splitArtists(line string) []string {
	var out []string
	for _, a := range artistSplitRe.Split(line, -1) {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// browseCollection reads a playlist or album tile; the browse ID of a playlist starts with "VL", which the playlist's own ID
// does not carry. The liked-songs lists are not returned: the library lists them itself.
func browseCollection(tile map[string]any, titleKey string) (music.Collection, bool) {
	title := joinRuns(runsOf(tile[titleKey]))
	if title == "" {
		if rs := flexRuns(tile, 0); len(rs) > 0 {
			title = joinRuns(rs)
		}
	}
	id, _ := dig(tile, "navigationEndpoint", "browseEndpoint", "browseId").(string)
	if id == "" { // a tile whose link is on its title only (where ytmusicapi reads it)
		for _, r := range runsOf(tile[titleKey]) {
			if s, _ := dig(r, "navigationEndpoint", "browseEndpoint", "browseId").(string); s != "" {
				id = s
				break
			}
		}
	}
	if id == "" || title == "" {
		return music.Collection{}, false
	}
	c := music.Collection{Title: title}
	switch {
	case strings.HasPrefix(id, "VL"):
		c.ID, c.Kind = strings.TrimPrefix(id, "VL"), "playlist"
	case strings.HasPrefix(id, "MPREb_"):
		c.ID, c.Kind = id, "album"
	default:
		return music.Collection{}, false
	}
	if c.ID == "LM" || c.ID == "LL" {
		return music.Collection{}, false
	}
	for _, r := range runsOf(tile["subtitle"]) {
		if s, _ := r["text"].(string); strings.HasSuffix(s, " songs") || strings.HasSuffix(s, " song") || strings.HasSuffix(s, " tracks") {
			if n, err := strconv.Atoi(strings.NewReplacer(",", "", ".", "").Replace(strings.Fields(s)[0])); err == nil { // "1,234 songs"
				c.Count = n
			}
		}
	}
	return c, true
}

var artistSplitRe = lazyre.New(`\s*(?:,|&| x )\s*`)

// ParseEntities reads the albums or the artists of a search answer (kind "album" or "artist"): each row's browse ID, title,
// the line under it (the "Album" or "Artist" label dropped) and its picture. An answer without a result list is an error.
func ParseEntities(data []byte, kind string) ([]music.Entity, error) {
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("YouTube Music search did not answer with JSON (%d bytes)", len(data))
	}
	var out []music.Entity
	walkJSON(doc, func(key string, row map[string]any) {
		if key != "musicResponsiveListItemRenderer" {
			return
		}
		id, _ := dig(row, "navigationEndpoint", "browseEndpoint", "browseId").(string)
		if (kind == "album" && !strings.HasPrefix(id, "MPREb_")) || (kind == "artist" && !strings.HasPrefix(id, "UC")) {
			return
		}
		title := joinRuns(flexRuns(row, 0))
		if title == "" {
			return
		}
		e := music.Entity{Kind: kind, ID: id, Title: title}
		sub := joinRuns(flexRuns(row, 1))
		for _, label := range []string{"Album • ", "Artist • "} {
			sub = strings.TrimPrefix(sub, label)
		}
		e.Subtitle = sub
		if ths, _ := dig(row, "thumbnail", "musicThumbnailRenderer", "thumbnail", "thumbnails").([]any); len(ths) > 0 {
			e.ArtURL, _ = dig(ths[len(ths)-1], "url").(string)
		}
		out = append(out, e)
	})
	if len(out) == 0 && !hasRenderer(doc) {
		return nil, errors.New("YouTube Music search answered in a shape this version does not know")
	}
	return out, nil
}

// ParseEntityPage reads the page of an album (its tracks in order) or an artist (its top songs): the title from the page's
// header, the tracks from its list. An album's tracks carry its name.
func ParseEntityPage(data []byte, kind string) (string, []music.Track, error) {
	page, err := ParseBrowse(data)
	if err != nil {
		return "", nil, err
	}
	var doc any
	_ = json.Unmarshal(data, &doc)
	title := ""
	walkJSON(doc, func(key string, hdr map[string]any) {
		if title == "" && (key == "musicResponsiveHeaderRenderer" || key == "musicImmersiveHeaderRenderer" || key == "musicVisualHeaderRenderer") {
			title = joinRuns(runsOf(hdr["title"]))
		}
	})
	if kind == "album" {
		var artists []string // the album's own artists, from its header: rows leave them out when they are the same
		walkJSON(doc, func(key string, hdr map[string]any) {
			if key == "musicResponsiveHeaderRenderer" && artists == nil {
				for _, r := range runsOf(hdr["straplineTextOne"]) {
					if pageTypeOf(r) == "MUSIC_PAGE_TYPE_ARTIST" {
						if text, _ := r["text"].(string); text != "" {
							artists = append(artists, strings.TrimSpace(Clean(text)))
						}
					}
				}
			}
		})
		for i := range page.Tracks {
			if title != "" {
				page.Tracks[i].Album = title
			}
			if len(page.Tracks[i].Artists) == 0 {
				page.Tracks[i].Artists = append([]string(nil), artists...)
			}
		}
	}
	return title, page.Tracks, nil
}
