package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

var _ music.Enricher = (*Source)(nil)

// Enrich fills in the artist, album, length and cover of a track from YouTube Music's own `next` answer for the video: one
// small request (0.17 s measured), where yt-dlp's watch-page extraction took 2.8 s. What the track already had is kept.
func (s *Source) Enrich(ctx context.Context, t music.Track) (music.Track, error) {
	if !videoIDRe.MatchString(t.ID) {
		return t, errors.New("enrich: the track has no valid video ID")
	}
	data, err := s.post(ctx, s.NextURL, defaultNextURL, "details", map[string]any{"videoId": t.ID, "isAudioOnly": true})
	if err != nil {
		return t, err
	}
	got, err := parseNext(data, t.ID)
	if err != nil {
		return t, fmt.Errorf("enrich %s: %w", t.ID, err)
	}
	if len(got.Artists) > 0 {
		t.Artists = got.Artists
	}
	if got.Album != "" {
		t.Album = got.Album
	}
	if got.Duration > 0 {
		t.Duration = got.Duration
	}
	if t.Title == "" {
		t.Title = got.Title
	}
	if t.ArtURL == "" {
		t.ArtURL = got.ArtURL
	}
	return t, nil
}

type nextResponse struct {
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Contents struct {
		R struct {
			Tabbed struct {
				R struct {
					Tabs []struct {
						Tab struct {
							Content struct {
								Queue struct {
									Content struct {
										Panel struct {
											Contents []struct {
												Row panelRow `json:"playlistPanelVideoRenderer"`
											} `json:"contents"`
										} `json:"playlistPanelRenderer"`
									} `json:"content"`
								} `json:"musicQueueRenderer"`
							} `json:"content"`
						} `json:"tabRenderer"`
					} `json:"tabs"`
				} `json:"watchNextTabbedResultsRenderer"`
			} `json:"tabbedRenderer"`
		} `json:"singleColumnMusicWatchNextResultsRenderer"`
	} `json:"contents"`
}

type panelRow struct {
	VideoID string `json:"videoId"`
	Title   struct {
		Runs []run `json:"runs"`
	} `json:"title"`
	Byline struct {
		Runs []run `json:"runs"`
	} `json:"longBylineText"`
	Length struct {
		Runs []run `json:"runs"`
	} `json:"lengthText"`
	Thumbnail struct {
		Thumbnails []struct {
			URL string `json:"url"`
		} `json:"thumbnails"`
	} `json:"thumbnail"`
}

// parseNext reads the row for video from a `next` answer: the first panel row is the video asked about, but the row is
// matched by its ID so a changed order cannot give a track someone else's artist.
func parseNext(data []byte, video string) (music.Track, error) {
	var r nextResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return music.Track{}, errors.New("the answer is not JSON")
	}
	if r.Error != nil {
		return music.Track{}, fmt.Errorf("YouTube Music answered an error: %s", Clean(r.Error.Message))
	}
	for _, tab := range r.Contents.R.Tabbed.R.Tabs {
		for _, c := range tab.Tab.Content.Queue.Content.Panel.Contents {
			row := c.Row
			if row.VideoID != video {
				continue
			}
			t := music.Track{ID: video}
			for _, run := range row.Title.Runs {
				t.Title += run.Text
			}
			t.Title = Clean(t.Title)
			details(&t, row.Byline.Runs)
			if len(row.Length.Runs) > 0 && durationRe.MatchString(row.Length.Runs[0].Text) {
				t.Duration = parseClock(row.Length.Runs[0].Text)
			}
			if th := row.Thumbnail.Thumbnails; len(th) > 0 {
				t.ArtURL = th[len(th)-1].URL
			}
			t.Album = Clean(t.Album)
			for i := range t.Artists {
				t.Artists[i] = Clean(t.Artists[i])
			}
			return t, nil
		}
	}
	return music.Track{}, errors.New("the answer has no row for this video")
}
