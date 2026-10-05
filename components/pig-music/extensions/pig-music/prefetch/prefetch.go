// Package prefetch makes the gap between two tracks short. mpv resolves each queue entry only when it becomes current, by
// running yt-dlp (3 to 5 seconds, measured). While a track plays, Warm runs that same yt-dlp command for the entry after it and
// keeps its answer; Shim is the program mpv is told to use as its yt-dlp (ytdl_hook-ytdl_path), which answers the hook from the
// kept answer and otherwise runs the real yt-dlp. Queue entries stay the plain watch URLs, so the queue, its metadata and the
// native engine are untouched.
//
// What is kept is yt-dlp's listing of one public video: stream URLs that YouTube signed for this IP address for about six
// hours. It carries no cookie (a stream resolve never has one) and sits in a 0700 directory, in files of mode 0600, for at most
// MaxAge. Nothing here reads an account.
package prefetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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
	MaxAge    time.Duration // default 4 h 30 min: stream URLs last about six hours
	MaxFiles  int           // default 8
	Now       func() time.Time

	mu       sync.Mutex
	inflight map[string]chan struct{}
}

const (
	defaultMaxAge   = 4*time.Hour + 30*time.Minute
	defaultMaxFiles = 8
	maxAnswer       = 8 << 20
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

// hookArgs are the arguments mpv 0.37's yt-dlp hook passes for ytdl-format=bestaudio, which is what the player sets, with the
// JavaScript runtime the player gives it.
func hookArgs(raw, jsRuntime string) []string {
	args := []string{"--no-warnings", "-J", "--flat-playlist", "--sub-format", "ass/srt/best", "--format", "bestaudio",
		"--ignore-config", "--all-subs", "--no-playlist"}
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
	if werr != nil || cerr != nil || os.Chmod(tmp.Name(), 0o600) != nil || os.Rename(tmp.Name(), c.file(id)) != nil {
		_ = os.Remove(tmp.Name())
		return errors.New("prefetch: could not keep the answer")
	}
	c.prune()
	return nil
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
// them), so the next time the entry plays yt-dlp resolves it again.
func (c *Cache) Forget(raw string) {
	if id := ID(raw); id != "" {
		_ = os.Remove(c.file(id))
	}
}

// Clear removes every kept answer.
func (c *Cache) Clear() { _ = os.RemoveAll(c.Dir) }

// Shim writes the program mpv runs as its yt-dlp, next to the answers, and returns its path. It is a shell script (not a
// binary): it answers mpv's "-J ... -- <watch URL>" from the kept answer when one younger than MaxAge is there, and runs the
// real yt-dlp with the same arguments otherwise, so that the worst it can do is what mpv did without it. The URL is only
// ever used after it has passed a strict check, and is never evaluated. Windows has no shell: Shim reports that, and mpv
// keeps using yt-dlp directly.
func (c *Cache) Shim() (string, error) {
	if runtime.GOOS == "windows" {
		return "", errors.New("no shell on Windows")
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return "", err
	}
	mins := int(c.maxAge() / time.Minute)
	script := fmt.Sprintf(`#!/bin/sh
# Written by pig-music. mpv runs this as its yt-dlp: a prefetched answer for a watch URL is returned as it is, anything else
# goes to the real yt-dlp unchanged.
real=%s
dir=%s
for a in "$@"; do last=$a; done
list=
for a in "$@"; do [ "$a" = "-J" ] && list=1; done
if [ -n "$list" ]; then
  case "$last" in
    https://music.youtube.com/watch\?v=*|https://www.youtube.com/watch\?v=*)
      id=${last#*v=}
      id=${id%%%%&*}
      case "$id" in
        ''|*[!A-Za-z0-9_-]*) ;;
        *)
          f="$dir/$id.json"
          if [ -s "$f" ] && [ -z "$(find "$f" -mmin +%d 2>/dev/null)" ]; then
            exec cat "$f"
          fi;;
      esac;;
  esac
fi
exec "$real" "$@"
`, shQuote(c.Bin), shQuote(c.Dir), mins)
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
