// Package prefetch makes the gap between two tracks short. mpv resolves each queue entry only when it becomes current, by
// running yt-dlp (3 to 5 seconds, measured). While a track plays, Warm runs that same yt-dlp command for the entry after it and
// keeps its answer; Shim is the program mpv is told to use as its yt-dlp (ytdl_hook-ytdl_path), which answers the hook from the
// kept answer and otherwise runs the real yt-dlp. Either answer reaches mpv only once YouTube serves its stream (yt-dlp's
// available_at; see holdAwk). Queue entries stay the plain watch URLs, so the queue, its metadata and the native engine are
// untouched.
//
// What is kept is yt-dlp's listing of one public video: stream URLs that YouTube signed for this IP address for about six
// hours. It carries no cookie (a stream resolve never has one) and sits in a 0700 directory, in files of mode 0600, for at most
// MaxAge, and never past an hour before the first of its stream URLs expires. Nothing here reads an account.
package prefetch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/internal/lazyre"
)

// Runner runs yt-dlp; ytdlp.ExecRunner has this shape.
type Runner func(ctx context.Context, bin string, args []string) (stdout, stderr []byte, err error)

// Cache is the kept answers of a yt-dlp, in Dir.
type Cache struct {
	Dir string // 0700; made when first needed
	Bin string // the real yt-dlp
	// JSRuntime is the doctor's JavaScript runtime for yt-dlp ("" lets yt-dlp choose), the one mpv's hook is given
	// (--ytdl-raw-options-append=js-runtimes=...): the prefetch runs the command mpv would run.
	JSRuntime string
	Runner    Runner        // a test's; required for Warm
	MaxAge    time.Duration // default 1 h: half the age at which R4 still found stream URLs served (2 h; they expire after 6)
	MaxFiles  int           // default 8
	Now       func() time.Time

	mu       sync.Mutex
	inflight map[string]chan struct{}
}

const (
	defaultMaxAge   = time.Hour
	defaultMaxFiles = 8
	maxAnswer       = 8 << 20
	// expiryMargin is how long before the first of its stream URLs expires an answer stops being used: a track that starts
	// then still has an hour to play, and to be played again.
	expiryMargin = time.Hour
	// maxHold is the longest the shim holds an answer whose stream YouTube does not serve yet (available_at): a pre-roll ad's
	// skip offset (5 s, measured) or its length. One held back longer is not waited for.
	maxHold = time.Minute
)

var idRe = lazyre.New(`^[A-Za-z0-9_-]{6,32}$`)

// ID is the video ID of a YouTube watch URL, "" for anything else (it names the kept file, so it is strict).
func ID(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Host != "music.youtube.com" && u.Host != "www.youtube.com") || u.Path != "/watch" {
		return ""
	}
	if id := u.Query().Get("v"); idRe.MatchString(id) {
		return id
	}
	return ""
}

func (c *Cache) maxAge() time.Duration {
	if c.MaxAge > 0 {
		return c.MaxAge
	}
	return defaultMaxAge
}

func (c *Cache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Cache) file(id string) string { return filepath.Join(c.Dir, id+".json") }

// Has reports whether a fresh answer for the watch URL is kept.
func (c *Cache) Has(raw string) bool {
	id := ID(raw)
	if id == "" {
		return false
	}
	st, err := os.Stat(c.file(id))
	return err == nil && st.Size() > 0 && c.now().Sub(st.ModTime()) < c.maxAge()
}

// hookArgs are the arguments mpv 0.41's yt-dlp hook passes for ytdl-format=bestaudio, which is what the player sets, with the
// JavaScript runtime the player gives it (recorded from a real mpv 0.41.0; mpv 0.37 asks for the subtitles with --all-subs,
// which yt-dlp reads the same way).
func hookArgs(raw, jsRuntime string) []string {
	args := []string{"--no-warnings", "-J", "--flat-playlist", "--sub-format", "ass/srt/best", "--format", "bestaudio",
		"--ignore-config", "--sub-langs", "all", "--write-srt", "--no-playlist"}
	if jsRuntime != "" {
		args = append(args, "--js-runtimes", jsRuntime)
	}
	return append(args, "--", raw)
}

