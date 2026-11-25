//go:build !windows

package ternwire

import (
	"context"
	"fmt"
	"net"
)

type unixSocketTransport struct {
	socketPath string
	conn       net.Conn
}

func newUnixSocketTransport(socketPath string) Transport {
	return &unixSocketTransport{socketPath: socketPath}
}

func (t *unixSocketTransport) Product() string      { return "Tern" }
func (t *unixSocketTransport) Peer() string         { return fmt.Sprintf("Tern daemon at %s", t.socketPath) }
func (t *unixSocketTransport) WaitsForEvents() bool { return false }
func (t *unixSocketTransport) GreetingHint() string {
	return "a Tern without omp's JSON protocol cannot read its hello (update Tern)"
}

func (t *unixSocketTransport) Connect(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", t.socketPath)
	if err != nil {
		return nil, &Error{
			Kind:    ErrConnect,
			Message: fmt.Sprintf("Cannot reach the %s: %s", t.Peer(), err.Error()),
		}
	}
	t.conn = conn
	return conn, nil
}
