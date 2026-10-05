package art

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, solid(w, h, func(x, y int) color.Color { return red }), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, solid(8, 8, func(x, y int) color.Color { return blue })); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// server serves one body per path with a content type, counts requests, and hands back a client that trusts it.
type server struct {
	*httptest.Server
	hits  atomic.Int32
	paths []string
}

func newServer(t *testing.T, routes map[string]struct {
	typ  string
	body []byte
}) *server {
	t.Helper()
	s := &server{}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		s.paths = append(s.paths, r.URL.Path)
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			http.Error(w, "credentials sent", 400)
			return
		}
		rt, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", rt.typ)
		w.Write(rt.body)
	}))
	t.Cleanup(s.Close)
	return s
}

type route = struct {
	typ  string
	body []byte
}

const vid = "dQw4w9WgXcQ"

func newCache(t *testing.T, s *server) *Cache {
	t.Helper()
	c := &Cache{Dir: filepath.Join(t.TempDir(), "art"), Client: s.Client(), MaxFile: 1 << 20, MaxTotal: 10 << 20}
	c.baseOverride = s.URL // tests send the ytimg pattern to the test server
	return c
}

func TestAThumbnailIsFetchedDecodedAndCachedPerTrack(t *testing.T) {
	s := newServer(t, map[string]route{"/vi/" + vid + "/mqdefault.jpg": {"image/jpeg", jpegBytes(t, 32, 24)}})
	c := newCache(t, s)
	img, err := c.Image(context.Background(), music.Track{ID: vid})
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 24 {
		t.Errorf("bounds %v", b)
	}
	if _, err := c.Image(context.Background(), music.Track{ID: vid}); err != nil {
		t.Fatal(err)
	}
	if s.hits.Load() != 1 {
		t.Errorf("%d requests, want 1: the second answer must come from the cache", s.hits.Load())
	}
	// a second Cache on the same directory (a new session) uses the files
	c2 := &Cache{Dir: c.Dir, Client: s.Client(), MaxFile: 1 << 20, MaxTotal: 10 << 20, baseOverride: s.URL}
	if _, err := c2.Image(context.Background(), music.Track{ID: vid}); err != nil || s.hits.Load() != 1 {
		t.Errorf("err %v hits %d", err, s.hits.Load())
	}
	if fi, err := os.Stat(filepath.Join(c.Dir, vid+".img")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the cache file: %v %v", fi, err)
	}
}

func TestTheTracksOwnThumbnailIsUsedWhenItIsAJpegOrPng(t *testing.T) {
	s := newServer(t, map[string]route{"/cover/a.png": {"image/png", pngBytes(t)}})
	c := newCache(t, s)
	tr := music.Track{ID: vid, ArtURL: s.URL + "/cover/a.png?sqp=abc"}
	if _, err := c.Image(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.paths, ","); got != "/cover/a.png" {
		t.Errorf("requested %s", got)
	}
}

func TestAWebpOrUnknownThumbnailFallsBackToTheStaticYouTubeImage(t *testing.T) {
	s := newServer(t, map[string]route{"/vi/" + vid + "/mqdefault.jpg": {"image/jpeg", jpegBytes(t, 16, 16)}})
	c := newCache(t, s)
	tr := music.Track{ID: vid, ArtURL: s.URL + "/vi_webp/" + vid + "/maxresdefault.webp"}
	if _, err := c.Image(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.paths, ","); got != "/vi/"+vid+"/mqdefault.jpg" {
		t.Errorf("a webp URL must not even be requested: %s", got)
	}
}