// Warm runs yt-dlp for the watch URL and keeps its answer, unless a fresh one is kept or another Warm is running for it.
func (c *Cache) Warm(ctx context.Context, raw string) error {
	id := ID(raw)
	if id == "" {
		return fmt.Errorf("%q is not a YouTube watch URL", raw)
	}
	if c.Has(raw) {
		return nil
	}
	c.mu.Lock()
	if wait, busy := c.inflight[id]; busy {
		c.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
		}
		return nil
	}
	if c.inflight == nil {
		c.inflight = map[string]chan struct{}{}
	}
	done := make(chan struct{})
	c.inflight[id] = done
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.inflight, id)
		c.mu.Unlock()
		close(done)
	}()
	if c.Runner == nil {
		return errors.New("prefetch: no runner")
	}
	out, _, err := c.Runner(ctx, c.Bin, hookArgs(raw, c.JSRuntime))
	if err != nil {
		return fmt.Errorf("prefetching %s: %w", id, err)
	}
	if len(out) == 0 || len(out) > maxAnswer || !usable(out) {
		return fmt.Errorf("prefetching %s: yt-dlp gave no stream listing", id)
	}
	// The answer's age, which Has, prune and the shim's `find -mmin` all read from its mtime, is set so that it goes stale
	// MaxAge from now or expiryMargin before its first stream URL expires, whichever comes first.
	now := c.now()
	stale := now.Add(c.maxAge())
	if exp, ok := earliestExpiry(out); ok && exp.Add(-expiryMargin).Before(stale) {
		stale = exp.Add(-expiryMargin)
	}
	if !stale.After(now) {
		return fmt.Errorf("prefetching %s: its stream URLs expire within %s", id, expiryMargin)
	}
	born := stale.Add(-c.maxAge())
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(c.Dir, 0o700)
	tmp, err := os.CreateTemp(c.Dir, ".warm-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(out)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Chmod(tmp.Name(), 0o600) != nil || os.Chtimes(tmp.Name(), born, born) != nil || os.Rename(tmp.Name(), c.file(id)) != nil {
		_ = os.Remove(tmp.Name())
		return errors.New("prefetch: could not keep the answer")
	}
	c.prune()
	return nil
}

// earliestExpiry is the first expiry among the stream URLs of a yt-dlp listing: YouTube signs each with expire=<unix
// seconds> (expire/<seconds>/ in a manifest's path), after which it is refused. The listing is read as bytes, so the
// parameter is found whether the JSON escapes the & before it or not.
func earliestExpiry(out []byte) (time.Time, bool) {
	var first int64
	for rest := out; ; {
		i := bytes.Index(rest, []byte("expire"))
		if i < 0 {
			break
		}
		before, after := rest[:i], rest[i+len("expire"):]
		rest = after
		if len(after) == 0 || (after[0] != '=' && after[0] != '/') {
			continue
		}
		if !bytes.HasSuffix(before, []byte("?")) && !bytes.HasSuffix(before, []byte("&")) && !bytes.HasSuffix(before, []byte("/")) && !bytes.HasSuffix(before, []byte(`\u0026`)) {
			continue
		}
		n := 1
		for n < len(after) && n <= 12 && after[n] >= '0' && after[n] <= '9' {
			n++
		}
		if v, err := strconv.ParseInt(string(after[1:n]), 10, 64); err == nil && v > 0 && (first == 0 || v < first) {
			first = v
		}
	}
	if first == 0 {
		return time.Time{}, false
	}
	return time.Unix(first, 0), true
}

// usable is true for a yt-dlp listing of a video: JSON with formats or a URL.
func usable(out []byte) bool {
	var v struct {
		URL     string            `json:"url"`
		Formats []json.RawMessage `json:"formats"`
	}
	return json.Unmarshal(out, &v) == nil && (v.URL != "" || len(v.Formats) > 0)
}

// prune removes expired answers and, beyond MaxFiles, the oldest.
func (c *Cache) prune() {
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		return
	}
	type kept struct {
		path string
		at   time.Time
	}
	var keep []kept
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		p := filepath.Join(c.Dir, e.Name())
		if !strings.HasSuffix(e.Name(), ".json") || c.now().Sub(info.ModTime()) >= c.maxAge() {
			if strings.HasSuffix(e.Name(), ".json") || strings.HasSuffix(e.Name(), ".tmp") {
				_ = os.Remove(p)
			}
			continue
		}
		keep = append(keep, kept{p, info.ModTime()})
	}
	max := c.MaxFiles
	if max <= 0 {
		max = defaultMaxFiles
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].at.After(keep[j].at) })
	for i := max; i < len(keep); i++ {
		_ = os.Remove(keep[i].path)
	}
}

// Forget drops the kept answer for the watch URL: its stream was refused (the URLs are bound to the IP address that resolved
// them), so the next time the entry plays yt-dlp resolves it again. It reports whether a fresh answer was kept: one the shim
// would have given mpv.
func (c *Cache) Forget(raw string) bool {
	id := ID(raw)
	if id == "" {
		return false
	}
	kept := c.Has(raw)
	_ = os.Remove(c.file(id))
	return kept
}

// Clear removes every kept answer.
func (c *Cache) Clear() { _ = os.RemoveAll(c.Dir) }

