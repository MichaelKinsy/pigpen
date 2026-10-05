package native

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/MichaelKinsy/pigpen/pig-music/internal/lazyre"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// PlaylistEntry is one item of a playlist listing.
type PlaylistEntry struct {
	VideoID  string
	Title    string
	Author   string
	Duration time.Duration
}

// PlaylistLister lists a public playlist. Helper (the pigmusic program) is the production one.
type PlaylistLister interface {
	Playlist(ctx context.Context, id string) ([]PlaylistEntry, error)
}

// Source is a music.Source with no external program: search is YouTube Music's
// own unauthenticated web search (the songs filter), playlists come from WaxTap.
// It reads no cookie and sends no credential.
type Source struct {
	HTTP *http.Client
	// SearchURL and NextURL override the search and next endpoints (tests).
	SearchURL string
	NextURL   string
	BrowseURL string // tests
	// Timeout bounds one search; default 20 s.
	Timeout time.Duration
	Lister  PlaylistLister
}

var _ music.Source = (*Source)(nil)

const (
	defaultSearchURL = "https://music.youtube.com/youtubei/v1/search?prettyPrint=false"
	defaultNextURL   = "https://music.youtube.com/youtubei/v1/next?prettyPrint=false"
	defaultBrowseURL = "https://music.youtube.com/youtubei/v1/browse?prettyPrint=false"
	// The albums and artists filters of YouTube Music's search.
	albumsParams  = "EgWKAQIYAWoKEAkQBRAKEAMQBA=="
	artistsParams = "EgWKAQIgAWoKEAkQBRAKEAMQBA=="
	// The songs filter of YouTube Music's search.
	songsParams = "EgWKAQIIAWoKEAkQBRAKEAMQBA=="
	// Search does not need a current client version; this is the one it was recorded with.
	webRemixVersion = "1.20260901.01.00"
	searchUA        = "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0"
)

// Search finds songs. It lists the first page, at most limit tracks.
func (s *Source) Search(ctx context.Context, query string, limit int) ([]music.Track, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("a search needs words to look for")
	}
	if limit < 1 {
		limit = 10
	}
	data, err := s.post(ctx, s.SearchURL, defaultSearchURL, "search", map[string]any{
		"query":  query,
		"params": songsParams,
	})
	if err != nil {
		return nil, err
	}
	return parseSearch(data, limit)
}

