package ui

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// The fakes that open an album as a list of tracks (OpenEntity) open its page the same way.
func pageOf(e music.Entity, title string, tracks []music.Track, err error) (music.Page, error) {
	return music.Page{Kind: e.Kind, ID: e.ID, Title: title, Sections: []music.Section{{Title: "Tracks", Tracks: tracks}}}, err
}

func (e *entSource) OpenPage(ctx context.Context, en music.Entity) (music.Page, error) {
	t, tr, err := e.OpenEntity(ctx, en)
	return pageOf(en, t, tr, err)
}
func (e *entSource) Watch(context.Context, string) (music.Watch, error) {
	return music.Watch{}, errors.New("not in this fake")
}

func (s *ctxSource) OpenPage(ctx context.Context, en music.Entity) (music.Page, error) {
	t, tr, err := s.OpenEntity(ctx, en)
	return pageOf(en, t, tr, err)
}
func (s *ctxSource) Watch(context.Context, string) (music.Watch, error) {
	return music.Watch{}, errors.New("not in this fake")
}

func (e *entEnrichSource) OpenPage(ctx context.Context, en music.Entity) (music.Page, error) {
	t, tr, err := e.OpenEntity(ctx, en)
	return pageOf(en, t, tr, err)
}
func (e *entEnrichSource) Watch(context.Context, string) (music.Watch, error) {
	return music.Watch{}, errors.New("not in this fake")
}

// topTracks is the tracks of the page on top, in order.
func (m Model) topTracks() []music.Track {
	if len(m.pages) == 0 {
		return nil
	}
	var out []music.Track
	for _, s := range m.pages[len(m.pages)-1].secs {
		out = append(out, s.Tracks...)
	}
	return out
}