// holdAwk prints the latest available_at (unix seconds, rounded up) of the requested_downloads of the yt-dlp listing on its
// input, and nothing when they have none. yt-dlp sets it when YouTube refuses a stream until then: tv_samsung, the first default
// client of yt-dlp 2026.10.10 and later, gets a pre-roll ad and a 403 until its skip offset has passed (5 s, measured). yt-dlp
// waits for it before it downloads, mpv master's hook before it opens the stream; mpv 0.41's hook does not, so the shim does.
// Only the requested streams count, as in mpv master: the listing's other formats come from other clients and may be held back
// longer. The array is found by scanning to its closing bracket outside strings, so that a title cannot end it early.
// A value in exponent form (1.79e+09; yt-dlp writes integers) reads as its mantissa, a time long past: it fails open (no hold).
const holdAwk = `{ s = s $0 "\n" }
END {
	i = index(s, "\"requested_downloads\":")
	if (!i) exit
	s = substr(s, i + 22)
	if (!sub(/^[ \n]*\[/, "", s)) exit
	n = length(s); depth = 1; str = 0
	for (i = 1; i <= n && depth > 0; i++) {
		c = substr(s, i, 1)
		if (str) { if (c == "\\") i++; else if (c == "\"") str = 0 }
		else if (c == "\"") str = 1
		else if (c == "[" || c == "{") depth++
		else if (c == "]" || c == "}") depth--
	}
	s = substr(s, 1, i); at = 0
	while (match(s, /"available_at": *[0-9.]+/)) {
		v = substr(s, RSTART, RLENGTH); sub(/^[^:]*: */, "", v); v += 0
		if (v > at) at = v
		s = substr(s, RSTART + RLENGTH)
	}
	if (at > 0) { w = int(at); if (w < at) w++; printf "%.0f\n", w }
}`

// Shim writes the program mpv runs as its yt-dlp, next to the answers, and returns its path. It is a shell script (not a
// binary): it answers mpv's "-J ... -- <watch URL>" from the kept answer when one younger than MaxAge is there (Warm dates an
// answer whose streams expire sooner back, so that this one age check also ends it expiryMargin before they do), and runs the
// real yt-dlp with the same arguments otherwise, so that the worst it can do is what mpv did without it. Either listing is
// held until its stream is available (holdAwk), for at most maxHold: a kept answer held back longer is resolved afresh, and a
// fresh one fails, saying so. The URL is only ever used after it has passed a strict check, and is never evaluated. Windows
// has no shell: Shim reports that, and mpv keeps using yt-dlp directly.
func (c *Cache) Shim() (string, error) {
	if runtime.GOOS == "windows" {
		return "", errors.New("no shell on Windows")
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return "", err
	}
	mins := int(c.maxAge() / time.Minute)
	script := fmt.Sprintf(`#!/bin/sh
# Written by pig-music. mpv runs this as its yt-dlp: a prefetched answer for a watch URL is returned as it is, and any listing
# once its stream is available; anything else goes to the real yt-dlp unchanged.
real=%[1]s
dir=%[2]s
# ready waits until the stream of the listing on its input is available (its available_at), or fails when that is more than
# %[4]d s away.
ready() {
  at=$(awk %[5]s)
  case "$at" in ''|*[!0-9]*) return 0;; esac
  wait=$((at - $(date +%%s)))
  [ "$wait" -gt 0 ] || return 0
  [ "$wait" -le %[4]d ] || return 1
  sleep "$wait"
}
for a in "$@"; do last=$a; done
list=
for a in "$@"; do [ "$a" = "-J" ] && list=1; done
[ -n "$list" ] || exec "$real" "$@"
case "$last" in
  https://music.youtube.com/watch\?v=*|https://www.youtube.com/watch\?v=*)
    id=${last#*v=}
    id=${id%%%%&*}
    case "$id" in
      ''|*[!A-Za-z0-9_-]*) ;;
      *)
        f="$dir/$id.json"
        if [ -s "$f" ] && [ -z "$(find "$f" -mmin +%[3]d 2>/dev/null)" ] && out=$(cat "$f") && printf '%%s\n' "$out" | ready; then
          printf '%%s\n' "$out"
          exit 0
        fi;;
    esac;;
esac
out=$("$real" "$@")
rc=$?
if [ "$rc" -eq 0 ] && ! printf '%%s\n' "$out" | ready; then
  echo "pig-music: YouTube holds this stream back for more than %[4]d s (available_at); not waiting for it" >&2
  exit 1
fi
[ -z "$out" ] || printf '%%s\n' "$out"
exit "$rc"
`, shQuote(c.Bin), shQuote(c.Dir), mins, int(maxHold/time.Second), shQuote(holdAwk))
	path := filepath.Join(c.Dir, "ytdl")
	tmp, err := os.CreateTemp(c.Dir, ".shim-*.tmp")
	if err != nil {
		return "", err
	}
	_, werr := tmp.WriteString(script)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Chmod(tmp.Name(), 0o700) != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
		return "", errors.New("could not write the yt-dlp shim")
	}
	return path, nil
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
