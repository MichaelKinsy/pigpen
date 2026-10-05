package ytdlp

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/MichaelKinsy/pigpen/pig-music/internal/lazyre"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// videoID is the shape of a YouTube video ID. Search pages also list playlists,
// channels and albums, whose IDs are longer; they are not tracks.
var videoID = lazyre.New(`^[A-Za-z0-9_-]{11}$`)

// unavailable titles yt-dlp gives entries it cannot show.
var unavailable = map[string]bool{"[Deleted video]": true, "[Private video]": true, "[Unavailable video]": true}

type thumbnail struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type entry struct {
	Type       string      `json:"_type"`
	ID         string      `json:"id"`
	URL        string      `json:"url"`
	Title      string      `json:"title"`
	Track      string      `json:"track"`
	Artist     string      `json:"artist"`
	Artists    []string    `json:"artists"`
	Creator    string      `json:"creator"`
	Channel    string      `json:"channel"`
	Uploader   string      `json:"uploader"`
	Album      string      `json:"album"`
	Duration   *float64    `json:"duration"`
	Thumbnail  string      `json:"thumbnail"`
	Thumbnails []thumbnail `json:"thumbnails"`
	// Entries is set on the top-level object of a playlist or search.
	Entries []entry `json:"entries"`
}

// ParseTracks reads the JSON of `yt-dlp --flat-playlist -J` (a playlist or search
// result with entries) or of a single video, and returns its tracks. Entries that
// are not playable videos are skipped.
func ParseTracks(data []byte) ([]music.Track, error) {
	var top entry
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("yt-dlp printed JSON this player cannot read: %w", err)
	}
	entries := top.Entries
	if top.Entries == nil {
		if top.ID == "" {
			return nil, errors.New("yt-dlp printed JSON with neither entries nor a video")
		}
		entries = []entry{top}
	}
	var tracks []music.Track
	seen := map[string]bool{}
	for _, e := range entries {
		t, ok := e.track()
		if !ok || seen[t.ID] {
			continue
		}
		seen[t.ID] = true
		tracks = append(tracks, t)
	}
	return tracks, nil
}

func (e entry) track() (music.Track, bool) {
	if e.Type == "playlist" || !videoID.MatchString(e.ID) {
		return music.Track{}, false
	}
	if strings.Contains(e.URL, "list=") && !strings.Contains(e.URL, "v=") {
		return music.Track{}, false
	}
	title := first(e.Track, e.Title)
	if title == "" || unavailable[title] {
		return music.Track{}, false
	}
	t := music.Track{ID: e.ID, Title: title, Album: e.Album, ArtURL: e.thumbnailURL()}
	switch {
	case len(e.Artists) > 0:
		t.Artists = e.Artists
	case e.Artist != "":
		t.Artists = splitArtists(e.Artist)
	default:
		if name := strings.TrimSuffix(first(e.Creator, e.Channel, e.Uploader), " - Topic"); name != "" {
			t.Artists = []string{name}
		}
	}
	if e.Duration != nil && *e.Duration > 0 {
		t.Duration = time.Duration(*e.Duration * float64(time.Second))
	}
	return t, true
}

func (e entry) thumbnailURL() string {
	best, area := e.Thumbnail, -1
	for _, th := range e.Thumbnails {
		if a := th.Width * th.Height; th.URL != "" && a > area {
			best, area = th.URL, a
		}
	}
	return best
}

func first(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// splitArtists splits yt-dlp's comma-joined `artist` field.
func splitArtists(s string) []string {
	var out []string
	for _, a := range strings.Split(s, ", ") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}
