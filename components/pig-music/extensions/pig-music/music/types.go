// Package music holds the types the pig-music player is built from: tracks,
// the Source that finds them and the Player that plays them, plus the paths,
// settings and dependency checks both ends share.
package music

import (
	"context"
	"image"
	"time"
)

// Track is one playable item.
type Track struct {
	ID       string        `json:"id"` // YouTube video ID
	Title    string        `json:"title"`
	Artists  []string      `json:"artists,omitempty"`
	Album    string        `json:"album,omitempty"`
	Duration time.Duration `json:"duration,omitempty"`
	ArtURL   string        `json:"artUrl,omitempty"`
	Explicit bool          `json:"explicit,omitempty"` // YouTube Music's explicit badge
}

// Entity is a search result that is not a song: an album or an artist, opened to see its tracks (Enter in the search results).
type Entity struct {
	Kind     string `json:"kind"` // "album" | "artist"
	ID       string `json:"id"`   // the browse ID: MPREb_... for an album, UC... for an artist
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"` // "Daft Punk • 2013", "80.8M monthly audience"
	ArtURL   string `json:"artUrl,omitempty"`
}

// EntitySearcher is an optional extra of a Source: albums and artists for a search, and the tracks of one of them. A Source
// that is not one shows songs only.
type EntitySearcher interface {
	SearchEntities(ctx context.Context, query string, limit int) ([]Entity, error)
	// OpenEntity returns the entity's title and its tracks in order (an album's, or an artist's top songs); the tracks of an
	// album carry its name.
	OpenEntity(ctx context.Context, e Entity) (title string, tracks []Track, err error)
}

// Collection is a playlist, album or the liked-songs list.
type Collection struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"` // "playlist" | "album" | "liked"
	Title string `json:"title"`
	Count int    `json:"count"`
}

// Source is where music comes from.
type Source interface {
	Search(ctx context.Context, query string, limit int) ([]Track, error)
	// Library lists the user's collections. It needs cookies and fails with a
	// message saying so when none are configured.
	Library(ctx context.Context) ([]Collection, error)
	Tracks(ctx context.Context, collectionID string) ([]Track, error)
	// PlayURL is the URL handed to the player, which resolves the stream itself.
	PlayURL(t Track) string
}

// NeedsConsentError means the library needs the user's browser session and the user has not yet agreed to that browser being
// read. Description names the browser only; Notes says what to expect (a Keychain prompt, Full Disk Access).
type NeedsConsentError struct {
	Browser     string
	Description string
	Notes       string
}

func (e *NeedsConsentError) Error() string {
	return "pig-music needs your OK to let yt-dlp read " + e.Description + "'s cookies to list your library"
}

// NoBrowserError means there is no browser to read: none detected, an unsupported one, or no cookie access configured.
// Reason says why and which setting to use.
type NoBrowserError struct{ Reason string }

func (e *NoBrowserError) Error() string { return "the library needs your YouTube account: " + e.Reason }

// CookieConsenter is an optional extra of a Source: it records the user's agreement to let the Source read their
// browser's cookies, which the library needs. The UI calls it only after the user pressed "allow", with the browser the
// prompt named (NeedsConsentError.Browser). When that is no longer the browser in use, nothing is recorded and the error
// is a *NeedsConsentError for the browser in use now.
type CookieConsenter interface {
	GrantCookieAccess(ctx context.Context, browser string) error
}

// Enricher is an optional extra of a Source: it fills in what a listing leaves out (artist, album, duration) for one
// track, which costs a request, so it is asked for the track a person is looking at. Source itself is unchanged; a UI
// uses an Enricher when the Source it has is one.
type Enricher interface {
	Enrich(ctx context.Context, t Track) (Track, error)
}

// TrackPager is an optional extra of a Source: one collection's tracks a page at a time, so a long one (hundreds of liked
// songs) shows its first rows at once and fetches the rest as the cursor nears the end. from is the index of the first track
// wanted, n the page size; more reports that tracks may follow. Source.Tracks still lists everything.
type TrackPager interface {
	TracksPage(ctx context.Context, id string, from, n int) (tracks []Track, more bool, err error)
}

