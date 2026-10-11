package native

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// YouTubeLyrics returns YouTube Music's own lyrics for a video as plain text: the watch-next answer names a Lyrics tab (a browse
// ID), and that page holds the text. A track with no such tab, or an empty page, has none (music.ErrNoLyrics). No cookie.
func (s *Source) YouTubeLyrics(ctx context.Context, videoID string) (string, error) {
	if !videoIDRe.MatchString(videoID) {
		return "", fmt.Errorf("%q is not a video ID", videoID)
	}
	data, err := s.post(ctx, s.NextURL, defaultNextURL, "watch next", map[string]any{"videoId": videoID, "isAudioOnly": true})
	if err != nil {
		return "", err
	}
	var doc any
	if json.Unmarshal(data, &doc) != nil {
		return "", fmt.Errorf("YouTube Music watch next did not answer with JSON")
	}
	browseID := ""
	walkJSON(doc, func(key string, tab map[string]any) {
		if key != "tabRenderer" || browseID != "" {
			return
		}
		cfg := dig(tab, "endpoint", "browseEndpoint")
		if dig(cfg, "browseEndpointContextSupportedConfigs", "browseEndpointContextMusicConfig", "pageType") == "MUSIC_PAGE_TYPE_TRACK_LYRICS" {
			browseID, _ = dig(cfg, "browseId").(string)
		}
	})
	if browseID == "" {
		return "", music.ErrNoLyrics
	}
	page, err := s.post(ctx, s.BrowseURL, defaultBrowseURL, "browse", map[string]any{"browseId": browseID})
	if err != nil {
		return "", err
	}
	var pdoc any
	if json.Unmarshal(page, &pdoc) != nil {
		return "", fmt.Errorf("YouTube Music lyrics did not answer with JSON")
	}
	var b strings.Builder
	walkJSON(pdoc, func(key string, shelf map[string]any) {
		if key == "musicDescriptionShelfRenderer" && b.Len() == 0 {
			for _, r := range runsOf(shelf["description"]) {
				if text, _ := r["text"].(string); text != "" {
					b.WriteString(text)
				}
			}
		}
	})
	lines := strings.Split(b.String(), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(Clean(lines[i])) // control and bidi characters out; the line breaks stay
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if text == "" {
		return "", music.ErrNoLyrics
	}
	return text, nil
}
