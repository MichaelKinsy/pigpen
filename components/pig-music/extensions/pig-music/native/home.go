package native

import (
	"context"
	"errors"
	"strings"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// chartsID is the browse ID of YouTube Music's charts page, HomeID the one of its home (package account browses it signed).
const (
	chartsID = "FEmusic_charts"
	HomeID   = "FEmusic_home"
)

// Popular is the chart of the songs most played now: the charts page (no account) names its charts as playlists, and the
// top one ("Top 100 Music Videos", by YouTube Music's country for an unsigned request) is browsed for its tracks. Two
// requests, no cookie.
func (s *Source) Popular(ctx context.Context) (music.Section, error) {
	data, err := s.post(ctx, s.BrowseURL, defaultBrowseURL, "charts", map[string]any{"browseId": chartsID})
	if err != nil {
		return music.Section{}, err
	}
	chart, err := TopChart(data)
	if err != nil {
		return music.Section{}, err
	}
	data, err = s.post(ctx, s.BrowseURL, defaultBrowseURL, "chart", map[string]any{"browseId": chart.ID})
	if err != nil {
		return music.Section{}, err
	}
	return ParseChart(data, chart)
}

// TopChart picks the chart Popular lists from a charts answer: the "Top 100 Music Videos" playlist, else the first playlist of
// the "Video charts" shelf, else the first playlist on the page whose name starts with "Top".
func TopChart(data []byte) (music.Entity, error) {
	p, err := ParsePage(data, music.Entity{Kind: "charts", ID: chartsID})
	if err != nil {
		return music.Entity{}, err
	}
	var video, top *music.Entity
	for _, sec := range p.Sections {
		for i := range sec.Entities {
			e := &sec.Entities[i]
			if e.Kind != "playlist" {
				continue
			}
			if strings.HasPrefix(e.Title, "Top 100 Music Videos") {
				return *e, nil
			}
			if video == nil && strings.EqualFold(sec.Title, "Video charts") {
				video = e
			}
			if top == nil && strings.HasPrefix(e.Title, "Top") {
				top = e
			}
		}
	}
	switch {
	case video != nil:
		return *video, nil
	case top != nil:
		return *top, nil
	}
	return music.Entity{}, errors.New("the YouTube Music charts name no chart this version knows")
}

// ParseChart reads a chart's playlist answer as one section: the chart's name and its tracks in chart order.
func ParseChart(data []byte, chart music.Entity) (music.Section, error) {
	p, err := ParsePage(data, chart)
	if err != nil {
		return music.Section{}, err
	}
	for _, sec := range p.Sections {
		if len(sec.Tracks) > 0 {
			return music.Section{Title: p.Title, Tracks: sec.Tracks}, nil
		}
	}
	return music.Section{}, errors.New("the YouTube Music chart lists no tracks")
}

// ParseHome reads a home answer (FEmusic_home, signed in or not) as its sections, in the order YouTube Music shows them: songs
// to play, and albums, artists and playlists to open.
func ParseHome(data []byte) ([]music.Section, error) {
	p, err := ParsePage(data, music.Entity{Kind: "home", ID: HomeID})
	if err != nil {
		return nil, err
	}
	return p.Sections, nil
}
