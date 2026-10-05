package ytdlp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

var _ music.Enricher = (*Source)(nil)

// enrichTemplate asks yt-dlp for just the fields a track row shows, as one JSON object.
const enrichTemplate = "%(.{id,title,artists,artist,channel,uploader,duration,album})j"

type enrichedJSON struct {
	ID       string   `json:"id"`
	Artists  []string `json:"artists"`
	Artist   *string  `json:"artist"`
	Channel  *string  `json:"channel"`
	Uploader *string  `json:"uploader"`
	Duration *float64 `json:"duration"`
	Album    *string  `json:"album"`
}

// Enrich fills in the artist, album and duration of a track: from Inner's details when it has them, else from its own page. A songs search lists only IDs and titles,
// and a page costs a few seconds, so callers ask for the tracks a person is looking at, not for a whole result list.
// What the track already had is kept when yt-dlp has nothing better.
func (s *Source) Enrich(ctx context.Context, t music.Track) (music.Track, error) {
	if t.ID == "" {
		return t, errors.New("enrich: the track has no ID")
	}
	// The direct details (one small request) first; the watch-page extraction only when they fail or leave a gap.
	if e, ok := s.Inner.(music.Enricher); ok {
		if got, err := e.Enrich(ctx, t); err == nil && got.Duration > 0 && len(got.Artists) > 0 {
			return got, nil
		} else if ctx.Err() != nil {
			return t, ctx.Err()
		}
	}
	out, err := s.run(ctx, "--skip-download", "--no-playlist", "--print", enrichTemplate, "--", "https://music.youtube.com/watch?v="+t.ID)
	if err != nil {
		return t, err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return t, fmt.Errorf("enrich %s: yt-dlp printed nothing", t.ID)
	}
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	var e enrichedJSON
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		return t, fmt.Errorf("enrich %s: %w", t.ID, err)
	}
	if artists := e.artists(); len(artists) > 0 {
		t.Artists = artists
	}
	if e.Album != nil && *e.Album != "" {
		t.Album = *e.Album
	}
	if e.Duration != nil && *e.Duration > 0 {
		t.Duration = time.Duration(*e.Duration * float64(time.Second))
	}
	return t, nil
}

// artists prefers the music metadata (artists, artist) and falls back to the channel, without YouTube's " - Topic" suffix.
func (e enrichedJSON) artists() []string {
	if len(e.Artists) > 0 {
		// A name YouTube lists twice (seen: "artists": ["Vindaloo Singh", "Vindaloo Singh"]) is shown once.
		var out []string
		seen := map[string]bool{}
		for _, a := range e.Artists {
			if a = strings.TrimSpace(a); a != "" && !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
		return out
	}
	for _, p := range []*string{e.Artist, e.Channel, e.Uploader} {
		if p != nil && strings.TrimSpace(*p) != "" {
			return []string{strings.TrimSuffix(strings.TrimSpace(*p), " - Topic")}
		}
	}
	return nil
}
