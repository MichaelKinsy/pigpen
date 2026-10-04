//go:build windows

package herdragentstate

import (
	"fmt"
	"net"
	"os"
	"time"
)

// dial opens herdr's endpoint on Windows, where herdr publishes a named pipe
// rather than a Unix socket path. There is no named-pipe client in the standard
// library, so the pipe path is opened as a file; a blocked read is ended by
// closing that file, which is what (*client).attempt does when its deadline
// expires.
func dial(endpoint string, timeout time.Duration) (net.Conn, error) {
	// A host that also exposes the socket path itself is answered by the Unix
	// dial, which is the only one of the two with a connect timeout.
	if conn, err := net.DialTimeout("unix", endpoint, timeout); err == nil {
		return conn, nil
	}
	file, err := os.OpenFile(endpoint, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open herdr pipe %s: %w", endpoint, err)
	}
	return pipeConn{File: file}, nil
}

// namedPipeHost reports that herdr publishes a named pipe here, so its socket
// path is a bare pipe name rather than a filesystem path.
func namedPipeHost() bool { return true }

// pipeConn adapts a named pipe to net.Conn. A pipe file carries no address and
// usually refuses deadlines, which is why (*client).attempt ends a blocked read
// by closing the connection instead.
type pipeConn struct{ *os.File }

func (pipeConn) LocalAddr() net.Addr              { return pipeAddr{} }
func (pipeConn) RemoteAddr() net.Addr             { return pipeAddr{} }
func (pipeConn) SetDeadline(time.Time) error      { return nil }
func (pipeConn) SetReadDeadline(time.Time) error  { return nil }
func (pipeConn) SetWriteDeadline(time.Time) error { return nil }

// pipeAddr names a pipe connection. Nothing in this extension reads it.
type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "herdr" }
