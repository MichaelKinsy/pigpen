//go:build !windows

package herdragentstate

import (
	"net"
	"time"
)

// dial opens herdr's endpoint on the platforms where herdr listens on a Unix
// domain socket, bounded by timeout.
func dial(endpoint string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", endpoint, timeout)
}

// namedPipeHost reports that herdr publishes a named pipe here, so its socket
// path is a bare pipe name rather than a filesystem path.
func namedPipeHost() bool { return false }