// post sends one WEB_REMIX request (no cookie, no credential) and returns the body. override replaces the endpoint (tests).
func (s *Source) post(ctx context.Context, override, endpoint, what string, fields map[string]any) ([]byte, error) {
	fields["context"] = map[string]any{"client": map[string]any{
		"clientName": "WEB_REMIX", "clientVersion": webRemixVersion, "hl": "en", "gl": "US",
	}}
	body, _ := json.Marshal(fields)
	url := override
	if url == "" {
		url = endpoint
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://music.youtube.com")
	req.Header.Set("User-Agent", searchUA)
	client := s.HTTP
	if client == nil {
		client = sharedClient() // one transport, so the next request reuses the connection (no new TLS handshake)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, Explain(RedactError(err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, Explain(RedactError(err))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("YouTube Music %s answered HTTP %d", what, resp.StatusCode)
	}
	return data, nil
}

// Library needs a signed-in account, which the native engine never uses.
func (s *Source) Library(ctx context.Context) ([]music.Collection, error) {
	return nil, errors.New("the library needs a signed-in account, and the native engine does not sign in or read cookies; use the mpv engine (engine = mpv) with cookieBrowser set")
}

var playlistIDRe = lazyre.New(`^(PL|OLAK5uy_|RD|UU|FL)[A-Za-z0-9_-]{10,60}$`)

// Tracks lists a public playlist.
func (s *Source) Tracks(ctx context.Context, collectionID string) ([]music.Track, error) {
	switch {
	case collectionID == "LM":
		return nil, errors.New("liked songs need a signed-in account, which the native engine does not use; use the mpv engine (engine = mpv)")
	case strings.HasPrefix(collectionID, "MPREb_"):
		return nil, errors.New("the native engine cannot list an album by its browse ID; search for its songs instead")
	case !playlistIDRe.MatchString(collectionID):
		return nil, fmt.Errorf("%q is not a public YouTube playlist ID", collectionID)
	case s.Lister == nil:
		return nil, errors.New("this Source was built without a playlist lister")
	}
	entries, err := s.Lister.Playlist(ctx, collectionID)
	if err != nil {
		return nil, Explain(err)
	}
	var out []music.Track
	seen := map[string]bool{}
	for _, e := range entries {
		if !videoIDRe.MatchString(e.VideoID) || e.Title == "" || seen[e.VideoID] {
			continue
		}
		seen[e.VideoID] = true
		t := music.Track{ID: e.VideoID, Title: Clean(e.Title), Duration: e.Duration}
		if e.Author != "" {
			t.Artists = []string{Clean(e.Author)}
		}
		out = append(out, t)
	}
	return out, nil
}

// PlayURL is the track's watch URL. The native Player resolves the stream itself, on the host that plays it.
func (s *Source) PlayURL(t music.Track) string {
	return "https://music.youtube.com/watch?v=" + t.ID
}

// ── parsing ────────────────────────────────────────────────────────────────

type run struct {
	Text               string `json:"text"`
	NavigationEndpoint *struct {
		BrowseEndpoint *struct {
			Config struct {
				Music struct {
					PageType string `json:"pageType"`
				} `json:"browseEndpointContextMusicConfig"`
			} `json:"browseEndpointContextSupportedConfigs"`
		} `json:"browseEndpoint"`
		WatchEndpoint *struct {
			VideoID string `json:"videoId"`
		} `json:"watchEndpoint"`
	} `json:"navigationEndpoint"`
}

type flexColumn struct {
	R struct {
		Text struct {
			Runs []run `json:"runs"`
		} `json:"text"`
	} `json:"musicResponsiveListItemFlexColumnRenderer"`
}

type listItem struct {
	Item struct {
		FlexColumns []flexColumn `json:"flexColumns"`
		Data        *struct {
			VideoID string `json:"videoId"`
		} `json:"playlistItemData"`
		Badges []struct {
			R struct {
				Icon struct {
					Type string `json:"iconType"`
				} `json:"icon"`
			} `json:"musicInlineBadgeRenderer"`
		} `json:"badges"`
		Thumbnail struct {
			R struct {
				Thumbnail struct {
					Thumbnails []struct {
						URL string `json:"url"`
					} `json:"thumbnails"`
				} `json:"thumbnail"`
			} `json:"musicThumbnailRenderer"`
		} `json:"thumbnail"`
	} `json:"musicResponsiveListItemRenderer"`
}

type searchResponse struct {
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Contents *struct {
		Tabbed struct {
			Tabs []struct {
				Tab struct {
					Content struct {
						Sections struct {
							Contents []struct {
								Shelf *struct {
									Contents []listItem `json:"contents"`
								} `json:"musicShelfRenderer"`
							} `json:"contents"`
						} `json:"sectionListRenderer"`
					} `json:"content"`
				} `json:"tabRenderer"`
			} `json:"tabs"`
		} `json:"tabbedSearchResultsRenderer"`
	} `json:"contents"`
}

var durationRe = lazyre.New(`^\d{1,2}(?::\d{2}){1,2}$`)

func parseSearch(data []byte, limit int) ([]music.Track, error) {
	var resp searchResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("YouTube Music search did not answer with JSON (%d bytes)", len(data))
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("YouTube Music search failed: %s", Redact(resp.Error.Message))
	}
	if resp.Contents == nil {
		return nil, errors.New("YouTube Music search answered in a shape this version does not know; the native engine's search needs an update (or use the mpv engine)")
	}
	var out []music.Track
	seen := map[string]bool{}
	for _, tab := range resp.Contents.Tabbed.Tabs {
		for _, sec := range tab.Tab.Content.Sections.Contents {
			if sec.Shelf == nil {
				continue
			}
			for _, it := range sec.Shelf.Contents {
				t, ok := trackOf(it)
				if !ok || seen[t.ID] {
					continue
				}
				seen[t.ID] = true
				out = append(out, t)
				if len(out) >= limit {
					return out, nil
				}
			}
		}
	}
	return out, nil
}

func trackOf(it listItem) (music.Track, bool) {
	r := it.Item
	id := ""
	if r.Data != nil {
		id = r.Data.VideoID
	}
	if id == "" && len(r.FlexColumns) > 0 {
		for _, run := range r.FlexColumns[0].R.Text.Runs {
			if run.NavigationEndpoint != nil && run.NavigationEndpoint.WatchEndpoint != nil {
				id = run.NavigationEndpoint.WatchEndpoint.VideoID
			}
		}
	}
	if !videoIDRe.MatchString(id) || len(r.FlexColumns) == 0 {
		return music.Track{}, false
	}
	t := music.Track{ID: id}
	for _, b := range r.Badges {
		if b.R.Icon.Type == "MUSIC_EXPLICIT_BADGE" {
			t.Explicit = true
		}
	}
	for _, run := range r.FlexColumns[0].R.Text.Runs {
		t.Title += run.Text
	}
	t.Title = strings.TrimSpace(Clean(t.Title))
	if t.Title == "" {
		return music.Track{}, false
	}
	if th := r.Thumbnail.R.Thumbnail.Thumbnails; len(th) > 0 {
		t.ArtURL = th[len(th)-1].URL
	}
	if len(r.FlexColumns) > 1 {
		details(&t, r.FlexColumns[1].R.Text.Runs)
	}
	t.Album = Clean(t.Album)
	for i := range t.Artists {
		t.Artists[i] = Clean(t.Artists[i])
	}
	return t, true
}

