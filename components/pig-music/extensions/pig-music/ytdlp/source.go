// Package ytdlp is a music.Source that asks yt-dlp. It runs yt-dlp for listings
// only (search and playlist contents, as JSON). Streams are resolved by mpv's
// own yt-dlp hook when a track plays, so stream URLs are never cached.
package ytdlp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/MichaelKinsy/pigpen/pig-music/internal/lazyre"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Runner runs yt-dlp. It returns stdout and stderr; err is non-nil when the
// program could not run or exited with a failure.
type Runner func(ctx context.Context, bin string, args []string) (stdout, stderr []byte, err error)

// Source is a music.Source backed by yt-dlp.
type Source struct {
	// Bin is the yt-dlp program; empty means "yt-dlp" from PATH.
	Bin string
	// Runner runs the program; ExecRunner when nil.
	Runner Runner
	// Timeout bounds one yt-dlp run; default 30 s.
	Timeout time.Duration
	// LibraryTimeout bounds one library listing, which pages through the whole collection (about 0.65 s per 100
	// entries measured); default 2 minutes.
	LibraryTimeout time.Duration
	// Cookies, when set, returns the --cookies-from-browser value for a library call, or an error that says why there is
	// none (*music.NeedsConsentError, *music.NoBrowserError). It is asked before every library call and never for anything
	// else: search, enrichment and streams carry no cookies.
	Cookies func(ctx context.Context) (spec string, err error)
	// JSRuntime, when set, is passed as --js-runtimes (yt-dlp's syntax: "deno", "node:/path/to/node"). YouTube needs a
	// JavaScript runtime for most formats; the doctor finds one.
	JSRuntime string
	// Inner, when set, answers Search first: YouTube Music's own search in one HTTP request, with artists, album and length
	// (the native package's Source). yt-dlp's songs listing took seconds and carried only titles, so every row then cost a
	// full page extraction. yt-dlp is the fallback when Inner fails or finds nothing. Only Search is asked of it.
	Inner music.Source
	// Account, when set, lists the library through YouTube Music's own browse endpoint, signed with the cookies it exports
	// once and keeps in memory (package account): the first rows of the liked songs come in one request instead of a yt-dlp
	// run that decrypts the browser's whole cookie store. It is asked after the consent check, like every library call, and
	// yt-dlp does the listing when the account refuses or answers in a shape it does not know.
	Account Account

	// CacheDir, when set, is where the library's metadata is kept between sessions (disk_cache.go): never a cookie.
	CacheDir string

	disk       diskState
	accountOff atomic.Bool   // the account refused this session: yt-dlp does the library until the user refreshes
	mem        libraryMemory // what the account answered this session (library_cache.go)
}

var _ music.Source = (*Source)(nil)

const (
	defaultTimeout = 30 * time.Second
	// DefaultLibraryTimeout is how long one library listing may take.
	DefaultLibraryTimeout = 2 * time.Minute
	defaultLimit          = 10
	maxLimit              = 50
	maxOutput             = 32 << 20
)

func (s *Source) bin() string {
	if s.Bin == "" {
		return "yt-dlp"
	}
	return s.Bin
}

func (s *Source) run(ctx context.Context, args ...string) ([]byte, error) {
	timeout := s.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	return s.runFor(ctx, timeout, args...)
}