// Refresher is an optional extra of a Source that remembers the account's listings for the session: Refresh forgets them, so
// the next listing asks the account again (the Library tab's r key).
type Refresher interface {
	Refresh()
}

// LibraryCache is an optional extra of a Source that keeps what it learned of the library (metadata only: IDs, titles, artists,
// album, length, thumbnail URL) on disk between sessions, so the Library tab can show it at once and refresh it behind the
// rows (stale while revalidate). A Source returns false when it has nothing, or when reading the account is no longer allowed.
type LibraryCache interface {
	CachedLibrary(ctx context.Context) ([]Collection, bool)
	// CachedTracks returns the first n remembered tracks of a collection, and whether more are known to follow.
	CachedTracks(ctx context.Context, id string, n int) (tracks []Track, more bool, ok bool)
}

// Artwork is an optional extra of a Source or of anything beside it: the cover picture of a track, from its thumbnail. It is
// fetched over plain HTTPS with no cookies. Source and Player are unchanged.
type Artwork interface {
	Image(ctx context.Context, t Track) (image.Image, error)
}

// Level is the loudness of what is playing right now: RMS and peak, linear, 0 (silence) to 1 (full scale).
type Level struct{ RMS, Peak float64 }

// Levels is an optional extra of a Player: the real loudness of the audio, for visuals that move with the music. Measuring
// costs a little work, so it is off until SetLevels(true) and should be switched off when nobody is looking. Player is unchanged.
type Levels interface {
	// SetLevels starts or stops measuring; asking for what already is, is not an error.
	SetLevels(ctx context.Context, on bool) error
	// Level is the latest measurement. ok is false when there is none: not measuring, nothing playing, or the player cannot.
	Level(ctx context.Context) (l Level, ok bool)
}

// State is what the player is doing.
type State struct {
	// Connected is false when the player process is not running.
	Connected bool
	// Track is the current track, nil when nothing is loaded.
	Track    *Track
	Position time.Duration
	Duration time.Duration
	Paused   bool
	Volume   int // 0..100
	Queue    []Track
	Index    int // position of Track in Queue, -1 when none
	// Shuffle and Repeat are the play modes, when the Player has them (see Modes); the zero values are off.
	Shuffle bool
	Repeat  RepeatMode
}

// RepeatMode is what happens at the end of a track or of the queue.
type RepeatMode string

const (
	RepeatOff RepeatMode = ""
	RepeatAll RepeatMode = "all" // the queue starts over when it ends
	RepeatOne RepeatMode = "one" // the current track starts over when it ends
)

// Modes is an optional extra of a Player: shuffle and repeat. The UI offers the keys only when the Player has it.
type Modes interface {
	// SetShuffle reorders the rest of the queue at random (on) or puts the order back (off); the current track keeps playing.
	SetShuffle(ctx context.Context, on bool) error
	SetRepeat(ctx context.Context, mode RepeatMode) error
}

// Player is what makes sound. Every method may be called from any goroutine.
type Player interface {
	// Attach connects to a running player, or starts one.
	Attach(ctx context.Context) error
	// Replace sets a new queue and starts playing it at startAt.
	Replace(ctx context.Context, tracks []Track, startAt int) error
	Enqueue(ctx context.Context, tracks ...Track) error
	Remove(ctx context.Context, index int) error
	// Move puts the entry at from at position to.
	Move(ctx context.Context, from, to int) error
	Jump(ctx context.Context, index int) error
	Next(ctx context.Context) error
	Prev(ctx context.Context) error
	TogglePause(ctx context.Context) error
	SetPaused(ctx context.Context, paused bool) error
	SeekRelative(ctx context.Context, d time.Duration) error
	SetVolume(ctx context.Context, v int) error
	// State is the last state the player reported.
	State() State
	// Sync reads the live state from the player and returns it.
	Sync(ctx context.Context) (State, error)
	// Subscribe delivers the state after every change. A slow reader sees the
	// latest state, not every state. The channel closes with the player.
	Subscribe() <-chan State
	// Close drops the connection and leaves the player process running.
	Close() error
	// Shutdown stops the player process. Only an explicit request calls it.
	Shutdown(ctx context.Context) error
}