// details reads the "Artist • Album • 3:52" line. Runs that link to an artist
// or an album say what they are; without links the segments are told apart by
// position, and a leading "Song" or "Video" label is dropped.
func details(t *music.Track, runs []run) {
	var segs [][]run
	cur := []run{}
	for _, r := range runs {
		if strings.TrimSpace(r.Text) == "•" {
			segs = append(segs, cur)
			cur = []run{}
			continue
		}
		cur = append(cur, r)
	}
	segs = append(segs, cur)
	text := func(s []run) string {
		var b strings.Builder
		for _, r := range s {
			b.WriteString(r.Text)
		}
		return strings.TrimSpace(b.String())
	}
	if n := len(segs); n > 0 && durationRe.MatchString(text(segs[n-1])) {
		t.Duration = parseClock(text(segs[n-1]))
		segs = segs[:n-1]
	}
	if len(segs) > 0 {
		switch text(segs[0]) {
		case "Song", "Video", "Episode":
			segs = segs[1:]
		}
	}
	pageOf := func(r run) string {
		if r.NavigationEndpoint != nil && r.NavigationEndpoint.BrowseEndpoint != nil {
			return r.NavigationEndpoint.BrowseEndpoint.Config.Music.PageType
		}
		return ""
	}
	linked := false
	for _, s := range segs {
		for _, r := range s {
			switch pageOf(r) {
			case "MUSIC_PAGE_TYPE_ARTIST", "MUSIC_PAGE_TYPE_USER_CHANNEL":
				t.Artists = append(t.Artists, r.Text)
				linked = true
			case "MUSIC_PAGE_TYPE_ALBUM":
				t.Album = r.Text
				linked = true
			}
		}
	}
	if linked {
		return
	}
	if len(segs) > 0 {
		for _, a := range regexp.MustCompile(`\s*(?:,|&)\s*`).Split(text(segs[0]), -1) {
			if a = strings.TrimSpace(a); a != "" {
				t.Artists = append(t.Artists, a)
			}
		}
	}
	if len(segs) > 1 {
		if a := text(segs[1]); !countOrYear(a) {
			t.Album = a
		}
	}
}

// countOrYear tells "1.8B views", "12M plays" and "2009", which a video's line has where a song's has its album, from an album.
func countOrYear(s string) bool {
	return countOrYearRe.MatchString(s)
}

var countOrYearRe = lazyre.New(`(?i)^(\d{4}|[\d.,]+\s*[kmb]?\s*(views?|plays?|likes?|listeners?|subscribers?))$`)

func parseClock(s string) time.Duration {
	var secs int
	for _, p := range strings.Split(s, ":") {
		n, _ := strconv.Atoi(p)
		secs = secs*60 + n
	}
	return time.Duration(secs) * time.Second
}

// sharedClient is the HTTP client of a Source built without one, made on first use (nothing at load).
var sharedClient = sync.OnceValue(NewHTTPClient)

// NewSource is the production Source: search needs nothing, and a public playlist is listed by the pigmusic helper
// (configured is its path from the settings; empty looks for it).
func NewSource(configured string) *Source { return &Source{Lister: Helper{Configured: configured}} }

// NewSearcher is the direct YouTube Music search the yt-dlp Source asks first (ytdlp.Source.Inner): one HTTP request with
// artists, album and length, no cookies. PIG_MUSIC_SEARCH=ytdlp turns it off (nil), leaving yt-dlp's slower listing.
func NewSearcher(getenv func(string) string) music.Source {
	if getenv != nil && strings.EqualFold(strings.TrimSpace(getenv("PIG_MUSIC_SEARCH")), "ytdlp") {
		return nil
	}
	return &Source{}
}

// ClientContext is the "context" member of a WEB_REMIX request, and UserAgent the one the requests carry; the signed-in
// library (package account) uses the same client identity as search.
func ClientContext() map[string]any {
	return map[string]any{"client": map[string]any{
		"clientName": "WEB_REMIX", "clientVersion": webRemixVersion, "hl": "en", "gl": "US",
	}}
}

// UserAgent is the User-Agent of the WEB_REMIX requests.
const UserAgent = searchUA
