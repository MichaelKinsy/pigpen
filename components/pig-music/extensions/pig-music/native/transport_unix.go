//go:build !windows

package native

import (
	"context"
	"net"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Endpoint is where the daemon of paths listens: a unix socket in the 0700 runtime directory.
func Endpoint(p music.Paths) string { return filepath.Join(p.Dir, "native.sock") }

// LockPath is the file the daemon holds a lock on for as long as it lives.
func LockPath(p music.Paths) string { return filepath.Join(p.Dir, "native.lock") }

func listen(endpoint string) (net.Listener, error) {
	l, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(endpoint, 0o600)
	return l, nil
}

func dial(ctx context.Context, endpoint string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", endpoint)
}

// cleanEndpoint removes a socket file nobody holds.
func cleanEndpoint(endpoint string) { _ = os.Remove(endpoint) }