func (s *Source) runFor(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	runner := s.Runner
	if runner == nil {
		runner = ExecRunner
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// --ignore-config keeps a user's yt-dlp.conf (output templates, quiet flags) from changing the JSON.
	full := append([]string{"--ignore-config", "--no-warnings", "--no-progress"}, args...)
	if s.JSRuntime != "" {
		full = append([]string{"--js-runtimes", s.JSRuntime}, full...)
	}
	stdout, stderr, err := runner(ctx, s.bin(), full)
	if err == nil {
		return stdout, nil
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return nil, &music.MissingError{Names: []string{"yt-dlp"}, GOOS: runtime.GOOS}
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("yt-dlp did not finish in %s: %w", timeout, ctx.Err())
	}
	if msg := lastLines(stderr, 3); msg != "" {
		return nil, fmt.Errorf("yt-dlp: %s", msg)
	}
	return nil, fmt.Errorf("yt-dlp failed: %w", err)
}

// lastLines returns the last n non-empty lines of b, joined, at most 600 bytes.
func lastLines(b []byte, n int) string {
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := strings.Join(lines, " | ")
	if len(out) > 600 {
		out = out[:600] + "..."
	}
	return out
}

// Search looks for tracks. With Inner it asks YouTube Music directly first. Otherwise it asks yt-dlp for the songs search and falls
// back to a plain YouTube search when that fails or finds nothing.
//
// The songs section (`#songs`) is used because the unfiltered search page starts
// with albums and playlists, which are not tracks. Its entries carry only an ID and
// a title: yt-dlp's flat listing has no artist or duration for them. The fallback
// lists channel and duration but also full movies and mixes.
func (s *Source) Search(ctx context.Context, query string, limit int) ([]music.Track, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search: the query is empty")
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)

	var ierr error
	if s.Inner != nil {
		var tracks []music.Track
		tracks, ierr = s.Inner.Search(ctx, query, limit)
		if ierr == nil && len(tracks) > 0 {
			return trim(tracks, limit), nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	tracks, err := s.SearchViaYtdlp(ctx, query, limit)
	return tracks, AlsoDirect(err, ierr)
}

// AlsoDirect adds why the direct YouTube Music listing failed to the error of the way taken after it, so a listing that
// YouTube stopped answering is named rather than hidden behind yt-dlp's message. err stays what errors.Is/As see.
func AlsoDirect(err, direct error) error {
	if err == nil || direct == nil {
		return err
	}
	return fmt.Errorf("%w (YouTube Music's direct search failed first: %v)", err, direct)
}

// SearchViaYtdlp is Search without the direct listing: for a caller that has asked it already.
func (s *Source) SearchViaYtdlp(ctx context.Context, query string, limit int) ([]music.Track, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search: the query is empty")
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)
	n := strconv.Itoa(limit)

	out, err := s.run(ctx, "--flat-playlist", "--playlist-end", n, "-J", "--", "https://music.youtube.com/search?q="+url.QueryEscape(query)+"#songs")
	var missing *music.MissingError
	if errors.As(err, &missing) {
		return nil, err
	}
	if err == nil {
		tracks, perr := ParseTracks(out)
		if perr == nil && len(tracks) > 0 {
			return trim(tracks, limit), nil
		}
		err = perr
	}
	out, ferr := s.run(ctx, "--flat-playlist", "-J", "--", "ytsearch"+n+":"+query)
	if ferr != nil {
		return nil, joinErr(err, ferr)
	}
	tracks, perr := ParseTracks(out)
	if perr != nil {
		return nil, joinErr(err, perr)
	}
	return trim(tracks, limit), nil
}

func joinErr(first, second error) error {
	if first == nil {
		return second
	}
	return fmt.Errorf("%w (the fallback search failed too: %v)", first, second)
}

func trim(tracks []music.Track, limit int) []music.Track {
	if len(tracks) > limit {
		return tracks[:limit]
	}
	return tracks
}

var collectionID = lazyre.New(`^[A-Za-z0-9_-]{2,64}$`)

// Tracks lists the tracks of one of the account's collections by its ID. It is a library call: it reads the browser cookies.
func (s *Source) Tracks(ctx context.Context, id string) ([]music.Track, error) {
	if !collectionID.MatchString(id) {
		return nil, fmt.Errorf("%q is not a playlist ID", id)
	}
	tk, err := s.checkMemory(ctx)
	if err != nil {
		return nil, err
	}
	if known := s.known(id); known.complete {
		return known.tracks, nil
	}
	if all, ok := s.wholeFromAccount(ctx, id); ok {
		s.remember(tk, id, knownList{tracks: all, complete: true})
		s.saveFirstTracks(s.currentSpec(), id, all, false)
		return all, nil
	}
	out, err := s.libraryRun(ctx, "--flat-playlist", "-J", "--", "https://music.youtube.com/playlist?list="+id)
	if err != nil {
		return nil, err
	}
	tracks, err := ParseTracks(out)
	if err == nil {
		s.remember(tk, id, knownList{tracks: tracks, complete: true})
		s.saveFirstTracks(s.currentSpec(), id, tracks, false)
	}
	return tracks, err
}

// likedSongs is the liked-songs collection of YouTube Music.
var likedSongs = music.Collection{ID: "LM", Kind: "liked", Title: "Liked songs"}

// playlistsPage is the signed-in account's playlists, as yt-dlp's YouTube tab extractor reads it.
const playlistsPage = "https://www.youtube.com/feed/playlists"

// Library lists the signed-in account's collections: the liked songs, then its playlists. It reads the account through the
// browser cookies Cookies names, and only here and in Tracks.
func (s *Source) Library(ctx context.Context) ([]music.Collection, error) {
	tk, err := s.checkMemory(ctx)
	if err != nil {
		return nil, err
	}
	if cols, ok := s.rememberedCollections(); ok {
		return cols, nil
	}
	if cols, ok := s.collectionsFromAccount(ctx); ok {
		s.rememberCollections(tk, cols)
		return cols, nil
	}
	out, err := s.libraryRun(ctx, "--flat-playlist", "-J", "--", playlistsPage)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Entries []struct {
			ID    string  `json:"id"`
			Title *string `json:"title"`
			Count *int    `json:"playlist_count"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("the playlists listing is not JSON: %w", err)
	}
	cols := []music.Collection{likedSongs}
	for _, e := range doc.Entries {
		if e.Title == nil || *e.Title == "" || !collectionID.MatchString(e.ID) || e.ID == "LM" || e.ID == "LL" {
			continue // unavailable, untitled, or the two liked lists (LM is the first entry already)
		}
		c := music.Collection{ID: e.ID, Kind: "playlist", Title: *e.Title}
		if e.Count != nil {
			c.Count = *e.Count
		}
		cols = append(cols, c)
	}
	s.rememberCollections(tk, cols)
	return cols, nil
}

// libraryRun is a yt-dlp run that reads the user's browser cookies. The flag goes on library calls only.
func (s *Source) libraryRun(ctx context.Context, args ...string) ([]byte, error) {
	if s.Cookies == nil {
		return nil, &music.NoBrowserError{Reason: "cookie access is not configured"}
	}
	spec, err := s.Cookies(ctx)
	if err != nil {
		return nil, err
	}
	timeout := s.LibraryTimeout
	if timeout == 0 {
		timeout = DefaultLibraryTimeout
	}
	out, err := s.runFor(ctx, timeout, append([]string{"--cookies-from-browser", spec}, args...)...)
	if err != nil && strings.Contains(err.Error(), "Operation not permitted") && strings.Contains(err.Error(), "ookies") {
		// macOS keeps some browsers' cookie stores (Safari's above all) behind Full Disk Access.
		err = fmt.Errorf("%w. macOS blocked reading the browser's cookies: give the terminal app that runs PiG Full Disk Access (System Settings, Privacy & Security), or choose another browser with the cookieBrowser setting", err)
	}
	return out, err
}

// PlayURL is the URL mpv plays; mpv's yt-dlp hook resolves the stream.
func (s *Source) PlayURL(t music.Track) string {
	return "https://music.youtube.com/watch?v=" + url.QueryEscape(t.ID)
}

// ExecRunner runs yt-dlp as a child process, bounded by ctx, with stdout limited
// to 32 MiB. When ctx ends, yt-dlp and everything it started are killed.
func ExecRunner(ctx context.Context, bin string, args []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	ownGroup(cmd)
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitWriter{w: &stdout, left: maxOutput}
	cmd.Stderr = &limitWriter{w: &stderr, left: 1 << 20}
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// limitWriter stops accepting after left bytes, which fails the command with a clear error.
type limitWriter struct {
	w    *bytes.Buffer
	left int
}

var errTooLarge = errors.New("yt-dlp output is larger than 32 MiB")

func (l *limitWriter) Write(p []byte) (int, error) {
	if len(p) > l.left {
		return 0, errTooLarge
	}
	l.left -= len(p)
	return l.w.Write(p)
}