func TestAFailingFirstCandidateFallsBackToTheNext(t *testing.T) {
	s := newServer(t, map[string]route{"/vi/" + vid + "/mqdefault.jpg": {"image/jpeg", jpegBytes(t, 16, 16)}})
	c := newCache(t, s)
	tr := music.Track{ID: vid, ArtURL: s.URL + "/missing.jpg"}
	if _, err := c.Image(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyHTTPSImagesOfTheRightTypeAndSizeAreAccepted(t *testing.T) {
	big := append([]byte{0xff, 0xd8}, bytes.Repeat([]byte{1}, 2<<20)...)
	s := newServer(t, map[string]route{
		"/vi/" + vid + "/mqdefault.jpg": {"text/html", []byte("<html>")},
		"/big.jpg":                      {"image/jpeg", big},
		"/junk.jpg":                     {"image/jpeg", []byte("not an image")},
	})
	c := newCache(t, s)
	for name, tr := range map[string]music.Track{
		"wrong content type": {ID: vid},
		"too big":            {ID: "bbbbbbbbbbb", ArtURL: s.URL + "/big.jpg"},
		"not decodable":      {ID: "jjjjjjjjjjj", ArtURL: s.URL + "/junk.jpg"},
		"plain http":         {ID: "hhhhhhhhhhh", ArtURL: strings.Replace(s.URL, "https", "http", 1) + "/x.jpg"},
		"no usable ID":       {ID: "../etc/passwd"},
	} {
		if _, err := c.Image(context.Background(), tr); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	ents, _ := os.ReadDir(c.Dir)
	for _, e := range ents {
		t.Errorf("a rejected image was cached: %s", e.Name())
	}
}

func TestAHugeImageIsRefusedBeforeItIsDecoded(t *testing.T) {
	var b bytes.Buffer
	jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, 9000, 9000)), &jpeg.Options{Quality: 1})
	s := newServer(t, map[string]route{"/vi/" + vid + "/mqdefault.jpg": {"image/jpeg", b.Bytes()}})
	c := newCache(t, s)
	c.MaxFile = 8 << 20
	if _, err := c.Image(context.Background(), music.Track{ID: vid}); err == nil {
		t.Error("a 9000x9000 image should be refused")
	}
}

func TestTheCacheStaysUnderItsSizeCapByDroppingTheOldest(t *testing.T) {
	routes := map[string]route{}
	ids := []string{"aaaaaaaaaaa", "bbbbbbbbbbb", "ccccccccccc", "ddddddddddd"}
	body := jpegBytes(t, 64, 64)
	for _, id := range ids {
		routes["/vi/"+id+"/mqdefault.jpg"] = route{"image/jpeg", body}
	}
	s := newServer(t, routes)
	c := newCache(t, s)
	c.MaxTotal = int64(len(body))*2 + 10 // room for two files
	for i, id := range ids {
		if _, err := c.Image(context.Background(), music.Track{ID: id}); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(time.Duration(i-10) * time.Minute)
		os.Chtimes(filepath.Join(c.Dir, id+".img"), old, old) // distinct ages, oldest first
	}
	ents, _ := os.ReadDir(c.Dir)
	var total int64
	names := []string{}
	for _, e := range ents {
		fi, _ := e.Info()
		total += fi.Size()
		names = append(names, e.Name())
	}
	if total > c.MaxTotal {
		t.Errorf("%d bytes in %v, cap %d", total, names, c.MaxTotal)
	}
	if _, err := os.Stat(filepath.Join(c.Dir, "ddddddddddd.img")); err != nil {
		t.Errorf("the newest must stay: %v", names)
	}
	if _, err := os.Stat(filepath.Join(c.Dir, "aaaaaaaaaaa.img")); err == nil {
		t.Errorf("the oldest should have gone: %v", names)
	}
}

func TestACorruptCacheFileIsRefetched(t *testing.T) {
	s := newServer(t, map[string]route{"/vi/" + vid + "/mqdefault.jpg": {"image/jpeg", jpegBytes(t, 16, 16)}})
	c := newCache(t, s)
	os.MkdirAll(c.Dir, 0o700)
	os.WriteFile(filepath.Join(c.Dir, vid+".img"), []byte("garbage"), 0o600)
	if _, err := c.Image(context.Background(), music.Track{ID: vid}); err != nil || s.hits.Load() != 1 {
		t.Errorf("err %v hits %d", err, s.hits.Load())
	}
}

func TestNoCredentialsAreEverSent(t *testing.T) {
	s := newServer(t, map[string]route{"/vi/" + vid + "/mqdefault.jpg": {"image/jpeg", jpegBytes(t, 16, 16)}})
	c := newCache(t, s)
	if _, err := c.Image(context.Background(), music.Track{ID: vid}); err != nil { // the server answers 400 on a Cookie or Authorization header
		t.Fatal(err)
	}
}

func TestACancelledContextStopsTheFetch(t *testing.T) {
	s := newServer(t, nil)
	c := newCache(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Image(ctx, music.Track{ID: vid}); err == nil {
		t.Error("no error")
	}
}
