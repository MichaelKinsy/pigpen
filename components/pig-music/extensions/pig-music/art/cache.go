package art

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/MichaelKinsy/pigpen/pig-music/internal/lazyre"
	"image"
	_ "image/jpeg" // the decoders for the thumbnails
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

const (
	defaultMaxFile  = 2 << 20
	defaultMaxTotal = 20 << 20
	maxSide         = 4096 // a larger picture is refused before it is decoded
	ytimg           = "https://i.ytimg.com"
)

var idRe = lazyre.New(`^[A-Za-z0-9_-]{1,64}$`)

// Cache fetches thumbnails over HTTPS and keeps them, one file per track, in Dir. It never sends credentials. The
// zero value for MaxFile (2 MiB) and MaxTotal (20 MiB) means the default.
type Cache struct {
	Dir      string
	Client   *http.Client
	MaxFile  int64 // the largest accepted picture
	MaxTotal int64 // the cap on the whole directory; the oldest files go first

	baseOverride string // tests only: replaces https://i.ytimg.com
	mu           sync.Mutex
}

var _ music.Artwork = (*Cache)(nil)

// Image returns the cover of t. The track's own thumbnail is tried first when it is an HTTPS JPEG or PNG (a WebP one is not
// requested at all: the standard library cannot decode it), then the static 16:9 YouTube thumbnail of its video ID (mqdefault: unlike hqdefault it has no letterbox bars, so the centre square is the picture).
func (c *Cache) Image(ctx context.Context, t music.Track) (image.Image, error) {
	if !idRe.MatchString(t.ID) {
		return nil, errors.New("art: the track has no usable ID")
	}
	file := filepath.Join(c.Dir, t.ID+".img")
	if data, err := os.ReadFile(file); err == nil {
		if img, err := c.decode(data); err == nil {
			now := time.Now()
			_ = os.Chtimes(file, now, now) // a used file is a recent one
			return img, nil
		}
		_ = os.Remove(file) // corrupt: fetched again
	}
	var lastErr error
	for _, u := range c.candidates(t) {
		data, err := c.fetch(ctx, u)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		img, err := c.decode(data)
		if err != nil {
			lastErr = err
			continue
		}
		c.store(file, data)
		return img, nil
	}
	if lastErr == nil {
		lastErr = errors.New("art: no thumbnail to try")
	}
	return nil, lastErr
}

func (c *Cache) candidates(t music.Track) []string {
	var out []string
	if u, err := url.Parse(t.ArtURL); err == nil && u.Scheme == "https" && u.Host != "" {
		switch strings.ToLower(filepath.Ext(u.Path)) {
		case ".jpg", ".jpeg", ".png":
			u.RawQuery, u.Fragment = "", ""
			out = append(out, u.String())
		}
	}
	base := ytimg
	if c.baseOverride != "" {
		base = c.baseOverride
	}
	return append(out, base+"/vi/"+t.ID+"/mqdefault.jpg")
}

func (c *Cache) maxFile() int64 {
	if c.MaxFile > 0 {
		return c.MaxFile
	}
	return defaultMaxFile
}

// client is the HTTP client with a redirect rule: a redirect is followed only to another HTTPS URL, so "HTTPS only" holds
// for the whole fetch.
func (c *Cache) client() *http.Client {
	cl := http.Client{Timeout: 15 * time.Second} // no jar: no cookies
	if c.Client != nil {
		cl = *c.Client
	}
	cl.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return fmt.Errorf("art: a redirect to %s is not followed: thumbnails are fetched over HTTPS only", req.URL.Scheme)
		}
		if len(via) >= 5 {
			return errors.New("art: too many redirects")
		}
		return nil
	}
	return &cl
}

func (c *Cache) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	if !strings.HasPrefix(rawURL, "https://") {
		return nil, errors.New("art: only https thumbnails are fetched")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("art: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("art: thumbnail answered %s", resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/jpeg") && !strings.HasPrefix(ct, "image/png") {
		return nil, fmt.Errorf("art: thumbnail is %q, not a JPEG or PNG", ct)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, c.maxFile()+1))
	if err != nil {
		return nil, fmt.Errorf("art: %w", err)
	}
	if int64(len(data)) > c.maxFile() {
		return nil, fmt.Errorf("art: thumbnail is over %d bytes", c.maxFile())
	}
	return data, nil
}

func (c *Cache) decode(data []byte) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("art: %w", err)
	}
	if cfg.Width > maxSide || cfg.Height > maxSide || cfg.Width < 1 || cfg.Height < 1 {
		return nil, fmt.Errorf("art: a %dx%d thumbnail is not accepted", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("art: %w", err)
	}
	return img, nil
}

// store writes a validated picture atomically (0600, in a 0700 directory) and trims the directory to its cap. A failure to
// cache is not a failure to show the picture, so it is dropped.
func (c *Cache) store(file string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if os.MkdirAll(c.Dir, 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(c.Dir, ".art-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Chmod(tmp.Name(), 0o600) != nil || os.Rename(tmp.Name(), file) != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	c.prune()
}

func (c *Cache) prune() {
	limit := c.MaxTotal
	if limit <= 0 {
		limit = defaultMaxTotal
	}
	ents, err := os.ReadDir(c.Dir)
	if err != nil {
		return
	}
	type f struct {
		name string
		size int64
		mod  time.Time
	}
	var files []f
	var total int64
	for _, e := range ents {
		fi, err := e.Info()
		if err != nil || e.IsDir() || !strings.HasSuffix(e.Name(), ".img") {
			continue
		}
		files = append(files, f{e.Name(), fi.Size(), fi.ModTime()})
		total += fi.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, x := range files {
		if total <= limit {
			break
		}
		if os.Remove(filepath.Join(c.Dir, x.name)) == nil {
			total -= x.size
		}
	}
}
