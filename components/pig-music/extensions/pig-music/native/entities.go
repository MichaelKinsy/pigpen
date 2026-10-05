package native

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/pigpen/pig-music/internal/lazyre"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

var _ music.EntitySearcher = (*Source)(nil)

var browseIDRe = lazyre.New(`^(MPREb_|UC)[A-Za-z0-9_-]{6,40}$`)

// SearchEntities finds albums and artists for a query: two filtered searches, made together, no cookie. A failure of one is
// not a failure of the other; both failing is an error.
func (s *Source) SearchEntities(ctx context.Context, query string, limit int) ([]music.Entity, error) {
	if query == "" {
		return nil, errors.New("a search needs words to look for")
	}
	if limit < 1 {
		limit = 5
	}
	kinds := []struct{ kind, params string }{{"album", albumsParams}, {"artist", artistsParams}}
	got := make([][]music.Entity, len(kinds))
	errs := make([]error, len(kinds))
	var wg sync.WaitGroup
	for i, k := range kinds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := s.post(ctx, s.SearchURL, defaultSearchURL, "search", map[string]any{"query": query, "params": k.params})
			if err == nil {
				got[i], err = ParseEntities(data, k.kind)
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	var out []music.Entity
	for i := range kinds {
		out = append(out, got[i][:min(limit, len(got[i]))]...)
	}
	if errs[0] != nil && errs[1] != nil {
		return nil, errs[0]
	}
	return out, nil
}

// OpenEntity lists an album's tracks or an artist's top songs.
func (s *Source) OpenEntity(ctx context.Context, e music.Entity) (string, []music.Track, error) {
	if !browseIDRe.MatchString(e.ID) {
		return "", nil, fmt.Errorf("%q is not an album or artist ID", e.ID)
	}
	data, err := s.post(ctx, s.BrowseURL, defaultBrowseURL, "browse", map[string]any{"browseId": e.ID})
	if err != nil {
		return "", nil, err
	}
	title, tracks, err := ParseEntityPage(data, e.Kind)
	if err != nil {
		return "", nil, err
	}
	if title == "" {
		title = e.Title
	}
	return title, tracks, nil
}
