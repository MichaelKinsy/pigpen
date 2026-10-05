package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Review of M10b (rich listings).

// entEnrichSource finds albums and enriches tracks.
type entEnrichSource struct {
	*enrichingSource
	page []music.Track
}

func (e *entEnrichSource) SearchEntities(context.Context, string, int) ([]music.Entity, error) {
	return []music.Entity{{Kind: "artist", ID: "UCaaaaaaaaaaaaaaaaaaaaaa", Title: "Daft Punk"}}, nil
}

func (e *entEnrichSource) OpenEntity(_ context.Context, en music.Entity) (string, []music.Track, error) {
	return en.Title, e.page, nil
}

// An artist's top songs come without a length; they are filled in like any other list the screen shows.
func TestReviewM10bTheTracksOfAnOpenedArtistAreEnriched(t *testing.T) {
	src := &entEnrichSource{
		enrichingSource: &enrichingSource{stubSource: &stubSource{tracks: bareTracks(1)}},
		page:            []music.Track{{ID: "pg000000001", Title: "One More Time", Artists: []string{"Daft Punk"}}, {ID: "pg000000002", Title: "Around the World", Artists: []string{"Daft Punk"}}},
	}
	r := newRig(t, 100, 30)
	r.m = New(Deps{Source: src, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	r.key("/")
	r.typed("daft punk")
	r.key("enter", "3", "enter")
	got := strings.Join(src.enriched, ",")
	if !strings.Contains(got, "pg000000001") || !strings.Contains(got, "pg000000002") {
		t.Errorf("enriched %s", got)
	}
	if text := r.text(); strings.Count(text, "3:20") != 2 {
		t.Errorf("the top songs have no length:\n%s", text)
	}
}
