package wax

import (
	"context"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
	"os"
	"path/filepath"
	"time"

	"github.com/colespringer/waxtap/v3"
)

// NewWaxClient builds the WaxTap client the engine resolves streams with, on
// WaxTap's default client chain (no player client is named here, and no PO-token
// provider, session or cookie is configured). cacheDir holds WaxTap's player
// cache; empty uses the user's cache directory. Logging is off: WaxTap's debug
// records carry stream URLs.
func NewWaxClient(cacheDir string) (*waxtap.Client, error) {
	native.SanitizeEnvironment() // WaxTap dumps raw responses, signed URLs included, to a directory these name
	if cacheDir == "" {
		if base, err := os.UserCacheDir(); err == nil {
			cacheDir = filepath.Join(base, "pig-music", "waxtap")
		}
	}
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			cacheDir = "" // fall back to WaxTap's own default rather than fail
		}
	}
	return waxtap.New(waxtap.Options{
		CacheDir: cacheDir,
		Timeouts: waxtap.Timeouts{
			Extraction: 20 * time.Second,
			Resolve:    15 * time.Second,
		},
	})
}

// WaxLister lists public playlists with WaxTap.
type WaxLister struct{ Client *waxtap.Client }

// Playlist lists the entries of a public playlist, at most 500.
func (l WaxLister) Playlist(ctx context.Context, id string) ([]native.PlaylistEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	pl, err := l.Client.Enumerate(ctx, "https://www.youtube.com/playlist?list="+id, waxtap.EnumerateOptions{MaxItems: 500})
	if err != nil {
		return nil, err
	}
	out := make([]native.PlaylistEntry, 0, len(pl.Entries))
	for _, e := range pl.Entries {
		out = append(out, native.PlaylistEntry{VideoID: e.VideoID, Title: e.Title, Author: e.Author, Duration: e.Duration})
	}
	return out, nil
}
