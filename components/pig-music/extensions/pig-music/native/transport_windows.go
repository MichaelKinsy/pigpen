//go:build windows

package native

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"path/filepath"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/Microsoft/go-winio"
)

// Endpoint is the daemon's named pipe, named after the runtime directory so that
// every agent directory has its own player.
func Endpoint(p music.Paths) string {
	sum := sha256.Sum256([]byte(filepath.Clean(p.Dir)))
	return `\\.\pipe\pig-music-` + hex.EncodeToString(sum[:8])
}

func LockPath(p music.Paths) string { return filepath.Join(p.Dir, "native.lock") }

// listen creates the pipe for the current user only (SYSTEM and administrators may also open it).
func listen(endpoint string) (net.Listener, error) {
	return winio.ListenPipe(endpoint, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;OW)(A;;GA;;;SY)(A;;GA;;;BA)",
		MessageMode:        false,
	})
}

func dial(ctx context.Context, endpoint string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, endpoint)
}

func cleanEndpoint(string) {}
