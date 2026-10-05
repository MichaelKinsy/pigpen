package native

import (
	"github.com/MichaelKinsy/pigpen/pig-music/internal/lazyre"
	"net"
	"net/http"
	"time"
)

var videoIDRe = lazyre.New(`^[A-Za-z0-9_-]{11}$`)

// ValidVideoID reports whether id has the shape of a YouTube video ID.
func ValidVideoID(id string) bool { return videoIDRe.MatchString(id) }

// NewHTTPClient is the client streams are read with: proxy from the
// environment, bounded dialing and headers; per-request time limits are the
// range reader's.
func NewHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       60 * time.Second,
		ForceAttemptHTTP2:     true,
	}}
}

// SharedHTTPClient is the one client (one transport) of the direct YouTube Music requests, search and library alike.
func SharedHTTPClient() *http.Client { return sharedClient() }
