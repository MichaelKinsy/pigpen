package art

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// rev-pig-music-m7: "HTTPS only" must hold after a redirect too. The default client follows a redirect from an HTTPS
// thumbnail to plain HTTP, which would fetch the picture in the clear.
func TestARedirectToPlainHTTPIsNotFollowed(t *testing.T) {
	var plainHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plainHits.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(jpegBytes(t, 8, 8))
	}))
	t.Cleanup(plain.Close)
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/x.jpg", http.StatusFound)
	}))
	t.Cleanup(tls.Close)
	c := &Cache{Dir: t.TempDir(), Client: tls.Client(), baseOverride: tls.URL}
	if _, err := c.Image(context.Background(), music.Track{ID: vid}); err == nil {
		t.Error("a thumbnail reached over plain HTTP was accepted")
	}
	if n := plainHits.Load(); n != 0 {
		t.Errorf("the plain HTTP server was asked %d times", n)
	}
}
