package account

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
)

// Library lists the signed-in user's playlists and their tracks, a page of rows at a time: the first request of a collection
// brings its first rows (about a hundred) at once, and a continuation brings the next. What was fetched is kept for the
// session, in memory.
type Library struct {
	C *Client

	mu    sync.Mutex
	cols  []music.Collection
	haveC bool
	lists map[string]*list
}

type list struct {
	fetch  sync.Mutex // held while the list's next page is asked for, so two callers do not both append the same page
	tracks []music.Track
	next   string // the continuation token, "" when there is none (yet or any more)
	done   bool
}

// Forget drops what is remembered and the cookies, so the next listing asks the account again.
func (l *Library) Forget() {
	l.mu.Lock()
	l.cols, l.haveC, l.lists = nil, false, nil
	l.mu.Unlock()
	l.C.Drop()
}

// Collections lists the library's playlists and albums (the liked songs are the caller's: they have a fixed ID).
func (l *Library) Collections(ctx context.Context) ([]music.Collection, error) {
	l.mu.Lock()
	if l.haveC {
		defer l.mu.Unlock()
		return append([]music.Collection(nil), l.cols...), nil
	}
	l.mu.Unlock()
	var all []music.Collection
	fields := map[string]any{"browseId": "FEmusic_liked_playlists"}
	for pages := 0; pages < 20; pages++ {
		data, err := l.C.Browse(ctx, fields)
		if err != nil {
			return nil, err
		}
		page, err := native.ParseBrowse(data)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrShape, err)
		}
		all = append(all, page.Collections...)
		if page.Next == "" {
			break
		}
		fields = map[string]any{"continuation": page.Next}
	}
	l.mu.Lock()
	l.cols, l.haveC = append([]music.Collection(nil), all...), true
	l.mu.Unlock()
	return all, nil
}

// TracksPage returns up to n tracks of collection id starting at index from, and whether more follow.
func (l *Library) TracksPage(ctx context.Context, id string, from, n int) ([]music.Track, bool, error) {
	if from < 0 || n < 1 {
		return nil, false, fmt.Errorf("a page of %d tracks from %d", n, from)
	}
	end := from + n
	l.mu.Lock()
	st := l.lists[id]
	if st == nil {
		st = &list{}
		if l.lists == nil {
			l.lists = map[string]*list{}
		}
		l.lists[id] = st
	}
	l.mu.Unlock()
	st.fetch.Lock()
	defer st.fetch.Unlock()
	for {
		l.mu.Lock()
		have, next, done := len(st.tracks), st.next, st.done
		l.mu.Unlock()
		if have >= end || done {
			break
		}
		fields := map[string]any{"browseId": browseID(id)}
		if have > 0 || next != "" {
			if next == "" {
				break
			}
			fields = map[string]any{"continuation": next}
		}
		data, err := l.C.Browse(ctx, fields)
		if err != nil {
			return nil, false, err
		}
		page, err := native.ParseBrowse(data)
		if err != nil {
			return nil, false, fmt.Errorf("%w: %v", ErrShape, err)
		}
		l.mu.Lock()
		st.tracks = append(st.tracks, page.Tracks...)
		st.next, st.done = page.Next, page.Next == ""
		l.mu.Unlock()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	last := min(end, len(st.tracks))
	if from > last {
		from = last
	}
	return append([]music.Track(nil), st.tracks[from:last]...), last < len(st.tracks) || !st.done, nil
}

// browseID is what a collection is browsed by: an album by its own ID, a playlist (the liked songs too) by "VL" and its ID.
func browseID(id string) string {
	if strings.HasPrefix(id, "MPREb_") {
		return id
	}
	return "VL" + id
}
